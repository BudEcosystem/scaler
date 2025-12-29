# Scaling Decision Hierarchy

BudAIScaler uses a layered approach to scaling decisions. This document explains the priority order of different inputs and how they interact.

## Priority Order

The scaling algorithm processes inputs in a specific order, with later stages able to override or constrain earlier ones:

```
Priority (Highest to Lowest)
─────────────────────────────────────────────────

1. COST CONSTRAINTS        ← Hard ceiling (always wins)
   └── Budget limits cap maximum replicas

2. MAX REPLICAS            ← Hard ceiling
   └── Never exceed configured maximum

3. SCHEDULE HINTS          ← Dynamic floor (when active)
   └── Minimum replicas during scheduled periods

4. MIN REPLICAS            ← Static floor
   └── Never go below configured minimum

5. SCALING INPUTS          ← Calculate desired
   ├── Metrics (primary)
   ├── GPU utilization
   └── Predictions

─────────────────────────────────────────────────
```

## Decision Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                    Scaling Decision Pipeline                     │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  Step 1: Calculate Metric-Based Recommendation                  │
│  ┌─────────────────────────────────────────────┐               │
│  │  For each metric source:                     │               │
│  │    ratio = current_value / target_value      │               │
│  │    desired = current × (1 + α×(ratio-1))     │               │
│  │  Take maximum across all metrics             │               │
│  └─────────────────────┬───────────────────────┘               │
│                        │                                        │
│                        ▼                                        │
│  Step 2: Apply GPU Adjustment (can only increase)               │
│  ┌─────────────────────────────────────────────┐               │
│  │  If GPU enabled and metrics available:       │               │
│  │    gpu_ratio = max(mem_ratio, compute_ratio) │               │
│  │    gpu_desired = current × gpu_ratio         │               │
│  │    desired = max(metric_desired, gpu_desired)│               │
│  └─────────────────────┬───────────────────────┘               │
│                        │                                        │
│                        ▼                                        │
│  Step 3: Apply Prediction Adjustment                            │
│  ┌─────────────────────────────────────────────┐               │
│  │  If prediction enabled AND confidence ≥ 0.7: │               │
│  │    weight = 0.3 × confidence                 │               │
│  │    desired = blend(desired, predicted)       │               │
│  └─────────────────────┬───────────────────────┘               │
│                        │                                        │
│                        ▼                                        │
│  Step 4: Apply Schedule Hint Floor (can only increase)          │
│  ┌─────────────────────────────────────────────┐               │
│  │  If schedule hint active:                    │               │
│  │    desired = max(desired, hint_replicas)     │               │
│  └─────────────────────┬───────────────────────┘               │
│                        │                                        │
│                        ▼                                        │
│  Step 5: Apply Cost Ceiling (can only decrease)                 │
│  ┌─────────────────────────────────────────────┐               │
│  │  If cost config enabled:                     │               │
│  │    max_affordable = budget / cost_per_replica│               │
│  │    desired = min(desired, max_affordable)    │               │
│  └─────────────────────┬───────────────────────┘               │
│                        │                                        │
│                        ▼                                        │
│  Step 6: Apply Min/Max Constraints                              │
│  ┌─────────────────────────────────────────────┐               │
│  │  desired = max(min_replicas, desired)        │               │
│  │  desired = min(max_replicas, desired)        │               │
│  └─────────────────────┬───────────────────────┘               │
│                        │                                        │
│                        ▼                                        │
│  Step 7: Apply Stabilization Windows                            │
│  ┌─────────────────────────────────────────────┐               │
│  │  If scale up and within stabilization:       │               │
│  │    desired = current (no change)             │               │
│  │  If scale down and within stabilization:     │               │
│  │    desired = current (no change)             │               │
│  └─────────────────────┬───────────────────────┘               │
│                        │                                        │
│                        ▼                                        │
│                  Final Recommendation                           │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

## Metrics Hierarchy

### Multi-Metric Scaling

When multiple metrics sources are configured, the scaler takes the **maximum** desired replicas:

```yaml
spec:
  metricsSources:
    - targetMetric: "cpu_usage"
      targetValue: "70"        # → desires 3 replicas
    - targetMetric: "memory_usage"
      targetValue: "80"        # → desires 5 replicas
    - targetMetric: "queue_depth"
      targetValue: "10"        # → desires 4 replicas

# Result: 5 replicas (maximum of all)
```

**Rationale:** Any metric exceeding its target indicates resource pressure. Scaling to satisfy the most demanding metric ensures all metrics stay within acceptable ranges.

### Metric Weights

Currently, all metrics have equal weight. The maximum-based approach means any single metric can drive scale-up decisions, while scale-down only happens when ALL metrics indicate lower load.

## GPU Metrics

GPU metrics operate as a **secondary scaling input** that can only increase the replica count:

```
gpu_memory_ratio = gpu_memory_util / gpu_memory_threshold
gpu_compute_ratio = gpu_compute_util / gpu_compute_threshold
gpu_ratio = max(gpu_memory_ratio, gpu_compute_ratio)

final_desired = max(metric_desired, current × gpu_ratio)
```

### Why GPU Can Only Increase

GPU pressure indicates immediate resource contention that cannot be ignored. If GPU memory is at 95% but CPU metric suggests scaling down, the GPU constraint takes precedence to prevent OOM or performance degradation.

## Schedule Hints

Schedule hints act as a **dynamic floor** during their active period:

```yaml
spec:
  scheduleHints:
    - name: business-hours
      cronExpression: "0 9 * * 1-5"  # 9 AM weekdays
      duration: 10h                   # Until 7 PM
      targetReplicas: 5
```

### Floor Behavior

```
During business hours (9 AM - 7 PM weekdays):
  Metric-based desired: 3 → Final: 5 (floor applies)
  Metric-based desired: 8 → Final: 8 (above floor)

Outside business hours:
  Metric-based desired: 3 → Final: 3 (no floor)
```

### Multiple Overlapping Hints

If multiple schedule hints are active, only the first matching hint applies. Order your hints from most specific to least specific.

## Cost Constraints

Cost constraints act as a **hard ceiling** that always wins:

```yaml
spec:
  costConfig:
    enabled: true
    budgetPerHour: "10.00"  # $10/hour maximum
```

### Cost Calculation

```
current_cost = current_replicas × cost_per_replica
max_replicas = budget_per_hour / cost_per_replica

If desired > max_replicas:
  final = max_replicas  # Budget ceiling
```

### Cost vs Schedule Hint Conflict

If cost constraints conflict with schedule hints:

```
Schedule hint floor: 10 replicas
Cost ceiling: 5 replicas

Result: 5 replicas (cost always wins)
```

**Warning:** This scenario should be avoided through proper configuration. If your budget cannot support your schedule hints, either increase the budget or reduce hint targets.

## Prediction Integration

Predictions are integrated as a weighted input, not an override:

```
If confidence ≥ 0.7:
  weight = 0.3 × confidence  # Max 30% influence
  adjusted = (metric_based × (1-weight)) + (predicted × weight)
```

### Prediction Confidence Thresholds

| Confidence | Behavior |
|------------|----------|
| < 0.7 | Prediction ignored |
| 0.7 - 0.85 | Low weight (21-25%) |
| 0.85 - 1.0 | Higher weight (25-30%) |

## Stabilization Windows

Stabilization prevents rapid oscillation by enforcing minimum time between scaling events:

```yaml
spec:
  behavior:
    scaleUp:
      stabilizationWindowSeconds: 30    # Wait 30s before scale up
    scaleDown:
      stabilizationWindowSeconds: 300   # Wait 5 min before scale down
```

### Asymmetric Windows

Scale-down typically has a longer window than scale-up because:
- Scale-up: Missing capacity causes immediate degradation
- Scale-down: Extra capacity is wasteful but not harmful

## Example Scenarios

### Scenario 1: Normal Load Increase

```
Current: 2 replicas
Metrics suggest: 4 replicas
GPU: Not configured
Schedule: Not active
Cost: Within budget

Final: 4 replicas
```

### Scenario 2: GPU Pressure

```
Current: 2 replicas
Metrics suggest: 2 replicas (load is fine)
GPU memory: 90% (threshold 70%)
GPU ratio: 90/70 = 1.29

GPU desired: 2 × 1.29 = 3 replicas
Final: 3 replicas (GPU overrides metrics)
```

### Scenario 3: Schedule Hint Floor

```
Current: 2 replicas
Metrics suggest: 3 replicas
Schedule hint active: 5 replicas minimum

Final: 5 replicas (floor applied)
```

### Scenario 4: Cost Ceiling

```
Current: 5 replicas
Metrics suggest: 10 replicas
Budget: $10/hour
Cost per replica: $2/hour
Max affordable: 10 / 2 = 5 replicas

Final: 5 replicas (cost ceiling)
```

### Scenario 5: All Constraints Active

```
Min: 2, Max: 20
Schedule hint: 5 (active)
Cost ceiling: 8 replicas
Metrics + GPU suggest: 10 replicas

Step 1: Metrics/GPU → 10
Step 2: Schedule floor → max(10, 5) = 10
Step 3: Cost ceiling → min(10, 8) = 8
Step 4: Min/Max → clamp(8, 2, 20) = 8

Final: 8 replicas
```

## Debugging Decisions

The scaler logs detailed decision reasoning:

```bash
kubectl logs -n scaler-system deployment/scaler-controller | grep BudScaler
```

Example output:
```
BudScaler: Scaling up from 2 to 4 replicas [schedule hint 'business-hours' floor: 5] (GPU mem: 75.0%, compute: 60.0%) (budget usage: 40.0%)
```

### Status Fields

Check the BudAIScaler status for decision context:

```bash
kubectl get budaiscaler my-scaler -o yaml
```

Key status fields:
- `currentReplicas` - Actual replica count
- `desiredReplicas` - Calculated desired count
- `scalingDecision` - Last decision reason
- `activeScheduleHint` - Currently active schedule hint (if any)
- `predictedReplicas` - Prediction-based replica count
- `learningStatus` - Learning system state
