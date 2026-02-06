package functional

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/buffer"
	"github.com/szibis/metrics-governor/internal/stats"
	"github.com/szibis/metrics-governor/internal/tenant"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

func makeTenantRM(namespace, metricName string, dpCount int) *metricspb.ResourceMetrics {
	dps := make([]*metricspb.NumberDataPoint, dpCount)
	for i := 0; i < dpCount; i++ {
		dps[i] = &metricspb.NumberDataPoint{
			TimeUnixNano: uint64(time.Now().UnixNano()),
			Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)},
		}
	}
	return &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{
				{Key: "service.namespace", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: namespace}}},
			},
		},
		ScopeMetrics: []*metricspb.ScopeMetrics{{
			Metrics: []*metricspb.Metric{{
				Name: metricName,
				Data: &metricspb.Metric_Gauge{
					Gauge: &metricspb.Gauge{DataPoints: dps},
				},
			}},
		}},
	}
}

// TestFunctional_Tenant_LabelInjection verifies that the tenant processor
// injects the __tenant__ label on all datapoints through a real buffer pipeline.
func TestFunctional_Tenant_LabelInjection(t *testing.T) {
	cfg := tenant.DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = tenant.ModeAttribute
	cfg.AttributeKey = "service.namespace"
	cfg.InjectLabel = true
	cfg.InjectLabelName = "__tenant__"
	d, err := tenant.NewDetector(cfg)
	if err != nil {
		t.Fatalf("NewDetector: %v", err)
	}
	tp := tenant.NewPipeline(d, nil) // No quotas

	exp := &bufferMockExporter{}
	statsC := stats.NewCollector(nil)
	buf := buffer.New(1000, 100, 50*time.Millisecond, exp, statsC, nil, nil, buffer.WithTenantProcessor(tp))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go buf.Start(ctx)

	for i := 0; i < 10; i++ {
		rm := makeTenantRM("team-alpha", fmt.Sprintf("metric_%d", i), 1)
		buf.Add([]*metricspb.ResourceMetrics{rm})
	}

	time.Sleep(300 * time.Millisecond)
	cancel()
	buf.Wait()

	total := countExportedDatapoints(exp)
	if total != 10 {
		t.Fatalf("expected 10 datapoints, got %d", total)
	}

	// Verify __tenant__ label was injected
	for _, req := range exp.getExports() {
		for _, rm := range req.ResourceMetrics {
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					for _, dp := range m.GetGauge().GetDataPoints() {
						found := false
						for _, kv := range dp.Attributes {
							if kv.Key == "__tenant__" && kv.Value.GetStringValue() == "team-alpha" {
								found = true
							}
						}
						if !found {
							t.Errorf("metric %s: missing __tenant__=team-alpha label", m.Name)
						}
					}
				}
			}
		}
	}
}

// TestFunctional_Tenant_QuotaDrop verifies that tenant quota enforcement
// drops excess data through a real buffer pipeline.
func TestFunctional_Tenant_QuotaDrop(t *testing.T) {
	cfg := tenant.DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = tenant.ModeAttribute
	cfg.AttributeKey = "service.namespace"
	cfg.InjectLabel = false
	d, err := tenant.NewDetector(cfg)
	if err != nil {
		t.Fatalf("NewDetector: %v", err)
	}

	parsed, err := tenant.ParseQuotasConfig([]byte(`
default:
  max_datapoints: 5
  action: drop
  window: 1m
`))
	if err != nil {
		t.Fatalf("ParseQuotasConfig: %v", err)
	}
	qe := tenant.NewQuotaEnforcer(parsed)
	tp := tenant.NewPipeline(d, qe)

	exp := &bufferMockExporter{}
	statsC := stats.NewCollector(nil)
	buf := buffer.New(1000, 100, 50*time.Millisecond, exp, statsC, nil, nil, buffer.WithTenantProcessor(tp))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go buf.Start(ctx)

	// Send 20 metrics for the same tenant (quota=5)
	for i := 0; i < 20; i++ {
		rm := makeTenantRM("over-quota-ns", fmt.Sprintf("metric_%d", i), 1)
		buf.Add([]*metricspb.ResourceMetrics{rm})
	}

	time.Sleep(300 * time.Millisecond)
	cancel()
	buf.Wait()

	total := countExportedDatapoints(exp)
	if total > 5 {
		t.Fatalf("expected at most 5 datapoints (quota enforcement), got %d", total)
	}
	t.Logf("tenant quota drop: %d/20 datapoints exported (quota=5)", total)
}

// TestFunctional_Tenant_MultiTenant verifies independent per-tenant quota enforcement
// through a real buffer pipeline.
func TestFunctional_Tenant_MultiTenant(t *testing.T) {
	cfg := tenant.DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = tenant.ModeAttribute
	cfg.AttributeKey = "service.namespace"
	cfg.InjectLabel = true
	cfg.InjectLabelName = "__tenant__"
	d, err := tenant.NewDetector(cfg)
	if err != nil {
		t.Fatalf("NewDetector: %v", err)
	}

	// Each tenant gets quota of 100 — no drops expected
	parsed, err := tenant.ParseQuotasConfig([]byte(`
default:
  max_datapoints: 100
  action: drop
  window: 1m
`))
	if err != nil {
		t.Fatalf("ParseQuotasConfig: %v", err)
	}
	qe := tenant.NewQuotaEnforcer(parsed)
	tp := tenant.NewPipeline(d, qe)

	exp := &bufferMockExporter{}
	statsC := stats.NewCollector(nil)
	buf := buffer.New(10000, 500, 50*time.Millisecond, exp, statsC, nil, nil, buffer.WithTenantProcessor(tp))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go buf.Start(ctx)

	// 3 tenants, 10 metrics each (30 total, well under quota)
	tenants := []string{"team-a", "team-b", "team-c"}
	for _, ns := range tenants {
		for i := 0; i < 10; i++ {
			rm := makeTenantRM(ns, fmt.Sprintf("metric_%d", i), 1)
			buf.Add([]*metricspb.ResourceMetrics{rm})
		}
	}

	time.Sleep(300 * time.Millisecond)
	cancel()
	buf.Wait()

	total := countExportedDatapoints(exp)
	if total != 30 {
		t.Fatalf("multi-tenant: expected 30 datapoints, got %d", total)
	}

	// Verify all three tenants have __tenant__ labels
	tenantsSeen := make(map[string]bool)
	for _, req := range exp.getExports() {
		for _, rm := range req.ResourceMetrics {
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					for _, dp := range m.GetGauge().GetDataPoints() {
						for _, kv := range dp.Attributes {
							if kv.Key == "__tenant__" {
								tenantsSeen[kv.Value.GetStringValue()] = true
							}
						}
					}
				}
			}
		}
	}
	for _, ns := range tenants {
		if !tenantsSeen[ns] {
			t.Errorf("missing tenant %q in exported data", ns)
		}
	}
}
