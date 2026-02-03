//go:build integration
// +build integration

package test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// This file contains e2e integration tests for the metrics-governor ecosystem.
// These tests require the full docker-compose environment to be running.
//
// To run these tests:
//   1. Start the docker-compose environment: docker compose up -d
//   2. Wait for services to be ready: sleep 30
//   3. Run tests: go test -tags=integration -v ./test/...
//   4. Stop the environment: docker compose down

const (
	metricsGovernorEndpoint = "http://localhost:9090"
	metricsGovernorGRPC     = "localhost:14317"
	victoriametricsEndpoint = "http://localhost:8428"
	generatorEndpoint       = "http://localhost:9091"
	verifierEndpoint        = "http://localhost:9092"
	otelCollectorEndpoint   = "http://localhost:8888"
	otelCollectorGRPC       = "localhost:4317"

	// Test timeouts
	testTimeout     = 60 * time.Second
	pollInterval    = 5 * time.Second
	dataFlowTimeout = 120 * time.Second
)

// TestE2E_ServiceHealth tests that all services are healthy
func TestE2E_ServiceHealth(t *testing.T) {
	services := []struct {
		name     string
		endpoint string
		path     string
	}{
		{"metrics-governor", metricsGovernorEndpoint, "/metrics"},
		{"victoriametrics", victoriametricsEndpoint, "/api/v1/status/tsdb"},
		{"generator", generatorEndpoint, "/metrics"},
		{"verifier", verifierEndpoint, "/metrics"},
		{"otel-collector", otelCollectorEndpoint, "/metrics"},
	}

	for _, svc := range services {
		t.Run(svc.name, func(t *testing.T) {
			resp, err := http.Get(svc.endpoint + svc.path)
			if err != nil {
				t.Fatalf("Failed to connect to %s: %v", svc.name, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				t.Errorf("%s returned status %d, expected 200", svc.name, resp.StatusCode)
			}
		})
	}
}

// TestE2E_MetricsFlowToVictoriaMetrics tests that metrics flow from generator to VictoriaMetrics
func TestE2E_MetricsFlowToVictoriaMetrics(t *testing.T) {
	deadline := time.Now().Add(dataFlowTimeout)

	for time.Now().Before(deadline) {
		// Query VictoriaMetrics for the verification counter
		resp, err := http.Get(victoriametricsEndpoint + "/api/v1/query?query=max(generator_verification_counter_total)")
		if err != nil {
			t.Logf("Query failed: %v", err)
			time.Sleep(pollInterval)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var vmResp struct {
			Status string `json:"status"`
			Data   struct {
				Result []struct {
					Value []interface{} `json:"value"`
				} `json:"result"`
			} `json:"data"`
		}

		if err := json.Unmarshal(body, &vmResp); err != nil {
			t.Logf("Parse failed: %v", err)
			time.Sleep(pollInterval)
			continue
		}

		if vmResp.Status == "success" && len(vmResp.Data.Result) > 0 {
			if len(vmResp.Data.Result[0].Value) >= 2 {
				valStr, ok := vmResp.Data.Result[0].Value[1].(string)
				if ok && valStr != "0" {
					t.Logf("Metrics flowing - verification counter: %s", valStr)
					return
				}
			}
		}

		t.Logf("Waiting for metrics to flow...")
		time.Sleep(pollInterval)
	}

	t.Fatal("Metrics did not flow to VictoriaMetrics within timeout")
}

// TestE2E_MetricsGovernorStats tests that metrics-governor is tracking stats correctly
func TestE2E_MetricsGovernorStats(t *testing.T) {
	deadline := time.Now().Add(testTimeout)

	for time.Now().Before(deadline) {
		resp, err := http.Get(metricsGovernorEndpoint + "/metrics")
		if err != nil {
			t.Logf("Failed to get metrics: %v", err)
			time.Sleep(pollInterval)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		metrics := string(body)

		// Check for expected metrics
		expectedMetrics := []string{
			"metrics_governor_datapoints_received_total",
			"metrics_governor_datapoints_sent_total",
			"metrics_governor_batches_sent_total",
		}

		allFound := true
		for _, metric := range expectedMetrics {
			if !strings.Contains(metrics, metric) {
				t.Logf("Metric %s not found yet", metric)
				allFound = false
				break
			}
		}

		if allFound {
			// Verify datapoints received is > 0
			received := extractMetricValue(metrics, "metrics_governor_datapoints_received_total")
			if received > 0 {
				t.Logf("Metrics-governor receiving data: %d datapoints", received)
				return
			}
		}

		time.Sleep(pollInterval)
	}

	t.Fatal("Metrics-governor stats not reporting correctly within timeout")
}

// TestE2E_GeneratorStats tests that generator is reporting stats correctly
func TestE2E_GeneratorStats(t *testing.T) {
	resp, err := http.Get(generatorEndpoint + "/metrics")
	if err != nil {
		t.Fatalf("Failed to get generator metrics: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	metrics := string(body)

	// Check for expected metrics
	expectedMetrics := []string{
		"generator_metrics_sent_total",
		"generator_datapoints_sent_total",
		"generator_batches_sent_total",
		"generator_runtime_seconds",
	}

	for _, metric := range expectedMetrics {
		if !strings.Contains(metrics, metric) {
			t.Errorf("Expected metric %s not found in generator output", metric)
		}
	}

	// Verify data is being generated
	sent := extractMetricValue(metrics, "generator_datapoints_sent_total")
	if sent == 0 {
		t.Error("Generator has not sent any datapoints")
	} else {
		t.Logf("Generator has sent %d datapoints", sent)
	}
}

// TestE2E_VerifierStats tests that verifier is reporting stats correctly
func TestE2E_VerifierStats(t *testing.T) {
	resp, err := http.Get(verifierEndpoint + "/metrics")
	if err != nil {
		t.Fatalf("Failed to get verifier metrics: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	metrics := string(body)

	// Check for expected metrics
	expectedMetrics := []string{
		"verifier_checks_total",
		"verifier_checks_passed_total",
		"verifier_runtime_seconds",
	}

	for _, metric := range expectedMetrics {
		if !strings.Contains(metrics, metric) {
			t.Errorf("Expected metric %s not found in verifier output", metric)
		}
	}
}

// TestE2E_IngestionRate tests that the ingestion rate is acceptable
func TestE2E_IngestionRate(t *testing.T) {
	deadline := time.Now().Add(dataFlowTimeout)

	for time.Now().Before(deadline) {
		resp, err := http.Get(metricsGovernorEndpoint + "/metrics")
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		metrics := string(body)

		received := extractMetricValue(metrics, "metrics_governor_datapoints_received_total")
		sent := extractMetricValue(metrics, "metrics_governor_datapoints_sent_total")

		if received > 1000 && sent > 0 {
			rate := float64(sent) / float64(received) * 100
			t.Logf("Ingestion rate: %.2f%% (received: %d, sent: %d)", rate, received, sent)

			if rate >= 90.0 {
				t.Logf("Ingestion rate is acceptable: %.2f%%", rate)
				return
			}
			if rate < 50.0 {
				t.Errorf("Ingestion rate is too low: %.2f%%", rate)
				return
			}
		}

		time.Sleep(pollInterval)
	}

	t.Fatal("Could not verify ingestion rate within timeout")
}

// TestE2E_HighCardinalityMetrics tests that high cardinality metrics are handled
func TestE2E_HighCardinalityMetrics(t *testing.T) {
	deadline := time.Now().Add(testTimeout)

	for time.Now().Before(deadline) {
		resp, err := http.Get(victoriametricsEndpoint + "/api/v1/query?query=count(high_cardinality_metric_total)")
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var vmResp struct {
			Status string `json:"status"`
			Data   struct {
				Result []struct {
					Value []interface{} `json:"value"`
				} `json:"result"`
			} `json:"data"`
		}

		if err := json.Unmarshal(body, &vmResp); err != nil {
			time.Sleep(pollInterval)
			continue
		}

		if vmResp.Status == "success" && len(vmResp.Data.Result) > 0 {
			if len(vmResp.Data.Result[0].Value) >= 2 {
				valStr, ok := vmResp.Data.Result[0].Value[1].(string)
				if ok && valStr != "0" {
					t.Logf("High cardinality metrics present: %s time series", valStr)
					return
				}
			}
		}

		time.Sleep(pollInterval)
	}

	t.Log("High cardinality metrics not found (may be expected if limits are filtering)")
}

// TestE2E_OtelCollectorMetrics tests that otel-collector is exporting metrics
func TestE2E_OtelCollectorMetrics(t *testing.T) {
	resp, err := http.Get(otelCollectorEndpoint + "/metrics")
	if err != nil {
		t.Fatalf("Failed to get otel-collector metrics: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	metrics := string(body)

	// Check for expected otel-collector metrics
	expectedMetrics := []string{
		"otelcol_receiver_accepted_metric_points",
		"otelcol_exporter_sent_metric_points",
	}

	for _, metric := range expectedMetrics {
		if strings.Contains(metrics, metric) {
			t.Logf("Found otel-collector metric: %s", metric)
		}
	}
}

// TestE2E_VictoriaMetricsTimeSeries tests that VictoriaMetrics has expected time series
func TestE2E_VictoriaMetricsTimeSeries(t *testing.T) {
	deadline := time.Now().Add(testTimeout)

	for time.Now().Before(deadline) {
		resp, err := http.Get(victoriametricsEndpoint + "/api/v1/query?query=count({__name__=~\".+\"})")
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var vmResp struct {
			Status string `json:"status"`
			Data   struct {
				Result []struct {
					Value []interface{} `json:"value"`
				} `json:"result"`
			} `json:"data"`
		}

		if err := json.Unmarshal(body, &vmResp); err != nil {
			time.Sleep(pollInterval)
			continue
		}

		if vmResp.Status == "success" && len(vmResp.Data.Result) > 0 {
			if len(vmResp.Data.Result[0].Value) >= 2 {
				valStr, ok := vmResp.Data.Result[0].Value[1].(string)
				if ok {
					t.Logf("VictoriaMetrics has %s total time series", valStr)
					return
				}
			}
		}

		time.Sleep(pollInterval)
	}

	t.Fatal("Could not query VictoriaMetrics time series count")
}

// TestE2E_VerificationPassRate tests that verification pass rate is acceptable
func TestE2E_VerificationPassRate(t *testing.T) {
	deadline := time.Now().Add(dataFlowTimeout)

	for time.Now().Before(deadline) {
		resp, err := http.Get(verifierEndpoint + "/metrics")
		if err != nil {
			time.Sleep(pollInterval)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		metrics := string(body)

		total := extractMetricValue(metrics, "verifier_checks_total")
		passed := extractMetricValue(metrics, "verifier_checks_passed_total")

		if total >= 3 { // Wait for at least 3 checks
			passRate := float64(passed) / float64(total) * 100
			t.Logf("Verification pass rate: %.2f%% (%d/%d)", passRate, passed, total)

			if passRate >= 80.0 {
				t.Logf("Verification pass rate is acceptable: %.2f%%", passRate)
				return
			}
		}

		time.Sleep(pollInterval)
	}

	t.Fatal("Could not verify pass rate within timeout")
}

// TestE2E_NoExportErrors tests that there are minimal export errors
func TestE2E_NoExportErrors(t *testing.T) {
	resp, err := http.Get(metricsGovernorEndpoint + "/metrics")
	if err != nil {
		t.Fatalf("Failed to get metrics: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	metrics := string(body)

	errors := extractMetricValue(metrics, "metrics_governor_export_errors_total")
	sent := extractMetricValue(metrics, "metrics_governor_batches_sent_total")

	if sent > 0 {
		errorRate := float64(errors) / float64(sent) * 100
		if errorRate > 10.0 {
			t.Errorf("Export error rate too high: %.2f%% (%d errors / %d batches)", errorRate, errors, sent)
		} else {
			t.Logf("Export error rate: %.2f%%", errorRate)
		}
	}
}

// TestE2E_LimitsEnforcement tests that limits are being enforced (in dry-run mode, just logged)
func TestE2E_LimitsEnforcement(t *testing.T) {
	resp, err := http.Get(metricsGovernorEndpoint + "/metrics")
	if err != nil {
		t.Fatalf("Failed to get metrics: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	metrics := string(body)

	// In dry-run mode, we should see violation logging but no actual drops
	if strings.Contains(metrics, "metrics_governor_limits_violations_total") {
		violations := extractMetricValue(metrics, "metrics_governor_limits_violations_total")
		t.Logf("Limits violations logged (dry-run): %d", violations)
	}

	// Check cardinality tracking
	if strings.Contains(metrics, "metrics_governor_cardinality") {
		t.Log("Cardinality tracking is active")
	}
}

// TestE2E_CacheMetricsExposed verifies all caching metrics are registered and exposed on /metrics
func TestE2E_CacheMetricsExposed(t *testing.T) {
	expectedMetrics := []string{
		// Rule Cache metrics
		"metrics_governor_rule_cache_hits_total",
		"metrics_governor_rule_cache_misses_total",
		"metrics_governor_rule_cache_evictions_total",
		"metrics_governor_rule_cache_size",
		"metrics_governor_rule_cache_max_size",
		"metrics_governor_rule_cache_hit_ratio",
		"metrics_governor_rule_cache_negative_entries",
		// Compression Pool metrics
		"metrics_governor_compression_pool_gets_total",
		"metrics_governor_compression_pool_puts_total",
		"metrics_governor_compression_pool_discards_total",
		"metrics_governor_compression_pool_new_total",
		"metrics_governor_compression_buffer_pool_gets_total",
		"metrics_governor_compression_buffer_pool_puts_total",
		// String Intern Pool metrics
		"metrics_governor_intern_hits_total",
		"metrics_governor_intern_misses_total",
		"metrics_governor_intern_pool_size",
		// Series Key Pool metrics
		"metrics_governor_serieskey_pool_gets_total",
		"metrics_governor_serieskey_pool_puts_total",
		"metrics_governor_serieskey_pool_discards_total",
	}

	deadline := time.Now().Add(testTimeout)

	for time.Now().Before(deadline) {
		resp, err := http.Get(metricsGovernorEndpoint + "/metrics")
		if err != nil {
			t.Logf("Failed to get metrics: %v", err)
			time.Sleep(pollInterval)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		metrics := string(body)

		allFound := true
		for _, metric := range expectedMetrics {
			if !strings.Contains(metrics, metric) {
				t.Logf("Metric %s not found yet", metric)
				allFound = false
				break
			}
		}

		if allFound {
			t.Log("All caching metrics are exposed on /metrics endpoint")
			for _, metric := range expectedMetrics {
				val := extractMetricValue(metrics, metric)
				t.Logf("  %s = %d", metric, val)
			}
			return
		}

		time.Sleep(pollInterval)
	}

	// Final check: report which metrics are missing
	resp, err := http.Get(metricsGovernorEndpoint + "/metrics")
	if err != nil {
		t.Fatalf("Failed to get metrics on final attempt: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	metrics := string(body)

	for _, metric := range expectedMetrics {
		if !strings.Contains(metrics, metric) {
			t.Errorf("Caching metric not found: %s", metric)
		}
	}
}

// TestE2E_RuleCacheBehavior verifies the rule cache is populated and actively used
func TestE2E_RuleCacheBehavior(t *testing.T) {
	deadline := time.Now().Add(dataFlowTimeout)

	for time.Now().Before(deadline) {
		resp, err := http.Get(metricsGovernorEndpoint + "/metrics")
		if err != nil {
			t.Logf("Failed to get metrics: %v", err)
			time.Sleep(pollInterval)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		metrics := string(body)

		cacheSize := extractMetricValue(metrics, "metrics_governor_rule_cache_size")
		cacheHits := extractMetricValue(metrics, "metrics_governor_rule_cache_hits_total")
		cacheMisses := extractMetricValue(metrics, "metrics_governor_rule_cache_misses_total")
		cacheEvictions := extractMetricValue(metrics, "metrics_governor_rule_cache_evictions_total")
		cacheMaxSize := extractMetricValue(metrics, "metrics_governor_rule_cache_max_size")
		cacheHitRatio := extractMetricValueFloat64(metrics, "metrics_governor_rule_cache_hit_ratio")
		negativeEntries := extractMetricValue(metrics, "metrics_governor_rule_cache_negative_entries")

		totalLookups := cacheHits + cacheMisses

		t.Logf("Rule cache stats: size=%d, hits=%d, misses=%d, evictions=%d, maxSize=%d, hitRatio=%.4f, negativeEntries=%d",
			cacheSize, cacheHits, cacheMisses, cacheEvictions, cacheMaxSize, cacheHitRatio, negativeEntries)

		if cacheSize > 0 && totalLookups > 0 {
			if cacheMaxSize != 10000 {
				t.Errorf("Expected rule_cache_max_size=10000 (from docker-compose config), got %d", cacheMaxSize)
			}

			if cacheHitRatio <= 0 {
				t.Logf("Warning: cache hit ratio is %.4f, expected > 0", cacheHitRatio)
			}

			t.Logf("Rule cache is active: size=%d, total lookups=%d, hit ratio=%.4f", cacheSize, totalLookups, cacheHitRatio)
			return
		}

		time.Sleep(pollInterval)
	}

	t.Fatal("Rule cache did not become active within timeout")
}

// TestE2E_CompressionPoolActive verifies compression pooling is operational
func TestE2E_CompressionPoolActive(t *testing.T) {
	deadline := time.Now().Add(dataFlowTimeout)

	for time.Now().Before(deadline) {
		resp, err := http.Get(metricsGovernorEndpoint + "/metrics")
		if err != nil {
			t.Logf("Failed to get metrics: %v", err)
			time.Sleep(pollInterval)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		metrics := string(body)

		poolGets := extractMetricValue(metrics, "metrics_governor_compression_pool_gets_total")
		poolPuts := extractMetricValue(metrics, "metrics_governor_compression_pool_puts_total")
		poolDiscards := extractMetricValue(metrics, "metrics_governor_compression_pool_discards_total")
		poolNew := extractMetricValue(metrics, "metrics_governor_compression_pool_new_total")
		bufferGets := extractMetricValue(metrics, "metrics_governor_compression_buffer_pool_gets_total")
		bufferPuts := extractMetricValue(metrics, "metrics_governor_compression_buffer_pool_puts_total")

		t.Logf("Compression pool stats: gets=%d, puts=%d, discards=%d, new=%d, bufferGets=%d, bufferPuts=%d",
			poolGets, poolPuts, poolDiscards, poolNew, bufferGets, bufferPuts)

		if poolGets > 0 && poolPuts > 0 && bufferGets > 0 {
			if poolGets > 0 {
				reuseRatio := float64(poolPuts) / float64(poolGets) * 100
				t.Logf("Compression pool reuse ratio: %.2f%% (puts/gets)", reuseRatio)
			}
			t.Logf("Compression pooling is operational")
			return
		}

		time.Sleep(pollInterval)
	}

	t.Fatal("Compression pool did not become active within timeout")
}

// TestE2E_InternPoolActive verifies string interning is working
func TestE2E_InternPoolActive(t *testing.T) {
	deadline := time.Now().Add(dataFlowTimeout)

	for time.Now().Before(deadline) {
		resp, err := http.Get(metricsGovernorEndpoint + "/metrics")
		if err != nil {
			t.Logf("Failed to get metrics: %v", err)
			time.Sleep(pollInterval)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		metrics := string(body)

		internHits := extractMetricValue(metrics, "metrics_governor_intern_hits_total")
		internMisses := extractMetricValue(metrics, "metrics_governor_intern_misses_total")
		internPoolSize := extractMetricValue(metrics, "metrics_governor_intern_pool_size")

		t.Logf("Intern pool stats: hits=%d, misses=%d, poolSize=%d", internHits, internMisses, internPoolSize)

		if internHits > 0 && internPoolSize > 0 {
			totalLookups := internHits + internMisses
			if totalLookups > 0 {
				hitRatio := float64(internHits) / float64(totalLookups) * 100
				t.Logf("Intern pool hit ratio: %.2f%% (%d/%d)", hitRatio, internHits, totalLookups)
			}
			t.Logf("String interning is active")
			return
		}

		time.Sleep(pollInterval)
	}

	t.Fatal("Intern pool did not become active within timeout")
}

// TestE2E_SeriesKeyPoolActive verifies series key pooling is operational
func TestE2E_SeriesKeyPoolActive(t *testing.T) {
	deadline := time.Now().Add(dataFlowTimeout)

	for time.Now().Before(deadline) {
		resp, err := http.Get(metricsGovernorEndpoint + "/metrics")
		if err != nil {
			t.Logf("Failed to get metrics: %v", err)
			time.Sleep(pollInterval)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		metrics := string(body)

		poolGets := extractMetricValue(metrics, "metrics_governor_serieskey_pool_gets_total")
		poolPuts := extractMetricValue(metrics, "metrics_governor_serieskey_pool_puts_total")
		poolDiscards := extractMetricValue(metrics, "metrics_governor_serieskey_pool_discards_total")

		t.Logf("Series key pool stats: gets=%d, puts=%d, discards=%d", poolGets, poolPuts, poolDiscards)

		if poolGets > 0 && poolPuts > 0 {
			t.Logf("Series key pooling is operational (discards=%d)", poolDiscards)
			return
		}

		time.Sleep(pollInterval)
	}

	t.Fatal("Series key pool did not become active within timeout")
}

// TestE2E_CacheMemoryBounded verifies caching does not cause unbounded memory growth
func TestE2E_CacheMemoryBounded(t *testing.T) {
	// First scrape
	resp1, err := http.Get(metricsGovernorEndpoint + "/metrics")
	if err != nil {
		t.Fatalf("Failed to get metrics on first scrape: %v", err)
	}
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()
	metrics1 := string(body1)

	cacheSize1 := extractMetricValue(metrics1, "metrics_governor_rule_cache_size")
	cacheMaxSize1 := extractMetricValue(metrics1, "metrics_governor_rule_cache_max_size")
	internPoolSize1 := extractMetricValue(metrics1, "metrics_governor_intern_pool_size")

	t.Logf("First scrape: cacheSize=%d, cacheMaxSize=%d, internPoolSize=%d", cacheSize1, cacheMaxSize1, internPoolSize1)

	// Verify cache size is within bounds on first scrape
	if cacheMaxSize1 > 0 && cacheSize1 > cacheMaxSize1 {
		t.Errorf("Rule cache size (%d) exceeds max size (%d) on first scrape", cacheSize1, cacheMaxSize1)
	}

	// Wait 10 seconds for more activity
	t.Log("Waiting 10 seconds between scrapes...")
	time.Sleep(10 * time.Second)

	// Second scrape
	resp2, err := http.Get(metricsGovernorEndpoint + "/metrics")
	if err != nil {
		t.Fatalf("Failed to get metrics on second scrape: %v", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	metrics2 := string(body2)

	cacheSize2 := extractMetricValue(metrics2, "metrics_governor_rule_cache_size")
	cacheMaxSize2 := extractMetricValue(metrics2, "metrics_governor_rule_cache_max_size")
	internPoolSize2 := extractMetricValue(metrics2, "metrics_governor_intern_pool_size")

	t.Logf("Second scrape: cacheSize=%d, cacheMaxSize=%d, internPoolSize=%d", cacheSize2, cacheMaxSize2, internPoolSize2)

	// Verify cache size is within bounds on second scrape
	if cacheMaxSize2 > 0 && cacheSize2 > cacheMaxSize2 {
		t.Errorf("Rule cache size (%d) exceeds max size (%d) on second scrape", cacheSize2, cacheMaxSize2)
	}

	// Verify intern pool is not growing unboundedly
	// In steady state, growth should be < 1000 entries between scrapes
	internGrowth := internPoolSize2 - internPoolSize1
	t.Logf("Intern pool growth between scrapes: %d entries", internGrowth)
	if internGrowth > 1000 {
		t.Errorf("Intern pool growing too fast: %d new entries in 10 seconds (threshold: 1000)", internGrowth)
	} else {
		t.Logf("Intern pool growth is bounded (%d entries in 10s)", internGrowth)
	}

	// Log overall cache memory health
	t.Logf("Cache memory bounded check passed: rule cache %d/%d, intern pool growth %d",
		cacheSize2, cacheMaxSize2, internGrowth)
}

// Helper function to extract metric value from Prometheus text format
func extractMetricValue(metricsText, metricName string) int64 {
	lines := strings.Split(metricsText, "\n")
	var total int64 = 0

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(line, metricName) {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				valueStr := parts[len(parts)-1]
				var val float64
				fmt.Sscanf(valueStr, "%f", &val)
				total += int64(val)
			}
		}
	}
	return total
}

// Helper function to extract float64 metric value from Prometheus text format (for ratio/gauge metrics)
func extractMetricValueFloat64(metricsText, metricName string) float64 {
	lines := strings.Split(metricsText, "\n")
	var total float64 = 0

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(line, metricName) {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				valueStr := parts[len(parts)-1]
				var val float64
				fmt.Sscanf(valueStr, "%f", &val)
				total += val
			}
		}
	}
	return total
}
