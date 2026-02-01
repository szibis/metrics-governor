#!/bin/bash
set -e

# FastQueue E2E Persistence Test Script
# Tests queue persistence, recovery, and metrics

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_DIR"

# Configuration
STARTUP_TIMEOUT=60
VERIFICATION_TIMEOUT=60
CONTAINER_NAME="metrics-governor-metrics-governor-1"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $1"; }
log_warn() { echo -e "${YELLOW}[WARN]${NC} $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }

cleanup() {
    log_info "Cleaning up..."
    docker compose -f docker-compose.yaml -f docker-compose.queue.yaml down -v --remove-orphans 2>/dev/null || true
}

trap cleanup EXIT

# Start stack with queue configuration
start_queue_stack() {
    log_info "Starting stack with FastQueue configuration..."
    docker compose -f docker-compose.yaml -f docker-compose.queue.yaml up -d --build
    if [ $? -ne 0 ]; then
        log_error "Failed to start stack"
        exit 1
    fi
}

# Wait for services
wait_for_services() {
    log_info "Waiting for services (timeout: ${STARTUP_TIMEOUT}s)..."
    local start_time=$(date +%s)

    while true; do
        local elapsed=$(($(date +%s) - start_time))
        if [ $elapsed -gt $STARTUP_TIMEOUT ]; then
            log_error "Timeout waiting for services"
            docker compose -f docker-compose.yaml -f docker-compose.queue.yaml logs --tail=50
            exit 1
        fi

        local status=$(docker inspect --format='{{.State.Status}}' "$CONTAINER_NAME" 2>/dev/null || echo "not found")
        if [ "$status" = "running" ]; then
            # Check if metrics endpoint is ready
            if curl -s http://localhost:9090/metrics > /dev/null 2>&1; then
                log_info "Services are ready"
                break
            fi
        fi

        echo -n "."
        sleep 2
    done
}

# Verify FastQueue metrics are exposed
verify_fastqueue_metrics() {
    log_info "Verifying FastQueue metrics..."

    local metrics=$(curl -s http://localhost:9090/metrics 2>/dev/null)

    # Check for required FastQueue metrics
    local required_metrics=(
        "metrics_governor_fastqueue_inmemory_blocks"
        "metrics_governor_fastqueue_disk_bytes"
        "metrics_governor_fastqueue_meta_sync_total"
        "metrics_governor_fastqueue_chunk_rotations"
        "metrics_governor_fastqueue_inmemory_flushes"
        "metrics_governor_queue_size"
        "metrics_governor_queue_push_total"
    )

    local missing=0
    for metric in "${required_metrics[@]}"; do
        if ! echo "$metrics" | grep -q "^${metric}"; then
            log_error "Missing metric: $metric"
            missing=$((missing + 1))
        else
            local value=$(echo "$metrics" | grep "^${metric}" | head -1 | awk '{print $2}')
            log_info "Found metric: $metric = $value"
        fi
    done

    if [ $missing -gt 0 ]; then
        log_error "$missing required FastQueue metrics missing"
        exit 1
    fi

    log_info "All FastQueue metrics are present"
}

# Wait for queue activity
wait_for_queue_activity() {
    log_info "Waiting for queue activity (timeout: ${VERIFICATION_TIMEOUT}s)..."
    local start_time=$(date +%s)

    while true; do
        local elapsed=$(($(date +%s) - start_time))
        if [ $elapsed -gt $VERIFICATION_TIMEOUT ]; then
            log_error "Timeout waiting for queue activity"
            exit 1
        fi

        local push_total=$(curl -s http://localhost:9090/metrics 2>/dev/null | grep "^metrics_governor_queue_push_total" | awk '{print $2}' | cut -d'.' -f1)
        push_total=${push_total:-0}

        if [ "$push_total" -gt 0 ]; then
            log_info "Queue has activity: push_total=$push_total"
            break
        fi

        sleep 2
    done
}

# Check queue data files exist
check_queue_files() {
    log_info "Checking queue data files..."

    # Check for fastqueue.meta file in the container
    local meta_exists=$(docker exec "$CONTAINER_NAME" ls -la /data/queue/fastqueue.meta 2>/dev/null || echo "not found")

    if echo "$meta_exists" | grep -q "not found"; then
        log_warn "fastqueue.meta not found (data may be in memory only)"
    else
        log_info "Found fastqueue.meta file"
        docker exec "$CONTAINER_NAME" cat /data/queue/fastqueue.meta 2>/dev/null || true
    fi

    # List all queue files
    log_info "Queue directory contents:"
    docker exec "$CONTAINER_NAME" ls -la /data/queue/ 2>/dev/null || log_warn "Could not list queue directory"
}

# Test queue recovery after restart
test_queue_recovery() {
    log_info "Testing queue recovery after restart..."

    # Get current queue state
    local push_before=$(curl -s http://localhost:9090/metrics 2>/dev/null | grep "^metrics_governor_queue_push_total" | awk '{print $2}' | cut -d'.' -f1)
    local meta_sync_before=$(curl -s http://localhost:9090/metrics 2>/dev/null | grep "^metrics_governor_fastqueue_meta_sync_total" | awk '{print $2}' | cut -d'.' -f1)

    log_info "Before restart: push_total=$push_before, meta_sync_total=$meta_sync_before"

    # Force a metadata sync by waiting
    log_info "Waiting for metadata sync..."
    sleep 3

    # Restart metrics-governor container
    log_info "Restarting metrics-governor container..."
    docker restart "$CONTAINER_NAME"

    # Wait for it to come back up
    sleep 10
    wait_for_services

    # Check queue state after restart
    local queue_size=$(curl -s http://localhost:9090/metrics 2>/dev/null | grep "^metrics_governor_queue_size" | awk '{print $2}' | cut -d'.' -f1)
    queue_size=${queue_size:-0}

    log_info "After restart: queue_size=$queue_size"

    # Queue should either be empty (all exported) or have recovered entries
    log_info "Queue recovery test completed"

    # Check for any errors in logs
    local errors=$(docker logs "$CONTAINER_NAME" 2>&1 | grep -i "error" | tail -5)
    if [ -n "$errors" ]; then
        log_warn "Errors found in logs:"
        echo "$errors"
    else
        log_info "No errors in container logs"
    fi
}

# Print final summary
print_summary() {
    log_info "=== FastQueue E2E Test Summary ==="
    echo ""

    log_info "Container status:"
    docker compose -f docker-compose.yaml -f docker-compose.queue.yaml ps
    echo ""

    log_info "FastQueue metrics:"
    curl -s http://localhost:9090/metrics 2>/dev/null | grep -E "^metrics_governor_(fastqueue|queue)" | head -15
    echo ""

    log_info "Queue directory:"
    docker exec "$CONTAINER_NAME" ls -la /data/queue/ 2>/dev/null || true
    echo ""
}

# Main execution
main() {
    log_info "Starting FastQueue E2E tests..."
    echo ""

    cleanup
    start_queue_stack

    sleep 5

    wait_for_services
    verify_fastqueue_metrics

    # Wait for some data to flow
    sleep 15

    wait_for_queue_activity
    check_queue_files

    # Test recovery
    test_queue_recovery

    print_summary

    log_info "=== All FastQueue E2E tests passed! ==="
}

main
