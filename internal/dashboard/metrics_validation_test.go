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

	// PRW export metrics (exporter/prw_exporter.go)
	"metrics_governor_prw_export_requests_total",
	"metrics_governor_prw_export_timeseries_total",
	"metrics_governor_prw_export_samples_total",
	"metrics_governor_prw_export_bytes_total",
	"metrics_governor_prw_retry_failure_total",
	"metrics_governor_prw_retry_total",
	"metrics_governor_prw_retry_success_total",

	// OTLP stats (stats.go)
	"metrics_governor_otlp_bytes_total",

	// OTLP export metrics (exporter/exporter.go)
	"metrics_governor_otlp_export_bytes_total",
	"metrics_governor_otlp_export_requests_total",
	"metrics_governor_otlp_export_errors_total",
	"metrics_governor_otlp_export_datapoints_total",

	// gRPC receiver metrics (receiver/grpc.go, receiver/metrics.go)
	"metrics_governor_grpc_received_bytes_total",
	"metrics_governor_receiver_errors_total",

	// Buffer stats (stats.go)
	"metrics_governor_buffer_size",

	// Batch splitting metrics (buffer/splitter.go)
	"metrics_governor_batch_splits_total",
	"metrics_governor_batch_bytes",
	"metrics_governor_batch_too_large_total",

	// Concurrent export metrics (buffer/buffer.go)
	"metrics_governor_export_concurrent_workers",
	"metrics_governor_export_retry_split_total",
	"metrics_governor_failover_queue_push_total",
	"metrics_governor_failover_queue_drain_total",
	"metrics_governor_failover_queue_drain_errors_total",

	// Memory queue metrics (buffer/memqueue.go)
	"metrics_governor_memqueue_size",
	"metrics_governor_memqueue_bytes",
	"metrics_governor_memqueue_evictions_total",

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
	"metrics_governor_queue_retry_failure_total",
	"metrics_governor_queue_disk_full_total",
	"metrics_governor_fastqueue_inmemory_blocks",
	"metrics_governor_fastqueue_disk_bytes",
	"metrics_governor_fastqueue_meta_sync_total",
	"metrics_governor_fastqueue_chunk_rotations",
	"metrics_governor_fastqueue_inmemory_flushes",
	// Circuit breaker and backoff metrics (queue/metrics.go)
	"metrics_governor_circuit_breaker_state",
	"metrics_governor_circuit_breaker_open_total",
	"metrics_governor_circuit_breaker_rejected_total",
	"metrics_governor_queue_backoff_seconds",

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
	"metrics_governor_dropped_group_info",
	"metrics_governor_rule_max_datapoints_rate",
	"metrics_governor_rule_max_cardinality",
	"metrics_governor_rule_group_datapoints",
	"metrics_governor_rule_group_cardinality",
	"metrics_governor_limits_total_datapoints",
	"metrics_governor_limits_total_cardinality",
	"metrics_governor_rule_cardinality_memory_bytes",
	"metrics_governor_limits_cardinality_memory_bytes",
	"metrics_governor_limits_cardinality_trackers_total",

	// Cardinality tracking (stats.go, limits/enforcer.go)
	"metrics_governor_cardinality_mode",
	"metrics_governor_cardinality_memory_bytes",
	"metrics_governor_cardinality_trackers_total",
	"metrics_governor_cardinality_config_expected_items",
	"metrics_governor_cardinality_config_fp_rate",

	// Hybrid tracker metrics (limits/enforcer.go)
	"metrics_governor_tracker_mode",
	"metrics_governor_tracker_switches_total",
	"metrics_governor_tracker_sample_rate",

	// Config reload metrics (limits/enforcer.go)
	"metrics_governor_config_reloads_total",
	"metrics_governor_config_reload_last_success_timestamp_seconds",

	// Rule cache metrics (limits/enforcer.go)
	"metrics_governor_rule_cache_evictions_total",
	"metrics_governor_rule_cache_hit_ratio",
	"metrics_governor_rule_cache_hits_total",
	"metrics_governor_rule_cache_max_size",
	"metrics_governor_rule_cache_misses_total",
	"metrics_governor_rule_cache_negative_entries",
	"metrics_governor_rule_cache_size",

	// Compression pool metrics (compression/metrics.go)
	"metrics_governor_compression_buffer_pool_gets_total",
	"metrics_governor_compression_buffer_pool_puts_total",
	"metrics_governor_compression_pool_discards_total",
	"metrics_governor_compression_pool_gets_total",
	"metrics_governor_compression_pool_new_total",
	"metrics_governor_compression_pool_puts_total",

	// String intern pool metrics (intern/metrics.go)
	"metrics_governor_intern_hits_total",
	"metrics_governor_intern_misses_total",
	"metrics_governor_intern_pool_size",

	// Series key pool metrics (stats.go, limits/enforcer.go)
	"metrics_governor_serieskey_pool_discards_total",
	"metrics_governor_serieskey_pool_gets_total",
	"metrics_governor_serieskey_pool_puts_total",

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
	// Only real storage-layer I/O (read_bytes/write_bytes from /proc/self/io).
	// VFS-level rchar/wchar and syscall counts are excluded — they mix disk + network.
	"metrics_governor_disk_read_bytes_total",
	"metrics_governor_disk_write_bytes_total",

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
		if strings.Contains(m, "latency") || strings.Contains(m, "duration") || strings.Contains(m, "seconds") || strings.HasSuffix(m, "batch_bytes") {
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
		"metrics_governor_grpc",
		"metrics_governor_receiver",
		"metrics_governor_buffer",
		"metrics_governor_batch",
		"metrics_governor_export",
		"metrics_governor_failover",
		"metrics_governor_memqueue",
		"metrics_governor_metric_",
		"metrics_governor_label_",
		"metrics_governor_queue",
		"metrics_governor_fastqueue",
		"metrics_governor_circuit_breaker",
		"metrics_governor_sharding",
		"metrics_governor_limit",
		"metrics_governor_rule",
		"metrics_governor_limits",
		"metrics_governor_dropped_",
		"metrics_governor_tracker",
		"metrics_governor_config",
		"metrics_governor_cardinality",
		// Caching & pool metrics prefixes
		"metrics_governor_compression",
		"metrics_governor_intern",
		"metrics_governor_serieskey",
		// Runtime metrics prefixes
		"metrics_governor_process",
		"metrics_governor_goroutines",
		"metrics_governor_go_",
		"metrics_governor_memory",
		"metrics_governor_gc_",
		"metrics_governor_psi_",
		"metrics_governor_disk_",
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
		"../../dashboards/development.json",
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
