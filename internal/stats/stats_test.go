package stats

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
)

func createTestMetric(name string, dpAttrs []map[string]string) *metricspb.Metric {
	datapoints := make([]*metricspb.NumberDataPoint, len(dpAttrs))
	for i, attrs := range dpAttrs {
		kvs := make([]*commonpb.KeyValue, 0, len(attrs))
		for k, v := range attrs {
			kvs = append(kvs, &commonpb.KeyValue{
				Key:   k,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}},
			})
		}
		datapoints[i] = &metricspb.NumberDataPoint{
			Attributes: kvs,
		}
	}

	return &metricspb.Metric{
		Name: name,
		Data: &metricspb.Metric_Sum{
			Sum: &metricspb.Sum{
				DataPoints: datapoints,
			},
		},
	}
}

func createTestResourceMetrics(resourceAttrs map[string]string, metrics []*metricspb.Metric) *metricspb.ResourceMetrics {
	kvs := make([]*commonpb.KeyValue, 0, len(resourceAttrs))
	for k, v := range resourceAttrs {
		kvs = append(kvs, &commonpb.KeyValue{
			Key:   k,
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}},
		})
	}

	return &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: kvs,
		},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{
				Metrics: metrics,
			},
		},
	}
}

func TestNewCollector(t *testing.T) {
	trackLabels := []string{"service", "env"}
	c := NewCollector(trackLabels, StatsLevelFull)

	if c == nil {
		t.Fatal("expected non-nil collector")
	}
	if len(c.trackLabels) != 2 {
		t.Errorf("expected 2 track labels, got %d", len(c.trackLabels))
	}
	if c.metricStats == nil {
		t.Error("expected metricStats to be initialized")
	}
	if c.labelStats == nil {
		t.Error("expected labelStats to be initialized")
	}
}

func TestProcess(t *testing.T) {
	c := NewCollector(nil, StatsLevelFull)

	rm := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{
			createTestMetric("http_requests_total", []map[string]string{
				{"method": "GET", "status": "200"},
				{"method": "POST", "status": "201"},
			}),
		},
	)

	c.Process([]*metricspb.ResourceMetrics{rm})

	datapoints, uniqueMetrics, totalCardinality := c.GetGlobalStats()

	if datapoints != 2 {
		t.Errorf("expected 2 datapoints, got %d", datapoints)
	}
	if uniqueMetrics != 1 {
		t.Errorf("expected 1 unique metric, got %d", uniqueMetrics)
	}
	if totalCardinality != 2 {
		t.Errorf("expected 2 total cardinality, got %d", totalCardinality)
	}
}

func TestProcessMultipleMetrics(t *testing.T) {
	c := NewCollector(nil, StatsLevelFull)

	rm := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{
			createTestMetric("metric_a", []map[string]string{{"id": "1"}}),
			createTestMetric("metric_b", []map[string]string{{"id": "2"}, {"id": "3"}}),
			createTestMetric("metric_c", []map[string]string{{"id": "4"}}),
		},
	)

	c.Process([]*metricspb.ResourceMetrics{rm})

	datapoints, uniqueMetrics, _ := c.GetGlobalStats()

	if datapoints != 4 {
		t.Errorf("expected 4 datapoints, got %d", datapoints)
	}
	if uniqueMetrics != 3 {
		t.Errorf("expected 3 unique metrics, got %d", uniqueMetrics)
	}
}

func TestProcessWithLabelTracking(t *testing.T) {
	c := NewCollector([]string{"service", "env"}, StatsLevelFull)

	rm := createTestResourceMetrics(
		map[string]string{"service": "api", "env": "prod"},
		[]*metricspb.Metric{
			createTestMetric("http_requests_total", []map[string]string{
				{"method": "GET"},
				{"method": "POST"},
			}),
		},
	)

	c.Process([]*metricspb.ResourceMetrics{rm})

	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.labelStats) != 1 {
		t.Errorf("expected 1 label combination, got %d", len(c.labelStats))
	}

	// Check the label key
	for key, stats := range c.labelStats {
		if !strings.Contains(key, "service=api") {
			t.Errorf("expected label key to contain 'service=api', got '%s'", key)
		}
		if !strings.Contains(key, "env=prod") {
			t.Errorf("expected label key to contain 'env=prod', got '%s'", key)
		}
		if stats.Datapoints != 2 {
			t.Errorf("expected 2 datapoints for label combo, got %d", stats.Datapoints)
		}
	}
}

func TestProcessCardinality(t *testing.T) {
	c := NewCollector(nil, StatsLevelFull)

	// Same metric with same attributes should have cardinality of 1
	rm1 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{
			createTestMetric("test_metric", []map[string]string{{"id": "1"}}),
		},
	)
	c.Process([]*metricspb.ResourceMetrics{rm1})

	// Same attributes again
	rm2 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{
			createTestMetric("test_metric", []map[string]string{{"id": "1"}}),
		},
	)
	c.Process([]*metricspb.ResourceMetrics{rm2})

	_, _, totalCardinality := c.GetGlobalStats()

	if totalCardinality != 1 {
		t.Errorf("expected cardinality of 1 (same series), got %d", totalCardinality)
	}
}

func TestProcessGaugeMetric(t *testing.T) {
	c := NewCollector(nil, StatsLevelFull)

	metric := &metricspb.Metric{
		Name: "gauge_metric",
		Data: &metricspb.Metric_Gauge{
			Gauge: &metricspb.Gauge{
				DataPoints: []*metricspb.NumberDataPoint{
					{Attributes: []*commonpb.KeyValue{{Key: "id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "1"}}}}},
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

	datapoints, _, _ := c.GetGlobalStats()
	if datapoints != 1 {
		t.Errorf("expected 1 datapoint, got %d", datapoints)
	}
}

func TestProcessHistogramMetric(t *testing.T) {
	c := NewCollector(nil, StatsLevelFull)

	metric := &metricspb.Metric{
		Name: "histogram_metric",
		Data: &metricspb.Metric_Histogram{
			Histogram: &metricspb.Histogram{
				DataPoints: []*metricspb.HistogramDataPoint{
					{Attributes: []*commonpb.KeyValue{{Key: "id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "1"}}}}},
					{Attributes: []*commonpb.KeyValue{{Key: "id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "2"}}}}},
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

	datapoints, _, _ := c.GetGlobalStats()
	if datapoints != 2 {
		t.Errorf("expected 2 datapoints, got %d", datapoints)
	}
}

func TestProcessSummaryMetric(t *testing.T) {
	c := NewCollector(nil, StatsLevelFull)

	metric := &metricspb.Metric{
		Name: "summary_metric",
		Data: &metricspb.Metric_Summary{
			Summary: &metricspb.Summary{
				DataPoints: []*metricspb.SummaryDataPoint{
					{Attributes: []*commonpb.KeyValue{{Key: "id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "1"}}}}},
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

	datapoints, _, _ := c.GetGlobalStats()
	if datapoints != 1 {
		t.Errorf("expected 1 datapoint, got %d", datapoints)
	}
}

func TestServeHTTP(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelFull)

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

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body := w.Body.String()

	// Check for expected metrics
	expectedMetrics := []string{
		"metrics_governor_datapoints_total",
		"metrics_governor_metrics_total",
		"metrics_governor_metric_datapoints_total",
		"metrics_governor_metric_cardinality",
		"metrics_governor_label_datapoints_total",
		"metrics_governor_label_cardinality",
	}

	for _, metric := range expectedMetrics {
		if !bytes.Contains([]byte(body), []byte(metric)) {
			t.Errorf("expected response to contain '%s'", metric)
		}
	}
}

func TestServeHTTPContentType(t *testing.T) {
	c := NewCollector(nil, StatsLevelFull)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	c.ServeHTTP(w, req)

	resp := w.Result()
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/plain") {
		t.Errorf("expected Content-Type to contain 'text/plain', got '%s'", contentType)
	}
}

func TestStartPeriodicLogging(t *testing.T) {
	c := NewCollector(nil, StatsLevelFull)

	// Add some data
	rm := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{
			createTestMetric("test_metric", []map[string]string{{"id": "1"}}),
		},
	)
	c.Process([]*metricspb.ResourceMetrics{rm})

	// Start with a very short interval
	ctx, cancel := context.WithCancel(context.Background())
	go c.StartPeriodicLogging(ctx, 10*time.Millisecond)

	// Let it run for a bit
	time.Sleep(50 * time.Millisecond)

	// Stop
	cancel()

	// Just verify it doesn't panic and stops cleanly
}

func TestBuildLabelKey(t *testing.T) {
	c := NewCollector([]string{"service", "env", "cluster"}, StatsLevelFull)

	tests := []struct {
		name     string
		attrs    map[string]string
		expected string
	}{
		{
			name:     "all labels present",
			attrs:    map[string]string{"service": "api", "env": "prod", "cluster": "us-east"},
			expected: "service=api,env=prod,cluster=us-east",
		},
		{
			name:     "partial labels",
			attrs:    map[string]string{"service": "api", "env": "prod"},
			expected: "service=api,env=prod",
		},
		{
			name:     "no tracked labels",
			attrs:    map[string]string{"other": "value"},
			expected: "",
		},
		{
			name:     "empty attrs",
			attrs:    map[string]string{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := c.buildLabelKey(tt.attrs)
			if result != tt.expected {
				t.Errorf("buildLabelKey() = '%s', expected '%s'", result, tt.expected)
			}
		})
	}
}

func TestParseLabelKey(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		expected map[string]string
	}{
		{
			name:     "multiple labels",
			key:      "service=api,env=prod",
			expected: map[string]string{"service": "api", "env": "prod"},
		},
		{
			name:     "single label",
			key:      "service=api",
			expected: map[string]string{"service": "api"},
		},
		{
			name:     "empty key",
			key:      "",
			expected: map[string]string{},
		},
		{
			name:     "label with equals in value",
			key:      "filter=key=value",
			expected: map[string]string{"filter": "key=value"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseLabelKey(tt.key)
			for k, v := range tt.expected {
				if result[k] != v {
					t.Errorf("parseLabelKey() missing or incorrect value for key '%s': got '%s', expected '%s'", k, result[k], v)
				}
			}
		})
	}
}

func TestFormatLabels(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		expected string
	}{
		{
			name:     "multiple labels sorted",
			labels:   map[string]string{"zebra": "z", "alpha": "a"},
			expected: `alpha="a",zebra="z"`,
		},
		{
			name:     "single label",
			labels:   map[string]string{"service": "api"},
			expected: `service="api"`,
		},
		{
			name:     "empty labels",
			labels:   map[string]string{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatLabels(tt.labels)
			if result != tt.expected {
				t.Errorf("formatLabels() = '%s', expected '%s'", result, tt.expected)
			}
		})
	}
}

func TestExtractAttributes(t *testing.T) {
	attrs := []*commonpb.KeyValue{
		{Key: "str", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "value"}}},
		{Key: "empty", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: ""}}},
		{Key: "int", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 123}}},
		{Key: "nil", Value: nil},
	}

	result := extractAttributes(attrs)

	if result["str"] != "value" {
		t.Errorf("expected str='value', got '%s'", result["str"])
	}
	if _, ok := result["empty"]; ok {
		t.Error("expected empty string to be excluded")
	}
	if _, ok := result["int"]; ok {
		t.Error("expected int value to be excluded")
	}
	if _, ok := result["nil"]; ok {
		t.Error("expected nil value to be excluded")
	}
}

func TestBuildSeriesKeyDualBytes_OverridePriority(t *testing.T) {
	a := map[string]string{"key1": "val1"}
	b := map[string]string{"key1": "override", "key2": "val2"}

	result := string(buildSeriesKeyDualBytes(a, b))

	// dp attrs (b) override resource attrs (a), keys sorted
	expected := "key1=override,key2=val2"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestBuildSeriesKey(t *testing.T) {
	tests := []struct {
		name     string
		attrs    map[string]string
		expected string
	}{
		{
			name:     "multiple attrs sorted",
			attrs:    map[string]string{"z": "1", "a": "2"},
			expected: "a=2,z=1",
		},
		{
			name:     "single attr",
			attrs:    map[string]string{"key": "value"},
			expected: "key=value",
		},
		{
			name:     "empty",
			attrs:    map[string]string{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildSeriesKey(tt.attrs)
			if result != tt.expected {
				t.Errorf("buildSeriesKey() = '%s', expected '%s'", result, tt.expected)
			}
		})
	}
}

// TestProcessFull_MergedAttributeExtraction verifies that the optimized processFull()
// extracts attributes once and uses them for both cardinality and label stats.
func TestProcessFull_MergedAttributeExtraction(t *testing.T) {
	c := NewCollector([]string{"service", "env"}, StatsLevelFull)

	rm := createTestResourceMetrics(
		map[string]string{"service.name": "test-svc"},
		[]*metricspb.Metric{
			createTestMetric("http_requests_total", []map[string]string{
				{"service": "web", "env": "prod", "method": "GET"},
				{"service": "web", "env": "prod", "method": "POST"},
				{"service": "api", "env": "staging", "method": "GET"},
			}),
		},
	)

	c.Process([]*metricspb.ResourceMetrics{rm})

	dp, metrics, _ := c.GetGlobalStats()
	if dp != 3 {
		t.Errorf("expected 3 datapoints, got %d", dp)
	}
	if metrics != 1 {
		t.Errorf("expected 1 metric, got %d", metrics)
	}

	// Verify label stats captured both tracked labels
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.labelStats) == 0 {
		t.Error("expected label stats to be populated")
	}
	// Should have entries for service=web,env=prod and service=api,env=staging
	foundWebProd := false
	foundAPIStagging := false
	for key := range c.labelStats {
		if strings.Contains(key, "service=web") && strings.Contains(key, "env=prod") {
			foundWebProd = true
		}
		if strings.Contains(key, "service=api") && strings.Contains(key, "env=staging") {
			foundAPIStagging = true
		}
	}
	if !foundWebProd {
		t.Error("expected label stats entry for service=web,env=prod")
	}
	if !foundAPIStagging {
		t.Error("expected label stats entry for service=api,env=staging")
	}
}

// TestProcessFull_MergedAttrs_AllMetricTypes verifies that merged attribute extraction
// works correctly for all 5 OTLP metric types.
func TestProcessFull_MergedAttrs_AllMetricTypes(t *testing.T) {
	baseAttrs := []*commonpb.KeyValue{
		{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "test"}}},
	}

	tests := []struct {
		name   string
		metric *metricspb.Metric
		want   uint64 // expected datapoint count
	}{
		{
			name: "gauge",
			metric: &metricspb.Metric{
				Name: "gauge_metric",
				Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{
					DataPoints: []*metricspb.NumberDataPoint{
						{Attributes: baseAttrs},
						{Attributes: baseAttrs},
					},
				}},
			},
			want: 2,
		},
		{
			name: "sum",
			metric: &metricspb.Metric{
				Name: "sum_metric",
				Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{
					DataPoints: []*metricspb.NumberDataPoint{
						{Attributes: baseAttrs},
					},
				}},
			},
			want: 1,
		},
		{
			name: "histogram",
			metric: &metricspb.Metric{
				Name: "histogram_metric",
				Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{
					DataPoints: []*metricspb.HistogramDataPoint{
						{Attributes: baseAttrs, Count: 10, Sum: ptrFloat64(100.0)},
						{Attributes: baseAttrs, Count: 20, Sum: ptrFloat64(200.0)},
						{Attributes: baseAttrs, Count: 30, Sum: ptrFloat64(300.0)},
					},
				}},
			},
			want: 3,
		},
		{
			name: "exponential_histogram",
			metric: &metricspb.Metric{
				Name: "exp_histogram_metric",
				Data: &metricspb.Metric_ExponentialHistogram{ExponentialHistogram: &metricspb.ExponentialHistogram{
					DataPoints: []*metricspb.ExponentialHistogramDataPoint{
						{Attributes: baseAttrs, Count: 5},
					},
				}},
			},
			want: 1,
		},
		{
			name: "summary",
			metric: &metricspb.Metric{
				Name: "summary_metric",
				Data: &metricspb.Metric_Summary{Summary: &metricspb.Summary{
					DataPoints: []*metricspb.SummaryDataPoint{
						{Attributes: baseAttrs, Count: 100},
						{Attributes: baseAttrs, Count: 200},
					},
				}},
			},
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCollector([]string{"service"}, StatsLevelFull)
			rm := &metricspb.ResourceMetrics{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "test"}}},
					},
				},
				ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{tt.metric}}},
			}

			c.Process([]*metricspb.ResourceMetrics{rm})

			dp, _, _ := c.GetGlobalStats()
			if dp != tt.want {
				t.Errorf("expected %d datapoints, got %d", tt.want, dp)
			}
		})
	}
}

func ptrFloat64(v float64) *float64 { return &v }

// TestProcessFull_ReducedLockScope verifies that concurrent processing
// produces consistent results with the reduced lock scope optimization.
func TestProcessFull_ReducedLockScope(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelFull)

	rm := createTestResourceMetrics(
		map[string]string{"service.name": "test"},
		[]*metricspb.Metric{
			createTestMetric("metric_a", []map[string]string{
				{"service": "web"},
			}),
		},
	)

	const goroutines = 4
	const iterations = 100
	done := make(chan struct{}, goroutines)

	for g := 0; g < goroutines; g++ {
		go func() {
			for i := 0; i < iterations; i++ {
				c.Process([]*metricspb.ResourceMetrics{rm})
			}
			done <- struct{}{}
		}()
	}

	for g := 0; g < goroutines; g++ {
		<-done
	}

	dp, metrics, _ := c.GetGlobalStats()
	expectedDP := uint64(goroutines * iterations * 1) // 1 datapoint per metric
	if dp != expectedDP {
		t.Errorf("expected %d datapoints, got %d", expectedDP, dp)
	}
	if metrics != 1 {
		t.Errorf("expected 1 unique metric, got %d", metrics)
	}
}

// TestExtractAllDatapointAttrs_Pooled verifies pooled extraction returns correct attrs.
func TestExtractAllDatapointAttrs_Pooled(t *testing.T) {
	metric := &metricspb.Metric{
		Name: "test_metric",
		Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{
			DataPoints: []*metricspb.NumberDataPoint{
				{Attributes: []*commonpb.KeyValue{
					{Key: "k1", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "v1"}}},
					{Key: "k2", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "v2"}}},
				}},
				{Attributes: []*commonpb.KeyValue{
					{Key: "k3", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "v3"}}},
				}},
			},
		}},
	}

	result := extractAllDatapointAttrs(metric)
	if len(result) != 2 {
		t.Fatalf("expected 2 attr maps, got %d", len(result))
	}
	if result[0]["k1"] != "v1" || result[0]["k2"] != "v2" {
		t.Errorf("first datapoint attrs wrong: %v", result[0])
	}
	if result[1]["k3"] != "v3" {
		t.Errorf("second datapoint attrs wrong: %v", result[1])
	}

	// Return to pool
	for _, m := range result {
		putPooledMap(m)
	}
}

func TestGetGlobalStatsEmpty(t *testing.T) {
	c := NewCollector(nil, StatsLevelFull)

	datapoints, uniqueMetrics, totalCardinality := c.GetGlobalStats()

	if datapoints != 0 {
		t.Errorf("expected 0 datapoints, got %d", datapoints)
	}
	if uniqueMetrics != 0 {
		t.Errorf("expected 0 unique metrics, got %d", uniqueMetrics)
	}
	if totalCardinality != 0 {
		t.Errorf("expected 0 total cardinality, got %d", totalCardinality)
	}
}

func TestCountDatapoints(t *testing.T) {
	c := NewCollector(nil, StatsLevelFull)

	tests := []struct {
		name     string
		metric   *metricspb.Metric
		expected int
	}{
		{
			name: "sum",
			metric: &metricspb.Metric{
				Data: &metricspb.Metric_Sum{
					Sum: &metricspb.Sum{DataPoints: make([]*metricspb.NumberDataPoint, 5)},
				},
			},
			expected: 5,
		},
		{
			name: "gauge",
			metric: &metricspb.Metric{
				Data: &metricspb.Metric_Gauge{
					Gauge: &metricspb.Gauge{DataPoints: make([]*metricspb.NumberDataPoint, 3)},
				},
			},
			expected: 3,
		},
		{
			name: "histogram",
			metric: &metricspb.Metric{
				Data: &metricspb.Metric_Histogram{
					Histogram: &metricspb.Histogram{DataPoints: make([]*metricspb.HistogramDataPoint, 2)},
				},
			},
			expected: 2,
		},
		{
			name: "exponential histogram",
			metric: &metricspb.Metric{
				Data: &metricspb.Metric_ExponentialHistogram{
					ExponentialHistogram: &metricspb.ExponentialHistogram{DataPoints: make([]*metricspb.ExponentialHistogramDataPoint, 4)},
				},
			},
			expected: 4,
		},
		{
			name: "summary",
			metric: &metricspb.Metric{
				Data: &metricspb.Metric_Summary{
					Summary: &metricspb.Summary{DataPoints: make([]*metricspb.SummaryDataPoint, 1)},
				},
			},
			expected: 1,
		},
		{
			name:     "nil data",
			metric:   &metricspb.Metric{},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := c.countDatapoints(tt.metric)
			if result != tt.expected {
				t.Errorf("countDatapoints() = %d, expected %d", result, tt.expected)
			}
		})
	}
}

func TestRecordPRWBytes(t *testing.T) {
	c := NewCollector(nil, StatsLevelFull)

	// Test PRW bytes received (uncompressed)
	c.RecordPRWBytesReceived(1000)
	c.RecordPRWBytesReceived(500)

	// Test PRW bytes received (compressed)
	c.RecordPRWBytesReceivedCompressed(200)
	c.RecordPRWBytesReceivedCompressed(100)

	// Test PRW bytes sent (uncompressed)
	c.RecordPRWBytesSent(800)
	c.RecordPRWBytesSent(400)

	// Test PRW bytes sent (compressed)
	c.RecordPRWBytesSentCompressed(150)
	c.RecordPRWBytesSentCompressed(75)

	// Verify via metrics output
	var buf bytes.Buffer
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	c.ServeHTTP(rr, req)
	buf.Write(rr.Body.Bytes())
	output := buf.String()

	// Check PRW bytes metrics exist
	if !strings.Contains(output, "metrics_governor_prw_bytes_total") {
		t.Error("expected metrics_governor_prw_bytes_total in output")
	}

	// Check specific label combinations
	if !strings.Contains(output, `direction="in",compression="uncompressed"`) {
		t.Error("expected PRW bytes received uncompressed metric")
	}
	if !strings.Contains(output, `direction="in",compression="compressed"`) {
		t.Error("expected PRW bytes received compressed metric")
	}
	if !strings.Contains(output, `direction="out",compression="uncompressed"`) {
		t.Error("expected PRW bytes sent uncompressed metric")
	}
	if !strings.Contains(output, `direction="out",compression="compressed"`) {
		t.Error("expected PRW bytes sent compressed metric")
	}
}

func TestRecordOTLPBytes(t *testing.T) {
	c := NewCollector(nil, StatsLevelFull)

	// Test OTLP bytes received
	c.RecordOTLPBytesReceived(2000)
	c.RecordOTLPBytesReceived(1000)

	// Test OTLP bytes received compressed
	c.RecordOTLPBytesReceivedCompressed(400)
	c.RecordOTLPBytesReceivedCompressed(200)

	// Test OTLP bytes sent
	c.RecordOTLPBytesSent(1600)
	c.RecordOTLPBytesSent(800)

	// Test OTLP bytes sent compressed
	c.RecordOTLPBytesSentCompressed(300)
	c.RecordOTLPBytesSentCompressed(150)

	// Verify via metrics output
	var buf bytes.Buffer
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	c.ServeHTTP(rr, req)
	buf.Write(rr.Body.Bytes())
	output := buf.String()

	// Check OTLP bytes metrics exist
	if !strings.Contains(output, "metrics_governor_otlp_bytes_total") {
		t.Error("expected metrics_governor_otlp_bytes_total in output")
	}

	// Check specific label combinations
	if !strings.Contains(output, `direction="in",compression="uncompressed"`) {
		t.Error("expected OTLP bytes received uncompressed metric")
	}
	if !strings.Contains(output, `direction="out",compression="uncompressed"`) {
		t.Error("expected OTLP bytes sent uncompressed metric")
	}
}

func TestSetBufferSize(t *testing.T) {
	c := NewCollector(nil, StatsLevelFull)

	// Set PRW buffer size
	c.SetPRWBufferSize(100)
	c.SetPRWBufferSize(150) // Update to new value

	// Set OTLP buffer size
	c.SetOTLPBufferSize(200)
	c.SetOTLPBufferSize(250) // Update to new value

	// Verify via metrics output
	var buf bytes.Buffer
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	c.ServeHTTP(rr, req)
	buf.Write(rr.Body.Bytes())
	output := buf.String()

	// Check buffer size metrics exist
	if !strings.Contains(output, "metrics_governor_buffer_size") {
		t.Error("expected metrics_governor_buffer_size in output")
	}

	// Check protocol labels
	if !strings.Contains(output, `protocol="prw"`) {
		t.Error("expected PRW buffer size metric")
	}
	if !strings.Contains(output, `protocol="otlp"`) {
		t.Error("expected OTLP buffer size metric")
	}

	// Check that values are the latest (gauge behavior)
	if !strings.Contains(output, "metrics_governor_buffer_size{protocol=\"prw\"} 150") {
		t.Error("expected PRW buffer size to be 150")
	}
	if !strings.Contains(output, "metrics_governor_buffer_size{protocol=\"otlp\"} 250") {
		t.Error("expected OTLP buffer size to be 250")
	}
}

func TestByteMetricsConcurrent(t *testing.T) {
	c := NewCollector(nil, StatsLevelFull)

	const goroutines = 10
	const iterations = 100

	done := make(chan bool)

	// Concurrent PRW byte recording
	for i := 0; i < goroutines; i++ {
		go func() {
			for j := 0; j < iterations; j++ {
				c.RecordPRWBytesReceived(100)
				c.RecordPRWBytesReceivedCompressed(50)
				c.RecordPRWBytesSent(80)
				c.RecordPRWBytesSentCompressed(40)
			}
			done <- true
		}()
	}

	// Concurrent OTLP byte recording
	for i := 0; i < goroutines; i++ {
		go func() {
			for j := 0; j < iterations; j++ {
				c.RecordOTLPBytesReceived(200)
				c.RecordOTLPBytesReceivedCompressed(100)
				c.RecordOTLPBytesSent(160)
				c.RecordOTLPBytesSentCompressed(80)
			}
			done <- true
		}()
	}

	// Concurrent buffer size updates
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			for j := 0; j < iterations; j++ {
				c.SetPRWBufferSize(id*100 + j)
				c.SetOTLPBufferSize(id*100 + j)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < goroutines*3; i++ {
		<-done
	}

	// Verify no panic and metrics are written
	var buf bytes.Buffer
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr := httptest.NewRecorder()
	c.ServeHTTP(rr, req)
	buf.Write(rr.Body.Bytes())
	output := buf.String()

	if !strings.Contains(output, "metrics_governor_prw_bytes_total") {
		t.Error("expected PRW bytes metrics after concurrent access")
	}
	if !strings.Contains(output, "metrics_governor_otlp_bytes_total") {
		t.Error("expected OTLP bytes metrics after concurrent access")
	}
	if !strings.Contains(output, "metrics_governor_buffer_size") {
		t.Error("expected buffer size metrics after concurrent access")
	}
}
