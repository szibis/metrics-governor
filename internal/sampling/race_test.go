package sampling

import (
	"fmt"
	"sync"
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

func makeRaceRM(name string, dpCount int) []*metricspb.ResourceMetrics {
	dps := make([]*metricspb.NumberDataPoint, dpCount)
	for i := 0; i < dpCount; i++ {
		dps[i] = &metricspb.NumberDataPoint{
			Attributes: []*commonpb.KeyValue{
				{Key: "host", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprintf("host-%d", i)}}},
			},
			Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)},
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

// TestRace_Sampling_ConcurrentSample verifies that concurrent Sample calls
// do not trigger the race detector.
func TestRace_Sampling_ConcurrentSample(t *testing.T) {
	s, err := New(FileConfig{
		DefaultRate: 0.5,
		Strategy:    StrategyHead,
	})
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
				result := s.Sample(rms)
				_ = result
			}
		}(g)
	}
	wg.Wait()
}

// TestRace_Sampling_SampleWithReload verifies that concurrent Sample and
// ReloadConfig calls do not race on the RWMutex-protected state.
func TestRace_Sampling_SampleWithReload(t *testing.T) {
	s, err := New(FileConfig{
		DefaultRate: 0.5,
		Strategy:    StrategyHead,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var wg sync.WaitGroup

	// 4 reader goroutines calling Sample
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				rms := makeRaceRM(fmt.Sprintf("metric_%d_%d", id, i), 3)
				s.Sample(rms)
			}
		}(g)
	}

	// 1 writer goroutine calling ReloadConfig
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			newCfg := FileConfig{
				DefaultRate: 0.7,
				Strategy:    StrategyProbabilistic,
				Rules: []Rule{
					{
						Name:     "test-rule",
						Match:    map[string]string{"__name__": "metric_.*"},
						Rate:     0.3,
						Strategy: StrategyHead,
					},
				},
			}
			if err := s.ReloadConfig(newCfg); err != nil {
				t.Errorf("ReloadConfig: %v", err)
			}
		}
	}()

	wg.Wait()
}
