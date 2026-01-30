package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/slawomirskowron/metrics-governor/internal/buffer"
	"github.com/slawomirskowron/metrics-governor/internal/config"
	"github.com/slawomirskowron/metrics-governor/internal/exporter"
	"github.com/slawomirskowron/metrics-governor/internal/limits"
	"github.com/slawomirskowron/metrics-governor/internal/logging"
	"github.com/slawomirskowron/metrics-governor/internal/queue"
	"github.com/slawomirskowron/metrics-governor/internal/receiver"
	"github.com/slawomirskowron/metrics-governor/internal/stats"
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

	// Create exporter with full configuration
	exp, err := exporter.New(ctx, cfg.ExporterConfig())
	if err != nil {
		logging.Fatal("failed to create exporter", logging.F("error", err.Error()))
	}

	// Wrap with queued exporter if enabled
	var finalExporter exporter.Exporter = exp
	if cfg.QueueEnabled {
		queueCfg := queue.Config{
			Path:              cfg.QueuePath,
			MaxSize:           cfg.QueueMaxSize,
			MaxBytes:          cfg.QueueMaxBytes,
			RetryInterval:     cfg.QueueRetryInterval,
			MaxRetryDelay:     cfg.QueueMaxRetryDelay,
			FullBehavior:      queue.FullBehavior(cfg.QueueFullBehavior),
			TargetUtilization: cfg.QueueTargetUtilization,
			AdaptiveEnabled:   cfg.QueueAdaptiveEnabled,
			CompactThreshold:  cfg.QueueCompactThreshold,
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

	// Create limits enforcer (if configured)
	var limitsEnforcer *limits.Enforcer
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

	// Create buffer with stats collector and limits enforcer
	buf := buffer.New(cfg.BufferSize, cfg.MaxBatchSize, cfg.FlushInterval, finalExporter, statsCollector, limitsEnforcer)

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
	))

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	logging.Info("shutting down")

	// Graceful shutdown
	grpcReceiver.Stop()
	httpReceiver.Stop(ctx)
	statsServer.Shutdown(ctx)
	cancel()
	buf.Wait()

	logging.Info("shutdown complete")
}
