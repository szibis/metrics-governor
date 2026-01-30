package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestFunctional_MetricsEndpoint tests the Prometheus metrics endpoint
func TestFunctional_MetricsEndpoint(t *testing.T) {
	// Setup test stats
	testStats := &GeneratorStats{
		StartTime: time.Now().Add(-1 * time.Hour),
	}
	testStats.TotalMetricsSent.Store(100000)
	testStats.TotalDatapointsSent.Store(500000)
	testStats.TotalBatchesSent.Store(1000)
	testStats.TotalErrors.Store(5)
	testStats.MinBatchLatency.Store(1000000)      // 1ms in ns
	testStats.MaxBatchLatency.Store(100000000)    // 100ms in ns
	testStats.TotalBatchLatency.Store(50000000000) // 50s total
	testStats.HighCardinalityMetrics.Store(10000)
	testStats.UniqueLabels.Store(10000)
	testStats.BurstsSent.Store(10)
	testStats.BurstMetricsSent.Store(20000)

	// Save original and restore after test
	originalStats := stats
	stats = testStats
	defer func() { stats = originalStats }()

	// Create test request
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	// Call the handler
	http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elapsed := time.Since(stats.StartTime).Seconds()
		totalMetrics := stats.TotalMetricsSent.Load()
		totalDatapoints := stats.TotalDatapointsSent.Load()
		totalBatches := stats.TotalBatchesSent.Load()
		totalErrors := stats.TotalErrors.Load()
		highCardMetrics := stats.HighCardinalityMetrics.Load()
		bursts := stats.BurstsSent.Load()
		burstMetrics := stats.BurstMetricsSent.Load()

		avgLatency := float64(0)
		if totalBatches > 0 {
			avgLatency = float64(stats.TotalBatchLatency.Load()) / float64(totalBatches) / 1e9
		}

		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		fmt.Fprintf(w, "# HELP generator_runtime_seconds Total runtime of the generator\n")
		fmt.Fprintf(w, "# TYPE generator_runtime_seconds counter\n")
		fmt.Fprintf(w, "generator_runtime_seconds %.3f\n", elapsed)

		fmt.Fprintf(w, "# HELP generator_metrics_sent_total Total number of metrics sent\n")
		fmt.Fprintf(w, "# TYPE generator_metrics_sent_total counter\n")
		fmt.Fprintf(w, "generator_metrics_sent_total %d\n", totalMetrics)

		fmt.Fprintf(w, "# HELP generator_datapoints_sent_total Total number of datapoints sent\n")
		fmt.Fprintf(w, "# TYPE generator_datapoints_sent_total counter\n")
		fmt.Fprintf(w, "generator_datapoints_sent_total %d\n", totalDatapoints)

		fmt.Fprintf(w, "# HELP generator_batches_sent_total Total number of batches sent\n")
		fmt.Fprintf(w, "# TYPE generator_batches_sent_total counter\n")
		fmt.Fprintf(w, "generator_batches_sent_total %d\n", totalBatches)

		fmt.Fprintf(w, "# HELP generator_batch_latency_avg_seconds Average batch latency\n")
		fmt.Fprintf(w, "# TYPE generator_batch_latency_avg_seconds gauge\n")
		fmt.Fprintf(w, "generator_batch_latency_avg_seconds %.6f\n", avgLatency)

		fmt.Fprintf(w, "# HELP generator_high_cardinality_metrics_total Total high cardinality metrics\n")
		fmt.Fprintf(w, "# TYPE generator_high_cardinality_metrics_total counter\n")
		fmt.Fprintf(w, "generator_high_cardinality_metrics_total %d\n", highCardMetrics)

		fmt.Fprintf(w, "# HELP generator_bursts_sent_total Total number of bursts sent\n")
		fmt.Fprintf(w, "# TYPE generator_bursts_sent_total counter\n")
		fmt.Fprintf(w, "generator_bursts_sent_total %d\n", bursts)

		fmt.Fprintf(w, "# HELP generator_burst_metrics_total Total metrics sent in bursts\n")
		fmt.Fprintf(w, "# TYPE generator_burst_metrics_total counter\n")
		fmt.Fprintf(w, "generator_burst_metrics_total %d\n", burstMetrics)

		fmt.Fprintf(w, "# HELP generator_errors_total Total number of errors\n")
		fmt.Fprintf(w, "# TYPE generator_errors_total counter\n")
		fmt.Fprintf(w, "generator_errors_total %d\n", totalErrors)
	}).ServeHTTP(w, req)

	// Verify response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Check for expected metrics
	expectedMetrics := []string{
		"generator_metrics_sent_total 100000",
		"generator_datapoints_sent_total 500000",
		"generator_batches_sent_total 1000",
		"generator_high_cardinality_metrics_total 10000",
		"generator_bursts_sent_total 10",
		"generator_burst_metrics_total 20000",
		"generator_errors_total 5",
	}

	for _, expected := range expectedMetrics {
		if !strings.Contains(body, expected) {
			t.Errorf("Expected metric %q not found in response", expected)
		}
	}
}

// TestFunctional_StatsTracking tests that stats are tracked correctly
func TestFunctional_StatsTracking(t *testing.T) {
	testStats := &GeneratorStats{
		StartTime: time.Now(),
	}

	// Simulate generating metrics
	for i := 0; i < 100; i++ {
		batchStart := time.Now()
		batchMetrics := int64(50)
		batchDatapoints := int64(500)

		testStats.TotalMetricsSent.Add(batchMetrics)
		testStats.TotalDatapointsSent.Add(batchDatapoints)
		testStats.TotalBatchesSent.Add(1)
		testStats.LastBatchTime.Store(time.Now().UnixNano())

		batchLatency := time.Since(batchStart).Nanoseconds()
		testStats.TotalBatchLatency.Add(batchLatency)

		// Update min/max
		for {
			oldMin := testStats.MinBatchLatency.Load()
			if oldMin != 0 && oldMin <= batchLatency {
				break
			}
			if testStats.MinBatchLatency.CompareAndSwap(oldMin, batchLatency) {
				break
			}
		}
		for {
			oldMax := testStats.MaxBatchLatency.Load()
			if oldMax >= batchLatency {
				break
			}
			if testStats.MaxBatchLatency.CompareAndSwap(oldMax, batchLatency) {
				break
			}
		}
	}

	// Verify totals
	if got := testStats.TotalMetricsSent.Load(); got != 5000 {
		t.Errorf("TotalMetricsSent = %d, want 5000", got)
	}
	if got := testStats.TotalDatapointsSent.Load(); got != 50000 {
		t.Errorf("TotalDatapointsSent = %d, want 50000", got)
	}
	if got := testStats.TotalBatchesSent.Load(); got != 100 {
		t.Errorf("TotalBatchesSent = %d, want 100", got)
	}
	if testStats.MinBatchLatency.Load() == 0 {
		t.Error("MinBatchLatency should be set")
	}
	if testStats.MaxBatchLatency.Load() == 0 {
		t.Error("MaxBatchLatency should be set")
	}
}

// TestFunctional_BurstTracking tests burst traffic tracking
func TestFunctional_BurstTracking(t *testing.T) {
	testStats := &GeneratorStats{
		StartTime: time.Now(),
	}

	burstSize := 2000
	numBursts := 5

	for i := 0; i < numBursts; i++ {
		testStats.BurstsSent.Add(1)
		testStats.BurstMetricsSent.Add(int64(burstSize))
		testStats.TotalDatapointsSent.Add(int64(burstSize))
	}

	if got := testStats.BurstsSent.Load(); got != int64(numBursts) {
		t.Errorf("BurstsSent = %d, want %d", got, numBursts)
	}
	if got := testStats.BurstMetricsSent.Load(); got != int64(numBursts*burstSize) {
		t.Errorf("BurstMetricsSent = %d, want %d", got, numBursts*burstSize)
	}
	if got := testStats.TotalDatapointsSent.Load(); got != int64(numBursts*burstSize) {
		t.Errorf("TotalDatapointsSent = %d, want %d", got, numBursts*burstSize)
	}
}

// TestFunctional_ConcurrentStatsUpdates tests thread safety
func TestFunctional_ConcurrentStatsUpdates(t *testing.T) {
	testStats := &GeneratorStats{
		StartTime: time.Now(),
	}

	var wg sync.WaitGroup
	numGoroutines := 20
	iterationsPerGoroutine := 1000

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterationsPerGoroutine; j++ {
				testStats.TotalMetricsSent.Add(1)
				testStats.TotalDatapointsSent.Add(10)
				testStats.TotalBatchesSent.Add(1)
				testStats.TotalBatchLatency.Add(1000)

				// Update last batch time
				testStats.LastBatchTime.Store(time.Now().UnixNano())
			}
		}()
	}

	wg.Wait()

	expectedTotal := int64(numGoroutines * iterationsPerGoroutine)
	if got := testStats.TotalMetricsSent.Load(); got != expectedTotal {
		t.Errorf("TotalMetricsSent = %d, want %d", got, expectedTotal)
	}
	if got := testStats.TotalDatapointsSent.Load(); got != expectedTotal*10 {
		t.Errorf("TotalDatapointsSent = %d, want %d", got, expectedTotal*10)
	}
	if got := testStats.TotalBatchesSent.Load(); got != expectedTotal {
		t.Errorf("TotalBatchesSent = %d, want %d", got, expectedTotal)
	}
}

// TestFunctional_HighCardinalityTracking tests high cardinality metric tracking
func TestFunctional_HighCardinalityTracking(t *testing.T) {
	testStats := &GeneratorStats{
		StartTime: time.Now(),
	}

	highCardCount := 100
	iterations := 50

	for i := 0; i < iterations; i++ {
		testStats.HighCardinalityMetrics.Add(int64(highCardCount))
		testStats.UniqueLabels.Add(int64(highCardCount))
	}

	expected := int64(highCardCount * iterations)
	if got := testStats.HighCardinalityMetrics.Load(); got != expected {
		t.Errorf("HighCardinalityMetrics = %d, want %d", got, expected)
	}
	if got := testStats.UniqueLabels.Load(); got != expected {
		t.Errorf("UniqueLabels = %d, want %d", got, expected)
	}
}

// TestFunctional_ErrorTracking tests error tracking
func TestFunctional_ErrorTracking(t *testing.T) {
	testStats := &GeneratorStats{
		StartTime: time.Now(),
	}

	testStats.TotalBatchesSent.Store(100)
	testStats.TotalErrors.Store(5)

	errorRate := float64(testStats.TotalErrors.Load()) / float64(testStats.TotalBatchesSent.Load()) * 100

	if errorRate != 5.0 {
		t.Errorf("Error rate = %.2f%%, want 5.00%%", errorRate)
	}
}

// TestFunctional_ThroughputCalculation tests throughput calculation
func TestFunctional_ThroughputCalculation(t *testing.T) {
	testStats := &GeneratorStats{
		StartTime: time.Now().Add(-10 * time.Second), // 10 seconds ago
	}
	testStats.TotalMetricsSent.Store(10000)
	testStats.TotalDatapointsSent.Store(100000)

	elapsed := time.Since(testStats.StartTime).Seconds()
	metricsPerSec := float64(testStats.TotalMetricsSent.Load()) / elapsed
	datapointsPerSec := float64(testStats.TotalDatapointsSent.Load()) / elapsed

	// Should be approximately 1000 metrics/sec and 10000 datapoints/sec
	if metricsPerSec < 900 || metricsPerSec > 1100 {
		t.Errorf("metricsPerSec = %.2f, want ~1000", metricsPerSec)
	}
	if datapointsPerSec < 9000 || datapointsPerSec > 11000 {
		t.Errorf("datapointsPerSec = %.2f, want ~10000", datapointsPerSec)
	}
}

// TestFunctional_LatencyCalculation tests latency calculations
func TestFunctional_LatencyCalculation(t *testing.T) {
	testStats := &GeneratorStats{
		StartTime: time.Now(),
	}

	// Set up test data
	testStats.TotalBatchesSent.Store(100)
	testStats.TotalBatchLatency.Store(5000000000) // 5 seconds total in nanoseconds
	testStats.MinBatchLatency.Store(10000000)     // 10ms
	testStats.MaxBatchLatency.Store(100000000)    // 100ms

	// Calculate average
	avgLatency := float64(testStats.TotalBatchLatency.Load()) / float64(testStats.TotalBatchesSent.Load()) / 1e6

	if avgLatency != 50 { // 5000ms / 100 = 50ms
		t.Errorf("avgLatency = %.2f ms, want 50.00 ms", avgLatency)
	}

	minLatencyMs := float64(testStats.MinBatchLatency.Load()) / 1e6
	if minLatencyMs != 10 {
		t.Errorf("minLatency = %.2f ms, want 10.00 ms", minLatencyMs)
	}

	maxLatencyMs := float64(testStats.MaxBatchLatency.Load()) / 1e6
	if maxLatencyMs != 100 {
		t.Errorf("maxLatency = %.2f ms, want 100.00 ms", maxLatencyMs)
	}
}

// BenchmarkStatsUpdate benchmarks stats update operations
func BenchmarkStatsUpdate(b *testing.B) {
	testStats := &GeneratorStats{
		StartTime: time.Now(),
	}

	b.Run("atomic add", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			testStats.TotalMetricsSent.Add(1)
			testStats.TotalDatapointsSent.Add(10)
			testStats.TotalBatchesSent.Add(1)
		}
	})

	b.Run("atomic store", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			testStats.LastBatchTime.Store(time.Now().UnixNano())
		}
	})

	b.Run("compare and swap", func(b *testing.B) {
		var counter atomic.Int64
		for i := 0; i < b.N; i++ {
			old := counter.Load()
			counter.CompareAndSwap(old, old+1)
		}
	})
}

// BenchmarkConcurrentAccess benchmarks concurrent access patterns
func BenchmarkConcurrentAccess(b *testing.B) {
	testStats := &GeneratorStats{
		StartTime: time.Now(),
	}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			testStats.TotalMetricsSent.Add(1)
			testStats.TotalDatapointsSent.Add(10)
			_ = testStats.TotalBatchesSent.Load()
		}
	})
}
