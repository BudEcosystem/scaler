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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	scalerv1alpha1 "github.com/BudEcosystem/scaler/api/scaler/v1alpha1"
)

const (
	defaultHTTPTimeout = 10 * time.Second
)

// PodMetricFetcher fetches metrics from pod HTTP endpoints.
type PodMetricFetcher struct {
	httpClient *http.Client
}

// NewPodMetricFetcher creates a new PodMetricFetcher.
func NewPodMetricFetcher() *PodMetricFetcher {
	return &PodMetricFetcher{
		httpClient: &http.Client{
			Timeout: defaultHTTPTimeout,
		},
	}
}

// FetchPodMetrics fetches a metric from a specific pod's HTTP endpoint.
func (f *PodMetricFetcher) FetchPodMetrics(ctx context.Context, pod corev1.Pod, source scalerv1alpha1.MetricSource) (float64, error) {
	// Skip pods without an IP
	if pod.Status.PodIP == "" {
		return 0, fmt.Errorf("pod %s/%s has no IP address", pod.Namespace, pod.Name)
	}

	// Build the URL
	url := f.buildURL(pod, source)

	// Create request with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	// Execute request
	resp, err := f.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch metrics from %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("unexpected status code %d from %s", resp.StatusCode, url)
	}

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse the metric value
	return f.parseMetricValue(body, source)
}

// SourceType returns the metric source type.
func (f *PodMetricFetcher) SourceType() scalerv1alpha1.MetricSourceType {
	return scalerv1alpha1.MetricSourcePod
}

// buildURL constructs the URL for fetching metrics from a pod.
func (f *PodMetricFetcher) buildURL(pod corev1.Pod, source scalerv1alpha1.MetricSource) string {
	protocol := "http"
	if source.ProtocolType == scalerv1alpha1.HTTPS {
		protocol = "https"
	}

	port := source.Port
	if port == "" {
		port = "8000"
	}

	path := source.Path
	if path == "" {
		path = "/metrics"
	}

	// Handle inference engine configuration
	if source.InferenceEngineConfig != nil {
		if source.InferenceEngineConfig.MetricsPort != 0 {
			port = strconv.Itoa(int(source.InferenceEngineConfig.MetricsPort))
		}
		if source.InferenceEngineConfig.MetricsPath != "" {
			path = source.InferenceEngineConfig.MetricsPath
		}
	}

	return fmt.Sprintf("%s://%s:%s%s", protocol, pod.Status.PodIP, port, path)
}

// parseMetricValue extracts the target metric value from the response body.
func (f *PodMetricFetcher) parseMetricValue(body []byte, source scalerv1alpha1.MetricSource) (float64, error) {
	contentStr := string(body)

	// Try JSON parsing first
	if value, err := f.parseJSONMetric(contentStr, source.TargetMetric); err == nil {
		return value, nil
	}

	// Fall back to Prometheus format parsing
	if value, err := f.parsePrometheusMetric(contentStr, source.TargetMetric); err == nil {
		return value, nil
	}

	return 0, fmt.Errorf("metric %s not found in response", source.TargetMetric)
}

// parseJSONMetric attempts to parse the metric from JSON format.
func (f *PodMetricFetcher) parseJSONMetric(content string, metricName string) (float64, error) {
	// Try to parse as a simple JSON object
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(content), &data); err != nil {
		return 0, err
	}

	// Look for the metric in the JSON
	value, err := f.findMetricInJSON(data, metricName)
	if err != nil {
		return 0, err
	}

	return value, nil
}

// findMetricInJSON recursively searches for a metric in a JSON structure.
func (f *PodMetricFetcher) findMetricInJSON(data map[string]interface{}, metricName string) (float64, error) {
	// Check direct key match
	if val, ok := data[metricName]; ok {
		return f.toFloat64(val)
	}

	// Check nested structures
	for _, v := range data {
		if nested, ok := v.(map[string]interface{}); ok {
			if val, err := f.findMetricInJSON(nested, metricName); err == nil {
				return val, nil
			}
		}
	}

	return 0, fmt.Errorf("metric %s not found", metricName)
}

// parsePrometheusMetric parses Prometheus exposition format.
func (f *PodMetricFetcher) parsePrometheusMetric(content string, metricName string) (float64, error) {
	lines := strings.Split(content, "\n")

	// Build regex pattern for the metric
	// Matches: metric_name{labels} value or metric_name value
	pattern := fmt.Sprintf(`^%s(\{[^}]*\})?\s+([0-9eE.+-]+)`, regexp.QuoteMeta(metricName))
	re := regexp.MustCompile(pattern)

	var values []float64

	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Skip comments and empty lines
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		if matches := re.FindStringSubmatch(line); len(matches) >= 3 {
			value, err := strconv.ParseFloat(matches[2], 64)
			if err != nil {
				klog.V(5).InfoS("Failed to parse metric value", "line", line, "error", err)
				continue
			}
			values = append(values, value)
		}
	}

	if len(values) == 0 {
		return 0, fmt.Errorf("metric %s not found in Prometheus format", metricName)
	}

	// If multiple values (e.g., with different labels), return the average
	if len(values) == 1 {
		return values[0], nil
	}

	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values)), nil
}

// toFloat64 converts various types to float64.
func (f *PodMetricFetcher) toFloat64(val interface{}) (float64, error) {
	switch v := val.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case string:
		return strconv.ParseFloat(v, 64)
	case json.Number:
		return v.Float64()
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", val)
	}
}

// InferenceEngineMetricFetcher is a specialized fetcher for inference engines.
type InferenceEngineMetricFetcher struct {
	*PodMetricFetcher
}

// NewInferenceEngineMetricFetcher creates a new InferenceEngineMetricFetcher.
func NewInferenceEngineMetricFetcher() *InferenceEngineMetricFetcher {
	return &InferenceEngineMetricFetcher{
		PodMetricFetcher: NewPodMetricFetcher(),
	}
}

// FetchPodMetrics fetches metrics with inference-engine-specific parsing.
func (f *InferenceEngineMetricFetcher) FetchPodMetrics(ctx context.Context, pod corev1.Pod, source scalerv1alpha1.MetricSource) (float64, error) {
	if source.InferenceEngineConfig == nil {
		return f.PodMetricFetcher.FetchPodMetrics(ctx, pod, source)
	}

	// Use engine-specific metric mapping if needed
	metricName := source.TargetMetric

	// vLLM-specific metric name mapping
	if source.InferenceEngineConfig.EngineType == scalerv1alpha1.InferenceEngineVLLM {
		metricName = f.mapVLLMMetricName(source.TargetMetric)
	}

	// TGI-specific metric name mapping
	if source.InferenceEngineConfig.EngineType == scalerv1alpha1.InferenceEngineTGI {
		metricName = f.mapTGIMetricName(source.TargetMetric)
	}

	// Create a modified source with the mapped metric name
	modifiedSource := source
	modifiedSource.TargetMetric = metricName

	return f.PodMetricFetcher.FetchPodMetrics(ctx, pod, modifiedSource)
}

// mapVLLMMetricName maps common metric names to vLLM-specific names.
func (f *InferenceEngineMetricFetcher) mapVLLMMetricName(name string) string {
	// vLLM metric name mappings
	vllmMetrics := map[string]string{
		"gpu_cache_usage":       "vllm:gpu_cache_usage_perc",
		"gpu_cache_usage_perc":  "vllm:gpu_cache_usage_perc",
		"requests_running":      "vllm:num_requests_running",
		"num_requests_running":  "vllm:num_requests_running",
		"requests_waiting":      "vllm:num_requests_waiting",
		"num_requests_waiting":  "vllm:num_requests_waiting",
		"kv_cache_utilization":  "vllm:gpu_cache_usage_perc",
	}

	if mapped, ok := vllmMetrics[name]; ok {
		return mapped
	}
	return name
}

// mapTGIMetricName maps common metric names to TGI-specific names.
func (f *InferenceEngineMetricFetcher) mapTGIMetricName(name string) string {
	// TGI metric name mappings
	tgiMetrics := map[string]string{
		"requests_running":     "tgi_request_count",
		"queue_depth":          "tgi_queue_size",
		"batch_size":           "tgi_batch_current_size",
	}

	if mapped, ok := tgiMetrics[name]; ok {
		return mapped
	}
	return name
}

// SourceType returns the metric source type.
func (f *InferenceEngineMetricFetcher) SourceType() scalerv1alpha1.MetricSourceType {
	return scalerv1alpha1.MetricSourceInferenceEngine
}
