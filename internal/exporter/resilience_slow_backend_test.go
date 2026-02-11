package exporter

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	"github.com/szibis/metrics-governor/internal/prw"
	"github.com/szibis/metrics-governor/internal/queue"
)

// slowBackendExporter simulates a slow-but-succeeding destination.
// It sleeps for `delay` on each export, but respects context cancellation.
type slowBackendExporter struct {
	delay        time.Duration
	mu           sync.Mutex
	timeoutCount int
	successCount int
}

func (e *slowBackendExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
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

func (e *slowBackendExporter) Close() error { return nil }

func (e *slowBackendExporter) getTimeoutCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.timeoutCount
}

func (e *slowBackendExporter) getSuccessCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.successCount
}

// slowPRWBackendExporter simulates a slow PRW destination with timeout tracking.
type slowPRWBackendExporter struct {
	delay        time.Duration
	mu           sync.Mutex
	timeoutCount int
	successCount int
}

func (e *slowPRWBackendExporter) Export(ctx context.Context, req *prw.WriteRequest) error {
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

func (e *slowPRWBackendExporter) Close() error { return nil }

func (e *slowPRWBackendExporter) getTimeoutCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.timeoutCount
}

// =============================================================================
// Test: Direct Export Timeout — Slow Backend Triggers Circuit Breaker
// =============================================================================

func TestDirectExportTimeout_SlowBackendTriggersCircuitBreaker(t *testing.T) {
	t.Parallel()

	slow := &slowBackendExporter{delay: 20 * time.Second} // Would block forever without timeout
	tmpDir := t.TempDir()

	queueCfg := queue.Config{
		Path:                       tmpDir,
		MaxSize:                    1000,
		RetryInterval:              time.Hour, // Don't retry during test
		MaxRetryDelay:              time.Hour,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    3, // Open after 3 failures
		CircuitBreakerResetTimeout: time.Hour,
		DirectExportTimeout:        200 * time.Millisecond, // Fail fast
		CloseTimeout:               time.Second,
		DrainTimeout:               time.Second,
	}

	qe, err := NewQueued(slow, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued error: %v", err)
	}
	defer qe.Close()

	ctx := context.Background()
	req := createSlowBackendTestRequest()

	// Send 10 requests — first 3 should timeout and trigger CB,
	// remaining 7 should be queued instantly (CB open).
	var queuedCount int
	for i := 0; i < 10; i++ {
		exportErr := qe.Export(ctx, req)
		if exportErr == ErrExportQueued {
			queuedCount++
		}
	}

	// Verify: CB should be open after 3 timeouts
	if qe.circuitBreaker.State() != CircuitOpen {
		t.Errorf("expected circuit breaker state Open, got %v", qe.circuitBreaker.State())
	}

	// At least 3 timeouts on the slow exporter
	if slow.getTimeoutCount() < 3 {
		t.Errorf("expected at least 3 timeouts, got %d", slow.getTimeoutCount())
	}

	// 0 successes (all should have timed out or been queued)
	if slow.getSuccessCount() != 0 {
		t.Errorf("expected 0 successes on slow exporter, got %d", slow.getSuccessCount())
	}

	// Most requests should have been queued
	if queuedCount < 7 {
		t.Errorf("expected at least 7 queued, got %d", queuedCount)
	}

	// Queue should have entries
	if qe.QueueLen() < 7 {
		t.Errorf("expected queue length >= 7, got %d", qe.QueueLen())
	}
}

// =============================================================================
// Test: Direct Export Timeout — Fast Backend Not Affected
// =============================================================================

func TestDirectExportTimeout_FastBackendNotAffected(t *testing.T) {
	t.Parallel()

	fast := &mockExporter{} // Instant success
	tmpDir := t.TempDir()

	queueCfg := queue.Config{
		Path:                       tmpDir,
		MaxSize:                    1000,
		RetryInterval:              time.Hour,
		MaxRetryDelay:              time.Hour,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    3,
		CircuitBreakerResetTimeout: time.Hour,
		DirectExportTimeout:        5 * time.Second, // Won't trigger for fast backend
		CloseTimeout:               time.Second,
		DrainTimeout:               time.Second,
	}

	qe, err := NewQueued(fast, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued error: %v", err)
	}
	defer qe.Close()

	ctx := context.Background()
	req := createSlowBackendTestRequest()

	// Export 10 requests — all should succeed directly
	for i := 0; i < 10; i++ {
		if exportErr := qe.Export(ctx, req); exportErr != nil {
			t.Errorf("export %d failed unexpectedly: %v", i, exportErr)
		}
	}

	// CB should still be closed
	if qe.circuitBreaker.State() != CircuitClosed {
		t.Errorf("expected circuit breaker Closed, got %v", qe.circuitBreaker.State())
	}

	// Queue should be empty
	if qe.QueueLen() != 0 {
		t.Errorf("expected empty queue, got %d", qe.QueueLen())
	}

	// All exports went through
	if fast.getExportCount() != 10 {
		t.Errorf("expected 10 exports, got %d", fast.getExportCount())
	}
}

// =============================================================================
// Test: Half-Open — Only One Probe Allowed
// =============================================================================

func TestHalfOpen_OnlyOneProbeAllowed(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker(1, 10*time.Millisecond) // Opens after 1 failure, resets fast

	// Force CB open
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Fatalf("expected CircuitOpen, got %v", cb.State())
	}

	// Wait for reset timeout → next AllowRequest transitions to half-open
	time.Sleep(20 * time.Millisecond)

	// First AllowRequest: should transition Open → HalfOpen and allow (probe)
	if !cb.AllowRequest() {
		t.Fatal("first AllowRequest in half-open should succeed (probe)")
	}
	if cb.State() != CircuitHalfOpen {
		t.Fatalf("expected CircuitHalfOpen, got %v", cb.State())
	}

	// Concurrent requests should all be rejected while probe is in flight
	const concurrentRequests = 10
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < concurrentRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if cb.AllowRequest() {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	// No additional requests should be allowed while probe is in flight
	if allowed.Load() != 0 {
		t.Errorf("expected 0 additional requests allowed during half-open probe, got %d", allowed.Load())
	}

	// After recording success, probe flag resets and CB closes
	cb.RecordSuccess()
	if cb.State() != CircuitClosed {
		t.Errorf("expected CircuitClosed after success, got %v", cb.State())
	}

	// New requests should be allowed
	if !cb.AllowRequest() {
		t.Error("request should be allowed after CB closed")
	}
}

// =============================================================================
// Test: Half-Open Probe Reset on Failure
// =============================================================================

func TestHalfOpen_ProbeResetOnFailure(t *testing.T) {
	t.Parallel()

	cb := NewCircuitBreaker(1, 10*time.Millisecond)

	// Force to half-open
	cb.RecordFailure()
	time.Sleep(20 * time.Millisecond)
	if !cb.AllowRequest() {
		t.Fatal("probe should be allowed")
	}
	if cb.State() != CircuitHalfOpen {
		t.Fatalf("expected HalfOpen, got %v", cb.State())
	}

	// Record failure — should reopen and reset probe flag
	cb.RecordFailure()
	if cb.State() != CircuitOpen {
		t.Errorf("expected CircuitOpen after half-open failure, got %v", cb.State())
	}

	// Wait for reset timeout again
	time.Sleep(20 * time.Millisecond)

	// Should be able to enter half-open again (probe flag was reset)
	if !cb.AllowRequest() {
		t.Error("probe should be allowed after failure reset")
	}
	if cb.State() != CircuitHalfOpen {
		t.Errorf("expected HalfOpen after second reset, got %v", cb.State())
	}
}

// =============================================================================
// Test: Non-Blocking Concurrency — Slots Full Still Exports
// =============================================================================

func TestNonBlockingConcurrency_SlotsFullExportsDirectly(t *testing.T) {
	t.Parallel()

	// ConcurrencyLimiter with 2 slots
	limiter := NewConcurrencyLimiter(2)

	// Fill both slots
	limiter.Acquire()
	limiter.Acquire()

	// TryAcquire should fail now
	if limiter.TryAcquire() {
		t.Fatal("TryAcquire should return false when all slots are full")
	}

	// Release one
	limiter.Release()

	// Now should succeed
	if !limiter.TryAcquire() {
		t.Fatal("TryAcquire should succeed after release")
	}

	// Clean up
	limiter.Release()
	limiter.Release()
}

// =============================================================================
// Test: PRW Direct Export Timeout — Slow Backend Triggers CB
// =============================================================================

func TestPRWDirectExportTimeout_SlowBackendTriggersCircuitBreaker(t *testing.T) {
	t.Parallel()

	slow := &slowPRWBackendExporter{delay: 20 * time.Second}
	tmpDir := t.TempDir()

	cfg := PRWQueueConfig{
		Path:                    tmpDir,
		MaxSize:                 1000,
		RetryInterval:           time.Hour,
		MaxRetryDelay:           time.Hour,
		CircuitBreakerEnabled:   true,
		CircuitFailureThreshold: 3,
		CircuitResetTimeout:     time.Hour,
		DirectExportTimeout:     200 * time.Millisecond,
		CloseTimeout:            time.Second,
		DrainTimeout:            time.Second,
	}

	qe, err := NewPRWQueued(slow, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued error: %v", err)
	}
	defer qe.Close()

	ctx := context.Background()
	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{Labels: []prw.Label{{Name: "__name__", Value: "test_metric"}}},
		},
	}

	var queuedCount int
	for i := 0; i < 10; i++ {
		exportErr := qe.Export(ctx, req)
		if exportErr == ErrExportQueued {
			queuedCount++
		}
	}

	// CB should be open
	if qe.circuitBreaker.State() != CircuitOpen {
		t.Errorf("expected circuit breaker Open, got %v", qe.circuitBreaker.State())
	}

	// At least 3 timeouts
	if slow.getTimeoutCount() < 3 {
		t.Errorf("expected at least 3 timeouts, got %d", slow.getTimeoutCount())
	}

	// Most requests queued
	if queuedCount < 7 {
		t.Errorf("expected at least 7 queued, got %d", queuedCount)
	}
}

// =============================================================================
// Test: Direct Export Timeout Disabled (0) — No Wrapping
// =============================================================================

func TestDirectExportTimeout_DisabledWhenZero(t *testing.T) {
	t.Parallel()

	// A moderately slow exporter (100ms) — should succeed since no timeout wrapping
	mod := &slowBackendExporter{delay: 100 * time.Millisecond}
	tmpDir := t.TempDir()

	queueCfg := queue.Config{
		Path:                       tmpDir,
		MaxSize:                    1000,
		RetryInterval:              time.Hour,
		MaxRetryDelay:              time.Hour,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    3,
		CircuitBreakerResetTimeout: time.Hour,
		DirectExportTimeout:        0, // Disabled
		CloseTimeout:               time.Second,
		DrainTimeout:               time.Second,
	}

	qe, err := NewQueued(mod, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued error: %v", err)
	}
	defer qe.Close()

	ctx := context.Background()
	req := createSlowBackendTestRequest()

	// With timeout disabled, the 100ms export should succeed
	if exportErr := qe.Export(ctx, req); exportErr != nil {
		t.Errorf("export should succeed with disabled timeout, got: %v", exportErr)
	}

	if mod.getSuccessCount() != 1 {
		t.Errorf("expected 1 success, got %d", mod.getSuccessCount())
	}

	if qe.circuitBreaker.State() != CircuitClosed {
		t.Errorf("CB should remain closed, got %v", qe.circuitBreaker.State())
	}
}

// =============================================================================
// Integration: Slow Destination → CB Opens → Queue Fills → Recovery
// =============================================================================

func TestIntegration_SlowDestination_CBOpensAndQueueFills(t *testing.T) {
	t.Parallel()

	slow := &slowBackendExporter{delay: 20 * time.Second}
	tmpDir := t.TempDir()

	queueCfg := queue.Config{
		Path:                       tmpDir,
		MaxSize:                    10000,
		RetryInterval:              time.Hour, // Don't retry during test
		MaxRetryDelay:              time.Hour,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    5,
		CircuitBreakerResetTimeout: time.Hour,
		DirectExportTimeout:        100 * time.Millisecond,
		CloseTimeout:               time.Second,
		DrainTimeout:               time.Second,
	}

	qe, err := NewQueued(slow, queueCfg)
	if err != nil {
		t.Fatalf("NewQueued error: %v", err)
	}
	defer qe.Close()

	ctx := context.Background()
	req := createSlowBackendTestRequest()

	// Phase 1: Send requests — first few timeout (100ms each), CB opens after 5
	start := time.Now()
	totalSent := 100
	for i := 0; i < totalSent; i++ {
		_ = qe.Export(ctx, req)
	}
	elapsed := time.Since(start)

	// With 100ms timeout and 5 threshold, first 5 take ~500ms,
	// then remaining 95 are queued instantly.
	// Total should be well under 2s (not 100 * 20s = 33 minutes!).
	if elapsed > 5*time.Second {
		t.Errorf("exports took too long (%v) — slow destination is blocking flush", elapsed)
	}

	// CB should be open
	if qe.circuitBreaker.State() != CircuitOpen {
		t.Errorf("expected CB Open, got %v", qe.circuitBreaker.State())
	}

	// Queue should have accumulated entries (most of the 100 requests)
	queueLen := qe.QueueLen()
	if queueLen < 90 {
		t.Errorf("expected queue length >= 90, got %d", queueLen)
	}

	// Exactly 5 timeouts on slow exporter (CB threshold)
	if slow.getTimeoutCount() != 5 {
		t.Errorf("expected 5 timeouts, got %d", slow.getTimeoutCount())
	}

	t.Logf("Integration test: %d sent, %d queued, %d timeouts in %v",
		totalSent, queueLen, slow.getTimeoutCount(), elapsed)
}

// =============================================================================
// Helper
// =============================================================================

func createSlowBackendTestRequest() *colmetricspb.ExportMetricsServiceRequest {
	return &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{Name: "slow_backend_test_metric"},
						},
					},
				},
			},
		},
	}
}
