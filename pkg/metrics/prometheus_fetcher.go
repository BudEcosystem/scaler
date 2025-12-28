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
	"net/url"
	"strconv"
	"time"

	"k8s.io/klog/v2"

	scalerv1alpha1 "github.com/BudEcosystem/scaler/api/scaler/v1alpha1"
)

// PrometheusMetricFetcher fetches metrics via PromQL queries.
type PrometheusMetricFetcher struct {
	httpClient *http.Client
}

// NewPrometheusMetricFetcher creates a new PrometheusMetricFetcher.
func NewPrometheusMetricFetcher() *PrometheusMetricFetcher {
	return &PrometheusMetricFetcher{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// FetchExternalMetrics executes a PromQL query against a Prometheus server.
func (f *PrometheusMetricFetcher) FetchExternalMetrics(ctx context.Context, source scalerv1alpha1.MetricSource, namespace string, podLabels map[string]string) (float64, error) {
	if source.PromQL == "" {
		return 0, fmt.Errorf("promQL query is required for prometheus metric source")
	}

	if source.Endpoint == "" {
		return 0, fmt.Errorf("endpoint is required for prometheus metric source")
	}

	// Build the Prometheus API URL
	queryURL := f.buildQueryURL(source.Endpoint, source.PromQL)

	// Create request with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, queryURL, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	// Execute request
	resp, err := f.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to query Prometheus: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("prometheus query failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse the response
	return f.parsePrometheusResponse(resp.Body)
}

// SourceType returns the metric source type.
func (f *PrometheusMetricFetcher) SourceType() scalerv1alpha1.MetricSourceType {
	return scalerv1alpha1.MetricSourcePrometheus
}

// buildQueryURL constructs the Prometheus query API URL.
func (f *PrometheusMetricFetcher) buildQueryURL(endpoint, query string) string {
	// Ensure endpoint has a scheme
	if endpoint[:4] != "http" {
		endpoint = "http://" + endpoint
	}

	// Build the query URL
	baseURL := endpoint
	if baseURL[len(baseURL)-1] != '/' {
		baseURL += "/"
	}
	baseURL += "api/v1/query"

	// Add query parameter
	return fmt.Sprintf("%s?query=%s", baseURL, url.QueryEscape(query))
}

// PrometheusResponse represents the Prometheus API response.
type PrometheusResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"`
		} `json:"result"`
	} `json:"data"`
	Error     string `json:"error,omitempty"`
	ErrorType string `json:"errorType,omitempty"`
}

// parsePrometheusResponse parses the Prometheus API response and extracts the metric value.
func (f *PrometheusMetricFetcher) parsePrometheusResponse(body io.Reader) (float64, error) {
	var response PrometheusResponse
	if err := json.NewDecoder(body).Decode(&response); err != nil {
		return 0, fmt.Errorf("failed to parse Prometheus response: %w", err)
	}

	if response.Status != "success" {
		return 0, fmt.Errorf("prometheus query failed: %s - %s", response.ErrorType, response.Error)
	}

	if len(response.Data.Result) == 0 {
		return 0, fmt.Errorf("no data returned from Prometheus query")
	}

	// If there's a single result, return its value
	if len(response.Data.Result) == 1 {
		return f.extractValue(response.Data.Result[0].Value)
	}

	// If there are multiple results, calculate the average
	var sum float64
	var count int
	for _, result := range response.Data.Result {
		value, err := f.extractValue(result.Value)
		if err != nil {
			klog.V(5).InfoS("Failed to extract value from result", "metric", result.Metric, "error", err)
			continue
		}
		sum += value
		count++
	}

	if count == 0 {
		return 0, fmt.Errorf("no valid values in Prometheus response")
	}

	return sum / float64(count), nil
}

// extractValue extracts a float64 value from a Prometheus value tuple.
func (f *PrometheusMetricFetcher) extractValue(value []interface{}) (float64, error) {
	if len(value) < 2 {
		return 0, fmt.Errorf("invalid value format")
	}

	// The second element is the value as a string
	valueStr, ok := value[1].(string)
	if !ok {
		return 0, fmt.Errorf("value is not a string")
	}

	return strconv.ParseFloat(valueStr, 64)
}
