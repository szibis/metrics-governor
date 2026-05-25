package autotune

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// ErrReloadInProgress is returned when a reload is already running.
var ErrReloadInProgress = errors.New("reload already in progress")

// ErrRateLimited is returned when a reload is rejected by rate limiting.
var ErrRateLimited = errors.New("reload rate limited")

// --- ReloadRateLimiter ---

// ReloadRateLimiter enforces a minimum interval between autotune-triggered reloads.
// Manual SIGHUP bypasses and resets the limiter.
type ReloadRateLimiter struct {
	minInterval time.Duration
	lastReload  atomic.Int64 // unix nanoseconds of last successful reload
	mu          sync.Mutex

	// Metrics.
	rejected atomic.Int64
}

// NewReloadRateLimiter creates a rate limiter with the given minimum interval.
func NewReloadRateLimiter(minInterval time.Duration) *ReloadRateLimiter {
	return &ReloadRateLimiter{
		minInterval: minInterval,
	}
}

// TryReload attempts to execute fn if enough time has passed since the last reload.
// Returns (true, nil) on success, (false, nil) if rate limited, (false, err) on fn error.
func (r *ReloadRateLimiter) TryReload(fn func() error, now time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	last := time.Unix(0, r.lastReload.Load())
	if !last.IsZero() && now.Sub(last) < r.minInterval {
		r.rejected.Add(1)
		return false, nil
	}
	if err := fn(); err != nil {
		return false, err
	}
	r.lastReload.Store(now.UnixNano())
	return true, nil
}

// Reset resets the rate limiter timer (called when SIGHUP is received).
func (r *ReloadRateLimiter) Reset(now time.Time) {
	r.lastReload.Store(now.UnixNano())
}

// Rejected returns the total number of rate-limited rejections.
func (r *ReloadRateLimiter) Rejected() int64 {
	return r.rejected.Load()
}

// --- ReloadCoordinator ---

// ReloadCoordinator prevents concurrent reloads and coalesces requests.
type ReloadCoordinator struct {
	reloading atomic.Bool
	pending   atomic.Bool
	rejected  atomic.Int64
}

// NewReloadCoordinator creates a reload coordinator.
func NewReloadCoordinator() *ReloadCoordinator {
	return &ReloadCoordinator{}
}

// Execute runs fn if no reload is currently in progress.
// Returns ErrReloadInProgress if a reload is already running.
func (rc *ReloadCoordinator) Execute(fn func() error) error {
	if !rc.reloading.CompareAndSwap(false, true) {
		rc.pending.Store(true)
		rc.rejected.Add(1)
		return ErrReloadInProgress
	}
	defer rc.reloading.Store(false)

	err := fn()
	rc.pending.Store(false)
	return err
}

// IsPending returns true if another reload was requested during the current one.
func (rc *ReloadCoordinator) IsPending() bool {
	return rc.pending.Load()
}

// Rejected returns the total number of rejected concurrent reloads.
func (rc *ReloadCoordinator) Rejected() int64 {
	return rc.rejected.Load()
}

// --- OscillationDetector ---

// OscillationDetector tracks recent decision history per rule to detect flip-flopping.
type OscillationDetector struct {
	mu        sync.Mutex
	history   map[string][]Decision // last N decisions per rule
	frozen    map[string]time.Time  // rule -> freeze expiry
	lookback  int
	threshold float64
	freezeDur time.Duration

	// Metrics.
	freezes atomic.Int64
}

// NewOscillationDetector creates an oscillation detector.
func NewOscillationDetector(cfg OscillationConfig) *OscillationDetector {
	return &OscillationDetector{
		history:   make(map[string][]Decision),
		frozen:    make(map[string]time.Time),
		lookback:  cfg.LookbackCount,
		threshold: cfg.Threshold,
		freezeDur: cfg.FreezeDuration,
	}
}

// Record adds a decision to the history for a rule.
func (od *OscillationDetector) Record(ruleName string, d Decision) {
	od.mu.Lock()
	defer od.mu.Unlock()

	h := od.history[ruleName]
	h = append(h, d)
	if len(h) > od.lookback {
		h = h[len(h)-od.lookback:]
	}
	od.history[ruleName] = h

	// Check for oscillation after recording.
	if od.isOscillating(h) {
		od.frozen[ruleName] = d.Timestamp.Add(od.freezeDur)
		od.freezes.Add(1)
	}
}

// IsFrozen returns true if the rule is currently frozen due to oscillation.
func (od *OscillationDetector) IsFrozen(ruleName string, now time.Time) bool {
	od.mu.Lock()
	defer od.mu.Unlock()
	expiry, ok := od.frozen[ruleName]
	if !ok {
		return false
	}
	if now.After(expiry) {
		delete(od.frozen, ruleName)
		return false
	}
	return true
}

// isOscillating checks if the history shows an alternating pattern (must be called under mu.Lock).
func (od *OscillationDetector) isOscillating(h []Decision) bool {
	if len(h) < 4 {
		return false
	}

	alternations := 0
	for i := 1; i < len(h); i++ {
		if h[i].Action != h[i-1].Action {
			alternations++
		}
	}
	ratio := float64(alternations) / float64(len(h)-1)
	return ratio > od.threshold
}

// Freezes returns the total number of oscillation freezes.
func (od *OscillationDetector) Freezes() int64 {
	return od.freezes.Load()
}

// --- ErrorBudget ---

// ErrorBudget tracks cumulative errors in a rolling window and pauses autotune when exhausted.
type ErrorBudget struct {
	mu            sync.Mutex
	window        time.Duration
	maxErrors     int
	pauseDuration time.Duration
	errors        []time.Time
	pauseUntil    time.Time
}

// NewErrorBudget creates an error budget.
func NewErrorBudget(cfg ErrorBudgetConfig) *ErrorBudget {
	return &ErrorBudget{
		window:        cfg.Window,
		maxErrors:     cfg.MaxErrors,
		pauseDuration: cfg.PauseDuration,
	}
}

// RecordError records an error and may trigger a pause.
func (eb *ErrorBudget) RecordError(now time.Time) {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.errors = append(eb.errors, now)
	eb.pruneOld(now)
	if len(eb.errors) >= eb.maxErrors {
		eb.pauseUntil = now.Add(eb.pauseDuration)
	}
}

// IsPaused returns true if the error budget is exhausted and autotune should pause.
func (eb *ErrorBudget) IsPaused(now time.Time) bool {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	if eb.pauseUntil.IsZero() {
		return false
	}
	if now.After(eb.pauseUntil) {
		eb.pauseUntil = time.Time{}
		eb.errors = nil // reset on unpause
		return false
	}
	return true
}

// Remaining returns the number of errors left before pause.
func (eb *ErrorBudget) Remaining(now time.Time) int {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.pruneOld(now)
	rem := eb.maxErrors - len(eb.errors)
	if rem < 0 {
		rem = 0
	}
	return rem
}

// Reset clears the error budget (used on manual SIGHUP).
func (eb *ErrorBudget) Reset() {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.errors = nil
	eb.pauseUntil = time.Time{}
}

// pruneOld removes errors outside the rolling window (must be called under mu.Lock).
func (eb *ErrorBudget) pruneOld(now time.Time) {
	cutoff := now.Add(-eb.window)
	i := 0
	for i < len(eb.errors) && eb.errors[i].Before(cutoff) {
		i++
	}
	if i > 0 {
		eb.errors = eb.errors[i:]
	}
}
