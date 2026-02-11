package exporter

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
)

// --- Race condition tests ---

func TestRace_ConcurrencyLimiter_AcquireRelease(t *testing.T) {
	limiter := NewConcurrencyLimiter(10)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				limiter.Acquire()
				// Simulate work
				runtime.Gosched()
				limiter.Release()
			}
		}()
	}

	wg.Wait()

	if limiter.Available() != limiter.Limit() {
		t.Errorf("All slots should be available: available=%d, limit=%d",
			limiter.Available(), limiter.Limit())
	}
}

func TestRace_ConcurrencyLimiter_TryAcquireRelease(t *testing.T) {
	limiter := NewConcurrencyLimiter(5)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 300; j++ {
				if limiter.TryAcquire() {
					runtime.Gosched()
					limiter.Release()
				}
				_ = limiter.Available()
				_ = limiter.InUse()
			}
		}()
	}

	wg.Wait()
}

func TestRace_ConcurrencyLimiter_AcquireContext(t *testing.T) {
	limiter := NewConcurrencyLimiter(3)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if err := limiter.AcquireContext(ctx); err == nil {
					runtime.Gosched()
					limiter.Release()
				}
			}
		}()
	}

	wg.Wait()
}

func TestRace_ConcurrencyLimiter_AcquireTimeout(t *testing.T) {
	limiter := NewConcurrencyLimiter(2)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if limiter.AcquireTimeout(time.Millisecond) {
					runtime.Gosched()
					limiter.Release()
				}
			}
		}()
	}

	wg.Wait()
}

func TestRace_ConcurrencyLimiter_MixedOperations(t *testing.T) {
	limiter := NewConcurrencyLimiter(8)
	ctx := context.Background()

	var wg sync.WaitGroup

	// Regular acquire/release
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				limiter.Acquire()
				limiter.Release()
			}
		}()
	}

	// TryAcquire
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if limiter.TryAcquire() {
					limiter.Release()
				}
			}
		}()
	}

	// Context acquire
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if err := limiter.AcquireContext(ctx); err == nil {
					limiter.Release()
				}
			}
		}()
	}

	// Stats readers
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = limiter.Available()
				_ = limiter.InUse()
				_ = limiter.Limit()
			}
		}()
	}

	wg.Wait()
}

func TestRace_CircuitBreaker_ConcurrentStateChanges(t *testing.T) {
	cb := NewCircuitBreaker(5, 100*time.Millisecond)

	var wg sync.WaitGroup

	// Recorders
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				if j%3 == 0 {
					cb.RecordFailure()
				} else {
					cb.RecordSuccess()
				}
			}
		}(i)
	}

	// Readers
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_ = cb.AllowRequest()
				_ = cb.State()
				_ = cb.ConsecutiveFailures()
			}
		}()
	}

	wg.Wait()
}

func TestRace_CircuitBreaker_OpenCloseTransitions(t *testing.T) {
	cb := NewCircuitBreaker(3, 10*time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				// Drive state machine through transitions
				for k := 0; k < 5; k++ {
					cb.RecordFailure()
				}
				time.Sleep(time.Millisecond)
				cb.AllowRequest()
				cb.RecordSuccess()
			}
		}(i)
	}

	wg.Wait()
}

// --- Export pool race tests ---

func TestRace_ExportReqPool_ConcurrentMarshal(t *testing.T) {
	const goroutines = 8
	const iterations = 100

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				req := colmetricspb.ExportMetricsServiceRequestFromVTPool()

				// Simulate unmarshal by directly setting fields
				req.ResourceMetrics = []*metricspb.ResourceMetrics{{
					ScopeMetrics: []*metricspb.ScopeMetrics{{
						Metrics: []*metricspb.Metric{{Name: fmt.Sprintf("race_%d_%d", id, i)}},
					}},
				}}

				// Marshal like the export path would
				data, err := req.MarshalVT()
				if err != nil {
					t.Errorf("goroutine %d iter %d: marshal failed: %v", id, i, err)
				}
				if len(data) == 0 {
					t.Errorf("goroutine %d iter %d: empty marshal result", id, i)
				}

				req.ReturnToVTPool()
				runtime.Gosched()
			}
		}(g)
	}

	wg.Wait()
}

// --- Memory leak tests ---

func TestMemLeak_ConcurrencyLimiter_AcquireReleaseCycles(t *testing.T) {
	limiter := NewConcurrencyLimiter(10)

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10000; j++ {
				limiter.Acquire()
				limiter.Release()
			}
		}()
	}

	wg.Wait()

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("ConcurrencyLimiter cycles: heap_before=%dKB, heap_after=%dKB",
		heapBefore/1024, heapAfter/1024)

	if heapAfter > heapBefore+10*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after 100K acquire/release cycles",
			heapBefore/1024, heapAfter/1024)
	}
}

func TestMemLeak_CircuitBreaker_StateCycles(t *testing.T) {
	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	// Create and cycle many circuit breakers
	for i := 0; i < 1000; i++ {
		cb := NewCircuitBreaker(3, time.Millisecond)
		for j := 0; j < 10; j++ {
			cb.RecordFailure()
		}
		cb.RecordSuccess()
		_ = fmt.Sprintf("%v", cb.State())
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("CircuitBreaker cycles: heap_before=%dKB, heap_after=%dKB",
		heapBefore/1024, heapAfter/1024)

	if heapAfter > heapBefore+10*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB",
			heapBefore/1024, heapAfter/1024)
	}
}
