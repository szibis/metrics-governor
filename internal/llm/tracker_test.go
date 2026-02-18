package llm

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
)

// --- Test Helpers ---

// makeTokenMetrics builds []*metricspb.ResourceMetrics with Sum-type token usage data.
func makeTokenMetrics(provider, model string, inputTokens, outputTokens float64) []*metricspb.ResourceMetrics {
	dps := make([]*metricspb.NumberDataPoint, 0, 2)
	if inputTokens > 0 {
		dps = append(dps, &metricspb.NumberDataPoint{
			Attributes: makeGenAIAttrs(provider, model, "input"),
			Value:      &metricspb.NumberDataPoint_AsDouble{AsDouble: inputTokens},
		})
	}
	if outputTokens > 0 {
		dps = append(dps, &metricspb.NumberDataPoint{
			Attributes: makeGenAIAttrs(provider, model, "output"),
			Value:      &metricspb.NumberDataPoint_AsDouble{AsDouble: outputTokens},
		})
	}
	return []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "gen_ai.client.token.usage",
							Data: &metricspb.Metric_Sum{
								Sum: &metricspb.Sum{DataPoints: dps},
							},
						},
					},
				},
			},
		},
	}
}

// makeGaugeTokenMetrics builds token metrics using Gauge type.
func makeGaugeTokenMetrics(provider, model string, inputTokens float64) []*metricspb.ResourceMetrics {
	return []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "gen_ai.client.token.usage",
							Data: &metricspb.Metric_Gauge{
								Gauge: &metricspb.Gauge{
									DataPoints: []*metricspb.NumberDataPoint{
										{
											Attributes: makeGenAIAttrs(provider, model, "input"),
											Value:      &metricspb.NumberDataPoint_AsDouble{AsDouble: inputTokens},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// makeHistogramTokenMetrics builds token metrics using Histogram type.
func makeHistogramTokenMetrics(provider, model string, sum float64) []*metricspb.ResourceMetrics {
	s := sum
	return []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "gen_ai.client.token.usage",
							Data: &metricspb.Metric_Histogram{
								Histogram: &metricspb.Histogram{
									DataPoints: []*metricspb.HistogramDataPoint{
										{
											Attributes: makeGenAIAttrs(provider, model, "input"),
											Sum:        &s,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// makeNonGenAIMetrics builds metrics that don't match gen_ai prefix.
func makeNonGenAIMetrics(count int) []*metricspb.ResourceMetrics {
	metrics := make([]*metricspb.Metric, count)
	for i := 0; i < count; i++ {
		metrics[i] = &metricspb.Metric{
			Name: "http.server.request.duration",
			Data: &metricspb.Metric_Sum{
				Sum: &metricspb.Sum{
					DataPoints: []*metricspb.NumberDataPoint{
						{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 1.0}},
					},
				},
			},
		}
	}
	return []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{Metrics: metrics},
			},
		},
	}
}

func makeGenAIAttrs(provider, model, tokenType string) []*commonpb.KeyValue {
	attrs := []*commonpb.KeyValue{
		{Key: "gen_ai.system", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: provider}}},
		{Key: "gen_ai.request.model", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: model}}},
	}
	if tokenType != "" {
		attrs = append(attrs, &commonpb.KeyValue{
			Key:   "gen_ai.token.type",
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: tokenType}},
		})
	}
	return attrs
}

// --- Unit Tests ---

func TestNewTracker(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	if tracker.config.TokenMetric != DefaultTokenMetric {
		t.Errorf("expected default token metric %q, got %q", DefaultTokenMetric, tracker.config.TokenMetric)
	}
	if tracker.config.BudgetWindow != DefaultBudgetWindow {
		t.Errorf("expected default budget window %v, got %v", DefaultBudgetWindow, tracker.config.BudgetWindow)
	}
	if len(tracker.ring) != DefaultRingSize {
		t.Errorf("expected ring size %d, got %d", DefaultRingSize, len(tracker.ring))
	}
	if tracker.accum == nil {
		t.Error("expected accum map to be initialized")
	}
}

func TestNewTracker_CustomConfig(t *testing.T) {
	cfg := LLMConfig{
		Enabled:      true,
		TokenMetric:  "custom.token.metric",
		BudgetWindow: 12 * time.Hour,
	}
	tracker := NewTracker(cfg)
	if tracker.config.TokenMetric != "custom.token.metric" {
		t.Errorf("expected custom token metric, got %q", tracker.config.TokenMetric)
	}
	if tracker.config.BudgetWindow != 12*time.Hour {
		t.Errorf("expected 12h budget window, got %v", tracker.config.BudgetWindow)
	}
}

func TestObserveMetrics_Sum(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	rms := makeTokenMetrics("openai", "gpt-4", 1000, 500)
	tracker.ObserveMetrics(rms)

	key := modelKey{provider: "openai", model: "gpt-4"}
	acc, ok := tracker.accum[key]
	if !ok {
		t.Fatal("expected accumulator for openai/gpt-4")
	}
	if acc.inputTokens != 1000 {
		t.Errorf("expected 1000 input tokens, got %d", acc.inputTokens)
	}
	if acc.outputTokens != 500 {
		t.Errorf("expected 500 output tokens, got %d", acc.outputTokens)
	}
}

func TestObserveMetrics_IntValue(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	rms := []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "gen_ai.client.token.usage",
							Data: &metricspb.Metric_Sum{
								Sum: &metricspb.Sum{
									DataPoints: []*metricspb.NumberDataPoint{
										{
											Attributes: makeGenAIAttrs("openai", "gpt-4", "input"),
											Value:      &metricspb.NumberDataPoint_AsInt{AsInt: 750},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	tracker.ObserveMetrics(rms)

	key := modelKey{provider: "openai", model: "gpt-4"}
	acc := tracker.accum[key]
	if acc.inputTokens != 750 {
		t.Errorf("expected 750 input tokens from int value, got %d", acc.inputTokens)
	}
}

func TestObserveMetrics_Histogram(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	rms := makeHistogramTokenMetrics("anthropic", "claude-3", 2000)
	tracker.ObserveMetrics(rms)

	key := modelKey{provider: "anthropic", model: "claude-3"}
	acc, ok := tracker.accum[key]
	if !ok {
		t.Fatal("expected accumulator for anthropic/claude-3")
	}
	if acc.inputTokens != 2000 {
		t.Errorf("expected 2000 input tokens from histogram sum, got %d", acc.inputTokens)
	}
}

func TestObserveMetrics_Gauge(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	rms := makeGaugeTokenMetrics("openai", "gpt-3.5", 300)
	tracker.ObserveMetrics(rms)

	key := modelKey{provider: "openai", model: "gpt-3.5"}
	acc, ok := tracker.accum[key]
	if !ok {
		t.Fatal("expected accumulator for openai/gpt-3.5")
	}
	if acc.inputTokens != 300 {
		t.Errorf("expected 300 input tokens from gauge, got %d", acc.inputTokens)
	}
}

func TestObserveMetrics_WrongMetric(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	rms := makeNonGenAIMetrics(5)
	tracker.ObserveMetrics(rms)

	if len(tracker.accum) != 0 {
		t.Errorf("expected no accumulators for non-gen_ai metrics, got %d", len(tracker.accum))
	}
}

func TestObserveMetrics_MissingAttributes(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	// Metric with no attributes
	rms := []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "gen_ai.client.token.usage",
							Data: &metricspb.Metric_Sum{
								Sum: &metricspb.Sum{
									DataPoints: []*metricspb.NumberDataPoint{
										{
											Attributes: nil,
											Value:      &metricspb.NumberDataPoint_AsDouble{AsDouble: 100},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	tracker.ObserveMetrics(rms)

	// Should accumulate under "unknown"/"unknown"
	key := modelKey{provider: "unknown", model: "unknown"}
	acc, ok := tracker.accum[key]
	if !ok {
		t.Fatal("expected accumulator for unknown/unknown when attributes missing")
	}
	if acc.inputTokens != 100 {
		t.Errorf("expected 100 input tokens (default to input for unknown type), got %d", acc.inputTokens)
	}
}

func TestObserveMetrics_MaxModels(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})

	// Fill up to max
	for i := 0; i < DefaultMaxModels; i++ {
		rms := makeTokenMetrics("openai", "model-"+strings.Repeat("x", 3)+string(rune('A'+i%26))+string(rune('0'+i/26)), 100, 50)
		tracker.ObserveMetrics(rms)
	}
	if len(tracker.accum) != DefaultMaxModels {
		t.Fatalf("expected %d models, got %d", DefaultMaxModels, len(tracker.accum))
	}

	// Try to add one more — should be rejected
	rms := makeTokenMetrics("newprovider", "new-model", 100, 50)
	tracker.ObserveMetrics(rms)
	if len(tracker.accum) != DefaultMaxModels {
		t.Errorf("expected model cap at %d, got %d", DefaultMaxModels, len(tracker.accum))
	}
}

func TestObserveMetrics_ProviderNameAttribute(t *testing.T) {
	// Test gen_ai.provider.name (newer convention) instead of gen_ai.system
	tracker := NewTracker(LLMConfig{Enabled: true})
	rms := []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "gen_ai.client.token.usage",
							Data: &metricspb.Metric_Sum{
								Sum: &metricspb.Sum{
									DataPoints: []*metricspb.NumberDataPoint{
										{
											Attributes: []*commonpb.KeyValue{
												{Key: "gen_ai.provider.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "anthropic"}}},
												{Key: "gen_ai.request.model", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "claude-3.5"}}},
												{Key: "gen_ai.token.type", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "output"}}},
											},
											Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 200},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	tracker.ObserveMetrics(rms)

	key := modelKey{provider: "anthropic", model: "claude-3.5"}
	acc, ok := tracker.accum[key]
	if !ok {
		t.Fatal("expected accumulator for anthropic/claude-3.5")
	}
	if acc.outputTokens != 200 {
		t.Errorf("expected 200 output tokens, got %d", acc.outputTokens)
	}
}

func TestRecordSnapshot(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	tracker.ObserveMetrics(makeTokenMetrics("openai", "gpt-4", 1000, 500))

	tracker.RecordSnapshot()
	if tracker.count != 1 {
		t.Errorf("expected count 1 after first snapshot, got %d", tracker.count)
	}
	if tracker.head != 1 {
		t.Errorf("expected head 1, got %d", tracker.head)
	}
	if tracker.startSnapshot == nil {
		t.Error("expected startSnapshot to be set")
	}
	if tracker.startSnapshot.totalInput != 1000 {
		t.Errorf("expected start snapshot totalInput 1000, got %d", tracker.startSnapshot.totalInput)
	}
	if tracker.snapshotsTotal != 1 {
		t.Errorf("expected snapshotsTotal 1, got %d", tracker.snapshotsTotal)
	}
}

func TestRecordSnapshot_RingWrap(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})

	// Fill the entire ring and then some
	for i := 0; i < DefaultRingSize+10; i++ {
		tracker.ObserveMetrics(makeTokenMetrics("openai", "gpt-4", 1, 0))
		tracker.RecordSnapshot()
	}

	if tracker.count != DefaultRingSize {
		t.Errorf("expected count capped at %d, got %d", DefaultRingSize, tracker.count)
	}
	if tracker.head != (DefaultRingSize+10)%DefaultRingSize {
		t.Errorf("expected head at %d, got %d", (DefaultRingSize+10)%DefaultRingSize, tracker.head)
	}
	if tracker.snapshotsTotal != uint64(DefaultRingSize+10) {
		t.Errorf("expected snapshotsTotal %d, got %d", DefaultRingSize+10, tracker.snapshotsTotal)
	}
}

func TestWriteLLMMetrics_NoData(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	w := httptest.NewRecorder()
	tracker.WriteLLMMetrics(w)

	body := w.Body.String()
	if !strings.Contains(body, "metrics_governor_llm_tracker_enabled 1") {
		t.Error("expected tracker_enabled=1 in output")
	}
	if !strings.Contains(body, "metrics_governor_llm_models_active 0") {
		t.Error("expected models_active=0 in output")
	}
	if !strings.Contains(body, "metrics_governor_llm_snapshots_total 0") {
		t.Error("expected snapshots_total=0 in output")
	}
}

func TestWriteLLMMetrics_TokenTotals(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	tracker.ObserveMetrics(makeTokenMetrics("openai", "gpt-4", 1000, 500))

	w := httptest.NewRecorder()
	tracker.WriteLLMMetrics(w)

	body := w.Body.String()
	if !strings.Contains(body, `metrics_governor_llm_tokens_total{provider="openai",model="gpt-4",token_type="input"} 1000`) {
		t.Errorf("expected input token total in output, got:\n%s", body)
	}
	if !strings.Contains(body, `metrics_governor_llm_tokens_total{provider="openai",model="gpt-4",token_type="output"} 500`) {
		t.Errorf("expected output token total in output, got:\n%s", body)
	}
}

func TestWriteLLMMetrics_TokenRate(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})

	// Record two snapshots with known token delta
	tracker.ObserveMetrics(makeTokenMetrics("openai", "gpt-4", 0, 0))
	tracker.RecordSnapshot()

	tracker.ObserveMetrics(makeTokenMetrics("openai", "gpt-4", 3000, 1500))
	tracker.RecordSnapshot()

	w := httptest.NewRecorder()
	tracker.WriteLLMMetrics(w)

	body := w.Body.String()
	if !strings.Contains(body, "metrics_governor_llm_token_rate") {
		t.Errorf("expected token_rate metrics in output, got:\n%s", body)
	}
}

func TestWriteLLMMetrics_BudgetRemaining(t *testing.T) {
	tracker := NewTracker(LLMConfig{
		Enabled: true,
		Budgets: []BudgetRule{
			{Provider: "openai", Model: "gpt-4", DailyTokens: 10000},
		},
	})
	tracker.ObserveMetrics(makeTokenMetrics("openai", "gpt-4", 2000, 1000))
	tracker.RecordSnapshot()
	tracker.RecordSnapshot()

	w := httptest.NewRecorder()
	tracker.WriteLLMMetrics(w)

	body := w.Body.String()
	if !strings.Contains(body, "metrics_governor_llm_budget_remaining_ratio") {
		t.Errorf("expected budget_remaining_ratio in output, got:\n%s", body)
	}
}

func TestWriteLLMMetrics_BudgetBurnRate(t *testing.T) {
	tracker := NewTracker(LLMConfig{
		Enabled: true,
		Budgets: []BudgetRule{
			{Provider: "openai", Model: "gpt-4", DailyTokens: 100000},
		},
	})
	tracker.ObserveMetrics(makeTokenMetrics("openai", "gpt-4", 5000, 3000))
	tracker.RecordSnapshot()
	tracker.RecordSnapshot()

	w := httptest.NewRecorder()
	tracker.WriteLLMMetrics(w)

	body := w.Body.String()
	if !strings.Contains(body, "metrics_governor_llm_budget_burn_rate") {
		t.Errorf("expected budget_burn_rate in output, got:\n%s", body)
	}
}

func TestWriteLLMMetrics_NoBudget(t *testing.T) {
	tracker := NewTracker(LLMConfig{
		Enabled: true,
		// No Budgets — observe-only mode
	})
	tracker.ObserveMetrics(makeTokenMetrics("openai", "gpt-4", 5000, 3000))
	tracker.RecordSnapshot()
	tracker.RecordSnapshot()

	w := httptest.NewRecorder()
	tracker.WriteLLMMetrics(w)

	body := w.Body.String()
	// Should still have token totals
	if !strings.Contains(body, "metrics_governor_llm_tokens_total") {
		t.Error("expected tokens_total in observe-only mode")
	}
	// Should NOT have budget metrics
	if strings.Contains(body, "budget_remaining_ratio") {
		t.Error("did not expect budget_remaining_ratio in observe-only mode")
	}
}

func TestWriteLLMMetrics_MultipleBudgets(t *testing.T) {
	tracker := NewTracker(LLMConfig{
		Enabled: true,
		Budgets: []BudgetRule{
			{Provider: "openai", Model: "gpt-4*", DailyTokens: 1000000},
			{Provider: "anthropic", Model: "claude-*", DailyTokens: 500000},
			{Provider: "*", Model: "*", DailyTokens: 2000000}, // catch-all
		},
	})
	tracker.ObserveMetrics(makeTokenMetrics("openai", "gpt-4-turbo", 5000, 3000))
	tracker.ObserveMetrics(makeTokenMetrics("anthropic", "claude-3.5", 2000, 1000))
	tracker.ObserveMetrics(makeTokenMetrics("google", "gemini-pro", 1000, 500))
	tracker.RecordSnapshot()
	tracker.RecordSnapshot()

	w := httptest.NewRecorder()
	tracker.WriteLLMMetrics(w)

	body := w.Body.String()
	// All three models should have budget metrics
	if !strings.Contains(body, `provider="openai"`) {
		t.Error("expected openai in budget metrics")
	}
	if !strings.Contains(body, `provider="anthropic"`) {
		t.Error("expected anthropic in budget metrics")
	}
	if !strings.Contains(body, `provider="google"`) {
		t.Error("expected google in budget metrics (catch-all rule)")
	}
}

func TestMatchBudgetRule_GlobMatching(t *testing.T) {
	tracker := NewTracker(LLMConfig{
		Enabled: true,
		Budgets: []BudgetRule{
			{Provider: "openai", Model: "gpt-4*", DailyTokens: 1000000},
			{Provider: "*", Model: "*", DailyTokens: 500000},
		},
	})

	// Exact match
	rule := tracker.matchBudgetRule("openai", "gpt-4-turbo")
	if rule == nil || rule.DailyTokens != 1000000 {
		t.Error("expected gpt-4* rule to match gpt-4-turbo")
	}

	// Catch-all
	rule = tracker.matchBudgetRule("anthropic", "claude-3")
	if rule == nil || rule.DailyTokens != 500000 {
		t.Error("expected catch-all rule for anthropic/claude-3")
	}

	// No match (empty rules)
	tracker2 := NewTracker(LLMConfig{Enabled: true})
	rule = tracker2.matchBudgetRule("openai", "gpt-4")
	if rule != nil {
		t.Error("expected nil rule when no budgets configured")
	}
}

func TestConcurrentAccess(t *testing.T) {
	tracker := NewTracker(LLMConfig{
		Enabled: true,
		Budgets: []BudgetRule{
			{Provider: "*", Model: "*", DailyTokens: 1000000},
		},
	})

	var wg sync.WaitGroup
	const goroutines = 10
	const iterations = 100

	// Concurrent ObserveMetrics
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				rms := makeTokenMetrics("openai", "gpt-4", 100, 50)
				tracker.ObserveMetrics(rms)
			}
		}(i)
	}

	// Concurrent RecordSnapshot
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				tracker.RecordSnapshot()
			}
		}()
	}

	// Concurrent WriteLLMMetrics
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				w := httptest.NewRecorder()
				tracker.WriteLLMMetrics(w)
			}
		}()
	}

	wg.Wait()

	// Verify no data corruption
	key := modelKey{provider: "openai", model: "gpt-4"}
	acc := tracker.accum[key]
	expectedInput := int64(goroutines * iterations * 100)
	if acc.inputTokens != expectedInput {
		t.Errorf("expected %d input tokens after concurrent access, got %d", expectedInput, acc.inputTokens)
	}
}

func TestObserveMetrics_ZeroValue(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	rms := makeTokenMetrics("openai", "gpt-4", 0, 0)
	tracker.ObserveMetrics(rms)

	// Zero values should not create accumulators
	if len(tracker.accum) != 0 {
		t.Errorf("expected no accumulators for zero-value tokens, got %d", len(tracker.accum))
	}
}

func TestObserveMetrics_CompletionTokenType(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	rms := []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "gen_ai.client.token.usage",
							Data: &metricspb.Metric_Sum{
								Sum: &metricspb.Sum{
									DataPoints: []*metricspb.NumberDataPoint{
										{
											Attributes: makeGenAIAttrs("openai", "gpt-4", "completion"),
											Value:      &metricspb.NumberDataPoint_AsDouble{AsDouble: 300},
										},
										{
											Attributes: makeGenAIAttrs("openai", "gpt-4", "prompt"),
											Value:      &metricspb.NumberDataPoint_AsDouble{AsDouble: 500},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	tracker.ObserveMetrics(rms)

	key := modelKey{provider: "openai", model: "gpt-4"}
	acc := tracker.accum[key]
	if acc.inputTokens != 500 {
		t.Errorf("expected 500 input tokens for 'prompt' type, got %d", acc.inputTokens)
	}
	if acc.outputTokens != 300 {
		t.Errorf("expected 300 output tokens for 'completion' type, got %d", acc.outputTokens)
	}
}

// --- Edge Case Tests ---

func TestObserveMetrics_EmptyBatch(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	tracker.ObserveMetrics([]*metricspb.ResourceMetrics{})

	if len(tracker.accum) != 0 {
		t.Errorf("expected no accumulators for empty batch, got %d", len(tracker.accum))
	}
}

func TestObserveMetrics_NilBatch(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	tracker.ObserveMetrics(nil)

	if len(tracker.accum) != 0 {
		t.Errorf("expected no accumulators for nil batch, got %d", len(tracker.accum))
	}
}

func TestObserveMetrics_NegativeValue(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	// Build metric with negative token values directly (bypass makeTokenMetrics which filters >0)
	rms := []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "gen_ai.client.token.usage",
							Data: &metricspb.Metric_Sum{
								Sum: &metricspb.Sum{
									DataPoints: []*metricspb.NumberDataPoint{
										{
											Attributes: makeGenAIAttrs("openai", "gpt-4", "input"),
											Value:      &metricspb.NumberDataPoint_AsDouble{AsDouble: -500},
										},
										{
											Attributes: makeGenAIAttrs("openai", "gpt-4", "output"),
											Value:      &metricspb.NumberDataPoint_AsDouble{AsDouble: -200},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	tracker.ObserveMetrics(rms)

	// accumulate() has `if value <= 0 { return }` — negative values are discarded
	if len(tracker.accum) != 0 {
		t.Errorf("expected no accumulators for negative values (discarded by accumulate), got %d", len(tracker.accum))
	}
}

func TestObserveMetrics_MultipleProviders(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	tracker.ObserveMetrics(makeTokenMetrics("openai", "gpt-4", 1000, 500))
	tracker.ObserveMetrics(makeTokenMetrics("anthropic", "claude-3", 2000, 800))

	keyOpenAI := modelKey{provider: "openai", model: "gpt-4"}
	keyAnthropic := modelKey{provider: "anthropic", model: "claude-3"}

	accOAI, ok := tracker.accum[keyOpenAI]
	if !ok {
		t.Fatal("expected accumulator for openai/gpt-4")
	}
	accAnth, ok := tracker.accum[keyAnthropic]
	if !ok {
		t.Fatal("expected accumulator for anthropic/claude-3")
	}

	if accOAI.inputTokens != 1000 {
		t.Errorf("openai input: expected 1000, got %d", accOAI.inputTokens)
	}
	if accOAI.outputTokens != 500 {
		t.Errorf("openai output: expected 500, got %d", accOAI.outputTokens)
	}
	if accAnth.inputTokens != 2000 {
		t.Errorf("anthropic input: expected 2000, got %d", accAnth.inputTokens)
	}
	if accAnth.outputTokens != 800 {
		t.Errorf("anthropic output: expected 800, got %d", accAnth.outputTokens)
	}

	// Accumulate more to openai — should be independent
	tracker.ObserveMetrics(makeTokenMetrics("openai", "gpt-4", 500, 200))
	if tracker.accum[keyOpenAI].inputTokens != 1500 {
		t.Errorf("openai input after second observe: expected 1500, got %d", tracker.accum[keyOpenAI].inputTokens)
	}
	// anthropic should remain unchanged
	if tracker.accum[keyAnthropic].inputTokens != 2000 {
		t.Errorf("anthropic input should be unchanged at 2000, got %d", tracker.accum[keyAnthropic].inputTokens)
	}
}

func TestObserveMetrics_LargeValues(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	largeValue := float64(int64(1) << 50) // 1125899906842624
	rms := []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "gen_ai.client.token.usage",
							Data: &metricspb.Metric_Sum{
								Sum: &metricspb.Sum{
									DataPoints: []*metricspb.NumberDataPoint{
										{
											Attributes: makeGenAIAttrs("openai", "gpt-4", "input"),
											Value:      &metricspb.NumberDataPoint_AsDouble{AsDouble: largeValue},
										},
										{
											Attributes: makeGenAIAttrs("openai", "gpt-4", "output"),
											Value:      &metricspb.NumberDataPoint_AsDouble{AsDouble: largeValue},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	tracker.ObserveMetrics(rms)

	key := modelKey{provider: "openai", model: "gpt-4"}
	acc, ok := tracker.accum[key]
	if !ok {
		t.Fatal("expected accumulator for openai/gpt-4")
	}
	expected := int64(1) << 50
	if acc.inputTokens != expected {
		t.Errorf("expected input tokens %d, got %d", expected, acc.inputTokens)
	}
	if acc.outputTokens != expected {
		t.Errorf("expected output tokens %d, got %d", expected, acc.outputTokens)
	}

	// Accumulate the same large value again — should not overflow for int64
	tracker.ObserveMetrics(rms)
	if acc.inputTokens != 2*expected {
		t.Errorf("expected input tokens %d after double, got %d", 2*expected, acc.inputTokens)
	}
}

func TestObserveMetrics_CustomTokenMetric(t *testing.T) {
	customMetric := "custom.llm.tokens"
	tracker := NewTracker(LLMConfig{
		Enabled:     true,
		TokenMetric: customMetric,
	})

	// Default metric name should NOT match
	rms := makeTokenMetrics("openai", "gpt-4", 1000, 500)
	tracker.ObserveMetrics(rms)
	if len(tracker.accum) != 0 {
		t.Errorf("expected no accumulators when metric name doesn't match custom config, got %d", len(tracker.accum))
	}

	// Custom metric name should match
	rmsCustom := []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: customMetric,
							Data: &metricspb.Metric_Sum{
								Sum: &metricspb.Sum{
									DataPoints: []*metricspb.NumberDataPoint{
										{
											Attributes: makeGenAIAttrs("openai", "gpt-4", "input"),
											Value:      &metricspb.NumberDataPoint_AsDouble{AsDouble: 777},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	tracker.ObserveMetrics(rmsCustom)

	key := modelKey{provider: "openai", model: "gpt-4"}
	acc, ok := tracker.accum[key]
	if !ok {
		t.Fatal("expected accumulator for custom metric name")
	}
	if acc.inputTokens != 777 {
		t.Errorf("expected 777 input tokens, got %d", acc.inputTokens)
	}
}

func TestRecordSnapshot_NoAccumulators(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	// No ObserveMetrics calls — record a snapshot with zero data
	tracker.RecordSnapshot()

	if tracker.count != 1 {
		t.Errorf("expected count 1, got %d", tracker.count)
	}
	if tracker.startSnapshot == nil {
		t.Fatal("expected startSnapshot to be set")
	}
	if tracker.startSnapshot.totalInput != 0 {
		t.Errorf("expected zero totalInput in start snapshot, got %d", tracker.startSnapshot.totalInput)
	}
	if tracker.startSnapshot.totalOutput != 0 {
		t.Errorf("expected zero totalOutput in start snapshot, got %d", tracker.startSnapshot.totalOutput)
	}
	if tracker.startSnapshot.activeModels != 0 {
		t.Errorf("expected zero activeModels in start snapshot, got %d", tracker.startSnapshot.activeModels)
	}
}

func TestRecordSnapshot_MultipleSnapshots(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})

	// Record 5 snapshots with increasing data
	for i := 1; i <= 5; i++ {
		tracker.ObserveMetrics(makeTokenMetrics("openai", "gpt-4", 100, 50))
		tracker.RecordSnapshot()
	}

	if tracker.head != 5 {
		t.Errorf("expected head at 5 after 5 snapshots, got %d", tracker.head)
	}
	if tracker.count != 5 {
		t.Errorf("expected count 5, got %d", tracker.count)
	}
	if tracker.snapshotsTotal != 5 {
		t.Errorf("expected snapshotsTotal 5, got %d", tracker.snapshotsTotal)
	}

	// Latest snapshot should reflect cumulative tokens
	tracker.mu.RLock()
	latest, ok := tracker.getSnapshotAt(0)
	tracker.mu.RUnlock()
	if !ok {
		t.Fatal("expected latest snapshot to be available")
	}
	if latest.totalInput != 500 {
		t.Errorf("expected totalInput 500 in latest snapshot, got %d", latest.totalInput)
	}
	if latest.totalOutput != 250 {
		t.Errorf("expected totalOutput 250 in latest snapshot, got %d", latest.totalOutput)
	}

	// Start snapshot should still reflect first-ever snapshot values
	if tracker.startSnapshot.totalInput != 100 {
		t.Errorf("expected startSnapshot totalInput 100, got %d", tracker.startSnapshot.totalInput)
	}
}

func TestWriteLLMMetrics_ModelsActive(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	tracker.ObserveMetrics(makeTokenMetrics("openai", "gpt-4", 100, 50))
	tracker.ObserveMetrics(makeTokenMetrics("anthropic", "claude-3", 200, 100))
	tracker.ObserveMetrics(makeTokenMetrics("google", "gemini-pro", 300, 150))
	tracker.RecordSnapshot()

	w := httptest.NewRecorder()
	tracker.WriteLLMMetrics(w)

	body := w.Body.String()
	if !strings.Contains(body, "metrics_governor_llm_models_active 3") {
		t.Errorf("expected models_active=3 for 3 distinct models, got:\n%s", body)
	}
}

func TestWriteLLMMetrics_UptimeSeconds(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	// Set startTime slightly in the past so uptime is measurable
	tracker.startTime = time.Now().Add(-2 * time.Second)

	w := httptest.NewRecorder()
	tracker.WriteLLMMetrics(w)

	body := w.Body.String()
	if !strings.Contains(body, "metrics_governor_llm_uptime_seconds") {
		t.Fatal("expected uptime_seconds metric in output")
	}
	// Uptime should be >= 2 seconds (we set startTime 2s ago)
	// Parse the value from the output
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "metrics_governor_llm_uptime_seconds ") {
			var uptime float64
			_, err := fmt.Sscanf(line, "metrics_governor_llm_uptime_seconds %f", &uptime)
			if err != nil {
				t.Fatalf("failed to parse uptime: %v", err)
			}
			if uptime < 2.0 {
				t.Errorf("expected uptime >= 2.0 seconds, got %g", uptime)
			}
			return
		}
	}
	t.Error("uptime_seconds metric line not found in output")
}

func TestWriteLLMMetrics_SnapshotsTotal(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})

	tracker.RecordSnapshot()
	tracker.RecordSnapshot()
	tracker.RecordSnapshot()

	w := httptest.NewRecorder()
	tracker.WriteLLMMetrics(w)

	body := w.Body.String()
	if !strings.Contains(body, "metrics_governor_llm_snapshots_total 3") {
		t.Errorf("expected snapshots_total=3 after 3 RecordSnapshot calls, got:\n%s", body)
	}
}

func TestObserveMetrics_EmptyScopeMetrics(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	rms := []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{},
		},
	}
	tracker.ObserveMetrics(rms)

	if len(tracker.accum) != 0 {
		t.Errorf("expected no accumulators for empty ScopeMetrics, got %d", len(tracker.accum))
	}
}

func TestObserveMetrics_EmptyMetricsInScope(t *testing.T) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	rms := []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{},
				},
			},
		},
	}
	tracker.ObserveMetrics(rms)

	if len(tracker.accum) != 0 {
		t.Errorf("expected no accumulators for empty Metrics slice, got %d", len(tracker.accum))
	}
}

// --- Benchmarks ---

func BenchmarkObserveMetrics_NoGenAI(b *testing.B) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	rms := makeNonGenAIMetrics(50) // 50 non-matching metrics per batch

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tracker.ObserveMetrics(rms)
	}
}

func BenchmarkObserveMetrics_WithGenAI(b *testing.B) {
	tracker := NewTracker(LLMConfig{Enabled: true})

	// Build batch with 10 gen_ai datapoints across 2 metrics
	rms := []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: func() []*metricspb.Metric {
						metrics := make([]*metricspb.Metric, 2)
						for i := 0; i < 2; i++ {
							dps := make([]*metricspb.NumberDataPoint, 5)
							for j := 0; j < 5; j++ {
								dps[j] = &metricspb.NumberDataPoint{
									Attributes: makeGenAIAttrs("openai", "gpt-4", "input"),
									Value:      &metricspb.NumberDataPoint_AsDouble{AsDouble: 100},
								}
							}
							metrics[i] = &metricspb.Metric{
								Name: "gen_ai.client.token.usage",
								Data: &metricspb.Metric_Sum{
									Sum: &metricspb.Sum{DataPoints: dps},
								},
							}
						}
						return metrics
					}(),
				},
			},
		},
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		tracker.ObserveMetrics(rms)
	}
}

func BenchmarkObserveMetrics_Parallel(b *testing.B) {
	tracker := NewTracker(LLMConfig{Enabled: true})
	rms := makeTokenMetrics("openai", "gpt-4", 100, 50)

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tracker.ObserveMetrics(rms)
		}
	})
}
