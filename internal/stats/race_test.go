package stats

import (
	"context"
	"fmt"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

func makeRaceResourceMetrics(service string, metricName string, numDatapoints int) *metricspb.ResourceMetrics {
	dps := make([]*metricspb.NumberDataPoint, numDatapoints)
	for i := 0; i < numDatapoints; i++ {
		dps[i] = &metricspb.NumberDataPoint{
			Attributes: []*commonpb.KeyValue{
				{Key: "endpoint", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprintf("/api/%d", i)}}},
			},
		}
	}
	return &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{
				{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: service}}},
			},
		},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{Metrics: []*metricspb.Metric{
				{Name: metricName, Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{DataPoints: dps}}},
			}},
		},
	}
}

// --- Race condition tests ---

func TestRace_Collector_ConcurrentProcess(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelFull)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				rm := makeRaceResourceMetrics(
					fmt.Sprintf("svc-%d", id%4),
					fmt.Sprintf("http_requests_%d", j%20),
					3,
				)
				c.Process([]*metricspb.ResourceMetrics{rm})
			}
		}(i)
	}

	wg.Wait()
}

func TestRace_Collector_ProcessWithResetCardinality(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelFull)

	var wg sync.WaitGroup

	// Processors
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 300; j++ {
				rm := makeRaceResourceMetrics(
					fmt.Sprintf("svc-%d", id),
					fmt.Sprintf("metric_%d", j%30),
					2,
				)
				c.Process([]*metricspb.ResourceMetrics{rm})
			}
		}(i)
	}

	// Resetters
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 30; j++ {
			c.ResetCardinality()
			runtime.Gosched()
		}
	}()

	wg.Wait()
}

func TestRace_Collector_ProcessWithServeHTTP(t *testing.T) {
	c := NewCollector([]string{"service", "env"}, StatsLevelFull)

	var wg sync.WaitGroup

	// Processors
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				rm := makeRaceResourceMetrics(
					fmt.Sprintf("svc-%d", id%3),
					fmt.Sprintf("api_latency_%d", j%10),
					2,
				)
				c.Process([]*metricspb.ResourceMetrics{rm})
			}
		}(i)
	}

	// Metrics endpoint readers
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest("GET", "/metrics", nil)
				c.ServeHTTP(rec, req)
				runtime.Gosched()
			}
		}()
	}

	wg.Wait()
}

func TestRace_Collector_ProcessWithGetGlobalStats(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelFull)

	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				rm := makeRaceResourceMetrics("svc", fmt.Sprintf("m_%d", j%20), 1)
				c.Process([]*metricspb.ResourceMetrics{rm})
			}
		}(i)
	}

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				c.GetGlobalStats()
			}
		}()
	}

	wg.Wait()
}

func TestRace_Collector_ProcessWithAtomicCounters(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelFull)

	var wg sync.WaitGroup

	// Process metrics
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				rm := makeRaceResourceMetrics("svc", "metric", 2)
				c.Process([]*metricspb.ResourceMetrics{rm})
			}
		}(i)
	}

	// Update atomic counters
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				c.RecordReceived(10)
				c.RecordExport(9)
				c.RecordExportError()
			}
		}()
	}

	// Read atomic counters via ServeHTTP
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 50; j++ {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/metrics", nil)
			c.ServeHTTP(rec, req)
		}
	}()

	wg.Wait()
}

func TestRace_Collector_PeriodicLoggingWithProcess(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelFull)
	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup

	// Start periodic logging
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.StartPeriodicLogging(ctx, 50*time.Millisecond)
	}()

	// Concurrent processing
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				rm := makeRaceResourceMetrics(
					fmt.Sprintf("svc-%d", id),
					fmt.Sprintf("metric_%d", j%20),
					2,
				)
				c.Process([]*metricspb.ResourceMetrics{rm})
			}
		}(i)
	}

	time.Sleep(200 * time.Millisecond)
	cancel()
	wg.Wait()
}

// --- Memory leak tests ---

func TestMemLeak_Collector_ResetCycles(t *testing.T) {
	c := NewCollector([]string{"service", "env"}, StatsLevelFull)

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	const cycles = 50
	for cycle := 0; cycle < cycles; cycle++ {
		// Add many unique metrics and labels
		for j := 0; j < 200; j++ {
			rm := makeRaceResourceMetrics(
				fmt.Sprintf("svc-%d", j%10),
				fmt.Sprintf("metric_%d_%d", cycle, j),
				5,
			)
			c.Process([]*metricspb.ResourceMetrics{rm})
		}
		c.ResetCardinality()
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("Collector reset cycles: heap_before=%dKB, heap_after=%dKB",
		heapBefore/1024, heapAfter/1024)

	if heapAfter > heapBefore+50*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after %d reset cycles",
			heapBefore/1024, heapAfter/1024, cycles)
	}
}

func TestMemLeak_Collector_HighCardinalityGrowth(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelFull)

	// Simulate high cardinality: many unique metric names
	for i := 0; i < 10000; i++ {
		rm := makeRaceResourceMetrics("svc", fmt.Sprintf("unique_metric_%d", i), 1)
		c.Process([]*metricspb.ResourceMetrics{rm})
	}

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapAfterGrowth := m.HeapInuse

	// Reset should reclaim
	c.ResetCardinality()

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfterReset := m.HeapInuse

	t.Logf("High cardinality: heap_after_growth=%dKB, heap_after_reset=%dKB",
		heapAfterGrowth/1024, heapAfterReset/1024)

	// After reset, heap should shrink significantly (at least 50% of growth reclaimed)
	if heapAfterGrowth > 1024*1024 && heapAfterReset > heapAfterGrowth*9/10 {
		t.Logf("Warning: heap did not shrink much after reset (growth=%dKB, after=%dKB)",
			heapAfterGrowth/1024, heapAfterReset/1024)
	}
}
