package buffer

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"

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

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Add(metrics)
	}
}

// BenchmarkBuffer_AddWithStats benchmarks adding metrics with stats collection
func BenchmarkBuffer_AddWithStats(b *testing.B) {
	exp := &noopExporter{}
	statsCollector := stats.NewCollector([]string{"service", "env"})
	buf := New(100000, 1000, time.Hour, exp, statsCollector, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Start(ctx)

	metrics := createBenchmarkResourceMetrics(100, 10)

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

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Add(metrics)
	}
}

// BenchmarkBuffer_Scale tests performance at different scales
func BenchmarkBuffer_Scale(b *testing.B) {
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
			statsCollector := stats.NewCollector([]string{"service", "env"})
			// Use maxBatchSize equal to batchSize so each flush sends one batch
			buf := New(batchSize*2, batchSize, time.Hour, exp, statsCollector, nil, nil)

			// Pre-create metrics matching the batch size (10 datapoints per metric)
			metrics := createBenchmarkResourceMetrics(batchSize, 10)

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
			statsCollector := stats.NewCollector([]string{"service", "env"})
			buf := New(batchSize*2, batchSize, time.Hour, exp, statsCollector, nil, nil)

			metrics := createBenchmarkResourceMetrics(batchSize, 10)

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
