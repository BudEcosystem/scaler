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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPredictor(t *testing.T) {
	predictor := NewPredictor(WithLookAhead(15 * time.Minute))
	assert.NotNil(t, predictor)
	assert.Equal(t, 15*time.Minute, predictor.lookAhead)
}

func TestPredictor_RecordMetric(t *testing.T) {
	predictor := NewPredictor()

	// Record some metrics
	now := time.Now()
	predictor.RecordMetric("cpu", 50.0, now.Add(-10*time.Minute))
	predictor.RecordMetric("cpu", 60.0, now.Add(-5*time.Minute))
	predictor.RecordMetric("cpu", 70.0, now)

	// Check history
	history := predictor.GetHistory("cpu")
	require.Len(t, history, 3)
	assert.Equal(t, 50.0, history[0].Value)
	assert.Equal(t, 70.0, history[2].Value)
}

func TestPredictor_MovingAverage(t *testing.T) {
	predictor := NewPredictor()

	// Record metrics with consistent pattern
	now := time.Now()
	for i := 0; i < 10; i++ {
		predictor.RecordMetric("cpu", float64(50+i*2), now.Add(time.Duration(-10+i)*time.Minute))
	}

	// Get moving average
	avg := predictor.MovingAverage("cpu", 5)
	// Values: 50, 52, 54, 56, 58, 60, 62, 64, 66, 68
	// Last 5 values: 60, 62, 64, 66, 68 -> avg = 64
	assert.InDelta(t, 64.0, avg, 0.1)
}

func TestPredictor_LinearTrend(t *testing.T) {
	predictor := NewPredictor()

	// Record increasing metrics
	now := time.Now()
	predictor.RecordMetric("cpu", 40.0, now.Add(-30*time.Minute))
	predictor.RecordMetric("cpu", 50.0, now.Add(-20*time.Minute))
	predictor.RecordMetric("cpu", 60.0, now.Add(-10*time.Minute))
	predictor.RecordMetric("cpu", 70.0, now)

	// Predict 10 minutes ahead
	prediction := predictor.PredictLinear("cpu", 10*time.Minute)
	// Trend is +10 per 10 minutes, so prediction should be around 80
	assert.InDelta(t, 80.0, prediction, 5.0)
}

func TestPredictor_PredictReplicas(t *testing.T) {
	tests := []struct {
		name             string
		metricHistory    []float64
		currentReplicas  int32
		targetValue      float64
		minReplicas      int32
		maxReplicas      int32
		expectedReplicas int32
	}{
		{
			name:             "predict scale up - increasing load",
			metricHistory:    []float64{50, 55, 60, 65, 70, 75, 80},
			currentReplicas:  2,
			targetValue:      70,
			minReplicas:      1,
			maxReplicas:      10,
			expectedReplicas: 3, // Need to scale up based on trend
		},
		{
			name:             "predict scale down - decreasing load",
			metricHistory:    []float64{80, 70, 60, 50, 40, 35, 30},
			currentReplicas:  5,
			targetValue:      50,
			minReplicas:      1,
			maxReplicas:      10,
			expectedReplicas: 1, // Predicted value will be low, so scale down to min
		},
		{
			name:             "stable - no change needed",
			metricHistory:    []float64{50, 50, 50, 50, 50, 50, 50},
			currentReplicas:  3,
			targetValue:      50,
			minReplicas:      1,
			maxReplicas:      10,
			expectedReplicas: 3, // Stay stable (prediction = 50, ratio = 1)
		},
		{
			name:             "respect min replicas",
			metricHistory:    []float64{10, 8, 6, 4, 2, 1, 0.5},
			currentReplicas:  3,
			targetValue:      50,
			minReplicas:      2,
			maxReplicas:      10,
			expectedReplicas: 2, // Can't go below min
		},
		{
			name:             "respect max replicas",
			metricHistory:    []float64{80, 85, 90, 95, 100, 105, 110},
			currentReplicas:  5,
			targetValue:      50,
			minReplicas:      1,
			maxReplicas:      6,
			expectedReplicas: 6, // Can't go above max
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			predictor := NewPredictor(WithLookAhead(10 * time.Minute))

			// Add history
			now := time.Now()
			for i, v := range tt.metricHistory {
				offset := time.Duration(len(tt.metricHistory)-1-i) * 5 * time.Minute
				predictor.RecordMetric("cpu", v, now.Add(-offset))
			}

			result := predictor.PredictReplicas(
				context.Background(),
				"cpu",
				tt.currentReplicas,
				tt.targetValue,
				tt.minReplicas,
				tt.maxReplicas,
			)

			assert.Equal(t, tt.expectedReplicas, result.RecommendedReplicas)
		})
	}
}

func TestScheduleHint_IsActive(t *testing.T) {
	tests := []struct {
		name           string
		cronExpression string
		checkTime      time.Time
		duration       time.Duration
		expected       bool
	}{
		{
			name:           "weekday morning active",
			cronExpression: "0 9 * * 1-5",                                 // 9 AM weekdays
			checkTime:      time.Date(2024, 1, 15, 9, 30, 0, 0, time.UTC), // Monday 9:30 AM
			duration:       2 * time.Hour,
			expected:       true,
		},
		{
			name:           "weekday morning not active - wrong time",
			cronExpression: "0 9 * * 1-5",                                 // 9 AM weekdays
			checkTime:      time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC), // Monday noon
			duration:       2 * time.Hour,
			expected:       false,
		},
		{
			name:           "weekday morning not active - weekend",
			cronExpression: "0 9 * * 1-5",                                 // 9 AM weekdays
			checkTime:      time.Date(2024, 1, 13, 9, 30, 0, 0, time.UTC), // Saturday 9:30 AM
			duration:       2 * time.Hour,
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hint := ScheduleHint{
				Name:           tt.name,
				CronExpression: tt.cronExpression,
				Duration:       tt.duration,
				TargetReplicas: 5,
			}

			result := hint.IsActive(tt.checkTime)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPredictor_GetActiveScheduleHint(t *testing.T) {
	predictor := NewPredictor()

	// Add schedule hints
	predictor.AddScheduleHint(ScheduleHint{
		Name:           "morning-rush",
		CronExpression: "0 9 * * 1-5",
		Duration:       2 * time.Hour,
		TargetReplicas: 5,
	})
	predictor.AddScheduleHint(ScheduleHint{
		Name:           "evening-rush",
		CronExpression: "0 17 * * 1-5",
		Duration:       3 * time.Hour,
		TargetReplicas: 8,
	})

	// Check during morning rush (Monday 9:30 AM)
	morningTime := time.Date(2024, 1, 15, 9, 30, 0, 0, time.UTC)
	hint := predictor.GetActiveScheduleHint(morningTime)
	require.NotNil(t, hint)
	assert.Equal(t, "morning-rush", hint.Name)
	assert.Equal(t, int32(5), hint.TargetReplicas)

	// Check during evening rush
	eveningTime := time.Date(2024, 1, 15, 18, 0, 0, 0, time.UTC)
	hint = predictor.GetActiveScheduleHint(eveningTime)
	require.NotNil(t, hint)
	assert.Equal(t, "evening-rush", hint.Name)
	assert.Equal(t, int32(8), hint.TargetReplicas)

	// Check during off-hours
	nightTime := time.Date(2024, 1, 15, 23, 0, 0, 0, time.UTC)
	hint = predictor.GetActiveScheduleHint(nightTime)
	assert.Nil(t, hint)
}

func TestPredictor_Confidence(t *testing.T) {
	predictor := NewPredictor()

	// Few data points = low confidence
	predictor.RecordMetric("cpu", 50.0, time.Now())
	predictor.RecordMetric("cpu", 60.0, time.Now().Add(-5*time.Minute))

	confidence := predictor.GetConfidence("cpu")
	assert.Less(t, confidence, 0.5) // Low confidence with few points

	// Add more data points
	now := time.Now()
	for i := 0; i < 20; i++ {
		predictor.RecordMetric("cpu", float64(50+i), now.Add(time.Duration(-i)*5*time.Minute))
	}

	confidence = predictor.GetConfidence("cpu")
	assert.Greater(t, confidence, 0.5) // Higher confidence with more points
}
