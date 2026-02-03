package stats

import (
	"fmt"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// BenchmarkCollector_Process benchmarks the stats collector processing
func BenchmarkCollector_Process(b *testing.B) {
	collector := NewCollector([]string{"service", "env"})

	metrics := createBenchmarkMetrics(100, 10)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		collector.Process(metrics)
	}
}

// BenchmarkCollector_ProcessHighCardinality benchmarks with high cardinality labels
func BenchmarkCollector_ProcessHighCardinality(b *testing.B) {
	collector := NewCollector([]string{"service", "env", "user_id", "request_id"})

	// Create metrics with many unique label combinations
	metrics := createHighCardinalityMetrics(1000, 10)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		collector.Process(metrics)
	}
}

// BenchmarkCollector_ProcessManyDatapoints benchmarks with many datapoints per metric
func BenchmarkCollector_ProcessManyDatapoints(b *testing.B) {
	collector := NewCollector([]string{"service"})

	// Create metrics with many datapoints
	metrics := createMetricsWithManyDatapoints(10, 1000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		collector.Process(metrics)
	}
}

// BenchmarkCollector_GetGlobalStats benchmarks getting stats
func BenchmarkCollector_GetGlobalStats(b *testing.B) {
	collector := NewCollector([]string{"service", "env"})

	// Pre-populate with data
	for i := 0; i < 100; i++ {
		metrics := createBenchmarkMetrics(100, 10)
		collector.Process(metrics)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = collector.GetGlobalStats()
	}
}

// BenchmarkCollector_ConcurrentProcess benchmarks concurrent processing
func BenchmarkCollector_ConcurrentProcess(b *testing.B) {
	collector := NewCollector([]string{"service", "env"})
	metrics := createBenchmarkMetrics(100, 10)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			collector.Process(metrics)
		}
	})
}

// Benchmark table tests for different scales
func BenchmarkCollector_Scale(b *testing.B) {
	scales := []struct {
		name       string
		metrics    int
		datapoints int
	}{
		{"small_10x10", 10, 10},
		{"medium_100x100", 100, 100},
		{"large_1000x100", 1000, 100},
		{"many_metrics_10000x10", 10000, 10},
		{"many_datapoints_10x10000", 10, 10000},
	}

	for _, scale := range scales {
		b.Run(scale.name, func(b *testing.B) {
			collector := NewCollector([]string{"service"})
			metrics := createBenchmarkMetrics(scale.metrics, scale.datapoints)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				collector.Process(metrics)
			}
		})
	}
}

// BenchmarkBuildSeriesKey benchmarks the pooled buildSeriesKey function.
func BenchmarkBuildSeriesKey(b *testing.B) {
	sizes := []int{1, 5, 10, 50}

	for _, size := range sizes {
		attrs := make(map[string]string, size)
		for i := 0; i < size; i++ {
			attrs[fmt.Sprintf("key_%03d", i)] = fmt.Sprintf("value_%03d", i)
		}

		b.Run(fmt.Sprintf("attrs_%d", size), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = buildSeriesKey(attrs)
			}
		})
	}
}

// BenchmarkBuildSeriesKey_Parallel benchmarks buildSeriesKey under contention.
func BenchmarkBuildSeriesKey_Parallel(b *testing.B) {
	sizes := []int{1, 5, 10, 50}

	for _, size := range sizes {
		attrs := make(map[string]string, size)
		for i := 0; i < size; i++ {
			attrs[fmt.Sprintf("key_%03d", i)] = fmt.Sprintf("value_%03d", i)
		}

		b.Run(fmt.Sprintf("attrs_%d", size), func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					_ = buildSeriesKey(attrs)
				}
			})
		})
	}
}

// Helper functions

func createBenchmarkMetrics(numMetrics, datapointsPerMetric int) []*metricspb.ResourceMetrics {
	metrics := make([]*metricspb.Metric, numMetrics)
	for i := 0; i < numMetrics; i++ {
		dps := make([]*metricspb.NumberDataPoint, datapointsPerMetric)
		for j := 0; j < datapointsPerMetric; j++ {
			dps[j] = &metricspb.NumberDataPoint{
				TimeUnixNano: uint64(time.Now().UnixNano()),
				Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(j)},
				Attributes: []*commonpb.KeyValue{
					{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "test-service"}}},
					{Key: "env", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "prod"}}},
				},
			}
		}
		metrics[i] = &metricspb.Metric{
			Name: fmt.Sprintf("benchmark_metric_%d", i),
			Data: &metricspb.Metric_Gauge{
				Gauge: &metricspb.Gauge{
					DataPoints: dps,
				},
			},
		}
	}

	return []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "benchmark-service"}}},
				},
			},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: metrics,
				},
			},
		},
	}
}

func createHighCardinalityMetrics(uniqueCombinations, datapointsPerCombination int) []*metricspb.ResourceMetrics {
	var allDatapoints []*metricspb.NumberDataPoint

	for i := 0; i < uniqueCombinations; i++ {
		for j := 0; j < datapointsPerCombination; j++ {
			allDatapoints = append(allDatapoints, &metricspb.NumberDataPoint{
				TimeUnixNano: uint64(time.Now().UnixNano()),
				Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(j)},
				Attributes: []*commonpb.KeyValue{
					{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprintf("service_%d", i%10)}}},
					{Key: "env", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprintf("env_%d", i%3)}}},
					{Key: "user_id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprintf("user_%d", i)}}},
					{Key: "request_id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprintf("req_%d_%d", i, j)}}},
				},
			})
		}
	}

	return []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "high-cardinality-service"}}},
				},
			},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "high_cardinality_metric",
							Data: &metricspb.Metric_Gauge{
								Gauge: &metricspb.Gauge{
									DataPoints: allDatapoints,
								},
							},
						},
					},
				},
			},
		},
	}
}

func createMetricsWithManyDatapoints(numMetrics, datapointsPerMetric int) []*metricspb.ResourceMetrics {
	metrics := make([]*metricspb.Metric, numMetrics)
	for i := 0; i < numMetrics; i++ {
		dps := make([]*metricspb.NumberDataPoint, datapointsPerMetric)
		for j := 0; j < datapointsPerMetric; j++ {
			dps[j] = &metricspb.NumberDataPoint{
				TimeUnixNano: uint64(time.Now().UnixNano()) + uint64(j),
				Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(j) * 0.1},
				Attributes: []*commonpb.KeyValue{
					{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "datapoint-service"}}},
				},
			}
		}
		metrics[i] = &metricspb.Metric{
			Name: fmt.Sprintf("many_datapoints_metric_%d", i),
			Data: &metricspb.Metric_Gauge{
				Gauge: &metricspb.Gauge{
					DataPoints: dps,
				},
			},
		}
	}

	return []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "many-datapoints-service"}}},
				},
			},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: metrics,
				},
			},
		},
	}
}
