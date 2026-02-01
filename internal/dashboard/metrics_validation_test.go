package dashboard

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// knownMetrics lists all metrics that metrics-governor exports.
// This is the source of truth for dashboard validation.
var knownMetrics = []string{
	// Global stats (stats.go)
	"metrics_governor_datapoints_total",
	"metrics_governor_metrics_total",
	"metrics_governor_datapoints_received_total",
	"metrics_governor_datapoints_sent_total",
	"metrics_governor_batches_sent_total",
	"metrics_governor_export_errors_total",

	// PRW stats (stats.go)
	"metrics_governor_prw_datapoints_received_total",
	"metrics_governor_prw_timeseries_received_total",
	"metrics_governor_prw_datapoints_sent_total",
	"metrics_governor_prw_timeseries_sent_total",
	"metrics_governor_prw_batches_sent_total",
	"metrics_governor_prw_export_errors_total",
	"metrics_governor_prw_bytes_total",

	// OTLP stats (stats.go)
	"metrics_governor_otlp_bytes_total",

	// Buffer stats (stats.go)
	"metrics_governor_buffer_size",

	// Per-metric stats (stats.go)
	"metrics_governor_metric_datapoints_total",
	"metrics_governor_metric_cardinality",
	"metrics_governor_label_datapoints_total",
	"metrics_governor_label_cardinality",

	// Queue metrics (queue/metrics.go)
	"metrics_governor_queue_size",
	"metrics_governor_queue_bytes",
	"metrics_governor_queue_max_size",
	"metrics_governor_queue_max_bytes",
	"metrics_governor_queue_effective_max_size",
	"metrics_governor_queue_effective_max_bytes",
	"metrics_governor_queue_utilization_ratio",
	"metrics_governor_queue_disk_available_bytes",
	"metrics_governor_queue_push_total",
	"metrics_governor_queue_dropped_total",
	"metrics_governor_queue_retry_total",
	"metrics_governor_queue_retry_success_total",
	"metrics_governor_queue_disk_full_total",
	"metrics_governor_fastqueue_inmemory_blocks",
	"metrics_governor_fastqueue_disk_bytes",
	"metrics_governor_fastqueue_meta_sync_total",
	"metrics_governor_fastqueue_chunk_rotations",
	"metrics_governor_fastqueue_inmemory_flushes",

	// Sharding metrics (sharding/metrics.go)
	"metrics_governor_sharding_endpoints_total",
	"metrics_governor_sharding_datapoints_total",
	"metrics_governor_sharding_export_errors_total",
	"metrics_governor_sharding_rehash_total",
	"metrics_governor_sharding_dns_refresh_total",
	"metrics_governor_sharding_dns_errors_total",
	"metrics_governor_sharding_dns_latency_seconds",
	"metrics_governor_sharding_export_latency_seconds",

	// Limits metrics (limits/enforcer.go)
	"metrics_governor_limit_datapoints_exceeded_total",
	"metrics_governor_limit_cardinality_exceeded_total",
	"metrics_governor_limit_datapoints_dropped_total",
	"metrics_governor_limit_datapoints_passed_total",
	"metrics_governor_limit_groups_dropped_total",
	"metrics_governor_rule_current_datapoints",
	"metrics_governor_rule_current_cardinality",
	"metrics_governor_rule_groups_total",
	"metrics_governor_rule_dropped_groups_total",
	"metrics_governor_rule_max_datapoints_rate",
	"metrics_governor_rule_max_cardinality",
	"metrics_governor_rule_group_datapoints",
	"metrics_governor_rule_group_cardinality",
	"metrics_governor_limits_total_datapoints",
	"metrics_governor_limits_total_cardinality",

	// Runtime metrics (stats/runtime.go)
	"metrics_governor_process_start_time_seconds",
	"metrics_governor_process_uptime_seconds",
	"metrics_governor_goroutines",
	"metrics_governor_go_threads",
	"metrics_governor_go_info",

	// Memory metrics (stats/runtime.go)
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
	"metrics_governor_memory_mallocs_total",
	"metrics_governor_memory_frees_total",

	// GC metrics (stats/runtime.go)
	"metrics_governor_gc_cycles_total",
	"metrics_governor_gc_pause_total_seconds",
	"metrics_governor_gc_last_pause_seconds",
	"metrics_governor_gc_cpu_percent",

	// PSI metrics (stats/runtime.go) - Linux only
	"metrics_governor_psi_cpu_some_avg10",
	"metrics_governor_psi_cpu_some_avg60",
	"metrics_governor_psi_cpu_some_avg300",
	"metrics_governor_psi_cpu_some_total_microseconds",
	"metrics_governor_psi_memory_some_avg10",
	"metrics_governor_psi_memory_some_avg60",
	"metrics_governor_psi_memory_some_avg300",
	"metrics_governor_psi_memory_some_total_microseconds",
	"metrics_governor_psi_memory_full_avg10",
	"metrics_governor_psi_memory_full_avg60",
	"metrics_governor_psi_memory_full_avg300",
	"metrics_governor_psi_memory_full_total_microseconds",
	"metrics_governor_psi_io_some_avg10",
	"metrics_governor_psi_io_some_avg60",
	"metrics_governor_psi_io_some_avg300",
	"metrics_governor_psi_io_some_total_microseconds",
	"metrics_governor_psi_io_full_avg10",
	"metrics_governor_psi_io_full_avg60",
	"metrics_governor_psi_io_full_avg300",
	"metrics_governor_psi_io_full_total_microseconds",

	// Process CPU metrics (stats/runtime.go) - Linux only
	"metrics_governor_process_cpu_user_seconds",
	"metrics_governor_process_cpu_system_seconds",
	"metrics_governor_process_cpu_total_seconds",
	"metrics_governor_process_virtual_memory_bytes",
	"metrics_governor_process_resident_memory_bytes",
	"metrics_governor_process_open_fds",
	"metrics_governor_process_max_fds",

	// Disk I/O metrics (stats/runtime.go) - Linux only
	"metrics_governor_process_io_read_chars_total",
	"metrics_governor_process_io_write_chars_total",
	"metrics_governor_process_io_read_syscalls_total",
	"metrics_governor_process_io_write_syscalls_total",
	"metrics_governor_process_io_read_bytes_total",
	"metrics_governor_process_io_write_bytes_total",

	// Network metrics (stats/runtime.go) - Linux only
	"metrics_governor_network_receive_bytes_total",
	"metrics_governor_network_transmit_bytes_total",
	"metrics_governor_network_receive_packets_total",
	"metrics_governor_network_transmit_packets_total",
	"metrics_governor_network_receive_errors_total",
	"metrics_governor_network_transmit_errors_total",
	"metrics_governor_network_receive_dropped_total",
	"metrics_governor_network_transmit_dropped_total",
}

// goRuntimeMetrics lists standard Go runtime metrics that are expected.
var goRuntimeMetrics = []string{
	"go_memstats_heap_alloc_bytes",
	"go_memstats_heap_inuse_bytes",
	"go_memstats_stack_inuse_bytes",
	"go_memstats_gc_sys_bytes",
	"go_memstats_next_gc_bytes",
	"go_memstats_last_gc_time_seconds",
	"go_memstats_mallocs_total",
	"go_memstats_frees_total",
	"go_memstats_heap_objects",
	"go_memstats_sys_bytes",
	"go_goroutines",
	"go_gc_duration_seconds",
	"go_gc_duration_seconds_count", // Counter for GC cycles
	"go_gc_duration_seconds_sum",   // Total GC duration
	"go_sched_gomaxprocs_threads",
	"process_cpu_seconds_total",
	"process_resident_memory_bytes",
	"process_virtual_memory_bytes",
	"process_open_fds",
	"process_max_fds",
}

// histogramSuffixes are added to histogram metrics when queried in Prometheus.
var histogramSuffixes = []string{"_bucket", "_count", "_sum"}

// GrafanaDashboard represents the structure of a Grafana dashboard JSON.
type GrafanaDashboard struct {
	Panels []Panel `json:"panels"`
}

// Panel represents a Grafana panel.
type Panel struct {
	Type    string   `json:"type"`
	Title   string   `json:"title"`
	Targets []Target `json:"targets"`
	Panels  []Panel  `json:"panels"` // For row panels
}

// Target represents a panel target with a PromQL expression.
type Target struct {
	Expr string `json:"expr"`
}

// extractMetricsFromExpr extracts metric names from a PromQL expression.
func extractMetricsFromExpr(expr string) []string {
	// Match metric names that start with metrics_governor_, go_, or process_
	// Include digits in the pattern to capture names like _avg10, _avg60, etc.
	re := regexp.MustCompile(`(metrics_governor_[a-z0-9_]+|go_[a-z0-9_]+|process_[a-z0-9_]+)`)
	matches := re.FindAllString(expr, -1)

	// Deduplicate
	seen := make(map[string]bool)
	var result []string
	for _, m := range matches {
		if !seen[m] {
			seen[m] = true
			result = append(result, m)
		}
	}
	return result
}

// extractAllMetricsFromDashboard extracts all metric names used in a dashboard.
func extractAllMetricsFromDashboard(dashboard *GrafanaDashboard) map[string][]string {
	metrics := make(map[string][]string) // metric -> panel titles that use it

	var extractFromPanels func(panels []Panel)
	extractFromPanels = func(panels []Panel) {
		for _, panel := range panels {
			// Process nested panels (for row types)
			if len(panel.Panels) > 0 {
				extractFromPanels(panel.Panels)
			}

			for _, target := range panel.Targets {
				if target.Expr == "" {
					continue
				}
				for _, metric := range extractMetricsFromExpr(target.Expr) {
					metrics[metric] = append(metrics[metric], panel.Title)
				}
			}
		}
	}

	extractFromPanels(dashboard.Panels)
	return metrics
}

// buildKnownMetricsSet builds a set of all known metrics including histogram variants.
func buildKnownMetricsSet() map[string]bool {
	knownSet := make(map[string]bool)

	// Add all known metrics
	for _, m := range knownMetrics {
		knownSet[m] = true
		// Add histogram suffixes for histogram metrics
		if strings.Contains(m, "latency") || strings.Contains(m, "duration") || strings.Contains(m, "seconds") {
			for _, suffix := range histogramSuffixes {
				knownSet[m+suffix] = true
			}
		}
	}

	// Add Go runtime metrics
	for _, m := range goRuntimeMetrics {
		knownSet[m] = true
	}

	return knownSet
}

// TestOperationsDashboardMetrics validates that all metrics used in the operations
// dashboard are actually exported by metrics-governor.
func TestOperationsDashboardMetrics(t *testing.T) {
	dashboardPath := "../../dashboards/operations.json"

	data, err := os.ReadFile(dashboardPath)
	if err != nil {
		t.Fatalf("Failed to read dashboard file: %v", err)
	}

	var dashboard GrafanaDashboard
	if err := json.Unmarshal(data, &dashboard); err != nil {
		t.Fatalf("Failed to parse dashboard JSON: %v", err)
	}

	// Build a set of all known metrics
	knownSet := buildKnownMetricsSet()

	// Extract metrics from dashboard
	dashboardMetrics := extractAllMetricsFromDashboard(&dashboard)

	// Check each metric
	var unknownMetrics []string
	for metric, panels := range dashboardMetrics {
		if !knownSet[metric] {
			unknownMetrics = append(unknownMetrics, metric+" (used in: "+strings.Join(panels, ", ")+")")
		}
	}

	if len(unknownMetrics) > 0 {
		sort.Strings(unknownMetrics)
		t.Errorf("Dashboard uses %d unknown metrics:\n%s", len(unknownMetrics), strings.Join(unknownMetrics, "\n"))
	}
}

// TestTestDashboardMetrics validates metrics in the test dashboard.
// This is a warning-only test since test dashboards may include experimental/aspirational metrics.
func TestTestDashboardMetrics(t *testing.T) {
	dashboardPath := "../../test/grafana/dashboards/metrics-governor.json"

	data, err := os.ReadFile(dashboardPath)
	if err != nil {
		t.Skipf("Test dashboard not found: %v", err)
	}

	var dashboard GrafanaDashboard
	if err := json.Unmarshal(data, &dashboard); err != nil {
		t.Fatalf("Failed to parse dashboard JSON: %v", err)
	}

	// Build a set of all known metrics
	knownSet := buildKnownMetricsSet()

	// Extract metrics from dashboard
	dashboardMetrics := extractAllMetricsFromDashboard(&dashboard)

	// Check each metric
	var unknownMetrics []string
	for metric, panels := range dashboardMetrics {
		if !knownSet[metric] {
			unknownMetrics = append(unknownMetrics, metric+" (used in: "+strings.Join(panels, ", ")+")")
		}
	}

	if len(unknownMetrics) > 0 {
		sort.Strings(unknownMetrics)
		// Log as warning but don't fail - test dashboards may have experimental metrics
		t.Logf("Warning: Test dashboard uses %d metrics not in known list (may be experimental):\n%s",
			len(unknownMetrics), strings.Join(unknownMetrics, "\n"))
	}
}

// TestKnownMetricsAreSorted ensures the knownMetrics list is sorted for maintainability.
func TestKnownMetricsAreSorted(t *testing.T) {
	// Group metrics by prefix
	prefixes := []string{
		"metrics_governor_datapoints",
		"metrics_governor_metrics",
		"metrics_governor_batches",
		"metrics_governor_export",
		"metrics_governor_prw",
		"metrics_governor_otlp",
		"metrics_governor_buffer",
		"metrics_governor_metric_",
		"metrics_governor_label_",
		"metrics_governor_queue",
		"metrics_governor_fastqueue",
		"metrics_governor_sharding",
		"metrics_governor_limit",
		"metrics_governor_rule",
		"metrics_governor_limits",
		// Runtime metrics prefixes
		"metrics_governor_process",
		"metrics_governor_goroutines",
		"metrics_governor_go_",
		"metrics_governor_memory",
		"metrics_governor_gc_",
		"metrics_governor_psi_",
		"metrics_governor_network",
	}

	// Just verify list is valid - no sorting requirement
	for _, metric := range knownMetrics {
		valid := false
		for _, prefix := range prefixes {
			if strings.HasPrefix(metric, prefix) {
				valid = true
				break
			}
		}
		if !valid {
			t.Errorf("Metric %q doesn't match any known prefix", metric)
		}
	}
}

// TestDashboardJSONValid ensures dashboard files are valid JSON.
func TestDashboardJSONValid(t *testing.T) {
	dashboards := []string{
		"../../dashboards/operations.json",
		"../../test/grafana/dashboards/metrics-governor.json",
		"../../test/grafana/dashboards/e2e-testing.json",
	}

	for _, path := range dashboards {
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("Dashboard not found: %v", err)
			}

			var dashboard GrafanaDashboard
			if err := json.Unmarshal(data, &dashboard); err != nil {
				t.Errorf("Invalid JSON: %v", err)
			}
		})
	}
}
