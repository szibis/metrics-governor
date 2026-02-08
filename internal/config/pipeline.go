package config

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// BuildResult holds the output of the full configuration pipeline.
type BuildResult struct {
	Config         *Config
	ExplicitFields map[string]bool
	ProfileApplied ProfileName
	Derivations    []DerivedValue
	Deprecations   []DeprecationWarning
}

// BuildConfig runs the full configuration resolution pipeline:
//
//	DefaultConfig → Profile → YAML → CLI → Consolidated → AutoDerive → Deprecation warnings → Validate
//
// This replaces the simpler ParseFlags() → main.go flow with a comprehensive pipeline.
func BuildConfig() (*BuildResult, error) {
	result := &BuildResult{}

	// Step 1: Start from defaults
	cfg := DefaultConfig()

	// Parse flags (but don't validate yet)
	var configFile string
	// We need to register and parse flags first
	// NOTE: ParseFlags already registered flags, so we use flag.Parsed()
	// The flags are registered in ParseFlags; here we just gather results

	// Since ParseFlags already called flag.Parse(), we work with the parsed state
	if !flag.Parsed() {
		return nil, fmt.Errorf("flags not parsed; call flag.Parse() first")
	}

	// Collect explicitly-set CLI flags
	cliExplicit := ExplicitFlags()

	// Get config file from flags
	if f := flag.Lookup("config"); f != nil {
		configFile = f.Value.String()
	}

	// Step 2: Apply profile (before YAML so YAML overrides profile)
	profileName := cfg.Profile
	if f := flag.Lookup("profile"); f != nil && cliExplicit["profile"] {
		profileName = f.Value.String()
	}

	// Step 3: Load YAML config if specified
	var yamlExplicit map[string]bool
	if configFile != "" {
		// Two-pass: first detect explicit fields, then parse typed config
		data, err := os.ReadFile(configFile)
		if err != nil {
			return nil, fmt.Errorf("reading config file %s: %w", configFile, err)
		}

		yamlExplicit, err = ExplicitYAMLFields(data)
		if err != nil {
			return nil, fmt.Errorf("analyzing config file %s: %w", configFile, err)
		}

		// Check if YAML sets profile
		var rawTop map[string]interface{}
		if err := yaml.Unmarshal(data, &rawTop); err == nil {
			if p, ok := rawTop["profile"].(string); ok && !cliExplicit["profile"] {
				profileName = p
			}
		}

		yamlCfg, err := LoadYAML(configFile)
		if err != nil {
			return nil, fmt.Errorf("loading config file %s: %w", configFile, err)
		}
		yamlOverlay := yamlCfg.ToConfig()

		// Apply profile first (before YAML overlay)
		if profileName != "" {
			explicitForProfile := MergeExplicitFields(cliExplicit, yamlExplicit)
			if err := ApplyProfile(cfg, ProfileName(profileName), explicitForProfile); err != nil {
				return nil, fmt.Errorf("applying profile %q: %w", profileName, err)
			}
			result.ProfileApplied = ProfileName(profileName)
		}

		// Then overlay YAML values
		mergeYAMLIntoConfig(cfg, yamlOverlay)
		cfg.ConfigFile = configFile
	} else {
		// No YAML config — apply profile with only CLI explicit fields
		if profileName != "" {
			if err := ApplyProfile(cfg, ProfileName(profileName), cliExplicit); err != nil {
				return nil, fmt.Errorf("applying profile %q: %w", profileName, err)
			}
			result.ProfileApplied = ProfileName(profileName)
		}
	}

	// Step 4: Apply CLI flag overrides (these win over profile + YAML)
	applyFlagOverrides(cfg)

	// Merge all explicit fields
	if yamlExplicit == nil {
		yamlExplicit = make(map[string]bool)
	}
	result.ExplicitFields = MergeExplicitFields(cliExplicit, yamlExplicit)

	// Step 5: Expand consolidated params (parallelism → workers, etc.)
	consolidatedDerived := ExpandConsolidatedParams(cfg, result.ExplicitFields)
	result.Derivations = append(result.Derivations, consolidatedDerived...)

	// Step 6: Auto-derive from resources (CPU/memory)
	resources := DetectResources()
	autoDerived := AutoDerive(cfg, resources, result.ExplicitFields)
	result.Derivations = append(result.Derivations, autoDerived...)

	// Step 7: Deprecation warnings
	registry := NewDeprecationRegistry(GetVersion())
	for _, entry := range DefaultDeprecations() {
		registry.Register(entry)
	}
	result.Deprecations = registry.CheckAndWarn(result.ExplicitFields)

	// Step 8: Validate
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	result.Config = cfg
	return result, nil
}

// HandleEarlyExits checks for flags like --show-profile, --show-effective-config,
// --show-deprecations, --help, --version that should print output and exit.
// Returns true if the program should exit (output was printed).
func HandleEarlyExits(cfg *Config) bool {
	if cfg.ShowHelp {
		PrintUsage()
		return true
	}

	if cfg.ShowVersion {
		PrintVersion()
		return true
	}

	if cfg.ShowProfile != "" {
		output, err := DumpProfile(ProfileName(cfg.ShowProfile))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(output)
		return true
	}

	if cfg.ShowDeprecations {
		registry := NewDeprecationRegistry(GetVersion())
		for _, entry := range DefaultDeprecations() {
			registry.Register(entry)
		}
		fmt.Println(registry.DumpDeprecations())
		return true
	}

	return false
}

// DumpEffectiveConfig returns the effective config as a human-readable summary.
func DumpEffectiveConfig(result *BuildResult) string {
	var b strings.Builder

	cfg := result.Config

	fmt.Fprintf(&b, "Effective Configuration\n")
	b.WriteString(strings.Repeat("=", 60))
	b.WriteString("\n\n")

	if result.ProfileApplied != "" {
		fmt.Fprintf(&b, "Profile: %s\n", result.ProfileApplied)
	}
	b.WriteString("\n")

	// Key settings
	b.WriteString("Receiver:\n")
	fmt.Fprintf(&b, "  gRPC: %s\n", cfg.GRPCListenAddr)
	fmt.Fprintf(&b, "  HTTP: %s\n", cfg.HTTPListenAddr)
	b.WriteString("\n")

	b.WriteString("Exporter:\n")
	fmt.Fprintf(&b, "  endpoint: %s\n", cfg.ExporterEndpoint)
	fmt.Fprintf(&b, "  protocol: %s\n", cfg.ExporterProtocol)
	fmt.Fprintf(&b, "  compression: %s\n", cfg.ExporterCompression)
	fmt.Fprintf(&b, "  timeout: %s\n", cfg.ExporterTimeout)
	b.WriteString("\n")

	b.WriteString("Buffer:\n")
	fmt.Fprintf(&b, "  size: %d\n", cfg.BufferSize)
	fmt.Fprintf(&b, "  batch_size: %d\n", cfg.MaxBatchSize)
	fmt.Fprintf(&b, "  flush_interval: %s\n", cfg.FlushInterval)
	fmt.Fprintf(&b, "  full_policy: %s\n", cfg.BufferFullPolicy)
	fmt.Fprintf(&b, "  batch_auto_tune: %t\n", cfg.BufferBatchAutoTuneEnabled)
	b.WriteString("\n")

	b.WriteString("Queue:\n")
	fmt.Fprintf(&b, "  enabled: %t\n", cfg.QueueEnabled)
	fmt.Fprintf(&b, "  type: %s\n", cfg.QueueType)
	fmt.Fprintf(&b, "  workers: %d\n", cfg.QueueWorkers)
	fmt.Fprintf(&b, "  max_size: %d\n", cfg.QueueMaxSize)
	fmt.Fprintf(&b, "  max_bytes: %s\n", FormatByteSize(cfg.QueueMaxBytes))
	fmt.Fprintf(&b, "  adaptive_workers: %t\n", cfg.QueueAdaptiveWorkersEnabled)
	fmt.Fprintf(&b, "  pipeline_split: %t\n", cfg.QueuePipelineSplitEnabled)
	if cfg.QueuePipelineSplitEnabled {
		fmt.Fprintf(&b, "  preparer_count: %d\n", cfg.QueuePreparerCount)
		fmt.Fprintf(&b, "  sender_count: %d\n", cfg.QueueSenderCount)
	}
	b.WriteString("\n")

	b.WriteString("Resilience:\n")
	fmt.Fprintf(&b, "  backoff: %t (multiplier: %.1f)\n", cfg.QueueBackoffEnabled, cfg.QueueBackoffMultiplier)
	fmt.Fprintf(&b, "  circuit_breaker: %t (threshold: %d, reset: %s)\n",
		cfg.QueueCircuitBreakerEnabled, cfg.QueueCircuitBreakerThreshold, cfg.QueueCircuitBreakerResetTimeout)
	b.WriteString("\n")

	b.WriteString("Memory:\n")
	fmt.Fprintf(&b, "  limit_ratio: %.2f\n", cfg.MemoryLimitRatio)
	fmt.Fprintf(&b, "  buffer_percent: %.2f\n", cfg.BufferMemoryPercent)
	fmt.Fprintf(&b, "  queue_percent: %.2f\n", cfg.QueueMemoryPercent)
	b.WriteString("\n")

	b.WriteString("Performance:\n")
	fmt.Fprintf(&b, "  export_concurrency: %d\n", cfg.ExportConcurrency)
	fmt.Fprintf(&b, "  string_interning: %t\n", cfg.StringInterning)
	fmt.Fprintf(&b, "  cardinality_mode: %s\n", cfg.CardinalityMode)
	b.WriteString("\n")

	// Derivations
	if len(result.Derivations) > 0 {
		b.WriteString("Auto-derived values:\n")
		for _, d := range result.Derivations {
			fmt.Fprintf(&b, "  %s = %s (%s)\n", d.Field, d.Value, d.Formula)
		}
		b.WriteString("\n")
	}

	// Deprecation warnings
	if len(result.Deprecations) > 0 {
		b.WriteString("Deprecation warnings:\n")
		for _, w := range result.Deprecations {
			fmt.Fprintf(&b, "  [%s] %s\n", w.Stage, w.Message)
		}
	}

	return b.String()
}

// ParseFlagsOnly registers and parses all CLI flags, returning the config
// with flag values applied but WITHOUT running the full pipeline.
// Used by BuildConfig internally. For the full pipeline, use BuildConfig().
func ParseFlagsOnly() *Config {
	cfg := DefaultConfig()

	// Config file flag
	flag.StringVar(new(string), "config", "", "Path to YAML configuration file")

	// Profile & simplified config flags
	flag.StringVar(&cfg.Profile, "profile", "balanced", "Configuration profile: minimal (dev), balanced (production default), performance (high throughput)")
	flag.IntVar(&cfg.Parallelism, "parallelism", 0, "Parallelism level (0 = auto from CPU)")
	flag.Float64Var(&cfg.MemoryBudgetPct, "memory-budget-percent", 0, "Memory budget as fraction (0.0-0.5)")
	flag.DurationVar(&cfg.ExportTimeoutBase, "export-timeout", 0, "Base export timeout for cascade derivation")
	flag.StringVar(&cfg.ResilienceLevel, "resilience-level", "", "Resilience preset: low, medium, high")

	// Introspection flags
	flag.StringVar(&cfg.ShowProfile, "show-profile", "", "Show profile settings and exit")
	flag.BoolVar(&cfg.ShowEffectiveConfig, "show-effective-config", false, "Show final merged config and exit")
	flag.BoolVar(&cfg.ShowDeprecations, "show-deprecations", false, "Show deprecation tracker and exit")
	flag.BoolVar(&cfg.StrictDeprecations, "strict-deprecations", false, "Fail startup on removed deprecated params")

	// ... all other flags are registered in ParseFlags()
	// This function exists for the pipeline to register new flags

	return cfg
}

// LogDerivations logs all auto-derived values. Called from main after BuildConfig.
func LogDerivations(derivations []DerivedValue, logFn func(msg string, fields map[string]interface{})) {
	for _, d := range derivations {
		logFn("auto-derived config value", map[string]interface{}{
			"field":   d.Field,
			"value":   d.Value,
			"formula": d.Formula,
		})
	}
}

// LogDeprecations logs deprecation warnings. Called from main after BuildConfig.
func LogDeprecations(warnings []DeprecationWarning, infoFn, warnFn, errorFn func(msg string, fields map[string]interface{})) {
	for _, w := range warnings {
		fields := map[string]interface{}{
			"old_param": w.Entry.OldName,
			"new_param": w.Entry.NewName,
			"removal":   w.Entry.RemovedIn,
		}
		switch w.Stage {
		case StageAnnounced:
			infoFn(w.Message, fields)
		case StageDeprecated, StageWarning:
			warnFn(w.Message, fields)
		case StageRemoved:
			errorFn(w.Message, fields)
		}
	}
}

// CheckStrictDeprecations returns an error if --strict-deprecations is set
// and any REMOVED-stage params are in use.
func CheckStrictDeprecations(warnings []DeprecationWarning) error {
	for _, w := range warnings {
		if w.Stage == StageRemoved {
			return fmt.Errorf("strict-deprecations: removed parameter --%s is in use. %s",
				w.Entry.OldName, w.Message)
		}
	}
	return nil
}

// LogProfileInfo logs the profile startup message including governance posture.
func LogProfileInfo(profileName ProfileName, logFn func(msg string, fields map[string]interface{})) {
	p, err := GetProfile(profileName)
	if err != nil {
		return
	}

	fields := map[string]interface{}{
		"profile": string(profileName),
	}
	if p.CardinalityMode != nil {
		fields["cardinality_mode"] = *p.CardinalityMode
	}
	if p.LimitsDryRun != nil {
		if *p.LimitsDryRun {
			fields["limits"] = "log/dry-run"
		} else {
			fields["limits"] = "adaptive"
		}
	}
	if p.BufferFullPolicy != nil {
		fields["buffer_policy"] = *p.BufferFullPolicy
	}
	if p.MemoryLimitRatio != nil {
		fields["memory_ratio"] = fmt.Sprintf("%.2f", *p.MemoryLimitRatio)
	}
	if p.ReceiverMaxRequestBodySize != nil {
		fields["request_limit"] = FormatByteSize(*p.ReceiverMaxRequestBodySize)
	}
	if p.BloomPersistenceEnabled != nil && *p.BloomPersistenceEnabled {
		fields["bloom_persistence"] = "on"
	}

	logFn(fmt.Sprintf("profile=%s governance settings applied", profileName), fields)

	// Log prerequisites
	prereqs := p.Prerequisites()
	for _, pr := range prereqs {
		logFn(fmt.Sprintf("profile prerequisite: %s", pr.Description), map[string]interface{}{
			"type":     pr.Type,
			"severity": pr.Severity,
		})
	}
}
