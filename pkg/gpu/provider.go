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

package gpu

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// GPUMetrics contains aggregated GPU metrics.
type GPUMetrics struct {
	// GPUCount is the number of GPUs.
	GPUCount int

	// AverageMemoryUtilization is the average memory utilization across all GPUs (0-100).
	AverageMemoryUtilization float64

	// AverageComputeUtilization is the average compute utilization across all GPUs (0-100).
	AverageComputeUtilization float64

	// GPUType is the type of GPU (e.g., "NVIDIA A100").
	GPUType string

	// PerGPUData contains per-GPU metrics.
	PerGPUData []GPUData
}

// GPUData contains metrics for a single GPU.
type GPUData struct {
	// ID is the GPU index.
	ID string

	// UUID is the unique identifier.
	UUID string

	// MemoryUtilization is the memory utilization (0-100).
	MemoryUtilization float64

	// ComputeUtilization is the compute utilization (0-100).
	ComputeUtilization float64

	// MemoryUsedMB is the memory used in MB.
	MemoryUsedMB float64

	// MemoryTotalMB is the total memory in MB.
	MemoryTotalMB float64

	// ModelName is the GPU model name.
	ModelName string
}

// ShouldScaleUp returns true if GPU metrics exceed thresholds.
func (m *GPUMetrics) ShouldScaleUp(memoryThreshold, computeThreshold float64) bool {
	return m.AverageMemoryUtilization > memoryThreshold ||
		m.AverageComputeUtilization > computeThreshold
}

// ShouldScaleDown returns true if GPU metrics are well below thresholds.
func (m *GPUMetrics) ShouldScaleDown(memoryThreshold, computeThreshold float64) bool {
	return m.AverageMemoryUtilization < memoryThreshold &&
		m.AverageComputeUtilization < computeThreshold
}

// Provider defines the interface for GPU metrics providers.
type Provider interface {
	// FetchMetrics fetches GPU metrics.
	FetchMetrics(ctx context.Context) (*GPUMetrics, error)
}

// DCGMProvider fetches GPU metrics from NVIDIA DCGM exporter.
type DCGMProvider struct {
	endpoint   string
	httpClient *http.Client
}

// NewDCGMProvider creates a new DCGM provider.
func NewDCGMProvider(endpoint string) *DCGMProvider {
	return &DCGMProvider{
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// FetchMetrics fetches GPU metrics from DCGM exporter.
func (p *DCGMProvider) FetchMetrics(ctx context.Context) (*GPUMetrics, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint+"/metrics", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch metrics: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	gpuData := p.parsePrometheusMetrics(string(body))
	return calculateAverages(gpuData), nil
}

// parsePrometheusMetrics parses DCGM Prometheus metrics format.
func (p *DCGMProvider) parsePrometheusMetrics(body string) []GPUData {
	gpuMap := make(map[string]*GPUData)

	// Regex patterns for DCGM metrics
	utilPattern := regexp.MustCompile(`DCGM_FI_DEV_GPU_UTIL\{[^}]*gpu="(\d+)"[^}]*UUID="([^"]+)"[^}]*\}\s+([\d.]+)`)
	fbUsedPattern := regexp.MustCompile(`DCGM_FI_DEV_FB_USED\{[^}]*gpu="(\d+)"[^}]*\}\s+([\d.]+)`)
	fbTotalPattern := regexp.MustCompile(`DCGM_FI_DEV_FB_TOTAL\{[^}]*gpu="(\d+)"[^}]*\}\s+([\d.]+)`)

	lines := strings.Split(body, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		// Parse GPU utilization
		if matches := utilPattern.FindStringSubmatch(line); matches != nil {
			gpuID := matches[1]
			uuid := matches[2]
			util, _ := strconv.ParseFloat(matches[3], 64)

			if _, exists := gpuMap[gpuID]; !exists {
				gpuMap[gpuID] = &GPUData{ID: gpuID, UUID: uuid}
			}
			gpuMap[gpuID].ComputeUtilization = util
		}

		// Parse frame buffer used
		if matches := fbUsedPattern.FindStringSubmatch(line); matches != nil {
			gpuID := matches[1]
			used, _ := strconv.ParseFloat(matches[2], 64)

			if _, exists := gpuMap[gpuID]; !exists {
				gpuMap[gpuID] = &GPUData{ID: gpuID}
			}
			gpuMap[gpuID].MemoryUsedMB = used
		}

		// Parse frame buffer total
		if matches := fbTotalPattern.FindStringSubmatch(line); matches != nil {
			gpuID := matches[1]
			total, _ := strconv.ParseFloat(matches[2], 64)

			if _, exists := gpuMap[gpuID]; !exists {
				gpuMap[gpuID] = &GPUData{ID: gpuID}
			}
			gpuMap[gpuID].MemoryTotalMB = total
		}
	}

	// Calculate memory utilization and convert to slice
	var result []GPUData
	for _, gpu := range gpuMap {
		if gpu.MemoryTotalMB > 0 {
			gpu.MemoryUtilization = (gpu.MemoryUsedMB / gpu.MemoryTotalMB) * 100
		}
		result = append(result, *gpu)
	}

	return result
}

// calculateAverages calculates average metrics from GPU data.
func calculateAverages(gpuData []GPUData) *GPUMetrics {
	metrics := &GPUMetrics{
		GPUCount:   len(gpuData),
		PerGPUData: gpuData,
	}

	if len(gpuData) == 0 {
		return metrics
	}

	var totalMemory, totalCompute float64
	for _, gpu := range gpuData {
		totalMemory += gpu.MemoryUtilization
		totalCompute += gpu.ComputeUtilization
	}

	metrics.AverageMemoryUtilization = totalMemory / float64(len(gpuData))
	metrics.AverageComputeUtilization = totalCompute / float64(len(gpuData))

	// Set GPU type from first GPU if available
	if len(gpuData) > 0 && gpuData[0].ModelName != "" {
		metrics.GPUType = gpuData[0].ModelName
	}

	return metrics
}

// MockProvider is a mock GPU provider for testing.
type MockProvider struct {
	Metrics *GPUMetrics
	Error   error
}

// FetchMetrics returns mock metrics.
func (p *MockProvider) FetchMetrics(ctx context.Context) (*GPUMetrics, error) {
	if p.Error != nil {
		return nil, p.Error
	}
	return p.Metrics, nil
}
