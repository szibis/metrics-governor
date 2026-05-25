package autotune

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// --- ReloadRateLimiter Tests ---

func TestRateLimiter_FirstCallAllowed(t *testing.T) {
	rl := NewReloadRateLimiter(time.Minute)
	called := false
	ok, err := rl.TryReload(func() error { called = true; return nil }, time.Now())
	if !ok || err != nil {
		t.Errorf("first call: ok=%v, err=%v", ok, err)
	}
	if !called {
		t.Error("fn was not called")
	}
}

func TestRateLimiter_SecondCallRejected(t *testing.T) {
	rl := NewReloadRateLimiter(time.Minute)
	now := time.Now()
	rl.TryReload(func() error { return nil }, now)

	ok, err := rl.TryReload(func() error { return nil }, now.Add(30*time.Second))
	if ok || err != nil {
		t.Errorf("expected rate limited: ok=%v, err=%v", ok, err)
	}
	if rl.Rejected() != 1 {
		t.Errorf("expected 1 rejection, got %d", rl.Rejected())
	}
}

func TestRateLimiter_AllowedAfterInterval(t *testing.T) {
	rl := NewReloadRateLimiter(time.Minute)
	now := time.Now()
	rl.TryReload(func() error { return nil }, now)

	ok, _ := rl.TryReload(func() error { return nil }, now.Add(65*time.Second))
	if !ok {
		t.Error("expected allowed after interval")
	}
}

func TestRateLimiter_FailedReloadDoesNotResetTimer(t *testing.T) {
	rl := NewReloadRateLimiter(time.Minute)
	now := time.Now()

	testErr := errors.New("test error")
	ok, err := rl.TryReload(func() error { return testErr }, now)
	if ok || err != testErr {
		t.Errorf("expected failure: ok=%v, err=%v", ok, err)
	}

	// Should still allow because the failed reload didn't count.
	ok, _ = rl.TryReload(func() error { return nil }, now.Add(time.Second))
	if !ok {
		t.Error("expected allowed after failed reload")
	}
}

func TestRateLimiter_SIGHUPResets(t *testing.T) {
	rl := NewReloadRateLimiter(time.Minute)
	now := time.Now()
	rl.TryReload(func() error { return nil }, now)

	// SIGHUP resets timer.
	rl.Reset(now.Add(10 * time.Second))

	// Should be rate limited relative to the SIGHUP time.
	ok, _ := rl.TryReload(func() error { return nil }, now.Add(30*time.Second))
	if ok {
		t.Error("expected rate limited after SIGHUP reset")
	}

	ok, _ = rl.TryReload(func() error { return nil }, now.Add(75*time.Second))
	if !ok {
		t.Error("expected allowed after SIGHUP + interval")
	}
}

// --- ReloadCoordinator Tests ---

func TestCoordinator_SingleCall(t *testing.T) {
	rc := NewReloadCoordinator()
	err := rc.Execute(func() error { return nil })
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestCoordinator_ConcurrentCallRejected(t *testing.T) {
	rc := NewReloadCoordinator()

	started := make(chan struct{})
	done := make(chan struct{})

	// First call: blocks.
	go func() {
		rc.Execute(func() error {
			close(started)
			<-done
			return nil
		})
	}()

	<-started

	// Second call: rejected.
	err := rc.Execute(func() error { return nil })
	if !errors.Is(err, ErrReloadInProgress) {
		t.Errorf("expected ErrReloadInProgress, got %v", err)
	}
	if rc.Rejected() != 1 {
		t.Errorf("expected 1 rejection, got %d", rc.Rejected())
	}

	close(done)
}

// --- OscillationDetector Tests ---

func TestOscillation_NotDetectedWithFewDecisions(t *testing.T) {
	od := NewOscillationDetector(OscillationConfig{
		LookbackCount:  6,
		Threshold:      0.75,
		FreezeDuration: time.Hour,
	})

	od.Record("rule1", Decision{Action: ActionIncrease, Timestamp: time.Now()})
	od.Record("rule1", Decision{Action: ActionDecrease, Timestamp: time.Now()})

	if od.IsFrozen("rule1", time.Now()) {
		t.Error("should not be frozen with only 2 decisions")
	}
}

func TestOscillation_NotDetectedWithSameDirection(t *testing.T) {
	od := NewOscillationDetector(OscillationConfig{
		LookbackCount:  6,
		Threshold:      0.75,
		FreezeDuration: time.Hour,
	})

	now := time.Now()
	for i := 0; i < 6; i++ {
		od.Record("rule1", Decision{Action: ActionIncrease, Timestamp: now.Add(time.Duration(i) * time.Second)})
	}

	if od.IsFrozen("rule1", now) {
		t.Error("should not be frozen with all same direction")
	}
}

func TestOscillation_DetectedWithAlternating(t *testing.T) {
	od := NewOscillationDetector(OscillationConfig{
		LookbackCount:  6,
		Threshold:      0.75,
		FreezeDuration: time.Hour,
	})

	now := time.Now()
	actions := []Action{ActionIncrease, ActionDecrease, ActionIncrease, ActionDecrease, ActionIncrease, ActionDecrease}
	for i, a := range actions {
		od.Record("rule1", Decision{Action: a, Timestamp: now.Add(time.Duration(i) * time.Second)})
	}

	if !od.IsFrozen("rule1", now) {
		t.Error("should be frozen with alternating pattern")
	}
	// Record fires oscillation check after each insert; with 6 alternating entries,
	// the pattern is detected multiple times (after 4th, 5th, 6th entries).
	if od.Freezes() < 1 {
		t.Errorf("expected at least 1 freeze, got %d", od.Freezes())
	}
}

func TestOscillation_FreezeExpires(t *testing.T) {
	od := NewOscillationDetector(OscillationConfig{
		LookbackCount:  6,
		Threshold:      0.75,
		FreezeDuration: time.Hour,
	})

	now := time.Now()
	actions := []Action{ActionIncrease, ActionDecrease, ActionIncrease, ActionDecrease, ActionIncrease, ActionDecrease}
	for i, a := range actions {
		od.Record("rule1", Decision{Action: a, Timestamp: now.Add(time.Duration(i) * time.Second)})
	}

	if !od.IsFrozen("rule1", now.Add(30*time.Minute)) {
		t.Error("should still be frozen at 30 min")
	}

	if od.IsFrozen("rule1", now.Add(65*time.Minute)) {
		t.Error("should not be frozen after 65 min (freeze duration is 1h)")
	}
}

// --- ErrorBudget Tests ---

func TestErrorBudget_UnderLimit(t *testing.T) {
	eb := NewErrorBudget(ErrorBudgetConfig{
		Window:        time.Hour,
		MaxErrors:     10,
		PauseDuration: 30 * time.Minute,
	})

	now := time.Now()
	for i := 0; i < 9; i++ {
		eb.RecordError(now.Add(time.Duration(i) * time.Minute))
	}

	if eb.IsPaused(now.Add(10 * time.Minute)) {
		t.Error("should not be paused with 9 errors (limit is 10)")
	}
	if eb.Remaining(now.Add(10*time.Minute)) != 1 {
		t.Errorf("expected 1 remaining, got %d", eb.Remaining(now.Add(10*time.Minute)))
	}
}

func TestErrorBudget_AtLimit(t *testing.T) {
	eb := NewErrorBudget(ErrorBudgetConfig{
		Window:        time.Hour,
		MaxErrors:     10,
		PauseDuration: 30 * time.Minute,
	})

	now := time.Now()
	for i := 0; i < 10; i++ {
		eb.RecordError(now.Add(time.Duration(i) * time.Minute))
	}

	if !eb.IsPaused(now.Add(15 * time.Minute)) {
		t.Error("should be paused with 10 errors")
	}
}

func TestErrorBudget_PauseExpires(t *testing.T) {
	eb := NewErrorBudget(ErrorBudgetConfig{
		Window:        time.Hour,
		MaxErrors:     3,
		PauseDuration: 10 * time.Minute,
	})

	now := time.Now()
	for i := 0; i < 3; i++ {
		eb.RecordError(now)
	}

	if !eb.IsPaused(now.Add(5 * time.Minute)) {
		t.Error("should be paused at T+5m")
	}

	if eb.IsPaused(now.Add(15 * time.Minute)) {
		t.Error("should not be paused at T+15m (pause duration is 10m)")
	}
}

func TestErrorBudget_RollingWindowPrunesOldErrors(t *testing.T) {
	eb := NewErrorBudget(ErrorBudgetConfig{
		Window:        time.Hour,
		MaxErrors:     5,
		PauseDuration: 10 * time.Minute,
	})

	now := time.Now()
	// Record 4 errors in the past (will be pruned).
	for i := 0; i < 4; i++ {
		eb.RecordError(now.Add(-70 * time.Minute))
	}

	// Record 1 recent error.
	eb.RecordError(now)

	if eb.Remaining(now) != 4 {
		t.Errorf("expected 4 remaining (old errors pruned), got %d", eb.Remaining(now))
	}
}

func TestErrorBudget_Reset(t *testing.T) {
	eb := NewErrorBudget(ErrorBudgetConfig{
		Window:        time.Hour,
		MaxErrors:     3,
		PauseDuration: 10 * time.Minute,
	})

	now := time.Now()
	for i := 0; i < 3; i++ {
		eb.RecordError(now)
	}

	if !eb.IsPaused(now) {
		t.Error("should be paused")
	}

	eb.Reset()

	if eb.IsPaused(now) {
		t.Error("should not be paused after reset")
	}
}

// --- Concurrent Safety ---

func TestRateLimiter_ConcurrentSafety(t *testing.T) {
	rl := NewReloadRateLimiter(time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				rl.TryReload(func() error { return nil }, time.Now())
			}
		}()
	}
	wg.Wait()
}

func TestErrorBudget_ConcurrentSafety(t *testing.T) {
	eb := NewErrorBudget(ErrorBudgetConfig{
		Window:        time.Hour,
		MaxErrors:     100,
		PauseDuration: time.Minute,
	})

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				eb.RecordError(time.Now())
				eb.IsPaused(time.Now())
				eb.Remaining(time.Now())
			}
		}()
	}
	wg.Wait()
}
