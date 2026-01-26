package limits

import (
	"fmt"
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

// groupStats tracks statistics for a specific label combination (group).
type groupStats struct {
	datapoints  int64                  // Total datapoints in current window
	cardinality map[string]struct{}    // Unique series keys
	windowEnd   time.Time              // When the current window expires
}

// ruleStats tracks all groups for a specific rule.
type ruleStats struct {
	groups      map[string]*groupStats // groupKey -> stats
	totalDPs    int64                  // Total datapoints across all groups
	totalCard   int64                  // Total cardinality across all groups
	windowEnd   time.Time              // Global window end
}

// Enforcer enforces limits on metrics based on configuration.
type Enforcer struct {
	mu     sync.RWMutex
	config *Config

	// Per-rule statistics tracking
	ruleStats map[string]*ruleStats // ruleName -> stats

	// Dropped groups (top offenders marked for dropping)
	droppedGroups map[string]map[string]time.Time // ruleName -> groupKey -> expiry

	// Violation counters for Prometheus
	violations *ViolationMetrics

	// Dry run mode (log only, don't actually drop)
	dryRun bool
}

// ViolationMetrics tracks limit violation counts.
type ViolationMetrics struct {
	mu sync.RWMutex

	// Counters by rule name
	datapointsExceeded  map[string]*atomic.Int64 // rule -> count
	cardinalityExceeded map[string]*atomic.Int64 // rule -> count
	datapointsDropped   map[string]*atomic.Int64 // rule -> count
	datapointsPassed    map[string]*atomic.Int64 // rule -> count
	groupsDropped       map[string]*atomic.Int64 // rule -> count (unique groups)
}

// NewEnforcer creates a new limits enforcer.
func NewEnforcer(config *Config, dryRun bool) *Enforcer {
	return &Enforcer{
		config:        config,
		ruleStats:     make(map[string]*ruleStats),
		droppedGroups: make(map[string]map[string]time.Time),
		violations: &ViolationMetrics{
			datapointsExceeded:  make(map[string]*atomic.Int64),
			cardinalityExceeded: make(map[string]*atomic.Int64),
			datapointsDropped:   make(map[string]*atomic.Int64),
			datapointsPassed:    make(map[string]*atomic.Int64),
			groupsDropped:       make(map[string]*atomic.Int64),
		},
		dryRun: dryRun,
	}
}

// Process processes metrics and enforces limits.
// Returns filtered metrics (may be modified if limits exceeded with adaptive action).
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

	// Build group key based on rule's GroupBy labels
	groupKey := e.buildGroupKey(rule, resourceAttrs, m)

	// Check if this group is currently marked as dropped
	if e.isGroupDropped(rule.Name, groupKey) {
		datapointsCount := countDatapoints(m)
		e.recordDrop(rule.Name, datapointsCount, false) // Not a new group drop
		if !e.dryRun {
			return nil
		}
		return m
	}

	// Update tracking and check limits
	exceeded, reason := e.updateAndCheckLimits(rule, groupKey, m, resourceAttrs)

	if !exceeded {
		e.recordPass(rule.Name, countDatapoints(m))
		return m
	}

	// Handle violation based on action
	return e.handleViolation(rule, groupKey, m, resourceAttrs, reason)
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

func (e *Enforcer) buildGroupKey(rule *Rule, resourceAttrs map[string]string, m *metricspb.Metric) string {
	// If GroupBy is specified, use only those labels
	if len(rule.GroupBy) > 0 {
		parts := make([]string, 0, len(rule.GroupBy))

		// Merge resource attrs with first datapoint attrs for grouping
		allAttrs := resourceAttrs
		dpAttrs := extractDatapointAttributes(m)
		if len(dpAttrs) > 0 {
			allAttrs = mergeAttrs(resourceAttrs, dpAttrs[0])
		}

		for _, label := range rule.GroupBy {
			if v, ok := allAttrs[label]; ok {
				parts = append(parts, fmt.Sprintf("%s=%s", label, v))
			}
		}
		return strings.Join(parts, ",")
	}

	// Default: use metric name as the group
	return m.Name
}

func (e *Enforcer) isGroupDropped(ruleName, groupKey string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if ruleDropped, ok := e.droppedGroups[ruleName]; ok {
		if expiry, ok := ruleDropped[groupKey]; ok {
			if time.Now().Before(expiry) {
				return true
			}
		}
	}
	return false
}

func (e *Enforcer) updateAndCheckLimits(rule *Rule, groupKey string, m *metricspb.Metric, resourceAttrs map[string]string) (exceeded bool, reason string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now()
	datapointsCount := countDatapoints(m)

	// Initialize rule stats if needed
	if _, ok := e.ruleStats[rule.Name]; !ok {
		e.ruleStats[rule.Name] = &ruleStats{
			groups:    make(map[string]*groupStats),
			windowEnd: now.Add(time.Minute),
		}
	}
	rs := e.ruleStats[rule.Name]

	// Reset if window expired
	if now.After(rs.windowEnd) {
		rs.groups = make(map[string]*groupStats)
		rs.totalDPs = 0
		rs.totalCard = 0
		rs.windowEnd = now.Add(time.Minute)
		// Also clear dropped groups for this rule
		delete(e.droppedGroups, rule.Name)
	}

	// Initialize group stats if needed
	if _, ok := rs.groups[groupKey]; !ok {
		rs.groups[groupKey] = &groupStats{
			cardinality: make(map[string]struct{}),
			windowEnd:   rs.windowEnd,
		}
	}
	gs := rs.groups[groupKey]

	// Calculate new series from this metric
	newSeries := 0
	dpAttrs := extractDatapointAttributes(m)
	for _, attrs := range dpAttrs {
		merged := mergeAttrs(resourceAttrs, attrs)
		seriesKey := buildSeriesKey(merged)
		if _, exists := gs.cardinality[seriesKey]; !exists {
			newSeries++
		}
	}

	// Check if adding this would exceed limits
	newTotalDPs := rs.totalDPs + int64(datapointsCount)
	newTotalCard := rs.totalCard + int64(newSeries)

	exceededDP := rule.MaxDatapointsRate > 0 && newTotalDPs > rule.MaxDatapointsRate
	exceededCard := rule.MaxCardinality > 0 && newTotalCard > rule.MaxCardinality

	if !exceededDP && !exceededCard {
		// Update tracking - within limits
		gs.datapoints += int64(datapointsCount)
		for _, attrs := range dpAttrs {
			merged := mergeAttrs(resourceAttrs, attrs)
			seriesKey := buildSeriesKey(merged)
			if _, exists := gs.cardinality[seriesKey]; !exists {
				gs.cardinality[seriesKey] = struct{}{}
				rs.totalCard++
			}
		}
		rs.totalDPs += int64(datapointsCount)
		return false, ""
	}

	// Limits exceeded
	if exceededCard {
		return true, "cardinality"
	}
	return true, "datapoints_rate"
}

func (e *Enforcer) handleViolation(rule *Rule, groupKey string, m *metricspb.Metric, resourceAttrs map[string]string, reason string) *metricspb.Metric {
	datapointsCount := countDatapoints(m)

	// Log the violation
	logging.Warn("limit exceeded", logging.F(
		"rule", rule.Name,
		"metric", m.Name,
		"group", groupKey,
		"reason", reason,
		"action", string(rule.Action),
		"dry_run", e.dryRun,
		"datapoints", datapointsCount,
	))

	// Record violation
	e.recordViolation(rule.Name, reason)

	switch rule.Action {
	case ActionLog:
		// Just log, pass through
		e.recordPass(rule.Name, datapointsCount)
		return m

	case ActionDrop:
		// Drop everything for this metric
		e.recordDrop(rule.Name, datapointsCount, false)
		if e.dryRun {
			return m
		}
		return nil

	case ActionAdaptive:
		// Adaptive: identify and drop top offenders
		return e.handleAdaptive(rule, groupKey, m, resourceAttrs, reason, datapointsCount)

	default:
		return m
	}
}

func (e *Enforcer) handleAdaptive(rule *Rule, groupKey string, m *metricspb.Metric, resourceAttrs map[string]string, reason string, datapointsCount int) *metricspb.Metric {
	e.mu.Lock()
	defer e.mu.Unlock()

	rs := e.ruleStats[rule.Name]
	if rs == nil {
		return m
	}

	// Find top offenders (groups with highest contribution)
	type groupContrib struct {
		key         string
		datapoints  int64
		cardinality int
	}

	contribs := make([]groupContrib, 0, len(rs.groups))
	for k, gs := range rs.groups {
		contribs = append(contribs, groupContrib{
			key:         k,
			datapoints:  gs.datapoints,
			cardinality: len(gs.cardinality),
		})
	}

	// Sort by contribution (descending) based on the violation reason
	if reason == "cardinality" {
		sort.Slice(contribs, func(i, j int) bool {
			return contribs[i].cardinality > contribs[j].cardinality
		})
	} else {
		sort.Slice(contribs, func(i, j int) bool {
			return contribs[i].datapoints > contribs[j].datapoints
		})
	}

	// Calculate how much we need to reduce
	var excess int64
	if reason == "cardinality" && rule.MaxCardinality > 0 {
		excess = rs.totalCard - rule.MaxCardinality
	} else if rule.MaxDatapointsRate > 0 {
		excess = rs.totalDPs - rule.MaxDatapointsRate
	}

	if excess <= 0 {
		return m
	}

	// Mark top offenders for dropping until we're under limit
	var reduced int64
	droppedCount := 0

	// Initialize dropped groups map for this rule
	if _, ok := e.droppedGroups[rule.Name]; !ok {
		e.droppedGroups[rule.Name] = make(map[string]time.Time)
	}

	for _, contrib := range contribs {
		if reduced >= excess {
			break
		}

		// Mark this group for dropping (until window end)
		e.droppedGroups[rule.Name][contrib.key] = rs.windowEnd
		droppedCount++

		if reason == "cardinality" {
			reduced += int64(contrib.cardinality)
		} else {
			reduced += contrib.datapoints
		}

		logging.Info("adaptive: marked group for dropping", logging.F(
			"rule", rule.Name,
			"group", contrib.key,
			"reason", reason,
			"contribution_datapoints", contrib.datapoints,
			"contribution_cardinality", contrib.cardinality,
		))
	}

	// Check if current group is now marked for dropping
	isCurrentDropped := false
	if _, ok := e.droppedGroups[rule.Name][groupKey]; ok {
		isCurrentDropped = true
	}

	// Record metrics
	e.recordGroupsDropped(rule.Name, droppedCount)

	if isCurrentDropped {
		e.recordDrop(rule.Name, datapointsCount, true)
		if e.dryRun {
			return m
		}
		return nil
	}

	// This group wasn't a top offender, pass through
	e.recordPass(rule.Name, datapointsCount)
	return m
}

func (e *Enforcer) recordViolation(ruleName, reason string) {
	e.violations.mu.Lock()
	defer e.violations.mu.Unlock()

	if _, ok := e.violations.datapointsExceeded[ruleName]; !ok {
		e.violations.datapointsExceeded[ruleName] = &atomic.Int64{}
	}
	if _, ok := e.violations.cardinalityExceeded[ruleName]; !ok {
		e.violations.cardinalityExceeded[ruleName] = &atomic.Int64{}
	}

	switch reason {
	case "datapoints_rate":
		e.violations.datapointsExceeded[ruleName].Add(1)
	case "cardinality":
		e.violations.cardinalityExceeded[ruleName].Add(1)
	}
}

func (e *Enforcer) recordDrop(ruleName string, count int, isNewGroup bool) {
	e.violations.mu.Lock()
	defer e.violations.mu.Unlock()

	if _, ok := e.violations.datapointsDropped[ruleName]; !ok {
		e.violations.datapointsDropped[ruleName] = &atomic.Int64{}
	}
	e.violations.datapointsDropped[ruleName].Add(int64(count))
}

func (e *Enforcer) recordPass(ruleName string, count int) {
	e.violations.mu.Lock()
	defer e.violations.mu.Unlock()

	if _, ok := e.violations.datapointsPassed[ruleName]; !ok {
		e.violations.datapointsPassed[ruleName] = &atomic.Int64{}
	}
	e.violations.datapointsPassed[ruleName].Add(int64(count))
}

func (e *Enforcer) recordGroupsDropped(ruleName string, count int) {
	e.violations.mu.Lock()
	defer e.violations.mu.Unlock()

	if _, ok := e.violations.groupsDropped[ruleName]; !ok {
		e.violations.groupsDropped[ruleName] = &atomic.Int64{}
	}
	e.violations.groupsDropped[ruleName].Add(int64(count))
}

// ServeHTTP implements http.Handler for Prometheus metrics endpoint.
func (e *Enforcer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.violations.mu.RLock()
	defer e.violations.mu.RUnlock()

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

	// Datapoints passed
	fmt.Fprintf(w, "# HELP metrics_governor_limit_datapoints_passed_total Datapoints passed through (within limits)\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_limit_datapoints_passed_total counter\n")
	for rule, counter := range e.violations.datapointsPassed {
		fmt.Fprintf(w, "metrics_governor_limit_datapoints_passed_total{rule=%q} %d\n", rule, counter.Load())
	}

	// Groups dropped (adaptive)
	fmt.Fprintf(w, "# HELP metrics_governor_limit_groups_dropped_total Groups (label combinations) dropped by adaptive limiting\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_limit_groups_dropped_total counter\n")
	for rule, counter := range e.violations.groupsDropped {
		fmt.Fprintf(w, "metrics_governor_limit_groups_dropped_total{rule=%q} %d\n", rule, counter.Load())
	}

	// Current tracking stats
	e.mu.RLock()
	defer e.mu.RUnlock()

	fmt.Fprintf(w, "# HELP metrics_governor_rule_current_datapoints Current datapoints in window per rule\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_rule_current_datapoints gauge\n")
	for rule, rs := range e.ruleStats {
		fmt.Fprintf(w, "metrics_governor_rule_current_datapoints{rule=%q} %d\n", rule, rs.totalDPs)
	}

	fmt.Fprintf(w, "# HELP metrics_governor_rule_current_cardinality Current cardinality in window per rule\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_rule_current_cardinality gauge\n")
	for rule, rs := range e.ruleStats {
		fmt.Fprintf(w, "metrics_governor_rule_current_cardinality{rule=%q} %d\n", rule, rs.totalCard)
	}

	fmt.Fprintf(w, "# HELP metrics_governor_rule_groups_total Number of tracked groups per rule\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_rule_groups_total gauge\n")
	for rule, rs := range e.ruleStats {
		fmt.Fprintf(w, "metrics_governor_rule_groups_total{rule=%q} %d\n", rule, len(rs.groups))
	}

	fmt.Fprintf(w, "# HELP metrics_governor_rule_dropped_groups_total Number of currently dropped groups per rule\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_rule_dropped_groups_total gauge\n")
	for rule, dropped := range e.droppedGroups {
		fmt.Fprintf(w, "metrics_governor_rule_dropped_groups_total{rule=%q} %d\n", rule, len(dropped))
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
