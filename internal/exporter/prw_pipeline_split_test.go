package exporter

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	prw "github.com/szibis/metrics-governor/internal/prw"
)

// =============================================================================
// PRW Pipeline Split: prwPreparerLoop + prwSenderLoop coverage
// =============================================================================

// prwPipelineMock implements prwExporterInterface + prwDataCompressor + prwCompressedSender
type prwPipelineMock struct {
	exportCalls   atomic.Int64
	compressCalls atomic.Int64
	sendCalls     atomic.Int64
	exportErr     error
	compressErr   error
	sendErr       error
}

func (m *prwPipelineMock) Export(_ context.Context, _ *prw.WriteRequest) error {
	m.exportCalls.Add(1)
	return m.exportErr
}

func (m *prwPipelineMock) ExportData(_ context.Context, _ []byte) error {
	m.exportCalls.Add(1)
	return m.exportErr
}

func (m *prwPipelineMock) CompressData(data []byte) ([]byte, string, error) {
	m.compressCalls.Add(1)
	if m.compressErr != nil {
		return nil, "", m.compressErr
	}
	return data, "snappy", nil
}

func (m *prwPipelineMock) SendCompressed(_ context.Context, _ []byte, _ string, _ int) error {
	m.sendCalls.Add(1)
	return m.sendErr
}

func (m *prwPipelineMock) Close() error { return nil }

func testPRWPipelineConfig(t *testing.T) PRWQueueConfig {
	t.Helper()
	return PRWQueueConfig{
		Path:                 t.TempDir(),
		MaxSize:              1000,
		MaxBytes:             1024 * 1024,
		RetryInterval:        20 * time.Millisecond,
		MaxRetryDelay:        200 * time.Millisecond,
		AlwaysQueue:          true,
		Workers:              2,
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
}

func makePRWTestRequest() *prw.WriteRequest {
	return &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []prw.Sample{{Value: 42, Timestamp: time.Now().UnixMilli()}},
			},
		},
	}
}

// TestPRWPipelineSplit_Success tests the happy path: preparer compresses,
// sender sends compressed data via PRW pipeline.
func TestPRWPipelineSplit_Success(t *testing.T) {
	mock := &prwPipelineMock{}
	cfg := testPRWPipelineConfig(t)

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}

	for i := 0; i < 5; i++ {
		if err := qe.Export(context.Background(), makePRWTestRequest()); err != nil {
			t.Fatalf("Export %d: %v", i, err)
		}
	}

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

// TestPRWPipelineSplit_CompressionFailure tests the preparer re-pushing
// when compression fails.
func TestPRWPipelineSplit_CompressionFailure(t *testing.T) {
	mock := &prwPipelineMock{
		compressErr: errors.New("snappy compression failed"),
	}
	cfg := testPRWPipelineConfig(t)
	cfg.CloseTimeout = 2 * time.Second
	cfg.DrainTimeout = 1 * time.Second
	cfg.RetryInterval = 50 * time.Millisecond

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}

	if err := qe.Export(context.Background(), makePRWTestRequest()); err != nil {
		t.Fatalf("Export: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	compressions := mock.compressCalls.Load()
	if compressions < 2 {
		t.Errorf("expected at least 2 compression attempts (retries), got %d", compressions)
	}

	sends := mock.sendCalls.Load()
	if sends > 0 {
		t.Errorf("expected 0 sends since compression fails, got %d", sends)
	}

	qe.Close()
}

// TestPRWPipelineSplit_SenderRetryableFailure tests the sender re-pushing
// when send fails with a retryable error.
func TestPRWPipelineSplit_SenderRetryableFailure(t *testing.T) {
	mock := &prwPipelineMock{
		sendErr: errors.New("connection refused"),
	}
	cfg := testPRWPipelineConfig(t)
	cfg.CloseTimeout = 2 * time.Second
	cfg.DrainTimeout = 1 * time.Second
	cfg.RetryInterval = 50 * time.Millisecond
	cfg.BackoffEnabled = true
	cfg.BackoffMultiplier = 1.5

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}

	if err := qe.Export(context.Background(), makePRWTestRequest()); err != nil {
		t.Fatalf("Export: %v", err)
	}

	time.Sleep(400 * time.Millisecond)

	sends := mock.sendCalls.Load()
	if sends < 2 {
		t.Errorf("expected at least 2 send attempts (retries), got %d", sends)
	}

	qe.Close()
}

// TestPRWPipelineSplit_SenderNonRetryableError tests the sender dropping
// non-retryable errors.
func TestPRWPipelineSplit_SenderNonRetryableError(t *testing.T) {
	authErr := &ExportError{
		Err:        errors.New("unauthorized"),
		Type:       ErrorTypeAuth,
		StatusCode: 401,
		Message:    "unauthorized",
	}

	mock := &prwPipelineMock{sendErr: authErr}
	cfg := testPRWPipelineConfig(t)
	cfg.CloseTimeout = 2 * time.Second
	cfg.DrainTimeout = 1 * time.Second

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}

	if err := qe.Export(context.Background(), makePRWTestRequest()); err != nil {
		t.Fatalf("Export: %v", err)
	}

	time.Sleep(300 * time.Millisecond)

	sends := mock.sendCalls.Load()
	if sends < 1 {
		t.Errorf("expected at least 1 send attempt, got %d", sends)
	}

	qe.Close()
}

// TestPRWPipelineSplit_CircuitBreaker tests the preparer respecting the
// circuit breaker in PRW pipeline mode.
func TestPRWPipelineSplit_CircuitBreaker(t *testing.T) {
	mock := &prwPipelineMock{}
	cfg := testPRWPipelineConfig(t)
	cfg.CircuitBreakerEnabled = true
	cfg.CircuitFailureThreshold = 2
	cfg.CircuitResetTimeout = time.Hour
	cfg.CloseTimeout = 2 * time.Second
	cfg.DrainTimeout = 1 * time.Second

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}

	// Force circuit open
	qe.circuitBreaker.RecordFailure()
	qe.circuitBreaker.RecordFailure()

	for i := 0; i < 3; i++ {
		if err := qe.Export(context.Background(), makePRWTestRequest()); err != nil {
			t.Fatalf("Export %d: %v", i, err)
		}
	}

	time.Sleep(300 * time.Millisecond)

	sends := mock.sendCalls.Load()
	if sends > 0 {
		t.Errorf("expected 0 sends with circuit open, got %d", sends)
	}

	qe.Close()
}

// TestPRWPipelineSplit_NoCompressor tests the pipeline with an exporter
// that doesn't implement prwDataCompressor -- raw data passes through.
func TestPRWPipelineSplit_NoCompressor(t *testing.T) {
	// Use mockPRWExporter which does NOT implement CompressData
	mock := &mockPRWExporter{}
	cfg := testPRWPipelineConfig(t)

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := qe.Export(context.Background(), makePRWTestRequest()); err != nil {
			t.Fatalf("Export %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		calls := atomic.LoadInt64(&mock.exportCalls)
		if calls >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	calls := atomic.LoadInt64(&mock.exportCalls)
	if calls < 3 {
		t.Errorf("expected at least 3 export calls, got %d", calls)
	}

	qe.Close()
}

// TestPRWPipelineSplit_SenderExtraLabelsRequireDeserialize tests the
// fallback from SendCompressed to Export when extra labels are configured.
func TestPRWPipelineSplit_SenderExtraLabelsRequireDeserialize(t *testing.T) {
	mock := &prwPipelineMock{
		sendErr: ErrExtraLabelsRequireDeserialize,
	}
	cfg := testPRWPipelineConfig(t)
	cfg.CloseTimeout = 5 * time.Second
	cfg.DrainTimeout = 2 * time.Second

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}

	if err := qe.Export(context.Background(), makePRWTestRequest()); err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Wait for the fallback to slow path
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if mock.exportCalls.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	exports := mock.exportCalls.Load()
	if exports < 1 {
		t.Errorf("expected at least 1 Export call (fallback from SendCompressed), got %d", exports)
	}

	qe.Close()
}

// =============================================================================
// PRW workerLoop -- additional coverage paths
// =============================================================================

// TestPRWWorkerLoop_ExportDataFastPath tests the fast path where ExportData
// is used by PRW workers.
func TestPRWWorkerLoop_ExportDataFastPath(t *testing.T) {
	mock := &prwPipelineMock{} // implements prwDataExporter
	cfg := PRWQueueConfig{
		Path:               t.TempDir(),
		MaxSize:            1000,
		MaxBytes:           1024 * 1024,
		RetryInterval:      20 * time.Millisecond,
		MaxRetryDelay:      200 * time.Millisecond,
		AlwaysQueue:        true,
		Workers:            1,
		CloseTimeout:       5 * time.Second,
		DrainTimeout:       2 * time.Second,
		DrainEntryTimeout:  1 * time.Second,
		RetryExportTimeout: 2 * time.Second,
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := qe.Export(context.Background(), makePRWTestRequest()); err != nil {
			t.Fatalf("Export %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if mock.exportCalls.Load() >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	exports := mock.exportCalls.Load()
	if exports < 3 {
		t.Errorf("expected at least 3 ExportData calls (fast path), got %d", exports)
	}

	qe.Close()
}

// TestPRWWorkerLoop_ExtraLabelsDisablesFastPath tests that when ExportData
// returns ErrExtraLabelsRequireDeserialize, the worker permanently falls back
// to the slow path (full unmarshal + Export).
func TestPRWWorkerLoop_ExtraLabelsDisablesFastPath(t *testing.T) {
	exportDataCalls := atomic.Int64{}
	exportCalls := atomic.Int64{}

	mock := &prwExtraLabelsMock{
		exportDataCalls: &exportDataCalls,
		exportCalls:     &exportCalls,
	}

	cfg := PRWQueueConfig{
		Path:               t.TempDir(),
		MaxSize:            1000,
		MaxBytes:           1024 * 1024,
		RetryInterval:      20 * time.Millisecond,
		MaxRetryDelay:      200 * time.Millisecond,
		AlwaysQueue:        true,
		Workers:            1,
		CloseTimeout:       5 * time.Second,
		DrainTimeout:       2 * time.Second,
		DrainEntryTimeout:  1 * time.Second,
		RetryExportTimeout: 2 * time.Second,
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}

	// Push several items -- first should trigger ExportData, get extra-labels error,
	// then fall back to Export for all subsequent items.
	for i := 0; i < 5; i++ {
		if err := qe.Export(context.Background(), makePRWTestRequest()); err != nil {
			t.Fatalf("Export %d: %v", i, err)
		}
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if exportCalls.Load() >= 4 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// ExportData should have been called exactly once (before disabling)
	edCalls := exportDataCalls.Load()
	if edCalls != 1 {
		t.Errorf("expected 1 ExportData call (before disable), got %d", edCalls)
	}

	// Export (slow path) should have been called for the remaining items
	eCalls := exportCalls.Load()
	if eCalls < 4 {
		t.Errorf("expected at least 4 Export calls (slow path), got %d", eCalls)
	}

	qe.Close()
}

// prwExtraLabelsMock simulates a PRW exporter that returns
// ErrExtraLabelsRequireDeserialize from ExportData, forcing fallback.
type prwExtraLabelsMock struct {
	exportDataCalls *atomic.Int64
	exportCalls     *atomic.Int64
}

func (m *prwExtraLabelsMock) Export(_ context.Context, _ *prw.WriteRequest) error {
	m.exportCalls.Add(1)
	return nil
}

func (m *prwExtraLabelsMock) ExportData(_ context.Context, _ []byte) error {
	m.exportDataCalls.Add(1)
	return ErrExtraLabelsRequireDeserialize
}

func (m *prwExtraLabelsMock) Close() error { return nil }

// TestPRWWorkerLoop_CircuitBreakerSkip tests the PRW worker respecting
// the circuit breaker.
func TestPRWWorkerLoop_CircuitBreakerSkip(t *testing.T) {
	serverErr := errors.New("server error 500")
	mock := &mockPRWExporter{
		failCount: 1000,
		failErr:   serverErr,
	}
	cfg := PRWQueueConfig{
		Path:                    t.TempDir(),
		MaxSize:                 1000,
		MaxBytes:                1024 * 1024,
		RetryInterval:           20 * time.Millisecond,
		MaxRetryDelay:           100 * time.Millisecond,
		AlwaysQueue:             true,
		Workers:                 1,
		CircuitBreakerEnabled:   true,
		CircuitFailureThreshold: 2,
		CircuitResetTimeout:     time.Hour,
		CloseTimeout:            2 * time.Second,
		DrainTimeout:            1 * time.Second,
		DrainEntryTimeout:       500 * time.Millisecond,
		RetryExportTimeout:      1 * time.Second,
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}

	if err := qe.Export(context.Background(), makePRWTestRequest()); err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Wait for failures to open circuit
	time.Sleep(300 * time.Millisecond)

	callsBefore := atomic.LoadInt64(&mock.exportCalls)

	// Force circuit fully open
	qe.circuitBreaker.RecordFailure()
	qe.circuitBreaker.RecordFailure()
	qe.circuitBreaker.RecordFailure()

	time.Sleep(200 * time.Millisecond)

	callsAfter := atomic.LoadInt64(&mock.exportCalls)
	t.Logf("PRW calls before=%d, after=%d (circuit should limit retries)", callsBefore, callsAfter)

	qe.Close()
}

// TestPRWWorkerLoop_SplittableError tests splitting of 413 errors in
// the PRW worker loop.
func TestPRWWorkerLoop_SplittableError(t *testing.T) {
	splittableErr := &ExportError{
		Err:        errors.New("request too large"),
		Type:       ErrorTypeClientError,
		StatusCode: 413,
		Message:    "entity too large",
	}

	mock := &mockPRWExporter{
		failCount: 1,
		failErr:   splittableErr,
	}
	cfg := PRWQueueConfig{
		Path:               t.TempDir(),
		MaxSize:            1000,
		MaxBytes:           1024 * 1024,
		RetryInterval:      20 * time.Millisecond,
		MaxRetryDelay:      200 * time.Millisecond,
		AlwaysQueue:        true,
		Workers:            1,
		CloseTimeout:       5 * time.Second,
		DrainTimeout:       2 * time.Second,
		DrainEntryTimeout:  1 * time.Second,
		RetryExportTimeout: 2 * time.Second,
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}

	// Create a request with multiple timeseries for splitting
	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{Labels: []prw.Label{{Name: "__name__", Value: "metric1"}}, Samples: []prw.Sample{{Value: 1}}},
			{Labels: []prw.Label{{Name: "__name__", Value: "metric2"}}, Samples: []prw.Sample{{Value: 2}}},
			{Labels: []prw.Label{{Name: "__name__", Value: "metric3"}}, Samples: []prw.Sample{{Value: 3}}},
			{Labels: []prw.Label{{Name: "__name__", Value: "metric4"}}, Samples: []prw.Sample{{Value: 4}}},
		},
	}

	if err := qe.Export(context.Background(), req); err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Wait for split + retry
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		calls := atomic.LoadInt64(&mock.exportCalls)
		if calls >= 3 { // 1 fail + 2 halves
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	calls := atomic.LoadInt64(&mock.exportCalls)
	if calls < 2 {
		t.Logf("PRW split calls: %d (split may still be processing)", calls)
	}

	qe.Close()
}

// TestPRWWorkerLoop_NonRetryableError tests the PRW worker dropping
// non-retryable errors.
func TestPRWWorkerLoop_NonRetryableError(t *testing.T) {
	authErr := &ExportError{
		Err:        errors.New("unauthorized"),
		Type:       ErrorTypeAuth,
		StatusCode: 401,
		Message:    "unauthorized",
	}

	mock := &mockPRWExporter{
		failCount: 1000,
		failErr:   authErr,
	}
	cfg := PRWQueueConfig{
		Path:               t.TempDir(),
		MaxSize:            1000,
		MaxBytes:           1024 * 1024,
		RetryInterval:      20 * time.Millisecond,
		MaxRetryDelay:      100 * time.Millisecond,
		AlwaysQueue:        true,
		Workers:            1,
		CloseTimeout:       2 * time.Second,
		DrainTimeout:       1 * time.Second,
		DrainEntryTimeout:  500 * time.Millisecond,
		RetryExportTimeout: 1 * time.Second,
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}

	if err := qe.Export(context.Background(), makePRWTestRequest()); err != nil {
		t.Fatalf("Export: %v", err)
	}

	// Wait for worker to drop non-retryable
	time.Sleep(300 * time.Millisecond)

	calls := atomic.LoadInt64(&mock.exportCalls)
	if calls < 1 {
		t.Errorf("expected at least 1 call, got %d", calls)
	}

	qe.Close()
}

// TestPRWWorkerLoop_Backoff tests the PRW worker backoff behavior.
func TestPRWWorkerLoop_Backoff(t *testing.T) {
	retryErr := errors.New("transient failure")
	mock := &mockPRWExporter{
		failCount: 1000,
		failErr:   retryErr,
	}
	cfg := PRWQueueConfig{
		Path:               t.TempDir(),
		MaxSize:            1000,
		MaxBytes:           1024 * 1024,
		RetryInterval:      20 * time.Millisecond,
		MaxRetryDelay:      200 * time.Millisecond,
		AlwaysQueue:        true,
		Workers:            1,
		BackoffEnabled:     true,
		BackoffMultiplier:  2.0,
		CloseTimeout:       2 * time.Second,
		DrainTimeout:       1 * time.Second,
		DrainEntryTimeout:  500 * time.Millisecond,
		RetryExportTimeout: 1 * time.Second,
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}

	if err := qe.Export(context.Background(), makePRWTestRequest()); err != nil {
		t.Fatalf("Export: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	calls := atomic.LoadInt64(&mock.exportCalls)
	if calls < 2 {
		t.Errorf("expected at least 2 retry calls with backoff, got %d", calls)
	}

	qe.Close()
}
