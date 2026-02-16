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
  cardinality_threshold: 100
  max_label_combinations: 5000

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
	if cfg.Stats.CardinalityThreshold != 100 {
		t.Errorf("expected cardinality_threshold 100, got %d", cfg.Stats.CardinalityThreshold)
	}
	if cfg.Stats.MaxLabelCombinations != 5000 {
		t.Errorf("expected max_label_combinations 5000, got %d", cfg.Stats.MaxLabelCombinations)
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
  cardinality_threshold: 50
  max_label_combinations: 2000
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
	if cfg.StatsCardinalityThreshold != 50 {
		t.Errorf("expected StatsCardinalityThreshold 50, got %d", cfg.StatsCardinalityThreshold)
	}
	if cfg.StatsMaxLabelCombinations != 2000 {
		t.Errorf("expected StatsMaxLabelCombinations 2000, got %d", cfg.StatsMaxLabelCombinations)
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
	if cfg.Buffer.BatchSize != 5000 {
		t.Errorf("expected default batch size 5000, got %d", cfg.Buffer.BatchSize)
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

// Tests for new resilience settings (backoff, circuit breaker, memory)

func TestParseYAMLQueueBackoff(t *testing.T) {
	yaml := `
exporter:
  endpoint: "test:4317"
  queue:
    enabled: true
    path: "/data/queue"
    backoff:
      enabled: true
      multiplier: 3.0
`
	cfg, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("failed to parse yaml: %v", err)
	}

	if !*cfg.Exporter.Queue.Enabled {
		t.Error("expected queue enabled")
	}
	if !*cfg.Exporter.Queue.Backoff.Enabled {
		t.Error("expected backoff enabled")
	}
	if cfg.Exporter.Queue.Backoff.Multiplier != 3.0 {
		t.Errorf("expected multiplier 3.0, got %f", cfg.Exporter.Queue.Backoff.Multiplier)
	}
}

func TestParseYAMLQueueCircuitBreaker(t *testing.T) {
	yaml := `
exporter:
  endpoint: "test:4317"
  queue:
    enabled: true
    circuit_breaker:
      enabled: true
      threshold: 15
      reset_timeout: "45s"
`
	cfg, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("failed to parse yaml: %v", err)
	}

	if !*cfg.Exporter.Queue.CircuitBreaker.Enabled {
		t.Error("expected circuit breaker enabled")
	}
	if cfg.Exporter.Queue.CircuitBreaker.Threshold != 15 {
		t.Errorf("expected threshold 15, got %d", cfg.Exporter.Queue.CircuitBreaker.Threshold)
	}
	if time.Duration(cfg.Exporter.Queue.CircuitBreaker.ResetTimeout) != 45*time.Second {
		t.Errorf("expected reset timeout 45s, got %v", cfg.Exporter.Queue.CircuitBreaker.ResetTimeout)
	}
}

func TestParseYAMLMemoryLimitRatio(t *testing.T) {
	yaml := `
exporter:
  endpoint: "test:4317"
memory:
  limit_ratio: 0.85
`
	cfg, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("failed to parse yaml: %v", err)
	}

	if cfg.Memory.LimitRatio != 0.85 {
		t.Errorf("expected limit ratio 0.85, got %f", cfg.Memory.LimitRatio)
	}
}

func TestParseYAMLFastQueueSettings(t *testing.T) {
	yaml := `
exporter:
  endpoint: "test:4317"
  queue:
    enabled: true
    inmemory_blocks: 512
    chunk_size: 268435456
    meta_sync_interval: "2s"
    stale_flush_interval: "10s"
`
	cfg, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("failed to parse yaml: %v", err)
	}

	if cfg.Exporter.Queue.InmemoryBlocks != 512 {
		t.Errorf("expected inmemory_blocks 512, got %d", cfg.Exporter.Queue.InmemoryBlocks)
	}
	if cfg.Exporter.Queue.ChunkSize != 268435456 {
		t.Errorf("expected chunk_size 268435456, got %d", cfg.Exporter.Queue.ChunkSize)
	}
	if time.Duration(cfg.Exporter.Queue.MetaSyncInterval) != 2*time.Second {
		t.Errorf("expected meta_sync_interval 2s, got %v", cfg.Exporter.Queue.MetaSyncInterval)
	}
	if time.Duration(cfg.Exporter.Queue.StaleFlushInterval) != 10*time.Second {
		t.Errorf("expected stale_flush_interval 10s, got %v", cfg.Exporter.Queue.StaleFlushInterval)
	}
}

func TestApplyDefaultsBackoff(t *testing.T) {
	cfg := &YAMLConfig{}
	cfg.ApplyDefaults()

	if cfg.Exporter.Queue.Backoff.Enabled == nil {
		t.Fatal("expected backoff enabled to be set")
	}
	if !*cfg.Exporter.Queue.Backoff.Enabled {
		t.Error("expected default backoff enabled true")
	}
	if cfg.Exporter.Queue.Backoff.Multiplier != 2.0 {
		t.Errorf("expected default multiplier 2.0, got %f", cfg.Exporter.Queue.Backoff.Multiplier)
	}
}

func TestApplyDefaultsCircuitBreaker(t *testing.T) {
	cfg := &YAMLConfig{}
	cfg.ApplyDefaults()

	if cfg.Exporter.Queue.CircuitBreaker.Enabled == nil {
		t.Fatal("expected circuit breaker enabled to be set")
	}
	if !*cfg.Exporter.Queue.CircuitBreaker.Enabled {
		t.Error("expected default circuit breaker enabled true")
	}
	if cfg.Exporter.Queue.CircuitBreaker.Threshold != 5 {
		t.Errorf("expected default threshold 5, got %d", cfg.Exporter.Queue.CircuitBreaker.Threshold)
	}
	if time.Duration(cfg.Exporter.Queue.CircuitBreaker.ResetTimeout) != 30*time.Second {
		t.Errorf("expected default reset timeout 30s, got %v", cfg.Exporter.Queue.CircuitBreaker.ResetTimeout)
	}
}

func TestApplyDefaultsMemory(t *testing.T) {
	cfg := &YAMLConfig{}
	cfg.ApplyDefaults()

	if cfg.Memory.LimitRatio != 0.85 {
		t.Errorf("expected default limit ratio 0.85, got %f", cfg.Memory.LimitRatio)
	}
}

func TestApplyDefaultsFastQueue(t *testing.T) {
	cfg := &YAMLConfig{}
	cfg.ApplyDefaults()

	if cfg.Exporter.Queue.InmemoryBlocks != 2048 {
		t.Errorf("expected default inmemory_blocks 2048, got %d", cfg.Exporter.Queue.InmemoryBlocks)
	}
	if cfg.Exporter.Queue.ChunkSize != 512*1024*1024 {
		t.Errorf("expected default chunk_size 512MB, got %d", cfg.Exporter.Queue.ChunkSize)
	}
	if time.Duration(cfg.Exporter.Queue.MetaSyncInterval) != time.Second {
		t.Errorf("expected default meta_sync_interval 1s, got %v", cfg.Exporter.Queue.MetaSyncInterval)
	}
	if time.Duration(cfg.Exporter.Queue.StaleFlushInterval) != 30*time.Second {
		t.Errorf("expected default stale_flush_interval 30s, got %v", cfg.Exporter.Queue.StaleFlushInterval)
	}
	if cfg.Exporter.Queue.WriteBufferSize != 262144 {
		t.Errorf("expected default write_buffer_size 262144, got %d", cfg.Exporter.Queue.WriteBufferSize)
	}
	if cfg.Exporter.Queue.Compression != "snappy" {
		t.Errorf("expected default compression 'snappy', got '%s'", cfg.Exporter.Queue.Compression)
	}
}

func TestToConfigBackoffAndCircuitBreaker(t *testing.T) {
	yaml := `
exporter:
  endpoint: "test:4317"
  queue:
    enabled: true
    backoff:
      enabled: true
      multiplier: 2.5
    circuit_breaker:
      enabled: true
      threshold: 20
      reset_timeout: "1m"
memory:
  limit_ratio: 0.75
`
	yamlCfg, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("failed to parse yaml: %v", err)
	}

	cfg := yamlCfg.ToConfig()

	// Verify backoff settings
	if !cfg.QueueBackoffEnabled {
		t.Error("expected QueueBackoffEnabled true")
	}
	if cfg.QueueBackoffMultiplier != 2.5 {
		t.Errorf("expected QueueBackoffMultiplier 2.5, got %f", cfg.QueueBackoffMultiplier)
	}

	// Verify circuit breaker settings
	if !cfg.QueueCircuitBreakerEnabled {
		t.Error("expected QueueCircuitBreakerEnabled true")
	}
	if cfg.QueueCircuitBreakerThreshold != 20 {
		t.Errorf("expected QueueCircuitBreakerThreshold 20, got %d", cfg.QueueCircuitBreakerThreshold)
	}
	if cfg.QueueCircuitBreakerResetTimeout != time.Minute {
		t.Errorf("expected QueueCircuitBreakerResetTimeout 1m, got %v", cfg.QueueCircuitBreakerResetTimeout)
	}

	// Verify memory limit
	if cfg.MemoryLimitRatio != 0.75 {
		t.Errorf("expected MemoryLimitRatio 0.75, got %f", cfg.MemoryLimitRatio)
	}
}

func TestToConfigFastQueueSettings(t *testing.T) {
	yaml := `
exporter:
  endpoint: "test:4317"
  queue:
    enabled: true
    inmemory_blocks: 128
    chunk_size: 134217728
    meta_sync_interval: "500ms"
    stale_flush_interval: "3s"
`
	yamlCfg, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("failed to parse yaml: %v", err)
	}

	cfg := yamlCfg.ToConfig()

	if cfg.QueueInmemoryBlocks != 128 {
		t.Errorf("expected QueueInmemoryBlocks 128, got %d", cfg.QueueInmemoryBlocks)
	}
	if cfg.QueueChunkSize != 134217728 {
		t.Errorf("expected QueueChunkSize 134217728, got %d", cfg.QueueChunkSize)
	}
	if cfg.QueueMetaSyncInterval != 500*time.Millisecond {
		t.Errorf("expected QueueMetaSyncInterval 500ms, got %v", cfg.QueueMetaSyncInterval)
	}
	if cfg.QueueStaleFlushInterval != 3*time.Second {
		t.Errorf("expected QueueStaleFlushInterval 3s, got %v", cfg.QueueStaleFlushInterval)
	}
}

func TestParseYAMLFullResilienceConfig(t *testing.T) {
	yaml := `
exporter:
  endpoint: "backend:4317"
  protocol: "http"
  queue:
    enabled: true
    path: "/data/queue"
    max_size: 5000
    max_bytes: 536870912
    retry_interval: "10s"
    max_retry_delay: "2m"
    full_behavior: "drop_oldest"
    inmemory_blocks: 128
    chunk_size: 268435456
    meta_sync_interval: "2s"
    stale_flush_interval: "5s"
    backoff:
      enabled: true
      multiplier: 1.5
    circuit_breaker:
      enabled: true
      threshold: 5
      reset_timeout: "1m"
memory:
  limit_ratio: 0.8
`
	cfg, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("failed to parse yaml: %v", err)
	}

	// Verify all queue settings
	if !*cfg.Exporter.Queue.Enabled {
		t.Error("expected queue enabled")
	}
	if cfg.Exporter.Queue.Path != "/data/queue" {
		t.Errorf("expected path '/data/queue', got %s", cfg.Exporter.Queue.Path)
	}
	if cfg.Exporter.Queue.MaxSize != 5000 {
		t.Errorf("expected max_size 5000, got %d", cfg.Exporter.Queue.MaxSize)
	}
	if cfg.Exporter.Queue.MaxBytes != 536870912 {
		t.Errorf("expected max_bytes 536870912, got %d", cfg.Exporter.Queue.MaxBytes)
	}

	// Verify backoff
	if !*cfg.Exporter.Queue.Backoff.Enabled {
		t.Error("expected backoff enabled")
	}
	if cfg.Exporter.Queue.Backoff.Multiplier != 1.5 {
		t.Errorf("expected multiplier 1.5, got %f", cfg.Exporter.Queue.Backoff.Multiplier)
	}

	// Verify circuit breaker
	if !*cfg.Exporter.Queue.CircuitBreaker.Enabled {
		t.Error("expected circuit breaker enabled")
	}
	if cfg.Exporter.Queue.CircuitBreaker.Threshold != 5 {
		t.Errorf("expected threshold 5, got %d", cfg.Exporter.Queue.CircuitBreaker.Threshold)
	}

	// Verify memory
	if cfg.Memory.LimitRatio != 0.8 {
		t.Errorf("expected limit_ratio 0.8, got %f", cfg.Memory.LimitRatio)
	}
}

// --- ByteSize tests ---

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		input string
		want  int64
		err   bool
	}{
		// Raw integers
		{"0", 0, false},
		{"1024", 1024, false},
		{"262144", 262144, false},
		{"1073741824", 1073741824, false},

		// Ki (1024)
		{"1Ki", 1024, false},
		{"256Ki", 262144, false},
		{"512Ki", 524288, false},

		// Mi (1048576)
		{"1Mi", 1048576, false},
		{"8Mi", 8388608, false},
		{"256Mi", 268435456, false},
		{"512Mi", 536870912, false},

		// Gi (1073741824)
		{"1Gi", 1073741824, false},
		{"2Gi", 2147483648, false},
		{"10Gi", 10737418240, false},

		// Ti (1099511627776)
		{"1Ti", 1099511627776, false},

		// Fractional values
		{"1.5Gi", 1610612736, false},
		{"0.5Mi", 524288, false},
		{"2.5Mi", 2621440, false},

		// Whitespace
		{"  256Mi  ", 268435456, false},
		{"", 0, false},

		// Invalid
		{"abc", 0, true},
		{"Mi", 0, true},
		{"256MB", 0, true},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := ParseByteSize(tc.input)
			if tc.err {
				if err == nil {
					t.Fatalf("ParseByteSize(%q) expected error, got %d", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseByteSize(%q) unexpected error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("ParseByteSize(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestFormatByteSize(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0"},
		{512, "512"},
		{1024, "1Ki"},
		{262144, "256Ki"},
		{1048576, "1Mi"},
		{8388608, "8Mi"},
		{268435456, "256Mi"},
		{536870912, "512Mi"},
		{1073741824, "1Gi"},
		{2147483648, "2Gi"},
		{1099511627776, "1Ti"},
		// Non-aligned values stay as bytes
		{1000, "1000"},
		{1500000, "1500000"},
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			got := FormatByteSize(tc.input)
			if got != tc.want {
				t.Errorf("FormatByteSize(%d) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestByteSizeRoundTrip(t *testing.T) {
	// Verify ParseByteSize(FormatByteSize(x)) == x for common sizes
	sizes := []int64{
		0, 1024, 262144, 1048576, 8388608, 268435456,
		536870912, 1073741824, 2147483648, 1099511627776,
	}
	for _, s := range sizes {
		formatted := FormatByteSize(s)
		parsed, err := ParseByteSize(formatted)
		if err != nil {
			t.Fatalf("ParseByteSize(%q) error: %v (original: %d)", formatted, err, s)
		}
		if parsed != s {
			t.Errorf("round-trip failed: %d -> %q -> %d", s, formatted, parsed)
		}
	}
}

func TestByteSizeYAMLUnmarshal(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want int64
	}{
		{
			name: "raw_integer",
			yaml: `
exporter:
  endpoint: "test:4317"
  queue:
    max_bytes: 1073741824
`,
			want: 1073741824,
		},
		{
			name: "gi_notation",
			yaml: `
exporter:
  endpoint: "test:4317"
  queue:
    max_bytes: "1Gi"
`,
			want: 1073741824,
		},
		{
			name: "mi_notation",
			yaml: `
exporter:
  endpoint: "test:4317"
  queue:
    max_bytes: "256Mi"
`,
			want: 268435456,
		},
		{
			name: "ki_notation",
			yaml: `
exporter:
  endpoint: "test:4317"
  queue:
    write_buffer_size: "256Ki"
`,
			want: 262144,
		},
		{
			name: "fractional_gi",
			yaml: `
exporter:
  endpoint: "test:4317"
  queue:
    max_bytes: "1.5Gi"
`,
			want: 1610612736,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseYAML([]byte(tc.yaml))
			if err != nil {
				t.Fatalf("ParseYAML error: %v", err)
			}
			cfg.ApplyDefaults()

			switch tc.name {
			case "ki_notation":
				if int64(cfg.Exporter.Queue.WriteBufferSize) != tc.want {
					t.Errorf("WriteBufferSize = %d, want %d", cfg.Exporter.Queue.WriteBufferSize, tc.want)
				}
			default:
				if int64(cfg.Exporter.Queue.MaxBytes) != tc.want {
					t.Errorf("MaxBytes = %d, want %d", cfg.Exporter.Queue.MaxBytes, tc.want)
				}
			}
		})
	}
}

func TestByteSizeYAMLAllFields(t *testing.T) {
	yaml := `
receiver:
  http:
    server:
      max_request_body_size: "10Mi"
exporter:
  endpoint: "test:4317"
  queue:
    max_bytes: "2Gi"
    chunk_size: "512Mi"
    write_buffer_size: "256Ki"
buffer:
  max_batch_bytes: "8Mi"
`
	cfg, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseYAML error: %v", err)
	}
	cfg.ApplyDefaults()

	if int64(cfg.Receiver.HTTP.Server.MaxRequestBodySize) != 10*1048576 {
		t.Errorf("MaxRequestBodySize = %d, want %d", cfg.Receiver.HTTP.Server.MaxRequestBodySize, 10*1048576)
	}
	if int64(cfg.Exporter.Queue.MaxBytes) != 2*1073741824 {
		t.Errorf("MaxBytes = %d, want %d", cfg.Exporter.Queue.MaxBytes, 2*1073741824)
	}
	if int64(cfg.Exporter.Queue.ChunkSize) != 512*1048576 {
		t.Errorf("ChunkSize = %d, want %d", cfg.Exporter.Queue.ChunkSize, 512*1048576)
	}
	if int64(cfg.Exporter.Queue.WriteBufferSize) != 256*1024 {
		t.Errorf("WriteBufferSize = %d, want %d", cfg.Exporter.Queue.WriteBufferSize, 256*1024)
	}
	if int64(cfg.Buffer.MaxBatchBytes) != 8*1048576 {
		t.Errorf("MaxBatchBytes = %d, want %d", cfg.Buffer.MaxBatchBytes, 8*1048576)
	}
}

func TestParseYAMLStatsCardinalityFields(t *testing.T) {
	yaml := `
exporter:
  endpoint: "test:4317"
stats:
  address: ":9090"
  cardinality_threshold: 200
  max_label_combinations: 10000
`
	cfg, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("failed to parse yaml: %v", err)
	}

	if cfg.Stats.CardinalityThreshold != 200 {
		t.Errorf("expected cardinality_threshold 200, got %d", cfg.Stats.CardinalityThreshold)
	}
	if cfg.Stats.MaxLabelCombinations != 10000 {
		t.Errorf("expected max_label_combinations 10000, got %d", cfg.Stats.MaxLabelCombinations)
	}
}

func TestParseYAMLStatsCardinalityFieldsZero(t *testing.T) {
	yaml := `
exporter:
  endpoint: "test:4317"
stats:
  cardinality_threshold: 0
  max_label_combinations: 0
`
	cfg, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("failed to parse yaml: %v", err)
	}

	if cfg.Stats.CardinalityThreshold != 0 {
		t.Errorf("expected cardinality_threshold 0, got %d", cfg.Stats.CardinalityThreshold)
	}
	if cfg.Stats.MaxLabelCombinations != 0 {
		t.Errorf("expected max_label_combinations 0, got %d", cfg.Stats.MaxLabelCombinations)
	}
}

func TestToConfigStatsCardinalityFields(t *testing.T) {
	yaml := `
exporter:
  endpoint: "test:4317"
stats:
  cardinality_threshold: 75
  max_label_combinations: 3000
`
	yamlCfg, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("failed to parse yaml: %v", err)
	}

	cfg := yamlCfg.ToConfig()

	if cfg.StatsCardinalityThreshold != 75 {
		t.Errorf("expected StatsCardinalityThreshold 75, got %d", cfg.StatsCardinalityThreshold)
	}
	if cfg.StatsMaxLabelCombinations != 3000 {
		t.Errorf("expected StatsMaxLabelCombinations 3000, got %d", cfg.StatsMaxLabelCombinations)
	}
}

func TestByteSizeBackwardCompatRawIntegers(t *testing.T) {
	// Existing configs with raw byte integers must still work
	yaml := `
exporter:
  endpoint: "test:4317"
  queue:
    max_bytes: 536870912
    chunk_size: 268435456
    write_buffer_size: 262144
buffer:
  max_batch_bytes: 8388608
receiver:
  http:
    server:
      max_request_body_size: 10485760
`
	cfg, err := ParseYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseYAML error: %v", err)
	}
	cfg.ApplyDefaults()

	if int64(cfg.Exporter.Queue.MaxBytes) != 536870912 {
		t.Errorf("MaxBytes = %d, want 536870912", cfg.Exporter.Queue.MaxBytes)
	}
	if int64(cfg.Exporter.Queue.ChunkSize) != 268435456 {
		t.Errorf("ChunkSize = %d, want 268435456", cfg.Exporter.Queue.ChunkSize)
	}
	if int64(cfg.Exporter.Queue.WriteBufferSize) != 262144 {
		t.Errorf("WriteBufferSize = %d, want 262144", cfg.Exporter.Queue.WriteBufferSize)
	}
	if int64(cfg.Buffer.MaxBatchBytes) != 8388608 {
		t.Errorf("MaxBatchBytes = %d, want 8388608", cfg.Buffer.MaxBatchBytes)
	}
	if int64(cfg.Receiver.HTTP.Server.MaxRequestBodySize) != 10485760 {
		t.Errorf("MaxRequestBodySize = %d, want 10485760", cfg.Receiver.HTTP.Server.MaxRequestBodySize)
	}
}
