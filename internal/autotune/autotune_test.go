package autotune

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTuner_NewDefaults(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Source.Enabled = false
	cfg.AIEnabled = true
	cfg.AIEndpoint = "http://localhost"

	tuner := NewTuner(cfg)
	if tuner == nil {
		t.Fatal("expected non-nil tuner")
	}
	if tuner.elector == nil {
		t.Error("expected non-nil elector")
	}
	if tuner.persister == nil {
		t.Error("expected non-nil persister")
	}
	if tuner.policy == nil {
		t.Error("expected non-nil policy")
	}
}

func TestTuner_DisabledNoMetrics(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = false

	tuner := NewTuner(cfg)
	w := httptest.NewRecorder()
	tuner.ServeHTTP(w, nil)

	body := w.Body.String()
	if !strings.Contains(body, "metrics_governor_autotune_enabled 0") {
		t.Error("expected autotune_enabled=0 when disabled")
	}
}

func TestTuner_EnabledShowsMetrics(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Source.Enabled = false
	cfg.AIEnabled = true
	cfg.AIEndpoint = "http://localhost"

	tuner := NewTuner(cfg)
	var buf bytes.Buffer
	writeAutotuneMetrics(&buf, tuner)

	body := buf.String()
	if !strings.Contains(body, "metrics_governor_autotune_enabled 1") {
		t.Error("expected autotune_enabled=1 when enabled")
	}
	if !strings.Contains(body, "metrics_governor_autotune_cycles_total 0") {
		t.Error("expected cycles_total=0 initially")
	}
	if !strings.Contains(body, "metrics_governor_autotune_ha_is_leader 0") {
		t.Error("expected ha_is_leader=0 initially")
	}
}

func TestTuner_CycleWithMockSource(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Interval = 50 * time.Millisecond
	cfg.Source.Enabled = false // we'll inject directly
	cfg.AIEnabled = true
	cfg.AIEndpoint = "http://localhost"
	cfg.Policy.Cooldown = time.Millisecond
	cfg.Safeguards.ReloadMinInterval = time.Millisecond
	cfg.Safeguards.ReloadTimeout = 5 * time.Second

	var reloadCalled atomic.Int64
	var lastLimits atomic.Value

	tuner := NewTuner(cfg,
		WithUtilizationFunc(func() map[string]RuleUtilization {
			return map[string]RuleUtilization{
				"rule1": {Name: "rule1", MaxCard: 1000, CurrentCard: 900},
			}
		}),
		WithDropsFunc(func() int64 { return 0 }),
		WithReloadFunc(func(cfg any) {
			reloadCalled.Add(1)
			lastLimits.Store(cfg)
		}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	tuner.Start(ctx)
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-tuner.done

	cycles := tuner.cyclesTotal.Load()
	if cycles == 0 {
		t.Error("expected at least 1 cycle")
	}
}

func TestTuner_UpdateBaseline(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Source.Enabled = false
	cfg.AIEnabled = true
	cfg.AIEndpoint = "http://localhost"

	utilData := map[string]RuleUtilization{
		"rule1": {Name: "rule1", MaxCard: 1000},
	}
	tuner := NewTuner(cfg,
		WithUtilizationFunc(func() map[string]RuleUtilization {
			return utilData
		}),
	)

	// Snapshot initial limits.
	tuner.snapshotLimits()
	if tuner.currentLimits["rule1"] != 1000 {
		t.Errorf("expected initial limit 1000, got %d", tuner.currentLimits["rule1"])
	}

	// Simulate SIGHUP with new limits.
	utilData = map[string]RuleUtilization{
		"rule1": {Name: "rule1", MaxCard: 2000},
	}
	tuner.UpdateBaseline()

	if tuner.currentLimits["rule1"] != 2000 {
		t.Errorf("expected updated limit 2000, got %d", tuner.currentLimits["rule1"])
	}
	if tuner.baselineLimits["rule1"] != 2000 {
		t.Errorf("expected updated baseline 2000, got %d", tuner.baselineLimits["rule1"])
	}
}

func TestTuner_NoopElectorAlwaysLeader(t *testing.T) {
	e := NewNoopElector()
	if !e.IsLeader() {
		t.Error("NoopElector should always be leader")
	}
	if err := e.Start(context.Background()); err != nil {
		t.Errorf("Start should return nil: %v", err)
	}
	e.Stop() // should not panic
}

func TestTuner_MemoryPersister(t *testing.T) {
	p := NewMemoryPersister(10)
	ctx := context.Background()

	// Persist some changes.
	changes := []ConfigChange{
		{RuleName: "rule1", Action: "increase", NewValue: 1500},
		{RuleName: "rule2", Action: "decrease", NewValue: 500},
	}
	if err := p.Persist(ctx, changes); err != nil {
		t.Fatalf("Persist error: %v", err)
	}

	// Load history.
	history, err := p.LoadHistory(ctx)
	if err != nil {
		t.Fatalf("LoadHistory error: %v", err)
	}
	if len(history) != 2 {
		t.Errorf("expected 2 changes, got %d", len(history))
	}

	// Test max size trim.
	for i := 0; i < 20; i++ {
		p.Persist(ctx, []ConfigChange{{RuleName: "rule1"}})
	}
	history, _ = p.LoadHistory(ctx)
	if len(history) > 10 {
		t.Errorf("expected max 10 entries, got %d", len(history))
	}
}

func TestTuner_SignalAggregator_InternalOnly(t *testing.T) {
	var dropCount int64 = 42
	utilData := map[string]RuleUtilization{
		"rule1": {Name: "rule1", MaxCard: 1000, MaxDPRate: 500, CurrentCard: 800, CurrentDPs: 200},
	}

	agg := NewSignalAggregator(nil, nil,
		func() int64 { return dropCount },
		func() map[string]RuleUtilization { return utilData },
	)

	signals, err := agg.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if signals.InternalDropTotal != 42 {
		t.Errorf("expected drops=42, got %d", signals.InternalDropTotal)
	}
	if signals.Utilization["rule1"] != 0.8 {
		t.Errorf("expected utilization=0.8, got %f", signals.Utilization["rule1"])
	}
	if signals.CardinalityUtilization["rule1"] != 0.8 {
		t.Errorf("expected card util=0.8, got %f", signals.CardinalityUtilization["rule1"])
	}
	if signals.DPRateUtilization["rule1"] != 0.4 {
		t.Errorf("expected dp util=0.4, got %f", signals.DPRateUtilization["rule1"])
	}
}

func TestTuner_DecisionsToChanges(t *testing.T) {
	decisions := []Decision{
		{
			RuleName:  "rule1",
			Field:     "max_cardinality",
			OldValue:  1000,
			NewValue:  1250,
			Action:    ActionIncrease,
			Reason:    "test",
			Timestamp: time.Now(),
		},
	}

	changes := decisionsToChanges(decisions)
	if len(changes) != 1 {
		t.Fatalf("expected 1 change, got %d", len(changes))
	}
	if changes[0].Domain != "limits" {
		t.Errorf("expected domain 'limits', got %s", changes[0].Domain)
	}
	if changes[0].Action != "increase" {
		t.Errorf("expected action 'increase', got %s", changes[0].Action)
	}
}
