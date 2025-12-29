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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDCGMProvider(t *testing.T) {
	provider := NewDCGMProvider("http://localhost:9400")
	assert.NotNil(t, provider)
	assert.Equal(t, "http://localhost:9400", provider.endpoint)
}

func TestDCGMProvider_FetchMetrics(t *testing.T) {
	tests := []struct {
		name           string
		response       string
		statusCode     int
		expectedError  bool
		expectedGPUs   int
		expectedMemory float64
		expectedUtil   float64
	}{
		{
			name: "successful fetch with single GPU",
			response: `
# HELP DCGM_FI_DEV_GPU_UTIL GPU utilization
# TYPE DCGM_FI_DEV_GPU_UTIL gauge
DCGM_FI_DEV_GPU_UTIL{gpu="0",UUID="GPU-123"} 75
# HELP DCGM_FI_DEV_FB_USED Frame buffer memory used
# TYPE DCGM_FI_DEV_FB_USED gauge
DCGM_FI_DEV_FB_USED{gpu="0",UUID="GPU-123"} 8192
# HELP DCGM_FI_DEV_FB_TOTAL Frame buffer memory total
# TYPE DCGM_FI_DEV_FB_TOTAL gauge
DCGM_FI_DEV_FB_TOTAL{gpu="0",UUID="GPU-123"} 16384
`,
			statusCode:     http.StatusOK,
			expectedError:  false,
			expectedGPUs:   1,
			expectedMemory: 50.0, // 8192/16384 * 100
			expectedUtil:   75.0,
		},
		{
			name: "successful fetch with multiple GPUs",
			response: `
DCGM_FI_DEV_GPU_UTIL{gpu="0",UUID="GPU-123"} 80
DCGM_FI_DEV_GPU_UTIL{gpu="1",UUID="GPU-456"} 60
DCGM_FI_DEV_FB_USED{gpu="0",UUID="GPU-123"} 12288
DCGM_FI_DEV_FB_USED{gpu="1",UUID="GPU-456"} 8192
DCGM_FI_DEV_FB_TOTAL{gpu="0",UUID="GPU-123"} 16384
DCGM_FI_DEV_FB_TOTAL{gpu="1",UUID="GPU-456"} 16384
`,
			statusCode:     http.StatusOK,
			expectedError:  false,
			expectedGPUs:   2,
			expectedMemory: 62.5, // avg((12288+8192)/(16384*2)) * 100
			expectedUtil:   70.0, // avg(80+60)/2
		},
		{
			name:          "server error",
			response:      "",
			statusCode:    http.StatusInternalServerError,
			expectedError: true,
		},
		{
			name:           "empty response",
			response:       "",
			statusCode:     http.StatusOK,
			expectedError:  false,
			expectedGPUs:   0,
			expectedMemory: 0,
			expectedUtil:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/metrics", r.URL.Path)
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.response))
			}))
			defer server.Close()

			provider := NewDCGMProvider(server.URL)
			metrics, err := provider.FetchMetrics(context.Background())

			if tt.expectedError {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedGPUs, metrics.GPUCount)
			assert.InDelta(t, tt.expectedMemory, metrics.AverageMemoryUtilization, 0.1)
			assert.InDelta(t, tt.expectedUtil, metrics.AverageComputeUtilization, 0.1)
		})
	}
}

func TestDCGMProvider_ParsePrometheusMetrics(t *testing.T) {
	provider := NewDCGMProvider("http://localhost:9400")

	input := `
# HELP DCGM_FI_DEV_GPU_UTIL GPU utilization
DCGM_FI_DEV_GPU_UTIL{gpu="0",UUID="GPU-123",modelName="NVIDIA A100"} 85.5
DCGM_FI_DEV_FB_USED{gpu="0",UUID="GPU-123"} 10240
DCGM_FI_DEV_FB_TOTAL{gpu="0",UUID="GPU-123"} 40960
`

	gpuData := provider.parsePrometheusMetrics(input)

	require.Len(t, gpuData, 1)
	assert.Equal(t, "0", gpuData[0].ID)
	assert.Equal(t, "GPU-123", gpuData[0].UUID)
	assert.InDelta(t, 85.5, gpuData[0].ComputeUtilization, 0.01)
	assert.InDelta(t, 25.0, gpuData[0].MemoryUtilization, 0.1) // 10240/40960 * 100
}

func TestGPUMetrics_AverageCalculation(t *testing.T) {
	gpuData := []GPUData{
		{ID: "0", ComputeUtilization: 80, MemoryUtilization: 60},
		{ID: "1", ComputeUtilization: 60, MemoryUtilization: 40},
		{ID: "2", ComputeUtilization: 90, MemoryUtilization: 80},
	}

	metrics := calculateAverages(gpuData)

	assert.Equal(t, 3, metrics.GPUCount)
	assert.InDelta(t, 76.67, metrics.AverageComputeUtilization, 0.1) // (80+60+90)/3
	assert.InDelta(t, 60.0, metrics.AverageMemoryUtilization, 0.1)   // (60+40+80)/3
}

func TestGPUMetrics_ShouldScaleUp(t *testing.T) {
	tests := []struct {
		name             string
		metrics          *GPUMetrics
		memoryThreshold  float64
		computeThreshold float64
		expected         bool
	}{
		{
			name: "should scale up - memory exceeded",
			metrics: &GPUMetrics{
				AverageMemoryUtilization:  85,
				AverageComputeUtilization: 50,
			},
			memoryThreshold:  80,
			computeThreshold: 80,
			expected:         true,
		},
		{
			name: "should scale up - compute exceeded",
			metrics: &GPUMetrics{
				AverageMemoryUtilization:  50,
				AverageComputeUtilization: 85,
			},
			memoryThreshold:  80,
			computeThreshold: 80,
			expected:         true,
		},
		{
			name: "should not scale - both below threshold",
			metrics: &GPUMetrics{
				AverageMemoryUtilization:  70,
				AverageComputeUtilization: 70,
			},
			memoryThreshold:  80,
			computeThreshold: 80,
			expected:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.metrics.ShouldScaleUp(tt.memoryThreshold, tt.computeThreshold)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGPUMetrics_ShouldScaleDown(t *testing.T) {
	tests := []struct {
		name             string
		metrics          *GPUMetrics
		memoryThreshold  float64
		computeThreshold float64
		expected         bool
	}{
		{
			name: "should scale down - both well below threshold",
			metrics: &GPUMetrics{
				AverageMemoryUtilization:  30,
				AverageComputeUtilization: 25,
			},
			memoryThreshold:  80,
			computeThreshold: 80,
			expected:         true,
		},
		{
			name: "should not scale down - memory still high",
			metrics: &GPUMetrics{
				AverageMemoryUtilization:  70,
				AverageComputeUtilization: 25,
			},
			memoryThreshold:  80,
			computeThreshold: 80,
			expected:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Scale down threshold is typically 50% of scale up threshold
			result := tt.metrics.ShouldScaleDown(tt.memoryThreshold*0.5, tt.computeThreshold*0.5)
			assert.Equal(t, tt.expected, result)
		})
	}
}
