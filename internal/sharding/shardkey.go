package sharding

import (
	"sort"
	"strings"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
)

// ShardKeyConfig configures which labels are included in the shard key.
type ShardKeyConfig struct {
	// Labels specifies additional labels to include in shard key.
	// Metric name is ALWAYS included automatically.
	Labels []string
}

// ShardKeyBuilder constructs shard keys from metric name and attributes.
type ShardKeyBuilder struct {
	config     ShardKeyConfig
	sortedKeys []string      // Pre-sorted label keys for deterministic ordering
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

// BuildKey constructs a shard key from metric name and attributes.
// The metric name is ALWAYS the first component of the key.
// Format: "metric_name|label1=value1|label2=value2" (labels sorted alphabetically)
//
// Example:
//
//	metricName: "http_requests_total"
//	attrs: {service: "api", env: "prod", method: "GET"}
//	config.Labels: ["service", "env"]
//	Result: "http_requests_total|env=prod|service=api"
//
// If a configured label is missing from attrs, it's omitted from the key.
func (b *ShardKeyBuilder) BuildKey(metricName string, attrs map[string]string) string {
	b.buf.Reset()
	b.buf.WriteString(metricName)

	// Add configured labels in sorted order
	for _, key := range b.sortedKeys {
		if value, ok := attrs[key]; ok && value != "" {
			b.buf.WriteByte('|')
			b.buf.WriteString(key)
			b.buf.WriteByte('=')
			b.buf.WriteString(value)
		}
	}

	return b.buf.String()
}

// BuildKeyFromProto constructs a shard key from metric name and protobuf attributes.
// This is a convenience method that combines ExtractAttributes and BuildKey.
func (b *ShardKeyBuilder) BuildKeyFromProto(metricName string, attrs []*commonpb.KeyValue) string {
	return b.BuildKey(metricName, ExtractAttributes(attrs))
}

// GetConfiguredLabels returns a copy of the configured labels.
func (b *ShardKeyBuilder) GetConfiguredLabels() []string {
	result := make([]string, len(b.sortedKeys))
	copy(result, b.sortedKeys)
	return result
}

// ExtractAttributes converts protobuf KeyValue attributes to a string map.
// Handles string, int, bool, and double value types.
func ExtractAttributes(attrs []*commonpb.KeyValue) map[string]string {
	if len(attrs) == 0 {
		return make(map[string]string)
	}

	result := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		if attr == nil || attr.Key == "" {
			continue
		}

		value := ""
		if attr.Value != nil {
			switch v := attr.Value.Value.(type) {
			case *commonpb.AnyValue_StringValue:
				value = v.StringValue
			case *commonpb.AnyValue_IntValue:
				value = intToString(v.IntValue)
			case *commonpb.AnyValue_BoolValue:
				if v.BoolValue {
					value = "true"
				} else {
					value = "false"
				}
			case *commonpb.AnyValue_DoubleValue:
				value = floatToString(v.DoubleValue)
			}
		}

		if value != "" {
			result[attr.Key] = value
		}
	}

	return result
}

// MergeAttributes merges multiple attribute maps.
// Later maps override earlier ones for duplicate keys.
func MergeAttributes(maps ...map[string]string) map[string]string {
	size := 0
	for _, m := range maps {
		size += len(m)
	}

	result := make(map[string]string, size)
	for _, m := range maps {
		for k, v := range m {
			result[k] = v
		}
	}
	return result
}

// intToString converts int64 to string without using fmt.Sprintf.
func intToString(i int64) string {
	if i == 0 {
		return "0"
	}

	// Handle minimum int64 specially to avoid overflow when negating
	if i == -9223372036854775808 {
		return "-9223372036854775808"
	}

	neg := i < 0
	if neg {
		i = -i
	}

	// Max int64 is 19 digits
	var buf [20]byte
	idx := len(buf)

	for i > 0 {
		idx--
		buf[idx] = byte('0' + i%10)
		i /= 10
	}

	if neg {
		idx--
		buf[idx] = '-'
	}

	return string(buf[idx:])
}

// floatToString converts float64 to string.
// Uses simple formatting for common cases.
func floatToString(f float64) string {
	// Handle special cases
	if f == 0 {
		return "0"
	}

	// For integers, use intToString
	if f == float64(int64(f)) {
		return intToString(int64(f))
	}

	// For decimals, use a simple approach
	// This handles most common cases adequately for shard keys
	neg := f < 0
	if neg {
		f = -f
	}

	intPart := int64(f)
	fracPart := f - float64(intPart)

	var result strings.Builder
	if neg {
		result.WriteByte('-')
	}
	result.WriteString(intToString(intPart))
	result.WriteByte('.')

	// Up to 6 decimal places
	for i := 0; i < 6 && fracPart > 0; i++ {
		fracPart *= 10
		digit := int(fracPart)
		result.WriteByte(byte('0' + digit))
		fracPart -= float64(digit)
	}

	return result.String()
}
