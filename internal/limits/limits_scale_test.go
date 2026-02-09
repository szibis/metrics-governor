package limits

import (
	"fmt"
	"math"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// BenchmarkEnforcer_RuleScale measures enforcer throughput as rule count scales.
func BenchmarkEnforcer_RuleScale(b *testing.B) {
	ruleCounts := []int{10, 50, 100, 200, 500, 1000}

	// Warm cache: repeated metric names.
	for _, rc := range ruleCounts {
		b.Run(fmt.Sprintf("%d_cached", rc), func(b *testing.B) {
			cfg := makeScaleConfig(rc)
			enforcer := NewEnforcer(cfg, false, 50000)
			metrics := createScaleMetrics(100, 10)

			// Warm up cache.
			for i := 0; i < 10; i++ {
				enforcer.Process(metrics)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				enforcer.Process(metrics)
			}
		})
	}

	// Cold cache: unique metric names every iteration.
	for _, rc := range []int{100, 500, 1000} {
		b.Run(fmt.Sprintf("%d_coldcache", rc), func(b *testing.B) {
			cfg := makeScaleConfig(rc)
			enforcer := NewEnforcer(cfg, false, 100) // small cache

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				metrics := createUniqueNameMetrics(50, i)
				enforcer.Process(metrics)
			}
		})
	}

	// Label matchers: cache bypass.
	for _, rc := range []int{100, 500} {
		b.Run(fmt.Sprintf("%d_labels", rc), func(b *testing.B) {
			cfg := makeLabelScaleConfig(rc)
			enforcer := NewEnforcer(cfg, false, 50000)
			metrics := createScaleMetrics(100, 10)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				enforcer.Process(metrics)
			}
		})
	}
}

// TestPerf_LimitsScalingVsRuleCount measures throughput degradation as rule count
// increases from 1 to 1000.
func TestPerf_LimitsScalingVsRuleCount(t *testing.T) {
	const iterations = 200

	ruleCounts := []int{1, 10, 50, 100, 200, 500, 1000}
	throughputs := make([]float64, len(ruleCounts))

	for idx, ruleCount := range ruleCounts {
		t.Run(fmt.Sprintf("rules_%d", ruleCount), func(t *testing.T) {
			cfg := makeScaleConfig(ruleCount)
			enforcer := NewEnforcer(cfg, false, 50000)
			metrics := createScaleMetrics(100, 10)

			// Warm up.
			for i := 0; i < 50; i++ {
				enforcer.Process(metrics)
			}

			start := time.Now()
			for i := 0; i < iterations; i++ {
				enforcer.Process(metrics)
			}
			elapsed := time.Since(start)

			// 100 metrics * 10 DPs = 1000 DPs per batch.
			throughput := float64(iterations*1000) / elapsed.Seconds()
			throughputs[idx] = throughput

			t.Logf("%d rules: %.0f DPs/sec (%v for %d iterations)", ruleCount, throughput, elapsed, iterations)
		})
	}

	// Report scaling ratios.
	if throughputs[0] > 0 {
		for i := 1; i < len(ruleCounts); i++ {
			if throughputs[i] > 0 {
				ratio := throughputs[0] / throughputs[i]
				t.Logf("1-rule / %d-rule = %.1fx slowdown", ruleCounts[i], ratio)
			}
		}
	}

	// Verify: 1000 rules should not be more than 100x slower than 1 rule
	// (limits has a cache, so should scale much better than linear).
	if len(throughputs) == len(ruleCounts) && throughputs[0] > 0 && throughputs[len(throughputs)-1] > 0 {
		ratio := throughputs[0] / throughputs[len(throughputs)-1]
		if ratio > 100 && !math.IsInf(ratio, 0) {
			t.Errorf("Scaling %.1fx exceeds 100x threshold", ratio)
		}
	}
}

// makeScaleConfig creates a limits config with N non-matching rules plus a catch-all.
func makeScaleConfig(ruleCount int) *Config {
	rules := make([]Rule, 0, ruleCount+1)
	for i := 0; i < ruleCount; i++ {
		rules = append(rules, Rule{
			Name:              fmt.Sprintf("rule_%04d", i),
			Match:             RuleMatch{MetricName: fmt.Sprintf("nomatch_%04d_.*", i)},
			MaxDatapointsRate: 1000000,
			MaxCardinality:    100000,
			Action:            ActionDrop,
		})
	}
	// Catch-all rule.
	rules = append(rules, Rule{
		Name:              "catch-all",
		Match:             RuleMatch{MetricName: ".*"},
		MaxDatapointsRate: 10000000,
		MaxCardinality:    1000000,
		Action:            ActionLog,
	})

	cfg := &Config{Rules: rules}
	LoadConfigFromStruct(cfg)
	return cfg
}

// makeLabelScaleConfig creates rules with label matchers (bypasses name-only cache).
func makeLabelScaleConfig(ruleCount int) *Config {
	rules := make([]Rule, 0, ruleCount+1)
	for i := 0; i < ruleCount; i++ {
		rules = append(rules, Rule{
			Name: fmt.Sprintf("label_rule_%04d", i),
			Match: RuleMatch{
				MetricName: "benchmark_.*",
				Labels:     map[string]string{"service": fmt.Sprintf("nonexistent_%04d", i)},
			},
			MaxDatapointsRate: 1000000,
			Action:            ActionDrop,
		})
	}
	rules = append(rules, Rule{
		Name:              "catch-all",
		Match:             RuleMatch{MetricName: ".*"},
		MaxDatapointsRate: 10000000,
		Action:            ActionLog,
	})

	cfg := &Config{Rules: rules}
	LoadConfigFromStruct(cfg)
	return cfg
}

// createScaleMetrics creates N metrics with M datapoints each.
func createScaleMetrics(numMetrics, dpPerMetric int) []*metricspb.ResourceMetrics {
	metrics := make([]*metricspb.Metric, numMetrics)
	for i := 0; i < numMetrics; i++ {
		dps := make([]*metricspb.NumberDataPoint, dpPerMetric)
		for j := 0; j < dpPerMetric; j++ {
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
			Name: fmt.Sprintf("benchmark_metric_%04d", i),
			Data: &metricspb.Metric_Gauge{
				Gauge: &metricspb.Gauge{DataPoints: dps},
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
				{Metrics: metrics},
			},
		},
	}
}

// createUniqueNameMetrics creates metrics with unique names per iteration to force cache misses.
func createUniqueNameMetrics(numMetrics int, iteration int) []*metricspb.ResourceMetrics {
	metrics := make([]*metricspb.Metric, numMetrics)
	for i := 0; i < numMetrics; i++ {
		metrics[i] = &metricspb.Metric{
			Name: fmt.Sprintf("unique_%d_metric_%04d", iteration, i),
			Data: &metricspb.Metric_Gauge{
				Gauge: &metricspb.Gauge{
					DataPoints: []*metricspb.NumberDataPoint{
						{
							TimeUnixNano: uint64(time.Now().UnixNano()),
							Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: 1.0},
							Attributes: []*commonpb.KeyValue{
								{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "test"}}},
							},
						},
					},
				},
			},
		}
	}

	return []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{Metrics: metrics},
			},
		},
	}
}
