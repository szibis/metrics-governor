#!/bin/bash
# Benchmark: metrics-governor vs otel-collector vs vmagent
#
# All proxies receive OTLP HTTP from the generator, export to VictoriaMetrics.
# Tests both OTLP HTTP and Prometheus Remote Write export paths.
#
# Generator uses stable mode: 100 metrics × 100 series = 10,000 series @ 10Hz
# All variability sources disabled for reproducible results.
#
# Test matrix (generator → proxy → VM):
#   A  generator →  governor      → VM  (OTLP HTTP export)
#   B  generator →  governor      → VM  (PRW export)
#   C  generator →  otel-collector → VM  (OTLP HTTP export)
#   D  generator →  otel-collector → VM  (PRW export)
#   E  generator →  vmagent       → VM  (PRW export)
#
# Results are saved to test/benchmark-results/benchmark-<timestamp>.txt
#
# Usage:
#   ./test/benchmark-proxy.sh              # Run all 5 tests
#   ./test/benchmark-proxy.sh --test=A     # Run only governor OTLP test
#   ./test/benchmark-proxy.sh --test=ACE   # Run governor OTLP, collector OTLP, vmagent
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
RUN_TESTS="ABCDE"  # Default: run all tests

# Container images
OTEL_IMAGE="otel/opentelemetry-collector-contrib:0.144.0"
VMAGENT_IMAGE="victoriametrics/vmagent:v1.134.0"

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

# ── Per-test result storage ─────────────────────────────────────
declare -a R_LABEL=()       # Test label
declare -a R_TAG=()         # Short tag (A/B/C/D/E)
declare -a R_PROXY_CPU=()   # Proxy avg CPU %
declare -a R_PROXY_MEM=()   # Proxy RAM (last sample)
declare -a R_VM_CPU=()      # VictoriaMetrics avg CPU %
declare -a R_VM_MEM=()      # VictoriaMetrics RAM (last sample)
declare -a R_DPS=()         # Avg datapoints/sec

# ── Functions ───────────────────────────────────────────────────

log() { echo "[$(date +%H:%M:%S)] $*"; }

# get_container_stat <grep-pattern> -> sets _cpu and _mem
get_container_stat() {
  local pattern=$1
  _cpu="0" _mem="—"
  local line
  line=$(docker stats --no-stream --format "{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}" 2>/dev/null \
    | grep -E "$pattern" | head -1 || true)
  if [ -n "$line" ]; then
    _cpu=$(echo "$line" | awk -F'\t' '{gsub(/%/,"",$2); print $2}')
    _mem=$(echo "$line" | awk -F'\t' '{print $3}' | awk '{print $1}')
  fi
}

collect_stats() {
  local label=$1 tag=$2 proxy_pattern=$3

  log ""
  log "================================================================"
  log "  TEST $tag: $label"
  log "================================================================"
  log "Warming up for ${WARMUP}s..."
  sleep "$WARMUP"

  local sum_proxy_cpu=0 sum_vm_cpu=0 sum_dps=0
  local last_proxy_mem="—" last_vm_mem="—"
  local valid_dps=0

  log ""
  log "Collecting $SAMPLES samples at ${INTERVAL}s intervals..."
  for i in $(seq 1 "$SAMPLES"); do
    log ""
    log "--- Sample $i/$SAMPLES ---"

    # Generator rate
    local rate
    rate=$(curl -s http://localhost:9091/metrics 2>/dev/null \
      | grep "^generator_datapoints_per_second " \
      | awk '{printf "%.0f", $2}')
    log "  Generator:       ${rate:-0} dps"

    # Proxy stats
    local proxy_cpu=0 proxy_mem="—"
    if [ -n "$proxy_pattern" ]; then
      get_container_stat "$proxy_pattern"
      proxy_cpu=$_cpu; proxy_mem=$_mem; last_proxy_mem=$_mem
      log "  Proxy:           ${proxy_cpu}% CPU  ${proxy_mem} RAM"
    fi

    # VictoriaMetrics stats
    get_container_stat "victoriametrics"
    local vm_cpu=$_cpu; local vm_mem=$_mem; last_vm_mem=$_mem
    log "  VictoriaMetrics: ${vm_cpu}% CPU  ${vm_mem} RAM"

    # Accumulate
    sum_proxy_cpu=$(echo "$sum_proxy_cpu + $proxy_cpu" | bc 2>/dev/null || echo "$sum_proxy_cpu")
    sum_vm_cpu=$(echo "$sum_vm_cpu + $vm_cpu" | bc 2>/dev/null || echo "$sum_vm_cpu")
    if [ -n "$rate" ] && [ "$rate" -gt 0 ] 2>/dev/null; then
      sum_dps=$((sum_dps + rate))
      valid_dps=$((valid_dps + 1))
    fi

    [ "$i" -lt "$SAMPLES" ] && sleep "$INTERVAL"
  done

  # Averages
  local avg_proxy_cpu avg_vm_cpu avg_dps
  avg_proxy_cpu=$(echo "scale=1; $sum_proxy_cpu / $SAMPLES" | bc 2>/dev/null || echo "0")
  avg_vm_cpu=$(echo "scale=1; $sum_vm_cpu / $SAMPLES" | bc 2>/dev/null || echo "0")
  [ "$valid_dps" -gt 0 ] && avg_dps=$((sum_dps / valid_dps)) || avg_dps=0

  R_LABEL+=("$label"); R_TAG+=("$tag")
  R_PROXY_CPU+=("$avg_proxy_cpu"); R_PROXY_MEM+=("$last_proxy_mem")
  R_VM_CPU+=("$avg_vm_cpu");       R_VM_MEM+=("$last_vm_mem")
  R_DPS+=("$avg_dps")

  log ""
  log "  AVERAGES: proxy=${avg_proxy_cpu}% | vm=${avg_vm_cpu}% | ${avg_dps} dps"
}

cleanup() {
  log "Stopping all containers..."
  $COMPOSE $COMPOSE_FILES down -v --remove-orphans 2>/dev/null || true
  docker rm -f otel-bench otel-bench-prw vmagent-bench 2>/dev/null || true
}

# Start base infrastructure (VM + Grafana) from compose
start_infra() {
  $COMPOSE $COMPOSE_FILES up -d victoriametrics grafana 2>&1 | tail -3
  sleep 5
}

# Start generator from compose, targeting given proxy via OTLP HTTP
# Args: <endpoint> [url_path]
start_generator() {
  local endpoint=$1
  local url_path=${2:-}
  export BENCH_OTLP_ENDPOINT="$endpoint"
  export BENCH_OTLP_PROTOCOL="http"
  export BENCH_OTLP_HTTP_PATH="$url_path"
  $COMPOSE $COMPOSE_FILES up -d --no-deps metrics-generator 2>&1 | tail -3
  sleep 3
}

# Stop generator only
stop_generator() {
  $COMPOSE $COMPOSE_FILES stop metrics-generator 2>/dev/null || true
  $COMPOSE $COMPOSE_FILES rm -f metrics-generator 2>/dev/null || true
}

# ── Formatting helpers ──────────────────────────────────────────

fmt_cpu() { [ "$1" = "0" ] || [ "$1" = ".0" ] && echo "—" || echo "${1}%"; }
fmt_dps() { printf "%'d" "$1" 2>/dev/null || echo "$1"; }

print_summary() {
  local n=${#R_TAG[@]}
  [ "$n" -eq 0 ] && { echo "No test results to display."; return; }

  # Dynamic box width based on number of tests
  local COL_W=16
  local LABEL_W=18
  local BW=$(( LABEL_W + n * COL_W + 2 ))

  box() { printf "║  %-${BW}s║\n" "$1"; }
  box_empty() { printf "║  %${BW}s║\n" ""; }
  box_sep() { printf "╠"; printf '═%.0s' $(seq 1 $((BW+2))); printf "╣\n"; }
  box_top() { printf "╔"; printf '═%.0s' $(seq 1 $((BW+2))); printf "╗\n"; }
  box_bot() { printf "╚"; printf '═%.0s' $(seq 1 $((BW+2))); printf "╝\n"; }

  data_row() {
    local label=$1; shift
    local line
    line=$(printf "%-${LABEL_W}s" "$label")
    for v in "$@"; do
      line+=$(printf "%${COL_W}s" "$v")
    done
    box "$line"
  }

  # Column headers
  local -a COL_HDR=()
  for i in "${!R_TAG[@]}"; do COL_HDR+=("${R_TAG[$i]}: ${R_LABEL[$i]}"); done

  echo ""
  box_top
  box "       PROXY BENCHMARK — 100k dps stable mode (OTLP HTTP input)"
  box "$(date '+%Y-%m-%d %H:%M:%S') | Warmup: ${WARMUP}s | Samples: ${SAMPLES} @ ${INTERVAL}s"
  box_sep
  box_empty
  box "Data paths (all receive OTLP HTTP from generator):"
  [[ "$RUN_TESTS" == *"A"* ]] && box "  A  governor      → VM  via OTLP HTTP"
  [[ "$RUN_TESTS" == *"B"* ]] && box "  B  governor      → VM  via PRW"
  [[ "$RUN_TESTS" == *"C"* ]] && box "  C  otel-collector → VM  via OTLP HTTP"
  [[ "$RUN_TESTS" == *"D"* ]] && box "  D  otel-collector → VM  via PRW"
  [[ "$RUN_TESTS" == *"E"* ]] && box "  E  vmagent       → VM  via PRW"
  box_empty
  box_sep
  box_empty
  box "PROXY RESOURCE USAGE"
  box_empty
  data_row "" "${COL_HDR[@]}"
  # Separator (ASCII dashes avoid multibyte printf width issues)
  local -a sep=(); for i in "${!COL_HDR[@]}"; do sep+=("$(printf '%*s' "$((COL_W-2))" "" | tr ' ' '-')"); done
  data_row "" "${sep[@]}"
  # Proxy stats
  local -a v=()
  v=(); for i in "${!R_TAG[@]}"; do v+=("$(fmt_cpu "${R_PROXY_CPU[$i]}")"); done
  data_row "Proxy    CPU" "${v[@]}"
  v=(); for i in "${!R_TAG[@]}"; do v+=("${R_PROXY_MEM[$i]}"); done
  data_row "         RAM" "${v[@]}"
  box_empty
  # VM stats
  v=(); for i in "${!R_TAG[@]}"; do v+=("$(fmt_cpu "${R_VM_CPU[$i]}")"); done
  data_row "Victoria CPU" "${v[@]}"
  v=(); for i in "${!R_TAG[@]}"; do v+=("${R_VM_MEM[$i]}"); done
  data_row "         RAM" "${v[@]}"
  box_empty
  # Throughput (no spaces in value to avoid word-split issues)
  v=(); for i in "${!R_TAG[@]}"; do v+=("$(fmt_dps "${R_DPS[$i]}")dps"); done
  data_row "Throughput" "${v[@]}"
  box_empty

  # ── Verdict (only if 2+ tests) ──
  if [ "$n" -ge 2 ]; then
    box_sep
    box_empty
    box "VERDICT"
    box_empty

    # Find lowest proxy CPU
    local min_cpu=999999 min_tag="" min_label=""
    for i in "${!R_TAG[@]}"; do
      local cpu10
      cpu10=$(echo "${R_PROXY_CPU[$i]} * 10" | bc 2>/dev/null | cut -d. -f1)
      local min10
      min10=$(echo "$min_cpu * 10" | bc 2>/dev/null | cut -d. -f1)
      if [ "${cpu10:-999999}" -lt "${min10:-999999}" ] 2>/dev/null; then
        min_cpu="${R_PROXY_CPU[$i]}"; min_tag="${R_TAG[$i]}"; min_label="${R_LABEL[$i]}"
      fi
    done

    # Find highest throughput
    local max_dps=0 max_tag="" max_label=""
    for i in "${!R_TAG[@]}"; do
      if [ "${R_DPS[$i]}" -gt "$max_dps" ] 2>/dev/null; then
        max_dps="${R_DPS[$i]}"; max_tag="${R_TAG[$i]}"; max_label="${R_LABEL[$i]}"
      fi
    done

    box "  Lowest proxy CPU:  $min_tag ($min_label) — ${min_cpu}%"
    box "  Best throughput:   $max_tag ($max_label) — $(fmt_dps "$max_dps") dps"
    box_empty
  fi

  box_bot
}

# ═══════════════════════════════════════════════════════════════
log "╔═══════════════════════════════════════════════════════════════╗"
log "║   Proxy Benchmark: Governor vs Collector vs vmagent          ║"
log "║   Input: OTLP HTTP | Output: OTLP HTTP or PRW               ║"
log "║   Generator: 100k dps flat (stable mode)                     ║"
log "╚═══════════════════════════════════════════════════════════════╝"
log ""
log "Tests to run: $RUN_TESTS"

# Ensure results directory exists
mkdir -p "$RESULTS_DIR"

# Build images once
log "Building images..."
$COMPOSE -f docker-compose.yaml build metrics-generator 2>&1 | tail -3
$COMPOSE -f docker-compose.yaml build metrics-governor 2>&1 | tail -3

# ── TEST A: governor → VM (OTLP HTTP export) ───────────────────
if [[ "$RUN_TESTS" == *"A"* ]]; then
  log ""
  log "Starting TEST A: governor → VM via OTLP HTTP..."
  cleanup
  start_infra

  # Start governor (OTLP HTTP export to VM — default from benchmark.yaml)
  $COMPOSE $COMPOSE_FILES up -d --no-deps metrics-governor 2>&1 | tail -3
  sleep 3

  # Generator → governor:4318 (OTLP HTTP)
  start_generator "metrics-governor:4318"

  collect_stats "gov/otlp" "A" "metrics-governor"
  cleanup
fi

# ── TEST B: governor → VM (PRW export) ─────────────────────────
if [[ "$RUN_TESTS" == *"B"* ]]; then
  log ""
  log "Starting TEST B: governor → VM via PRW..."
  cleanup
  start_infra

  # Start governor with PRW exporter instead of OTLP
  # Override command to use PRW export
  $COMPOSE $COMPOSE_FILES run -d --name metrics-governor-prw \
    --service-ports \
    metrics-governor \
    --profile=performance \
    -prw-exporter-endpoint=http://victoriametrics:8428/api/v1/write \
    -prw-exporter-vm-mode \
    -prw-exporter-vm-compression=zstd \
    -flush-interval=75ms \
    -batch-size=500 \
    -max-batch-bytes=4194304 \
    -buffer-size=10000 \
    -queue-enabled=false \
    -stats-labels=service,env \
    2>&1 | tail -3
  sleep 3

  # Generator → governor:4318 (OTLP HTTP)
  start_generator "metrics-governor-prw:4318"

  collect_stats "gov/prw" "B" "metrics-governor-prw"
  docker rm -f metrics-governor-prw 2>/dev/null || true
  cleanup
fi

# ── TEST C: otel-collector → VM (OTLP HTTP export) ─────────────
if [[ "$RUN_TESTS" == *"C"* ]]; then
  log ""
  log "Starting TEST C: otel-collector → VM via OTLP HTTP..."
  cleanup
  start_infra

  # Standalone otel-collector with OTLP HTTP export to VM
  docker run -d --name otel-bench \
    --network "$NETWORK" \
    -p 4317:4317 -p 4318:4318 -p 8888:8888 \
    --cpus="4" --memory="4g" \
    -v "$(pwd)/test/otel-collector-config-direct.yaml:/etc/otelcol/config.yaml:ro" \
    $OTEL_IMAGE \
    --config=/etc/otelcol/config.yaml 2>&1 | tail -1
  sleep 3

  # Generator → otel-collector:4318 (OTLP HTTP)
  start_generator "otel-bench:4318"

  collect_stats "coll/otlp" "C" "otel-bench"
  docker rm -f otel-bench 2>/dev/null || true
  cleanup
fi

# ── TEST D: otel-collector → VM (PRW export) ───────────────────
if [[ "$RUN_TESTS" == *"D"* ]]; then
  log ""
  log "Starting TEST D: otel-collector → VM via PRW..."
  cleanup
  start_infra

  # Standalone otel-collector with PRW export to VM
  docker run -d --name otel-bench-prw \
    --network "$NETWORK" \
    -p 4317:4317 -p 4318:4318 -p 8888:8888 \
    --cpus="4" --memory="4g" \
    -v "$(pwd)/test/otel-collector-config-prw.yaml:/etc/otelcol/config.yaml:ro" \
    $OTEL_IMAGE \
    --config=/etc/otelcol/config.yaml 2>&1 | tail -1
  sleep 3

  # Generator → otel-collector:4318 (OTLP HTTP)
  start_generator "otel-bench-prw:4318"

  collect_stats "coll/prw" "D" "otel-bench-prw"
  docker rm -f otel-bench-prw 2>/dev/null || true
  cleanup
fi

# ── TEST E: vmagent → VM (PRW export) ──────────────────────────
if [[ "$RUN_TESTS" == *"E"* ]]; then
  log ""
  log "Starting TEST E: vmagent → VM via PRW..."
  cleanup
  start_infra

  # vmagent with OTLP HTTP receiver + Remote Write export
  docker run -d --name vmagent-bench \
    --network "$NETWORK" \
    -p 8429:8429 \
    --cpus="4" --memory="4g" \
    $VMAGENT_IMAGE \
    -httpListenAddr=:8429 \
    -openTelemetryListenAddr=:4318 \
    -remoteWrite.url=http://victoriametrics:8428/api/v1/write \
    -remoteWrite.maxDiskUsagePerURL=1GB \
    -remoteWrite.queues=8 2>&1 | tail -1
  sleep 3

  # Generator → vmagent:4318 (OTLP HTTP)
  # vmagent accepts OTLP at /opentelemetry/api/v1/push
  start_generator "vmagent-bench:4318" "/opentelemetry/api/v1/push"

  collect_stats "vmag/prw" "E" "vmagent-bench"
  docker rm -f vmagent-bench 2>/dev/null || true
  cleanup
fi

# ── Summary ───────────────────────────────────────────────────
print_summary | tee "$RESULTS_FILE"

log ""
log "Results saved to: $RESULTS_FILE"
log ""
log "╔═══════════════════════════════════════════════════════════════╗"
log "║                   Benchmark Complete                         ║"
log "╚═══════════════════════════════════════════════════════════════╝"
