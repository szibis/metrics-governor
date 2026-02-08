package stats

import (
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestWritePSIResource tests PSI metrics writing with mock data.
func TestWritePSIResource(t *testing.T) {
	metrics := map[string]*psiMetric{
		"some": {Avg10: 1.23, Avg60: 4.56, Avg300: 7.89, Total: 12345678},
		"full": {Avg10: 0.12, Avg60: 0.34, Avg300: 0.56, Total: 9876543},
	}

	w := httptest.NewRecorder()
	writePSIResource(w, "cpu", metrics)

	body := w.Body.String()

	expected := []string{
		"metrics_governor_psi_cpu_some_avg10 1.23",
		"metrics_governor_psi_cpu_some_avg60 4.56",
		"metrics_governor_psi_cpu_some_avg300 7.89",
		"metrics_governor_psi_cpu_some_total_microseconds 12345678",
		"metrics_governor_psi_cpu_full_avg10 0.12",
		"metrics_governor_psi_cpu_full_avg60 0.34",
		"metrics_governor_psi_cpu_full_avg300 0.56",
		"metrics_governor_psi_cpu_full_total_microseconds 9876543",
	}

	for _, exp := range expected {
		if !strings.Contains(body, exp) {
			t.Errorf("Expected output to contain %q", exp)
		}
	}

	// Verify HELP and TYPE comments
	if !strings.Contains(body, "# HELP metrics_governor_psi_cpu_some_avg10") {
		t.Error("Missing HELP for psi_cpu_some_avg10")
	}
	if !strings.Contains(body, "# TYPE metrics_governor_psi_cpu_some_avg10 gauge") {
		t.Error("Missing TYPE gauge for psi_cpu_some_avg10")
	}
	if !strings.Contains(body, "# TYPE metrics_governor_psi_cpu_some_total_microseconds counter") {
		t.Error("Missing TYPE counter for psi_cpu_some_total_microseconds")
	}
}

// TestWritePSIResourceSomeOnly tests PSI writing with only "some" metrics (like CPU).
func TestWritePSIResourceSomeOnly(t *testing.T) {
	metrics := map[string]*psiMetric{
		"some": {Avg10: 2.5, Avg60: 1.0, Avg300: 0.5, Total: 100},
	}

	w := httptest.NewRecorder()
	writePSIResource(w, "memory", metrics)

	body := w.Body.String()

	if !strings.Contains(body, "metrics_governor_psi_memory_some_avg10 2.50") {
		t.Error("Missing memory some avg10")
	}
	// Should NOT contain "full" metrics
	if strings.Contains(body, "metrics_governor_psi_memory_full") {
		t.Error("Unexpected full metrics for memory when only some is present")
	}
}

// TestWritePSIResourceEmpty tests PSI writing with empty metrics map.
func TestWritePSIResourceEmpty(t *testing.T) {
	w := httptest.NewRecorder()
	writePSIResource(w, "io", map[string]*psiMetric{})

	body := w.Body.String()
	if body != "" {
		t.Errorf("Expected empty output for empty metrics, got: %s", body)
	}
}

// TestWriteProcessStat tests /proc/self/stat parsing and metric output.
func TestWriteProcessStat(t *testing.T) {
	// Simulated /proc/self/stat line (simplified — 52 fields like real Linux)
	// Fields of interest: index 13 (utime), 14 (stime), 22 (vsize), 23 (rss)
	fields := make([]string, 52)
	fields[0] = "12345"              // pid
	fields[1] = "(metrics-governor)" // comm
	fields[2] = "S"                  // state
	for i := 3; i < 52; i++ {
		fields[i] = "0"
	}
	fields[13] = "500"       // utime = 500 jiffies = 5.00 seconds
	fields[14] = "200"       // stime = 200 jiffies = 2.00 seconds
	fields[22] = "104857600" // vsize = 100MB
	fields[23] = "25600"     // rss = 25600 pages

	data := strings.Join(fields, " ")

	w := httptest.NewRecorder()
	writeProcessStat(w, data)

	body := w.Body.String()

	expected := []struct {
		metric string
		value  string
	}{
		{"metrics_governor_process_cpu_user_seconds", "5.00"},
		{"metrics_governor_process_cpu_system_seconds", "2.00"},
		{"metrics_governor_process_cpu_total_seconds", "7.00"},
		{"metrics_governor_process_virtual_memory_bytes", "104857600"},
	}

	for _, exp := range expected {
		line := exp.metric + " " + exp.value
		if !strings.Contains(body, line) {
			t.Errorf("Expected output to contain %q", line)
		}
	}

	// Verify HELP/TYPE
	if !strings.Contains(body, "# TYPE metrics_governor_process_cpu_user_seconds counter") {
		t.Error("Missing counter TYPE for cpu_user_seconds")
	}
	if !strings.Contains(body, "# TYPE metrics_governor_process_virtual_memory_bytes gauge") {
		t.Error("Missing gauge TYPE for virtual_memory_bytes")
	}

	// RSS should be present (value depends on page size)
	if !strings.Contains(body, "metrics_governor_process_resident_memory_bytes") {
		t.Error("Missing resident_memory_bytes metric")
	}
}

// TestWriteProcessStatTooFewFields tests graceful handling of short data.
func TestWriteProcessStatTooFewFields(t *testing.T) {
	w := httptest.NewRecorder()
	writeProcessStat(w, "1 (foo) S 0 0 0")

	body := w.Body.String()
	if body != "" {
		t.Errorf("Expected empty output for too-few fields, got: %s", body)
	}
}

// TestWriteMaxFDs tests /proc/self/limits parsing.
func TestWriteMaxFDs(t *testing.T) {
	data := `Limit                     Soft Limit           Hard Limit           Units
Max cpu time              unlimited            unlimited            seconds
Max file size             unlimited            unlimited            bytes
Max data size             unlimited            unlimited            bytes
Max open files            1048576              1048576              files
Max locked memory         8388608              unlimited            bytes
`

	w := httptest.NewRecorder()
	writeMaxFDs(w, data)

	body := w.Body.String()

	if !strings.Contains(body, "metrics_governor_process_max_fds 1048576") {
		t.Errorf("Expected max_fds=1048576, got: %s", body)
	}
	if !strings.Contains(body, "# TYPE metrics_governor_process_max_fds gauge") {
		t.Error("Missing gauge TYPE for max_fds")
	}
}

// TestWriteMaxFDsNoMatch tests limits data without "Max open files".
func TestWriteMaxFDsNoMatch(t *testing.T) {
	data := `Limit                     Soft Limit           Hard Limit           Units
Max cpu time              unlimited            unlimited            seconds
`

	w := httptest.NewRecorder()
	writeMaxFDs(w, data)

	body := w.Body.String()
	if body != "" {
		t.Errorf("Expected empty output when no open files limit, got: %s", body)
	}
}

// TestWriteDiskIO tests /proc/self/io parsing — disk I/O, VFS bytes, and syscall counts.
func TestWriteDiskIO(t *testing.T) {
	data := `rchar: 123456789
wchar: 987654321
syscr: 50000
syscw: 30000
read_bytes: 4096000
write_bytes: 8192000
canceled_write_bytes: 0
`

	w := httptest.NewRecorder()
	writeDiskIO(w, data)

	body := w.Body.String()

	// All I/O metrics should be emitted
	expected := []struct {
		metric string
		value  string
	}{
		{"metrics_governor_disk_read_bytes_total", "4096000"},
		{"metrics_governor_disk_write_bytes_total", "8192000"},
		{"metrics_governor_io_read_ops_total", "50000"},
		{"metrics_governor_io_write_ops_total", "30000"},
		{"metrics_governor_io_vfs_read_bytes_total", "123456789"},
		{"metrics_governor_io_vfs_write_bytes_total", "987654321"},
	}

	for _, exp := range expected {
		line := exp.metric + " " + exp.value
		if !strings.Contains(body, line) {
			t.Errorf("Expected output to contain %q", line)
		}
	}

	// Verify all are counters
	for _, exp := range expected {
		typeComment := "# TYPE " + exp.metric + " counter"
		if !strings.Contains(body, typeComment) {
			t.Errorf("Expected counter TYPE for %s", exp.metric)
		}
	}

	// canceled_write_bytes should NOT be emitted
	if strings.Contains(body, "canceled_write_bytes") {
		t.Error("Should not emit canceled_write_bytes metric")
	}
}

// TestWriteDiskIOPartialData tests disk I/O with incomplete data.
func TestWriteDiskIOPartialData(t *testing.T) {
	data := `rchar: 100
invalid_line
wchar: not_a_number
read_bytes: 200
`

	w := httptest.NewRecorder()
	writeDiskIO(w, data)

	body := w.Body.String()

	// read_bytes should be present
	if !strings.Contains(body, "metrics_governor_disk_read_bytes_total 200") {
		t.Error("Expected disk_read_bytes metric")
	}
	// rchar should be present (valid number)
	if !strings.Contains(body, "metrics_governor_io_vfs_read_bytes_total 100") {
		t.Error("Expected io_vfs_read_bytes metric for rchar=100")
	}
	// wchar should NOT be present (non-numeric value)
	if strings.Contains(body, "metrics_governor_io_vfs_write_bytes_total") {
		t.Error("wchar with non-numeric value should not produce metric")
	}
}

// TestWriteDiskIO_MalformedData tests disk I/O with various malformed inputs.
func TestWriteDiskIO_MalformedData(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{"missing colons", "read_bytes 4096\nwrite_bytes 8192\n"},
		{"non-numeric values", "read_bytes: abc\nwrite_bytes: def\n"},
		{"empty values", "read_bytes: \nwrite_bytes: \n"},
		{"only whitespace", "   \n  \n"},
		{"extra colons", "read_bytes: 100: extra\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeDiskIO(w, tt.data)
			// Should not panic; output depends on parsing
		})
	}
}

// TestWriteDiskIOEmpty tests disk I/O with empty data.
func TestWriteDiskIOEmpty(t *testing.T) {
	w := httptest.NewRecorder()
	writeDiskIO(w, "")

	body := w.Body.String()
	if body != "" {
		t.Errorf("Expected empty output for empty data, got: %s", body)
	}
}

// TestWriteNetworkIO tests /proc/net/dev parsing.
func TestWriteNetworkIO(t *testing.T) {
	data := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 1000000  10000    0    0    0     0          0         0  1000000  10000    0    0    0     0       0          0
  eth0: 5000000  40000    5    2    0     0          0         0  3000000  25000    1    0    0     0       0          0
  eth1: 2000000  15000    0    1    0     0          0         0  1000000  8000     0    3    0     0       0          0
`

	w := httptest.NewRecorder()
	writeNetworkIO(w, data)

	body := w.Body.String()

	// lo should be excluded, only eth0 + eth1
	expected := []struct {
		metric string
		value  string
	}{
		{"metrics_governor_network_receive_bytes_total", "7000000"},  // 5000000 + 2000000
		{"metrics_governor_network_transmit_bytes_total", "4000000"}, // 3000000 + 1000000
		{"metrics_governor_network_receive_packets_total", "55000"},  // 40000 + 15000
		{"metrics_governor_network_transmit_packets_total", "33000"}, // 25000 + 8000
		{"metrics_governor_network_receive_errors_total", "5"},       // 5 + 0
		{"metrics_governor_network_transmit_errors_total", "1"},      // 1 + 0
		{"metrics_governor_network_receive_dropped_total", "3"},      // 2 + 1
		{"metrics_governor_network_transmit_dropped_total", "3"},     // 0 + 3
	}

	for _, exp := range expected {
		line := exp.metric + " " + exp.value
		if !strings.Contains(body, line) {
			t.Errorf("Expected output to contain %q, body:\n%s", line, body)
		}
	}
}

// TestWriteNetworkIOSkipsLoopback tests that loopback is excluded.
func TestWriteNetworkIOSkipsLoopback(t *testing.T) {
	data := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 9999999  99999    0    0    0     0          0         0  9999999  99999    0    0    0     0       0          0
`

	w := httptest.NewRecorder()
	writeNetworkIO(w, data)

	body := w.Body.String()

	// All counters should be 0 since only lo is present
	if !strings.Contains(body, "metrics_governor_network_receive_bytes_total 0") {
		t.Error("Expected receive_bytes=0 when only loopback is present")
	}
}

// TestWriteNetworkIOEmpty tests network I/O with empty data.
func TestWriteNetworkIOEmpty(t *testing.T) {
	w := httptest.NewRecorder()
	writeNetworkIO(w, "")

	body := w.Body.String()

	// Should still emit zero counters
	if !strings.Contains(body, "metrics_governor_network_receive_bytes_total 0") {
		t.Error("Expected zero counters for empty data")
	}
}

// TestWriteNetworkIOHeaderOnly tests with just headers, no interfaces.
func TestWriteNetworkIOHeaderOnly(t *testing.T) {
	data := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
`

	w := httptest.NewRecorder()
	writeNetworkIO(w, data)

	body := w.Body.String()

	if !strings.Contains(body, "metrics_governor_network_receive_bytes_total 0") {
		t.Error("Expected zero counters for header-only data")
	}
}

// TestWriteMemoryStatus tests /proc/self/status memory parsing.
func TestWriteMemoryStatus(t *testing.T) {
	data := `Name:	metrics-governor
Umask:	0022
State:	S (sleeping)
Tgid:	1
Pid:	1
VmPeak:	 1764000 kB
VmSize:	 1200000 kB
VmLck:	       0 kB
VmPin:	       0 kB
VmHWM:	  832000 kB
VmRSS:	  600000 kB
RssAnon:	  550000 kB
RssFile:	   45000 kB
RssShmem:	    5000 kB
VmData:	  400000 kB
VmStk:	     132 kB
VmExe:	     832 kB
VmLib:	     656 kB
VmPTE:	      44 kB
VmSwap:	    1024 kB
HugetlbPages:	       0 kB
Threads:	10
`

	w := httptest.NewRecorder()
	writeMemoryStatus(w, data)

	body := w.Body.String()

	expected := []struct {
		metric string
		value  string
	}{
		{"metrics_governor_os_memory_vm_peak_bytes", "1806336000"}, // 1764000 * 1024
		{"metrics_governor_os_memory_vm_size_bytes", "1228800000"}, // 1200000 * 1024
		{"metrics_governor_os_memory_vm_hwm_bytes", "851968000"},   // 832000 * 1024
		{"metrics_governor_os_memory_rss_bytes", "614400000"},      // 600000 * 1024
		{"metrics_governor_os_memory_rss_anon_bytes", "563200000"}, // 550000 * 1024
		{"metrics_governor_os_memory_rss_file_bytes", "46080000"},  // 45000 * 1024
		{"metrics_governor_os_memory_rss_shmem_bytes", "5120000"},  // 5000 * 1024
		{"metrics_governor_os_memory_vm_data_bytes", "409600000"},  // 400000 * 1024
		{"metrics_governor_os_memory_vm_stack_bytes", "135168"},    // 132 * 1024
		{"metrics_governor_os_memory_vm_swap_bytes", "1048576"},    // 1024 * 1024
	}

	for _, exp := range expected {
		line := exp.metric + " " + exp.value
		if !strings.Contains(body, line) {
			t.Errorf("Expected output to contain %q, got:\n%s", line, body)
		}
	}

	// All should be gauge type
	if !strings.Contains(body, "# TYPE metrics_governor_os_memory_rss_bytes gauge") {
		t.Error("Missing gauge TYPE for rss_bytes")
	}

	// Non-memory fields should not appear
	if strings.Contains(body, "Threads") || strings.Contains(body, "Name") {
		t.Error("Non-memory fields should not be emitted")
	}
}

// TestWriteMemoryStatusEmpty tests graceful handling of empty data.
func TestWriteMemoryStatusEmpty(t *testing.T) {
	w := httptest.NewRecorder()
	writeMemoryStatus(w, "")

	if w.Body.String() != "" {
		t.Error("Expected empty output for empty data")
	}
}

// TestWriteMemoryStatusMalformed tests various malformed inputs.
func TestWriteMemoryStatusMalformed(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{"no colon", "VmRSS 600000 kB"},
		{"no unit", "VmRSS:	600000"},
		{"non-numeric", "VmRSS:	not_a_number kB"},
		{"only spaces", "   "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeMemoryStatus(w, tt.data)
			// Should not panic
		})
	}
}

// TestWriteCgroupMemoryStat tests cgroup v2 memory.stat parsing.
func TestWriteCgroupMemoryStat(t *testing.T) {
	data := `anon 547655680
file 4382720
kernel 4333568
kernel_stack 147456
pagetables 3350528
sec_pagetables 0
percpu 35712
sock 262144
vmalloc 16384
shmem 0
zswap 0
zswapped 0
slab 731352
inactive_anon 133009408
active_anon 414580736
inactive_file 2908160
active_file 1478656
pgfault 40242894
pgmajfault 15
`

	w := httptest.NewRecorder()
	writeCgroupMemoryStat(w, data)

	body := w.Body.String()

	expected := []struct {
		metric string
		value  string
		mtype  string
	}{
		{"metrics_governor_cgroup_memory_anon_bytes", "547655680", "gauge"},
		{"metrics_governor_cgroup_memory_file_bytes", "4382720", "gauge"},
		{"metrics_governor_cgroup_memory_kernel_bytes", "4333568", "gauge"},
		{"metrics_governor_cgroup_memory_kernel_stack_bytes", "147456", "gauge"},
		{"metrics_governor_cgroup_memory_pagetables_bytes", "3350528", "gauge"},
		{"metrics_governor_cgroup_memory_slab_bytes", "731352", "gauge"},
		{"metrics_governor_cgroup_memory_sock_bytes", "262144", "gauge"},
		{"metrics_governor_cgroup_memory_inactive_anon_bytes", "133009408", "gauge"},
		{"metrics_governor_cgroup_memory_active_anon_bytes", "414580736", "gauge"},
		{"metrics_governor_cgroup_memory_inactive_file_bytes", "2908160", "gauge"},
		{"metrics_governor_cgroup_memory_active_file_bytes", "1478656", "gauge"},
		{"metrics_governor_cgroup_memory_pgfault_total", "40242894", "counter"},
		{"metrics_governor_cgroup_memory_pgmajfault_total", "15", "counter"},
	}

	for _, exp := range expected {
		line := exp.metric + " " + exp.value
		if !strings.Contains(body, line) {
			t.Errorf("Expected output to contain %q", line)
		}
		typeComment := "# TYPE " + exp.metric + " " + exp.mtype
		if !strings.Contains(body, typeComment) {
			t.Errorf("Expected %q", typeComment)
		}
	}

	// Fields we don't track should not appear
	if strings.Contains(body, "percpu") || strings.Contains(body, "vmalloc") {
		t.Error("Unexpected untracked field in output")
	}
}

// TestWriteCgroupMemoryStatEmpty tests graceful handling of empty data.
func TestWriteCgroupMemoryStatEmpty(t *testing.T) {
	w := httptest.NewRecorder()
	writeCgroupMemoryStat(w, "")

	if w.Body.String() != "" {
		t.Error("Expected empty output for empty data")
	}
}

// TestReadCgroupUint64 tests cgroup file reading helper.
func TestReadCgroupUint64(t *testing.T) {
	// Test "max" value (unlimited)
	tmpFile := t.TempDir() + "/memory.max"
	if err := os.WriteFile(tmpFile, []byte("max\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := readCgroupUint64(tmpFile)
	if err == nil {
		t.Error("Expected error for 'max' value")
	}

	// Test numeric value
	tmpFile2 := t.TempDir() + "/memory.current"
	if err := os.WriteFile(tmpFile2, []byte("3221225472\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	val, err := readCgroupUint64(tmpFile2)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if val != 3221225472 {
		t.Errorf("Expected 3221225472, got %d", val)
	}

	// Test non-existent file
	_, err = readCgroupUint64("/nonexistent/file")
	if err == nil {
		t.Error("Expected error for non-existent file")
	}
}
