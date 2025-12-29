#!/bin/bash
# Accuracy Test - Measures MAE, MAPE, Direction Accuracy
# Supports quick (3 min) and full (10 min) modes
#
# Usage:
#   ./accuracy-test.sh [run|cleanup|status] [--quick|--full]
#
# Examples:
#   ./accuracy-test.sh run --quick    # Quick test (~8 min)
#   ./accuracy-test.sh run --full     # Full test (~15 min)
#   ./accuracy-test.sh status         # Show current metrics
#   ./accuracy-test.sh cleanup        # Clean up resources

set -e

# Source common utilities
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/common.sh"

# Test configuration
SCALER_NAME="accuracy-test-scaler"
METRICS_SERVER="learning-metrics-server"

# Mode-specific settings
MODE="${2:---quick}"
if [ "$MODE" = "--full" ]; then
    PATTERN_DURATION=120  # 2 minutes per pattern
    VERIFY_WAIT=600       # 10 minutes for verification
    log_info "Running in FULL mode (longer patterns, more verification time)"
else
    PATTERN_DURATION=30   # 30 seconds per pattern
    VERIFY_WAIT=330       # 5.5 minutes for verification
    log_info "Running in QUICK mode (shorter patterns)"
fi

set_metrics() {
    local gpu_cache=$1
    local running=$2
    local waiting=$3
    local pod=$(kubectl get pod -l "app=$METRICS_SERVER" -n "$NAMESPACE" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
    kubectl exec "$pod" -n "$NAMESPACE" -- \
        curl -s "localhost:8000/set?gpu_cache=${gpu_cache}&requests_running=${running}&requests_waiting=${waiting}" >/dev/null 2>&1 || true
}

set_pattern() {
    local pattern=$1
    local pod=$(kubectl get pod -l "app=$METRICS_SERVER" -n "$NAMESPACE" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
    kubectl exec "$pod" -n "$NAMESPACE" -- \
        curl -s "localhost:8000/set_pattern?mode=${pattern}&duration=${PATTERN_DURATION}" >/dev/null 2>&1 || true
}

get_learning_status() {
    kubectl get budaiscaler "$SCALER_NAME" -n "$NAMESPACE" -o json 2>/dev/null | jq '.status.learningStatus // {}'
}

print_accuracy_metrics() {
    echo ""
    echo "=============================================================================="
    echo "                      ACCURACY METRICS"
    echo "=============================================================================="
    local status=$(get_learning_status)
    echo "$status" | jq -r '
        "Data Points:      \(.dataPointsCollected // 0)",
        "Direction Acc:    \(.directionAccuracy // "N/A")",
        "Overall Acc:      \(.overallAccuracy // "N/A")",
        "MAPE:             \(.recentMAPE // "N/A")",
        "MAE:              \(.recentMAE // "N/A")",
        "Pattern:          \(.detectedPattern // "unknown")",
        "Confidence:       \(.patternConfidence // "0.00")",
        "Calibration:      \(if .calibrationNeeded then "NEEDED" else "OK" end)"
    '
    echo "=============================================================================="
}

do_cleanup() {
    log_step "Cleaning up accuracy test resources"
    kubectl delete -f "$CONFIGS_DIR/accuracy-test.yaml" --ignore-not-found -n "$NAMESPACE" 2>/dev/null || true
    kubectl delete -f "$MOCKS_DIR/learning-metrics-server.yaml" --ignore-not-found -n "$NAMESPACE" 2>/dev/null || true
    kubectl delete -f "$MOCKS_DIR/simulator.yaml" --ignore-not-found -n "$NAMESPACE" 2>/dev/null || true
}

setup() {
    log_step "Setting up test environment"
    setup_namespace

    log_info "Deploying mock metrics server..."
    apply_config "$MOCKS_DIR/learning-metrics-server.yaml"
    wait_for_pod "app=$METRICS_SERVER"

    log_info "Deploying simulator deployment..."
    apply_config "$MOCKS_DIR/simulator.yaml"
    wait_for_pod "app=llm-inference-sim"

    log_info "Deploying accuracy test scaler..."
    apply_config "$CONFIGS_DIR/accuracy-test.yaml"
    sleep 10
    wait_for_scaler "$SCALER_NAME"

    log_success "Setup complete"
}

# Run through test patterns
run_patterns() {
    local total_patterns=6
    local pattern_time=$PATTERN_DURATION
    local total_time=$((total_patterns * pattern_time))

    log_step "Collecting predictions ($total_patterns patterns, ${total_time}s total)"

    log_info "Pattern 1/$total_patterns: Steady state (${pattern_time}s)..."
    set_metrics 50 10 2
    sleep "$pattern_time"

    log_info "Pattern 2/$total_patterns: High GPU cache - scale up (${pattern_time}s)..."
    set_metrics 85 25 15
    sleep "$pattern_time"

    log_info "Pattern 3/$total_patterns: Request spike - scale up (${pattern_time}s)..."
    set_metrics 70 40 30
    sleep "$pattern_time"

    log_info "Pattern 4/$total_patterns: Low load - scale down (${pattern_time}s)..."
    set_metrics 20 3 0
    sleep "$pattern_time"

    log_info "Pattern 5/$total_patterns: KV cache pressure (${pattern_time}s)..."
    set_pattern "kv_pressure"
    sleep "$pattern_time"

    log_info "Pattern 6/$total_patterns: Return to normal (${pattern_time}s)..."
    set_pattern "normal"
    sleep "$pattern_time"

    log_success "Pattern collection complete"
    print_accuracy_metrics
}

# Wait for verifications to complete
wait_for_verifications() {
    local wait_time=$VERIFY_WAIT
    log_step "Waiting for verifications (${wait_time}s)"

    local elapsed=0
    local interval=30
    while [ $elapsed -lt $wait_time ]; do
        sleep $interval
        elapsed=$((elapsed + interval))

        # Show progress every 30 seconds
        local pct=$((elapsed * 100 / wait_time))
        log_info "Progress: ${elapsed}s / ${wait_time}s (${pct}%)"

        # Show metrics every minute
        if [ $((elapsed % 60)) -eq 0 ]; then
            local status=$(get_learning_status)
            local acc=$(echo "$status" | jq -r '.directionAccuracy // "N/A"')
            local mape=$(echo "$status" | jq -r '.recentMAPE // "N/A"')
            echo -e "  ${BLUE}Direction: $acc, MAPE: $mape${NC}"
        fi
    done

    log_success "Verification wait complete"
}

# Show final results
show_results() {
    log_step "Final Results"
    print_accuracy_metrics

    echo ""
    log_info "Full Learning Status:"
    get_learning_status | jq .

    echo ""
    log_info "Scaler Status:"
    kubectl get budaiscaler "$SCALER_NAME" -n "$NAMESPACE" -o wide 2>/dev/null || true
}

# Main test function
run_test() {
    print_header "Accuracy Test ($MODE)"

    local total_time
    if [ "$MODE" = "--full" ]; then
        total_time="~15 minutes"
    else
        total_time="~8 minutes"
    fi
    log_info "Estimated duration: $total_time"

    do_cleanup
    sleep 3
    setup
    run_patterns
    wait_for_verifications
    show_results

    if [[ "${CLEANUP:-false}" == "true" ]]; then
        do_cleanup
    else
        log_info "Resources left running. Use CLEANUP=true to remove."
    fi

    log_success "Accuracy test completed!"
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
        print_accuracy_metrics
        ;;
    *)
        echo "Usage: $0 [run|cleanup|status] [--quick|--full]"
        echo ""
        echo "Commands:"
        echo "  run      Run the accuracy test (default)"
        echo "  cleanup  Clean up test resources"
        echo "  status   Show current accuracy metrics"
        echo ""
        echo "Modes:"
        echo "  --quick  Quick test with 30s patterns (~8 min total)"
        echo "  --full   Full test with 120s patterns (~15 min total)"
        exit 1
        ;;
esac
