package tenant

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
	"go.uber.org/goleak"
)

func makeLeakRM(namespace string, dpCount int) *metricspb.ResourceMetrics {
	dps := make([]*metricspb.NumberDataPoint, dpCount)
	for i := range dps {
		dps[i] = &metricspb.NumberDataPoint{
			Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)},
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
				Name: "test_metric",
				Data: &metricspb.Metric_Gauge{
					Gauge: &metricspb.Gauge{DataPoints: dps},
				},
			}},
		}},
	}
}

// TestLeakCheck_TenantPipeline verifies that creating, using, and discarding
// a tenant Pipeline does not leak goroutines.
func TestLeakCheck_TenantPipeline(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeAttribute
	cfg.AttributeKey = "service.namespace"
	cfg.InjectLabel = false
	d, err := NewDetector(cfg)
	if err != nil {
		t.Fatalf("NewDetector: %v", err)
	}

	quotasCfg := &QuotasConfig{
		Default: &TenantQuota{
			MaxDatapoints: 100000,
			Action:        QuotaActionDrop,
			Window:        "1m",
			parsedWindow:  time.Minute,
		},
	}
	qe := NewQuotaEnforcer(quotasCfg)
	p := NewPipeline(d, qe)

	for i := 0; i < 50; i++ {
		rms := []*metricspb.ResourceMetrics{
			makeLeakRM(fmt.Sprintf("ns-%d", i), 5),
		}
		p.ProcessBatch(rms, "")
	}
}

// TestMemLeak_QuotaEnforcer_TenantExpiry verifies that cycling through many
// unique tenant IDs does not cause unbounded heap growth.
// The QuotaEnforcer tracks per-tenant windows which should be cleaned up on reload.
func TestMemLeak_QuotaEnforcer_TenantExpiry(t *testing.T) {
	quotasCfg := &QuotasConfig{
		Default: &TenantQuota{
			MaxDatapoints: 100000,
			Action:        QuotaActionDrop,
			Window:        "1m",
			parsedWindow:  time.Minute,
		},
	}
	qe := NewQuotaEnforcer(quotasCfg)

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	const cycles = 200
	for c := 0; c < cycles; c++ {
		// Process metrics with unique tenant IDs each cycle
		for i := 0; i < 10; i++ {
			tenantID := fmt.Sprintf("tenant-cycle%d-id%d", c, i)
			rms := []*metricspb.ResourceMetrics{makeLeakRM(tenantID, 5)}
			qe.Process(tenantID, rms)
		}

		// Periodically reload to clear state (simulates config reload in production)
		if c%50 == 49 {
			qe.ReloadConfig(quotasCfg)
		}
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("QuotaEnforcer tenant expiry: heap_before=%dKB, heap_after=%dKB",
		heapBefore/1024, heapAfter/1024)

	if heapAfter > heapBefore+30*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after %d cycles",
			heapBefore/1024, heapAfter/1024, cycles)
	}
}
