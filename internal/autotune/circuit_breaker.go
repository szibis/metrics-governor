package autotune

import (
	"sync"
	"sync/atomic"
	"time"
)

// CBState represents the circuit breaker state.
type CBState int32

const (
	CBClosed   CBState = 0
	CBOpen     CBState = 1
	CBHalfOpen CBState = 2
)

// String returns the state name.
func (s CBState) String() string {
	switch s {
	case CBClosed:
		return "closed"
	case CBOpen:
		return "open"
	case CBHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreaker implements a standard circuit breaker pattern.
// Thread-safe for concurrent use.
type CircuitBreaker struct {
	name         string
	maxFailures  int
	resetTimeout time.Duration

	mu                  sync.Mutex
	state               atomic.Int32 // CBState
	consecutiveFailures int
	lastFailure         time.Time
	lastTransition      time.Time

	// Metrics callbacks (optional).
	onTransition func(name string, from, to CBState)
}

// NewCircuitBreaker creates a circuit breaker with the given config.
// If maxFailures is 0, the breaker is effectively disabled (always closed).
func NewCircuitBreaker(name string, maxFailures int, resetTimeout time.Duration) *CircuitBreaker {
	cb := &CircuitBreaker{
		name:         name,
		maxFailures:  maxFailures,
		resetTimeout: resetTimeout,
	}
	cb.state.Store(int32(CBClosed))
	return cb
}

// SetTransitionCallback sets an optional callback for state transitions.
func (cb *CircuitBreaker) SetTransitionCallback(fn func(name string, from, to CBState)) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.onTransition = fn
}

// State returns the current state.
func (cb *CircuitBreaker) State() CBState {
	return CBState(cb.state.Load())
}

// Name returns the circuit breaker name.
func (cb *CircuitBreaker) Name() string {
	return cb.name
}

// Allow checks whether a request is allowed through the circuit breaker.
// Returns true if the request should proceed.
func (cb *CircuitBreaker) Allow() bool {
	if cb.maxFailures <= 0 {
		return true // disabled
	}

	state := CBState(cb.state.Load())
	switch state {
	case CBClosed:
		return true
	case CBOpen:
		cb.mu.Lock()
		defer cb.mu.Unlock()
		// Check if reset timeout has elapsed.
		if time.Since(cb.lastFailure) >= cb.resetTimeout {
			cb.transition(CBHalfOpen)
			return true // allow one probe
		}
		return false
	case CBHalfOpen:
		return true // allow probe request
	default:
		return true
	}
}

// RecordSuccess records a successful operation. Resets the breaker to CLOSED.
func (cb *CircuitBreaker) RecordSuccess() {
	if cb.maxFailures <= 0 {
		return
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.consecutiveFailures = 0
	if CBState(cb.state.Load()) != CBClosed {
		cb.transition(CBClosed)
	}
}

// RecordFailure records a failed operation. May trip the breaker to OPEN.
func (cb *CircuitBreaker) RecordFailure() {
	if cb.maxFailures <= 0 {
		return
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.consecutiveFailures++
	cb.lastFailure = time.Now()

	state := CBState(cb.state.Load())
	switch state {
	case CBClosed:
		if cb.consecutiveFailures >= cb.maxFailures {
			cb.transition(CBOpen)
		}
	case CBHalfOpen:
		// Probe failed — go back to OPEN.
		cb.transition(CBOpen)
	}
}

// transition changes the state (must be called under mu.Lock).
func (cb *CircuitBreaker) transition(to CBState) {
	from := CBState(cb.state.Load())
	cb.state.Store(int32(to))
	cb.lastTransition = time.Now()
	if cb.onTransition != nil {
		cb.onTransition(cb.name, from, to)
	}
}

// ConsecutiveFailures returns the current failure count.
func (cb *CircuitBreaker) ConsecutiveFailures() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.consecutiveFailures
}
