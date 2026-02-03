package limits

import (
	"fmt"
	"sync"
	"testing"
)

func TestRuleCache_BasicHitMiss(t *testing.T) {
	cache := newRuleCache(100)

	rule := &Rule{Name: "test-rule", Match: RuleMatch{MetricName: "http_requests"}}

	// Miss
	got, ok := cache.Get("http_requests")
	if ok {
		t.Error("expected miss for uncached key")
	}
	if got != nil {
		t.Error("expected nil rule on miss")
	}

	// Put
	cache.Put("http_requests", rule)

	// Hit
	got, ok = cache.Get("http_requests")
	if !ok {
		t.Error("expected hit for cached key")
	}
	if got != rule {
		t.Errorf("expected cached rule %v, got %v", rule, got)
	}

	// Verify counters
	if cache.hits.Load() != 1 {
		t.Errorf("expected 1 hit, got %d", cache.hits.Load())
	}
	if cache.misses.Load() != 1 {
		t.Errorf("expected 1 miss, got %d", cache.misses.Load())
	}
}

func TestRuleCache_LabelMatcherBypass(t *testing.T) {
	// Rules with label matchers should not be cached via findMatchingRule logic.
	// This tests that the cache itself can still store them if explicitly Put,
	// but the enforcer findMatchingRule integration avoids caching them.
	cfg := &Config{
		Rules: []Rule{
			{
				Name:              "label-rule",
				Match:             RuleMatch{MetricName: "http_.*", Labels: map[string]string{"env": "prod"}},
				MaxDatapointsRate: 1000,
				Action:            ActionLog,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 100)
	defer enforcer.Stop()

	// First call: should find rule and NOT cache it (has label matchers)
	rule := enforcer.findMatchingRule("http_requests", map[string]string{"env": "prod"})
	if rule == nil {
		t.Fatal("expected rule to match")
	}
	if rule.Name != "label-rule" {
		t.Errorf("expected rule 'label-rule', got '%s'", rule.Name)
	}

	// Cache should be empty because the rule has label matchers
	if enforcer.ruleMatchCache.Size() != 0 {
		t.Errorf("expected cache size 0 (label matchers bypass), got %d", enforcer.ruleMatchCache.Size())
	}
}

func TestRuleCache_NegativeCache(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{
				Name:  "only-http",
				Match: RuleMatch{MetricName: "http_.*"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 100)
	defer enforcer.Stop()

	// Look up a metric that doesn't match any rule
	rule := enforcer.findMatchingRule("grpc_requests", map[string]string{})
	if rule != nil {
		t.Error("expected no rule match")
	}

	// Should be negatively cached now
	if enforcer.ruleMatchCache.Size() != 1 {
		t.Errorf("expected cache size 1 (negative entry), got %d", enforcer.ruleMatchCache.Size())
	}
	if enforcer.ruleMatchCache.NegativeEntries() != 1 {
		t.Errorf("expected 1 negative entry, got %d", enforcer.ruleMatchCache.NegativeEntries())
	}

	// Second lookup should be a cache hit
	rule = enforcer.findMatchingRule("grpc_requests", map[string]string{})
	if rule != nil {
		t.Error("expected no rule match on cached negative")
	}
	if enforcer.ruleMatchCache.hits.Load() != 1 {
		t.Errorf("expected 1 cache hit, got %d", enforcer.ruleMatchCache.hits.Load())
	}
}

func TestRuleCache_NegativeCache_WithLabelMatchers(t *testing.T) {
	// When any rule has label matchers, negative results should NOT be cached
	// because different labels could produce different results.
	cfg := &Config{
		Rules: []Rule{
			{
				Name:  "label-rule",
				Match: RuleMatch{MetricName: "http_.*", Labels: map[string]string{"env": "prod"}},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 100)
	defer enforcer.Stop()

	// Look up a metric that doesn't match
	rule := enforcer.findMatchingRule("grpc_requests", map[string]string{})
	if rule != nil {
		t.Error("expected no rule match")
	}

	// Should NOT be cached because config has label matchers
	if enforcer.ruleMatchCache.Size() != 0 {
		t.Errorf("expected cache size 0 (label matchers prevent negative caching), got %d", enforcer.ruleMatchCache.Size())
	}
}

func TestRuleCache_LRUEviction(t *testing.T) {
	cache := newRuleCache(3)

	rule := &Rule{Name: "r"}

	cache.Put("a", rule)
	cache.Put("b", rule)
	cache.Put("c", rule)

	if cache.Size() != 3 {
		t.Fatalf("expected size 3, got %d", cache.Size())
	}

	// Adding a 4th should evict the LRU (which is "a")
	cache.Put("d", rule)

	if cache.Size() != 3 {
		t.Errorf("expected size 3 after eviction, got %d", cache.Size())
	}

	// "a" should be evicted
	if _, ok := cache.Get("a"); ok {
		t.Error("expected 'a' to be evicted")
	}

	// "b", "c", "d" should still be present
	for _, key := range []string{"b", "c", "d"} {
		if _, ok := cache.Get(key); !ok {
			t.Errorf("expected '%s' to be present", key)
		}
	}

	if cache.evictions.Load() != 1 {
		t.Errorf("expected 1 eviction, got %d", cache.evictions.Load())
	}
}

func TestRuleCache_LRUEviction_AccessOrder(t *testing.T) {
	cache := newRuleCache(3)

	rule := &Rule{Name: "r"}

	cache.Put("a", rule)
	cache.Put("b", rule)
	cache.Put("c", rule)

	// Access "a" to promote it
	cache.Get("a")

	// Add "d" - should evict "b" (now the LRU)
	cache.Put("d", rule)

	if _, ok := cache.Get("b"); ok {
		t.Error("expected 'b' to be evicted (was LRU after 'a' was promoted)")
	}
	if _, ok := cache.Get("a"); !ok {
		t.Error("expected 'a' to still be present (was promoted)")
	}
}

func TestRuleCache_BoundedSize(t *testing.T) {
	maxSize := 100
	cache := newRuleCache(maxSize)

	rule := &Rule{Name: "r"}

	// Add 1000 unique names
	for i := 0; i < 1000; i++ {
		cache.Put(fmt.Sprintf("metric_%d", i), rule)

		if cache.Size() > maxSize {
			t.Fatalf("cache size %d exceeded max %d at iteration %d", cache.Size(), maxSize, i)
		}
	}

	if cache.Size() != maxSize {
		t.Errorf("expected final size %d, got %d", maxSize, cache.Size())
	}

	if cache.evictions.Load() != int64(1000-maxSize) {
		t.Errorf("expected %d evictions, got %d", 1000-maxSize, cache.evictions.Load())
	}
}

func TestRuleCache_OOMProtection(t *testing.T) {
	// Simulate attacker-generated metric names
	maxSize := 100
	cache := newRuleCache(maxSize)

	// 100K unique attacker-generated names
	for i := 0; i < 100000; i++ {
		name := fmt.Sprintf("attacker_metric_%06d_payload", i)
		cache.Put(name, nil) // negative cache entries
	}

	if cache.Size() > maxSize {
		t.Errorf("cache size %d exceeded max %d", cache.Size(), maxSize)
	}

	// All entries should be negative
	if cache.NegativeEntries() != maxSize {
		t.Errorf("expected %d negative entries, got %d", maxSize, cache.NegativeEntries())
	}
}

func TestRuleCache_Concurrent(t *testing.T) {
	cache := newRuleCache(100)

	rule := &Rule{Name: "concurrent-rule"}

	var wg sync.WaitGroup
	goroutines := 10
	opsPerGoroutine := 1000

	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				key := fmt.Sprintf("metric_%d_%d", id, i%50)

				// Alternate between Put and Get
				if i%2 == 0 {
					cache.Put(key, rule)
				} else {
					cache.Get(key)
				}
			}
		}(g)
	}

	wg.Wait()

	// Verify cache is still consistent
	if cache.Size() > 100 {
		t.Errorf("cache size %d exceeded max 100 after concurrent access", cache.Size())
	}

	// Verify counters are non-negative
	if cache.hits.Load() < 0 || cache.misses.Load() < 0 || cache.evictions.Load() < 0 {
		t.Error("expected non-negative counters")
	}
}

func TestRuleCache_ClearCache(t *testing.T) {
	cache := newRuleCache(100)

	rule := &Rule{Name: "r"}

	for i := 0; i < 50; i++ {
		cache.Put(fmt.Sprintf("metric_%d", i), rule)
	}

	if cache.Size() != 50 {
		t.Fatalf("expected size 50 before clear, got %d", cache.Size())
	}

	cache.ClearCache()

	if cache.Size() != 0 {
		t.Errorf("expected size 0 after clear, got %d", cache.Size())
	}

	// Verify all entries are gone
	for i := 0; i < 50; i++ {
		if _, ok := cache.Get(fmt.Sprintf("metric_%d", i)); ok {
			t.Errorf("expected miss after clear for metric_%d", i)
		}
	}

	// Should be able to add new entries after clear
	cache.Put("new_metric", rule)
	if cache.Size() != 1 {
		t.Errorf("expected size 1 after adding post-clear, got %d", cache.Size())
	}
}

func TestRuleCache_NilCache(t *testing.T) {
	// Verify nil ruleCache is safe (no panics)
	var cache *ruleCache

	// All operations should be no-ops and not panic
	rule, ok := cache.Get("test")
	if ok {
		t.Error("expected false from nil cache Get")
	}
	if rule != nil {
		t.Error("expected nil rule from nil cache Get")
	}

	cache.Put("test", &Rule{Name: "r"}) // should not panic

	if cache.Size() != 0 {
		t.Error("expected size 0 from nil cache")
	}

	if cache.NegativeEntries() != 0 {
		t.Error("expected 0 negative entries from nil cache")
	}

	cache.ClearCache() // should not panic
}

func TestRuleCache_NilCacheFromZeroMaxSize(t *testing.T) {
	cache := newRuleCache(0)
	if cache != nil {
		t.Error("expected nil cache for maxSize=0")
	}

	cache = newRuleCache(-1)
	if cache != nil {
		t.Error("expected nil cache for maxSize=-1")
	}
}

func TestRuleCache_UpdateExisting(t *testing.T) {
	cache := newRuleCache(100)

	rule1 := &Rule{Name: "rule1"}
	rule2 := &Rule{Name: "rule2"}

	cache.Put("metric", rule1)

	got, ok := cache.Get("metric")
	if !ok || got.Name != "rule1" {
		t.Error("expected rule1")
	}

	// Update with different rule
	cache.Put("metric", rule2)

	got, ok = cache.Get("metric")
	if !ok || got.Name != "rule2" {
		t.Error("expected rule2 after update")
	}

	// Size should still be 1 (updated, not added)
	if cache.Size() != 1 {
		t.Errorf("expected size 1 after update, got %d", cache.Size())
	}
}

func TestRuleCache_EnforcerIntegration_CacheHit(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{
				Name:              "http-rule",
				Match:             RuleMatch{MetricName: "http_.*"},
				MaxDatapointsRate: 100000,
				Action:            ActionLog,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 100)
	defer enforcer.Stop()

	// First call: cache miss, should populate cache
	rule := enforcer.findMatchingRule("http_requests", map[string]string{})
	if rule == nil || rule.Name != "http-rule" {
		t.Fatal("expected http-rule to match")
	}

	if enforcer.ruleMatchCache.misses.Load() != 1 {
		t.Errorf("expected 1 miss on first lookup, got %d", enforcer.ruleMatchCache.misses.Load())
	}

	// Second call: cache hit
	rule = enforcer.findMatchingRule("http_requests", map[string]string{})
	if rule == nil || rule.Name != "http-rule" {
		t.Fatal("expected http-rule from cache")
	}

	if enforcer.ruleMatchCache.hits.Load() != 1 {
		t.Errorf("expected 1 hit on second lookup, got %d", enforcer.ruleMatchCache.hits.Load())
	}
}

func TestRuleCache_EnforcerClearRuleCache(t *testing.T) {
	cfg := &Config{
		Rules: []Rule{
			{
				Name:  "rule1",
				Match: RuleMatch{MetricName: "test_.*"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 100)
	defer enforcer.Stop()

	// Populate cache
	enforcer.findMatchingRule("test_metric", map[string]string{})
	if enforcer.ruleMatchCache.Size() != 1 {
		t.Fatal("expected cache to have 1 entry")
	}

	// Clear
	enforcer.ClearRuleCache()
	if enforcer.ruleMatchCache.Size() != 0 {
		t.Error("expected cache to be empty after ClearRuleCache")
	}
}
