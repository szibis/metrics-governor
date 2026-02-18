package autotune

import (
	"context"
	"fmt"
	"time"
)

// CardinalityData holds normalized cardinality data from any backend.
type CardinalityData struct {
	TotalSeries int64
	// TopMetrics is the top-N metrics by series count.
	TopMetrics []MetricCardinality
	// TopLabelValues is per-label-value series counts (from VM explorer / Mimir).
	TopLabelValues map[string][]LabelValueCardinality
	// FetchedAt is when the data was fetched.
	FetchedAt time.Time
	// Backend identifies which backend produced this data.
	Backend Backend
}

// MetricCardinality holds series count for a single metric name.
type MetricCardinality struct {
	Name        string
	SeriesCount int64
}

// LabelValueCardinality holds series count for a single label value.
type LabelValueCardinality struct {
	Value       string
	SeriesCount int64
}

// AggregatedSignals holds all signals collected in a single autotune cycle.
type AggregatedSignals struct {
	// Utilization is per-rule utilization ratio (0.0-1.0).
	// Key is rule name; value is max(cardinality_utilization, dp_rate_utilization).
	Utilization map[string]float64

	// CardinalityUtilization is per-rule cardinality utilization (0.0-1.0).
	CardinalityUtilization map[string]float64

	// DPRateUtilization is per-rule datapoints-rate utilization (0.0-1.0).
	DPRateUtilization map[string]float64

	// ExternalCardinality is cardinality data from the external source (Tier 1).
	// nil when Tier 1 is not enabled.
	ExternalCardinality *CardinalityData

	// AnomalyScores is per-metric anomaly scores from vmanomaly (Tier 2).
	// nil when Tier 2 is not enabled.
	AnomalyScores map[string]float64

	// InternalDropRate is the current drop rate from the enforcer (always available).
	InternalDropRate float64

	// InternalDropTotal is the total drops since start.
	InternalDropTotal int64

	// RuleUtilization is per-rule utilization from the enforcer (always available).
	RuleUtilization map[string]RuleUtilization

	// Timestamp is when this signal snapshot was created.
	Timestamp time.Time
}

// RuleUtilization holds utilization data for a single enforcer rule.
type RuleUtilization struct {
	Name        string
	MaxCard     int64
	MaxDPRate   int64
	CurrentCard int64
	CurrentDPs  int64
}

// Decision represents a single autotune decision for a rule.
type Decision struct {
	RuleName  string
	Field     string // "max_cardinality" or "max_datapoints_rate"
	OldValue  int64
	NewValue  int64
	Action    Action
	Reason    string
	Signals   DecisionSignals
	Timestamp time.Time
}

// Action is the type of autotune action.
type Action string

const (
	ActionHold     Action = "hold"
	ActionIncrease Action = "increase"
	ActionDecrease Action = "decrease"
	ActionTighten  Action = "tighten"
	ActionRollback Action = "rollback"
)

// DecisionSignals captures the signal snapshot that drove a decision.
type DecisionSignals struct {
	Utilization    float64
	AnomalyScore   float64
	DropRate       float64
	ExternalSeries int64
}

// ConfigChange records a single autotune adjustment for persistence/audit.
type ConfigChange struct {
	Timestamp time.Time
	Domain    string // "limits", "relabel", "processing", etc.
	RuleName  string
	Field     string
	OldValue  int64
	NewValue  int64
	Action    string
	Reason    string
	Signals   DecisionSignals
}

// --- Pluggable Interfaces ---

// CardinalitySource fetches cardinality data from a backend.
type CardinalitySource interface {
	FetchCardinality(ctx context.Context) (*CardinalityData, error)
}

// AnomalySource fetches anomaly scores from vmanomaly or similar.
type AnomalySource interface {
	FetchAnomalyScores(ctx context.Context, metrics []string) (map[string]float64, error)
}

// AIAdvisor generates recommendations using an LLM.
type AIAdvisor interface {
	GenerateRecommendations(ctx context.Context, signals AggregatedSignals) ([]Decision, error)
}

// ChangePersister persists and loads autotune change history.
type ChangePersister interface {
	Persist(ctx context.Context, changes []ConfigChange) error
	LoadHistory(ctx context.Context) ([]ConfigChange, error)
}

// LeaderElector determines if this instance should run autotune.
type LeaderElector interface {
	IsLeader() bool
	Start(ctx context.Context) error
	Stop()
}

// ConfigPropagator distributes config changes from leader to followers.
type ConfigPropagator interface {
	Propagate(ctx context.Context, changes []ConfigChange) error
	ReceiveUpdates(handler func([]ConfigChange))
	Start(ctx context.Context) error
	Stop()
}

// SignalAggregator collects signals from all sources into AggregatedSignals.
type SignalAggregator struct {
	source    CardinalitySource
	anomaly   AnomalySource
	dropsFunc func() int64
	utilFunc  func() map[string]RuleUtilization
}

// NewSignalAggregator creates a signal aggregator with the given sources.
func NewSignalAggregator(
	source CardinalitySource,
	anomaly AnomalySource,
	dropsFunc func() int64,
	utilFunc func() map[string]RuleUtilization,
) *SignalAggregator {
	return &SignalAggregator{
		source:    source,
		anomaly:   anomaly,
		dropsFunc: dropsFunc,
		utilFunc:  utilFunc,
	}
}

// Collect gathers signals from all available sources.
func (sa *SignalAggregator) Collect(ctx context.Context) (AggregatedSignals, error) {
	now := time.Now()
	signals := AggregatedSignals{
		Utilization:            make(map[string]float64),
		CardinalityUtilization: make(map[string]float64),
		DPRateUtilization:      make(map[string]float64),
		RuleUtilization:        make(map[string]RuleUtilization),
		Timestamp:              now,
	}

	// Always collect internal signals.
	if sa.dropsFunc != nil {
		signals.InternalDropTotal = sa.dropsFunc()
	}
	if sa.utilFunc != nil {
		ruleUtils := sa.utilFunc()
		for name, ru := range ruleUtils {
			signals.RuleUtilization[name] = ru

			var cardUtil, dpUtil float64
			if ru.MaxCard > 0 {
				cardUtil = float64(ru.CurrentCard) / float64(ru.MaxCard)
			}
			if ru.MaxDPRate > 0 {
				dpUtil = float64(ru.CurrentDPs) / float64(ru.MaxDPRate)
			}
			signals.CardinalityUtilization[name] = cardUtil
			signals.DPRateUtilization[name] = dpUtil

			// Overall utilization is the max of both dimensions.
			util := cardUtil
			if dpUtil > util {
				util = dpUtil
			}
			signals.Utilization[name] = util
		}
	}

	// Tier 1: external cardinality source.
	if sa.source != nil {
		data, err := sa.source.FetchCardinality(ctx)
		if err != nil {
			return signals, fmt.Errorf("cardinality source: %w", err)
		}
		signals.ExternalCardinality = data
	}

	// Tier 2: anomaly scores.
	if sa.anomaly != nil {
		// Collect metric names to query.
		var metricNames []string
		for name := range signals.RuleUtilization {
			metricNames = append(metricNames, name)
		}
		if len(metricNames) > 0 {
			scores, err := sa.anomaly.FetchAnomalyScores(ctx, metricNames)
			if err != nil {
				return signals, fmt.Errorf("anomaly source: %w", err)
			}
			signals.AnomalyScores = scores
		}
	}

	return signals, nil
}
