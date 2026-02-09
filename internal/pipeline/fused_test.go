package pipeline

import (
	"testing"

	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// mockTenant records calls and optionally filters metrics.
type mockTenant struct {
	calls   int
	lastHdr string
	dropAll bool
}

func (m *mockTenant) ProcessBatch(rms []*metricspb.ResourceMetrics, hdr string) []*metricspb.ResourceMetrics {
	m.calls++
	m.lastHdr = hdr
	if m.dropAll {
		return nil
	}
	return rms
}

// mockLimits records calls and optionally filters metrics.
type mockLimits struct {
	calls   int
	dropAll bool
}

func (m *mockLimits) Process(rms []*metricspb.ResourceMetrics) []*metricspb.ResourceMetrics {
	m.calls++
	if m.dropAll {
		return nil
	}
	return rms
}

func makeTestRMs(n int) []*metricspb.ResourceMetrics {
	rms := make([]*metricspb.ResourceMetrics, n)
	for i := range rms {
		rms[i] = &metricspb.ResourceMetrics{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{{
				Metrics: []*metricspb.Metric{{
					Name: "test_metric",
					Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{
						DataPoints: []*metricspb.NumberDataPoint{{}},
					}},
				}},
			}},
		}
	}
	return rms
}

func TestNewFusedProcessor_NilBoth(t *testing.T) {
	fp := NewFusedProcessor(nil, nil)
	if fp != nil {
		t.Error("expected nil when both processors are nil")
	}
}

func TestNewFusedProcessor_TenantOnly(t *testing.T) {
	tp := &mockTenant{}
	fp := NewFusedProcessor(tp, nil)
	if fp == nil {
		t.Fatal("expected non-nil processor")
	}
	if !fp.HasTenant() {
		t.Error("expected HasTenant=true")
	}
	if fp.HasLimits() {
		t.Error("expected HasLimits=false")
	}

	rms := makeTestRMs(3)
	result := fp.Process(rms, "tenant-a")
	if len(result) != 3 {
		t.Errorf("expected 3 RMs, got %d", len(result))
	}
	if tp.calls != 1 {
		t.Errorf("expected 1 tenant call, got %d", tp.calls)
	}
	if tp.lastHdr != "tenant-a" {
		t.Errorf("expected header tenant-a, got %s", tp.lastHdr)
	}
}

func TestNewFusedProcessor_LimitsOnly(t *testing.T) {
	lp := &mockLimits{}
	fp := NewFusedProcessor(nil, lp)
	if fp == nil {
		t.Fatal("expected non-nil processor")
	}
	if fp.HasTenant() {
		t.Error("expected HasTenant=false")
	}
	if !fp.HasLimits() {
		t.Error("expected HasLimits=true")
	}

	rms := makeTestRMs(2)
	result := fp.Process(rms, "")
	if len(result) != 2 {
		t.Errorf("expected 2 RMs, got %d", len(result))
	}
	if lp.calls != 1 {
		t.Errorf("expected 1 limits call, got %d", lp.calls)
	}
}

func TestFusedProcessor_BothProcessors(t *testing.T) {
	tp := &mockTenant{}
	lp := &mockLimits{}
	fp := NewFusedProcessor(tp, lp)

	rms := makeTestRMs(5)
	result := fp.Process(rms, "hdr")
	if len(result) != 5 {
		t.Errorf("expected 5 RMs, got %d", len(result))
	}
	if tp.calls != 1 || lp.calls != 1 {
		t.Errorf("expected 1 call each, got tenant=%d limits=%d", tp.calls, lp.calls)
	}
}

func TestFusedProcessor_TenantDropsAll(t *testing.T) {
	tp := &mockTenant{dropAll: true}
	lp := &mockLimits{}
	fp := NewFusedProcessor(tp, lp)

	rms := makeTestRMs(3)
	result := fp.Process(rms, "")
	if len(result) != 0 {
		t.Errorf("expected 0 RMs after tenant drop, got %d", len(result))
	}
	// Limits should NOT be called when tenant returns empty
	if lp.calls != 0 {
		t.Errorf("expected 0 limits calls when tenant drops all, got %d", lp.calls)
	}
}

func TestFusedProcessor_LimitsDropsAll(t *testing.T) {
	tp := &mockTenant{}
	lp := &mockLimits{dropAll: true}
	fp := NewFusedProcessor(tp, lp)

	rms := makeTestRMs(3)
	result := fp.Process(rms, "")
	if len(result) != 0 {
		t.Errorf("expected 0 RMs after limits drop, got %d", len(result))
	}
	if tp.calls != 1 {
		t.Errorf("expected 1 tenant call, got %d", tp.calls)
	}
	if lp.calls != 1 {
		t.Errorf("expected 1 limits call, got %d", lp.calls)
	}
}

func TestFusedProcessor_EmptyInput(t *testing.T) {
	tp := &mockTenant{}
	lp := &mockLimits{}
	fp := NewFusedProcessor(tp, lp)

	result := fp.Process(nil, "")
	if len(result) != 0 {
		t.Errorf("expected 0 RMs for nil input, got %d", len(result))
	}
}

func TestFusedProcessor_OrderingTenantBeforeLimits(t *testing.T) {
	// Verify tenant runs before limits by having tenant annotate
	// and limits verify the annotation exists.
	order := make([]string, 0, 2)

	tp := &recordingTenant{order: &order}
	lp := &recordingLimits{order: &order}
	fp := NewFusedProcessor(tp, lp)

	rms := makeTestRMs(1)
	fp.Process(rms, "")

	if len(order) != 2 || order[0] != "tenant" || order[1] != "limits" {
		t.Errorf("expected [tenant, limits], got %v", order)
	}
}

type recordingTenant struct {
	order *[]string
}

func (r *recordingTenant) ProcessBatch(rms []*metricspb.ResourceMetrics, _ string) []*metricspb.ResourceMetrics {
	*r.order = append(*r.order, "tenant")
	return rms
}

type recordingLimits struct {
	order *[]string
}

func (r *recordingLimits) Process(rms []*metricspb.ResourceMetrics) []*metricspb.ResourceMetrics {
	*r.order = append(*r.order, "limits")
	return rms
}
