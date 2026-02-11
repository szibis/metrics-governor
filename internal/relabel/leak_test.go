package relabel

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	"go.uber.org/goleak"
)

func makeLeakRM(name string, dpCount int) []*metricspb.ResourceMetrics {
	dps := make([]*metricspb.NumberDataPoint, dpCount)
	for i := 0; i < dpCount; i++ {
		dps[i] = &metricspb.NumberDataPoint{
			Attributes: []*commonpb.KeyValue{
				{Key: "env", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "prod"}}},
			},
			Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)},
		}
	}
	return []*metricspb.ResourceMetrics{
		{
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

// TestLeakCheck_Relabeler verifies that creating, using, and discarding a Relabeler
// does not leak goroutines.
func TestLeakCheck_Relabeler(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	r, err := New([]Config{
		{
			SourceLabels: []string{"env"},
			TargetLabel:  "environment",
			Regex:        "(.*)",
			Replacement:  "$1",
			Action:       ActionReplace,
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := 0; i < 50; i++ {
		rms := makeLeakRM(fmt.Sprintf("metric_%d", i), 5)
		r.Relabel(rms)
	}
	// Discard r — goleak verifies no goroutine leak
}

// TestMemLeak_Relabeler_ReloadCycles verifies that repeated Relabel + ReloadConfig
// cycles do not cause unbounded heap growth.
func TestMemLeak_Relabeler_ReloadCycles(t *testing.T) {
	r, err := New([]Config{
		{
			SourceLabels: []string{"env"},
			TargetLabel:  "environment",
			Regex:        "(.*)",
			Replacement:  "$1",
			Action:       ActionReplace,
		},
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
			r.Relabel(rms)
		}
		// Reload with a different config each cycle
		newCfgs := []Config{
			{
				SourceLabels: []string{"host"},
				TargetLabel:  fmt.Sprintf("hostname_%d", c),
				Regex:        "(.*)",
				Replacement:  "$1",
				Action:       ActionReplace,
			},
		}
		if err := r.ReloadConfig(newCfgs); err != nil {
			t.Fatalf("ReloadConfig cycle %d: %v", c, err)
		}
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("Relabeler reload cycles: heap_before=%dKB, heap_after=%dKB",
		heapBefore/1024, heapAfter/1024)

	if heapAfter > heapBefore+30*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after %d cycles",
			heapBefore/1024, heapAfter/1024, cycles)
	}
}
