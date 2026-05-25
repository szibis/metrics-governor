package autotune

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/szibis/metrics-governor/internal/logging"
)

// Tuner is the core autotune controller. It runs an OBSERVE-DECIDE-APPLY loop
// in a background goroutine, completely decoupled from the metrics hot path.
type Tuner struct {
	cfg Config

	// Signal sources.
	aggregator *SignalAggregator

	// Policy engine.
	policy *PolicyEngine

	// Safeguards.
	rateLimiter *ReloadRateLimiter
	coordinator *ReloadCoordinator
	errorBudget *ErrorBudget
	oscillation *OscillationDetector
	verifier    *PostApplyVerifier
	sourceCB    *CircuitBreaker

	// Persistence.
	persister ChangePersister

	// HA.
	elector LeaderElector

	// Functions injected from the enforcer.
	reloadFunc func(cfg any) // calls limitsEnforcer.ReloadConfig
	configFunc func() any    // calls limitsEnforcer.GetConfig
	utilFunc   func() map[string]RuleUtilization
	dropsFunc  func() int64

	// State.
	mu             sync.RWMutex
	currentLimits  map[string]int64 // rule -> current max_cardinality
	baselineLimits map[string]int64 // rule -> baseline (from SIGHUP)
	previousLimits map[string]int64 // rule -> pre-last-apply (for rollback)

	// Lifecycle.
	cancel context.CancelFunc
	done   chan struct{}

	// Metrics counters.
	cyclesTotal      atomic.Int64
	errorsTotal      atomic.Int64
	adjustmentsTotal atomic.Int64
	rollbacksTotal   atomic.Int64
	reloadTimeouts   atomic.Int64
	isLeader         atomic.Bool
	paused           atomic.Bool
}

// Option configures the Tuner.
type Option func(*Tuner)

// WithReloadFunc injects the enforcer's ReloadConfig function.
func WithReloadFunc(fn func(any)) Option {
	return func(t *Tuner) { t.reloadFunc = fn }
}

// WithConfigFunc injects the enforcer's GetConfig function.
func WithConfigFunc(fn func() any) Option {
	return func(t *Tuner) { t.configFunc = fn }
}

// WithUtilizationFunc injects the enforcer's GetRuleUtilization function.
func WithUtilizationFunc(fn func() map[string]RuleUtilization) Option {
	return func(t *Tuner) { t.utilFunc = fn }
}

// WithDropsFunc injects the enforcer's TotalDropped function.
func WithDropsFunc(fn func() int64) Option {
	return func(t *Tuner) { t.dropsFunc = fn }
}

// WithCardinalitySource sets the cardinality source (Tier 1).
func WithCardinalitySource(source CardinalitySource) Option {
	return func(t *Tuner) {
		t.aggregator = NewSignalAggregator(source, nil, t.dropsFunc, t.utilFunc)
	}
}

// WithPersister sets the change persister.
func WithPersister(p ChangePersister) Option {
	return func(t *Tuner) { t.persister = p }
}

// WithElector sets the leader elector.
func WithElector(e LeaderElector) Option {
	return func(t *Tuner) { t.elector = e }
}

// NewTuner creates a new autotune controller.
func NewTuner(cfg Config, opts ...Option) *Tuner {
	oscillation := NewOscillationDetector(cfg.Safeguards.Oscillation)

	t := &Tuner{
		cfg:            cfg,
		oscillation:    oscillation,
		policy:         NewPolicyEngine(cfg.Policy, oscillation),
		rateLimiter:    NewReloadRateLimiter(cfg.Safeguards.ReloadMinInterval),
		coordinator:    NewReloadCoordinator(),
		errorBudget:    NewErrorBudget(cfg.Safeguards.ErrorBudget),
		sourceCB:       NewCircuitBreaker("source", cfg.Safeguards.CircuitBreaker.MaxConsecutiveFailures, cfg.Safeguards.CircuitBreaker.ResetTimeout),
		persister:      NewMemoryPersister(1000),
		elector:        NewNoopElector(),
		currentLimits:  make(map[string]int64),
		baselineLimits: make(map[string]int64),
		previousLimits: make(map[string]int64),
		done:           make(chan struct{}),
	}

	for _, opt := range opts {
		opt(t)
	}

	// Build persister from config (overridable via WithPersister option).
	if _, isMemory := t.persister.(*MemoryPersister); isMemory {
		t.persister = t.buildPersister()
	}

	// Build elector from config (overridable via WithElector option).
	if _, isNoop := t.elector.(*NoopElector); isNoop {
		t.elector = t.buildElector()
	}

	// Build aggregator if not set via options.
	if t.aggregator == nil {
		var source CardinalitySource
		if cfg.Source.Enabled {
			source = t.buildSource()
		}
		var anomaly AnomalySource
		if cfg.VManomalyEnabled {
			anomaly = NewAnomalyClient(cfg.VManomalyURL, cfg.VManomalyMetric, cfg.Source.Timeout)
		}
		t.aggregator = NewSignalAggregator(source, anomaly, t.dropsFunc, t.utilFunc)
	}

	// Build verifier.
	t.verifier = NewPostApplyVerifier(cfg.Safeguards.Verification, cfg.Policy.RollbackDropPct, t.dropsFunc)

	return t
}

// buildPersister constructs the appropriate ChangePersister based on config.
func (t *Tuner) buildPersister() ChangePersister {
	switch t.cfg.Persist.Mode {
	case PersistFile:
		return NewFilePersister(
			t.cfg.Persist.File.Path,
			t.cfg.Safeguards.Git.MaxRetries,
			t.cfg.Safeguards.Git.RetryBackoffBase,
		)
	case PersistGit:
		return NewGitPersister(t.cfg.Persist.Git, t.cfg.Safeguards.Git)
	default:
		return NewMemoryPersister(1000)
	}
}

// buildElector constructs the appropriate LeaderElector based on config.
func (t *Tuner) buildElector() LeaderElector {
	switch t.cfg.HA.Mode {
	case HADesignated:
		return NewDesignatedElector(t.cfg.HA.DesignatedLeader)
	case HALease:
		le, err := NewLeaseElector(t.cfg.HA)
		if err != nil {
			logging.Warn("autotune: failed to create lease elector, falling back to noop",
				map[string]any{"error": err.Error()})
			return NewNoopElector()
		}
		return le
	default:
		return NewNoopElector()
	}
}

// buildSource constructs the appropriate CardinalitySource based on config.
func (t *Tuner) buildSource() CardinalitySource {
	switch t.cfg.Source.Backend {
	case BackendVM:
		return NewVMClient(t.cfg.Source)
	case BackendPrometheus:
		return NewPrometheusClient(t.cfg.Source)
	case BackendMimir:
		return NewMimirClient(t.cfg.Source)
	case BackendThanos:
		return NewThanosClient(t.cfg.Source)
	case BackendPromQL:
		return NewPromQLClient(t.cfg.Source)
	case BackendMulti:
		return t.buildMultiSource()
	default:
		logging.Warn("autotune: unknown backend, falling back to VM", map[string]any{"backend": string(t.cfg.Source.Backend)})
		return NewVMClient(t.cfg.Source)
	}
}

// buildMultiSource constructs a MultiClient from the config's sub-sources.
func (t *Tuner) buildMultiSource() CardinalitySource {
	var sources []CardinalitySource
	for _, sub := range t.cfg.Source.Multi.Sources {
		switch sub.Backend {
		case BackendVM:
			sources = append(sources, NewVMClient(sub))
		case BackendPrometheus:
			sources = append(sources, NewPrometheusClient(sub))
		case BackendMimir:
			sources = append(sources, NewMimirClient(sub))
		case BackendThanos:
			sources = append(sources, NewThanosClient(sub))
		case BackendPromQL:
			sources = append(sources, NewPromQLClient(sub))
		default:
			logging.Warn("autotune: multi-source skipping unknown backend", map[string]any{"backend": string(sub.Backend)})
		}
	}
	return NewMultiClient(sources, t.cfg.Source.Multi.MergeStrategy)
}

// Start begins the autotune loop in a background goroutine.
func (t *Tuner) Start(ctx context.Context) {
	ctx, t.cancel = context.WithCancel(ctx)

	// Start leader elector.
	if err := t.elector.Start(ctx); err != nil {
		logging.Error("autotune: failed to start leader elector", map[string]any{"error": err.Error()})
	}

	// Snapshot initial limits.
	t.snapshotLimits()

	go t.run(ctx)
}

// Stop gracefully shuts down the autotune loop.
func (t *Tuner) Stop() {
	if t.cancel != nil {
		t.cancel()
	}
	<-t.done
	t.elector.Stop()
}

// run is the main autotune loop.
func (t *Tuner) run(ctx context.Context) {
	defer close(t.done)

	ticker := time.NewTicker(t.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.cycle(ctx)
		}
	}
}

// cycle executes one OBSERVE-DECIDE-APPLY iteration.
func (t *Tuner) cycle(ctx context.Context) {
	t.cyclesTotal.Add(1)
	now := time.Now()

	// Check if we're the leader.
	isLeader := t.elector.IsLeader()
	t.isLeader.Store(isLeader)
	if !isLeader {
		return
	}

	// Check error budget.
	if t.errorBudget.IsPaused(now) {
		t.paused.Store(true)
		return
	}
	t.paused.Store(false)

	// Check if verifier has pending results.
	if t.verifier.IsActive() {
		result, rollbackDecisions := t.verifier.Check(now)
		switch result {
		case VerificationFail:
			t.handleRollback(ctx, rollbackDecisions, now)
			return
		case VerificationPass:
			// Committed — no action needed.
		case VerificationPending:
			// Still verifying — skip this cycle's new decisions.
			return
		}
	}

	// OBSERVE: collect signals.
	var signals AggregatedSignals
	var err error

	if t.sourceCB.Allow() {
		signals, err = t.aggregator.Collect(ctx)
		if err != nil {
			t.sourceCB.RecordFailure()
			t.errorsTotal.Add(1)
			t.errorBudget.RecordError(now)
			logging.Warn("autotune: signal collection failed", map[string]any{"error": err.Error()})
			return
		}
		t.sourceCB.RecordSuccess()
	} else {
		// Circuit breaker open — use internal signals only.
		signals = t.collectInternalOnly()
	}

	// DECIDE: evaluate policy.
	t.mu.RLock()
	limits := make(map[string]int64, len(t.currentLimits))
	maps.Copy(limits, t.currentLimits)
	t.mu.RUnlock()

	decisions := t.policy.Evaluate(signals, limits, now)
	if len(decisions) == 0 {
		return
	}

	// APPLY: apply decisions with safeguards.
	t.apply(ctx, decisions, now)
}

// apply applies decisions through the safeguard chain.
func (t *Tuner) apply(ctx context.Context, decisions []Decision, now time.Time) {
	ok, err := t.rateLimiter.TryReload(func() error {
		return t.coordinator.Execute(func() error {
			return t.applyWithTimeout(ctx, decisions)
		})
	}, now)

	if err != nil {
		t.errorsTotal.Add(1)
		t.errorBudget.RecordError(now)
		logging.Warn("autotune: apply failed", map[string]any{"error": err.Error()})
		return
	}
	if !ok {
		return // rate limited
	}

	// Start post-apply verification.
	t.verifier.StartVerification(decisions, now)

	// Persist changes.
	changes := decisionsToChanges(decisions)
	if err := t.persister.Persist(ctx, changes); err != nil {
		logging.Warn("autotune: persistence failed (non-blocking)", map[string]any{"error": err.Error()})
	}

	t.adjustmentsTotal.Add(int64(len(decisions)))
}

// applyWithTimeout wraps the apply operation with a context deadline.
func (t *Tuner) applyWithTimeout(ctx context.Context, decisions []Decision) error {
	ctx, cancel := context.WithTimeout(ctx, t.cfg.Safeguards.ReloadTimeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- t.applyDecisions(decisions)
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		t.reloadTimeouts.Add(1)
		return fmt.Errorf("reload timed out after %s", t.cfg.Safeguards.ReloadTimeout)
	}
}

// applyDecisions updates the enforcer config with the new limits.
func (t *Tuner) applyDecisions(decisions []Decision) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Snapshot previous limits for rollback.
	maps.Copy(t.previousLimits, t.currentLimits)

	// Update current limits.
	for _, d := range decisions {
		t.currentLimits[d.RuleName] = d.NewValue
		logging.Info("autotune: limit adjusted", map[string]any{
			"action": string(d.Action), "rule": d.RuleName, "field": d.Field,
			"old": d.OldValue, "new": d.NewValue, "reason": d.Reason,
		})
	}

	// Call enforcer's ReloadConfig if available.
	if t.reloadFunc != nil {
		t.reloadFunc(t.currentLimits)
	}

	return nil
}

// handleRollback reverts the enforcer to previous limits.
func (t *Tuner) handleRollback(_ context.Context, decisions []Decision, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, d := range decisions {
		if prev, ok := t.previousLimits[d.RuleName]; ok {
			t.currentLimits[d.RuleName] = prev
			logging.Warn("autotune: ROLLBACK", map[string]any{
				"rule": d.RuleName, "from": d.NewValue, "to": prev,
				"reason": "drop rate exceeded threshold",
			})
		}
		// Apply extended cooldown.
		cooldown := t.cfg.Policy.Cooldown * time.Duration(t.cfg.Safeguards.Verification.RollbackCooldownFactor)
		t.policy.SetCooldown(d.RuleName, cooldown, now)
	}

	t.rollbacksTotal.Add(int64(len(decisions)))

	// Reload enforcer with reverted limits.
	if t.reloadFunc != nil {
		t.reloadFunc(t.currentLimits)
	}

	t.errorBudget.RecordError(now)
}

// collectInternalOnly gathers signals from internal sources only (no external queries).
func (t *Tuner) collectInternalOnly() AggregatedSignals {
	signals := AggregatedSignals{
		Utilization:            make(map[string]float64),
		CardinalityUtilization: make(map[string]float64),
		DPRateUtilization:      make(map[string]float64),
		RuleUtilization:        make(map[string]RuleUtilization),
		Timestamp:              time.Now(),
	}
	if t.dropsFunc != nil {
		signals.InternalDropTotal = t.dropsFunc()
	}
	if t.utilFunc != nil {
		ruleUtils := t.utilFunc()
		for name, ru := range ruleUtils {
			signals.RuleUtilization[name] = ru
			var cardUtil, dpUtil float64
			if ru.MaxCard > 0 {
				cardUtil = float64(ru.CurrentCard) / float64(ru.MaxCard)
			}
			if ru.MaxDPRate > 0 {
				dpUtil = float64(ru.CurrentDPs) / float64(ru.MaxDPRate)
			}
			signals.CardinalityUtilization[name] = cardUtil
			signals.DPRateUtilization[name] = dpUtil
			util := cardUtil
			if dpUtil > util {
				util = dpUtil
			}
			signals.Utilization[name] = util
		}
	}
	return signals
}

// snapshotLimits reads the current enforcer limits into the tuner's state.
func (t *Tuner) snapshotLimits() {
	if t.utilFunc == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	ruleUtils := t.utilFunc()
	for name, ru := range ruleUtils {
		if ru.MaxCard > 0 {
			t.currentLimits[name] = ru.MaxCard
			t.baselineLimits[name] = ru.MaxCard
		}
	}
}

// UpdateBaseline updates the autotune baseline after a manual SIGHUP.
// This ensures autotune builds on the operator's latest config.
func (t *Tuner) UpdateBaseline() {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Cancel any active verification.
	t.verifier.Cancel()

	// Reset rate limiter timer.
	t.rateLimiter.Reset(time.Now())

	// Reset policy state.
	t.policy.ResetAll()

	// Re-snapshot from enforcer.
	if t.utilFunc != nil {
		ruleUtils := t.utilFunc()
		for name, ru := range ruleUtils {
			if ru.MaxCard > 0 {
				t.currentLimits[name] = ru.MaxCard
				t.baselineLimits[name] = ru.MaxCard
			}
		}
	}
}

// decisionsToChanges converts decisions to persistence records.
func decisionsToChanges(decisions []Decision) []ConfigChange {
	changes := make([]ConfigChange, len(decisions))
	for i, d := range decisions {
		changes[i] = ConfigChange{
			Timestamp: d.Timestamp,
			Domain:    "limits",
			RuleName:  d.RuleName,
			Field:     d.Field,
			OldValue:  d.OldValue,
			NewValue:  d.NewValue,
			Action:    string(d.Action),
			Reason:    d.Reason,
			Signals:   d.Signals,
		}
	}
	return changes
}

// ServeHTTP writes autotune metrics in Prometheus text format.
func (t *Tuner) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	writeAutotuneMetrics(w, t)
}
