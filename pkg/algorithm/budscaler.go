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
	"context"
	"fmt"
	"math"
	"strconv"
	"time"

	"k8s.io/klog/v2"

	scalerv1alpha1 "github.com/BudEcosystem/scaler/api/scaler/v1alpha1"
	"github.com/BudEcosystem/scaler/pkg/types"
)

const (
	// GPUMemoryWeight is the weight for GPU memory in composite scoring.
	GPUMemoryWeight = 0.4

	// GPUComputeWeight is the weight for GPU compute in composite scoring.
	GPUComputeWeight = 0.3

	// RequestQueueWeight is the weight for request queue metrics.
	RequestQueueWeight = 0.3

	// DefaultScaleUpMargin is the margin for scale up decisions.
	DefaultScaleUpMargin = 0.1 // 10% above target triggers scale up

	// DefaultScaleDownMargin is the margin for scale down decisions.
	DefaultScaleDownMargin = 0.2 // 20% below target allows scale down

	// DefaultPredictionWeight is how much prediction influences the decision.
	DefaultPredictionWeight = 0.3
)

// BudScalerAlgorithm implements a custom GenAI-optimized scaling algorithm.
// It integrates GPU metrics, cost awareness, and predictions.
type BudScalerAlgorithm struct{}

// NewBudScalerAlgorithm creates a new BudScalerAlgorithm.
func NewBudScalerAlgorithm() *BudScalerAlgorithm {
	return &BudScalerAlgorithm{}
}

// GetAlgorithmType returns the algorithm type.
func (a *BudScalerAlgorithm) GetAlgorithmType() scalerv1alpha1.ScalingStrategyType {
	return scalerv1alpha1.BudScaler
}

// ComputeRecommendation calculates the recommended replica count.
func (a *BudScalerAlgorithm) ComputeRecommendation(ctx context.Context, request ScalingRequest) (*ScalingRecommendation, error) {
	if request.Scaler == nil {
		return nil, fmt.Errorf("scaler is required")
	}

	if len(request.Scaler.Spec.MetricsSources) == 0 {
		return nil, fmt.Errorf("at least one metric source is required")
	}

	sctx := request.ScalingContext
	if sctx == nil {
		return nil, fmt.Errorf("scaling context is required")
	}

	// Initialize recommendation
	rec := &ScalingRecommendation{
		DesiredReplicas: request.CurrentReplicas,
		Timestamp:       time.Now(),
		ScaleDirection:  NoScale,
		Confidence:      1.0,
	}

	// Calculate metric-based recommendation
	metricRec, err := a.calculateMetricBasedRecommendation(request, sctx)
	if err != nil {
		klog.V(4).InfoS("Failed to calculate metric-based recommendation", "error", err)
		metricRec = request.CurrentReplicas
	}

	// Calculate GPU-based recommendation if enabled
	gpuRec := metricRec
	if request.Scaler.Spec.GPUConfig != nil && request.Scaler.Spec.GPUConfig.Enabled && request.GPUMetrics != nil {
		gpuRec = a.calculateGPUBasedRecommendation(request, sctx)
	}

	// Calculate cost-constrained recommendation if enabled
	costRec := gpuRec
	if request.Scaler.Spec.CostConfig != nil && request.CostMetrics != nil {
		costRec = a.applyCostConstraints(gpuRec, request)
	}

	// Apply prediction adjustment if enabled
	predRec := costRec
	if request.Scaler.Spec.PredictionConfig != nil && request.Scaler.Spec.PredictionConfig.Enabled && request.PredictionData != nil {
		predRec = a.applyPredictionAdjustment(costRec, request)
	}

	// Apply min/max constraints
	rec.DesiredReplicas = a.applyConstraints(predRec, sctx.GetMinReplicas(), sctx.GetMaxReplicas())

	// Apply stabilization
	rec.DesiredReplicas = a.applyStabilization(rec.DesiredReplicas, request, sctx)

	// Determine scale direction and reason
	a.setRecommendationDetails(rec, request)

	return rec, nil
}

// calculateMetricBasedRecommendation calculates desired replicas based on metrics.
func (a *BudScalerAlgorithm) calculateMetricBasedRecommendation(request ScalingRequest, sctx ScalingContextProvider) (int32, error) {
	var maxDesired int32 = 0

	for _, source := range request.Scaler.Spec.MetricsSources {
		snapshot, exists := request.MetricSnapshots[source.TargetMetric]
		if !exists || snapshot == nil {
			continue
		}

		targetValue, err := strconv.ParseFloat(source.TargetValue, 64)
		if err != nil {
			continue
		}

		currentValue := snapshot.Average
		if currentValue <= 0 || targetValue <= 0 {
			continue
		}

		// Calculate desired replicas with BudScaler-specific logic
		desired := a.calculateDesiredForMetric(currentValue, targetValue, request.CurrentReplicas, sctx)

		if desired > maxDesired {
			maxDesired = desired
		}
	}

	if maxDesired == 0 {
		return request.CurrentReplicas, nil
	}

	return maxDesired, nil
}

// calculateDesiredForMetric calculates desired replicas for a single metric.
func (a *BudScalerAlgorithm) calculateDesiredForMetric(currentValue, targetValue float64, currentReplicas int32, sctx ScalingContextProvider) int32 {
	ratio := currentValue / targetValue
	tolerance := sctx.GetTolerance() / 100

	// Check if within tolerance
	if math.Abs(ratio-1.0) < tolerance {
		return currentReplicas
	}

	// Use a smoothed scaling formula to avoid aggressive scaling
	// Formula: desired = current * (1 + alpha * (ratio - 1))
	// where alpha is a dampening factor (0.5-1.0)
	alpha := 0.7 // Dampening factor

	if ratio > 1.0 {
		// Scale up - be more responsive
		alpha = 0.8
	} else {
		// Scale down - be more conservative
		alpha = 0.5
	}

	desiredFloat := float64(currentReplicas) * (1 + alpha*(ratio-1))
	desired := int32(math.Ceil(desiredFloat))

	// Apply rate limits
	if desired > currentReplicas {
		maxUp := sctx.GetScaleUpRate()
		if maxUp <= 0 {
			maxUp = DefaultMaxScaleUpRate
		}
		maxDesired := int32(math.Ceil(float64(currentReplicas) * maxUp))
		if desired > maxDesired {
			desired = maxDesired
		}
	} else if desired < currentReplicas {
		maxDown := sctx.GetScaleDownRate()
		if maxDown <= 0 {
			maxDown = DefaultMaxScaleDownRate
		}
		minDesired := int32(math.Floor(float64(currentReplicas) * maxDown))
		if minDesired < 1 {
			minDesired = 1
		}
		if desired < minDesired {
			desired = minDesired
		}
	}

	if desired < 1 {
		desired = 1
	}

	return desired
}

// calculateGPUBasedRecommendation adjusts recommendation based on GPU metrics.
func (a *BudScalerAlgorithm) calculateGPUBasedRecommendation(request ScalingRequest, sctx ScalingContextProvider) int32 {
	gpu := request.GPUMetrics
	gpuConfig := request.Scaler.Spec.GPUConfig

	if gpu == nil || gpuConfig == nil {
		return request.CurrentReplicas
	}

	// Calculate composite GPU score
	memoryScore := 0.0
	computeScore := 0.0

	// Memory utilization score
	if gpuConfig.GPUMemoryThreshold != nil && *gpuConfig.GPUMemoryThreshold > 0 {
		memoryScore = gpu.MemoryUtilization / float64(*gpuConfig.GPUMemoryThreshold) * 100
	}

	// Compute utilization score
	if gpuConfig.GPUComputeThreshold != nil && *gpuConfig.GPUComputeThreshold > 0 {
		computeScore = gpu.ComputeUtilization / float64(*gpuConfig.GPUComputeThreshold) * 100
	}

	// Weighted composite score
	compositeScore := memoryScore*GPUMemoryWeight + computeScore*GPUComputeWeight

	// Calculate desired replicas based on GPU score
	if compositeScore > 0 {
		ratio := compositeScore / 100 // Normalize to 0-1+
		desiredFloat := float64(request.CurrentReplicas) * ratio
		desired := int32(math.Ceil(desiredFloat))

		// Apply constraints
		if desired < 1 {
			desired = 1
		}

		return desired
	}

	return request.CurrentReplicas
}

// applyCostConstraints applies cost budget constraints.
func (a *BudScalerAlgorithm) applyCostConstraints(desired int32, request ScalingRequest) int32 {
	costConfig := request.Scaler.Spec.CostConfig
	costMetrics := request.CostMetrics

	if costConfig == nil || costMetrics == nil {
		return desired
	}

	// Check if scaling up would exceed budget
	if desired > request.CurrentReplicas {
		additionalReplicas := desired - request.CurrentReplicas
		additionalCost := float64(additionalReplicas) * costMetrics.PerReplicaCostPerHour

		if costMetrics.CurrentCostPerHour+additionalCost > costMetrics.BudgetPerHour {
			// Calculate max affordable replicas
			availableBudget := costMetrics.BudgetPerHour - costMetrics.CurrentCostPerHour
			if availableBudget > 0 && costMetrics.PerReplicaCostPerHour > 0 {
				maxAdditional := int32(availableBudget / costMetrics.PerReplicaCostPerHour)
				constrainedDesired := request.CurrentReplicas + maxAdditional
				if constrainedDesired < desired {
					klog.V(4).InfoS("Constraining scale up due to budget",
						"originalDesired", desired,
						"constrainedDesired", constrainedDesired,
						"budget", costMetrics.BudgetPerHour)
					return constrainedDesired
				}
			} else {
				// No budget for additional replicas
				klog.V(4).InfoS("No budget for additional replicas", "desired", desired)
				return request.CurrentReplicas
			}
		}
	}

	return desired
}

// applyPredictionAdjustment adjusts recommendation based on predictions.
func (a *BudScalerAlgorithm) applyPredictionAdjustment(desired int32, request ScalingRequest) int32 {
	pred := request.PredictionData
	if pred == nil {
		return desired
	}

	// If prediction confidence is low, ignore it
	if pred.Confidence < 0.7 {
		return desired
	}

	// Use prediction to pre-scale
	predictedReplicas := pred.PredictedReplicas
	if predictedReplicas <= 0 {
		return desired
	}

	// Weighted average of current decision and prediction
	weight := DefaultPredictionWeight * pred.Confidence
	adjustedFloat := float64(desired)*(1-weight) + float64(predictedReplicas)*weight
	adjusted := int32(math.Round(adjustedFloat))

	if adjusted < 1 {
		adjusted = 1
	}

	klog.V(5).InfoS("Applied prediction adjustment",
		"desired", desired,
		"predicted", predictedReplicas,
		"adjusted", adjusted,
		"confidence", pred.Confidence)

	return adjusted
}

// applyConstraints applies min/max constraints.
func (a *BudScalerAlgorithm) applyConstraints(desired, min, max int32) int32 {
	if desired < min {
		return min
	}
	if desired > max {
		return max
	}
	return desired
}

// applyStabilization applies stabilization windows.
func (a *BudScalerAlgorithm) applyStabilization(desired int32, request ScalingRequest, sctx ScalingContextProvider) int32 {
	if request.LastScaleTime == nil {
		return desired
	}

	elapsed := time.Since(*request.LastScaleTime)

	if desired > request.CurrentReplicas {
		stabilization := time.Duration(sctx.GetScaleUpStabilizationSeconds()) * time.Second
		if elapsed < stabilization {
			return request.CurrentReplicas
		}
	} else if desired < request.CurrentReplicas {
		stabilization := time.Duration(sctx.GetScaleDownStabilizationSeconds()) * time.Second
		if elapsed < stabilization {
			return request.CurrentReplicas
		}
	}

	return desired
}

// setRecommendationDetails sets the recommendation details.
func (a *BudScalerAlgorithm) setRecommendationDetails(rec *ScalingRecommendation, request ScalingRequest) {
	// Find the primary metric
	var primaryMetric string
	var primaryValue, primaryTarget float64

	for _, source := range request.Scaler.Spec.MetricsSources {
		if snapshot, exists := request.MetricSnapshots[source.TargetMetric]; exists && snapshot != nil {
			primaryMetric = source.TargetMetric
			primaryValue = snapshot.Average
			if tv, err := strconv.ParseFloat(source.TargetValue, 64); err == nil {
				primaryTarget = tv
			}
			break
		}
	}

	rec.MetricUsed = primaryMetric
	rec.CurrentMetricValue = primaryValue
	rec.TargetMetricValue = primaryTarget

	if rec.DesiredReplicas > request.CurrentReplicas {
		rec.ScaleDirection = ScaleUp
		rec.Reason = fmt.Sprintf("BudScaler: Scaling up from %d to %d replicas",
			request.CurrentReplicas, rec.DesiredReplicas)
	} else if rec.DesiredReplicas < request.CurrentReplicas {
		rec.ScaleDirection = ScaleDown
		rec.Reason = fmt.Sprintf("BudScaler: Scaling down from %d to %d replicas",
			request.CurrentReplicas, rec.DesiredReplicas)
	} else {
		rec.Reason = fmt.Sprintf("BudScaler: No scaling needed at %d replicas", request.CurrentReplicas)
	}

	// Add GPU info if available
	if request.GPUMetrics != nil {
		rec.Reason += fmt.Sprintf(" (GPU mem: %.1f%%, compute: %.1f%%)",
			request.GPUMetrics.MemoryUtilization,
			request.GPUMetrics.ComputeUtilization)
	}

	// Add cost info if available
	if request.CostMetrics != nil && request.CostMetrics.BudgetPerHour > 0 {
		usage := (request.CostMetrics.CurrentCostPerHour / request.CostMetrics.BudgetPerHour) * 100
		rec.Reason += fmt.Sprintf(" (budget usage: %.1f%%)", usage)
	}
}

// CalculateReplicasForLoad calculates replicas needed for a given load.
// This is useful for prediction-based pre-scaling.
func (a *BudScalerAlgorithm) CalculateReplicasForLoad(
	predictedLoad float64,
	capacityPerReplica float64,
	minReplicas, maxReplicas int32,
) int32 {
	if capacityPerReplica <= 0 {
		return minReplicas
	}

	desired := int32(math.Ceil(predictedLoad / capacityPerReplica))

	if desired < minReplicas {
		return minReplicas
	}
	if desired > maxReplicas {
		return maxReplicas
	}

	return desired
}

// CalculateOptimalReplicas calculates optimal replicas considering all factors.
func (a *BudScalerAlgorithm) CalculateOptimalReplicas(
	metricValue, targetValue float64,
	gpuMetrics *types.GPUMetrics,
	gpuMemoryThreshold, gpuComputeThreshold int32,
	currentReplicas, minReplicas, maxReplicas int32,
) int32 {
	// Base calculation from metric
	if targetValue <= 0 {
		return currentReplicas
	}

	ratio := metricValue / targetValue
	baseDesired := int32(math.Ceil(float64(currentReplicas) * ratio))

	// Adjust for GPU if available
	if gpuMetrics != nil {
		gpuFactor := 1.0

		if gpuMemoryThreshold > 0 {
			memRatio := gpuMetrics.MemoryUtilization / float64(gpuMemoryThreshold)
			if memRatio > 1.0 {
				gpuFactor = math.Max(gpuFactor, memRatio)
			}
		}

		if gpuComputeThreshold > 0 {
			computeRatio := gpuMetrics.ComputeUtilization / float64(gpuComputeThreshold)
			if computeRatio > 1.0 {
				gpuFactor = math.Max(gpuFactor, computeRatio)
			}
		}

		if gpuFactor > 1.0 {
			baseDesired = int32(math.Ceil(float64(baseDesired) * gpuFactor))
		}
	}

	// Apply constraints
	if baseDesired < minReplicas {
		return minReplicas
	}
	if baseDesired > maxReplicas {
		return maxReplicas
	}

	return baseDesired
}
