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

// Package metrics provides metric fetching and aggregation capabilities.
package metrics

import (
	"context"

	corev1 "k8s.io/api/core/v1"

	scalerv1alpha1 "github.com/BudEcosystem/scaler/api/scaler/v1alpha1"
	"github.com/BudEcosystem/scaler/pkg/types"
)

// MetricFetcher defines the interface for fetching metrics from various sources.
type MetricFetcher interface {
	// FetchPodMetrics fetches a metric value for a specific pod.
	// Returns the metric value and any error encountered.
	FetchPodMetrics(ctx context.Context, pod corev1.Pod, source scalerv1alpha1.MetricSource) (float64, error)

	// SourceType returns the metric source type this fetcher handles.
	SourceType() scalerv1alpha1.MetricSourceType
}

// MetricCollector collects metrics from multiple pods using a MetricFetcher.
type MetricCollector interface {
	// CollectMetrics collects metrics from all pods and returns a snapshot.
	CollectMetrics(ctx context.Context, pods []corev1.Pod, source scalerv1alpha1.MetricSource) (*types.MetricSnapshot, error)
}

// ExternalMetricFetcher fetches metrics from external sources (not per-pod).
type ExternalMetricFetcher interface {
	// FetchExternalMetrics fetches metrics from an external source.
	// Returns the aggregated metric value for the workload.
	FetchExternalMetrics(ctx context.Context, source scalerv1alpha1.MetricSource, namespace string, podLabels map[string]string) (float64, error)

	// SourceType returns the metric source type this fetcher handles.
	SourceType() scalerv1alpha1.MetricSourceType
}
