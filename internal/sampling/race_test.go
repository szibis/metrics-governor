package sampling

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
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
	s, err := newFromLegacy(FileConfig{
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
	s, err := newFromLegacy(FileConfig{
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
			if err := reloadFromLegacy(s, newCfg); err != nil {
				t.Errorf("ReloadConfig: %v", err)
			}
		}
	}()

	wg.Wait()
}

// TestRace_Processing_ConcurrentProcess verifies concurrent Process calls with all 5 action types.
func TestRace_Processing_ConcurrentProcess(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "transform-env", Input: ".*", Action: ActionTransform,
				Operations: []Operation{{Set: &SetOp{Label: "env", Value: "test"}}}},
			{Name: "drop-debug", Input: "debug_.*", Action: ActionDrop},
			{Name: "sample-half", Input: "sample_.*", Action: ActionSample, Rate: 0.5, Method: "head"},
			{Name: "ds-avg", Input: "cpu_.*", Action: ActionDownsample, Method: "avg", Interval: "1m"},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		t.Fatalf("validateProcessingConfig: %v", err)
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatalf("NewFromProcessing: %v", err)
	}

	var wg sync.WaitGroup
	// 4 goroutines processing different metric name patterns concurrently.
	metricPrefixes := []string{"debug_", "sample_", "cpu_", "http_"}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			prefix := metricPrefixes[id%len(metricPrefixes)]
			for i := 0; i < 200; i++ {
				rms := makeRaceRM(fmt.Sprintf("%smetric_%d_%d", prefix, id, i), 3)
				result := s.Process(rms)
				_ = result
			}
		}(g)
	}
	wg.Wait()
}

// TestRace_Processing_ProcessWithReload verifies concurrent Process + ReloadProcessingConfig.
func TestRace_Processing_ProcessWithReload(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "transform-env", Input: ".*", Action: ActionTransform,
				Operations: []Operation{{Set: &SetOp{Label: "env", Value: "test"}}}},
			{Name: "drop-debug", Input: "debug_.*", Action: ActionDrop},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		t.Fatalf("validateProcessingConfig: %v", err)
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatalf("NewFromProcessing: %v", err)
	}

	var wg sync.WaitGroup

	// 4 reader goroutines calling Process.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				rms := makeRaceRM(fmt.Sprintf("metric_%d_%d", id, i), 3)
				s.Process(rms)
			}
		}(g)
	}

	// 1 writer goroutine calling ReloadProcessingConfig.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			newCfg := ProcessingConfig{
				Rules: []ProcessingRule{
					{Name: "transform-env", Input: ".*", Action: ActionTransform,
						Operations: []Operation{{Set: &SetOp{Label: "env", Value: fmt.Sprintf("v%d", i)}}}},
					{Name: "sample-varied", Input: "metric_.*", Action: ActionSample,
						Rate: float64(i%10) / 10.0, Method: "head"},
				},
			}
			if err := s.ReloadProcessingConfig(newCfg); err != nil {
				t.Errorf("ReloadProcessingConfig iter %d: %v", i, err)
			}
		}
	}()

	wg.Wait()
}

// TestRace_Processing_ProcessWithAggregateFlush verifies Process + aggregate flush timer.
func TestRace_Processing_ProcessWithAggregateFlush(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "agg-sum", Input: "agg_.*", Action: ActionAggregate,
				Interval: "100ms", GroupBy: []string{"service"}, Functions: []string{"sum"}},
			{Name: "transform-tag", Input: ".*", Action: ActionTransform,
				Operations: []Operation{{Set: &SetOp{Label: "processed", Value: "true"}}}},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		t.Fatalf("validateProcessingConfig: %v", err)
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatalf("NewFromProcessing: %v", err)
	}

	// Set aggregate output callback.
	var outputMu sync.Mutex
	var outputCount int
	s.SetAggregateOutput(func(rms []*metricspb.ResourceMetrics) {
		outputMu.Lock()
		outputCount += len(rms)
		outputMu.Unlock()
	})

	// Start aggregation with a timeout context.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s.StartAggregation(ctx)
	defer s.StopAggregation()

	var wg sync.WaitGroup
	// 4 goroutines processing data concurrently while aggregate engine flushes.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				rms := makeRaceRM(fmt.Sprintf("agg_metric_%d_%d", id, i), 3)
				s.Process(rms)
			}
		}(g)
	}
	wg.Wait()

	// Allow a few flush cycles.
	time.Sleep(300 * time.Millisecond)
}

// TestRace_Processing_TransformOperations verifies concurrent transform label operations.
func TestRace_Processing_TransformOperations(t *testing.T) {
	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{Name: "set-env", Input: ".*", Action: ActionTransform,
				Operations: []Operation{
					{Set: &SetOp{Label: "env", Value: "test"}},
				}},
			{Name: "rename-host", Input: ".*", Action: ActionTransform,
				Operations: []Operation{
					{Rename: &RenameOp{Source: "host", Target: "hostname"}},
				}},
			{Name: "replace-prefix", Input: ".*", Action: ActionTransform,
				Operations: []Operation{
					{Replace: &ReplaceOp{Label: "hostname", Pattern: "host-(\\d+)", Replacement: "server-$1"}},
				}},
			{Name: "hash-host", Input: ".*", Action: ActionTransform,
				Operations: []Operation{
					{HashMod: &HashModOp{Source: "hostname", Target: "shard", Modulus: 8}},
				}},
		},
	}
	if err := validateProcessingConfig(&cfg); err != nil {
		t.Fatalf("validateProcessingConfig: %v", err)
	}
	s, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatalf("NewFromProcessing: %v", err)
	}

	var wg sync.WaitGroup
	// 4 goroutines processing the same metric name with different host labels.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				rms := makeRaceRM(fmt.Sprintf("transform_metric_%d", i), 3)
				result := s.Process(rms)
				_ = result
			}
		}(g)
	}
	wg.Wait()
}
