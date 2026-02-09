package functional

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/buffer"
	"github.com/szibis/metrics-governor/internal/relabel"
	"github.com/szibis/metrics-governor/internal/stats"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

func makeRelabelRM(name string, dpCount int, labels map[string]string) *metricspb.ResourceMetrics {
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

func countExportedDatapoints(exp *bufferMockExporter) int {
	exports := exp.getExports()
	total := 0
	for _, req := range exports {
		for _, rm := range req.ResourceMetrics {
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					if g := m.GetGauge(); g != nil {
						total += len(g.DataPoints)
					}
					if s := m.GetSum(); s != nil {
						total += len(s.DataPoints)
					}
				}
			}
		}
	}
	return total
}

// TestFunctional_Relabel_Replace verifies that relabeling with a replace action
// renames a label through a real buffer pipeline.
func TestFunctional_Relabel_Replace(t *testing.T) {
	r, err := relabel.New([]relabel.Config{
		{
			SourceLabels: []string{"env"},
			TargetLabel:  "environment",
			Regex:        "(.*)",
			Replacement:  "$1",
			Action:       relabel.ActionReplace,
		},
	})
	if err != nil {
		t.Fatalf("relabel.New: %v", err)
	}

	exp := &bufferMockExporter{}
	statsC := stats.NewCollector(nil, stats.StatsLevelFull)
	buf := buffer.New(1000, 100, 50*time.Millisecond, exp, statsC, nil, nil, buffer.WithRelabeler(r))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go buf.Start(ctx)

	for i := 0; i < 10; i++ {
		rm := makeRelabelRM(fmt.Sprintf("metric_%d", i), 1, map[string]string{"env": "prod"})
		buf.Add([]*metricspb.ResourceMetrics{rm})
	}

	time.Sleep(300 * time.Millisecond)
	cancel()
	buf.Wait()

	total := countExportedDatapoints(exp)
	if total != 10 {
		t.Fatalf("expected 10 datapoints, got %d", total)
	}

	// Verify label rename
	for _, req := range exp.getExports() {
		for _, rm := range req.ResourceMetrics {
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					for _, dp := range m.GetGauge().GetDataPoints() {
						found := false
						for _, kv := range dp.Attributes {
							if kv.Key == "environment" {
								found = true
							}
						}
						if !found {
							t.Errorf("missing 'environment' label after relabel replace")
						}
					}
				}
			}
		}
	}
}

// TestFunctional_Relabel_Drop verifies that relabeling with a drop action
// removes matching metrics through a real buffer pipeline.
func TestFunctional_Relabel_Drop(t *testing.T) {
	r, err := relabel.New([]relabel.Config{
		{
			SourceLabels: []string{"__name__"},
			Regex:        "debug_.*",
			Action:       relabel.ActionDrop,
		},
	})
	if err != nil {
		t.Fatalf("relabel.New: %v", err)
	}

	exp := &bufferMockExporter{}
	statsC := stats.NewCollector(nil, stats.StatsLevelFull)
	buf := buffer.New(1000, 100, 50*time.Millisecond, exp, statsC, nil, nil, buffer.WithRelabeler(r))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go buf.Start(ctx)

	// 5 debug + 5 normal
	for i := 0; i < 5; i++ {
		buf.Add([]*metricspb.ResourceMetrics{makeRelabelRM(fmt.Sprintf("debug_%d", i), 1, nil)})
	}
	for i := 0; i < 5; i++ {
		buf.Add([]*metricspb.ResourceMetrics{makeRelabelRM(fmt.Sprintf("normal_%d", i), 1, nil)})
	}

	time.Sleep(300 * time.Millisecond)
	cancel()
	buf.Wait()

	total := countExportedDatapoints(exp)
	if total != 5 {
		t.Fatalf("expected 5 datapoints (debug dropped), got %d", total)
	}
}

// TestFunctional_Relabel_LabelDrop verifies that relabeling with labeldrop action
// removes matching labels from datapoints.
func TestFunctional_Relabel_LabelDrop(t *testing.T) {
	r, err := relabel.New([]relabel.Config{
		{
			Regex:  "internal_.*",
			Action: relabel.ActionLabelDrop,
		},
	})
	if err != nil {
		t.Fatalf("relabel.New: %v", err)
	}

	exp := &bufferMockExporter{}
	statsC := stats.NewCollector(nil, stats.StatsLevelFull)
	buf := buffer.New(1000, 100, 50*time.Millisecond, exp, statsC, nil, nil, buffer.WithRelabeler(r))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go buf.Start(ctx)

	rm := makeRelabelRM("test_metric", 1, map[string]string{
		"internal_id":    "abc123",
		"internal_trace": "xyz",
		"visible_label":  "keep_me",
	})
	buf.Add([]*metricspb.ResourceMetrics{rm})

	time.Sleep(300 * time.Millisecond)
	cancel()
	buf.Wait()

	total := countExportedDatapoints(exp)
	if total != 1 {
		t.Fatalf("expected 1 datapoint, got %d", total)
	}

	// Verify internal labels are gone, visible label remains
	for _, req := range exp.getExports() {
		for _, rm := range req.ResourceMetrics {
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					for _, dp := range m.GetGauge().GetDataPoints() {
						for _, kv := range dp.Attributes {
							if kv.Key == "internal_id" || kv.Key == "internal_trace" {
								t.Errorf("label %q should have been dropped by labeldrop", kv.Key)
							}
						}
					}
				}
			}
		}
	}
}

// TestFunctional_Relabel_HighVolume pushes 1000 metrics through relabeling
// and verifies all arrive at the exporter.
func TestFunctional_Relabel_HighVolume(t *testing.T) {
	r, err := relabel.New([]relabel.Config{
		{
			SourceLabels: []string{"env"},
			TargetLabel:  "environment",
			Regex:        "(.*)",
			Replacement:  "$1",
			Action:       relabel.ActionReplace,
		},
	})
	if err != nil {
		t.Fatalf("relabel.New: %v", err)
	}

	exp := &bufferMockExporter{}
	statsC := stats.NewCollector(nil, stats.StatsLevelFull)
	buf := buffer.New(100000, 500, 50*time.Millisecond, exp, statsC, nil, nil, buffer.WithRelabeler(r))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go buf.Start(ctx)

	for i := 0; i < 1000; i++ {
		rm := makeRelabelRM(fmt.Sprintf("metric_%d", i), 1, map[string]string{"env": "staging"})
		buf.Add([]*metricspb.ResourceMetrics{rm})
	}

	time.Sleep(500 * time.Millisecond)
	cancel()
	buf.Wait()

	total := countExportedDatapoints(exp)
	if total != 1000 {
		t.Fatalf("high volume relabel: expected 1000 datapoints, got %d", total)
	}
}
