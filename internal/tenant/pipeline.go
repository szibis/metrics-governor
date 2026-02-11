package tenant

import (
	"sync"

	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
)

// Pipeline combines tenant detection and quota enforcement into a single
// processor that satisfies the buffer.TenantProcessor interface.
type Pipeline struct {
	mu       sync.RWMutex
	detector *Detector
	quotas   *QuotaEnforcer // nil if no quotas configured
}

// NewPipeline creates a tenant processing pipeline.
// quotas may be nil (tenant detection without quota enforcement).
func NewPipeline(detector *Detector, quotas *QuotaEnforcer) *Pipeline {
	return &Pipeline{
		detector: detector,
		quotas:   quotas,
	}
}

// ProcessBatch implements buffer.TenantProcessor.
// It detects tenants, applies labels, and enforces per-tenant quotas.
func (p *Pipeline) ProcessBatch(rms []*metricspb.ResourceMetrics, headerTenant string) []*metricspb.ResourceMetrics {
	p.mu.RLock()
	detector := p.detector
	quotas := p.quotas
	p.mu.RUnlock()

	if detector == nil || !detector.Enabled() {
		return rms
	}

	// Step 1: Detect tenants and annotate metrics
	tenantBuckets := detector.DetectAndAnnotate(rms, headerTenant)

	if quotas == nil {
		// No quotas — just return all metrics (they've been annotated with tenant labels)
		return rms
	}

	// Step 2: Apply per-tenant quotas
	var result []*metricspb.ResourceMetrics
	for tenantID, tenantRMs := range tenantBuckets {
		surviving := quotas.Process(tenantID, tenantRMs)
		result = append(result, surviving...)
	}
	return result
}

// SetQuotaEnforcer replaces the quota enforcer (used during reload).
func (p *Pipeline) SetQuotaEnforcer(qe *QuotaEnforcer) {
	p.mu.Lock()
	p.quotas = qe
	p.mu.Unlock()
}

// ReloadDetector reloads the detector configuration.
func (p *Pipeline) ReloadDetector(cfg Config) error {
	p.mu.RLock()
	d := p.detector
	p.mu.RUnlock()
	return d.ReloadConfig(cfg)
}
