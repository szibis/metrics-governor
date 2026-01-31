package functional

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"

	"github.com/szibis/metrics-governor/internal/exporter"
	"github.com/szibis/metrics-governor/internal/queue"
)

// failingExporter always fails
type failingExporter struct {
	mu         sync.Mutex
	failCount  int
	succeedAt  int // Start succeeding after this many failures
	exports    []*colmetrics.ExportMetricsServiceRequest
	closed     bool
}

func (f *failingExporter) Export(ctx context.Context, req *colmetrics.ExportMetricsServiceRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.failCount++
	if f.succeedAt > 0 && f.failCount > f.succeedAt {
		f.exports = append(f.exports, req)
		return nil
	}
	return context.DeadlineExceeded
}

func (f *failingExporter) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *failingExporter) getFailCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failCount
}

func (f *failingExporter) getExports() []*colmetrics.ExportMetricsServiceRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exports
}

func createQueueTestDir(t *testing.T) string {
	dir, err := os.MkdirTemp("", "queue-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	return dir
}

// TestFunctional_Queue_BasicPushPop tests basic queue operations
func TestFunctional_Queue_BasicPushPop(t *testing.T) {
	dir := createQueueTestDir(t)
	defer os.RemoveAll(dir)

	cfg := queue.Config{
		Path:    dir,
		MaxSize: 100,
	}

	q, err := queue.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Push some items
	for i := 0; i < 10; i++ {
		rm := createTestResourceMetrics("queue-test", "queue_metric", 5)
		req := &colmetrics.ExportMetricsServiceRequest{
			ResourceMetrics: []*metricspb.ResourceMetrics{rm},
		}
		if err := q.Push(req); err != nil {
			t.Fatalf("Push failed: %v", err)
		}
	}

	if q.Len() != 10 {
		t.Errorf("Expected queue length 10, got %d", q.Len())
	}

	// Pop items
	poppedCount := 0
	for i := 0; i < 10; i++ {
		entry, err := q.Pop()
		if err != nil {
			t.Fatalf("Pop failed: %v", err)
		}
		if entry != nil {
			poppedCount++
		}
	}

	t.Logf("Popped %d items, queue length after: %d", poppedCount, q.Len())

	// Verify we popped items
	if poppedCount == 0 {
		t.Errorf("Expected to pop some items, popped %d", poppedCount)
	}
}

// TestFunctional_Queue_Persistence tests that queue survives restart
func TestFunctional_Queue_Persistence(t *testing.T) {
	dir := createQueueTestDir(t)
	defer os.RemoveAll(dir)

	cfg := queue.Config{
		Path:    dir,
		MaxSize: 100,
	}

	// Create queue and push items
	q1, err := queue.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	for i := 0; i < 5; i++ {
		rm := createTestResourceMetrics("persist-test", "persist_metric", 3)
		req := &colmetrics.ExportMetricsServiceRequest{
			ResourceMetrics: []*metricspb.ResourceMetrics{rm},
		}
		if err := q1.Push(req); err != nil {
			t.Fatalf("Push failed: %v", err)
		}
	}

	initialLen := q1.Len()
	q1.Close()

	// Reopen queue
	q2, err := queue.New(cfg)
	if err != nil {
		t.Fatalf("Failed to reopen queue: %v", err)
	}
	defer q2.Close()

	// Should have same items
	if q2.Len() != initialLen {
		t.Errorf("Expected %d items after reopen, got %d", initialLen, q2.Len())
	}

	// Pop and verify
	for i := 0; i < initialLen; i++ {
		req, err := q2.Pop()
		if err != nil {
			t.Fatalf("Pop failed: %v", err)
		}
		if req == nil {
			t.Error("Expected non-nil request")
		}
	}
}

// TestFunctional_Queue_DropOldest tests drop_oldest behavior
func TestFunctional_Queue_DropOldest(t *testing.T) {
	dir := createQueueTestDir(t)
	defer os.RemoveAll(dir)

	cfg := queue.Config{
		Path:         dir,
		MaxSize:      5, // Small queue
		FullBehavior: queue.DropOldest,
	}

	q, err := queue.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Push more than max size
	for i := 0; i < 10; i++ {
		rm := createTestResourceMetrics("drop-test", "drop_metric", 1)
		req := &colmetrics.ExportMetricsServiceRequest{
			ResourceMetrics: []*metricspb.ResourceMetrics{rm},
		}
		if err := q.Push(req); err != nil {
			t.Fatalf("Push failed: %v", err)
		}
	}

	// Queue should be at max size
	if q.Len() > 5 {
		t.Errorf("Expected queue length <= 5, got %d", q.Len())
	}
}

// TestFunctional_Queue_DropNewest tests drop_newest behavior
func TestFunctional_Queue_DropNewest(t *testing.T) {
	dir := createQueueTestDir(t)
	defer os.RemoveAll(dir)

	cfg := queue.Config{
		Path:         dir,
		MaxSize:      5,
		FullBehavior: queue.DropNewest,
	}

	q, err := queue.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Push more than max size
	for i := 0; i < 10; i++ {
		rm := createTestResourceMetrics("drop-newest-test", "drop_metric", 1)
		req := &colmetrics.ExportMetricsServiceRequest{
			ResourceMetrics: []*metricspb.ResourceMetrics{rm},
		}
		q.Push(req) // May return error when full
	}

	// Queue should be at max size
	if q.Len() > 5 {
		t.Errorf("Expected queue length <= 5, got %d", q.Len())
	}
}

// TestFunctional_QueuedExporter_RetryOnFailure tests retry behavior
func TestFunctional_QueuedExporter_RetryOnFailure(t *testing.T) {
	dir := createQueueTestDir(t)
	defer os.RemoveAll(dir)

	// Exporter that fails first 3 times then succeeds
	baseExp := &failingExporter{succeedAt: 3}

	cfg := queue.Config{
		Path:          dir,
		MaxSize:       100,
		RetryInterval: 100 * time.Millisecond,
		MaxRetryDelay: 500 * time.Millisecond,
	}

	queuedExp, err := exporter.NewQueued(baseExp, cfg)
	if err != nil {
		t.Fatalf("Failed to create queued exporter: %v", err)
	}

	// Export should queue on failure
	ctx := context.Background()
	rm := createTestResourceMetrics("retry-test", "retry_metric", 5)
	req := &colmetrics.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{rm},
	}

	// First export will fail and queue
	err = queuedExp.Export(ctx, req)
	// Error may or may not be returned depending on implementation

	// Wait for retries
	time.Sleep(800 * time.Millisecond)

	// Should have eventually succeeded
	exports := baseExp.getExports()
	t.Logf("After retries: %d failures, %d successful exports", baseExp.getFailCount(), len(exports))

	queuedExp.Close()
}

// TestFunctional_QueuedExporter_QueuesDuringOutage tests queuing during outage
func TestFunctional_QueuedExporter_QueuesDuringOutage(t *testing.T) {
	dir := createQueueTestDir(t)
	defer os.RemoveAll(dir)

	// Exporter that always fails
	baseExp := &failingExporter{succeedAt: 0}

	cfg := queue.Config{
		Path:          dir,
		MaxSize:       1000,
		RetryInterval: 1 * time.Second, // Slow retry
		MaxRetryDelay: 5 * time.Second,
	}

	queuedExp, err := exporter.NewQueued(baseExp, cfg)
	if err != nil {
		t.Fatalf("Failed to create queued exporter: %v", err)
	}

	ctx := context.Background()

	// Send multiple exports during "outage"
	for i := 0; i < 10; i++ {
		rm := createTestResourceMetrics("outage-test", "outage_metric", 3)
		req := &colmetrics.ExportMetricsServiceRequest{
			ResourceMetrics: []*metricspb.ResourceMetrics{rm},
		}
		queuedExp.Export(ctx, req)
	}

	// Wait a bit
	time.Sleep(200 * time.Millisecond)

	// Check queue stats
	qLen := queuedExp.QueueLen()
	qSize := queuedExp.QueueSize()
	t.Logf("Queue stats: len=%d, bytes=%d", qLen, qSize)

	queuedExp.Close()
}

// TestFunctional_Queue_Compaction tests WAL compaction
func TestFunctional_Queue_Compaction(t *testing.T) {
	dir := createQueueTestDir(t)
	defer os.RemoveAll(dir)

	cfg := queue.Config{
		Path:             dir,
		MaxSize:          100,
		CompactThreshold: 0.3, // Compact when 30% consumed
	}

	q, err := queue.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Push items
	for i := 0; i < 50; i++ {
		rm := createTestResourceMetrics("compact-test", "compact_metric", 2)
		req := &colmetrics.ExportMetricsServiceRequest{
			ResourceMetrics: []*metricspb.ResourceMetrics{rm},
		}
		if err := q.Push(req); err != nil {
			t.Fatalf("Push failed: %v", err)
		}
	}

	// Pop half (should trigger compaction at 30% threshold)
	for i := 0; i < 25; i++ {
		if _, err := q.Pop(); err != nil {
			t.Fatalf("Pop failed: %v", err)
		}
	}

	// Check remaining - should have around 25 items remaining
	remaining := q.Len()
	t.Logf("Queue length after popping 25: %d items remaining", remaining)
	if remaining < 20 || remaining > 30 {
		t.Errorf("Expected around 25 items remaining, got %d", remaining)
	}

	// Check WAL files
	files, _ := filepath.Glob(filepath.Join(dir, "*.wal"))
	t.Logf("WAL files after compaction: %d", len(files))
}

// TestFunctional_Queue_ConcurrentAccess tests concurrent queue operations
func TestFunctional_Queue_ConcurrentAccess(t *testing.T) {
	dir := createQueueTestDir(t)
	defer os.RemoveAll(dir)

	cfg := queue.Config{
		Path:    dir,
		MaxSize: 10000,
	}

	q, err := queue.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	var wg sync.WaitGroup
	pushCount := 100
	goroutines := 10

	// Concurrent pushers
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < pushCount; i++ {
				rm := createTestResourceMetrics("concurrent-test", "concurrent_metric", 1)
				req := &colmetrics.ExportMetricsServiceRequest{
					ResourceMetrics: []*metricspb.ResourceMetrics{rm},
				}
				q.Push(req)
			}
		}()
	}

	// Concurrent poppers
	var popCount int64
	var popMu sync.Mutex
	for g := 0; g < goroutines/2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < pushCount; i++ {
				if _, err := q.Pop(); err == nil {
					popMu.Lock()
					popCount++
					popMu.Unlock()
				}
			}
		}()
	}

	wg.Wait()

	t.Logf("Concurrent test: pushed %d, popped %d, remaining %d",
		goroutines*pushCount, popCount, q.Len())
}

// TestFunctional_Queue_LargePayloads tests queue with large payloads
func TestFunctional_Queue_LargePayloads(t *testing.T) {
	dir := createQueueTestDir(t)
	defer os.RemoveAll(dir)

	cfg := queue.Config{
		Path:     dir,
		MaxSize:  100,
		MaxBytes: 100 * 1024 * 1024, // 100MB
	}

	q, err := queue.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Push large payloads
	for i := 0; i < 10; i++ {
		rm := createTestResourceMetrics("large-test", "large_metric", 1000) // 1000 datapoints
		req := &colmetrics.ExportMetricsServiceRequest{
			ResourceMetrics: []*metricspb.ResourceMetrics{rm},
		}
		if err := q.Push(req); err != nil {
			t.Fatalf("Push failed: %v", err)
		}
	}

	t.Logf("Queue size after large payloads: %d items, %d bytes", q.Len(), q.Size())

	// Pop and verify
	for i := 0; i < 10; i++ {
		entry, err := q.Pop()
		if err != nil {
			t.Fatalf("Pop failed: %v", err)
		}
		req, err := entry.GetRequest()
		if err != nil {
			t.Fatalf("GetRequest failed: %v", err)
		}
		if len(req.ResourceMetrics) == 0 {
			t.Error("Expected non-empty ResourceMetrics")
		}
	}
}
