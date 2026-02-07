package config

import (
	"testing"
	"time"
)

// =============================================================================
// YAML round-trip: parse all new resilience fields from YAML
// =============================================================================

func TestParseYAML_AllResilienceFields(t *testing.T) {
	yamlInput := `
exporter:
  endpoint: "backend:4317"
  protocol: "http"
  timeout: "45s"
  http_client:
    max_idle_conns: 200
    max_idle_conns_per_host: 50
    max_conns_per_host: 100
    idle_conn_timeout: "2m"
    dial_timeout: "15s"
  queue:
    enabled: true
    path: "/data/queue"
    max_size: 5000
    max_bytes: "2Gi"
    retry_interval: "10s"
    max_retry_delay: "2m"
    full_behavior: "drop_oldest"
    backoff:
      enabled: true
      multiplier: 1.5
    circuit_breaker:
      enabled: true
      threshold: 5
      reset_timeout: "45s"
    batch_drain_size: 20
    burst_drain_size: 200
    retry_timeout: "3s"
    close_timeout: "45s"
    drain_timeout: "20s"
    drain_entry_timeout: "2s"
telemetry:
  endpoint: "otel-collector:4317"
  protocol: "grpc"
  insecure: true
  push_interval: "15s"
  retry:
    enabled: true
    initial: "3s"
    max_interval: "20s"
    max_elapsed: "2m"
`
	cfg, err := ParseYAML([]byte(yamlInput))
	if err != nil {
		t.Fatalf("ParseYAML error: %v", err)
	}

	// --- Queue resilience fields ---
	if cfg.Exporter.Queue.BatchDrainSize != 20 {
		t.Errorf("BatchDrainSize = %d, want 20", cfg.Exporter.Queue.BatchDrainSize)
	}
	if cfg.Exporter.Queue.BurstDrainSize != 200 {
		t.Errorf("BurstDrainSize = %d, want 200", cfg.Exporter.Queue.BurstDrainSize)
	}
	if time.Duration(cfg.Exporter.Queue.RetryTimeout) != 3*time.Second {
		t.Errorf("RetryTimeout = %v, want 3s", cfg.Exporter.Queue.RetryTimeout)
	}
	if time.Duration(cfg.Exporter.Queue.CloseTimeout) != 45*time.Second {
		t.Errorf("CloseTimeout = %v, want 45s", cfg.Exporter.Queue.CloseTimeout)
	}
	if time.Duration(cfg.Exporter.Queue.DrainTimeout) != 20*time.Second {
		t.Errorf("DrainTimeout = %v, want 20s", cfg.Exporter.Queue.DrainTimeout)
	}
	if time.Duration(cfg.Exporter.Queue.DrainEntryTimeout) != 2*time.Second {
		t.Errorf("DrainEntryTimeout = %v, want 2s", cfg.Exporter.Queue.DrainEntryTimeout)
	}

	// --- Circuit breaker fields ---
	if !*cfg.Exporter.Queue.CircuitBreaker.Enabled {
		t.Error("CircuitBreaker.Enabled should be true")
	}
	if cfg.Exporter.Queue.CircuitBreaker.Threshold != 5 {
		t.Errorf("CircuitBreaker.Threshold = %d, want 5", cfg.Exporter.Queue.CircuitBreaker.Threshold)
	}
	if time.Duration(cfg.Exporter.Queue.CircuitBreaker.ResetTimeout) != 45*time.Second {
		t.Errorf("CircuitBreaker.ResetTimeout = %v, want 45s", cfg.Exporter.Queue.CircuitBreaker.ResetTimeout)
	}

	// --- Backoff fields ---
	if !*cfg.Exporter.Queue.Backoff.Enabled {
		t.Error("Backoff.Enabled should be true")
	}
	if cfg.Exporter.Queue.Backoff.Multiplier != 1.5 {
		t.Errorf("Backoff.Multiplier = %f, want 1.5", cfg.Exporter.Queue.Backoff.Multiplier)
	}

	// --- Telemetry retry fields ---
	if !*cfg.Telemetry.Retry.Enabled {
		t.Error("Telemetry.Retry.Enabled should be true")
	}
	if time.Duration(cfg.Telemetry.Retry.Initial) != 3*time.Second {
		t.Errorf("Telemetry.Retry.Initial = %v, want 3s", cfg.Telemetry.Retry.Initial)
	}
	if time.Duration(cfg.Telemetry.Retry.MaxInterval) != 20*time.Second {
		t.Errorf("Telemetry.Retry.MaxInterval = %v, want 20s", cfg.Telemetry.Retry.MaxInterval)
	}
	if time.Duration(cfg.Telemetry.Retry.MaxElapsed) != 2*time.Minute {
		t.Errorf("Telemetry.Retry.MaxElapsed = %v, want 2m", cfg.Telemetry.Retry.MaxElapsed)
	}

	// --- HTTP client dial timeout ---
	if time.Duration(cfg.Exporter.HTTPClient.DialTimeout) != 15*time.Second {
		t.Errorf("HTTPClient.DialTimeout = %v, want 15s", cfg.Exporter.HTTPClient.DialTimeout)
	}
}

// =============================================================================
// ApplyDefaults: verify defaults for all new resilience fields
// =============================================================================

func TestApplyDefaults_ResilienceFields(t *testing.T) {
	cfg := &YAMLConfig{}
	cfg.ApplyDefaults()

	// Queue resilience defaults
	if cfg.Exporter.Queue.BatchDrainSize != 10 {
		t.Errorf("default BatchDrainSize = %d, want 10", cfg.Exporter.Queue.BatchDrainSize)
	}
	if cfg.Exporter.Queue.BurstDrainSize != 100 {
		t.Errorf("default BurstDrainSize = %d, want 100", cfg.Exporter.Queue.BurstDrainSize)
	}
	if time.Duration(cfg.Exporter.Queue.RetryTimeout) != 10*time.Second {
		t.Errorf("default RetryTimeout = %v, want 10s", cfg.Exporter.Queue.RetryTimeout)
	}
	if time.Duration(cfg.Exporter.Queue.CloseTimeout) != 60*time.Second {
		t.Errorf("default CloseTimeout = %v, want 60s", cfg.Exporter.Queue.CloseTimeout)
	}
	if time.Duration(cfg.Exporter.Queue.DrainTimeout) != 30*time.Second {
		t.Errorf("default DrainTimeout = %v, want 30s", cfg.Exporter.Queue.DrainTimeout)
	}
	if time.Duration(cfg.Exporter.Queue.DrainEntryTimeout) != 5*time.Second {
		t.Errorf("default DrainEntryTimeout = %v, want 5s", cfg.Exporter.Queue.DrainEntryTimeout)
	}

	// Circuit breaker defaults
	if cfg.Exporter.Queue.CircuitBreaker.Enabled == nil {
		t.Fatal("default CircuitBreaker.Enabled should not be nil")
	}
	if !*cfg.Exporter.Queue.CircuitBreaker.Enabled {
		t.Error("default CircuitBreaker.Enabled should be true")
	}
	if cfg.Exporter.Queue.CircuitBreaker.Threshold != 10 {
		t.Errorf("default CircuitBreaker.Threshold = %d, want 10", cfg.Exporter.Queue.CircuitBreaker.Threshold)
	}
	if time.Duration(cfg.Exporter.Queue.CircuitBreaker.ResetTimeout) != 30*time.Second {
		t.Errorf("default CircuitBreaker.ResetTimeout = %v, want 30s", cfg.Exporter.Queue.CircuitBreaker.ResetTimeout)
	}

	// Backoff defaults
	if cfg.Exporter.Queue.Backoff.Enabled == nil {
		t.Fatal("default Backoff.Enabled should not be nil")
	}
	if !*cfg.Exporter.Queue.Backoff.Enabled {
		t.Error("default Backoff.Enabled should be true")
	}
	if cfg.Exporter.Queue.Backoff.Multiplier != 2.0 {
		t.Errorf("default Backoff.Multiplier = %f, want 2.0", cfg.Exporter.Queue.Backoff.Multiplier)
	}

	// HTTP client dial timeout default
	if time.Duration(cfg.Exporter.HTTPClient.DialTimeout) != 30*time.Second {
		t.Errorf("default HTTPClient.DialTimeout = %v, want 30s", cfg.Exporter.HTTPClient.DialTimeout)
	}

	// Telemetry retry defaults
	if cfg.Telemetry.Retry.Enabled == nil {
		t.Fatal("default Telemetry.Retry.Enabled should not be nil")
	}
	if !*cfg.Telemetry.Retry.Enabled {
		t.Error("default Telemetry.Retry.Enabled should be true")
	}
	if time.Duration(cfg.Telemetry.Retry.Initial) != 5*time.Second {
		t.Errorf("default Telemetry.Retry.Initial = %v, want 5s", cfg.Telemetry.Retry.Initial)
	}
	if time.Duration(cfg.Telemetry.Retry.MaxInterval) != 30*time.Second {
		t.Errorf("default Telemetry.Retry.MaxInterval = %v, want 30s", cfg.Telemetry.Retry.MaxInterval)
	}
	if time.Duration(cfg.Telemetry.Retry.MaxElapsed) != 1*time.Minute {
		t.Errorf("default Telemetry.Retry.MaxElapsed = %v, want 1m", cfg.Telemetry.Retry.MaxElapsed)
	}
}

// =============================================================================
// ToConfig: verify all new fields are mapped to the flat Config struct
// =============================================================================

func TestToConfig_ResilienceFields(t *testing.T) {
	yamlInput := `
exporter:
  endpoint: "backend:4317"
  http_client:
    dial_timeout: "15s"
  queue:
    enabled: true
    backoff:
      enabled: true
      multiplier: 3.0
    circuit_breaker:
      enabled: true
      threshold: 7
      reset_timeout: "1m"
    batch_drain_size: 25
    burst_drain_size: 250
    retry_timeout: "5s"
    close_timeout: "90s"
    drain_timeout: "45s"
    drain_entry_timeout: "8s"
telemetry:
  endpoint: "collector:4317"
  retry:
    enabled: true
    initial: "2s"
    max_interval: "15s"
    max_elapsed: "3m"
`
	yamlCfg, err := ParseYAML([]byte(yamlInput))
	if err != nil {
		t.Fatalf("ParseYAML error: %v", err)
	}

	cfg := yamlCfg.ToConfig()

	// Queue resilience in flat Config
	if cfg.QueueBatchDrainSize != 25 {
		t.Errorf("QueueBatchDrainSize = %d, want 25", cfg.QueueBatchDrainSize)
	}
	if cfg.QueueBurstDrainSize != 250 {
		t.Errorf("QueueBurstDrainSize = %d, want 250", cfg.QueueBurstDrainSize)
	}
	if cfg.QueueRetryTimeout != 5*time.Second {
		t.Errorf("QueueRetryTimeout = %v, want 5s", cfg.QueueRetryTimeout)
	}
	if cfg.QueueCloseTimeout != 90*time.Second {
		t.Errorf("QueueCloseTimeout = %v, want 90s", cfg.QueueCloseTimeout)
	}
	if cfg.QueueDrainTimeout != 45*time.Second {
		t.Errorf("QueueDrainTimeout = %v, want 45s", cfg.QueueDrainTimeout)
	}
	if cfg.QueueDrainEntryTimeout != 8*time.Second {
		t.Errorf("QueueDrainEntryTimeout = %v, want 8s", cfg.QueueDrainEntryTimeout)
	}

	// Backoff in flat Config
	if !cfg.QueueBackoffEnabled {
		t.Error("QueueBackoffEnabled should be true")
	}
	if cfg.QueueBackoffMultiplier != 3.0 {
		t.Errorf("QueueBackoffMultiplier = %f, want 3.0", cfg.QueueBackoffMultiplier)
	}

	// Circuit breaker in flat Config
	if !cfg.QueueCircuitBreakerEnabled {
		t.Error("QueueCircuitBreakerEnabled should be true")
	}
	if cfg.QueueCircuitBreakerThreshold != 7 {
		t.Errorf("QueueCircuitBreakerThreshold = %d, want 7", cfg.QueueCircuitBreakerThreshold)
	}
	if cfg.QueueCircuitBreakerResetTimeout != 1*time.Minute {
		t.Errorf("QueueCircuitBreakerResetTimeout = %v, want 1m", cfg.QueueCircuitBreakerResetTimeout)
	}

	// HTTP client dial timeout in flat Config
	if cfg.ExporterDialTimeout != 15*time.Second {
		t.Errorf("ExporterDialTimeout = %v, want 15s", cfg.ExporterDialTimeout)
	}

	// Telemetry retry in flat Config
	if !cfg.TelemetryRetryEnabled {
		t.Error("TelemetryRetryEnabled should be true")
	}
	if cfg.TelemetryRetryInitial != 2*time.Second {
		t.Errorf("TelemetryRetryInitial = %v, want 2s", cfg.TelemetryRetryInitial)
	}
	if cfg.TelemetryRetryMaxInterval != 15*time.Second {
		t.Errorf("TelemetryRetryMaxInterval = %v, want 15s", cfg.TelemetryRetryMaxInterval)
	}
	if cfg.TelemetryRetryMaxElapsed != 3*time.Minute {
		t.Errorf("TelemetryRetryMaxElapsed = %v, want 3m", cfg.TelemetryRetryMaxElapsed)
	}
}

// =============================================================================
// ToConfig with defaults: verify unset YAML fields get correct defaults in Config
// =============================================================================

func TestToConfig_ResilienceFieldsWithDefaults(t *testing.T) {
	// Minimal YAML -- all resilience fields use defaults
	yamlInput := `
exporter:
  endpoint: "backend:4317"
`
	yamlCfg, err := ParseYAML([]byte(yamlInput))
	if err != nil {
		t.Fatalf("ParseYAML error: %v", err)
	}

	cfg := yamlCfg.ToConfig()

	// Queue resilience defaults
	if cfg.QueueBatchDrainSize != 10 {
		t.Errorf("default QueueBatchDrainSize = %d, want 10", cfg.QueueBatchDrainSize)
	}
	if cfg.QueueBurstDrainSize != 100 {
		t.Errorf("default QueueBurstDrainSize = %d, want 100", cfg.QueueBurstDrainSize)
	}
	if cfg.QueueRetryTimeout != 10*time.Second {
		t.Errorf("default QueueRetryTimeout = %v, want 10s", cfg.QueueRetryTimeout)
	}
	if cfg.QueueCloseTimeout != 60*time.Second {
		t.Errorf("default QueueCloseTimeout = %v, want 60s", cfg.QueueCloseTimeout)
	}
	if cfg.QueueDrainTimeout != 30*time.Second {
		t.Errorf("default QueueDrainTimeout = %v, want 30s", cfg.QueueDrainTimeout)
	}
	if cfg.QueueDrainEntryTimeout != 5*time.Second {
		t.Errorf("default QueueDrainEntryTimeout = %v, want 5s", cfg.QueueDrainEntryTimeout)
	}

	// Backoff defaults
	if !cfg.QueueBackoffEnabled {
		t.Error("default QueueBackoffEnabled should be true")
	}
	if cfg.QueueBackoffMultiplier != 2.0 {
		t.Errorf("default QueueBackoffMultiplier = %f, want 2.0", cfg.QueueBackoffMultiplier)
	}

	// Circuit breaker defaults
	if !cfg.QueueCircuitBreakerEnabled {
		t.Error("default QueueCircuitBreakerEnabled should be true")
	}
	if cfg.QueueCircuitBreakerThreshold != 10 {
		t.Errorf("default QueueCircuitBreakerThreshold = %d, want 10", cfg.QueueCircuitBreakerThreshold)
	}
	if cfg.QueueCircuitBreakerResetTimeout != 30*time.Second {
		t.Errorf("default QueueCircuitBreakerResetTimeout = %v, want 30s", cfg.QueueCircuitBreakerResetTimeout)
	}

	// HTTP client dial timeout default
	if cfg.ExporterDialTimeout != 30*time.Second {
		t.Errorf("default ExporterDialTimeout = %v, want 30s", cfg.ExporterDialTimeout)
	}

	// Telemetry retry defaults
	if !cfg.TelemetryRetryEnabled {
		t.Error("default TelemetryRetryEnabled should be true")
	}
	if cfg.TelemetryRetryInitial != 5*time.Second {
		t.Errorf("default TelemetryRetryInitial = %v, want 5s", cfg.TelemetryRetryInitial)
	}
	if cfg.TelemetryRetryMaxInterval != 30*time.Second {
		t.Errorf("default TelemetryRetryMaxInterval = %v, want 30s", cfg.TelemetryRetryMaxInterval)
	}
	if cfg.TelemetryRetryMaxElapsed != 1*time.Minute {
		t.Errorf("default TelemetryRetryMaxElapsed = %v, want 1m", cfg.TelemetryRetryMaxElapsed)
	}
}

// =============================================================================
// Edge cases: disabled features, zero/negative values
// =============================================================================

func TestParseYAML_DisabledResilienceFeatures(t *testing.T) {
	yamlInput := `
exporter:
  endpoint: "backend:4317"
  queue:
    enabled: false
    backoff:
      enabled: false
    circuit_breaker:
      enabled: false
telemetry:
  endpoint: "collector:4317"
  retry:
    enabled: false
`
	cfg, err := ParseYAML([]byte(yamlInput))
	if err != nil {
		t.Fatalf("ParseYAML error: %v", err)
	}

	if *cfg.Exporter.Queue.Enabled {
		t.Error("Queue should be disabled")
	}
	if *cfg.Exporter.Queue.Backoff.Enabled {
		t.Error("Backoff should be disabled")
	}
	if *cfg.Exporter.Queue.CircuitBreaker.Enabled {
		t.Error("CircuitBreaker should be disabled")
	}
	if *cfg.Telemetry.Retry.Enabled {
		t.Error("Telemetry retry should be disabled")
	}

	flat := cfg.ToConfig()
	if flat.QueueEnabled {
		t.Error("flat QueueEnabled should be false")
	}
	if flat.QueueBackoffEnabled {
		t.Error("flat QueueBackoffEnabled should be false")
	}
	if flat.QueueCircuitBreakerEnabled {
		t.Error("flat QueueCircuitBreakerEnabled should be false")
	}
	if flat.TelemetryRetryEnabled {
		t.Error("flat TelemetryRetryEnabled should be false")
	}
}

func TestParseYAML_PartialResilienceOverrides(t *testing.T) {
	// Only override some fields, rest should get defaults
	yamlInput := `
exporter:
  endpoint: "backend:4317"
  queue:
    batch_drain_size: 50
    circuit_breaker:
      threshold: 3
`
	cfg, err := ParseYAML([]byte(yamlInput))
	if err != nil {
		t.Fatalf("ParseYAML error: %v", err)
	}

	// Overridden
	if cfg.Exporter.Queue.BatchDrainSize != 50 {
		t.Errorf("BatchDrainSize = %d, want 50", cfg.Exporter.Queue.BatchDrainSize)
	}
	if cfg.Exporter.Queue.CircuitBreaker.Threshold != 3 {
		t.Errorf("CircuitBreaker.Threshold = %d, want 3", cfg.Exporter.Queue.CircuitBreaker.Threshold)
	}

	// Should get defaults
	if cfg.Exporter.Queue.BurstDrainSize != 100 {
		t.Errorf("BurstDrainSize = %d, want default 100", cfg.Exporter.Queue.BurstDrainSize)
	}
	if time.Duration(cfg.Exporter.Queue.RetryTimeout) != 10*time.Second {
		t.Errorf("RetryTimeout = %v, want default 10s", cfg.Exporter.Queue.RetryTimeout)
	}
	if time.Duration(cfg.Exporter.Queue.CircuitBreaker.ResetTimeout) != 30*time.Second {
		t.Errorf("CircuitBreaker.ResetTimeout = %v, want default 30s", cfg.Exporter.Queue.CircuitBreaker.ResetTimeout)
	}
}
