package exporter

import (
	"sync"
	"time"

	"github.com/szibis/metrics-governor/internal/queue"
)

// spilloverRateLimiter is a simple token-bucket rate limiter that caps
// disk queue push rate during spillover to prevent IOPS cascade.
type spilloverRateLimiter struct {
	mu         sync.Mutex
	maxPerSec  int
	tokens     int
	lastRefill time.Time
}

// newSpilloverRateLimiter creates a rate limiter that allows maxPerSec operations per second.
func newSpilloverRateLimiter(maxPerSec int) *spilloverRateLimiter {
	return &spilloverRateLimiter{
		maxPerSec:  maxPerSec,
		tokens:     maxPerSec,
		lastRefill: time.Now(),
	}
}

// Allow returns true if a disk push is allowed. If not, the caller should
// skip the disk push (the batch goes to memory or is dropped).
func (r *spilloverRateLimiter) Allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.refillLocked()

	if r.tokens > 0 {
		r.tokens--
		return true
	}

	queue.IncrementSpilloverRateLimited()
	return false
}

func (r *spilloverRateLimiter) refillLocked() {
	now := time.Now()
	elapsed := now.Sub(r.lastRefill)
	if elapsed <= 0 {
		return
	}

	newTokens := int(elapsed.Seconds() * float64(r.maxPerSec))
	if newTokens > 0 {
		r.tokens += newTokens
		if r.tokens > r.maxPerSec {
			r.tokens = r.maxPerSec
		}
		r.lastRefill = now
	}
}
