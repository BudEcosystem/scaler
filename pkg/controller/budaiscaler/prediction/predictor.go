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

package prediction

import (
	"context"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

// MetricDataPoint represents a single metric observation.
type MetricDataPoint struct {
	Value     float64
	Timestamp time.Time
}

// ScheduleHint defines a known traffic pattern.
type ScheduleHint struct {
	// Name is an identifier for this hint.
	Name string

	// CronExpression specifies when to apply this hint.
	CronExpression string

	// Duration is how long to maintain the target.
	Duration time.Duration

	// TargetReplicas is the number of replicas to scale to.
	TargetReplicas int32
}

// IsActive checks if the schedule hint is currently active.
func (h *ScheduleHint) IsActive(now time.Time) bool {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	schedule, err := parser.Parse(h.CronExpression)
	if err != nil {
		return false
	}

	// Find the last scheduled time before now
	// We need to check if now is within Duration of the last trigger
	checkTime := now.Add(-h.Duration)
	nextTime := schedule.Next(checkTime)

	// If the next scheduled time after (now - duration) is before or equal to now,
	// and now is within the duration window, the hint is active
	return !nextTime.After(now) && now.Before(nextTime.Add(h.Duration))
}

// PredictionResult contains the prediction output.
type PredictionResult struct {
	// RecommendedReplicas is the predicted replica count.
	RecommendedReplicas int32

	// PredictedValue is the predicted metric value.
	PredictedValue float64

	// Confidence is the confidence level (0-1).
	Confidence float64

	// Reason explains the prediction.
	Reason string

	// ScheduleHint is the active schedule hint, if any.
	ScheduleHint *ScheduleHint
}

// Predictor provides time-series prediction for scaling.
type Predictor struct {
	mu            sync.RWMutex
	history       map[string][]MetricDataPoint
	scheduleHints []ScheduleHint
	lookAhead     time.Duration
	maxHistory    int
}

// Option configures the predictor.
type Option func(*Predictor)

// WithLookAhead sets the look-ahead duration.
func WithLookAhead(d time.Duration) Option {
	return func(p *Predictor) {
		p.lookAhead = d
	}
}

// WithMaxHistory sets the maximum history size.
func WithMaxHistory(n int) Option {
	return func(p *Predictor) {
		p.maxHistory = n
	}
}

// NewPredictor creates a new predictor.
func NewPredictor(opts ...Option) *Predictor {
	p := &Predictor{
		history:    make(map[string][]MetricDataPoint),
		lookAhead:  15 * time.Minute,
		maxHistory: 1000,
	}

	for _, opt := range opts {
		opt(p)
	}

	return p
}

// RecordMetric records a metric data point.
func (p *Predictor) RecordMetric(metric string, value float64, timestamp time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.history[metric] == nil {
		p.history[metric] = make([]MetricDataPoint, 0)
	}

	p.history[metric] = append(p.history[metric], MetricDataPoint{
		Value:     value,
		Timestamp: timestamp,
	})

	// Sort by timestamp
	sort.Slice(p.history[metric], func(i, j int) bool {
		return p.history[metric][i].Timestamp.Before(p.history[metric][j].Timestamp)
	})

	// Trim to max history
	if len(p.history[metric]) > p.maxHistory {
		p.history[metric] = p.history[metric][len(p.history[metric])-p.maxHistory:]
	}
}

// GetHistory returns the metric history.
func (p *Predictor) GetHistory(metric string) []MetricDataPoint {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if history, ok := p.history[metric]; ok {
		result := make([]MetricDataPoint, len(history))
		copy(result, history)
		return result
	}
	return nil
}

// MovingAverage calculates the moving average of the last n points.
func (p *Predictor) MovingAverage(metric string, n int) float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()

	history := p.history[metric]
	if len(history) == 0 {
		return 0
	}

	count := n
	if count > len(history) {
		count = len(history)
	}

	var sum float64
	for i := len(history) - count; i < len(history); i++ {
		sum += history[i].Value
	}

	return sum / float64(count)
}

// PredictLinear uses linear regression to predict future values.
func (p *Predictor) PredictLinear(metric string, ahead time.Duration) float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()

	history := p.history[metric]
	if len(history) < 2 {
		if len(history) == 1 {
			return history[0].Value
		}
		return 0
	}

	// Simple linear regression using least squares
	n := float64(len(history))
	var sumX, sumY, sumXY, sumX2 float64

	baseTime := history[0].Timestamp
	for _, dp := range history {
		x := dp.Timestamp.Sub(baseTime).Minutes()
		y := dp.Value
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}

	// Calculate slope and intercept
	denominator := n*sumX2 - sumX*sumX
	if denominator == 0 {
		return history[len(history)-1].Value
	}

	slope := (n*sumXY - sumX*sumY) / denominator
	intercept := (sumY - slope*sumX) / n

	// Predict for future time
	lastTime := history[len(history)-1].Timestamp
	futureTime := lastTime.Add(ahead)
	futureX := futureTime.Sub(baseTime).Minutes()

	return intercept + slope*futureX
}

// PredictReplicas predicts the recommended replica count.
func (p *Predictor) PredictReplicas(
	ctx context.Context,
	metric string,
	currentReplicas int32,
	targetValue float64,
	minReplicas int32,
	maxReplicas int32,
) *PredictionResult {
	result := &PredictionResult{
		RecommendedReplicas: currentReplicas,
	}

	// Check for active schedule hints first
	hint := p.GetActiveScheduleHint(time.Now())
	if hint != nil {
		result.RecommendedReplicas = hint.TargetReplicas
		result.ScheduleHint = hint
		result.Reason = "schedule hint: " + hint.Name
		result.Confidence = 1.0

		// Clamp to bounds
		if result.RecommendedReplicas < minReplicas {
			result.RecommendedReplicas = minReplicas
		}
		if result.RecommendedReplicas > maxReplicas {
			result.RecommendedReplicas = maxReplicas
		}

		return result
	}

	// Predict based on metric trend
	predictedValue := p.PredictLinear(metric, p.lookAhead)
	result.PredictedValue = predictedValue
	result.Confidence = p.GetConfidence(metric)

	if targetValue <= 0 {
		result.Reason = "invalid target value"
		return result
	}

	// Calculate desired replicas based on predicted load
	// If predicted value is higher than target, we need more replicas
	ratio := predictedValue / targetValue
	desiredReplicas := int32(math.Ceil(float64(currentReplicas) * ratio))

	// Apply bounds
	if desiredReplicas < minReplicas {
		desiredReplicas = minReplicas
	}
	if desiredReplicas > maxReplicas {
		desiredReplicas = maxReplicas
	}

	result.RecommendedReplicas = desiredReplicas

	if desiredReplicas > currentReplicas {
		result.Reason = "predicted increase in load"
	} else if desiredReplicas < currentReplicas {
		result.Reason = "predicted decrease in load"
	} else {
		result.Reason = "stable load predicted"
	}

	return result
}

// AddScheduleHint adds a schedule hint if it doesn't already exist.
func (p *Predictor) AddScheduleHint(hint ScheduleHint) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check if hint with same name already exists
	for _, existing := range p.scheduleHints {
		if existing.Name == hint.Name {
			return // Already exists, skip
		}
	}
	p.scheduleHints = append(p.scheduleHints, hint)
}

// GetActiveScheduleHint returns the currently active schedule hint.
func (p *Predictor) GetActiveScheduleHint(now time.Time) *ScheduleHint {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for i := range p.scheduleHints {
		if p.scheduleHints[i].IsActive(now) {
			return &p.scheduleHints[i]
		}
	}
	return nil
}

// GetConfidence returns the confidence level for predictions.
func (p *Predictor) GetConfidence(metric string) float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()

	history := p.history[metric]
	if len(history) == 0 {
		return 0
	}

	// Confidence based on:
	// 1. Number of data points (more = higher confidence)
	// 2. Variance in data (lower = higher confidence)

	// Data points factor (sigmoid-like curve)
	countFactor := 1.0 - 1.0/(1.0+float64(len(history))/10.0)

	// Variance factor
	if len(history) < 2 {
		return countFactor * 0.5
	}

	var sum, sumSq float64
	for _, dp := range history {
		sum += dp.Value
		sumSq += dp.Value * dp.Value
	}
	n := float64(len(history))
	mean := sum / n
	variance := sumSq/n - mean*mean

	// Normalize variance (lower variance = higher confidence)
	// Use coefficient of variation (CV) for normalization
	cv := 0.0
	if mean != 0 {
		cv = math.Sqrt(variance) / math.Abs(mean)
	}
	varianceFactor := 1.0 / (1.0 + cv)

	return countFactor * varianceFactor
}

// ClearHistory clears the metric history.
func (p *Predictor) ClearHistory(metric string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.history, metric)
}

// ClearAllHistory clears all metric history.
func (p *Predictor) ClearAllHistory() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.history = make(map[string][]MetricDataPoint)
}
