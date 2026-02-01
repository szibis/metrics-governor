package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/szibis/metrics-governor/internal/buffer"
	"github.com/szibis/metrics-governor/internal/config"
	"github.com/szibis/metrics-governor/internal/exporter"
	"github.com/szibis/metrics-governor/internal/limits"
	"github.com/szibis/metrics-governor/internal/logging"
	"github.com/szibis/metrics-governor/internal/prw"
	"github.com/szibis/metrics-governor/internal/queue"
	"github.com/szibis/metrics-governor/internal/receiver"
	"github.com/szibis/metrics-governor/internal/stats"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

func main() {
	cfg := config.ParseFlags()

	if cfg.ShowHelp {
		config.PrintUsage()
		os.Exit(0)
	}

	if cfg.ShowVersion {
		config.PrintVersion()
		os.Exit(0)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
				Path:               cfg.QueuePath,
				MaxSize:            cfg.QueueMaxSize,
				MaxBytes:           cfg.QueueMaxBytes,
				RetryInterval:      cfg.QueueRetryInterval,
				MaxRetryDelay:      cfg.QueueMaxRetryDelay,
				FullBehavior:       queue.FullBehavior(cfg.QueueFullBehavior),
				TargetUtilization:  cfg.QueueTargetUtilization,
				AdaptiveEnabled:    cfg.QueueAdaptiveEnabled,
				CompactThreshold:   cfg.QueueCompactThreshold,
				InmemoryBlocks:     cfg.QueueInmemoryBlocks,
				ChunkSize:          cfg.QueueChunkSize,
				MetaSyncInterval:   cfg.QueueMetaSyncInterval,
				StaleFlushInterval: cfg.QueueStaleFlushInterval,
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
				Path:               cfg.QueuePath,
				MaxSize:            cfg.QueueMaxSize,
				MaxBytes:           cfg.QueueMaxBytes,
				RetryInterval:      cfg.QueueRetryInterval,
				MaxRetryDelay:      cfg.QueueMaxRetryDelay,
				FullBehavior:       queue.FullBehavior(cfg.QueueFullBehavior),
				TargetUtilization:  cfg.QueueTargetUtilization,
				AdaptiveEnabled:    cfg.QueueAdaptiveEnabled,
				CompactThreshold:   cfg.QueueCompactThreshold,
				InmemoryBlocks:     cfg.QueueInmemoryBlocks,
				ChunkSize:          cfg.QueueChunkSize,
				MetaSyncInterval:   cfg.QueueMetaSyncInterval,
				StaleFlushInterval: cfg.QueueStaleFlushInterval,
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
		Stop()
	}
	if cfg.LimitsConfig != "" {
		limitsCfg, err := limits.LoadConfig(cfg.LimitsConfig)
		if err != nil {
			logging.Fatal("failed to load limits config", logging.F("error", err.Error(), "path", cfg.LimitsConfig))
		}
		limitsEnforcer = limits.NewEnforcer(limitsCfg, cfg.LimitsDryRun)
		logging.Info("limits enforcer initialized", logging.F(
			"config", cfg.LimitsConfig,
			"dry_run", cfg.LimitsDryRun,
			"rules_count", len(limitsCfg.Rules),
		))
	}

	// Create log aggregator for buffer (aggregates logs per 10s interval)
	bufferLogAggregator := limits.NewLogAggregator(10 * time.Second)

	// Create buffer with stats collector, limits enforcer, and log aggregator
	buf := buffer.New(cfg.BufferSize, cfg.MaxBatchSize, cfg.FlushInterval, finalExporter, statsCollector, limitsEnforcer, bufferLogAggregator)

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

	// Start stats HTTP server with combined metrics
	statsMux := http.NewServeMux()
	statsMux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		// Write stats metrics
		statsCollector.ServeHTTP(w, r)
		// Write limits metrics (if enabled)
		if limitsEnforcer != nil {
			limitsEnforcer.ServeHTTP(w, r)
		}
		// Write runtime metrics (goroutines, memory, GC, PSI)
		runtimeStats.ServeHTTP(w, r)
	})

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

	logging.Info("metrics-governor started", logging.F(
		"grpc_addr", cfg.GRPCListenAddr,
		"http_addr", cfg.HTTPListenAddr,
		"exporter_endpoint", cfg.ExporterEndpoint,
		"exporter_protocol", cfg.ExporterProtocol,
		"stats_addr", cfg.StatsAddr,
		"receiver_tls", cfg.ReceiverTLSEnabled,
		"receiver_auth", cfg.ReceiverAuthEnabled,
		"limits_enabled", cfg.LimitsConfig != "",
		"queue_enabled", cfg.QueueEnabled,
		"sharding_enabled", cfg.ShardingEnabled,
		"prw_enabled", cfg.PRWListenAddr != "",
	))

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	logging.Info("shutting down")

	// Graceful shutdown
	grpcReceiver.Stop()
	_ = httpReceiver.Stop(ctx)
	if prwReceiver != nil {
		_ = prwReceiver.Stop(ctx)
	}
	_ = statsServer.Shutdown(ctx)
	cancel()
	buf.Wait()
	if prwBuffer != nil {
		prwBuffer.Wait()
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

	logging.Info("shutdown complete")
}
