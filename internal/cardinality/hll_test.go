package cardinality

import (
	"fmt"
	"math"
	"sync"
	"testing"
)

func TestHLLTracker_Add(t *testing.T) {
	tracker := NewHLLTracker()

	// Add always returns true (HLL cannot determine exact membership)
	if !tracker.Add([]byte("key1")) {
		t.Error("HLL Add should always return true")
	}

	if !tracker.Add([]byte("key1")) {
		t.Error("HLL Add should return true even for duplicates")
	}

	if !tracker.Add([]byte("key2")) {
		t.Error("HLL Add should return true for new element")
	}
}

func TestHLLTracker_TestOnly(t *testing.T) {
	tracker := NewHLLTracker()

	// TestOnly always returns false (HLL cannot test membership)
	if tracker.TestOnly([]byte("key1")) {
		t.Error("HLL TestOnly should always return false")
	}

	tracker.Add([]byte("key1"))

	// Still false after adding
	if tracker.TestOnly([]byte("key1")) {
		t.Error("HLL TestOnly should always return false even after Add")
	}
}

func TestHLLTracker_Count(t *testing.T) {
	tracker := NewHLLTracker()

	if tracker.Count() != 0 {
		t.Errorf("Initial count should be 0, got %d", tracker.Count())
	}

	for i := 0; i < 1000; i++ {
		tracker.Add([]byte(fmt.Sprintf("key%d", i)))
	}

	count := tracker.Count()
	// HLL should estimate within ~5% for 1000 items
	errorPct := math.Abs(float64(count)-1000.0) / 1000.0
	if errorPct > 0.05 {
		t.Errorf("HLL count %d deviates >5%% from expected 1000 (%.2f%%)", count, errorPct*100)
	}
}

func TestHLLTracker_HighCardinality(t *testing.T) {
	tracker := NewHLLTracker()

	n := 100000
	for i := 0; i < n; i++ {
		tracker.Add([]byte(fmt.Sprintf("series{service=\"svc%d\",env=\"prod\",pod=\"pod-%d\"}", i%100, i)))
	}

	count := tracker.Count()
	errorPct := math.Abs(float64(count)-float64(n)) / float64(n)
	// HLL with precision 14 should be within ~2% for 100K items
	if errorPct > 0.02 {
		t.Errorf("HLL count %d deviates >2%% from expected %d (%.2f%%)", count, n, errorPct*100)
	}
	t.Logf("HLL estimate for %d items: %d (%.2f%% error)", n, count, errorPct*100)
}

func TestHLLTracker_Reset(t *testing.T) {
	tracker := NewHLLTracker()

	for i := 0; i < 1000; i++ {
		tracker.Add([]byte(fmt.Sprintf("key%d", i)))
	}

	if tracker.Count() == 0 {
		t.Error("Count should be non-zero before reset")
	}

	tracker.Reset()

	if tracker.Count() != 0 {
		t.Errorf("Count should be 0 after reset, got %d", tracker.Count())
	}
}

func TestHLLTracker_MemoryUsage(t *testing.T) {
	tracker := NewHLLTracker()

	mem := tracker.MemoryUsage()
	if mem != 12288 {
		t.Errorf("HLL memory should be 12288 bytes (~12KB), got %d", mem)
	}

	// Memory should stay fixed regardless of how many items are added
	for i := 0; i < 100000; i++ {
		tracker.Add([]byte(fmt.Sprintf("key%d", i)))
	}

	memAfter := tracker.MemoryUsage()
	if memAfter != mem {
		t.Errorf("HLL memory should remain fixed at %d, got %d after 100K adds", mem, memAfter)
	}
}

func TestHLLTracker_Concurrent(t *testing.T) {
	tracker := NewHLLTracker()

	var wg sync.WaitGroup
	numGoroutines := 10
	keysPerGoroutine := 1000

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < keysPerGoroutine; j++ {
				key := fmt.Sprintf("g%d-key%d", id, j)
				tracker.Add([]byte(key))
				tracker.TestOnly([]byte(key))
				tracker.Count()
			}
		}(i)
	}

	wg.Wait()

	expected := numGoroutines * keysPerGoroutine
	count := tracker.Count()
	errorPct := math.Abs(float64(count)-float64(expected)) / float64(expected)
	if errorPct > 0.05 {
		t.Errorf("Concurrent HLL count %d deviates >5%% from expected %d", count, expected)
	}
}

func TestHLLTracker_DuplicateHandling(t *testing.T) {
	tracker := NewHLLTracker()

	// Add same key 10000 times
	for i := 0; i < 10000; i++ {
		tracker.Add([]byte("same-key"))
	}

	count := tracker.Count()
	if count != 1 {
		t.Errorf("Count should be 1 for single unique key, got %d", count)
	}
}
