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

// Test parsePSIFile with mock data
func TestParsePSIFileWithMockData(t *testing.T) {
	// Create a temp file with mock PSI data
	tmpDir := t.TempDir()
	mockPSI := filepath.Join(tmpDir, "cpu")

	mockData := `some avg10=0.50 avg60=0.30 avg300=0.10 total=12345
full avg10=0.10 avg60=0.05 avg300=0.02 total=6789`

	if err := os.WriteFile(mockPSI, []byte(mockData), 0644); err != nil {
		t.Fatalf("Failed to create mock PSI file: %v", err)
	}

	metrics, err := parsePSIFile(mockPSI)
	if err != nil {
		t.Fatalf("parsePSIFile failed: %v", err)
	}

	// Check "some" metrics
	if some, ok := metrics["some"]; ok {
		if some.Avg10 != 0.50 {
			t.Errorf("Expected Avg10=0.50, got %f", some.Avg10)
		}
		if some.Avg60 != 0.30 {
			t.Errorf("Expected Avg60=0.30, got %f", some.Avg60)
		}
		if some.Avg300 != 0.10 {
			t.Errorf("Expected Avg300=0.10, got %f", some.Avg300)
		}
		if some.Total != 12345 {
			t.Errorf("Expected Total=12345, got %d", some.Total)
		}
	} else {
		t.Error("Expected 'some' metrics to be present")
	}

	// Check "full" metrics
	if full, ok := metrics["full"]; ok {
		if full.Avg10 != 0.10 {
			t.Errorf("Expected Avg10=0.10, got %f", full.Avg10)
		}
		if full.Total != 6789 {
			t.Errorf("Expected Total=6789, got %d", full.Total)
		}
	} else {
		t.Error("Expected 'full' metrics to be present")
	}
}

func TestParsePSIFileShortLine(t *testing.T) {
	tmpDir := t.TempDir()
	mockPSI := filepath.Join(tmpDir, "short")

	// Line with fewer than 5 parts should be skipped
	mockData := `short line
some avg10=0.50 avg60=0.30 avg300=0.10 total=1000`

	if err := os.WriteFile(mockPSI, []byte(mockData), 0644); err != nil {
		t.Fatalf("Failed to create mock PSI file: %v", err)
	}

	metrics, err := parsePSIFile(mockPSI)
	if err != nil {
		t.Fatalf("parsePSIFile failed: %v", err)
	}

	// Should have parsed the valid line
	if _, ok := metrics["some"]; !ok {
		t.Error("Expected 'some' metrics to be present")
	}
}

func TestParsePSIFileMalformedKV(t *testing.T) {
	tmpDir := t.TempDir()
	mockPSI := filepath.Join(tmpDir, "malformed")

	// Key-value without = should be skipped
	mockData := `some avg10 avg60=0.30 avg300=0.10 total=1000`

	if err := os.WriteFile(mockPSI, []byte(mockData), 0644); err != nil {
		t.Fatalf("Failed to create mock PSI file: %v", err)
	}

	metrics, err := parsePSIFile(mockPSI)
	if err != nil {
		t.Fatalf("parsePSIFile failed: %v", err)
	}

	// Should still parse other fields
	if some, ok := metrics["some"]; ok {
		// Avg10 should be 0 (not parsed due to malformed)
		if some.Avg10 != 0 {
			t.Errorf("Expected Avg10=0 for malformed, got %f", some.Avg10)
		}
		if some.Avg60 != 0.30 {
			t.Errorf("Expected Avg60=0.30, got %f", some.Avg60)
		}
	}
}

func TestWritePSIMetricsNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("This test is for non-Linux platforms")
	}

	rs := NewRuntimeStats()
	var buf bytes.Buffer
	w := httptest.NewRecorder()

	rs.writePSIMetrics(w)

	// Should produce no output on non-Linux
	body := buf.String()
	if strings.Contains(body, "psi") {
		t.Error("PSI metrics should not be written on non-Linux")
	}
}

func TestWritePSIMetricsLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("This test is only for Linux")
	}

	rs := NewRuntimeStats()
	w := httptest.NewRecorder()

	rs.writePSIMetrics(w)

	body := w.Body.String()
	// On Linux, PSI metrics may or may not be available
	// Just verify no panic occurred
	t.Logf("PSI output length: %d bytes", len(body))
}

func TestWriteProcessCPUMetricsNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("This test is for non-Linux platforms")
	}

	rs := NewRuntimeStats()
	w := httptest.NewRecorder()

	rs.writeProcessCPUMetrics(w)

	// Should produce no output on non-Linux
	body := w.Body.String()
	if body != "" {
		t.Error("Process CPU metrics should not be written on non-Linux")
	}
}

func TestWriteProcessCPUMetricsLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("This test is only for Linux")
	}

	rs := NewRuntimeStats()
	w := httptest.NewRecorder()

	rs.writeProcessCPUMetrics(w)

	body := w.Body.String()

	// Should contain process CPU metrics on Linux
	expectedMetrics := []string{
		"metrics_governor_process_cpu",
		"metrics_governor_process_virtual_memory_bytes",
		"metrics_governor_process_resident_memory_bytes",
	}

	for _, metric := range expectedMetrics {
		if !strings.Contains(body, metric) {
			t.Errorf("Expected process CPU metric '%s' to be present", metric)
		}
	}
}

func TestWriteDiskIOMetricsNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("This test is for non-Linux platforms")
	}

	rs := NewRuntimeStats()
	w := httptest.NewRecorder()

	rs.writeDiskIOMetrics(w)

	// Should produce no output on non-Linux
	body := w.Body.String()
	if body != "" {
		t.Error("Disk I/O metrics should not be written on non-Linux")
	}
}

func TestWriteDiskIOMetricsLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("This test is only for Linux")
	}

	rs := NewRuntimeStats()
	w := httptest.NewRecorder()

	rs.writeDiskIOMetrics(w)

	// Disk I/O metrics may or may not be available depending on permissions
	body := w.Body.String()
	t.Logf("Disk I/O output length: %d bytes", len(body))
}

func TestWriteNetworkIOMetricsNonLinux(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("This test is for non-Linux platforms")
	}

	rs := NewRuntimeStats()
	w := httptest.NewRecorder()

	rs.writeNetworkIOMetrics(w)

	// Should produce no output on non-Linux
	body := w.Body.String()
	if body != "" {
		t.Error("Network I/O metrics should not be written on non-Linux")
	}
}

func TestWriteNetworkIOMetricsLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("This test is only for Linux")
	}

	rs := NewRuntimeStats()
	w := httptest.NewRecorder()

	rs.writeNetworkIOMetrics(w)

	body := w.Body.String()

	// Should contain network metrics on Linux
	if !strings.Contains(body, "metrics_governor_network") {
		t.Error("Expected network metrics to be present on Linux")
	}
}

func TestRuntimeStatsAllMetricsOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Full metrics test only on Linux")
	}

	rs := NewRuntimeStats()
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	rs.ServeHTTP(w, req)

	body := w.Body.String()

	// Check for Linux-specific metric categories
	categories := []string{
		"process_cpu",
		"process_virtual_memory_bytes",
		"process_resident_memory_bytes",
		"network_receive_bytes_total",
		"network_transmit_bytes_total",
	}

	for _, cat := range categories {
		if !strings.Contains(body, cat) {
			t.Errorf("Expected Linux metric category '%s' to be present", cat)
		}
	}
}
