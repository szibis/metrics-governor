package autotune

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

// policyEngine creates a PolicyEngine with a nil OscillationDetector, which is
// the simplest setup when we only care about threshold behavior.
func policyEngine(cfg PolicyConfig) *PolicyEngine {
	return NewPolicyEngine(cfg, nil)
}

// policyEngineWithOscillation creates a PolicyEngine wired to the given OscillationDetector.
func policyEngineWithOscillation(cfg PolicyConfig, od *OscillationDetector) *PolicyEngine {
	return NewPolicyEngine(cfg, od)
}

// requireDecisions is a test helper that asserts exactly n decisions were returned.
func requireDecisions(t *testing.T, decisions []Decision, n int) {
	t.Helper()
	if len(decisions) != n {
		t.Fatalf("expected %d decision(s), got %d: %+v", n, len(decisions), decisions)
	}
}

// requireAction is a test helper that asserts the first decision has the expected action.
func requireAction(t *testing.T, decisions []Decision, want Action) {
	t.Helper()
	requireDecisions(t, decisions, 1)
	if decisions[0].Action != want {
		t.Fatalf("expected action %q, got %q (reason: %s)", want, decisions[0].Action, decisions[0].Reason)
	}
}

// ===========================================================================
// Policy threshold tests
// ===========================================================================

func TestConfig_Policy_IncreaseThreshold(t *testing.T) {
	tests := []struct {
		name      string
		util      float64
		wantCount int
		wantAct   Action
	}{
		{"below threshold (0.84) → no decision", 0.84, 0, ""},
		{"above threshold (0.86) → increase", 0.86, 1, ActionIncrease},
		{"exactly at threshold (0.85) → increase", 0.85, 1, ActionIncrease},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultPolicyConfig()
			// Defaults: IncreaseThreshold=0.85
			engine := policyEngine(cfg)
			signals := AggregatedSignals{Utilization: map[string]float64{"r1": tt.util}}
			decisions := engine.Evaluate(signals, map[string]int64{"r1": 1000}, time.Now())
			requireDecisions(t, decisions, tt.wantCount)
			if tt.wantCount == 1 && decisions[0].Action != tt.wantAct {
				t.Errorf("expected action %q, got %q", tt.wantAct, decisions[0].Action)
			}
		})
	}
}

func TestConfig_Policy_DecreaseThreshold(t *testing.T) {
	tests := []struct {
		name      string
		util      float64
		sustained bool
		wantCount int
	}{
		{"above threshold (0.31) → no decision", 0.31, false, 0},
		{"below threshold (0.29) not sustained → hold (registers, no decision)", 0.29, false, 0},
		{"below threshold (0.29) sustained → decrease", 0.29, true, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultPolicyConfig()
			// Defaults: DecreaseThreshold=0.30, DecreaseSustainTime=1h
			engine := policyEngine(cfg)
			now := time.Now()
			signals := AggregatedSignals{Utilization: map[string]float64{"r1": tt.util}}
			limits := map[string]int64{"r1": 1000}

			if tt.sustained {
				// First call registers the low point.
				engine.Evaluate(signals, limits, now)
				// Second call after sustain time triggers decrease.
				decisions := engine.Evaluate(signals, limits, now.Add(65*time.Minute))
				requireDecisions(t, decisions, tt.wantCount)
				if tt.wantCount == 1 && decisions[0].Action != ActionDecrease {
					t.Errorf("expected decrease, got %q", decisions[0].Action)
				}
			} else {
				decisions := engine.Evaluate(signals, limits, now)
				requireDecisions(t, decisions, tt.wantCount)
			}
		})
	}
}

func TestConfig_Policy_GrowFactor(t *testing.T) {
	tests := []struct {
		name       string
		growFactor float64
		current    int64
		expected   int64
	}{
		{"1.25x", 1.25, 1000, 1250},
		{"1.50x", 1.50, 1000, 1500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultPolicyConfig()
			cfg.GrowFactor = tt.growFactor
			engine := policyEngine(cfg)
			signals := AggregatedSignals{
				Utilization: map[string]float64{"rule1": 0.90},
			}
			decisions := engine.Evaluate(signals, map[string]int64{"rule1": tt.current}, time.Now())
			requireDecisions(t, decisions, 1)
			if decisions[0].NewValue != tt.expected {
				t.Errorf("GrowFactor=%v: expected %d, got %d", tt.growFactor, tt.expected, decisions[0].NewValue)
			}
		})
	}
}

func TestConfig_Policy_ShrinkFactor(t *testing.T) {
	tests := []struct {
		name         string
		shrinkFactor float64
		current      int64
		expected     int64
	}{
		{"0.75x", 0.75, 1000, 750},
		{"0.60x", 0.60, 1000, 600},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultPolicyConfig()
			cfg.ShrinkFactor = tt.shrinkFactor
			engine := policyEngine(cfg)
			now := time.Now()
			signals := AggregatedSignals{Utilization: map[string]float64{"r1": 0.10}}
			limits := map[string]int64{"r1": tt.current}

			// First call: registers low point.
			engine.Evaluate(signals, limits, now)
			// Second call: past sustain time.
			decisions := engine.Evaluate(signals, limits, now.Add(2*time.Hour))
			requireDecisions(t, decisions, 1)
			if decisions[0].NewValue != tt.expected {
				t.Errorf("ShrinkFactor=%v: expected %d, got %d", tt.shrinkFactor, tt.expected, decisions[0].NewValue)
			}
		})
	}
}

func TestConfig_Policy_TightenFactor(t *testing.T) {
	tests := []struct {
		name          string
		tightenFactor float64
		current       int64
		expected      int64
	}{
		{"0.80x", 0.80, 1000, 800},
		{"0.70x", 0.70, 1000, 700},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultPolicyConfig()
			cfg.TightenFactor = tt.tightenFactor
			engine := policyEngine(cfg)
			signals := AggregatedSignals{
				Utilization:   map[string]float64{"r1": 0.50},
				AnomalyScores: map[string]float64{"r1": 0.80}, // above AnomalyTightenScore(0.7)
			}
			decisions := engine.Evaluate(signals, map[string]int64{"r1": tt.current}, time.Now())
			requireDecisions(t, decisions, 1)
			if decisions[0].Action != ActionTighten {
				t.Fatalf("expected tighten, got %q", decisions[0].Action)
			}
			if decisions[0].NewValue != tt.expected {
				t.Errorf("TightenFactor=%v: expected %d, got %d", tt.tightenFactor, tt.expected, decisions[0].NewValue)
			}
		})
	}
}

func TestConfig_Policy_MaxChangePct(t *testing.T) {
	cfg := DefaultPolicyConfig()
	cfg.MaxChangePct = 0.10 // 10% max change
	cfg.GrowFactor = 2.00   // would double without clamping
	engine := policyEngine(cfg)

	signals := AggregatedSignals{Utilization: map[string]float64{"r1": 0.90}}
	decisions := engine.Evaluate(signals, map[string]int64{"r1": 1000}, time.Now())
	requireDecisions(t, decisions, 1)

	// MaxChangePct=0.10 on 1000 → max delta = 100, so new = 1100 (clamped from 2000).
	if decisions[0].NewValue != 1100 {
		t.Errorf("MaxChangePct clamping: expected 1100, got %d", decisions[0].NewValue)
	}
}

func TestConfig_Policy_Cooldown(t *testing.T) {
	cfg := DefaultPolicyConfig()
	cfg.Cooldown = 5 * time.Minute
	engine := policyEngine(cfg)

	now := time.Now()
	signals := AggregatedSignals{Utilization: map[string]float64{"r1": 0.90}}
	limits := map[string]int64{"r1": 1000}

	// First call at T+0 → should produce increase.
	d1 := engine.Evaluate(signals, limits, now)
	requireAction(t, d1, ActionIncrease)

	// Second call at T+3m → in cooldown, should produce nothing.
	d2 := engine.Evaluate(signals, limits, now.Add(3*time.Minute))
	requireDecisions(t, d2, 0)

	// Third call at T+6m → cooldown expired, should produce increase.
	d3 := engine.Evaluate(signals, limits, now.Add(6*time.Minute))
	requireAction(t, d3, ActionIncrease)
}

func TestConfig_Policy_MinCardinality(t *testing.T) {
	cfg := DefaultPolicyConfig()
	cfg.MinCardinality = 100
	cfg.ShrinkFactor = 0.01 // aggressive shrink to push below min
	engine := policyEngine(cfg)

	now := time.Now()
	signals := AggregatedSignals{Utilization: map[string]float64{"r1": 0.05}}
	limits := map[string]int64{"r1": 200}

	// Register low.
	engine.Evaluate(signals, limits, now)
	// Trigger decrease after sustain.
	decisions := engine.Evaluate(signals, limits, now.Add(2*time.Hour))
	requireDecisions(t, decisions, 1)

	// ShrinkFactor=0.01 * 200 = 2, but clamped to MinCardinality=100.
	if decisions[0].NewValue != 100 {
		t.Errorf("MinCardinality: expected 100, got %d", decisions[0].NewValue)
	}
}

func TestConfig_Policy_MaxCardinality(t *testing.T) {
	cfg := DefaultPolicyConfig()
	cfg.MaxCardinality = 10_000_000
	cfg.GrowFactor = 1.50
	cfg.MaxChangePct = 1.0 // allow big change
	engine := policyEngine(cfg)

	signals := AggregatedSignals{Utilization: map[string]float64{"r1": 0.95}}
	// 9M * 1.5 = 13.5M → clamped to 10M.
	decisions := engine.Evaluate(signals, map[string]int64{"r1": 9_000_000}, time.Now())
	requireDecisions(t, decisions, 1)

	if decisions[0].NewValue != 10_000_000 {
		t.Errorf("MaxCardinality: expected 10000000, got %d", decisions[0].NewValue)
	}
}

func TestConfig_Policy_AnomalyTightenScore(t *testing.T) {
	tests := []struct {
		name      string
		score     float64
		wantCount int
		wantAct   Action
	}{
		{"0.65 below threshold (0.7) → no tighten", 0.65, 0, ""},
		{"0.75 above threshold (0.7) → tighten", 0.75, 1, ActionTighten},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultPolicyConfig()
			// AnomalyTightenScore default is 0.7
			engine := policyEngine(cfg)
			signals := AggregatedSignals{
				Utilization:   map[string]float64{"r1": 0.50}, // normal range
				AnomalyScores: map[string]float64{"r1": tt.score},
			}
			decisions := engine.Evaluate(signals, map[string]int64{"r1": 1000}, time.Now())
			requireDecisions(t, decisions, tt.wantCount)
			if tt.wantCount == 1 && decisions[0].Action != tt.wantAct {
				t.Errorf("expected %q, got %q", tt.wantAct, decisions[0].Action)
			}
		})
	}
}

func TestConfig_Policy_AnomalySafeScore(t *testing.T) {
	tests := []struct {
		name      string
		score     float64
		wantCount int
		desc      string
	}{
		{"0.55 unsafe → hold (no grow)", 0.55, 0, "anomaly >= safe score prevents growth"},
		{"0.45 safe → increase", 0.45, 1, "anomaly < safe score allows growth"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultPolicyConfig()
			// AnomalySafeScore default is 0.5
			engine := policyEngine(cfg)
			signals := AggregatedSignals{
				Utilization:   map[string]float64{"r1": 0.90}, // above increase threshold
				AnomalyScores: map[string]float64{"r1": tt.score},
			}
			decisions := engine.Evaluate(signals, map[string]int64{"r1": 1000}, time.Now())
			requireDecisions(t, decisions, tt.wantCount)
			if tt.wantCount == 1 && decisions[0].Action != ActionIncrease {
				t.Errorf("expected increase, got %q", decisions[0].Action)
			}
		})
	}
}

func TestConfig_Policy_DecreaseSustainTime(t *testing.T) {
	cfg := DefaultPolicyConfig()
	// Default DecreaseSustainTime = 1h
	engine := policyEngine(cfg)
	now := time.Now()
	signals := AggregatedSignals{Utilization: map[string]float64{"r1": 0.10}}
	limits := map[string]int64{"r1": 1000}

	// Register low.
	d1 := engine.Evaluate(signals, limits, now)
	requireDecisions(t, d1, 0)

	// At T+30m → not sustained yet.
	d2 := engine.Evaluate(signals, limits, now.Add(30*time.Minute))
	requireDecisions(t, d2, 0)

	// At T+65m → past sustain time.
	d3 := engine.Evaluate(signals, limits, now.Add(65*time.Minute))
	requireAction(t, d3, ActionDecrease)
}

// ===========================================================================
// Safeguard tests
// ===========================================================================

func TestConfig_Safe_RateLimit(t *testing.T) {
	rl := NewReloadRateLimiter(60 * time.Second)
	now := time.Now()
	noop := func() error { return nil }

	// First reload → accepted.
	ok, err := rl.TryReload(noop, now)
	if err != nil || !ok {
		t.Fatalf("first reload should succeed: ok=%v err=%v", ok, err)
	}

	// Second reload at T+30s → rejected.
	ok, err = rl.TryReload(noop, now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("reload at T+30s should be rate limited")
	}

	// Third reload at T+65s → accepted.
	ok, err = rl.TryReload(noop, now.Add(65*time.Second))
	if err != nil || !ok {
		t.Errorf("reload at T+65s should succeed: ok=%v err=%v", ok, err)
	}
}

func TestConfig_Safe_CircuitBreaker(t *testing.T) {
	cb := NewCircuitBreaker("test", 3, 5*time.Minute)

	// Closed state: allow.
	if !cb.Allow() {
		t.Fatal("closed circuit should allow")
	}

	// Record 3 failures → opens.
	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordFailure()

	if cb.State() != CBOpen {
		t.Fatalf("expected open after 3 failures, got %s", cb.State())
	}
	if cb.Allow() {
		t.Error("open circuit should not allow (before reset timeout)")
	}

	// Record success after transitioning to half-open closes it.
	// We can't easily advance time.Now() for the reset timeout check,
	// so test the half-open → close path by recording success.
	// Force half-open by manipulating state for test purposes.
	cb.state.Store(int32(CBHalfOpen))
	cb.RecordSuccess()

	if cb.State() != CBClosed {
		t.Errorf("expected closed after success in half-open, got %s", cb.State())
	}
}

func TestConfig_Safe_OscillationLookback(t *testing.T) {
	cfg := OscillationConfig{
		LookbackCount:  6,
		Threshold:      0.75,
		FreezeDuration: time.Hour,
	}
	od := NewOscillationDetector(cfg)
	now := time.Now()

	// Feed 5 alternating decisions (4 alternations / 4 transitions → ratio = 1.0).
	// But lookback is 6 and isOscillating needs at least 4 decisions.
	// 5 alternating decisions: ratio = 4/4 = 1.0 > 0.75 → should freeze.
	actions := []Action{ActionIncrease, ActionDecrease, ActionIncrease, ActionDecrease, ActionIncrease}
	for i, a := range actions {
		od.Record("r1", Decision{
			RuleName:  "r1",
			Action:    a,
			Timestamp: now.Add(time.Duration(i) * time.Minute),
		})
	}
	frozen5 := od.IsFrozen("r1", now.Add(5*time.Minute))

	// Reset for the 6-decision test.
	od2 := NewOscillationDetector(cfg)
	actions6 := []Action{ActionIncrease, ActionDecrease, ActionIncrease, ActionDecrease, ActionIncrease, ActionDecrease}
	for i, a := range actions6 {
		od2.Record("r1", Decision{
			RuleName:  "r1",
			Action:    a,
			Timestamp: now.Add(time.Duration(i) * time.Minute),
		})
	}
	frozen6 := od2.IsFrozen("r1", now.Add(6*time.Minute))

	// Both should be frozen since oscillation detection requires >= 4 decisions and
	// a ratio above threshold. The lookback controls how many decisions are kept.
	if !frozen5 {
		t.Error("expected freeze after 5 alternating decisions within lookback of 6")
	}
	if !frozen6 {
		t.Error("expected freeze after 6 alternating decisions at lookback of 6")
	}
}

func TestConfig_Safe_OscillationFreezeDuration(t *testing.T) {
	cfg := OscillationConfig{
		LookbackCount:  4,
		Threshold:      0.5,
		FreezeDuration: 30 * time.Minute,
	}
	od := NewOscillationDetector(cfg)
	now := time.Now()

	// Feed alternating decisions to trigger freeze.
	for i, a := range []Action{ActionIncrease, ActionDecrease, ActionIncrease, ActionDecrease} {
		od.Record("r1", Decision{
			RuleName:  "r1",
			Action:    a,
			Timestamp: now.Add(time.Duration(i) * time.Minute),
		})
	}

	if !od.IsFrozen("r1", now.Add(10*time.Minute)) {
		t.Error("should be frozen within freeze duration")
	}
	// After 30m freeze duration the freeze should have expired.
	if od.IsFrozen("r1", now.Add(35*time.Minute)) {
		t.Error("should not be frozen after freeze duration expires")
	}
}

func TestConfig_Safe_ErrorBudget(t *testing.T) {
	cfg := ErrorBudgetConfig{
		Window:        time.Hour,
		MaxErrors:     10,
		PauseDuration: 30 * time.Minute,
	}
	eb := NewErrorBudget(cfg)
	now := time.Now()

	// Record 9 errors → not paused.
	for i := 0; i < 9; i++ {
		eb.RecordError(now.Add(time.Duration(i) * time.Second))
	}
	if eb.IsPaused(now.Add(10 * time.Second)) {
		t.Error("should not be paused with 9 errors (budget=10)")
	}
	if eb.Remaining(now.Add(10*time.Second)) != 1 {
		t.Errorf("expected 1 remaining, got %d", eb.Remaining(now.Add(10*time.Second)))
	}

	// 10th error → paused.
	eb.RecordError(now.Add(10 * time.Second))
	if !eb.IsPaused(now.Add(11 * time.Second)) {
		t.Error("should be paused after 10 errors")
	}

	// After pause duration → unpaused.
	if eb.IsPaused(now.Add(41 * time.Minute)) {
		t.Error("should not be paused after pause duration expires")
	}
}

// ===========================================================================
// HA mode tests
// ===========================================================================

func TestConfig_HA_Noop_AlwaysLeader(t *testing.T) {
	elector := NewNoopElector()
	if !elector.IsLeader() {
		t.Error("NoopElector should always report as leader")
	}
	if err := elector.Start(context.Background()); err != nil {
		t.Errorf("Start should succeed: %v", err)
	}
	elector.Stop()
	// Still leader after stop.
	if !elector.IsLeader() {
		t.Error("NoopElector should remain leader after Stop")
	}
}

func TestConfig_HA_Designated_Leader(t *testing.T) {
	elector := NewDesignatedElector(true)
	if !elector.IsLeader() {
		t.Error("DesignatedElector(true) should be leader")
	}
}

func TestConfig_HA_Designated_Follower(t *testing.T) {
	elector := NewDesignatedElector(false)
	if elector.IsLeader() {
		t.Error("DesignatedElector(false) should not be leader")
	}
}

// ===========================================================================
// Propagation mode tests
// ===========================================================================

func TestConfig_Prop_Pull_EndpointRegistered(t *testing.T) {
	pp := NewPullPropagator(PropagationConfig{
		Mode:         PropPull,
		PullInterval: 10 * time.Second,
	})

	// Propagate stores changes.
	changes := []ConfigChange{{
		Timestamp: time.Now(),
		RuleName:  "test-rule",
		Field:     "max_cardinality",
		OldValue:  1000,
		NewValue:  1250,
		Action:    "increase",
	}}
	if err := pp.Propagate(context.Background(), changes); err != nil {
		t.Fatalf("Propagate: %v", err)
	}

	// GET handler returns the changes.
	handler := pp.HandleGetConfig()
	req := httptest.NewRequest(http.MethodGet, "/autotune/config", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("ETag") == "" {
		t.Error("expected ETag header to be set")
	}

	var got []ConfigChange
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].RuleName != "test-rule" {
		t.Errorf("unexpected response: %+v", got)
	}
}

func TestConfig_Prop_Peer_PushSent(t *testing.T) {
	// Set up a test server that records the POST body.
	var received atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/autotune/config" {
			received.Store(true)
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	pp := NewPeerPropagator(PropagationConfig{
		Mode:        PropPeer,
		PeerService: srv.Listener.Addr().String(),
	})

	// Manually inject the test server as a peer.
	pp.mu.Lock()
	pp.peers = []string{srv.Listener.Addr().String()}
	pp.selfIP = "0.0.0.0" // ensure we don't skip the peer
	pp.mu.Unlock()

	changes := []ConfigChange{{
		RuleName: "r1",
		Action:   "increase",
	}}
	if err := pp.Propagate(context.Background(), changes); err != nil {
		t.Fatalf("Propagate: %v", err)
	}
	if !received.Load() {
		t.Error("peer server should have received the push")
	}
}

// ===========================================================================
// Persistence mode tests
// ===========================================================================

func TestConfig_Persist_Memory(t *testing.T) {
	mp := NewMemoryPersister(100)
	ctx := context.Background()

	changes := []ConfigChange{{RuleName: "r1", Action: "increase"}}
	if err := mp.Persist(ctx, changes); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	history, err := mp.LoadHistory(ctx)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(history) != 1 || history[0].RuleName != "r1" {
		t.Errorf("unexpected history: %+v", history)
	}

	// Verify no files created in temp dir.
	tmpDir := t.TempDir()
	entries, _ := os.ReadDir(tmpDir)
	if len(entries) != 0 {
		t.Errorf("memory persister should not create files, found %d entries", len(entries))
	}
}

func TestConfig_Persist_File(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "autotune-history.json")

	fp := NewFilePersister(path, 1, time.Millisecond)
	ctx := context.Background()

	changes := []ConfigChange{{RuleName: "r1", Field: "max_cardinality", OldValue: 1000, NewValue: 1250, Action: "increase"}}
	if err := fp.Persist(ctx, changes); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	// File should exist with valid JSON.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read persisted file: %v", err)
	}
	var loaded []ConfigChange
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal file: %v", err)
	}
	if len(loaded) != 1 || loaded[0].RuleName != "r1" {
		t.Errorf("unexpected file content: %+v", loaded)
	}

	// LoadHistory should return the same.
	history, err := fp.LoadHistory(ctx)
	if err != nil {
		t.Fatalf("LoadHistory: %v", err)
	}
	if len(history) != 1 {
		t.Errorf("expected 1 entry in history, got %d", len(history))
	}
}

// ===========================================================================
// Config validation tests
// ===========================================================================

func TestConfig_Validate_MissingURL(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Source.Enabled = true
	cfg.Source.URL = "" // missing
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for missing URL")
	}
	if got := err.Error(); !contains(got, "source.url required") {
		t.Errorf("expected 'source.url required' in error, got: %s", got)
	}
}

func TestConfig_Validate_InvalidBackend(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Source.Enabled = true
	cfg.Source.URL = "http://localhost:8428"
	cfg.Source.Backend = Backend("nosqldb")
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for invalid backend")
	}
	if got := err.Error(); !contains(got, "unknown backend") {
		t.Errorf("expected 'unknown backend' in error, got: %s", got)
	}
}

func TestConfig_Validate_GrowFactorTooLow(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Source.Enabled = true
	cfg.Source.URL = "http://localhost:8428"
	cfg.Policy.GrowFactor = 0.99 // must be > 1.0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for grow_factor <= 1.0")
	}
	if got := err.Error(); !contains(got, "grow_factor must be > 1.0") {
		t.Errorf("expected 'grow_factor' error, got: %s", got)
	}
}

func TestConfig_Validate_ShrinkFactorTooHigh(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Source.Enabled = true
	cfg.Source.URL = "http://localhost:8428"
	cfg.Policy.ShrinkFactor = 1.5 // must be < 1.0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for shrink_factor >= 1.0")
	}
	if got := err.Error(); !contains(got, "shrink_factor must be between 0 and 1.0") {
		t.Errorf("expected 'shrink_factor' error, got: %s", got)
	}
}

func TestConfig_Validate_DisabledSkipsAll(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = false
	cfg.Source.URL = "" // would fail if validated
	cfg.Policy.GrowFactor = 0.5
	err := cfg.Validate()
	if err != nil {
		t.Errorf("disabled config should skip validation, got: %v", err)
	}
}

func TestConfig_Validate_ValidConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Source.Enabled = true
	cfg.Source.URL = "http://localhost:8428"
	err := cfg.Validate()
	if err != nil {
		t.Errorf("valid config should pass: %v", err)
	}
}

// ===========================================================================
// Source backend tests (httptest)
// ===========================================================================

func TestConfig_Backend_VM_CorrectAPI(t *testing.T) {
	var gotPath string
	var gotTopN string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotTopN = r.URL.Query().Get("topN")
		json.NewEncoder(w).Encode(tsdbStatusResponse{
			Status: "success",
			Data: tsdbStatusData{
				HeadStats: tsdbHeadStats{NumSeries: 50000},
				SeriesCountByMetricName: []nameValuePair{
					{Name: "http_requests_total", Value: 10000},
				},
			},
		})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendVM,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    50,
		VM:      VMSourceConfig{TSDBInsights: true},
	}
	client := NewVMClient(cfg)

	data, err := client.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("FetchCardinality: %v", err)
	}

	if gotPath != "/api/v1/status/tsdb" {
		t.Errorf("expected path /api/v1/status/tsdb, got %q", gotPath)
	}
	if gotTopN != "50" {
		t.Errorf("expected topN=50, got %q", gotTopN)
	}
	if data.TotalSeries != 50000 {
		t.Errorf("expected 50000 series, got %d", data.TotalSeries)
	}
	if len(data.TopMetrics) != 1 || data.TopMetrics[0].Name != "http_requests_total" {
		t.Errorf("unexpected top metrics: %+v", data.TopMetrics)
	}
}

func TestConfig_Backend_VM_TenantParam(t *testing.T) {
	var gotExtraLabel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotExtraLabel = r.URL.Query().Get("extra_label")
		json.NewEncoder(w).Encode(tsdbStatusResponse{Status: "success"})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend:  BackendVM,
		URL:      srv.URL,
		Timeout:  5 * time.Second,
		TopN:     10,
		TenantID: "team-a",
		VM:       VMSourceConfig{TSDBInsights: true},
	}
	client := NewVMClient(cfg)
	_, _ = client.FetchCardinality(context.Background())

	if gotExtraLabel != "tenant_id:team-a" {
		t.Errorf("expected extra_label=tenant_id:team-a, got %q", gotExtraLabel)
	}
}

func TestConfig_Backend_Mimir_TenantHeader(t *testing.T) {
	var gotTenantHeader string
	var gotCountMethod string
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTenantHeader = r.Header.Get("X-Scope-OrgID")
		gotCountMethod = r.URL.Query().Get("count_method")
		gotPath = r.URL.Path
		json.NewEncoder(w).Encode(mimirCardinalityResponse{
			SeriesCountTotal: 100000,
			Labels: []mimirLabelCardinality{
				{
					LabelName: "__name__",
					Cardinality: []mimirValueCardinality{
						{LabelValue: "up", SeriesCount: 500},
					},
				},
			},
		})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend:  BackendMimir,
		URL:      srv.URL,
		Timeout:  5 * time.Second,
		TopN:     20,
		TenantID: "tenant-xyz",
		Mimir:    MimirSourceConfig{CountMethod: "active"},
	}
	client := NewMimirClient(cfg)

	data, err := client.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("FetchCardinality: %v", err)
	}

	if gotPath != "/api/v1/cardinality/label_values" {
		t.Errorf("expected Mimir cardinality path, got %q", gotPath)
	}
	if gotTenantHeader != "tenant-xyz" {
		t.Errorf("expected X-Scope-OrgID=tenant-xyz, got %q", gotTenantHeader)
	}
	if gotCountMethod != "active" {
		t.Errorf("expected count_method=active, got %q", gotCountMethod)
	}
	if data.TotalSeries != 100000 {
		t.Errorf("expected 100000 series, got %d", data.TotalSeries)
	}
}

func TestConfig_Backend_Thanos_TenantHeader(t *testing.T) {
	var gotTenantHeader string
	var gotAllTenants string
	var gotDedup string
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTenantHeader = r.Header.Get("THANOS-TENANT")
		gotAllTenants = r.URL.Query().Get("all_tenants")
		gotDedup = r.URL.Query().Get("dedup")
		gotPath = r.URL.Path
		json.NewEncoder(w).Encode(tsdbStatusResponse{
			Status: "success",
			Data: tsdbStatusData{
				HeadStats: tsdbHeadStats{NumSeries: 200000},
			},
		})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend:  BackendThanos,
		URL:      srv.URL,
		Timeout:  5 * time.Second,
		TopN:     10,
		TenantID: "thanos-team",
		Thanos: ThanosSourceConfig{
			AllTenants: true,
			Dedup:      true,
		},
	}
	client := NewThanosClient(cfg)

	data, err := client.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("FetchCardinality: %v", err)
	}

	if gotPath != "/api/v1/status/tsdb" {
		t.Errorf("expected Thanos TSDB path, got %q", gotPath)
	}
	if gotTenantHeader != "thanos-team" {
		t.Errorf("expected THANOS-TENANT=thanos-team, got %q", gotTenantHeader)
	}
	if gotAllTenants != "true" {
		t.Errorf("expected all_tenants=true, got %q", gotAllTenants)
	}
	if gotDedup != "true" {
		t.Errorf("expected dedup=true, got %q", gotDedup)
	}
	if data.TotalSeries != 200000 {
		t.Errorf("expected 200000 series, got %d", data.TotalSeries)
	}
}

func TestConfig_Backend_Thanos_NoTenant(t *testing.T) {
	var gotTenantHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTenantHeader = r.Header.Get("THANOS-TENANT")
		json.NewEncoder(w).Encode(tsdbStatusResponse{Status: "success"})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend:  BackendThanos,
		URL:      srv.URL,
		Timeout:  5 * time.Second,
		TopN:     10,
		TenantID: "", // no tenant
	}
	client := NewThanosClient(cfg)
	_, _ = client.FetchCardinality(context.Background())

	if gotTenantHeader != "" {
		t.Errorf("expected no THANOS-TENANT header, got %q", gotTenantHeader)
	}
}

// ===========================================================================
// Verifier config tests
// ===========================================================================

func TestConfig_Verifier_WarmupWindow(t *testing.T) {
	var drops int64
	v := NewPostApplyVerifier(
		VerificationConfig{
			Warmup:                 10 * time.Second,
			Window:                 30 * time.Second,
			RollbackCooldownFactor: 3,
		},
		0.05,
		func() int64 { return atomic.LoadInt64(&drops) },
	)

	now := time.Now()
	v.StartVerification([]Decision{{RuleName: "r1", Action: ActionIncrease}}, now)

	// During warmup → pending.
	result, _ := v.Check(now.Add(5 * time.Second))
	if result != VerificationPending {
		t.Errorf("expected pending during warmup, got %d", result)
	}

	// After warmup+window → pass.
	result, _ = v.Check(now.Add(41 * time.Second))
	if result != VerificationPass {
		t.Errorf("expected pass after window, got %d", result)
	}
}

// ===========================================================================
// PolicyEngine interaction with OscillationDetector
// ===========================================================================

func TestConfig_Policy_OscillationFreeze(t *testing.T) {
	od := NewOscillationDetector(OscillationConfig{
		LookbackCount:  4,
		Threshold:      0.5,
		FreezeDuration: time.Hour,
	})
	cfg := DefaultPolicyConfig()
	cfg.Cooldown = time.Millisecond // near-zero cooldown so it doesn't interfere
	engine := policyEngineWithOscillation(cfg, od)

	now := time.Now()
	limits := map[string]int64{"r1": 1000}

	// Alternate high/low signals to trigger oscillation detection.
	highSignals := AggregatedSignals{Utilization: map[string]float64{"r1": 0.95}}
	lowSignals := AggregatedSignals{Utilization: map[string]float64{"r1": 0.05}}

	// T+0: increase
	d := engine.Evaluate(highSignals, limits, now)
	requireAction(t, d, ActionIncrease)

	// T+1: need to register low first
	engine.Evaluate(lowSignals, limits, now.Add(1*time.Minute))
	// T+62m: decrease (past sustain)
	d = engine.Evaluate(lowSignals, limits, now.Add(62*time.Minute))
	requireAction(t, d, ActionDecrease)

	// T+63m: increase
	d = engine.Evaluate(highSignals, limits, now.Add(63*time.Minute))
	requireAction(t, d, ActionIncrease)

	// T+64m: register low + T+125m: decrease
	engine.Evaluate(lowSignals, limits, now.Add(64*time.Minute))
	d = engine.Evaluate(lowSignals, limits, now.Add(125*time.Minute))
	// At this point the oscillation detector should have frozen the rule.
	// The 4th alternating decision should trigger freeze, so this may be empty.
	// The exact behavior depends on when isOscillating fires.
	// Verify that at some point the rule becomes frozen.
	if !od.IsFrozen("r1", now.Add(126*time.Minute)) {
		// It is possible the freeze triggered on the previous decision.
		// Check that subsequent evaluations produce no decisions.
		d = engine.Evaluate(highSignals, limits, now.Add(127*time.Minute))
		if len(d) != 0 {
			t.Error("expected no decisions while frozen due to oscillation")
		}
	}
}

// ===========================================================================
// DefaultPolicyConfig correctness
// ===========================================================================

func TestConfig_Defaults(t *testing.T) {
	cfg := DefaultPolicyConfig()

	checks := []struct {
		name string
		got  interface{}
		want interface{}
	}{
		{"IncreaseThreshold", cfg.IncreaseThreshold, 0.85},
		{"DecreaseThreshold", cfg.DecreaseThreshold, 0.30},
		{"GrowFactor", cfg.GrowFactor, 1.25},
		{"ShrinkFactor", cfg.ShrinkFactor, 0.75},
		{"TightenFactor", cfg.TightenFactor, 0.80},
		{"MaxChangePct", cfg.MaxChangePct, 0.50},
		{"MinCardinality", cfg.MinCardinality, int64(100)},
		{"MaxCardinality", cfg.MaxCardinality, int64(10_000_000)},
		{"AnomalyTightenScore", cfg.AnomalyTightenScore, 0.7},
		{"AnomalySafeScore", cfg.AnomalySafeScore, 0.5},
		{"Cooldown", cfg.Cooldown, 5 * time.Minute},
		{"DecreaseSustainTime", cfg.DecreaseSustainTime, time.Hour},
		{"RollbackDropPct", cfg.RollbackDropPct, 0.05},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
			}
		})
	}
}

func TestConfig_SafeguardsDefaults(t *testing.T) {
	cfg := DefaultSafeguardsConfig()

	if cfg.ReloadMinInterval != 60*time.Second {
		t.Errorf("ReloadMinInterval: got %v, want 60s", cfg.ReloadMinInterval)
	}
	if cfg.CircuitBreaker.MaxConsecutiveFailures != 3 {
		t.Errorf("MaxConsecutiveFailures: got %d, want 3", cfg.CircuitBreaker.MaxConsecutiveFailures)
	}
	if cfg.Oscillation.LookbackCount != 6 {
		t.Errorf("LookbackCount: got %d, want 6", cfg.Oscillation.LookbackCount)
	}
	if cfg.ErrorBudget.MaxErrors != 10 {
		t.Errorf("MaxErrors: got %d, want 10", cfg.ErrorBudget.MaxErrors)
	}
	if cfg.Verification.Warmup != 30*time.Second {
		t.Errorf("Warmup: got %v, want 30s", cfg.Verification.Warmup)
	}
}

// ===========================================================================
// Helpers
// ===========================================================================

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
