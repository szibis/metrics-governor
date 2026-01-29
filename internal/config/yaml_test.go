package config

import (
	"os"
	"testing"
	"time"
)

func TestParseYAMLMinimal(t *testing.T) {
	yaml := `
exporter:
  endpoint: "test:4317"
`
	cfg, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("failed to parse yaml: %v", err)
	}

	// Verify defaults applied
	if cfg.Receiver.GRPC.Address != ":4317" {
		t.Errorf("expected default GRPC address ':4317', got %s", cfg.Receiver.GRPC.Address)
	}
	if cfg.Receiver.HTTP.Address != ":4318" {
		t.Errorf("expected default HTTP address ':4318', got %s", cfg.Receiver.HTTP.Address)
	}
	if cfg.Buffer.Size != 10000 {
		t.Errorf("expected default buffer size 10000, got %d", cfg.Buffer.Size)
	}

	// Verify override applied
	if cfg.Exporter.Endpoint != "test:4317" {
		t.Errorf("expected endpoint 'test:4317', got %s", cfg.Exporter.Endpoint)
	}
}

func TestParseYAMLFull(t *testing.T) {
	yaml := `
receiver:
  grpc:
    address: ":5317"
  http:
    address: ":5318"
    server:
      max_request_body_size: 10485760
      read_timeout: "30s"
      read_header_timeout: "1m"
      write_timeout: "1m"
      idle_timeout: "2m"
      keep_alives_enabled: false
  tls:
    enabled: true
    cert_file: "/etc/tls/server.crt"
    key_file: "/etc/tls/server.key"
    ca_file: "/etc/tls/ca.crt"
    client_auth: true
  auth:
    enabled: true
    bearer_token: "secret-token"

exporter:
  endpoint: "otel-collector:4317"
  protocol: "http"
  insecure: false
  timeout: "60s"
  tls:
    enabled: true
    cert_file: "/etc/tls/client.crt"
    key_file: "/etc/tls/client.key"
    ca_file: "/etc/tls/ca.crt"
    skip_verify: true
    server_name: "otel.example.com"
  auth:
    bearer_token: "exporter-token"
    basic_username: "user"
    basic_password: "pass"
    headers:
      X-Custom-Header: "value"
  compression:
    type: "gzip"
    level: 6
  http_client:
    max_idle_conns: 200
    max_idle_conns_per_host: 50
    max_conns_per_host: 100
    idle_conn_timeout: "2m"
    disable_keep_alives: true
    force_http2: true
    http2_read_idle_timeout: "30s"
    http2_ping_timeout: "15s"

buffer:
  size: 50000
  batch_size: 2000
  flush_interval: "10s"

stats:
  address: ":8080"
  labels:
    - service
    - env
    - cluster

limits:
  dry_run: false
`
	cfg, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("failed to parse yaml: %v", err)
	}

	// Verify receiver settings
	if cfg.Receiver.GRPC.Address != ":5317" {
		t.Errorf("expected GRPC address ':5317', got %s", cfg.Receiver.GRPC.Address)
	}
	if cfg.Receiver.HTTP.Address != ":5318" {
		t.Errorf("expected HTTP address ':5318', got %s", cfg.Receiver.HTTP.Address)
	}
	if cfg.Receiver.HTTP.Server.MaxRequestBodySize != 10485760 {
		t.Errorf("expected max request body size 10485760, got %d", cfg.Receiver.HTTP.Server.MaxRequestBodySize)
	}
	if *cfg.Receiver.HTTP.Server.KeepAlivesEnabled != false {
		t.Errorf("expected keep alives disabled")
	}
	if !cfg.Receiver.TLS.Enabled {
		t.Error("expected receiver TLS enabled")
	}
	if !cfg.Receiver.TLS.ClientAuth {
		t.Error("expected receiver TLS client auth enabled")
	}
	if !cfg.Receiver.Auth.Enabled {
		t.Error("expected receiver auth enabled")
	}

	// Verify exporter settings
	if cfg.Exporter.Endpoint != "otel-collector:4317" {
		t.Errorf("expected endpoint 'otel-collector:4317', got %s", cfg.Exporter.Endpoint)
	}
	if cfg.Exporter.Protocol != "http" {
		t.Errorf("expected protocol 'http', got %s", cfg.Exporter.Protocol)
	}
	if *cfg.Exporter.Insecure != false {
		t.Error("expected insecure false")
	}
	if time.Duration(cfg.Exporter.Timeout) != 60*time.Second {
		t.Errorf("expected timeout 60s, got %v", cfg.Exporter.Timeout)
	}
	if cfg.Exporter.Compression.Type != "gzip" {
		t.Errorf("expected compression type 'gzip', got %s", cfg.Exporter.Compression.Type)
	}
	if cfg.Exporter.Compression.Level != 6 {
		t.Errorf("expected compression level 6, got %d", cfg.Exporter.Compression.Level)
	}
	if cfg.Exporter.HTTPClient.MaxIdleConns != 200 {
		t.Errorf("expected max idle conns 200, got %d", cfg.Exporter.HTTPClient.MaxIdleConns)
	}
	if cfg.Exporter.Auth.Headers["X-Custom-Header"] != "value" {
		t.Errorf("expected custom header, got %v", cfg.Exporter.Auth.Headers)
	}

	// Verify buffer settings
	if cfg.Buffer.Size != 50000 {
		t.Errorf("expected buffer size 50000, got %d", cfg.Buffer.Size)
	}
	if cfg.Buffer.BatchSize != 2000 {
		t.Errorf("expected batch size 2000, got %d", cfg.Buffer.BatchSize)
	}

	// Verify stats settings
	if cfg.Stats.Address != ":8080" {
		t.Errorf("expected stats address ':8080', got %s", cfg.Stats.Address)
	}
	if len(cfg.Stats.Labels) != 3 {
		t.Errorf("expected 3 labels, got %d", len(cfg.Stats.Labels))
	}

	// Verify limits settings
	if *cfg.Limits.DryRun != false {
		t.Error("expected dry run false")
	}
}

func TestParseYAMLDurations(t *testing.T) {
	yaml := `
exporter:
  timeout: "2m30s"
buffer:
  flush_interval: "10s"
`
	cfg, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("failed to parse yaml: %v", err)
	}

	if time.Duration(cfg.Exporter.Timeout) != 150*time.Second {
		t.Errorf("expected 2m30s (150s), got %v", cfg.Exporter.Timeout)
	}
	if time.Duration(cfg.Buffer.FlushInterval) != 10*time.Second {
		t.Errorf("expected 10s, got %v", cfg.Buffer.FlushInterval)
	}
}

func TestParseYAMLInvalid(t *testing.T) {
	yaml := `
this is not: valid: yaml: syntax
`
	_, err := ParseYAML([]byte(yaml))
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestParseYAMLInvalidDuration(t *testing.T) {
	yaml := `
exporter:
  timeout: "not-a-duration"
`
	_, err := ParseYAML([]byte(yaml))
	if err == nil {
		t.Error("expected error for invalid duration")
	}
}

func TestYAMLConfigToConfig(t *testing.T) {
	yaml := `
receiver:
  grpc:
    address: ":5317"
  http:
    address: ":5318"
exporter:
  endpoint: "test:4317"
  protocol: "http"
  auth:
    headers:
      X-Header-One: "value1"
      X-Header-Two: "value2"
buffer:
  size: 20000
  batch_size: 500
  flush_interval: "15s"
stats:
  address: ":8080"
  labels:
    - service
    - env
limits:
  dry_run: false
`
	yamlCfg, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("failed to parse yaml: %v", err)
	}

	cfg := yamlCfg.ToConfig()

	if cfg.GRPCListenAddr != ":5317" {
		t.Errorf("expected GRPCListenAddr ':5317', got %s", cfg.GRPCListenAddr)
	}
	if cfg.HTTPListenAddr != ":5318" {
		t.Errorf("expected HTTPListenAddr ':5318', got %s", cfg.HTTPListenAddr)
	}
	if cfg.ExporterEndpoint != "test:4317" {
		t.Errorf("expected ExporterEndpoint 'test:4317', got %s", cfg.ExporterEndpoint)
	}
	if cfg.ExporterProtocol != "http" {
		t.Errorf("expected ExporterProtocol 'http', got %s", cfg.ExporterProtocol)
	}
	if cfg.BufferSize != 20000 {
		t.Errorf("expected BufferSize 20000, got %d", cfg.BufferSize)
	}
	if cfg.MaxBatchSize != 500 {
		t.Errorf("expected MaxBatchSize 500, got %d", cfg.MaxBatchSize)
	}
	if cfg.FlushInterval != 15*time.Second {
		t.Errorf("expected FlushInterval 15s, got %v", cfg.FlushInterval)
	}
	if cfg.StatsAddr != ":8080" {
		t.Errorf("expected StatsAddr ':8080', got %s", cfg.StatsAddr)
	}
	if cfg.StatsLabels != "service,env" {
		t.Errorf("expected StatsLabels 'service,env', got %s", cfg.StatsLabels)
	}
	if cfg.LimitsDryRun != false {
		t.Errorf("expected LimitsDryRun false, got %v", cfg.LimitsDryRun)
	}
	// Check headers are converted to comma-separated format
	if cfg.ExporterAuthHeaders == "" {
		t.Error("expected ExporterAuthHeaders to be set")
	}
}

func TestLoadYAMLFile(t *testing.T) {
	// Create a temporary config file
	content := `
exporter:
  endpoint: "file-test:4317"
buffer:
  size: 5000
`
	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatalf("failed to close temp file: %v", err)
	}

	cfg, err := LoadYAML(tmpfile.Name())
	if err != nil {
		t.Fatalf("failed to load yaml file: %v", err)
	}

	if cfg.Exporter.Endpoint != "file-test:4317" {
		t.Errorf("expected endpoint 'file-test:4317', got %s", cfg.Exporter.Endpoint)
	}
	if cfg.Buffer.Size != 5000 {
		t.Errorf("expected buffer size 5000, got %d", cfg.Buffer.Size)
	}
}

func TestLoadYAMLFileNotFound(t *testing.T) {
	_, err := LoadYAML("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestApplyDefaults(t *testing.T) {
	cfg := &YAMLConfig{}
	cfg.ApplyDefaults()

	if cfg.Receiver.GRPC.Address != ":4317" {
		t.Errorf("expected default GRPC address ':4317', got %s", cfg.Receiver.GRPC.Address)
	}
	if cfg.Receiver.HTTP.Address != ":4318" {
		t.Errorf("expected default HTTP address ':4318', got %s", cfg.Receiver.HTTP.Address)
	}
	if cfg.Exporter.Endpoint != "localhost:4317" {
		t.Errorf("expected default endpoint 'localhost:4317', got %s", cfg.Exporter.Endpoint)
	}
	if cfg.Exporter.Protocol != "grpc" {
		t.Errorf("expected default protocol 'grpc', got %s", cfg.Exporter.Protocol)
	}
	if *cfg.Exporter.Insecure != true {
		t.Error("expected default insecure true")
	}
	if cfg.Buffer.Size != 10000 {
		t.Errorf("expected default buffer size 10000, got %d", cfg.Buffer.Size)
	}
	if cfg.Buffer.BatchSize != 1000 {
		t.Errorf("expected default batch size 1000, got %d", cfg.Buffer.BatchSize)
	}
	if cfg.Stats.Address != ":9090" {
		t.Errorf("expected default stats address ':9090', got %s", cfg.Stats.Address)
	}
	if *cfg.Limits.DryRun != true {
		t.Error("expected default dry run true")
	}
}

func TestHeadersMapToString(t *testing.T) {
	headers := map[string]string{
		"X-Header-One": "value1",
		"X-Header-Two": "value2",
	}

	result := headersMapToString(headers)
	if result == "" {
		t.Error("expected non-empty result")
	}
	// Note: map iteration order is not guaranteed, so we just check it contains something
	if len(result) < 10 {
		t.Errorf("expected longer result, got %s", result)
	}
}

func TestHeadersMapToStringEmpty(t *testing.T) {
	result := headersMapToString(nil)
	if result != "" {
		t.Errorf("expected empty string for nil map, got %s", result)
	}

	result = headersMapToString(map[string]string{})
	if result != "" {
		t.Errorf("expected empty string for empty map, got %s", result)
	}
}
