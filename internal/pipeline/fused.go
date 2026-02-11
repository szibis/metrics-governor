package pipeline

import (
	"reflect"
	"time"

	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
)

// TenantProcessor detects tenants and applies per-tenant quotas.
// Matches the buffer.TenantProcessor interface.
type TenantProcessor interface {
	ProcessBatch(resourceMetrics []*metricspb.ResourceMetrics, headerTenant string) []*metricspb.ResourceMetrics
}

// LimitsEnforcer enforces metrics limits.
// Matches the buffer.LimitsEnforcer interface.
type LimitsEnforcer interface {
	Process(resourceMetrics []*metricspb.ResourceMetrics) []*metricspb.ResourceMetrics
}

// FusedProcessor combines tenant detection and limits enforcement into a
// single pipeline step, recorded under one timing metric. This avoids the
// overhead of recording two separate pipeline stages and provides a single
// "fused_tenant_limits" timing entry.
//
// Each processor is still called independently (tenant first, then limits)
// because tenant detection annotates metrics with labels that limits rules
// may depend on. The fusion benefit comes from:
//   - Single pipeline timing record instead of two
//   - Avoiding an intermediate nil check between stages
//   - Clear signal that these two stages run as one unit
type FusedProcessor struct {
	tenant TenantProcessor // optional
	limits LimitsEnforcer  // optional
}

// isNilInterface returns true if i is nil or a typed nil (interface holding
// a nil pointer). This defends against Go's typed nil pitfall where a nil
// concrete pointer wrapped in an interface passes standard nil checks.
func isNilInterface(i interface{}) bool {
	if i == nil {
		return true
	}
	v := reflect.ValueOf(i)
	return v.Kind() == reflect.Ptr && v.IsNil()
}

// NewFusedProcessor creates a fused tenant+limits processor.
// Either or both processors may be nil. Typed nils (a nil concrete pointer
// wrapped in an interface) are sanitized to true nils to prevent panics.
func NewFusedProcessor(tenant TenantProcessor, limits LimitsEnforcer) *FusedProcessor {
	// Sanitize typed nils: a nil *tenant.Pipeline wrapped in TenantProcessor
	// interface is NOT == nil, but calling methods on it will panic.
	if isNilInterface(tenant) {
		tenant = nil
	}
	if isNilInterface(limits) {
		limits = nil
	}
	if tenant == nil && limits == nil {
		return nil
	}
	return &FusedProcessor{
		tenant: tenant,
		limits: limits,
	}
}

// Process runs tenant detection (which annotates RMs with tenant labels),
// then limits enforcement in a single timed pipeline step.
func (p *FusedProcessor) Process(resourceMetrics []*metricspb.ResourceMetrics, headerTenant string) []*metricspb.ResourceMetrics {
	start := time.Now()

	if p.tenant != nil {
		resourceMetrics = p.tenant.ProcessBatch(resourceMetrics, headerTenant)
	}

	if p.limits != nil && len(resourceMetrics) > 0 {
		resourceMetrics = p.limits.Process(resourceMetrics)
	}

	Record("fused_tenant_limits", Since(start))
	return resourceMetrics
}

// HasTenant returns whether the fused processor has a tenant component.
func (p *FusedProcessor) HasTenant() bool {
	return p.tenant != nil
}

// HasLimits returns whether the fused processor has a limits component.
func (p *FusedProcessor) HasLimits() bool {
	return p.limits != nil
}
