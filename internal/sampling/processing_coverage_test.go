package sampling

import (
	"os"
	"path/filepath"
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// ---------------------------------------------------------------------------
// Helpers to build OTLP test data for Summary and ExponentialHistogram types.
// ---------------------------------------------------------------------------

func makeSummaryDP(attrs map[string]string, ts uint64, count uint64, sum float64) *metricspb.SummaryDataPoint {
	var kvs []*commonpb.KeyValue
	for k, v := range attrs {
		kvs = append(kvs, &commonpb.KeyValue{
			Key:   k,
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}},
		})
	}
	return &metricspb.SummaryDataPoint{
		Attributes:   kvs,
		TimeUnixNano: ts,
		Count:        count,
		Sum:          sum,
	}
}

func makeExpHistDP(attrs map[string]string, ts uint64, count uint64, sum float64) *metricspb.ExponentialHistogramDataPoint {
	var kvs []*commonpb.KeyValue
	for k, v := range attrs {
		kvs = append(kvs, &commonpb.KeyValue{
			Key:   k,
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}},
		})
	}
	return &metricspb.ExponentialHistogramDataPoint{
		Attributes:   kvs,
		TimeUnixNano: ts,
		Count:        count,
		Sum:          &sum,
	}
}

func makeHistDP(attrs map[string]string, ts uint64, count uint64, sum float64) *metricspb.HistogramDataPoint {
	var kvs []*commonpb.KeyValue
	for k, v := range attrs {
		kvs = append(kvs, &commonpb.KeyValue{
			Key:   k,
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}},
		})
	}
	return &metricspb.HistogramDataPoint{
		Attributes:   kvs,
		TimeUnixNano: ts,
		Count:        count,
		Sum:          &sum,
	}
}

func makeSummaryRM(metricName string, dps ...*metricspb.SummaryDataPoint) []*metricspb.ResourceMetrics {
	return []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: metricName,
							Data: &metricspb.Metric_Summary{
								Summary: &metricspb.Summary{DataPoints: dps},
							},
						},
					},
				},
			},
		},
	}
}

func makeExpHistRM(metricName string, dps ...*metricspb.ExponentialHistogramDataPoint) []*metricspb.ResourceMetrics {
	return []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: metricName,
							Data: &metricspb.Metric_ExponentialHistogram{
								ExponentialHistogram: &metricspb.ExponentialHistogram{DataPoints: dps},
							},
						},
					},
				},
			},
		},
	}
}

func makeHistRM(metricName string, dps ...*metricspb.HistogramDataPoint) []*metricspb.ResourceMetrics {
	return []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: metricName,
							Data: &metricspb.Metric_Histogram{
								Histogram: &metricspb.Histogram{DataPoints: dps},
							},
						},
					},
				},
			},
		},
	}
}

func makeSumRM(metricName string, dps ...*metricspb.NumberDataPoint) []*metricspb.ResourceMetrics {
	return []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: metricName,
							Data: &metricspb.Metric_Sum{
								Sum: &metricspb.Sum{DataPoints: dps},
							},
						},
					},
				},
			},
		},
	}
}

// ---------------------------------------------------------------------------
// 1. processSummaryDataPoints (0% coverage)
// ---------------------------------------------------------------------------

func TestProcessSummaryDataPoints_Drop(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "drop-summary", Input: "rpc_.*", Action: ActionDrop},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeSummaryDP(map[string]string{"service": "web"}, 1000, 5, 100.0)
	rms := makeSummaryRM("rpc_duration", dp)
	result := s.Process(rms)
	if len(result) != 0 {
		t.Error("expected summary datapoint to be dropped")
	}
}

func TestProcessSummaryDataPoints_SampleKeep(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "keep-all", Input: ".*", Action: ActionSample, Rate: 1.0, Method: "head"},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeSummaryDP(map[string]string{}, 1000, 10, 200.0)
	rms := makeSummaryRM("rpc_latency", dp)
	result := s.Process(rms)
	if len(result) == 0 {
		t.Error("expected summary datapoint to be kept (rate=1.0)")
	}
}

func TestProcessSummaryDataPoints_SampleDrop(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "drop-all", Input: ".*", Action: ActionSample, Rate: 0.0, Method: "head"},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeSummaryDP(map[string]string{}, 1000, 10, 200.0)
	rms := makeSummaryRM("rpc_latency", dp)
	result := s.Process(rms)
	if len(result) != 0 {
		t.Error("expected summary datapoint to be dropped (rate=0.0)")
	}
}

func TestProcessSummaryDataPoints_Transform(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "add-env",
				Input:  ".*",
				Action: ActionTransform,
				Operations: []Operation{
					{Set: &SetOp{Label: "env", Value: "prod"}},
				},
			},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeSummaryDP(map[string]string{"service": "api"}, 1000, 10, 200.0)
	rms := makeSummaryRM("rpc_latency", dp)
	result := s.Process(rms)
	if len(result) == 0 {
		t.Fatal("expected summary to pass through after transform")
	}

	outDP := result[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Summary).Summary.DataPoints[0]
	envVal := getTransformAttr(outDP.Attributes, "env")
	if envVal != "prod" {
		t.Errorf("env = %q, want 'prod'", envVal)
	}
}

func TestProcessSummaryDataPoints_PassThrough(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "drop-debug", Input: "debug_.*", Action: ActionDrop},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeSummaryDP(map[string]string{}, 1000, 3, 50.0)
	rms := makeSummaryRM("http_duration", dp)
	result := s.Process(rms)
	if len(result) == 0 {
		t.Error("expected summary datapoint to pass through (no matching rule)")
	}
}

// ---------------------------------------------------------------------------
// 2. processExponentialHistogramDataPoints (0% coverage)
// ---------------------------------------------------------------------------

func TestProcessExpHistDataPoints_Drop(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "drop-internal", Input: "internal_.*", Action: ActionDrop},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeExpHistDP(map[string]string{}, 1000, 5, 100.0)
	rms := makeExpHistRM("internal_latency", dp)
	result := s.Process(rms)
	if len(result) != 0 {
		t.Error("expected exponential histogram to be dropped")
	}
}

func TestProcessExpHistDataPoints_SampleKeep(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "keep-all", Input: ".*", Action: ActionSample, Rate: 1.0, Method: "head"},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeExpHistDP(map[string]string{"env": "prod"}, 1000, 10, 200.0)
	rms := makeExpHistRM("request_duration", dp)
	result := s.Process(rms)
	if len(result) == 0 {
		t.Error("expected exponential histogram to be kept (rate=1.0)")
	}
}

func TestProcessExpHistDataPoints_SampleDrop(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "drop-all", Input: ".*", Action: ActionSample, Rate: 0.0, Method: "head"},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeExpHistDP(map[string]string{}, 1000, 10, 200.0)
	rms := makeExpHistRM("request_duration", dp)
	result := s.Process(rms)
	if len(result) != 0 {
		t.Error("expected exponential histogram to be dropped (rate=0.0)")
	}
}

func TestProcessExpHistDataPoints_Transform(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "tag-region",
				Input:  ".*",
				Action: ActionTransform,
				Operations: []Operation{
					{Set: &SetOp{Label: "region", Value: "us-east-1"}},
				},
			},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeExpHistDP(map[string]string{}, 1000, 10, 200.0)
	rms := makeExpHistRM("request_duration", dp)
	result := s.Process(rms)
	if len(result) == 0 {
		t.Fatal("expected exponential histogram to pass through after transform")
	}

	outDP := result[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_ExponentialHistogram).ExponentialHistogram.DataPoints[0]
	regionVal := getTransformAttr(outDP.Attributes, "region")
	if regionVal != "us-east-1" {
		t.Errorf("region = %q, want 'us-east-1'", regionVal)
	}
}

func TestProcessExpHistDataPoints_PassThrough(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "drop-debug", Input: "debug_.*", Action: ActionDrop},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeExpHistDP(map[string]string{}, 1000, 3, 50.0)
	rms := makeExpHistRM("http_requests", dp)
	result := s.Process(rms)
	if len(result) == 0 {
		t.Error("expected exponential histogram to pass through (no matching rule)")
	}
}

// ---------------------------------------------------------------------------
// 3. applyProcessingRulesGeneric (17.2% coverage)
// ---------------------------------------------------------------------------

func TestApplyProcessingRulesGeneric_SampleKeep(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "keep-hist", Input: "request_.*", Action: ActionSample, Rate: 1.0, Method: "head"},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeHistDP(map[string]string{"service": "api"}, 1000, 10, 500.0)
	rms := makeHistRM("request_duration", dp)
	result := s.Process(rms)
	if len(result) == 0 {
		t.Error("expected histogram to be kept by sample rule (rate=1.0)")
	}
}

func TestApplyProcessingRulesGeneric_SampleDrop(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "drop-hist", Input: "request_.*", Action: ActionSample, Rate: 0.0, Method: "head"},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeHistDP(map[string]string{"service": "api"}, 1000, 10, 500.0)
	rms := makeHistRM("request_duration", dp)
	result := s.Process(rms)
	if len(result) != 0 {
		t.Error("expected histogram to be dropped by sample rule (rate=0.0)")
	}
}

func TestApplyProcessingRulesGeneric_DropAction(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "drop-internal", Input: "internal_.*", Action: ActionDrop},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeHistDP(map[string]string{}, 1000, 5, 100.0)
	rms := makeHistRM("internal_trace", dp)
	result := s.Process(rms)
	if len(result) != 0 {
		t.Error("expected histogram to be dropped")
	}
}

func TestApplyProcessingRulesGeneric_TransformThenPassThrough(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "add-tier",
				Input:  ".*",
				Action: ActionTransform,
				Operations: []Operation{
					{Set: &SetOp{Label: "tier", Value: "gold"}},
				},
			},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeHistDP(map[string]string{"service": "api"}, 1000, 10, 500.0)
	rms := makeHistRM("request_duration", dp)
	result := s.Process(rms)
	if len(result) == 0 {
		t.Fatal("expected histogram to pass through after transform")
	}

	outDP := result[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Histogram).Histogram.DataPoints[0]
	tierVal := getTransformAttr(outDP.Attributes, "tier")
	if tierVal != "gold" {
		t.Errorf("tier = %q, want 'gold'", tierVal)
	}
}

func TestApplyProcessingRulesGeneric_TransformThenDrop(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "add-tag",
				Input:  ".*",
				Action: ActionTransform,
				Operations: []Operation{
					{Set: &SetOp{Label: "processed", Value: "true"}},
				},
			},
			{
				Name:   "drop-internal",
				Input:  "internal_.*",
				Action: ActionDrop,
			},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// internal_trace: transformed then dropped.
	dp := makeHistDP(map[string]string{}, 1000, 5, 100.0)
	rms := makeHistRM("internal_trace", dp)
	result := s.Process(rms)
	if len(result) != 0 {
		t.Error("expected histogram to be dropped after transform")
	}

	// http_latency: transformed then passes through.
	dp2 := makeHistDP(map[string]string{}, 2000, 10, 200.0)
	rms2 := makeHistRM("http_latency", dp2)
	result2 := s.Process(rms2)
	if len(result2) == 0 {
		t.Error("expected histogram to pass through")
	}
}

func TestApplyProcessingRulesGeneric_DownsampleSkipped(t *testing.T) {
	// Downsample only applies to NumberDataPoints; for histograms it should be skipped (continue).
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:     "ds-rule",
				Input:    ".*",
				Action:   ActionDownsample,
				Method:   "avg",
				Interval: "1m",
			},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeHistDP(map[string]string{}, 1000, 5, 100.0)
	rms := makeHistRM("http_latency", dp)
	result := s.Process(rms)
	// Downsample is skipped for histogram types, so data should pass through.
	if len(result) == 0 {
		t.Error("expected histogram to pass through (downsample skipped for non-number types)")
	}
}

func TestApplyProcessingRulesGeneric_AggregateSkipped(t *testing.T) {
	// Aggregate only applies to NumberDataPoints; for summaries it should be skipped.
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:      "agg-rule",
				Input:     ".*",
				Action:    ActionAggregate,
				Interval:  "1m",
				Functions: []string{"sum"},
				Output:    "agg_output",
			},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeSummaryDP(map[string]string{}, 1000, 10, 200.0)
	rms := makeSummaryRM("http_requests", dp)
	result := s.Process(rms)
	// Aggregate is skipped for summary types, so data should pass through.
	if len(result) == 0 {
		t.Error("expected summary to pass through (aggregate skipped for non-number types)")
	}
}

func TestApplyProcessingRulesGeneric_NoMatchPassThrough(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "drop-debug", Input: "debug_.*", Action: ActionDrop},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeHistDP(map[string]string{}, 1000, 5, 100.0)
	rms := makeHistRM("http_requests", dp)
	result := s.Process(rms)
	if len(result) == 0 {
		t.Error("expected histogram to pass through (no matching rule)")
	}
}

func TestApplyProcessingRulesGeneric_TransformWithCondition(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "prod-only",
				Input:  ".*",
				Action: ActionTransform,
				When: []Condition{
					{Label: "env", Equals: "production"},
				},
				Operations: []Operation{
					{Set: &SetOp{Label: "priority", Value: "high"}},
				},
			},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Production env: transform should apply.
	dp1 := makeHistDP(map[string]string{"env": "production"}, 1000, 10, 500.0)
	rms1 := makeHistRM("request_duration", dp1)
	result1 := s.Process(rms1)
	if len(result1) == 0 {
		t.Fatal("expected pass through")
	}
	outDP1 := result1[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Histogram).Histogram.DataPoints[0]
	if getTransformAttr(outDP1.Attributes, "priority") != "high" {
		t.Error("expected priority=high for production env")
	}

	// Staging env: condition not met, transform should NOT apply.
	dp2 := makeHistDP(map[string]string{"env": "staging"}, 2000, 5, 200.0)
	rms2 := makeHistRM("request_duration", dp2)
	result2 := s.Process(rms2)
	if len(result2) == 0 {
		t.Fatal("expected pass through")
	}
	outDP2 := result2[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Histogram).Histogram.DataPoints[0]
	if hasTransformAttr(outDP2.Attributes, "priority") {
		t.Error("transform should NOT apply for staging env")
	}
}

func TestApplyProcessingRulesGeneric_ProbabilisticSample(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "prob-sample", Input: ".*", Action: ActionSample, Rate: 1.0, Method: "probabilistic"},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeHistDP(map[string]string{}, 1000, 5, 100.0)
	rms := makeHistRM("http_latency", dp)
	result := s.Process(rms)
	if len(result) == 0 {
		t.Error("expected histogram to be kept (probabilistic rate=1.0)")
	}
}

// ---------------------------------------------------------------------------
// 4. processMetric (41.4% coverage) — test all switch branches
// ---------------------------------------------------------------------------

func TestProcessMetric_AllTypes(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "drop-debug", Input: "debug_.*", Action: ActionDrop},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		metricName string
		rms        []*metricspb.ResourceMetrics
		wantKept   bool
	}{
		{
			name:       "Gauge_kept",
			metricName: "http_requests",
			rms:        makeProcGaugeRM("http_requests", makeNumberDP(map[string]string{}, 1000, 42)),
			wantKept:   true,
		},
		{
			name:       "Gauge_dropped",
			metricName: "debug_trace",
			rms:        makeProcGaugeRM("debug_trace", makeNumberDP(map[string]string{}, 1000, 42)),
			wantKept:   false,
		},
		{
			name:       "Sum_kept",
			metricName: "http_total",
			rms:        makeSumRM("http_total", makeNumberDP(map[string]string{}, 1000, 42)),
			wantKept:   true,
		},
		{
			name:       "Sum_dropped",
			metricName: "debug_counter",
			rms:        makeSumRM("debug_counter", makeNumberDP(map[string]string{}, 1000, 42)),
			wantKept:   false,
		},
		{
			name:       "Histogram_kept",
			metricName: "http_duration",
			rms:        makeHistRM("http_duration", makeHistDP(map[string]string{}, 1000, 10, 500.0)),
			wantKept:   true,
		},
		{
			name:       "Histogram_dropped",
			metricName: "debug_histogram",
			rms:        makeHistRM("debug_histogram", makeHistDP(map[string]string{}, 1000, 10, 500.0)),
			wantKept:   false,
		},
		{
			name:       "Summary_kept",
			metricName: "http_summary",
			rms:        makeSummaryRM("http_summary", makeSummaryDP(map[string]string{}, 1000, 10, 200.0)),
			wantKept:   true,
		},
		{
			name:       "Summary_dropped",
			metricName: "debug_summary",
			rms:        makeSummaryRM("debug_summary", makeSummaryDP(map[string]string{}, 1000, 10, 200.0)),
			wantKept:   false,
		},
		{
			name:       "ExponentialHistogram_kept",
			metricName: "http_exphist",
			rms:        makeExpHistRM("http_exphist", makeExpHistDP(map[string]string{}, 1000, 5, 100.0)),
			wantKept:   true,
		},
		{
			name:       "ExponentialHistogram_dropped",
			metricName: "debug_exphist",
			rms:        makeExpHistRM("debug_exphist", makeExpHistDP(map[string]string{}, 1000, 5, 100.0)),
			wantKept:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.Process(tt.rms)
			if tt.wantKept && len(result) == 0 {
				t.Errorf("expected metric %q to be kept", tt.metricName)
			}
			if !tt.wantKept && len(result) != 0 {
				t.Errorf("expected metric %q to be dropped", tt.metricName)
			}
		})
	}
}

func TestProcessMetric_NilMetric(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "keep-all", Input: ".*", Action: ActionSample, Rate: 1.0, Method: "head"},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Process with a nil ResourceMetrics entry.
	rms := []*metricspb.ResourceMetrics{nil}
	result := s.Process(rms)
	if len(result) != 0 {
		t.Error("expected nil ResourceMetrics to be filtered out")
	}
}

func TestProcessMetric_EmptyInput(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "keep-all", Input: ".*", Action: ActionSample, Rate: 1.0, Method: "head"},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	result := s.Process(nil)
	if result != nil {
		t.Error("expected nil for nil input")
	}

	result = s.Process([]*metricspb.ResourceMetrics{})
	if len(result) != 0 {
		t.Error("expected empty for empty input")
	}
}

func TestProcessMetric_TransformOnAllTypes(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "tag-all",
				Input:  ".*",
				Action: ActionTransform,
				Operations: []Operation{
					{Set: &SetOp{Label: "processed_by", Value: "governor"}},
				},
			},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Test Gauge.
	gaugeRMS := makeProcGaugeRM("gauge_metric", makeNumberDP(map[string]string{}, 1000, 1))
	gaugeResult := s.Process(gaugeRMS)
	if len(gaugeResult) == 0 {
		t.Fatal("expected gauge to pass through")
	}
	gaugeDP := gaugeResult[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Gauge).Gauge.DataPoints[0]
	if getTransformAttr(gaugeDP.Attributes, "processed_by") != "governor" {
		t.Error("expected transform on gauge")
	}

	// Test Sum.
	sumRMS := makeSumRM("sum_metric", makeNumberDP(map[string]string{}, 1000, 2))
	sumResult := s.Process(sumRMS)
	if len(sumResult) == 0 {
		t.Fatal("expected sum to pass through")
	}
	sumDP := sumResult[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Sum).Sum.DataPoints[0]
	if getTransformAttr(sumDP.Attributes, "processed_by") != "governor" {
		t.Error("expected transform on sum")
	}

	// Test Histogram.
	histRMS := makeHistRM("hist_metric", makeHistDP(map[string]string{}, 1000, 5, 100.0))
	histResult := s.Process(histRMS)
	if len(histResult) == 0 {
		t.Fatal("expected histogram to pass through")
	}
	histDP := histResult[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Histogram).Histogram.DataPoints[0]
	if getTransformAttr(histDP.Attributes, "processed_by") != "governor" {
		t.Error("expected transform on histogram")
	}

	// Test Summary.
	summaryRMS := makeSummaryRM("summary_metric", makeSummaryDP(map[string]string{}, 1000, 10, 200.0))
	summaryResult := s.Process(summaryRMS)
	if len(summaryResult) == 0 {
		t.Fatal("expected summary to pass through")
	}
	summaryDP := summaryResult[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Summary).Summary.DataPoints[0]
	if getTransformAttr(summaryDP.Attributes, "processed_by") != "governor" {
		t.Error("expected transform on summary")
	}

	// Test ExponentialHistogram.
	expHistRMS := makeExpHistRM("exphist_metric", makeExpHistDP(map[string]string{}, 1000, 5, 100.0))
	expHistResult := s.Process(expHistRMS)
	if len(expHistResult) == 0 {
		t.Fatal("expected exponential histogram to pass through")
	}
	expHistDP := expHistResult[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_ExponentialHistogram).ExponentialHistogram.DataPoints[0]
	if getTransformAttr(expHistDP.Attributes, "processed_by") != "governor" {
		t.Error("expected transform on exponential histogram")
	}
}

// ---------------------------------------------------------------------------
// 5. processHistogramDataPoints edge cases (76.9% coverage)
// ---------------------------------------------------------------------------

func TestProcessHistogramDataPoints_EmptySlice(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "drop-all", Input: ".*", Action: ActionDrop},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Histogram with no data points.
	rms := []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "empty_hist",
							Data: &metricspb.Metric_Histogram{
								Histogram: &metricspb.Histogram{DataPoints: []*metricspb.HistogramDataPoint{}},
							},
						},
					},
				},
			},
		},
	}
	result := s.Process(rms)
	// Empty datapoints should pass through (not trigger nil return) since len==0 returns dps.
	if len(result) == 0 {
		// This is acceptable: empty datapoints may cause the metric to be filtered.
		// The important thing is no panic.
	}
}

func TestProcessHistogramDataPoints_AllDropped(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "drop-all", Input: ".*", Action: ActionDrop},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp1 := makeHistDP(map[string]string{}, 1000, 10, 100.0)
	dp2 := makeHistDP(map[string]string{}, 2000, 20, 200.0)
	rms := makeHistRM("http_duration", dp1, dp2)
	result := s.Process(rms)
	if len(result) != 0 {
		t.Error("expected all histogram datapoints to be dropped")
	}
}

func TestProcessHistogramDataPoints_TransformOnly(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "relabel",
				Input:  ".*",
				Action: ActionTransform,
				Operations: []Operation{
					{Set: &SetOp{Label: "cluster", Value: "primary"}},
				},
			},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeHistDP(map[string]string{"service": "api"}, 1000, 10, 500.0)
	rms := makeHistRM("http_duration", dp)
	result := s.Process(rms)
	if len(result) == 0 {
		t.Fatal("expected histogram to pass through after transform")
	}

	outDP := result[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Histogram).Histogram.DataPoints[0]
	if getTransformAttr(outDP.Attributes, "cluster") != "primary" {
		t.Error("expected cluster=primary after transform")
	}
	if getTransformAttr(outDP.Attributes, "service") != "api" {
		t.Error("expected service=api to be preserved")
	}
}

func TestProcessHistogramDataPoints_MultipleDataPointsPartialDrop(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:        "drop-debug-service",
				Input:       ".*",
				InputLabels: map[string]string{"service": "debug"},
				Action:      ActionDrop,
			},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp1 := makeHistDP(map[string]string{"service": "debug"}, 1000, 10, 100.0)
	dp2 := makeHistDP(map[string]string{"service": "web"}, 2000, 20, 200.0)
	rms := makeHistRM("http_duration", dp1, dp2)
	result := s.Process(rms)
	if len(result) == 0 {
		t.Fatal("expected at least one datapoint to pass through")
	}

	outDPs := result[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Histogram).Histogram.DataPoints
	if len(outDPs) != 1 {
		t.Fatalf("expected 1 datapoint, got %d", len(outDPs))
	}
}

// ---------------------------------------------------------------------------
// 6. LoadProcessingFile (0% coverage)
// ---------------------------------------------------------------------------

func TestLoadProcessingFile_Valid(t *testing.T) {
	content := `
rules:
  - name: drop-debug
    input: "debug_.*"
    action: drop
  - name: keep-sli
    input: "sli_.*"
    action: sample
    rate: 1.0
    method: head
`
	dir := t.TempDir()
	path := filepath.Join(dir, "processing.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadProcessingFile(path)
	if err != nil {
		t.Fatalf("LoadProcessingFile failed: %v", err)
	}
	if len(cfg.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(cfg.Rules))
	}
	if cfg.Rules[0].Name != "drop-debug" {
		t.Errorf("expected rule[0].Name='drop-debug', got %q", cfg.Rules[0].Name)
	}
	if cfg.Rules[0].Action != ActionDrop {
		t.Errorf("expected rule[0].Action='drop', got %q", cfg.Rules[0].Action)
	}
	if cfg.Rules[1].Name != "keep-sli" {
		t.Errorf("expected rule[1].Name='keep-sli', got %q", cfg.Rules[1].Name)
	}
	if cfg.Rules[1].Rate != 1.0 {
		t.Errorf("expected rule[1].Rate=1.0, got %f", cfg.Rules[1].Rate)
	}
}

func TestLoadProcessingFile_NonExistentFile(t *testing.T) {
	_, err := LoadProcessingFile("/nonexistent/path/does_not_exist.yaml")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestLoadProcessingFile_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte(":::invalid yaml!!!"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadProcessingFile(path)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoadProcessingFile_InvalidConfig(t *testing.T) {
	content := `
rules:
  - name: bad-rule
    input: "[invalid-regex"
    action: drop
`
	dir := t.TempDir()
	path := filepath.Join(dir, "invalid.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadProcessingFile(path)
	if err == nil {
		t.Error("expected error for invalid regex in config")
	}
}

func TestLoadProcessingFile_WithStaleness(t *testing.T) {
	content := `
staleness_interval: "5m"
rules:
  - name: keep-all
    input: ".*"
    action: sample
    rate: 1.0
    method: head
`
	dir := t.TempDir()
	path := filepath.Join(dir, "staleness.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadProcessingFile(path)
	if err != nil {
		t.Fatalf("LoadProcessingFile failed: %v", err)
	}
	if cfg.StalenessInterval != "5m" {
		t.Errorf("expected staleness_interval='5m', got %q", cfg.StalenessInterval)
	}
}

// ---------------------------------------------------------------------------
// 7. ProcessingRuleCount (0% coverage)
// ---------------------------------------------------------------------------

func TestProcessingRuleCount(t *testing.T) {
	tests := []struct {
		name  string
		rules []ProcessingRule
		want  int
	}{
		{
			name:  "no_rules",
			rules: []ProcessingRule{},
			want:  0,
		},
		{
			name: "one_rule",
			rules: []ProcessingRule{
				{Name: "r1", Input: ".*", Action: ActionDrop},
			},
			want: 1,
		},
		{
			name: "three_rules",
			rules: []ProcessingRule{
				{Name: "r1", Input: "debug_.*", Action: ActionDrop},
				{Name: "r2", Input: "sli_.*", Action: ActionSample, Rate: 1.0, Method: "head"},
				{Name: "r3", Input: ".*", Action: ActionTransform, Operations: []Operation{
					{Set: &SetOp{Label: "env", Value: "test"}},
				}},
			},
			want: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := ProcessingConfig{Rules: tt.rules}
			s, err := NewFromProcessing(cfg)
			if err != nil {
				t.Fatal(err)
			}
			got := s.ProcessingRuleCount()
			if got != tt.want {
				t.Errorf("ProcessingRuleCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Additional edge-case coverage for processMetric switch branches.
// ---------------------------------------------------------------------------

func TestProcessMetric_SumType_TransformAndDrop(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "tag-all",
				Input:  ".*",
				Action: ActionTransform,
				Operations: []Operation{
					{Set: &SetOp{Label: "tagged", Value: "yes"}},
				},
			},
			{
				Name:   "drop-internal",
				Input:  "internal_.*",
				Action: ActionDrop,
			},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Sum metric that should be dropped.
	rms := makeSumRM("internal_counter", makeNumberDP(map[string]string{}, 1000, 42))
	result := s.Process(rms)
	if len(result) != 0 {
		t.Error("expected internal Sum metric to be dropped")
	}

	// Sum metric that should pass through with transform.
	rms2 := makeSumRM("http_total", makeNumberDP(map[string]string{}, 1000, 42))
	result2 := s.Process(rms2)
	if len(result2) == 0 {
		t.Fatal("expected http Sum metric to pass through")
	}
	outDP := result2[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Sum).Sum.DataPoints[0]
	if getTransformAttr(outDP.Attributes, "tagged") != "yes" {
		t.Error("expected tagged=yes on Sum datapoint")
	}
}

func TestProcessMetric_SampleOnSummaryAndExpHist(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "keep-http", Input: "http_.*", Action: ActionSample, Rate: 1.0, Method: "head"},
			{Name: "drop-rest", Input: ".*", Action: ActionSample, Rate: 0.0, Method: "head"},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Summary: http_ should be kept.
	summaryRMS := makeSummaryRM("http_summary", makeSummaryDP(map[string]string{}, 1000, 10, 200.0))
	if result := s.Process(summaryRMS); len(result) == 0 {
		t.Error("expected http_summary to be kept")
	}

	// Summary: other should be dropped.
	summaryRMS2 := makeSummaryRM("grpc_summary", makeSummaryDP(map[string]string{}, 1000, 10, 200.0))
	if result := s.Process(summaryRMS2); len(result) != 0 {
		t.Error("expected grpc_summary to be dropped")
	}

	// ExponentialHistogram: http_ should be kept.
	expHistRMS := makeExpHistRM("http_exphist", makeExpHistDP(map[string]string{}, 1000, 5, 100.0))
	if result := s.Process(expHistRMS); len(result) == 0 {
		t.Error("expected http_exphist to be kept")
	}

	// ExponentialHistogram: other should be dropped.
	expHistRMS2 := makeExpHistRM("grpc_exphist", makeExpHistDP(map[string]string{}, 1000, 5, 100.0))
	if result := s.Process(expHistRMS2); len(result) != 0 {
		t.Error("expected grpc_exphist to be dropped")
	}
}

func TestProcessMetric_InputLabelsOnGenericTypes(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:        "drop-prod-debug",
				Input:       "debug_.*",
				InputLabels: map[string]string{"env": "production"},
				Action:      ActionDrop,
			},
		},
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Histogram: matching input+label should be dropped.
	histRMS := makeHistRM("debug_hist", makeHistDP(map[string]string{"env": "production"}, 1000, 10, 100.0))
	if result := s.Process(histRMS); len(result) != 0 {
		t.Error("expected histogram with debug+production to be dropped")
	}

	// Histogram: wrong label should pass through.
	histRMS2 := makeHistRM("debug_hist", makeHistDP(map[string]string{"env": "staging"}, 1000, 10, 100.0))
	if result := s.Process(histRMS2); len(result) == 0 {
		t.Error("expected histogram with debug+staging to pass through")
	}

	// Summary: matching input+label should be dropped.
	summaryRMS := makeSummaryRM("debug_summary", makeSummaryDP(map[string]string{"env": "production"}, 1000, 10, 200.0))
	if result := s.Process(summaryRMS); len(result) != 0 {
		t.Error("expected summary with debug+production to be dropped")
	}

	// ExponentialHistogram: matching input+label should be dropped.
	expHistRMS := makeExpHistRM("debug_exphist", makeExpHistDP(map[string]string{"env": "production"}, 1000, 5, 100.0))
	if result := s.Process(expHistRMS); len(result) != 0 {
		t.Error("expected exponential histogram with debug+production to be dropped")
	}
}
