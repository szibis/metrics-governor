package buffer

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/exporter"
	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	"github.com/szibis/metrics-governor/internal/queue"
)

// aqBenchExporter is a no-op exporter for always-queue benchmarks.
type aqBenchExporter struct{}

func (e *aqBenchExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	return nil
}

func (e *aqBenchExporter) Close() error { return nil }

// newAQBenchQueuedExporter creates a QueuedExporter in always-queue mode for benchmarks.
func newAQBenchQueuedExporter(b *testing.B) *exporter.QueuedExporter {
	b.Helper()
	tmpDir := b.TempDir()
	queueCfg := queue.Config{
		Path:               tmpDir,
		AlwaysQueue:        true,
		Workers:            4,
		MaxSize:            1000000,
		MaxBytes:           1 << 30, // 1GB
		RetryInterval:      time.Second,
		MaxRetryDelay:      time.Minute,
		CloseTimeout:       5 * time.Second,
		DrainTimeout:       5 * time.Second,
		DrainEntryTimeout:  2 * time.Second,
		RetryExportTimeout: 30 * time.Second,
		InmemoryBlocks:     4096,
	}
	qe, err := exporter.NewQueued(&aqBenchExporter{}, queueCfg)
	if err != nil {
		b.Fatalf("failed to create QueuedExporter: %v", err)
	}
	return qe
}

// BenchmarkFlush_AlwaysQueue measures flush() throughput with an always-queue exporter.
// In always-queue mode, each batch is pushed to the queue in microseconds,
// so flush should be very fast regardless of batch count.
func BenchmarkFlush_AlwaysQueue(b *testing.B) {
	qe := newAQBenchQueuedExporter(b)
	defer qe.Close()

	buf := New(
		100000,
		100,
		time.Hour,
		qe,
		nil, nil, nil,
	)

	// Pre-create test data
	testMetrics := createTestResourceMetrics(100)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Add data to buffer
		buf.Add(testMetrics)
		// Flush it (this pushes to queue in always-queue mode)
		buf.flush(context.Background())
	}
}

// BenchmarkAdd_WithCapacityCheck measures Add() overhead from byte estimation
// and capacity checking.
func BenchmarkAdd_WithCapacityCheck(b *testing.B) {
	qe := newAQBenchQueuedExporter(b)
	defer qe.Close()

	buf := New(
		1000000,
		1000,
		time.Hour,
		qe,
		nil, nil, nil,
		WithMaxBufferBytes(1<<30), // 1GB capacity
		WithBufferFullPolicy(queue.DropNewest),
	)

	testMetrics := createTestResourceMetrics(10)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		buf.Add(testMetrics)

		// Periodically flush to prevent buffer from growing unbounded
		if i%1000 == 0 {
			buf.flush(context.Background())
		}
	}
}

// BenchmarkSplitByBytes_VariousSizes measures the splitter performance with various batch sizes.
func BenchmarkSplitByBytes_VariousSizes(b *testing.B) {
	benchCases := []struct {
		name      string
		batchSize int
		maxBytes  int
	}{
		{"Small_NoSplit", 10, 0},         // No splitting (maxBytes=0 disables)
		{"Small_WithSplit", 10, 100},     // Small batch, needs splitting
		{"Medium_NoSplit", 100, 0},       // Medium batch, no splitting
		{"Medium_WithSplit", 100, 1000},  // Medium batch, needs splitting
		{"Large_NoSplit", 1000, 0},       // Large batch, no splitting
		{"Large_WithSplit", 1000, 10000}, // Large batch, needs splitting
	}

	for _, bc := range benchCases {
		b.Run(bc.name, func(b *testing.B) {
			batch := createTestResourceMetrics(bc.batchSize)

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				_ = splitByBytes(batch, bc.maxBytes)
			}
		})
	}
}

// BenchmarkAdd_Concurrent measures concurrent Add() performance from multiple goroutines.
func BenchmarkAdd_Concurrent(b *testing.B) {
	goroutineCounts := []int{1, 4, 8, 16}

	for _, numG := range goroutineCounts {
		b.Run(fmt.Sprintf("Goroutines_%d", numG), func(b *testing.B) {
			qe := newAQBenchQueuedExporter(b)
			defer qe.Close()

			buf := New(
				1000000,
				1000,
				time.Hour,
				qe,
				nil, nil, nil,
			)

			testMetrics := createTestResourceMetrics(5)
			perGoroutine := b.N / numG
			if perGoroutine == 0 {
				perGoroutine = 1
			}

			// Start a periodic flush to prevent buffer growth
			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						buf.flush(context.Background())
					}
				}
			}()

			b.ResetTimer()
			b.ReportAllocs()

			var wg sync.WaitGroup
			for g := 0; g < numG; g++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < perGoroutine; i++ {
						buf.Add(testMetrics)
					}
				}()
			}
			wg.Wait()

			b.StopTimer()
			cancel()
		})
	}
}
