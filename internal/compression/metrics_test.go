package compression

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestCompressionPoolMetrics_Registration(t *testing.T) {
	// Reset counters to known state so the test is deterministic.
	ResetPoolStats()

	expectedMetrics := []string{
		"metrics_governor_compression_pool_gets_total",
		"metrics_governor_compression_pool_puts_total",
		"metrics_governor_compression_pool_discards_total",
		"metrics_governor_compression_pool_new_total",
		"metrics_governor_compression_buffer_pool_gets_total",
		"metrics_governor_compression_buffer_pool_puts_total",
	}

	// Gather all metrics from the default registry.
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	gathered := make(map[string]*dto.MetricFamily, len(families))
	for _, mf := range families {
		gathered[mf.GetName()] = mf
	}

	for _, name := range expectedMetrics {
		mf, ok := gathered[name]
		if !ok {
			t.Errorf("metric %q not found in gathered metrics", name)
			continue
		}

		// CounterFunc is reported as COUNTER type by the prometheus client.
		if mf.GetType() != dto.MetricType_COUNTER {
			t.Errorf("metric %q: expected type COUNTER, got %v", name, mf.GetType())
		}

		metrics := mf.GetMetric()
		if len(metrics) == 0 {
			t.Errorf("metric %q: no metric samples", name)
			continue
		}

		// After ResetPoolStats, all counters should be 0.
		val := metrics[0].GetCounter().GetValue()
		if val != 0 {
			t.Errorf("metric %q: expected value 0 after reset, got %f", name, val)
		}
	}
}

func TestCompressionPoolMetrics_Values(t *testing.T) {
	// Reset and then bump some counters.
	ResetPoolStats()

	compressionPoolGets.Add(5)
	compressionPoolPuts.Add(3)
	compressionPoolDiscards.Add(1)
	compressionPoolNews.Add(2)
	bufferPoolGets.Add(10)
	bufferPoolPuts.Add(10)

	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	expected := map[string]float64{
		"metrics_governor_compression_pool_gets_total":        5,
		"metrics_governor_compression_pool_puts_total":        3,
		"metrics_governor_compression_pool_discards_total":    1,
		"metrics_governor_compression_pool_new_total":         2,
		"metrics_governor_compression_buffer_pool_gets_total": 10,
		"metrics_governor_compression_buffer_pool_puts_total": 10,
	}

	gathered := make(map[string]*dto.MetricFamily, len(families))
	for _, mf := range families {
		gathered[mf.GetName()] = mf
	}

	for name, want := range expected {
		mf, ok := gathered[name]
		if !ok {
			t.Errorf("metric %q not found", name)
			continue
		}
		got := mf.GetMetric()[0].GetCounter().GetValue()
		if got != want {
			t.Errorf("metric %q: got %f, want %f", name, got, want)
		}
	}

	// Clean up for other tests.
	ResetPoolStats()
}
