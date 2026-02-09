package limits

import (
	"math"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// limitsGetGaugeValue reads the current value of a GaugeVec for the given labels.
func limitsGetGaugeValue(gv *prometheus.GaugeVec, labels ...string) float64 {
	m := &dto.Metric{}
	gv.WithLabelValues(labels...).Write(m)
	return m.Gauge.GetValue()
}

// makeLimitsRM builds a minimal ResourceMetrics with a single Sum metric.
func makeLimitsRM(metricName string, dpAttrs map[string]string, dpCount int) []*metricspb.ResourceMetrics {
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

	return []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{},
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

// TestLimitsDeadRules_ActivityTracking creates an Enforcer, calls Process with
// metrics that match a rule, and verifies that the rule's lastMatchTime was
// updated in the ruleActivityMap.
func TestLimitsDeadRules_ActivityTracking(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "ldr-track-1",
				Match:             RuleMatch{MetricName: "ldr_track1_.*"},
				MaxDatapointsRate: 100000,
				Action:            ActionLog,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Before processing, lastMatchTime should be 0.
	act := e.ruleActivityMap["ldr-track-1"]
	if act == nil {
		t.Fatal("expected ruleActivityMap entry for ldr-track-1")
	}
	if act.lastMatchTime.Load() != 0 {
		t.Fatalf("expected lastMatchTime=0 before processing, got %d", act.lastMatchTime.Load())
	}

	// Process a matching metric.
	rms := makeLimitsRM("ldr_track1_metric", map[string]string{}, 1)
	e.Process(rms)

	// After processing, lastMatchTime should be non-zero.
	lastMatch := act.lastMatchTime.Load()
	if lastMatch == 0 {
		t.Error("expected lastMatchTime to be updated after processing matching metric")
	}

	now := time.Now().UnixNano()
	ageNanos := now - lastMatch
	if ageNanos < 0 || ageNanos > int64(2*time.Second) {
		t.Errorf("lastMatchTime age = %v ns, expected recent (< 2s)", ageNanos)
	}
}

// TestLimitsDeadRules_NeverMatched creates an enforcer with rules that don't
// match any input, and verifies that updateLimitsDeadRuleMetrics shows
// never_matched=1 and last_match_seconds=+Inf.
func TestLimitsDeadRules_NeverMatched(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "ldr-nomatch-1",
				Match:             RuleMatch{MetricName: "ldr_nomatch1_.*"},
				MaxDatapointsRate: 100000,
				Action:            ActionLog,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Set loadedTime to the past so loaded_seconds is meaningful.
	e.ruleActivityMap["ldr-nomatch-1"].loadedTime = time.Now().Add(-3 * time.Minute).UnixNano()

	// Process a non-matching metric — rule should remain unmatched.
	rms := makeLimitsRM("completely_different_metric", map[string]string{}, 1)
	e.Process(rms)

	neverMatched := limitsGetGaugeValue(limitsRuleNeverMatched, "ldr-nomatch-1")
	if neverMatched != 1 {
		t.Errorf("never_matched = %v, want 1", neverMatched)
	}

	lastMatch := limitsGetGaugeValue(limitsRuleLastMatchSeconds, "ldr-nomatch-1")
	if !math.IsInf(lastMatch, 1) {
		t.Errorf("last_match_seconds = %v, want +Inf", lastMatch)
	}

	loaded := limitsGetGaugeValue(limitsRuleLoadedSeconds, "ldr-nomatch-1")
	if loaded < 179 { // Should be ~180s (3 minutes), allow small tolerance.
		t.Errorf("loaded_seconds = %v, want >= 179", loaded)
	}
}

// TestLimitsDeadRules_MetricGauges verifies that all three dead rule gauge
// values (last_match_seconds, never_matched, loaded_seconds) are set correctly
// for both matched and unmatched rules.
func TestLimitsDeadRules_MetricGauges(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "ldr-gauge-matched-1",
				Match:             RuleMatch{MetricName: "ldr_gm1_.*"},
				MaxDatapointsRate: 100000,
				Action:            ActionLog,
			},
			{
				Name:              "ldr-gauge-unmatched-1",
				Match:             RuleMatch{MetricName: "ldr_gu1_.*"},
				MaxDatapointsRate: 100000,
				Action:            ActionLog,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Process a metric that only matches the first rule.
	rms := makeLimitsRM("ldr_gm1_requests", map[string]string{}, 1)
	e.Process(rms)

	// Check the matched rule gauges.
	neverMatchedMatched := limitsGetGaugeValue(limitsRuleNeverMatched, "ldr-gauge-matched-1")
	if neverMatchedMatched != 0 {
		t.Errorf("matched rule never_matched = %v, want 0", neverMatchedMatched)
	}

	lastMatchMatched := limitsGetGaugeValue(limitsRuleLastMatchSeconds, "ldr-gauge-matched-1")
	if lastMatchMatched < 0 || lastMatchMatched > 2 {
		t.Errorf("matched rule last_match_seconds = %v, want between 0 and 2", lastMatchMatched)
	}

	loadedMatched := limitsGetGaugeValue(limitsRuleLoadedSeconds, "ldr-gauge-matched-1")
	if loadedMatched < 0 {
		t.Errorf("matched rule loaded_seconds = %v, want >= 0", loadedMatched)
	}

	// Check the unmatched rule gauges.
	neverMatchedUnmatched := limitsGetGaugeValue(limitsRuleNeverMatched, "ldr-gauge-unmatched-1")
	if neverMatchedUnmatched != 1 {
		t.Errorf("unmatched rule never_matched = %v, want 1", neverMatchedUnmatched)
	}

	lastMatchUnmatched := limitsGetGaugeValue(limitsRuleLastMatchSeconds, "ldr-gauge-unmatched-1")
	if !math.IsInf(lastMatchUnmatched, 1) {
		t.Errorf("unmatched rule last_match_seconds = %v, want +Inf", lastMatchUnmatched)
	}
}

// TestLimitsDeadRules_ReloadPreservesActivity calls ReloadConfig and verifies
// that activity for existing rules is preserved (lastMatchTime, loadedTime,
// wasDead are carried over).
func TestLimitsDeadRules_ReloadPreservesActivity(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "ldr-reload-1",
				Match:             RuleMatch{MetricName: "ldr_reload1_.*"},
				MaxDatapointsRate: 100000,
				Action:            ActionLog,
			},
			{
				Name:              "ldr-reload-remove-1",
				Match:             RuleMatch{MetricName: "ldr_reload_remove1_.*"},
				MaxDatapointsRate: 100000,
				Action:            ActionLog,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Process a metric matching the first rule to set its lastMatchTime.
	rms := makeLimitsRM("ldr_reload1_metric", map[string]string{}, 1)
	e.Process(rms)

	act := e.ruleActivityMap["ldr-reload-1"]
	if act == nil {
		t.Fatal("expected ruleActivityMap entry for ldr-reload-1")
	}
	lastMatchBefore := act.lastMatchTime.Load()
	loadedTimeBefore := act.loadedTime
	if lastMatchBefore == 0 {
		t.Fatal("expected lastMatchTime to be non-zero after processing")
	}

	// Mark the rule as wasDead to verify it's preserved.
	act.wasDead.Store(true)

	// Reload with the first rule still present and a new rule replacing the second.
	newCfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "ldr-reload-1",
				Match:             RuleMatch{MetricName: "ldr_reload1_.*"},
				MaxDatapointsRate: 200000, // Changed limit.
				Action:            ActionLog,
			},
			{
				Name:              "ldr-reload-new-1",
				Match:             RuleMatch{MetricName: "ldr_reload_new1_.*"},
				MaxDatapointsRate: 100000,
				Action:            ActionLog,
			},
		},
	}
	LoadConfigFromStruct(newCfg)
	e.ReloadConfig(newCfg)

	// Verify the preserved rule retains its activity.
	actAfter := e.ruleActivityMap["ldr-reload-1"]
	if actAfter == nil {
		t.Fatal("expected ruleActivityMap entry for ldr-reload-1 after reload")
	}
	if actAfter.lastMatchTime.Load() != lastMatchBefore {
		t.Errorf("lastMatchTime changed after reload: got %d, want %d", actAfter.lastMatchTime.Load(), lastMatchBefore)
	}
	if actAfter.loadedTime != loadedTimeBefore {
		t.Errorf("loadedTime changed after reload: got %d, want %d", actAfter.loadedTime, loadedTimeBefore)
	}
	if !actAfter.wasDead.Load() {
		t.Error("wasDead was not preserved after reload")
	}

	// Verify the removed rule is gone.
	if _, exists := e.ruleActivityMap["ldr-reload-remove-1"]; exists {
		t.Error("expected ldr-reload-remove-1 to be removed from ruleActivityMap after reload")
	}

	// Verify the new rule has fresh activity.
	actNew := e.ruleActivityMap["ldr-reload-new-1"]
	if actNew == nil {
		t.Fatal("expected ruleActivityMap entry for ldr-reload-new-1 after reload")
	}
	if actNew.lastMatchTime.Load() != 0 {
		t.Errorf("new rule lastMatchTime = %d, want 0", actNew.lastMatchTime.Load())
	}
	if actNew.wasDead.Load() {
		t.Error("new rule wasDead should be false")
	}
}
