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
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	subsystemName = "budaiscaler_learning"
)

var (
	// DataPointsTotal tracks the total number of data points collected
	DataPointsTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystemName,
			Name:      "data_points_total",
			Help:      "Total number of data points collected for learning",
		},
		[]string{"scaler", "namespace", "metric"},
	)

	// SeasonalFactor tracks the current seasonal adjustment factor
	SeasonalFactor = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystemName,
			Name:      "seasonal_factor",
			Help:      "Current seasonal adjustment factor for predictions",
		},
		[]string{"scaler", "namespace", "hour", "day_of_week"},
	)

	// PredictionAccuracy tracks prediction accuracy metrics
	PredictionAccuracy = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystemName,
			Name:      "prediction_accuracy",
			Help:      "Prediction accuracy metrics (MAE, MAPE, direction)",
		},
		[]string{"scaler", "namespace", "type"},
	)

	// PatternDetected tracks detected LLM workload patterns
	PatternDetected = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystemName,
			Name:      "pattern_detected",
			Help:      "Currently detected LLM workload pattern (1 = active)",
		},
		[]string{"scaler", "namespace", "pattern"},
	)

	// PatternConfidence tracks the confidence of pattern detection
	PatternConfidence = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystemName,
			Name:      "pattern_confidence",
			Help:      "Confidence level of the detected pattern (0-1)",
		},
		[]string{"scaler", "namespace"},
	)

	// CalibrationEventsTotal tracks calibration events
	CalibrationEventsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: subsystemName,
			Name:      "calibration_events_total",
			Help:      "Total number of calibration events",
		},
		[]string{"scaler", "namespace"},
	)

	// StorageBytes tracks storage used for learning data
	StorageBytes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystemName,
			Name:      "storage_bytes",
			Help:      "Storage used for learning data in bytes",
		},
		[]string{"scaler", "namespace"},
	)

	// SeasonalCompleteness tracks the completeness of seasonal data
	SeasonalCompleteness = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystemName,
			Name:      "seasonal_completeness",
			Help:      "Percentage of seasonal time buckets with sufficient data (0-100)",
		},
		[]string{"scaler", "namespace"},
	)

	// FeedbackRecordsTotal tracks the number of feedback records processed
	FeedbackRecordsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: subsystemName,
			Name:      "feedback_records_total",
			Help:      "Total number of feedback records processed",
		},
		[]string{"scaler", "namespace"},
	)

	// PredictionVerificationsTotal tracks prediction verification outcomes
	PredictionVerificationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: subsystemName,
			Name:      "prediction_verifications_total",
			Help:      "Total number of prediction verifications",
		},
		[]string{"scaler", "namespace", "outcome"}, // outcome: accurate, over_provisioned, under_provisioned
	)
)

func init() {
	// Register all metrics with controller-runtime metrics registry
	metrics.Registry.MustRegister(
		DataPointsTotal,
		SeasonalFactor,
		PredictionAccuracy,
		PatternDetected,
		PatternConfidence,
		CalibrationEventsTotal,
		StorageBytes,
		SeasonalCompleteness,
		FeedbackRecordsTotal,
		PredictionVerificationsTotal,
	)
}

// MetricsRecorder provides methods to record learning metrics
type MetricsRecorder struct {
	ScalerName string
	Namespace  string
}

// NewMetricsRecorder creates a new metrics recorder
func NewMetricsRecorder(scalerName, namespace string) *MetricsRecorder {
	return &MetricsRecorder{
		ScalerName: scalerName,
		Namespace:  namespace,
	}
}

// RecordDataPoints records the number of data points for a metric
func (r *MetricsRecorder) RecordDataPoints(metric string, count float64) {
	DataPointsTotal.WithLabelValues(r.ScalerName, r.Namespace, metric).Set(count)
}

// RecordSeasonalFactor records the seasonal factor for a time slot
func (r *MetricsRecorder) RecordSeasonalFactor(hour, dayOfWeek int, factor float64) {
	SeasonalFactor.WithLabelValues(
		r.ScalerName,
		r.Namespace,
		intToString(hour),
		intToString(dayOfWeek),
	).Set(factor)
}

// RecordPredictionAccuracy records prediction accuracy metrics
func (r *MetricsRecorder) RecordPredictionAccuracy(mae, mape, directionAccuracy float64) {
	PredictionAccuracy.WithLabelValues(r.ScalerName, r.Namespace, "mae").Set(mae)
	PredictionAccuracy.WithLabelValues(r.ScalerName, r.Namespace, "mape").Set(mape)
	PredictionAccuracy.WithLabelValues(r.ScalerName, r.Namespace, "direction").Set(directionAccuracy)
}

// RecordPatternDetected records the currently detected pattern
func (r *MetricsRecorder) RecordPatternDetected(pattern LLMWorkloadPattern, confidence float64) {
	// Reset all pattern values
	patterns := []LLMWorkloadPattern{
		PatternSteadyState,
		PatternBatchIngestion,
		PatternKVCachePressure,
		PatternColdStart,
		PatternTrafficSpike,
		PatternTrafficDrain,
	}
	for _, p := range patterns {
		val := 0.0
		if p == pattern {
			val = 1.0
		}
		PatternDetected.WithLabelValues(r.ScalerName, r.Namespace, string(p)).Set(val)
	}

	PatternConfidence.WithLabelValues(r.ScalerName, r.Namespace).Set(confidence)
}

// RecordCalibration records a calibration event
func (r *MetricsRecorder) RecordCalibration() {
	CalibrationEventsTotal.WithLabelValues(r.ScalerName, r.Namespace).Inc()
}

// RecordStorageBytes records the storage used
func (r *MetricsRecorder) RecordStorageBytes(bytes float64) {
	StorageBytes.WithLabelValues(r.ScalerName, r.Namespace).Set(bytes)
}

// RecordSeasonalCompleteness records the seasonal data completeness
func (r *MetricsRecorder) RecordSeasonalCompleteness(percentage float64) {
	SeasonalCompleteness.WithLabelValues(r.ScalerName, r.Namespace).Set(percentage)
}

// RecordFeedback records a feedback record
func (r *MetricsRecorder) RecordFeedback() {
	FeedbackRecordsTotal.WithLabelValues(r.ScalerName, r.Namespace).Inc()
}

// RecordPredictionVerification records a prediction verification outcome
func (r *MetricsRecorder) RecordPredictionVerification(outcome string) {
	PredictionVerificationsTotal.WithLabelValues(r.ScalerName, r.Namespace, outcome).Inc()
}

// intToString converts an int to a string (simple helper)
func intToString(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}
