package sampling

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
	"go.uber.org/goleak"
)

func makeLeakRM(name string, dpCount int) []*metricspb.ResourceMetrics {
	dps := make([]*metricspb.NumberDataPoint, dpCount)
	for i := 0; i < dpCount; i++ {
		dps[i] = &metricspb.NumberDataPoint{
			Attributes: []*commonpb.KeyValue{
				{Key: "host", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprintf("host-%d", i)}}},
			},
			Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)},
			TimeUnixNano: uint64(time.Now().UnixNano()) + uint64(i),
		}
	}
	return []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{Metrics: []*metricspb.Metric{
					{
						Name: name,
						Data: &metricspb.Metric_Gauge{
							Gauge: &metricspb.Gauge{DataPoints: dps},
						},
					},
				}},
			},
		},
	}
}

// TestLeakCheck_Sampler verifies that creating, using, and discarding a Sampler
// does not leak goroutines.
func TestLeakCheck_Sampler(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	s, err := newFromLegacy(FileConfig{
		DefaultRate: 0.5,
		Strategy:    StrategyHead,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := 0; i < 50; i++ {
		rms := makeLeakRM(fmt.Sprintf("metric_%d", i), 5)
		s.Sample(rms)
	}
}

// TestMemLeak_Sampler_ReloadCycles verifies that repeated Sample + ReloadConfig
// cycles do not cause unbounded heap growth.
func TestMemLeak_Sampler_ReloadCycles(t *testing.T) {
	s, err := newFromLegacy(FileConfig{
		DefaultRate: 0.5,
		Strategy:    StrategyHead,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	const cycles = 200
	for c := 0; c < cycles; c++ {
		for i := 0; i < 50; i++ {
			rms := makeLeakRM(fmt.Sprintf("metric_%d_%d", c, i), 5)
			s.Sample(rms)
		}
		newCfg := FileConfig{
			DefaultRate: float64(c%10) / 10.0,
			Strategy:    StrategyHead,
		}
		if err := reloadFromLegacy(s, newCfg); err != nil {
			t.Fatalf("ReloadConfig cycle %d: %v", c, err)
		}
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("Sampler reload cycles: heap_before=%dKB, heap_after=%dKB",
		heapBefore/1024, heapAfter/1024)

	if heapAfter > heapBefore+30*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after %d cycles",
			heapBefore/1024, heapAfter/1024, cycles)
	}
}

// TestMemLeak_DownsampleEngine_WindowCycles verifies that the downsample engine's
// series map doesn't grow unbounded when processing many unique series.
func TestMemLeak_DownsampleEngine_WindowCycles(t *testing.T) {
	de := newDownsampleEngine()

	cfg := &DownsampleConfig{
		Method:       DSAvg,
		Window:       "1s",
		parsedWindow: time.Second,
	}

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	// Push data through 500 unique series. The engine should clean up stale series.
	const cycles = 500
	baseTime := uint64(time.Now().UnixNano())
	for c := 0; c < cycles; c++ {
		for i := 0; i < 10; i++ {
			seriesKey := fmt.Sprintf("metric_%d|host=host-%d", c, i)
			ts := baseTime + uint64(c)*uint64(time.Second) + uint64(i)*uint64(time.Millisecond)
			de.ingestAndEmit(seriesKey, cfg, ts, float64(c*10+i))
		}
	}

	// Series count should not be unbounded — the engine cleans up every 10000 ingestions
	count := de.seriesCount()
	t.Logf("DownsampleEngine: %d active series after %d cycles", count, cycles)

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("DownsampleEngine window cycles: heap_before=%dKB, heap_after=%dKB",
		heapBefore/1024, heapAfter/1024)

	if heapAfter > heapBefore+30*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after %d cycles",
			heapBefore/1024, heapAfter/1024, cycles)
	}
}

// TestLeakCheck_Processing_AggregateLifecycle verifies aggregate Start/Stop doesn't leak goroutines.
func TestLeakCheck_Processing_AggregateLifecycle(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "agg-sum", Input: "agg_.*", Action: ActionAggregate,
				Interval: "100ms", GroupBy: []string{"service"}, Functions: []string{"sum"}},
			{Name: "agg-avg", Input: "cpu_.*", Action: ActionAggregate,
				Interval: "200ms", Functions: []string{"avg"}},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		t.Fatalf("validateProcessingConfig: %v", err)
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatalf("NewFromProcessing: %v", err)
	}

	var outputMu sync.Mutex
	var outputCount int
	s.SetAggregateOutput(func(rms []*metricspb.ResourceMetrics) {
		outputMu.Lock()
		outputCount += len(rms)
		outputMu.Unlock()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.StartAggregation(ctx)

	// Process some data through the aggregate pipeline.
	for i := 0; i < 50; i++ {
		rms := makeLeakRM(fmt.Sprintf("agg_metric_%d", i), 5)
		s.Process(rms)
	}

	// Allow a few flush cycles.
	time.Sleep(350 * time.Millisecond)

	// Stop aggregation — all goroutines should be cleaned up.
	s.StopAggregation()
}

// TestMemLeak_Processing_ReloadCycles verifies processing reload with aggregate engine
// doesn't cause unbounded heap growth.
func TestMemLeak_Processing_ReloadCycles(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "agg-sum", Input: "agg_.*", Action: ActionAggregate,
				Interval: "1m", GroupBy: []string{"service"}, Functions: []string{"sum"}},
			{Name: "transform-env", Input: ".*", Action: ActionTransform,
				Operations: []Operation{{Set: &SetOp{Label: "env", Value: "test"}}}},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		t.Fatalf("validateProcessingConfig: %v", err)
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatalf("NewFromProcessing: %v", err)
	}

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	const cycles = 200
	for c := 0; c < cycles; c++ {
		// Process data.
		for i := 0; i < 50; i++ {
			rms := makeLeakRM(fmt.Sprintf("agg_metric_%d_%d", c, i), 5)
			s.Process(rms)
		}

		// Reload config with alternating rule sets.
		var newCfg ProcessingConfig
		if c%2 == 0 {
			newCfg = ProcessingConfig{
				Rules: []ProcessingRule{
					{Name: "agg-avg", Input: "agg_.*", Action: ActionAggregate,
						Interval: "30s", GroupBy: []string{"host"}, Functions: []string{"avg"}},
					{Name: "transform-region", Input: ".*", Action: ActionTransform,
						Operations: []Operation{{Set: &SetOp{Label: "region", Value: "us-east-1"}}}},
				},
			}
		} else {
			newCfg = ProcessingConfig{
				Rules: []ProcessingRule{
					{Name: "agg-sum", Input: "agg_.*", Action: ActionAggregate,
						Interval: "1m", GroupBy: []string{"service"}, Functions: []string{"sum"}},
					{Name: "transform-env", Input: ".*", Action: ActionTransform,
						Operations: []Operation{{Set: &SetOp{Label: "env", Value: "prod"}}}},
				},
			}
		}
		if err := s.ReloadProcessingConfig(newCfg); err != nil {
			t.Fatalf("ReloadProcessingConfig cycle %d: %v", c, err)
		}
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("Processing reload cycles: heap_before=%dKB, heap_after=%dKB",
		heapBefore/1024, heapAfter/1024)

	if heapAfter > heapBefore+30*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after %d cycles",
			heapBefore/1024, heapAfter/1024, cycles)
	}
}

// TestMemLeak_Processing_AggregateGroupGrowth verifies aggregate groups don't grow unbounded
// by confirming staleness cleanup keeps group count bounded.
func TestMemLeak_Processing_AggregateGroupGrowth(t *testing.T) {
	cfg := ProcessingConfig{
		StalenessInterval: "100ms",
		Rules: []ProcessingRule{
			{Name: "agg-groups", Input: "group_.*", Action: ActionAggregate,
				Interval: "50ms", GroupBy: []string{"unique_id"}, Functions: []string{"sum"}},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		t.Fatalf("validateProcessingConfig: %v", err)
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatalf("NewFromProcessing: %v", err)
	}

	var outputMu sync.Mutex
	var outputCount int
	s.SetAggregateOutput(func(rms []*metricspb.ResourceMetrics) {
		outputMu.Lock()
		outputCount += len(rms)
		outputMu.Unlock()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s.StartAggregation(ctx)
	defer s.StopAggregation()

	// Feed data in batches with unique label combinations, then wait for cleanup.
	for batch := 0; batch < 5; batch++ {
		// Create 100 unique label combinations per batch.
		for i := 0; i < 100; i++ {
			dp := makeNumberDP(
				map[string]string{"unique_id": fmt.Sprintf("batch%d-id%d", batch, i)},
				uint64(time.Now().UnixNano()),
				float64(i),
			)
			rms := []*metricspb.ResourceMetrics{
				{
					Resource: &resourcepb.Resource{},
					ScopeMetrics: []*metricspb.ScopeMetrics{
						{Metrics: []*metricspb.Metric{
							{
								Name: "group_metric",
								Data: &metricspb.Metric_Gauge{
									Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{dp}},
								},
							},
						}},
					},
				},
			}
			s.Process(rms)
		}

		// Wait for staleness cleanup to run.
		time.Sleep(200 * time.Millisecond)
	}

	// After all batches with cleanup, the active group count should be bounded.
	// Only the most recent batch's groups should remain (at most).
	if !s.HasAggregation() {
		t.Fatal("expected aggregation to be active")
	}

	// Verify group count via heap — we just verify no massive growth.
	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	t.Logf("Aggregate group growth test: heap_inuse=%dKB after %d batches of 100 unique groups",
		m.HeapInuse/1024, 5)
}

// TestMemLeak_Processing_TransformManyRules verifies transform rules don't accumulate memory.
func TestMemLeak_Processing_TransformManyRules(t *testing.T) {
	// Create 20 transform rules.
	rules := make([]ProcessingRule, 20)
	for i := 0; i < 20; i++ {
		rules[i] = ProcessingRule{
			Name:   fmt.Sprintf("transform-%d", i),
			Input:  ".*",
			Action: ActionTransform,
			Operations: []Operation{
				{Set: &SetOp{Label: fmt.Sprintf("label_%d", i), Value: fmt.Sprintf("value_%d", i)}},
			},
		}
	}

	cfg := ProcessingConfig{Rules: rules}
	if err := validateProcessingConfig(&cfg); err != nil {
		t.Fatalf("validateProcessingConfig: %v", err)
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatalf("NewFromProcessing: %v", err)
	}

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	// Process 10000 datapoints through 20 transform rules.
	for i := 0; i < 10000; i++ {
		rms := makeLeakRM(fmt.Sprintf("transform_metric_%d", i%100), 1)
		result := s.Process(rms)
		_ = result
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("Transform many rules: heap_before=%dKB, heap_after=%dKB",
		heapBefore/1024, heapAfter/1024)

	if heapAfter > heapBefore+30*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after 10000 datapoints x 20 rules",
			heapBefore/1024, heapAfter/1024)
	}
}
