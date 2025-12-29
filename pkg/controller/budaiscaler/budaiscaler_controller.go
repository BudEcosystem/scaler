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
	"time"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	scalerv1alpha1 "github.com/BudEcosystem/scaler/api/scaler/v1alpha1"
	"github.com/BudEcosystem/scaler/pkg/controller/budaiscaler/algorithm"
	"github.com/BudEcosystem/scaler/pkg/metrics"
)

const (
	// DefaultRequeueInterval is the default interval between reconciliations.
	DefaultRequeueInterval = 10 * time.Second

	// FinalizerName is the finalizer name for BudAIScaler resources.
	FinalizerName = "scaler.bud.studio/finalizer"

	// MaxScalingHistoryEvents is the maximum number of scaling events to keep.
	MaxScalingHistoryEvents = 10
)

// BudAIScalerReconciler reconciles a BudAIScaler object.
type BudAIScalerReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	RestConfig *rest.Config

	// AutoScaler handles the scaling logic.
	autoScaler *AutoScaler

	// HPABuilder builds HPA resources.
	hpaBuilder *algorithm.HPABuilder

	// MetricsFactory creates metric fetchers.
	metricsFactory *metrics.DefaultMetricFetcherFactory
}

// NewBudAIScalerReconciler creates a new BudAIScalerReconciler.
func NewBudAIScalerReconciler(c client.Client, scheme *runtime.Scheme, restConfig *rest.Config) (*BudAIScalerReconciler, error) {
	// Create metrics factory
	metricsFactory, err := metrics.NewDefaultMetricFetcherFactory(restConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics factory: %w", err)
	}

	return &BudAIScalerReconciler{
		Client:         c,
		Scheme:         scheme,
		RestConfig:     restConfig,
		autoScaler:     NewAutoScaler(c, metricsFactory),
		hpaBuilder:     algorithm.NewHPABuilder(),
		metricsFactory: metricsFactory,
	}, nil
}

// +kubebuilder:rbac:groups=scaler.bud.studio,resources=budaiscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=scaler.bud.studio,resources=budaiscalers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=scaler.bud.studio,resources=budaiscalers/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments;statefulsets;replicasets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments/scale;statefulsets/scale;replicasets/scale,verbs=get;update;patch
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=metrics.k8s.io,resources=pods;nodes,verbs=get;list;watch

// Reconcile handles BudAIScaler reconciliation.
func (r *BudAIScalerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("Reconciling BudAIScaler", "name", req.Name, "namespace", req.Namespace)

	// Fetch the BudAIScaler resource
	scaler := &scalerv1alpha1.BudAIScaler{}
	err := r.Get(ctx, req.NamespacedName, scaler)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("BudAIScaler not found, likely deleted")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "Failed to get BudAIScaler")
		return ctrl.Result{}, err
	}

	// Handle deletion
	if !scaler.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, scaler)
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(scaler, FinalizerName) {
		controllerutil.AddFinalizer(scaler, FinalizerName)
		if err := r.Update(ctx, scaler); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Validate the scaler
	if err := r.validateScaler(scaler); err != nil {
		logger.Error(err, "Validation failed")
		r.updateCondition(ctx, scaler, scalerv1alpha1.ConditionReady, metav1.ConditionFalse,
			"ValidationFailed", err.Error())
		return ctrl.Result{RequeueAfter: DefaultRequeueInterval}, nil
	}

	// Check for conflicts with other scalers
	if err := r.checkConflicts(ctx, scaler); err != nil {
		logger.Error(err, "Conflict detected")
		r.updateCondition(ctx, scaler, scalerv1alpha1.ConditionReady, metav1.ConditionFalse,
			"ConflictDetected", err.Error())
		return ctrl.Result{RequeueAfter: DefaultRequeueInterval}, nil
	}

	// Route based on strategy
	var result ctrl.Result
	switch scaler.Spec.ScalingStrategy {
	case scalerv1alpha1.HPA:
		result, err = r.reconcileHPA(ctx, scaler)
	case scalerv1alpha1.KPA, scalerv1alpha1.BudScaler:
		result, err = r.reconcileCustomScaler(ctx, scaler)
	default:
		err = fmt.Errorf("unknown scaling strategy: %s", scaler.Spec.ScalingStrategy)
	}

	if err != nil {
		logger.Error(err, "Reconciliation failed")
		r.updateCondition(ctx, scaler, scalerv1alpha1.ConditionReady, metav1.ConditionFalse,
			"ReconciliationFailed", err.Error())
		return ctrl.Result{RequeueAfter: DefaultRequeueInterval}, err
	}

	// Update ready condition
	r.updateCondition(ctx, scaler, scalerv1alpha1.ConditionReady, metav1.ConditionTrue,
		"ScalerReady", "BudAIScaler is ready and operating")

	return result, nil
}

// handleDeletion handles cleanup when a BudAIScaler is deleted.
func (r *BudAIScalerReconciler) handleDeletion(ctx context.Context, scaler *scalerv1alpha1.BudAIScaler) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Handling deletion", "name", scaler.Name)

	// Clean up managed HPA if using HPA strategy
	if scaler.Spec.ScalingStrategy == scalerv1alpha1.HPA {
		hpaName := scaler.Name + "-hpa"
		hpa := &autoscalingv2.HorizontalPodAutoscaler{}
		err := r.Get(ctx, types.NamespacedName{Namespace: scaler.Namespace, Name: hpaName}, hpa)
		if err == nil {
			if err := r.Delete(ctx, hpa); err != nil && !errors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
			logger.Info("Deleted managed HPA", "hpa", hpaName)
		}
	}

	// Remove finalizer
	controllerutil.RemoveFinalizer(scaler, FinalizerName)
	if err := r.Update(ctx, scaler); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// validateScaler validates the BudAIScaler spec.
func (r *BudAIScalerReconciler) validateScaler(scaler *scalerv1alpha1.BudAIScaler) error {
	if scaler.Spec.ScaleTargetRef.Name == "" {
		return fmt.Errorf("scaleTargetRef.name is required")
	}

	if len(scaler.Spec.MetricsSources) == 0 {
		return fmt.Errorf("at least one metric source is required")
	}

	if scaler.Spec.MaxReplicas < 1 {
		return fmt.Errorf("maxReplicas must be at least 1")
	}

	minReplicas := int32(1)
	if scaler.Spec.MinReplicas != nil {
		minReplicas = *scaler.Spec.MinReplicas
	}

	if minReplicas > scaler.Spec.MaxReplicas {
		return fmt.Errorf("minReplicas (%d) cannot be greater than maxReplicas (%d)",
			minReplicas, scaler.Spec.MaxReplicas)
	}

	return nil
}

// checkConflicts checks if another scaler is targeting the same workload.
func (r *BudAIScalerReconciler) checkConflicts(ctx context.Context, scaler *scalerv1alpha1.BudAIScaler) error {
	// List all BudAIScalers in the namespace
	scalers := &scalerv1alpha1.BudAIScalerList{}
	if err := r.List(ctx, scalers, client.InNamespace(scaler.Namespace)); err != nil {
		return fmt.Errorf("failed to list scalers: %w", err)
	}

	for _, other := range scalers.Items {
		if other.Name == scaler.Name {
			continue
		}

		// Check if targeting the same workload
		if other.Spec.ScaleTargetRef.Kind == scaler.Spec.ScaleTargetRef.Kind &&
			other.Spec.ScaleTargetRef.Name == scaler.Spec.ScaleTargetRef.Name {
			return fmt.Errorf("conflict: BudAIScaler %s is already targeting %s/%s",
				other.Name, scaler.Spec.ScaleTargetRef.Kind, scaler.Spec.ScaleTargetRef.Name)
		}
	}

	return nil
}

// reconcileHPA handles HPA strategy by creating/updating a Kubernetes HPA.
func (r *BudAIScalerReconciler) reconcileHPA(ctx context.Context, scaler *scalerv1alpha1.BudAIScaler) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Build the HPA
	desiredHPA, err := r.hpaBuilder.BuildHPA(scaler)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to build HPA: %w", err)
	}

	// Check if HPA exists
	existingHPA := &autoscalingv2.HorizontalPodAutoscaler{}
	err = r.Get(ctx, types.NamespacedName{Namespace: desiredHPA.Namespace, Name: desiredHPA.Name}, existingHPA)

	if errors.IsNotFound(err) {
		// Create the HPA
		logger.Info("Creating HPA", "name", desiredHPA.Name)
		if err := r.Create(ctx, desiredHPA); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create HPA: %w", err)
		}
	} else if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get HPA: %w", err)
	} else {
		// Update the HPA
		if err := r.hpaBuilder.UpdateHPASpec(existingHPA, scaler); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update HPA spec: %w", err)
		}

		logger.V(1).Info("Updating HPA", "name", existingHPA.Name)
		if err := r.Update(ctx, existingHPA); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update HPA: %w", err)
		}
	}

	// Update status with HPA status
	r.updateStatusFromHPA(ctx, scaler, desiredHPA.Name)

	return ctrl.Result{RequeueAfter: DefaultRequeueInterval}, nil
}

// reconcileCustomScaler handles KPA and BudScaler strategies.
func (r *BudAIScalerReconciler) reconcileCustomScaler(ctx context.Context, scaler *scalerv1alpha1.BudAIScaler) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Perform scaling
	result, err := r.autoScaler.Scale(ctx, scaler)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("scaling failed: %w", err)
	}

	// Update status
	now := metav1.Now()
	scaler.Status.ActualScale = result.DesiredReplicas
	scaler.Status.DesiredScale = result.DesiredReplicas

	if result.Scaled {
		scaler.Status.LastScaleTime = &now

		// Record scaling decision
		decision := r.autoScaler.RecordScalingDecision(scaler, result)
		scaler.Status.ScalingHistory = append(scaler.Status.ScalingHistory, decision)

		// Trim history
		if len(scaler.Status.ScalingHistory) > MaxScalingHistoryEvents {
			scaler.Status.ScalingHistory = scaler.Status.ScalingHistory[len(scaler.Status.ScalingHistory)-MaxScalingHistoryEvents:]
		}

		logger.Info("Scaled workload",
			"from", result.CurrentReplicas,
			"to", result.DesiredReplicas,
			"reason", result.Recommendation.Reason)
	}

	// Update learning status if learning is enabled
	if scaler.Spec.PredictionConfig != nil && scaler.Spec.PredictionConfig.EnableLearning {
		learningStatus := r.autoScaler.GetLearningStatus(ctx, scaler)
		if learningStatus != nil {
			scaler.Status.LearningStatus = &scalerv1alpha1.LearningStatus{
				DataPointsCollected:  int32(learningStatus.DataPointsCollected),
				OverallAccuracy:      fmt.Sprintf("%.1f%%", learningStatus.OverallAccuracy),
				DirectionAccuracy:    fmt.Sprintf("%.1f%%", learningStatus.OverallAccuracy), // Direction accuracy
				RecentMAPE:           fmt.Sprintf("%.2f%%", learningStatus.OverallMAPE),
				RecentMAE:            fmt.Sprintf("%.2f", learningStatus.OverallMAE),
				SeasonalDataComplete: learningStatus.SeasonalDataComplete,
				DetectedPattern:      learningStatus.CurrentPattern,
				PatternConfidence:    fmt.Sprintf("%.2f", learningStatus.PatternConfidence),
				CalibrationNeeded:    learningStatus.CalibrationNeeded,
				ConfigMapName:        learningStatus.ConfigMapName,
			}
			logger.V(1).Info("Updated learning status",
				"dataPoints", learningStatus.DataPointsCollected,
				"pattern", learningStatus.CurrentPattern)
		}
	}

	// Update status
	if err := r.Status().Update(ctx, scaler); err != nil {
		klog.V(4).InfoS("Failed to update status", "error", err)
	}

	return ctrl.Result{RequeueAfter: DefaultRequeueInterval}, nil
}

// updateStatusFromHPA updates BudAIScaler status based on HPA status.
func (r *BudAIScalerReconciler) updateStatusFromHPA(ctx context.Context, scaler *scalerv1alpha1.BudAIScaler, hpaName string) {
	hpa := &autoscalingv2.HorizontalPodAutoscaler{}
	err := r.Get(ctx, types.NamespacedName{Namespace: scaler.Namespace, Name: hpaName}, hpa)
	if err != nil {
		return
	}

	scaler.Status.ActualScale = hpa.Status.CurrentReplicas
	scaler.Status.DesiredScale = hpa.Status.DesiredReplicas

	if hpa.Status.LastScaleTime != nil {
		scaler.Status.LastScaleTime = hpa.Status.LastScaleTime
	}

	if err := r.Status().Update(ctx, scaler); err != nil {
		klog.V(4).InfoS("Failed to update status from HPA", "error", err)
	}
}

// updateCondition updates a condition on the BudAIScaler status.
func (r *BudAIScalerReconciler) updateCondition(
	ctx context.Context,
	scaler *scalerv1alpha1.BudAIScaler,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string,
) {
	condition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}

	// Find and update existing condition or append new one
	found := false
	for i, c := range scaler.Status.Conditions {
		if c.Type == conditionType {
			if c.Status != status {
				scaler.Status.Conditions[i] = condition
			}
			found = true
			break
		}
	}
	if !found {
		scaler.Status.Conditions = append(scaler.Status.Conditions, condition)
	}

	if err := r.Status().Update(ctx, scaler); err != nil {
		klog.V(4).InfoS("Failed to update condition", "error", err)
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *BudAIScalerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&scalerv1alpha1.BudAIScaler{}).
		Owns(&autoscalingv2.HorizontalPodAutoscaler{}).
		WithEventFilter(predicate.GenerationChangedPredicate{}).
		Complete(r)
}
