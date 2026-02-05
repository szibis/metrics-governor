package pipeline_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/buffer"
	"github.com/szibis/metrics-governor/internal/exporter"
	"github.com/szibis/metrics-governor/internal/prw"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// --- Mocks ---

// countingExporter records all exported requests and counts datapoints.
type countingExporter struct {
	mu         sync.Mutex
	requests   []*colmetricspb.ExportMetricsServiceRequest
	datapoints int64
}

func (e *countingExporter) Export(_ context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	dp := countDatapoints(req.ResourceMetrics)
	e.mu.Lock()
	e.requests = append(e.requests, req)
	e.datapoints += int64(dp)
	e.mu.Unlock()
	return nil
}

func (e *countingExporter) Close() error { return nil }

func (e *countingExporter) getDatapoints() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.datapoints
}

// failingExporter always returns an error.
type failingExporter struct {
	mu        sync.Mutex
	callCount int64
}

func (e *failingExporter) Export(_ context.Context, _ *colmetricspb.ExportMetricsServiceRequest) error {
	e.mu.Lock()
	e.callCount++
	e.mu.Unlock()
	return &exporter.ExportError{
		Err:        fmt.Errorf("server error"),
		Type:       "server_error",
		StatusCode: 500,
		Message:    "server error",
	}
}

func (e *failingExporter) Close() error { return nil }

// noopStatsCollector implements buffer.StatsCollector as a no-op.
type noopStatsCollector struct{}

func (n *noopStatsCollector) Process([]*metricspb.ResourceMetrics)  {}
func (n *noopStatsCollector) RecordReceived(int)                     {}
func (n *noopStatsCollector) RecordExport(int)                       {}
func (n *noopStatsCollector) RecordExportError()                     {}
func (n *noopStatsCollector) RecordOTLPBytesReceived(int)            {}
func (n *noopStatsCollector) RecordOTLPBytesReceivedCompressed(int)  {}
func (n *noopStatsCollector) RecordOTLPBytesSent(int)                {}
func (n *noopStatsCollector) SetOTLPBufferSize(int)                  {}

// noopLimitsEnforcer passes all metrics through unchanged.
type noopLimitsEnforcer struct{}

func (n *noopLimitsEnforcer) Process(rm []*metricspb.ResourceMetrics) []*metricspb.ResourceMetrics {
	return rm
}

// droppingLimitsEnforcer drops all metrics (simulates limit rules).
type droppingLimitsEnforcer struct {
	mu      sync.Mutex
	dropped int64
}

func (d *droppingLimitsEnforcer) Process(rm []*metricspb.ResourceMetrics) []*metricspb.ResourceMetrics {
	dp := 0
	for _, r := range rm {
		for _, sm := range r.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				switch data := m.Data.(type) {
				case *metricspb.Metric_Gauge:
					dp += len(data.Gauge.GetDataPoints())
				case *metricspb.Metric_Sum:
					dp += len(data.Sum.GetDataPoints())
				}
			}
		}
	}
	d.mu.Lock()
	d.dropped += int64(dp)
	d.mu.Unlock()
	return nil // drop everything
}

func (d *droppingLimitsEnforcer) getDropped() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dropped
}

// noopLogAggregator implements buffer.LogAggregator as a no-op.
type noopLogAggregator struct{}

func (n *noopLogAggregator) Error(string, string, map[string]interface{}, int64) {}
func (n *noopLogAggregator) Stop()                                               {}

// prwCountingExporter implements the prwExporterInterface for PRW pipeline tests.
type prwCountingExporter struct {
	mu         sync.Mutex
	timeseries int
	samples    int
}

func (e *prwCountingExporter) Export(_ context.Context, req *prw.WriteRequest) error {
	if req == nil {
		return nil
	}
	samples := 0
	for i := range req.Timeseries {
		samples += len(req.Timeseries[i].Samples)
	}
	e.mu.Lock()
	e.timeseries += len(req.Timeseries)
	e.samples += samples
	e.mu.Unlock()
	return nil
}

func (e *prwCountingExporter) Close() error { return nil }

// --- Helpers ---

func makeGaugeRM(name string, dpCount int) *metricspb.ResourceMetrics {
	dps := make([]*metricspb.NumberDataPoint, dpCount)
	for i := 0; i < dpCount; i++ {
		dps[i] = &metricspb.NumberDataPoint{
			Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)},
		}
	}
	return &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{
				{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: name}}},
			},
		},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{
				Metrics: []*metricspb.Metric{
					{
						Name: name,
						Data: &metricspb.Metric_Gauge{
							Gauge: &metricspb.Gauge{DataPoints: dps},
						},
					},
				},
			},
		},
	}
}

func makeSumRM(name string, dpCount int) *metricspb.ResourceMetrics {
	dps := make([]*metricspb.NumberDataPoint, dpCount)
	for i := 0; i < dpCount; i++ {
		dps[i] = &metricspb.NumberDataPoint{
			Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)},
		}
	}
	return &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{
				{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: name}}},
			},
		},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{
				Metrics: []*metricspb.Metric{
					{
						Name: name,
						Data: &metricspb.Metric_Sum{
							Sum: &metricspb.Sum{DataPoints: dps},
						},
					},
				},
			},
		},
	}
}

func countDatapoints(rms []*metricspb.ResourceMetrics) int {
	count := 0
	for _, rm := range rms {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				switch d := m.Data.(type) {
				case *metricspb.Metric_Gauge:
					count += len(d.Gauge.GetDataPoints())
				case *metricspb.Metric_Sum:
					count += len(d.Sum.GetDataPoints())
				case *metricspb.Metric_Histogram:
					count += len(d.Histogram.GetDataPoints())
				case *metricspb.Metric_ExponentialHistogram:
					count += len(d.ExponentialHistogram.GetDataPoints())
				case *metricspb.Metric_Summary:
					count += len(d.Summary.GetDataPoints())
				}
			}
		}
	}
	return count
}

// --- Tests ---

// TestDataIntegrity_Pipeline_DatapointsInEqualsOut verifies that every datapoint pushed into
// the buffer comes out through the exporter — zero loss in normal operation.
func TestDataIntegrity_Pipeline_DatapointsInEqualsOut(t *testing.T) {
	tests := []struct {
		name    string
		metrics []*metricspb.ResourceMetrics
		wantDPs int
	}{
		{
			name:    "gauge_10dp",
			metrics: []*metricspb.ResourceMetrics{makeGaugeRM("test_gauge", 10)},
			wantDPs: 10,
		},
		{
			name:    "sum_15dp",
			metrics: []*metricspb.ResourceMetrics{makeSumRM("test_sum", 15)},
			wantDPs: 15,
		},
		{
			name: "mixed_25dp",
			metrics: []*metricspb.ResourceMetrics{
				makeGaugeRM("gauge_a", 10),
				makeSumRM("sum_b", 15),
			},
			wantDPs: 25,
		},
		{
			name: "multi_batch_100dp",
			metrics: func() []*metricspb.ResourceMetrics {
				var rms []*metricspb.ResourceMetrics
				for i := 0; i < 10; i++ {
					rms = append(rms, makeGaugeRM(fmt.Sprintf("gauge_%d", i), 10))
				}
				return rms
			}(),
			wantDPs: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp := &countingExporter{}
			buf := buffer.New(
				1000,                // maxSize
				50,                  // maxBatchSize
				50*time.Millisecond, // flushInterval
				exp,
				&noopStatsCollector{},
				&noopLimitsEnforcer{},
				&noopLogAggregator{},
			)

			ctx, cancel := context.WithCancel(context.Background())
			go buf.Start(ctx)

			buf.Add(tt.metrics)

			// Wait for flush
			time.Sleep(200 * time.Millisecond)
			cancel()
			buf.Wait()

			got := exp.getDatapoints()
			if got != int64(tt.wantDPs) {
				t.Fatalf("exported datapoints = %d, want %d", got, tt.wantDPs)
			}
		})
	}
}

// TestDataIntegrity_Pipeline_PRWDatapointsInEqualsOut verifies that PRW timeseries and sample
// counts match through the PRWQueuedExporter path.
func TestDataIntegrity_Pipeline_PRWDatapointsInEqualsOut(t *testing.T) {
	mockExp := &prwCountingExporter{}

	cfg := exporter.PRWQueueConfig{
		Path:          t.TempDir(),
		MaxSize:       1000,
		RetryInterval: time.Hour,
		MaxRetryDelay: time.Hour,
	}

	qe, err := exporter.NewPRWQueued(mockExp, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued: %v", err)
	}
	defer qe.Close()

	const wantTS = 20
	const wantSamples = 20

	req := &prw.WriteRequest{
		Timeseries: make([]prw.TimeSeries, wantTS),
	}
	for i := 0; i < wantTS; i++ {
		req.Timeseries[i] = prw.TimeSeries{
			Labels:  []prw.Label{{Name: "__name__", Value: fmt.Sprintf("metric_%d", i)}},
			Samples: []prw.Sample{{Value: float64(i), Timestamp: int64(i + 1)}},
		}
	}

	if err := qe.Export(context.Background(), req); err != nil {
		t.Fatalf("Export: %v", err)
	}

	mockExp.mu.Lock()
	gotTS := mockExp.timeseries
	gotSamples := mockExp.samples
	mockExp.mu.Unlock()

	if gotTS != wantTS {
		t.Fatalf("timeseries = %d, want %d", gotTS, wantTS)
	}
	if gotSamples != wantSamples {
		t.Fatalf("samples = %d, want %d", gotSamples, wantSamples)
	}
}

// TestDataIntegrity_Pipeline_DataLossAccounting verifies that received == passed + dropped_by_limits.
func TestDataIntegrity_Pipeline_DataLossAccounting(t *testing.T) {
	exp := &countingExporter{}
	dropper := &droppingLimitsEnforcer{}

	buf := buffer.New(
		1000,
		50,
		50*time.Millisecond,
		exp,
		&noopStatsCollector{},
		dropper,
		&noopLogAggregator{},
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	const totalReceived = 100
	for i := 0; i < 10; i++ {
		buf.Add([]*metricspb.ResourceMetrics{makeGaugeRM(fmt.Sprintf("svc_%d", i), 10)})
	}

	time.Sleep(200 * time.Millisecond)
	cancel()
	buf.Wait()

	exported := exp.getDatapoints()
	dropped := dropper.getDropped()

	if exported+dropped != totalReceived {
		t.Fatalf("accounting mismatch: exported(%d) + dropped(%d) = %d, want %d",
			exported, dropped, exported+dropped, totalReceived)
	}

	if exported != 0 {
		t.Fatalf("expected 0 exported (all dropped), got %d", exported)
	}
	if dropped != totalReceived {
		t.Fatalf("expected %d dropped, got %d", totalReceived, dropped)
	}
}

// TestResilience_Pipeline_CascadingFailure uses a small MemoryQueue failover, always-failing
// exporter, pushes 100 batches. Asserts: no panic, no deadlock (10s watchdog).
func TestResilience_Pipeline_CascadingFailure(t *testing.T) {
	exp := &failingExporter{}
	failover := buffer.NewMemoryQueue(3, 1024*1024)

	buf := buffer.New(
		50,
		10,
		50*time.Millisecond,
		exp,
		&noopStatsCollector{},
		&noopLimitsEnforcer{},
		&noopLogAggregator{},
		buffer.WithFailoverQueue(failover),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	var done atomic.Bool
	go func() {
		time.Sleep(10 * time.Second)
		if !done.Load() {
			t.Error("DEADLOCK: test did not complete within 10 seconds")
			cancel()
		}
	}()

	for i := 0; i < 100; i++ {
		buf.Add([]*metricspb.ResourceMetrics{makeGaugeRM(fmt.Sprintf("cascade_%d", i), 5)})
	}

	time.Sleep(500 * time.Millisecond)
	cancel()
	buf.Wait()
	done.Store(true)

	if failover.Len() > 3 {
		t.Fatalf("failover queue size %d exceeds max 3", failover.Len())
	}
}
