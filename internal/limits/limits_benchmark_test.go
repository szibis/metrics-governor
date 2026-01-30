package limits

import (
	"fmt"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// BenchmarkEnforcer_Process benchmarks the enforcer with no rules
func BenchmarkEnforcer_Process_NoRules(b *testing.B) {
	enforcer := NewEnforcer(&Config{}, false)
	metrics := createBenchmarkMetrics(100, 10)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = enforcer.Process(metrics)
	}
}

// BenchmarkEnforcer_Process_SimpleRule benchmarks with a simple rule
func BenchmarkEnforcer_Process_SimpleRule(b *testing.B) {
	config := &Config{
		Rules: []Rule{
			{
				Name:              "test-rule",
				Match:             RuleMatch{MetricName: "benchmark_.*"},
				MaxDatapointsRate: 100000,
				MaxCardinality:    10000,
				Action:            ActionDrop,
				GroupBy:           []string{"service"},
			},
		},
	}
	enforcer := NewEnforcer(config, false)
	metrics := createBenchmarkMetrics(100, 10)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = enforcer.Process(metrics)
	}
}

// BenchmarkEnforcer_Process_MultipleRules benchmarks with multiple rules
func BenchmarkEnforcer_Process_MultipleRules(b *testing.B) {
	config := &Config{
		Rules: []Rule{
			{
				Name:              "rule-1",
				Match:             RuleMatch{MetricName: "benchmark_metric_.*"},
				MaxDatapointsRate: 100000,
				MaxCardinality:    10000,
				Action:            ActionDrop,
				GroupBy:           []string{"service"},
			},
			{
				Name:              "rule-2",
				Match:             RuleMatch{MetricName: "other_.*"},
				MaxDatapointsRate: 50000,
				MaxCardinality:    5000,
				Action:            ActionDrop,
				GroupBy:           []string{"service", "env"},
			},
			{
				Name:              "rule-3",
				Match:             RuleMatch{MetricName: ".*_total$"},
				MaxDatapointsRate: 200000,
				MaxCardinality:    20000,
				Action:            ActionAdaptive,
				GroupBy:           []string{"service"},
			},
		},
	}
	enforcer := NewEnforcer(config, false)
	metrics := createBenchmarkMetrics(100, 10)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = enforcer.Process(metrics)
	}
}

// BenchmarkEnforcer_Process_DryRun benchmarks dry run mode
func BenchmarkEnforcer_Process_DryRun(b *testing.B) {
	config := &Config{
		Rules: []Rule{
			{
				Name:              "test-rule",
				Match:             RuleMatch{MetricName: "benchmark_.*"},
				MaxDatapointsRate: 10,
				MaxCardinality:    5, // Low limits
				Action:            ActionDrop,
				GroupBy:           []string{"service"},
			},
		},
	}
	enforcer := NewEnforcer(config, true) // dry run
	metrics := createBenchmarkMetrics(100, 10)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = enforcer.Process(metrics)
	}
}

// BenchmarkEnforcer_Process_HighCardinality benchmarks with high cardinality data
func BenchmarkEnforcer_Process_HighCardinality(b *testing.B) {
	config := &Config{
		Rules: []Rule{
			{
				Name:              "high-card-rule",
				Match:             RuleMatch{MetricName: "high_cardinality_.*"},
				MaxDatapointsRate: 1000000,
				MaxCardinality:    100000,
				Action:            ActionDrop,
				GroupBy:           []string{"service", "user_id"},
			},
		},
	}
	enforcer := NewEnforcer(config, false)
	metrics := createHighCardinalityMetrics(1000, 10)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = enforcer.Process(metrics)
	}
}

// BenchmarkEnforcer_Process_Concurrent benchmarks concurrent processing
func BenchmarkEnforcer_Process_Concurrent(b *testing.B) {
	config := &Config{
		Rules: []Rule{
			{
				Name:              "concurrent-rule",
				Match:             RuleMatch{MetricName: "benchmark_.*"},
				MaxDatapointsRate: 1000000,
				MaxCardinality:    100000,
				Action:            ActionDrop,
				GroupBy:           []string{"service"},
			},
		},
	}
	enforcer := NewEnforcer(config, false)
	metrics := createBenchmarkMetrics(100, 10)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = enforcer.Process(metrics)
		}
	})
}

// BenchmarkEnforcer_Process_Scale benchmarks at different scales
func BenchmarkEnforcer_Process_Scale(b *testing.B) {
	scales := []struct {
		name       string
		metrics    int
		datapoints int
	}{
		{"small_10x10", 10, 10},
		{"medium_100x100", 100, 100},
		{"large_1000x100", 1000, 100},
		{"xlarge_1000x1000", 1000, 1000},
	}

	config := &Config{
		Rules: []Rule{
			{
				Name:              "scale-rule",
				Match:             RuleMatch{MetricName: "benchmark_.*"},
				MaxDatapointsRate: 10000000,
				MaxCardinality:    1000000,
				Action:            ActionDrop,
				GroupBy:           []string{"service"},
			},
		},
	}

	for _, scale := range scales {
		b.Run(scale.name, func(b *testing.B) {
			enforcer := NewEnforcer(config, false)
			metrics := createBenchmarkMetrics(scale.metrics, scale.datapoints)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = enforcer.Process(metrics)
			}
		})
	}
}

// BenchmarkEnforcer_Process_RegexMatch benchmarks regex matching
func BenchmarkEnforcer_Process_RegexMatch(b *testing.B) {
	config := &Config{
		Rules: []Rule{
			{
				Name:              "regex-rule",
				Match:             RuleMatch{MetricName: "^benchmark_metric_[0-9]+$"},
				MaxDatapointsRate: 100000,
				MaxCardinality:    10000,
				Action:            ActionDrop,
				GroupBy:           []string{"service"},
			},
		},
	}
	enforcer := NewEnforcer(config, false)
	metrics := createBenchmarkMetrics(100, 10)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = enforcer.Process(metrics)
	}
}

// BenchmarkEnforcer_Process_LabelMatch benchmarks label matching
func BenchmarkEnforcer_Process_LabelMatch(b *testing.B) {
	config := &Config{
		Rules: []Rule{
			{
				Name: "label-rule",
				Match: RuleMatch{
					Labels: map[string]string{
						"service": "benchmark-service",
						"env":     "prod",
					},
				},
				MaxDatapointsRate: 100000,
				MaxCardinality:    10000,
				Action:            ActionDrop,
				GroupBy:           []string{"service"},
			},
		},
	}
	enforcer := NewEnforcer(config, false)
	metrics := createBenchmarkMetrics(100, 10)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = enforcer.Process(metrics)
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
					{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "benchmark-service"}}},
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

func createHighCardinalityMetrics(uniqueUsers, datapointsPerUser int) []*metricspb.ResourceMetrics {
	var allDatapoints []*metricspb.NumberDataPoint

	for i := 0; i < uniqueUsers; i++ {
		for j := 0; j < datapointsPerUser; j++ {
			allDatapoints = append(allDatapoints, &metricspb.NumberDataPoint{
				TimeUnixNano: uint64(time.Now().UnixNano()),
				Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(j)},
				Attributes: []*commonpb.KeyValue{
					{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "high-cardinality-service"}}},
					{Key: "user_id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprintf("user_%d", i)}}},
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
