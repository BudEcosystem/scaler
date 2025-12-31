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

// Package algorithm provides scaling algorithm implementations.
package algorithm

import (
	"context"
	"time"

	scalerv1alpha1 "github.com/BudEcosystem/scaler/api/scaler/v1alpha1"
	"github.com/BudEcosystem/scaler/pkg/types"
)

// ScalingAlgorithm defines the interface for scaling algorithms.
type ScalingAlgorithm interface {
	// ComputeRecommendation calculates the recommended replica count.
	ComputeRecommendation(ctx context.Context, request ScalingRequest) (*ScalingRecommendation, error)

	// GetAlgorithmType returns the algorithm type identifier.
	GetAlgorithmType() scalerv1alpha1.ScalingStrategyType
}

// ScalingRequest contains all information needed to make a scaling decision.
type ScalingRequest struct {
	// Scaler is the BudAIScaler resource.
	Scaler *scalerv1alpha1.BudAIScaler

	// CurrentReplicas is the current number of replicas.
	CurrentReplicas int32

	// DesiredReplicas from the last scaling decision (if any).
	DesiredReplicas int32

	// ReadyPodCount is the number of pods in ready state.
	ReadyPodCount int32

	// StartingPodCount is the number of pods that are running but not yet ready.
	// These pods are expected to become ready soon and contribute to capacity.
	StartingPodCount int32

	// MetricSnapshots contains collected metrics for each source.
	MetricSnapshots map[string]*types.MetricSnapshot

	// AggregatedMetrics contains time-windowed aggregated metrics.
	AggregatedMetrics *types.AggregatedMetrics

	// GPUMetrics contains GPU-specific metrics (if enabled).
	GPUMetrics *types.GPUMetrics

	// CostMetrics contains cost-related metrics (if enabled).
	CostMetrics *types.CostMetrics

	// PredictionData contains prediction data (if enabled).
	PredictionData *types.PredictionData

	// LastScaleTime is when scaling last occurred.
	LastScaleTime *time.Time

	// ScalingContext provides scaling configuration.
	ScalingContext ScalingContextProvider
}

// ScalingContextProvider provides scaling context configuration.
type ScalingContextProvider interface {
	GetMinReplicas() int32
	GetMaxReplicas() int32
	GetScaleUpRate() float64
	GetScaleDownRate() float64
	GetTolerance() float64
	GetPanicThreshold() float64
	GetPanicWindowSeconds() int32
	GetStableWindowSeconds() int32
	GetScaleUpStabilizationSeconds() int32
	GetScaleDownStabilizationSeconds() int32
	GetScaleUpPolicies() []scalerv1alpha1.ScalingPolicy
	GetScaleDownPolicies() []scalerv1alpha1.ScalingPolicy
	GetScaleUpSelectPolicy() scalerv1alpha1.ScalingPolicySelect
	GetScaleDownSelectPolicy() scalerv1alpha1.ScalingPolicySelect
	// Starting pods configuration
	GetStartingPodWeight() float64
	GetMaxStartingPods() int32
	GetMaxStartingPodPercent() int32
	GetBypassGateOnPanic() bool
}

// ScalingRecommendation contains the result of a scaling decision.
type ScalingRecommendation struct {
	// DesiredReplicas is the recommended number of replicas.
	DesiredReplicas int32

	// Reason explains why this recommendation was made.
	Reason string

	// InPanicMode indicates if the algorithm is in panic mode (rapid scaling).
	InPanicMode bool

	// MetricUsed identifies which metric drove the decision.
	MetricUsed string

	// CurrentMetricValue is the metric value that triggered the decision.
	CurrentMetricValue float64

	// TargetMetricValue is the target value for the metric.
	TargetMetricValue float64

	// ScaleDirection indicates the scaling direction.
	ScaleDirection ScaleDirection

	// Confidence indicates how confident the algorithm is (0-1).
	Confidence float64

	// Timestamp when the recommendation was made.
	Timestamp time.Time
}

// ScaleDirection represents the direction of scaling.
type ScaleDirection string

const (
	// ScaleUp indicates scaling up (adding replicas).
	ScaleUp ScaleDirection = "up"

	// ScaleDown indicates scaling down (removing replicas).
	ScaleDown ScaleDirection = "down"

	// NoScale indicates no scaling is needed.
	NoScale ScaleDirection = "none"
)

// AlgorithmFactory creates scaling algorithms.
type AlgorithmFactory interface {
	// Create returns the appropriate algorithm for the given strategy.
	Create(strategy scalerv1alpha1.ScalingStrategyType) (ScalingAlgorithm, error)
}

// DefaultAlgorithmFactory creates scaling algorithms.
type DefaultAlgorithmFactory struct {
	algorithms map[scalerv1alpha1.ScalingStrategyType]ScalingAlgorithm
}

// NewDefaultAlgorithmFactory creates a new DefaultAlgorithmFactory.
func NewDefaultAlgorithmFactory() *DefaultAlgorithmFactory {
	factory := &DefaultAlgorithmFactory{
		algorithms: make(map[scalerv1alpha1.ScalingStrategyType]ScalingAlgorithm),
	}

	// Register default algorithms
	factory.algorithms[scalerv1alpha1.KPA] = NewKPAAlgorithm()
	factory.algorithms[scalerv1alpha1.BudScaler] = NewBudScalerAlgorithm()
	// HPA is handled separately as it delegates to K8s HPA

	return factory
}

// Create returns the appropriate algorithm for the given strategy.
func (f *DefaultAlgorithmFactory) Create(strategy scalerv1alpha1.ScalingStrategyType) (ScalingAlgorithm, error) {
	if algo, exists := f.algorithms[strategy]; exists {
		return algo, nil
	}

	// Default to BudScaler
	return f.algorithms[scalerv1alpha1.BudScaler], nil
}

// Register registers an algorithm for a strategy type.
func (f *DefaultAlgorithmFactory) Register(strategy scalerv1alpha1.ScalingStrategyType, algo ScalingAlgorithm) {
	f.algorithms[strategy] = algo
}

// ApplyScaleUpPolicies applies scale-up policies to limit the desired replica change.
// It returns the maximum allowed replicas based on the configured policies.
func ApplyScaleUpPolicies(currentReplicas, desiredReplicas int32, sctx ScalingContextProvider) int32 {
	if desiredReplicas <= currentReplicas {
		return desiredReplicas // Not scaling up
	}

	policies := sctx.GetScaleUpPolicies()
	if len(policies) == 0 {
		return desiredReplicas // No policies, allow full scale
	}

	selectPolicy := sctx.GetScaleUpSelectPolicy()
	if selectPolicy == scalerv1alpha1.DisabledPolicySelect {
		return currentReplicas // Scaling disabled
	}

	// Calculate allowed change for each policy
	allowedChanges := make([]int32, len(policies))
	for i, policy := range policies {
		allowedChanges[i] = calculatePolicyLimit(currentReplicas, policy, true)
	}

	// Select based on policy
	var maxAllowed int32
	if selectPolicy == scalerv1alpha1.MinChangePolicySelect {
		maxAllowed = minInt32Slice(allowedChanges)
	} else {
		// Default to Max for scale-up
		maxAllowed = maxInt32Slice(allowedChanges)
	}

	maxReplicas := currentReplicas + maxAllowed
	if desiredReplicas > maxReplicas {
		return maxReplicas
	}
	return desiredReplicas
}

// ApplyScaleDownPolicies applies scale-down policies to limit the desired replica change.
// It returns the minimum allowed replicas based on the configured policies.
func ApplyScaleDownPolicies(currentReplicas, desiredReplicas int32, sctx ScalingContextProvider) int32 {
	if desiredReplicas >= currentReplicas {
		return desiredReplicas // Not scaling down
	}

	policies := sctx.GetScaleDownPolicies()
	if len(policies) == 0 {
		return desiredReplicas // No policies, allow full scale
	}

	selectPolicy := sctx.GetScaleDownSelectPolicy()
	if selectPolicy == scalerv1alpha1.DisabledPolicySelect {
		return currentReplicas // Scaling disabled
	}

	// Calculate allowed change for each policy
	allowedChanges := make([]int32, len(policies))
	for i, policy := range policies {
		allowedChanges[i] = calculatePolicyLimit(currentReplicas, policy, false)
	}

	// Select based on policy
	var maxAllowed int32
	if selectPolicy == scalerv1alpha1.MaxChangePolicySelect {
		maxAllowed = maxInt32Slice(allowedChanges)
	} else {
		// Default to Min for scale-down (most conservative)
		maxAllowed = minInt32Slice(allowedChanges)
	}

	minReplicas := currentReplicas - maxAllowed
	if minReplicas < 1 {
		minReplicas = 1
	}
	if desiredReplicas < minReplicas {
		return minReplicas
	}
	return desiredReplicas
}

// calculatePolicyLimit calculates the allowed change for a single policy.
func calculatePolicyLimit(currentReplicas int32, policy scalerv1alpha1.ScalingPolicy, isScaleUp bool) int32 {
	var limit int32

	switch policy.Type {
	case scalerv1alpha1.PodsScalingPolicy:
		limit = policy.Value
	case scalerv1alpha1.PercentScalingPolicy:
		limit = int32(float64(currentReplicas) * float64(policy.Value) / 100.0)
		if limit < 1 && policy.Value > 0 {
			limit = 1 // Ensure at least 1 pod change if percentage > 0
		}
	}

	return limit
}

// minInt32Slice returns the minimum value from a slice of int32.
func minInt32Slice(values []int32) int32 {
	if len(values) == 0 {
		return 0
	}
	min := values[0]
	for _, v := range values[1:] {
		if v < min {
			min = v
		}
	}
	return min
}

// maxInt32Slice returns the maximum value from a slice of int32.
func maxInt32Slice(values []int32) int32 {
	if len(values) == 0 {
		return 0
	}
	max := values[0]
	for _, v := range values[1:] {
		if v > max {
			max = v
		}
	}
	return max
}

// CalculateEffectiveReplicas returns the effective replica count considering
// starting pods with a configurable weight.
// Formula: effectiveReplicas = readyPods + (startingPodWeight * startingPods)
func CalculateEffectiveReplicas(readyPods, startingPods int32, startingPodWeight float64) float64 {
	return float64(readyPods) + (startingPodWeight * float64(startingPods))
}

// ShouldGateScaleUp determines if scale-up should be gated due to too many
// starting pods. Returns true if scale-up should be prevented.
func ShouldGateScaleUp(readyPods, startingPods, maxStartingPods, maxStartingPodPercent int32) bool {
	// If both limits are 0, gate is disabled
	if maxStartingPods == 0 && maxStartingPodPercent == 0 {
		return false
	}

	// Check absolute limit
	if maxStartingPods > 0 && startingPods >= maxStartingPods {
		return true
	}

	// Check percentage limit
	if maxStartingPodPercent > 0 {
		totalPods := readyPods + startingPods
		if totalPods > 0 {
			currentPercent := int32((float64(startingPods) / float64(totalPods)) * 100)
			if currentPercent >= maxStartingPodPercent {
				return true
			}
		}
	}

	return false
}
