package limits

import (
	"fmt"
	"runtime"
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

func TestResourceUsage_EnforcerWithRuleCache(t *testing.T) {
	const (
		totalMetrics  = 100000
		batchSize     = 100
		ruleCacheSize = 10000
	)

	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "high-volume-rule",
				Match:             RuleMatch{MetricName: "test_.*"},
				MaxDatapointsRate: int64(totalMetrics * 10), // High limit so nothing is dropped
				MaxCardinality:    int64(totalMetrics * 10),
				Action:            ActionLog,
				GroupBy:           []string{"service"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, ruleCacheSize)
	defer enforcer.Stop()

	// Pre-create all batches to avoid measuring metric creation allocations
	numBatches := totalMetrics / batchSize
	batches := make([][]*metricspb.ResourceMetrics, numBatches)
	for i := 0; i < numBatches; i++ {
		metrics := make([]*metricspb.Metric, batchSize)
		for j := 0; j < batchSize; j++ {
			idx := i*batchSize + j
			metrics[j] = createTestMetric(
				fmt.Sprintf("test_metric_%d", idx),
				map[string]string{
					"instance": fmt.Sprintf("host-%d", idx%100),
				},
				1,
			)
		}
		batches[i] = []*metricspb.ResourceMetrics{
			createTestResourceMetrics(
				map[string]string{"service": fmt.Sprintf("svc-%d", i%10)},
				metrics,
			),
		}
	}

	// Warm up: process a few batches to initialize internal structures
	for i := 0; i < 10; i++ {
		warmupMetrics := []*metricspb.Metric{
			createTestMetric("test_warmup", map[string]string{}, 1),
		}
		warmupRM := []*metricspb.ResourceMetrics{
			createTestResourceMetrics(map[string]string{"service": "warmup"}, warmupMetrics),
		}
		enforcer.Process(warmupRM)
	}

	var mBefore, mAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&mBefore)

	processedTotal := 0
	for _, batch := range batches {
		result := enforcer.Process(batch)
		processedTotal += len(result)
	}

	runtime.GC()
	runtime.ReadMemStats(&mAfter)

	totalAllocs := mAfter.TotalAlloc - mBefore.TotalAlloc
	allocsPerMetric := totalAllocs / uint64(totalMetrics)
	heapDelta := int64(mAfter.HeapInuse) - int64(mBefore.HeapInuse)

	t.Logf("Processed %d metrics in %d batches", totalMetrics, numBatches)
	t.Logf("Passed through: %d batches", processedTotal)
	t.Logf("Total allocations: %d bytes (%d MB)", totalAllocs, totalAllocs/(1024*1024))
	t.Logf("Allocs per metric: %d bytes", allocsPerMetric)
	t.Logf("GC cycles: %d", mAfter.NumGC-mBefore.NumGC)
	t.Logf("Heap delta: %d bytes (%d KB)", heapDelta, heapDelta/1024)
	t.Logf("Heap in use: before=%d after=%d", mBefore.HeapInuse, mAfter.HeapInuse)
	t.Logf("Rule cache size: %d", enforcer.ruleMatchCache.Size())

	// Per-metric allocation includes protobuf copies, map allocations for
	// attribute extraction, series key building, cardinality tracking, and
	// label injection. With the race detector, allocations are higher due
	// to sync.Pool being disabled. Allow up to 20KB per metric.
	maxAllocsPerMetric := uint64(20 * 1024)
	if allocsPerMetric > maxAllocsPerMetric {
		t.Errorf("Per-metric allocation budget exceeded: %d > %d bytes/metric",
			allocsPerMetric, maxAllocsPerMetric)
	}

	// Heap growth should be bounded even after processing 100K metrics.
	// The rule cache is bounded and old window stats are reset.
	// Allow up to 200MB for heap growth (includes cardinality tracker overhead).
	maxHeapGrowth := int64(200 * 1024 * 1024)
	if heapDelta > maxHeapGrowth {
		t.Errorf("Heap grew by %d bytes (%d MB), exceeds %d MB limit",
			heapDelta, heapDelta/(1024*1024), maxHeapGrowth/(1024*1024))
	}
}

func TestResourceUsage_EnforcerCacheMemoryBounded(t *testing.T) {
	const (
		uniqueMetrics = 50000
		ruleCacheSize = 1000 // Small cache to test eviction
	)

	// Use a rule that does NOT create cardinality trackers (no GroupBy,
	// no MaxCardinality) so we isolate the cache memory behavior.
	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules:    []Rule{}, // No rules match - tests the negative cache path
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, ruleCacheSize)
	defer enforcer.Stop()

	// Warm up
	for i := 0; i < 100; i++ {
		m := []*metricspb.Metric{
			createTestMetric(fmt.Sprintf("metric_warmup_%d", i), map[string]string{}, 1),
		}
		rm := []*metricspb.ResourceMetrics{
			createTestResourceMetrics(map[string]string{"service": "warmup"}, m),
		}
		enforcer.Process(rm)
	}

	var mBefore, mAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&mBefore)

	// Process many unique metric names to trigger cache evictions
	for i := 0; i < uniqueMetrics; i++ {
		m := []*metricspb.Metric{
			createTestMetric(fmt.Sprintf("metric_%d", i), map[string]string{}, 1),
		}
		rm := []*metricspb.ResourceMetrics{
			createTestResourceMetrics(map[string]string{"service": "test"}, m),
		}
		enforcer.Process(rm)
	}

	runtime.GC()
	runtime.ReadMemStats(&mAfter)

	heapDelta := int64(mAfter.HeapInuse) - int64(mBefore.HeapInuse)
	cacheSize := enforcer.ruleMatchCache.Size()
	totalAllocs := mAfter.TotalAlloc - mBefore.TotalAlloc
	allocsPerMetric := totalAllocs / uint64(uniqueMetrics)

	t.Logf("Processed %d unique metric names with cache size %d", uniqueMetrics, ruleCacheSize)
	t.Logf("Final cache entries: %d", cacheSize)
	t.Logf("Cache evictions: %d", enforcer.ruleMatchCache.evictions.Load())
	t.Logf("Cache hits: %d, misses: %d",
		enforcer.ruleMatchCache.hits.Load(), enforcer.ruleMatchCache.misses.Load())
	t.Logf("Total allocations: %d bytes (%d MB)", totalAllocs, totalAllocs/(1024*1024))
	t.Logf("Allocs per metric: %d bytes", allocsPerMetric)
	t.Logf("Heap delta: %d bytes (%d KB)", heapDelta, heapDelta/1024)

	// Cache size should never exceed the configured maximum
	if cacheSize > ruleCacheSize {
		t.Errorf("Cache size %d exceeds configured max %d", cacheSize, ruleCacheSize)
	}

	// With evictions working properly, cache evictions should be at least
	// uniqueMetrics - ruleCacheSize (minus warmup entries already evicted)
	expectedMinEvictions := int64(uniqueMetrics - ruleCacheSize)
	actualEvictions := enforcer.ruleMatchCache.evictions.Load()
	if actualEvictions < expectedMinEvictions {
		t.Errorf("Expected at least %d evictions, got %d", expectedMinEvictions, actualEvictions)
	}

	// Without cardinality trackers (no matching rules), memory should be bounded
	// by the cache size, not the number of unique metrics processed.
	// Allow up to 100MB for 50K metrics with race detector overhead.
	maxHeapGrowth := int64(100 * 1024 * 1024)
	if heapDelta > maxHeapGrowth {
		t.Errorf("Heap grew by %d bytes (%d MB), exceeds %d MB limit",
			heapDelta, heapDelta/(1024*1024), maxHeapGrowth/(1024*1024))
	}
}

func TestResourceUsage_EnforcerNoRulePassthrough(t *testing.T) {
	const totalMetrics = 50000

	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:  "only-http",
				Match: RuleMatch{MetricName: "http_.*"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 5000)
	defer enforcer.Stop()

	// Pre-create metrics that don't match any rule (no "http_" prefix)
	batches := make([][]*metricspb.ResourceMetrics, totalMetrics)
	for i := 0; i < totalMetrics; i++ {
		batches[i] = []*metricspb.ResourceMetrics{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "api"}}},
					},
				},
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{
								Name: fmt.Sprintf("grpc_request_%d", i),
								Data: &metricspb.Metric_Gauge{
									Gauge: &metricspb.Gauge{
										DataPoints: []*metricspb.NumberDataPoint{
											{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 1.0}},
										},
									},
								},
							},
						},
					},
				},
			},
		}
	}

	var mBefore, mAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&mBefore)

	passedCount := 0
	for _, batch := range batches {
		result := enforcer.Process(batch)
		passedCount += len(result)
	}

	runtime.GC()
	runtime.ReadMemStats(&mAfter)

	totalAllocs := mAfter.TotalAlloc - mBefore.TotalAlloc
	allocsPerMetric := totalAllocs / uint64(totalMetrics)

	t.Logf("Processed %d non-matching metrics", totalMetrics)
	t.Logf("Passed through: %d", passedCount)
	t.Logf("Total allocations: %d bytes", totalAllocs)
	t.Logf("Allocs per metric: %d bytes", allocsPerMetric)
	t.Logf("GC cycles: %d", mAfter.NumGC-mBefore.NumGC)

	// Non-matching metrics incur attribute extraction, rule regex matching,
	// slice allocation for result building, and cache lookups.
	// With race detector overhead, allow up to 20KB per metric.
	maxAllocsPerMetric := uint64(20 * 1024)
	if allocsPerMetric > maxAllocsPerMetric {
		t.Errorf("Per-metric allocation budget exceeded for passthrough: %d > %d bytes/metric",
			allocsPerMetric, maxAllocsPerMetric)
	}

	// All metrics should pass through
	if passedCount != totalMetrics {
		t.Errorf("Expected %d metrics to pass through, got %d", totalMetrics, passedCount)
	}
}
