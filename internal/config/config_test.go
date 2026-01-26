package config

import (
	"flag"
	"os"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.GRPCListenAddr != ":4317" {
		t.Errorf("expected GRPCListenAddr ':4317', got '%s'", cfg.GRPCListenAddr)
	}
	if cfg.HTTPListenAddr != ":4318" {
		t.Errorf("expected HTTPListenAddr ':4318', got '%s'", cfg.HTTPListenAddr)
	}
	if cfg.ExporterEndpoint != "localhost:4317" {
		t.Errorf("expected ExporterEndpoint 'localhost:4317', got '%s'", cfg.ExporterEndpoint)
	}
	if cfg.ExporterInsecure != true {
		t.Errorf("expected ExporterInsecure true, got %v", cfg.ExporterInsecure)
	}
	if cfg.ExporterTimeout != 30*time.Second {
		t.Errorf("expected ExporterTimeout 30s, got %v", cfg.ExporterTimeout)
	}
	if cfg.BufferSize != 10000 {
		t.Errorf("expected BufferSize 10000, got %d", cfg.BufferSize)
	}
	if cfg.FlushInterval != 5*time.Second {
		t.Errorf("expected FlushInterval 5s, got %v", cfg.FlushInterval)
	}
	if cfg.MaxBatchSize != 1000 {
		t.Errorf("expected MaxBatchSize 1000, got %d", cfg.MaxBatchSize)
	}
	if cfg.StatsAddr != ":9090" {
		t.Errorf("expected StatsAddr ':9090', got '%s'", cfg.StatsAddr)
	}
	if cfg.StatsLabels != "" {
		t.Errorf("expected StatsLabels '', got '%s'", cfg.StatsLabels)
	}
	if cfg.LimitsConfig != "" {
		t.Errorf("expected LimitsConfig '', got '%s'", cfg.LimitsConfig)
	}
	if cfg.LimitsDryRun != true {
		t.Errorf("expected LimitsDryRun true, got %v", cfg.LimitsDryRun)
	}
}

func TestParseFlags(t *testing.T) {
	// Reset flags for testing
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	// Save original args
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	// Test with custom args
	os.Args = []string{
		"test",
		"-grpc-listen", ":5317",
		"-http-listen", ":5318",
		"-exporter-endpoint", "otel:4317",
		"-exporter-insecure=false",
		"-exporter-timeout", "60s",
		"-buffer-size", "50000",
		"-flush-interval", "10s",
		"-batch-size", "2000",
		"-stats-addr", ":8080",
		"-stats-labels", "service,env",
		"-limits-config", "/etc/limits.yaml",
		"-limits-dry-run=false",
	}

	cfg := ParseFlags()

	if cfg.GRPCListenAddr != ":5317" {
		t.Errorf("expected GRPCListenAddr ':5317', got '%s'", cfg.GRPCListenAddr)
	}
	if cfg.HTTPListenAddr != ":5318" {
		t.Errorf("expected HTTPListenAddr ':5318', got '%s'", cfg.HTTPListenAddr)
	}
	if cfg.ExporterEndpoint != "otel:4317" {
		t.Errorf("expected ExporterEndpoint 'otel:4317', got '%s'", cfg.ExporterEndpoint)
	}
	if cfg.ExporterInsecure != false {
		t.Errorf("expected ExporterInsecure false, got %v", cfg.ExporterInsecure)
	}
	if cfg.ExporterTimeout != 60*time.Second {
		t.Errorf("expected ExporterTimeout 60s, got %v", cfg.ExporterTimeout)
	}
	if cfg.BufferSize != 50000 {
		t.Errorf("expected BufferSize 50000, got %d", cfg.BufferSize)
	}
	if cfg.FlushInterval != 10*time.Second {
		t.Errorf("expected FlushInterval 10s, got %v", cfg.FlushInterval)
	}
	if cfg.MaxBatchSize != 2000 {
		t.Errorf("expected MaxBatchSize 2000, got %d", cfg.MaxBatchSize)
	}
	if cfg.StatsAddr != ":8080" {
		t.Errorf("expected StatsAddr ':8080', got '%s'", cfg.StatsAddr)
	}
	if cfg.StatsLabels != "service,env" {
		t.Errorf("expected StatsLabels 'service,env', got '%s'", cfg.StatsLabels)
	}
	if cfg.LimitsConfig != "/etc/limits.yaml" {
		t.Errorf("expected LimitsConfig '/etc/limits.yaml', got '%s'", cfg.LimitsConfig)
	}
	if cfg.LimitsDryRun != false {
		t.Errorf("expected LimitsDryRun false, got %v", cfg.LimitsDryRun)
	}
}

func TestParseFlagsHelp(t *testing.T) {
	// Reset flags for testing
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	// Save original args
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{"test", "-help"}

	cfg := ParseFlags()

	if !cfg.ShowHelp {
		t.Error("expected ShowHelp to be true")
	}
}

func TestParseFlagsVersion(t *testing.T) {
	// Reset flags for testing
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	// Save original args
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{"test", "-version"}

	cfg := ParseFlags()

	if !cfg.ShowVersion {
		t.Error("expected ShowVersion to be true")
	}
}

func TestParseFlagsShortHelp(t *testing.T) {
	// Reset flags for testing
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	// Save original args
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{"test", "-h"}

	cfg := ParseFlags()

	if !cfg.ShowHelp {
		t.Error("expected ShowHelp to be true with -h")
	}
}

func TestParseFlagsShortVersion(t *testing.T) {
	// Reset flags for testing
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	// Save original args
	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{"test", "-v"}

	cfg := ParseFlags()

	if !cfg.ShowVersion {
		t.Error("expected ShowVersion to be true with -v")
	}
}

func TestPrintUsage(t *testing.T) {
	// Just ensure it doesn't panic
	// Capture stderr
	oldStderr := os.Stderr
	_, w, _ := os.Pipe()
	os.Stderr = w

	PrintUsage()

	w.Close()
	os.Stderr = oldStderr
}

func TestPrintVersion(t *testing.T) {
	// Just ensure it doesn't panic
	// Capture stdout
	oldStdout := os.Stdout
	_, w, _ := os.Pipe()
	os.Stdout = w

	PrintVersion()

	w.Close()
	os.Stdout = oldStdout
}
