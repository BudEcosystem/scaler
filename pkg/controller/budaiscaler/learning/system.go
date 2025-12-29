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
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// FeedbackInput contains all inputs needed to record a scaling decision
type FeedbackInput struct {
	ScalerName          string
	Namespace           string
	Timestamp           time.Time
	CurrentReplicas     int32
	RecommendedReplicas int32
	PredictedReplicas   int32
	MetricSnapshots     map[string]float64
	LLMMetrics          LLMMetrics
	LookAheadMinutes    int
	PredictionSource    string
}

// LearningSystem coordinates all learning components
type LearningSystem struct {
	mu sync.RWMutex

	// Core components
	seasonalProfiles *SeasonalProfileManager
	patternDetector  *LLMPatternDetector
	accuracyTracker  *PredictionAccuracyTracker
	calibrator       *AdaptiveCalibrator

	// Feedback management
	feedbackBuffer  *RingBuffer // Pending outcome verifications
	feedbackHistory []FeedbackRecord

	// Persistence
	storage         *LearningStorage
	lastPersisted   time.Time
	persistInterval time.Duration
	dirty           bool

	// Configuration
	enabled        bool
	scalerName     string
	namespace      string
	historicalDays int
	lookAhead      time.Duration

	// Throttling - prevent rapid-fire prediction recording
	lastRecordedAt time.Time
	recordInterval time.Duration // Minimum interval between recordings
}

// NewLearningSystem creates a new learning system
func NewLearningSystem(
	c client.Client,
	scalerName, namespace string,
	ownerRef *metav1.OwnerReference,
) *LearningSystem {
	seasonalProfiles := NewSeasonalProfileManager(DefaultAlpha)
	accuracyTracker := NewPredictionAccuracyTracker()

	ls := &LearningSystem{
		seasonalProfiles: seasonalProfiles,
		patternDetector:  NewLLMPatternDetector(),
		accuracyTracker:  accuracyTracker,
		calibrator:       NewAdaptiveCalibrator(accuracyTracker, seasonalProfiles),
		feedbackBuffer:   NewRingBuffer(1000),
		feedbackHistory:  make([]FeedbackRecord, 0),
		storage:          NewLearningStorage(c, scalerName, namespace, ownerRef),
		persistInterval:  DefaultPersistInterval,
		enabled:          true,
		scalerName:       scalerName,
		namespace:        namespace,
		historicalDays:   7,
		lookAhead:        DefaultLookAhead,
		recordInterval:   10 * time.Second, // Only record one prediction per 10 seconds
	}

	return ls
}

// Initialize loads existing data from storage
func (ls *LearningSystem) Initialize(ctx context.Context) error {
	data, err := ls.storage.Load(ctx)
	if err != nil {
		return err
	}

	if data != nil {
		ls.mu.Lock()
		ls.seasonalProfiles = data.SeasonalProfiles
		ls.patternDetector = data.PatternDetector
		ls.accuracyTracker = data.AccuracyTracker
		// Recreate calibrator with loaded components
		ls.calibrator = NewAdaptiveCalibrator(ls.accuracyTracker, ls.seasonalProfiles)
		ls.mu.Unlock()

		klog.V(4).InfoS("Loaded learning data from storage",
			"scaler", ls.scalerName,
			"dataPoints", data.Metadata.DataPointCount,
			"lastUpdated", data.Metadata.LastUpdated)
	}

	return nil
}

// RecordScalingDecision records a scaling decision for future verification
func (ls *LearningSystem) RecordScalingDecision(ctx context.Context, input FeedbackInput) {
	ls.mu.Lock()
	defer ls.mu.Unlock()

	now := time.Now()
	if input.Timestamp.IsZero() {
		input.Timestamp = now
	}

	// Throttle: only record one prediction per interval to prevent buffer overflow
	if !ls.lastRecordedAt.IsZero() && now.Sub(ls.lastRecordedAt) < ls.recordInterval {
		// Still update seasonal profiles and pattern detector, just skip the pending verification
		// All metrics from MetricSnapshots are tracked generically - no hardcoded metric names
		for metric, value := range input.MetricSnapshots {
			ls.seasonalProfiles.Update(metric, input.Timestamp, value)
		}
		ls.patternDetector.UpdateHistory(input.MetricSnapshots, input.LLMMetrics)
		return
	}
	ls.lastRecordedAt = now

	// Create pending feedback record
	record := FeedbackRecord{
		Timestamp:           input.Timestamp,
		HourOfDay:           input.Timestamp.Hour(),
		DayOfWeek:           int(input.Timestamp.Weekday()),
		PredictedReplicas:   input.PredictedReplicas,
		RecommendedReplicas: input.RecommendedReplicas,
		ActualReplicas:      input.CurrentReplicas,
		MetricSnapshots:     input.MetricSnapshots,
		LLMMetrics:          input.LLMMetrics,
	}

	// Schedule outcome verification for T+LookAhead
	lookAhead := ls.lookAhead
	if input.LookAheadMinutes > 0 {
		lookAhead = time.Duration(input.LookAheadMinutes) * time.Minute
	}

	ls.feedbackBuffer.Push(PendingVerification{
		Record:     record,
		VerifyAt:   input.Timestamp.Add(lookAhead),
		ScalerName: input.ScalerName,
		Namespace:  input.Namespace,
	})

	// Update seasonal profiles with current metrics
	// All metrics from MetricSnapshots are tracked generically - no hardcoded metric names
	for metric, value := range input.MetricSnapshots {
		ls.seasonalProfiles.Update(metric, input.Timestamp, value)
	}

	// Update pattern detector with all available metrics
	ls.patternDetector.UpdateHistory(input.MetricSnapshots, input.LLMMetrics)

	ls.dirty = true
}

// VerifyPastPredictions checks predictions that are now verifiable
func (ls *LearningSystem) VerifyPastPredictions(ctx context.Context, actualReplicas int32) {
	ls.mu.Lock()
	defer ls.mu.Unlock()

	now := time.Now()
	verified := make([]PendingVerification, 0)
	toKeep := make([]interface{}, 0)

	// Process pending verifications
	for _, item := range ls.feedbackBuffer.GetAll() {
		pv, ok := item.(PendingVerification)
		if !ok {
			continue
		}

		if now.Before(pv.VerifyAt) {
			toKeep = append(toKeep, item)
			continue
		}

		verified = append(verified, pv)
	}

	// Only log when verifications actually happen (not on every check)
	if len(verified) > 0 {
		klog.V(4).InfoS("Predictions verified",
			"count", len(verified),
			"remaining", len(toKeep),
			"actualReplicas", actualReplicas)
	}

	// Rebuild buffer with remaining items
	ls.feedbackBuffer = NewRingBuffer(1000)
	for _, item := range toKeep {
		ls.feedbackBuffer.Push(item)
	}

	// Process verified predictions
	for _, pv := range verified {
		record := pv.Record
		record.OutcomeReplicas = actualReplicas

		// Calculate prediction error
		if actualReplicas > 0 {
			record.PredictionError = float64(record.PredictedReplicas-actualReplicas) / float64(actualReplicas)
		}
		record.WasOverProvisioned = record.PredictedReplicas > actualReplicas+1
		record.WasUnderProvisioned = record.PredictedReplicas < actualReplicas-1

		// Record for accuracy tracking
		predictedDir := GetDirection(record.ActualReplicas, record.PredictedReplicas)
		actualDir := GetDirection(record.ActualReplicas, actualReplicas)

		ls.accuracyTracker.RecordPredictionOutcome(
			record.Timestamp,
			record.PredictedReplicas,
			actualReplicas,
			predictedDir,
			actualDir,
		)

		klog.InfoS("Verified prediction",
			"predictedReplicas", record.PredictedReplicas,
			"actualReplicas", actualReplicas,
			"beforeReplicas", record.ActualReplicas,
			"predictedDir", predictedDir,
			"actualDir", actualDir,
			"error", record.PredictionError)

		// Learn from pattern
		pattern, confidence := ls.patternDetector.GetCurrentPattern()
		if confidence > 0.5 {
			successful := !record.WasOverProvisioned && !record.WasUnderProvisioned
			multiplier := 1.0
			if actualReplicas > 0 && record.ActualReplicas > 0 {
				multiplier = float64(actualReplicas) / float64(record.ActualReplicas)
			}
			ls.patternDetector.LearnPattern(pattern, successful, multiplier)
		}

		// Store completed record
		ls.feedbackHistory = append(ls.feedbackHistory, record)

		// Trim history if too long
		if len(ls.feedbackHistory) > DefaultMaxHistory {
			ls.feedbackHistory = ls.feedbackHistory[len(ls.feedbackHistory)-DefaultMaxHistory:]
		}
	}

	if len(verified) > 0 {
		ls.dirty = true

		// Trigger calibration if needed
		if ls.calibrator.ShouldCalibrate() {
			result := ls.calibrator.Calibrate(ctx)
			if !result.Skipped {
				klog.InfoS("Calibration performed",
					"scaler", ls.scalerName,
					"alphaAdjusted", result.AlphaAdjusted,
					"newAlpha", result.NewAlpha,
					"previousMAPE", result.PreviousMAPE,
					"bucketsReset", len(result.BucketsReset))
			}
		}
	}
}

// GetSeasonalFactor returns the seasonal factor for a metric at a given time
func (ls *LearningSystem) GetSeasonalFactor(metric string, t time.Time) (factor float64, confidence float64) {
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	return ls.seasonalProfiles.GetSeasonalFactor(metric, t)
}

// DetectPattern detects the current workload pattern
func (ls *LearningSystem) DetectPattern(llmMetrics LLMMetrics) LLMWorkloadPattern {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return ls.patternDetector.DetectPattern(llmMetrics)
}

// GetPatternAdjustment returns the recommended scaling adjustment
func (ls *LearningSystem) GetPatternAdjustment() *PatternAdjustment {
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	return ls.patternDetector.GetPatternAdjustment()
}

// GetAccuracyReport returns the current accuracy report
func (ls *LearningSystem) GetAccuracyReport() AccuracyReport {
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	return ls.accuracyTracker.GetAccuracyReport()
}

// ShouldPersist returns true if data should be persisted
func (ls *LearningSystem) ShouldPersist() bool {
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	return ls.dirty && time.Since(ls.lastPersisted) >= ls.persistInterval
}

// Persist saves learning data to storage
func (ls *LearningSystem) Persist(ctx context.Context) error {
	ls.mu.Lock()
	defer ls.mu.Unlock()

	// Count total data points
	dataPointCount := 0
	for _, profile := range ls.seasonalProfiles.GetAllProfiles() {
		dataPointCount += profile.GetTotalObservations()
	}

	data := &LearningData{
		SeasonalProfiles: ls.seasonalProfiles,
		PatternDetector:  ls.patternDetector,
		AccuracyTracker:  ls.accuracyTracker,
		Metadata: StorageMetadata{
			ScalerName:     ls.scalerName,
			Namespace:      ls.namespace,
			DataPointCount: dataPointCount,
			ProfilesCount:  len(ls.seasonalProfiles.GetAllProfiles()),
		},
	}

	if err := ls.storage.Save(ctx, data); err != nil {
		return err
	}

	ls.lastPersisted = time.Now()
	ls.dirty = false

	klog.V(4).InfoS("Persisted learning data",
		"scaler", ls.scalerName,
		"dataPoints", dataPointCount)

	return nil
}

// GetStatus returns the current learning status
func (ls *LearningSystem) GetStatus() LearningSystemStatus {
	ls.mu.RLock()
	defer ls.mu.RUnlock()

	report := ls.accuracyTracker.GetAccuracyReport()
	pattern, confidence := ls.patternDetector.GetCurrentPattern()

	// Count total data points
	dataPointCount := 0
	complete := true
	for _, profile := range ls.seasonalProfiles.GetAllProfiles() {
		dataPointCount += profile.GetTotalObservations()
		if !profile.IsComplete() {
			complete = false
		}
	}

	// Get storage size
	var storageSize int
	size, err := ls.storage.GetStorageSize(context.Background())
	if err == nil {
		storageSize = size
	}

	return LearningSystemStatus{
		Enabled:              ls.enabled,
		DataPointsCollected:  dataPointCount,
		OverallAccuracy:      report.DirectionAccuracy * 100,
		OverallMAE:           report.OverallMAE,
		OverallMAPE:          report.OverallMAPE,
		SeasonalDataComplete: complete,
		CurrentPattern:       string(pattern),
		PatternConfidence:    confidence,
		LastCalibration:      ls.calibrator.GetLastCalibration(),
		CalibrationNeeded:    report.CalibrationNeeded,
		ConfigMapName:        ls.storage.GetConfigMapName(),
		StorageSizeBytes:     storageSize,
		PendingVerifications: ls.feedbackBuffer.Len(),
		TotalPredictions:     report.TotalPredictions,
	}
}

// LearningSystemStatus contains the current status of the learning system
type LearningSystemStatus struct {
	Enabled              bool      `json:"enabled"`
	DataPointsCollected  int       `json:"dataPointsCollected"`
	OverallAccuracy      float64   `json:"overallAccuracy"` // Direction accuracy percentage
	OverallMAE           float64   `json:"overallMAE"`      // Mean Absolute Error
	OverallMAPE          float64   `json:"overallMAPE"`     // Mean Absolute Percentage Error
	SeasonalDataComplete bool      `json:"seasonalDataComplete"`
	CurrentPattern       string    `json:"currentPattern"`
	PatternConfidence    float64   `json:"patternConfidence"`
	LastCalibration      time.Time `json:"lastCalibration"`
	CalibrationNeeded    bool      `json:"calibrationNeeded"`
	ConfigMapName        string    `json:"configMapName"`
	StorageSizeBytes     int       `json:"storageSizeBytes"`
	PendingVerifications int       `json:"pendingVerifications"`
	TotalPredictions     int       `json:"totalPredictions"`
}

// SetEnabled enables or disables the learning system
func (ls *LearningSystem) SetEnabled(enabled bool) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	ls.enabled = enabled
}

// IsEnabled returns true if the learning system is enabled
func (ls *LearningSystem) IsEnabled() bool {
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	return ls.enabled
}

// SetLookAhead sets the prediction look-ahead duration
func (ls *LearningSystem) SetLookAhead(duration time.Duration) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	ls.lookAhead = duration
}

// SetHistoricalDays sets how many days of history to use
func (ls *LearningSystem) SetHistoricalDays(days int) {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if days > 0 && days <= 90 {
		ls.historicalDays = days
	}
}

// ForceCalibrate forces a calibration
func (ls *LearningSystem) ForceCalibrate(ctx context.Context) CalibrationResult {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return ls.calibrator.ForceCalibrate(ctx)
}

// GetPerformanceAnalysis returns detailed performance analysis
func (ls *LearningSystem) GetPerformanceAnalysis() PerformanceAnalysis {
	ls.mu.RLock()
	defer ls.mu.RUnlock()
	return ls.calibrator.AnalyzePerformance()
}

// Reset clears all learning data
func (ls *LearningSystem) Reset(ctx context.Context) error {
	ls.mu.Lock()
	defer ls.mu.Unlock()

	ls.seasonalProfiles = NewSeasonalProfileManager(DefaultAlpha)
	ls.patternDetector = NewLLMPatternDetector()
	ls.accuracyTracker = NewPredictionAccuracyTracker()
	ls.calibrator = NewAdaptiveCalibrator(ls.accuracyTracker, ls.seasonalProfiles)
	ls.feedbackBuffer = NewRingBuffer(1000)
	ls.feedbackHistory = make([]FeedbackRecord, 0)
	ls.dirty = true

	// Delete stored data
	return ls.storage.Delete(ctx)
}
