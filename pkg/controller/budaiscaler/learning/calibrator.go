/*
Copyright 2024 Bud Studio.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package learning

import (
	"context"
	"math"
	"sync"
	"time"
)

// AdaptiveCalibrator adjusts learning parameters based on accuracy
type AdaptiveCalibrator struct {
	mu sync.RWMutex

	accuracyTracker  *PredictionAccuracyTracker
	seasonalProfiles *SeasonalProfileManager

	// Calibration parameters
	minAccuracyThreshold float64       // 85% direction accuracy target
	maxMAPE              float64       // 20% MAPE target
	calibrationInterval  time.Duration // Minimum time between calibrations
	lastCalibration      time.Time

	// Adaptation rates
	alphaAdjustmentRate float64 // How much to adjust alpha per calibration

	// Calibration history
	calibrationCount int
	lastAlphaChange  float64
}

// NewAdaptiveCalibrator creates a new adaptive calibrator
func NewAdaptiveCalibrator(
	accuracyTracker *PredictionAccuracyTracker,
	seasonalProfiles *SeasonalProfileManager,
) *AdaptiveCalibrator {
	return &AdaptiveCalibrator{
		accuracyTracker:      accuracyTracker,
		seasonalProfiles:     seasonalProfiles,
		minAccuracyThreshold: MinAccuracyThreshold,
		maxMAPE:              MaxMAPE,
		calibrationInterval:  5 * time.Minute, // Reduced for faster feedback
		alphaAdjustmentRate:  0.1,             // 10% adjustment per calibration
	}
}

// ShouldCalibrate returns true if calibration should be performed
func (c *AdaptiveCalibrator) ShouldCalibrate() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Check time since last calibration
	if time.Since(c.lastCalibration) < c.calibrationInterval {
		return false
	}

	// Check if accuracy tracker says calibration is needed
	if c.accuracyTracker.IsCalibrationNeeded() {
		return true
	}

	// Check if accuracy is below threshold
	report := c.accuracyTracker.GetAccuracyReport()
	if report.TotalPredictions < 20 {
		return false // Not enough data
	}

	if report.OverallMAPE > c.maxMAPE {
		return true
	}

	if report.DirectionAccuracy < c.minAccuracyThreshold {
		return true
	}

	return false
}

// Calibrate performs automatic calibration if needed
func (c *AdaptiveCalibrator) Calibrate(ctx context.Context) CalibrationResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	report := c.accuracyTracker.GetAccuracyReport()

	result := CalibrationResult{
		Time:         time.Now(),
		PreviousMAE:  report.OverallMAE,
		PreviousMAPE: report.OverallMAPE,
	}

	// Check if we should skip
	if report.TotalPredictions < 20 {
		result.Skipped = true
		return result
	}

	c.calibrationCount++

	// Get current alpha
	profiles := c.seasonalProfiles.GetAllProfiles()
	var currentAlpha float64
	if len(profiles) > 0 {
		for _, p := range profiles {
			currentAlpha = p.GetAlpha()
			break
		}
	} else {
		currentAlpha = DefaultAlpha
	}

	// Adjust alpha based on error patterns
	newAlpha := currentAlpha
	alphaAdjusted := false

	if report.OverallMAPE > c.maxMAPE {
		// High error -> increase responsiveness (higher alpha)
		// Higher alpha means more weight on recent observations
		newAlpha = math.Min(0.5, currentAlpha*(1+c.alphaAdjustmentRate))
		alphaAdjusted = true
	} else if report.OverallMAPE < c.maxMAPE*0.5 {
		// Low error -> can decrease responsiveness for stability
		// Lower alpha means more stable, slower to change
		newAlpha = math.Max(0.05, currentAlpha*(1-c.alphaAdjustmentRate*0.5))
		alphaAdjusted = true
	}

	if alphaAdjusted {
		c.seasonalProfiles.SetAlpha(newAlpha)
		result.AlphaAdjusted = true
		result.NewAlpha = newAlpha
		c.lastAlphaChange = newAlpha - currentAlpha
	}

	// Identify and reset underperforming time slots
	for _, slot := range report.WorstTimeSlots {
		// Only reset buckets with significantly bad performance
		if slot.MAPE > c.maxMAPE*2 && slot.Count > 10 {
			// Reset the seasonal profile bucket
			for _, profile := range profiles {
				profile.ResetBucket(slot.Hour, slot.DayOfWeek)
			}
			// Reset accuracy bucket
			c.accuracyTracker.ResetBucket(slot.Hour, slot.DayOfWeek)
			result.BucketsReset = append(result.BucketsReset, slot)
		}
	}

	// Clear calibration flag
	c.accuracyTracker.ResetCalibrationFlag()
	c.lastCalibration = time.Now()

	return result
}

// ForceCalibrate forces a calibration regardless of timing
func (c *AdaptiveCalibrator) ForceCalibrate(ctx context.Context) CalibrationResult {
	c.mu.Lock()
	c.lastCalibration = time.Time{} // Reset to allow calibration
	c.mu.Unlock()

	return c.Calibrate(ctx)
}

// GetCalibrationStats returns calibration statistics
func (c *AdaptiveCalibrator) GetCalibrationStats() CalibrationStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return CalibrationStats{
		CalibrationCount:     c.calibrationCount,
		LastCalibration:      c.lastCalibration,
		LastAlphaChange:      c.lastAlphaChange,
		MinAccuracyThreshold: c.minAccuracyThreshold,
		MaxMAPE:              c.maxMAPE,
		CalibrationInterval:  c.calibrationInterval,
	}
}

// CalibrationStats contains calibration statistics
type CalibrationStats struct {
	CalibrationCount     int           `json:"calibrationCount"`
	LastCalibration      time.Time     `json:"lastCalibration"`
	LastAlphaChange      float64       `json:"lastAlphaChange"`
	MinAccuracyThreshold float64       `json:"minAccuracyThreshold"`
	MaxMAPE              float64       `json:"maxMAPE"`
	CalibrationInterval  time.Duration `json:"calibrationInterval"`
}

// SetMinAccuracyThreshold updates the minimum accuracy threshold
func (c *AdaptiveCalibrator) SetMinAccuracyThreshold(threshold float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if threshold > 0 && threshold <= 1 {
		c.minAccuracyThreshold = threshold
	}
}

// SetMaxMAPE updates the maximum MAPE threshold
func (c *AdaptiveCalibrator) SetMaxMAPE(mape float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if mape > 0 {
		c.maxMAPE = mape
	}
}

// SetCalibrationInterval updates the calibration interval
func (c *AdaptiveCalibrator) SetCalibrationInterval(interval time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if interval > 0 {
		c.calibrationInterval = interval
	}
}

// GetLastCalibration returns the time of the last calibration
func (c *AdaptiveCalibrator) GetLastCalibration() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastCalibration
}

// AnalyzePerformance provides a detailed analysis of prediction performance
func (c *AdaptiveCalibrator) AnalyzePerformance() PerformanceAnalysis {
	c.mu.RLock()
	defer c.mu.RUnlock()

	report := c.accuracyTracker.GetAccuracyReport()

	analysis := PerformanceAnalysis{
		OverallScore:      c.calculateOverallScore(report),
		MAPEStatus:        c.getMAPEStatus(report.OverallMAPE),
		DirectionStatus:   c.getDirectionStatus(report.DirectionAccuracy),
		TrendStatus:       c.getTrendStatus(report.RecentTrend),
		Recommendations:   c.generateRecommendations(report),
		CalibrationNeeded: report.CalibrationNeeded,
	}

	return analysis
}

// PerformanceAnalysis contains detailed performance analysis
type PerformanceAnalysis struct {
	OverallScore      float64  `json:"overallScore"`    // 0-100
	MAPEStatus        string   `json:"mapeStatus"`      // "good", "moderate", "poor"
	DirectionStatus   string   `json:"directionStatus"` // "good", "moderate", "poor"
	TrendStatus       string   `json:"trendStatus"`     // "improving", "stable", "degrading"
	Recommendations   []string `json:"recommendations"`
	CalibrationNeeded bool     `json:"calibrationNeeded"`
}

func (c *AdaptiveCalibrator) calculateOverallScore(report AccuracyReport) float64 {
	// Score based on MAPE (50%) and direction accuracy (50%)
	mapeScore := 100.0 * math.Max(0, 1-(report.OverallMAPE/100))
	directionScore := 100.0 * report.DirectionAccuracy

	return (mapeScore + directionScore) / 2
}

func (c *AdaptiveCalibrator) getMAPEStatus(mape float64) string {
	if mape <= c.maxMAPE*0.5 {
		return "good"
	} else if mape <= c.maxMAPE {
		return "moderate"
	}
	return "poor"
}

func (c *AdaptiveCalibrator) getDirectionStatus(accuracy float64) string {
	if accuracy >= c.minAccuracyThreshold {
		return "good"
	} else if accuracy >= c.minAccuracyThreshold*0.8 {
		return "moderate"
	}
	return "poor"
}

func (c *AdaptiveCalibrator) getTrendStatus(trend float64) string {
	if trend > 0.1 {
		return "improving"
	} else if trend < -0.1 {
		return "degrading"
	}
	return "stable"
}

func (c *AdaptiveCalibrator) generateRecommendations(report AccuracyReport) []string {
	var recommendations []string

	if report.OverallMAPE > c.maxMAPE {
		recommendations = append(recommendations, "High prediction error - consider increasing alpha for faster adaptation")
	}

	if report.DirectionAccuracy < c.minAccuracyThreshold {
		recommendations = append(recommendations, "Low direction accuracy - review scaling thresholds")
	}

	if report.RecentTrend < -0.2 {
		recommendations = append(recommendations, "Accuracy degrading - manual calibration recommended")
	}

	if len(report.WorstTimeSlots) > 0 {
		recommendations = append(recommendations,
			"Consider adding schedule hints for problematic time slots")
	}

	if report.TotalPredictions < 100 {
		recommendations = append(recommendations, "Collecting more data - predictions will improve over time")
	}

	return recommendations
}
