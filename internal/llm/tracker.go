package llm

import (
	"fmt"
	"math"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
)

// Default configuration values.
const (
	DefaultTokenMetric  = "gen_ai.client.token.usage" //nolint:gosec // not a credential
	DefaultBudgetWindow = 24 * time.Hour
	DefaultRingSize     = 720 // 6h at 30s intervals
	DefaultMaxModels    = 500
)

// LLM tracker windows for rate computation (same as SLI tracker).
var llmWindows = []struct {
	Label string
	Slots int
}{
	{"5m", 10},  // 10 × 30s = 5 min
	{"30m", 60}, // 60 × 30s = 30 min
	{"1h", 120}, // 120 × 30s = 1 hour
	{"6h", 720}, // 720 × 30s = 6 hours
}

// LLMConfig holds LLM token budget tracker configuration.
type LLMConfig struct {
	Enabled      bool
	TokenMetric  string
	BudgetWindow time.Duration
	Budgets      []BudgetRule
}

// BudgetRule defines a token budget for a provider/model pattern.
type BudgetRule struct {
	Provider    string // glob pattern: "openai", "*"
	Model       string // glob pattern: "gpt-4*", "*"
	DailyTokens int64  // 0 = observe only
}

// modelKey identifies a unique provider+model combination.
type modelKey struct {
	provider string
	model    string
}

// tokenAccumulator tracks cumulative token counts for a single model.
type tokenAccumulator struct {
	inputTokens  int64
	outputTokens int64
	lastSeen     int64 // unix seconds
}

// tokenSnapshot holds aggregate token state at a point in time.
type tokenSnapshot struct {
	timestamp    int64
	totalInput   int64
	totalOutput  int64
	activeModels int32
}

// Tracker computes LLM token consumption rates, budget burn, and per-model
// visibility from periodic counter snapshots stored in a fixed-size ring buffer.
//
// Thread safety:
//   - ObserveMetrics: called from hot path under accMu (separate from Collector.mu)
//   - RecordSnapshot: called every 30s, acquires accMu then mu.Lock
//   - WriteLLMMetrics: called on /metrics scrape under mu.RLock + accMu
type Tracker struct {
	mu     sync.RWMutex
	config LLMConfig

	ring  []tokenSnapshot // fixed-size ring buffer
	head  int             // next write position
	count int             // number of valid entries (up to len(ring))

	startTime     time.Time
	startSnapshot *tokenSnapshot // first-ever snapshot for budget baseline

	accMu sync.Mutex
	accum map[modelKey]*tokenAccumulator

	snapshotsTotal uint64
}

// NewTracker creates a new LLM token budget tracker.
func NewTracker(cfg LLMConfig) *Tracker {
	if cfg.TokenMetric == "" {
		cfg.TokenMetric = DefaultTokenMetric
	}
	if cfg.BudgetWindow <= 0 {
		cfg.BudgetWindow = DefaultBudgetWindow
	}
	return &Tracker{
		config:    cfg,
		ring:      make([]tokenSnapshot, DefaultRingSize),
		accum:     make(map[modelKey]*tokenAccumulator),
		startTime: time.Now(),
	}
}

// ObserveMetrics scans incoming metrics for gen_ai token usage data.
// Called from the stats.Collector.Process() hot path.
func (t *Tracker) ObserveMetrics(resourceMetrics []*metricspb.ResourceMetrics) {
	prefix := t.config.TokenMetric

	for _, rm := range resourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if !strings.HasPrefix(m.Name, prefix) {
					continue
				}
				t.extractTokens(m)
			}
		}
	}
}

// extractTokens extracts token counts from a matching metric.
func (t *Tracker) extractTokens(m *metricspb.Metric) {
	switch d := m.Data.(type) {
	case *metricspb.Metric_Sum:
		for _, dp := range d.Sum.DataPoints {
			provider, model, tokenType := extractGenAIAttrs(dp.Attributes)
			value := getNumberValue(dp)
			t.accumulate(provider, model, tokenType, value)
		}
	case *metricspb.Metric_Gauge:
		for _, dp := range d.Gauge.DataPoints {
			provider, model, tokenType := extractGenAIAttrs(dp.Attributes)
			value := getNumberValue(dp)
			t.accumulate(provider, model, tokenType, value)
		}
	case *metricspb.Metric_Histogram:
		for _, dp := range d.Histogram.DataPoints {
			provider, model, tokenType := extractGenAIAttrs(dp.Attributes)
			var value float64
			if dp.Sum != nil {
				value = *dp.Sum
			}
			t.accumulateHistogram(provider, model, tokenType, value)
		}
	}
}

// extractGenAIAttrs extracts provider, model, and token_type from OTel attributes.
func extractGenAIAttrs(attrs []*commonpb.KeyValue) (provider, model, tokenType string) {
	for _, kv := range attrs {
		if kv.Value == nil {
			continue
		}
		sv := kv.Value.GetStringValue()
		if sv == "" {
			continue
		}
		switch kv.Key {
		case "gen_ai.system", "gen_ai.provider.name":
			provider = sv
		case "gen_ai.request.model":
			model = sv
		case "gen_ai.token.type":
			tokenType = sv
		}
	}
	return
}

// getNumberValue extracts a float64 value from a NumberDataPoint.
func getNumberValue(dp *metricspb.NumberDataPoint) float64 {
	switch v := dp.Value.(type) {
	case *metricspb.NumberDataPoint_AsDouble:
		return v.AsDouble
	case *metricspb.NumberDataPoint_AsInt:
		return float64(v.AsInt)
	}
	return 0
}

// accumulate adds token counts to the appropriate model accumulator.
func (t *Tracker) accumulate(provider, model, tokenType string, value float64) {
	if value <= 0 {
		return
	}
	if provider == "" {
		provider = "unknown"
	}
	if model == "" {
		model = "unknown"
	}

	key := modelKey{provider: provider, model: model}
	tokens := int64(value)
	now := time.Now().Unix()

	t.accMu.Lock()
	defer t.accMu.Unlock()

	acc, ok := t.accum[key]
	if !ok {
		if len(t.accum) >= DefaultMaxModels {
			return // cap reached
		}
		acc = &tokenAccumulator{}
		t.accum[key] = acc
	}
	acc.lastSeen = now

	switch tokenType {
	case "input", "prompt":
		acc.inputTokens += tokens
	case "output", "completion":
		acc.outputTokens += tokens
	default:
		// Unknown token type — count as input
		acc.inputTokens += tokens
	}
}

// accumulateHistogram handles histogram datapoints (using Sum for token counts).
func (t *Tracker) accumulateHistogram(provider, model, tokenType string, value float64) {
	t.accumulate(provider, model, tokenType, value)
}

// RecordSnapshot captures a point-in-time snapshot of aggregated token counts.
// Called every 30s from the periodic logging goroutine.
func (t *Tracker) RecordSnapshot() {
	now := time.Now().Unix()

	t.accMu.Lock()
	var totalInput, totalOutput int64
	var activeModels int32
	for _, acc := range t.accum {
		totalInput += acc.inputTokens
		totalOutput += acc.outputTokens
		activeModels++
	}
	t.accMu.Unlock()

	snap := tokenSnapshot{
		timestamp:    now,
		totalInput:   totalInput,
		totalOutput:  totalOutput,
		activeModels: activeModels,
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.startSnapshot == nil {
		cp := snap
		t.startSnapshot = &cp
	}

	t.ring[t.head] = snap
	t.head = (t.head + 1) % len(t.ring)
	if t.count < len(t.ring) {
		t.count++
	}
	t.snapshotsTotal++
}

// getSnapshotAt returns the snapshot at the given number of slots back from head.
// Must be called under mu.RLock.
func (t *Tracker) getSnapshotAt(slotsBack int) (tokenSnapshot, bool) {
	if slotsBack >= t.count {
		return tokenSnapshot{}, false
	}
	idx := (t.head - 1 - slotsBack + len(t.ring)) % len(t.ring)
	return t.ring[idx], true
}

// WriteLLMMetrics emits Prometheus-format LLM metrics to the response writer.
// Called from stats.Collector.ServeHTTP under its own lock scope.
func (t *Tracker) WriteLLMMetrics(w http.ResponseWriter) {
	t.mu.RLock()
	latestSnap, hasLatest := t.getSnapshotAt(0)
	ringCount := t.count
	snapshotsTotal := t.snapshotsTotal
	startSnapshot := t.startSnapshot
	startTime := t.startTime

	// Copy window snapshots while under read lock
	type windowSnap struct {
		label string
		older tokenSnapshot
		ok    bool
	}
	windowSnaps := make([]windowSnap, len(llmWindows))
	for i, win := range llmWindows {
		older, ok := t.getSnapshotAt(win.Slots - 1)
		windowSnaps[i] = windowSnap{label: win.Label, older: older, ok: ok}
	}
	t.mu.RUnlock()

	// Snapshot per-model accumulators
	t.accMu.Lock()
	type modelData struct {
		key          modelKey
		inputTokens  int64
		outputTokens int64
	}
	models := make([]modelData, 0, len(t.accum))
	for k, acc := range t.accum {
		models = append(models, modelData{
			key:          k,
			inputTokens:  acc.inputTokens,
			outputTokens: acc.outputTokens,
		})
	}
	t.accMu.Unlock()

	// --- Emit metrics ---

	// Per-model token totals
	fmt.Fprintf(w, "# HELP metrics_governor_llm_tokens_total Cumulative token count by provider, model, and token type\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_llm_tokens_total counter\n")
	for _, md := range models {
		fmt.Fprintf(w, "metrics_governor_llm_tokens_total{provider=%q,model=%q,token_type=\"input\"} %d\n",
			md.key.provider, md.key.model, md.inputTokens)
		fmt.Fprintf(w, "metrics_governor_llm_tokens_total{provider=%q,model=%q,token_type=\"output\"} %d\n",
			md.key.provider, md.key.model, md.outputTokens)
	}

	// Token rates per window
	if hasLatest && ringCount >= 2 {
		fmt.Fprintf(w, "# HELP metrics_governor_llm_token_rate Token consumption rate (tokens/second) over window\n")
		fmt.Fprintf(w, "# TYPE metrics_governor_llm_token_rate gauge\n")
		for _, ws := range windowSnaps {
			if !ws.ok {
				continue
			}
			dt := float64(latestSnap.timestamp - ws.older.timestamp)
			if dt <= 0 {
				continue
			}
			inputRate := float64(latestSnap.totalInput-ws.older.totalInput) / dt
			outputRate := float64(latestSnap.totalOutput-ws.older.totalOutput) / dt
			fmt.Fprintf(w, "metrics_governor_llm_token_rate{token_type=\"input\",window=%q} %g\n", ws.label, inputRate)
			fmt.Fprintf(w, "metrics_governor_llm_token_rate{token_type=\"output\",window=%q} %g\n", ws.label, outputRate)
		}
	}

	// Budget remaining + burn rate
	if len(t.config.Budgets) > 0 && hasLatest && startSnapshot != nil {
		fmt.Fprintf(w, "# HELP metrics_governor_llm_budget_remaining_ratio Fraction of daily token budget remaining (0-1)\n")
		fmt.Fprintf(w, "# TYPE metrics_governor_llm_budget_remaining_ratio gauge\n")
		fmt.Fprintf(w, "# HELP metrics_governor_llm_budget_burn_rate Token budget burn rate (1.0 = on pace for daily budget)\n")
		fmt.Fprintf(w, "# TYPE metrics_governor_llm_budget_burn_rate gauge\n")

		for _, md := range models {
			rule := t.matchBudgetRule(md.key.provider, md.key.model)
			if rule == nil || rule.DailyTokens <= 0 {
				continue
			}
			totalTokens := md.inputTokens + md.outputTokens
			startTotal := startSnapshot.totalInput + startSnapshot.totalOutput
			consumed := totalTokens - startTotal
			if consumed < 0 {
				consumed = 0
			}

			remaining := 1.0 - float64(consumed)/float64(rule.DailyTokens)
			remaining = math.Max(0, math.Min(1, remaining))
			fmt.Fprintf(w, "metrics_governor_llm_budget_remaining_ratio{provider=%q,model=%q} %g\n",
				md.key.provider, md.key.model, remaining)

			// Burn rate: project daily consumption from current rate
			elapsed := time.Since(startTime).Seconds()
			if elapsed > 0 {
				dailyProjected := float64(consumed) / elapsed * t.config.BudgetWindow.Seconds()
				burnRate := dailyProjected / float64(rule.DailyTokens)
				fmt.Fprintf(w, "metrics_governor_llm_budget_burn_rate{provider=%q,model=%q} %g\n",
					md.key.provider, md.key.model, burnRate)
			}
		}
	}

	// Active models gauge
	activeModels := int32(len(models))
	if hasLatest {
		activeModels = latestSnap.activeModels
	}
	fmt.Fprintf(w, "# HELP metrics_governor_llm_models_active Number of active LLM models observed\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_llm_models_active gauge\n")
	fmt.Fprintf(w, "metrics_governor_llm_models_active %d\n", activeModels)

	// Config/operational metrics
	fmt.Fprintf(w, "# HELP metrics_governor_llm_tracker_enabled Whether the LLM tracker is enabled (1=yes)\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_llm_tracker_enabled gauge\n")
	enabled := 0
	if t.config.Enabled {
		enabled = 1
	}
	fmt.Fprintf(w, "metrics_governor_llm_tracker_enabled %d\n", enabled)

	fmt.Fprintf(w, "# HELP metrics_governor_llm_uptime_seconds Seconds since LLM tracker started\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_llm_uptime_seconds gauge\n")
	fmt.Fprintf(w, "metrics_governor_llm_uptime_seconds %g\n", time.Since(startTime).Seconds())

	fmt.Fprintf(w, "# HELP metrics_governor_llm_snapshots_total Total number of snapshots recorded\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_llm_snapshots_total counter\n")
	fmt.Fprintf(w, "metrics_governor_llm_snapshots_total %d\n", snapshotsTotal)
}

// matchBudgetRule finds the first matching budget rule for a provider/model.
// Uses filepath.Match for glob matching.
func (t *Tracker) matchBudgetRule(provider, model string) *BudgetRule {
	for i := range t.config.Budgets {
		rule := &t.config.Budgets[i]
		providerMatch, _ := filepath.Match(rule.Provider, provider)
		modelMatch, _ := filepath.Match(rule.Model, model)
		if providerMatch && modelMatch {
			return rule
		}
	}
	return nil
}
