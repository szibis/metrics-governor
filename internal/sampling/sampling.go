package sampling

import (
	"context"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
)

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
}

// FileConfig is the top-level sampling configuration file.
type FileConfig struct {
	DefaultRate float64  `yaml:"default_rate"`
	Strategy    Strategy `yaml:"strategy"`
	Rules       []Rule   `yaml:"rules"`
}

// Sampler applies sampling rules to OTLP metrics.
// When created via NewFromProcessing, it uses the unified processing engine
// with multi-touch routing (transform/classify=non-terminal, others=terminal).
type Sampler struct {
	mu          sync.RWMutex
	defaultRate float64
	strategy    Strategy
	ops         atomic.Int64
	dsEngine    *downsampleEngine // nil when no downsample rules exist

	// Processing engine fields (set when using NewFromProcessing).
	procConfig    *ProcessingConfig
	procRules     []ProcessingRule
	aggEngine     *aggregateEngine
	deadScanner   *deadRuleScanner     // nil when dead_rule_interval not set
	ruleNameCache *processingRuleCache // LRU cache: metric name → matching rule indices
}

// Ops returns the total number of sampling operations performed.
func (s *Sampler) Ops() int64 {
	return s.ops.Load()
}

// Sample applies sampling rules to OTLP ResourceMetrics.
// Returns the modified slice (datapoints may be removed by sampling).
// Delegates to the processing engine.
func (s *Sampler) Sample(rms []*metricspb.ResourceMetrics) []*metricspb.ResourceMetrics {
	return s.Process(rms)
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

// ---------------------------------------------------------------------------
// Processing Engine
// ---------------------------------------------------------------------------

// NewFromProcessing creates a Sampler from a ProcessingConfig.
func NewFromProcessing(cfg ProcessingConfig) (*Sampler, error) {
	if err := validateProcessingConfig(&cfg); err != nil {
		return nil, err
	}

	// Build downsample engine if any downsample rules exist.
	var dsEngine *downsampleEngine
	for _, r := range cfg.Rules {
		if r.Action == ActionDownsample {
			dsEngine = newDownsampleEngine()
			break
		}
	}

	// Build aggregate engine.
	aggEngine := newAggregateEngine(cfg.Rules, cfg.parsedStaleness)

	// Start dead rule scanner if interval is configured.
	var scanner *deadRuleScanner
	if cfg.parsedDeadRuleIntvl > 0 {
		scanner = newDeadRuleScanner(cfg.parsedDeadRuleIntvl, cfg.Rules)
	}

	s := &Sampler{
		defaultRate:   1.0,
		strategy:      StrategyHead,
		procConfig:    &cfg,
		procRules:     cfg.Rules,
		dsEngine:      dsEngine,
		aggEngine:     aggEngine,
		deadScanner:   scanner,
		ruleNameCache: newProcessingRuleCache(50000),
	}

	updateProcessingRulesActive(cfg.Rules)

	return s, nil
}

// Process applies processing rules to OTLP ResourceMetrics using multi-touch routing.
// Transform rules are non-terminal (apply and continue). Other actions are terminal (first match wins).
func (s *Sampler) Process(rms []*metricspb.ResourceMetrics) []*metricspb.ResourceMetrics {
	if len(rms) == 0 {
		return rms
	}

	start := time.Now()

	s.mu.RLock()
	procRules := s.procRules
	s.mu.RUnlock()

	result := make([]*metricspb.ResourceMetrics, 0, len(rms))
	for _, rm := range rms {
		rm = s.processResourceMetrics(rm, procRules)
		if rm != nil {
			result = append(result, rm)
		}
	}

	processingDuration.Observe(time.Since(start).Seconds())

	// Update always-on dead rule gauges (cheap: just reads atomic timestamps).
	updateDeadRuleMetrics(procRules)

	return result
}

func (s *Sampler) processResourceMetrics(rm *metricspb.ResourceMetrics, rules []ProcessingRule) *metricspb.ResourceMetrics {
	if rm == nil {
		return nil
	}

	filteredScopes := make([]*metricspb.ScopeMetrics, 0, len(rm.ScopeMetrics))
	for _, sm := range rm.ScopeMetrics {
		sm = s.processScopeMetrics(sm, rules)
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

func (s *Sampler) processScopeMetrics(sm *metricspb.ScopeMetrics, rules []ProcessingRule) *metricspb.ScopeMetrics {
	if sm == nil {
		return nil
	}

	filteredMetrics := make([]*metricspb.Metric, 0, len(sm.Metrics))
	for _, m := range sm.Metrics {
		m = s.processMetric(m, rules)
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

func (s *Sampler) processMetric(m *metricspb.Metric, rules []ProcessingRule) *metricspb.Metric {
	if m == nil {
		return nil
	}

	switch data := m.Data.(type) {
	case *metricspb.Metric_Gauge:
		if data.Gauge != nil {
			filtered := s.processNumberDataPoints(m.Name, data.Gauge.DataPoints, rules)
			if filtered == nil {
				return nil
			}
			data.Gauge.DataPoints = filtered
		}
	case *metricspb.Metric_Sum:
		if data.Sum != nil {
			filtered := s.processNumberDataPoints(m.Name, data.Sum.DataPoints, rules)
			if filtered == nil {
				return nil
			}
			data.Sum.DataPoints = filtered
		}
	case *metricspb.Metric_Histogram:
		if data.Histogram != nil {
			filtered := s.processHistogramDataPoints(m.Name, data.Histogram.DataPoints, rules)
			if filtered == nil {
				return nil
			}
			data.Histogram.DataPoints = filtered
		}
	case *metricspb.Metric_Summary:
		if data.Summary != nil {
			filtered := s.processSummaryDataPoints(m.Name, data.Summary.DataPoints, rules)
			if filtered == nil {
				return nil
			}
			data.Summary.DataPoints = filtered
		}
	case *metricspb.Metric_ExponentialHistogram:
		if data.ExponentialHistogram != nil {
			filtered := s.processExponentialHistogramDataPoints(m.Name, data.ExponentialHistogram.DataPoints, rules)
			if filtered == nil {
				return nil
			}
			data.ExponentialHistogram.DataPoints = filtered
		}
	}

	return m
}

// matchProcessingRuleName checks if a metric name matches a rule's name pattern
// (prefix + regex). Does NOT check label conditions.
func matchProcessingRuleName(rule *ProcessingRule, metricName string) bool {
	if rule.inputPrefix != "" && !strings.HasPrefix(metricName, rule.inputPrefix) {
		return false
	}
	if rule.compiledInput != nil && !rule.compiledInput.MatchString(metricName) {
		return false
	}
	return true
}

// matchProcessingRuleLabels checks if a rule's label conditions match the given
// attributes. Uses a pre-built attribute map for O(1) lookups.
func matchProcessingRuleLabels(rule *ProcessingRule, attrMap map[string]string) bool {
	for key, re := range rule.compiledLabels {
		val, ok := attrMap[key]
		if !ok || !re.MatchString(val) {
			return false
		}
	}
	return true
}

// buildAttrMap creates a map from a KeyValue slice for O(1) label lookups.
func buildAttrMap(attrs []*commonpb.KeyValue) map[string]string {
	m := make(map[string]string, len(attrs))
	for _, kv := range attrs {
		m[kv.Key] = kv.Value.GetStringValue()
	}
	return m
}

// computeNameMatchingIndices returns the indices of all rules whose name pattern
// matches the given metric name. Used to populate the rule name cache.
func computeNameMatchingIndices(rules []ProcessingRule, metricName string) []int {
	var indices []int
	for i := range rules {
		if matchProcessingRuleName(&rules[i], metricName) {
			indices = append(indices, i)
		}
	}
	return indices
}

// processNumberDataPoints applies multi-touch processing rules to number data points.
func (s *Sampler) processNumberDataPoints(metricName string, dps []*metricspb.NumberDataPoint, rules []ProcessingRule) []*metricspb.NumberDataPoint {
	if len(dps) == 0 {
		return dps
	}

	result := make([]*metricspb.NumberDataPoint, 0, len(dps))
	for _, dp := range dps {
		processingInputDatapointsTotal.Inc()
		kept := s.applyProcessingRules(metricName, dp, rules)
		if kept {
			result = append(result, dp)
			processingOutputDatapointsTotal.Inc()
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

// applyProcessingRules walks rules in order with multi-touch semantics.
// Returns true if the datapoint should be kept in the output.
// Uses the rule name cache to skip rules whose name pattern doesn't match.
func (s *Sampler) applyProcessingRules(metricName string, dp *metricspb.NumberDataPoint, rules []ProcessingRule) bool {
	// Get name-matching rule indices from cache (or compute and cache).
	indices, ok := s.ruleNameCache.Get(metricName)
	if !ok {
		indices = computeNameMatchingIndices(rules, metricName)
		s.ruleNameCache.Put(metricName, indices)
	}

	// Pre-build attribute map once per DP for O(1) label lookups.
	var attrMap map[string]string

	for _, i := range indices {
		rule := &rules[i]

		// Check label conditions if present.
		if len(rule.compiledLabels) > 0 {
			if attrMap == nil {
				attrMap = buildAttrMap(dp.Attributes)
			}
			if !matchProcessingRuleLabels(rule, attrMap) {
				continue
			}
		}

		rule.metrics.evalCounter.Inc()

		// Record last-match timestamp for dead rule detection (always, unconditionally).
		rule.metrics.dead.RecordMatch()

		switch rule.Action {
		case ActionTransform:
			// Non-terminal: apply transform and continue to next rule.
			if len(rule.When) > 0 && !evaluateConditions(rule.When, dp.Attributes) {
				continue
			}
			dp.Attributes = applyTransformOperations(dp.Attributes, rule.compiledOps, &rule.metrics)
			rule.metrics.inputCounter.Inc()
			rule.metrics.outputCounter.Inc()
			continue // Non-terminal — check next rule.

		case ActionClassify:
			// Non-terminal: apply classification and continue to next rule.
			dp.Attributes = applyClassify(dp.Attributes, rule.compiledClassify, &rule.metrics)
			rule.metrics.inputCounter.Inc()
			rule.metrics.outputCounter.Inc()
			continue // Non-terminal — check next rule.

		case ActionDrop:
			rule.metrics.inputCounter.Inc()
			rule.metrics.droppedCounter.Inc()
			processingDroppedDatapointsTotal.Inc()
			return false // Terminal — drop.

		case ActionSample:
			rule.metrics.inputCounter.Inc()
			strategy := StrategyHead
			if rule.Method == "probabilistic" {
				strategy = StrategyProbabilistic
			}
			if s.shouldKeep(rule.Rate, strategy) {
				rule.metrics.outputCounter.Inc()
				return true // Terminal — kept.
			}
			rule.metrics.droppedCounter.Inc()
			processingDroppedDatapointsTotal.Inc()
			return false // Terminal — dropped.

		case ActionDownsample:
			rule.metrics.inputCounter.Inc()
			if rule.dsConfig != nil && s.dsEngine != nil {
				seriesKey := buildDSSeriesKey(metricName, dp.Attributes)
				emitted := s.dsEngine.ingestAndEmit(seriesKey, rule.dsConfig, dp.TimeUnixNano, getNumberValue(dp))

				// Track active series per rule/method for dashboard visibility.
				processingDownsampleActiveSeries.WithLabelValues(rule.Name, rule.Method).Set(float64(s.dsEngine.seriesCount()))

				if len(emitted) > 0 {
					// Replace dp value with first emitted; additional points are handled upstream.
					dp.TimeUnixNano = emitted[0].timestamp
					dp.Value = &metricspb.NumberDataPoint_AsDouble{AsDouble: emitted[0].value}
					for range emitted {
						processingRuleOutputTotal.WithLabelValues(rule.Name, string(ActionDownsample), rule.Method).Inc()
					}
					return true // Terminal — emitted.
				}
				// Accumulated but not ready to emit yet.
				processingDroppedDatapointsTotal.Inc()
				return false // Terminal — buffered.
			}
			return true // No engine — pass through.

		case ActionAggregate:
			rule.metrics.inputCounter.Inc()
			if s.aggEngine != nil {
				s.aggEngine.Ingest(rule, metricName, dp)
			}
			if rule.KeepInput {
				return true // Terminal — ingested + keep original.
			}
			processingDroppedDatapointsTotal.Inc()
			return false // Terminal — ingested, original dropped.
		}
	}

	// No terminal rule matched — pass through unchanged.
	processingOutputDatapointsTotal.Inc()
	return true
}

// processHistogramDataPoints applies processing rules to histogram datapoints.
// Only sample, drop, and transform actions apply (downsample/aggregate are for NumberDataPoints only).
func (s *Sampler) processHistogramDataPoints(metricName string, dps []*metricspb.HistogramDataPoint, rules []ProcessingRule) []*metricspb.HistogramDataPoint {
	if len(dps) == 0 {
		return dps
	}

	result := make([]*metricspb.HistogramDataPoint, 0, len(dps))
	for _, dp := range dps {
		processingInputDatapointsTotal.Inc()
		kept := s.applyProcessingRulesGeneric(metricName, dp.Attributes, rules, func(attrs []*commonpb.KeyValue) {
			dp.Attributes = attrs
		})
		if kept {
			result = append(result, dp)
			processingOutputDatapointsTotal.Inc()
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

// processSummaryDataPoints applies processing rules to summary datapoints.
func (s *Sampler) processSummaryDataPoints(metricName string, dps []*metricspb.SummaryDataPoint, rules []ProcessingRule) []*metricspb.SummaryDataPoint {
	if len(dps) == 0 {
		return dps
	}

	result := make([]*metricspb.SummaryDataPoint, 0, len(dps))
	for _, dp := range dps {
		processingInputDatapointsTotal.Inc()
		kept := s.applyProcessingRulesGeneric(metricName, dp.Attributes, rules, func(attrs []*commonpb.KeyValue) {
			dp.Attributes = attrs
		})
		if kept {
			result = append(result, dp)
			processingOutputDatapointsTotal.Inc()
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

// processExponentialHistogramDataPoints applies processing rules to exponential histogram datapoints.
func (s *Sampler) processExponentialHistogramDataPoints(metricName string, dps []*metricspb.ExponentialHistogramDataPoint, rules []ProcessingRule) []*metricspb.ExponentialHistogramDataPoint {
	if len(dps) == 0 {
		return dps
	}

	result := make([]*metricspb.ExponentialHistogramDataPoint, 0, len(dps))
	for _, dp := range dps {
		processingInputDatapointsTotal.Inc()
		kept := s.applyProcessingRulesGeneric(metricName, dp.Attributes, rules, func(attrs []*commonpb.KeyValue) {
			dp.Attributes = attrs
		})
		if kept {
			result = append(result, dp)
			processingOutputDatapointsTotal.Inc()
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

// applyProcessingRulesGeneric handles multi-touch routing for non-NumberDataPoint types.
// Only sample, drop, transform, and classify actions apply. Downsample/aggregate are skipped (NumberDataPoint only).
func (s *Sampler) applyProcessingRulesGeneric(metricName string, attrs []*commonpb.KeyValue, rules []ProcessingRule, setAttrs func([]*commonpb.KeyValue)) bool {
	// Get name-matching rule indices from cache (or compute and cache).
	indices, ok := s.ruleNameCache.Get(metricName)
	if !ok {
		indices = computeNameMatchingIndices(rules, metricName)
		s.ruleNameCache.Put(metricName, indices)
	}

	var attrMap map[string]string

	for _, i := range indices {
		rule := &rules[i]

		if len(rule.compiledLabels) > 0 {
			if attrMap == nil {
				attrMap = buildAttrMap(attrs)
			}
			if !matchProcessingRuleLabels(rule, attrMap) {
				continue
			}
		}

		rule.metrics.evalCounter.Inc()

		// Record last-match timestamp for dead rule detection.
		rule.metrics.dead.RecordMatch()

		switch rule.Action {
		case ActionTransform:
			if len(rule.When) > 0 && !evaluateConditions(rule.When, attrs) {
				continue
			}
			attrs = applyTransformOperations(attrs, rule.compiledOps, &rule.metrics)
			setAttrs(attrs)
			rule.metrics.inputCounter.Inc()
			rule.metrics.outputCounter.Inc()
			continue // Non-terminal.

		case ActionClassify:
			attrs = applyClassify(attrs, rule.compiledClassify, &rule.metrics)
			setAttrs(attrs)
			rule.metrics.inputCounter.Inc()
			rule.metrics.outputCounter.Inc()
			continue // Non-terminal.

		case ActionDrop:
			rule.metrics.inputCounter.Inc()
			rule.metrics.droppedCounter.Inc()
			processingDroppedDatapointsTotal.Inc()
			return false

		case ActionSample:
			rule.metrics.inputCounter.Inc()
			strategy := StrategyHead
			if rule.Method == "probabilistic" {
				strategy = StrategyProbabilistic
			}
			if s.shouldKeep(rule.Rate, strategy) {
				rule.metrics.outputCounter.Inc()
				return true
			}
			rule.metrics.droppedCounter.Inc()
			processingDroppedDatapointsTotal.Inc()
			return false

		case ActionDownsample, ActionAggregate:
			// Skip — these only apply to NumberDataPoints.
			continue
		}
	}

	// No terminal rule matched — pass through.
	return true
}

// ReloadProcessingConfig atomically replaces the processing rules.
func (s *Sampler) ReloadProcessingConfig(cfg ProcessingConfig) error {
	if err := validateProcessingConfig(&cfg); err != nil {
		processingConfigReloadsTotal.WithLabelValues("error").Inc()
		return err
	}

	// Rebuild downsample engine.
	var dsEngine *downsampleEngine
	for _, r := range cfg.Rules {
		if r.Action == ActionDownsample {
			dsEngine = newDownsampleEngine()
			break
		}
	}

	s.mu.Lock()
	s.procConfig = &cfg
	s.procRules = cfg.Rules
	s.dsEngine = dsEngine
	s.mu.Unlock()

	// Clear the rule name cache — rule indices are no longer valid.
	s.ruleNameCache.ClearCache()

	// Reload aggregate engine (flushes existing state).
	if s.aggEngine != nil {
		s.aggEngine.Reload(cfg.Rules, cfg.parsedStaleness)
	}

	// Update dead rule scanner with new rules.
	if s.deadScanner != nil {
		s.deadScanner.updateRules(cfg.Rules)
	}

	updateProcessingRulesActive(cfg.Rules)
	processingConfigReloadsTotal.WithLabelValues("success").Inc()
	return nil
}

// StartAggregation starts the aggregate engine's flush timers.
func (s *Sampler) StartAggregation(ctx context.Context) {
	if s.aggEngine != nil {
		s.aggEngine.Start(ctx)
	}
}

// StopAggregation stops the aggregate engine's flush timers.
func (s *Sampler) StopAggregation() {
	if s.aggEngine != nil {
		s.aggEngine.Stop()
	}
}

// StopDeadRuleScanner stops the dead rule scanner goroutine.
func (s *Sampler) StopDeadRuleScanner() {
	if s.deadScanner != nil {
		s.deadScanner.stop()
	}
}

// UpdateDeadRuleMetrics computes the always-on dead rule gauges from current state.
// Should be called periodically (e.g. on Prometheus scrape or in Process loop).
func (s *Sampler) UpdateDeadRuleMetrics() {
	s.mu.RLock()
	rules := s.procRules
	s.mu.RUnlock()
	if len(rules) > 0 {
		updateDeadRuleMetrics(rules)
	}
}

// SetAggregateOutput sets the callback for re-injecting aggregated metrics.
func (s *Sampler) SetAggregateOutput(fn func([]*metricspb.ResourceMetrics)) {
	if s.aggEngine != nil {
		s.aggEngine.SetOutput(fn)
	}
}

// HasAggregation returns true if there are any aggregate rules configured.
func (s *Sampler) HasAggregation() bool {
	return s.aggEngine != nil && s.aggEngine.HasRules()
}

// ProcessingRuleCount returns the number of active processing rules.
func (s *Sampler) ProcessingRuleCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.procRules)
}
