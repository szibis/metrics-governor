package stats

import (
	"bytes"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestParsePSIFileMockData tests PSI parsing with mock data.
func TestParsePSIFileMockData(t *testing.T) {
	tmpDir := t.TempDir()
	psiFile := filepath.Join(tmpDir, "cpu")

	// Create mock PSI file with valid format
	content := `some avg10=1.23 avg60=4.56 avg300=7.89 total=12345678
full avg10=0.12 avg60=0.34 avg300=0.56 total=9876543
`
	if err := os.WriteFile(psiFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create mock PSI file: %v", err)
	}

	metrics, err := parsePSIFile(psiFile)
	if err != nil {
		t.Fatalf("Failed to parse mock PSI file: %v", err)
	}

	// Verify "some" metrics
	some, ok := metrics["some"]
	if !ok {
		t.Fatal("Expected 'some' metrics")
	}
	if some.Avg10 != 1.23 {
		t.Errorf("Expected Avg10=1.23, got %f", some.Avg10)
	}
	if some.Avg60 != 4.56 {
		t.Errorf("Expected Avg60=4.56, got %f", some.Avg60)
	}
	if some.Avg300 != 7.89 {
		t.Errorf("Expected Avg300=7.89, got %f", some.Avg300)
	}
	if some.Total != 12345678 {
		t.Errorf("Expected Total=12345678, got %d", some.Total)
	}

	// Verify "full" metrics
	full, ok := metrics["full"]
	if !ok {
		t.Fatal("Expected 'full' metrics")
	}
	if full.Avg10 != 0.12 {
		t.Errorf("Expected full Avg10=0.12, got %f", full.Avg10)
	}
	if full.Total != 9876543 {
		t.Errorf("Expected full Total=9876543, got %d", full.Total)
	}
}

// TestParsePSIFilePartialData tests PSI parsing with partial fields.
func TestParsePSIFilePartialData(t *testing.T) {
	tmpDir := t.TempDir()
	psiFile := filepath.Join(tmpDir, "cpu")

	// Create mock PSI file with 5 fields (minimum) but missing avg300 and total
	// Format requires at least 5 fields: type field1 field2 field3 field4
	content := `some avg10=1.23 avg60=4.56 extra1=0 extra2=0
`
	if err := os.WriteFile(psiFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create mock PSI file: %v", err)
	}

	metrics, err := parsePSIFile(psiFile)
	if err != nil {
		t.Fatalf("Failed to parse mock PSI file: %v", err)
	}

	some, ok := metrics["some"]
	if !ok {
		t.Fatal("Expected 'some' metrics")
	}
	if some.Avg10 != 1.23 {
		t.Errorf("Expected Avg10=1.23, got %f", some.Avg10)
	}
	// avg300 and total should be zero (not present in file)
	if some.Avg300 != 0 {
		t.Errorf("Expected Avg300=0, got %f", some.Avg300)
	}
}

// TestParsePSIFileEmptyFile tests PSI parsing with empty file.
func TestParsePSIFileEmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	psiFile := filepath.Join(tmpDir, "cpu")

	if err := os.WriteFile(psiFile, []byte(""), 0644); err != nil {
		t.Fatalf("Failed to create mock PSI file: %v", err)
	}

	metrics, err := parsePSIFile(psiFile)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(metrics) != 0 {
		t.Errorf("Expected empty metrics, got %d", len(metrics))
	}
}

// TestParsePSIFileMalformedLine tests PSI parsing with malformed lines.
func TestParsePSIFileMalformedLine(t *testing.T) {
	tmpDir := t.TempDir()
	psiFile := filepath.Join(tmpDir, "cpu")

	// Lines with fewer than 5 fields should be skipped
	content := `short line
some avg10=1.23 avg60=4.56 avg300=7.89 total=12345678
`
	if err := os.WriteFile(psiFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create mock PSI file: %v", err)
	}

	metrics, err := parsePSIFile(psiFile)
	if err != nil {
		t.Fatalf("Failed to parse mock PSI file: %v", err)
	}

	// Should have parsed the valid line
	if _, ok := metrics["some"]; !ok {
		t.Fatal("Expected 'some' metrics from valid line")
	}
}

// TestParsePSIFileMalformedKeyValue tests PSI parsing with malformed key=value pairs.
func TestParsePSIFileMalformedKeyValue(t *testing.T) {
	tmpDir := t.TempDir()
	psiFile := filepath.Join(tmpDir, "cpu")

	// Malformed key-value pairs should be skipped
	content := `some avg10=1.23 malformed avg300=7.89 total=12345678
`
	if err := os.WriteFile(psiFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create mock PSI file: %v", err)
	}

	metrics, err := parsePSIFile(psiFile)
	if err != nil {
		t.Fatalf("Failed to parse mock PSI file: %v", err)
	}

	some := metrics["some"]
	if some.Avg10 != 1.23 {
		t.Errorf("Expected Avg10=1.23, got %f", some.Avg10)
	}
	// avg60 should be zero since "malformed" isn't valid
	if some.Avg60 != 0 {
		t.Errorf("Expected Avg60=0, got %f", some.Avg60)
	}
}

// TestWritePSIMetricsNonLinuxExtended tests that PSI metrics are skipped on non-Linux.
func TestWritePSIMetricsNonLinuxExtended(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("This test is for non-Linux systems")
	}

	rs := NewRuntimeStats()
	var buf bytes.Buffer
	w := httptest.NewRecorder()
	rs.writePSIMetrics(w)

	// On non-Linux, should not write any PSI metrics
	body := buf.String()
	if strings.Contains(body, "metrics_governor_psi") {
		t.Error("Expected no PSI metrics on non-Linux")
	}
}

// TestWriteProcessCPUMetricsNonLinuxExtended tests that CPU metrics are skipped on non-Linux.
func TestWriteProcessCPUMetricsNonLinuxExtended(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("This test is for non-Linux systems")
	}

	rs := NewRuntimeStats()
	w := httptest.NewRecorder()
	rs.writeProcessCPUMetrics(w)

	// On non-Linux, should not write any process CPU metrics
	body := w.Body.String()
	if strings.Contains(body, "metrics_governor_process_cpu") {
		t.Error("Expected no process CPU metrics on non-Linux")
	}
}

// TestWriteDiskIOMetricsNonLinuxExtended tests that disk I/O metrics are skipped on non-Linux.
func TestWriteDiskIOMetricsNonLinuxExtended(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("This test is for non-Linux systems")
	}

	rs := NewRuntimeStats()
	w := httptest.NewRecorder()
	rs.writeDiskIOMetrics(w)

	// On non-Linux, should not write any disk I/O metrics
	body := w.Body.String()
	if strings.Contains(body, "metrics_governor_disk_") {
		t.Error("Expected no disk I/O metrics on non-Linux")
	}
}

// TestWriteNetworkIOMetricsNonLinuxExtended tests that network I/O metrics are skipped on non-Linux.
func TestWriteNetworkIOMetricsNonLinuxExtended(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("This test is for non-Linux systems")
	}

	rs := NewRuntimeStats()
	w := httptest.NewRecorder()
	rs.writeNetworkIOMetrics(w)

	// On non-Linux, should not write any network I/O metrics
	body := w.Body.String()
	if strings.Contains(body, "metrics_governor_network") {
		t.Error("Expected no network I/O metrics on non-Linux")
	}
}

// TestRuntimeStatsAllMetricsPresent verifies all expected metrics are present.
func TestRuntimeStatsAllMetricsPresent(t *testing.T) {
	rs := NewRuntimeStats()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	rs.ServeHTTP(w, req)

	body := w.Body.String()

	// All cross-platform metrics must be present
	requiredMetrics := []string{
		"metrics_governor_process_start_time_seconds",
		"metrics_governor_process_uptime_seconds",
		"metrics_governor_goroutines",
		"metrics_governor_go_threads",
		"metrics_governor_memory_alloc_bytes",
		"metrics_governor_memory_sys_bytes",
		"metrics_governor_gc_cycles_total",
		"metrics_governor_go_info",
	}

	for _, metric := range requiredMetrics {
		if !strings.Contains(body, metric) {
			t.Errorf("Required metric '%s' not found", metric)
		}
	}
}

// TestRuntimeStatsMetricValues verifies metrics have reasonable values.
func TestRuntimeStatsMetricValues(t *testing.T) {
	rs := NewRuntimeStats()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	rs.ServeHTTP(w, req)

	body := w.Body.String()

	// Start time should be a large positive number (Unix timestamp)
	if !strings.Contains(body, "metrics_governor_process_start_time_seconds") {
		t.Error("Missing start time metric")
	}

	// Goroutines should be at least 1
	if !strings.Contains(body, "metrics_governor_goroutines") {
		t.Error("Missing goroutines metric")
	}

	// Memory should be positive
	if !strings.Contains(body, "metrics_governor_memory_alloc_bytes") {
		t.Error("Missing memory alloc metric")
	}
}

// TestRuntimeStatsHelpAndType verifies HELP and TYPE comments are present.
func TestRuntimeStatsHelpAndType(t *testing.T) {
	rs := NewRuntimeStats()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	rs.ServeHTTP(w, req)

	body := w.Body.String()

	// Check for HELP and TYPE comments
	if !strings.Contains(body, "# HELP") {
		t.Error("Missing HELP comments")
	}
	if !strings.Contains(body, "# TYPE") {
		t.Error("Missing TYPE comments")
	}
}

// TestRuntimeStatsGaugeTypes verifies gauge metrics have correct type.
func TestRuntimeStatsGaugeTypes(t *testing.T) {
	rs := NewRuntimeStats()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	rs.ServeHTTP(w, req)

	body := w.Body.String()

	gaugeMetrics := []string{
		"metrics_governor_goroutines",
		"metrics_governor_go_threads",
		"metrics_governor_memory_alloc_bytes",
		"metrics_governor_process_uptime_seconds",
	}

	for _, metric := range gaugeMetrics {
		typeComment := "# TYPE " + metric + " gauge"
		if !strings.Contains(body, typeComment) {
			t.Errorf("Expected gauge type for '%s'", metric)
		}
	}
}

// TestRuntimeStatsCounterTypes verifies counter metrics have correct type.
func TestRuntimeStatsCounterTypes(t *testing.T) {
	rs := NewRuntimeStats()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	rs.ServeHTTP(w, req)

	body := w.Body.String()

	counterMetrics := []string{
		"metrics_governor_gc_cycles_total",
		"metrics_governor_gc_pause_total_seconds",
		"metrics_governor_memory_mallocs_total",
		"metrics_governor_memory_frees_total",
	}

	for _, metric := range counterMetrics {
		typeComment := "# TYPE " + metric + " counter"
		if !strings.Contains(body, typeComment) {
			t.Errorf("Expected counter type for '%s'", metric)
		}
	}
}
