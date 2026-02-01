package prw

import (
	"sort"
	"strings"

	"github.com/szibis/metrics-governor/internal/intern"
)

// ShardKeyConfig configures which labels are included in the shard key.
type ShardKeyConfig struct {
	// Labels specifies additional labels to include in shard key.
	// Metric name (__name__) is ALWAYS included automatically.
	Labels []string
}

// Interning pools for shard key components
var (
	// metricNameIntern is used for interning metric names (always repeated)
	metricNameIntern = intern.NewPool()

	// shardLabelKeyIntern is used for interning label keys in shard key building
	shardLabelKeyIntern = intern.CommonLabels()
)

// ShardKeyBuilder constructs shard keys from metric name and labels.
// This type is safe for concurrent use.
type ShardKeyBuilder struct {
	config     ShardKeyConfig
	sortedKeys []string // Pre-sorted label keys for deterministic ordering
}

// NewShardKeyBuilder creates a new shard key builder.
func NewShardKeyBuilder(cfg ShardKeyConfig) *ShardKeyBuilder {
	// Sort label keys for deterministic ordering and intern them
	sortedKeys := make([]string, len(cfg.Labels))
	for i, key := range cfg.Labels {
		sortedKeys[i] = shardLabelKeyIntern.Intern(key)
	}
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

	var buf strings.Builder

	// Always start with metric name (interned for deduplication)
	metricName := metricNameIntern.Intern(ts.MetricName())
	buf.WriteString(metricName)

	// Build a map of labels for quick lookup
	labelMap := make(map[string]string, len(ts.Labels))
	for _, l := range ts.Labels {
		labelMap[l.Name] = l.Value
	}

	// Add configured labels in sorted order
	for _, key := range b.sortedKeys {
		if value, ok := labelMap[key]; ok && value != "" {
			buf.WriteByte('|')
			buf.WriteString(key)
			buf.WriteByte('=')
			buf.WriteString(value)
		}
	}

	return buf.String()
}

// BuildKeyFromLabels constructs a shard key from metric name and a label map.
func (b *ShardKeyBuilder) BuildKeyFromLabels(metricName string, labels map[string]string) string {
	var buf strings.Builder
	// Intern metric name
	buf.WriteString(metricNameIntern.Intern(metricName))

	// Add configured labels in sorted order
	for _, key := range b.sortedKeys {
		if value, ok := labels[key]; ok && value != "" {
			buf.WriteByte('|')
			buf.WriteString(key)
			buf.WriteByte('=')
			buf.WriteString(value)
		}
	}

	return buf.String()
}

// ShardKeyInternStats returns interning statistics for shard key components.
func ShardKeyInternStats() (metricHits, metricMisses, labelHits, labelMisses uint64) {
	metricHits, metricMisses = metricNameIntern.Stats()
	labelHits, labelMisses = shardLabelKeyIntern.Stats()
	return
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
