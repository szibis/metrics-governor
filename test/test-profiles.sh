#!/bin/bash
# Run each profile at its resource limits and verify stability
# Usage: bash test/test-profiles.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

DURATION=${PROFILE_TEST_DURATION:-300}  # 5 minutes default

log() { echo -e "${YELLOW}[$(date '+%H:%M:%S')]${NC} $*"; }
pass() { echo -e "${GREEN}[PASS]${NC} $*"; }
fail() { echo -e "${RED}[FAIL]${NC} $*"; }

results=()

for profile in minimal balanced performance; do
  log "=== Testing profile: $profile ==="

  cd "$PROJECT_DIR"
  docker compose -f docker-compose.yaml -f "compose_overrides/profile-${profile}.yaml" up -d 2>&1

  log "Waiting ${DURATION}s for stabilization..."
  sleep "$DURATION"

  # Check verifier pass rate
  PASS_RATE=$(curl -sf http://localhost:9092/metrics 2>/dev/null | grep 'verification_pass_rate' | tail -1 | awk '{print $2}' || echo "N/A")
  log "Profile $profile: pass rate = ${PASS_RATE}"

  # Check for OOMs
  OOMS=$(docker compose logs metrics-governor 2>&1 | grep -ci "OOM\|killed\|out of memory" || true)
  log "Profile $profile: OOM events = $OOMS"

  # Collect key metrics
  log "Governor metrics snapshot:"
  curl -sf http://localhost:9090/metrics 2>/dev/null | grep -E "queue_size|buffer_active|workers_active|circuit_breaker" | head -10 || true

  # Determine pass/fail
  if [ "$OOMS" -eq 0 ] && [ "$PASS_RATE" != "N/A" ]; then
    pass "Profile $profile: rate=${PASS_RATE}, OOMs=${OOMS}"
    results+=("$profile: PASS (rate=${PASS_RATE})")
  else
    fail "Profile $profile: rate=${PASS_RATE}, OOMs=${OOMS}"
    results+=("$profile: FAIL (rate=${PASS_RATE}, OOMs=${OOMS})")
  fi

  docker compose -f docker-compose.yaml -f "compose_overrides/profile-${profile}.yaml" down -v 2>&1
  log ""
done

echo ""
log "=== Profile Test Summary ==="
for r in "${results[@]}"; do
  echo "  $r"
done
