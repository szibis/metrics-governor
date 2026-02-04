package stats

import (
	"runtime"
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

func TestStress_StatsMapGrowth(t *testing.T) {
	c := NewCollector([]string{"service"})

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
