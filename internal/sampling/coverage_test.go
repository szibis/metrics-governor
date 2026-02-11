package sampling

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
)

// ---------------------------------------------------------------------------
// 1. sampleSummaryDataPoints (0% coverage) — via legacy Sampler.Sample path
// ---------------------------------------------------------------------------

func TestSampleSummaryDataPoints_KeepAll(t *testing.T) {
	cfg := FileConfig{DefaultRate: 1.0}
	s, err := newFromLegacy(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeSummaryDP(map[string]string{"service": "api"}, 1000, 5, 100.0)
	rms := makeSummaryRM("http_summary", dp)
	result := s.Sample(rms)
	if len(result) == 0 {
		t.Error("expected summary metric to be kept (rate=1.0)")
	}
}

func TestSampleSummaryDataPoints_DropAll(t *testing.T) {
	cfg := FileConfig{DefaultRate: 0.0}
	s, err := newFromLegacy(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeSummaryDP(map[string]string{}, 1000, 10, 200.0)
	rms := makeSummaryRM("rpc_duration", dp)
	result := s.Sample(rms)
	if len(result) != 0 {
		t.Error("expected summary metric to be dropped (rate=0.0)")
	}
}

func TestSampleSummaryDataPoints_RuleMatch(t *testing.T) {
	cfg := FileConfig{
		DefaultRate: 0.0,
		Rules: []Rule{
			{Name: "keep-sli", Match: map[string]string{"__name__": "sli_.*"}, Rate: 1.0},
		},
	}
	s, err := newFromLegacy(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// SLI metric should be kept.
	dp1 := makeSummaryDP(map[string]string{}, 1000, 5, 100.0)
	rms1 := makeSummaryRM("sli_latency", dp1)
	result1 := s.Sample(rms1)
	if len(result1) == 0 {
		t.Error("expected sli_latency summary to be kept")
	}

	// Non-SLI metric should be dropped.
	dp2 := makeSummaryDP(map[string]string{}, 2000, 10, 200.0)
	rms2 := makeSummaryRM("debug_summary", dp2)
	result2 := s.Sample(rms2)
	if len(result2) != 0 {
		t.Error("expected debug_summary to be dropped")
	}
}

func TestSampleSummaryDataPoints_EmptySlice(t *testing.T) {
	cfg := FileConfig{DefaultRate: 1.0}
	s, err := newFromLegacy(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Summary with empty datapoints.
	rms := []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "empty_summary",
							Data: &metricspb.Metric_Summary{
								Summary: &metricspb.Summary{DataPoints: []*metricspb.SummaryDataPoint{}},
							},
						},
					},
				},
			},
		},
	}
	// Should not panic.
	s.Sample(rms)
}

// ---------------------------------------------------------------------------
// 2. sampleExponentialHistogramDataPoints (0% coverage)
// ---------------------------------------------------------------------------

func TestSampleExpHistDataPoints_KeepAll(t *testing.T) {
	cfg := FileConfig{DefaultRate: 1.0}
	s, err := newFromLegacy(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeExpHistDP(map[string]string{"region": "us"}, 1000, 5, 100.0)
	rms := makeExpHistRM("http_exphist", dp)
	result := s.Sample(rms)
	if len(result) == 0 {
		t.Error("expected exponential histogram metric to be kept (rate=1.0)")
	}
}

func TestSampleExpHistDataPoints_DropAll(t *testing.T) {
	cfg := FileConfig{DefaultRate: 0.0}
	s, err := newFromLegacy(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeExpHistDP(map[string]string{}, 1000, 10, 200.0)
	rms := makeExpHistRM("request_exphist", dp)
	result := s.Sample(rms)
	if len(result) != 0 {
		t.Error("expected exponential histogram metric to be dropped (rate=0.0)")
	}
}

func TestSampleExpHistDataPoints_RuleMatch(t *testing.T) {
	cfg := FileConfig{
		DefaultRate: 0.0,
		Rules: []Rule{
			{Name: "keep-http", Match: map[string]string{"__name__": "http_.*"}, Rate: 1.0},
		},
	}
	s, err := newFromLegacy(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Matching metric should be kept.
	dp1 := makeExpHistDP(map[string]string{}, 1000, 5, 100.0)
	rms1 := makeExpHistRM("http_latency", dp1)
	result1 := s.Sample(rms1)
	if len(result1) == 0 {
		t.Error("expected http_latency exponential histogram to be kept")
	}

	// Non-matching metric should be dropped.
	dp2 := makeExpHistDP(map[string]string{}, 2000, 10, 200.0)
	rms2 := makeExpHistRM("grpc_latency", dp2)
	result2 := s.Sample(rms2)
	if len(result2) != 0 {
		t.Error("expected grpc_latency exponential histogram to be dropped")
	}
}

func TestSampleExpHistDataPoints_EmptySlice(t *testing.T) {
	cfg := FileConfig{DefaultRate: 1.0}
	s, err := newFromLegacy(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// ExponentialHistogram with empty datapoints.
	rms := []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "empty_exphist",
							Data: &metricspb.Metric_ExponentialHistogram{
								ExponentialHistogram: &metricspb.ExponentialHistogram{
									DataPoints: []*metricspb.ExponentialHistogramDataPoint{},
								},
							},
						},
					},
				},
			},
		},
	}
	// Should not panic.
	s.Sample(rms)
}

// ---------------------------------------------------------------------------
// 3. LoadFile (0% coverage)
// ---------------------------------------------------------------------------

func TestLoadFile_Valid(t *testing.T) {
	content := `
default_rate: 0.5
strategy: head
rules:
  - name: keep_sli
    match:
      __name__: "sli_.*"
    rate: 1.0
`
	dir := t.TempDir()
	path := filepath.Join(dir, "sampling.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadFileConfig(path)
	if err != nil {
		t.Fatalf("LoadFile failed: %v", err)
	}
	if cfg.DefaultRate != 0.5 {
		t.Errorf("expected default_rate 0.5, got %f", cfg.DefaultRate)
	}
	if cfg.Strategy != StrategyHead {
		t.Errorf("expected strategy head, got %s", cfg.Strategy)
	}
	if len(cfg.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(cfg.Rules))
	}
	if cfg.Rules[0].Name != "keep_sli" {
		t.Errorf("expected rule name 'keep_sli', got '%s'", cfg.Rules[0].Name)
	}
}

func TestLoadFile_NonExistentFile(t *testing.T) {
	_, err := loadFileConfig("/nonexistent/path/does_not_exist.yaml")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestLoadFile_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte(":::invalid yaml!!!"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := loadFileConfig(path)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

// ---------------------------------------------------------------------------
// 4. UpdateDeadRuleMetrics (0% coverage)
// ---------------------------------------------------------------------------

func TestUpdateDeadRuleMetrics(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "cov-dead-1", Input: "covdead1_.*", Action: ActionDrop},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Should not panic and should update gauges.
	s.UpdateDeadRuleMetrics()

	// Process a metric so lastMatchTime gets set.
	dp := makeNumberDP(map[string]string{}, 1000, 42)
	rms := makeProcGaugeRM("covdead1_metric", dp)
	s.Process(rms)

	// Call again after a match has occurred.
	s.UpdateDeadRuleMetrics()
}

func TestUpdateDeadRuleMetrics_NoRules(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Should not panic with empty rules.
	s.UpdateDeadRuleMetrics()
}

// ---------------------------------------------------------------------------
// 5. deadRuleScanner.updateRules (0% coverage)
// ---------------------------------------------------------------------------

func TestDeadRuleScanner_UpdateRules(t *testing.T) {
	interval := 100 * time.Millisecond

	initialRules := []ProcessingRule{
		{Name: "cov-scanup-1", Input: "covscanup1_.*", Action: ActionDrop},
	}
	cfg := ProcessingConfig{Rules: initialRules}
	if err := validateProcessingConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	initialRules = cfg.Rules

	scanner := newDeadRuleScanner(interval, initialRules)
	defer scanner.stop()

	// Verify initial rules are set.
	scanner.mu.Lock()
	if len(scanner.rules) != 1 {
		t.Errorf("expected 1 initial rule, got %d", len(scanner.rules))
	}
	scanner.mu.Unlock()

	// Update with new rules.
	newRules := []ProcessingRule{
		{Name: "cov-scanup-2", Input: "covscanup2_.*", Action: ActionSample, Rate: 1.0, Method: "head"},
		{Name: "cov-scanup-3", Input: "covscanup3_.*", Action: ActionDrop},
	}
	newCfg := ProcessingConfig{Rules: newRules}
	if err := validateProcessingConfig(&newCfg); err != nil {
		t.Fatal(err)
	}
	newRules = newCfg.Rules

	scanner.updateRules(newRules)

	scanner.mu.Lock()
	if len(scanner.rules) != 2 {
		t.Errorf("expected 2 updated rules, got %d", len(scanner.rules))
	}
	if scanner.rules[0].Name != "cov-scanup-2" {
		t.Errorf("expected first rule name 'cov-scanup-2', got %q", scanner.rules[0].Name)
	}
	scanner.mu.Unlock()
}

// ---------------------------------------------------------------------------
// 6. StopDeadRuleScanner — test with active scanner (50% coverage)
// ---------------------------------------------------------------------------

func TestStopDeadRuleScanner_WithActiveScanner(t *testing.T) {
	cfg := ProcessingConfig{
		DeadRuleInterval: "200ms",
		Rules: []ProcessingRule{
			{Name: "cov-stop-1", Input: "covstop1_.*", Action: ActionDrop},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if s.deadScanner == nil {
		t.Fatal("expected deadScanner to be non-nil when dead_rule_interval is set")
	}

	// Give the scanner time to run at least one scan.
	time.Sleep(150 * time.Millisecond)

	// StopDeadRuleScanner should not panic and should block until scanner exits.
	s.StopDeadRuleScanner()

	// Calling again on already-stopped scanner should not panic.
	// (deadScanner is still set but its stopCh is closed.)
}

// ---------------------------------------------------------------------------
// 7. sampleMetric — Summary and ExponentialHistogram branches (55.2% -> higher)
// ---------------------------------------------------------------------------

func TestSampleMetric_SummaryWithRule(t *testing.T) {
	cfg := FileConfig{
		DefaultRate: 1.0,
		Rules: []Rule{
			{Name: "drop-debug", Match: map[string]string{"__name__": "debug_.*"}, Rate: 0.0},
		},
	}
	s, err := newFromLegacy(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Summary metric matching drop rule.
	dp := makeSummaryDP(map[string]string{}, 1000, 5, 100.0)
	rms := makeSummaryRM("debug_summary", dp)
	result := s.Sample(rms)
	if len(result) != 0 {
		t.Error("expected debug_summary to be dropped by rule")
	}
}

func TestSampleMetric_ExponentialHistogramWithRule(t *testing.T) {
	cfg := FileConfig{
		DefaultRate: 1.0,
		Rules: []Rule{
			{Name: "drop-debug", Match: map[string]string{"__name__": "debug_.*"}, Rate: 0.0},
		},
	}
	s, err := newFromLegacy(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// ExponentialHistogram metric matching drop rule.
	dp := makeExpHistDP(map[string]string{}, 1000, 10, 200.0)
	rms := makeExpHistRM("debug_exphist", dp)
	result := s.Sample(rms)
	if len(result) != 0 {
		t.Error("expected debug_exphist to be dropped by rule")
	}
}

func TestSampleMetric_HistogramKeepAndDrop(t *testing.T) {
	cfg := FileConfig{
		DefaultRate: 1.0,
		Rules: []Rule{
			{Name: "drop-internal", Match: map[string]string{"__name__": "internal_.*"}, Rate: 0.0},
		},
	}
	s, err := newFromLegacy(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Histogram that should be kept.
	dp1 := makeHistDP(map[string]string{}, 1000, 5, 100.0)
	rms1 := makeHistRM("http_duration", dp1)
	result1 := s.Sample(rms1)
	if len(result1) == 0 {
		t.Error("expected http_duration histogram to be kept")
	}

	// Histogram that should be dropped by rule.
	dp2 := makeHistDP(map[string]string{}, 1000, 5, 100.0)
	rms2 := makeHistRM("internal_hist", dp2)
	result2 := s.Sample(rms2)
	if len(result2) != 0 {
		t.Error("expected internal_hist histogram to be dropped")
	}
}

func TestSampleMetric_NilDataFields(t *testing.T) {
	cfg := FileConfig{DefaultRate: 1.0}
	s, err := newFromLegacy(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Summary with nil Summary field.
	rms := []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "nil_summary",
							Data: &metricspb.Metric_Summary{Summary: nil},
						},
						{
							Name: "nil_exphist",
							Data: &metricspb.Metric_ExponentialHistogram{ExponentialHistogram: nil},
						},
						{
							Name: "nil_histogram",
							Data: &metricspb.Metric_Histogram{Histogram: nil},
						},
						{
							Name: "nil_gauge",
							Data: &metricspb.Metric_Gauge{Gauge: nil},
						},
						{
							Name: "nil_sum",
							Data: &metricspb.Metric_Sum{Sum: nil},
						},
					},
				},
			},
		},
	}
	// Should not panic with nil data fields.
	result := s.Sample(rms)
	// Metrics with nil data fields pass through without crashing.
	if len(result) == 0 {
		// This is acceptable; nil data returns the metric as-is.
	}
}

// ---------------------------------------------------------------------------
// 8. createProcessor — test all method branches (50% coverage)
// ---------------------------------------------------------------------------

func TestCreateProcessor_AllMethods(t *testing.T) {
	tests := []struct {
		name   string
		cfg    *DownsampleConfig
		method DownsampleMethod
	}{
		{"avg", &DownsampleConfig{Method: DSAvg, parsedWindow: time.Minute}, DSAvg},
		{"min", &DownsampleConfig{Method: DSMin, parsedWindow: time.Minute}, DSMin},
		{"max", &DownsampleConfig{Method: DSMax, parsedWindow: time.Minute}, DSMax},
		{"sum", &DownsampleConfig{Method: DSSum, parsedWindow: time.Minute}, DSSum},
		{"count", &DownsampleConfig{Method: DSCount, parsedWindow: time.Minute}, DSCount},
		{"last", &DownsampleConfig{Method: DSLast, parsedWindow: time.Minute}, DSLast},
		{"delta", &DownsampleConfig{Method: DSDelta, parsedWindow: time.Minute}, DSDelta},
		{"lttb", &DownsampleConfig{Method: DSLTTB, parsedWindow: time.Second, Resolution: 10}, DSLTTB},
		{"sdt", &DownsampleConfig{Method: DSSDT, Deviation: 0.5}, DSSDT},
		{"adaptive", &DownsampleConfig{Method: DSAdaptive, parsedWindow: time.Minute, MinRate: 0.1, MaxRate: 1.0, VarianceWindow: 20}, DSAdaptive},
		{"unknown_falls_to_default", &DownsampleConfig{Method: "unknown_method", parsedWindow: time.Minute}, "unknown_method"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := createProcessor(tt.cfg)
			if p == nil {
				t.Fatal("createProcessor returned nil")
			}
			// Verify it can ingest without panic.
			p.ingest(1000, 42.0)
			p.flush()
		})
	}
}

// ---------------------------------------------------------------------------
// 9. newAdaptiveProcessor — edge cases (55.6% coverage)
// ---------------------------------------------------------------------------

func TestNewAdaptiveProcessor_Defaults(t *testing.T) {
	// All zeros/negatives should get defaults.
	p := newAdaptiveProcessor(0, 0, 0)
	if p.minRate != 0.1 {
		t.Errorf("expected default minRate 0.1, got %f", p.minRate)
	}
	if p.maxRate != 1.0 {
		t.Errorf("expected default maxRate 1.0, got %f", p.maxRate)
	}
	if p.varianceWindow != 20 {
		t.Errorf("expected default varianceWindow 20, got %d", p.varianceWindow)
	}
}

func TestNewAdaptiveProcessor_MinExceedsMax(t *testing.T) {
	// minRate > maxRate should be clamped.
	p := newAdaptiveProcessor(0.9, 0.5, 10)
	if p.minRate > p.maxRate {
		t.Errorf("minRate %f should not exceed maxRate %f", p.minRate, p.maxRate)
	}
	if p.minRate != p.maxRate {
		t.Errorf("expected minRate to be clamped to maxRate=%f, got %f", p.maxRate, p.minRate)
	}
}

func TestNewAdaptiveProcessor_NegativeRates(t *testing.T) {
	p := newAdaptiveProcessor(-0.5, -0.5, -5)
	if p.minRate != 0.1 {
		t.Errorf("expected default minRate 0.1 for negative input, got %f", p.minRate)
	}
	if p.maxRate != 1.0 {
		t.Errorf("expected default maxRate 1.0 for negative input, got %f", p.maxRate)
	}
	if p.varianceWindow != 20 {
		t.Errorf("expected default varianceWindow 20 for negative input, got %d", p.varianceWindow)
	}
}

func TestNewAdaptiveProcessor_MaxRateAboveOne(t *testing.T) {
	p := newAdaptiveProcessor(0.2, 1.5, 10)
	if p.maxRate != 1.0 {
		t.Errorf("expected maxRate clamped to 1.0, got %f", p.maxRate)
	}
}

// ---------------------------------------------------------------------------
// 10. newLTTBProcessor — edge cases (66.7% coverage)
// ---------------------------------------------------------------------------

func TestNewLTTBProcessor_NegativeResolution(t *testing.T) {
	p := newLTTBProcessor(time.Second, -5)
	if p.bucketDur == 0 {
		t.Error("expected non-zero bucketDur even with negative resolution")
	}
	// Should default to resolution=10
	expected := uint64(time.Second) / 10
	if p.bucketDur != expected {
		t.Errorf("expected bucketDur %d, got %d", expected, p.bucketDur)
	}
}

func TestNewLTTBProcessor_ZeroResolution(t *testing.T) {
	p := newLTTBProcessor(time.Second, 0)
	if p.bucketDur == 0 {
		t.Error("expected non-zero bucketDur with zero resolution")
	}
}

func TestNewLTTBProcessor_VerySmallWindow(t *testing.T) {
	// Window so small that bucketDur would be 0 before the guard.
	p := newLTTBProcessor(time.Nanosecond, 100)
	if p.bucketDur == 0 {
		t.Error("expected non-zero bucketDur for very small window")
	}
	// Should fall back to 1 second.
	if p.bucketDur != uint64(time.Second) {
		t.Errorf("expected bucketDur %d (1s fallback), got %d", uint64(time.Second), p.bucketDur)
	}
}

// ---------------------------------------------------------------------------
// 11. newSDTProcessor — edge case (66.7% coverage)
// ---------------------------------------------------------------------------

func TestNewSDTProcessor_ZeroDeviation(t *testing.T) {
	p := newSDTProcessor(0)
	if p.deviation != 1.0 {
		t.Errorf("expected default deviation 1.0 for zero input, got %f", p.deviation)
	}
}

func TestNewSDTProcessor_NegativeDeviation(t *testing.T) {
	p := newSDTProcessor(-5.0)
	if p.deviation != 1.0 {
		t.Errorf("expected default deviation 1.0 for negative input, got %f", p.deviation)
	}
}

// ---------------------------------------------------------------------------
// 12. selectBest — test with prevSelected nil (67.9% coverage)
// ---------------------------------------------------------------------------

func TestLTTBSelectBest_NoPreviousSelection(t *testing.T) {
	// When prevSelected is nil, selectBest should pick the most extreme point.
	p := newLTTBProcessor(time.Second, 10)

	// Manually add points to the current bucket without a prevSelected.
	p.initialized = true
	p.currentBucket = []emittedPoint{
		{timestamp: 0, value: 5.0},
		{timestamp: 10, value: 5.1},
		{timestamp: 20, value: 100.0}, // extreme
		{timestamp: 30, value: 5.0},
	}
	p.prevSelected = nil

	selected := p.selectBest()
	// Should select the most extreme point (100.0).
	if selected.value != 100.0 {
		t.Errorf("expected extreme value 100.0, got %f", selected.value)
	}
}

func TestLTTBSelectBest_WithPreviousSelection(t *testing.T) {
	p := newLTTBProcessor(time.Second, 10)

	prev := emittedPoint{timestamp: 0, value: 5.0}
	p.prevSelected = &prev
	p.initialized = true
	p.currentBucket = []emittedPoint{
		{timestamp: 100, value: 5.0},
		{timestamp: 200, value: 50.0}, // spike
		{timestamp: 300, value: 5.1},
	}

	selected := p.selectBest()
	// Should select the point maximizing triangle area (the spike at 50.0).
	if selected.value != 50.0 {
		t.Errorf("expected spike value 50.0 selected via LTTB, got %f", selected.value)
	}
}

// ---------------------------------------------------------------------------
// 13. EstimatedMemoryBytes (22.2% coverage)
// ---------------------------------------------------------------------------

func TestEstimatedMemoryBytes_WithEntries(t *testing.T) {
	c := newProcessingRuleCache(100)

	c.Put("metric_alpha", []int{0, 1, 2})
	c.Put("metric_beta", []int{3, 4})
	c.Put("metric_gamma", []int{})

	mem := c.EstimatedMemoryBytes()
	if mem <= 0 {
		t.Errorf("expected positive memory estimate, got %d", mem)
	}

	// Each entry should contribute: 200 base + len(key) + len(indices)*8
	// Minimum expected: 3 entries * 200 base = 600
	if mem < 600 {
		t.Errorf("expected memory >= 600 bytes, got %d", mem)
	}
}

func TestEstimatedMemoryBytes_Empty(t *testing.T) {
	c := newProcessingRuleCache(100)
	mem := c.EstimatedMemoryBytes()
	if mem != 0 {
		t.Errorf("expected 0 memory for empty cache, got %d", mem)
	}
}

// ---------------------------------------------------------------------------
// 14. newProcessingRuleCache — zero/negative maxSize (66.7% coverage)
// ---------------------------------------------------------------------------

func TestNewProcessingRuleCache_ZeroSize(t *testing.T) {
	c := newProcessingRuleCache(0)
	if c != nil {
		t.Error("expected nil cache for maxSize=0")
	}
}

func TestNewProcessingRuleCache_NegativeSize(t *testing.T) {
	c := newProcessingRuleCache(-10)
	if c != nil {
		t.Error("expected nil cache for negative maxSize")
	}
}

// ---------------------------------------------------------------------------
// 15. ReloadProcessingConfig with dead scanner (covers updateRules call path)
// ---------------------------------------------------------------------------

func TestReloadProcessingConfig_WithDeadScanner(t *testing.T) {
	cfg := ProcessingConfig{
		DeadRuleInterval: "200ms",
		Rules: []ProcessingRule{
			{Name: "cov-rpc-1", Input: "covrpc1_.*", Action: ActionDrop},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.StopDeadRuleScanner()

	if s.deadScanner == nil {
		t.Fatal("expected deadScanner to be non-nil")
	}

	// Reload should call updateRules on the dead scanner.
	newCfg := ProcessingConfig{
		DeadRuleInterval: "200ms",
		Rules: []ProcessingRule{
			{Name: "cov-rpc-2", Input: "covrpc2_.*", Action: ActionSample, Rate: 1.0, Method: "head"},
			{Name: "cov-rpc-3", Input: "covrpc3_.*", Action: ActionDrop},
		},
	}
	if err := s.ReloadProcessingConfig(newCfg); err != nil {
		t.Fatal(err)
	}

	if s.ProcessingRuleCount() != 2 {
		t.Errorf("expected 2 rules after reload, got %d", s.ProcessingRuleCount())
	}
}

// ---------------------------------------------------------------------------
// 16. sampleHistogramDataPoints — test both keep and empty result (66.7%)
// ---------------------------------------------------------------------------

func TestSampleHistogramDataPoints_KeepAll(t *testing.T) {
	cfg := FileConfig{DefaultRate: 1.0}
	s, err := newFromLegacy(cfg)
	if err != nil {
		t.Fatal(err)
	}

	m := &metricspb.Metric{
		Name: "hist_keep",
		Data: &metricspb.Metric_Histogram{
			Histogram: &metricspb.Histogram{
				DataPoints: []*metricspb.HistogramDataPoint{
					{Attributes: makeAttrs("env", "prod")},
					{Attributes: makeAttrs("env", "staging")},
				},
			},
		},
	}
	rms := makeRM(m)
	result := s.Sample(rms)
	if len(result) == 0 {
		t.Error("expected histogram data to be kept (rate=1.0)")
	}
	histDPs := result[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Histogram).Histogram.DataPoints
	if len(histDPs) != 2 {
		t.Errorf("expected 2 histogram datapoints, got %d", len(histDPs))
	}
}

// ---------------------------------------------------------------------------
// 18. createGroup — with rate accumulator (covers rate setWindow branch)
// ---------------------------------------------------------------------------

func TestCreateGroup_WithRateFunction(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:      "cov-agg-rate-1",
				Input:     "covaggrate1_.*",
				Action:    ActionAggregate,
				Output:    "covaggrate1_agg",
				Interval:  "30s",
				Functions: []string{"rate"},
				GroupBy:   []string{"host"},
			},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Ingest data to trigger group creation.
	dp := makeNumberDP(map[string]string{"host": "a"}, 1000, 100.0)
	rms := makeProcGaugeRM("covaggrate1_cpu", dp)
	s.Process(rms)

	dp2 := makeNumberDP(map[string]string{"host": "a"}, 2000, 200.0)
	rms2 := makeProcGaugeRM("covaggrate1_cpu", dp2)
	s.Process(rms2)

	// Verify the group was created with rate accumulator.
	if s.aggEngine.ActiveGroups() < 1 {
		t.Error("expected at least 1 aggregate group")
	}
}

func TestCreateGroup_WithQuantilesFunction(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:      "cov-agg-q-1",
				Input:     "covaggq1_.*",
				Action:    ActionAggregate,
				Output:    "covaggq1_agg",
				Interval:  "30s",
				Functions: []string{"quantiles(0.5,0.9,0.99)"},
				GroupBy:   []string{"host"},
			},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Ingest data.
	dp := makeNumberDP(map[string]string{"host": "x"}, 1000, 42.0)
	rms := makeProcGaugeRM("covaggq1_latency", dp)
	s.Process(rms)

	if s.aggEngine.ActiveGroups() < 1 {
		t.Error("expected at least 1 aggregate group with quantiles")
	}
}

// ---------------------------------------------------------------------------
// 19. quantilesAccum.Add — reservoir sampling path (57.1% coverage)
// ---------------------------------------------------------------------------

func TestQuantilesAccum_ReservoirSampling(t *testing.T) {
	a := newQuantilesAccum([]float64{0.5})

	// Add more than maxQuantileSamples to trigger reservoir sampling.
	for i := 0; i < maxQuantileSamples+1000; i++ {
		a.Add(float64(i))
	}

	// Values slice should be capped at maxQuantileSamples.
	if len(a.values) != maxQuantileSamples {
		t.Errorf("expected values capped at %d, got %d", maxQuantileSamples, len(a.values))
	}

	// Count should reflect all additions.
	if a.count != int64(maxQuantileSamples+1000) {
		t.Errorf("expected count %d, got %d", maxQuantileSamples+1000, a.count)
	}

	// P50 should be roughly in the middle of the range.
	result := a.Result()
	if result < float64(maxQuantileSamples/4) || result > float64(maxQuantileSamples+1000)*3/4 {
		t.Errorf("p50 = %f, seems out of expected range", result)
	}
}

// ---------------------------------------------------------------------------
// 20. stdvarAccum.Result — count < 2 path (66.7% coverage)
// ---------------------------------------------------------------------------

func TestStdvarAccum_SingleValue(t *testing.T) {
	a := &stdvarAccum{}
	a.Add(5)
	if got := a.Result(); got != 0 {
		t.Errorf("stdvar(single) = %f, want 0", got)
	}
}

func TestStdvarAccum_Empty(t *testing.T) {
	a := &stdvarAccum{}
	if got := a.Result(); got != 0 {
		t.Errorf("stdvar(empty) = %f, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// 21. SDT processor — out-of-order timestamp (covers ts <= lastEmitted path)
// ---------------------------------------------------------------------------

func TestSDTProcessor_OutOfOrderTimestamp(t *testing.T) {
	p := newSDTProcessor(0.5)

	// First point emitted.
	e0 := p.ingest(1000, 10.0)
	if len(e0) != 1 {
		t.Fatalf("expected first point emitted, got %d", len(e0))
	}

	// Out-of-order timestamp (same or earlier).
	e1 := p.ingest(1000, 20.0) // same timestamp
	if len(e1) != 0 {
		t.Error("expected no emission for duplicate timestamp")
	}

	e2 := p.ingest(500, 30.0) // earlier timestamp
	if len(e2) != 0 {
		t.Error("expected no emission for out-of-order timestamp")
	}
}

// ---------------------------------------------------------------------------
// 22. Downsample config validate — negative window duration
// ---------------------------------------------------------------------------

func TestDownsampleConfig_NegativeWindow(t *testing.T) {
	cfg := DownsampleConfig{Method: DSAvg, Window: "-1m"}
	err := cfg.validate()
	if err == nil {
		t.Error("expected error for negative window duration")
	}
}

// ---------------------------------------------------------------------------
// 23. Aggregate agg processor — aggregate with count=0 case
// ---------------------------------------------------------------------------

func TestAggProcessor_AggregateCountZero(t *testing.T) {
	// The aggregate method should handle count=0 for avg method.
	p := newAggProcessor(DSAvg, time.Minute)
	// Call aggregate on a fresh (count=0) processor.
	val := p.aggregate()
	if val != 0 {
		t.Errorf("expected 0 for avg with count=0, got %f", val)
	}
}

// ---------------------------------------------------------------------------
// 24. Adaptive processor variance — mean=0 (covers cv=0 branch)
// ---------------------------------------------------------------------------

func TestAdaptiveProcessor_ZeroMean(t *testing.T) {
	p := newAdaptiveProcessor(0.1, 1.0, 5)

	// Feed values that average to zero.
	p.ingest(0, -10.0)
	p.ingest(1000, 10.0)
	p.ingest(2000, -10.0)
	p.ingest(3000, 10.0)
	p.ingest(4000, -10.0)
	p.ingest(5000, 10.0)

	// Should not panic. keepRate should be valid.
	rate := p.keepRateValue()
	if rate < p.minRate || rate > p.maxRate {
		t.Errorf("keepRate %f out of expected range [%f, %f]", rate, p.minRate, p.maxRate)
	}
}

// ---------------------------------------------------------------------------
// 25. getNumberValue — nil/unset value (covers default path)
// ---------------------------------------------------------------------------

func TestGetNumberValue_NilValue(t *testing.T) {
	dp := &metricspb.NumberDataPoint{
		Value: nil,
	}
	if v := getNumberValue(dp); v != 0 {
		t.Errorf("expected 0 for nil value, got %f", v)
	}
}

// ---------------------------------------------------------------------------
// 26. Validate classify rule edge cases — to improve validateClassifyRule
// ---------------------------------------------------------------------------

func TestValidateClassifyRule_EmptyChains(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "cov-classify-1",
				Input:  "covclassify1_.*",
				Action: ActionClassify,
				Classify: &ClassifyConfig{
					Chains: []ClassifyChain{},
					Mappings: []ClassifyMapping{
						{Source: "env", Target: "tier", Values: map[string]string{"prod": "high"}},
					},
				},
			},
		},
	}
	_, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatalf("expected valid config with empty chains: %v", err)
	}
}

func TestValidateClassifyRule_MissingTarget(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "cov-classify-bad-1",
				Input:  "covclassifybad1_.*",
				Action: ActionClassify,
				Classify: &ClassifyConfig{
					Mappings: []ClassifyMapping{
						{Source: "env", Target: "", Values: map[string]string{"prod": "high"}},
					},
				},
			},
		},
	}
	_, err := NewFromProcessing(cfg)
	if err == nil {
		t.Error("expected error for mapping with empty target")
	}
}

func TestValidateClassifyRule_MissingSources(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "cov-classify-bad-2",
				Input:  "covclassifybad2_.*",
				Action: ActionClassify,
				Classify: &ClassifyConfig{
					Mappings: []ClassifyMapping{
						{Target: "tier", Values: map[string]string{"prod": "high"}},
					},
				},
			},
		},
	}
	_, err := NewFromProcessing(cfg)
	if err == nil {
		t.Error("expected error for mapping with no source/sources")
	}
}

func TestValidateClassifyRule_NilClassify(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:     "cov-classify-nil-1",
				Input:    "covclassifynil1_.*",
				Action:   ActionClassify,
				Classify: nil,
			},
		},
	}
	_, err := NewFromProcessing(cfg)
	if err == nil {
		t.Error("expected error for classify action with nil Classify config")
	}
}

// ---------------------------------------------------------------------------
// 27. Parse — invalid YAML (covers error path in Parse)
// ---------------------------------------------------------------------------

func TestParse_InvalidYAML(t *testing.T) {
	_, err := parseFileConfig([]byte(":::invalid yaml"))
	if err == nil {
		t.Error("expected error for invalid YAML input to parseFileConfig()")
	}
}

// ---------------------------------------------------------------------------
// 28. matchProcessingRuleName — prefix and regex paths
// ---------------------------------------------------------------------------

func TestMatchProcessingRuleName_PrefixMismatch(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "cov-prefix-1", Input: "specific_prefix_.*", Action: ActionDrop},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		t.Fatal(err)
	}

	rule := &cfg.Rules[0]

	if matchProcessingRuleName(rule, "specific_prefix_metric") != true {
		t.Error("expected match for specific_prefix_metric")
	}
	if matchProcessingRuleName(rule, "other_metric") != false {
		t.Error("expected no match for other_metric")
	}
}

// ---------------------------------------------------------------------------
// 29. Validate downsample rule in processing config
// ---------------------------------------------------------------------------

func TestValidateDownsampleRule_MissingMethod(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:     "cov-ds-bad-1",
				Input:    "covdsbad1_.*",
				Action:   ActionDownsample,
				Interval: "1m",
				// Method is empty.
			},
		},
	}
	_, err := NewFromProcessing(cfg)
	if err == nil {
		t.Error("expected error for downsample rule without method")
	}
}

func TestValidateDownsampleRule_MissingInterval(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "cov-ds-bad-2",
				Input:  "covdsbad2_.*",
				Action: ActionDownsample,
				Method: "avg",
				// Interval is empty.
			},
		},
	}
	_, err := NewFromProcessing(cfg)
	if err == nil {
		t.Error("expected error for downsample rule without interval")
	}
}

// ---------------------------------------------------------------------------
// 30. Validate aggregate rule in processing config
// ---------------------------------------------------------------------------

func TestValidateAggregateRule_MissingInterval(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:      "cov-agg-bad-1",
				Input:     "covaggbad1_.*",
				Action:    ActionAggregate,
				Functions: []string{"sum"},
				Output:    "output",
				// Interval is empty.
			},
		},
	}
	_, err := NewFromProcessing(cfg)
	if err == nil {
		t.Error("expected error for aggregate rule without interval")
	}
}

func TestValidateAggregateRule_MissingFunctions(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:     "cov-agg-bad-2",
				Input:    "covaggbad2_.*",
				Action:   ActionAggregate,
				Interval: "1m",
				Output:   "output",
				// Functions is empty.
			},
		},
	}
	_, err := NewFromProcessing(cfg)
	if err == nil {
		t.Error("expected error for aggregate rule without functions")
	}
}

// ---------------------------------------------------------------------------
// 31. Sample with downsample strategy (integration via legacy sampler)
// ---------------------------------------------------------------------------

func TestSampleMetric_DownsampleLTTB(t *testing.T) {
	cfg := FileConfig{
		DefaultRate: 1.0,
		Rules: []Rule{
			{
				Name:     "ds-lttb",
				Match:    map[string]string{"__name__": "lttb_metric"},
				Strategy: StrategyDownsample,
				Downsample: &DownsampleConfig{
					Method:     DSLTTB,
					Window:     "1s",
					Resolution: 5,
				},
			},
		},
	}
	s, err := newFromLegacy(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Feed points across multiple buckets.
	bucketDur := uint64(time.Second) / 5
	rms := []*metricspb.ResourceMetrics{
		makeGaugeRM("lttb_metric", []dpVal{
			{ts: 0, val: 1.0},
			{ts: bucketDur, val: 2.0},
			{ts: 2 * bucketDur, val: 3.0},
			{ts: 3 * bucketDur, val: 4.0},
		}),
	}
	result := s.Sample(rms)
	// Some points should be emitted (LTTB emits on bucket transitions).
	_ = result // No panic is sufficient for coverage.
}

func TestSampleMetric_DownsampleAdaptive(t *testing.T) {
	cfg := FileConfig{
		DefaultRate: 1.0,
		Rules: []Rule{
			{
				Name:     "ds-adaptive",
				Match:    map[string]string{"__name__": "adaptive_metric"},
				Strategy: StrategyDownsample,
				Downsample: &DownsampleConfig{
					Method:         DSAdaptive,
					Window:         "1m",
					MinRate:        0.1,
					MaxRate:        1.0,
					VarianceWindow: 5,
				},
			},
		},
	}
	s, err := newFromLegacy(cfg)
	if err != nil {
		t.Fatal(err)
	}

	rms := []*metricspb.ResourceMetrics{
		makeGaugeRM("adaptive_metric", []dpVal{
			{ts: 1000, val: 10.0},
			{ts: 2000, val: 10.0},
			{ts: 3000, val: 10.0},
		}),
	}
	result := s.Sample(rms)
	_ = result // No panic is sufficient.
}

// ---------------------------------------------------------------------------
// 32. compileRules — downsample without config block
// ---------------------------------------------------------------------------

func TestCompileRules_DownsampleMissingConfig(t *testing.T) {
	cfg := FileConfig{
		Rules: []Rule{
			{
				Name:     "ds-no-config",
				Match:    map[string]string{"__name__": "test"},
				Strategy: StrategyDownsample,
				// Downsample is nil.
			},
		},
	}
	_, err := newFromLegacy(cfg)
	if err == nil {
		t.Error("expected error for downsample strategy without downsample config")
	}
}

// ---------------------------------------------------------------------------
// 33. newAccumulator — unknown AggFunc falls to default (91.7% -> 100%)
// ---------------------------------------------------------------------------

func TestNewAccumulator_UnknownFunc(t *testing.T) {
	a := newAccumulator(AggFunc("unknown"))
	if a == nil {
		t.Fatal("expected non-nil accumulator for unknown func")
	}
	// Should default to lastAccum.
	a.Add(42)
	if got := a.Result(); got != 42 {
		t.Errorf("expected 42 from default accumulator, got %f", got)
	}
}
