package functional

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"

	"github.com/szibis/metrics-governor/internal/buffer"
	"github.com/szibis/metrics-governor/internal/stats"
)

// bufferMockExporter tracks exports for testing
type bufferMockExporter struct {
	mu           sync.Mutex
	exports      []*colmetrics.ExportMetricsServiceRequest
	exportCount  int64
	failNext     bool
	exportDelay  time.Duration
	closed       bool
}

func (m *bufferMockExporter) Export(ctx context.Context, req *colmetrics.ExportMetricsServiceRequest) error {
	if m.exportDelay > 0 {
		time.Sleep(m.exportDelay)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failNext {
		m.failNext = false
		return context.DeadlineExceeded
	}

	m.exports = append(m.exports, req)
	atomic.AddInt64(&m.exportCount, 1)
	return nil
}

func (m *bufferMockExporter) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *bufferMockExporter) getExportCount() int {
	return int(atomic.LoadInt64(&m.exportCount))
}

func (m *bufferMockExporter) getExports() []*colmetrics.ExportMetricsServiceRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.exports
}

func createTestResourceMetrics(service, metricName string, datapointCount int) *metricspb.ResourceMetrics {
	datapoints := make([]*metricspb.NumberDataPoint, datapointCount)
	for i := 0; i < datapointCount; i++ {
		datapoints[i] = &metricspb.NumberDataPoint{
			TimeUnixNano: uint64(time.Now().UnixNano()),
			Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)},
		}
	}

	return &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{
				{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: service}}},
			},
		},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{
				Metrics: []*metricspb.Metric{
					{
						Name: metricName,
						Data: &metricspb.Metric_Gauge{
							Gauge: &metricspb.Gauge{
								DataPoints: datapoints,
							},
						},
					},
				},
			},
		},
	}
}

// TestFunctional_Buffer_BatchingBehavior tests that buffer batches correctly
func TestFunctional_Buffer_BatchingBehavior(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exp := &bufferMockExporter{}
	statsCollector := stats.NewCollector(nil)

	// Buffer size 100, batch size 10, flush interval 100ms
	buf := buffer.New(100, 10, 100*time.Millisecond, exp, statsCollector, nil, nil)
	go buf.Start(ctx)

	// Add 25 metrics (should trigger 2 batches of 10, leaving 5)
	for i := 0; i < 25; i++ {
		rm := createTestResourceMetrics("test-service", "test_metric", 1)
		buf.Add([]*metricspb.ResourceMetrics{rm})
	}

	// Wait for batches to be processed
	time.Sleep(300 * time.Millisecond)

	// Should have exports - batch size is 10, so 25 metrics should trigger at least 2 exports
	// and flush interval should export remaining
	exportCount := exp.getExportCount()
	if exportCount < 1 {
		t.Errorf("Expected at least 1 export, got %d", exportCount)
	}
	t.Logf("After adding 25 metrics: %d exports", exportCount)

	// Wait for flush interval to ensure all remaining are exported
	time.Sleep(200 * time.Millisecond)

	exportCount = exp.getExportCount()
	t.Logf("After flush interval: %d total exports", exportCount)

	cancel()
	buf.Wait()
}

// TestFunctional_Buffer_FlushInterval tests periodic flushing
func TestFunctional_Buffer_FlushInterval(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	exp := &bufferMockExporter{}
	statsCollector := stats.NewCollector(nil)

	// Large batch size, short flush interval
	buf := buffer.New(1000, 1000, 100*time.Millisecond, exp, statsCollector, nil, nil)
	go buf.Start(ctx)

	// Add just 5 metrics (won't trigger batch size)
	for i := 0; i < 5; i++ {
		rm := createTestResourceMetrics("test-service", "test_metric", 1)
		buf.Add([]*metricspb.ResourceMetrics{rm})
	}

	// Wait for flush interval
	time.Sleep(200 * time.Millisecond)

	exportCount := exp.getExportCount()
	if exportCount != 1 {
		t.Errorf("Expected 1 export from flush interval, got %d", exportCount)
	}

	cancel()
	buf.Wait()
}

// TestFunctional_Buffer_ConcurrentAdds tests concurrent buffer access
func TestFunctional_Buffer_ConcurrentAdds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exp := &bufferMockExporter{}
	statsCollector := stats.NewCollector(nil)

	buf := buffer.New(10000, 100, 50*time.Millisecond, exp, statsCollector, nil, nil)
	go buf.Start(ctx)

	// Concurrent adds from 10 goroutines
	var wg sync.WaitGroup
	metricsPerGoroutine := 100

	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < metricsPerGoroutine; i++ {
				rm := createTestResourceMetrics("concurrent-service", "concurrent_metric", 1)
				buf.Add([]*metricspb.ResourceMetrics{rm})
			}
		}(g)
	}

	wg.Wait()

	// Wait for all exports
	time.Sleep(500 * time.Millisecond)

	// Count total datapoints exported
	exports := exp.getExports()
	totalDatapoints := 0
	for _, req := range exports {
		for _, rm := range req.ResourceMetrics {
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					if gauge := m.GetGauge(); gauge != nil {
						totalDatapoints += len(gauge.DataPoints)
					}
				}
			}
		}
	}

	expected := 10 * metricsPerGoroutine
	if totalDatapoints != expected {
		t.Errorf("Expected %d datapoints, got %d", expected, totalDatapoints)
	}

	cancel()
	buf.Wait()
}

// TestFunctional_Buffer_GracefulShutdown tests that buffer flushes on shutdown
func TestFunctional_Buffer_GracefulShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	exp := &bufferMockExporter{}
	statsCollector := stats.NewCollector(nil)

	// Large batch size, long flush interval (won't trigger naturally)
	buf := buffer.New(10000, 10000, 1*time.Hour, exp, statsCollector, nil, nil)
	go buf.Start(ctx)

	// Add some metrics
	for i := 0; i < 50; i++ {
		rm := createTestResourceMetrics("shutdown-service", "shutdown_metric", 1)
		buf.Add([]*metricspb.ResourceMetrics{rm})
	}

	// Cancel context (graceful shutdown)
	cancel()
	buf.Wait()

	// Should have flushed remaining metrics
	exportCount := exp.getExportCount()
	if exportCount < 1 {
		t.Errorf("Expected at least 1 export on shutdown, got %d", exportCount)
	}
}

// TestFunctional_Buffer_StatsIntegration tests stats collector integration
func TestFunctional_Buffer_StatsIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	exp := &bufferMockExporter{}
	statsCollector := stats.NewCollector([]string{"service"})

	buf := buffer.New(100, 10, 100*time.Millisecond, exp, statsCollector, nil, nil)
	go buf.Start(ctx)

	// Add metrics with different services
	services := []string{"api", "web", "worker"}
	for _, svc := range services {
		for i := 0; i < 5; i++ {
			rm := createTestResourceMetrics(svc, "request_count", 3)
			buf.Add([]*metricspb.ResourceMetrics{rm})
		}
	}

	// Wait for processing
	time.Sleep(300 * time.Millisecond)

	// Check stats
	datapoints, uniqueMetrics, _ := statsCollector.GetGlobalStats()
	if datapoints == 0 {
		t.Error("Expected stats to track datapoints")
	}

	if uniqueMetrics == 0 {
		t.Error("Expected stats to track unique metrics")
	}

	cancel()
	buf.Wait()
}

// TestFunctional_Buffer_HighThroughput tests buffer under high load
func TestFunctional_Buffer_HighThroughput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exp := &bufferMockExporter{}
	statsCollector := stats.NewCollector(nil)

	// Larger buffer and batch for throughput
	buf := buffer.New(100000, 1000, 50*time.Millisecond, exp, statsCollector, nil, nil)
	go buf.Start(ctx)

	// High-volume adds
	start := time.Now()
	metricsCount := 10000

	for i := 0; i < metricsCount; i++ {
		rm := createTestResourceMetrics("throughput-service", "throughput_metric", 5)
		buf.Add([]*metricspb.ResourceMetrics{rm})
	}

	elapsed := time.Since(start)
	t.Logf("Added %d metrics in %v (%.0f metrics/sec)", metricsCount, elapsed, float64(metricsCount)/elapsed.Seconds())

	// Wait for exports
	time.Sleep(500 * time.Millisecond)

	exportCount := exp.getExportCount()
	if exportCount < 10 {
		t.Errorf("Expected at least 10 exports for %d metrics, got %d", metricsCount, exportCount)
	}

	cancel()
	buf.Wait()
}
