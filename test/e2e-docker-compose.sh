#!/bin/bash
set -e

# Docker Compose E2E Test Script
# Verifies the full metrics-governor stack is working correctly

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
cd "$PROJECT_DIR"

# Configuration
STARTUP_TIMEOUT=120        # seconds to wait for services to start
VERIFICATION_TIMEOUT=180   # seconds to run verification checks
MIN_DATAPOINTS=1000       # minimum datapoints that should flow through
MIN_VERIFICATION_PASSES=3 # minimum consecutive verification passes needed
MEMORY_LIMIT_MB=512       # expected memory limit for metrics-governor

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

cleanup() {
    log_info "Cleaning up..."
    docker compose down -v --remove-orphans 2>/dev/null || true
}

trap cleanup EXIT

# Check if Docker is available
check_docker() {
    if ! command -v docker &> /dev/null; then
        log_error "Docker is not installed"
        exit 1
    fi
    if ! docker info &> /dev/null; then
        log_error "Docker is not running"
        exit 1
    fi
    log_info "Docker is available"
}

# Get version for build
get_version() {
    if [ -n "$BUILD_VERSION" ]; then
        echo "$BUILD_VERSION"
    elif git describe --tags --exact-match 2>/dev/null; then
        git describe --tags --exact-match
    else
        local chart_version=$(grep '^version:' helm/metrics-governor/Chart.yaml | awk '{print $2}')
        local short_sha=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
        echo "${chart_version}-${short_sha}"
    fi
}

# Build all images from scratch
build_images() {
    local version=$(get_version)
    log_info "Building Docker images (no cache) - version: $version"
    docker compose build --no-cache --build-arg VERSION="$version" 2>&1 | tail -20
    if [ $? -ne 0 ]; then
        log_error "Failed to build Docker images"
        exit 1
    fi
    log_info "Docker images built successfully - version: $version"
}

# Start the stack
start_stack() {
    log_info "Starting Docker Compose stack..."
    docker compose up -d
    if [ $? -ne 0 ]; then
        log_error "Failed to start Docker Compose stack"
        exit 1
    fi
    log_info "Docker Compose stack started"
}

# Wait for services to be healthy
wait_for_services() {
    log_info "Waiting for services to be healthy (timeout: ${STARTUP_TIMEOUT}s)..."

    local services=("metrics-governor" "victoriametrics" "otel-collector" "metrics-generator" "verifier")
    local start_time=$(date +%s)

    while true; do
        local all_healthy=true
        local current_time=$(date +%s)
        local elapsed=$((current_time - start_time))

        if [ $elapsed -gt $STARTUP_TIMEOUT ]; then
            log_error "Timeout waiting for services to be healthy"
            docker compose ps
            docker compose logs --tail=50
            exit 1
        fi

        for service in "${services[@]}"; do
            local container_name="metrics-governor-${service}-1"
            local status=$(docker inspect --format='{{.State.Status}}' "$container_name" 2>/dev/null || echo "not found")

            if [ "$status" != "running" ]; then
                all_healthy=false
                break
            fi
        done

        if $all_healthy; then
            log_info "All services are running"
            break
        fi

        echo -n "."
        sleep 2
    done
}

# Check for restart loops
check_restart_loops() {
    log_info "Checking for restart loops..."

    local container_name="metrics-governor-metrics-governor-1"
    local restart_count=$(docker inspect --format='{{.RestartCount}}' "$container_name" 2>/dev/null || echo "0")
    local status=$(docker inspect --format='{{.State.Status}}' "$container_name" 2>/dev/null || echo "unknown")

    log_info "Container status: $status, restart count: $restart_count"

    if [ "$restart_count" -gt 3 ]; then
        log_error "metrics-governor has restarted $restart_count times (possible crash loop)"
        docker logs "$container_name" --tail=100
        exit 1
    fi

    if [ "$status" = "restarting" ]; then
        log_error "metrics-governor is in restarting state"
        docker logs "$container_name" --tail=100
        exit 1
    fi

    log_info "No restart loops detected"
}

# Check for OOM issues
check_oom() {
    log_info "Checking for OOM issues..."

    local container_name="metrics-governor-metrics-governor-1"
    local oom_killed=$(docker inspect --format='{{.State.OOMKilled}}' "$container_name" 2>/dev/null || echo "false")

    if [ "$oom_killed" = "true" ]; then
        log_error "metrics-governor was OOM killed"
        exit 1
    fi

    # Check memory usage
    local mem_usage=$(docker stats --no-stream --format "{{.MemUsage}}" "$container_name" 2>/dev/null | cut -d'/' -f1 | tr -d ' ')
    log_info "Current memory usage: $mem_usage"

    log_info "No OOM issues detected"
}

# Check metrics endpoint is responding
check_metrics_endpoint() {
    log_info "Checking metrics endpoint..."

    local response=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:9090/metrics 2>/dev/null || echo "000")

    if [ "$response" != "200" ]; then
        log_error "Metrics endpoint not responding (HTTP $response)"
        exit 1
    fi

    # Check for essential metrics
    local metrics_output=$(curl -s http://localhost:9090/metrics 2>/dev/null)

    if ! echo "$metrics_output" | grep -q "metrics_governor_datapoints_total"; then
        log_error "Essential metric 'metrics_governor_datapoints_total' not found"
        exit 1
    fi

    log_info "Metrics endpoint is healthy"
}

# Check and display running version
check_version() {
    log_info "Checking running version..."

    local container_name="metrics-governor-metrics-governor-1"

    # Try to get version from container
    local running_version=$(docker exec "$container_name" /app/metrics-governor -version 2>/dev/null | head -1 || echo "unknown")

    log_info "Running version: $running_version"
}

# Verify data is flowing
verify_data_flow() {
    log_info "Verifying data flow (timeout: ${VERIFICATION_TIMEOUT}s)..."

    local start_time=$(date +%s)
    local consecutive_passes=0

    while true; do
        local current_time=$(date +%s)
        local elapsed=$((current_time - start_time))

        if [ $elapsed -gt $VERIFICATION_TIMEOUT ]; then
            log_error "Timeout waiting for data to flow"
            exit 1
        fi

        # Get metrics from metrics-governor
        local datapoints=$(curl -s http://localhost:9090/metrics 2>/dev/null | grep "^metrics_governor_datapoints_total" | awk '{print $2}' | cut -d'.' -f1)
        local batches=$(curl -s http://localhost:9090/metrics 2>/dev/null | grep "^metrics_governor_batches_sent_total" | awk '{print $2}' | cut -d'.' -f1)
        local errors=$(curl -s http://localhost:9090/metrics 2>/dev/null | grep "^metrics_governor_export_errors_total" | awk '{print $2}' | cut -d'.' -f1)

        datapoints=${datapoints:-0}
        batches=${batches:-0}
        errors=${errors:-0}

        log_info "Datapoints: $datapoints, Batches: $batches, Errors: $errors"

        # Check VictoriaMetrics
        local vm_series=$(curl -s 'http://localhost:8428/api/v1/status/tsdb' 2>/dev/null | jq -r '.data.totalSeries // 0')
        log_info "VictoriaMetrics total series: $vm_series"

        # Verify data is flowing
        if [ "$datapoints" -ge "$MIN_DATAPOINTS" ] && [ "$batches" -gt 0 ] && [ "$errors" -eq 0 ] && [ "$vm_series" -gt 0 ]; then
            consecutive_passes=$((consecutive_passes + 1))
            log_info "Verification pass $consecutive_passes/$MIN_VERIFICATION_PASSES"

            if [ $consecutive_passes -ge $MIN_VERIFICATION_PASSES ]; then
                log_info "Data flow verified successfully!"
                return 0
            fi
        else
            consecutive_passes=0
        fi

        sleep 5
    done
}

# Check queue metrics (FastQueue)
check_queue() {
    log_info "Checking FastQueue metrics..."

    local queue_metrics=$(curl -s http://localhost:9090/metrics 2>/dev/null)

    # Check basic queue metrics
    local queue_size=$(echo "$queue_metrics" | grep "^metrics_governor_queue_size " | awk '{print $2}' | cut -d'.' -f1)
    local queue_push=$(echo "$queue_metrics" | grep "^metrics_governor_queue_push_total " | awk '{print $2}' | cut -d'.' -f1)
    local queue_bytes=$(echo "$queue_metrics" | grep "^metrics_governor_queue_bytes " | awk '{print $2}' | cut -d'.' -f1)

    # Check FastQueue-specific metrics
    local inmemory_blocks=$(echo "$queue_metrics" | grep "^metrics_governor_fastqueue_inmemory_blocks " | awk '{print $2}' | cut -d'.' -f1)
    local disk_bytes=$(echo "$queue_metrics" | grep "^metrics_governor_fastqueue_disk_bytes " | awk '{print $2}' | cut -d'.' -f1)
    local meta_sync=$(echo "$queue_metrics" | grep "^metrics_governor_fastqueue_meta_sync_total " | awk '{print $2}' | cut -d'.' -f1)
    local chunk_rotations=$(echo "$queue_metrics" | grep "^metrics_governor_fastqueue_chunk_rotations " | awk '{print $2}' | cut -d'.' -f1)
    local inmemory_flushes=$(echo "$queue_metrics" | grep "^metrics_governor_fastqueue_inmemory_flushes " | awk '{print $2}' | cut -d'.' -f1)

    queue_size=${queue_size:-0}
    queue_push=${queue_push:-0}
    queue_bytes=${queue_bytes:-0}
    inmemory_blocks=${inmemory_blocks:-0}
    disk_bytes=${disk_bytes:-0}
    meta_sync=${meta_sync:-0}
    chunk_rotations=${chunk_rotations:-0}
    inmemory_flushes=${inmemory_flushes:-0}

    log_info "Queue: size=$queue_size, push_total=$queue_push, bytes=$queue_bytes"
    log_info "FastQueue: inmemory_blocks=$inmemory_blocks, disk_bytes=$disk_bytes"
    log_info "FastQueue ops: meta_syncs=$meta_sync, chunk_rotations=$chunk_rotations, flushes=$inmemory_flushes"

    if [ "$queue_push" -gt 0 ]; then
        log_info "Queue is operational (pushes recorded)"
    else
        log_warn "No queue pushes recorded (queue may not be enabled)"
    fi

    if [ "$meta_sync" -gt 0 ]; then
        log_info "FastQueue metadata syncs occurring (persistence working)"
    fi

    log_info "Queue check completed"
}

# Check verifier results
check_verifier() {
    log_info "Checking verifier results..."

    local verifier_metrics=$(curl -s http://localhost:9092/metrics 2>/dev/null)

    if [ -z "$verifier_metrics" ]; then
        log_warn "Verifier metrics not available"
        return 0
    fi

    local checks_total=$(echo "$verifier_metrics" | grep "^verifier_checks_total" | awk '{print $2}' | cut -d'.' -f1)
    local checks_passed=$(echo "$verifier_metrics" | grep "^verifier_checks_passed_total" | awk '{print $2}' | cut -d'.' -f1)
    local last_status=$(echo "$verifier_metrics" | grep "^verifier_last_check_status" | awk '{print $2}' | cut -d'.' -f1)

    checks_total=${checks_total:-0}
    checks_passed=${checks_passed:-0}
    last_status=${last_status:-0}

    log_info "Verifier: $checks_passed/$checks_total checks passed, last status: $last_status"

    if [ "$checks_total" -gt 0 ]; then
        local pass_rate=$((checks_passed * 100 / checks_total))
        if [ $pass_rate -lt 80 ]; then
            log_warn "Verifier pass rate is low: ${pass_rate}%"
        fi
    fi

    log_info "Verifier check completed"
}

# Print final summary
print_summary() {
    local version=$(get_version)
    log_info "=== E2E Test Summary ==="
    log_info "Version: $version"

    echo ""
    docker compose ps
    echo ""

    log_info "Final metrics:"
    curl -s http://localhost:9090/metrics 2>/dev/null | grep -E "^metrics_governor_(datapoints_total|batches_sent|export_errors|goroutines|memory)" | head -10
    echo ""

    log_info "Queue metrics:"
    curl -s http://localhost:9090/metrics 2>/dev/null | grep -E "^metrics_governor_queue_(size|push_total|bytes|retry)" | head -10
    echo ""

    log_info "FastQueue metrics:"
    curl -s http://localhost:9090/metrics 2>/dev/null | grep -E "^metrics_governor_fastqueue_" | head -10
    echo ""

    log_info "VictoriaMetrics status:"
    curl -s 'http://localhost:8428/api/v1/status/tsdb' 2>/dev/null | jq -r '.data | "Total series: \(.totalSeries)"'
    echo ""
}

# Main execution
main() {
    log_info "Starting Docker Compose E2E tests..."
    echo ""

    check_docker
    cleanup  # Clean up any previous runs
    build_images
    start_stack

    # Wait a bit for services to initialize
    sleep 10

    wait_for_services
    check_restart_loops
    check_oom
    check_metrics_endpoint
    check_version

    # Wait for data to start flowing
    sleep 20

    verify_data_flow
    check_queue
    check_verifier
    check_restart_loops  # Check again after load
    check_oom            # Check again after load

    print_summary

    log_info "=== All E2E tests passed! ==="
}

# Run with optional arguments
if [ "$1" = "--skip-build" ]; then
    build_images() { log_info "Skipping build (--skip-build)"; }
fi

main
