package cardinality

import (
	"sync"

	"github.com/bits-and-blooms/bloom/v3"
)

// Tracker interface for cardinality tracking (exact or probabilistic).
// Both BloomTracker and ExactTracker implement this interface.
type Tracker interface {
	// Add tests membership and adds element if new.
	// Returns true if the element was new (not seen before).
	Add(key []byte) bool

	// TestOnly tests membership without adding.
	// Returns true if the element likely exists.
	TestOnly(key []byte) bool

	// Count returns the number of unique elements seen.
	// Note: For BloomTracker, may slightly undercount due to false positives on Add().
	Count() int64

	// Reset clears the tracker for a new window.
	Reset()

	// MemoryUsage returns approximate memory usage in bytes.
	MemoryUsage() uint64
}

// BloomTracker provides memory-efficient unique element tracking using a Bloom filter.
// It uses a manual counter since Bloom filters don't support cardinality estimation.
type BloomTracker struct {
	filter *bloom.BloomFilter
	count  int64
	mu     sync.RWMutex
}

// NewBloomTracker creates a new Bloom filter-based cardinality tracker.
func NewBloomTracker(cfg Config) *BloomTracker {
	return &BloomTracker{
		filter: bloom.NewWithEstimates(cfg.ExpectedItems, cfg.FalsePositiveRate),
		count:  0,
	}
}

// Add tests membership and adds element if new.
// Returns true if the element was new (not seen before).
// Note: Due to false positives, Add may return false for a truly new element ~FPR% of the time.
func (t *BloomTracker) Add(key []byte) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.filter.Test(key) {
		// Likely already exists (may be false positive)
		return false
	}

	// New element - add to filter and increment counter
	t.filter.Add(key)
	t.count++
	return true
}

// TestOnly tests membership without adding.
// Returns true if the element likely exists.
func (t *BloomTracker) TestOnly(key []byte) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.filter.Test(key)
}

// Count returns the number of unique elements seen.
// Note: May slightly undercount due to false positives on Add().
func (t *BloomTracker) Count() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.count
}

// Reset clears the tracker for a new window.
func (t *BloomTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.filter.ClearAll()
	t.count = 0
}

// MemoryUsage returns approximate memory usage in bytes.
func (t *BloomTracker) MemoryUsage() uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	// Bloom filter bit array size in bytes
	return uint64(t.filter.Cap()) / 8
}
