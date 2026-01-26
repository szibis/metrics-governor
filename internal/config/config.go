package config

import (
	"flag"
	"fmt"
	"os"
	"time"
)

// version is set at build time via ldflags
var version = "dev"

// Config holds the application configuration.
type Config struct {
	// Receiver settings
	GRPCListenAddr string
	HTTPListenAddr string

	// Exporter settings
	ExporterEndpoint string
	ExporterInsecure bool
	ExporterTimeout  time.Duration

	// Buffer settings
	BufferSize    int
	FlushInterval time.Duration
	MaxBatchSize  int

	// Stats settings
	StatsAddr   string
	StatsLabels string

	// Flags
	ShowHelp    bool
	ShowVersion bool
}

// ParseFlags parses command line flags and returns the configuration.
func ParseFlags() *Config {
	cfg := &Config{}

	// Receiver flags
	flag.StringVar(&cfg.GRPCListenAddr, "grpc-listen", ":4317", "gRPC receiver listen address")
	flag.StringVar(&cfg.HTTPListenAddr, "http-listen", ":4318", "HTTP receiver listen address")

	// Exporter flags
	flag.StringVar(&cfg.ExporterEndpoint, "exporter-endpoint", "localhost:4317", "OTLP exporter endpoint")
	flag.BoolVar(&cfg.ExporterInsecure, "exporter-insecure", true, "Use insecure connection for exporter")
	flag.DurationVar(&cfg.ExporterTimeout, "exporter-timeout", 30*time.Second, "Exporter request timeout")

	// Buffer flags
	flag.IntVar(&cfg.BufferSize, "buffer-size", 10000, "Maximum number of metrics to buffer")
	flag.DurationVar(&cfg.FlushInterval, "flush-interval", 5*time.Second, "Buffer flush interval")
	flag.IntVar(&cfg.MaxBatchSize, "batch-size", 1000, "Maximum batch size for export")

	// Stats flags
	flag.StringVar(&cfg.StatsAddr, "stats-addr", ":9090", "Stats/metrics HTTP endpoint address")
	flag.StringVar(&cfg.StatsLabels, "stats-labels", "", "Comma-separated labels to track for grouping (e.g., service,env,cluster)")

	// Help and version
	flag.BoolVar(&cfg.ShowHelp, "help", false, "Show help message")
	flag.BoolVar(&cfg.ShowHelp, "h", false, "Show help message (shorthand)")
	flag.BoolVar(&cfg.ShowVersion, "version", false, "Show version")
	flag.BoolVar(&cfg.ShowVersion, "v", false, "Show version (shorthand)")

	flag.Usage = PrintUsage

	flag.Parse()

	return cfg
}

// PrintUsage prints the help message.
func PrintUsage() {
	fmt.Fprintf(os.Stderr, `metrics-governor - OTLP metrics proxy with buffering

USAGE:
    metrics-governor [OPTIONS]

DESCRIPTION:
    Receives OTLP metrics via gRPC and HTTP, buffers them, and forwards
    to a configurable OTLP endpoint with batching support.

OPTIONS:
    Receiver:
        -grpc-listen <addr>      gRPC receiver listen address (default: ":4317")
        -http-listen <addr>      HTTP receiver listen address (default: ":4318")

    Exporter:
        -exporter-endpoint <addr>  OTLP exporter endpoint (default: "localhost:4317")
        -exporter-insecure         Use insecure connection (default: true)
        -exporter-timeout <dur>    Exporter request timeout (default: 30s)

    Buffer:
        -buffer-size <n>         Maximum metrics to buffer (default: 10000)
        -flush-interval <dur>    Buffer flush interval (default: 5s)
        -batch-size <n>          Maximum batch size for export (default: 1000)

    Stats:
        -stats-addr <addr>       Stats/metrics HTTP endpoint address (default: ":9090")
        -stats-labels <labels>   Comma-separated labels to track (e.g., service,env,cluster)

    General:
        -h, -help                Show this help message
        -v, -version             Show version

EXAMPLES:
    # Start with default settings
    metrics-governor

    # Custom receiver ports
    metrics-governor -grpc-listen :5317 -http-listen :5318

    # Forward to remote endpoint
    metrics-governor -exporter-endpoint otel-collector:4317

    # Adjust buffering
    metrics-governor -buffer-size 50000 -flush-interval 10s -batch-size 2000

    # Enable stats tracking by service and environment
    metrics-governor -stats-labels service,env,cluster

`)
}

// PrintVersion prints the version and exits.
func PrintVersion() {
	fmt.Printf("metrics-governor version %s\n", version)
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		GRPCListenAddr:   ":4317",
		HTTPListenAddr:   ":4318",
		ExporterEndpoint: "localhost:4317",
		ExporterInsecure: true,
		ExporterTimeout:  30 * time.Second,
		BufferSize:       10000,
		FlushInterval:    5 * time.Second,
		MaxBatchSize:     1000,
		StatsAddr:        ":9090",
		StatsLabels:      "",
	}
}
