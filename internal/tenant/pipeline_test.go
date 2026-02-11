package tenant

import (
	"testing"
	"time"

	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
)

func TestPipeline_Disabled(t *testing.T) {
	cfg := Config{Enabled: false}
	d, _ := NewDetector(cfg)
	p := NewPipeline(d, nil)

	rms := []*metricspb.ResourceMetrics{makeGaugeRM(nil, "cpu", nil)}
	result := p.ProcessBatch(rms, "")
	if len(result) != 1 {
		t.Fatal("disabled pipeline should pass through")
	}
}

func TestPipeline_DetectionOnly(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeHeader
	cfg.InjectLabel = true
	cfg.InjectLabelName = "__tenant__"
	d, _ := NewDetector(cfg)
	p := NewPipeline(d, nil)

	rms := []*metricspb.ResourceMetrics{makeGaugeRM(nil, "cpu", nil)}
	result := p.ProcessBatch(rms, "team-a")
	if len(result) != 1 {
		t.Fatal("should pass through without quotas")
	}
	// Verify label injection
	dp := result[0].ScopeMetrics[0].Metrics[0].GetGauge().DataPoints[0]
	found := false
	for _, kv := range dp.Attributes {
		if kv.Key == "__tenant__" {
			found = true
		}
	}
	if !found {
		t.Fatal("tenant label should be injected")
	}
}

func TestPipeline_WithQuotas(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeAttribute
	cfg.AttributeKey = "service.namespace"
	cfg.InjectLabel = false
	d, _ := NewDetector(cfg)

	quotasCfg := &QuotasConfig{
		Tenants: map[string]*TenantQuota{
			"allowed": {
				MaxDatapoints: 100,
				Action:        QuotaActionLog,
				Window:        "1m",
				parsedWindow:  time.Minute,
			},
			"limited": {
				MaxDatapoints: 2,
				Action:        QuotaActionDrop,
				Window:        "1m",
				parsedWindow:  time.Minute,
			},
		},
	}
	qe := NewQuotaEnforcer(quotasCfg)
	p := NewPipeline(d, qe)

	// Allowed tenant: passes
	rms := []*metricspb.ResourceMetrics{
		makeGaugeRM(map[string]string{"service.namespace": "allowed"}, "cpu", nil),
	}
	result := p.ProcessBatch(rms, "")
	if len(result) != 1 {
		t.Fatal("allowed tenant should pass")
	}

	// Limited tenant: first 2 DPs pass
	rms = []*metricspb.ResourceMetrics{
		makeGaugeRM(map[string]string{"service.namespace": "limited"}, "cpu", nil),
	}
	result = p.ProcessBatch(rms, "")
	if len(result) != 1 {
		t.Fatal("limited tenant first batch should pass")
	}

	// Limited tenant: next batch exceeds quota
	rms = []*metricspb.ResourceMetrics{
		makeTestRM("mem", 5, map[string]string{"host": "a"}),
	}
	// Note: this RM doesn't have service.namespace, so it falls back to default tenant (nil quota)
	// Test with proper attribute
	rms = []*metricspb.ResourceMetrics{
		makeGaugeRM(map[string]string{"service.namespace": "limited"}, "mem", nil),
		makeGaugeRM(map[string]string{"service.namespace": "limited"}, "disk", nil),
	}
	result = p.ProcessBatch(rms, "")
	if result != nil {
		t.Fatal("limited tenant should be dropped after exceeding quota")
	}
}

func TestPipeline_MultiTenantQuotas(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeAttribute
	cfg.AttributeKey = "ns"
	cfg.InjectLabel = false
	d, _ := NewDetector(cfg)

	quotasCfg := &QuotasConfig{
		Default: &TenantQuota{
			MaxDatapoints: 5,
			Action:        QuotaActionDrop,
			Window:        "1m",
			parsedWindow:  time.Minute,
		},
	}
	qe := NewQuotaEnforcer(quotasCfg)
	p := NewPipeline(d, qe)

	// Mixed batch: tenant-a gets 3 DPs, tenant-b gets 3 DPs
	rms := []*metricspb.ResourceMetrics{
		makeGaugeRM(map[string]string{"ns": "a"}, "cpu", nil),
		makeGaugeRM(map[string]string{"ns": "a"}, "mem", nil),
		makeGaugeRM(map[string]string{"ns": "a"}, "disk", nil),
		makeGaugeRM(map[string]string{"ns": "b"}, "cpu", nil),
		makeGaugeRM(map[string]string{"ns": "b"}, "mem", nil),
		makeGaugeRM(map[string]string{"ns": "b"}, "disk", nil),
	}
	result := p.ProcessBatch(rms, "")
	// Both tenants have 3 DPs each, well under limit of 5
	if len(result) != 6 {
		t.Fatalf("all should pass, got %d", len(result))
	}
}

func TestPipeline_SetQuotaEnforcer(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeHeader
	cfg.InjectLabel = false
	d, _ := NewDetector(cfg)
	p := NewPipeline(d, nil)

	// No quotas initially — everything passes
	rms := []*metricspb.ResourceMetrics{makeTestRM("cpu", 100, nil)}
	result := p.ProcessBatch(rms, "t1")
	if len(result) != 1 {
		t.Fatal("should pass without quotas")
	}

	// Attach quotas
	quotasCfg := &QuotasConfig{
		Default: &TenantQuota{
			MaxDatapoints: 5,
			Action:        QuotaActionDrop,
			Window:        "1m",
			parsedWindow:  time.Minute,
		},
	}
	p.SetQuotaEnforcer(NewQuotaEnforcer(quotasCfg))

	// Now quota applies
	rms = []*metricspb.ResourceMetrics{makeTestRM("cpu", 100, nil)}
	result = p.ProcessBatch(rms, "t1")
	if result != nil {
		t.Fatal("should be dropped with quota enforcer")
	}
}

func TestPipeline_HeaderMode_WithQuotas(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeHeader
	cfg.InjectLabel = true
	cfg.InjectLabelName = "__tenant__"
	d, _ := NewDetector(cfg)

	quotasCfg := &QuotasConfig{
		Tenants: map[string]*TenantQuota{
			"org-1": {
				MaxDatapoints: 100,
				Action:        QuotaActionLog,
				Window:        "1m",
				parsedWindow:  time.Minute,
			},
		},
	}
	qe := NewQuotaEnforcer(quotasCfg)
	p := NewPipeline(d, qe)

	rms := []*metricspb.ResourceMetrics{
		makeGaugeRM(nil, "cpu", nil),
		makeGaugeRM(nil, "mem", nil),
	}
	result := p.ProcessBatch(rms, "org-1")
	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}
}

// --- Race Test ---

func TestRace_PipelineConcurrent(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeAttribute
	cfg.AttributeKey = "ns"
	cfg.InjectLabel = true
	cfg.InjectLabelName = "__tenant__"
	d, _ := NewDetector(cfg)

	quotasCfg := &QuotasConfig{
		Default: &TenantQuota{
			MaxDatapoints: 100000,
			Action:        QuotaActionAdaptive,
			Window:        "1m",
			parsedWindow:  time.Minute,
		},
	}
	qe := NewQuotaEnforcer(quotasCfg)
	p := NewPipeline(d, qe)

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func(num int) {
			defer func() { done <- struct{}{} }()
			ns := "ns-" + string(rune('a'+num))
			for j := 0; j < 100; j++ {
				rms := []*metricspb.ResourceMetrics{
					makeGaugeRM(map[string]string{"ns": ns}, "cpu", nil),
				}
				p.ProcessBatch(rms, "")
			}
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}
