package limits

import (
	"math"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/szibis/metrics-governor/internal/ruleactivity"
)

// Dead rule detection metrics (always-on).
var (
	limitsRuleLastMatchSeconds = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "metrics_governor_limits_rule_last_match_seconds",
		Help: "Seconds since a limits rule last matched (Inf if never)",
	}, []string{"rule"})

	limitsRuleNeverMatched = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "metrics_governor_limits_rule_never_matched",
		Help: "1 if a limits rule has never matched since load, 0 otherwise",
	}, []string{"rule"})

	limitsRuleLoadedSeconds = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "metrics_governor_limits_rule_loaded_seconds",
		Help: "Seconds since a limits rule was loaded",
	}, []string{"rule"})
)

// Dead rule scanner metrics (only set when scanner is enabled).
var (
	limitsRuleDead = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "metrics_governor_limits_rule_dead",
		Help: "1 if the limits rule is considered dead, 0 if alive (scanner only)",
	}, []string{"rule"})

	limitsRulesDeadTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_limits_rules_dead_total",
		Help: "Total count of dead limits rules (scanner only)",
	})
)

func init() {
	prometheus.MustRegister(limitsRuleLastMatchSeconds)
	prometheus.MustRegister(limitsRuleNeverMatched)
	prometheus.MustRegister(limitsRuleLoadedSeconds)
	prometheus.MustRegister(limitsRuleDead)
	prometheus.MustRegister(limitsRulesDeadTotal)
	limitsRulesDeadTotal.Set(0)
}

// initRuleActivity initializes the activity map for all configured rules.
func initRuleActivity(rules []Rule) map[string]*ruleactivity.Activity {
	m := make(map[string]*ruleactivity.Activity, len(rules))
	for _, r := range rules {
		m[r.Name] = ruleactivity.NewActivity()
	}
	return m
}

// recordRuleMatch updates the last match timestamp for a rule.
func (e *Enforcer) recordRuleMatch(ruleName string) {
	if a, ok := e.ruleActivityMap[ruleName]; ok {
		a.RecordMatch()
	}
}

// updateLimitsDeadRuleMetrics computes always-on dead rule gauges.
func (e *Enforcer) updateLimitsDeadRuleMetrics() {
	now := time.Now().UnixNano()
	for name, a := range e.ruleActivityMap {
		lastMatch := a.LastMatchTime.Load()
		if lastMatch == 0 {
			limitsRuleLastMatchSeconds.WithLabelValues(name).Set(math.Inf(1))
			limitsRuleNeverMatched.WithLabelValues(name).Set(1)
		} else {
			elapsed := float64(now-lastMatch) / float64(time.Second)
			limitsRuleLastMatchSeconds.WithLabelValues(name).Set(elapsed)
			limitsRuleNeverMatched.WithLabelValues(name).Set(0)
		}
		loadedElapsed := float64(now-a.LoadedTime) / float64(time.Second)
		limitsRuleLoadedSeconds.WithLabelValues(name).Set(loadedElapsed)
	}
}

// limitsDeadRuleScanner periodically evaluates limits rules for liveness.
type limitsDeadRuleScanner struct {
	mu       sync.Mutex
	interval time.Duration
	enforcer *Enforcer
	stopCh   chan struct{}
	stopped  chan struct{}
}

func newLimitsDeadRuleScanner(interval time.Duration, enforcer *Enforcer) *limitsDeadRuleScanner {
	s := &limitsDeadRuleScanner{
		interval: interval,
		enforcer: enforcer,
		stopCh:   make(chan struct{}),
		stopped:  make(chan struct{}),
	}
	go s.run()
	return s
}

func (s *limitsDeadRuleScanner) run() {
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

func (s *limitsDeadRuleScanner) scan() {
	s.mu.Lock()
	e := s.enforcer
	threshold := s.interval
	s.mu.Unlock()

	now := time.Now().UnixNano()
	thresholdNanos := threshold.Nanoseconds()
	deadCount := 0

	for name, a := range e.ruleActivityMap {
		isDead, transitioned, _ := a.EvaluateAndTransition(now, thresholdNanos)

		if isDead {
			deadCount++
			limitsRuleDead.WithLabelValues(name).Set(1)
		} else {
			limitsRuleDead.WithLabelValues(name).Set(0)
		}

		if transitioned {
			ruleactivity.LogTransition("limits", name, isDead, transitioned, threshold)
		}
	}

	limitsRulesDeadTotal.Set(float64(deadCount))
}

func (s *limitsDeadRuleScanner) stop() {
	close(s.stopCh)
	<-s.stopped
}
