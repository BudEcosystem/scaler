#!/bin/bash
# E2E test for BudAIScaler scaling behavior
# Tests both scale-up and scale-down scenarios
#
# Usage:
#   ./scaling-test.sh [run|cleanup|status]
#
# Examples:
#   ./scaling-test.sh run        # Run the scaling test
#   ./scaling-test.sh status     # Show current scaler status
#   ./scaling-test.sh cleanup    # Clean up resources

set -e

# Source common utilities
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/common.sh"

# Test configuration
SCALER_NAME="llm-inference-scaler"
DEPLOYMENT="llm-inference-sim"

set_metrics() {
    local gpu_cache=$1
    local running=$2
    local waiting=$3

    # Get all simulator pods and update metrics on each one's sidecar
    local pods=$(kubectl get pod -l "app=$DEPLOYMENT" -n "$NAMESPACE" -o jsonpath='{.items[*].metadata.name}' 2>/dev/null)
    if [ -z "$pods" ]; then
        log_error "Could not find simulator pods"
        return 1
    fi

    local failed=0
    for pod in $pods; do
        # Use Python's urllib to update metrics on the sidecar (port 9000)
        local result=$(kubectl exec "$pod" -n "$NAMESPACE" -c metrics-sidecar -- python3 -c "
import urllib.request
try:
    url = 'http://localhost:9000/set?gpu_cache=${gpu_cache}&requests_running=${running}&requests_waiting=${waiting}'
    response = urllib.request.urlopen(url, timeout=5)
    print(response.read().decode())
except Exception as e:
    print(f'ERROR: {e}')
    exit(1)
" 2>&1)
        if [[ "$result" == ERROR* ]]; then
            log_error "Failed to set metrics on $pod: $result"
            failed=1
        fi
    done

    if [ $failed -eq 1 ]; then
        return 1
    fi
    log_info "Metrics set: gpu_cache=$gpu_cache, running=$running, waiting=$waiting"
}

do_cleanup() {
    log_step "Cleaning up scaling test resources"
    kubectl delete -f "$CONFIGS_DIR/basic-test.yaml" --ignore-not-found -n "$NAMESPACE" 2>/dev/null || true
    kubectl delete -f "$MOCKS_DIR/simulator.yaml" --ignore-not-found -n "$NAMESPACE" 2>/dev/null || true
}

setup() {
    log_step "Setting up test environment"
    setup_namespace

    log_info "Deploying simulator with metrics sidecar..."
    apply_config "$MOCKS_DIR/simulator.yaml"
    wait_for_pod "app=$DEPLOYMENT"

    log_info "Deploying basic test scaler..."
    apply_config "$CONFIGS_DIR/basic-test.yaml"
    sleep 10
    wait_for_scaler "$SCALER_NAME"

    log_success "Setup complete"
}

test_scale_up() {
    log_step "Testing SCALE-UP"

    log_info "Initial state:"
    show_scaler_status "$SCALER_NAME"

    log_info "Setting high load metrics (GPU cache: 85%, Running: 25, Waiting: 15)..."
    set_metrics 85 25 15

    log_info "Waiting for scale-up response (60s)..."
    for i in $(seq 1 4); do
        sleep 15
        local desired=$(get_desired_replicas "$SCALER_NAME")
        local actual=$(get_replicas "$SCALER_NAME")
        echo -e "  ${BLUE}[$((i*15))s]${NC} Desired: $desired, Actual: $actual"
    done

    local final_desired=$(get_desired_replicas "$SCALER_NAME")
    if [ "$final_desired" -gt 1 ]; then
        print_result "Scale-up triggered" "true"
    else
        print_result "Scale-up triggered" "false"
    fi
}

test_scale_down() {
    log_step "Testing SCALE-DOWN"

    # Wait for all pods to be ready after scale-up
    log_info "Waiting for all pods to be ready..."
    local ready_pods=0
    local expected_pods=$(get_replicas "$SCALER_NAME")
    for i in $(seq 1 12); do
        ready_pods=$(kubectl get pods -l "app=$DEPLOYMENT" -n "$NAMESPACE" --field-selector=status.phase=Running -o name 2>/dev/null | wc -l)
        if [ "$ready_pods" -ge "$expected_pods" ]; then
            break
        fi
        sleep 5
    done
    log_info "Found $ready_pods running pods"

    log_info "Setting low load metrics on ALL pods (GPU cache: 20%, Running: 2, Waiting: 0)..."
    set_metrics 20 2 0

    log_info "Waiting for scale-down response (120s)..."
    for i in $(seq 1 8); do
        sleep 15
        local desired=$(get_desired_replicas "$SCALER_NAME")
        local actual=$(get_replicas "$SCALER_NAME")
        echo -e "  ${BLUE}[$((i*15))s]${NC} Desired: $desired, Actual: $actual"
        # Update metrics on any new pods that might have been created
        set_metrics 20 2 0 2>/dev/null || true
    done

    local final_desired=$(get_desired_replicas "$SCALER_NAME")
    if [ "$final_desired" -le 2 ]; then
        print_result "Scale-down triggered" "true"
    else
        print_result "Scale-down triggered" "false"
    fi
}

show_status() {
    print_header "Scaling Test Status"
    show_scaler_status "$SCALER_NAME"

    log_info "Deployment status:"
    kubectl get deployment "$DEPLOYMENT" -n "$NAMESPACE" -o wide 2>/dev/null || echo "Deployment not found"
}

run_test() {
    print_header "Scaling Test"
    log_info "Estimated duration: ~3 minutes"

    do_cleanup
    sleep 3
    setup
    test_scale_up
    test_scale_down
    show_status

    if [[ "${CLEANUP:-false}" == "true" ]]; then
        do_cleanup
    else
        log_info "Resources left running. Use CLEANUP=true to remove."
    fi

    log_success "Scaling test completed!"
}

# Command handler
case "${1:-run}" in
    run)
        run_test
        ;;
    cleanup)
        do_cleanup
        ;;
    status)
        show_status
        ;;
    *)
        echo "Usage: $0 [run|cleanup|status]"
        echo ""
        echo "Commands:"
        echo "  run      Run the scaling test (default)"
        echo "  cleanup  Clean up test resources"
        echo "  status   Show current scaler status"
        exit 1
        ;;
esac
