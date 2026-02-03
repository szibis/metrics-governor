package intern

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestInternPoolMetrics_Registration(t *testing.T) {
	// Reset pools to a known state.
	LabelNames.Reset()
	MetricNames.Reset()

	expectedMetrics := map[string]dto.MetricType{
		"metrics_governor_intern_hits_total":   dto.MetricType_COUNTER,
		"metrics_governor_intern_misses_total": dto.MetricType_COUNTER,
		"metrics_governor_intern_pool_size":    dto.MetricType_GAUGE,
	}

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	gathered := make(map[string]*dto.MetricFamily, len(families))
	for _, mf := range families {
		gathered[mf.GetName()] = mf
	}

	for name, wantType := range expectedMetrics {
		mf, ok := gathered[name]
		if !ok {
			t.Errorf("metric %q not found in gathered metrics", name)
			continue
		}

		if mf.GetType() != wantType {
			t.Errorf("metric %q: expected type %v, got %v", name, wantType, mf.GetType())
		}

		metrics := mf.GetMetric()
		if len(metrics) == 0 {
			t.Errorf("metric %q: no metric samples", name)
			continue
		}

		// Each metric should have exactly 2 samples (label_names and metric_names pools).
		if len(metrics) != 2 {
			t.Errorf("metric %q: expected 2 pool samples, got %d", name, len(metrics))
		}

		// Verify pool label exists on each sample.
		for _, m := range metrics {
			labels := m.GetLabel()
			found := false
			for _, l := range labels {
				if l.GetName() == "pool" {
					found = true
					val := l.GetValue()
					if val != "label_names" && val != "metric_names" {
						t.Errorf("metric %q: unexpected pool label value %q", name, val)
					}
				}
			}
			if !found {
				t.Errorf("metric %q: missing 'pool' label", name)
			}
		}
	}
}

func TestInternPoolMetrics_Values(t *testing.T) {
	// Reset pools.
	LabelNames.Reset()
	MetricNames.Reset()

	// Intern some strings to generate hits/misses.
	LabelNames.Intern("foo")
	LabelNames.Intern("bar")
	LabelNames.Intern("foo") // hit

	MetricNames.Intern("cpu_usage")

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	gathered := make(map[string]*dto.MetricFamily, len(families))
	for _, mf := range families {
		gathered[mf.GetName()] = mf
	}

	// Helper to find a counter metric sample by pool label value.
	findCounterByPool := func(mf *dto.MetricFamily, pool string) float64 {
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "pool" && l.GetValue() == pool {
					return m.GetCounter().GetValue()
				}
			}
		}
		t.Fatalf("pool label %q not found in metric %q", pool, mf.GetName())
		return -1
	}

	// Helper to find a gauge metric sample by pool label value.
	findGaugeByPool := func(mf *dto.MetricFamily, pool string) float64 {
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "pool" && l.GetValue() == pool {
					return m.GetGauge().GetValue()
				}
			}
		}
		t.Fatalf("pool label %q not found in metric %q", pool, mf.GetName())
		return -1
	}

	// LabelNames: 2 misses (foo, bar), 1 hit (foo again)
	hitsMF := gathered["metrics_governor_intern_hits_total"]
	if hitsMF == nil {
		t.Fatal("metrics_governor_intern_hits_total not found")
	}
	if got := findCounterByPool(hitsMF, "label_names"); got != 1 {
		t.Errorf("label_names hits: got %f, want 1", got)
	}

	missesMF := gathered["metrics_governor_intern_misses_total"]
	if missesMF == nil {
		t.Fatal("metrics_governor_intern_misses_total not found")
	}
	if got := findCounterByPool(missesMF, "label_names"); got != 2 {
		t.Errorf("label_names misses: got %f, want 2", got)
	}

	sizeMF := gathered["metrics_governor_intern_pool_size"]
	if sizeMF == nil {
		t.Fatal("metrics_governor_intern_pool_size not found")
	}
	if got := findGaugeByPool(sizeMF, "label_names"); got != 2 {
		t.Errorf("label_names size: got %f, want 2", got)
	}

	// MetricNames: 1 miss, 0 hits
	if got := findCounterByPool(hitsMF, "metric_names"); got != 0 {
		t.Errorf("metric_names hits: got %f, want 0", got)
	}
	if got := findCounterByPool(missesMF, "metric_names"); got != 1 {
		t.Errorf("metric_names misses: got %f, want 1", got)
	}
	if got := findGaugeByPool(sizeMF, "metric_names"); got != 1 {
		t.Errorf("metric_names size: got %f, want 1", got)
	}

	// Clean up.
	LabelNames.Reset()
	MetricNames.Reset()
}
