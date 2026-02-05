package cardinality

import (
	"hash/fnv"
	"sync"
	"sync/atomic"

	"github.com/szibis/metrics-governor/internal/logging"
)

// TrackerMode represents the current mode of a hybrid tracker.
type TrackerMode int32

const (
	// TrackerModeBloom indicates the tracker is using a Bloom filter.
	TrackerModeBloom TrackerMode = 0
	// TrackerModeHLL indicates the tracker has switched to HyperLogLog.
	TrackerModeHLL TrackerMode = 1
)

// String returns the string representation of the tracker mode.
func (m TrackerMode) String() string {
	switch m {
	case TrackerModeBloom:
		return "bloom"
	case TrackerModeHLL:
		return "hll"
	default:
		return "unknown"
	}
}

// HybridTracker automatically switches from Bloom filter to HyperLogLog
// when cardinality exceeds a configurable threshold. This provides
// membership testing (for selective dropping) at low cardinality while
// maintaining fixed ~12KB memory at high cardinality.
type HybridTracker struct {
	bloomTracker *BloomTracker
	hllTracker   *HLLTracker
	mode         atomic.Int32
	threshold    int64
	switchCount  atomic.Int64

	// Protects mode switch operation
	switchMu sync.Mutex

	// Callback for mode switch events (optional)
	OnModeSwitch func(previous, current TrackerMode, cardinalityAtSwitch int64)
}

// NewHybridTracker creates a new hybrid tracker that starts in Bloom mode
// and switches to HLL when the count exceeds threshold.
func NewHybridTracker(cfg Config, threshold int64) *HybridTracker {
	ht := &HybridTracker{
		bloomTracker: NewBloomTracker(cfg),
		hllTracker:   NewHLLTracker(),
		threshold:    threshold,
	}
	ht.mode.Store(int32(TrackerModeBloom))
	return ht
}

// Mode returns the current tracker mode.
func (t *HybridTracker) Mode() TrackerMode {
	return TrackerMode(t.mode.Load())
}

// SwitchCount returns the number of times this tracker has switched modes.
func (t *HybridTracker) SwitchCount() int64 {
	return t.switchCount.Load()
}

// Add adds an element and returns true if it was new (Bloom mode only).
// In HLL mode, always returns true since HLL cannot determine membership.
func (t *HybridTracker) Add(key []byte) bool {
	if TrackerMode(t.mode.Load()) == TrackerModeHLL {
		t.hllTracker.Add(key)
		return true
	}

	result := t.bloomTracker.Add(key)

	// Check if we should switch to HLL
	if t.threshold > 0 && t.bloomTracker.Count() >= t.threshold {
		t.switchToHLL()
	}

	return result
}

// TestOnly tests membership without adding.
// Returns true if the element likely exists (Bloom mode).
// Always returns false in HLL mode (HLL cannot test membership).
func (t *HybridTracker) TestOnly(key []byte) bool {
	if TrackerMode(t.mode.Load()) == TrackerModeHLL {
		return false
	}
	return t.bloomTracker.TestOnly(key)
}

// Count returns the number of unique elements.
func (t *HybridTracker) Count() int64 {
	if TrackerMode(t.mode.Load()) == TrackerModeHLL {
		return t.hllTracker.Count()
	}
	return t.bloomTracker.Count()
}

// Reset clears both trackers and resets to Bloom mode.
func (t *HybridTracker) Reset() {
	t.switchMu.Lock()
	defer t.switchMu.Unlock()

	t.bloomTracker.Reset()
	t.hllTracker.Reset()
	t.mode.Store(int32(TrackerModeBloom))
}

// MemoryUsage returns approximate memory usage in bytes.
func (t *HybridTracker) MemoryUsage() uint64 {
	if TrackerMode(t.mode.Load()) == TrackerModeHLL {
		return t.hllTracker.MemoryUsage()
	}
	return t.bloomTracker.MemoryUsage() + t.hllTracker.MemoryUsage()
}

// ShouldSample returns true if the given series key should be kept based on
// deterministic hash-based sampling. The same series key always produces
// the same result, ensuring consistent time series (no random gaps).
func (t *HybridTracker) ShouldSample(seriesKey []byte, sampleRate float64) bool {
	if sampleRate >= 1.0 {
		return true
	}
	if sampleRate <= 0.0 {
		return false
	}

	h := fnv.New32a()
	h.Write(seriesKey)
	hashVal := h.Sum32()

	// Use 10000 for ~0.01% granularity
	threshold := uint32(sampleRate * 10000)
	return (hashVal % 10000) < threshold
}

func (t *HybridTracker) switchToHLL() {
	t.switchMu.Lock()
	defer t.switchMu.Unlock()

	// Double-check after acquiring lock
	if TrackerMode(t.mode.Load()) == TrackerModeHLL {
		return
	}

	cardinalityAtSwitch := t.bloomTracker.Count()

	// Note: We don't migrate Bloom data to HLL since HLL was receiving
	// the same inserts in parallel during the Bloom phase would be wasteful.
	// Instead, HLL starts fresh and catches up as new data arrives.
	// The count will be slightly lower temporarily but converges quickly.

	t.mode.Store(int32(TrackerModeHLL))
	t.switchCount.Add(1)

	logging.Info("tracker mode switched to HLL", logging.F(
		"cardinality_at_switch", cardinalityAtSwitch,
		"threshold", t.threshold,
	))

	if t.OnModeSwitch != nil {
		t.OnModeSwitch(TrackerModeBloom, TrackerModeHLL, cardinalityAtSwitch)
	}
}
