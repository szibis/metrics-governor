package stats

import (
	"runtime"
	"testing"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
)

func TestStress_StatsMapGrowth(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelFull)

	var mBefore, mAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&mBefore)

	// Simulate multiple collection windows with high cardinality
	for window := 0; window < 5; window++ {
		// Add many unique metrics in each window
		for i := 0; i < 1000; i++ {
			rm := makeResourceMetrics(t, window, i)
			c.Process(rm)
		}
		// Reset (as would happen every 60s in production)
		c.ResetCardinality()
	}

	runtime.GC()
	runtime.ReadMemStats(&mAfter)

	// HeapAlloc can decrease after GC (desired outcome). Only fail if heap grew significantly.
	if mAfter.HeapAlloc > mBefore.HeapAlloc {
		growth := mAfter.HeapAlloc - mBefore.HeapAlloc
		if growth > 50*1024*1024 {
			t.Fatalf("memory growth too high after periodic resets: %d bytes", growth)
		}
	}
}

// TestStress_ProcessFull_MemoryStability_MergedAttrs verifies that the optimized
// processFull with sync.Pool doesn't leak memory across many reset cycles.
func TestStress_ProcessFull_MemoryStability_MergedAttrs(t *testing.T) {
	c := NewCollector([]string{"service", "env"}, StatsLevelFull)

	var mBefore, mAfter runtime.MemStats

	// Warm up: let pools fill and GC settle
	for i := 0; i < 10; i++ {
		rm := makeStressResourceMetricsLarge(100, 10) // 1k datapoints
		c.Process(rm)
	}
	c.ResetCardinality()
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&mBefore)

	// Run 5 cycles of heavy processing + reset
	for cycle := 0; cycle < 5; cycle++ {
		for batch := 0; batch < 100; batch++ {
			rm := makeStressResourceMetricsLarge(100, 10) // 1k datapoints per batch
			c.Process(rm)
		}
		c.ResetCardinality()
	}

	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&mAfter)

	// After 5 cycles of 100k datapoints each, heap growth should be bounded
	if mAfter.HeapAlloc > mBefore.HeapAlloc {
		growth := mAfter.HeapAlloc - mBefore.HeapAlloc
		if growth > 50*1024*1024 { // 50 MB
			t.Errorf("heap grew %d MB after 5 reset cycles with pooled attrs (possible sync.Pool leak)",
				growth/1024/1024)
		}
	}
	t.Logf("heap before=%dKB, after=%dKB", mBefore.HeapAlloc/1024, mAfter.HeapAlloc/1024)
}

// TestStress_ProcessFull_AllocationRate measures allocation rate per datapoint to catch
// sync.Pool regressions. If pool stops working, allocs/datapoint will spike.
func TestStress_ProcessFull_AllocationRate(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelFull)
	rm := makeStressResourceMetricsLarge(100, 100) // 10k datapoints

	// Warm up pools
	for i := 0; i < 5; i++ {
		c.Process(rm)
	}
	c.ResetCardinality()

	var mBefore, mAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&mBefore)

	const batches = 100
	const datapointsPerBatch = 10000
	for i := 0; i < batches; i++ {
		c.Process(rm)
	}

	runtime.ReadMemStats(&mAfter)

	totalAllocs := mAfter.Mallocs - mBefore.Mallocs
	totalDatapoints := uint64(batches * datapointsPerBatch)
	allocsPerDP := float64(totalAllocs) / float64(totalDatapoints)

	t.Logf("allocs/datapoint=%.2f (total allocs=%d, datapoints=%d)", allocsPerDP, totalAllocs, totalDatapoints)

	// With sync.Pool, we expect < 10 allocs per datapoint (maps are pooled).
	// Without pool, this would be ~20+ allocs per datapoint.
	if allocsPerDP > 15 {
		t.Errorf("allocs/datapoint=%.2f exceeds threshold of 15 (sync.Pool regression?)", allocsPerDP)
	}
}

// makeStressResourceMetricsLarge creates resource metrics with specified scale.
func makeStressResourceMetricsLarge(numMetrics, dpPerMetric int) []*metricspb.ResourceMetrics {
	metrics := make([]*metricspb.Metric, numMetrics)
	for i := 0; i < numMetrics; i++ {
		dps := make([]*metricspb.NumberDataPoint, dpPerMetric)
		for j := 0; j < dpPerMetric; j++ {
			dps[j] = &metricspb.NumberDataPoint{
				Attributes: []*commonpb.KeyValue{
					{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "stress-svc"}}},
					{Key: "env", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "prod"}}},
				},
			}
		}
		metrics[i] = &metricspb.Metric{
			Name: "stress_metric",
			Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: dps}},
		}
	}
	return []*metricspb.ResourceMetrics{{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{
				{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "stress"}}},
			},
		},
		ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: metrics}},
	}}
}

func makeResourceMetrics(t *testing.T, window, idx int) []*metricspb.ResourceMetrics {
	t.Helper()
	return []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "svc"}}},
				},
			},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "test_metric",
							Data: &metricspb.Metric_Gauge{
								Gauge: &metricspb.Gauge{
									DataPoints: []*metricspb.NumberDataPoint{
										{
											Attributes: []*commonpb.KeyValue{
												{Key: "id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{
													StringValue: t.Name(),
												}}},
											},
											Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(idx)},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}
