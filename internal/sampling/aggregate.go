package sampling

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// aggregateGroup holds accumulators for a single label combination within a rule.
type aggregateGroup struct {
	accumulators []seriesAccumulator
	quantilesIdx []int // indices into accumulators that are quantilesAccum
	lastSeen     time.Time
	count        int64
	labels       []*commonpb.KeyValue // the grouped label set
}

// aggregateRuleState holds per-rule aggregation state.
type aggregateRuleState struct {
	mu       sync.Mutex
	rule     ProcessingRule
	groups   map[string]*aggregateGroup // key = sorted label combo string
	interval time.Duration
}

// aggregateEngine manages cross-series aggregation for all aggregate rules.
type aggregateEngine struct {
	mu        sync.RWMutex
	rules     []*aggregateRuleState
	staleness time.Duration
	outputFn  func([]*metricspb.ResourceMetrics) // callback to re-inject results
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	started   bool
}

// newAggregateEngine creates a new aggregate engine from the given rules.
func newAggregateEngine(rules []ProcessingRule, staleness time.Duration) *aggregateEngine {
	if staleness <= 0 {
		staleness = 10 * time.Minute
	}

	var aggRules []*aggregateRuleState
	for _, r := range rules {
		if r.Action != ActionAggregate {
			continue
		}
		state := &aggregateRuleState{
			rule:     r,
			groups:   make(map[string]*aggregateGroup),
			interval: r.parsedInterval,
		}
		aggRules = append(aggRules, state)
	}

	return &aggregateEngine{
		rules:     aggRules,
		staleness: staleness,
	}
}

// SetOutput sets the callback for re-injecting aggregated metrics.
func (e *aggregateEngine) SetOutput(fn func([]*metricspb.ResourceMetrics)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.outputFn = fn
}

// Start begins the periodic flush timers for each rule.
func (e *aggregateEngine) Start(ctx context.Context) {
	e.mu.Lock()
	if e.started {
		e.mu.Unlock()
		return
	}
	e.started = true
	innerCtx, cancel := context.WithCancel(ctx)
	e.cancel = cancel
	e.mu.Unlock()

	for _, rs := range e.rules {
		e.wg.Add(1)
		go e.flushLoop(innerCtx, rs)
	}

	// Stale cleanup goroutine
	e.wg.Add(1)
	go e.cleanupLoop(innerCtx)
}

// Stop stops all flush timers and waits for goroutines to exit.
func (e *aggregateEngine) Stop() {
	e.mu.Lock()
	if !e.started {
		e.mu.Unlock()
		return
	}
	e.started = false
	if e.cancel != nil {
		e.cancel()
	}
	e.mu.Unlock()
	e.wg.Wait()
}

// flushLoop periodically flushes a rule's aggregation groups.
func (e *aggregateEngine) flushLoop(ctx context.Context, rs *aggregateRuleState) {
	defer e.wg.Done()
	ticker := time.NewTicker(rs.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Final flush on shutdown.
			e.flushRule(rs)
			return
		case <-ticker.C:
			e.flushRule(rs)
		}
	}
}

// cleanupLoop periodically removes stale aggregation groups.
func (e *aggregateEngine) cleanupLoop(ctx context.Context) {
	defer e.wg.Done()
	ticker := time.NewTicker(e.staleness / 2)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.cleanupStale()
		}
	}
}

// Ingest adds a datapoint to the appropriate aggregation group.
func (e *aggregateEngine) Ingest(rule *ProcessingRule, metricName string, dp *metricspb.NumberDataPoint) {
	// Find the rule state.
	e.mu.RLock()
	var rs *aggregateRuleState
	for _, r := range e.rules {
		if r.rule.Name == rule.Name {
			rs = r
			break
		}
	}
	e.mu.RUnlock()
	if rs == nil {
		return
	}

	// Build the group key from the label set.
	groupLabels := buildGroupLabels(dp.Attributes, rule.GroupBy, rule.DropLabels)
	groupKey := buildGroupKey(groupLabels)

	rs.mu.Lock()
	defer rs.mu.Unlock()

	group, ok := rs.groups[groupKey]
	if !ok {
		group = e.createGroup(rule, groupLabels)
		rs.groups[groupKey] = group

		// Track group creation.
		processingAggregateGroupsCreatedTotal.WithLabelValues(rule.Name).Inc()
	}

	value := getNumberValue(dp)
	for _, acc := range group.accumulators {
		acc.Add(value)
	}
	group.count++
	group.lastSeen = time.Now()

	processingAggregateGroupsActive.WithLabelValues(rule.Name).Set(float64(len(rs.groups)))
}

// createGroup initializes accumulators for a new group.
func (e *aggregateEngine) createGroup(rule *ProcessingRule, labels []*commonpb.KeyValue) *aggregateGroup {
	group := &aggregateGroup{
		labels:   labels,
		lastSeen: time.Now(),
	}

	for i, pf := range rule.parsedFunctions {
		if pf.Func == AggQuantiles {
			acc := newQuantilesAccum(pf.Quantiles)
			group.accumulators = append(group.accumulators, acc)
			group.quantilesIdx = append(group.quantilesIdx, i)
		} else {
			acc := newAccumulator(pf.Func)
			// Set window for rate accumulator.
			if ra, ok := acc.(*rateAccum); ok {
				ra.setWindow(rule.parsedInterval.Seconds())
			}
			group.accumulators = append(group.accumulators, acc)
		}
	}

	return group
}

// flushRule emits aggregated values for all groups of a rule and resets accumulators.
func (e *aggregateEngine) flushRule(rs *aggregateRuleState) {
	start := time.Now()
	rs.mu.Lock()

	if len(rs.groups) == 0 {
		rs.mu.Unlock()
		return
	}

	rule := &rs.rule
	outputName := rule.Output
	if outputName == "" {
		outputName = rule.Input
	}

	var metrics []*metricspb.Metric

	for _, group := range rs.groups {
		if group.count == 0 {
			continue
		}

		now := uint64(time.Now().UnixNano())

		// When there's a single non-quantile function, output metric name is outputName.
		// When multiple functions or quantiles, append _funcname suffix.
		multiOutput := len(rule.parsedFunctions) > 1

		for i, pf := range rule.parsedFunctions {
			if pf.Func == AggQuantiles {
				qa := group.accumulators[i].(*quantilesAccum)
				results := qa.Results()
				for qi, q := range pf.Quantiles {
					name := fmt.Sprintf("%s_p%s", outputName, formatQuantile(q))
					dp := &metricspb.NumberDataPoint{
						Attributes:   group.labels,
						TimeUnixNano: now,
						Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: results[qi]},
					}
					metrics = append(metrics, &metricspb.Metric{
						Name: name,
						Data: &metricspb.Metric_Gauge{
							Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{dp}},
						},
					})
				}
			} else {
				name := outputName
				if multiOutput {
					name = fmt.Sprintf("%s_%s", outputName, pf.Func)
				}
				dp := &metricspb.NumberDataPoint{
					Attributes:   group.labels,
					TimeUnixNano: now,
					Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: group.accumulators[i].Result()},
				}
				metrics = append(metrics, &metricspb.Metric{
					Name: name,
					Data: &metricspb.Metric_Gauge{
						Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{dp}},
					},
				})
			}
		}

		// Reset accumulators for next window.
		for _, acc := range group.accumulators {
			acc.Reset()
		}
		group.count = 0
	}

	rs.mu.Unlock()

	if len(metrics) == 0 {
		return
	}

	// Track metrics.
	processingAggregateWindowsCompletedTotal.WithLabelValues(rule.Name).Inc()
	processingAggregateFlushDuration.WithLabelValues(rule.Name).Observe(time.Since(start).Seconds())

	for range metrics {
		processingRuleOutputTotal.WithLabelValues(rule.Name, string(ActionAggregate), "").Inc()
	}

	// Build ResourceMetrics and inject via callback.
	rm := &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{
				Metrics: metrics,
			},
		},
	}

	e.mu.RLock()
	fn := e.outputFn
	e.mu.RUnlock()

	if fn != nil {
		fn([]*metricspb.ResourceMetrics{rm})
	}
}

// cleanupStale removes groups not seen within the staleness interval.
func (e *aggregateEngine) cleanupStale() {
	now := time.Now()

	for _, rs := range e.rules {
		rs.mu.Lock()
		for key, group := range rs.groups {
			if now.Sub(group.lastSeen) > e.staleness {
				delete(rs.groups, key)
				processingAggregateStaleCleaned.WithLabelValues(rs.rule.Name).Inc()
			}
		}
		processingAggregateGroupsActive.WithLabelValues(rs.rule.Name).Set(float64(len(rs.groups)))
		rs.mu.Unlock()
	}
}

// Reload replaces the aggregate rules. Existing state is flushed and discarded.
func (e *aggregateEngine) Reload(rules []ProcessingRule, staleness time.Duration) {
	// Stop existing timers.
	e.Stop()

	if staleness <= 0 {
		staleness = 10 * time.Minute
	}

	var aggRules []*aggregateRuleState
	for _, r := range rules {
		if r.Action != ActionAggregate {
			continue
		}
		state := &aggregateRuleState{
			rule:     r,
			groups:   make(map[string]*aggregateGroup),
			interval: r.parsedInterval,
		}
		aggRules = append(aggRules, state)
	}

	e.mu.Lock()
	e.rules = aggRules
	e.staleness = staleness
	e.mu.Unlock()
}

// ActiveGroups returns total active groups across all rules.
func (e *aggregateEngine) ActiveGroups() int {
	total := 0
	for _, rs := range e.rules {
		rs.mu.Lock()
		total += len(rs.groups)
		rs.mu.Unlock()
	}
	return total
}

// HasRules returns true if there are any aggregate rules.
func (e *aggregateEngine) HasRules() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.rules) > 0
}

// buildGroupLabels extracts the subset of labels for grouping.
func buildGroupLabels(attrs []*commonpb.KeyValue, groupBy []string, dropLabels []string) []*commonpb.KeyValue {
	if len(groupBy) > 0 {
		// Keep only group_by labels.
		keep := make(map[string]bool, len(groupBy))
		for _, g := range groupBy {
			keep[g] = true
		}
		var result []*commonpb.KeyValue
		for _, kv := range attrs {
			if keep[kv.Key] {
				result = append(result, kv)
			}
		}
		return result
	}

	if len(dropLabels) > 0 {
		// Keep everything except drop_labels.
		drop := make(map[string]bool, len(dropLabels))
		for _, d := range dropLabels {
			drop[d] = true
		}
		var result []*commonpb.KeyValue
		for _, kv := range attrs {
			if !drop[kv.Key] {
				result = append(result, kv)
			}
		}
		return result
	}

	// No grouping specified — keep all labels (aggregate across all series with same labels).
	result := make([]*commonpb.KeyValue, len(attrs))
	copy(result, attrs)
	return result
}

// buildGroupKey creates a deterministic string key from sorted labels.
func buildGroupKey(labels []*commonpb.KeyValue) string {
	if len(labels) == 0 {
		return ""
	}

	sorted := make([]*commonpb.KeyValue, len(labels))
	copy(sorted, labels)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Key < sorted[j].Key
	})

	// Pre-size: each label contributes key + "=" + value, separated by "|".
	size := len(sorted) - 1 // separators
	for _, kv := range sorted {
		size += len(kv.Key) + 1 + len(kv.Value.GetStringValue())
	}

	var b strings.Builder
	b.Grow(size)
	for i, kv := range sorted {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(kv.Key)
		b.WriteByte('=')
		b.WriteString(kv.Value.GetStringValue())
	}
	return b.String()
}

// formatQuantile formats a quantile for use in metric names (e.g. 0.99 → "99", 0.5 → "50").
func formatQuantile(q float64) string {
	p := q * 100
	if p == float64(int(p)) {
		return fmt.Sprintf("%d", int(p))
	}
	return fmt.Sprintf("%.1f", p)
}
