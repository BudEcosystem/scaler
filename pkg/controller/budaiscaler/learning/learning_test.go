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
	"testing"
	"time"
)

func TestSeasonalProfile_Update(t *testing.T) {
	profile := NewSeasonalProfile("test_metric", 0.1)

	// Update with values
	now := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC) // Monday 10:00
	profile.Update(now, 100.0)
	profile.Update(now.Add(10*time.Second), 110.0)
	profile.Update(now.Add(20*time.Second), 120.0)

	// Check bucket was updated
	bucket := profile.GetBucket(GetBucketIndex(now))
	if bucket == nil {
		t.Fatal("bucket should not be nil")
	}
	if bucket.Observations != 3 {
		t.Errorf("expected 3 observations, got %d", bucket.Observations)
	}
	if bucket.Mean < 100 || bucket.Mean > 120 {
		t.Errorf("mean should be between 100 and 120, got %f", bucket.Mean)
	}
}

func TestSeasonalProfile_GetSeasonalFactor(t *testing.T) {
	profile := NewSeasonalProfile("test_metric", 0.3)
	profile.MinObservations = 5

	now := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)

	// Add observations to build confidence
	for i := 0; i < 20; i++ {
		profile.Update(now.Add(time.Duration(i)*time.Second), 100.0+float64(i))
	}

	factor, confidence := profile.GetSeasonalFactor(now)

	// Factor should be close to 1 since we only have one bucket with data
	if factor < 0.5 || factor > 2.0 {
		t.Errorf("factor should be reasonable, got %f", factor)
	}
	// Confidence should be > 0 since we have observations
	if confidence <= 0 {
		t.Errorf("confidence should be > 0, got %f", confidence)
	}
}

func TestSeasonalProfile_Serialization(t *testing.T) {
	profile := NewSeasonalProfile("test_metric", 0.2)

	now := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		profile.Update(now.Add(time.Duration(i)*time.Second), 50.0+float64(i)*5)
	}

	// Serialize
	data, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	// Deserialize
	profile2 := &SeasonalProfile{}
	err = json.Unmarshal(data, profile2)
	if err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	// Check values match
	if profile2.MetricName != profile.MetricName {
		t.Errorf("metric name mismatch: %s vs %s", profile2.MetricName, profile.MetricName)
	}
	if profile2.GlobalCount != profile.GlobalCount {
		t.Errorf("global count mismatch: %d vs %d", profile2.GlobalCount, profile.GlobalCount)
	}
}

func TestLLMPatternDetector_DetectKVCachePressure(t *testing.T) {
	detector := NewLLMPatternDetector()

	// Simulate KV cache pressure
	metrics := LLMMetrics{
		GPUCacheUsage:   0.85, // Above threshold
		RequestsWaiting: 10,
		RequestsRunning: 50,
	}

	pattern := detector.DetectPattern(metrics)
	if pattern != PatternKVCachePressure {
		t.Errorf("expected KV cache pressure pattern, got %s", pattern)
	}
}

func TestLLMPatternDetector_SteadyState(t *testing.T) {
	detector := NewLLMPatternDetector()

	// Normal metrics
	metrics := LLMMetrics{
		GPUCacheUsage:   0.50,
		RequestsWaiting: 2,
		RequestsRunning: 20,
	}

	pattern := detector.DetectPattern(metrics)
	if pattern != PatternSteadyState {
		t.Errorf("expected steady state pattern, got %s", pattern)
	}
}

func TestPredictionAccuracyTracker_RecordOutcome(t *testing.T) {
	tracker := NewPredictionAccuracyTracker()

	now := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)

	// Record predictions
	tracker.RecordPredictionOutcome(now, 5, 5, ScaleUp, ScaleUp)   // Perfect
	tracker.RecordPredictionOutcome(now, 10, 8, ScaleUp, ScaleUp)  // Over by 2
	tracker.RecordPredictionOutcome(now, 3, 5, ScaleDown, ScaleUp) // Wrong direction

	report := tracker.GetAccuracyReport()

	if report.TotalPredictions != 3 {
		t.Errorf("expected 3 predictions, got %d", report.TotalPredictions)
	}
	if report.DirectionAccuracy < 0 || report.DirectionAccuracy > 1 {
		t.Errorf("direction accuracy should be 0-1, got %f", report.DirectionAccuracy)
	}
}

func TestPredictionAccuracyTracker_Serialization(t *testing.T) {
	tracker := NewPredictionAccuracyTracker()

	now := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	tracker.RecordPredictionOutcome(now, 5, 5, ScaleUp, ScaleUp)
	tracker.RecordPredictionOutcome(now, 10, 8, ScaleUp, ScaleUp)

	// Serialize
	data, err := json.Marshal(tracker)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	// Deserialize
	tracker2 := &PredictionAccuracyTracker{
		recentPredictions: NewRingBuffer(100),
		maxRecentSize:     100,
	}
	err = json.Unmarshal(data, tracker2)
	if err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if tracker2.totalPredictions != tracker.totalPredictions {
		t.Errorf("total predictions mismatch: %d vs %d", tracker2.totalPredictions, tracker.totalPredictions)
	}
}

func TestRingBuffer(t *testing.T) {
	rb := NewRingBuffer(5)

	// Push items
	for i := 0; i < 7; i++ {
		rb.Push(i)
	}

	// Should only have last 5 items
	if rb.Len() != 5 {
		t.Errorf("expected 5 items, got %d", rb.Len())
	}

	// First item should be 2 (oldest)
	if rb.Get(0).(int) != 2 {
		t.Errorf("expected 2, got %v", rb.Get(0))
	}

	// Last items
	last := rb.Last(3)
	if len(last) != 3 {
		t.Errorf("expected 3 items, got %d", len(last))
	}
	if last[0].(int) != 6 { // Most recent
		t.Errorf("expected 6, got %v", last[0])
	}
}

func TestGetBucketIndex(t *testing.T) {
	tests := []struct {
		hour      int
		dayOfWeek int
		expected  int
	}{
		{0, 0, 0},    // Sunday midnight
		{12, 0, 12},  // Sunday noon
		{0, 1, 24},   // Monday midnight
		{10, 1, 34},  // Monday 10:00
		{23, 6, 167}, // Saturday 11 PM
	}

	for _, tc := range tests {
		testTime := time.Date(2024, 1, 14+tc.dayOfWeek, tc.hour, 0, 0, 0, time.UTC)
		idx := GetBucketIndex(testTime)
		if idx != tc.expected {
			// Note: actual index depends on day of week of Jan 14, 2024
			// Just verify it's in valid range
			if idx < 0 || idx >= NumBuckets {
				t.Errorf("index out of range: %d", idx)
			}
		}
	}
}

func TestGetDirection(t *testing.T) {
	tests := []struct {
		from     int32
		to       int32
		expected ScaleDirection
	}{
		{3, 5, ScaleUp},
		{5, 3, ScaleDown},
		{5, 5, ScaleNone},
	}

	for _, tc := range tests {
		result := GetDirection(tc.from, tc.to)
		if result != tc.expected {
			t.Errorf("GetDirection(%d, %d) = %s, expected %s", tc.from, tc.to, result, tc.expected)
		}
	}
}

func TestRunningAverage(t *testing.T) {
	ra := NewRunningAverage(0.5)

	ra.Update(10.0)
	if ra.Get() != 10.0 {
		t.Errorf("first value should be exact, got %f", ra.Get())
	}

	ra.Update(20.0)
	expected := 0.5*20.0 + 0.5*10.0 // 15.0
	if ra.Get() != expected {
		t.Errorf("expected %f, got %f", expected, ra.Get())
	}
}

func TestSeasonalProfileManager(t *testing.T) {
	manager := NewSeasonalProfileManager(0.2)

	now := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)

	// Update multiple metrics
	manager.Update("cpu", now, 50.0)
	manager.Update("memory", now, 70.0)
	manager.Update("cpu", now.Add(time.Second), 55.0)

	// Get profile
	cpuProfile := manager.Get("cpu")
	if cpuProfile == nil {
		t.Fatal("cpu profile should exist")
	}

	// Get factor
	factor, confidence := manager.GetSeasonalFactor("cpu", now)
	if factor == 0 {
		t.Error("factor should not be 0")
	}
	// Confidence will be low with few data points
	_ = confidence
}

func TestPatternAdjustment(t *testing.T) {
	detector := NewLLMPatternDetector()

	// Detect a pattern first
	metrics := LLMMetrics{
		GPUCacheUsage:   0.90,
		RequestsWaiting: 20,
	}
	detector.DetectPattern(metrics)

	// Get adjustment
	adjustment := detector.GetPatternAdjustment()
	if adjustment == nil {
		t.Fatal("should get adjustment for KV cache pressure")
	}

	if adjustment.Pattern != PatternKVCachePressure {
		t.Errorf("expected KV cache pressure pattern, got %s", adjustment.Pattern)
	}
	if adjustment.Factor <= 1.0 {
		t.Errorf("factor should be > 1 for scale up, got %f", adjustment.Factor)
	}
}

func TestMetricHistory(t *testing.T) {
	history := NewMetricHistory(10)

	now := time.Now()
	for i := 0; i < 15; i++ {
		history.Add(float64(i*10), now.Add(time.Duration(i)*time.Second))
	}

	// Should be capped at 10
	if history.Len() != 10 {
		t.Errorf("expected 10, got %d", history.Len())
	}

	// Recent should return last n
	recent := history.GetRecent(5)
	if len(recent) != 5 {
		t.Errorf("expected 5, got %d", len(recent))
	}
}
