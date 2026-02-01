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

# Extended stability testing configuration
STABILITY_TEST_DURATION=${STABILITY_TEST_DURATION:-60}  # seconds for stability tests
MEMORY_GROWTH_THRESHOLD=50  # max MB memory growth allowed during stability test
GOROUTINE_GROWTH_THRESHOLD=100  # max goroutine growth allowed
EXTENDED_MODE=${EXTENDED_MODE:-false}  # set to true for extended testing

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
        # Queue only receives items when exports fail - zero pushes means exports are succeeding
        log_info "No queue pushes recorded (expected when exports are successful)"
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
    local ingestion_rate=$(echo "$verifier_metrics" | grep "^verifier_last_ingestion_rate_percent" | awk '{print $2}')
    local vm_counter=$(echo "$verifier_metrics" | grep "^verifier_vm_verification_counter" | awk '{print $2}' | cut -d'.' -f1)
    local mg_received=$(echo "$verifier_metrics" | grep "^verifier_mg_datapoints_received" | awk '{print $2}' | cut -d'.' -f1)
    local mg_sent=$(echo "$verifier_metrics" | grep "^verifier_mg_datapoints_sent" | awk '{print $2}' | cut -d'.' -f1)

    checks_total=${checks_total:-0}
    checks_passed=${checks_passed:-0}
    last_status=${last_status:-0}
    ingestion_rate=${ingestion_rate:-0}
    vm_counter=${vm_counter:-0}
    mg_received=${mg_received:-0}
    mg_sent=${mg_sent:-0}

    log_info "Verifier: $checks_passed/$checks_total checks passed, last status: $last_status"
    log_info "Verifier details: VM counter=$vm_counter, MG received=$mg_received, MG sent=$mg_sent, Ingestion rate=$ingestion_rate"

    if [ "$checks_total" -gt 0 ]; then
        local pass_rate=$((checks_passed * 100 / checks_total))
        if [ $pass_rate -lt 80 ]; then
            # Provide more context on why verification might be failing
            if [ "$vm_counter" -eq 0 ]; then
                log_warn "Verifier pass rate low (${pass_rate}%): verification counter not found in VictoriaMetrics"
            elif [ "$mg_received" -eq 0 ]; then
                log_warn "Verifier pass rate low (${pass_rate}%): no datapoints_received metrics (check stats collector)"
            else
                log_warn "Verifier pass rate is low: ${pass_rate}%"
            fi
        else
            log_info "Verifier pass rate: ${pass_rate}%"
        fi
    fi

    log_info "Verifier check completed"
}

# Check Bloom filter cardinality tracking
check_bloom_filter() {
    log_info "Checking Bloom filter cardinality tracking..."

    local metrics=$(curl -s http://localhost:9090/metrics 2>/dev/null)

    # Check cardinality mode
    local bloom_mode=$(echo "$metrics" | grep 'metrics_governor_cardinality_mode{mode="bloom"}' | awk '{print $2}' | cut -d'.' -f1)
    local exact_mode=$(echo "$metrics" | grep 'metrics_governor_cardinality_mode{mode="exact"}' | awk '{print $2}' | cut -d'.' -f1)

    bloom_mode=${bloom_mode:-0}
    exact_mode=${exact_mode:-0}

    if [ "$bloom_mode" -eq 1 ]; then
        log_info "Bloom filter mode is active (memory-efficient)"
    elif [ "$exact_mode" -eq 1 ]; then
        log_info "Exact mode is active (higher memory usage)"
    else
        log_warn "Cardinality mode metrics not found"
    fi

    # Check cardinality memory usage
    local stats_memory=$(echo "$metrics" | grep "^metrics_governor_cardinality_memory_bytes " | awk '{print $2}' | cut -d'.' -f1)
    local limits_memory=$(echo "$metrics" | grep "^metrics_governor_limits_cardinality_memory_bytes " | awk '{print $2}' | cut -d'.' -f1)
    local trackers=$(echo "$metrics" | grep "^metrics_governor_cardinality_trackers_total " | awk '{print $2}' | cut -d'.' -f1)

    stats_memory=${stats_memory:-0}
    limits_memory=${limits_memory:-0}
    trackers=${trackers:-0}

    local total_memory=$((stats_memory + limits_memory))
    local memory_mb=$((total_memory / 1024 / 1024))

    log_info "Cardinality tracking: ${trackers} trackers, ${memory_mb}MB memory used"

    # Check metric cardinality is being tracked
    local metric_cardinality=$(echo "$metrics" | grep "^metrics_governor_metric_cardinality{" | wc -l | tr -d ' ')
    log_info "Tracking cardinality for $metric_cardinality unique metrics"

    if [ "$metric_cardinality" -eq 0 ]; then
        log_warn "No metric cardinality data found (stats may not be enabled)"
    fi

    log_info "Bloom filter check completed"
}

# Check limits enforcement
check_limits_enforcement() {
    log_info "Checking limits enforcement..."

    local metrics=$(curl -s http://localhost:9090/metrics 2>/dev/null)

    # Check if limits are being evaluated
    local passed=$(echo "$metrics" | grep "^metrics_governor_limit_datapoints_passed_total" | awk '{sum+=$2} END {print int(sum)}')
    local dropped=$(echo "$metrics" | grep "^metrics_governor_limit_datapoints_dropped_total" | awk '{sum+=$2} END {print int(sum)}')
    local exceeded=$(echo "$metrics" | grep "^metrics_governor_limit_cardinality_exceeded_total" | awk '{sum+=$2} END {print int(sum)}')

    passed=${passed:-0}
    dropped=${dropped:-0}
    exceeded=${exceeded:-0}

    log_info "Limits: $passed passed, $dropped dropped, $exceeded cardinality exceeded"

    # Check per-rule metrics
    local rules=$(echo "$metrics" | grep "^metrics_governor_rule_current_datapoints{" | wc -l | tr -d ' ')
    log_info "Active limit rules: $rules"

    # Check if dry-run mode
    if [ "$dropped" -eq 0 ] && [ "$passed" -gt 0 ]; then
        log_info "Limits in dry-run mode (no drops) or all within limits"
    fi

    log_info "Limits enforcement check completed"
}

# Memory stability test - detect potential memory leaks
check_memory_stability() {
    log_info "Running memory stability test (${STABILITY_TEST_DURATION}s)..."

    local container_name="metrics-governor-metrics-governor-1"

    # Get initial memory
    local initial_mem=$(docker stats --no-stream --format "{{.MemUsage}}" "$container_name" 2>/dev/null | cut -d'/' -f1 | tr -d ' ' | sed 's/MiB//' | sed 's/GiB/*1024/' | bc 2>/dev/null || echo "0")
    local initial_goroutines=$(curl -s http://localhost:9090/metrics 2>/dev/null | grep "^metrics_governor_goroutines " | awk '{print $2}' | cut -d'.' -f1)

    initial_mem=${initial_mem:-0}
    initial_goroutines=${initial_goroutines:-0}

    log_info "Initial: ${initial_mem}MB memory, ${initial_goroutines} goroutines"

    # Sample memory over time
    local samples=5
    local interval=$((STABILITY_TEST_DURATION / samples))
    local max_mem=$initial_mem
    local max_goroutines=$initial_goroutines

    for i in $(seq 1 $samples); do
        sleep $interval

        local current_mem=$(docker stats --no-stream --format "{{.MemUsage}}" "$container_name" 2>/dev/null | cut -d'/' -f1 | tr -d ' ' | sed 's/MiB//' | sed 's/GiB/*1024/' | bc 2>/dev/null || echo "0")
        local current_goroutines=$(curl -s http://localhost:9090/metrics 2>/dev/null | grep "^metrics_governor_goroutines " | awk '{print $2}' | cut -d'.' -f1)

        current_mem=${current_mem:-0}
        current_goroutines=${current_goroutines:-0}

        log_info "Sample $i/$samples: ${current_mem}MB memory, ${current_goroutines} goroutines"

        if [ "$current_mem" -gt "$max_mem" ]; then
            max_mem=$current_mem
        fi
        if [ "$current_goroutines" -gt "$max_goroutines" ]; then
            max_goroutines=$current_goroutines
        fi
    done

    # Calculate growth
    local mem_growth=$((max_mem - initial_mem))
    local goroutine_growth=$((max_goroutines - initial_goroutines))

    log_info "Memory growth: ${mem_growth}MB (threshold: ${MEMORY_GROWTH_THRESHOLD}MB)"
    log_info "Goroutine growth: ${goroutine_growth} (threshold: ${GOROUTINE_GROWTH_THRESHOLD})"

    # Check for potential leaks
    if [ "$mem_growth" -gt "$MEMORY_GROWTH_THRESHOLD" ]; then
        log_warn "Potential memory leak detected: ${mem_growth}MB growth exceeds threshold"

        # Get heap profile if available
        local heap_alloc=$(curl -s http://localhost:9090/metrics 2>/dev/null | grep "^metrics_governor_memory_heap_alloc_bytes " | awk '{print $2}')
        local heap_objects=$(curl -s http://localhost:9090/metrics 2>/dev/null | grep "^metrics_governor_memory_heap_objects " | awk '{print $2}')
        log_info "Heap details: alloc=${heap_alloc} bytes, objects=${heap_objects}"
    fi

    if [ "$goroutine_growth" -gt "$GOROUTINE_GROWTH_THRESHOLD" ]; then
        log_warn "Potential goroutine leak detected: ${goroutine_growth} growth exceeds threshold"
    fi

    log_info "Memory stability test completed"
}

# Check GC and runtime health
check_runtime_health() {
    log_info "Checking runtime health..."

    local metrics=$(curl -s http://localhost:9090/metrics 2>/dev/null)

    # GC metrics
    local gc_cycles=$(echo "$metrics" | grep "^metrics_governor_gc_cycles_total " | awk '{print $2}' | cut -d'.' -f1)
    local gc_pause=$(echo "$metrics" | grep "^metrics_governor_gc_pause_total_seconds " | awk '{print $2}')
    local gc_cpu=$(echo "$metrics" | grep "^metrics_governor_gc_cpu_percent " | awk '{print $2}')

    gc_cycles=${gc_cycles:-0}
    gc_pause=${gc_pause:-0}
    gc_cpu=${gc_cpu:-0}

    log_info "GC: $gc_cycles cycles, ${gc_pause}s total pause, ${gc_cpu}% CPU"

    # Check for excessive GC
    if [ "$(echo "$gc_cpu > 10" | bc 2>/dev/null)" = "1" ]; then
        log_warn "High GC CPU usage: ${gc_cpu}% (may indicate memory pressure)"
    fi

    # Goroutines and threads
    local goroutines=$(echo "$metrics" | grep "^metrics_governor_goroutines " | awk '{print $2}' | cut -d'.' -f1)
    local threads=$(echo "$metrics" | grep "^metrics_governor_go_threads " | awk '{print $2}' | cut -d'.' -f1)

    goroutines=${goroutines:-0}
    threads=${threads:-0}

    log_info "Goroutines: $goroutines, OS threads: $threads"

    # Check for goroutine explosion
    if [ "$goroutines" -gt 1000 ]; then
        log_warn "High goroutine count: $goroutines (potential goroutine leak)"
    fi

    log_info "Runtime health check completed"
}

# Check throughput and performance
check_performance() {
    log_info "Checking performance metrics..."

    local metrics=$(curl -s http://localhost:9090/metrics 2>/dev/null)

    # Get current counters
    local datapoints=$(echo "$metrics" | grep "^metrics_governor_datapoints_total " | awk '{print $2}' | cut -d'.' -f1)
    local batches=$(echo "$metrics" | grep "^metrics_governor_batches_sent_total " | awk '{print $2}' | cut -d'.' -f1)

    datapoints=${datapoints:-0}
    batches=${batches:-0}

    # Wait and measure rate
    sleep 10

    local metrics2=$(curl -s http://localhost:9090/metrics 2>/dev/null)
    local datapoints2=$(echo "$metrics2" | grep "^metrics_governor_datapoints_total " | awk '{print $2}' | cut -d'.' -f1)
    local batches2=$(echo "$metrics2" | grep "^metrics_governor_batches_sent_total " | awk '{print $2}' | cut -d'.' -f1)

    datapoints2=${datapoints2:-0}
    batches2=${batches2:-0}

    local dp_rate=$(((datapoints2 - datapoints) / 10))
    local batch_rate=$(((batches2 - batches) / 10))
    local avg_batch_size=0
    if [ "$batch_rate" -gt 0 ]; then
        avg_batch_size=$((dp_rate / batch_rate))
    fi

    log_info "Throughput: ${dp_rate} datapoints/sec, ${batch_rate} batches/sec"
    log_info "Average batch size: $avg_batch_size datapoints"

    # Check buffer utilization
    local buffer_size=$(echo "$metrics2" | grep "^metrics_governor_buffer_size " | awk '{print $2}' | cut -d'.' -f1)
    buffer_size=${buffer_size:-0}
    log_info "Current buffer size: $buffer_size"

    log_info "Performance check completed"
}

# Extended stability test for longer-running scenarios
run_extended_stability_test() {
    if [ "$EXTENDED_MODE" != "true" ]; then
        log_info "Extended stability test skipped (set EXTENDED_MODE=true to enable)"
        return 0
    fi

    log_info "=== Running Extended Stability Test (5 minutes) ==="

    local duration=300  # 5 minutes
    local interval=30   # check every 30 seconds
    local checks=$((duration / interval))

    # Arrays to store samples (using temp files for bash compatibility)
    local mem_file=$(mktemp)
    local goroutine_file=$(mktemp)

    local container_name="metrics-governor-metrics-governor-1"

    for i in $(seq 1 $checks); do
        local elapsed=$((i * interval))

        # Get memory
        local mem=$(docker stats --no-stream --format "{{.MemUsage}}" "$container_name" 2>/dev/null | cut -d'/' -f1 | tr -d ' ' | sed 's/MiB//' | sed 's/GiB/*1024/' | bc 2>/dev/null || echo "0")

        # Get goroutines
        local goroutines=$(curl -s http://localhost:9090/metrics 2>/dev/null | grep "^metrics_governor_goroutines " | awk '{print $2}' | cut -d'.' -f1)

        # Get datapoints
        local datapoints=$(curl -s http://localhost:9090/metrics 2>/dev/null | grep "^metrics_governor_datapoints_total " | awk '{print $2}' | cut -d'.' -f1)

        # Get errors
        local errors=$(curl -s http://localhost:9090/metrics 2>/dev/null | grep "^metrics_governor_export_errors_total " | awk '{print $2}' | cut -d'.' -f1)

        log_info "[$elapsed/${duration}s] Memory: ${mem}MB, Goroutines: ${goroutines}, Datapoints: ${datapoints}, Errors: ${errors}"

        echo "$mem" >> "$mem_file"
        echo "$goroutines" >> "$goroutine_file"

        # Check for errors
        if [ "${errors:-0}" -gt 0 ]; then
            log_warn "Export errors detected during stability test"
        fi

        # Check for OOM
        local oom_killed=$(docker inspect --format='{{.State.OOMKilled}}' "$container_name" 2>/dev/null || echo "false")
        if [ "$oom_killed" = "true" ]; then
            log_error "OOM killed during extended stability test!"
            rm -f "$mem_file" "$goroutine_file"
            return 1
        fi

        sleep $interval
    done

    # Analyze trends
    local first_mem=$(head -1 "$mem_file")
    local last_mem=$(tail -1 "$mem_file")
    local first_gor=$(head -1 "$goroutine_file")
    local last_gor=$(tail -1 "$goroutine_file")

    local mem_trend=$((last_mem - first_mem))
    local gor_trend=$((last_gor - first_gor))

    log_info "=== Extended Stability Summary ==="
    log_info "Memory trend: ${first_mem}MB -> ${last_mem}MB (${mem_trend}MB change)"
    log_info "Goroutine trend: ${first_gor} -> ${last_gor} (${gor_trend} change)"

    # Check for concerning trends
    if [ "$mem_trend" -gt 100 ]; then
        log_warn "Significant memory growth detected over 5 minutes: ${mem_trend}MB"
    else
        log_info "Memory stable during extended test"
    fi

    if [ "$gor_trend" -gt 200 ]; then
        log_warn "Significant goroutine growth detected: ${gor_trend}"
    else
        log_info "Goroutines stable during extended test"
    fi

    rm -f "$mem_file" "$goroutine_file"
    log_info "Extended stability test completed"
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

    log_info "Cardinality tracking metrics:"
    curl -s http://localhost:9090/metrics 2>/dev/null | grep -E "^metrics_governor_cardinality_(mode|memory_bytes|trackers_total)" | head -5
    echo ""

    log_info "Limits metrics:"
    curl -s http://localhost:9090/metrics 2>/dev/null | grep -E "^metrics_governor_limit_(datapoints_passed|datapoints_dropped)" | head -5
    echo ""

    log_info "Runtime metrics:"
    curl -s http://localhost:9090/metrics 2>/dev/null | grep -E "^metrics_governor_(goroutines|gc_cycles_total|memory_heap_alloc_bytes)" | head -5
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

    # Feature-specific checks
    check_bloom_filter
    check_limits_enforcement
    check_runtime_health
    check_performance

    # Stability checks
    check_memory_stability
    check_restart_loops  # Check again after load
    check_oom            # Check again after load

    # Optional extended testing
    run_extended_stability_test

    print_summary

    log_info "=== All E2E tests passed! ==="
}

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --skip-build)
            build_images() { log_info "Skipping build (--skip-build)"; }
            shift
            ;;
        --extended)
            EXTENDED_MODE=true
            log_info "Extended stability testing enabled"
            shift
            ;;
        --stability-duration)
            STABILITY_TEST_DURATION="$2"
            log_info "Stability test duration: ${STABILITY_TEST_DURATION}s"
            shift 2
            ;;
        --help)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --skip-build           Skip Docker image build"
            echo "  --extended             Enable extended stability testing (5 min)"
            echo "  --stability-duration N Set stability test duration in seconds"
            echo "  --help                 Show this help message"
            echo ""
            echo "Environment variables:"
            echo "  STABILITY_TEST_DURATION  Duration for stability tests (default: 60)"
            echo "  EXTENDED_MODE            Set to 'true' for extended testing"
            echo "  MEMORY_GROWTH_THRESHOLD  Max MB growth allowed (default: 50)"
            echo "  GOROUTINE_GROWTH_THRESHOLD Max goroutine growth (default: 100)"
            exit 0
            ;;
        *)
            log_error "Unknown option: $1"
            exit 1
            ;;
    esac
done

main
