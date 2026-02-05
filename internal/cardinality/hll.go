package cardinality

import (
	"sync"

	"github.com/axiomhq/hyperloglog"
)

// HLLTracker provides fixed-memory cardinality tracking using HyperLogLog.
// It uses ~12KB of memory regardless of cardinality (with precision 14).
// HLL does not support membership testing, so TestOnly always returns false.
type HLLTracker struct {
	sketch *hyperloglog.Sketch
	mu     sync.Mutex
}

// NewHLLTracker creates a new HyperLogLog-based cardinality tracker.
func NewHLLTracker() *HLLTracker {
	return &HLLTracker{
		sketch: hyperloglog.New(),
	}
}

// Add adds an element to the HLL sketch.
// Returns true always since HLL cannot determine exact membership.
func (t *HLLTracker) Add(key []byte) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sketch.Insert(key)
	return true
}

// TestOnly always returns false because HLL does not support membership testing.
func (t *HLLTracker) TestOnly(_ []byte) bool {
	return false
}

// Count returns the estimated number of unique elements.
// Uses full Lock because Estimate() may mutate internal state (sparse→dense merge).
func (t *HLLTracker) Count() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return int64(t.sketch.Estimate())
}

// Reset clears the HLL sketch for a new window.
func (t *HLLTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sketch = hyperloglog.New()
}

// MemoryUsage returns approximate memory usage in bytes.
// HyperLogLog with default precision uses ~16384 registers = ~12KB.
func (t *HLLTracker) MemoryUsage() uint64 {
	return 12288 // ~12KB fixed for precision 14
}
