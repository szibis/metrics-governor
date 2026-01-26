package limits

import (
	"fmt"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/slawomirskowron/metrics-governor/internal/logging"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// Enforcer enforces limits on metrics based on configuration.
type Enforcer struct {
	mu     sync.RWMutex
	config *Config

	// Rate tracking (per minute windows)
	rateWindows map[string]*rateWindow

	// Cardinality tracking
	cardinality map[string]map[string]struct{}

	// Violation counters for Prometheus
	violations *ViolationMetrics

	// Dry run mode (log only, don't actually drop/sample)
	dryRun bool
}

// rateWindow tracks datapoints in a time window.
type rateWindow struct {
	count     int64
	windowEnd time.Time
}

// ViolationMetrics tracks limit violation counts.
type ViolationMetrics struct {
	mu sync.RWMutex

	// Counters by rule name and action
	datapointsExceeded map[string]*atomic.Int64 // rule -> count
	cardinalityExceeded map[string]*atomic.Int64 // rule -> count
	datapointsDropped   map[string]*atomic.Int64 // rule -> count
	datapointsSampled   map[string]*atomic.Int64 // rule -> count
}

// NewEnforcer creates a new limits enforcer.
func NewEnforcer(config *Config, dryRun bool) *Enforcer {
	return &Enforcer{
		config:      config,
		rateWindows: make(map[string]*rateWindow),
		cardinality: make(map[string]map[string]struct{}),
		violations: &ViolationMetrics{
			datapointsExceeded:  make(map[string]*atomic.Int64),
			cardinalityExceeded: make(map[string]*atomic.Int64),
			datapointsDropped:   make(map[string]*atomic.Int64),
			datapointsSampled:   make(map[string]*atomic.Int64),
		},
		dryRun: dryRun,
	}
}

// Process processes metrics and enforces limits.
// Returns filtered metrics (may be modified if action is drop/sample).
func (e *Enforcer) Process(resourceMetrics []*metricspb.ResourceMetrics) []*metricspb.ResourceMetrics {
	if e.config == nil {
		return resourceMetrics
	}

	result := make([]*metricspb.ResourceMetrics, 0, len(resourceMetrics))

	for _, rm := range resourceMetrics {
		resourceAttrs := extractAttributes(rm.Resource.GetAttributes())
		filteredRM := e.processResourceMetrics(rm, resourceAttrs)
		if filteredRM != nil {
			result = append(result, filteredRM)
		}
	}

	return result
}

func (e *Enforcer) processResourceMetrics(rm *metricspb.ResourceMetrics, resourceAttrs map[string]string) *metricspb.ResourceMetrics {
	filteredScopeMetrics := make([]*metricspb.ScopeMetrics, 0, len(rm.ScopeMetrics))

	for _, sm := range rm.ScopeMetrics {
		filteredMetrics := make([]*metricspb.Metric, 0, len(sm.Metrics))

		for _, m := range sm.Metrics {
			filteredMetric := e.processMetric(m, resourceAttrs)
			if filteredMetric != nil {
				filteredMetrics = append(filteredMetrics, filteredMetric)
			}
		}

		if len(filteredMetrics) > 0 {
			filteredSM := &metricspb.ScopeMetrics{
				Scope:   sm.Scope,
				Metrics: filteredMetrics,
			}
			filteredScopeMetrics = append(filteredScopeMetrics, filteredSM)
		}
	}

	if len(filteredScopeMetrics) == 0 {
		return nil
	}

	return &metricspb.ResourceMetrics{
		Resource:     rm.Resource,
		ScopeMetrics: filteredScopeMetrics,
	}
}

func (e *Enforcer) processMetric(m *metricspb.Metric, resourceAttrs map[string]string) *metricspb.Metric {
	metricName := m.Name

	// Find matching rule
	rule := e.findMatchingRule(metricName, resourceAttrs)
	if rule == nil {
		return m // No rule matches, pass through
	}

	// Build rule key for tracking
	ruleKey := e.buildRuleKey(rule, metricName, resourceAttrs)

	// Check limits
	datapointsCount := countDatapoints(m)
	exceeded, reason := e.checkLimits(rule, ruleKey, metricName, m, resourceAttrs, datapointsCount)

	if !exceeded {
		// Update tracking
		e.updateTracking(ruleKey, metricName, m, resourceAttrs, datapointsCount)
		return m
	}

	// Handle violation based on action
	return e.handleViolation(rule, ruleKey, m, reason, datapointsCount)
}

func (e *Enforcer) findMatchingRule(metricName string, labels map[string]string) *Rule {
	for i := range e.config.Rules {
		rule := &e.config.Rules[i]
		if rule.Matches(metricName, labels) {
			return rule
		}
	}
	return nil
}

func (e *Enforcer) buildRuleKey(rule *Rule, metricName string, labels map[string]string) string {
	// Build a unique key for this rule + metric + labels combination
	parts := []string{rule.Name, metricName}

	if len(rule.Match.Labels) > 0 {
		keys := make([]string, 0, len(rule.Match.Labels))
		for k := range rule.Match.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for _, k := range keys {
			if v, ok := labels[k]; ok {
				parts = append(parts, fmt.Sprintf("%s=%s", k, v))
			}
		}
	}

	return strings.Join(parts, "|")
}

func (e *Enforcer) checkLimits(rule *Rule, ruleKey, metricName string, m *metricspb.Metric, labels map[string]string, datapointsCount int) (exceeded bool, reason string) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	now := time.Now()

	// Check datapoints rate limit
	if rule.MaxDatapointsRate > 0 {
		window, ok := e.rateWindows[ruleKey]
		if ok && now.Before(window.windowEnd) {
			if window.count+int64(datapointsCount) > rule.MaxDatapointsRate {
				return true, "datapoints_rate"
			}
		}
	}

	// Check cardinality limit
	if rule.MaxCardinality > 0 {
		cardMap, ok := e.cardinality[ruleKey]
		if ok {
			newSeries := e.countNewSeries(m, labels, cardMap)
			if int64(len(cardMap))+int64(newSeries) > rule.MaxCardinality {
				return true, "cardinality"
			}
		}
	}

	return false, ""
}

func (e *Enforcer) countNewSeries(m *metricspb.Metric, resourceAttrs map[string]string, existing map[string]struct{}) int {
	newCount := 0
	allAttrs := extractDatapointAttributes(m)

	for _, attrs := range allAttrs {
		merged := mergeAttrs(resourceAttrs, attrs)
		seriesKey := buildSeriesKey(merged)
		if _, ok := existing[seriesKey]; !ok {
			newCount++
		}
	}

	return newCount
}

func (e *Enforcer) updateTracking(ruleKey, metricName string, m *metricspb.Metric, resourceAttrs map[string]string, datapointsCount int) {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now()

	// Update rate window
	window, ok := e.rateWindows[ruleKey]
	if !ok || now.After(window.windowEnd) {
		e.rateWindows[ruleKey] = &rateWindow{
			count:     int64(datapointsCount),
			windowEnd: now.Add(time.Minute),
		}
	} else {
		window.count += int64(datapointsCount)
	}

	// Update cardinality
	if _, ok := e.cardinality[ruleKey]; !ok {
		e.cardinality[ruleKey] = make(map[string]struct{})
	}

	allAttrs := extractDatapointAttributes(m)
	for _, attrs := range allAttrs {
		merged := mergeAttrs(resourceAttrs, attrs)
		seriesKey := buildSeriesKey(merged)
		e.cardinality[ruleKey][seriesKey] = struct{}{}
	}
}

func (e *Enforcer) handleViolation(rule *Rule, ruleKey string, m *metricspb.Metric, reason string, datapointsCount int) *metricspb.Metric {
	action := rule.Action

	// Log the violation
	logging.Warn("limit exceeded", logging.F(
		"rule", rule.Name,
		"metric", m.Name,
		"reason", reason,
		"action", string(action),
		"dry_run", e.dryRun,
		"datapoints", datapointsCount,
	))

	// Update violation metrics
	e.recordViolation(rule.Name, reason, action, datapointsCount)

	// In dry run mode, always pass through
	if e.dryRun {
		return m
	}

	switch action {
	case ActionLog:
		return m // Pass through

	case ActionDrop:
		return nil // Drop entirely

	case ActionSample:
		return e.sampleMetric(m, rule.SampleRate)

	default:
		return m
	}
}

func (e *Enforcer) sampleMetric(m *metricspb.Metric, sampleRate float64) *metricspb.Metric {
	// Simple random sampling - keep datapoint if random < sampleRate
	if rand.Float64() < sampleRate {
		return m
	}
	return nil
}

func (e *Enforcer) recordViolation(ruleName, reason string, action Action, datapointsCount int) {
	e.violations.mu.Lock()
	defer e.violations.mu.Unlock()

	// Initialize counters if needed
	if _, ok := e.violations.datapointsExceeded[ruleName]; !ok {
		e.violations.datapointsExceeded[ruleName] = &atomic.Int64{}
	}
	if _, ok := e.violations.cardinalityExceeded[ruleName]; !ok {
		e.violations.cardinalityExceeded[ruleName] = &atomic.Int64{}
	}
	if _, ok := e.violations.datapointsDropped[ruleName]; !ok {
		e.violations.datapointsDropped[ruleName] = &atomic.Int64{}
	}
	if _, ok := e.violations.datapointsSampled[ruleName]; !ok {
		e.violations.datapointsSampled[ruleName] = &atomic.Int64{}
	}

	// Record violation type
	switch reason {
	case "datapoints_rate":
		e.violations.datapointsExceeded[ruleName].Add(1)
	case "cardinality":
		e.violations.cardinalityExceeded[ruleName].Add(1)
	}

	// Record action taken
	switch action {
	case ActionDrop:
		e.violations.datapointsDropped[ruleName].Add(int64(datapointsCount))
	case ActionSample:
		e.violations.datapointsSampled[ruleName].Add(int64(datapointsCount))
	}
}

// ServeHTTP implements http.Handler for Prometheus metrics endpoint.
func (e *Enforcer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.violations.mu.RLock()
	defer e.violations.mu.RUnlock()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	// Datapoints rate exceeded
	fmt.Fprintf(w, "# HELP metrics_governor_limit_datapoints_exceeded_total Times datapoints rate limit was exceeded\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_limit_datapoints_exceeded_total counter\n")
	for rule, counter := range e.violations.datapointsExceeded {
		fmt.Fprintf(w, "metrics_governor_limit_datapoints_exceeded_total{rule=%q} %d\n", rule, counter.Load())
	}

	// Cardinality exceeded
	fmt.Fprintf(w, "# HELP metrics_governor_limit_cardinality_exceeded_total Times cardinality limit was exceeded\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_limit_cardinality_exceeded_total counter\n")
	for rule, counter := range e.violations.cardinalityExceeded {
		fmt.Fprintf(w, "metrics_governor_limit_cardinality_exceeded_total{rule=%q} %d\n", rule, counter.Load())
	}

	// Datapoints dropped
	fmt.Fprintf(w, "# HELP metrics_governor_limit_datapoints_dropped_total Datapoints dropped due to limits\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_limit_datapoints_dropped_total counter\n")
	for rule, counter := range e.violations.datapointsDropped {
		fmt.Fprintf(w, "metrics_governor_limit_datapoints_dropped_total{rule=%q} %d\n", rule, counter.Load())
	}

	// Datapoints sampled
	fmt.Fprintf(w, "# HELP metrics_governor_limit_datapoints_sampled_total Datapoints affected by sampling\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_limit_datapoints_sampled_total counter\n")
	for rule, counter := range e.violations.datapointsSampled {
		fmt.Fprintf(w, "metrics_governor_limit_datapoints_sampled_total{rule=%q} %d\n", rule, counter.Load())
	}
}

// Helper functions

func extractAttributes(attrs []*commonpb.KeyValue) map[string]string {
	result := make(map[string]string)
	for _, kv := range attrs {
		if kv.Value != nil {
			if sv := kv.Value.GetStringValue(); sv != "" {
				result[kv.Key] = sv
			}
		}
	}
	return result
}

func extractDatapointAttributes(m *metricspb.Metric) []map[string]string {
	var allAttrs []map[string]string

	switch d := m.Data.(type) {
	case *metricspb.Metric_Gauge:
		for _, dp := range d.Gauge.DataPoints {
			allAttrs = append(allAttrs, extractAttributes(dp.Attributes))
		}
	case *metricspb.Metric_Sum:
		for _, dp := range d.Sum.DataPoints {
			allAttrs = append(allAttrs, extractAttributes(dp.Attributes))
		}
	case *metricspb.Metric_Histogram:
		for _, dp := range d.Histogram.DataPoints {
			allAttrs = append(allAttrs, extractAttributes(dp.Attributes))
		}
	case *metricspb.Metric_ExponentialHistogram:
		for _, dp := range d.ExponentialHistogram.DataPoints {
			allAttrs = append(allAttrs, extractAttributes(dp.Attributes))
		}
	case *metricspb.Metric_Summary:
		for _, dp := range d.Summary.DataPoints {
			allAttrs = append(allAttrs, extractAttributes(dp.Attributes))
		}
	}

	return allAttrs
}

func countDatapoints(m *metricspb.Metric) int {
	switch d := m.Data.(type) {
	case *metricspb.Metric_Gauge:
		return len(d.Gauge.DataPoints)
	case *metricspb.Metric_Sum:
		return len(d.Sum.DataPoints)
	case *metricspb.Metric_Histogram:
		return len(d.Histogram.DataPoints)
	case *metricspb.Metric_ExponentialHistogram:
		return len(d.ExponentialHistogram.DataPoints)
	case *metricspb.Metric_Summary:
		return len(d.Summary.DataPoints)
	}
	return 0
}

func mergeAttrs(a, b map[string]string) map[string]string {
	result := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		result[k] = v
	}
	for k, v := range b {
		result[k] = v
	}
	return result
}

func buildSeriesKey(attrs map[string]string) string {
	if len(attrs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, attrs[k]))
	}
	return strings.Join(parts, ",")
}
