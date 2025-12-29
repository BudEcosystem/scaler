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

// Package types contains core type definitions used throughout the BudAIScaler.
package types

import (
	"time"

	corev1 "k8s.io/api/core/v1"
)

// MetricKey uniquely identifies a metric source.
type MetricKey struct {
	// Name is the name of the metric (e.g., "cpu", "gpu_cache_usage_perc").
	Name string
	// Namespace is the namespace of the metric source.
	Namespace string
	// PodName is the pod this metric came from (if applicable).
	PodName string
	// SourceType is the type of metric source (e.g., "pod", "prometheus").
	SourceType string
}

// MetricValue represents a single metric measurement.
type MetricValue struct {
	// Value is the metric value.
	Value float64
	// Timestamp is when the metric was collected.
	Timestamp time.Time
	// PodName is the pod this metric came from (if applicable).
	PodName string
	// Error indicates any error during metric collection.
	Error error
}

// MetricSnapshot contains a point-in-time collection of metrics.
type MetricSnapshot struct {
	// Key identifies the metric.
	Key MetricKey
	// Values contains per-pod metric values, keyed by pod name.
	Values map[string]MetricValue
	// Timestamp is when the snapshot was taken.
	Timestamp time.Time
	// Average is the computed average across all values.
	Average float64
	// Sum is the sum of all values.
	Sum float64
	// Min is the minimum value.
	Min float64
	// Max is the maximum value.
	Max float64
	// Count is the number of values.
	Count int
	// TotalPods is the number of pods contributing to this snapshot.
	TotalPods int
	// ReadyPods is the number of ready pods.
	ReadyPods int
	// Error indicates any error during collection.
	Error error
}

// AggregatedMetrics contains time-window aggregated metric data.
type AggregatedMetrics struct {
	// Values contains aggregated values keyed by MetricKey.
	Values map[MetricKey]MetricValue
	// MetricKey identifies the metric.
	MetricKey MetricKey
	// StableValue is the aggregated value over the stable window (e.g., 180s).
	StableValue float64
	// PanicValue is the aggregated value over the panic window (e.g., 60s).
	PanicValue float64
	// Timestamp is when the aggregation was performed.
	Timestamp time.Time
	// Valid indicates whether the aggregated metrics are valid.
	Valid bool
	// ReadyPodCount is the number of ready pods.
	ReadyPodCount int
}

// ScaleTarget represents the target resource to scale.
type ScaleTarget struct {
	// Namespace of the target.
	Namespace string
	// Name of the target resource.
	Name string
	// Kind of the target resource (e.g., "Deployment").
	Kind string
	// APIVersion of the target resource.
	APIVersion string
	// SubTarget for role-based scaling.
	SubTarget string
}

// String returns a string representation of the ScaleTarget.
func (t ScaleTarget) String() string {
	key := t.APIVersion + "." + t.Kind + "/" + t.Namespace + "/" + t.Name
	if t.SubTarget != "" {
		key += "/" + t.SubTarget
	}
	return key
}

// ScaleDecision represents a scaling decision.
type ScaleDecision struct {
	// DesiredReplicas is the target number of replicas.
	DesiredReplicas int32
	// CurrentReplicas is the current number of replicas.
	CurrentReplicas int32
	// ShouldScale indicates whether scaling should occur.
	ShouldScale bool
	// Reason explains the scaling decision.
	Reason string
	// Algorithm is the algorithm that made the decision.
	Algorithm string
	// Timestamp is when the decision was made.
	Timestamp time.Time
	// Confidence is the confidence level of the decision (0-1).
	Confidence float64
}

// GPUMetrics contains GPU utilization data.
type GPUMetrics struct {
	// MemoryUtilization is the GPU memory utilization percentage (0-100).
	MemoryUtilization float64
	// ComputeUtilization is the GPU compute utilization percentage (0-100).
	ComputeUtilization float64
	// MemoryUsedBytes is the GPU memory used in bytes.
	MemoryUsedBytes int64
	// MemoryTotalBytes is the total GPU memory in bytes.
	MemoryTotalBytes int64
	// GPUType is the type of GPU (e.g., "nvidia-a100").
	GPUType string
	// GPUCount is the number of GPUs.
	GPUCount int32
	// PerGPUMetrics contains per-GPU metrics.
	PerGPUMetrics []PerGPUMetric
	// Timestamp is when the metrics were collected.
	Timestamp time.Time
}

// PerGPUMetric contains metrics for a single GPU.
type PerGPUMetric struct {
	// Index is the GPU index.
	Index int
	// UUID is the GPU UUID.
	UUID string
	// MemoryUtilization is the memory utilization percentage.
	MemoryUtilization float64
	// ComputeUtilization is the compute utilization percentage.
	ComputeUtilization float64
	// Temperature is the GPU temperature in Celsius.
	Temperature float64
}

// CostMetrics contains cost-related metrics.
type CostMetrics struct {
	// CurrentCostPerHour is the current hourly cost.
	CurrentCostPerHour float64
	// BudgetPerHour is the budget limit per hour.
	BudgetPerHour float64
	// PerReplicaCostPerHour is the cost per replica per hour.
	PerReplicaCostPerHour float64
	// DailyCost is the projected daily cost.
	DailyCost float64
	// Currency is the currency code (e.g., "USD").
	Currency string
	// SpotInstanceCount is the number of spot instances.
	SpotInstanceCount int
	// OnDemandInstanceCount is the number of on-demand instances.
	OnDemandInstanceCount int
	// BudgetRemaining is the remaining budget.
	BudgetRemaining float64
	// Timestamp is when the metrics were collected.
	Timestamp time.Time
}

// PredictionData contains prediction-related data.
type PredictionData struct {
	// PredictedReplicas is the predicted number of replicas needed.
	PredictedReplicas int32
	// Confidence is the prediction confidence (0-1).
	Confidence float64
	// PredictionTime is when the prediction was made.
	PredictionTime time.Time
	// LookAheadMinutes is how far ahead the prediction looks.
	LookAheadMinutes int
	// Reason explains the prediction.
	Reason string
	// Source indicates the prediction source.
	Source PredictionSource
}

// PredictionSource indicates what the prediction is based on.
type PredictionSource string

const (
	// PredictionSourceHistorical uses historical patterns.
	PredictionSourceHistorical PredictionSource = "historical"
	// PredictionSourceTimeSeries uses time-series forecasting.
	PredictionSourceTimeSeries PredictionSource = "timeseries"
	// PredictionSourceSchedule uses schedule hints.
	PredictionSourceSchedule PredictionSource = "schedule"
	// PredictionSourceLearned uses learned behavior.
	PredictionSourceLearned PredictionSource = "learned"
)

// MetricPoint represents a single point in time-series data.
type MetricPoint struct {
	// Timestamp of the data point.
	Timestamp time.Time
	// Value of the metric.
	Value float64
}

// PodInfo contains information about a pod for metric collection.
type PodInfo struct {
	// Pod is the Kubernetes pod.
	Pod *corev1.Pod
	// IP is the pod IP address.
	IP string
	// Ready indicates if the pod is ready.
	Ready bool
	// Name is the pod name.
	Name string
	// Namespace is the pod namespace.
	Namespace string
}

// NewPodInfo creates a PodInfo from a Kubernetes pod.
func NewPodInfo(pod *corev1.Pod) PodInfo {
	ready := false
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			ready = true
			break
		}
	}
	return PodInfo{
		Pod:       pod,
		IP:        pod.Status.PodIP,
		Ready:     ready,
		Name:      pod.Name,
		Namespace: pod.Namespace,
	}
}

// ClusterInfo contains information about a cluster for multi-cluster operations.
type ClusterInfo struct {
	// Name is the cluster name.
	Name string
	// Endpoint is the API server endpoint.
	Endpoint string
	// Region is the cloud region.
	Region string
	// IsHealthy indicates if the cluster is healthy.
	IsHealthy bool
	// CurrentLoad is the current load (0-1).
	CurrentLoad float64
	// AvailableGPUs is the number of available GPUs.
	AvailableGPUs int32
	// Weight is the load distribution weight.
	Weight int32
}

// ClusterScaleStatus contains scaling status for a cluster.
type ClusterScaleStatus struct {
	// ClusterName is the name of the cluster.
	ClusterName string
	// CurrentReplicas is the current number of replicas.
	CurrentReplicas int32
	// DesiredReplicas is the desired number of replicas.
	DesiredReplicas int32
	// LastUpdated is when the status was last updated.
	LastUpdated time.Time
	// Error contains any error message.
	Error string
}
