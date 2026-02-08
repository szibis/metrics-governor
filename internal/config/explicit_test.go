package config

import (
	"flag"
	"os"
	"testing"
)

// TestExplicitFlags verifies that ExplicitFlags returns only the flags
// that were explicitly set on the command line via flag.Visit.
func TestExplicitFlags(t *testing.T) {
	// Save and restore original flag.CommandLine
	origCommandLine := flag.CommandLine
	defer func() { flag.CommandLine = origCommandLine }()

	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	// Register some flags
	var grpc string
	var bufSize int
	flag.StringVar(&grpc, "grpc-listen", ":4317", "gRPC listen address")
	flag.IntVar(&bufSize, "buffer-size", 10000, "buffer size")

	// Parse with only --grpc-listen set
	if err := flag.CommandLine.Parse([]string{"-grpc-listen", ":5317"}); err != nil {
		t.Fatalf("parse error: %v", err)
	}

	explicit := ExplicitFlags()

	// grpc-listen was explicitly set
	if !explicit["grpc-listen"] {
		t.Error("expected grpc-listen to be in ExplicitFlags")
	}

	// buffer-size was NOT set — should not appear
	if explicit["buffer-size"] {
		t.Error("buffer-size should NOT be in ExplicitFlags when not passed")
	}
}

// TestExplicitFlagsEmpty verifies ExplicitFlags returns empty when no flags are passed.
func TestExplicitFlagsEmpty(t *testing.T) {
	origCommandLine := flag.CommandLine
	defer func() { flag.CommandLine = origCommandLine }()

	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	flag.String("some-flag", "default", "unused")
	if err := flag.CommandLine.Parse([]string{}); err != nil {
		t.Fatalf("parse error: %v", err)
	}

	explicit := ExplicitFlags()
	if len(explicit) != 0 {
		t.Errorf("expected empty map, got %v", explicit)
	}
}

// TestExplicitYAMLFieldsNested verifies that nested YAML paths are converted
// to CLI flag names via the yamlPathToCLIFlag mapping.
func TestExplicitYAMLFieldsNested(t *testing.T) {
	yamlData := []byte(`
exporter:
  endpoint: "otel:4317"
  queue:
    workers: 4
`)

	explicit, err := ExplicitYAMLFields(yamlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// "exporter.endpoint" → "exporter-endpoint"
	if !explicit["exporter-endpoint"] {
		t.Error("expected exporter-endpoint in explicit fields")
	}

	// "exporter.queue.workers" → "queue-workers"
	if !explicit["queue-workers"] {
		t.Error("expected queue-workers in explicit fields")
	}

	// Raw YAML paths should also be recorded
	if !explicit["exporter.endpoint"] {
		t.Error("expected raw path exporter.endpoint in explicit fields")
	}
	if !explicit["exporter.queue.workers"] {
		t.Error("expected raw path exporter.queue.workers in explicit fields")
	}
}

// TestExplicitYAMLFieldsTopLevel verifies top-level YAML fields like profile.
func TestExplicitYAMLFieldsTopLevel(t *testing.T) {
	yamlData := []byte(`
profile: balanced
parallelism: 4
`)

	explicit, err := ExplicitYAMLFields(yamlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !explicit["profile"] {
		t.Error("expected 'profile' in explicit fields")
	}
	if !explicit["parallelism"] {
		t.Error("expected 'parallelism' in explicit fields")
	}
}

// TestExplicitYAMLFieldsMemory verifies memory section YAML paths.
func TestExplicitYAMLFieldsMemory(t *testing.T) {
	yamlData := []byte(`
memory:
  limit_ratio: 0.8
`)

	explicit, err := ExplicitYAMLFields(yamlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !explicit["memory-limit-ratio"] {
		t.Error("expected 'memory-limit-ratio' in explicit fields")
	}
	if !explicit["memory.limit_ratio"] {
		t.Error("expected raw path 'memory.limit_ratio' in explicit fields")
	}
}

// TestExplicitYAMLFieldsInvalidYAML verifies error on malformed YAML.
func TestExplicitYAMLFieldsInvalidYAML(t *testing.T) {
	yamlData := []byte(`
not: valid: yaml: [broken
`)
	_, err := ExplicitYAMLFields(yamlData)
	if err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

// TestExplicitYAMLFieldsEmpty verifies empty YAML returns empty map.
func TestExplicitYAMLFieldsEmpty(t *testing.T) {
	yamlData := []byte(`{}`)

	explicit, err := ExplicitYAMLFields(yamlData)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(explicit) != 0 {
		t.Errorf("expected empty map for empty YAML, got %v", explicit)
	}
}

// TestYamlPathToCLIFlag verifies the direct mapping of YAML paths to CLI flag names.
func TestYamlPathToCLIFlag(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{
			name:     "top-level profile",
			path:     "profile",
			expected: "profile",
		},
		{
			name:     "exporter endpoint",
			path:     "exporter.endpoint",
			expected: "exporter-endpoint",
		},
		{
			name:     "queue workers",
			path:     "exporter.queue.workers",
			expected: "queue-workers",
		},
		{
			name:     "memory limit_ratio",
			path:     "memory.limit_ratio",
			expected: "memory-limit-ratio",
		},
		{
			name:     "buffer size",
			path:     "buffer.size",
			expected: "buffer-size",
		},
		{
			name:     "stats address",
			path:     "stats.address",
			expected: "stats-addr",
		},
		{
			name:     "receiver grpc address",
			path:     "receiver.grpc.address",
			expected: "grpc-listen",
		},
		{
			name:     "receiver http address",
			path:     "receiver.http.address",
			expected: "http-listen",
		},
		{
			name:     "exporter compression type",
			path:     "exporter.compression.type",
			expected: "exporter-compression",
		},
		{
			name:     "limits dry_run",
			path:     "limits.dry_run",
			expected: "limits-dry-run",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := yamlPathToCLIFlag(tc.path)
			if got != tc.expected {
				t.Errorf("yamlPathToCLIFlag(%q) = %q, want %q", tc.path, got, tc.expected)
			}
		})
	}
}

// TestYamlPathToCLIFlagFallback verifies that unknown YAML paths use the
// fallback logic: underscores become hyphens.
func TestYamlPathToCLIFlagFallback(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"unknown_field", "unknown-field"},
		{"some.nested_unknown.path_here", "some.nested-unknown.path-here"},
		{"no_underscores_at_all", "no-underscores-at-all"},
		{"already-hyphenated", "already-hyphenated"},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := yamlPathToCLIFlag(tc.path)
			if got != tc.expected {
				t.Errorf("yamlPathToCLIFlag(%q) = %q, want %q", tc.path, got, tc.expected)
			}
		})
	}
}

// TestMergeExplicitFields verifies that CLI fields override YAML fields
// and both sets are preserved in the merged result.
func TestMergeExplicitFields(t *testing.T) {
	cli := map[string]bool{
		"exporter-endpoint": true,
		"buffer-size":       true,
	}
	yamlFields := map[string]bool{
		"exporter-endpoint": true, // also in CLI — CLI wins
		"queue-workers":     true,
		"profile":           true,
	}

	merged := MergeExplicitFields(cli, yamlFields)

	// All keys from both maps should be present
	expectedKeys := []string{"exporter-endpoint", "buffer-size", "queue-workers", "profile"}
	for _, key := range expectedKeys {
		if !merged[key] {
			t.Errorf("expected key %q in merged map", key)
		}
	}

	// Total count should be 4 unique keys
	if len(merged) != 4 {
		t.Errorf("expected 4 keys in merged map, got %d", len(merged))
	}
}

// TestMergeExplicitFieldsEmpty verifies merging with empty maps.
func TestMergeExplicitFieldsEmpty(t *testing.T) {
	// Both empty
	merged := MergeExplicitFields(map[string]bool{}, map[string]bool{})
	if len(merged) != 0 {
		t.Errorf("expected empty merged map, got %v", merged)
	}

	// CLI empty, YAML has values
	yamlOnly := map[string]bool{"profile": true}
	merged = MergeExplicitFields(map[string]bool{}, yamlOnly)
	if !merged["profile"] {
		t.Error("expected profile in merged map from YAML")
	}

	// YAML empty, CLI has values
	cliOnly := map[string]bool{"buffer-size": true}
	merged = MergeExplicitFields(cliOnly, map[string]bool{})
	if !merged["buffer-size"] {
		t.Error("expected buffer-size in merged map from CLI")
	}
}

// TestMergeExplicitFieldsNilMaps verifies merging with nil maps does not panic.
func TestMergeExplicitFieldsNilMaps(t *testing.T) {
	// nil + nil should not panic
	merged := MergeExplicitFields(nil, nil)
	if merged == nil {
		t.Error("expected non-nil map even with nil inputs")
	}
	if len(merged) != 0 {
		t.Errorf("expected empty map, got %v", merged)
	}

	// nil + populated
	merged = MergeExplicitFields(nil, map[string]bool{"a": true})
	if !merged["a"] {
		t.Error("expected 'a' from YAML map")
	}

	// populated + nil
	merged = MergeExplicitFields(map[string]bool{"b": true}, nil)
	if !merged["b"] {
		t.Error("expected 'b' from CLI map")
	}
}
