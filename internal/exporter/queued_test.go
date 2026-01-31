package exporter

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/queue"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// mockExporter is a test exporter that can be configured to fail or succeed.
type mockExporter struct {
	mu          sync.Mutex
	failCount   int
	exportCount int64
	exportErr   error
	closed      bool
}

func (m *mockExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	atomic.AddInt64(&m.exportCount, 1)

	if m.failCount > 0 {
		m.failCount--
		if m.exportErr != nil {
			return m.exportErr
		}
		return errors.New("mock export error")
	}
	return nil
}

func (m *mockExporter) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockExporter) setFailCount(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failCount = count
}

func (m *mockExporter) getExportCount() int64 {
	return atomic.LoadInt64(&m.exportCount)
}

func createTestRequest() *colmetricspb.ExportMetricsServiceRequest {
	return &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{
								Name: "test-metric",
							},
						},
					},
				},
			},
		},
	}
}

func TestQueuedExporter_ImmediateSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{}

	queueCfg := queue.Config{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 100 * time.Millisecond,
		MaxRetryDelay: 1 * time.Second,
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("Failed to create QueuedExporter: %v", err)
	}
	defer qe.Close()

	req := createTestRequest()
	err = qe.Export(context.Background(), req)
	if err != nil {
		t.Errorf("Expected no error on successful export, got: %v", err)
	}

	if qe.QueueLen() != 0 {
		t.Errorf("Expected empty queue after successful export, got %d", qe.QueueLen())
	}

	if mock.getExportCount() != 1 {
		t.Errorf("Expected 1 export call, got %d", mock.getExportCount())
	}
}

func TestQueuedExporter_FailureQueues(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{failCount: 100} // Always fail

	queueCfg := queue.Config{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 1 * time.Hour, // Don't retry during test
		MaxRetryDelay: 1 * time.Hour,
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("Failed to create QueuedExporter: %v", err)
	}
	defer qe.Close()

	req := createTestRequest()
	err = qe.Export(context.Background(), req)
	if err != nil {
		t.Errorf("Expected nil (queued), got error: %v", err)
	}

	if qe.QueueLen() != 1 {
		t.Errorf("Expected 1 queued entry, got %d", qe.QueueLen())
	}
}

func TestQueuedExporter_RetrySuccess(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{failCount: 1} // Fail once, then succeed

	queueCfg := queue.Config{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 50 * time.Millisecond,
		MaxRetryDelay: 100 * time.Millisecond,
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("Failed to create QueuedExporter: %v", err)
	}
	defer qe.Close()

	req := createTestRequest()
	_ = qe.Export(context.Background(), req)

	// Wait for retry
	time.Sleep(200 * time.Millisecond)

	if qe.QueueLen() != 0 {
		t.Errorf("Expected empty queue after retry, got %d", qe.QueueLen())
	}

	// Should have 2 export attempts: initial fail + retry success
	if mock.getExportCount() < 2 {
		t.Errorf("Expected at least 2 export calls, got %d", mock.getExportCount())
	}
}

func TestQueuedExporter_Backoff(t *testing.T) {
	qe := &QueuedExporter{
		baseDelay: 1 * time.Second,
		maxDelay:  30 * time.Second,
	}

	tests := []struct {
		retries  int
		expected time.Duration
	}{
		{0, 0},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 16 * time.Second},
		{5, 30 * time.Second}, // Capped at maxDelay
		{6, 30 * time.Second}, // Capped at maxDelay
	}

	for _, tt := range tests {
		got := qe.calculateBackoff(tt.retries)
		if got != tt.expected {
			t.Errorf("calculateBackoff(%d) = %v, want %v", tt.retries, got, tt.expected)
		}
	}
}

func TestQueuedExporter_GracefulShutdown(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{failCount: 100} // Always fail initially

	queueCfg := queue.Config{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 1 * time.Hour, // Don't retry during test
		MaxRetryDelay: 1 * time.Hour,
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("Failed to create QueuedExporter: %v", err)
	}

	// Queue some entries
	for i := 0; i < 5; i++ {
		req := createTestRequest()
		qe.Export(context.Background(), req)
	}

	if qe.QueueLen() != 5 {
		t.Errorf("Expected 5 queued entries, got %d", qe.QueueLen())
	}

	// Now allow exports to succeed for drain
	mock.setFailCount(0)

	// Close should drain the queue
	start := time.Now()
	err = qe.Close()
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("Close returned error: %v", err)
	}

	if elapsed > 35*time.Second {
		t.Errorf("Close took too long: %v", elapsed)
	}
}

func TestQueuedExporter_QueueRecovery(t *testing.T) {
	tmpDir := t.TempDir()

	// First instance - queue entries and close
	mock1 := &mockExporter{failCount: 100}
	queueCfg := queue.Config{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 1 * time.Hour,
		MaxRetryDelay: 1 * time.Hour,
	}

	qe1, err := NewQueued(mock1, queueCfg)
	if err != nil {
		t.Fatalf("Failed to create first QueuedExporter: %v", err)
	}

	for i := 0; i < 3; i++ {
		req := createTestRequest()
		qe1.Export(context.Background(), req)
	}

	// Close without allowing drain to succeed
	qe1.Close()

	// Second instance - should recover queued entries
	mock2 := &mockExporter{}
	qe2, err := NewQueued(mock2, queueCfg)
	if err != nil {
		t.Fatalf("Failed to create second QueuedExporter: %v", err)
	}
	defer qe2.Close()

	// Queue should have recovered entries
	if qe2.QueueLen() < 1 {
		t.Errorf("Expected recovered entries in queue, got %d", qe2.QueueLen())
	}
}

func TestQueuedExporter_ConcurrentExports(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{failCount: 50} // Half will fail

	queueCfg := queue.Config{
		Path:          tmpDir,
		MaxSize:       1000,
		RetryInterval: 1 * time.Hour,
		MaxRetryDelay: 1 * time.Hour,
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("Failed to create QueuedExporter: %v", err)
	}
	defer qe.Close()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := createTestRequest()
			qe.Export(context.Background(), req)
		}()
	}

	wg.Wait()

	// Some should be queued (the ones that failed)
	if qe.QueueLen() == 0 {
		t.Error("Expected some entries to be queued after failures")
	}

	if mock.getExportCount() != 100 {
		t.Errorf("Expected 100 export attempts, got %d", mock.getExportCount())
	}
}

func TestQueuedExporter_MultipleFailuresThenSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{failCount: 3} // Fail 3 times, then succeed

	queueCfg := queue.Config{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 10 * time.Millisecond,
		MaxRetryDelay: 50 * time.Millisecond,
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("Failed to create QueuedExporter: %v", err)
	}
	defer qe.Close()

	req := createTestRequest()
	_ = qe.Export(context.Background(), req)

	// Wait for retries
	time.Sleep(500 * time.Millisecond)

	if qe.QueueLen() != 0 {
		t.Errorf("Expected empty queue after successful retry, got %d", qe.QueueLen())
	}

	// Should have multiple export attempts
	if mock.getExportCount() < 4 {
		t.Errorf("Expected at least 4 export calls, got %d", mock.getExportCount())
	}
}
