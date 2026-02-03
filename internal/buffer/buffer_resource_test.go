package buffer

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

func TestResourceUsage_BufferFlush(t *testing.T) {
	const (
		flushCount       = 1000
		metricsPerFlush  = 100
		maxBatchSize     = 100
		maxBufferSize    = 1000
	)

	// Pre-create metrics to avoid measuring metric creation allocations
	allMetrics := make([][]*metricspb.ResourceMetrics, flushCount)
	for i := 0; i < flushCount; i++ {
		allMetrics[i] = createTestResourceMetrics(metricsPerFlush)
	}

	exporter := &mockExporter{}
	stats := &mockStatsCollector{}
	buf := New(maxBufferSize, maxBatchSize, time.Hour, exporter, stats, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Warm up: run a few flush cycles so pools and internal structures are initialized
	for i := 0; i < 5; i++ {
		buf.Add(createTestResourceMetrics(metricsPerFlush))
		buf.flush(context.Background())
	}

	var mBefore, mAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&mBefore)

	for i := 0; i < flushCount; i++ {
		buf.Add(allMetrics[i])
		buf.flush(context.Background())
	}

	runtime.GC()
	runtime.ReadMemStats(&mAfter)

	cancel()
	buf.Wait()

	totalAllocs := mAfter.TotalAlloc - mBefore.TotalAlloc
	allocsPerFlush := totalAllocs / uint64(flushCount)

	t.Logf("Total allocations: %d bytes over %d flushes", totalAllocs, flushCount)
	t.Logf("Allocs per flush: %d bytes", allocsPerFlush)
	t.Logf("GC cycles: %d", mAfter.NumGC-mBefore.NumGC)
	t.Logf("Heap in use: before=%d after=%d delta=%d",
		mBefore.HeapInuse, mAfter.HeapInuse, mAfter.HeapInuse-mBefore.HeapInuse)
	t.Logf("Total export requests: %d", exporter.getRequestCount())

	// 100 metrics per flush, each ResourceMetrics is small protobuf.
	// Expect reasonable allocation overhead per flush (under 256KB).
	maxAllocsPerFlush := uint64(256 * 1024)
	if allocsPerFlush > maxAllocsPerFlush {
		t.Errorf("Allocation budget exceeded: %d > %d bytes/flush", allocsPerFlush, maxAllocsPerFlush)
	}
}

func TestResourceUsage_BufferFlushHeapBounded(t *testing.T) {
	const (
		flushCount      = 500
		metricsPerFlush = 50
		maxBatchSize    = 50
		maxBufferSize   = 500
	)

	exporter := &mockExporter{}
	buf := New(maxBufferSize, maxBatchSize, time.Hour, exporter, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Warm up
	for i := 0; i < 10; i++ {
		buf.Add(createTestResourceMetrics(metricsPerFlush))
		buf.flush(context.Background())
	}

	runtime.GC()
	var mBefore runtime.MemStats
	runtime.ReadMemStats(&mBefore)

	for i := 0; i < flushCount; i++ {
		metrics := make([]*metricspb.ResourceMetrics, metricsPerFlush)
		for j := 0; j < metricsPerFlush; j++ {
			metrics[j] = createTestResourceMetrics(1)[0]
		}
		buf.Add(metrics)
		buf.flush(context.Background())
	}

	runtime.GC()
	var mAfter runtime.MemStats
	runtime.ReadMemStats(&mAfter)

	cancel()
	buf.Wait()

	heapDelta := int64(mAfter.HeapInuse) - int64(mBefore.HeapInuse)
	t.Logf("Heap delta: %d bytes (%d KB)", heapDelta, heapDelta/1024)
	t.Logf("Heap in use: before=%d after=%d", mBefore.HeapInuse, mAfter.HeapInuse)
	t.Logf("Mallocs: %d, Frees: %d", mAfter.Mallocs-mBefore.Mallocs, mAfter.Frees-mBefore.Frees)
	t.Logf("Total export requests: %d", exporter.getRequestCount())

	// After GC, heap growth should be bounded. Allow up to 50MB including
	// race detector overhead and protobuf allocation retention.
	maxHeapGrowth := int64(50 * 1024 * 1024)
	if heapDelta > maxHeapGrowth {
		t.Errorf("Heap grew by %d bytes, exceeds %d byte limit", heapDelta, maxHeapGrowth)
	}
}

// createTestResourceMetricsNamed creates resource metrics with distinct metric names
// to simulate realistic workloads with varied metric names.
func createTestResourceMetricsNamed(count int, prefix string) []*metricspb.ResourceMetrics {
	result := make([]*metricspb.ResourceMetrics, count)
	for i := 0; i < count; i++ {
		dps := []*metricspb.NumberDataPoint{
			{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)}},
		}
		result[i] = &metricspb.ResourceMetrics{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: fmt.Sprintf("%s_metric_%d", prefix, i),
							Data: &metricspb.Metric_Gauge{
								Gauge: &metricspb.Gauge{
									DataPoints: dps,
								},
							},
						},
					},
				},
			},
		}
	}
	return result
}

func TestResourceUsage_BufferWithLimitsEnforcer(t *testing.T) {
	const (
		flushCount      = 200
		metricsPerFlush = 50
	)

	exporter := &mockExporter{}
	limits := &mockLimitsEnforcer{dropAll: false}
	stats := &mockStatsCollector{}
	buf := New(500, 50, time.Hour, exporter, stats, limits, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Warm up
	for i := 0; i < 5; i++ {
		buf.Add(createTestResourceMetricsNamed(metricsPerFlush, "warmup"))
		buf.flush(context.Background())
	}

	var mBefore, mAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&mBefore)

	for i := 0; i < flushCount; i++ {
		buf.Add(createTestResourceMetricsNamed(metricsPerFlush, fmt.Sprintf("batch%d", i)))
		buf.flush(context.Background())
	}

	runtime.GC()
	runtime.ReadMemStats(&mAfter)

	cancel()
	buf.Wait()

	totalAllocs := mAfter.TotalAlloc - mBefore.TotalAlloc
	allocsPerFlush := totalAllocs / uint64(flushCount)

	t.Logf("Total allocations: %d bytes over %d flushes (with limits enforcer)", totalAllocs, flushCount)
	t.Logf("Allocs per flush: %d bytes", allocsPerFlush)
	t.Logf("GC cycles: %d", mAfter.NumGC-mBefore.NumGC)
	t.Logf("Total export requests: %d", exporter.getRequestCount())

	// With the limits enforcer pass-through, overhead should be similar
	maxAllocsPerFlush := uint64(512 * 1024)
	if allocsPerFlush > maxAllocsPerFlush {
		t.Errorf("Allocation budget exceeded with limits enforcer: %d > %d bytes/flush",
			allocsPerFlush, maxAllocsPerFlush)
	}
}
