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

package cost

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCalculator(t *testing.T) {
	calc := NewCalculator(AWS)
	assert.NotNil(t, calc)
	assert.Equal(t, AWS, calc.provider)
}

func TestCalculator_CalculateHourlyCost(t *testing.T) {
	tests := []struct {
		name          string
		provider      CloudProvider
		instanceType  string
		count         int32
		isSpot        bool
		expectedCost  float64
		expectedError bool
	}{
		{
			name:          "AWS on-demand GPU instance",
			provider:      AWS,
			instanceType:  "p4d.24xlarge",
			count:         1,
			isSpot:        false,
			expectedCost:  32.77, // From AWS pricing
			expectedError: false,
		},
		{
			name:          "AWS spot GPU instance",
			provider:      AWS,
			instanceType:  "p4d.24xlarge",
			count:         1,
			isSpot:        true,
			expectedCost:  9.83, // ~30% of on-demand
			expectedError: false,
		},
		{
			name:          "Multiple instances",
			provider:      AWS,
			instanceType:  "g5.xlarge",
			count:         3,
			isSpot:        false,
			expectedCost:  3.06, // 1.02 * 3
			expectedError: false,
		},
		{
			name:          "GCP GPU instance",
			provider:      GCP,
			instanceType:  "a2-highgpu-1g",
			count:         1,
			isSpot:        false,
			expectedCost:  3.67,
			expectedError: false,
		},
		{
			name:          "Unknown instance type",
			provider:      AWS,
			instanceType:  "unknown-instance",
			count:         1,
			isSpot:        false,
			expectedCost:  0,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calc := NewCalculator(tt.provider)
			cost, err := calc.CalculateHourlyCost(context.Background(), tt.instanceType, tt.count, tt.isSpot)

			if tt.expectedError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.InDelta(t, tt.expectedCost, cost, 0.1)
		})
	}
}

func TestCalculator_ProjectDailyCost(t *testing.T) {
	calc := NewCalculator(AWS)

	// Test projecting from current usage
	hourlyCost := 10.0
	dailyCost := calc.ProjectDailyCost(hourlyCost)
	assert.InDelta(t, 240.0, dailyCost, 0.01) // 10 * 24
}

func TestCalculator_IsWithinBudget(t *testing.T) {
	tests := []struct {
		name          string
		currentCost   float64
		budgetPerHour float64
		budgetPerDay  float64
		expected      bool
	}{
		{
			name:          "within budget",
			currentCost:   5.0,
			budgetPerHour: 10.0,
			budgetPerDay:  200.0,
			expected:      true,
		},
		{
			name:          "exceeds hourly budget",
			currentCost:   15.0,
			budgetPerHour: 10.0,
			budgetPerDay:  400.0,
			expected:      false,
		},
		{
			name:          "projected exceeds daily budget",
			currentCost:   9.0, // 9 * 24 = 216 > 200
			budgetPerHour: 10.0,
			budgetPerDay:  200.0,
			expected:      false,
		},
		{
			name:          "no budget set",
			currentCost:   100.0,
			budgetPerHour: 0,
			budgetPerDay:  0,
			expected:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calc := NewCalculator(AWS)
			budget := &Budget{
				HourlyLimit: tt.budgetPerHour,
				DailyLimit:  tt.budgetPerDay,
			}

			result := calc.IsWithinBudget(tt.currentCost, budget)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculator_GetScalingRecommendation(t *testing.T) {
	tests := []struct {
		name             string
		currentReplicas  int32
		desiredReplicas  int32
		instanceType     string
		budget           *Budget
		expectedReplicas int32
		expectedReason   string
	}{
		{
			name:             "scale up allowed - within budget",
			currentReplicas:  2,
			desiredReplicas:  5,
			instanceType:     "g5.xlarge",
			budget:           &Budget{HourlyLimit: 10.0, DailyLimit: 200.0},
			expectedReplicas: 5,
			expectedReason:   "within budget",
		},
		{
			name:             "scale up limited by budget",
			currentReplicas:  2,
			desiredReplicas:  10,
			instanceType:     "p4d.24xlarge",                                  // expensive at $32.77/hour
			budget:           &Budget{HourlyLimit: 100.0, DailyLimit: 2400.0}, // Daily allows 100/hour
			expectedReplicas: 3,                                               // 100/32.77 = 3.05, so max 3 replicas
			expectedReason:   "limited by budget",
		},
		{
			name:             "scale down always allowed",
			currentReplicas:  5,
			desiredReplicas:  2,
			instanceType:     "g5.xlarge",
			budget:           &Budget{HourlyLimit: 1.0, DailyLimit: 10.0}, // Very tight budget
			expectedReplicas: 2,
			expectedReason:   "scale down",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calc := NewCalculator(AWS)
			rec := calc.GetScalingRecommendation(
				context.Background(),
				tt.currentReplicas,
				tt.desiredReplicas,
				tt.instanceType,
				false, // on-demand
				tt.budget,
			)

			assert.Equal(t, tt.expectedReplicas, rec.RecommendedReplicas)
			assert.Contains(t, rec.Reason, tt.expectedReason)
		})
	}
}

func TestCostTracker_TrackUsage(t *testing.T) {
	tracker := NewCostTracker()

	// Track some usage (all within the last hour)
	tracker.RecordUsage(10.0, time.Now().Add(-45*time.Minute))
	tracker.RecordUsage(15.0, time.Now().Add(-30*time.Minute))
	tracker.RecordUsage(20.0, time.Now())

	// Get current hour usage
	hourlyUsage := tracker.GetHourlyUsage()
	assert.InDelta(t, 45.0, hourlyUsage, 0.1)

	// Get daily usage
	dailyUsage := tracker.GetDailyUsage()
	assert.InDelta(t, 45.0, dailyUsage, 0.1)
}

func TestSpotInstanceManager(t *testing.T) {
	manager := NewSpotInstanceManager(AWS)

	// Test spot availability check
	available := manager.IsSpotAvailable(context.Background(), "g5.xlarge", "us-east-1a")
	// This is a mock, should return true by default
	assert.True(t, available)

	// Test spot price comparison
	savings := manager.GetSpotSavings("g5.xlarge")
	assert.Greater(t, savings, 0.0)
	assert.Less(t, savings, 1.0) // Should be a percentage < 100%
}
