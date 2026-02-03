package limits

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

func TestBuildSeriesKey_Pooled(t *testing.T) {
	tests := []struct {
		name  string
		attrs map[string]string
		want  string
	}{
		{
			name:  "empty_attrs",
			attrs: map[string]string{},
			want:  "",
		},
		{
			name:  "nil_attrs",
			attrs: nil,
			want:  "",
		},
		{
			name:  "single_attr",
			attrs: map[string]string{"service": "web"},
			want:  "service=web",
		},
		{
			name: "five_attrs",
			attrs: map[string]string{
				"service": "web",
				"env":     "prod",
				"cluster": "us-east-1",
				"region":  "us",
				"team":    "platform",
			},
			want: "cluster=us-east-1,env=prod,region=us,service=web,team=platform",
		},
		{
			name:  "fifty_attrs",
			attrs: generateAttrs(50),
		},
		{
			name:  "hundred_attrs",
			attrs: generateAttrs(100),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSeriesKey(tt.attrs)

			if tt.want != "" {
				if got != tt.want {
					t.Errorf("buildSeriesKey() = %q, want %q", got, tt.want)
				}
			} else if len(tt.attrs) > 0 {
				// For generated attrs, verify correctness by rebuilding expected value
				expected := referenceSeriesKey(tt.attrs)
				if got != expected {
					t.Errorf("buildSeriesKey() = %q, want %q", got, expected)
				}
			}

			// Verify determinism: calling again should produce the same result
			got2 := buildSeriesKey(tt.attrs)
			if got != got2 {
				t.Errorf("buildSeriesKey() not deterministic: %q != %q", got, got2)
			}
		})
	}
}

// TestBuildSeriesKey_PoolReuse verifies the pool is actually being used across calls.
func TestBuildSeriesKey_PoolReuse(t *testing.T) {
	before := seriesKeyPoolGets.Load()

	for i := 0; i < 100; i++ {
		attrs := map[string]string{
			"key1": fmt.Sprintf("val%d", i),
			"key2": "static",
		}
		_ = buildSeriesKey(attrs)
	}

	after := seriesKeyPoolGets.Load()
	if after <= before {
		t.Error("expected pool gets counter to increase")
	}
}

// generateAttrs creates a map with n key-value pairs.
func generateAttrs(n int) map[string]string {
	attrs := make(map[string]string, n)
	for i := 0; i < n; i++ {
		attrs[fmt.Sprintf("key_%03d", i)] = fmt.Sprintf("value_%03d", i)
	}
	return attrs
}

// referenceSeriesKey is a simple reference implementation for verification.
func referenceSeriesKey(attrs map[string]string) string {
	if len(attrs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(attrs[k])
	}
	return sb.String()
}
