package stats

import (
	"net/http/httptest"
	"strings"
	"testing"

	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
)

// TestStatsLevel_BasicMode verifies basic level tracks dp counts but not cardinality.
func TestStatsLevel_BasicMode(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelBasic)

	if c.Level() != StatsLevelBasic {
		t.Fatalf("expected level basic, got %s", c.Level())
	}

	rm := createTestResourceMetrics(
		map[string]string{"service": "frontend"},
		[]*metricspb.Metric{
			createTestMetric("http_requests", []map[string]string{
				{"method": "GET"},
				{"method": "POST"},
			}),
			createTestMetric("http_latency", []map[string]string{
				{"method": "GET"},
			}),
		},
	)
	c.Process([]*metricspb.ResourceMetrics{rm})

	// Verify dp counts are tracked
	dp, uniqueMetrics, totalCard := c.GetGlobalStats()
	if dp != 3 {
		t.Errorf("expected 3 datapoints, got %d", dp)
	}
	if uniqueMetrics != 2 {
		t.Errorf("expected 2 unique metrics, got %d", uniqueMetrics)
	}
	// Cardinality must be 0 at basic level (no Bloom filters)
	if totalCard != 0 {
		t.Errorf("expected 0 cardinality at basic level, got %d", totalCard)
	}

	// Verify per-metric stats exist
	c.mu.RLock()
	ms, ok := c.metricStats["http_requests"]
	c.mu.RUnlock()
	if !ok {
		t.Fatal("expected http_requests in metricStats")
	}
	if ms.Datapoints != 2 {
		t.Errorf("expected 2 datapoints for http_requests, got %d", ms.Datapoints)
	}
	// No cardinality tracker at basic level
	if ms.cardinality != nil {
		t.Error("expected nil cardinality tracker at basic level")
	}
}

// TestStatsLevel_FullMode verifies full level tracks everything including cardinality.
func TestStatsLevel_FullMode(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelFull)

	if c.Level() != StatsLevelFull {
		t.Fatalf("expected level full, got %s", c.Level())
	}

	rm := createTestResourceMetrics(
		map[string]string{"service": "frontend"},
		[]*metricspb.Metric{
			createTestMetric("http_requests", []map[string]string{
				{"method": "GET"},
				{"method": "POST"},
			}),
		},
	)
	c.Process([]*metricspb.ResourceMetrics{rm})

	dp, _, totalCard := c.GetGlobalStats()
	if dp != 2 {
		t.Errorf("expected 2 datapoints, got %d", dp)
	}
	// At full level, cardinality tracking via Bloom filters is active
	if totalCard == 0 {
		t.Error("expected non-zero cardinality at full level")
	}
}

// TestStatsLevel_DefaultLevel verifies empty string defaults to full.
func TestStatsLevel_DefaultLevel(t *testing.T) {
	c := NewCollector(nil, "")
	if c.Level() != StatsLevelFull {
		t.Errorf("expected default level to be full, got %s", c.Level())
	}
}

// TestStatsLevel_BasicServeHTTP verifies basic level outputs dp counts but NOT cardinality.
func TestStatsLevel_BasicServeHTTP(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelBasic)
	rm := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{
			createTestMetric("http_requests", []map[string]string{
				{"method": "GET"},
			}),
		},
	)
	c.Process([]*metricspb.ResourceMetrics{rm})

	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()

	// Basic level: per-metric dp counts present
	if !strings.Contains(body, "metrics_governor_metric_datapoints_total") {
		t.Error("expected metric_datapoints_total at basic level")
	}
	// Stats level gauge present
	if !strings.Contains(body, `metrics_governor_stats_level{level="basic"} 1`) {
		t.Error("expected stats_level gauge with basic")
	}

	// Basic level: cardinality metrics absent
	cardinalityMetrics := []string{
		"metrics_governor_metric_cardinality",
		"metrics_governor_label_datapoints_total",
		"metrics_governor_label_cardinality",
		"metrics_governor_cardinality_trackers_total",
		"metrics_governor_cardinality_memory_bytes",
		"metrics_governor_cardinality_mode",
		"metrics_governor_serieskey_pool_gets_total",
	}
	for _, m := range cardinalityMetrics {
		if strings.Contains(body, m) {
			t.Errorf("unexpected cardinality metric at basic level: %s", m)
		}
	}
}

// TestStatsLevel_FullServeHTTP verifies full level outputs everything.
func TestStatsLevel_FullServeHTTP(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelFull)
	rm := createTestResourceMetrics(
		map[string]string{"service": "api"},
		[]*metricspb.Metric{
			createTestMetric("http_requests", []map[string]string{
				{"method": "GET"},
			}),
		},
	)
	c.Process([]*metricspb.ResourceMetrics{rm})

	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()

	// Full level: cardinality metrics present
	if !strings.Contains(body, "metrics_governor_metric_cardinality") {
		t.Error("expected metric_cardinality at full level")
	}
	if !strings.Contains(body, "metrics_governor_cardinality_trackers_total") {
		t.Error("expected cardinality_trackers_total at full level")
	}
	if !strings.Contains(body, `metrics_governor_stats_level{level="full"} 1`) {
		t.Error("expected stats_level gauge with full")
	}
}

// TestStatsLevel_BasicResetCardinality verifies reset works without Bloom filters.
func TestStatsLevel_BasicResetCardinality(t *testing.T) {
	c := NewCollector(nil, StatsLevelBasic)
	rm := createTestResourceMetrics(nil, []*metricspb.Metric{
		createTestMetric("metric1", []map[string]string{{"k": "v"}}),
	})
	c.Process([]*metricspb.ResourceMetrics{rm})

	if c.totalMetrics != 1 {
		t.Fatal("expected 1 metric before reset")
	}

	c.ResetCardinality()

	c.mu.RLock()
	mLen := len(c.metricStats)
	c.mu.RUnlock()
	if mLen != 0 {
		t.Errorf("expected metricStats cleared after reset, got %d", mLen)
	}
	if c.totalMetrics != 0 {
		t.Errorf("expected totalMetrics=0 after reset, got %d", c.totalMetrics)
	}
}

// TestStatsLevel_BasicDatapointCounts verifies basic level counts match full level counts.
func TestStatsLevel_BasicDatapointCounts(t *testing.T) {
	basicC := NewCollector(nil, StatsLevelBasic)
	fullC := NewCollector(nil, StatsLevelFull)

	rms := []*metricspb.ResourceMetrics{
		createTestResourceMetrics(
			map[string]string{"host": "a"},
			[]*metricspb.Metric{
				createTestMetric("m1", []map[string]string{{"k": "1"}, {"k": "2"}, {"k": "3"}}),
				createTestMetric("m2", []map[string]string{{"k": "x"}}),
			},
		),
		createTestResourceMetrics(
			map[string]string{"host": "b"},
			[]*metricspb.Metric{
				createTestMetric("m1", []map[string]string{{"k": "4"}}),
			},
		),
	}

	basicC.Process(rms)
	fullC.Process(rms)

	basicDP, basicMetrics, _ := basicC.GetGlobalStats()
	fullDP, fullMetrics, _ := fullC.GetGlobalStats()

	if basicDP != fullDP {
		t.Errorf("basic dp count %d != full dp count %d", basicDP, fullDP)
	}
	if basicMetrics != fullMetrics {
		t.Errorf("basic unique metrics %d != full unique metrics %d", basicMetrics, fullMetrics)
	}

	// Verify per-metric dp counts match
	basicC.mu.RLock()
	fullC.mu.RLock()
	for name, bms := range basicC.metricStats {
		fms, ok := fullC.metricStats[name]
		if !ok {
			t.Errorf("metric %q in basic but not in full", name)
			continue
		}
		if bms.Datapoints != fms.Datapoints {
			t.Errorf("metric %q: basic dps %d != full dps %d", name, bms.Datapoints, fms.Datapoints)
		}
	}
	fullC.mu.RUnlock()
	basicC.mu.RUnlock()
}

// TestStatsLevel_RecordMethodsWork verifies Record* methods work at all levels.
func TestStatsLevel_RecordMethodsWork(t *testing.T) {
	for _, level := range []StatsLevel{StatsLevelBasic, StatsLevelFull} {
		t.Run(string(level), func(t *testing.T) {
			c := NewCollector(nil, level)

			// These should work at any level
			c.RecordReceived(100)
			c.RecordExport(50)
			c.RecordExportError()
			c.RecordOTLPBytesReceived(1024)
			c.RecordOTLPBytesSent(512)
			c.SetOTLPBufferSize(10)
			c.SetPRWBufferSize(5)

			rec := httptest.NewRecorder()
			c.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
			body := rec.Body.String()

			// Global counters always present
			if !strings.Contains(body, "metrics_governor_datapoints_received_total 100") {
				t.Error("expected datapoints_received_total 100")
			}
			if !strings.Contains(body, "metrics_governor_datapoints_sent_total 50") {
				t.Error("expected datapoints_sent_total 50")
			}
			if !strings.Contains(body, "metrics_governor_export_errors_total 1") {
				t.Error("expected export_errors_total 1")
			}
		})
	}
}
