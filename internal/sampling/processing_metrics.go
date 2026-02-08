package sampling

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Processing rule metrics.
var (
	processingRuleEvaluationsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_processing_rule_evaluations_total",
		Help: "Total datapoints that matched a processing rule",
	}, []string{"rule", "action"})

	processingRuleInputTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_processing_rule_input_total",
		Help: "Datapoints ingested by a processing rule",
	}, []string{"rule", "action"})

	processingRuleOutputTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_processing_rule_output_total",
		Help: "Datapoints emitted by a processing rule",
	}, []string{"rule", "action", "function"})

	processingRuleDroppedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_processing_rule_dropped_total",
		Help: "Datapoints dropped by a processing rule",
	}, []string{"rule", "action"})

	processingRuleDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "metrics_governor_processing_rule_duration_seconds",
		Help:    "Per-rule processing duration",
		Buckets: []float64{0.00001, 0.0001, 0.001, 0.005, 0.01, 0.05, 0.1},
	}, []string{"rule", "action"})
)

// Overall processing stage metrics.
var (
	processingInputDatapointsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_processing_input_datapoints_total",
		Help: "Total datapoints entering the processing stage",
	})

	processingOutputDatapointsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_processing_output_datapoints_total",
		Help: "Total datapoints exiting the processing stage",
	})

	processingDroppedDatapointsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_processing_dropped_datapoints_total",
		Help: "Total datapoints dropped across all processing rules",
	})

	processingDuration = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "metrics_governor_processing_duration_seconds",
		Help:    "Overall processing stage duration",
		Buckets: []float64{0.00001, 0.0001, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5},
	})

	processingConfigReloadsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_processing_config_reloads_total",
		Help: "Processing config reload count by result",
	}, []string{"result"})

	processingRulesActive = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "metrics_governor_processing_rules_active",
		Help: "Current number of active processing rules by action type",
	}, []string{"action"})

	processingMemoryBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_processing_memory_bytes",
		Help: "Estimated memory used by processing state (aggregate groups + downsample series)",
	})
)

// Aggregate-specific metrics.
var (
	processingAggregateGroupsActive = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "metrics_governor_processing_aggregate_groups_active",
		Help: "Current active aggregation groups per rule",
	}, []string{"rule"})

	processingAggregateGroupsCreatedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_processing_aggregate_groups_created_total",
		Help: "Total aggregation groups ever created per rule",
	}, []string{"rule"})

	processingAggregateStaleCleaned = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_processing_aggregate_stale_cleaned_total",
		Help: "Stale aggregation groups cleaned up per rule",
	}, []string{"rule"})

	processingAggregateFlushDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "metrics_governor_processing_aggregate_flush_duration_seconds",
		Help:    "Duration of aggregate flush operations per rule",
		Buckets: []float64{0.0001, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0},
	}, []string{"rule"})

	processingAggregateWindowsCompletedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_processing_aggregate_windows_completed_total",
		Help: "Completed aggregate windows per rule",
	}, []string{"rule"})
)

// Downsample-specific metrics.
var (
	processingDownsampleActiveSeries = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "metrics_governor_processing_downsample_active_series",
		Help: "Active series being downsampled per rule and method",
	}, []string{"rule", "method"})
)

// Transform-specific metrics.
var (
	processingTransformLabelsAddedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_processing_transform_labels_added_total",
		Help: "Labels added by transform operations per rule",
	}, []string{"rule"})

	processingTransformLabelsRemovedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_processing_transform_labels_removed_total",
		Help: "Labels removed by transform operations per rule",
	}, []string{"rule"})

	processingTransformLabelsModifiedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_processing_transform_labels_modified_total",
		Help: "Labels modified by transform operations per rule",
	}, []string{"rule"})

	processingTransformOperationsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_processing_transform_operations_total",
		Help: "Transform operation count per rule and operation type",
	}, []string{"rule", "operation"})
)

func init() {
	// Per-rule metrics
	prometheus.MustRegister(processingRuleEvaluationsTotal)
	prometheus.MustRegister(processingRuleInputTotal)
	prometheus.MustRegister(processingRuleOutputTotal)
	prometheus.MustRegister(processingRuleDroppedTotal)
	prometheus.MustRegister(processingRuleDuration)

	// Overall stage metrics
	prometheus.MustRegister(processingInputDatapointsTotal)
	prometheus.MustRegister(processingOutputDatapointsTotal)
	prometheus.MustRegister(processingDroppedDatapointsTotal)
	prometheus.MustRegister(processingDuration)
	prometheus.MustRegister(processingConfigReloadsTotal)
	prometheus.MustRegister(processingRulesActive)
	prometheus.MustRegister(processingMemoryBytes)

	// Aggregate metrics
	prometheus.MustRegister(processingAggregateGroupsActive)
	prometheus.MustRegister(processingAggregateGroupsCreatedTotal)
	prometheus.MustRegister(processingAggregateStaleCleaned)
	prometheus.MustRegister(processingAggregateFlushDuration)
	prometheus.MustRegister(processingAggregateWindowsCompletedTotal)

	// Downsample metrics
	prometheus.MustRegister(processingDownsampleActiveSeries)

	// Transform metrics
	prometheus.MustRegister(processingTransformLabelsAddedTotal)
	prometheus.MustRegister(processingTransformLabelsRemovedTotal)
	prometheus.MustRegister(processingTransformLabelsModifiedTotal)
	prometheus.MustRegister(processingTransformOperationsTotal)

	// Initialize zero values for overall metrics
	processingInputDatapointsTotal.Add(0)
	processingOutputDatapointsTotal.Add(0)
	processingDroppedDatapointsTotal.Add(0)
	processingMemoryBytes.Set(0)
}

// updateProcessingRulesActive updates the active rules gauge from a config.
func updateProcessingRulesActive(rules []ProcessingRule) {
	counts := make(map[Action]int)
	for _, r := range rules {
		counts[r.Action]++
	}
	for _, action := range []Action{ActionSample, ActionDownsample, ActionAggregate, ActionTransform, ActionDrop} {
		processingRulesActive.WithLabelValues(string(action)).Set(float64(counts[action]))
	}
}
