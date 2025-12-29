# Prediction Algorithm

BudAIScaler uses a multi-layered prediction system that combines time-series analysis, seasonal pattern learning, and workload pattern detection to make proactive scaling decisions.

## Overview

The prediction system operates at three levels:

1. **Time-Series Prediction** - Linear regression on recent metric history
2. **Seasonal Learning** - EWMA-based hourly/daily pattern learning
3. **Pattern Detection** - Workload-specific pattern recognition

```
┌─────────────────────────────────────────────────────────────────┐
│                    Prediction Pipeline                          │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  Metrics Sources                                                │
│       │                                                         │
│       ▼                                                         │
│  ┌──────────────────┐                                          │
│  │  Metric Collector │ ─────► Collects from pods/external      │
│  └────────┬─────────┘                                          │
│           │                                                     │
│           ▼                                                     │
│  ┌──────────────────┐    ┌────────────────────┐                │
│  │ Time-Series      │    │  Seasonal Profile   │                │
│  │ Predictor        │    │  Manager            │                │
│  │                  │    │                     │                │
│  │ • Linear Regression│  │ • 168 time buckets  │                │
│  │ • Moving Average │    │ • EWMA smoothing    │                │
│  │ • Confidence calc│    │ • Factor prediction │                │
│  └────────┬─────────┘    └──────────┬─────────┘                │
│           │                         │                           │
│           ▼                         ▼                           │
│  ┌──────────────────────────────────────────────┐              │
│  │           Pattern Detector                    │              │
│  │                                               │              │
│  │  • KV Cache Pressure  • Batch Ingestion      │              │
│  │  • Traffic Spike/Drain • Cold Start          │              │
│  └───────────────────────┬──────────────────────┘              │
│                          │                                      │
│                          ▼                                      │
│  ┌──────────────────────────────────────────────┐              │
│  │        Final Prediction Output                │              │
│  │                                               │              │
│  │  • Predicted Replicas                         │              │
│  │  • Confidence Score (0-1)                     │              │
│  │  • Pattern Adjustment Factor                  │              │
│  └──────────────────────────────────────────────┘              │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

## Configuration

### Specifying Prediction Metrics

The prediction system is fully generic - you can use any metrics from your `metricsSources`:

```yaml
apiVersion: scaler.bud.studio/v1alpha1
kind: BudAIScaler
spec:
  metricsSources:
    - targetMetric: "app:cache_usage"
      targetValue: "70"
    - targetMetric: "app:queue_depth"
      targetValue: "50"
    - targetMetric: "app:response_time_ms"
      targetValue: "200"

  predictionConfig:
    enabled: true
    lookAheadMinutes: 15

    # Which metrics to use for time-series prediction
    # These metrics drive the linear regression model
    predictionMetrics:
      - "app:cache_usage"

    # Which metrics to track for seasonal learning
    # These metrics build up hourly/daily patterns over time
    seasonalMetrics:
      - "app:cache_usage"
      - "app:queue_depth"
```

### Default Behavior

| Field | Default |
|-------|---------|
| `predictionMetrics` | First metric in `metricsSources` |
| `seasonalMetrics` | Same as `predictionMetrics` |

## Time-Series Prediction

### Linear Regression

The predictor uses least-squares linear regression on recent metric history to forecast future values:

```
predicted_value = intercept + slope × future_time
```

Where:
- `slope = (n × Σxy - Σx × Σy) / (n × Σx² - (Σx)²)`
- `intercept = (Σy - slope × Σx) / n`
- `future_time = current_time + lookAheadMinutes`

### Replica Calculation

Once the predicted metric value is known:

```
ratio = predicted_value / target_value
desired_replicas = ceil(current_replicas × ratio)
```

### Confidence Score

Confidence is calculated based on:

1. **Data point count** - More history = higher confidence
2. **Variance** - Lower variance = higher confidence

```
count_factor = 1 - 1/(1 + history_length/10)
cv = std_dev / mean  # Coefficient of variation
variance_factor = 1 / (1 + cv)
confidence = count_factor × variance_factor
```

## Seasonal Learning

### Time Buckets

The seasonal profile tracks 168 buckets (24 hours × 7 days) to capture weekly patterns:

```
bucket_index = day_of_week × 24 + hour_of_day

Example:
  Monday 9 AM = 0 × 24 + 9 = bucket 9
  Friday 5 PM = 4 × 24 + 17 = bucket 113
  Sunday 2 AM = 6 × 24 + 2 = bucket 146
```

### EWMA Smoothing

Each bucket maintains an exponentially weighted moving average:

```
new_mean = α × new_value + (1 - α) × old_mean
```

Where `α` (alpha) is the smoothing factor:
- Default: 0.2
- Higher α = more responsive to recent data
- Lower α = more stable, slower to adapt

### Seasonal Factor

The seasonal factor indicates how much the current time bucket deviates from the global average:

```
factor = bucket_mean / global_mean
```

Example:
- Factor 1.5 at Monday 9 AM → expect 50% higher load than average
- Factor 0.7 at Sunday 3 AM → expect 30% lower load than average

## Pattern Detection

The system detects several workload patterns specific to GenAI/LLM workloads:

### KV Cache Pressure

**Trigger conditions:**
- GPU cache usage > 85% AND requests waiting > 5
- OR cache usage rapidly increasing toward 70%+

**Response:**
- Scale factor: 1.5× (default)
- Priority: Highest

### Batch Ingestion

**Trigger conditions:**
- Current requests > 2× baseline (5-10 min ago)
- Sudden spike pattern detected

**Response:**
- Scale factor: 2.0× (default)
- Priority: High

### Traffic Spike/Drain

**Trigger conditions:**
- Trend analysis shows >50% increase (spike) or decrease (drain)

**Response:**
- Spike: Scale factor 1.5×
- Drain: Scale factor 0.8×

### Cold Start

**Trigger conditions:**
- Low cache usage (<20%)
- Low request count (<10)
- Increasing trend

**Response:**
- Scale factor: 1.2× (preemptive)

## Learning and Calibration

### Prediction Verification

The system records predictions and verifies them after the look-ahead period:

```
T=0:    Record prediction (P=5 replicas in 15 min)
T=15:   Verify actual state (A=4 replicas)
        Error = (P - A) / A = 25%
```

### Accuracy Tracking

The system tracks:
- **MAE** - Mean Absolute Error
- **MAPE** - Mean Absolute Percentage Error
- **Direction Accuracy** - Did we predict scale up/down correctly?

### Self-Calibration

When accuracy drops, the system automatically:
1. Adjusts EWMA alpha for seasonal profiles
2. Resets poorly-performing time buckets
3. Updates pattern detection thresholds

## Integration with Scaling Algorithm

Prediction integrates with the BudScaler algorithm through weighted blending:

```go
// Only if confidence >= 0.7
weight = 0.3 × confidence
adjusted = (metric_based × (1 - weight)) + (prediction × weight)
```

The prediction can influence the final decision by up to 30%, proportional to its confidence level.

## Schedule Hints vs Prediction

Schedule hints and predictions serve different purposes:

| Aspect | Schedule Hints | Prediction |
|--------|---------------|------------|
| Source | User-defined cron schedules | Learned from metrics |
| Priority | Acts as a floor | Weighted input |
| Certainty | 100% confidence | Variable confidence |
| Use case | Known traffic patterns | Unknown/varying patterns |

When both are active:
1. Schedule hint sets a minimum floor
2. Prediction can only increase above the floor
3. Cost constraints can still cap the maximum

## Best Practices

### Choosing Prediction Metrics

Select metrics that:
- Correlate with scaling needs
- Change before resource pressure (leading indicators)
- Have relatively stable patterns

**Good choices:**
- Cache/memory utilization
- Queue depth
- Request latency percentiles

**Avoid:**
- Highly volatile metrics without patterns
- Metrics with lots of noise

### Seasonal Learning Timeline

| Duration | Expected Accuracy |
|----------|------------------|
| < 1 day | Low - insufficient data |
| 1-3 days | Moderate - some patterns |
| 1 week | Good - full weekly cycle |
| 2+ weeks | Best - refined patterns |

### Tuning Parameters

```yaml
predictionConfig:
  # Longer look-ahead = more time to scale, but less accurate
  lookAheadMinutes: 15  # 5-30 minutes typical

  # More history = better patterns, more storage
  historicalDataDays: 7  # 3-14 days typical

  # Higher confidence = only act on strong signals
  minConfidence: 0.7    # 0.5-0.9 typical
```

## Monitoring

Check prediction status via the BudAIScaler status:

```bash
kubectl get budaiscaler my-scaler -o jsonpath='{.status.learningStatus}'
```

Key fields:
- `dataPointsCollected` - Total learning data points
- `overallAccuracy` - Direction prediction accuracy (%)
- `overallMAPE` - Mean Absolute Percentage Error
- `currentPattern` - Detected workload pattern
- `patternConfidence` - Pattern detection confidence
- `calibrationNeeded` - Whether recalibration is needed
