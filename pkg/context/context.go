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

// Package context provides the ScalingContext interface and implementation
// for managing scaling configuration as a single source of truth.
package context

import (
	"strconv"
	"time"

	scalerv1alpha1 "github.com/BudEcosystem/scaler/api/scaler/v1alpha1"
	"github.com/BudEcosystem/scaler/pkg/types"
)

// Default values for scaling configuration.
const (
	DefaultMaxScaleUpRate           = 2.0
	DefaultMaxScaleDownRate         = 2.0
	DefaultUpFluctuationTolerance   = 0.1
	DefaultDownFluctuationTolerance = 0.1
	DefaultScaleUpCooldownWindow    = 0 * time.Second
	DefaultScaleDownCooldownWindow  = 300 * time.Second // 5 minutes
	DefaultPanicThreshold           = 2.0
	DefaultPanicWindow              = 60 * time.Second
	DefaultStableWindow             = 180 * time.Second
	DefaultActivationScale          = int32(1)
)

// ScalingContext provides configuration for scaling algorithms.
// It acts as the single source of truth for all scaling parameters.
type ScalingContext interface {
	// Replica bounds
	GetMinReplicas() int32
	GetMaxReplicas() int32
	SetMinReplicas(replicas int32)
	SetMaxReplicas(replicas int32)

	// Scaling rates
	GetMaxScaleUpRate() float64
	GetMaxScaleDownRate() float64

	// Tolerance thresholds
	GetUpFluctuationTolerance() float64
	GetDownFluctuationTolerance() float64

	// Cooldown windows
	GetScaleUpCooldownWindow() time.Duration
	GetScaleDownCooldownWindow() time.Duration

	// Time windows for metric aggregation
	GetPanicWindow() time.Duration
	GetStableWindow() time.Duration

	// KPA-specific configuration
	GetPanicThreshold() float64
	GetInPanicMode() bool
	SetInPanicMode(inPanic bool)

	// Scale-to-zero configuration
	GetScaleToZero() bool
	GetActivationScale() int32

	// Per-metric target values
	GetTargetValueForMetric(metricName string) (float64, bool)
	SetTargetValueForMetric(metricName string, value float64)

	// GPU configuration
	IsGPUAware() bool
	GetGPUMemoryThreshold() int32
	GetGPUComputeThreshold() int32

	// Cost configuration
	IsCostAware() bool
	GetBudgetPerHour() float64
	GetBudgetPerDay() float64

	// Prediction configuration
	IsPredictionEnabled() bool
	GetLookAheadMinutes() int32

	// ScalingContextProvider compatible methods
	GetScaleUpRate() float64
	GetScaleDownRate() float64
	GetTolerance() float64
	GetPanicWindowSeconds() int32
	GetStableWindowSeconds() int32
	GetScaleUpStabilizationSeconds() int32
	GetScaleDownStabilizationSeconds() int32
}

// baseScalingContext is the default implementation of ScalingContext.
type baseScalingContext struct {
	// Replica bounds
	minReplicas int32
	maxReplicas int32

	// Scaling rates
	maxScaleUpRate   float64
	maxScaleDownRate float64

	// Tolerance thresholds
	upFluctuationTolerance   float64
	downFluctuationTolerance float64

	// Cooldown windows
	scaleUpCooldownWindow   time.Duration
	scaleDownCooldownWindow time.Duration

	// Time windows
	panicWindow  time.Duration
	stableWindow time.Duration

	// KPA configuration
	panicThreshold float64
	inPanicMode    bool

	// Scale-to-zero
	scaleToZero     bool
	activationScale int32

	// Per-metric targets
	metricTargets map[string]float64

	// GPU configuration
	gpuAware             bool
	gpuMemoryThreshold   int32
	gpuComputeThreshold  int32

	// Cost configuration
	costAware     bool
	budgetPerHour float64
	budgetPerDay  float64

	// Prediction configuration
	predictionEnabled bool
	lookAheadMinutes  int32
}

// NewBaseScalingContext creates a new ScalingContext with default values.
func NewBaseScalingContext() ScalingContext {
	return &baseScalingContext{
		minReplicas:              1,
		maxReplicas:              10,
		maxScaleUpRate:           DefaultMaxScaleUpRate,
		maxScaleDownRate:         DefaultMaxScaleDownRate,
		upFluctuationTolerance:   DefaultUpFluctuationTolerance,
		downFluctuationTolerance: DefaultDownFluctuationTolerance,
		scaleUpCooldownWindow:    DefaultScaleUpCooldownWindow,
		scaleDownCooldownWindow:  DefaultScaleDownCooldownWindow,
		panicWindow:              DefaultPanicWindow,
		stableWindow:             DefaultStableWindow,
		panicThreshold:           DefaultPanicThreshold,
		inPanicMode:              false,
		scaleToZero:              false,
		activationScale:          DefaultActivationScale,
		metricTargets:            make(map[string]float64),
		gpuAware:                 false,
		gpuMemoryThreshold:       80,
		gpuComputeThreshold:      80,
		costAware:                false,
		budgetPerHour:            0,
		budgetPerDay:             0,
		predictionEnabled:        false,
		lookAheadMinutes:         15,
	}
}

// NewScalingContextFromScaler creates a ScalingContext from a BudAIScaler.
func NewScalingContextFromScaler(scaler *scalerv1alpha1.BudAIScaler) ScalingContext {
	ctx := NewBaseScalingContext().(*baseScalingContext)

	// Set replica bounds
	ctx.minReplicas = scaler.Spec.GetMinReplicas()
	ctx.maxReplicas = scaler.Spec.MaxReplicas

	// Parse behavior settings
	if scaler.Spec.Behavior != nil {
		if scaler.Spec.Behavior.ScaleUp != nil {
			if scaler.Spec.Behavior.ScaleUp.StabilizationWindowSeconds != nil {
				ctx.scaleUpCooldownWindow = time.Duration(*scaler.Spec.Behavior.ScaleUp.StabilizationWindowSeconds) * time.Second
			}
			if scaler.Spec.Behavior.ScaleUp.MaxScaleRate != nil {
				if rate, err := strconv.ParseFloat(*scaler.Spec.Behavior.ScaleUp.MaxScaleRate, 64); err == nil {
					ctx.maxScaleUpRate = rate
				}
			}
			if scaler.Spec.Behavior.ScaleUp.Tolerance != nil {
				if tol, err := strconv.ParseFloat(*scaler.Spec.Behavior.ScaleUp.Tolerance, 64); err == nil {
					ctx.upFluctuationTolerance = tol
				}
			}
		}
		if scaler.Spec.Behavior.ScaleDown != nil {
			if scaler.Spec.Behavior.ScaleDown.StabilizationWindowSeconds != nil {
				ctx.scaleDownCooldownWindow = time.Duration(*scaler.Spec.Behavior.ScaleDown.StabilizationWindowSeconds) * time.Second
			}
			if scaler.Spec.Behavior.ScaleDown.MaxScaleRate != nil {
				if rate, err := strconv.ParseFloat(*scaler.Spec.Behavior.ScaleDown.MaxScaleRate, 64); err == nil {
					ctx.maxScaleDownRate = rate
				}
			}
			if scaler.Spec.Behavior.ScaleDown.Tolerance != nil {
				if tol, err := strconv.ParseFloat(*scaler.Spec.Behavior.ScaleDown.Tolerance, 64); err == nil {
					ctx.downFluctuationTolerance = tol
				}
			}
		}
	}

	// Parse GPU configuration
	if scaler.Spec.GPUConfig != nil && scaler.Spec.GPUConfig.Enabled {
		ctx.gpuAware = true
		if scaler.Spec.GPUConfig.GPUMemoryThreshold != nil {
			ctx.gpuMemoryThreshold = *scaler.Spec.GPUConfig.GPUMemoryThreshold
		}
		if scaler.Spec.GPUConfig.GPUComputeThreshold != nil {
			ctx.gpuComputeThreshold = *scaler.Spec.GPUConfig.GPUComputeThreshold
		}
	}

	// Parse cost configuration
	if scaler.Spec.CostConfig != nil && scaler.Spec.CostConfig.Enabled {
		ctx.costAware = true
		if scaler.Spec.CostConfig.BudgetPerHour != nil {
			ctx.budgetPerHour = scaler.Spec.CostConfig.BudgetPerHour.AsApproximateFloat64()
		}
		if scaler.Spec.CostConfig.BudgetPerDay != nil {
			ctx.budgetPerDay = scaler.Spec.CostConfig.BudgetPerDay.AsApproximateFloat64()
		}
	}

	// Parse prediction configuration
	if scaler.Spec.PredictionConfig != nil && scaler.Spec.PredictionConfig.Enabled {
		ctx.predictionEnabled = true
		if scaler.Spec.PredictionConfig.LookAheadMinutes != nil {
			ctx.lookAheadMinutes = *scaler.Spec.PredictionConfig.LookAheadMinutes
		}
	}

	// Parse per-metric target values
	for _, ms := range scaler.Spec.MetricsSources {
		if targetValue, err := strconv.ParseFloat(ms.TargetValue, 64); err == nil {
			ctx.metricTargets[ms.TargetMetric] = targetValue
		}
	}

	// Parse annotation overrides
	ctx.parseAnnotations(scaler.Annotations)

	return ctx
}

// parseAnnotations parses annotation overrides.
func (c *baseScalingContext) parseAnnotations(annotations map[string]string) {
	if annotations == nil {
		return
	}

	// Parse scaling rate annotations
	if v, ok := annotations[types.MaxScaleUpRateAnnotation]; ok {
		if rate, err := strconv.ParseFloat(v, 64); err == nil {
			c.maxScaleUpRate = rate
		}
	}
	if v, ok := annotations[types.MaxScaleDownRateAnnotation]; ok {
		if rate, err := strconv.ParseFloat(v, 64); err == nil {
			c.maxScaleDownRate = rate
		}
	}

	// Parse tolerance annotations
	if v, ok := annotations[types.ScaleUpToleranceAnnotation]; ok {
		if tol, err := strconv.ParseFloat(v, 64); err == nil {
			c.upFluctuationTolerance = tol
		}
	}
	if v, ok := annotations[types.ScaleDownToleranceAnnotation]; ok {
		if tol, err := strconv.ParseFloat(v, 64); err == nil {
			c.downFluctuationTolerance = tol
		}
	}

	// Parse cooldown annotations
	if v, ok := annotations[types.ScaleUpCooldownAnnotation]; ok {
		if dur, err := time.ParseDuration(v); err == nil {
			c.scaleUpCooldownWindow = dur
		}
	}
	if v, ok := annotations[types.ScaleDownCooldownAnnotation]; ok {
		if dur, err := time.ParseDuration(v); err == nil {
			c.scaleDownCooldownWindow = dur
		}
	}

	// Parse KPA annotations
	if v, ok := annotations[types.PanicThresholdAnnotation]; ok {
		if threshold, err := strconv.ParseFloat(v, 64); err == nil {
			c.panicThreshold = threshold
		}
	}
	if v, ok := annotations[types.PanicWindowAnnotation]; ok {
		if dur, err := time.ParseDuration(v); err == nil {
			c.panicWindow = dur
		}
	}
	if v, ok := annotations[types.StableWindowAnnotation]; ok {
		if dur, err := time.ParseDuration(v); err == nil {
			c.stableWindow = dur
		}
	}

	// Parse scale-to-zero annotations
	if v, ok := annotations[types.ScaleToZeroAnnotation]; ok {
		c.scaleToZero = v == "true"
	}
	if v, ok := annotations[types.ActivationScaleAnnotation]; ok {
		if scale, err := strconv.ParseInt(v, 10, 32); err == nil {
			c.activationScale = int32(scale)
		}
	}
}

// Replica bounds implementation
func (c *baseScalingContext) GetMinReplicas() int32       { return c.minReplicas }
func (c *baseScalingContext) GetMaxReplicas() int32       { return c.maxReplicas }
func (c *baseScalingContext) SetMinReplicas(replicas int32) { c.minReplicas = replicas }
func (c *baseScalingContext) SetMaxReplicas(replicas int32) { c.maxReplicas = replicas }

// Scaling rates implementation
func (c *baseScalingContext) GetMaxScaleUpRate() float64   { return c.maxScaleUpRate }
func (c *baseScalingContext) GetMaxScaleDownRate() float64 { return c.maxScaleDownRate }

// Tolerance implementation
func (c *baseScalingContext) GetUpFluctuationTolerance() float64   { return c.upFluctuationTolerance }
func (c *baseScalingContext) GetDownFluctuationTolerance() float64 { return c.downFluctuationTolerance }

// Cooldown windows implementation
func (c *baseScalingContext) GetScaleUpCooldownWindow() time.Duration   { return c.scaleUpCooldownWindow }
func (c *baseScalingContext) GetScaleDownCooldownWindow() time.Duration { return c.scaleDownCooldownWindow }

// Time windows implementation
func (c *baseScalingContext) GetPanicWindow() time.Duration  { return c.panicWindow }
func (c *baseScalingContext) GetStableWindow() time.Duration { return c.stableWindow }

// KPA configuration implementation
func (c *baseScalingContext) GetPanicThreshold() float64 { return c.panicThreshold }
func (c *baseScalingContext) GetInPanicMode() bool       { return c.inPanicMode }
func (c *baseScalingContext) SetInPanicMode(inPanic bool) { c.inPanicMode = inPanic }

// Scale-to-zero implementation
func (c *baseScalingContext) GetScaleToZero() bool      { return c.scaleToZero }
func (c *baseScalingContext) GetActivationScale() int32 { return c.activationScale }

// Per-metric targets implementation
func (c *baseScalingContext) GetTargetValueForMetric(metricName string) (float64, bool) {
	value, ok := c.metricTargets[metricName]
	return value, ok
}
func (c *baseScalingContext) SetTargetValueForMetric(metricName string, value float64) {
	c.metricTargets[metricName] = value
}

// GPU configuration implementation
func (c *baseScalingContext) IsGPUAware() bool             { return c.gpuAware }
func (c *baseScalingContext) GetGPUMemoryThreshold() int32 { return c.gpuMemoryThreshold }
func (c *baseScalingContext) GetGPUComputeThreshold() int32 { return c.gpuComputeThreshold }

// Cost configuration implementation
func (c *baseScalingContext) IsCostAware() bool       { return c.costAware }
func (c *baseScalingContext) GetBudgetPerHour() float64 { return c.budgetPerHour }
func (c *baseScalingContext) GetBudgetPerDay() float64  { return c.budgetPerDay }

// Prediction configuration implementation
func (c *baseScalingContext) IsPredictionEnabled() bool { return c.predictionEnabled }
func (c *baseScalingContext) GetLookAheadMinutes() int32 { return c.lookAheadMinutes }

// ScalingContextProvider compatible methods implementation
func (c *baseScalingContext) GetScaleUpRate() float64   { return c.maxScaleUpRate }
func (c *baseScalingContext) GetScaleDownRate() float64 { return c.maxScaleDownRate }
func (c *baseScalingContext) GetTolerance() float64     { return c.upFluctuationTolerance }
func (c *baseScalingContext) GetPanicWindowSeconds() int32 {
	return int32(c.panicWindow.Seconds())
}
func (c *baseScalingContext) GetStableWindowSeconds() int32 {
	return int32(c.stableWindow.Seconds())
}
func (c *baseScalingContext) GetScaleUpStabilizationSeconds() int32 {
	return int32(c.scaleUpCooldownWindow.Seconds())
}
func (c *baseScalingContext) GetScaleDownStabilizationSeconds() int32 {
	return int32(c.scaleDownCooldownWindow.Seconds())
}
