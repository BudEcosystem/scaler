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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=bas
// +kubebuilder:printcolumn:name="MIN",type="integer",JSONPath=".spec.minReplicas"
// +kubebuilder:printcolumn:name="MAX",type="integer",JSONPath=".spec.maxReplicas"
// +kubebuilder:printcolumn:name="REPLICAS",type="integer",JSONPath=".status.actualScale"
// +kubebuilder:printcolumn:name="STRATEGY",type="string",JSONPath=".spec.scalingStrategy"
// +kubebuilder:printcolumn:name="READY",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"
// +kubebuilder:printcolumn:name="AGE",type="date",JSONPath=".metadata.creationTimestamp"

// BudAIScaler is the Schema for the budaiscalers API.
// It provides autoscaling for GenAI workloads with support for GPU-aware,
// cost-aware, and predictive scaling.
type BudAIScaler struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BudAIScalerSpec   `json:"spec,omitempty"`
	Status BudAIScalerStatus `json:"status,omitempty"`
}

// BudAIScalerSpec defines the desired state of BudAIScaler.
type BudAIScalerSpec struct {
	// ScaleTargetRef points to the target resource to scale.
	// +kubebuilder:validation:Required
	ScaleTargetRef corev1.ObjectReference `json:"scaleTargetRef"`

	// SubTargetSelector selects a sub-component within the target resource.
	// For example, a specific role in a StormService.
	// +optional
	SubTargetSelector *SubTargetSelector `json:"subTargetSelector,omitempty"`

	// MinReplicas is the minimum number of replicas.
	// Defaults to 1 if not specified.
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	MinReplicas *int32 `json:"minReplicas,omitempty"`

	// MaxReplicas is the maximum number of replicas.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	MaxReplicas int32 `json:"maxReplicas"`

	// ScalingStrategy defines the algorithm to use for scaling decisions.
	// +kubebuilder:validation:Enum={HPA,KPA,BudScaler}
	// +kubebuilder:default=BudScaler
	ScalingStrategy ScalingStrategyType `json:"scalingStrategy"`

	// MetricsSources defines the metrics used for scaling decisions.
	// At least one metric source must be specified.
	// +kubebuilder:validation:MinItems=1
	MetricsSources []MetricSource `json:"metricsSources"`

	// GPUConfig configures GPU-aware scaling behavior.
	// +optional
	GPUConfig *GPUScalingConfig `json:"gpuConfig,omitempty"`

	// CostConfig configures cost-aware scaling behavior.
	// +optional
	CostConfig *CostConfig `json:"costConfig,omitempty"`

	// PredictionConfig configures predictive scaling behavior.
	// +optional
	PredictionConfig *PredictionConfig `json:"predictionConfig,omitempty"`

	// ScheduleHints defines known traffic patterns for scheduled scaling.
	// These work independently of PredictionConfig and can be used for
	// deterministic schedule-based scaling without ML predictions.
	// +optional
	ScheduleHints []ScheduleHint `json:"scheduleHints,omitempty"`

	// MultiClusterConfig configures multi-cluster scaling.
	// +optional
	MultiClusterConfig *MultiClusterConfig `json:"multiClusterConfig,omitempty"`

	// Behavior configures the scaling behavior for scale up and scale down.
	// +optional
	Behavior *ScalingBehavior `json:"behavior,omitempty"`
}

// ScalingStrategyType defines the type of scaling algorithm.
type ScalingStrategyType string

const (
	// HPA uses a Kubernetes HorizontalPodAutoscaler wrapper.
	// Creates and manages an actual HPA resource.
	HPA ScalingStrategyType = "HPA"

	// KPA uses KNative-style Pod Autoscaling with panic and stable windows.
	// Good for bursty workloads that need rapid scale-up.
	KPA ScalingStrategyType = "KPA"

	// BudScaler uses a custom algorithm optimized for GenAI workloads.
	// Integrates GPU, cost, and prediction awareness.
	BudScaler ScalingStrategyType = "BudScaler"
)

// MetricSourceType defines the type of metric source.
type MetricSourceType string

const (
	// MetricSourcePod fetches metrics from pod endpoints (http://pod_ip:port/path).
	MetricSourcePod MetricSourceType = "pod"

	// MetricSourceResource fetches from Kubernetes resource metrics API (cpu, memory).
	MetricSourceResource MetricSourceType = "resource"

	// MetricSourceCustom fetches from Kubernetes custom metrics API.
	MetricSourceCustom MetricSourceType = "custom"

	// MetricSourceExternal fetches from external services.
	MetricSourceExternal MetricSourceType = "external"

	// MetricSourcePrometheus fetches via direct PromQL queries.
	MetricSourcePrometheus MetricSourceType = "prometheus"

	// MetricSourceInferenceEngine fetches engine-specific metrics (vLLM, TGI, etc.).
	MetricSourceInferenceEngine MetricSourceType = "inferenceEngine"
)

// ProtocolType defines the protocol for metric fetching.
type ProtocolType string

const (
	// HTTP uses HTTP protocol.
	HTTP ProtocolType = "http"
	// HTTPS uses HTTPS protocol.
	HTTPS ProtocolType = "https"
)

// MetricSource defines a metric source configuration.
type MetricSource struct {
	// MetricSourceType specifies how to fetch metrics.
	// +kubebuilder:validation:Enum={pod,resource,custom,external,prometheus,inferenceEngine}
	MetricSourceType MetricSourceType `json:"metricSourceType"`

	// TargetMetric identifies the specific metric to monitor.
	// For resource type: "cpu" or "memory".
	// For inference engines: "gpu_cache_usage_perc", "num_requests_waiting", etc.
	// +kubebuilder:validation:Required
	TargetMetric string `json:"targetMetric"`

	// TargetValue sets the desired threshold for the metric.
	// The format depends on the metric type (e.g., "80" for 80% utilization).
	// +kubebuilder:validation:Required
	TargetValue string `json:"targetValue"`

	// ProtocolType for pod and external metric sources.
	// +optional
	// +kubebuilder:validation:Enum={http,https}
	ProtocolType ProtocolType `json:"protocolType,omitempty"`

	// Endpoint for external or prometheus sources.
	// For prometheus: the Prometheus server URL.
	// For external: the service endpoint.
	// +optional
	Endpoint string `json:"endpoint,omitempty"`

	// Path to the metrics endpoint.
	// For pod type: the path on the pod (e.g., "/metrics").
	// +optional
	Path string `json:"path,omitempty"`

	// Port for pod-level metrics.
	// +optional
	Port string `json:"port,omitempty"`

	// PromQL query for prometheus source type.
	// The query should return a single value or per-pod values.
	// +optional
	PromQL string `json:"promQL,omitempty"`

	// Headers to include in requests to external endpoints.
	// Useful for authentication or custom headers.
	// +optional
	Headers map[string]string `json:"headers,omitempty"`

	// InferenceEngineConfig for engine-specific metrics.
	// +optional
	InferenceEngineConfig *InferenceEngineConfig `json:"inferenceEngineConfig,omitempty"`
}

// InferenceEngineType defines the type of inference engine.
type InferenceEngineType string

const (
	// InferenceEngineVLLM is the vLLM inference engine.
	InferenceEngineVLLM InferenceEngineType = "vllm"
	// InferenceEngineTGI is the Text Generation Inference engine.
	InferenceEngineTGI InferenceEngineType = "tgi"
	// InferenceEngineSGLang is the SGLang inference engine.
	InferenceEngineSGLang InferenceEngineType = "sglang"
	// InferenceEngineTriton is the NVIDIA Triton inference server.
	InferenceEngineTriton InferenceEngineType = "triton"
)

// InferenceEngineConfig configures inference engine metric collection.
type InferenceEngineConfig struct {
	// EngineType specifies the inference engine type.
	// +kubebuilder:validation:Enum={vllm,tgi,sglang,triton}
	EngineType InferenceEngineType `json:"engineType"`

	// MetricsPort for the engine metrics endpoint.
	// +optional
	// +kubebuilder:default=8000
	MetricsPort int32 `json:"metricsPort,omitempty"`

	// MetricsPath for the engine metrics endpoint.
	// +optional
	// +kubebuilder:default="/metrics"
	MetricsPath string `json:"metricsPath,omitempty"`
}

// SubTargetSelector identifies a sub-component within the scale target.
type SubTargetSelector struct {
	// RoleName for role-based scaling (e.g., in StormService).
	// +optional
	RoleName string `json:"roleName,omitempty"`
}

// GPUScalingConfig configures GPU-aware scaling behavior.
type GPUScalingConfig struct {
	// Enabled turns on GPU-aware scaling.
	// +optional
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// GPUMemoryThreshold triggers scaling when GPU memory utilization exceeds this percentage.
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	GPUMemoryThreshold *int32 `json:"gpuMemoryThreshold,omitempty"`

	// GPUComputeThreshold triggers scaling when GPU compute utilization exceeds this percentage.
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	GPUComputeThreshold *int32 `json:"gpuComputeThreshold,omitempty"`

	// TopologyAware enables topology-aware GPU placement.
	// +optional
	// +kubebuilder:default=false
	TopologyAware bool `json:"topologyAware,omitempty"`

	// PreferredGPUType specifies the preferred GPU type for scaling.
	// Examples: "nvidia-a100", "nvidia-h100", "nvidia-l4".
	// +optional
	PreferredGPUType string `json:"preferredGPUType,omitempty"`

	// VGPUSupport enables vGPU/time-slicing awareness.
	// +optional
	// +kubebuilder:default=false
	VGPUSupport bool `json:"vgpuSupport,omitempty"`
}

// CostConfig configures cost-aware scaling behavior.
type CostConfig struct {
	// Enabled turns on cost-aware scaling.
	// +optional
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// CloudProvider for cost integration.
	// +optional
	// +kubebuilder:validation:Enum={aws,gcp,azure}
	CloudProvider string `json:"cloudProvider,omitempty"`

	// BudgetPerHour is the maximum hourly spend for this workload.
	// +optional
	BudgetPerHour *resource.Quantity `json:"budgetPerHour,omitempty"`

	// BudgetPerDay is the maximum daily spend for this workload.
	// +optional
	BudgetPerDay *resource.Quantity `json:"budgetPerDay,omitempty"`

	// PreferSpotInstances enables spot instance preference.
	// +optional
	// +kubebuilder:default=false
	PreferSpotInstances bool `json:"preferSpotInstances,omitempty"`

	// SpotFallbackToOnDemand allows fallback to on-demand when spot is unavailable.
	// +optional
	// +kubebuilder:default=true
	SpotFallbackToOnDemand bool `json:"spotFallbackToOnDemand,omitempty"`

	// CostOptimizationMode controls the cost/performance trade-off.
	// +optional
	// +kubebuilder:validation:Enum={balanced,cost-optimized,performance}
	// +kubebuilder:default=balanced
	CostOptimizationMode string `json:"costOptimizationMode,omitempty"`
}

// PredictionConfig configures predictive scaling behavior.
type PredictionConfig struct {
	// Enabled turns on predictive scaling.
	// +optional
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// LookAheadMinutes specifies how far ahead to predict for proactive scaling.
	// +optional
	// +kubebuilder:default=15
	// +kubebuilder:validation:Minimum=5
	// +kubebuilder:validation:Maximum=60
	LookAheadMinutes *int32 `json:"lookAheadMinutes,omitempty"`

	// HistoricalDataDays specifies how many days of history to use for pattern analysis.
	// +optional
	// +kubebuilder:default=7
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=90
	HistoricalDataDays *int32 `json:"historicalDataDays,omitempty"`

	// EnableLearning allows the system to learn from past scaling decisions.
	// +optional
	// +kubebuilder:default=true
	EnableLearning bool `json:"enableLearning,omitempty"`

	// PredictionMetrics specifies which metrics from metricsSources to use for
	// time-series prediction. Reference metrics by their targetMetric name.
	// If not specified, defaults to the first metric in metricsSources.
	// +optional
	PredictionMetrics []string `json:"predictionMetrics,omitempty"`

	// SeasonalMetrics specifies which metrics from metricsSources to track for
	// seasonal pattern learning (hourly/daily patterns). Reference metrics by
	// their targetMetric name. If not specified, defaults to predictionMetrics.
	// +optional
	SeasonalMetrics []string `json:"seasonalMetrics,omitempty"`
}

// ScheduleHint defines a known traffic pattern for scheduled scaling.
type ScheduleHint struct {
	// Name is an identifier for this schedule hint.
	Name string `json:"name"`

	// CronExpression specifies when to apply this hint.
	// Format: "minute hour day-of-month month day-of-week"
	// Example: "0 9 * * 1-5" for weekday mornings at 9 AM.
	CronExpression string `json:"cronExpression"`

	// TargetReplicas is the number of replicas to scale to.
	TargetReplicas int32 `json:"targetReplicas"`

	// Duration specifies how long to maintain this replica count.
	Duration metav1.Duration `json:"duration"`
}

// MultiClusterConfig configures multi-cluster scaling.
type MultiClusterConfig struct {
	// Enabled turns on multi-cluster support.
	// +optional
	// +kubebuilder:default=false
	Enabled bool `json:"enabled,omitempty"`

	// FederationMode defines how replicas are distributed across clusters.
	// +optional
	// +kubebuilder:validation:Enum={active-active,active-passive,load-balanced}
	// +kubebuilder:default=load-balanced
	FederationMode string `json:"federationMode,omitempty"`

	// TargetClusters is the list of cluster names to scale across.
	// +optional
	TargetClusters []string `json:"targetClusters,omitempty"`

	// ClusterWeights maps cluster names to their load distribution weights.
	// Higher weight means more replicas.
	// +optional
	ClusterWeights map[string]int32 `json:"clusterWeights,omitempty"`

	// FailoverEnabled allows automatic failover to other clusters.
	// +optional
	// +kubebuilder:default=true
	FailoverEnabled bool `json:"failoverEnabled,omitempty"`
}

// ScalingBehavior configures the scaling behavior.
type ScalingBehavior struct {
	// ScaleUp configures scale-up behavior.
	// +optional
	ScaleUp *ScalingRules `json:"scaleUp,omitempty"`

	// ScaleDown configures scale-down behavior.
	// +optional
	ScaleDown *ScalingRules `json:"scaleDown,omitempty"`
}

// ScalingPolicyType defines the type of scaling policy.
type ScalingPolicyType string

const (
	// PodsScalingPolicy scales by a fixed number of pods.
	PodsScalingPolicy ScalingPolicyType = "Pods"

	// PercentScalingPolicy scales by a percentage of current replicas.
	PercentScalingPolicy ScalingPolicyType = "Percent"
)

// ScalingPolicySelect defines how to select which policy to use.
type ScalingPolicySelect string

const (
	// MaxChangePolicySelect selects the policy that results in the highest change.
	MaxChangePolicySelect ScalingPolicySelect = "Max"

	// MinChangePolicySelect selects the policy that results in the lowest change.
	MinChangePolicySelect ScalingPolicySelect = "Min"

	// DisabledPolicySelect disables scaling in this direction.
	DisabledPolicySelect ScalingPolicySelect = "Disabled"
)

// ScalingPolicy defines a single scaling policy.
type ScalingPolicy struct {
	// Type specifies the type of scaling policy.
	// +kubebuilder:validation:Enum={Pods,Percent}
	Type ScalingPolicyType `json:"type"`

	// Value is the amount to scale.
	// For Pods type: the number of pods to add/remove.
	// For Percent type: the percentage of current replicas.
	// +kubebuilder:validation:Minimum=0
	Value int32 `json:"value"`

	// PeriodSeconds is the time window for this policy.
	// The policy allows at most Value pods/percent change within this period.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1800
	PeriodSeconds int32 `json:"periodSeconds"`
}

// ScalingRules defines rules for scaling in a particular direction.
type ScalingRules struct {
	// StabilizationWindowSeconds is the number of seconds to look back
	// when calculating desired replicas to avoid flapping.
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=3600
	StabilizationWindowSeconds *int32 `json:"stabilizationWindowSeconds,omitempty"`

	// SelectPolicy determines which policy to use when multiple policies apply.
	// Defaults to Max for scale-up and Min for scale-down.
	// +optional
	// +kubebuilder:validation:Enum={Max,Min,Disabled}
	SelectPolicy *ScalingPolicySelect `json:"selectPolicy,omitempty"`

	// Policies is a list of scaling policies.
	// +optional
	Policies []ScalingPolicy `json:"policies,omitempty"`

	// MaxScaleRate is the maximum scaling rate (deprecated, use Policies instead).
	// For example, 2.0 means the replica count can double in one step.
	// +optional
	MaxScaleRate *string `json:"maxScaleRate,omitempty"`

	// Tolerance is the threshold for metric fluctuations before scaling.
	// For example, 0.1 means 10% fluctuation is tolerated.
	// +optional
	Tolerance *string `json:"tolerance,omitempty"`
}

// BudAIScalerStatus defines the observed state of BudAIScaler.
type BudAIScalerStatus struct {
	// LastScaleTime is the last time the scaler changed the replica count.
	// +optional
	LastScaleTime *metav1.Time `json:"lastScaleTime,omitempty"`

	// DesiredScale is the computed desired number of replicas.
	DesiredScale int32 `json:"desiredScale,omitempty"`

	// ActualScale is the current number of running replicas.
	ActualScale int32 `json:"actualScale,omitempty"`

	// Conditions represent the current state of the scaler.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ScalingHistory stores recent scaling decisions for debugging.
	// +optional
	// +kubebuilder:validation:MaxItems=10
	ScalingHistory []ScalingDecision `json:"scalingHistory,omitempty"`

	// GPUStatus contains current GPU utilization information.
	// +optional
	GPUStatus *GPUStatus `json:"gpuStatus,omitempty"`

	// CostStatus contains current cost information.
	// +optional
	CostStatus *CostStatus `json:"costStatus,omitempty"`

	// PredictionStatus contains current prediction state.
	// +optional
	PredictionStatus *PredictionStatus `json:"predictionStatus,omitempty"`

	// LearningStatus contains adaptive learning system state.
	// +optional
	LearningStatus *LearningStatus `json:"learningStatus,omitempty"`

	// MultiClusterStatus contains federation status.
	// +optional
	MultiClusterStatus *MultiClusterStatus `json:"multiClusterStatus,omitempty"`
}

// ScalingDecision records a single scaling decision.
type ScalingDecision struct {
	// Timestamp when the scaling decision was made.
	Timestamp metav1.Time `json:"timestamp"`

	// PreviousScale is the number of replicas before scaling.
	PreviousScale int32 `json:"previousScale"`

	// NewScale is the number of replicas after scaling.
	NewScale int32 `json:"newScale"`

	// Reason explains why the scaling decision was made.
	Reason string `json:"reason"`

	// Algorithm is the algorithm that made the decision.
	Algorithm string `json:"algorithm"`

	// Success indicates whether the scaling operation succeeded.
	Success bool `json:"success"`

	// Error contains any error message if scaling failed.
	// +optional
	Error string `json:"error,omitempty"`
}

// GPUStatus contains GPU utilization information.
type GPUStatus struct {
	// AverageMemoryUtilization is the average GPU memory utilization percentage.
	AverageMemoryUtilization int32 `json:"averageMemoryUtilization,omitempty"`

	// AverageComputeUtilization is the average GPU compute utilization percentage.
	AverageComputeUtilization int32 `json:"averageComputeUtilization,omitempty"`

	// TotalGPUs is the total number of GPUs in use.
	TotalGPUs int32 `json:"totalGPUs,omitempty"`

	// GPUType is the type of GPU in use.
	GPUType string `json:"gpuType,omitempty"`
}

// CostStatus contains cost information.
type CostStatus struct {
	// CurrentHourlyCost is the current hourly cost.
	CurrentHourlyCost string `json:"currentHourlyCost,omitempty"`

	// ProjectedDailyCost is the projected daily cost.
	ProjectedDailyCost string `json:"projectedDailyCost,omitempty"`

	// BudgetUtilization is the percentage of budget used.
	BudgetUtilization int32 `json:"budgetUtilization,omitempty"`

	// SpotInstancesInUse is the number of spot instances currently in use.
	SpotInstancesInUse int32 `json:"spotInstancesInUse,omitempty"`

	// OnDemandInstancesInUse is the number of on-demand instances in use.
	OnDemandInstancesInUse int32 `json:"onDemandInstancesInUse,omitempty"`
}

// PredictionStatus contains prediction state.
type PredictionStatus struct {
	// PredictedReplicas is the predicted number of replicas needed.
	PredictedReplicas int32 `json:"predictedReplicas,omitempty"`

	// PredictionConfidence is the confidence level of the prediction (0-1).
	PredictionConfidence string `json:"predictionConfidence,omitempty"`

	// NextPredictedChange is when the next scaling change is predicted.
	// +optional
	NextPredictedChange *metav1.Time `json:"nextPredictedChange,omitempty"`

	// ActiveScheduleHint is the currently active schedule hint, if any.
	ActiveScheduleHint string `json:"activeScheduleHint,omitempty"`
}

// LearningStatus contains adaptive learning system state.
type LearningStatus struct {
	// DataPointsCollected is the total number of data points collected for learning.
	DataPointsCollected int32 `json:"dataPointsCollected,omitempty"`

	// OldestDataPoint is the timestamp of the oldest data point.
	// +optional
	OldestDataPoint *metav1.Time `json:"oldestDataPoint,omitempty"`

	// OverallAccuracy is the overall prediction accuracy percentage.
	OverallAccuracy string `json:"overallAccuracy,omitempty"`

	// DirectionAccuracy is the accuracy of scale direction predictions.
	DirectionAccuracy string `json:"directionAccuracy,omitempty"`

	// RecentMAPE is the recent Mean Absolute Percentage Error.
	RecentMAPE string `json:"recentMAPE,omitempty"`

	// RecentMAE is the recent Mean Absolute Error (in replica count).
	RecentMAE string `json:"recentMAE,omitempty"`

	// SeasonalDataComplete indicates if all 168 time buckets have sufficient data.
	SeasonalDataComplete bool `json:"seasonalDataComplete,omitempty"`

	// CurrentSeasonalFactor is the current seasonal adjustment factor.
	CurrentSeasonalFactor string `json:"currentSeasonalFactor,omitempty"`

	// DetectedPattern is the currently detected LLM workload pattern.
	DetectedPattern string `json:"detectedPattern,omitempty"`

	// PatternConfidence is the confidence level of the detected pattern (0-1).
	PatternConfidence string `json:"patternConfidence,omitempty"`

	// LastCalibration is when the learning system was last calibrated.
	// +optional
	LastCalibration *metav1.Time `json:"lastCalibration,omitempty"`

	// CalibrationNeeded indicates if calibration is recommended.
	CalibrationNeeded bool `json:"calibrationNeeded,omitempty"`

	// ConfigMapName is the name of the ConfigMap storing learning data.
	ConfigMapName string `json:"configMapName,omitempty"`

	// StorageUsed is the approximate storage used for learning data.
	StorageUsed string `json:"storageUsed,omitempty"`
}

// MultiClusterStatus contains federation status.
type MultiClusterStatus struct {
	// TotalReplicasAcrossClusters is the total replicas across all clusters.
	TotalReplicasAcrossClusters int32 `json:"totalReplicasAcrossClusters,omitempty"`

	// ClusterDistribution maps cluster names to their replica counts.
	ClusterDistribution map[string]int32 `json:"clusterDistribution,omitempty"`

	// HealthyClusters is the list of healthy clusters.
	HealthyClusters []string `json:"healthyClusters,omitempty"`

	// UnhealthyClusters is the list of unhealthy clusters.
	UnhealthyClusters []string `json:"unhealthyClusters,omitempty"`
}

// Condition types for BudAIScaler.
const (
	// ConditionReady indicates the scaler is ready to make scaling decisions.
	ConditionReady = "Ready"

	// ConditionValidSpec indicates the spec is valid.
	ConditionValidSpec = "ValidSpec"

	// ConditionScalingActive indicates scaling is actively happening.
	ConditionScalingActive = "ScalingActive"

	// ConditionAbleToScale indicates the scaler can perform scaling.
	ConditionAbleToScale = "AbleToScale"

	// ConditionConflict indicates there is a conflict with another scaler.
	ConditionConflict = "Conflict"
)

// +kubebuilder:object:root=true

// BudAIScalerList contains a list of BudAIScaler.
type BudAIScalerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BudAIScaler `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BudAIScaler{}, &BudAIScalerList{})
}

// GetMinReplicas returns the minimum replicas, defaulting to 1.
func (s *BudAIScalerSpec) GetMinReplicas() int32 {
	if s.MinReplicas == nil {
		return 1
	}
	return *s.MinReplicas
}

// GetScaleUpStabilizationWindow returns the scale-up stabilization window.
func (s *BudAIScalerSpec) GetScaleUpStabilizationWindow() int32 {
	if s.Behavior == nil || s.Behavior.ScaleUp == nil || s.Behavior.ScaleUp.StabilizationWindowSeconds == nil {
		return 0 // Default: no stabilization for scale-up
	}
	return *s.Behavior.ScaleUp.StabilizationWindowSeconds
}

// GetScaleDownStabilizationWindow returns the scale-down stabilization window.
func (s *BudAIScalerSpec) GetScaleDownStabilizationWindow() int32 {
	if s.Behavior == nil || s.Behavior.ScaleDown == nil || s.Behavior.ScaleDown.StabilizationWindowSeconds == nil {
		return 300 // Default: 5 minutes for scale-down
	}
	return *s.Behavior.ScaleDown.StabilizationWindowSeconds
}
