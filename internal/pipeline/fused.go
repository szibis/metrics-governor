package pipeline

import (
	"time"

	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
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

// NewFusedProcessor creates a fused tenant+limits processor.
// Either or both processors may be nil.
func NewFusedProcessor(tenant TenantProcessor, limits LimitsEnforcer) *FusedProcessor {
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
