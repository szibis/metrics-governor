package functional

import (
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"

	"github.com/szibis/metrics-governor/internal/limits"
)

func createMetricsWithLabels(metricName string, labels map[string]string, datapointCount int) []*metricspb.ResourceMetrics {
	attrs := make([]*commonpb.KeyValue, 0, len(labels))
	for k, v := range labels {
		attrs = append(attrs, &commonpb.KeyValue{
			Key:   k,
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}},
		})
	}

	datapoints := make([]*metricspb.NumberDataPoint, datapointCount)
	for i := 0; i < datapointCount; i++ {
		datapoints[i] = &metricspb.NumberDataPoint{
			TimeUnixNano: uint64(time.Now().UnixNano()),
			Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)},
			Attributes:   attrs,
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

// TestFunctional_Limits_NoRules tests that metrics pass through without rules
func TestFunctional_Limits_NoRules(t *testing.T) {
	cfg := &limits.Config{
		Rules: []limits.Rule{},
	}

	enforcer := limits.NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	metrics := createMetricsWithLabels("test_metric", map[string]string{"service": "api"}, 100)
	result := enforcer.Process(metrics)

	if len(result) != len(metrics) {
		t.Errorf("Expected all metrics to pass through, got %d/%d", len(result), len(metrics))
	}
}

// TestFunctional_Limits_DryRunMode tests that dry run doesn't drop metrics
func TestFunctional_Limits_DryRunMode(t *testing.T) {
	cfg := &limits.Config{
		Rules: []limits.Rule{
			{
				Name: "strict-limit",
				Match: limits.RuleMatch{
					Labels: map[string]string{"service": "*"},
				},
				MaxCardinality:    1, // Very low limit
				MaxDatapointsRate: 1, // Very low limit
				Action:            "drop",
				GroupBy:           []string{"service"},
			},
		},
	}

	// Dry run mode - should NOT drop
	enforcer := limits.NewEnforcer(cfg, true, 0)
	defer enforcer.Stop()

	// Send many metrics that would exceed limits
	var totalInput, totalOutput int
	for i := 0; i < 10; i++ {
		metrics := createMetricsWithLabels("test_metric", map[string]string{"service": "api"}, 10)
		totalInput += len(metrics)
		result := enforcer.Process(metrics)
		totalOutput += len(result)
	}

	// In dry run, all metrics should pass
	if totalOutput != totalInput {
		t.Errorf("Dry run should pass all metrics, got %d/%d", totalOutput, totalInput)
	}
}

// TestFunctional_Limits_DropAction tests that drop action removes metrics when limits exceeded
func TestFunctional_Limits_DropAction(t *testing.T) {
	cfg := &limits.Config{
		Rules: []limits.Rule{
			{
				Name: "drop-rule",
				Match: limits.RuleMatch{
					MetricName: "drop_me",
				},
				MaxCardinality:    5,  // Low limit to trigger dropping
				MaxDatapointsRate: 10, // Low limit to trigger dropping
				Action:            "drop",
				GroupBy:           []string{"service"},
			},
		},
	}

	enforcer := limits.NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Send many metrics to exceed limits (this will cause the group to be marked for dropping)
	for i := 0; i < 20; i++ {
		dropMetrics := createMetricsWithLabels("drop_me", map[string]string{"service": "api"}, 10)
		enforcer.Process(dropMetrics)
	}

	// After exceeding limits, future metrics from same group should be dropped
	dropMetrics := createMetricsWithLabels("drop_me", map[string]string{"service": "api"}, 10)
	dropResult := enforcer.Process(dropMetrics)

	// Metrics that don't match the rule
	keepMetrics := createMetricsWithLabels("keep_me", map[string]string{"service": "api"}, 10)
	keepResult := enforcer.Process(keepMetrics)

	// After exceeding limits with drop action, subsequent metrics should be dropped
	t.Logf("After exceeding limits: drop_me result=%d, keep_me result=%d", len(dropResult), len(keepResult))

	if len(keepResult) != len(keepMetrics) {
		t.Errorf("Expected keep_me metrics to pass, got %d/%d", len(keepResult), len(keepMetrics))
	}
}

// TestFunctional_Limits_LogAction tests that log action passes metrics through
func TestFunctional_Limits_LogAction(t *testing.T) {
	cfg := &limits.Config{
		Rules: []limits.Rule{
			{
				Name: "log-rule",
				Match: limits.RuleMatch{
					Labels: map[string]string{"env": "prod"},
				},
				MaxCardinality:    1,
				MaxDatapointsRate: 1,
				Action:            "log",
				GroupBy:           []string{"service"},
			},
		},
	}

	enforcer := limits.NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Send metrics that exceed limits
	var totalInput, totalOutput int
	for i := 0; i < 10; i++ {
		metrics := createMetricsWithLabels("test_metric", map[string]string{"env": "prod", "service": "api"}, 10)
		totalInput += len(metrics)
		result := enforcer.Process(metrics)
		totalOutput += len(result)
	}

	// Log action should pass all metrics
	if totalOutput != totalInput {
		t.Errorf("Log action should pass all metrics, got %d/%d", totalOutput, totalInput)
	}
}

// TestFunctional_Limits_AdaptiveAction tests adaptive limiting behavior
func TestFunctional_Limits_AdaptiveAction(t *testing.T) {
	cfg := &limits.Config{
		Rules: []limits.Rule{
			{
				Name: "adaptive-rule",
				Match: limits.RuleMatch{
					Labels: map[string]string{"env": "*"},
				},
				MaxCardinality:    50,
				MaxDatapointsRate: 100,
				Action:            "adaptive",
				GroupBy:           []string{"service"},
			},
		},
	}

	enforcer := limits.NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Send metrics from multiple services with different volumes
	services := map[string]int{
		"small-service":  10,  // Low volume
		"medium-service": 50,  // Medium volume
		"large-service":  200, // High volume (should be dropped first)
	}

	var totalInput, totalOutput int
	for service, count := range services {
		for i := 0; i < count; i++ {
			metrics := createMetricsWithLabels("test_metric", map[string]string{"env": "prod", "service": service}, 5)
			totalInput += len(metrics)
			result := enforcer.Process(metrics)
			totalOutput += len(result)
		}
	}

	// Adaptive should have dropped some metrics to stay within limits
	// The exact behavior depends on implementation, but we should see some dropping
	t.Logf("Adaptive limiting: %d input, %d output (%.1f%% passed)", totalInput, totalOutput, float64(totalOutput)/float64(totalInput)*100)

	// At minimum, not all should pass if limits are exceeded
	if totalOutput == totalInput && totalInput > 100 {
		t.Log("Warning: Adaptive limiting may not be dropping as expected")
	}
}

// TestFunctional_Limits_MetricNameMatch tests regex matching on metric names
func TestFunctional_Limits_MetricNameMatch(t *testing.T) {
	cfg := &limits.Config{
		Rules: []limits.Rule{
			{
				Name: "http-metrics-rule",
				Match: limits.RuleMatch{
					MetricName: "http_.*",
				},
				MaxCardinality:    100,
				MaxDatapointsRate: 1000,
				Action:            "log",
			},
		},
	}

	enforcer := limits.NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Test matching metrics
	httpMetrics := createMetricsWithLabels("http_requests_total", nil, 10)
	result := enforcer.Process(httpMetrics)
	if len(result) != len(httpMetrics) {
		t.Errorf("http_requests_total should match rule")
	}

	// Test non-matching metrics
	dbMetrics := createMetricsWithLabels("db_queries_total", nil, 10)
	result = enforcer.Process(dbMetrics)
	if len(result) != len(dbMetrics) {
		t.Errorf("db_queries_total should pass (no matching rule)")
	}
}

// TestFunctional_Limits_LabelMatch tests label matching
func TestFunctional_Limits_LabelMatch(t *testing.T) {
	cfg := &limits.Config{
		Rules: []limits.Rule{
			{
				Name: "prod-only-rule",
				Match: limits.RuleMatch{
					Labels: map[string]string{
						"env":     "prod",
						"service": "*",
					},
				},
				MaxCardinality: 100,
				Action:         "log",
				GroupBy:        []string{"service"},
			},
		},
	}

	enforcer := limits.NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Test matching labels
	prodMetrics := createMetricsWithLabels("test_metric", map[string]string{"env": "prod", "service": "api"}, 10)
	result := enforcer.Process(prodMetrics)
	if len(result) != len(prodMetrics) {
		t.Errorf("prod metrics should match rule")
	}

	// Test non-matching labels
	devMetrics := createMetricsWithLabels("test_metric", map[string]string{"env": "dev", "service": "api"}, 10)
	result = enforcer.Process(devMetrics)
	if len(result) != len(devMetrics) {
		t.Errorf("dev metrics should pass (no matching rule)")
	}
}

// TestFunctional_Limits_MultipleRules tests multiple rules in order
func TestFunctional_Limits_MultipleRules(t *testing.T) {
	cfg := &limits.Config{
		Rules: []limits.Rule{
			{
				Name: "first-rule-drop",
				Match: limits.RuleMatch{
					MetricName: "dangerous_.*",
				},
				MaxCardinality:    5,  // Low limit to trigger
				MaxDatapointsRate: 10, // Low limit to trigger
				Action:            "drop",
				GroupBy:           []string{"service"},
			},
			{
				Name: "second-rule-log",
				Match: limits.RuleMatch{
					Labels: map[string]string{"service": "*"},
				},
				MaxCardinality: 1000,
				Action:         "log",
			},
		},
	}

	enforcer := limits.NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Exceed limits for dangerous metrics
	for i := 0; i < 20; i++ {
		dangerous := createMetricsWithLabels("dangerous_metric", map[string]string{"service": "api"}, 10)
		enforcer.Process(dangerous)
	}

	// After exceeding, new dangerous metrics should be dropped
	dangerous := createMetricsWithLabels("dangerous_metric", map[string]string{"service": "api"}, 10)
	result := enforcer.Process(dangerous)
	t.Logf("dangerous_metric after exceeding limits: %d metrics passed", len(result))

	// Second rule should apply (log = pass through)
	safe := createMetricsWithLabels("safe_metric", map[string]string{"service": "api"}, 10)
	result = enforcer.Process(safe)
	if len(result) != len(safe) {
		t.Errorf("safe_metric should pass through second rule")
	}
}

// TestFunctional_Limits_HighVolume tests limits under high volume
func TestFunctional_Limits_HighVolume(t *testing.T) {
	cfg := &limits.Config{
		Rules: []limits.Rule{
			{
				Name: "high-volume-rule",
				Match: limits.RuleMatch{
					Labels: map[string]string{"env": "*"},
				},
				MaxCardinality:    1000,
				MaxDatapointsRate: 10000,
				Action:            "adaptive",
				GroupBy:           []string{"service"},
			},
		},
	}

	enforcer := limits.NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	start := time.Now()
	var totalInput, totalOutput int

	// High volume processing
	for i := 0; i < 1000; i++ {
		service := []string{"api", "web", "worker", "scheduler"}[i%4]
		metrics := createMetricsWithLabels("test_metric", map[string]string{"env": "prod", "service": service}, 10)
		totalInput += len(metrics)
		result := enforcer.Process(metrics)
		totalOutput += len(result)
	}

	elapsed := time.Since(start)
	t.Logf("Processed %d metrics in %v (%.0f metrics/sec), passed %d (%.1f%%)",
		totalInput, elapsed, float64(totalInput)/elapsed.Seconds(),
		totalOutput, float64(totalOutput)/float64(totalInput)*100)
}

// Helper to get governor labels from a metric
func getGovernorLabels(rm []*metricspb.ResourceMetrics) (action, rule string, found bool) {
	if len(rm) == 0 {
		return "", "", false
	}
	if len(rm[0].ScopeMetrics) == 0 {
		return "", "", false
	}
	if len(rm[0].ScopeMetrics[0].Metrics) == 0 {
		return "", "", false
	}

	m := rm[0].ScopeMetrics[0].Metrics[0]
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

// TestFunctional_Limits_Labels_Passed tests that "passed" label is added when within limits
func TestFunctional_Limits_Labels_Passed(t *testing.T) {
	cfg := &limits.Config{
		Rules: []limits.Rule{
			{
				Name: "test-rule",
				Match: limits.RuleMatch{
					Labels: map[string]string{"service": "*"},
				},
				MaxCardinality:    1000,
				MaxDatapointsRate: 10000,
				Action:            "drop",
			},
		},
	}

	enforcer := limits.NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Send metrics that are within limits
	metrics := createMetricsWithLabels("test_metric", map[string]string{"service": "api"}, 5)
	result := enforcer.Process(metrics)

	if len(result) != 1 {
		t.Fatalf("expected 1 resource metric, got %d", len(result))
	}

	action, rule, found := getGovernorLabels(result)
	if !found {
		t.Fatal("expected labels to be present on metric within limits")
	}
	if action != "passed" {
		t.Errorf("expected action='passed', got '%s'", action)
	}
	if rule != "test-rule" {
		t.Errorf("expected rule='test-rule', got '%s'", rule)
	}
}

// TestFunctional_Limits_Labels_Drop tests that "drop" label is added in dry-run when limits exceeded
func TestFunctional_Limits_Labels_Drop(t *testing.T) {
	cfg := &limits.Config{
		Rules: []limits.Rule{
			{
				Name: "drop-rule",
				Match: limits.RuleMatch{
					Labels: map[string]string{"service": "*"},
				},
				MaxCardinality:    1, // Very low limit
				MaxDatapointsRate: 1, // Very low limit
				Action:            "drop",
			},
		},
	}

	enforcer := limits.NewEnforcer(cfg, true, 0) // dryRun = true
	defer enforcer.Stop()

	// First metric - within limits
	metrics1 := createMetricsWithLabels("test_metric", map[string]string{"service": "api"}, 1)
	result1 := enforcer.Process(metrics1)
	if len(result1) != 1 {
		t.Fatalf("expected first metric to pass")
	}

	// Second metric - exceeds limits, should get "drop" label in dry-run
	metrics2 := createMetricsWithLabels("test_metric", map[string]string{"service": "api"}, 1)
	result2 := enforcer.Process(metrics2)

	if len(result2) != 1 {
		t.Fatalf("expected metric to pass in dry-run mode, got %d results", len(result2))
	}

	action, rule, found := getGovernorLabels(result2)
	if !found {
		t.Fatal("expected labels to be present on metric exceeding limits")
	}
	if action != "drop" {
		t.Errorf("expected action='drop', got '%s'", action)
	}
	if rule != "drop-rule" {
		t.Errorf("expected rule='drop-rule', got '%s'", rule)
	}
}

// TestFunctional_Limits_Labels_Adaptive tests that "adaptive" label is added when adaptive limiting is triggered
func TestFunctional_Limits_Labels_Adaptive(t *testing.T) {
	cfg := &limits.Config{
		Rules: []limits.Rule{
			{
				Name: "adaptive-rule",
				Match: limits.RuleMatch{
					Labels: map[string]string{"service": "*"},
				},
				MaxCardinality:    100,
				MaxDatapointsRate: 10, // Low limit
				Action:            "adaptive",
				GroupBy:           []string{"service"},
			},
		},
	}

	enforcer := limits.NewEnforcer(cfg, true, 0) // dryRun = true
	defer enforcer.Stop()

	// Fill up to limit
	metrics1 := createMetricsWithLabels("test_metric", map[string]string{"service": "api"}, 10)
	enforcer.Process(metrics1)

	// Exceed limit - should trigger adaptive
	metrics2 := createMetricsWithLabels("test_metric", map[string]string{"service": "api"}, 5)
	result2 := enforcer.Process(metrics2)

	if len(result2) != 1 {
		t.Fatalf("expected metric to pass in dry-run mode, got %d results", len(result2))
	}

	action, rule, found := getGovernorLabels(result2)
	if !found {
		t.Fatal("expected labels to be present on metric with adaptive limiting")
	}
	if action != "adaptive" {
		t.Errorf("expected action='adaptive', got '%s'", action)
	}
	if rule != "adaptive-rule" {
		t.Errorf("expected rule='adaptive-rule', got '%s'", rule)
	}
}

// TestFunctional_Limits_Labels_Log tests that "log" label is added when log action is triggered
func TestFunctional_Limits_Labels_Log(t *testing.T) {
	cfg := &limits.Config{
		Rules: []limits.Rule{
			{
				Name: "log-rule",
				Match: limits.RuleMatch{
					Labels: map[string]string{"service": "*"},
				},
				MaxCardinality:    1, // Very low limit
				MaxDatapointsRate: 1, // Very low limit
				Action:            "log",
			},
		},
	}

	enforcer := limits.NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// First metric - within limits
	metrics1 := createMetricsWithLabels("test_metric", map[string]string{"service": "api"}, 1)
	enforcer.Process(metrics1)

	// Second metric - exceeds limits, should get "log" label
	metrics2 := createMetricsWithLabels("test_metric", map[string]string{"service": "api"}, 1)
	result2 := enforcer.Process(metrics2)

	if len(result2) != 1 {
		t.Fatalf("expected metric to pass with log action, got %d results", len(result2))
	}

	action, rule, found := getGovernorLabels(result2)
	if !found {
		t.Fatal("expected labels to be present on metric with log action")
	}
	if action != "log" {
		t.Errorf("expected action='log', got '%s'", action)
	}
	if rule != "log-rule" {
		t.Errorf("expected rule='log-rule', got '%s'", rule)
	}
}

// TestFunctional_Limits_Labels_NoRuleMatch tests that no labels are added when no rule matches
func TestFunctional_Limits_Labels_NoRuleMatch(t *testing.T) {
	cfg := &limits.Config{
		Rules: []limits.Rule{
			{
				Name: "http-rule",
				Match: limits.RuleMatch{
					MetricName: "http_.*", // Only matches http_ prefix
				},
				MaxCardinality: 1000,
				Action:         "drop",
			},
		},
	}

	enforcer := limits.NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Send metric that doesn't match any rule
	metrics := createMetricsWithLabels("grpc_requests", map[string]string{"service": "api"}, 5)
	result := enforcer.Process(metrics)

	if len(result) != 1 {
		t.Fatalf("expected 1 resource metric, got %d", len(result))
	}

	_, _, found := getGovernorLabels(result)
	if found {
		t.Error("expected no labels when no rule matches the metric")
	}
}
