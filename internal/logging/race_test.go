package logging

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Race condition tests (run with -race flag)
// ---------------------------------------------------------------------------

// TestRace_ConcurrentLogging verifies that 8 goroutines can call Info, Warn,
// and Error concurrently without triggering the race detector.
func TestRace_ConcurrentLogging(t *testing.T) {
	SetOutput(io.Discard)
	defer SetOutput(os.Stdout)

	const goroutines = 8
	const iterations = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				switch id % 3 {
				case 0:
					Info(fmt.Sprintf("goroutine %d iteration %d", id, i))
				case 1:
					Warn(fmt.Sprintf("goroutine %d iteration %d", id, i))
				case 2:
					Error(fmt.Sprintf("goroutine %d iteration %d", id, i))
				}
			}
		}(g)
	}

	wg.Wait()
}

// TestRace_SetOutputDuringLogging verifies that calling SetOutput while other
// goroutines are actively logging does not cause a data race.
func TestRace_SetOutputDuringLogging(t *testing.T) {
	SetOutput(io.Discard)
	defer SetOutput(os.Stdout)

	const loggers = 4
	const iterations = 500

	var wg sync.WaitGroup
	wg.Add(loggers + 1)

	// Start logging goroutines.
	for g := 0; g < loggers; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				Info(fmt.Sprintf("logger %d msg %d", id, i), F("id", id, "i", i))
			}
		}(g)
	}

	// Goroutine that keeps swapping the output writer.
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			SetOutput(io.Discard)
		}
	}()

	wg.Wait()
}

// TestRace_ConcurrentInfoWithFields verifies that concurrent Info calls, each
// with their own fields map, do not race.
func TestRace_ConcurrentInfoWithFields(t *testing.T) {
	SetOutput(io.Discard)
	defer SetOutput(os.Stdout)

	const goroutines = 8
	const iterations = 500

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				fields := F(
					"goroutine", id,
					"iteration", i,
					"label", fmt.Sprintf("g%d-i%d", id, i),
				)
				Info("fields test", fields)
			}
		}(g)
	}

	wg.Wait()
}

// TestRace_ConcurrentMixedLevels exercises all three exported log functions
// from separate goroutines to surface any level-specific races.
func TestRace_ConcurrentMixedLevels(t *testing.T) {
	SetOutput(io.Discard)
	defer SetOutput(os.Stdout)

	const iterations = 500

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			Info("info message", F("level", "info", "i", i))
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			Warn("warn message", F("level", "warn", "i", i))
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			Error("error message", F("level", "error", "i", i))
		}
	}()

	wg.Wait()
}

// TestRace_FieldsHelper_Concurrent verifies that F() is a pure function with
// no shared mutable state; concurrent calls must never interfere.
func TestRace_FieldsHelper_Concurrent(t *testing.T) {
	const goroutines = 8
	const iterations = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				fields := F("goroutine", id, "iteration", i, "tag", "race-test")
				// Sanity-check the returned map to ensure no cross-goroutine
				// contamination.
				if fields["goroutine"] != id {
					t.Errorf("expected goroutine=%d, got %v", id, fields["goroutine"])
				}
				if fields["iteration"] != i {
					t.Errorf("expected iteration=%d, got %v", i, fields["iteration"])
				}
			}
		}(g)
	}

	wg.Wait()
}

// ---------------------------------------------------------------------------
// Memory leak tests
// ---------------------------------------------------------------------------

// heapAllocBytes forces a GC cycle and returns the current HeapAlloc value.
func heapAllocBytes() uint64 {
	runtime.GC()
	runtime.GC() // double GC to reclaim finalizer-dependent objects
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// TestMemLeak_ConcurrentLogging_Cycles logs a large number of messages across
// multiple goroutines and verifies that heap usage does not grow unboundedly.
func TestMemLeak_ConcurrentLogging_Cycles(t *testing.T) {
	SetOutput(io.Discard)
	defer SetOutput(os.Stdout)

	const goroutines = 8
	const iterations = 1000
	const maxHeapGrowth = 20 * 1024 * 1024 // 20 MB

	// Warm up: let the runtime stabilize allocations.
	for i := 0; i < 100; i++ {
		Info("warmup", F("i", i))
	}

	baseline := heapAllocBytes()

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				Info(fmt.Sprintf("leak test %d-%d", id, i), F("id", id, "i", i))
			}
		}(g)
	}

	wg.Wait()

	after := heapAllocBytes()

	growth := uint64(0)
	if after > baseline {
		growth = after - baseline
	}

	t.Logf("heap baseline=%d bytes, after=%d bytes, growth=%d bytes", baseline, after, growth)

	if growth > maxHeapGrowth {
		t.Errorf("possible memory leak: heap grew by %d bytes (threshold %d bytes)", growth, maxHeapGrowth)
	}
}

// TestMemLeak_FieldsCreation_Cycles creates a large number of fields maps via
// F() and verifies that the garbage collector reclaims them properly.
func TestMemLeak_FieldsCreation_Cycles(t *testing.T) {
	const goroutines = 8
	const iterations = 1000
	const maxHeapGrowth = 20 * 1024 * 1024 // 20 MB

	// Warm up.
	for i := 0; i < 100; i++ {
		_ = F("warmup", i)
	}

	baseline := heapAllocBytes()

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				fields := F(
					"goroutine", id,
					"iteration", i,
					"payload", fmt.Sprintf("data-%d-%d", id, i),
				)
				// Use the map so the compiler does not optimize it away.
				if len(fields) == 0 {
					t.Error("unexpected empty fields")
				}
			}
		}(g)
	}

	wg.Wait()

	after := heapAllocBytes()

	growth := uint64(0)
	if after > baseline {
		growth = after - baseline
	}

	t.Logf("heap baseline=%d bytes, after=%d bytes, growth=%d bytes", baseline, after, growth)

	if growth > maxHeapGrowth {
		t.Errorf("possible memory leak: heap grew by %d bytes (threshold %d bytes)", growth, maxHeapGrowth)
	}
}
