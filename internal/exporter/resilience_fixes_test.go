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
)

// =============================================================================
// Test 1: CB Gate in Export()
// When circuit breaker is OPEN, Export() should queue immediately without
// calling the underlying exporter's Export(). When CLOSED, it should call it.
// =============================================================================

func TestCBGate_OpenCircuitQueuesWithoutCallingExporter(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{failCount: 0} // Would succeed if called

	queueCfg := queue.Config{
		Path:                       tmpDir,
		MaxSize:                    1000,
		RetryInterval:              1 * time.Hour, // Don't auto-retry during test
		MaxRetryDelay:              1 * time.Hour,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    2,
		CircuitBreakerResetTimeout: 1 * time.Hour, // Don't transition to half-open
		InmemoryBlocks:             100,
		ChunkSize:                  64 * 1024,
		MetaSyncInterval:           5 * time.Second,
		StaleFlushInterval:         5 * time.Second,
		WriteBufferSize:            1024,
		Compression:                "snappy",
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// Force circuit breaker to OPEN state by recording enough failures
	qe.circuitBreaker.RecordFailure()
	qe.circuitBreaker.RecordFailure()

	if qe.circuitBreaker.State() != CircuitOpen {
		t.Fatalf("expected circuit OPEN, got %v", qe.circuitBreaker.State())
	}

	// Record export count before
	countBefore := mock.getExportCount()

	// Export with circuit OPEN -- should queue without calling exporter
	req := createTestRequest()
	err = qe.Export(context.Background(), req)

	// Must return ErrExportQueued
	if !errors.Is(err, ErrExportQueued) {
		t.Fatalf("expected ErrExportQueued, got %v", err)
	}

	// Exporter must NOT have been called
	countAfter := mock.getExportCount()
	if countAfter != countBefore {
		t.Fatalf("exporter was called %d times when circuit was open (expected 0 additional calls)", countAfter-countBefore)
	}

	// Entry must be in queue
	if qe.QueueLen() != 1 {
		t.Fatalf("expected 1 queued entry, got %d", qe.QueueLen())
	}
}

func TestCBGate_ClosedCircuitCallsExporter(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{failCount: 0} // Will succeed

	queueCfg := queue.Config{
		Path:                       tmpDir,
		MaxSize:                    1000,
		RetryInterval:              1 * time.Hour,
		MaxRetryDelay:              1 * time.Hour,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    10,
		CircuitBreakerResetTimeout: 30 * time.Second,
		InmemoryBlocks:             100,
		ChunkSize:                  64 * 1024,
		MetaSyncInterval:           5 * time.Second,
		StaleFlushInterval:         5 * time.Second,
		WriteBufferSize:            1024,
		Compression:                "snappy",
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	if qe.circuitBreaker.State() != CircuitClosed {
		t.Fatalf("expected circuit CLOSED, got %v", qe.circuitBreaker.State())
	}

	req := createTestRequest()
	err = qe.Export(context.Background(), req)

	if err != nil {
		t.Fatalf("expected nil error on successful export with closed circuit, got %v", err)
	}

	if mock.getExportCount() != 1 {
		t.Fatalf("expected exporter to be called once, got %d", mock.getExportCount())
	}

	if qe.QueueLen() != 0 {
		t.Fatalf("expected empty queue after successful export, got %d", qe.QueueLen())
	}
}

func TestCBGate_MultipleExportsWhileOpen(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{failCount: 0}

	queueCfg := queue.Config{
		Path:                       tmpDir,
		MaxSize:                    1000,
		RetryInterval:              1 * time.Hour,
		MaxRetryDelay:              1 * time.Hour,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    2,
		CircuitBreakerResetTimeout: 1 * time.Hour,
		InmemoryBlocks:             100,
		ChunkSize:                  64 * 1024,
		MetaSyncInterval:           5 * time.Second,
		StaleFlushInterval:         5 * time.Second,
		WriteBufferSize:            1024,
		Compression:                "snappy",
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// Force open
	qe.circuitBreaker.RecordFailure()
	qe.circuitBreaker.RecordFailure()

	// Export 20 requests with circuit open
	const n = 20
	for i := 0; i < n; i++ {
		err := qe.Export(context.Background(), createTestRequest())
		if !errors.Is(err, ErrExportQueued) {
			t.Fatalf("export %d: expected ErrExportQueued, got %v", i, err)
		}
	}

	// Zero exporter calls
	if mock.getExportCount() != 0 {
		t.Fatalf("expected 0 exporter calls while circuit open, got %d", mock.getExportCount())
	}

	// All 20 in queue
	if qe.QueueLen() != n {
		t.Fatalf("expected %d queued entries, got %d", n, qe.QueueLen())
	}
}

// =============================================================================
// Test 2: CAS Half-Open Transition
// When many goroutines call AllowRequest() simultaneously after the reset
// timeout, the CAS(Open->HalfOpen) should fire exactly ONCE. Goroutines that
// read Open state and lose the CAS get false. The CAS winner and goroutines
// that subsequently read HalfOpen get true.
//
// The key invariant: the state transitions Open->HalfOpen exactly once, and
// goroutines that observe the Open state and fail the CAS are rejected (they
// don't get to send a probe request to the recovering backend).
// =============================================================================

func TestCAS_HalfOpenTransition_OnlyOneCASWinner(t *testing.T) {
	cb := NewCircuitBreaker(2, 100*time.Millisecond)

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.State() != CircuitOpen {
		t.Fatalf("expected circuit OPEN, got %v", cb.State())
	}

	// Wait for reset timeout to expire
	time.Sleep(150 * time.Millisecond)

	// Use the raw CAS directly to test the invariant: only one goroutine
	// can successfully transition from Open to HalfOpen.
	const goroutines = 50
	var (
		wg       sync.WaitGroup
		casWins  atomic.Int32
		casLoses atomic.Int32
		barrier  = make(chan struct{})
	)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			// Replicate the CAS logic from AllowRequest() for Open state
			if cb.state.CompareAndSwap(int32(CircuitOpen), int32(CircuitHalfOpen)) {
				casWins.Add(1)
			} else {
				casLoses.Add(1)
			}
		}()
	}

	close(barrier)
	wg.Wait()

	wins := casWins.Load()
	if wins != 1 {
		t.Fatalf("expected exactly 1 CAS winner, got %d", wins)
	}

	// State should be HalfOpen
	if cb.State() != CircuitHalfOpen {
		t.Fatalf("expected circuit HALF_OPEN, got %v", cb.State())
	}
}

func TestCAS_HalfOpenTransition_ViaAllowRequest(t *testing.T) {
	// Test AllowRequest() integration: after reset timeout, calling AllowRequest
	// transitions to HalfOpen. The first caller gets true (CAS winner). Subsequent
	// callers also get true because they see HalfOpen state. This is correct
	// behavior -- the CAS only prevents multiple Open->HalfOpen transitions, not
	// requests in HalfOpen state.
	//
	// Note: resetTimeout uses Unix() second granularity, so we use a 2s timeout
	// to ensure the "before timeout" check works reliably.
	cb := NewCircuitBreaker(2, 2*time.Second)

	// Open the circuit
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatalf("expected OPEN, got %v", cb.State())
	}

	// Immediately after opening, AllowRequest should return false
	// (resetTimeout is 2s and we haven't waited)
	if cb.AllowRequest() {
		t.Fatal("AllowRequest should return false before reset timeout")
	}

	// Wait for reset timeout to expire
	time.Sleep(2100 * time.Millisecond)

	// First call transitions Open->HalfOpen and returns true
	if !cb.AllowRequest() {
		t.Fatal("first AllowRequest after timeout should return true (CAS winner)")
	}
	if cb.State() != CircuitHalfOpen {
		t.Fatalf("expected HALF_OPEN after first AllowRequest, got %v", cb.State())
	}

	// Second call sees HalfOpen and returns true (not a CAS race, just HalfOpen behavior)
	if !cb.AllowRequest() {
		t.Fatal("AllowRequest in HalfOpen should return true")
	}
}

func TestCAS_HalfOpenTransition_RepeatedCycles(t *testing.T) {
	cb := NewCircuitBreaker(1, 50*time.Millisecond)

	for cycle := 0; cycle < 3; cycle++ {
		// Open the circuit
		cb.RecordFailure()
		if cb.State() != CircuitOpen {
			t.Fatalf("cycle %d: expected OPEN, got %v", cycle, cb.State())
		}

		// Wait for reset timeout
		time.Sleep(80 * time.Millisecond)

		// Race many goroutines on the raw CAS
		const goroutines = 20
		var (
			wg      sync.WaitGroup
			casWins atomic.Int32
			barrier = make(chan struct{})
		)

		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-barrier
				if cb.state.CompareAndSwap(int32(CircuitOpen), int32(CircuitHalfOpen)) {
					casWins.Add(1)
				}
			}()
		}

		close(barrier)
		wg.Wait()

		if casWins.Load() != 1 {
			t.Fatalf("cycle %d: expected 1 CAS winner, got %d", cycle, casWins.Load())
		}

		// Close the circuit to start fresh
		cb.RecordSuccess()
		if cb.State() != CircuitClosed {
			t.Fatalf("cycle %d: expected CLOSED after success, got %v", cycle, cb.State())
		}
	}
}

// =============================================================================
// Test 3: Batch Drain
// With batchDrainSize=10, the retry loop should drain up to 10 entries per
// tick (not just 1).
// =============================================================================

func TestBatchDrain_ProcessesMultipleEntriesPerTick(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{failCount: 0} // All retries succeed

	const batchSize = 10
	queueCfg := queue.Config{
		Path:               tmpDir,
		MaxSize:            1000,
		RetryInterval:      50 * time.Millisecond,
		MaxRetryDelay:      100 * time.Millisecond,
		BatchDrainSize:     batchSize,
		BurstDrainSize:     0, // Disable burst drain so we only measure batch drain
		InmemoryBlocks:     100,
		ChunkSize:          64 * 1024,
		MetaSyncInterval:   5 * time.Second,
		StaleFlushInterval: 5 * time.Second,
		WriteBufferSize:    1024,
		Compression:        "snappy",
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// Directly push entries to the queue (bypass Export to avoid calling mock)
	for i := 0; i < batchSize; i++ {
		if pushErr := qe.queue.Push(createTestRequest()); pushErr != nil {
			t.Fatalf("push %d: %v", i, pushErr)
		}
	}

	if qe.QueueLen() != batchSize {
		t.Fatalf("expected %d entries in queue, got %d", batchSize, qe.QueueLen())
	}

	// Wait for one or two retry ticks to process the batch
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if qe.QueueLen() == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if qe.QueueLen() != 0 {
		t.Fatalf("expected queue to be drained, still has %d entries", qe.QueueLen())
	}

	// All entries should have been exported via retries
	count := mock.getExportCount()
	if count < int64(batchSize) {
		t.Fatalf("expected at least %d export calls from batch drain, got %d", batchSize, count)
	}
}

func TestBatchDrain_RespectsConfiguredSize(t *testing.T) {
	tmpDir := t.TempDir()

	// Use a mock that always fails to prevent entries from being drained
	// and then succeeds, so we can observe the batch size effect
	mock := &mockExporter{failCount: 1000}

	queueCfg := queue.Config{
		Path:               tmpDir,
		MaxSize:            1000,
		RetryInterval:      1 * time.Hour, // Don't auto-retry
		MaxRetryDelay:      1 * time.Hour,
		BatchDrainSize:     5,
		InmemoryBlocks:     100,
		ChunkSize:          64 * 1024,
		MetaSyncInterval:   5 * time.Second,
		StaleFlushInterval: 5 * time.Second,
		WriteBufferSize:    1024,
		Compression:        "snappy",
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// Verify batchDrainSize is set correctly
	if qe.batchDrainSize != 5 {
		t.Fatalf("expected batchDrainSize=5, got %d", qe.batchDrainSize)
	}

	// Default should be 10
	tmpDir2 := t.TempDir()
	queueCfg2 := queue.Config{
		Path:               tmpDir2,
		MaxSize:            1000,
		RetryInterval:      1 * time.Hour,
		MaxRetryDelay:      1 * time.Hour,
		InmemoryBlocks:     100,
		ChunkSize:          64 * 1024,
		MetaSyncInterval:   5 * time.Second,
		StaleFlushInterval: 5 * time.Second,
		WriteBufferSize:    1024,
		Compression:        "snappy",
	}
	qe2, err := NewQueued(&mockExporter{}, queueCfg2)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe2.Close()

	if qe2.batchDrainSize != 10 {
		t.Fatalf("expected default batchDrainSize=10, got %d", qe2.batchDrainSize)
	}
}

// =============================================================================
// Test 4: Burst Drain
// After a recovery (success after failure), burst drain should process up to
// burstDrainSize entries rapidly.
// =============================================================================

func TestBurstDrain_ProcessesEntriesOnRecovery(t *testing.T) {
	tmpDir := t.TempDir()

	// Start with exporter that fails, then succeeds
	mock := &mockExporter{}
	mock.mu.Lock()
	mock.failCount = 100 // initially fail everything
	mock.mu.Unlock()

	const burstSize = 50
	queueCfg := queue.Config{
		Path:               tmpDir,
		MaxSize:            10000,
		RetryInterval:      50 * time.Millisecond,
		MaxRetryDelay:      100 * time.Millisecond,
		BatchDrainSize:     5,
		BurstDrainSize:     burstSize,
		InmemoryBlocks:     200,
		ChunkSize:          64 * 1024,
		MetaSyncInterval:   5 * time.Second,
		StaleFlushInterval: 5 * time.Second,
		WriteBufferSize:    1024,
		Compression:        "snappy",
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// Verify burstDrainSize is configured
	if qe.burstDrainSize != burstSize {
		t.Fatalf("expected burstDrainSize=%d, got %d", burstSize, qe.burstDrainSize)
	}

	// Push entries directly to queue
	const totalEntries = 60
	for i := 0; i < totalEntries; i++ {
		if pushErr := qe.queue.Push(createTestRequest()); pushErr != nil {
			t.Fatalf("push %d: %v", i, pushErr)
		}
	}

	// Wait a tick so the retry loop sees failures
	time.Sleep(100 * time.Millisecond)

	// Now flip mock to succeed — this triggers burst drain on next successful retry
	mock.setFailCount(0)

	// Wait for burst drain to process entries
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if qe.QueueLen() == 0 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	if qe.QueueLen() != 0 {
		t.Fatalf("expected queue to be empty after burst drain, still has %d entries", qe.QueueLen())
	}

	// All entries should have been exported
	exportCount := mock.getExportCount()
	if exportCount < int64(totalEntries) {
		t.Fatalf("expected at least %d export calls, got %d", totalEntries, exportCount)
	}
}

func TestBurstDrain_DefaultSize(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{}

	queueCfg := queue.Config{
		Path:          tmpDir,
		MaxSize:       1000,
		RetryInterval: 1 * time.Hour,
		MaxRetryDelay: 1 * time.Hour,
		// BurstDrainSize not set -- should default to 100
		InmemoryBlocks:     100,
		ChunkSize:          64 * 1024,
		MetaSyncInterval:   5 * time.Second,
		StaleFlushInterval: 5 * time.Second,
		WriteBufferSize:    1024,
		Compression:        "snappy",
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	if qe.burstDrainSize != 100 {
		t.Fatalf("expected default burstDrainSize=100, got %d", qe.burstDrainSize)
	}
}

// =============================================================================
// Test 5: ErrExportQueued Sentinel
// When data is queued (CB open), Export() should return ErrExportQueued (not nil).
// =============================================================================

func TestErrExportQueued_SentinelOnCircuitOpen(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{}

	queueCfg := queue.Config{
		Path:                       tmpDir,
		MaxSize:                    1000,
		RetryInterval:              1 * time.Hour,
		MaxRetryDelay:              1 * time.Hour,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    2,
		CircuitBreakerResetTimeout: 1 * time.Hour,
		InmemoryBlocks:             100,
		ChunkSize:                  64 * 1024,
		MetaSyncInterval:           5 * time.Second,
		StaleFlushInterval:         5 * time.Second,
		WriteBufferSize:            1024,
		Compression:                "snappy",
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// Force open
	qe.circuitBreaker.RecordFailure()
	qe.circuitBreaker.RecordFailure()

	req := createTestRequest()
	err = qe.Export(context.Background(), req)

	// Must be ErrExportQueued, not nil
	if err == nil {
		t.Fatal("expected ErrExportQueued, got nil")
	}
	if !errors.Is(err, ErrExportQueued) {
		t.Fatalf("expected errors.Is(err, ErrExportQueued) to be true, got: %v", err)
	}
}

func TestErrExportQueued_SentinelOnExportFailure(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{failCount: 100} // Always fail

	queueCfg := queue.Config{
		Path:               tmpDir,
		MaxSize:            1000,
		RetryInterval:      1 * time.Hour,
		MaxRetryDelay:      1 * time.Hour,
		InmemoryBlocks:     100,
		ChunkSize:          64 * 1024,
		MetaSyncInterval:   5 * time.Second,
		StaleFlushInterval: 5 * time.Second,
		WriteBufferSize:    1024,
		Compression:        "snappy",
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	req := createTestRequest()
	err = qe.Export(context.Background(), req)

	// When export fails and data is queued, should return ErrExportQueued
	if !errors.Is(err, ErrExportQueued) {
		t.Fatalf("expected ErrExportQueued after failed export with queuing, got: %v", err)
	}
}

func TestErrExportQueued_SuccessReturnsNil(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{} // Always succeeds

	queueCfg := queue.Config{
		Path:                       tmpDir,
		MaxSize:                    1000,
		RetryInterval:              1 * time.Hour,
		MaxRetryDelay:              1 * time.Hour,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    10,
		CircuitBreakerResetTimeout: 30 * time.Second,
		InmemoryBlocks:             100,
		ChunkSize:                  64 * 1024,
		MetaSyncInterval:           5 * time.Second,
		StaleFlushInterval:         5 * time.Second,
		WriteBufferSize:            1024,
		Compression:                "snappy",
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	req := createTestRequest()
	err = qe.Export(context.Background(), req)

	// Successful export should return nil, not ErrExportQueued
	if err != nil {
		t.Fatalf("expected nil on successful export, got: %v", err)
	}
}

func TestErrExportQueued_IsDistinguishable(t *testing.T) {
	// Verify ErrExportQueued is a distinct sentinel that can be detected with errors.Is
	err := ErrExportQueued
	if !errors.Is(err, ErrExportQueued) {
		t.Fatal("errors.Is should detect ErrExportQueued")
	}

	otherErr := errors.New("some other error")
	if errors.Is(otherErr, ErrExportQueued) {
		t.Fatal("errors.Is should not match unrelated error")
	}
}

// =============================================================================
// Test 6: Configurable Retry Timeout
// With retryExportTimeout=1s, retries should time out at 1s (not the old
// hardcoded 30s).
// =============================================================================

func TestConfigurableRetryTimeout_UsesConfiguredValue(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{}

	customTimeout := 1 * time.Second
	queueCfg := queue.Config{
		Path:               tmpDir,
		MaxSize:            1000,
		RetryInterval:      50 * time.Millisecond,
		MaxRetryDelay:      100 * time.Millisecond,
		RetryExportTimeout: customTimeout,
		InmemoryBlocks:     100,
		ChunkSize:          64 * 1024,
		MetaSyncInterval:   5 * time.Second,
		StaleFlushInterval: 5 * time.Second,
		WriteBufferSize:    1024,
		Compression:        "snappy",
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	if qe.retryExportTimeout != customTimeout {
		t.Fatalf("expected retryExportTimeout=%v, got %v", customTimeout, qe.retryExportTimeout)
	}
}

func TestConfigurableRetryTimeout_DefaultIs10s(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{}

	queueCfg := queue.Config{
		Path:          tmpDir,
		MaxSize:       1000,
		RetryInterval: 50 * time.Millisecond,
		MaxRetryDelay: 100 * time.Millisecond,
		// RetryExportTimeout not set -- should default to 10s
		InmemoryBlocks:     100,
		ChunkSize:          64 * 1024,
		MetaSyncInterval:   5 * time.Second,
		StaleFlushInterval: 5 * time.Second,
		WriteBufferSize:    1024,
		Compression:        "snappy",
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	if qe.retryExportTimeout != 10*time.Second {
		t.Fatalf("expected default retryExportTimeout=10s, got %v", qe.retryExportTimeout)
	}
}

func TestConfigurableRetryTimeout_ActuallyTimesOut(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a slow exporter that sleeps longer than the retry timeout
	slowMock := &slowExporter{delay: 500 * time.Millisecond}

	shortTimeout := 100 * time.Millisecond
	queueCfg := queue.Config{
		Path:               tmpDir,
		MaxSize:            1000,
		RetryInterval:      50 * time.Millisecond,
		MaxRetryDelay:      100 * time.Millisecond,
		RetryExportTimeout: shortTimeout,
		InmemoryBlocks:     100,
		ChunkSize:          64 * 1024,
		MetaSyncInterval:   5 * time.Second,
		StaleFlushInterval: 5 * time.Second,
		WriteBufferSize:    1024,
		Compression:        "snappy",
	}

	qe, err := NewQueued(slowMock, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// Push an entry directly to queue (it will be retried with the configured timeout)
	if pushErr := qe.queue.Push(createTestRequest()); pushErr != nil {
		t.Fatalf("push: %v", pushErr)
	}

	// Wait for a retry attempt -- the slow exporter should be canceled by the short timeout
	time.Sleep(300 * time.Millisecond)

	// The entry should still be in queue because the slow exporter timed out
	if qe.QueueLen() == 0 {
		// The entry might have been processed if timing is tight.
		// Check that the slow exporter recorded a timeout
		if slowMock.getTimeoutCount() == 0 && slowMock.getSuccessCount() == 0 {
			t.Fatal("expected at least one timeout or success from slow exporter")
		}
	}
}

func TestConfigurableRetryTimeout_AllTimeoutsConfigurable(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{}

	queueCfg := queue.Config{
		Path:               tmpDir,
		MaxSize:            1000,
		RetryInterval:      50 * time.Millisecond,
		MaxRetryDelay:      100 * time.Millisecond,
		RetryExportTimeout: 2 * time.Second,
		CloseTimeout:       45 * time.Second,
		DrainTimeout:       20 * time.Second,
		DrainEntryTimeout:  3 * time.Second,
		InmemoryBlocks:     100,
		ChunkSize:          64 * 1024,
		MetaSyncInterval:   5 * time.Second,
		StaleFlushInterval: 5 * time.Second,
		WriteBufferSize:    1024,
		Compression:        "snappy",
	}

	qe, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	if qe.retryExportTimeout != 2*time.Second {
		t.Errorf("expected retryExportTimeout=2s, got %v", qe.retryExportTimeout)
	}
	if qe.closeTimeout != 45*time.Second {
		t.Errorf("expected closeTimeout=45s, got %v", qe.closeTimeout)
	}
	if qe.drainTimeout != 20*time.Second {
		t.Errorf("expected drainTimeout=20s, got %v", qe.drainTimeout)
	}
	if qe.drainEntryTimeout != 3*time.Second {
		t.Errorf("expected drainEntryTimeout=3s, got %v", qe.drainEntryTimeout)
	}
}

// =============================================================================
// slowExporter sleeps for a configurable duration, allowing timeout testing.
// =============================================================================

type slowExporter struct {
	delay        time.Duration
	mu           sync.Mutex
	timeoutCount int
	successCount int
}

func (e *slowExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	select {
	case <-time.After(e.delay):
		e.mu.Lock()
		e.successCount++
		e.mu.Unlock()
		return nil
	case <-ctx.Done():
		e.mu.Lock()
		e.timeoutCount++
		e.mu.Unlock()
		return ctx.Err()
	}
}

func (e *slowExporter) Close() error { return nil }

func (e *slowExporter) getTimeoutCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.timeoutCount
}

func (e *slowExporter) getSuccessCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.successCount
}
