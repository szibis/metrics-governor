package stats

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
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
	c := NewCollector(trackLabels)

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
	c := NewCollector(nil)

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
	c := NewCollector(nil)

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
	c := NewCollector([]string{"service", "env"})

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
	c := NewCollector(nil)

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
	c := NewCollector(nil)

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
	c := NewCollector(nil)

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
	c := NewCollector(nil)

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
	c := NewCollector([]string{"service"})

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
	c := NewCollector(nil)

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
	c := NewCollector(nil)

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
	c := NewCollector([]string{"service", "env", "cluster"})

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

func TestMergeAttrs(t *testing.T) {
	a := map[string]string{"key1": "val1"}
	b := map[string]string{"key1": "override", "key2": "val2"}

	result := mergeAttrs(a, b)

	if result["key1"] != "override" {
		t.Errorf("expected key1='override', got '%s'", result["key1"])
	}
	if result["key2"] != "val2" {
		t.Errorf("expected key2='val2', got '%s'", result["key2"])
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

func TestGetGlobalStatsEmpty(t *testing.T) {
	c := NewCollector(nil)

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
	c := NewCollector(nil)

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
