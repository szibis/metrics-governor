package autotune

import (
	"sync"
	"testing"
	"time"
)

func TestCircuitBreaker_ClosedToOpen(t *testing.T) {
	cb := NewCircuitBreaker("test", 3, 5*time.Minute)

	if cb.State() != CBClosed {
		t.Fatalf("expected CLOSED, got %s", cb.State())
	}

	// Two failures: still closed.
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CBClosed {
		t.Errorf("expected CLOSED after 2 failures, got %s", cb.State())
	}
	if !cb.Allow() {
		t.Error("expected Allow() = true when CLOSED")
	}

	// Third failure: trips to OPEN.
	cb.RecordFailure()
	if cb.State() != CBOpen {
		t.Errorf("expected OPEN after 3 failures, got %s", cb.State())
	}
	if cb.Allow() {
		t.Error("expected Allow() = false when OPEN (before reset timeout)")
	}
}

func TestCircuitBreaker_OpenToHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker("test", 1, 50*time.Millisecond)

	cb.RecordFailure()
	if cb.State() != CBOpen {
		t.Fatalf("expected OPEN, got %s", cb.State())
	}

	// Wait for reset timeout.
	time.Sleep(60 * time.Millisecond)

	// Should transition to HALF-OPEN on next Allow().
	if !cb.Allow() {
		t.Error("expected Allow() = true after reset timeout (HALF-OPEN)")
	}
	if cb.State() != CBHalfOpen {
		t.Errorf("expected HALF-OPEN, got %s", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenToClosedOnSuccess(t *testing.T) {
	cb := NewCircuitBreaker("test", 1, 50*time.Millisecond)

	cb.RecordFailure()
	time.Sleep(60 * time.Millisecond)
	cb.Allow() // transition to HALF-OPEN

	cb.RecordSuccess()
	if cb.State() != CBClosed {
		t.Errorf("expected CLOSED after success in HALF-OPEN, got %s", cb.State())
	}
	if cb.ConsecutiveFailures() != 0 {
		t.Errorf("expected 0 consecutive failures after success, got %d", cb.ConsecutiveFailures())
	}
}

func TestCircuitBreaker_HalfOpenToOpenOnFailure(t *testing.T) {
	cb := NewCircuitBreaker("test", 1, 50*time.Millisecond)

	cb.RecordFailure()
	time.Sleep(60 * time.Millisecond)
	cb.Allow() // transition to HALF-OPEN

	cb.RecordFailure() // probe fails
	if cb.State() != CBOpen {
		t.Errorf("expected OPEN after failure in HALF-OPEN, got %s", cb.State())
	}
}

func TestCircuitBreaker_Disabled(t *testing.T) {
	cb := NewCircuitBreaker("test", 0, time.Minute)

	// Always allows, never trips.
	for i := 0; i < 100; i++ {
		cb.RecordFailure()
	}
	if !cb.Allow() {
		t.Error("expected Allow() = true when disabled")
	}
	if cb.State() != CBClosed {
		t.Errorf("expected CLOSED when disabled, got %s", cb.State())
	}
}

func TestCircuitBreaker_TransitionCallback(t *testing.T) {
	cb := NewCircuitBreaker("test", 1, time.Minute)

	var transitions []struct{ from, to CBState }
	cb.SetTransitionCallback(func(name string, from, to CBState) {
		transitions = append(transitions, struct{ from, to CBState }{from, to})
	})

	cb.RecordFailure() // CLOSED → OPEN
	if len(transitions) != 1 {
		t.Fatalf("expected 1 transition, got %d", len(transitions))
	}
	if transitions[0].from != CBClosed || transitions[0].to != CBOpen {
		t.Errorf("expected CLOSED→OPEN, got %s→%s", transitions[0].from, transitions[0].to)
	}
}

func TestCircuitBreaker_ConcurrentSafety(t *testing.T) {
	cb := NewCircuitBreaker("test", 5, time.Minute)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				cb.Allow()
				if j%2 == 0 {
					cb.RecordFailure()
				} else {
					cb.RecordSuccess()
				}
				_ = cb.State()
			}
		}()
	}
	wg.Wait()

	// Should not panic. State should be valid.
	s := cb.State()
	if s != CBClosed && s != CBOpen && s != CBHalfOpen {
		t.Errorf("invalid state: %d", s)
	}
}
