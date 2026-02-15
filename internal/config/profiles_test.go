package config

import (
	"testing"
	"time"
)

func TestGetProfile(t *testing.T) {
	tests := []struct {
		name    string
		profile ProfileName
		wantErr bool
	}{
		{"minimal", ProfileMinimal, false},
		{"balanced", ProfileBalanced, false},
		{"safety", ProfileSafety, false},
		{"observable", ProfileObservable, false},
		{"resilient", ProfileResilient, false},
		{"performance", ProfilePerformance, false},
		{"unknown", ProfileName("unknown"), true},
		{"empty", ProfileName(""), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := GetProfile(tt.profile)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if p != nil {
					t.Fatal("expected nil profile on error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p == nil {
				t.Fatal("expected non-nil profile")
			}
			if p.Name != tt.profile {
				t.Errorf("expected name %q, got %q", tt.profile, p.Name)
			}
		})
	}
}

func TestIsValidProfile(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"empty is valid", "", true},
		{"minimal", "minimal", true},
		{"balanced", "balanced", true},
		{"safety", "safety", true},
		{"observable", "observable", true},
		{"resilient", "resilient", true},
		{"performance", "performance", true},
		{"unknown", "unknown", false},
		{"capitalized", "Minimal", false},
		{"with spaces", " minimal ", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidProfile(tt.input)
			if got != tt.want {
				t.Errorf("IsValidProfile(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidProfileNames(t *testing.T) {
	names := ValidProfileNames()
	if len(names) != 6 {
		t.Fatalf("expected 6 profile names, got %d", len(names))
	}
	expected := map[ProfileName]bool{
		ProfileMinimal:     true,
		ProfileBalanced:    true,
		ProfileSafety:      true,
		ProfileObservable:  true,
		ProfileResilient:   true,
		ProfilePerformance: true,
	}
	for _, n := range names {
		if !expected[n] {
			t.Errorf("unexpected profile name: %q", n)
		}
	}
}

func TestApplyProfile_Minimal(t *testing.T) {
	cfg := DefaultConfig()
	err := ApplyProfile(cfg, ProfileMinimal, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.QueueWorkers != 1 {
		t.Errorf("QueueWorkers = %d, want 1", cfg.QueueWorkers)
	}
	if cfg.ExportConcurrency != 1 {
		t.Errorf("ExportConcurrency = %d, want 1", cfg.ExportConcurrency)
	}
	if cfg.QueueEnabled != false {
		t.Errorf("QueueEnabled = %v, want false", cfg.QueueEnabled)
	}
	if cfg.BufferBatchAutoTuneEnabled != false {
		t.Errorf("BufferBatchAutoTuneEnabled = %v, want false", cfg.BufferBatchAutoTuneEnabled)
	}
	if cfg.QueueAdaptiveWorkersEnabled != false {
		t.Errorf("QueueAdaptiveWorkersEnabled = %v, want false", cfg.QueueAdaptiveWorkersEnabled)
	}
	if cfg.BufferSize != 1000 {
		t.Errorf("BufferSize = %d, want 1000", cfg.BufferSize)
	}
	if cfg.QueuePipelineSplitEnabled != false {
		t.Errorf("QueuePipelineSplitEnabled = %v, want false", cfg.QueuePipelineSplitEnabled)
	}
	if cfg.ExporterCompression != "none" {
		t.Errorf("ExporterCompression = %q, want %q", cfg.ExporterCompression, "none")
	}
	if cfg.StringInterning != false {
		t.Errorf("StringInterning = %v, want false", cfg.StringInterning)
	}
	if cfg.QueueBackoffEnabled != false {
		t.Errorf("QueueBackoffEnabled = %v, want false", cfg.QueueBackoffEnabled)
	}
	if cfg.QueueCircuitBreakerEnabled != false {
		t.Errorf("QueueCircuitBreakerEnabled = %v, want false", cfg.QueueCircuitBreakerEnabled)
	}
	if cfg.ExporterPrewarmConnections != false {
		t.Errorf("ExporterPrewarmConnections = %v, want false", cfg.ExporterPrewarmConnections)
	}
	if cfg.MaxBatchSize != 200 {
		t.Errorf("MaxBatchSize = %d, want 200", cfg.MaxBatchSize)
	}
	if cfg.FlushInterval != 10*time.Second {
		t.Errorf("FlushInterval = %v, want 10s", cfg.FlushInterval)
	}
	if cfg.BufferFullPolicy != "reject" {
		t.Errorf("BufferFullPolicy = %q, want %q", cfg.BufferFullPolicy, "reject")
	}
	if cfg.QueueType != "memory" {
		t.Errorf("QueueType = %q, want %q", cfg.QueueType, "memory")
	}
	if cfg.LimitsDryRun != true {
		t.Errorf("LimitsDryRun = %v, want true", cfg.LimitsDryRun)
	}
	if cfg.CardinalityMode != "exact" {
		t.Errorf("CardinalityMode = %q, want %q", cfg.CardinalityMode, "exact")
	}
	if cfg.BloomPersistenceEnabled != false {
		t.Errorf("BloomPersistenceEnabled = %v, want false", cfg.BloomPersistenceEnabled)
	}
}

func TestApplyProfile_Balanced(t *testing.T) {
	cfg := DefaultConfig()
	err := ApplyProfile(cfg, ProfileBalanced, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.QueueEnabled != true {
		t.Errorf("QueueEnabled = %v, want true", cfg.QueueEnabled)
	}
	if cfg.BufferBatchAutoTuneEnabled != true {
		t.Errorf("BufferBatchAutoTuneEnabled = %v, want true", cfg.BufferBatchAutoTuneEnabled)
	}
	if cfg.QueueAdaptiveWorkersEnabled != true {
		t.Errorf("QueueAdaptiveWorkersEnabled = %v, want true", cfg.QueueAdaptiveWorkersEnabled)
	}
	if cfg.BufferSize != 5000 {
		t.Errorf("BufferSize = %d, want 5000", cfg.BufferSize)
	}
	if cfg.QueuePipelineSplitEnabled != false {
		t.Errorf("QueuePipelineSplitEnabled = %v, want false", cfg.QueuePipelineSplitEnabled)
	}
	if cfg.ExporterCompression != "snappy" {
		t.Errorf("ExporterCompression = %q, want %q", cfg.ExporterCompression, "snappy")
	}
	if cfg.StringInterning != true {
		t.Errorf("StringInterning = %v, want true", cfg.StringInterning)
	}
	if cfg.QueueBackoffEnabled != true {
		t.Errorf("QueueBackoffEnabled = %v, want true", cfg.QueueBackoffEnabled)
	}
	if cfg.QueueCircuitBreakerEnabled != true {
		t.Errorf("QueueCircuitBreakerEnabled = %v, want true", cfg.QueueCircuitBreakerEnabled)
	}
	if cfg.ExporterPrewarmConnections != true {
		t.Errorf("ExporterPrewarmConnections = %v, want true", cfg.ExporterPrewarmConnections)
	}
	if cfg.QueueType != "memory" {
		t.Errorf("QueueType = %q, want %q", cfg.QueueType, "memory")
	}
	if cfg.CardinalityMode != "bloom" {
		t.Errorf("CardinalityMode = %q, want %q", cfg.CardinalityMode, "bloom")
	}
	if cfg.LimitsDryRun != false {
		t.Errorf("LimitsDryRun = %v, want false", cfg.LimitsDryRun)
	}
	if cfg.MaxBatchSize != 500 {
		t.Errorf("MaxBatchSize = %d, want 500", cfg.MaxBatchSize)
	}
	if cfg.FlushInterval != 5*time.Second {
		t.Errorf("FlushInterval = %v, want 5s", cfg.FlushInterval)
	}
	if cfg.QueueMaxConcurrentSends != 4 {
		t.Errorf("QueueMaxConcurrentSends = %d, want 4", cfg.QueueMaxConcurrentSends)
	}
}

func TestApplyProfile_Performance(t *testing.T) {
	cfg := DefaultConfig()
	err := ApplyProfile(cfg, ProfilePerformance, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.QueuePipelineSplitEnabled != true {
		t.Errorf("QueuePipelineSplitEnabled = %v, want true", cfg.QueuePipelineSplitEnabled)
	}
	if cfg.QueueType != "disk" {
		t.Errorf("QueueType = %q, want %q", cfg.QueueType, "disk")
	}
	if cfg.BufferFullPolicy != "drop_oldest" {
		t.Errorf("BufferFullPolicy = %q, want %q", cfg.BufferFullPolicy, "drop_oldest")
	}
	if cfg.BufferSize != 50000 {
		t.Errorf("BufferSize = %d, want 50000", cfg.BufferSize)
	}
	if cfg.QueueEnabled != true {
		t.Errorf("QueueEnabled = %v, want true", cfg.QueueEnabled)
	}
	if cfg.BufferBatchAutoTuneEnabled != true {
		t.Errorf("BufferBatchAutoTuneEnabled = %v, want true", cfg.BufferBatchAutoTuneEnabled)
	}
	if cfg.QueueAdaptiveWorkersEnabled != true {
		t.Errorf("QueueAdaptiveWorkersEnabled = %v, want true", cfg.QueueAdaptiveWorkersEnabled)
	}
	if cfg.ExporterCompression != "zstd" {
		t.Errorf("ExporterCompression = %q, want %q", cfg.ExporterCompression, "zstd")
	}
	if cfg.QueueCompression != "snappy" {
		t.Errorf("QueueCompression = %q, want %q", cfg.QueueCompression, "snappy")
	}
	if cfg.StringInterning != true {
		t.Errorf("StringInterning = %v, want true", cfg.StringInterning)
	}
	if cfg.MaxBatchSize != 1000 {
		t.Errorf("MaxBatchSize = %d, want 1000", cfg.MaxBatchSize)
	}
	if cfg.FlushInterval != 2*time.Second {
		t.Errorf("FlushInterval = %v, want 2s", cfg.FlushInterval)
	}
	if cfg.QueueBackoffEnabled != true {
		t.Errorf("QueueBackoffEnabled = %v, want true", cfg.QueueBackoffEnabled)
	}
	if cfg.QueueCircuitBreakerEnabled != true {
		t.Errorf("QueueCircuitBreakerEnabled = %v, want true", cfg.QueueCircuitBreakerEnabled)
	}
	if cfg.ExporterPrewarmConnections != true {
		t.Errorf("ExporterPrewarmConnections = %v, want true", cfg.ExporterPrewarmConnections)
	}
	if cfg.CardinalityMode != "hybrid" {
		t.Errorf("CardinalityMode = %q, want %q", cfg.CardinalityMode, "hybrid")
	}
	if cfg.LimitsDryRun != false {
		t.Errorf("LimitsDryRun = %v, want false", cfg.LimitsDryRun)
	}
	if cfg.BloomPersistenceEnabled != true {
		t.Errorf("BloomPersistenceEnabled = %v, want true", cfg.BloomPersistenceEnabled)
	}
	if cfg.QueueMaxSize != 50000 {
		t.Errorf("QueueMaxSize = %d, want 50000", cfg.QueueMaxSize)
	}
}

func TestApplyProfile_Safety(t *testing.T) {
	cfg := DefaultConfig()
	err := ApplyProfile(cfg, ProfileSafety, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Disk queue — maximum data safety
	if cfg.QueueMode != "disk" {
		t.Errorf("QueueMode = %q, want %q", cfg.QueueMode, "disk")
	}
	if cfg.QueueType != "disk" {
		t.Errorf("QueueType = %q, want %q", cfg.QueueType, "disk")
	}
	if cfg.QueueEnabled != true {
		t.Errorf("QueueEnabled = %v, want true", cfg.QueueEnabled)
	}
	if cfg.QueueMaxBytes != 8589934592 {
		t.Errorf("QueueMaxBytes = %d, want 8589934592", cfg.QueueMaxBytes)
	}
	// Full stats for cost visibility
	if cfg.StatsLevel != "full" {
		t.Errorf("StatsLevel = %q, want %q", cfg.StatsLevel, "full")
	}
	// Zstd export compression
	if cfg.ExporterCompression != "zstd" {
		t.Errorf("ExporterCompression = %q, want %q", cfg.ExporterCompression, "zstd")
	}
	if cfg.QueueCompression != "snappy" {
		t.Errorf("QueueCompression = %q, want %q", cfg.QueueCompression, "snappy")
	}
	// Reject policy — never lose data
	if cfg.BufferFullPolicy != "reject" {
		t.Errorf("BufferFullPolicy = %q, want %q", cfg.BufferFullPolicy, "reject")
	}
	// Pipeline split off
	if cfg.QueuePipelineSplitEnabled != false {
		t.Errorf("QueuePipelineSplitEnabled = %v, want false", cfg.QueuePipelineSplitEnabled)
	}
	// Adaptive features on
	if cfg.QueueAdaptiveWorkersEnabled != true {
		t.Errorf("QueueAdaptiveWorkersEnabled = %v, want true", cfg.QueueAdaptiveWorkersEnabled)
	}
	if cfg.BufferBatchAutoTuneEnabled != true {
		t.Errorf("BufferBatchAutoTuneEnabled = %v, want true", cfg.BufferBatchAutoTuneEnabled)
	}
	// Bloom persistence on
	if cfg.BloomPersistenceEnabled != true {
		t.Errorf("BloomPersistenceEnabled = %v, want true", cfg.BloomPersistenceEnabled)
	}
	// Resilience on
	if cfg.QueueBackoffEnabled != true {
		t.Errorf("QueueBackoffEnabled = %v, want true", cfg.QueueBackoffEnabled)
	}
	if cfg.QueueCircuitBreakerEnabled != true {
		t.Errorf("QueueCircuitBreakerEnabled = %v, want true", cfg.QueueCircuitBreakerEnabled)
	}
	if cfg.ExporterPrewarmConnections != true {
		t.Errorf("ExporterPrewarmConnections = %v, want true", cfg.ExporterPrewarmConnections)
	}
	if cfg.LimitsDryRun != false {
		t.Errorf("LimitsDryRun = %v, want false", cfg.LimitsDryRun)
	}
	if cfg.StringInterning != true {
		t.Errorf("StringInterning = %v, want true", cfg.StringInterning)
	}
}

func TestApplyProfile_Observable(t *testing.T) {
	cfg := DefaultConfig()
	err := ApplyProfile(cfg, ProfileObservable, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Hybrid queue — memory-speed with disk spillover
	if cfg.QueueMode != "hybrid" {
		t.Errorf("QueueMode = %q, want %q", cfg.QueueMode, "hybrid")
	}
	if cfg.QueueType != "disk" {
		t.Errorf("QueueType = %q, want %q", cfg.QueueType, "disk")
	}
	if cfg.QueueEnabled != true {
		t.Errorf("QueueEnabled = %v, want true", cfg.QueueEnabled)
	}
	if cfg.QueueMaxBytes != 8589934592 {
		t.Errorf("QueueMaxBytes = %d, want 8589934592", cfg.QueueMaxBytes)
	}
	// Full stats for cost visibility
	if cfg.StatsLevel != "full" {
		t.Errorf("StatsLevel = %q, want %q", cfg.StatsLevel, "full")
	}
	// Zstd export compression
	if cfg.ExporterCompression != "zstd" {
		t.Errorf("ExporterCompression = %q, want %q", cfg.ExporterCompression, "zstd")
	}
	// Reject policy
	if cfg.BufferFullPolicy != "reject" {
		t.Errorf("BufferFullPolicy = %q, want %q", cfg.BufferFullPolicy, "reject")
	}
	// Pipeline split off
	if cfg.QueuePipelineSplitEnabled != false {
		t.Errorf("QueuePipelineSplitEnabled = %v, want false", cfg.QueuePipelineSplitEnabled)
	}
	// Adaptive features on
	if cfg.QueueAdaptiveWorkersEnabled != true {
		t.Errorf("QueueAdaptiveWorkersEnabled = %v, want true", cfg.QueueAdaptiveWorkersEnabled)
	}
	// Bloom persistence on
	if cfg.BloomPersistenceEnabled != true {
		t.Errorf("BloomPersistenceEnabled = %v, want true", cfg.BloomPersistenceEnabled)
	}
	// Memory ratios
	if cfg.MemoryLimitRatio != 0.80 {
		t.Errorf("MemoryLimitRatio = %f, want 0.80", cfg.MemoryLimitRatio)
	}
	if cfg.LimitsDryRun != false {
		t.Errorf("LimitsDryRun = %v, want false", cfg.LimitsDryRun)
	}
}

func TestApplyProfile_Resilient(t *testing.T) {
	cfg := DefaultConfig()
	err := ApplyProfile(cfg, ProfileResilient, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Hybrid queue
	if cfg.QueueMode != "hybrid" {
		t.Errorf("QueueMode = %q, want %q", cfg.QueueMode, "hybrid")
	}
	if cfg.QueueType != "disk" {
		t.Errorf("QueueType = %q, want %q", cfg.QueueType, "disk")
	}
	if cfg.QueueEnabled != true {
		t.Errorf("QueueEnabled = %v, want true", cfg.QueueEnabled)
	}
	if cfg.QueueMaxBytes != 12884901888 {
		t.Errorf("QueueMaxBytes = %d, want 12884901888", cfg.QueueMaxBytes)
	}
	// Basic stats — lower overhead
	if cfg.StatsLevel != "basic" {
		t.Errorf("StatsLevel = %q, want %q", cfg.StatsLevel, "basic")
	}
	// Snappy compression — fast
	if cfg.ExporterCompression != "snappy" {
		t.Errorf("ExporterCompression = %q, want %q", cfg.ExporterCompression, "snappy")
	}
	if cfg.QueueCompression != "snappy" {
		t.Errorf("QueueCompression = %q, want %q", cfg.QueueCompression, "snappy")
	}
	// Reject policy
	if cfg.BufferFullPolicy != "reject" {
		t.Errorf("BufferFullPolicy = %q, want %q", cfg.BufferFullPolicy, "reject")
	}
	// Pipeline split off
	if cfg.QueuePipelineSplitEnabled != false {
		t.Errorf("QueuePipelineSplitEnabled = %v, want false", cfg.QueuePipelineSplitEnabled)
	}
	// Adaptive features on
	if cfg.QueueAdaptiveWorkersEnabled != true {
		t.Errorf("QueueAdaptiveWorkersEnabled = %v, want true", cfg.QueueAdaptiveWorkersEnabled)
	}
	if cfg.BufferBatchAutoTuneEnabled != true {
		t.Errorf("BufferBatchAutoTuneEnabled = %v, want true", cfg.BufferBatchAutoTuneEnabled)
	}
	// Bloom persistence on
	if cfg.BloomPersistenceEnabled != true {
		t.Errorf("BloomPersistenceEnabled = %v, want true", cfg.BloomPersistenceEnabled)
	}
	// Resilience on
	if cfg.QueueBackoffEnabled != true {
		t.Errorf("QueueBackoffEnabled = %v, want true", cfg.QueueBackoffEnabled)
	}
	if cfg.QueueCircuitBreakerEnabled != true {
		t.Errorf("QueueCircuitBreakerEnabled = %v, want true", cfg.QueueCircuitBreakerEnabled)
	}
	if cfg.LimitsDryRun != false {
		t.Errorf("LimitsDryRun = %v, want false", cfg.LimitsDryRun)
	}
	if cfg.StringInterning != true {
		t.Errorf("StringInterning = %v, want true", cfg.StringInterning)
	}
}

func TestApplyProfile_UnknownProfile(t *testing.T) {
	cfg := DefaultConfig()
	err := ApplyProfile(cfg, ProfileName("unknown"), nil)
	if err == nil {
		t.Fatal("expected error for unknown profile")
	}
}

func TestApplyProfile_ExplicitFieldsNotOverridden(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueueWorkers = 42
	cfg.BufferSize = 9999
	cfg.QueueEnabled = true

	explicit := map[string]bool{
		"queue-workers": true,
		"buffer-size":   true,
		"queue-enabled": true,
	}

	err := ApplyProfile(cfg, ProfileMinimal, explicit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// These fields should NOT be changed because they are in explicitFields.
	if cfg.QueueWorkers != 42 {
		t.Errorf("QueueWorkers = %d, want 42 (should not be overridden)", cfg.QueueWorkers)
	}
	if cfg.BufferSize != 9999 {
		t.Errorf("BufferSize = %d, want 9999 (should not be overridden)", cfg.BufferSize)
	}
	if cfg.QueueEnabled != true {
		t.Errorf("QueueEnabled = %v, want true (should not be overridden)", cfg.QueueEnabled)
	}

	// But other fields should still be set by the profile.
	if cfg.ExportConcurrency != 1 {
		t.Errorf("ExportConcurrency = %d, want 1 (should be set by minimal profile)", cfg.ExportConcurrency)
	}
	if cfg.BufferBatchAutoTuneEnabled != false {
		t.Errorf("BufferBatchAutoTuneEnabled = %v, want false (should be set by minimal profile)", cfg.BufferBatchAutoTuneEnabled)
	}
}

func TestApplyProfile_ExplicitFieldsMultipleProfiles(t *testing.T) {
	tests := []struct {
		name           string
		profile        ProfileName
		explicitFields map[string]bool
		checkField     string
		checkFn        func(*Config) bool
	}{
		{
			name:           "performance explicit compression",
			profile:        ProfilePerformance,
			explicitFields: map[string]bool{"exporter-compression": true},
			checkField:     "ExporterCompression",
			checkFn: func(cfg *Config) bool {
				return cfg.ExporterCompression == "original"
			},
		},
		{
			name:           "balanced explicit queue-type",
			profile:        ProfileBalanced,
			explicitFields: map[string]bool{"queue-type": true},
			checkField:     "QueueType",
			checkFn: func(cfg *Config) bool {
				return cfg.QueueType == "disk"
			},
		},
		{
			name:           "minimal explicit buffer-full-policy",
			profile:        ProfileMinimal,
			explicitFields: map[string]bool{"buffer-full-policy": true},
			checkField:     "BufferFullPolicy",
			checkFn: func(cfg *Config) bool {
				return cfg.BufferFullPolicy == "drop_oldest"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			// Set the field to a known value before applying the profile.
			switch tt.checkField {
			case "ExporterCompression":
				cfg.ExporterCompression = "original"
			case "QueueType":
				cfg.QueueType = "disk"
			case "BufferFullPolicy":
				cfg.BufferFullPolicy = "drop_oldest"
			}

			err := ApplyProfile(cfg, tt.profile, tt.explicitFields)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.checkFn(cfg) {
				t.Errorf("explicit field %s was overridden by profile", tt.checkField)
			}
		})
	}
}

func TestApplyProfile_NilExplicitFields(t *testing.T) {
	cfg := DefaultConfig()
	err := ApplyProfile(cfg, ProfileBalanced, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should apply all balanced values without panicking.
	if cfg.QueueEnabled != true {
		t.Errorf("QueueEnabled = %v, want true", cfg.QueueEnabled)
	}
}

func TestApplyProfile_EmptyExplicitFields(t *testing.T) {
	cfg := DefaultConfig()
	err := ApplyProfile(cfg, ProfileMinimal, map[string]bool{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// All minimal values should be applied.
	if cfg.QueueWorkers != 1 {
		t.Errorf("QueueWorkers = %d, want 1", cfg.QueueWorkers)
	}
}

func TestDumpProfile(t *testing.T) {
	tests := []struct {
		name    string
		profile ProfileName
		wantErr bool
		// Substrings that must appear in the output.
		mustContain []string
	}{
		{
			name:    "minimal",
			profile: ProfileMinimal,
			wantErr: false,
			mustContain: []string{
				"Profile: minimal",
				"queue.workers",
				"1",
				"no disk required",
			},
		},
		{
			name:    "balanced",
			profile: ProfileBalanced,
			wantErr: false,
			mustContain: []string{
				"Profile: balanced",
				"(default)",
				"queue.enabled",
				"true",
			},
		},
		{
			name:    "safety",
			profile: ProfileSafety,
			wantErr: false,
			mustContain: []string{
				"Profile: safety",
				"queue.mode",
				"disk",
				"stats.level",
				"full",
				"disk required",
			},
		},
		{
			name:    "observable",
			profile: ProfileObservable,
			wantErr: false,
			mustContain: []string{
				"Profile: observable",
				"queue.mode",
				"hybrid",
				"stats.level",
				"full",
				"disk required",
			},
		},
		{
			name:    "resilient",
			profile: ProfileResilient,
			wantErr: false,
			mustContain: []string{
				"Profile: resilient",
				"queue.mode",
				"hybrid",
				"stats.level",
				"basic",
				"disk required",
			},
		},
		{
			name:    "performance",
			profile: ProfilePerformance,
			wantErr: false,
			mustContain: []string{
				"Profile: performance",
				"pipeline_split.enabled",
				"true",
				"disk required",
			},
		},
		{
			name:    "unknown",
			profile: ProfileName("unknown"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output, err := DumpProfile(tt.profile)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if output != "" {
					t.Errorf("expected empty output on error, got %q", output)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if output == "" {
				t.Fatal("expected non-empty output")
			}
			for _, s := range tt.mustContain {
				if !containsSubstring(output, s) {
					t.Errorf("output missing substring %q\noutput:\n%s", s, output)
				}
			}
		})
	}
}

func TestDumpProfile_ContainsPrerequisites(t *testing.T) {
	output, err := DumpProfile(ProfilePerformance)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mustContain := []string{
		"Prerequisites:",
		"REQUIRED",
		"RECOMMENDED",
		"Persistent disk",
		"At least 2 CPU cores",
	}
	for _, s := range mustContain {
		if !containsSubstring(output, s) {
			t.Errorf("performance dump missing %q\noutput:\n%s", s, output)
		}
	}
}

func TestDumpProfile_MinimalNoPrerequisites(t *testing.T) {
	output, err := DumpProfile(ProfileMinimal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if containsSubstring(output, "Prerequisites:") {
		t.Error("minimal profile should not have prerequisites section")
	}
}

func TestPrerequisites_Minimal(t *testing.T) {
	p, err := GetProfile(ProfileMinimal)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prereqs := p.Prerequisites()
	if len(prereqs) != 0 {
		t.Errorf("minimal should have 0 prerequisites, got %d", len(prereqs))
	}
}

func TestPrerequisites_Balanced(t *testing.T) {
	p, err := GetProfile(ProfileBalanced)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prereqs := p.Prerequisites()
	if len(prereqs) != 1 {
		t.Fatalf("balanced should have 1 prerequisite, got %d", len(prereqs))
	}
	if prereqs[0].Severity != "recommended" {
		t.Errorf("balanced prerequisite severity = %q, want %q", prereqs[0].Severity, "recommended")
	}
	if prereqs[0].Type != "memory" {
		t.Errorf("balanced prerequisite type = %q, want %q", prereqs[0].Type, "memory")
	}
}

func TestPrerequisites_Performance(t *testing.T) {
	p, err := GetProfile(ProfilePerformance)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prereqs := p.Prerequisites()
	if len(prereqs) != 4 {
		t.Fatalf("performance should have 4 prerequisites, got %d", len(prereqs))
	}

	var requiredCount, recommendedCount int
	for _, pr := range prereqs {
		switch pr.Severity {
		case "required":
			requiredCount++
		case "recommended":
			recommendedCount++
		default:
			t.Errorf("unexpected severity %q", pr.Severity)
		}
	}
	if requiredCount != 2 {
		t.Errorf("performance required prerequisites = %d, want 2", requiredCount)
	}
	if recommendedCount != 2 {
		t.Errorf("performance recommended prerequisites = %d, want 2", recommendedCount)
	}
}

func TestPrerequisites_PerformanceDiskRequired(t *testing.T) {
	p, err := GetProfile(ProfilePerformance)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var hasDiskRequired bool
	for _, pr := range p.Prerequisites() {
		if pr.Type == "disk" && pr.Severity == "required" {
			hasDiskRequired = true
			break
		}
	}
	if !hasDiskRequired {
		t.Error("performance profile should have a required disk prerequisite")
	}
}

func TestPrerequisites_PerformanceCPURequired(t *testing.T) {
	p, err := GetProfile(ProfilePerformance)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var hasCPURequired bool
	for _, pr := range p.Prerequisites() {
		if pr.Type == "cpu" && pr.Severity == "required" {
			hasCPURequired = true
			break
		}
	}
	if !hasCPURequired {
		t.Error("performance profile should have a required cpu prerequisite")
	}
}

func TestPrerequisites_Safety(t *testing.T) {
	p, err := GetProfile(ProfileSafety)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prereqs := p.Prerequisites()
	if len(prereqs) != 2 {
		t.Fatalf("safety should have 2 prerequisites, got %d", len(prereqs))
	}

	var hasDiskRequired bool
	for _, pr := range prereqs {
		if pr.Type == "disk" && pr.Severity == "required" {
			hasDiskRequired = true
		}
	}
	if !hasDiskRequired {
		t.Error("safety profile should have a required disk prerequisite")
	}
}

func TestPrerequisites_Observable(t *testing.T) {
	p, err := GetProfile(ProfileObservable)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prereqs := p.Prerequisites()
	if len(prereqs) != 2 {
		t.Fatalf("observable should have 2 prerequisites, got %d", len(prereqs))
	}

	var hasDiskRequired bool
	for _, pr := range prereqs {
		if pr.Type == "disk" && pr.Severity == "required" {
			hasDiskRequired = true
		}
	}
	if !hasDiskRequired {
		t.Error("observable profile should have a required disk prerequisite")
	}
}

func TestPrerequisites_Resilient(t *testing.T) {
	p, err := GetProfile(ProfileResilient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	prereqs := p.Prerequisites()
	if len(prereqs) != 2 {
		t.Fatalf("resilient should have 2 prerequisites, got %d", len(prereqs))
	}

	var hasDiskRequired bool
	for _, pr := range prereqs {
		if pr.Type == "disk" && pr.Severity == "required" {
			hasDiskRequired = true
		}
	}
	if !hasDiskRequired {
		t.Error("resilient profile should have a required disk prerequisite")
	}
}

func TestProfileConfig_ResourceTargets(t *testing.T) {
	tests := []struct {
		name         string
		profile      ProfileName
		diskRequired bool
	}{
		{"minimal no disk", ProfileMinimal, false},
		{"balanced no disk", ProfileBalanced, false},
		{"safety disk required", ProfileSafety, true},
		{"observable disk required", ProfileObservable, true},
		{"resilient disk required", ProfileResilient, true},
		{"performance disk required", ProfilePerformance, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := GetProfile(tt.profile)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.DiskRequired != tt.diskRequired {
				t.Errorf("DiskRequired = %v, want %v", p.DiskRequired, tt.diskRequired)
			}
			if p.TargetCPU == "" {
				t.Error("TargetCPU should not be empty")
			}
			if p.TargetMemory == "" {
				t.Error("TargetMemory should not be empty")
			}
			if p.MaxThroughput == "" {
				t.Error("MaxThroughput should not be empty")
			}
		})
	}
}

func TestApplyProfile_MemoryFields(t *testing.T) {
	cfg := DefaultConfig()
	err := ApplyProfile(cfg, ProfileMinimal, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MemoryLimitRatio != 0.80 {
		t.Errorf("MemoryLimitRatio = %f, want 0.80", cfg.MemoryLimitRatio)
	}
	if cfg.BufferMemoryPercent != 0.15 {
		t.Errorf("BufferMemoryPercent = %f, want 0.15", cfg.BufferMemoryPercent)
	}
	if cfg.QueueMemoryPercent != 0.0 {
		t.Errorf("QueueMemoryPercent = %f, want 0.0", cfg.QueueMemoryPercent)
	}
}

func TestApplyProfile_ResilienceFields(t *testing.T) {
	cfg := DefaultConfig()
	err := ApplyProfile(cfg, ProfileBalanced, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.QueueBackoffMultiplier != 2.0 {
		t.Errorf("QueueBackoffMultiplier = %f, want 2.0", cfg.QueueBackoffMultiplier)
	}
	if cfg.QueueCircuitBreakerThreshold != 5 {
		t.Errorf("QueueCircuitBreakerThreshold = %d, want 5", cfg.QueueCircuitBreakerThreshold)
	}
	if cfg.QueueCircuitBreakerResetTimeout != 30*time.Second {
		t.Errorf("QueueCircuitBreakerResetTimeout = %v, want 30s", cfg.QueueCircuitBreakerResetTimeout)
	}
	if cfg.QueueBatchDrainSize != 10 {
		t.Errorf("QueueBatchDrainSize = %d, want 10", cfg.QueueBatchDrainSize)
	}
	if cfg.QueueBurstDrainSize != 100 {
		t.Errorf("QueueBurstDrainSize = %d, want 100", cfg.QueueBurstDrainSize)
	}
}

func TestApplyProfile_GovernanceFields(t *testing.T) {
	cfg := DefaultConfig()
	err := ApplyProfile(cfg, ProfilePerformance, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CardinalityExpectedItems != 500000 {
		t.Errorf("CardinalityExpectedItems = %d, want 500000", cfg.CardinalityExpectedItems)
	}
	if cfg.RuleCacheMaxSize != 50000 {
		t.Errorf("RuleCacheMaxSize = %d, want 50000", cfg.RuleCacheMaxSize)
	}
	if cfg.ReceiverMaxRequestBodySize != 67108864 {
		t.Errorf("ReceiverMaxRequestBodySize = %d, want 67108864", cfg.ReceiverMaxRequestBodySize)
	}
	if cfg.LimitsStatsThreshold != 0 {
		t.Errorf("LimitsStatsThreshold = %d, want 0", cfg.LimitsStatsThreshold)
	}
}

func TestApplyProfile_BloomPersistence(t *testing.T) {
	tests := []struct {
		name    string
		profile ProfileName
		enabled bool
	}{
		{"minimal bloom off", ProfileMinimal, false},
		{"balanced bloom off", ProfileBalanced, false},
		{"safety bloom on", ProfileSafety, true},
		{"observable bloom on", ProfileObservable, true},
		{"resilient bloom on", ProfileResilient, true},
		{"performance bloom on", ProfilePerformance, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			err := ApplyProfile(cfg, tt.profile, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.BloomPersistenceEnabled != tt.enabled {
				t.Errorf("BloomPersistenceEnabled = %v, want %v", cfg.BloomPersistenceEnabled, tt.enabled)
			}
		})
	}
}

func TestApplyProfile_FastQueueFields(t *testing.T) {
	cfg := DefaultConfig()
	err := ApplyProfile(cfg, ProfilePerformance, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.QueueInmemoryBlocks != 4096 {
		t.Errorf("QueueInmemoryBlocks = %d, want 4096", cfg.QueueInmemoryBlocks)
	}
	if cfg.QueueMetaSyncInterval != 2*time.Second {
		t.Errorf("QueueMetaSyncInterval = %v, want 2s", cfg.QueueMetaSyncInterval)
	}
}

// --- Phase 8.5: Profile parameter validation tests ---

func TestAllProfiles_HaveConsistentStatsAndCompression(t *testing.T) {
	profiles := ValidProfileNames()
	for _, name := range profiles {
		t.Run(string(name), func(t *testing.T) {
			p, err := GetProfile(name)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Every profile must set stats level
			if p.StatsLevel == nil {
				t.Error("StatsLevel must be set")
			}

			// Every profile must set export compression
			if p.ExporterCompression == nil {
				t.Error("ExporterCompression must be set")
			}
		})
	}
}

func TestAllProfiles_ExportCompression_Policy(t *testing.T) {
	// zstd profiles: safety, observable, performance (best compression ratio)
	zstdProfiles := []ProfileName{ProfileSafety, ProfileObservable, ProfilePerformance}
	for _, name := range zstdProfiles {
		t.Run(string(name)+"_zstd", func(t *testing.T) {
			p, err := GetProfile(name)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if *p.ExporterCompression != "zstd" {
				t.Errorf("%s: ExporterCompression = %q, want %q", name, *p.ExporterCompression, "zstd")
			}
		})
	}

	// snappy profiles: balanced, resilient (high throughput, lower ratio acceptable)
	snappyProfiles := []ProfileName{ProfileBalanced, ProfileResilient}
	for _, name := range snappyProfiles {
		t.Run(string(name)+"_snappy", func(t *testing.T) {
			p, err := GetProfile(name)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if *p.ExporterCompression != "snappy" {
				t.Errorf("%s: ExporterCompression = %q, want %q", name, *p.ExporterCompression, "snappy")
			}
		})
	}

	// none: minimal (zero overhead)
	t.Run("minimal_none", func(t *testing.T) {
		p, err := GetProfile(ProfileMinimal)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if *p.ExporterCompression != "none" {
			t.Errorf("minimal: ExporterCompression = %q, want %q", *p.ExporterCompression, "none")
		}
	})
}

func TestAllProfiles_QueueCompression_Policy(t *testing.T) {
	// All disk/hybrid profiles must use snappy for queue (fast, allocation-free)
	diskProfiles := []ProfileName{ProfileSafety, ProfileObservable, ProfileResilient, ProfilePerformance}
	for _, name := range diskProfiles {
		t.Run(string(name), func(t *testing.T) {
			p, err := GetProfile(name)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if *p.QueueCompression != "snappy" {
				t.Errorf("%s: QueueCompression = %q, want %q", name, *p.QueueCompression, "snappy")
			}
		})
	}
}

func TestObservableProfile_InmemoryBlocks_2048(t *testing.T) {
	p, err := GetProfile(ProfileObservable)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *p.QueueInmemoryBlocks != 2048 {
		t.Errorf("observable QueueInmemoryBlocks = %d, want 2048", *p.QueueInmemoryBlocks)
	}
}

func TestObservableProfile_CPUTarget_Honest(t *testing.T) {
	p, err := GetProfile(ProfileObservable)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Observable uses full stats + zstd + hybrid queue — must claim >= 1.0 cores minimum
	if p.TargetCPU == "0.75-1.25 cores" {
		t.Error("observable CPU target must not claim 0.75 cores minimum — full stats + zstd requires >= 1.0")
	}
}

func TestSafetyProfile_CPUTarget_Honest(t *testing.T) {
	p, err := GetProfile(ProfileSafety)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Safety uses full stats + disk queue + zstd — must claim >= 1.25 cores minimum
	if p.TargetCPU == "1-1.5 cores" {
		t.Error("safety CPU target must not claim 1.0 cores minimum — full stats + disk queue + zstd requires >= 1.25")
	}
}

func TestAllProfiles_MetaSyncIntervals_Appropriate(t *testing.T) {
	tests := []struct {
		name        string
		profile     ProfileName
		minInterval time.Duration
		maxInterval time.Duration
	}{
		{"minimal", ProfileMinimal, 1 * time.Second, 10 * time.Second},
		{"balanced", ProfileBalanced, 1 * time.Second, 5 * time.Second},
		{"safety", ProfileSafety, 1 * time.Second, 1 * time.Second},           // maximum durability
		{"observable", ProfileObservable, 3 * time.Second, 10 * time.Second},  // observability > durability
		{"resilient", ProfileResilient, 2 * time.Second, 5 * time.Second},     // balance durability + IOPS
		{"performance", ProfilePerformance, 1 * time.Second, 5 * time.Second}, // throughput > durability
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := GetProfile(tt.profile)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.QueueMetaSyncInterval == nil {
				t.Fatal("QueueMetaSyncInterval must be set")
			}
			interval := *p.QueueMetaSyncInterval
			if interval < tt.minInterval || interval > tt.maxInterval {
				t.Errorf("MetaSyncInterval = %v, want [%v, %v]", interval, tt.minInterval, tt.maxInterval)
			}
		})
	}
}

func TestObservableProfile_MaxThroughput_100k(t *testing.T) {
	p, err := GetProfile(ProfileObservable)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.MaxThroughput != "~100k dps" {
		t.Errorf("observable MaxThroughput = %q, want %q", p.MaxThroughput, "~100k dps")
	}
}

// containsSubstring is a helper to check if s contains substr.
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && searchSubstring(s, substr)))
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestAllProfiles_HaveLoadSheddingThreshold(t *testing.T) {
	for _, name := range ValidProfileNames() {
		t.Run(string(name), func(t *testing.T) {
			p, err := GetProfile(name)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.LoadSheddingThreshold == nil {
				t.Fatal("LoadSheddingThreshold must be set")
			}
			threshold := *p.LoadSheddingThreshold
			if threshold < 0.5 || threshold > 1.0 {
				t.Errorf("LoadSheddingThreshold = %f, want [0.5, 1.0]", threshold)
			}
		})
	}
}

func TestAllProfiles_LoadSheddingThresholdOrdering(t *testing.T) {
	// Higher capacity profiles should tolerate more pressure before shedding.
	// minimal <= balanced <= observable <= safety/resilient <= performance
	expectedOrder := []struct {
		name      ProfileName
		threshold float64
	}{
		{ProfileMinimal, 0.80},
		{ProfileBalanced, 0.85},
		{ProfileObservable, 0.85},
		{ProfileSafety, 0.90},
		{ProfileResilient, 0.90},
		{ProfilePerformance, 0.95},
	}

	for _, expected := range expectedOrder {
		t.Run(string(expected.name), func(t *testing.T) {
			p, err := GetProfile(expected.name)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if *p.LoadSheddingThreshold != expected.threshold {
				t.Errorf("%s: LoadSheddingThreshold = %f, want %f",
					expected.name, *p.LoadSheddingThreshold, expected.threshold)
			}
		})
	}
}

func TestApplyProfile_LoadSheddingThreshold(t *testing.T) {
	cfg := &Config{}
	err := ApplyProfile(cfg, ProfileObservable, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LoadSheddingThreshold != 0.85 {
		t.Errorf("LoadSheddingThreshold = %f, want 0.85", cfg.LoadSheddingThreshold)
	}
}

func TestApplyProfile_LoadSheddingThreshold_ExplicitOverride(t *testing.T) {
	cfg := &Config{LoadSheddingThreshold: 0.99}
	explicit := map[string]bool{"load-shedding-threshold": true}
	err := ApplyProfile(cfg, ProfileObservable, explicit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Explicit override should be preserved
	if cfg.LoadSheddingThreshold != 0.99 {
		t.Errorf("LoadSheddingThreshold = %f, want 0.99 (explicit override)", cfg.LoadSheddingThreshold)
	}
}

func TestAllProfiles_HaveGOGC(t *testing.T) {
	for _, name := range ValidProfileNames() {
		t.Run(string(name), func(t *testing.T) {
			p, err := GetProfile(name)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.GOGC == nil {
				t.Fatal("GOGC must be set")
			}
			gogc := *p.GOGC
			if gogc < 10 || gogc > 500 {
				t.Errorf("GOGC=%d, want range [10, 500]", gogc)
			}
		})
	}
}

func TestAllProfiles_GOGCValues(t *testing.T) {
	expected := []struct {
		name ProfileName
		gogc int
	}{
		{ProfileMinimal, 100},
		{ProfileBalanced, 200},
		{ProfileSafety, 200},
		{ProfileObservable, 200},
		{ProfileResilient, 200},
		{ProfilePerformance, 400},
	}
	for _, tc := range expected {
		t.Run(string(tc.name), func(t *testing.T) {
			p, err := GetProfile(tc.name)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if p.GOGC == nil {
				t.Fatalf("GOGC is nil, want %d", tc.gogc)
			}
			if *p.GOGC != tc.gogc {
				t.Errorf("GOGC=%d, want %d", *p.GOGC, tc.gogc)
			}
		})
	}
}

func TestApplyProfile_GOGC(t *testing.T) {
	cfg := &Config{}
	err := ApplyProfile(cfg, ProfileObservable, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.GOGC != 200 {
		t.Errorf("GOGC = %d, want 200 (observable profile)", cfg.GOGC)
	}
}

func TestApplyProfile_GOGC_ExplicitOverride(t *testing.T) {
	cfg := &Config{GOGC: 42}
	explicit := map[string]bool{"gogc": true}
	err := ApplyProfile(cfg, ProfilePerformance, explicit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Explicit override should be preserved
	if cfg.GOGC != 42 {
		t.Errorf("GOGC = %d, want 42 (explicit override)", cfg.GOGC)
	}
}
