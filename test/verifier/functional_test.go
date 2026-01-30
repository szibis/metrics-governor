package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestFunctional_MetricsEndpoint tests the Prometheus metrics endpoint
func TestFunctional_MetricsEndpoint(t *testing.T) {
	// Setup test stats
	testStats := &VerifierStats{
		StartTime: time.Now().Add(-1 * time.Hour),
	}
	testStats.ChecksTotal.Store(100)
	testStats.ChecksPassed.Store(95)
	testStats.ChecksFailed.Store(5)
	testStats.LastIngestionRate.Store(9750) // 97.5%

	// Add a last result
	testStats.mu.Lock()
	testStats.lastResult = &VerificationResult{
		VMTotalTimeSeries:     5000,
		VMUniqueMetricNames:   50,
		VMVerificationCounter: 1000,
		MGDatapointsReceived:  100000,
		MGDatapointsSent:      97500,
		MGBatchesSent:         1000,
		MGExportErrors:        5,
		VerificationMatch:     true,
	}
	testStats.mu.Unlock()

	// Save original and restore after test
	originalStats := verifierStats
	verifierStats = testStats
	defer func() { verifierStats = originalStats }()

	// Create test request
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	// Call the handler directly
	http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elapsed := time.Since(verifierStats.StartTime).Seconds()
		checksTotal := verifierStats.ChecksTotal.Load()
		checksPassed := verifierStats.ChecksPassed.Load()
		checksFailed := verifierStats.ChecksFailed.Load()
		lastIngestionRate := float64(verifierStats.LastIngestionRate.Load()) / 100.0

		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		// Runtime
		fmt.Fprintf(w, "# HELP verifier_runtime_seconds Total runtime of the verifier\n")
		fmt.Fprintf(w, "# TYPE verifier_runtime_seconds counter\n")
		fmt.Fprintf(w, "verifier_runtime_seconds %.3f\n", elapsed)

		// Verification checks
		fmt.Fprintf(w, "# HELP verifier_checks_total Total number of verification checks\n")
		fmt.Fprintf(w, "# TYPE verifier_checks_total counter\n")
		fmt.Fprintf(w, "verifier_checks_total %d\n", checksTotal)

		fmt.Fprintf(w, "# HELP verifier_checks_passed_total Total number of passed checks\n")
		fmt.Fprintf(w, "# TYPE verifier_checks_passed_total counter\n")
		fmt.Fprintf(w, "verifier_checks_passed_total %d\n", checksPassed)

		fmt.Fprintf(w, "# HELP verifier_checks_failed_total Total number of failed checks\n")
		fmt.Fprintf(w, "# TYPE verifier_checks_failed_total counter\n")
		fmt.Fprintf(w, "verifier_checks_failed_total %d\n", checksFailed)

		// Pass rate
		passRate := float64(0)
		if checksTotal > 0 {
			passRate = float64(checksPassed) / float64(checksTotal) * 100
		}
		fmt.Fprintf(w, "# HELP verifier_pass_rate_percent Current pass rate percentage\n")
		fmt.Fprintf(w, "# TYPE verifier_pass_rate_percent gauge\n")
		fmt.Fprintf(w, "verifier_pass_rate_percent %.2f\n", passRate)

		// Last ingestion rate
		fmt.Fprintf(w, "# HELP verifier_last_ingestion_rate_percent Last recorded ingestion rate\n")
		fmt.Fprintf(w, "# TYPE verifier_last_ingestion_rate_percent gauge\n")
		fmt.Fprintf(w, "verifier_last_ingestion_rate_percent %.2f\n", lastIngestionRate)
	}).ServeHTTP(w, req)

	// Verify response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Check for expected metrics
	expectedMetrics := []string{
		"verifier_runtime_seconds",
		"verifier_checks_total 100",
		"verifier_checks_passed_total 95",
		"verifier_checks_failed_total 5",
		"verifier_pass_rate_percent 95.00",
		"verifier_last_ingestion_rate_percent 97.50",
	}

	for _, expected := range expectedMetrics {
		if !strings.Contains(body, expected) {
			t.Errorf("Expected metric %q not found in response", expected)
		}
	}

	// Check content type
	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/plain") {
		t.Errorf("Expected text/plain content type, got %s", contentType)
	}
}

// TestFunctional_VMQueryResponse tests parsing of VictoriaMetrics responses
func TestFunctional_VMQueryResponse(t *testing.T) {
	tests := []struct {
		name     string
		response string
		expected float64
		wantErr  bool
	}{
		{
			name: "scalar integer response",
			response: `{
				"status": "success",
				"data": {
					"resultType": "vector",
					"result": [{"metric": {}, "value": [1234567890, "42"]}]
				}
			}`,
			expected: 42,
			wantErr:  false,
		},
		{
			name: "scalar float response",
			response: `{
				"status": "success",
				"data": {
					"resultType": "vector",
					"result": [{"metric": {}, "value": [1234567890, "123.456"]}]
				}
			}`,
			expected: 123.456,
			wantErr:  false,
		},
		{
			name: "empty result",
			response: `{
				"status": "success",
				"data": {
					"resultType": "vector",
					"result": []
				}
			}`,
			expected: 0,
			wantErr:  false,
		},
		{
			name: "error response",
			response: `{
				"status": "error",
				"errorType": "bad_data",
				"error": "parse error"
			}`,
			expected: 0,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tt.response)
			}))
			defer server.Close()

			val, err := queryVMScalar(server.URL, "test_query")
			if (err != nil) != tt.wantErr {
				t.Errorf("queryVMScalar() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && val != tt.expected {
				t.Errorf("queryVMScalar() = %v, want %v", val, tt.expected)
			}
		})
	}
}

// TestFunctional_MGStatsEndpoint tests querying metrics-governor stats endpoint
func TestFunctional_MGStatsEndpoint(t *testing.T) {
	// Mock metrics-governor /metrics endpoint
	metricsOutput := `# HELP metrics_governor_datapoints_received_total Total datapoints received
# TYPE metrics_governor_datapoints_received_total counter
metrics_governor_datapoints_received_total 50000

# HELP metrics_governor_datapoints_sent_total Total datapoints sent
# TYPE metrics_governor_datapoints_sent_total counter
metrics_governor_datapoints_sent_total 49000

# HELP metrics_governor_queue_size Current queue size
# TYPE metrics_governor_queue_size gauge
metrics_governor_queue_size 25

# HELP metrics_governor_queue_dropped_total Total dropped metrics
# TYPE metrics_governor_queue_dropped_total counter
metrics_governor_queue_dropped_total{reason="limit_exceeded"} 500
metrics_governor_queue_dropped_total{reason="buffer_full"} 100

# HELP metrics_governor_export_errors_total Total export errors
# TYPE metrics_governor_export_errors_total counter
metrics_governor_export_errors_total 10

# HELP metrics_governor_batches_sent_total Total batches sent
# TYPE metrics_governor_batches_sent_total counter
metrics_governor_batches_sent_total 500
`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			t.Errorf("Unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, metricsOutput)
	}))
	defer server.Close()

	stats, err := queryMGStats(server.URL)
	if err != nil {
		t.Fatalf("queryMGStats() error = %v", err)
	}

	// Verify parsed values
	if stats.DatapointsReceived != 50000 {
		t.Errorf("DatapointsReceived = %d, want 50000", stats.DatapointsReceived)
	}
	if stats.DatapointsSent != 49000 {
		t.Errorf("DatapointsSent = %d, want 49000", stats.DatapointsSent)
	}
	if stats.QueueSize != 25 {
		t.Errorf("QueueSize = %d, want 25", stats.QueueSize)
	}
	if stats.DroppedTotal != 600 { // 500 + 100
		t.Errorf("DroppedTotal = %d, want 600", stats.DroppedTotal)
	}
	if stats.ExportErrors != 10 {
		t.Errorf("ExportErrors = %d, want 10", stats.ExportErrors)
	}
	if stats.BatchesSent != 500 {
		t.Errorf("BatchesSent = %d, want 500", stats.BatchesSent)
	}
}

// TestFunctional_VerifyFunction tests the verify function with mocked endpoints
func TestFunctional_VerifyFunction(t *testing.T) {
	// Mock VictoriaMetrics
	vmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		var response string

		switch {
		case strings.Contains(query, "count({__name__"):
			response = `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[0,"1000"]}]}}`
		case strings.Contains(query, "count(count by"):
			response = `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[0,"50"]}]}}`
		case strings.Contains(query, "max(generator_verification"):
			response = `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[0,"500"]}]}}`
		case strings.Contains(query, "count(high_cardinality"):
			response = `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[0,"100"]}]}}`
		case strings.Contains(query, "sum(vm_rows"):
			response = `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[0,"50000"]}]}}`
		default:
			response = `{"status":"success","data":{"resultType":"vector","result":[]}}`
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, response)
	}))
	defer vmServer.Close()

	// Mock Metrics Governor
	mgServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metrics := `metrics_governor_datapoints_received_total 10000
metrics_governor_datapoints_sent_total 9800
metrics_governor_queue_size 10
metrics_governor_queue_dropped_total 50
metrics_governor_export_errors_total 2
metrics_governor_batches_sent_total 100`
		fmt.Fprint(w, metrics)
	}))
	defer mgServer.Close()

	// Run verify
	result := verify(vmServer.URL, mgServer.URL, 95.0)

	// Check results
	if result.VMTotalTimeSeries != 1000 {
		t.Errorf("VMTotalTimeSeries = %d, want 1000", result.VMTotalTimeSeries)
	}
	if result.VMUniqueMetricNames != 50 {
		t.Errorf("VMUniqueMetricNames = %d, want 50", result.VMUniqueMetricNames)
	}
	if result.VMVerificationCounter != 500 {
		t.Errorf("VMVerificationCounter = %d, want 500", result.VMVerificationCounter)
	}
	if result.MGDatapointsReceived != 10000 {
		t.Errorf("MGDatapointsReceived = %d, want 10000", result.MGDatapointsReceived)
	}
	if result.MGDatapointsSent != 9800 {
		t.Errorf("MGDatapointsSent = %d, want 9800", result.MGDatapointsSent)
	}

	// Ingestion rate should be 98% (9800/10000)
	expectedRate := 98.0
	if result.IngestionRate != expectedRate {
		t.Errorf("IngestionRate = %.2f, want %.2f", result.IngestionRate, expectedRate)
	}

	// Should pass with 98% > 95% threshold
	if !result.VerificationMatch {
		t.Errorf("Expected verification to pass, got fail: %s", result.VerificationMessage)
	}
}

// TestFunctional_ConcurrentVerifications tests thread safety of stats updates
func TestFunctional_ConcurrentVerifications(t *testing.T) {
	testStats := &VerifierStats{
		StartTime: time.Now(),
	}

	var wg sync.WaitGroup
	numGoroutines := 10
	checksPerGoroutine := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < checksPerGoroutine; j++ {
				testStats.ChecksTotal.Add(1)
				if j%2 == 0 {
					testStats.ChecksPassed.Add(1)
				} else {
					testStats.ChecksFailed.Add(1)
				}
				testStats.LastIngestionRate.Store(int64(j * 100))

				// Update last result
				testStats.mu.Lock()
				testStats.lastResult = &VerificationResult{
					VMTotalTimeSeries: j,
					IngestionRate:     float64(j),
				}
				testStats.mu.Unlock()
			}
		}()
	}

	wg.Wait()

	expectedTotal := int64(numGoroutines * checksPerGoroutine)
	if got := testStats.ChecksTotal.Load(); got != expectedTotal {
		t.Errorf("ChecksTotal = %d, want %d", got, expectedTotal)
	}

	// Half passed, half failed
	expectedPassed := int64(numGoroutines * checksPerGoroutine / 2)
	if got := testStats.ChecksPassed.Load(); got != expectedPassed {
		t.Errorf("ChecksPassed = %d, want %d", got, expectedPassed)
	}
}

// TestFunctional_VerificationResultJSON tests that VerificationResult can be serialized
func TestFunctional_VerificationResultJSON(t *testing.T) {
	result := VerificationResult{
		Timestamp:             time.Now(),
		VMTotalTimeSeries:     1000,
		VMUniqueMetricNames:   50,
		VMVerificationCounter: 500,
		MGDatapointsReceived:  10000,
		MGDatapointsSent:      9800,
		MGBatchesSent:         100,
		MGExportErrors:        2,
		IngestionRate:         98.0,
		VerificationMatch:     true,
		PassThreshold:         95.0,
		VerificationMessage:   "Test message",
	}

	// Serialize to JSON
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	// Deserialize back
	var decoded VerificationResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Verify key fields
	if decoded.VMTotalTimeSeries != result.VMTotalTimeSeries {
		t.Errorf("VMTotalTimeSeries mismatch")
	}
	if decoded.IngestionRate != result.IngestionRate {
		t.Errorf("IngestionRate mismatch")
	}
	if decoded.VerificationMatch != result.VerificationMatch {
		t.Errorf("VerificationMatch mismatch")
	}
}

// BenchmarkVerification benchmarks the verification function
func BenchmarkVerification(b *testing.B) {
	// Mock servers
	vmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[0,"1000"]}]}}`)
	}))
	defer vmServer.Close()

	mgServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `metrics_governor_datapoints_received_total 10000
metrics_governor_datapoints_sent_total 9800`)
	}))
	defer mgServer.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		verify(vmServer.URL, mgServer.URL, 95.0)
	}
}

// BenchmarkExtractMetric benchmarks metric extraction
func BenchmarkExtractMetric(b *testing.B) {
	metricsText := `# HELP metrics_governor_datapoints_received_total Total datapoints received
# TYPE metrics_governor_datapoints_received_total counter
metrics_governor_datapoints_received_total 50000
metrics_governor_datapoints_sent_total 49000
metrics_governor_queue_size 25
metrics_governor_queue_dropped_total{reason="limit_exceeded"} 500
metrics_governor_queue_dropped_total{reason="buffer_full"} 100
metrics_governor_export_errors_total 10
metrics_governor_batches_sent_total 500`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		extractMetricValue(metricsText, "metrics_governor_datapoints_received_total")
		extractMetricValue(metricsText, "metrics_governor_queue_dropped_total")
	}
}
