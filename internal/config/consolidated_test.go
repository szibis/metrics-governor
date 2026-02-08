package config

import (
	"testing"
	"time"
)

func TestExpandParallelism(t *testing.T) {
	tests := []struct {
		name           string
		parallelism    int
		explicit       map[string]bool
		wantWorkers    int
		wantConcur     int
		wantPrep       int
		wantSender     int
		wantMaxSends   int
		wantGlobalSend int
		wantDerived    int // expected number of derived entries
	}{
		{
			name:           "parallelism=4 expands all fields",
			parallelism:    4,
			explicit:       map[string]bool{"parallelism": true},
			wantWorkers:    8,  // 4*2
			wantConcur:     16, // 4*4
			wantPrep:       4,  // 4*1
			wantSender:     8,  // 4*2
			wantMaxSends:   2,  // max(2, 4/2)
			wantGlobalSend: 32, // 4*8
			wantDerived:    6,
		},
		{
			name:           "parallelism=1 applies min for max_concurrent_sends",
			parallelism:    1,
			explicit:       map[string]bool{"parallelism": true},
			wantWorkers:    2, // 1*2
			wantConcur:     4, // 1*4
			wantPrep:       1, // 1*1
			wantSender:     2, // 1*2
			wantMaxSends:   2, // max(2, 1/2=0) => 2
			wantGlobalSend: 8, // 1*8
			wantDerived:    6,
		},
		{
			name:           "parallelism=10 high value",
			parallelism:    10,
			explicit:       map[string]bool{"parallelism": true},
			wantWorkers:    20, // 10*2
			wantConcur:     40, // 10*4
			wantPrep:       10, // 10*1
			wantSender:     20, // 10*2
			wantMaxSends:   5,  // 10/2
			wantGlobalSend: 80, // 10*8
			wantDerived:    6,
		},
		{
			name:        "parallelism=0 does nothing",
			parallelism: 0,
			explicit:    map[string]bool{"parallelism": true},
			wantDerived: 0,
		},
		{
			name:        "parallelism not explicit does nothing",
			parallelism: 4,
			explicit:    map[string]bool{},
			wantDerived: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Parallelism = tt.parallelism

			derived := ExpandConsolidatedParams(cfg, tt.explicit)

			// Count only parallelism-related derived values
			parallelismDerived := 0
			for _, d := range derived {
				switch d.Field {
				case "queue-workers", "export-concurrency",
					"queue.pipeline_split.preparer_count", "queue.pipeline_split.sender_count",
					"queue.max_concurrent_sends", "queue.global_send_limit":
					parallelismDerived++
				}
			}

			if parallelismDerived != tt.wantDerived {
				t.Errorf("derived count = %d, want %d", parallelismDerived, tt.wantDerived)
			}

			if tt.wantDerived == 0 {
				return
			}

			if cfg.QueueWorkers != tt.wantWorkers {
				t.Errorf("QueueWorkers = %d, want %d", cfg.QueueWorkers, tt.wantWorkers)
			}
			if cfg.ExportConcurrency != tt.wantConcur {
				t.Errorf("ExportConcurrency = %d, want %d", cfg.ExportConcurrency, tt.wantConcur)
			}
			if cfg.QueuePreparerCount != tt.wantPrep {
				t.Errorf("QueuePreparerCount = %d, want %d", cfg.QueuePreparerCount, tt.wantPrep)
			}
			if cfg.QueueSenderCount != tt.wantSender {
				t.Errorf("QueueSenderCount = %d, want %d", cfg.QueueSenderCount, tt.wantSender)
			}
			if cfg.QueueMaxConcurrentSends != tt.wantMaxSends {
				t.Errorf("QueueMaxConcurrentSends = %d, want %d", cfg.QueueMaxConcurrentSends, tt.wantMaxSends)
			}
			if cfg.QueueGlobalSendLimit != tt.wantGlobalSend {
				t.Errorf("QueueGlobalSendLimit = %d, want %d", cfg.QueueGlobalSendLimit, tt.wantGlobalSend)
			}
		})
	}
}

func TestExpandParallelism_ExplicitOverridePreserved(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Parallelism = 4
	cfg.QueueWorkers = 99

	explicit := map[string]bool{
		"parallelism":   true,
		"queue-workers": true, // user explicitly set queue-workers
	}

	derived := ExpandConsolidatedParams(cfg, explicit)

	if cfg.QueueWorkers != 99 {
		t.Errorf("QueueWorkers should not be overridden when explicit, got %d, want 99", cfg.QueueWorkers)
	}

	// Should have 5 derived entries (all except queue-workers)
	parallelismDerived := 0
	for _, d := range derived {
		if d.Field == "queue-workers" {
			t.Error("queue-workers should not appear in derived values when explicitly set")
		}
		switch d.Field {
		case "export-concurrency",
			"queue.pipeline_split.preparer_count", "queue.pipeline_split.sender_count",
			"queue.max_concurrent_sends", "queue.global_send_limit":
			parallelismDerived++
		}
	}
	if parallelismDerived != 5 {
		t.Errorf("expected 5 parallelism derived entries, got %d", parallelismDerived)
	}
}

func TestExpandMemoryBudget(t *testing.T) {
	tests := []struct {
		name        string
		budgetPct   float64
		explicit    map[string]bool
		wantBuffer  float64
		wantQueue   float64
		wantDerived int
	}{
		{
			name:        "budget=0.20 splits evenly",
			budgetPct:   0.20,
			explicit:    map[string]bool{"memory-budget-percent": true},
			wantBuffer:  0.10,
			wantQueue:   0.10,
			wantDerived: 2,
		},
		{
			name:        "budget=0.40 splits evenly",
			budgetPct:   0.40,
			explicit:    map[string]bool{"memory-budget-percent": true},
			wantBuffer:  0.20,
			wantQueue:   0.20,
			wantDerived: 2,
		},
		{
			name:        "budget=0 does nothing",
			budgetPct:   0,
			explicit:    map[string]bool{"memory-budget-percent": true},
			wantDerived: 0,
		},
		{
			name:        "budget not explicit does nothing",
			budgetPct:   0.20,
			explicit:    map[string]bool{},
			wantDerived: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.MemoryBudgetPct = tt.budgetPct

			derived := ExpandConsolidatedParams(cfg, tt.explicit)

			memDerived := 0
			for _, d := range derived {
				if d.Field == "buffer-memory-percent" || d.Field == "queue-memory-percent" {
					memDerived++
				}
			}

			if memDerived != tt.wantDerived {
				t.Errorf("derived count = %d, want %d", memDerived, tt.wantDerived)
			}

			if tt.wantDerived == 0 {
				return
			}

			if cfg.BufferMemoryPercent != tt.wantBuffer {
				t.Errorf("BufferMemoryPercent = %f, want %f", cfg.BufferMemoryPercent, tt.wantBuffer)
			}
			if cfg.QueueMemoryPercent != tt.wantQueue {
				t.Errorf("QueueMemoryPercent = %f, want %f", cfg.QueueMemoryPercent, tt.wantQueue)
			}
		})
	}
}

func TestExpandMemoryBudget_ExplicitOverridePreserved(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MemoryBudgetPct = 0.30
	cfg.BufferMemoryPercent = 0.05

	explicit := map[string]bool{
		"memory-budget-percent": true,
		"buffer-memory-percent": true, // user explicitly set buffer-memory-percent
	}

	derived := ExpandConsolidatedParams(cfg, explicit)

	if cfg.BufferMemoryPercent != 0.05 {
		t.Errorf("BufferMemoryPercent should not be overridden, got %f, want 0.05", cfg.BufferMemoryPercent)
	}
	if cfg.QueueMemoryPercent != 0.15 {
		t.Errorf("QueueMemoryPercent = %f, want 0.15", cfg.QueueMemoryPercent)
	}

	for _, d := range derived {
		if d.Field == "buffer-memory-percent" {
			t.Error("buffer-memory-percent should not appear in derived when explicitly set")
		}
	}
}

func TestExpandExportTimeout(t *testing.T) {
	tests := []struct {
		name             string
		base             time.Duration
		explicit         map[string]bool
		wantExporter     time.Duration
		wantDirectExport time.Duration
		wantRetry        time.Duration
		wantDrainEntry   time.Duration
		wantDrain        time.Duration
		wantClose        time.Duration
		wantFlush        time.Duration
		wantDerived      int
	}{
		{
			name:             "base=30s cascades all timeouts",
			base:             30 * time.Second,
			explicit:         map[string]bool{"export-timeout": true},
			wantExporter:     30 * time.Second,
			wantDirectExport: 5 * time.Second,  // 30s/6
			wantRetry:        10 * time.Second, // 30s/3
			wantDrainEntry:   5 * time.Second,  // 30s/6
			wantDrain:        30 * time.Second, // 30s*1
			wantClose:        60 * time.Second, // 30s*2
			wantFlush:        30 * time.Second, // 30s*1
			wantDerived:      7,
		},
		{
			name:             "base=60s cascades all timeouts",
			base:             60 * time.Second,
			explicit:         map[string]bool{"export-timeout": true},
			wantExporter:     60 * time.Second,
			wantDirectExport: 10 * time.Second,  // 60s/6
			wantRetry:        20 * time.Second,  // 60s/3
			wantDrainEntry:   10 * time.Second,  // 60s/6
			wantDrain:        60 * time.Second,  // 60s*1
			wantClose:        120 * time.Second, // 60s*2
			wantFlush:        60 * time.Second,  // 60s*1
			wantDerived:      7,
		},
		{
			name:        "base=0 does nothing",
			base:        0,
			explicit:    map[string]bool{"export-timeout": true},
			wantDerived: 0,
		},
		{
			name:        "base not explicit does nothing",
			base:        30 * time.Second,
			explicit:    map[string]bool{},
			wantDerived: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.ExportTimeoutBase = tt.base

			derived := ExpandConsolidatedParams(cfg, tt.explicit)

			timeoutFields := map[string]bool{
				"exporter-timeout":            true,
				"queue-direct-export-timeout": true,
				"queue-retry-timeout":         true,
				"queue-drain-entry-timeout":   true,
				"queue-drain-timeout":         true,
				"queue-close-timeout":         true,
				"flush-timeout":               true,
			}

			timeoutDerived := 0
			for _, d := range derived {
				if timeoutFields[d.Field] {
					timeoutDerived++
				}
			}

			if timeoutDerived != tt.wantDerived {
				t.Errorf("derived count = %d, want %d", timeoutDerived, tt.wantDerived)
			}

			if tt.wantDerived == 0 {
				return
			}

			if cfg.ExporterTimeout != tt.wantExporter {
				t.Errorf("ExporterTimeout = %v, want %v", cfg.ExporterTimeout, tt.wantExporter)
			}
			if cfg.QueueDirectExportTimeout != tt.wantDirectExport {
				t.Errorf("QueueDirectExportTimeout = %v, want %v", cfg.QueueDirectExportTimeout, tt.wantDirectExport)
			}
			if cfg.QueueRetryTimeout != tt.wantRetry {
				t.Errorf("QueueRetryTimeout = %v, want %v", cfg.QueueRetryTimeout, tt.wantRetry)
			}
			if cfg.QueueDrainEntryTimeout != tt.wantDrainEntry {
				t.Errorf("QueueDrainEntryTimeout = %v, want %v", cfg.QueueDrainEntryTimeout, tt.wantDrainEntry)
			}
			if cfg.QueueDrainTimeout != tt.wantDrain {
				t.Errorf("QueueDrainTimeout = %v, want %v", cfg.QueueDrainTimeout, tt.wantDrain)
			}
			if cfg.QueueCloseTimeout != tt.wantClose {
				t.Errorf("QueueCloseTimeout = %v, want %v", cfg.QueueCloseTimeout, tt.wantClose)
			}
			if cfg.FlushTimeout != tt.wantFlush {
				t.Errorf("FlushTimeout = %v, want %v", cfg.FlushTimeout, tt.wantFlush)
			}
		})
	}
}

func TestExpandExportTimeout_ExplicitOverridePreserved(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ExportTimeoutBase = 30 * time.Second
	cfg.ExporterTimeout = 99 * time.Second

	explicit := map[string]bool{
		"export-timeout":   true,
		"exporter-timeout": true, // user explicitly set this
	}

	ExpandConsolidatedParams(cfg, explicit)

	if cfg.ExporterTimeout != 99*time.Second {
		t.Errorf("ExporterTimeout should not be overridden, got %v, want 99s", cfg.ExporterTimeout)
	}
	// Other timeouts should still be derived
	if cfg.QueueRetryTimeout != 10*time.Second {
		t.Errorf("QueueRetryTimeout = %v, want 10s", cfg.QueueRetryTimeout)
	}
}

func TestExpandResilienceLevel(t *testing.T) {
	tests := []struct {
		name               string
		level              string
		explicit           map[string]bool
		wantBackoff        float64
		wantCBEnabled      bool
		wantCBThreshold    int
		wantCBResetTimeout time.Duration
		wantBatchDrain     int
		wantBurstDrain     int
		wantDerived        int
	}{
		{
			name:               "low resilience",
			level:              "low",
			explicit:           map[string]bool{"resilience-level": true},
			wantBackoff:        1.5,
			wantCBEnabled:      false,
			wantCBThreshold:    10,
			wantCBResetTimeout: 60 * time.Second,
			wantBatchDrain:     5,
			wantBurstDrain:     50,
			wantDerived:        6,
		},
		{
			name:               "medium resilience",
			level:              "medium",
			explicit:           map[string]bool{"resilience-level": true},
			wantBackoff:        2.0,
			wantCBEnabled:      true,
			wantCBThreshold:    5,
			wantCBResetTimeout: 30 * time.Second,
			wantBatchDrain:     10,
			wantBurstDrain:     100,
			wantDerived:        6,
		},
		{
			name:               "high resilience",
			level:              "high",
			explicit:           map[string]bool{"resilience-level": true},
			wantBackoff:        3.0,
			wantCBEnabled:      true,
			wantCBThreshold:    3,
			wantCBResetTimeout: 15 * time.Second,
			wantBatchDrain:     25,
			wantBurstDrain:     250,
			wantDerived:        6,
		},
		{
			name:        "unknown level does nothing",
			level:       "extreme",
			explicit:    map[string]bool{"resilience-level": true},
			wantDerived: 0,
		},
		{
			name:        "empty level does nothing",
			level:       "",
			explicit:    map[string]bool{"resilience-level": true},
			wantDerived: 0,
		},
		{
			name:        "level not explicit does nothing",
			level:       "high",
			explicit:    map[string]bool{},
			wantDerived: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.ResilienceLevel = tt.level

			derived := ExpandConsolidatedParams(cfg, tt.explicit)

			resFields := map[string]bool{
				"queue-backoff-multiplier":            true,
				"queue-circuit-breaker-enabled":       true,
				"queue-circuit-breaker-threshold":     true,
				"queue-circuit-breaker-reset-timeout": true,
				"queue-batch-drain-size":              true,
				"queue-burst-drain-size":              true,
			}

			resDerived := 0
			for _, d := range derived {
				if resFields[d.Field] {
					resDerived++
				}
			}

			if resDerived != tt.wantDerived {
				t.Errorf("derived count = %d, want %d", resDerived, tt.wantDerived)
			}

			if tt.wantDerived == 0 {
				return
			}

			if cfg.QueueBackoffMultiplier != tt.wantBackoff {
				t.Errorf("QueueBackoffMultiplier = %f, want %f", cfg.QueueBackoffMultiplier, tt.wantBackoff)
			}
			if cfg.QueueCircuitBreakerEnabled != tt.wantCBEnabled {
				t.Errorf("QueueCircuitBreakerEnabled = %v, want %v", cfg.QueueCircuitBreakerEnabled, tt.wantCBEnabled)
			}
			if cfg.QueueCircuitBreakerThreshold != tt.wantCBThreshold {
				t.Errorf("QueueCircuitBreakerThreshold = %d, want %d", cfg.QueueCircuitBreakerThreshold, tt.wantCBThreshold)
			}
			if cfg.QueueCircuitBreakerResetTimeout != tt.wantCBResetTimeout {
				t.Errorf("QueueCircuitBreakerResetTimeout = %v, want %v", cfg.QueueCircuitBreakerResetTimeout, tt.wantCBResetTimeout)
			}
			if cfg.QueueBatchDrainSize != tt.wantBatchDrain {
				t.Errorf("QueueBatchDrainSize = %d, want %d", cfg.QueueBatchDrainSize, tt.wantBatchDrain)
			}
			if cfg.QueueBurstDrainSize != tt.wantBurstDrain {
				t.Errorf("QueueBurstDrainSize = %d, want %d", cfg.QueueBurstDrainSize, tt.wantBurstDrain)
			}
		})
	}
}

func TestExpandResilienceLevel_ExplicitOverridePreserved(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ResilienceLevel = "high"
	cfg.QueueBackoffMultiplier = 5.0

	explicit := map[string]bool{
		"resilience-level":         true,
		"queue-backoff-multiplier": true, // user explicitly set this
	}

	derived := ExpandConsolidatedParams(cfg, explicit)

	if cfg.QueueBackoffMultiplier != 5.0 {
		t.Errorf("QueueBackoffMultiplier should not be overridden, got %f, want 5.0", cfg.QueueBackoffMultiplier)
	}

	for _, d := range derived {
		if d.Field == "queue-backoff-multiplier" {
			t.Error("queue-backoff-multiplier should not appear in derived when explicitly set")
		}
	}

	// Other resilience fields should still be derived
	if cfg.QueueCircuitBreakerThreshold != 3 {
		t.Errorf("QueueCircuitBreakerThreshold = %d, want 3 (high preset)", cfg.QueueCircuitBreakerThreshold)
	}
}

func TestIsValidResilienceLevel(t *testing.T) {
	tests := []struct {
		level string
		want  bool
	}{
		{"", true},
		{"low", true},
		{"medium", true},
		{"high", true},
		{"extreme", false},
		{"LOW", false},
		{"Medium", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run("level="+tt.level, func(t *testing.T) {
			got := IsValidResilienceLevel(tt.level)
			if got != tt.want {
				t.Errorf("IsValidResilienceLevel(%q) = %v, want %v", tt.level, got, tt.want)
			}
		})
	}
}

func TestExpandConsolidatedParams_AllFour(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Parallelism = 4
	cfg.MemoryBudgetPct = 0.30
	cfg.ExportTimeoutBase = 30 * time.Second
	cfg.ResilienceLevel = "medium"

	explicit := map[string]bool{
		"parallelism":           true,
		"memory-budget-percent": true,
		"export-timeout":        true,
		"resilience-level":      true,
	}

	derived := ExpandConsolidatedParams(cfg, explicit)

	// 6 parallelism + 2 memory + 7 timeout + 6 resilience = 21
	if len(derived) != 21 {
		t.Errorf("total derived = %d, want 21", len(derived))
	}

	// Spot-check one from each group
	if cfg.QueueWorkers != 8 {
		t.Errorf("QueueWorkers = %d, want 8", cfg.QueueWorkers)
	}
	if cfg.BufferMemoryPercent != 0.15 {
		t.Errorf("BufferMemoryPercent = %f, want 0.15", cfg.BufferMemoryPercent)
	}
	if cfg.QueueCloseTimeout != 60*time.Second {
		t.Errorf("QueueCloseTimeout = %v, want 60s", cfg.QueueCloseTimeout)
	}
	if cfg.QueueCircuitBreakerThreshold != 5 {
		t.Errorf("QueueCircuitBreakerThreshold = %d, want 5 (medium)", cfg.QueueCircuitBreakerThreshold)
	}
}

func TestExpandConsolidatedParams_NoneExplicit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Parallelism = 4
	cfg.MemoryBudgetPct = 0.30
	cfg.ExportTimeoutBase = 30 * time.Second
	cfg.ResilienceLevel = "high"

	// Nothing marked as explicit -- nothing should expand
	explicit := map[string]bool{}

	derived := ExpandConsolidatedParams(cfg, explicit)

	if len(derived) != 0 {
		t.Errorf("expected 0 derived when nothing explicit, got %d", len(derived))
	}
}

func TestExpandConsolidatedParams_DerivedValueFormula(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Parallelism = 4
	explicit := map[string]bool{"parallelism": true}

	derived := ExpandConsolidatedParams(cfg, explicit)

	// Verify that derived entries carry meaningful formula strings
	for _, d := range derived {
		if d.Field == "" {
			t.Error("derived entry has empty Field")
		}
		if d.Value == "" {
			t.Error("derived entry has empty Value")
		}
		if d.Formula == "" {
			t.Error("derived entry has empty Formula")
		}
	}
}

func TestValidResilienceLevels(t *testing.T) {
	levels := ValidResilienceLevels()
	want := []string{"low", "medium", "high"}

	if len(levels) != len(want) {
		t.Fatalf("ValidResilienceLevels() returned %d levels, want %d", len(levels), len(want))
	}

	for i, l := range levels {
		if l != want[i] {
			t.Errorf("ValidResilienceLevels()[%d] = %q, want %q", i, l, want[i])
		}
	}
}
