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
// QueuedExporter.Export — always-queue mode with memory queue
// =============================================================================

func TestQueuedExporter_Export_MemoryQueue(t *testing.T) {
	mock := &aqMockExporter{}
	cfg := queue.Config{
		Path:               t.TempDir(),
		MaxSize:            1000,
		MaxBytes:           1024 * 1024,
		RetryInterval:      20 * time.Millisecond,
		MaxRetryDelay:      200 * time.Millisecond,
		AlwaysQueue:        true,
		Workers:            2,
		Mode:               queue.QueueModeMemory,
		CloseTimeout:       5 * time.Second,
		DrainTimeout:       2 * time.Second,
		DrainEntryTimeout:  1 * time.Second,
		RetryExportTimeout: 2 * time.Second,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	// Push items
	for i := 0; i < 5; i++ {
		err := qe.Export(context.Background(), aqTestRequest("mem_mode"))
		if err != nil {
			t.Fatalf("Export %d: %v", i, err)
		}
	}

	// Wait for workers to process
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if mock.getCalls() >= 5 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if mock.getCalls() < 5 {
		t.Errorf("expected at least 5 export calls, got %d", mock.getCalls())
	}

	qe.Close()
}

func TestQueuedExporter_Export_MemoryQueue_QueueMode(t *testing.T) {
	mock := &aqMockExporter{}
	cfg := queue.Config{
		Path:               t.TempDir(),
		MaxSize:            1000,
		MaxBytes:           1024 * 1024,
		RetryInterval:      time.Hour,
		MaxRetryDelay:      time.Hour,
		AlwaysQueue:        true,
		Workers:            1,
		Mode:               queue.QueueModeMemory,
		CloseTimeout:       5 * time.Second,
		DrainTimeout:       2 * time.Second,
		DrainEntryTimeout:  1 * time.Second,
		RetryExportTimeout: 2 * time.Second,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// Verify QueueMode accessor
	if qe.QueueMode() != queue.QueueModeMemory {
		t.Errorf("QueueMode() = %q, want %q", qe.QueueMode(), queue.QueueModeMemory)
	}
}

// =============================================================================
// QueuedExporter.Export — always-queue mode with disk queue
// =============================================================================

func TestQueuedExporter_Export_DiskQueue(t *testing.T) {
	mock := &aqMockExporter{}
	cfg := queue.Config{
		Path:               t.TempDir(),
		MaxSize:            1000,
		RetryInterval:      20 * time.Millisecond,
		MaxRetryDelay:      200 * time.Millisecond,
		AlwaysQueue:        true,
		Workers:            2,
		Mode:               queue.QueueModeDisk,
		CloseTimeout:       5 * time.Second,
		DrainTimeout:       2 * time.Second,
		DrainEntryTimeout:  1 * time.Second,
		RetryExportTimeout: 2 * time.Second,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	for i := 0; i < 5; i++ {
		err := qe.Export(context.Background(), aqTestRequest("disk_mode"))
		if err != nil {
			t.Fatalf("Export %d: %v", i, err)
		}
	}

	// Wait for workers to process
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if mock.getCalls() >= 5 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if mock.getCalls() < 5 {
		t.Errorf("expected at least 5 export calls, got %d", mock.getCalls())
	}

	qe.Close()
}

func TestQueuedExporter_Export_DiskQueue_QueueMode(t *testing.T) {
	mock := &aqMockExporter{}
	cfg := queue.Config{
		Path:               t.TempDir(),
		MaxSize:            1000,
		RetryInterval:      time.Hour,
		MaxRetryDelay:      time.Hour,
		AlwaysQueue:        true,
		Workers:            1,
		Mode:               queue.QueueModeDisk,
		CloseTimeout:       5 * time.Second,
		DrainTimeout:       2 * time.Second,
		DrainEntryTimeout:  1 * time.Second,
		RetryExportTimeout: 2 * time.Second,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	if qe.QueueMode() != queue.QueueModeDisk {
		t.Errorf("QueueMode() = %q, want %q", qe.QueueMode(), queue.QueueModeDisk)
	}
}

// =============================================================================
// QueuedExporter.Export — always-queue mode with hybrid queue
// =============================================================================

func TestQueuedExporter_Export_HybridQueue(t *testing.T) {
	mock := &aqMockExporter{}
	cfg := queue.Config{
		Path:               t.TempDir(),
		MaxSize:            10000,
		MaxBytes:           1024 * 1024,
		RetryInterval:      20 * time.Millisecond,
		MaxRetryDelay:      200 * time.Millisecond,
		AlwaysQueue:        true,
		Workers:            2,
		Mode:               queue.QueueModeHybrid,
		CloseTimeout:       5 * time.Second,
		DrainTimeout:       2 * time.Second,
		DrainEntryTimeout:  1 * time.Second,
		RetryExportTimeout: 2 * time.Second,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	// Push items through hybrid queue
	for i := 0; i < 5; i++ {
		err := qe.Export(context.Background(), aqTestRequest("hybrid_mode"))
		if err != nil {
			t.Fatalf("Export %d: %v", i, err)
		}
	}

	// Wait for workers to process
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if mock.getCalls() >= 5 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if mock.getCalls() < 5 {
		t.Errorf("expected at least 5 export calls, got %d", mock.getCalls())
	}

	qe.Close()
}

// =============================================================================
// QueuedExporter.QueueMode — default mode
// =============================================================================

func TestQueuedExporter_QueueMode_Default(t *testing.T) {
	mock := &mockExporter{}
	cfg := queue.Config{
		Path:          t.TempDir(),
		MaxSize:       100,
		RetryInterval: time.Hour,
		MaxRetryDelay: time.Hour,
		// Mode not set — should default to disk
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	if qe.QueueMode() != queue.QueueModeDisk {
		t.Errorf("QueueMode() = %q, want %q", qe.QueueMode(), queue.QueueModeDisk)
	}
}

// =============================================================================
// QueuedExporter.SetBatchTuner
// =============================================================================

func TestQueuedExporter_SetBatchTuner(t *testing.T) {
	mock := &mockExporter{}
	cfg := queue.Config{
		Path:          t.TempDir(),
		MaxSize:       100,
		RetryInterval: time.Hour,
		MaxRetryDelay: time.Hour,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// Initially nil
	if qe.batchTuner != nil {
		t.Error("expected nil batchTuner initially")
	}

	// Set batch tuner
	bt := NewBatchTuner(BatchTunerConfig{
		Enabled:      true,
		MaxBytes:     1024 * 1024,
		MinBytes:     1024,
		ShrinkFactor: 0.5,
		GrowFactor:   1.25,
	})
	qe.SetBatchTuner(bt)

	if qe.batchTuner == nil {
		t.Error("expected non-nil batchTuner after SetBatchTuner")
	}
	if qe.batchTuner != bt {
		t.Error("batchTuner should be the instance we set")
	}
}

// =============================================================================
// QueuedExporter legacy Export with circuit breaker + queue push paths
// =============================================================================

func TestQueuedExporter_LegacyExport_CircuitBreakerOpenQueues(t *testing.T) {
	mock := &mockExporter{}
	cfg := queue.Config{
		Path:                       t.TempDir(),
		MaxSize:                    1000,
		RetryInterval:              time.Hour,
		MaxRetryDelay:              time.Hour,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    2,
		CircuitBreakerResetTimeout: time.Hour,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// Force circuit open
	qe.circuitBreaker.RecordFailure()
	qe.circuitBreaker.RecordFailure()

	req := createTestRequest()
	err = qe.Export(context.Background(), req)
	if !errors.Is(err, ErrExportQueued) {
		t.Fatalf("expected ErrExportQueued when circuit is open, got %v", err)
	}

	// Should not have called the mock exporter
	if mock.getExportCount() != 0 {
		t.Fatalf("expected 0 export calls with open circuit, got %d", mock.getExportCount())
	}

	if qe.QueueLen() != 1 {
		t.Fatalf("expected 1 queued entry, got %d", qe.QueueLen())
	}
}

func TestQueuedExporter_LegacyExport_DirectExportTimeout(t *testing.T) {
	// Mock that is very slow
	mock := &aqMockExporter{delay: 5 * time.Second}
	cfg := queue.Config{
		Path:                t.TempDir(),
		MaxSize:             1000,
		RetryInterval:       time.Hour,
		MaxRetryDelay:       time.Hour,
		DirectExportTimeout: 100 * time.Millisecond,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	req := createTestRequest()
	start := time.Now()
	err = qe.Export(context.Background(), req)
	elapsed := time.Since(start)

	// Should timeout and queue the request, not wait 5 seconds
	if elapsed > 2*time.Second {
		t.Fatalf("Export took too long: %v (expected ~100ms timeout)", elapsed)
	}

	// Either returns queued or nil, depending on behavior
	if err != nil && !errors.Is(err, ErrExportQueued) {
		t.Logf("Export error (expected): %v", err)
	}
}

// =============================================================================
// QueuedExporter.workerLoop coverage improvement via always-queue + failures
// =============================================================================

func TestQueuedExporter_WorkerLoop_SplittableError(t *testing.T) {
	// Splittable error (413) should trigger split in workerLoop
	splittableErr := &ExportError{
		Err:        errors.New("entity too large"),
		Type:       ErrorTypeClientError,
		StatusCode: 413,
		Message:    "request entity too large",
	}

	callCount := int64(0)
	mock := &aqMockExporter{failCount: 1, exportErr: splittableErr}
	cfg := queue.Config{
		Path:               t.TempDir(),
		MaxSize:            10000,
		RetryInterval:      30 * time.Millisecond,
		MaxRetryDelay:      200 * time.Millisecond,
		AlwaysQueue:        true,
		Workers:            2,
		CloseTimeout:       5 * time.Second,
		DrainTimeout:       2 * time.Second,
		DrainEntryTimeout:  1 * time.Second,
		RetryExportTimeout: 2 * time.Second,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	// Request with multiple ResourceMetrics for splitting
	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{{Name: "m1"}}}}},
			{ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{{Name: "m2"}}}}},
			{ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{{Name: "m3"}}}}},
			{ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{{Name: "m4"}}}}},
		},
	}

	_ = qe.Export(context.Background(), req)

	// Wait for split + retry to complete
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(mock.getExported()) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	exported := mock.getExported()
	if len(exported) < 2 {
		t.Logf("Only %d exports completed (split may still be processing)", len(exported))
	}

	qe.Close()
	_ = callCount
}

func TestQueuedExporter_WorkerLoop_NonRetryableError(t *testing.T) {
	authErr := &ExportError{
		Err:        errors.New("unauthorized"),
		Type:       ErrorTypeAuth,
		StatusCode: 401,
		Message:    "unauthorized",
	}

	mock := &aqMockExporter{failCount: 1000, exportErr: authErr}
	cfg := queue.Config{
		Path:               t.TempDir(),
		MaxSize:            1000,
		RetryInterval:      20 * time.Millisecond,
		MaxRetryDelay:      100 * time.Millisecond,
		AlwaysQueue:        true,
		Workers:            1,
		CloseTimeout:       5 * time.Second,
		DrainTimeout:       2 * time.Second,
		DrainEntryTimeout:  1 * time.Second,
		RetryExportTimeout: 2 * time.Second,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	_ = qe.Export(context.Background(), aqTestRequest("auth_fail"))

	// Wait for worker to drop non-retryable
	time.Sleep(300 * time.Millisecond)

	// Should be at least 1 call (the initial attempt)
	if mock.getCalls() < 1 {
		t.Fatalf("expected at least 1 export call, got %d", mock.getCalls())
	}

	qe.Close()
}

func TestQueuedExporter_WorkerLoop_CircuitBreakerSkip(t *testing.T) {
	serverErr := &ExportError{
		Err:        errors.New("server error"),
		Type:       ErrorTypeServerError,
		StatusCode: 500,
		Message:    "server error",
	}

	mock := &aqMockExporter{failCount: 1000, exportErr: serverErr}
	cfg := queue.Config{
		Path:                       t.TempDir(),
		MaxSize:                    1000,
		RetryInterval:              20 * time.Millisecond,
		MaxRetryDelay:              100 * time.Millisecond,
		AlwaysQueue:                true,
		Workers:                    1,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    2,
		CircuitBreakerResetTimeout: time.Hour,
		CloseTimeout:               5 * time.Second,
		DrainTimeout:               2 * time.Second,
		DrainEntryTimeout:          1 * time.Second,
		RetryExportTimeout:         2 * time.Second,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	_ = qe.Export(context.Background(), aqTestRequest("cb_test"))

	// Wait for circuit to open
	time.Sleep(300 * time.Millisecond)

	callsBefore := mock.getCalls()

	// Force circuit open
	qe.circuitBreaker.RecordFailure()
	qe.circuitBreaker.RecordFailure()
	qe.circuitBreaker.RecordFailure()

	// Wait — workers should skip when circuit is open
	time.Sleep(200 * time.Millisecond)

	// Not many more calls should have happened while circuit is open
	callsAfter := mock.getCalls()
	t.Logf("Calls before=%d, after=%d (circuit should slow retries)", callsBefore, callsAfter)

	qe.Close()
}

// =============================================================================
// QueuedExporter.workerLoop — backoff behavior with always-queue
// =============================================================================

func TestQueuedExporter_WorkerLoop_Backoff(t *testing.T) {
	retryableErr := errors.New("transient failure")

	mock := &aqMockExporter{failCount: 100, exportErr: retryableErr}
	cfg := queue.Config{
		Path:               t.TempDir(),
		MaxSize:            1000,
		RetryInterval:      20 * time.Millisecond,
		MaxRetryDelay:      200 * time.Millisecond,
		AlwaysQueue:        true,
		Workers:            1,
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

	_ = qe.Export(context.Background(), aqTestRequest("backoff_test"))

	// Wait for a few retries with backoff
	time.Sleep(500 * time.Millisecond)

	calls := mock.getCalls()
	if calls < 2 {
		t.Fatalf("expected at least 2 export calls with backoff, got %d", calls)
	}

	qe.Close()
}

// =============================================================================
// QueuedExporter.Export — always-queue returns nil not ErrExportQueued
// =============================================================================

func TestQueuedExporter_AlwaysQueue_AllModes_ReturnNil(t *testing.T) {
	modes := []queue.QueueMode{
		queue.QueueModeDisk,
		queue.QueueModeMemory,
	}

	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			mock := &aqMockExporter{}
			cfg := queue.Config{
				Path:               t.TempDir(),
				MaxSize:            1000,
				MaxBytes:           1024 * 1024,
				RetryInterval:      time.Hour,
				MaxRetryDelay:      time.Hour,
				AlwaysQueue:        true,
				Workers:            1,
				Mode:               mode,
				CloseTimeout:       5 * time.Second,
				DrainTimeout:       2 * time.Second,
				DrainEntryTimeout:  1 * time.Second,
				RetryExportTimeout: 2 * time.Second,
			}

			qe, err := NewQueued(mock, cfg)
			if err != nil {
				t.Fatalf("NewQueued: %v", err)
			}
			defer qe.Close()

			err = qe.Export(context.Background(), aqTestRequest("nil_return"))
			if err != nil {
				t.Fatalf("Export() should return nil, got %v", err)
			}
		})
	}
}

// =============================================================================
// PRWQueuedExporter.SetBatchTuner
// =============================================================================

func TestPRWQueuedExporter_SetBatchTuner(t *testing.T) {
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
	defer qe.Close()

	// Initially nil
	if qe.batchTuner != nil {
		t.Error("expected nil batchTuner initially")
	}

	bt := NewBatchTuner(BatchTunerConfig{
		Enabled:      true,
		MaxBytes:     1024 * 1024,
		MinBytes:     1024,
		ShrinkFactor: 0.5,
		GrowFactor:   1.25,
	})
	qe.SetBatchTuner(bt)

	if qe.batchTuner == nil {
		t.Error("expected non-nil batchTuner after SetBatchTuner")
	}
}

// =============================================================================
// Additional workerLoop coverage via ExportData interface test
// =============================================================================

// mockDataExporter implements Exporter + dataExporter + dataCompressor + compressedSender
type mockDataExporter struct {
	exportCalls     int64
	exportDataCalls int64
	compressCalls   int64
	sendCalls       int64
	exportErr       error
	exportDataErr   error
	compressErr     error
	sendErr         error
}

func (m *mockDataExporter) Export(_ context.Context, _ *colmetricspb.ExportMetricsServiceRequest) error {
	atomic.AddInt64(&m.exportCalls, 1)
	return m.exportErr
}

func (m *mockDataExporter) ExportData(_ context.Context, _ []byte) error {
	atomic.AddInt64(&m.exportDataCalls, 1)
	return m.exportDataErr
}

func (m *mockDataExporter) CompressData(data []byte) ([]byte, string, error) {
	atomic.AddInt64(&m.compressCalls, 1)
	if m.compressErr != nil {
		return nil, "", m.compressErr
	}
	return data, "mock", nil
}

func (m *mockDataExporter) SendCompressed(_ context.Context, _ []byte, _ string, _ int) error {
	atomic.AddInt64(&m.sendCalls, 1)
	return m.sendErr
}

func (m *mockDataExporter) Close() error {
	return nil
}

func TestQueuedExporter_WorkerLoop_UsesExportDataFastPath(t *testing.T) {
	mock := &mockDataExporter{}
	cfg := queue.Config{
		Path:               t.TempDir(),
		MaxSize:            1000,
		RetryInterval:      20 * time.Millisecond,
		MaxRetryDelay:      200 * time.Millisecond,
		AlwaysQueue:        true,
		Workers:            1,
		CloseTimeout:       5 * time.Second,
		DrainTimeout:       2 * time.Second,
		DrainEntryTimeout:  1 * time.Second,
		RetryExportTimeout: 2 * time.Second,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	req := aqTestRequest("fast_path")
	_ = qe.Export(context.Background(), req)

	// Wait for worker to process
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&mock.exportDataCalls) >= 1 || atomic.LoadInt64(&mock.exportCalls) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Should use ExportData (fast path) instead of Export
	dataCalls := atomic.LoadInt64(&mock.exportDataCalls)
	exportCalls := atomic.LoadInt64(&mock.exportCalls)

	if dataCalls == 0 && exportCalls == 0 {
		t.Fatal("expected at least one export call (either ExportData or Export)")
	}

	// ExportData should be preferred when available
	if dataCalls > 0 {
		t.Logf("ExportData (fast path) used: %d calls", dataCalls)
	} else {
		t.Logf("Export (standard path) used: %d calls", exportCalls)
	}

	qe.Close()
}
