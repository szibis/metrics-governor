package stats

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// RuntimeStats collects Go runtime and process metrics.
type RuntimeStats struct {
	startTime time.Time
}

// NewRuntimeStats creates a new runtime stats collector.
func NewRuntimeStats() *RuntimeStats {
	return &RuntimeStats{
		startTime: time.Now(),
	}
}

// ServeHTTP writes runtime metrics in Prometheus format.
func (r *RuntimeStats) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Process info
	fmt.Fprintf(w, "# HELP metrics_governor_process_start_time_seconds Start time of the process since unix epoch in seconds\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_process_start_time_seconds gauge\n")
	fmt.Fprintf(w, "metrics_governor_process_start_time_seconds %d\n", r.startTime.Unix())

	fmt.Fprintf(w, "# HELP metrics_governor_process_uptime_seconds Time since process started in seconds\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_process_uptime_seconds gauge\n")
	fmt.Fprintf(w, "metrics_governor_process_uptime_seconds %.2f\n", time.Since(r.startTime).Seconds())

	// Goroutines
	fmt.Fprintf(w, "# HELP metrics_governor_goroutines Number of goroutines\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_goroutines gauge\n")
	fmt.Fprintf(w, "metrics_governor_goroutines %d\n", runtime.NumGoroutine())

	// CPU info
	fmt.Fprintf(w, "# HELP metrics_governor_go_threads Number of OS threads created\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_go_threads gauge\n")
	fmt.Fprintf(w, "metrics_governor_go_threads %d\n", runtime.GOMAXPROCS(0))

	// Memory - General
	fmt.Fprintf(w, "# HELP metrics_governor_memory_alloc_bytes Currently allocated memory in bytes\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_memory_alloc_bytes gauge\n")
	fmt.Fprintf(w, "metrics_governor_memory_alloc_bytes %d\n", m.Alloc)

	fmt.Fprintf(w, "# HELP metrics_governor_memory_total_alloc_bytes Total allocated memory over lifetime in bytes\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_memory_total_alloc_bytes counter\n")
	fmt.Fprintf(w, "metrics_governor_memory_total_alloc_bytes %d\n", m.TotalAlloc)

	fmt.Fprintf(w, "# HELP metrics_governor_memory_sys_bytes Total memory obtained from system in bytes\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_memory_sys_bytes gauge\n")
	fmt.Fprintf(w, "metrics_governor_memory_sys_bytes %d\n", m.Sys)

	// Memory - Heap
	fmt.Fprintf(w, "# HELP metrics_governor_memory_heap_alloc_bytes Heap memory allocated in bytes\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_memory_heap_alloc_bytes gauge\n")
	fmt.Fprintf(w, "metrics_governor_memory_heap_alloc_bytes %d\n", m.HeapAlloc)

	fmt.Fprintf(w, "# HELP metrics_governor_memory_heap_sys_bytes Heap memory obtained from system in bytes\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_memory_heap_sys_bytes gauge\n")
	fmt.Fprintf(w, "metrics_governor_memory_heap_sys_bytes %d\n", m.HeapSys)

	fmt.Fprintf(w, "# HELP metrics_governor_memory_heap_idle_bytes Heap memory waiting to be used in bytes\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_memory_heap_idle_bytes gauge\n")
	fmt.Fprintf(w, "metrics_governor_memory_heap_idle_bytes %d\n", m.HeapIdle)

	fmt.Fprintf(w, "# HELP metrics_governor_memory_heap_inuse_bytes Heap memory in use in bytes\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_memory_heap_inuse_bytes gauge\n")
	fmt.Fprintf(w, "metrics_governor_memory_heap_inuse_bytes %d\n", m.HeapInuse)

	fmt.Fprintf(w, "# HELP metrics_governor_memory_heap_released_bytes Heap memory released to OS in bytes\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_memory_heap_released_bytes gauge\n")
	fmt.Fprintf(w, "metrics_governor_memory_heap_released_bytes %d\n", m.HeapReleased)

	fmt.Fprintf(w, "# HELP metrics_governor_memory_heap_objects Number of allocated heap objects\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_memory_heap_objects gauge\n")
	fmt.Fprintf(w, "metrics_governor_memory_heap_objects %d\n", m.HeapObjects)

	// Memory - Stack
	fmt.Fprintf(w, "# HELP metrics_governor_memory_stack_inuse_bytes Stack memory in use in bytes\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_memory_stack_inuse_bytes gauge\n")
	fmt.Fprintf(w, "metrics_governor_memory_stack_inuse_bytes %d\n", m.StackInuse)

	fmt.Fprintf(w, "# HELP metrics_governor_memory_stack_sys_bytes Stack memory obtained from system in bytes\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_memory_stack_sys_bytes gauge\n")
	fmt.Fprintf(w, "metrics_governor_memory_stack_sys_bytes %d\n", m.StackSys)

	// GC Stats
	fmt.Fprintf(w, "# HELP metrics_governor_gc_cycles_total Total number of GC cycles completed\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_gc_cycles_total counter\n")
	fmt.Fprintf(w, "metrics_governor_gc_cycles_total %d\n", m.NumGC)

	fmt.Fprintf(w, "# HELP metrics_governor_gc_pause_total_seconds Total GC pause time in seconds\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_gc_pause_total_seconds counter\n")
	fmt.Fprintf(w, "metrics_governor_gc_pause_total_seconds %.6f\n", float64(m.PauseTotalNs)/1e9)

	// Last GC pause time
	if m.NumGC > 0 {
		lastPauseIdx := (m.NumGC + 255) % 256
		fmt.Fprintf(w, "# HELP metrics_governor_gc_last_pause_seconds Duration of the last GC pause in seconds\n")
		fmt.Fprintf(w, "# TYPE metrics_governor_gc_last_pause_seconds gauge\n")
		fmt.Fprintf(w, "metrics_governor_gc_last_pause_seconds %.6f\n", float64(m.PauseNs[lastPauseIdx])/1e9)
	}

	fmt.Fprintf(w, "# HELP metrics_governor_gc_cpu_percent Percentage of CPU used by GC\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_gc_cpu_percent gauge\n")
	fmt.Fprintf(w, "metrics_governor_gc_cpu_percent %.2f\n", m.GCCPUFraction*100)

	// Memory - Other
	fmt.Fprintf(w, "# HELP metrics_governor_memory_mallocs_total Total number of mallocs\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_memory_mallocs_total counter\n")
	fmt.Fprintf(w, "metrics_governor_memory_mallocs_total %d\n", m.Mallocs)

	fmt.Fprintf(w, "# HELP metrics_governor_memory_frees_total Total number of frees\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_memory_frees_total counter\n")
	fmt.Fprintf(w, "metrics_governor_memory_frees_total %d\n", m.Frees)

	// Go version info
	fmt.Fprintf(w, "# HELP metrics_governor_go_info Go version information\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_go_info gauge\n")
	fmt.Fprintf(w, "metrics_governor_go_info{version=%q} 1\n", runtime.Version())

	// OS-level process memory (Linux only — /proc/self/status)
	r.writeProcessMemoryStatus(w)

	// Container/cgroup memory (Linux only — /sys/fs/cgroup/)
	r.writeCgroupMemoryMetrics(w)

	// PSI metrics (Linux only)
	r.writePSIMetrics(w)

	// Process CPU time (Linux only)
	r.writeProcessCPUMetrics(w)
}

// writePSIMetrics writes PSI (Pressure Stall Information) metrics if available.
// PSI is a Linux feature that provides information about resource pressure.
func (r *RuntimeStats) writePSIMetrics(w http.ResponseWriter) {
	// PSI is only available on Linux
	if runtime.GOOS != "linux" {
		return
	}

	// Try to read PSI files
	psiTypes := []struct {
		resource string
		path     string
	}{
		{"cpu", "/proc/pressure/cpu"},
		{"memory", "/proc/pressure/memory"},
		{"io", "/proc/pressure/io"},
	}

	for _, psi := range psiTypes {
		metrics, err := parsePSIFile(psi.path)
		if err != nil {
			continue // PSI not available or not accessible
		}
		writePSIResource(w, psi.resource, metrics)
	}
}

// writePSIResource writes parsed PSI metrics for a single resource type.
func writePSIResource(w http.ResponseWriter, resource string, metrics map[string]*psiMetric) {
	// Write some metrics
	if some, ok := metrics["some"]; ok {
		fmt.Fprintf(w, "# HELP metrics_governor_psi_%s_some_avg10 PSI %s some average over 10 seconds\n", resource, resource)
		fmt.Fprintf(w, "# TYPE metrics_governor_psi_%s_some_avg10 gauge\n", resource)
		fmt.Fprintf(w, "metrics_governor_psi_%s_some_avg10 %.2f\n", resource, some.Avg10)

		fmt.Fprintf(w, "# HELP metrics_governor_psi_%s_some_avg60 PSI %s some average over 60 seconds\n", resource, resource)
		fmt.Fprintf(w, "# TYPE metrics_governor_psi_%s_some_avg60 gauge\n", resource)
		fmt.Fprintf(w, "metrics_governor_psi_%s_some_avg60 %.2f\n", resource, some.Avg60)

		fmt.Fprintf(w, "# HELP metrics_governor_psi_%s_some_avg300 PSI %s some average over 300 seconds\n", resource, resource)
		fmt.Fprintf(w, "# TYPE metrics_governor_psi_%s_some_avg300 gauge\n", resource)
		fmt.Fprintf(w, "metrics_governor_psi_%s_some_avg300 %.2f\n", resource, some.Avg300)

		fmt.Fprintf(w, "# HELP metrics_governor_psi_%s_some_total_microseconds PSI %s some total stall time in microseconds\n", resource, resource)
		fmt.Fprintf(w, "# TYPE metrics_governor_psi_%s_some_total_microseconds counter\n", resource)
		fmt.Fprintf(w, "metrics_governor_psi_%s_some_total_microseconds %d\n", resource, some.Total)
	}

	// Write full metrics (memory and io only)
	if full, ok := metrics["full"]; ok {
		fmt.Fprintf(w, "# HELP metrics_governor_psi_%s_full_avg10 PSI %s full average over 10 seconds\n", resource, resource)
		fmt.Fprintf(w, "# TYPE metrics_governor_psi_%s_full_avg10 gauge\n", resource)
		fmt.Fprintf(w, "metrics_governor_psi_%s_full_avg10 %.2f\n", resource, full.Avg10)

		fmt.Fprintf(w, "# HELP metrics_governor_psi_%s_full_avg60 PSI %s full average over 60 seconds\n", resource, resource)
		fmt.Fprintf(w, "# TYPE metrics_governor_psi_%s_full_avg60 gauge\n", resource)
		fmt.Fprintf(w, "metrics_governor_psi_%s_full_avg60 %.2f\n", resource, full.Avg60)

		fmt.Fprintf(w, "# HELP metrics_governor_psi_%s_full_avg300 PSI %s full average over 300 seconds\n", resource, resource)
		fmt.Fprintf(w, "# TYPE metrics_governor_psi_%s_full_avg300 gauge\n", resource)
		fmt.Fprintf(w, "metrics_governor_psi_%s_full_avg300 %.2f\n", resource, full.Avg300)

		fmt.Fprintf(w, "# HELP metrics_governor_psi_%s_full_total_microseconds PSI %s full total stall time in microseconds\n", resource, resource)
		fmt.Fprintf(w, "# TYPE metrics_governor_psi_%s_full_total_microseconds counter\n", resource)
		fmt.Fprintf(w, "metrics_governor_psi_%s_full_total_microseconds %d\n", resource, full.Total)
	}
}

// psiMetric holds parsed PSI values.
type psiMetric struct {
	Avg10  float64
	Avg60  float64
	Avg300 float64
	Total  uint64
}

// parsePSIFile parses a PSI file and returns metrics.
// Format: some avg10=0.00 avg60=0.00 avg300=0.00 total=0
func parsePSIFile(path string) (map[string]*psiMetric, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	result := make(map[string]*psiMetric)
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 5 {
			continue
		}

		metricType := parts[0] // "some" or "full"
		metric := &psiMetric{}

		for _, part := range parts[1:] {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) != 2 {
				continue
			}
			switch kv[0] {
			case "avg10":
				metric.Avg10, _ = strconv.ParseFloat(kv[1], 64)
			case "avg60":
				metric.Avg60, _ = strconv.ParseFloat(kv[1], 64)
			case "avg300":
				metric.Avg300, _ = strconv.ParseFloat(kv[1], 64)
			case "total":
				metric.Total, _ = strconv.ParseUint(kv[1], 10, 64)
			}
		}

		result[metricType] = metric
	}

	return result, scanner.Err()
}

// writeProcessCPUMetrics writes process CPU metrics from /proc/self/stat.
func (r *RuntimeStats) writeProcessCPUMetrics(w http.ResponseWriter) {
	// Only available on Linux
	if runtime.GOOS != "linux" {
		return
	}

	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return
	}

	writeProcessStat(w, string(data))

	// Try to get open file descriptors
	if fds, err := os.ReadDir("/proc/self/fd"); err == nil {
		fmt.Fprintf(w, "# HELP metrics_governor_process_open_fds Number of open file descriptors\n")
		fmt.Fprintf(w, "# TYPE metrics_governor_process_open_fds gauge\n")
		fmt.Fprintf(w, "metrics_governor_process_open_fds %d\n", len(fds))
	}

	// Try to get max file descriptors
	if data, err := os.ReadFile("/proc/self/limits"); err == nil {
		writeMaxFDs(w, string(data))
	}

	// Disk I/O from /proc/self/io
	r.writeDiskIOMetrics(w)

	// Network I/O from /proc/net/dev
	r.writeNetworkIOMetrics(w)
}

// writeProcessStat parses /proc/self/stat data and writes CPU and memory metrics.
func writeProcessStat(w http.ResponseWriter, data string) {
	// Parse /proc/self/stat - fields are space separated
	// Field 14 = utime (user mode jiffies)
	// Field 15 = stime (kernel mode jiffies)
	// Field 23 = vsize (virtual memory size)
	// Field 24 = rss (resident set size in pages)
	fields := strings.Fields(data)
	if len(fields) < 24 {
		return
	}

	utime, _ := strconv.ParseUint(fields[13], 10, 64)
	stime, _ := strconv.ParseUint(fields[14], 10, 64)
	vsize, _ := strconv.ParseUint(fields[22], 10, 64)
	rss, _ := strconv.ParseInt(fields[23], 10, 64)

	clockTick := float64(100)

	fmt.Fprintf(w, "# HELP metrics_governor_process_cpu_user_seconds Total user CPU time in seconds\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_process_cpu_user_seconds counter\n")
	fmt.Fprintf(w, "metrics_governor_process_cpu_user_seconds %.2f\n", float64(utime)/clockTick)

	fmt.Fprintf(w, "# HELP metrics_governor_process_cpu_system_seconds Total system CPU time in seconds\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_process_cpu_system_seconds counter\n")
	fmt.Fprintf(w, "metrics_governor_process_cpu_system_seconds %.2f\n", float64(stime)/clockTick)

	fmt.Fprintf(w, "# HELP metrics_governor_process_cpu_total_seconds Total CPU time (user + system) in seconds\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_process_cpu_total_seconds counter\n")
	fmt.Fprintf(w, "metrics_governor_process_cpu_total_seconds %.2f\n", float64(utime+stime)/clockTick)

	fmt.Fprintf(w, "# HELP metrics_governor_process_virtual_memory_bytes Virtual memory size in bytes\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_process_virtual_memory_bytes gauge\n")
	fmt.Fprintf(w, "metrics_governor_process_virtual_memory_bytes %d\n", vsize)

	pageSize := int64(os.Getpagesize())
	fmt.Fprintf(w, "# HELP metrics_governor_process_resident_memory_bytes Resident memory size in bytes\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_process_resident_memory_bytes gauge\n")
	fmt.Fprintf(w, "metrics_governor_process_resident_memory_bytes %d\n", rss*pageSize)
}

// writeMaxFDs parses /proc/self/limits data and writes max FD metric.
func writeMaxFDs(w http.ResponseWriter, data string) {
	lines := strings.Split(data, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "Max open files") {
			fields := strings.Fields(line)
			if len(fields) >= 4 {
				if maxFds, err := strconv.ParseUint(fields[3], 10, 64); err == nil {
					fmt.Fprintf(w, "# HELP metrics_governor_process_max_fds Maximum number of open file descriptors\n")
					fmt.Fprintf(w, "# TYPE metrics_governor_process_max_fds gauge\n")
					fmt.Fprintf(w, "metrics_governor_process_max_fds %d\n", maxFds)
				}
			}
			break
		}
	}
}

// writeDiskIOMetrics writes process disk I/O metrics from /proc/self/io.
func (r *RuntimeStats) writeDiskIOMetrics(w http.ResponseWriter) {
	if runtime.GOOS != "linux" {
		return
	}

	data, err := os.ReadFile("/proc/self/io")
	if err != nil {
		return
	}

	writeDiskIO(w, string(data))
}

// writeDiskIO parses /proc/self/io data and writes I/O metrics at multiple layers:
// - read_bytes/write_bytes: actual storage-layer I/O (bypasses page cache)
// - rchar/wchar: VFS-level bytes (includes cache, sockets, pipes)
// - syscr/syscw: total read/write system call counts
func writeDiskIO(w http.ResponseWriter, data string) {
	lines := strings.Split(data, "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value, err := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil {
			continue
		}

		switch key {
		case "read_bytes":
			fmt.Fprintf(w, "# HELP metrics_governor_disk_read_bytes_total Actual bytes read from storage by the process\n")
			fmt.Fprintf(w, "# TYPE metrics_governor_disk_read_bytes_total counter\n")
			fmt.Fprintf(w, "metrics_governor_disk_read_bytes_total %d\n", value)
		case "write_bytes":
			fmt.Fprintf(w, "# HELP metrics_governor_disk_write_bytes_total Actual bytes written to storage by the process\n")
			fmt.Fprintf(w, "# TYPE metrics_governor_disk_write_bytes_total counter\n")
			fmt.Fprintf(w, "metrics_governor_disk_write_bytes_total %d\n", value)
		case "syscr":
			fmt.Fprintf(w, "# HELP metrics_governor_io_read_ops_total Total read system calls by the process\n")
			fmt.Fprintf(w, "# TYPE metrics_governor_io_read_ops_total counter\n")
			fmt.Fprintf(w, "metrics_governor_io_read_ops_total %d\n", value)
		case "syscw":
			fmt.Fprintf(w, "# HELP metrics_governor_io_write_ops_total Total write system calls by the process\n")
			fmt.Fprintf(w, "# TYPE metrics_governor_io_write_ops_total counter\n")
			fmt.Fprintf(w, "metrics_governor_io_write_ops_total %d\n", value)
		case "rchar":
			fmt.Fprintf(w, "# HELP metrics_governor_io_vfs_read_bytes_total VFS-level bytes read (includes cache, sockets, pipes)\n")
			fmt.Fprintf(w, "# TYPE metrics_governor_io_vfs_read_bytes_total counter\n")
			fmt.Fprintf(w, "metrics_governor_io_vfs_read_bytes_total %d\n", value)
		case "wchar":
			fmt.Fprintf(w, "# HELP metrics_governor_io_vfs_write_bytes_total VFS-level bytes written (includes cache, sockets, pipes)\n")
			fmt.Fprintf(w, "# TYPE metrics_governor_io_vfs_write_bytes_total counter\n")
			fmt.Fprintf(w, "metrics_governor_io_vfs_write_bytes_total %d\n", value)
		}
	}
}

// writeNetworkIOMetrics writes network I/O metrics from /proc/net/dev.
func (r *RuntimeStats) writeNetworkIOMetrics(w http.ResponseWriter) {
	if runtime.GOOS != "linux" {
		return
	}

	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return
	}

	writeNetworkIO(w, string(data))
}

// writeNetworkIO parses /proc/net/dev data and writes network I/O metrics.
func writeNetworkIO(w http.ResponseWriter, data string) {
	var totalRxBytes, totalTxBytes, totalRxPackets, totalTxPackets uint64
	var totalRxErrors, totalTxErrors, totalRxDropped, totalTxDropped uint64

	lines := strings.Split(data, "\n")
	for _, line := range lines {
		// Skip header lines
		if strings.Contains(line, "|") || strings.TrimSpace(line) == "" {
			continue
		}

		// Split by : to get interface name and stats
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		iface := strings.TrimSpace(parts[0])
		// Skip loopback
		if iface == "lo" {
			continue
		}

		fields := strings.Fields(parts[1])
		if len(fields) < 16 {
			continue
		}

		// Receive: bytes packets errs drop fifo frame compressed multicast
		rxBytes, _ := strconv.ParseUint(fields[0], 10, 64)
		rxPackets, _ := strconv.ParseUint(fields[1], 10, 64)
		rxErrors, _ := strconv.ParseUint(fields[2], 10, 64)
		rxDropped, _ := strconv.ParseUint(fields[3], 10, 64)

		// Transmit: bytes packets errs drop fifo colls carrier compressed
		txBytes, _ := strconv.ParseUint(fields[8], 10, 64)
		txPackets, _ := strconv.ParseUint(fields[9], 10, 64)
		txErrors, _ := strconv.ParseUint(fields[10], 10, 64)
		txDropped, _ := strconv.ParseUint(fields[11], 10, 64)

		totalRxBytes += rxBytes
		totalTxBytes += txBytes
		totalRxPackets += rxPackets
		totalTxPackets += txPackets
		totalRxErrors += rxErrors
		totalTxErrors += txErrors
		totalRxDropped += rxDropped
		totalTxDropped += txDropped
	}

	fmt.Fprintf(w, "# HELP metrics_governor_network_receive_bytes_total Total bytes received\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_network_receive_bytes_total counter\n")
	fmt.Fprintf(w, "metrics_governor_network_receive_bytes_total %d\n", totalRxBytes)

	fmt.Fprintf(w, "# HELP metrics_governor_network_transmit_bytes_total Total bytes transmitted\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_network_transmit_bytes_total counter\n")
	fmt.Fprintf(w, "metrics_governor_network_transmit_bytes_total %d\n", totalTxBytes)

	fmt.Fprintf(w, "# HELP metrics_governor_network_receive_packets_total Total packets received\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_network_receive_packets_total counter\n")
	fmt.Fprintf(w, "metrics_governor_network_receive_packets_total %d\n", totalRxPackets)

	fmt.Fprintf(w, "# HELP metrics_governor_network_transmit_packets_total Total packets transmitted\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_network_transmit_packets_total counter\n")
	fmt.Fprintf(w, "metrics_governor_network_transmit_packets_total %d\n", totalTxPackets)

	fmt.Fprintf(w, "# HELP metrics_governor_network_receive_errors_total Total receive errors\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_network_receive_errors_total counter\n")
	fmt.Fprintf(w, "metrics_governor_network_receive_errors_total %d\n", totalRxErrors)

	fmt.Fprintf(w, "# HELP metrics_governor_network_transmit_errors_total Total transmit errors\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_network_transmit_errors_total counter\n")
	fmt.Fprintf(w, "metrics_governor_network_transmit_errors_total %d\n", totalTxErrors)

	fmt.Fprintf(w, "# HELP metrics_governor_network_receive_dropped_total Total receive packets dropped\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_network_receive_dropped_total counter\n")
	fmt.Fprintf(w, "metrics_governor_network_receive_dropped_total %d\n", totalRxDropped)

	fmt.Fprintf(w, "# HELP metrics_governor_network_transmit_dropped_total Total transmit packets dropped\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_network_transmit_dropped_total counter\n")
	fmt.Fprintf(w, "metrics_governor_network_transmit_dropped_total %d\n", totalTxDropped)
}

// writeProcessMemoryStatus writes real OS-level memory metrics from /proc/self/status.
// These reflect the kernel's view of the process, not Go's internal bookkeeping.
func (r *RuntimeStats) writeProcessMemoryStatus(w http.ResponseWriter) {
	if runtime.GOOS != "linux" {
		return
	}

	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return
	}

	writeMemoryStatus(w, string(data))
}

// writeMemoryStatus parses /proc/self/status and writes real memory metrics.
// Fields are in kB and need conversion to bytes.
func writeMemoryStatus(w http.ResponseWriter, data string) {
	fields := map[string]string{
		"VmPeak":   "metrics_governor_os_memory_vm_peak_bytes",
		"VmSize":   "metrics_governor_os_memory_vm_size_bytes",
		"VmHWM":    "metrics_governor_os_memory_vm_hwm_bytes",
		"VmRSS":    "metrics_governor_os_memory_rss_bytes",
		"RssAnon":  "metrics_governor_os_memory_rss_anon_bytes",
		"RssFile":  "metrics_governor_os_memory_rss_file_bytes",
		"RssShmem": "metrics_governor_os_memory_rss_shmem_bytes",
		"VmData":   "metrics_governor_os_memory_vm_data_bytes",
		"VmStk":    "metrics_governor_os_memory_vm_stack_bytes",
		"VmSwap":   "metrics_governor_os_memory_vm_swap_bytes",
	}

	help := map[string]string{
		"VmPeak":   "Peak virtual memory size (high-water mark)",
		"VmSize":   "Current virtual memory size",
		"VmHWM":    "Peak resident set size (high-water mark)",
		"VmRSS":    "Current resident set size (physical memory)",
		"RssAnon":  "Anonymous RSS (heap, stack, mmap'd private)",
		"RssFile":  "File-backed RSS (shared libraries, mapped files)",
		"RssShmem": "Shared memory RSS",
		"VmData":   "Data + stack segment size",
		"VmStk":    "Stack segment size",
		"VmSwap":   "Swap usage",
	}

	for _, line := range strings.Split(data, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		metricName, ok := fields[key]
		if !ok {
			continue
		}

		// Value is like "12345 kB"
		valStr := strings.TrimSpace(parts[1])
		valStr = strings.TrimSuffix(valStr, " kB")
		valStr = strings.TrimSpace(valStr)
		kbVal, err := strconv.ParseUint(valStr, 10, 64)
		if err != nil {
			continue
		}
		bytesVal := kbVal * 1024

		fmt.Fprintf(w, "# HELP %s %s\n", metricName, help[key])
		fmt.Fprintf(w, "# TYPE %s gauge\n", metricName)
		fmt.Fprintf(w, "%s %d\n", metricName, bytesVal)
	}
}

// writeCgroupMemoryMetrics writes container-level memory metrics from cgroup v2.
// These are what Docker/Kubernetes actually use for limits and OOM decisions.
func (r *RuntimeStats) writeCgroupMemoryMetrics(w http.ResponseWriter) {
	if runtime.GOOS != "linux" {
		return
	}

	// memory.current — what Docker reports as container memory usage
	if val, err := readCgroupUint64("/sys/fs/cgroup/memory.current"); err == nil {
		fmt.Fprintf(w, "# HELP metrics_governor_cgroup_memory_current_bytes Current cgroup memory usage (what Docker reports)\n")
		fmt.Fprintf(w, "# TYPE metrics_governor_cgroup_memory_current_bytes gauge\n")
		fmt.Fprintf(w, "metrics_governor_cgroup_memory_current_bytes %d\n", val)
	}

	// memory.max — the container memory limit
	if val, err := readCgroupUint64("/sys/fs/cgroup/memory.max"); err == nil {
		fmt.Fprintf(w, "# HELP metrics_governor_cgroup_memory_limit_bytes Cgroup memory limit (container memory limit)\n")
		fmt.Fprintf(w, "# TYPE metrics_governor_cgroup_memory_limit_bytes gauge\n")
		fmt.Fprintf(w, "metrics_governor_cgroup_memory_limit_bytes %d\n", val)
	}

	// memory.peak — highest memory.current ever reached
	if val, err := readCgroupUint64("/sys/fs/cgroup/memory.peak"); err == nil {
		fmt.Fprintf(w, "# HELP metrics_governor_cgroup_memory_peak_bytes Peak cgroup memory usage (high-water mark)\n")
		fmt.Fprintf(w, "# TYPE metrics_governor_cgroup_memory_peak_bytes gauge\n")
		fmt.Fprintf(w, "metrics_governor_cgroup_memory_peak_bytes %d\n", val)
	}

	// memory.swap.current — swap usage
	if val, err := readCgroupUint64("/sys/fs/cgroup/memory.swap.current"); err == nil {
		fmt.Fprintf(w, "# HELP metrics_governor_cgroup_swap_current_bytes Current cgroup swap usage\n")
		fmt.Fprintf(w, "# TYPE metrics_governor_cgroup_swap_current_bytes gauge\n")
		fmt.Fprintf(w, "metrics_governor_cgroup_swap_current_bytes %d\n", val)
	}

	// memory.stat — detailed breakdown
	data, err := os.ReadFile("/sys/fs/cgroup/memory.stat")
	if err != nil {
		return
	}

	writeCgroupMemoryStat(w, string(data))
}

// writeCgroupMemoryStat parses cgroup memory.stat and writes key fields.
func writeCgroupMemoryStat(w http.ResponseWriter, data string) {
	fields := map[string]string{
		"anon":          "metrics_governor_cgroup_memory_anon_bytes",
		"file":          "metrics_governor_cgroup_memory_file_bytes",
		"kernel":        "metrics_governor_cgroup_memory_kernel_bytes",
		"kernel_stack":  "metrics_governor_cgroup_memory_kernel_stack_bytes",
		"pagetables":    "metrics_governor_cgroup_memory_pagetables_bytes",
		"slab":          "metrics_governor_cgroup_memory_slab_bytes",
		"sock":          "metrics_governor_cgroup_memory_sock_bytes",
		"inactive_anon": "metrics_governor_cgroup_memory_inactive_anon_bytes",
		"active_anon":   "metrics_governor_cgroup_memory_active_anon_bytes",
		"inactive_file": "metrics_governor_cgroup_memory_inactive_file_bytes",
		"active_file":   "metrics_governor_cgroup_memory_active_file_bytes",
		"pgfault":       "metrics_governor_cgroup_memory_pgfault_total",
		"pgmajfault":    "metrics_governor_cgroup_memory_pgmajfault_total",
	}

	help := map[string]string{
		"anon":          "Anonymous memory (heap, stack, mmap private)",
		"file":          "File-backed memory (page cache)",
		"kernel":        "Kernel memory (slab, stack, pagetables)",
		"kernel_stack":  "Kernel stack memory",
		"pagetables":    "Page table memory",
		"slab":          "Slab allocator memory",
		"sock":          "Socket buffer memory",
		"inactive_anon": "Inactive anonymous pages (reclaimable under pressure)",
		"active_anon":   "Active anonymous pages",
		"inactive_file": "Inactive file pages (easily reclaimable)",
		"active_file":   "Active file pages",
		"pgfault":       "Total page faults",
		"pgmajfault":    "Total major page faults (required disk I/O)",
	}

	metricType := map[string]string{
		"pgfault":    "counter",
		"pgmajfault": "counter",
	}

	for _, line := range strings.Split(data, "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		metricName, ok := fields[key]
		if !ok {
			continue
		}

		val, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			continue
		}

		mType := "gauge"
		if t, ok := metricType[key]; ok {
			mType = t
		}

		fmt.Fprintf(w, "# HELP %s %s\n", metricName, help[key])
		fmt.Fprintf(w, "# TYPE %s %s\n", metricName, mType)
		fmt.Fprintf(w, "%s %d\n", metricName, val)
	}
}

// readCgroupUint64 reads a single uint64 value from a cgroup file.
// Returns an error for "max" (unlimited) values.
func readCgroupUint64(path string) (uint64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(data))
	if s == "max" {
		return 0, fmt.Errorf("unlimited")
	}
	return strconv.ParseUint(s, 10, 64)
}
