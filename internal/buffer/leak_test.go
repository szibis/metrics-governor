package buffer

import (
	"context"
	"runtime"
	"testing"
	"time"

	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	"go.uber.org/goleak"
)

func TestLeakCheck_MetricsBuffer(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	exporter := &mockExporter{}
	stats := &mockStatsCollector{}
	buf := New(100, 50, 100*time.Millisecond, exporter, stats, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Add some data and let it flush
	buf.Add(createTestResourceMetrics(10))
	time.Sleep(200 * time.Millisecond)

	cancel()
	buf.Wait()
}

func TestLeakCheck_MemoryQueue(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	q := NewMemoryQueue(100, 1024*1024)

	// Push and pop some entries
	for i := 0; i < 10; i++ {
		rm := createTestResourceMetrics(1)
		req := &colmetricspb.ExportMetricsServiceRequest{
			ResourceMetrics: rm,
		}
		if err := q.Push(req); err != nil {
			t.Fatalf("Push failed: %v", err)
		}
	}

	for q.Len() > 0 {
		q.Pop()
	}
}

// TestLeak_Buffer_CachedSizeCleanup verifies that cached sizes are properly
// freed on flush and don't accumulate memory across add/flush cycles.
func TestLeak_Buffer_CachedSizeCleanup(t *testing.T) {
	exp := &mockExporter{}
	buf := New(1000, 100, time.Hour, exp, nil, nil, nil)

	// Warm up
	for i := 0; i < 10; i++ {
		buf.Add(createTestResourceMetrics(50))
		buf.flush(context.Background())
	}

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	// Run many add/flush cycles
	const cycles = 100
	for i := 0; i < cycles; i++ {
		buf.Add(createTestResourceMetrics(50))
		buf.flush(context.Background())
	}

	// Verify metricSizes is empty after final flush
	buf.mu.Lock()
	sizesLen := len(buf.metricSizes)
	metricsLen := len(buf.metrics)
	buf.mu.Unlock()

	if sizesLen != 0 {
		t.Errorf("metricSizes should be empty after flush, got len=%d", sizesLen)
	}
	if metricsLen != 0 {
		t.Errorf("metrics should be empty after flush, got len=%d", metricsLen)
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("Buffer cached size cleanup: heap_before=%dKB, heap_after=%dKB, cycles=%d",
		heapBefore/1024, heapAfter/1024, cycles)

	const maxGrowthBytes = 10 * 1024 * 1024 // 10MB threshold
	if heapAfter > heapBefore+maxGrowthBytes {
		t.Errorf("Possible memory leak in cached sizes: heap grew from %dKB to %dKB",
			heapBefore/1024, heapAfter/1024)
	}
}
