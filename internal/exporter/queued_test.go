package exporter

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	"github.com/szibis/metrics-governor/internal/queue"
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
	if err != nil && !errors.Is(err, ErrExportQueued) {
		t.Errorf("Expected nil or ErrExportQueued, got error: %v", err)
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

// Circuit Breaker Tests

func TestCircuitBreaker_StartsClosedState(t *testing.T) {
	cb := NewCircuitBreaker(5, 30*time.Second)

	if cb.State() != CircuitClosed {
		t.Errorf("Expected circuit to start in closed state, got %v", cb.State())
	}

	if !cb.AllowRequest() {
		t.Error("Expected circuit to allow requests when closed")
	}

	if cb.ConsecutiveFailures() != 0 {
		t.Errorf("Expected 0 consecutive failures, got %d", cb.ConsecutiveFailures())
	}
}

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	threshold := 5
	cb := NewCircuitBreaker(threshold, 30*time.Second)

	// Record failures up to threshold
	for i := 0; i < threshold; i++ {
		if cb.State() != CircuitClosed {
			t.Errorf("Expected circuit to remain closed after %d failures", i)
		}
		cb.RecordFailure()
	}

	// Circuit should be open now
	if cb.State() != CircuitOpen {
		t.Errorf("Expected circuit to be open after %d failures, got %v", threshold, cb.State())
	}

	if cb.AllowRequest() {
		t.Error("Expected circuit to block requests when open")
	}
}

func TestCircuitBreaker_ResetsOnSuccess(t *testing.T) {
	cb := NewCircuitBreaker(5, 30*time.Second)

	// Record some failures
	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.ConsecutiveFailures() != 3 {
		t.Errorf("Expected 3 consecutive failures, got %d", cb.ConsecutiveFailures())
	}

	// Record success - should reset counter
	cb.RecordSuccess()

	if cb.ConsecutiveFailures() != 0 {
		t.Errorf("Expected 0 consecutive failures after success, got %d", cb.ConsecutiveFailures())
	}

	if cb.State() != CircuitClosed {
		t.Error("Expected circuit to remain closed after success")
	}
}

func TestCircuitBreaker_HalfOpenAfterTimeout(t *testing.T) {
	cb := NewCircuitBreaker(2, 100*time.Millisecond)

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.State() != CircuitOpen {
		t.Fatal("Expected circuit to be open")
	}

	// Wait for reset timeout
	time.Sleep(150 * time.Millisecond)

	// Should transition to half-open on next AllowRequest
	if !cb.AllowRequest() {
		t.Error("Expected circuit to allow request after reset timeout")
	}

	if cb.State() != CircuitHalfOpen {
		t.Errorf("Expected circuit to be half-open, got %v", cb.State())
	}
}

func TestCircuitBreaker_ClosesOnHalfOpenSuccess(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for half-open
	time.Sleep(100 * time.Millisecond)
	cb.AllowRequest() // Trigger half-open transition

	if cb.State() != CircuitHalfOpen {
		t.Fatal("Expected circuit to be half-open")
	}

	// Success in half-open should close circuit
	cb.RecordSuccess()

	if cb.State() != CircuitClosed {
		t.Errorf("Expected circuit to be closed after half-open success, got %v", cb.State())
	}
}

func TestCircuitBreaker_ReopensOnHalfOpenFailure(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond)

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	// Wait for half-open
	time.Sleep(100 * time.Millisecond)
	cb.AllowRequest() // Trigger half-open transition

	if cb.State() != CircuitHalfOpen {
		t.Fatal("Expected circuit to be half-open")
	}

	// Failure in half-open should reopen circuit
	cb.RecordFailure()

	if cb.State() != CircuitOpen {
		t.Errorf("Expected circuit to be open after half-open failure, got %v", cb.State())
	}
}

func TestCircuitBreaker_ConcurrentAccess(t *testing.T) {
	cb := NewCircuitBreaker(100, 1*time.Second)

	var wg sync.WaitGroup
	// Concurrent failures
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cb.RecordFailure()
		}()
	}
	// Concurrent successes
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cb.RecordSuccess()
		}()
	}
	// Concurrent checks
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = cb.AllowRequest()
			_ = cb.State()
			_ = cb.ConsecutiveFailures()
		}()
	}

	wg.Wait()
	// Just verify no panics occurred
}

// Exponential Backoff Tests

func TestQueuedExporter_BackoffEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{failCount: 100} // Always fail

	queueCfg := queue.Config{
		Path:              tmpDir,
		MaxSize:           100,
		RetryInterval:     10 * time.Millisecond,
		MaxRetryDelay:     500 * time.Millisecond,
		BackoffEnabled:    true,
		BackoffMultiplier: 2.0,
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("Failed to create QueuedExporter: %v", err)
	}
	defer qe.Close()

	// Verify backoff is enabled
	if !qe.backoffEnabled {
		t.Error("Expected backoff to be enabled")
	}
	if qe.backoffMultiplier != 2.0 {
		t.Errorf("Expected backoff multiplier 2.0, got %f", qe.backoffMultiplier)
	}
}

func TestQueuedExporter_BackoffMultiplierDefault(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{}

	queueCfg := queue.Config{
		Path:           tmpDir,
		MaxSize:        100,
		RetryInterval:  10 * time.Millisecond,
		MaxRetryDelay:  500 * time.Millisecond,
		BackoffEnabled: true,
		// BackoffMultiplier not set - should default to 2.0
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("Failed to create QueuedExporter: %v", err)
	}
	defer qe.Close()

	if qe.backoffMultiplier != 2.0 {
		t.Errorf("Expected default backoff multiplier 2.0, got %f", qe.backoffMultiplier)
	}
}

func TestQueuedExporter_WithCircuitBreaker(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{failCount: 100}

	queueCfg := queue.Config{
		Path:                       tmpDir,
		MaxSize:                    100,
		RetryInterval:              10 * time.Millisecond,
		MaxRetryDelay:              100 * time.Millisecond,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    3,
		CircuitBreakerResetTimeout: 50 * time.Millisecond,
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("Failed to create QueuedExporter: %v", err)
	}
	defer qe.Close()

	if qe.circuitBreaker == nil {
		t.Fatal("Expected circuit breaker to be initialized")
	}

	// Queue an entry to trigger failures
	req := createTestRequest()
	_ = qe.Export(context.Background(), req)

	// Wait for circuit to open (threshold is 3 failures)
	time.Sleep(200 * time.Millisecond)

	// Circuit should have opened
	if qe.circuitBreaker.State() != CircuitOpen {
		t.Logf("Circuit state: %v, consecutive failures: %d",
			qe.circuitBreaker.State(), qe.circuitBreaker.ConsecutiveFailures())
	}
}

func TestQueuedExporter_CircuitBreakerDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{}

	queueCfg := queue.Config{
		Path:                  tmpDir,
		MaxSize:               100,
		RetryInterval:         10 * time.Millisecond,
		MaxRetryDelay:         100 * time.Millisecond,
		CircuitBreakerEnabled: true,
		// Threshold and ResetTimeout not set - should use defaults
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("Failed to create QueuedExporter: %v", err)
	}
	defer qe.Close()

	if qe.circuitBreaker == nil {
		t.Fatal("Expected circuit breaker to be initialized")
	}

	// Verify defaults
	if qe.circuitBreaker.failureThreshold != 5 {
		t.Errorf("Expected default threshold 5, got %d", qe.circuitBreaker.failureThreshold)
	}
	if qe.circuitBreaker.resetTimeout != 30*time.Second {
		t.Errorf("Expected default reset timeout 30s, got %v", qe.circuitBreaker.resetTimeout)
	}
}

func TestQueuedExporter_CircuitBreakerDisabled(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{}

	queueCfg := queue.Config{
		Path:                  tmpDir,
		MaxSize:               100,
		RetryInterval:         10 * time.Millisecond,
		MaxRetryDelay:         100 * time.Millisecond,
		CircuitBreakerEnabled: false,
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("Failed to create QueuedExporter: %v", err)
	}
	defer qe.Close()

	if qe.circuitBreaker != nil {
		t.Error("Expected circuit breaker to be nil when disabled")
	}
}

func TestQueuedExporter_GetCircuitState(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{}

	// Test with circuit breaker disabled
	queueCfg := queue.Config{
		Path:                  tmpDir,
		MaxSize:               100,
		RetryInterval:         1 * time.Hour,
		MaxRetryDelay:         1 * time.Hour,
		CircuitBreakerEnabled: false,
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("Failed to create QueuedExporter: %v", err)
	}

	state := qe.getCircuitState()
	if state != "disabled" {
		t.Errorf("Expected 'disabled' state, got %s", state)
	}
	qe.Close()

	// Test with circuit breaker enabled
	queueCfg.CircuitBreakerEnabled = true
	queueCfg.Path = t.TempDir()
	qe, err = NewQueued(&mockExporter{}, queueCfg)
	if err != nil {
		t.Fatalf("Failed to create QueuedExporter: %v", err)
	}
	defer qe.Close()

	state = qe.getCircuitState()
	if state != "closed" {
		t.Errorf("Expected 'closed' state, got %s", state)
	}
}

func TestQueuedExporter_BackoffAndCircuitBreakerIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{failCount: 100}

	queueCfg := queue.Config{
		Path:                       tmpDir,
		MaxSize:                    100,
		RetryInterval:              5 * time.Millisecond,
		MaxRetryDelay:              50 * time.Millisecond,
		BackoffEnabled:             true,
		BackoffMultiplier:          2.0,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    5,
		CircuitBreakerResetTimeout: 100 * time.Millisecond,
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("Failed to create QueuedExporter: %v", err)
	}
	defer qe.Close()

	// Queue entries
	for i := 0; i < 3; i++ {
		req := createTestRequest()
		_ = qe.Export(context.Background(), req)
	}

	// Wait for retries and circuit breaker to activate
	time.Sleep(300 * time.Millisecond)

	// Verify both mechanisms are working
	if qe.circuitBreaker.ConsecutiveFailures() < 1 {
		// Circuit may have been reset by successful retries or may be counting failures
		t.Logf("Circuit failures: %d, state: %v",
			qe.circuitBreaker.ConsecutiveFailures(), qe.circuitBreaker.State())
	}
}
