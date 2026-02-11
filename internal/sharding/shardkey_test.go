package sharding

import (
	"testing"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
)

func TestShardKeyBuilder_New(t *testing.T) {
	cfg := ShardKeyConfig{
		Labels: []string{"service", "env"},
	}
	builder := NewShardKeyBuilder(cfg)
	if builder == nil {
		t.Fatal("expected non-nil builder")
	}
}

func TestShardKeyBuilder_New_EmptyLabels(t *testing.T) {
	cfg := ShardKeyConfig{
		Labels: []string{},
	}
	builder := NewShardKeyBuilder(cfg)
	if builder == nil {
		t.Fatal("expected non-nil builder")
	}
	if len(builder.sortedKeys) != 0 {
		t.Errorf("expected empty sorted keys, got %d", len(builder.sortedKeys))
	}
}

func TestShardKeyBuilder_New_SortsLabels(t *testing.T) {
	cfg := ShardKeyConfig{
		Labels: []string{"service", "env", "cluster"},
	}
	builder := NewShardKeyBuilder(cfg)

	expected := []string{"cluster", "env", "service"}
	if len(builder.sortedKeys) != len(expected) {
		t.Fatalf("expected %d sorted keys, got %d", len(expected), len(builder.sortedKeys))
	}
	for i, key := range expected {
		if builder.sortedKeys[i] != key {
			t.Errorf("sorted key %d: expected %s, got %s", i, key, builder.sortedKeys[i])
		}
	}
}

func TestShardKeyBuilder_BuildKey(t *testing.T) {
	cfg := ShardKeyConfig{
		Labels: []string{"service", "env"},
	}
	builder := NewShardKeyBuilder(cfg)

	attrs := map[string]string{
		"service": "api",
		"env":     "prod",
		"method":  "GET", // Not in config, should be ignored
	}

	key := builder.BuildKey("http_requests_total", attrs)
	expected := "http_requests_total|env=prod|service=api"

	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestShardKeyBuilder_BuildKey_EmptyLabels(t *testing.T) {
	cfg := ShardKeyConfig{
		Labels: []string{},
	}
	builder := NewShardKeyBuilder(cfg)

	attrs := map[string]string{
		"service": "api",
		"env":     "prod",
	}

	key := builder.BuildKey("http_requests_total", attrs)
	expected := "http_requests_total"

	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestShardKeyBuilder_BuildKey_SingleLabel(t *testing.T) {
	cfg := ShardKeyConfig{
		Labels: []string{"service"},
	}
	builder := NewShardKeyBuilder(cfg)

	attrs := map[string]string{
		"service": "api",
		"env":     "prod",
	}

	key := builder.BuildKey("cpu_usage", attrs)
	expected := "cpu_usage|service=api"

	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestShardKeyBuilder_BuildKey_MultipleLabels(t *testing.T) {
	cfg := ShardKeyConfig{
		Labels: []string{"service", "env", "cluster", "region"},
	}
	builder := NewShardKeyBuilder(cfg)

	attrs := map[string]string{
		"service": "api",
		"env":     "prod",
		"cluster": "us-east-1",
		"region":  "us-east",
	}

	key := builder.BuildKey("memory_bytes", attrs)
	expected := "memory_bytes|cluster=us-east-1|env=prod|region=us-east|service=api"

	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestShardKeyBuilder_BuildKey_MissingLabels(t *testing.T) {
	cfg := ShardKeyConfig{
		Labels: []string{"service", "env", "cluster"},
	}
	builder := NewShardKeyBuilder(cfg)

	// No matching labels
	attrs := map[string]string{
		"method": "GET",
		"path":   "/api/v1",
	}

	key := builder.BuildKey("requests", attrs)
	expected := "requests"

	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestShardKeyBuilder_BuildKey_PartialLabels(t *testing.T) {
	cfg := ShardKeyConfig{
		Labels: []string{"service", "env", "cluster"},
	}
	builder := NewShardKeyBuilder(cfg)

	// Only some labels present
	attrs := map[string]string{
		"service": "api",
		// "env" missing
		"cluster": "primary",
	}

	key := builder.BuildKey("metric", attrs)
	expected := "metric|cluster=primary|service=api"

	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestShardKeyBuilder_BuildKey_SortedLabels(t *testing.T) {
	// Labels provided in different orders should produce same key
	cfg := ShardKeyConfig{
		Labels: []string{"z_label", "a_label", "m_label"},
	}
	builder := NewShardKeyBuilder(cfg)

	attrs := map[string]string{
		"z_label": "z_value",
		"a_label": "a_value",
		"m_label": "m_value",
	}

	key := builder.BuildKey("metric", attrs)
	expected := "metric|a_label=a_value|m_label=m_value|z_label=z_value"

	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestShardKeyBuilder_BuildKey_SpecialCharacters(t *testing.T) {
	cfg := ShardKeyConfig{
		Labels: []string{"path", "query"},
	}
	builder := NewShardKeyBuilder(cfg)

	attrs := map[string]string{
		"path":  "/api/v1/users",
		"query": "name=foo&age=30",
	}

	key := builder.BuildKey("http_requests", attrs)
	expected := "http_requests|path=/api/v1/users|query=name=foo&age=30"

	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestShardKeyBuilder_BuildKey_EmptyMetricName(t *testing.T) {
	cfg := ShardKeyConfig{
		Labels: []string{"service"},
	}
	builder := NewShardKeyBuilder(cfg)

	attrs := map[string]string{
		"service": "api",
	}

	key := builder.BuildKey("", attrs)
	expected := "|service=api"

	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestShardKeyBuilder_BuildKey_EmptyLabelValue(t *testing.T) {
	cfg := ShardKeyConfig{
		Labels: []string{"service", "env"},
	}
	builder := NewShardKeyBuilder(cfg)

	attrs := map[string]string{
		"service": "api",
		"env":     "", // Empty value - should be skipped
	}

	key := builder.BuildKey("metric", attrs)
	expected := "metric|service=api"

	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestShardKeyBuilder_BuildKey_UnicodeLabels(t *testing.T) {
	cfg := ShardKeyConfig{
		Labels: []string{"name", "region"},
	}
	builder := NewShardKeyBuilder(cfg)

	attrs := map[string]string{
		"name":   "サービス",
		"region": "日本",
	}

	key := builder.BuildKey("metric", attrs)
	expected := "metric|name=サービス|region=日本"

	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestShardKeyBuilder_Deterministic(t *testing.T) {
	cfg := ShardKeyConfig{
		Labels: []string{"service", "env", "cluster"},
	}
	builder := NewShardKeyBuilder(cfg)

	attrs := map[string]string{
		"service": "api",
		"env":     "prod",
		"cluster": "primary",
	}

	// Build key multiple times
	key1 := builder.BuildKey("metric", attrs)
	key2 := builder.BuildKey("metric", attrs)
	key3 := builder.BuildKey("metric", attrs)

	if key1 != key2 || key2 != key3 {
		t.Errorf("non-deterministic keys: %q, %q, %q", key1, key2, key3)
	}
}

func TestShardKeyBuilder_BuildKeyFromProto(t *testing.T) {
	cfg := ShardKeyConfig{
		Labels: []string{"service", "env"},
	}
	builder := NewShardKeyBuilder(cfg)

	attrs := []*commonpb.KeyValue{
		{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "api"}}},
		{Key: "env", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "prod"}}},
	}

	key := builder.BuildKeyFromProto("http_requests", attrs)
	expected := "http_requests|env=prod|service=api"

	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestShardKeyBuilder_GetConfiguredLabels(t *testing.T) {
	cfg := ShardKeyConfig{
		Labels: []string{"service", "env", "cluster"},
	}
	builder := NewShardKeyBuilder(cfg)

	labels := builder.GetConfiguredLabels()

	// Should be sorted
	expected := []string{"cluster", "env", "service"}
	if len(labels) != len(expected) {
		t.Fatalf("expected %d labels, got %d", len(expected), len(labels))
	}
	for i, label := range expected {
		if labels[i] != label {
			t.Errorf("label %d: expected %s, got %s", i, label, labels[i])
		}
	}

	// Modify returned slice should not affect builder
	labels[0] = "modified"
	originalLabels := builder.GetConfiguredLabels()
	if originalLabels[0] == "modified" {
		t.Error("GetConfiguredLabels should return a copy")
	}
}

// ExtractAttributes tests

func TestExtractAttributes(t *testing.T) {
	attrs := []*commonpb.KeyValue{
		{Key: "string_key", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "string_value"}}},
		{Key: "int_key", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 42}}},
		{Key: "bool_key", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: true}}},
	}

	result := ExtractAttributes(attrs)

	if result["string_key"] != "string_value" {
		t.Errorf("string_key: expected 'string_value', got %q", result["string_key"])
	}
	if result["int_key"] != "42" {
		t.Errorf("int_key: expected '42', got %q", result["int_key"])
	}
	if result["bool_key"] != "true" {
		t.Errorf("bool_key: expected 'true', got %q", result["bool_key"])
	}
}

func TestExtractAttributes_Empty(t *testing.T) {
	result := ExtractAttributes(nil)
	if len(result) != 0 {
		t.Errorf("expected empty map, got %d entries", len(result))
	}

	result = ExtractAttributes([]*commonpb.KeyValue{})
	if len(result) != 0 {
		t.Errorf("expected empty map, got %d entries", len(result))
	}
}

func TestExtractAttributes_StringValues(t *testing.T) {
	attrs := []*commonpb.KeyValue{
		{Key: "key1", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "value1"}}},
		{Key: "key2", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "value2"}}},
	}

	result := ExtractAttributes(attrs)

	if result["key1"] != "value1" {
		t.Errorf("key1: expected 'value1', got %q", result["key1"])
	}
	if result["key2"] != "value2" {
		t.Errorf("key2: expected 'value2', got %q", result["key2"])
	}
}

func TestExtractAttributes_IntValues(t *testing.T) {
	attrs := []*commonpb.KeyValue{
		{Key: "positive", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 12345}}},
		{Key: "negative", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: -9876}}},
		{Key: "zero", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 0}}},
	}

	result := ExtractAttributes(attrs)

	if result["positive"] != "12345" {
		t.Errorf("positive: expected '12345', got %q", result["positive"])
	}
	if result["negative"] != "-9876" {
		t.Errorf("negative: expected '-9876', got %q", result["negative"])
	}
	if result["zero"] != "0" {
		t.Errorf("zero: expected '0', got %q", result["zero"])
	}
}

func TestExtractAttributes_BoolValues(t *testing.T) {
	attrs := []*commonpb.KeyValue{
		{Key: "true_key", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: true}}},
		{Key: "false_key", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: false}}},
	}

	result := ExtractAttributes(attrs)

	if result["true_key"] != "true" {
		t.Errorf("true_key: expected 'true', got %q", result["true_key"])
	}
	if result["false_key"] != "false" {
		t.Errorf("false_key: expected 'false', got %q", result["false_key"])
	}
}

func TestExtractAttributes_DoubleValues(t *testing.T) {
	attrs := []*commonpb.KeyValue{
		{Key: "float", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: 3.14159}}},
		{Key: "whole", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: 42.0}}},
		{Key: "zero", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: 0.0}}},
	}

	result := ExtractAttributes(attrs)

	// Check float has some decimal representation
	if result["float"] == "" {
		t.Error("float: expected non-empty value")
	}
	if result["whole"] != "42" {
		t.Errorf("whole: expected '42', got %q", result["whole"])
	}
	if result["zero"] != "0" {
		t.Errorf("zero: expected '0', got %q", result["zero"])
	}
}

func TestExtractAttributes_NilValue(t *testing.T) {
	attrs := []*commonpb.KeyValue{
		{Key: "nil_value", Value: nil},
		{Key: "empty_anyvalue", Value: &commonpb.AnyValue{}},
	}

	result := ExtractAttributes(attrs)

	// Nil values should be skipped (empty string not added)
	if _, ok := result["nil_value"]; ok {
		t.Error("nil_value should not be in result")
	}
	if _, ok := result["empty_anyvalue"]; ok {
		t.Error("empty_anyvalue should not be in result")
	}
}

func TestExtractAttributes_NilKeyValue(t *testing.T) {
	attrs := []*commonpb.KeyValue{
		nil,
		{Key: "valid", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "value"}}},
	}

	result := ExtractAttributes(attrs)

	if len(result) != 1 {
		t.Errorf("expected 1 entry, got %d", len(result))
	}
	if result["valid"] != "value" {
		t.Errorf("valid: expected 'value', got %q", result["valid"])
	}
}

func TestExtractAttributes_EmptyKey(t *testing.T) {
	attrs := []*commonpb.KeyValue{
		{Key: "", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "value"}}},
		{Key: "valid", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "value2"}}},
	}

	result := ExtractAttributes(attrs)

	// Empty key should be skipped
	if _, ok := result[""]; ok {
		t.Error("empty key should not be in result")
	}
	if result["valid"] != "value2" {
		t.Errorf("valid: expected 'value2', got %q", result["valid"])
	}
}

func TestExtractAttributes_MixedTypes(t *testing.T) {
	attrs := []*commonpb.KeyValue{
		{Key: "string", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "hello"}}},
		{Key: "int", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 123}}},
		{Key: "bool", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: true}}},
		{Key: "double", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: 1.5}}},
	}

	result := ExtractAttributes(attrs)

	if len(result) != 4 {
		t.Errorf("expected 4 entries, got %d", len(result))
	}
	if result["string"] != "hello" {
		t.Errorf("string: expected 'hello', got %q", result["string"])
	}
	if result["int"] != "123" {
		t.Errorf("int: expected '123', got %q", result["int"])
	}
	if result["bool"] != "true" {
		t.Errorf("bool: expected 'true', got %q", result["bool"])
	}
}

// MergeAttributes tests

func TestMergeAttributes(t *testing.T) {
	map1 := map[string]string{"a": "1", "b": "2"}
	map2 := map[string]string{"c": "3", "d": "4"}

	result := MergeAttributes(map1, map2)

	if len(result) != 4 {
		t.Errorf("expected 4 entries, got %d", len(result))
	}
	if result["a"] != "1" || result["b"] != "2" || result["c"] != "3" || result["d"] != "4" {
		t.Error("merge failed")
	}
}

func TestMergeAttributes_Override(t *testing.T) {
	map1 := map[string]string{"a": "1", "b": "2"}
	map2 := map[string]string{"b": "override", "c": "3"}

	result := MergeAttributes(map1, map2)

	if result["b"] != "override" {
		t.Errorf("expected 'override', got %q", result["b"])
	}
}

func TestMergeAttributes_Empty(t *testing.T) {
	result := MergeAttributes()
	if len(result) != 0 {
		t.Errorf("expected empty result, got %d entries", len(result))
	}

	result = MergeAttributes(map[string]string{})
	if len(result) != 0 {
		t.Errorf("expected empty result, got %d entries", len(result))
	}
}

// Helper function tests

func TestIntToString(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0"},
		{1, "1"},
		{-1, "-1"},
		{12345, "12345"},
		{-12345, "-12345"},
		{9223372036854775807, "9223372036854775807"},   // max int64
		{-9223372036854775808, "-9223372036854775808"}, // min int64
	}

	for _, tc := range tests {
		result := intToString(tc.input)
		if result != tc.expected {
			t.Errorf("intToString(%d): expected %q, got %q", tc.input, tc.expected, result)
		}
	}
}

func TestFloatToString(t *testing.T) {
	tests := []struct {
		input    float64
		expected string
	}{
		{0, "0"},
		{42, "42"},
		{-42, "-42"},
	}

	for _, tc := range tests {
		result := floatToString(tc.input)
		if result != tc.expected {
			t.Errorf("floatToString(%f): expected %q, got %q", tc.input, tc.expected, result)
		}
	}

	// For decimals, just verify it produces some output
	result := floatToString(3.14159)
	if result == "" {
		t.Error("floatToString(3.14159) should produce non-empty result")
	}
}

// Benchmarks

func BenchmarkShardKeyBuilder_BuildKey(b *testing.B) {
	cfg := ShardKeyConfig{
		Labels: []string{"service", "env", "cluster"},
	}
	builder := NewShardKeyBuilder(cfg)
	attrs := map[string]string{
		"service": "api",
		"env":     "prod",
		"cluster": "primary",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		builder.BuildKey("http_requests_total", attrs)
	}
}

func BenchmarkShardKeyBuilder_BuildKey_ManyLabels(b *testing.B) {
	labels := make([]string, 10)
	attrs := make(map[string]string, 10)
	for i := 0; i < 10; i++ {
		labels[i] = string(rune('a' + i))
		attrs[labels[i]] = string(rune('A' + i))
	}

	cfg := ShardKeyConfig{Labels: labels}
	builder := NewShardKeyBuilder(cfg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		builder.BuildKey("metric_name", attrs)
	}
}

func BenchmarkExtractAttributes(b *testing.B) {
	attrs := []*commonpb.KeyValue{
		{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "api"}}},
		{Key: "env", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "prod"}}},
		{Key: "cluster", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "primary"}}},
		{Key: "version", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 42}}},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ExtractAttributes(attrs)
	}
}
