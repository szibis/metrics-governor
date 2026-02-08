package sampling

import (
	"fmt"
	"sync"
	"testing"

	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// makeBenchBatch creates a batch of ResourceMetrics with the given number of
// unique metric names and datapoints per metric. Each datapoint has unique
// label values to exercise rule matching.
func makeBenchBatch(metricCount, dpPerMetric int, namePrefix string, tsBase uint64) []*metricspb.ResourceMetrics {
	rms := make([]*metricspb.ResourceMetrics, 0, metricCount)
	for m := 0; m < metricCount; m++ {
		name := fmt.Sprintf("%s_%04d", namePrefix, m)
		dps := make([]*metricspb.NumberDataPoint, 0, dpPerMetric)
		for d := 0; d < dpPerMetric; d++ {
			attrs := map[string]string{
				"service":  fmt.Sprintf("svc_%d", m%10),
				"instance": fmt.Sprintf("inst_%d", d%5),
				"env":      "production",
			}
			ts := tsBase + uint64(m*dpPerMetric+d)*uint64(1_000_000_000)
			dps = append(dps, makeNumberDP(attrs, ts, float64(m*100+d)))
		}
		rms = append(rms, makeProcGaugeRM(name, dps...)...)
	}
	return rms
}

// BenchmarkProcess_SampleHead benchmarks head sampling throughput.
func BenchmarkProcess_SampleHead(b *testing.B) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "sample-all", Input: "bench_.*", Action: ActionSample, Rate: 0.5, Method: "head"},
		},
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		b.Fatal(err)
	}

	batch := makeBenchBatch(50, 10, "bench_metric", 1_000_000_000)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sampler.Process(batch)
	}
}

// BenchmarkProcess_SampleProbabilistic benchmarks probabilistic sampling.
func BenchmarkProcess_SampleProbabilistic(b *testing.B) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "prob-sample", Input: "bench_.*", Action: ActionSample, Rate: 0.5, Method: "probabilistic"},
		},
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		b.Fatal(err)
	}

	batch := makeBenchBatch(50, 10, "bench_metric", 1_000_000_000)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sampler.Process(batch)
	}
}

// BenchmarkProcess_Downsample_Avg benchmarks average downsampling.
func BenchmarkProcess_Downsample_Avg(b *testing.B) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "ds-avg", Input: "bench_.*", Action: ActionDownsample, Method: "avg", Interval: "1m"},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		b.Fatal(err)
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Unique timestamps per iteration to simulate time progression.
		ts := uint64(i) * 10_000_000_000
		batch := makeBenchBatch(20, 5, "bench_metric", ts)
		sampler.Process(batch)
	}
}

// BenchmarkProcess_Downsample_LTTB benchmarks LTTB visual downsampling.
func BenchmarkProcess_Downsample_LTTB(b *testing.B) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "ds-lttb", Input: "bench_.*", Action: ActionDownsample, Method: "lttb", Interval: "1m", Resolution: 10},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		b.Fatal(err)
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ts := uint64(i) * 10_000_000_000
		batch := makeBenchBatch(20, 5, "bench_metric", ts)
		sampler.Process(batch)
	}
}

// BenchmarkProcess_Downsample_SDT benchmarks SDT deadband downsampling.
func BenchmarkProcess_Downsample_SDT(b *testing.B) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "ds-sdt", Input: "bench_.*", Action: ActionDownsample, Method: "sdt", Deviation: 1.5},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		b.Fatal(err)
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ts := uint64(i) * 10_000_000_000
		batch := makeBenchBatch(20, 5, "bench_metric", ts)
		sampler.Process(batch)
	}
}

// BenchmarkProcess_Downsample_Adaptive benchmarks adaptive variance-based downsampling.
func BenchmarkProcess_Downsample_Adaptive(b *testing.B) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:           "ds-adaptive",
				Input:          "bench_.*",
				Action:         ActionDownsample,
				Method:         "adaptive",
				Interval:       "1m",
				MinRate:        0.1,
				MaxRate:        1.0,
				VarianceWindow: 20,
			},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		b.Fatal(err)
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ts := uint64(i) * 10_000_000_000
		batch := makeBenchBatch(20, 5, "bench_metric", ts)
		sampler.Process(batch)
	}
}

// BenchmarkProcess_Aggregate_SmallGroups benchmarks aggregation with 100 unique label combinations.
func BenchmarkProcess_Aggregate_SmallGroups(b *testing.B) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:      "agg-small",
				Input:     "bench_.*",
				Action:    ActionAggregate,
				Interval:  "30s",
				GroupBy:   []string{"service", "instance"},
				Functions: []string{"sum", "count"},
				Output:    "bench_agg",
			},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		b.Fatal(err)
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		b.Fatal(err)
	}

	// 10 services * 10 instances = 100 unique groups.
	batch := make([]*metricspb.ResourceMetrics, 0, 100)
	for s := 0; s < 10; s++ {
		for inst := 0; inst < 10; inst++ {
			dp := makeNumberDP(map[string]string{
				"service":  fmt.Sprintf("svc_%d", s),
				"instance": fmt.Sprintf("inst_%d", inst),
			}, 1_000_000_000, float64(s*10+inst))
			batch = append(batch, makeProcGaugeRM("bench_requests", dp)...)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sampler.Process(batch)
	}
}

// BenchmarkProcess_Aggregate_LargeGroups benchmarks aggregation with 10k unique label combinations.
func BenchmarkProcess_Aggregate_LargeGroups(b *testing.B) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:      "agg-large",
				Input:     "bench_.*",
				Action:    ActionAggregate,
				Interval:  "30s",
				GroupBy:   []string{"service", "instance", "endpoint"},
				Functions: []string{"sum", "avg"},
				Output:    "bench_agg_large",
			},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		b.Fatal(err)
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		b.Fatal(err)
	}

	// 10 services * 100 instances * 10 endpoints = 10,000 unique groups.
	batch := make([]*metricspb.ResourceMetrics, 0, 10000)
	for s := 0; s < 10; s++ {
		for inst := 0; inst < 100; inst++ {
			for ep := 0; ep < 10; ep++ {
				dp := makeNumberDP(map[string]string{
					"service":  fmt.Sprintf("svc_%d", s),
					"instance": fmt.Sprintf("inst_%d", inst),
					"endpoint": fmt.Sprintf("/api/v1/ep_%d", ep),
				}, 1_000_000_000, float64(s*1000+inst*10+ep))
				batch = append(batch, makeProcGaugeRM("bench_requests", dp)...)
			}
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sampler.Process(batch)
	}
}

// BenchmarkProcess_Transform_SimpleSet benchmarks a simple label set operation.
func BenchmarkProcess_Transform_SimpleSet(b *testing.B) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "xform-set",
				Input:  "bench_.*",
				Action: ActionTransform,
				Operations: []Operation{
					{Set: &SetOp{Label: "env", Value: "production"}},
				},
			},
			// Terminal rule to prevent pass-through overhead confusion.
			{Name: "keep-all", Input: ".*", Action: ActionSample, Rate: 1.0, Method: "head"},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		b.Fatal(err)
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		b.Fatal(err)
	}

	batch := makeBenchBatch(50, 10, "bench_metric", 1_000_000_000)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sampler.Process(batch)
	}
}

// BenchmarkProcess_Transform_RegexExtract benchmarks regex extract operations.
func BenchmarkProcess_Transform_RegexExtract(b *testing.B) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "xform-extract",
				Input:  "bench_.*",
				Action: ActionTransform,
				Operations: []Operation{
					{Extract: &ExtractOp{
						Source:  "service",
						Target:  "service_prefix",
						Pattern: `^(svc)_\d+$`,
						Group:   1,
					}},
				},
			},
			{Name: "keep-all", Input: ".*", Action: ActionSample, Rate: 1.0, Method: "head"},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		b.Fatal(err)
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		b.Fatal(err)
	}

	batch := makeBenchBatch(50, 10, "bench_metric", 1_000_000_000)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sampler.Process(batch)
	}
}

// BenchmarkProcess_Transform_HashMod benchmarks hash_mod operations.
func BenchmarkProcess_Transform_HashMod(b *testing.B) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "xform-hashmod",
				Input:  "bench_.*",
				Action: ActionTransform,
				Operations: []Operation{
					{HashMod: &HashModOp{Source: "service", Target: "shard", Modulus: 16}},
				},
			},
			{Name: "keep-all", Input: ".*", Action: ActionSample, Rate: 1.0, Method: "head"},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		b.Fatal(err)
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		b.Fatal(err)
	}

	batch := makeBenchBatch(50, 10, "bench_metric", 1_000_000_000)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sampler.Process(batch)
	}
}

// BenchmarkProcess_Drop benchmarks the drop action.
func BenchmarkProcess_Drop(b *testing.B) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "drop-bench", Input: "bench_.*", Action: ActionDrop},
		},
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		b.Fatal(err)
	}

	batch := makeBenchBatch(50, 10, "bench_metric", 1_000_000_000)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sampler.Process(batch)
	}
}

// BenchmarkProcess_MixedRules benchmarks a realistic pipeline with 10 rules
// covering all action types.
func BenchmarkProcess_MixedRules(b *testing.B) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			// Transform: add environment label.
			{
				Name:   "add-env",
				Input:  ".*",
				Action: ActionTransform,
				Operations: []Operation{
					{Set: &SetOp{Label: "cluster", Value: "us-east-1"}},
				},
			},
			// Drop: remove debug metrics.
			{Name: "drop-debug", Input: "debug_.*", Action: ActionDrop},
			// Drop: remove internal metrics.
			{Name: "drop-internal", Input: "internal_.*", Action: ActionDrop},
			// Sample: keep 50% of test metrics.
			{Name: "sample-test", Input: "test_.*", Action: ActionSample, Rate: 0.5, Method: "head"},
			// Transform: extract service prefix.
			{
				Name:   "extract-svc",
				Input:  "bench_.*",
				Action: ActionTransform,
				Operations: []Operation{
					{Extract: &ExtractOp{Source: "service", Target: "svc_group", Pattern: `^(svc)_\d+$`, Group: 1}},
				},
			},
			// Transform: hash mod for sharding.
			{
				Name:   "shard",
				Input:  "bench_.*",
				Action: ActionTransform,
				Operations: []Operation{
					{HashMod: &HashModOp{Source: "service", Target: "shard_id", Modulus: 8}},
				},
			},
			// Downsample: avg for CPU metrics.
			{Name: "ds-cpu", Input: "cpu_.*", Action: ActionDownsample, Method: "avg", Interval: "1m"},
			// Downsample: LTTB for latency metrics.
			{Name: "ds-latency", Input: "latency_.*", Action: ActionDownsample, Method: "lttb", Interval: "1m", Resolution: 10},
			// Aggregate: request totals.
			{
				Name:      "agg-requests",
				Input:     "request_.*",
				Action:    ActionAggregate,
				Interval:  "30s",
				GroupBy:   []string{"service"},
				Functions: []string{"sum"},
				Output:    "request_total_agg",
			},
			// Sample: keep all remaining.
			{Name: "keep-rest", Input: ".*", Action: ActionSample, Rate: 1.0, Method: "head"},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		b.Fatal(err)
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		b.Fatal(err)
	}

	// Create a mixed batch with different metric name prefixes to hit different rules.
	batch := make([]*metricspb.ResourceMetrics, 0, 200)
	prefixes := []string{"bench_metric", "debug_trace", "internal_sys", "test_foo", "cpu_usage", "latency_p99", "request_total", "other_metric"}
	for _, prefix := range prefixes {
		batch = append(batch, makeBenchBatch(5, 5, prefix, 1_000_000_000)...)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sampler.Process(batch)
	}
}

// BenchmarkProcess_HighCardinality benchmarks processing with 100k unique series.
func BenchmarkProcess_HighCardinality(b *testing.B) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "tag-all",
				Input:  "hc_.*",
				Action: ActionTransform,
				Operations: []Operation{
					{Set: &SetOp{Label: "processed", Value: "true"}},
				},
			},
			{Name: "keep-hc", Input: "hc_.*", Action: ActionSample, Rate: 1.0, Method: "head"},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		b.Fatal(err)
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		b.Fatal(err)
	}

	// 100k unique series: 1000 metrics * 100 label combos.
	batch := make([]*metricspb.ResourceMetrics, 0, 100_000)
	for m := 0; m < 1000; m++ {
		name := fmt.Sprintf("hc_metric_%04d", m)
		for l := 0; l < 100; l++ {
			dp := makeNumberDP(map[string]string{
				"service":  fmt.Sprintf("svc_%d", l%50),
				"instance": fmt.Sprintf("inst_%d", l/50),
				"pod":      fmt.Sprintf("pod_%05d", m*100+l),
			}, 1_000_000_000, float64(m*100+l))
			batch = append(batch, makeProcGaugeRM(name, dp)...)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sampler.Process(batch)
	}
}

// BenchmarkProcess_ConfigReload benchmarks reload under concurrent load.
func BenchmarkProcess_ConfigReload(b *testing.B) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "sample-all", Input: ".*", Action: ActionSample, Rate: 1.0, Method: "head"},
		},
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		b.Fatal(err)
	}

	batch := makeBenchBatch(20, 5, "bench_metric", 1_000_000_000)

	// Alternate config for reload.
	altCfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "sample-half", Input: ".*", Action: ActionSample, Rate: 0.5, Method: "head"},
		},
	}

	var wg sync.WaitGroup

	b.ReportAllocs()
	b.ResetTimer()

	// Start a goroutine that continuously processes.
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				sampler.Process(batch)
			}
		}
	}()

	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			_ = sampler.ReloadProcessingConfig(altCfg)
		} else {
			_ = sampler.ReloadProcessingConfig(cfg)
		}
	}

	close(stop)
	wg.Wait()
}
