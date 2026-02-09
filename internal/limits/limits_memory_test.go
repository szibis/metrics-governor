package limits

import (
	"fmt"
	"runtime"
	"testing"
)

// TestPerf_LimitsMemoryPerRule measures memory overhead per limits rule.
func TestPerf_LimitsMemoryPerRule(t *testing.T) {
	for _, ruleCount := range []int{10, 100, 500, 1000} {
		t.Run(fmt.Sprintf("rules_%d", ruleCount), func(t *testing.T) {
			cfg := makeScaleConfig(ruleCount)

			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)

			enforcer := NewEnforcer(cfg, false, 0)
			_ = enforcer

			runtime.GC()
			var after runtime.MemStats
			runtime.ReadMemStats(&after)

			totalAllocDelta := after.TotalAlloc - before.TotalAlloc
			bytesPerRule := float64(totalAllocDelta) / float64(ruleCount)

			t.Logf("%d rules: total alloc=%d bytes (%.0f bytes/rule)", ruleCount, totalAllocDelta, bytesPerRule)

			// Each rule should use less than 10KB.
			if bytesPerRule > 10_000 {
				t.Errorf("Memory per rule %.0f bytes exceeds 10KB threshold", bytesPerRule)
			}
		})
	}
}

// TestPerf_CacheMemoryAtScale measures LRU cache memory at different sizes.
func TestPerf_CacheMemoryAtScale(t *testing.T) {
	for _, cacheSize := range []int{1000, 10000, 50000} {
		t.Run(fmt.Sprintf("cache_%d", cacheSize), func(t *testing.T) {
			cache := newRuleCache(cacheSize)

			// Fill the cache with entries.
			for i := 0; i < cacheSize; i++ {
				name := fmt.Sprintf("metric_name_%06d", i)
				cache.Put(name, nil) // negative cache entries
			}

			estimated := cache.EstimatedMemoryBytes()
			actualSize := cache.Size()

			t.Logf("Cache size=%d, entries=%d, estimated memory=%d bytes (%.0f bytes/entry)",
				cacheSize, actualSize, estimated, float64(estimated)/float64(actualSize))

			// Verify cache is at expected capacity.
			if actualSize != cacheSize {
				t.Errorf("Expected cache size %d, got %d", cacheSize, actualSize)
			}

			// Estimated memory should be reasonable: ~200 bytes per entry + key.
			bytesPerEntry := float64(estimated) / float64(actualSize)
			if bytesPerEntry > 500 {
				t.Errorf("Estimated %.0f bytes/entry exceeds 500 byte threshold", bytesPerEntry)
			}
		})
	}
}
