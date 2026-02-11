package tenant

import (
	"testing"
	"time"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
)

// --- Config Parsing Tests ---

func TestParseQuotasConfig_Minimal(t *testing.T) {
	yaml := `
global:
  max_datapoints: 1000000
  action: drop
`
	cfg, err := ParseQuotasConfig([]byte(yaml))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if cfg.Global.MaxDatapoints != 1000000 {
		t.Fatalf("expected 1M, got %d", cfg.Global.MaxDatapoints)
	}
	if cfg.Global.Action != QuotaActionDrop {
		t.Fatalf("expected drop, got %s", cfg.Global.Action)
	}
}

func TestParseQuotasConfig_Full(t *testing.T) {
	yaml := `
global:
  max_datapoints: 1000000
  max_cardinality: 500000
  action: drop
  window: 1m
default:
  max_datapoints: 100000
  max_cardinality: 50000
  action: adaptive
  window: 1m
tenants:
  team-payments:
    max_datapoints: 500000
    max_cardinality: 200000
    action: log
    priority: 10
  team-debug:
    max_datapoints: 10000
    max_cardinality: 5000
    action: drop
    priority: 0
`
	cfg, err := ParseQuotasConfig([]byte(yaml))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if cfg.Default.MaxDatapoints != 100000 {
		t.Fatal("default max_datapoints wrong")
	}
	if len(cfg.Tenants) != 2 {
		t.Fatalf("expected 2 tenants, got %d", len(cfg.Tenants))
	}
	payments := cfg.Tenants["team-payments"]
	if payments.Priority != 10 {
		t.Fatalf("expected priority 10, got %d", payments.Priority)
	}
	if payments.Action != QuotaActionLog {
		t.Fatalf("expected log, got %s", payments.Action)
	}
}

func TestParseQuotasConfig_InvalidAction(t *testing.T) {
	yaml := `
global:
  max_datapoints: 1000
  action: explode
`
	_, err := ParseQuotasConfig([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for invalid action")
	}
}

func TestParseQuotasConfig_InvalidWindow(t *testing.T) {
	yaml := `
global:
  max_datapoints: 1000
  action: drop
  window: "invalid"
`
	_, err := ParseQuotasConfig([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for invalid window")
	}
}

func TestParseQuotasConfig_TenantInvalidWindow(t *testing.T) {
	yaml := `
tenants:
  bad:
    max_datapoints: 100
    window: "nope"
`
	_, err := ParseQuotasConfig([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for invalid tenant window")
	}
}

func TestParseQuotasConfig_Empty(t *testing.T) {
	cfg, err := ParseQuotasConfig([]byte("{}"))
	if err != nil {
		t.Fatalf("empty config should parse: %v", err)
	}
	if cfg.Global != nil {
		t.Fatal("global should be nil")
	}
}

// --- QuotaEnforcer Tests ---

func makeTestRM(name string, numDPs int, attrs map[string]string) *metricspb.ResourceMetrics {
	dpKVs := make([]*commonpb.KeyValue, 0, len(attrs))
	for k, v := range attrs {
		dpKVs = append(dpKVs, makeStringKV(k, v))
	}
	dps := make([]*metricspb.NumberDataPoint, numDPs)
	for i := range dps {
		dpCopy := make([]*commonpb.KeyValue, len(dpKVs))
		copy(dpCopy, dpKVs)
		dps[i] = &metricspb.NumberDataPoint{Attributes: dpCopy}
	}
	return &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{},
		ScopeMetrics: []*metricspb.ScopeMetrics{{
			Metrics: []*metricspb.Metric{{
				Name: name,
				Data: &metricspb.Metric_Gauge{
					Gauge: &metricspb.Gauge{DataPoints: dps},
				},
			}},
		}},
	}
}

func TestQuotaEnforcer_NoLimits(t *testing.T) {
	cfg := &QuotasConfig{}
	qe := NewQuotaEnforcer(cfg)

	rms := []*metricspb.ResourceMetrics{makeTestRM("cpu", 100, nil)}
	result := qe.Process("test", rms)
	if len(result) != 1 {
		t.Fatalf("expected 1 RM, got %d", len(result))
	}
}

func TestQuotaEnforcer_GlobalDrop(t *testing.T) {
	cfg := &QuotasConfig{
		Global: &GlobalQuota{
			MaxDatapoints: 10,
			Action:        QuotaActionDrop,
			Window:        "1m",
			parsedWindow:  time.Minute,
		},
	}
	qe := NewQuotaEnforcer(cfg)

	// First batch: 5 datapoints — passes
	rms := []*metricspb.ResourceMetrics{makeTestRM("cpu", 5, nil)}
	result := qe.Process("t1", rms)
	if len(result) != 1 {
		t.Fatal("first batch should pass")
	}

	// Second batch: 10 more — exceeds global limit
	rms = []*metricspb.ResourceMetrics{makeTestRM("cpu", 10, nil)}
	result = qe.Process("t1", rms)
	if result != nil {
		t.Fatal("second batch should be dropped (global limit)")
	}
}

func TestQuotaEnforcer_GlobalLog(t *testing.T) {
	cfg := &QuotasConfig{
		Global: &GlobalQuota{
			MaxDatapoints: 5,
			Action:        QuotaActionLog,
			Window:        "1m",
			parsedWindow:  time.Minute,
		},
	}
	qe := NewQuotaEnforcer(cfg)

	// Exceed limit — action=log means pass through
	rms := []*metricspb.ResourceMetrics{makeTestRM("cpu", 100, nil)}
	result := qe.Process("t1", rms)
	if len(result) != 1 {
		t.Fatal("action=log should pass through")
	}
}

func TestQuotaEnforcer_TenantDrop(t *testing.T) {
	cfg := &QuotasConfig{
		Default: &TenantQuota{
			MaxDatapoints: 10,
			Action:        QuotaActionDrop,
			Window:        "1m",
			parsedWindow:  time.Minute,
		},
	}
	qe := NewQuotaEnforcer(cfg)

	// Under limit
	rms := []*metricspb.ResourceMetrics{makeTestRM("cpu", 5, nil)}
	result := qe.Process("t1", rms)
	if len(result) != 1 {
		t.Fatal("under limit should pass")
	}

	// Exceeds tenant limit
	rms = []*metricspb.ResourceMetrics{makeTestRM("cpu", 10, nil)}
	result = qe.Process("t1", rms)
	if result != nil {
		t.Fatal("over limit should be dropped")
	}
}

func TestQuotaEnforcer_TenantOverride(t *testing.T) {
	cfg := &QuotasConfig{
		Default: &TenantQuota{
			MaxDatapoints: 10,
			Action:        QuotaActionDrop,
			Window:        "1m",
			parsedWindow:  time.Minute,
		},
		Tenants: map[string]*TenantQuota{
			"premium": {
				MaxDatapoints: 1000,
				Action:        QuotaActionLog,
				Window:        "1m",
				parsedWindow:  time.Minute,
				Priority:      10,
			},
		},
	}
	qe := NewQuotaEnforcer(cfg)

	// Default tenant: limited to 10
	rms := []*metricspb.ResourceMetrics{makeTestRM("cpu", 20, nil)}
	result := qe.Process("regular", rms)
	if result != nil {
		t.Fatal("regular tenant should be dropped")
	}

	// Premium tenant: limited to 1000 with log action
	rms = []*metricspb.ResourceMetrics{makeTestRM("cpu", 500, nil)}
	result = qe.Process("premium", rms)
	if len(result) != 1 {
		t.Fatal("premium tenant should pass")
	}
}

func TestQuotaEnforcer_AdaptiveFilter(t *testing.T) {
	cfg := &QuotasConfig{
		Default: &TenantQuota{
			MaxDatapoints: 15,
			Action:        QuotaActionAdaptive,
			Window:        "1m",
			parsedWindow:  time.Minute,
		},
	}
	qe := NewQuotaEnforcer(cfg)

	// Send two ResourceMetrics: 10 + 10 = 20, but limit is 15
	rms := []*metricspb.ResourceMetrics{
		makeTestRM("cpu", 10, nil),
		makeTestRM("mem", 10, nil),
	}
	result := qe.Process("t1", rms)
	// Adaptive should keep the first (10 <= 15) and drop the second (10+10 > 15)
	if len(result) != 1 {
		t.Fatalf("adaptive should keep 1 RM, got %d", len(result))
	}
}

func TestQuotaEnforcer_WindowReset(t *testing.T) {
	cfg := &QuotasConfig{
		Default: &TenantQuota{
			MaxDatapoints: 10,
			Action:        QuotaActionDrop,
			Window:        "1ms", // Very short window for testing
			parsedWindow:  time.Millisecond,
		},
	}
	qe := NewQuotaEnforcer(cfg)

	rms := []*metricspb.ResourceMetrics{makeTestRM("cpu", 8, nil)}
	result := qe.Process("t1", rms)
	if len(result) != 1 {
		t.Fatal("first batch should pass")
	}

	// Wait for window to expire
	time.Sleep(2 * time.Millisecond)

	// After window reset, limit resets
	rms = []*metricspb.ResourceMetrics{makeTestRM("cpu", 8, nil)}
	result = qe.Process("t1", rms)
	if len(result) != 1 {
		t.Fatal("after window reset, should pass again")
	}
}

func TestQuotaEnforcer_GlobalCardinalityLimit(t *testing.T) {
	cfg := &QuotasConfig{
		Global: &GlobalQuota{
			MaxCardinality: 2,
			Action:         QuotaActionDrop,
			Window:         "1m",
			parsedWindow:   time.Minute,
		},
	}
	qe := NewQuotaEnforcer(cfg)

	// First: 2 unique series
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
		t.Fatal("third series should be dropped (cardinality limit)")
	}
}

func TestQuotaEnforcer_ReloadConfig(t *testing.T) {
	cfg := &QuotasConfig{
		Default: &TenantQuota{
			MaxDatapoints: 10,
			Action:        QuotaActionDrop,
			Window:        "1m",
			parsedWindow:  time.Minute,
		},
	}
	qe := NewQuotaEnforcer(cfg)

	// Use up quota
	rms := []*metricspb.ResourceMetrics{makeTestRM("cpu", 8, nil)}
	qe.Process("t1", rms)

	// Reload with higher limit
	newCfg := &QuotasConfig{
		Default: &TenantQuota{
			MaxDatapoints: 100,
			Action:        QuotaActionDrop,
			Window:        "1m",
			parsedWindow:  time.Minute,
		},
	}
	qe.ReloadConfig(newCfg)

	// After reload, windows reset, so 20 should pass
	rms = []*metricspb.ResourceMetrics{makeTestRM("cpu", 20, nil)}
	result := qe.Process("t1", rms)
	if len(result) != 1 {
		t.Fatal("after reload, higher limit should pass")
	}
}

func TestQuotaEnforcer_GlobalAdaptive_HighPriority(t *testing.T) {
	cfg := &QuotasConfig{
		Global: &GlobalQuota{
			MaxDatapoints: 5,
			Action:        QuotaActionAdaptive,
			Window:        "1m",
			parsedWindow:  time.Minute,
		},
		Tenants: map[string]*TenantQuota{
			"vip": {
				MaxDatapoints: 1000,
				Priority:      10,
				Window:        "1m",
				parsedWindow:  time.Minute,
			},
			"low": {
				MaxDatapoints: 1000,
				Priority:      0,
				Window:        "1m",
				parsedWindow:  time.Minute,
			},
		},
	}
	qe := NewQuotaEnforcer(cfg)

	// Exceed global limit
	rms := []*metricspb.ResourceMetrics{makeTestRM("cpu", 100, nil)}

	// VIP tenant: high priority, passes despite global exceed
	result := qe.Process("vip", rms)
	if len(result) != 1 {
		t.Fatal("VIP should pass global adaptive")
	}

	// Low-priority tenant: dropped by global adaptive
	rms = []*metricspb.ResourceMetrics{makeTestRM("cpu", 100, nil)}
	result = qe.Process("low", rms)
	if result != nil {
		t.Fatal("low-priority should be dropped by global adaptive")
	}
}

// --- Helper Function Tests ---

func TestCountDatapointsAndSeries(t *testing.T) {
	rms := []*metricspb.ResourceMetrics{
		makeTestRM("cpu", 3, map[string]string{"host": "a"}),
		makeTestRM("mem", 2, map[string]string{"host": "b"}),
	}
	count, keys := countDatapointsAndSeries(rms)
	if count != 5 {
		t.Fatalf("expected 5 datapoints, got %d", count)
	}
	if len(keys) != 5 {
		t.Fatalf("expected 5 keys, got %d", len(keys))
	}
}

func TestBuildSeriesKey(t *testing.T) {
	key := buildSeriesKey("cpu", []*commonpb.KeyValue{
		makeStringKV("host", "a"),
		makeStringKV("env", "prod"),
	})
	expected := "cpu{host=a,env=prod}"
	if key != expected {
		t.Fatalf("expected %q, got %q", expected, key)
	}
}

func TestBuildSeriesKey_NoAttrs(t *testing.T) {
	key := buildSeriesKey("cpu", nil)
	if key != "cpu" {
		t.Fatalf("expected 'cpu', got %q", key)
	}
}

func TestCountNewSeries(t *testing.T) {
	existing := map[string]struct{}{
		"cpu{host=a}": {},
		"cpu{host=b}": {},
	}
	keys := []string{"cpu{host=a}", "cpu{host=c}", "cpu{host=d}"}
	count := countNewSeries(existing, keys)
	if count != 2 {
		t.Fatalf("expected 2 new, got %d", count)
	}
}

func TestGetStringValue(t *testing.T) {
	tests := []struct {
		name string
		val  *commonpb.AnyValue
		want string
	}{
		{"string", &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "hi"}}, "hi"},
		{"int", &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 42}}, "42"},
		{"double", &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: 3.14}}, "3.14"},
		{"bool", &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: true}}, "true"},
		{"nil", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getStringValue(tt.val)
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestCountMetricDatapoints_AllTypes(t *testing.T) {
	// Gauge
	gauge := &metricspb.Metric{
		Name: "g", Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{
			DataPoints: []*metricspb.NumberDataPoint{{}, {}},
		}},
	}
	if countMetricDatapoints(gauge) != 2 {
		t.Fatal("gauge count wrong")
	}

	// Sum
	sum := &metricspb.Metric{
		Name: "s", Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{
			DataPoints: []*metricspb.NumberDataPoint{{}, {}, {}},
		}},
	}
	if countMetricDatapoints(sum) != 3 {
		t.Fatal("sum count wrong")
	}

	// Histogram
	hist := &metricspb.Metric{
		Name: "h", Data: &metricspb.Metric_Histogram{Histogram: &metricspb.Histogram{
			DataPoints: []*metricspb.HistogramDataPoint{{}},
		}},
	}
	if countMetricDatapoints(hist) != 1 {
		t.Fatal("histogram count wrong")
	}

	// Summary
	summary := &metricspb.Metric{
		Name: "sum", Data: &metricspb.Metric_Summary{Summary: &metricspb.Summary{
			DataPoints: []*metricspb.SummaryDataPoint{{}, {}, {}, {}},
		}},
	}
	if countMetricDatapoints(summary) != 4 {
		t.Fatal("summary count wrong")
	}

	// ExponentialHistogram
	expHist := &metricspb.Metric{
		Name: "e", Data: &metricspb.Metric_ExponentialHistogram{ExponentialHistogram: &metricspb.ExponentialHistogram{
			DataPoints: []*metricspb.ExponentialHistogramDataPoint{{}, {}},
		}},
	}
	if countMetricDatapoints(expHist) != 2 {
		t.Fatal("exponential histogram count wrong")
	}
}

// --- Race Condition Tests ---

func TestRace_QuotaEnforcerConcurrent(t *testing.T) {
	cfg := &QuotasConfig{
		Global: &GlobalQuota{
			MaxDatapoints: 100000,
			Action:        QuotaActionAdaptive,
			Window:        "1m",
			parsedWindow:  time.Minute,
		},
		Default: &TenantQuota{
			MaxDatapoints: 10000,
			Action:        QuotaActionAdaptive,
			Window:        "1m",
			parsedWindow:  time.Minute,
		},
	}
	qe := NewQuotaEnforcer(cfg)

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func(tenantNum int) {
			defer func() { done <- struct{}{} }()
			tid := "tenant-" + string(rune('a'+tenantNum))
			for j := 0; j < 100; j++ {
				rms := []*metricspb.ResourceMetrics{makeTestRM("cpu", 5, nil)}
				qe.Process(tid, rms)
			}
		}(i)
	}
	// Concurrent reload
	go func() {
		defer func() { done <- struct{}{} }()
		for j := 0; j < 10; j++ {
			newCfg := &QuotasConfig{
				Default: &TenantQuota{
					MaxDatapoints: 50000,
					Action:        QuotaActionAdaptive,
					Window:        "1m",
					parsedWindow:  time.Minute,
				},
			}
			qe.ReloadConfig(newCfg)
		}
	}()
	for i := 0; i < 11; i++ {
		<-done
	}
}

// --- Benchmarks ---

func BenchmarkQuotaEnforcer_Process(b *testing.B) {
	cfg := &QuotasConfig{
		Global: &GlobalQuota{
			MaxDatapoints:  100000000,
			MaxCardinality: 10000000,
			Action:         QuotaActionDrop,
			Window:         "1m",
			parsedWindow:   time.Minute,
		},
		Default: &TenantQuota{
			MaxDatapoints:  10000000,
			MaxCardinality: 1000000,
			Action:         QuotaActionAdaptive,
			Window:         "1m",
			parsedWindow:   time.Minute,
		},
	}
	qe := NewQuotaEnforcer(cfg)
	rms := []*metricspb.ResourceMetrics{makeTestRM("cpu", 10, map[string]string{"host": "a"})}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		qe.Process("tenant-1", rms)
	}
}

func TestNewQuotaEnforcer_InitialReloadTimestamp(t *testing.T) {
	before := time.Now().UTC().Unix()
	qe := NewQuotaEnforcer(&QuotasConfig{
		Global: &GlobalQuota{MaxDatapoints: 1000, Action: QuotaActionDrop},
	})
	after := time.Now().UTC().Unix()

	ts := qe.lastReloadUTC.Load()
	if ts == 0 {
		t.Fatal("lastReloadUTC should not be 0 (Unix epoch)")
	}
	if ts < before || ts > after {
		t.Errorf("lastReloadUTC %d not between %d and %d", ts, before, after)
	}
}

func TestQuotaEnforcer_ReloadConfig_UpdatesTimestamp(t *testing.T) {
	cfg := &QuotasConfig{
		Global: &GlobalQuota{MaxDatapoints: 1000, Action: QuotaActionDrop},
	}
	qe := NewQuotaEnforcer(cfg)

	initialTS := qe.lastReloadUTC.Load()
	time.Sleep(1100 * time.Millisecond)

	newCfg := &QuotasConfig{
		Global: &GlobalQuota{MaxDatapoints: 2000, Action: QuotaActionDrop},
	}
	qe.ReloadConfig(newCfg)

	reloadedTS := qe.lastReloadUTC.Load()
	if reloadedTS <= initialTS {
		t.Errorf("lastReloadUTC should advance after reload: initial=%d, reloaded=%d", initialTS, reloadedTS)
	}
}
