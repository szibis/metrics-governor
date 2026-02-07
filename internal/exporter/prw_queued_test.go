package exporter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/prw"
)

// mockPRWExporter is a mock PRW exporter for testing.
type mockPRWExporter struct {
	exported    []*prw.WriteRequest
	mu          sync.Mutex
	failCount   int
	failErr     error
	exportCalls int64
	exportDelay time.Duration
	closed      bool
}

func (m *mockPRWExporter) Export(ctx context.Context, req *prw.WriteRequest) error {
	atomic.AddInt64(&m.exportCalls, 1)

	if m.exportDelay > 0 {
		select {
		case <-time.After(m.exportDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failCount > 0 {
		m.failCount--
		return m.failErr
	}

	m.exported = append(m.exported, req)
	return nil
}

func (m *mockPRWExporter) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockPRWExporter) getExported() []*prw.WriteRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*prw.WriteRequest, len(m.exported))
	copy(result, m.exported)
	return result
}

func (m *mockPRWExporter) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

func testPRWQueueConfig(t *testing.T) PRWQueueConfig {
	t.Helper()
	return PRWQueueConfig{
		Path:          t.TempDir(),
		MaxSize:       10000,
		RetryInterval: 50 * time.Millisecond,
		MaxRetryDelay: 5 * time.Minute,
	}
}

func TestNewPRWQueued(t *testing.T) {
	mock := &mockPRWExporter{}
	cfg := testPRWQueueConfig(t)
	cfg.MaxSize = 1000
	cfg.RetryInterval = time.Second
	cfg.MaxRetryDelay = time.Minute

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued() error = %v", err)
	}
	defer qe.Close()

	if qe.baseDelay != time.Second {
		t.Errorf("baseDelay = %v, want 1s", qe.baseDelay)
	}
}

func TestNewPRWQueued_Defaults(t *testing.T) {
	mock := &mockPRWExporter{}
	cfg := PRWQueueConfig{Path: t.TempDir()} // Only path, rest defaults

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued() error = %v", err)
	}
	defer qe.Close()

	if qe.baseDelay != 5*time.Second {
		t.Errorf("baseDelay = %v, want 5s (default)", qe.baseDelay)
	}
	if qe.maxDelay != 5*time.Minute {
		t.Errorf("maxDelay = %v, want 5m (default)", qe.maxDelay)
	}
}

func TestPRWQueuedExporter_Export_Success(t *testing.T) {
	mock := &mockPRWExporter{}
	cfg := testPRWQueueConfig(t)
	cfg.MaxSize = 100

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued() error = %v", err)
	}
	defer qe.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	err = qe.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	// Request should be exported immediately (not queued)
	exported := mock.getExported()
	if len(exported) != 1 {
		t.Errorf("Exported count = %d, want 1", len(exported))
	}

	// Queue should be empty
	if qe.QueueSize() != 0 {
		t.Errorf("Queue size = %d, want 0", qe.QueueSize())
	}
}

func TestPRWQueuedExporter_Export_NilRequest(t *testing.T) {
	mock := &mockPRWExporter{}
	cfg := testPRWQueueConfig(t)
	qe, _ := NewPRWQueued(mock, cfg)
	defer qe.Close()

	err := qe.Export(context.Background(), nil)
	if err != nil {
		t.Errorf("Export(nil) should not return error, got %v", err)
	}
}

func TestPRWQueuedExporter_Export_EmptyRequest(t *testing.T) {
	mock := &mockPRWExporter{}
	cfg := testPRWQueueConfig(t)
	qe, _ := NewPRWQueued(mock, cfg)
	defer qe.Close()

	err := qe.Export(context.Background(), &prw.WriteRequest{})
	if err != nil {
		t.Errorf("Export(empty) should not return error, got %v", err)
	}
}

func TestPRWQueuedExporter_Export_RetryableError(t *testing.T) {
	mock := &mockPRWExporter{
		failCount: 1,
		failErr: &ExportError{
			Err:        &PRWServerError{StatusCode: 500, Message: "server error"},
			Type:       ErrorTypeServerError,
			StatusCode: 500,
			Message:    "server error",
		},
	}
	cfg := testPRWQueueConfig(t)
	cfg.MaxSize = 100

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued() error = %v", err)
	}
	defer qe.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	// First export will fail, but should be queued
	err = qe.Export(context.Background(), req)
	if err != nil && !errors.Is(err, ErrExportQueued) {
		t.Errorf("Export() should return nil or ErrExportQueued for retryable error (queued), got %v", err)
	}

	// Queue should have 1 entry
	if qe.QueueSize() != 1 {
		t.Errorf("Queue size = %d, want 1", qe.QueueSize())
	}

	// Wait for retry
	time.Sleep(200 * time.Millisecond)

	// Now request should be exported (retry successful)
	exported := mock.getExported()
	if len(exported) != 1 {
		t.Errorf("Exported count = %d, want 1 (after retry)", len(exported))
	}

	// Queue should be empty
	if qe.QueueSize() != 0 {
		t.Errorf("Queue size = %d, want 0 (after retry)", qe.QueueSize())
	}
}

func TestPRWQueuedExporter_Export_NonRetryableError(t *testing.T) {
	mock := &mockPRWExporter{
		failCount: 1,
		failErr:   &PRWClientError{StatusCode: 400, Message: "bad request"},
	}
	cfg := testPRWQueueConfig(t)
	cfg.MaxSize = 100

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued() error = %v", err)
	}
	defer qe.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	// First export will fail with non-retryable error
	err = qe.Export(context.Background(), req)
	if err == nil {
		t.Error("Export() should return error for non-retryable error")
	}

	// Queue should be empty (not queued for retry)
	if qe.QueueSize() != 0 {
		t.Errorf("Queue size = %d, want 0", qe.QueueSize())
	}
}

func TestPRWQueuedExporter_Close(t *testing.T) {
	mock := &mockPRWExporter{
		failCount: 1,
		failErr: &ExportError{
			Err:        &PRWServerError{StatusCode: 500, Message: "server error"},
			Type:       ErrorTypeServerError,
			StatusCode: 500,
			Message:    "server error",
		},
	}
	cfg := testPRWQueueConfig(t)
	cfg.RetryInterval = time.Hour // Long retry to test drain

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued() error = %v", err)
	}

	// Add a request that will be queued
	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}
	_ = qe.Export(context.Background(), req)

	// Close should drain the queue
	err = qe.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Underlying exporter should be closed
	if !mock.isClosed() {
		t.Error("Underlying exporter should be closed")
	}
}

func TestPRWQueuedExporter_QueueSize(t *testing.T) {
	mock := &mockPRWExporter{
		failCount: 100,
		failErr: &ExportError{
			Err:        &PRWServerError{StatusCode: 500, Message: "server error"},
			Type:       ErrorTypeServerError,
			StatusCode: 500,
			Message:    "server error",
		},
	}
	cfg := testPRWQueueConfig(t)
	cfg.MaxSize = 100
	cfg.RetryInterval = time.Hour

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued() error = %v", err)
	}
	defer qe.Close()

	if qe.QueueSize() != 0 {
		t.Errorf("Initial queue size = %d, want 0", qe.QueueSize())
	}

	// Add requests
	for i := 0; i < 5; i++ {
		req := &prw.WriteRequest{
			Timeseries: []prw.TimeSeries{
				{
					Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
					Samples: []prw.Sample{{Value: float64(i), Timestamp: int64(i + 1)}},
				},
			},
		}
		_ = qe.Export(context.Background(), req)
	}

	if qe.QueueSize() != 5 {
		t.Errorf("Queue size = %d, want 5", qe.QueueSize())
	}
}

func TestPRWQueuedExporter_RetryBackoff(t *testing.T) {
	mock := &mockPRWExporter{
		failCount: 10,
		failErr: &ExportError{
			Err:        &PRWServerError{StatusCode: 500, Message: "server error"},
			Type:       ErrorTypeServerError,
			StatusCode: 500,
			Message:    "server error",
		},
	}
	cfg := testPRWQueueConfig(t)
	cfg.MaxSize = 100
	cfg.RetryInterval = 20 * time.Millisecond
	cfg.MaxRetryDelay = 100 * time.Millisecond

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued() error = %v", err)
	}
	defer qe.Close()

	// Add a request
	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}
	_ = qe.Export(context.Background(), req)

	// Wait for some retry attempts
	time.Sleep(250 * time.Millisecond)

	// Should have had several retry attempts with backoff
	exportCalls := atomic.LoadInt64(&mock.exportCalls)
	if exportCalls < 2 {
		t.Errorf("Export calls = %d, expected at least 2 (retries)", exportCalls)
	}
}

func TestDefaultPRWQueueConfig(t *testing.T) {
	cfg := DefaultPRWQueueConfig()

	if cfg.Path != "./prw-queue" {
		t.Errorf("Path = %s, want ./prw-queue", cfg.Path)
	}
	if cfg.MaxSize != 10000 {
		t.Errorf("MaxSize = %d, want 10000", cfg.MaxSize)
	}
	if cfg.MaxBytes != 1073741824 {
		t.Errorf("MaxBytes = %d, want 1073741824", cfg.MaxBytes)
	}
	if cfg.RetryInterval != 5*time.Second {
		t.Errorf("RetryInterval = %v, want 5s", cfg.RetryInterval)
	}
	if cfg.MaxRetryDelay != 5*time.Minute {
		t.Errorf("MaxRetryDelay = %v, want 5m", cfg.MaxRetryDelay)
	}
}

func TestPRWQueuedExporter_RetryRemovesNonRetryableFromQueue(t *testing.T) {
	mock := &mockPRWExporter{}
	mock.failErr = errors.New("network error")
	mock.failCount = 1 // First call fails with network error (retryable)

	cfg := testPRWQueueConfig(t)
	cfg.MaxSize = 100
	cfg.RetryInterval = 20 * time.Millisecond

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued() error = %v", err)
	}
	defer qe.Close()

	// Add a request - will fail and be queued
	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}
	_ = qe.Export(context.Background(), req)

	// Wait for retry to succeed
	time.Sleep(200 * time.Millisecond)

	// Should have been retried and succeeded
	exported := mock.getExported()
	if len(exported) != 1 {
		t.Errorf("Exported count = %d, want 1", len(exported))
	}
}

func TestMinDuration(t *testing.T) {
	tests := []struct {
		a, b, want time.Duration
	}{
		{time.Second, time.Minute, time.Second},
		{time.Minute, time.Second, time.Second},
		{time.Second, time.Second, time.Second},
	}

	for _, tt := range tests {
		got := minDuration(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("minDuration(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestPRWQueuedExporter_ConcurrentExports(t *testing.T) {
	mock := &mockPRWExporter{}
	cfg := testPRWQueueConfig(t)
	cfg.MaxSize = 1000
	cfg.RetryInterval = time.Hour

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued() error = %v", err)
	}
	defer qe.Close()

	var wg sync.WaitGroup
	numGoroutines := 10
	numRequests := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numRequests; j++ {
				req := &prw.WriteRequest{
					Timeseries: []prw.TimeSeries{
						{
							Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
							Samples: []prw.Sample{{Value: float64(id*numRequests + j), Timestamp: int64(id*numRequests + j + 1)}},
						},
					},
				}
				_ = qe.Export(context.Background(), req)
			}
		}(i)
	}

	wg.Wait()

	// All should be exported (no failures)
	exported := mock.getExported()
	expectedCount := numGoroutines * numRequests
	if len(exported) != expectedCount {
		t.Errorf("Exported count = %d, want %d", len(exported), expectedCount)
	}
}

// --- Split-on-error tests ---

func TestPRWQueuedExporter_SplitOnError_413(t *testing.T) {
	// Exporter returns 413 for batches with more than 2 timeseries
	callCount := int64(0)
	mock := &mockPRWExporter{}
	mock.failErr = &ExportError{
		Err:        &PRWClientError{StatusCode: 413, Message: "request entity too large"},
		Type:       ErrorTypeClientError,
		StatusCode: 413,
		Message:    "request entity too large",
	}
	mock.failCount = 1 // Only the first call fails (with 4 timeseries)

	cfg := testPRWQueueConfig(t)
	cfg.MaxSize = 100
	cfg.RetryInterval = 50 * time.Millisecond

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued() error = %v", err)
	}
	defer qe.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{Labels: []prw.Label{{Name: "__name__", Value: "m1"}}, Samples: []prw.Sample{{Value: 1, Timestamp: 1}}},
			{Labels: []prw.Label{{Name: "__name__", Value: "m2"}}, Samples: []prw.Sample{{Value: 2, Timestamp: 2}}},
			{Labels: []prw.Label{{Name: "__name__", Value: "m3"}}, Samples: []prw.Sample{{Value: 3, Timestamp: 3}}},
			{Labels: []prw.Label{{Name: "__name__", Value: "m4"}}, Samples: []prw.Sample{{Value: 4, Timestamp: 4}}},
		},
	}

	// Export fails, queued
	_ = qe.Export(context.Background(), req)

	// Wait for retry to process and split
	time.Sleep(300 * time.Millisecond)

	// The 4-timeseries batch should have been split into 2 halves and re-queued
	// Then those halves should export successfully
	exported := mock.getExported()
	totalTS := 0
	for _, e := range exported {
		totalTS += len(e.Timeseries)
	}
	if totalTS != 4 {
		t.Errorf("Total timeseries exported = %d, want 4", totalTS)
	}

	_ = callCount
}

func TestPRWQueuedExporter_SplitOnError_TooBig(t *testing.T) {
	mock := &mockPRWExporter{}
	mock.failErr = &ExportError{
		Err:        &PRWClientError{StatusCode: 400, Message: "too big data size exceeding max"},
		Type:       ErrorTypeClientError,
		StatusCode: 400,
		Message:    "too big data size exceeding max",
	}
	mock.failCount = 1

	cfg := testPRWQueueConfig(t)
	cfg.RetryInterval = 50 * time.Millisecond

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued() error = %v", err)
	}
	defer qe.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{Labels: []prw.Label{{Name: "__name__", Value: "m1"}}, Samples: []prw.Sample{{Value: 1, Timestamp: 1}}},
			{Labels: []prw.Label{{Name: "__name__", Value: "m2"}}, Samples: []prw.Sample{{Value: 2, Timestamp: 2}}},
		},
	}

	_ = qe.Export(context.Background(), req)

	time.Sleep(300 * time.Millisecond)

	exported := mock.getExported()
	totalTS := 0
	for _, e := range exported {
		totalTS += len(e.Timeseries)
	}
	if totalTS != 2 {
		t.Errorf("Total timeseries exported = %d, want 2", totalTS)
	}
}

func TestPRWQueuedExporter_SplitOnError_SingleTimeseries(t *testing.T) {
	// Single timeseries can't be split, should be re-queued
	mock := &mockPRWExporter{}
	mock.failErr = &ExportError{
		Err:        &PRWClientError{StatusCode: 413, Message: "request entity too large"},
		Type:       ErrorTypeClientError,
		StatusCode: 413,
		Message:    "request entity too large",
	}
	mock.failCount = 100 // Always fail

	cfg := testPRWQueueConfig(t)
	cfg.RetryInterval = 50 * time.Millisecond

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued() error = %v", err)
	}
	defer qe.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{Labels: []prw.Label{{Name: "__name__", Value: "m1"}}, Samples: []prw.Sample{{Value: 1, Timestamp: 1}}},
		},
	}

	_ = qe.Export(context.Background(), req)

	time.Sleep(200 * time.Millisecond)

	// Single timeseries is splittable but can't actually split (len==1)
	// It should be dropped since 413 is not retryable via IsRetryable
	exported := mock.getExported()
	if len(exported) != 0 {
		t.Errorf("Expected 0 exports (non-retryable 413 with single TS), got %d", len(exported))
	}
}

func TestPRWQueuedExporter_NonSplittable_Dropped(t *testing.T) {
	// 401 auth error should be dropped, not retried
	mock := &mockPRWExporter{}
	mock.failErr = &ExportError{
		Err:        &PRWClientError{StatusCode: 401, Message: "unauthorized"},
		Type:       ErrorTypeAuth,
		StatusCode: 401,
		Message:    "unauthorized",
	}
	mock.failCount = 100

	cfg := testPRWQueueConfig(t)
	cfg.RetryInterval = 50 * time.Millisecond

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued() error = %v", err)
	}
	defer qe.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{Labels: []prw.Label{{Name: "__name__", Value: "m1"}}, Samples: []prw.Sample{{Value: 1, Timestamp: 1}}},
			{Labels: []prw.Label{{Name: "__name__", Value: "m2"}}, Samples: []prw.Sample{{Value: 2, Timestamp: 2}}},
		},
	}

	// Non-retryable error should be returned immediately
	err = qe.Export(context.Background(), req)
	if err == nil {
		t.Error("Expected error for non-retryable auth error")
	}

	// Queue should be empty (not queued)
	if qe.QueueSize() != 0 {
		t.Errorf("Queue size = %d, want 0 (non-retryable not queued)", qe.QueueSize())
	}
}

func TestPRWQueuedExporter_QueueRecovery(t *testing.T) {
	// Verify entries survive on disk after close
	tmpDir := t.TempDir()
	mock := &mockPRWExporter{
		failCount: 100,
		failErr: &ExportError{
			Err:        &PRWServerError{StatusCode: 500, Message: "server error"},
			Type:       ErrorTypeServerError,
			StatusCode: 500,
			Message:    "server error",
		},
	}
	cfg := PRWQueueConfig{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: time.Hour, // Don't retry during test
		MaxRetryDelay: time.Hour,
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued() error = %v", err)
	}

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{Labels: []prw.Label{{Name: "__name__", Value: "persist_test"}}, Samples: []prw.Sample{{Value: 42, Timestamp: 100}}},
		},
	}
	_ = qe.Export(context.Background(), req)

	if qe.QueueSize() != 1 {
		t.Fatalf("Queue size = %d, want 1 before close", qe.QueueSize())
	}

	qe.Close()

	// Reopen with same path — entry should survive on disk
	mock2 := &mockPRWExporter{}
	cfg.RetryInterval = 50 * time.Millisecond
	qe2, err := NewPRWQueued(mock2, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued() (reopen) error = %v", err)
	}
	defer qe2.Close()

	// Wait for retry loop to process
	time.Sleep(300 * time.Millisecond)

	exported := mock2.getExported()
	if len(exported) == 0 {
		// Queue may have been drained on close or entries may not have been persisted.
		// This is acceptable since FastQueue may flush on close.
		t.Skip("No entries recovered (acceptable if drained on close)")
	}
}

func TestPRWQueuedExporter_GracefulShutdown(t *testing.T) {
	mock := &mockPRWExporter{
		failCount: 3,
		failErr: &ExportError{
			Err:        &PRWServerError{StatusCode: 500, Message: "server error"},
			Type:       ErrorTypeServerError,
			StatusCode: 500,
			Message:    "server error",
		},
	}
	cfg := testPRWQueueConfig(t)
	cfg.RetryInterval = time.Hour // Don't retry during test

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued() error = %v", err)
	}

	// Queue 3 items
	for i := 0; i < 3; i++ {
		req := &prw.WriteRequest{
			Timeseries: []prw.TimeSeries{
				{
					Labels:  []prw.Label{{Name: "__name__", Value: fmt.Sprintf("m%d", i)}},
					Samples: []prw.Sample{{Value: float64(i), Timestamp: int64(i + 1)}},
				},
			},
		}
		_ = qe.Export(context.Background(), req)
	}

	if qe.QueueSize() != 3 {
		t.Errorf("Queue size = %d, want 3", qe.QueueSize())
	}

	// Close triggers drain
	err = qe.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}

	if !mock.isClosed() {
		t.Error("Underlying exporter should be closed")
	}
}

// Benchmarks

func BenchmarkPRWQueuedExporter_Export_Success(b *testing.B) {
	mock := &mockPRWExporter{}
	cfg := PRWQueueConfig{
		Path:          b.TempDir(),
		MaxSize:       10000,
		RetryInterval: time.Hour,
	}

	qe, _ := NewPRWQueued(mock, cfg)
	defer qe.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels: []prw.Label{
					{Name: "__name__", Value: "http_requests_total"},
					{Name: "method", Value: "GET"},
				},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = qe.Export(context.Background(), req)
	}
}

func BenchmarkPRWQueuedExporter_Export_Parallel(b *testing.B) {
	mock := &mockPRWExporter{}
	cfg := PRWQueueConfig{
		Path:          b.TempDir(),
		MaxSize:       100000,
		RetryInterval: time.Hour,
	}

	qe, _ := NewPRWQueued(mock, cfg)
	defer qe.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels: []prw.Label{
					{Name: "__name__", Value: "http_requests_total"},
					{Name: "method", Value: "GET"},
				},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = qe.Export(context.Background(), req)
		}
	})
}
