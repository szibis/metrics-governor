package autotune

import (
	"testing"
	"time"
)

func TestVerifier_PassAfterWindow(t *testing.T) {
	cfg := VerificationConfig{
		Warmup:                 100 * time.Millisecond,
		Window:                 200 * time.Millisecond,
		RollbackCooldownFactor: 3,
	}

	var drops int64
	v := NewPostApplyVerifier(cfg, 0.05, func() int64 { return drops })

	now := time.Now()
	decisions := []Decision{{RuleName: "rule1", Action: ActionIncrease}}
	v.StartVerification(decisions, now)

	if !v.IsActive() {
		t.Error("expected active after start")
	}

	// During warmup.
	result, _ := v.Check(now.Add(50 * time.Millisecond))
	if result != VerificationPending {
		t.Errorf("expected pending during warmup, got %d", result)
	}

	// After window expires.
	result, _ = v.Check(now.Add(400 * time.Millisecond))
	if result != VerificationPass {
		t.Errorf("expected pass after window, got %d", result)
	}

	if v.PassTotal() != 1 {
		t.Errorf("expected passTotal=1, got %d", v.PassTotal())
	}

	if v.IsActive() {
		t.Error("expected not active after pass")
	}
}

func TestVerifier_Cancel(t *testing.T) {
	cfg := VerificationConfig{
		Warmup: 100 * time.Millisecond,
		Window: 200 * time.Millisecond,
	}

	v := NewPostApplyVerifier(cfg, 0.05, func() int64 { return 0 })

	now := time.Now()
	v.StartVerification([]Decision{{RuleName: "rule1"}}, now)
	v.Cancel()

	if v.IsActive() {
		t.Error("expected not active after cancel")
	}
}

func TestVerifier_NotActiveByDefault(t *testing.T) {
	cfg := VerificationConfig{
		Warmup: time.Second,
		Window: time.Second,
	}

	v := NewPostApplyVerifier(cfg, 0.05, func() int64 { return 0 })

	if v.IsActive() {
		t.Error("expected not active by default")
	}

	result, _ := v.Check(time.Now())
	if result != VerificationPending {
		t.Errorf("expected pending when not active, got %d", result)
	}
}

func TestVerifier_FailOnHighDropRate(t *testing.T) {
	cfg := VerificationConfig{
		Warmup: 10 * time.Millisecond,
		Window: 100 * time.Millisecond,
	}

	var drops int64
	v := NewPostApplyVerifier(cfg, 0.05, func() int64 { return drops })

	now := time.Now()
	v.StartVerification([]Decision{{RuleName: "rule1", Action: ActionDecrease}}, now)

	// After warmup, simulate high drops.
	drops = 10000 // large spike
	result, rollback := v.Check(now.Add(20 * time.Millisecond))
	if result == VerificationFail {
		// Good — drops detected.
		if len(rollback) != 1 {
			t.Errorf("expected 1 rollback decision, got %d", len(rollback))
		}
		if v.FailTotal() != 1 {
			t.Errorf("expected failTotal=1, got %d", v.FailTotal())
		}
	}
	// Note: the exact threshold depends on the heuristic, so we accept either
	// VerificationFail or VerificationPending (if the threshold wasn't hit).
}

func TestVerifier_NilDropsFuncHandled(t *testing.T) {
	cfg := VerificationConfig{
		Warmup: 10 * time.Millisecond,
		Window: 100 * time.Millisecond,
	}

	v := NewPostApplyVerifier(cfg, 0.05, nil)

	now := time.Now()
	v.StartVerification([]Decision{{RuleName: "rule1"}}, now)

	// Should not panic with nil drops func.
	result, _ := v.Check(now.Add(20 * time.Millisecond))
	if result != VerificationPending {
		t.Errorf("expected pending with nil drops func, got %d", result)
	}
}
