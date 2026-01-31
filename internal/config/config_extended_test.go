package config

import (
	"flag"
	"os"
	"testing"
	"time"
)

func TestShardingConfig(t *testing.T) {
	cfg := &Config{
		ShardingEnabled:            true,
		ShardingHeadlessService:    "vminsert-headless.monitoring.svc:8428",
		ShardingDNSRefreshInterval: 60 * time.Second,
		ShardingDNSTimeout:         10 * time.Second,
		ShardingLabels:             "service,env,cluster",
		ShardingVirtualNodes:       200,
		ShardingFallbackOnEmpty:    true,
	}

	shardCfg := cfg.ShardingConfig()

	if !shardCfg.Enabled {
		t.Error("expected Enabled true")
	}
	if shardCfg.HeadlessService != "vminsert-headless.monitoring.svc:8428" {
		t.Errorf("expected HeadlessService 'vminsert-headless.monitoring.svc:8428', got '%s'", shardCfg.HeadlessService)
	}
	if shardCfg.DNSRefreshInterval != 60*time.Second {
		t.Errorf("expected DNSRefreshInterval 60s, got %v", shardCfg.DNSRefreshInterval)
	}
	if shardCfg.DNSTimeout != 10*time.Second {
		t.Errorf("expected DNSTimeout 10s, got %v", shardCfg.DNSTimeout)
	}
	if len(shardCfg.Labels) != 3 {
		t.Errorf("expected 3 labels, got %d", len(shardCfg.Labels))
	}
	if shardCfg.Labels[0] != "service" {
		t.Errorf("expected first label 'service', got '%s'", shardCfg.Labels[0])
	}
	if shardCfg.VirtualNodes != 200 {
		t.Errorf("expected VirtualNodes 200, got %d", shardCfg.VirtualNodes)
	}
	if !shardCfg.FallbackOnEmpty {
		t.Error("expected FallbackOnEmpty true")
	}
}

func TestShardingConfigEmptyLabels(t *testing.T) {
	cfg := &Config{
		ShardingEnabled: true,
		ShardingLabels:  "",
	}

	shardCfg := cfg.ShardingConfig()

	if len(shardCfg.Labels) != 0 {
		t.Errorf("expected 0 labels for empty string, got %d", len(shardCfg.Labels))
	}
}

func TestPRWReceiverConfig(t *testing.T) {
	cfg := &Config{
		PRWListenAddr:                 ":9091",
		PRWReceiverVersion:            "2.0",
		PRWReceiverTLSEnabled:         true,
		PRWReceiverTLSCertFile:        "/etc/certs/server.crt",
		PRWReceiverTLSKeyFile:         "/etc/certs/server.key",
		PRWReceiverTLSCAFile:          "/etc/certs/ca.crt",
		PRWReceiverTLSClientAuth:      true,
		PRWReceiverAuthEnabled:        true,
		PRWReceiverAuthBearerToken:    "prw-token",
		PRWReceiverMaxRequestBodySize: 10485760,
		PRWReceiverReadTimeout:        2 * time.Minute,
		PRWReceiverWriteTimeout:       1 * time.Minute,
	}

	prwCfg := cfg.PRWReceiverConfig()

	if prwCfg.Addr != ":9091" {
		t.Errorf("expected Addr ':9091', got '%s'", prwCfg.Addr)
	}
	if !prwCfg.TLS.Enabled {
		t.Error("expected TLS enabled")
	}
	if prwCfg.TLS.CertFile != "/etc/certs/server.crt" {
		t.Errorf("expected TLS CertFile '/etc/certs/server.crt', got '%s'", prwCfg.TLS.CertFile)
	}
	if prwCfg.TLS.KeyFile != "/etc/certs/server.key" {
		t.Errorf("expected TLS KeyFile '/etc/certs/server.key', got '%s'", prwCfg.TLS.KeyFile)
	}
	if prwCfg.TLS.CAFile != "/etc/certs/ca.crt" {
		t.Errorf("expected TLS CAFile '/etc/certs/ca.crt', got '%s'", prwCfg.TLS.CAFile)
	}
	if !prwCfg.TLS.ClientAuth {
		t.Error("expected TLS ClientAuth true")
	}
	if !prwCfg.Auth.Enabled {
		t.Error("expected Auth enabled")
	}
	if prwCfg.Auth.BearerToken != "prw-token" {
		t.Errorf("expected Auth BearerToken 'prw-token', got '%s'", prwCfg.Auth.BearerToken)
	}
	if prwCfg.Server.MaxRequestBodySize != 10485760 {
		t.Errorf("expected MaxRequestBodySize 10485760, got %d", prwCfg.Server.MaxRequestBodySize)
	}
	if prwCfg.Server.ReadTimeout != 2*time.Minute {
		t.Errorf("expected ReadTimeout 2m, got %v", prwCfg.Server.ReadTimeout)
	}
	if prwCfg.Server.WriteTimeout != 1*time.Minute {
		t.Errorf("expected WriteTimeout 1m, got %v", prwCfg.Server.WriteTimeout)
	}
}

func TestPRWExporterConfig(t *testing.T) {
	cfg := &Config{
		PRWExporterEndpoint:        "http://victoria:8428/api/v1/write",
		PRWExporterVersion:         "1.0",
		PRWExporterTimeout:         45 * time.Second,
		PRWExporterTLSEnabled:      true,
		PRWExporterTLSCertFile:     "/etc/certs/client.crt",
		PRWExporterTLSKeyFile:      "/etc/certs/client.key",
		PRWExporterTLSCAFile:       "/etc/certs/ca.crt",
		PRWExporterTLSSkipVerify:   true,
		PRWExporterAuthBearerToken: "export-token",
		PRWExporterVMMode:          true,
		PRWExporterVMCompression:   "zstd",
		PRWExporterVMShortEndpoint: true,
		PRWExporterVMExtraLabels:   "env=prod,cluster=us-east",
		// HTTP client settings from exporter
		ExporterMaxIdleConns:        50,
		ExporterMaxIdleConnsPerHost: 25,
	}

	prwExpCfg := cfg.PRWExporterConfig()

	if prwExpCfg.Endpoint != "http://victoria:8428/api/v1/write" {
		t.Errorf("expected Endpoint 'http://victoria:8428/api/v1/write', got '%s'", prwExpCfg.Endpoint)
	}
	if prwExpCfg.Timeout != 45*time.Second {
		t.Errorf("expected Timeout 45s, got %v", prwExpCfg.Timeout)
	}
	if !prwExpCfg.TLS.Enabled {
		t.Error("expected TLS enabled")
	}
	if prwExpCfg.TLS.CertFile != "/etc/certs/client.crt" {
		t.Errorf("expected TLS CertFile '/etc/certs/client.crt', got '%s'", prwExpCfg.TLS.CertFile)
	}
	if !prwExpCfg.TLS.InsecureSkipVerify {
		t.Error("expected TLS InsecureSkipVerify true")
	}
	if prwExpCfg.Auth.BearerToken != "export-token" {
		t.Errorf("expected Auth BearerToken 'export-token', got '%s'", prwExpCfg.Auth.BearerToken)
	}
	if !prwExpCfg.VMMode {
		t.Error("expected VMMode true")
	}
	if prwExpCfg.VMOptions.Compression != "zstd" {
		t.Errorf("expected VMOptions Compression 'zstd', got '%s'", prwExpCfg.VMOptions.Compression)
	}
	if !prwExpCfg.VMOptions.UseShortEndpoint {
		t.Error("expected VMOptions UseShortEndpoint true")
	}
	if len(prwExpCfg.VMOptions.ExtraLabels) != 2 {
		t.Errorf("expected 2 extra labels, got %d", len(prwExpCfg.VMOptions.ExtraLabels))
	}
	if prwExpCfg.VMOptions.ExtraLabels["env"] != "prod" {
		t.Errorf("expected ExtraLabels['env']='prod', got '%s'", prwExpCfg.VMOptions.ExtraLabels["env"])
	}
}

func TestPRWExporterConfigEmptyLabels(t *testing.T) {
	cfg := &Config{
		PRWExporterEndpoint:      "http://victoria:8428",
		PRWExporterVMExtraLabels: "",
	}

	prwExpCfg := cfg.PRWExporterConfig()

	if len(prwExpCfg.VMOptions.ExtraLabels) != 0 {
		t.Errorf("expected 0 extra labels for empty string, got %d", len(prwExpCfg.VMOptions.ExtraLabels))
	}
}

func TestPRWBufferConfig(t *testing.T) {
	cfg := &Config{
		PRWBufferSize:    20000,
		PRWBatchSize:     2000,
		PRWFlushInterval: 10 * time.Second,
	}

	bufCfg := cfg.PRWBufferConfig()

	if bufCfg.MaxSize != 20000 {
		t.Errorf("expected MaxSize 20000, got %d", bufCfg.MaxSize)
	}
	if bufCfg.MaxBatchSize != 2000 {
		t.Errorf("expected MaxBatchSize 2000, got %d", bufCfg.MaxBatchSize)
	}
	if bufCfg.FlushInterval != 10*time.Second {
		t.Errorf("expected FlushInterval 10s, got %v", bufCfg.FlushInterval)
	}
}

func TestPRWQueueConfig(t *testing.T) {
	cfg := &Config{
		PRWQueuePath:          "/data/prw-queue",
		PRWQueueMaxSize:       5000,
		PRWQueueMaxBytes:      500000000,
		PRWQueueRetryInterval: 10 * time.Second,
		PRWQueueMaxRetryDelay: 3 * time.Minute,
	}

	queueCfg := cfg.PRWQueueConfig()

	if queueCfg.Path != "/data/prw-queue" {
		t.Errorf("expected Path '/data/prw-queue', got '%s'", queueCfg.Path)
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
	if queueCfg.MaxRetryDelay != 3*time.Minute {
		t.Errorf("expected MaxRetryDelay 3m, got %v", queueCfg.MaxRetryDelay)
	}
}

// Test applyFlagOverrides with various flags

func TestParseFlagsTLS(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-receiver-tls-enabled",
		"-receiver-tls-cert", "/etc/certs/server.crt",
		"-receiver-tls-key", "/etc/certs/server.key",
		"-receiver-tls-ca", "/etc/certs/ca.crt",
		"-receiver-tls-client-auth",
	}

	cfg := ParseFlags()

	if !cfg.ReceiverTLSEnabled {
		t.Error("expected ReceiverTLSEnabled true")
	}
	if cfg.ReceiverTLSCertFile != "/etc/certs/server.crt" {
		t.Errorf("expected ReceiverTLSCertFile, got '%s'", cfg.ReceiverTLSCertFile)
	}
	if cfg.ReceiverTLSKeyFile != "/etc/certs/server.key" {
		t.Errorf("expected ReceiverTLSKeyFile, got '%s'", cfg.ReceiverTLSKeyFile)
	}
	if cfg.ReceiverTLSCAFile != "/etc/certs/ca.crt" {
		t.Errorf("expected ReceiverTLSCAFile, got '%s'", cfg.ReceiverTLSCAFile)
	}
	if !cfg.ReceiverTLSClientAuth {
		t.Error("expected ReceiverTLSClientAuth true")
	}
}

func TestParseFlagsAuth(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-receiver-auth-enabled",
		"-receiver-auth-bearer-token", "secret",
		"-receiver-auth-basic-username", "user",
		"-receiver-auth-basic-password", "pass",
	}

	cfg := ParseFlags()

	if !cfg.ReceiverAuthEnabled {
		t.Error("expected ReceiverAuthEnabled true")
	}
	if cfg.ReceiverAuthBearerToken != "secret" {
		t.Errorf("expected ReceiverAuthBearerToken 'secret', got '%s'", cfg.ReceiverAuthBearerToken)
	}
	if cfg.ReceiverAuthBasicUsername != "user" {
		t.Errorf("expected ReceiverAuthBasicUsername 'user', got '%s'", cfg.ReceiverAuthBasicUsername)
	}
	if cfg.ReceiverAuthBasicPassword != "pass" {
		t.Errorf("expected ReceiverAuthBasicPassword 'pass', got '%s'", cfg.ReceiverAuthBasicPassword)
	}
}

func TestParseFlagsExporterTLS(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-exporter-tls-enabled",
		"-exporter-tls-cert", "/etc/certs/client.crt",
		"-exporter-tls-key", "/etc/certs/client.key",
		"-exporter-tls-ca", "/etc/certs/ca.crt",
		"-exporter-tls-skip-verify",
		"-exporter-tls-server-name", "example.com",
	}

	cfg := ParseFlags()

	if !cfg.ExporterTLSEnabled {
		t.Error("expected ExporterTLSEnabled true")
	}
	if cfg.ExporterTLSCertFile != "/etc/certs/client.crt" {
		t.Errorf("expected ExporterTLSCertFile, got '%s'", cfg.ExporterTLSCertFile)
	}
	if cfg.ExporterTLSKeyFile != "/etc/certs/client.key" {
		t.Errorf("expected ExporterTLSKeyFile, got '%s'", cfg.ExporterTLSKeyFile)
	}
	if cfg.ExporterTLSCAFile != "/etc/certs/ca.crt" {
		t.Errorf("expected ExporterTLSCAFile, got '%s'", cfg.ExporterTLSCAFile)
	}
	if !cfg.ExporterTLSInsecureSkipVerify {
		t.Error("expected ExporterTLSInsecureSkipVerify true")
	}
	if cfg.ExporterTLSServerName != "example.com" {
		t.Errorf("expected ExporterTLSServerName 'example.com', got '%s'", cfg.ExporterTLSServerName)
	}
}

func TestParseFlagsExporterAuth(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-exporter-auth-bearer-token", "token",
		"-exporter-auth-basic-username", "user",
		"-exporter-auth-basic-password", "pass",
		"-exporter-auth-headers", "X-Custom=value",
	}

	cfg := ParseFlags()

	if cfg.ExporterAuthBearerToken != "token" {
		t.Errorf("expected ExporterAuthBearerToken 'token', got '%s'", cfg.ExporterAuthBearerToken)
	}
	if cfg.ExporterAuthBasicUsername != "user" {
		t.Errorf("expected ExporterAuthBasicUsername 'user', got '%s'", cfg.ExporterAuthBasicUsername)
	}
	if cfg.ExporterAuthBasicPassword != "pass" {
		t.Errorf("expected ExporterAuthBasicPassword 'pass', got '%s'", cfg.ExporterAuthBasicPassword)
	}
	if cfg.ExporterAuthHeaders != "X-Custom=value" {
		t.Errorf("expected ExporterAuthHeaders 'X-Custom=value', got '%s'", cfg.ExporterAuthHeaders)
	}
}

func TestParseFlagsCompression(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-exporter-compression", "zstd",
		"-exporter-compression-level", "6",
	}

	cfg := ParseFlags()

	if cfg.ExporterCompression != "zstd" {
		t.Errorf("expected ExporterCompression 'zstd', got '%s'", cfg.ExporterCompression)
	}
	if cfg.ExporterCompressionLevel != 6 {
		t.Errorf("expected ExporterCompressionLevel 6, got %d", cfg.ExporterCompressionLevel)
	}
}

func TestParseFlagsHTTPClient(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-exporter-max-idle-conns", "200",
		"-exporter-max-idle-conns-per-host", "50",
		"-exporter-max-conns-per-host", "100",
		"-exporter-idle-conn-timeout", "2m",
		"-exporter-disable-keep-alives",
		"-exporter-force-http2",
		"-exporter-http2-read-idle-timeout", "30s",
		"-exporter-http2-ping-timeout", "10s",
	}

	cfg := ParseFlags()

	if cfg.ExporterMaxIdleConns != 200 {
		t.Errorf("expected ExporterMaxIdleConns 200, got %d", cfg.ExporterMaxIdleConns)
	}
	if cfg.ExporterMaxIdleConnsPerHost != 50 {
		t.Errorf("expected ExporterMaxIdleConnsPerHost 50, got %d", cfg.ExporterMaxIdleConnsPerHost)
	}
	if cfg.ExporterMaxConnsPerHost != 100 {
		t.Errorf("expected ExporterMaxConnsPerHost 100, got %d", cfg.ExporterMaxConnsPerHost)
	}
	if cfg.ExporterIdleConnTimeout != 2*time.Minute {
		t.Errorf("expected ExporterIdleConnTimeout 2m, got %v", cfg.ExporterIdleConnTimeout)
	}
	if !cfg.ExporterDisableKeepAlives {
		t.Error("expected ExporterDisableKeepAlives true")
	}
	if !cfg.ExporterForceHTTP2 {
		t.Error("expected ExporterForceHTTP2 true")
	}
	if cfg.ExporterHTTP2ReadIdleTimeout != 30*time.Second {
		t.Errorf("expected ExporterHTTP2ReadIdleTimeout 30s, got %v", cfg.ExporterHTTP2ReadIdleTimeout)
	}
	if cfg.ExporterHTTP2PingTimeout != 10*time.Second {
		t.Errorf("expected ExporterHTTP2PingTimeout 10s, got %v", cfg.ExporterHTTP2PingTimeout)
	}
}

func TestParseFlagsReceiverServer(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-receiver-max-request-body-size", "10485760",
		"-receiver-read-timeout", "30s",
		"-receiver-read-header-timeout", "10s",
		"-receiver-write-timeout", "60s",
		"-receiver-idle-timeout", "120s",
		"-receiver-keep-alives-enabled=false",
	}

	cfg := ParseFlags()

	if cfg.ReceiverMaxRequestBodySize != 10485760 {
		t.Errorf("expected ReceiverMaxRequestBodySize 10485760, got %d", cfg.ReceiverMaxRequestBodySize)
	}
	if cfg.ReceiverReadTimeout != 30*time.Second {
		t.Errorf("expected ReceiverReadTimeout 30s, got %v", cfg.ReceiverReadTimeout)
	}
	if cfg.ReceiverReadHeaderTimeout != 10*time.Second {
		t.Errorf("expected ReceiverReadHeaderTimeout 10s, got %v", cfg.ReceiverReadHeaderTimeout)
	}
	if cfg.ReceiverWriteTimeout != 60*time.Second {
		t.Errorf("expected ReceiverWriteTimeout 60s, got %v", cfg.ReceiverWriteTimeout)
	}
	if cfg.ReceiverIdleTimeout != 120*time.Second {
		t.Errorf("expected ReceiverIdleTimeout 120s, got %v", cfg.ReceiverIdleTimeout)
	}
	if cfg.ReceiverKeepAlivesEnabled {
		t.Error("expected ReceiverKeepAlivesEnabled false")
	}
}

func TestParseFlagsQueue(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-queue-enabled",
		"-queue-path", "/data/queue",
		"-queue-max-size", "5000",
		"-queue-max-bytes", "500000000",
		"-queue-retry-interval", "10s",
		"-queue-max-retry-delay", "10m",
		"-queue-full-behavior", "drop_newest",
		"-queue-target-utilization", "0.9",
		"-queue-adaptive-enabled",
		"-queue-compact-threshold", "0.3",
	}

	cfg := ParseFlags()

	if !cfg.QueueEnabled {
		t.Error("expected QueueEnabled true")
	}
	if cfg.QueuePath != "/data/queue" {
		t.Errorf("expected QueuePath '/data/queue', got '%s'", cfg.QueuePath)
	}
	if cfg.QueueMaxSize != 5000 {
		t.Errorf("expected QueueMaxSize 5000, got %d", cfg.QueueMaxSize)
	}
	if cfg.QueueMaxBytes != 500000000 {
		t.Errorf("expected QueueMaxBytes 500000000, got %d", cfg.QueueMaxBytes)
	}
	if cfg.QueueRetryInterval != 10*time.Second {
		t.Errorf("expected QueueRetryInterval 10s, got %v", cfg.QueueRetryInterval)
	}
	if cfg.QueueMaxRetryDelay != 10*time.Minute {
		t.Errorf("expected QueueMaxRetryDelay 10m, got %v", cfg.QueueMaxRetryDelay)
	}
	if cfg.QueueFullBehavior != "drop_newest" {
		t.Errorf("expected QueueFullBehavior 'drop_newest', got '%s'", cfg.QueueFullBehavior)
	}
}

func TestParseFlagsSharding(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-sharding-enabled",
		"-sharding-headless-service", "vminsert-headless.monitoring.svc:8428",
		"-sharding-dns-refresh-interval", "60s",
		"-sharding-dns-timeout", "10s",
		"-sharding-labels", "service,env",
		"-sharding-virtual-nodes", "200",
		"-sharding-fallback-on-empty",
	}

	cfg := ParseFlags()

	if !cfg.ShardingEnabled {
		t.Error("expected ShardingEnabled true")
	}
	if cfg.ShardingHeadlessService != "vminsert-headless.monitoring.svc:8428" {
		t.Errorf("expected ShardingHeadlessService, got '%s'", cfg.ShardingHeadlessService)
	}
	if cfg.ShardingDNSRefreshInterval != 60*time.Second {
		t.Errorf("expected ShardingDNSRefreshInterval 60s, got %v", cfg.ShardingDNSRefreshInterval)
	}
	if cfg.ShardingDNSTimeout != 10*time.Second {
		t.Errorf("expected ShardingDNSTimeout 10s, got %v", cfg.ShardingDNSTimeout)
	}
	if cfg.ShardingLabels != "service,env" {
		t.Errorf("expected ShardingLabels 'service,env', got '%s'", cfg.ShardingLabels)
	}
	if cfg.ShardingVirtualNodes != 200 {
		t.Errorf("expected ShardingVirtualNodes 200, got %d", cfg.ShardingVirtualNodes)
	}
}

func TestParseFlagsPRWReceiver(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-prw-listen", ":9091",
		"-prw-receiver-version", "2.0",
		"-prw-receiver-tls-enabled",
		"-prw-receiver-tls-cert", "/etc/certs/server.crt",
		"-prw-receiver-tls-key", "/etc/certs/server.key",
		"-prw-receiver-tls-ca", "/etc/certs/ca.crt",
		"-prw-receiver-tls-client-auth",
		"-prw-receiver-auth-enabled",
		"-prw-receiver-auth-bearer-token", "prw-token",
		"-prw-receiver-max-body-size", "10485760",
		"-prw-receiver-read-timeout", "2m",
		"-prw-receiver-write-timeout", "1m",
	}

	cfg := ParseFlags()

	if cfg.PRWListenAddr != ":9091" {
		t.Errorf("expected PRWListenAddr ':9091', got '%s'", cfg.PRWListenAddr)
	}
	if cfg.PRWReceiverVersion != "2.0" {
		t.Errorf("expected PRWReceiverVersion '2.0', got '%s'", cfg.PRWReceiverVersion)
	}
	if !cfg.PRWReceiverTLSEnabled {
		t.Error("expected PRWReceiverTLSEnabled true")
	}
	if cfg.PRWReceiverTLSCertFile != "/etc/certs/server.crt" {
		t.Errorf("expected PRWReceiverTLSCertFile, got '%s'", cfg.PRWReceiverTLSCertFile)
	}
	if !cfg.PRWReceiverTLSClientAuth {
		t.Error("expected PRWReceiverTLSClientAuth true")
	}
	if !cfg.PRWReceiverAuthEnabled {
		t.Error("expected PRWReceiverAuthEnabled true")
	}
	if cfg.PRWReceiverAuthBearerToken != "prw-token" {
		t.Errorf("expected PRWReceiverAuthBearerToken 'prw-token', got '%s'", cfg.PRWReceiverAuthBearerToken)
	}
	if cfg.PRWReceiverMaxRequestBodySize != 10485760 {
		t.Errorf("expected PRWReceiverMaxRequestBodySize 10485760, got %d", cfg.PRWReceiverMaxRequestBodySize)
	}
	if cfg.PRWReceiverReadTimeout != 2*time.Minute {
		t.Errorf("expected PRWReceiverReadTimeout 2m, got %v", cfg.PRWReceiverReadTimeout)
	}
	if cfg.PRWReceiverWriteTimeout != 1*time.Minute {
		t.Errorf("expected PRWReceiverWriteTimeout 1m, got %v", cfg.PRWReceiverWriteTimeout)
	}
}

func TestParseFlagsPRWExporter(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-prw-exporter-endpoint", "http://victoria:8428",
		"-prw-exporter-version", "1.0",
		"-prw-exporter-timeout", "45s",
		"-prw-exporter-tls-enabled",
		"-prw-exporter-tls-cert", "/etc/certs/client.crt",
		"-prw-exporter-tls-key", "/etc/certs/client.key",
		"-prw-exporter-tls-ca", "/etc/certs/ca.crt",
		"-prw-exporter-tls-skip-verify",
		"-prw-exporter-auth-bearer-token", "export-token",
		"-prw-exporter-vm-mode",
		"-prw-exporter-vm-compression", "zstd",
		"-prw-exporter-vm-short-endpoint",
		"-prw-exporter-vm-extra-labels", "env=prod",
	}

	cfg := ParseFlags()

	if cfg.PRWExporterEndpoint != "http://victoria:8428" {
		t.Errorf("expected PRWExporterEndpoint, got '%s'", cfg.PRWExporterEndpoint)
	}
	if cfg.PRWExporterVersion != "1.0" {
		t.Errorf("expected PRWExporterVersion '1.0', got '%s'", cfg.PRWExporterVersion)
	}
	if cfg.PRWExporterTimeout != 45*time.Second {
		t.Errorf("expected PRWExporterTimeout 45s, got %v", cfg.PRWExporterTimeout)
	}
	if !cfg.PRWExporterTLSEnabled {
		t.Error("expected PRWExporterTLSEnabled true")
	}
	if !cfg.PRWExporterTLSSkipVerify {
		t.Error("expected PRWExporterTLSSkipVerify true")
	}
	if cfg.PRWExporterAuthBearerToken != "export-token" {
		t.Errorf("expected PRWExporterAuthBearerToken, got '%s'", cfg.PRWExporterAuthBearerToken)
	}
	if !cfg.PRWExporterVMMode {
		t.Error("expected PRWExporterVMMode true")
	}
	if cfg.PRWExporterVMCompression != "zstd" {
		t.Errorf("expected PRWExporterVMCompression 'zstd', got '%s'", cfg.PRWExporterVMCompression)
	}
	if !cfg.PRWExporterVMShortEndpoint {
		t.Error("expected PRWExporterVMShortEndpoint true")
	}
	if cfg.PRWExporterVMExtraLabels != "env=prod" {
		t.Errorf("expected PRWExporterVMExtraLabels 'env=prod', got '%s'", cfg.PRWExporterVMExtraLabels)
	}
}

func TestParseFlagsPRWBuffer(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-prw-buffer-size", "20000",
		"-prw-batch-size", "2000",
		"-prw-flush-interval", "10s",
	}

	cfg := ParseFlags()

	if cfg.PRWBufferSize != 20000 {
		t.Errorf("expected PRWBufferSize 20000, got %d", cfg.PRWBufferSize)
	}
	if cfg.PRWBatchSize != 2000 {
		t.Errorf("expected PRWBatchSize 2000, got %d", cfg.PRWBatchSize)
	}
	if cfg.PRWFlushInterval != 10*time.Second {
		t.Errorf("expected PRWFlushInterval 10s, got %v", cfg.PRWFlushInterval)
	}
}

func TestParseFlagsPRWQueue(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-prw-queue-enabled",
		"-prw-queue-path", "/data/prw-queue",
		"-prw-queue-max-size", "5000",
		"-prw-queue-max-bytes", "500000000",
		"-prw-queue-retry-interval", "10s",
		"-prw-queue-max-retry-delay", "3m",
	}

	cfg := ParseFlags()

	if !cfg.PRWQueueEnabled {
		t.Error("expected PRWQueueEnabled true")
	}
	if cfg.PRWQueuePath != "/data/prw-queue" {
		t.Errorf("expected PRWQueuePath '/data/prw-queue', got '%s'", cfg.PRWQueuePath)
	}
	if cfg.PRWQueueMaxSize != 5000 {
		t.Errorf("expected PRWQueueMaxSize 5000, got %d", cfg.PRWQueueMaxSize)
	}
	if cfg.PRWQueueMaxBytes != 500000000 {
		t.Errorf("expected PRWQueueMaxBytes 500000000, got %d", cfg.PRWQueueMaxBytes)
	}
	if cfg.PRWQueueRetryInterval != 10*time.Second {
		t.Errorf("expected PRWQueueRetryInterval 10s, got %v", cfg.PRWQueueRetryInterval)
	}
	if cfg.PRWQueueMaxRetryDelay != 3*time.Minute {
		t.Errorf("expected PRWQueueMaxRetryDelay 3m, got %v", cfg.PRWQueueMaxRetryDelay)
	}
}

func TestParseFlagsPRWLimits(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-prw-limits-enabled",
		"-prw-limits-config", "/etc/limits.yaml",
		"-prw-limits-dry-run=false",
	}

	cfg := ParseFlags()

	if !cfg.PRWLimitsEnabled {
		t.Error("expected PRWLimitsEnabled true")
	}
	if cfg.PRWLimitsConfig != "/etc/limits.yaml" {
		t.Errorf("expected PRWLimitsConfig '/etc/limits.yaml', got '%s'", cfg.PRWLimitsConfig)
	}
	if cfg.PRWLimitsDryRun {
		t.Error("expected PRWLimitsDryRun false")
	}
}
