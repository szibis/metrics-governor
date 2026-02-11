package functional

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/buffer"
	"github.com/szibis/metrics-governor/internal/sampling"
	"github.com/szibis/metrics-governor/internal/stats"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
)

func makeSamplingRM(name string, dpCount int, labels map[string]string) *metricspb.ResourceMetrics {
	dps := make([]*metricspb.NumberDataPoint, dpCount)
	for i := 0; i < dpCount; i++ {
		attrs := make([]*commonpb.KeyValue, 0, len(labels))
		for k, v := range labels {
			attrs = append(attrs, &commonpb.KeyValue{
				Key:   k,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}},
			})
		}
		dps[i] = &metricspb.NumberDataPoint{
			Attributes:   attrs,
			TimeUnixNano: uint64(time.Now().UnixNano()),
			Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)},
		}
	}
	return &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{
				{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: name}}},
			},
		},
		ScopeMetrics: []*metricspb.ScopeMetrics{{
			Metrics: []*metricspb.Metric{{
				Name: name,
				Data: &metricspb.Metric_Gauge{
					Gauge: &metricspb.Gauge{DataPoints: dps},
				},
			}},
		}},
	}
}

// TestFunctional_Sampling_HeadStrategy verifies deterministic 1-in-N head sampling
// through a real buffer pipeline.
func TestFunctional_Sampling_HeadStrategy(t *testing.T) {
	// rate=1.0 should keep everything
	s, err := sampling.New(sampling.FileConfig{
		DefaultRate: 1.0,
		Strategy:    sampling.StrategyHead,
	})
	if err != nil {
		t.Fatalf("sampling.New: %v", err)
	}

	exp := &bufferMockExporter{}
	statsC := stats.NewCollector(nil, stats.StatsLevelFull)
	buf := buffer.New(1000, 100, 50*time.Millisecond, exp, statsC, nil, nil, buffer.WithSampler(s))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go buf.Start(ctx)

	for i := 0; i < 100; i++ {
		rm := makeSamplingRM(fmt.Sprintf("metric_%d", i), 1, nil)
		buf.Add([]*metricspb.ResourceMetrics{rm})
	}

	time.Sleep(300 * time.Millisecond)
	cancel()
	buf.Wait()

	total := countExportedDatapoints(exp)
	if total != 100 {
		t.Fatalf("head sampling rate=1.0: expected 100, got %d", total)
	}
}

// TestFunctional_Sampling_RuleMatch verifies that per-metric sampling rules
// are applied correctly through a real buffer pipeline.
func TestFunctional_Sampling_RuleMatch(t *testing.T) {
	// Default rate=1.0, but debug metrics get rate=0
	s, err := sampling.New(sampling.FileConfig{
		DefaultRate: 1.0,
		Strategy:    sampling.StrategyHead,
		Rules: []sampling.Rule{
			{
				Name:     "drop-debug",
				Match:    map[string]string{"__name__": "debug_.*"},
				Rate:     0.0,
				Strategy: sampling.StrategyHead,
			},
		},
	})
	if err != nil {
		t.Fatalf("sampling.New: %v", err)
	}

	exp := &bufferMockExporter{}
	statsC := stats.NewCollector(nil, stats.StatsLevelFull)
	buf := buffer.New(1000, 100, 50*time.Millisecond, exp, statsC, nil, nil, buffer.WithSampler(s))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go buf.Start(ctx)

	// 10 debug (rate=0 → dropped) + 10 normal (rate=1.0 → kept)
	for i := 0; i < 10; i++ {
		buf.Add([]*metricspb.ResourceMetrics{makeSamplingRM(fmt.Sprintf("debug_%d", i), 1, nil)})
	}
	for i := 0; i < 10; i++ {
		buf.Add([]*metricspb.ResourceMetrics{makeSamplingRM(fmt.Sprintf("normal_%d", i), 1, nil)})
	}

	time.Sleep(300 * time.Millisecond)
	cancel()
	buf.Wait()

	total := countExportedDatapoints(exp)
	if total != 10 {
		t.Fatalf("rule-based sampling: expected 10 (debug dropped), got %d", total)
	}
}

// TestFunctional_Sampling_HighVolume pushes 1000 metrics through sampling
// with rate=1.0 and verifies all arrive.
func TestFunctional_Sampling_HighVolume(t *testing.T) {
	s, err := sampling.New(sampling.FileConfig{
		DefaultRate: 1.0,
		Strategy:    sampling.StrategyHead,
	})
	if err != nil {
		t.Fatalf("sampling.New: %v", err)
	}

	exp := &bufferMockExporter{}
	statsC := stats.NewCollector(nil, stats.StatsLevelFull)
	buf := buffer.New(100000, 500, 50*time.Millisecond, exp, statsC, nil, nil, buffer.WithSampler(s))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go buf.Start(ctx)

	for i := 0; i < 1000; i++ {
		rm := makeSamplingRM(fmt.Sprintf("metric_%d", i), 1, nil)
		buf.Add([]*metricspb.ResourceMetrics{rm})
	}

	time.Sleep(500 * time.Millisecond)
	cancel()
	buf.Wait()

	total := countExportedDatapoints(exp)
	if total != 1000 {
		t.Fatalf("high volume sampling: expected 1000, got %d", total)
	}
}
