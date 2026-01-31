package queue

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// BenchmarkQueue_Push benchmarks pushing to the queue
func BenchmarkQueue_Push(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "queue-bench-*")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := Config{
		Path:          filepath.Join(tmpDir, "queue"),
		MaxSize:       100000,
		MaxBytes:      1073741824, // 1GB
		FullBehavior:  DropOldest,
		RetryInterval: 5 * time.Second,
		MaxRetryDelay: 5 * time.Minute,
	}

	q, err := New(cfg)
	if err != nil {
		b.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	req := createBenchmarkRequest(10, 10)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = q.Push(req)
	}
}

// BenchmarkQueue_PushPop benchmarks push/pop cycle
func BenchmarkQueue_PushPop(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "queue-bench-*")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := Config{
		Path:          filepath.Join(tmpDir, "queue"),
		MaxSize:       100000,
		MaxBytes:      1073741824,
		FullBehavior:  DropOldest,
		RetryInterval: 5 * time.Second,
		MaxRetryDelay: 5 * time.Minute,
	}

	q, err := New(cfg)
	if err != nil {
		b.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	req := createBenchmarkRequest(10, 10)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = q.Push(req)
		_, _ = q.Pop()
	}
}

// BenchmarkQueue_Peek benchmarks peeking at the queue
func BenchmarkQueue_Peek(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "queue-bench-*")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := Config{
		Path:          filepath.Join(tmpDir, "queue"),
		MaxSize:       100000,
		MaxBytes:      1073741824,
		FullBehavior:  DropOldest,
		RetryInterval: 5 * time.Second,
		MaxRetryDelay: 5 * time.Minute,
	}

	q, err := New(cfg)
	if err != nil {
		b.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Pre-populate queue
	req := createBenchmarkRequest(10, 10)
	for i := 0; i < 1000; i++ {
		_ = q.Push(req)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = q.Peek()
	}
}

// BenchmarkQueue_LenSize benchmarks getting queue length and size
func BenchmarkQueue_LenSize(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "queue-bench-*")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := Config{
		Path:          filepath.Join(tmpDir, "queue"),
		MaxSize:       100000,
		MaxBytes:      1073741824,
		FullBehavior:  DropOldest,
		RetryInterval: 5 * time.Second,
		MaxRetryDelay: 5 * time.Minute,
	}

	q, err := New(cfg)
	if err != nil {
		b.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Pre-populate queue
	req := createBenchmarkRequest(10, 10)
	for i := 0; i < 1000; i++ {
		_ = q.Push(req)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = q.Len()
		_ = q.Size()
	}
}

// BenchmarkQueue_Push_PayloadSizes benchmarks with different payload sizes
func BenchmarkQueue_Push_PayloadSizes(b *testing.B) {
	sizes := []struct {
		name       string
		metrics    int
		datapoints int
	}{
		{"tiny_1x1", 1, 1},
		{"small_10x10", 10, 10},
		{"medium_50x50", 50, 50},
		{"large_100x100", 100, 100},
	}

	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			tmpDir, err := os.MkdirTemp("", "queue-bench-*")
			if err != nil {
				b.Fatalf("Failed to create temp dir: %v", err)
			}
			defer os.RemoveAll(tmpDir)

			cfg := Config{
				Path:          filepath.Join(tmpDir, "queue"),
				MaxSize:       100000,
				MaxBytes:      1073741824,
				FullBehavior:  DropOldest,
				RetryInterval: 5 * time.Second,
				MaxRetryDelay: 5 * time.Minute,
			}

			q, err := New(cfg)
			if err != nil {
				b.Fatalf("Failed to create queue: %v", err)
			}
			defer q.Close()

			req := createBenchmarkRequest(size.metrics, size.datapoints)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = q.Push(req)
			}
		})
	}
}

// BenchmarkQueue_DropOldest benchmarks the drop_oldest behavior
func BenchmarkQueue_DropOldest(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "queue-bench-*")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := Config{
		Path:          filepath.Join(tmpDir, "queue"),
		MaxSize:       100, // Small queue to trigger drops
		MaxBytes:      1073741824,
		FullBehavior:  DropOldest,
		RetryInterval: 5 * time.Second,
		MaxRetryDelay: 5 * time.Minute,
	}

	q, err := New(cfg)
	if err != nil {
		b.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	req := createBenchmarkRequest(10, 10)

	// Fill the queue
	for i := 0; i < 100; i++ {
		_ = q.Push(req)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = q.Push(req) // This will drop oldest
	}
}

// BenchmarkQueue_Concurrent benchmarks concurrent push operations
func BenchmarkQueue_Concurrent(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "queue-bench-*")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := Config{
		Path:          filepath.Join(tmpDir, "queue"),
		MaxSize:       100000,
		MaxBytes:      1073741824,
		FullBehavior:  DropOldest,
		RetryInterval: 5 * time.Second,
		MaxRetryDelay: 5 * time.Minute,
	}

	q, err := New(cfg)
	if err != nil {
		b.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	req := createBenchmarkRequest(10, 10)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = q.Push(req)
		}
	})
}

// Helper functions

func createBenchmarkRequest(numMetrics, datapointsPerMetric int) *colmetricspb.ExportMetricsServiceRequest {
	metrics := make([]*metricspb.Metric, numMetrics)
	for i := 0; i < numMetrics; i++ {
		dps := make([]*metricspb.NumberDataPoint, datapointsPerMetric)
		for j := 0; j < datapointsPerMetric; j++ {
			dps[j] = &metricspb.NumberDataPoint{
				TimeUnixNano: uint64(time.Now().UnixNano()),
				Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(j)},
				Attributes: []*commonpb.KeyValue{
					{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "benchmark-service"}}},
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

	return &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
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
		},
	}
}
