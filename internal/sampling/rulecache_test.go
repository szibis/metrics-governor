package sampling

import (
	"fmt"
	"sync"
	"testing"
)

func TestRuleCache_Hit(t *testing.T) {
	c := newProcessingRuleCache(100)
	c.Put("cpu_usage", []int{0, 3})

	indices, ok := c.Get("cpu_usage")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if len(indices) != 2 || indices[0] != 0 || indices[1] != 3 {
		t.Errorf("expected [0,3], got %v", indices)
	}
}

func TestRuleCache_Miss(t *testing.T) {
	c := newProcessingRuleCache(100)

	indices, ok := c.Get("unknown_metric")
	if ok {
		t.Fatal("expected cache miss")
	}
	if indices != nil {
		t.Errorf("expected nil indices on miss, got %v", indices)
	}
}

func TestRuleCache_EmptyIndices(t *testing.T) {
	// A metric that matches no rules should cache an empty (nil) slice.
	c := newProcessingRuleCache(100)
	c.Put("no_match", nil)

	indices, ok := c.Get("no_match")
	if !ok {
		t.Fatal("expected cache hit for empty indices")
	}
	if len(indices) != 0 {
		t.Errorf("expected empty indices, got %v", indices)
	}
}

func TestRuleCache_Eviction(t *testing.T) {
	const capacity = 10
	c := newProcessingRuleCache(capacity)

	// Fill beyond capacity.
	for i := 0; i < capacity+5; i++ {
		c.Put(fmt.Sprintf("metric_%d", i), []int{i})
	}

	// Size should not exceed capacity.
	if c.Size() > capacity {
		t.Errorf("cache size %d exceeds capacity %d", c.Size(), capacity)
	}

	// Oldest entries should be evicted.
	_, ok := c.Get("metric_0")
	if ok {
		t.Error("expected metric_0 to be evicted")
	}

	// Newest entries should be present.
	_, ok = c.Get("metric_14")
	if !ok {
		t.Error("expected metric_14 to be in cache")
	}

	if c.evictions.Load() == 0 {
		t.Error("expected eviction counter > 0")
	}
}

func TestRuleCache_ClearOnReload(t *testing.T) {
	c := newProcessingRuleCache(100)

	for i := 0; i < 10; i++ {
		c.Put(fmt.Sprintf("metric_%d", i), []int{i})
	}

	if c.Size() != 10 {
		t.Errorf("expected 10 entries, got %d", c.Size())
	}

	c.ClearCache()

	if c.Size() != 0 {
		t.Errorf("expected 0 entries after clear, got %d", c.Size())
	}

	// All entries should be gone.
	for i := 0; i < 10; i++ {
		_, ok := c.Get(fmt.Sprintf("metric_%d", i))
		if ok {
			t.Errorf("expected miss for metric_%d after clear", i)
		}
	}
}

func TestRuleCache_ConcurrentAccess(t *testing.T) {
	c := newProcessingRuleCache(1000)

	var wg sync.WaitGroup
	const goroutines = 10
	const ops = 1000

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				name := fmt.Sprintf("metric_%d_%d", id, i%50)
				c.Put(name, []int{id, i})
				c.Get(name)
			}
		}(g)
	}

	wg.Wait()

	// Just verify no panic/race — size should be reasonable.
	if c.Size() == 0 {
		t.Error("expected some entries after concurrent access")
	}
}

func TestRuleCache_NilSafe(t *testing.T) {
	var c *processingRuleCache

	// All methods should be safe on nil.
	indices, ok := c.Get("anything")
	if ok || indices != nil {
		t.Error("expected nil cache to return miss")
	}

	c.Put("anything", []int{1})
	c.ClearCache()

	if c.Size() != 0 {
		t.Error("expected nil cache size 0")
	}
	if c.EstimatedMemoryBytes() != 0 {
		t.Error("expected nil cache memory 0")
	}
}

func TestRuleCache_Integration_ProcessingRules(t *testing.T) {
	const ruleCount = 100

	rules := make([]ProcessingRule, 0, ruleCount+1)
	for i := 0; i < ruleCount; i++ {
		rules = append(rules, ProcessingRule{
			Name:   fmt.Sprintf("rule_%03d", i),
			Input:  fmt.Sprintf("match_%03d_.*", i),
			Action: ActionDrop,
		})
	}
	rules = append(rules, ProcessingRule{
		Name: "catch-all", Input: ".*", Action: ActionSample, Rate: 1.0, Method: "head",
	})

	cfg := ProcessingConfig{Rules: rules}
	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	batch := makePerfBatch(10, "integration_test", 1_000_000_000)

	// First call: cache miss (populates cache).
	result1 := sampler.Process(batch)

	// Second call: cache hit (should produce same result).
	result2 := sampler.Process(batch)

	// Results should be identical.
	if len(result1) != len(result2) {
		t.Errorf("cache hit produced different result: %d vs %d", len(result1), len(result2))
	}

	// Cache should have entries now.
	if sampler.ruleNameCache.Size() == 0 {
		t.Error("expected cache to be populated after processing")
	}

	// Verify hit counter increased.
	if sampler.ruleNameCache.hits.Load() == 0 {
		t.Error("expected cache hits after second call")
	}
}

func TestRuleCache_Update(t *testing.T) {
	c := newProcessingRuleCache(100)

	c.Put("metric_a", []int{0, 1})
	c.Put("metric_a", []int{2, 3}) // Update

	indices, ok := c.Get("metric_a")
	if !ok {
		t.Fatal("expected hit")
	}
	if len(indices) != 2 || indices[0] != 2 || indices[1] != 3 {
		t.Errorf("expected updated [2,3], got %v", indices)
	}

	// Size should still be 1 (update, not insert).
	if c.Size() != 1 {
		t.Errorf("expected size 1 after update, got %d", c.Size())
	}
}

func TestRuleCache_ObservabilityCounters(t *testing.T) {
	c := newProcessingRuleCache(100)

	c.Get("miss1")
	c.Get("miss2")
	c.Put("hit1", []int{0})
	c.Get("hit1")
	c.Get("hit1")

	if c.misses.Load() != 2 {
		t.Errorf("expected 2 misses, got %d", c.misses.Load())
	}
	if c.hits.Load() != 2 {
		t.Errorf("expected 2 hits, got %d", c.hits.Load())
	}
}
