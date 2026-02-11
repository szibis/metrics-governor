package buffer

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/exporter"
	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
)

// --- Mocks ---

// recordingExporter records each exported batch as a slice of ResourceMetrics.
type recordingExporter struct {
	mu      sync.Mutex
	batches [][]*metricspb.ResourceMetrics
}

func (e *recordingExporter) Export(_ context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	e.mu.Lock()
	// Make a copy of the slice to avoid aliasing
	batch := make([]*metricspb.ResourceMetrics, len(req.ResourceMetrics))
	copy(batch, req.ResourceMetrics)
	e.batches = append(e.batches, batch)
	e.mu.Unlock()
	return nil
}

func (e *recordingExporter) Close() error { return nil }

func (e *recordingExporter) getBatches() [][]*metricspb.ResourceMetrics {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([][]*metricspb.ResourceMetrics, len(e.batches))
	copy(result, e.batches)
	return result
}

// splittingExporter returns a 413-like splittable error when batch exceeds maxResourceMetrics.
type splittingExporter struct {
	maxResourceMetrics int
	mu                 sync.Mutex
	exported           [][]*metricspb.ResourceMetrics
}

func (e *splittingExporter) Export(_ context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	if len(req.ResourceMetrics) > e.maxResourceMetrics {
		return &exporter.ExportError{
			Err:        fmt.Errorf("request entity too large"),
			Type:       "client_error",
			StatusCode: 413,
			Message:    "request entity too large",
		}
	}
	e.mu.Lock()
	batch := make([]*metricspb.ResourceMetrics, len(req.ResourceMetrics))
	copy(batch, req.ResourceMetrics)
	e.exported = append(e.exported, batch)
	e.mu.Unlock()
	return nil
}

func (e *splittingExporter) Close() error { return nil }

func (e *splittingExporter) getExported() [][]*metricspb.ResourceMetrics {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([][]*metricspb.ResourceMetrics, len(e.exported))
	copy(result, e.exported)
	return result
}

// integrityStatsCollector is a no-op stats collector for integrity tests.
type integrityStatsCollector struct{}

func (n *integrityStatsCollector) Process([]*metricspb.ResourceMetrics)  {}
func (n *integrityStatsCollector) RecordReceived(int)                    {}
func (n *integrityStatsCollector) RecordExport(int)                      {}
func (n *integrityStatsCollector) RecordExportError()                    {}
func (n *integrityStatsCollector) RecordOTLPBytesReceived(int)           {}
func (n *integrityStatsCollector) RecordOTLPBytesReceivedCompressed(int) {}
func (n *integrityStatsCollector) RecordOTLPBytesSent(int)               {}
func (n *integrityStatsCollector) SetOTLPBufferSize(int)                 {}

// integrityLimitsEnforcer passes all metrics through unchanged.
type integrityLimitsEnforcer struct{}

func (n *integrityLimitsEnforcer) Process(rm []*metricspb.ResourceMetrics) []*metricspb.ResourceMetrics {
	return rm
}

// integrityLogAggregator is a no-op log aggregator.
type integrityLogAggregator struct{}

func (n *integrityLogAggregator) Error(string, string, map[string]interface{}, int64) {}
func (n *integrityLogAggregator) Stop()                                               {}

// --- Helpers ---

func makeIntegrityRM(serviceName string) *metricspb.ResourceMetrics {
	return &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{
				{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: serviceName}}},
			},
		},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{
				Metrics: []*metricspb.Metric{
					{
						Name: "test_metric",
						Data: &metricspb.Metric_Gauge{
							Gauge: &metricspb.Gauge{
								DataPoints: []*metricspb.NumberDataPoint{
									{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 1.0}},
								},
							},
						},
					},
				},
			},
		},
	}
}

// makeLargeRM creates a ResourceMetrics with approximately targetBytes of data.
func makeLargeRM(name string, targetBytes int) *metricspb.ResourceMetrics {
	// Each datapoint is roughly 50-80 bytes when serialized.
	// We use a large attribute value to pad up.
	padding := make([]byte, targetBytes/2)
	for i := range padding {
		padding[i] = 'x'
	}

	return &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{
				{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: name}}},
				{Key: "padding", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: string(padding)}}},
			},
		},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{
				Metrics: []*metricspb.Metric{
					{
						Name: name,
						Data: &metricspb.Metric_Gauge{
							Gauge: &metricspb.Gauge{
								DataPoints: []*metricspb.NumberDataPoint{
									{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 1.0}},
								},
							},
						},
					},
				},
			},
		},
	}
}

func getServiceName(rm *metricspb.ResourceMetrics) string {
	for _, attr := range rm.GetResource().GetAttributes() {
		if attr.Key == "service.name" {
			return attr.GetValue().GetStringValue()
		}
	}
	return ""
}

// --- Tests ---

// TestConsistency_Buffer_FlushCountMatchesBatchSize verifies that with batchSize=10 and 55
// metrics, flush produces 6 export calls (5x10 + 1x5).
func TestConsistency_Buffer_FlushCountMatchesBatchSize(t *testing.T) {
	exp := &recordingExporter{}

	buf := New(
		1000,                // maxSize
		10,                  // maxBatchSize
		50*time.Millisecond, // flushInterval
		exp,
		&integrityStatsCollector{},
		&integrityLimitsEnforcer{},
		&integrityLogAggregator{},
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Add 55 metrics
	metrics := make([]*metricspb.ResourceMetrics, 55)
	for i := 0; i < 55; i++ {
		metrics[i] = makeIntegrityRM(fmt.Sprintf("svc_%d", i))
	}
	buf.Add(metrics)

	// Wait for flush
	time.Sleep(200 * time.Millisecond)
	cancel()
	buf.Wait()

	batches := exp.getBatches()

	// Count total ResourceMetrics across all batches
	totalRM := 0
	for _, batch := range batches {
		totalRM += len(batch)
	}

	if totalRM != 55 {
		t.Fatalf("total ResourceMetrics = %d, want 55", totalRM)
	}

	// With maxBatchSize=10, we expect 6 batches: 5x10 + 1x5
	if len(batches) != 6 {
		t.Fatalf("batch count = %d, want 6 (5x10 + 1x5)", len(batches))
	}

	// Verify batch sizes
	for i, batch := range batches {
		if i < 5 && len(batch) != 10 {
			t.Errorf("batch %d size = %d, want 10", i, len(batch))
		}
		if i == 5 && len(batch) != 5 {
			t.Errorf("batch %d size = %d, want 5", i, len(batch))
		}
	}
}

// TestConsistency_Buffer_ByteSplitNeverExceedsLimit verifies that with large metrics and
// WithMaxBatchBytes, every exported batch stays within the byte limit.
func TestConsistency_Buffer_ByteSplitNeverExceedsLimit(t *testing.T) {
	exp := &recordingExporter{}

	const maxBatchBytes = 200 * 1024 // 200KB

	buf := New(
		1000,
		100, // Large batch size so byte splitting is what matters
		50*time.Millisecond,
		exp,
		&integrityStatsCollector{},
		&integrityLimitsEnforcer{},
		&integrityLogAggregator{},
		WithMaxBatchBytes(maxBatchBytes),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Add 10 metrics, each ~100KB
	metrics := make([]*metricspb.ResourceMetrics, 10)
	for i := 0; i < 10; i++ {
		metrics[i] = makeLargeRM(fmt.Sprintf("large_%d", i), 100*1024)
	}
	buf.Add(metrics)

	time.Sleep(300 * time.Millisecond)
	cancel()
	buf.Wait()

	batches := exp.getBatches()

	// Verify every batch is within byte limit
	for i, batch := range batches {
		size := estimateBatchSize(batch)
		if size > maxBatchBytes && len(batch) > 1 {
			t.Errorf("batch %d: size %d exceeds limit %d (count=%d)", i, size, maxBatchBytes, len(batch))
		}
	}

	// Verify all 10 metrics were exported
	totalRM := 0
	for _, batch := range batches {
		totalRM += len(batch)
	}
	if totalRM != 10 {
		t.Fatalf("total ResourceMetrics = %d, want 10", totalRM)
	}
}

// TestDataIntegrity_Buffer_SplitOnErrorPreservesAllMetrics uses a splittingExporter with
// max=4. Pushes 32 metrics with unique service.name. After flush, verifies all 32 exported
// exactly once.
func TestDataIntegrity_Buffer_SplitOnErrorPreservesAllMetrics(t *testing.T) {
	exp := &splittingExporter{maxResourceMetrics: 4}

	buf := New(
		1000,
		32, // All metrics in one batch
		50*time.Millisecond,
		exp,
		&integrityStatsCollector{},
		&integrityLimitsEnforcer{},
		&integrityLogAggregator{},
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Add 32 metrics with unique service names
	const total = 32
	metrics := make([]*metricspb.ResourceMetrics, total)
	for i := 0; i < total; i++ {
		metrics[i] = makeIntegrityRM(fmt.Sprintf("unique_svc_%d", i))
	}
	buf.Add(metrics)

	time.Sleep(300 * time.Millisecond)
	cancel()
	buf.Wait()

	// Collect all exported service names
	exported := exp.getExported()
	seen := make(map[string]int)
	for _, batch := range exported {
		for _, rm := range batch {
			name := getServiceName(rm)
			seen[name]++
		}
	}

	// Verify all 32 unique service names exported exactly once
	if len(seen) != total {
		t.Fatalf("unique services exported = %d, want %d", len(seen), total)
	}

	for name, count := range seen {
		if count != 1 {
			t.Errorf("service %q exported %d times, want 1", name, count)
		}
	}

	// Verify all batches were within the max
	for i, batch := range exported {
		if len(batch) > 4 {
			t.Errorf("batch %d has %d items, exceeds max 4", i, len(batch))
		}
	}
}
