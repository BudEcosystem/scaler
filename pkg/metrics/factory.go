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
	"fmt"
	"sync"

	"k8s.io/client-go/rest"
	"k8s.io/metrics/pkg/client/clientset/versioned"
	custommetrics "k8s.io/metrics/pkg/client/custom_metrics"

	scalerv1alpha1 "github.com/BudEcosystem/scaler/api/scaler/v1alpha1"
)

// MetricFetcherFactory creates and caches MetricFetchers for different source types.
type MetricFetcherFactory interface {
	// For returns the appropriate fetcher for the given metric source.
	For(source scalerv1alpha1.MetricSource) (MetricFetcher, error)

	// ForExternal returns the appropriate external fetcher for the given metric source.
	ForExternal(source scalerv1alpha1.MetricSource) (ExternalMetricFetcher, error)
}

// DefaultMetricFetcherFactory is the default implementation of MetricFetcherFactory.
type DefaultMetricFetcherFactory struct {
	mu sync.RWMutex

	// Cached fetchers
	podFetcher      MetricFetcher
	resourceFetcher MetricFetcher

	// External fetchers
	prometheusFetcher ExternalMetricFetcher
	externalFetcher   ExternalMetricFetcher

	// Clients for K8s metrics APIs
	resourceMetricsClient versioned.Interface
	customMetricsClient   custommetrics.CustomMetricsClient

	// REST config for creating clients
	restConfig *rest.Config
}

// NewDefaultMetricFetcherFactory creates a new DefaultMetricFetcherFactory.
func NewDefaultMetricFetcherFactory(restConfig *rest.Config) (*DefaultMetricFetcherFactory, error) {
	factory := &DefaultMetricFetcherFactory{
		restConfig: restConfig,
	}

	// Create resource metrics client if possible
	if restConfig != nil {
		resourceClient, err := versioned.NewForConfig(restConfig)
		if err == nil {
			factory.resourceMetricsClient = resourceClient
		}
	}

	return factory, nil
}

// For returns the appropriate fetcher for the given metric source.
func (f *DefaultMetricFetcherFactory) For(source scalerv1alpha1.MetricSource) (MetricFetcher, error) {
	switch source.MetricSourceType {
	case scalerv1alpha1.MetricSourcePod:
		return f.getPodFetcher()

	case scalerv1alpha1.MetricSourceResource:
		return f.getResourceFetcher()

	case scalerv1alpha1.MetricSourceInferenceEngine:
		// Inference engine metrics are fetched via pod endpoints
		return f.getPodFetcher()

	case scalerv1alpha1.MetricSourceCustom:
		// Custom metrics use the K8s custom metrics API
		return f.getResourceFetcher()

	default:
		return nil, fmt.Errorf("unsupported metric source type for pod metrics: %s", source.MetricSourceType)
	}
}

// ForExternal returns the appropriate external fetcher for the given metric source.
func (f *DefaultMetricFetcherFactory) ForExternal(source scalerv1alpha1.MetricSource) (ExternalMetricFetcher, error) {
	switch source.MetricSourceType {
	case scalerv1alpha1.MetricSourcePrometheus:
		return f.getPrometheusFetcher()

	case scalerv1alpha1.MetricSourceExternal:
		return f.getExternalFetcher()

	default:
		return nil, fmt.Errorf("unsupported metric source type for external metrics: %s", source.MetricSourceType)
	}
}

// getPodFetcher returns or creates the pod metric fetcher.
func (f *DefaultMetricFetcherFactory) getPodFetcher() (MetricFetcher, error) {
	f.mu.RLock()
	if f.podFetcher != nil {
		f.mu.RUnlock()
		return f.podFetcher, nil
	}
	f.mu.RUnlock()

	f.mu.Lock()
	defer f.mu.Unlock()

	// Double-check after acquiring write lock
	if f.podFetcher != nil {
		return f.podFetcher, nil
	}

	f.podFetcher = NewPodMetricFetcher()
	return f.podFetcher, nil
}

// getResourceFetcher returns or creates the resource metric fetcher.
func (f *DefaultMetricFetcherFactory) getResourceFetcher() (MetricFetcher, error) {
	f.mu.RLock()
	if f.resourceFetcher != nil {
		f.mu.RUnlock()
		return f.resourceFetcher, nil
	}
	f.mu.RUnlock()

	f.mu.Lock()
	defer f.mu.Unlock()

	// Double-check after acquiring write lock
	if f.resourceFetcher != nil {
		return f.resourceFetcher, nil
	}

	if f.resourceMetricsClient == nil {
		return nil, fmt.Errorf("resource metrics client not available")
	}

	f.resourceFetcher = NewResourceMetricFetcher(f.resourceMetricsClient)
	return f.resourceFetcher, nil
}

// getPrometheusFetcher returns or creates the prometheus fetcher.
func (f *DefaultMetricFetcherFactory) getPrometheusFetcher() (ExternalMetricFetcher, error) {
	f.mu.RLock()
	if f.prometheusFetcher != nil {
		f.mu.RUnlock()
		return f.prometheusFetcher, nil
	}
	f.mu.RUnlock()

	f.mu.Lock()
	defer f.mu.Unlock()

	// Double-check after acquiring write lock
	if f.prometheusFetcher != nil {
		return f.prometheusFetcher, nil
	}

	f.prometheusFetcher = NewPrometheusMetricFetcher()
	return f.prometheusFetcher, nil
}

// getExternalFetcher returns or creates the external fetcher.
func (f *DefaultMetricFetcherFactory) getExternalFetcher() (ExternalMetricFetcher, error) {
	f.mu.RLock()
	if f.externalFetcher != nil {
		f.mu.RUnlock()
		return f.externalFetcher, nil
	}
	f.mu.RUnlock()

	f.mu.Lock()
	defer f.mu.Unlock()

	// Double-check after acquiring write lock
	if f.externalFetcher != nil {
		return f.externalFetcher, nil
	}

	f.externalFetcher = NewExternalMetricFetcher()
	return f.externalFetcher, nil
}

// IsExternalSource returns true if the metric source is an external source.
func IsExternalSource(sourceType scalerv1alpha1.MetricSourceType) bool {
	switch sourceType {
	case scalerv1alpha1.MetricSourcePrometheus, scalerv1alpha1.MetricSourceExternal:
		return true
	default:
		return false
	}
}

// IsPodSource returns true if the metric source requires per-pod collection.
func IsPodSource(sourceType scalerv1alpha1.MetricSourceType) bool {
	switch sourceType {
	case scalerv1alpha1.MetricSourcePod, scalerv1alpha1.MetricSourceResource,
		scalerv1alpha1.MetricSourceCustom, scalerv1alpha1.MetricSourceInferenceEngine:
		return true
	default:
		return false
	}
}
