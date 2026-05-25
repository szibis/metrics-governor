package autotune

import (
	"fmt"
	"io"
	"time"
)

// writeAutotuneMetrics writes all autotune metrics in Prometheus text format.
func writeAutotuneMetrics(w io.Writer, t *Tuner) {
	// Enabled gauge.
	fmt.Fprintf(w, "# HELP metrics_governor_autotune_enabled Whether autotune is enabled\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_autotune_enabled gauge\n")
	if t.cfg.Enabled {
		fmt.Fprintf(w, "metrics_governor_autotune_enabled 1\n")
	} else {
		fmt.Fprintf(w, "metrics_governor_autotune_enabled 0\n")
	}

	// Active tiers.
	fmt.Fprintf(w, "# HELP metrics_governor_autotune_tier Active autotune tier\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_autotune_tier gauge\n")
	for _, tier := range t.cfg.ActiveTiers() {
		fmt.Fprintf(w, "metrics_governor_autotune_tier{tier=%q} 1\n", fmt.Sprintf("%d", tier))
	}

	// Cycles.
	fmt.Fprintf(w, "# HELP metrics_governor_autotune_cycles_total Total autotune evaluation cycles\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_autotune_cycles_total counter\n")
	fmt.Fprintf(w, "metrics_governor_autotune_cycles_total %d\n", t.cyclesTotal.Load())

	// Errors.
	fmt.Fprintf(w, "# HELP metrics_governor_autotune_errors_total Total autotune errors\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_autotune_errors_total counter\n")
	fmt.Fprintf(w, "metrics_governor_autotune_errors_total %d\n", t.errorsTotal.Load())

	// Adjustments.
	fmt.Fprintf(w, "# HELP metrics_governor_autotune_adjustments_total Total autotune limit adjustments\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_autotune_adjustments_total counter\n")
	fmt.Fprintf(w, "metrics_governor_autotune_adjustments_total %d\n", t.adjustmentsTotal.Load())

	// Rollbacks.
	fmt.Fprintf(w, "# HELP metrics_governor_autotune_rollbacks_total Total autotune rollbacks\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_autotune_rollbacks_total counter\n")
	fmt.Fprintf(w, "metrics_governor_autotune_rollbacks_total %d\n", t.rollbacksTotal.Load())

	// HA.
	fmt.Fprintf(w, "# HELP metrics_governor_autotune_ha_is_leader Whether this instance is the autotune leader\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_autotune_ha_is_leader gauge\n")
	if t.isLeader.Load() {
		fmt.Fprintf(w, "metrics_governor_autotune_ha_is_leader 1\n")
	} else {
		fmt.Fprintf(w, "metrics_governor_autotune_ha_is_leader 0\n")
	}

	// Paused.
	fmt.Fprintf(w, "# HELP metrics_governor_autotune_paused Whether autotune is paused by error budget\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_autotune_paused gauge\n")
	if t.paused.Load() {
		fmt.Fprintf(w, "metrics_governor_autotune_paused 1\n")
	} else {
		fmt.Fprintf(w, "metrics_governor_autotune_paused 0\n")
	}

	// Safeguards.
	fmt.Fprintf(w, "# HELP metrics_governor_autotune_reload_rate_limited_total Reloads rejected by rate limiter\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_autotune_reload_rate_limited_total counter\n")
	fmt.Fprintf(w, "metrics_governor_autotune_reload_rate_limited_total %d\n", t.rateLimiter.Rejected())

	fmt.Fprintf(w, "# HELP metrics_governor_autotune_reload_timeout_total Reloads that timed out\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_autotune_reload_timeout_total counter\n")
	fmt.Fprintf(w, "metrics_governor_autotune_reload_timeout_total %d\n", t.reloadTimeouts.Load())

	fmt.Fprintf(w, "# HELP metrics_governor_autotune_reload_concurrent_rejected_total Reloads rejected due to in-progress reload\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_autotune_reload_concurrent_rejected_total counter\n")
	fmt.Fprintf(w, "metrics_governor_autotune_reload_concurrent_rejected_total %d\n", t.coordinator.Rejected())

	// Circuit breaker.
	fmt.Fprintf(w, "# HELP metrics_governor_autotune_circuit_breaker_state Circuit breaker state (0=closed, 1=open, 2=half-open)\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_autotune_circuit_breaker_state gauge\n")
	fmt.Fprintf(w, "metrics_governor_autotune_circuit_breaker_state{component=%q} %d\n",
		t.sourceCB.Name(), int(t.sourceCB.State()))

	// Oscillation freezes.
	fmt.Fprintf(w, "# HELP metrics_governor_autotune_oscillation_freezes_total Total oscillation freezes\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_autotune_oscillation_freezes_total counter\n")
	fmt.Fprintf(w, "metrics_governor_autotune_oscillation_freezes_total %d\n", t.oscillation.Freezes())

	// Error budget remaining.
	fmt.Fprintf(w, "# HELP metrics_governor_autotune_error_budget_remaining Errors remaining before autotune pauses\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_autotune_error_budget_remaining gauge\n")
	fmt.Fprintf(w, "metrics_governor_autotune_error_budget_remaining %d\n", t.errorBudget.Remaining(timeNow()))

	// Verification.
	fmt.Fprintf(w, "# HELP metrics_governor_autotune_verification_pass_total Total verification passes\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_autotune_verification_pass_total counter\n")
	fmt.Fprintf(w, "metrics_governor_autotune_verification_pass_total %d\n", t.verifier.PassTotal())

	fmt.Fprintf(w, "# HELP metrics_governor_autotune_verification_fail_total Total verification failures (rollbacks)\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_autotune_verification_fail_total counter\n")
	fmt.Fprintf(w, "metrics_governor_autotune_verification_fail_total %d\n", t.verifier.FailTotal())

	// Per-rule current limits.
	t.mu.RLock()
	fmt.Fprintf(w, "# HELP metrics_governor_autotune_rule_current_limit Current autotune-adjusted limit per rule\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_autotune_rule_current_limit gauge\n")
	if len(t.currentLimits) == 0 {
		fmt.Fprintf(w, "metrics_governor_autotune_rule_current_limit{rule=\"none\"} 0\n")
	}
	for rule, limit := range t.currentLimits {
		fmt.Fprintf(w, "metrics_governor_autotune_rule_current_limit{rule=%q} %d\n", rule, limit)
	}

	fmt.Fprintf(w, "# HELP metrics_governor_autotune_rule_baseline_limit Baseline limit per rule (from last SIGHUP)\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_autotune_rule_baseline_limit gauge\n")
	if len(t.baselineLimits) == 0 {
		fmt.Fprintf(w, "metrics_governor_autotune_rule_baseline_limit{rule=\"none\"} 0\n")
	}
	for rule, limit := range t.baselineLimits {
		fmt.Fprintf(w, "metrics_governor_autotune_rule_baseline_limit{rule=%q} %d\n", rule, limit)
	}
	t.mu.RUnlock()

	// Source backend.
	fmt.Fprintf(w, "# HELP metrics_governor_autotune_source_backend Active cardinality source backend\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_autotune_source_backend gauge\n")
	if t.cfg.Source.Enabled {
		fmt.Fprintf(w, "metrics_governor_autotune_source_backend{backend=%q} 1\n", t.cfg.Source.Backend)
	} else {
		fmt.Fprintf(w, "metrics_governor_autotune_source_backend{backend=\"none\"} 0\n")
	}
}

// timeNow is a variable for testing.
var timeNow = time.Now
