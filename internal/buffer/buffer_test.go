package buffer

import (
	"context"
	"sync"
	"testing"
	"time"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// Mock exporter for testing
type mockExporter struct {
	mu       sync.Mutex
	requests []*colmetricspb.ExportMetricsServiceRequest
	err      error
}

func (m *mockExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.requests = append(m.requests, req)
	return nil
}

func (m *mockExporter) getRequestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

func (m *mockExporter) getTotalResourceMetrics() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	total := 0
	for _, req := range m.requests {
		total += len(req.ResourceMetrics)
	}
	return total
}

// Mock stats collector
type mockStatsCollector struct {
	mu             sync.Mutex
	processed      int
	received       int
	exported       int
	exportErrors   int
	bytesReceived  int
	bytesSent      int
	otlpBufferSize int
}

func (m *mockStatsCollector) Process(resourceMetrics []*metricspb.ResourceMetrics) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.processed += len(resourceMetrics)
}

func (m *mockStatsCollector) RecordReceived(count int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.received += count
}

func (m *mockStatsCollector) RecordExport(datapointCount int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exported += datapointCount
}

func (m *mockStatsCollector) RecordExportError() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exportErrors++
}

func (m *mockStatsCollector) RecordOTLPBytesReceived(bytes int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bytesReceived += bytes
}

func (m *mockStatsCollector) RecordOTLPBytesReceivedCompressed(bytes int) {
	// No-op for mock
}

func (m *mockStatsCollector) RecordOTLPBytesSent(bytes int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bytesSent += bytes
}

func (m *mockStatsCollector) SetOTLPBufferSize(size int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.otlpBufferSize = size
}

func (m *mockStatsCollector) getProcessedCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.processed
}

// Mock limits enforcer
type mockLimitsEnforcer struct {
	dropAll bool
}

func (m *mockLimitsEnforcer) Process(resourceMetrics []*metricspb.ResourceMetrics) []*metricspb.ResourceMetrics {
	if m.dropAll {
		return nil
	}
	return resourceMetrics
}

func createTestResourceMetrics(count int) []*metricspb.ResourceMetrics {
	result := make([]*metricspb.ResourceMetrics, count)
	for i := 0; i < count; i++ {
		result[i] = &metricspb.ResourceMetrics{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "test"}}},
				},
			},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{Name: "test_metric"},
					},
				},
			},
		}
	}
	return result
}

func TestNew(t *testing.T) {
	exporter := &mockExporter{}
	buf := New(100, 10, time.Second, exporter, nil, nil, nil)

	if buf == nil {
		t.Fatal("expected non-nil buffer")
	}
	if buf.maxSize != 100 {
		t.Errorf("expected maxSize 100, got %d", buf.maxSize)
	}
	if buf.maxBatchSize != 10 {
		t.Errorf("expected maxBatchSize 10, got %d", buf.maxBatchSize)
	}
	if buf.flushInterval != time.Second {
		t.Errorf("expected flushInterval 1s, got %v", buf.flushInterval)
	}
}

func TestAdd(t *testing.T) {
	exporter := &mockExporter{}
	buf := New(100, 10, time.Hour, exporter, nil, nil, nil) // Long interval to prevent auto-flush

	metrics := createTestResourceMetrics(5)
	buf.Add(metrics)

	buf.mu.Lock()
	count := len(buf.metrics)
	buf.mu.Unlock()

	if count != 5 {
		t.Errorf("expected 5 metrics in buffer, got %d", count)
	}
}

func TestAddWithStats(t *testing.T) {
	exporter := &mockExporter{}
	stats := &mockStatsCollector{}
	buf := New(100, 10, time.Hour, exporter, stats, nil, nil)

	metrics := createTestResourceMetrics(5)
	buf.Add(metrics)

	if stats.getProcessedCount() != 5 {
		t.Errorf("expected stats to process 5 metrics, got %d", stats.getProcessedCount())
	}
}

func TestAddWithLimitsEnforcerPass(t *testing.T) {
	exporter := &mockExporter{}
	limits := &mockLimitsEnforcer{dropAll: false}
	buf := New(100, 10, time.Hour, exporter, nil, limits, nil)

	metrics := createTestResourceMetrics(5)
	buf.Add(metrics)

	buf.mu.Lock()
	count := len(buf.metrics)
	buf.mu.Unlock()

	if count != 5 {
		t.Errorf("expected 5 metrics in buffer, got %d", count)
	}
}

func TestAddWithLimitsEnforcerDrop(t *testing.T) {
	exporter := &mockExporter{}
	limits := &mockLimitsEnforcer{dropAll: true}
	buf := New(100, 10, time.Hour, exporter, nil, limits, nil)

	metrics := createTestResourceMetrics(5)
	buf.Add(metrics)

	buf.mu.Lock()
	count := len(buf.metrics)
	buf.mu.Unlock()

	if count != 0 {
		t.Errorf("expected 0 metrics in buffer (all dropped), got %d", count)
	}
}

func TestAddTriggersFlushWhenFull(t *testing.T) {
	exporter := &mockExporter{}
	buf := New(5, 5, time.Hour, exporter, nil, nil, nil) // Buffer size 5

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Add enough metrics to trigger flush
	metrics := createTestResourceMetrics(6)
	buf.Add(metrics)

	// Wait a bit for flush
	time.Sleep(100 * time.Millisecond)

	cancel()
	buf.Wait()

	if exporter.getRequestCount() == 0 {
		t.Error("expected at least one export request")
	}
}

func TestStartAndStop(t *testing.T) {
	exporter := &mockExporter{}
	buf := New(100, 10, 50*time.Millisecond, exporter, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())

	// Add some metrics before starting
	metrics := createTestResourceMetrics(5)
	buf.Add(metrics)

	// Start the buffer flush routine
	go buf.Start(ctx)

	// Wait for periodic flush
	time.Sleep(100 * time.Millisecond)

	// Stop
	cancel()
	buf.Wait()

	if exporter.getRequestCount() == 0 {
		t.Error("expected at least one export request")
	}
}

func TestFlushOnShutdown(t *testing.T) {
	exporter := &mockExporter{}
	buf := New(100, 10, time.Hour, exporter, nil, nil, nil) // Long interval

	ctx, cancel := context.WithCancel(context.Background())

	// Add some metrics
	metrics := createTestResourceMetrics(5)
	buf.Add(metrics)

	// Start and immediately stop
	go buf.Start(ctx)
	cancel()
	buf.Wait()

	// Should have flushed on shutdown
	if exporter.getRequestCount() == 0 {
		t.Error("expected final flush on shutdown")
	}
}

func TestBatching(t *testing.T) {
	exporter := &mockExporter{}
	buf := New(100, 3, 50*time.Millisecond, exporter, nil, nil, nil) // Batch size 3

	ctx, cancel := context.WithCancel(context.Background())

	// Add 7 metrics (should result in 3 batches: 3, 3, 1)
	metrics := createTestResourceMetrics(7)
	buf.Add(metrics)

	go buf.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	cancel()
	buf.Wait()

	// Should have 3 export requests
	if exporter.getRequestCount() != 3 {
		t.Errorf("expected 3 export requests (batches), got %d", exporter.getRequestCount())
	}
	if exporter.getTotalResourceMetrics() != 7 {
		t.Errorf("expected 7 total metrics exported, got %d", exporter.getTotalResourceMetrics())
	}
}

func TestFlushEmptyBuffer(t *testing.T) {
	exporter := &mockExporter{}
	buf := New(100, 10, 50*time.Millisecond, exporter, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())

	// Start without adding any metrics
	go buf.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	cancel()
	buf.Wait()

	// Should not have any export requests (buffer was empty)
	if exporter.getRequestCount() != 0 {
		t.Errorf("expected 0 export requests for empty buffer, got %d", exporter.getRequestCount())
	}
}

func TestExportError(t *testing.T) {
	exporter := &mockExporter{err: context.DeadlineExceeded}
	buf := New(100, 10, 50*time.Millisecond, exporter, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())

	// Add some metrics
	metrics := createTestResourceMetrics(5)
	buf.Add(metrics)

	go buf.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	cancel()
	buf.Wait()

	// Should have attempted export even with error (error is logged but not returned)
	// The metrics are lost on error in current implementation
}

func TestConcurrentAdds(t *testing.T) {
	exporter := &mockExporter{}
	buf := New(1000, 100, time.Hour, exporter, nil, nil, nil)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				metrics := createTestResourceMetrics(1)
				buf.Add(metrics)
			}
		}()
	}

	wg.Wait()

	buf.mu.Lock()
	count := len(buf.metrics)
	buf.mu.Unlock()

	if count != 100 {
		t.Errorf("expected 100 metrics in buffer, got %d", count)
	}
}

func TestWait(t *testing.T) {
	exporter := &mockExporter{}
	buf := New(100, 10, 50*time.Millisecond, exporter, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())

	go buf.Start(ctx)

	// Cancel and wait
	cancel()

	// Wait should not block indefinitely
	done := make(chan struct{})
	go func() {
		buf.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(time.Second):
		t.Error("Wait() blocked for too long")
	}
}
