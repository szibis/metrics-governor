package relabel

import (
	"fmt"
	"sync"
	"testing"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
)

func makeRaceRM(name string, dpCount int) []*metricspb.ResourceMetrics {
	dps := make([]*metricspb.NumberDataPoint, dpCount)
	for i := 0; i < dpCount; i++ {
		dps[i] = &metricspb.NumberDataPoint{
			Attributes: []*commonpb.KeyValue{
				{Key: "env", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "prod"}}},
				{Key: "host", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprintf("host-%d", i)}}},
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

// TestRace_Relabel_ConcurrentRelabel verifies that concurrent Relabel calls
// do not trigger the race detector.
func TestRace_Relabel_ConcurrentRelabel(t *testing.T) {
	cfgs := []Config{
		{
			SourceLabels: []string{"env"},
			TargetLabel:  "environment",
			Regex:        "(.*)",
			Replacement:  "$1",
			Action:       ActionReplace,
		},
	}
	r, err := New(cfgs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				rms := makeRaceRM(fmt.Sprintf("metric_%d_%d", id, i), 3)
				result := r.Relabel(rms)
				_ = result
			}
		}(g)
	}
	wg.Wait()
}

// TestRace_Relabel_RelabelWithReload verifies that concurrent Relabel and
// ReloadConfig calls do not race on the RWMutex-protected config.
func TestRace_Relabel_RelabelWithReload(t *testing.T) {
	cfgs := []Config{
		{
			SourceLabels: []string{"env"},
			TargetLabel:  "environment",
			Regex:        "(.*)",
			Replacement:  "$1",
			Action:       ActionReplace,
		},
	}
	r, err := New(cfgs)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var wg sync.WaitGroup

	// 4 reader goroutines calling Relabel
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				rms := makeRaceRM(fmt.Sprintf("metric_%d_%d", id, i), 3)
				r.Relabel(rms)
			}
		}(g)
	}

	// 1 writer goroutine calling ReloadConfig
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			newCfgs := []Config{
				{
					SourceLabels: []string{"host"},
					TargetLabel:  "hostname",
					Regex:        "(.*)",
					Replacement:  "$1",
					Action:       ActionReplace,
				},
			}
			if err := r.ReloadConfig(newCfgs); err != nil {
				t.Errorf("ReloadConfig: %v", err)
			}
		}
	}()

	wg.Wait()
}
