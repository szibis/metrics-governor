package exporter

import (
	"context"
	"runtime"
	"time"
)

// ConcurrencyLimiter provides a semaphore-based mechanism to limit concurrent operations.
// It prevents goroutine explosion in parallel operations like sharded exports.
//
// This pattern is inspired by concurrency control techniques described in VictoriaMetrics
// blog articles (https://valyala.medium.com/). The code is an original implementation
// using the standard Go channel-based semaphore pattern.
type ConcurrencyLimiter struct {
	sem chan struct{}
}

// NewConcurrencyLimiter creates a new concurrency limiter with the specified limit.
// If limit is <= 0, it defaults to runtime.NumCPU() * 4.
func NewConcurrencyLimiter(limit int) *ConcurrencyLimiter {
	if limit <= 0 {
		limit = runtime.NumCPU() * 4
	}
	return &ConcurrencyLimiter{
		sem: make(chan struct{}, limit),
	}
}

// Acquire blocks until a slot is available.
func (l *ConcurrencyLimiter) Acquire() {
	l.sem <- struct{}{}
}

// Release returns a slot to the pool.
// Must be called after Acquire() completes its work.
func (l *ConcurrencyLimiter) Release() {
	<-l.sem
}

// TryAcquire attempts to acquire a slot without blocking.
// Returns true if acquired, false if all slots are in use.
func (l *ConcurrencyLimiter) TryAcquire() bool {
	select {
	case l.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

// AcquireContext blocks until a slot is available or the context is canceled.
// Returns nil if acquired, or the context error if canceled.
func (l *ConcurrencyLimiter) AcquireContext(ctx context.Context) error {
	select {
	case l.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// AcquireTimeout attempts to acquire a slot within the specified timeout.
// Returns true if acquired within the timeout, false otherwise.
func (l *ConcurrencyLimiter) AcquireTimeout(timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case l.sem <- struct{}{}:
		return true
	case <-timer.C:
		return false
	}
}

// Limit returns the maximum number of concurrent operations allowed.
func (l *ConcurrencyLimiter) Limit() int {
	return cap(l.sem)
}

// Available returns the number of slots currently available.
// Note: This is a snapshot and may change immediately after the call.
func (l *ConcurrencyLimiter) Available() int {
	return cap(l.sem) - len(l.sem)
}

// InUse returns the number of slots currently in use.
// Note: This is a snapshot and may change immediately after the call.
func (l *ConcurrencyLimiter) InUse() int {
	return len(l.sem)
}

// DefaultExportConcurrency returns the default concurrency limit for exports.
func DefaultExportConcurrency() int {
	return runtime.NumCPU() * 4
}
