package prw

import (
	"regexp"
	"sync"
	"time"

	"github.com/szibis/metrics-governor/internal/logging"
)

// LimitsConfig holds the configuration for PRW limits enforcement.
type LimitsConfig struct {
	// Rules is the list of limit rules to apply.
	Rules []LimitRule
	// DryRun when true logs violations without dropping data.
	DryRun bool
	// LogInterval is the interval for aggregated logging.
	LogInterval time.Duration
}

// LimitRule defines a single limit rule for PRW data.
type LimitRule struct {
	// Name is the name of the rule for logging purposes.
	Name string
	// MetricPattern is a regex pattern to match metric names.
	MetricPattern string
	// LabelPatterns is a map of label names to regex patterns.
	LabelPatterns map[string]string
	// MaxDatapointsPerSecond limits datapoints per second for matching metrics.
	MaxDatapointsPerSecond int64
	// MaxCardinality limits the number of unique time series.
	MaxCardinality int64
	// Action is what to do when the limit is exceeded (drop, log).
	Action LimitAction

	// Compiled patterns
	metricRegex *regexp.Regexp
	labelRegexes map[string]*regexp.Regexp
}

// LimitAction defines what happens when a limit is exceeded.
type LimitAction string

const (
	// LimitActionDrop drops the metric when the limit is exceeded.
	LimitActionDrop LimitAction = "drop"
	// LimitActionLog logs but does not drop when the limit is exceeded.
	LimitActionLog LimitAction = "log"
)

// LimitsEnforcer enforces limits on PRW data.
type LimitsEnforcer struct {
	config LimitsConfig
	dryRun bool

	// Tracking state
	mu              sync.RWMutex
	cardinalityMap  map[string]map[uint64]struct{} // rule name -> series hash -> exists
	datapointCounts map[string]int64               // rule name -> count since last reset
	lastReset       time.Time

	// Stats
	totalDropped int64
	totalViolations int64
}

// NewLimitsEnforcer creates a new PRW limits enforcer.
func NewLimitsEnforcer(config LimitsConfig, dryRun bool) *LimitsEnforcer {
	// Compile regex patterns
	for i := range config.Rules {
		if config.Rules[i].MetricPattern != "" {
			if re, err := regexp.Compile(config.Rules[i].MetricPattern); err == nil {
				config.Rules[i].metricRegex = re
			} else {
				logging.Error("invalid metric pattern regex", logging.F(
					"rule", config.Rules[i].Name,
					"pattern", config.Rules[i].MetricPattern,
					"error", err.Error(),
				))
			}
		}

		if len(config.Rules[i].LabelPatterns) > 0 {
			config.Rules[i].labelRegexes = make(map[string]*regexp.Regexp)
			for labelName, pattern := range config.Rules[i].LabelPatterns {
				if re, err := regexp.Compile(pattern); err == nil {
					config.Rules[i].labelRegexes[labelName] = re
				} else {
					logging.Error("invalid label pattern regex", logging.F(
						"rule", config.Rules[i].Name,
						"label", labelName,
						"pattern", pattern,
						"error", err.Error(),
					))
				}
			}
		}

		// Default action
		if config.Rules[i].Action == "" {
			config.Rules[i].Action = LimitActionDrop
		}
	}

	return &LimitsEnforcer{
		config:          config,
		dryRun:          dryRun,
		cardinalityMap:  make(map[string]map[uint64]struct{}),
		datapointCounts: make(map[string]int64),
		lastReset:       time.Now(),
	}
}

// Process applies limits to a WriteRequest and returns the filtered request.
func (e *LimitsEnforcer) Process(req *WriteRequest) *WriteRequest {
	if req == nil || len(e.config.Rules) == 0 {
		return req
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Reset counters every second
	now := time.Now()
	if now.Sub(e.lastReset) >= time.Second {
		e.datapointCounts = make(map[string]int64)
		e.lastReset = now
	}

	filteredTimeseries := make([]TimeSeries, 0, len(req.Timeseries))

	for i := range req.Timeseries {
		ts := &req.Timeseries[i]
		keep := true
		var violatedRule *LimitRule

		for j := range e.config.Rules {
			rule := &e.config.Rules[j]

			if !e.matchesRule(ts, rule) {
				continue
			}

			// Check datapoints per second limit
			if rule.MaxDatapointsPerSecond > 0 {
				dpCount := int64(ts.DatapointCount())
				currentCount := e.datapointCounts[rule.Name]
				if currentCount+dpCount > rule.MaxDatapointsPerSecond {
					keep = false
					violatedRule = rule
					e.totalViolations++
					break
				}
				e.datapointCounts[rule.Name] = currentCount + dpCount
			}

			// Check cardinality limit
			if rule.MaxCardinality > 0 {
				ts.SortLabels()
				hash := ts.LabelsHash()

				if e.cardinalityMap[rule.Name] == nil {
					e.cardinalityMap[rule.Name] = make(map[uint64]struct{})
				}

				cardMap := e.cardinalityMap[rule.Name]
				if _, exists := cardMap[hash]; !exists {
					if int64(len(cardMap)) >= rule.MaxCardinality {
						keep = false
						violatedRule = rule
						e.totalViolations++
						break
					}
					cardMap[hash] = struct{}{}
				}
			}
		}

		if !keep && violatedRule != nil {
			if e.dryRun || violatedRule.Action == LimitActionLog {
				// Log but don't drop in dry run mode
				// Note: Using Info for dry run violations as Debug is not available
				filteredTimeseries = append(filteredTimeseries, *ts)
			} else {
				// Actually drop
				e.totalDropped++
			}
		} else {
			filteredTimeseries = append(filteredTimeseries, *ts)
		}
	}

	if len(filteredTimeseries) == 0 {
		return nil
	}

	return &WriteRequest{
		Timeseries: filteredTimeseries,
		Metadata:   req.Metadata,
	}
}

// matchesRule checks if a time series matches a rule's patterns.
func (e *LimitsEnforcer) matchesRule(ts *TimeSeries, rule *LimitRule) bool {
	// Check metric name pattern
	if rule.metricRegex != nil {
		metricName := ts.MetricName()
		if !rule.metricRegex.MatchString(metricName) {
			return false
		}
	}

	// Check label patterns
	if len(rule.labelRegexes) > 0 {
		for labelName, re := range rule.labelRegexes {
			value := ts.GetLabelValue(labelName)
			if !re.MatchString(value) {
				return false
			}
		}
	}

	// If no patterns specified, rule matches everything
	return rule.metricRegex != nil || len(rule.labelRegexes) > 0
}

// Stats returns current limit enforcement statistics.
func (e *LimitsEnforcer) Stats() (dropped, violations int64) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.totalDropped, e.totalViolations
}

// ResetStats resets the statistics counters.
func (e *LimitsEnforcer) ResetStats() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.totalDropped = 0
	e.totalViolations = 0
}

// ResetCardinality resets the cardinality tracking.
// This should be called periodically to allow new series to be tracked.
func (e *LimitsEnforcer) ResetCardinality() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cardinalityMap = make(map[string]map[uint64]struct{})
}

// Stop stops the limits enforcer.
func (e *LimitsEnforcer) Stop() {
	// Clean up resources if needed
}
