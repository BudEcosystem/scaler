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

package types

// Annotation prefix for BudAIScaler annotations.
const AnnotationPrefix = "scaler.bud.studio/"

// Scaling rate annotations.
const (
	// MaxScaleUpRateAnnotation defines the maximum scale-up rate.
	// Value format: float (e.g., "2.0" means can double in one step).
	MaxScaleUpRateAnnotation = AnnotationPrefix + "max-scale-up-rate"

	// MaxScaleDownRateAnnotation defines the maximum scale-down rate.
	// Value format: float (e.g., "2.0" means can halve in one step).
	MaxScaleDownRateAnnotation = AnnotationPrefix + "max-scale-down-rate"
)

// Tolerance annotations.
const (
	// ScaleUpToleranceAnnotation defines the tolerance for scale-up decisions.
	// Value format: float (e.g., "0.1" means 10% fluctuation is tolerated).
	ScaleUpToleranceAnnotation = AnnotationPrefix + "scale-up-tolerance"

	// ScaleDownToleranceAnnotation defines the tolerance for scale-down decisions.
	// Value format: float (e.g., "0.1" means 10% fluctuation is tolerated).
	ScaleDownToleranceAnnotation = AnnotationPrefix + "scale-down-tolerance"
)

// Window annotations.
const (
	// ScaleUpCooldownAnnotation defines the cooldown window for scale-up.
	// Value format: duration string (e.g., "30s", "1m").
	ScaleUpCooldownAnnotation = AnnotationPrefix + "scale-up-cooldown"

	// ScaleDownCooldownAnnotation defines the cooldown window for scale-down.
	// Value format: duration string (e.g., "5m", "300s").
	ScaleDownCooldownAnnotation = AnnotationPrefix + "scale-down-cooldown"
)

// KPA-specific annotations.
const (
	// PanicThresholdAnnotation defines the panic threshold for KPA.
	// Value format: float (e.g., "2.0" means panic if panic/stable > 2.0).
	PanicThresholdAnnotation = AnnotationPrefix + "panic-threshold"

	// PanicWindowAnnotation defines the panic window duration.
	// Value format: duration string (e.g., "60s").
	PanicWindowAnnotation = AnnotationPrefix + "panic-window"

	// StableWindowAnnotation defines the stable window duration.
	// Value format: duration string (e.g., "180s").
	StableWindowAnnotation = AnnotationPrefix + "stable-window"
)

// GPU annotations.
const (
	// GPUPreferredTypeAnnotation specifies the preferred GPU type.
	// Value format: string (e.g., "nvidia-a100", "nvidia-h100").
	GPUPreferredTypeAnnotation = AnnotationPrefix + "gpu-preferred-type"

	// GPUTopologyAwareAnnotation enables topology-aware GPU placement.
	// Value format: "true" or "false".
	GPUTopologyAwareAnnotation = AnnotationPrefix + "gpu-topology-aware"
)

// Cost annotations.
const (
	// CostMaxHourlyAnnotation defines the maximum hourly cost.
	// Value format: string with currency (e.g., "50", "100.50").
	CostMaxHourlyAnnotation = AnnotationPrefix + "cost-max-hourly"

	// CostMaxDailyAnnotation defines the maximum daily cost.
	// Value format: string with currency (e.g., "1000").
	CostMaxDailyAnnotation = AnnotationPrefix + "cost-max-daily"

	// CostPreferSpotAnnotation enables spot instance preference.
	// Value format: "true" or "false".
	CostPreferSpotAnnotation = AnnotationPrefix + "cost-prefer-spot"
)

// Metric-specific target annotations.
// Format: scaler.bud.studio/target-{metricName}
const (
	// TargetMetricAnnotationPrefix is the prefix for per-metric target values.
	// Usage: scaler.bud.studio/target-cpu, scaler.bud.studio/target-gpu_cache_usage_perc
	TargetMetricAnnotationPrefix = AnnotationPrefix + "target-"
)

// Scale-to-zero annotations.
const (
	// ScaleToZeroAnnotation enables scale-to-zero behavior.
	// Value format: "true" or "false".
	ScaleToZeroAnnotation = AnnotationPrefix + "scale-to-zero"

	// ActivationScaleAnnotation defines the minimum non-zero scale.
	// Value format: integer (e.g., "1", "2").
	ActivationScaleAnnotation = AnnotationPrefix + "activation-scale"
)

// Label constants.
const (
	// LabelPrefix is the prefix for BudAIScaler labels.
	LabelPrefix = "scaler.bud.studio/"

	// ManagedByLabel indicates the resource is managed by BudAIScaler.
	ManagedByLabel = LabelPrefix + "managed-by"

	// ScalerNameLabel contains the name of the BudAIScaler managing the resource.
	ScalerNameLabel = LabelPrefix + "scaler-name"
)
