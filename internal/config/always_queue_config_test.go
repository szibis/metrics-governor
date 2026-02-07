package config

import (
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Validation tests
// ---------------------------------------------------------------------------

func TestValidate_BufferFullPolicy_Valid(t *testing.T) {
	for _, policy := range []string{"reject", "drop_oldest", "block"} {
		t.Run(policy, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.BufferFullPolicy = policy
			if err := cfg.Validate(); err != nil {
				t.Errorf("expected BufferFullPolicy %q to pass validation, got error: %v", policy, err)
			}
		})
	}
}

func TestValidate_BufferFullPolicy_Invalid(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BufferFullPolicy = "invalid"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for BufferFullPolicy=\"invalid\", got nil")
	}
	if got := err.Error(); !strContains(got, "buffer-full-policy") {
		t.Errorf("expected error mentioning buffer-full-policy, got: %s", got)
	}
}

func TestValidate_BufferMemoryPercent_Valid(t *testing.T) {
	for _, pct := range []float64{0.0, 0.01, 0.15, 0.25, 0.50} {
		t.Run("", func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.BufferMemoryPercent = pct
			// Keep total <= 0.5 so we don't trigger the combined check.
			cfg.QueueMemoryPercent = 0.0
			if err := cfg.Validate(); err != nil {
				t.Errorf("expected BufferMemoryPercent=%f to pass validation, got error: %v", pct, err)
			}
		})
	}
}

func TestValidate_BufferMemoryPercent_Invalid(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  float64
	}{
		{"negative", -0.1},
		{"above_one", 1.1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.BufferMemoryPercent = tc.val
			cfg.QueueMemoryPercent = 0.0
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("expected validation error for BufferMemoryPercent=%f, got nil", tc.val)
			}
			if got := err.Error(); !strContains(got, "buffer-memory-percent") {
				t.Errorf("expected error mentioning buffer-memory-percent, got: %s", got)
			}
		})
	}
}

func TestValidate_QueueMemoryPercent_Invalid(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  float64
	}{
		{"negative", -0.05},
		{"above_one", 2.0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.QueueMemoryPercent = tc.val
			cfg.BufferMemoryPercent = 0.0
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("expected validation error for QueueMemoryPercent=%f, got nil", tc.val)
			}
			if got := err.Error(); !strContains(got, "queue-memory-percent") {
				t.Errorf("expected error mentioning queue-memory-percent, got: %s", got)
			}
		})
	}
}

func TestValidate_MemoryPercent_TotalExceeds50(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BufferMemoryPercent = 0.30
	cfg.QueueMemoryPercent = 0.25
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error when buffer_percent + queue_percent > 0.5, got nil")
	}
	got := err.Error()
	if !strContains(got, "buffer-memory-percent") || !strContains(got, "queue-memory-percent") {
		t.Errorf("expected error about combined memory percent, got: %s", got)
	}
}

func TestValidate_MemoryPercent_TotalOK(t *testing.T) {
	for _, tc := range []struct {
		name string
		buf  float64
		q    float64
	}{
		{"exactly_half", 0.25, 0.25},
		{"under_half", 0.20, 0.10},
		{"zero", 0.0, 0.0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.BufferMemoryPercent = tc.buf
			cfg.QueueMemoryPercent = tc.q
			if err := cfg.Validate(); err != nil {
				t.Errorf("expected buffer_percent=%f + queue_percent=%f to pass validation, got error: %v", tc.buf, tc.q, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DefaultConfig tests
// ---------------------------------------------------------------------------

func TestDefaultConfig_AlwaysQueueFields(t *testing.T) {
	cfg := DefaultConfig()

	if !cfg.QueueAlwaysQueue {
		t.Error("expected QueueAlwaysQueue=true in defaults")
	}
	if cfg.QueueWorkers != 0 {
		t.Errorf("expected QueueWorkers=0 in defaults, got %d", cfg.QueueWorkers)
	}
	if cfg.BufferFullPolicy != "reject" {
		t.Errorf("expected BufferFullPolicy=\"reject\" in defaults, got %q", cfg.BufferFullPolicy)
	}
	if cfg.FlushTimeout != 30*time.Second {
		t.Errorf("expected FlushTimeout=30s in defaults, got %v", cfg.FlushTimeout)
	}
	if cfg.QueueCircuitBreakerThreshold != 5 {
		t.Errorf("expected QueueCircuitBreakerThreshold=5 in defaults, got %d", cfg.QueueCircuitBreakerThreshold)
	}
	if cfg.BufferMemoryPercent != 0.15 {
		t.Errorf("expected BufferMemoryPercent=0.15 in defaults, got %f", cfg.BufferMemoryPercent)
	}
	if cfg.QueueMemoryPercent != 0.15 {
		t.Errorf("expected QueueMemoryPercent=0.15 in defaults, got %f", cfg.QueueMemoryPercent)
	}
}

// ---------------------------------------------------------------------------
// YAML tests
// ---------------------------------------------------------------------------

func TestYAML_AlwaysQueueDefaults(t *testing.T) {
	// An empty YAML doc should get all defaults via ApplyDefaults().
	yamlCfg, err := ParseYAML([]byte("{}"))
	if err != nil {
		t.Fatalf("ParseYAML on empty doc failed: %v", err)
	}

	if yamlCfg.Exporter.Queue.AlwaysQueue == nil || !*yamlCfg.Exporter.Queue.AlwaysQueue {
		t.Error("expected AlwaysQueue default=true in parsed YAML")
	}
	if yamlCfg.Exporter.Queue.Workers != 0 {
		t.Errorf("expected Workers default=0, got %d", yamlCfg.Exporter.Queue.Workers)
	}
	if time.Duration(yamlCfg.Buffer.FlushTimeout) != 30*time.Second {
		t.Errorf("expected FlushTimeout default=30s, got %v", time.Duration(yamlCfg.Buffer.FlushTimeout))
	}
	if yamlCfg.Buffer.FullPolicy != "reject" {
		t.Errorf("expected FullPolicy default=\"reject\", got %q", yamlCfg.Buffer.FullPolicy)
	}
	if yamlCfg.Exporter.Queue.CircuitBreaker.Threshold != 5 {
		t.Errorf("expected CB Threshold default=5, got %d", yamlCfg.Exporter.Queue.CircuitBreaker.Threshold)
	}

	// Also verify ToConfig() propagation.
	cfg := yamlCfg.ToConfig()
	if !cfg.QueueAlwaysQueue {
		t.Error("expected ToConfig().QueueAlwaysQueue=true")
	}
	if cfg.QueueWorkers != 0 {
		t.Errorf("expected ToConfig().QueueWorkers=0, got %d", cfg.QueueWorkers)
	}
}

func TestYAML_AlwaysQueueExplicit(t *testing.T) {
	yamlData := `
exporter:
  queue:
    always_queue: false
    workers: 8
`
	yamlCfg, err := ParseYAML([]byte(yamlData))
	if err != nil {
		t.Fatalf("ParseYAML failed: %v", err)
	}

	if yamlCfg.Exporter.Queue.AlwaysQueue == nil {
		t.Fatal("expected AlwaysQueue to be set, got nil")
	}
	if *yamlCfg.Exporter.Queue.AlwaysQueue {
		t.Error("expected AlwaysQueue=false from explicit YAML")
	}
	if yamlCfg.Exporter.Queue.Workers != 8 {
		t.Errorf("expected Workers=8, got %d", yamlCfg.Exporter.Queue.Workers)
	}

	cfg := yamlCfg.ToConfig()
	if cfg.QueueAlwaysQueue {
		t.Error("expected ToConfig().QueueAlwaysQueue=false")
	}
	if cfg.QueueWorkers != 8 {
		t.Errorf("expected ToConfig().QueueWorkers=8, got %d", cfg.QueueWorkers)
	}
}

func TestYAML_MemoryPercent(t *testing.T) {
	yamlData := `
memory:
  buffer_percent: 0.20
  queue_percent: 0.10
`
	yamlCfg, err := ParseYAML([]byte(yamlData))
	if err != nil {
		t.Fatalf("ParseYAML failed: %v", err)
	}

	if yamlCfg.Memory.BufferPercent != 0.20 {
		t.Errorf("expected BufferPercent=0.20, got %f", yamlCfg.Memory.BufferPercent)
	}
	if yamlCfg.Memory.QueuePercent != 0.10 {
		t.Errorf("expected QueuePercent=0.10, got %f", yamlCfg.Memory.QueuePercent)
	}

	cfg := yamlCfg.ToConfig()
	if cfg.BufferMemoryPercent != 0.20 {
		t.Errorf("expected ToConfig().BufferMemoryPercent=0.20, got %f", cfg.BufferMemoryPercent)
	}
	if cfg.QueueMemoryPercent != 0.10 {
		t.Errorf("expected ToConfig().QueueMemoryPercent=0.10, got %f", cfg.QueueMemoryPercent)
	}
}

func TestYAML_BufferFullPolicy(t *testing.T) {
	yamlData := `
buffer:
  full_policy: "drop_oldest"
`
	yamlCfg, err := ParseYAML([]byte(yamlData))
	if err != nil {
		t.Fatalf("ParseYAML failed: %v", err)
	}

	if yamlCfg.Buffer.FullPolicy != "drop_oldest" {
		t.Errorf("expected FullPolicy=\"drop_oldest\", got %q", yamlCfg.Buffer.FullPolicy)
	}

	cfg := yamlCfg.ToConfig()
	if cfg.BufferFullPolicy != "drop_oldest" {
		t.Errorf("expected ToConfig().BufferFullPolicy=\"drop_oldest\", got %q", cfg.BufferFullPolicy)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func strContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
