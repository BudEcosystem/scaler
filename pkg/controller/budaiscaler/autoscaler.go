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

package budaiscaler

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scalerv1alpha1 "github.com/BudEcosystem/scaler/api/scaler/v1alpha1"
	"github.com/BudEcosystem/scaler/pkg/aggregation"
	"github.com/BudEcosystem/scaler/pkg/algorithm"
	scalercontext "github.com/BudEcosystem/scaler/pkg/context"
	"github.com/BudEcosystem/scaler/pkg/metrics"
	"github.com/BudEcosystem/scaler/pkg/types"
)

// AutoScaler orchestrates the autoscaling process.
type AutoScaler struct {
	client           client.Client
	metricsFactory   metrics.MetricFetcherFactory
	algorithmFactory *algorithm.DefaultAlgorithmFactory
	aggregator       *aggregation.DefaultMetricAggregator
	collector        *metrics.MultiSourceCollector
	workloadScale    *WorkloadScale
}

// NewAutoScaler creates a new AutoScaler.
func NewAutoScaler(
	client client.Client,
	metricsFactory metrics.MetricFetcherFactory,
) *AutoScaler {
	aggregator := aggregation.NewMetricAggregator()
	collector := metrics.NewMultiSourceCollector(metricsFactory, aggregator)

	return &AutoScaler{
		client:           client,
		metricsFactory:   metricsFactory,
		algorithmFactory: algorithm.NewDefaultAlgorithmFactory(),
		aggregator:       aggregator,
		collector:        collector,
		workloadScale:    NewWorkloadScale(client),
	}
}

// ScaleResult contains the result of a scaling operation.
type ScaleResult struct {
	// Scaled indicates if scaling occurred.
	Scaled bool

	// DesiredReplicas is the target replica count.
	DesiredReplicas int32

	// CurrentReplicas was the replica count before scaling.
	CurrentReplicas int32

	// Recommendation contains the full scaling recommendation.
	Recommendation *algorithm.ScalingRecommendation

	// Error contains any error that occurred.
	Error error
}

// Scale performs the scaling operation for a BudAIScaler.
func (a *AutoScaler) Scale(ctx context.Context, scaler *scalerv1alpha1.BudAIScaler) (*ScaleResult, error) {
	result := &ScaleResult{}

	// Get current scale
	scale, err := a.workloadScale.GetScale(ctx, scaler)
	if err != nil {
		return nil, fmt.Errorf("failed to get scale: %w", err)
	}

	result.CurrentReplicas = scale.Spec.Replicas

	// Get pods for the target workload
	pods, err := a.getPods(ctx, scaler, scale)
	if err != nil {
		return nil, fmt.Errorf("failed to get pods: %w", err)
	}

	// Collect metrics
	metricSnapshots, err := a.collector.CollectAllMetrics(ctx, pods, scaler.Spec.MetricsSources)
	if err != nil {
		klog.V(4).InfoS("Failed to collect some metrics", "scaler", scaler.Name, "error", err)
		// Continue with partial metrics
	}

	// Create scaling context
	scalingCtx := scalercontext.NewScalingContextFromScaler(scaler)

	// Build scaling request
	request := algorithm.ScalingRequest{
		Scaler:          scaler,
		CurrentReplicas: scale.Spec.Replicas,
		DesiredReplicas: scale.Spec.Replicas,
		ReadyPodCount:   a.countReadyPods(pods),
		MetricSnapshots: metricSnapshots,
		ScalingContext:  scalingCtx,
	}

	// Get last scale time from status
	if scaler.Status.LastScaleTime != nil {
		t := scaler.Status.LastScaleTime.Time
		request.LastScaleTime = &t
	}

	// Get GPU metrics if enabled
	if scaler.Spec.GPUConfig != nil && scaler.Spec.GPUConfig.Enabled {
		request.GPUMetrics = a.collectGPUMetrics(ctx, pods, scaler)
	}

	// Get the appropriate algorithm
	algo, err := a.algorithmFactory.Create(scaler.Spec.ScalingStrategy)
	if err != nil {
		return nil, fmt.Errorf("failed to create algorithm: %w", err)
	}

	// Compute recommendation
	recommendation, err := algo.ComputeRecommendation(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("failed to compute recommendation: %w", err)
	}

	result.Recommendation = recommendation
	result.DesiredReplicas = recommendation.DesiredReplicas

	// Apply scaling if needed
	if recommendation.DesiredReplicas != scale.Spec.Replicas {
		klog.V(2).InfoS("Scaling workload",
			"scaler", scaler.Name,
			"namespace", scaler.Namespace,
			"from", scale.Spec.Replicas,
			"to", recommendation.DesiredReplicas,
			"reason", recommendation.Reason)

		err = a.workloadScale.SetScale(ctx, scaler, recommendation.DesiredReplicas)
		if err != nil {
			result.Error = fmt.Errorf("failed to set scale: %w", err)
			return result, result.Error
		}

		result.Scaled = true
	}

	return result, nil
}

// getPods retrieves pods for the target workload.
func (a *AutoScaler) getPods(ctx context.Context, scaler *scalerv1alpha1.BudAIScaler, scale *Scale) ([]corev1.Pod, error) {
	// Parse selector from scale
	selector, err := labels.Parse(scale.Status.Selector)
	if err != nil {
		return nil, fmt.Errorf("failed to parse selector: %w", err)
	}

	// List pods matching selector
	podList := &corev1.PodList{}
	err = a.client.List(ctx, podList, &client.ListOptions{
		Namespace:     scaler.Namespace,
		LabelSelector: selector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	// Filter to running pods
	var runningPods []corev1.Pod
	for _, pod := range podList.Items {
		if pod.Status.Phase == corev1.PodRunning && pod.DeletionTimestamp == nil {
			runningPods = append(runningPods, pod)
		}
	}

	return runningPods, nil
}

// countReadyPods counts the number of ready pods.
func (a *AutoScaler) countReadyPods(pods []corev1.Pod) int32 {
	var count int32
	for _, pod := range pods {
		if isPodReady(&pod) {
			count++
		}
	}
	return count
}

// isPodReady checks if a pod is ready.
func isPodReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

// collectGPUMetrics collects GPU metrics from pods.
func (a *AutoScaler) collectGPUMetrics(ctx context.Context, pods []corev1.Pod, scaler *scalerv1alpha1.BudAIScaler) *types.GPUMetrics {
	// This is a placeholder for GPU metrics collection
	// In a real implementation, this would query GPU metrics from pods
	// using the NVIDIA DCGM or similar monitoring solution

	// For now, return nil to indicate GPU metrics are not available
	return nil
}

// GetAggregator returns the metric aggregator.
func (a *AutoScaler) GetAggregator() *aggregation.DefaultMetricAggregator {
	return a.aggregator
}

// GetCollector returns the metric collector.
func (a *AutoScaler) GetCollector() *metrics.MultiSourceCollector {
	return a.collector
}

// RecordScalingDecision records a scaling decision for history.
func (a *AutoScaler) RecordScalingDecision(
	scaler *scalerv1alpha1.BudAIScaler,
	result *ScaleResult,
) scalerv1alpha1.ScalingDecision {
	reason := "Scaling decision"
	if result.Recommendation != nil {
		reason = result.Recommendation.Reason
	}

	decision := scalerv1alpha1.ScalingDecision{
		Timestamp:     metav1.Now(),
		PreviousScale: result.CurrentReplicas,
		NewScale:      result.DesiredReplicas,
		Reason:        reason,
		Algorithm:     string(scaler.Spec.ScalingStrategy),
		Success:       result.Error == nil,
	}

	if result.Error != nil {
		decision.Error = result.Error.Error()
	}

	return decision
}
