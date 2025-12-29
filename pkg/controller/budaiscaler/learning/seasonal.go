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

// SeasonalProfile maintains learned patterns per metric using EWMA
// It tracks 168 time buckets (24 hours x 7 days) to capture weekly patterns
type SeasonalProfile struct {
	mu sync.RWMutex

	MetricName string                     `json:"metricName"`
	Buckets    [NumBuckets]SeasonalBucket `json:"buckets"`

	// Global statistics for normalization
	GlobalMean     float64 `json:"globalMean"`
	GlobalVariance float64 `json:"globalVariance"`
	GlobalCount    int     `json:"globalCount"`

	// Learning parameters
	Alpha           float64 `json:"alpha"`           // EWMA smoothing (0.1-0.3)
	MinObservations int     `json:"minObservations"` // Minimum before confident prediction
}

// NewSeasonalProfile creates a new seasonal profile for a metric
func NewSeasonalProfile(metricName string, alpha float64) *SeasonalProfile {
	if alpha <= 0 || alpha > 1 {
		alpha = DefaultAlpha
	}

	profile := &SeasonalProfile{
		MetricName:      metricName,
		Alpha:           alpha,
		MinObservations: DefaultMinObservations,
	}

	// Initialize all buckets
	for i := 0; i < NumBuckets; i++ {
		hour, dayOfWeek := GetTimeFromBucketIndex(i)
		profile.Buckets[i] = SeasonalBucket{
			HourOfDay: hour,
			DayOfWeek: dayOfWeek,
		}
	}

	return profile
}

// GetSeasonalFactor returns the expected deviation from global mean for a given time
// Returns factor (multiplier) and confidence (0-1)
func (p *SeasonalProfile) GetSeasonalFactor(t time.Time) (factor float64, confidence float64) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	bucket := p.getBucketAt(t)

	// Not enough data - return neutral with low confidence
	if bucket.Observations < p.MinObservations {
		conf := 0.0
		if p.MinObservations > 0 {
			conf = float64(bucket.Observations) / float64(p.MinObservations)
		}
		return 1.0, conf
	}

	// Factor = bucket mean / global mean
	if p.GlobalMean == 0 || math.IsNaN(p.GlobalMean) {
		return 1.0, 0.5
	}

	factor = bucket.Mean / p.GlobalMean

	// Clamp factor to reasonable range to avoid extreme predictions
	factor = math.Max(0.1, math.Min(10.0, factor))

	// Calculate confidence based on observations and variance
	observationConfidence := math.Min(1.0, float64(bucket.Observations)/1000.0)

	varianceConfidence := 1.0
	if p.GlobalVariance > 0 && bucket.Variance > 0 {
		varianceRatio := bucket.Variance / p.GlobalVariance
		varianceConfidence = 1.0 / (1.0 + varianceRatio)
	}

	confidence = observationConfidence * varianceConfidence

	return factor, confidence
}

// Update updates the profile with a new observation
func (p *SeasonalProfile) Update(t time.Time, value float64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	bucketIdx := GetBucketIndex(t)
	bucket := &p.Buckets[bucketIdx]

	// EWMA update for bucket mean
	oldMean := bucket.Mean
	if bucket.Observations == 0 {
		bucket.Mean = value
		bucket.Variance = 0
	} else {
		bucket.Mean = p.Alpha*value + (1-p.Alpha)*bucket.Mean

		// Online variance update (Welford's algorithm adapted for EWMA)
		delta := value - oldMean
		bucket.Variance = (1-p.Alpha)*bucket.Variance + p.Alpha*delta*(value-bucket.Mean)
	}

	bucket.Observations++
	bucket.LastUpdated = t

	// Update global statistics
	p.updateGlobalStats(value)
}

// updateGlobalStats updates global mean and variance using EWMA
func (p *SeasonalProfile) updateGlobalStats(value float64) {
	p.GlobalCount++

	if p.GlobalCount == 1 {
		p.GlobalMean = value
		p.GlobalVariance = 0
	} else {
		oldMean := p.GlobalMean
		p.GlobalMean = p.Alpha*value + (1-p.Alpha)*p.GlobalMean
		delta := value - oldMean
		p.GlobalVariance = (1-p.Alpha)*p.GlobalVariance + p.Alpha*delta*(value-p.GlobalMean)
	}
}

// getBucketAt returns the bucket for a given time
func (p *SeasonalProfile) getBucketAt(t time.Time) *SeasonalBucket {
	idx := GetBucketIndex(t)
	return &p.Buckets[idx]
}

// GetBucket returns the bucket at the specified index
func (p *SeasonalProfile) GetBucket(index int) *SeasonalBucket {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if index < 0 || index >= NumBuckets {
		return nil
	}
	return &p.Buckets[index]
}

// ResetBucket resets a specific bucket to learn fresh
func (p *SeasonalProfile) ResetBucket(hour, dayOfWeek int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	idx := dayOfWeek*24 + hour
	if idx >= 0 && idx < NumBuckets {
		p.Buckets[idx] = SeasonalBucket{
			HourOfDay: hour,
			DayOfWeek: dayOfWeek,
		}
	}
}

// IsComplete returns true if all buckets have at least MinObservations
func (p *SeasonalProfile) IsComplete() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	for i := 0; i < NumBuckets; i++ {
		if p.Buckets[i].Observations < p.MinObservations {
			return false
		}
	}
	return true
}

// GetCompleteness returns the percentage of buckets with sufficient data (0-100)
func (p *SeasonalProfile) GetCompleteness() float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()

	complete := 0
	for i := 0; i < NumBuckets; i++ {
		if p.Buckets[i].Observations >= p.MinObservations {
			complete++
		}
	}
	return float64(complete) / float64(NumBuckets) * 100
}

// GetTotalObservations returns the total number of observations across all buckets
func (p *SeasonalProfile) GetTotalObservations() int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	total := 0
	for i := 0; i < NumBuckets; i++ {
		total += p.Buckets[i].Observations
	}
	return total
}

// SetAlpha updates the EWMA smoothing factor
func (p *SeasonalProfile) SetAlpha(alpha float64) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if alpha > 0 && alpha <= 1 {
		p.Alpha = alpha
	}
}

// GetAlpha returns the current EWMA smoothing factor
func (p *SeasonalProfile) GetAlpha() float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.Alpha
}

// PredictValue predicts the expected metric value for a given time
func (p *SeasonalProfile) PredictValue(t time.Time) (value float64, confidence float64) {
	factor, conf := p.GetSeasonalFactor(t)

	p.mu.RLock()
	value = p.GlobalMean * factor
	p.mu.RUnlock()

	return value, conf
}

// GetPeakHours returns the hours with highest average values for a given day
func (p *SeasonalProfile) GetPeakHours(dayOfWeek int, topN int) []int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	type hourMean struct {
		hour int
		mean float64
	}

	hours := make([]hourMean, 24)
	for h := 0; h < 24; h++ {
		idx := dayOfWeek*24 + h
		hours[h] = hourMean{hour: h, mean: p.Buckets[idx].Mean}
	}

	// Sort by mean descending
	for i := 0; i < len(hours)-1; i++ {
		for j := i + 1; j < len(hours); j++ {
			if hours[j].mean > hours[i].mean {
				hours[i], hours[j] = hours[j], hours[i]
			}
		}
	}

	if topN > 24 {
		topN = 24
	}

	result := make([]int, topN)
	for i := 0; i < topN; i++ {
		result[i] = hours[i].hour
	}
	return result
}

// MarshalJSON implements json.Marshaler
func (p *SeasonalProfile) MarshalJSON() ([]byte, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	type alias SeasonalProfile
	return json.Marshal(&struct {
		*alias
	}{
		alias: (*alias)(p),
	})
}

// UnmarshalJSON implements json.Unmarshaler
func (p *SeasonalProfile) UnmarshalJSON(data []byte) error {
	type alias SeasonalProfile
	aux := &struct {
		*alias
	}{
		alias: (*alias)(p),
	}
	return json.Unmarshal(data, aux)
}

// SeasonalProfileManager manages multiple seasonal profiles for different metrics
type SeasonalProfileManager struct {
	mu       sync.RWMutex
	profiles map[string]*SeasonalProfile
	alpha    float64
}

// NewSeasonalProfileManager creates a new manager
func NewSeasonalProfileManager(alpha float64) *SeasonalProfileManager {
	if alpha <= 0 || alpha > 1 {
		alpha = DefaultAlpha
	}
	return &SeasonalProfileManager{
		profiles: make(map[string]*SeasonalProfile),
		alpha:    alpha,
	}
}

// GetOrCreate returns an existing profile or creates a new one
func (m *SeasonalProfileManager) GetOrCreate(metricName string) *SeasonalProfile {
	m.mu.Lock()
	defer m.mu.Unlock()

	if profile, exists := m.profiles[metricName]; exists {
		return profile
	}

	profile := NewSeasonalProfile(metricName, m.alpha)
	m.profiles[metricName] = profile
	return profile
}

// Get returns a profile by name or nil if not found
func (m *SeasonalProfileManager) Get(metricName string) *SeasonalProfile {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.profiles[metricName]
}

// Update updates the profile for a metric with a new value
func (m *SeasonalProfileManager) Update(metricName string, t time.Time, value float64) {
	profile := m.GetOrCreate(metricName)
	profile.Update(t, value)
}

// GetSeasonalFactor returns the seasonal factor for a metric at a given time
func (m *SeasonalProfileManager) GetSeasonalFactor(metricName string, t time.Time) (factor float64, confidence float64) {
	m.mu.RLock()
	profile := m.profiles[metricName]
	m.mu.RUnlock()

	if profile == nil {
		return 1.0, 0.0
	}
	return profile.GetSeasonalFactor(t)
}

// GetAllProfiles returns all profiles
func (m *SeasonalProfileManager) GetAllProfiles() map[string]*SeasonalProfile {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*SeasonalProfile, len(m.profiles))
	for k, v := range m.profiles {
		result[k] = v
	}
	return result
}

// SetAlpha updates alpha for all profiles
func (m *SeasonalProfileManager) SetAlpha(alpha float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.alpha = alpha
	for _, profile := range m.profiles {
		profile.SetAlpha(alpha)
	}
}

// MarshalJSON implements json.Marshaler
func (m *SeasonalProfileManager) MarshalJSON() ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return json.Marshal(struct {
		Profiles map[string]*SeasonalProfile `json:"profiles"`
		Alpha    float64                     `json:"alpha"`
	}{
		Profiles: m.profiles,
		Alpha:    m.alpha,
	})
}

// UnmarshalJSON implements json.Unmarshaler
func (m *SeasonalProfileManager) UnmarshalJSON(data []byte) error {
	var aux struct {
		Profiles map[string]*SeasonalProfile `json:"profiles"`
		Alpha    float64                     `json:"alpha"`
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.profiles = aux.Profiles
	if m.profiles == nil {
		m.profiles = make(map[string]*SeasonalProfile)
	}
	m.alpha = aux.Alpha

	return nil
}
