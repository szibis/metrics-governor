package cardinality

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- Race condition tests ---
// These tests are designed to be run with -race to detect data races.
// They exercise concurrent access patterns that occur in production:
// simultaneous Add/Count/Reset across goroutines, mode switching under
// contention, and callback execution during transitions.

func TestRace_HLL_AddCountReset(t *testing.T) {
	tracker := NewHLLTracker()
	var wg sync.WaitGroup

	// Writers: concurrent Add
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 2000; j++ {
				tracker.Add([]byte(fmt.Sprintf("w%d-k%d", id, j)))
			}
		}(i)
	}

	// Readers: concurrent Count (Estimate mutates internal state)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 2000; j++ {
				tracker.Count()
			}
		}()
	}

	// Resetter: concurrent Reset
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 50; j++ {
			tracker.Reset()
			runtime.Gosched()
		}
	}()

	wg.Wait()
}

func TestRace_HLL_AddMemoryUsage(t *testing.T) {
	tracker := NewHLLTracker()
	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				tracker.Add([]byte(fmt.Sprintf("m%d-k%d", id, j)))
				tracker.MemoryUsage()
				tracker.TestOnly([]byte("any"))
			}
		}(i)
	}

	wg.Wait()
}

func TestRace_Bloom_AddTestCountReset(t *testing.T) {
	tracker := NewBloomTracker(DefaultConfig())
	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 2000; j++ {
				key := []byte(fmt.Sprintf("b%d-k%d", id, j))
				tracker.Add(key)
				tracker.TestOnly(key)
				tracker.Count()
				tracker.MemoryUsage()
			}
		}(i)
	}

	// Concurrent reset
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 50; j++ {
			tracker.Reset()
			runtime.Gosched()
		}
	}()

	wg.Wait()
}

func TestRace_Hybrid_ModeSwitchUnderContention(t *testing.T) {
	// Low threshold so switch happens while goroutines are racing
	tracker := newTestHybridTracker(100)

	var switchCount atomic.Int64
	tracker.OnModeSwitch = func(previous, current TrackerMode, cardinalityAtSwitch int64) {
		switchCount.Add(1)
	}

	var wg sync.WaitGroup

	// Many goroutines adding concurrently — switch will occur mid-flight
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				key := []byte(fmt.Sprintf("h%d-k%d", id, j))
				tracker.Add(key)
				tracker.TestOnly(key)
				tracker.Count()
				tracker.Mode()
				tracker.MemoryUsage()
				tracker.ShouldSample(key, 0.5)
			}
		}(i)
	}

	wg.Wait()

	if tracker.Mode() != TrackerModeHLL {
		t.Errorf("Expected HLL mode after 4000 adds with threshold 100, got %s", tracker.Mode())
	}

	// Switch should happen exactly once
	if switchCount.Load() != 1 {
		t.Errorf("Expected exactly 1 mode switch, got %d", switchCount.Load())
	}
}

func TestRace_Hybrid_ResetDuringModeSwitch(t *testing.T) {
	// Test that Reset and mode switch don't race
	for iter := 0; iter < 20; iter++ {
		tracker := newTestHybridTracker(50)
		var wg sync.WaitGroup

		// Writer goroutines pushing toward threshold
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					tracker.Add([]byte(fmt.Sprintf("r%d-k%d", id, j)))
				}
			}(i)
		}

		// Concurrent reset to fight the mode switch
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				tracker.Reset()
				runtime.Gosched()
			}
		}()

		wg.Wait()
		// Just verify no panic/race — final state depends on timing
		_ = tracker.Mode()
		_ = tracker.Count()
	}
}

func TestRace_Hybrid_ShouldSampleConcurrent(t *testing.T) {
	tracker := newTestHybridTracker(100)

	// Push into HLL mode
	for i := 0; i < 200; i++ {
		tracker.Add([]byte(fmt.Sprintf("pre-%d", i)))
	}
	if tracker.Mode() != TrackerModeHLL {
		t.Fatal("Expected HLL mode")
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				key := []byte(fmt.Sprintf("sample-%d-%d", id, j))
				tracker.ShouldSample(key, 0.3)
				tracker.Add(key)
				tracker.Count()
			}
		}(i)
	}

	wg.Wait()
}

func TestRace_Exact_AddTestCountReset(t *testing.T) {
	tracker := NewExactTracker()
	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 2000; j++ {
				key := []byte(fmt.Sprintf("e%d-k%d", id, j))
				tracker.Add(key)
				tracker.TestOnly(key)
				tracker.Count()
				tracker.MemoryUsage()
			}
		}(i)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 50; j++ {
			tracker.Reset()
			runtime.Gosched()
		}
	}()

	wg.Wait()
}

func TestRace_Hybrid_CallbackDuringSwitch(t *testing.T) {
	// Ensure the OnModeSwitch callback doesn't race with concurrent ops
	tracker := newTestHybridTracker(100)

	var callbackSeen atomic.Int64
	tracker.OnModeSwitch = func(prev, curr TrackerMode, count int64) {
		callbackSeen.Add(1)
		// Simulate callback doing work (reading state)
		_ = tracker.Mode()
		_ = tracker.Count()
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				tracker.Add([]byte(fmt.Sprintf("cb%d-%d", id, j)))
			}
		}(i)
	}

	wg.Wait()

	if callbackSeen.Load() == 0 {
		t.Error("OnModeSwitch callback should have been called")
	}
}

func TestRace_Hybrid_RepeatedResetCycles(t *testing.T) {
	// Simulate window resets: fill → switch → reset → fill again
	tracker := newTestHybridTracker(50)

	var wg sync.WaitGroup
	for cycle := 0; cycle < 5; cycle++ {
		// Fill and trigger switch
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(id, c int) {
				defer wg.Done()
				for j := 0; j < 50; j++ {
					tracker.Add([]byte(fmt.Sprintf("c%d-g%d-k%d", c, id, j)))
				}
			}(i, cycle)
		}
		wg.Wait()

		// Reset back to bloom
		tracker.Reset()
		if tracker.Mode() != TrackerModeBloom {
			t.Fatalf("Cycle %d: expected Bloom after reset, got %s", cycle, tracker.Mode())
		}
	}
}

// --- Memory leak tests ---
// These tests verify that trackers don't leak memory across Reset cycles
// and that repeated creation/destruction stays bounded.

func TestMemLeak_HLL_ResetCycles(t *testing.T) {
	tracker := NewHLLTracker()

	// Warm up GC
	runtime.GC()
	runtime.GC()

	var baseAlloc uint64
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	baseAlloc = m.TotalAlloc

	const cycles = 100
	const itemsPerCycle = 5000

	for c := 0; c < cycles; c++ {
		for i := 0; i < itemsPerCycle; i++ {
			tracker.Add([]byte(fmt.Sprintf("leak-test-c%d-k%d", c, i)))
		}
		tracker.Reset()
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond) // let finalizers run

	runtime.ReadMemStats(&m)
	totalAllocated := m.TotalAlloc - baseAlloc

	// After 100 reset cycles with 5K items each, the live heap should be small.
	// HLL is fixed ~12KB, so after GC the live set should be bounded.
	// We check that total allocation is reasonable (not growing unboundedly).
	// With 100 cycles * 5K items * ~60 bytes/key = ~30MB of key allocations is expected.
	// The important thing is HeapInuse stays small.
	heapInUse := m.HeapInuse

	t.Logf("HLL reset cycles: total_alloc=%dMB, heap_inuse=%dKB",
		totalAllocated/(1024*1024), heapInUse/1024)

	// HeapInuse should be well under 10MB — HLL is ~12KB fixed
	if heapInUse > 50*1024*1024 {
		t.Errorf("Possible memory leak: HeapInuse=%dMB after %d reset cycles", heapInUse/(1024*1024), cycles)
	}
}

func TestMemLeak_Bloom_ResetCycles(t *testing.T) {
	cfg := Config{
		ExpectedItems:     10000,
		FalsePositiveRate: 0.01,
	}
	tracker := NewBloomTracker(cfg)

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	const cycles = 100
	const itemsPerCycle = 5000

	for c := 0; c < cycles; c++ {
		for i := 0; i < itemsPerCycle; i++ {
			tracker.Add([]byte(fmt.Sprintf("bloom-leak-c%d-k%d", c, i)))
		}
		tracker.Reset()
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("Bloom reset cycles: heap_before=%dKB, heap_after=%dKB",
		heapBefore/1024, heapAfter/1024)

	// Bloom filter uses ClearAll which resets bits in-place (no new allocation).
	// Heap should not grow significantly.
	if heapAfter > heapBefore+20*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after %d reset cycles",
			heapBefore/1024, heapAfter/1024, cycles)
	}
}

func TestMemLeak_Hybrid_ResetCycles(t *testing.T) {
	tracker := newTestHybridTracker(100)

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	const cycles = 100
	const itemsPerCycle = 500 // Enough to trigger Bloom→HLL switch each cycle

	for c := 0; c < cycles; c++ {
		for i := 0; i < itemsPerCycle; i++ {
			tracker.Add([]byte(fmt.Sprintf("hybrid-leak-c%d-k%d", c, i)))
		}

		// Should have switched to HLL
		if tracker.Mode() != TrackerModeHLL {
			t.Fatalf("Cycle %d: expected HLL mode", c)
		}

		tracker.Reset()

		// Should be back to Bloom
		if tracker.Mode() != TrackerModeBloom {
			t.Fatalf("Cycle %d: expected Bloom after reset", c)
		}
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("Hybrid reset cycles: heap_before=%dKB, heap_after=%dKB, switches=%d",
		heapBefore/1024, heapAfter/1024, tracker.SwitchCount())

	// HLL Reset creates a new Sketch (hyperloglog.New()) each time.
	// After GC, old sketches should be collected. Verify heap is bounded.
	if heapAfter > heapBefore+20*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after %d reset cycles",
			heapBefore/1024, heapAfter/1024, cycles)
	}
}

func TestMemLeak_TrackerCreationDestruction(t *testing.T) {
	// Simulate creating/destroying many trackers (like tracker store evictions)
	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	const iterations = 500
	const itemsPerTracker = 1000

	for i := 0; i < iterations; i++ {
		// Create all three tracker types
		bloom := NewBloomTracker(Config{
			ExpectedItems:     uint(itemsPerTracker),
			FalsePositiveRate: 0.01,
		})
		hll := NewHLLTracker()
		hybrid := NewHybridTracker(Config{
			ExpectedItems:     uint(itemsPerTracker),
			FalsePositiveRate: 0.01,
			HLLThreshold:      500,
		}, 500)

		for j := 0; j < itemsPerTracker; j++ {
			key := []byte(fmt.Sprintf("destroy-test-%d-%d", i, j))
			bloom.Add(key)
			hll.Add(key)
			hybrid.Add(key)
		}

		// Let trackers go out of scope (GC should reclaim)
		_ = bloom.Count()
		_ = hll.Count()
		_ = hybrid.Count()
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("Tracker creation/destruction: heap_before=%dKB, heap_after=%dKB",
		heapBefore/1024, heapAfter/1024)

	// After creating and discarding 500 trackers of each type, heap should be bounded.
	// Live set should only contain the last few trackers before GC.
	if heapAfter > heapBefore+50*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after %d tracker create/destroy cycles",
			heapBefore/1024, heapAfter/1024, iterations)
	}
}

func TestMemLeak_HLL_HighCardinalityStable(t *testing.T) {
	// Verify HLL memory stays fixed at ~12KB even with very high cardinality
	tracker := NewHLLTracker()

	// Add 100K unique items
	for i := 0; i < 100000; i++ {
		tracker.Add([]byte(fmt.Sprintf("high-card-%d", i)))
	}

	mem1 := tracker.MemoryUsage()

	// Add another 100K
	for i := 100000; i < 200000; i++ {
		tracker.Add([]byte(fmt.Sprintf("high-card-%d", i)))
	}

	mem2 := tracker.MemoryUsage()

	if mem1 != mem2 {
		t.Errorf("HLL memory should be constant: %d vs %d", mem1, mem2)
	}

	if mem1 != 12288 {
		t.Errorf("HLL memory should be 12288, got %d", mem1)
	}
}
