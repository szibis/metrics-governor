package cardinality

import (
	"sync"
)

// ExactTracker uses a map for 100% accurate cardinality tracking.
// This is the original behavior preserved for backward compatibility.
type ExactTracker struct {
	items map[string]struct{}
	mu    sync.RWMutex
}

// NewExactTracker creates a new exact cardinality tracker.
func NewExactTracker() *ExactTracker {
	return &ExactTracker{
		items: make(map[string]struct{}),
	}
}

// Add tests membership and adds element if new.
// Returns true if the element was new (not seen before).
func (t *ExactTracker) Add(key []byte) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	k := string(key)
	if _, exists := t.items[k]; exists {
		return false
	}
	t.items[k] = struct{}{}
	return true
}

// TestOnly tests membership without adding.
// Returns true if the element exists.
func (t *ExactTracker) TestOnly(key []byte) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, exists := t.items[string(key)]
	return exists
}

// Count returns the number of unique elements seen.
func (t *ExactTracker) Count() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return int64(len(t.items))
}

// Reset clears the tracker for a new window.
func (t *ExactTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.items = make(map[string]struct{})
}

// MemoryUsage returns approximate memory usage in bytes.
// Estimates ~75 bytes per key (average series key length).
func (t *ExactTracker) MemoryUsage() uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	// Rough estimate: 75 bytes per entry (key + map overhead)
	return uint64(len(t.items)) * 75
}
