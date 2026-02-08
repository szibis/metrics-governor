package config

import (
	"fmt"
	"math"
	"runtime"
)

// ResourceInfo holds detected runtime resources for auto-derivation.
type ResourceInfo struct {
	CPUCount    int
	MemoryBytes int64
	Source      string // "cgroup", "system", "none"
}

// DetectResources returns the current system's resource information.
func DetectResources() ResourceInfo {
	return ResourceInfo{
		CPUCount: runtime.NumCPU(),
		Source:   "system",
	}
}

// DerivedValue records a single auto-derived value for logging.
type DerivedValue struct {
	Field   string
	Value   string
	Formula string
}

// AutoDerive fills in configuration values based on detected resources.
// It only sets fields that are NOT in explicitFields (user overrides always win).
// Returns a list of derivations applied for logging.
func AutoDerive(cfg *Config, resources ResourceInfo, explicitFields map[string]bool) []DerivedValue {
	var derived []DerivedValue

	cpus := resources.CPUCount
	if cpus < 1 {
		cpus = 1
	}

	// QueueWorkers: derive from CPU count when 0 (sentinel)
	if cfg.QueueWorkers == 0 && !explicitFields["queue-workers"] {
		cfg.QueueWorkers = cpus
		derived = append(derived, DerivedValue{
			Field:   "queue-workers",
			Value:   fmt.Sprintf("%d", cpus),
			Formula: "NumCPU",
		})
	}

	// ExportConcurrency: derive from CPU count when 0
	if cfg.ExportConcurrency == 0 && !explicitFields["export-concurrency"] {
		v := cpus * 4
		cfg.ExportConcurrency = v
		derived = append(derived, DerivedValue{
			Field:   "export-concurrency",
			Value:   fmt.Sprintf("%d", v),
			Formula: "NumCPU * 4",
		})
	}

	// QueuePreparerCount: derive when pipeline split enabled and value is 0
	if cfg.QueuePipelineSplitEnabled && cfg.QueuePreparerCount == 0 && !explicitFields["queue.pipeline_split.preparer_count"] {
		cfg.QueuePreparerCount = cpus
		derived = append(derived, DerivedValue{
			Field:   "queue.pipeline_split.preparer_count",
			Value:   fmt.Sprintf("%d", cpus),
			Formula: "NumCPU",
		})
	}

	// QueueSenderCount: derive when pipeline split enabled and value is 0
	if cfg.QueuePipelineSplitEnabled && cfg.QueueSenderCount == 0 && !explicitFields["queue.pipeline_split.sender_count"] {
		v := cpus * 2
		cfg.QueueSenderCount = v
		derived = append(derived, DerivedValue{
			Field:   "queue.pipeline_split.sender_count",
			Value:   fmt.Sprintf("%d", v),
			Formula: "NumCPU * 2",
		})
	}

	// QueueGlobalSendLimit: derive when 0
	if cfg.QueueGlobalSendLimit == 0 && !explicitFields["queue.global_send_limit"] {
		v := cpus * 8
		cfg.QueueGlobalSendLimit = v
		derived = append(derived, DerivedValue{
			Field:   "queue.global_send_limit",
			Value:   fmt.Sprintf("%d", v),
			Formula: "NumCPU * 8",
		})
	}

	// Memory-based derivations: only when memory is detected and positive
	mem := resources.MemoryBytes
	if mem > 0 && mem != math.MaxInt64 {
		// QueueMaxBytes from memory percentage
		if cfg.QueueMemoryPercent > 0 && !explicitFields["queue-max-bytes"] {
			v := int64(float64(mem) * cfg.QueueMemoryPercent)
			if v > 0 {
				cfg.QueueMaxBytes = v
				derived = append(derived, DerivedValue{
					Field:   "queue-max-bytes",
					Value:   FormatByteSize(v),
					Formula: fmt.Sprintf("MemoryLimit * %.0f%%", cfg.QueueMemoryPercent*100),
				})
			}
		}

		// MaxBatchBytes: derive as min(QueueMaxBytes/4, 8MB) when not explicitly set
		if !explicitFields["max-batch-bytes"] {
			quarter := cfg.QueueMaxBytes / 4
			limit := int64(8388608) // 8 MB
			if quarter > 0 && quarter < int64(limit) {
				cfg.MaxBatchBytes = int(quarter)
				derived = append(derived, DerivedValue{
					Field:   "max-batch-bytes",
					Value:   FormatByteSize(quarter),
					Formula: "min(QueueMaxBytes/4, 8MB)",
				})
			}
		}
	}

	return derived
}
