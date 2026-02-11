package sampling

import (
	"math"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
)

// getGaugeScalarValue reads the current value of a plain Gauge (non-Vec).
func getGaugeScalarValue(g prometheus.Gauge) float64 {
	m := &dto.Metric{}
	g.(prometheus.Metric).Write(m)
	return m.Gauge.GetValue()
}

// TestDeadRules_NeverMatched verifies that a rule that was never matched
// reports never_matched=1 and last_match_seconds=+Inf.
func TestDeadRules_NeverMatched(t *testing.T) {
	t.Parallel()

	rules := []ProcessingRule{
		{
			Name:   "dr-never-1",
			Input:  "dr_never1_.*",
			Action: ActionDrop,
		},
	}
	// Validate/compile to populate metrics fields.
	cfg := ProcessingConfig{Rules: rules}
	if err := validateProcessingConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	rules = cfg.Rules

	// Set loadedTime to a past timestamp so the rule appears to have been loaded a while ago.
	rules[0].metrics.dead.LoadedTime = time.Now().Add(-5 * time.Minute).UnixNano()

	// Do NOT call lastMatchTime.Store — simulate never matched.
	updateDeadRuleMetrics(rules)

	neverMatched := getGaugeValue(processingRuleNeverMatched, "dr-never-1", "drop")
	if neverMatched != 1 {
		t.Errorf("never_matched = %v, want 1", neverMatched)
	}

	lastMatch := getGaugeValue(processingRuleLastMatchSeconds, "dr-never-1", "drop")
	if !math.IsInf(lastMatch, 1) {
		t.Errorf("last_match_seconds = %v, want +Inf", lastMatch)
	}

	loaded := getGaugeValue(processingRuleLoadedSeconds, "dr-never-1", "drop")
	if loaded < 299 { // Should be ~300s (5 minutes), allow small tolerance.
		t.Errorf("loaded_seconds = %v, want >= 299", loaded)
	}
}

// TestDeadRules_RecentMatch verifies that a rule with a recent match
// reports never_matched=0 and a small last_match_seconds value.
func TestDeadRules_RecentMatch(t *testing.T) {
	t.Parallel()

	rules := []ProcessingRule{
		{
			Name:   "dr-recent-1",
			Input:  "dr_recent1_.*",
			Action: ActionDrop,
		},
	}
	cfg := ProcessingConfig{Rules: rules}
	if err := validateProcessingConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	rules = cfg.Rules

	// Simulate a recent match.
	rules[0].metrics.dead.LastMatchTime.Store(time.Now().UnixNano())

	updateDeadRuleMetrics(rules)

	neverMatched := getGaugeValue(processingRuleNeverMatched, "dr-recent-1", "drop")
	if neverMatched != 0 {
		t.Errorf("never_matched = %v, want 0", neverMatched)
	}

	lastMatch := getGaugeValue(processingRuleLastMatchSeconds, "dr-recent-1", "drop")
	if lastMatch < 0 || lastMatch > 2 {
		t.Errorf("last_match_seconds = %v, want between 0 and 2", lastMatch)
	}
}

// TestDeadRules_ScannerTransitions verifies that the dead rule scanner detects
// dead->alive->dead transitions by manipulating lastMatchTime atomics.
func TestDeadRules_ScannerTransitions(t *testing.T) {
	t.Parallel()

	interval := 200 * time.Millisecond // Scanner ticks at interval/2 = 100ms.

	rules := []ProcessingRule{
		{
			Name:   "dr-scan-1",
			Input:  "dr_scan1_.*",
			Action: ActionDrop,
		},
	}
	cfg := ProcessingConfig{Rules: rules}
	if err := validateProcessingConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	rules = cfg.Rules

	// Set loaded time far in the past so the rule is immediately dead.
	rules[0].metrics.dead.LoadedTime = time.Now().Add(-10 * time.Second).UnixNano()

	scanner := newDeadRuleScanner(interval, rules)
	defer scanner.stop()

	// Wait for a few scan ticks so the scanner marks the rule as dead.
	time.Sleep(3 * interval)

	deadVal := getGaugeValue(processingRuleDead, "dr-scan-1", "drop")
	if deadVal != 1 {
		t.Errorf("after initial period: dead = %v, want 1", deadVal)
	}
	if !rules[0].metrics.dead.WasDead.Load() {
		t.Error("after initial period: wasDead should be true")
	}

	// Continuously update lastMatchTime in a goroutine so the rule stays alive
	// across multiple scanner ticks.
	aliveDone := make(chan struct{})
	go func() {
		defer close(aliveDone)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		deadline := time.After(2 * interval)
		for {
			select {
			case <-ticker.C:
				rules[0].metrics.dead.LastMatchTime.Store(time.Now().UnixNano())
			case <-deadline:
				return
			}
		}
	}()

	// Wait for scanner to detect the alive transition while we keep refreshing.
	time.Sleep(interval)

	deadVal = getGaugeValue(processingRuleDead, "dr-scan-1", "drop")
	if deadVal != 0 {
		t.Errorf("after match: dead = %v, want 0", deadVal)
	}
	if rules[0].metrics.dead.WasDead.Load() {
		t.Error("after match: wasDead should be false")
	}

	// Wait for the goroutine to finish, then wait for the rule to go dead again.
	<-aliveDone
	time.Sleep(3 * interval)

	deadVal = getGaugeValue(processingRuleDead, "dr-scan-1", "drop")
	if deadVal != 1 {
		t.Errorf("after second dead period: dead = %v, want 1", deadVal)
	}
	if !rules[0].metrics.dead.WasDead.Load() {
		t.Error("after second dead period: wasDead should be true")
	}
}

// TestDeadRules_ScannerDisabled verifies that when parsedDeadRuleIntvl is 0,
// no scanner is started (deadScanner is nil on the Sampler).
func TestDeadRules_ScannerDisabled(t *testing.T) {
	t.Parallel()

	cfg := ProcessingConfig{
		// DeadRuleInterval left empty -> parsedDeadRuleIntvl = 0.
		Rules: []ProcessingRule{
			{Name: "dr-disabled-1", Input: "dr_disabled1_.*", Action: ActionDrop},
		},
	}

	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.StopDeadRuleScanner()

	if s.deadScanner != nil {
		t.Error("expected deadScanner to be nil when dead_rule_interval is not set")
	}
}

// TestDeadRules_Integration_ProcessUpdatesTimestamp creates a Sampler via
// NewFromProcessing, calls Process with matching data, and verifies that
// lastMatchTime was updated on the matching rule.
func TestDeadRules_Integration_ProcessUpdatesTimestamp(t *testing.T) {
	t.Parallel()

	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "dr-integ-1", Input: "dr_integ1_.*", Action: ActionDrop},
		},
	}

	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.StopDeadRuleScanner()

	// Before processing, lastMatchTime should be 0 (never matched).
	s.mu.RLock()
	lastBefore := s.procRules[0].metrics.dead.LastMatchTime.Load()
	s.mu.RUnlock()

	if lastBefore != 0 {
		t.Fatalf("expected lastMatchTime=0 before processing, got %d", lastBefore)
	}

	// Process a matching metric.
	dp := makeNumberDP(map[string]string{}, 1000, 42)
	rms := []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "dr_integ1_metric",
							Data: &metricspb.Metric_Gauge{
								Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{dp}},
							},
						},
					},
				},
			},
		},
	}
	s.Process(rms)

	// After processing, lastMatchTime should be nonzero.
	s.mu.RLock()
	lastAfter := s.procRules[0].metrics.dead.LastMatchTime.Load()
	s.mu.RUnlock()

	if lastAfter == 0 {
		t.Error("expected lastMatchTime to be updated after processing a matching metric")
	}

	now := time.Now().UnixNano()
	ageNanos := now - lastAfter
	if ageNanos < 0 || ageNanos > int64(2*time.Second) {
		t.Errorf("lastMatchTime age = %v ns, expected recent (< 2s)", ageNanos)
	}
}
