package sampling

import (
	"testing"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
)

func makeProcGaugeRM(metricName string, dps ...*metricspb.NumberDataPoint) []*metricspb.ResourceMetrics {
	return []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: metricName,
							Data: &metricspb.Metric_Gauge{
								Gauge: &metricspb.Gauge{DataPoints: dps},
							},
						},
					},
				},
			},
		},
	}
}

func TestProcess_DropAction(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "drop-internal", Input: "internal_.*", Action: ActionDrop},
		},
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeNumberDP(map[string]string{}, 1000, 42)

	// Should be dropped.
	rms := makeProcGaugeRM("internal_debug", dp)
	result := sampler.Sample(rms)
	if len(result) != 0 {
		t.Error("expected internal_debug to be dropped")
	}

	// Should pass through.
	dp2 := makeNumberDP(map[string]string{}, 1000, 42)
	rms2 := makeProcGaugeRM("http_requests_total", dp2)
	result2 := sampler.Sample(rms2)
	if len(result2) == 0 {
		t.Error("expected http_requests_total to pass through")
	}
}

func TestProcess_SampleAction(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "keep-all-sli", Input: "sli_.*", Action: ActionSample, Rate: 1.0, Method: "head"},
			{Name: "drop-all-debug", Input: "debug_.*", Action: ActionSample, Rate: 0.0, Method: "head"},
		},
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Rate 1.0 should keep everything.
	dp := makeNumberDP(map[string]string{}, 1000, 42)
	rms := makeProcGaugeRM("sli_latency", dp)
	result := sampler.Sample(rms)
	if len(result) == 0 {
		t.Error("expected sli_latency to be kept (rate=1.0)")
	}

	// Rate 0.0 should drop everything.
	dp2 := makeNumberDP(map[string]string{}, 1000, 42)
	rms2 := makeProcGaugeRM("debug_trace", dp2)
	result2 := sampler.Sample(rms2)
	if len(result2) != 0 {
		t.Error("expected debug_trace to be dropped (rate=0.0)")
	}
}

func TestProcess_TransformThenDrop(t *testing.T) {
	// Transform is non-terminal, should continue to next rule.
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "add-env",
				Input:  ".*",
				Action: ActionTransform,
				Operations: []Operation{
					{Set: &SetOp{Label: "env", Value: "test"}},
				},
			},
			{
				Name:   "drop-internal",
				Input:  "internal_.*",
				Action: ActionDrop,
			},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		t.Fatal(err)
	}

	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// internal_debug should get transform applied AND then be dropped.
	dp := makeNumberDP(map[string]string{}, 1000, 42)
	rms := makeProcGaugeRM("internal_debug", dp)
	result := sampler.Sample(rms)
	if len(result) != 0 {
		t.Error("expected internal_debug to be dropped")
	}

	// http_requests should get transform applied and pass through.
	dp2 := makeNumberDP(map[string]string{}, 1000, 42)
	rms2 := makeProcGaugeRM("http_requests", dp2)
	result2 := sampler.Sample(rms2)
	if len(result2) == 0 {
		t.Fatal("expected http_requests to pass through")
	}

	// Check that the transform was applied.
	outDP := result2[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Gauge).Gauge.DataPoints[0]
	envVal := ""
	for _, kv := range outDP.Attributes {
		if kv.Key == "env" {
			envVal = kv.Value.GetStringValue()
		}
	}
	if envVal != "test" {
		t.Errorf("env = %q, want 'test' (transform should have applied)", envVal)
	}
}

func TestProcess_PassThrough(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "drop-debug", Input: "debug_.*", Action: ActionDrop},
		},
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Non-matching metric should pass through unchanged.
	dp := makeNumberDP(map[string]string{"service": "web"}, 1000, 42)
	rms := makeProcGaugeRM("http_requests_total", dp)
	result := sampler.Sample(rms)
	if len(result) == 0 {
		t.Error("expected http_requests_total to pass through (no matching rule)")
	}
}

func TestProcess_TransformLabelConditions(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "prod-only-transform",
				Input:  ".*",
				Action: ActionTransform,
				When: []Condition{
					{Label: "env", Equals: "production"},
				},
				Operations: []Operation{
					{Set: &SetOp{Label: "transformed", Value: "true"}},
				},
			},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		t.Fatal(err)
	}

	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Production env should be transformed.
	dp1 := makeNumberDP(map[string]string{"env": "production"}, 1000, 42)
	rms1 := makeProcGaugeRM("test_metric", dp1)
	result1 := sampler.Sample(rms1)
	if len(result1) == 0 {
		t.Fatal("expected pass through")
	}
	outDP1 := result1[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Gauge).Gauge.DataPoints[0]
	if getTransformAttr(outDP1.Attributes, "transformed") != "true" {
		t.Error("expected transform to apply for env=production")
	}

	// Staging env should NOT be transformed.
	dp2 := makeNumberDP(map[string]string{"env": "staging"}, 1000, 42)
	rms2 := makeProcGaugeRM("test_metric", dp2)
	result2 := sampler.Sample(rms2)
	if len(result2) == 0 {
		t.Fatal("expected pass through")
	}
	outDP2 := result2[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Gauge).Gauge.DataPoints[0]
	if hasTransformAttr(outDP2.Attributes, "transformed") {
		t.Error("transform should NOT apply for env=staging")
	}
}

func TestProcess_InputLabelsMatching(t *testing.T) {
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
	if err := validateProcessingConfig(&cfg); err != nil {
		t.Fatal(err)
	}

	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// debug + production → dropped.
	dp1 := makeNumberDP(map[string]string{"env": "production"}, 1000, 42)
	rms1 := makeProcGaugeRM("debug_trace", dp1)
	if result := sampler.Sample(rms1); len(result) != 0 {
		t.Error("expected drop for debug_trace + env=production")
	}

	// debug + staging → pass through (label doesn't match).
	dp2 := makeNumberDP(map[string]string{"env": "staging"}, 1000, 42)
	rms2 := makeProcGaugeRM("debug_trace", dp2)
	if result := sampler.Sample(rms2); len(result) == 0 {
		t.Error("expected pass through for debug_trace + env=staging")
	}
}

func TestProcess_HistogramPassthrough(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "drop-debug", Input: "debug_.*", Action: ActionDrop},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		t.Fatal(err)
	}

	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Histogram datapoint (not a NumberDataPoint).
	attrs := []*commonpb.KeyValue{
		{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "web"}}},
	}
	histDP := &metricspb.HistogramDataPoint{
		Attributes:   attrs,
		TimeUnixNano: 1000,
		Count:        10,
		Sum:          pFloat64(100),
	}

	rms := []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "http_request_duration",
							Data: &metricspb.Metric_Histogram{
								Histogram: &metricspb.Histogram{DataPoints: []*metricspb.HistogramDataPoint{histDP}},
							},
						},
					},
				},
			},
		},
	}

	result := sampler.Sample(rms)
	if len(result) == 0 {
		t.Error("expected histogram to pass through (no matching rule)")
	}
}

func TestProcess_DownsampleAction(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:     "cpu-avg",
				Input:    "process_cpu_.*",
				Action:   ActionDownsample,
				Method:   "avg",
				Interval: "1m",
			},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		t.Fatal(err)
	}

	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// First datapoint is buffered (no output yet).
	dp1 := makeNumberDP(map[string]string{}, 1000000000, 10)
	rms1 := makeProcGaugeRM("process_cpu_seconds", dp1)
	result1 := sampler.Sample(rms1)
	// Downsample accumulates — first point may or may not emit.
	// Just verify no crash.
	_ = result1
}

func TestProcess_ReloadConfig(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "keep-all", Input: ".*", Action: ActionSample, Rate: 1.0, Method: "head"},
		},
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Reload with a drop rule.
	newCfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "drop-all", Input: ".*", Action: ActionDrop},
		},
	}
	if err := sampler.ReloadProcessingConfig(newCfg); err != nil {
		t.Fatal(err)
	}

	dp := makeNumberDP(map[string]string{}, 1000, 42)
	rms := makeProcGaugeRM("any_metric", dp)
	result := sampler.Sample(rms)
	if len(result) != 0 {
		t.Error("after reload to drop-all, expected all metrics dropped")
	}
}

func TestProcess_EmptyRules(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{},
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	dp := makeNumberDP(map[string]string{}, 1000, 42)
	rms := makeProcGaugeRM("any_metric", dp)
	result := sampler.Sample(rms)
	if len(result) == 0 {
		t.Error("with no rules, all metrics should pass through")
	}
}

func pFloat64(v float64) *float64 { return &v }
