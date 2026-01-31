package prw

import (
	"sort"
	"strings"
)

// ShardKeyConfig configures which labels are included in the shard key.
type ShardKeyConfig struct {
	// Labels specifies additional labels to include in shard key.
	// Metric name (__name__) is ALWAYS included automatically.
	Labels []string
}

// ShardKeyBuilder constructs shard keys from metric name and labels.
type ShardKeyBuilder struct {
	config     ShardKeyConfig
	sortedKeys []string       // Pre-sorted label keys for deterministic ordering
	buf        strings.Builder // Reusable buffer for key building
}

// NewShardKeyBuilder creates a new shard key builder.
func NewShardKeyBuilder(cfg ShardKeyConfig) *ShardKeyBuilder {
	// Sort label keys for deterministic ordering
	sortedKeys := make([]string, len(cfg.Labels))
	copy(sortedKeys, cfg.Labels)
	sort.Strings(sortedKeys)

	return &ShardKeyBuilder{
		config:     cfg,
		sortedKeys: sortedKeys,
	}
}

// BuildKey constructs a shard key from a time series.
// The metric name (__name__) is ALWAYS the first component of the key.
// Format: "metric_name|label1=value1|label2=value2" (labels sorted alphabetically)
//
// Example:
//
//	labels: {__name__: "http_requests_total", service: "api", env: "prod"}
//	config.Labels: ["service", "env"]
//	Result: "http_requests_total|env=prod|service=api"
func (b *ShardKeyBuilder) BuildKey(ts *TimeSeries) string {
	if ts == nil {
		return ""
	}

	b.buf.Reset()

	// Always start with metric name
	metricName := ts.MetricName()
	b.buf.WriteString(metricName)

	// Build a map of labels for quick lookup
	labelMap := make(map[string]string, len(ts.Labels))
	for _, l := range ts.Labels {
		labelMap[l.Name] = l.Value
	}

	// Add configured labels in sorted order
	for _, key := range b.sortedKeys {
		if value, ok := labelMap[key]; ok && value != "" {
			b.buf.WriteByte('|')
			b.buf.WriteString(key)
			b.buf.WriteByte('=')
			b.buf.WriteString(value)
		}
	}

	return b.buf.String()
}

// BuildKeyFromLabels constructs a shard key from metric name and a label map.
func (b *ShardKeyBuilder) BuildKeyFromLabels(metricName string, labels map[string]string) string {
	b.buf.Reset()
	b.buf.WriteString(metricName)

	// Add configured labels in sorted order
	for _, key := range b.sortedKeys {
		if value, ok := labels[key]; ok && value != "" {
			b.buf.WriteByte('|')
			b.buf.WriteString(key)
			b.buf.WriteByte('=')
			b.buf.WriteString(value)
		}
	}

	return b.buf.String()
}

// GetConfiguredLabels returns a copy of the configured labels.
func (b *ShardKeyBuilder) GetConfiguredLabels() []string {
	result := make([]string, len(b.sortedKeys))
	copy(result, b.sortedKeys)
	return result
}

// ExtractLabelsMap converts PRW labels to a string map.
func ExtractLabelsMap(labels []Label) map[string]string {
	if len(labels) == 0 {
		return make(map[string]string)
	}

	result := make(map[string]string, len(labels))
	for _, l := range labels {
		if l.Name != "" {
			result[l.Name] = l.Value
		}
	}
	return result
}
