package intern

import (
	"fmt"
	"runtime"
	"testing"
)

func TestStress_InternPoolGrowth(t *testing.T) {
	pool := NewPool()

	// Add many unique strings
	for i := 0; i < 200000; i++ {
		pool.Intern(fmt.Sprintf("metric_%d", i))
	}

	if pool.Size() <= 0 {
		t.Fatal("pool should have entries")
	}

	// ResetIfLarge should clear it when above threshold
	if !pool.ResetIfLarge(100000) {
		t.Fatal("ResetIfLarge should have returned true for pool > 100000")
	}
	if pool.Size() != 0 {
		t.Fatalf("pool should be empty after reset, got %d", pool.Size())
	}

	// ResetIfLarge should NOT clear when below threshold
	for i := 0; i < 100; i++ {
		pool.Intern(fmt.Sprintf("metric_%d", i))
	}
	if pool.ResetIfLarge(100000) {
		t.Fatal("ResetIfLarge should have returned false for pool < 100000")
	}
	if pool.Size() != 100 {
		t.Fatalf("pool should still have 100 entries, got %d", pool.Size())
	}
}

func TestStress_InternPoolMemoryBounded(t *testing.T) {
	pool := NewPool()

	var mBefore, mAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&mBefore)

	// Simulate sustained load with periodic resets
	for cycle := 0; cycle < 5; cycle++ {
		for i := 0; i < 50000; i++ {
			pool.Intern(fmt.Sprintf("cycle_%d_metric_%d", cycle, i))
		}
		pool.ResetIfLarge(100000)
	}

	// Final reset
	pool.Reset()
	runtime.GC()
	runtime.ReadMemStats(&mAfter)

	// HeapAlloc can decrease after GC, which is the desired outcome.
	// Only fail if heap grew significantly.
	if mAfter.HeapAlloc > mBefore.HeapAlloc {
		growth := mAfter.HeapAlloc - mBefore.HeapAlloc
		if growth > 10*1024*1024 {
			t.Fatalf("memory growth after reset too high: %d bytes", growth)
		}
	}
}
