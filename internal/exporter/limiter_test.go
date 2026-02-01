package exporter

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewConcurrencyLimiter(t *testing.T) {
	// Test explicit limit
	l := NewConcurrencyLimiter(10)
	if l.Limit() != 10 {
		t.Errorf("expected limit 10, got %d", l.Limit())
	}

	// Test default limit (0 or negative)
	l = NewConcurrencyLimiter(0)
	expected := runtime.NumCPU() * 4
	if l.Limit() != expected {
		t.Errorf("expected default limit %d, got %d", expected, l.Limit())
	}

	l = NewConcurrencyLimiter(-5)
	if l.Limit() != expected {
		t.Errorf("expected default limit %d for negative input, got %d", expected, l.Limit())
	}
}

func TestConcurrencyLimiter_AcquireRelease(t *testing.T) {
	l := NewConcurrencyLimiter(2)

	// Initial state
	if l.Available() != 2 {
		t.Errorf("expected 2 available, got %d", l.Available())
	}
	if l.InUse() != 0 {
		t.Errorf("expected 0 in use, got %d", l.InUse())
	}

	// Acquire first slot
	l.Acquire()
	if l.Available() != 1 {
		t.Errorf("expected 1 available, got %d", l.Available())
	}
	if l.InUse() != 1 {
		t.Errorf("expected 1 in use, got %d", l.InUse())
	}

	// Acquire second slot
	l.Acquire()
	if l.Available() != 0 {
		t.Errorf("expected 0 available, got %d", l.Available())
	}
	if l.InUse() != 2 {
		t.Errorf("expected 2 in use, got %d", l.InUse())
	}

	// Release one
	l.Release()
	if l.Available() != 1 {
		t.Errorf("expected 1 available after release, got %d", l.Available())
	}

	// Release another
	l.Release()
	if l.Available() != 2 {
		t.Errorf("expected 2 available after release, got %d", l.Available())
	}
}

func TestConcurrencyLimiter_TryAcquire(t *testing.T) {
	l := NewConcurrencyLimiter(1)

	// First try should succeed
	if !l.TryAcquire() {
		t.Error("expected TryAcquire to succeed")
	}

	// Second try should fail (no slots available)
	if l.TryAcquire() {
		t.Error("expected TryAcquire to fail when full")
	}

	// Release and try again
	l.Release()
	if !l.TryAcquire() {
		t.Error("expected TryAcquire to succeed after release")
	}

	l.Release()
}

func TestConcurrencyLimiter_AcquireContext(t *testing.T) {
	l := NewConcurrencyLimiter(1)

	// Acquire first slot
	ctx := context.Background()
	if err := l.AcquireContext(ctx); err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// Try to acquire with canceled context
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := l.AcquireContext(cancelCtx)
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}

	// Try with timeout context
	timeoutCtx, cancelTimeout := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelTimeout()

	err = l.AcquireContext(timeoutCtx)
	if err != context.DeadlineExceeded {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}

	// Release and acquire should work
	l.Release()
	if err := l.AcquireContext(context.Background()); err != nil {
		t.Errorf("expected no error after release, got %v", err)
	}
	l.Release()
}

func TestConcurrencyLimiter_AcquireTimeout(t *testing.T) {
	l := NewConcurrencyLimiter(1)

	// First acquire should succeed immediately
	if !l.AcquireTimeout(time.Millisecond) {
		t.Error("expected immediate acquire to succeed")
	}

	// Second acquire should timeout
	start := time.Now()
	if l.AcquireTimeout(50 * time.Millisecond) {
		t.Error("expected acquire to fail with timeout")
	}
	elapsed := time.Since(start)
	if elapsed < 40*time.Millisecond {
		t.Errorf("expected at least 40ms timeout, got %v", elapsed)
	}

	// Release in background after short delay
	go func() {
		time.Sleep(10 * time.Millisecond)
		l.Release()
	}()

	// This should succeed within timeout
	if !l.AcquireTimeout(100 * time.Millisecond) {
		t.Error("expected acquire to succeed after release")
	}
	l.Release()
}

func TestConcurrencyLimiter_Blocking(t *testing.T) {
	l := NewConcurrencyLimiter(1)
	l.Acquire()

	var acquired atomic.Bool
	go func() {
		l.Acquire()
		acquired.Store(true)
		l.Release()
	}()

	// Give goroutine time to block
	time.Sleep(10 * time.Millisecond)

	if acquired.Load() {
		t.Error("expected goroutine to be blocked")
	}

	// Release to unblock
	l.Release()

	// Wait for goroutine to acquire and release
	time.Sleep(10 * time.Millisecond)

	if !acquired.Load() {
		t.Error("expected goroutine to have acquired after release")
	}
}

func TestConcurrencyLimiter_Concurrent(t *testing.T) {
	const limit = 5
	const goroutines = 100
	const iterations = 100

	l := NewConcurrencyLimiter(limit)

	var maxConcurrent atomic.Int32
	var currentConcurrent atomic.Int32
	var wg sync.WaitGroup

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				l.Acquire()

				// Track concurrent operations
				current := currentConcurrent.Add(1)
				if current > int32(limit) {
					t.Errorf("concurrent operations %d exceeded limit %d", current, limit)
				}

				// Update max seen
				for {
					old := maxConcurrent.Load()
					if current <= old || maxConcurrent.CompareAndSwap(old, current) {
						break
					}
				}

				// Simulate work
				runtime.Gosched()

				currentConcurrent.Add(-1)
				l.Release()
			}
		}()
	}

	wg.Wait()

	// Verify we actually achieved parallelism
	if maxConcurrent.Load() < 2 {
		t.Logf("Warning: max concurrent was only %d (expected close to %d)", maxConcurrent.Load(), limit)
	}
}

func TestDefaultExportConcurrency(t *testing.T) {
	expected := runtime.NumCPU() * 4
	if DefaultExportConcurrency() != expected {
		t.Errorf("expected %d, got %d", expected, DefaultExportConcurrency())
	}
}

// Benchmarks

func BenchmarkConcurrencyLimiter_AcquireRelease(b *testing.B) {
	l := NewConcurrencyLimiter(runtime.NumCPU())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l.Acquire()
		l.Release()
	}
}

func BenchmarkConcurrencyLimiter_TryAcquire(b *testing.B) {
	l := NewConcurrencyLimiter(runtime.NumCPU())

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if l.TryAcquire() {
			l.Release()
		}
	}
}

func BenchmarkConcurrencyLimiter_Parallel(b *testing.B) {
	l := NewConcurrencyLimiter(runtime.NumCPU() * 4)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			l.Acquire()
			l.Release()
		}
	})
}

func BenchmarkConcurrencyLimiter_ParallelContended(b *testing.B) {
	// Small limit to force contention
	l := NewConcurrencyLimiter(2)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			l.Acquire()
			runtime.Gosched() // Simulate some work
			l.Release()
		}
	})
}
