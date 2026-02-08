package sampling

import (
	"strings"
	"testing"
)

func TestParseProcessing_NewFormat(t *testing.T) {
	yaml := `
staleness_interval: 5m
rules:
  - name: keep-sli
    input: "sli_.*"
    action: sample
    rate: 1.0
    method: head
  - name: reduce-debug
    input: "debug_.*"
    action: sample
    rate: 0.01
    method: probabilistic
  - name: cpu-avg
    input: "process_cpu_.*"
    action: downsample
    method: avg
    interval: 1m
  - name: drop-internal
    input: "internal_.*"
    action: drop
`
	cfg, err := ParseProcessing([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseProcessing: %v", err)
	}
	if len(cfg.Rules) != 4 {
		t.Fatalf("expected 4 rules, got %d", len(cfg.Rules))
	}
	if cfg.Rules[0].Action != ActionSample {
		t.Errorf("rule[0] action = %q, want sample", cfg.Rules[0].Action)
	}
	if cfg.Rules[2].Action != ActionDownsample {
		t.Errorf("rule[2] action = %q, want downsample", cfg.Rules[2].Action)
	}
	if cfg.Rules[3].Action != ActionDrop {
		t.Errorf("rule[3] action = %q, want drop", cfg.Rules[3].Action)
	}
	if cfg.parsedStaleness.Minutes() != 5 {
		t.Errorf("staleness = %v, want 5m", cfg.parsedStaleness)
	}
}

func TestParseProcessing_LegacyFormat(t *testing.T) {
	yaml := `
default_rate: 0.5
strategy: head
rules:
  - name: keep-all
    match:
      __name__: "sli_.*"
    rate: 1.0
  - name: drop-debug
    match:
      __name__: "debug_.*"
    rate: 0.0
`
	cfg, err := ParseProcessing([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseProcessing (legacy): %v", err)
	}

	// Should have 2 legacy rules + 1 default catch-all.
	if len(cfg.Rules) != 3 {
		t.Fatalf("expected 3 rules (2 legacy + 1 default), got %d", len(cfg.Rules))
	}

	if cfg.Rules[0].Action != ActionSample {
		t.Errorf("rule[0] action = %q, want sample", cfg.Rules[0].Action)
	}
	if cfg.Rules[0].Rate != 1.0 {
		t.Errorf("rule[0] rate = %f, want 1.0", cfg.Rules[0].Rate)
	}
	if cfg.Rules[1].Action != ActionDrop {
		t.Errorf("rule[1] action = %q, want drop (rate=0)", cfg.Rules[1].Action)
	}
	if cfg.Rules[2].Name != "default" {
		t.Errorf("rule[2] name = %q, want 'default'", cfg.Rules[2].Name)
	}
	if cfg.Rules[2].Rate != 0.5 {
		t.Errorf("default rule rate = %f, want 0.5", cfg.Rules[2].Rate)
	}
}

func TestParseProcessing_LegacyDownsample(t *testing.T) {
	yaml := `
default_rate: 1.0
strategy: head
rules:
  - name: cpu-ds
    match:
      __name__: "cpu_.*"
    rate: 1.0
    strategy: downsample
    downsample:
      method: avg
      window: 1m
`
	cfg, err := ParseProcessing([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseProcessing (legacy ds): %v", err)
	}

	if cfg.Rules[0].Action != ActionDownsample {
		t.Errorf("rule action = %q, want downsample", cfg.Rules[0].Action)
	}
	if cfg.Rules[0].Method != "avg" {
		t.Errorf("rule method = %q, want avg", cfg.Rules[0].Method)
	}
	if cfg.Rules[0].Interval != "1m" {
		t.Errorf("rule interval = %q, want 1m", cfg.Rules[0].Interval)
	}
}

func TestValidateProcessingConfig_DuplicateNames(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "rule1", Input: ".*", Action: ActionDrop},
			{Name: "rule1", Input: "foo", Action: ActionDrop},
		},
	}
	err := validateProcessingConfig(&cfg)
	if err == nil {
		t.Fatal("expected error for duplicate names")
	}
	if !strings.Contains(err.Error(), "duplicate name") {
		t.Errorf("error = %q, want 'duplicate name'", err.Error())
	}
}

func TestValidateProcessingConfig_UnknownAction(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "rule1", Input: ".*", Action: "unknown"},
		},
	}
	err := validateProcessingConfig(&cfg)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestValidateProcessingConfig_InvalidInputRegex(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "rule1", Input: "[invalid", Action: ActionDrop},
		},
	}
	err := validateProcessingConfig(&cfg)
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestValidateSampleRule(t *testing.T) {
	tests := []struct {
		name    string
		rule    ProcessingRule
		wantErr bool
	}{
		{"valid head", ProcessingRule{Rate: 0.5, Method: "head"}, false},
		{"valid probabilistic", ProcessingRule{Rate: 0.1, Method: "probabilistic"}, false},
		{"empty method defaults to head", ProcessingRule{Rate: 0.5}, false},
		{"rate too high", ProcessingRule{Rate: 1.5}, true},
		{"rate negative", ProcessingRule{Rate: -0.1}, true},
		{"invalid method", ProcessingRule{Rate: 0.5, Method: "unknown"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSampleRule(&tt.rule)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSampleRule() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateAggregateRule_MutuallyExclusive(t *testing.T) {
	r := &ProcessingRule{
		Interval:   "1m",
		GroupBy:    []string{"service"},
		DropLabels: []string{"pod"},
		Functions:  []string{"sum"},
	}
	err := validateAggregateRule(r)
	if err == nil {
		t.Fatal("expected error for group_by + drop_labels")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %q, want 'mutually exclusive'", err.Error())
	}
}

func TestValidateAggregateRule_NoFunctions(t *testing.T) {
	r := &ProcessingRule{
		Interval: "1m",
		GroupBy:  []string{"service"},
	}
	err := validateAggregateRule(r)
	if err == nil {
		t.Fatal("expected error for missing functions")
	}
}

func TestParseAggFunc(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
		wantFn  AggFunc
		wantQ   int // number of quantiles
	}{
		{"sum", false, AggSum, 0},
		{"avg", false, AggAvg, 0},
		{"min", false, AggMin, 0},
		{"max", false, AggMax, 0},
		{"count", false, AggCount, 0},
		{"last", false, AggLast, 0},
		{"increase", false, AggIncrease, 0},
		{"rate", false, AggRate, 0},
		{"stddev", false, AggStddev, 0},
		{"stdvar", false, AggStdvar, 0},
		{"quantiles(0.5, 0.9, 0.99)", false, AggQuantiles, 3},
		{"quantiles(0.5)", false, AggQuantiles, 1},
		{"unknown", true, "", 0},
		{"quantiles()", true, "", 0},
		{"quantiles(1.5)", true, "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			pf, err := parseAggFunc(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseAggFunc(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if err == nil {
				if pf.Func != tt.wantFn {
					t.Errorf("func = %q, want %q", pf.Func, tt.wantFn)
				}
				if len(pf.Quantiles) != tt.wantQ {
					t.Errorf("quantiles count = %d, want %d", len(pf.Quantiles), tt.wantQ)
				}
			}
		})
	}
}

func TestValidateTransformRule_NoOperations(t *testing.T) {
	r := &ProcessingRule{}
	err := validateTransformRule(r)
	if err == nil {
		t.Fatal("expected error for no operations")
	}
}

func TestValidateTransformRule_MultipleOpsPerEntry(t *testing.T) {
	r := &ProcessingRule{
		Operations: []Operation{
			{
				Remove: []string{"foo"},
				Lower:  &LabelRef{Label: "bar"},
			},
		},
	}
	err := validateTransformRule(r)
	if err == nil {
		t.Fatal("expected error for multiple operations per entry")
	}
	if !strings.Contains(err.Error(), "exactly one") {
		t.Errorf("error = %q, want 'exactly one'", err.Error())
	}
}

func TestValidateTransformRule_EmptyOperation(t *testing.T) {
	r := &ProcessingRule{
		Operations: []Operation{
			{}, // nothing set
		},
	}
	err := validateTransformRule(r)
	if err == nil {
		t.Fatal("expected error for empty operation")
	}
}

func TestCompileOperation_Replace(t *testing.T) {
	op := Operation{
		Replace: &ReplaceOp{
			Label:       "instance",
			Pattern:     `^(\d+\.\d+\.\d+\.\d+):\d+$`,
			Replacement: "$1",
		},
	}
	co, err := compileOperation(op, 0)
	if err != nil {
		t.Fatalf("compileOperation: %v", err)
	}
	if co.compiledReplace == nil {
		t.Error("expected compiled regex for replace")
	}
}

func TestCompileOperation_Map(t *testing.T) {
	op := Operation{
		Map: &MapOp{
			Source: "namespace",
			Target: "tier",
			Values: map[string]string{
				"production": "tier-1",
				"staging":    "tier-2",
				"dev.*":      "tier-3",
			},
			Default: "tier-unknown",
		},
	}
	co, err := compileOperation(op, 0)
	if err != nil {
		t.Fatalf("compileOperation: %v", err)
	}
	if len(co.compiledMap) != 3 {
		t.Errorf("expected 3 compiled map entries, got %d", len(co.compiledMap))
	}
}

func TestCompileOperation_HashModZeroModulus(t *testing.T) {
	op := Operation{
		HashMod: &HashModOp{
			Source:  "service",
			Target:  "shard",
			Modulus: 0,
		},
	}
	_, err := compileOperation(op, 0)
	if err == nil {
		t.Fatal("expected error for modulus=0")
	}
}

func TestCompileOperation_MathUnknownOp(t *testing.T) {
	op := Operation{
		Math: &MathOp{
			Source:    "x",
			Target:    "y",
			Operation: "pow",
			Operand:   2,
		},
	}
	_, err := compileOperation(op, 0)
	if err == nil {
		t.Fatal("expected error for unknown math operation")
	}
}

func TestConvertLegacyConfig_DefaultRatePassthrough(t *testing.T) {
	old := FileConfig{
		DefaultRate: 1.0,
		Strategy:    StrategyHead,
	}
	cfg := convertLegacyConfig(old)
	// default_rate >= 1.0 means pass-through, no default rule needed.
	if len(cfg.Rules) != 0 {
		t.Errorf("expected 0 rules for default_rate=1.0, got %d", len(cfg.Rules))
	}
}

func TestParseProcessing_InvalidYAML(t *testing.T) {
	data := []byte(`
rules:
  - name: "broken
    input: [[[
`)
	_, err := ParseProcessing(data)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error = %q, want parse-related error", err.Error())
	}
}

func TestParseProcessing_UnknownActionType(t *testing.T) {
	data := []byte(`
rules:
  - name: bad-action
    input: ".*"
    action: explode
`)
	_, err := ParseProcessing(data)
	if err == nil {
		t.Fatal("expected error for unknown action type")
	}
	if !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("error = %q, want 'unknown action'", err.Error())
	}
}

func TestParseProcessing_AggregateGroupByAndDropLabels(t *testing.T) {
	data := []byte(`
rules:
  - name: bad-agg
    input: "cpu_.*"
    action: aggregate
    interval: 1m
    group_by:
      - service
    drop_labels:
      - pod
    functions:
      - sum
`)
	_, err := ParseProcessing(data)
	if err == nil {
		t.Fatal("expected error for group_by + drop_labels")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %q, want 'mutually exclusive'", err.Error())
	}
}

func TestParseProcessing_DownsampleMissingMethod(t *testing.T) {
	data := []byte(`
rules:
  - name: bad-ds
    input: "cpu_.*"
    action: downsample
    interval: 1m
`)
	_, err := ParseProcessing(data)
	if err == nil {
		t.Fatal("expected error for downsample missing method")
	}
	if !strings.Contains(err.Error(), "method") {
		t.Errorf("error = %q, want method-related error", err.Error())
	}
}

func TestParseProcessing_TransformNoOperations(t *testing.T) {
	data := []byte(`
rules:
  - name: bad-transform
    input: ".*"
    action: transform
    operations: []
`)
	_, err := ParseProcessing(data)
	if err == nil {
		t.Fatal("expected error for transform with no operations")
	}
	if !strings.Contains(err.Error(), "at least one operation") {
		t.Errorf("error = %q, want 'at least one operation'", err.Error())
	}
}

func TestConvertLegacyConfig_DefaultRateZero(t *testing.T) {
	old := FileConfig{
		DefaultRate: 0,
		Strategy:    StrategyHead,
	}
	cfg := convertLegacyConfig(old)
	if len(cfg.Rules) != 1 {
		t.Fatalf("expected 1 rule for default_rate=0, got %d", len(cfg.Rules))
	}
	if cfg.Rules[0].Action != ActionDrop {
		t.Errorf("default rule action = %q, want drop", cfg.Rules[0].Action)
	}
}
