package autotune

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func BenchmarkPolicyEngine_Evaluate(b *testing.B) {
	engine := NewPolicyEngine(DefaultPolicyConfig(), nil)
	signals := AggregatedSignals{
		Utilization: map[string]float64{
			"rule1": 0.60,
			"rule2": 0.90,
			"rule3": 0.20,
		},
	}
	limits := map[string]int64{
		"rule1": 1000,
		"rule2": 5000,
		"rule3": 2000,
	}
	now := time.Now()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.Evaluate(signals, limits, now.Add(time.Duration(i)*time.Hour))
	}
}

func BenchmarkCircuitBreaker_ClosedCheck(b *testing.B) {
	cb := NewCircuitBreaker("bench", 10, time.Minute)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if cb.Allow() {
			cb.RecordSuccess()
		}
	}
}

func BenchmarkReloadRateLimiter_Check(b *testing.B) {
	rl := NewReloadRateLimiter(time.Nanosecond) // allow every call
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rl.TryReload(func() error { return nil }, time.Now())
	}
}

func BenchmarkOscillationDetector_Record(b *testing.B) {
	od := NewOscillationDetector(OscillationConfig{
		LookbackCount:  6,
		Threshold:      0.75,
		FreezeDuration: time.Minute,
	})
	d := Decision{Action: ActionIncrease, RuleName: "rule1"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		od.Record("rule1", d)
	}
}

func BenchmarkErrorBudget_RecordError(b *testing.B) {
	eb := NewErrorBudget(ErrorBudgetConfig{
		Window:        time.Hour,
		MaxErrors:     1000000,
		PauseDuration: time.Minute,
	})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		eb.RecordError(time.Now())
	}
}

func BenchmarkVMClient_FetchCardinality(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"headStats":{"numSeries":50000},"seriesCountByMetricName":[{"name":"m1","value":10000},{"name":"m2","value":8000}]}`)
	}))
	defer server.Close()

	client := NewVMClient(SourceConfig{URL: server.URL, TopN: 10, VM: VMSourceConfig{TSDBInsights: true}})
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		client.FetchCardinality(ctx)
	}
}

func BenchmarkMemoryPersister_Persist(b *testing.B) {
	mp := NewMemoryPersister(10000)
	changes := []ConfigChange{
		{Action: "increase", RuleName: "rule_a", NewValue: 1500},
	}
	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mp.Persist(ctx, changes)
	}
}
