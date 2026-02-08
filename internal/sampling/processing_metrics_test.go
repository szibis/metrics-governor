package sampling

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// getCounterValue reads the current value of a CounterVec for the given labels.
func getCounterValue(cv *prometheus.CounterVec, labels ...string) float64 {
	m := &dto.Metric{}
	cv.WithLabelValues(labels...).Write(m)
	return m.Counter.GetValue()
}

// getGaugeValue reads the current value of a GaugeVec for the given labels.
func getGaugeValue(gv *prometheus.GaugeVec, labels ...string) float64 {
	m := &dto.Metric{}
	gv.WithLabelValues(labels...).Write(m)
	return m.Gauge.GetValue()
}

// getCounterScalarValue reads the current value of a plain Counter.
func getCounterScalarValue(c prometheus.Counter) float64 {
	m := &dto.Metric{}
	c.(prometheus.Metric).Write(m)
	return m.Counter.GetValue()
}

// getHistogramCount reads the sample count from a Histogram.
func getHistogramCount(h prometheus.Histogram) uint64 {
	m := &dto.Metric{}
	h.(prometheus.Metric).Write(m)
	return m.Histogram.GetSampleCount()
}

// TestMetrics_DropRuleCounters verifies that a drop action increments
// evaluation and dropped counters by the correct amount.
func TestMetrics_DropRuleCounters(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "m-drop-1", Input: "mdrop1_.*", Action: ActionDrop},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	evalBefore := getCounterValue(processingRuleEvaluationsTotal, "m-drop-1", "drop")
	droppedBefore := getCounterValue(processingRuleDroppedTotal, "m-drop-1", "drop")
	inputBefore := getCounterValue(processingRuleInputTotal, "m-drop-1", "drop")
	overallDropBefore := getCounterScalarValue(processingDroppedDatapointsTotal)

	dp := makeNumberDP(map[string]string{}, 1000, 42)
	rms := makeProcGaugeRM("mdrop1_metric", dp)
	s.Sample(rms)

	evalAfter := getCounterValue(processingRuleEvaluationsTotal, "m-drop-1", "drop")
	droppedAfter := getCounterValue(processingRuleDroppedTotal, "m-drop-1", "drop")
	inputAfter := getCounterValue(processingRuleInputTotal, "m-drop-1", "drop")
	overallDropAfter := getCounterScalarValue(processingDroppedDatapointsTotal)

	if delta := evalAfter - evalBefore; delta != 1 {
		t.Errorf("evaluation counter delta = %v, want 1", delta)
	}
	if delta := droppedAfter - droppedBefore; delta != 1 {
		t.Errorf("dropped counter delta = %v, want 1", delta)
	}
	if delta := inputAfter - inputBefore; delta != 1 {
		t.Errorf("input counter delta = %v, want 1", delta)
	}
	if delta := overallDropAfter - overallDropBefore; delta != 1 {
		t.Errorf("overall dropped counter delta = %v, want 1", delta)
	}
}

// TestMetrics_SampleRuleCounters verifies that a sample action increments
// evaluation, input, output, and dropped counters correctly for kept and dropped cases.
func TestMetrics_SampleRuleCounters(t *testing.T) {
	// Rate=1.0 keeps everything.
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "m-sample-keep-1", Input: "mskeep1_.*", Action: ActionSample, Rate: 1.0, Method: "head"},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	evalBefore := getCounterValue(processingRuleEvaluationsTotal, "m-sample-keep-1", "sample")
	inputBefore := getCounterValue(processingRuleInputTotal, "m-sample-keep-1", "sample")
	outputBefore := getCounterValue(processingRuleOutputTotal, "m-sample-keep-1", "sample", "")

	dp := makeNumberDP(map[string]string{}, 1000, 100)
	rms := makeProcGaugeRM("mskeep1_latency", dp)
	s.Sample(rms)

	evalAfter := getCounterValue(processingRuleEvaluationsTotal, "m-sample-keep-1", "sample")
	inputAfter := getCounterValue(processingRuleInputTotal, "m-sample-keep-1", "sample")
	outputAfter := getCounterValue(processingRuleOutputTotal, "m-sample-keep-1", "sample", "")

	if delta := evalAfter - evalBefore; delta != 1 {
		t.Errorf("evaluation counter delta = %v, want 1", delta)
	}
	if delta := inputAfter - inputBefore; delta != 1 {
		t.Errorf("input counter delta = %v, want 1", delta)
	}
	if delta := outputAfter - outputBefore; delta != 1 {
		t.Errorf("output counter delta = %v, want 1 (rate=1.0 should keep)", delta)
	}

	// Rate=0.0 drops everything.
	cfg2 := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "m-sample-drop-1", Input: "msdrop1_.*", Action: ActionSample, Rate: 0.0, Method: "head"},
		},
	}
	s2, err := NewFromProcessing(cfg2)
	if err != nil {
		t.Fatal(err)
	}

	droppedBefore := getCounterValue(processingRuleDroppedTotal, "m-sample-drop-1", "sample")

	dp2 := makeNumberDP(map[string]string{}, 1000, 100)
	rms2 := makeProcGaugeRM("msdrop1_latency", dp2)
	s2.Sample(rms2)

	droppedAfter := getCounterValue(processingRuleDroppedTotal, "m-sample-drop-1", "sample")

	if delta := droppedAfter - droppedBefore; delta != 1 {
		t.Errorf("dropped counter delta = %v, want 1 (rate=0.0 should drop)", delta)
	}
}

// TestMetrics_TransformRuleCounters verifies that a transform action increments
// evaluations, input, and output counters, and is non-terminal (continues to next rule).
func TestMetrics_TransformRuleCounters(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "m-xform-1",
				Input:  "mxform1_.*",
				Action: ActionTransform,
				Operations: []Operation{
					{Set: &SetOp{Label: "test_label", Value: "test_value"}},
				},
			},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	evalBefore := getCounterValue(processingRuleEvaluationsTotal, "m-xform-1", "transform")
	inputBefore := getCounterValue(processingRuleInputTotal, "m-xform-1", "transform")
	outputBefore := getCounterValue(processingRuleOutputTotal, "m-xform-1", "transform", "")

	dp := makeNumberDP(map[string]string{}, 1000, 42)
	rms := makeProcGaugeRM("mxform1_metric", dp)
	result := s.Sample(rms)

	evalAfter := getCounterValue(processingRuleEvaluationsTotal, "m-xform-1", "transform")
	inputAfter := getCounterValue(processingRuleInputTotal, "m-xform-1", "transform")
	outputAfter := getCounterValue(processingRuleOutputTotal, "m-xform-1", "transform", "")

	if delta := evalAfter - evalBefore; delta != 1 {
		t.Errorf("evaluation counter delta = %v, want 1", delta)
	}
	if delta := inputAfter - inputBefore; delta != 1 {
		t.Errorf("input counter delta = %v, want 1", delta)
	}
	if delta := outputAfter - outputBefore; delta != 1 {
		t.Errorf("output counter delta = %v, want 1 (transform emits)", delta)
	}

	// Transform is non-terminal: data should pass through.
	if len(result) == 0 {
		t.Error("transform is non-terminal; data should pass through")
	}
}

// TestMetrics_MultiTouchCounting verifies that when a datapoint matches a transform
// rule followed by a drop rule, the evaluation counter shows 2 increments total.
func TestMetrics_MultiTouchCounting(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "m-multi-xform-1",
				Input:  "mmulti1_.*",
				Action: ActionTransform,
				Operations: []Operation{
					{Set: &SetOp{Label: "stage", Value: "transformed"}},
				},
			},
			{
				Name:   "m-multi-drop-1",
				Input:  "mmulti1_.*",
				Action: ActionDrop,
			},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	xformEvalBefore := getCounterValue(processingRuleEvaluationsTotal, "m-multi-xform-1", "transform")
	dropEvalBefore := getCounterValue(processingRuleEvaluationsTotal, "m-multi-drop-1", "drop")

	dp := makeNumberDP(map[string]string{}, 1000, 42)
	rms := makeProcGaugeRM("mmulti1_metric", dp)
	result := s.Sample(rms)

	xformEvalAfter := getCounterValue(processingRuleEvaluationsTotal, "m-multi-xform-1", "transform")
	dropEvalAfter := getCounterValue(processingRuleEvaluationsTotal, "m-multi-drop-1", "drop")

	if delta := xformEvalAfter - xformEvalBefore; delta != 1 {
		t.Errorf("transform evaluation delta = %v, want 1", delta)
	}
	if delta := dropEvalAfter - dropEvalBefore; delta != 1 {
		t.Errorf("drop evaluation delta = %v, want 1", delta)
	}

	// The final result should be dropped.
	if len(result) != 0 {
		t.Error("expected datapoint to be dropped after transform+drop")
	}
}

// TestMetrics_RulesActiveGauge verifies that the processingRulesActive gauge
// reflects the number of rules by action type.
func TestMetrics_RulesActiveGauge(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "m-gauge-drop-1", Input: "mgd1_.*", Action: ActionDrop},
			{Name: "m-gauge-drop-2", Input: "mgd2_.*", Action: ActionDrop},
			{Name: "m-gauge-sample-1", Input: "mgs1_.*", Action: ActionSample, Rate: 1.0, Method: "head"},
			{
				Name:   "m-gauge-xform-1",
				Input:  "mgx1_.*",
				Action: ActionTransform,
				Operations: []Operation{
					{Set: &SetOp{Label: "x", Value: "y"}},
				},
			},
		},
	}
	_, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// NewFromProcessing calls updateProcessingRulesActive.
	dropGauge := getGaugeValue(processingRulesActive, "drop")
	sampleGauge := getGaugeValue(processingRulesActive, "sample")
	transformGauge := getGaugeValue(processingRulesActive, "transform")

	if dropGauge != 2 {
		t.Errorf("drop gauge = %v, want 2", dropGauge)
	}
	if sampleGauge != 1 {
		t.Errorf("sample gauge = %v, want 1", sampleGauge)
	}
	if transformGauge != 1 {
		t.Errorf("transform gauge = %v, want 1", transformGauge)
	}
}

// TestMetrics_ConfigReloadCounter verifies that successful and failed config reloads
// increment the appropriate counter labels.
func TestMetrics_ConfigReloadCounter(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "m-reload-init-1", Input: "mri1_.*", Action: ActionDrop},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	successBefore := getCounterValue(processingConfigReloadsTotal, "success")
	errorBefore := getCounterValue(processingConfigReloadsTotal, "error")

	// Successful reload.
	newCfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "m-reload-new-1", Input: "mrn1_.*", Action: ActionSample, Rate: 1.0, Method: "head"},
		},
	}
	if err := s.ReloadProcessingConfig(newCfg); err != nil {
		t.Fatal(err)
	}

	successAfter := getCounterValue(processingConfigReloadsTotal, "success")
	if delta := successAfter - successBefore; delta != 1 {
		t.Errorf("success reload counter delta = %v, want 1", delta)
	}

	// Failed reload (invalid config — empty rule name).
	badCfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "", Input: ".*", Action: ActionDrop},
		},
	}
	err = s.ReloadProcessingConfig(badCfg)
	if err == nil {
		t.Fatal("expected reload to fail with empty rule name")
	}

	errorAfter := getCounterValue(processingConfigReloadsTotal, "error")
	if delta := errorAfter - errorBefore; delta != 1 {
		t.Errorf("error reload counter delta = %v, want 1", delta)
	}
}

// TestMetrics_ProcessingDuration verifies that the processingDuration histogram
// gets observed when processing data through the engine.
func TestMetrics_ProcessingDuration(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "m-dur-1", Input: "mdur1_.*", Action: ActionDrop},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	countBefore := getHistogramCount(processingDuration)

	dp := makeNumberDP(map[string]string{}, 1000, 42)
	rms := makeProcGaugeRM("mdur1_metric", dp)
	s.Sample(rms)

	countAfter := getHistogramCount(processingDuration)
	if countAfter <= countBefore {
		t.Errorf("processingDuration sample count did not increase: before=%d after=%d", countBefore, countAfter)
	}
}

// TestMetrics_OverallInputOutput verifies that the overall input/output/dropped
// counters are accurate across a processing pass.
func TestMetrics_OverallInputOutput(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "m-io-keep-1", Input: "miok1_.*", Action: ActionSample, Rate: 1.0, Method: "head"},
			{Name: "m-io-drop-1", Input: "miod1_.*", Action: ActionDrop},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inputBefore := getCounterScalarValue(processingInputDatapointsTotal)
	outputBefore := getCounterScalarValue(processingOutputDatapointsTotal)
	droppedBefore := getCounterScalarValue(processingDroppedDatapointsTotal)

	// Process a kept metric.
	dp1 := makeNumberDP(map[string]string{}, 1000, 42)
	rms1 := makeProcGaugeRM("miok1_metric", dp1)
	s.Sample(rms1)

	// Process a dropped metric.
	dp2 := makeNumberDP(map[string]string{}, 1000, 99)
	rms2 := makeProcGaugeRM("miod1_metric", dp2)
	s.Sample(rms2)

	inputAfter := getCounterScalarValue(processingInputDatapointsTotal)
	outputAfter := getCounterScalarValue(processingOutputDatapointsTotal)
	droppedAfter := getCounterScalarValue(processingDroppedDatapointsTotal)

	inputDelta := inputAfter - inputBefore
	outputDelta := outputAfter - outputBefore
	droppedDelta := droppedAfter - droppedBefore

	if inputDelta != 2 {
		t.Errorf("overall input delta = %v, want 2", inputDelta)
	}
	if outputDelta != 1 {
		t.Errorf("overall output delta = %v, want 1", outputDelta)
	}
	if droppedDelta != 1 {
		t.Errorf("overall dropped delta = %v, want 1", droppedDelta)
	}
}

// TestMetrics_TransformLabelCounters verifies that the transform-specific label
// counters (added, removed) are incremented correctly.
func TestMetrics_TransformLabelCounters(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "m-xlabel-1",
				Input:  "mxlbl1_.*",
				Action: ActionTransform,
				Operations: []Operation{
					{Set: &SetOp{Label: "new_label", Value: "new_value"}},
					{Remove: []string{"old_label"}},
				},
			},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	addedBefore := getCounterValue(processingTransformLabelsAddedTotal, "m-xlabel-1")
	removedBefore := getCounterValue(processingTransformLabelsRemovedTotal, "m-xlabel-1")

	dp := makeNumberDP(map[string]string{"old_label": "old_value"}, 1000, 42)
	rms := makeProcGaugeRM("mxlbl1_metric", dp)
	s.Sample(rms)

	addedAfter := getCounterValue(processingTransformLabelsAddedTotal, "m-xlabel-1")
	removedAfter := getCounterValue(processingTransformLabelsRemovedTotal, "m-xlabel-1")

	if delta := addedAfter - addedBefore; delta < 1 {
		t.Errorf("labels added delta = %v, want >= 1", delta)
	}
	if delta := removedAfter - removedBefore; delta < 1 {
		t.Errorf("labels removed delta = %v, want >= 1", delta)
	}
}

// TestMetrics_AggregateGroupsActive verifies that the aggregate groups gauge
// increases when data is ingested into an aggregate rule.
func TestMetrics_AggregateGroupsActive(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:      "m-agggrp-1",
				Input:     "magggrp1_.*",
				Action:    ActionAggregate,
				Output:    "magggrp1_agg",
				Interval:  "1m",
				GroupBy:   []string{"host"},
				Functions: []string{"sum"},
			},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	groupsBefore := getGaugeValue(processingAggregateGroupsActive, "m-agggrp-1")

	// Ingest two datapoints with different group-by values.
	dp1 := makeNumberDP(map[string]string{"host": "a"}, 1000, 10)
	rms1 := makeProcGaugeRM("magggrp1_cpu", dp1)
	s.Sample(rms1)

	dp2 := makeNumberDP(map[string]string{"host": "b"}, 1000, 20)
	rms2 := makeProcGaugeRM("magggrp1_cpu", dp2)
	s.Sample(rms2)

	groupsAfter := getGaugeValue(processingAggregateGroupsActive, "m-agggrp-1")

	if groupsAfter <= groupsBefore {
		t.Errorf("aggregate groups gauge did not increase: before=%v after=%v", groupsBefore, groupsAfter)
	}
}

// TestMetrics_PassthroughCounting verifies that when no terminal rule matches,
// the overall output counter is incremented for pass-through behavior.
// The output counter is incremented both in applyProcessingRules (pass-through path)
// and in processNumberDataPoints (kept path), so the delta is 2 for a single
// pass-through datapoint.
func TestMetrics_PassthroughCounting(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "m-pt-drop-1", Input: "mptdrop1_.*", Action: ActionDrop},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	inputBefore := getCounterScalarValue(processingInputDatapointsTotal)
	outputBefore := getCounterScalarValue(processingOutputDatapointsTotal)

	// This metric does NOT match the drop rule, so it passes through.
	dp := makeNumberDP(map[string]string{}, 1000, 42)
	rms := makeProcGaugeRM("unmatched_metric", dp)
	result := s.Sample(rms)

	inputAfter := getCounterScalarValue(processingInputDatapointsTotal)
	outputAfter := getCounterScalarValue(processingOutputDatapointsTotal)

	if len(result) == 0 {
		t.Error("expected unmatched metric to pass through")
	}
	if delta := inputAfter - inputBefore; delta != 1 {
		t.Errorf("overall input delta = %v, want 1", delta)
	}
	// Output is incremented in both applyProcessingRules (pass-through) and
	// processNumberDataPoints (kept), totaling 2 for one pass-through datapoint.
	if delta := outputAfter - outputBefore; delta != 2 {
		t.Errorf("overall output delta = %v, want 2 (pass-through: 1 in applyRules + 1 in processNumberDPs)", delta)
	}
}
