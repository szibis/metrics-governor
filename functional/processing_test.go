package functional

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/buffer"
	"github.com/szibis/metrics-governor/internal/sampling"
	"github.com/szibis/metrics-governor/internal/stats"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
)

// makeProcessingRM creates a single ResourceMetrics with one Gauge metric and one NumberDataPoint.
func makeProcessingRM(name string, labels map[string]string, ts uint64, value float64) *metricspb.ResourceMetrics {
	attrs := make([]*commonpb.KeyValue, 0, len(labels))
	for k, v := range labels {
		attrs = append(attrs, &commonpb.KeyValue{
			Key:   k,
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}},
		})
	}
	dp := &metricspb.NumberDataPoint{
		Attributes:   attrs,
		TimeUnixNano: ts,
		Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: value},
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
					Gauge: &metricspb.Gauge{DataPoints: []*metricspb.NumberDataPoint{dp}},
				},
			}},
		}},
	}
}

// collectExportedMetricNames returns all metric names found in the mock exporter's exports.
func collectExportedMetricNames(exp *bufferMockExporter) []string {
	exports := exp.getExports()
	var names []string
	for _, req := range exports {
		for _, rm := range req.ResourceMetrics {
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					names = append(names, m.Name)
				}
			}
		}
	}
	return names
}

// collectExportedAttributes returns a map of label key→value for each datapoint across all exports.
func collectExportedAttributes(exp *bufferMockExporter) []map[string]string {
	exports := exp.getExports()
	var result []map[string]string
	for _, req := range exports {
		for _, rm := range req.ResourceMetrics {
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					if g := m.GetGauge(); g != nil {
						for _, dp := range g.DataPoints {
							attrs := make(map[string]string)
							for _, kv := range dp.Attributes {
								attrs[kv.Key] = kv.Value.GetStringValue()
							}
							result = append(result, attrs)
						}
					}
				}
			}
		}
	}
	return result
}

// TestFunctional_Processing_DropInPipeline verifies that the drop action removes
// matching metrics before export, while non-matching metrics pass through.
func TestFunctional_Processing_DropInPipeline(t *testing.T) {
	s, err := sampling.NewFromProcessing(sampling.ProcessingConfig{
		Rules: []sampling.ProcessingRule{
			{Name: "drop-internal", Input: "internal_.*", Action: sampling.ActionDrop},
		},
	})
	if err != nil {
		t.Fatalf("NewFromProcessing: %v", err)
	}

	exp := &bufferMockExporter{}
	statsC := stats.NewCollector(nil, stats.StatsLevelFull)
	buf := buffer.New(1000, 100, 50*time.Millisecond, exp, statsC, nil, nil, buffer.WithProcessor(s))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go buf.Start(ctx)

	now := uint64(time.Now().UnixNano())
	buf.Add([]*metricspb.ResourceMetrics{
		makeProcessingRM("internal_metric", nil, now, 1.0),
	})
	buf.Add([]*metricspb.ResourceMetrics{
		makeProcessingRM("http_metric", nil, now+1, 2.0),
	})

	time.Sleep(300 * time.Millisecond)
	cancel()
	buf.Wait()

	names := collectExportedMetricNames(exp)
	for _, n := range names {
		if n == "internal_metric" {
			t.Fatal("internal_metric should have been dropped")
		}
	}
	found := false
	for _, n := range names {
		if n == "http_metric" {
			found = true
		}
	}
	if !found {
		t.Fatal("http_metric should have been exported")
	}
}

// TestFunctional_Processing_TransformLabelsVisible verifies that transform rules
// add labels that appear in the exported data.
func TestFunctional_Processing_TransformLabelsVisible(t *testing.T) {
	s, err := sampling.NewFromProcessing(sampling.ProcessingConfig{
		Rules: []sampling.ProcessingRule{
			{
				Name:   "add-env",
				Input:  ".*",
				Action: sampling.ActionTransform,
				Operations: []sampling.Operation{
					{Set: &sampling.SetOp{Label: "env", Value: "production"}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewFromProcessing: %v", err)
	}

	exp := &bufferMockExporter{}
	statsC := stats.NewCollector(nil, stats.StatsLevelFull)
	buf := buffer.New(1000, 100, 50*time.Millisecond, exp, statsC, nil, nil, buffer.WithProcessor(s))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go buf.Start(ctx)

	now := uint64(time.Now().UnixNano())
	buf.Add([]*metricspb.ResourceMetrics{
		makeProcessingRM("test_metric", map[string]string{"service": "web"}, now, 42.0),
	})

	time.Sleep(300 * time.Millisecond)
	cancel()
	buf.Wait()

	allAttrs := collectExportedAttributes(exp)
	if len(allAttrs) == 0 {
		t.Fatal("expected at least one exported datapoint")
	}
	for _, attrs := range allAttrs {
		if attrs["env"] != "production" {
			t.Errorf("expected env=production, got env=%q", attrs["env"])
		}
	}
}

// TestFunctional_Processing_SampleRateAccuracy verifies that head sampling at rate=0.5
// keeps approximately 50% of datapoints. Head sampling is deterministic 1-in-N (N=2),
// so for 1000 datapoints the result should be exactly 500.
func TestFunctional_Processing_SampleRateAccuracy(t *testing.T) {
	s, err := sampling.NewFromProcessing(sampling.ProcessingConfig{
		Rules: []sampling.ProcessingRule{
			{Name: "half-sample", Input: ".*", Action: sampling.ActionSample, Rate: 0.5, Method: "head"},
		},
	})
	if err != nil {
		t.Fatalf("NewFromProcessing: %v", err)
	}

	exp := &bufferMockExporter{}
	statsC := stats.NewCollector(nil, stats.StatsLevelFull)
	buf := buffer.New(100000, 500, 50*time.Millisecond, exp, statsC, nil, nil, buffer.WithProcessor(s))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go buf.Start(ctx)

	now := uint64(time.Now().UnixNano())
	for i := 0; i < 1000; i++ {
		buf.Add([]*metricspb.ResourceMetrics{
			makeProcessingRM("sample_metric", nil, now+uint64(i), float64(i)),
		})
	}

	time.Sleep(500 * time.Millisecond)
	cancel()
	buf.Wait()

	total := countExportedDatapoints(exp)
	// Head sampling at rate=0.5 means 1-in-2 deterministic. Accept ±10% tolerance.
	low := 400
	high := 600
	if total < low || total > high {
		t.Fatalf("sample rate=0.5: expected %d-%d datapoints, got %d", low, high, total)
	}
}

// TestFunctional_Processing_DownsampleInPipeline verifies that the downsample action
// compresses datapoints within a time window, producing fewer outputs than inputs.
func TestFunctional_Processing_DownsampleInPipeline(t *testing.T) {
	s, err := sampling.NewFromProcessing(sampling.ProcessingConfig{
		Rules: []sampling.ProcessingRule{
			{
				Name:     "ds-avg",
				Input:    ".*",
				Action:   sampling.ActionDownsample,
				Method:   "avg",
				Interval: "1s",
			},
		},
	})
	if err != nil {
		t.Fatalf("NewFromProcessing: %v", err)
	}

	exp := &bufferMockExporter{}
	statsC := stats.NewCollector(nil, stats.StatsLevelFull)
	buf := buffer.New(1000, 100, 50*time.Millisecond, exp, statsC, nil, nil, buffer.WithProcessor(s))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go buf.Start(ctx)

	// Add 10 datapoints spanning 2 seconds (5 per second window).
	base := uint64(time.Now().UnixNano())
	for i := 0; i < 10; i++ {
		// Spread across 2 seconds: 0ms, 200ms, 400ms, 600ms, 800ms, 1000ms, ...
		ts := base + uint64(i)*200*uint64(time.Millisecond)
		buf.Add([]*metricspb.ResourceMetrics{
			makeProcessingRM("ds_metric", nil, ts, float64(i*10)),
		})
	}

	time.Sleep(500 * time.Millisecond)
	cancel()
	buf.Wait()

	total := countExportedDatapoints(exp)
	// Downsample accumulates within window and emits aggregated.
	// With 10 inputs over 2 windows, we expect fewer outputs than 10.
	// The exact count depends on window boundaries, but should be strictly less.
	if total >= 10 {
		t.Fatalf("downsample: expected fewer than 10 datapoints, got %d", total)
	}
	t.Logf("downsample: %d input -> %d output datapoints", 10, total)
}

// TestFunctional_Processing_MixedActions verifies that multiple processing rule types
// (transform, drop, sample) work together in a single pipeline.
func TestFunctional_Processing_MixedActions(t *testing.T) {
	s, err := sampling.NewFromProcessing(sampling.ProcessingConfig{
		Rules: []sampling.ProcessingRule{
			{
				Name:   "add-label",
				Input:  ".*",
				Action: sampling.ActionTransform,
				Operations: []sampling.Operation{
					{Set: &sampling.SetOp{Label: "pipeline", Value: "processed"}},
				},
			},
			{Name: "drop-internal", Input: "internal_.*", Action: sampling.ActionDrop},
			{Name: "keep-all", Input: ".*", Action: sampling.ActionSample, Rate: 1.0, Method: "head"},
		},
	})
	if err != nil {
		t.Fatalf("NewFromProcessing: %v", err)
	}

	exp := &bufferMockExporter{}
	statsC := stats.NewCollector(nil, stats.StatsLevelFull)
	buf := buffer.New(1000, 100, 50*time.Millisecond, exp, statsC, nil, nil, buffer.WithProcessor(s))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go buf.Start(ctx)

	now := uint64(time.Now().UnixNano())
	buf.Add([]*metricspb.ResourceMetrics{
		makeProcessingRM("internal_metric", nil, now, 1.0),
	})
	buf.Add([]*metricspb.ResourceMetrics{
		makeProcessingRM("http_metric", nil, now+1, 2.0),
	})
	buf.Add([]*metricspb.ResourceMetrics{
		makeProcessingRM("sli_metric", nil, now+2, 3.0),
	})

	time.Sleep(300 * time.Millisecond)
	cancel()
	buf.Wait()

	// Assert: internal_metric should be dropped.
	names := collectExportedMetricNames(exp)
	for _, n := range names {
		if n == "internal_metric" {
			t.Fatal("internal_metric should have been dropped")
		}
	}

	// Assert: http_metric and sli_metric should be exported.
	nameSet := make(map[string]bool)
	for _, n := range names {
		nameSet[n] = true
	}
	if !nameSet["http_metric"] {
		t.Error("http_metric should have been exported")
	}
	if !nameSet["sli_metric"] {
		t.Error("sli_metric should have been exported")
	}

	// Assert: surviving metrics should have the transform label applied.
	allAttrs := collectExportedAttributes(exp)
	for _, attrs := range allAttrs {
		if attrs["pipeline"] != "processed" {
			t.Errorf("expected pipeline=processed label on exported datapoint, got pipeline=%q", attrs["pipeline"])
		}
	}
}

// TestFunctional_Processing_ConfigReloadUnderLoad verifies that reloading the processing
// configuration under load preserves pipeline integrity. After reload from keep-all to
// drop-all, new data should be dropped.
func TestFunctional_Processing_ConfigReloadUnderLoad(t *testing.T) {
	s, err := sampling.NewFromProcessing(sampling.ProcessingConfig{
		Rules: []sampling.ProcessingRule{
			{Name: "keep-all", Input: ".*", Action: sampling.ActionSample, Rate: 1.0, Method: "head"},
		},
	})
	if err != nil {
		t.Fatalf("NewFromProcessing: %v", err)
	}

	exp := &bufferMockExporter{}
	statsC := stats.NewCollector(nil, stats.StatsLevelFull)
	buf := buffer.New(100000, 500, 50*time.Millisecond, exp, statsC, nil, nil, buffer.WithProcessor(s))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go buf.Start(ctx)

	// Phase 1: send data while keep-all is active.
	now := uint64(time.Now().UnixNano())
	var wg sync.WaitGroup
	stopCh := make(chan struct{})
	dataSent := int64(0)

	wg.Add(1)
	go func() {
		defer wg.Done()
		i := 0
		for {
			select {
			case <-stopCh:
				return
			default:
				buf.Add([]*metricspb.ResourceMetrics{
					makeProcessingRM("load_metric", nil, now+uint64(i), float64(i)),
				})
				i++
				dataSent = int64(i)
				time.Sleep(time.Millisecond)
			}
		}
	}()

	// Let some data flow through.
	time.Sleep(100 * time.Millisecond)

	// Reload to drop-all.
	err = s.ReloadProcessingConfig(sampling.ProcessingConfig{
		Rules: []sampling.ProcessingRule{
			{Name: "drop-all", Input: ".*", Action: sampling.ActionDrop},
		},
	})
	if err != nil {
		t.Fatalf("ReloadProcessingConfig: %v", err)
	}

	// Let reload take effect and collect some data under the new config.
	time.Sleep(200 * time.Millisecond)
	close(stopCh)
	wg.Wait()

	// Wait for buffer to flush any remaining data.
	time.Sleep(200 * time.Millisecond)
	cancel()
	buf.Wait()

	total := countExportedDatapoints(exp)
	// We should have some data from before the reload but not all.
	// The total exported should be strictly less than all data sent.
	if total >= int(dataSent) {
		t.Fatalf("config reload: expected fewer exported (%d) than sent (%d) — drop-all should have dropped post-reload data", total, dataSent)
	}
	if total == 0 {
		t.Fatal("config reload: expected some data exported before reload to drop-all")
	}
	t.Logf("config reload: sent %d, exported %d (drop-all took effect)", dataSent, total)
}

// TestFunctional_Processing_AggregateOutputReinjection verifies that aggregated
// metrics flow through the buffer's AddAggregated path and appear in exports.
func TestFunctional_Processing_AggregateOutputReinjection(t *testing.T) {
	s, err := sampling.NewFromProcessing(sampling.ProcessingConfig{
		Rules: []sampling.ProcessingRule{
			{
				Name:      "agg-by-service",
				Input:     "request_count",
				Action:    sampling.ActionAggregate,
				Output:    "request_count_total",
				GroupBy:   []string{"service"},
				Functions: []string{"sum"},
				Interval:  "1s",
			},
		},
	})
	if err != nil {
		t.Fatalf("NewFromProcessing: %v", err)
	}

	exp := &bufferMockExporter{}
	statsC := stats.NewCollector(nil, stats.StatsLevelFull)
	buf := buffer.New(1000, 100, 50*time.Millisecond, exp, statsC, nil, nil, buffer.WithProcessor(s))

	// Wire aggregate output to the buffer's AddAggregated method.
	s.SetAggregateOutput(buf.AddAggregated)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Start aggregation timers.
	s.StartAggregation(ctx)

	go buf.Start(ctx)

	// Add data with different service labels.
	now := uint64(time.Now().UnixNano())
	for i := 0; i < 5; i++ {
		buf.Add([]*metricspb.ResourceMetrics{
			makeProcessingRM("request_count", map[string]string{"service": "web"}, now+uint64(i)*uint64(100*time.Millisecond), float64(i+1)),
		})
		buf.Add([]*metricspb.ResourceMetrics{
			makeProcessingRM("request_count", map[string]string{"service": "api"}, now+uint64(i)*uint64(100*time.Millisecond), float64((i+1)*10)),
		})
	}

	// Wait for at least one aggregate flush interval (1s) plus buffer flush.
	time.Sleep(2 * time.Second)

	// Stop aggregation (triggers final flush).
	s.StopAggregation()

	// Give buffer time to flush the re-injected aggregates.
	time.Sleep(300 * time.Millisecond)
	cancel()
	buf.Wait()

	// Check that aggregated metrics appear in exports.
	names := collectExportedMetricNames(exp)
	foundAgg := false
	for _, n := range names {
		if n == "request_count_total" {
			foundAgg = true
			break
		}
	}
	if !foundAgg {
		t.Fatalf("expected aggregated metric 'request_count_total' in exports, got names: %v", names)
	}
	t.Logf("aggregate reinjection: found 'request_count_total' among %d exported metric names", len(names))
}
