package sampling

import (
	"context"
	"sync"
	"testing"
	"time"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
)

func makeNumberDP(attrs map[string]string, ts uint64, value float64) *metricspb.NumberDataPoint {
	var kvs []*commonpb.KeyValue
	for k, v := range attrs {
		kvs = append(kvs, &commonpb.KeyValue{
			Key:   k,
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}},
		})
	}
	return &metricspb.NumberDataPoint{
		Attributes:   kvs,
		TimeUnixNano: ts,
		Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: value},
	}
}

func TestAggregateEngine_BasicGroupBy(t *testing.T) {
	rules := []ProcessingRule{
		{
			Name:           "test-agg",
			Input:          "http_requests_total",
			Action:         ActionAggregate,
			Interval:       "1m",
			GroupBy:        []string{"service"},
			Functions:      []string{"sum", "count"},
			parsedInterval: time.Minute,
			parsedFunctions: []parsedAggFunc{
				{Func: AggSum},
				{Func: AggCount},
			},
		},
	}

	engine := newAggregateEngine(rules, 10*time.Minute)

	var mu sync.Mutex
	var output []*metricspb.ResourceMetrics
	engine.SetOutput(func(rms []*metricspb.ResourceMetrics) {
		mu.Lock()
		output = append(output, rms...)
		mu.Unlock()
	})

	// Ingest datapoints from different pods but same service.
	dp1 := makeNumberDP(map[string]string{"service": "web", "pod": "web-1"}, 1000, 10)
	dp2 := makeNumberDP(map[string]string{"service": "web", "pod": "web-2"}, 1000, 20)
	dp3 := makeNumberDP(map[string]string{"service": "api", "pod": "api-1"}, 1000, 5)

	engine.Ingest(&rules[0], "http_requests_total", dp1)
	engine.Ingest(&rules[0], "http_requests_total", dp2)
	engine.Ingest(&rules[0], "http_requests_total", dp3)

	// Verify groups were created.
	if groups := engine.ActiveGroups(); groups != 2 {
		t.Errorf("active groups = %d, want 2 (web, api)", groups)
	}

	// Manually flush.
	for _, rs := range engine.rules {
		engine.flushRule(rs)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(output) == 0 {
		t.Fatal("no output produced after flush")
	}

	// Should have metrics for both groups (web and api), 2 functions each.
	rm := output[0]
	if rm.ScopeMetrics == nil || len(rm.ScopeMetrics) == 0 {
		t.Fatal("no scope metrics in output")
	}

	metrics := rm.ScopeMetrics[0].Metrics
	// 2 groups × 2 functions = 4 metrics (with _sum and _count suffixes).
	if len(metrics) < 4 {
		t.Errorf("expected at least 4 metrics (2 groups × 2 functions), got %d", len(metrics))
	}
}

func TestAggregateEngine_DropLabels(t *testing.T) {
	rules := []ProcessingRule{
		{
			Name:           "strip-pod",
			Input:          "kube_.*",
			Action:         ActionAggregate,
			Interval:       "30s",
			DropLabels:     []string{"pod", "pod_ip"},
			Functions:      []string{"last"},
			parsedInterval: 30 * time.Second,
			parsedFunctions: []parsedAggFunc{
				{Func: AggLast},
			},
		},
	}

	engine := newAggregateEngine(rules, 10*time.Minute)

	dp := makeNumberDP(map[string]string{
		"service": "web",
		"pod":     "web-abc123",
		"pod_ip":  "10.0.0.1",
		"env":     "prod",
	}, 1000, 42)

	engine.Ingest(&rules[0], "kube_pod_info", dp)

	// The group key should NOT include pod/pod_ip.
	if groups := engine.ActiveGroups(); groups != 1 {
		t.Errorf("active groups = %d, want 1", groups)
	}
}

func TestAggregateEngine_StartStop(t *testing.T) {
	rules := []ProcessingRule{
		{
			Name:           "test",
			Input:          ".*",
			Action:         ActionAggregate,
			Interval:       "100ms",
			Functions:      []string{"sum"},
			parsedInterval: 100 * time.Millisecond,
			parsedFunctions: []parsedAggFunc{
				{Func: AggSum},
			},
		},
	}

	engine := newAggregateEngine(rules, time.Second)

	var mu sync.Mutex
	flushCount := 0
	engine.SetOutput(func(rms []*metricspb.ResourceMetrics) {
		mu.Lock()
		flushCount++
		mu.Unlock()
	})

	dp := makeNumberDP(map[string]string{"service": "web"}, 1000, 10)
	engine.Ingest(&rules[0], "test_metric", dp)

	ctx, cancel := context.WithCancel(context.Background())
	engine.Start(ctx)

	// Wait for at least one flush.
	time.Sleep(300 * time.Millisecond)
	cancel()
	engine.Stop()

	mu.Lock()
	if flushCount == 0 {
		t.Error("expected at least one flush")
	}
	mu.Unlock()
}

func TestAggregateEngine_StaleCleanup(t *testing.T) {
	rules := []ProcessingRule{
		{
			Name:           "test",
			Input:          ".*",
			Action:         ActionAggregate,
			Interval:       "1m",
			Functions:      []string{"sum"},
			parsedInterval: time.Minute,
			parsedFunctions: []parsedAggFunc{
				{Func: AggSum},
			},
		},
	}

	// Very short staleness for testing.
	engine := newAggregateEngine(rules, 10*time.Millisecond)

	dp := makeNumberDP(map[string]string{"service": "web"}, 1000, 10)
	engine.Ingest(&rules[0], "test_metric", dp)

	if groups := engine.ActiveGroups(); groups != 1 {
		t.Fatalf("active groups = %d, want 1", groups)
	}

	// Wait for staleness period.
	time.Sleep(20 * time.Millisecond)
	engine.cleanupStale()

	if groups := engine.ActiveGroups(); groups != 0 {
		t.Errorf("after cleanup, active groups = %d, want 0", groups)
	}
}

func TestAggregateEngine_Reload(t *testing.T) {
	rules := []ProcessingRule{
		{
			Name:           "test",
			Input:          ".*",
			Action:         ActionAggregate,
			Interval:       "1m",
			Functions:      []string{"sum"},
			parsedInterval: time.Minute,
			parsedFunctions: []parsedAggFunc{
				{Func: AggSum},
			},
		},
	}

	engine := newAggregateEngine(rules, 10*time.Minute)

	dp := makeNumberDP(map[string]string{"service": "web"}, 1000, 10)
	engine.Ingest(&rules[0], "test_metric", dp)

	if groups := engine.ActiveGroups(); groups != 1 {
		t.Fatalf("active groups = %d, want 1", groups)
	}

	// Reload with new rules.
	newRules := []ProcessingRule{
		{
			Name:           "new-test",
			Input:          ".*",
			Action:         ActionAggregate,
			Interval:       "30s",
			Functions:      []string{"avg"},
			parsedInterval: 30 * time.Second,
			parsedFunctions: []parsedAggFunc{
				{Func: AggAvg},
			},
		},
	}
	engine.Reload(newRules, 5*time.Minute)

	// Old groups should be gone.
	if groups := engine.ActiveGroups(); groups != 0 {
		t.Errorf("after reload, active groups = %d, want 0", groups)
	}
}

func TestBuildGroupLabels_GroupBy(t *testing.T) {
	attrs := []*commonpb.KeyValue{
		{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "web"}}},
		{Key: "pod", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "web-1"}}},
		{Key: "env", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "prod"}}},
	}

	result := buildGroupLabels(attrs, []string{"service", "env"}, nil)
	if len(result) != 2 {
		t.Fatalf("expected 2 labels, got %d", len(result))
	}
}

func TestBuildGroupLabels_DropLabels(t *testing.T) {
	attrs := []*commonpb.KeyValue{
		{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "web"}}},
		{Key: "pod", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "web-1"}}},
		{Key: "env", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "prod"}}},
	}

	result := buildGroupLabels(attrs, nil, []string{"pod"})
	if len(result) != 2 {
		t.Fatalf("expected 2 labels, got %d", len(result))
	}
	for _, kv := range result {
		if kv.Key == "pod" {
			t.Error("pod label should have been dropped")
		}
	}
}

func TestBuildGroupKey(t *testing.T) {
	labels := []*commonpb.KeyValue{
		{Key: "b", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "2"}}},
		{Key: "a", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "1"}}},
	}

	key := buildGroupKey(labels)
	// Should be sorted: a=1|b=2
	if key != "a=1|b=2" {
		t.Errorf("group key = %q, want 'a=1|b=2'", key)
	}
}

func TestFormatQuantile(t *testing.T) {
	tests := []struct {
		q    float64
		want string
	}{
		{0.5, "50"},
		{0.9, "90"},
		{0.95, "95"},
		{0.99, "99"},
		{0.999, "99.9"},
	}
	for _, tt := range tests {
		got := formatQuantile(tt.q)
		if got != tt.want {
			t.Errorf("formatQuantile(%f) = %q, want %q", tt.q, got, tt.want)
		}
	}
}
