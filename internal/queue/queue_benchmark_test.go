package queue

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
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

	b.ReportAllocs()
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

	b.ReportAllocs()
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

	b.ReportAllocs()
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

	b.ReportAllocs()
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

			b.ReportAllocs()
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

	b.ReportAllocs()
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

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = q.Push(req)
		}
	})
}

// BenchmarkQueue_Pop_UnmarshalVT benchmarks the queue pop path which uses UnmarshalVT.
func BenchmarkQueue_Pop_UnmarshalVT(b *testing.B) {
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

	sizes := []struct {
		name       string
		metrics    int
		datapoints int
	}{
		{"small_10x10", 10, 10},
		{"medium_50x50", 50, 50},
		{"large_100x100", 100, 100},
	}

	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			req := createBenchmarkRequest(size.metrics, size.datapoints)
			// Pre-fill queue
			for i := 0; i < b.N+100; i++ {
				_ = q.Push(req)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				entry, _ := q.Pop()
				if entry != nil {
					_, _ = entry.GetRequest() // Uses UnmarshalVT internally
				}
			}
		})
	}
}

// TestQueue_VTProto_MarshalUnmarshal_RoundTrip verifies data integrity through
// the disk queue's MarshalVT (Push) → UnmarshalVT (Pop/GetRequest) cycle.
func TestQueue_VTProto_MarshalUnmarshal_RoundTrip(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "queue-vt-roundtrip-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := Config{
		Path:          filepath.Join(tmpDir, "queue"),
		MaxSize:       1000,
		MaxBytes:      1073741824,
		FullBehavior:  DropOldest,
		RetryInterval: 5 * time.Second,
		MaxRetryDelay: 5 * time.Minute,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Push with various payload sizes
	payloads := []struct {
		name    string
		metrics int
	}{
		{"single", 1},
		{"small", 10},
		{"medium", 100},
	}

	for _, p := range payloads {
		t.Run(p.name, func(t *testing.T) {
			original := createBenchmarkRequest(p.metrics, 5)

			if err := q.Push(original); err != nil {
				t.Fatalf("Push failed: %v", err)
			}

			entry, err := q.Pop()
			if err != nil {
				t.Fatalf("Pop failed: %v", err)
			}
			if entry == nil {
				t.Fatal("Pop returned nil")
			}

			restored, err := entry.GetRequest()
			if err != nil {
				t.Fatalf("GetRequest failed: %v", err)
			}

			// Verify structural equality
			if len(restored.ResourceMetrics) != len(original.ResourceMetrics) {
				t.Fatalf("ResourceMetrics count: got %d, want %d",
					len(restored.ResourceMetrics), len(original.ResourceMetrics))
			}
			for i, rm := range restored.ResourceMetrics {
				origRM := original.ResourceMetrics[i]
				if len(rm.ScopeMetrics) != len(origRM.ScopeMetrics) {
					t.Fatalf("ScopeMetrics[%d] count: got %d, want %d",
						i, len(rm.ScopeMetrics), len(origRM.ScopeMetrics))
				}
				for j, sm := range rm.ScopeMetrics {
					origSM := origRM.ScopeMetrics[j]
					if len(sm.Metrics) != len(origSM.Metrics) {
						t.Fatalf("Metrics[%d][%d] count: got %d, want %d",
							i, j, len(sm.Metrics), len(origSM.Metrics))
					}
					for k, m := range sm.Metrics {
						if m.Name != origSM.Metrics[k].Name {
							t.Errorf("Metric name: got %q, want %q", m.Name, origSM.Metrics[k].Name)
						}
					}
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Phase 8.12: Memory-optimization benchmarks for MemoryBatchQueue
// These benchmarks compare the reduced balanced-profile settings (MaxSize=256)
// against the original settings (MaxSize=1024) to quantify the throughput
// impact of the QueueInmemoryBlocks reduction.
// ---------------------------------------------------------------------------

// BenchmarkMemoryQueue_Push_ReducedBlocks benchmarks PushBatch throughput
// with MaxSize=256 (the new balanced profile value for QueueInmemoryBlocks).
func BenchmarkMemoryQueue_Push_ReducedBlocks(b *testing.B) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      256, // balanced profile: reduced from 1024
		FullBehavior: DropOldest,
	})
	defer q.Close()

	req := createBenchmarkRequest(10, 10)
	batch := NewExportBatch(req)

	// Drain in background to prevent queue from filling up
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				q.PopBatch()
			}
		}
	}()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = q.PushBatch(batch)
	}
	b.StopTimer()
	close(done)
}

// BenchmarkMemoryQueue_PushPop_ReducedBlocks benchmarks push+pop cycle
// with MaxSize=256 (balanced profile) to measure round-trip latency.
func BenchmarkMemoryQueue_PushPop_ReducedBlocks(b *testing.B) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      256, // balanced profile: reduced from 1024
		FullBehavior: DropOldest,
	})
	defer q.Close()

	req := createBenchmarkRequest(10, 10)
	batch := NewExportBatch(req)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = q.PushBatch(batch)
		_, _ = q.PopBatch()
	}
}

// BenchmarkMemoryQueue_Push_OriginalBlocks benchmarks PushBatch throughput
// with MaxSize=1024 (the original balanced profile value) for comparison
// against the reduced setting.
func BenchmarkMemoryQueue_Push_OriginalBlocks(b *testing.B) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      1024, // original balanced profile value
		FullBehavior: DropOldest,
	})
	defer q.Close()

	req := createBenchmarkRequest(10, 10)
	batch := NewExportBatch(req)

	// Drain in background to prevent queue from filling up
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				q.PopBatch()
			}
		}
	}()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = q.PushBatch(batch)
	}
	b.StopTimer()
	close(done)
}

// BenchmarkMemoryQueue_Push_PayloadSizes_ReducedBlocks benchmarks PushBatch
// with MaxSize=256 across different payload sizes to verify the reduced queue
// depth handles varying batch sizes without throughput degradation.
func BenchmarkMemoryQueue_Push_PayloadSizes_ReducedBlocks(b *testing.B) {
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
			q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
				MaxSize:      256,
				FullBehavior: DropOldest,
			})
			defer q.Close()

			req := createBenchmarkRequest(size.metrics, size.datapoints)
			batch := NewExportBatch(req)

			// Drain in background
			done := make(chan struct{})
			go func() {
				for {
					select {
					case <-done:
						return
					default:
						q.PopBatch()
					}
				}
			}()

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = q.PushBatch(batch)
			}
			b.StopTimer()
			close(done)
		})
	}
}

// BenchmarkMemoryQueue_DropOldest_ReducedBlocks benchmarks the drop_oldest
// behavior when the queue (MaxSize=256) is saturated. This is the critical
// path — with fewer blocks, drops happen sooner under sustained load.
func BenchmarkMemoryQueue_DropOldest_ReducedBlocks(b *testing.B) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      256,
		FullBehavior: DropOldest,
	})
	defer q.Close()

	req := createBenchmarkRequest(10, 10)
	batch := NewExportBatch(req)

	// Fill the queue completely
	for i := 0; i < 256; i++ {
		_ = q.PushBatch(batch)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = q.PushBatch(batch) // triggers drop_oldest on every push
	}
}

// BenchmarkMemoryQueue_Concurrent_ReducedBlocks benchmarks concurrent
// PushBatch with MaxSize=256. With a smaller channel buffer, contention
// patterns may differ from the original 1024 setting.
func BenchmarkMemoryQueue_Concurrent_ReducedBlocks(b *testing.B) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      256,
		FullBehavior: DropOldest,
	})
	defer q.Close()

	req := createBenchmarkRequest(10, 10)
	batch := NewExportBatch(req)

	// Drain in background
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				q.PopBatch()
			}
		}
	}()

	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = q.PushBatch(batch)
		}
	})
	close(done)
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
