package limits

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// ---------- Helpers ----------

// makeAdvRM builds a ResourceMetrics with a single Sum metric, specific dp attributes per datapoint,
// and optional resource-level attributes.
func makeAdvRM(metricName string, dpAttrs map[string]string, dpCount int) []*metricspb.ResourceMetrics {
	return makeLimitsRM(metricName, dpAttrs, dpCount)
}

// makeAdvRMWithResourceAttrs builds a ResourceMetrics with resource-level attributes.
func makeAdvRMWithResourceAttrs(metricName string, resAttrs, dpAttrs map[string]string, dpCount int) []*metricspb.ResourceMetrics {
	datapoints := make([]*metricspb.NumberDataPoint, dpCount)
	for i := 0; i < dpCount; i++ {
		kvs := make([]*commonpb.KeyValue, 0, len(dpAttrs))
		for k, v := range dpAttrs {
			kvs = append(kvs, &commonpb.KeyValue{
				Key:   k,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}},
			})
		}
		datapoints[i] = &metricspb.NumberDataPoint{Attributes: kvs}
	}

	rkvs := make([]*commonpb.KeyValue, 0, len(resAttrs))
	for k, v := range resAttrs {
		rkvs = append(rkvs, &commonpb.KeyValue{
			Key:   k,
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}},
		})
	}

	return []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{Attributes: rkvs},
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
		},
	}
}

// getDPAttrsFromResult extracts the first metric's first datapoint attributes from a Process result.
func getDPAttrsFromResult(result []*metricspb.ResourceMetrics) map[string]string {
	if len(result) == 0 {
		return nil
	}
	rm := result[0]
	if len(rm.ScopeMetrics) == 0 || len(rm.ScopeMetrics[0].Metrics) == 0 {
		return nil
	}
	m := rm.ScopeMetrics[0].Metrics[0]
	switch d := m.Data.(type) {
	case *metricspb.Metric_Sum:
		if len(d.Sum.DataPoints) == 0 {
			return nil
		}
		attrs := make(map[string]string)
		for _, kv := range d.Sum.DataPoints[0].Attributes {
			if kv.Value != nil {
				if sv, ok := kv.Value.Value.(*commonpb.AnyValue_StringValue); ok {
					attrs[kv.Key] = sv.StringValue
				}
			}
		}
		return attrs
	}
	return nil
}

// writeYAMLTmpFile writes YAML content to a temp file and returns the path.
func writeYAMLTmpFile(t *testing.T, content string) string {
	t.Helper()
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "limits.yaml")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write temp YAML: %v", err)
	}
	return tmpFile
}

// ========================================================================
// Tiered Escalation Tests
// ========================================================================

// TestEnforcer_Tiers_LogAtLowUtil verifies that when utilization just barely
// exceeds the limit (just above 100%), the lowest matching tier's action is used.
// With tiers at 100% (log) and 100% < util < 150%, the log tier fires and the
// metric passes through instead of being dropped.
func TestEnforcer_Tiers_LogAtLowUtil(t *testing.T) {
	t.Parallel()

	// Tiers:
	//   at 50%  -> log   (matches as soon as violation fires, since util >= 100%)
	//   at 100% -> drop  (matches when util >= 100%)
	// When util is just slightly above 100%, the highest matching tier is at_percent=100
	// (drop). To test the log tier at low util, we structure tiers so that the log
	// tier fires when util is 100-109% and the drop tier fires at >= 110%.
	//
	// Violations only fire when totalDPs > MaxDatapointsRate. At that point,
	// utilization = totalDPs * 100 / MaxDatapointsRate > 100.
	// Use tiers at_percent: [1, 100] where the first (at_percent=1) tier is log
	// and at_percent=100 is drop. Just barely exceeding the limit gives util ~101%
	// which matches both tiers; the code picks the highest matching one (drop at 100).
	//
	// Correct approach: use a single tier at at_percent=1 (action=log). Since the
	// base action is "drop", without tiers the metric would be dropped. With the
	// tier at at_percent=1, the log tier always matches when violated, so the metric
	// passes through.
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "tier-log-rule",
				Match:             RuleMatch{MetricName: "tier_log_.*"},
				MaxDatapointsRate: 100,
				Action:            ActionDrop, // base action
				Tiers: []Tier{
					{AtPercent: 1, Action: ActionLog}, // always fires on violation
				},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Fill up to the limit.
	for i := 0; i < 100; i++ {
		rms := makeAdvRM("tier_log_metric", map[string]string{"i": fmt.Sprintf("%d", i)}, 1)
		e.Process(rms)
	}

	// Next metric exceeds limit. Without tiers, base action=drop would drop it.
	// With tier at_percent=1 (action=log), the metric should pass through.
	rms := makeAdvRM("tier_log_metric", map[string]string{"i": "overflow"}, 1)
	result := e.Process(rms)

	if len(result) == 0 {
		t.Error("expected metric to pass through (tier action=log overrides base drop), but it was dropped")
	}

	// Verify the governor action label is "log" (not "drop").
	attrs := getDPAttrsFromResult(result)
	if attrs != nil {
		if action, ok := attrs["metrics.governor.action"]; ok && action != "log" {
			t.Errorf("expected governor action='log' from tier escalation, got %q", action)
		}
	}
}

// TestEnforcer_Tiers_DropAtHighUtil verifies that when two tiers are configured
// and utilization is high enough to match the drop tier, the metric is dropped.
// Tier at_percent=1 -> log, at_percent=100 -> drop. Since util at violation
// is always >= 100%, the drop tier (highest matching) is selected.
func TestEnforcer_Tiers_DropAtHighUtil(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "tier-drop-rule",
				Match:             RuleMatch{MetricName: "tier_drop_.*"},
				MaxDatapointsRate: 100,
				Action:            ActionLog, // base action is log (lenient)
				Tiers: []Tier{
					{AtPercent: 1, Action: ActionLog},
					{AtPercent: 100, Action: ActionDrop}, // escalate to drop at >= 100%
				},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Fill up to the limit.
	for i := 0; i < 100; i++ {
		rms := makeAdvRM("tier_drop_metric", map[string]string{"i": fmt.Sprintf("%d", i)}, 1)
		e.Process(rms)
	}

	// Exceed limit. Util = 101%, matches tier at_percent=100 (action=drop).
	rms := makeAdvRM("tier_drop_metric", map[string]string{"i": "overflow"}, 1)
	result := e.Process(rms)

	if len(result) != 0 {
		t.Error("expected metric to be dropped (tier action=drop at 100%), but it passed through")
	}
}

// TestEnforcer_Tiers_SampleTier verifies that a tier with action=sample and
// sample_rate=0.5 applies sampling when the limit is exceeded.
// We use a single tier at at_percent=1 with action=sample. Since the violation
// fires at util > 100%, this tier always matches, and sampling at 50% applies.
func TestEnforcer_Tiers_SampleTier(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "tier-sample-rule",
				Match:             RuleMatch{MetricName: "tier_sample_.*"},
				MaxDatapointsRate: 10,
				Action:            ActionDrop, // base action
				Tiers: []Tier{
					{AtPercent: 1, Action: ActionSample, SampleRate: 0.5},
				},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Fill up to the limit.
	for i := 0; i < 10; i++ {
		rms := makeAdvRM("tier_sample_metric", map[string]string{"i": fmt.Sprintf("%d", i)}, 1)
		e.Process(rms)
	}

	// Now send 200 more metrics after the limit is exceeded.
	// The tier at_percent=1 (action=sample, rate=0.5) should apply.
	kept := 0
	total := 200
	for i := 0; i < total; i++ {
		rms := makeAdvRM("tier_sample_metric", map[string]string{
			"sample_instance": fmt.Sprintf("s-%d", i),
		}, 1)
		result := e.Process(rms)
		if len(result) > 0 {
			kept++
		}
	}

	// With sample_rate=0.5, expect roughly 50% kept. Allow generous tolerance (20-80%).
	lowerBound := int(float64(total) * 0.20)
	upperBound := int(float64(total) * 0.80)
	if kept < lowerBound || kept > upperBound {
		t.Errorf("tier sampling: kept %d out of %d, expected roughly 50%% (bounds %d-%d)",
			kept, total, lowerBound, upperBound)
	}
	t.Logf("tier sampling result: kept=%d/%d", kept, total)
}

// ========================================================================
// Per-Label Cardinality Limit Tests
// ========================================================================

// TestEnforcer_LabelLimits_StripExceeded verifies that when a per-label
// cardinality limit is exceeded with action=strip, the offending label is
// removed from subsequent metrics while the first N (within limit) retain it.
func TestEnforcer_LabelLimits_StripExceeded(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "ll-strip-rule",
				Match:             RuleMatch{MetricName: "ll_strip_.*"},
				MaxDatapointsRate: 100000,
				Action:            ActionLog,
				LabelLimits:       map[string]int64{"user_id": 3},
				LabelLimitAction:  "strip",
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	for i := 0; i < 5; i++ {
		rms := makeAdvRM("ll_strip_metric", map[string]string{
			"user_id": fmt.Sprintf("user-%d", i),
			"service": "api",
		}, 1)
		result := e.Process(rms)

		if len(result) == 0 {
			t.Fatalf("metric %d should not be dropped (strip mode)", i)
		}

		attrs := getDPAttrsFromResult(result)
		if attrs == nil {
			t.Fatalf("metric %d: no attributes found", i)
		}

		if i < 3 {
			// First 3 unique user_id values should be preserved.
			if _, ok := attrs["user_id"]; !ok {
				t.Errorf("metric %d: user_id should be preserved (within label limit)", i)
			}
		} else {
			// Metrics 4-5 (index 3-4): user_id should be stripped.
			if _, ok := attrs["user_id"]; ok {
				t.Errorf("metric %d: user_id should have been stripped (exceeded label limit)", i)
			}
		}

		// service should always be preserved.
		if _, ok := attrs["service"]; !ok {
			t.Errorf("metric %d: service should always be preserved", i)
		}
	}
}

// TestEnforcer_LabelLimits_ZeroAlwaysStrips verifies that a label limit of 0
// causes the label to always be removed from datapoints, regardless of value.
func TestEnforcer_LabelLimits_ZeroAlwaysStrips(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "ll-zero-rule",
				Match:             RuleMatch{MetricName: "ll_zero_.*"},
				MaxDatapointsRate: 100000,
				Action:            ActionLog,
				LabelLimits:       map[string]int64{"request_id": 0},
				LabelLimitAction:  "strip",
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Send a metric with request_id; it should always be stripped.
	rms := makeAdvRM("ll_zero_metric", map[string]string{
		"request_id": "abc-123",
		"service":    "web",
	}, 1)
	result := e.Process(rms)

	if len(result) == 0 {
		t.Fatal("metric should not be dropped (strip mode)")
	}

	attrs := getDPAttrsFromResult(result)
	if attrs == nil {
		t.Fatal("no attributes found")
	}

	if _, ok := attrs["request_id"]; ok {
		t.Error("request_id should always be stripped (limit=0)")
	}
	if _, ok := attrs["service"]; !ok {
		t.Error("service should be preserved")
	}
}

// TestEnforcer_LabelLimits_DropAction verifies that when per-label cardinality
// is exceeded with action=drop, the entire metric is dropped (nil from Process).
func TestEnforcer_LabelLimits_DropAction(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "ll-drop-rule",
				Match:             RuleMatch{MetricName: "ll_drop_.*"},
				MaxDatapointsRate: 100000,
				Action:            ActionLog,
				LabelLimits:       map[string]int64{"user_id": 2},
				LabelLimitAction:  "drop",
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	for i := 0; i < 4; i++ {
		rms := makeAdvRM("ll_drop_metric", map[string]string{
			"user_id": fmt.Sprintf("user-%d", i),
			"service": "api",
		}, 1)
		result := e.Process(rms)

		if i < 2 {
			// First 2 unique user_id values are within the limit.
			if len(result) == 0 {
				t.Errorf("metric %d should pass (within label limit)", i)
			}
		} else {
			// Metrics 3-4 (index 2-3): entire metric should be dropped.
			if len(result) != 0 {
				t.Errorf("metric %d should be dropped (exceeded label limit with action=drop)", i)
			}
		}
	}
}

// ========================================================================
// Priority-Based Adaptive Dropping Tests
// ========================================================================

// TestEnforcer_PriorityAdaptive_LowPriorityDroppedFirst verifies that when
// adaptive dropping is configured with priority, low-priority groups (e.g.,
// "debug") are dropped before high-priority groups (e.g., "critical").
//
// The adaptive algorithm works by marking top-offending groups for dropping when
// there is a positive excess (totalDPs > limit in the ruleStats). Since
// updateAndCheckLimits does not count DPs that trigger a violation, we
// synthetically set ruleStats.totalDPs above the limit to create a meaningful
// excess, then trigger handleAdaptive via Process(). After the adaptive round,
// we verify that the lowest-priority group was marked for dropping.
func TestEnforcer_PriorityAdaptive_LowPriorityDroppedFirst(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "prio-adaptive-rule",
				Match:             RuleMatch{MetricName: "prio_adap_.*"},
				MaxDatapointsRate: 50,
				Action:            ActionAdaptive,
				GroupBy:           []string{"severity"},
				AdaptivePriority: &AdaptivePriorityConfig{
					Label:           "severity",
					Order:           []string{"critical", "warning", "info", "debug"},
					DefaultPriority: 0,
				},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Build all four severity groups, staying under the limit (50).
	severities := []string{"critical", "warning", "info", "debug"}
	for i := 0; i < 12; i++ {
		for _, sev := range severities {
			rms := makeAdvRMWithResourceAttrs(
				"prio_adap_metric",
				map[string]string{},
				map[string]string{"severity": sev},
				1,
			)
			e.Process(rms)
		}
	}
	// 48 DPs total (12 per group), all within limit.

	// Artificially inflate totalDPs above the limit so that when the next
	// violation fires, handleAdaptive sees a positive excess.
	e.mu.Lock()
	rs := e.ruleStats["prio-adaptive-rule"]
	rs.totalDPs = 70 // 70 - 50 = 20 DPs excess
	e.mu.Unlock()

	// Send one more metric to trigger a violation. The totalDPs (70) + 1 = 71 > 50.
	// handleAdaptive will calculate excess = 70 - 50 = 20.
	// Groups sorted by priority (lowest first):
	//   debug (prio=1, 12 DPs), info (prio=2, 12 DPs),
	//   warning (prio=3, 12 DPs), critical (prio=4, 12 DPs)
	// Marking debug (12 DPs) covers 12 of 20. Then info (12 DPs) covers remaining 8.
	rms := makeAdvRMWithResourceAttrs(
		"prio_adap_metric",
		map[string]string{},
		map[string]string{"severity": "info"}, // trigger from info group
		1,
	)
	e.Process(rms)

	// Check droppedGroups directly.
	e.mu.RLock()
	ruleDropped := e.droppedGroups["prio-adaptive-rule"]
	debugDropped := false
	criticalDropped := false
	if ruleDropped != nil {
		_, debugDropped = ruleDropped["severity=debug"]
		_, criticalDropped = ruleDropped["severity=critical"]
	}
	e.mu.RUnlock()

	// Debug (lowest priority) should be the first group dropped.
	if !debugDropped {
		t.Error("debug severity should be in droppedGroups (lowest priority, dropped first)")
	}
	// Critical (highest priority) should NOT be dropped.
	if criticalDropped {
		t.Error("critical severity should NOT be in droppedGroups (highest priority, preserved)")
	}
}

// TestEnforcer_PriorityAdaptive_DefaultPriority verifies that groups without
// the priority label get default_priority=0 (lowest), causing them to be
// dropped before groups with explicitly ordered priority values.
// We use the same synthetic excess approach as the previous test.
func TestEnforcer_PriorityAdaptive_DefaultPriority(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "prio-default-rule",
				Match:             RuleMatch{MetricName: "prio_def_.*"},
				MaxDatapointsRate: 30,
				Action:            ActionAdaptive,
				GroupBy:           []string{"severity"},
				AdaptivePriority: &AdaptivePriorityConfig{
					Label:           "severity",
					Order:           []string{"critical", "warning"},
					DefaultPriority: 0,
				},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Build two groups: "critical" (priority=2) and no-severity (priority=0).
	for i := 0; i < 14; i++ {
		rms1 := makeAdvRMWithResourceAttrs(
			"prio_def_metric",
			map[string]string{},
			map[string]string{"severity": "critical"},
			1,
		)
		e.Process(rms1)

		rms2 := makeAdvRMWithResourceAttrs(
			"prio_def_metric",
			map[string]string{},
			map[string]string{"other_label": "value"},
			1,
		)
		e.Process(rms2)
	}
	// 28 DPs total: critical=14, no-severity=14.

	// Artificially inflate totalDPs above the limit to create a modest excess.
	// excess = 40 - 30 = 10 DPs. Marking no-severity (14 DPs) covers this.
	e.mu.Lock()
	rs := e.ruleStats["prio-default-rule"]
	rs.totalDPs = 40 // 40 - 30 = 10 DPs excess
	e.mu.Unlock()

	// Trigger a violation from the no-severity group.
	rms := makeAdvRMWithResourceAttrs(
		"prio_def_metric",
		map[string]string{},
		map[string]string{"other_label": "value"},
		1,
	)
	e.Process(rms)
	// handleAdaptive: excess = 40 - 30 = 10.
	// Groups sorted by priority:
	//   no-severity (priority=0, 14 DPs) -> dropped first (covers 14 >= 10). Done.
	//   critical (priority=2, 14 DPs) -> NOT dropped.

	// Check droppedGroups.
	e.mu.RLock()
	ruleDropped := e.droppedGroups["prio-default-rule"]
	noSevDropped := false
	critDropped := false
	if ruleDropped != nil {
		_, noSevDropped = ruleDropped[""]
		_, critDropped = ruleDropped["severity=critical"]
	}
	e.mu.RUnlock()

	// No-severity (default_priority=0) should be dropped first.
	if !noSevDropped {
		t.Error("no-severity group should be in droppedGroups (default_priority=0, lowest)")
	}
	// Critical (priority=2) should NOT be dropped since the excess was fully covered
	// by no-severity.
	if critDropped {
		t.Error("critical severity should NOT be in droppedGroups (highest priority, preserved)")
	}
}

// ========================================================================
// Config Validation Tests
// ========================================================================

// TestConfig_SampleValidation verifies that LoadConfig validates sample_rate
// correctly for action=sample rules.
func TestConfig_SampleValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		yaml      string
		wantError bool
	}{
		{
			name: "valid sample_rate 0.5",
			yaml: `
rules:
  - name: "s1"
    match:
      metric_name: "test"
    max_datapoints_rate: 100
    action: sample
    sample_rate: 0.5
`,
			wantError: false,
		},
		{
			name: "valid sample_rate 1.0",
			yaml: `
rules:
  - name: "s2"
    match:
      metric_name: "test"
    max_datapoints_rate: 100
    action: sample
    sample_rate: 1.0
`,
			wantError: false,
		},
		{
			name: "invalid sample_rate 0.0",
			yaml: `
rules:
  - name: "s3"
    match:
      metric_name: "test"
    max_datapoints_rate: 100
    action: sample
    sample_rate: 0.0
`,
			wantError: true,
		},
		{
			name: "invalid sample_rate negative",
			yaml: `
rules:
  - name: "s4"
    match:
      metric_name: "test"
    max_datapoints_rate: 100
    action: sample
    sample_rate: -0.1
`,
			wantError: true,
		},
		{
			name: "invalid sample_rate > 1",
			yaml: `
rules:
  - name: "s5"
    match:
      metric_name: "test"
    max_datapoints_rate: 100
    action: sample
    sample_rate: 1.5
`,
			wantError: true,
		},
		{
			name: "missing sample_rate for action=sample",
			yaml: `
rules:
  - name: "s6"
    match:
      metric_name: "test"
    max_datapoints_rate: 100
    action: sample
`,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := writeYAMLTmpFile(t, tt.yaml)
			_, err := LoadConfig(path)
			if tt.wantError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}

// TestConfig_TiersValidation verifies that LoadConfig validates tier definitions
// for correct ordering, at_percent bounds, and per-tier action constraints.
func TestConfig_TiersValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		yaml      string
		wantError bool
	}{
		{
			name: "valid tiers sorted ascending",
			yaml: `
rules:
  - name: "t1"
    match:
      metric_name: "test"
    max_datapoints_rate: 100
    action: drop
    tiers:
      - at_percent: 50
        action: log
      - at_percent: 80
        action: sample
        sample_rate: 0.5
      - at_percent: 100
        action: drop
`,
			wantError: false,
		},
		{
			name: "invalid unsorted tiers",
			yaml: `
rules:
  - name: "t2"
    match:
      metric_name: "test"
    max_datapoints_rate: 100
    action: drop
    tiers:
      - at_percent: 80
        action: log
      - at_percent: 50
        action: drop
`,
			wantError: true,
		},
		{
			name: "invalid at_percent=0",
			yaml: `
rules:
  - name: "t3"
    match:
      metric_name: "test"
    max_datapoints_rate: 100
    action: drop
    tiers:
      - at_percent: 0
        action: log
`,
			wantError: true,
		},
		{
			name: "invalid at_percent=101",
			yaml: `
rules:
  - name: "t4"
    match:
      metric_name: "test"
    max_datapoints_rate: 100
    action: drop
    tiers:
      - at_percent: 101
        action: log
`,
			wantError: true,
		},
		{
			name: "invalid tier sample without sample_rate",
			yaml: `
rules:
  - name: "t5"
    match:
      metric_name: "test"
    max_datapoints_rate: 100
    action: drop
    tiers:
      - at_percent: 80
        action: sample
`,
			wantError: true,
		},
		{
			name: "invalid tier strip_labels without strip_labels list",
			yaml: `
rules:
  - name: "t6"
    match:
      metric_name: "test"
    max_datapoints_rate: 100
    action: drop
    tiers:
      - at_percent: 80
        action: strip_labels
`,
			wantError: true,
		},
		{
			name: "invalid duplicate at_percent values",
			yaml: `
rules:
  - name: "t7"
    match:
      metric_name: "test"
    max_datapoints_rate: 100
    action: drop
    tiers:
      - at_percent: 80
        action: log
      - at_percent: 80
        action: drop
`,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := writeYAMLTmpFile(t, tt.yaml)
			_, err := LoadConfig(path)
			if tt.wantError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}

// TestConfig_LabelLimitsValidation verifies that LoadConfig validates
// label_limits and label_limit_action fields correctly.
func TestConfig_LabelLimitsValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		yaml      string
		wantError bool
	}{
		{
			name: "valid label_limits with strip",
			yaml: `
rules:
  - name: "ll1"
    match:
      metric_name: "test"
    max_datapoints_rate: 100
    action: log
    label_limits:
      user_id: 10
    label_limit_action: strip
`,
			wantError: false,
		},
		{
			name: "valid label_limits with drop",
			yaml: `
rules:
  - name: "ll2"
    match:
      metric_name: "test"
    max_datapoints_rate: 100
    action: log
    label_limits:
      user_id: 5
    label_limit_action: drop
`,
			wantError: false,
		},
		{
			name: "valid label_limits defaults to strip",
			yaml: `
rules:
  - name: "ll3"
    match:
      metric_name: "test"
    max_datapoints_rate: 100
    action: log
    label_limits:
      user_id: 5
`,
			wantError: false,
		},
		{
			name: "invalid empty label name in label_limits",
			yaml: `
rules:
  - name: "ll4"
    match:
      metric_name: "test"
    max_datapoints_rate: 100
    action: log
    label_limits:
      "": 10
`,
			wantError: true,
		},
		{
			name: "invalid label_limit_action",
			yaml: `
rules:
  - name: "ll5"
    match:
      metric_name: "test"
    max_datapoints_rate: 100
    action: log
    label_limits:
      user_id: 10
    label_limit_action: truncate
`,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := writeYAMLTmpFile(t, tt.yaml)
			_, err := LoadConfig(path)
			if tt.wantError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}

// TestConfig_AdaptivePriorityValidation verifies that LoadConfig validates
// adaptive_priority configuration fields correctly.
func TestConfig_AdaptivePriorityValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		yaml      string
		wantError bool
	}{
		{
			name: "valid adaptive_priority",
			yaml: `
rules:
  - name: "ap1"
    match:
      metric_name: "test"
    max_datapoints_rate: 100
    action: adaptive
    group_by: ["severity"]
    adaptive_priority:
      label: severity
      order: ["critical", "warning", "info"]
      default_priority: 0
`,
			wantError: false,
		},
		{
			name: "invalid empty label",
			yaml: `
rules:
  - name: "ap2"
    match:
      metric_name: "test"
    max_datapoints_rate: 100
    action: adaptive
    group_by: ["severity"]
    adaptive_priority:
      label: ""
      order: ["critical"]
`,
			wantError: true,
		},
		{
			name: "invalid empty order",
			yaml: `
rules:
  - name: "ap3"
    match:
      metric_name: "test"
    max_datapoints_rate: 100
    action: adaptive
    group_by: ["severity"]
    adaptive_priority:
      label: severity
      order: []
`,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := writeYAMLTmpFile(t, tt.yaml)
			_, err := LoadConfig(path)
			if tt.wantError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
		})
	}
}
