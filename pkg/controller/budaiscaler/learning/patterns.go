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

package learning

import (
	"encoding/json"
	"math"
	"sync"
	"time"
)

// MetricPoint represents a single metric measurement with timestamp
type MetricPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

// LLMPatternDetector detects patterns specific to LLM workloads
type LLMPatternDetector struct {
	mu sync.RWMutex

	// Recent metric history for pattern matching (last 30 minutes at 10s resolution = 180 points)
	recentHistory map[string]*MetricHistory
	historyWindow time.Duration

	// Learned pattern templates
	batchPatterns   []PatternTemplate
	kvCachePatterns []KVCachePattern

	// Detection state
	currentPattern    LLMWorkloadPattern
	patternStartTime  time.Time
	patternConfidence float64

	// Configuration thresholds
	kvCachePressureThreshold float64
	batchSpikeThreshold      float64
	minQueuedRequests        int
}

// MetricHistory stores recent metric values for trend analysis
type MetricHistory struct {
	Points    []MetricPoint `json:"points"`
	MaxPoints int           `json:"maxPoints"`
}

// NewMetricHistory creates a new metric history
func NewMetricHistory(maxPoints int) *MetricHistory {
	return &MetricHistory{
		Points:    make([]MetricPoint, 0, maxPoints),
		MaxPoints: maxPoints,
	}
}

// Add adds a new point to the history
func (h *MetricHistory) Add(value float64, timestamp time.Time) {
	h.Points = append(h.Points, MetricPoint{
		Timestamp: timestamp,
		Value:     value,
	})

	// Trim if over capacity
	if len(h.Points) > h.MaxPoints {
		h.Points = h.Points[len(h.Points)-h.MaxPoints:]
	}
}

// GetRecent returns the last n points
func (h *MetricHistory) GetRecent(n int) []MetricPoint {
	if n > len(h.Points) {
		n = len(h.Points)
	}
	return h.Points[len(h.Points)-n:]
}

// Average returns the average of points in a time range
func (h *MetricHistory) Average(startSecondsAgo, endSecondsAgo int) float64 {
	now := time.Now()
	startTime := now.Add(-time.Duration(startSecondsAgo) * time.Second)
	endTime := now.Add(-time.Duration(endSecondsAgo) * time.Second)

	var sum float64
	var count int

	for _, p := range h.Points {
		if p.Timestamp.After(startTime) && p.Timestamp.Before(endTime) {
			sum += p.Value
			count++
		}
	}

	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

// Len returns the number of points
func (h *MetricHistory) Len() int {
	return len(h.Points)
}

// NewLLMPatternDetector creates a new LLM pattern detector
func NewLLMPatternDetector() *LLMPatternDetector {
	return &LLMPatternDetector{
		recentHistory:            make(map[string]*MetricHistory),
		historyWindow:            30 * time.Minute,
		batchPatterns:            make([]PatternTemplate, 0),
		kvCachePatterns:          make([]KVCachePattern, 0),
		currentPattern:           PatternSteadyState,
		kvCachePressureThreshold: KVCachePressureThreshold,
		batchSpikeThreshold:      BatchSpikeThreshold,
		minQueuedRequests:        5,
	}
}

// UpdateHistory adds new metric values to the history
func (d *LLMPatternDetector) UpdateHistory(metrics map[string]float64, llmMetrics LLMMetrics) {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()

	// Update standard metrics
	for metric, value := range metrics {
		if _, exists := d.recentHistory[metric]; !exists {
			d.recentHistory[metric] = NewMetricHistory(180) // 30 min at 10s intervals
		}
		d.recentHistory[metric].Add(value, now)
	}

	// Update LLM-specific metrics
	d.updateLLMMetricHistory("gpu_cache_usage", llmMetrics.GPUCacheUsage, now)
	d.updateLLMMetricHistory("requests_waiting", llmMetrics.RequestsWaiting, now)
	d.updateLLMMetricHistory("requests_running", llmMetrics.RequestsRunning, now)
	d.updateLLMMetricHistory("token_throughput", llmMetrics.TokenThroughput, now)
}

func (d *LLMPatternDetector) updateLLMMetricHistory(name string, value float64, timestamp time.Time) {
	if _, exists := d.recentHistory[name]; !exists {
		d.recentHistory[name] = NewMetricHistory(180)
	}
	d.recentHistory[name].Add(value, timestamp)
}

// DetectPattern analyzes current metrics to identify workload pattern
func (d *LLMPatternDetector) DetectPattern(llmMetrics LLMMetrics) LLMWorkloadPattern {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Check for KV cache pressure (highest priority - indicates immediate scaling need)
	if d.detectKVCachePressure(llmMetrics) {
		if d.currentPattern != PatternKVCachePressure {
			d.patternStartTime = time.Now()
			d.currentPattern = PatternKVCachePressure
			d.patternConfidence = 0.85
		}
		return PatternKVCachePressure
	}

	// Check for batch ingestion pattern
	if d.detectBatchSpike() {
		if d.currentPattern != PatternBatchIngestion {
			d.patternStartTime = time.Now()
			d.currentPattern = PatternBatchIngestion
			d.patternConfidence = 0.75
		}
		return PatternBatchIngestion
	}

	// Check for cold start (low cache, low requests, but increasing)
	if d.detectColdStart(llmMetrics) {
		if d.currentPattern != PatternColdStart {
			d.patternStartTime = time.Now()
			d.currentPattern = PatternColdStart
			d.patternConfidence = 0.7
		}
		return PatternColdStart
	}

	// Check for traffic spike/drain based on trend
	trend := d.calculateTrend("requests_running")
	if trend > 0.5 {
		if d.currentPattern != PatternTrafficSpike {
			d.patternStartTime = time.Now()
			d.currentPattern = PatternTrafficSpike
			d.patternConfidence = 0.6 + trend*0.3
		}
		return PatternTrafficSpike
	} else if trend < -0.5 {
		if d.currentPattern != PatternTrafficDrain {
			d.patternStartTime = time.Now()
			d.currentPattern = PatternTrafficDrain
			d.patternConfidence = 0.6 + (-trend)*0.3
		}
		return PatternTrafficDrain
	}

	// Default to steady state
	d.currentPattern = PatternSteadyState
	d.patternConfidence = 0.5
	return PatternSteadyState
}

// detectKVCachePressure checks if the KV cache is under pressure
func (d *LLMPatternDetector) detectKVCachePressure(llmMetrics LLMMetrics) bool {
	// KV cache pressure: high cache usage + requests waiting
	if llmMetrics.GPUCacheUsage > d.kvCachePressureThreshold &&
		llmMetrics.RequestsWaiting > float64(d.minQueuedRequests) {
		return true
	}

	// Also check if cache usage is rapidly increasing
	history := d.recentHistory["gpu_cache_usage"]
	if history != nil && history.Len() >= 6 {
		recent := history.GetRecent(6)
		if len(recent) >= 6 {
			oldAvg := (recent[0].Value + recent[1].Value + recent[2].Value) / 3
			newAvg := (recent[3].Value + recent[4].Value + recent[5].Value) / 3
			if newAvg-oldAvg > 0.15 && newAvg > 0.7 { // Rapid increase toward high usage
				return true
			}
		}
	}

	return false
}

// detectBatchSpike uses pattern matching on recent history
func (d *LLMPatternDetector) detectBatchSpike() bool {
	history := d.recentHistory["requests_running"]
	if history == nil || history.Len() < 10 {
		return false
	}

	recent := history.GetRecent(10)
	baseline := history.Average(300, 600) // 5-10 minutes ago

	if baseline == 0 {
		return false
	}

	// Check if current value is significantly above baseline
	currentValue := recent[len(recent)-1].Value
	if currentValue > baseline*d.batchSpikeThreshold {
		// Verify it's a spike pattern (sudden increase)
		midpoint := recent[len(recent)/2].Value
		if midpoint > baseline*1.5 && currentValue > midpoint*1.2 {
			return true
		}
	}

	// Check against learned batch patterns
	for _, pattern := range d.batchPatterns {
		if d.matchPattern(recent, pattern) > 0.7 {
			return true
		}
	}

	return false
}

// detectColdStart checks if the workload is starting from cold state
func (d *LLMPatternDetector) detectColdStart(llmMetrics LLMMetrics) bool {
	// Cold start: low cache usage, low requests, but showing increasing trend
	if llmMetrics.GPUCacheUsage > 0.2 {
		return false // Cache is already warmed
	}

	if llmMetrics.RequestsRunning > 10 {
		return false // Too many requests for cold start
	}

	// Check for increasing trend in requests
	trend := d.calculateTrend("requests_running")
	if trend > 0.3 {
		return true
	}

	return false
}

// calculateTrend calculates the trend for a metric (-1 to 1)
func (d *LLMPatternDetector) calculateTrend(metricName string) float64 {
	history := d.recentHistory[metricName]
	if history == nil || history.Len() < 6 {
		return 0
	}

	recent := history.GetRecent(12)
	if len(recent) < 6 {
		return 0
	}

	// Compare first half to second half
	n := len(recent)
	firstHalfSum := 0.0
	secondHalfSum := 0.0

	for i := 0; i < n/2; i++ {
		firstHalfSum += recent[i].Value
	}
	for i := n / 2; i < n; i++ {
		secondHalfSum += recent[i].Value
	}

	firstHalfAvg := firstHalfSum / float64(n/2)
	secondHalfAvg := secondHalfSum / float64(n-n/2)

	// Normalize to -1 to 1
	if firstHalfAvg == 0 {
		if secondHalfAvg > 0 {
			return 1.0
		}
		return 0
	}

	change := (secondHalfAvg - firstHalfAvg) / firstHalfAvg
	return math.Max(-1.0, math.Min(1.0, change))
}

// matchPattern compares recent values against a pattern template
func (d *LLMPatternDetector) matchPattern(recent []MetricPoint, pattern PatternTemplate) float64 {
	if len(pattern.Signature) == 0 || len(recent) < len(pattern.Signature) {
		return 0
	}

	// Normalize recent values
	var sum float64
	for _, p := range recent {
		sum += p.Value
	}
	avg := sum / float64(len(recent))
	if avg == 0 {
		return 0
	}

	normalized := make([]float64, len(pattern.Signature))
	for i := 0; i < len(pattern.Signature); i++ {
		idx := len(recent) - len(pattern.Signature) + i
		normalized[i] = recent[idx].Value / avg
	}

	// Calculate cosine similarity
	var dotProduct, normA, normB float64
	for i := 0; i < len(pattern.Signature); i++ {
		dotProduct += normalized[i] * pattern.Signature[i]
		normA += normalized[i] * normalized[i]
		normB += pattern.Signature[i] * pattern.Signature[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// GetPatternAdjustment returns the recommended scaling adjustment for the current pattern
func (d *LLMPatternDetector) GetPatternAdjustment() *PatternAdjustment {
	d.mu.RLock()
	defer d.mu.RUnlock()

	switch d.currentPattern {
	case PatternKVCachePressure:
		// Find matching KV cache pattern
		for _, p := range d.kvCachePatterns {
			if p.TotalPredictions > 0 {
				accuracy := float64(p.SuccessfulPredictions) / float64(p.TotalPredictions)
				if accuracy > 0.6 {
					return &PatternAdjustment{
						Pattern:    PatternKVCachePressure,
						Factor:     p.OptimalScaleAction,
						Confidence: accuracy * d.patternConfidence,
						Reason:     "KV cache pressure detected - scaling up to relieve memory pressure",
					}
				}
			}
		}
		// Default KV cache adjustment
		return &PatternAdjustment{
			Pattern:    PatternKVCachePressure,
			Factor:     1.5,
			Confidence: d.patternConfidence * 0.7,
			Reason:     "KV cache pressure detected - default scale up",
		}

	case PatternBatchIngestion:
		// Find matching batch pattern
		for _, p := range d.batchPatterns {
			if p.Confidence > 0.6 {
				return &PatternAdjustment{
					Pattern:    PatternBatchIngestion,
					Factor:     p.OptimalScaleMultiplier,
					Confidence: p.Confidence * d.patternConfidence,
					Reason:     "Batch ingestion pattern detected",
				}
			}
		}
		// Default batch adjustment
		return &PatternAdjustment{
			Pattern:    PatternBatchIngestion,
			Factor:     2.0,
			Confidence: d.patternConfidence * 0.6,
			Reason:     "Batch ingestion detected - scaling for burst",
		}

	case PatternColdStart:
		return &PatternAdjustment{
			Pattern:    PatternColdStart,
			Factor:     1.2,
			Confidence: d.patternConfidence * 0.5,
			Reason:     "Cold start detected - preemptive scale up",
		}

	case PatternTrafficSpike:
		return &PatternAdjustment{
			Pattern:    PatternTrafficSpike,
			Factor:     1.5,
			Confidence: d.patternConfidence,
			Reason:     "Traffic spike detected",
		}

	case PatternTrafficDrain:
		return &PatternAdjustment{
			Pattern:    PatternTrafficDrain,
			Factor:     0.8,
			Confidence: d.patternConfidence,
			Reason:     "Traffic drain detected - preparing for scale down",
		}

	default:
		return nil
	}
}

// LearnPattern learns from a successful scaling decision
func (d *LLMPatternDetector) LearnPattern(pattern LLMWorkloadPattern, successful bool, actualMultiplier float64) {
	d.mu.Lock()
	defer d.mu.Unlock()

	switch pattern {
	case PatternKVCachePressure:
		// Find or create KV cache pattern
		found := false
		for i := range d.kvCachePatterns {
			if d.kvCachePatterns[i].CacheUsageThreshold == d.kvCachePressureThreshold {
				d.kvCachePatterns[i].TotalPredictions++
				if successful {
					d.kvCachePatterns[i].SuccessfulPredictions++
					// Update optimal scale action with EWMA
					d.kvCachePatterns[i].OptimalScaleAction =
						0.2*actualMultiplier + 0.8*d.kvCachePatterns[i].OptimalScaleAction
				}
				found = true
				break
			}
		}
		if !found {
			d.kvCachePatterns = append(d.kvCachePatterns, KVCachePattern{
				CacheUsageThreshold:   d.kvCachePressureThreshold,
				RequestsWaitingMin:    d.minQueuedRequests,
				OptimalScaleAction:    actualMultiplier,
				SuccessfulPredictions: 1,
				TotalPredictions:      1,
			})
		}

	case PatternBatchIngestion:
		// Learn batch pattern signature from recent history
		history := d.recentHistory["requests_running"]
		if history != nil && history.Len() >= 10 {
			recent := history.GetRecent(10)
			signature := d.extractSignature(recent)

			// Find similar pattern or create new
			found := false
			for i := range d.batchPatterns {
				if d.matchPattern(recent, d.batchPatterns[i]) > 0.8 {
					d.batchPatterns[i].MatchCount++
					if successful {
						d.batchPatterns[i].Confidence =
							0.1 + 0.9*d.batchPatterns[i].Confidence
						d.batchPatterns[i].OptimalScaleMultiplier =
							0.2*actualMultiplier + 0.8*d.batchPatterns[i].OptimalScaleMultiplier
					} else {
						d.batchPatterns[i].Confidence *= 0.9
					}
					found = true
					break
				}
			}
			if !found && len(d.batchPatterns) < 10 {
				d.batchPatterns = append(d.batchPatterns, PatternTemplate{
					Name:                   "batch_pattern_" + time.Now().Format("20060102_150405"),
					Signature:              signature,
					OptimalScaleMultiplier: actualMultiplier,
					Confidence:             0.5,
					MatchCount:             1,
				})
			}
		}
	}
}

// extractSignature extracts a normalized signature from recent metric points
func (d *LLMPatternDetector) extractSignature(points []MetricPoint) []float64 {
	if len(points) == 0 {
		return nil
	}

	var sum float64
	for _, p := range points {
		sum += p.Value
	}
	avg := sum / float64(len(points))
	if avg == 0 {
		avg = 1
	}

	signature := make([]float64, len(points))
	for i, p := range points {
		signature[i] = p.Value / avg
	}
	return signature
}

// GetCurrentPattern returns the current detected pattern and confidence
func (d *LLMPatternDetector) GetCurrentPattern() (LLMWorkloadPattern, float64) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.currentPattern, d.patternConfidence
}

// GetPatternDuration returns how long the current pattern has been active
func (d *LLMPatternDetector) GetPatternDuration() time.Duration {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.patternStartTime.IsZero() {
		return 0
	}
	return time.Since(d.patternStartTime)
}

// MarshalJSON implements json.Marshaler
func (d *LLMPatternDetector) MarshalJSON() ([]byte, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return json.Marshal(struct {
		BatchPatterns            []PatternTemplate  `json:"batchPatterns"`
		KVCachePatterns          []KVCachePattern   `json:"kvCachePatterns"`
		CurrentPattern           LLMWorkloadPattern `json:"currentPattern"`
		PatternConfidence        float64            `json:"patternConfidence"`
		KVCachePressureThreshold float64            `json:"kvCachePressureThreshold"`
		BatchSpikeThreshold      float64            `json:"batchSpikeThreshold"`
	}{
		BatchPatterns:            d.batchPatterns,
		KVCachePatterns:          d.kvCachePatterns,
		CurrentPattern:           d.currentPattern,
		PatternConfidence:        d.patternConfidence,
		KVCachePressureThreshold: d.kvCachePressureThreshold,
		BatchSpikeThreshold:      d.batchSpikeThreshold,
	})
}

// UnmarshalJSON implements json.Unmarshaler
func (d *LLMPatternDetector) UnmarshalJSON(data []byte) error {
	var aux struct {
		BatchPatterns            []PatternTemplate  `json:"batchPatterns"`
		KVCachePatterns          []KVCachePattern   `json:"kvCachePatterns"`
		CurrentPattern           LLMWorkloadPattern `json:"currentPattern"`
		PatternConfidence        float64            `json:"patternConfidence"`
		KVCachePressureThreshold float64            `json:"kvCachePressureThreshold"`
		BatchSpikeThreshold      float64            `json:"batchSpikeThreshold"`
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.batchPatterns = aux.BatchPatterns
	d.kvCachePatterns = aux.KVCachePatterns
	d.currentPattern = aux.CurrentPattern
	d.patternConfidence = aux.PatternConfidence

	if aux.KVCachePressureThreshold > 0 {
		d.kvCachePressureThreshold = aux.KVCachePressureThreshold
	}
	if aux.BatchSpikeThreshold > 0 {
		d.batchSpikeThreshold = aux.BatchSpikeThreshold
	}

	return nil
}
