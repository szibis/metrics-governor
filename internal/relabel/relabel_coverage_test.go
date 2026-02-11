package relabel

import (
	"os"
	"path/filepath"
	"testing"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
)

// --- LoadFile tests ---

func TestLoadFile_Valid(t *testing.T) {
	content := []byte(`
relabel_configs:
  - source_labels: [__name__]
    regex: "internal_.*"
    action: drop
  - source_labels: [env]
    target_label: environment
    action: replace
`)
	dir := t.TempDir()
	path := filepath.Join(dir, "relabel.yaml")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	configs, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile failed: %v", err)
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

func TestLoadFile_NonExistent(t *testing.T) {
	_, err := LoadFile("/tmp/does_not_exist_relabel_test.yaml")
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestLoadFile_InvalidYAML(t *testing.T) {
	content := []byte(`
relabel_configs:
  - source_labels: [__name__]
    regex: "[invalid"
    action: drop
`)
	dir := t.TempDir()
	path := filepath.Join(dir, "bad_relabel.yaml")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	_, err := LoadFile(path)
	if err == nil {
		t.Fatal("expected error for invalid regex in config file")
	}
}

func TestLoadFile_MalformedYAML(t *testing.T) {
	content := []byte(`this is not valid yaml: [[[`)
	dir := t.TempDir()
	path := filepath.Join(dir, "malformed.yaml")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	_, err := LoadFile(path)
	if err == nil {
		t.Fatal("expected error for malformed YAML")
	}
}

func TestLoadFile_EmptyConfigs(t *testing.T) {
	content := []byte(`
relabel_configs: []
`)
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}

	configs, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile failed: %v", err)
	}
	if len(configs) != 0 {
		t.Fatalf("expected 0 configs, got %d", len(configs))
	}
}

// --- relabelSummaryDataPoints tests ---

func makeSummaryMetric(name string, attrSets ...[]*commonpb.KeyValue) *metricspb.Metric {
	dps := make([]*metricspb.SummaryDataPoint, len(attrSets))
	for i, a := range attrSets {
		dps[i] = &metricspb.SummaryDataPoint{
			Attributes: a,
			Count:      10,
			Sum:        42.0,
		}
	}
	return &metricspb.Metric{
		Name: name,
		Data: &metricspb.Metric_Summary{
			Summary: &metricspb.Summary{DataPoints: dps},
		},
	}
}

func TestRelabelSummaryDataPoints_Replace(t *testing.T) {
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

	attrs := makeAttrs("env", "production", "service", "api")
	rms := makeRM(makeSummaryMetric("request_duration", attrs))

	result := r.Relabel(rms)
	if len(result) != 1 {
		t.Fatalf("expected 1 ResourceMetrics, got %d", len(result))
	}

	dp := result[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Summary).Summary.DataPoints[0]
	val := getAttrValue(dp.Attributes, "environment")
	if val != "production" {
		t.Errorf("expected environment=production, got %q", val)
	}
}

func TestRelabelSummaryDataPoints_LabelDrop(t *testing.T) {
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
	rms := makeRM(makeSummaryMetric("latency_summary", attrs))

	result := r.Relabel(rms)
	if len(result) != 1 {
		t.Fatalf("expected 1 ResourceMetrics, got %d", len(result))
	}

	dp := result[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Summary).Summary.DataPoints[0]
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

func TestRelabelSummaryDataPoints_Drop(t *testing.T) {
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
		makeSummaryMetric("internal_debug_summary", makeAttrs("service", "api")),
		makeSummaryMetric("public_summary", makeAttrs("service", "api")),
	)

	result := r.Relabel(rms)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}

	metrics := result[0].ScopeMetrics[0].Metrics
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	if metrics[0].Name != "public_summary" {
		t.Errorf("expected public_summary, got %s", metrics[0].Name)
	}
}

func TestRelabelSummaryDataPoints_DropAll(t *testing.T) {
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

	rms := makeRM(makeSummaryMetric("any_summary", makeAttrs("k", "v")))
	result := r.Relabel(rms)
	if len(result) != 0 {
		t.Errorf("expected 0 results when all dropped, got %d", len(result))
	}
}

func TestRelabelSummaryDataPoints_EmptyDataPoints(t *testing.T) {
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

	// Summary with no data points — the scope has no surviving metrics, so
	// the entire ResourceMetrics is filtered out.
	m := &metricspb.Metric{
		Name: "empty_summary",
		Data: &metricspb.Metric_Summary{
			Summary: &metricspb.Summary{DataPoints: nil},
		},
	}
	rms := makeRM(m)
	result := r.Relabel(rms)
	if len(result) != 0 {
		t.Fatalf("expected 0 results for empty summary data points, got %d", len(result))
	}
}

func TestRelabelSummaryDataPoints_Rename(t *testing.T) {
	configs := []Config{
		{
			SourceLabels: []string{"__name__"},
			Regex:        "old_(.*)",
			TargetLabel:  "__name__",
			Replacement:  "new_$1",
			Action:       ActionReplace,
		},
	}
	r, err := New(configs)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	rms := makeRM(makeSummaryMetric("old_latency", makeAttrs("service", "api")))
	result := r.Relabel(rms)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}

	metricName := result[0].ScopeMetrics[0].Metrics[0].Name
	if metricName != "new_latency" {
		t.Errorf("expected metric name 'new_latency', got %q", metricName)
	}
}

// --- relabelExponentialHistogramDataPoints tests ---

func makeExponentialHistogramMetric(name string, attrSets ...[]*commonpb.KeyValue) *metricspb.Metric {
	dps := make([]*metricspb.ExponentialHistogramDataPoint, len(attrSets))
	for i, a := range attrSets {
		dps[i] = &metricspb.ExponentialHistogramDataPoint{
			Attributes: a,
			Count:      10,
			Sum:        func() *float64 { v := 42.0; return &v }(),
			Scale:      2,
		}
	}
	return &metricspb.Metric{
		Name: name,
		Data: &metricspb.Metric_ExponentialHistogram{
			ExponentialHistogram: &metricspb.ExponentialHistogram{DataPoints: dps},
		},
	}
}

func TestRelabelExponentialHistogramDataPoints_Replace(t *testing.T) {
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

	attrs := makeAttrs("env", "staging", "region", "us-east")
	rms := makeRM(makeExponentialHistogramMetric("request_latency", attrs))

	result := r.Relabel(rms)
	if len(result) != 1 {
		t.Fatalf("expected 1 ResourceMetrics, got %d", len(result))
	}

	dp := result[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_ExponentialHistogram).ExponentialHistogram.DataPoints[0]
	val := getAttrValue(dp.Attributes, "environment")
	if val != "staging" {
		t.Errorf("expected environment=staging, got %q", val)
	}
}

func TestRelabelExponentialHistogramDataPoints_LabelDrop(t *testing.T) {
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

	attrs := makeAttrs("service", "web", "debug_request_id", "xyz", "env", "prod")
	rms := makeRM(makeExponentialHistogramMetric("exp_hist_metric", attrs))

	result := r.Relabel(rms)
	if len(result) != 1 {
		t.Fatalf("expected 1 ResourceMetrics, got %d", len(result))
	}

	dp := result[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_ExponentialHistogram).ExponentialHistogram.DataPoints[0]
	for _, kv := range dp.Attributes {
		if kv.Key == "debug_request_id" {
			t.Error("debug_request_id should have been dropped")
		}
	}
	if getAttrValue(dp.Attributes, "service") != "web" {
		t.Error("service label should be preserved")
	}
	if getAttrValue(dp.Attributes, "env") != "prod" {
		t.Error("env label should be preserved")
	}
}

func TestRelabelExponentialHistogramDataPoints_Drop(t *testing.T) {
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
		makeExponentialHistogramMetric("internal_exp_hist", makeAttrs("service", "api")),
		makeExponentialHistogramMetric("public_exp_hist", makeAttrs("service", "api")),
	)

	result := r.Relabel(rms)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}

	metrics := result[0].ScopeMetrics[0].Metrics
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	if metrics[0].Name != "public_exp_hist" {
		t.Errorf("expected public_exp_hist, got %s", metrics[0].Name)
	}
}

func TestRelabelExponentialHistogramDataPoints_DropAll(t *testing.T) {
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

	rms := makeRM(makeExponentialHistogramMetric("any_exp_hist", makeAttrs("k", "v")))
	result := r.Relabel(rms)
	if len(result) != 0 {
		t.Errorf("expected 0 results when all dropped, got %d", len(result))
	}
}

func TestRelabelExponentialHistogramDataPoints_EmptyDataPoints(t *testing.T) {
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

	// ExponentialHistogram with no data points — the scope has no surviving
	// metrics, so the entire ResourceMetrics is filtered out.
	m := &metricspb.Metric{
		Name: "empty_exp_hist",
		Data: &metricspb.Metric_ExponentialHistogram{
			ExponentialHistogram: &metricspb.ExponentialHistogram{DataPoints: nil},
		},
	}
	rms := makeRM(m)
	result := r.Relabel(rms)
	if len(result) != 0 {
		t.Fatalf("expected 0 results for empty exp histogram data points, got %d", len(result))
	}
}

func TestRelabelExponentialHistogramDataPoints_Rename(t *testing.T) {
	configs := []Config{
		{
			SourceLabels: []string{"__name__"},
			Regex:        "old_(.*)",
			TargetLabel:  "__name__",
			Replacement:  "new_$1",
			Action:       ActionReplace,
		},
	}
	r, err := New(configs)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	rms := makeRM(makeExponentialHistogramMetric("old_exp_hist", makeAttrs("service", "api")))
	result := r.Relabel(rms)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}

	metricName := result[0].ScopeMetrics[0].Metrics[0].Name
	if metricName != "new_exp_hist" {
		t.Errorf("expected metric name 'new_exp_hist', got %q", metricName)
	}
}

func TestRelabelExponentialHistogramDataPoints_Keep(t *testing.T) {
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
		makeExponentialHistogramMetric("kept_metric", makeAttrs("env", "production")),
		makeExponentialHistogramMetric("dropped_metric", makeAttrs("env", "staging")),
	)

	result := r.Relabel(rms)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}

	metrics := result[0].ScopeMetrics[0].Metrics
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	if metrics[0].Name != "kept_metric" {
		t.Errorf("expected kept_metric, got %s", metrics[0].Name)
	}
}

func TestRelabelSummaryDataPoints_Keep(t *testing.T) {
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
		makeSummaryMetric("kept_summary", makeAttrs("env", "production")),
		makeSummaryMetric("dropped_summary", makeAttrs("env", "staging")),
	)

	result := r.Relabel(rms)
	if len(result) != 1 {
		t.Fatalf("expected 1 result, got %d", len(result))
	}

	metrics := result[0].ScopeMetrics[0].Metrics
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	if metrics[0].Name != "kept_summary" {
		t.Errorf("expected kept_summary, got %s", metrics[0].Name)
	}
}

// --- Mixed metric types to ensure Summary and ExpHist go through full pipeline ---

func TestRelabelMixedMetricTypes_IncludingSummaryAndExpHist(t *testing.T) {
	configs := []Config{
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
		makeGaugeMetric("gauge_metric", makeAttrs("env", "prod", "debug_id", "x")),
		makeSummaryMetric("summary_metric", makeAttrs("env", "prod", "debug_id", "y")),
		makeExponentialHistogramMetric("exphist_metric", makeAttrs("env", "prod", "debug_id", "z")),
	)

	result := r.Relabel(rms)
	if len(result) != 1 {
		t.Fatalf("expected 1 ResourceMetrics, got %d", len(result))
	}

	metrics := result[0].ScopeMetrics[0].Metrics
	if len(metrics) != 3 {
		t.Fatalf("expected 3 metrics, got %d", len(metrics))
	}

	// Check Summary metric
	summaryDP := metrics[1].Data.(*metricspb.Metric_Summary).Summary.DataPoints[0]
	if getAttrValue(summaryDP.Attributes, "environment") != "prod" {
		t.Error("expected environment=prod on summary")
	}
	if getAttrValue(summaryDP.Attributes, "debug_id") != "" {
		t.Error("debug_id should have been dropped from summary")
	}

	// Check ExponentialHistogram metric
	expHistDP := metrics[2].Data.(*metricspb.Metric_ExponentialHistogram).ExponentialHistogram.DataPoints[0]
	if getAttrValue(expHistDP.Attributes, "environment") != "prod" {
		t.Error("expected environment=prod on exp histogram")
	}
	if getAttrValue(expHistDP.Attributes, "debug_id") != "" {
		t.Error("debug_id should have been dropped from exp histogram")
	}
}
