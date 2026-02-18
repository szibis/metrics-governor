package autotune

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestIntegration_PolicyAndSafeguards_CooldownAfterAction(t *testing.T) {
	// Policy engine + rate limiter: action at T+0, rejected at T+2s, allowed at T+5s.
	cfg := DefaultPolicyConfig()
	cfg.Cooldown = 3 * time.Second
	engine := NewPolicyEngine(cfg, nil)

	signals := AggregatedSignals{
		Utilization: map[string]float64{"rule1": 0.90},
	}
	limits := map[string]int64{"rule1": 1000}
	now := time.Now()

	// First eval: should increase.
	d1 := engine.Evaluate(signals, limits, now)
	if len(d1) != 1 || d1[0].Action != ActionIncrease {
		t.Fatalf("expected INCREASE, got %+v", d1)
	}

	// Second eval at T+2s: should be on cooldown.
	d2 := engine.Evaluate(signals, limits, now.Add(2*time.Second))
	if len(d2) != 0 {
		t.Fatalf("expected no decisions during cooldown, got %+v", d2)
	}

	// Third eval at T+5s: cooldown expired.
	d3 := engine.Evaluate(signals, limits, now.Add(5*time.Second))
	if len(d3) != 1 || d3[0].Action != ActionIncrease {
		t.Fatalf("expected INCREASE after cooldown, got %+v", d3)
	}
}

func TestIntegration_CircuitBreaker_SourceFailure(t *testing.T) {
	cb := NewCircuitBreaker("test", 3, 100*time.Millisecond)

	// 3 failures → open.
	for i := 0; i < 3; i++ {
		if cb.Allow() {
			cb.RecordFailure()
		}
	}
	if cb.State() != CBOpen {
		t.Fatalf("expected open, got %s", cb.State())
	}

	// While open, Allow() returns false.
	if cb.Allow() {
		t.Fatal("expected Allow()=false while open")
	}

	// Wait for reset timeout (100ms) + margin for race detector.
	time.Sleep(300 * time.Millisecond)

	// Half-open: Allow() returns true for one probe.
	if !cb.Allow() {
		t.Fatal("expected Allow()=true in half-open")
	}
	cb.RecordSuccess()

	if cb.State() != CBClosed {
		t.Fatalf("expected closed after success, got %s", cb.State())
	}
}

func TestIntegration_ErrorBudget_PauseAndResume(t *testing.T) {
	eb := NewErrorBudget(ErrorBudgetConfig{
		Window:        time.Hour,
		MaxErrors:     5,
		PauseDuration: 10 * time.Minute,
	})
	now := time.Now()

	// 4 errors: not paused.
	for i := 0; i < 4; i++ {
		eb.RecordError(now.Add(time.Duration(i) * time.Second))
	}
	if eb.IsPaused(now.Add(5 * time.Second)) {
		t.Fatal("should not be paused at 4 errors")
	}

	// 5th error: paused.
	eb.RecordError(now.Add(5 * time.Second))
	if !eb.IsPaused(now.Add(6 * time.Second)) {
		t.Fatal("should be paused at 5 errors")
	}

	// After pause duration expires (10m from 5th error at +5s = +10m5s).
	if eb.IsPaused(now.Add(11 * time.Minute)) {
		t.Fatal("should have resumed after pause duration")
	}
}

func TestIntegration_PullPropagator_FullRoundTrip(t *testing.T) {
	// Leader propagates → follower receives via HTTP.
	leader := NewPullPropagator(PropagationConfig{})
	changes := []ConfigChange{{Action: "increase", RuleName: "rule_a", NewValue: 2000}}
	leader.Propagate(context.Background(), changes)

	// Serve via HTTP.
	server := httptest.NewServer(leader.HandleGetConfig())
	defer server.Close()

	// Follower polls.
	var received []ConfigChange
	follower := NewPullPropagator(PropagationConfig{
		PeerService:  server.Listener.Addr().String(),
		PullInterval: 50 * time.Millisecond,
	})
	follower.ReceiveUpdates(func(c []ConfigChange) {
		received = c
	})

	etag, err := follower.fetchFromLeader(context.Background(), "")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if etag == "" {
		t.Error("expected non-empty etag")
	}
	if len(received) != 1 || received[0].NewValue != 2000 {
		t.Errorf("unexpected received: %+v", received)
	}
}

func TestIntegration_MemoryPersister_StoresHistory(t *testing.T) {
	mp := NewMemoryPersister(100)

	changes := []ConfigChange{
		{Action: "increase", RuleName: "rule_a", NewValue: 1500},
		{Action: "decrease", RuleName: "rule_b", NewValue: 500},
	}
	if err := mp.Persist(context.Background(), changes); err != nil {
		t.Fatalf("persist: %v", err)
	}

	history, err := mp.LoadHistory(context.Background())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(history) != 2 {
		t.Errorf("expected 2 history entries, got %d", len(history))
	}
}

func TestIntegration_DesignatedElector_LeaderFollower(t *testing.T) {
	leader := NewDesignatedElector(true)
	follower := NewDesignatedElector(false)

	leader.Start(context.Background())
	follower.Start(context.Background())

	if !leader.IsLeader() {
		t.Error("leader should be leader")
	}
	if follower.IsLeader() {
		t.Error("follower should not be leader")
	}

	leader.Stop()
	follower.Stop()
}

func TestIntegration_VMSource_WithCircuitBreaker(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n <= 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"success","data":{"headStats":{"numSeries":5000},"seriesCountByMetricName":[{"name":"http_requests","value":1000}]}}`)
	}))
	defer server.Close()

	cb := NewCircuitBreaker("test-vm", 3, 100*time.Millisecond)
	client := NewVMClient(SourceConfig{URL: server.URL, TopN: 10, VM: VMSourceConfig{TSDBInsights: true}})

	// First 3 calls fail via circuit breaker wrapping.
	for i := 0; i < 3; i++ {
		if cb.Allow() {
			_, err := client.FetchCardinality(context.Background())
			if err != nil {
				cb.RecordFailure()
			} else {
				cb.RecordSuccess()
			}
		}
	}

	if cb.State() != CBOpen {
		t.Fatalf("expected open after 3 failures, got %s", cb.State())
	}

	// Wait for reset timeout (100ms) + margin for race detector.
	time.Sleep(300 * time.Millisecond)

	var data *CardinalityData
	allowed := cb.Allow()
	if !allowed {
		t.Fatalf("expected Allow()=true after reset timeout, state=%s", cb.State())
	}
	var fetchErr error
	data, fetchErr = client.FetchCardinality(context.Background())
	if fetchErr != nil {
		cb.RecordFailure()
		t.Fatalf("expected success on 4th call, got error: %v (server calls=%d)", fetchErr, calls.Load())
	}
	cb.RecordSuccess()

	if data == nil {
		t.Fatal("expected data after recovery")
	}
	if data.TotalSeries != 5000 {
		t.Errorf("expected 5000 series, got %d", data.TotalSeries)
	}
}
