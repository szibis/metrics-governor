package limits

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/cardinality"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// ---------------------------------------------------------------------------
// SetDryRun / DryRun
// ---------------------------------------------------------------------------

func TestSetDryRun_And_DryRun(t *testing.T) {
	e := NewEnforcer(nil, false, 0)
	defer e.Stop()

	if e.DryRun() {
		t.Fatal("expected DryRun() == false initially")
	}

	e.SetDryRun(true)
	if !e.DryRun() {
		t.Fatal("expected DryRun() == true after SetDryRun(true)")
	}

	e.SetDryRun(false)
	if e.DryRun() {
		t.Fatal("expected DryRun() == false after SetDryRun(false)")
	}
}

// ---------------------------------------------------------------------------
// cleanupExpiredDroppedGroups
// ---------------------------------------------------------------------------

func TestCleanupExpiredDroppedGroups(t *testing.T) {
	e := NewEnforcer(nil, false, 0)
	defer e.Stop()

	now := time.Now()

	e.mu.Lock()
	e.droppedGroups["rule-a"] = map[string]time.Time{
		"g1": now.Add(-time.Hour), // expired
		"g2": now.Add(time.Hour),  // still active
	}
	e.droppedGroups["rule-b"] = map[string]time.Time{
		"g3": now.Add(-time.Minute), // expired (only entry)
	}
	e.cleanupExpiredDroppedGroups(now)
	e.mu.Unlock()

	e.mu.RLock()
	defer e.mu.RUnlock()

	// rule-a should keep g2 only
	if _, ok := e.droppedGroups["rule-a"]["g1"]; ok {
		t.Error("g1 should have been cleaned up (expired)")
	}
	if _, ok := e.droppedGroups["rule-a"]["g2"]; !ok {
		t.Error("g2 should still exist (active)")
	}

	// rule-b should be deleted entirely (all entries expired)
	if _, ok := e.droppedGroups["rule-b"]; ok {
		t.Error("rule-b should have been deleted (all entries expired)")
	}
}

// ---------------------------------------------------------------------------
// ReloadConfig: edge cases (nil old config, droppedGroups cleanup, cache recreation)
// ---------------------------------------------------------------------------

func TestReloadConfig_NilOldConfig(t *testing.T) {
	e := NewEnforcer(nil, false, 100)
	defer e.Stop()

	newCfg := &Config{
		Rules: []Rule{
			{Name: "new-rule", MaxCardinality: 100, Action: ActionLog},
		},
	}
	// Should not panic even though old config is nil
	e.ReloadConfig(newCfg)

	if e.config != newCfg {
		t.Fatal("config should be updated")
	}
	if e.reloadCount.Load() != 1 {
		t.Fatal("reload count should be 1")
	}
}

func TestReloadConfig_CleansDroppedGroups(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{Name: "keep-rule", MaxCardinality: 100, Action: ActionDrop},
			{Name: "remove-rule", MaxCardinality: 100, Action: ActionDrop},
		},
	}
	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Manually add dropped groups for both rules
	e.mu.Lock()
	e.droppedGroups["keep-rule"] = map[string]time.Time{"g": time.Now().Add(time.Hour)}
	e.droppedGroups["remove-rule"] = map[string]time.Time{"g": time.Now().Add(time.Hour)}
	e.mu.Unlock()

	// Reload with only keep-rule
	newCfg := &Config{
		Rules: []Rule{
			{Name: "keep-rule", MaxCardinality: 200, Action: ActionLog},
		},
	}
	e.ReloadConfig(newCfg)

	e.mu.RLock()
	defer e.mu.RUnlock()

	if _, ok := e.droppedGroups["keep-rule"]; !ok {
		t.Error("keep-rule droppedGroups should be preserved")
	}
	if _, ok := e.droppedGroups["remove-rule"]; ok {
		t.Error("remove-rule droppedGroups should be cleaned up")
	}
}

func TestReloadConfig_RecreatesCache(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{Name: "test", Match: RuleMatch{MetricName: "test_.*"}, Action: ActionLog},
		},
	}
	LoadConfigFromStruct(cfg)
	e := NewEnforcer(cfg, false, 100)
	defer e.Stop()

	// Populate cache
	e.findMatchingRule("test_metric", nil)
	if e.ruleMatchCache.Size() == 0 {
		t.Fatal("cache should have entries")
	}

	newCfg := &Config{
		Rules: []Rule{
			{Name: "new-test", Match: RuleMatch{MetricName: "new_.*"}, Action: ActionDrop},
		},
	}
	e.ReloadConfig(newCfg)

	// Cache should be cleared (size 0)
	if e.ruleMatchCache.Size() != 0 {
		t.Error("cache should be cleared after reload")
	}
}

// ---------------------------------------------------------------------------
// processMetric: dropped group path in non-dry-run
// ---------------------------------------------------------------------------

func TestProcessMetric_DroppedGroupNonDryRun(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{
				Name:              "drop-test",
				Match:             RuleMatch{MetricName: "test_.*"},
				MaxDatapointsRate: 5,
				Action:            ActionAdaptive,
				GroupBy:           []string{"service"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, false, 0) // non-dry-run
	defer e.Stop()

	// Manually mark a group as dropped
	e.mu.Lock()
	e.droppedGroups["drop-test"] = map[string]time.Time{
		"service=api": time.Now().Add(time.Hour),
	}
	e.mu.Unlock()

	rm := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{}, 3)},
	)

	result := e.Process([]*metricspb.ResourceMetrics{rm})
	if len(result) != 0 {
		t.Error("dropped group in non-dry-run should return nil (0 results)")
	}
}

func TestProcessMetric_DroppedGroupDryRunLabel(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{
				Name:              "drop-test",
				Match:             RuleMatch{MetricName: "test_.*"},
				MaxDatapointsRate: 5,
				Action:            ActionAdaptive,
				GroupBy:           []string{"service"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, true, 0) // dry-run
	defer e.Stop()

	// Manually mark a group as dropped
	e.mu.Lock()
	e.droppedGroups["drop-test"] = map[string]time.Time{
		"service=api": time.Now().Add(time.Hour),
	}
	e.mu.Unlock()

	rm := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{createTestMetric("test_metric", map[string]string{}, 1)},
	)

	result := e.Process([]*metricspb.ResourceMetrics{rm})
	if len(result) != 1 {
		t.Fatal("dry-run dropped group should pass through")
	}

	action, _, found := getGovernorLabels(result[0].ScopeMetrics[0].Metrics[0])
	if !found {
		t.Fatal("expected labels")
	}
	if action != "drop" {
		t.Errorf("expected action='drop', got '%s'", action)
	}
}

// ---------------------------------------------------------------------------
// handleAdaptive: excess <= 0 path (no excess)
// ---------------------------------------------------------------------------

func TestHandleAdaptive_NoExcess(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{
				Name:              "no-excess-rule",
				Match:             RuleMatch{MetricName: "noex_.*"},
				MaxDatapointsRate: 100,
				Action:            ActionAdaptive,
				GroupBy:           []string{"service"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Set up rule stats manually so that excess is <= 0 when handleAdaptive is invoked
	e.mu.Lock()
	e.ruleStats["no-excess-rule"] = &ruleStats{
		groups: map[string]*groupStats{
			"service=api": {
				datapoints:  5,
				cardinality: cardinality.NewTrackerFromGlobal(),
				windowEnd:   time.Now().Add(time.Minute),
			},
		},
		totalDPs:  5,
		totalCard: 2,
		windowEnd: time.Now().Add(time.Minute),
	}
	e.mu.Unlock()

	m := createTestMetric("noex_metric", map[string]string{}, 1)
	resourceAttrs := map[string]string{"service": "api"}

	result := e.handleAdaptive(&cfg.Rules[0], "service=api", m, resourceAttrs, "datapoints_rate", 1)
	if result == nil {
		t.Fatal("when excess <= 0, handleAdaptive should return non-nil metric with 'adaptive' label")
	}

	action, _, found := getGovernorLabels(result)
	if !found {
		t.Fatal("expected labels")
	}
	if action != "adaptive" {
		t.Errorf("expected action='adaptive', got '%s'", action)
	}
}

// ---------------------------------------------------------------------------
// handleAdaptive: nil ruleStats path
// ---------------------------------------------------------------------------

func TestHandleAdaptive_NilRuleStats(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{
				Name:   "nil-rs",
				Match:  RuleMatch{MetricName: "nilrs_.*"},
				Action: ActionAdaptive,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()
	// Do not add any ruleStats -> rs == nil

	m := createTestMetric("nilrs_metric", map[string]string{}, 1)
	result := e.handleAdaptive(&cfg.Rules[0], "group", m, nil, "datapoints_rate", 1)
	if result == nil {
		t.Fatal("handleAdaptive with nil rs should return the metric unchanged")
	}
}

// ---------------------------------------------------------------------------
// handleAdaptive: cardinality sort and current group not top offender
// ---------------------------------------------------------------------------

func TestHandleAdaptive_CurrentGroupNotDropped(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{
				Name:           "adapt-card",
				Match:          RuleMatch{MetricName: "adaptcard_.*"},
				MaxCardinality: 5,
				Action:         ActionAdaptive,
				GroupBy:        []string{"group"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Create multiple groups: offenderA has high cardinality, safeB has low cardinality
	trackerA := cardinality.NewTrackerFromGlobal()
	for i := 0; i < 20; i++ {
		trackerA.Add([]byte(fmt.Sprintf("series_%d", i)))
	}
	trackerB := cardinality.NewTrackerFromGlobal()
	trackerB.Add([]byte("series_0"))

	e.mu.Lock()
	e.ruleStats["adapt-card"] = &ruleStats{
		groups: map[string]*groupStats{
			"group=offenderA": {datapoints: 50, cardinality: trackerA, windowEnd: time.Now().Add(time.Minute)},
			"group=safeB":     {datapoints: 2, cardinality: trackerB, windowEnd: time.Now().Add(time.Minute)},
		},
		totalDPs:  52,
		totalCard: 21, // exceeds limit of 5
		windowEnd: time.Now().Add(time.Minute),
	}
	e.mu.Unlock()

	m := createTestMetric("adaptcard_metric", map[string]string{}, 1)
	result := e.handleAdaptive(&cfg.Rules[0], "group=safeB", m, nil, "cardinality", 1)
	if result == nil {
		t.Fatal("safeB (low cardinality) should not be dropped")
	}
	action, _, _ := getGovernorLabels(result)
	if action != "adaptive" {
		t.Errorf("expected adaptive label, got '%s'", action)
	}
}

// ---------------------------------------------------------------------------
// recordTrackerModeSwitch
// ---------------------------------------------------------------------------

func TestRecordTrackerModeSwitch(t *testing.T) {
	e := NewEnforcer(nil, false, 0)
	defer e.Stop()

	e.recordTrackerModeSwitch("test-rule", "test-group",
		cardinality.TrackerModeBloom, cardinality.TrackerModeHLL, 5000)

	if e.totalSwitches.Load() != 1 {
		t.Errorf("expected totalSwitches=1, got %d", e.totalSwitches.Load())
	}

	e.mu.RLock()
	info := e.trackerModes["test-rule"]["test-group"]
	e.mu.RUnlock()

	if info == nil {
		t.Fatal("expected mode info to be recorded")
	}
	if info.mode != cardinality.TrackerModeHLL {
		t.Errorf("expected HLL mode, got %d", info.mode)
	}
	if info.switchCount != 1 {
		t.Errorf("expected switchCount=1, got %d", info.switchCount)
	}
}

// ---------------------------------------------------------------------------
// recordGroupsDropped
// ---------------------------------------------------------------------------

func TestRecordGroupsDropped(t *testing.T) {
	e := NewEnforcer(nil, false, 0)
	defer e.Stop()

	e.recordGroupsDropped("test-rule", 5)

	e.violations.mu.RLock()
	counter := e.violations.groupsDropped["test-rule"]
	e.violations.mu.RUnlock()

	if counter == nil {
		t.Fatal("expected groupsDropped counter")
	}
	if counter.Load() != 5 {
		t.Errorf("expected 5, got %d", counter.Load())
	}

	// Call again to accumulate
	e.recordGroupsDropped("test-rule", 3)
	if counter.Load() != 8 {
		t.Errorf("expected 8, got %d", counter.Load())
	}
}

// ---------------------------------------------------------------------------
// handleHLLSampling
// ---------------------------------------------------------------------------

func TestHandleHLLSampling_NilGroupStats(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{Name: "hll-test", Match: RuleMatch{MetricName: "hll_.*"}, Action: ActionDrop},
		},
	}
	LoadConfigFromStruct(cfg)
	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	m := createTestMetric("hll_metric", map[string]string{}, 1)
	result := e.handleHLLSampling(&cfg.Rules[0], "nonexistent-group", m, nil, 0.5, 1)
	if result == nil {
		t.Fatal("nil gs should return metric unchanged")
	}
}

func TestHandleHLLSampling_NonHybridTracker(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{Name: "hll-non-hybrid", Match: RuleMatch{MetricName: "hll_.*"}, Action: ActionDrop},
		},
	}
	LoadConfigFromStruct(cfg)
	e := NewEnforcer(cfg, false, 0) // non-dry-run
	defer e.Stop()

	// Set up with a non-hybrid tracker (e.g., bloom)
	bloomTracker := cardinality.NewBloomTracker(cardinality.DefaultConfig())
	e.mu.Lock()
	e.ruleStats["hll-non-hybrid"] = &ruleStats{
		groups: map[string]*groupStats{
			"g1": {datapoints: 10, cardinality: bloomTracker, windowEnd: time.Now().Add(time.Minute)},
		},
		totalDPs:  10,
		windowEnd: time.Now().Add(time.Minute),
	}
	e.mu.Unlock()

	m := createTestMetric("hll_metric", map[string]string{}, 1)
	result := e.handleHLLSampling(&cfg.Rules[0], "g1", m, nil, 0.5, 1)
	// Non-hybrid tracker falls back to normal drop, non-dry-run -> nil
	if result != nil {
		t.Error("non-hybrid tracker in non-dry-run should drop (return nil)")
	}
}

func TestHandleHLLSampling_NonHybridTrackerDryRun(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{Name: "hll-fallback-dry", Match: RuleMatch{MetricName: "hll_.*"}, Action: ActionDrop},
		},
	}
	LoadConfigFromStruct(cfg)
	e := NewEnforcer(cfg, true, 0) // dry-run
	defer e.Stop()

	bloomTracker := cardinality.NewBloomTracker(cardinality.DefaultConfig())
	e.mu.Lock()
	e.ruleStats["hll-fallback-dry"] = &ruleStats{
		groups: map[string]*groupStats{
			"g1": {datapoints: 10, cardinality: bloomTracker, windowEnd: time.Now().Add(time.Minute)},
		},
		totalDPs:  10,
		windowEnd: time.Now().Add(time.Minute),
	}
	e.mu.Unlock()

	m := createTestMetric("hll_metric", map[string]string{}, 1)
	result := e.handleHLLSampling(&cfg.Rules[0], "g1", m, nil, 0.5, 1)
	if result == nil {
		t.Fatal("dry-run non-hybrid should return metric with 'drop' label")
	}
	action, _, _ := getGovernorLabels(result)
	if action != "drop" {
		t.Errorf("expected action='drop', got '%s'", action)
	}
}

func TestHandleHLLSampling_WithHybridTracker(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{Name: "hll-hybrid", Match: RuleMatch{MetricName: "hll_.*"}, Action: ActionDrop},
		},
	}
	LoadConfigFromStruct(cfg)

	// Test both dry-run and non-dry-run with a hybrid tracker
	hybridCfg := cardinality.Config{
		Mode:              cardinality.ModeHybrid,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
		HLLThreshold:      5,
	}
	ht := cardinality.NewHybridTracker(hybridCfg, 5)

	// Add enough items to force HLL mode
	for i := 0; i < 100; i++ {
		ht.Add([]byte(fmt.Sprintf("key_%d", i)))
	}

	for _, dryRun := range []bool{true, false} {
		t.Run(fmt.Sprintf("dryRun=%v", dryRun), func(t *testing.T) {
			e := NewEnforcer(cfg, dryRun, 0)
			defer e.Stop()

			e.mu.Lock()
			e.ruleStats["hll-hybrid"] = &ruleStats{
				groups: map[string]*groupStats{
					"g1": {datapoints: 100, cardinality: ht, windowEnd: time.Now().Add(time.Minute)},
				},
				totalDPs:  100,
				windowEnd: time.Now().Add(time.Minute),
			}
			e.mu.Unlock()

			m := createTestMetric("hll_metric", map[string]string{"id": "unique_test"}, 1)
			resourceAttrs := map[string]string{"host": "node1"}
			// With sample rate 1.0, everything should be sampled through
			result := e.handleHLLSampling(&cfg.Rules[0], "g1", m, resourceAttrs, 1.0, 1)
			// ShouldSample with rate=1.0 should keep everything
			if result == nil && dryRun {
				t.Error("dry-run with high sample rate should not drop")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// handleAdaptiveHLL
// ---------------------------------------------------------------------------

func TestHandleAdaptiveHLL_NilGroupStats(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{Name: "ahll-nil", Match: RuleMatch{MetricName: "ahll_.*"}, Action: ActionAdaptive},
		},
	}
	LoadConfigFromStruct(cfg)
	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	m := createTestMetric("ahll_metric", map[string]string{}, 1)
	result := e.handleAdaptiveHLL(&cfg.Rules[0], "nonexistent", m, nil, "cardinality", 0.5, 1)
	if result == nil {
		t.Fatal("nil gs should return metric unchanged")
	}
}

func TestHandleAdaptiveHLL_NonHybridFallback(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{Name: "ahll-fallback", Match: RuleMatch{MetricName: "ahll_.*"},
				MaxDatapointsRate: 100, Action: ActionAdaptive, GroupBy: []string{"service"}},
		},
	}
	LoadConfigFromStruct(cfg)
	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Non-hybrid tracker should fall back to standard adaptive
	bloomTracker := cardinality.NewBloomTracker(cardinality.DefaultConfig())
	e.mu.Lock()
	e.ruleStats["ahll-fallback"] = &ruleStats{
		groups: map[string]*groupStats{
			"service=api": {datapoints: 10, cardinality: bloomTracker, windowEnd: time.Now().Add(time.Minute)},
		},
		totalDPs:  10,
		totalCard: 5,
		windowEnd: time.Now().Add(time.Minute),
	}
	e.mu.Unlock()

	m := createTestMetric("ahll_metric", map[string]string{}, 1)
	// Should call handleAdaptive fallback; with excess <= 0 it returns "adaptive" label
	result := e.handleAdaptiveHLL(&cfg.Rules[0], "service=api", m, nil, "datapoints_rate", 0.5, 1)
	if result == nil {
		t.Fatal("non-hybrid fallback to adaptive should return non-nil")
	}
}

func TestHandleAdaptiveHLL_WithHybridTracker(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{Name: "ahll-hybrid", Match: RuleMatch{MetricName: "ahll_.*"},
				MaxCardinality: 10, Action: ActionAdaptive},
		},
	}
	LoadConfigFromStruct(cfg)

	hybridCfg := cardinality.Config{
		Mode:              cardinality.ModeHybrid,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
		HLLThreshold:      5,
	}
	ht := cardinality.NewHybridTracker(hybridCfg, 5)
	for i := 0; i < 100; i++ {
		ht.Add([]byte(fmt.Sprintf("series_%d", i)))
	}

	for _, dryRun := range []bool{true, false} {
		t.Run(fmt.Sprintf("dryRun=%v", dryRun), func(t *testing.T) {
			e := NewEnforcer(cfg, dryRun, 0)
			defer e.Stop()

			e.mu.Lock()
			e.ruleStats["ahll-hybrid"] = &ruleStats{
				groups: map[string]*groupStats{
					"g1": {datapoints: 100, cardinality: ht, windowEnd: time.Now().Add(time.Minute)},
				},
				totalDPs:  100,
				totalCard: 100,
				windowEnd: time.Now().Add(time.Minute),
			}
			e.mu.Unlock()

			m := createTestMetric("ahll_metric", map[string]string{"id": "test"}, 1)
			resourceAttrs := map[string]string{"host": "n1"}
			result := e.handleAdaptiveHLL(&cfg.Rules[0], "g1", m, resourceAttrs, "cardinality", 1.0, 1)
			// With sample rate 1.0, ShouldSample should pass
			if result == nil && dryRun {
				t.Error("dry-run should not drop with rate=1.0")
			}
		})
	}
}

func TestHandleAdaptiveHLL_DryRunDropLabel(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{Name: "ahll-dryrun-drop", Match: RuleMatch{MetricName: "ahll_.*"},
				MaxCardinality: 10, Action: ActionAdaptive},
		},
	}
	LoadConfigFromStruct(cfg)

	hybridCfg := cardinality.Config{
		Mode:              cardinality.ModeHybrid,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
		HLLThreshold:      5,
	}
	ht := cardinality.NewHybridTracker(hybridCfg, 5)
	for i := 0; i < 100; i++ {
		ht.Add([]byte(fmt.Sprintf("series_%d", i)))
	}

	e := NewEnforcer(cfg, true, 0) // dry-run
	defer e.Stop()

	e.mu.Lock()
	e.ruleStats["ahll-dryrun-drop"] = &ruleStats{
		groups: map[string]*groupStats{
			"g1": {datapoints: 100, cardinality: ht, windowEnd: time.Now().Add(time.Minute)},
		},
		totalDPs:  100,
		totalCard: 100,
		windowEnd: time.Now().Add(time.Minute),
	}
	e.mu.Unlock()

	m := createTestMetric("ahll_metric", map[string]string{"id": "should_drop"}, 1)
	// Rate 0.0 => nothing is sampled => drop path
	result := e.handleAdaptiveHLL(&cfg.Rules[0], "g1", m, map[string]string{}, "cardinality", 0.0, 1)
	if result == nil {
		t.Fatal("dry-run drop should return metric with label")
	}
	action, _, _ := getGovernorLabels(result)
	if action != "adaptive_drop" {
		t.Errorf("expected action='adaptive_drop', got '%s'", action)
	}
}

// ---------------------------------------------------------------------------
// getTrackerMode
// ---------------------------------------------------------------------------

func TestGetTrackerMode_NilRS(t *testing.T) {
	e := NewEnforcer(nil, false, 0)
	defer e.Stop()

	mode := e.getTrackerMode("nonexistent", "group")
	if mode != cardinality.TrackerModeBloom {
		t.Errorf("expected Bloom for nil rs, got %d", mode)
	}
}

func TestGetTrackerMode_NilGS(t *testing.T) {
	e := NewEnforcer(nil, false, 0)
	defer e.Stop()

	e.mu.Lock()
	e.ruleStats["exists"] = &ruleStats{
		groups: map[string]*groupStats{},
	}
	e.mu.Unlock()

	mode := e.getTrackerMode("exists", "nonexistent-group")
	if mode != cardinality.TrackerModeBloom {
		t.Errorf("expected Bloom for nil gs, got %d", mode)
	}
}

func TestGetTrackerMode_HybridTracker(t *testing.T) {
	e := NewEnforcer(nil, false, 0)
	defer e.Stop()

	hybridCfg := cardinality.Config{
		Mode:              cardinality.ModeHybrid,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
		HLLThreshold:      5,
	}
	ht := cardinality.NewHybridTracker(hybridCfg, 5)

	e.mu.Lock()
	e.ruleStats["rule"] = &ruleStats{
		groups: map[string]*groupStats{
			"g": {cardinality: ht},
		},
	}
	e.mu.Unlock()

	mode := e.getTrackerMode("rule", "g")
	if mode != cardinality.TrackerModeBloom {
		t.Errorf("expected Bloom initially, got %d", mode)
	}

	// Force HLL mode by adding many items
	for i := 0; i < 100; i++ {
		ht.Add([]byte(fmt.Sprintf("k%d", i)))
	}

	mode = e.getTrackerMode("rule", "g")
	if mode != cardinality.TrackerModeHLL {
		t.Errorf("expected HLL after many items, got %d", mode)
	}
}

// ---------------------------------------------------------------------------
// calculateSampleRate
// ---------------------------------------------------------------------------

func TestCalculateSampleRate_NilRS(t *testing.T) {
	e := NewEnforcer(nil, false, 0)
	defer e.Stop()

	rule := &Rule{Name: "no-rs", MaxCardinality: 100}
	rate := e.calculateSampleRate(rule, "group")
	if rate != 1.0 {
		t.Errorf("nil rs should return 1.0, got %f", rate)
	}
}

func TestCalculateSampleRate_NilGS(t *testing.T) {
	e := NewEnforcer(nil, false, 0)
	defer e.Stop()

	e.mu.Lock()
	e.ruleStats["rule"] = &ruleStats{groups: map[string]*groupStats{}}
	e.mu.Unlock()

	rule := &Rule{Name: "rule", MaxCardinality: 100}
	rate := e.calculateSampleRate(rule, "missing")
	if rate != 1.0 {
		t.Errorf("nil gs should return 1.0, got %f", rate)
	}
}

func TestCalculateSampleRate_ZeroCount(t *testing.T) {
	e := NewEnforcer(nil, false, 0)
	defer e.Stop()

	tracker := cardinality.NewBloomTracker(cardinality.DefaultConfig())

	e.mu.Lock()
	e.ruleStats["rule"] = &ruleStats{
		groups: map[string]*groupStats{
			"g": {cardinality: tracker},
		},
	}
	e.mu.Unlock()

	rule := &Rule{Name: "rule", MaxCardinality: 100}
	rate := e.calculateSampleRate(rule, "g")
	if rate != 1.0 {
		t.Errorf("zero count should return 1.0, got %f", rate)
	}
}

func TestCalculateSampleRate_ZeroLimit(t *testing.T) {
	e := NewEnforcer(nil, false, 0)
	defer e.Stop()

	tracker := cardinality.NewBloomTracker(cardinality.DefaultConfig())
	tracker.Add([]byte("item"))

	e.mu.Lock()
	e.ruleStats["rule"] = &ruleStats{
		groups: map[string]*groupStats{
			"g": {cardinality: tracker},
		},
	}
	e.mu.Unlock()

	rule := &Rule{Name: "rule", MaxCardinality: 0}
	rate := e.calculateSampleRate(rule, "g")
	if rate != 1.0 {
		t.Errorf("zero limit should return 1.0, got %f", rate)
	}
}

func TestCalculateSampleRate_UnderLimit(t *testing.T) {
	e := NewEnforcer(nil, false, 0)
	defer e.Stop()

	tracker := cardinality.NewExactTracker()
	tracker.Add([]byte("item"))

	e.mu.Lock()
	e.ruleStats["rule"] = &ruleStats{
		groups: map[string]*groupStats{
			"g": {cardinality: tracker},
		},
	}
	e.mu.Unlock()

	rule := &Rule{Name: "rule", MaxCardinality: 100}
	rate := e.calculateSampleRate(rule, "g")
	if rate != 1.0 {
		t.Errorf("under limit should return 1.0, got %f", rate)
	}
}

func TestCalculateSampleRate_OverLimit(t *testing.T) {
	e := NewEnforcer(nil, false, 0)
	defer e.Stop()

	tracker := cardinality.NewExactTracker()
	for i := 0; i < 200; i++ {
		tracker.Add([]byte(fmt.Sprintf("item_%d", i)))
	}

	e.mu.Lock()
	e.ruleStats["rule"] = &ruleStats{
		groups: map[string]*groupStats{
			"g": {cardinality: tracker},
		},
	}
	e.mu.Unlock()

	rule := &Rule{Name: "rule", MaxCardinality: 100}
	rate := e.calculateSampleRate(rule, "g")
	// limit/count = 100/200 = 0.5
	if rate < 0.49 || rate > 0.51 {
		t.Errorf("expected ~0.5, got %f", rate)
	}
}

// ---------------------------------------------------------------------------
// handleViolation: default action (unrecognized action passes through)
// ---------------------------------------------------------------------------

func TestHandleViolation_UnknownAction(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{Name: "unknown-action", Match: RuleMatch{MetricName: "unk_.*"},
				MaxCardinality: 1, Action: Action("nonexistent")},
		},
	}
	LoadConfigFromStruct(cfg)
	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Set up minimal state for handleViolation
	e.mu.Lock()
	e.ruleStats["unknown-action"] = &ruleStats{
		groups: map[string]*groupStats{
			"g": {cardinality: cardinality.NewBloomTracker(cardinality.DefaultConfig()), datapoints: 1},
		},
		totalDPs:  1,
		windowEnd: time.Now().Add(time.Minute),
	}
	e.mu.Unlock()

	m := createTestMetric("unk_metric", map[string]string{}, 1)
	result := e.handleViolation(&cfg.Rules[0], "g", m, nil, "cardinality")
	if result == nil {
		t.Fatal("unknown action should pass through (default case)")
	}
}

// ---------------------------------------------------------------------------
// ServeHTTP: dropped groups info lines, null ruleMatchCache
// ---------------------------------------------------------------------------

func TestServeHTTP_WithDroppedGroupsInfo(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{Name: "info-test", Match: RuleMatch{MetricName: "info_.*"}, MaxCardinality: 100, Action: ActionAdaptive},
		},
	}
	LoadConfigFromStruct(cfg)
	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Manually add dropped groups
	e.mu.Lock()
	e.droppedGroups["info-test"] = map[string]time.Time{
		"group=alpha": time.Now().Add(time.Hour),
		"group=beta":  time.Now().Add(time.Hour),
	}
	e.mu.Unlock()

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()

	if !strings.Contains(body, `metrics_governor_dropped_group_info{rule="info-test",group="group=alpha"} 1`) {
		t.Error("expected dropped_group_info for group=alpha")
	}
	if !strings.Contains(body, `metrics_governor_dropped_group_info{rule="info-test",group="group=beta"} 1`) {
		t.Error("expected dropped_group_info for group=beta")
	}
}

func TestServeHTTP_NilConfig(t *testing.T) {
	// When config is nil, the handler getAction returns "unknown" and rule metrics are skipped
	e := NewEnforcer(nil, false, 0)
	defer e.Stop()

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()

	// Should not contain rule config metrics
	if strings.Contains(body, "metrics_governor_rule_max_datapoints_rate") {
		t.Error("nil config should not emit rule config metrics")
	}
}

func TestServeHTTP_NilRuleMatchCache(t *testing.T) {
	// ruleCacheMaxSize=0 means no cache
	e := NewEnforcer(nil, false, 0)
	defer e.Stop()

	if e.ruleMatchCache != nil {
		t.Fatal("expected nil ruleMatchCache when size=0")
	}

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()

	// Cache metrics should not appear
	if strings.Contains(body, "metrics_governor_rule_cache_hits_total") {
		t.Error("nil cache should not emit cache metrics")
	}
}

func TestServeHTTP_TrackerModeMetrics(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{Name: "mode-test", Match: RuleMatch{MetricName: "mode_.*"}, MaxCardinality: 100, Action: ActionDrop},
		},
	}
	LoadConfigFromStruct(cfg)
	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	tracker := cardinality.NewExactTracker()
	tracker.Add([]byte("s1"))

	e.mu.Lock()
	e.ruleStats["mode-test"] = &ruleStats{
		groups: map[string]*groupStats{
			"g1": {datapoints: 10, cardinality: tracker, windowEnd: time.Now().Add(time.Minute)},
		},
		totalDPs:  10,
		totalCard: 1,
		windowEnd: time.Now().Add(time.Minute),
	}
	e.trackerModes["mode-test"] = map[string]*trackerModeInfo{
		"g1": {mode: cardinality.TrackerModeHLL, sampleRate: 0.75, switchCount: 1},
	}
	e.mu.Unlock()

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()

	if !strings.Contains(body, `metrics_governor_tracker_mode{rule="mode-test",group="g1"} 1`) {
		t.Error("expected tracker_mode metric for g1 (HLL=1)")
	}
	if !strings.Contains(body, `metrics_governor_tracker_sample_rate{rule="mode-test",group="g1"}`) {
		t.Error("expected tracker_sample_rate metric for g1")
	}
}

func TestServeHTTP_TrackerModeWithThreshold(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{Name: "thr-test", Match: RuleMatch{MetricName: "thr_.*"}, MaxCardinality: 100, Action: ActionDrop},
		},
	}
	LoadConfigFromStruct(cfg)
	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()
	e.SetStatsThreshold(1000) // high threshold

	tracker := cardinality.NewExactTracker()
	tracker.Add([]byte("s1"))

	e.mu.Lock()
	e.ruleStats["thr-test"] = &ruleStats{
		groups: map[string]*groupStats{
			"g1": {datapoints: 5, cardinality: tracker, windowEnd: time.Now().Add(time.Minute)},
		},
		totalDPs:  5,
		totalCard: 1,
		windowEnd: time.Now().Add(time.Minute),
	}
	e.trackerModes["thr-test"] = map[string]*trackerModeInfo{
		"g1": {mode: cardinality.TrackerModeHLL, sampleRate: 0.5, switchCount: 1},
	}
	e.mu.Unlock()

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()

	// With threshold=1000 and datapoints=5, per-group tracker metrics should be filtered
	if strings.Contains(body, `metrics_governor_tracker_mode{rule="thr-test",group="g1"}`) {
		t.Error("tracker_mode should be filtered by threshold")
	}
	if strings.Contains(body, `metrics_governor_tracker_sample_rate{rule="thr-test",group="g1"}`) {
		t.Error("tracker_sample_rate should be filtered by threshold")
	}
}

// ---------------------------------------------------------------------------
// LogAggregator: entry cap (maxLogAggregatorEntries)
// ---------------------------------------------------------------------------

func TestLogAggregator_EntryCapDrops(t *testing.T) {
	la := NewLogAggregator(10 * time.Second) // long interval so no auto-flush
	defer la.Stop()

	// Fill up to the cap
	for i := 0; i < maxLogAggregatorEntries; i++ {
		la.Info(fmt.Sprintf("key_%d", i), "msg", nil, 1)
	}

	// Verify size
	la.mu.Lock()
	size := len(la.entries)
	la.mu.Unlock()
	if size != maxLogAggregatorEntries {
		t.Fatalf("expected %d entries, got %d", maxLogAggregatorEntries, size)
	}

	// Next entry should be dropped
	la.Info("overflow_key", "msg", nil, 1)

	la.mu.Lock()
	dropped := la.dropped
	_, exists := la.entries["overflow_key"]
	la.mu.Unlock()

	if dropped != 1 {
		t.Errorf("expected 1 dropped entry, got %d", dropped)
	}
	if exists {
		t.Error("overflow_key should not be in entries")
	}
}

// ---------------------------------------------------------------------------
// LogAggregator: flush with all levels and dropped report
// ---------------------------------------------------------------------------

func TestLogAggregator_FlushAllLevels(t *testing.T) {
	la := NewLogAggregator(50 * time.Millisecond)
	defer la.Stop()

	la.Warn("w1", "warn msg", map[string]interface{}{"a": 1}, 10)
	la.Warn("w1", "warn msg", map[string]interface{}{"a": 1}, 20) // increment count
	la.Info("i1", "info msg", map[string]interface{}{"b": 2}, 5)
	la.Error("e1", "error msg", map[string]interface{}{"c": 3}, 15)

	// Wait for flush
	time.Sleep(100 * time.Millisecond)

	// Entries should be cleared after flush
	la.mu.Lock()
	size := len(la.entries)
	la.mu.Unlock()

	if size != 0 {
		t.Errorf("expected 0 entries after flush, got %d", size)
	}
}

// ---------------------------------------------------------------------------
// ruleCache: EstimatedMemoryBytes with entries
// ---------------------------------------------------------------------------

func TestRuleCache_EstimatedMemoryBytes(t *testing.T) {
	cache := newRuleCache(100)
	if cache.EstimatedMemoryBytes() != 0 {
		t.Error("empty cache should have 0 bytes")
	}

	cache.Put("short", nil)
	cache.Put("longer_metric_name", &Rule{Name: "test"})

	est := cache.EstimatedMemoryBytes()
	// Each entry has 192 overhead + key length
	expected := int64(192+5) + int64(192+18)
	if est != expected {
		t.Errorf("expected %d bytes, got %d", expected, est)
	}
}

// ---------------------------------------------------------------------------
// updateAndCheckLimits: maxGroupsPerRule cap
// ---------------------------------------------------------------------------

func TestUpdateAndCheckLimits_MaxGroupsCap(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{
				Name:           "cap-test",
				Match:          RuleMatch{MetricName: "cap_.*"},
				MaxCardinality: 1000000,
				Action:         ActionDrop,
				GroupBy:        []string{"id"},
			},
		},
	}
	LoadConfigFromStruct(cfg)
	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Process metrics to create many groups. The cap is 100,000 groups per rule.
	// We can't create 100K groups in a unit test easily, so test the boundary
	// by pre-populating.
	e.mu.Lock()
	rs := &ruleStats{
		groups:    make(map[string]*groupStats),
		totalDPs:  0,
		totalCard: 0,
		windowEnd: time.Now().Add(time.Minute),
	}
	for i := 0; i < 100000; i++ {
		key := fmt.Sprintf("id=%d", i)
		rs.groups[key] = &groupStats{
			cardinality: cardinality.NewBloomTracker(cardinality.DefaultConfig()),
			windowEnd:   rs.windowEnd,
		}
	}
	e.ruleStats["cap-test"] = rs
	e.mu.Unlock()

	// Now try to add one more group - should return false (not exceeded)
	// because we hit the cap and bail out
	m := createTestMetric("cap_metric", map[string]string{}, 1)
	resourceAttrs := map[string]string{"id": "new_group"}
	exceeded, _ := e.updateAndCheckLimits(&cfg.Rules[0], "id=new_group", m, resourceAttrs)
	if exceeded {
		t.Error("at max groups cap, should return false (not exceeded)")
	}
}

// ---------------------------------------------------------------------------
// handleViolation with HLL tracker mode in Drop action
// ---------------------------------------------------------------------------

func TestHandleViolation_DropAction_HLLMode(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{Name: "hll-drop-v", Match: RuleMatch{MetricName: "hllv_.*"},
				MaxCardinality: 1, Action: ActionDrop},
		},
	}
	LoadConfigFromStruct(cfg)

	hybridCfg := cardinality.Config{
		Mode:              cardinality.ModeHybrid,
		ExpectedItems:     100,
		FalsePositiveRate: 0.01,
		HLLThreshold:      5,
	}
	ht := cardinality.NewHybridTracker(hybridCfg, 5)
	for i := 0; i < 50; i++ {
		ht.Add([]byte(fmt.Sprintf("s%d", i)))
	}

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	e.mu.Lock()
	e.ruleStats["hll-drop-v"] = &ruleStats{
		groups: map[string]*groupStats{
			"g1": {datapoints: 50, cardinality: ht, windowEnd: time.Now().Add(time.Minute)},
		},
		totalDPs:  50,
		totalCard: 50,
		windowEnd: time.Now().Add(time.Minute),
	}
	e.mu.Unlock()

	m := createTestMetric("hllv_metric", map[string]string{"id": "test"}, 1)
	// This triggers handleHLLSampling via handleViolation->ActionDrop path
	result := e.handleViolation(&cfg.Rules[0], "g1", m, map[string]string{}, "cardinality")
	// Result depends on ShouldSample; we mainly want to ensure no panic
	_ = result
}

func TestHandleViolation_AdaptiveAction_HLLMode(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{Name: "hll-adapt-v", Match: RuleMatch{MetricName: "hllav_.*"},
				MaxCardinality: 1, Action: ActionAdaptive},
		},
	}
	LoadConfigFromStruct(cfg)

	hybridCfg := cardinality.Config{
		Mode:              cardinality.ModeHybrid,
		ExpectedItems:     100,
		FalsePositiveRate: 0.01,
		HLLThreshold:      5,
	}
	ht := cardinality.NewHybridTracker(hybridCfg, 5)
	for i := 0; i < 50; i++ {
		ht.Add([]byte(fmt.Sprintf("s%d", i)))
	}

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	e.mu.Lock()
	e.ruleStats["hll-adapt-v"] = &ruleStats{
		groups: map[string]*groupStats{
			"g1": {datapoints: 50, cardinality: ht, windowEnd: time.Now().Add(time.Minute)},
		},
		totalDPs:  50,
		totalCard: 50,
		windowEnd: time.Now().Add(time.Minute),
	}
	e.mu.Unlock()

	m := createTestMetric("hllav_metric", map[string]string{"id": "test"}, 1)
	result := e.handleViolation(&cfg.Rules[0], "g1", m, map[string]string{}, "cardinality")
	_ = result // Mainly testing no panic
}

// ---------------------------------------------------------------------------
// processResourceMetrics: empty ScopeMetrics
// ---------------------------------------------------------------------------

func TestProcessResourceMetrics_EmptyScopeMetrics(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{Name: "empty-sm", Match: RuleMatch{MetricName: ".*"}, MaxCardinality: 100, Action: ActionDrop},
		},
	}
	LoadConfigFromStruct(cfg)
	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	rm := &metricspb.ResourceMetrics{
		Resource:     &resourcepb.Resource{},
		ScopeMetrics: []*metricspb.ScopeMetrics{},
	}

	result := e.processResourceMetrics(rm, nil)
	if result != nil {
		t.Error("empty scope metrics should return nil")
	}
}

func TestProcessResourceMetrics_AllMetricsDropped(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{Name: "drop-all", Match: RuleMatch{MetricName: "test_.*"}, MaxCardinality: 1, Action: ActionDrop},
		},
	}
	LoadConfigFromStruct(cfg)
	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// First metric passes
	rm1 := createTestResourceMetrics(
		map[string]string{},
		[]*metricspb.Metric{createTestMetric("test_m", map[string]string{"id": "1"}, 1)},
	)
	e.Process([]*metricspb.ResourceMetrics{rm1})

	// Second metric should be dropped; entire RM returns nil
	rm2 := createTestResourceMetrics(
		map[string]string{},
		[]*metricspb.Metric{createTestMetric("test_m", map[string]string{"id": "2"}, 1)},
	)
	result := e.processResourceMetrics(rm2, map[string]string{})
	if result != nil {
		t.Error("when all metrics are dropped, processResourceMetrics should return nil")
	}
}

// ---------------------------------------------------------------------------
// handleHLLSampling: no datapoint attributes path
// ---------------------------------------------------------------------------

func TestHandleHLLSampling_NoDPAttrs(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{Name: "no-dp-attrs", Match: RuleMatch{MetricName: "nodp_.*"}, Action: ActionDrop},
		},
	}
	LoadConfigFromStruct(cfg)

	hybridCfg := cardinality.Config{
		Mode:              cardinality.ModeHybrid,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
		HLLThreshold:      5,
	}
	ht := cardinality.NewHybridTracker(hybridCfg, 5)
	for i := 0; i < 100; i++ {
		ht.Add([]byte(fmt.Sprintf("k%d", i)))
	}

	e := NewEnforcer(cfg, false, 0) // non-dry-run
	defer e.Stop()

	e.mu.Lock()
	e.ruleStats["no-dp-attrs"] = &ruleStats{
		groups: map[string]*groupStats{
			"g1": {datapoints: 100, cardinality: ht, windowEnd: time.Now().Add(time.Minute)},
		},
		totalDPs:  100,
		windowEnd: time.Now().Add(time.Minute),
	}
	e.mu.Unlock()

	// Metric with no data (nil) -> extractDatapointAttributes returns empty
	m := &metricspb.Metric{Name: "nodp_metric", Data: nil}
	result := e.handleHLLSampling(&cfg.Rules[0], "g1", m, nil, 0.5, 0)
	// No dpAttrs -> falls to the drop path at end
	if result != nil {
		t.Error("no dp attrs in non-dry-run should drop")
	}
}

func TestHandleHLLSampling_NoDPAttrsDryRun(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{Name: "no-dp-attrs-dry", Match: RuleMatch{MetricName: "nodp_.*"}, Action: ActionDrop},
		},
	}
	LoadConfigFromStruct(cfg)

	hybridCfg := cardinality.Config{
		Mode:              cardinality.ModeHybrid,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
		HLLThreshold:      5,
	}
	ht := cardinality.NewHybridTracker(hybridCfg, 5)
	for i := 0; i < 100; i++ {
		ht.Add([]byte(fmt.Sprintf("k%d", i)))
	}

	e := NewEnforcer(cfg, true, 0) // dry-run
	defer e.Stop()

	e.mu.Lock()
	e.ruleStats["no-dp-attrs-dry"] = &ruleStats{
		groups: map[string]*groupStats{
			"g1": {datapoints: 100, cardinality: ht, windowEnd: time.Now().Add(time.Minute)},
		},
		totalDPs:  100,
		windowEnd: time.Now().Add(time.Minute),
	}
	e.mu.Unlock()

	// Use a gauge metric with 1 empty datapoint instead of nil Data,
	// so that labels can still be injected by injectDatapointLabels.
	m := &metricspb.Metric{
		Name: "nodp_metric",
		Data: &metricspb.Metric_Gauge{
			Gauge: &metricspb.Gauge{
				DataPoints: []*metricspb.NumberDataPoint{{}},
			},
		},
	}
	result := e.handleHLLSampling(&cfg.Rules[0], "g1", m, nil, 0.5, 1)
	if result == nil {
		t.Fatal("dry-run drop should return metric with label")
	}
	action, _, _ := getGovernorLabels(result)
	if action != "sampled_drop" {
		t.Errorf("expected sampled_drop, got '%s'", action)
	}
}
