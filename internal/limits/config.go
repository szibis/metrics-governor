package limits

import (
	"fmt"
	"os"
	"regexp"
	"time"

	"gopkg.in/yaml.v3"
)

// Action defines what to do when a limit is exceeded.
type Action string

const (
	ActionLog      Action = "log"      // Log only, don't modify data
	ActionAdaptive Action = "adaptive" // Adaptive: drop top offenders to stay within limits
	ActionDrop     Action = "drop"     // Drop all data when limit exceeded
)

// Config holds the complete limits configuration.
type Config struct {
	Defaults         *DefaultLimits `yaml:"defaults"`
	DeadRuleInterval string         `yaml:"dead_rule_interval,omitempty"`
	RequiredLabels   []string       `yaml:"required_labels,omitempty"`
	Rules            []Rule         `yaml:"rules"`

	parsedDeadRuleIntvl time.Duration
}

// reservedLimitsLabelNames are label names that cannot be used in rule ownership labels.
var reservedLimitsLabelNames = map[string]bool{
	"rule":     true,
	"__name__": true,
}

// DefaultLimits defines default limits when no rule matches.
type DefaultLimits struct {
	MaxDatapointsRate int64  `yaml:"max_datapoints_rate"` // per minute
	MaxCardinality    int64  `yaml:"max_cardinality"`
	Action            Action `yaml:"action"`
}

// Rule defines a limit rule with matching criteria.
type Rule struct {
	Name              string            `yaml:"name"`
	Match             RuleMatch         `yaml:"match"`
	MaxDatapointsRate int64             `yaml:"max_datapoints_rate"` // per minute, 0 = no limit
	MaxCardinality    int64             `yaml:"max_cardinality"`     // 0 = no limit
	Action            Action            `yaml:"action"`
	Labels            map[string]string `yaml:"labels,omitempty"` // Ownership metadata for alerting/routing

	// GroupBy specifies which labels to use for tracking top offenders.
	// Datapoints and cardinality are tracked per unique combination of these labels.
	// When limits are exceeded with action=adaptive, top offenders by these labels are dropped first.
	// Example: ["service", "env"] tracks per service+env combination.
	GroupBy []string `yaml:"group_by"`

	// Compiled regex (internal)
	metricRegex *regexp.Regexp
}

// RuleMatch defines matching criteria for a rule.
type RuleMatch struct {
	MetricName string            `yaml:"metric_name"` // exact match or regex pattern
	Labels     map[string]string `yaml:"labels"`      // label key-value pairs, "*" = wildcard
}

// LoadConfig loads limits configuration from a YAML file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read limits config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse limits config: %w", err)
	}

	// Set defaults
	if cfg.Defaults == nil {
		cfg.Defaults = &DefaultLimits{
			Action: ActionLog,
		}
	}
	if cfg.Defaults.Action == "" {
		cfg.Defaults.Action = ActionLog
	}

	// Parse dead_rule_interval.
	if cfg.DeadRuleInterval != "" {
		d, err := time.ParseDuration(cfg.DeadRuleInterval)
		if err != nil {
			return nil, fmt.Errorf("limits: invalid dead_rule_interval %q: %w", cfg.DeadRuleInterval, err)
		}
		if d < 0 {
			return nil, fmt.Errorf("limits: dead_rule_interval must be non-negative")
		}
		cfg.parsedDeadRuleIntvl = d
	}

	// Compile regex patterns and validate rules
	for i := range cfg.Rules {
		rule := &cfg.Rules[i]

		if rule.Action == "" {
			rule.Action = cfg.Defaults.Action
		}

		// Validate ownership labels.
		for k := range rule.Labels {
			if reservedLimitsLabelNames[k] {
				return nil, fmt.Errorf("limits: rule %q: label %q is reserved", rule.Name, k)
			}
		}

		// Validate required labels.
		for _, req := range cfg.RequiredLabels {
			if _, ok := rule.Labels[req]; !ok {
				return nil, fmt.Errorf("limits: rule %q is missing required label %q", rule.Name, req)
			}
		}

		if rule.Match.MetricName != "" {
			// Check if it's a regex pattern (contains special chars)
			if containsRegexChars(rule.Match.MetricName) {
				regex, err := regexp.Compile(rule.Match.MetricName)
				if err != nil {
					return nil, fmt.Errorf("invalid regex in rule %q: %w", rule.Name, err)
				}
				rule.metricRegex = regex
			}
		}
	}

	return &cfg, nil
}

// ParsedDeadRuleInterval returns the parsed dead rule detection interval.
func (c *Config) ParsedDeadRuleInterval() time.Duration {
	return c.parsedDeadRuleIntvl
}

// containsRegexChars checks if a string contains regex special characters.
func containsRegexChars(s string) bool {
	specialChars := `.*+?^${}()|[]\`
	for _, c := range s {
		for _, sc := range specialChars {
			if c == sc {
				return true
			}
		}
	}
	return false
}

// MatchesMetric checks if the rule matches a metric name.
func (r *Rule) MatchesMetric(metricName string) bool {
	if r.Match.MetricName == "" {
		return true // No metric filter
	}

	if r.metricRegex != nil {
		return r.metricRegex.MatchString(metricName)
	}

	return r.Match.MetricName == metricName
}

// MatchesLabels checks if the rule matches a set of labels.
func (r *Rule) MatchesLabels(labels map[string]string) bool {
	if len(r.Match.Labels) == 0 {
		return true // No label filter
	}

	for key, pattern := range r.Match.Labels {
		value, exists := labels[key]
		if !exists {
			return false
		}
		if pattern != "*" && pattern != value {
			return false
		}
	}

	return true
}

// HasLabelMatchers returns true if the rule has label-based match criteria.
func (r *Rule) HasLabelMatchers() bool {
	return len(r.Match.Labels) > 0
}

// Matches checks if the rule matches a metric name and labels.
func (r *Rule) Matches(metricName string, labels map[string]string) bool {
	return r.MatchesMetric(metricName) && r.MatchesLabels(labels)
}

// HasAnyLabelMatchers returns true if any rule has label matchers.
func (c *Config) HasAnyLabelMatchers() bool {
	for i := range c.Rules {
		if c.Rules[i].HasLabelMatchers() {
			return true
		}
	}
	return false
}
