// generate-config-meta reads Go defaults, estimation constants, and storage-specs.json,
// then injects the assembled config-meta JSON into the config helper HTML files.
//
// Usage:
//
//	go run ./tools/config-helper/cmd/generate
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/szibis/metrics-governor/internal/config"
)

// configMeta is the top-level structure embedded in the HTML.
type configMeta struct {
	Defaults   map[string]any             `json:"defaults"`
	Estimation map[string]any             `json:"estimation"`
	Storage    map[string]json.RawMessage `json:"storage"`
}

func main() {
	goDefaults := config.DefaultConfig()

	defaults := map[string]any{
		"buffer_size":             goDefaults.BufferSize,
		"buffer_batch_size":       goDefaults.MaxBatchSize,
		"queue_type":              goDefaults.QueueType,
		"queue_max_bytes":         goDefaults.QueueMaxBytes,
		"queue_max_retries":       3, // UI-only default (not in Go config)
		"queue_retry_interval":    fmtDuration(goDefaults.QueueRetryInterval),
		"queue_max_retry_delay":   fmtDuration(goDefaults.QueueMaxRetryDelay),
		"queue_full_behavior":     goDefaults.QueueFullBehavior,
		"cardinality_mode":        goDefaults.CardinalityMode,
		"bloom_fpr":               goDefaults.CardinalityFPRate,
		"hll_threshold":           goDefaults.CardinalityHLLThreshold,
		"retention_window":        "60s", // UI-only default
		"grpc_listen":             goDefaults.GRPCListenAddr,
		"http_listen":             goDefaults.HTTPListenAddr,
		"stats_addr":              goDefaults.StatsAddr,
		"exporter_endpoint":       goDefaults.ExporterEndpoint,
		"exporter_protocol":       goDefaults.ExporterProtocol,
		"exporter_insecure":       goDefaults.ExporterInsecure,
		"flush_interval":          fmtDuration(goDefaults.FlushInterval),
		"string_interning":        goDefaults.StringInterning,
		"intern_max_value_length": goDefaults.InternMaxValueLength,
	}

	estimation := map[string]any{
		"dps_per_core":                100000,
		"compression_ratio":           0.3,
		"base_overhead_bytes":         52428800, // 50 MB
		"pvc_headroom_factor":         1.2,
		"cpu_target_utilization":      0.7,
		"read_iops_ratio":             0.1,
		"cpu_limit_factor":            1.5,
		"mem_limit_factor":            1.3,
		"hll_memory_bytes":            12288,
		"exact_mode_bytes_per_series": 75,
		"label_key_overhead_bytes":    10,
		"series_base_overhead_bytes":  16,
	}

	// Read storage specs from external JSON file
	storageData, err := os.ReadFile("tools/config-helper/storage-specs.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading storage-specs.json: %v\n", err)
		os.Exit(1)
	}

	// Validate it's valid JSON and parse into provider map
	var storageMap map[string]json.RawMessage
	if err := json.Unmarshal(storageData, &storageMap); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing storage-specs.json: %v\n", err)
		os.Exit(1)
	}

	meta := configMeta{
		Defaults:   defaults,
		Estimation: estimation,
		Storage:    storageMap,
	}

	metaJSON, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling config-meta: %v\n", err)
		os.Exit(1)
	}

	// Inject into HTML files
	htmlFiles := []string{
		"tools/config-helper/index.html",
		"index.html",
	}

	re := regexp.MustCompile(`(?s)(<script type="application/json" id="config-meta">)\s*.*?\s*(</script>)`)

	for _, path := range htmlFiles {
		html, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", path, err)
			os.Exit(1)
		}

		if !re.Match(html) {
			fmt.Fprintf(os.Stderr, "Error: config-meta script tag not found in %s\n", path)
			os.Exit(1)
		}

		replacement := "${1}\n" + string(metaJSON) + "\n${2}"
		updated := re.ReplaceAll(html, []byte(replacement))

		if err := os.WriteFile(path, updated, 0600); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", path, err)
			os.Exit(1)
		}

		fmt.Printf("Updated %s\n", path)
	}

	fmt.Println("Config-meta generation complete.")
}

func fmtDuration(d interface{ String() string }) string {
	s := d.String()
	// Convert Go duration strings: "5s", "5m0s" → "5s", "5m"
	s = strings.TrimSuffix(s, "0s")
	if s == "" {
		s = "0s"
	}
	return s
}
