package buffer

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
)

func makeRaceResourceMetrics(service string, metricName string, dps int) []*metricspb.ResourceMetrics {
	datapoints := make([]*metricspb.NumberDataPoint, dps)
	for i := 0; i < dps; i++ {
		datapoints[i] = &metricspb.NumberDataPoint{
			Attributes: []*commonpb.KeyValue{
				{Key: "endpoint", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprintf("/api/%d", i)}}},
			},
		}
	}
	return []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: service}}},
				},
			},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{Metrics: []*metricspb.Metric{
					{Name: metricName, Data: &metricspb.Metric_Sum{Sum: &metricspb.Sum{DataPoints: datapoints}}},
				}},
			},
		},
	}
}

// --- Race condition tests ---

func TestRace_MemoryQueue_ConcurrentPushPop(t *testing.T) {
	q := NewMemoryQueue(100, 10*1024*1024)

	var wg sync.WaitGroup

	// Pushers
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 300; j++ {
				req := &colmetricspb.ExportMetricsServiceRequest{
					ResourceMetrics: makeRaceResourceMetrics(
						fmt.Sprintf("svc-%d", id),
						fmt.Sprintf("metric_%d", j%10),
						2,
					),
				}
				q.Push(req)
			}
		}(i)
	}

	// Poppers
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 300; j++ {
				q.Pop()
				runtime.Gosched()
			}
		}()
	}

	wg.Wait()
}

func TestRace_MemoryQueue_PushWithLenSize(t *testing.T) {
	q := NewMemoryQueue(50, 5*1024*1024)

	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				req := &colmetricspb.ExportMetricsServiceRequest{
					ResourceMetrics: makeRaceResourceMetrics("svc", "metric", 1),
				}
				q.Push(req)
				q.Len()
				q.Size()
			}
		}(i)
	}

	wg.Wait()
}

func TestRace_MemoryQueue_EvictionUnderContention(t *testing.T) {
	q := NewMemoryQueue(10, 1*1024*1024) // Small to force evictions

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				req := &colmetricspb.ExportMetricsServiceRequest{
					ResourceMetrics: makeRaceResourceMetrics(
						fmt.Sprintf("svc-%d", id),
						fmt.Sprintf("metric_%d", j),
						3,
					),
				}
				q.Push(req)
				q.Pop()
			}
		}(i)
	}

	wg.Wait()
}

// --- Memory leak tests ---

func TestMemLeak_MemoryQueue_PushPopCycles(t *testing.T) {
	q := NewMemoryQueue(50, 5*1024*1024)

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	const cycles = 200
	for c := 0; c < cycles; c++ {
		for i := 0; i < 50; i++ {
			req := &colmetricspb.ExportMetricsServiceRequest{
				ResourceMetrics: makeRaceResourceMetrics("svc", fmt.Sprintf("m_%d", i), 2),
			}
			q.Push(req)
		}
		for i := 0; i < 50; i++ {
			q.Pop()
		}
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("MemoryQueue push/pop cycles: heap_before=%dKB, heap_after=%dKB, len=%d",
		heapBefore/1024, heapAfter/1024, q.Len())

	if heapAfter > heapBefore+30*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after %d cycles",
			heapBefore/1024, heapAfter/1024, cycles)
	}
}

func TestMemLeak_MemoryQueue_EvictionReclaims(t *testing.T) {
	q := NewMemoryQueue(20, 2*1024*1024) // Small queue

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	// Push far more than capacity — forces continuous eviction
	for i := 0; i < 5000; i++ {
		req := &colmetricspb.ExportMetricsServiceRequest{
			ResourceMetrics: makeRaceResourceMetrics("svc", fmt.Sprintf("metric_%d", i), 3),
		}
		q.Push(req)
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("MemoryQueue eviction: heap_before=%dKB, heap_after=%dKB, len=%d",
		heapBefore/1024, heapAfter/1024, q.Len())

	if q.Len() > 20 {
		t.Errorf("Queue length %d exceeds max %d", q.Len(), 20)
	}

	if heapAfter > heapBefore+20*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB with max_size=20",
			heapBefore/1024, heapAfter/1024)
	}
}
