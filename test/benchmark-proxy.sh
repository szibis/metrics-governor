#!/bin/bash
# Benchmark: metrics-governor vs otel-collector vs vmagent
#
# Runs each proxy with identical flat-rate 100k dps generator load,
# collects CPU/memory/throughput samples after warmup.
#
# Generator uses stable mode: 100 metrics × 100 series = 10,000 series @ 10Hz
# All variability sources disabled for reproducible results.
#
# Results are saved to test/benchmark-results/benchmark-<timestamp>.txt
#
# Usage:
#   ./test/benchmark-proxy.sh              # Run all 3 tests
#   ./test/benchmark-proxy.sh --test=A     # Run only governor test
#   ./test/benchmark-proxy.sh --test=B     # Run only otel-collector test
#   ./test/benchmark-proxy.sh --test=C     # Run only vmagent test
#   ./test/benchmark-proxy.sh --warmup=90  # Custom warmup (seconds)
#   ./test/benchmark-proxy.sh --samples=5  # Custom sample count

set -e

# ── Configuration ───────────────────────────────────────────────
COMPOSE="docker compose"
COMPOSE_FILES="-f docker-compose.yaml -f compose_overrides/benchmark.yaml"
WARMUP=${WARMUP:-60}
SAMPLES=${SAMPLES:-3}
INTERVAL=${INTERVAL:-10}
NETWORK="metrics-governor_default"
RESULTS_DIR="test/benchmark-results"
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
RESULTS_FILE="${RESULTS_DIR}/benchmark-${TIMESTAMP}.txt"
RUN_TESTS="ABC"  # Default: run all tests

# ── Parse arguments ─────────────────────────────────────────────
for arg in "$@"; do
  case "$arg" in
    --test=*) RUN_TESTS="${arg#*=}" ;;
    --warmup=*) WARMUP="${arg#*=}" ;;
    --samples=*) SAMPLES="${arg#*=}" ;;
    --interval=*) INTERVAL="${arg#*=}" ;;
    *) echo "Unknown argument: $arg"; exit 1 ;;
  esac
done

# ── Result storage arrays ───────────────────────────────────────
declare -a TEST_NAMES=()
declare -a TEST_CPU=()
declare -a TEST_MEM=()
declare -a TEST_OTEL_CPU=()
declare -a TEST_OTEL_MEM=()
declare -a TEST_DPS=()

# ── Functions ───────────────────────────────────────────────────

log() { echo "[$(date +%H:%M:%S)] $*"; }

collect_stats() {
  local label=$1
  local proxy_container=$2

  log ""
  log "================================================================"
  log "  TEST: $label"
  log "================================================================"
  log "Warming up for ${WARMUP}s..."
  sleep "$WARMUP"

  local total_cpu=0 total_otel_cpu=0 total_dps=0
  local last_proxy_mem="N/A" last_otel_mem="N/A"
  local valid_samples=0

  log ""
  log "Collecting $SAMPLES samples at ${INTERVAL}s intervals..."
  for i in $(seq 1 "$SAMPLES"); do
    log ""
    log "--- Sample $i/$SAMPLES ---"

    # Generator rate (from Prometheus endpoint)
    local rate
    rate=$(curl -s http://localhost:9091/metrics 2>/dev/null \
      | grep "^generator_datapoints_per_second " \
      | awk '{printf "%.0f", $2}')
    log "Generator rate: ${rate:-N/A} dps"

    # Proxy CPU/memory
    local proxy_cpu=0
    if [ -n "$proxy_container" ]; then
      local stats
      stats=$(docker stats --no-stream --format "{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}" 2>/dev/null \
        | grep "$proxy_container" || true)
      if [ -n "$stats" ]; then
        proxy_cpu=$(echo "$stats" | awk -F'\t' '{gsub(/%/,"",$2); print $2}')
        last_proxy_mem=$(echo "$stats" | awk -F'\t' '{print $3}' | awk '{print $1}')
        log "Proxy:          ${proxy_cpu}% CPU, ${last_proxy_mem} RAM"
      fi
    fi

    # OTel collector stats (matches compose or standalone names)
    local otel_cpu=0
    local otel_stats
    otel_stats=$(docker stats --no-stream --format "{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}" 2>/dev/null \
      | grep -E "otel-collector|otel-bench" || true)
    if [ -n "$otel_stats" ]; then
      otel_cpu=$(echo "$otel_stats" | head -1 | awk -F'\t' '{gsub(/%/,"",$2); print $2}')
      last_otel_mem=$(echo "$otel_stats" | head -1 | awk -F'\t' '{print $3}' | awk '{print $1}')
      log "OTel collector: ${otel_cpu}% CPU, ${last_otel_mem} RAM"
    fi

    # VictoriaMetrics stats (informational)
    local vm_stats
    vm_stats=$(docker stats --no-stream --format "{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}" 2>/dev/null \
      | grep "victoriametrics" || true)
    if [ -n "$vm_stats" ]; then
      log "VictoriaMetrics: $(echo "$vm_stats" | awk -F'\t' '{print $2, "CPU,", $3}')"
    fi

    # Accumulate for averages
    if [ -n "$rate" ] && [ "$rate" -gt 0 ] 2>/dev/null; then
      total_dps=$((total_dps + rate))
      valid_samples=$((valid_samples + 1))
    fi
    total_cpu=$(echo "$total_cpu + ${proxy_cpu:-0}" | bc 2>/dev/null || echo "$total_cpu")
    total_otel_cpu=$(echo "$total_otel_cpu + ${otel_cpu:-0}" | bc 2>/dev/null || echo "$total_otel_cpu")

    [ "$i" -lt "$SAMPLES" ] && sleep "$INTERVAL"
  done

  # Calculate averages and store results
  local avg_cpu avg_otel_cpu avg_dps
  avg_cpu=$(echo "scale=1; $total_cpu / $SAMPLES" | bc 2>/dev/null || echo "0")
  avg_otel_cpu=$(echo "scale=1; $total_otel_cpu / $SAMPLES" | bc 2>/dev/null || echo "0")
  if [ "$valid_samples" -gt 0 ]; then
    avg_dps=$((total_dps / valid_samples))
  else
    avg_dps=0
  fi

  TEST_NAMES+=("$label")
  TEST_CPU+=("$avg_cpu")
  TEST_MEM+=("$last_proxy_mem")
  TEST_OTEL_CPU+=("$avg_otel_cpu")
  TEST_OTEL_MEM+=("$last_otel_mem")
  TEST_DPS+=("$avg_dps")

  log ""
  log "AVERAGES: proxy=${avg_cpu}% CPU, ${last_proxy_mem} RAM | otel=${avg_otel_cpu}% CPU | ${avg_dps} dps"
}

cleanup() {
  log "Stopping all containers..."
  $COMPOSE $COMPOSE_FILES down -v --remove-orphans 2>/dev/null || true
  docker rm -f otel-bench otel-vmagent vmagent-bench 2>/dev/null || true
}

print_summary() {
  cat <<'HEADER'

══════════════════════════════════════════════════════════════════════════════
  BENCHMARK SUMMARY — Flat 100k dps stable mode
HEADER
  echo "  $(date)"
  echo "══════════════════════════════════════════════════════════════════════════════"
  printf "%-30s %10s %10s %10s %10s %10s\n" \
    "Test" "Proxy CPU" "Proxy RAM" "OTel CPU" "OTel RAM" "Avg DPS"
  printf "%-30s %10s %10s %10s %10s %10s\n" \
    "──────────────────────────────" "─────────" "─────────" "────────" "────────" "───────"

  for i in "${!TEST_NAMES[@]}"; do
    local cpu="${TEST_CPU[$i]}"
    [ "$cpu" != "0" ] && [ "$cpu" != ".0" ] && cpu="${cpu}%"
    local otel_cpu="${TEST_OTEL_CPU[$i]}"
    [ "$otel_cpu" != "0" ] && [ "$otel_cpu" != ".0" ] && otel_cpu="${otel_cpu}%"
    printf "%-30s %10s %10s %10s %10s %10s\n" \
      "${TEST_NAMES[$i]}" \
      "$cpu" \
      "${TEST_MEM[$i]}" \
      "$otel_cpu" \
      "${TEST_OTEL_MEM[$i]}" \
      "${TEST_DPS[$i]}"
  done
  cat <<'FOOTER'
══════════════════════════════════════════════════════════════════════════════
FOOTER
  echo "Configuration:"
  echo "  Generator: stable mode, 100 metrics × 100 series = 10,000 series @ 10Hz"
  echo "  Warmup: ${WARMUP}s | Samples: ${SAMPLES} @ ${INTERVAL}s intervals"
  echo "  Governor: --profile=performance, no processing, no limits"
  echo "══════════════════════════════════════════════════════════════════════════════"
}

# ═══════════════════════════════════════════════════════════════
log "╔═══════════════════════════════════════════════════════════╗"
log "║     Proxy Benchmark: Governor vs Collector vs vmagent    ║"
log "║     Generator: 100k dps flat (stable mode)               ║"
log "╚═══════════════════════════════════════════════════════════╝"
log ""

# Ensure results directory exists
mkdir -p "$RESULTS_DIR"

# Build generator image once
log "Building generator image..."
$COMPOSE -f docker-compose.yaml build metrics-generator 2>&1 | tail -3

# ── TEST A: metrics-governor ──────────────────────────────────
if [[ "$RUN_TESTS" == *"A"* ]]; then
  log ""
  log "Starting TEST A: metrics-governor (performance profile, passthrough)..."
  cleanup
  $COMPOSE $COMPOSE_FILES up -d --build 2>&1 | tail -5
  collect_stats "metrics-governor" "metrics-governor"
  cleanup
fi

# ── TEST B: otel-collector direct ─────────────────────────────
if [[ "$RUN_TESTS" == *"B"* ]]; then
  log ""
  log "Starting TEST B: otel-collector direct export to VictoriaMetrics..."
  cleanup

  # Start VM + Grafana (uses benchmark overlay for resource limits)
  $COMPOSE $COMPOSE_FILES up -d victoriametrics grafana 2>&1 | tail -3
  sleep 5

  # Start otel-collector with direct-to-VM config
  # Named 'otel-collector' so the generator's OTLP_ENDPOINT resolves
  docker run -d --name otel-bench \
    --network "$NETWORK" \
    --network-alias otel-collector \
    -p 4317:4317 -p 4318:4318 -p 8888:8888 \
    --cpus="4" --memory="4g" \
    -v "$(pwd)/test/otel-collector-config-direct.yaml:/etc/otelcol/config.yaml:ro" \
    otel/opentelemetry-collector-contrib:0.144.0 \
    --config=/etc/otelcol/config.yaml 2>&1 | tail -1

  sleep 3

  # Start generator from compose (--no-deps skips otel-collector → governor chain)
  # Generator uses benchmark overlay's stable-mode env vars
  $COMPOSE $COMPOSE_FILES up -d --no-deps metrics-generator 2>&1 | tail -3

  collect_stats "otel-collector direct" "otel-bench"

  docker rm -f otel-bench 2>/dev/null || true
  $COMPOSE $COMPOSE_FILES down -v 2>/dev/null || true
fi

# ── TEST C: vmagent ───────────────────────────────────────────
if [[ "$RUN_TESTS" == *"C"* ]]; then
  log ""
  log "Starting TEST C: vmagent as OTLP proxy..."
  cleanup

  # Start VM + Grafana
  $COMPOSE $COMPOSE_FILES up -d victoriametrics grafana 2>&1 | tail -3
  sleep 5

  # Start vmagent (accepts OTLP via HTTP at /opentelemetry/... on httpListenAddr)
  docker run -d --name vmagent-bench \
    --network "$NETWORK" \
    -p 8429:8429 \
    --cpus="4" --memory="4g" \
    victoriametrics/vmagent:v1.134.0 \
    -httpListenAddr=:8429 \
    -remoteWrite.url=http://victoriametrics:8428/api/v1/write \
    -remoteWrite.maxDiskUsagePerURL=1GB \
    -remoteWrite.queues=8 2>&1 | tail -1

  # Start otel-collector pointing to vmagent
  # Named with network-alias 'otel-collector' for generator DNS resolution
  docker run -d --name otel-vmagent \
    --network "$NETWORK" \
    --network-alias otel-collector \
    -p 4317:4317 -p 4318:4318 -p 8888:8888 \
    --cpus="2" --memory="2g" \
    -v "$(pwd)/test/otel-collector-config-vmagent.yaml:/etc/otelcol/config.yaml:ro" \
    otel/opentelemetry-collector-contrib:0.144.0 \
    --config=/etc/otelcol/config.yaml 2>&1 | tail -1

  sleep 3

  # Start generator from compose (--no-deps)
  $COMPOSE $COMPOSE_FILES up -d --no-deps metrics-generator 2>&1 | tail -3

  collect_stats "vmagent (OTLP→remote write)" "vmagent-bench"

  docker rm -f vmagent-bench otel-vmagent 2>/dev/null || true
  $COMPOSE $COMPOSE_FILES down -v 2>/dev/null || true
fi

# ── Summary ───────────────────────────────────────────────────
print_summary | tee "$RESULTS_FILE"

log ""
log "Results saved to: $RESULTS_FILE"
log ""
log "╔═══════════════════════════════════════════════════════════╗"
log "║                   Benchmark Complete                     ║"
log "╚═══════════════════════════════════════════════════════════╝"
