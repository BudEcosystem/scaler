# Scaler

[![Go Version](https://img.shields.io/github/go-mod/go-version/BudEcosystem/scaler)](https://go.dev/)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

**BudAIScaler** is a Kubernetes autoscaler designed for GenAI workloads. It provides intelligent scaling with GPU awareness, cost optimization, adaptive learning, and predictive capabilities.

## Features

### Scaling Strategies

| Strategy | Description | Best For |
|----------|-------------|----------|
| **HPA** | Kubernetes HorizontalPodAutoscaler wrapper | Standard workloads, native K8s integration |
| **KPA** | KNative-style with panic/stable windows | Bursty traffic, rapid scale-up needs |
| **BudScaler** | Custom GenAI-optimized algorithm | LLM inference, GPU workloads |

### Metrics Sources

- **Pod Metrics** - HTTP endpoints on pods (e.g., `/metrics`)
- **Resource Metrics** - CPU/Memory via Kubernetes Metrics API
- **Custom Metrics** - Kubernetes Custom Metrics API
- **External Metrics** - External HTTP endpoints
- **Prometheus** - Direct PromQL queries
- **Inference Engines** - Native support for vLLM, TGI, SGLang, Triton

### Adaptive Learning System

The BudAIScaler includes an adaptive learning system that improves scaling decisions over time:

- **Pattern Detection** - Automatically detects workload patterns (KV cache pressure, batch spikes, traffic drains)
- **Seasonal Learning** - Learns time-of-day and day-of-week traffic patterns
- **Prediction Accuracy Tracking** - Monitors MAE, MAPE, and direction accuracy
- **Self-Calibration** - Automatically adjusts model parameters based on prediction errors
- **Persistent Learning** - Stores learned data in ConfigMaps for recovery across restarts

### Schedule Hints

Define known traffic patterns for deterministic schedule-based scaling:

```yaml
spec:
  scheduleHints:
    - name: morning-rush
      cronExpression: "0 9 * * 1-5"
      targetReplicas: 5
      duration: 2h
    - name: batch-processing
      cronExpression: "0 2 * * *"
      targetReplicas: 10
      duration: 1h
```

### GPU-Aware Scaling

Scale based on GPU memory and compute utilization:

```yaml
spec:
  gpuConfig:
    enabled: true
    gpuMemoryThreshold: 80
    gpuComputeThreshold: 70
    metricsSource: dcgm  # dcgm, nvml, or prometheus
    dcgmEndpoint: "http://dcgm-exporter:9400/metrics"
```

### Cost-Aware Scaling

Optimize scaling decisions based on cost constraints:

```yaml
spec:
  costConfig:
    enabled: true
    cloudProvider: aws
    budgetPerHour: "10.00"
    budgetPerDay: "200.00"
    preferSpotInstances: true
    spotFallbackToOnDemand: true
```

### Predictive Scaling

ML-based prediction for proactive scaling:

```yaml
spec:
  metricsSources:
    - targetMetric: "app:cache_usage"
      targetValue: "70"
    - targetMetric: "app:queue_depth"
      targetValue: "50"
  predictionConfig:
    enabled: true
    lookAheadMinutes: 15
    historicalDataDays: 7
    enableLearning: true
    minConfidence: 0.7
    # Specify which metrics to use for prediction (optional)
    predictionMetrics:
      - "app:cache_usage"
    # Specify which metrics to track for seasonal patterns (optional)
    seasonalMetrics:
      - "app:cache_usage"
      - "app:queue_depth"
```

The prediction system is fully generic - you can use any metrics from your `metricsSources`. See [Prediction Algorithm](docs/prediction-algorithm.md) for details.

## Quick Start

### Prerequisites

- Kubernetes 1.26+
- Helm 3.x (for Helm installation)
- kubectl configured to access your cluster

### Installation

#### Using Helm (Recommended)

```bash
# Install from OCI registry
helm install scaler oci://ghcr.io/budecosystem/charts/scaler \
  --namespace scaler-system \
  --create-namespace

# Verify installation
kubectl get pods -n scaler-system
kubectl get crd budaiscalers.scaler.bud.studio
```

#### Using Kustomize

```bash
# Install CRDs
kubectl apply -k config/crd

# Install controller
kubectl apply -k config/default
```

#### From Source

```bash
# Clone the repository
git clone https://github.com/BudEcosystem/scaler.git
cd scaler

# Install using local chart
helm install scaler charts/scaler \
  --namespace scaler-system \
  --create-namespace
```

### Create a BudAIScaler

#### Basic Example

```yaml
apiVersion: scaler.bud.studio/v1alpha1
kind: BudAIScaler
metadata:
  name: my-app-scaler
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: my-app
  minReplicas: 1
  maxReplicas: 10
  scalingStrategy: BudScaler
  metricsSources:
    - metricSourceType: inferenceEngine
      targetMetric: gpu_cache_usage_perc
      targetValue: "70"
      inferenceEngineConfig:
        engineType: vllm
        metricsPort: 8000
```

#### Full-Featured Example

```yaml
apiVersion: scaler.bud.studio/v1alpha1
kind: BudAIScaler
metadata:
  name: llm-scaler
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: llm-inference
  minReplicas: 2
  maxReplicas: 20
  scalingStrategy: BudScaler

  metricsSources:
    - metricSourceType: inferenceEngine
      targetMetric: gpu_cache_usage_perc
      targetValue: "70"
      inferenceEngineConfig:
        engineType: vllm
        metricsPort: 8000

  # Schedule-based scaling hints
  scheduleHints:
    - name: business-hours
      cronExpression: "0 9 * * 1-5"
      targetReplicas: 8
      duration: 10h

  # Adaptive learning and prediction
  predictionConfig:
    enabled: true
    lookAheadMinutes: 15
    enableLearning: true
    minConfidence: 0.7

  # GPU awareness
  gpuConfig:
    enabled: true
    gpuMemoryThreshold: 80

  # Cost constraints
  costConfig:
    enabled: true
    budgetPerHour: "50.00"
    preferSpotInstances: true

  # Scaling behavior
  behavior:
    scaleUp:
      stabilizationWindowSeconds: 30
      policies:
        - type: Percent
          value: 100
          periodSeconds: 60
    scaleDown:
      stabilizationWindowSeconds: 300
      policies:
        - type: Percent
          value: 50
          periodSeconds: 60
```

## Configuration

### Scaling Strategies

#### HPA Strategy

Delegates scaling to a native Kubernetes HPA:

```yaml
spec:
  scalingStrategy: HPA
  metricsSources:
    - metricSourceType: resource
      targetMetric: cpu
      targetValue: "80"
```

#### KPA Strategy

KNative-style scaling with panic and stable windows for rapid response:

```yaml
spec:
  scalingStrategy: KPA
  metricsSources:
    - metricSourceType: pod
      targetMetric: requests_per_second
      targetValue: "100"
      port: "8080"
      path: "/metrics"
  behavior:
    scaleUp:
      stabilizationWindowSeconds: 0
    scaleDown:
      stabilizationWindowSeconds: 300
```

#### BudScaler Strategy

Optimized for GenAI workloads with GPU and inference engine awareness:

```yaml
spec:
  scalingStrategy: BudScaler
  metricsSources:
    - metricSourceType: inferenceEngine
      targetMetric: gpu_cache_usage_perc
      targetValue: "70"
      inferenceEngineConfig:
        engineType: vllm
        metricsPort: 8000
```

### Inference Engine Metrics

#### vLLM

```yaml
metricsSources:
  - metricSourceType: inferenceEngine
    targetMetric: gpu_cache_usage_perc  # or: num_requests_waiting, num_requests_running
    targetValue: "70"
    inferenceEngineConfig:
      engineType: vllm
      metricsPort: 8000
      metricsPath: "/metrics"
```

#### Text Generation Inference (TGI)

```yaml
metricsSources:
  - metricSourceType: inferenceEngine
    targetMetric: queue_size
    targetValue: "10"
    inferenceEngineConfig:
      engineType: tgi
      metricsPort: 8080
```

### Adaptive Learning Configuration

```yaml
spec:
  metricsSources:
    - targetMetric: "vllm:gpu_cache_usage_perc"
      targetValue: "70"
    - targetMetric: "vllm:num_requests_waiting"
      targetValue: "10"
  predictionConfig:
    enabled: true
    enableLearning: true
    lookAheadMinutes: 15
    historicalDataDays: 7
    minConfidence: 0.7
    maxPredictedReplicas: 20
    # Use cache usage for time-series prediction
    predictionMetrics:
      - "vllm:gpu_cache_usage_perc"
    # Track seasonal patterns for both metrics
    seasonalMetrics:
      - "vllm:gpu_cache_usage_perc"
      - "vllm:num_requests_waiting"
```

The learning system tracks:

| Metric | Description |
|--------|-------------|
| `dataPointsCollected` | Total data points for learning |
| `detectedPattern` | Current workload pattern (normal, kv_pressure, batch_spike) |
| `patternConfidence` | Confidence in detected pattern (0-1) |
| `directionAccuracy` | Accuracy of scale up/down predictions |
| `overallAccuracy` | Overall prediction accuracy |
| `recentMAE` | Mean Absolute Error of recent predictions |
| `recentMAPE` | Mean Absolute Percentage Error |

### Prometheus Metrics

```yaml
metricsSources:
  - metricSourceType: prometheus
    targetMetric: custom_metric
    targetValue: "100"
    endpoint: "http://prometheus.monitoring:9090"
    promQL: "sum(rate(http_requests_total{app='myapp'}[5m]))"
```

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                      BudAIScaler Controller                          │
├─────────────────────────────────────────────────────────────────────┤
│  ┌─────────────┐  ┌─────────────┐  ┌───────────────────────────────┐│
│  │   Metrics   │  │  Algorithms │  │      Scaling Context          ││
│  │  Collector  │  │             │  │  (Single Source of Truth)     ││
│  │             │  │  - HPA      │  │                               ││
│  │  - Pod      │  │  - KPA      │  │  - Replica bounds             ││
│  │  - Resource │  │  - BudScaler│  │  - Scaling rates              ││
│  │  - Prometheus│ │             │  │  - Time windows               ││
│  │  - External │  │             │  │  - GPU/Cost/Prediction config ││
│  │  - vLLM/TGI │  │             │  │                               ││
│  └─────────────┘  └─────────────┘  └───────────────────────────────┘│
├─────────────────────────────────────────────────────────────────────┤
│  ┌─────────────────────────────────────────────────────────────────┐│
│  │                    Adaptive Learning System                      ││
│  │  ┌──────────────┐ ┌──────────────┐ ┌──────────────────────────┐ ││
│  │  │   Pattern    │ │  Prediction  │ │     Calibration          │ ││
│  │  │  Detection   │ │   Accuracy   │ │     System               │ ││
│  │  │              │ │   Tracker    │ │                          │ ││
│  │  │ - KV Cache   │ │ - MAE/MAPE   │ │ - Auto-adjusts params    │ ││
│  │  │ - Batch Spike│ │ - Direction  │ │ - Improves over time     │ ││
│  │  │ - Seasonal   │ │ - Overall    │ │ - ConfigMap persistence  │ ││
│  │  └──────────────┘ └──────────────┘ └──────────────────────────┘ ││
│  └─────────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────────┘
                              │
                              ▼
              ┌───────────────────────────────┐
              │     Target Workload           │
              │  (Deployment/StatefulSet)     │
              └───────────────────────────────┘
```

## Documentation

For detailed documentation, see the [docs](docs/) directory:

- [Prediction Algorithm](docs/prediction-algorithm.md) - How predictive scaling works
- [Scaling Decision Hierarchy](docs/scaling-hierarchy.md) - Priority order of scaling inputs

## Examples

See the [config/samples](config/samples/) directory for complete examples:

- `scaler_v1alpha1_budaiscaler_hpa.yaml` - HPA strategy
- `scaler_v1alpha1_budaiscaler_kpa.yaml` - KPA strategy
- `scaler_v1alpha1_budaiscaler_vllm.yaml` - vLLM scaling
- `scaler_v1alpha1_budaiscaler_tgi.yaml` - TGI scaling
- `scaler_v1alpha1_budaiscaler_full.yaml` - Full configuration with all features

## E2E Testing

End-to-end tests are available in the `e2e/` directory:

```bash
cd e2e/scripts

# Run scaling test (~3 min)
./scaling-test.sh run

# Run learning system test (~5 min)
./learning-test.sh run

# Run prediction accuracy test (~8 min)
./accuracy-test.sh run --quick
```

See [e2e/README.md](e2e/README.md) for detailed test documentation.

## Helm Chart Configuration

**Chart Location:** `oci://ghcr.io/budecosystem/charts/scaler`

```bash
# Install specific version
helm install scaler oci://ghcr.io/budecosystem/charts/scaler --version 0.1.0

# Upgrade
helm upgrade scaler oci://ghcr.io/budecosystem/charts/scaler

# Uninstall
helm uninstall scaler -n scaler-system
```

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Controller replicas | `1` |
| `image.repository` | Controller image | `ghcr.io/budecosystem/scaler` |
| `image.tag` | Image tag | Chart appVersion |
| `resources.limits.cpu` | CPU limit | `500m` |
| `resources.limits.memory` | Memory limit | `256Mi` |
| `metrics.enabled` | Enable Prometheus metrics | `true` |
| `gpu.enabled` | Enable GPU metrics | `false` |
| `cost.enabled` | Enable cost tracking | `false` |
| `crds.install` | Install CRDs | `true` |

See [charts/scaler/values.yaml](charts/scaler/values.yaml) for all options.

## Development

### Prerequisites

- Go 1.22+
- Docker
- kubectl
- Kind or Minikube (for local testing)

### Build

```bash
# Build the controller
make build

# Build Docker image
make docker-build

# Run tests
make test
```

### Run Locally

```bash
# Install CRDs
make install

# Run controller locally
make run
```

## Roadmap

- [x] **Phase 1: Core Autoscaling**
  - [x] HPA strategy (K8s HPA wrapper)
  - [x] KPA strategy (panic/stable windows)
  - [x] BudScaler strategy (GenAI-optimized)
  - [x] Metric aggregation with time windows
  - [x] ScalingContext as single source of truth
- [x] **Phase 2: Extended Metrics**
  - [x] Pod HTTP endpoint metrics
  - [x] Kubernetes resource metrics (CPU/memory)
  - [x] Prometheus/PromQL integration
  - [x] vLLM metrics support
  - [x] TGI metrics support
- [x] **Phase 3: Adaptive Learning**
  - [x] Pattern detection (KV cache, batch spikes)
  - [x] Seasonal pattern learning
  - [x] Prediction accuracy tracking
  - [x] Self-calibration system
  - [x] ConfigMap persistence
- [x] **Phase 4: Schedule Hints**
  - [x] Cron-based schedule definitions
  - [x] Independent of prediction config
  - [x] Duration-based activation
- [ ] **Phase 5: GPU-Aware Scaling**
  - [x] GPU metrics provider (DCGM, NVML, Prometheus)
  - [x] GPU memory/compute thresholds
  - [ ] GPU topology awareness
  - [ ] Multi-GPU support
- [ ] **Phase 6: Cost-Aware Scaling**
  - [x] Cost calculator implementation
  - [x] Budget constraints (hourly, daily)
  - [ ] Cloud pricing integration (AWS, GCP, Azure)
  - [ ] Spot instance optimization
- [ ] **Phase 7: Multi-Cluster Support**
  - [ ] Cross-cluster metrics aggregation
  - [ ] Federated scaling decisions

## Contributing

Contributions are welcome! Please read our [Contributing Guide](CONTRIBUTING.md) for details.

## License

Apache License 2.0 - see [LICENSE](LICENSE) for details.

## Acknowledgments

This project is inspired by:
- [Kubernetes HPA](https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/)
- [KNative Pod Autoscaler](https://knative.dev/docs/serving/autoscaling/)
- [AIBrix](https://github.com/vllm-project/aibrix)
