package limits

import (
	"bytes"
	"net/http"
	"net/http/httptest"
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

	enforcer := NewEnforcer(cfg, true)

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
	enforcer := NewEnforcer(nil, false)

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

	enforcer := NewEnforcer(cfg, false)

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

	enforcer := NewEnforcer(cfg, false)

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

	enforcer := NewEnforcer(cfg, false)

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

	enforcer := NewEnforcer(cfg, true) // dryRun = true

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

	enforcer := NewEnforcer(cfg, false)

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

	enforcer := NewEnforcer(cfg, false)

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

	enforcer := NewEnforcer(cfg, false)

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

	enforcer := NewEnforcer(cfg, false)

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

	enforcer := NewEnforcer(cfg, false)

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

	enforcer := NewEnforcer(cfg, false)

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

	enforcer := NewEnforcer(cfg, false)

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

	enforcer := NewEnforcer(cfg, true) // dryRun = true

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

	enforcer := NewEnforcer(cfg, false)

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

	enforcer := NewEnforcer(cfg, false)

	result := enforcer.Process([]*metricspb.ResourceMetrics{})
	if len(result) != 0 {
		t.Errorf("expected empty result, got %d", len(result))
	}
}

func TestProcessNilResourceMetrics(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
	}

	enforcer := NewEnforcer(cfg, false)

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

	enforcer := NewEnforcer(cfg, false)

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
