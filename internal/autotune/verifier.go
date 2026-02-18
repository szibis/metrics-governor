package autotune

import (
	"sync"
	"sync/atomic"
	"time"
)

// VerificationResult is the outcome of post-apply verification.
type VerificationResult int

const (
	VerificationPending VerificationResult = iota
	VerificationPass
	VerificationFail
)

// PostApplyVerifier monitors drop rates after applying autotune decisions
// and triggers rollback if drops exceed the threshold.
type PostApplyVerifier struct {
	cfg VerificationConfig

	mu              sync.Mutex
	active          bool
	applyTime       time.Time
	baselineDrops   int64
	decisions       []Decision
	canceled        bool

	dropsFunc       func() int64 // returns current total drops
	rollbackDropPct float64

	// Metrics.
	passTotal atomic.Int64
	failTotal atomic.Int64
}

// NewPostApplyVerifier creates a verifier.
func NewPostApplyVerifier(cfg VerificationConfig, rollbackDropPct float64, dropsFunc func() int64) *PostApplyVerifier {
	return &PostApplyVerifier{
		cfg:             cfg,
		dropsFunc:       dropsFunc,
		rollbackDropPct: rollbackDropPct,
	}
}

// StartVerification begins monitoring after an apply.
func (v *PostApplyVerifier) StartVerification(decisions []Decision, now time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.active = true
	v.applyTime = now
	v.decisions = decisions
	v.canceled = false
	if v.dropsFunc != nil {
		v.baselineDrops = v.dropsFunc()
	}
}

// Cancel cancels an active verification (called on SIGHUP override).
func (v *PostApplyVerifier) Cancel() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.active = false
	v.canceled = true
	v.decisions = nil
}

// IsActive returns true if a verification window is currently active.
func (v *PostApplyVerifier) IsActive() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.active
}

// Check evaluates the current drop rate against the baseline.
// Returns the verification result and the decisions that should be rolled back (if any).
// Should be called periodically during the verification window.
func (v *PostApplyVerifier) Check(now time.Time) (VerificationResult, []Decision) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if !v.active || v.canceled {
		return VerificationPending, nil
	}

	elapsed := now.Sub(v.applyTime)

	// Still in warmup phase — ignore.
	if elapsed < v.cfg.Warmup {
		return VerificationPending, nil
	}

	// Verification window expired — pass.
	totalWindow := v.cfg.Warmup + v.cfg.Window
	if elapsed >= totalWindow {
		v.active = false
		v.passTotal.Add(1)
		decisions := v.decisions
		v.decisions = nil
		_ = decisions // decisions are "committed" — no rollback needed
		return VerificationPass, nil
	}

	// In measurement window — check drops.
	if v.dropsFunc == nil {
		return VerificationPending, nil
	}

	currentDrops := v.dropsFunc()
	dropsDelta := currentDrops - v.baselineDrops
	if dropsDelta < 0 {
		dropsDelta = 0
	}

	// Calculate drop rate as a fraction.
	// We use drops-per-second normalized by elapsed time in the measurement window.
	measurementElapsed := elapsed - v.cfg.Warmup
	if measurementElapsed <= 0 {
		return VerificationPending, nil
	}

	// Simple: if total drops during measurement exceed threshold fraction of baseline, fail.
	// Threshold is applied as: if drops/second suggests more than rollbackDropPct of
	// the total data would be dropped, trigger rollback.
	// For simplicity: drop rate = dropsDelta / measurement_seconds
	// We fail if dropsDelta > 0 and dropsDelta exceeds a reasonable threshold.
	// Use the configured rollback_drop_pct as the absolute delta threshold
	// relative to total processed (approximated by drops + passed).
	if dropsDelta > 0 && v.rollbackDropPct > 0 {
		// If we're seeing any significant drops that didn't exist before, fail.
		// The threshold is: drops_delta / total_observed > rollbackDropPct
		// Since we don't have total_observed from the enforcer directly,
		// we use a heuristic: any drops exceeding the absolute pct of baseline.
		// For the first implementation, fail if drops are growing faster than threshold.
		dropsPerSec := float64(dropsDelta) / measurementElapsed.Seconds()
		if dropsPerSec > 0 {
			// Fail if we're seeing drops and the drop rate seems significant.
			// Better heuristic: check absolute drops > some threshold based on
			// the expected volume. For now: fail if drops/sec > (rollbackDropPct * 1000).
			// This means with 5% threshold: > 50 drops/sec.
			dropThreshold := v.rollbackDropPct * 1000
			if dropsPerSec > dropThreshold {
				v.active = false
				v.failTotal.Add(1)
				decisions := v.decisions
				v.decisions = nil
				return VerificationFail, decisions
			}
		}
	}

	return VerificationPending, nil
}

// PassTotal returns the total number of verification passes.
func (v *PostApplyVerifier) PassTotal() int64 {
	return v.passTotal.Load()
}

// FailTotal returns the total number of verification failures.
func (v *PostApplyVerifier) FailTotal() int64 {
	return v.failTotal.Load()
}
