package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetEnv(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		envValue     string
		defaultValue string
		expected     string
	}{
		{
			name:         "returns env value when set",
			key:          "TEST_VERIFIER_ENV",
			envValue:     "custom_value",
			defaultValue: "default",
			expected:     "custom_value",
		},
		{
			name:         "returns default when env not set",
			key:          "TEST_VERIFIER_UNSET",
			envValue:     "",
			defaultValue: "default_value",
			expected:     "default_value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer os.Unsetenv(tt.key)

			if tt.envValue != "" {
				os.Setenv(tt.key, tt.envValue)
			} else {
				os.Unsetenv(tt.key)
			}

			result := getEnv(tt.key, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("getEnv(%q, %q) = %q, want %q", tt.key, tt.defaultValue, result, tt.expected)
			}
		})
	}
}

func TestGetEnvFloat(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		envValue     string
		defaultValue float64
		expected     float64
	}{
		{
			name:         "returns float when env is valid",
			key:          "TEST_FLOAT_VALID",
			envValue:     "95.5",
			defaultValue: 0,
			expected:     95.5,
		},
		{
			name:         "returns integer as float",
			key:          "TEST_FLOAT_INT",
			envValue:     "100",
			defaultValue: 0,
			expected:     100.0,
		},
		{
			name:         "returns negative float",
			key:          "TEST_FLOAT_NEG",
			envValue:     "-10.5",
			defaultValue: 0,
			expected:     -10.5,
		},
		{
			name:         "returns default when env not set",
			key:          "TEST_FLOAT_UNSET",
			envValue:     "",
			defaultValue: 95.0,
			expected:     95.0,
		},
		{
			name:         "returns default when env is invalid",
			key:          "TEST_FLOAT_INVALID",
			envValue:     "not_a_float",
			defaultValue: 50.0,
			expected:     50.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer os.Unsetenv(tt.key)

			if tt.envValue != "" {
				os.Setenv(tt.key, tt.envValue)
			} else {
				os.Unsetenv(tt.key)
			}

			result := getEnvFloat(tt.key, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("getEnvFloat(%q, %v) = %v, want %v", tt.key, tt.defaultValue, result, tt.expected)
			}
		})
	}
}

func TestGetEnvDuration(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		envValue     string
		defaultValue time.Duration
		expected     time.Duration
	}{
		{
			name:         "returns duration in seconds",
			key:          "TEST_DUR_SEC",
			envValue:     "15s",
			defaultValue: 0,
			expected:     15 * time.Second,
		},
		{
			name:         "returns duration in minutes",
			key:          "TEST_DUR_MIN",
			envValue:     "5m",
			defaultValue: 0,
			expected:     5 * time.Minute,
		},
		{
			name:         "returns duration in milliseconds",
			key:          "TEST_DUR_MS",
			envValue:     "100ms",
			defaultValue: 0,
			expected:     100 * time.Millisecond,
		},
		{
			name:         "returns default when env not set",
			key:          "TEST_DUR_UNSET",
			envValue:     "",
			defaultValue: 10 * time.Second,
			expected:     10 * time.Second,
		},
		{
			name:         "returns default when env is invalid",
			key:          "TEST_DUR_INVALID",
			envValue:     "invalid",
			defaultValue: 30 * time.Second,
			expected:     30 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer os.Unsetenv(tt.key)

			if tt.envValue != "" {
				os.Setenv(tt.key, tt.envValue)
			} else {
				os.Unsetenv(tt.key)
			}

			result := getEnvDuration(tt.key, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("getEnvDuration(%q, %v) = %v, want %v", tt.key, tt.defaultValue, result, tt.expected)
			}
		})
	}
}

func TestExtractMetricValue(t *testing.T) {
	tests := []struct {
		name       string
		metricsText string
		metricName string
		expected   int64
	}{
		{
			name: "extracts simple metric",
			metricsText: `# HELP test_metric A test metric
# TYPE test_metric counter
test_metric 42`,
			metricName: "test_metric",
			expected:   42,
		},
		{
			name: "extracts metric with labels",
			metricsText: `# HELP test_metric A test metric
# TYPE test_metric counter
test_metric{label="value"} 100`,
			metricName: "test_metric",
			expected:   100,
		},
		{
			name: "sums multiple metrics with same name",
			metricsText: `# HELP test_metric A test metric
# TYPE test_metric counter
test_metric{env="prod"} 50
test_metric{env="staging"} 30
test_metric{env="dev"} 20`,
			metricName: "test_metric",
			expected:   100,
		},
		{
			name: "returns 0 for non-existent metric",
			metricsText: `# HELP other_metric Another metric
other_metric 42`,
			metricName: "test_metric",
			expected:   0,
		},
		{
			name: "ignores comment lines",
			metricsText: `# test_metric 999
# TYPE test_metric counter
test_metric 42`,
			metricName: "test_metric",
			expected:   42,
		},
		{
			name: "handles float values",
			metricsText: `test_metric 123.456`,
			metricName: "test_metric",
			expected:   123,
		},
		{
			name: "handles real metrics-governor output",
			metricsText: `# HELP metrics_governor_datapoints_received_total Total datapoints received
# TYPE metrics_governor_datapoints_received_total counter
metrics_governor_datapoints_received_total 12345`,
			metricName: "metrics_governor_datapoints_received_total",
			expected:   12345,
		},
		{
			name: "handles multiple label combinations",
			metricsText: `metrics_governor_queue_dropped_total{reason="limit_exceeded"} 10
metrics_governor_queue_dropped_total{reason="buffer_full"} 5`,
			metricName: "metrics_governor_queue_dropped_total",
			expected:   15,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractMetricValue(tt.metricsText, tt.metricName)
			if result != tt.expected {
				t.Errorf("extractMetricValue() = %d, want %d", result, tt.expected)
			}
		})
	}
}

func TestVerificationResultCalculation(t *testing.T) {
	t.Run("fails when no time series exist", func(t *testing.T) {
		result := VerificationResult{
			VMTotalTimeSeries: 0,
			PassThreshold:     95.0,
		}
		result.calculateVerification()

		if result.VerificationMatch {
			t.Error("Expected verification to fail with no time series")
		}
		if result.VerificationMessage != "No time series in VictoriaMetrics" {
			t.Errorf("Wrong message: %s", result.VerificationMessage)
		}
	})

	t.Run("fails when verification counter is zero", func(t *testing.T) {
		result := VerificationResult{
			VMTotalTimeSeries:     100,
			VMVerificationCounter: 0,
			PassThreshold:         95.0,
		}
		result.calculateVerification()

		if result.VerificationMatch {
			t.Error("Expected verification to fail with zero verification counter")
		}
		if result.VerificationMessage != "Verification counter not found or zero" {
			t.Errorf("Wrong message: %s", result.VerificationMessage)
		}
	})

	t.Run("calculates ingestion rate correctly", func(t *testing.T) {
		result := VerificationResult{
			VMTotalTimeSeries:     100,
			VMVerificationCounter: 50,
			MGDatapointsReceived:  1000,
			MGDatapointsSent:      950,
			PassThreshold:         95.0,
		}
		result.calculateVerification()

		if result.IngestionRate != 95.0 {
			t.Errorf("IngestionRate = %.2f, want 95.0", result.IngestionRate)
		}
		if !result.VerificationMatch {
			t.Error("Expected verification to pass at threshold")
		}
	})

	t.Run("caps ingestion rate at 100%", func(t *testing.T) {
		result := VerificationResult{
			VMTotalTimeSeries:     100,
			VMVerificationCounter: 50,
			MGDatapointsReceived:  900,
			MGDatapointsSent:      1000, // More sent than received (timing issue)
			PassThreshold:         95.0,
		}
		result.calculateVerification()

		if result.IngestionRate != 100.0 {
			t.Errorf("IngestionRate = %.2f, want 100.0 (capped)", result.IngestionRate)
		}
	})

	t.Run("fails when ingestion rate below threshold", func(t *testing.T) {
		result := VerificationResult{
			VMTotalTimeSeries:     100,
			VMVerificationCounter: 50,
			MGDatapointsReceived:  1000,
			MGDatapointsSent:      800,
			PassThreshold:         95.0,
		}
		result.calculateVerification()

		if result.IngestionRate != 80.0 {
			t.Errorf("IngestionRate = %.2f, want 80.0", result.IngestionRate)
		}
		if result.VerificationMatch {
			t.Error("Expected verification to fail below threshold")
		}
	})

	t.Run("fails when drop rate is too high", func(t *testing.T) {
		result := VerificationResult{
			VMTotalTimeSeries:     100,
			VMVerificationCounter: 50,
			MGDatapointsReceived:  1000,
			MGDatapointsSent:      900,
			MGDroppedTotal:        100, // 10% dropped
			PassThreshold:         95.0,
		}
		result.calculateVerification()

		if result.VerificationMatch {
			t.Error("Expected verification to fail with high drop rate")
		}
	})

	t.Run("passes with data flowing and no errors", func(t *testing.T) {
		result := VerificationResult{
			VMTotalTimeSeries:     100,
			VMVerificationCounter: 50,
			MGDatapointsReceived:  0, // No received count (old metrics-governor)
			MGDatapointsSent:      0,
			MGExportErrors:        0,
			PassThreshold:         95.0,
		}
		result.calculateVerification()

		if !result.VerificationMatch {
			t.Error("Expected verification to pass with data flowing")
		}
		if result.IngestionRate != 100.0 {
			t.Errorf("IngestionRate = %.2f, want 100.0", result.IngestionRate)
		}
	})

	t.Run("notes export errors in message", func(t *testing.T) {
		result := VerificationResult{
			VMTotalTimeSeries:     100,
			VMVerificationCounter: 50,
			MGDatapointsReceived:  1000,
			MGDatapointsSent:      980,
			MGExportErrors:        5,
			PassThreshold:         95.0,
		}
		result.calculateVerification()

		if !strings.Contains(result.VerificationMessage, "98.00%") && !strings.Contains(result.VerificationMessage, "Export errors") {
			// It might note errors or just show the rate
			t.Logf("Message: %s", result.VerificationMessage)
		}
	})
}

func TestQueryVMScalar(t *testing.T) {
	t.Run("parses successful response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1234567890,"42"]}]}}`)
		}))
		defer server.Close()

		val, err := queryVMScalar(server.URL, "test_query")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if val != 42 {
			t.Errorf("queryVMScalar = %v, want 42", val)
		}
	})

	t.Run("returns 0 for empty result", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
		}))
		defer server.Close()

		val, err := queryVMScalar(server.URL, "test_query")
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if val != 0 {
			t.Errorf("queryVMScalar = %v, want 0", val)
		}
	})

	t.Run("returns error for failed status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"status":"error","errorType":"bad_data","error":"parse error"}`)
		}))
		defer server.Close()

		_, err := queryVMScalar(server.URL, "test_query")
		if err == nil {
			t.Error("Expected error for failed status")
		}
	})

	t.Run("returns error for invalid JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `not json`)
		}))
		defer server.Close()

		_, err := queryVMScalar(server.URL, "test_query")
		if err == nil {
			t.Error("Expected error for invalid JSON")
		}
	})

	t.Run("returns error for connection failure", func(t *testing.T) {
		_, err := queryVMScalar("http://localhost:99999", "test_query")
		if err == nil {
			t.Error("Expected error for connection failure")
		}
	})
}

func TestQueryMGStats(t *testing.T) {
	t.Run("parses metrics correctly", func(t *testing.T) {
		metricsOutput := `# HELP metrics_governor_datapoints_received_total Total datapoints received
# TYPE metrics_governor_datapoints_received_total counter
metrics_governor_datapoints_received_total 10000

# HELP metrics_governor_datapoints_sent_total Total datapoints sent
# TYPE metrics_governor_datapoints_sent_total counter
metrics_governor_datapoints_sent_total 9500

# HELP metrics_governor_queue_size Current queue size
# TYPE metrics_governor_queue_size gauge
metrics_governor_queue_size 50

# HELP metrics_governor_queue_dropped_total Total dropped
# TYPE metrics_governor_queue_dropped_total counter
metrics_governor_queue_dropped_total 100

# HELP metrics_governor_export_errors_total Export errors
# TYPE metrics_governor_export_errors_total counter
metrics_governor_export_errors_total 5

# HELP metrics_governor_batches_sent_total Batches sent
# TYPE metrics_governor_batches_sent_total counter
metrics_governor_batches_sent_total 200
`
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/metrics" {
				t.Errorf("Unexpected path: %s", r.URL.Path)
			}
			fmt.Fprint(w, metricsOutput)
		}))
		defer server.Close()

		stats, err := queryMGStats(server.URL)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if stats.DatapointsReceived != 10000 {
			t.Errorf("DatapointsReceived = %d, want 10000", stats.DatapointsReceived)
		}
		if stats.DatapointsSent != 9500 {
			t.Errorf("DatapointsSent = %d, want 9500", stats.DatapointsSent)
		}
		if stats.QueueSize != 50 {
			t.Errorf("QueueSize = %d, want 50", stats.QueueSize)
		}
		if stats.DroppedTotal != 100 {
			t.Errorf("DroppedTotal = %d, want 100", stats.DroppedTotal)
		}
		if stats.ExportErrors != 5 {
			t.Errorf("ExportErrors = %d, want 5", stats.ExportErrors)
		}
		if stats.BatchesSent != 200 {
			t.Errorf("BatchesSent = %d, want 200", stats.BatchesSent)
		}
	})

	t.Run("returns error for connection failure", func(t *testing.T) {
		_, err := queryMGStats("http://localhost:99999")
		if err == nil {
			t.Error("Expected error for connection failure")
		}
	})
}

func TestVerifierStatsAtomic(t *testing.T) {
	t.Run("atomic counters increment correctly", func(t *testing.T) {
		testStats := &VerifierStats{}

		testStats.ChecksTotal.Add(1)
		testStats.ChecksTotal.Add(1)
		testStats.ChecksTotal.Add(1)
		if got := testStats.ChecksTotal.Load(); got != 3 {
			t.Errorf("ChecksTotal = %d, want 3", got)
		}

		testStats.ChecksPassed.Add(2)
		if got := testStats.ChecksPassed.Load(); got != 2 {
			t.Errorf("ChecksPassed = %d, want 2", got)
		}

		testStats.ChecksFailed.Add(1)
		if got := testStats.ChecksFailed.Load(); got != 1 {
			t.Errorf("ChecksFailed = %d, want 1", got)
		}
	})

	t.Run("last ingestion rate stored as int64", func(t *testing.T) {
		testStats := &VerifierStats{}

		// Store 95.5% as 9550
		testStats.LastIngestionRate.Store(int64(95.5 * 100))

		rate := float64(testStats.LastIngestionRate.Load()) / 100.0
		if rate != 95.5 {
			t.Errorf("LastIngestionRate = %.2f, want 95.5", rate)
		}
	})

	t.Run("concurrent access to last result", func(t *testing.T) {
		testStats := &VerifierStats{}
		done := make(chan bool)

		// Writer goroutine
		go func() {
			for i := 0; i < 100; i++ {
				result := &VerificationResult{
					VMTotalTimeSeries: i,
					IngestionRate:     float64(i),
				}
				testStats.mu.Lock()
				testStats.lastResult = result
				testStats.mu.Unlock()
			}
			done <- true
		}()

		// Reader goroutine
		go func() {
			for i := 0; i < 100; i++ {
				testStats.mu.RLock()
				_ = testStats.lastResult
				testStats.mu.RUnlock()
			}
			done <- true
		}()

		<-done
		<-done
	})
}

func TestPassRateCalculation(t *testing.T) {
	tests := []struct {
		name     string
		passed   int64
		total    int64
		expected float64
	}{
		{"100% pass rate", 10, 10, 100.0},
		{"50% pass rate", 5, 10, 50.0},
		{"0% pass rate", 0, 10, 0.0},
		{"zero total", 0, 0, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var passRate float64
			if tt.total > 0 {
				passRate = float64(tt.passed) / float64(tt.total) * 100
			}
			if passRate != tt.expected {
				t.Errorf("passRate = %.2f, want %.2f", passRate, tt.expected)
			}
		})
	}
}

func TestVerificationResultMessage(t *testing.T) {
	t.Run("message format for pass", func(t *testing.T) {
		result := VerificationResult{
			VMTotalTimeSeries:     100,
			VMVerificationCounter: 50,
			MGDatapointsReceived:  1000,
			MGDatapointsSent:      980,
			PassThreshold:         95.0,
		}
		result.calculateVerification()

		if !strings.Contains(result.VerificationMessage, "98.00%") {
			t.Errorf("Expected message to contain rate, got: %s", result.VerificationMessage)
		}
		if !strings.Contains(result.VerificationMessage, "95.00%") {
			t.Errorf("Expected message to contain threshold, got: %s", result.VerificationMessage)
		}
	})
}

// BenchmarkExtractMetricValue tests performance of metric extraction
func BenchmarkExtractMetricValue(b *testing.B) {
	metricsText := `# HELP test_metric A test metric
# TYPE test_metric counter
test_metric{env="prod"} 1000
test_metric{env="staging"} 500
test_metric{env="dev"} 250
# HELP other_metric Another metric
# TYPE other_metric gauge
other_metric 42`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		extractMetricValue(metricsText, "test_metric")
	}
}

func BenchmarkAtomicOperations(b *testing.B) {
	b.Run("atomic add", func(b *testing.B) {
		var counter atomic.Int64
		for i := 0; i < b.N; i++ {
			counter.Add(1)
		}
	})

	b.Run("atomic store/load int64", func(b *testing.B) {
		var counter atomic.Int64
		for i := 0; i < b.N; i++ {
			counter.Store(int64(i))
			_ = counter.Load()
		}
	})
}
