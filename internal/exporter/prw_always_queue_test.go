package exporter

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/prw"
)

// ── PRW helpers ──────────────────────────────────────────────────────────────

// aqPRWMockExporter is a mock PRW exporter for always-queue tests.
type aqPRWMockExporter struct {
	mu        sync.Mutex
	failCount int
	failErr   error
	calls     int64
	exported  []*prw.WriteRequest
	closed    bool
	delay     time.Duration
}

func (m *aqPRWMockExporter) Export(ctx context.Context, req *prw.WriteRequest) error {
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
		if m.failErr != nil {
			return m.failErr
		}
		return errors.New("aq prw mock export error")
	}

	m.exported = append(m.exported, req)
	return nil
}

func (m *aqPRWMockExporter) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *aqPRWMockExporter) getCalls() int64 {
	return atomic.LoadInt64(&m.calls)
}

func (m *aqPRWMockExporter) getExported() []*prw.WriteRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*prw.WriteRequest, len(m.exported))
	copy(out, m.exported)
	return out
}

func (m *aqPRWMockExporter) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

func (m *aqPRWMockExporter) setFailCount(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failCount = n
}

// aqPRWRequest builds a PRW WriteRequest with n timeseries.
func aqPRWRequest(n int) *prw.WriteRequest {
	ts := make([]prw.TimeSeries, n)
	for i := 0; i < n; i++ {
		ts[i] = prw.TimeSeries{
			Labels:  []prw.Label{{Name: "__name__", Value: fmt.Sprintf("aq_metric_%d", i)}},
			Samples: []prw.Sample{{Value: float64(i), Timestamp: int64(i + 1)}},
		}
	}
	return &prw.WriteRequest{Timeseries: ts}
}

// aqPRWQueueConfig returns a PRWQueueConfig with AlwaysQueue enabled.
func aqPRWQueueConfig(t *testing.T, workers int) PRWQueueConfig {
	t.Helper()
	return PRWQueueConfig{
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

// ── PRW AlwaysQueue Export path ──────────────────────────────────────────────

// TestPRWAlwaysQueue_ExportPushesToQueue verifies that in always-queue mode,
// Export() returns nil instantly and data lands in the queue or gets exported
// by workers.
func TestPRWAlwaysQueue_ExportPushesToQueue(t *testing.T) {
	mock := &aqPRWMockExporter{}
	cfg := aqPRWQueueConfig(t, 1)
	cfg.RetryInterval = time.Hour // workers idle, won't consume

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	t.Cleanup(func() { qe.Close() })

	req := aqPRWRequest(3)
	err = qe.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("Export() returned error: %v", err)
	}

	// The export should return nil (queuing is the success path).
	if errors.Is(err, ErrExportQueued) {
		t.Fatal("Export() should NOT return ErrExportQueued in always-queue mode")
	}

	// Wait briefly so the worker has a chance to pop (it should not export
	// anything because its base delay is 1 hour, so workerSleep blocks it).
	time.Sleep(20 * time.Millisecond)

	// Item should be in queue or already exported by a worker
	if qe.QueueSize()+len(mock.getExported()) < 1 {
		t.Fatalf("expected at least 1 item queued or exported, got queue=%d exported=%d",
			qe.QueueSize(), len(mock.getExported()))
	}
}

// TestPRWAlwaysQueue_NilRequest verifies that nil/empty requests are handled
// correctly in always-queue mode.
func TestPRWAlwaysQueue_NilRequest(t *testing.T) {
	mock := &aqPRWMockExporter{}
	cfg := aqPRWQueueConfig(t, 1)

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	t.Cleanup(func() { qe.Close() })

	// nil request
	if err := qe.Export(context.Background(), nil); err != nil {
		t.Fatalf("Export(nil) error: %v", err)
	}

	// empty timeseries
	if err := qe.Export(context.Background(), &prw.WriteRequest{}); err != nil {
		t.Fatalf("Export(empty) error: %v", err)
	}

	if mock.getCalls() != 0 {
		t.Fatalf("expected 0 exporter calls for nil/empty requests, got %d", mock.getCalls())
	}
}

// TestPRWAlwaysQueue_ReturnNilNotErrExportQueued ensures always-queue mode
// returns nil, not ErrExportQueued.
func TestPRWAlwaysQueue_ReturnNilNotErrExportQueued(t *testing.T) {
	mock := &aqPRWMockExporter{}
	cfg := aqPRWQueueConfig(t, 1)
	cfg.RetryInterval = time.Hour

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	t.Cleanup(func() { qe.Close() })

	err = qe.Export(context.Background(), aqPRWRequest(1))
	if err != nil {
		t.Fatalf("Export() should return nil, got: %v", err)
	}
}

// TestPRWAlwaysQueue_BackwardsCompat verifies that with AlwaysQueue=false the
// old try-direct behavior is preserved.
func TestPRWAlwaysQueue_BackwardsCompat(t *testing.T) {
	mock := &aqPRWMockExporter{}
	cfg := PRWQueueConfig{
		Path:          t.TempDir(),
		MaxSize:       100,
		RetryInterval: time.Hour,
		MaxRetryDelay: time.Hour,
		AlwaysQueue:   false, // legacy mode
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	t.Cleanup(func() { qe.Close() })

	err = qe.Export(context.Background(), aqPRWRequest(1))
	if err != nil {
		t.Fatalf("Export() error: %v", err)
	}

	if mock.getCalls() != 1 {
		t.Fatalf("expected 1 direct exporter call in legacy mode, got %d", mock.getCalls())
	}
	if qe.QueueSize() != 0 {
		t.Fatalf("expected empty queue after direct export, got %d", qe.QueueSize())
	}
}

// TestPRWAlwaysQueue_DefaultWorkerCount verifies that Workers defaults to
// 2 * runtime.NumCPU when set to 0.
func TestPRWAlwaysQueue_DefaultWorkerCount(t *testing.T) {
	mock := &aqPRWMockExporter{}
	cfg := aqPRWQueueConfig(t, 0)
	cfg.Workers = 0

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	t.Cleanup(func() { qe.Close() })

	expected := 2 * runtime.NumCPU()
	if qe.workers != expected {
		t.Fatalf("workers = %d, want %d (2 * NumCPU)", qe.workers, expected)
	}
}

// ── PRW Worker pool tests ────────────────────────────────────────────────────

// TestPRWWorkerPool_ConcurrentDrain verifies that N workers drain N items
// concurrently through the PRW pipeline.
func TestPRWWorkerPool_ConcurrentDrain(t *testing.T) {
	const workers = 4
	const items = 20

	mock := &aqPRWMockExporter{}
	cfg := aqPRWQueueConfig(t, workers)
	cfg.RetryInterval = 10 * time.Millisecond

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	t.Cleanup(func() { qe.Close() })

	for i := 0; i < items; i++ {
		if err := qe.Export(context.Background(), aqPRWRequest(1)); err != nil {
			t.Fatalf("Export %d: %v", i, err)
		}
	}

	// Wait for workers to drain
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if qe.QueueSize() == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if qe.QueueSize() != 0 {
		t.Fatalf("expected queue drained, got %d remaining", qe.QueueSize())
	}

	exported := mock.getExported()
	if len(exported) != items {
		t.Fatalf("expected %d exported items, got %d", items, len(exported))
	}
}

// TestPRWWorkerPool_RetriesOnFailure verifies that failed exports are re-pushed
// and eventually succeed.
func TestPRWWorkerPool_RetriesOnFailure(t *testing.T) {
	const failTimes = 3

	mock := &aqPRWMockExporter{
		failCount: failTimes,
		failErr:   errors.New("transient PRW failure"),
	}
	cfg := aqPRWQueueConfig(t, 2)
	cfg.RetryInterval = 20 * time.Millisecond

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	t.Cleanup(func() { qe.Close() })

	err = qe.Export(context.Background(), aqPRWRequest(1))
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

	totalCalls := mock.getCalls()
	if totalCalls < int64(failTimes+1) {
		t.Fatalf("expected at least %d total calls, got %d", failTimes+1, totalCalls)
	}
}

// TestPRWWorkerPool_GracefulShutdown verifies that Close() waits for workers
// to finish and then exits cleanly.
func TestPRWWorkerPool_GracefulShutdown(t *testing.T) {
	const items = 10

	mock := &aqPRWMockExporter{}
	cfg := aqPRWQueueConfig(t, 4)
	cfg.RetryInterval = 10 * time.Millisecond

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}

	for i := 0; i < items; i++ {
		_ = qe.Export(context.Background(), aqPRWRequest(1))
	}

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
		t.Fatal("underlying exporter should be closed")
	}
}

// TestPRWWorkerPool_IdleOnEmptyQueue verifies workers don't busy-loop when the
// queue is empty.
func TestPRWWorkerPool_IdleOnEmptyQueue(t *testing.T) {
	mock := &aqPRWMockExporter{}
	cfg := aqPRWQueueConfig(t, 4)
	cfg.RetryInterval = 50 * time.Millisecond

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	t.Cleanup(func() { qe.Close() })

	// Let workers idle for a bit
	time.Sleep(200 * time.Millisecond)

	if mock.getCalls() != 0 {
		t.Fatalf("expected 0 export calls on empty queue, got %d", mock.getCalls())
	}
}

// TestPRWWorkerPool_ConcurrentExportAndDrain verifies that concurrent
// Export() calls and worker draining are race-free.
func TestPRWWorkerPool_ConcurrentExportAndDrain(t *testing.T) {
	mock := &aqPRWMockExporter{}
	cfg := aqPRWQueueConfig(t, 4)
	cfg.RetryInterval = 10 * time.Millisecond

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	t.Cleanup(func() { qe.Close() })

	const goroutines = 8
	const perGoroutine = 10

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				_ = qe.Export(context.Background(), aqPRWRequest(1))
			}
		}(g)
	}

	wg.Wait()

	// Wait for all items to be drained
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if qe.QueueSize() == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	total := goroutines * perGoroutine
	exported := mock.getExported()
	if len(exported) != total {
		t.Fatalf("expected %d exported items, got %d (queue remaining: %d)",
			total, len(exported), qe.QueueSize())
	}
}
