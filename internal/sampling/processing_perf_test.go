package sampling

import (
	"fmt"
	"math"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// makePerfBatch creates a batch of ResourceMetrics for performance tests.
func makePerfBatch(count int, namePrefix string, tsBase uint64) []*metricspb.ResourceMetrics {
	rms := make([]*metricspb.ResourceMetrics, 0, count)
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("%s_%04d", namePrefix, i)
		dp := makeNumberDP(map[string]string{
			"service":  fmt.Sprintf("svc_%d", i%20),
			"instance": fmt.Sprintf("inst_%d", i%10),
			"env":      "production",
		}, tsBase+uint64(i)*1_000_000_000, float64(i)*1.5)
		rms = append(rms, makeProcGaugeRM(name, dp)...)
	}
	return rms
}

// TestPerf_SampleThroughput measures sample action throughput and compares
// processing-engine Sample with the legacy Sampler pathway.
func TestPerf_SampleThroughput(t *testing.T) {
	const iterations = 500
	const batchSize = 100

	// Processing engine sampler.
	procCfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "sample-all", Input: "perf_.*", Action: ActionSample, Rate: 0.5, Method: "head"},
		},
	}
	procSampler, err := NewFromProcessing(procCfg)
	if err != nil {
		t.Fatal(err)
	}

	// Legacy sampler.
	legacyCfg := FileConfig{
		DefaultRate: 0.5,
		Strategy:    StrategyHead,
	}
	legacySampler, err := New(legacyCfg)
	if err != nil {
		t.Fatal(err)
	}

	batch := makePerfBatch(batchSize, "perf_metric", 1_000_000_000)

	// Warm up.
	for i := 0; i < 50; i++ {
		procSampler.Process(batch)
		legacySampler.Sample(batch)
	}

	// Measure processing engine.
	start := time.Now()
	for i := 0; i < iterations; i++ {
		procSampler.Process(batch)
	}
	procDur := time.Since(start)
	procThroughput := float64(iterations*batchSize) / procDur.Seconds()

	// Measure legacy sampler.
	start = time.Now()
	for i := 0; i < iterations; i++ {
		legacySampler.Sample(batch)
	}
	legacyDur := time.Since(start)
	legacyThroughput := float64(iterations*batchSize) / legacyDur.Seconds()

	t.Logf("Processing engine: %d iterations in %v (%.0f series/sec)", iterations, procDur, procThroughput)
	t.Logf("Legacy sampler:    %d iterations in %v (%.0f series/sec)", iterations, legacyDur, legacyThroughput)

	// Processing engine should not be more than 10x slower than legacy.
	// The overhead comes from regex matching, multi-touch rule evaluation, and
	// Prometheus metric recording. With -race enabled, both paths slow down
	// unevenly, so we use a generous threshold here. Benchmarks without -race
	// give the real performance picture.
	ratio := procDur.Seconds() / legacyDur.Seconds()
	t.Logf("Processing/Legacy ratio: %.2fx", ratio)
	if ratio > 10.0 {
		t.Errorf("Processing engine is %.1fx slower than legacy (threshold 10x)", ratio)
	}
}

// TestPerf_LatencyPercentiles measures P50/P95/P99 latency of Process() under
// steady load across 1000 iterations.
func TestPerf_LatencyPercentiles(t *testing.T) {
	const iterations = 1000
	const batchSize = 50

	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "xform-tag",
				Input:  ".*",
				Action: ActionTransform,
				Operations: []Operation{
					{Set: &SetOp{Label: "cluster", Value: "bench"}},
				},
			},
			{Name: "drop-debug", Input: "debug_.*", Action: ActionDrop},
			{Name: "keep-rest", Input: ".*", Action: ActionSample, Rate: 1.0, Method: "head"},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	batch := makePerfBatch(batchSize, "perf_metric", 1_000_000_000)

	// Warm up.
	for i := 0; i < 100; i++ {
		sampler.Process(batch)
	}

	// Collect latencies.
	latencies := make([]time.Duration, iterations)
	for i := 0; i < iterations; i++ {
		start := time.Now()
		sampler.Process(batch)
		latencies[i] = time.Since(start)
	}

	// Sort latencies for percentile calculation.
	sortDurations(latencies)

	p50 := latencies[iterations*50/100]
	p95 := latencies[iterations*95/100]
	p99 := latencies[iterations*99/100]

	t.Logf("Latency P50=%v  P95=%v  P99=%v", p50, p95, p99)
	t.Logf("Batch size: %d series, Rules: %d", batchSize, len(cfg.Rules))

	// P99 should be under 10ms for 50 series with 3 rules.
	if p99 > 10*time.Millisecond {
		t.Errorf("P99 latency %v exceeds 10ms threshold", p99)
	}

	// P95/P50 ratio should be reasonable (no extreme outliers).
	if p50 > 0 {
		jitterRatio := float64(p95) / float64(p50)
		t.Logf("P95/P50 jitter ratio: %.2f", jitterRatio)
		if jitterRatio > 20.0 {
			t.Errorf("P95/P50 jitter ratio %.1f exceeds 20x threshold", jitterRatio)
		}
	}
}

// sortDurations performs an insertion sort on a slice of durations.
// Using insertion sort to avoid importing sort package for a simple test utility.
func sortDurations(d []time.Duration) {
	for i := 1; i < len(d); i++ {
		key := d[i]
		j := i - 1
		for j >= 0 && d[j] > key {
			d[j+1] = d[j]
			j--
		}
		d[j+1] = key
	}
}

// TestPerf_MemoryPerGroup verifies that aggregate groups use a reasonable
// amount of memory each (target: <= 2000 bytes per group including test overhead).
func TestPerf_MemoryPerGroup(t *testing.T) {
	const groupCount = 10_000

	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:      "agg-mem",
				Input:     "mem_.*",
				Action:    ActionAggregate,
				Interval:  "1m",
				GroupBy:   []string{"service", "instance"},
				Functions: []string{"sum", "count"},
				Output:    "mem_agg",
			},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Force GC and measure baseline using TotalAlloc (monotonically increasing).
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	// Create 10k unique groups: 100 services * 100 instances.
	for s := 0; s < 100; s++ {
		for inst := 0; inst < 100; inst++ {
			dp := makeNumberDP(map[string]string{
				"service":  fmt.Sprintf("svc_%03d", s),
				"instance": fmt.Sprintf("inst_%03d", inst),
			}, 1_000_000_000, float64(s*100+inst))
			rms := makeProcGaugeRM("mem_requests", dp)
			sampler.Process(rms)
		}
	}

	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	totalAlloc := after.TotalAlloc - before.TotalAlloc
	bytesPerGroup := float64(totalAlloc) / float64(groupCount)

	t.Logf("Total allocations: %d bytes for %d groups (%.1f bytes/group)", totalAlloc, groupCount, bytesPerGroup)

	// Allow generous threshold — TotalAlloc includes transient allocations from
	// protobuf construction, string formatting (Sprintf), map buckets, etc.
	if bytesPerGroup > 2000 {
		t.Errorf("Allocations per group %.1f bytes exceed 2000 byte threshold", bytesPerGroup)
	}
}

// TestPerf_MemoryPerSeries verifies that downsample series use a reasonable
// amount of memory each (target: <= 2000 bytes per series including test overhead).
func TestPerf_MemoryPerSeries(t *testing.T) {
	const seriesCount = 10_000

	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "ds-mem", Input: "dsmem_.*", Action: ActionDownsample, Method: "avg", Interval: "1m"},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Force GC and measure baseline using TotalAlloc (monotonically increasing).
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	// Ingest 10k unique series.
	for i := 0; i < seriesCount; i++ {
		dp := makeNumberDP(map[string]string{
			"series_id": fmt.Sprintf("s_%05d", i),
		}, 1_000_000_000, float64(i))
		rms := makeProcGaugeRM(fmt.Sprintf("dsmem_%04d", i), dp)
		sampler.Process(rms)
	}

	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	totalAlloc := after.TotalAlloc - before.TotalAlloc
	bytesPerSeries := float64(totalAlloc) / float64(seriesCount)

	t.Logf("Total allocations: %d bytes for %d series (%.1f bytes/series)", totalAlloc, seriesCount, bytesPerSeries)

	if bytesPerSeries > 2000 {
		t.Errorf("Allocations per series %.1f bytes exceed 2000 byte threshold", bytesPerSeries)
	}
}

// TestPerf_AggregateFlushLatency measures the time to flush aggregate state
// with varying group counts.
func TestPerf_AggregateFlushLatency(t *testing.T) {
	for _, groupCount := range []int{1000, 10_000} {
		t.Run(fmt.Sprintf("groups_%d", groupCount), func(t *testing.T) {
			cfg := ProcessingConfig{
				Rules: []ProcessingRule{
					{
						Name:      "agg-flush",
						Input:     "flush_.*",
						Action:    ActionAggregate,
						Interval:  "1h", // Long interval so we can control flush.
						GroupBy:   []string{"group_id"},
						Functions: []string{"sum", "last"},
						Output:    "flush_agg",
					},
				},
			}
			if err := validateProcessingConfig(&cfg); err != nil {
				t.Fatal(err)
			}
			sampler, err := NewFromProcessing(cfg)
			if err != nil {
				t.Fatal(err)
			}

			// Ingest data to create groups.
			for i := 0; i < groupCount; i++ {
				dp := makeNumberDP(map[string]string{
					"group_id": fmt.Sprintf("g_%06d", i),
				}, 1_000_000_000, float64(i))
				rms := makeProcGaugeRM("flush_metric", dp)
				sampler.Process(rms)
			}

			// Measure flush by ingesting another batch that triggers processing.
			batch := make([]*metricspb.ResourceMetrics, 0, 100)
			for i := 0; i < 100; i++ {
				dp := makeNumberDP(map[string]string{
					"group_id": fmt.Sprintf("g_%06d", i),
				}, 2_000_000_000, float64(i*2))
				batch = append(batch, makeProcGaugeRM("flush_metric", dp)...)
			}

			// Warm up to stabilize.
			sampler.Process(batch)

			start := time.Now()
			for iter := 0; iter < 100; iter++ {
				sampler.Process(batch)
			}
			elapsed := time.Since(start)

			avgLatency := elapsed / 100
			t.Logf("%d groups: 100 ingestion rounds in %v (avg %v/round)", groupCount, elapsed, avgLatency)

			// With 10k groups, each round should complete under 5ms.
			if groupCount <= 10_000 && avgLatency > 5*time.Millisecond {
				t.Errorf("Average latency %v exceeds 5ms for %d groups", avgLatency, groupCount)
			}
		})
	}
}

// TestPerf_TransformOverhead measures the per-datapoint overhead of applying
// N transform operations.
func TestPerf_TransformOverhead(t *testing.T) {
	const batchSize = 100
	const iterations = 500

	for _, opCount := range []int{1, 5, 10, 20} {
		t.Run(fmt.Sprintf("ops_%d", opCount), func(t *testing.T) {
			ops := make([]Operation, 0, opCount)
			for i := 0; i < opCount; i++ {
				ops = append(ops, Operation{
					Set: &SetOp{Label: fmt.Sprintf("label_%d", i), Value: fmt.Sprintf("value_%d", i)},
				})
			}

			cfg := ProcessingConfig{
				Rules: []ProcessingRule{
					{
						Name:       "xform-overhead",
						Input:      "xform_.*",
						Action:     ActionTransform,
						Operations: ops,
					},
					{Name: "keep-all", Input: ".*", Action: ActionSample, Rate: 1.0, Method: "head"},
				},
			}
			if err := validateProcessingConfig(&cfg); err != nil {
				t.Fatal(err)
			}
			sampler, err := NewFromProcessing(cfg)
			if err != nil {
				t.Fatal(err)
			}

			batch := makePerfBatch(batchSize, "xform_metric", 1_000_000_000)

			// Warm up.
			for i := 0; i < 50; i++ {
				sampler.Process(batch)
			}

			start := time.Now()
			for i := 0; i < iterations; i++ {
				sampler.Process(batch)
			}
			elapsed := time.Since(start)

			totalDatapoints := float64(iterations * batchSize)
			nsPerDP := float64(elapsed.Nanoseconds()) / totalDatapoints
			nsPerOp := nsPerDP / float64(opCount)

			t.Logf("%d ops: %.1f ns/datapoint, %.1f ns/operation", opCount, nsPerDP, nsPerOp)
		})
	}

	// Also measure baseline (no transform) for overhead calculation.
	t.Run("baseline_no_transform", func(t *testing.T) {
		cfg := ProcessingConfig{
			Rules: []ProcessingRule{
				{Name: "keep-all", Input: ".*", Action: ActionSample, Rate: 1.0, Method: "head"},
			},
		}
		sampler, err := NewFromProcessing(cfg)
		if err != nil {
			t.Fatal(err)
		}

		batch := makePerfBatch(batchSize, "xform_metric", 1_000_000_000)

		for i := 0; i < 50; i++ {
			sampler.Process(batch)
		}

		start := time.Now()
		for i := 0; i < iterations; i++ {
			sampler.Process(batch)
		}
		elapsed := time.Since(start)

		nsPerDP := float64(elapsed.Nanoseconds()) / float64(iterations*batchSize)
		t.Logf("Baseline (no transform): %.1f ns/datapoint", nsPerDP)
	})
}

// TestPerf_GCPressure tracks GC pause times during sustained processing to
// detect allocation-heavy code paths.
func TestPerf_GCPressure(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "gc-xform",
				Input:  "gc_.*",
				Action: ActionTransform,
				Operations: []Operation{
					{Set: &SetOp{Label: "env", Value: "production"}},
					{Copy: &CopyOp{Source: "service", Target: "service_copy"}},
				},
			},
			{Name: "gc-drop-debug", Input: "debug_.*", Action: ActionDrop},
			{Name: "gc-keep", Input: ".*", Action: ActionSample, Rate: 1.0, Method: "head"},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		t.Fatal(err)
	}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	batch := makePerfBatch(200, "gc_metric", 1_000_000_000)

	// Warm up and stabilize GC.
	for i := 0; i < 100; i++ {
		sampler.Process(batch)
	}
	runtime.GC()

	// Reset GC stats.
	var gcStatsBefore debug.GCStats
	debug.ReadGCStats(&gcStatsBefore)
	numGCBefore := gcStatsBefore.NumGC

	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	// Sustained processing.
	const iterations = 2000
	start := time.Now()
	for i := 0; i < iterations; i++ {
		sampler.Process(batch)
	}
	elapsed := time.Since(start)

	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)

	var gcStatsAfter debug.GCStats
	debug.ReadGCStats(&gcStatsAfter)

	numGCDuring := gcStatsAfter.NumGC - numGCBefore
	totalAlloc := memAfter.TotalAlloc - memBefore.TotalAlloc
	allocPerIter := float64(totalAlloc) / float64(iterations)

	t.Logf("Sustained processing: %d iterations in %v", iterations, elapsed)
	t.Logf("GC collections during test: %d", numGCDuring)
	t.Logf("Total allocations: %d bytes (%.0f bytes/iteration)", totalAlloc, allocPerIter)

	// Compute max GC pause from recent pauses.
	var maxPause time.Duration
	if len(gcStatsAfter.Pause) > 0 {
		// gcStatsAfter.Pause contains the most recent pauses.
		count := int(numGCDuring)
		if count > len(gcStatsAfter.Pause) {
			count = len(gcStatsAfter.Pause)
		}
		for i := 0; i < count; i++ {
			if gcStatsAfter.Pause[i] > maxPause {
				maxPause = gcStatsAfter.Pause[i]
			}
		}
	}
	t.Logf("Max GC pause: %v", maxPause)

	// Max GC pause should be under 5ms for a processing workload.
	if maxPause > 5*time.Millisecond {
		t.Errorf("Max GC pause %v exceeds 5ms threshold", maxPause)
	}

	// Per-iteration allocation should be bounded — under 200KB per iteration
	// for 200 series with 3 rules including transform operations that clone attributes.
	if allocPerIter > 200_000 {
		t.Errorf("Allocations per iteration %.0f bytes exceed 200KB threshold", allocPerIter)
	}
}

// TestPerf_ScalingVsRuleCount measures throughput degradation as rule count
// increases from 1 to 100.
func TestPerf_ScalingVsRuleCount(t *testing.T) {
	const batchSize = 50
	const iterations = 200

	ruleCounts := []int{1, 10, 50, 100}
	throughputs := make([]float64, len(ruleCounts))

	for idx, ruleCount := range ruleCounts {
		t.Run(fmt.Sprintf("rules_%d", ruleCount), func(t *testing.T) {
			rules := make([]ProcessingRule, 0, ruleCount)
			for i := 0; i < ruleCount; i++ {
				// Each rule matches a different prefix so they all get evaluated.
				rules = append(rules, ProcessingRule{
					Name:   fmt.Sprintf("rule_%03d", i),
					Input:  fmt.Sprintf("nomatch_%03d_.*", i),
					Action: ActionDrop,
				})
			}
			// Add a final catch-all to keep metrics.
			rules = append(rules, ProcessingRule{
				Name:   "catch-all",
				Input:  ".*",
				Action: ActionSample,
				Rate:   1.0,
				Method: "head",
			})

			cfg := ProcessingConfig{Rules: rules}
			if err := validateProcessingConfig(&cfg); err != nil {
				t.Fatal(err)
			}
			sampler, err := NewFromProcessing(cfg)
			if err != nil {
				t.Fatal(err)
			}

			batch := makePerfBatch(batchSize, "scale_metric", 1_000_000_000)

			// Warm up.
			for i := 0; i < 50; i++ {
				sampler.Process(batch)
			}

			start := time.Now()
			for i := 0; i < iterations; i++ {
				sampler.Process(batch)
			}
			elapsed := time.Since(start)

			throughput := float64(iterations*batchSize) / elapsed.Seconds()
			throughputs[idx] = throughput

			t.Logf("%d rules: %.0f series/sec (%v for %d iterations)", ruleCount, throughput, elapsed, iterations)
		})
	}

	// Verify scaling: 100 rules should not be more than 200x slower than 1 rule.
	// This is generous — expected linear scaling would be ~100x.
	if len(throughputs) == len(ruleCounts) && throughputs[0] > 0 && throughputs[len(throughputs)-1] > 0 {
		ratio := throughputs[0] / throughputs[len(throughputs)-1]
		scaleFactor := float64(ruleCounts[len(ruleCounts)-1]) / float64(ruleCounts[0])
		t.Logf("Scaling: 1-rule throughput / %d-rule throughput = %.1fx (expected ~%.0fx for linear)",
			ruleCounts[len(ruleCounts)-1], ratio, scaleFactor)

		// Allow 2x overhead beyond linear scaling.
		if ratio > scaleFactor*2 && !math.IsInf(ratio, 0) {
			t.Errorf("Scaling %.1fx exceeds %.0fx threshold (linear*2)", ratio, scaleFactor*2)
		}
	}
}
