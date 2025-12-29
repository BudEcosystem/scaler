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
METRICS_SERVER="mock-vllm-metrics"

set_metrics() {
    local gpu_cache=$1
    local running=$2
    local waiting=$3
    local pod=$(kubectl get pod -l "app=$METRICS_SERVER" -n "$NAMESPACE" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
    kubectl exec "$pod" -n "$NAMESPACE" -- \
        curl -s "localhost:8000/set?gpu_cache=${gpu_cache}&requests_running=${running}&requests_waiting=${waiting}" >/dev/null 2>&1 || true
}

do_cleanup() {
    log_step "Cleaning up scaling test resources"
    kubectl delete -f "$CONFIGS_DIR/basic-test.yaml" --ignore-not-found -n "$NAMESPACE" 2>/dev/null || true
    kubectl delete -f "$MOCKS_DIR/metrics-server.yaml" --ignore-not-found -n "$NAMESPACE" 2>/dev/null || true
    kubectl delete -f "$MOCKS_DIR/simulator.yaml" --ignore-not-found -n "$NAMESPACE" 2>/dev/null || true
}

setup() {
    log_step "Setting up test environment"
    setup_namespace

    log_info "Deploying mock metrics server..."
    apply_config "$MOCKS_DIR/metrics-server.yaml"
    wait_for_pod "app=$METRICS_SERVER"

    log_info "Deploying simulator deployment..."
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

    log_info "Setting low load metrics (GPU cache: 20%, Running: 2, Waiting: 0)..."
    set_metrics 20 2 0

    log_info "Waiting for scale-down response (90s)..."
    for i in $(seq 1 6); do
        sleep 15
        local desired=$(get_desired_replicas "$SCALER_NAME")
        local actual=$(get_replicas "$SCALER_NAME")
        echo -e "  ${BLUE}[$((i*15))s]${NC} Desired: $desired, Actual: $actual"
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
