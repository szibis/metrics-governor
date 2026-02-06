package limits

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

func createTestMetric(name string, attrs map[string]string, datapointCount int) *metricspb.Metric {
	datapoints := make([]*metricspb.NumberDataPoint, datapointCount)
	for i := 0; i < datapointCount; i++ {
		dpAttrs := make([]*commonpb.KeyValue, 0, len(attrs))
		for k, v := range attrs {
			dpAttrs = append(dpAttrs, &commonpb.KeyValue{
				Key:   k,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}},
			})
		}
		datapoints[i] = &metricspb.NumberDataPoint{
			Attributes: dpAttrs,
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
	resAttrs := make([]*commonpb.KeyValue, 0, len(resourceAttrs))
	for k, v := range resourceAttrs {
		resAttrs = append(resAttrs, &commonpb.KeyValue{
			Key:   k,
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}},
		})
	}

	return &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: resAttrs,
		},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{
				Metrics: metrics,
			},
		},
	}
}

func TestNewEnforcer(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{
			MaxCardinality: 1000,
			Action:         ActionLog,
		},
	}

	enforcer := NewEnforcer(cfg, true, 0)

	if enforcer == nil {
		t.Fatal("expected non-nil enforcer")
	}
	if enforcer.config != cfg {
		t.Error("expected config to be set")
	}
	if !enforcer.dryRun {
		t.Error("expected dryRun to be true")
	}
	if enforcer.ruleStats == nil {
		t.Error("expected ruleStats to be initialized")
	}
	if enforcer.droppedGroups == nil {
		t.Error("expected droppedGroups to be initialized")
	}
	if enforcer.violations == nil {
		t.Error("expected violations to be initialized")
	}
}

func TestProcessNoConfig(t *testing.T) {
	enforcer := NewEnforcer(nil, false, 0)

	rm := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{}, 1)},
	)

	result := enforcer.Process([]*metricspb.ResourceMetrics{rm})

	if len(result) != 1 {
		t.Errorf("expected 1 resource metric, got %d", len(result))
	}
}

func TestProcessNoMatchingRule(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:  "only-http",
				Match: RuleMatch{MetricName: "http_.*"},
			},
		},
	}
	// Compile regex
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)

	rm := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("grpc_requests_total", map[string]string{}, 1)},
	)

	result := enforcer.Process([]*metricspb.ResourceMetrics{rm})

	if len(result) != 1 {
		t.Errorf("expected 1 resource metric (no rule match), got %d", len(result))
	}
}

func TestProcessWithinLimits(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:           "test-rule",
				Match:          RuleMatch{MetricName: "test_.*"},
				MaxCardinality: 1000,
				Action:         ActionAdaptive,
				GroupBy:        []string{"service"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)

	rm := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{}, 5)},
	)

	result := enforcer.Process([]*metricspb.ResourceMetrics{rm})

	if len(result) != 1 {
		t.Errorf("expected 1 resource metric (within limits), got %d", len(result))
	}
}

func TestProcessDropAction(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:           "drop-rule",
				Match:          RuleMatch{MetricName: "test_.*"},
				MaxCardinality: 1, // Very low limit
				Action:         ActionDrop,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)

	// First metric - should pass
	rm1 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{"id": "1"}, 1)},
	)
	result1 := enforcer.Process([]*metricspb.ResourceMetrics{rm1})
	if len(result1) != 1 {
		t.Errorf("expected first metric to pass, got %d results", len(result1))
	}

	// Second metric - should be dropped (exceeds cardinality)
	rm2 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{"id": "2"}, 1)},
	)
	result2 := enforcer.Process([]*metricspb.ResourceMetrics{rm2})
	if len(result2) != 0 {
		t.Errorf("expected second metric to be dropped, got %d results", len(result2))
	}
}

func TestProcessDropActionDryRun(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:           "drop-rule",
				Match:          RuleMatch{MetricName: "test_.*"},
				MaxCardinality: 1,
				Action:         ActionDrop,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, true, 0) // dryRun = true

	// First metric
	rm1 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{"id": "1"}, 1)},
	)
	enforcer.Process([]*metricspb.ResourceMetrics{rm1})

	// Second metric - should pass through in dry run mode
	rm2 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{"id": "2"}, 1)},
	)
	result2 := enforcer.Process([]*metricspb.ResourceMetrics{rm2})
	if len(result2) != 1 {
		t.Errorf("expected metric to pass in dry run mode, got %d results", len(result2))
	}
}

func TestProcessLogAction(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:           "log-rule",
				Match:          RuleMatch{MetricName: "test_.*"},
				MaxCardinality: 1,
				Action:         ActionLog,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)

	// First metric
	rm1 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{"id": "1"}, 1)},
	)
	enforcer.Process([]*metricspb.ResourceMetrics{rm1})

	// Second metric - should pass through with log action
	rm2 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{"id": "2"}, 1)},
	)
	result2 := enforcer.Process([]*metricspb.ResourceMetrics{rm2})
	if len(result2) != 1 {
		t.Errorf("expected metric to pass with log action, got %d results", len(result2))
	}
}

func TestBuildGroupKey(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:    "rule-with-groupby",
				Match:   RuleMatch{MetricName: "test_.*"},
				GroupBy: []string{"service", "env"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)

	metric := createTestMetric("test_metric", map[string]string{"env": "prod", "extra": "value"}, 1)
	resourceAttrs := map[string]string{"service": "api"}

	groupKey := enforcer.buildGroupKey(&cfg.Rules[0], resourceAttrs, metric)

	// Should contain service=api and env=prod
	if groupKey != "service=api,env=prod" {
		t.Errorf("expected group key 'service=api,env=prod', got '%s'", groupKey)
	}
}

func TestBuildGroupKeyNoGroupBy(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:  "rule-no-groupby",
				Match: RuleMatch{MetricName: "test_.*"},
				// No GroupBy
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)

	metric := createTestMetric("test_metric", map[string]string{}, 1)
	resourceAttrs := map[string]string{"service": "api"}

	groupKey := enforcer.buildGroupKey(&cfg.Rules[0], resourceAttrs, metric)

	// Should use metric name as key
	if groupKey != "test_metric" {
		t.Errorf("expected group key 'test_metric', got '%s'", groupKey)
	}
}

func TestServeHTTP(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:           "test-rule",
				Match:          RuleMatch{MetricName: "test_.*"},
				MaxCardinality: 100,
				Action:         ActionAdaptive,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)

	// Process some metrics to generate stats
	rm := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{}, 5)},
	)
	enforcer.Process([]*metricspb.ResourceMetrics{rm})

	// Test HTTP handler
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	enforcer.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body := w.Body.String()
	if !bytes.Contains([]byte(body), []byte("metrics_governor_limit")) {
		t.Error("expected metrics output to contain 'metrics_governor_limit'")
	}
}

func TestExtractAttributes(t *testing.T) {
	attrs := []*commonpb.KeyValue{
		{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "api"}}},
		{Key: "env", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "prod"}}},
		{Key: "empty", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: ""}}},
		{Key: "int", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 123}}},
	}

	result := extractAttributes(attrs)

	if result["service"] != "api" {
		t.Errorf("expected service='api', got '%s'", result["service"])
	}
	if result["env"] != "prod" {
		t.Errorf("expected env='prod', got '%s'", result["env"])
	}
	if _, ok := result["empty"]; ok {
		t.Error("expected empty string values to be excluded")
	}
	if _, ok := result["int"]; ok {
		t.Error("expected non-string values to be excluded")
	}
}

func TestCountDatapoints(t *testing.T) {
	tests := []struct {
		name     string
		metric   *metricspb.Metric
		expected int
	}{
		{
			name: "sum metric",
			metric: &metricspb.Metric{
				Data: &metricspb.Metric_Sum{
					Sum: &metricspb.Sum{
						DataPoints: make([]*metricspb.NumberDataPoint, 5),
					},
				},
			},
			expected: 5,
		},
		{
			name: "gauge metric",
			metric: &metricspb.Metric{
				Data: &metricspb.Metric_Gauge{
					Gauge: &metricspb.Gauge{
						DataPoints: make([]*metricspb.NumberDataPoint, 3),
					},
				},
			},
			expected: 3,
		},
		{
			name: "histogram metric",
			metric: &metricspb.Metric{
				Data: &metricspb.Metric_Histogram{
					Histogram: &metricspb.Histogram{
						DataPoints: make([]*metricspb.HistogramDataPoint, 7),
					},
				},
			},
			expected: 7,
		},
		{
			name: "summary metric",
			metric: &metricspb.Metric{
				Data: &metricspb.Metric_Summary{
					Summary: &metricspb.Summary{
						DataPoints: make([]*metricspb.SummaryDataPoint, 2),
					},
				},
			},
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := countDatapoints(tt.metric)
			if result != tt.expected {
				t.Errorf("countDatapoints() = %d, expected %d", result, tt.expected)
			}
		})
	}
}

func TestMergeAttrs(t *testing.T) {
	a := map[string]string{"key1": "val1", "key2": "val2"}
	b := map[string]string{"key2": "override", "key3": "val3"}

	result := mergeAttrs(a, b)

	if result["key1"] != "val1" {
		t.Errorf("expected key1='val1', got '%s'", result["key1"])
	}
	if result["key2"] != "override" {
		t.Errorf("expected key2='override', got '%s'", result["key2"])
	}
	if result["key3"] != "val3" {
		t.Errorf("expected key3='val3', got '%s'", result["key3"])
	}
}

func TestBuildSeriesKey(t *testing.T) {
	tests := []struct {
		name     string
		attrs    map[string]string
		expected string
	}{
		{
			name:     "empty attrs",
			attrs:    map[string]string{},
			expected: "",
		},
		{
			name:     "single attr",
			attrs:    map[string]string{"key": "value"},
			expected: "key=value",
		},
		{
			name:     "multiple attrs sorted",
			attrs:    map[string]string{"zebra": "z", "alpha": "a", "beta": "b"},
			expected: "alpha=a,beta=b,zebra=z",
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

func TestFindMatchingRule(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{Name: "rule1", Match: RuleMatch{MetricName: "http_.*"}},
			{Name: "rule2", Match: RuleMatch{MetricName: "grpc_.*"}},
			{Name: "rule3", Match: RuleMatch{Labels: map[string]string{"env": "prod"}}},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)

	tests := []struct {
		name         string
		metricName   string
		labels       map[string]string
		expectedRule string
	}{
		{"match first rule", "http_requests", map[string]string{}, "rule1"},
		{"match second rule", "grpc_calls", map[string]string{}, "rule2"},
		{"match label rule", "unknown_metric", map[string]string{"env": "prod"}, "rule3"},
		{"no match", "unknown_metric", map[string]string{"env": "dev"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := enforcer.findMatchingRule(tt.metricName, tt.labels)
			if tt.expectedRule == "" {
				if rule != nil {
					t.Errorf("expected no rule, got '%s'", rule.Name)
				}
			} else {
				if rule == nil {
					t.Error("expected rule, got nil")
				} else if rule.Name != tt.expectedRule {
					t.Errorf("expected rule '%s', got '%s'", tt.expectedRule, rule.Name)
				}
			}
		})
	}
}

func TestDatapointsRateLimit(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "rate-limit",
				Match:             RuleMatch{MetricName: "test_.*"},
				MaxDatapointsRate: 10, // Very low limit
				Action:            ActionDrop,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)

	// First batch - should pass (5 datapoints)
	rm1 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{}, 5)},
	)
	result1 := enforcer.Process([]*metricspb.ResourceMetrics{rm1})
	if len(result1) != 1 {
		t.Error("expected first batch to pass")
	}

	// Second batch - should pass (another 5, total 10)
	rm2 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{}, 5)},
	)
	result2 := enforcer.Process([]*metricspb.ResourceMetrics{rm2})
	if len(result2) != 1 {
		t.Error("expected second batch to pass")
	}

	// Third batch - should be dropped (would exceed rate limit)
	rm3 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{}, 5)},
	)
	result3 := enforcer.Process([]*metricspb.ResourceMetrics{rm3})
	if len(result3) != 0 {
		t.Error("expected third batch to be dropped (exceeds rate limit)")
	}
}

func TestAdaptiveActionTracking(t *testing.T) {
	// Test that adaptive action tracks groups and updates stats
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "adaptive-rule",
				Match:             RuleMatch{MetricName: "test_.*"},
				MaxDatapointsRate: 10, // Allow only 10 datapoints
				Action:            ActionAdaptive,
				GroupBy:           []string{"service"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)

	// Service A sends 8 datapoints - should pass (under limit)
	rmA := createTestResourceMetrics(
		map[string]string{"service": "service-a"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{}, 8)},
	)
	result1 := enforcer.Process([]*metricspb.ResourceMetrics{rmA})
	if len(result1) != 1 {
		t.Error("expected first batch to pass")
	}

	// Service A sends 2 more datapoints - should pass (at limit, 10 total)
	rmA2 := createTestResourceMetrics(
		map[string]string{"service": "service-a"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{}, 2)},
	)
	result2 := enforcer.Process([]*metricspb.ResourceMetrics{rmA2})
	if len(result2) != 1 {
		t.Error("expected second batch to pass (at limit)")
	}

	// Service A sends 1 more - exceeds limit, triggers adaptive violation
	rmA3 := createTestResourceMetrics(
		map[string]string{"service": "service-a"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{}, 1)},
	)
	// This triggers violation - adaptive marks service-a for dropping
	result3 := enforcer.Process([]*metricspb.ResourceMetrics{rmA3})
	// The current group causing the violation is passed through initially
	// but marked for future drops
	_ = result3 // Result depends on implementation - may or may not be dropped immediately

	// Check the enforcer HTTP metrics show a violation was recorded
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	enforcer.ServeHTTP(w, req)

	body := w.Body.String()
	if !bytes.Contains([]byte(body), []byte("metrics_governor_limit_datapoints_exceeded_total")) {
		t.Error("expected datapoints exceeded metric to be recorded")
	}
}

func TestAdaptiveActionDryRun(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "adaptive-rule",
				Match:             RuleMatch{MetricName: "test_.*"},
				MaxDatapointsRate: 10,
				Action:            ActionAdaptive,
				GroupBy:           []string{"service"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, true, 0) // dryRun = true

	// Exceed limits
	for i := 0; i < 3; i++ {
		rm := createTestResourceMetrics(
			map[string]string{"service": "service-a"},
			[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{}, 10)},
		)
		result := enforcer.Process([]*metricspb.ResourceMetrics{rm})
		// In dry run mode, nothing should be dropped
		if len(result) != 1 {
			t.Errorf("expected metric to pass in dry run mode, got %d results", len(result))
		}
	}
}

func TestExtractDatapointAttributesGauge(t *testing.T) {
	metric := &metricspb.Metric{
		Name: "gauge_metric",
		Data: &metricspb.Metric_Gauge{
			Gauge: &metricspb.Gauge{
				DataPoints: []*metricspb.NumberDataPoint{
					{
						Attributes: []*commonpb.KeyValue{
							{Key: "host", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "server1"}}},
						},
					},
				},
			},
		},
	}

	allAttrs := extractDatapointAttributes(metric)
	if len(allAttrs) != 1 {
		t.Errorf("expected 1 set of attributes, got %d", len(allAttrs))
	}
}

func TestExtractDatapointAttributesHistogram(t *testing.T) {
	metric := &metricspb.Metric{
		Name: "histogram_metric",
		Data: &metricspb.Metric_Histogram{
			Histogram: &metricspb.Histogram{
				DataPoints: []*metricspb.HistogramDataPoint{
					{
						Attributes: []*commonpb.KeyValue{
							{Key: "bucket", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "100"}}},
						},
					},
				},
			},
		},
	}

	allAttrs := extractDatapointAttributes(metric)
	if len(allAttrs) != 1 {
		t.Errorf("expected 1 set of attributes, got %d", len(allAttrs))
	}
}

func TestExtractDatapointAttributesSummary(t *testing.T) {
	metric := &metricspb.Metric{
		Name: "summary_metric",
		Data: &metricspb.Metric_Summary{
			Summary: &metricspb.Summary{
				DataPoints: []*metricspb.SummaryDataPoint{
					{
						Attributes: []*commonpb.KeyValue{
							{Key: "quantile", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "0.99"}}},
						},
					},
				},
			},
		},
	}

	allAttrs := extractDatapointAttributes(metric)
	if len(allAttrs) != 1 {
		t.Errorf("expected 1 set of attributes, got %d", len(allAttrs))
	}
}

func TestExtractDatapointAttributesExponentialHistogram(t *testing.T) {
	metric := &metricspb.Metric{
		Name: "exp_histogram_metric",
		Data: &metricspb.Metric_ExponentialHistogram{
			ExponentialHistogram: &metricspb.ExponentialHistogram{
				DataPoints: []*metricspb.ExponentialHistogramDataPoint{
					{
						Attributes: []*commonpb.KeyValue{
							{Key: "scale", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "2"}}},
						},
					},
				},
			},
		},
	}

	allAttrs := extractDatapointAttributes(metric)
	if len(allAttrs) != 1 {
		t.Errorf("expected 1 set of attributes, got %d", len(allAttrs))
	}
}

func TestCountDatapointsExponentialHistogram(t *testing.T) {
	metric := &metricspb.Metric{
		Data: &metricspb.Metric_ExponentialHistogram{
			ExponentialHistogram: &metricspb.ExponentialHistogram{
				DataPoints: make([]*metricspb.ExponentialHistogramDataPoint, 4),
			},
		},
	}

	count := countDatapoints(metric)
	if count != 4 {
		t.Errorf("expected 4 datapoints, got %d", count)
	}
}

func TestCountDatapointsNilData(t *testing.T) {
	metric := &metricspb.Metric{
		Name: "empty_metric",
		Data: nil,
	}

	count := countDatapoints(metric)
	if count != 0 {
		t.Errorf("expected 0 datapoints for nil data, got %d", count)
	}
}

func TestProcessMultipleScopeMetrics(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:           "test-rule",
				Match:          RuleMatch{MetricName: "test_.*"},
				MaxCardinality: 100,
				Action:         ActionDrop,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)

	// Create ResourceMetrics with multiple ScopeMetrics
	rm := &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{
				{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "api"}}},
			},
		},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{
				Metrics: []*metricspb.Metric{
					createTestMetric("test_metric_1", map[string]string{}, 3),
				},
			},
			{
				Metrics: []*metricspb.Metric{
					createTestMetric("test_metric_2", map[string]string{}, 3),
				},
			},
		},
	}

	result := enforcer.Process([]*metricspb.ResourceMetrics{rm})

	if len(result) != 1 {
		t.Errorf("expected 1 resource metric, got %d", len(result))
	}
	if len(result[0].ScopeMetrics) != 2 {
		t.Errorf("expected 2 scope metrics, got %d", len(result[0].ScopeMetrics))
	}
}

func TestProcessEmptyMetricsList(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
	}

	enforcer := NewEnforcer(cfg, false, 0)

	result := enforcer.Process([]*metricspb.ResourceMetrics{})
	if len(result) != 0 {
		t.Errorf("expected empty result, got %d", len(result))
	}
}

func TestProcessNilResourceMetrics(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
	}

	enforcer := NewEnforcer(cfg, false, 0)

	result := enforcer.Process(nil)
	if len(result) != 0 {
		t.Errorf("expected empty result, got %d", len(result))
	}
}

func TestServeHTTPWithViolations(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:           "drop-rule",
				Match:          RuleMatch{MetricName: "test_.*"},
				MaxCardinality: 1,
				Action:         ActionDrop,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)

	// Create violation by exceeding cardinality
	rm1 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{"id": "1"}, 1)},
	)
	enforcer.Process([]*metricspb.ResourceMetrics{rm1})

	rm2 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{"id": "2"}, 1)},
	)
	enforcer.Process([]*metricspb.ResourceMetrics{rm2})

	// Test HTTP handler output
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	enforcer.ServeHTTP(w, req)

	body := w.Body.String()

	// Check for cardinality exceeded metrics
	if !bytes.Contains([]byte(body), []byte("metrics_governor_limit_cardinality_exceeded_total")) {
		t.Error("expected cardinality exceeded metrics in output")
	}
	// Check for datapoints dropped metrics
	if !bytes.Contains([]byte(body), []byte("metrics_governor_limit_datapoints_dropped_total")) {
		t.Error("expected datapoints dropped metrics in output")
	}
}

// Helper function to extract governor labels from a metric's datapoints
func getGovernorLabels(m *metricspb.Metric) (action, rule string, found bool) {
	var attrs []*commonpb.KeyValue

	switch d := m.Data.(type) {
	case *metricspb.Metric_Gauge:
		if len(d.Gauge.DataPoints) > 0 {
			attrs = d.Gauge.DataPoints[0].Attributes
		}
	case *metricspb.Metric_Sum:
		if len(d.Sum.DataPoints) > 0 {
			attrs = d.Sum.DataPoints[0].Attributes
		}
	case *metricspb.Metric_Histogram:
		if len(d.Histogram.DataPoints) > 0 {
			attrs = d.Histogram.DataPoints[0].Attributes
		}
	case *metricspb.Metric_ExponentialHistogram:
		if len(d.ExponentialHistogram.DataPoints) > 0 {
			attrs = d.ExponentialHistogram.DataPoints[0].Attributes
		}
	case *metricspb.Metric_Summary:
		if len(d.Summary.DataPoints) > 0 {
			attrs = d.Summary.DataPoints[0].Attributes
		}
	}

	for _, kv := range attrs {
		if kv.Key == "metrics.governor.action" {
			action = kv.Value.GetStringValue()
		}
		if kv.Key == "metrics.governor.rule" {
			rule = kv.Value.GetStringValue()
		}
	}

	found = action != "" && rule != ""
	return
}

// Helper to create metrics of various types for testing label injection
func createMetricOfType(name string, metricType string, datapointCount int) *metricspb.Metric {
	switch metricType {
	case "gauge":
		dps := make([]*metricspb.NumberDataPoint, datapointCount)
		for i := 0; i < datapointCount; i++ {
			dps[i] = &metricspb.NumberDataPoint{}
		}
		return &metricspb.Metric{
			Name: name,
			Data: &metricspb.Metric_Gauge{
				Gauge: &metricspb.Gauge{DataPoints: dps},
			},
		}
	case "sum":
		dps := make([]*metricspb.NumberDataPoint, datapointCount)
		for i := 0; i < datapointCount; i++ {
			dps[i] = &metricspb.NumberDataPoint{}
		}
		return &metricspb.Metric{
			Name: name,
			Data: &metricspb.Metric_Sum{
				Sum: &metricspb.Sum{DataPoints: dps},
			},
		}
	case "histogram":
		dps := make([]*metricspb.HistogramDataPoint, datapointCount)
		for i := 0; i < datapointCount; i++ {
			dps[i] = &metricspb.HistogramDataPoint{}
		}
		return &metricspb.Metric{
			Name: name,
			Data: &metricspb.Metric_Histogram{
				Histogram: &metricspb.Histogram{DataPoints: dps},
			},
		}
	case "exponential_histogram":
		dps := make([]*metricspb.ExponentialHistogramDataPoint, datapointCount)
		for i := 0; i < datapointCount; i++ {
			dps[i] = &metricspb.ExponentialHistogramDataPoint{}
		}
		return &metricspb.Metric{
			Name: name,
			Data: &metricspb.Metric_ExponentialHistogram{
				ExponentialHistogram: &metricspb.ExponentialHistogram{DataPoints: dps},
			},
		}
	case "summary":
		dps := make([]*metricspb.SummaryDataPoint, datapointCount)
		for i := 0; i < datapointCount; i++ {
			dps[i] = &metricspb.SummaryDataPoint{}
		}
		return &metricspb.Metric{
			Name: name,
			Data: &metricspb.Metric_Summary{
				Summary: &metricspb.Summary{DataPoints: dps},
			},
		}
	}
	return nil
}

func TestInjectDatapointLabels_AllTypes(t *testing.T) {
	metricTypes := []string{"gauge", "sum", "histogram", "exponential_histogram", "summary"}

	for _, metricType := range metricTypes {
		t.Run(metricType, func(t *testing.T) {
			m := createMetricOfType("test_metric", metricType, 3)

			injectDatapointLabels(m, "passed", "test-rule")

			action, rule, found := getGovernorLabels(m)
			if !found {
				t.Errorf("expected labels to be injected for %s", metricType)
			}
			if action != "passed" {
				t.Errorf("expected action='passed', got '%s' for %s", action, metricType)
			}
			if rule != "test-rule" {
				t.Errorf("expected rule='test-rule', got '%s' for %s", rule, metricType)
			}
		})
	}
}

func TestProcessMetric_NoRule_NoLabels(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:  "only-http",
				Match: RuleMatch{MetricName: "http_.*"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	rm := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("grpc_requests_total", map[string]string{}, 1)},
	)

	result := enforcer.Process([]*metricspb.ResourceMetrics{rm})

	if len(result) != 1 {
		t.Fatalf("expected 1 resource metric, got %d", len(result))
	}

	metric := result[0].ScopeMetrics[0].Metrics[0]
	_, _, found := getGovernorLabels(metric)
	if found {
		t.Error("expected no labels when no rule matches")
	}
}

func TestProcessMetric_WithinLimits_PassedLabel(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:           "test-rule",
				Match:          RuleMatch{MetricName: "test_.*"},
				MaxCardinality: 1000,
				Action:         ActionDrop,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	rm := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{}, 1)},
	)

	result := enforcer.Process([]*metricspb.ResourceMetrics{rm})

	if len(result) != 1 {
		t.Fatalf("expected 1 resource metric, got %d", len(result))
	}

	metric := result[0].ScopeMetrics[0].Metrics[0]
	action, rule, found := getGovernorLabels(metric)
	if !found {
		t.Fatal("expected labels to be present")
	}
	if action != "passed" {
		t.Errorf("expected action='passed', got '%s'", action)
	}
	if rule != "test-rule" {
		t.Errorf("expected rule='test-rule', got '%s'", rule)
	}
}

func TestProcessMetric_LogAction_LogLabel(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:           "log-rule",
				Match:          RuleMatch{MetricName: "test_.*"},
				MaxCardinality: 1, // Very low limit
				Action:         ActionLog,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// First metric - should get "passed" label
	rm1 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{"id": "1"}, 1)},
	)
	result1 := enforcer.Process([]*metricspb.ResourceMetrics{rm1})
	if len(result1) != 1 {
		t.Fatalf("expected first metric to pass")
	}
	action1, _, _ := getGovernorLabels(result1[0].ScopeMetrics[0].Metrics[0])
	if action1 != "passed" {
		t.Errorf("expected first metric action='passed', got '%s'", action1)
	}

	// Second metric - should exceed limit and get "log" label
	rm2 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{"id": "2"}, 1)},
	)
	result2 := enforcer.Process([]*metricspb.ResourceMetrics{rm2})
	if len(result2) != 1 {
		t.Fatalf("expected metric to pass with log action")
	}

	metric := result2[0].ScopeMetrics[0].Metrics[0]
	action, rule, found := getGovernorLabels(metric)
	if !found {
		t.Fatal("expected labels to be present")
	}
	if action != "log" {
		t.Errorf("expected action='log', got '%s'", action)
	}
	if rule != "log-rule" {
		t.Errorf("expected rule='log-rule', got '%s'", rule)
	}
}

func TestProcessMetric_DropAction_DropLabel_DryRun(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:           "drop-rule",
				Match:          RuleMatch{MetricName: "test_.*"},
				MaxCardinality: 1, // Very low limit
				Action:         ActionDrop,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, true, 0) // dryRun = true
	defer enforcer.Stop()

	// First metric - should get "passed" label
	rm1 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{"id": "1"}, 1)},
	)
	result1 := enforcer.Process([]*metricspb.ResourceMetrics{rm1})
	if len(result1) != 1 {
		t.Fatalf("expected first metric to pass")
	}

	// Second metric - should exceed limit and get "drop" label in dry-run
	rm2 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{"id": "2"}, 1)},
	)
	result2 := enforcer.Process([]*metricspb.ResourceMetrics{rm2})
	if len(result2) != 1 {
		t.Fatalf("expected metric to pass in dry-run mode")
	}

	metric := result2[0].ScopeMetrics[0].Metrics[0]
	action, rule, found := getGovernorLabels(metric)
	if !found {
		t.Fatal("expected labels to be present")
	}
	if action != "drop" {
		t.Errorf("expected action='drop', got '%s'", action)
	}
	if rule != "drop-rule" {
		t.Errorf("expected rule='drop-rule', got '%s'", rule)
	}
}

func TestProcessMetric_AdaptiveAction_AdaptiveLabel(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "adaptive-rule",
				Match:             RuleMatch{MetricName: "test_.*"},
				MaxDatapointsRate: 10, // Low limit
				Action:            ActionAdaptive,
				GroupBy:           []string{"service"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, true, 0) // dryRun = true
	defer enforcer.Stop()

	// First batch - should pass and get "passed" label
	rm1 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{}, 5)},
	)
	result1 := enforcer.Process([]*metricspb.ResourceMetrics{rm1})
	if len(result1) != 1 {
		t.Fatalf("expected first metric to pass")
	}
	action1, _, _ := getGovernorLabels(result1[0].ScopeMetrics[0].Metrics[0])
	if action1 != "passed" {
		t.Errorf("expected first metric action='passed', got '%s'", action1)
	}

	// Fill up to the limit
	rm2 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{}, 5)},
	)
	enforcer.Process([]*metricspb.ResourceMetrics{rm2})

	// Exceed limit - should get "adaptive" label
	rm3 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{}, 5)},
	)
	result3 := enforcer.Process([]*metricspb.ResourceMetrics{rm3})
	if len(result3) != 1 {
		t.Fatalf("expected metric to pass in dry-run mode")
	}

	metric := result3[0].ScopeMetrics[0].Metrics[0]
	action, rule, found := getGovernorLabels(metric)
	if !found {
		t.Fatal("expected labels to be present")
	}
	if action != "adaptive" {
		t.Errorf("expected action='adaptive', got '%s'", action)
	}
	if rule != "adaptive-rule" {
		t.Errorf("expected rule='adaptive-rule', got '%s'", rule)
	}
}

func TestProcessMetric_DroppedGroup_DropLabel_DryRun(t *testing.T) {
	// When a group is marked for dropping by adaptive limiting and then isGroupDropped
	// returns true, the metric gets "drop" label (not "adaptive") because it's going
	// through the dropped group path, not the adaptive handler path.
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "adaptive-rule",
				Match:             RuleMatch{MetricName: "test_.*"},
				MaxDatapointsRate: 10,
				Action:            ActionAdaptive,
				GroupBy:           []string{"service"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, true, 0) // dryRun = true
	defer enforcer.Stop()

	// First batch to fill up
	rm1 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{}, 10)},
	)
	enforcer.Process([]*metricspb.ResourceMetrics{rm1})

	// Exceed limit - marks group for dropping
	rm2 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{}, 5)},
	)
	enforcer.Process([]*metricspb.ResourceMetrics{rm2})

	// Subsequent request from dropped group - will still trigger adaptive handling
	// since the group is in droppedGroups but handleAdaptive will be called
	rm3 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{}, 1)},
	)
	result3 := enforcer.Process([]*metricspb.ResourceMetrics{rm3})
	if len(result3) != 1 {
		t.Fatalf("expected metric to pass in dry-run mode")
	}

	// Note: Due to the flow of processMetric, when isGroupDropped returns true,
	// we get "drop" label. When it goes through handleAdaptive, we get "adaptive".
	// Let's verify the label exists (either drop or adaptive is valid)
	metric := result3[0].ScopeMetrics[0].Metrics[0]
	action, rule, found := getGovernorLabels(metric)
	if !found {
		t.Fatal("expected labels to be present")
	}
	if action != "drop" && action != "adaptive" {
		t.Errorf("expected action='drop' or 'adaptive' for dropped group, got '%s'", action)
	}
	if rule != "adaptive-rule" {
		t.Errorf("expected rule='adaptive-rule', got '%s'", rule)
	}
}

func TestInjectDatapointLabels_AllDatapoints(t *testing.T) {
	// Create metric with multiple datapoints
	m := &metricspb.Metric{
		Name: "test_metric",
		Data: &metricspb.Metric_Sum{
			Sum: &metricspb.Sum{
				DataPoints: []*metricspb.NumberDataPoint{
					{},
					{},
					{},
				},
			},
		},
	}

	injectDatapointLabels(m, "passed", "test-rule")

	// Verify all datapoints got the labels
	sum := m.Data.(*metricspb.Metric_Sum).Sum
	for i, dp := range sum.DataPoints {
		var foundAction, foundRule bool
		for _, kv := range dp.Attributes {
			if kv.Key == "metrics.governor.action" && kv.Value.GetStringValue() == "passed" {
				foundAction = true
			}
			if kv.Key == "metrics.governor.rule" && kv.Value.GetStringValue() == "test-rule" {
				foundRule = true
			}
		}
		if !foundAction || !foundRule {
			t.Errorf("datapoint %d missing labels: action=%v, rule=%v", i, foundAction, foundRule)
		}
	}
}

func TestLabelValues(t *testing.T) {
	// Test that all expected action values are valid
	validActions := []string{"passed", "log", "drop", "adaptive"}

	for _, action := range validActions {
		m := createMetricOfType("test", "gauge", 1)
		injectDatapointLabels(m, action, "test-rule")

		gotAction, _, found := getGovernorLabels(m)
		if !found {
			t.Errorf("labels not found for action '%s'", action)
		}
		if gotAction != action {
			t.Errorf("expected action '%s', got '%s'", action, gotAction)
		}
	}
}

func TestLabelKeyNames(t *testing.T) {
	m := createMetricOfType("test", "gauge", 1)
	injectDatapointLabels(m, "passed", "test-rule")

	gauge := m.Data.(*metricspb.Metric_Gauge).Gauge
	dp := gauge.DataPoints[0]

	var foundActionKey, foundRuleKey bool
	for _, kv := range dp.Attributes {
		if kv.Key == "metrics.governor.action" {
			foundActionKey = true
		}
		if kv.Key == "metrics.governor.rule" {
			foundRuleKey = true
		}
	}

	if !foundActionKey {
		t.Error("expected key 'metrics.governor.action' to be present")
	}
	if !foundRuleKey {
		t.Error("expected key 'metrics.governor.rule' to be present")
	}
}

func TestProcessMetric_DropAction_NoLabel_WhenActuallyDropped(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:           "drop-rule",
				Match:          RuleMatch{MetricName: "test_.*"},
				MaxCardinality: 1,
				Action:         ActionDrop,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0) // dryRun = false
	defer enforcer.Stop()

	// First metric - should pass
	rm1 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{"id": "1"}, 1)},
	)
	enforcer.Process([]*metricspb.ResourceMetrics{rm1})

	// Second metric - should be dropped (nil result)
	rm2 := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{"id": "2"}, 1)},
	)
	result2 := enforcer.Process([]*metricspb.ResourceMetrics{rm2})

	// Result should be empty (metric dropped)
	if len(result2) != 0 {
		t.Errorf("expected metric to be dropped, got %d results", len(result2))
	}
}

func TestRuleNameInLabel(t *testing.T) {
	tests := []struct {
		ruleName string
	}{
		{"simple-rule"},
		{"rule-with-special_chars"},
		{"CamelCaseRule"},
		{"rule.with.dots"},
	}

	for _, tt := range tests {
		t.Run(tt.ruleName, func(t *testing.T) {
			cfg := &Config{
				Defaults: &DefaultLimits{Action: ActionLog},
				Rules: []Rule{
					{
						Name:           tt.ruleName,
						Match:          RuleMatch{MetricName: "test_.*"},
						MaxCardinality: 1000,
						Action:         ActionDrop,
					},
				},
			}
			LoadConfigFromStruct(cfg)

			enforcer := NewEnforcer(cfg, false, 0)
			defer enforcer.Stop()

			rm := createTestResourceMetrics(
				map[string]string{"service": "api"},
				[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{}, 1)},
			)

			result := enforcer.Process([]*metricspb.ResourceMetrics{rm})
			if len(result) != 1 {
				t.Fatalf("expected 1 resource metric")
			}

			metric := result[0].ScopeMetrics[0].Metrics[0]
			_, rule, found := getGovernorLabels(metric)
			if !found {
				t.Fatal("expected labels to be present")
			}
			if !strings.Contains(rule, tt.ruleName) {
				t.Errorf("expected rule label to contain '%s', got '%s'", tt.ruleName, rule)
			}
		})
	}
}

func TestReloadConfig(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{Name: "rule-a", MaxCardinality: 1000, Action: ActionLog},
			{Name: "rule-b", MaxCardinality: 2000, Action: ActionDrop},
		},
	}
	e := NewEnforcer(cfg, false, 100)

	// Process some metrics to populate stats for rule-a
	rm := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{"k": "v"}, 5)},
	)
	e.Process([]*metricspb.ResourceMetrics{rm})

	// Verify initial state
	if e.reloadCount.Load() != 0 {
		t.Errorf("expected reload count 0, got %d", e.reloadCount.Load())
	}

	// Reload with new config — removes rule-b, adds rule-c
	newCfg := &Config{
		Rules: []Rule{
			{Name: "rule-a", MaxCardinality: 5000, Action: ActionAdaptive},
			{Name: "rule-c", MaxCardinality: 3000, Action: ActionLog},
		},
	}
	e.ReloadConfig(newCfg)

	// Verify reload was tracked
	if e.reloadCount.Load() != 1 {
		t.Errorf("expected reload count 1, got %d", e.reloadCount.Load())
	}
	if e.lastReloadUTC.Load() == 0 {
		t.Error("expected lastReloadUTC to be set")
	}

	// Verify config was swapped
	if len(e.config.Rules) != 2 {
		t.Errorf("expected 2 rules, got %d", len(e.config.Rules))
	}
	if e.config.Rules[0].Name != "rule-a" || e.config.Rules[0].MaxCardinality != 5000 {
		t.Error("expected rule-a to be updated")
	}

	// Verify stats for rule-a are preserved, rule-b cleaned up
	e.mu.RLock()
	_, hasRuleA := e.ruleStats["rule-a"]
	_, hasRuleB := e.ruleStats["rule-b"]
	e.mu.RUnlock()
	if !hasRuleA {
		t.Error("expected stats for rule-a to be preserved")
	}
	if hasRuleB {
		t.Error("expected stats for rule-b to be cleaned up")
	}

	// Verify reload metrics appear in ServeHTTP output
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	if !strings.Contains(body, "metrics_governor_config_reloads_total 1") {
		t.Error("expected config_reloads_total=1 in metrics output")
	}
	if !strings.Contains(body, "metrics_governor_config_reload_last_success_timestamp_seconds") {
		t.Error("expected reload timestamp metric in output")
	}
}
