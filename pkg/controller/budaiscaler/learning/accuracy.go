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
	"encoding/json"
	"math"
	"sort"
	"sync"
	"time"
)

// PredictionAccuracyTracker tracks and reports prediction accuracy
type PredictionAccuracyTracker struct {
	mu sync.RWMutex

	// Per time-bucket accuracy (168 buckets)
	hourlyMAE  [NumBuckets]RunningAverage // Mean Absolute Error
	hourlyMAPE [NumBuckets]RunningAverage // Mean Absolute Percentage Error
	hourlyRMSE [NumBuckets]RunningAverage // Root Mean Square Error

	// Directional accuracy (did we predict scale direction correctly?)
	directionAccuracy [NumBuckets]RunningAccuracy

	// Overall metrics
	overallMAE  RunningAverage
	overallMAPE RunningAverage

	// Recent predictions for trend analysis
	recentPredictions *RingBuffer
	maxRecentSize     int

	// Calibration tracking
	lastCalibration   time.Time
	calibrationNeeded bool

	// Total predictions made
	totalPredictions int
}

// NewPredictionAccuracyTracker creates a new accuracy tracker
func NewPredictionAccuracyTracker() *PredictionAccuracyTracker {
	tracker := &PredictionAccuracyTracker{
		recentPredictions: NewRingBuffer(100),
		maxRecentSize:     100,
	}

	// Initialize running averages with default alpha
	for i := 0; i < NumBuckets; i++ {
		tracker.hourlyMAE[i] = RunningAverage{Alpha: DefaultAlpha}
		tracker.hourlyMAPE[i] = RunningAverage{Alpha: DefaultAlpha}
		tracker.hourlyRMSE[i] = RunningAverage{Alpha: DefaultAlpha}
	}

	tracker.overallMAE = RunningAverage{Alpha: DefaultAlpha}
	tracker.overallMAPE = RunningAverage{Alpha: DefaultAlpha}

	return tracker
}

// RecordPredictionOutcome records the outcome of a prediction
func (t *PredictionAccuracyTracker) RecordPredictionOutcome(
	predictionTime time.Time,
	predicted int32,
	actual int32,
	predictedDirection ScaleDirection,
	actualDirection ScaleDirection,
) {
	t.mu.Lock()
	defer t.mu.Unlock()

	bucket := GetBucketIndex(predictionTime)
	t.totalPredictions++

	// Calculate errors
	errorVal := float64(predicted - actual)
	absError := math.Abs(errorVal)
	pctError := 0.0
	if actual > 0 {
		pctError = absError / float64(actual) * 100
	}
	squaredError := errorVal * errorVal

	// Update per-bucket running averages
	t.hourlyMAE[bucket].Update(absError)
	t.hourlyMAPE[bucket].Update(pctError)
	t.hourlyRMSE[bucket].Update(squaredError)

	// Direction accuracy
	directionCorrect := predictedDirection == actualDirection ||
		(predictedDirection == ScaleNone && actualDirection == ScaleNone) ||
		(predicted == actual)
	t.directionAccuracy[bucket].Update(directionCorrect)

	// Overall metrics
	t.overallMAE.Update(absError)
	t.overallMAPE.Update(pctError)

	// Store for trend analysis
	t.recentPredictions.Push(PredictionOutcome{
		Time:      predictionTime,
		Predicted: predicted,
		Actual:    actual,
		Error:     errorVal,
	})

	// Check if calibration needed (high error rate)
	if pctError > 25.0 {
		t.calibrationNeeded = true
	}
}

// GetAccuracyReport generates a comprehensive accuracy report
func (t *PredictionAccuracyTracker) GetAccuracyReport() AccuracyReport {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return AccuracyReport{
		OverallMAE:        t.overallMAE.Get(),
		OverallMAPE:       t.overallMAPE.Get(),
		DirectionAccuracy: t.calculateOverallDirectionAccuracy(),
		WorstTimeSlots:    t.findWorstTimeSlots(5),
		CalibrationNeeded: t.calibrationNeeded,
		TotalPredictions:  t.totalPredictions,
		RecentTrend:       t.calculateRecentTrend(),
		HourlyAccuracy:    t.getHourlyAccuracy(),
	}
}

// calculateOverallDirectionAccuracy calculates the overall direction accuracy
func (t *PredictionAccuracyTracker) calculateOverallDirectionAccuracy() float64 {
	var totalCorrect, totalPredictions int

	for i := 0; i < NumBuckets; i++ {
		totalCorrect += t.directionAccuracy[i].Correct
		totalPredictions += t.directionAccuracy[i].Total
	}

	if totalPredictions == 0 {
		return 0
	}
	return float64(totalCorrect) / float64(totalPredictions)
}

// findWorstTimeSlots identifies the time slots with the worst prediction accuracy
func (t *PredictionAccuracyTracker) findWorstTimeSlots(n int) []TimeSlotStats {
	type slotError struct {
		index int
		mape  float64
		count int
	}

	slots := make([]slotError, 0, NumBuckets)
	for i := 0; i < NumBuckets; i++ {
		if t.hourlyMAPE[i].Count > 0 {
			slots = append(slots, slotError{
				index: i,
				mape:  t.hourlyMAPE[i].Get(),
				count: t.hourlyMAPE[i].Count,
			})
		}
	}

	// Sort by MAPE descending
	sort.Slice(slots, func(i, j int) bool {
		return slots[i].mape > slots[j].mape
	})

	if n > len(slots) {
		n = len(slots)
	}

	result := make([]TimeSlotStats, n)
	for i := 0; i < n; i++ {
		hour, dayOfWeek := GetTimeFromBucketIndex(slots[i].index)
		result[i] = TimeSlotStats{
			Hour:      hour,
			DayOfWeek: dayOfWeek,
			MAE:       t.hourlyMAE[slots[i].index].Get(),
			MAPE:      slots[i].mape,
			Count:     slots[i].count,
		}
	}

	return result
}

// calculateRecentTrend calculates the trend in prediction accuracy
// Positive = improving, negative = degrading
func (t *PredictionAccuracyTracker) calculateRecentTrend() float64 {
	if t.recentPredictions.Len() < 20 {
		return 0
	}

	all := t.recentPredictions.GetAll()
	n := len(all)

	// Compare first half errors to second half errors
	var firstHalfMAPE, secondHalfMAPE float64
	var firstCount, secondCount int

	for i := 0; i < n/2; i++ {
		outcome := all[i].(PredictionOutcome)
		if outcome.Actual > 0 {
			firstHalfMAPE += math.Abs(outcome.Error) / float64(outcome.Actual) * 100
			firstCount++
		}
	}
	for i := n / 2; i < n; i++ {
		outcome := all[i].(PredictionOutcome)
		if outcome.Actual > 0 {
			secondHalfMAPE += math.Abs(outcome.Error) / float64(outcome.Actual) * 100
			secondCount++
		}
	}

	if firstCount == 0 || secondCount == 0 {
		return 0
	}

	firstAvg := firstHalfMAPE / float64(firstCount)
	secondAvg := secondHalfMAPE / float64(secondCount)

	if firstAvg == 0 {
		return 0
	}

	// Positive trend means error is decreasing (improving)
	// Scale to -1 to 1
	improvement := (firstAvg - secondAvg) / firstAvg
	return math.Max(-1.0, math.Min(1.0, improvement))
}

// getHourlyAccuracy returns per-bucket accuracy metrics
func (t *PredictionAccuracyTracker) getHourlyAccuracy() []HourlyAccuracy {
	result := make([]HourlyAccuracy, NumBuckets)

	for i := 0; i < NumBuckets; i++ {
		hour, dayOfWeek := GetTimeFromBucketIndex(i)
		result[i] = HourlyAccuracy{
			Hour:              hour,
			DayOfWeek:         dayOfWeek,
			MAE:               t.hourlyMAE[i].Get(),
			MAPE:              t.hourlyMAPE[i].Get(),
			DirectionAccuracy: t.directionAccuracy[i].Get(),
			DataPoints:        t.hourlyMAE[i].Count,
		}
	}

	return result
}

// GetMAE returns the overall Mean Absolute Error
func (t *PredictionAccuracyTracker) GetMAE() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.overallMAE.Get()
}

// GetMAPE returns the overall Mean Absolute Percentage Error
func (t *PredictionAccuracyTracker) GetMAPE() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.overallMAPE.Get()
}

// GetDirectionAccuracy returns the overall direction accuracy (0-1)
func (t *PredictionAccuracyTracker) GetDirectionAccuracy() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.calculateOverallDirectionAccuracy()
}

// GetBucketAccuracy returns accuracy for a specific time bucket
func (t *PredictionAccuracyTracker) GetBucketAccuracy(hour, dayOfWeek int) HourlyAccuracy {
	t.mu.RLock()
	defer t.mu.RUnlock()

	idx := dayOfWeek*24 + hour
	if idx < 0 || idx >= NumBuckets {
		return HourlyAccuracy{}
	}

	return HourlyAccuracy{
		Hour:              hour,
		DayOfWeek:         dayOfWeek,
		MAE:               t.hourlyMAE[idx].Get(),
		MAPE:              t.hourlyMAPE[idx].Get(),
		DirectionAccuracy: t.directionAccuracy[idx].Get(),
		DataPoints:        t.hourlyMAE[idx].Count,
	}
}

// IsCalibrationNeeded returns true if calibration is recommended
func (t *PredictionAccuracyTracker) IsCalibrationNeeded() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.calibrationNeeded
}

// ResetCalibrationFlag resets the calibration needed flag
func (t *PredictionAccuracyTracker) ResetCalibrationFlag() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.calibrationNeeded = false
	t.lastCalibration = time.Now()
}

// GetLastCalibration returns the time of the last calibration
func (t *PredictionAccuracyTracker) GetLastCalibration() time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastCalibration
}

// ResetBucket resets accuracy tracking for a specific bucket
func (t *PredictionAccuracyTracker) ResetBucket(hour, dayOfWeek int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	idx := dayOfWeek*24 + hour
	if idx >= 0 && idx < NumBuckets {
		t.hourlyMAE[idx] = RunningAverage{Alpha: DefaultAlpha}
		t.hourlyMAPE[idx] = RunningAverage{Alpha: DefaultAlpha}
		t.hourlyRMSE[idx] = RunningAverage{Alpha: DefaultAlpha}
		t.directionAccuracy[idx] = RunningAccuracy{}
	}
}

// GetTotalPredictions returns the total number of predictions recorded
func (t *PredictionAccuracyTracker) GetTotalPredictions() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.totalPredictions
}

// MarshalJSON implements json.Marshaler
func (t *PredictionAccuracyTracker) MarshalJSON() ([]byte, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	// Convert recent predictions to slice
	recentOutcomes := make([]PredictionOutcome, 0, t.recentPredictions.Len())
	for _, item := range t.recentPredictions.GetAll() {
		if outcome, ok := item.(PredictionOutcome); ok {
			recentOutcomes = append(recentOutcomes, outcome)
		}
	}

	return json.Marshal(struct {
		HourlyMAE         [NumBuckets]RunningAverage  `json:"hourlyMAE"`
		HourlyMAPE        [NumBuckets]RunningAverage  `json:"hourlyMAPE"`
		HourlyRMSE        [NumBuckets]RunningAverage  `json:"hourlyRMSE"`
		DirectionAccuracy [NumBuckets]RunningAccuracy `json:"directionAccuracy"`
		OverallMAE        RunningAverage              `json:"overallMAE"`
		OverallMAPE       RunningAverage              `json:"overallMAPE"`
		LastCalibration   time.Time                   `json:"lastCalibration"`
		CalibrationNeeded bool                        `json:"calibrationNeeded"`
		TotalPredictions  int                         `json:"totalPredictions"`
		RecentPredictions []PredictionOutcome         `json:"recentPredictions"`
	}{
		HourlyMAE:         t.hourlyMAE,
		HourlyMAPE:        t.hourlyMAPE,
		HourlyRMSE:        t.hourlyRMSE,
		DirectionAccuracy: t.directionAccuracy,
		OverallMAE:        t.overallMAE,
		OverallMAPE:       t.overallMAPE,
		LastCalibration:   t.lastCalibration,
		CalibrationNeeded: t.calibrationNeeded,
		TotalPredictions:  t.totalPredictions,
		RecentPredictions: recentOutcomes,
	})
}

// UnmarshalJSON implements json.Unmarshaler
func (t *PredictionAccuracyTracker) UnmarshalJSON(data []byte) error {
	var aux struct {
		HourlyMAE         [NumBuckets]RunningAverage  `json:"hourlyMAE"`
		HourlyMAPE        [NumBuckets]RunningAverage  `json:"hourlyMAPE"`
		HourlyRMSE        [NumBuckets]RunningAverage  `json:"hourlyRMSE"`
		DirectionAccuracy [NumBuckets]RunningAccuracy `json:"directionAccuracy"`
		OverallMAE        RunningAverage              `json:"overallMAE"`
		OverallMAPE       RunningAverage              `json:"overallMAPE"`
		LastCalibration   time.Time                   `json:"lastCalibration"`
		CalibrationNeeded bool                        `json:"calibrationNeeded"`
		TotalPredictions  int                         `json:"totalPredictions"`
		RecentPredictions []PredictionOutcome         `json:"recentPredictions"`
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.hourlyMAE = aux.HourlyMAE
	t.hourlyMAPE = aux.HourlyMAPE
	t.hourlyRMSE = aux.HourlyRMSE
	t.directionAccuracy = aux.DirectionAccuracy
	t.overallMAE = aux.OverallMAE
	t.overallMAPE = aux.OverallMAPE
	t.lastCalibration = aux.LastCalibration
	t.calibrationNeeded = aux.CalibrationNeeded
	t.totalPredictions = aux.TotalPredictions

	// Restore recent predictions
	t.recentPredictions = NewRingBuffer(t.maxRecentSize)
	for _, outcome := range aux.RecentPredictions {
		t.recentPredictions.Push(outcome)
	}

	return nil
}
