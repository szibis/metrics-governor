package sampling

import (
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

func makeAttrs(kvs ...string) []*commonpb.KeyValue {
	attrs := make([]*commonpb.KeyValue, 0, len(kvs)/2)
	for i := 0; i < len(kvs)-1; i += 2 {
		attrs = append(attrs, &commonpb.KeyValue{
			Key:   kvs[i],
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: kvs[i+1]}},
		})
	}
	return attrs
}

func makeGaugeMetric(name string, attrs []*commonpb.KeyValue) *metricspb.Metric {
	return &metricspb.Metric{
		Name: name,
		Data: &metricspb.Metric_Gauge{
			Gauge: &metricspb.Gauge{
				DataPoints: []*metricspb.NumberDataPoint{
					{Attributes: attrs},
				},
			},
		},
	}
}

func makeRM(metrics ...*metricspb.Metric) []*metricspb.ResourceMetrics {
	return []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{Metrics: metrics},
			},
		},
	}
}

func TestParse(t *testing.T) {
	data := []byte(`
default_rate: 0.5
strategy: head
rules:
  - name: keep_sli
    match:
      __name__: "sli_.*"
    rate: 1.0
  - name: drop_debug
    match:
      __name__: "debug_.*"
    rate: 0.01
`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if cfg.DefaultRate != 0.5 {
		t.Errorf("expected default_rate 0.5, got %f", cfg.DefaultRate)
	}
	if cfg.Strategy != StrategyHead {
		t.Errorf("expected strategy head, got %s", cfg.Strategy)
	}
	if len(cfg.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(cfg.Rules))
	}
	if cfg.Rules[0].Name != "keep_sli" {
		t.Errorf("expected rule name 'keep_sli', got '%s'", cfg.Rules[0].Name)
	}
	if cfg.Rules[1].Rate != 0.01 {
		t.Errorf("expected rate 0.01, got %f", cfg.Rules[1].Rate)
	}
}

func TestParse_InvalidRegex(t *testing.T) {
	data := []byte(`
rules:
  - name: bad
    match:
      __name__: "[invalid"
    rate: 0.5
`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_, err = New(cfg)
	if err == nil {
		t.Error("expected error for invalid regex")
	}
}

func TestParse_InvalidRate(t *testing.T) {
	data := []byte(`
rules:
  - name: bad
    match:
      __name__: "test"
    rate: 1.5
`)
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	_, err = New(cfg)
	if err == nil {
		t.Error("expected error for rate > 1.0")
	}
}

func TestKeepAllByDefault(t *testing.T) {
	cfg := FileConfig{DefaultRate: 1.0}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	m := makeGaugeMetric("test_metric", makeAttrs("env", "prod"))
	rms := makeRM(m)
	result := s.Sample(rms)

	if len(result) != 1 {
		t.Fatalf("expected 1 ResourceMetrics, got %d", len(result))
	}
}

func TestDropAll(t *testing.T) {
	cfg := FileConfig{DefaultRate: 0.0}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	m := makeGaugeMetric("test_metric", makeAttrs("env", "prod"))
	rms := makeRM(m)
	result := s.Sample(rms)

	if len(result) != 0 {
		t.Errorf("expected 0 ResourceMetrics when rate=0, got %d", len(result))
	}
}

func TestRuleMatchKeepsAll(t *testing.T) {
	cfg := FileConfig{
		DefaultRate: 0.0, // drop all by default
		Rules: []Rule{
			{
				Name:  "keep_sli",
				Match: map[string]string{"__name__": "sli_.*"},
				Rate:  1.0, // but keep SLI metrics
			},
		},
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	sliMetric := makeGaugeMetric("sli_latency", makeAttrs("service", "api"))
	nonSliMetric := makeGaugeMetric("debug_info", makeAttrs("service", "api"))
	rms := makeRM(sliMetric, nonSliMetric)
	result := s.Sample(rms)

	// SLI metric should be kept, debug metric should be dropped
	if len(result) != 1 {
		t.Fatalf("expected 1 ResourceMetrics, got %d", len(result))
	}
	if len(result[0].ScopeMetrics[0].Metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(result[0].ScopeMetrics[0].Metrics))
	}
	if result[0].ScopeMetrics[0].Metrics[0].Name != "sli_latency" {
		t.Errorf("expected metric 'sli_latency', got '%s'", result[0].ScopeMetrics[0].Metrics[0].Name)
	}
}

func TestRuleMatchByLabel(t *testing.T) {
	cfg := FileConfig{
		DefaultRate: 1.0,
		Rules: []Rule{
			{
				Name:  "drop_dev",
				Match: map[string]string{"env": "dev"},
				Rate:  0.0,
			},
		},
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	devMetric := makeGaugeMetric("metric1", makeAttrs("env", "dev"))
	prodMetric := makeGaugeMetric("metric2", makeAttrs("env", "prod"))
	rms := makeRM(devMetric, prodMetric)
	result := s.Sample(rms)

	if len(result) != 1 {
		t.Fatalf("expected 1 ResourceMetrics, got %d", len(result))
	}
	if result[0].ScopeMetrics[0].Metrics[0].Name != "metric2" {
		t.Errorf("expected metric 'metric2' (prod), got '%s'", result[0].ScopeMetrics[0].Metrics[0].Name)
	}
}

func TestMultipleMatchConditions(t *testing.T) {
	cfg := FileConfig{
		DefaultRate: 1.0,
		Rules: []Rule{
			{
				Name:  "specific",
				Match: map[string]string{"__name__": "debug_.*", "env": "dev"},
				Rate:  0.0,
			},
		},
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Matches both conditions — should be dropped
	m1 := makeGaugeMetric("debug_info", makeAttrs("env", "dev"))
	// Only matches __name__ — should be kept
	m2 := makeGaugeMetric("debug_info", makeAttrs("env", "prod"))
	// Doesn't match __name__ — should be kept
	m3 := makeGaugeMetric("sli_latency", makeAttrs("env", "dev"))
	rms := makeRM(m1, m2, m3)
	result := s.Sample(rms)

	if len(result) != 1 {
		t.Fatalf("expected 1 ResourceMetrics, got %d", len(result))
	}
	metrics := result[0].ScopeMetrics[0].Metrics
	if len(metrics) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(metrics))
	}
}

func TestFirstMatchWins(t *testing.T) {
	cfg := FileConfig{
		DefaultRate: 0.0,
		Rules: []Rule{
			{
				Name:  "keep_critical",
				Match: map[string]string{"__name__": "critical_.*"},
				Rate:  1.0,
			},
			{
				Name:  "drop_all_metrics",
				Match: map[string]string{"__name__": ".*"},
				Rate:  0.0,
			},
		},
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	m := makeGaugeMetric("critical_alert", makeAttrs())
	rms := makeRM(m)
	result := s.Sample(rms)

	// First rule should match and keep
	if len(result) != 1 {
		t.Errorf("expected critical metric to be kept by first matching rule, got %d results", len(result))
	}
}

func TestEmptyInput(t *testing.T) {
	cfg := FileConfig{DefaultRate: 0.5}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	result := s.Sample(nil)
	if result != nil {
		t.Errorf("expected nil for nil input, got %v", result)
	}

	result = s.Sample([]*metricspb.ResourceMetrics{})
	if len(result) != 0 {
		t.Errorf("expected empty for empty input, got %d", len(result))
	}
}

func TestReloadConfig(t *testing.T) {
	cfg := FileConfig{DefaultRate: 1.0}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Initially keeps all
	m := makeGaugeMetric("test", makeAttrs())
	rms := makeRM(m)
	result := s.Sample(rms)
	if len(result) != 1 {
		t.Fatal("expected keep before reload")
	}

	// Reload to drop all
	err = s.ReloadConfig(FileConfig{DefaultRate: 0.0})
	if err != nil {
		t.Fatal(err)
	}

	m = makeGaugeMetric("test", makeAttrs())
	rms = makeRM(m)
	result = s.Sample(rms)
	if len(result) != 0 {
		t.Error("expected drop after reload")
	}
}

func TestReloadConfig_InvalidRegex(t *testing.T) {
	cfg := FileConfig{DefaultRate: 1.0}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	err = s.ReloadConfig(FileConfig{
		Rules: []Rule{
			{Match: map[string]string{"__name__": "[invalid"}, Rate: 0.5},
		},
	})
	if err == nil {
		t.Error("expected error for invalid regex on reload")
	}

	// Should still keep all (original config preserved)
	m := makeGaugeMetric("test", makeAttrs())
	rms := makeRM(m)
	result := s.Sample(rms)
	if len(result) != 1 {
		t.Error("expected original config preserved after failed reload")
	}
}

func TestOpsCounter(t *testing.T) {
	cfg := FileConfig{DefaultRate: 0.5, Strategy: StrategyProbabilistic}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	before := s.Ops()
	m := makeGaugeMetric("test", makeAttrs())
	rms := makeRM(m)
	s.Sample(rms)

	after := s.Ops()
	if after <= before {
		t.Errorf("expected ops counter to increase, before=%d after=%d", before, after)
	}
}

func TestDefaultRateClamping(t *testing.T) {
	// Negative rate should clamp to 0.0 (drop all)
	cfg := FileConfig{DefaultRate: -0.5}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	m := makeGaugeMetric("test", makeAttrs())
	rms := makeRM(m)
	result := s.Sample(rms)
	if len(result) != 0 {
		t.Error("expected negative rate to clamp to 0.0 (drop all)")
	}
}

func TestDefaultRateAboveOne(t *testing.T) {
	// Rate > 1.0 should clamp to 1.0
	cfg := FileConfig{DefaultRate: 2.0}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	m := makeGaugeMetric("test", makeAttrs())
	rms := makeRM(m)
	result := s.Sample(rms)
	if len(result) != 1 {
		t.Error("expected rate > 1.0 to clamp to 1.0 (keep all)")
	}
}

func TestSumMetricType(t *testing.T) {
	cfg := FileConfig{DefaultRate: 0.0}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	m := &metricspb.Metric{
		Name: "sum_metric",
		Data: &metricspb.Metric_Sum{
			Sum: &metricspb.Sum{
				DataPoints: []*metricspb.NumberDataPoint{
					{Attributes: makeAttrs("env", "prod")},
				},
			},
		},
	}
	rms := makeRM(m)
	result := s.Sample(rms)
	if len(result) != 0 {
		t.Error("expected Sum metric to be dropped")
	}
}

func TestHistogramMetricType(t *testing.T) {
	cfg := FileConfig{DefaultRate: 0.0}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	m := &metricspb.Metric{
		Name: "histogram_metric",
		Data: &metricspb.Metric_Histogram{
			Histogram: &metricspb.Histogram{
				DataPoints: []*metricspb.HistogramDataPoint{
					{Attributes: makeAttrs("env", "prod")},
				},
			},
		},
	}
	rms := makeRM(m)
	result := s.Sample(rms)
	if len(result) != 0 {
		t.Error("expected Histogram metric to be dropped")
	}
}

func TestProbabilisticStrategy(t *testing.T) {
	cfg := FileConfig{
		DefaultRate: 0.5,
		Strategy:    StrategyProbabilistic,
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Run many samples and check rough distribution
	kept := 0
	total := 1000
	for i := 0; i < total; i++ {
		m := makeGaugeMetric("test", makeAttrs())
		rms := makeRM(m)
		result := s.Sample(rms)
		if len(result) > 0 {
			kept++
		}
	}

	// With 50% rate over 1000 samples, expect between 30% and 70%
	ratio := float64(kept) / float64(total)
	if ratio < 0.3 || ratio > 0.7 {
		t.Errorf("probabilistic sampling ratio %f outside expected range [0.3, 0.7]", ratio)
	}
}

func TestNilMetric(t *testing.T) {
	cfg := FileConfig{DefaultRate: 1.0}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	rms := []*metricspb.ResourceMetrics{nil}
	result := s.Sample(rms)
	if len(result) != 0 {
		t.Error("expected nil resource metrics to be filtered out")
	}
}

func BenchmarkSample(b *testing.B) {
	cfg := FileConfig{
		DefaultRate: 0.5,
		Strategy:    StrategyHead,
		Rules: []Rule{
			{Name: "sli", Match: map[string]string{"__name__": "sli_.*"}, Rate: 1.0},
			{Name: "debug", Match: map[string]string{"__name__": "debug_.*"}, Rate: 0.01},
		},
	}
	s, err := New(cfg)
	if err != nil {
		b.Fatal(err)
	}

	metrics := make([]*metricspb.Metric, 100)
	for i := 0; i < 100; i++ {
		metrics[i] = makeGaugeMetric("test_metric", makeAttrs("env", "prod", "service", "api"))
	}
	rms := makeRM(metrics...)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Sample(rms)
	}
}
