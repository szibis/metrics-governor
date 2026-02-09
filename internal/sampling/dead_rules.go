package sampling

import (
	"log"
	"math"
	"sync"
	"time"
)

// deadRuleScanner periodically evaluates processing rules for liveness.
type deadRuleScanner struct {
	mu       sync.Mutex
	interval time.Duration
	rules    []ProcessingRule
	stopCh   chan struct{}
	stopped  chan struct{}
}

// updateDeadRuleMetrics computes always-on dead rule gauge values from current rule state.
// Called on Prometheus scrapes (via the Sampler) — zero background CPU cost.
func updateDeadRuleMetrics(rules []ProcessingRule) {
	now := time.Now().UnixNano()
	for i := range rules {
		r := &rules[i]
		ruleName := r.Name
		action := string(r.Action)

		lastMatch := r.metrics.dead.lastMatchTime.Load()
		loadedAt := r.metrics.dead.loadedTime

		if lastMatch == 0 {
			// Never matched: report Inf for last_match_seconds.
			processingRuleLastMatchSeconds.WithLabelValues(ruleName, action).Set(math.Inf(1))
			processingRuleNeverMatched.WithLabelValues(ruleName, action).Set(1)
		} else {
			elapsed := float64(now-lastMatch) / float64(time.Second)
			processingRuleLastMatchSeconds.WithLabelValues(ruleName, action).Set(elapsed)
			processingRuleNeverMatched.WithLabelValues(ruleName, action).Set(0)
		}

		loadedElapsed := float64(now-loadedAt) / float64(time.Second)
		processingRuleLoadedSeconds.WithLabelValues(ruleName, action).Set(loadedElapsed)
	}
}

// newDeadRuleScanner creates and starts a scanner goroutine.
// The scanner ticks at interval/2 and sets _rule_dead gauges + logs transitions.
func newDeadRuleScanner(interval time.Duration, rules []ProcessingRule) *deadRuleScanner {
	s := &deadRuleScanner{
		interval: interval,
		rules:    rules,
		stopCh:   make(chan struct{}),
		stopped:  make(chan struct{}),
	}
	go s.run()
	return s
}

func (s *deadRuleScanner) run() {
	defer close(s.stopped)

	tick := time.NewTicker(s.interval / 2)
	defer tick.Stop()

	for {
		select {
		case <-tick.C:
			s.scan()
		case <-s.stopCh:
			return
		}
	}
}

func (s *deadRuleScanner) scan() {
	s.mu.Lock()
	rules := s.rules
	threshold := s.interval
	s.mu.Unlock()

	now := time.Now().UnixNano()
	thresholdNanos := threshold.Nanoseconds()
	deadCount := 0

	for i := range rules {
		r := &rules[i]
		ruleName := r.Name
		action := string(r.Action)

		lastMatch := r.metrics.dead.lastMatchTime.Load()
		isDead := false

		if lastMatch == 0 {
			// Never matched — consider dead if loaded longer than threshold.
			loadedAge := now - r.metrics.dead.loadedTime
			isDead = loadedAge > thresholdNanos
		} else {
			isDead = (now - lastMatch) > thresholdNanos
		}

		if isDead {
			deadCount++
			processingRuleDead.WithLabelValues(ruleName, action).Set(1)

			// Log state transition: alive → dead.
			if !r.metrics.dead.wasDead.Swap(true) {
				log.Printf("[WARN] processing rule %q (action=%s) appears dead — no match in %v", ruleName, action, threshold)
			}
		} else {
			processingRuleDead.WithLabelValues(ruleName, action).Set(0)

			// Log state transition: dead → alive.
			if r.metrics.dead.wasDead.Swap(false) {
				log.Printf("[INFO] processing rule %q (action=%s) is alive again", ruleName, action)
			}
		}
	}

	processingRulesDeadTotal.Set(float64(deadCount))
}

// updateRules replaces the rule set used by the scanner (for hot-reload).
func (s *deadRuleScanner) updateRules(rules []ProcessingRule) {
	s.mu.Lock()
	s.rules = rules
	s.mu.Unlock()
}

// stop shuts down the scanner goroutine.
func (s *deadRuleScanner) stop() {
	close(s.stopCh)
	<-s.stopped
}
