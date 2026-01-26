package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/slawomirskowron/metrics-governor/internal/buffer"
	"github.com/slawomirskowron/metrics-governor/internal/config"
	"github.com/slawomirskowron/metrics-governor/internal/exporter"
	"github.com/slawomirskowron/metrics-governor/internal/receiver"
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
		log.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	// Create buffer
	buf := buffer.New(cfg.BufferSize, cfg.MaxBatchSize, cfg.FlushInterval, exp)

	// Start buffer flush routine
	go buf.Start(ctx)

	// Create and start gRPC receiver
	grpcReceiver := receiver.NewGRPC(cfg.GRPCListenAddr, buf)
	go func() {
		if err := grpcReceiver.Start(); err != nil {
			log.Printf("gRPC receiver error: %v", err)
		}
	}()

	// Create and start HTTP receiver
	httpReceiver := receiver.NewHTTP(cfg.HTTPListenAddr, buf)
	go func() {
		if err := httpReceiver.Start(); err != nil {
			log.Printf("HTTP receiver error: %v", err)
		}
	}()

	log.Println("Metrics Governor started")

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down...")

	// Graceful shutdown
	grpcReceiver.Stop()
	httpReceiver.Stop(ctx)
	cancel()
	buf.Wait()

	log.Println("Shutdown complete")
}
