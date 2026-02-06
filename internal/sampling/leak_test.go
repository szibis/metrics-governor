package sampling

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"go.uber.org/goleak"
)

func makeLeakRM(name string, dpCount int) []*metricspb.ResourceMetrics {
	dps := make([]*metricspb.NumberDataPoint, dpCount)
	for i := 0; i < dpCount; i++ {
		dps[i] = &metricspb.NumberDataPoint{
			Attributes: []*commonpb.KeyValue{
				{Key: "host", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprintf("host-%d", i)}}},
			},
			Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)},
			TimeUnixNano: uint64(time.Now().UnixNano()) + uint64(i),
		}
	}
	return []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{Metrics: []*metricspb.Metric{
					{
						Name: name,
						Data: &metricspb.Metric_Gauge{
							Gauge: &metricspb.Gauge{DataPoints: dps},
						},
					},
				}},
			},
		},
	}
}

// TestLeakCheck_Sampler verifies that creating, using, and discarding a Sampler
// does not leak goroutines.
func TestLeakCheck_Sampler(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	s, err := New(FileConfig{
		DefaultRate: 0.5,
		Strategy:    StrategyHead,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := 0; i < 50; i++ {
		rms := makeLeakRM(fmt.Sprintf("metric_%d", i), 5)
		s.Sample(rms)
	}
}

// TestMemLeak_Sampler_ReloadCycles verifies that repeated Sample + ReloadConfig
// cycles do not cause unbounded heap growth.
func TestMemLeak_Sampler_ReloadCycles(t *testing.T) {
	s, err := New(FileConfig{
		DefaultRate: 0.5,
		Strategy:    StrategyHead,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	const cycles = 200
	for c := 0; c < cycles; c++ {
		for i := 0; i < 50; i++ {
			rms := makeLeakRM(fmt.Sprintf("metric_%d_%d", c, i), 5)
			s.Sample(rms)
		}
		newCfg := FileConfig{
			DefaultRate: float64(c%10) / 10.0,
			Strategy:    StrategyHead,
		}
		if err := s.ReloadConfig(newCfg); err != nil {
			t.Fatalf("ReloadConfig cycle %d: %v", c, err)
		}
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("Sampler reload cycles: heap_before=%dKB, heap_after=%dKB",
		heapBefore/1024, heapAfter/1024)

	if heapAfter > heapBefore+30*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after %d cycles",
			heapBefore/1024, heapAfter/1024, cycles)
	}
}

// TestMemLeak_DownsampleEngine_WindowCycles verifies that the downsample engine's
// series map doesn't grow unbounded when processing many unique series.
func TestMemLeak_DownsampleEngine_WindowCycles(t *testing.T) {
	de := newDownsampleEngine()

	cfg := &DownsampleConfig{
		Method:       DSAvg,
		Window:       "1s",
		parsedWindow: time.Second,
	}

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	// Push data through 500 unique series. The engine should clean up stale series.
	const cycles = 500
	baseTime := uint64(time.Now().UnixNano())
	for c := 0; c < cycles; c++ {
		for i := 0; i < 10; i++ {
			seriesKey := fmt.Sprintf("metric_%d|host=host-%d", c, i)
			ts := baseTime + uint64(c)*uint64(time.Second) + uint64(i)*uint64(time.Millisecond)
			de.ingestAndEmit(seriesKey, cfg, ts, float64(c*10+i))
		}
	}

	// Series count should not be unbounded — the engine cleans up every 10000 ingestions
	count := de.seriesCount()
	t.Logf("DownsampleEngine: %d active series after %d cycles", count, cycles)

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("DownsampleEngine window cycles: heap_before=%dKB, heap_after=%dKB",
		heapBefore/1024, heapAfter/1024)

	if heapAfter > heapBefore+30*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after %d cycles",
			heapBefore/1024, heapAfter/1024, cycles)
	}
}
