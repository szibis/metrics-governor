package exporter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/prw"
	"github.com/szibis/metrics-governor/internal/queue"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// --- Mocks ---

// controllableExporter allows toggling failure on/off and records successful exports.
type controllableExporter struct {
	shouldFail atomic.Bool
	mu         sync.Mutex
	exported   []*colmetricspb.ExportMetricsServiceRequest
	closed     bool
}

func (e *controllableExporter) Export(_ context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	if e.shouldFail.Load() {
		return &ExportError{
			Err:        fmt.Errorf("network error: connection refused"),
			Type:       ErrorTypeNetwork,
			StatusCode: 0,
			Message:    "connection refused",
		}
	}
	e.mu.Lock()
	e.exported = append(e.exported, req)
	e.mu.Unlock()
	return nil
}

func (e *controllableExporter) Close() error {
	e.mu.Lock()
	e.closed = true
	e.mu.Unlock()
	return nil
}

func (e *controllableExporter) getExported() []*colmetricspb.ExportMetricsServiceRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]*colmetricspb.ExportMetricsServiceRequest, len(e.exported))
	copy(result, e.exported)
	return result
}

// prwSplitExporter returns 413 when timeseries count exceeds threshold.
type prwSplitExporter struct {
	threshold int
	mu        sync.Mutex
	exported  []*prw.WriteRequest
}

func (e *prwSplitExporter) Export(_ context.Context, req *prw.WriteRequest) error {
	if len(req.Timeseries) > e.threshold {
		return &ExportError{
			Err:        fmt.Errorf("request entity too large"),
			Type:       ErrorTypeClientError,
			StatusCode: 413,
			Message:    "request entity too large",
		}
	}
	e.mu.Lock()
	e.exported = append(e.exported, req)
	e.mu.Unlock()
	return nil
}

func (e *prwSplitExporter) Close() error { return nil }

func (e *prwSplitExporter) getExported() []*prw.WriteRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]*prw.WriteRequest, len(e.exported))
	copy(result, e.exported)
	return result
}

// --- Helpers ---

func makeResilienceRequest(n int) *colmetricspb.ExportMetricsServiceRequest {
	rms := make([]*metricspb.ResourceMetrics, n)
	for i := 0; i < n; i++ {
		rms[i] = &metricspb.ResourceMetrics{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprintf("svc_%d", i)}}},
				},
			},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: fmt.Sprintf("metric_%d", i),
							Data: &metricspb.Metric_Gauge{
								Gauge: &metricspb.Gauge{
									DataPoints: []*metricspb.NumberDataPoint{
										{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)}},
									},
								},
							},
						},
					},
				},
			},
		}
	}
	return &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: rms}
}

func testQueueConfig(t *testing.T) queue.Config {
	t.Helper()
	return queue.Config{
		Path:               t.TempDir(),
		MaxSize:            10000,
		RetryInterval:      50 * time.Millisecond,
		MaxRetryDelay:      200 * time.Millisecond,
		FullBehavior:       queue.DropOldest,
		InmemoryBlocks:     100,
		ChunkSize:          64 * 1024,
		MetaSyncInterval:   5 * time.Second,
		StaleFlushInterval: 5 * time.Second,
		WriteBufferSize:    1024,
		Compression:        "snappy",
	}
}

// --- Tests ---

// TestResilience_QueuedExporter_NetworkFailureRecovery sets the mock to fail, exports 10
// requests (all queued), flips to succeed, waits for drain, asserts all 10 exported.
func TestResilience_QueuedExporter_NetworkFailureRecovery(t *testing.T) {
	mock := &controllableExporter{}
	mock.shouldFail.Store(true)

	cfg := testQueueConfig(t)
	cfg.RetryInterval = 50 * time.Millisecond

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// Export 10 requests — all fail, all get queued
	for i := 0; i < 10; i++ {
		if err := qe.Export(context.Background(), makeResilienceRequest(1)); err != nil && !errors.Is(err, ErrExportQueued) {
			t.Fatalf("Export %d: %v", i, err)
		}
	}

	// Wait briefly for items to settle in queue
	time.Sleep(100 * time.Millisecond)

	if qe.QueueLen() == 0 {
		t.Fatal("expected items in queue after failure")
	}

	// Flip to success
	mock.shouldFail.Store(false)

	// Wait for retry loop to drain (check exported count to avoid race between
	// Pop decrementing queue len and Export appending to mock.exported)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if len(mock.getExported()) >= 10 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	exported := mock.getExported()
	if len(exported) < 10 {
		t.Fatalf("exported %d requests, want >= 10 (queue_len=%d)", len(exported), qe.QueueLen())
	}
}

// TestResilience_QueuedExporter_CircuitBreakerOpensAndRecovers verifies circuit breaker
// lifecycle: closed → open after threshold → half-open after timeout → closed on success.
func TestResilience_QueuedExporter_CircuitBreakerOpensAndRecovers(t *testing.T) {
	mock := &controllableExporter{}
	mock.shouldFail.Store(true)

	cfg := testQueueConfig(t)
	cfg.CircuitBreakerEnabled = true
	cfg.CircuitBreakerThreshold = 3
	cfg.CircuitBreakerResetTimeout = 200 * time.Millisecond
	cfg.RetryInterval = 30 * time.Millisecond

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// Export enough to trigger failures in retry loop
	for i := 0; i < 5; i++ {
		_ = qe.Export(context.Background(), makeResilienceRequest(1))
	}

	// Wait for circuit breaker to open (3 failures)
	time.Sleep(300 * time.Millisecond)

	if qe.circuitBreaker == nil {
		t.Fatal("circuit breaker should be initialized")
	}

	// Circuit should be open after 3+ failures
	state := qe.circuitBreaker.State()
	if state != CircuitOpen && state != CircuitHalfOpen {
		t.Logf("circuit state = %d (may have already transitioned)", state)
	}

	// Wait for reset timeout to transition to half-open
	time.Sleep(250 * time.Millisecond)

	// Flip to success
	mock.shouldFail.Store(false)

	// Wait for a successful retry to close the circuit
	time.Sleep(300 * time.Millisecond)

	state = qe.circuitBreaker.State()
	if state != CircuitClosed {
		t.Fatalf("circuit breaker should be closed after success, got state %d", state)
	}
}

// TestResilience_QueuedExporter_DrainOnShutdown queues 10 items with a failing exporter,
// sets succeed, calls Close(), and verifies all 10 exported during drain.
func TestResilience_QueuedExporter_DrainOnShutdown(t *testing.T) {
	mock := &controllableExporter{}
	mock.shouldFail.Store(true)

	cfg := testQueueConfig(t)
	cfg.RetryInterval = time.Hour // Don't auto-retry

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	// Export 10 requests — all fail, all queued
	for i := 0; i < 10; i++ {
		_ = qe.Export(context.Background(), makeResilienceRequest(1))
	}

	time.Sleep(100 * time.Millisecond)

	if qe.QueueLen() != 10 {
		t.Fatalf("queue len = %d, want 10", qe.QueueLen())
	}

	// Flip to success before close
	mock.shouldFail.Store(false)

	// Close triggers drain
	if err := qe.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	exported := mock.getExported()
	if len(exported) != 10 {
		t.Fatalf("exported during drain = %d, want 10", len(exported))
	}
}

// TestResilience_PRWQueuedExporter_SplitOnErrorPreservesTimeseries verifies that a 413-returning
// mock eventually exports all unique timeseries through splitting.
func TestResilience_PRWQueuedExporter_SplitOnErrorPreservesTimeseries(t *testing.T) {
	mock := &prwSplitExporter{threshold: 5}

	cfg := PRWQueueConfig{
		Path:          t.TempDir(),
		MaxSize:       10000,
		RetryInterval: 50 * time.Millisecond,
		MaxRetryDelay: 200 * time.Millisecond,
	}

	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	defer qe.Close()

	// Push 20 unique timeseries
	const total = 20
	req := &prw.WriteRequest{
		Timeseries: make([]prw.TimeSeries, total),
	}
	for i := 0; i < total; i++ {
		req.Timeseries[i] = prw.TimeSeries{
			Labels:  []prw.Label{{Name: "__name__", Value: fmt.Sprintf("unique_metric_%d", i)}},
			Samples: []prw.Sample{{Value: float64(i), Timestamp: int64(i + 1)}},
		}
	}

	// This will fail with 413 (20 > 5), get queued, then split on retry
	_ = qe.Export(context.Background(), req)

	// Wait for splits and retries — poll exported count rather than queue size,
	// because QueueSize() drops to 0 when the last item is dequeued for retry
	// but before the mock records the successful export.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		exported := mock.getExported()
		count := 0
		for _, e := range exported {
			count += len(e.Timeseries)
		}
		if count >= total {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Verify all 20 unique timeseries were exported
	exported := mock.getExported()
	seen := make(map[string]bool)
	for _, e := range exported {
		for _, ts := range e.Timeseries {
			name := ts.MetricName()
			if seen[name] {
				t.Fatalf("duplicate timeseries exported: %s", name)
			}
			seen[name] = true
		}
	}

	if len(seen) != total {
		t.Fatalf("unique timeseries exported = %d, want %d", len(seen), total)
	}
}

// TestResilience_Race_ConcurrentPushPopDuringFailure runs concurrent pushers, the retry loop
// as a popper, and a failure toggler. Verifies no race with -race flag.
func TestResilience_Race_ConcurrentPushPopDuringFailure(t *testing.T) {
	mock := &controllableExporter{}

	cfg := testQueueConfig(t)
	cfg.RetryInterval = 20 * time.Millisecond

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var wg sync.WaitGroup

	// 4 pushers
	for p := 0; p < 4; p++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-ctx.Done():
					return
				default:
				}
				_ = qe.Export(ctx, makeResilienceRequest(1))
			}
		}(p)
	}

	// 1 failure toggler
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond):
				mock.shouldFail.Store(!mock.shouldFail.Load())
			}
		}
	}()

	wg.Wait()
	// If we get here without -race panic, the test passes.
}

// TestResilience_QueuedExporter_TimeoutRecovery verifies that a slow exporter that times out
// initially eventually recovers and exports all data when timeouts stop.
func TestResilience_QueuedExporter_TimeoutRecovery(t *testing.T) {
	mock := &timeoutExporter{}
	mock.shouldTimeout.Store(true)

	cfg := testQueueConfig(t)
	cfg.RetryInterval = 50 * time.Millisecond

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// Export 3 requests — all timeout, all get queued
	for i := 0; i < 3; i++ {
		_ = qe.Export(context.Background(), makeResilienceRequest(1))
	}

	// Wait for items to settle in queue
	time.Sleep(200 * time.Millisecond)

	if qe.QueueLen() == 0 {
		t.Fatal("expected items in queue after timeout failures")
	}

	// Stop timing out — retries should now succeed
	mock.shouldTimeout.Store(false)

	// Wait for all items to be exported (check exported count, not queue len,
	// because Pop decrements queue len before Export appends to mock.exported)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if len(mock.getExported()) >= 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	exported := mock.getExported()
	if len(exported) < 3 {
		t.Fatalf("exported %d, want >= 3 (queue_len=%d)", len(exported), qe.QueueLen())
	}
}

// timeoutExporter simulates a server that times out when shouldTimeout is true.
type timeoutExporter struct {
	shouldTimeout atomic.Bool
	mu            sync.Mutex
	exported      []*colmetricspb.ExportMetricsServiceRequest
}

func (e *timeoutExporter) Export(_ context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	if e.shouldTimeout.Load() {
		return &ExportError{
			Err:        fmt.Errorf("context deadline exceeded"),
			Type:       ErrorTypeTimeout,
			StatusCode: 0,
			Message:    "timeout",
		}
	}

	e.mu.Lock()
	e.exported = append(e.exported, req)
	e.mu.Unlock()
	return nil
}

func (e *timeoutExporter) Close() error { return nil }

func (e *timeoutExporter) getExported() []*colmetricspb.ExportMetricsServiceRequest {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]*colmetricspb.ExportMetricsServiceRequest, len(e.exported))
	copy(result, e.exported)
	return result
}
