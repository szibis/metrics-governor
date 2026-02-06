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
	if cfg.MaxBatchSize != 5000 {
		t.Errorf("expected MaxBatchSize 5000, got %d", cfg.MaxBatchSize)
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
		GRPCListenAddr:          ":4317",
		ReceiverTLSEnabled:      true,
		ReceiverTLSCertFile:     "/etc/certs/server.crt",
		ReceiverTLSKeyFile:      "/etc/certs/server.key",
		ReceiverAuthEnabled:     true,
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
		HTTPListenAddr:             ":4318",
		ReceiverTLSEnabled:         true,
		ReceiverAuthEnabled:        true,
		ReceiverMaxRequestBodySize: 1024,
		ReceiverReadTimeout:        30 * time.Second,
		ReceiverReadHeaderTimeout:  10 * time.Second,
		ReceiverWriteTimeout:       60 * time.Second,
		ReceiverIdleTimeout:        120 * time.Second,
		ReceiverKeepAlivesEnabled:  true,
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

func TestHTTPReceiverConfigPath(t *testing.T) {
	cfg := &Config{
		HTTPListenAddr:   ":4318",
		HTTPReceiverPath: "/custom/otlp/path",
	}

	httpCfg := cfg.HTTPReceiverConfig()

	if httpCfg.Path != "/custom/otlp/path" {
		t.Errorf("expected Path '/custom/otlp/path', got '%s'", httpCfg.Path)
	}
}

func TestHTTPReceiverConfigDefaultPath(t *testing.T) {
	cfg := &Config{
		HTTPListenAddr: ":4318",
		// HTTPReceiverPath is empty
	}

	httpCfg := cfg.HTTPReceiverConfig()

	if httpCfg.Path != "" {
		t.Errorf("expected empty Path (for default handling), got '%s'", httpCfg.Path)
	}
}

func TestExporterConfigDefaultPath(t *testing.T) {
	cfg := &Config{
		ExporterEndpoint:    "victoriametrics:8428",
		ExporterProtocol:    "http",
		ExporterDefaultPath: "/opentelemetry/v1/metrics",
	}

	expCfg := cfg.ExporterConfig()

	if expCfg.DefaultPath != "/opentelemetry/v1/metrics" {
		t.Errorf("expected DefaultPath '/opentelemetry/v1/metrics', got '%s'", expCfg.DefaultPath)
	}
}

func TestPRWReceiverConfigPath(t *testing.T) {
	cfg := &Config{
		PRWListenAddr:   ":9090",
		PRWReceiverPath: "/custom/prw/write",
	}

	prwCfg := cfg.PRWReceiverConfig()

	if prwCfg.Path != "/custom/prw/write" {
		t.Errorf("expected Path '/custom/prw/write', got '%s'", prwCfg.Path)
	}
}

func TestPRWExporterConfigDefaultPath(t *testing.T) {
	cfg := &Config{
		PRWExporterEndpoint:    "victoriametrics:8428",
		PRWExporterDefaultPath: "/api/v1/write",
	}

	prwExpCfg := cfg.PRWExporterConfig()

	if prwExpCfg.DefaultPath != "/api/v1/write" {
		t.Errorf("expected DefaultPath '/api/v1/write', got '%s'", prwExpCfg.DefaultPath)
	}
}

func TestDefaultConfigPaths(t *testing.T) {
	cfg := DefaultConfig()

	// Check default paths are set correctly
	if cfg.HTTPReceiverPath != "/v1/metrics" {
		t.Errorf("expected HTTPReceiverPath '/v1/metrics' by default, got '%s'", cfg.HTTPReceiverPath)
	}
	if cfg.ExporterDefaultPath != "/v1/metrics" {
		t.Errorf("expected ExporterDefaultPath '/v1/metrics' by default, got '%s'", cfg.ExporterDefaultPath)
	}
	if cfg.PRWReceiverPath != "/api/v1/write" {
		t.Errorf("expected PRWReceiverPath '/api/v1/write' by default, got '%s'", cfg.PRWReceiverPath)
	}
	if cfg.PRWExporterDefaultPath != "/api/v1/write" {
		t.Errorf("expected PRWExporterDefaultPath '/api/v1/write' by default, got '%s'", cfg.PRWExporterDefaultPath)
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

func TestByteSizeFlagTypes(t *testing.T) {
	// Test byteSizeFlag (int64)
	t.Run("byteSizeFlag", func(t *testing.T) {
		var val int64
		f := &byteSizeFlag{target: &val}

		tests := []struct {
			input string
			want  int64
		}{
			{"1Gi", 1073741824},
			{"512Mi", 536870912},
			{"256Ki", 262144},
			{"1073741824", 1073741824},
			{"1.5Gi", 1610612736},
		}
		for _, tc := range tests {
			if err := f.Set(tc.input); err != nil {
				t.Fatalf("Set(%q) error: %v", tc.input, err)
			}
			if val != tc.want {
				t.Errorf("Set(%q): got %d, want %d", tc.input, val, tc.want)
			}
			got := f.Get().(int64)
			if got != tc.want {
				t.Errorf("Get() after Set(%q): got %d, want %d", tc.input, got, tc.want)
			}
		}
		// Verify String() returns human-readable format
		f.Set("1Gi")
		if s := f.String(); s != "1Gi" {
			t.Errorf("String() after Set(1Gi): got %q, want %q", s, "1Gi")
		}
	})

	// Test byteSizeIntFlag (int)
	t.Run("byteSizeIntFlag", func(t *testing.T) {
		var val int
		f := &byteSizeIntFlag{target: &val}

		if err := f.Set("256Ki"); err != nil {
			t.Fatalf("Set(256Ki) error: %v", err)
		}
		if val != 262144 {
			t.Errorf("Set(256Ki): got %d, want 262144", val)
		}
		got := f.Get().(int)
		if got != 262144 {
			t.Errorf("Get() = %d, want 262144", got)
		}
		if s := f.String(); s != "256Ki" {
			t.Errorf("String() = %q, want %q", s, "256Ki")
		}
	})

	// Test that these flags work with Go's flag package
	t.Run("flag_integration", func(t *testing.T) {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		var maxBytes int64 = 1073741824
		var bufSize = 262144

		fs.Var(&byteSizeFlag{target: &maxBytes}, "max-bytes", "test")
		fs.Var(&byteSizeIntFlag{target: &bufSize}, "buf-size", "test")

		if err := fs.Parse([]string{"-max-bytes", "2Gi", "-buf-size", "512Ki"}); err != nil {
			t.Fatalf("Parse error: %v", err)
		}
		if maxBytes != 2*1073741824 {
			t.Errorf("max-bytes = %d, want %d", maxBytes, 2*1073741824)
		}
		if bufSize != 512*1024 {
			t.Errorf("buf-size = %d, want %d", bufSize, 512*1024)
		}
	})
}

func TestValidate_DefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config should be valid, got: %v", err)
	}
}

func TestValidate_MemoryLimitRatio(t *testing.T) {
	cfg := DefaultConfig()

	cfg.MemoryLimitRatio = 1.5
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for memory-limit-ratio > 1.0")
	}

	cfg.MemoryLimitRatio = -0.1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for memory-limit-ratio < 0")
	}

	cfg.MemoryLimitRatio = 0.0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("0.0 should be valid, got: %v", err)
	}
}

func TestValidate_QueueTargetUtilization(t *testing.T) {
	cfg := DefaultConfig()

	cfg.QueueTargetUtilization = 2.0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for target utilization > 1.0")
	}
}

func TestValidate_CardinalityFPRate(t *testing.T) {
	cfg := DefaultConfig()

	cfg.CardinalityFPRate = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for fp-rate = 0")
	}

	cfg.CardinalityFPRate = 1.0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for fp-rate = 1.0")
	}

	cfg.CardinalityFPRate = 0.001
	if err := cfg.Validate(); err != nil {
		t.Fatalf("0.001 should be valid, got: %v", err)
	}
}

func TestValidate_CardinalityHLLPrecision(t *testing.T) {
	cfg := DefaultConfig()

	cfg.CardinalityHLLPrecision = 3
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for hll-precision < 4")
	}

	cfg.CardinalityHLLPrecision = 19
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for hll-precision > 18")
	}

	cfg.CardinalityHLLPrecision = 14
	if err := cfg.Validate(); err != nil {
		t.Fatalf("14 should be valid, got: %v", err)
	}
}

func TestValidate_CardinalityMode(t *testing.T) {
	cfg := DefaultConfig()

	cfg.CardinalityMode = "invalid"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid cardinality mode")
	}

	for _, mode := range []string{"bloom", "exact", "hybrid"} {
		cfg.CardinalityMode = mode
		if err := cfg.Validate(); err != nil {
			t.Fatalf("mode %q should be valid, got: %v", mode, err)
		}
	}
}

func TestValidate_ShardingCrossField(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ShardingEnabled = true
	cfg.ShardingHeadlessService = ""

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error when sharding is enabled without headless service")
	}

	cfg.ShardingHeadlessService = "my-service.ns.svc:4317"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("should be valid with headless service set, got: %v", err)
	}
}

func TestValidate_ExporterProtocol(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ExporterProtocol = "websocket"

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid protocol")
	}
}

func TestValidate_QueueType(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueueType = "redis"

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid queue type")
	}
}

func TestValidate_BackoffMultiplier(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueueBackoffEnabled = true
	cfg.QueueBackoffMultiplier = 0.5

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for backoff multiplier <= 1.0")
	}
}

func TestValidate_MultipleErrors(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MemoryLimitRatio = 5.0
	cfg.CardinalityFPRate = 0
	cfg.ShardingEnabled = true
	cfg.ShardingHeadlessService = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected multiple validation errors")
	}

	// Check that all three errors are present
	errMsg := err.Error()
	if !contains(errMsg, "memory-limit-ratio") {
		t.Error("missing memory-limit-ratio error")
	}
	if !contains(errMsg, "cardinality-fp-rate") {
		t.Error("missing cardinality-fp-rate error")
	}
	if !contains(errMsg, "sharding-headless-service") {
		t.Error("missing sharding-headless-service error")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
