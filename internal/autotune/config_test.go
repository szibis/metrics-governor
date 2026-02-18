package autotune

import (
	"strings"
	"testing"
	"time"
)

func TestConfig_DefaultsAreValid(t *testing.T) {
	cfg := DefaultConfig()
	// Disabled config should always be valid.
	if err := cfg.Validate(); err != nil {
		t.Errorf("default config should be valid: %v", err)
	}
}

func TestConfig_EnabledRequiresTier(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error when enabled with no tiers")
	}
	if !strings.Contains(err.Error(), "at least one signal tier") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConfig_SourceRequiresURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Source.Enabled = true
	cfg.Source.URL = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error when source enabled without URL")
	}
	if !strings.Contains(err.Error(), "source.url required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConfig_InvalidBackend(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Source.Enabled = true
	cfg.Source.URL = "http://localhost:8428"
	cfg.Source.Backend = "invalid"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid backend")
	}
	if !strings.Contains(err.Error(), "unknown backend") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConfig_VMBothModesDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Source.Enabled = true
	cfg.Source.URL = "http://localhost:8428"
	cfg.Source.Backend = BackendVM
	cfg.Source.VM.TSDBInsights = false
	cfg.Source.VM.CardinalityExplorer = false

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error when both VM modes disabled")
	}
	if !strings.Contains(err.Error(), "at least one VM mode") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConfig_VMExplorerIntervalTooShort(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Source.Enabled = true
	cfg.Source.URL = "http://localhost:8428"
	cfg.Source.Backend = BackendVM
	cfg.Source.VM.CardinalityExplorer = true
	cfg.Source.VM.ExplorerInterval = 30 * time.Second

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for short explorer interval")
	}
	if !strings.Contains(err.Error(), "explorer_interval must be >= 1m") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConfig_GrowFactorTooLow(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Source.Enabled = true
	cfg.Source.URL = "http://localhost:8428"
	cfg.Policy.GrowFactor = 0.5

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for grow_factor <= 1.0")
	}
	if !strings.Contains(err.Error(), "grow_factor must be > 1.0") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConfig_ShrinkFactorTooHigh(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Source.Enabled = true
	cfg.Source.URL = "http://localhost:8428"
	cfg.Policy.ShrinkFactor = 1.5

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for shrink_factor >= 1.0")
	}
	if !strings.Contains(err.Error(), "shrink_factor must be between 0 and 1.0") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConfig_MinGtMax(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Source.Enabled = true
	cfg.Source.URL = "http://localhost:8428"
	cfg.Policy.MinCardinality = 20_000_000
	cfg.Policy.MaxCardinality = 10_000_000

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for min > max")
	}
	if !strings.Contains(err.Error(), "min_cardinality must be <= max_cardinality") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConfig_ZeroReloadTimeout(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Source.Enabled = true
	cfg.Source.URL = "http://localhost:8428"
	cfg.Safeguards.ReloadTimeout = 0

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for zero reload timeout")
	}
	if !strings.Contains(err.Error(), "reload_timeout must be > 0") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConfig_ValidFullConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Source.Enabled = true
	cfg.Source.URL = "http://vmselect:8481"
	cfg.Source.Backend = BackendVM

	if err := cfg.Validate(); err != nil {
		t.Errorf("valid full config should pass: %v", err)
	}
}

func TestConfig_ActiveTiers(t *testing.T) {
	tests := []struct {
		name      string
		source    bool
		vmanomaly bool
		ai        bool
		expected  []int
	}{
		{"tier 0 only", false, false, false, []int{0}},
		{"tier 0+1", true, false, false, []int{0, 1}},
		{"tier 0+2", false, true, false, []int{0, 2}},
		{"tier 0+3", false, false, true, []int{0, 3}},
		{"tier 0+1+2", true, true, false, []int{0, 1, 2}},
		{"all tiers", true, true, true, []int{0, 1, 2, 3}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Source.Enabled = tt.source
			cfg.VManomalyEnabled = tt.vmanomaly
			cfg.AIEnabled = tt.ai

			tiers := cfg.ActiveTiers()
			if len(tiers) != len(tt.expected) {
				t.Errorf("expected %d tiers, got %d", len(tt.expected), len(tiers))
				return
			}
			for i, tier := range tiers {
				if tier != tt.expected[i] {
					t.Errorf("tier[%d]: expected %d, got %d", i, tt.expected[i], tier)
				}
			}
		})
	}
}

func TestConfig_VManomalyRequiresURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.VManomalyEnabled = true
	cfg.VManomalyURL = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error when vmanomaly enabled without URL")
	}
	if !strings.Contains(err.Error(), "vmanomaly_url required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestConfig_AIRequiresEndpoint(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.AIEnabled = true
	cfg.AIEndpoint = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error when ai enabled without endpoint")
	}
	if !strings.Contains(err.Error(), "ai_endpoint required") {
		t.Errorf("unexpected error: %v", err)
	}
}
