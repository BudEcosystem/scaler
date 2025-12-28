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

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scalerv1alpha1 "github.com/BudEcosystem/scaler/api/scaler/v1alpha1"
)

// Scale represents the scale subresource.
type Scale struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ScaleSpec   `json:"spec,omitempty"`
	Status ScaleStatus `json:"status,omitempty"`
}

// ScaleSpec contains the spec of a Scale.
type ScaleSpec struct {
	Replicas int32 `json:"replicas,omitempty"`
}

// ScaleStatus contains the status of a Scale.
type ScaleStatus struct {
	Replicas int32  `json:"replicas,omitempty"`
	Selector string `json:"selector,omitempty"`
}

// WorkloadScale handles scaling operations for workloads.
type WorkloadScale struct {
	client     client.Client
	restMapper meta.RESTMapper
}

// NewWorkloadScale creates a new WorkloadScale.
func NewWorkloadScale(c client.Client) *WorkloadScale {
	return &WorkloadScale{
		client: c,
	}
}

// SetRESTMapper sets the REST mapper for resolving resource types.
func (w *WorkloadScale) SetRESTMapper(mapper meta.RESTMapper) {
	w.restMapper = mapper
}

// GetScale retrieves the scale for a workload.
func (w *WorkloadScale) GetScale(ctx context.Context, scaler *scalerv1alpha1.BudAIScaler) (*Scale, error) {
	targetRef := scaler.Spec.ScaleTargetRef

	// Determine the workload type
	gvk := schema.FromAPIVersionAndKind(targetRef.APIVersion, targetRef.Kind)

	switch gvk.Kind {
	case "Deployment":
		return w.getDeploymentScale(ctx, scaler.Namespace, targetRef.Name)
	case "StatefulSet":
		return w.getStatefulSetScale(ctx, scaler.Namespace, targetRef.Name)
	case "ReplicaSet":
		return w.getReplicaSetScale(ctx, scaler.Namespace, targetRef.Name)
	default:
		// Try to get scale using the generic scale subresource
		return w.getGenericScale(ctx, scaler)
	}
}

// SetScale updates the scale for a workload.
func (w *WorkloadScale) SetScale(ctx context.Context, scaler *scalerv1alpha1.BudAIScaler, replicas int32) error {
	targetRef := scaler.Spec.ScaleTargetRef

	// Determine the workload type
	gvk := schema.FromAPIVersionAndKind(targetRef.APIVersion, targetRef.Kind)

	switch gvk.Kind {
	case "Deployment":
		return w.setDeploymentScale(ctx, scaler.Namespace, targetRef.Name, replicas)
	case "StatefulSet":
		return w.setStatefulSetScale(ctx, scaler.Namespace, targetRef.Name, replicas)
	case "ReplicaSet":
		return w.setReplicaSetScale(ctx, scaler.Namespace, targetRef.Name, replicas)
	default:
		return w.setGenericScale(ctx, scaler, replicas)
	}
}

// getDeploymentScale gets the scale for a Deployment.
func (w *WorkloadScale) getDeploymentScale(ctx context.Context, namespace, name string) (*Scale, error) {
	deployment := &appsv1.Deployment{}
	err := w.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, deployment)
	if err != nil {
		return nil, fmt.Errorf("failed to get deployment: %w", err)
	}

	selector, err := metav1.LabelSelectorAsSelector(deployment.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("failed to parse selector: %w", err)
	}

	return &Scale{
		ObjectMeta: deployment.ObjectMeta,
		Spec: ScaleSpec{
			Replicas: *deployment.Spec.Replicas,
		},
		Status: ScaleStatus{
			Replicas: deployment.Status.Replicas,
			Selector: selector.String(),
		},
	}, nil
}

// setDeploymentScale sets the scale for a Deployment.
func (w *WorkloadScale) setDeploymentScale(ctx context.Context, namespace, name string, replicas int32) error {
	deployment := &appsv1.Deployment{}
	err := w.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, deployment)
	if err != nil {
		return fmt.Errorf("failed to get deployment: %w", err)
	}

	if *deployment.Spec.Replicas == replicas {
		return nil // No change needed
	}

	klog.V(4).InfoS("Scaling deployment",
		"deployment", name,
		"namespace", namespace,
		"from", *deployment.Spec.Replicas,
		"to", replicas)

	deployment.Spec.Replicas = &replicas
	return w.client.Update(ctx, deployment)
}

// getStatefulSetScale gets the scale for a StatefulSet.
func (w *WorkloadScale) getStatefulSetScale(ctx context.Context, namespace, name string) (*Scale, error) {
	sts := &appsv1.StatefulSet{}
	err := w.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, sts)
	if err != nil {
		return nil, fmt.Errorf("failed to get statefulset: %w", err)
	}

	selector, err := metav1.LabelSelectorAsSelector(sts.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("failed to parse selector: %w", err)
	}

	return &Scale{
		ObjectMeta: sts.ObjectMeta,
		Spec: ScaleSpec{
			Replicas: *sts.Spec.Replicas,
		},
		Status: ScaleStatus{
			Replicas: sts.Status.Replicas,
			Selector: selector.String(),
		},
	}, nil
}

// setStatefulSetScale sets the scale for a StatefulSet.
func (w *WorkloadScale) setStatefulSetScale(ctx context.Context, namespace, name string, replicas int32) error {
	sts := &appsv1.StatefulSet{}
	err := w.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, sts)
	if err != nil {
		return fmt.Errorf("failed to get statefulset: %w", err)
	}

	if *sts.Spec.Replicas == replicas {
		return nil
	}

	klog.V(4).InfoS("Scaling statefulset",
		"statefulset", name,
		"namespace", namespace,
		"from", *sts.Spec.Replicas,
		"to", replicas)

	sts.Spec.Replicas = &replicas
	return w.client.Update(ctx, sts)
}

// getReplicaSetScale gets the scale for a ReplicaSet.
func (w *WorkloadScale) getReplicaSetScale(ctx context.Context, namespace, name string) (*Scale, error) {
	rs := &appsv1.ReplicaSet{}
	err := w.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, rs)
	if err != nil {
		return nil, fmt.Errorf("failed to get replicaset: %w", err)
	}

	selector, err := metav1.LabelSelectorAsSelector(rs.Spec.Selector)
	if err != nil {
		return nil, fmt.Errorf("failed to parse selector: %w", err)
	}

	return &Scale{
		ObjectMeta: rs.ObjectMeta,
		Spec: ScaleSpec{
			Replicas: *rs.Spec.Replicas,
		},
		Status: ScaleStatus{
			Replicas: rs.Status.Replicas,
			Selector: selector.String(),
		},
	}, nil
}

// setReplicaSetScale sets the scale for a ReplicaSet.
func (w *WorkloadScale) setReplicaSetScale(ctx context.Context, namespace, name string, replicas int32) error {
	rs := &appsv1.ReplicaSet{}
	err := w.client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, rs)
	if err != nil {
		return fmt.Errorf("failed to get replicaset: %w", err)
	}

	if *rs.Spec.Replicas == replicas {
		return nil
	}

	klog.V(4).InfoS("Scaling replicaset",
		"replicaset", name,
		"namespace", namespace,
		"from", *rs.Spec.Replicas,
		"to", replicas)

	rs.Spec.Replicas = &replicas
	return w.client.Update(ctx, rs)
}

// getGenericScale tries to get scale using the autoscaling/v1 Scale subresource.
func (w *WorkloadScale) getGenericScale(ctx context.Context, scaler *scalerv1alpha1.BudAIScaler) (*Scale, error) {
	// This is a simplified implementation
	// In a real implementation, you would use the scale subresource API
	return nil, fmt.Errorf("generic scale not implemented for %s/%s",
		scaler.Spec.ScaleTargetRef.Kind, scaler.Spec.ScaleTargetRef.Name)
}

// setGenericScale tries to set scale using the autoscaling/v1 Scale subresource.
func (w *WorkloadScale) setGenericScale(ctx context.Context, scaler *scalerv1alpha1.BudAIScaler, replicas int32) error {
	return fmt.Errorf("generic scale not implemented for %s/%s",
		scaler.Spec.ScaleTargetRef.Kind, scaler.Spec.ScaleTargetRef.Name)
}

// GetTargetGVR returns the GroupVersionResource for the scale target.
func (w *WorkloadScale) GetTargetGVR(scaler *scalerv1alpha1.BudAIScaler) (schema.GroupVersionResource, error) {
	targetRef := scaler.Spec.ScaleTargetRef
	gv, err := schema.ParseGroupVersion(targetRef.APIVersion)
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("failed to parse API version: %w", err)
	}

	// Map kind to resource
	var resource string
	switch targetRef.Kind {
	case "Deployment":
		resource = "deployments"
	case "StatefulSet":
		resource = "statefulsets"
	case "ReplicaSet":
		resource = "replicasets"
	default:
		// Use lowercase plural as a guess
		resource = fmt.Sprintf("%ss", targetRef.Kind)
	}

	return schema.GroupVersionResource{
		Group:    gv.Group,
		Version:  gv.Version,
		Resource: resource,
	}, nil
}

// ConvertToAutoscalingScale converts our Scale type to autoscaling/v1 Scale.
func ConvertToAutoscalingScale(scale *Scale) *autoscalingv1.Scale {
	return &autoscalingv1.Scale{
		ObjectMeta: scale.ObjectMeta,
		Spec: autoscalingv1.ScaleSpec{
			Replicas: scale.Spec.Replicas,
		},
		Status: autoscalingv1.ScaleStatus{
			Replicas: scale.Status.Replicas,
			Selector: scale.Status.Selector,
		},
	}
}

// ConvertFromAutoscalingScale converts autoscaling/v1 Scale to our Scale type.
func ConvertFromAutoscalingScale(scale *autoscalingv1.Scale) *Scale {
	return &Scale{
		ObjectMeta: scale.ObjectMeta,
		Spec: ScaleSpec{
			Replicas: scale.Spec.Replicas,
		},
		Status: ScaleStatus{
			Replicas: scale.Status.Replicas,
			Selector: scale.Status.Selector,
		},
	}
}
