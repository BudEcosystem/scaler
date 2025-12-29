# E2E Tests for BudAIScaler

End-to-end tests for verifying BudAIScaler functionality in a Kubernetes cluster.

## Quick Start

```bash
# From project root
cd e2e/scripts

# Run scaling test (~3 min)
./scaling-test.sh run

# Run learning test (~5 min)
./learning-test.sh run

# Run accuracy test (~8 min)
./accuracy-test.sh run --quick
```

## Directory Structure

```
e2e/
├── README.md
├── configs/                      # BudAIScaler CRD configurations
│   ├── accuracy-test.yaml        # Learning accuracy measurement
│   ├── basic-test.yaml           # Basic scaling (llm-inference-scaler)
│   ├── cost-test.yaml            # Cost-aware scaling
│   ├── gpu-test.yaml             # GPU metrics integration
│   ├── learning-test.yaml        # Adaptive learning system
│   ├── mock-test.yaml            # Mock metrics validation
│   ├── prediction-test.yaml      # ML prediction config
│   └── schedulehints-test.yaml   # Schedule-based scaling
├── mocks/                        # Mock infrastructure
│   ├── dcgm-server.yaml          # Mock DCGM exporter (GPU metrics)
│   ├── learning-metrics-server.yaml  # Programmable metrics with patterns
│   ├── load-generator.yaml       # Load generation job
│   ├── metrics-server.yaml       # Basic vLLM metrics mock
│   └── simulator.yaml            # llm-d-inference-sim deployment
└── scripts/                      # Test automation
    ├── common.sh                 # Shared utilities
    ├── accuracy-test.sh          # Prediction accuracy tests
    ├── learning-test.sh          # Learning system tests
    ├── load-test.sh              # Load generation utility
    └── scaling-test.sh           # Basic scaling tests
```

## Prerequisites

- Kubernetes cluster with `kubectl` configured
- BudAIScaler controller running in `budaiscaler-system` namespace
- `jq` installed for JSON processing
- `curl` available in test pods

## Test Scripts

### Scaling Test

Tests basic scale-up and scale-down behavior based on metrics.

```bash
./scripts/scaling-test.sh run       # Run test (~3 min)
./scripts/scaling-test.sh status    # Show scaler status
./scripts/scaling-test.sh cleanup   # Remove test resources
```

**What it tests:**
- Scale-up when GPU cache > 70% threshold
- Scale-down when load decreases
- Replica count changes within min/max bounds

### Learning Test

Tests the adaptive learning system for pattern detection and data collection.

```bash
./scripts/learning-test.sh run      # Full test (~5 min)
./scripts/learning-test.sh quick    # Quick test (~2 min)
./scripts/learning-test.sh status   # Show learning status
./scripts/learning-test.sh cleanup  # Remove test resources
```

**What it tests:**
- Data point collection
- KV cache pressure pattern detection
- Batch spike pattern detection
- Seasonal pattern learning
- ConfigMap persistence
- Prediction accuracy tracking

### Accuracy Test

Measures prediction accuracy metrics over time.

```bash
./scripts/accuracy-test.sh run --quick   # Quick test (~8 min)
./scripts/accuracy-test.sh run --full    # Full test (~15 min)
./scripts/accuracy-test.sh status        # Show accuracy metrics
./scripts/accuracy-test.sh cleanup       # Remove test resources
```

**Metrics measured:**
- **MAE** (Mean Absolute Error) - Average prediction error
- **MAPE** (Mean Absolute Percentage Error) - Percentage error
- **Direction Accuracy** - Correct scale up/down predictions

## Configuration

### Environment Variables

| Variable    | Default    | Description                        |
|-------------|------------|------------------------------------|
| `NAMESPACE` | `llm-test` | Kubernetes namespace for tests     |
| `CLEANUP`   | `false`    | Auto-cleanup resources after test  |

### Examples

```bash
# Run in custom namespace
NAMESPACE=my-tests ./scripts/scaling-test.sh run

# Auto-cleanup after test
CLEANUP=true ./scripts/learning-test.sh run

# Run quick accuracy test with cleanup
CLEANUP=true ./scripts/accuracy-test.sh run --quick
```

## Mock Servers

### metrics-server.yaml

Basic mock exposing vLLM-compatible Prometheus metrics.

```bash
# Inside the cluster, set metrics dynamically:
kubectl exec -n llm-test deploy/mock-vllm-metrics -- \
  curl -s "localhost:8000/set?gpu_cache=80&requests_running=10&requests_waiting=5"
```

### learning-metrics-server.yaml

Advanced mock with programmable traffic patterns for learning tests.

```bash
# Set traffic pattern
kubectl exec -n llm-test deploy/learning-metrics-server -- \
  curl -s "localhost:8000/set_pattern?mode=kv_pressure&duration=60"

# Simulate time of day (for seasonal patterns)
kubectl exec -n llm-test deploy/learning-metrics-server -- \
  curl -s "localhost:8000/set_time?hour=10&day=1"
```

**Available patterns:**
| Pattern         | Description                          |
|-----------------|--------------------------------------|
| `normal`        | Steady state traffic                 |
| `kv_pressure`   | High KV cache utilization (>85%)     |
| `batch_spike`   | Sudden burst of requests             |
| `traffic_drain` | Traffic decreasing to zero           |

### simulator.yaml

Deploys `llm-d-inference-sim` as the scaling target workload.

### dcgm-server.yaml

Mock DCGM exporter for GPU metrics testing.

## Writing New Tests

1. Create a BudAIScaler config in `configs/`
2. Reuse or create mock servers in `mocks/`
3. Write a test script in `scripts/` using `common.sh`:

```bash
#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/common.sh"

SCALER_NAME="my-scaler"

do_cleanup() {
    log_step "Cleaning up"
    kubectl delete -f "$CONFIGS_DIR/my-test.yaml" --ignore-not-found -n "$NAMESPACE"
    kubectl delete -f "$MOCKS_DIR/simulator.yaml" --ignore-not-found -n "$NAMESPACE"
}

setup() {
    log_step "Setting up"
    setup_namespace
    apply_config "$MOCKS_DIR/simulator.yaml"
    wait_for_pod "app=llm-inference-sim"
    apply_config "$CONFIGS_DIR/my-test.yaml"
    wait_for_scaler "$SCALER_NAME"
    log_success "Setup complete"
}

run_test() {
    print_header "My Test"
    do_cleanup
    sleep 3
    setup

    # Test logic here
    log_info "Running test..."
    sleep 30

    # Verify results
    local replicas=$(get_replicas "$SCALER_NAME")
    if [ "$replicas" -gt 1 ]; then
        print_result "Scaling worked" "true"
    else
        print_result "Scaling worked" "false"
    fi

    show_scaler_status "$SCALER_NAME"

    if [[ "${CLEANUP:-false}" == "true" ]]; then
        do_cleanup
    fi

    log_success "Test completed!"
}

case "${1:-run}" in
    run) run_test ;;
    cleanup) do_cleanup ;;
    status) show_scaler_status "$SCALER_NAME" ;;
    *) echo "Usage: $0 [run|cleanup|status]" ;;
esac
```

## Common Utilities Reference

### Variables (set by common.sh)

| Variable      | Value                    |
|---------------|--------------------------|
| `NAMESPACE`   | `llm-test` (or env var)  |
| `SCRIPT_DIR`  | Path to scripts/         |
| `E2E_DIR`     | Path to e2e/             |
| `CONFIGS_DIR` | Path to e2e/configs/     |
| `MOCKS_DIR`   | Path to e2e/mocks/       |

### Logging Functions

| Function                  | Output                    |
|---------------------------|---------------------------|
| `log_info "msg"`          | `[INFO] msg` (blue)       |
| `log_success "msg"`       | `[SUCCESS] msg` (green)   |
| `log_warning "msg"`       | `[WARNING] msg` (yellow)  |
| `log_error "msg"`         | `[ERROR] msg` (red)       |
| `log_step "msg"`          | `==> msg` (blue header)   |
| `print_header "title"`    | Boxed title               |
| `print_result "name" "t"` | `[PASS/FAIL] name`        |

### Wait Functions

| Function                        | Description                          |
|---------------------------------|--------------------------------------|
| `wait_for_pod "label" [timeout]`| Wait for pod ready (default 120s)    |
| `wait_for_deployment "name"`    | Wait for deployment available        |
| `wait_for_scaler "name"`        | Wait for BudAIScaler Ready=True      |

### Utility Functions

| Function                      | Description                      |
|-------------------------------|----------------------------------|
| `setup_namespace`             | Create namespace if not exists   |
| `apply_config "file"`         | kubectl apply with server-side   |
| `get_replicas "scaler"`       | Get actualScale                  |
| `get_desired_replicas "scaler"` | Get desiredScale               |
| `show_scaler_status "scaler"` | Print scaler status table        |
| `show_learning_status "scaler"` | Print learning status JSON     |
| `cleanup_namespace`           | Delete all resources in NS       |

## Troubleshooting

### Controller not responding

```bash
# Check controller logs
kubectl logs -n budaiscaler-system deploy/budaiscaler-controller-manager --tail=100

# Check if controller is running
kubectl get pods -n budaiscaler-system
```

### Scaler stuck in non-Ready state

```bash
# Describe the scaler
kubectl describe budaiscaler -n llm-test

# Check events
kubectl get events -n llm-test --sort-by='.lastTimestamp'

# Check if target deployment exists
kubectl get deploy -n llm-test
```

### Metrics not being collected

```bash
# Check metrics endpoint directly
kubectl exec -n llm-test deploy/llm-inference-sim -- curl -s localhost:8000/metrics

# Check if service is accessible
kubectl get svc -n llm-test

# Test from another pod
kubectl run -n llm-test test-curl --rm -it --image=curlimages/curl -- \
  curl -s http://llm-inference-sim:8000/metrics
```

### Test cleanup issues

```bash
# Force cleanup all test resources
kubectl delete budaiscaler --all -n llm-test
kubectl delete deploy --all -n llm-test
kubectl delete svc --all -n llm-test
kubectl delete configmap --all -n llm-test

# Or delete the entire namespace
kubectl delete namespace llm-test
```
