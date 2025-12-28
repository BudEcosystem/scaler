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

// Package aggregation provides metric aggregation with time windows.
package aggregation

import (
	"sync"
	"time"

	"github.com/BudEcosystem/scaler/pkg/types"
)

const (
	// DefaultPanicWindow is the default panic window duration (60 seconds).
	// Panic mode is triggered when metrics spike rapidly within this window.
	DefaultPanicWindow = 60 * time.Second

	// DefaultStableWindow is the default stable window duration (180 seconds).
	// Stable mode uses this longer window for smoother scaling decisions.
	DefaultStableWindow = 180 * time.Second

	// DefaultMaxDataPoints is the maximum number of data points to store.
	DefaultMaxDataPoints = 360

	// DefaultTickInterval is the interval for collecting metrics.
	DefaultTickInterval = 2 * time.Second
)

// MetricAggregator aggregates metrics over time windows.
type MetricAggregator interface {
	// Record records a new metric value.
	Record(key types.MetricKey, value float64, timestamp time.Time)

	// GetPanicWindowAverage returns the average metric value over the panic window.
	GetPanicWindowAverage(key types.MetricKey) (float64, bool)

	// GetStableWindowAverage returns the average metric value over the stable window.
	GetStableWindowAverage(key types.MetricKey) (float64, bool)

	// GetWindowAverage returns the average over a custom duration window.
	GetWindowAverage(key types.MetricKey, window time.Duration) (float64, bool)

	// GetLatest returns the most recent metric value.
	GetLatest(key types.MetricKey) (float64, time.Time, bool)

	// GetAggregatedMetrics returns aggregated metrics for all stored keys.
	GetAggregatedMetrics() *types.AggregatedMetrics

	// Clear removes all stored data for a key.
	Clear(key types.MetricKey)

	// ClearAll removes all stored data.
	ClearAll()
}

// TimeSeriesData represents a time-stamped metric value.
type TimeSeriesData struct {
	Timestamp time.Time
	Value     float64
}

// DefaultMetricAggregator is the default implementation of MetricAggregator.
type DefaultMetricAggregator struct {
	mu sync.RWMutex

	// Per-metric time series data
	data map[types.MetricKey][]TimeSeriesData

	// Configuration
	panicWindow   time.Duration
	stableWindow  time.Duration
	maxDataPoints int
}

// AggregatorOption configures the metric aggregator.
type AggregatorOption func(*DefaultMetricAggregator)

// WithPanicWindow sets the panic window duration.
func WithPanicWindow(d time.Duration) AggregatorOption {
	return func(a *DefaultMetricAggregator) {
		a.panicWindow = d
	}
}

// WithStableWindow sets the stable window duration.
func WithStableWindow(d time.Duration) AggregatorOption {
	return func(a *DefaultMetricAggregator) {
		a.stableWindow = d
	}
}

// WithMaxDataPoints sets the maximum number of data points to store.
func WithMaxDataPoints(n int) AggregatorOption {
	return func(a *DefaultMetricAggregator) {
		a.maxDataPoints = n
	}
}

// NewMetricAggregator creates a new DefaultMetricAggregator.
func NewMetricAggregator(opts ...AggregatorOption) *DefaultMetricAggregator {
	a := &DefaultMetricAggregator{
		data:          make(map[types.MetricKey][]TimeSeriesData),
		panicWindow:   DefaultPanicWindow,
		stableWindow:  DefaultStableWindow,
		maxDataPoints: DefaultMaxDataPoints,
	}

	for _, opt := range opts {
		opt(a)
	}

	return a
}

// Record records a new metric value.
func (a *DefaultMetricAggregator) Record(key types.MetricKey, value float64, timestamp time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()

	dataPoint := TimeSeriesData{
		Timestamp: timestamp,
		Value:     value,
	}

	series, exists := a.data[key]
	if !exists {
		a.data[key] = []TimeSeriesData{dataPoint}
		return
	}

	// Append new data point
	series = append(series, dataPoint)

	// Trim old data points if exceeding max
	if len(series) > a.maxDataPoints {
		series = series[len(series)-a.maxDataPoints:]
	}

	// Also trim data older than the stable window (plus some buffer)
	cutoff := timestamp.Add(-a.stableWindow - time.Minute)
	trimIndex := 0
	for i, dp := range series {
		if dp.Timestamp.After(cutoff) {
			trimIndex = i
			break
		}
	}
	if trimIndex > 0 {
		series = series[trimIndex:]
	}

	a.data[key] = series
}

// GetPanicWindowAverage returns the average metric value over the panic window.
func (a *DefaultMetricAggregator) GetPanicWindowAverage(key types.MetricKey) (float64, bool) {
	return a.GetWindowAverage(key, a.panicWindow)
}

// GetStableWindowAverage returns the average metric value over the stable window.
func (a *DefaultMetricAggregator) GetStableWindowAverage(key types.MetricKey) (float64, bool) {
	return a.GetWindowAverage(key, a.stableWindow)
}

// GetWindowAverage returns the average over a custom duration window.
func (a *DefaultMetricAggregator) GetWindowAverage(key types.MetricKey, window time.Duration) (float64, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	series, exists := a.data[key]
	if !exists || len(series) == 0 {
		return 0, false
	}

	now := time.Now()
	cutoff := now.Add(-window)

	var sum float64
	var count int

	for _, dp := range series {
		if dp.Timestamp.After(cutoff) {
			sum += dp.Value
			count++
		}
	}

	if count == 0 {
		return 0, false
	}

	return sum / float64(count), true
}

// GetLatest returns the most recent metric value.
func (a *DefaultMetricAggregator) GetLatest(key types.MetricKey) (float64, time.Time, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	series, exists := a.data[key]
	if !exists || len(series) == 0 {
		return 0, time.Time{}, false
	}

	latest := series[len(series)-1]
	return latest.Value, latest.Timestamp, true
}

// GetAggregatedMetrics returns aggregated metrics for all stored keys.
func (a *DefaultMetricAggregator) GetAggregatedMetrics() *types.AggregatedMetrics {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := &types.AggregatedMetrics{
		Values:    make(map[types.MetricKey]types.MetricValue),
		Timestamp: time.Now(),
	}

	for key := range a.data {
		// Use panic window average for current value
		panicAvg, panicExists := a.GetWindowAverage(key, a.panicWindow)
		stableAvg, stableExists := a.GetWindowAverage(key, a.stableWindow)

		if panicExists || stableExists {
			mv := types.MetricValue{
				Timestamp: time.Now(),
			}

			if panicExists {
				mv.Value = panicAvg
			} else if stableExists {
				mv.Value = stableAvg
			}

			result.Values[key] = mv
		}
	}

	return result
}

// Clear removes all stored data for a key.
func (a *DefaultMetricAggregator) Clear(key types.MetricKey) {
	a.mu.Lock()
	defer a.mu.Unlock()

	delete(a.data, key)
}

// ClearAll removes all stored data.
func (a *DefaultMetricAggregator) ClearAll() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.data = make(map[types.MetricKey][]TimeSeriesData)
}

// GetPanicWindow returns the configured panic window duration.
func (a *DefaultMetricAggregator) GetPanicWindow() time.Duration {
	return a.panicWindow
}

// GetStableWindow returns the configured stable window duration.
func (a *DefaultMetricAggregator) GetStableWindow() time.Duration {
	return a.stableWindow
}

// DataPointCount returns the number of data points stored for a key.
func (a *DefaultMetricAggregator) DataPointCount(key types.MetricKey) int {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if series, exists := a.data[key]; exists {
		return len(series)
	}
	return 0
}

// IsInPanicMode determines if the metric is in panic mode.
// Panic mode is triggered when the panic window average exceeds the stable window average
// by more than the tolerance percentage.
func (a *DefaultMetricAggregator) IsInPanicMode(key types.MetricKey, tolerancePercent float64) bool {
	panicAvg, panicExists := a.GetPanicWindowAverage(key)
	stableAvg, stableExists := a.GetStableWindowAverage(key)

	if !panicExists || !stableExists {
		return false
	}

	if stableAvg == 0 {
		return panicAvg > 0
	}

	// Check if panic average exceeds stable average by tolerance
	threshold := stableAvg * (1 + tolerancePercent/100)
	return panicAvg > threshold
}

// GetMax returns the maximum value in the window.
func (a *DefaultMetricAggregator) GetMax(key types.MetricKey, window time.Duration) (float64, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	series, exists := a.data[key]
	if !exists || len(series) == 0 {
		return 0, false
	}

	now := time.Now()
	cutoff := now.Add(-window)

	var max float64
	found := false

	for _, dp := range series {
		if dp.Timestamp.After(cutoff) {
			if !found || dp.Value > max {
				max = dp.Value
				found = true
			}
		}
	}

	return max, found
}

// GetMin returns the minimum value in the window.
func (a *DefaultMetricAggregator) GetMin(key types.MetricKey, window time.Duration) (float64, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	series, exists := a.data[key]
	if !exists || len(series) == 0 {
		return 0, false
	}

	now := time.Now()
	cutoff := now.Add(-window)

	var min float64
	found := false

	for _, dp := range series {
		if dp.Timestamp.After(cutoff) {
			if !found || dp.Value < min {
				min = dp.Value
				found = true
			}
		}
	}

	return min, found
}

// GetPercentile returns the given percentile value in the window.
func (a *DefaultMetricAggregator) GetPercentile(key types.MetricKey, window time.Duration, percentile float64) (float64, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	series, exists := a.data[key]
	if !exists || len(series) == 0 {
		return 0, false
	}

	now := time.Now()
	cutoff := now.Add(-window)

	var values []float64
	for _, dp := range series {
		if dp.Timestamp.After(cutoff) {
			values = append(values, dp.Value)
		}
	}

	if len(values) == 0 {
		return 0, false
	}

	// Sort values
	sortFloat64s(values)

	// Calculate percentile index
	idx := int(float64(len(values)-1) * percentile / 100)
	if idx >= len(values) {
		idx = len(values) - 1
	}

	return values[idx], true
}

// sortFloat64s sorts a slice of float64 in ascending order.
func sortFloat64s(values []float64) {
	for i := 0; i < len(values)-1; i++ {
		for j := i + 1; j < len(values); j++ {
			if values[i] > values[j] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}
