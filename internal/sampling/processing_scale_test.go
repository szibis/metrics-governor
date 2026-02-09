package sampling

import (
	"fmt"
	"math"
	"testing"
	"time"

	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// BenchmarkProcess_RuleScale measures processing throughput as rule count scales.
// Each sub-benchmark creates N non-matching rules plus a catch-all, simulating
// worst-case rule evaluation (every rule checked before the catch-all fires).
func BenchmarkProcess_RuleScale(b *testing.B) {
	ruleCounts := []int{10, 50, 100, 200, 500, 1000}

	for _, rc := range ruleCounts {
		b.Run(fmt.Sprintf("%d_nomatch", rc), func(b *testing.B) {
			rules := makeNonMatchingRules(rc)
			rules = append(rules, ProcessingRule{
				Name: "catch-all", Input: ".*", Action: ActionSample, Rate: 1.0, Method: "head",
			})

			cfg := ProcessingConfig{Rules: rules}
			if err := validateProcessingConfig(&cfg); err != nil {
				b.Fatal(err)
			}
			sampler, err := NewFromProcessing(cfg)
			if err != nil {
				b.Fatal(err)
			}

			batch := makeBenchBatch(50, 1, "scale_metric", 1_000_000_000)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sampler.Process(batch)
			}
		})
	}

	// Mixed action types: transform (non-terminal) + drop + sample catch-all.
	for _, rc := range []int{50, 200, 1000} {
		b.Run(fmt.Sprintf("%d_mixed", rc), func(b *testing.B) {
			rules := make([]ProcessingRule, 0, rc+1)
			for i := 0; i < rc; i++ {
				switch i % 3 {
				case 0:
					rules = append(rules, ProcessingRule{
						Name:   fmt.Sprintf("xform_%03d", i),
						Input:  fmt.Sprintf("nomatch_%03d_.*", i),
						Action: ActionTransform,
						Operations: []Operation{
							{Set: &SetOp{Label: "tag", Value: "val"}},
						},
					})
				case 1:
					rules = append(rules, ProcessingRule{
						Name:   fmt.Sprintf("drop_%03d", i),
						Input:  fmt.Sprintf("nomatch_%03d_.*", i),
						Action: ActionDrop,
					})
				case 2:
					rules = append(rules, ProcessingRule{
						Name:   fmt.Sprintf("sample_%03d", i),
						Input:  fmt.Sprintf("nomatch_%03d_.*", i),
						Action: ActionSample,
						Rate:   0.5,
						Method: "head",
					})
				}
			}
			rules = append(rules, ProcessingRule{
				Name: "catch-all", Input: ".*", Action: ActionSample, Rate: 1.0, Method: "head",
			})

			cfg := ProcessingConfig{Rules: rules}
			if err := validateProcessingConfig(&cfg); err != nil {
				b.Fatal(err)
			}
			sampler, err := NewFromProcessing(cfg)
			if err != nil {
				b.Fatal(err)
			}

			batch := makeBenchBatch(50, 1, "scale_metric", 1_000_000_000)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sampler.Process(batch)
			}
		})
	}

	// Rules with input_labels — forces attribute-level matching.
	for _, rc := range []int{100, 500} {
		b.Run(fmt.Sprintf("%d_labels", rc), func(b *testing.B) {
			rules := make([]ProcessingRule, 0, rc+1)
			for i := 0; i < rc; i++ {
				rules = append(rules, ProcessingRule{
					Name:        fmt.Sprintf("label_%03d", i),
					Input:       "scale_.*",
					InputLabels: map[string]string{"service": fmt.Sprintf("nomatch_svc_%03d", i)},
					Action:      ActionDrop,
				})
			}
			rules = append(rules, ProcessingRule{
				Name: "catch-all", Input: ".*", Action: ActionSample, Rate: 1.0, Method: "head",
			})

			cfg := ProcessingConfig{Rules: rules}
			if err := validateProcessingConfig(&cfg); err != nil {
				b.Fatal(err)
			}
			sampler, err := NewFromProcessing(cfg)
			if err != nil {
				b.Fatal(err)
			}

			batch := makeBenchBatch(50, 1, "scale_metric", 1_000_000_000)

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sampler.Process(batch)
			}
		})
	}
}

// TestPerf_ScalingVsRuleCount_Extended extends the existing scaling test to
// 1000 rules, establishing baseline performance at scale.
func TestPerf_ScalingVsRuleCount_Extended(t *testing.T) {
	const batchSize = 50
	const iterations = 200

	ruleCounts := []int{1, 10, 50, 100, 200, 500, 1000}
	throughputs := make([]float64, len(ruleCounts))

	for idx, ruleCount := range ruleCounts {
		t.Run(fmt.Sprintf("rules_%d", ruleCount), func(t *testing.T) {
			rules := makeNonMatchingRules(ruleCount)
			rules = append(rules, ProcessingRule{
				Name: "catch-all", Input: ".*", Action: ActionSample, Rate: 1.0, Method: "head",
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

	// Report scaling ratios.
	if throughputs[0] > 0 {
		for i := 1; i < len(ruleCounts); i++ {
			if throughputs[i] > 0 {
				ratio := throughputs[0] / throughputs[i]
				t.Logf("1-rule / %d-rule = %.1fx slowdown", ruleCounts[i], ratio)
			}
		}
	}

	// Verify scaling: 1000 rules should not be more than 2000x slower than 1 rule.
	if len(throughputs) == len(ruleCounts) && throughputs[0] > 0 && throughputs[len(throughputs)-1] > 0 {
		ratio := throughputs[0] / throughputs[len(throughputs)-1]
		scaleFactor := float64(ruleCounts[len(ruleCounts)-1]) / float64(ruleCounts[0])
		if ratio > scaleFactor*2 && !math.IsInf(ratio, 0) {
			t.Errorf("Scaling %.1fx exceeds %.0fx threshold (linear*2)", ratio, scaleFactor*2)
		}
	}
}

// makeNonMatchingRules creates N rules that won't match "scale_metric_*" names.
func makeNonMatchingRules(count int) []ProcessingRule {
	rules := make([]ProcessingRule, 0, count)
	for i := 0; i < count; i++ {
		rules = append(rules, ProcessingRule{
			Name:   fmt.Sprintf("rule_%04d", i),
			Input:  fmt.Sprintf("nomatch_%04d_.*", i),
			Action: ActionDrop,
		})
	}
	return rules
}

// makePerfBatchMultiDP creates a batch where each metric has multiple datapoints.
func makePerfBatchMultiDP(metricCount, dpPerMetric int, namePrefix string, tsBase uint64) []*metricspb.ResourceMetrics {
	rms := make([]*metricspb.ResourceMetrics, 0, metricCount)
	for m := 0; m < metricCount; m++ {
		name := fmt.Sprintf("%s_%04d", namePrefix, m)
		dps := make([]*metricspb.NumberDataPoint, 0, dpPerMetric)
		for d := 0; d < dpPerMetric; d++ {
			dps = append(dps, makeNumberDP(map[string]string{
				"service":  fmt.Sprintf("svc_%d", m%20),
				"instance": fmt.Sprintf("inst_%d", d%10),
				"env":      "production",
			}, tsBase+uint64(m*dpPerMetric+d)*1_000_000_000, float64(m*100+d)))
		}
		rms = append(rms, makeProcGaugeRM(name, dps...)...)
	}
	return rms
}
