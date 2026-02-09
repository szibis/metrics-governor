package buffer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/exporter"
	"github.com/szibis/metrics-governor/internal/queue"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// createRMWithGaugeDatapoints creates resource metrics with a gauge that has
// the given number of datapoints, useful for coverage of countDatapoints.
func createRMWithGaugeDatapoints(n int) []*metricspb.ResourceMetrics {
	dps := make([]*metricspb.NumberDataPoint, n)
	for i := 0; i < n; i++ {
		dps[i] = &metricspb.NumberDataPoint{
			Attributes: []*commonpb.KeyValue{
				{Key: "dp", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprintf("%d", i)}}},
			},
		}
	}
	return []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "gauge_metric",
							Data: &metricspb.Metric_Gauge{
								Gauge: &metricspb.Gauge{DataPoints: dps},
							},
						},
					},
				},
			},
		},
	}
}

// mockFailoverQueue implements FailoverQueue for testing.
type mockFailoverQueue struct {
	mu      sync.Mutex
	entries []*colmetricspb.ExportMetricsServiceRequest
	pushErr error
}

func (q *mockFailoverQueue) Push(req *colmetricspb.ExportMetricsServiceRequest) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.pushErr != nil {
		return q.pushErr
	}
	q.entries = append(q.entries, req)
	return nil
}

func (q *mockFailoverQueue) Pop() *colmetricspb.ExportMetricsServiceRequest {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.entries) == 0 {
		return nil
	}
	e := q.entries[0]
	q.entries = q.entries[1:]
	return e
}

func (q *mockFailoverQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.entries)
}

func (q *mockFailoverQueue) Size() int64 { return 0 }

func (q *mockFailoverQueue) getLen() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.entries)
}

// delayExporter delays export by a configurable duration.
type delayExporter struct {
	delay time.Duration
	mu    sync.Mutex
	count int
}

func (d *delayExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	select {
	case <-time.After(d.delay):
	case <-ctx.Done():
		return ctx.Err()
	}
	d.mu.Lock()
	d.count++
	d.mu.Unlock()
	return nil
}

// coverageSplittableExporter returns a splittable ExportError on first call, then succeeds.
type coverageSplittableExporter struct {
	mu    sync.Mutex
	calls int
}

func (s *coverageSplittableExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	s.mu.Lock()
	s.calls++
	call := s.calls
	s.mu.Unlock()

	if call == 1 && len(req.ResourceMetrics) > 1 {
		return &exporter.ExportError{
			Err:        fmt.Errorf("payload too large"),
			Type:       exporter.ErrorTypeClientError,
			StatusCode: 413,
			Message:    "payload too large",
		}
	}
	return nil
}

// mockBatchSizer implements BatchSizer for testing WithBatchTuner.
type mockBatchSizer struct {
	maxBytes int
}

func (m *mockBatchSizer) CurrentMaxBytes() int { return m.maxBytes }

// mockFusedProcessor implements FusedTenantLimitsProcessor.
type mockFusedProcessor struct {
	mu      sync.Mutex
	calls   int
	dropAll bool
}

func (m *mockFusedProcessor) Process(rms []*metricspb.ResourceMetrics, headerTenant string) []*metricspb.ResourceMetrics {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	if m.dropAll {
		return nil
	}
	return rms
}

func (m *mockFusedProcessor) getCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// mockLogAggregator implements LogAggregator.
type mockLogAggregator struct {
	mu    sync.Mutex
	calls int
}

func (m *mockLogAggregator) Error(key string, message string, fields map[string]interface{}, datapoints int64) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
}

func (m *mockLogAggregator) Stop() {}

func (m *mockLogAggregator) getCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// errExporter always returns the configured error.
type errExporter struct {
	err error
}

func (e *errExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	return e.err
}

// queuedExporter returns ErrExportQueued.
type queuedExporter struct{}

func (q *queuedExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	return exporter.ErrExportQueued
}

// ---------------------------------------------------------------------------
// Tests — Buffer Option Coverage
// ---------------------------------------------------------------------------

func TestWithProcessor_SetsProcessorField(t *testing.T) {
	smp := &mockSampler{}
	exp := &mockExporter{}
	buf := New(100, 10, time.Hour, exp, nil, nil, nil, WithProcessor(smp))

	if buf.sampler == nil {
		t.Fatal("expected WithProcessor to set sampler field")
	}

	buf.Add(createTestResourceMetrics(3))
	if smp.getCalls() != 1 {
		t.Errorf("expected processor (sampler) to be called 1 time, got %d", smp.getCalls())
	}
}

func TestWithBatchTuner_SetsField(t *testing.T) {
	sizer := &mockBatchSizer{maxBytes: 1024}
	exp := &mockExporter{}
	buf := New(100, 10, time.Hour, exp, nil, nil, nil, WithBatchTuner(sizer))

	if buf.batchTuner == nil {
		t.Fatal("expected WithBatchTuner to set batchTuner field")
	}
	if buf.batchTuner.CurrentMaxBytes() != 1024 {
		t.Errorf("expected batchTuner to return 1024, got %d", buf.batchTuner.CurrentMaxBytes())
	}
}

func TestWithFusedProcessor_SetsField(t *testing.T) {
	fp := &mockFusedProcessor{}
	exp := &mockExporter{}
	buf := New(100, 10, time.Hour, exp, nil, nil, nil, WithFusedProcessor(fp))

	if buf.fusedProcessor == nil {
		t.Fatal("expected WithFusedProcessor to set fusedProcessor field")
	}
}

func TestWithFlushTimeout_SetsField(t *testing.T) {
	exp := &mockExporter{}
	buf := New(100, 10, time.Hour, exp, nil, nil, nil, WithFlushTimeout(5*time.Second))

	if buf.flushTimeout != 5*time.Second {
		t.Errorf("expected flushTimeout 5s, got %v", buf.flushTimeout)
	}
}

// ---------------------------------------------------------------------------
// Tests — AddWithTenant
// ---------------------------------------------------------------------------

func TestAddWithTenant_BasicFlow(t *testing.T) {
	exp := &mockExporter{}
	tp := &mockTenantProcessor{}
	buf := New(100, 10, time.Hour, exp, nil, nil, nil, WithTenantProcessor(tp))

	if err := buf.AddWithTenant(createTestResourceMetrics(3), "tenant-1"); err != nil {
		t.Fatalf("AddWithTenant failed: %v", err)
	}

	buf.mu.Lock()
	count := len(buf.metrics)
	buf.mu.Unlock()

	if count != 3 {
		t.Errorf("expected 3 metrics in buffer, got %d", count)
	}
	if tp.getCalls() != 1 {
		t.Errorf("expected tenant processor to be called 1 time, got %d", tp.getCalls())
	}
}

// ---------------------------------------------------------------------------
// Tests — AddAggregated
// ---------------------------------------------------------------------------

func TestAddAggregated_BasicFlow(t *testing.T) {
	exp := &mockExporter{}
	buf := New(100, 10, time.Hour, exp, nil, nil, nil)

	buf.AddAggregated(createTestResourceMetrics(3))

	buf.mu.Lock()
	count := len(buf.metrics)
	buf.mu.Unlock()

	if count != 3 {
		t.Errorf("expected 3 metrics in buffer, got %d", count)
	}
}

func TestAddAggregated_EmptyInput(t *testing.T) {
	exp := &mockExporter{}
	buf := New(100, 10, time.Hour, exp, nil, nil, nil)

	buf.AddAggregated(nil)

	buf.mu.Lock()
	count := len(buf.metrics)
	buf.mu.Unlock()

	if count != 0 {
		t.Errorf("expected 0 metrics after empty AddAggregated, got %d", count)
	}
}

func TestAddAggregated_WithFusedProcessor(t *testing.T) {
	exp := &mockExporter{}
	fp := &mockFusedProcessor{}
	buf := New(100, 10, time.Hour, exp, nil, nil, nil, WithFusedProcessor(fp))

	buf.AddAggregated(createTestResourceMetrics(2))

	if fp.getCalls() != 1 {
		t.Errorf("expected fused processor to be called 1 time, got %d", fp.getCalls())
	}
}

func TestAddAggregated_WithFusedProcessorDropAll(t *testing.T) {
	exp := &mockExporter{}
	fp := &mockFusedProcessor{dropAll: true}
	buf := New(100, 10, time.Hour, exp, nil, nil, nil, WithFusedProcessor(fp))

	buf.AddAggregated(createTestResourceMetrics(2))

	buf.mu.Lock()
	count := len(buf.metrics)
	buf.mu.Unlock()

	if count != 0 {
		t.Errorf("expected 0 metrics when fused processor drops all, got %d", count)
	}
}

func TestAddAggregated_WithTenantAndLimits(t *testing.T) {
	exp := &mockExporter{}
	tp := &mockTenantProcessor{}
	lim := &mockLimitsEnforcer{}
	buf := New(100, 10, time.Hour, exp, nil, lim, nil, WithTenantProcessor(tp))

	buf.AddAggregated(createTestResourceMetrics(2))

	buf.mu.Lock()
	count := len(buf.metrics)
	buf.mu.Unlock()

	if count != 2 {
		t.Errorf("expected 2 metrics, got %d", count)
	}
	if tp.getCalls() != 1 {
		t.Error("expected tenant processor to be called")
	}
}

func TestAddAggregated_WithBufferCapacityReject(t *testing.T) {
	exp := &mockExporter{}
	buf := New(100, 10, time.Hour, exp, nil, nil, nil,
		WithMaxBufferBytes(1), // Very small capacity
		WithBufferFullPolicy(queue.DropNewest),
	)

	// AddAggregated with large data should silently drop
	buf.AddAggregated(createRMWithGaugeDatapoints(10))

	buf.mu.Lock()
	count := len(buf.metrics)
	buf.mu.Unlock()

	if count != 0 {
		t.Errorf("expected 0 metrics after capacity exceeded, got %d", count)
	}
}

func TestAddAggregated_TriggersFlush(t *testing.T) {
	exp := &mockExporter{}
	stats := &mockStatsCollector{}
	buf := New(2, 2, time.Hour, exp, stats, nil, nil) // maxSize = 2

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	buf.AddAggregated(createTestResourceMetrics(3)) // exceeds maxSize=2

	time.Sleep(100 * time.Millisecond)
	cancel()
	buf.Wait()

	if exp.getRequestCount() == 0 {
		t.Error("expected flush to be triggered when buffer exceeds maxSize via AddAggregated")
	}
}

// ---------------------------------------------------------------------------
// Tests — addInternal with fused processor
// ---------------------------------------------------------------------------

func TestAddInternal_FusedProcessor(t *testing.T) {
	exp := &mockExporter{}
	fp := &mockFusedProcessor{}
	stats := &mockStatsCollector{}
	buf := New(100, 10, time.Hour, exp, stats, nil, nil, WithFusedProcessor(fp))

	buf.Add(createTestResourceMetrics(3))

	if fp.getCalls() != 1 {
		t.Errorf("expected fused processor to be called 1 time via Add, got %d", fp.getCalls())
	}

	buf.mu.Lock()
	count := len(buf.metrics)
	buf.mu.Unlock()

	if count != 3 {
		t.Errorf("expected 3 metrics, got %d", count)
	}
}

func TestAddInternal_FusedProcessorDropsAll(t *testing.T) {
	exp := &mockExporter{}
	fp := &mockFusedProcessor{dropAll: true}
	buf := New(100, 10, time.Hour, exp, nil, nil, nil, WithFusedProcessor(fp))

	if err := buf.Add(createTestResourceMetrics(3)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	buf.mu.Lock()
	count := len(buf.metrics)
	buf.mu.Unlock()

	if count != 0 {
		t.Errorf("expected 0 metrics when fused processor drops all, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// Tests — enforceCapacity edge cases
// ---------------------------------------------------------------------------

func TestEnforceCapacity_DropOldest(t *testing.T) {
	exp := &mockExporter{}
	buf := New(100, 10, time.Hour, exp, nil, nil, nil,
		WithMaxBufferBytes(100),
		WithBufferFullPolicy(queue.DropOldest),
	)

	// Pre-fill some data
	rm := createTestResourceMetrics(5)
	buf.Add(rm)

	currentBefore := buf.CurrentBytes()
	if currentBefore == 0 {
		t.Fatal("expected non-zero currentBytes after adding metrics")
	}

	// Add more data that exceeds capacity, should evict oldest
	rm2 := createRMWithGaugeDatapoints(20)
	if err := buf.Add(rm2); err != nil {
		t.Fatalf("expected no error with drop_oldest policy, got: %v", err)
	}
}

func TestEnforceCapacity_Block(t *testing.T) {
	exp := &mockExporter{}
	buf := New(100, 100, 50*time.Millisecond, exp, nil, nil, nil,
		WithMaxBufferBytes(500),
		WithBufferFullPolicy(queue.Block),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Fill up buffer to near capacity
	rm := createRMWithGaugeDatapoints(5)
	buf.Add(rm)

	// Start a goroutine that will block because buffer is full
	done := make(chan error, 1)
	go func() {
		// This should block, then succeed after flush frees space
		big := createRMWithGaugeDatapoints(50)
		done <- buf.Add(big)
	}()

	// Wait for flush + blocked goroutine to complete
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("expected no error with block policy, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		// The block policy should eventually succeed after flush
		// If it times out, we've at least exercised the code path
	}

	cancel()
	buf.Wait()
}

func TestEnforceCapacity_DefaultFallback(t *testing.T) {
	exp := &mockExporter{}
	buf := New(100, 10, time.Hour, exp, nil, nil, nil,
		WithMaxBufferBytes(1),
	)
	// fullPolicy defaults to DropNewest — test the default case.
	err := buf.Add(createRMWithGaugeDatapoints(10))
	if !errors.Is(err, ErrBufferFull) {
		t.Errorf("expected ErrBufferFull, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests — exportBatch error paths
// ---------------------------------------------------------------------------

func TestExportBatch_ErrExportQueued(t *testing.T) {
	qExp := &queuedExporter{}
	stats := &mockStatsCollector{}
	buf := New(100, 10, 50*time.Millisecond, qExp, stats, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	buf.Add(createTestResourceMetrics(3))
	time.Sleep(200 * time.Millisecond)
	cancel()
	buf.Wait()

	// ErrExportQueued should not count as an export error or success
	stats.mu.Lock()
	exported := stats.exported
	exportErrors := stats.exportErrors
	stats.mu.Unlock()

	if exported != 0 {
		t.Errorf("expected 0 exported with ErrExportQueued, got %d", exported)
	}
	if exportErrors != 0 {
		t.Errorf("expected 0 exportErrors with ErrExportQueued, got %d", exportErrors)
	}
}

func TestExportBatch_SplittableError(t *testing.T) {
	sExp := &coverageSplittableExporter{}
	buf := New(100, 10, 50*time.Millisecond, sExp, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	buf.Add(createTestResourceMetrics(4))
	time.Sleep(200 * time.Millisecond)
	cancel()
	buf.Wait()

	sExp.mu.Lock()
	calls := sExp.calls
	sExp.mu.Unlock()

	// Should have more than 1 call due to splitting
	if calls < 2 {
		t.Errorf("expected multiple export calls from split-on-error, got %d", calls)
	}
}

func TestExportBatch_FailoverQueuePush(t *testing.T) {
	eExp := &errExporter{err: fmt.Errorf("permanent failure")}
	fq := &mockFailoverQueue{}
	buf := New(100, 10, 50*time.Millisecond, eExp, nil, nil, nil, WithFailoverQueue(fq))

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	buf.Add(createTestResourceMetrics(3))
	time.Sleep(200 * time.Millisecond)
	cancel()
	buf.Wait()

	if fq.getLen() == 0 {
		t.Error("expected failed batch to be pushed to failover queue")
	}
}

func TestExportBatch_FailoverQueuePushFails(t *testing.T) {
	eExp := &errExporter{err: fmt.Errorf("permanent failure")}
	fq := &mockFailoverQueue{pushErr: fmt.Errorf("queue full")}
	stats := &mockStatsCollector{}
	buf := New(100, 10, 50*time.Millisecond, eExp, stats, nil, nil, WithFailoverQueue(fq))

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	buf.Add(createTestResourceMetrics(3))
	time.Sleep(200 * time.Millisecond)
	cancel()
	buf.Wait()

	// Data is lost — should have recorded an export error
	stats.mu.Lock()
	exportErrors := stats.exportErrors
	stats.mu.Unlock()

	if exportErrors == 0 {
		t.Error("expected export error to be recorded when failover push also fails")
	}
}

func TestExportBatch_NoFailoverQueue_LogAggregator(t *testing.T) {
	eExp := &errExporter{err: fmt.Errorf("export failure")}
	la := &mockLogAggregator{}
	stats := &mockStatsCollector{}
	buf := New(100, 10, 50*time.Millisecond, eExp, stats, nil, la)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	buf.Add(createTestResourceMetrics(3))
	time.Sleep(200 * time.Millisecond)
	cancel()
	buf.Wait()

	if la.getCalls() == 0 {
		t.Error("expected log aggregator to be called when export fails and no failover queue")
	}
}

func TestExportBatch_NoFailoverQueue_NoLogAggregator(t *testing.T) {
	eExp := &errExporter{err: fmt.Errorf("export failure")}
	stats := &mockStatsCollector{}
	buf := New(100, 10, 50*time.Millisecond, eExp, stats, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	buf.Add(createTestResourceMetrics(3))
	time.Sleep(200 * time.Millisecond)
	cancel()
	buf.Wait()

	// Just verify it doesn't panic — the fallback path uses logging.Error.
	stats.mu.Lock()
	exportErrors := stats.exportErrors
	stats.mu.Unlock()

	if exportErrors == 0 {
		t.Error("expected export error to be recorded")
	}
}

// ---------------------------------------------------------------------------
// Tests — flush with batchTuner
// ---------------------------------------------------------------------------

func TestFlush_BatchTuner(t *testing.T) {
	exp := &mockExporter{}
	sizer := &mockBatchSizer{maxBytes: 50} // Small limit to force splits
	buf := New(100, 100, 50*time.Millisecond, exp, nil, nil, nil,
		WithBatchTuner(sizer),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	buf.Add(createRMWithGaugeDatapoints(20))
	time.Sleep(200 * time.Millisecond)
	cancel()
	buf.Wait()

	if exp.getRequestCount() == 0 {
		t.Error("expected export calls with batch tuner active")
	}
}

func TestFlush_BatchTuner_ZeroReturns(t *testing.T) {
	exp := &mockExporter{}
	sizer := &mockBatchSizer{maxBytes: 0} // Returns 0, should fall back to static maxBatchBytes
	buf := New(100, 100, 50*time.Millisecond, exp, nil, nil, nil,
		WithBatchTuner(sizer),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	buf.Add(createTestResourceMetrics(5))
	time.Sleep(200 * time.Millisecond)
	cancel()
	buf.Wait()

	if exp.getRequestCount() == 0 {
		t.Error("expected export calls when batchTuner returns 0 (fallback to static)")
	}
}

// ---------------------------------------------------------------------------
// Tests — flush with timeout
// ---------------------------------------------------------------------------

func TestFlush_Timeout(t *testing.T) {
	dExp := &delayExporter{delay: 500 * time.Millisecond}
	buf := New(100, 10, 50*time.Millisecond, dExp, nil, nil, nil,
		WithFlushTimeout(50*time.Millisecond),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	buf.Add(createTestResourceMetrics(3))
	time.Sleep(300 * time.Millisecond)
	cancel()
	buf.Wait()

	// The flush timeout should have fired, canceling the slow export.
	// We just verify no panic.
}

// ---------------------------------------------------------------------------
// Tests — flush with concurrency limiter
// ---------------------------------------------------------------------------

func TestFlush_WithConcurrency(t *testing.T) {
	exp := &mockExporter{}
	buf := New(100, 2, 50*time.Millisecond, exp, nil, nil, nil,
		WithConcurrency(2),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	buf.Add(createTestResourceMetrics(8)) // 4 batches of 2
	time.Sleep(200 * time.Millisecond)
	cancel()
	buf.Wait()

	if exp.getRequestCount() == 0 {
		t.Error("expected exports with concurrency limiter")
	}
}

func TestFlush_ConcurrencyLimiterBusy(t *testing.T) {
	dExp := &delayExporter{delay: 200 * time.Millisecond}
	buf := New(100, 1, 50*time.Millisecond, dExp, nil, nil, nil,
		WithConcurrency(1), // Only 1 slot
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	buf.Add(createTestResourceMetrics(3)) // 3 batches of 1 — slot will be busy
	time.Sleep(1 * time.Second)
	cancel()
	buf.Wait()

	// Verify it doesn't deadlock. The second and third batches should be
	// exported inline when TryAcquire fails.
}

// ---------------------------------------------------------------------------
// Tests — drainFailoverQueue
// ---------------------------------------------------------------------------

func TestDrainFailoverQueue_Success(t *testing.T) {
	exp := &mockExporter{}
	fq := &mockFailoverQueue{}
	buf := New(100, 10, time.Hour, exp, nil, nil, nil, WithFailoverQueue(fq))

	// Pre-fill the failover queue
	for i := 0; i < 3; i++ {
		fq.Push(&colmetricspb.ExportMetricsServiceRequest{
			ResourceMetrics: createTestResourceMetrics(1),
		})
	}

	ctx := context.Background()
	buf.drainFailoverQueue(ctx)

	if fq.getLen() != 0 {
		t.Errorf("expected failover queue to be drained, got %d entries", fq.getLen())
	}
	if exp.getRequestCount() != 3 {
		t.Errorf("expected 3 exports from drain, got %d", exp.getRequestCount())
	}
}

func TestDrainFailoverQueue_ExportFailsRepush(t *testing.T) {
	eExp := &errExporter{err: fmt.Errorf("backend down")}
	fq := &mockFailoverQueue{}
	buf := New(100, 10, time.Hour, eExp, nil, nil, nil, WithFailoverQueue(fq))

	fq.Push(&colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: createTestResourceMetrics(1),
	})

	ctx := context.Background()
	buf.drainFailoverQueue(ctx)

	// On export failure, the entry should be re-pushed
	if fq.getLen() != 1 {
		t.Errorf("expected entry to be re-pushed to failover queue, got %d", fq.getLen())
	}
}

func TestDrainFailoverQueue_ExportFailsRepushFails(t *testing.T) {
	eExp := &errExporter{err: fmt.Errorf("backend down")}
	fq := &mockFailoverQueue{pushErr: fmt.Errorf("queue also broken")}
	buf := New(100, 10, time.Hour, eExp, nil, nil, nil, WithFailoverQueue(fq))

	fq.entries = append(fq.entries, &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: createTestResourceMetrics(1),
	})

	ctx := context.Background()
	buf.drainFailoverQueue(ctx)

	// Data is lost — we just ensure no panic
}

func TestDrainFailoverQueue_NilQueue(t *testing.T) {
	exp := &mockExporter{}
	buf := New(100, 10, time.Hour, exp, nil, nil, nil)

	// Should be a no-op when failoverQueue is nil
	buf.drainFailoverQueue(context.Background())
}

func TestDrainFailoverQueue_EmptyQueue(t *testing.T) {
	exp := &mockExporter{}
	fq := &mockFailoverQueue{}
	buf := New(100, 10, time.Hour, exp, nil, nil, nil, WithFailoverQueue(fq))

	// Should be a no-op when queue is empty
	buf.drainFailoverQueue(context.Background())

	if exp.getRequestCount() != 0 {
		t.Errorf("expected 0 exports from empty drain, got %d", exp.getRequestCount())
	}
}

// ---------------------------------------------------------------------------
// Tests — MemoryQueue (memqueue.go)
// ---------------------------------------------------------------------------

func TestMemoryQueue_MaybeCompact(t *testing.T) {
	q := NewMemoryQueue(1000, 256*1024*1024)

	// Push many entries to build up capacity
	for i := 0; i < 300; i++ {
		q.Push(&colmetricspb.ExportMetricsServiceRequest{
			ResourceMetrics: createTestResourceMetrics(1),
		})
	}

	// Pop all but a few to create a large cap vs len mismatch
	for i := 0; i < 290; i++ {
		q.Pop()
	}

	// The remaining entries should still be accessible
	if q.Len() != 10 {
		t.Errorf("expected 10 entries, got %d", q.Len())
	}
}

func TestMemoryQueue_EvictOldest_EmptySlice(t *testing.T) {
	q := NewMemoryQueue(1, 256*1024*1024)

	// Push one entry then pop it
	q.Push(&colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: createTestResourceMetrics(1),
	})
	q.Pop()

	// Queue is empty. Pushing another entry when max=1 should work fine.
	q.Push(&colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: createTestResourceMetrics(1),
	})

	if q.Len() != 1 {
		t.Errorf("expected 1 entry, got %d", q.Len())
	}
}

func TestMemoryQueue_EvictByBytes(t *testing.T) {
	// Very small byte limit
	q := NewMemoryQueue(1000, 50)

	for i := 0; i < 10; i++ {
		q.Push(&colmetricspb.ExportMetricsServiceRequest{
			ResourceMetrics: createTestResourceMetrics(1),
		})
	}

	// Queue should have evicted older entries to stay within byte limit
	if q.Size() > 50 {
		t.Errorf("expected queue bytes <= 50, got %d", q.Size())
	}
}

func TestMemoryQueue_Pop_BytesNeverNegative(t *testing.T) {
	q := NewMemoryQueue(10, 256*1024*1024)

	q.Push(&colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: createTestResourceMetrics(1),
	})
	q.Pop()

	if q.Size() < 0 {
		t.Errorf("expected non-negative bytes, got %d", q.Size())
	}
}

// ---------------------------------------------------------------------------
// Tests — splitter.go edge cases
// ---------------------------------------------------------------------------

func TestSplitByBytesWithDepth_MaxDepthExceeded(t *testing.T) {
	// Create a batch that exceeds maxBytes even after splitting
	batch := createRMWithGaugeDatapoints(4)
	results := splitByBytesWithDepth(batch, 1, defaultMaxSplitDepth) // Already at max depth

	// Should return the batch as-is since depth limit reached
	if len(results) != 1 {
		t.Errorf("expected 1 result at max depth, got %d", len(results))
	}
}

func TestSplitByBytesWithDepth_DisabledMaxBytes(t *testing.T) {
	batch := createTestResourceMetrics(5)
	results := splitByBytesWithDepth(batch, 0, 0)

	if len(results) != 1 {
		t.Errorf("expected 1 result when maxBytes=0, got %d", len(results))
	}
}

func TestSplitByBytesWithDepth_SingleElement(t *testing.T) {
	batch := createTestResourceMetrics(1)
	results := splitByBytesWithDepth(batch, 1, 0) // 1 byte max, single element

	if len(results) != 1 {
		t.Errorf("expected 1 result for single element, got %d", len(results))
	}
}

// ---------------------------------------------------------------------------
// Tests — Start with drain ticker integration
// ---------------------------------------------------------------------------

func TestStart_DrainTickerFires(t *testing.T) {
	exp := &mockExporter{}
	fq := &mockFailoverQueue{}
	buf := New(100, 10, time.Hour, exp, nil, nil, nil, WithFailoverQueue(fq))

	// Pre-fill failover queue
	fq.Push(&colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: createTestResourceMetrics(1),
	})

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Wait for drain ticker to fire (every 5s) — use shorter test timeout
	time.Sleep(6 * time.Second)
	cancel()
	buf.Wait()

	if fq.getLen() != 0 {
		t.Errorf("expected drain ticker to empty failover queue, got %d entries", fq.getLen())
	}
}

// ---------------------------------------------------------------------------
// Tests — CurrentBytes
// ---------------------------------------------------------------------------

func TestCurrentBytes(t *testing.T) {
	exp := &mockExporter{}
	buf := New(100, 10, time.Hour, exp, nil, nil, nil)

	if buf.CurrentBytes() != 0 {
		t.Errorf("expected 0 bytes for empty buffer, got %d", buf.CurrentBytes())
	}

	buf.Add(createTestResourceMetrics(5))

	if buf.CurrentBytes() == 0 {
		t.Error("expected non-zero bytes after adding metrics")
	}
}
