package relabel

import (
	"testing"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
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

func makeGaugeMetric(name string, attrs ...[]*commonpb.KeyValue) *metricspb.Metric {
	dps := make([]*metricspb.NumberDataPoint, len(attrs))
	for i, a := range attrs {
		dps[i] = &metricspb.NumberDataPoint{
			Attributes: a,
			Value:      &metricspb.NumberDataPoint_AsInt{AsInt: 1},
		}
	}
	return &metricspb.Metric{
		Name: name,
		Data: &metricspb.Metric_Gauge{
			Gauge: &metricspb.Gauge{DataPoints: dps},
		},
	}
}

func makeRM(metrics ...*metricspb.Metric) []*metricspb.ResourceMetrics {
	return []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{Metrics: metrics},
			},
		},
	}
}

func getAttrValue(attrs []*commonpb.KeyValue, key string) string {
	for _, kv := range attrs {
		if kv.Key == key {
			return kv.Value.GetStringValue()
		}
	}
	return ""
}

func TestParse(t *testing.T) {
	yaml := []byte(`
relabel_configs:
  - source_labels: [__name__]
    regex: "internal_.*"
    action: drop
  - source_labels: [env]
    target_label: environment
    action: replace
`)

	configs, err := Parse(yaml)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(configs) != 2 {
		t.Fatalf("expected 2 configs, got %d", len(configs))
	}
	if configs[0].Action != ActionDrop {
		t.Errorf("expected drop action, got %s", configs[0].Action)
	}
	if configs[1].Action != ActionReplace {
		t.Errorf("expected replace action, got %s", configs[1].Action)
	}
}

func TestParse_InvalidRegex(t *testing.T) {
	yaml := []byte(`
relabel_configs:
  - source_labels: [__name__]
    regex: "[invalid"
    action: drop
`)
	_, err := Parse(yaml)
	if err == nil {
		t.Fatal("expected error for invalid regex")
	}
}

func TestParse_Defaults(t *testing.T) {
	yaml := []byte(`
relabel_configs:
  - source_labels: [env]
    target_label: environment
`)
	configs, err := Parse(yaml)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	c := configs[0]
	if c.Separator != ";" {
		t.Errorf("expected default separator ';', got %q", c.Separator)
	}
	if c.Regex != "(.*)" {
		t.Errorf("expected default regex '(.*)', got %q", c.Regex)
	}
	if c.Replacement != "$1" {
		t.Errorf("expected default replacement '$1', got %q", c.Replacement)
	}
	if c.Action != ActionReplace {
		t.Errorf("expected default action 'replace', got %s", c.Action)
	}
}

func TestActionReplace(t *testing.T) {
	configs := []Config{
		{
			SourceLabels: []string{"env"},
			TargetLabel:  "environment",
			Action:       ActionReplace,
		},
	}
	r, err := New(configs)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	attrs := makeAttrs("env", "production")
	rms := makeRM(makeGaugeMetric("test_metric", attrs))

	result := r.Relabel(rms)
	if len(result) != 1 {
		t.Fatalf("expected 1 ResourceMetrics, got %d", len(result))
	}

	dp := result[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Gauge).Gauge.DataPoints[0]
	val := getAttrValue(dp.Attributes, "environment")
	if val != "production" {
		t.Errorf("expected environment=production, got %q", val)
	}
}

func TestActionReplace_Regex(t *testing.T) {
	configs := []Config{
		{
			SourceLabels: []string{"__name__"},
			Regex:        "old_prefix_(.*)",
			TargetLabel:  "__name__",
			Replacement:  "new_prefix_$1",
			Action:       ActionReplace,
		},
	}
	r, err := New(configs)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	rms := makeRM(makeGaugeMetric("old_prefix_requests", makeAttrs("service", "api")))

	result := r.Relabel(rms)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}

	metricName := result[0].ScopeMetrics[0].Metrics[0].Name
	if metricName != "new_prefix_requests" {
		t.Errorf("expected metric name 'new_prefix_requests', got %q", metricName)
	}
}

func TestActionKeep(t *testing.T) {
	configs := []Config{
		{
			SourceLabels: []string{"env"},
			Regex:        "prod.*",
			Action:       ActionKeep,
		},
	}
	r, err := New(configs)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	rms := makeRM(
		makeGaugeMetric("metric_a", makeAttrs("env", "production")),
		makeGaugeMetric("metric_b", makeAttrs("env", "staging")),
	)

	result := r.Relabel(rms)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}

	// Only metric_a should remain (production matches prod.*)
	metrics := result[0].ScopeMetrics[0].Metrics
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	if metrics[0].Name != "metric_a" {
		t.Errorf("expected metric_a, got %s", metrics[0].Name)
	}
}

func TestActionDrop(t *testing.T) {
	configs := []Config{
		{
			SourceLabels: []string{"__name__"},
			Regex:        "internal_.*",
			Action:       ActionDrop,
		},
	}
	r, err := New(configs)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	rms := makeRM(
		makeGaugeMetric("internal_debug", makeAttrs("service", "api")),
		makeGaugeMetric("public_metric", makeAttrs("service", "api")),
	)

	result := r.Relabel(rms)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}

	metrics := result[0].ScopeMetrics[0].Metrics
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	if metrics[0].Name != "public_metric" {
		t.Errorf("expected public_metric, got %s", metrics[0].Name)
	}
}

func TestActionLabelDrop(t *testing.T) {
	configs := []Config{
		{
			Regex:  "debug_.*",
			Action: ActionLabelDrop,
		},
	}
	r, err := New(configs)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	attrs := makeAttrs("service", "api", "debug_id", "123", "debug_trace", "abc", "env", "prod")
	rms := makeRM(makeGaugeMetric("test", attrs))

	result := r.Relabel(rms)
	dp := result[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Gauge).Gauge.DataPoints[0]

	for _, kv := range dp.Attributes {
		if kv.Key == "debug_id" || kv.Key == "debug_trace" {
			t.Errorf("label %q should have been dropped", kv.Key)
		}
	}
	if getAttrValue(dp.Attributes, "service") != "api" {
		t.Error("service label should be preserved")
	}
	if getAttrValue(dp.Attributes, "env") != "prod" {
		t.Error("env label should be preserved")
	}
}

func TestActionLabelKeep(t *testing.T) {
	configs := []Config{
		{
			Regex:  "service|env",
			Action: ActionLabelKeep,
		},
	}
	r, err := New(configs)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	attrs := makeAttrs("service", "api", "debug_id", "123", "env", "prod", "cluster", "us-east")
	rms := makeRM(makeGaugeMetric("test", attrs))

	result := r.Relabel(rms)
	dp := result[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Gauge).Gauge.DataPoints[0]

	kept := make(map[string]bool)
	for _, kv := range dp.Attributes {
		kept[kv.Key] = true
	}

	if !kept["service"] {
		t.Error("service should be kept")
	}
	if !kept["env"] {
		t.Error("env should be kept")
	}
	if kept["debug_id"] {
		t.Error("debug_id should be dropped")
	}
	if kept["cluster"] {
		t.Error("cluster should be dropped")
	}
	// __name__ is extracted to metric name, so it won't be in attrs
	// Verify the metric name is preserved
	if result[0].ScopeMetrics[0].Metrics[0].Name != "test" {
		t.Error("metric name should be preserved")
	}
}

func TestActionLabelMap(t *testing.T) {
	configs := []Config{
		{
			Regex:       "label_(.*)",
			Replacement: "mapped_$1",
			Action:      ActionLabelMap,
		},
	}
	r, err := New(configs)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	attrs := makeAttrs("label_foo", "bar", "label_baz", "qux", "other", "value")
	rms := makeRM(makeGaugeMetric("test", attrs))

	result := r.Relabel(rms)
	dp := result[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Gauge).Gauge.DataPoints[0]

	if getAttrValue(dp.Attributes, "mapped_foo") != "bar" {
		t.Error("expected mapped_foo=bar")
	}
	if getAttrValue(dp.Attributes, "mapped_baz") != "qux" {
		t.Error("expected mapped_baz=qux")
	}
}

func TestActionHashMod(t *testing.T) {
	configs := []Config{
		{
			SourceLabels: []string{"service"},
			TargetLabel:  "__shard__",
			Modulus:      4,
			Action:       ActionHashMod,
		},
	}
	r, err := New(configs)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	attrs := makeAttrs("service", "my-service")
	rms := makeRM(makeGaugeMetric("test", attrs))

	result := r.Relabel(rms)
	dp := result[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Gauge).Gauge.DataPoints[0]

	shard := getAttrValue(dp.Attributes, "__shard__")
	if shard == "" {
		t.Error("expected __shard__ to be set")
	}
	// Shard should be 0-3
	for _, valid := range []string{"0", "1", "2", "3"} {
		if shard == valid {
			return
		}
	}
	t.Errorf("shard %q is not in range 0-3", shard)
}

func TestMultipleRules(t *testing.T) {
	configs := []Config{
		{
			SourceLabels: []string{"__name__"},
			Regex:        "internal_.*",
			Action:       ActionDrop,
		},
		{
			SourceLabels: []string{"env"},
			TargetLabel:  "environment",
			Action:       ActionReplace,
		},
		{
			Regex:  "debug_.*",
			Action: ActionLabelDrop,
		},
	}
	r, err := New(configs)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	rms := makeRM(
		makeGaugeMetric("internal_debug", makeAttrs("env", "prod")),
		makeGaugeMetric("public_metric", makeAttrs("env", "prod", "debug_id", "123")),
	)

	result := r.Relabel(rms)

	metrics := result[0].ScopeMetrics[0].Metrics
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	if metrics[0].Name != "public_metric" {
		t.Errorf("expected public_metric, got %s", metrics[0].Name)
	}

	dp := metrics[0].Data.(*metricspb.Metric_Gauge).Gauge.DataPoints[0]
	if getAttrValue(dp.Attributes, "environment") != "prod" {
		t.Error("expected environment=prod from replace rule")
	}
	if getAttrValue(dp.Attributes, "debug_id") != "" {
		t.Error("debug_id should have been dropped by labeldrop rule")
	}
}

func TestEmptyConfigs(t *testing.T) {
	r, err := New(nil)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	rms := makeRM(makeGaugeMetric("test", makeAttrs("key", "value")))
	result := r.Relabel(rms)

	if len(result) != 1 {
		t.Fatalf("expected 1 result with no configs, got %d", len(result))
	}
}

func TestReloadConfig(t *testing.T) {
	r, err := New(nil)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// No rules — everything passes through
	rms := makeRM(makeGaugeMetric("internal_test", makeAttrs("key", "value")))
	result := r.Relabel(rms)
	if len(result) != 1 {
		t.Fatal("expected pass-through with no configs")
	}

	// Reload with drop rule
	err = r.ReloadConfig([]Config{
		{
			SourceLabels: []string{"__name__"},
			Regex:        "internal_.*",
			Action:       ActionDrop,
		},
	})
	if err != nil {
		t.Fatalf("ReloadConfig failed: %v", err)
	}

	rms = makeRM(makeGaugeMetric("internal_test", makeAttrs("key", "value")))
	result = r.Relabel(rms)
	if len(result) != 0 {
		t.Error("expected drop after reload")
	}
}

func TestOpsCounter(t *testing.T) {
	r, err := New([]Config{
		{
			SourceLabels: []string{"env"},
			TargetLabel:  "environment",
			Action:       ActionReplace,
		},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	rms := makeRM(makeGaugeMetric("test", makeAttrs("env", "prod")))
	r.Relabel(rms)

	if r.Ops() < 1 {
		t.Errorf("expected ops >= 1, got %d", r.Ops())
	}
}

func TestDropAllDatapoints(t *testing.T) {
	configs := []Config{
		{
			SourceLabels: []string{"__name__"},
			Regex:        ".*",
			Action:       ActionDrop,
		},
	}
	r, err := New(configs)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	rms := makeRM(
		makeGaugeMetric("a", makeAttrs("k", "v")),
		makeGaugeMetric("b", makeAttrs("k", "v")),
	)

	result := r.Relabel(rms)
	if len(result) != 0 {
		t.Errorf("expected 0 results, got %d", len(result))
	}
}

func TestSumMetricType(t *testing.T) {
	configs := []Config{
		{
			SourceLabels: []string{"env"},
			TargetLabel:  "environment",
			Action:       ActionReplace,
		},
	}
	r, err := New(configs)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// Create a Sum metric
	dp := &metricspb.NumberDataPoint{
		Attributes: makeAttrs("env", "staging"),
		Value:      &metricspb.NumberDataPoint_AsInt{AsInt: 100},
	}
	m := &metricspb.Metric{
		Name: "requests_total",
		Data: &metricspb.Metric_Sum{
			Sum: &metricspb.Sum{DataPoints: []*metricspb.NumberDataPoint{dp}},
		},
	}
	rms := makeRM(m)

	result := r.Relabel(rms)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}

	resultDP := result[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Sum).Sum.DataPoints[0]
	if getAttrValue(resultDP.Attributes, "environment") != "staging" {
		t.Error("expected environment=staging")
	}
}

func TestHistogramMetricType(t *testing.T) {
	configs := []Config{
		{
			SourceLabels: []string{"__name__"},
			Regex:        "internal_.*",
			Action:       ActionDrop,
		},
	}
	r, err := New(configs)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	dp := &metricspb.HistogramDataPoint{
		Attributes: makeAttrs("service", "api"),
	}
	m := &metricspb.Metric{
		Name: "internal_latency",
		Data: &metricspb.Metric_Histogram{
			Histogram: &metricspb.Histogram{DataPoints: []*metricspb.HistogramDataPoint{dp}},
		},
	}
	rms := makeRM(m)

	result := r.Relabel(rms)
	if len(result) != 0 {
		t.Error("expected internal_latency to be dropped")
	}
}

func TestNilInput(t *testing.T) {
	r, _ := New([]Config{
		{
			SourceLabels: []string{"env"},
			TargetLabel:  "x",
			Action:       ActionReplace,
		},
	})

	result := r.Relabel(nil)
	if len(result) != 0 {
		t.Error("expected empty for nil input")
	}
}

func BenchmarkRelabel(b *testing.B) {
	configs := []Config{
		{
			SourceLabels: []string{"__name__"},
			Regex:        "internal_.*",
			Action:       ActionDrop,
		},
		{
			SourceLabels: []string{"env"},
			TargetLabel:  "environment",
			Action:       ActionReplace,
		},
		{
			Regex:  "debug_.*",
			Action: ActionLabelDrop,
		},
	}
	r, _ := New(configs)

	rms := makeRM(
		makeGaugeMetric("http_requests_total", makeAttrs("env", "prod", "service", "api", "debug_trace", "abc")),
		makeGaugeMetric("http_duration_seconds", makeAttrs("env", "prod", "service", "api")),
		makeGaugeMetric("internal_gc_pause", makeAttrs("env", "prod")),
	)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		// Make a copy since Relabel modifies in-place
		cp := make([]*metricspb.ResourceMetrics, len(rms))
		copy(cp, rms)
		r.Relabel(cp)
	}
}
