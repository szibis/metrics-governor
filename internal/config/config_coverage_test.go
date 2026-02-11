package config

import (
	"flag"
	"os"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// PerformanceConfig (0% coverage)
// ---------------------------------------------------------------------------

func TestPerformanceConfig_Coverage(t *testing.T) {
	cfg := &Config{
		ExportConcurrency:    8,
		StringInterning:      true,
		InternMaxValueLength: 256,
	}

	pc := cfg.PerformanceConfig()

	if pc.ExportConcurrency != 8 {
		t.Errorf("expected ExportConcurrency 8, got %d", pc.ExportConcurrency)
	}
	if !pc.StringInterning {
		t.Error("expected StringInterning true")
	}
	if pc.InternMaxValueLength != 256 {
		t.Errorf("expected InternMaxValueLength 256, got %d", pc.InternMaxValueLength)
	}
}

func TestPerformanceConfig_Defaults(t *testing.T) {
	cfg := &Config{}

	pc := cfg.PerformanceConfig()

	if pc.ExportConcurrency != 0 {
		t.Errorf("expected ExportConcurrency 0 for zero config, got %d", pc.ExportConcurrency)
	}
	if pc.StringInterning {
		t.Error("expected StringInterning false for zero config")
	}
	if pc.InternMaxValueLength != 0 {
		t.Errorf("expected InternMaxValueLength 0 for zero config, got %d", pc.InternMaxValueLength)
	}
}

// ---------------------------------------------------------------------------
// CardinalityConfig (0% coverage)
// ---------------------------------------------------------------------------

func TestCardinalityConfig_Coverage(t *testing.T) {
	cfg := &Config{
		CardinalityMode:          "bloom",
		CardinalityExpectedItems: 100000,
		CardinalityFPRate:        0.01,
	}

	cc := cfg.CardinalityConfig()

	if cc.Mode != "bloom" {
		t.Errorf("expected Mode 'bloom', got '%s'", cc.Mode)
	}
	if cc.ExpectedItems != 100000 {
		t.Errorf("expected ExpectedItems 100000, got %d", cc.ExpectedItems)
	}
	if cc.FPRate != 0.01 {
		t.Errorf("expected FPRate 0.01, got %f", cc.FPRate)
	}
}

func TestCardinalityConfig_Exact(t *testing.T) {
	cfg := &Config{
		CardinalityMode:          "exact",
		CardinalityExpectedItems: 0,
		CardinalityFPRate:        0,
	}

	cc := cfg.CardinalityConfig()

	if cc.Mode != "exact" {
		t.Errorf("expected Mode 'exact', got '%s'", cc.Mode)
	}
	if cc.ExpectedItems != 0 {
		t.Errorf("expected ExpectedItems 0, got %d", cc.ExpectedItems)
	}
}

// ---------------------------------------------------------------------------
// BloomPersistenceConfig (0% coverage)
// ---------------------------------------------------------------------------

func TestBloomPersistenceConfig_Coverage(t *testing.T) {
	cfg := &Config{
		BloomPersistenceEnabled:          true,
		BloomPersistencePath:             "/data/bloom",
		BloomPersistenceSaveInterval:     5 * time.Minute,
		BloomPersistenceStateTTL:         24 * time.Hour,
		BloomPersistenceCleanupInterval:  1 * time.Hour,
		BloomPersistenceMaxSize:          1073741824,
		BloomPersistenceMaxMemory:        536870912,
		BloomPersistenceCompression:      true,
		BloomPersistenceCompressionLevel: 6,
	}

	bc := cfg.BloomPersistenceConfig()

	if !bc.Enabled {
		t.Error("expected Enabled true")
	}
	if bc.Path != "/data/bloom" {
		t.Errorf("expected Path '/data/bloom', got '%s'", bc.Path)
	}
	if bc.SaveInterval != 5*time.Minute {
		t.Errorf("expected SaveInterval 5m, got %v", bc.SaveInterval)
	}
	if bc.StateTTL != 24*time.Hour {
		t.Errorf("expected StateTTL 24h, got %v", bc.StateTTL)
	}
	if bc.CleanupInterval != 1*time.Hour {
		t.Errorf("expected CleanupInterval 1h, got %v", bc.CleanupInterval)
	}
	if bc.MaxSize != 1073741824 {
		t.Errorf("expected MaxSize 1073741824, got %d", bc.MaxSize)
	}
	if bc.MaxMemory != 536870912 {
		t.Errorf("expected MaxMemory 536870912, got %d", bc.MaxMemory)
	}
	if !bc.Compression {
		t.Error("expected Compression true")
	}
	if bc.CompressionLevel != 6 {
		t.Errorf("expected CompressionLevel 6, got %d", bc.CompressionLevel)
	}
}

func TestBloomPersistenceConfig_Disabled(t *testing.T) {
	cfg := &Config{}

	bc := cfg.BloomPersistenceConfig()

	if bc.Enabled {
		t.Error("expected Enabled false for zero config")
	}
	if bc.Path != "" {
		t.Errorf("expected empty Path, got '%s'", bc.Path)
	}
}

// ---------------------------------------------------------------------------
// applyFlagOverrides: uncovered flag paths (72.8% coverage)
// Test the flags not yet covered by config_extended_test.go:
// - performance flags: export-concurrency, string-interning, intern-max-value-length
// - cardinality flags: cardinality-mode, cardinality-expected-items, cardinality-fp-rate
// - bloom persistence flags
// - fastqueue flags: queue-inmemory-blocks, queue-chunk-size, queue-meta-sync, queue-stale-flush
// - backoff flags: queue-backoff-enabled, queue-backoff-multiplier
// - circuit breaker flags: queue-circuit-breaker-enabled, queue-circuit-breaker-threshold, queue-circuit-breaker-reset-timeout
// - help/version flags
// ---------------------------------------------------------------------------

func TestParseFlagsPerformance(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-string-interning",
		"-intern-max-value-length", "512",
	}

	cfg := ParseFlags()

	if !cfg.StringInterning {
		t.Error("expected StringInterning true")
	}
	if cfg.InternMaxValueLength != 512 {
		t.Errorf("expected InternMaxValueLength 512, got %d", cfg.InternMaxValueLength)
	}
}

func TestParseFlagsCardinality(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-cardinality-mode", "bloom",
		"-cardinality-expected-items", "50000",
		"-cardinality-fp-rate", "0.005",
	}

	cfg := ParseFlags()

	if cfg.CardinalityMode != "bloom" {
		t.Errorf("expected CardinalityMode 'bloom', got '%s'", cfg.CardinalityMode)
	}
	if cfg.CardinalityExpectedItems != 50000 {
		t.Errorf("expected CardinalityExpectedItems 50000, got %d", cfg.CardinalityExpectedItems)
	}
	if cfg.CardinalityFPRate != 0.005 {
		t.Errorf("expected CardinalityFPRate 0.005, got %f", cfg.CardinalityFPRate)
	}
}

func TestParseFlagsBloomPersistence(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-bloom-persistence-enabled",
		"-bloom-persistence-path", "/data/bloom",
		"-bloom-persistence-save-interval", "5m",
		"-bloom-persistence-state-ttl", "24h",
		"-bloom-persistence-cleanup-interval", "1h",
		"-bloom-persistence-max-size", "1073741824",
		"-bloom-persistence-max-memory", "536870912",
		"-bloom-persistence-compression",
		"-bloom-persistence-compression-level", "6",
	}

	cfg := ParseFlags()

	if !cfg.BloomPersistenceEnabled {
		t.Error("expected BloomPersistenceEnabled true")
	}
	if cfg.BloomPersistencePath != "/data/bloom" {
		t.Errorf("expected BloomPersistencePath '/data/bloom', got '%s'", cfg.BloomPersistencePath)
	}
	if cfg.BloomPersistenceSaveInterval != 5*time.Minute {
		t.Errorf("expected BloomPersistenceSaveInterval 5m, got %v", cfg.BloomPersistenceSaveInterval)
	}
	if cfg.BloomPersistenceStateTTL != 24*time.Hour {
		t.Errorf("expected BloomPersistenceStateTTL 24h, got %v", cfg.BloomPersistenceStateTTL)
	}
	if cfg.BloomPersistenceCleanupInterval != 1*time.Hour {
		t.Errorf("expected BloomPersistenceCleanupInterval 1h, got %v", cfg.BloomPersistenceCleanupInterval)
	}
	if cfg.BloomPersistenceMaxSize != 1073741824 {
		t.Errorf("expected BloomPersistenceMaxSize 1073741824, got %d", cfg.BloomPersistenceMaxSize)
	}
	if cfg.BloomPersistenceMaxMemory != 536870912 {
		t.Errorf("expected BloomPersistenceMaxMemory 536870912, got %d", cfg.BloomPersistenceMaxMemory)
	}
	if !cfg.BloomPersistenceCompression {
		t.Error("expected BloomPersistenceCompression true")
	}
	if cfg.BloomPersistenceCompressionLevel != 6 {
		t.Errorf("expected BloomPersistenceCompressionLevel 6, got %d", cfg.BloomPersistenceCompressionLevel)
	}
}

func TestParseFlagsFastQueue(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-queue-inmemory-blocks", "512",
		"-queue-chunk-size", "1073741824",
		"-queue-meta-sync", "2s",
		"-queue-stale-flush", "10s",
	}

	cfg := ParseFlags()

	if cfg.QueueInmemoryBlocks != 512 {
		t.Errorf("expected QueueInmemoryBlocks 512, got %d", cfg.QueueInmemoryBlocks)
	}
	if cfg.QueueChunkSize != 1073741824 {
		t.Errorf("expected QueueChunkSize 1073741824, got %d", cfg.QueueChunkSize)
	}
	if cfg.QueueMetaSyncInterval != 2*time.Second {
		t.Errorf("expected QueueMetaSyncInterval 2s, got %v", cfg.QueueMetaSyncInterval)
	}
	if cfg.QueueStaleFlushInterval != 10*time.Second {
		t.Errorf("expected QueueStaleFlushInterval 10s, got %v", cfg.QueueStaleFlushInterval)
	}
}

func TestParseFlagsQueueBackoff(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-queue-backoff-enabled",
	}

	cfg := ParseFlags()

	if !cfg.QueueBackoffEnabled {
		t.Error("expected QueueBackoffEnabled true")
	}
}

func TestParseFlagsQueueCircuitBreaker(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-queue-circuit-breaker-reset-timeout", "45s",
	}

	cfg := ParseFlags()

	if cfg.QueueCircuitBreakerResetTimeout != 45*time.Second {
		t.Errorf("expected QueueCircuitBreakerResetTimeout 45s, got %v", cfg.QueueCircuitBreakerResetTimeout)
	}
}

func TestParseFlagsMemoryLimit(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-memory-limit-ratio", "0.85",
	}

	cfg := ParseFlags()

	if cfg.MemoryLimitRatio != 0.85 {
		t.Errorf("expected MemoryLimitRatio 0.85, got %f", cfg.MemoryLimitRatio)
	}
}

func TestParseFlagsHelpAndVersion(t *testing.T) {
	t.Run("help flag", func(t *testing.T) {
		flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

		oldArgs := os.Args
		defer func() { os.Args = oldArgs }()

		os.Args = []string{
			"test",
			"-help",
		}

		cfg := ParseFlags()

		if !cfg.ShowHelp {
			t.Error("expected ShowHelp true")
		}
	})

	t.Run("version flag", func(t *testing.T) {
		flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

		oldArgs := os.Args
		defer func() { os.Args = oldArgs }()

		os.Args = []string{
			"test",
			"-version",
		}

		cfg := ParseFlags()

		if !cfg.ShowVersion {
			t.Error("expected ShowVersion true")
		}
	})
}

func TestParseFlagsBufferAndStats(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-buffer-size", "20000",
		"-flush-interval", "15s",
		"-batch-size", "5000",
		"-stats-addr", "localhost:9100",
		"-stats-labels", "env=prod",
		"-limits-config", "/etc/limits.yaml",
		"-limits-dry-run",
		"-rule-cache-max-size", "5000",
	}

	cfg := ParseFlags()

	if cfg.BufferSize != 20000 {
		t.Errorf("expected BufferSize 20000, got %d", cfg.BufferSize)
	}
	if cfg.FlushInterval != 15*time.Second {
		t.Errorf("expected FlushInterval 15s, got %v", cfg.FlushInterval)
	}
	if cfg.MaxBatchSize != 5000 {
		t.Errorf("expected MaxBatchSize 5000, got %d", cfg.MaxBatchSize)
	}
	if cfg.StatsAddr != "localhost:9100" {
		t.Errorf("expected StatsAddr 'localhost:9100', got '%s'", cfg.StatsAddr)
	}
	if cfg.StatsLabels != "env=prod" {
		t.Errorf("expected StatsLabels 'env=prod', got '%s'", cfg.StatsLabels)
	}
	if cfg.LimitsConfig != "/etc/limits.yaml" {
		t.Errorf("expected LimitsConfig '/etc/limits.yaml', got '%s'", cfg.LimitsConfig)
	}
	if !cfg.LimitsDryRun {
		t.Error("expected LimitsDryRun true")
	}
	if cfg.RuleCacheMaxSize != 5000 {
		t.Errorf("expected RuleCacheMaxSize 5000, got %d", cfg.RuleCacheMaxSize)
	}
}

// ---------------------------------------------------------------------------
// Duration.UnmarshalYAML edge cases (72.7% coverage)
// ---------------------------------------------------------------------------

func TestDuration_UnmarshalYAML_EdgeCases(t *testing.T) {
	t.Run("empty string", func(t *testing.T) {
		var d Duration
		node := &yaml.Node{Kind: yaml.ScalarNode, Value: ""}
		err := d.UnmarshalYAML(node)
		if err != nil {
			t.Errorf("expected nil error for empty string, got %v", err)
		}
		if d != 0 {
			t.Errorf("expected 0 duration, got %v", time.Duration(d))
		}
	})

	t.Run("valid duration string", func(t *testing.T) {
		var d Duration
		node := &yaml.Node{Kind: yaml.ScalarNode, Value: "30s"}
		err := d.UnmarshalYAML(node)
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
		if time.Duration(d) != 30*time.Second {
			t.Errorf("expected 30s, got %v", time.Duration(d))
		}
	})

	t.Run("complex duration string", func(t *testing.T) {
		var d Duration
		node := &yaml.Node{Kind: yaml.ScalarNode, Value: "1h30m"}
		err := d.UnmarshalYAML(node)
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
		if time.Duration(d) != 90*time.Minute {
			t.Errorf("expected 1h30m, got %v", time.Duration(d))
		}
	})

	t.Run("nanosecond duration", func(t *testing.T) {
		var d Duration
		node := &yaml.Node{Kind: yaml.ScalarNode, Value: "100ns"}
		err := d.UnmarshalYAML(node)
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
		if time.Duration(d) != 100*time.Nanosecond {
			t.Errorf("expected 100ns, got %v", time.Duration(d))
		}
	})

	t.Run("invalid duration string", func(t *testing.T) {
		var d Duration
		node := &yaml.Node{Kind: yaml.ScalarNode, Value: "notaduration"}
		err := d.UnmarshalYAML(node)
		if err == nil {
			t.Error("expected error for invalid duration string")
		}
	})

	t.Run("non-string node", func(t *testing.T) {
		var d Duration
		// Sequence node cannot be decoded to string
		node := &yaml.Node{Kind: yaml.SequenceNode}
		err := d.UnmarshalYAML(node)
		if err == nil {
			t.Error("expected error for non-string node")
		}
	})
}

// ---------------------------------------------------------------------------
// Duration.MarshalYAML (0% coverage)
// ---------------------------------------------------------------------------

func TestDuration_MarshalYAML(t *testing.T) {
	tests := []struct {
		name     string
		duration Duration
		want     string
	}{
		{
			name:     "zero",
			duration: Duration(0),
			want:     "0s",
		},
		{
			name:     "seconds",
			duration: Duration(30 * time.Second),
			want:     "30s",
		},
		{
			name:     "minutes",
			duration: Duration(5 * time.Minute),
			want:     "5m0s",
		},
		{
			name:     "hours",
			duration: Duration(2 * time.Hour),
			want:     "2h0m0s",
		},
		{
			name:     "complex",
			duration: Duration(1*time.Hour + 30*time.Minute + 15*time.Second),
			want:     "1h30m15s",
		},
		{
			name:     "milliseconds",
			duration: Duration(500 * time.Millisecond),
			want:     "500ms",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.duration.MarshalYAML()
			if err != nil {
				t.Fatalf("MarshalYAML error: %v", err)
			}
			str, ok := got.(string)
			if !ok {
				t.Fatalf("expected string, got %T", got)
			}
			if str != tt.want {
				t.Errorf("MarshalYAML() = %q, want %q", str, tt.want)
			}
		})
	}
}

func TestDuration_MarshalYAML_RoundTrip(t *testing.T) {
	durations := []time.Duration{
		0,
		1 * time.Second,
		5 * time.Minute,
		2 * time.Hour,
		100 * time.Millisecond,
		24 * time.Hour,
	}

	for _, d := range durations {
		dur := Duration(d)

		// Marshal
		iface, err := dur.MarshalYAML()
		if err != nil {
			t.Fatalf("MarshalYAML error for %v: %v", d, err)
		}
		str := iface.(string)

		// Unmarshal
		var result Duration
		node := &yaml.Node{Kind: yaml.ScalarNode, Value: str}
		err = result.UnmarshalYAML(node)
		if err != nil {
			t.Fatalf("UnmarshalYAML error for %q: %v", str, err)
		}

		if time.Duration(result) != d {
			t.Errorf("roundtrip failed: %v -> %q -> %v", d, str, time.Duration(result))
		}
	}
}
