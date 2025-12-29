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
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	scalerv1alpha1 "github.com/BudEcosystem/scaler/api/scaler/v1alpha1"
	"github.com/BudEcosystem/scaler/pkg/aggregation"
	"github.com/BudEcosystem/scaler/pkg/types"
)

// DefaultMetricCollector collects metrics from pods using fetchers.
type DefaultMetricCollector struct {
	factory    MetricFetcherFactory
	aggregator *aggregation.DefaultMetricAggregator
}

// NewDefaultMetricCollector creates a new DefaultMetricCollector.
func NewDefaultMetricCollector(factory MetricFetcherFactory, aggregator *aggregation.DefaultMetricAggregator) *DefaultMetricCollector {
	return &DefaultMetricCollector{
		factory:    factory,
		aggregator: aggregator,
	}
}

// CollectMetrics collects metrics from all pods and returns a snapshot.
func (c *DefaultMetricCollector) CollectMetrics(ctx context.Context, pods []corev1.Pod, source scalerv1alpha1.MetricSource) (*types.MetricSnapshot, error) {
	if len(pods) == 0 {
		return nil, fmt.Errorf("no pods to collect metrics from")
	}

	// Check if this is an external source
	if IsExternalSource(source.MetricSourceType) {
		return c.collectExternalMetrics(ctx, pods, source)
	}

	// Get the appropriate fetcher
	fetcher, err := c.factory.For(source)
	if err != nil {
		return nil, fmt.Errorf("failed to get metric fetcher: %w", err)
	}

	snapshot := &types.MetricSnapshot{
		Values:    make(map[string]types.MetricValue),
		Timestamp: time.Now(),
	}

	// Collect metrics from each pod concurrently
	var mu sync.Mutex
	var wg sync.WaitGroup
	var errors []error

	for _, pod := range pods {
		// Skip pods that aren't ready
		if !isPodReady(pod) {
			klog.V(5).InfoS("Skipping unready pod", "pod", pod.Name, "namespace", pod.Namespace)
			continue
		}

		wg.Add(1)
		go func(p corev1.Pod) {
			defer wg.Done()

			value, err := fetcher.FetchPodMetrics(ctx, p, source)
			if err != nil {
				mu.Lock()
				errors = append(errors, fmt.Errorf("pod %s/%s: %w", p.Namespace, p.Name, err))
				mu.Unlock()
				klog.V(4).InfoS("Failed to fetch pod metrics", "pod", p.Name, "namespace", p.Namespace, "error", err)
				return
			}

			mu.Lock()
			snapshot.Values[p.Name] = types.MetricValue{
				Value:     value,
				Timestamp: time.Now(),
			}

			// Record in aggregator if available
			if c.aggregator != nil {
				key := types.MetricKey{
					Name:      source.TargetMetric,
					Namespace: p.Namespace,
					PodName:   p.Name,
				}
				c.aggregator.Record(key, value, time.Now())
			}
			mu.Unlock()
		}(pod)
	}

	wg.Wait()

	// If all pods failed, return an error
	if len(snapshot.Values) == 0 && len(errors) > 0 {
		return nil, fmt.Errorf("failed to collect metrics from any pod: %v", errors)
	}

	// Calculate aggregates
	c.calculateAggregates(snapshot)

	return snapshot, nil
}

// collectExternalMetrics collects metrics from external sources.
func (c *DefaultMetricCollector) collectExternalMetrics(ctx context.Context, pods []corev1.Pod, source scalerv1alpha1.MetricSource) (*types.MetricSnapshot, error) {
	klog.V(4).InfoS("Collecting external metrics", "metric", source.TargetMetric, "endpoint", source.Endpoint, "sourceType", source.MetricSourceType)
	fetcher, err := c.factory.ForExternal(source)
	if err != nil {
		return nil, fmt.Errorf("failed to get external metric fetcher: %w", err)
	}

	// Get namespace and labels from the first pod
	var namespace string
	var podLabels map[string]string
	if len(pods) > 0 {
		namespace = pods[0].Namespace
		podLabels = pods[0].Labels
	}

	value, err := fetcher.FetchExternalMetrics(ctx, source, namespace, podLabels)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch external metrics: %w", err)
	}

	snapshot := &types.MetricSnapshot{
		Values:    make(map[string]types.MetricValue),
		Timestamp: time.Now(),
	}

	// Store as a single aggregate value
	snapshot.Values["_aggregate"] = types.MetricValue{
		Value:     value,
		Timestamp: time.Now(),
	}

	// Record in aggregator if available
	if c.aggregator != nil {
		key := types.MetricKey{
			Name:      source.TargetMetric,
			Namespace: namespace,
		}
		c.aggregator.Record(key, value, time.Now())
	}

	c.calculateAggregates(snapshot)

	return snapshot, nil
}

// calculateAggregates calculates aggregate values for the snapshot.
func (c *DefaultMetricCollector) calculateAggregates(snapshot *types.MetricSnapshot) {
	if len(snapshot.Values) == 0 {
		return
	}

	var sum, min, max float64
	first := true

	for _, v := range snapshot.Values {
		if first {
			min = v.Value
			max = v.Value
			first = false
		} else {
			if v.Value < min {
				min = v.Value
			}
			if v.Value > max {
				max = v.Value
			}
		}
		sum += v.Value
	}

	count := float64(len(snapshot.Values))
	snapshot.Average = sum / count
	snapshot.Sum = sum
	snapshot.Min = min
	snapshot.Max = max
	snapshot.Count = int(count)
}

// isPodReady checks if a pod is ready to receive traffic.
func isPodReady(pod corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}

	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}

	return false
}

// GetAggregator returns the aggregator.
func (c *DefaultMetricCollector) GetAggregator() *aggregation.DefaultMetricAggregator {
	return c.aggregator
}

// MultiSourceCollector collects metrics from multiple sources and aggregates them.
type MultiSourceCollector struct {
	collector *DefaultMetricCollector
}

// NewMultiSourceCollector creates a new MultiSourceCollector.
func NewMultiSourceCollector(factory MetricFetcherFactory, aggregator *aggregation.DefaultMetricAggregator) *MultiSourceCollector {
	return &MultiSourceCollector{
		collector: NewDefaultMetricCollector(factory, aggregator),
	}
}

// CollectAllMetrics collects metrics from all sources.
func (c *MultiSourceCollector) CollectAllMetrics(ctx context.Context, pods []corev1.Pod, sources []scalerv1alpha1.MetricSource) (map[string]*types.MetricSnapshot, error) {
	results := make(map[string]*types.MetricSnapshot)

	var mu sync.Mutex
	var wg sync.WaitGroup
	var errors []error

	for _, source := range sources {
		wg.Add(1)
		go func(s scalerv1alpha1.MetricSource) {
			defer wg.Done()

			snapshot, err := c.collector.CollectMetrics(ctx, pods, s)
			if err != nil {
				mu.Lock()
				errors = append(errors, fmt.Errorf("source %s: %w", s.TargetMetric, err))
				mu.Unlock()
				return
			}

			mu.Lock()
			results[s.TargetMetric] = snapshot
			mu.Unlock()
		}(source)
	}

	wg.Wait()

	if len(results) == 0 && len(errors) > 0 {
		return nil, fmt.Errorf("failed to collect metrics from any source: %v", errors)
	}

	return results, nil
}

// GetAggregator returns the underlying aggregator.
func (c *MultiSourceCollector) GetAggregator() *aggregation.DefaultMetricAggregator {
	return c.collector.aggregator
}
