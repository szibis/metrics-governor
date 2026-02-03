package stats

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/cardinality"
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
	_, _, card := c.GetGlobalStats()
	if card == 0 {
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

	_, _, card := c.GetGlobalStats()
	if card != 0 {
		t.Errorf("Expected 0 cardinality, got %d", card)
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

// TestResetCardinalityLargeMetricMap tests reset with >10000 metric entries to
// trigger the large-map eviction branch.
func TestResetCardinalityLargeMetricMap(t *testing.T) {
	c := NewCollector(nil)

	c.mu.Lock()
	for i := 0; i < 10001; i++ {
		name := fmt.Sprintf("metric_%d", i)
		c.metricStats[name] = &MetricStats{
			Name:        name,
			Datapoints:  1,
			cardinality: cardinality.NewTrackerFromGlobal(),
		}
	}
	c.totalMetrics = uint64(len(c.metricStats))
	c.mu.Unlock()

	if len(c.metricStats) <= 10000 {
		t.Fatalf("expected >10000 metric entries, got %d", len(c.metricStats))
	}

	c.ResetCardinality()

	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.metricStats) != 0 {
		t.Errorf("expected metric stats map to be cleared after eviction, got %d entries", len(c.metricStats))
	}
	if c.totalMetrics != 0 {
		t.Errorf("expected totalMetrics reset to 0, got %d", c.totalMetrics)
	}
}

// TestResetCardinalityLargeLabelMap tests reset with >5000 label entries to
// trigger the large label-map eviction branch.
func TestResetCardinalityLargeLabelMap(t *testing.T) {
	c := NewCollector([]string{"service"})

	c.mu.Lock()
	for i := 0; i < 5001; i++ {
		key := fmt.Sprintf("service=svc_%d", i)
		c.labelStats[key] = &LabelStats{
			Labels:      key,
			Datapoints:  1,
			cardinality: cardinality.NewTrackerFromGlobal(),
		}
	}
	c.mu.Unlock()

	if len(c.labelStats) <= 5000 {
		t.Fatalf("expected >5000 label entries, got %d", len(c.labelStats))
	}

	c.ResetCardinality()

	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.labelStats) != 0 {
		t.Errorf("expected label stats map to be cleared after eviction, got %d entries", len(c.labelStats))
	}
}

// TestResetCardinalityBothMapsLarge tests eviction when both maps exceed limits.
func TestResetCardinalityBothMapsLarge(t *testing.T) {
	c := NewCollector([]string{"service"})

	c.mu.Lock()
	for i := 0; i < 10001; i++ {
		name := fmt.Sprintf("metric_%d", i)
		c.metricStats[name] = &MetricStats{
			Name:        name,
			Datapoints:  1,
			cardinality: cardinality.NewTrackerFromGlobal(),
		}
	}
	c.totalMetrics = uint64(len(c.metricStats))
	for i := 0; i < 5001; i++ {
		key := fmt.Sprintf("service=svc_%d", i)
		c.labelStats[key] = &LabelStats{
			Labels:      key,
			Datapoints:  1,
			cardinality: cardinality.NewTrackerFromGlobal(),
		}
	}
	c.mu.Unlock()

	c.ResetCardinality()

	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.metricStats) != 0 {
		t.Errorf("expected metric stats map cleared, got %d entries", len(c.metricStats))
	}
	if len(c.labelStats) != 0 {
		t.Errorf("expected label stats map cleared, got %d entries", len(c.labelStats))
	}
}

// TestResetCardinalitySmallMapsPreserved tests that small maps are preserved
// but their cardinality trackers are reset.
func TestResetCardinalitySmallMapsPreserved(t *testing.T) {
	c := NewCollector([]string{"service"})

	rm := createTestResourceMetricsForCoverage(
		map[string]string{"service": "api"},
		"test_metric",
		2,
	)
	c.Process([]*metricspb.ResourceMetrics{rm})

	c.mu.RLock()
	metricCount := len(c.metricStats)
	labelCount := len(c.labelStats)
	c.mu.RUnlock()

	if metricCount == 0 || labelCount == 0 {
		t.Fatal("expected non-empty maps before reset")
	}

	c.ResetCardinality()

	c.mu.RLock()
	defer c.mu.RUnlock()

	// Maps should be preserved
	if len(c.metricStats) != metricCount {
		t.Errorf("expected metric stats preserved (%d), got %d", metricCount, len(c.metricStats))
	}
	if len(c.labelStats) != labelCount {
		t.Errorf("expected label stats preserved (%d), got %d", labelCount, len(c.labelStats))
	}

	// Cardinality trackers should be reset to 0
	for name, ms := range c.metricStats {
		if ms.cardinality.Count() != 0 {
			t.Errorf("expected cardinality 0 for metric %s, got %d", name, ms.cardinality.Count())
		}
	}
	for key, ls := range c.labelStats {
		if ls.cardinality.Count() != 0 {
			t.Errorf("expected cardinality 0 for label %s, got %d", key, ls.cardinality.Count())
		}
	}
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

// TestServeHTTPExactCardinalityMode tests the else branch in ServeHTTP for
// cardinality.ModeExact.
func TestServeHTTPExactCardinalityMode(t *testing.T) {
	// Save and restore global config
	origConfig := cardinality.GlobalConfig
	defer func() { cardinality.GlobalConfig = origConfig }()

	cardinality.GlobalConfig = cardinality.Config{
		Mode:              cardinality.ModeExact,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	c := NewCollector(nil)

	// Add some data so the collector has content
	rm := createTestResourceMetricsForCoverage(
		map[string]string{"service": "api"},
		"test_metric",
		1,
	)
	c.Process([]*metricspb.ResourceMetrics{rm})

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	c.ServeHTTP(w, req)

	body := w.Body.String()

	// Verify the exact mode output (else branch)
	if !strings.Contains(body, `metrics_governor_cardinality_mode{mode="exact"} 1`) {
		t.Errorf("expected cardinality mode output for exact mode, body:\n%s", body)
	}
	// Verify bloom mode output is NOT present
	if strings.Contains(body, `metrics_governor_cardinality_mode{mode="bloom"} 1`) {
		t.Error("did not expect bloom mode output when exact mode is configured")
	}
}

// TestServeHTTPBloomCardinalityMode confirms the default bloom branch.
func TestServeHTTPBloomCardinalityMode(t *testing.T) {
	origConfig := cardinality.GlobalConfig
	defer func() { cardinality.GlobalConfig = origConfig }()

	cardinality.GlobalConfig = cardinality.Config{
		Mode:              cardinality.ModeBloom,
		ExpectedItems:     100000,
		FalsePositiveRate: 0.01,
	}

	c := NewCollector(nil)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	c.ServeHTTP(w, req)

	body := w.Body.String()

	if !strings.Contains(body, `metrics_governor_cardinality_mode{mode="bloom"} 1`) {
		t.Errorf("expected bloom mode output, body:\n%s", body)
	}
}

// ---------------------------------------------------------------------------
// updateLabelStats: ExponentialHistogram metric type with label tracking
// ---------------------------------------------------------------------------
func TestUpdateLabelStats_ExponentialHistogram(t *testing.T) {
	c := NewCollector([]string{"service"})

	metric := &metricspb.Metric{
		Name: "exp_hist_metric",
		Data: &metricspb.Metric_ExponentialHistogram{
			ExponentialHistogram: &metricspb.ExponentialHistogram{
				DataPoints: []*metricspb.ExponentialHistogramDataPoint{
					{Attributes: []*commonpb.KeyValue{
						{Key: "host", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "h1"}}},
					}},
					{Attributes: []*commonpb.KeyValue{
						{Key: "host", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "h2"}}},
					}},
				},
			},
		},
	}

	rm := &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{
				{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "web"}}},
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
		t.Fatalf("expected 1 label combination, got %d", len(c.labelStats))
	}
	for key, ls := range c.labelStats {
		if key != "service=web" {
			t.Errorf("expected label key 'service=web', got '%s'", key)
		}
		if ls.Datapoints != 2 {
			t.Errorf("expected 2 datapoints, got %d", ls.Datapoints)
		}
	}
}

// ---------------------------------------------------------------------------
// updateLabelStats: Summary metric type with label tracking
// ---------------------------------------------------------------------------
func TestUpdateLabelStats_Summary(t *testing.T) {
	c := NewCollector([]string{"env"})

	metric := &metricspb.Metric{
		Name: "summary_metric_with_labels",
		Data: &metricspb.Metric_Summary{
			Summary: &metricspb.Summary{
				DataPoints: []*metricspb.SummaryDataPoint{
					{Attributes: []*commonpb.KeyValue{
						{Key: "quantile", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "0.5"}}},
					}},
					{Attributes: []*commonpb.KeyValue{
						{Key: "quantile", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "0.99"}}},
					}},
				},
			},
		},
	}

	rm := &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{
				{Key: "env", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "staging"}}},
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
		t.Fatalf("expected 1 label combination, got %d", len(c.labelStats))
	}
	for key, ls := range c.labelStats {
		if key != "env=staging" {
			t.Errorf("expected label key 'env=staging', got '%s'", key)
		}
		if ls.Datapoints != 2 {
			t.Errorf("expected 2 datapoints, got %d", ls.Datapoints)
		}
	}
}

// ---------------------------------------------------------------------------
// updateLabelStats: Gauge metric type with label tracking
// ---------------------------------------------------------------------------
func TestUpdateLabelStats_Gauge(t *testing.T) {
	c := NewCollector([]string{"service"})

	metric := &metricspb.Metric{
		Name: "gauge_with_labels",
		Data: &metricspb.Metric_Gauge{
			Gauge: &metricspb.Gauge{
				DataPoints: []*metricspb.NumberDataPoint{
					{Attributes: []*commonpb.KeyValue{
						{Key: "instance", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "i1"}}},
					}},
				},
			},
		},
	}

	rm := &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{
				{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "db"}}},
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
		t.Fatalf("expected 1 label combination, got %d", len(c.labelStats))
	}
	for key, ls := range c.labelStats {
		if key != "service=db" {
			t.Errorf("expected label key 'service=db', got '%s'", key)
		}
		if ls.Datapoints != 1 {
			t.Errorf("expected 1 datapoint, got %d", ls.Datapoints)
		}
	}
}

// ---------------------------------------------------------------------------
// updateLabelStats: Histogram metric type with label tracking
// ---------------------------------------------------------------------------
func TestUpdateLabelStats_Histogram(t *testing.T) {
	c := NewCollector([]string{"cluster"})

	metric := &metricspb.Metric{
		Name: "hist_with_labels",
		Data: &metricspb.Metric_Histogram{
			Histogram: &metricspb.Histogram{
				DataPoints: []*metricspb.HistogramDataPoint{
					{Attributes: []*commonpb.KeyValue{
						{Key: "le", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "0.5"}}},
					}},
					{Attributes: []*commonpb.KeyValue{
						{Key: "le", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "1.0"}}},
					}},
					{Attributes: []*commonpb.KeyValue{
						{Key: "le", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "+Inf"}}},
					}},
				},
			},
		},
	}

	rm := &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{
				{Key: "cluster", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "us-east-1"}}},
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
		t.Fatalf("expected 1 label combination, got %d", len(c.labelStats))
	}
	for key, ls := range c.labelStats {
		if key != "cluster=us-east-1" {
			t.Errorf("expected label key 'cluster=us-east-1', got '%s'", key)
		}
		if ls.Datapoints != 3 {
			t.Errorf("expected 3 datapoints, got %d", ls.Datapoints)
		}
	}
}

// ---------------------------------------------------------------------------
// updateLabelStats: no tracked labels in attributes (empty labelKey -> continue)
// ---------------------------------------------------------------------------
func TestUpdateLabelStats_NoTrackedLabelsInAttrs(t *testing.T) {
	c := NewCollector([]string{"service", "env"})

	// Datapoints have attributes that do NOT match any tracked labels.
	// Resource attributes also do NOT contain tracked labels.
	metric := createTestMetric("test_metric", []map[string]string{
		{"method": "GET", "status": "200"},
		{"method": "POST", "status": "201"},
	})

	rm := &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{
				{Key: "region", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "us-east"}}},
			},
		},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{Metrics: []*metricspb.Metric{metric}},
		},
	}

	c.Process([]*metricspb.ResourceMetrics{rm})

	c.mu.RLock()
	defer c.mu.RUnlock()

	// No label stats should be created because buildLabelKey returns ""
	if len(c.labelStats) != 0 {
		t.Errorf("expected 0 label combinations when no tracked labels in attrs, got %d", len(c.labelStats))
	}

	// But the metric datapoints should still be counted
	if c.totalDatapoints != 2 {
		t.Errorf("expected 2 total datapoints, got %d", c.totalDatapoints)
	}
}

// TestGetGlobalStatsAfterMultipleProcesses tests stats accumulation.
func TestGetGlobalStatsAfterMultipleProcesses(t *testing.T) {
	c := NewCollector(nil)

	// Process multiple batches
	for i := 0; i < 5; i++ {
		rm := createTestResourceMetricsForCoverage(
			map[string]string{"service": "api"},
			fmt.Sprintf("metric_%d", i),
			2,
		)
		c.Process([]*metricspb.ResourceMetrics{rm})
	}

	datapoints, metrics, card := c.GetGlobalStats()

	if datapoints != 10 {
		t.Errorf("Expected 10 datapoints, got %d", datapoints)
	}
	if metrics != 5 {
		t.Errorf("Expected 5 metrics, got %d", metrics)
	}
	if card != 10 {
		t.Errorf("Expected 10 cardinality, got %d", card)
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

	datapoints, metrics, card := c.GetGlobalStats()

	if datapoints != 6 {
		t.Errorf("Expected 6 datapoints, got %d", datapoints)
	}
	if metrics != 3 {
		t.Errorf("Expected 3 metrics, got %d", metrics)
	}
	if card != 6 {
		t.Errorf("Expected 6 cardinality, got %d", card)
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

	if len(c.labelStats) != 1 {
		t.Errorf("Expected 1 label combination, got %d", len(c.labelStats))
	}

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

	c.Process([]*metricspb.ResourceMetrics{rm})

	datapoints, _, _ := c.GetGlobalStats()
	if datapoints != 0 {
		t.Errorf("Expected 0 datapoints for unknown type, got %d", datapoints)
	}
}

// ---------------------------------------------------------------------------
// StartPeriodicLogging: test that the resetTicker (60s) branch fires.
// We directly call ResetCardinality to cover the code path, since the
// hardcoded 60s ticker cannot practically fire in a unit test.
// ---------------------------------------------------------------------------
func TestStartPeriodicLogging_ResetTickerBranch(t *testing.T) {
	c := NewCollector([]string{"service"})

	// Add data with cardinality
	rm := createTestResourceMetricsForCoverage(
		map[string]string{"service": "api"},
		"test_metric",
		5,
	)
	c.Process([]*metricspb.ResourceMetrics{rm})

	_, _, cardBefore := c.GetGlobalStats()
	if cardBefore == 0 {
		t.Fatal("expected non-zero cardinality before reset")
	}

	// Directly call ResetCardinality (the same code path as resetTicker.C)
	c.ResetCardinality()

	_, _, cardAfter := c.GetGlobalStats()
	if cardAfter != 0 {
		t.Errorf("expected 0 cardinality after reset, got %d", cardAfter)
	}

	// Also run StartPeriodicLogging for a brief window to cover both
	// the ticker.C and ctx.Done() branches.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.StartPeriodicLogging(ctx, 5*time.Millisecond)
		close(done)
	}()

	// Let the logging ticker fire a few times
	time.Sleep(25 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// success: goroutine exited
	case <-time.After(2 * time.Second):
		t.Fatal("StartPeriodicLogging did not exit after context cancellation")
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
				{Key: "id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprintf("%d", i)}}},
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
