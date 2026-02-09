// Package ruleactivity provides shared dead-rule detection primitives for both
// processing and limits subsystems. Each subsystem tracks rule match timestamps
// and uses a scanner goroutine to detect rules that stop matching.
package ruleactivity

import (
	"log"
	"sync/atomic"
	"time"
)

// Activity tracks the match lifecycle of a single rule.
// Fields are atomic for lock-free hot-path recording.
type Activity struct {
	LastMatchTime atomic.Int64 // Unix nanos of last match (0 = never)
	LoadedTime    int64        // Unix nanos when rule was loaded (immutable after init)
	WasDead       atomic.Bool  // For logging state transitions (scanner only)
}

// NewActivity creates an Activity with LoadedTime set to now.
func NewActivity() *Activity {
	return &Activity{LoadedTime: time.Now().UnixNano()}
}

// RecordMatch updates the last match timestamp to now.
func (a *Activity) RecordMatch() {
	a.LastMatchTime.Store(time.Now().UnixNano())
}

// IsDead returns whether a rule should be considered dead given a threshold duration.
// A rule is dead if:
//   - It has never matched AND has been loaded longer than threshold, OR
//   - Its last match was longer ago than threshold.
func (a *Activity) IsDead(now int64, thresholdNanos int64) bool {
	lastMatch := a.LastMatchTime.Load()
	if lastMatch == 0 {
		return (now - a.LoadedTime) > thresholdNanos
	}
	return (now - lastMatch) > thresholdNanos
}

// EvalResult holds the outcome of evaluating a single rule's liveness.
type EvalResult struct {
	Name         string
	IsDead       bool
	Transitioned bool   // true if state changed (alive→dead or dead→alive)
	Direction    string // "dead" or "alive" (only meaningful when Transitioned)
}

// EvaluateAndTransition checks liveness and handles the state transition.
// Returns whether the rule is dead and whether a transition occurred.
// This method handles the atomic wasDead swap for logging state transitions.
func (a *Activity) EvaluateAndTransition(now int64, thresholdNanos int64) (isDead bool, transitioned bool, direction string) {
	isDead = a.IsDead(now, thresholdNanos)

	if isDead {
		if !a.WasDead.Swap(true) {
			return true, true, "dead"
		}
		return true, false, ""
	}

	if a.WasDead.Swap(false) {
		return false, true, "alive"
	}
	return false, false, ""
}

// LogTransition logs a state transition if one occurred.
func LogTransition(subsystem, ruleName string, isDead, transitioned bool, threshold time.Duration) {
	if !transitioned {
		return
	}
	if isDead {
		log.Printf("[WARN] %s rule %q appears dead — no match in %v", subsystem, ruleName, threshold)
	} else {
		log.Printf("[INFO] %s rule %q is alive again", subsystem, ruleName)
	}
}
