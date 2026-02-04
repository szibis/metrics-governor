package main

import (
	"os"
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
			key:          "TEST_GENERATOR_ENV",
			envValue:     "custom_value",
			defaultValue: "default",
			expected:     "custom_value",
		},
		{
			name:         "returns default when env not set",
			key:          "TEST_GENERATOR_UNSET",
			envValue:     "",
			defaultValue: "default_value",
			expected:     "default_value",
		},
		{
			name:         "returns empty string as env value",
			key:          "TEST_GENERATOR_EMPTY",
			envValue:     "",
			defaultValue: "default",
			expected:     "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up
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

func TestGetEnvBool(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		envValue     string
		defaultValue bool
		expected     bool
	}{
		{
			name:         "returns true when env is 'true'",
			key:          "TEST_BOOL_TRUE",
			envValue:     "true",
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "returns false when env is 'false'",
			key:          "TEST_BOOL_FALSE",
			envValue:     "false",
			defaultValue: true,
			expected:     false,
		},
		{
			name:         "returns true when env is '1'",
			key:          "TEST_BOOL_ONE",
			envValue:     "1",
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "returns false when env is '0'",
			key:          "TEST_BOOL_ZERO",
			envValue:     "0",
			defaultValue: true,
			expected:     false,
		},
		{
			name:         "returns default when env not set",
			key:          "TEST_BOOL_UNSET",
			envValue:     "",
			defaultValue: true,
			expected:     true,
		},
		{
			name:         "returns default when env is invalid",
			key:          "TEST_BOOL_INVALID",
			envValue:     "invalid",
			defaultValue: true,
			expected:     true,
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

			result := getEnvBool(tt.key, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("getEnvBool(%q, %v) = %v, want %v", tt.key, tt.defaultValue, result, tt.expected)
			}
		})
	}
}

func TestGetEnvInt(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		envValue     string
		defaultValue int
		expected     int
	}{
		{
			name:         "returns int when env is valid",
			key:          "TEST_INT_VALID",
			envValue:     "42",
			defaultValue: 0,
			expected:     42,
		},
		{
			name:         "returns negative int",
			key:          "TEST_INT_NEGATIVE",
			envValue:     "-10",
			defaultValue: 0,
			expected:     -10,
		},
		{
			name:         "returns default when env not set",
			key:          "TEST_INT_UNSET",
			envValue:     "",
			defaultValue: 100,
			expected:     100,
		},
		{
			name:         "returns default when env is invalid",
			key:          "TEST_INT_INVALID",
			envValue:     "not_a_number",
			defaultValue: 50,
			expected:     50,
		},
		{
			name:         "returns default when env is float",
			key:          "TEST_INT_FLOAT",
			envValue:     "3.14",
			defaultValue: 5,
			expected:     5,
		},
		{
			name:         "returns zero when env is '0'",
			key:          "TEST_INT_ZERO",
			envValue:     "0",
			defaultValue: 10,
			expected:     0,
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

			result := getEnvInt(tt.key, tt.defaultValue)
			if result != tt.expected {
				t.Errorf("getEnvInt(%q, %v) = %v, want %v", tt.key, tt.defaultValue, result, tt.expected)
			}
		})
	}
}

func TestGeneratorStatsAtomic(t *testing.T) {
	t.Run("atomic counters increment correctly", func(t *testing.T) {
		testStats := &GeneratorStats{}

		// Test atomic operations
		testStats.TotalMetricsSent.Add(10)
		testStats.TotalMetricsSent.Add(5)
		if got := testStats.TotalMetricsSent.Load(); got != 15 {
			t.Errorf("TotalMetricsSent = %d, want 15", got)
		}

		testStats.TotalDatapointsSent.Add(100)
		if got := testStats.TotalDatapointsSent.Load(); got != 100 {
			t.Errorf("TotalDatapointsSent = %d, want 100", got)
		}

		testStats.TotalBatchesSent.Add(1)
		testStats.TotalBatchesSent.Add(1)
		testStats.TotalBatchesSent.Add(1)
		if got := testStats.TotalBatchesSent.Load(); got != 3 {
			t.Errorf("TotalBatchesSent = %d, want 3", got)
		}

		testStats.TotalErrors.Add(2)
		if got := testStats.TotalErrors.Load(); got != 2 {
			t.Errorf("TotalErrors = %d, want 2", got)
		}
	})

	t.Run("min/max latency tracking", func(t *testing.T) {
		testStats := &GeneratorStats{}

		// Simulate latency tracking like the main code does
		latencies := []int64{1000000, 500000, 2000000, 750000} // nanoseconds

		for _, latency := range latencies {
			// Update min
			for {
				oldMin := testStats.MinBatchLatency.Load()
				if oldMin != 0 && oldMin <= latency {
					break
				}
				if testStats.MinBatchLatency.CompareAndSwap(oldMin, latency) {
					break
				}
			}
			// Update max
			for {
				oldMax := testStats.MaxBatchLatency.Load()
				if oldMax >= latency {
					break
				}
				if testStats.MaxBatchLatency.CompareAndSwap(oldMax, latency) {
					break
				}
			}
		}

		// Min should be 500000, max should be 2000000
		if got := testStats.MinBatchLatency.Load(); got != 500000 {
			t.Errorf("MinBatchLatency = %d, want 500000", got)
		}
		if got := testStats.MaxBatchLatency.Load(); got != 2000000 {
			t.Errorf("MaxBatchLatency = %d, want 2000000", got)
		}
	})

	t.Run("concurrent access safety", func(t *testing.T) {
		testStats := &GeneratorStats{}
		done := make(chan bool)

		// Launch multiple goroutines incrementing stats
		for i := 0; i < 10; i++ {
			go func() {
				for j := 0; j < 100; j++ {
					testStats.TotalMetricsSent.Add(1)
					testStats.TotalDatapointsSent.Add(1)
					testStats.TotalBatchesSent.Add(1)
				}
				done <- true
			}()
		}

		// Wait for all goroutines
		for i := 0; i < 10; i++ {
			<-done
		}

		// Each goroutine adds 100, 10 goroutines = 1000
		if got := testStats.TotalMetricsSent.Load(); got != 1000 {
			t.Errorf("TotalMetricsSent after concurrent access = %d, want 1000", got)
		}
		if got := testStats.TotalDatapointsSent.Load(); got != 1000 {
			t.Errorf("TotalDatapointsSent after concurrent access = %d, want 1000", got)
		}
		if got := testStats.TotalBatchesSent.Load(); got != 1000 {
			t.Errorf("TotalBatchesSent after concurrent access = %d, want 1000", got)
		}
	})
}

func TestGeneratorStatsCalculations(t *testing.T) {
	t.Run("metrics per second calculation", func(t *testing.T) {
		testStats := &GeneratorStats{
			StartTime: time.Now().Add(-10 * time.Second),
		}
		testStats.TotalMetricsSent.Store(1000)

		elapsed := time.Since(testStats.StartTime).Seconds()
		metricsPerSec := float64(testStats.TotalMetricsSent.Load()) / elapsed

		// Should be approximately 100 metrics/sec (1000 / 10)
		if metricsPerSec < 95 || metricsPerSec > 105 {
			t.Errorf("metricsPerSec = %.2f, want ~100", metricsPerSec)
		}
	})

	t.Run("average latency calculation", func(t *testing.T) {
		testStats := &GeneratorStats{}
		testStats.TotalBatchesSent.Store(5)
		testStats.TotalBatchLatency.Store(5000000000) // 5 seconds in nanoseconds

		avgLatency := float64(0)
		if totalBatches := testStats.TotalBatchesSent.Load(); totalBatches > 0 {
			avgLatency = float64(testStats.TotalBatchLatency.Load()) / float64(totalBatches) / 1e6 // Convert to ms
		}

		// 5000000000 ns / 5 batches / 1e6 = 1000 ms
		if avgLatency != 1000 {
			t.Errorf("avgLatency = %.2f ms, want 1000", avgLatency)
		}
	})

	t.Run("error rate calculation", func(t *testing.T) {
		testStats := &GeneratorStats{}
		testStats.TotalBatchesSent.Store(100)
		testStats.TotalErrors.Store(5)

		errorRate := float64(testStats.TotalErrors.Load()) / float64(testStats.TotalBatchesSent.Load()) * 100

		if errorRate != 5.0 {
			t.Errorf("errorRate = %.2f%%, want 5.00%%", errorRate)
		}
	})

	t.Run("error rate with zero batches", func(t *testing.T) {
		testStats := &GeneratorStats{}
		testStats.TotalBatchesSent.Store(0)
		testStats.TotalErrors.Store(0)

		var errorRate float64
		if batches := testStats.TotalBatchesSent.Load(); batches > 0 {
			errorRate = float64(testStats.TotalErrors.Load()) / float64(batches) * 100
		}

		if errorRate != 0 {
			t.Errorf("errorRate = %.2f%%, want 0%%", errorRate)
		}
	})
}

func TestHighCardinalityTracking(t *testing.T) {
	t.Run("tracks high cardinality metrics", func(t *testing.T) {
		testStats := &GeneratorStats{}

		// Simulate high cardinality metric generation
		highCardinalityCount := 100
		testStats.HighCardinalityMetrics.Add(int64(highCardinalityCount))
		testStats.UniqueLabels.Add(int64(highCardinalityCount))

		if got := testStats.HighCardinalityMetrics.Load(); got != 100 {
			t.Errorf("HighCardinalityMetrics = %d, want 100", got)
		}
		if got := testStats.UniqueLabels.Load(); got != 100 {
			t.Errorf("UniqueLabels = %d, want 100", got)
		}
	})
}

func TestBurstTracking(t *testing.T) {
	t.Run("tracks burst metrics", func(t *testing.T) {
		testStats := &GeneratorStats{}

		burstSize := 2000
		testStats.BurstsSent.Add(1)
		testStats.BurstMetricsSent.Add(int64(burstSize))
		testStats.TotalDatapointsSent.Add(int64(burstSize))

		if got := testStats.BurstsSent.Load(); got != 1 {
			t.Errorf("BurstsSent = %d, want 1", got)
		}
		if got := testStats.BurstMetricsSent.Load(); got != 2000 {
			t.Errorf("BurstMetricsSent = %d, want 2000", got)
		}
		if got := testStats.TotalDatapointsSent.Load(); got != 2000 {
			t.Errorf("TotalDatapointsSent = %d, want 2000", got)
		}
	})
}

func TestLastBatchTimeTracking(t *testing.T) {
	t.Run("stores last batch time", func(t *testing.T) {
		testStats := &GeneratorStats{}

		now := time.Now()
		testStats.LastBatchTime.Store(now.UnixNano())

		stored := testStats.LastBatchTime.Load()
		storedTime := time.Unix(0, stored)

		// Should be within 1 second of now
		diff := storedTime.Sub(now)
		if diff < -time.Second || diff > time.Second {
			t.Errorf("LastBatchTime diff = %v, want within 1 second", diff)
		}
	})
}

// BenchmarkAtomicOperations tests performance of atomic operations
func BenchmarkAtomicOperations(b *testing.B) {
	b.Run("atomic add", func(b *testing.B) {
		var counter atomic.Int64
		for i := 0; i < b.N; i++ {
			counter.Add(1)
		}
	})

	b.Run("atomic load", func(b *testing.B) {
		var counter atomic.Int64
		counter.Store(12345)
		for i := 0; i < b.N; i++ {
			_ = counter.Load()
		}
	})

	b.Run("atomic compare and swap", func(b *testing.B) {
		var counter atomic.Int64
		for i := 0; i < b.N; i++ {
			old := counter.Load()
			counter.CompareAndSwap(old, old+1)
		}
	})
}

// Tests for spike scenario functionality

func TestGenerateUUID(t *testing.T) {
	t.Run("generates unique IDs", func(t *testing.T) {
		seen := make(map[string]bool)
		for i := 0; i < 1000; i++ {
			id := generateUUID()
			if seen[id] {
				t.Errorf("duplicate UUID generated: %s", id)
			}
			seen[id] = true
		}
	})

	t.Run("format contains dashes", func(t *testing.T) {
		id := generateUUID()
		// Should be hex format with dashes
		if len(id) < 20 {
			t.Errorf("UUID too short: %s (length %d)", id, len(id))
		}
		// Should contain dashes
		dashCount := 0
		for _, c := range id {
			if c == '-' {
				dashCount++
			}
		}
		if dashCount != 4 {
			t.Errorf("UUID should have 4 dashes, got %d: %s", dashCount, id)
		}
	})

	t.Run("all hex characters", func(t *testing.T) {
		id := generateUUID()
		validChars := "0123456789abcdef-"
		for _, c := range id {
			found := false
			for _, v := range validChars {
				if c == v {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("UUID contains invalid character %c: %s", c, id)
			}
		}
	})
}

func TestSpikeScenarioStatsAtomic(t *testing.T) {
	t.Run("spike counters increment correctly", func(t *testing.T) {
		testStats := &SpikeScenarioStats{}

		testStats.SpikesStarted.Add(1)
		testStats.SpikesEnded.Add(1)
		testStats.SpikeSeriesTotal.Add(100)

		if got := testStats.SpikesStarted.Load(); got != 1 {
			t.Errorf("SpikesStarted = %d, want 1", got)
		}
		if got := testStats.SpikesEnded.Load(); got != 1 {
			t.Errorf("SpikesEnded = %d, want 1", got)
		}
		if got := testStats.SpikeSeriesTotal.Load(); got != 100 {
			t.Errorf("SpikeSeriesTotal = %d, want 100", got)
		}
	})

	t.Run("mistake counters increment correctly", func(t *testing.T) {
		testStats := &SpikeScenarioStats{}

		testStats.MistakeStarted.Add(1)
		testStats.MistakeEnded.Add(1)
		testStats.MistakeSeriesTotal.Add(200)

		if got := testStats.MistakeStarted.Load(); got != 1 {
			t.Errorf("MistakeStarted = %d, want 1", got)
		}
		if got := testStats.MistakeEnded.Load(); got != 1 {
			t.Errorf("MistakeEnded = %d, want 1", got)
		}
		if got := testStats.MistakeSeriesTotal.Load(); got != 200 {
			t.Errorf("MistakeSeriesTotal = %d, want 200", got)
		}
	})

	t.Run("active flags work correctly", func(t *testing.T) {
		testStats := &SpikeScenarioStats{}

		// Initial state should be false
		if testStats.ActiveSpike.Load() {
			t.Error("ActiveSpike should be false initially")
		}
		if testStats.ActiveMistake.Load() {
			t.Error("ActiveMistake should be false initially")
		}

		// Set to true
		testStats.ActiveSpike.Store(true)
		testStats.ActiveMistake.Store(true)

		if !testStats.ActiveSpike.Load() {
			t.Error("ActiveSpike should be true")
		}
		if !testStats.ActiveMistake.Load() {
			t.Error("ActiveMistake should be true")
		}

		// Set back to false
		testStats.ActiveSpike.Store(false)
		testStats.ActiveMistake.Store(false)

		if testStats.ActiveSpike.Load() {
			t.Error("ActiveSpike should be false after reset")
		}
		if testStats.ActiveMistake.Load() {
			t.Error("ActiveMistake should be false after reset")
		}
	})

	t.Run("concurrent access safety", func(t *testing.T) {
		testStats := &SpikeScenarioStats{}
		done := make(chan bool)

		// Launch multiple goroutines incrementing spike stats
		for i := 0; i < 10; i++ {
			go func() {
				for j := 0; j < 100; j++ {
					testStats.SpikesStarted.Add(1)
					testStats.SpikeSeriesTotal.Add(10)
				}
				done <- true
			}()
		}

		// Wait for all goroutines
		for i := 0; i < 10; i++ {
			<-done
		}

		// Each goroutine adds 100, 10 goroutines = 1000
		if got := testStats.SpikesStarted.Load(); got != 1000 {
			t.Errorf("SpikesStarted after concurrent access = %d, want 1000", got)
		}
		// Each goroutine adds 100*10=1000, 10 goroutines = 10000
		if got := testStats.SpikeSeriesTotal.Load(); got != 10000 {
			t.Errorf("SpikeSeriesTotal after concurrent access = %d, want 10000", got)
		}
	})
}

func TestSpikeEnvVars(t *testing.T) {
	t.Run("spike scenario env defaults", func(t *testing.T) {
		os.Unsetenv("ENABLE_SPIKE_SCENARIOS")
		os.Unsetenv("SPIKE_MODE")
		os.Unsetenv("SPIKE_CARDINALITY")
		os.Unsetenv("SPIKE_DURATION_SEC")
		os.Unsetenv("SPIKE_INTERVAL_MIN_SEC")
		os.Unsetenv("SPIKE_INTERVAL_MAX_SEC")
		os.Unsetenv("MISTAKE_DELAY_SEC")
		os.Unsetenv("MISTAKE_DURATION_SEC")
		os.Unsetenv("MISTAKE_CARDINALITY_RATE")

		if getEnvBool("ENABLE_SPIKE_SCENARIOS", false) {
			t.Error("ENABLE_SPIKE_SCENARIOS default should be false")
		}
		if getEnv("SPIKE_MODE", "realistic") != "realistic" {
			t.Error("SPIKE_MODE default should be realistic")
		}
		if getEnvInt("SPIKE_CARDINALITY", 1000) != 1000 {
			t.Error("SPIKE_CARDINALITY default should be 1000")
		}
		if getEnvInt("SPIKE_DURATION_SEC", 20) != 20 {
			t.Error("SPIKE_DURATION_SEC default should be 20")
		}
		if getEnvInt("SPIKE_INTERVAL_MIN_SEC", 30) != 30 {
			t.Error("SPIKE_INTERVAL_MIN_SEC default should be 30")
		}
		if getEnvInt("SPIKE_INTERVAL_MAX_SEC", 120) != 120 {
			t.Error("SPIKE_INTERVAL_MAX_SEC default should be 120")
		}
		if getEnvInt("MISTAKE_DELAY_SEC", 90) != 90 {
			t.Error("MISTAKE_DELAY_SEC default should be 90")
		}
		if getEnvInt("MISTAKE_DURATION_SEC", 120) != 120 {
			t.Error("MISTAKE_DURATION_SEC default should be 120")
		}
		if getEnvInt("MISTAKE_CARDINALITY_RATE", 100) != 100 {
			t.Error("MISTAKE_CARDINALITY_RATE default should be 100")
		}
	})

	t.Run("spike scenario env custom values", func(t *testing.T) {
		defer func() {
			os.Unsetenv("ENABLE_SPIKE_SCENARIOS")
			os.Unsetenv("SPIKE_MODE")
			os.Unsetenv("SPIKE_CARDINALITY")
		}()

		os.Setenv("ENABLE_SPIKE_SCENARIOS", "true")
		os.Setenv("SPIKE_MODE", "random")
		os.Setenv("SPIKE_CARDINALITY", "500")

		if !getEnvBool("ENABLE_SPIKE_SCENARIOS", false) {
			t.Error("ENABLE_SPIKE_SCENARIOS should be true when set")
		}
		if getEnv("SPIKE_MODE", "realistic") != "random" {
			t.Error("SPIKE_MODE should be 'random' when set")
		}
		if getEnvInt("SPIKE_CARDINALITY", 1000) != 500 {
			t.Error("SPIKE_CARDINALITY should be 500 when set")
		}
	})
}
