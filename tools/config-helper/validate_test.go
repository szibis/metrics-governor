package config_helper_test

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/config"
)

// configMeta mirrors the JSON structure embedded in index.html.
type configMeta struct {
	Defaults struct {
		BufferSize          int     `json:"buffer_size"`
		BufferBatchSize     int     `json:"buffer_batch_size"`
		QueueType           string  `json:"queue_type"`
		QueueMaxBytes       int64   `json:"queue_max_bytes"`
		QueueRetryInterval  string  `json:"queue_retry_interval"`
		QueueMaxRetryDelay  string  `json:"queue_max_retry_delay"`
		QueueFullBehavior   string  `json:"queue_full_behavior"`
		CardinalityMode     string  `json:"cardinality_mode"`
		BloomFPR            float64 `json:"bloom_fpr"`
		HLLThreshold        int64   `json:"hll_threshold"`
		RetentionWindow     string  `json:"retention_window"`
		GRPCListen          string  `json:"grpc_listen"`
		HTTPListen          string  `json:"http_listen"`
		StatsAddr           string  `json:"stats_addr"`
		ExporterEndpoint    string  `json:"exporter_endpoint"`
		ExporterProtocol    string  `json:"exporter_protocol"`
		ExporterInsecure    bool    `json:"exporter_insecure"`
		FlushInterval       string  `json:"flush_interval"`
		StringInterning     bool    `json:"string_interning"`
		InternMaxValueLen   int     `json:"intern_max_value_length"`
	} `json:"defaults"`
}

func TestConfigMetaMatchesGoDefaults(t *testing.T) {
	html, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatalf("failed to read index.html: %v", err)
	}

	re := regexp.MustCompile(`(?s)<script[^>]+id="config-meta"[^>]*>(.*?)</script>`)
	match := re.FindSubmatch(html)
	if match == nil {
		t.Fatal("config-meta script tag not found in index.html")
	}

	var meta configMeta
	if err := json.Unmarshal(match[1], &meta); err != nil {
		t.Fatalf("failed to parse config-meta JSON: %v", err)
	}

	goDefaults := config.DefaultConfig()

	// Buffer
	assertEqual(t, "buffer_size", meta.Defaults.BufferSize, goDefaults.BufferSize)
	assertEqual(t, "buffer_batch_size", meta.Defaults.BufferBatchSize, goDefaults.MaxBatchSize)

	// Queue
	assertEqual(t, "queue_type", meta.Defaults.QueueType, goDefaults.QueueType)
	assertEqualInt64(t, "queue_max_bytes", meta.Defaults.QueueMaxBytes, int64(goDefaults.QueueMaxBytes))
	assertEqualDuration(t, "queue_retry_interval", meta.Defaults.QueueRetryInterval, goDefaults.QueueRetryInterval)
	assertEqualDuration(t, "queue_max_retry_delay", meta.Defaults.QueueMaxRetryDelay, goDefaults.QueueMaxRetryDelay)
	assertEqual(t, "queue_full_behavior", meta.Defaults.QueueFullBehavior, goDefaults.QueueFullBehavior)

	// Cardinality
	assertEqual(t, "cardinality_mode", meta.Defaults.CardinalityMode, goDefaults.CardinalityMode)
	assertEqualFloat(t, "bloom_fpr", meta.Defaults.BloomFPR, goDefaults.CardinalityFPRate)
	assertEqualInt64(t, "hll_threshold", meta.Defaults.HLLThreshold, int64(goDefaults.CardinalityHLLThreshold))

	// Network
	assertEqual(t, "grpc_listen", meta.Defaults.GRPCListen, goDefaults.GRPCListenAddr)
	assertEqual(t, "http_listen", meta.Defaults.HTTPListen, goDefaults.HTTPListenAddr)
	assertEqual(t, "stats_addr", meta.Defaults.StatsAddr, goDefaults.StatsAddr)
	assertEqual(t, "exporter_endpoint", meta.Defaults.ExporterEndpoint, goDefaults.ExporterEndpoint)
	assertEqual(t, "exporter_protocol", meta.Defaults.ExporterProtocol, goDefaults.ExporterProtocol)
	assertEqual(t, "exporter_insecure", meta.Defaults.ExporterInsecure, goDefaults.ExporterInsecure)

	// Performance
	assertEqualDuration(t, "flush_interval", meta.Defaults.FlushInterval, goDefaults.FlushInterval)
	assertEqual(t, "string_interning", meta.Defaults.StringInterning, goDefaults.StringInterning)
	assertEqual(t, "intern_max_value_length", meta.Defaults.InternMaxValueLen, goDefaults.InternMaxValueLength)
}

func assertEqual[T comparable](t *testing.T, name string, html, goVal T) {
	t.Helper()
	if html != goVal {
		t.Errorf("%s: HTML config-meta has %v, Go DefaultConfig() has %v", name, html, goVal)
	}
}

func assertEqualInt64(t *testing.T, name string, html, goVal int64) {
	t.Helper()
	if html != goVal {
		t.Errorf("%s: HTML config-meta has %d, Go DefaultConfig() has %d", name, html, goVal)
	}
}

func assertEqualFloat(t *testing.T, name string, html, goVal float64) {
	t.Helper()
	if html != goVal {
		t.Errorf("%s: HTML config-meta has %f, Go DefaultConfig() has %f", name, html, goVal)
	}
}

func assertEqualDuration(t *testing.T, name string, htmlStr string, goVal time.Duration) {
	t.Helper()
	htmlDur, err := parseDuration(htmlStr)
	if err != nil {
		t.Errorf("%s: failed to parse HTML duration %q: %v", name, htmlStr, err)
		return
	}
	if htmlDur != goVal {
		t.Errorf("%s: HTML config-meta has %q (%v), Go DefaultConfig() has %v", name, htmlStr, htmlDur, goVal)
	}
}

// parseDuration parses Go-style durations plus simple "Xs" / "Xm" / "Xh" formats.
func parseDuration(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err == nil {
		return d, nil
	}
	return 0, fmt.Errorf("cannot parse %q as duration: %w", s, err)
}
