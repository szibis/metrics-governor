package limits

import (
	"fmt"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// --- Race condition tests ---
// Run with -race to detect data races in the limits enforcer.
// These simulate production-like concurrent access patterns.

func newRaceEnforcer(rules []Rule, dryRun bool) *Enforcer {
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules:    rules,
	}
	LoadConfigFromStruct(cfg)
	return NewEnforcer(cfg, dryRun, 1000)
}

func TestRace_Enforcer_ConcurrentProcess(t *testing.T) {
	enforcer := newRaceEnforcer([]Rule{
		{
			Name:           "test-rule",
			Match:          RuleMatch{MetricName: "http_.*"},
			MaxCardinality: 100,
			Action:         ActionLog,
			GroupBy:        []string{"service"},
		},
	}, false)
	defer enforcer.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				rm := createTestResourceMetrics(
					map[string]string{"service": fmt.Sprintf("svc-%d", id%4)},
					[]*metricspb.Metric{
						createTestMetric(fmt.Sprintf("http_requests_%d", j%10),
							map[string]string{"method": "GET", "path": fmt.Sprintf("/api/%d", j)}, 2),
					},
				)
				enforcer.Process([]*metricspb.ResourceMetrics{rm})
			}
		}(i)
	}

	wg.Wait()
}

func TestRace_Enforcer_ProcessWithMetricsEndpoint(t *testing.T) {
	enforcer := newRaceEnforcer([]Rule{
		{
			Name:              "card-rule",
			Match:             RuleMatch{MetricName: ".*"},
			MaxCardinality:    50,
			MaxDatapointsRate: 500,
			Action:            ActionAdaptive,
			GroupBy:           []string{"service", "env"},
		},
	}, false)
	defer enforcer.Stop()

	var wg sync.WaitGroup

	// Processors
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				rm := createTestResourceMetrics(
					map[string]string{"service": fmt.Sprintf("svc-%d", id), "env": "prod"},
					[]*metricspb.Metric{
						createTestMetric("api_latency",
							map[string]string{"endpoint": fmt.Sprintf("/ep%d", j)}, 1),
					},
				)
				enforcer.Process([]*metricspb.ResourceMetrics{rm})
			}
		}(i)
	}

	// Metrics readers (ServeHTTP)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest("GET", "/metrics", nil)
				enforcer.ServeHTTP(rec, req)
				runtime.Gosched()
			}
		}()
	}

	wg.Wait()
}

func TestRace_Enforcer_ProcessWithCacheClear(t *testing.T) {
	enforcer := newRaceEnforcer([]Rule{
		{
			Name:           "cache-rule",
			Match:          RuleMatch{MetricName: "http_.*"},
			MaxCardinality: 100,
			Action:         ActionLog,
		},
	}, false)
	defer enforcer.Stop()

	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 300; j++ {
				rm := createTestResourceMetrics(
					map[string]string{"service": "api"},
					[]*metricspb.Metric{
						createTestMetric(fmt.Sprintf("http_request_%d", j%50),
							map[string]string{"method": "GET"}, 1),
					},
				)
				enforcer.Process([]*metricspb.ResourceMetrics{rm})
			}
		}(i)
	}

	// Concurrent cache clears
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 50; j++ {
			enforcer.ClearRuleCache()
			runtime.Gosched()
		}
	}()

	wg.Wait()
}

func TestRace_Enforcer_DropActionConcurrent(t *testing.T) {
	enforcer := newRaceEnforcer([]Rule{
		{
			Name:           "drop-rule",
			Match:          RuleMatch{MetricName: "test_.*"},
			MaxCardinality: 10,
			Action:         ActionDrop,
			GroupBy:        []string{"service"},
		},
	}, false)
	defer enforcer.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				rm := createTestResourceMetrics(
					map[string]string{"service": "api"},
					[]*metricspb.Metric{
						createTestMetric(fmt.Sprintf("test_metric_%d", j),
							map[string]string{"pod": fmt.Sprintf("pod-%d", j)}, 3),
					},
				)
				enforcer.Process([]*metricspb.ResourceMetrics{rm})
			}
		}(i)
	}

	wg.Wait()
}

func TestRace_Enforcer_DryRunVsEnforcement(t *testing.T) {
	// Create enforcers sequentially to avoid racing on LoadConfigFromStruct
	// which compiles regex in-place on the shared rules slice.
	makeRules := func() []Rule {
		return []Rule{
			{
				Name:           "test-rule",
				Match:          RuleMatch{MetricName: "test_.*"},
				MaxCardinality: 10,
				Action:         ActionDrop,
				GroupBy:        []string{"service"},
			},
		}
	}

	enforcerDryRun := newRaceEnforcer(makeRules(), true)
	defer enforcerDryRun.Stop()

	enforcerEnforce := newRaceEnforcer(makeRules(), false)
	defer enforcerEnforce.Stop()

	var wg sync.WaitGroup
	for _, enforcer := range []*Enforcer{enforcerDryRun, enforcerEnforce} {
		wg.Add(1)
		go func(e *Enforcer) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				rm := createTestResourceMetrics(
					map[string]string{"service": "api"},
					[]*metricspb.Metric{
						createTestMetric(fmt.Sprintf("test_metric_%d", j),
							map[string]string{"pod": fmt.Sprintf("pod-%d", j)}, 3),
					},
				)
				e.Process([]*metricspb.ResourceMetrics{rm})
			}
		}(enforcer)
	}

	wg.Wait()
}

func TestRace_RuleCache_ConcurrentGetPut(t *testing.T) {
	cache := newRuleCache(100)
	rule := &Rule{Name: "test"}

	var wg sync.WaitGroup

	// Writers
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				cache.Put(fmt.Sprintf("metric_%d_%d", id, j%50), rule)
			}
		}(i)
	}

	// Readers
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				cache.Get(fmt.Sprintf("metric_%d_%d", id, j%50))
			}
		}(i)
	}

	// Size readers
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 200; j++ {
			cache.Size()
			cache.NegativeEntries()
		}
	}()

	// Clearers
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 20; j++ {
			cache.ClearCache()
			runtime.Gosched()
		}
	}()

	wg.Wait()
}

func TestRace_RuleCache_EvictionUnderContention(t *testing.T) {
	cache := newRuleCache(10) // Small cache to force frequent evictions
	rule := &Rule{Name: "evict-test"}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				key := fmt.Sprintf("metric_%d_%d", id, j)
				cache.Put(key, rule)
				cache.Get(key)
			}
		}(i)
	}

	wg.Wait()

	if cache.Size() > 10 {
		t.Errorf("Cache size %d exceeds max %d", cache.Size(), 10)
	}
}

func TestRace_ViolationMetrics_ConcurrentIncrement(t *testing.T) {
	vm := &ViolationMetrics{
		datapointsExceeded:  make(map[string]*atomic.Int64),
		cardinalityExceeded: make(map[string]*atomic.Int64),
		datapointsDropped:   make(map[string]*atomic.Int64),
		datapointsPassed:    make(map[string]*atomic.Int64),
		groupsDropped:       make(map[string]*atomic.Int64),
	}

	rules := []string{"rule-a", "rule-b", "rule-c"}

	// Initialize counters
	for _, rule := range rules {
		vm.datapointsExceeded[rule] = &atomic.Int64{}
		vm.cardinalityExceeded[rule] = &atomic.Int64{}
		vm.datapointsDropped[rule] = &atomic.Int64{}
		vm.datapointsPassed[rule] = &atomic.Int64{}
		vm.groupsDropped[rule] = &atomic.Int64{}
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				rule := rules[j%len(rules)]
				vm.mu.RLock()
				vm.datapointsExceeded[rule].Add(1)
				vm.cardinalityExceeded[rule].Add(1)
				vm.datapointsDropped[rule].Add(int64(id))
				vm.datapointsPassed[rule].Add(1)
				vm.mu.RUnlock()
			}
		}(i)
	}

	wg.Wait()
}

func TestRace_LogAggregator_ConcurrentAddFlush(t *testing.T) {
	la := NewLogAggregator(50 * time.Millisecond)
	defer la.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				la.Warn(
					fmt.Sprintf("key-%d-%d", id, j%20),
					"test warning",
					map[string]interface{}{"id": id, "iter": j},
					int64(j),
				)
				la.Info(
					fmt.Sprintf("info-%d-%d", id, j%10),
					"test info",
					map[string]interface{}{"id": id},
					int64(j),
				)
			}
		}(i)
	}

	wg.Wait()
}

func TestRace_LogAggregator_StopDuringFlush(t *testing.T) {
	for iter := 0; iter < 20; iter++ {
		la := NewLogAggregator(10 * time.Millisecond)

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				la.Warn("key", "msg", map[string]interface{}{"i": j}, int64(j))
			}
		}()

		time.Sleep(5 * time.Millisecond)
		la.Stop()
		wg.Wait()
	}
}

// --- Memory leak tests ---

func TestMemLeak_RuleCache_EvictionReclaims(t *testing.T) {
	cache := newRuleCache(100)
	rule := &Rule{Name: "leak-test"}

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	for i := 0; i < 100000; i++ {
		cache.Put(fmt.Sprintf("metric_name_with_long_path_%d", i), rule)
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("RuleCache eviction: heap_before=%dKB, heap_after=%dKB, size=%d, evictions=%d",
		heapBefore/1024, heapAfter/1024, cache.Size(), cache.evictions.Load())

	if cache.Size() != 100 {
		t.Errorf("Cache should be at max size 100, got %d", cache.Size())
	}

	if heapAfter > heapBefore+20*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after 100K evictions",
			heapBefore/1024, heapAfter/1024)
	}
}

func TestMemLeak_LogAggregator_FlushReclaims(t *testing.T) {
	la := NewLogAggregator(50 * time.Millisecond)
	defer la.Stop()

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	for cycle := 0; cycle < 50; cycle++ {
		for i := 0; i < 200; i++ {
			la.Warn(
				fmt.Sprintf("cycle%d-key%d", cycle, i),
				"leak test warning",
				map[string]interface{}{"cycle": cycle, "iter": i},
				int64(i),
			)
		}
		time.Sleep(60 * time.Millisecond)
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(100 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("LogAggregator flush: heap_before=%dKB, heap_after=%dKB",
		heapBefore/1024, heapAfter/1024)

	if heapAfter > heapBefore+20*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after 50 flush cycles",
			heapBefore/1024, heapAfter/1024)
	}
}

func TestMemLeak_Enforcer_WindowResets(t *testing.T) {
	enforcer := newRaceEnforcer([]Rule{
		{
			Name:           "window-rule",
			Match:          RuleMatch{MetricName: ".*"},
			MaxCardinality: 1000,
			Action:         ActionLog,
			GroupBy:        []string{"service"},
		},
	}, false)
	defer enforcer.Stop()

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	for cycle := 0; cycle < 5; cycle++ {
		for j := 0; j < 500; j++ {
			rm := createTestResourceMetrics(
				map[string]string{"service": fmt.Sprintf("svc-%d", j%5)},
				[]*metricspb.Metric{
					createTestMetric(fmt.Sprintf("metric_%d_%d", cycle, j),
						map[string]string{"pod": fmt.Sprintf("p%d", j)}, 2),
				},
			)
			enforcer.Process([]*metricspb.ResourceMetrics{rm})
		}
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("Enforcer processing: heap_before=%dKB, heap_after=%dKB",
		heapBefore/1024, heapAfter/1024)

	if heapAfter > heapBefore+50*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB",
			heapBefore/1024, heapAfter/1024)
	}
}
