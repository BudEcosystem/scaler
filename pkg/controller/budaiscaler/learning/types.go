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
	"time"
)

// LLMWorkloadPattern identifies common LLM workload patterns
type LLMWorkloadPattern string

const (
	// PatternSteadyState indicates normal, stable workload
	PatternSteadyState LLMWorkloadPattern = "steady_state"
	// PatternBatchIngestion indicates bulk request processing
	PatternBatchIngestion LLMWorkloadPattern = "batch_ingestion"
	// PatternKVCachePressure indicates KV cache is under pressure
	PatternKVCachePressure LLMWorkloadPattern = "kv_cache_pressure"
	// PatternColdStart indicates workload is starting from cold state
	PatternColdStart LLMWorkloadPattern = "cold_start"
	// PatternTrafficSpike indicates sudden increase in traffic
	PatternTrafficSpike LLMWorkloadPattern = "traffic_spike"
	// PatternTrafficDrain indicates traffic is decreasing
	PatternTrafficDrain LLMWorkloadPattern = "traffic_drain"
)

// ScaleDirection indicates the direction of scaling
type ScaleDirection string

const (
	ScaleUp   ScaleDirection = "up"
	ScaleDown ScaleDirection = "down"
	ScaleNone ScaleDirection = "none"
)

// SeasonalBucket represents statistics for a specific time bucket
type SeasonalBucket struct {
	HourOfDay    int       `json:"hourOfDay"`
	DayOfWeek    int       `json:"dayOfWeek"`
	Mean         float64   `json:"mean"`
	Variance     float64   `json:"variance"`
	Observations int       `json:"observations"`
	LastUpdated  time.Time `json:"lastUpdated"`
}

// LLMMetrics contains LLM-specific metrics for pattern detection
type LLMMetrics struct {
	GPUCacheUsage         float64 `json:"gpuCacheUsage"`
	RequestsWaiting       float64 `json:"requestsWaiting"`
	RequestsRunning       float64 `json:"requestsRunning"`
	TokenThroughput       float64 `json:"tokenThroughput"`
	AvgTTFT               float64 `json:"avgTTFT"` // Time to first token
	AvgTPOT               float64 `json:"avgTPOT"` // Time per output token
	GPUMemoryUtilization  float64 `json:"gpuMemoryUtilization"`
	GPUComputeUtilization float64 `json:"gpuComputeUtilization"`
}

// FeedbackRecord captures the complete context for learning
type FeedbackRecord struct {
	// Temporal Context
	Timestamp time.Time `json:"timestamp"`
	HourOfDay int       `json:"hourOfDay"` // 0-23
	DayOfWeek int       `json:"dayOfWeek"` // 0-6 (Sunday=0)

	// Scaling Event
	PredictedReplicas   int32         `json:"predictedReplicas"`
	RecommendedReplicas int32         `json:"recommendedReplicas"`
	ActualReplicas      int32         `json:"actualReplicas"`
	ScalingLatency      time.Duration `json:"scalingLatency"` // Time from decision to pods ready

	// Metric Context at Decision Time
	MetricSnapshots map[string]float64 `json:"metricSnapshots"`

	// LLM-Specific Metrics
	LLMMetrics LLMMetrics `json:"llmMetrics"`

	// Outcome Metrics (collected T+LookAhead)
	OutcomeReplicas    int32   `json:"outcomeReplicas"`
	OutcomeMetricValue float64 `json:"outcomeMetricValue"`
	PredictionError    float64 `json:"predictionError"` // (Predicted - Actual) / Actual

	// Decision Quality Indicators
	WasOverProvisioned  bool `json:"wasOverProvisioned"`  // Scaled more than needed
	WasUnderProvisioned bool `json:"wasUnderProvisioned"` // Scaled less than needed (SLA breach)
	LatencyBreach       bool `json:"latencyBreach"`       // Latency exceeded threshold
}

// PendingVerification represents a prediction awaiting outcome verification
type PendingVerification struct {
	Record     FeedbackRecord `json:"record"`
	VerifyAt   time.Time      `json:"verifyAt"`
	ScalerName string         `json:"scalerName"`
	Namespace  string         `json:"namespace"`
}

// PredictionOutcome records the outcome of a single prediction
type PredictionOutcome struct {
	Time      time.Time `json:"time"`
	Predicted int32     `json:"predicted"`
	Actual    int32     `json:"actual"`
	Error     float64   `json:"error"`
}

// AccuracyReport provides a comprehensive accuracy summary
type AccuracyReport struct {
	OverallMAE        float64          `json:"overallMAE"`
	OverallMAPE       float64          `json:"overallMAPE"`
	DirectionAccuracy float64          `json:"directionAccuracy"`
	WorstTimeSlots    []TimeSlotStats  `json:"worstTimeSlots"`
	CalibrationNeeded bool             `json:"calibrationNeeded"`
	TotalPredictions  int              `json:"totalPredictions"`
	RecentTrend       float64          `json:"recentTrend"` // Positive = improving, negative = degrading
	HourlyAccuracy    []HourlyAccuracy `json:"hourlyAccuracy"`
}

// TimeSlotStats contains accuracy stats for a specific time slot
type TimeSlotStats struct {
	Hour      int     `json:"hour"`
	DayOfWeek int     `json:"dayOfWeek"`
	MAE       float64 `json:"mae"`
	MAPE      float64 `json:"mape"`
	Count     int     `json:"count"`
}

// HourlyAccuracy contains per-hour accuracy metrics
type HourlyAccuracy struct {
	Hour              int     `json:"hour"`
	DayOfWeek         int     `json:"dayOfWeek"`
	MAE               float64 `json:"mae"`
	MAPE              float64 `json:"mape"`
	DirectionAccuracy float64 `json:"directionAccuracy"`
	DataPoints        int     `json:"dataPoints"`
}

// CalibrationResult contains the outcome of a calibration operation
type CalibrationResult struct {
	Time          time.Time       `json:"time"`
	Skipped       bool            `json:"skipped"`
	PreviousMAE   float64         `json:"previousMAE"`
	PreviousMAPE  float64         `json:"previousMAPE"`
	AlphaAdjusted bool            `json:"alphaAdjusted"`
	NewAlpha      float64         `json:"newAlpha"`
	BucketsReset  []TimeSlotStats `json:"bucketsReset"`
}

// PatternTemplate represents a learned pattern signature
type PatternTemplate struct {
	Name                   string    `json:"name"`
	Signature              []float64 `json:"signature"` // Normalized metric values over time
	TypicalDuration        string    `json:"typicalDuration"`
	OptimalScaleMultiplier float64   `json:"optimalScaleMultiplier"`
	Confidence             float64   `json:"confidence"`
	MatchCount             int       `json:"matchCount"`
}

// KVCachePattern captures learned KV cache behavior
type KVCachePattern struct {
	CacheUsageThreshold   float64       `json:"cacheUsageThreshold"`
	RequestsWaitingMin    int           `json:"requestsWaitingMin"`
	OptimalScaleAction    float64       `json:"optimalScaleAction"` // Multiplier (e.g., 1.5x)
	ResponseDelay         time.Duration `json:"responseDelay"`      // Time before scaling helps
	SuccessfulPredictions int           `json:"successfulPredictions"`
	TotalPredictions      int           `json:"totalPredictions"`
}

// PatternAdjustment represents the scaling adjustment for a detected pattern
type PatternAdjustment struct {
	Pattern    LLMWorkloadPattern `json:"pattern"`
	Factor     float64            `json:"factor"`
	Confidence float64            `json:"confidence"`
	Reason     string             `json:"reason"`
}

// RunningAverage maintains a running average using EWMA
type RunningAverage struct {
	Value float64 `json:"value"`
	Count int     `json:"count"`
	Alpha float64 `json:"alpha"` // Smoothing factor
}

// NewRunningAverage creates a new running average with the given alpha
func NewRunningAverage(alpha float64) *RunningAverage {
	return &RunningAverage{
		Alpha: alpha,
	}
}

// Update updates the running average with a new value
func (r *RunningAverage) Update(value float64) {
	r.Count++
	if r.Count == 1 {
		r.Value = value
	} else {
		r.Value = r.Alpha*value + (1-r.Alpha)*r.Value
	}
}

// Get returns the current running average value
func (r *RunningAverage) Get() float64 {
	return r.Value
}

// RunningAccuracy tracks accuracy as a running percentage
type RunningAccuracy struct {
	Correct int `json:"correct"`
	Total   int `json:"total"`
}

// Update records a new prediction result
func (r *RunningAccuracy) Update(correct bool) {
	r.Total++
	if correct {
		r.Correct++
	}
}

// Get returns the current accuracy percentage (0-1)
func (r *RunningAccuracy) Get() float64 {
	if r.Total == 0 {
		return 0
	}
	return float64(r.Correct) / float64(r.Total)
}

// RingBuffer is a simple circular buffer for storing recent values
type RingBuffer struct {
	data  []interface{}
	head  int
	tail  int
	count int
	cap   int
}

// NewRingBuffer creates a new ring buffer with the given capacity
func NewRingBuffer(capacity int) *RingBuffer {
	return &RingBuffer{
		data: make([]interface{}, capacity),
		cap:  capacity,
	}
}

// Push adds an item to the buffer
func (rb *RingBuffer) Push(item interface{}) {
	rb.data[rb.tail] = item
	rb.tail = (rb.tail + 1) % rb.cap
	if rb.count < rb.cap {
		rb.count++
	} else {
		rb.head = (rb.head + 1) % rb.cap
	}
}

// Len returns the number of items in the buffer
func (rb *RingBuffer) Len() int {
	return rb.count
}

// Get returns the item at the given index (0 = oldest)
func (rb *RingBuffer) Get(index int) interface{} {
	if index < 0 || index >= rb.count {
		return nil
	}
	return rb.data[(rb.head+index)%rb.cap]
}

// GetAll returns all items in order (oldest first)
func (rb *RingBuffer) GetAll() []interface{} {
	result := make([]interface{}, rb.count)
	for i := 0; i < rb.count; i++ {
		result[i] = rb.data[(rb.head+i)%rb.cap]
	}
	return result
}

// Last returns the last n items (newest first)
func (rb *RingBuffer) Last(n int) []interface{} {
	if n > rb.count {
		n = rb.count
	}
	result := make([]interface{}, n)
	for i := 0; i < n; i++ {
		idx := (rb.tail - 1 - i + rb.cap) % rb.cap
		result[i] = rb.data[idx]
	}
	return result
}

// Clear removes all items from the buffer
func (rb *RingBuffer) Clear() {
	rb.head = 0
	rb.tail = 0
	rb.count = 0
}

// GetBucketIndex returns the bucket index (0-167) for a given time
func GetBucketIndex(t time.Time) int {
	hour := t.Hour()
	dayOfWeek := int(t.Weekday())
	return dayOfWeek*24 + hour
}

// GetTimeFromBucketIndex returns hour and dayOfWeek from bucket index
func GetTimeFromBucketIndex(index int) (hour int, dayOfWeek int) {
	dayOfWeek = index / 24
	hour = index % 24
	return
}

// GetDirection determines the scale direction from two replica counts
func GetDirection(from, to int32) ScaleDirection {
	if to > from {
		return ScaleUp
	} else if to < from {
		return ScaleDown
	}
	return ScaleNone
}

// Constants for the learning system
const (
	// NumBuckets is the total number of time buckets (24 hours x 7 days)
	NumBuckets = 168

	// DefaultAlpha is the default EWMA smoothing factor
	DefaultAlpha = 0.1

	// DefaultMinObservations is the minimum observations before confident prediction
	DefaultMinObservations = 10

	// DefaultMaxHistory is the maximum number of feedback records to keep
	DefaultMaxHistory = 1000

	// DefaultPersistInterval is how often to persist learning data
	DefaultPersistInterval = 5 * time.Minute

	// DefaultLookAhead is the default prediction horizon
	DefaultLookAhead = 15 * time.Minute

	// HighConfidenceThreshold is the confidence level for high confidence predictions
	HighConfidenceThreshold = 0.7

	// LowConfidenceThreshold is the confidence level below which predictions are less reliable
	LowConfidenceThreshold = 0.3

	// KVCachePressureThreshold is the GPU cache usage threshold for pressure detection
	KVCachePressureThreshold = 0.80

	// BatchSpikeThreshold is the multiplier threshold for batch spike detection
	BatchSpikeThreshold = 2.0

	// MaxMAPE is the target maximum Mean Absolute Percentage Error
	MaxMAPE = 20.0

	// MinAccuracyThreshold is the target minimum direction accuracy
	MinAccuracyThreshold = 0.85
)
