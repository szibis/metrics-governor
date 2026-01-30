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

func TestReceiverTLSConfig(t *testing.T) {
	cfg := &Config{
		ReceiverTLSEnabled:    true,
		ReceiverTLSCertFile:   "/etc/certs/server.crt",
		ReceiverTLSKeyFile:    "/etc/certs/server.key",
		ReceiverTLSCAFile:     "/etc/certs/ca.crt",
		ReceiverTLSClientAuth: true,
	}

	tlsCfg := cfg.ReceiverTLSConfig()

	if !tlsCfg.Enabled {
		t.Error("expected TLS enabled")
	}
	if tlsCfg.CertFile != "/etc/certs/server.crt" {
		t.Errorf("expected CertFile '/etc/certs/server.crt', got '%s'", tlsCfg.CertFile)
	}
	if tlsCfg.KeyFile != "/etc/certs/server.key" {
		t.Errorf("expected KeyFile '/etc/certs/server.key', got '%s'", tlsCfg.KeyFile)
	}
	if tlsCfg.CAFile != "/etc/certs/ca.crt" {
		t.Errorf("expected CAFile '/etc/certs/ca.crt', got '%s'", tlsCfg.CAFile)
	}
	if !tlsCfg.ClientAuth {
		t.Error("expected ClientAuth true")
	}
}

func TestReceiverAuthConfig(t *testing.T) {
	cfg := &Config{
		ReceiverAuthEnabled:       true,
		ReceiverAuthBearerToken:   "secret-token",
		ReceiverAuthBasicUsername: "user",
		ReceiverAuthBasicPassword: "pass",
	}

	authCfg := cfg.ReceiverAuthConfig()

	if !authCfg.Enabled {
		t.Error("expected auth enabled")
	}
	if authCfg.BearerToken != "secret-token" {
		t.Errorf("expected BearerToken 'secret-token', got '%s'", authCfg.BearerToken)
	}
	if authCfg.BasicAuthUsername != "user" {
		t.Errorf("expected BasicAuthUsername 'user', got '%s'", authCfg.BasicAuthUsername)
	}
	if authCfg.BasicAuthPassword != "pass" {
		t.Errorf("expected BasicAuthPassword 'pass', got '%s'", authCfg.BasicAuthPassword)
	}
}

func TestGRPCReceiverConfig(t *testing.T) {
	cfg := &Config{
		GRPCListenAddr:         ":4317",
		ReceiverTLSEnabled:     true,
		ReceiverTLSCertFile:    "/etc/certs/server.crt",
		ReceiverTLSKeyFile:     "/etc/certs/server.key",
		ReceiverAuthEnabled:    true,
		ReceiverAuthBearerToken: "secret-token",
	}

	grpcCfg := cfg.GRPCReceiverConfig()

	if grpcCfg.Addr != ":4317" {
		t.Errorf("expected Addr ':4317', got '%s'", grpcCfg.Addr)
	}
	if !grpcCfg.TLS.Enabled {
		t.Error("expected TLS enabled")
	}
	if !grpcCfg.Auth.Enabled {
		t.Error("expected Auth enabled")
	}
}

func TestHTTPReceiverConfig(t *testing.T) {
	cfg := &Config{
		HTTPListenAddr:              ":4318",
		ReceiverTLSEnabled:          true,
		ReceiverAuthEnabled:         true,
		ReceiverMaxRequestBodySize:  1024,
		ReceiverReadTimeout:         30 * time.Second,
		ReceiverReadHeaderTimeout:   10 * time.Second,
		ReceiverWriteTimeout:        60 * time.Second,
		ReceiverIdleTimeout:         120 * time.Second,
		ReceiverKeepAlivesEnabled:   true,
	}

	httpCfg := cfg.HTTPReceiverConfig()

	if httpCfg.Addr != ":4318" {
		t.Errorf("expected Addr ':4318', got '%s'", httpCfg.Addr)
	}
	if !httpCfg.TLS.Enabled {
		t.Error("expected TLS enabled")
	}
	if !httpCfg.Auth.Enabled {
		t.Error("expected Auth enabled")
	}
	if httpCfg.Server.MaxRequestBodySize != 1024 {
		t.Errorf("expected MaxRequestBodySize 1024, got %d", httpCfg.Server.MaxRequestBodySize)
	}
	if httpCfg.Server.ReadTimeout != 30*time.Second {
		t.Errorf("expected ReadTimeout 30s, got %v", httpCfg.Server.ReadTimeout)
	}
}

func TestExporterTLSConfig(t *testing.T) {
	cfg := &Config{
		ExporterTLSEnabled:            true,
		ExporterTLSCertFile:           "/etc/certs/client.crt",
		ExporterTLSKeyFile:            "/etc/certs/client.key",
		ExporterTLSCAFile:             "/etc/certs/ca.crt",
		ExporterTLSInsecureSkipVerify: true,
		ExporterTLSServerName:         "example.com",
	}

	tlsCfg := cfg.ExporterTLSConfig()

	if !tlsCfg.Enabled {
		t.Error("expected TLS enabled")
	}
	if tlsCfg.CertFile != "/etc/certs/client.crt" {
		t.Errorf("expected CertFile '/etc/certs/client.crt', got '%s'", tlsCfg.CertFile)
	}
	if tlsCfg.CAFile != "/etc/certs/ca.crt" {
		t.Errorf("expected CAFile '/etc/certs/ca.crt', got '%s'", tlsCfg.CAFile)
	}
	if !tlsCfg.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify true")
	}
	if tlsCfg.ServerName != "example.com" {
		t.Errorf("expected ServerName 'example.com', got '%s'", tlsCfg.ServerName)
	}
}

func TestExporterAuthConfig(t *testing.T) {
	cfg := &Config{
		ExporterAuthBearerToken:   "secret-token",
		ExporterAuthBasicUsername: "user",
		ExporterAuthBasicPassword: "pass",
		ExporterAuthHeaders:       "X-Custom=value,X-Other=other",
	}

	authCfg := cfg.ExporterAuthConfig()

	if authCfg.BearerToken != "secret-token" {
		t.Errorf("expected BearerToken 'secret-token', got '%s'", authCfg.BearerToken)
	}
	if authCfg.BasicAuthUsername != "user" {
		t.Errorf("expected BasicAuthUsername 'user', got '%s'", authCfg.BasicAuthUsername)
	}
	if len(authCfg.Headers) != 2 {
		t.Errorf("expected 2 headers, got %d", len(authCfg.Headers))
	}
	if authCfg.Headers["X-Custom"] != "value" {
		t.Errorf("expected header X-Custom='value', got '%s'", authCfg.Headers["X-Custom"])
	}
}

func TestExporterCompressionConfig(t *testing.T) {
	cfg := &Config{
		ExporterCompression:      "gzip",
		ExporterCompressionLevel: 6,
	}

	compCfg := cfg.ExporterCompressionConfig()

	if compCfg.Type != "gzip" {
		t.Errorf("expected Type 'gzip', got '%s'", compCfg.Type)
	}
	if compCfg.Level != 6 {
		t.Errorf("expected Level 6, got %d", compCfg.Level)
	}
}

func TestExporterHTTPClientConfig(t *testing.T) {
	cfg := &Config{
		ExporterMaxIdleConns:         200,
		ExporterMaxIdleConnsPerHost:  50,
		ExporterMaxConnsPerHost:      100,
		ExporterIdleConnTimeout:      2 * time.Minute,
		ExporterDisableKeepAlives:    true,
		ExporterForceHTTP2:           true,
		ExporterHTTP2ReadIdleTimeout: 30 * time.Second,
		ExporterHTTP2PingTimeout:     10 * time.Second,
	}

	httpClientCfg := cfg.ExporterHTTPClientConfig()

	if httpClientCfg.MaxIdleConns != 200 {
		t.Errorf("expected MaxIdleConns 200, got %d", httpClientCfg.MaxIdleConns)
	}
	if httpClientCfg.MaxIdleConnsPerHost != 50 {
		t.Errorf("expected MaxIdleConnsPerHost 50, got %d", httpClientCfg.MaxIdleConnsPerHost)
	}
	if httpClientCfg.MaxConnsPerHost != 100 {
		t.Errorf("expected MaxConnsPerHost 100, got %d", httpClientCfg.MaxConnsPerHost)
	}
	if !httpClientCfg.DisableKeepAlives {
		t.Error("expected DisableKeepAlives true")
	}
	if !httpClientCfg.ForceAttemptHTTP2 {
		t.Error("expected ForceAttemptHTTP2 true")
	}
}

func TestExporterConfig(t *testing.T) {
	cfg := &Config{
		ExporterEndpoint:    "otel:4317",
		ExporterProtocol:    "grpc",
		ExporterInsecure:    false,
		ExporterTimeout:     60 * time.Second,
		ExporterTLSEnabled:  true,
		ExporterCompression: "gzip",
	}

	expCfg := cfg.ExporterConfig()

	if expCfg.Endpoint != "otel:4317" {
		t.Errorf("expected Endpoint 'otel:4317', got '%s'", expCfg.Endpoint)
	}
	if expCfg.Protocol != "grpc" {
		t.Errorf("expected Protocol 'grpc', got '%s'", expCfg.Protocol)
	}
	if expCfg.Insecure {
		t.Error("expected Insecure false")
	}
	if expCfg.Timeout != 60*time.Second {
		t.Errorf("expected Timeout 60s, got %v", expCfg.Timeout)
	}
}

func TestQueueConfig(t *testing.T) {
	cfg := &Config{
		QueueEnabled:           true,
		QueuePath:              "/data/queue",
		QueueMaxSize:           5000,
		QueueMaxBytes:          500000000,
		QueueRetryInterval:     10 * time.Second,
		QueueMaxRetryDelay:     10 * time.Minute,
		QueueFullBehavior:      "drop_newest",
		QueueTargetUtilization: 0.9,
		QueueAdaptiveEnabled:   true,
		QueueCompactThreshold:  0.3,
	}

	queueCfg := cfg.QueueConfig()

	if queueCfg.Path != "/data/queue" {
		t.Errorf("expected Path '/data/queue', got '%s'", queueCfg.Path)
	}
	if queueCfg.MaxSize != 5000 {
		t.Errorf("expected MaxSize 5000, got %d", queueCfg.MaxSize)
	}
	if queueCfg.MaxBytes != 500000000 {
		t.Errorf("expected MaxBytes 500000000, got %d", queueCfg.MaxBytes)
	}
	if queueCfg.RetryInterval != 10*time.Second {
		t.Errorf("expected RetryInterval 10s, got %v", queueCfg.RetryInterval)
	}
	if queueCfg.FullBehavior != "drop_newest" {
		t.Errorf("expected FullBehavior 'drop_newest', got '%s'", queueCfg.FullBehavior)
	}
	if queueCfg.TargetUtilization != 0.9 {
		t.Errorf("expected TargetUtilization 0.9, got %v", queueCfg.TargetUtilization)
	}
}
