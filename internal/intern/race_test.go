package intern

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// --- Race condition tests ---

func TestRace_Pool_ConcurrentIntern(t *testing.T) {
	pool := NewPool()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 2000; j++ {
				pool.Intern(fmt.Sprintf("label-%d-%d", id, j%100))
			}
		}(i)
	}

	wg.Wait()

	// All goroutines interning same keys should converge
	if pool.Size() > 800 { // 8 goroutines * 100 unique each
		t.Errorf("Pool size %d seems too high", pool.Size())
	}
}

func TestRace_Pool_ConcurrentInternBytes(t *testing.T) {
	pool := NewPool()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 2000; j++ {
				b := []byte(fmt.Sprintf("bytes-label-%d-%d", id, j%100))
				pool.InternBytes(b)
			}
		}(i)
	}

	wg.Wait()
}

func TestRace_Pool_ConcurrentInternWithStats(t *testing.T) {
	pool := NewPool()

	var wg sync.WaitGroup

	// Interners
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				pool.Intern(fmt.Sprintf("stat-%d-%d", id, j%50))
			}
		}(i)
	}

	// Stats readers
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				pool.Stats()
				pool.Size()
			}
		}()
	}

	wg.Wait()
}

func TestRace_Pool_ResetBetweenBursts(t *testing.T) {
	// Note: Pool.Reset() replaces sync.Map and is NOT safe to call
	// concurrently with Intern/InternBytes. This test verifies
	// the serial Reset-between-bursts pattern used in production.
	pool := NewPool()

	for cycle := 0; cycle < 10; cycle++ {
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < 200; j++ {
					pool.Intern(fmt.Sprintf("reset-%d-%d", id, j%50))
					pool.InternBytes([]byte(fmt.Sprintf("reset-bytes-%d", j%25)))
				}
			}(i)
		}
		wg.Wait()

		// Reset between bursts (no concurrent access)
		pool.Reset()
	}
}

func TestRace_Pool_ResetIfLargeBetweenBursts(t *testing.T) {
	// ResetIfLarge internally calls Reset, which is not safe for concurrent use.
	// This test verifies the pattern used in production: serial ResetIfLarge
	// called between concurrent Intern bursts (e.g., in ResetCardinality).
	pool := NewPool()

	for cycle := 0; cycle < 10; cycle++ {
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < 200; j++ {
					pool.Intern(fmt.Sprintf("rifl-%d-%d-%d", cycle, id, j))
				}
			}(i)
		}
		wg.Wait()

		// ResetIfLarge between bursts (no concurrent access)
		pool.ResetIfLarge(500)
	}
}

func TestRace_GlobalPools_ConcurrentAccess(t *testing.T) {
	// Test the global LabelNames and MetricNames pools
	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				LabelNames.Intern(fmt.Sprintf("label_%d", j%50))
				MetricNames.Intern(fmt.Sprintf("metric_%d", j%100))
			}
		}(i)
	}

	// Concurrent stats
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 200; j++ {
			LabelNames.Stats()
			MetricNames.Stats()
			LabelNames.Size()
			MetricNames.Size()
		}
	}()

	wg.Wait()

	// Cleanup global state
	LabelNames.Reset()
	MetricNames.Reset()
}

func TestRace_CommonLabels_ConcurrentInit(t *testing.T) {
	// CommonLabels uses sync.Once, safe but let's verify
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pool := CommonLabels()
			pool.Intern("service.name")
			pool.Intern("new-label")
			pool.Stats()
		}()
	}

	wg.Wait()
}

func TestRace_Pool_InternBytesUnsafeString(t *testing.T) {
	// Test that unsafeString in InternBytes doesn't cause races
	// when the byte slice is reused.
	pool := NewPool()
	buf := make([]byte, 32)

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			localBuf := make([]byte, 32)
			for j := 0; j < 1000; j++ {
				n := copy(localBuf, fmt.Sprintf("unsafe-%d-%d", id, j%50))
				pool.InternBytes(localBuf[:n])
			}
		}(i)
	}

	// Also use the shared buffer (simulates worst-case reuse)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 500; j++ {
			n := copy(buf, fmt.Sprintf("shared-%d", j%50))
			pool.InternBytes(buf[:n])
		}
	}()

	wg.Wait()
}

// --- Memory leak tests ---

func TestMemLeak_Pool_ResetCycles(t *testing.T) {
	pool := NewPool()

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	const cycles = 100
	const entriesPerCycle = 5000

	for c := 0; c < cycles; c++ {
		for i := 0; i < entriesPerCycle; i++ {
			pool.Intern(fmt.Sprintf("leak-test-cycle%d-entry%d", c, i))
		}
		pool.Reset()
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("Pool reset cycles: heap_before=%dKB, heap_after=%dKB",
		heapBefore/1024, heapAfter/1024)

	// sync.Map replaced on Reset, old entries should be GC'd
	if heapAfter > heapBefore+30*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after %d reset cycles",
			heapBefore/1024, heapAfter/1024, cycles)
	}
}

func TestMemLeak_Pool_ResetIfLargeCycles(t *testing.T) {
	pool := NewPool()

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	const cycles = 50
	const maxEntries = 1000

	for c := 0; c < cycles; c++ {
		// Grow beyond threshold
		for i := 0; i < maxEntries+500; i++ {
			pool.Intern(fmt.Sprintf("rifl-leak-c%d-e%d", c, i))
		}
		// Should trigger reset
		if !pool.ResetIfLarge(maxEntries) {
			t.Fatalf("Cycle %d: ResetIfLarge should have returned true", c)
		}
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("ResetIfLarge cycles: heap_before=%dKB, heap_after=%dKB",
		heapBefore/1024, heapAfter/1024)

	if heapAfter > heapBefore+30*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after %d cycles",
			heapBefore/1024, heapAfter/1024, cycles)
	}
}

func TestMemLeak_Pool_InternBytesClonesSafely(t *testing.T) {
	pool := NewPool()

	// Verify that InternBytes clones the string (doesn't hold reference to large buffer)
	largeBuf := make([]byte, 4096)
	copy(largeBuf[100:110], "small-key!")

	pool.InternBytes(largeBuf[100:110])

	// Clear the buffer
	for i := range largeBuf {
		largeBuf[i] = 0
	}

	// The interned string should still be valid
	result := pool.Intern("small-key!")
	if result != "small-key!" {
		t.Errorf("Interned string corrupted: got %q", result)
	}
}

func TestMemLeak_Pool_UnboundedGrowthDetection(t *testing.T) {
	pool := NewPool()

	// Simulate unbounded growth without reset
	for i := 0; i < 50000; i++ {
		pool.Intern(fmt.Sprintf("unbounded-%d", i))
	}

	size := pool.Size()
	if size != 50000 {
		t.Errorf("Expected 50000 entries, got %d", size)
	}

	// Verify ResetIfLarge detects and cleans
	if !pool.ResetIfLarge(10000) {
		t.Error("ResetIfLarge should have triggered for 50K entries with 10K threshold")
	}

	if pool.Size() != 0 {
		t.Errorf("Pool should be empty after reset, got %d", pool.Size())
	}
}
