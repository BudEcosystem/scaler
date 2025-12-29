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
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scalerv1alpha1 "github.com/BudEcosystem/scaler/api/scaler/v1alpha1"
	"github.com/BudEcosystem/scaler/pkg/aggregation"
	scalercontext "github.com/BudEcosystem/scaler/pkg/context"
	"github.com/BudEcosystem/scaler/pkg/controller/budaiscaler/algorithm"
	"github.com/BudEcosystem/scaler/pkg/controller/budaiscaler/cost"
	"github.com/BudEcosystem/scaler/pkg/controller/budaiscaler/learning"
	"github.com/BudEcosystem/scaler/pkg/controller/budaiscaler/prediction"
	"github.com/BudEcosystem/scaler/pkg/gpu"
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
	gpuProvider      gpu.Provider
	costCalculators  map[cost.CloudProvider]*cost.Calculator
	predictor        *prediction.Predictor
	learningSystems  map[string]*learning.LearningSystem // Per-scaler learning systems
}

// NewAutoScaler creates a new AutoScaler.
func NewAutoScaler(
	client client.Client,
	metricsFactory metrics.MetricFetcherFactory,
) *AutoScaler {
	aggregator := aggregation.NewMetricAggregator()
	collector := metrics.NewMultiSourceCollector(metricsFactory, aggregator)

	// Initialize cost calculators for each cloud provider
	costCalculators := map[cost.CloudProvider]*cost.Calculator{
		cost.AWS:   cost.NewCalculator(cost.AWS),
		cost.GCP:   cost.NewCalculator(cost.GCP),
		cost.Azure: cost.NewCalculator(cost.Azure),
	}

	return &AutoScaler{
		client:           client,
		metricsFactory:   metricsFactory,
		algorithmFactory: algorithm.NewDefaultAlgorithmFactory(),
		aggregator:       aggregator,
		collector:        collector,
		workloadScale:    NewWorkloadScale(client),
		costCalculators:  costCalculators,
		predictor:        prediction.NewPredictor(),
		learningSystems:  make(map[string]*learning.LearningSystem),
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

	klog.V(4).InfoS("Scale check", "scaler", scaler.Name, "currentReplicas", scale.Spec.Replicas, "podCount", len(pods))

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

	// Get cost metrics if enabled
	if scaler.Spec.CostConfig != nil && scaler.Spec.CostConfig.Enabled {
		request.CostMetrics = a.collectCostMetrics(ctx, scaler, scale.Spec.Replicas)
	}

	// Load schedule hints from top-level spec (works independently of prediction config)
	a.loadScheduleHints(scaler)

	// Get prediction data if enabled, or schedule hint data if hints are active
	if scaler.Spec.PredictionConfig != nil && scaler.Spec.PredictionConfig.Enabled {
		request.PredictionData = a.getPredictionData(ctx, scaler, metricSnapshots)
	} else if len(scaler.Spec.ScheduleHints) > 0 {
		// Schedule hints can work without full prediction enabled
		request.PredictionData = a.getScheduleHintData(scaler)
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

	klog.V(4).InfoS("Scaling recommendation",
		"scaler", scaler.Name,
		"currentReplicas", request.CurrentReplicas,
		"desiredReplicas", recommendation.DesiredReplicas,
		"direction", recommendation.ScaleDirection,
		"reason", recommendation.Reason)

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

	// Record feedback for learning system if enabled
	if scaler.Spec.PredictionConfig != nil && scaler.Spec.PredictionConfig.EnableLearning {
		a.recordLearningFeedback(ctx, scaler, request, result, metricSnapshots)
		// Verify any past predictions that are now ready
		a.VerifyLearningPredictions(ctx, scaler)
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
	// Get GPU provider - either use configured provider or try DCGM exporter
	provider := a.gpuProvider
	if provider == nil {
		// Try to use DCGM exporter service in the same namespace
		// The service name follows the pattern: dcgm-exporter or nvidia-dcgm-exporter
		dcgmEndpoint := fmt.Sprintf("http://mock-dcgm-exporter.%s.svc.cluster.local:9400", scaler.Namespace)
		provider = gpu.NewDCGMProvider(dcgmEndpoint)
	}

	gpuMetrics, err := provider.FetchMetrics(ctx)
	if err != nil {
		klog.V(4).InfoS("Failed to fetch GPU metrics", "error", err, "namespace", scaler.Namespace)
		return nil
	}

	klog.V(4).InfoS("GPU metrics collected",
		"memoryUtil", gpuMetrics.AverageMemoryUtilization,
		"computeUtil", gpuMetrics.AverageComputeUtilization,
		"gpuCount", gpuMetrics.GPUCount)

	return &types.GPUMetrics{
		MemoryUtilization:  gpuMetrics.AverageMemoryUtilization,
		ComputeUtilization: gpuMetrics.AverageComputeUtilization,
		GPUCount:           int32(gpuMetrics.GPUCount),
		GPUType:            gpuMetrics.GPUType,
	}
}

// SetGPUProvider sets the GPU metrics provider.
func (a *AutoScaler) SetGPUProvider(provider gpu.Provider) {
	a.gpuProvider = provider
}

// collectCostMetrics collects cost metrics for the scaler.
func (a *AutoScaler) collectCostMetrics(ctx context.Context, scaler *scalerv1alpha1.BudAIScaler, currentReplicas int32) *types.CostMetrics {
	costConfig := scaler.Spec.CostConfig
	if costConfig == nil || !costConfig.Enabled {
		return nil
	}

	provider := cost.CloudProvider(costConfig.CloudProvider)
	if provider == "" {
		provider = cost.AWS // Default to AWS if not specified
	}
	calculator, ok := a.costCalculators[provider]
	if !ok {
		klog.InfoS("Cost calculator not found for provider", "provider", provider)
		return nil
	}

	// For now, use a default instance type - this could be detected from node labels
	instanceType := "g5.xlarge" // Default GPU instance
	isSpot := costConfig.PreferSpotInstances

	hourlyCost, err := calculator.CalculateHourlyCost(ctx, instanceType, currentReplicas, isSpot)
	if err != nil {
		klog.InfoS("Failed to calculate hourly cost", "error", err)
		return nil
	}

	budgetPerHour := 0.0
	if costConfig.BudgetPerHour != nil {
		budgetPerHour = costConfig.BudgetPerHour.AsApproximateFloat64()
	}

	klog.V(4).InfoS("Cost metrics calculated",
		"provider", provider,
		"replicas", currentReplicas,
		"hourlyCost", hourlyCost,
		"budgetPerHour", budgetPerHour,
		"perReplicaCost", hourlyCost/float64(currentReplicas))

	return &types.CostMetrics{
		CurrentCostPerHour:    hourlyCost,
		BudgetPerHour:         budgetPerHour,
		PerReplicaCostPerHour: hourlyCost / float64(currentReplicas),
	}
}

// loadScheduleHints loads schedule hints from the scaler spec into the predictor.
// This is independent of prediction config and allows schedule hints to work standalone.
func (a *AutoScaler) loadScheduleHints(scaler *scalerv1alpha1.BudAIScaler) {
	for _, hint := range scaler.Spec.ScheduleHints {
		a.predictor.AddScheduleHint(prediction.ScheduleHint{
			Name:           hint.Name,
			CronExpression: hint.CronExpression,
			Duration:       hint.Duration.Duration,
			TargetReplicas: hint.TargetReplicas,
		})
	}
}

// getScheduleHintData returns prediction data based only on schedule hints.
// Used when prediction is disabled but schedule hints are defined.
// Schedule hints act as a minimum floor - scaling can go higher but not lower.
func (a *AutoScaler) getScheduleHintData(scaler *scalerv1alpha1.BudAIScaler) *types.PredictionData {
	activeHint := a.predictor.GetActiveScheduleHint(time.Now())
	if activeHint == nil {
		return nil
	}

	klog.InfoS("Active schedule hint (standalone mode)",
		"scaler", scaler.Name,
		"hint", activeHint.Name,
		"targetReplicas", activeHint.TargetReplicas)

	return &types.PredictionData{
		PredictedReplicas: activeHint.TargetReplicas,
		Confidence:        1.0, // Schedule hints have 100% confidence
		Reason:            fmt.Sprintf("schedule hint: %s", activeHint.Name),
		Source:            types.PredictionSourceSchedule,
		IsScheduleHint:    true,
		ScheduleHintName:  activeHint.Name,
	}
}

// getPredictionData gets prediction data for the scaler.
func (a *AutoScaler) getPredictionData(ctx context.Context, scaler *scalerv1alpha1.BudAIScaler, metricSnapshots map[string]*types.MetricSnapshot) *types.PredictionData {
	predConfig := scaler.Spec.PredictionConfig
	if predConfig == nil || !predConfig.Enabled {
		return nil
	}

	// Record current metrics for prediction
	for metric, snapshot := range metricSnapshots {
		if snapshot != nil {
			a.predictor.RecordMetric(metric, snapshot.Average, snapshot.Timestamp)
		}
	}

	if len(scaler.Spec.MetricsSources) == 0 {
		return nil
	}

	// Determine which metrics to use for prediction
	predictionMetrics := a.getPredictionMetricNames(scaler)
	if len(predictionMetrics) == 0 {
		return nil
	}

	// Use the first prediction metric as primary for replica calculation
	primaryMetric := predictionMetrics[0]
	targetValue := a.getTargetValueForMetric(scaler, primaryMetric)

	result := a.predictor.PredictReplicas(
		ctx,
		primaryMetric,
		scaler.Status.ActualScale,
		targetValue,
		scaler.Spec.GetMinReplicas(),
		scaler.Spec.MaxReplicas,
	)

	predData := &types.PredictionData{
		PredictedReplicas: result.RecommendedReplicas,
		Confidence:        result.Confidence,
		Reason:            result.Reason,
		Source:            types.PredictionSourceTimeSeries,
	}

	// Check if there's an active schedule hint - schedule hints act as floor
	if result.ScheduleHint != nil {
		klog.InfoS("Active schedule hint (with prediction)",
			"scaler", scaler.Name,
			"hint", result.ScheduleHint.Name,
			"hintReplicas", result.ScheduleHint.TargetReplicas,
			"predictedReplicas", result.RecommendedReplicas)

		predData.IsScheduleHint = true
		predData.ScheduleHintName = result.ScheduleHint.Name
		predData.Source = types.PredictionSourceSchedule
		// Store the schedule hint target - algorithm will use this as floor
		predData.PredictedReplicas = result.ScheduleHint.TargetReplicas
	}

	return predData
}

// GetAggregator returns the metric aggregator.
func (a *AutoScaler) GetAggregator() *aggregation.DefaultMetricAggregator {
	return a.aggregator
}

// GetCollector returns the metric collector.
func (a *AutoScaler) GetCollector() *metrics.MultiSourceCollector {
	return a.collector
}

// getPredictionMetricNames returns the metrics to use for prediction.
// Uses configured PredictionMetrics if specified, otherwise defaults to first metric source.
func (a *AutoScaler) getPredictionMetricNames(scaler *scalerv1alpha1.BudAIScaler) []string {
	if scaler.Spec.PredictionConfig != nil && len(scaler.Spec.PredictionConfig.PredictionMetrics) > 0 {
		return scaler.Spec.PredictionConfig.PredictionMetrics
	}
	// Default to first metric source
	if len(scaler.Spec.MetricsSources) > 0 {
		return []string{scaler.Spec.MetricsSources[0].TargetMetric}
	}
	return nil
}

// getSeasonalMetricNames returns the metrics to track for seasonal patterns.
// Uses configured SeasonalMetrics if specified, otherwise defaults to prediction metrics.
func (a *AutoScaler) getSeasonalMetricNames(scaler *scalerv1alpha1.BudAIScaler) []string {
	if scaler.Spec.PredictionConfig != nil && len(scaler.Spec.PredictionConfig.SeasonalMetrics) > 0 {
		return scaler.Spec.PredictionConfig.SeasonalMetrics
	}
	// Default to prediction metrics
	return a.getPredictionMetricNames(scaler)
}

// getTargetValueForMetric looks up the target value for a metric name from metric sources.
func (a *AutoScaler) getTargetValueForMetric(scaler *scalerv1alpha1.BudAIScaler, metricName string) float64 {
	for _, source := range scaler.Spec.MetricsSources {
		if source.TargetMetric == metricName {
			if source.TargetValue != "" {
				if parsed, err := strconv.ParseFloat(source.TargetValue, 64); err == nil {
					return parsed
				}
			}
		}
	}
	return 50.0 // Default fallback
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

// getLearningSystem returns or creates the learning system for a scaler.
func (a *AutoScaler) getLearningSystem(ctx context.Context, scaler *scalerv1alpha1.BudAIScaler) *learning.LearningSystem {
	key := scaler.Namespace + "/" + scaler.Name
	if ls, exists := a.learningSystems[key]; exists {
		return ls
	}

	// Create owner reference for automatic cleanup
	ownerRef := learning.CreateOwnerReference(
		scaler.Name,
		string(scaler.UID),
		scalerv1alpha1.GroupVersion.String(),
		"BudAIScaler",
	)

	ls := learning.NewLearningSystem(a.client, scaler.Name, scaler.Namespace, ownerRef)

	// Initialize from stored data
	if err := ls.Initialize(ctx); err != nil {
		klog.V(4).InfoS("Failed to initialize learning system from storage", "error", err, "scaler", scaler.Name)
	}

	// Configure based on scaler spec
	if scaler.Spec.PredictionConfig != nil {
		if scaler.Spec.PredictionConfig.LookAheadMinutes != nil {
			ls.SetLookAhead(time.Duration(*scaler.Spec.PredictionConfig.LookAheadMinutes) * time.Minute)
		}
		if scaler.Spec.PredictionConfig.HistoricalDataDays != nil {
			ls.SetHistoricalDays(int(*scaler.Spec.PredictionConfig.HistoricalDataDays))
		}
		ls.SetEnabled(scaler.Spec.PredictionConfig.EnableLearning)
	}

	a.learningSystems[key] = ls
	return ls
}

// recordLearningFeedback records scaling decision feedback for learning.
func (a *AutoScaler) recordLearningFeedback(
	ctx context.Context,
	scaler *scalerv1alpha1.BudAIScaler,
	request algorithm.ScalingRequest,
	result *ScaleResult,
	metricSnapshots map[string]*types.MetricSnapshot,
) {
	ls := a.getLearningSystem(ctx, scaler)
	if ls == nil || !ls.IsEnabled() {
		return
	}

	// Extract metric values from snapshots
	metricValues := make(map[string]float64)
	for name, snapshot := range metricSnapshots {
		if snapshot != nil {
			metricValues[name] = snapshot.Average
		}
	}

	// Build LLM metrics from GPU metrics if available
	llmMetrics := learning.LLMMetrics{}
	if request.GPUMetrics != nil {
		llmMetrics.GPUMemoryUtilization = request.GPUMetrics.MemoryUtilization
		llmMetrics.GPUComputeUtilization = request.GPUMetrics.ComputeUtilization
	}

	// Note: LLMMetrics fields are populated from GPU metrics above.
	// All other metrics are stored generically in metricValues map.
	// The learning system uses metricValues for seasonal tracking based on
	// configured SeasonalMetrics, not hardcoded metric names.

	// Get predicted replicas if available
	predictedReplicas := result.DesiredReplicas
	if request.PredictionData != nil {
		predictedReplicas = request.PredictionData.PredictedReplicas
	}

	// Get look-ahead minutes
	lookAheadMinutes := 15 // default
	if scaler.Spec.PredictionConfig != nil && scaler.Spec.PredictionConfig.LookAheadMinutes != nil {
		lookAheadMinutes = int(*scaler.Spec.PredictionConfig.LookAheadMinutes)
	}

	// Record the scaling decision
	ls.RecordScalingDecision(ctx, learning.FeedbackInput{
		ScalerName:          scaler.Name,
		Namespace:           scaler.Namespace,
		Timestamp:           time.Now(),
		CurrentReplicas:     request.CurrentReplicas,
		RecommendedReplicas: result.DesiredReplicas,
		PredictedReplicas:   predictedReplicas,
		MetricSnapshots:     metricValues,
		LLMMetrics:          llmMetrics,
		LookAheadMinutes:    lookAheadMinutes,
	})

	klog.V(4).InfoS("Recorded learning feedback",
		"scaler", scaler.Name,
		"currentReplicas", request.CurrentReplicas,
		"recommendedReplicas", result.DesiredReplicas,
		"predictedReplicas", predictedReplicas)
}

// VerifyLearningPredictions verifies past predictions against actual outcomes.
func (a *AutoScaler) VerifyLearningPredictions(ctx context.Context, scaler *scalerv1alpha1.BudAIScaler) {
	ls := a.getLearningSystem(ctx, scaler)
	if ls == nil || !ls.IsEnabled() {
		return
	}

	ls.VerifyPastPredictions(ctx, scaler.Status.ActualScale)
}

// PersistLearningData persists learning data if needed.
func (a *AutoScaler) PersistLearningData(ctx context.Context, scaler *scalerv1alpha1.BudAIScaler) error {
	ls := a.getLearningSystem(ctx, scaler)
	if ls == nil || !ls.IsEnabled() {
		return nil
	}

	if ls.ShouldPersist() {
		return ls.Persist(ctx)
	}
	return nil
}

// GetLearningStatus returns the learning status for a scaler.
func (a *AutoScaler) GetLearningStatus(ctx context.Context, scaler *scalerv1alpha1.BudAIScaler) *learning.LearningSystemStatus {
	ls := a.getLearningSystem(ctx, scaler)
	if ls == nil {
		return nil
	}

	status := ls.GetStatus()
	return &status
}

// GetSeasonalAdjustment returns the seasonal adjustment for predictions.
func (a *AutoScaler) GetSeasonalAdjustment(ctx context.Context, scaler *scalerv1alpha1.BudAIScaler, metric string) (factor float64, confidence float64) {
	ls := a.getLearningSystem(ctx, scaler)
	if ls == nil || !ls.IsEnabled() {
		return 1.0, 0.0
	}

	return ls.GetSeasonalFactor(metric, time.Now())
}

// GetPatternAdjustment returns the pattern-based adjustment for predictions.
func (a *AutoScaler) GetPatternAdjustment(ctx context.Context, scaler *scalerv1alpha1.BudAIScaler, llmMetrics learning.LLMMetrics) *learning.PatternAdjustment {
	ls := a.getLearningSystem(ctx, scaler)
	if ls == nil || !ls.IsEnabled() {
		return nil
	}

	// Detect current pattern
	ls.DetectPattern(llmMetrics)

	return ls.GetPatternAdjustment()
}

// CleanupLearningSystem removes the learning system for a deleted scaler.
func (a *AutoScaler) CleanupLearningSystem(scaler *scalerv1alpha1.BudAIScaler) {
	key := scaler.Namespace + "/" + scaler.Name
	delete(a.learningSystems, key)
}
