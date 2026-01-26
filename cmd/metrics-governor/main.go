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
	"github.com/slawomirskowron/metrics-governor/internal/logging"
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

	// Create exporter
	exp, err := exporter.New(ctx, exporter.Config{
		Endpoint: cfg.ExporterEndpoint,
		Insecure: cfg.ExporterInsecure,
		Timeout:  cfg.ExporterTimeout,
	})
	if err != nil {
		logging.Fatal("failed to create exporter", logging.F("error", err.Error()))
	}
	defer exp.Close()

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

	// Create buffer with stats collector
	buf := buffer.New(cfg.BufferSize, cfg.MaxBatchSize, cfg.FlushInterval, exp, statsCollector)

	// Start buffer flush routine
	go buf.Start(ctx)

	// Create and start gRPC receiver
	grpcReceiver := receiver.NewGRPC(cfg.GRPCListenAddr, buf)
	go func() {
		if err := grpcReceiver.Start(); err != nil {
			logging.Error("gRPC receiver error", logging.F("error", err.Error()))
		}
	}()

	// Create and start HTTP receiver
	httpReceiver := receiver.NewHTTP(cfg.HTTPListenAddr, buf)
	go func() {
		if err := httpReceiver.Start(); err != nil {
			logging.Error("HTTP receiver error", logging.F("error", err.Error()))
		}
	}()

	// Start stats HTTP server
	statsServer := &http.Server{
		Addr:    cfg.StatsAddr,
		Handler: http.HandlerFunc(statsCollector.ServeHTTP),
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
		"stats_addr", cfg.StatsAddr,
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
