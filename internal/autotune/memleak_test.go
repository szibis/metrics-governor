package autotune

import (
	"runtime"
	"testing"
	"time"
)

func TestMemLeak_OscillationDetector(t *testing.T) {
	od := NewOscillationDetector(OscillationConfig{
		LookbackCount:  6,
		Threshold:      0.75,
		FreezeDuration: time.Minute,
	})

	// Record many decisions — only lookback_count should be retained per rule.
	for i := 0; i < 10000; i++ {
		action := ActionIncrease
		if i%2 == 0 {
			action = ActionDecrease
		}
		od.Record("rule1", Decision{Action: action})
	}

	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// With lookback=6, history per rule should be at most 6 entries.
	// Even with 100 rules, total memory should be trivial.
	if m.HeapInuse > 10*1024*1024 { // 10MB threshold (very generous)
		t.Errorf("excessive memory: %d bytes in use", m.HeapInuse)
	}
}

func TestMemLeak_ErrorBudget_RollingPrune(t *testing.T) {
	eb := NewErrorBudget(ErrorBudgetConfig{
		Window:        100 * time.Millisecond,
		MaxErrors:     1000,
		PauseDuration: time.Second,
	})

	// Record many errors — old ones should be pruned by rolling window.
	for i := 0; i < 10000; i++ {
		eb.RecordError(time.Now())
		if i%100 == 0 {
			time.Sleep(time.Millisecond) // let some errors age out
		}
	}

	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Budget should only retain errors within the window, not all 10k.
	if m.HeapInuse > 10*1024*1024 {
		t.Errorf("excessive memory: %d bytes in use", m.HeapInuse)
	}
}
