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

// --- Typed nil tests ---
// These test Go's "typed nil" pitfall: a nil concrete pointer wrapped in an
// interface is NOT == nil. Without defense, calling methods on it panics.
// See: https://go.dev/doc/faq#nil_error

func TestIsNilInterface(t *testing.T) {
	t.Run("plain nil", func(t *testing.T) {
		if !isNilInterface(nil) {
			t.Error("expected nil to be detected as nil")
		}
	})
	t.Run("typed nil TenantProcessor", func(t *testing.T) {
		var tp *mockTenant // nil concrete pointer
		var i TenantProcessor = tp
		if !isNilInterface(i) {
			t.Error("expected typed nil *mockTenant to be detected as nil")
		}
	})
	t.Run("typed nil LimitsEnforcer", func(t *testing.T) {
		var lp *mockLimits
		var i LimitsEnforcer = lp
		if !isNilInterface(i) {
			t.Error("expected typed nil *mockLimits to be detected as nil")
		}
	})
	t.Run("non-nil pointer", func(t *testing.T) {
		tp := &mockTenant{}
		if isNilInterface(tp) {
			t.Error("expected non-nil *mockTenant to NOT be detected as nil")
		}
	})
}

func TestNewFusedProcessor_TypedNilBothProcessors(t *testing.T) {
	// Both typed nils — should return nil FusedProcessor (same as plain nil)
	var tp *mockTenant
	var lp *mockLimits
	fp := NewFusedProcessor(tp, lp)
	if fp != nil {
		t.Error("expected nil FusedProcessor when both are typed nils")
	}
}

func TestNewFusedProcessor_TypedNilTenantWithRealLimits(t *testing.T) {
	// Typed nil tenant + real limits — should create processor, skip tenant
	var tp *mockTenant // nil concrete pointer → typed nil interface
	lp := &mockLimits{}
	fp := NewFusedProcessor(tp, lp)
	if fp == nil {
		t.Fatal("expected non-nil FusedProcessor when limits is real")
	}
	if fp.HasTenant() {
		t.Error("expected HasTenant=false after typed nil sanitization")
	}
	if !fp.HasLimits() {
		t.Error("expected HasLimits=true")
	}

	// Process should not panic
	rms := makeTestRMs(2)
	result := fp.Process(rms, "header")
	if len(result) != 2 {
		t.Errorf("expected 2 RMs, got %d", len(result))
	}
	if lp.calls != 1 {
		t.Errorf("expected 1 limits call, got %d", lp.calls)
	}
}

func TestNewFusedProcessor_RealTenantWithTypedNilLimits(t *testing.T) {
	// Real tenant + typed nil limits — should create processor, skip limits
	tp := &mockTenant{}
	var lp *mockLimits // typed nil
	fp := NewFusedProcessor(tp, lp)
	if fp == nil {
		t.Fatal("expected non-nil FusedProcessor when tenant is real")
	}
	if !fp.HasTenant() {
		t.Error("expected HasTenant=true")
	}
	if fp.HasLimits() {
		t.Error("expected HasLimits=false after typed nil sanitization")
	}

	rms := makeTestRMs(3)
	result := fp.Process(rms, "t1")
	if len(result) != 3 {
		t.Errorf("expected 3 RMs, got %d", len(result))
	}
	if tp.calls != 1 {
		t.Errorf("expected 1 tenant call, got %d", tp.calls)
	}
	if tp.lastHdr != "t1" {
		t.Errorf("expected header t1, got %s", tp.lastHdr)
	}
}

func TestNewFusedProcessor_TypedNilTenantNilLimits(t *testing.T) {
	// Typed nil tenant + plain nil limits — should return nil
	var tp *mockTenant
	fp := NewFusedProcessor(tp, nil)
	if fp != nil {
		t.Error("expected nil FusedProcessor when tenant is typed nil and limits is nil")
	}
}

func TestNewFusedProcessor_NilTenantTypedNilLimits(t *testing.T) {
	// Plain nil tenant + typed nil limits — should return nil
	var lp *mockLimits
	fp := NewFusedProcessor(nil, lp)
	if fp != nil {
		t.Error("expected nil FusedProcessor when tenant is nil and limits is typed nil")
	}
}
