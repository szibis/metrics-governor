package tenant

import (
	"os"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// ---------------------------------------------------------------------------
// Pipeline.ReloadDetector
// ---------------------------------------------------------------------------

func TestPipeline_ReloadDetector_Success(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeHeader
	d, _ := NewDetector(cfg)
	p := NewPipeline(d, nil)

	newCfg := DefaultConfig()
	newCfg.Enabled = true
	newCfg.Mode = ModeAttribute
	newCfg.AttributeKey = "env"

	if err := p.ReloadDetector(newCfg); err != nil {
		t.Fatalf("ReloadDetector should succeed: %v", err)
	}

	// After reload, attribute mode should work
	rm := makeGaugeRM(map[string]string{"env": "staging"}, "cpu", nil)
	tenant := d.Detect(rm, "ignored")
	if tenant != "staging" {
		t.Fatalf("expected staging after reload, got %s", tenant)
	}
}

func TestPipeline_ReloadDetector_Invalid(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	d, _ := NewDetector(cfg)
	p := NewPipeline(d, nil)

	badCfg := Config{Enabled: true, Mode: "bogus"}
	if err := p.ReloadDetector(badCfg); err == nil {
		t.Fatal("ReloadDetector with invalid config should fail")
	}

	// Original config should still work
	rm := makeGaugeRM(nil, "cpu", nil)
	tenant := d.Detect(rm, "original-header")
	if tenant != "original-header" {
		t.Fatalf("original config should still work, got %s", tenant)
	}
}

// ---------------------------------------------------------------------------
// Pipeline.ProcessBatch: nil detector
// ---------------------------------------------------------------------------

func TestPipeline_NilDetector(t *testing.T) {
	p := NewPipeline(nil, nil)
	rms := []*metricspb.ResourceMetrics{makeGaugeRM(nil, "cpu", nil)}
	result := p.ProcessBatch(rms, "")
	if len(result) != 1 {
		t.Fatal("nil detector should pass through")
	}
}

// ---------------------------------------------------------------------------
// LoadQuotasConfig from file (error path)
// ---------------------------------------------------------------------------

func TestLoadQuotasConfig_FileNotFound(t *testing.T) {
	_, err := LoadQuotasConfig("/nonexistent/path/to/file.yaml")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestLoadQuotasConfig_ValidFile(t *testing.T) {
	// Write a temp file to test the file reading path
	tmpFile := "/tmp/test_quotas_coverage.yaml"
	content := `
global:
  max_datapoints: 5000
  action: drop
  window: 1m
default:
  max_datapoints: 1000
  action: adaptive
  window: 1m
`
	if err := writeTestFile(tmpFile, content); err != nil {
		t.Skipf("cannot write temp file: %v", err)
	}

	cfg, err := LoadQuotasConfig(tmpFile)
	if err != nil {
		t.Fatalf("LoadQuotasConfig failed: %v", err)
	}
	if cfg.Global.MaxDatapoints != 5000 {
		t.Fatalf("expected 5000, got %d", cfg.Global.MaxDatapoints)
	}
}

// writeTestFile is a helper to create temp YAML files
func writeTestFile(path, content string) error {
	return writeFile(path, []byte(content))
}

// ---------------------------------------------------------------------------
// ParseQuotasConfig: invalid YAML
// ---------------------------------------------------------------------------

func TestParseQuotasConfig_InvalidYAML(t *testing.T) {
	_, err := ParseQuotasConfig([]byte("{{invalid yaml"))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

// ---------------------------------------------------------------------------
// ParseQuotasConfig: tenant with invalid action
// ---------------------------------------------------------------------------

func TestParseQuotasConfig_TenantInvalidAction(t *testing.T) {
	yaml := `
tenants:
  bad-tenant:
    max_datapoints: 100
    action: kaboom
`
	_, err := ParseQuotasConfig([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for invalid tenant action")
	}
}

// ---------------------------------------------------------------------------
// ParseQuotasConfig: default with invalid window
// ---------------------------------------------------------------------------

func TestParseQuotasConfig_DefaultInvalidWindow(t *testing.T) {
	yaml := `
default:
  max_datapoints: 100
  window: "not-a-duration"
`
	_, err := ParseQuotasConfig([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for invalid default window")
	}
}

// ---------------------------------------------------------------------------
// ParseQuotasConfig: global with invalid action
// ---------------------------------------------------------------------------

func TestParseQuotasConfig_GlobalInvalidAction(t *testing.T) {
	yaml := `
global:
  max_datapoints: 1000
  action: invalid_action_xyz
`
	_, err := ParseQuotasConfig([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for invalid global action")
	}
}

// ---------------------------------------------------------------------------
// ParseQuotasConfig: default with invalid action
// ---------------------------------------------------------------------------

func TestParseQuotasConfig_DefaultInvalidAction(t *testing.T) {
	yaml := `
default:
  max_datapoints: 100
  action: nope
`
	_, err := ParseQuotasConfig([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for invalid default action")
	}
}

// ---------------------------------------------------------------------------
// QuotaEnforcer.Process: tenant log action exceeds limit
// ---------------------------------------------------------------------------

func TestQuotaEnforcer_TenantLog(t *testing.T) {
	cfg := &QuotasConfig{
		Default: &TenantQuota{
			MaxDatapoints: 5,
			Action:        QuotaActionLog,
			Window:        "1m",
			parsedWindow:  time.Minute,
		},
	}
	qe := NewQuotaEnforcer(cfg)

	// Exceed tenant limit with log action -> should pass through
	rms := []*metricspb.ResourceMetrics{makeTestRM("cpu", 100, nil)}
	result := qe.Process("t1", rms)
	if len(result) != 1 {
		t.Fatal("tenant action=log should pass through even when exceeded")
	}
}

// ---------------------------------------------------------------------------
// QuotaEnforcer.Process: tenant cardinality limit
// ---------------------------------------------------------------------------

func TestQuotaEnforcer_TenantCardinalityLimit(t *testing.T) {
	cfg := &QuotasConfig{
		Default: &TenantQuota{
			MaxCardinality: 2,
			Action:         QuotaActionDrop,
			Window:         "1m",
			parsedWindow:   time.Minute,
		},
	}
	qe := NewQuotaEnforcer(cfg)

	// First: 2 unique series - should pass
	rms := []*metricspb.ResourceMetrics{
		makeTestRM("cpu", 1, map[string]string{"host": "a"}),
		makeTestRM("cpu", 1, map[string]string{"host": "b"}),
	}
	result := qe.Process("t1", rms)
	if len(result) != 2 {
		t.Fatal("first 2 series should pass")
	}

	// Third unique series: exceeds cardinality limit
	rms = []*metricspb.ResourceMetrics{
		makeTestRM("cpu", 1, map[string]string{"host": "c"}),
	}
	result = qe.Process("t1", rms)
	if result != nil {
		t.Fatal("third series should be dropped (tenant cardinality limit)")
	}
}

// ---------------------------------------------------------------------------
// QuotaEnforcer.Process: adaptive filter with all remaining used up
// ---------------------------------------------------------------------------

func TestQuotaEnforcer_AdaptiveFilterExhausted(t *testing.T) {
	cfg := &QuotasConfig{
		Default: &TenantQuota{
			MaxDatapoints: 5,
			Action:        QuotaActionAdaptive,
			Window:        "1m",
			parsedWindow:  time.Minute,
		},
	}
	qe := NewQuotaEnforcer(cfg)

	// Use up all quota
	rms := []*metricspb.ResourceMetrics{makeTestRM("cpu", 5, nil)}
	qe.Process("t1", rms)

	// Now send more - remaining is 0, adaptive should return nil
	rms = []*metricspb.ResourceMetrics{makeTestRM("cpu", 5, nil)}
	result := qe.Process("t1", rms)
	if result != nil {
		t.Fatal("adaptive with 0 remaining should drop all")
	}
}

// ---------------------------------------------------------------------------
// QuotaEnforcer.Process: global with zero parsedWindow
// ---------------------------------------------------------------------------

func TestQuotaEnforcer_GlobalZeroParsedWindow(t *testing.T) {
	cfg := &QuotasConfig{
		Global: &GlobalQuota{
			MaxDatapoints: 100,
			Action:        QuotaActionDrop,
			Window:        "1m",
			parsedWindow:  0, // zero -> should default to 1 minute internally
		},
	}
	qe := NewQuotaEnforcer(cfg)

	rms := []*metricspb.ResourceMetrics{makeTestRM("cpu", 5, nil)}
	result := qe.Process("t1", rms)
	if len(result) != 1 {
		t.Fatal("should pass with zero parsedWindow (defaults to 1m)")
	}
}

// ---------------------------------------------------------------------------
// QuotaEnforcer.Process: tenant with zero parsedWindow
// ---------------------------------------------------------------------------

func TestQuotaEnforcer_TenantZeroParsedWindow(t *testing.T) {
	cfg := &QuotasConfig{
		Default: &TenantQuota{
			MaxDatapoints: 100,
			Action:        QuotaActionDrop,
			Window:        "1m",
			parsedWindow:  0, // zero -> should default to 1 minute
		},
	}
	qe := NewQuotaEnforcer(cfg)

	rms := []*metricspb.ResourceMetrics{makeTestRM("cpu", 5, nil)}
	result := qe.Process("t1", rms)
	if len(result) != 1 {
		t.Fatal("should pass with zero parsedWindow (defaults to 1m)")
	}
}

// ---------------------------------------------------------------------------
// QuotaEnforcer.Process: global adaptive with negative priority
// ---------------------------------------------------------------------------

func TestQuotaEnforcer_GlobalAdaptive_NegativePriority(t *testing.T) {
	cfg := &QuotasConfig{
		Global: &GlobalQuota{
			MaxDatapoints: 5,
			Action:        QuotaActionAdaptive,
			Window:        "1m",
			parsedWindow:  time.Minute,
		},
		Tenants: map[string]*TenantQuota{
			"neg-priority": {
				MaxDatapoints: 1000,
				Priority:      -1, // negative priority -> should be dropped
				Window:        "1m",
				parsedWindow:  time.Minute,
			},
		},
	}
	qe := NewQuotaEnforcer(cfg)

	rms := []*metricspb.ResourceMetrics{makeTestRM("cpu", 100, nil)}
	result := qe.Process("neg-priority", rms)
	if result != nil {
		t.Fatal("negative priority tenant should be dropped by global adaptive")
	}
}

// ---------------------------------------------------------------------------
// QuotaEnforcer.Process: global adaptive with empty action (defaults to drop)
// ---------------------------------------------------------------------------

func TestQuotaEnforcer_GlobalEmptyAction(t *testing.T) {
	cfg := &QuotasConfig{
		Global: &GlobalQuota{
			MaxDatapoints: 5,
			Action:        "", // empty -> defaults to drop
			Window:        "1m",
			parsedWindow:  time.Minute,
		},
	}
	qe := NewQuotaEnforcer(cfg)

	rms := []*metricspb.ResourceMetrics{makeTestRM("cpu", 100, nil)}
	result := qe.Process("t1", rms)
	if result != nil {
		t.Fatal("empty global action should default to drop")
	}
}

// ---------------------------------------------------------------------------
// QuotaEnforcer.Process: tenant empty action (defaults to adaptive)
// ---------------------------------------------------------------------------

func TestQuotaEnforcer_TenantEmptyAction(t *testing.T) {
	cfg := &QuotasConfig{
		Default: &TenantQuota{
			MaxDatapoints: 5,
			Action:        "", // empty -> defaults to adaptive
			Window:        "1m",
			parsedWindow:  time.Minute,
		},
	}
	qe := NewQuotaEnforcer(cfg)

	// Under limit -> passes
	rms := []*metricspb.ResourceMetrics{makeTestRM("cpu", 3, nil)}
	result := qe.Process("t1", rms)
	if len(result) != 1 {
		t.Fatal("under limit should pass")
	}

	// Over limit with empty action (adaptive) -> partial data
	rms = []*metricspb.ResourceMetrics{
		makeTestRM("cpu", 1, nil),
		makeTestRM("mem", 10, nil), // exceeds remaining
	}
	result = qe.Process("t1", rms)
	// Adaptive should keep what fits: 3 + 1 = 4 < 5, so first RM passes; second (10) exceeds 5-4=1
	if len(result) != 1 {
		t.Fatalf("adaptive should keep 1 RM, got %d", len(result))
	}
}

// ---------------------------------------------------------------------------
// countMetricDatapointsAndKeys: all metric types
// ---------------------------------------------------------------------------

func TestCountMetricDatapointsAndKeys_AllTypes(t *testing.T) {
	// Gauge
	gauge := &metricspb.Metric{
		Name: "g",
		Data: &metricspb.Metric_Gauge{
			Gauge: &metricspb.Gauge{
				DataPoints: []*metricspb.NumberDataPoint{
					{Attributes: []*commonpb.KeyValue{makeStringKV("a", "1")}},
				},
			},
		},
	}
	cnt, keys := countMetricDatapointsAndKeys(gauge)
	if cnt != 1 || len(keys) != 1 {
		t.Errorf("gauge: expected 1 dp and 1 key, got %d/%d", cnt, len(keys))
	}

	// Sum
	sum := &metricspb.Metric{
		Name: "s",
		Data: &metricspb.Metric_Sum{
			Sum: &metricspb.Sum{
				DataPoints: []*metricspb.NumberDataPoint{
					{Attributes: []*commonpb.KeyValue{makeStringKV("b", "2")}},
					{Attributes: []*commonpb.KeyValue{makeStringKV("b", "3")}},
				},
			},
		},
	}
	cnt, keys = countMetricDatapointsAndKeys(sum)
	if cnt != 2 || len(keys) != 2 {
		t.Errorf("sum: expected 2 dp and 2 keys, got %d/%d", cnt, len(keys))
	}

	// Histogram
	hist := &metricspb.Metric{
		Name: "h",
		Data: &metricspb.Metric_Histogram{
			Histogram: &metricspb.Histogram{
				DataPoints: []*metricspb.HistogramDataPoint{
					{Attributes: []*commonpb.KeyValue{makeStringKV("c", "4")}},
				},
			},
		},
	}
	cnt, keys = countMetricDatapointsAndKeys(hist)
	if cnt != 1 || len(keys) != 1 {
		t.Errorf("histogram: expected 1 dp and 1 key, got %d/%d", cnt, len(keys))
	}

	// Summary
	summary := &metricspb.Metric{
		Name: "sm",
		Data: &metricspb.Metric_Summary{
			Summary: &metricspb.Summary{
				DataPoints: []*metricspb.SummaryDataPoint{
					{Attributes: []*commonpb.KeyValue{makeStringKV("d", "5")}},
				},
			},
		},
	}
	cnt, keys = countMetricDatapointsAndKeys(summary)
	if cnt != 1 || len(keys) != 1 {
		t.Errorf("summary: expected 1 dp and 1 key, got %d/%d", cnt, len(keys))
	}

	// ExponentialHistogram
	expHist := &metricspb.Metric{
		Name: "eh",
		Data: &metricspb.Metric_ExponentialHistogram{
			ExponentialHistogram: &metricspb.ExponentialHistogram{
				DataPoints: []*metricspb.ExponentialHistogramDataPoint{
					{Attributes: []*commonpb.KeyValue{makeStringKV("e", "6")}},
				},
			},
		},
	}
	cnt, keys = countMetricDatapointsAndKeys(expHist)
	if cnt != 1 || len(keys) != 1 {
		t.Errorf("exp histogram: expected 1 dp and 1 key, got %d/%d", cnt, len(keys))
	}

	// Unknown / nil data
	nodata := &metricspb.Metric{Name: "nil", Data: nil}
	cnt, keys = countMetricDatapointsAndKeys(nodata)
	if cnt != 0 || keys != nil {
		t.Errorf("nil data: expected 0/nil, got %d/%v", cnt, keys)
	}
}

// ---------------------------------------------------------------------------
// buildSeriesKey helpers: hist, summary, expHist (they all delegate to buildSeriesKey)
// ---------------------------------------------------------------------------

func TestBuildHistSeriesKey(t *testing.T) {
	key := buildHistSeriesKey("latency", []*commonpb.KeyValue{makeStringKV("le", "100")})
	if key != "latency{le=100}" {
		t.Fatalf("expected 'latency{le=100}', got %q", key)
	}
}

func TestBuildSummarySeriesKey(t *testing.T) {
	key := buildSummarySeriesKey("duration", []*commonpb.KeyValue{makeStringKV("quantile", "0.99")})
	if key != "duration{quantile=0.99}" {
		t.Fatalf("expected 'duration{quantile=0.99}', got %q", key)
	}
}

func TestBuildExpHistSeriesKey(t *testing.T) {
	key := buildExpHistSeriesKey("size", []*commonpb.KeyValue{makeStringKV("bucket", "5")})
	if key != "size{bucket=5}" {
		t.Fatalf("expected 'size{bucket=5}', got %q", key)
	}
}

// ---------------------------------------------------------------------------
// getStringValue: default branch (non-string/int/double/bool value)
// ---------------------------------------------------------------------------

func TestGetStringValue_Default(t *testing.T) {
	// Use ArrayValue which is not handled by any specific case
	v := &commonpb.AnyValue{Value: &commonpb.AnyValue_ArrayValue{
		ArrayValue: &commonpb.ArrayValue{},
	}}
	result := getStringValue(v)
	if result != "" {
		t.Fatalf("expected empty string for ArrayValue, got %q", result)
	}
}

// ---------------------------------------------------------------------------
// findLabelValue: nil ResourceMetrics
// ---------------------------------------------------------------------------

func TestFindLabelValue_NilRM(t *testing.T) {
	result := findLabelValue(nil, "tenant")
	if result != "" {
		t.Fatalf("expected empty for nil RM, got %q", result)
	}
}

// ---------------------------------------------------------------------------
// findLabelInMetric: all metric types
// ---------------------------------------------------------------------------

func TestFindLabelInMetric_AllTypes(t *testing.T) {
	tests := []struct {
		name   string
		metric *metricspb.Metric
	}{
		{
			"gauge",
			&metricspb.Metric{
				Data: &metricspb.Metric_Gauge{
					Gauge: &metricspb.Gauge{
						DataPoints: []*metricspb.NumberDataPoint{
							{Attributes: []*commonpb.KeyValue{makeStringKV("tenant", "t1")}},
						},
					},
				},
			},
		},
		{
			"sum",
			&metricspb.Metric{
				Data: &metricspb.Metric_Sum{
					Sum: &metricspb.Sum{
						DataPoints: []*metricspb.NumberDataPoint{
							{Attributes: []*commonpb.KeyValue{makeStringKV("tenant", "t1")}},
						},
					},
				},
			},
		},
		{
			"histogram",
			&metricspb.Metric{
				Data: &metricspb.Metric_Histogram{
					Histogram: &metricspb.Histogram{
						DataPoints: []*metricspb.HistogramDataPoint{
							{Attributes: []*commonpb.KeyValue{makeStringKV("tenant", "t1")}},
						},
					},
				},
			},
		},
		{
			"summary",
			&metricspb.Metric{
				Data: &metricspb.Metric_Summary{
					Summary: &metricspb.Summary{
						DataPoints: []*metricspb.SummaryDataPoint{
							{Attributes: []*commonpb.KeyValue{makeStringKV("tenant", "t1")}},
						},
					},
				},
			},
		},
		{
			"exponential_histogram",
			&metricspb.Metric{
				Data: &metricspb.Metric_ExponentialHistogram{
					ExponentialHistogram: &metricspb.ExponentialHistogram{
						DataPoints: []*metricspb.ExponentialHistogramDataPoint{
							{Attributes: []*commonpb.KeyValue{makeStringKV("tenant", "t1")}},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findLabelInMetric(tt.metric, "tenant")
			if result != "t1" {
				t.Fatalf("expected t1, got %q", result)
			}
		})
	}

	// nil/no data
	t.Run("nil_data", func(t *testing.T) {
		m := &metricspb.Metric{Data: nil}
		if v := findLabelInMetric(m, "tenant"); v != "" {
			t.Fatalf("expected empty for nil data, got %q", v)
		}
	})
}

// ---------------------------------------------------------------------------
// injectLabelInMetric: all metric types
// ---------------------------------------------------------------------------

func TestInjectLabelInMetric_AllTypes(t *testing.T) {
	kv := makeStringKV("__tenant__", "team-x")

	tests := []struct {
		name   string
		metric *metricspb.Metric
		check  func(*metricspb.Metric) bool
	}{
		{
			"gauge",
			&metricspb.Metric{
				Data: &metricspb.Metric_Gauge{
					Gauge: &metricspb.Gauge{
						DataPoints: []*metricspb.NumberDataPoint{{}},
					},
				},
			},
			func(m *metricspb.Metric) bool {
				return hasAttr(m.GetGauge().DataPoints[0].Attributes, "__tenant__", "team-x")
			},
		},
		{
			"sum",
			&metricspb.Metric{
				Data: &metricspb.Metric_Sum{
					Sum: &metricspb.Sum{
						DataPoints: []*metricspb.NumberDataPoint{{}},
					},
				},
			},
			func(m *metricspb.Metric) bool {
				return hasAttr(m.GetSum().DataPoints[0].Attributes, "__tenant__", "team-x")
			},
		},
		{
			"histogram",
			&metricspb.Metric{
				Data: &metricspb.Metric_Histogram{
					Histogram: &metricspb.Histogram{
						DataPoints: []*metricspb.HistogramDataPoint{{}},
					},
				},
			},
			func(m *metricspb.Metric) bool {
				return hasAttr(m.GetHistogram().DataPoints[0].Attributes, "__tenant__", "team-x")
			},
		},
		{
			"summary",
			&metricspb.Metric{
				Data: &metricspb.Metric_Summary{
					Summary: &metricspb.Summary{
						DataPoints: []*metricspb.SummaryDataPoint{{}},
					},
				},
			},
			func(m *metricspb.Metric) bool {
				return hasAttr(m.GetSummary().DataPoints[0].Attributes, "__tenant__", "team-x")
			},
		},
		{
			"exponential_histogram",
			&metricspb.Metric{
				Data: &metricspb.Metric_ExponentialHistogram{
					ExponentialHistogram: &metricspb.ExponentialHistogram{
						DataPoints: []*metricspb.ExponentialHistogramDataPoint{{}},
					},
				},
			},
			func(m *metricspb.Metric) bool {
				return hasAttr(m.GetExponentialHistogram().DataPoints[0].Attributes, "__tenant__", "team-x")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			injectLabelInMetric(tt.metric, kv)
			if !tt.check(tt.metric) {
				t.Error("label not injected")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// stripLabelInMetric: all metric types
// ---------------------------------------------------------------------------

func TestStripLabelInMetric_AllTypes(t *testing.T) {
	tests := []struct {
		name   string
		metric *metricspb.Metric
		check  func(*metricspb.Metric) bool
	}{
		{
			"gauge",
			&metricspb.Metric{
				Data: &metricspb.Metric_Gauge{
					Gauge: &metricspb.Gauge{
						DataPoints: []*metricspb.NumberDataPoint{
							{Attributes: []*commonpb.KeyValue{makeStringKV("remove_me", "v")}},
						},
					},
				},
			},
			func(m *metricspb.Metric) bool {
				return !hasKey(m.GetGauge().DataPoints[0].Attributes, "remove_me")
			},
		},
		{
			"sum",
			&metricspb.Metric{
				Data: &metricspb.Metric_Sum{
					Sum: &metricspb.Sum{
						DataPoints: []*metricspb.NumberDataPoint{
							{Attributes: []*commonpb.KeyValue{makeStringKV("remove_me", "v")}},
						},
					},
				},
			},
			func(m *metricspb.Metric) bool {
				return !hasKey(m.GetSum().DataPoints[0].Attributes, "remove_me")
			},
		},
		{
			"histogram",
			&metricspb.Metric{
				Data: &metricspb.Metric_Histogram{
					Histogram: &metricspb.Histogram{
						DataPoints: []*metricspb.HistogramDataPoint{
							{Attributes: []*commonpb.KeyValue{makeStringKV("remove_me", "v")}},
						},
					},
				},
			},
			func(m *metricspb.Metric) bool {
				return !hasKey(m.GetHistogram().DataPoints[0].Attributes, "remove_me")
			},
		},
		{
			"summary",
			&metricspb.Metric{
				Data: &metricspb.Metric_Summary{
					Summary: &metricspb.Summary{
						DataPoints: []*metricspb.SummaryDataPoint{
							{Attributes: []*commonpb.KeyValue{makeStringKV("remove_me", "v")}},
						},
					},
				},
			},
			func(m *metricspb.Metric) bool {
				return !hasKey(m.GetSummary().DataPoints[0].Attributes, "remove_me")
			},
		},
		{
			"exponential_histogram",
			&metricspb.Metric{
				Data: &metricspb.Metric_ExponentialHistogram{
					ExponentialHistogram: &metricspb.ExponentialHistogram{
						DataPoints: []*metricspb.ExponentialHistogramDataPoint{
							{Attributes: []*commonpb.KeyValue{makeStringKV("remove_me", "v")}},
						},
					},
				},
			},
			func(m *metricspb.Metric) bool {
				return !hasKey(m.GetExponentialHistogram().DataPoints[0].Attributes, "remove_me")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stripLabelInMetric(tt.metric, "remove_me")
			if !tt.check(tt.metric) {
				t.Error("label not stripped")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// QuotaEnforcer.Process: global window reset
// ---------------------------------------------------------------------------

func TestQuotaEnforcer_GlobalWindowReset(t *testing.T) {
	cfg := &QuotasConfig{
		Global: &GlobalQuota{
			MaxDatapoints: 10,
			Action:        QuotaActionDrop,
			Window:        "1ms",
			parsedWindow:  time.Millisecond,
		},
	}
	qe := NewQuotaEnforcer(cfg)

	// Fill up global
	rms := []*metricspb.ResourceMetrics{makeTestRM("cpu", 8, nil)}
	qe.Process("t1", rms)

	// Wait for window expiry
	time.Sleep(2 * time.Millisecond)

	// After window reset, should pass again
	rms = []*metricspb.ResourceMetrics{makeTestRM("cpu", 8, nil)}
	result := qe.Process("t1", rms)
	if len(result) != 1 {
		t.Fatal("after global window reset, should pass")
	}
}

// ---------------------------------------------------------------------------
// QuotaEnforcer.Process: tenant window reset
// ---------------------------------------------------------------------------

func TestQuotaEnforcer_TenantWindowReset(t *testing.T) {
	cfg := &QuotasConfig{
		Default: &TenantQuota{
			MaxDatapoints: 10,
			Action:        QuotaActionDrop,
			Window:        "1ms",
			parsedWindow:  time.Millisecond,
		},
	}
	qe := NewQuotaEnforcer(cfg)

	// Fill up tenant quota
	rms := []*metricspb.ResourceMetrics{makeTestRM("cpu", 8, nil)}
	qe.Process("t1", rms)

	// Wait for window expiry
	time.Sleep(2 * time.Millisecond)

	// After window reset, should pass again
	rms = []*metricspb.ResourceMetrics{makeTestRM("cpu", 8, nil)}
	result := qe.Process("t1", rms)
	if len(result) != 1 {
		t.Fatal("after tenant window reset, should pass")
	}
}

// ---------------------------------------------------------------------------
// DetectAndAnnotate: strip mode on non-label mode (no-op)
// ---------------------------------------------------------------------------

func TestDetectAndAnnotate_StripNotAppliedOnHeaderMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeHeader
	cfg.StripSourceLabel = true // should only apply in label mode
	cfg.InjectLabel = false
	d, _ := NewDetector(cfg)

	rm := makeGaugeRM(nil, "cpu", map[string]string{"tenant": "keep-me"})
	result := d.DetectAndAnnotate([]*metricspb.ResourceMetrics{rm}, "team-a")

	// In header mode, strip should not be applied, so "tenant" label remains
	dp := result["team-a"][0].ScopeMetrics[0].Metrics[0].GetGauge().DataPoints[0]
	if !hasKey(dp.Attributes, "tenant") {
		t.Fatal("tenant label should NOT be stripped in header mode")
	}
}

// ---------------------------------------------------------------------------
// Detect: label mode with Summary metric
// ---------------------------------------------------------------------------

func TestDetect_LabelMode_Summary(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeLabel
	cfg.LabelName = "org"
	cfg.InjectLabel = false
	d, _ := NewDetector(cfg)

	rm := &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{},
		ScopeMetrics: []*metricspb.ScopeMetrics{{
			Metrics: []*metricspb.Metric{{
				Name: "latency",
				Data: &metricspb.Metric_Summary{
					Summary: &metricspb.Summary{
						DataPoints: []*metricspb.SummaryDataPoint{{
							Attributes: []*commonpb.KeyValue{makeStringKV("org", "support")},
						}},
					},
				},
			}},
		}},
	}
	tenant := d.Detect(rm, "")
	if tenant != "support" {
		t.Fatalf("expected support, got %s", tenant)
	}
}

// ---------------------------------------------------------------------------
// Detect: label mode with ExponentialHistogram
// ---------------------------------------------------------------------------

func TestDetect_LabelMode_ExponentialHistogram(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeLabel
	cfg.LabelName = "team"
	cfg.InjectLabel = false
	d, _ := NewDetector(cfg)

	rm := &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{},
		ScopeMetrics: []*metricspb.ScopeMetrics{{
			Metrics: []*metricspb.Metric{{
				Name: "request_size",
				Data: &metricspb.Metric_ExponentialHistogram{
					ExponentialHistogram: &metricspb.ExponentialHistogram{
						DataPoints: []*metricspb.ExponentialHistogramDataPoint{{
							Attributes: []*commonpb.KeyValue{makeStringKV("team", "platform")},
						}},
					},
				},
			}},
		}},
	}
	tenant := d.Detect(rm, "")
	if tenant != "platform" {
		t.Fatalf("expected platform, got %s", tenant)
	}
}

// ---------------------------------------------------------------------------
// adaptiveFilter: partial fit
// ---------------------------------------------------------------------------

func TestAdaptiveFilter_PartialFit(t *testing.T) {
	cfg := &QuotasConfig{
		Default: &TenantQuota{
			MaxDatapoints: 12,
			Action:        QuotaActionAdaptive,
			Window:        "1m",
			parsedWindow:  time.Minute,
		},
	}
	qe := NewQuotaEnforcer(cfg)

	// First: use 5 datapoints
	rms := []*metricspb.ResourceMetrics{makeTestRM("cpu", 5, nil)}
	qe.Process("t1", rms)

	// Now send 3 RMs: 3 + 5 + 10 datapoints
	// Remaining = 12 - 5 = 7
	// First (3) fits: 3 <= 7
	// Second (5) doesn't fit: 3+5 = 8 > 7
	rms = []*metricspb.ResourceMetrics{
		makeTestRM("cpu", 3, nil),
		makeTestRM("mem", 5, nil),
		makeTestRM("disk", 10, nil),
	}
	result := qe.Process("t1", rms)
	if len(result) != 1 {
		t.Fatalf("adaptive should keep 1 RM (3 dps fits in remaining 7), got %d", len(result))
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func hasAttr(attrs []*commonpb.KeyValue, key, value string) bool {
	for _, kv := range attrs {
		if kv.Key == key {
			if sv, ok := kv.Value.GetValue().(*commonpb.AnyValue_StringValue); ok {
				return sv.StringValue == value
			}
		}
	}
	return false
}

func hasKey(attrs []*commonpb.KeyValue, key string) bool {
	for _, kv := range attrs {
		if kv.Key == key {
			return true
		}
	}
	return false
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}
