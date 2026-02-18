package autotune

import (
	"math"
	"sync"
	"time"
)

// PolicyEngine evaluates signals and produces decisions per rule.
type PolicyEngine struct {
	cfg PolicyConfig

	mu          sync.Mutex
	lastAction  map[string]time.Time // rule -> last action time (for cooldown)
	lowSince    map[string]time.Time // rule -> when utilization first went below decrease threshold
	oscillation *OscillationDetector
}

// NewPolicyEngine creates a policy engine with the given config.
func NewPolicyEngine(cfg PolicyConfig, oscillation *OscillationDetector) *PolicyEngine {
	return &PolicyEngine{
		cfg:         cfg,
		lastAction:  make(map[string]time.Time),
		lowSince:    make(map[string]time.Time),
		oscillation: oscillation,
	}
}

// Evaluate produces decisions for all rules based on the aggregated signals.
// currentLimits maps rule name -> current max_cardinality.
func (pe *PolicyEngine) Evaluate(signals AggregatedSignals, currentLimits map[string]int64, now time.Time) []Decision {
	pe.mu.Lock()
	defer pe.mu.Unlock()

	var decisions []Decision

	for ruleName, util := range signals.Utilization {
		limit, ok := currentLimits[ruleName]
		if !ok || limit <= 0 {
			continue
		}

		// Check cooldown.
		if lastTime, ok := pe.lastAction[ruleName]; ok {
			if now.Sub(lastTime) < pe.cfg.Cooldown {
				continue
			}
		}

		// Check oscillation freeze.
		if pe.oscillation != nil && pe.oscillation.IsFrozen(ruleName, now) {
			continue
		}

		decision := pe.evaluateRule(ruleName, util, limit, signals, now)
		if decision.Action == ActionHold {
			continue
		}

		// Record action time and oscillation history.
		pe.lastAction[ruleName] = now
		if pe.oscillation != nil {
			pe.oscillation.Record(ruleName, decision)
		}

		decisions = append(decisions, decision)
	}

	return decisions
}

// evaluateRule produces a decision for a single rule.
func (pe *PolicyEngine) evaluateRule(ruleName string, util float64, currentLimit int64, signals AggregatedSignals, now time.Time) Decision {
	base := Decision{
		RuleName:  ruleName,
		Field:     "max_cardinality",
		OldValue:  currentLimit,
		Timestamp: now,
		Signals: DecisionSignals{
			Utilization: util,
			DropRate:    signals.InternalDropRate,
		},
	}

	// Check anomaly score (Tier 2) — tighten overrides other actions.
	if signals.AnomalyScores != nil {
		if score, ok := signals.AnomalyScores[ruleName]; ok {
			base.Signals.AnomalyScore = score
			if score >= pe.cfg.AnomalyTightenScore {
				newVal := pe.applyFactor(currentLimit, pe.cfg.TightenFactor)
				newVal = pe.clamp(newVal)
				newVal = pe.limitChange(currentLimit, newVal)
				base.Action = ActionTighten
				base.NewValue = newVal
				base.Reason = "anomaly score exceeded tighten threshold"
				return base
			}
		}
	}

	// High utilization → increase.
	if util >= pe.cfg.IncreaseThreshold {
		// If anomaly score is high, don't grow (might be a bomb).
		if signals.AnomalyScores != nil {
			if score, ok := signals.AnomalyScores[ruleName]; ok && score >= pe.cfg.AnomalySafeScore {
				// Not safe to grow — hold.
				base.Action = ActionHold
				base.Reason = "high utilization but anomaly score unsafe for growth"
				return base
			}
		}

		newVal := pe.applyFactor(currentLimit, pe.cfg.GrowFactor)
		newVal = pe.clamp(newVal)
		newVal = pe.limitChange(currentLimit, newVal)
		if newVal == currentLimit {
			base.Action = ActionHold
			return base
		}
		delete(pe.lowSince, ruleName) // reset low tracker
		base.Action = ActionIncrease
		base.NewValue = newVal
		base.Reason = "utilization above increase threshold"
		return base
	}

	// Low utilization → decrease (after sustain time).
	if util <= pe.cfg.DecreaseThreshold {
		firstLow, ok := pe.lowSince[ruleName]
		if !ok {
			pe.lowSince[ruleName] = now
			base.Action = ActionHold
			return base
		}
		if now.Sub(firstLow) < pe.cfg.DecreaseSustainTime {
			base.Action = ActionHold
			return base
		}

		newVal := pe.applyFactor(currentLimit, pe.cfg.ShrinkFactor)
		newVal = pe.clamp(newVal)
		newVal = pe.limitChange(currentLimit, newVal)
		if newVal == currentLimit {
			base.Action = ActionHold
			return base
		}
		base.Action = ActionDecrease
		base.NewValue = newVal
		base.Reason = "utilization below decrease threshold for sustain period"
		return base
	}

	// In the normal range — hold and reset low tracker.
	delete(pe.lowSince, ruleName)
	base.Action = ActionHold
	return base
}

// applyFactor multiplies a limit by a factor, rounding to the nearest integer.
func (pe *PolicyEngine) applyFactor(current int64, factor float64) int64 {
	return int64(math.Round(float64(current) * factor))
}

// clamp ensures a value is within [MinCardinality, MaxCardinality].
func (pe *PolicyEngine) clamp(val int64) int64 {
	if val < pe.cfg.MinCardinality {
		return pe.cfg.MinCardinality
	}
	if val > pe.cfg.MaxCardinality {
		return pe.cfg.MaxCardinality
	}
	return val
}

// limitChange ensures the change doesn't exceed MaxChangePct of current.
func (pe *PolicyEngine) limitChange(current, proposed int64) int64 {
	maxDelta := int64(math.Round(float64(current) * pe.cfg.MaxChangePct))
	delta := proposed - current
	if delta > maxDelta {
		return current + maxDelta
	}
	if delta < -maxDelta {
		return current - maxDelta
	}
	return proposed
}

// SetCooldown overrides the cooldown for a specific rule (used by verifier after rollback).
func (pe *PolicyEngine) SetCooldown(ruleName string, cooldown time.Duration, now time.Time) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	// Set the lastAction to a future time such that cooldown expires at now + cooldown.
	pe.lastAction[ruleName] = now.Add(cooldown - pe.cfg.Cooldown)
}

// ResetLowSince clears the low-since tracker for a rule (used on baseline update).
func (pe *PolicyEngine) ResetLowSince(ruleName string) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	delete(pe.lowSince, ruleName)
}

// ResetAll clears all tracking state (used on baseline update from SIGHUP).
func (pe *PolicyEngine) ResetAll() {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	pe.lastAction = make(map[string]time.Time)
	pe.lowSince = make(map[string]time.Time)
}
