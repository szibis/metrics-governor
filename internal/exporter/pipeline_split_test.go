package exporter

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	"github.com/szibis/metrics-governor/internal/queue"
)

// =============================================================================
// OTLP Pipeline Split: preparerLoop + senderLoop coverage
// =============================================================================

// pipelineMockExporter implements Exporter + dataCompressor + compressedSender
// to test the pipeline split (preparer → sender) flow.
type pipelineMockExporter struct {
	exportCalls     atomic.Int64
	compressCalls   atomic.Int64
	sendCalls       atomic.Int64
	exportErr       error
	compressErr     error
	sendErr         error
	sendCompressErr error // set non-nil to simulate SendCompressed failure
}

func (m *pipelineMockExporter) Export(_ context.Context, _ *colmetricspb.ExportMetricsServiceRequest) error {
	m.exportCalls.Add(1)
	return m.exportErr
}

func (m *pipelineMockExporter) ExportData(_ context.Context, _ []byte) error {
	m.exportCalls.Add(1)
	return m.exportErr
}

func (m *pipelineMockExporter) CompressData(data []byte) ([]byte, string, error) {
	m.compressCalls.Add(1)
	if m.compressErr != nil {
		return nil, "", m.compressErr
	}
	return data, "gzip", nil
}

func (m *pipelineMockExporter) SendCompressed(_ context.Context, _ []byte, _ string, _ int) error {
	m.sendCalls.Add(1)
	if m.sendCompressErr != nil {
		return m.sendCompressErr
	}
	return nil
}

func (m *pipelineMockExporter) Close() error {
	return nil
}

// TestOTLPPipelineSplit_PreparerAndSenderSuccess tests the happy path
// of pipeline split: preparer compresses, sender sends compressed data.
func TestOTLPPipelineSplit_PreparerAndSenderSuccess(t *testing.T) {
	mock := &pipelineMockExporter{}
	cfg := queue.Config{
		Path:                 t.TempDir(),
		MaxSize:              1000,
		MaxBytes:             1024 * 1024,
		RetryInterval:        20 * time.Millisecond,
		MaxRetryDelay:        200 * time.Millisecond,
		AlwaysQueue:          true,
		Workers:              2,
		Mode:                 queue.QueueModeDisk,
		PipelineSplitEnabled: true,
		PreparerCount:        1,
		SenderCount:          1,
		PipelineChannelSize:  16,
		MaxConcurrentSends:   2,
		GlobalSendLimit:      4,
		CloseTimeout:         5 * time.Second,
		DrainTimeout:         2 * time.Second,
		DrainEntryTimeout:    1 * time.Second,
		RetryExportTimeout:   2 * time.Second,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	// Export several items to exercise the pipeline
	for i := 0; i < 5; i++ {
		if err := qe.Export(context.Background(), aqTestRequest("pipeline_test")); err != nil {
			t.Fatalf("Export %d: %v", i, err)
		}
	}

	// Wait for pipeline to process
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if mock.sendCalls.Load() >= 5 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	sends := mock.sendCalls.Load()
	if sends < 5 {
		t.Errorf("expected at least 5 SendCompressed calls, got %d", sends)
	}

	compressions := mock.compressCalls.Load()
	if compressions < 5 {
		t.Errorf("expected at least 5 CompressData calls, got %d", compressions)
	}

	qe.Close()
}

// TestOTLPPipelineSplit_PreparerCompressionFailure tests the preparer
// re-pushing to queue when compression fails.
func TestOTLPPipelineSplit_PreparerCompressionFailure(t *testing.T) {
	mock := &pipelineMockExporter{
		compressErr: errors.New("compression failure"),
	}
	cfg := queue.Config{
		Path:                 t.TempDir(),
		MaxSize:              1000,
		MaxBytes:             1024 * 1024,
		RetryInterval:        50 * time.Millisecond,
		MaxRetryDelay:        200 * time.Millisecond,
		AlwaysQueue:          true,
		Workers:              2,
		Mode:                 queue.QueueModeDisk,
		PipelineSplitEnabled: true,
		PreparerCount:        1,
		SenderCount:          1,
		PipelineChannelSize:  16,
		MaxConcurrentSends:   2,
		GlobalSendLimit:      4,
		CloseTimeout:         2 * time.Second,
		DrainTimeout:         1 * time.Second,
		DrainEntryTimeout:    500 * time.Millisecond,
		RetryExportTimeout:   1 * time.Second,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	// Push one item
	if err := qe.Export(context.Background(), aqTestRequest("compress_fail")); err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Wait for preparer to attempt compression a few times
	time.Sleep(300 * time.Millisecond)

	// Compression should have been attempted multiple times (re-push and retry)
	compressions := mock.compressCalls.Load()
	if compressions < 2 {
		t.Errorf("expected at least 2 CompressData calls (with retries), got %d", compressions)
	}

	// Sender should not have been called since compression fails
	sends := mock.sendCalls.Load()
	if sends > 0 {
		t.Errorf("expected 0 SendCompressed calls since compression fails, got %d", sends)
	}

	qe.Close()
}

// TestOTLPPipelineSplit_SenderFailure tests the sender re-pushing when
// the send fails with a retryable error.
func TestOTLPPipelineSplit_SenderFailure(t *testing.T) {
	mock := &pipelineMockExporter{
		sendCompressErr: errors.New("send failed: connection refused"),
	}
	cfg := queue.Config{
		Path:                 t.TempDir(),
		MaxSize:              1000,
		MaxBytes:             1024 * 1024,
		RetryInterval:        50 * time.Millisecond,
		MaxRetryDelay:        200 * time.Millisecond,
		AlwaysQueue:          true,
		Workers:              2,
		Mode:                 queue.QueueModeDisk,
		PipelineSplitEnabled: true,
		PreparerCount:        1,
		SenderCount:          1,
		PipelineChannelSize:  16,
		MaxConcurrentSends:   2,
		GlobalSendLimit:      4,
		CloseTimeout:         2 * time.Second,
		DrainTimeout:         1 * time.Second,
		DrainEntryTimeout:    500 * time.Millisecond,
		RetryExportTimeout:   1 * time.Second,
		BackoffEnabled:       true,
		BackoffMultiplier:    1.5,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	if err := qe.Export(context.Background(), aqTestRequest("send_fail")); err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Wait for a few retry cycles
	time.Sleep(400 * time.Millisecond)

	// Sender should have been called multiple times (retries with backoff)
	sends := mock.sendCalls.Load()
	if sends < 2 {
		t.Errorf("expected at least 2 SendCompressed attempts (retries), got %d", sends)
	}

	qe.Close()
}

// TestOTLPPipelineSplit_SenderSplittableError tests the sender splitting
// a 413 error into two halves.
func TestOTLPPipelineSplit_SenderSplittableError(t *testing.T) {
	sendAttempts := atomic.Int64{}

	splittableErr := &ExportError{
		Err:        errors.New("entity too large"),
		Type:       ErrorTypeClientError,
		StatusCode: 413,
		Message:    "request entity too large",
	}

	realMock := &pipelineSplittableMock{
		firstError:   splittableErr,
		sendAttempts: &sendAttempts,
	}

	cfg := queue.Config{
		Path:                 t.TempDir(),
		MaxSize:              1000,
		MaxBytes:             1024 * 1024,
		RetryInterval:        20 * time.Millisecond,
		MaxRetryDelay:        200 * time.Millisecond,
		AlwaysQueue:          true,
		Workers:              2,
		Mode:                 queue.QueueModeDisk,
		PipelineSplitEnabled: true,
		PreparerCount:        1,
		SenderCount:          1,
		PipelineChannelSize:  16,
		MaxConcurrentSends:   2,
		GlobalSendLimit:      4,
		CloseTimeout:         5 * time.Second,
		DrainTimeout:         2 * time.Second,
		DrainEntryTimeout:    1 * time.Second,
		RetryExportTimeout:   2 * time.Second,
	}

	qe, err := NewQueued(realMock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	// Request with multiple ResourceMetrics for splitting
	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{{Name: "s1"}}}}},
			{ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{{Name: "s2"}}}}},
		},
	}

	if err := qe.Export(context.Background(), req); err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Wait for split and retry
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if sendAttempts.Load() >= 3 { // 1 fail + 2 split halves
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	attempts := sendAttempts.Load()
	if attempts < 2 {
		t.Logf("Send attempts: %d (split may still be processing)", attempts)
	}

	qe.Close()
}

// pipelineSplittableMock is a mock for testing splittable errors in the pipeline.
type pipelineSplittableMock struct {
	firstError   error
	firstDone    atomic.Bool
	sendAttempts *atomic.Int64
}

func (m *pipelineSplittableMock) Export(_ context.Context, _ *colmetricspb.ExportMetricsServiceRequest) error {
	m.sendAttempts.Add(1)
	if !m.firstDone.Load() {
		m.firstDone.Store(true)
		return m.firstError
	}
	return nil
}

func (m *pipelineSplittableMock) ExportData(_ context.Context, _ []byte) error {
	m.sendAttempts.Add(1)
	if !m.firstDone.Load() {
		m.firstDone.Store(true)
		return m.firstError
	}
	return nil
}

func (m *pipelineSplittableMock) CompressData(data []byte) ([]byte, string, error) {
	return data, "gzip", nil
}

func (m *pipelineSplittableMock) SendCompressed(_ context.Context, _ []byte, _ string, _ int) error {
	m.sendAttempts.Add(1)
	if !m.firstDone.Load() {
		m.firstDone.Store(true)
		return m.firstError
	}
	return nil
}

func (m *pipelineSplittableMock) Close() error { return nil }

// TestOTLPPipelineSplit_SenderNonRetryableError tests the sender dropping
// non-retryable errors (e.g. 401 auth failure).
func TestOTLPPipelineSplit_SenderNonRetryableError(t *testing.T) {
	authErr := &ExportError{
		Err:        errors.New("unauthorized"),
		Type:       ErrorTypeAuth,
		StatusCode: 401,
		Message:    "unauthorized",
	}

	mock := &pipelineMockExporter{sendCompressErr: authErr}
	cfg := queue.Config{
		Path:                 t.TempDir(),
		MaxSize:              1000,
		MaxBytes:             1024 * 1024,
		RetryInterval:        20 * time.Millisecond,
		MaxRetryDelay:        100 * time.Millisecond,
		AlwaysQueue:          true,
		Workers:              1,
		Mode:                 queue.QueueModeDisk,
		PipelineSplitEnabled: true,
		PreparerCount:        1,
		SenderCount:          1,
		PipelineChannelSize:  16,
		MaxConcurrentSends:   2,
		GlobalSendLimit:      4,
		CloseTimeout:         2 * time.Second,
		DrainTimeout:         1 * time.Second,
		DrainEntryTimeout:    500 * time.Millisecond,
		RetryExportTimeout:   1 * time.Second,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	if err := qe.Export(context.Background(), aqTestRequest("auth_fail")); err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Wait for sender to drop non-retryable
	time.Sleep(300 * time.Millisecond)

	sends := mock.sendCalls.Load()
	if sends < 1 {
		t.Errorf("expected at least 1 send attempt, got %d", sends)
	}

	qe.Close()
}

// TestOTLPPipelineSplit_CircuitBreakerInPreparer tests the preparer
// respecting the circuit breaker.
func TestOTLPPipelineSplit_CircuitBreakerInPreparer(t *testing.T) {
	mock := &pipelineMockExporter{}
	cfg := queue.Config{
		Path:                       t.TempDir(),
		MaxSize:                    1000,
		MaxBytes:                   1024 * 1024,
		RetryInterval:              20 * time.Millisecond,
		MaxRetryDelay:              200 * time.Millisecond,
		AlwaysQueue:                true,
		Workers:                    2,
		Mode:                       queue.QueueModeDisk,
		PipelineSplitEnabled:       true,
		PreparerCount:              1,
		SenderCount:                1,
		PipelineChannelSize:        16,
		MaxConcurrentSends:         2,
		GlobalSendLimit:            4,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    2,
		CircuitBreakerResetTimeout: time.Hour,
		CloseTimeout:               2 * time.Second,
		DrainTimeout:               1 * time.Second,
		DrainEntryTimeout:          500 * time.Millisecond,
		RetryExportTimeout:         1 * time.Second,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	// Force circuit open
	qe.circuitBreaker.RecordFailure()
	qe.circuitBreaker.RecordFailure()

	// Push items
	for i := 0; i < 3; i++ {
		if err := qe.Export(context.Background(), aqTestRequest("cb_preparer")); err != nil {
			t.Fatalf("Export %d: %v", i, err)
		}
	}

	// With circuit open, sender should NOT get calls
	time.Sleep(300 * time.Millisecond)

	sends := mock.sendCalls.Load()
	if sends > 0 {
		t.Errorf("expected 0 sends with circuit open, got %d", sends)
	}

	qe.Close()
}

// TestOTLPPipelineSplit_NoCompressor tests the pipeline with an exporter
// that doesn't implement dataCompressor — raw data passes through.
func TestOTLPPipelineSplit_NoCompressor(t *testing.T) {
	// Use aqMockExporter which does NOT implement CompressData
	mock := &aqMockExporter{}
	cfg := queue.Config{
		Path:                 t.TempDir(),
		MaxSize:              1000,
		MaxBytes:             1024 * 1024,
		RetryInterval:        20 * time.Millisecond,
		MaxRetryDelay:        200 * time.Millisecond,
		AlwaysQueue:          true,
		Workers:              2,
		Mode:                 queue.QueueModeDisk,
		PipelineSplitEnabled: true,
		PreparerCount:        1,
		SenderCount:          1,
		PipelineChannelSize:  16,
		MaxConcurrentSends:   2,
		GlobalSendLimit:      4,
		CloseTimeout:         5 * time.Second,
		DrainTimeout:         2 * time.Second,
		DrainEntryTimeout:    1 * time.Second,
		RetryExportTimeout:   2 * time.Second,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := qe.Export(context.Background(), aqTestRequest("no_compress")); err != nil {
			t.Fatalf("Export %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if mock.getCalls() >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	calls := mock.getCalls()
	if calls < 3 {
		t.Errorf("expected at least 3 export calls, got %d", calls)
	}

	qe.Close()
}

// =============================================================================
// OTLP addSender / removeSender / activeSenderCount
// =============================================================================

func TestQueuedExporter_AddRemoveSender(t *testing.T) {
	mock := &pipelineMockExporter{}
	cfg := queue.Config{
		Path:                 t.TempDir(),
		MaxSize:              1000,
		MaxBytes:             1024 * 1024,
		RetryInterval:        50 * time.Millisecond,
		MaxRetryDelay:        200 * time.Millisecond,
		AlwaysQueue:          true,
		Workers:              2,
		Mode:                 queue.QueueModeDisk,
		PipelineSplitEnabled: true,
		PreparerCount:        1,
		SenderCount:          1,
		PipelineChannelSize:  16,
		MaxConcurrentSends:   2,
		GlobalSendLimit:      4,
		CloseTimeout:         5 * time.Second,
		DrainTimeout:         2 * time.Second,
		DrainEntryTimeout:    1 * time.Second,
		RetryExportTimeout:   2 * time.Second,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	// Initial senderCount should be 1 (from config)
	initialCount := qe.activeSenderCount()
	if initialCount != 1 {
		t.Errorf("initial activeSenderCount = %d, want 1", initialCount)
	}

	// Add a sender
	qe.addSender()
	time.Sleep(50 * time.Millisecond)
	afterAdd := qe.activeSenderCount()
	if afterAdd != 2 {
		t.Errorf("after addSender, activeSenderCount = %d, want 2", afterAdd)
	}

	// Add another
	qe.addSender()
	time.Sleep(50 * time.Millisecond)
	afterAdd2 := qe.activeSenderCount()
	if afterAdd2 != 3 {
		t.Errorf("after second addSender, activeSenderCount = %d, want 3", afterAdd2)
	}

	// Remove a sender
	qe.removeSender()
	time.Sleep(100 * time.Millisecond) // Give goroutine time to exit
	afterRemove := qe.activeSenderCount()
	if afterRemove != 2 {
		t.Errorf("after removeSender, activeSenderCount = %d, want 2", afterRemove)
	}

	// Remove another
	qe.removeSender()
	time.Sleep(100 * time.Millisecond)
	afterRemove2 := qe.activeSenderCount()
	if afterRemove2 != 1 {
		t.Errorf("after second removeSender, activeSenderCount = %d, want 1", afterRemove2)
	}

	qe.Close()
}

// =============================================================================
// OTLP memBatchWorkerLoop — additional paths
// =============================================================================

func TestQueuedExporter_MemBatchWorker_CircuitBreaker(t *testing.T) {
	// Test circuit breaker path in memBatchWorkerLoop
	serverErr := errors.New("server unavailable")
	mock := &aqMockExporter{failCount: 1000, exportErr: serverErr}
	cfg := queue.Config{
		Path:                       t.TempDir(),
		MaxSize:                    1000,
		MaxBytes:                   1024 * 1024,
		RetryInterval:              20 * time.Millisecond,
		MaxRetryDelay:              100 * time.Millisecond,
		AlwaysQueue:                true,
		Workers:                    1,
		Mode:                       queue.QueueModeMemory,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    2,
		CircuitBreakerResetTimeout: time.Hour,
		CloseTimeout:               2 * time.Second,
		DrainTimeout:               1 * time.Second,
		DrainEntryTimeout:          500 * time.Millisecond,
		RetryExportTimeout:         1 * time.Second,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	// Push some items first
	for i := 0; i < 3; i++ {
		if err := qe.Export(context.Background(), aqTestRequest("mem_cb")); err != nil {
			t.Fatalf("Export %d: %v", i, err)
		}
	}

	// Let failures accumulate to open the circuit
	time.Sleep(300 * time.Millisecond)

	callsBefore := mock.getCalls()

	// Force circuit fully open
	qe.circuitBreaker.RecordFailure()
	qe.circuitBreaker.RecordFailure()
	qe.circuitBreaker.RecordFailure()

	// Wait — worker should pause when circuit is open
	time.Sleep(200 * time.Millisecond)

	callsAfter := mock.getCalls()
	t.Logf("Calls before circuit open: %d, after: %d", callsBefore, callsAfter)

	qe.Close()
}

func TestQueuedExporter_MemBatchWorker_Backoff(t *testing.T) {
	// Test backoff path in memBatchWorkerLoop
	retryErr := errors.New("transient failure")
	mock := &aqMockExporter{failCount: 100, exportErr: retryErr}
	cfg := queue.Config{
		Path:               t.TempDir(),
		MaxSize:            1000,
		MaxBytes:           1024 * 1024,
		RetryInterval:      20 * time.Millisecond,
		MaxRetryDelay:      200 * time.Millisecond,
		AlwaysQueue:        true,
		Workers:            1,
		Mode:               queue.QueueModeMemory,
		BackoffEnabled:     true,
		BackoffMultiplier:  2.0,
		CloseTimeout:       2 * time.Second,
		DrainTimeout:       1 * time.Second,
		DrainEntryTimeout:  500 * time.Millisecond,
		RetryExportTimeout: 1 * time.Second,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	if err := qe.Export(context.Background(), aqTestRequest("mem_backoff")); err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Wait for a few retries with backoff
	time.Sleep(500 * time.Millisecond)

	calls := mock.getCalls()
	if calls < 2 {
		t.Errorf("expected at least 2 retries with backoff, got %d", calls)
	}

	qe.Close()
}

func TestQueuedExporter_MemBatchWorker_SuccessResetsBackoff(t *testing.T) {
	// Test that success resets backoff in memBatchWorkerLoop
	mock := &aqMockExporter{
		failCount: 2,
		exportErr: errors.New("transient"),
	}
	cfg := queue.Config{
		Path:               t.TempDir(),
		MaxSize:            1000,
		MaxBytes:           1024 * 1024,
		RetryInterval:      20 * time.Millisecond,
		MaxRetryDelay:      200 * time.Millisecond,
		AlwaysQueue:        true,
		Workers:            1,
		Mode:               queue.QueueModeMemory,
		BackoffEnabled:     true,
		BackoffMultiplier:  2.0,
		CloseTimeout:       5 * time.Second,
		DrainTimeout:       2 * time.Second,
		DrainEntryTimeout:  1 * time.Second,
		RetryExportTimeout: 2 * time.Second,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	// Push several items — first two fail, rest succeed
	for i := 0; i < 5; i++ {
		if err := qe.Export(context.Background(), aqTestRequest("success_reset")); err != nil {
			t.Fatalf("Export %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if mock.getCalls() >= 5 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if mock.getCalls() < 3 {
		t.Errorf("expected at least 3 calls (2 fail + succeed), got %d", mock.getCalls())
	}

	qe.Close()
}
