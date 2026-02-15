#!/usr/bin/env bash
# Multi-load performance comparison: governor-balanced vs otel-collector
#
# Runs at 50k, 100k, 200k dps with scaled resources.
# Results saved per load level in test/compare/results/
#
# Usage: ./test/compare/run-multi-load.sh [--warmup 60] [--duration 120]

set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

WARMUP=${WARMUP:-60}
DURATION=${DURATION:-120}
INTERVAL=${INTERVAL:-10}
RESULTS_DIR="test/compare/results"

# Parse args
while [[ $# -gt 0 ]]; do
    case $1 in
        --warmup)   WARMUP="$2"; shift 2 ;;
        --duration) DURATION="$2"; shift 2 ;;
        --interval) INTERVAL="$2"; shift 2 ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

# Load configurations: DPS MPS PROXY_MEM PROXY_CPU GEN_MEM GEN_CPU VM_MEM
# Generator auto-scales to TARGET_DPS via load_filler_datapoints metric
declare -A LOADS
LOADS[50k]="50000 25000 1G 2 2G 2 2G"
LOADS[100k]="100000 50000 2G 4 4G 4 4G"

# Load levels to run (override with LOAD_LEVELS env var, e.g. "50k 100k 200k")
LOAD_LEVELS=${LOAD_LEVELS:-"50k 100k"}

# Tests to run at each load level
TESTS=("compare-governor" "compare-governor-balanced" "compare-otel" "compare-vmagent")
LABELS=("governor-minimal" "governor-balanced" "otel-collector" "vmagent")
# Use full container names to avoid matching other containers with project name prefix
# Docker Compose names: <project>-<service>-<replica>, e.g. metrics-governor-metrics-governor-1
PROXY_CONTAINERS=("metrics-governor-metrics-governor" "metrics-governor-metrics-governor" "metrics-governor-otel-collector" "metrics-governor-vmagent")

mkdir -p "$RESULTS_DIR"

cleanup() {
    echo "Cleaning up..."
    for test in "${TESTS[@]}"; do
        docker compose -f docker-compose.yaml -f "compose_overrides/${test}.yaml" down -v 2>/dev/null || true
    done
}
trap cleanup EXIT

echo "================================================================"
echo "  Multi-Load Performance Comparison"
echo "================================================================"
echo "  Warmup: ${WARMUP}s | Sampling: ${DURATION}s | Interval: ${INTERVAL}s"
echo "  Load levels: ${LOAD_LEVELS}"
echo "  Proxies: governor-minimal, governor-balanced, otel-collector, vmagent"
echo "================================================================"

# Summary file for all load levels
MULTI_SUMMARY="$RESULTS_DIR/multi-load-summary.tsv"
echo -e "load\tproxy\tcpu_avg\tcpu_max\tmem_avg_pct\tmem_max_pct\tingestion\tdatapoints_recv\tdatapoints_sent\texport_errors" > "$MULTI_SUMMARY"

for load_key in $LOAD_LEVELS; do
    read -r DPS MPS PMEM PCPU GMEM GCPU VMEM <<< "${LOADS[$load_key]}"

    echo ""
    echo "╔══════════════════════════════════════════════════════════════╗"
    echo "║  LOAD LEVEL: ${load_key} dps (${MPS} metrics/sec)"
    echo "║  Resources: ${PCPU} CPU, ${PMEM} memory per proxy"
    echo "╚══════════════════════════════════════════════════════════════╝"

    for i in "${!TESTS[@]}"; do
        test="${TESTS[$i]}"
        label="${LABELS[$i]}"
        proxy="${PROXY_CONTAINERS[$i]}"

        echo ""
        echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
        echo "  ${load_key}: ${label}"
        echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

        echo "[$(date +%H:%M:%S)] Starting stack..."
        TARGET_DPS="$DPS" TARGET_MPS="$MPS" \
            PROXY_MEM="$PMEM" PROXY_CPU="$PCPU" \
            GEN_MEM="$GMEM" GEN_CPU="$GCPU" VM_MEM="$VMEM" \
            docker compose -f docker-compose.yaml -f "compose_overrides/${test}.yaml" up -d --build 2>&1 | tail -1

        echo "[$(date +%H:%M:%S)] Warming up (${WARMUP}s)..."
        sleep "$WARMUP"

        # Health check
        echo "[$(date +%H:%M:%S)] Checking health..."
        docker compose -f docker-compose.yaml -f "compose_overrides/${test}.yaml" ps --format "table {{.Name}}\t{{.State}}" 2>&1 | head -10

        # Collect stats
        STATS_FILE="$RESULTS_DIR/${label}-${load_key}-stats.tsv"
        echo -e "timestamp\tcontainer\tcpu\tmem_usage\tmem_pct" > "$STATS_FILE"

        samples=$((DURATION / INTERVAL))
        echo "[$(date +%H:%M:%S)] Collecting ${samples} samples over ${DURATION}s..."

        for s in $(seq 1 "$samples"); do
            ts=$(date +%H:%M:%S)
            docker stats --no-stream --format "{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.MemPerc}}" 2>/dev/null | while read -r line; do
                echo -e "${ts}\t${line}" >> "$STATS_FILE"
            done
            if (( s % 4 == 0 )); then
                echo "  [${ts}] Sample ${s}/${samples}"
            fi
            sleep "$INTERVAL"
        done

        # Get verifier results
        VERIFIER_FILE="$RESULTS_DIR/${label}-${load_key}-verifier.log"
        # Use compose logs to get the verifier output regardless of container name prefix
        docker compose -f docker-compose.yaml -f "compose_overrides/${test}.yaml" logs verifier --tail 40 2>&1 > "$VERIFIER_FILE"

        # Extract metrics
        ingestion=$(grep -o 'Ingestion rate:.*%' "$VERIFIER_FILE" | tail -1 | grep -o '[0-9.]*%' || echo "N/A")
        received=$(grep -o 'Datapoints received: *[0-9]*' "$VERIFIER_FILE" | tail -1 | grep -o '[0-9]*' || echo "0")
        sent=$(grep -o 'Datapoints sent: *[0-9]*' "$VERIFIER_FILE" | tail -1 | grep -o '[0-9]*' || echo "0")
        errors=$(grep -o 'Export errors: *[0-9]*' "$VERIFIER_FILE" | tail -1 | grep -o '[0-9]*' || echo "0")

        # Compute proxy stats
        proxy_cpu_avg=$(grep -i "$proxy" "$STATS_FILE" | awk -F'\t' '{gsub(/%/,"",$3); sum+=$3; n++} END {if(n>0) printf "%.2f", sum/n; else print "N/A"}')
        proxy_cpu_max=$(grep -i "$proxy" "$STATS_FILE" | awk -F'\t' '{gsub(/%/,"",$3); if($3+0>max+0) max=$3} END {printf "%.2f", max+0}')
        proxy_mem_avg=$(grep -i "$proxy" "$STATS_FILE" | awk -F'\t' '{gsub(/%/,"",$5); sum+=$5; n++} END {if(n>0) printf "%.1f", sum/n; else print "N/A"}')
        proxy_mem_max=$(grep -i "$proxy" "$STATS_FILE" | awk -F'\t' '{gsub(/%/,"",$5); if($5+0>max+0) max=$5} END {printf "%.1f", max+0}')

        echo ""
        echo "  ┌─────────────────────────────────────────┐"
        echo "  │ ${load_key}: ${label}"
        echo "  │ CPU avg/max: ${proxy_cpu_avg}% / ${proxy_cpu_max}%"
        echo "  │ Memory avg/max: ${proxy_mem_avg}% / ${proxy_mem_max}%"
        echo "  │ Ingestion: ${ingestion}"
        echo "  │ Datapoints: ${received} recv / ${sent} sent"
        echo "  │ Export errors: ${errors}"
        echo "  └─────────────────────────────────────────┘"

        # Append to multi-load summary
        echo -e "${load_key}\t${label}\t${proxy_cpu_avg}\t${proxy_cpu_max}\t${proxy_mem_avg}\t${proxy_mem_max}\t${ingestion}\t${received}\t${sent}\t${errors}" >> "$MULTI_SUMMARY"

        # Tear down
        echo "[$(date +%H:%M:%S)] Tearing down..."
        docker compose -f docker-compose.yaml -f "compose_overrides/${test}.yaml" down -v 2>&1 | tail -1
        sleep 5
    done
done

echo ""
echo "================================================================"
echo "  MULTI-LOAD RESULTS"
echo "================================================================"
echo ""
printf "%-6s │ %-20s │ %-10s │ %-10s │ %-8s │ %-8s │ %-10s │ %-8s\n" \
    "Load" "Proxy" "CPU avg" "CPU max" "Mem avg" "Mem max" "Ingestion" "Errors"
printf "%-6s─┼─%-20s─┼─%-10s─┼─%-10s─┼─%-8s─┼─%-8s─┼─%-10s─┼─%-8s\n" \
    "──────" "────────────────────" "──────────" "──────────" "────────" "────────" "──────────" "────────"

# Read and display the summary
tail -n +2 "$MULTI_SUMMARY" | while IFS=$'\t' read -r load proxy cpu_avg cpu_max mem_avg mem_max ingestion recv sent errors; do
    printf "%-6s │ %-20s │ %-10s │ %-10s │ %-8s │ %-8s │ %-10s │ %-8s\n" \
        "$load" "$proxy" "${cpu_avg}%" "${cpu_max}%" "${mem_avg}%" "${mem_max}%" "$ingestion" "$errors"
done

echo ""
echo "Results saved to: ${RESULTS_DIR}/"
echo "================================================================"
