package stats

import (
	"net/http/httptest"
	"strings"
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// Test OTLP record methods

func TestRecordReceived(t *testing.T) {
	c := NewCollector(nil)

	c.RecordReceived(100)
	c.RecordReceived(50)

	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.datapointsReceived != 150 {
		t.Errorf("expected datapointsReceived=150, got %d", c.datapointsReceived)
	}
}

func TestRecordExport(t *testing.T) {
	c := NewCollector(nil)

	c.RecordExport(100)
	c.RecordExport(200)

	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.batchesSent != 2 {
		t.Errorf("expected batchesSent=2, got %d", c.batchesSent)
	}
	if c.datapointsSent != 300 {
		t.Errorf("expected datapointsSent=300, got %d", c.datapointsSent)
	}
}

func TestRecordExportError(t *testing.T) {
	c := NewCollector(nil)

	c.RecordExportError()
	c.RecordExportError()
	c.RecordExportError()

	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.exportErrors != 3 {
		t.Errorf("expected exportErrors=3, got %d", c.exportErrors)
	}
}

// Test PRW record methods

func TestRecordPRWReceived(t *testing.T) {
	c := NewCollector(nil)

	c.RecordPRWReceived(100, 10)
	c.RecordPRWReceived(200, 20)

	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.prwDatapointsReceived != 300 {
		t.Errorf("expected prwDatapointsReceived=300, got %d", c.prwDatapointsReceived)
	}
	if c.prwTimeseriesReceived != 30 {
		t.Errorf("expected prwTimeseriesReceived=30, got %d", c.prwTimeseriesReceived)
	}
}

func TestRecordPRWExport(t *testing.T) {
	c := NewCollector(nil)

	c.RecordPRWExport(100, 10)
	c.RecordPRWExport(200, 20)

	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.prwBatchesSent != 2 {
		t.Errorf("expected prwBatchesSent=2, got %d", c.prwBatchesSent)
	}
	if c.prwDatapointsSent != 300 {
		t.Errorf("expected prwDatapointsSent=300, got %d", c.prwDatapointsSent)
	}
	if c.prwTimeseriesSent != 30 {
		t.Errorf("expected prwTimeseriesSent=30, got %d", c.prwTimeseriesSent)
	}
}

func TestRecordPRWExportError(t *testing.T) {
	c := NewCollector(nil)

	c.RecordPRWExportError()
	c.RecordPRWExportError()

	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.prwExportErrors != 2 {
		t.Errorf("expected prwExportErrors=2, got %d", c.prwExportErrors)
	}
}

// Test ResetCardinality

func TestResetCardinality(t *testing.T) {
	c := NewCollector([]string{"service"})

	// Add some data
	rm := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{
			createTestMetric("metric_a", []map[string]string{{"id": "1"}, {"id": "2"}}),
			createTestMetric("metric_b", []map[string]string{{"id": "3"}}),
		},
	)
	c.Process([]*metricspb.ResourceMetrics{rm})

	// Verify data exists
	_, _, cardinality := c.GetGlobalStats()
	if cardinality == 0 {
		t.Fatal("expected non-zero cardinality before reset")
	}

	// Reset
	c.ResetCardinality()

	// Verify cardinality is reset but datapoints remain
	datapoints, metrics, newCardinality := c.GetGlobalStats()
	if newCardinality != 0 {
		t.Errorf("expected cardinality=0 after reset, got %d", newCardinality)
	}
	if datapoints != 3 {
		t.Errorf("expected datapoints=3 preserved, got %d", datapoints)
	}
	if metrics != 2 {
		t.Errorf("expected metrics=2 preserved, got %d", metrics)
	}
}

func TestResetCardinalityLargeMap(t *testing.T) {
	c := NewCollector(nil)

	// Create many metrics to trigger map recreation
	for i := 0; i < 10001; i++ {
		rm := createTestResourceMetrics(
			map[string]string{"service": "api"},
			[]*metricspb.Metric{
				createTestMetric(
					"metric_"+string(rune('a'+i%26))+string(rune('0'+i/26)),
					[]map[string]string{{"id": "1"}},
				),
			},
		)
		c.Process([]*metricspb.ResourceMetrics{rm})
	}

	c.mu.RLock()
	metricCount := len(c.metricStats)
	c.mu.RUnlock()

	if metricCount < 10000 {
		t.Skipf("not enough unique metrics created: %d", metricCount)
	}

	// Reset should recreate the map
	c.ResetCardinality()

	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.metricStats) != 0 {
		t.Errorf("expected metricStats to be reset, got %d entries", len(c.metricStats))
	}
}

// Test ServeHTTP with full stats

func TestServeHTTPWithExportStats(t *testing.T) {
	c := NewCollector(nil)

	// Record some export stats
	c.RecordReceived(100)
	c.RecordExport(80)
	c.RecordExportError()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	c.ServeHTTP(w, req)

	body := w.Body.String()

	expectedMetrics := []string{
		"metrics_governor_datapoints_received_total 100",
		"metrics_governor_datapoints_sent_total 80",
		"metrics_governor_batches_sent_total 1",
		"metrics_governor_export_errors_total 1",
	}

	for _, expected := range expectedMetrics {
		if !strings.Contains(body, expected) {
			t.Errorf("expected response to contain '%s'", expected)
		}
	}
}

func TestServeHTTPWithPRWStats(t *testing.T) {
	c := NewCollector(nil)

	// Record some PRW stats
	c.RecordPRWReceived(500, 50)
	c.RecordPRWExport(400, 40)
	c.RecordPRWExportError()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	c.ServeHTTP(w, req)

	body := w.Body.String()

	expectedMetrics := []string{
		"metrics_governor_prw_datapoints_received_total 500",
		"metrics_governor_prw_timeseries_received_total 50",
		"metrics_governor_prw_datapoints_sent_total 400",
		"metrics_governor_prw_timeseries_sent_total 40",
		"metrics_governor_prw_batches_sent_total 1",
		"metrics_governor_prw_export_errors_total 1",
	}

	for _, expected := range expectedMetrics {
		if !strings.Contains(body, expected) {
			t.Errorf("expected response to contain '%s'", expected)
		}
	}
}

func TestServeHTTPWithoutLabelStats(t *testing.T) {
	c := NewCollector(nil) // No track labels

	rm := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{
			createTestMetric("test_metric", []map[string]string{{"method": "GET"}}),
		},
	)
	c.Process([]*metricspb.ResourceMetrics{rm})

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	c.ServeHTTP(w, req)

	body := w.Body.String()

	// Should not have label stats since no track labels configured
	if strings.Contains(body, "metrics_governor_label_datapoints_total") {
		t.Error("expected no label stats without track labels")
	}
}

// Test ExponentialHistogram processing

func TestProcessExponentialHistogramMetric(t *testing.T) {
	c := NewCollector(nil)

	metric := &metricspb.Metric{
		Name: "exp_histogram_metric",
		Data: &metricspb.Metric_ExponentialHistogram{
			ExponentialHistogram: &metricspb.ExponentialHistogram{
				DataPoints: []*metricspb.ExponentialHistogramDataPoint{
					{Attributes: []*commonpb.KeyValue{{Key: "id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "1"}}}}},
					{Attributes: []*commonpb.KeyValue{{Key: "id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "2"}}}}},
					{Attributes: []*commonpb.KeyValue{{Key: "id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "3"}}}}},
				},
			},
		},
	}

	rm := &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{Metrics: []*metricspb.Metric{metric}},
		},
	}

	c.Process([]*metricspb.ResourceMetrics{rm})

	datapoints, _, cardinality := c.GetGlobalStats()
	if datapoints != 3 {
		t.Errorf("expected 3 datapoints, got %d", datapoints)
	}
	if cardinality != 3 {
		t.Errorf("expected 3 cardinality, got %d", cardinality)
	}
}

// Test label tracking with ExponentialHistogram

func TestProcessExponentialHistogramWithLabelTracking(t *testing.T) {
	c := NewCollector([]string{"service"})

	metric := &metricspb.Metric{
		Name: "exp_histogram_metric",
		Data: &metricspb.Metric_ExponentialHistogram{
			ExponentialHistogram: &metricspb.ExponentialHistogram{
				DataPoints: []*metricspb.ExponentialHistogramDataPoint{
					{Attributes: []*commonpb.KeyValue{{Key: "id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "1"}}}}},
				},
			},
		},
	}

	rm := &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{
				{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "api"}}},
			},
		},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{Metrics: []*metricspb.Metric{metric}},
		},
	}

	c.Process([]*metricspb.ResourceMetrics{rm})

	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.labelStats) != 1 {
		t.Errorf("expected 1 label combination, got %d", len(c.labelStats))
	}
}

// Test label tracking with Histogram

func TestProcessHistogramWithLabelTracking(t *testing.T) {
	c := NewCollector([]string{"service"})

	metric := &metricspb.Metric{
		Name: "histogram_metric",
		Data: &metricspb.Metric_Histogram{
			Histogram: &metricspb.Histogram{
				DataPoints: []*metricspb.HistogramDataPoint{
					{Attributes: []*commonpb.KeyValue{{Key: "method", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "GET"}}}}},
				},
			},
		},
	}

	rm := &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{
				{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "api"}}},
			},
		},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{Metrics: []*metricspb.Metric{metric}},
		},
	}

	c.Process([]*metricspb.ResourceMetrics{rm})

	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.labelStats) != 1 {
		t.Errorf("expected 1 label combination, got %d", len(c.labelStats))
	}
}

// Test label tracking with Summary

func TestProcessSummaryWithLabelTracking(t *testing.T) {
	c := NewCollector([]string{"service"})

	metric := &metricspb.Metric{
		Name: "summary_metric",
		Data: &metricspb.Metric_Summary{
			Summary: &metricspb.Summary{
				DataPoints: []*metricspb.SummaryDataPoint{
					{Attributes: []*commonpb.KeyValue{{Key: "quantile", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "0.99"}}}}},
				},
			},
		},
	}

	rm := &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{
				{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "api"}}},
			},
		},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{Metrics: []*metricspb.Metric{metric}},
		},
	}

	c.Process([]*metricspb.ResourceMetrics{rm})

	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.labelStats) != 1 {
		t.Errorf("expected 1 label combination, got %d", len(c.labelStats))
	}
}

// Test label tracking with Gauge

func TestProcessGaugeWithLabelTracking(t *testing.T) {
	c := NewCollector([]string{"service"})

	metric := &metricspb.Metric{
		Name: "gauge_metric",
		Data: &metricspb.Metric_Gauge{
			Gauge: &metricspb.Gauge{
				DataPoints: []*metricspb.NumberDataPoint{
					{Attributes: []*commonpb.KeyValue{{Key: "host", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "server1"}}}}},
				},
			},
		},
	}

	rm := &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{
				{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "api"}}},
			},
		},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{Metrics: []*metricspb.Metric{metric}},
		},
	}

	c.Process([]*metricspb.ResourceMetrics{rm})

	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.labelStats) != 1 {
		t.Errorf("expected 1 label combination, got %d", len(c.labelStats))
	}
}

// Test concurrent access

func TestConcurrentAccess(t *testing.T) {
	c := NewCollector([]string{"service"})

	done := make(chan bool)

	// Writer goroutine
	go func() {
		for i := 0; i < 100; i++ {
			rm := createTestResourceMetrics(
				map[string]string{"service": "api"},
				[]*metricspb.Metric{
					createTestMetric("test_metric", []map[string]string{{"id": "1"}}),
				},
			)
			c.Process([]*metricspb.ResourceMetrics{rm})
			c.RecordReceived(10)
			c.RecordExport(5)
		}
		done <- true
	}()

	// Reader goroutine
	go func() {
		for i := 0; i < 100; i++ {
			c.GetGlobalStats()
		}
		done <- true
	}()

	// Reset goroutine
	go func() {
		for i := 0; i < 10; i++ {
			c.ResetCardinality()
		}
		done <- true
	}()

	// Wait for all goroutines
	<-done
	<-done
	<-done
}

// Test empty resource metrics

func TestProcessEmptyResourceMetrics(t *testing.T) {
	c := NewCollector(nil)

	c.Process([]*metricspb.ResourceMetrics{})

	datapoints, metrics, cardinality := c.GetGlobalStats()

	if datapoints != 0 || metrics != 0 || cardinality != 0 {
		t.Errorf("expected all zeros for empty input")
	}
}

// Test nil resource

func TestProcessNilResource(t *testing.T) {
	c := NewCollector(nil)

	rm := &metricspb.ResourceMetrics{
		Resource: nil,
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{
				Metrics: []*metricspb.Metric{
					createTestMetric("test_metric", []map[string]string{{"id": "1"}}),
				},
			},
		},
	}

	// Should not panic
	c.Process([]*metricspb.ResourceMetrics{rm})

	datapoints, _, _ := c.GetGlobalStats()
	if datapoints != 1 {
		t.Errorf("expected 1 datapoint, got %d", datapoints)
	}
}

// Test multiple scope metrics

func TestProcessMultipleScopeMetrics(t *testing.T) {
	c := NewCollector(nil)

	rm := &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{
				Metrics: []*metricspb.Metric{
					createTestMetric("metric_a", []map[string]string{{"id": "1"}}),
				},
			},
			{
				Metrics: []*metricspb.Metric{
					createTestMetric("metric_b", []map[string]string{{"id": "2"}}),
				},
			},
		},
	}

	c.Process([]*metricspb.ResourceMetrics{rm})

	datapoints, metrics, _ := c.GetGlobalStats()
	if datapoints != 2 {
		t.Errorf("expected 2 datapoints, got %d", datapoints)
	}
	if metrics != 2 {
		t.Errorf("expected 2 metrics, got %d", metrics)
	}
}
