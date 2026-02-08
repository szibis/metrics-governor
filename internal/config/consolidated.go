package config

import (
	"fmt"
	"time"
)

// ExpandConsolidatedParams expands the 4 high-level params into their constituent fields.
// Only fields NOT in explicitFields are set. Returns descriptions of expansions applied.
func ExpandConsolidatedParams(cfg *Config, explicitFields map[string]bool) []DerivedValue {
	var derived []DerivedValue

	derived = append(derived, expandParallelism(cfg, explicitFields)...)
	derived = append(derived, expandMemoryBudget(cfg, explicitFields)...)
	derived = append(derived, expandExportTimeout(cfg, explicitFields)...)
	derived = append(derived, expandResilienceLevel(cfg, explicitFields)...)

	return derived
}

// expandParallelism derives 6 worker-related fields from cfg.Parallelism.
func expandParallelism(cfg *Config, explicit map[string]bool) []DerivedValue {
	if cfg.Parallelism <= 0 {
		return nil
	}
	if !explicit["parallelism"] {
		return nil
	}

	var derived []DerivedValue
	base := cfg.Parallelism

	setDerived := func(field string, value int, formula string) {
		if !explicit[field] {
			derived = append(derived, DerivedValue{
				Field:   field,
				Value:   fmt.Sprintf("%d", value),
				Formula: fmt.Sprintf("parallelism(%d) %s", base, formula),
			})
		}
	}

	if !explicit["queue-workers"] {
		cfg.QueueWorkers = base * 2
		setDerived("queue-workers", base*2, "* 2")
	}
	if !explicit["export-concurrency"] {
		cfg.ExportConcurrency = base * 4
		setDerived("export-concurrency", base*4, "* 4")
	}
	if !explicit["queue.pipeline_split.preparer_count"] {
		cfg.QueuePreparerCount = base
		setDerived("queue.pipeline_split.preparer_count", base, "* 1")
	}
	if !explicit["queue.pipeline_split.sender_count"] {
		cfg.QueueSenderCount = base * 2
		setDerived("queue.pipeline_split.sender_count", base*2, "* 2")
	}
	if !explicit["queue.max_concurrent_sends"] {
		v := base / 2
		if v < 2 {
			v = 2
		}
		cfg.QueueMaxConcurrentSends = v
		setDerived("queue.max_concurrent_sends", v, "/ 2 (min 2)")
	}
	if !explicit["queue.global_send_limit"] {
		cfg.QueueGlobalSendLimit = base * 8
		setDerived("queue.global_send_limit", base*8, "* 8")
	}

	return derived
}

// expandMemoryBudget derives buffer and queue memory percentages.
func expandMemoryBudget(cfg *Config, explicit map[string]bool) []DerivedValue {
	if cfg.MemoryBudgetPct <= 0 {
		return nil
	}
	if !explicit["memory-budget-percent"] {
		return nil
	}

	var derived []DerivedValue
	half := cfg.MemoryBudgetPct / 2

	if !explicit["buffer-memory-percent"] {
		cfg.BufferMemoryPercent = half
		derived = append(derived, DerivedValue{
			Field:   "buffer-memory-percent",
			Value:   fmt.Sprintf("%.2f", half),
			Formula: fmt.Sprintf("memory_budget(%.2f) / 2", cfg.MemoryBudgetPct),
		})
	}
	if !explicit["queue-memory-percent"] {
		cfg.QueueMemoryPercent = half
		derived = append(derived, DerivedValue{
			Field:   "queue-memory-percent",
			Value:   fmt.Sprintf("%.2f", half),
			Formula: fmt.Sprintf("memory_budget(%.2f) / 2", cfg.MemoryBudgetPct),
		})
	}

	return derived
}

// expandExportTimeout derives the timeout cascade from ExportTimeoutBase.
func expandExportTimeout(cfg *Config, explicit map[string]bool) []DerivedValue {
	if cfg.ExportTimeoutBase <= 0 {
		return nil
	}
	if !explicit["export-timeout"] {
		return nil
	}

	var derived []DerivedValue
	base := cfg.ExportTimeoutBase

	setDur := func(field string, target *time.Duration, value time.Duration, formula string) {
		if !explicit[field] {
			*target = value
			derived = append(derived, DerivedValue{
				Field:   field,
				Value:   value.String(),
				Formula: fmt.Sprintf("export_timeout(%s) %s", base, formula),
			})
		}
	}

	setDur("exporter-timeout", &cfg.ExporterTimeout, base, "* 1")
	setDur("queue-direct-export-timeout", &cfg.QueueDirectExportTimeout, base/6, "/ 6")
	setDur("queue-retry-timeout", &cfg.QueueRetryTimeout, base/3, "/ 3")
	setDur("queue-drain-entry-timeout", &cfg.QueueDrainEntryTimeout, base/6, "/ 6")
	setDur("queue-drain-timeout", &cfg.QueueDrainTimeout, base, "* 1")
	setDur("queue-close-timeout", &cfg.QueueCloseTimeout, base*2, "* 2")
	setDur("flush-timeout", &cfg.FlushTimeout, base, "* 1")

	return derived
}

// expandResilienceLevel applies the resilience preset.
func expandResilienceLevel(cfg *Config, explicit map[string]bool) []DerivedValue {
	if cfg.ResilienceLevel == "" {
		return nil
	}
	if !explicit["resilience-level"] {
		return nil
	}

	var derived []DerivedValue

	type resiliencePreset struct {
		BackoffMultiplier          float64
		CircuitBreakerEnabled      bool
		CircuitBreakerThreshold    int
		CircuitBreakerResetTimeout time.Duration
		BatchDrainSize             int
		BurstDrainSize             int
	}

	presets := map[string]resiliencePreset{
		"low": {
			BackoffMultiplier:          1.5,
			CircuitBreakerEnabled:      false,
			CircuitBreakerThreshold:    10,
			CircuitBreakerResetTimeout: 60 * time.Second,
			BatchDrainSize:             5,
			BurstDrainSize:             50,
		},
		"medium": {
			BackoffMultiplier:          2.0,
			CircuitBreakerEnabled:      true,
			CircuitBreakerThreshold:    5,
			CircuitBreakerResetTimeout: 30 * time.Second,
			BatchDrainSize:             10,
			BurstDrainSize:             100,
		},
		"high": {
			BackoffMultiplier:          3.0,
			CircuitBreakerEnabled:      true,
			CircuitBreakerThreshold:    3,
			CircuitBreakerResetTimeout: 15 * time.Second,
			BatchDrainSize:             25,
			BurstDrainSize:             250,
		},
	}

	preset, ok := presets[cfg.ResilienceLevel]
	if !ok {
		return nil
	}

	label := fmt.Sprintf("resilience(%s)", cfg.ResilienceLevel)

	if !explicit["queue-backoff-multiplier"] {
		cfg.QueueBackoffMultiplier = preset.BackoffMultiplier
		derived = append(derived, DerivedValue{
			Field:   "queue-backoff-multiplier",
			Value:   fmt.Sprintf("%.1f", preset.BackoffMultiplier),
			Formula: label,
		})
	}
	if !explicit["queue-circuit-breaker-enabled"] {
		cfg.QueueCircuitBreakerEnabled = preset.CircuitBreakerEnabled
		derived = append(derived, DerivedValue{
			Field:   "queue-circuit-breaker-enabled",
			Value:   fmt.Sprintf("%t", preset.CircuitBreakerEnabled),
			Formula: label,
		})
	}
	if !explicit["queue-circuit-breaker-threshold"] {
		cfg.QueueCircuitBreakerThreshold = preset.CircuitBreakerThreshold
		derived = append(derived, DerivedValue{
			Field:   "queue-circuit-breaker-threshold",
			Value:   fmt.Sprintf("%d", preset.CircuitBreakerThreshold),
			Formula: label,
		})
	}
	if !explicit["queue-circuit-breaker-reset-timeout"] {
		cfg.QueueCircuitBreakerResetTimeout = preset.CircuitBreakerResetTimeout
		derived = append(derived, DerivedValue{
			Field:   "queue-circuit-breaker-reset-timeout",
			Value:   preset.CircuitBreakerResetTimeout.String(),
			Formula: label,
		})
	}
	if !explicit["queue-batch-drain-size"] {
		cfg.QueueBatchDrainSize = preset.BatchDrainSize
		derived = append(derived, DerivedValue{
			Field:   "queue-batch-drain-size",
			Value:   fmt.Sprintf("%d", preset.BatchDrainSize),
			Formula: label,
		})
	}
	if !explicit["queue-burst-drain-size"] {
		cfg.QueueBurstDrainSize = preset.BurstDrainSize
		derived = append(derived, DerivedValue{
			Field:   "queue-burst-drain-size",
			Value:   fmt.Sprintf("%d", preset.BurstDrainSize),
			Formula: label,
		})
	}

	return derived
}

// ValidResilienceLevels returns the valid resilience level names.
func ValidResilienceLevels() []string {
	return []string{"low", "medium", "high"}
}

// IsValidResilienceLevel checks if the given level is valid (or empty).
func IsValidResilienceLevel(level string) bool {
	if level == "" {
		return true
	}
	for _, l := range ValidResilienceLevels() {
		if level == l {
			return true
		}
	}
	return false
}
