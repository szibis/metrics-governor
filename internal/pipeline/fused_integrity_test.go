package pipeline

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
)

// --- Mock implementations for data-integrity tests ---

// annotatingTenant adds a "tenant" resource attribute to every ResourceMetrics.
type annotatingTenant struct {
	mu    sync.Mutex
	calls int
}

func (a *annotatingTenant) ProcessBatch(rms []*metricspb.ResourceMetrics, hdr string) []*metricspb.ResourceMetrics {
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	tenantVal := hdr
	if tenantVal == "" {
		tenantVal = "default"
	}
	for _, rm := range rms {
		if rm.Resource == nil {
			rm.Resource = &resourcepb.Resource{}
		}
		rm.Resource.Attributes = append(rm.Resource.Attributes, &commonpb.KeyValue{
			Key:   "tenant",
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: tenantVal}},
		})
	}
	return rms
}

// filteringLimits drops any metric whose name contains the given substring.
type filteringLimits struct {
	mu       sync.Mutex
	calls    int
	dropName string // metrics with names containing this substring are dropped
}

func (f *filteringLimits) Process(rms []*metricspb.ResourceMetrics) []*metricspb.ResourceMetrics {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	var result []*metricspb.ResourceMetrics
	for _, rm := range rms {
		var keptScopes []*metricspb.ScopeMetrics
		for _, sm := range rm.GetScopeMetrics() {
			var keptMetrics []*metricspb.Metric
			for _, m := range sm.GetMetrics() {
				if !strings.Contains(m.Name, f.dropName) {
					keptMetrics = append(keptMetrics, m)
				}
			}
			if len(keptMetrics) > 0 {
				keptScopes = append(keptScopes, &metricspb.ScopeMetrics{
					Scope:   sm.Scope,
					Metrics: keptMetrics,
				})
			}
		}
		if len(keptScopes) > 0 {
			result = append(result, &metricspb.ResourceMetrics{
				Resource:     rm.Resource,
				ScopeMetrics: keptScopes,
			})
		}
	}
	return result
}

// passthroughTenant returns the input unchanged (no annotation, no drop).
type passthroughTenant struct {
	callCount atomic.Int64
}

func (p *passthroughTenant) ProcessBatch(rms []*metricspb.ResourceMetrics, _ string) []*metricspb.ResourceMetrics {
	p.callCount.Add(1)
	return rms
}

// passthroughLimits returns the input unchanged.
type passthroughLimits struct {
	callCount atomic.Int64
}

func (p *passthroughLimits) Process(rms []*metricspb.ResourceMetrics) []*metricspb.ResourceMetrics {
	p.callCount.Add(1)
	return rms
}

// --- Helpers ---

// makeIntegrityRMs builds n ResourceMetrics, each with scopeCount ScopeMetrics
// and one gauge metric per scope. Metric names follow the pattern "{prefix}_rm{i}_scope{j}".
func makeIntegrityRMs(n, scopeCount int, prefix string) []*metricspb.ResourceMetrics {
	rms := make([]*metricspb.ResourceMetrics, n)
	for i := range rms {
		scopes := make([]*metricspb.ScopeMetrics, scopeCount)
		for j := range scopes {
			scopes[j] = &metricspb.ScopeMetrics{
				Metrics: []*metricspb.Metric{{
					Name: fmt.Sprintf("%s_rm%d_scope%d", prefix, i, j),
					Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{
						DataPoints: []*metricspb.NumberDataPoint{{
							Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i*scopeCount + j)},
						}},
					}},
				}},
			}
		}
		rms[i] = &metricspb.ResourceMetrics{
			Resource:     &resourcepb.Resource{},
			ScopeMetrics: scopes,
		}
	}
	return rms
}

// collectMetricNames extracts all metric names from a slice of ResourceMetrics.
func collectMetricNames(rms []*metricspb.ResourceMetrics) map[string]bool {
	names := make(map[string]bool)
	for _, rm := range rms {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				names[m.Name] = true
			}
		}
	}
	return names
}

// countTotalDatapoints counts all gauge datapoints across all ResourceMetrics.
func countTotalDatapoints(rms []*metricspb.ResourceMetrics) int {
	count := 0
	for _, rm := range rms {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				if g := m.GetGauge(); g != nil {
					count += len(g.GetDataPoints())
				}
			}
		}
	}
	return count
}

// --- Tests ---

// TestDataIntegrity_FusedProcessor_MatchesSeparate verifies that FusedProcessor.Process()
// produces identical results to calling tenant.ProcessBatch() then limits.Process() separately.
// Uses 5 ResourceMetrics with 3 ScopeMetrics each (15 metrics total).
func TestDataIntegrity_FusedProcessor_MatchesSeparate(t *testing.T) {
	t.Parallel()

	const rmCount = 5
	const scopeCount = 3

	// Build two identical copies of the same input (since processors may mutate in place).
	inputFused := makeIntegrityRMs(rmCount, scopeCount, "match")
	inputSeparate := makeIntegrityRMs(rmCount, scopeCount, "match")

	// Use passthrough mocks so both paths produce the same output structure.
	tenantFused := &passthroughTenant{}
	limitsFused := &passthroughLimits{}
	fp := NewFusedProcessor(tenantFused, limitsFused)
	if fp == nil {
		t.Fatal("expected non-nil FusedProcessor")
	}

	tenantSep := &passthroughTenant{}
	limitsSep := &passthroughLimits{}

	// Path (a): fused
	resultFused := fp.Process(inputFused, "test-tenant")

	// Path (b): separate — tenant first, then limits
	resultSeparate := tenantSep.ProcessBatch(inputSeparate, "test-tenant")
	if len(resultSeparate) > 0 {
		resultSeparate = limitsSep.Process(resultSeparate)
	}

	// Verify both paths were called
	if tenantFused.callCount.Load() != 1 {
		t.Errorf("fused tenant calls = %d, want 1", tenantFused.callCount.Load())
	}
	if limitsFused.callCount.Load() != 1 {
		t.Errorf("fused limits calls = %d, want 1", limitsFused.callCount.Load())
	}
	if tenantSep.callCount.Load() != 1 {
		t.Errorf("separate tenant calls = %d, want 1", tenantSep.callCount.Load())
	}
	if limitsSep.callCount.Load() != 1 {
		t.Errorf("separate limits calls = %d, want 1", limitsSep.callCount.Load())
	}

	// Verify same number of ResourceMetrics
	if len(resultFused) != len(resultSeparate) {
		t.Fatalf("ResourceMetrics count: fused=%d, separate=%d", len(resultFused), len(resultSeparate))
	}
	if len(resultFused) != rmCount {
		t.Fatalf("expected %d ResourceMetrics, got %d", rmCount, len(resultFused))
	}

	// Verify same metric names
	namesFused := collectMetricNames(resultFused)
	namesSeparate := collectMetricNames(resultSeparate)
	if len(namesFused) != len(namesSeparate) {
		t.Fatalf("metric name count: fused=%d, separate=%d", len(namesFused), len(namesSeparate))
	}
	for name := range namesFused {
		if !namesSeparate[name] {
			t.Errorf("metric %q found in fused but not in separate", name)
		}
	}
	expectedNames := rmCount * scopeCount
	if len(namesFused) != expectedNames {
		t.Errorf("expected %d unique metric names, got %d", expectedNames, len(namesFused))
	}

	// Verify same datapoint counts
	dpFused := countTotalDatapoints(resultFused)
	dpSeparate := countTotalDatapoints(resultSeparate)
	if dpFused != dpSeparate {
		t.Errorf("datapoint count: fused=%d, separate=%d", dpFused, dpSeparate)
	}
	if dpFused != expectedNames {
		t.Errorf("expected %d datapoints, got %d", expectedNames, dpFused)
	}

	// Verify per-RM ScopeMetrics structure matches
	for i := 0; i < len(resultFused); i++ {
		fusedScopes := resultFused[i].GetScopeMetrics()
		sepScopes := resultSeparate[i].GetScopeMetrics()
		if len(fusedScopes) != len(sepScopes) {
			t.Errorf("RM[%d] scope count: fused=%d, separate=%d", i, len(fusedScopes), len(sepScopes))
			continue
		}
		for j := 0; j < len(fusedScopes); j++ {
			fusedMetrics := fusedScopes[j].GetMetrics()
			sepMetrics := sepScopes[j].GetMetrics()
			if len(fusedMetrics) != len(sepMetrics) {
				t.Errorf("RM[%d].Scope[%d] metric count: fused=%d, separate=%d",
					i, j, len(fusedMetrics), len(sepMetrics))
				continue
			}
			for k := 0; k < len(fusedMetrics); k++ {
				if fusedMetrics[k].Name != sepMetrics[k].Name {
					t.Errorf("RM[%d].Scope[%d].Metric[%d] name: fused=%q, separate=%q",
						i, j, k, fusedMetrics[k].Name, sepMetrics[k].Name)
				}
			}
		}
	}
}

// TestDurability_FusedProcessor_UnderConcurrentLoad runs 50 goroutines each calling
// FusedProcessor.Process() with distinct input for 1 second. Verifies no panics,
// no data races, and no nil pointer dereferences.
func TestDurability_FusedProcessor_UnderConcurrentLoad(t *testing.T) {
	t.Parallel()

	tp := &passthroughTenant{}
	lp := &passthroughLimits{}
	fp := NewFusedProcessor(tp, lp)
	if fp == nil {
		t.Fatal("expected non-nil FusedProcessor")
	}

	const goroutines = 10
	const iterationsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	errors := make(chan string, goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			tenant := fmt.Sprintf("tenant-%d", id)
			for iter := 0; iter < iterationsPerGoroutine; iter++ {
				input := makeIntegrityRMs(2, 1, fmt.Sprintf("g%d_i%d", id, iter))
				result := fp.Process(input, tenant)
				if result == nil {
					errors <- fmt.Sprintf("goroutine %d iter %d: got nil result", id, iter)
					return
				}
				if len(result) != 2 {
					errors <- fmt.Sprintf("goroutine %d iter %d: expected 2 RMs, got %d", id, iter, len(result))
					return
				}
			}
		}(g)
	}

	wg.Wait()
	close(errors)

	for errMsg := range errors {
		t.Error(errMsg)
	}

	tenantCalls := tp.callCount.Load()
	limitsCalls := lp.callCount.Load()

	expected := int64(goroutines * iterationsPerGoroutine)
	if tenantCalls != expected {
		t.Errorf("tenant calls = %d, want %d", tenantCalls, expected)
	}
	if limitsCalls != expected {
		t.Errorf("limits calls = %d, want %d", limitsCalls, expected)
	}
	t.Logf("concurrent load: %d tenant calls, %d limits calls across %d goroutines",
		tenantCalls, limitsCalls, goroutines)
}

// TestDataIntegrity_FusedProcessor_TenantAnnotation verifies that a tenant processor
// that adds a "tenant" resource attribute has that annotation preserved in the fused output.
func TestDataIntegrity_FusedProcessor_TenantAnnotation(t *testing.T) {
	t.Parallel()

	tp := &annotatingTenant{}
	lp := &passthroughLimits{}
	fp := NewFusedProcessor(tp, lp)
	if fp == nil {
		t.Fatal("expected non-nil FusedProcessor")
	}

	input := makeIntegrityRMs(3, 2, "annotate")
	result := fp.Process(input, "my-team")

	if len(result) != 3 {
		t.Fatalf("expected 3 ResourceMetrics, got %d", len(result))
	}

	for i, rm := range result {
		found := false
		for _, attr := range rm.GetResource().GetAttributes() {
			if attr.Key == "tenant" {
				val := attr.GetValue().GetStringValue()
				if val != "my-team" {
					t.Errorf("RM[%d]: tenant attribute = %q, want %q", i, val, "my-team")
				}
				found = true
				break
			}
		}
		if !found {
			t.Errorf("RM[%d]: missing 'tenant' resource attribute after fused processing", i)
		}
	}

	// Verify metric names are all preserved
	names := collectMetricNames(result)
	expectedNames := 3 * 2 // 3 RMs x 2 scopes
	if len(names) != expectedNames {
		t.Errorf("expected %d unique metric names, got %d", expectedNames, len(names))
	}
}

// TestDataIntegrity_FusedProcessor_LimitsFiltering verifies that a limits processor
// that drops metrics matching a name pattern correctly filters those metrics
// while preserving non-matching ones.
func TestDataIntegrity_FusedProcessor_LimitsFiltering(t *testing.T) {
	t.Parallel()

	tp := &passthroughTenant{}
	fl := &filteringLimits{dropName: "debug"}
	fp := NewFusedProcessor(tp, fl)
	if fp == nil {
		t.Fatal("expected non-nil FusedProcessor")
	}

	// Build input: 3 RMs with "keep" metrics, 2 RMs with "debug" metrics.
	var input []*metricspb.ResourceMetrics
	for i := 0; i < 3; i++ {
		input = append(input, &metricspb.ResourceMetrics{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{{
				Metrics: []*metricspb.Metric{{
					Name: fmt.Sprintf("keep_metric_%d", i),
					Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{
						DataPoints: []*metricspb.NumberDataPoint{{
							Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)},
						}},
					}},
				}},
			}},
		})
	}
	for i := 0; i < 2; i++ {
		input = append(input, &metricspb.ResourceMetrics{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{{
				Metrics: []*metricspb.Metric{{
					Name: fmt.Sprintf("debug_metric_%d", i),
					Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{
						DataPoints: []*metricspb.NumberDataPoint{{
							Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)},
						}},
					}},
				}},
			}},
		})
	}

	result := fp.Process(input, "")

	// Should only have 3 RMs (the "keep" ones)
	if len(result) != 3 {
		t.Fatalf("expected 3 ResourceMetrics after filtering, got %d", len(result))
	}

	names := collectMetricNames(result)
	for name := range names {
		if strings.Contains(name, "debug") {
			t.Errorf("debug metric %q should have been filtered out", name)
		}
	}

	// Verify all "keep" metrics survived
	for i := 0; i < 3; i++ {
		expected := fmt.Sprintf("keep_metric_%d", i)
		if !names[expected] {
			t.Errorf("expected metric %q to survive filtering", expected)
		}
	}

	// Verify datapoint count
	dps := countTotalDatapoints(result)
	if dps != 3 {
		t.Errorf("expected 3 datapoints after filtering, got %d", dps)
	}

	fl.mu.Lock()
	calls := fl.calls
	fl.mu.Unlock()
	if calls != 1 {
		t.Errorf("expected 1 limits call, got %d", calls)
	}
}

// TestDataIntegrity_FusedProcessor_TimingRecorded verifies that calling FusedProcessor.Process()
// records a non-zero timing value for the "fused_tenant_limits" component.
func TestDataIntegrity_FusedProcessor_TimingRecorded(t *testing.T) {
	// Not parallel: reads a shared Prometheus counter that other tests also increment.

	tp := &passthroughTenant{}
	lp := &passthroughLimits{}
	fp := NewFusedProcessor(tp, lp)
	if fp == nil {
		t.Fatal("expected non-nil FusedProcessor")
	}

	// Read the counter value before processing.
	counter := secondsCounters["fused_tenant_limits"]
	if counter == nil {
		t.Fatal("fused_tenant_limits counter not found in pre-resolved counters")
	}
	before := testutil.ToFloat64(counter)

	input := makeIntegrityRMs(3, 2, "timing")
	fp.Process(input, "tenant-timing")

	after := testutil.ToFloat64(counter)
	delta := after - before
	if delta <= 0 {
		t.Errorf("expected positive timing delta for fused_tenant_limits, got %v", delta)
	}
	t.Logf("fused_tenant_limits timing delta: %v seconds", delta)
}
