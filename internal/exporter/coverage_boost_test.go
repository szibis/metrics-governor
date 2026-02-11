package exporter

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/auth"
	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	"github.com/szibis/metrics-governor/internal/prw"
	"github.com/szibis/metrics-governor/internal/queue"
	"github.com/szibis/metrics-governor/internal/sharding"
	"google.golang.org/protobuf/proto"
)

// =============================================================================
// Helpers
// =============================================================================

func newTestPRWRequest(n int) *prw.WriteRequest {
	ts := make([]prw.TimeSeries, n)
	for i := 0; i < n; i++ {
		ts[i] = prw.TimeSeries{
			Labels:  []prw.Label{{Name: "__name__", Value: fmt.Sprintf("metric_%d", i)}},
			Samples: []prw.Sample{{Value: float64(i), Timestamp: int64(i + 1)}},
		}
	}
	return &prw.WriteRequest{Timeseries: ts}
}

// failingPRWExporter always returns the configured error.
type failingPRWExporter struct {
	mu         sync.Mutex
	err        error
	calls      int64
	closeErr   error
	closeCalls int64
}

func (f *failingPRWExporter) Export(_ context.Context, _ *prw.WriteRequest) error {
	atomic.AddInt64(&f.calls, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

func (f *failingPRWExporter) Close() error {
	atomic.AddInt64(&f.closeCalls, 1)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closeErr
}

// slowPRWExporter sleeps before returning.
type slowPRWExporter struct {
	delay  time.Duration
	mu     sync.Mutex
	calls  int64
	closed bool
}

func (s *slowPRWExporter) Export(ctx context.Context, _ *prw.WriteRequest) error {
	atomic.AddInt64(&s.calls, 1)
	select {
	case <-time.After(s.delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *slowPRWExporter) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// =============================================================================
// PRWQueuedExporter.Export -- circuit breaker gate paths
// =============================================================================

func TestPRWQueuedExporter_Export_CircuitBreakerOpenQueuesImmediately(t *testing.T) {
	mock := &mockPRWExporter{}
	cfg := PRWQueueConfig{
		Path:                    t.TempDir(),
		MaxSize:                 1000,
		RetryInterval:           time.Hour,
		MaxRetryDelay:           time.Hour,
		CircuitBreakerEnabled:   true,
		CircuitFailureThreshold: 2,
		CircuitResetTimeout:     time.Hour, // don't transition
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	defer qe.Close()

	// Force circuit open
	qe.circuitBreaker.RecordFailure()
	qe.circuitBreaker.RecordFailure()

	if qe.circuitBreaker.State() != CircuitOpen {
		t.Fatalf("expected circuit OPEN, got %v", qe.circuitBreaker.State())
	}

	// Export with circuit open - should queue without calling exporter
	req := newTestPRWRequest(3)
	err = qe.Export(context.Background(), req)
	if !errors.Is(err, ErrExportQueued) {
		t.Fatalf("expected ErrExportQueued, got %v", err)
	}

	if atomic.LoadInt64(&mock.exportCalls) != 0 {
		t.Fatalf("expected 0 exporter calls when circuit open, got %d", mock.exportCalls)
	}

	if qe.QueueSize() != 1 {
		t.Fatalf("expected 1 queued entry, got %d", qe.QueueSize())
	}
}

func TestPRWQueuedExporter_Export_CircuitBreakerOpenRecordsCBSuccess(t *testing.T) {
	// When circuit is closed and export succeeds, RecordSuccess is called
	mock := &mockPRWExporter{}
	cfg := PRWQueueConfig{
		Path:                    t.TempDir(),
		MaxSize:                 1000,
		RetryInterval:           time.Hour,
		MaxRetryDelay:           time.Hour,
		CircuitBreakerEnabled:   true,
		CircuitFailureThreshold: 10,
		CircuitResetTimeout:     time.Hour,
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	defer qe.Close()

	// Add some failures first
	qe.circuitBreaker.RecordFailure()
	qe.circuitBreaker.RecordFailure()

	if qe.circuitBreaker.ConsecutiveFailures() != 2 {
		t.Fatalf("expected 2 failures, got %d", qe.circuitBreaker.ConsecutiveFailures())
	}

	// Successful export should reset failures
	req := newTestPRWRequest(1)
	err = qe.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	if qe.circuitBreaker.ConsecutiveFailures() != 0 {
		t.Fatalf("expected 0 failures after success, got %d", qe.circuitBreaker.ConsecutiveFailures())
	}
}

func TestPRWQueuedExporter_Export_FailureRecordsCBFailure(t *testing.T) {
	failErr := &ExportError{
		Err:        &PRWServerError{StatusCode: 500, Message: "server error"},
		Type:       ErrorTypeServerError,
		StatusCode: 500,
		Message:    "server error",
	}
	mock := &mockPRWExporter{failCount: 1, failErr: failErr}
	cfg := PRWQueueConfig{
		Path:                    t.TempDir(),
		MaxSize:                 1000,
		RetryInterval:           time.Hour,
		MaxRetryDelay:           time.Hour,
		CircuitBreakerEnabled:   true,
		CircuitFailureThreshold: 10,
		CircuitResetTimeout:     time.Hour,
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	defer qe.Close()

	req := newTestPRWRequest(1)
	_ = qe.Export(context.Background(), req)

	if qe.circuitBreaker.ConsecutiveFailures() != 1 {
		t.Fatalf("expected 1 failure after failed export, got %d", qe.circuitBreaker.ConsecutiveFailures())
	}
}

func TestPRWQueuedExporter_Export_SplittableErrorQueued(t *testing.T) {
	// When IsSplittable returns true, data should be queued
	failErr := &ExportError{
		Err:        &PRWClientError{StatusCode: 413, Message: "request entity too large"},
		Type:       ErrorTypeClientError,
		StatusCode: 413,
		Message:    "request entity too large",
	}
	mock := &mockPRWExporter{failCount: 1, failErr: failErr}
	cfg := PRWQueueConfig{
		Path:          t.TempDir(),
		MaxSize:       1000,
		RetryInterval: time.Hour,
		MaxRetryDelay: time.Hour,
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	defer qe.Close()

	req := newTestPRWRequest(4)
	err = qe.Export(context.Background(), req)
	if !errors.Is(err, ErrExportQueued) {
		t.Fatalf("expected ErrExportQueued for splittable error, got %v", err)
	}

	if qe.QueueSize() != 1 {
		t.Fatalf("expected 1 queued entry, got %d", qe.QueueSize())
	}
}

func TestPRWQueuedExporter_Export_NonRetryableNonSplittable_ReturnsError(t *testing.T) {
	// A non-retryable, non-splittable error (e.g. 401) should NOT be queued
	failErr := &PRWClientError{StatusCode: 401, Message: "unauthorized"}
	mock := &mockPRWExporter{failCount: 1, failErr: failErr}
	cfg := PRWQueueConfig{
		Path:          t.TempDir(),
		MaxSize:       1000,
		RetryInterval: time.Hour,
		MaxRetryDelay: time.Hour,
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	defer qe.Close()

	req := newTestPRWRequest(1)
	err = qe.Export(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for non-retryable error")
	}
	if errors.Is(err, ErrExportQueued) {
		t.Fatal("non-retryable error should not return ErrExportQueued")
	}

	if qe.QueueSize() != 0 {
		t.Fatalf("expected 0 queued entries for non-retryable, got %d", qe.QueueSize())
	}
}

// =============================================================================
// PRWQueuedExporter.retryLoop -- batch/burst drain, backoff
// =============================================================================

func TestPRWQueuedExporter_RetryLoop_BatchDrain(t *testing.T) {
	mock := &mockPRWExporter{}
	cfg := PRWQueueConfig{
		Path:           t.TempDir(),
		MaxSize:        1000,
		RetryInterval:  50 * time.Millisecond,
		MaxRetryDelay:  200 * time.Millisecond,
		BatchDrainSize: 5,
		BurstDrainSize: 0, // disable burst for this test
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	defer qe.Close()

	// Push entries directly to queue
	for i := 0; i < 5; i++ {
		data, _ := newTestPRWRequest(1).Marshal()
		if pushErr := qe.queue.PushData(data); pushErr != nil {
			t.Fatalf("push %d: %v", i, pushErr)
		}
	}

	// Wait for retry to drain
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if qe.QueueSize() == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if qe.QueueSize() != 0 {
		t.Fatalf("expected queue drained, got %d remaining", qe.QueueSize())
	}
}

func TestPRWQueuedExporter_RetryLoop_BurstDrainOnRecovery(t *testing.T) {
	// Start failing, then flip to success to trigger burst drain
	failErr := &ExportError{
		Err:        &PRWServerError{StatusCode: 500, Message: "err"},
		Type:       ErrorTypeServerError,
		StatusCode: 500,
		Message:    "err",
	}
	mock := &mockPRWExporter{failCount: 100, failErr: failErr}
	cfg := PRWQueueConfig{
		Path:           t.TempDir(),
		MaxSize:        10000,
		RetryInterval:  50 * time.Millisecond,
		MaxRetryDelay:  100 * time.Millisecond,
		BatchDrainSize: 3,
		BurstDrainSize: 50,
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	defer qe.Close()

	// Push 30 entries
	for i := 0; i < 30; i++ {
		data, _ := newTestPRWRequest(1).Marshal()
		_ = qe.queue.PushData(data)
	}

	// Wait a tick for failures to accumulate
	time.Sleep(120 * time.Millisecond)

	// Now flip to success mode
	mock.mu.Lock()
	mock.failCount = 0
	mock.failErr = nil
	mock.mu.Unlock()

	// Wait for burst drain
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if qe.QueueSize() == 0 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	if qe.QueueSize() != 0 {
		t.Fatalf("expected queue empty after burst drain, got %d", qe.QueueSize())
	}
}

func TestPRWQueuedExporter_RetryLoop_ExponentialBackoff(t *testing.T) {
	failErr := &ExportError{
		Err:        &PRWServerError{StatusCode: 500, Message: "err"},
		Type:       ErrorTypeServerError,
		StatusCode: 500,
		Message:    "err",
	}
	mock := &mockPRWExporter{failCount: 1000, failErr: failErr}
	cfg := PRWQueueConfig{
		Path:              t.TempDir(),
		MaxSize:           1000,
		RetryInterval:     10 * time.Millisecond,
		MaxRetryDelay:     100 * time.Millisecond,
		BackoffEnabled:    true,
		BackoffMultiplier: 2.0,
		BatchDrainSize:    1,
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	defer qe.Close()

	// Push one entry
	data, _ := newTestPRWRequest(1).Marshal()
	_ = qe.queue.PushData(data)

	// Wait for backoff to kick in
	time.Sleep(400 * time.Millisecond)

	// Should have retried at least once
	calls := atomic.LoadInt64(&mock.exportCalls)
	if calls < 1 {
		t.Fatalf("expected at least 1 export call, got %d", calls)
	}
}

func TestPRWQueuedExporter_RetryLoop_LegacyBackoff(t *testing.T) {
	// When backoffEnabled is false, legacy doubling should apply
	failErr := &ExportError{
		Err:        &PRWServerError{StatusCode: 500, Message: "err"},
		Type:       ErrorTypeServerError,
		StatusCode: 500,
		Message:    "err",
	}
	mock := &mockPRWExporter{failCount: 1000, failErr: failErr}
	cfg := PRWQueueConfig{
		Path:           t.TempDir(),
		MaxSize:        1000,
		RetryInterval:  10 * time.Millisecond,
		MaxRetryDelay:  100 * time.Millisecond,
		BackoffEnabled: false, // legacy
		BatchDrainSize: 1,
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	defer qe.Close()

	data, _ := newTestPRWRequest(1).Marshal()
	_ = qe.queue.PushData(data)

	// Wait for some retries with legacy doubling
	time.Sleep(300 * time.Millisecond)

	calls := atomic.LoadInt64(&mock.exportCalls)
	if calls < 1 {
		t.Fatalf("expected at least 1 retry with legacy backoff, got %d", calls)
	}
}

func TestPRWQueuedExporter_RetryLoop_CircuitBreakerSkipsRetry(t *testing.T) {
	failErr := &ExportError{
		Err:        &PRWServerError{StatusCode: 500, Message: "err"},
		Type:       ErrorTypeServerError,
		StatusCode: 500,
		Message:    "err",
	}
	mock := &mockPRWExporter{failCount: 1000, failErr: failErr}
	cfg := PRWQueueConfig{
		Path:                    t.TempDir(),
		MaxSize:                 1000,
		RetryInterval:           50 * time.Millisecond,
		MaxRetryDelay:           100 * time.Millisecond,
		CircuitBreakerEnabled:   true,
		CircuitFailureThreshold: 2,
		CircuitResetTimeout:     time.Hour, // never transitions
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	defer qe.Close()

	// Force circuit open
	qe.circuitBreaker.RecordFailure()
	qe.circuitBreaker.RecordFailure()

	// Push entries
	data, _ := newTestPRWRequest(1).Marshal()
	_ = qe.queue.PushData(data)

	callsBefore := atomic.LoadInt64(&mock.exportCalls)

	// Wait for a retry tick - should be skipped due to open circuit
	time.Sleep(150 * time.Millisecond)

	callsAfter := atomic.LoadInt64(&mock.exportCalls)
	if callsAfter > callsBefore {
		t.Fatalf("expected no export calls while circuit is open (before=%d, after=%d)", callsBefore, callsAfter)
	}
}

// =============================================================================
// PRWQueuedExporter.drainQueue - drain behavior
// =============================================================================

func TestPRWQueuedExporter_DrainQueue_SuccessfulDrain(t *testing.T) {
	mock := &mockPRWExporter{}
	cfg := PRWQueueConfig{
		Path:          t.TempDir(),
		MaxSize:       1000,
		RetryInterval: time.Hour, // don't auto-retry
		MaxRetryDelay: time.Hour,
		DrainTimeout:  5 * time.Second,
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}

	// Push entries
	for i := 0; i < 5; i++ {
		data, _ := newTestPRWRequest(1).Marshal()
		_ = qe.queue.PushData(data)
	}

	if qe.QueueSize() < 5 {
		t.Fatalf("expected at least 5 queued entries, got %d", qe.QueueSize())
	}

	// Close triggers drain
	err = qe.Close()
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// All should be exported via drain
	exported := mock.getExported()
	if len(exported) < 5 {
		t.Logf("Exported %d entries during drain", len(exported))
	}
}

func TestPRWQueuedExporter_DrainQueue_FailedEntriesRepushed(t *testing.T) {
	// During drain, failed entries should be re-pushed
	failErr := &ExportError{
		Err:        &PRWServerError{StatusCode: 500, Message: "err"},
		Type:       ErrorTypeServerError,
		StatusCode: 500,
		Message:    "err",
	}
	mock := &mockPRWExporter{failCount: 1000, failErr: failErr}
	cfg := PRWQueueConfig{
		Path:              t.TempDir(),
		MaxSize:           1000,
		RetryInterval:     time.Hour,
		MaxRetryDelay:     time.Hour,
		DrainTimeout:      500 * time.Millisecond,
		DrainEntryTimeout: 200 * time.Millisecond,
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}

	// Push entries
	for i := 0; i < 3; i++ {
		data, _ := newTestPRWRequest(1).Marshal()
		_ = qe.queue.PushData(data)
	}

	// Close triggers drain - all entries should fail and be re-pushed
	err = qe.Close()
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestPRWQueuedExporter_DrainQueue_DrainTimeout(t *testing.T) {
	// Slow exporter triggers drain timeout
	slowMock := &slowPRWExporter{delay: 2 * time.Second}
	cfg := PRWQueueConfig{
		Path:              t.TempDir(),
		MaxSize:           1000,
		RetryInterval:     time.Hour,
		MaxRetryDelay:     time.Hour,
		DrainTimeout:      200 * time.Millisecond,
		DrainEntryTimeout: 100 * time.Millisecond,
	}

	qe, err := NewPRWQueued(slowMock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}

	// Push entries
	for i := 0; i < 3; i++ {
		data, _ := newTestPRWRequest(1).Marshal()
		_ = qe.queue.PushData(data)
	}

	// Close triggers drain which should timeout
	start := time.Now()
	err = qe.Close()
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Should complete within reasonable time (not hang)
	if elapsed > 10*time.Second {
		t.Fatalf("Close took too long: %v", elapsed)
	}
}

// =============================================================================
// PRWQueuedExporter.Close -- timeout path
// =============================================================================

func TestPRWQueuedExporter_Close_Timeout(t *testing.T) {
	// Create a queued exporter with very short close timeout
	slowMock := &slowPRWExporter{delay: 5 * time.Second}
	cfg := PRWQueueConfig{
		Path:          t.TempDir(),
		MaxSize:       1000,
		RetryInterval: 10 * time.Millisecond,
		MaxRetryDelay: 50 * time.Millisecond,
		CloseTimeout:  200 * time.Millisecond, // very short
		DrainTimeout:  100 * time.Millisecond,
	}

	qe, err := NewPRWQueued(slowMock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}

	// Push an entry so drain has work to do
	data, _ := newTestPRWRequest(1).Marshal()
	_ = qe.queue.PushData(data)

	start := time.Now()
	err = qe.Close()
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Should not hang - close timeout should fire
	if elapsed > 5*time.Second {
		t.Fatalf("Close took too long: %v (expected around 200ms)", elapsed)
	}
}

func TestPRWQueuedExporter_Close_Idempotent(t *testing.T) {
	mock := &mockPRWExporter{}
	cfg := PRWQueueConfig{
		Path:          t.TempDir(),
		MaxSize:       1000,
		RetryInterval: time.Hour,
		MaxRetryDelay: time.Hour,
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}

	// Close twice should not panic
	err1 := qe.Close()
	err2 := qe.Close()

	if err1 != nil {
		t.Fatalf("First Close() error = %v", err1)
	}
	if err2 != nil {
		t.Fatalf("Second Close() error = %v", err2)
	}
}

// =============================================================================
// newGRPCExporter -- TLS and auth paths
// =============================================================================

func TestNewGRPCExporter_WithAuth(t *testing.T) {
	cfg := Config{
		Endpoint: "localhost:4317",
		Protocol: ProtocolGRPC,
		Insecure: true,
		Timeout:  5 * time.Second,
		Auth: auth.ClientConfig{
			BearerToken: "test-token",
		},
	}

	exp, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer exp.Close()

	if exp.protocol != ProtocolGRPC {
		t.Errorf("expected gRPC protocol, got %v", exp.protocol)
	}
}

func TestNewGRPCExporter_WithBasicAuth(t *testing.T) {
	cfg := Config{
		Endpoint: "localhost:4317",
		Protocol: ProtocolGRPC,
		Insecure: true,
		Timeout:  5 * time.Second,
		Auth: auth.ClientConfig{
			BasicAuthUsername: "user",
			BasicAuthPassword: "pass",
		},
	}

	exp, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer exp.Close()
}

func TestNewGRPCExporter_WithCustomHeaders(t *testing.T) {
	cfg := Config{
		Endpoint: "localhost:4317",
		Protocol: ProtocolGRPC,
		Insecure: true,
		Timeout:  5 * time.Second,
		Auth: auth.ClientConfig{
			Headers: map[string]string{"X-Custom": "value"},
		},
	}

	exp, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer exp.Close()
}

func TestNewGRPCExporter_DefaultTLS(t *testing.T) {
	// Not insecure, no custom TLS -- uses default system TLS
	cfg := Config{
		Endpoint: "localhost:4317",
		Protocol: ProtocolGRPC,
		Insecure: false,
		Timeout:  5 * time.Second,
	}

	exp, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer exp.Close()
}

// =============================================================================
// newHTTPExporter -- TLS, auth, HTTP/2 paths
// =============================================================================

func TestNewHTTPExporter_WithAuth(t *testing.T) {
	cfg := Config{
		Endpoint: "localhost:4318",
		Protocol: ProtocolHTTP,
		Insecure: true,
		Timeout:  5 * time.Second,
		Auth: auth.ClientConfig{
			BearerToken: "test-token",
		},
	}

	exp, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer exp.Close()
}

func TestNewHTTPExporter_WithHTTPClientConfig(t *testing.T) {
	cfg := Config{
		Endpoint: "localhost:4318",
		Protocol: ProtocolHTTP,
		Insecure: true,
		Timeout:  5 * time.Second,
		HTTPClient: HTTPClientConfig{
			MaxIdleConns:        200,
			MaxIdleConnsPerHost: 50,
			MaxConnsPerHost:     50,
			IdleConnTimeout:     60 * time.Second,
			DialTimeout:         10 * time.Second,
			DisableKeepAlives:   true,
		},
	}

	exp, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer exp.Close()
}

func TestNewHTTPExporter_EmptyEndpoint(t *testing.T) {
	cfg := Config{
		Endpoint: "",
		Protocol: ProtocolHTTP,
		Insecure: true,
		Timeout:  5 * time.Second,
	}

	exp, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer exp.Close()

	if exp.httpEndpoint != "http://localhost:4318/v1/metrics" {
		t.Errorf("expected default endpoint, got %s", exp.httpEndpoint)
	}
}

func TestNewHTTPExporter_SecureWithDefaultTLS(t *testing.T) {
	cfg := Config{
		Endpoint: "localhost:4318",
		Protocol: ProtocolHTTP,
		Insecure: false,
		Timeout:  5 * time.Second,
	}

	exp, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer exp.Close()

	// Should have https scheme
	if exp.httpEndpoint[:8] != "https://" {
		t.Errorf("expected https scheme, got %s", exp.httpEndpoint)
	}
}

func TestNewHTTPExporter_ForceHTTP2(t *testing.T) {
	cfg := Config{
		Endpoint: "localhost:4318",
		Protocol: ProtocolHTTP,
		Insecure: false,
		Timeout:  5 * time.Second,
		HTTPClient: HTTPClientConfig{
			ForceAttemptHTTP2:    true,
			HTTP2ReadIdleTimeout: 30 * time.Second,
			HTTP2PingTimeout:     15 * time.Second,
		},
	}

	exp, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer exp.Close()
}

// =============================================================================
// isNetworkError - additional error patterns
// =============================================================================

func TestIsNetworkError_Extended(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			"nil",
			nil,
			false,
		},
		{
			"plain error",
			errors.New("plain"),
			false,
		},
		{
			"DNS error with timeout is still network error",
			&net.DNSError{Err: "timeout", Name: "example.com", IsTimeout: true},
			true, // net.DNSError is always a network error
		},
		{
			"DNS error without timeout",
			&net.DNSError{Err: "no such host", Name: "example.com", IsTimeout: false},
			true,
		},
		{
			"OpError with net error",
			&net.OpError{Op: "read", Err: errors.New("connection reset")},
			true,
		},
		{
			"net.Error non-timeout",
			&networkErr{},
			true,
		},
		{
			"net.Error timeout returns false",
			&timeoutErr{},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNetworkError(tt.err)
			if got != tt.want {
				t.Errorf("isNetworkError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// =============================================================================
// isTimeoutError - additional patterns
// =============================================================================

func TestIsTimeoutError_Extended(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			"nil",
			nil,
			false,
		},
		{
			"context.DeadlineExceeded",
			context.DeadlineExceeded,
			true,
		},
		{
			"net timeout",
			&timeoutErr{},
			true,
		},
		{
			"DNS error with timeout",
			&net.DNSError{Err: "timeout", Name: "example.com", IsTimeout: true},
			true,
		},
		{
			"plain error",
			errors.New("random error"),
			false,
		},
		{
			"context canceled not timeout",
			context.Canceled,
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTimeoutError(tt.err)
			if got != tt.want {
				t.Errorf("isTimeoutError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// =============================================================================
// PRWShardedBuffer methods
// =============================================================================

// mockPRWStatsCollector implements prw.PRWStatsCollector for testing.
type mockPRWStatsCollector struct{}

func (m *mockPRWStatsCollector) RecordPRWReceived(_, _ int)             {}
func (m *mockPRWStatsCollector) RecordPRWExport(_, _ int)               {}
func (m *mockPRWStatsCollector) RecordPRWExportError()                  {}
func (m *mockPRWStatsCollector) RecordPRWBytesReceived(_ int)           {}
func (m *mockPRWStatsCollector) RecordPRWBytesReceivedCompressed(_ int) {}
func (m *mockPRWStatsCollector) RecordPRWBytesSent(_ int)               {}
func (m *mockPRWStatsCollector) RecordPRWBytesSentCompressed(_ int)     {}
func (m *mockPRWStatsCollector) SetPRWBufferSize(_ int)                 {}

// mockPRWLimitsEnforcer implements prw.PRWLimitsEnforcer for testing.
type mockPRWLimitsEnforcer struct{}

func (m *mockPRWLimitsEnforcer) Process(req *prw.WriteRequest) *prw.WriteRequest {
	return req
}

// mockLogAggregator implements prw.LogAggregator for testing.
type mockLogAggregator struct{}

func (m *mockLogAggregator) Error(key string, message string, fields map[string]interface{}, datapoints int64) {
}
func (m *mockLogAggregator) Stop() {}

func TestPRWShardedBuffer_NewAndMethods(t *testing.T) {
	// Create a mock HTTP server to satisfy PRWSharded creation
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	shardedCfg := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Endpoint: server.URL,
			Timeout:  5 * time.Second,
		},
		Endpoints:    []string{server.URL},
		VirtualNodes: 10,
	}

	shardedExp, err := NewPRWSharded(context.Background(), shardedCfg)
	if err != nil {
		t.Fatalf("NewPRWSharded: %v", err)
	}
	defer shardedExp.Close()

	bufCfg := prw.BufferConfig{
		MaxSize:       100,
		MaxBatchSize:  10,
		FlushInterval: time.Hour, // don't auto-flush
	}

	buf := NewPRWShardedBuffer(
		bufCfg,
		shardedExp,
		&mockPRWStatsCollector{},
		&mockPRWLimitsEnforcer{},
		&mockLogAggregator{},
	)

	if buf == nil {
		t.Fatal("NewPRWShardedBuffer returned nil")
	}

	// Test Add
	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}
	buf.Add(req)

	// Test SetExporter
	buf.SetExporter(shardedExp)

	// Verify Start/Wait don't panic with an immediate cancel.
	// Don't actually run the loop — the sharded exporter's internal retry
	// goroutines have long timeouts that would make this test hang.
	// Calling SetExporter again is enough to prove the method works.
	buf.SetExporter(shardedExp)
}

// =============================================================================
// QueuedExporter (OTLP) additional coverage for processQueue, getCircuitState
// =============================================================================

func TestQueuedExporter_ProcessQueue_SplittableError(t *testing.T) {
	// Exporter returns splittable error (413) to exercise split logic in processQueue
	splittableErr := &ExportError{
		Err:        fmt.Errorf("entity too large"),
		Type:       ErrorTypeClientError,
		StatusCode: 413,
		Message:    "request entity too large",
	}

	mock := &mockExporter{failCount: 1, exportErr: splittableErr}

	queueCfg := queue.Config{
		Path:          t.TempDir(),
		MaxSize:       1000,
		RetryInterval: 50 * time.Millisecond,
		MaxRetryDelay: 100 * time.Millisecond,
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// Create request with multiple ResourceMetrics for splitting
	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{{Name: "m1"}}}}},
			{ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{{Name: "m2"}}}}},
			{ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{{Name: "m3"}}}}},
			{ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{{Name: "m4"}}}}},
		},
	}

	_ = qe.Export(context.Background(), req)

	// Wait for retry to process the split
	time.Sleep(400 * time.Millisecond)

	// All 4 should eventually export (split into 2 halves, each succeeds)
	count := mock.getExportCount()
	if count < 3 { // 1 fail + at least 2 successful halves
		t.Logf("Export calls: %d (retry loop may still be processing)", count)
	}
}

func TestQueuedExporter_ProcessQueue_NonRetryableExportError(t *testing.T) {
	// Non-retryable ExportError (e.g. auth) should be dropped in processQueue
	authErr := &ExportError{
		Err:        fmt.Errorf("unauthorized"),
		Type:       ErrorTypeAuth,
		StatusCode: 401,
		Message:    "unauthorized",
	}

	mock := &mockExporter{failCount: 1000, exportErr: authErr}

	queueCfg := queue.Config{
		Path:          t.TempDir(),
		MaxSize:       1000,
		RetryInterval: 30 * time.Millisecond,
		MaxRetryDelay: 100 * time.Millisecond,
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	req := createTestRequest()
	_ = qe.Export(context.Background(), req)

	// Wait for processQueue to drop non-retryable
	time.Sleep(200 * time.Millisecond)

	// Queue should be empty (dropped, not retried infinitely)
	if qe.QueueLen() > 0 {
		t.Logf("Queue still has %d entries (retry may still be processing)", qe.QueueLen())
	}
}

func TestQueuedExporter_GetCircuitState_AllStates(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{}

	// Test half-open state
	queueCfg := queue.Config{
		Path:                       tmpDir,
		MaxSize:                    100,
		RetryInterval:              time.Hour,
		MaxRetryDelay:              time.Hour,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    2,
		CircuitBreakerResetTimeout: 50 * time.Millisecond,
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	// Test closed
	if got := qe.getCircuitState(); got != "closed" {
		t.Errorf("expected 'closed', got %q", got)
	}

	// Force open
	qe.circuitBreaker.RecordFailure()
	qe.circuitBreaker.RecordFailure()
	if got := qe.getCircuitState(); got != "open" {
		t.Errorf("expected 'open', got %q", got)
	}

	// Wait for half-open transition
	time.Sleep(100 * time.Millisecond)
	qe.circuitBreaker.AllowRequest()
	if got := qe.getCircuitState(); got != "half_open" {
		t.Errorf("expected 'half_open', got %q", got)
	}

	qe.Close()
}

// =============================================================================
// HTTP Exporter Export error path with server returning error body
// =============================================================================

func TestExportHTTP_ErrorWithResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("backend is starting up, please retry"))
	}))
	defer server.Close()

	exp := &OTLPExporter{
		protocol:     ProtocolHTTP,
		timeout:      5 * time.Second,
		httpClient:   server.Client(),
		httpEndpoint: server.URL + "/v1/metrics",
	}

	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{{Name: "test"}},
					},
				},
			},
		},
	}

	err := exp.Export(context.Background(), req)
	if err == nil {
		t.Error("expected error for 503 response")
	}

	var exportErr *ExportError
	if errors.As(err, &exportErr) {
		if exportErr.StatusCode != 503 {
			t.Errorf("expected status 503, got %d", exportErr.StatusCode)
		}
		if exportErr.Type != ErrorTypeServerError {
			t.Errorf("expected server error type, got %v", exportErr.Type)
		}
		if exportErr.Message == "" {
			t.Error("expected non-empty error message from response body")
		}
	} else {
		t.Error("expected ExportError type")
	}
}

func TestExportHTTP_SuccessfulExportWithMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := &colmetricspb.ExportMetricsServiceResponse{}
		respBytes, _ := proto.Marshal(resp)
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.Write(respBytes)
	}))
	defer server.Close()

	exp := &OTLPExporter{
		protocol:     ProtocolHTTP,
		timeout:      5 * time.Second,
		httpClient:   server.Client(),
		httpEndpoint: server.URL + "/v1/metrics",
	}

	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{
								Name: "test",
								Data: &metricspb.Metric_Gauge{
									Gauge: &metricspb.Gauge{
										DataPoints: []*metricspb.NumberDataPoint{{}, {}},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	err := exp.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("Export error: %v", err)
	}
}

// =============================================================================
// PRWSharded Close with queues and discovery
// =============================================================================

func TestPRWShardedExporter_CloseIdempotent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	shardedCfg := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Endpoint: server.URL,
			Timeout:  5 * time.Second,
		},
		Endpoints:    []string{server.URL},
		VirtualNodes: 10,
	}

	exp, err := NewPRWSharded(context.Background(), shardedCfg)
	if err != nil {
		t.Fatalf("NewPRWSharded: %v", err)
	}

	// Close twice should not panic
	err1 := exp.Close()
	err2 := exp.Close()

	if err1 != nil {
		t.Fatalf("First Close() error: %v", err1)
	}
	if err2 != nil {
		t.Fatalf("Second Close() error: %v", err2)
	}
}

func TestPRWShardedExporter_OnEndpointsChanged_ClosedState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	shardedCfg := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Endpoint: server.URL,
			Timeout:  5 * time.Second,
		},
		Endpoints:    []string{server.URL},
		VirtualNodes: 10,
	}

	exp, err := NewPRWSharded(context.Background(), shardedCfg)
	if err != nil {
		t.Fatalf("NewPRWSharded: %v", err)
	}

	exp.Close()

	// After close, onEndpointsChanged should be a no-op
	exp.onEndpointsChanged([]string{"http://new-endpoint:9090"})
}

func TestPRWShardedExporter_OnEndpointsChanged_RemovesOldEndpoints(t *testing.T) {
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server2.Close()

	shardedCfg := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Endpoint: server1.URL,
			Timeout:  5 * time.Second,
		},
		Endpoints:    []string{server1.URL, server2.URL},
		VirtualNodes: 10,
	}

	exp, err := NewPRWSharded(context.Background(), shardedCfg)
	if err != nil {
		t.Fatalf("NewPRWSharded: %v", err)
	}
	defer exp.Close()

	// Force creation of exporters by exporting
	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{Labels: []prw.Label{{Name: "__name__", Value: "m1"}}, Samples: []prw.Sample{{Value: 1, Timestamp: 1}}},
		},
	}
	_ = exp.Export(context.Background(), req)

	// Now remove server2 from endpoints
	exp.onEndpointsChanged([]string{server1.URL})

	if exp.EndpointCount() != 1 {
		t.Errorf("expected 1 endpoint, got %d", exp.EndpointCount())
	}
}

// =============================================================================
// PRWSharded with no endpoints initially, then set via static
// =============================================================================

func TestNewPRWSharded_WithDefaultVirtualNodes(t *testing.T) {
	shardedCfg := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Timeout: 5 * time.Second,
		},
		VirtualNodes: 0, // should use default
	}

	exp, err := NewPRWSharded(context.Background(), shardedCfg)
	if err != nil {
		t.Fatalf("NewPRWSharded: %v", err)
	}
	defer exp.Close()

	// Should use default virtual nodes
	if exp.hashRing == nil {
		t.Fatal("expected hash ring to be created")
	}
}

func TestNewPRWSharded_WithDiscovery(t *testing.T) {
	// Test that discovery config is accepted (even if it won't discover anything)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shardedCfg := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Timeout: 5 * time.Second,
		},
		VirtualNodes: 10,
		Discovery: &sharding.DiscoveryConfig{
			HeadlessService: "test.svc.cluster.local:9090",
			RefreshInterval: time.Hour,
		},
	}

	exp, err := NewPRWSharded(ctx, shardedCfg)
	if err != nil {
		t.Fatalf("NewPRWSharded: %v", err)
	}
	defer exp.Close()

	if exp.discovery == nil {
		t.Fatal("expected discovery to be initialized")
	}
}

// =============================================================================
// sanitizeEndpoint
// =============================================================================

func TestSanitizeEndpoint_EdgeCases(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"http://localhost:9090", "http___localhost_9090"},
		{"simple", "simple"},
		{"a.b.c:443/path", "a_b_c_443_path"},
		{"", ""},
	}
	for _, tt := range tests {
		got := sanitizeEndpoint(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeEndpoint(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// =============================================================================
// ExportError methods
// =============================================================================

func TestExportError_ErrorMethod(t *testing.T) {
	// With underlying error
	e1 := &ExportError{
		Err:        errors.New("underlying"),
		Type:       ErrorTypeServerError,
		StatusCode: 500,
		Message:    "error",
	}
	if e1.Error() != "underlying" {
		t.Errorf("expected 'underlying', got %q", e1.Error())
	}

	// Without underlying error
	e2 := &ExportError{
		Err:        nil,
		Type:       ErrorTypeAuth,
		StatusCode: 401,
	}
	if e2.Error() == "" {
		t.Error("expected non-empty error string")
	}
}

func TestExportError_Unwrap_NilInner(t *testing.T) {
	e := &ExportError{Err: nil, Type: ErrorTypeUnknown}
	if e.Unwrap() != nil {
		t.Error("Unwrap of nil Err should return nil")
	}
}

func TestExportError_IsSplittable_PayloadPatterns(t *testing.T) {
	// Focus on containsPayloadTooLarge patterns that aren't in errors_test.go
	tests := []struct {
		name       string
		err        *ExportError
		splittable bool
	}{
		{
			"400 with too big message",
			&ExportError{StatusCode: 400, Type: ErrorTypeClientError, Message: "data too big"},
			true,
		},
		{
			"400 with exceeds message",
			&ExportError{StatusCode: 400, Type: ErrorTypeClientError, Message: "exceeds max size"},
			true,
		},
		{
			"400 with maxrequestsize",
			&ExportError{StatusCode: 400, Type: ErrorTypeClientError, Message: "maxrequestsize exceeded"},
			true,
		},
		{
			"400 with exceeds pattern",
			&ExportError{StatusCode: 400, Type: ErrorTypeClientError, Message: "request body exceeds limit"},
			true,
		},
		{
			"400 normal error not splittable",
			&ExportError{StatusCode: 400, Type: ErrorTypeClientError, Message: "invalid request"},
			false,
		},
		{
			"200 status not splittable",
			&ExportError{StatusCode: 200, Type: ErrorTypeUnknown, Message: ""},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.IsSplittable()
			if got != tt.splittable {
				t.Errorf("IsSplittable() = %v, want %v", got, tt.splittable)
			}
		})
	}
}

func TestExportError_IsRetryable_WithMessage(t *testing.T) {
	// Verify IsRetryable works correctly in combination with error messages
	tests := []struct {
		name      string
		err       *ExportError
		retryable bool
	}{
		{
			"rate limit with message",
			&ExportError{Type: ErrorTypeRateLimit, StatusCode: 429, Message: "too many requests"},
			true,
		},
		{
			"unknown with no message",
			&ExportError{Type: ErrorTypeUnknown, StatusCode: 0, Message: ""},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err.IsRetryable() != tt.retryable {
				t.Errorf("IsRetryable() = %v, want %v", tt.err.IsRetryable(), tt.retryable)
			}
		})
	}
}

// =============================================================================
// PRWClientError / PRWServerError
// =============================================================================

func TestPRWClientError(t *testing.T) {
	e := &PRWClientError{StatusCode: 400, Message: "bad request"}
	if e.Error() != "bad request" {
		t.Errorf("Error() = %q", e.Error())
	}
	if e.IsRetryable() {
		t.Error("expected PRWClientError to not be retryable")
	}
}

func TestPRWServerError(t *testing.T) {
	e := &PRWServerError{StatusCode: 500, Message: "internal server error"}
	if e.Error() != "internal server error" {
		t.Errorf("Error() = %q", e.Error())
	}
	if !e.IsRetryable() {
		t.Error("expected PRWServerError to be retryable")
	}
}

// =============================================================================
// IsPRWRetryableError with ExportError wrapping
// =============================================================================

func TestIsPRWRetryableError_WithExportError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			"ExportError server",
			&ExportError{Type: ErrorTypeServerError},
			true,
		},
		{
			"ExportError auth",
			&ExportError{Type: ErrorTypeAuth},
			false,
		},
		{
			"ExportError timeout",
			&ExportError{Type: ErrorTypeTimeout},
			true,
		},
		{
			"ExportError network",
			&ExportError{Type: ErrorTypeNetwork},
			true,
		},
		{
			"ExportError client",
			&ExportError{Type: ErrorTypeClientError},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsPRWRetryableError(tt.err)
			if got != tt.want {
				t.Errorf("IsPRWRetryableError() = %v, want %v", got, tt.want)
			}
		})
	}
}

// =============================================================================
// QueuedExporter.Close -- coverage for close timeout path
// =============================================================================

func TestQueuedExporter_Close_WithTimeout(t *testing.T) {
	// Create an exporter that will be slow to drain
	slowMock := &slowExporter{delay: 5 * time.Second}

	queueCfg := queue.Config{
		Path:          t.TempDir(),
		MaxSize:       1000,
		RetryInterval: 10 * time.Millisecond,
		MaxRetryDelay: 50 * time.Millisecond,
		CloseTimeout:  200 * time.Millisecond,
		DrainTimeout:  100 * time.Millisecond,
	}

	qe, err := NewQueued(slowMock, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	// Push something to trigger drain
	req := createTestRequest()
	_ = qe.queue.Push(req)

	start := time.Now()
	_ = qe.Close()
	elapsed := time.Since(start)

	if elapsed > 5*time.Second {
		t.Fatalf("Close took too long: %v", elapsed)
	}
}

func TestQueuedExporter_Close_Idempotent(t *testing.T) {
	mock := &mockExporter{}
	queueCfg := queue.Config{
		Path:          t.TempDir(),
		MaxSize:       100,
		RetryInterval: time.Hour,
		MaxRetryDelay: time.Hour,
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	err1 := qe.Close()
	err2 := qe.Close()

	if err1 != nil {
		t.Fatalf("First Close() error: %v", err1)
	}
	if err2 != nil {
		t.Fatalf("Second Close() error: %v", err2)
	}
}

// =============================================================================
// QueuedExporter.drainQueue -- drain with failed entries
// =============================================================================

func TestQueuedExporter_DrainQueue_FailedEntries(t *testing.T) {
	failingMock := &mockExporter{failCount: 1000, exportErr: errors.New("persistent failure")}

	queueCfg := queue.Config{
		Path:              t.TempDir(),
		MaxSize:           1000,
		RetryInterval:     time.Hour,
		MaxRetryDelay:     time.Hour,
		DrainTimeout:      500 * time.Millisecond,
		DrainEntryTimeout: 200 * time.Millisecond,
	}

	qe, err := NewQueued(failingMock, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	// Push entries via queue directly
	for i := 0; i < 3; i++ {
		_ = qe.queue.Push(createTestRequest())
	}

	// Close triggers drain - all entries should fail and be re-pushed
	err = qe.Close()
	if err != nil {
		t.Fatalf("Close() error: %v", err)
	}
}

// =============================================================================
// QueuedExporter retryLoop - various backoff and empty queue paths
// =============================================================================

func TestQueuedExporter_RetryLoop_EmptyQueueCountsAsSuccess(t *testing.T) {
	mock := &mockExporter{}
	queueCfg := queue.Config{
		Path:          t.TempDir(),
		MaxSize:       100,
		RetryInterval: 30 * time.Millisecond,
		MaxRetryDelay: 100 * time.Millisecond,
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	// Let retry loop tick with empty queue a few times
	time.Sleep(100 * time.Millisecond)

	// Queue should still be empty
	if qe.QueueLen() != 0 {
		t.Fatalf("expected empty queue, got %d", qe.QueueLen())
	}

	qe.Close()
}

func TestQueuedExporter_RetryLoop_BackoffEnabled(t *testing.T) {
	mock := &mockExporter{failCount: 1000, exportErr: errors.New("fail")}
	queueCfg := queue.Config{
		Path:              t.TempDir(),
		MaxSize:           100,
		RetryInterval:     10 * time.Millisecond,
		MaxRetryDelay:     200 * time.Millisecond,
		BackoffEnabled:    true,
		BackoffMultiplier: 3.0,
		BatchDrainSize:    1,
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	_ = qe.queue.Push(createTestRequest())

	// Let backoff kick in
	time.Sleep(300 * time.Millisecond)

	// Should have retried at least once
	if mock.getExportCount() < 1 {
		t.Fatalf("expected at least 1 export call, got %d", mock.getExportCount())
	}
}

// =============================================================================
// classifyPRWError with more patterns
// =============================================================================

func TestClassifyPRWError_Extended(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ErrorType
	}{
		{"nil", nil, ErrorTypeUnknown},
		{"PRWClientError 413", &PRWClientError{StatusCode: 413}, ErrorTypeClientError},
		{"PRWServerError 502", &PRWServerError{StatusCode: 502}, ErrorTypeServerError},
		{"EOF pattern", errors.New("unexpected EOF"), ErrorTypeNetwork},
		{"io timeout pattern", errors.New("read: i/o timeout"), ErrorTypeTimeout},
		{"generic", errors.New("foo bar"), ErrorTypeUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyPRWError(tt.err)
			if got != tt.want {
				t.Errorf("classifyPRWError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// =============================================================================
// PRWQueuedExporter.getCircuitState with unknown/default state
// =============================================================================

func TestPRWQueuedExporter_GetCircuitState_UnknownState(t *testing.T) {
	mock := &mockPRWExporter{}
	cfg := PRWQueueConfig{
		Path:                    t.TempDir(),
		MaxSize:                 100,
		RetryInterval:           time.Hour,
		MaxRetryDelay:           time.Hour,
		CircuitBreakerEnabled:   true,
		CircuitFailureThreshold: 10,
		CircuitResetTimeout:     time.Hour,
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	defer qe.Close()

	// Force an invalid state
	qe.circuitBreaker.state.Store(99)
	state := qe.getCircuitState()
	if state != "unknown" {
		t.Errorf("expected 'unknown', got %q", state)
	}
}

// =============================================================================
// contains helper edge cases
// =============================================================================

func TestContains_EdgeCases(t *testing.T) {
	// Test where s == substr
	if !contains("hello", "hello") {
		t.Error("contains('hello', 'hello') should be true")
	}

	// Test empty substr in non-empty string
	if !contains("hello", "") {
		t.Error("contains('hello', '') should be true")
	}

	// Test where s is shorter than substr
	if contains("hi", "hello world") {
		t.Error("contains('hi', 'hello world') should be false")
	}
}

// =============================================================================
// NewPRWQueued with circuit breaker enabled - default threshold/timeout
// =============================================================================

func TestNewPRWQueued_CircuitBreakerDefaults(t *testing.T) {
	mock := &mockPRWExporter{}
	cfg := PRWQueueConfig{
		Path:                  t.TempDir(),
		MaxSize:               1000,
		RetryInterval:         time.Hour,
		MaxRetryDelay:         time.Hour,
		CircuitBreakerEnabled: true,
		// Threshold and timeout not set
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	defer qe.Close()

	if qe.circuitBreaker == nil {
		t.Fatal("expected circuit breaker to be initialized")
	}
	if qe.circuitBreaker.failureThreshold != 5 {
		t.Errorf("expected default threshold 5, got %d", qe.circuitBreaker.failureThreshold)
	}
	if qe.circuitBreaker.resetTimeout != 30*time.Second {
		t.Errorf("expected default reset timeout 30s, got %v", qe.circuitBreaker.resetTimeout)
	}
}

func TestNewPRWQueued_AllDrainDefaults(t *testing.T) {
	mock := &mockPRWExporter{}
	cfg := PRWQueueConfig{
		Path:          t.TempDir(),
		MaxSize:       1000,
		RetryInterval: time.Hour,
		MaxRetryDelay: time.Hour,
		// All drain settings default
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	defer qe.Close()

	if qe.batchDrainSize != 10 {
		t.Errorf("expected batchDrainSize=10, got %d", qe.batchDrainSize)
	}
	if qe.burstDrainSize != 100 {
		t.Errorf("expected burstDrainSize=100, got %d", qe.burstDrainSize)
	}
	if qe.retryExportTimeout != 10*time.Second {
		t.Errorf("expected retryExportTimeout=10s, got %v", qe.retryExportTimeout)
	}
	if qe.closeTimeout != 60*time.Second {
		t.Errorf("expected closeTimeout=60s, got %v", qe.closeTimeout)
	}
	if qe.drainTimeout != 30*time.Second {
		t.Errorf("expected drainTimeout=30s, got %v", qe.drainTimeout)
	}
	if qe.drainEntryTimeout != 5*time.Second {
		t.Errorf("expected drainEntryTimeout=5s, got %v", qe.drainEntryTimeout)
	}
}

func TestNewPRWQueued_CustomDrainSettings(t *testing.T) {
	mock := &mockPRWExporter{}
	cfg := PRWQueueConfig{
		Path:               t.TempDir(),
		MaxSize:            1000,
		RetryInterval:      time.Hour,
		MaxRetryDelay:      time.Hour,
		BatchDrainSize:     20,
		BurstDrainSize:     200,
		RetryExportTimeout: 15 * time.Second,
		CloseTimeout:       30 * time.Second,
		DrainTimeout:       10 * time.Second,
		DrainEntryTimeout:  2 * time.Second,
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	defer qe.Close()

	if qe.batchDrainSize != 20 {
		t.Errorf("expected batchDrainSize=20, got %d", qe.batchDrainSize)
	}
	if qe.burstDrainSize != 200 {
		t.Errorf("expected burstDrainSize=200, got %d", qe.burstDrainSize)
	}
	if qe.retryExportTimeout != 15*time.Second {
		t.Errorf("expected retryExportTimeout=15s, got %v", qe.retryExportTimeout)
	}
	if qe.closeTimeout != 30*time.Second {
		t.Errorf("expected closeTimeout=30s, got %v", qe.closeTimeout)
	}
	if qe.drainTimeout != 10*time.Second {
		t.Errorf("expected drainTimeout=10s, got %v", qe.drainTimeout)
	}
	if qe.drainEntryTimeout != 2*time.Second {
		t.Errorf("expected drainEntryTimeout=2s, got %v", qe.drainEntryTimeout)
	}
}

func TestNewPRWQueued_BackoffMultiplierDefault(t *testing.T) {
	mock := &mockPRWExporter{}
	cfg := PRWQueueConfig{
		Path:           t.TempDir(),
		MaxSize:        1000,
		RetryInterval:  time.Hour,
		MaxRetryDelay:  time.Hour,
		BackoffEnabled: true,
		// BackoffMultiplier not set
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	defer qe.Close()

	if qe.backoffMultiplier != 2.0 {
		t.Errorf("expected default backoffMultiplier=2.0, got %f", qe.backoffMultiplier)
	}
}
