package prw

import (
	"testing"
)

func TestShardKeyBuilder_BuildKey(t *testing.T) {
	tests := []struct {
		name       string
		labels     []string
		timeseries *TimeSeries
		want       string
	}{
		{
			name:   "metric_name_only",
			labels: []string{},
			timeseries: &TimeSeries{
				Labels: []Label{
					{Name: "__name__", Value: "http_requests_total"},
					{Name: "method", Value: "GET"},
				},
			},
			want: "http_requests_total",
		},
		{
			name:   "with_single_label",
			labels: []string{"method"},
			timeseries: &TimeSeries{
				Labels: []Label{
					{Name: "__name__", Value: "http_requests_total"},
					{Name: "method", Value: "GET"},
					{Name: "status", Value: "200"},
				},
			},
			want: "http_requests_total|method=GET",
		},
		{
			name:   "with_multiple_labels_sorted",
			labels: []string{"service", "env"},
			timeseries: &TimeSeries{
				Labels: []Label{
					{Name: "__name__", Value: "http_requests_total"},
					{Name: "service", Value: "api"},
					{Name: "env", Value: "prod"},
				},
			},
			want: "http_requests_total|env=prod|service=api",
		},
		{
			name:   "missing_configured_label",
			labels: []string{"service", "region"},
			timeseries: &TimeSeries{
				Labels: []Label{
					{Name: "__name__", Value: "http_requests_total"},
					{Name: "service", Value: "api"},
				},
			},
			want: "http_requests_total|service=api",
		},
		{
			name:   "nil_timeseries",
			labels: []string{"service"},
			timeseries: nil,
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := NewShardKeyBuilder(ShardKeyConfig{Labels: tt.labels})
			got := builder.BuildKey(tt.timeseries)
			if got != tt.want {
				t.Errorf("BuildKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestShardKeyBuilder_BuildKeyFromLabels(t *testing.T) {
	builder := NewShardKeyBuilder(ShardKeyConfig{
		Labels: []string{"env", "service"},
	})

	labels := map[string]string{
		"env":     "prod",
		"service": "api",
		"region":  "us-east",
	}

	got := builder.BuildKeyFromLabels("http_requests_total", labels)
	want := "http_requests_total|env=prod|service=api"

	if got != want {
		t.Errorf("BuildKeyFromLabels() = %q, want %q", got, want)
	}
}

func TestShardKeyBuilder_GetConfiguredLabels(t *testing.T) {
	builder := NewShardKeyBuilder(ShardKeyConfig{
		Labels: []string{"z_label", "a_label", "m_label"},
	})

	labels := builder.GetConfiguredLabels()

	// Should be sorted
	expected := []string{"a_label", "m_label", "z_label"}
	if len(labels) != len(expected) {
		t.Fatalf("GetConfiguredLabels() returned %d labels, want %d", len(labels), len(expected))
	}

	for i, l := range labels {
		if l != expected[i] {
			t.Errorf("GetConfiguredLabels()[%d] = %q, want %q", i, l, expected[i])
		}
	}
}

func TestExtractLabelsMap(t *testing.T) {
	labels := []Label{
		{Name: "__name__", Value: "test_metric"},
		{Name: "env", Value: "prod"},
		{Name: "service", Value: "api"},
		{Name: "", Value: "empty_name"}, // Should be skipped
	}

	result := ExtractLabelsMap(labels)

	if len(result) != 3 {
		t.Errorf("ExtractLabelsMap() returned %d entries, want 3", len(result))
	}

	if result["__name__"] != "test_metric" {
		t.Errorf("result[__name__] = %q, want 'test_metric'", result["__name__"])
	}

	if result["env"] != "prod" {
		t.Errorf("result[env] = %q, want 'prod'", result["env"])
	}

	if result["service"] != "api" {
		t.Errorf("result[service] = %q, want 'api'", result["service"])
	}
}

func TestExtractLabelsMap_Empty(t *testing.T) {
	result := ExtractLabelsMap(nil)
	if len(result) != 0 {
		t.Errorf("ExtractLabelsMap(nil) should return empty map, got %d entries", len(result))
	}

	result = ExtractLabelsMap([]Label{})
	if len(result) != 0 {
		t.Errorf("ExtractLabelsMap([]) should return empty map, got %d entries", len(result))
	}
}

func BenchmarkShardKeyBuilder_BuildKey(b *testing.B) {
	builder := NewShardKeyBuilder(ShardKeyConfig{
		Labels: []string{"service", "env", "region"},
	})

	ts := &TimeSeries{
		Labels: []Label{
			{Name: "__name__", Value: "http_requests_total"},
			{Name: "service", Value: "api-gateway"},
			{Name: "env", Value: "production"},
			{Name: "region", Value: "us-east-1"},
			{Name: "method", Value: "GET"},
			{Name: "status", Value: "200"},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = builder.BuildKey(ts)
	}
}
