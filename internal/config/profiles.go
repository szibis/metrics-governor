package config

import (
	"fmt"
	"runtime"
	"strings"
	"time"
)

// ProfileName identifies a configuration profile.
type ProfileName string

const (
	ProfileMinimal     ProfileName = "minimal"
	ProfileBalanced    ProfileName = "balanced"
	ProfilePerformance ProfileName = "performance"
)

// ValidProfileNames returns the set of recognized profile names.
func ValidProfileNames() []ProfileName {
	return []ProfileName{ProfileMinimal, ProfileBalanced, ProfilePerformance}
}

// IsValidProfile reports whether name is a recognized profile (or empty).
func IsValidProfile(name string) bool {
	if name == "" {
		return true
	}
	for _, p := range ValidProfileNames() {
		if string(p) == name {
			return true
		}
	}
	return false
}

// ProfilePrerequisite describes a resource requirement for a profile.
type ProfilePrerequisite struct {
	Type        string // "disk", "memory", "cpu"
	Description string
	Severity    string // "required", "recommended"
}

// ProfileConfig holds the values that a profile sets.
// Pointer fields: nil means "not set by this profile" (use default or user override).
type ProfileConfig struct {
	Name ProfileName

	// Workers & Parallelism
	QueueWorkers              *int
	ExportConcurrency         *int
	QueueMaxConcurrentSends   *int
	QueueGlobalSendLimit      *int
	QueuePipelineSplitEnabled *bool
	QueuePreparerCount        *int
	QueueSenderCount          *int

	// Adaptive features
	QueueAdaptiveWorkersEnabled *bool
	BufferBatchAutoTuneEnabled  *bool

	// Buffer
	BufferSize       *int
	MaxBatchSize     *int
	FlushInterval    *time.Duration
	BufferFullPolicy *string

	// Queue
	QueueMode     *string // "memory", "disk", "hybrid"
	QueueType     *string
	QueueEnabled  *bool
	QueueMaxSize  *int
	QueueMaxBytes *int64

	// Stats
	StatsLevel *string // "none", "basic", "full"

	// Compression & interning
	ExporterCompression *string
	QueueCompression    *string
	StringInterning     *bool

	// FastQueue
	QueueInmemoryBlocks   *int
	QueueMetaSyncInterval *time.Duration

	// Memory
	MemoryLimitRatio    *float64
	BufferMemoryPercent *float64
	QueueMemoryPercent  *float64

	// Resilience
	QueueBackoffEnabled             *bool
	QueueBackoffMultiplier          *float64
	QueueCircuitBreakerEnabled      *bool
	QueueCircuitBreakerThreshold    *int
	QueueCircuitBreakerResetTimeout *time.Duration
	QueueBatchDrainSize             *int
	QueueBurstDrainSize             *int

	// Connection warmup
	ExporterPrewarmConnections *bool

	// Cardinality & governance
	CardinalityMode            *string
	CardinalityExpectedItems   *uint
	LimitsDryRun               *bool
	LimitsStatsThreshold       *int64
	RuleCacheMaxSize           *int
	ReceiverMaxRequestBodySize *int64

	// Bloom persistence
	BloomPersistenceEnabled   *bool
	BloomPersistenceMaxMemory *int64

	// Resource targets (informational, not applied to config)
	TargetCPU     string
	TargetMemory  string
	DiskRequired  bool
	MaxThroughput string
}

// helper functions for pointer creation
func intPtr(v int) *int                     { return &v }
func int64Ptr(v int64) *int64               { return &v }
func uintPtr(v uint) *uint                  { return &v }
func boolPtr(v bool) *bool                  { return &v }
func strPtr(v string) *string               { return &v }
func float64Ptr(v float64) *float64         { return &v }
func durPtr(v time.Duration) *time.Duration { return &v }

// GetProfile returns the ProfileConfig for the given name.
func GetProfile(name ProfileName) (*ProfileConfig, error) {
	switch name {
	case ProfileMinimal:
		return minimalProfile(), nil
	case ProfileBalanced:
		return balancedProfile(), nil
	case ProfilePerformance:
		return performanceProfile(), nil
	default:
		return nil, fmt.Errorf("unknown profile: %q (valid: minimal, balanced, performance)", name)
	}
}

func minimalProfile() *ProfileConfig {
	return &ProfileConfig{
		Name: ProfileMinimal,

		// Workers: single worker, no pool overhead
		QueueWorkers:            intPtr(1),
		ExportConcurrency:       intPtr(1),
		QueueMaxConcurrentSends: intPtr(1),
		QueueGlobalSendLimit:    intPtr(4),

		// Pipeline split off
		QueuePipelineSplitEnabled: boolPtr(false),

		// Adaptive features off — no background goroutines
		QueueAdaptiveWorkersEnabled: boolPtr(false),
		BufferBatchAutoTuneEnabled:  boolPtr(false),

		// Small buffer
		BufferSize:       intPtr(1000),
		MaxBatchSize:     intPtr(200),
		FlushInterval:    durPtr(10 * time.Second),
		BufferFullPolicy: strPtr("reject"),

		// No queue — direct export
		QueueMode:     strPtr("memory"),
		QueueType:     strPtr("memory"),
		QueueEnabled:  boolPtr(false),
		QueueMaxSize:  intPtr(1000),
		QueueMaxBytes: int64Ptr(67108864), // 64 MB

		// Stats: disabled for minimal overhead
		StatsLevel: strPtr("none"),

		// No compression — saves CPU
		ExporterCompression: strPtr("none"),
		QueueCompression:    strPtr("none"),
		StringInterning:     boolPtr(false),

		// FastQueue (minimal even if enabled later)
		QueueInmemoryBlocks:   intPtr(256),
		QueueMetaSyncInterval: durPtr(5 * time.Second),

		// Tight memory ratio
		MemoryLimitRatio:    float64Ptr(0.90),
		BufferMemoryPercent: float64Ptr(0.15),
		QueueMemoryPercent:  float64Ptr(0.0),

		// Resilience off — fail fast in dev
		QueueBackoffEnabled:        boolPtr(false),
		QueueCircuitBreakerEnabled: boolPtr(false),
		QueueBatchDrainSize:        intPtr(5),
		QueueBurstDrainSize:        intPtr(50),

		// No warmup
		ExporterPrewarmConnections: boolPtr(false),

		// Governance: observe only
		CardinalityMode:            strPtr("exact"),
		CardinalityExpectedItems:   uintPtr(10000),
		LimitsDryRun:               boolPtr(true),
		LimitsStatsThreshold:       int64Ptr(100),
		RuleCacheMaxSize:           intPtr(1000),
		ReceiverMaxRequestBodySize: int64Ptr(4194304), // 4 MB

		// Bloom persistence off
		BloomPersistenceEnabled:   boolPtr(false),
		BloomPersistenceMaxMemory: int64Ptr(33554432), // 32 MB

		// Resource targets
		TargetCPU:     "0.25-0.5 cores",
		TargetMemory:  "128-256 MB",
		DiskRequired:  false,
		MaxThroughput: "~10k dps",
	}
}

func balancedProfile() *ProfileConfig {
	numCPU := runtime.NumCPU()
	workers := numCPU / 2
	if workers < 2 {
		workers = 2
	}

	return &ProfileConfig{
		Name: ProfileBalanced,

		// Workers: moderate parallelism
		QueueWorkers:            intPtr(workers),
		ExportConcurrency:       intPtr(numCPU * 4),
		QueueMaxConcurrentSends: intPtr(4),
		QueueGlobalSendLimit:    intPtr(numCPU * 8),

		// Pipeline split off at this scale
		QueuePipelineSplitEnabled: boolPtr(false),

		// Adaptive features ON — self-tunes
		QueueAdaptiveWorkersEnabled: boolPtr(true),
		BufferBatchAutoTuneEnabled:  boolPtr(true),

		// Standard buffer
		BufferSize:       intPtr(5000),
		MaxBatchSize:     intPtr(500),
		FlushInterval:    durPtr(5 * time.Second),
		BufferFullPolicy: strPtr("reject"),

		// Memory queue enabled
		QueueMode:     strPtr("memory"),
		QueueType:     strPtr("memory"),
		QueueEnabled:  boolPtr(true),
		QueueMaxSize:  intPtr(5000),
		QueueMaxBytes: int64Ptr(268435456), // 256 MB

		// Stats: basic per-metric counts
		StatsLevel: strPtr("basic"),

		// Snappy compression
		ExporterCompression: strPtr("snappy"),
		QueueCompression:    strPtr("snappy"),
		StringInterning:     boolPtr(true),

		// FastQueue standard
		QueueInmemoryBlocks:   intPtr(1024),
		QueueMetaSyncInterval: durPtr(1 * time.Second),

		// Balanced memory ratio
		MemoryLimitRatio:    float64Ptr(0.85),
		BufferMemoryPercent: float64Ptr(0.10),
		QueueMemoryPercent:  float64Ptr(0.10),

		// Resilience on
		QueueBackoffEnabled:             boolPtr(true),
		QueueBackoffMultiplier:          float64Ptr(2.0),
		QueueCircuitBreakerEnabled:      boolPtr(true),
		QueueCircuitBreakerThreshold:    intPtr(5),
		QueueCircuitBreakerResetTimeout: durPtr(30 * time.Second),
		QueueBatchDrainSize:             intPtr(10),
		QueueBurstDrainSize:             intPtr(100),

		// Warmup on
		ExporterPrewarmConnections: boolPtr(true),

		// Governance: smart protection
		CardinalityMode:            strPtr("bloom"),
		CardinalityExpectedItems:   uintPtr(100000),
		LimitsDryRun:               boolPtr(false),
		LimitsStatsThreshold:       int64Ptr(0),
		RuleCacheMaxSize:           intPtr(10000),
		ReceiverMaxRequestBodySize: int64Ptr(16777216), // 16 MB

		// Bloom persistence off (memory queue)
		BloomPersistenceEnabled:   boolPtr(false),
		BloomPersistenceMaxMemory: int64Ptr(134217728), // 128 MB

		// Resource targets
		TargetCPU:     "1-2 cores",
		TargetMemory:  "256 MB - 1 GB",
		DiskRequired:  false,
		MaxThroughput: "~100k dps",
	}
}

func performanceProfile() *ProfileConfig {
	numCPU := runtime.NumCPU()

	return &ProfileConfig{
		Name: ProfilePerformance,

		// Workers: maximum parallelism
		QueueWorkers:            intPtr(numCPU * 2),
		ExportConcurrency:       intPtr(numCPU * 4),
		QueueMaxConcurrentSends: intPtr(numCPU),
		QueueGlobalSendLimit:    intPtr(numCPU * 8),

		// Pipeline split ON
		QueuePipelineSplitEnabled: boolPtr(true),
		QueuePreparerCount:        intPtr(numCPU),
		QueueSenderCount:          intPtr(numCPU * 2),

		// All adaptive features ON
		QueueAdaptiveWorkersEnabled: boolPtr(true),
		BufferBatchAutoTuneEnabled:  boolPtr(true),

		// Large buffer
		BufferSize:       intPtr(50000),
		MaxBatchSize:     intPtr(1000),
		FlushInterval:    durPtr(2 * time.Second),
		BufferFullPolicy: strPtr("drop_oldest"),

		// Hybrid queue — memory L1 with disk spillover
		QueueMode:     strPtr("hybrid"),
		QueueType:     strPtr("disk"),
		QueueEnabled:  boolPtr(true),
		QueueMaxSize:  intPtr(50000),
		QueueMaxBytes: int64Ptr(2147483648), // 2 GB fallback

		// Stats: basic per-metric counts (full cardinality too expensive at scale)
		StatsLevel: strPtr("basic"),

		// Zstd compression
		ExporterCompression: strPtr("zstd"),
		QueueCompression:    strPtr("snappy"),
		StringInterning:     boolPtr(true),

		// FastQueue large
		QueueInmemoryBlocks:   intPtr(4096),
		QueueMetaSyncInterval: durPtr(500 * time.Millisecond),

		// More headroom for disk I/O
		MemoryLimitRatio:    float64Ptr(0.80),
		BufferMemoryPercent: float64Ptr(0.10),
		QueueMemoryPercent:  float64Ptr(0.15),

		// Aggressive resilience
		QueueBackoffEnabled:             boolPtr(true),
		QueueBackoffMultiplier:          float64Ptr(3.0),
		QueueCircuitBreakerEnabled:      boolPtr(true),
		QueueCircuitBreakerThreshold:    intPtr(3),
		QueueCircuitBreakerResetTimeout: durPtr(15 * time.Second),
		QueueBatchDrainSize:             intPtr(25),
		QueueBurstDrainSize:             intPtr(250),

		// Warmup on
		ExporterPrewarmConnections: boolPtr(true),

		// Governance: maximum throughput protection
		CardinalityMode:            strPtr("hybrid"),
		CardinalityExpectedItems:   uintPtr(500000),
		LimitsDryRun:               boolPtr(false),
		LimitsStatsThreshold:       int64Ptr(0),
		RuleCacheMaxSize:           intPtr(50000),
		ReceiverMaxRequestBodySize: int64Ptr(67108864), // 64 MB

		// Bloom persistence on
		BloomPersistenceEnabled:   boolPtr(true),
		BloomPersistenceMaxMemory: int64Ptr(268435456), // 256 MB

		// Resource targets
		TargetCPU:     "4+ cores",
		TargetMemory:  "1-4 GB",
		DiskRequired:  true,
		MaxThroughput: "~500k+ dps",
	}
}

// Prerequisites returns resource requirements for this profile.
func (p *ProfileConfig) Prerequisites() []ProfilePrerequisite {
	switch p.Name {
	case ProfileMinimal:
		return nil
	case ProfileBalanced:
		return []ProfilePrerequisite{
			{Type: "memory", Description: "At least 512 MB memory for adaptive tuning overhead", Severity: "recommended"},
		}
	case ProfilePerformance:
		return []ProfilePrerequisite{
			{Type: "disk", Description: "Persistent disk (PVC) for disk queue", Severity: "required"},
			{Type: "cpu", Description: "At least 2 CPU cores for pipeline split", Severity: "required"},
			{Type: "memory", Description: "At least 1 GB memory", Severity: "recommended"},
			{Type: "disk", Description: "SSD/NVMe storage for low-latency queue I/O", Severity: "recommended"},
		}
	}
	return nil
}

// ApplyProfile applies profile values to cfg, skipping fields present in explicitFields.
// explicitFields maps flag/YAML field names to true when the user explicitly set them.
func ApplyProfile(cfg *Config, profile ProfileName, explicitFields map[string]bool) error {
	p, err := GetProfile(profile)
	if err != nil {
		return err
	}

	set := func(fieldName string, apply func()) {
		if !explicitFields[fieldName] {
			apply()
		}
	}

	// Workers & Parallelism
	if p.QueueWorkers != nil {
		set("queue-workers", func() { cfg.QueueWorkers = *p.QueueWorkers })
	}
	if p.ExportConcurrency != nil {
		set("export-concurrency", func() { cfg.ExportConcurrency = *p.ExportConcurrency })
	}
	if p.QueueMaxConcurrentSends != nil {
		set("queue.max_concurrent_sends", func() { cfg.QueueMaxConcurrentSends = *p.QueueMaxConcurrentSends })
	}
	if p.QueueGlobalSendLimit != nil {
		set("queue.global_send_limit", func() { cfg.QueueGlobalSendLimit = *p.QueueGlobalSendLimit })
	}
	if p.QueuePipelineSplitEnabled != nil {
		set("queue.pipeline_split.enabled", func() { cfg.QueuePipelineSplitEnabled = *p.QueuePipelineSplitEnabled })
	}
	if p.QueuePreparerCount != nil {
		set("queue.pipeline_split.preparer_count", func() { cfg.QueuePreparerCount = *p.QueuePreparerCount })
	}
	if p.QueueSenderCount != nil {
		set("queue.pipeline_split.sender_count", func() { cfg.QueueSenderCount = *p.QueueSenderCount })
	}

	// Adaptive features
	if p.QueueAdaptiveWorkersEnabled != nil {
		set("queue.adaptive_workers.enabled", func() { cfg.QueueAdaptiveWorkersEnabled = *p.QueueAdaptiveWorkersEnabled })
	}
	if p.BufferBatchAutoTuneEnabled != nil {
		set("buffer.batch_auto_tune.enabled", func() { cfg.BufferBatchAutoTuneEnabled = *p.BufferBatchAutoTuneEnabled })
	}

	// Buffer
	if p.BufferSize != nil {
		set("buffer-size", func() { cfg.BufferSize = *p.BufferSize })
	}
	if p.MaxBatchSize != nil {
		set("batch-size", func() { cfg.MaxBatchSize = *p.MaxBatchSize })
	}
	if p.FlushInterval != nil {
		set("flush-interval", func() { cfg.FlushInterval = *p.FlushInterval })
	}
	if p.BufferFullPolicy != nil {
		set("buffer-full-policy", func() { cfg.BufferFullPolicy = *p.BufferFullPolicy })
	}

	// Queue
	if p.QueueMode != nil {
		set("queue-mode", func() { cfg.QueueMode = *p.QueueMode })
	}
	if p.QueueType != nil {
		set("queue-type", func() { cfg.QueueType = *p.QueueType })
	}
	if p.QueueEnabled != nil {
		set("queue-enabled", func() { cfg.QueueEnabled = *p.QueueEnabled })
	}
	if p.QueueMaxSize != nil {
		set("queue-max-size", func() { cfg.QueueMaxSize = *p.QueueMaxSize })
	}
	if p.QueueMaxBytes != nil {
		set("queue-max-bytes", func() { cfg.QueueMaxBytes = *p.QueueMaxBytes })
	}

	// Stats
	if p.StatsLevel != nil {
		set("stats-level", func() { cfg.StatsLevel = *p.StatsLevel })
	}

	// Compression & interning
	if p.ExporterCompression != nil {
		set("exporter-compression", func() { cfg.ExporterCompression = *p.ExporterCompression })
	}
	if p.QueueCompression != nil {
		set("queue-compression", func() { cfg.QueueCompression = *p.QueueCompression })
	}
	if p.StringInterning != nil {
		set("string-interning", func() { cfg.StringInterning = *p.StringInterning })
	}

	// FastQueue
	if p.QueueInmemoryBlocks != nil {
		set("queue-inmemory-blocks", func() { cfg.QueueInmemoryBlocks = *p.QueueInmemoryBlocks })
	}
	if p.QueueMetaSyncInterval != nil {
		set("queue-meta-sync", func() { cfg.QueueMetaSyncInterval = *p.QueueMetaSyncInterval })
	}

	// Memory
	if p.MemoryLimitRatio != nil {
		set("memory-limit-ratio", func() { cfg.MemoryLimitRatio = *p.MemoryLimitRatio })
	}
	if p.BufferMemoryPercent != nil {
		set("buffer-memory-percent", func() { cfg.BufferMemoryPercent = *p.BufferMemoryPercent })
	}
	if p.QueueMemoryPercent != nil {
		set("queue-memory-percent", func() { cfg.QueueMemoryPercent = *p.QueueMemoryPercent })
	}

	// Resilience
	if p.QueueBackoffEnabled != nil {
		set("queue-backoff-enabled", func() { cfg.QueueBackoffEnabled = *p.QueueBackoffEnabled })
	}
	if p.QueueBackoffMultiplier != nil {
		set("queue-backoff-multiplier", func() { cfg.QueueBackoffMultiplier = *p.QueueBackoffMultiplier })
	}
	if p.QueueCircuitBreakerEnabled != nil {
		set("queue-circuit-breaker-enabled", func() { cfg.QueueCircuitBreakerEnabled = *p.QueueCircuitBreakerEnabled })
	}
	if p.QueueCircuitBreakerThreshold != nil {
		set("queue-circuit-breaker-threshold", func() { cfg.QueueCircuitBreakerThreshold = *p.QueueCircuitBreakerThreshold })
	}
	if p.QueueCircuitBreakerResetTimeout != nil {
		set("queue-circuit-breaker-reset-timeout", func() { cfg.QueueCircuitBreakerResetTimeout = *p.QueueCircuitBreakerResetTimeout })
	}
	if p.QueueBatchDrainSize != nil {
		set("queue-batch-drain-size", func() { cfg.QueueBatchDrainSize = *p.QueueBatchDrainSize })
	}
	if p.QueueBurstDrainSize != nil {
		set("queue-burst-drain-size", func() { cfg.QueueBurstDrainSize = *p.QueueBurstDrainSize })
	}

	// Connection warmup
	if p.ExporterPrewarmConnections != nil {
		set("exporter-prewarm-connections", func() { cfg.ExporterPrewarmConnections = *p.ExporterPrewarmConnections })
	}

	// Cardinality & governance
	if p.CardinalityMode != nil {
		set("cardinality-mode", func() { cfg.CardinalityMode = *p.CardinalityMode })
	}
	if p.CardinalityExpectedItems != nil {
		set("cardinality-expected-items", func() { cfg.CardinalityExpectedItems = *p.CardinalityExpectedItems })
	}
	if p.LimitsDryRun != nil {
		set("limits-dry-run", func() { cfg.LimitsDryRun = *p.LimitsDryRun })
	}
	if p.LimitsStatsThreshold != nil {
		set("limits-stats-threshold", func() { cfg.LimitsStatsThreshold = *p.LimitsStatsThreshold })
	}
	if p.RuleCacheMaxSize != nil {
		set("rule-cache-max-size", func() { cfg.RuleCacheMaxSize = *p.RuleCacheMaxSize })
	}
	if p.ReceiverMaxRequestBodySize != nil {
		set("receiver-max-request-body-size", func() { cfg.ReceiverMaxRequestBodySize = *p.ReceiverMaxRequestBodySize })
	}

	// Bloom persistence
	if p.BloomPersistenceEnabled != nil {
		set("bloom-persistence-enabled", func() { cfg.BloomPersistenceEnabled = *p.BloomPersistenceEnabled })
	}
	if p.BloomPersistenceMaxMemory != nil {
		set("bloom-persistence-max-memory", func() { cfg.BloomPersistenceMaxMemory = *p.BloomPersistenceMaxMemory })
	}

	return nil
}

// profileParam describes one parameter set by a profile, for DumpProfile output.
type profileParam struct {
	Name        string
	Value       string
	Description string
}

// DumpProfile returns a human-readable table of all values the given profile sets.
func DumpProfile(name ProfileName) (string, error) {
	p, err := GetProfile(name)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Profile: %s", name)
	if name == ProfileBalanced {
		b.WriteString(" (default)")
	}
	b.WriteString("\n")
	b.WriteString(strings.Repeat("=", 60))
	b.WriteString("\n\n")

	// Prerequisites
	prereqs := p.Prerequisites()
	if len(prereqs) > 0 {
		b.WriteString("Prerequisites:\n")
		for _, pr := range prereqs {
			marker := "o"
			if pr.Severity == "required" {
				marker = "!"
			}
			fmt.Fprintf(&b, "  %s %s  %s\n", marker, strings.ToUpper(pr.Severity), pr.Description)
		}
		b.WriteString("\n")
	}

	fmt.Fprintf(&b, "Resource targets: %s CPU, %s memory", p.TargetCPU, p.TargetMemory)
	if p.DiskRequired {
		b.WriteString(", disk required")
	} else {
		b.WriteString(", no disk required")
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "Max throughput: %s\n\n", p.MaxThroughput)

	// Parameters table
	params := collectProfileParams(p)

	b.WriteString("Parameters:\n")
	b.WriteString(strings.Repeat("-", 60))
	b.WriteString("\n")
	fmt.Fprintf(&b, "  %-36s %-12s %s\n", "Parameter", "Value", "Description")
	b.WriteString(strings.Repeat("-", 60))
	b.WriteString("\n")
	for _, param := range params {
		fmt.Fprintf(&b, "  %-36s %-12s %s\n", param.Name, param.Value, param.Description)
	}

	// Governance section
	b.WriteString("\nGovernance:\n")
	b.WriteString(strings.Repeat("-", 60))
	b.WriteString("\n")
	govParams := collectGovernanceParams(p)
	for _, param := range govParams {
		fmt.Fprintf(&b, "  %-36s %-12s %s\n", param.Name, param.Value, param.Description)
	}

	return b.String(), nil
}

func collectProfileParams(p *ProfileConfig) []profileParam {
	var params []profileParam

	if p.QueueWorkers != nil {
		v := fmt.Sprintf("%d", *p.QueueWorkers)
		params = append(params, profileParam{"queue.workers", v, "Queue worker goroutines"})
	}
	if p.ExportConcurrency != nil {
		v := fmt.Sprintf("%d", *p.ExportConcurrency)
		params = append(params, profileParam{"export_concurrency", v, "Parallel export goroutines"})
	}
	if p.QueuePipelineSplitEnabled != nil {
		v := fmt.Sprintf("%t", *p.QueuePipelineSplitEnabled)
		params = append(params, profileParam{"pipeline_split.enabled", v, "Separate CPU/IO workers"})
	}
	if p.QueueAdaptiveWorkersEnabled != nil {
		v := fmt.Sprintf("%t", *p.QueueAdaptiveWorkersEnabled)
		params = append(params, profileParam{"adaptive_workers.enabled", v, "AIMD worker scaling"})
	}
	if p.BufferBatchAutoTuneEnabled != nil {
		v := fmt.Sprintf("%t", *p.BufferBatchAutoTuneEnabled)
		params = append(params, profileParam{"batch_auto_tune.enabled", v, "AIMD batch sizing"})
	}
	if p.BufferSize != nil {
		v := fmt.Sprintf("%d", *p.BufferSize)
		params = append(params, profileParam{"buffer.size", v, "Max buffered entries"})
	}
	if p.MaxBatchSize != nil {
		v := fmt.Sprintf("%d", *p.MaxBatchSize)
		params = append(params, profileParam{"buffer.batch_size", v, "Max batch size"})
	}
	if p.FlushInterval != nil {
		params = append(params, profileParam{"buffer.flush_interval", p.FlushInterval.String(), "Flush timer"})
	}
	if p.QueueMode != nil {
		params = append(params, profileParam{"queue.mode", *p.QueueMode, "Queue serialization mode"})
	}
	if p.QueueEnabled != nil {
		v := fmt.Sprintf("%t", *p.QueueEnabled)
		params = append(params, profileParam{"queue.enabled", v, "Persistent queue"})
	}
	if p.QueueType != nil {
		params = append(params, profileParam{"queue.type", *p.QueueType, "Queue backend"})
	}
	if p.StatsLevel != nil {
		params = append(params, profileParam{"stats.level", *p.StatsLevel, "Stats collection level"})
	}
	if p.QueueMaxSize != nil {
		v := fmt.Sprintf("%d", *p.QueueMaxSize)
		params = append(params, profileParam{"queue.max_size", v, "Max queue entries"})
	}
	if p.QueueMaxBytes != nil {
		params = append(params, profileParam{"queue.max_bytes", FormatByteSize(*p.QueueMaxBytes), "Max queue bytes"})
	}
	if p.ExporterCompression != nil {
		params = append(params, profileParam{"exporter.compression", *p.ExporterCompression, "Export compression"})
	}
	if p.StringInterning != nil {
		v := fmt.Sprintf("%t", *p.StringInterning)
		params = append(params, profileParam{"string_interning", v, "Label deduplication"})
	}
	if p.QueueInmemoryBlocks != nil {
		v := fmt.Sprintf("%d", *p.QueueInmemoryBlocks)
		params = append(params, profileParam{"queue.inmemory_blocks", v, "In-memory channel size"})
	}
	if p.MemoryLimitRatio != nil {
		v := fmt.Sprintf("%.2f", *p.MemoryLimitRatio)
		params = append(params, profileParam{"memory.limit_ratio", v, "GOMEMLIMIT ratio"})
	}
	if p.ExporterPrewarmConnections != nil {
		v := fmt.Sprintf("%t", *p.ExporterPrewarmConnections)
		params = append(params, profileParam{"exporter.prewarm", v, "Connection warmup"})
	}

	return params
}

func collectGovernanceParams(p *ProfileConfig) []profileParam {
	var params []profileParam

	if p.CardinalityMode != nil {
		params = append(params, profileParam{"cardinality_mode", *p.CardinalityMode, "Cardinality tracking"})
	}
	if p.CardinalityExpectedItems != nil {
		v := fmt.Sprintf("%d", *p.CardinalityExpectedItems)
		params = append(params, profileParam{"max_cardinality", v, "Default max cardinality"})
	}
	if p.LimitsDryRun != nil {
		v := fmt.Sprintf("%t", *p.LimitsDryRun)
		params = append(params, profileParam{"limits.dry_run", v, "Observe-only mode"})
	}
	if p.BufferFullPolicy != nil {
		params = append(params, profileParam{"buffer.full_policy", *p.BufferFullPolicy, "Buffer overflow action"})
	}
	if p.ReceiverMaxRequestBodySize != nil {
		params = append(params, profileParam{"receiver.max_body_size", FormatByteSize(*p.ReceiverMaxRequestBodySize), "Request size limit"})
	}
	if p.QueueCircuitBreakerEnabled != nil {
		v := fmt.Sprintf("%t", *p.QueueCircuitBreakerEnabled)
		params = append(params, profileParam{"circuit_breaker.enabled", v, "Fail-fast pattern"})
	}
	if p.QueueCircuitBreakerThreshold != nil {
		v := fmt.Sprintf("%d", *p.QueueCircuitBreakerThreshold)
		params = append(params, profileParam{"circuit_breaker.threshold", v, "Failures before open"})
	}
	if p.BloomPersistenceEnabled != nil {
		v := fmt.Sprintf("%t", *p.BloomPersistenceEnabled)
		params = append(params, profileParam{"bloom_persistence", v, "Survive restarts"})
	}
	if p.RuleCacheMaxSize != nil {
		v := fmt.Sprintf("%d", *p.RuleCacheMaxSize)
		params = append(params, profileParam{"rule_cache_size", v, "Rule matching cache"})
	}

	return params
}
