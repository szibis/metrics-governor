package sampling

import (
	"testing"
)

// TestCompat_LegacyConfigAutoDetect verifies that ParseProcessing auto-detects old
// format (containing default_rate) and converts it to a ProcessingConfig.
func TestCompat_LegacyConfigAutoDetect(t *testing.T) {
	yaml := []byte(`
default_rate: 0.5
strategy: head
rules:
  - name: keep-sli
    match:
      __name__: "sli_.*"
    rate: 1.0
`)

	cfg, err := ParseProcessing(yaml)
	if err != nil {
		t.Fatalf("ParseProcessing failed on legacy config: %v", err)
	}

	// Should have converted: the keep-sli rule + a default catch-all.
	if len(cfg.Rules) < 2 {
		t.Fatalf("expected at least 2 rules, got %d", len(cfg.Rules))
	}

	// First rule should be the explicit keep-sli rule.
	if cfg.Rules[0].Name != "keep-sli" {
		t.Errorf("rule[0].Name = %q, want %q", cfg.Rules[0].Name, "keep-sli")
	}
	if cfg.Rules[0].Action != ActionSample {
		t.Errorf("rule[0].Action = %q, want %q", cfg.Rules[0].Action, ActionSample)
	}
	if cfg.Rules[0].Rate != 1.0 {
		t.Errorf("rule[0].Rate = %v, want 1.0", cfg.Rules[0].Rate)
	}
	if cfg.Rules[0].Input != "sli_.*" {
		t.Errorf("rule[0].Input = %q, want %q", cfg.Rules[0].Input, "sli_.*")
	}

	// Last rule should be the default catch-all.
	last := cfg.Rules[len(cfg.Rules)-1]
	if last.Name != "default" {
		t.Errorf("last rule Name = %q, want %q", last.Name, "default")
	}
	if last.Input != ".*" {
		t.Errorf("last rule Input = %q, want %q", last.Input, ".*")
	}
	if last.Action != ActionSample {
		t.Errorf("last rule Action = %q, want %q", last.Action, ActionSample)
	}
	if last.Rate != 0.5 {
		t.Errorf("last rule Rate = %v, want 0.5", last.Rate)
	}
}

// TestCompat_LegacyConfigConversion verifies that convertLegacyConfig preserves
// semantics: rule ordering, rate values, strategy to method mapping, and input regex.
func TestCompat_LegacyConfigConversion(t *testing.T) {
	old := FileConfig{
		DefaultRate: 0.25,
		Strategy:    StrategyProbabilistic,
		Rules: []Rule{
			{
				Name:  "high-priority",
				Match: map[string]string{"__name__": "critical_.*"},
				Rate:  1.0,
			},
			{
				Name:     "low-priority",
				Match:    map[string]string{"__name__": "debug_.*"},
				Rate:     0.0,
				Strategy: StrategyHead,
			},
		},
	}

	cfg := convertLegacyConfig(old)

	if len(cfg.Rules) != 3 {
		t.Fatalf("expected 3 rules (2 explicit + 1 default), got %d", len(cfg.Rules))
	}

	// Rule 0: critical -> sample rate=1.0, method=probabilistic (inherits from FileConfig.Strategy).
	r0 := cfg.Rules[0]
	if r0.Name != "high-priority" {
		t.Errorf("rule[0].Name = %q, want %q", r0.Name, "high-priority")
	}
	if r0.Action != ActionSample {
		t.Errorf("rule[0].Action = %q, want %q", r0.Action, ActionSample)
	}
	if r0.Rate != 1.0 {
		t.Errorf("rule[0].Rate = %v, want 1.0", r0.Rate)
	}
	if r0.Method != "probabilistic" {
		t.Errorf("rule[0].Method = %q, want %q", r0.Method, "probabilistic")
	}
	if r0.Input != "critical_.*" {
		t.Errorf("rule[0].Input = %q, want %q", r0.Input, "critical_.*")
	}

	// Rule 1: debug -> drop (rate <= 0 becomes ActionDrop).
	r1 := cfg.Rules[1]
	if r1.Name != "low-priority" {
		t.Errorf("rule[1].Name = %q, want %q", r1.Name, "low-priority")
	}
	if r1.Action != ActionDrop {
		t.Errorf("rule[1].Action = %q, want %q", r1.Action, ActionDrop)
	}

	// Rule 2: default catch-all with default_rate=0.25, probabilistic.
	r2 := cfg.Rules[2]
	if r2.Name != "default" {
		t.Errorf("rule[2].Name = %q, want %q", r2.Name, "default")
	}
	if r2.Action != ActionSample {
		t.Errorf("rule[2].Action = %q, want %q", r2.Action, ActionSample)
	}
	if r2.Rate != 0.25 {
		t.Errorf("rule[2].Rate = %v, want 0.25", r2.Rate)
	}
	if r2.Method != "probabilistic" {
		t.Errorf("rule[2].Method = %q, want %q", r2.Method, "probabilistic")
	}
	if r2.Input != ".*" {
		t.Errorf("rule[2].Input = %q, want %q", r2.Input, ".*")
	}
}

// TestCompat_LegacyWithDownsampleRules verifies that old rules with strategy=downsample
// and embedded DownsampleConfig convert to action=downsample with correct method/interval.
func TestCompat_LegacyWithDownsampleRules(t *testing.T) {
	old := FileConfig{
		DefaultRate: 1.0,
		Strategy:    StrategyHead,
		Rules: []Rule{
			{
				Name:     "ds-cpu",
				Match:    map[string]string{"__name__": "cpu_usage_.*"},
				Strategy: StrategyDownsample,
				Downsample: &DownsampleConfig{
					Method: DSAvg,
					Window: "5m",
				},
			},
		},
	}

	cfg := convertLegacyConfig(old)

	// Should have the downsample rule; default_rate=1.0 means no default rule added.
	if len(cfg.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(cfg.Rules))
	}

	r := cfg.Rules[0]
	if r.Name != "ds-cpu" {
		t.Errorf("Name = %q, want %q", r.Name, "ds-cpu")
	}
	if r.Action != ActionDownsample {
		t.Errorf("Action = %q, want %q", r.Action, ActionDownsample)
	}
	if r.Method != string(DSAvg) {
		t.Errorf("Method = %q, want %q", r.Method, string(DSAvg))
	}
	if r.Interval != "5m" {
		t.Errorf("Interval = %q, want %q", r.Interval, "5m")
	}
	if r.Input != "cpu_usage_.*" {
		t.Errorf("Input = %q, want %q", r.Input, "cpu_usage_.*")
	}
}

// TestCompat_DefaultRateBecomeCatchAll verifies that different default_rate values
// produce the correct catch-all behavior.
func TestCompat_DefaultRateBecomeCatchAll(t *testing.T) {
	tests := []struct {
		name         string
		defaultRate  float64
		strategy     Strategy
		wantAction   Action
		wantRate     float64
		wantMethod   string
		wantCatchAll bool // whether a default rule is appended
	}{
		{
			name:         "rate=0.5 becomes sample catch-all",
			defaultRate:  0.5,
			strategy:     StrategyHead,
			wantAction:   ActionSample,
			wantRate:     0.5,
			wantMethod:   "head",
			wantCatchAll: true,
		},
		{
			name:         "rate=0 becomes drop catch-all",
			defaultRate:  0,
			strategy:     StrategyHead,
			wantAction:   ActionDrop,
			wantRate:     0,
			wantCatchAll: true,
		},
		{
			name:         "rate=1.0 means pass-through, no catch-all",
			defaultRate:  1.0,
			strategy:     StrategyHead,
			wantCatchAll: false,
		},
		{
			name:         "rate=0.8 with probabilistic",
			defaultRate:  0.8,
			strategy:     StrategyProbabilistic,
			wantAction:   ActionSample,
			wantRate:     0.8,
			wantMethod:   "probabilistic",
			wantCatchAll: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := FileConfig{
				DefaultRate: tt.defaultRate,
				Strategy:    tt.strategy,
			}
			cfg := convertLegacyConfig(old)

			if !tt.wantCatchAll {
				// With rate >= 1.0, no default rule should be added.
				if len(cfg.Rules) != 0 {
					t.Errorf("expected no rules for pass-through, got %d", len(cfg.Rules))
				}
				return
			}

			if len(cfg.Rules) < 1 {
				t.Fatalf("expected at least 1 rule (default catch-all), got %d", len(cfg.Rules))
			}

			last := cfg.Rules[len(cfg.Rules)-1]
			if last.Name != "default" {
				t.Errorf("last rule Name = %q, want %q", last.Name, "default")
			}
			if last.Input != ".*" {
				t.Errorf("last rule Input = %q, want %q", last.Input, ".*")
			}
			if last.Action != tt.wantAction {
				t.Errorf("last rule Action = %q, want %q", last.Action, tt.wantAction)
			}
			if tt.wantAction == ActionSample {
				if last.Rate != tt.wantRate {
					t.Errorf("last rule Rate = %v, want %v", last.Rate, tt.wantRate)
				}
				if last.Method != tt.wantMethod {
					t.Errorf("last rule Method = %q, want %q", last.Method, tt.wantMethod)
				}
			}
		})
	}
}

// TestCompat_NewFormatPassthrough verifies that a new-format config (without default_rate)
// is parsed as-is without any legacy conversion.
func TestCompat_NewFormatPassthrough(t *testing.T) {
	yaml := []byte(`
rules:
  - name: drop-debug
    input: "debug_.*"
    action: drop
  - name: keep-sli
    input: "sli_.*"
    action: sample
    rate: 1.0
    method: head
`)

	cfg, err := ParseProcessing(yaml)
	if err != nil {
		t.Fatalf("ParseProcessing failed: %v", err)
	}

	if len(cfg.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(cfg.Rules))
	}

	if cfg.Rules[0].Name != "drop-debug" {
		t.Errorf("rule[0].Name = %q, want %q", cfg.Rules[0].Name, "drop-debug")
	}
	if cfg.Rules[0].Action != ActionDrop {
		t.Errorf("rule[0].Action = %q, want %q", cfg.Rules[0].Action, ActionDrop)
	}
	if cfg.Rules[0].Input != "debug_.*" {
		t.Errorf("rule[0].Input = %q, want %q", cfg.Rules[0].Input, "debug_.*")
	}

	if cfg.Rules[1].Name != "keep-sli" {
		t.Errorf("rule[1].Name = %q, want %q", cfg.Rules[1].Name, "keep-sli")
	}
	if cfg.Rules[1].Action != ActionSample {
		t.Errorf("rule[1].Action = %q, want %q", cfg.Rules[1].Action, ActionSample)
	}
	if cfg.Rules[1].Rate != 1.0 {
		t.Errorf("rule[1].Rate = %v, want 1.0", cfg.Rules[1].Rate)
	}
}

// TestCompat_SameResultOldVsNew verifies that an old-format config and its manually
// equivalent new-format config produce the same output when processing identical data.
func TestCompat_SameResultOldVsNew(t *testing.T) {
	// Old format config: keep sli_* at rate=1.0, drop debug_* (rate=0), default=1.0.
	oldCfg := FileConfig{
		DefaultRate: 1.0,
		Strategy:    StrategyHead,
		Rules: []Rule{
			{
				Name:  "keep-sli",
				Match: map[string]string{"__name__": "sli_.*"},
				Rate:  1.0,
			},
			{
				Name:  "drop-debug",
				Match: map[string]string{"__name__": "debug_.*"},
				Rate:  0,
			},
		},
	}

	// Convert old to new via ParseProcessing (round-trip through YAML).
	oldYAML := []byte(`
default_rate: 1.0
strategy: head
rules:
  - name: keep-sli
    match:
      __name__: "sli_.*"
    rate: 1.0
  - name: drop-debug
    match:
      __name__: "debug_.*"
    rate: 0
`)
	convertedCfg, err := ParseProcessing(oldYAML)
	if err != nil {
		t.Fatalf("ParseProcessing failed: %v", err)
	}

	// Build old sampler.
	oldSampler, err := newFromLegacy(oldCfg)
	if err != nil {
		t.Fatalf("newFromLegacy(old) failed: %v", err)
	}

	// Build new sampler from converted config.
	newSampler, err := NewFromProcessing(convertedCfg)
	if err != nil {
		t.Fatalf("NewFromProcessing failed: %v", err)
	}

	tests := []struct {
		metricName string
		wantKept   bool
	}{
		{"sli_latency_p99", true},
		{"sli_availability", true},
		{"debug_trace_id", false},
		{"debug_internal_gc", false},
		{"http_requests_total", true}, // matches default: pass-through
	}

	for _, tt := range tests {
		t.Run(tt.metricName, func(t *testing.T) {
			// Old sampler result.
			dpOld := makeNumberDP(map[string]string{}, 1000, 42)
			rmsOld := makeProcGaugeRM(tt.metricName, dpOld)
			oldResult := oldSampler.Sample(rmsOld)
			oldKept := len(oldResult) > 0

			// New sampler result.
			dpNew := makeNumberDP(map[string]string{}, 1000, 42)
			rmsNew := makeProcGaugeRM(tt.metricName, dpNew)
			newResult := newSampler.Sample(rmsNew)
			newKept := len(newResult) > 0

			if oldKept != tt.wantKept {
				t.Errorf("old sampler: metric %q kept=%v, want %v", tt.metricName, oldKept, tt.wantKept)
			}
			if newKept != tt.wantKept {
				t.Errorf("new sampler: metric %q kept=%v, want %v", tt.metricName, newKept, tt.wantKept)
			}
			if oldKept != newKept {
				t.Errorf("old vs new mismatch: metric %q old=%v new=%v", tt.metricName, oldKept, newKept)
			}
		})
	}
}

// TestCompat_LegacyLabelMatchers verifies that non-__name__ matchers in old format
// convert to input_labels in the new format.
func TestCompat_LegacyLabelMatchers(t *testing.T) {
	old := FileConfig{
		DefaultRate: 1.0,
		Strategy:    StrategyHead,
		Rules: []Rule{
			{
				Name: "env-filter",
				Match: map[string]string{
					"__name__": "http_.*",
					"env":      "prod",
				},
				Rate: 0.5,
			},
		},
	}

	cfg := convertLegacyConfig(old)

	if len(cfg.Rules) < 1 {
		t.Fatalf("expected at least 1 rule, got %d", len(cfg.Rules))
	}

	r := cfg.Rules[0]
	if r.Input != "http_.*" {
		t.Errorf("Input = %q, want %q", r.Input, "http_.*")
	}
	if r.InputLabels == nil {
		t.Fatal("InputLabels should not be nil for multi-label match")
	}
	if r.InputLabels["env"] != "prod" {
		t.Errorf("InputLabels[env] = %q, want %q", r.InputLabels["env"], "prod")
	}
	// __name__ should NOT appear in InputLabels.
	if _, ok := r.InputLabels["__name__"]; ok {
		t.Error("__name__ should not be in InputLabels")
	}
}

// TestCompat_LegacyNoNameMatch verifies that old rules without a __name__ matcher
// get a catch-all input pattern.
func TestCompat_LegacyNoNameMatch(t *testing.T) {
	old := FileConfig{
		DefaultRate: 1.0,
		Strategy:    StrategyHead,
		Rules: []Rule{
			{
				Name:  "label-only",
				Match: map[string]string{"env": "staging"},
				Rate:  0.1,
			},
		},
	}

	cfg := convertLegacyConfig(old)

	if len(cfg.Rules) < 1 {
		t.Fatalf("expected at least 1 rule, got %d", len(cfg.Rules))
	}

	r := cfg.Rules[0]
	if r.Input != ".*" {
		t.Errorf("Input = %q, want %q (catch-all for missing __name__)", r.Input, ".*")
	}
	if r.InputLabels["env"] != "staging" {
		t.Errorf("InputLabels[env] = %q, want %q", r.InputLabels["env"], "staging")
	}
}
