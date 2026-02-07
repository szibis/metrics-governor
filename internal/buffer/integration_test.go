package buffer

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/exporter"
	"github.com/szibis/metrics-governor/internal/queue"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
)

// e2eSlowExporter simulates a slow destination with configurable delay.
type e2eSlowExporter struct {
	delay    time.Duration
	exported atomic.Int64
	mu       sync.Mutex
	failing  bool
}

func (e *e2eSlowExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	e.mu.Lock()
	failing := e.failing
	e.mu.Unlock()
	if failing {
		return fmt.Errorf("destination unavailable")
	}
	time.Sleep(e.delay)
	e.exported.Add(1)
	return nil
}

func (e *e2eSlowExporter) Close() error { return nil }

// e2eFastExporter counts exported ResourceMetrics without any delay.
type e2eFastExporter struct {
	exportCalls     atomic.Int64
	resourceMetrics atomic.Int64
}

func (e *e2eFastExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	e.exportCalls.Add(1)
	e.resourceMetrics.Add(int64(len(req.ResourceMetrics)))
	return nil
}

func (e *e2eFastExporter) Close() error { return nil }

// newE2EQueuedExporter creates a QueuedExporter in always-queue mode for integration tests.
func newE2EQueuedExporter(t *testing.T, exp exporter.Exporter, workers int) *exporter.QueuedExporter {
	t.Helper()
	tmpDir := t.TempDir()
	queueCfg := queue.Config{
		Path:               tmpDir,
		AlwaysQueue:        true,
		Workers:            workers,
		MaxSize:            100000,
		MaxBytes:           1 << 30, // 1GB
		RetryInterval:      time.Second,
		MaxRetryDelay:      time.Minute,
		CloseTimeout:       10 * time.Second,
		DrainTimeout:       5 * time.Second,
		DrainEntryTimeout:  2 * time.Second,
		RetryExportTimeout: 30 * time.Second,
		InmemoryBlocks:     4096,
	}
	qe, err := exporter.NewQueued(exp, queueCfg)
	if err != nil {
		t.Fatalf("failed to create QueuedExporter: %v", err)
	}
	return qe
}

// TestE2E_SlowExporter_FlushNonBlocking verifies that with always-queue mode,
// flush() completes quickly even when the exporter is slow.
// Regression: catches the case where flush blocks on wg.Wait() for slow exports.
func TestE2E_SlowExporter_FlushNonBlocking(t *testing.T) {
	slow := &e2eSlowExporter{delay: 5 * time.Second}
	qe := newE2EQueuedExporter(t, slow, 4)
	defer qe.Close()

	// Create buffer using the queued exporter (no concurrency limiter = always-queue path)
	buf := New(
		10000,     // maxSize
		100,       // maxBatchSize
		time.Hour, // flushInterval - don't auto-flush
		qe,        // exporter (satisfies buffer.Exporter via Export method)
		nil,       // stats
		nil,       // limits
		nil,       // logAggregator
	)

	// Add 100 batches to the buffer
	for i := 0; i < 100; i++ {
		metrics := createTestResourceMetrics(1)
		if err := buf.Add(metrics); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	// Flush should complete quickly because always-queue mode just pushes to queue
	start := time.Now()
	buf.flush(context.Background())
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Errorf("flush() took %v, expected <100ms (regression: flush is blocking on slow exporter)", elapsed)
	}

	// Verify data was queued (workers will process it asynchronously)
	if qe.QueueLen() == 0 && slow.exported.Load() == 0 {
		t.Error("expected data to be queued or already exported")
	}
}

// TestE2E_FastExporter_NoDataLoss verifies that all batches sent through the
// always-queue pipeline are eventually exported without data loss.
func TestE2E_FastExporter_NoDataLoss(t *testing.T) {
	fast := &e2eFastExporter{}
	qe := newE2EQueuedExporter(t, fast, 4)
	defer qe.Close()

	buf := New(
		100000, // maxSize - large enough to hold all data
		100,    // maxBatchSize
		time.Hour,
		qe,
		nil, nil, nil,
	)

	const totalBatches = 1000
	const goroutines = 10
	batchesPerGoroutine := totalBatches / goroutines

	// Add batches from multiple goroutines (simulating concurrent receivers)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < batchesPerGoroutine; i++ {
				metrics := createTestResourceMetrics(1)
				if err := buf.Add(metrics); err != nil {
					t.Errorf("Add failed: %v", err)
				}
			}
		}()
	}
	wg.Wait()

	// Flush all buffered data into the queue
	buf.flush(context.Background())

	// Wait for workers to process all queued entries.
	// Track ResourceMetrics count (not Export calls) because the buffer
	// batches individual Add() calls into larger batches (maxBatchSize=100).
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if fast.resourceMetrics.Load() == totalBatches && qe.QueueLen() == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	exportedRM := fast.resourceMetrics.Load()
	exportCalls := fast.exportCalls.Load()
	remaining := qe.QueueLen()

	if exportedRM != totalBatches {
		t.Errorf("data loss detected: resourceMetrics=%d, exportCalls=%d, queued=%d, expected=%d",
			exportedRM, exportCalls, remaining, totalBatches)
	}
}

// TestE2E_BackpressurePropagation verifies that when the buffer's byte capacity
// is exceeded, ErrBufferFull is returned to callers (backpressure propagation).
func TestE2E_BackpressurePropagation(t *testing.T) {
	fast := &e2eFastExporter{}
	qe := newE2EQueuedExporter(t, fast, 2)
	defer qe.Close()

	// Create a buffer with a very small byte capacity
	buf := New(
		100000,
		100,
		time.Hour,
		qe,
		nil, nil, nil,
		WithMaxBufferBytes(100),                // Very small: ~100 bytes
		WithBufferFullPolicy(queue.DropNewest), // Reject when full
	)

	// Add data until we get ErrBufferFull
	gotBufferFull := false
	for i := 0; i < 1000; i++ {
		metrics := createTestResourceMetrics(5) // Each batch has some size
		err := buf.Add(metrics)
		if err == ErrBufferFull {
			gotBufferFull = true
			break
		}
	}

	if !gotBufferFull {
		t.Error("expected ErrBufferFull to be returned when buffer capacity is exceeded")
	}
}

// TestE2E_ConcurrentReceivers_BufferSafe verifies that concurrent Add() calls
// are safe under the race detector. Run with -race to detect data races.
func TestE2E_ConcurrentReceivers_BufferSafe(t *testing.T) {
	fast := &e2eFastExporter{}
	qe := newE2EQueuedExporter(t, fast, 4)
	defer qe.Close()

	buf := New(
		100000,
		100,
		50*time.Millisecond, // Short flush interval to exercise concurrent flush + add
		qe,
		&mockStatsCollector{},
		&mockLimitsEnforcer{},
		nil,
		WithMaxBufferBytes(1<<20), // 1MB
		WithBufferFullPolicy(queue.DropNewest),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	const numGoroutines = 50
	const addsPerGoroutine = 100

	var wg sync.WaitGroup
	var addErrors atomic.Int64

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < addsPerGoroutine; i++ {
				metrics := createTestResourceMetrics(1)
				if err := buf.Add(metrics); err != nil {
					// ErrBufferFull is acceptable under concurrent load
					addErrors.Add(1)
				}
			}
		}()
	}

	wg.Wait()

	// Let remaining flushes complete
	time.Sleep(200 * time.Millisecond)
	cancel()
	buf.Wait()

	// We expect the majority of adds to succeed (some may be rejected under load)
	totalAttempted := int64(numGoroutines * addsPerGoroutine)
	errors := addErrors.Load()
	successRate := float64(totalAttempted-errors) / float64(totalAttempted)

	t.Logf("concurrent adds: %d attempted, %d errors, %.1f%% success rate",
		totalAttempted, errors, successRate*100)

	// At minimum, some adds should succeed
	if successRate < 0.01 {
		t.Errorf("too many failures: success rate %.1f%%", successRate*100)
	}
}

// TestE2E_GoroutineLeakCheck verifies that creating and closing a QueuedExporter
// does not leak worker goroutines.
func TestE2E_GoroutineLeakCheck(t *testing.T) {
	// Let existing goroutines settle
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	before := runtime.NumGoroutine()

	// Create and close multiple QueuedExporters
	for i := 0; i < 3; i++ {
		fast := &e2eFastExporter{}
		qe := newE2EQueuedExporter(t, fast, 4)

		// Push some data to exercise workers
		for j := 0; j < 10; j++ {
			req := &colmetricspb.ExportMetricsServiceRequest{
				ResourceMetrics: createTestResourceMetrics(1),
			}
			if err := qe.Export(context.Background(), req); err != nil {
				t.Fatalf("Export failed: %v", err)
			}
		}

		// Let workers process
		time.Sleep(200 * time.Millisecond)

		// Close should stop all workers
		if err := qe.Close(); err != nil {
			t.Fatalf("Close failed: %v", err)
		}
	}

	// Let goroutines settle after cleanup
	runtime.GC()
	time.Sleep(500 * time.Millisecond)

	after := runtime.NumGoroutine()

	// Allow a small delta for runtime goroutines (GC, finalizers, etc.)
	delta := after - before
	if delta > 5 {
		t.Errorf("goroutine leak detected: before=%d, after=%d, delta=%d (expected delta <= 5)",
			before, after, delta)
	} else {
		t.Logf("goroutine check passed: before=%d, after=%d, delta=%d", before, after, delta)
	}
}
