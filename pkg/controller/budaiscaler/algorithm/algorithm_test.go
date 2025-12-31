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

package algorithm

import (
	"testing"

	scalerv1alpha1 "github.com/BudEcosystem/scaler/api/scaler/v1alpha1"
)

// mockScalingContext implements ScalingContextProvider for testing.
type mockScalingContext struct {
	minReplicas            int32
	maxReplicas            int32
	scaleUpRate            float64
	scaleDownRate          float64
	tolerance              float64
	panicThreshold         float64
	panicWindowSeconds     int32
	stableWindowSeconds    int32
	scaleUpStabilization   int32
	scaleDownStabilization int32
	scaleUpPolicies        []scalerv1alpha1.ScalingPolicy
	scaleDownPolicies      []scalerv1alpha1.ScalingPolicy
	scaleUpSelectPolicy    scalerv1alpha1.ScalingPolicySelect
	scaleDownSelectPolicy  scalerv1alpha1.ScalingPolicySelect
	// Starting pods config
	startingPodWeight     float64
	maxStartingPods       int32
	maxStartingPodPercent int32
	bypassGateOnPanic     bool
}

func (m *mockScalingContext) GetMinReplicas() int32                 { return m.minReplicas }
func (m *mockScalingContext) GetMaxReplicas() int32                 { return m.maxReplicas }
func (m *mockScalingContext) GetScaleUpRate() float64               { return m.scaleUpRate }
func (m *mockScalingContext) GetScaleDownRate() float64             { return m.scaleDownRate }
func (m *mockScalingContext) GetTolerance() float64                 { return m.tolerance }
func (m *mockScalingContext) GetPanicThreshold() float64            { return m.panicThreshold }
func (m *mockScalingContext) GetPanicWindowSeconds() int32          { return m.panicWindowSeconds }
func (m *mockScalingContext) GetStableWindowSeconds() int32         { return m.stableWindowSeconds }
func (m *mockScalingContext) GetScaleUpStabilizationSeconds() int32 { return m.scaleUpStabilization }
func (m *mockScalingContext) GetScaleDownStabilizationSeconds() int32 {
	return m.scaleDownStabilization
}
func (m *mockScalingContext) GetScaleUpPolicies() []scalerv1alpha1.ScalingPolicy {
	return m.scaleUpPolicies
}
func (m *mockScalingContext) GetScaleDownPolicies() []scalerv1alpha1.ScalingPolicy {
	return m.scaleDownPolicies
}
func (m *mockScalingContext) GetScaleUpSelectPolicy() scalerv1alpha1.ScalingPolicySelect {
	return m.scaleUpSelectPolicy
}
func (m *mockScalingContext) GetScaleDownSelectPolicy() scalerv1alpha1.ScalingPolicySelect {
	return m.scaleDownSelectPolicy
}
func (m *mockScalingContext) GetStartingPodWeight() float64   { return m.startingPodWeight }
func (m *mockScalingContext) GetMaxStartingPods() int32       { return m.maxStartingPods }
func (m *mockScalingContext) GetMaxStartingPodPercent() int32 { return m.maxStartingPodPercent }
func (m *mockScalingContext) GetBypassGateOnPanic() bool      { return m.bypassGateOnPanic }

func TestApplyScaleUpPolicies(t *testing.T) {
	tests := []struct {
		name            string
		currentReplicas int32
		desiredReplicas int32
		policies        []scalerv1alpha1.ScalingPolicy
		selectPolicy    scalerv1alpha1.ScalingPolicySelect
		expected        int32
	}{
		{
			name:            "no policies - allow full scale",
			currentReplicas: 2,
			desiredReplicas: 10,
			policies:        nil,
			selectPolicy:    scalerv1alpha1.MaxChangePolicySelect,
			expected:        10,
		},
		{
			name:            "not scaling up - return desired",
			currentReplicas: 5,
			desiredReplicas: 3,
			policies:        []scalerv1alpha1.ScalingPolicy{{Type: scalerv1alpha1.PodsScalingPolicy, Value: 2, PeriodSeconds: 60}},
			selectPolicy:    scalerv1alpha1.MaxChangePolicySelect,
			expected:        3,
		},
		{
			name:            "pods policy - limit by pods",
			currentReplicas: 2,
			desiredReplicas: 10,
			policies:        []scalerv1alpha1.ScalingPolicy{{Type: scalerv1alpha1.PodsScalingPolicy, Value: 4, PeriodSeconds: 60}},
			selectPolicy:    scalerv1alpha1.MaxChangePolicySelect,
			expected:        6, // 2 + 4 = 6
		},
		{
			name:            "percent policy - limit by percent",
			currentReplicas: 10,
			desiredReplicas: 30,
			policies:        []scalerv1alpha1.ScalingPolicy{{Type: scalerv1alpha1.PercentScalingPolicy, Value: 100, PeriodSeconds: 60}},
			selectPolicy:    scalerv1alpha1.MaxChangePolicySelect,
			expected:        20, // 10 + 100% of 10 = 20
		},
		{
			name:            "multiple policies - select max",
			currentReplicas: 4,
			desiredReplicas: 20,
			policies: []scalerv1alpha1.ScalingPolicy{
				{Type: scalerv1alpha1.PodsScalingPolicy, Value: 2, PeriodSeconds: 60},
				{Type: scalerv1alpha1.PercentScalingPolicy, Value: 100, PeriodSeconds: 60},
			},
			selectPolicy: scalerv1alpha1.MaxChangePolicySelect,
			expected:     8, // max(2, 4) = 4, so 4 + 4 = 8
		},
		{
			name:            "multiple policies - select min",
			currentReplicas: 4,
			desiredReplicas: 20,
			policies: []scalerv1alpha1.ScalingPolicy{
				{Type: scalerv1alpha1.PodsScalingPolicy, Value: 2, PeriodSeconds: 60},
				{Type: scalerv1alpha1.PercentScalingPolicy, Value: 100, PeriodSeconds: 60},
			},
			selectPolicy: scalerv1alpha1.MinChangePolicySelect,
			expected:     6, // min(2, 4) = 2, so 4 + 2 = 6
		},
		{
			name:            "disabled policy - no scaling",
			currentReplicas: 2,
			desiredReplicas: 10,
			policies:        []scalerv1alpha1.ScalingPolicy{{Type: scalerv1alpha1.PodsScalingPolicy, Value: 4, PeriodSeconds: 60}},
			selectPolicy:    scalerv1alpha1.DisabledPolicySelect,
			expected:        2, // keep current
		},
		{
			name:            "desired within policy limit - allow desired",
			currentReplicas: 2,
			desiredReplicas: 4,
			policies:        []scalerv1alpha1.ScalingPolicy{{Type: scalerv1alpha1.PodsScalingPolicy, Value: 10, PeriodSeconds: 60}},
			selectPolicy:    scalerv1alpha1.MaxChangePolicySelect,
			expected:        4, // desired is within limit
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &mockScalingContext{
				scaleUpPolicies:     tt.policies,
				scaleUpSelectPolicy: tt.selectPolicy,
			}
			result := ApplyScaleUpPolicies(tt.currentReplicas, tt.desiredReplicas, ctx)
			if result != tt.expected {
				t.Errorf("ApplyScaleUpPolicies() = %d, want %d", result, tt.expected)
			}
		})
	}
}

func TestApplyScaleDownPolicies(t *testing.T) {
	tests := []struct {
		name            string
		currentReplicas int32
		desiredReplicas int32
		policies        []scalerv1alpha1.ScalingPolicy
		selectPolicy    scalerv1alpha1.ScalingPolicySelect
		expected        int32
	}{
		{
			name:            "no policies - allow full scale",
			currentReplicas: 10,
			desiredReplicas: 2,
			policies:        nil,
			selectPolicy:    scalerv1alpha1.MinChangePolicySelect,
			expected:        2,
		},
		{
			name:            "not scaling down - return desired",
			currentReplicas: 3,
			desiredReplicas: 5,
			policies:        []scalerv1alpha1.ScalingPolicy{{Type: scalerv1alpha1.PodsScalingPolicy, Value: 2, PeriodSeconds: 60}},
			selectPolicy:    scalerv1alpha1.MinChangePolicySelect,
			expected:        5,
		},
		{
			name:            "pods policy - limit by pods",
			currentReplicas: 10,
			desiredReplicas: 2,
			policies:        []scalerv1alpha1.ScalingPolicy{{Type: scalerv1alpha1.PodsScalingPolicy, Value: 4, PeriodSeconds: 60}},
			selectPolicy:    scalerv1alpha1.MinChangePolicySelect,
			expected:        6, // 10 - 4 = 6
		},
		{
			name:            "percent policy - limit by percent",
			currentReplicas: 10,
			desiredReplicas: 2,
			policies:        []scalerv1alpha1.ScalingPolicy{{Type: scalerv1alpha1.PercentScalingPolicy, Value: 50, PeriodSeconds: 60}},
			selectPolicy:    scalerv1alpha1.MinChangePolicySelect,
			expected:        5, // 10 - 50% of 10 = 5
		},
		{
			name:            "multiple policies - select min (conservative)",
			currentReplicas: 10,
			desiredReplicas: 2,
			policies: []scalerv1alpha1.ScalingPolicy{
				{Type: scalerv1alpha1.PodsScalingPolicy, Value: 2, PeriodSeconds: 60},
				{Type: scalerv1alpha1.PercentScalingPolicy, Value: 50, PeriodSeconds: 60},
			},
			selectPolicy: scalerv1alpha1.MinChangePolicySelect,
			expected:     8, // min(2, 5) = 2, so 10 - 2 = 8
		},
		{
			name:            "multiple policies - select max (aggressive)",
			currentReplicas: 10,
			desiredReplicas: 2,
			policies: []scalerv1alpha1.ScalingPolicy{
				{Type: scalerv1alpha1.PodsScalingPolicy, Value: 2, PeriodSeconds: 60},
				{Type: scalerv1alpha1.PercentScalingPolicy, Value: 50, PeriodSeconds: 60},
			},
			selectPolicy: scalerv1alpha1.MaxChangePolicySelect,
			expected:     5, // max(2, 5) = 5, so 10 - 5 = 5
		},
		{
			name:            "disabled policy - no scaling",
			currentReplicas: 10,
			desiredReplicas: 2,
			policies:        []scalerv1alpha1.ScalingPolicy{{Type: scalerv1alpha1.PodsScalingPolicy, Value: 4, PeriodSeconds: 60}},
			selectPolicy:    scalerv1alpha1.DisabledPolicySelect,
			expected:        10, // keep current
		},
		{
			name:            "desired within policy limit - allow desired",
			currentReplicas: 10,
			desiredReplicas: 8,
			policies:        []scalerv1alpha1.ScalingPolicy{{Type: scalerv1alpha1.PodsScalingPolicy, Value: 10, PeriodSeconds: 60}},
			selectPolicy:    scalerv1alpha1.MinChangePolicySelect,
			expected:        8, // desired is within limit
		},
		{
			name:            "ensure minimum 1 replica",
			currentReplicas: 2,
			desiredReplicas: 0,
			policies:        []scalerv1alpha1.ScalingPolicy{{Type: scalerv1alpha1.PodsScalingPolicy, Value: 10, PeriodSeconds: 60}},
			selectPolicy:    scalerv1alpha1.MaxChangePolicySelect,
			expected:        1, // minimum 1 replica
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &mockScalingContext{
				scaleDownPolicies:     tt.policies,
				scaleDownSelectPolicy: tt.selectPolicy,
			}
			result := ApplyScaleDownPolicies(tt.currentReplicas, tt.desiredReplicas, ctx)
			if result != tt.expected {
				t.Errorf("ApplyScaleDownPolicies() = %d, want %d", result, tt.expected)
			}
		})
	}
}

func TestCalculatePolicyLimit(t *testing.T) {
	tests := []struct {
		name            string
		currentReplicas int32
		policy          scalerv1alpha1.ScalingPolicy
		isScaleUp       bool
		expected        int32
	}{
		{
			name:            "pods policy",
			currentReplicas: 10,
			policy:          scalerv1alpha1.ScalingPolicy{Type: scalerv1alpha1.PodsScalingPolicy, Value: 5},
			isScaleUp:       true,
			expected:        5,
		},
		{
			name:            "percent policy - 50%",
			currentReplicas: 10,
			policy:          scalerv1alpha1.ScalingPolicy{Type: scalerv1alpha1.PercentScalingPolicy, Value: 50},
			isScaleUp:       true,
			expected:        5,
		},
		{
			name:            "percent policy - 100%",
			currentReplicas: 4,
			policy:          scalerv1alpha1.ScalingPolicy{Type: scalerv1alpha1.PercentScalingPolicy, Value: 100},
			isScaleUp:       true,
			expected:        4,
		},
		{
			name:            "percent policy - ensures at least 1",
			currentReplicas: 1,
			policy:          scalerv1alpha1.ScalingPolicy{Type: scalerv1alpha1.PercentScalingPolicy, Value: 50},
			isScaleUp:       true,
			expected:        1, // 50% of 1 rounds to 0, but we ensure at least 1
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculatePolicyLimit(tt.currentReplicas, tt.policy, tt.isScaleUp)
			if result != tt.expected {
				t.Errorf("calculatePolicyLimit() = %d, want %d", result, tt.expected)
			}
		})
	}
}

func TestCalculateEffectiveReplicas(t *testing.T) {
	tests := []struct {
		name              string
		readyPods         int32
		startingPods      int32
		startingPodWeight float64
		expected          float64
	}{
		{
			name:              "no starting pods",
			readyPods:         5,
			startingPods:      0,
			startingPodWeight: 0.5,
			expected:          5.0,
		},
		{
			name:              "all starting pods",
			readyPods:         0,
			startingPods:      4,
			startingPodWeight: 0.5,
			expected:          2.0, // 0 + 0.5 * 4 = 2
		},
		{
			name:              "mixed pods with 50% weight",
			readyPods:         3,
			startingPods:      2,
			startingPodWeight: 0.5,
			expected:          4.0, // 3 + 0.5 * 2 = 4
		},
		{
			name:              "mixed pods with 100% weight",
			readyPods:         3,
			startingPods:      2,
			startingPodWeight: 1.0,
			expected:          5.0, // 3 + 1.0 * 2 = 5
		},
		{
			name:              "mixed pods with 0% weight",
			readyPods:         3,
			startingPods:      2,
			startingPodWeight: 0.0,
			expected:          3.0, // 3 + 0.0 * 2 = 3
		},
		{
			name:              "mixed pods with 75% weight",
			readyPods:         4,
			startingPods:      4,
			startingPodWeight: 0.75,
			expected:          7.0, // 4 + 0.75 * 4 = 7
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateEffectiveReplicas(tt.readyPods, tt.startingPods, tt.startingPodWeight)
			if result != tt.expected {
				t.Errorf("CalculateEffectiveReplicas() = %f, want %f", result, tt.expected)
			}
		})
	}
}

func TestShouldGateScaleUp(t *testing.T) {
	tests := []struct {
		name                  string
		readyPods             int32
		startingPods          int32
		maxStartingPods       int32
		maxStartingPodPercent int32
		expected              bool
	}{
		{
			name:                  "gate disabled - both limits 0",
			readyPods:             5,
			startingPods:          3,
			maxStartingPods:       0,
			maxStartingPodPercent: 0,
			expected:              false,
		},
		{
			name:                  "under absolute limit",
			readyPods:             5,
			startingPods:          2,
			maxStartingPods:       3,
			maxStartingPodPercent: 0,
			expected:              false,
		},
		{
			name:                  "at absolute limit - gate",
			readyPods:             5,
			startingPods:          3,
			maxStartingPods:       3,
			maxStartingPodPercent: 0,
			expected:              true,
		},
		{
			name:                  "above absolute limit - gate",
			readyPods:             5,
			startingPods:          5,
			maxStartingPods:       3,
			maxStartingPodPercent: 0,
			expected:              true,
		},
		{
			name:                  "under percent limit",
			readyPods:             8,
			startingPods:          2,
			maxStartingPods:       0,
			maxStartingPodPercent: 30,
			expected:              false, // 2/10 = 20% < 30%
		},
		{
			name:                  "at percent limit - gate",
			readyPods:             7,
			startingPods:          3,
			maxStartingPods:       0,
			maxStartingPodPercent: 30,
			expected:              true, // 3/10 = 30% >= 30%
		},
		{
			name:                  "above percent limit - gate",
			readyPods:             5,
			startingPods:          5,
			maxStartingPods:       0,
			maxStartingPodPercent: 30,
			expected:              true, // 5/10 = 50% > 30%
		},
		{
			name:                  "both limits - under both",
			readyPods:             8,
			startingPods:          2,
			maxStartingPods:       5,
			maxStartingPodPercent: 50,
			expected:              false, // 2 < 5 and 20% < 50%
		},
		{
			name:                  "both limits - over absolute",
			readyPods:             7,
			startingPods:          3,
			maxStartingPods:       2,
			maxStartingPodPercent: 50,
			expected:              true, // 3 >= 2
		},
		{
			name:                  "both limits - over percent",
			readyPods:             2,
			startingPods:          3,
			maxStartingPods:       10,
			maxStartingPodPercent: 50,
			expected:              true, // 3/5 = 60% >= 50%
		},
		{
			name:                  "no starting pods - never gate",
			readyPods:             5,
			startingPods:          0,
			maxStartingPods:       1,
			maxStartingPodPercent: 10,
			expected:              false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ShouldGateScaleUp(tt.readyPods, tt.startingPods, tt.maxStartingPods, tt.maxStartingPodPercent)
			if result != tt.expected {
				t.Errorf("ShouldGateScaleUp() = %v, want %v", result, tt.expected)
			}
		})
	}
}
