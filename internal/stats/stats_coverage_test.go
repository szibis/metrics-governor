package stats

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// TestStartPeriodicLoggingExtended tests the periodic logging goroutine.
func TestStartPeriodicLoggingExtended(t *testing.T) {
	c := NewCollector(nil)

	// Add some data first
	c.RecordReceived(100)
	c.RecordExport(50)

	// Start with short interval
	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.StartPeriodicLogging(ctx, 50*time.Millisecond)
	}()

	// Let it run for a bit
	time.Sleep(150 * time.Millisecond)

	// Cancel and wait
	cancel()
	wg.Wait()
}

// TestStartPeriodicLoggingWithReset tests that cardinality gets reset periodically.
func TestStartPeriodicLoggingWithReset(t *testing.T) {
	c := NewCollector([]string{"service"})

	// Add data
	rm := createTestResourceMetricsForCoverage(
		map[string]string{"service": "api"},
		"test_metric",
		3,
	)
	c.Process([]*metricspb.ResourceMetrics{rm})

	// Verify cardinality exists
	_, _, cardinality := c.GetGlobalStats()
	if cardinality == 0 {
		t.Fatal("Expected non-zero cardinality")
	}

	// Start periodic logging with very short reset interval
	ctx, cancel := context.WithCancel(context.Background())

	go c.StartPeriodicLogging(ctx, 10*time.Millisecond)

	// Wait enough for reset to happen
	time.Sleep(100 * time.Millisecond)

	cancel()
}

// TestResetCardinalityEmpty tests resetting when maps are empty.
func TestResetCardinalityEmpty(t *testing.T) {
	c := NewCollector(nil)

	// Should not panic on empty collector
	c.ResetCardinality()

	_, _, cardinality := c.GetGlobalStats()
	if cardinality != 0 {
		t.Errorf("Expected 0 cardinality, got %d", cardinality)
	}
}

// TestResetCardinalityPreservesDatapoints tests that datapoints are preserved.
func TestResetCardinalityPreservesDatapoints(t *testing.T) {
	c := NewCollector(nil)

	// Add data
	c.RecordReceived(100)
	c.RecordExport(80)

	c.ResetCardinality()

	c.mu.RLock()
	received := c.datapointsReceived
	sent := c.datapointsSent
	c.mu.RUnlock()

	if received != 100 {
		t.Errorf("Expected received=100, got %d", received)
	}
	if sent != 80 {
		t.Errorf("Expected sent=80, got %d", sent)
	}
}

// TestResetCardinalityLargeLabelMap tests reset with large label map.
func TestResetCardinalityLargeLabelMap(t *testing.T) {
	c := NewCollector([]string{"id"})

	// Create many label combinations to trigger map reset
	for i := 0; i < 5001; i++ {
		rm := &metricspb.ResourceMetrics{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					{Key: "id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: string(rune('a' + i%26))}}},
				},
			},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						createTestMetric("metric", []map[string]string{{"x": "y"}}),
					},
				},
			},
		}
		c.Process([]*metricspb.ResourceMetrics{rm})
	}

	c.ResetCardinality()
}

// TestSetOTLPBufferSize tests buffer size setting.
func TestSetOTLPBufferSize(t *testing.T) {
	c := NewCollector(nil)

	c.SetOTLPBufferSize(1000)

	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.otlpBufferSize != 1000 {
		t.Errorf("Expected buffer size 1000, got %d", c.otlpBufferSize)
	}
}

// TestSetPRWBufferSize tests PRW buffer size setting.
func TestSetPRWBufferSize(t *testing.T) {
	c := NewCollector(nil)

	c.SetPRWBufferSize(2000)

	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.prwBufferSize != 2000 {
		t.Errorf("Expected PRW buffer size 2000, got %d", c.prwBufferSize)
	}
}

// TestServeHTTPWithBufferSizes tests that buffer sizes are reported.
func TestServeHTTPWithBufferSizes(t *testing.T) {
	c := NewCollector(nil)

	c.SetOTLPBufferSize(500)
	c.SetPRWBufferSize(1500)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	c.ServeHTTP(w, req)

	body := w.Body.String()

	if !strings.Contains(body, `metrics_governor_buffer_size{protocol="otlp"} 500`) {
		t.Error("Expected OTLP buffer size metric")
	}
	if !strings.Contains(body, `metrics_governor_buffer_size{protocol="prw"} 1500`) {
		t.Error("Expected PRW buffer size metric")
	}
}

// TestGetGlobalStatsAfterMultipleProcesses tests stats accumulation.
func TestGetGlobalStatsAfterMultipleProcesses(t *testing.T) {
	c := NewCollector(nil)

	// Process multiple batches
	for i := 0; i < 5; i++ {
		rm := createTestResourceMetricsForCoverage(
			map[string]string{"service": "api"},
			"metric_"+string(rune('a'+i)),
			2,
		)
		c.Process([]*metricspb.ResourceMetrics{rm})
	}

	datapoints, metrics, cardinality := c.GetGlobalStats()

	if datapoints != 10 {
		t.Errorf("Expected 10 datapoints, got %d", datapoints)
	}
	if metrics != 5 {
		t.Errorf("Expected 5 metrics, got %d", metrics)
	}
	if cardinality != 10 {
		t.Errorf("Expected 10 cardinality, got %d", cardinality)
	}
}

// TestProcessMetricWithEmptyName tests metric with empty name.
func TestProcessMetricWithEmptyName(t *testing.T) {
	c := NewCollector(nil)

	metric := &metricspb.Metric{
		Name: "",
		Data: &metricspb.Metric_Sum{
			Sum: &metricspb.Sum{
				DataPoints: []*metricspb.NumberDataPoint{{}},
			},
		},
	}

	rm := &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{Metrics: []*metricspb.Metric{metric}},
		},
	}

	// Should not panic
	c.Process([]*metricspb.ResourceMetrics{rm})
}

// TestProcessMultipleResourceMetrics tests processing multiple resource metrics.
func TestProcessMultipleResourceMetrics(t *testing.T) {
	c := NewCollector([]string{"service"})

	rms := []*metricspb.ResourceMetrics{
		createTestResourceMetricsForCoverage(map[string]string{"service": "api"}, "metric_a", 2),
		createTestResourceMetricsForCoverage(map[string]string{"service": "web"}, "metric_b", 3),
		createTestResourceMetricsForCoverage(map[string]string{"service": "worker"}, "metric_c", 1),
	}

	c.Process(rms)

	datapoints, metrics, cardinality := c.GetGlobalStats()

	if datapoints != 6 {
		t.Errorf("Expected 6 datapoints, got %d", datapoints)
	}
	if metrics != 3 {
		t.Errorf("Expected 3 metrics, got %d", metrics)
	}
	if cardinality != 6 {
		t.Errorf("Expected 6 cardinality, got %d", cardinality)
	}
}

// TestLabelStatsTracking tests label stats are properly tracked.
func TestLabelStatsTracking(t *testing.T) {
	c := NewCollector([]string{"service", "env"})

	rm := &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{
				{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "api"}}},
				{Key: "env", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "prod"}}},
			},
		},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{
				Metrics: []*metricspb.Metric{
					createTestMetric("test_metric", []map[string]string{{"id": "1"}, {"id": "2"}}),
				},
			},
		},
	}

	c.Process([]*metricspb.ResourceMetrics{rm})

	c.mu.RLock()
	defer c.mu.RUnlock()

	// Should have one label combination (service=api, env=prod)
	if len(c.labelStats) != 1 {
		t.Errorf("Expected 1 label combination, got %d", len(c.labelStats))
	}

	// The label key should be "service=api,env=prod"
	for key, stats := range c.labelStats {
		if stats.Datapoints != 2 {
			t.Errorf("Expected 2 datapoints for %s, got %d", key, stats.Datapoints)
		}
	}
}

// TestServeHTTPWithLabelStats tests serving metrics with label stats.
func TestServeHTTPWithLabelStats(t *testing.T) {
	c := NewCollector([]string{"service"})

	rm := createTestResourceMetricsForCoverage(
		map[string]string{"service": "api"},
		"test_metric",
		3,
	)
	c.Process([]*metricspb.ResourceMetrics{rm})

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	c.ServeHTTP(w, req)

	body := w.Body.String()

	// Should contain label stats
	if !strings.Contains(body, "metrics_governor_label_datapoints_total") {
		t.Error("Expected label datapoints metric")
	}
	if !strings.Contains(body, "metrics_governor_label_cardinality") {
		t.Error("Expected label cardinality metric")
	}
}

// TestProcessMetricWithUnknownType tests metric with unknown type.
func TestProcessMetricWithUnknownType(t *testing.T) {
	c := NewCollector(nil)

	// Metric with nil Data
	metric := &metricspb.Metric{
		Name: "unknown_type",
		Data: nil,
	}

	rm := &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{Metrics: []*metricspb.Metric{metric}},
		},
	}

	// Should not panic
	c.Process([]*metricspb.ResourceMetrics{rm})

	datapoints, _, _ := c.GetGlobalStats()
	if datapoints != 0 {
		t.Errorf("Expected 0 datapoints for unknown type, got %d", datapoints)
	}
}

// Helper function for test resource metrics
func createTestResourceMetricsForCoverage(resourceAttrs map[string]string, metricName string, dpCount int) *metricspb.ResourceMetrics {
	attrs := make([]*commonpb.KeyValue, 0, len(resourceAttrs))
	for k, v := range resourceAttrs {
		attrs = append(attrs, &commonpb.KeyValue{
			Key:   k,
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}},
		})
	}

	datapoints := make([]*metricspb.NumberDataPoint, dpCount)
	for i := 0; i < dpCount; i++ {
		datapoints[i] = &metricspb.NumberDataPoint{
			Attributes: []*commonpb.KeyValue{
				{Key: "id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: string(rune('0' + i))}}},
			},
		}
	}

	return &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{Attributes: attrs},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{
				Metrics: []*metricspb.Metric{
					{
						Name: metricName,
						Data: &metricspb.Metric_Sum{
							Sum: &metricspb.Sum{DataPoints: datapoints},
						},
					},
				},
			},
		},
	}
}
