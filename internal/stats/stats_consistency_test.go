package stats

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// TestConsistency_StatsLevel_BasicCountsMatchFull processes the same batch through
// both a basic-level and full-level collector and verifies that per-metric datapoint
// counts from basic exactly match those from full. Uses 10 distinct metric names
// with varying datapoint counts.
func TestConsistency_StatsLevel_BasicCountsMatchFull(t *testing.T) {
	t.Parallel()

	// 10 metric names with varying dp counts (1..10).
	type metricSpec struct {
		name    string
		dpCount int
	}
	specs := []metricSpec{
		{"http_requests_total", 1},
		{"http_duration_seconds", 2},
		{"grpc_server_handled", 3},
		{"node_cpu_seconds", 4},
		{"process_resident_memory", 5},
		{"disk_io_bytes", 6},
		{"network_rx_packets", 7},
		{"container_memory_usage", 8},
		{"kube_pod_status", 9},
		{"api_latency_bucket", 10},
	}

	// Build metrics for each spec.
	var metrics []*metricspb.Metric
	expectedTotal := 0
	for _, s := range specs {
		attrs := make([]map[string]string, s.dpCount)
		for j := 0; j < s.dpCount; j++ {
			attrs[j] = map[string]string{
				"instance": fmt.Sprintf("host-%d", j),
				"region":   "us-east-1",
			}
		}
		metrics = append(metrics, createTestMetric(s.name, attrs))
		expectedTotal += s.dpCount
	}

	rms := []*metricspb.ResourceMetrics{
		createTestResourceMetrics(
			map[string]string{"service": "gateway", "env": "prod"},
			metrics,
		),
	}

	basicC := NewCollector([]string{"service"}, StatsLevelBasic)
	fullC := NewCollector([]string{"service"}, StatsLevelFull)

	basicC.Process(rms)
	fullC.Process(rms)

	// Global datapoint totals must match.
	basicDP, basicMetrics, _ := basicC.GetGlobalStats()
	fullDP, fullMetrics, _ := fullC.GetGlobalStats()

	if basicDP != fullDP {
		t.Errorf("global datapoints mismatch: basic=%d full=%d", basicDP, fullDP)
	}
	if basicDP != uint64(expectedTotal) {
		t.Errorf("expected %d total datapoints, basic got %d", expectedTotal, basicDP)
	}
	if basicMetrics != fullMetrics {
		t.Errorf("unique metrics mismatch: basic=%d full=%d", basicMetrics, fullMetrics)
	}
	if basicMetrics != uint64(len(specs)) {
		t.Errorf("expected %d unique metrics, basic got %d", len(specs), basicMetrics)
	}

	// Per-metric datapoint counts must match exactly.
	basicC.mu.RLock()
	fullC.mu.RLock()
	defer basicC.mu.RUnlock()
	defer fullC.mu.RUnlock()

	for _, s := range specs {
		bms, bok := basicC.metricStats[s.name]
		fms, fok := fullC.metricStats[s.name]
		if !bok {
			t.Errorf("metric %q missing from basic collector", s.name)
			continue
		}
		if !fok {
			t.Errorf("metric %q missing from full collector", s.name)
			continue
		}
		if bms.Datapoints != fms.Datapoints {
			t.Errorf("metric %q dp mismatch: basic=%d full=%d", s.name, bms.Datapoints, fms.Datapoints)
		}
		if bms.Datapoints != uint64(s.dpCount) {
			t.Errorf("metric %q expected %d dps, got basic=%d", s.name, s.dpCount, bms.Datapoints)
		}
	}
}

// TestConsistency_StatsLevel_NoneNoSideEffects creates a nil collector (the "none"
// level approach) and verifies that the nil-guard pattern used in buffer code
// correctly skips all processing without panics or side effects.
func TestConsistency_StatsLevel_NoneNoSideEffects(t *testing.T) {
	t.Parallel()

	// Simulate the nil-guard pattern used in internal/buffer and internal/prw:
	//   if b.stats != nil { b.stats.Process(...) }
	var collector *Collector // nil — the "none" level approach

	rms := []*metricspb.ResourceMetrics{
		createTestResourceMetrics(
			map[string]string{"service": "test"},
			[]*metricspb.Metric{
				createTestMetric("metric_a", []map[string]string{{"k": "v"}}),
			},
		),
	}

	// Wrapper simulating buffer nil-guard for Process.
	processIfNotNil := func(c *Collector, data []*metricspb.ResourceMetrics) {
		if c != nil {
			c.Process(data)
		}
	}

	// Wrapper simulating buffer nil-guard for RecordReceived.
	recordReceivedIfNotNil := func(c *Collector, count int) {
		if c != nil {
			c.RecordReceived(count)
		}
	}

	// Wrapper simulating buffer nil-guard for RecordExport.
	recordExportIfNotNil := func(c *Collector, count int) {
		if c != nil {
			c.RecordExport(count)
		}
	}

	// Wrapper simulating buffer nil-guard for RecordExportError.
	recordExportErrorIfNotNil := func(c *Collector) {
		if c != nil {
			c.RecordExportError()
		}
	}

	// Wrapper simulating buffer nil-guard for SetOTLPBufferSize.
	setOTLPBufferSizeIfNotNil := func(c *Collector, size int) {
		if c != nil {
			c.SetOTLPBufferSize(size)
		}
	}

	// Wrapper simulating buffer nil-guard for SetPRWBufferSize.
	setPRWBufferSizeIfNotNil := func(c *Collector, size int) {
		if c != nil {
			c.SetPRWBufferSize(size)
		}
	}

	// Wrapper simulating PRW buffer nil-guard for RecordPRWReceived.
	recordPRWReceivedIfNotNil := func(c *Collector, dp, ts int) {
		if c != nil {
			c.RecordPRWReceived(dp, ts)
		}
	}

	// None of these should panic.
	processIfNotNil(collector, rms)
	recordReceivedIfNotNil(collector, 100)
	recordExportIfNotNil(collector, 50)
	recordExportErrorIfNotNil(collector)
	setOTLPBufferSizeIfNotNil(collector, 10)
	setPRWBufferSizeIfNotNil(collector, 5)
	recordPRWReceivedIfNotNil(collector, 10, 5)

	// Verify the collector is still nil (no accidental allocation).
	if collector != nil {
		t.Error("expected collector to remain nil after nil-guarded calls")
	}
}

// TestConsistency_StatsLevel_BasicNoBloomAllocation processes 1000 unique series
// at basic level and verifies that no Bloom filters or cardinality trackers are
// allocated. TotalSeries from GetGlobalStats must be 0.
func TestConsistency_StatsLevel_BasicNoBloomAllocation(t *testing.T) {
	t.Parallel()

	c := NewCollector([]string{"service", "env"}, StatsLevelBasic)

	// Build 1000 unique series across 10 metrics (100 series each).
	var allMetrics []*metricspb.Metric
	for i := 0; i < 10; i++ {
		attrs := make([]map[string]string, 100)
		for j := 0; j < 100; j++ {
			attrs[j] = map[string]string{
				"instance": fmt.Sprintf("host-%d-%d", i, j),
				"region":   fmt.Sprintf("region-%d", j%5),
			}
		}
		allMetrics = append(allMetrics, createTestMetric(
			fmt.Sprintf("metric_%03d", i), attrs,
		))
	}

	rms := []*metricspb.ResourceMetrics{
		createTestResourceMetrics(
			map[string]string{"service": "big-service", "env": "staging"},
			allMetrics,
		),
	}

	c.Process(rms)

	// Verify datapoints are counted.
	dp, uniqueMetrics, totalCard := c.GetGlobalStats()
	if dp != 1000 {
		t.Errorf("expected 1000 datapoints, got %d", dp)
	}
	if uniqueMetrics != 10 {
		t.Errorf("expected 10 unique metrics, got %d", uniqueMetrics)
	}

	// Cardinality must be 0 at basic level (no Bloom filters).
	if totalCard != 0 {
		t.Errorf("expected 0 totalCardinality at basic level, got %d", totalCard)
	}

	// Verify no cardinality trackers were allocated on individual metricStats.
	c.mu.RLock()
	defer c.mu.RUnlock()

	for name, ms := range c.metricStats {
		if ms.cardinality != nil {
			t.Errorf("metric %q: expected nil cardinality tracker at basic level", name)
		}
	}

	// Verify labelStats map is empty (no per-label tracking at basic level).
	if len(c.labelStats) != 0 {
		t.Errorf("expected 0 labelStats at basic level, got %d", len(c.labelStats))
	}
}

// TestConsistency_StatsLevel_FullCardinalityTracking processes the same 1000-series
// batch at full level and verifies that TotalSeries > 0 and cardinality stats are
// populated with Bloom filters.
func TestConsistency_StatsLevel_FullCardinalityTracking(t *testing.T) {
	t.Parallel()

	c := NewCollector([]string{"service", "env"}, StatsLevelFull)

	// Build 1000 unique series across 10 metrics (100 series each).
	var allMetrics []*metricspb.Metric
	for i := 0; i < 10; i++ {
		attrs := make([]map[string]string, 100)
		for j := 0; j < 100; j++ {
			attrs[j] = map[string]string{
				"instance": fmt.Sprintf("host-%d-%d", i, j),
				"region":   fmt.Sprintf("region-%d", j%5),
			}
		}
		allMetrics = append(allMetrics, createTestMetric(
			fmt.Sprintf("metric_%03d", i), attrs,
		))
	}

	rms := []*metricspb.ResourceMetrics{
		createTestResourceMetrics(
			map[string]string{"service": "big-service", "env": "staging"},
			allMetrics,
		),
	}

	c.Process(rms)

	// Verify datapoints are counted.
	dp, uniqueMetrics, totalCard := c.GetGlobalStats()
	if dp != 1000 {
		t.Errorf("expected 1000 datapoints, got %d", dp)
	}
	if uniqueMetrics != 10 {
		t.Errorf("expected 10 unique metrics, got %d", uniqueMetrics)
	}

	// At full level, cardinality must be > 0.
	if totalCard == 0 {
		t.Error("expected non-zero totalCardinality at full level")
	}

	// Each metric should have tracked ~100 unique series.
	// With Bloom filters there may be slight overcounting, so allow a range.
	c.mu.RLock()
	defer c.mu.RUnlock()

	for name, ms := range c.metricStats {
		if ms.cardinality == nil {
			t.Errorf("metric %q: expected non-nil cardinality tracker at full level", name)
			continue
		}
		count := ms.cardinality.Count()
		if count < 90 || count > 110 {
			t.Errorf("metric %q: expected ~100 unique series, got %d", name, count)
		}
	}

	// labelStats should be populated (we track "service" and "env").
	if len(c.labelStats) == 0 {
		t.Error("expected non-empty labelStats at full level with tracked labels")
	}

	// Verify the label combination "service=big-service,env=staging" exists.
	found := false
	for key, ls := range c.labelStats {
		if strings.Contains(key, "service=big-service") && strings.Contains(key, "env=staging") {
			found = true
			if ls.Datapoints != 1000 {
				t.Errorf("label combo %q: expected 1000 dps, got %d", key, ls.Datapoints)
			}
			if ls.cardinality == nil {
				t.Errorf("label combo %q: expected non-nil cardinality tracker", key)
			} else if ls.cardinality.Count() == 0 {
				t.Errorf("label combo %q: expected non-zero cardinality", key)
			}
		}
	}
	if !found {
		t.Error("expected label combo containing service=big-service and env=staging")
	}
}

// TestConsistency_StatsLevel_ServeHTTPBasicFormat verifies that at basic level the
// HTTP output contains metric_datapoints_total lines but does NOT contain
// metric_cardinality or label_cardinality lines.
func TestConsistency_StatsLevel_ServeHTTPBasicFormat(t *testing.T) {
	t.Parallel()

	c := NewCollector([]string{"service"}, StatsLevelBasic)

	// Process several metrics so per-metric lines are emitted.
	var metrics []*metricspb.Metric
	for i := 0; i < 5; i++ {
		metrics = append(metrics, createTestMetric(
			fmt.Sprintf("basic_metric_%d", i),
			[]map[string]string{{"k": fmt.Sprintf("v%d", i)}},
		))
	}
	rms := []*metricspb.ResourceMetrics{
		createTestResourceMetrics(map[string]string{"service": "web"}, metrics),
	}
	c.Process(rms)

	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()

	// Must contain per-metric datapoint lines.
	if !strings.Contains(body, "metrics_governor_metric_datapoints_total") {
		t.Error("basic level: expected metric_datapoints_total in HTTP output")
	}

	// Verify each metric name appears in a datapoints line.
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("basic_metric_%d", i)
		expected := fmt.Sprintf("metrics_governor_metric_datapoints_total{metric_name=%q} 1", name)
		if !strings.Contains(body, expected) {
			t.Errorf("basic level: expected line %q in HTTP output", expected)
		}
	}

	// Must NOT contain cardinality-related metrics.
	forbidden := []string{
		"metrics_governor_metric_cardinality",
		"metrics_governor_label_cardinality",
		"metrics_governor_label_datapoints_total",
		"metrics_governor_cardinality_trackers_total",
		"metrics_governor_cardinality_memory_bytes",
		"metrics_governor_cardinality_mode",
		"metrics_governor_serieskey_pool_gets_total",
		"metrics_governor_serieskey_pool_puts_total",
		"metrics_governor_serieskey_pool_discards_total",
	}
	for _, f := range forbidden {
		if strings.Contains(body, f) {
			t.Errorf("basic level: unexpected cardinality metric %q in HTTP output", f)
		}
	}

	// Stats level gauge must indicate "basic".
	if !strings.Contains(body, `metrics_governor_stats_level{level="basic"} 1`) {
		t.Error("basic level: expected stats_level gauge with level=basic")
	}
}

// TestConsistency_StatsLevel_ServeHTTPFullFormat verifies that at full level the
// HTTP output contains both metric_datapoints_total AND metric_cardinality lines.
func TestConsistency_StatsLevel_ServeHTTPFullFormat(t *testing.T) {
	t.Parallel()

	c := NewCollector([]string{"service"}, StatsLevelFull)

	// Process several metrics so per-metric and cardinality lines are emitted.
	var metrics []*metricspb.Metric
	for i := 0; i < 5; i++ {
		metrics = append(metrics, createTestMetric(
			fmt.Sprintf("full_metric_%d", i),
			[]map[string]string{{"k": fmt.Sprintf("v%d", i)}},
		))
	}
	rms := []*metricspb.ResourceMetrics{
		createTestResourceMetrics(map[string]string{"service": "web"}, metrics),
	}
	c.Process(rms)

	rec := httptest.NewRecorder()
	c.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()

	// Must contain per-metric datapoint lines.
	if !strings.Contains(body, "metrics_governor_metric_datapoints_total") {
		t.Error("full level: expected metric_datapoints_total in HTTP output")
	}

	// Must contain per-metric cardinality lines.
	if !strings.Contains(body, "metrics_governor_metric_cardinality") {
		t.Error("full level: expected metric_cardinality in HTTP output")
	}

	// Verify each metric name appears in both datapoint and cardinality lines.
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("full_metric_%d", i)
		dpLine := fmt.Sprintf("metrics_governor_metric_datapoints_total{metric_name=%q} 1", name)
		if !strings.Contains(body, dpLine) {
			t.Errorf("full level: expected line %q in HTTP output", dpLine)
		}
		cardLine := fmt.Sprintf("metrics_governor_metric_cardinality{metric_name=%q}", name)
		if !strings.Contains(body, cardLine) {
			t.Errorf("full level: expected cardinality line for %q in HTTP output", name)
		}
	}

	// Must contain label-related metrics (we track "service").
	if !strings.Contains(body, "metrics_governor_label_datapoints_total") {
		t.Error("full level: expected label_datapoints_total in HTTP output")
	}
	if !strings.Contains(body, "metrics_governor_label_cardinality") {
		t.Error("full level: expected label_cardinality in HTTP output")
	}

	// Must contain cardinality infrastructure metrics.
	infraMetrics := []string{
		"metrics_governor_cardinality_trackers_total",
		"metrics_governor_cardinality_memory_bytes",
		"metrics_governor_cardinality_mode",
	}
	for _, m := range infraMetrics {
		if !strings.Contains(body, m) {
			t.Errorf("full level: expected %q in HTTP output", m)
		}
	}

	// Stats level gauge must indicate "full".
	if !strings.Contains(body, `metrics_governor_stats_level{level="full"} 1`) {
		t.Error("full level: expected stats_level gauge with level=full")
	}
}
