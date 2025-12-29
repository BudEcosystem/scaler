#!/bin/bash
# E2E Test for Adaptive Learning System
# Tests learning data collection, pattern detection, and prediction accuracy
#
# Usage:
#   ./learning-test.sh [run|cleanup|status|quick]
#
# Examples:
#   ./learning-test.sh run       # Full learning test (~5 min)
#   ./learning-test.sh quick     # Quick learning test (~2 min)
#   ./learning-test.sh status    # Show current learning status
#   ./learning-test.sh cleanup   # Clean up resources

set -e

# Source common utilities
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/common.sh"

# Test configuration
SCALER_NAME="learning-scaler"
METRICS_SERVER="learning-metrics-server"
CONFIGMAP_PREFIX="learning-data"

# Mode-specific settings
MODE="${2:---full}"
if [ "$MODE" = "--quick" ] || [ "${1:-}" = "quick" ]; then
    PATTERN_DURATION=15
    PATTERN_WAIT=10
    VERIFY_WAIT=30
    log_info "Running in QUICK mode"
else
    PATTERN_DURATION=30
    PATTERN_WAIT=20
    VERIFY_WAIT=60
    log_info "Running in FULL mode"
fi

set_pattern() {
    local pattern=$1
    local duration=${2:-$PATTERN_DURATION}
    log_info "Setting pattern: $pattern for ${duration}s"
    local pod=$(kubectl get pod -l "app=$METRICS_SERVER" -n "$NAMESPACE" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
    kubectl exec "$pod" -n "$NAMESPACE" -- \
        curl -s "localhost:8000/set_pattern?mode=${pattern}&duration=${duration}" >/dev/null 2>&1 || true
}

set_simulated_time() {
    local hour=$1
    local day=${2:-1}
    log_info "Setting simulated time: hour=$hour, day=$day"
    local pod=$(kubectl get pod -l "app=$METRICS_SERVER" -n "$NAMESPACE" -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
    kubectl exec "$pod" -n "$NAMESPACE" -- \
        curl -s "localhost:8000/set_time?hour=${hour}&day=${day}" >/dev/null 2>&1 || true
}

get_learning_status() {
    kubectl get budaiscaler "$SCALER_NAME" -n "$NAMESPACE" -o json 2>/dev/null | jq '.status.learningStatus // {}'
}

get_learning_configmap() {
    kubectl get configmap -n "$NAMESPACE" -l app.kubernetes.io/managed-by=budaiscaler-learning -o json 2>/dev/null || echo '{"items":[]}'
}

do_cleanup() {
    log_step "Cleaning up learning test resources"
    kubectl delete -f "$CONFIGS_DIR/learning-test.yaml" --ignore-not-found -n "$NAMESPACE" 2>/dev/null || true
    kubectl delete -f "$MOCKS_DIR/learning-metrics-server.yaml" --ignore-not-found -n "$NAMESPACE" 2>/dev/null || true
    kubectl delete -f "$MOCKS_DIR/simulator.yaml" --ignore-not-found -n "$NAMESPACE" 2>/dev/null || true
    kubectl delete configmap -l app.kubernetes.io/managed-by=budaiscaler-learning -n "$NAMESPACE" 2>/dev/null || true
}

setup() {
    log_step "Setting up test environment"
    setup_namespace

    log_info "Deploying learning metrics server..."
    apply_config "$MOCKS_DIR/learning-metrics-server.yaml"
    wait_for_pod "app=$METRICS_SERVER"

    log_info "Deploying simulator deployment..."
    apply_config "$MOCKS_DIR/simulator.yaml"
    wait_for_pod "app=llm-inference-sim"

    log_info "Deploying learning-enabled scaler..."
    apply_config "$CONFIGS_DIR/learning-test.yaml"
    sleep 10
    wait_for_scaler "$SCALER_NAME"

    log_success "Setup complete"
}

test_normal_pattern() {
    log_step "Test: Normal Pattern Collection"

    set_pattern "normal" "$PATTERN_DURATION"
    set_simulated_time 10 1  # Monday 10 AM (peak)

    log_info "Collecting data for ${PATTERN_WAIT}s in normal mode..."
    sleep "$PATTERN_WAIT"

    local status=$(get_learning_status)
    local replicas=$(echo "$status" | jq -r '.currentReplicas // 0' 2>/dev/null || echo "0")
    local data_points=$(echo "$status" | jq -r '.dataPointsCollected // 0' 2>/dev/null || echo "0")

    log_info "Data points: $data_points"

    if [[ "$data_points" -gt 0 ]]; then
        print_result "Normal pattern data collection" "true"
    else
        print_result "Normal pattern data collection" "false"
    fi
}

test_kv_cache_pressure() {
    log_step "Test: KV Cache Pressure Detection"

    set_pattern "kv_pressure" "$PATTERN_DURATION"

    log_info "Simulating KV cache pressure for ${PATTERN_WAIT}s..."
    sleep "$PATTERN_WAIT"

    local status=$(get_learning_status)
    local detected=$(echo "$status" | jq -r '.detectedPattern // "unknown"')

    log_info "Detected pattern: $detected"

    if [[ "$detected" == *"kv"* ]] || [[ "$detected" == *"pressure"* ]] || [[ "$detected" == *"cache"* ]]; then
        print_result "KV cache pressure detection" "true"
    else
        print_result "KV cache pressure detection" "false"
        log_info "Note: Pattern detection requires sufficient data points"
    fi
}

test_batch_spike() {
    log_step "Test: Batch Spike Detection"

    set_pattern "batch_spike" "$PATTERN_DURATION"

    log_info "Simulating batch spike for ${PATTERN_WAIT}s..."
    sleep "$PATTERN_WAIT"

    local status=$(get_learning_status)
    local detected=$(echo "$status" | jq -r '.detectedPattern // "unknown"')

    log_info "Detected pattern: $detected"

    if [[ "$detected" == *"spike"* ]] || [[ "$detected" == *"batch"* ]]; then
        print_result "Batch spike detection" "true"
    else
        print_result "Batch spike detection" "false"
    fi
}

test_seasonal_patterns() {
    log_step "Test: Seasonal Pattern Learning"

    local times=("10" "15" "23" "3")
    local descriptions=("Morning peak" "Afternoon" "Night" "Early morning")

    for i in "${!times[@]}"; do
        set_simulated_time "${times[$i]}" 1
        set_pattern "normal" 15
        log_info "Testing ${descriptions[$i]} (hour ${times[$i]})..."
        sleep 10
    done

    local status=$(get_learning_status)
    local data_points=$(echo "$status" | jq -r '.dataPointsCollected // 0')

    log_info "Total data points: $data_points"

    if [[ "$data_points" -gt 5 ]]; then
        print_result "Seasonal data collection" "true"
    else
        print_result "Seasonal data collection" "false"
    fi
}

test_configmap_persistence() {
    log_step "Test: ConfigMap Persistence"

    log_info "Waiting for ConfigMap persistence (${VERIFY_WAIT}s)..."
    sleep "$VERIFY_WAIT"

    local configmaps=$(get_learning_configmap)
    local cm_count=$(echo "$configmaps" | jq '.items | length')

    if [[ "$cm_count" -gt 0 ]]; then
        local cm_name=$(echo "$configmaps" | jq -r '.items[0].metadata.name')
        local data_keys=$(echo "$configmaps" | jq -r '.items[0].data | keys | length')

        log_info "ConfigMap: $cm_name (keys: $data_keys)"
        print_result "ConfigMap persistence" "true"
    else
        print_result "ConfigMap persistence" "false"
        log_info "Note: ConfigMap may need more time to be created"
    fi
}

test_prediction_accuracy() {
    log_step "Test: Prediction Accuracy Tracking"

    local status=$(get_learning_status)

    local overall=$(echo "$status" | jq -r '.overallAccuracy // "N/A"')
    local direction=$(echo "$status" | jq -r '.directionAccuracy // "N/A"')
    local mape=$(echo "$status" | jq -r '.recentMAPE // "N/A"')
    local mae=$(echo "$status" | jq -r '.recentMAE // "N/A"')

    log_info "Overall accuracy:   $overall"
    log_info "Direction accuracy: $direction"
    log_info "MAPE:               $mape"
    log_info "MAE:                $mae"

    if [[ "$overall" != "N/A" ]] || [[ "$direction" != "N/A" ]]; then
        print_result "Accuracy tracking" "true"
    else
        print_result "Accuracy tracking" "false"
        log_info "Note: Accuracy requires multiple prediction cycles"
    fi
}

show_status() {
    print_header "Learning System Status"

    log_info "Learning Status:"
    get_learning_status | jq .

    echo ""
    log_info "ConfigMaps:"
    kubectl get configmap -n "$NAMESPACE" -l app.kubernetes.io/managed-by=budaiscaler-learning 2>/dev/null || echo "None found"

    echo ""
    show_scaler_status "$SCALER_NAME"
}

print_summary() {
    log_step "Test Summary"

    echo ""
    echo "=============================================================================="
    echo "                      LEARNING TEST RESULTS"
    echo "=============================================================================="

    local status=$(get_learning_status)
    echo "$status" | jq -r '
        "Data Points:       \(.dataPointsCollected // 0)",
        "Detected Pattern:  \(.detectedPattern // "unknown")",
        "Pattern Confidence:\(.patternConfidence // "0.00")",
        "Direction Accuracy:\(.directionAccuracy // "N/A")",
        "Overall Accuracy:  \(.overallAccuracy // "N/A")",
        "MAPE:              \(.recentMAPE // "N/A")",
        "MAE:               \(.recentMAE // "N/A")"
    '

    echo "=============================================================================="
    echo ""
}

run_test() {
    print_header "Learning System Test"

    local total_time
    if [ "$MODE" = "--quick" ] || [ "${1:-}" = "quick" ]; then
        total_time="~2 minutes"
    else
        total_time="~5 minutes"
    fi
    log_info "Estimated duration: $total_time"

    do_cleanup
    sleep 3
    setup

    test_normal_pattern
    test_kv_cache_pressure
    test_batch_spike
    test_seasonal_patterns
    test_configmap_persistence
    test_prediction_accuracy

    print_summary

    if [[ "${CLEANUP:-false}" == "true" ]]; then
        do_cleanup
    else
        log_info "Resources left running. Use CLEANUP=true to remove."
    fi

    log_success "Learning test completed!"
}

run_quick() {
    MODE="--quick"
    PATTERN_DURATION=15
    PATTERN_WAIT=10
    VERIFY_WAIT=30
    run_test
}

# Command handler
case "${1:-run}" in
    run)
        run_test
        ;;
    quick)
        run_quick
        ;;
    cleanup)
        do_cleanup
        ;;
    status)
        show_status
        ;;
    *)
        echo "Usage: $0 [run|quick|cleanup|status]"
        echo ""
        echo "Commands:"
        echo "  run      Full learning test (~5 min, default)"
        echo "  quick    Quick learning test (~2 min)"
        echo "  cleanup  Clean up test resources"
        echo "  status   Show current learning status"
        exit 1
        ;;
esac
