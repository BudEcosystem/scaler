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
)

const (
	// DefaultPanicThreshold is the default threshold for entering panic mode.
	// If current value exceeds target by this percentage, enter panic mode.
	DefaultPanicThreshold = 200.0 // 200% of target

	// DefaultMaxScaleUpRate is the maximum rate of scale up per tick.
	DefaultMaxScaleUpRate = 2.0 // Double each tick

	// DefaultMaxScaleDownRate is the maximum rate of scale down per tick.
	DefaultMaxScaleDownRate = 0.5 // Halve each tick
)

// KPAAlgorithm implements KNative-style Pod Autoscaling.
// It uses panic and stable windows to make scaling decisions.
type KPAAlgorithm struct{}

// NewKPAAlgorithm creates a new KPAAlgorithm.
func NewKPAAlgorithm() *KPAAlgorithm {
	return &KPAAlgorithm{}
}

// GetAlgorithmType returns the algorithm type.
func (a *KPAAlgorithm) GetAlgorithmType() scalerv1alpha1.ScalingStrategyType {
	return scalerv1alpha1.KPA
}

// ComputeRecommendation calculates the recommended replica count using KPA logic.
func (a *KPAAlgorithm) ComputeRecommendation(ctx context.Context, request ScalingRequest) (*ScalingRecommendation, error) {
	if request.Scaler == nil {
		return nil, fmt.Errorf("scaler is required")
	}

	if len(request.Scaler.Spec.MetricsSources) == 0 {
		return nil, fmt.Errorf("at least one metric source is required")
	}

	// Get scaling context
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

	// Process each metric source and take the maximum desired replicas
	var maxDesired int32 = 0
	var primaryMetric string
	var primaryValue, primaryTarget float64
	var inPanicMode bool

	for _, source := range request.Scaler.Spec.MetricsSources {
		snapshot, exists := request.MetricSnapshots[source.TargetMetric]
		if !exists || snapshot == nil {
			klog.V(4).InfoS("Metric snapshot not found", "metric", source.TargetMetric)
			continue
		}

		// Parse target value
		targetValue, err := strconv.ParseFloat(source.TargetValue, 64)
		if err != nil {
			klog.V(4).InfoS("Failed to parse target value", "metric", source.TargetMetric, "value", source.TargetValue)
			continue
		}

		// Get current metric value (use average across pods)
		currentValue := snapshot.Average

		// Calculate desired replicas based on this metric
		desired, panicMode := a.calculateDesiredReplicas(
			currentValue,
			targetValue,
			request.CurrentReplicas,
			request.ReadyPodCount,
			sctx,
		)

		if panicMode {
			inPanicMode = true
		}

		if desired > maxDesired {
			maxDesired = desired
			primaryMetric = source.TargetMetric
			primaryValue = currentValue
			primaryTarget = targetValue
		}
	}

	if maxDesired == 0 {
		maxDesired = request.CurrentReplicas
	}

	// Apply min/max constraints
	rec.DesiredReplicas = a.applyConstraints(maxDesired, sctx.GetMinReplicas(), sctx.GetMaxReplicas())

	// Apply scaling policies (limit rate of change)
	if rec.DesiredReplicas > request.CurrentReplicas && !inPanicMode {
		rec.DesiredReplicas = ApplyScaleUpPolicies(request.CurrentReplicas, rec.DesiredReplicas, sctx)
	} else if rec.DesiredReplicas < request.CurrentReplicas {
		rec.DesiredReplicas = ApplyScaleDownPolicies(request.CurrentReplicas, rec.DesiredReplicas, sctx)
	}

	// Apply stabilization
	rec.DesiredReplicas = a.applyStabilization(rec.DesiredReplicas, request, sctx)

	// Set recommendation details
	rec.InPanicMode = inPanicMode
	rec.MetricUsed = primaryMetric
	rec.CurrentMetricValue = primaryValue
	rec.TargetMetricValue = primaryTarget

	// Determine scale direction
	if rec.DesiredReplicas > request.CurrentReplicas {
		rec.ScaleDirection = ScaleUp
		rec.Reason = fmt.Sprintf("Scaling up from %d to %d replicas based on %s metric (current: %.2f, target: %.2f)",
			request.CurrentReplicas, rec.DesiredReplicas, primaryMetric, primaryValue, primaryTarget)
	} else if rec.DesiredReplicas < request.CurrentReplicas {
		rec.ScaleDirection = ScaleDown
		rec.Reason = fmt.Sprintf("Scaling down from %d to %d replicas based on %s metric (current: %.2f, target: %.2f)",
			request.CurrentReplicas, rec.DesiredReplicas, primaryMetric, primaryValue, primaryTarget)
	} else {
		rec.Reason = fmt.Sprintf("No scaling needed. Current replicas %d is optimal for %s metric (current: %.2f, target: %.2f)",
			request.CurrentReplicas, primaryMetric, primaryValue, primaryTarget)
	}

	if inPanicMode {
		rec.Reason += " [PANIC MODE]"
	}

	return rec, nil
}

// calculateDesiredReplicas calculates the desired replica count for a single metric.
func (a *KPAAlgorithm) calculateDesiredReplicas(
	currentValue, targetValue float64,
	currentReplicas, readyPodCount int32,
	sctx ScalingContextProvider,
) (int32, bool) {
	if targetValue <= 0 {
		return currentReplicas, false
	}

	// Calculate the ratio
	ratio := currentValue / targetValue

	// Check for panic mode
	panicThreshold := sctx.GetPanicThreshold() / 100 // Convert from percentage
	if panicThreshold <= 0 {
		panicThreshold = DefaultPanicThreshold / 100
	}
	inPanicMode := ratio >= panicThreshold

	// Check tolerance
	tolerance := sctx.GetTolerance() / 100 // Convert from percentage
	if math.Abs(ratio-1.0) < tolerance {
		// Within tolerance, no scaling needed
		return currentReplicas, false
	}

	// Calculate desired replicas
	// In KPA, desired = ceil(currentReplicas * ratio)
	desiredFloat := float64(currentReplicas) * ratio
	desired := int32(math.Ceil(desiredFloat))

	// Apply rate limits
	if desired > currentReplicas {
		// Scale up
		maxUp := sctx.GetScaleUpRate()
		if maxUp <= 0 {
			maxUp = DefaultMaxScaleUpRate
		}
		maxDesired := int32(math.Ceil(float64(currentReplicas) * maxUp))
		if desired > maxDesired && !inPanicMode {
			desired = maxDesired
		}
	} else if desired < currentReplicas {
		// Scale down
		maxDown := sctx.GetScaleDownRate()
		if maxDown <= 0 {
			maxDown = DefaultMaxScaleDownRate
		}
		minDesired := int32(math.Floor(float64(currentReplicas) * maxDown))
		if desired < minDesired {
			desired = minDesired
		}
	}

	// Ensure at least 1 replica
	if desired < 1 {
		desired = 1
	}

	return desired, inPanicMode
}

// applyConstraints applies min/max constraints to the desired replicas.
func (a *KPAAlgorithm) applyConstraints(desired, min, max int32) int32 {
	if desired < min {
		return min
	}
	if desired > max {
		return max
	}
	return desired
}

// applyStabilization applies stabilization windows to prevent thrashing.
func (a *KPAAlgorithm) applyStabilization(desired int32, request ScalingRequest, sctx ScalingContextProvider) int32 {
	if request.LastScaleTime == nil {
		return desired
	}

	elapsed := time.Since(*request.LastScaleTime)

	if desired > request.CurrentReplicas {
		// Scale up stabilization
		stabilization := time.Duration(sctx.GetScaleUpStabilizationSeconds()) * time.Second
		if elapsed < stabilization {
			klog.V(5).InfoS("Scale up stabilization active", "elapsed", elapsed, "window", stabilization)
			return request.CurrentReplicas
		}
	} else if desired < request.CurrentReplicas {
		// Scale down stabilization
		stabilization := time.Duration(sctx.GetScaleDownStabilizationSeconds()) * time.Second
		if elapsed < stabilization {
			klog.V(5).InfoS("Scale down stabilization active", "elapsed", elapsed, "window", stabilization)
			return request.CurrentReplicas
		}
	}

	return desired
}
