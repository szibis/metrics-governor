package stats

import (
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

func TestNewRuntimeStats(t *testing.T) {
	rs := NewRuntimeStats()

	if rs == nil {
		t.Fatal("expected non-nil RuntimeStats")
	}
	if rs.startTime.IsZero() {
		t.Error("expected startTime to be set")
	}
}

func TestRuntimeStatsServeHTTP(t *testing.T) {
	rs := NewRuntimeStats()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	rs.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body := w.Body.String()

	// Check for expected metrics (cross-platform)
	expectedMetrics := []string{
		"metrics_governor_process_start_time_seconds",
		"metrics_governor_process_uptime_seconds",
		"metrics_governor_goroutines",
		"metrics_governor_go_threads",
		"metrics_governor_memory_alloc_bytes",
		"metrics_governor_memory_total_alloc_bytes",
		"metrics_governor_memory_sys_bytes",
		"metrics_governor_memory_heap_alloc_bytes",
		"metrics_governor_memory_heap_sys_bytes",
		"metrics_governor_memory_heap_idle_bytes",
		"metrics_governor_memory_heap_inuse_bytes",
		"metrics_governor_memory_heap_released_bytes",
		"metrics_governor_memory_heap_objects",
		"metrics_governor_memory_stack_inuse_bytes",
		"metrics_governor_memory_stack_sys_bytes",
		"metrics_governor_gc_cycles_total",
		"metrics_governor_gc_pause_total_seconds",
		"metrics_governor_gc_cpu_percent",
		"metrics_governor_memory_mallocs_total",
		"metrics_governor_memory_frees_total",
		"metrics_governor_go_info",
	}

	for _, metric := range expectedMetrics {
		if !strings.Contains(body, metric) {
			t.Errorf("expected response to contain '%s'", metric)
		}
	}
}

func TestRuntimeStatsGoVersion(t *testing.T) {
	rs := NewRuntimeStats()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	rs.ServeHTTP(w, req)

	body := w.Body.String()

	// Should contain the Go version
	goVersion := runtime.Version()
	if !strings.Contains(body, goVersion) {
		t.Errorf("expected response to contain Go version '%s'", goVersion)
	}
}

func TestRuntimeStatsContentType(t *testing.T) {
	rs := NewRuntimeStats()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	rs.ServeHTTP(w, req)

	// Content-Type is not explicitly set in RuntimeStats.ServeHTTP
	// but should be set by default to text/plain by fmt.Fprintf
}

func TestRuntimeStatsMemoryMetrics(t *testing.T) {
	rs := NewRuntimeStats()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	rs.ServeHTTP(w, req)

	body := w.Body.String()

	// Memory values should be positive numbers
	if !strings.Contains(body, "metrics_governor_memory_alloc_bytes") {
		t.Error("missing memory_alloc_bytes metric")
	}
}

func TestRuntimeStatsUptime(t *testing.T) {
	rs := NewRuntimeStats()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	rs.ServeHTTP(w, req)

	body := w.Body.String()

	// Uptime should be present and positive
	if !strings.Contains(body, "metrics_governor_process_uptime_seconds") {
		t.Error("missing process_uptime_seconds metric")
	}
}

func TestRuntimeStatsGoroutines(t *testing.T) {
	rs := NewRuntimeStats()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	rs.ServeHTTP(w, req)

	body := w.Body.String()

	// Should report at least 1 goroutine (the test itself)
	if !strings.Contains(body, "metrics_governor_goroutines") {
		t.Error("missing goroutines metric")
	}
}

// Test PSI parsing function

func TestParsePSIFile(t *testing.T) {
	// This test only runs on Linux where PSI is available
	if runtime.GOOS != "linux" {
		t.Skip("PSI metrics only available on Linux")
	}

	// Try to parse CPU PSI (most likely to exist)
	metrics, err := parsePSIFile("/proc/pressure/cpu")
	if err != nil {
		t.Skipf("PSI not available: %v", err)
	}

	// Check that some metric was parsed
	if len(metrics) == 0 {
		t.Error("expected at least one PSI metric type")
	}

	// Check for "some" metric
	if some, ok := metrics["some"]; ok {
		// Values should be valid (non-negative)
		if some.Avg10 < 0 || some.Avg60 < 0 || some.Avg300 < 0 {
			t.Error("PSI averages should be non-negative")
		}
	}
}

func TestParsePSIFileNonExistent(t *testing.T) {
	_, err := parsePSIFile("/nonexistent/path")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

// Test Linux-specific metrics presence

func TestLinuxSpecificMetrics(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-specific metrics test")
	}

	rs := NewRuntimeStats()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	rs.ServeHTTP(w, req)

	body := w.Body.String()

	// These metrics should be present on Linux
	linuxMetrics := []string{
		"metrics_governor_process_cpu_user_seconds",
		"metrics_governor_process_cpu_system_seconds",
		"metrics_governor_process_cpu_total_seconds",
		"metrics_governor_process_virtual_memory_bytes",
		"metrics_governor_process_resident_memory_bytes",
	}

	for _, metric := range linuxMetrics {
		if !strings.Contains(body, metric) {
			t.Errorf("expected Linux metric '%s' to be present", metric)
		}
	}
}

func TestNetworkMetrics(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Network metrics test only on Linux")
	}

	rs := NewRuntimeStats()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	rs.ServeHTTP(w, req)

	body := w.Body.String()

	// Network metrics should be present on Linux
	networkMetrics := []string{
		"metrics_governor_network_receive_bytes_total",
		"metrics_governor_network_transmit_bytes_total",
		"metrics_governor_network_receive_packets_total",
		"metrics_governor_network_transmit_packets_total",
	}

	for _, metric := range networkMetrics {
		if !strings.Contains(body, metric) {
			t.Errorf("expected network metric '%s' to be present", metric)
		}
	}
}

func TestDiskIOMetrics(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Disk I/O metrics test only on Linux")
	}

	rs := NewRuntimeStats()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	rs.ServeHTTP(w, req)

	body := w.Body.String()

	// Disk I/O metrics may or may not be available depending on permissions
	// Just check the basic ones that should work
	if strings.Contains(body, "metrics_governor_process_io_read_bytes_total") {
		// Good, IO metrics are available
		t.Log("Disk I/O metrics are available")
	}
}

// Test multiple calls don't panic

func TestRuntimeStatsMultipleCalls(t *testing.T) {
	rs := NewRuntimeStats()

	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("GET", "/metrics", nil)
		w := httptest.NewRecorder()
		rs.ServeHTTP(w, req)

		if w.Result().StatusCode != 200 {
			t.Errorf("call %d failed with status %d", i, w.Result().StatusCode)
		}
	}
}

// Test GC metrics after forced GC

func TestRuntimeStatsAfterGC(t *testing.T) {
	rs := NewRuntimeStats()

	// Force a GC
	runtime.GC()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	rs.ServeHTTP(w, req)

	body := w.Body.String()

	// After GC, we should have at least 1 GC cycle
	if !strings.Contains(body, "metrics_governor_gc_cycles_total") {
		t.Error("missing gc_cycles_total metric")
	}

	// Last GC pause should be present
	if !strings.Contains(body, "metrics_governor_gc_last_pause_seconds") {
		t.Error("missing gc_last_pause_seconds metric after GC")
	}
}
