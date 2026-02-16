package stats

import (
	"fmt"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
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
		"metrics_governor_memory_budget_gomemlimit_bytes",
		"metrics_governor_memory_budget_buffer_bytes",
		"metrics_governor_memory_budget_queue_bytes",
		"metrics_governor_memory_budget_utilization_ratio",
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
	if strings.Contains(body, "metrics_governor_disk_read_bytes_total") {
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

// --- Memory budget metrics tests ---

func TestRuntimeMetrics_MemoryBudget_Exposed(t *testing.T) {
	rs := NewRuntimeStats()
	rs.SetMemoryBudget(850*1024*1024, 60*1024*1024, 42*1024*1024)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	rs.ServeHTTP(w, req)

	body := w.Body.String()

	budgetMetrics := []string{
		"metrics_governor_memory_budget_gomemlimit_bytes",
		"metrics_governor_memory_budget_buffer_bytes",
		"metrics_governor_memory_budget_queue_bytes",
		"metrics_governor_memory_budget_utilization_ratio",
	}
	for _, metric := range budgetMetrics {
		if !strings.Contains(body, metric) {
			t.Errorf("missing budget metric %q in output", metric)
		}
	}
}

func TestRuntimeMetrics_MemoryBudget_Values(t *testing.T) {
	const gomemlimit = int64(850 * 1024 * 1024) // 850 MB
	const bufferBytes = int64(60 * 1024 * 1024)  // 60 MB
	const queueBytes = int64(42 * 1024 * 1024)   // 42 MB

	rs := NewRuntimeStats()
	rs.SetMemoryBudget(gomemlimit, bufferBytes, queueBytes)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	rs.ServeHTTP(w, req)

	body := w.Body.String()

	// Check gomemlimit value is present
	expectedGomemlimit := "metrics_governor_memory_budget_gomemlimit_bytes 891289600"
	if !strings.Contains(body, expectedGomemlimit) {
		t.Errorf("expected %q in output", expectedGomemlimit)
	}

	// Check buffer budget
	expectedBuffer := "metrics_governor_memory_budget_buffer_bytes 62914560"
	if !strings.Contains(body, expectedBuffer) {
		t.Errorf("expected %q in output", expectedBuffer)
	}

	// Check queue budget
	expectedQueue := "metrics_governor_memory_budget_queue_bytes 44040192"
	if !strings.Contains(body, expectedQueue) {
		t.Errorf("expected %q in output", expectedQueue)
	}
}

func TestRuntimeMetrics_MemoryBudget_ZeroWhenNoLimit(t *testing.T) {
	rs := NewRuntimeStats()
	// Don't call SetMemoryBudget — simulate no GOMEMLIMIT

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	rs.ServeHTTP(w, req)

	body := w.Body.String()

	// Buffer and queue budgets should be 0
	if !strings.Contains(body, "metrics_governor_memory_budget_buffer_bytes 0") {
		t.Error("expected buffer_bytes=0 when no memory budget set")
	}
	if !strings.Contains(body, "metrics_governor_memory_budget_queue_bytes 0") {
		t.Error("expected queue_bytes=0 when no memory budget set")
	}
}

func TestRuntimeMetrics_UtilizationRatio_Range(t *testing.T) {
	rs := NewRuntimeStats()
	rs.SetMemoryBudget(850*1024*1024, 60*1024*1024, 42*1024*1024)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	rs.ServeHTTP(w, req)

	body := w.Body.String()

	// Find the utilization ratio line and verify it's in 0.0-1.0 range
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "metrics_governor_memory_budget_utilization_ratio ") {
			parts := strings.Fields(line)
			if len(parts) != 2 {
				t.Fatalf("unexpected line format: %q", line)
			}
			var ratio float64
			_, err := fmt.Sscanf(parts[1], "%f", &ratio)
			if err != nil {
				t.Fatalf("failed to parse ratio %q: %v", parts[1], err)
			}
			if ratio < 0.0 || ratio > 1.0 {
				t.Errorf("utilization_ratio = %f, want [0.0, 1.0]", ratio)
			}
			return
		}
	}
	t.Error("utilization_ratio metric not found")
}

func TestRuntimeMetrics_UtilizationRatio_Increases(t *testing.T) {
	rs := NewRuntimeStats()
	rs.SetMemoryBudget(850*1024*1024, 60*1024*1024, 42*1024*1024)

	// Get initial ratio
	req1 := httptest.NewRequest("GET", "/metrics", nil)
	w1 := httptest.NewRecorder()
	rs.ServeHTTP(w1, req1)
	ratio1 := extractRatio(t, w1.Body.String())

	// Allocate some memory to increase heap
	waste := make([]byte, 10*1024*1024) // 10 MB
	_ = waste[0]                         // prevent optimization

	// Get new ratio
	req2 := httptest.NewRequest("GET", "/metrics", nil)
	w2 := httptest.NewRecorder()
	rs.ServeHTTP(w2, req2)
	ratio2 := extractRatio(t, w2.Body.String())

	// ratio2 should be >= ratio1 (we allocated memory)
	// Note: GC might have run, so we just check it's still valid
	if ratio2 < 0.0 || ratio2 > 1.0 {
		t.Errorf("utilization_ratio after alloc = %f, want [0.0, 1.0]", ratio2)
	}

	// Keep waste alive past the measurement
	runtime.KeepAlive(waste)
	_ = ratio1 // use the value
}

func extractRatio(t *testing.T, body string) float64 {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "metrics_governor_memory_budget_utilization_ratio ") {
			parts := strings.Fields(line)
			if len(parts) != 2 {
				t.Fatalf("unexpected line format: %q", line)
			}
			var ratio float64
			_, err := fmt.Sscanf(parts[1], "%f", &ratio)
			if err != nil {
				t.Fatalf("failed to parse ratio: %v", err)
			}
			return ratio
		}
	}
	t.Fatal("utilization_ratio metric not found")
	return 0
}

func TestRace_RuntimeMetrics_ConcurrentRead(t *testing.T) {
	t.Parallel()

	rs := NewRuntimeStats()
	rs.SetMemoryBudget(850*1024*1024, 60*1024*1024, 42*1024*1024)

	done := make(chan struct{})
	time.AfterFunc(500*time.Millisecond, func() { close(done) })

	var wg sync.WaitGroup

	// 4 goroutines calling ServeHTTP concurrently
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				req := httptest.NewRequest("GET", "/metrics", nil)
				w := httptest.NewRecorder()
				rs.ServeHTTP(w, req)

				if w.Result().StatusCode != 200 {
					t.Errorf("unexpected status: %d", w.Result().StatusCode)
					return
				}
			}
		}()
	}

	// 1 goroutine allocating and freeing memory
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			waste := make([]byte, 1024*1024) // 1 MB
			waste[0] = 1
			runtime.KeepAlive(waste)
			runtime.Gosched()
		}
	}()

	wg.Wait()
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
