# Scaler

[![Go Version](https://img.shields.io/github/go-mod/go-version/BudEcosystem/scaler)](https://go.dev/)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

**BudAIScaler** is a Kubernetes autoscaler designed for GenAI workloads. It provides intelligent scaling with GPU awareness, cost optimization, and predictive capabilities.

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

### Advanced Capabilities

- **GPU-Aware Scaling** - Scale based on GPU memory/compute utilization
- **Cost-Aware Scaling** - Budget constraints, spot instance preference
- **Predictive Scaling** - Time-series forecasting, schedule hints
- **Multi-Cluster Support** - Federated scaling across clusters

## Quick Start

### Prerequisites

- Kubernetes 1.26+
- Helm 3.x (for Helm installation)
- kubectl configured to access your cluster

### Installation

#### Using Helm (Recommended)

```bash
# Add the Helm repository (coming soon)
# helm repo add budecosystem https://budecosystem.github.io/scaler

# Install from local chart
helm install scaler charts/scaler \
  --namespace scaler-system \
  --create-namespace
```

#### Using Kustomize

```bash
# Install CRDs
kubectl apply -k config/crd

# Install controller
kubectl apply -k config/default
```

### Create a BudAIScaler

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
    - metricSourceType: resource
      targetMetric: cpu
      targetValue: "80"
```

```bash
kubectl apply -f my-scaler.yaml
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
      stabilizationWindowSeconds: 0    # React immediately
    scaleDown:
      stabilizationWindowSeconds: 300  # 5 min cooldown
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

### GPU-Aware Scaling

```yaml
spec:
  gpuConfig:
    enabled: true
    gpuMemoryThreshold: 80      # Scale up when GPU memory > 80%
    gpuComputeThreshold: 70     # Scale up when GPU compute > 70%
    topologyAware: true         # Consider GPU topology
    preferredGPUType: nvidia-a100
```

### Cost-Aware Scaling

```yaml
spec:
  costConfig:
    enabled: true
    cloudProvider: aws          # aws, gcp, azure
    budgetPerHour: "10.00"      # Max $10/hour
    budgetPerDay: "200.00"      # Max $200/day
    preferSpotInstances: true
    spotFallbackToOnDemand: true
```

### Predictive Scaling

```yaml
spec:
  predictionConfig:
    enabled: true
    lookAheadMinutes: 15
    historicalDataDays: 7
    enableLearning: true
    scheduleHints:
      - name: morning-rush
        cronExpression: "0 9 * * 1-5"
        targetReplicas: 5
        duration: 2h
```

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
┌─────────────────────────────────────────────────────────────────┐
│                      BudAIScaler Controller                      │
├─────────────────────────────────────────────────────────────────┤
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────────────────┐  │
│  │   Metrics   │  │  Algorithms │  │    Scaling Context      │  │
│  │  Collector  │  │             │  │  (Single Source of      │  │
│  │             │  │  - HPA      │  │   Truth for Config)     │  │
│  │  - Pod      │  │  - KPA      │  │                         │  │
│  │  - Resource │  │  - BudScaler│  │  - Replica bounds       │  │
│  │  - Prometheus│ │             │  │  - Scaling rates        │  │
│  │  - External │  │             │  │  - Time windows         │  │
│  │  - vLLM/TGI │  │             │  │  - GPU/Cost/Prediction  │  │
│  └─────────────┘  └─────────────┘  └─────────────────────────┘  │
├─────────────────────────────────────────────────────────────────┤
│                        Metric Aggregation                        │
│           (Stable Window: 180s, Panic Window: 60s)              │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
              ┌───────────────────────────────┐
              │     Target Workload           │
              │  (Deployment/StatefulSet)     │
              └───────────────────────────────┘
```

## Examples

See the [config/samples](config/samples/) directory for complete examples:

- `scaler_v1alpha1_budaiscaler_hpa.yaml` - HPA strategy
- `scaler_v1alpha1_budaiscaler_kpa.yaml` - KPA strategy
- `scaler_v1alpha1_budaiscaler_vllm.yaml` - vLLM scaling
- `scaler_v1alpha1_budaiscaler_tgi.yaml` - TGI scaling
- `scaler_v1alpha1_budaiscaler_full.yaml` - Full configuration

## Helm Chart Configuration

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
go build -o bin/manager cmd/controller/main.go

# Build Docker image
docker build -t scaler-controller:latest .

# Run tests
go test ./...
```

### Run Locally

```bash
# Install CRDs
kubectl apply -k config/crd

# Run controller locally
go run cmd/controller/main.go
```

### Project Structure

```
scaler/
├── api/scaler/v1alpha1/     # CRD types
├── cmd/controller/          # Entry point
├── config/
│   ├── crd/                 # CRD manifests
│   ├── rbac/                # RBAC configuration
│   ├── manager/             # Controller deployment
│   └── samples/             # Example BudAIScalers
├── charts/scaler/           # Helm chart
└── pkg/
    ├── algorithm/           # Scaling algorithms
    ├── aggregation/         # Metric aggregation
    ├── context/             # ScalingContext
    ├── controller/          # Reconciler
    ├── metrics/             # Metric fetchers
    └── types/               # Core types
```

## Roadmap

- [x] Phase 1: Core autoscaling (HPA, KPA, BudScaler)
- [x] Phase 2: Extended metrics (Prometheus, vLLM, TGI)
- [ ] Phase 3: GPU-aware scaling implementation
- [ ] Phase 4: Cost-aware scaling implementation
- [ ] Phase 5: Predictive scaling implementation
- [ ] Phase 6: Multi-cluster support

## Contributing

Contributions are welcome! Please read our [Contributing Guide](CONTRIBUTING.md) for details.

## License

Apache License 2.0 - see [LICENSE](LICENSE) for details.

## Acknowledgments

This project is inspired by:
- [Kubernetes HPA](https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/)
- [KNative Pod Autoscaler](https://knative.dev/docs/serving/autoscaling/)
- [AIBrix](https://github.com/vllm-project/aibrix)
