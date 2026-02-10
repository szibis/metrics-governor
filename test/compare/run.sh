#!/usr/bin/env bash
# Performance comparison: metrics-governor vs otel-collector vs vmagent
#
# Direct connection: generator sends OTLP HTTP directly to each proxy.
# No intermediate components — pure proxy-to-proxy comparison.
#
# Pipeline per test:
#   governor:       generator --OTLP HTTP--> governor --OTLP HTTP--> VM
#   otel-collector: generator --OTLP HTTP--> otel-collector --OTLP HTTP--> VM
#   vmagent:        generator --OTLP HTTP--> vmagent --Remote Write--> VM
#
# Usage: ./test/compare/run.sh [--warmup 90] [--duration 180] [--interval 10]
# Results: test/compare/results/

set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

# Defaults
WARMUP=${WARMUP:-90}
DURATION=${DURATION:-180}
INTERVAL=${INTERVAL:-10}
RESULTS_DIR="test/compare/results"

# Parse args
while [[ $# -gt 0 ]]; do
    case $1 in
        --warmup)   WARMUP="$2"; shift 2 ;;
        --duration) DURATION="$2"; shift 2 ;;
        --interval) INTERVAL="$2"; shift 2 ;;
        --help)
            echo "Usage: $0 [--warmup SEC] [--duration SEC] [--interval SEC]"
            echo ""
            echo "  --warmup    Seconds to wait before sampling (default: 90)"
            echo "  --duration  Seconds of sampling (default: 180)"
            echo "  --interval  Seconds between samples (default: 10)"
            exit 0
            ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

TESTS=("compare-governor" "compare-otel" "compare-vmagent")
LABELS=("governor" "otel-collector" "vmagent")
# Which container is the "proxy under test" for each test
PROXY_CONTAINERS=("metrics-governor" "otel-collector" "vmagent")

mkdir -p "$RESULTS_DIR"
SUMMARY="$RESULTS_DIR/summary.tsv"
echo -e "proxy\tcpu_avg\tcpu_max\tmem_avg_pct\tingestion\tdatapoints_recv\tdatapoints_sent\texport_errors" > "$SUMMARY"

echo "================================================================"
echo "  Performance Comparison: governor vs otel-collector vs vmagent"
echo "================================================================"
echo "  Direct connection: generator → proxy → VictoriaMetrics"
echo "  Protocol: OTLP HTTP (all three)"
echo "  Warmup: ${WARMUP}s | Sampling: ${DURATION}s | Interval: ${INTERVAL}s"
echo "  Load: 15k dps (7.5k metrics/sec)"
echo "  Proxy resources: 1 CPU, 512M memory (identical)"
echo "================================================================"
echo ""

cleanup() {
    echo "Cleaning up..."
    for test in "${TESTS[@]}"; do
        docker compose -f docker-compose.yaml -f "compose_overrides/${test}.yaml" down -v 2>/dev/null || true
    done
}
trap cleanup EXIT

declare -A RESULTS_CPU_AVG RESULTS_CPU_MAX RESULTS_MEM RESULTS_INGESTION RESULTS_ERRORS

for i in "${!TESTS[@]}"; do
    test="${TESTS[$i]}"
    label="${LABELS[$i]}"
    proxy="${PROXY_CONTAINERS[$i]}"

    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "  TEST $((i+1))/3: ${label}"
    echo "  Pipeline: generator --OTLP HTTP--> ${label} --> VictoriaMetrics"
    echo "  Proxy container: ${proxy} (1 CPU, 512M)"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

    # Start
    echo "[$(date +%H:%M:%S)] Starting stack..."
    docker compose -f docker-compose.yaml -f "compose_overrides/${test}.yaml" up -d 2>&1 | tail -1

    # Warmup
    echo "[$(date +%H:%M:%S)] Warming up (${WARMUP}s)..."
    sleep "$WARMUP"

    # Check all containers are running
    echo "[$(date +%H:%M:%S)] Checking container health..."
    docker compose -f docker-compose.yaml -f "compose_overrides/${test}.yaml" ps --format "table {{.Name}}\t{{.State}}" 2>&1 | head -10

    # Collect stats samples
    STATS_FILE="$RESULTS_DIR/${label}-stats.tsv"
    echo -e "timestamp\tcontainer\tcpu\tmem_usage\tmem_pct" > "$STATS_FILE"

    samples=$((DURATION / INTERVAL))
    echo "[$(date +%H:%M:%S)] Collecting ${samples} samples over ${DURATION}s..."

    for s in $(seq 1 "$samples"); do
        ts=$(date +%H:%M:%S)
        docker stats --no-stream --format "{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.MemPerc}}" 2>/dev/null | while read -r line; do
            echo -e "${ts}\t${line}" >> "$STATS_FILE"
        done
        if (( s % 6 == 0 )); then
            echo "  [${ts}] Sample ${s}/${samples}"
        fi
        sleep "$INTERVAL"
    done

    # Get verifier results
    VERIFIER_FILE="$RESULTS_DIR/${label}-verifier.log"
    docker logs metrics-governor-verifier-1 --tail 40 2>&1 > "$VERIFIER_FILE"

    # Extract key metrics from verifier
    ingestion=$(grep -o 'Ingestion rate:.*%' "$VERIFIER_FILE" | tail -1 | grep -o '[0-9.]*%' || echo "N/A")
    received=$(grep -o 'Datapoints received:.*' "$VERIFIER_FILE" | tail -1 | grep -o '[0-9]*' || echo "0")
    sent=$(grep -o 'Datapoints sent:.*' "$VERIFIER_FILE" | tail -1 | grep -o '[0-9]*' || echo "0")
    errors=$(grep -o 'Export errors:.*' "$VERIFIER_FILE" | tail -1 | grep -o '[0-9]*' || echo "0")

    # For vmagent: verifier can't match metrics due to Remote Write naming conversion
    # (OTLP metric names → Prometheus format). Check VM TSDB directly for time series count.
    if [[ "$proxy" == "vmagent" ]]; then
        vm_series=$(curl -s "http://localhost:8428/api/v1/status/tsdb" 2>/dev/null | grep -o '"totalSeries":[0-9]*' | grep -o '[0-9]*' || echo "0")
        if [[ "$vm_series" -gt 0 ]]; then
            ingestion="flowing (${vm_series} series)"
        fi
    fi

    # Compute stats for proxy container (match partial container name)
    proxy_cpu_avg=$(grep -i "$proxy" "$STATS_FILE" | awk -F'\t' '{gsub(/%/,"",$3); sum+=$3; n++} END {if(n>0) printf "%.2f", sum/n; else print "N/A"}')
    proxy_cpu_max=$(grep -i "$proxy" "$STATS_FILE" | awk -F'\t' '{gsub(/%/,"",$3); if($3+0>max+0) max=$3} END {printf "%.2f", max+0}')
    proxy_mem_avg=$(grep -i "$proxy" "$STATS_FILE" | awk -F'\t' '{gsub(/%/,"",$5); sum+=$5; n++} END {if(n>0) printf "%.1f", sum/n; else print "N/A"}')

    echo ""
    echo "  Results:"
    echo "  ┌─────────────────────────────────────────┐"
    echo "  │ Proxy: ${label}"
    echo "  │ CPU avg/max: ${proxy_cpu_avg}% / ${proxy_cpu_max}%"
    echo "  │ Memory avg: ${proxy_mem_avg}%"
    echo "  │ Ingestion: ${ingestion}"
    echo "  │ Datapoints: ${received} recv / ${sent} sent"
    echo "  │ Export errors: ${errors}"
    echo "  └─────────────────────────────────────────┘"

    # Write to summary
    echo -e "${label}\t${proxy_cpu_avg}\t${proxy_cpu_max}\t${proxy_mem_avg}\t${ingestion}\t${received}\t${sent}\t${errors}" >> "$SUMMARY"

    # Store for comparison
    RESULTS_CPU_AVG[$label]="$proxy_cpu_avg"
    RESULTS_CPU_MAX[$label]="$proxy_cpu_max"
    RESULTS_MEM[$label]="$proxy_mem_avg"
    RESULTS_INGESTION[$label]="$ingestion"
    RESULTS_ERRORS[$label]="$errors"

    # Tear down
    echo "[$(date +%H:%M:%S)] Tearing down..."
    docker compose -f docker-compose.yaml -f "compose_overrides/${test}.yaml" down -v 2>&1 | tail -1

    echo "[$(date +%H:%M:%S)] Done with ${label}."
    sleep 5  # Brief pause between tests
done

# Generate comparison table
echo ""
echo ""
echo "================================================================"
echo "  COMPARISON RESULTS"
echo "================================================================"
echo "  Load: 15k dps | Protocol: OTLP HTTP | Resources: 1 CPU, 512M"
echo "================================================================"
echo ""
printf "%-16s │ %-10s │ %-10s │ %-8s │ %-10s │ %-8s\n" \
    "Proxy" "CPU avg" "CPU max" "Mem %" "Ingestion" "Errors"
printf "%-16s─┼─%-10s─┼─%-10s─┼─%-8s─┼─%-10s─┼─%-8s\n" \
    "────────────────" "──────────" "──────────" "────────" "──────────" "────────"

for label in "${LABELS[@]}"; do
    printf "%-16s │ %-10s │ %-10s │ %-8s │ %-10s │ %-8s\n" \
        "$label" \
        "${RESULTS_CPU_AVG[$label]:-N/A}%" \
        "${RESULTS_CPU_MAX[$label]:-N/A}%" \
        "${RESULTS_MEM[$label]:-N/A}%" \
        "${RESULTS_INGESTION[$label]:-N/A}" \
        "${RESULTS_ERRORS[$label]:-0}"
done

echo ""
echo "Notes:"
echo "  - All proxies receive OTLP HTTP from generator (identical ingestion)"
echo "  - governor exports OTLP HTTP to VM"
echo "  - otel-collector exports OTLP HTTP to VM"
echo "  - vmagent exports Prometheus Remote Write to VM"
echo "  - vmagent verifier shows 'flowing (N series)' because Remote Write"
echo "    converts OTLP metric names to Prometheus format, so verifier can't"
echo "    match metrics by name. Data IS flowing — verify via VM TSDB status."
echo ""
echo "Detailed stats: ${RESULTS_DIR}/"
echo "================================================================"
