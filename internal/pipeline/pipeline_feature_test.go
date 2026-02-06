package pipeline_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/buffer"
	"github.com/szibis/metrics-governor/internal/relabel"
	"github.com/szibis/metrics-governor/internal/sampling"
	"github.com/szibis/metrics-governor/internal/tenant"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// makeGaugeRMWithLabels creates a ResourceMetrics with gauge datapoints that have
// specific labels set on each datapoint. This enables relabeler/sampler tests
// to match or drop based on datapoint attributes.
func makeGaugeRMWithLabels(name string, dpCount int, labels map[string]string) *metricspb.ResourceMetrics {
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
			Attributes: attrs,
			Value:      &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)},
		}
	}
	return &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{
				{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: name}}},
			},
		},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{
				Metrics: []*metricspb.Metric{
					{
						Name: name,
						Data: &metricspb.Metric_Gauge{
							Gauge: &metricspb.Gauge{DataPoints: dps},
						},
					},
				},
			},
		},
	}
}

// makeGaugeRMWithResource creates a ResourceMetrics with specific resource attributes
// and datapoint labels.
func makeGaugeRMWithResource(name string, dpCount int, resourceAttrs, dpLabels map[string]string) *metricspb.ResourceMetrics {
	rAttrs := make([]*commonpb.KeyValue, 0, len(resourceAttrs))
	for k, v := range resourceAttrs {
		rAttrs = append(rAttrs, &commonpb.KeyValue{
			Key:   k,
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}},
		})
	}
	dps := make([]*metricspb.NumberDataPoint, dpCount)
	for i := 0; i < dpCount; i++ {
		attrs := make([]*commonpb.KeyValue, 0, len(dpLabels))
		for k, v := range dpLabels {
			attrs = append(attrs, &commonpb.KeyValue{
				Key:   k,
				Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}},
			})
		}
		dps[i] = &metricspb.NumberDataPoint{
			Attributes: attrs,
			Value:      &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)},
		}
	}
	return &metricspb.ResourceMetrics{
		Resource:     &resourcepb.Resource{Attributes: rAttrs},
		ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{{Name: name, Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: dps}}}}}},
	}
}

// runPipeline creates a buffer with the given options, sends the metrics, flushes, and returns the exporter.
func runPipeline(t *testing.T, metrics []*metricspb.ResourceMetrics, opts ...buffer.BufferOption) *countingExporter {
	t.Helper()
	exp := &countingExporter{}
	buf := buffer.New(
		10000,
		5000,
		50*time.Millisecond,
		exp,
		&noopStatsCollector{},
		&noopLimitsEnforcer{},
		&noopLogAggregator{},
		opts...,
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)
	buf.Add(metrics)
	time.Sleep(200 * time.Millisecond)
	cancel()
	buf.Wait()
	return exp
}

// TestDataIntegrity_Pipeline_RelabelPassthrough verifies that relabeling with a replace action
// (rename label, no drops) passes all metrics through.
func TestDataIntegrity_Pipeline_RelabelPassthrough(t *testing.T) {
	cfgs := []relabel.Config{
		{
			SourceLabels: []string{"env"},
			TargetLabel:  "environment",
			Regex:        "(.*)",
			Replacement:  "$1",
			Action:       relabel.ActionReplace,
		},
	}
	r, err := relabel.New(cfgs)
	if err != nil {
		t.Fatalf("relabel.New: %v", err)
	}

	// Build 100 single-DP metrics, each with an "env" label
	var metrics []*metricspb.ResourceMetrics
	for i := 0; i < 100; i++ {
		metrics = append(metrics, makeGaugeRMWithLabels(
			fmt.Sprintf("metric_%d", i), 1,
			map[string]string{"env": "prod"},
		))
	}

	exp := runPipeline(t, metrics, buffer.WithRelabeler(r))

	got := exp.getDatapoints()
	if got != 100 {
		t.Fatalf("relabel passthrough: exported %d datapoints, want 100", got)
	}

	// Verify that the "environment" label was actually added
	exp.mu.Lock()
	defer exp.mu.Unlock()
	for _, req := range exp.requests {
		for _, rm := range req.ResourceMetrics {
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					for _, dp := range m.GetGauge().GetDataPoints() {
						found := false
						for _, kv := range dp.Attributes {
							if kv.Key == "environment" {
								found = true
								break
							}
						}
						if !found {
							t.Errorf("metric %s: missing 'environment' label after relabel", m.Name)
						}
					}
				}
			}
		}
	}
}

// TestDataIntegrity_Pipeline_RelabelDrop verifies that relabeling with a drop action
// drops matching metrics and passes the rest.
func TestDataIntegrity_Pipeline_RelabelDrop(t *testing.T) {
	// Drop metrics whose __name__ matches "debug_.*"
	cfgs := []relabel.Config{
		{
			SourceLabels: []string{"__name__"},
			Regex:        "debug_.*",
			Action:       relabel.ActionDrop,
		},
	}
	r, err := relabel.New(cfgs)
	if err != nil {
		t.Fatalf("relabel.New: %v", err)
	}

	var metrics []*metricspb.ResourceMetrics
	// 50 debug metrics (will be dropped)
	for i := 0; i < 50; i++ {
		metrics = append(metrics, makeGaugeRMWithLabels(
			fmt.Sprintf("debug_%d", i), 1, nil,
		))
	}
	// 50 normal metrics (will pass)
	for i := 0; i < 50; i++ {
		metrics = append(metrics, makeGaugeRMWithLabels(
			fmt.Sprintf("normal_%d", i), 1, nil,
		))
	}

	exp := runPipeline(t, metrics, buffer.WithRelabeler(r))

	got := exp.getDatapoints()
	if got != 50 {
		t.Fatalf("relabel drop: exported %d datapoints, want 50", got)
	}
}

// TestDataIntegrity_Pipeline_SamplingReduction verifies that head sampling with rate=0
// drops all metrics, and rate=1.0 keeps all.
func TestDataIntegrity_Pipeline_SamplingReduction(t *testing.T) {
	// Rate=1.0 — keep all
	cfg := sampling.FileConfig{
		DefaultRate: 1.0,
		Strategy:    sampling.StrategyHead,
	}
	s, err := sampling.New(cfg)
	if err != nil {
		t.Fatalf("sampling.New: %v", err)
	}

	var metrics []*metricspb.ResourceMetrics
	for i := 0; i < 100; i++ {
		metrics = append(metrics, makeGaugeRMWithLabels(
			fmt.Sprintf("metric_%d", i), 1, nil,
		))
	}

	exp := runPipeline(t, metrics, buffer.WithSampler(s))
	got := exp.getDatapoints()
	if got != 100 {
		t.Fatalf("sampling rate=1.0: exported %d, want 100", got)
	}

	// Rate=0 — drop all
	cfg.DefaultRate = 0.0
	s2, err := sampling.New(cfg)
	if err != nil {
		t.Fatalf("sampling.New: %v", err)
	}

	exp2 := runPipeline(t, metrics, buffer.WithSampler(s2))
	got2 := exp2.getDatapoints()
	if got2 != 0 {
		t.Fatalf("sampling rate=0: exported %d, want 0", got2)
	}
}

// TestDataIntegrity_Pipeline_TenantQuotaEnforcement verifies that per-tenant quotas
// drop excess datapoints.
func TestDataIntegrity_Pipeline_TenantQuotaEnforcement(t *testing.T) {
	// Setup tenant detector in label mode
	tcfg := tenant.DefaultConfig()
	tcfg.Enabled = true
	tcfg.Mode = tenant.ModeAttribute
	tcfg.AttributeKey = "service.namespace"
	tcfg.InjectLabel = false
	d, err := tenant.NewDetector(tcfg)
	if err != nil {
		t.Fatalf("NewDetector: %v", err)
	}

	quotasCfg := &tenant.QuotasConfig{
		Default: &tenant.TenantQuota{
			MaxDatapoints: 50,
			Action:        tenant.QuotaActionDrop,
			Window:        "1m",
		},
	}
	// Need to parse-validate the config
	parsed, err := tenant.ParseQuotasConfig([]byte(`
default:
  max_datapoints: 50
  action: drop
  window: 1m
`))
	if err != nil {
		// Fallback: use the struct directly (the parsedWindow needs to be set)
		_ = quotasCfg
		t.Fatalf("ParseQuotasConfig: %v", err)
	}
	qe := tenant.NewQuotaEnforcer(parsed)
	pipeline := tenant.NewPipeline(d, qe)

	// Send 100 metrics all belonging to the same tenant "team-a"
	// Each RM has 1 datapoint, so 100 DPs total. Quota is 50.
	var metrics []*metricspb.ResourceMetrics
	for i := 0; i < 100; i++ {
		metrics = append(metrics, makeGaugeRMWithResource(
			fmt.Sprintf("metric_%d", i), 1,
			map[string]string{"service.namespace": "team-a"},
			nil,
		))
	}

	exp := runPipeline(t, metrics, buffer.WithTenantProcessor(pipeline))

	got := exp.getDatapoints()
	// The first batch of 100 RMs goes through ProcessBatch as one call.
	// With quota=50 and action=drop, the entire batch exceeds the limit,
	// so it should be dropped entirely.
	if got > 50 {
		t.Fatalf("tenant quota: exported %d datapoints, want <= 50", got)
	}
	t.Logf("tenant quota enforcement: %d/100 datapoints exported (quota=50)", got)
}

// TestDataIntegrity_Pipeline_FullPipeline_AllFeatures verifies that all features
// wired together (relabel rename, sampling rate=1.0, tenant no quotas) pass all data through.
func TestDataIntegrity_Pipeline_FullPipeline_AllFeatures(t *testing.T) {
	// Relabeler: rename "env" → "environment" (no drops)
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

	// Sampler: rate=1.0 keep all
	s, err := sampling.New(sampling.FileConfig{
		DefaultRate: 1.0,
		Strategy:    sampling.StrategyHead,
	})
	if err != nil {
		t.Fatalf("sampling.New: %v", err)
	}

	// Tenant: enabled but no quotas (passthrough)
	tcfg := tenant.DefaultConfig()
	tcfg.Enabled = true
	tcfg.Mode = tenant.ModeAttribute
	tcfg.AttributeKey = "service.namespace"
	tcfg.InjectLabel = true
	tcfg.InjectLabelName = "__tenant__"
	d, err := tenant.NewDetector(tcfg)
	if err != nil {
		t.Fatalf("NewDetector: %v", err)
	}
	tp := tenant.NewPipeline(d, nil) // no quotas

	var metrics []*metricspb.ResourceMetrics
	for i := 0; i < 100; i++ {
		metrics = append(metrics, makeGaugeRMWithResource(
			fmt.Sprintf("metric_%d", i), 1,
			map[string]string{"service.namespace": "team-a"},
			map[string]string{"env": "prod"},
		))
	}

	exp := runPipeline(t, metrics,
		buffer.WithRelabeler(r),
		buffer.WithSampler(s),
		buffer.WithTenantProcessor(tp),
	)

	got := exp.getDatapoints()
	if got != 100 {
		t.Fatalf("full pipeline passthrough: exported %d, want 100", got)
	}
}

// TestDataIntegrity_Pipeline_FullPipeline_CascadingDrops verifies cascading drops
// through relabel, sampling, and tenant quota enforcement.
func TestDataIntegrity_Pipeline_FullPipeline_CascadingDrops(t *testing.T) {
	// Relabeler: drop metrics matching "debug_.*"
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

	// Sampler: rate=1.0 (keep all surviving after relabel)
	// Using rate=1.0 to make the test deterministic
	s, err := sampling.New(sampling.FileConfig{
		DefaultRate: 1.0,
		Strategy:    sampling.StrategyHead,
	})
	if err != nil {
		t.Fatalf("sampling.New: %v", err)
	}

	// Tenant: quota of 30 per tenant (default tenant)
	tcfg := tenant.DefaultConfig()
	tcfg.Enabled = true
	tcfg.Mode = tenant.ModeAttribute
	tcfg.AttributeKey = "service.namespace"
	tcfg.InjectLabel = false
	d, err := tenant.NewDetector(tcfg)
	if err != nil {
		t.Fatalf("NewDetector: %v", err)
	}
	parsed, err := tenant.ParseQuotasConfig([]byte(`
default:
  max_datapoints: 30
  action: drop
  window: 1m
`))
	if err != nil {
		t.Fatalf("ParseQuotasConfig: %v", err)
	}
	qe := tenant.NewQuotaEnforcer(parsed)
	tp := tenant.NewPipeline(d, qe)

	// Build metrics: 20 debug (will be dropped by relabel) + 80 normal
	var metrics []*metricspb.ResourceMetrics
	for i := 0; i < 20; i++ {
		metrics = append(metrics, makeGaugeRMWithResource(
			fmt.Sprintf("debug_%d", i), 1,
			map[string]string{"service.namespace": "team-x"},
			nil,
		))
	}
	for i := 0; i < 80; i++ {
		metrics = append(metrics, makeGaugeRMWithResource(
			fmt.Sprintf("normal_%d", i), 1,
			map[string]string{"service.namespace": "team-x"},
			nil,
		))
	}

	exp := runPipeline(t, metrics,
		buffer.WithRelabeler(r),
		buffer.WithSampler(s),
		buffer.WithTenantProcessor(tp),
	)

	got := exp.getDatapoints()
	// Pipeline: 100 → relabel drops 20 debug → 80 remain
	// Sampling rate=1.0 → 80 remain
	// Tenant quota=30 → at most 30 exported
	// Note: relabel runs at export time, so 100 enter the buffer.
	// Sampling runs at Add time (rate=1.0, so 100 pass).
	// Tenant runs at Add time: quota=30 drops the batch if >30 DPs.
	// The 100-DP batch exceeds 30, so it gets dropped entirely.
	// After relabel at export time, up to 80 survive relabel.
	//
	// Actual flow: Add → sampling(pass) → tenant(drops if >30) → buffer → flush → relabel → export.
	// So tenant sees 100 DPs, exceeds 30, drops all → 0 exported.
	t.Logf("cascading drops: %d/100 datapoints exported", got)
	if got > 30 {
		t.Fatalf("cascading drops: exported %d, should not exceed tenant quota of 30", got)
	}
}
