package sampling

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/szibis/metrics-governor/internal/ruleactivity"
	"gopkg.in/yaml.v3"
)

// Action defines the type of processing action.
type Action string

const (
	ActionSample     Action = "sample"
	ActionDownsample Action = "downsample"
	ActionAggregate  Action = "aggregate"
	ActionTransform  Action = "transform"
	ActionClassify   Action = "classify"
	ActionDrop       Action = "drop"
)

// reservedLabelNames are label names reserved for internal use and cannot be
// used in rule ownership labels.
var reservedLabelNames = map[string]bool{
	"rule":     true,
	"action":   true,
	"__name__": true,
}

// AggFunc identifies a cross-series aggregation function.
type AggFunc string

const (
	AggSum       AggFunc = "sum"
	AggAvg       AggFunc = "avg"
	AggMin       AggFunc = "min"
	AggMax       AggFunc = "max"
	AggCount     AggFunc = "count"
	AggLast      AggFunc = "last"
	AggIncrease  AggFunc = "increase"
	AggRate      AggFunc = "rate"
	AggStddev    AggFunc = "stddev"
	AggStdvar    AggFunc = "stdvar"
	AggQuantiles AggFunc = "quantiles" // parameterized: quantiles(0.5, 0.9, 0.99)
)

// ProcessingConfig is the top-level processing rules configuration.
type ProcessingConfig struct {
	StalenessInterval string           `yaml:"staleness_interval,omitempty"`
	DeadRuleInterval  string           `yaml:"dead_rule_interval,omitempty"`
	RequiredLabels    []string         `yaml:"required_labels,omitempty"`
	Rules             []ProcessingRule `yaml:"rules"`

	parsedStaleness     time.Duration
	parsedDeadRuleIntvl time.Duration
}

// ProcessingRule defines a single processing rule.
type ProcessingRule struct {
	Name        string            `yaml:"name"`
	Input       string            `yaml:"input"`
	InputLabels map[string]string `yaml:"input_labels,omitempty"`
	Action      Action            `yaml:"action"`
	Labels      map[string]string `yaml:"labels,omitempty"` // Ownership metadata for alerting/routing

	// Sample fields
	Rate   float64 `yaml:"rate,omitempty"`
	Method string  `yaml:"method,omitempty"` // head | probabilistic

	// Downsample fields
	Interval       string  `yaml:"interval,omitempty"`
	Resolution     int     `yaml:"resolution,omitempty"`      // LTTB: points per window
	Deviation      float64 `yaml:"deviation,omitempty"`       // SDT: deadband threshold
	MinRate        float64 `yaml:"min_rate,omitempty"`        // Adaptive: min keep rate
	MaxRate        float64 `yaml:"max_rate,omitempty"`        // Adaptive: max keep rate
	VarianceWindow int     `yaml:"variance_window,omitempty"` // Adaptive: samples for CV calc

	// Aggregate fields
	Output     string   `yaml:"output,omitempty"`
	GroupBy    []string `yaml:"group_by,omitempty"`
	DropLabels []string `yaml:"drop_labels,omitempty"`
	Functions  []string `yaml:"functions,omitempty"`
	KeepInput  bool     `yaml:"keep_input,omitempty"`

	// Transform fields
	When       []Condition `yaml:"when,omitempty"`
	Operations []Operation `yaml:"operations,omitempty"`

	// Classify fields
	Classify *ClassifyConfig `yaml:"classify,omitempty"`

	// Compiled (not serialized)
	compiledInput    *regexp.Regexp
	compiledLabels   map[string]*regexp.Regexp
	parsedInterval   time.Duration
	parsedFunctions  []parsedAggFunc
	dsConfig         *DownsampleConfig
	compiledOps      []compiledOperation
	compiledClassify *compiledClassifyConfig // pre-compiled classify config
	metrics          ruleMetrics             // pre-resolved Prometheus counters
	inputPrefix      string                  // literal prefix for fast regex short-circuit
}

// ClassifyConfig defines the classify action configuration.
type ClassifyConfig struct {
	Chains      []ClassifyChain   `yaml:"chains,omitempty"`
	Mappings    []ClassifyMapping `yaml:"mappings,omitempty"`
	RemoveAfter []string          `yaml:"remove_after,omitempty"`
}

// ClassifyChain is an ordered chain entry: first match wins, sets multiple labels.
type ClassifyChain struct {
	When []Condition       `yaml:"when"`
	Set  map[string]string `yaml:"set"` // Supports ${interpolation}
}

// ClassifyMapping maps source label value(s) to a target label via regex lookup.
type ClassifyMapping struct {
	Source    string            `yaml:"source,omitempty"`
	Sources   []string          `yaml:"sources,omitempty"`
	Separator string            `yaml:"separator,omitempty"` // Default ":"
	Target    string            `yaml:"target"`
	Values    map[string]string `yaml:"values"`
	Default   string            `yaml:"default,omitempty"`
}

// compiledClassifyConfig holds pre-compiled classify state.
type compiledClassifyConfig struct {
	chains      []compiledClassifyChain
	mappings    []compiledClassifyMapping
	removeAfter []string
}

// compiledClassifyChain holds a pre-compiled chain entry.
type compiledClassifyChain struct {
	conditions []Condition // pre-compiled (matches/not_matches regex)
	set        map[string]string
}

// compiledClassifyMapping holds a pre-compiled mapping entry.
type compiledClassifyMapping struct {
	sources   []string // resolved source list (from Source or Sources)
	separator string
	target    string
	entries   []compiledMapEntry
	dflt      string
}

// parsedAggFunc holds a parsed aggregation function with optional quantile parameters.
type parsedAggFunc struct {
	Func      AggFunc
	Quantiles []float64 // only for AggQuantiles
}

// Condition defines a matching condition for transform rules.
type Condition struct {
	Label      string `yaml:"label"`
	Equals     string `yaml:"equals,omitempty"`
	Matches    string `yaml:"matches,omitempty"`
	Contains   string `yaml:"contains,omitempty"`
	NotMatches string `yaml:"not_matches,omitempty"`

	compiledMatches    *regexp.Regexp
	compiledNotMatches *regexp.Regexp
}

// Operation defines a single transform operation.
// Only one field should be set per operation (YAML inline union).
type Operation struct {
	Remove  []string   `yaml:"remove,omitempty"`
	Set     *SetOp     `yaml:"set,omitempty"`
	Rename  *RenameOp  `yaml:"rename,omitempty"`
	Copy    *CopyOp    `yaml:"copy,omitempty"`
	Replace *ReplaceOp `yaml:"replace,omitempty"`
	Extract *ExtractOp `yaml:"extract,omitempty"`
	HashMod *HashModOp `yaml:"hash_mod,omitempty"`
	Lower   *LabelRef  `yaml:"lower,omitempty"`
	Upper   *LabelRef  `yaml:"upper,omitempty"`
	Concat  *ConcatOp  `yaml:"concat,omitempty"`
	Map     *MapOp     `yaml:"map,omitempty"`
	Math    *MathOp    `yaml:"math,omitempty"`
}

// SetOp sets a label to a value (supports ${interpolation}).
type SetOp struct {
	Label string `yaml:"label"`
	Value string `yaml:"value"`
}

// RenameOp renames a label.
type RenameOp struct {
	Source string `yaml:"source"`
	Target string `yaml:"target"`
}

// CopyOp copies a label value to a new name.
type CopyOp struct {
	Source string `yaml:"source"`
	Target string `yaml:"target"`
}

// ReplaceOp does regex replacement on a label value.
type ReplaceOp struct {
	Label       string `yaml:"label"`
	Pattern     string `yaml:"pattern"`
	Replacement string `yaml:"replacement"`
}

// ExtractOp extracts a regex capture group to a new label.
type ExtractOp struct {
	Source  string `yaml:"source"`
	Target  string `yaml:"target"`
	Pattern string `yaml:"pattern"`
	Group   int    `yaml:"group"`
}

// HashModOp hashes a label value mod N to a new label.
type HashModOp struct {
	Source  string `yaml:"source"`
	Target  string `yaml:"target"`
	Modulus uint64 `yaml:"modulus"`
}

// LabelRef references a single label (for lower/upper operations).
type LabelRef struct {
	Label string `yaml:"label"`
}

// ConcatOp joins multiple label values with a separator.
type ConcatOp struct {
	Sources   []string `yaml:"sources"`
	Target    string   `yaml:"target"`
	Separator string   `yaml:"separator"`
}

// MapOp maps a label value via lookup (supports regex keys).
type MapOp struct {
	Source  string            `yaml:"source"`
	Target  string            `yaml:"target"`
	Values  map[string]string `yaml:"values"`
	Default string            `yaml:"default"`
}

// MathOp performs math on a numeric label value.
type MathOp struct {
	Source    string  `yaml:"source"`
	Target    string  `yaml:"target"`
	Operation string  `yaml:"operation"` // add, sub, mul, div, mod
	Operand   float64 `yaml:"operand"`
}

// deadRuleState is an alias for ruleactivity.Activity.
// Kept behind a pointer so ProcessingRule remains safely copyable.
type deadRuleState = ruleactivity.Activity

// ruleMetrics holds pre-resolved Prometheus counters for a single rule.
// By caching WithLabelValues results at config compile time, the hot path
// avoids hashing label strings on every datapoint (saves ~33% CPU).
type ruleMetrics struct {
	evalCounter     prometheus.Counter
	inputCounter    prometheus.Counter
	outputCounter   prometheus.Counter // for actions with no function label
	droppedCounter  prometheus.Counter
	transformAdd    prometheus.Counter
	transformRemove prometheus.Counter
	transformMod    prometheus.Counter

	// Classify-specific counters
	classifyChainMatch   prometheus.Counter
	classifyChainNoMatch prometheus.Counter
	classifyLabelsSet    prometheus.Counter

	// Dead rule tracking (always active, regardless of scanner config).
	// Behind a pointer so ruleMetrics (and ProcessingRule) remain copyable.
	dead *deadRuleState
}

// compiledOperation holds pre-compiled regex for a transform operation.
type compiledOperation struct {
	op              Operation
	compiledReplace *regexp.Regexp     // for Replace
	compiledExtract *regexp.Regexp     // for Extract
	compiledMap     []compiledMapEntry // for Map
}

// compiledMapEntry holds a pre-compiled regex key for Map operations.
type compiledMapEntry struct {
	pattern *regexp.Regexp
	value   string
}

// LoadProcessingFile loads processing config from a YAML file.
func LoadProcessingFile(path string) (ProcessingConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ProcessingConfig{}, fmt.Errorf("processing: read config: %w", err)
	}
	return ParseProcessing(data)
}

// ParseProcessing parses processing config from YAML bytes.
// Auto-detects old format (has default_rate/strategy) vs new format (has rules with action).
func ParseProcessing(data []byte) (ProcessingConfig, error) {
	// Try to detect old format by probing for legacy fields.
	var probe struct {
		DefaultRate *float64 `yaml:"default_rate"`
		Strategy    string   `yaml:"strategy"`
		Rules       []struct {
			Action string `yaml:"action"`
		} `yaml:"rules"`
	}
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return ProcessingConfig{}, fmt.Errorf("processing: parse config: %w", err)
	}

	// Old format: has default_rate or strategy but no rules with action field,
	// or rules without action field.
	isLegacy := false
	if probe.DefaultRate != nil || probe.Strategy != "" {
		isLegacy = true
	}
	if !isLegacy && len(probe.Rules) > 0 && probe.Rules[0].Action == "" {
		isLegacy = true
	}

	if isLegacy {
		var oldCfg FileConfig
		if err := yaml.Unmarshal(data, &oldCfg); err != nil {
			return ProcessingConfig{}, fmt.Errorf("processing: parse legacy config: %w", err)
		}
		return convertLegacyConfig(oldCfg), nil
	}

	var cfg ProcessingConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return ProcessingConfig{}, fmt.Errorf("processing: parse config: %w", err)
	}

	if err := validateProcessingConfig(&cfg); err != nil {
		return ProcessingConfig{}, err
	}

	return cfg, nil
}

// convertLegacyConfig converts old FileConfig to ProcessingConfig.
func convertLegacyConfig(old FileConfig) ProcessingConfig {
	cfg := ProcessingConfig{
		StalenessInterval: "10m",
	}

	for _, r := range old.Rules {
		pr := ProcessingRule{
			Name:  r.Name,
			Input: r.Match["__name__"],
		}

		// Copy non-__name__ matchers to input_labels.
		if len(r.Match) > 1 || (len(r.Match) == 1 && r.Match["__name__"] == "") {
			pr.InputLabels = make(map[string]string)
			for k, v := range r.Match {
				if k != "__name__" {
					pr.InputLabels[k] = v
				}
			}
		}

		// If no __name__ match, use catch-all.
		if pr.Input == "" {
			pr.Input = ".*"
		}

		strategy := r.Strategy
		if strategy == "" {
			strategy = old.Strategy
		}

		switch strategy {
		case StrategyDownsample:
			pr.Action = ActionDownsample
			if r.Downsample != nil {
				pr.Method = string(r.Downsample.Method)
				pr.Interval = r.Downsample.Window
				pr.Resolution = r.Downsample.Resolution
				pr.Deviation = r.Downsample.Deviation
				pr.MinRate = r.Downsample.MinRate
				pr.MaxRate = r.Downsample.MaxRate
				pr.VarianceWindow = r.Downsample.VarianceWindow
			}
		default:
			if r.Rate <= 0 {
				pr.Action = ActionDrop
			} else {
				pr.Action = ActionSample
				pr.Rate = r.Rate
				if strategy == StrategyProbabilistic {
					pr.Method = "probabilistic"
				} else {
					pr.Method = "head"
				}
			}
		}

		cfg.Rules = append(cfg.Rules, pr)
	}

	// Add default rule if default_rate is set.
	if old.DefaultRate > 0 && old.DefaultRate < 1.0 {
		method := "head"
		if old.Strategy == StrategyProbabilistic {
			method = "probabilistic"
		}
		cfg.Rules = append(cfg.Rules, ProcessingRule{
			Name:   "default",
			Input:  ".*",
			Action: ActionSample,
			Rate:   old.DefaultRate,
			Method: method,
		})
	} else if old.DefaultRate <= 0 {
		cfg.Rules = append(cfg.Rules, ProcessingRule{
			Name:   "default",
			Input:  ".*",
			Action: ActionDrop,
		})
	}
	// default_rate >= 1.0 means pass-through, no rule needed.

	return cfg
}

// validateProcessingConfig validates and compiles all rules in the config.
func validateProcessingConfig(cfg *ProcessingConfig) error {
	if cfg.StalenessInterval != "" {
		d, err := time.ParseDuration(cfg.StalenessInterval)
		if err != nil {
			return fmt.Errorf("processing: invalid staleness_interval %q: %w", cfg.StalenessInterval, err)
		}
		if d <= 0 {
			return fmt.Errorf("processing: staleness_interval must be positive")
		}
		cfg.parsedStaleness = d
	} else {
		cfg.parsedStaleness = 10 * time.Minute // default
	}

	// Parse dead_rule_interval (0 = scanner disabled, timestamps still tracked).
	if cfg.DeadRuleInterval != "" {
		d, err := time.ParseDuration(cfg.DeadRuleInterval)
		if err != nil {
			return fmt.Errorf("processing: invalid dead_rule_interval %q: %w", cfg.DeadRuleInterval, err)
		}
		if d < 0 {
			return fmt.Errorf("processing: dead_rule_interval must be non-negative")
		}
		cfg.parsedDeadRuleIntvl = d
	}

	names := make(map[string]bool, len(cfg.Rules))
	for i := range cfg.Rules {
		r := &cfg.Rules[i]

		// Validate name uniqueness.
		if r.Name == "" {
			return fmt.Errorf("processing: rule %d: name is required", i)
		}
		if names[r.Name] {
			return fmt.Errorf("processing: rule %d: duplicate name %q", i, r.Name)
		}
		names[r.Name] = true

		// Validate ownership labels.
		for k := range r.Labels {
			if reservedLabelNames[k] {
				return fmt.Errorf("processing: rule %q: label %q is reserved", r.Name, k)
			}
		}

		// Validate required labels.
		for _, req := range cfg.RequiredLabels {
			if _, ok := r.Labels[req]; !ok {
				return fmt.Errorf("processing: rule %q is missing required label %q", r.Name, req)
			}
		}

		// Validate input regex.
		if r.Input == "" {
			return fmt.Errorf("processing: rule %q: input is required", r.Name)
		}
		re, err := regexp.Compile("^(?:" + r.Input + ")$")
		if err != nil {
			return fmt.Errorf("processing: rule %q: invalid input regex %q: %w", r.Name, r.Input, err)
		}
		r.compiledInput = re

		// Compile input_labels.
		if len(r.InputLabels) > 0 {
			r.compiledLabels = make(map[string]*regexp.Regexp, len(r.InputLabels))
			for k, v := range r.InputLabels {
				lre, err := regexp.Compile("^(?:" + v + ")$")
				if err != nil {
					return fmt.Errorf("processing: rule %q: invalid label regex for %q: %w", r.Name, k, err)
				}
				r.compiledLabels[k] = lre
			}
		}

		// Validate per-action fields.
		switch r.Action {
		case ActionSample:
			if err := validateSampleRule(r); err != nil {
				return fmt.Errorf("processing: rule %q: %w", r.Name, err)
			}
		case ActionDownsample:
			if err := validateDownsampleRule(r); err != nil {
				return fmt.Errorf("processing: rule %q: %w", r.Name, err)
			}
		case ActionAggregate:
			if err := validateAggregateRule(r); err != nil {
				return fmt.Errorf("processing: rule %q: %w", r.Name, err)
			}
		case ActionTransform:
			if err := validateTransformRule(r); err != nil {
				return fmt.Errorf("processing: rule %q: %w", r.Name, err)
			}
		case ActionClassify:
			if err := validateClassifyRule(r); err != nil {
				return fmt.Errorf("processing: rule %q: %w", r.Name, err)
			}
		case ActionDrop:
			// No additional validation needed.
		default:
			return fmt.Errorf("processing: rule %q: unknown action %q", r.Name, r.Action)
		}

		// Pre-resolve Prometheus counter references (avoids WithLabelValues hashing in hot path).
		r.metrics = ruleMetrics{
			evalCounter:    processingRuleEvaluationsTotal.WithLabelValues(r.Name, string(r.Action)),
			inputCounter:   processingRuleInputTotal.WithLabelValues(r.Name, string(r.Action)),
			outputCounter:  processingRuleOutputTotal.WithLabelValues(r.Name, string(r.Action), ""),
			droppedCounter: processingRuleDroppedTotal.WithLabelValues(r.Name, string(r.Action)),
		}
		if r.Action == ActionTransform {
			r.metrics.transformAdd = processingTransformLabelsAddedTotal.WithLabelValues(r.Name)
			r.metrics.transformRemove = processingTransformLabelsRemovedTotal.WithLabelValues(r.Name)
			r.metrics.transformMod = processingTransformLabelsModifiedTotal.WithLabelValues(r.Name)
		}
		if r.Action == ActionClassify {
			r.metrics.classifyChainMatch = processingClassifyChainMatchesTotal.WithLabelValues(r.Name)
			r.metrics.classifyChainNoMatch = processingClassifyChainNoMatchTotal.WithLabelValues(r.Name)
			r.metrics.classifyLabelsSet = processingClassifyLabelsSetTotal.WithLabelValues(r.Name)
		}

		// Initialize dead rule tracking state.
		r.metrics.dead = ruleactivity.NewActivity()

		// Extract literal prefix from input regex for fast short-circuit matching.
		r.inputPrefix = extractLiteralPrefix(r.Input)
	}

	return nil
}

// validateClassifyRule validates and compiles a classify rule.
func validateClassifyRule(r *ProcessingRule) error {
	if r.Classify == nil {
		return fmt.Errorf("classify config is required for classify action")
	}
	cc := r.Classify
	if len(cc.Chains) == 0 && len(cc.Mappings) == 0 {
		return fmt.Errorf("classify must have at least one chain or mapping")
	}

	compiled := &compiledClassifyConfig{
		removeAfter: cc.RemoveAfter,
	}

	// Compile chains.
	for i, chain := range cc.Chains {
		if len(chain.When) == 0 {
			return fmt.Errorf("classify.chains[%d]: when conditions are required", i)
		}
		if len(chain.Set) == 0 {
			return fmt.Errorf("classify.chains[%d]: set labels are required", i)
		}

		// Compile conditions (reuse the same pattern as transform when conditions).
		compiledConds := make([]Condition, len(chain.When))
		for j := range chain.When {
			c := chain.When[j]
			if c.Label == "" {
				return fmt.Errorf("classify.chains[%d].when[%d]: label is required", i, j)
			}
			if c.Matches != "" {
				re, err := regexp.Compile("^(?:" + c.Matches + ")$")
				if err != nil {
					return fmt.Errorf("classify.chains[%d].when[%d]: invalid matches regex %q: %w", i, j, c.Matches, err)
				}
				c.compiledMatches = re
			}
			if c.NotMatches != "" {
				re, err := regexp.Compile("^(?:" + c.NotMatches + ")$")
				if err != nil {
					return fmt.Errorf("classify.chains[%d].when[%d]: invalid not_matches regex %q: %w", i, j, c.NotMatches, err)
				}
				c.compiledNotMatches = re
			}
			compiledConds[j] = c
		}

		compiled.chains = append(compiled.chains, compiledClassifyChain{
			conditions: compiledConds,
			set:        chain.Set,
		})
	}

	// Compile mappings.
	for i, m := range cc.Mappings {
		if m.Target == "" {
			return fmt.Errorf("classify.mappings[%d]: target is required", i)
		}

		// Resolve sources.
		var sources []string
		if m.Source != "" {
			sources = []string{m.Source}
		} else if len(m.Sources) > 0 {
			sources = m.Sources
		} else {
			return fmt.Errorf("classify.mappings[%d]: source or sources is required", i)
		}

		if len(m.Values) == 0 {
			return fmt.Errorf("classify.mappings[%d]: values are required", i)
		}

		separator := m.Separator
		if separator == "" {
			separator = ":"
		}

		// Pre-compile regex values.
		var entries []compiledMapEntry
		for pattern, value := range m.Values {
			re, err := regexp.Compile("^(?:" + pattern + ")$")
			if err != nil {
				return fmt.Errorf("classify.mappings[%d]: invalid value regex %q: %w", i, pattern, err)
			}
			entries = append(entries, compiledMapEntry{pattern: re, value: value})
		}

		compiled.mappings = append(compiled.mappings, compiledClassifyMapping{
			sources:   sources,
			separator: separator,
			target:    m.Target,
			entries:   entries,
			dflt:      m.Default,
		})
	}

	r.compiledClassify = compiled
	return nil
}

// extractLiteralPrefix returns the leading literal characters from a regex pattern.
// E.g. "http_request_.*" → "http_request_", ".*" → "", "(foo|bar)" → "".
func extractLiteralPrefix(pattern string) string {
	var prefix strings.Builder
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		// Stop at any regex metacharacter.
		switch c {
		case '.', '*', '+', '?', '[', ']', '(', ')', '{', '}', '|', '^', '$', '\\':
			return prefix.String()
		default:
			prefix.WriteByte(c)
		}
	}
	return prefix.String()
}

func validateSampleRule(r *ProcessingRule) error {
	if r.Rate < 0 || r.Rate > 1.0 {
		return fmt.Errorf("rate must be between 0.0 and 1.0, got %f", r.Rate)
	}
	if r.Method == "" {
		r.Method = "head"
	}
	if r.Method != "head" && r.Method != "probabilistic" {
		return fmt.Errorf("method must be 'head' or 'probabilistic', got %q", r.Method)
	}
	return nil
}

func validateDownsampleRule(r *ProcessingRule) error {
	if r.Method == "" {
		return fmt.Errorf("method is required for downsample action")
	}

	dsMethod := DownsampleMethod(r.Method)
	if !validMethods[dsMethod] {
		return fmt.Errorf("unknown downsample method %q", r.Method)
	}

	dc := &DownsampleConfig{
		Method:         dsMethod,
		Window:         r.Interval,
		Resolution:     r.Resolution,
		Deviation:      r.Deviation,
		MinRate:        r.MinRate,
		MaxRate:        r.MaxRate,
		VarianceWindow: r.VarianceWindow,
	}
	if err := dc.validate(); err != nil {
		return err
	}
	r.dsConfig = dc
	r.parsedInterval = dc.parsedWindow
	return nil
}

func validateAggregateRule(r *ProcessingRule) error {
	if r.Interval == "" {
		return fmt.Errorf("interval is required for aggregate action")
	}
	d, err := time.ParseDuration(r.Interval)
	if err != nil {
		return fmt.Errorf("invalid interval %q: %w", r.Interval, err)
	}
	if d <= 0 {
		return fmt.Errorf("interval must be positive")
	}
	r.parsedInterval = d

	if len(r.GroupBy) > 0 && len(r.DropLabels) > 0 {
		return fmt.Errorf("group_by and drop_labels are mutually exclusive")
	}

	if len(r.Functions) == 0 {
		return fmt.Errorf("at least one function is required for aggregate action")
	}

	for _, f := range r.Functions {
		pf, err := parseAggFunc(f)
		if err != nil {
			return err
		}
		r.parsedFunctions = append(r.parsedFunctions, pf)
	}

	return nil
}

func parseAggFunc(s string) (parsedAggFunc, error) {
	s = strings.TrimSpace(s)

	// Check for quantiles(0.5, 0.9, ...) syntax.
	if strings.HasPrefix(s, "quantiles(") && strings.HasSuffix(s, ")") {
		inner := s[len("quantiles(") : len(s)-1]
		parts := strings.Split(inner, ",")
		var quantiles []float64
		for _, p := range parts {
			p = strings.TrimSpace(p)
			var q float64
			if _, err := fmt.Sscanf(p, "%f", &q); err != nil {
				return parsedAggFunc{}, fmt.Errorf("invalid quantile value %q: %w", p, err)
			}
			if q < 0 || q > 1 {
				return parsedAggFunc{}, fmt.Errorf("quantile must be between 0 and 1, got %f", q)
			}
			quantiles = append(quantiles, q)
		}
		if len(quantiles) == 0 {
			return parsedAggFunc{}, fmt.Errorf("quantiles() requires at least one value")
		}
		return parsedAggFunc{Func: AggQuantiles, Quantiles: quantiles}, nil
	}

	switch AggFunc(s) {
	case AggSum, AggAvg, AggMin, AggMax, AggCount, AggLast, AggIncrease, AggRate, AggStddev, AggStdvar:
		return parsedAggFunc{Func: AggFunc(s)}, nil
	default:
		return parsedAggFunc{}, fmt.Errorf("unknown aggregation function %q", s)
	}
}

func validateTransformRule(r *ProcessingRule) error {
	if len(r.Operations) == 0 {
		return fmt.Errorf("at least one operation is required for transform action")
	}

	// Compile conditions.
	for i := range r.When {
		c := &r.When[i]
		if c.Label == "" {
			return fmt.Errorf("when[%d]: label is required", i)
		}
		if c.Matches != "" {
			re, err := regexp.Compile("^(?:" + c.Matches + ")$")
			if err != nil {
				return fmt.Errorf("when[%d]: invalid matches regex %q: %w", i, c.Matches, err)
			}
			c.compiledMatches = re
		}
		if c.NotMatches != "" {
			re, err := regexp.Compile("^(?:" + c.NotMatches + ")$")
			if err != nil {
				return fmt.Errorf("when[%d]: invalid not_matches regex %q: %w", i, c.NotMatches, err)
			}
			c.compiledNotMatches = re
		}
	}

	// Compile operations.
	for i, op := range r.Operations {
		cop, err := compileOperation(op, i)
		if err != nil {
			return err
		}
		r.compiledOps = append(r.compiledOps, cop)
	}

	return nil
}

func compileOperation(op Operation, idx int) (compiledOperation, error) {
	co := compiledOperation{op: op}

	// Count how many fields are set to validate exactly one.
	setCount := 0
	if len(op.Remove) > 0 {
		setCount++
	}
	if op.Set != nil {
		setCount++
		if op.Set.Label == "" {
			return co, fmt.Errorf("operation[%d]: set.label is required", idx)
		}
	}
	if op.Rename != nil {
		setCount++
		if op.Rename.Source == "" || op.Rename.Target == "" {
			return co, fmt.Errorf("operation[%d]: rename requires source and target", idx)
		}
	}
	if op.Copy != nil {
		setCount++
		if op.Copy.Source == "" || op.Copy.Target == "" {
			return co, fmt.Errorf("operation[%d]: copy requires source and target", idx)
		}
	}
	if op.Replace != nil {
		setCount++
		if op.Replace.Label == "" || op.Replace.Pattern == "" {
			return co, fmt.Errorf("operation[%d]: replace requires label and pattern", idx)
		}
		re, err := regexp.Compile(op.Replace.Pattern)
		if err != nil {
			return co, fmt.Errorf("operation[%d]: invalid replace pattern %q: %w", idx, op.Replace.Pattern, err)
		}
		co.compiledReplace = re
	}
	if op.Extract != nil {
		setCount++
		if op.Extract.Source == "" || op.Extract.Target == "" || op.Extract.Pattern == "" {
			return co, fmt.Errorf("operation[%d]: extract requires source, target, and pattern", idx)
		}
		re, err := regexp.Compile(op.Extract.Pattern)
		if err != nil {
			return co, fmt.Errorf("operation[%d]: invalid extract pattern %q: %w", idx, op.Extract.Pattern, err)
		}
		co.compiledExtract = re
	}
	if op.HashMod != nil {
		setCount++
		if op.HashMod.Source == "" || op.HashMod.Target == "" {
			return co, fmt.Errorf("operation[%d]: hash_mod requires source and target", idx)
		}
		if op.HashMod.Modulus == 0 {
			return co, fmt.Errorf("operation[%d]: hash_mod.modulus must be > 0", idx)
		}
	}
	if op.Lower != nil {
		setCount++
		if op.Lower.Label == "" {
			return co, fmt.Errorf("operation[%d]: lower.label is required", idx)
		}
	}
	if op.Upper != nil {
		setCount++
		if op.Upper.Label == "" {
			return co, fmt.Errorf("operation[%d]: upper.label is required", idx)
		}
	}
	if op.Concat != nil {
		setCount++
		if len(op.Concat.Sources) < 2 {
			return co, fmt.Errorf("operation[%d]: concat requires at least 2 sources", idx)
		}
		if op.Concat.Target == "" {
			return co, fmt.Errorf("operation[%d]: concat.target is required", idx)
		}
	}
	if op.Map != nil {
		setCount++
		if op.Map.Source == "" || op.Map.Target == "" {
			return co, fmt.Errorf("operation[%d]: map requires source and target", idx)
		}
		if len(op.Map.Values) == 0 {
			return co, fmt.Errorf("operation[%d]: map.values is required", idx)
		}
		// Pre-compile regex keys.
		for pattern, value := range op.Map.Values {
			re, err := regexp.Compile("^(?:" + pattern + ")$")
			if err != nil {
				return co, fmt.Errorf("operation[%d]: invalid map key regex %q: %w", idx, pattern, err)
			}
			co.compiledMap = append(co.compiledMap, compiledMapEntry{pattern: re, value: value})
		}
	}
	if op.Math != nil {
		setCount++
		if op.Math.Source == "" || op.Math.Target == "" {
			return co, fmt.Errorf("operation[%d]: math requires source and target", idx)
		}
		switch op.Math.Operation {
		case "add", "sub", "mul", "div", "mod":
		default:
			return co, fmt.Errorf("operation[%d]: unknown math operation %q", idx, op.Math.Operation)
		}
	}

	if setCount == 0 {
		return co, fmt.Errorf("operation[%d]: no operation specified", idx)
	}
	if setCount > 1 {
		return co, fmt.Errorf("operation[%d]: exactly one operation must be specified, got %d", idx, setCount)
	}

	return co, nil
}
