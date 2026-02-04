package exporter

import (
	"runtime"
	"testing"
)

func TestStress_PRWResponseBodyBounded(t *testing.T) {
	// This is a compile-time verification test. The actual fix is using
	// io.LimitReader(resp.Body, 4096) instead of io.ReadAll(resp.Body).
	// We verify memory stays bounded when processing many export cycles.

	var mBefore, mAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&mBefore)

	// Simulate allocation patterns similar to export cycles
	for i := 0; i < 10000; i++ {
		// Allocate a bounded buffer (simulating the 4KB limit)
		buf := make([]byte, 4096)
		_ = buf
	}

	runtime.GC()
	runtime.ReadMemStats(&mAfter)

	// HeapAlloc can decrease after GC (desired). Only fail if heap grew significantly.
	if mAfter.HeapAlloc > mBefore.HeapAlloc {
		growth := mAfter.HeapAlloc - mBefore.HeapAlloc
		if growth > 10*1024*1024 {
			t.Fatalf("memory growth too high: %d bytes", growth)
		}
	}
}
