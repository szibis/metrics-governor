package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/KimMachineGun/automemlimit/memlimit"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/szibis/metrics-governor/internal/buffer"
	"github.com/szibis/metrics-governor/internal/cardinality"
	"github.com/szibis/metrics-governor/internal/config"
	"github.com/szibis/metrics-governor/internal/exporter"
	"github.com/szibis/metrics-governor/internal/health"
	"github.com/szibis/metrics-governor/internal/limits"
	"github.com/szibis/metrics-governor/internal/logging"
	"github.com/szibis/metrics-governor/internal/prw"
	"github.com/szibis/metrics-governor/internal/queue"
	"github.com/szibis/metrics-governor/internal/receiver"
	"github.com/szibis/metrics-governor/internal/relabel"
	"github.com/szibis/metrics-governor/internal/sampling"
	"github.com/szibis/metrics-governor/internal/stats"
	"github.com/szibis/metrics-governor/internal/telemetry"
	"github.com/szibis/metrics-governor/internal/tenant"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// metricsRecorder is a minimal http.ResponseWriter that writes to an io.Writer
type metricsRecorder struct {
	io.Writer
}

func (m *metricsRecorder) Header() http.Header {
	return http.Header{}
}

func (m *metricsRecorder) WriteHeader(statusCode int) {
	// Ignore status codes since we're just collecting metrics text
}

func main() {
	// Handle "validate" subcommand before flag parsing
	if len(os.Args) >= 2 && os.Args[1] == "validate" {
		runValidate(os.Args[2:])
		return
	}

	cfg := config.ParseFlags()

	// Handle early-exit introspection flags (--show-profile, --show-deprecations, --help, --version)
	if config.HandleEarlyExits(cfg) {
		os.Exit(0)
	}

	// Collect explicitly-set CLI flags for profile/derivation precedence
	explicitFields := config.ExplicitFlags()

	// Apply configuration profile (balanced is default)
	// Profile values only fill fields NOT explicitly set by the user
	if cfg.Profile != "" {
		if err := config.ApplyProfile(cfg, config.ProfileName(cfg.Profile), explicitFields); err != nil {
			fmt.Fprintf(os.Stderr, "Error applying profile: %v\n", err)
			os.Exit(1)
		}
	}

	// Re-apply CLI overrides after profile (CLI always wins)
	config.ReapplyFlagOverrides(cfg)

	// Expand consolidated params (parallelism → workers, timeout → cascade, etc.)
	consolidatedDerived := config.ExpandConsolidatedParams(cfg, explicitFields)

	// Auto-derive resource-based values (CPU count, memory limits)
	resources := config.DetectResources()
	autoDerived := config.AutoDerive(cfg, resources, explicitFields)
	allDerived := append(consolidatedDerived, autoDerived...)

	// Check deprecation warnings
	deprecationRegistry := config.NewDeprecationRegistry(config.GetVersion())
	for _, entry := range config.DefaultDeprecations() {
		deprecationRegistry.Register(entry)
	}
	deprecationWarnings := deprecationRegistry.CheckAndWarn(explicitFields)

	// Handle --show-effective-config (after full pipeline)
	if cfg.ShowEffectiveConfig {
		result := &config.BuildResult{
			Config:         cfg,
			ExplicitFields: explicitFields,
			ProfileApplied: config.ProfileName(cfg.Profile),
			Derivations:    allDerived,
			Deprecations:   deprecationWarnings,
		}
		fmt.Println(config.DumpEffectiveConfig(result))
		os.Exit(0)
	}

	// Handle --strict-deprecations
	if cfg.StrictDeprecations {
		if err := config.CheckStrictDeprecations(deprecationWarnings); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}

	// Re-validate after profile + derivation
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Auto-detect and set GOMEMLIMIT based on container memory limits (cgroups)
	// Uses configurable ratio (default 90%) to leave headroom for other processes
	initMemoryLimit(cfg.MemoryLimitRatio)

	// Set OTEL-compatible resource attributes for structured logging
	logging.SetResource(map[string]string{
		"service.name":    "metrics-governor",
		"service.version": config.GetVersion(),
	})

	// Set log verbosity (metrics are always emitted regardless of level)
	if cfg.LogLevel != "" {
		logging.SetLevel(logging.ParseLevel(cfg.LogLevel))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize OTLP telemetry export (logs + metrics) if configured
	telHeaders := make(map[string]string)
	if cfg.TelemetryHeaders != "" {
		for _, pair := range strings.Split(cfg.TelemetryHeaders, ",") {
			kv := strings.SplitN(pair, "=", 2)
			if len(kv) == 2 {
				telHeaders[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
			}
		}
	}
	tel, err := telemetry.Init(ctx, telemetry.Config{
		Endpoint:         cfg.TelemetryEndpoint,
		Protocol:         cfg.TelemetryProtocol,
		Insecure:         cfg.TelemetryInsecure,
		Timeout:          cfg.TelemetryTimeout,
		PushInterval:     cfg.TelemetryPushInterval,
		Compression:      cfg.TelemetryCompression,
		Headers:          telHeaders,
		ShutdownTimeout:  cfg.TelemetryShutdownTimeout,
		RetryEnabled:     cfg.TelemetryRetryEnabled,
		RetryInitial:     cfg.TelemetryRetryInitial,
		RetryMaxInterval: cfg.TelemetryRetryMaxInterval,
		RetryMaxElapsed:  cfg.TelemetryRetryMaxElapsed,
	}, "metrics-governor", config.GetVersion())
	if err != nil {
		logging.Error("failed to initialize telemetry", logging.F("error", err.Error()))
	}
	if tel != nil {
		defer func() {
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), tel.ShutdownTimeout())
			defer shutdownCancel()
			if err := tel.Shutdown(shutdownCtx); err != nil {
				logging.Error("telemetry shutdown error", logging.F("error", err.Error()))
			}
		}()
		// Register OTLP log hook so all log entries are also exported via OTLP
		logging.SetHook(tel.NewLogHook())
		logging.Info("OTLP telemetry enabled", logging.F(
			"endpoint", cfg.TelemetryEndpoint,
			"protocol", cfg.TelemetryProtocol,
		))
	}

	// Initialize cardinality tracking configuration
	cardinalityCfg := cfg.CardinalityConfig()
	cardinality.GlobalConfig = cardinality.Config{
		Mode:              cardinality.ParseMode(cardinalityCfg.Mode),
		ExpectedItems:     cardinalityCfg.ExpectedItems,
		FalsePositiveRate: cardinalityCfg.FPRate,
		HLLThreshold:      cardinalityCfg.HLLThreshold,
	}
	logFields := logging.F(
		"mode", cardinalityCfg.Mode,
		"expected_items", cardinalityCfg.ExpectedItems,
		"fp_rate", cardinalityCfg.FPRate,
	)
	if cardinalityCfg.Mode == "hybrid" {
		logFields["hll_threshold"] = cardinalityCfg.HLLThreshold
	}
	logging.Info("cardinality tracking initialized", logFields)

	// Initialize bloom filter persistence if enabled
	bloomPersistenceCfg := cfg.BloomPersistenceConfig()
	if bloomPersistenceCfg.Enabled {
		persistCfg := cardinality.PersistenceConfig{
			Enabled:          bloomPersistenceCfg.Enabled,
			Path:             bloomPersistenceCfg.Path,
			SaveInterval:     bloomPersistenceCfg.SaveInterval,
			StateTTL:         bloomPersistenceCfg.StateTTL,
			CleanupInterval:  bloomPersistenceCfg.CleanupInterval,
			MaxSize:          bloomPersistenceCfg.MaxSize,
			MaxMemory:        bloomPersistenceCfg.MaxMemory,
			Compression:      bloomPersistenceCfg.Compression,
			CompressionLevel: bloomPersistenceCfg.CompressionLevel,
		}
		store, err := cardinality.NewTrackerStore(persistCfg, cardinality.GlobalConfig)
		if err != nil {
			logging.Warn("bloom persistence disabled due to initialization error", logging.F("error", err.Error()))
		} else {
			// Load existing state from disk
			if err := store.LoadAll(); err != nil {
				logging.Warn("failed to load bloom state from disk", logging.F("error", err.Error()))
			}
			// Start background save/cleanup loops
			store.Start()
			cardinality.GlobalTrackerStore = store
		}
	}

	// Create exporter (either sharded or single endpoint)
	var finalExporter exporter.Exporter

	if cfg.ShardingEnabled {
		// Sharding mode: ShardedExporter manages multiple endpoints
		shardingCfg := cfg.ShardingConfig()
		shardedCfg := exporter.ShardedExporterConfig{
			BaseConfig:         cfg.ExporterConfig(),
			HeadlessService:    shardingCfg.HeadlessService,
			DNSRefreshInterval: shardingCfg.DNSRefreshInterval,
			DNSTimeout:         shardingCfg.DNSTimeout,
			Labels:             shardingCfg.Labels,
			VirtualNodes:       shardingCfg.VirtualNodes,
			FallbackOnEmpty:    shardingCfg.FallbackOnEmpty,
			QueueEnabled:       cfg.QueueEnabled,
			QueueConfig: queue.Config{
				Path:                       cfg.QueuePath,
				MaxSize:                    cfg.QueueMaxSize,
				MaxBytes:                   cfg.QueueMaxBytes,
				RetryInterval:              cfg.QueueRetryInterval,
				MaxRetryDelay:              cfg.QueueMaxRetryDelay,
				FullBehavior:               queue.FullBehavior(cfg.QueueFullBehavior),
				TargetUtilization:          cfg.QueueTargetUtilization,
				AdaptiveEnabled:            cfg.QueueAdaptiveEnabled,
				CompactThreshold:           cfg.QueueCompactThreshold,
				BackoffEnabled:             cfg.QueueBackoffEnabled,
				BackoffMultiplier:          cfg.QueueBackoffMultiplier,
				CircuitBreakerEnabled:      cfg.QueueCircuitBreakerEnabled,
				CircuitBreakerThreshold:    cfg.QueueCircuitBreakerThreshold,
				CircuitBreakerResetTimeout: cfg.QueueCircuitBreakerResetTimeout,
				DirectExportTimeout:        cfg.QueueDirectExportTimeout,
				InmemoryBlocks:             cfg.QueueInmemoryBlocks,
				ChunkSize:                  cfg.QueueChunkSize,
				MetaSyncInterval:           cfg.QueueMetaSyncInterval,
				StaleFlushInterval:         cfg.QueueStaleFlushInterval,
				WriteBufferSize:            cfg.QueueWriteBufferSize,
				Compression:                cfg.QueueCompression,
				BatchDrainSize:             cfg.QueueBatchDrainSize,
				BurstDrainSize:             cfg.QueueBurstDrainSize,
				RetryExportTimeout:         cfg.QueueRetryTimeout,
				CloseTimeout:               cfg.QueueCloseTimeout,
				DrainTimeout:               cfg.QueueDrainTimeout,
				DrainEntryTimeout:          cfg.QueueDrainEntryTimeout,
				AlwaysQueue:                cfg.QueueAlwaysQueue,
				Workers:                    cfg.QueueWorkers,
			},
		}

		shardedExp, err := exporter.NewSharded(ctx, shardedCfg)
		if err != nil {
			logging.Fatal("failed to create sharded exporter", logging.F("error", err.Error()))
		}
		finalExporter = shardedExp

		logging.Info("sharded exporter initialized", logging.F(
			"headless_service", shardingCfg.HeadlessService,
			"labels", cfg.ShardingLabels,
			"virtual_nodes", shardingCfg.VirtualNodes,
			"queue_enabled", cfg.QueueEnabled,
		))
	} else {
		// Single endpoint mode
		exp, err := exporter.New(ctx, cfg.ExporterConfig())
		if err != nil {
			logging.Fatal("failed to create exporter", logging.F("error", err.Error()))
		}
		finalExporter = exp

		// Wrap with queued exporter if enabled
		if cfg.QueueEnabled {
			queueCfg := queue.Config{
				Path:                       cfg.QueuePath,
				MaxSize:                    cfg.QueueMaxSize,
				MaxBytes:                   cfg.QueueMaxBytes,
				RetryInterval:              cfg.QueueRetryInterval,
				MaxRetryDelay:              cfg.QueueMaxRetryDelay,
				FullBehavior:               queue.FullBehavior(cfg.QueueFullBehavior),
				TargetUtilization:          cfg.QueueTargetUtilization,
				AdaptiveEnabled:            cfg.QueueAdaptiveEnabled,
				CompactThreshold:           cfg.QueueCompactThreshold,
				BackoffEnabled:             cfg.QueueBackoffEnabled,
				BackoffMultiplier:          cfg.QueueBackoffMultiplier,
				CircuitBreakerEnabled:      cfg.QueueCircuitBreakerEnabled,
				CircuitBreakerThreshold:    cfg.QueueCircuitBreakerThreshold,
				CircuitBreakerResetTimeout: cfg.QueueCircuitBreakerResetTimeout,
				DirectExportTimeout:        cfg.QueueDirectExportTimeout,
				InmemoryBlocks:             cfg.QueueInmemoryBlocks,
				ChunkSize:                  cfg.QueueChunkSize,
				MetaSyncInterval:           cfg.QueueMetaSyncInterval,
				StaleFlushInterval:         cfg.QueueStaleFlushInterval,
				WriteBufferSize:            cfg.QueueWriteBufferSize,
				Compression:                cfg.QueueCompression,
				BatchDrainSize:             cfg.QueueBatchDrainSize,
				BurstDrainSize:             cfg.QueueBurstDrainSize,
				RetryExportTimeout:         cfg.QueueRetryTimeout,
				CloseTimeout:               cfg.QueueCloseTimeout,
				DrainTimeout:               cfg.QueueDrainTimeout,
				DrainEntryTimeout:          cfg.QueueDrainEntryTimeout,
				AlwaysQueue:                cfg.QueueAlwaysQueue,
				Workers:                    cfg.QueueWorkers,
				PipelineSplitEnabled:       cfg.QueuePipelineSplitEnabled,
				PreparerCount:              cfg.QueuePreparerCount,
				SenderCount:                cfg.QueueSenderCount,
				PipelineChannelSize:        cfg.QueuePipelineChannelSize,
				MaxConcurrentSends:         cfg.QueueMaxConcurrentSends,
				GlobalSendLimit:            cfg.QueueGlobalSendLimit,
				AdaptiveWorkersEnabled:     cfg.QueueAdaptiveWorkersEnabled,
				MinWorkers:                 cfg.QueueMinWorkers,
				MaxWorkers:                 cfg.QueueMaxWorkers,
			}

			queuedExp, queueErr := exporter.NewQueued(exp, queueCfg)
			if queueErr != nil {
				logging.Fatal("failed to create queued exporter", logging.F("error", queueErr.Error()))
			}
			finalExporter = queuedExp
			logging.Info("queue enabled", logging.F(
				"path", cfg.QueuePath,
				"max_size", cfg.QueueMaxSize,
				"max_bytes", cfg.QueueMaxBytes,
				"retry_interval", cfg.QueueRetryInterval,
				"full_behavior", cfg.QueueFullBehavior,
			))
		}
	}
	defer finalExporter.Close()

	// Parse stats labels
	var trackLabels []string
	if cfg.StatsLabels != "" {
		trackLabels = strings.Split(cfg.StatsLabels, ",")
		for i, l := range trackLabels {
			trackLabels[i] = strings.TrimSpace(l)
		}
	}

	// Create stats collector
	statsCollector := stats.NewCollector(trackLabels)

	// Create runtime stats collector
	runtimeStats := stats.NewRuntimeStats()

	// Create limits enforcer (if configured)
	// Use interface type to avoid typed nil issue with interface comparison
	var limitsEnforcer interface {
		Process([]*metricspb.ResourceMetrics) []*metricspb.ResourceMetrics
		ServeHTTP(http.ResponseWriter, *http.Request)
		ReloadConfig(cfg *limits.Config)
		SetDryRun(dryRun bool)
		DryRun() bool
		SetStatsThreshold(int64)
		Stop()
	}
	if cfg.LimitsConfig != "" {
		limitsCfg, err := limits.LoadConfig(cfg.LimitsConfig)
		if err != nil {
			logging.Fatal("failed to load limits config", logging.F("error", err.Error(), "path", cfg.LimitsConfig))
		}
		limitsEnforcer = limits.NewEnforcer(limitsCfg, cfg.LimitsDryRun, cfg.RuleCacheMaxSize)
		if cfg.LimitsStatsThreshold > 0 {
			limitsEnforcer.SetStatsThreshold(cfg.LimitsStatsThreshold)
		}
		logging.Info("limits enforcer initialized", logging.F(
			"config", cfg.LimitsConfig,
			"dry_run", cfg.LimitsDryRun,
			"rules_count", len(limitsCfg.Rules),
			"stats_threshold", cfg.LimitsStatsThreshold,
		))
	}

	// Create relabeler (if configured)
	var relabeler *relabel.Relabeler
	if cfg.RelabelConfig != "" {
		relabelConfigs, err := relabel.LoadFile(cfg.RelabelConfig)
		if err != nil {
			logging.Fatal("failed to load relabel config", logging.F("error", err.Error(), "path", cfg.RelabelConfig))
		}
		relabeler, err = relabel.New(relabelConfigs)
		if err != nil {
			logging.Fatal("failed to create relabeler", logging.F("error", err.Error()))
		}
		logging.Info("relabeler initialized", logging.F(
			"config", cfg.RelabelConfig,
			"rules_count", len(relabelConfigs),
		))
	}

	// Create sampler/processor (if configured)
	// Processing config takes precedence; --sampling-config is a deprecated alias.
	var sampler *sampling.Sampler
	processingConfigPath := cfg.ProcessingConfig
	if processingConfigPath == "" && cfg.SamplingConfig != "" {
		processingConfigPath = cfg.SamplingConfig
		logging.Warn("--sampling-config is deprecated, use --processing-config instead", logging.F("path", cfg.SamplingConfig))
	}
	if processingConfigPath != "" {
		procCfg, err := sampling.LoadProcessingFile(processingConfigPath)
		if err != nil {
			logging.Fatal("failed to load processing config", logging.F("error", err.Error(), "path", processingConfigPath))
		}
		sampler, err = sampling.NewFromProcessing(procCfg)
		if err != nil {
			logging.Fatal("failed to create processor", logging.F("error", err.Error()))
		}
		logging.Info("processing engine initialized", logging.F(
			"config", processingConfigPath,
			"rules_count", sampler.ProcessingRuleCount(),
			"has_aggregation", sampler.HasAggregation(),
		))
	}

	// Create tenant pipeline (if tenancy enabled)
	var tenantPipeline *tenant.Pipeline
	if cfg.TenancyEnabled {
		tenancyCfg := tenant.Config{
			Enabled:          true,
			Mode:             tenant.DetectionMode(cfg.TenancyMode),
			HeaderName:       cfg.TenancyHeaderName,
			LabelName:        cfg.TenancyLabelName,
			AttributeKey:     cfg.TenancyAttributeKey,
			DefaultTenant:    cfg.TenancyDefaultTenant,
			InjectLabel:      cfg.TenancyInjectLabel,
			InjectLabelName:  cfg.TenancyInjectLabelName,
			StripSourceLabel: cfg.TenancyStripSource,
		}
		detector, err := tenant.NewDetector(tenancyCfg)
		if err != nil {
			logging.Fatal("failed to create tenant detector", logging.F("error", err.Error()))
		}

		var quotaEnforcer *tenant.QuotaEnforcer
		if cfg.TenancyConfigFile != "" {
			quotasCfg, err := tenant.LoadQuotasConfig(cfg.TenancyConfigFile)
			if err != nil {
				logging.Fatal("failed to load tenant quotas config", logging.F("error", err.Error(), "path", cfg.TenancyConfigFile))
			}
			quotaEnforcer = tenant.NewQuotaEnforcer(quotasCfg)
			logging.Info("tenant quota enforcer initialized", logging.F("path", cfg.TenancyConfigFile))
		}

		tenantPipeline = tenant.NewPipeline(detector, quotaEnforcer)
		logging.Info("tenancy enabled", logging.F(
			"mode", cfg.TenancyMode,
			"default_tenant", cfg.TenancyDefaultTenant,
			"inject_label", cfg.TenancyInjectLabel,
			"quotas_enabled", cfg.TenancyConfigFile != "",
		))
	}

	// Derive percentage-based memory sizing from GOMEMLIMIT.
	// Uses config.DeriveMemorySizing which handles the math.MaxInt64 edge case
	// (GOMEMLIMIT not set → unbounded buffer prevention).
	rawMemoryLimit := debug.SetMemoryLimit(-1) // Get the limit that was set (does not change it)
	memorySizing := config.DeriveMemorySizing(rawMemoryLimit, cfg.BufferMemoryPercent, cfg.QueueMemoryPercent)
	if memorySizing.BufferMaxBytes > 0 {
		logging.Info("percentage-based buffer sizing", logging.F(
			"memory_limit_bytes", memorySizing.MemoryLimit,
			"buffer_percent", cfg.BufferMemoryPercent,
			"buffer_max_bytes", memorySizing.BufferMaxBytes,
		))
	}

	// Create log aggregator for buffer (aggregates logs per 10s interval)
	bufferLogAggregator := limits.NewLogAggregator(10 * time.Second)

	// Build buffer options
	var bufOpts []buffer.BufferOption
	if cfg.MaxBatchBytes > 0 {
		bufOpts = append(bufOpts, buffer.WithMaxBatchBytes(cfg.MaxBatchBytes))
	}
	if cfg.FlushTimeout > 0 {
		bufOpts = append(bufOpts, buffer.WithFlushTimeout(cfg.FlushTimeout))
	}
	// Map buffer full policy string to queue.FullBehavior
	switch cfg.BufferFullPolicy {
	case "reject":
		bufOpts = append(bufOpts, buffer.WithBufferFullPolicy(queue.DropNewest))
	case "drop_oldest":
		bufOpts = append(bufOpts, buffer.WithBufferFullPolicy(queue.DropOldest))
	case "block":
		bufOpts = append(bufOpts, buffer.WithBufferFullPolicy(queue.Block))
	}
	// Apply derived or explicit buffer byte capacity
	if memorySizing.BufferMaxBytes > 0 {
		bufOpts = append(bufOpts, buffer.WithMaxBufferBytes(memorySizing.BufferMaxBytes))
	}

	// Apply derived queue byte capacity from GOMEMLIMIT percentage
	if memorySizing.QueueMaxBytes > 0 {
		cfg.QueueMaxBytes = memorySizing.QueueMaxBytes
		cfg.PRWQueueMaxBytes = memorySizing.QueueMaxBytes
		logging.Info("percentage-based queue sizing", logging.F(
			"memory_limit_bytes", memorySizing.MemoryLimit,
			"queue_percent", cfg.QueueMemoryPercent,
			"queue_max_bytes", memorySizing.QueueMaxBytes,
		))
	}

	// Set up tenant processor in buffer (if tenancy enabled)
	if tenantPipeline != nil {
		bufOpts = append(bufOpts, buffer.WithTenantProcessor(tenantPipeline))
	}

	// Set up failover queue (safety net for export failures)
	if cfg.QueueEnabled && cfg.QueueType == "memory" {
		failoverQ := buffer.NewMemoryQueue(cfg.QueueMaxSize, cfg.QueueMaxBytes)
		bufOpts = append(bufOpts, buffer.WithFailoverQueue(failoverQ))
		logging.Info("memory failover queue enabled", logging.F(
			"max_size", cfg.QueueMaxSize,
			"max_bytes", cfg.QueueMaxBytes,
		))
	}

	// Wire relabeler into export pipeline
	if relabeler != nil {
		bufOpts = append(bufOpts, buffer.WithRelabeler(relabeler))
	}

	// Wire processor/sampler into buffer pipeline (processing before limits)
	if sampler != nil {
		bufOpts = append(bufOpts, buffer.WithProcessor(sampler))
	}

	// Wire batch auto-tuner (AIMD) into buffer and queued exporter
	if cfg.BufferBatchAutoTuneEnabled {
		bt := exporter.NewBatchTuner(exporter.BatchTunerConfig{
			Enabled:       true,
			MinBytes:      cfg.BufferBatchMinBytes,
			MaxBytes:      cfg.BufferBatchMaxBytes,
			SuccessStreak: cfg.BufferBatchSuccessStreak,
			GrowFactor:    cfg.BufferBatchGrowFactor,
			ShrinkFactor:  cfg.BufferBatchShrinkFactor,
		})
		bufOpts = append(bufOpts, buffer.WithBatchTuner(bt))

		// Wire tuner into queued exporter for success/failure feedback
		if qe, ok := finalExporter.(*exporter.QueuedExporter); ok {
			qe.SetBatchTuner(bt)
		}
	}

	// Create buffer with stats collector, limits enforcer, and log aggregator
	buf := buffer.New(cfg.BufferSize, cfg.MaxBatchSize, cfg.FlushInterval, finalExporter, statsCollector, limitsEnforcer, bufferLogAggregator, bufOpts...)

	// Wire aggregate output into buffer (bypasses stats+processing to avoid loops)
	if sampler != nil && sampler.HasAggregation() {
		sampler.SetAggregateOutput(buf.AddAggregated)
		sampler.StartAggregation(ctx)
		logging.Info("aggregate engine started")
	}

	// Start buffer flush routine
	go buf.Start(ctx)

	// Create and start gRPC receiver with full configuration
	grpcReceiver := receiver.NewGRPCWithConfig(cfg.GRPCReceiverConfig(), buf)
	go func() {
		if err := grpcReceiver.Start(); err != nil {
			logging.Error("gRPC receiver error", logging.F("error", err.Error()))
		}
	}()

	// Create and start HTTP receiver with full configuration
	httpReceiver := receiver.NewHTTPWithConfig(cfg.HTTPReceiverConfig(), buf)
	go func() {
		if err := httpReceiver.Start(); err != nil {
			logging.Error("HTTP receiver error", logging.F("error", err.Error()))
		}
	}()

	// Create health checker for liveness/readiness probes
	healthChecker := health.New()

	// Start stats HTTP server with combined metrics + health endpoints
	statsMux := http.NewServeMux()
	statsMux.HandleFunc("/live", healthChecker.LiveHandler())
	statsMux.HandleFunc("/ready", healthChecker.ReadyHandler())
	statsMux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		// Buffer all metrics first to support proper compression
		var buf bytes.Buffer

		// Create a response recorder to capture all metrics
		recorder := &metricsRecorder{Writer: &buf}

		// Write stats metrics
		statsCollector.ServeHTTP(recorder, r)
		// Write limits metrics (if enabled)
		if limitsEnforcer != nil {
			limitsEnforcer.ServeHTTP(recorder, r)
		}
		// Write runtime metrics (goroutines, memory, GC, PSI)
		runtimeStats.ServeHTTP(recorder, r)
		// Write Prometheus registry metrics (queue, sharding, etc.)
		promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{
			DisableCompression: true,
		}).ServeHTTP(recorder, r)

		// Check if client accepts gzip
		acceptEncoding := r.Header.Get("Accept-Encoding")
		if strings.Contains(acceptEncoding, "gzip") {
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
			gz := gzip.NewWriter(w)
			defer gz.Close()
			_, _ = io.Copy(gz, &buf)
		} else {
			w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
			_, _ = io.Copy(w, &buf)
		}
	})

	// Register pprof endpoints (disabled by default, for debugging only)
	if cfg.PprofEnabled {
		statsMux.HandleFunc("/debug/pprof/", pprof.Index)
		statsMux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		statsMux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		statsMux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		statsMux.HandleFunc("/debug/pprof/trace", pprof.Trace)
		logging.Warn("pprof endpoints enabled at /debug/pprof/ — do not expose in production without auth")
	}

	statsServer := &http.Server{
		Addr:    cfg.StatsAddr,
		Handler: statsMux,
	}
	go func() {
		logging.Info("stats endpoint started", logging.F("addr", cfg.StatsAddr, "path", "/metrics"))
		if err := statsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logging.Error("stats server error", logging.F("error", err.Error()))
		}
	}()

	// Start periodic stats logging (every 30 seconds)
	go statsCollector.StartPeriodicLogging(ctx, 30*time.Second)

	// PRW Pipeline (separate from OTLP)
	var prwReceiver *receiver.PRWReceiver
	var prwBuffer *prw.Buffer
	var prwExporter prw.PRWExporter
	if cfg.PRWListenAddr != "" {
		logging.Info("initializing PRW pipeline", logging.F(
			"listen_addr", cfg.PRWListenAddr,
			"exporter_endpoint", cfg.PRWExporterEndpoint,
		))

		// Create PRW exporter if configured
		if cfg.PRWExporterEndpoint != "" {
			prwExp, err := exporter.NewPRW(ctx, cfg.PRWExporterConfig())
			if err != nil {
				logging.Fatal("failed to create PRW exporter", logging.F("error", err.Error()))
			}

			// Wrap with queued exporter if enabled
			if cfg.PRWQueueEnabled {
				queuedPRWExp, queueErr := exporter.NewPRWQueued(prwExp, cfg.PRWQueueConfig())
				if queueErr != nil {
					logging.Fatal("failed to create PRW queued exporter", logging.F("error", queueErr.Error()))
				}
				prwExporter = queuedPRWExp
				logging.Info("PRW queue enabled", logging.F(
					"path", cfg.PRWQueuePath,
					"max_size", cfg.PRWQueueMaxSize,
				))
			} else {
				prwExporter = prwExp
			}
		}

		// Create PRW log aggregator
		prwLogAggregator := limits.NewLogAggregator(10 * time.Second)

		// Create PRW buffer
		prwBuffer = prw.NewBuffer(cfg.PRWBufferConfig(), prwExporter, nil, nil, prwLogAggregator)

		// Start PRW buffer flush routine
		go prwBuffer.Start(ctx)

		// Create and start PRW receiver
		prwReceiver = receiver.NewPRWWithConfig(cfg.PRWReceiverConfig(), prwBuffer)
		go func() {
			if err := prwReceiver.Start(); err != nil && err != http.ErrServerClosed {
				logging.Error("PRW receiver error", logging.F("error", err.Error()))
			}
		}()

		logging.Info("PRW pipeline started", logging.F(
			"receiver_addr", cfg.PRWListenAddr,
			"exporter_endpoint", cfg.PRWExporterEndpoint,
			"vm_mode", cfg.PRWExporterVMMode,
		))
	}

	// Register readiness checks for pipeline components
	healthChecker.RegisterReadiness("grpc_receiver", grpcReceiver.HealthCheck)
	healthChecker.RegisterReadiness("http_receiver", httpReceiver.HealthCheck)
	if prwReceiver != nil {
		healthChecker.RegisterReadiness("prw_receiver", prwReceiver.HealthCheck)
	}

	logging.Info("metrics-governor started", logging.F(
		"grpc_addr", cfg.GRPCListenAddr,
		"http_addr", cfg.HTTPListenAddr,
		"exporter_endpoint", cfg.ExporterEndpoint,
		"exporter_protocol", cfg.ExporterProtocol,
		"stats_addr", cfg.StatsAddr,
		"receiver_tls", cfg.ReceiverTLSEnabled,
		"receiver_auth", cfg.ReceiverAuthEnabled,
		"limits_enabled", cfg.LimitsConfig != "",
		"relabel_enabled", cfg.RelabelConfig != "",
		"processing_enabled", processingConfigPath != "",
		"queue_enabled", cfg.QueueEnabled,
		"sharding_enabled", cfg.ShardingEnabled,
		"prw_enabled", cfg.PRWListenAddr != "",
		"tenancy_enabled", cfg.TenancyEnabled,
	))

	// Set up SIGHUP handler for config reload
	// Reloads: limits rules, limits dry-run toggle, relabel rules, sampling rules
	// Does NOT reload: receiver, exporter, buffer, queue, sharding settings (require restart)
	{
		sighupChan := make(chan os.Signal, 1)
		signal.Notify(sighupChan, syscall.SIGHUP)
		go func() {
			for {
				select {
				case <-ctx.Done():
					signal.Stop(sighupChan)
					return
				case _, ok := <-sighupChan:
					if !ok {
						return
					}
				}
				logging.Info("received SIGHUP, reloading configuration")

				// Reload limits config (rules file)
				if cfg.LimitsConfig != "" && limitsEnforcer != nil {
					logging.Info("reloading limits config", logging.F("path", cfg.LimitsConfig))
					newLimitsCfg, err := limits.LoadConfig(cfg.LimitsConfig)
					if err != nil {
						logging.Error("limits config reload failed, keeping current config", logging.F("error", err.Error(), "path", cfg.LimitsConfig))
					} else {
						limitsEnforcer.ReloadConfig(newLimitsCfg)
						logging.Info("limits config reloaded successfully", logging.F("rules_count", len(newLimitsCfg.Rules)))
					}
				}

				// Reload relabel config
				if cfg.RelabelConfig != "" && relabeler != nil {
					logging.Info("reloading relabel config", logging.F("path", cfg.RelabelConfig))
					newRelabelConfigs, err := relabel.LoadFile(cfg.RelabelConfig)
					if err != nil {
						logging.Error("relabel config reload failed, keeping current config", logging.F("error", err.Error(), "path", cfg.RelabelConfig))
					} else {
						if err := relabeler.ReloadConfig(newRelabelConfigs); err != nil {
							logging.Error("relabel config apply failed, keeping current config", logging.F("error", err.Error()))
						} else {
							logging.Info("relabel config reloaded successfully", logging.F("rules_count", len(newRelabelConfigs)))
						}
					}
				}

				// Reload processing config
				if processingConfigPath != "" && sampler != nil {
					logging.Info("reloading processing config", logging.F("path", processingConfigPath))
					newProcCfg, err := sampling.LoadProcessingFile(processingConfigPath)
					if err != nil {
						logging.Error("processing config reload failed, keeping current config", logging.F("error", err.Error(), "path", processingConfigPath))
					} else {
						if err := sampler.ReloadProcessingConfig(newProcCfg); err != nil {
							logging.Error("processing config apply failed, keeping current config", logging.F("error", err.Error()))
						} else {
							logging.Info("processing config reloaded successfully", logging.F("rules_count", sampler.ProcessingRuleCount()))
							// Restart aggregation if needed.
							if sampler.HasAggregation() {
								sampler.StartAggregation(ctx)
							}
						}
					}
				}

				// Reload main config (for hot-reloadable settings like dry_run)
				if cfg.ConfigFile != "" {
					logging.Info("reloading main config", logging.F("path", cfg.ConfigFile))
					yamlCfg, err := config.LoadYAML(cfg.ConfigFile)
					if err != nil {
						logging.Error("main config reload failed, keeping current settings", logging.F("error", err.Error(), "path", cfg.ConfigFile))
					} else {
						newMainCfg := yamlCfg.ToConfig()

						// Apply hot-reloadable settings
						if limitsEnforcer != nil {
							oldDryRun := limitsEnforcer.DryRun()
							newDryRun := newMainCfg.LimitsDryRun
							if oldDryRun != newDryRun {
								limitsEnforcer.SetDryRun(newDryRun)
								logging.Info("limits dry-run mode changed", logging.F("old", oldDryRun, "new", newDryRun))
							}
						}

						logging.Info("main config reloaded (hot-reloadable settings applied)")
					}
				}

				// Reload tenant quotas config
				if cfg.TenancyConfigFile != "" && tenantPipeline != nil {
					logging.Info("reloading tenant quotas config", logging.F("path", cfg.TenancyConfigFile))
					newQuotasCfg, err := tenant.LoadQuotasConfig(cfg.TenancyConfigFile)
					if err != nil {
						logging.Error("tenant quotas reload failed, keeping current config", logging.F("error", err.Error(), "path", cfg.TenancyConfigFile))
					} else {
						tenantPipeline.SetQuotaEnforcer(tenant.NewQuotaEnforcer(newQuotasCfg))
						logging.Info("tenant quotas config reloaded successfully")
					}
				}

				logging.Info("SIGHUP reload complete")
			}
		}()
	}

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigChan

	logging.Info("shutting down", logging.F("signal", sig.String(), "timeout", cfg.ShutdownTimeout.String()))

	// Mark as shutting down so health probes return 503 immediately
	healthChecker.SetShuttingDown()

	// Create shutdown context with configurable timeout
	// Note: K8s terminationGracePeriodSeconds should be > this timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer shutdownCancel()

	// Graceful shutdown - stop receivers first (stop accepting new data)
	grpcReceiver.Stop()
	if err := httpReceiver.Stop(shutdownCtx); err != nil {
		logging.Warn("HTTP receiver shutdown error", logging.F("error", err.Error()))
	}
	if prwReceiver != nil {
		if err := prwReceiver.Stop(shutdownCtx); err != nil {
			logging.Warn("PRW receiver shutdown error", logging.F("error", err.Error()))
		}
	}
	if err := statsServer.Shutdown(shutdownCtx); err != nil {
		logging.Warn("stats server shutdown error", logging.F("error", err.Error()))
	}
	// Stop aggregate engine (flushes final windows)
	if sampler != nil {
		sampler.StopAggregation()
	}

	cancel()

	// Wait for buffers to drain (uses shutdown timeout)
	bufDone := make(chan struct{})
	go func() {
		buf.Wait()
		if prwBuffer != nil {
			prwBuffer.Wait()
		}
		close(bufDone)
	}()

	select {
	case <-bufDone:
		logging.Info("buffers drained successfully")
	case <-shutdownCtx.Done():
		logging.Warn("buffer drain timeout, some data may be lost")
	}

	// Close PRW exporter
	if prwExporter != nil {
		prwExporter.Close()
	}

	// Stop log aggregators (flushes remaining entries)
	bufferLogAggregator.Stop()
	if limitsEnforcer != nil {
		limitsEnforcer.Stop()
	}

	// Close bloom persistence (final save and cleanup)
	if cardinality.GlobalTrackerStore != nil {
		if err := cardinality.GlobalTrackerStore.Close(); err != nil {
			logging.Warn("error closing bloom persistence", logging.F("error", err.Error()))
		}
	}

	logging.Info("shutdown complete")
}

// initMemoryLimit auto-detects container memory limits and sets GOMEMLIMIT.
// This helps prevent OOM kills by making the Go GC more aggressive as memory
// approaches the limit.
func runValidate(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: metrics-governor validate <config-file> [<config-file>...]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Validates YAML configuration files and reports errors/warnings.")
		fmt.Fprintln(os.Stderr, "Exit code 0 if all files are valid, 1 if any have errors.")
		os.Exit(1)
	}

	allValid := true
	for _, path := range args {
		result := config.ValidateFile(path)
		fmt.Println(result.JSON())
		if !result.Valid {
			allValid = false
		}
	}

	if !allValid {
		os.Exit(1)
	}
}

func initMemoryLimit(ratio float64) {
	// Check if GOMEMLIMIT is already set via environment
	if os.Getenv("GOMEMLIMIT") != "" {
		// User explicitly set GOMEMLIMIT, respect their choice
		limit := debug.SetMemoryLimit(-1) // Get current limit without changing
		logging.Info("using explicit GOMEMLIMIT from environment", logging.F(
			"limit_bytes", limit,
			"limit_mb", limit/(1024*1024),
		))
		autoSetGOGC()
		return
	}

	// Validate and apply defaults for ratio
	if ratio <= 0 || ratio > 1.0 {
		ratio = 0.85 // Default to 85%
	}

	// Auto-detect container memory limit (cgroups v1 and v2)
	limit, err := memlimit.SetGoMemLimitWithOpts(
		memlimit.WithRatio(ratio),
		memlimit.WithProvider(memlimit.ApplyFallback(
			memlimit.FromCgroup,
			memlimit.FromSystem,
		)),
	)

	if err != nil {
		logging.Warn("failed to auto-detect memory limit", logging.F(
			"error", err.Error(),
		))
		return
	}

	if limit == 0 {
		logging.Info("no memory limit detected, GOMEMLIMIT not set")
		return
	}

	logging.Info("auto-detected memory limit and set GOMEMLIMIT", logging.F(
		"limit_bytes", limit,
		"limit_mb", limit/(1024*1024),
		"ratio", ratio,
		"source", "cgroup/system",
	))
	autoSetGOGC()
}

// autoSetGOGC sets GOGC=50 for tighter heap control when not explicitly set.
// GOGC=50 means GC triggers when the live heap grows by 50% (vs 100% default),
// trading slightly more GC CPU for much tighter memory usage. Acceptable for
// an I/O-bound proxy doing minimal computation. Users can override via GOGC env var.
func autoSetGOGC() {
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(50)
		logging.Info("GOGC auto-set to 50 for tighter heap control")
	}
}
