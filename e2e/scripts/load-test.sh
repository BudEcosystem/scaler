#!/bin/bash
# Load test script to trigger scaling behavior

SIMULATOR_SVC="llm-inference-sim.llm-test.svc.cluster.local:8000"
CONCURRENT_REQUESTS=${1:-50}
DURATION_SECONDS=${2:-60}

echo "Starting load test with $CONCURRENT_REQUESTS concurrent requests for $DURATION_SECONDS seconds"

# Function to send a single request
send_request() {
    curl -s -X POST "http://$SIMULATOR_SVC/v1/chat/completions" \
        -H "Content-Type: application/json" \
        -d '{
            "model": "Qwen/Qwen2.5-1.5B-Instruct",
            "messages": [{"role": "user", "content": "Write a long essay about artificial intelligence and its impact on society. Include details about machine learning, deep learning, and neural networks."}],
            "max_tokens": 500,
            "stream": false
        }' > /dev/null 2>&1
}

# Send concurrent requests in a loop
END_TIME=$(($(date +%s) + DURATION_SECONDS))
while [ $(date +%s) -lt $END_TIME ]; do
    for i in $(seq 1 $CONCURRENT_REQUESTS); do
        send_request &
    done
    wait
    echo "Batch completed at $(date)"
done

echo "Load test completed"
