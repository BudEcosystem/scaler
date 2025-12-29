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

package cost

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// CloudProvider represents a cloud provider.
type CloudProvider string

const (
	AWS   CloudProvider = "aws"
	GCP   CloudProvider = "gcp"
	Azure CloudProvider = "azure"
)

// Budget represents cost budget constraints.
type Budget struct {
	// HourlyLimit is the maximum hourly spend.
	HourlyLimit float64

	// DailyLimit is the maximum daily spend.
	DailyLimit float64
}

// ScalingRecommendation contains cost-aware scaling recommendation.
type ScalingRecommendation struct {
	// RecommendedReplicas is the recommended replica count.
	RecommendedReplicas int32

	// ProjectedHourlyCost is the projected hourly cost.
	ProjectedHourlyCost float64

	// ProjectedDailyCost is the projected daily cost.
	ProjectedDailyCost float64

	// Reason explains the recommendation.
	Reason string

	// BudgetUtilization is the percentage of budget used.
	BudgetUtilization float64
}

// Calculator calculates costs for cloud resources.
type Calculator struct {
	provider CloudProvider
	pricing  map[string]InstancePricing
}

// InstancePricing contains pricing information for an instance type.
type InstancePricing struct {
	OnDemandHourly float64
	SpotHourly     float64
	GPUCount       int
	GPUType        string
}

// NewCalculator creates a new cost calculator.
func NewCalculator(provider CloudProvider) *Calculator {
	calc := &Calculator{
		provider: provider,
		pricing:  make(map[string]InstancePricing),
	}
	calc.initializePricing()
	return calc
}

// initializePricing sets up pricing data for common GPU instances.
func (c *Calculator) initializePricing() {
	// AWS GPU instance pricing (approximate, US East)
	awsPricing := map[string]InstancePricing{
		"p4d.24xlarge":  {OnDemandHourly: 32.77, SpotHourly: 9.83, GPUCount: 8, GPUType: "A100"},
		"p4de.24xlarge": {OnDemandHourly: 40.97, SpotHourly: 12.29, GPUCount: 8, GPUType: "A100"},
		"p3.2xlarge":    {OnDemandHourly: 3.06, SpotHourly: 0.92, GPUCount: 1, GPUType: "V100"},
		"p3.8xlarge":    {OnDemandHourly: 12.24, SpotHourly: 3.67, GPUCount: 4, GPUType: "V100"},
		"p3.16xlarge":   {OnDemandHourly: 24.48, SpotHourly: 7.34, GPUCount: 8, GPUType: "V100"},
		"g5.xlarge":     {OnDemandHourly: 1.02, SpotHourly: 0.31, GPUCount: 1, GPUType: "A10G"},
		"g5.2xlarge":    {OnDemandHourly: 1.21, SpotHourly: 0.36, GPUCount: 1, GPUType: "A10G"},
		"g5.4xlarge":    {OnDemandHourly: 1.62, SpotHourly: 0.49, GPUCount: 1, GPUType: "A10G"},
		"g5.12xlarge":   {OnDemandHourly: 5.67, SpotHourly: 1.70, GPUCount: 4, GPUType: "A10G"},
		"g4dn.xlarge":   {OnDemandHourly: 0.526, SpotHourly: 0.16, GPUCount: 1, GPUType: "T4"},
		"g4dn.12xlarge": {OnDemandHourly: 3.912, SpotHourly: 1.17, GPUCount: 4, GPUType: "T4"},
	}

	// GCP GPU instance pricing
	gcpPricing := map[string]InstancePricing{
		"a2-highgpu-1g":    {OnDemandHourly: 3.67, SpotHourly: 1.10, GPUCount: 1, GPUType: "A100"},
		"a2-highgpu-2g":    {OnDemandHourly: 7.35, SpotHourly: 2.20, GPUCount: 2, GPUType: "A100"},
		"a2-highgpu-4g":    {OnDemandHourly: 14.69, SpotHourly: 4.41, GPUCount: 4, GPUType: "A100"},
		"a2-highgpu-8g":    {OnDemandHourly: 29.39, SpotHourly: 8.82, GPUCount: 8, GPUType: "A100"},
		"n1-standard-4-t4": {OnDemandHourly: 0.55, SpotHourly: 0.17, GPUCount: 1, GPUType: "T4"},
	}

	// Azure GPU instance pricing
	azurePricing := map[string]InstancePricing{
		"Standard_NC6s_v3":    {OnDemandHourly: 3.06, SpotHourly: 0.92, GPUCount: 1, GPUType: "V100"},
		"Standard_NC12s_v3":   {OnDemandHourly: 6.12, SpotHourly: 1.84, GPUCount: 2, GPUType: "V100"},
		"Standard_NC24s_v3":   {OnDemandHourly: 12.24, SpotHourly: 3.67, GPUCount: 4, GPUType: "V100"},
		"Standard_ND96asr_v4": {OnDemandHourly: 27.20, SpotHourly: 8.16, GPUCount: 8, GPUType: "A100"},
	}

	switch c.provider {
	case AWS:
		c.pricing = awsPricing
	case GCP:
		c.pricing = gcpPricing
	case Azure:
		c.pricing = azurePricing
	}
}

// CalculateHourlyCost calculates the hourly cost for given instances.
func (c *Calculator) CalculateHourlyCost(ctx context.Context, instanceType string, count int32, isSpot bool) (float64, error) {
	pricing, ok := c.pricing[instanceType]
	if !ok {
		return 0, fmt.Errorf("unknown instance type: %s", instanceType)
	}

	var pricePerHour float64
	if isSpot {
		pricePerHour = pricing.SpotHourly
	} else {
		pricePerHour = pricing.OnDemandHourly
	}

	return pricePerHour * float64(count), nil
}

// ProjectDailyCost projects daily cost from hourly cost.
func (c *Calculator) ProjectDailyCost(hourlyCost float64) float64 {
	return hourlyCost * 24
}

// IsWithinBudget checks if current cost is within budget.
func (c *Calculator) IsWithinBudget(currentHourlyCost float64, budget *Budget) bool {
	if budget == nil {
		return true
	}

	// Check hourly limit
	if budget.HourlyLimit > 0 && currentHourlyCost > budget.HourlyLimit {
		return false
	}

	// Check projected daily limit
	if budget.DailyLimit > 0 {
		projectedDaily := c.ProjectDailyCost(currentHourlyCost)
		if projectedDaily > budget.DailyLimit {
			return false
		}
	}

	return true
}

// GetScalingRecommendation provides a cost-aware scaling recommendation.
func (c *Calculator) GetScalingRecommendation(
	ctx context.Context,
	currentReplicas int32,
	desiredReplicas int32,
	instanceType string,
	isSpot bool,
	budget *Budget,
) *ScalingRecommendation {
	// Scale down is always allowed
	if desiredReplicas <= currentReplicas {
		cost, _ := c.CalculateHourlyCost(ctx, instanceType, desiredReplicas, isSpot)
		return &ScalingRecommendation{
			RecommendedReplicas: desiredReplicas,
			ProjectedHourlyCost: cost,
			ProjectedDailyCost:  c.ProjectDailyCost(cost),
			Reason:              "scale down always allowed",
		}
	}

	// For scale up, check budget constraints
	pricing, ok := c.pricing[instanceType]
	if !ok {
		// Unknown instance, allow the scaling
		return &ScalingRecommendation{
			RecommendedReplicas: desiredReplicas,
			Reason:              "unknown instance type, allowing scaling",
		}
	}

	var pricePerInstance float64
	if isSpot {
		pricePerInstance = pricing.SpotHourly
	} else {
		pricePerInstance = pricing.OnDemandHourly
	}

	// If no budget, allow full scaling
	if budget == nil || (budget.HourlyLimit == 0 && budget.DailyLimit == 0) {
		cost := pricePerInstance * float64(desiredReplicas)
		return &ScalingRecommendation{
			RecommendedReplicas: desiredReplicas,
			ProjectedHourlyCost: cost,
			ProjectedDailyCost:  c.ProjectDailyCost(cost),
			Reason:              "within budget (no budget set)",
		}
	}

	// Calculate max replicas within budget
	maxReplicasByHourly := int32(999)
	maxReplicasByDaily := int32(999)

	if budget.HourlyLimit > 0 {
		maxReplicasByHourly = int32(budget.HourlyLimit / pricePerInstance)
	}
	if budget.DailyLimit > 0 {
		maxHourly := budget.DailyLimit / 24
		maxReplicasByDaily = int32(maxHourly / pricePerInstance)
	}

	maxReplicas := min(maxReplicasByHourly, maxReplicasByDaily)
	recommendedReplicas := min(desiredReplicas, maxReplicas)

	// Ensure we don't go below current
	if recommendedReplicas < currentReplicas {
		recommendedReplicas = currentReplicas
	}

	cost := pricePerInstance * float64(recommendedReplicas)
	budgetUtil := 0.0
	if budget.HourlyLimit > 0 {
		budgetUtil = (cost / budget.HourlyLimit) * 100
	}

	reason := "within budget"
	if recommendedReplicas < desiredReplicas {
		reason = fmt.Sprintf("limited by budget (max %d replicas)", maxReplicas)
	}

	return &ScalingRecommendation{
		RecommendedReplicas: recommendedReplicas,
		ProjectedHourlyCost: cost,
		ProjectedDailyCost:  c.ProjectDailyCost(cost),
		Reason:              reason,
		BudgetUtilization:   budgetUtil,
	}
}

// CostTracker tracks cost usage over time.
type CostTracker struct {
	mu      sync.RWMutex
	records []CostRecord
}

// CostRecord represents a single cost recording.
type CostRecord struct {
	Cost      float64
	Timestamp time.Time
}

// NewCostTracker creates a new cost tracker.
func NewCostTracker() *CostTracker {
	return &CostTracker{
		records: make([]CostRecord, 0),
	}
}

// RecordUsage records a cost usage.
func (t *CostTracker) RecordUsage(cost float64, timestamp time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.records = append(t.records, CostRecord{
		Cost:      cost,
		Timestamp: timestamp,
	})

	// Keep only last 24 hours of records
	cutoff := time.Now().Add(-24 * time.Hour)
	filtered := make([]CostRecord, 0)
	for _, r := range t.records {
		if r.Timestamp.After(cutoff) {
			filtered = append(filtered, r)
		}
	}
	t.records = filtered
}

// GetHourlyUsage returns the total cost in the last hour.
func (t *CostTracker) GetHourlyUsage() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	cutoff := time.Now().Add(-1 * time.Hour)
	var total float64
	for _, r := range t.records {
		if r.Timestamp.After(cutoff) {
			total += r.Cost
		}
	}
	return total
}

// GetDailyUsage returns the total cost in the last 24 hours.
func (t *CostTracker) GetDailyUsage() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	cutoff := time.Now().Add(-24 * time.Hour)
	var total float64
	for _, r := range t.records {
		if r.Timestamp.After(cutoff) {
			total += r.Cost
		}
	}
	return total
}

// SpotInstanceManager manages spot instance operations.
type SpotInstanceManager struct {
	provider CloudProvider
	pricing  map[string]InstancePricing
}

// NewSpotInstanceManager creates a new spot instance manager.
func NewSpotInstanceManager(provider CloudProvider) *SpotInstanceManager {
	calc := NewCalculator(provider)
	return &SpotInstanceManager{
		provider: provider,
		pricing:  calc.pricing,
	}
}

// IsSpotAvailable checks if spot instances are available.
func (m *SpotInstanceManager) IsSpotAvailable(ctx context.Context, instanceType string, zone string) bool {
	// In a real implementation, this would check cloud provider APIs
	// For now, return true as a mock
	_, ok := m.pricing[instanceType]
	return ok
}

// GetSpotSavings returns the savings percentage for spot instances.
func (m *SpotInstanceManager) GetSpotSavings(instanceType string) float64 {
	pricing, ok := m.pricing[instanceType]
	if !ok || pricing.OnDemandHourly == 0 {
		return 0
	}
	return 1 - (pricing.SpotHourly / pricing.OnDemandHourly)
}

func min(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}
