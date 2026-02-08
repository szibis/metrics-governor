package config

import (
	"flag"
	"strings"

	"gopkg.in/yaml.v3"
)

// ExplicitFlags returns a set of flag names that were explicitly set on the command line.
// Uses flag.Visit which only visits flags that were set.
func ExplicitFlags() map[string]bool {
	explicit := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) {
		explicit[f.Name] = true
	})
	return explicit
}

// ExplicitYAMLFields parses raw YAML data and returns a set of field "paths"
// that are present in the YAML. This enables distinguishing "not specified" from
// "set to zero/empty". Field paths use CLI flag naming (hyphenated) for
// compatibility with the explicit-fields map.
func ExplicitYAMLFields(data []byte) (map[string]bool, error) {
	var raw map[string]interface{}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	explicit := make(map[string]bool)
	flattenYAML("", raw, explicit)
	return explicit, nil
}

// flattenYAML recursively walks a map and records all leaf key paths.
// Keys are translated from YAML snake_case to CLI kebab-case.
func flattenYAML(prefix string, m map[string]interface{}, result map[string]bool) {
	for key, value := range m {
		// Build the full path
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}

		switch v := value.(type) {
		case map[string]interface{}:
			// Recurse into nested maps
			flattenYAML(path, v, result)
		default:
			// Leaf value — record the CLI-style flag name
			cliName := yamlPathToCLIFlag(path)
			if cliName != "" {
				result[cliName] = true
			}
			// Also record the raw YAML path for flexibility
			result[path] = true
		}
	}
}

// yamlPathToCLIFlag converts a YAML field path to its corresponding CLI flag name.
// Maps common YAML paths to their CLI flag equivalents.
func yamlPathToCLIFlag(path string) string {
	// Direct mappings for top-level new fields
	directMap := map[string]string{
		"profile":               "profile",
		"parallelism":           "parallelism",
		"memory_budget_percent": "memory-budget-percent",
		"export_timeout":        "export-timeout",
		"resilience_level":      "resilience-level",

		// Receiver
		"receiver.grpc.address":                      "grpc-listen",
		"receiver.http.address":                      "http-listen",
		"receiver.tls.enabled":                       "receiver-tls-enabled",
		"receiver.tls.cert_file":                     "receiver-tls-cert",
		"receiver.tls.key_file":                      "receiver-tls-key",
		"receiver.tls.ca_file":                       "receiver-tls-ca",
		"receiver.tls.client_auth":                   "receiver-tls-client-auth",
		"receiver.auth.enabled":                      "receiver-auth-enabled",
		"receiver.auth.bearer_token":                 "receiver-auth-bearer-token",
		"receiver.auth.basic_username":               "receiver-auth-basic-username",
		"receiver.auth.basic_password":               "receiver-auth-basic-password",
		"receiver.http.server.max_request_body_size": "receiver-max-request-body-size",

		// Exporter
		"exporter.endpoint":         "exporter-endpoint",
		"exporter.protocol":         "exporter-protocol",
		"exporter.insecure":         "exporter-insecure",
		"exporter.timeout":          "exporter-timeout",
		"exporter.default_path":     "exporter-default-path",
		"exporter.compression.type": "exporter-compression",
		"exporter.tls.enabled":      "exporter-tls-enabled",

		// Buffer
		"buffer.size":                    "buffer-size",
		"buffer.batch_size":              "batch-size",
		"buffer.max_batch_bytes":         "max-batch-bytes",
		"buffer.flush_interval":          "flush-interval",
		"buffer.flush_timeout":           "flush-timeout",
		"buffer.full_policy":             "buffer-full-policy",
		"buffer.batch_auto_tune.enabled": "buffer.batch_auto_tune.enabled",

		// Queue
		"exporter.queue.type":                          "queue-type",
		"exporter.queue.enabled":                       "queue-enabled",
		"exporter.queue.path":                          "queue-path",
		"exporter.queue.max_size":                      "queue-max-size",
		"exporter.queue.max_bytes":                     "queue-max-bytes",
		"exporter.queue.workers":                       "queue-workers",
		"exporter.queue.compression":                   "queue-compression",
		"exporter.queue.inmemory_blocks":               "queue-inmemory-blocks",
		"exporter.queue.meta_sync_interval":            "queue-meta-sync",
		"exporter.queue.backoff.enabled":               "queue-backoff-enabled",
		"exporter.queue.backoff.multiplier":            "queue-backoff-multiplier",
		"exporter.queue.circuit_breaker.enabled":       "queue-circuit-breaker-enabled",
		"exporter.queue.circuit_breaker.threshold":     "queue-circuit-breaker-threshold",
		"exporter.queue.circuit_breaker.reset_timeout": "queue-circuit-breaker-reset-timeout",
		"exporter.queue.batch_drain_size":              "queue-batch-drain-size",
		"exporter.queue.burst_drain_size":              "queue-burst-drain-size",
		"exporter.queue.retry_timeout":                 "queue-retry-timeout",
		"exporter.queue.close_timeout":                 "queue-close-timeout",
		"exporter.queue.drain_timeout":                 "queue-drain-timeout",
		"exporter.queue.drain_entry_timeout":           "queue-drain-entry-timeout",
		"exporter.queue.direct_export_timeout":         "queue-direct-export-timeout",
		"exporter.queue.always_queue":                  "queue-always-queue",
		"exporter.queue.pipeline_split.enabled":        "queue.pipeline_split.enabled",
		"exporter.queue.pipeline_split.preparer_count": "queue.pipeline_split.preparer_count",
		"exporter.queue.pipeline_split.sender_count":   "queue.pipeline_split.sender_count",
		"exporter.queue.max_concurrent_sends":          "queue.max_concurrent_sends",
		"exporter.queue.global_send_limit":             "queue.global_send_limit",
		"exporter.queue.adaptive_workers.enabled":      "queue.adaptive_workers.enabled",

		// Memory
		"memory.limit_ratio":    "memory-limit-ratio",
		"memory.buffer_percent": "buffer-memory-percent",
		"memory.queue_percent":  "queue-memory-percent",

		// Performance
		"performance.export_concurrency": "export-concurrency",
		"performance.string_interning":   "string-interning",

		// Limits
		"limits.dry_run":             "limits-dry-run",
		"limits.rule_cache_max_size": "rule-cache-max-size",
		"limits.stats_threshold":     "limits-stats-threshold",

		// Stats
		"stats.address": "stats-addr",
		"stats.labels":  "stats-labels",
	}

	if cliName, ok := directMap[path]; ok {
		return cliName
	}

	// Fallback: convert underscores to hyphens for unknown paths
	return strings.ReplaceAll(path, "_", "-")
}

// MergeExplicitFields combines CLI and YAML explicit field sets.
// CLI fields take precedence (appear in both maps).
func MergeExplicitFields(cli, yamlFields map[string]bool) map[string]bool {
	merged := make(map[string]bool, len(cli)+len(yamlFields))
	for k, v := range yamlFields {
		merged[k] = v
	}
	for k, v := range cli {
		merged[k] = v
	}
	return merged
}
