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
	"fmt"
	"strconv"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	scalerv1alpha1 "github.com/BudEcosystem/scaler/api/scaler/v1alpha1"
)

// HPABuilder builds Kubernetes HPA resources from BudAIScaler specs.
// Unlike KPA and BudScaler, HPA delegates scaling decisions to the native K8s HPA.
type HPABuilder struct{}

// NewHPABuilder creates a new HPABuilder.
func NewHPABuilder() *HPABuilder {
	return &HPABuilder{}
}

// BuildHPA creates an HPA resource from a BudAIScaler.
func (b *HPABuilder) BuildHPA(scaler *scalerv1alpha1.BudAIScaler) (*autoscalingv2.HorizontalPodAutoscaler, error) {
	if scaler == nil {
		return nil, fmt.Errorf("scaler is required")
	}

	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      scaler.Name + "-hpa",
			Namespace: scaler.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "budaiscaler",
				"scaler.bud.studio/scaler":     scaler.Name,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: scaler.APIVersion,
					Kind:       scaler.Kind,
					Name:       scaler.Name,
					UID:        scaler.UID,
					Controller: boolPtr(true),
				},
			},
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: scaler.Spec.ScaleTargetRef.APIVersion,
				Kind:       scaler.Spec.ScaleTargetRef.Kind,
				Name:       scaler.Spec.ScaleTargetRef.Name,
			},
			MinReplicas: scaler.Spec.MinReplicas,
			MaxReplicas: scaler.Spec.MaxReplicas,
		},
	}

	// Build metrics
	metrics, err := b.buildMetrics(scaler.Spec.MetricsSources)
	if err != nil {
		return nil, fmt.Errorf("failed to build metrics: %w", err)
	}
	hpa.Spec.Metrics = metrics

	// Build behavior if specified
	if scaler.Spec.Behavior != nil {
		hpa.Spec.Behavior = b.buildBehavior(scaler.Spec.Behavior)
	}

	return hpa, nil
}

// buildMetrics converts BudAIScaler metric sources to HPA metrics.
func (b *HPABuilder) buildMetrics(sources []scalerv1alpha1.MetricSource) ([]autoscalingv2.MetricSpec, error) {
	var metrics []autoscalingv2.MetricSpec

	for _, source := range sources {
		metric, err := b.buildMetricSpec(source)
		if err != nil {
			// Skip unsupported metrics
			continue
		}
		metrics = append(metrics, *metric)
	}

	if len(metrics) == 0 {
		return nil, fmt.Errorf("no valid metrics could be converted to HPA format")
	}

	return metrics, nil
}

// buildMetricSpec converts a single metric source to HPA metric spec.
func (b *HPABuilder) buildMetricSpec(source scalerv1alpha1.MetricSource) (*autoscalingv2.MetricSpec, error) {
	switch source.MetricSourceType {
	case scalerv1alpha1.MetricSourceResource:
		return b.buildResourceMetric(source)
	case scalerv1alpha1.MetricSourceCustom:
		return b.buildPodMetric(source)
	case scalerv1alpha1.MetricSourceExternal:
		return b.buildExternalMetric(source)
	default:
		return nil, fmt.Errorf("metric source type %s not supported by HPA", source.MetricSourceType)
	}
}

// buildResourceMetric creates a resource metric spec.
func (b *HPABuilder) buildResourceMetric(source scalerv1alpha1.MetricSource) (*autoscalingv2.MetricSpec, error) {
	// Parse target as percentage
	targetValue, err := strconv.ParseInt(source.TargetValue, 10, 32)
	if err != nil {
		return nil, fmt.Errorf("failed to parse target value: %w", err)
	}

	resourceName := corev1.ResourceName(source.TargetMetric)
	if source.TargetMetric == "cpu" {
		resourceName = corev1.ResourceCPU
	} else if source.TargetMetric == "memory" {
		resourceName = corev1.ResourceMemory
	}

	return &autoscalingv2.MetricSpec{
		Type: autoscalingv2.ResourceMetricSourceType,
		Resource: &autoscalingv2.ResourceMetricSource{
			Name: resourceName,
			Target: autoscalingv2.MetricTarget{
				Type:               autoscalingv2.UtilizationMetricType,
				AverageUtilization: int32Ptr(int32(targetValue)),
			},
		},
	}, nil
}

// buildPodMetric creates a pod metric spec.
func (b *HPABuilder) buildPodMetric(source scalerv1alpha1.MetricSource) (*autoscalingv2.MetricSpec, error) {
	targetValue, err := resource.ParseQuantity(source.TargetValue)
	if err != nil {
		return nil, fmt.Errorf("failed to parse target value: %w", err)
	}

	return &autoscalingv2.MetricSpec{
		Type: autoscalingv2.PodsMetricSourceType,
		Pods: &autoscalingv2.PodsMetricSource{
			Metric: autoscalingv2.MetricIdentifier{
				Name: source.TargetMetric,
			},
			Target: autoscalingv2.MetricTarget{
				Type:         autoscalingv2.AverageValueMetricType,
				AverageValue: &targetValue,
			},
		},
	}, nil
}

// buildExternalMetric creates an external metric spec.
func (b *HPABuilder) buildExternalMetric(source scalerv1alpha1.MetricSource) (*autoscalingv2.MetricSpec, error) {
	targetValue, err := resource.ParseQuantity(source.TargetValue)
	if err != nil {
		return nil, fmt.Errorf("failed to parse target value: %w", err)
	}

	return &autoscalingv2.MetricSpec{
		Type: autoscalingv2.ExternalMetricSourceType,
		External: &autoscalingv2.ExternalMetricSource{
			Metric: autoscalingv2.MetricIdentifier{
				Name: source.TargetMetric,
			},
			Target: autoscalingv2.MetricTarget{
				Type:  autoscalingv2.ValueMetricType,
				Value: &targetValue,
			},
		},
	}, nil
}

// buildBehavior converts BudAIScaler behavior to HPA behavior.
func (b *HPABuilder) buildBehavior(behavior *scalerv1alpha1.ScalingBehavior) *autoscalingv2.HorizontalPodAutoscalerBehavior {
	hpaBehavior := &autoscalingv2.HorizontalPodAutoscalerBehavior{}

	if behavior.ScaleUp != nil {
		hpaBehavior.ScaleUp = &autoscalingv2.HPAScalingRules{}

		if behavior.ScaleUp.StabilizationWindowSeconds != nil {
			hpaBehavior.ScaleUp.StabilizationWindowSeconds = behavior.ScaleUp.StabilizationWindowSeconds
		}
	}

	if behavior.ScaleDown != nil {
		hpaBehavior.ScaleDown = &autoscalingv2.HPAScalingRules{}

		if behavior.ScaleDown.StabilizationWindowSeconds != nil {
			hpaBehavior.ScaleDown.StabilizationWindowSeconds = behavior.ScaleDown.StabilizationWindowSeconds
		}
	}

	return hpaBehavior
}

// UpdateHPASpec updates an existing HPA with new spec from BudAIScaler.
func (b *HPABuilder) UpdateHPASpec(hpa *autoscalingv2.HorizontalPodAutoscaler, scaler *scalerv1alpha1.BudAIScaler) error {
	if hpa == nil || scaler == nil {
		return fmt.Errorf("hpa and scaler are required")
	}

	// Update scale target
	hpa.Spec.ScaleTargetRef = autoscalingv2.CrossVersionObjectReference{
		APIVersion: scaler.Spec.ScaleTargetRef.APIVersion,
		Kind:       scaler.Spec.ScaleTargetRef.Kind,
		Name:       scaler.Spec.ScaleTargetRef.Name,
	}

	// Update min/max replicas
	hpa.Spec.MinReplicas = scaler.Spec.MinReplicas
	hpa.Spec.MaxReplicas = scaler.Spec.MaxReplicas

	// Update metrics
	metrics, err := b.buildMetrics(scaler.Spec.MetricsSources)
	if err != nil {
		return err
	}
	hpa.Spec.Metrics = metrics

	// Update behavior
	if scaler.Spec.Behavior != nil {
		hpa.Spec.Behavior = b.buildBehavior(scaler.Spec.Behavior)
	} else {
		hpa.Spec.Behavior = nil
	}

	return nil
}

// Helper functions
func boolPtr(b bool) *bool {
	return &b
}

func int32Ptr(i int32) *int32 {
	return &i
}
