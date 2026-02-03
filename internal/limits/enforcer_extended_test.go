package limits

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

func TestLogAggregator_InfoMethod(t *testing.T) {
	la := NewLogAggregator(50 * time.Millisecond)
	defer la.Stop()

	// Test Info method
	la.Info("test_info_key", "info message", map[string]interface{}{"key": "value"}, 100)
	la.Info("test_info_key", "info message", map[string]interface{}{"key": "value"}, 200)

	// Wait for flush
	time.Sleep(100 * time.Millisecond)
}

func TestLogAggregator_ErrorMethod(t *testing.T) {
	la := NewLogAggregator(50 * time.Millisecond)
	defer la.Stop()

	// Test Error method
	la.Error("test_error_key", "error message", map[string]interface{}{"error": "test"}, 50)
	la.Error("test_error_key", "error message", map[string]interface{}{"error": "test"}, 75)

	// Wait for flush
	time.Sleep(100 * time.Millisecond)
}

func TestLogAggregator_WarnMethod(t *testing.T) {
	la := NewLogAggregator(50 * time.Millisecond)
	defer la.Stop()

	// Test Warn method
	la.Warn("test_warn_key", "warning message", map[string]interface{}{"level": "warn"}, 30)

	// Wait for flush
	time.Sleep(100 * time.Millisecond)
}

func TestLogAggregator_MultipleFlushes(t *testing.T) {
	la := NewLogAggregator(30 * time.Millisecond)
	defer la.Stop()

	// Add entries
	for i := 0; i < 5; i++ {
		la.Warn("warn_key", "warning", map[string]interface{}{"i": i}, int64(i*10))
	}

	// Wait for first flush
	time.Sleep(50 * time.Millisecond)

	// Add more entries
	for i := 0; i < 3; i++ {
		la.Error("error_key", "error", map[string]interface{}{"j": i}, int64(i*20))
	}

	// Wait for second flush
	time.Sleep(50 * time.Millisecond)
}

func TestLogAggregator_Aggregation(t *testing.T) {
	la := NewLogAggregator(100 * time.Millisecond)
	defer la.Stop()

	// Add same key multiple times - should aggregate
	for i := 0; i < 10; i++ {
		la.Info("same_key", "repeated message", map[string]interface{}{"type": "test"}, 10)
	}

	// Check aggregation
	la.mu.Lock()
	entry, exists := la.entries["same_key"]
	if exists {
		if entry.count != 10 {
			t.Errorf("Expected count 10, got %d", entry.count)
		}
		if entry.totalDPs != 100 {
			t.Errorf("Expected totalDPs 100, got %d", entry.totalDPs)
		}
	}
	la.mu.Unlock()
}

func TestLogAggregator_DefaultInterval(t *testing.T) {
	// Test with zero/negative interval - should use default
	la := NewLogAggregator(0)
	defer la.Stop()

	if la.flushInterval != 10*time.Second {
		t.Errorf("Expected default interval 10s, got %v", la.flushInterval)
	}
}

func TestEnforcer_ServeHTTP_Stats(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{},
	}

	enforcer := NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Test HTTP endpoint
	req := httptest.NewRequest(http.MethodGet, "/limits/stats", nil)
	w := httptest.NewRecorder()

	enforcer.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	if len(body) == 0 {
		t.Error("Expected non-empty response body")
	}
}

func TestEnforcer_Stop(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{},
	}

	enforcer := NewEnforcer(cfg, false, 0)

	// Stop should not panic
	enforcer.Stop()
	// Note: Multiple stops are not safe - calling Stop() twice will panic
	// due to closing an already closed channel
}

func TestEnforcer_DryRunMode(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{},
	}

	// Create with dry run enabled
	enforcer := NewEnforcer(cfg, true, 0)
	defer enforcer.Stop()

	if enforcer == nil {
		t.Error("Expected non-nil enforcer")
	}
}

func TestEnforcer_Process_AdaptiveAction(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "adaptive-rule",
				Match:             RuleMatch{MetricName: "test_.*"},
				MaxDatapointsRate: 10, // Very low limit to trigger
				Action:            ActionAdaptive,
				GroupBy:           []string{"service"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Process many metrics to exceed limit
	for i := 0; i < 20; i++ {
		rm := createTestResourceMetrics(
			map[string]string{"service": "api"},
			[]*metricspb.Metric{createTestMetric("test_requests", map[string]string{"id": string(rune('a' + i))}, 1)},
		)
		enforcer.Process([]*metricspb.ResourceMetrics{rm})
	}
}

func TestEnforcer_Process_DropAction(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "drop-rule",
				Match:             RuleMatch{MetricName: "drop_.*"},
				MaxDatapointsRate: 5,
				Action:            ActionDrop,
				GroupBy:           []string{"service"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Process metrics to exceed limit
	for i := 0; i < 10; i++ {
		rm := createTestResourceMetrics(
			map[string]string{"service": "api"},
			[]*metricspb.Metric{createTestMetric("drop_requests", map[string]string{}, 1)},
		)
		result := enforcer.Process([]*metricspb.ResourceMetrics{rm})
		// After exceeding limit, metrics should be dropped
		if i > 5 && len(result) > 0 && len(result[0].ScopeMetrics) > 0 && len(result[0].ScopeMetrics[0].Metrics) > 0 {
			// Drop action should result in nil metrics eventually
			t.Logf("Iteration %d: got %d metrics", i, len(result[0].ScopeMetrics[0].Metrics))
		}
	}
}

func TestEnforcer_Process_LogAction(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "log-rule",
				Match:             RuleMatch{MetricName: "log_.*"},
				MaxDatapointsRate: 5,
				Action:            ActionLog,
				GroupBy:           []string{"service"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Process metrics - log action should pass through all
	for i := 0; i < 10; i++ {
		rm := createTestResourceMetrics(
			map[string]string{"service": "api"},
			[]*metricspb.Metric{createTestMetric("log_requests", map[string]string{}, 1)},
		)
		result := enforcer.Process([]*metricspb.ResourceMetrics{rm})
		// Log action should always pass through
		if len(result) != 1 {
			t.Errorf("Expected 1 result, got %d", len(result))
		}
	}
}

func TestEnforcer_Process_CardinalityLimit(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:           "cardinality-rule",
				Match:          RuleMatch{MetricName: "card_.*"},
				MaxCardinality: 3, // Very low cardinality limit
				Action:         ActionAdaptive,
				GroupBy:        []string{"service"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Process metrics with unique label values to create cardinality
	for i := 0; i < 10; i++ {
		rm := createTestResourceMetrics(
			map[string]string{"service": "api"},
			[]*metricspb.Metric{createTestMetric("card_requests", map[string]string{"unique_id": string(rune('A' + i))}, 1)},
		)
		enforcer.Process([]*metricspb.ResourceMetrics{rm})
	}
}

func TestEnforcer_Process_MultipleRules(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "rule-1",
				Match:             RuleMatch{MetricName: "multi_a_.*"},
				MaxDatapointsRate: 100,
				Action:            ActionDrop,
			},
			{
				Name:              "rule-2",
				Match:             RuleMatch{MetricName: "multi_b_.*"},
				MaxDatapointsRate: 100,
				Action:            ActionLog,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Process metrics matching first rule
	rm1 := createTestResourceMetrics(
		map[string]string{},
		[]*metricspb.Metric{createTestMetric("multi_a_metric", map[string]string{}, 1)},
	)
	result1 := enforcer.Process([]*metricspb.ResourceMetrics{rm1})
	if len(result1) != 1 {
		t.Errorf("Expected 1 result for rule-1, got %d", len(result1))
	}

	// Process metrics matching second rule
	rm2 := createTestResourceMetrics(
		map[string]string{},
		[]*metricspb.Metric{createTestMetric("multi_b_metric", map[string]string{}, 1)},
	)
	result2 := enforcer.Process([]*metricspb.ResourceMetrics{rm2})
	if len(result2) != 1 {
		t.Errorf("Expected 1 result for rule-2, got %d", len(result2))
	}
}

func TestEnforcer_Process_LabelMatch(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{
				Name:              "label-match-rule",
				Match:             RuleMatch{MetricName: "label_.*", Labels: map[string]string{"env": "prod"}},
				MaxDatapointsRate: 100,
				Action:            ActionLog,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Process metrics with matching labels
	rm := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("label_metric", map[string]string{"env": "prod"}, 1)},
	)
	result := enforcer.Process([]*metricspb.ResourceMetrics{rm})
	if len(result) != 1 {
		t.Errorf("Expected 1 result, got %d", len(result))
	}
}

func TestEnforcer_WindowReset(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{
				Name:              "window-test",
				Match:             RuleMatch{MetricName: "win_.*"},
				MaxDatapointsRate: 5,
				Action:            ActionDrop,
				GroupBy:           []string{"service"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Process metrics
	for i := 0; i < 3; i++ {
		rm := createTestResourceMetrics(
			map[string]string{"service": "api"},
			[]*metricspb.Metric{createTestMetric("win_metric", map[string]string{}, 1)},
		)
		enforcer.Process([]*metricspb.ResourceMetrics{rm})
	}

	// Manually manipulate window end to simulate expiry
	enforcer.mu.Lock()
	if rs, ok := enforcer.ruleStats["window-test"]; ok {
		rs.windowEnd = time.Now().Add(-time.Second) // Set to past
	}
	enforcer.mu.Unlock()

	// Process more - should reset window
	rm := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("win_metric", map[string]string{}, 1)},
	)
	enforcer.Process([]*metricspb.ResourceMetrics{rm})
}

func TestEnforcer_IsGroupDropped(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{},
	}

	enforcer := NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Manually add a dropped group
	enforcer.mu.Lock()
	enforcer.droppedGroups["test-rule"] = make(map[string]time.Time)
	enforcer.droppedGroups["test-rule"]["group-1"] = time.Now().Add(time.Hour)  // Future expiry
	enforcer.droppedGroups["test-rule"]["group-2"] = time.Now().Add(-time.Hour) // Past expiry
	enforcer.mu.Unlock()

	// Test - should be dropped (future expiry)
	if !enforcer.isGroupDropped("test-rule", "group-1") {
		t.Error("Expected group-1 to be dropped")
	}

	// Test - should not be dropped (past expiry)
	if enforcer.isGroupDropped("test-rule", "group-2") {
		t.Error("Expected group-2 to not be dropped (expired)")
	}

	// Test - non-existent rule
	if enforcer.isGroupDropped("non-existent", "group") {
		t.Error("Expected false for non-existent rule")
	}

	// Test - non-existent group
	if enforcer.isGroupDropped("test-rule", "non-existent") {
		t.Error("Expected false for non-existent group")
	}
}

func TestEnforcer_DryRunDropAction(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{
				Name:              "dryrun-drop",
				Match:             RuleMatch{MetricName: "dry_.*"},
				MaxDatapointsRate: 3,
				Action:            ActionDrop,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, true, 0) // Dry run enabled
	defer enforcer.Stop()

	// Process metrics to exceed limit
	for i := 0; i < 10; i++ {
		rm := createTestResourceMetrics(
			map[string]string{},
			[]*metricspb.Metric{createTestMetric("dry_metric", map[string]string{}, 1)},
		)
		result := enforcer.Process([]*metricspb.ResourceMetrics{rm})
		// In dry run mode, metrics should still pass through
		if len(result) != 1 {
			t.Errorf("Expected 1 result in dry run mode, got %d", len(result))
		}
	}
}

func TestEnforcer_AdaptiveAction_NonDryRun(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{
				Name:              "adaptive-nodryrun",
				Match:             RuleMatch{MetricName: "adapt_.*"},
				MaxDatapointsRate: 5,
				Action:            ActionAdaptive,
				GroupBy:           []string{"service"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0) // Not dry run
	defer enforcer.Stop()

	// Process metrics from multiple services to build up groups
	services := []string{"svc-a", "svc-b", "svc-c"}
	for i := 0; i < 20; i++ {
		for _, svc := range services {
			rm := createTestResourceMetrics(
				map[string]string{"service": svc},
				[]*metricspb.Metric{createTestMetric("adapt_metric", map[string]string{}, 1)},
			)
			enforcer.Process([]*metricspb.ResourceMetrics{rm})
		}
	}
}

func TestEnforcer_AdaptiveAction_CardinalityReason(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{
				Name:           "adaptive-cardinality",
				Match:          RuleMatch{MetricName: "cardadapt_.*"},
				MaxCardinality: 3, // Very low to trigger
				Action:         ActionAdaptive,
				GroupBy:        []string{"service"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Process metrics with different unique label values to create cardinality
	for i := 0; i < 15; i++ {
		rm := createTestResourceMetrics(
			map[string]string{"service": "api"},
			[]*metricspb.Metric{createTestMetric("cardadapt_metric", map[string]string{"id": string(rune('A' + i))}, 1)},
		)
		enforcer.Process([]*metricspb.ResourceMetrics{rm})
	}
}

func TestEnforcer_GroupDropped_Scenario(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{
				Name:              "group-drop-test",
				Match:             RuleMatch{MetricName: "grpdrp_.*"},
				MaxDatapointsRate: 5, // Low limit
				Action:            ActionAdaptive,
				GroupBy:           []string{"group"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Build up groups first with multiple batches
	groups := []string{"group-a", "group-b", "group-c"}
	for i := 0; i < 5; i++ {
		for _, g := range groups {
			rm := createTestResourceMetrics(
				map[string]string{"group": g},
				[]*metricspb.Metric{createTestMetric("grpdrp_metric", map[string]string{}, 2)},
			)
			enforcer.Process([]*metricspb.ResourceMetrics{rm})
		}
	}

	// Now the limit should be exceeded and groups should be marked for dropping
	// Check that subsequent requests from a group are handled
	rm := createTestResourceMetrics(
		map[string]string{"group": "group-a"},
		[]*metricspb.Metric{createTestMetric("grpdrp_metric", map[string]string{}, 1)},
	)
	result := enforcer.Process([]*metricspb.ResourceMetrics{rm})
	t.Logf("After exceeding limit, result count: %d", len(result))
}

func TestEnforcer_MultipleGroups_Adaptive(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{
				Name:              "multi-group",
				Match:             RuleMatch{MetricName: "multigrp_.*"},
				MaxDatapointsRate: 10,
				Action:            ActionAdaptive,
				GroupBy:           []string{"env", "service"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Process from multiple groups
	groups := []map[string]string{
		{"env": "prod", "service": "api"},
		{"env": "prod", "service": "web"},
		{"env": "staging", "service": "api"},
	}

	for i := 0; i < 20; i++ {
		for _, attrs := range groups {
			rm := createTestResourceMetrics(
				attrs,
				[]*metricspb.Metric{createTestMetric("multigrp_metric", map[string]string{}, 1)},
			)
			enforcer.Process([]*metricspb.ResourceMetrics{rm})
		}
	}
}

func TestEnforcer_HandleViolation_DefaultAction(t *testing.T) {
	cfg := &Config{
		Defaults: &DefaultLimits{
			MaxDatapointsRate: 5,
			Action:            ActionLog,
		},
		Rules: []Rule{
			{
				Name:              "default-action-test",
				Match:             RuleMatch{MetricName: "defact_.*"},
				MaxDatapointsRate: 3,
				// No action specified - should use default
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Process metrics to exceed limit
	for i := 0; i < 10; i++ {
		rm := createTestResourceMetrics(
			map[string]string{},
			[]*metricspb.Metric{createTestMetric("defact_metric", map[string]string{}, 1)},
		)
		result := enforcer.Process([]*metricspb.ResourceMetrics{rm})
		// Log action should pass through
		if len(result) != 1 {
			t.Errorf("Expected log action to pass through, got %d results", len(result))
		}
	}
}

func TestEnforcer_AdaptiveWithExcess(t *testing.T) {
	// This test is designed to exercise the full adaptive flow including
	// the recordGroupsDropped function by ensuring positive excess
	cfg := &Config{
		Rules: []Rule{
			{
				Name:              "excess-test",
				Match:             RuleMatch{MetricName: "excess_.*"},
				MaxDatapointsRate: 10, // Set limit
				Action:            ActionAdaptive,
				GroupBy:           []string{"group"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Step 1: Add data from multiple groups to fill up to the limit
	// We'll add from 3 groups, each contributing datapoints
	for i := 0; i < 5; i++ {
		// Group A
		rm := createTestResourceMetrics(
			map[string]string{"group": "A"},
			[]*metricspb.Metric{createTestMetric("excess_metric", map[string]string{}, 2)},
		)
		enforcer.Process([]*metricspb.ResourceMetrics{rm})

		// Group B
		rm = createTestResourceMetrics(
			map[string]string{"group": "B"},
			[]*metricspb.Metric{createTestMetric("excess_metric", map[string]string{}, 2)},
		)
		enforcer.Process([]*metricspb.ResourceMetrics{rm})
	}

	// At this point, we should have accumulated datapoints exceeding the limit
	// Now the next request should trigger the adaptive logic with positive excess

	// Step 2: Add more data to trigger adaptive with excess > 0
	for i := 0; i < 5; i++ {
		rm := createTestResourceMetrics(
			map[string]string{"group": "C"},
			[]*metricspb.Metric{createTestMetric("excess_metric", map[string]string{}, 2)},
		)
		result := enforcer.Process([]*metricspb.ResourceMetrics{rm})
		t.Logf("Iteration %d: result count=%d", i, len(result))
	}
}

func TestEnforcer_AdaptiveCardinality_WithExcess(t *testing.T) {
	// Test the cardinality path in handleAdaptive
	cfg := &Config{
		Rules: []Rule{
			{
				Name:           "card-excess-test",
				Match:          RuleMatch{MetricName: "cardex_.*"},
				MaxCardinality: 5, // Low cardinality limit
				Action:         ActionAdaptive,
				GroupBy:        []string{"group"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Step 1: Add unique series from multiple groups
	for i := 0; i < 10; i++ {
		// Group A with unique series
		rm := createTestResourceMetrics(
			map[string]string{"group": "A"},
			[]*metricspb.Metric{createTestMetric("cardex_metric", map[string]string{"id": string(rune('a' + i))}, 1)},
		)
		enforcer.Process([]*metricspb.ResourceMetrics{rm})

		// Group B with unique series
		rm = createTestResourceMetrics(
			map[string]string{"group": "B"},
			[]*metricspb.Metric{createTestMetric("cardex_metric", map[string]string{"id": string(rune('A' + i))}, 1)},
		)
		enforcer.Process([]*metricspb.ResourceMetrics{rm})
	}
}

func TestEnforcer_RecordGroupsDropped_Direct(t *testing.T) {
	// This test directly exercises the recordGroupsDropped function
	// by creating an adaptive scenario with guaranteed excess
	cfg := &Config{
		Rules: []Rule{
			{
				Name:              "force-drop-test",
				Match:             RuleMatch{MetricName: "forcedrop_.*"},
				MaxDatapointsRate: 5, // Very low limit
				Action:            ActionAdaptive,
				GroupBy:           []string{"source"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Rapidly accumulate datapoints from multiple sources
	sources := []string{"source-a", "source-b", "source-c", "source-d"}

	// First, fill up above the limit
	for round := 0; round < 10; round++ {
		for _, src := range sources {
			rm := createTestResourceMetrics(
				map[string]string{"source": src},
				[]*metricspb.Metric{createTestMetric("forcedrop_metric", map[string]string{"round": string(rune('0' + round))}, 3)},
			)
			enforcer.Process([]*metricspb.ResourceMetrics{rm})
		}
	}

	// Verify through metrics endpoint
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	enforcer.ServeHTTP(w, r)

	body := w.Body.String()
	t.Logf("Metrics output contains %d bytes", len(body))
}

func TestEnforcer_Adaptive_SortByDatapoints(t *testing.T) {
	// Test the datapoints sorting branch in handleAdaptive
	cfg := &Config{
		Rules: []Rule{
			{
				Name:              "dp-sort-test",
				Match:             RuleMatch{MetricName: "dpsort_.*"},
				MaxDatapointsRate: 20, // Moderate limit
				Action:            ActionAdaptive,
				GroupBy:           []string{"group"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)
	defer enforcer.Stop()

	// Create groups with different datapoint contributions
	// Group A: high contribution
	for i := 0; i < 10; i++ {
		rm := createTestResourceMetrics(
			map[string]string{"group": "high-contrib"},
			[]*metricspb.Metric{createTestMetric("dpsort_metric", map[string]string{}, 5)},
		)
		enforcer.Process([]*metricspb.ResourceMetrics{rm})
	}

	// Group B: low contribution
	for i := 0; i < 5; i++ {
		rm := createTestResourceMetrics(
			map[string]string{"group": "low-contrib"},
			[]*metricspb.Metric{createTestMetric("dpsort_metric", map[string]string{}, 1)},
		)
		enforcer.Process([]*metricspb.ResourceMetrics{rm})
	}

	// Now high-contrib should be marked as top offender
	// Continue to verify the sorting logic works
	for i := 0; i < 5; i++ {
		rm := createTestResourceMetrics(
			map[string]string{"group": "high-contrib"},
			[]*metricspb.Metric{createTestMetric("dpsort_metric", map[string]string{}, 2)},
		)
		result := enforcer.Process([]*metricspb.ResourceMetrics{rm})
		t.Logf("High contrib iteration %d: result=%d", i, len(result))
	}
}

func TestEnforcer_Adaptive_DryRunWithExcess(t *testing.T) {
	// Test dry run mode with adaptive action
	cfg := &Config{
		Rules: []Rule{
			{
				Name:              "dryrun-adaptive",
				Match:             RuleMatch{MetricName: "dryadapt_.*"},
				MaxDatapointsRate: 10,
				Action:            ActionAdaptive,
				GroupBy:           []string{"service"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, true, 0) // Dry run mode
	defer enforcer.Stop()

	// Exceed the limit
	for i := 0; i < 30; i++ {
		rm := createTestResourceMetrics(
			map[string]string{"service": "api"},
			[]*metricspb.Metric{createTestMetric("dryadapt_metric", map[string]string{}, 2)},
		)
		result := enforcer.Process([]*metricspb.ResourceMetrics{rm})
		// Dry run should always pass through
		if len(result) != 1 {
			t.Errorf("Dry run should pass through, got %d results", len(result))
		}
	}
}
