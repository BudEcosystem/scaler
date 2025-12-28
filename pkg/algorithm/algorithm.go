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
