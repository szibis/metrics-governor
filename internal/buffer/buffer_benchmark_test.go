package buffer

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"

	"google.golang.org/protobuf/proto"

	"github.com/szibis/metrics-governor/internal/stats"
)

// noopExporter discards all exports
type noopExporter struct{}

func (n *noopExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	return nil
}

// countingExporter counts exports for verification
type countingExporter struct {
	mu    sync.Mutex
	count int
}

func (c *countingExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	c.mu.Lock()
	c.count++
	c.mu.Unlock()
	return nil
}

// BenchmarkBuffer_Add benchmarks adding metrics to buffer
func BenchmarkBuffer_Add(b *testing.B) {
	exp := &noopExporter{}
	buf := New(100000, 1000, time.Hour, exp, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Start(ctx)

	metrics := createBenchmarkResourceMetrics(100, 10)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Add(metrics)
	}
}

// BenchmarkBuffer_AddWithStats benchmarks adding metrics with stats collection
func BenchmarkBuffer_AddWithStats(b *testing.B) {
	exp := &noopExporter{}
	statsCollector := stats.NewCollector([]string{"service", "env"}, stats.StatsLevelFull)
	buf := New(100000, 1000, time.Hour, exp, statsCollector, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Start(ctx)

	metrics := createBenchmarkResourceMetrics(100, 10)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Add(metrics)
	}
}

// BenchmarkBuffer_ConcurrentAdd benchmarks concurrent adds
func BenchmarkBuffer_ConcurrentAdd(b *testing.B) {
	exp := &noopExporter{}
	buf := New(100000, 1000, time.Hour, exp, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Start(ctx)

	metrics := createBenchmarkResourceMetrics(100, 10)

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buf.Add(metrics)
		}
	})
}

// BenchmarkBuffer_HighThroughput benchmarks sustained high throughput
func BenchmarkBuffer_HighThroughput(b *testing.B) {
	exp := &countingExporter{}
	// Small batch size to trigger frequent flushes
	buf := New(10000, 100, 10*time.Millisecond, exp, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Start(ctx)

	metrics := createBenchmarkResourceMetrics(10, 10)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Add(metrics)
	}
}

// BenchmarkBuffer_Scale tests performance at different scales
func BenchmarkBuffer_Scale(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping scale benchmark in short mode")
	}
	scales := []struct {
		name       string
		bufferSize int
		batchSize  int
		metrics    int
		datapoints int
	}{
		{"small", 1000, 100, 10, 10},
		{"medium", 10000, 500, 50, 50},
		{"large", 100000, 1000, 100, 100},
		{"high_cardinality", 100000, 1000, 1000, 10},
		{"many_datapoints", 100000, 1000, 10, 1000},
	}

	for _, scale := range scales {
		b.Run(scale.name, func(b *testing.B) {
			exp := &noopExporter{}
			buf := New(scale.bufferSize, scale.batchSize, time.Hour, exp, nil, nil, nil)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go buf.Start(ctx)

			metrics := createBenchmarkResourceMetrics(scale.metrics, scale.datapoints)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf.Add(metrics)
			}
		})
	}
}

// BenchmarkBuffer_FlushThroughput measures flush throughput
func BenchmarkBuffer_FlushThroughput(b *testing.B) {
	exp := &countingExporter{}
	// Immediate flush on every add
	buf := New(10, 1, time.Millisecond, exp, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Start(ctx)

	metrics := createBenchmarkResourceMetrics(10, 10)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Add(metrics)
		// Small sleep to allow flush to complete
		time.Sleep(time.Microsecond)
	}
}

// BenchmarkFlush benchmarks flush at various batch sizes
func BenchmarkFlush(b *testing.B) {
	batchSizes := []int{10, 100, 1000}

	for _, batchSize := range batchSizes {
		b.Run(fmt.Sprintf("batch_%d", batchSize), func(b *testing.B) {
			exp := &noopExporter{}
			statsCollector := stats.NewCollector([]string{"service", "env"}, stats.StatsLevelFull)
			// Use maxBatchSize equal to batchSize so each flush sends one batch
			buf := New(batchSize*2, batchSize, time.Hour, exp, statsCollector, nil, nil)

			// Pre-create metrics matching the batch size (10 datapoints per metric)
			metrics := createBenchmarkResourceMetrics(batchSize, 10)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// Add metrics directly (no Start loop needed since we call flush manually)
				buf.mu.Lock()
				buf.metrics = append(buf.metrics[:0], metrics...)
				buf.mu.Unlock()

				buf.flush(context.Background())
			}
		})
	}
}

// BenchmarkFlush_WithStats benchmarks flush with stats collection enabled
func BenchmarkFlush_WithStats(b *testing.B) {
	batchSizes := []int{10, 100, 1000}

	for _, batchSize := range batchSizes {
		b.Run(fmt.Sprintf("batch_%d", batchSize), func(b *testing.B) {
			exp := &noopExporter{}
			statsCollector := stats.NewCollector([]string{"service", "env"}, stats.StatsLevelFull)
			buf := New(batchSize*2, batchSize, time.Hour, exp, statsCollector, nil, nil)

			metrics := createBenchmarkResourceMetrics(batchSize, 10)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf.mu.Lock()
				buf.metrics = append(buf.metrics[:0], metrics...)
				buf.mu.Unlock()

				buf.flush(context.Background())
			}
		})
	}
}

// BenchmarkFlush_NoStats benchmarks flush without stats collection
func BenchmarkFlush_NoStats(b *testing.B) {
	batchSizes := []int{10, 100, 1000}

	for _, batchSize := range batchSizes {
		b.Run(fmt.Sprintf("batch_%d", batchSize), func(b *testing.B) {
			exp := &noopExporter{}
			buf := New(batchSize*2, batchSize, time.Hour, exp, nil, nil, nil)

			metrics := createBenchmarkResourceMetrics(batchSize, 10)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf.mu.Lock()
				buf.metrics = append(buf.metrics[:0], metrics...)
				buf.mu.Unlock()

				buf.flush(context.Background())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Phase 8.12: Memory-optimization benchmarks for reduced buffer capacity
// These benchmarks compare BufferSize=2000 (new balanced profile) against
// BufferSize=5000 (original) to quantify the impact on Add throughput.
// ---------------------------------------------------------------------------

// BenchmarkBuffer_Add_ReducedCapacity benchmarks Add operations at both the
// original (5000) and reduced (2000) buffer sizes. The reduced capacity means
// the buffer hits its cap sooner, exercising the drop/reject path more often
// under sustained load.
func BenchmarkBuffer_Add_ReducedCapacity(b *testing.B) {
	configs := []struct {
		name       string
		bufferSize int
		batchSize  int
	}{
		{"reduced_2000", 2000, 500},  // new balanced profile
		{"original_5000", 5000, 500}, // original balanced profile
	}

	for _, cfg := range configs {
		b.Run(cfg.name, func(b *testing.B) {
			exp := &noopExporter{}
			buf := New(cfg.bufferSize, cfg.batchSize, time.Hour, exp, nil, nil, nil)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go buf.Start(ctx)

			metrics := createBenchmarkResourceMetrics(10, 10)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf.Add(metrics)
			}
		})
	}
}

// BenchmarkBuffer_Add_ReducedCapacity_HighCardinality benchmarks Add with
// high-cardinality payloads (many metrics, few datapoints each) at both
// buffer sizes. High cardinality workloads are common in production and
// stress the per-entry size tracking.
func BenchmarkBuffer_Add_ReducedCapacity_HighCardinality(b *testing.B) {
	configs := []struct {
		name       string
		bufferSize int
	}{
		{"reduced_2000", 2000},
		{"original_5000", 5000},
	}

	for _, cfg := range configs {
		b.Run(cfg.name, func(b *testing.B) {
			exp := &noopExporter{}
			buf := New(cfg.bufferSize, 500, time.Hour, exp, nil, nil, nil)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go buf.Start(ctx)

			// High cardinality: 500 metrics, 2 datapoints each
			metrics := createBenchmarkResourceMetrics(500, 2)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf.Add(metrics)
			}
		})
	}
}

// BenchmarkBuffer_FlushCycle_ReducedCapacity benchmarks full add-then-flush
// cycles at the reduced buffer size. With a smaller buffer, flushes are
// triggered more frequently, so flush overhead becomes proportionally larger.
func BenchmarkBuffer_FlushCycle_ReducedCapacity(b *testing.B) {
	configs := []struct {
		name       string
		bufferSize int
		batchSize  int
	}{
		{"reduced_2000_batch500", 2000, 500},
		{"original_5000_batch500", 5000, 500},
	}

	for _, cfg := range configs {
		b.Run(cfg.name, func(b *testing.B) {
			exp := &noopExporter{}
			buf := New(cfg.bufferSize, cfg.batchSize, time.Hour, exp, nil, nil, nil)

			metrics := createBenchmarkResourceMetrics(cfg.batchSize, 10)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf.mu.Lock()
				buf.metrics = append(buf.metrics[:0], metrics...)
				buf.mu.Unlock()

				buf.flush(context.Background())
			}
		})
	}
}

// Helper functions

func createBenchmarkResourceMetrics(numMetrics, datapointsPerMetric int) []*metricspb.ResourceMetrics {
	metrics := make([]*metricspb.Metric, numMetrics)
	for i := 0; i < numMetrics; i++ {
		dps := make([]*metricspb.NumberDataPoint, datapointsPerMetric)
		for j := 0; j < datapointsPerMetric; j++ {
			dps[j] = &metricspb.NumberDataPoint{
				TimeUnixNano: uint64(time.Now().UnixNano()),
				Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(j)},
				Attributes: []*commonpb.KeyValue{
					{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "benchmark-service"}}},
					{Key: "env", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "prod"}}},
				},
			}
		}
		metrics[i] = &metricspb.Metric{
			Name: fmt.Sprintf("benchmark_metric_%d", i),
			Data: &metricspb.Metric_Gauge{
				Gauge: &metricspb.Gauge{
					DataPoints: dps,
				},
			},
		}
	}

	return []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "benchmark-service"}}},
				},
			},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: metrics,
				},
			},
		},
	}
}

// createMixedResourceMetrics creates ResourceMetrics with gauge, sum, and histogram
// metric types for testing cached size accuracy across different metric kinds.
func createMixedResourceMetrics() []*metricspb.ResourceMetrics {
	now := uint64(time.Now().UnixNano())
	attrs := []*commonpb.KeyValue{
		{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "test-svc"}}},
	}

	gaugeMetric := &metricspb.Metric{
		Name: "test_gauge",
		Data: &metricspb.Metric_Gauge{
			Gauge: &metricspb.Gauge{
				DataPoints: []*metricspb.NumberDataPoint{
					{TimeUnixNano: now, Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 42.0}, Attributes: attrs},
				},
			},
		},
	}

	sumMetric := &metricspb.Metric{
		Name: "test_sum",
		Data: &metricspb.Metric_Sum{
			Sum: &metricspb.Sum{
				AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
				IsMonotonic:            true,
				DataPoints: []*metricspb.NumberDataPoint{
					{TimeUnixNano: now, Value: &metricspb.NumberDataPoint_AsInt{AsInt: 100}, Attributes: attrs},
					{TimeUnixNano: now, Value: &metricspb.NumberDataPoint_AsInt{AsInt: 200}, Attributes: attrs},
				},
			},
		},
	}

	histMetric := &metricspb.Metric{
		Name: "test_histogram",
		Data: &metricspb.Metric_Histogram{
			Histogram: &metricspb.Histogram{
				AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
				DataPoints: []*metricspb.HistogramDataPoint{
					{
						TimeUnixNano:   now,
						Count:          10,
						Sum:            func() *float64 { v := 55.5; return &v }(),
						BucketCounts:   []uint64{1, 2, 3, 4},
						ExplicitBounds: []float64{1.0, 5.0, 10.0},
						Attributes:     attrs,
					},
				},
			},
		},
	}

	return []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "gauge-svc"}}},
				},
			},
			ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{gaugeMetric}}},
		},
		{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "sum-svc"}}},
				},
			},
			ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{sumMetric}}},
		},
		{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "hist-svc"}}},
				},
			},
			ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{histMetric}}},
		},
	}
}

// TestBuffer_CachedSize_Accuracy verifies that cached sizes in metricSizes
// match proto.Size(rm) for each buffered entry.
func TestBuffer_CachedSize_Accuracy(t *testing.T) {
	exp := &noopExporter{}
	buf := New(1000, 100, time.Hour, exp, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Start(ctx)

	mixed := createMixedResourceMetrics()
	if err := buf.Add(mixed); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Also add some standard gauge-only metrics
	gaugeOnly := createBenchmarkResourceMetrics(5, 3)
	if err := buf.Add(gaugeOnly); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	buf.mu.Lock()
	defer buf.mu.Unlock()

	if len(buf.metrics) != len(buf.metricSizes) {
		t.Fatalf("length mismatch: metrics=%d metricSizes=%d", len(buf.metrics), len(buf.metricSizes))
	}

	for i, rm := range buf.metrics {
		expected := proto.Size(rm)
		if buf.metricSizes[i] != expected {
			t.Errorf("metricSizes[%d] = %d, want %d (proto.Size)", i, buf.metricSizes[i], expected)
		}
	}
}

// TestBuffer_CachedSize_SurvivesFlush verifies sizes are reset on flush
// and new entries after flush have correct cached sizes.
func TestBuffer_CachedSize_SurvivesFlush(t *testing.T) {
	exp := &noopExporter{}
	buf := New(1000, 100, time.Hour, exp, nil, nil, nil)

	// Add entries before flush (do not start the background loop;
	// we call flush manually to keep the test deterministic)
	pre := createBenchmarkResourceMetrics(3, 5)
	if err := buf.Add(pre); err != nil {
		t.Fatalf("Add (pre-flush) failed: %v", err)
	}

	// Verify pre-flush state
	buf.mu.Lock()
	preFlushedLen := len(buf.metrics)
	preFlushedSizesLen := len(buf.metricSizes)
	buf.mu.Unlock()

	if preFlushedLen == 0 {
		t.Fatal("expected metrics in buffer before flush")
	}
	if preFlushedLen != preFlushedSizesLen {
		t.Fatalf("pre-flush length mismatch: metrics=%d metricSizes=%d", preFlushedLen, preFlushedSizesLen)
	}

	// Flush clears the buffer
	buf.flush(context.Background())

	buf.mu.Lock()
	afterFlushLen := len(buf.metrics)
	afterFlushSizesLen := len(buf.metricSizes)
	buf.mu.Unlock()

	if afterFlushLen != 0 {
		t.Fatalf("expected 0 metrics after flush, got %d", afterFlushLen)
	}
	if afterFlushSizesLen != 0 {
		t.Fatalf("expected 0 metricSizes after flush, got %d", afterFlushSizesLen)
	}

	// Add new entries after flush
	post := createMixedResourceMetrics()
	if err := buf.Add(post); err != nil {
		t.Fatalf("Add (post-flush) failed: %v", err)
	}

	buf.mu.Lock()
	defer buf.mu.Unlock()

	if len(buf.metrics) != len(buf.metricSizes) {
		t.Fatalf("post-flush length mismatch: metrics=%d metricSizes=%d", len(buf.metrics), len(buf.metricSizes))
	}

	for i, rm := range buf.metrics {
		expected := proto.Size(rm)
		if buf.metricSizes[i] != expected {
			t.Errorf("post-flush metricSizes[%d] = %d, want %d", i, buf.metricSizes[i], expected)
		}
	}
}

// BenchmarkBuffer_EstimateSize_Cached_vs_Uncached measures the benefit of cached sizes
// by comparing buffer with cached sizes vs computing proto.Size per entry on the fly.
func BenchmarkBuffer_EstimateSize_Cached_vs_Uncached(b *testing.B) {
	// Pre-fill a buffer with a realistic number of entries
	const numEntries = 500
	entries := createBenchmarkResourceMetrics(numEntries, 10)

	b.Run("cached", func(b *testing.B) {
		exp := &noopExporter{}
		buf := New(numEntries*2, numEntries, time.Hour, exp, nil, nil, nil)

		// Populate the buffer so metricSizes is filled
		if err := buf.Add(entries); err != nil {
			b.Fatalf("Add failed: %v", err)
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			buf.mu.Lock()
			total := 0
			for _, s := range buf.metricSizes {
				total += s
			}
			buf.mu.Unlock()
			_ = total
		}
	})

	b.Run("uncached", func(b *testing.B) {
		exp := &noopExporter{}
		buf := New(numEntries*2, numEntries, time.Hour, exp, nil, nil, nil)

		// Populate the buffer
		if err := buf.Add(entries); err != nil {
			b.Fatalf("Add failed: %v", err)
		}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			buf.mu.Lock()
			total := 0
			for _, rm := range buf.metrics {
				total += proto.Size(rm)
			}
			buf.mu.Unlock()
			_ = total
		}
	})
}
