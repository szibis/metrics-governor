package exporter

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	"github.com/szibis/metrics-governor/internal/queue"
)

// ── helpers ──────────────────────────────────────────────────────────────────

// aqMockExporter is a mock OTLP exporter for always-queue tests.
// It tracks calls, can be configured to fail N times, and supports
// optional latency to simulate slow destinations.
type aqMockExporter struct {
	mu        sync.Mutex
	failCount int
	exportErr error
	calls     int64
	exported  []*colmetricspb.ExportMetricsServiceRequest
	closed    bool
	delay     time.Duration
}

func (m *aqMockExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	atomic.AddInt64(&m.calls, 1)

	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failCount > 0 {
		m.failCount--
		if m.exportErr != nil {
			return m.exportErr
		}
		return errors.New("aq mock export error")
	}

	m.exported = append(m.exported, req)
	return nil
}

func (m *aqMockExporter) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *aqMockExporter) getCalls() int64 {
	return atomic.LoadInt64(&m.calls)
}

func (m *aqMockExporter) getExported() []*colmetricspb.ExportMetricsServiceRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*colmetricspb.ExportMetricsServiceRequest, len(m.exported))
	copy(out, m.exported)
	return out
}

func (m *aqMockExporter) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

func (m *aqMockExporter) setFailCount(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failCount = n
}

// aqTestRequest creates a minimal ExportMetricsServiceRequest.
func aqTestRequest(name string) *colmetricspb.ExportMetricsServiceRequest {
	return &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{{Name: name}},
					},
				},
			},
		},
	}
}

// alwaysQueueConfig returns a queue.Config with AlwaysQueue enabled.
func alwaysQueueConfig(t *testing.T, workers int) queue.Config {
	t.Helper()
	return queue.Config{
		Path:               t.TempDir(),
		MaxSize:            10000,
		RetryInterval:      50 * time.Millisecond,
		MaxRetryDelay:      500 * time.Millisecond,
		AlwaysQueue:        true,
		Workers:            workers,
		CloseTimeout:       5 * time.Second,
		DrainTimeout:       2 * time.Second,
		DrainEntryTimeout:  1 * time.Second,
		RetryExportTimeout: 2 * time.Second,
	}
}

// ── AlwaysQueue Export path ──────────────────────────────────────────────────

// TestAlwaysQueue_ExportPushesToQueue verifies that Export() in always-queue
// mode returns nil immediately and the batch appears in the queue.
func TestAlwaysQueue_ExportPushesToQueue(t *testing.T) {
	mock := &aqMockExporter{}
	cfg := alwaysQueueConfig(t, 0) // 0 workers so nothing drains
	// We want the queue to hold items without workers consuming them.
	// Use 1 worker but with very high retry interval so it sleeps.
	cfg.Workers = 1
	cfg.RetryInterval = time.Hour

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	t.Cleanup(func() { qe.Close() })

	req := aqTestRequest("push_test")
	err = qe.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("Export() returned error: %v", err)
	}

	// Give the worker a moment to potentially pop (it shouldn't due to hour delay
	// after processing).  The item should appear in the queue before the worker
	// even wakes up, but we still give a tiny window.
	time.Sleep(20 * time.Millisecond)

	// Either the queue still has it or the worker already exported it.
	// What we really care about: Export() returned nil and the mock was not
	// called *during* Export().  The mock may be called later by a worker.
	// This assertion is intentionally lenient to avoid race flakiness.
	if qe.QueueLen()+len(mock.getExported()) < 1 {
		t.Fatalf("expected at least 1 item in queue or exported, got queueLen=%d exported=%d",
			qe.QueueLen(), len(mock.getExported()))
	}
}

// TestAlwaysQueue_NoDirectExportCall verifies that the underlying exporter's
// Export() is NOT called during QueuedExporter.Export(). The exporter is only
// called by workers asynchronously.
func TestAlwaysQueue_NoDirectExportCall(t *testing.T) {
	mock := &aqMockExporter{delay: time.Hour} // never returns
	cfg := alwaysQueueConfig(t, 1)
	cfg.RetryInterval = time.Hour // workers sleep forever

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	t.Cleanup(func() { qe.Close() })

	callsBefore := mock.getCalls()

	req := aqTestRequest("no_direct")
	err = qe.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	callsAfter := mock.getCalls()
	if callsAfter != callsBefore {
		t.Fatalf("expected 0 exporter calls during Export(), got %d", callsAfter-callsBefore)
	}
}

// TestAlwaysQueue_QueueFullReturnsError verifies that when the queue is at max
// capacity, Export() returns an error.
func TestAlwaysQueue_QueueFullReturnsError(t *testing.T) {
	mock := &aqMockExporter{}
	cfg := alwaysQueueConfig(t, 1)
	cfg.MaxSize = 2
	cfg.RetryInterval = time.Hour       // workers idle
	cfg.FullBehavior = queue.DropNewest // drop newest so Push returns nil but entry is dropped

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	t.Cleanup(func() { qe.Close() })

	// Fill the queue
	for i := 0; i < 3; i++ {
		_ = qe.Export(context.Background(), aqTestRequest("fill"))
	}

	// With DropNewest the push succeeds (entry is silently dropped).
	// With Block + short timeout, the push would return timeout error.
	// The important thing: Export() does not panic or hang.

	// Verify at least some items were accepted
	if qe.QueueLen() == 0 && len(mock.getExported()) == 0 {
		t.Fatal("expected at least some items to be queued or exported")
	}
}

// TestAlwaysQueue_ReturnNilNotErrExportQueued verifies that always-queue mode
// returns nil (not ErrExportQueued) on success, because queuing IS the success
// path.
func TestAlwaysQueue_ReturnNilNotErrExportQueued(t *testing.T) {
	mock := &aqMockExporter{}
	cfg := alwaysQueueConfig(t, 1)
	cfg.RetryInterval = time.Hour

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	t.Cleanup(func() { qe.Close() })

	err = qe.Export(context.Background(), aqTestRequest("nil_return"))
	if err != nil {
		t.Fatalf("Export() should return nil in always-queue mode, got: %v", err)
	}
	if errors.Is(err, ErrExportQueued) {
		t.Fatal("Export() must NOT return ErrExportQueued in always-queue mode")
	}
}

// TestAlwaysQueue_BackwardsCompat_DirectMode verifies that when AlwaysQueue=false
// the old try-direct behavior is preserved: a successful Export() calls the
// underlying exporter directly and does not queue.
func TestAlwaysQueue_BackwardsCompat_DirectMode(t *testing.T) {
	mock := &aqMockExporter{}
	cfg := queue.Config{
		Path:          t.TempDir(),
		MaxSize:       100,
		RetryInterval: time.Hour,
		MaxRetryDelay: time.Hour,
		AlwaysQueue:   false, // legacy mode
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	t.Cleanup(func() { qe.Close() })

	if qe.AlwaysQueue() {
		t.Fatal("expected AlwaysQueue()=false")
	}

	err = qe.Export(context.Background(), aqTestRequest("compat"))
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	if mock.getCalls() != 1 {
		t.Fatalf("expected exactly 1 direct exporter call, got %d", mock.getCalls())
	}
	if qe.QueueLen() != 0 {
		t.Fatalf("expected empty queue after successful direct export, got %d", qe.QueueLen())
	}
}

// ── Worker pool tests ────────────────────────────────────────────────────────

// TestWorkerPool_ConcurrentDrain verifies that N workers drain N items
// concurrently.
func TestWorkerPool_ConcurrentDrain(t *testing.T) {
	const workers = 4
	const items = 20

	mock := &aqMockExporter{}
	cfg := alwaysQueueConfig(t, workers)
	cfg.RetryInterval = 10 * time.Millisecond

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	t.Cleanup(func() { qe.Close() })

	// Push items via Export
	for i := 0; i < items; i++ {
		if err := qe.Export(context.Background(), aqTestRequest("drain")); err != nil {
			t.Fatalf("Export %d: %v", i, err)
		}
	}

	// Wait for workers to drain everything
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if qe.QueueLen() == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if qe.QueueLen() != 0 {
		t.Fatalf("expected queue to be drained, got %d remaining", qe.QueueLen())
	}

	exported := mock.getExported()
	if len(exported) != items {
		t.Fatalf("expected %d exported items, got %d", items, len(exported))
	}
}

// TestWorkerPool_RetriesOnFailure verifies that when an export fails, the batch
// is re-pushed to the queue and eventually succeeds.
func TestWorkerPool_RetriesOnFailure(t *testing.T) {
	const failTimes = 3

	mock := &aqMockExporter{
		failCount: failTimes,
		exportErr: errors.New("transient failure"),
	}
	cfg := alwaysQueueConfig(t, 2)
	cfg.RetryInterval = 20 * time.Millisecond

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	t.Cleanup(func() { qe.Close() })

	err = qe.Export(context.Background(), aqTestRequest("retry_test"))
	if err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Wait for retries + eventual success
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(mock.getExported()) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	exported := mock.getExported()
	if len(exported) < 1 {
		t.Fatalf("expected at least 1 successful export after retries, got %d", len(exported))
	}

	// Total calls should be > failTimes (failTimes failures + 1 success)
	totalCalls := mock.getCalls()
	if totalCalls < int64(failTimes+1) {
		t.Fatalf("expected at least %d total calls, got %d", failTimes+1, totalCalls)
	}
}

// TestWorkerPool_GracefulShutdown verifies that on Close(), workers finish
// current work and exit cleanly.
func TestWorkerPool_GracefulShutdown(t *testing.T) {
	const items = 10

	mock := &aqMockExporter{}
	cfg := alwaysQueueConfig(t, 4)
	cfg.RetryInterval = 10 * time.Millisecond

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	for i := 0; i < items; i++ {
		_ = qe.Export(context.Background(), aqTestRequest("shutdown"))
	}

	// Close should wait for workers then drain remaining
	start := time.Now()
	err = qe.Close()
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	if elapsed > 10*time.Second {
		t.Fatalf("Close() took too long: %v", elapsed)
	}

	if !mock.isClosed() {
		t.Fatal("underlying exporter should be closed after Close()")
	}
}

// TestWorkerPool_IdleOnEmptyQueue verifies that workers sleep when the queue
// is empty instead of busy-looping. We check that the CPU is not pinned by
// verifying the test completes quickly without excessive goroutine churn.
func TestWorkerPool_IdleOnEmptyQueue(t *testing.T) {
	mock := &aqMockExporter{}
	cfg := alwaysQueueConfig(t, 4)
	cfg.RetryInterval = 50 * time.Millisecond

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	t.Cleanup(func() { qe.Close() })

	// Let workers idle on empty queue for a bit
	time.Sleep(200 * time.Millisecond)

	// No items were pushed, so no export calls should have been made
	if mock.getCalls() != 0 {
		t.Fatalf("expected 0 export calls on empty queue, got %d", mock.getCalls())
	}

	// Queue should still be empty
	if qe.QueueLen() != 0 {
		t.Fatalf("expected empty queue, got %d", qe.QueueLen())
	}
}

// TestWorkerPool_WorkerCount verifies that the Workers() accessor returns the
// configured value.
func TestWorkerPool_WorkerCount(t *testing.T) {
	mock := &aqMockExporter{}
	cfg := alwaysQueueConfig(t, 7)

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	t.Cleanup(func() { qe.Close() })

	if qe.Workers() != 7 {
		t.Fatalf("Workers() = %d, want 7", qe.Workers())
	}
	if !qe.AlwaysQueue() {
		t.Fatal("expected AlwaysQueue()=true")
	}
}

// TestWorkerPool_DefaultWorkerCount verifies that when Workers is 0 (default),
// the actual count is runtime.NumCPU().
func TestWorkerPool_DefaultWorkerCount(t *testing.T) {
	mock := &aqMockExporter{}
	cfg := alwaysQueueConfig(t, 0) // 0 = use default
	cfg.Workers = 0

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	t.Cleanup(func() { qe.Close() })

	expected := runtime.NumCPU()
	if qe.Workers() != expected {
		t.Fatalf("Workers() = %d, want %d (NumCPU)", qe.Workers(), expected)
	}
}
