package sampling

import (
	"fmt"
	"runtime"
	"testing"
)

// TestPerf_MemoryPerRule measures memory overhead per processing rule at scale.
// Each rule compiles a regex, allocates Prometheus counters, and holds dead-rule state.
func TestPerf_MemoryPerRule(t *testing.T) {
	for _, ruleCount := range []int{10, 100, 500, 1000} {
		t.Run(fmt.Sprintf("rules_%d", ruleCount), func(t *testing.T) {
			rules := make([]ProcessingRule, 0, ruleCount+1)
			for i := 0; i < ruleCount; i++ {
				rules = append(rules, ProcessingRule{
					Name:   fmt.Sprintf("rule_%04d", i),
					Input:  fmt.Sprintf("prefix_%04d_.*", i),
					Action: ActionDrop,
				})
			}
			rules = append(rules, ProcessingRule{
				Name: "catch-all", Input: ".*", Action: ActionSample, Rate: 1.0, Method: "head",
			})

			cfg := ProcessingConfig{Rules: rules}
			if err := validateProcessingConfig(&cfg); err != nil {
				t.Fatal(err)
			}

			// Force GC and measure baseline.
			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)

			sampler, err := NewFromProcessing(cfg)
			if err != nil {
				t.Fatal(err)
			}
			_ = sampler

			runtime.GC()
			var after runtime.MemStats
			runtime.ReadMemStats(&after)

			// Use HeapInuse for a rough approximation of retained memory.
			heapDelta := int64(after.HeapInuse) - int64(before.HeapInuse)
			totalAllocDelta := after.TotalAlloc - before.TotalAlloc

			// Subtract fixed cache overhead (~1.6MB for 50K-entry map) since it's a
			// one-time cost independent of rule count.
			const cacheFixedOverhead = 1_800_000
			adjusted := totalAllocDelta
			if adjusted > cacheFixedOverhead {
				adjusted -= cacheFixedOverhead
			}
			bytesPerRule := float64(adjusted) / float64(ruleCount)
			t.Logf("%d rules: heap delta=%d bytes, total alloc=%d bytes, adjusted=%d bytes (%.0f bytes/rule)",
				ruleCount, heapDelta, totalAllocDelta, adjusted, bytesPerRule)

			// Each rule should use less than 10KB (regex + Prometheus refs + dead-rule state).
			// The cache's fixed allocation is subtracted above.
			if bytesPerRule > 10_000 {
				t.Errorf("Memory per rule %.0f bytes exceeds 10KB threshold", bytesPerRule)
			}
		})
	}
}
