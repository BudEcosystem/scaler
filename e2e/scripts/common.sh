#!/bin/bash
# Common utilities for e2e tests
# Source this file in test scripts: source "$(dirname "$0")/common.sh"

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Directories
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_DIR="$(dirname "$SCRIPT_DIR")"
CONFIGS_DIR="$E2E_DIR/configs"
MOCKS_DIR="$E2E_DIR/mocks"

# Default namespace
NAMESPACE="${NAMESPACE:-llm-test}"

# Logging functions
log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

log_step() {
    echo -e "\n${BLUE}==>${NC} $1"
}

# Wait for a pod to be ready
wait_for_pod() {
    local label=$1
    local timeout=${2:-120}
    log_info "Waiting for pod with label $label to be ready (timeout: ${timeout}s)..."
    kubectl wait --for=condition=ready pod -l "$label" -n "$NAMESPACE" --timeout="${timeout}s" 2>/dev/null || {
        log_error "Pod with label $label did not become ready within ${timeout}s"
        return 1
    }
    log_success "Pod with label $label is ready"
}

# Wait for a deployment to be available
wait_for_deployment() {
    local name=$1
    local timeout=${2:-120}
    log_info "Waiting for deployment $name to be available (timeout: ${timeout}s)..."
    kubectl wait --for=condition=available deployment/"$name" -n "$NAMESPACE" --timeout="${timeout}s" 2>/dev/null || {
        log_error "Deployment $name did not become available within ${timeout}s"
        return 1
    }
    log_success "Deployment $name is available"
}

# Wait for BudAIScaler to be ready
wait_for_scaler() {
    local name=$1
    local timeout=${2:-60}
    log_info "Waiting for BudAIScaler $name to be ready (timeout: ${timeout}s)..."

    local end_time=$((SECONDS + timeout))
    while [ $SECONDS -lt $end_time ]; do
        local ready=$(kubectl get budaiscaler "$name" -n "$NAMESPACE" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null)
        if [ "$ready" = "True" ]; then
            log_success "BudAIScaler $name is ready"
            return 0
        fi
        sleep 2
    done

    log_error "BudAIScaler $name did not become ready within ${timeout}s"
    return 1
}

# Get current replica count
get_replicas() {
    local scaler_name=$1
    kubectl get budaiscaler "$scaler_name" -n "$NAMESPACE" -o jsonpath='{.status.actualScale}' 2>/dev/null || echo "0"
}

# Get desired replica count
get_desired_replicas() {
    local scaler_name=$1
    kubectl get budaiscaler "$scaler_name" -n "$NAMESPACE" -o jsonpath='{.status.desiredScale}' 2>/dev/null || echo "0"
}

# Set metric value on mock server
set_metric() {
    local service=$1
    local metric=$2
    local value=$3
    local port=${4:-8000}

    local pod=$(kubectl get pod -l "app=$service" -n "$NAMESPACE" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
    if [ -z "$pod" ]; then
        log_error "No pod found for service $service"
        return 1
    fi

    kubectl exec "$pod" -n "$NAMESPACE" -- curl -s "http://localhost:$port/set?metric=$metric&value=$value" >/dev/null 2>&1
    log_info "Set $metric=$value on $service"
}

# Setup namespace
setup_namespace() {
    log_step "Setting up namespace $NAMESPACE"
    kubectl create namespace "$NAMESPACE" 2>/dev/null || true
}

# Cleanup resources
cleanup() {
    local resources=("$@")
    log_step "Cleaning up resources"

    for resource in "${resources[@]}"; do
        log_info "Deleting $resource..."
        kubectl delete -f "$resource" -n "$NAMESPACE" --ignore-not-found=true 2>/dev/null || true
    done
}

# Full cleanup of namespace
cleanup_namespace() {
    log_step "Cleaning up namespace $NAMESPACE"
    kubectl delete budaiscaler --all -n "$NAMESPACE" --ignore-not-found=true 2>/dev/null || true
    kubectl delete deployment --all -n "$NAMESPACE" --ignore-not-found=true 2>/dev/null || true
    kubectl delete service --all -n "$NAMESPACE" --ignore-not-found=true 2>/dev/null || true
    kubectl delete configmap --all -n "$NAMESPACE" --ignore-not-found=true 2>/dev/null || true
    kubectl delete job --all -n "$NAMESPACE" --ignore-not-found=true 2>/dev/null || true
}

# Apply a config file
apply_config() {
    local file=$1
    log_info "Applying $file..."
    kubectl apply -f "$file" -n "$NAMESPACE" --server-side 2>/dev/null || kubectl apply -f "$file" -n "$NAMESPACE"
}

# Show scaler status
show_scaler_status() {
    local name=$1
    echo ""
    log_info "BudAIScaler $name status:"
    kubectl get budaiscaler "$name" -n "$NAMESPACE" -o wide 2>/dev/null || true
    echo ""
}

# Show learning status
show_learning_status() {
    local name=$1
    log_info "Learning status for $name:"
    kubectl get budaiscaler "$name" -n "$NAMESPACE" -o jsonpath='{.status.learningStatus}' 2>/dev/null | jq . 2>/dev/null || true
    echo ""
}

# Print test header
print_header() {
    local title=$1
    echo ""
    echo -e "${BLUE}======================================${NC}"
    echo -e "${BLUE}  $title${NC}"
    echo -e "${BLUE}======================================${NC}"
    echo ""
}

# Print test result
print_result() {
    local test_name=$1
    local passed=$2

    if [ "$passed" = "true" ]; then
        echo -e "${GREEN}[PASS]${NC} $test_name"
    else
        echo -e "${RED}[FAIL]${NC} $test_name"
    fi
}

# Trap handler for cleanup on exit
setup_trap() {
    local cleanup_func=$1
    trap "$cleanup_func" EXIT INT TERM
}
