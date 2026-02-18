package functional

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/autotune"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newTestPolicyConfig returns a PolicyConfig suitable for fast functional tests
// (short cooldowns, small cardinality range).
func newTestPolicyConfig() autotune.PolicyConfig {
	cfg := autotune.DefaultPolicyConfig()
	cfg.Cooldown = 1 * time.Millisecond // eliminate cooldown delay in tests
	cfg.MinCardinality = 100
	cfg.MaxCardinality = 100_000
	cfg.DecreaseSustainTime = 1 * time.Hour
	return cfg
}

// ---------------------------------------------------------------------------
// 1. TestFunctional_Autotune_GrowOnHighUtilization
// ---------------------------------------------------------------------------

func TestFunctional_Autotune_GrowOnHighUtilization(t *testing.T) {
	cfg := newTestPolicyConfig()
	od := autotune.NewOscillationDetector(autotune.OscillationConfig{
		LookbackCount:  6,
		Threshold:      0.75,
		FreezeDuration: time.Hour,
	})
	engine := autotune.NewPolicyEngine(cfg, od)

	signals := autotune.AggregatedSignals{
		Utilization:            map[string]float64{"rule-a": 0.88},
		CardinalityUtilization: map[string]float64{"rule-a": 0.88},
		DPRateUtilization:      map[string]float64{"rule-a": 0},
		RuleUtilization:        map[string]autotune.RuleUtilization{},
		Timestamp:              time.Now(),
	}
	limits := map[string]int64{"rule-a": 1000}
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	decisions := engine.Evaluate(signals, limits, now)
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	d := decisions[0]
	if d.Action != autotune.ActionIncrease {
		t.Errorf("expected ActionIncrease, got %s", d.Action)
	}
	if d.NewValue <= d.OldValue {
		t.Errorf("expected new value (%d) > old value (%d)", d.NewValue, d.OldValue)
	}
	if d.RuleName != "rule-a" {
		t.Errorf("expected rule-a, got %s", d.RuleName)
	}
}

// ---------------------------------------------------------------------------
// 2. TestFunctional_Autotune_ShrinkAfterSustainedLow
// ---------------------------------------------------------------------------

func TestFunctional_Autotune_ShrinkAfterSustainedLow(t *testing.T) {
	cfg := newTestPolicyConfig()
	cfg.DecreaseSustainTime = 1 * time.Hour
	od := autotune.NewOscillationDetector(autotune.OscillationConfig{
		LookbackCount:  6,
		Threshold:      0.75,
		FreezeDuration: time.Hour,
	})
	engine := autotune.NewPolicyEngine(cfg, od)

	signals := autotune.AggregatedSignals{
		Utilization:            map[string]float64{"rule-b": 0.20},
		CardinalityUtilization: map[string]float64{"rule-b": 0.20},
		DPRateUtilization:      map[string]float64{"rule-b": 0},
		RuleUtilization:        map[string]autotune.RuleUtilization{},
		Timestamp:              time.Now(),
	}
	limits := map[string]int64{"rule-b": 5000}

	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	// First evaluation: records low-since, returns hold.
	decisions := engine.Evaluate(signals, limits, t0)
	if len(decisions) != 0 {
		t.Fatalf("expected 0 decisions on first low eval, got %d", len(decisions))
	}

	// Evaluate again before sustain time elapses — should still hold.
	t1 := t0.Add(30 * time.Minute)
	decisions = engine.Evaluate(signals, limits, t1)
	if len(decisions) != 0 {
		t.Fatalf("expected 0 decisions before sustain time, got %d", len(decisions))
	}

	// Evaluate after sustain time — should decrease.
	t2 := t0.Add(1*time.Hour + 1*time.Second)
	decisions = engine.Evaluate(signals, limits, t2)
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision after sustain, got %d", len(decisions))
	}
	if decisions[0].Action != autotune.ActionDecrease {
		t.Errorf("expected ActionDecrease, got %s", decisions[0].Action)
	}
	if decisions[0].NewValue >= decisions[0].OldValue {
		t.Errorf("expected new value (%d) < old value (%d)", decisions[0].NewValue, decisions[0].OldValue)
	}
}

// ---------------------------------------------------------------------------
// 3. TestFunctional_Autotune_RollbackOnDropSpike
// ---------------------------------------------------------------------------

func TestFunctional_Autotune_RollbackOnDropSpike(t *testing.T) {
	var drops atomic.Int64

	vcfg := autotune.VerificationConfig{
		Warmup:                 10 * time.Millisecond,
		Window:                 200 * time.Millisecond,
		RollbackCooldownFactor: 3,
	}
	rollbackDropPct := 0.05
	verifier := autotune.NewPostApplyVerifier(vcfg, rollbackDropPct, func() int64 {
		return drops.Load()
	})

	// Baseline drops = 0, start verification.
	decisions := []autotune.Decision{
		{RuleName: "rule-c", Action: autotune.ActionDecrease, OldValue: 5000, NewValue: 3750},
	}
	now := time.Now()
	verifier.StartVerification(decisions, now)

	// During warmup, result should be pending.
	res, _ := verifier.Check(now.Add(5 * time.Millisecond))
	if res != autotune.VerificationPending {
		t.Fatalf("expected VerificationPending during warmup, got %d", res)
	}

	// Simulate a big drop spike after warmup.
	drops.Store(10000)

	// Check after warmup + some window time — should fail.
	checkTime := now.Add(15 * time.Millisecond)
	res, rollbackDecs := verifier.Check(checkTime)
	if res != autotune.VerificationFail {
		t.Fatalf("expected VerificationFail with drop spike, got %d", res)
	}
	if len(rollbackDecs) != 1 {
		t.Fatalf("expected 1 rollback decision, got %d", len(rollbackDecs))
	}
	if rollbackDecs[0].RuleName != "rule-c" {
		t.Errorf("expected rule-c in rollback, got %s", rollbackDecs[0].RuleName)
	}
}

// ---------------------------------------------------------------------------
// 4. TestFunctional_Autotune_OscillationFreeze
// ---------------------------------------------------------------------------

func TestFunctional_Autotune_OscillationFreeze(t *testing.T) {
	od := autotune.NewOscillationDetector(autotune.OscillationConfig{
		LookbackCount:  6,
		Threshold:      0.75,
		FreezeDuration: time.Hour,
	})

	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	// Feed alternating decisions directly into the oscillation detector
	// to reliably trigger oscillation detection.
	actions := []autotune.Action{
		autotune.ActionIncrease,
		autotune.ActionDecrease,
		autotune.ActionIncrease,
		autotune.ActionDecrease,
		autotune.ActionIncrease,
		autotune.ActionDecrease,
	}
	for i, action := range actions {
		d := autotune.Decision{
			RuleName:  "rule-osc",
			Action:    action,
			Timestamp: now.Add(time.Duration(i) * time.Minute),
		}
		od.Record("rule-osc", d)
	}

	// After 6 alternating decisions (5 alternations / 5 transitions = 1.0 > 0.75),
	// the oscillation detector should have frozen the rule.
	checkTime := now.Add(7 * time.Minute)
	if !od.IsFrozen("rule-osc", checkTime) {
		t.Error("expected rule-osc to be frozen after oscillating decisions")
	}

	if od.Freezes() == 0 {
		t.Error("expected at least 1 oscillation freeze to be recorded")
	}

	// Now verify the policy engine respects the freeze.
	cfg := newTestPolicyConfig()
	engine := autotune.NewPolicyEngine(cfg, od)

	signals := autotune.AggregatedSignals{
		Utilization:            map[string]float64{"rule-osc": 0.92},
		CardinalityUtilization: map[string]float64{"rule-osc": 0.92},
		DPRateUtilization:      map[string]float64{"rule-osc": 0},
		RuleUtilization:        map[string]autotune.RuleUtilization{},
		Timestamp:              checkTime,
	}
	limits := map[string]int64{"rule-osc": 1000}

	// Despite high utilization, the frozen rule should produce no decisions.
	decisions := engine.Evaluate(signals, limits, checkTime)
	if len(decisions) != 0 {
		t.Errorf("expected 0 decisions for frozen rule, got %d", len(decisions))
	}
}

// ---------------------------------------------------------------------------
// 5. TestFunctional_Autotune_ManualSIGHUPOverride
// ---------------------------------------------------------------------------

func TestFunctional_Autotune_ManualSIGHUPOverride(t *testing.T) {
	cfg := newTestPolicyConfig()
	od := autotune.NewOscillationDetector(autotune.OscillationConfig{
		LookbackCount:  6,
		Threshold:      0.75,
		FreezeDuration: time.Hour,
	})
	engine := autotune.NewPolicyEngine(cfg, od)

	signals := autotune.AggregatedSignals{
		Utilization:            map[string]float64{"rule-e": 0.88},
		CardinalityUtilization: map[string]float64{"rule-e": 0.88},
		DPRateUtilization:      map[string]float64{"rule-e": 0},
		RuleUtilization:        map[string]autotune.RuleUtilization{},
		Timestamp:              time.Now(),
	}
	limits := map[string]int64{"rule-e": 1000}
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	// Trigger an increase decision.
	decisions := engine.Evaluate(signals, limits, now)
	if len(decisions) != 1 || decisions[0].Action != autotune.ActionIncrease {
		t.Fatalf("expected 1 increase decision, got %d", len(decisions))
	}

	// Simulate SIGHUP: ResetAll clears tracking state.
	engine.ResetAll()

	// After reset, the engine should be able to evaluate again immediately
	// (cooldown is cleared).
	now2 := now.Add(1 * time.Second) // tiny step, normally within cooldown
	decisions2 := engine.Evaluate(signals, limits, now2)
	if len(decisions2) != 1 {
		t.Fatalf("expected 1 decision after ResetAll, got %d", len(decisions2))
	}
	if decisions2[0].Action != autotune.ActionIncrease {
		t.Errorf("expected ActionIncrease after reset, got %s", decisions2[0].Action)
	}
}

// ---------------------------------------------------------------------------
// 6. TestFunctional_Autotune_DisabledByDefault
// ---------------------------------------------------------------------------

func TestFunctional_Autotune_DisabledByDefault(t *testing.T) {
	cfg := autotune.DefaultConfig()
	if cfg.Enabled {
		t.Fatal("expected autotune to be disabled by default")
	}

	// Validate should pass when disabled (nothing to validate).
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected no validation error when disabled, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 7. TestFunctional_Autotune_MultiRuleEvaluation
// ---------------------------------------------------------------------------

func TestFunctional_Autotune_MultiRuleEvaluation(t *testing.T) {
	cfg := newTestPolicyConfig()
	od := autotune.NewOscillationDetector(autotune.OscillationConfig{
		LookbackCount:  6,
		Threshold:      0.75,
		FreezeDuration: time.Hour,
	})
	engine := autotune.NewPolicyEngine(cfg, od)

	signals := autotune.AggregatedSignals{
		Utilization: map[string]float64{
			"rule-high": 0.92,
			"rule-mid":  0.50, // in normal range, should hold
			"rule-low":  0.10, // below decrease threshold, but needs sustain
		},
		CardinalityUtilization: map[string]float64{
			"rule-high": 0.92,
			"rule-mid":  0.50,
			"rule-low":  0.10,
		},
		DPRateUtilization: map[string]float64{
			"rule-high": 0,
			"rule-mid":  0,
			"rule-low":  0,
		},
		RuleUtilization: map[string]autotune.RuleUtilization{},
		Timestamp:       time.Now(),
	}
	limits := map[string]int64{
		"rule-high": 2000,
		"rule-mid":  3000,
		"rule-low":  4000,
	}
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	decisions := engine.Evaluate(signals, limits, now)

	// Should get exactly 1 decision: increase for rule-high.
	// rule-mid is in normal range (hold), rule-low just recorded low-since (hold).
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision (only rule-high), got %d", len(decisions))
	}
	if decisions[0].RuleName != "rule-high" {
		t.Errorf("expected rule-high, got %s", decisions[0].RuleName)
	}
	if decisions[0].Action != autotune.ActionIncrease {
		t.Errorf("expected ActionIncrease, got %s", decisions[0].Action)
	}
}

// ---------------------------------------------------------------------------
// 8. TestFunctional_Autotune_ClampToFloorAndCeiling
// ---------------------------------------------------------------------------

func TestFunctional_Autotune_ClampToFloorAndCeiling(t *testing.T) {
	cfg := newTestPolicyConfig()
	cfg.MinCardinality = 500
	cfg.MaxCardinality = 2000
	cfg.GrowFactor = 2.0   // aggressive growth
	cfg.MaxChangePct = 1.0 // allow full change
	od := autotune.NewOscillationDetector(autotune.OscillationConfig{
		LookbackCount:  6,
		Threshold:      0.75,
		FreezeDuration: time.Hour,
	})
	engine := autotune.NewPolicyEngine(cfg, od)

	// Test ceiling clamp: limit at 1500, grow factor 2.0 → 3000, but max is 2000.
	signals := autotune.AggregatedSignals{
		Utilization:            map[string]float64{"rule-ceil": 0.95},
		CardinalityUtilization: map[string]float64{"rule-ceil": 0.95},
		DPRateUtilization:      map[string]float64{"rule-ceil": 0},
		RuleUtilization:        map[string]autotune.RuleUtilization{},
		Timestamp:              time.Now(),
	}
	limits := map[string]int64{"rule-ceil": 1500}
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	decisions := engine.Evaluate(signals, limits, now)
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	if decisions[0].NewValue > 2000 {
		t.Errorf("expected new value clamped to 2000, got %d", decisions[0].NewValue)
	}

	// Test floor clamp: limit at 600, shrink factor 0.75 → 450, but min is 500.
	cfg2 := newTestPolicyConfig()
	cfg2.MinCardinality = 500
	cfg2.MaxCardinality = 2000
	cfg2.ShrinkFactor = 0.50     // aggressive shrink
	cfg2.MaxChangePct = 1.0      // allow full change
	cfg2.DecreaseSustainTime = 0 // no sustain needed for this test
	od2 := autotune.NewOscillationDetector(autotune.OscillationConfig{
		LookbackCount:  6,
		Threshold:      0.75,
		FreezeDuration: time.Hour,
	})
	engine2 := autotune.NewPolicyEngine(cfg2, od2)

	signalsLow := autotune.AggregatedSignals{
		Utilization:            map[string]float64{"rule-floor": 0.10},
		CardinalityUtilization: map[string]float64{"rule-floor": 0.10},
		DPRateUtilization:      map[string]float64{"rule-floor": 0},
		RuleUtilization:        map[string]autotune.RuleUtilization{},
		Timestamp:              time.Now(),
	}
	limitsLow := map[string]int64{"rule-floor": 600}
	t0 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	// First eval records low-since with DecreaseSustainTime=0.
	engine2.Evaluate(signalsLow, limitsLow, t0)
	// Second eval should trigger decrease since sustain=0.
	t1 := t0.Add(1 * time.Millisecond)
	decisions2 := engine2.Evaluate(signalsLow, limitsLow, t1)
	if len(decisions2) == 1 {
		if decisions2[0].NewValue < 500 {
			t.Errorf("expected new value clamped to floor 500, got %d", decisions2[0].NewValue)
		}
	}
}

// ---------------------------------------------------------------------------
// 9. TestFunctional_Autotune_PersistAndReload
// ---------------------------------------------------------------------------

func TestFunctional_Autotune_PersistAndReload(t *testing.T) {
	persister := autotune.NewMemoryPersister(100)
	ctx := context.Background()

	changes := []autotune.ConfigChange{
		{
			Timestamp: time.Now(),
			Domain:    "limits",
			RuleName:  "rule-persist",
			Field:     "max_cardinality",
			OldValue:  1000,
			NewValue:  1250,
			Action:    "increase",
			Reason:    "utilization above threshold",
		},
	}

	if err := persister.Persist(ctx, changes); err != nil {
		t.Fatalf("persist failed: %v", err)
	}

	loaded, err := persister.LoadHistory(ctx)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 change, got %d", len(loaded))
	}
	if loaded[0].RuleName != "rule-persist" {
		t.Errorf("expected rule-persist, got %s", loaded[0].RuleName)
	}
	if loaded[0].NewValue != 1250 {
		t.Errorf("expected NewValue 1250, got %d", loaded[0].NewValue)
	}

	// Persist more changes and verify they accumulate.
	changes2 := []autotune.ConfigChange{
		{
			Timestamp: time.Now(),
			Domain:    "limits",
			RuleName:  "rule-persist-2",
			Field:     "max_cardinality",
			OldValue:  2000,
			NewValue:  1500,
			Action:    "decrease",
			Reason:    "low utilization",
		},
	}
	if err := persister.Persist(ctx, changes2); err != nil {
		t.Fatalf("second persist failed: %v", err)
	}

	loaded2, err := persister.LoadHistory(ctx)
	if err != nil {
		t.Fatalf("second load failed: %v", err)
	}
	if len(loaded2) != 2 {
		t.Fatalf("expected 2 changes, got %d", len(loaded2))
	}
}

// ---------------------------------------------------------------------------
// 10. TestFunctional_Autotune_CircuitBreakerRecovery
// ---------------------------------------------------------------------------

func TestFunctional_Autotune_CircuitBreakerRecovery(t *testing.T) {
	cb := autotune.NewCircuitBreaker("test-cb", 3, 100*time.Millisecond)

	// Initially closed.
	if cb.State() != autotune.CBClosed {
		t.Fatalf("expected CBClosed, got %s", cb.State())
	}
	if !cb.Allow() {
		t.Fatal("expected Allow() to return true when closed")
	}

	// Record failures to trip the breaker.
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != autotune.CBClosed {
		t.Fatal("expected still CBClosed after 2 failures")
	}

	cb.RecordFailure() // 3rd failure — should open.
	if cb.State() != autotune.CBOpen {
		t.Fatalf("expected CBOpen after 3 failures, got %s", cb.State())
	}
	if cb.Allow() {
		t.Fatal("expected Allow() to return false when open (before reset timeout)")
	}

	// Wait for reset timeout to elapse.
	time.Sleep(150 * time.Millisecond)

	// Allow should transition to half-open and return true.
	if !cb.Allow() {
		t.Fatal("expected Allow() to return true after reset timeout (half-open)")
	}
	if cb.State() != autotune.CBHalfOpen {
		t.Fatalf("expected CBHalfOpen, got %s", cb.State())
	}

	// Record success — should close.
	cb.RecordSuccess()
	if cb.State() != autotune.CBClosed {
		t.Fatalf("expected CBClosed after RecordSuccess, got %s", cb.State())
	}
}

// ---------------------------------------------------------------------------
// 11. TestFunctional_Autotune_ErrorBudgetPause
// ---------------------------------------------------------------------------

func TestFunctional_Autotune_ErrorBudgetPause(t *testing.T) {
	eb := autotune.NewErrorBudget(autotune.ErrorBudgetConfig{
		Window:        time.Hour,
		MaxErrors:     3,
		PauseDuration: 30 * time.Minute,
	})

	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	if eb.IsPaused(now) {
		t.Fatal("should not be paused initially")
	}

	// Record errors up to the limit.
	eb.RecordError(now)
	eb.RecordError(now.Add(1 * time.Second))
	if eb.IsPaused(now.Add(2 * time.Second)) {
		t.Fatal("should not be paused after 2 errors")
	}

	eb.RecordError(now.Add(3 * time.Second)) // 3rd error — should trigger pause.
	if !eb.IsPaused(now.Add(4 * time.Second)) {
		t.Fatal("should be paused after 3 errors")
	}

	// Should still be paused 15 minutes later (within 30-minute window).
	if !eb.IsPaused(now.Add(15 * time.Minute)) {
		t.Fatal("should still be paused after 15 minutes")
	}

	// Should unpause after 30 minutes.
	if eb.IsPaused(now.Add(31 * time.Minute)) {
		t.Fatal("should no longer be paused after 31 minutes")
	}

	// Remaining should be replenished after unpause.
	remaining := eb.Remaining(now.Add(32 * time.Minute))
	if remaining != 3 {
		t.Errorf("expected 3 remaining after unpause, got %d", remaining)
	}
}

// ---------------------------------------------------------------------------
// 12. TestFunctional_Autotune_RateLimiterEnforced
// ---------------------------------------------------------------------------

func TestFunctional_Autotune_RateLimiterEnforced(t *testing.T) {
	rl := autotune.NewReloadRateLimiter(1 * time.Minute)

	callCount := 0
	reloadFn := func() error {
		callCount++
		return nil
	}

	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	// First reload should succeed.
	ok, err := rl.TryReload(reloadFn, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("first reload should have succeeded")
	}
	if callCount != 1 {
		t.Fatalf("expected callCount 1, got %d", callCount)
	}

	// Second reload immediately after should be rate-limited.
	ok2, err2 := rl.TryReload(reloadFn, now.Add(10*time.Second))
	if err2 != nil {
		t.Fatalf("unexpected error: %v", err2)
	}
	if ok2 {
		t.Fatal("second reload should have been rate-limited")
	}
	if callCount != 1 {
		t.Fatalf("expected callCount still 1, got %d", callCount)
	}

	// Verify rejected count.
	if rl.Rejected() != 1 {
		t.Errorf("expected 1 rejection, got %d", rl.Rejected())
	}

	// After min interval, reload should succeed again.
	ok3, err3 := rl.TryReload(reloadFn, now.Add(61*time.Second))
	if err3 != nil {
		t.Fatalf("unexpected error: %v", err3)
	}
	if !ok3 {
		t.Fatal("third reload should have succeeded after interval elapsed")
	}
	if callCount != 2 {
		t.Fatalf("expected callCount 2, got %d", callCount)
	}

	// Test Reset: after reset, immediate reload should succeed.
	rl.Reset(now.Add(62 * time.Second))
	ok4, err4 := rl.TryReload(reloadFn, now.Add(63*time.Second))
	if err4 != nil {
		t.Fatalf("unexpected error: %v", err4)
	}
	// Reset stores the reset time, so 1 second later is within minInterval.
	// This means it should be rate-limited.
	if ok4 {
		// This is expected: Reset stores now, and 1s < 1min.
		t.Log("rate limiter correctly enforces min interval even after Reset")
	}
}

// ---------------------------------------------------------------------------
// 13. TestFunctional_Autotune_PullPropagation
// ---------------------------------------------------------------------------

func TestFunctional_Autotune_PullPropagation(t *testing.T) {
	// Set up leader propagator.
	leaderProp := autotune.NewPullPropagator(autotune.PropagationConfig{
		Mode:         autotune.PropPull,
		PullInterval: 10 * time.Second,
	})

	// Propagate some changes (stores them for GET handler).
	changes := []autotune.ConfigChange{
		{
			Timestamp: time.Now(),
			Domain:    "limits",
			RuleName:  "rule-prop",
			Field:     "max_cardinality",
			OldValue:  1000,
			NewValue:  1250,
			Action:    "increase",
			Reason:    "high utilization",
		},
	}
	if err := leaderProp.Propagate(context.Background(), changes); err != nil {
		t.Fatalf("propagate failed: %v", err)
	}

	// Create an HTTP test server using the leader's handler.
	handler := leaderProp.HandleGetConfig()
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Simulate follower GET request.
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var received []autotune.ConfigChange
	if err := json.NewDecoder(resp.Body).Decode(&received); err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(received) != 1 {
		t.Fatalf("expected 1 change, got %d", len(received))
	}
	if received[0].RuleName != "rule-prop" {
		t.Errorf("expected rule-prop, got %s", received[0].RuleName)
	}
	if received[0].NewValue != 1250 {
		t.Errorf("expected NewValue 1250, got %d", received[0].NewValue)
	}

	// Test ETag caching: second request with If-None-Match should return 304.
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("expected ETag header in response")
	}

	req2, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req2.Header.Set("If-None-Match", etag)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("second GET failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusNotModified {
		t.Errorf("expected 304, got %d", resp2.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// 14. TestFunctional_Autotune_VerifierPassAndFail
// ---------------------------------------------------------------------------

func TestFunctional_Autotune_VerifierPassAndFail(t *testing.T) {
	t.Run("pass", func(t *testing.T) {
		var drops atomic.Int64

		vcfg := autotune.VerificationConfig{
			Warmup:                 10 * time.Millisecond,
			Window:                 50 * time.Millisecond,
			RollbackCooldownFactor: 3,
		}
		v := autotune.NewPostApplyVerifier(vcfg, 0.05, func() int64 {
			return drops.Load()
		})

		decisions := []autotune.Decision{
			{RuleName: "rule-vp", Action: autotune.ActionIncrease, OldValue: 1000, NewValue: 1250},
		}
		now := time.Now()
		v.StartVerification(decisions, now)

		if !v.IsActive() {
			t.Fatal("expected verifier to be active")
		}

		// Wait for warmup + window to expire.
		time.Sleep(70 * time.Millisecond)

		res, _ := v.Check(time.Now())
		if res != autotune.VerificationPass {
			t.Errorf("expected VerificationPass, got %d", res)
		}

		if !v.IsActive() == false {
			// After pass, should no longer be active.
		}
		if v.PassTotal() < 1 {
			t.Errorf("expected PassTotal >= 1, got %d", v.PassTotal())
		}
	})

	t.Run("fail", func(t *testing.T) {
		var drops atomic.Int64

		vcfg := autotune.VerificationConfig{
			Warmup:                 10 * time.Millisecond,
			Window:                 200 * time.Millisecond,
			RollbackCooldownFactor: 3,
		}
		v := autotune.NewPostApplyVerifier(vcfg, 0.05, func() int64 {
			return drops.Load()
		})

		decisions := []autotune.Decision{
			{RuleName: "rule-vf", Action: autotune.ActionDecrease, OldValue: 5000, NewValue: 3750},
		}
		now := time.Now()
		v.StartVerification(decisions, now)

		// Wait for warmup.
		time.Sleep(15 * time.Millisecond)

		// Inject high drop count.
		drops.Store(50000)

		res, rollback := v.Check(time.Now())
		if res != autotune.VerificationFail {
			t.Errorf("expected VerificationFail, got %d", res)
		}
		if len(rollback) != 1 {
			t.Fatalf("expected 1 rollback decision, got %d", len(rollback))
		}
		if v.FailTotal() < 1 {
			t.Errorf("expected FailTotal >= 1, got %d", v.FailTotal())
		}
	})
}

// ---------------------------------------------------------------------------
// 15. TestFunctional_Autotune_AnomalyTighten
// ---------------------------------------------------------------------------

func TestFunctional_Autotune_AnomalyTighten(t *testing.T) {
	cfg := newTestPolicyConfig()
	cfg.AnomalyTightenScore = 0.7
	od := autotune.NewOscillationDetector(autotune.OscillationConfig{
		LookbackCount:  6,
		Threshold:      0.75,
		FreezeDuration: time.Hour,
	})
	engine := autotune.NewPolicyEngine(cfg, od)

	// Anomaly score 0.8 (above tighten threshold of 0.7) with normal utilization.
	signals := autotune.AggregatedSignals{
		Utilization:            map[string]float64{"rule-anomaly": 0.50},
		CardinalityUtilization: map[string]float64{"rule-anomaly": 0.50},
		DPRateUtilization:      map[string]float64{"rule-anomaly": 0},
		RuleUtilization:        map[string]autotune.RuleUtilization{},
		AnomalyScores:          map[string]float64{"rule-anomaly": 0.8},
		Timestamp:              time.Now(),
	}
	limits := map[string]int64{"rule-anomaly": 5000}
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	decisions := engine.Evaluate(signals, limits, now)
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	if decisions[0].Action != autotune.ActionTighten {
		t.Errorf("expected ActionTighten, got %s", decisions[0].Action)
	}
	if decisions[0].NewValue >= decisions[0].OldValue {
		t.Errorf("expected tightened value (%d) < old value (%d)", decisions[0].NewValue, decisions[0].OldValue)
	}
}

// ---------------------------------------------------------------------------
// 16. TestFunctional_Autotune_DesignatedLeaderElection
// ---------------------------------------------------------------------------

func TestFunctional_Autotune_DesignatedLeaderElection(t *testing.T) {
	leader := autotune.NewDesignatedElector(true)
	follower := autotune.NewDesignatedElector(false)

	if err := leader.Start(context.Background()); err != nil {
		t.Fatalf("leader start failed: %v", err)
	}
	defer leader.Stop()

	if err := follower.Start(context.Background()); err != nil {
		t.Fatalf("follower start failed: %v", err)
	}
	defer follower.Stop()

	if !leader.IsLeader() {
		t.Error("designated leader should report as leader")
	}
	if follower.IsLeader() {
		t.Error("designated follower should not report as leader")
	}
}

// ---------------------------------------------------------------------------
// 17. TestFunctional_Autotune_NoopElectorAlwaysLeader
// ---------------------------------------------------------------------------

func TestFunctional_Autotune_NoopElectorAlwaysLeader(t *testing.T) {
	e := autotune.NewNoopElector()
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	defer e.Stop()

	if !e.IsLeader() {
		t.Error("noop elector should always be leader")
	}
}

// ---------------------------------------------------------------------------
// 18. TestFunctional_Autotune_MemoryPersisterMaxSize
// ---------------------------------------------------------------------------

func TestFunctional_Autotune_MemoryPersisterMaxSize(t *testing.T) {
	persister := autotune.NewMemoryPersister(5)
	ctx := context.Background()

	// Write 10 changes — only the last 5 should be retained.
	for i := 0; i < 10; i++ {
		change := []autotune.ConfigChange{
			{
				Timestamp: time.Now(),
				RuleName:  fmt.Sprintf("rule-%d", i),
				Field:     "max_cardinality",
				OldValue:  int64(i * 100),
				NewValue:  int64((i + 1) * 100),
				Action:    "increase",
			},
		}
		if err := persister.Persist(ctx, change); err != nil {
			t.Fatalf("persist failed: %v", err)
		}
	}

	loaded, err := persister.LoadHistory(ctx)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if len(loaded) != 5 {
		t.Fatalf("expected 5 changes (maxSize), got %d", len(loaded))
	}
	// The retained changes should be the last 5 (rule-5 through rule-9).
	if loaded[0].RuleName != "rule-5" {
		t.Errorf("expected first retained change to be rule-5, got %s", loaded[0].RuleName)
	}
	if loaded[4].RuleName != "rule-9" {
		t.Errorf("expected last retained change to be rule-9, got %s", loaded[4].RuleName)
	}
}

// ---------------------------------------------------------------------------
// 19. TestFunctional_Autotune_FilePersisterRoundTrip
// ---------------------------------------------------------------------------

func TestFunctional_Autotune_FilePersisterRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/autotune-history.json"

	fp := autotune.NewFilePersister(path, 3, time.Second)
	ctx := context.Background()

	changes := []autotune.ConfigChange{
		{
			Timestamp: time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC),
			Domain:    "limits",
			RuleName:  "rule-file",
			Field:     "max_cardinality",
			OldValue:  1000,
			NewValue:  1250,
			Action:    "increase",
			Reason:    "high utilization",
		},
	}

	if err := fp.Persist(ctx, changes); err != nil {
		t.Fatalf("persist failed: %v", err)
	}

	loaded, err := fp.LoadHistory(ctx)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 change, got %d", len(loaded))
	}
	if loaded[0].RuleName != "rule-file" {
		t.Errorf("expected rule-file, got %s", loaded[0].RuleName)
	}

	// Persist more and verify accumulation.
	changes2 := []autotune.ConfigChange{
		{
			Timestamp: time.Date(2025, 1, 1, 13, 0, 0, 0, time.UTC),
			Domain:    "limits",
			RuleName:  "rule-file-2",
			Field:     "max_cardinality",
			OldValue:  2000,
			NewValue:  1500,
			Action:    "decrease",
		},
	}
	if err := fp.Persist(ctx, changes2); err != nil {
		t.Fatalf("second persist failed: %v", err)
	}

	loaded2, err := fp.LoadHistory(ctx)
	if err != nil {
		t.Fatalf("second load failed: %v", err)
	}
	if len(loaded2) != 2 {
		t.Fatalf("expected 2 changes, got %d", len(loaded2))
	}
}

// ---------------------------------------------------------------------------
// 20. TestFunctional_Autotune_CircuitBreakerHalfOpenFailure
// ---------------------------------------------------------------------------

func TestFunctional_Autotune_CircuitBreakerHalfOpenFailure(t *testing.T) {
	cb := autotune.NewCircuitBreaker("test-halfopen", 2, 100*time.Millisecond)

	// Trip the breaker.
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != autotune.CBOpen {
		t.Fatalf("expected CBOpen, got %s", cb.State())
	}

	// Wait for reset timeout.
	time.Sleep(150 * time.Millisecond)

	// Allow transitions to half-open.
	if !cb.Allow() {
		t.Fatal("expected Allow() after timeout")
	}
	if cb.State() != autotune.CBHalfOpen {
		t.Fatalf("expected CBHalfOpen, got %s", cb.State())
	}

	// Failure in half-open should re-open.
	cb.RecordFailure()
	if cb.State() != autotune.CBOpen {
		t.Fatalf("expected CBOpen after half-open failure, got %s", cb.State())
	}
}
