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

package metrics

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/metrics/pkg/client/clientset/versioned"

	scalerv1alpha1 "github.com/BudEcosystem/scaler/api/scaler/v1alpha1"
)

// ResourceMetricFetcher fetches resource metrics (CPU, memory) from the Kubernetes metrics API.
type ResourceMetricFetcher struct {
	metricsClient versioned.Interface
}

// NewResourceMetricFetcher creates a new ResourceMetricFetcher.
func NewResourceMetricFetcher(metricsClient versioned.Interface) *ResourceMetricFetcher {
	return &ResourceMetricFetcher{
		metricsClient: metricsClient,
	}
}

// FetchPodMetrics fetches CPU or memory metrics for a specific pod.
func (f *ResourceMetricFetcher) FetchPodMetrics(ctx context.Context, pod corev1.Pod, source scalerv1alpha1.MetricSource) (float64, error) {
	if f.metricsClient == nil {
		return 0, fmt.Errorf("metrics client not available")
	}

	// Fetch pod metrics from metrics API
	podMetrics, err := f.metricsClient.MetricsV1beta1().PodMetricses(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed to get pod metrics for %s/%s: %w", pod.Namespace, pod.Name, err)
	}

	// Aggregate metrics across all containers
	var totalValue int64
	for _, container := range podMetrics.Containers {
		switch source.TargetMetric {
		case "cpu":
			// CPU is in milliCPU
			totalValue += container.Usage.Cpu().MilliValue()
		case "memory":
			// Memory is in bytes
			totalValue += container.Usage.Memory().Value()
		default:
			return 0, fmt.Errorf("unsupported resource metric: %s", source.TargetMetric)
		}
	}

	// Calculate utilization based on pod requests
	return f.calculateUtilization(pod, source.TargetMetric, totalValue)
}

// SourceType returns the metric source type.
func (f *ResourceMetricFetcher) SourceType() scalerv1alpha1.MetricSourceType {
	return scalerv1alpha1.MetricSourceResource
}

// calculateUtilization calculates the utilization percentage based on requests.
func (f *ResourceMetricFetcher) calculateUtilization(pod corev1.Pod, metricName string, currentValue int64) (float64, error) {
	var totalRequest int64

	for _, container := range pod.Spec.Containers {
		switch metricName {
		case "cpu":
			if request, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
				totalRequest += request.MilliValue()
			}
		case "memory":
			if request, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
				totalRequest += request.Value()
			}
		}
	}

	if totalRequest == 0 {
		// If no request is set, return the raw value
		// For CPU: milliCPU value
		// For memory: bytes value
		return float64(currentValue), nil
	}

	// Return utilization as a percentage
	return (float64(currentValue) / float64(totalRequest)) * 100, nil
}

// ResourceUtilizationFetcher fetches resource utilization as a percentage.
type ResourceUtilizationFetcher struct {
	*ResourceMetricFetcher
}

// NewResourceUtilizationFetcher creates a new ResourceUtilizationFetcher.
func NewResourceUtilizationFetcher(metricsClient versioned.Interface) *ResourceUtilizationFetcher {
	return &ResourceUtilizationFetcher{
		ResourceMetricFetcher: NewResourceMetricFetcher(metricsClient),
	}
}

// FetchPodMetricsRaw fetches raw metric values without utilization calculation.
func (f *ResourceMetricFetcher) FetchPodMetricsRaw(ctx context.Context, pod corev1.Pod, metricName string) (int64, error) {
	if f.metricsClient == nil {
		return 0, fmt.Errorf("metrics client not available")
	}

	podMetrics, err := f.metricsClient.MetricsV1beta1().PodMetricses(pod.Namespace).Get(ctx, pod.Name, metav1.GetOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed to get pod metrics: %w", err)
	}

	var totalValue int64
	for _, container := range podMetrics.Containers {
		switch metricName {
		case "cpu":
			totalValue += container.Usage.Cpu().MilliValue()
		case "memory":
			totalValue += container.Usage.Memory().Value()
		default:
			return 0, fmt.Errorf("unsupported resource metric: %s", metricName)
		}
	}

	return totalValue, nil
}
