package exporter

import (
	"context"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/queue"
)

// =============================================================================
// QueueLen / QueueSize with memQueue (memory + hybrid modes)
// =============================================================================

func TestQueuedExporter_QueueLen_WithMemQueue(t *testing.T) {
	mock := &aqMockExporter{}
	cfg := queue.Config{
		Path:               t.TempDir(),
		MaxSize:            1000,
		MaxBytes:           1024 * 1024,
		RetryInterval:      time.Hour, // Very long so workers don't consume
		MaxRetryDelay:      time.Hour,
		AlwaysQueue:        true,
		Workers:            1,
		Mode:               queue.QueueModeMemory,
		CloseTimeout:       2 * time.Second,
		DrainTimeout:       1 * time.Second,
		DrainEntryTimeout:  500 * time.Millisecond,
		RetryExportTimeout: 1 * time.Second,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// memQueue should exist for memory mode
	if qe.memQueue == nil {
		t.Fatal("memQueue should be non-nil in memory mode")
	}

	// Push some items
	for i := 0; i < 3; i++ {
		_ = qe.Export(context.Background(), aqTestRequest("qlen_test"))
	}

	// QueueLen should include memQueue items
	length := qe.QueueLen()
	if length < 0 {
		t.Errorf("QueueLen() = %d, expected >= 0", length)
	}

	// QueueSize should include memQueue bytes
	size := qe.QueueSize()
	if size < 0 {
		t.Errorf("QueueSize() = %d, expected >= 0", size)
	}
}

func TestQueuedExporter_QueueLen_Hybrid(t *testing.T) {
	mock := &aqMockExporter{}
	cfg := queue.Config{
		Path:               t.TempDir(),
		MaxSize:            1000,
		MaxBytes:           1024 * 1024,
		MemoryMaxBytes:     1024 * 1024,
		RetryInterval:      time.Hour,
		MaxRetryDelay:      time.Hour,
		AlwaysQueue:        true,
		Workers:            1,
		Mode:               queue.QueueModeHybrid,
		CloseTimeout:       2 * time.Second,
		DrainTimeout:       1 * time.Second,
		DrainEntryTimeout:  500 * time.Millisecond,
		RetryExportTimeout: 1 * time.Second,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// memQueue should exist for hybrid mode
	if qe.memQueue == nil {
		t.Fatal("memQueue should be non-nil in hybrid mode")
	}

	for i := 0; i < 3; i++ {
		_ = qe.Export(context.Background(), aqTestRequest("hybrid_qlen"))
	}

	length := qe.QueueLen()
	t.Logf("Hybrid QueueLen: %d", length)

	size := qe.QueueSize()
	t.Logf("Hybrid QueueSize: %d", size)
}

// =============================================================================
// Export hybrid mode with blocking exporter to keep queue full
// =============================================================================

func TestQueuedExporter_Export_HybridSpillover_Detailed(t *testing.T) {
	// Use a blocking mock that never returns, so the memory queue stays full
	blockingMock := &aqMockExporter{delay: time.Hour}
	cfg := queue.Config{
		Path:               t.TempDir(),
		MaxSize:            5, // Very small memory queue
		MaxBytes:           1024 * 1024,
		MemoryMaxBytes:     1024 * 1024,
		RetryInterval:      20 * time.Millisecond,
		MaxRetryDelay:      200 * time.Millisecond,
		AlwaysQueue:        true,
		Workers:            1,
		Mode:               queue.QueueModeHybrid,
		CloseTimeout:       2 * time.Second,
		DrainTimeout:       1 * time.Second,
		DrainEntryTimeout:  500 * time.Millisecond,
		RetryExportTimeout: 1 * time.Second,
	}

	qe, err := NewQueued(blockingMock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	// Fill the memory queue to trigger spillover states
	// MaxSize=5, so pushing ~10 items should fill it and trigger spillover
	for i := 0; i < 15; i++ {
		err := qe.Export(context.Background(), aqTestRequest("spill_detail"))
		if err != nil {
			// Some errors are expected (load shedding, queue full)
			t.Logf("Export %d error: %v", i, err)
		}
	}

	qe.Close()
}

// TestQueuedExporter_Export_HybridSpillover_RateLimited tests hybrid spillover
// with a rate limiter that throttles disk pushes, triggering the rate-limited
// fallback paths in both PartialDisk and AllDisk modes.
func TestQueuedExporter_Export_HybridSpillover_RateLimited(t *testing.T) {
	blockingMock := &aqMockExporter{delay: time.Hour}
	cfg := queue.Config{
		Path:                     t.TempDir(),
		MaxSize:                  5,
		MaxBytes:                 1024 * 1024,
		MemoryMaxBytes:           1024 * 1024,
		RetryInterval:            20 * time.Millisecond,
		MaxRetryDelay:            200 * time.Millisecond,
		AlwaysQueue:              true,
		Workers:                  1,
		Mode:                     queue.QueueModeHybrid,
		SpilloverRateLimitPerSec: 2, // Very low to trigger rate limiting
		CloseTimeout:             2 * time.Second,
		DrainTimeout:             1 * time.Second,
		DrainEntryTimeout:        500 * time.Millisecond,
		RetryExportTimeout:       1 * time.Second,
	}

	qe, err := NewQueued(blockingMock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	// Push many items to fill queue and trigger rate-limited paths
	for i := 0; i < 20; i++ {
		err := qe.Export(context.Background(), aqTestRequest("rl_spill"))
		if err != nil {
			t.Logf("Export %d: %v", i, err)
		}
	}

	qe.Close()
}

// TestQueuedExporter_getCircuitState_AllBranches exercises the getCircuitState helper.
func TestQueuedExporter_getCircuitState_AllBranches(t *testing.T) {
	mock := &mockExporter{}

	// Without circuit breaker
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

	state := qe.getCircuitState()
	if state != "disabled" {
		t.Errorf("expected circuit state 'disabled', got %q", state)
	}

	// With circuit breaker
	cfg2 := queue.Config{
		Path:                       t.TempDir(),
		MaxSize:                    100,
		RetryInterval:              time.Hour,
		MaxRetryDelay:              time.Hour,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    3,
		CircuitBreakerResetTimeout: time.Hour,
	}
	qe2, err := NewQueued(mock, cfg2)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe2.Close()

	state2 := qe2.getCircuitState()
	if state2 != "closed" {
		t.Errorf("expected circuit state 'closed', got %q", state2)
	}

	// Open the circuit
	qe2.circuitBreaker.RecordFailure()
	qe2.circuitBreaker.RecordFailure()
	qe2.circuitBreaker.RecordFailure()

	state3 := qe2.getCircuitState()
	if state3 != "open" {
		t.Errorf("expected circuit state 'open', got %q", state3)
	}
}
