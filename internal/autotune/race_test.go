package autotune

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestRace_PolicyEngine_ConcurrentEvaluate(t *testing.T) {
	// 16 goroutines evaluating concurrently with different rules
	engine := NewPolicyEngine(DefaultPolicyConfig(), NewOscillationDetector(OscillationConfig{
		LookbackCount:  6,
		Threshold:      0.75,
		FreezeDuration: time.Minute,
	}))
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				rule := fmt.Sprintf("rule_%d", id%4)
				signals := AggregatedSignals{
					Utilization: map[string]float64{rule: 0.90},
				}
				engine.Evaluate(signals, map[string]int64{rule: 1000}, time.Now())
			}
		}(i)
	}
	wg.Wait()
}

func TestRace_CircuitBreaker_ConcurrentTransitions(t *testing.T) {
	// 8 goroutines forcing failures + 8 checking state
	cb := NewCircuitBreaker("test-cb", 3, 100*time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				if cb.Allow() {
					cb.RecordFailure()
				}
			}
		}()
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_ = cb.State()
			}
		}()
	}
	wg.Wait()
}

func TestRace_ErrorBudget_ConcurrentRecordCheck(t *testing.T) {
	eb := NewErrorBudget(ErrorBudgetConfig{
		Window:        time.Hour,
		MaxErrors:     100,
		PauseDuration: time.Minute,
	})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				eb.RecordError(time.Now())
			}
		}()
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_ = eb.IsPaused(time.Now())
			}
		}()
	}
	wg.Wait()
}

func TestRace_OscillationDetector_ConcurrentCheck(t *testing.T) {
	od := NewOscillationDetector(OscillationConfig{
		LookbackCount:  6,
		Threshold:      0.75,
		FreezeDuration: time.Minute,
	})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				action := ActionIncrease
				if j%2 == 0 {
					action = ActionDecrease
				}
				od.Record("rule1", Decision{Action: action})
			}
		}()
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_ = od.IsFrozen("rule1", time.Now())
			}
		}()
	}
	wg.Wait()
}

func TestRace_ReloadRateLimiter_ConcurrentReload(t *testing.T) {
	rl := NewReloadRateLimiter(10 * time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				rl.TryReload(func() error { return nil }, time.Now())
				time.Sleep(time.Millisecond)
			}
		}()
	}
	wg.Wait()
}
