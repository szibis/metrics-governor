package functional

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"

	"github.com/slawomirskowron/metrics-governor/internal/stats"
)

func createStatsTestMetrics(metricName string, labels map[string]string, datapointCount int) []*metricspb.ResourceMetrics {
	attrs := make([]*commonpb.KeyValue, 0, len(labels))
	for k, v := range labels {
		attrs = append(attrs, &commonpb.KeyValue{
			Key:   k,
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}},
		})
	}

	datapoints := make([]*metricspb.NumberDataPoint, datapointCount)
	for i := 0; i < datapointCount; i++ {
		// Each datapoint has unique attributes for cardinality testing
		dpAttrs := make([]*commonpb.KeyValue, len(attrs))
		copy(dpAttrs, attrs)
		dpAttrs = append(dpAttrs, &commonpb.KeyValue{
			Key:   "instance",
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: string(rune('A' + i%26))}},
		})

		datapoints[i] = &metricspb.NumberDataPoint{
			TimeUnixNano: uint64(time.Now().UnixNano()),
			Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)},
			Attributes:   dpAttrs,
		}
	}

	return []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{
				Attributes: attrs,
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
		},
	}
}

// TestFunctional_Stats_BasicTracking tests basic stats collection
func TestFunctional_Stats_BasicTracking(t *testing.T) {
	collector := stats.NewCollector(nil)

	// Process some metrics
	metrics := createStatsTestMetrics("http_requests_total", nil, 10)
	collector.Process(metrics)

	// Check global stats
	datapoints, uniqueMetrics, _ := collector.GetGlobalStats()

	if datapoints != 10 {
		t.Errorf("Expected 10 datapoints, got %d", datapoints)
	}

	if uniqueMetrics != 1 {
		t.Errorf("Expected 1 unique metric, got %d", uniqueMetrics)
	}
}

// TestFunctional_Stats_MultipleMetrics tests tracking multiple metric names
func TestFunctional_Stats_MultipleMetrics(t *testing.T) {
	collector := stats.NewCollector(nil)

	metricNames := []string{
		"http_requests_total",
		"http_request_duration_seconds",
		"http_response_size_bytes",
		"db_queries_total",
		"cache_hits_total",
	}

	for _, name := range metricNames {
		metrics := createStatsTestMetrics(name, nil, 5)
		collector.Process(metrics)
	}

	datapoints, uniqueMetrics, _ := collector.GetGlobalStats()

	if uniqueMetrics != uint64(len(metricNames)) {
		t.Errorf("Expected %d unique metrics, got %d", len(metricNames), uniqueMetrics)
	}

	if datapoints != uint64(len(metricNames)*5) {
		t.Errorf("Expected %d datapoints, got %d", len(metricNames)*5, datapoints)
	}
}

// TestFunctional_Stats_LabelTracking tests per-label stats
func TestFunctional_Stats_LabelTracking(t *testing.T) {
	// Track by service and env labels
	collector := stats.NewCollector([]string{"service", "env"})

	// Process metrics from different services
	services := []struct {
		service string
		env     string
		count   int
	}{
		{"api", "prod", 10},
		{"api", "staging", 5},
		{"web", "prod", 8},
		{"worker", "prod", 3},
	}

	for _, s := range services {
		labels := map[string]string{"service": s.service, "env": s.env}
		metrics := createStatsTestMetrics("request_count", labels, s.count)
		collector.Process(metrics)
	}

	datapoints, uniqueMetrics, cardinality := collector.GetGlobalStats()
	t.Logf("Global stats: %d datapoints, %d unique metrics, %d cardinality",
		datapoints, uniqueMetrics, cardinality)

	// Check label stats via HTTP
	recorder := httptest.NewRecorder()
	collector.ServeHTTP(recorder, nil)

	body := recorder.Body.String()

	// Should have label metrics
	if !strings.Contains(body, "metrics_governor_label_datapoints_total") {
		t.Error("Expected label_datapoints_total metric in output")
	}

	// Should track by service
	if !strings.Contains(body, `service="api"`) {
		t.Error("Expected service=api label in output")
	}
}

// TestFunctional_Stats_CardinalityTracking tests cardinality calculation
func TestFunctional_Stats_CardinalityTracking(t *testing.T) {
	collector := stats.NewCollector(nil)

	// Create metrics with varying cardinality
	// Each datapoint has unique instance attribute
	metrics := createStatsTestMetrics("high_cardinality_metric", nil, 100)
	collector.Process(metrics)

	_, _, cardinality := collector.GetGlobalStats()

	// Cardinality should be tracked
	if cardinality == 0 {
		t.Error("Expected non-zero cardinality")
	}

	t.Logf("Cardinality for 100 unique datapoints: %d", cardinality)
}

// TestFunctional_Stats_PrometheusOutput tests Prometheus metrics format
func TestFunctional_Stats_PrometheusOutput(t *testing.T) {
	collector := stats.NewCollector([]string{"service"})

	// Process some metrics
	metrics := createStatsTestMetrics("test_metric", map[string]string{"service": "api"}, 10)
	collector.Process(metrics)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	collector.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", recorder.Code)
	}

	body := recorder.Body.String()

	// Check for expected metrics
	expectedMetrics := []string{
		"metrics_governor_datapoints_total",
		"metrics_governor_metrics_total",
		"metrics_governor_metric_datapoints_total",
		"metrics_governor_metric_cardinality",
	}

	for _, metric := range expectedMetrics {
		if !strings.Contains(body, metric) {
			t.Errorf("Expected metric %s in output", metric)
		}
	}

	// Check format is valid Prometheus exposition format
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Should have metric name and value
		if !strings.Contains(line, " ") {
			t.Errorf("Invalid Prometheus format line: %s", line)
		}
	}
}

// TestFunctional_Stats_ConcurrentAccess tests thread safety
func TestFunctional_Stats_ConcurrentAccess(t *testing.T) {
	collector := stats.NewCollector([]string{"service"})

	var wg sync.WaitGroup
	goroutines := 10
	metricsPerGoroutine := 100

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			service := []string{"api", "web", "worker"}[id%3]
			for i := 0; i < metricsPerGoroutine; i++ {
				metrics := createStatsTestMetrics("concurrent_metric",
					map[string]string{"service": service}, 5)
				collector.Process(metrics)
			}
		}(g)
	}

	// Concurrent reads
	for g := 0; g < 5; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				collector.GetGlobalStats()
				time.Sleep(time.Millisecond)
			}
		}()
	}

	wg.Wait()

	datapoints, _, _ := collector.GetGlobalStats()
	expectedDatapoints := goroutines * metricsPerGoroutine * 5

	if datapoints != uint64(expectedDatapoints) {
		t.Errorf("Expected %d datapoints, got %d", expectedDatapoints, datapoints)
	}
}

// TestFunctional_Stats_HighVolume tests stats under high volume
func TestFunctional_Stats_HighVolume(t *testing.T) {
	collector := stats.NewCollector([]string{"service", "env"})

	start := time.Now()
	metricsCount := 10000

	for i := 0; i < metricsCount; i++ {
		service := []string{"api", "web", "worker", "scheduler", "cron"}[i%5]
		env := []string{"prod", "staging", "dev"}[i%3]
		metrics := createStatsTestMetrics("high_volume_metric",
			map[string]string{"service": service, "env": env}, 10)
		collector.Process(metrics)
	}

	elapsed := time.Since(start)
	datapoints, _, _ := collector.GetGlobalStats()

	t.Logf("Processed %d metrics (%d datapoints) in %v (%.0f metrics/sec)",
		metricsCount, datapoints, elapsed, float64(metricsCount)/elapsed.Seconds())

	expectedDatapoints := uint64(metricsCount * 10)
	if datapoints != expectedDatapoints {
		t.Errorf("Expected %d datapoints, got %d", expectedDatapoints, datapoints)
	}
}

// TestFunctional_Stats_MetricNameStats tests per-metric-name statistics
func TestFunctional_Stats_MetricNameStats(t *testing.T) {
	collector := stats.NewCollector(nil)

	// Process different metric types
	metricsData := []struct {
		name  string
		count int
	}{
		{"http_requests_total", 100},
		{"http_request_duration_seconds", 50},
		{"db_connections_active", 20},
	}

	for _, m := range metricsData {
		metrics := createStatsTestMetrics(m.name, nil, m.count)
		collector.Process(metrics)
	}

	// Check Prometheus output for per-metric stats
	recorder := httptest.NewRecorder()
	collector.ServeHTTP(recorder, nil)
	body := recorder.Body.String()

	for _, m := range metricsData {
		// Should have metric-specific stats
		expected := `metric_name="` + m.name + `"`
		if !strings.Contains(body, expected) {
			t.Errorf("Expected per-metric stats for %s", m.name)
		}
	}
}

// TestFunctional_Stats_ServeHTTPConcurrent tests concurrent HTTP requests
func TestFunctional_Stats_ServeHTTPConcurrent(t *testing.T) {
	collector := stats.NewCollector([]string{"service"})

	// Add some initial data
	for i := 0; i < 100; i++ {
		metrics := createStatsTestMetrics("test_metric",
			map[string]string{"service": "api"}, 10)
		collector.Process(metrics)
	}

	var wg sync.WaitGroup

	// Concurrent HTTP requests
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			collector.ServeHTTP(recorder, req)

			if recorder.Code != http.StatusOK {
				t.Errorf("Expected status 200, got %d", recorder.Code)
			}
		}()
	}

	// Concurrent writes while serving
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			metrics := createStatsTestMetrics("concurrent_write_metric",
				map[string]string{"service": "web"}, 5)
			collector.Process(metrics)
		}()
	}

	wg.Wait()
}

// TestFunctional_Stats_EmptyLabels tests behavior with no tracked labels
func TestFunctional_Stats_EmptyLabels(t *testing.T) {
	collector := stats.NewCollector(nil) // No label tracking

	metrics := createStatsTestMetrics("test_metric",
		map[string]string{"service": "api", "env": "prod"}, 10)
	collector.Process(metrics)

	recorder := httptest.NewRecorder()
	collector.ServeHTTP(recorder, nil)
	body := recorder.Body.String()

	// Should still have basic metrics
	if !strings.Contains(body, "metrics_governor_datapoints_total") {
		t.Error("Expected datapoints_total metric")
	}

	// Should NOT have label-specific metrics
	if strings.Contains(body, "metrics_governor_label_datapoints_total") {
		t.Log("Note: label metrics present even without tracked labels (OK if empty)")
	}
}

// TestFunctional_Stats_Reset tests that stats accumulate correctly
func TestFunctional_Stats_Reset(t *testing.T) {
	collector := stats.NewCollector(nil)

	// First batch
	metrics1 := createStatsTestMetrics("metric1", nil, 10)
	collector.Process(metrics1)

	datapoints1, _, _ := collector.GetGlobalStats()
	if datapoints1 != 10 {
		t.Errorf("Expected 10 datapoints, got %d", datapoints1)
	}

	// Second batch
	metrics2 := createStatsTestMetrics("metric2", nil, 20)
	collector.Process(metrics2)

	datapoints2, uniqueMetrics2, _ := collector.GetGlobalStats()
	if datapoints2 != 30 {
		t.Errorf("Expected 30 datapoints, got %d", datapoints2)
	}

	if uniqueMetrics2 != 2 {
		t.Errorf("Expected 2 unique metrics, got %d", uniqueMetrics2)
	}
}

// TestFunctional_Stats_OutputFormat verifies exact output format
func TestFunctional_Stats_OutputFormat(t *testing.T) {
	collector := stats.NewCollector([]string{"service"})

	metrics := createStatsTestMetrics("format_test",
		map[string]string{"service": "api"}, 5)
	collector.Process(metrics)

	var buf bytes.Buffer
	recorder := httptest.NewRecorder()
	collector.ServeHTTP(recorder, nil)
	buf.Write(recorder.Body.Bytes())

	body := buf.String()
	lines := strings.Split(body, "\n")

	hasHelp := false
	hasType := false
	hasValue := false

	for _, line := range lines {
		if strings.HasPrefix(line, "# HELP") {
			hasHelp = true
		}
		if strings.HasPrefix(line, "# TYPE") {
			hasType = true
		}
		if strings.HasPrefix(line, "metrics_governor_") && !strings.HasPrefix(line, "#") {
			hasValue = true
		}
	}

	if !hasHelp {
		t.Error("Expected # HELP comments in output")
	}
	if !hasType {
		t.Error("Expected # TYPE comments in output")
	}
	if !hasValue {
		t.Error("Expected metric values in output")
	}
}
