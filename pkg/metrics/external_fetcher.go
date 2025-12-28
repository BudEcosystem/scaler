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

	scalerv1alpha1 "github.com/BudEcosystem/scaler/api/scaler/v1alpha1"
)

// ExternalMetricFetcherImpl fetches metrics from external HTTP endpoints.
type ExternalMetricFetcherImpl struct {
	httpClient *http.Client
}

// NewExternalMetricFetcher creates a new ExternalMetricFetcherImpl.
func NewExternalMetricFetcher() *ExternalMetricFetcherImpl {
	return &ExternalMetricFetcherImpl{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// FetchExternalMetrics fetches metrics from an external HTTP endpoint.
func (f *ExternalMetricFetcherImpl) FetchExternalMetrics(ctx context.Context, source scalerv1alpha1.MetricSource, namespace string, podLabels map[string]string) (float64, error) {
	if source.Endpoint == "" {
		return 0, fmt.Errorf("endpoint is required for external metric source")
	}

	// Build the URL with optional query parameters
	queryURL, err := f.buildURL(source, namespace, podLabels)
	if err != nil {
		return 0, fmt.Errorf("failed to build URL: %w", err)
	}

	// Create request with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, queryURL, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	// Add headers if configured
	if source.Headers != nil {
		for key, value := range source.Headers {
			req.Header.Set(key, value)
		}
	}

	// Execute request
	resp, err := f.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to query external endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("external query failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse the response
	return f.parseResponse(resp.Body, source.TargetMetric)
}

// SourceType returns the metric source type.
func (f *ExternalMetricFetcherImpl) SourceType() scalerv1alpha1.MetricSourceType {
	return scalerv1alpha1.MetricSourceExternal
}

// buildURL constructs the external API URL with query parameters.
func (f *ExternalMetricFetcherImpl) buildURL(source scalerv1alpha1.MetricSource, namespace string, podLabels map[string]string) (string, error) {
	endpoint := source.Endpoint

	// Ensure endpoint has a scheme
	if len(endpoint) >= 4 && endpoint[:4] != "http" {
		endpoint = "http://" + endpoint
	}

	parsedURL, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("invalid endpoint URL: %w", err)
	}

	// Add query parameters
	query := parsedURL.Query()

	// Add namespace if specified
	if namespace != "" {
		query.Set("namespace", namespace)
	}

	// Add metric name if specified
	if source.TargetMetric != "" {
		query.Set("metric", source.TargetMetric)
	}

	// Add pod labels as query parameters
	for key, value := range podLabels {
		query.Set("label_"+key, value)
	}

	parsedURL.RawQuery = query.Encode()
	return parsedURL.String(), nil
}

// ExternalResponse represents a generic external API response.
type ExternalResponse struct {
	Value  interface{}            `json:"value"`
	Metric string                 `json:"metric,omitempty"`
	Data   map[string]interface{} `json:"data,omitempty"`
	Error  string                 `json:"error,omitempty"`
}

// parseResponse parses the external API response and extracts the metric value.
func (f *ExternalMetricFetcherImpl) parseResponse(body io.Reader, metricName string) (float64, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response body: %w", err)
	}

	// Try to parse as a simple value first
	if value, err := strconv.ParseFloat(string(data), 64); err == nil {
		return value, nil
	}

	// Try to parse as JSON
	var response ExternalResponse
	if err := json.Unmarshal(data, &response); err != nil {
		// Try parsing as a generic map
		return f.parseGenericJSON(data, metricName)
	}

	// Check for error in response
	if response.Error != "" {
		return 0, fmt.Errorf("external endpoint returned error: %s", response.Error)
	}

	// Extract value from response
	if response.Value != nil {
		return f.toFloat64(response.Value)
	}

	// Try to find the metric in the data field
	if response.Data != nil && metricName != "" {
		if value, ok := response.Data[metricName]; ok {
			return f.toFloat64(value)
		}
	}

	return 0, fmt.Errorf("could not extract metric value from response")
}

// parseGenericJSON attempts to parse a generic JSON response.
func (f *ExternalMetricFetcherImpl) parseGenericJSON(data []byte, metricName string) (float64, error) {
	var jsonData map[string]interface{}
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return 0, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	// If metric name is specified, look for it in the response
	if metricName != "" {
		if value, ok := jsonData[metricName]; ok {
			return f.toFloat64(value)
		}

		// Try nested lookup
		if value, err := f.findNestedValue(jsonData, metricName); err == nil {
			return value, nil
		}
	}

	// Try common field names
	for _, field := range []string{"value", "metric", "result", "data"} {
		if value, ok := jsonData[field]; ok {
			if floatVal, err := f.toFloat64(value); err == nil {
				return floatVal, nil
			}
		}
	}

	return 0, fmt.Errorf("metric %s not found in response", metricName)
}

// findNestedValue searches for a value in a nested JSON structure.
func (f *ExternalMetricFetcherImpl) findNestedValue(data map[string]interface{}, key string) (float64, error) {
	for k, v := range data {
		if k == key {
			return f.toFloat64(v)
		}
		if nested, ok := v.(map[string]interface{}); ok {
			if value, err := f.findNestedValue(nested, key); err == nil {
				return value, nil
			}
		}
	}
	return 0, fmt.Errorf("key %s not found", key)
}

// toFloat64 converts various types to float64.
func (f *ExternalMetricFetcherImpl) toFloat64(val interface{}) (float64, error) {
	switch v := val.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case string:
		return strconv.ParseFloat(v, 64)
	case json.Number:
		return v.Float64()
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", val)
	}
}
