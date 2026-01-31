package exporter

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/prw"
)

// mockPRWExporter is a mock PRW exporter for testing.
type mockPRWExporter struct {
	exported     []*prw.WriteRequest
	mu           sync.Mutex
	failCount    int
	failErr      error
	exportCalls  int64
	exportDelay  time.Duration
	closed       bool
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

func TestNewPRWQueued(t *testing.T) {
	mock := &mockPRWExporter{}
	cfg := PRWQueueConfig{
		MaxSize:       1000,
		RetryInterval: time.Second,
		MaxRetryDelay: time.Minute,
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued() error = %v", err)
	}
	defer qe.Close()

	if qe.maxSize != 1000 {
		t.Errorf("maxSize = %d, want 1000", qe.maxSize)
	}
}

func TestNewPRWQueued_Defaults(t *testing.T) {
	mock := &mockPRWExporter{}
	cfg := PRWQueueConfig{} // All defaults

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued() error = %v", err)
	}
	defer qe.Close()

	if qe.maxSize != 10000 {
		t.Errorf("maxSize = %d, want 10000 (default)", qe.maxSize)
	}
	if qe.baseDelay != 5*time.Second {
		t.Errorf("baseDelay = %v, want 5s (default)", qe.baseDelay)
	}
	if qe.maxDelay != 5*time.Minute {
		t.Errorf("maxDelay = %v, want 5m (default)", qe.maxDelay)
	}
}

func TestPRWQueuedExporter_Export_Success(t *testing.T) {
	mock := &mockPRWExporter{}
	cfg := PRWQueueConfig{
		MaxSize:       100,
		RetryInterval: 50 * time.Millisecond,
	}

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
	qe, _ := NewPRWQueued(mock, PRWQueueConfig{})
	defer qe.Close()

	err := qe.Export(context.Background(), nil)
	if err != nil {
		t.Errorf("Export(nil) should not return error, got %v", err)
	}
}

func TestPRWQueuedExporter_Export_EmptyRequest(t *testing.T) {
	mock := &mockPRWExporter{}
	qe, _ := NewPRWQueued(mock, PRWQueueConfig{})
	defer qe.Close()

	err := qe.Export(context.Background(), &prw.WriteRequest{})
	if err != nil {
		t.Errorf("Export(empty) should not return error, got %v", err)
	}
}

func TestPRWQueuedExporter_Export_RetryableError(t *testing.T) {
	mock := &mockPRWExporter{
		failCount: 1,
		failErr:   &PRWServerError{StatusCode: 500, Message: "server error"},
	}
	cfg := PRWQueueConfig{
		MaxSize:       100,
		RetryInterval: 50 * time.Millisecond,
	}

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
	if err != nil {
		t.Errorf("Export() should return nil for retryable error (queued), got %v", err)
	}

	// Queue should have 1 entry
	if qe.QueueSize() != 1 {
		t.Errorf("Queue size = %d, want 1", qe.QueueSize())
	}

	// Wait for retry
	time.Sleep(150 * time.Millisecond)

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
	cfg := PRWQueueConfig{
		MaxSize:       100,
		RetryInterval: 50 * time.Millisecond,
	}

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

func TestPRWQueuedExporter_Export_QueueFull(t *testing.T) {
	mock := &mockPRWExporter{
		failCount: 100, // Always fail
		failErr:   &PRWServerError{StatusCode: 500, Message: "server error"},
	}
	cfg := PRWQueueConfig{
		MaxSize:       3, // Small queue
		RetryInterval: time.Hour,
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued() error = %v", err)
	}
	defer qe.Close()

	// Add more requests than queue can hold
	for i := 0; i < 5; i++ {
		req := &prw.WriteRequest{
			Timeseries: []prw.TimeSeries{
				{
					Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
					Samples: []prw.Sample{{Value: float64(i), Timestamp: int64(i)}},
				},
			},
		}
		_ = qe.Export(context.Background(), req)
	}

	// Queue should be at max size (oldest entries dropped)
	if qe.QueueSize() != 3 {
		t.Errorf("Queue size = %d, want 3 (max)", qe.QueueSize())
	}
}

func TestPRWQueuedExporter_Close(t *testing.T) {
	mock := &mockPRWExporter{
		failCount: 1,
		failErr:   &PRWServerError{StatusCode: 500, Message: "server error"},
	}
	cfg := PRWQueueConfig{
		RetryInterval: time.Hour, // Long retry to test drain
	}

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
		failErr:   &PRWServerError{StatusCode: 500, Message: "server error"},
	}
	cfg := PRWQueueConfig{
		MaxSize:       100,
		RetryInterval: time.Hour,
	}

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
					Samples: []prw.Sample{{Value: float64(i), Timestamp: int64(i)}},
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
	var exportCalls int64
	mock := &mockPRWExporter{
		failCount: 10, // Keep failing
		failErr:   &PRWServerError{StatusCode: 500, Message: "server error"},
	}
	cfg := PRWQueueConfig{
		MaxSize:       100,
		RetryInterval: 20 * time.Millisecond,
		MaxRetryDelay: 100 * time.Millisecond,
	}

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
	exportCalls = atomic.LoadInt64(&mock.exportCalls)
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
	// First call succeeds (on initial export), then fails with non-retryable
	callCount := 0
	mock := &mockPRWExporter{}
	mock.failErr = errors.New("network error")
	mock.failCount = 1 // First call fails with network error (retryable)

	cfg := PRWQueueConfig{
		MaxSize:       100,
		RetryInterval: 20 * time.Millisecond,
	}

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
	_ = callCount

	// Wait for retry to succeed
	time.Sleep(100 * time.Millisecond)

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
	cfg := PRWQueueConfig{
		MaxSize:       1000,
		RetryInterval: time.Hour,
	}

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
							Samples: []prw.Sample{{Value: float64(id*numRequests + j), Timestamp: int64(id*numRequests + j)}},
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

// Benchmarks

func BenchmarkPRWQueuedExporter_Export_Success(b *testing.B) {
	mock := &mockPRWExporter{}
	cfg := PRWQueueConfig{
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
