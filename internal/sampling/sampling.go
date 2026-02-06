package sampling

import (
	"fmt"
	"math/rand/v2"
	"os"
	"regexp"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"gopkg.in/yaml.v3"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

var (
	samplingKeptTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_sampling_kept_total",
		Help: "Total number of datapoints kept by sampling rules",
	}, []string{"rule"})

	samplingDroppedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_sampling_dropped_total",
		Help: "Total number of datapoints dropped by sampling rules",
	}, []string{"rule"})

	samplingConfigReloadsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_sampling_config_reloads_total",
		Help: "Total number of sampling config reloads by result",
	}, []string{"result"})
)

func init() {
	prometheus.MustRegister(samplingKeptTotal)
	prometheus.MustRegister(samplingDroppedTotal)
	prometheus.MustRegister(samplingConfigReloadsTotal)
}

// Strategy defines the sampling strategy.
type Strategy string

const (
	StrategyHead          Strategy = "head"
	StrategyProbabilistic Strategy = "probabilistic"
	StrategyDownsample    Strategy = "downsample"
)

// Rule defines a single sampling rule.
type Rule struct {
	Name       string            `yaml:"name"`
	Match      map[string]string `yaml:"match"`
	Rate       float64           `yaml:"rate"`
	Strategy   Strategy          `yaml:"strategy"`
	Downsample *DownsampleConfig `yaml:"downsample,omitempty"`

	compiledMatch map[string]*regexp.Regexp
}

// FileConfig is the top-level sampling configuration file.
type FileConfig struct {
	DefaultRate float64  `yaml:"default_rate"`
	Strategy    Strategy `yaml:"strategy"`
	Rules       []Rule   `yaml:"rules"`
}

// Sampler applies sampling rules to OTLP metrics.
type Sampler struct {
	mu          sync.RWMutex
	defaultRate float64
	strategy    Strategy
	rules       []Rule
	ops         atomic.Int64
	dsEngine    *downsampleEngine // nil when no downsample rules exist
}

// New creates a Sampler from a config.
func New(cfg FileConfig) (*Sampler, error) {
	compiled, err := compileRules(cfg.Rules)
	if err != nil {
		return nil, err
	}

	defaultRate := cfg.DefaultRate
	if defaultRate < 0 {
		defaultRate = 0
	}
	if defaultRate > 1.0 {
		defaultRate = 1.0
	}

	strategy := cfg.Strategy
	if strategy == "" {
		strategy = StrategyHead
	}

	// Only create downsample engine if needed.
	var dsEngine *downsampleEngine
	for _, r := range compiled {
		if r.Strategy == StrategyDownsample {
			dsEngine = newDownsampleEngine()
			break
		}
	}

	return &Sampler{
		defaultRate: defaultRate,
		strategy:    strategy,
		rules:       compiled,
		dsEngine:    dsEngine,
	}, nil
}

// LoadFile loads sampling config from a YAML file.
func LoadFile(path string) (FileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, fmt.Errorf("sampling: read config: %w", err)
	}
	return Parse(data)
}

// Parse parses sampling config from YAML bytes.
func Parse(data []byte) (FileConfig, error) {
	var cfg FileConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, fmt.Errorf("sampling: parse config: %w", err)
	}
	return cfg, nil
}

func compileRules(rules []Rule) ([]Rule, error) {
	compiled := make([]Rule, len(rules))
	for i, r := range rules {
		if r.Strategy == "" {
			r.Strategy = StrategyHead
		}

		// Validate rate for non-downsample rules.
		if r.Strategy != StrategyDownsample {
			if r.Rate < 0 || r.Rate > 1.0 {
				return nil, fmt.Errorf("sampling: rule %d (%s): rate must be between 0.0 and 1.0, got %f", i, r.Name, r.Rate)
			}
		}

		// Validate downsample config.
		if r.Strategy == StrategyDownsample {
			if r.Downsample == nil {
				return nil, fmt.Errorf("sampling: rule %d (%s): strategy 'downsample' requires a downsample config block", i, r.Name)
			}
			if err := r.Downsample.validate(); err != nil {
				return nil, fmt.Errorf("sampling: rule %d (%s): %w", i, r.Name, err)
			}
		}

		r.compiledMatch = make(map[string]*regexp.Regexp, len(r.Match))
		for k, v := range r.Match {
			re, err := regexp.Compile("^(?:" + v + ")$")
			if err != nil {
				return nil, fmt.Errorf("sampling: rule %d (%s): invalid regex for key %q: %w", i, r.Name, k, err)
			}
			r.compiledMatch[k] = re
		}
		compiled[i] = r
	}
	return compiled, nil
}

// ReloadConfig atomically replaces the sampling rules.
func (s *Sampler) ReloadConfig(cfg FileConfig) error {
	compiled, err := compileRules(cfg.Rules)
	if err != nil {
		samplingConfigReloadsTotal.WithLabelValues("error").Inc()
		return err
	}

	defaultRate := cfg.DefaultRate
	if defaultRate < 0 {
		defaultRate = 0
	}
	if defaultRate > 1.0 {
		defaultRate = 1.0
	}

	strategy := cfg.Strategy
	if strategy == "" {
		strategy = StrategyHead
	}

	// Rebuild downsample engine (flushes accumulated state).
	var dsEngine *downsampleEngine
	for _, r := range compiled {
		if r.Strategy == StrategyDownsample {
			dsEngine = newDownsampleEngine()
			break
		}
	}

	s.mu.Lock()
	s.defaultRate = defaultRate
	s.strategy = strategy
	s.rules = compiled
	s.dsEngine = dsEngine
	s.mu.Unlock()

	samplingConfigReloadsTotal.WithLabelValues("success").Inc()
	return nil
}

// Ops returns the total number of sampling operations performed.
func (s *Sampler) Ops() int64 {
	return s.ops.Load()
}

// Sample applies sampling rules to OTLP ResourceMetrics.
// Returns the modified slice (datapoints may be removed by sampling).
func (s *Sampler) Sample(rms []*metricspb.ResourceMetrics) []*metricspb.ResourceMetrics {
	s.mu.RLock()
	rules := s.rules
	defaultRate := s.defaultRate
	strategy := s.strategy
	s.mu.RUnlock()

	if len(rms) == 0 {
		return rms
	}

	result := make([]*metricspb.ResourceMetrics, 0, len(rms))
	for _, rm := range rms {
		rm = s.sampleResourceMetrics(rm, rules, defaultRate, strategy)
		if rm != nil {
			result = append(result, rm)
		}
	}
	return result
}

func (s *Sampler) sampleResourceMetrics(rm *metricspb.ResourceMetrics, rules []Rule, defaultRate float64, strategy Strategy) *metricspb.ResourceMetrics {
	if rm == nil {
		return nil
	}

	filteredScopes := make([]*metricspb.ScopeMetrics, 0, len(rm.ScopeMetrics))
	for _, sm := range rm.ScopeMetrics {
		sm = s.sampleScopeMetrics(sm, rules, defaultRate, strategy)
		if sm != nil && len(sm.Metrics) > 0 {
			filteredScopes = append(filteredScopes, sm)
		}
	}

	if len(filteredScopes) == 0 {
		return nil
	}
	rm.ScopeMetrics = filteredScopes
	return rm
}

func (s *Sampler) sampleScopeMetrics(sm *metricspb.ScopeMetrics, rules []Rule, defaultRate float64, strategy Strategy) *metricspb.ScopeMetrics {
	if sm == nil {
		return nil
	}

	filteredMetrics := make([]*metricspb.Metric, 0, len(sm.Metrics))
	for _, m := range sm.Metrics {
		m = s.sampleMetric(m, rules, defaultRate, strategy)
		if m != nil {
			filteredMetrics = append(filteredMetrics, m)
		}
	}

	if len(filteredMetrics) == 0 {
		return nil
	}
	sm.Metrics = filteredMetrics
	return sm
}

func (s *Sampler) sampleMetric(m *metricspb.Metric, rules []Rule, defaultRate float64, strategy Strategy) *metricspb.Metric {
	if m == nil {
		return nil
	}

	switch data := m.Data.(type) {
	case *metricspb.Metric_Gauge:
		if data.Gauge != nil {
			filtered := s.sampleNumberDataPoints(m.Name, data.Gauge.DataPoints, rules, defaultRate, strategy)
			if filtered == nil {
				return nil
			}
			data.Gauge.DataPoints = filtered
		}
	case *metricspb.Metric_Sum:
		if data.Sum != nil {
			filtered := s.sampleNumberDataPoints(m.Name, data.Sum.DataPoints, rules, defaultRate, strategy)
			if filtered == nil {
				return nil
			}
			data.Sum.DataPoints = filtered
		}
	case *metricspb.Metric_Histogram:
		if data.Histogram != nil {
			filtered := s.sampleHistogramDataPoints(m.Name, data.Histogram.DataPoints, rules, defaultRate, strategy)
			if filtered == nil {
				return nil
			}
			data.Histogram.DataPoints = filtered
		}
	case *metricspb.Metric_Summary:
		if data.Summary != nil {
			filtered := s.sampleSummaryDataPoints(m.Name, data.Summary.DataPoints, rules, defaultRate, strategy)
			if filtered == nil {
				return nil
			}
			data.Summary.DataPoints = filtered
		}
	case *metricspb.Metric_ExponentialHistogram:
		if data.ExponentialHistogram != nil {
			filtered := s.sampleExponentialHistogramDataPoints(m.Name, data.ExponentialHistogram.DataPoints, rules, defaultRate, strategy)
			if filtered == nil {
				return nil
			}
			data.ExponentialHistogram.DataPoints = filtered
		}
	}

	return m
}

// matchRule checks if a set of labels matches a rule.
func matchRule(rule Rule, metricName string, attrs []*commonpb.KeyValue) bool {
	for key, re := range rule.compiledMatch {
		if key == "__name__" {
			if !re.MatchString(metricName) {
				return false
			}
			continue
		}
		found := false
		for _, kv := range attrs {
			if kv.Key == key {
				if re.MatchString(kv.Value.GetStringValue()) {
					found = true
				}
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// findMatchingRule returns the first matching rule and its name, or nil if no rule matches.
func findMatchingRule(rules []Rule, metricName string, attrs []*commonpb.KeyValue) *Rule {
	for i := range rules {
		if matchRule(rules[i], metricName, attrs) {
			return &rules[i]
		}
	}
	return nil
}

// shouldKeep determines whether a datapoint should be kept based on rate and strategy.
func (s *Sampler) shouldKeep(rate float64, strategy Strategy) bool {
	if rate >= 1.0 {
		return true
	}
	if rate <= 0.0 {
		return false
	}

	s.ops.Add(1)

	switch strategy {
	case StrategyProbabilistic:
		return rand.Float64() < rate //nolint:gosec // G404: probabilistic sampling doesn't need crypto randomness
	default: // head sampling — deterministic 1-in-N
		n := int64(1.0 / rate)
		if n <= 0 {
			n = 1
		}
		return s.ops.Load()%n == 0
	}
}

func (s *Sampler) sampleNumberDataPoints(metricName string, dps []*metricspb.NumberDataPoint, rules []Rule, defaultRate float64, strategy Strategy) []*metricspb.NumberDataPoint {
	if len(dps) == 0 {
		return dps
	}
	result := make([]*metricspb.NumberDataPoint, 0, len(dps))
	for _, dp := range dps {
		rule := findMatchingRule(rules, metricName, dp.Attributes)

		if rule != nil && rule.Strategy == StrategyDownsample && rule.Downsample != nil && s.dsEngine != nil {
			// Downsample path: accumulate datapoints and emit aggregated values.
			ruleName := rule.Name
			if ruleName == "" {
				ruleName = "unnamed"
			}
			methodStr := string(rule.Downsample.Method)

			seriesKey := buildDSSeriesKey(metricName, dp.Attributes)
			emitted := s.dsEngine.ingestAndEmit(seriesKey, rule.Downsample, dp.TimeUnixNano, getNumberValue(dp))

			downsamplingInputTotal.WithLabelValues(ruleName, methodStr).Inc()
			for _, ep := range emitted {
				result = append(result, cloneDatapointWithValue(dp, ep.timestamp, ep.value))
				downsamplingOutputTotal.WithLabelValues(ruleName, methodStr).Inc()
			}
		} else {
			// Regular sampling path.
			rate, rs, ruleName := resolveRateFromRule(rule, defaultRate, strategy)
			if s.shouldKeep(rate, rs) {
				samplingKeptTotal.WithLabelValues(ruleName).Inc()
				result = append(result, dp)
			} else {
				samplingDroppedTotal.WithLabelValues(ruleName).Inc()
			}
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func (s *Sampler) sampleHistogramDataPoints(metricName string, dps []*metricspb.HistogramDataPoint, rules []Rule, defaultRate float64, strategy Strategy) []*metricspb.HistogramDataPoint {
	if len(dps) == 0 {
		return dps
	}
	result := make([]*metricspb.HistogramDataPoint, 0, len(dps))
	for _, dp := range dps {
		rate, rs, ruleName := s.resolveRate(metricName, dp.Attributes, rules, defaultRate, strategy)
		if s.shouldKeep(rate, rs) {
			samplingKeptTotal.WithLabelValues(ruleName).Inc()
			result = append(result, dp)
		} else {
			samplingDroppedTotal.WithLabelValues(ruleName).Inc()
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func (s *Sampler) sampleSummaryDataPoints(metricName string, dps []*metricspb.SummaryDataPoint, rules []Rule, defaultRate float64, strategy Strategy) []*metricspb.SummaryDataPoint {
	if len(dps) == 0 {
		return dps
	}
	result := make([]*metricspb.SummaryDataPoint, 0, len(dps))
	for _, dp := range dps {
		rate, rs, ruleName := s.resolveRate(metricName, dp.Attributes, rules, defaultRate, strategy)
		if s.shouldKeep(rate, rs) {
			samplingKeptTotal.WithLabelValues(ruleName).Inc()
			result = append(result, dp)
		} else {
			samplingDroppedTotal.WithLabelValues(ruleName).Inc()
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func (s *Sampler) sampleExponentialHistogramDataPoints(metricName string, dps []*metricspb.ExponentialHistogramDataPoint, rules []Rule, defaultRate float64, strategy Strategy) []*metricspb.ExponentialHistogramDataPoint {
	if len(dps) == 0 {
		return dps
	}
	result := make([]*metricspb.ExponentialHistogramDataPoint, 0, len(dps))
	for _, dp := range dps {
		rate, rs, ruleName := s.resolveRate(metricName, dp.Attributes, rules, defaultRate, strategy)
		if s.shouldKeep(rate, rs) {
			samplingKeptTotal.WithLabelValues(ruleName).Inc()
			result = append(result, dp)
		} else {
			samplingDroppedTotal.WithLabelValues(ruleName).Inc()
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// resolveRate finds the sampling rate for a datapoint: first matching rule wins, else default.
func (s *Sampler) resolveRate(metricName string, attrs []*commonpb.KeyValue, rules []Rule, defaultRate float64, defaultStrategy Strategy) (float64, Strategy, string) {
	rule := findMatchingRule(rules, metricName, attrs)
	return resolveRateFromRule(rule, defaultRate, defaultStrategy)
}

// resolveRateFromRule extracts rate/strategy/name from a pre-found rule (or returns defaults).
func resolveRateFromRule(rule *Rule, defaultRate float64, defaultStrategy Strategy) (float64, Strategy, string) {
	if rule != nil {
		rs := rule.Strategy
		if rs == "" {
			rs = defaultStrategy
		}
		name := rule.Name
		if name == "" {
			name = "unnamed"
		}
		return rule.Rate, rs, name
	}
	return defaultRate, defaultStrategy, "default"
}
