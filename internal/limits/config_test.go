package limits

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// Create temp file with valid config
	content := `
defaults:
  max_datapoints_rate: 1000000
  max_cardinality: 100000
  action: log

rules:
  - name: "test-rule"
    match:
      metric_name: "http_request_.*"
      labels:
        service: "api"
        env: "*"
    max_datapoints_rate: 50000
    max_cardinality: 2000
    action: adaptive
    group_by: ["service", "env"]
`
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "limits.yaml")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	cfg, err := LoadConfig(tmpFile)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Defaults == nil {
		t.Fatal("expected defaults to be set")
	}
	if cfg.Defaults.MaxDatapointsRate != 1000000 {
		t.Errorf("expected max_datapoints_rate 1000000, got %d", cfg.Defaults.MaxDatapointsRate)
	}
	if cfg.Defaults.MaxCardinality != 100000 {
		t.Errorf("expected max_cardinality 100000, got %d", cfg.Defaults.MaxCardinality)
	}
	if cfg.Defaults.Action != ActionLog {
		t.Errorf("expected action 'log', got '%s'", cfg.Defaults.Action)
	}

	if len(cfg.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(cfg.Rules))
	}

	rule := cfg.Rules[0]
	if rule.Name != "test-rule" {
		t.Errorf("expected rule name 'test-rule', got '%s'", rule.Name)
	}
	if rule.Match.MetricName != "http_request_.*" {
		t.Errorf("expected metric_name 'http_request_.*', got '%s'", rule.Match.MetricName)
	}
	if rule.metricRegex == nil {
		t.Error("expected regex to be compiled")
	}
	if rule.Action != ActionAdaptive {
		t.Errorf("expected action 'adaptive', got '%s'", rule.Action)
	}
	if len(rule.GroupBy) != 2 {
		t.Errorf("expected 2 group_by labels, got %d", len(rule.GroupBy))
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	// Create temp file with minimal config (no defaults)
	content := `
rules:
  - name: "test-rule"
    match:
      metric_name: "test"
`
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "limits.yaml")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	cfg, err := LoadConfig(tmpFile)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.Defaults == nil {
		t.Fatal("expected defaults to be set")
	}
	if cfg.Defaults.Action != ActionLog {
		t.Errorf("expected default action 'log', got '%s'", cfg.Defaults.Action)
	}
	// Rule should inherit default action
	if cfg.Rules[0].Action != ActionLog {
		t.Errorf("expected rule to inherit default action 'log', got '%s'", cfg.Rules[0].Action)
	}
}

func TestLoadConfigInvalidRegex(t *testing.T) {
	content := `
rules:
  - name: "invalid-regex"
    match:
      metric_name: "[invalid"
`
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "limits.yaml")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	_, err := LoadConfig(tmpFile)
	if err == nil {
		t.Error("expected error for invalid regex")
	}
}

func TestLoadConfigFileNotFound(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/limits.yaml")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	content := `invalid: yaml: content: [}`
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "limits.yaml")
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}

	_, err := LoadConfig(tmpFile)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestContainsRegexChars(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"simple_metric", false},
		{"http_request_.*", true},
		{"metric.name", true},
		{"metric+name", true},
		{"metric?name", true},
		{"metric^name", true},
		{"metric$name", true},
		{"metric{1}", true},
		{"metric(group)", true},
		{"metric|other", true},
		{"metric[0]", true},
		{"metric\\name", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := containsRegexChars(tt.input)
			if result != tt.expected {
				t.Errorf("containsRegexChars(%q) = %v, expected %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestRuleMatchesMetric(t *testing.T) {
	tests := []struct {
		name       string
		ruleMatch  string
		metricName string
		expected   bool
	}{
		{"empty pattern matches all", "", "any_metric", true},
		{"exact match", "http_requests_total", "http_requests_total", true},
		{"exact match fail", "http_requests_total", "http_requests_count", false},
		{"regex match", "http_request_.*", "http_request_duration", true},
		{"regex match fail", "http_request_.*", "grpc_request_duration", false},
		{"complex regex", "^(http|grpc)_.*_total$", "http_requests_total", true},
		{"complex regex match", "^(http|grpc)_.*_total$", "grpc_calls_total", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := &Rule{
				Match: RuleMatch{MetricName: tt.ruleMatch},
			}
			// Compile regex if needed
			if tt.ruleMatch != "" && containsRegexChars(tt.ruleMatch) {
				cfg := &Config{Rules: []Rule{*rule}}
				LoadConfigFromStruct(cfg) // This compiles regexes
				rule = &cfg.Rules[0]
			}

			result := rule.MatchesMetric(tt.metricName)
			if result != tt.expected {
				t.Errorf("MatchesMetric(%q) = %v, expected %v", tt.metricName, result, tt.expected)
			}
		})
	}
}

// Helper function to compile regexes in a config
func LoadConfigFromStruct(cfg *Config) {
	if cfg.Defaults == nil {
		cfg.Defaults = &DefaultLimits{Action: ActionLog}
	}
	if cfg.Defaults.Action == "" {
		cfg.Defaults.Action = ActionLog
	}

	for i := range cfg.Rules {
		rule := &cfg.Rules[i]
		if rule.Action == "" {
			rule.Action = cfg.Defaults.Action
		}
		if rule.Match.MetricName != "" && containsRegexChars(rule.Match.MetricName) {
			rule.metricRegex, _ = compileRegex(rule.Match.MetricName)
		}
	}
}

func compileRegex(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile(pattern)
}

func TestRuleMatchesLabels(t *testing.T) {
	tests := []struct {
		name       string
		ruleLabels map[string]string
		labels     map[string]string
		expected   bool
	}{
		{
			name:       "empty rule labels matches all",
			ruleLabels: nil,
			labels:     map[string]string{"service": "api", "env": "prod"},
			expected:   true,
		},
		{
			name:       "exact match",
			ruleLabels: map[string]string{"service": "api"},
			labels:     map[string]string{"service": "api", "env": "prod"},
			expected:   true,
		},
		{
			name:       "exact match fail",
			ruleLabels: map[string]string{"service": "api"},
			labels:     map[string]string{"service": "web", "env": "prod"},
			expected:   false,
		},
		{
			name:       "wildcard match",
			ruleLabels: map[string]string{"service": "*", "env": "prod"},
			labels:     map[string]string{"service": "any-service", "env": "prod"},
			expected:   true,
		},
		{
			name:       "missing label",
			ruleLabels: map[string]string{"service": "api", "env": "prod"},
			labels:     map[string]string{"service": "api"},
			expected:   false,
		},
		{
			name:       "multiple labels match",
			ruleLabels: map[string]string{"service": "api", "env": "prod", "cluster": "us-east"},
			labels:     map[string]string{"service": "api", "env": "prod", "cluster": "us-east", "extra": "value"},
			expected:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := &Rule{
				Match: RuleMatch{Labels: tt.ruleLabels},
			}

			result := rule.MatchesLabels(tt.labels)
			if result != tt.expected {
				t.Errorf("MatchesLabels() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestRuleMatches(t *testing.T) {
	rule := &Rule{
		Match: RuleMatch{
			MetricName: "http_request_.*",
			Labels:     map[string]string{"env": "prod"},
		},
	}
	// Compile regex
	cfg := &Config{Rules: []Rule{*rule}}
	LoadConfigFromStruct(cfg)
	rule = &cfg.Rules[0]

	tests := []struct {
		name       string
		metricName string
		labels     map[string]string
		expected   bool
	}{
		{
			name:       "both match",
			metricName: "http_request_duration",
			labels:     map[string]string{"env": "prod"},
			expected:   true,
		},
		{
			name:       "metric matches, labels don't",
			metricName: "http_request_duration",
			labels:     map[string]string{"env": "dev"},
			expected:   false,
		},
		{
			name:       "labels match, metric doesn't",
			metricName: "grpc_request_duration",
			labels:     map[string]string{"env": "prod"},
			expected:   false,
		},
		{
			name:       "neither match",
			metricName: "grpc_request_duration",
			labels:     map[string]string{"env": "dev"},
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rule.Matches(tt.metricName, tt.labels)
			if result != tt.expected {
				t.Errorf("Matches() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
