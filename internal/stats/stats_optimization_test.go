package stats

import (
	"fmt"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
)

// =============================================================================
// Phase 1-2: Dual-Map Key Building
// =============================================================================

func TestBuildSeriesKeyDualBytes_BasicMerge(t *testing.T) {
	resAttrs := map[string]string{"region": "us-east", "cluster": "prod"}
	dpAttrs := map[string]string{"endpoint": "/api/v1", "method": "GET"}

	result := string(buildSeriesKeyDualBytes(resAttrs, dpAttrs))

	// All 4 keys should appear, sorted alphabetically
	if !strings.Contains(result, "cluster=prod") {
		t.Errorf("expected cluster=prod in result, got %q", result)
	}
	if !strings.Contains(result, "endpoint=/api/v1") {
		t.Errorf("expected endpoint=/api/v1 in result, got %q", result)
	}
	if !strings.Contains(result, "method=GET") {
		t.Errorf("expected method=GET in result, got %q", result)
	}
	if !strings.Contains(result, "region=us-east") {
		t.Errorf("expected region=us-east in result, got %q", result)
	}

	expected := "cluster=prod,endpoint=/api/v1,method=GET,region=us-east"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestBuildSeriesKeyDualBytes_EmptyMaps(t *testing.T) {
	result := buildSeriesKeyDualBytes(map[string]string{}, map[string]string{})
	if result != nil {
		t.Errorf("expected nil for empty maps, got %q", result)
	}

	result = buildSeriesKeyDualBytes(nil, nil)
	if result != nil {
		t.Errorf("expected nil for nil maps, got %q", result)
	}
}

func TestBuildSeriesKeyDualBytes_ResourceOnly(t *testing.T) {
	resAttrs := map[string]string{"service": "gateway", "env": "staging"}
	dpAttrs := map[string]string{}

	result := string(buildSeriesKeyDualBytes(resAttrs, dpAttrs))
	expected := "env=staging,service=gateway"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestBuildSeriesKeyDualBytes_DpOnly(t *testing.T) {
	resAttrs := map[string]string{}
	dpAttrs := map[string]string{"method": "POST", "status": "200"}

	result := string(buildSeriesKeyDualBytes(resAttrs, dpAttrs))
	expected := "method=POST,status=200"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestBuildSeriesKeyDualBytes_OverlapPriority(t *testing.T) {
	resAttrs := map[string]string{"service": "old-service", "region": "us-west"}
	dpAttrs := map[string]string{"service": "new-service", "endpoint": "/health"}

	result := string(buildSeriesKeyDualBytes(resAttrs, dpAttrs))

	// dp attrs override resource attrs: "service" should be "new-service"
	if strings.Contains(result, "service=old-service") {
		t.Error("dp attrs should override resource attrs for 'service'")
	}
	if !strings.Contains(result, "service=new-service") {
		t.Errorf("expected service=new-service in result, got %q", result)
	}

	expected := "endpoint=/health,region=us-west,service=new-service"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestBuildSeriesKeyDualBytes_SortOrder(t *testing.T) {
	resAttrs := map[string]string{"z_last": "1", "a_first": "2"}
	dpAttrs := map[string]string{"m_middle": "3"}

	result := string(buildSeriesKeyDualBytes(resAttrs, dpAttrs))
	expected := "a_first=2,m_middle=3,z_last=1"
	if result != expected {
		t.Errorf("keys not sorted alphabetically: expected %q, got %q", expected, result)
	}
}

func TestBuildSeriesKeyDualBytes_MatchesLegacy(t *testing.T) {
	tests := []struct {
		name     string
		resAttrs map[string]string
		dpAttrs  map[string]string
	}{
		{
			name:     "disjoint keys",
			resAttrs: map[string]string{"region": "us-east", "cluster": "prod"},
			dpAttrs:  map[string]string{"endpoint": "/api", "method": "GET"},
		},
		{
			name:     "overlapping keys with dp override",
			resAttrs: map[string]string{"service": "old", "region": "us-west"},
			dpAttrs:  map[string]string{"service": "new", "path": "/v2"},
		},
		{
			name:     "resource only",
			resAttrs: map[string]string{"env": "prod", "dc": "dc1"},
			dpAttrs:  map[string]string{},
		},
		{
			name:     "dp only",
			resAttrs: map[string]string{},
			dpAttrs:  map[string]string{"code": "200", "handler": "search"},
		},
		{
			name:     "single overlapping key",
			resAttrs: map[string]string{"x": "resource-val"},
			dpAttrs:  map[string]string{"x": "dp-val"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Manually merge maps (dp overrides resource) to simulate old behavior
			merged := make(map[string]string, len(tt.resAttrs)+len(tt.dpAttrs))
			for k, v := range tt.resAttrs {
				merged[k] = v
			}
			for k, v := range tt.dpAttrs {
				merged[k] = v // dp overrides resource
			}

			legacyResult := buildSeriesKey(merged)
			dualResult := string(buildSeriesKeyDualBytes(tt.resAttrs, tt.dpAttrs))

			if legacyResult != dualResult {
				t.Errorf("legacy vs dual mismatch: legacy=%q, dual=%q", legacyResult, dualResult)
			}
		})
	}
}

func TestBuildLabelKeyDual_BasicLabels(t *testing.T) {
	c := NewCollector([]string{"service", "env"}, StatsLevelFull)

	resAttrs := map[string]string{"service": "gateway"}
	dpAttrs := map[string]string{"env": "production"}

	result := c.buildLabelKeyDual(resAttrs, dpAttrs)
	expected := "service=gateway,env=production"
	if result != expected {
		t.Errorf("expected %q, got %q", expected, result)
	}
}

func TestBuildLabelKeyDual_DpOverridesResource(t *testing.T) {
	c := NewCollector([]string{"service", "env"}, StatsLevelFull)

	resAttrs := map[string]string{"service": "old-svc", "env": "staging"}
	dpAttrs := map[string]string{"service": "new-svc"}

	result := c.buildLabelKeyDual(resAttrs, dpAttrs)

	// dp "service" should override resource "service"
	if strings.Contains(result, "old-svc") {
		t.Error("dp attrs should override resource attrs for 'service'")
	}
	if !strings.Contains(result, "service=new-svc") {
		t.Errorf("expected service=new-svc in result, got %q", result)
	}
	// "env" comes from resource since dp doesn't have it
	if !strings.Contains(result, "env=staging") {
		t.Errorf("expected env=staging from resource, got %q", result)
	}
}

func TestBuildLabelKeyDual_MissingLabels(t *testing.T) {
	c := NewCollector([]string{"service", "env", "cluster"}, StatsLevelFull)

	resAttrs := map[string]string{"service": "api"}
	dpAttrs := map[string]string{"method": "GET"} // none of the tracked labels

	result := c.buildLabelKeyDual(resAttrs, dpAttrs)

	// Only "service" is tracked and present
	expected := "service=api"
	if result != expected {
		t.Errorf("expected %q (only tracked+present labels), got %q", expected, result)
	}
}

func TestBuildLabelKeyDual_MatchesLegacy(t *testing.T) {
	trackLabels := []string{"service", "env", "region"}
	c := NewCollector(trackLabels, StatsLevelFull)

	tests := []struct {
		name     string
		resAttrs map[string]string
		dpAttrs  map[string]string
	}{
		{
			name:     "all labels from resource",
			resAttrs: map[string]string{"service": "gw", "env": "prod", "region": "eu"},
			dpAttrs:  map[string]string{},
		},
		{
			name:     "all labels from dp",
			resAttrs: map[string]string{},
			dpAttrs:  map[string]string{"service": "api", "env": "dev", "region": "us"},
		},
		{
			name:     "split across maps",
			resAttrs: map[string]string{"service": "web", "region": "ap"},
			dpAttrs:  map[string]string{"env": "staging"},
		},
		{
			name:     "dp overrides resource",
			resAttrs: map[string]string{"service": "old", "env": "prod"},
			dpAttrs:  map[string]string{"service": "new", "region": "us"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Manually merge (dp overrides resource) for legacy approach
			merged := make(map[string]string, len(tt.resAttrs)+len(tt.dpAttrs))
			for k, v := range tt.resAttrs {
				merged[k] = v
			}
			for k, v := range tt.dpAttrs {
				merged[k] = v
			}

			legacyResult := c.buildLabelKeyFromAttrs(merged)
			dualResult := c.buildLabelKeyDual(tt.resAttrs, tt.dpAttrs)

			if legacyResult != dualResult {
				t.Errorf("legacy vs dual mismatch: legacy=%q, dual=%q", legacyResult, dualResult)
			}
		})
	}
}

// =============================================================================
// Phase 3: Atomic Counters
// =============================================================================

func TestAtomicCounters_RecordMethods(t *testing.T) {
	c := NewCollector(nil, StatsLevelBasic)

	// OTLP pipeline
	c.RecordReceived(100)
	c.RecordReceived(50)
	if got := c.datapointsReceived.Load(); got != 150 {
		t.Errorf("datapointsReceived: expected 150, got %d", got)
	}

	c.RecordExport(80)
	c.RecordExport(20)
	if got := c.datapointsSent.Load(); got != 100 {
		t.Errorf("datapointsSent: expected 100, got %d", got)
	}
	if got := c.batchesSent.Load(); got != 2 {
		t.Errorf("batchesSent: expected 2, got %d", got)
	}

	c.RecordExportError()
	c.RecordExportError()
	c.RecordExportError()
	if got := c.exportErrors.Load(); got != 3 {
		t.Errorf("exportErrors: expected 3, got %d", got)
	}

	// PRW pipeline
	c.RecordPRWReceived(200, 10)
	if got := c.prwDatapointsReceived.Load(); got != 200 {
		t.Errorf("prwDatapointsReceived: expected 200, got %d", got)
	}
	if got := c.prwTimeseriesReceived.Load(); got != 10 {
		t.Errorf("prwTimeseriesReceived: expected 10, got %d", got)
	}

	c.RecordPRWExport(180, 9)
	if got := c.prwDatapointsSent.Load(); got != 180 {
		t.Errorf("prwDatapointsSent: expected 180, got %d", got)
	}
	if got := c.prwTimeseriesSent.Load(); got != 9 {
		t.Errorf("prwTimeseriesSent: expected 9, got %d", got)
	}
	if got := c.prwBatchesSent.Load(); got != 1 {
		t.Errorf("prwBatchesSent: expected 1, got %d", got)
	}

	c.RecordPRWExportError()
	if got := c.prwExportErrors.Load(); got != 1 {
		t.Errorf("prwExportErrors: expected 1, got %d", got)
	}

	// Byte counters
	c.RecordPRWBytesReceived(4096)
	if got := c.prwBytesReceivedUncompressed.Load(); got != 4096 {
		t.Errorf("prwBytesReceivedUncompressed: expected 4096, got %d", got)
	}
	c.RecordPRWBytesReceivedCompressed(1024)
	if got := c.prwBytesReceivedCompressed.Load(); got != 1024 {
		t.Errorf("prwBytesReceivedCompressed: expected 1024, got %d", got)
	}
	c.RecordPRWBytesSent(3000)
	if got := c.prwBytesSentUncompressed.Load(); got != 3000 {
		t.Errorf("prwBytesSentUncompressed: expected 3000, got %d", got)
	}
	c.RecordPRWBytesSentCompressed(500)
	if got := c.prwBytesSentCompressed.Load(); got != 500 {
		t.Errorf("prwBytesSentCompressed: expected 500, got %d", got)
	}

	c.RecordOTLPBytesReceived(8192)
	if got := c.otlpBytesReceivedUncompressed.Load(); got != 8192 {
		t.Errorf("otlpBytesReceivedUncompressed: expected 8192, got %d", got)
	}
	c.RecordOTLPBytesReceivedCompressed(2048)
	if got := c.otlpBytesReceivedCompressed.Load(); got != 2048 {
		t.Errorf("otlpBytesReceivedCompressed: expected 2048, got %d", got)
	}
	c.RecordOTLPBytesSent(7000)
	if got := c.otlpBytesSentUncompressed.Load(); got != 7000 {
		t.Errorf("otlpBytesSentUncompressed: expected 7000, got %d", got)
	}
	c.RecordOTLPBytesSentCompressed(1500)
	if got := c.otlpBytesSentCompressed.Load(); got != 1500 {
		t.Errorf("otlpBytesSentCompressed: expected 1500, got %d", got)
	}

	// Buffer sizes
	c.SetPRWBufferSize(42)
	if got := c.prwBufferSize.Load(); got != 42 {
		t.Errorf("prwBufferSize: expected 42, got %d", got)
	}
	c.SetOTLPBufferSize(99)
	if got := c.otlpBufferSize.Load(); got != 99 {
		t.Errorf("otlpBufferSize: expected 99, got %d", got)
	}
}

func TestAtomicCounters_ServeHTTP_ReadsAtomics(t *testing.T) {
	c := NewCollector(nil, StatsLevelBasic)

	// Set known values via atomic Record methods
	c.RecordReceived(42)
	c.RecordExport(30)
	c.RecordExportError()
	c.RecordPRWReceived(100, 5)
	c.RecordPRWExport(90, 4)
	c.RecordPRWExportError()

	// Serve the metrics page
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	c.ServeHTTP(w, req)

	body := w.Body.String()

	// Verify the atomic values appear in the output
	checks := []struct {
		metric string
		value  string
	}{
		{"metrics_governor_datapoints_received_total", "42"},
		{"metrics_governor_datapoints_sent_total", "30"},
		{"metrics_governor_batches_sent_total", "1"},
		{"metrics_governor_export_errors_total", "1"},
		{"metrics_governor_prw_datapoints_received_total", "100"},
		{"metrics_governor_prw_timeseries_received_total", "5"},
		{"metrics_governor_prw_datapoints_sent_total", "90"},
		{"metrics_governor_prw_timeseries_sent_total", "4"},
		{"metrics_governor_prw_batches_sent_total", "1"},
		{"metrics_governor_prw_export_errors_total", "1"},
	}

	for _, check := range checks {
		expected := fmt.Sprintf("%s %s", check.metric, check.value)
		if !strings.Contains(body, expected) {
			t.Errorf("expected %q in ServeHTTP output, not found", expected)
		}
	}
}

func TestAtomicCounters_ConcurrentRecordAndServeHTTP(t *testing.T) {
	c := NewCollector(nil, StatsLevelBasic)

	var wg sync.WaitGroup

	// 8 goroutines calling Record* methods
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				c.RecordReceived(1)
				c.RecordExport(1)
				c.RecordPRWReceived(1, 1)
				c.RecordPRWExport(1, 1)
				c.RecordOTLPBytesReceived(10)
				c.RecordPRWBytesReceived(10)
			}
		}()
	}

	// 4 goroutines calling ServeHTTP concurrently
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				req := httptest.NewRequest("GET", "/metrics", nil)
				w := httptest.NewRecorder()
				c.ServeHTTP(w, req)
				// Just verify it doesn't panic and returns valid output
				if w.Code != 200 {
					t.Errorf("expected 200, got %d", w.Code)
				}
			}
		}()
	}

	wg.Wait()

	// After all goroutines complete, verify final values
	if got := c.datapointsReceived.Load(); got != 4000 {
		t.Errorf("datapointsReceived: expected 4000, got %d", got)
	}
	if got := c.datapointsSent.Load(); got != 4000 {
		t.Errorf("datapointsSent: expected 4000, got %d", got)
	}
	if got := c.batchesSent.Load(); got != 4000 {
		t.Errorf("batchesSent: expected 4000, got %d", got)
	}
}

// =============================================================================
// Phase 4: Lock Scope — Per-Metric Lock in processFull
// =============================================================================

func TestProcessFull_PerMetricLock_DatapointConsistency(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelFull)

	const numWorkers = 8
	const batchesPerWorker = 100
	const datapointsPerBatch = 5

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < batchesPerWorker; j++ {
				rm := makeRaceResourceMetrics(
					fmt.Sprintf("svc-%d", workerID%4),
					"http_requests_total",
					datapointsPerBatch,
				)
				c.Process([]*metricspb.ResourceMetrics{rm})
			}
		}(i)
	}
	wg.Wait()

	expectedTotal := uint64(numWorkers * batchesPerWorker * datapointsPerBatch)

	// Verify total datapoints via atomic counter
	if got := c.totalDatapoints.Load(); got != expectedTotal {
		t.Errorf("totalDatapoints: expected %d, got %d", expectedTotal, got)
	}

	// Verify per-metric datapoint sum matches total
	c.mu.RLock()
	ms, ok := c.metricStats["http_requests_total"]
	c.mu.RUnlock()
	if !ok {
		t.Fatal("expected metricStats entry for http_requests_total")
	}
	if ms.Datapoints != expectedTotal {
		t.Errorf("MetricStats.Datapoints: expected %d, got %d", expectedTotal, ms.Datapoints)
	}
}

// =============================================================================
// Phase 5: Config Knobs
// =============================================================================

func TestConfigKnob_CardinalityThreshold_BelowThreshold(t *testing.T) {
	c := NewCollector(nil, StatsLevelFull)
	c.SetStatsCardinalityThreshold(10)

	// Create a metric with 5 datapoints (below threshold of 10)
	dpAttrs := make([]map[string]string, 5)
	for i := 0; i < 5; i++ {
		dpAttrs[i] = map[string]string{"endpoint": fmt.Sprintf("/api/%d", i)}
	}
	m := createTestMetric("low_volume_metric", dpAttrs)
	rm := createTestResourceMetrics(
		map[string]string{"service": "test"},
		[]*metricspb.Metric{m},
	)
	c.Process([]*metricspb.ResourceMetrics{rm})

	c.mu.RLock()
	ms, ok := c.metricStats["low_volume_metric"]
	c.mu.RUnlock()

	if !ok {
		t.Fatal("expected metricStats entry for low_volume_metric")
	}
	if ms.cardinality != nil {
		t.Error("expected nil cardinality tracker for metric below threshold")
	}
	if ms.Datapoints != 5 {
		t.Errorf("expected 5 datapoints, got %d", ms.Datapoints)
	}
}

func TestConfigKnob_CardinalityThreshold_AboveThreshold(t *testing.T) {
	c := NewCollector(nil, StatsLevelFull)
	c.SetStatsCardinalityThreshold(10)

	// Create a metric with 15 datapoints (above threshold of 10)
	dpAttrs := make([]map[string]string, 15)
	for i := 0; i < 15; i++ {
		dpAttrs[i] = map[string]string{"endpoint": fmt.Sprintf("/api/%d", i)}
	}
	m := createTestMetric("high_volume_metric", dpAttrs)
	rm := createTestResourceMetrics(
		map[string]string{"service": "test"},
		[]*metricspb.Metric{m},
	)
	c.Process([]*metricspb.ResourceMetrics{rm})

	c.mu.RLock()
	ms, ok := c.metricStats["high_volume_metric"]
	c.mu.RUnlock()

	if !ok {
		t.Fatal("expected metricStats entry for high_volume_metric")
	}
	if ms.cardinality == nil {
		t.Error("expected non-nil cardinality tracker for metric above threshold")
	}
	if ms.Datapoints != 15 {
		t.Errorf("expected 15 datapoints, got %d", ms.Datapoints)
	}
	// Bloom filter should have tracked some cardinality
	if ms.cardinality.Count() == 0 {
		t.Error("expected non-zero cardinality count for metric above threshold")
	}
}

func TestConfigKnob_CardinalityThreshold_Zero(t *testing.T) {
	c := NewCollector(nil, StatsLevelFull)
	// threshold=0 (default): all metrics tracked

	dpAttrs := make([]map[string]string, 3)
	for i := 0; i < 3; i++ {
		dpAttrs[i] = map[string]string{"path": fmt.Sprintf("/%d", i)}
	}
	m := createTestMetric("any_metric", dpAttrs)
	rm := createTestResourceMetrics(
		map[string]string{"service": "test"},
		[]*metricspb.Metric{m},
	)
	c.Process([]*metricspb.ResourceMetrics{rm})

	c.mu.RLock()
	ms, ok := c.metricStats["any_metric"]
	c.mu.RUnlock()

	if !ok {
		t.Fatal("expected metricStats entry for any_metric")
	}
	if ms.cardinality == nil {
		t.Error("expected non-nil cardinality tracker when threshold=0 (track all)")
	}
}

func TestConfigKnob_LabelCombinationCap_BelowCap(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelFull)
	c.SetStatsMaxLabelCombinations(10)

	// Create 5 unique label combinations (below cap of 10)
	for i := 0; i < 5; i++ {
		dpAttrs := []map[string]string{
			{"service": fmt.Sprintf("svc-%d", i)},
		}
		m := createTestMetric(fmt.Sprintf("metric_%d", i), dpAttrs)
		rm := createTestResourceMetrics(nil, []*metricspb.Metric{m})
		c.Process([]*metricspb.ResourceMetrics{rm})
	}

	c.mu.RLock()
	labelCount := len(c.labelStats)
	c.mu.RUnlock()

	if labelCount != 5 {
		t.Errorf("expected 5 label combinations, got %d", labelCount)
	}
}

func TestConfigKnob_LabelCombinationCap_AboveCap(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelFull)
	c.SetStatsMaxLabelCombinations(10)

	// Create 15 unique label combinations (above cap of 10)
	for i := 0; i < 15; i++ {
		dpAttrs := []map[string]string{
			{"service": fmt.Sprintf("svc-%d", i)},
		}
		m := createTestMetric(fmt.Sprintf("metric_%d", i), dpAttrs)
		rm := createTestResourceMetrics(nil, []*metricspb.Metric{m})
		c.Process([]*metricspb.ResourceMetrics{rm})
	}

	c.mu.RLock()
	labelCount := len(c.labelStats)
	c.mu.RUnlock()

	if labelCount > 10 {
		t.Errorf("expected label combinations capped at 10, got %d", labelCount)
	}
	if labelCount < 10 {
		t.Errorf("expected at least 10 label combinations before cap, got %d", labelCount)
	}
}

func TestConfigKnob_LabelCombinationCap_Zero(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelFull)
	// cap=0 (default): unlimited tracking

	// Create 20 unique label combinations
	for i := 0; i < 20; i++ {
		dpAttrs := []map[string]string{
			{"service": fmt.Sprintf("svc-%d", i)},
		}
		m := createTestMetric(fmt.Sprintf("metric_%d", i), dpAttrs)
		rm := createTestResourceMetrics(nil, []*metricspb.Metric{m})
		c.Process([]*metricspb.ResourceMetrics{rm})
	}

	c.mu.RLock()
	labelCount := len(c.labelStats)
	c.mu.RUnlock()

	if labelCount != 20 {
		t.Errorf("expected 20 label combinations with unlimited cap, got %d", labelCount)
	}
}

// =============================================================================
// Pool Tests
// =============================================================================

func TestKeyBufPool_NoStaleData(t *testing.T) {
	// Get a buffer from the pool
	bp := keyBufPool.Get().(*[]byte)
	buf := (*bp)[:0]

	// Verify it starts clean (zero length after reslice)
	if len(buf) != 0 {
		t.Errorf("expected zero-length buffer from pool, got length %d", len(buf))
	}

	// Write some data
	buf = append(buf, "stale-data-here"...)
	if len(buf) != 15 {
		t.Errorf("expected length 15 after write, got %d", len(buf))
	}

	// Return to pool
	*bp = buf
	keyBufPool.Put(bp)

	// Get again — the buffer may be reused but should be resliceable to zero
	bp2 := keyBufPool.Get().(*[]byte)
	buf2 := (*bp2)[:0]
	if len(buf2) != 0 {
		t.Errorf("expected zero-length buffer after pool reuse, got length %d", len(buf2))
	}

	// Verify we can write fresh data without stale contamination
	buf2 = append(buf2, "fresh-data"...)
	result := string(buf2)
	if result != "fresh-data" {
		t.Errorf("expected 'fresh-data', got %q", result)
	}

	*bp2 = buf2
	keyBufPool.Put(bp2)
}

func TestPrecompSlicePool_Reuse(t *testing.T) {
	// Get a slice from the pool
	sp := precompSlicePool.Get().(*[]precomputedDatapoint)
	s := (*sp)[:0]

	// Verify it starts at zero length
	if len(s) != 0 {
		t.Errorf("expected zero-length slice from pool, got length %d", len(s))
	}

	// Append some items
	s = append(s, precomputedDatapoint{seriesKey: []byte("key1"), labelKey: "label1"})
	s = append(s, precomputedDatapoint{seriesKey: []byte("key2"), labelKey: "label2"})
	if len(s) != 2 {
		t.Errorf("expected length 2, got %d", len(s))
	}

	// Return to pool after reslicing to zero (as processFull does)
	*sp = s[:0]
	precompSlicePool.Put(sp)

	// Get again and verify zero length with preserved capacity
	sp2 := precompSlicePool.Get().(*[]precomputedDatapoint)
	s2 := (*sp2)[:0]
	if len(s2) != 0 {
		t.Errorf("expected zero-length slice after pool reuse, got length %d", len(s2))
	}
	// Capacity should be at least 2 (from previous use)
	if cap(s2) < 2 {
		t.Errorf("expected capacity >= 2 after reuse, got %d", cap(s2))
	}

	// Can append fresh data
	s2 = append(s2, precomputedDatapoint{seriesKey: []byte("fresh"), labelKey: "new"})
	if string(s2[0].seriesKey) != "fresh" || s2[0].labelKey != "new" {
		t.Errorf("unexpected data after pool reuse: seriesKey=%q, labelKey=%q",
			s2[0].seriesKey, s2[0].labelKey)
	}

	*sp2 = s2[:0]
	precompSlicePool.Put(sp2)
}

// =============================================================================
// Helpers (keep unused import references alive)
// =============================================================================

// Ensure sort is used (for TestBuildSeriesKeyDualBytes_SortOrder verification).
var _ = sort.Strings
