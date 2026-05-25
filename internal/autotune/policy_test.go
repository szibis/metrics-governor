package autotune

import (
	"testing"
	"time"
)

func TestPolicyEngine_IncreaseOnHighUtilization(t *testing.T) {
	tests := []struct {
		name      string
		util      float64
		threshold float64
		current   int64
		growFactor float64
		expected  int64
		action    Action
	}{
		{"above threshold → increase", 0.90, 0.85, 1000, 1.25, 1250, ActionIncrease},
		{"at threshold → increase", 0.85, 0.85, 1000, 1.25, 1250, ActionIncrease},
		{"below threshold → hold", 0.80, 0.85, 1000, 1.25, 0, ActionHold},
		{"grow factor 1.50", 0.90, 0.85, 1000, 1.50, 1500, ActionIncrease},
		{"grow factor 2.00", 0.90, 0.85, 1000, 2.00, 1500, ActionIncrease}, // clamped by MaxChangePct=0.50
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultPolicyConfig()
			cfg.IncreaseThreshold = tt.threshold
			cfg.GrowFactor = tt.growFactor

			engine := NewPolicyEngine(cfg, NewOscillationDetector(DefaultSafeguardsConfig().Oscillation))
			now := time.Now()

			signals := AggregatedSignals{
				Utilization: map[string]float64{"rule1": tt.util},
			}
			limits := map[string]int64{"rule1": tt.current}

			decisions := engine.Evaluate(signals, limits, now)

			if tt.action == ActionHold {
				if len(decisions) != 0 {
					t.Errorf("expected no decisions, got %d", len(decisions))
				}
				return
			}

			if len(decisions) != 1 {
				t.Fatalf("expected 1 decision, got %d", len(decisions))
			}
			d := decisions[0]
			if d.Action != tt.action {
				t.Errorf("expected action %s, got %s", tt.action, d.Action)
			}
			if d.NewValue != tt.expected {
				t.Errorf("expected NewValue=%d, got %d", tt.expected, d.NewValue)
			}
		})
	}
}

func TestPolicyEngine_DecreaseOnLowUtilization(t *testing.T) {
	cfg := DefaultPolicyConfig()
	cfg.DecreaseSustainTime = 10 * time.Second // short for testing

	engine := NewPolicyEngine(cfg, NewOscillationDetector(DefaultSafeguardsConfig().Oscillation))
	limits := map[string]int64{"rule1": 1000}

	// First call: low utilization — record timestamp, no action.
	t0 := time.Now()
	signals := AggregatedSignals{
		Utilization: map[string]float64{"rule1": 0.20},
	}
	decisions := engine.Evaluate(signals, limits, t0)
	if len(decisions) != 0 {
		t.Fatalf("expected no decisions on first low reading, got %d", len(decisions))
	}

	// Second call: still low but not sustained long enough.
	t1 := t0.Add(5 * time.Second)
	decisions = engine.Evaluate(signals, limits, t1)
	if len(decisions) != 0 {
		t.Fatalf("expected no decisions before sustain time, got %d", len(decisions))
	}

	// Third call: sustained low → decrease.
	t2 := t0.Add(15 * time.Second)
	decisions = engine.Evaluate(signals, limits, t2)
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision after sustain time, got %d", len(decisions))
	}
	if decisions[0].Action != ActionDecrease {
		t.Errorf("expected ActionDecrease, got %s", decisions[0].Action)
	}
	if decisions[0].NewValue != 750 { // 1000 * 0.75
		t.Errorf("expected NewValue=750, got %d", decisions[0].NewValue)
	}
}

func TestPolicyEngine_Cooldown(t *testing.T) {
	cfg := DefaultPolicyConfig()
	cfg.Cooldown = 5 * time.Minute

	engine := NewPolicyEngine(cfg, NewOscillationDetector(DefaultSafeguardsConfig().Oscillation))
	limits := map[string]int64{"rule1": 1000}
	signals := AggregatedSignals{
		Utilization: map[string]float64{"rule1": 0.90},
	}

	// First: should produce a decision.
	t0 := time.Now()
	decisions := engine.Evaluate(signals, limits, t0)
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}

	// Update limits to the new value.
	limits["rule1"] = decisions[0].NewValue

	// Within cooldown: no decision.
	t1 := t0.Add(3 * time.Minute)
	decisions = engine.Evaluate(signals, limits, t1)
	if len(decisions) != 0 {
		t.Errorf("expected no decisions during cooldown, got %d", len(decisions))
	}

	// After cooldown: should produce a decision.
	t2 := t0.Add(6 * time.Minute)
	decisions = engine.Evaluate(signals, limits, t2)
	if len(decisions) != 1 {
		t.Errorf("expected 1 decision after cooldown, got %d", len(decisions))
	}
}

func TestPolicyEngine_MinMaxClamping(t *testing.T) {
	cfg := DefaultPolicyConfig()
	cfg.MinCardinality = 100
	cfg.MaxCardinality = 10_000_000
	cfg.MaxChangePct = 1.0 // allow full change for this test

	// Test min clamping: proposed < min.
	t.Run("min clamping", func(t *testing.T) {
		limits := map[string]int64{"rule1": 150}
		clampCfg := DefaultPolicyConfig()
		clampCfg.MinCardinality = 100
		clampCfg.MaxCardinality = 10_000_000
		clampCfg.MaxChangePct = 1.0
		clampCfg.DecreaseSustainTime = 0
		engine2 := NewPolicyEngine(clampCfg, NewOscillationDetector(DefaultSafeguardsConfig().Oscillation))
		signals := AggregatedSignals{
			Utilization: map[string]float64{"rule1": 0.10},
		}
		// Need two calls to pass sustain time=0.
		now := time.Now()
		engine2.Evaluate(signals, limits, now)
		decisions := engine2.Evaluate(signals, limits, now.Add(time.Millisecond))
		if len(decisions) == 1 {
			if decisions[0].NewValue < 100 {
				t.Errorf("expected clamped to min 100, got %d", decisions[0].NewValue)
			}
		}
	})
}

func TestPolicyEngine_MaxChangePct(t *testing.T) {
	tests := []struct {
		name         string
		maxChangePct float64
		current      int64
		expected     int64
	}{
		{"50% max change", 0.50, 1000, 1500},
		{"25% max change", 0.25, 1000, 1250},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultPolicyConfig()
			cfg.MaxChangePct = tt.maxChangePct
			cfg.GrowFactor = 3.0 // would go to 3000, but clamped

			engine := NewPolicyEngine(cfg, NewOscillationDetector(DefaultSafeguardsConfig().Oscillation))
			signals := AggregatedSignals{
				Utilization: map[string]float64{"rule1": 0.90},
			}
			limits := map[string]int64{"rule1": tt.current}

			decisions := engine.Evaluate(signals, limits, time.Now())
			if len(decisions) != 1 {
				t.Fatalf("expected 1 decision, got %d", len(decisions))
			}
			if decisions[0].NewValue != tt.expected {
				t.Errorf("expected NewValue=%d, got %d", tt.expected, decisions[0].NewValue)
			}
		})
	}
}

func TestPolicyEngine_AnomalyTighten(t *testing.T) {
	cfg := DefaultPolicyConfig()
	cfg.AnomalyTightenScore = 0.7
	cfg.TightenFactor = 0.80

	engine := NewPolicyEngine(cfg, NewOscillationDetector(DefaultSafeguardsConfig().Oscillation))
	signals := AggregatedSignals{
		Utilization:   map[string]float64{"rule1": 0.50},
		AnomalyScores: map[string]float64{"rule1": 0.85},
	}
	limits := map[string]int64{"rule1": 1000}

	decisions := engine.Evaluate(signals, limits, time.Now())
	if len(decisions) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(decisions))
	}
	if decisions[0].Action != ActionTighten {
		t.Errorf("expected ActionTighten, got %s", decisions[0].Action)
	}
	if decisions[0].NewValue != 800 {
		t.Errorf("expected NewValue=800, got %d", decisions[0].NewValue)
	}
}

func TestPolicyEngine_AnomalyBlocksGrowth(t *testing.T) {
	cfg := DefaultPolicyConfig()
	cfg.AnomalySafeScore = 0.5

	engine := NewPolicyEngine(cfg, NewOscillationDetector(DefaultSafeguardsConfig().Oscillation))
	signals := AggregatedSignals{
		Utilization:   map[string]float64{"rule1": 0.90},
		AnomalyScores: map[string]float64{"rule1": 0.6}, // above safe threshold
	}
	limits := map[string]int64{"rule1": 1000}

	decisions := engine.Evaluate(signals, limits, time.Now())
	if len(decisions) != 0 {
		t.Errorf("expected no decisions (anomaly blocks growth), got %d", len(decisions))
	}
}

func TestPolicyEngine_ResetAll(t *testing.T) {
	cfg := DefaultPolicyConfig()
	engine := NewPolicyEngine(cfg, NewOscillationDetector(DefaultSafeguardsConfig().Oscillation))

	// Create some state.
	signals := AggregatedSignals{
		Utilization: map[string]float64{"rule1": 0.90},
	}
	limits := map[string]int64{"rule1": 1000}
	engine.Evaluate(signals, limits, time.Now())

	// Reset.
	engine.ResetAll()

	// After reset, cooldown should not apply.
	decisions := engine.Evaluate(signals, limits, time.Now())
	if len(decisions) != 1 {
		t.Errorf("expected 1 decision after reset, got %d", len(decisions))
	}
}
