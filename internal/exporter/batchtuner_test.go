package exporter

import (
	"fmt"
	"net/http"
	"runtime"
	"sync"
	"testing"
	"time"
)

// --- Helpers ---

func newTestTuner(t *testing.T) *BatchTuner {
	t.Helper()
	return NewBatchTuner(BatchTunerConfig{
		Enabled:       true,
		MinBytes:      512,
		MaxBytes:      16 * 1024 * 1024, // 16MB
		SuccessStreak: 10,
		GrowFactor:    1.25,
		ShrinkFactor:  0.5,
	})
}

func make413Error() error {
	return &ExportError{
		Err:        fmt.Errorf("HTTP 413: payload too large"),
		Type:       ErrorTypeClientError,
		StatusCode: http.StatusRequestEntityTooLarge,
		Message:    "payload too large",
	}
}

func makeGenericError() error {
	return &ExportError{
		Err:        fmt.Errorf("HTTP 500: internal server error"),
		Type:       ErrorTypeServerError,
		StatusCode: http.StatusInternalServerError,
		Message:    "internal server error",
	}
}

// recordSuccesses records n consecutive successes on the tuner.
func recordSuccesses(t *testing.T, bt *BatchTuner, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		bt.RecordSuccess(time.Millisecond, 1000, 500)
	}
}

// --- Unit tests ---

func TestBatchTuner_DefaultConfig(t *testing.T) {
	// Providing zero/invalid values should yield defaults.
	bt := NewBatchTuner(BatchTunerConfig{Enabled: true})

	if bt.minBytes != 512 {
		t.Errorf("expected minBytes=512, got %d", bt.minBytes)
	}
	if bt.maxBytes != 16*1024*1024 {
		t.Errorf("expected maxBytes=16MB, got %d", bt.maxBytes)
	}
	if bt.successStreak != 10 {
		t.Errorf("expected successStreak=10, got %d", bt.successStreak)
	}
	if bt.growFactor != 1.25 {
		t.Errorf("expected growFactor=1.25, got %f", bt.growFactor)
	}
	if bt.shrinkFactor != 0.5 {
		t.Errorf("expected shrinkFactor=0.5, got %f", bt.shrinkFactor)
	}
	if bt.CurrentMaxBytes() != 16*1024*1024 {
		t.Errorf("expected initial CurrentMaxBytes=16MB, got %d", bt.CurrentMaxBytes())
	}
}

func TestBatchTuner_GrowsOnSuccessStreak(t *testing.T) {
	bt := newTestTuner(t)

	// The tuner starts at MaxBytes, so first shrink it to create room for growth.
	bt.RecordFailure(makeGenericError())
	shrunk := bt.CurrentMaxBytes() // 16MB * 0.5 = 8MB

	// Record exactly SuccessStreak (10) consecutive successes.
	recordSuccesses(t, bt, 10)

	after := bt.CurrentMaxBytes()
	expected := int(float64(shrunk) * 1.25)

	if after != expected {
		t.Errorf("after 10 successes: expected CurrentMaxBytes=%d (shrunk*1.25), got %d",
			expected, after)
	}
}

func TestBatchTuner_NoGrowBeforeStreak(t *testing.T) {
	bt := newTestTuner(t)
	initial := bt.CurrentMaxBytes()

	// Record 9 successes — one short of the streak threshold.
	recordSuccesses(t, bt, 9)

	if bt.CurrentMaxBytes() != initial {
		t.Errorf("expected no growth before streak threshold: initial=%d, got=%d",
			initial, bt.CurrentMaxBytes())
	}
}

func TestBatchTuner_ShrinksOnFailure(t *testing.T) {
	bt := newTestTuner(t)
	initial := bt.CurrentMaxBytes()

	bt.RecordFailure(makeGenericError())

	after := bt.CurrentMaxBytes()
	expected := int(float64(initial) * 0.5)

	if after != expected {
		t.Errorf("after failure: expected CurrentMaxBytes=%d (initial*0.5), got %d",
			expected, after)
	}
}

func TestBatchTuner_HTTP413SetsHardCeiling(t *testing.T) {
	bt := newTestTuner(t)
	initial := bt.CurrentMaxBytes()

	bt.RecordFailure(make413Error())

	// Hard ceiling should be set at 80% of the current value before shrink.
	expectedCeiling := int64(float64(initial) * 0.8)
	actualCeiling := bt.hardCeiling.Load()

	if actualCeiling != expectedCeiling {
		t.Errorf("expected hard ceiling=%d (80%% of %d), got %d",
			expectedCeiling, initial, actualCeiling)
	}

	// The current max should also have shrunk by ShrinkFactor.
	expectedMax := int(float64(initial) * 0.5)
	if bt.CurrentMaxBytes() != expectedMax {
		t.Errorf("after 413: expected CurrentMaxBytes=%d, got %d",
			expectedMax, bt.CurrentMaxBytes())
	}
}

func TestBatchTuner_NeverExceedsHardCeiling(t *testing.T) {
	bt := NewBatchTuner(BatchTunerConfig{
		Enabled:       true,
		MinBytes:      100,
		MaxBytes:      100000,
		SuccessStreak: 1, // Grow after every success for faster testing.
		GrowFactor:    2.0,
		ShrinkFactor:  0.5,
	})

	// Set a low ceiling manually (simulating a 413 discovery).
	bt.hardCeiling.Store(5000)

	// Shrink first so there is room to grow.
	bt.currentMaxBytes.Store(1000)

	// Record successes and try to grow past the ceiling.
	for i := 0; i < 20; i++ {
		bt.RecordSuccess(time.Millisecond, 1000, 500)
	}

	if bt.CurrentMaxBytes() > 5000 {
		t.Errorf("CurrentMaxBytes=%d exceeds hard ceiling=5000", bt.CurrentMaxBytes())
	}
}

func TestBatchTuner_NeverBelowMinBytes(t *testing.T) {
	bt := NewBatchTuner(BatchTunerConfig{
		Enabled:       true,
		MinBytes:      1024,
		MaxBytes:      16 * 1024 * 1024,
		SuccessStreak: 10,
		GrowFactor:    1.25,
		ShrinkFactor:  0.5,
	})

	// Repeatedly shrink with failures.
	for i := 0; i < 50; i++ {
		bt.RecordFailure(makeGenericError())
	}

	if bt.CurrentMaxBytes() < 1024 {
		t.Errorf("CurrentMaxBytes=%d fell below MinBytes=1024", bt.CurrentMaxBytes())
	}
	if bt.CurrentMaxBytes() != 1024 {
		t.Errorf("expected CurrentMaxBytes to clamp at MinBytes=1024, got %d", bt.CurrentMaxBytes())
	}
}

func TestBatchTuner_NeverAboveMaxBytes(t *testing.T) {
	maxBytes := 1000
	bt := NewBatchTuner(BatchTunerConfig{
		Enabled:       true,
		MinBytes:      100,
		MaxBytes:      maxBytes,
		SuccessStreak: 1, // Grow after every success.
		GrowFactor:    2.0,
		ShrinkFactor:  0.5,
	})

	// Many successes — should never exceed MaxBytes.
	for i := 0; i < 100; i++ {
		bt.RecordSuccess(time.Millisecond, 1000, 500)
	}

	if bt.CurrentMaxBytes() > maxBytes {
		t.Errorf("CurrentMaxBytes=%d exceeds MaxBytes=%d", bt.CurrentMaxBytes(), maxBytes)
	}
}

func TestBatchTuner_AtomicCurrentMaxBytes(t *testing.T) {
	bt := newTestTuner(t)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				v := bt.CurrentMaxBytes()
				if v <= 0 {
					t.Errorf("CurrentMaxBytes returned non-positive value: %d", v)
					return
				}
				runtime.Gosched()
			}
		}()
	}

	wg.Wait()
}

func TestBatchTuner_ConsecutiveOKResetOnFailure(t *testing.T) {
	bt := newTestTuner(t)
	initial := bt.CurrentMaxBytes()

	// Record 9 successes (one short of streak).
	recordSuccesses(t, bt, 9)

	// A failure resets the streak counter.
	bt.RecordFailure(makeGenericError())

	afterFailure := bt.CurrentMaxBytes()
	expected := int(float64(initial) * 0.5)
	if afterFailure != expected {
		t.Errorf("after failure: expected CurrentMaxBytes=%d, got %d", expected, afterFailure)
	}

	// Record 9 more successes — still should not trigger growth because streak was reset.
	recordSuccesses(t, bt, 9)
	if bt.CurrentMaxBytes() != afterFailure {
		t.Errorf("expected no growth after 9 successes post-reset: was %d, now %d",
			afterFailure, bt.CurrentMaxBytes())
	}

	// The 10th success after the failure should trigger growth.
	bt.RecordSuccess(time.Millisecond, 1000, 500)
	expectedGrown := int(float64(afterFailure) * 1.25)
	if bt.CurrentMaxBytes() != expectedGrown {
		t.Errorf("after full streak post-failure: expected %d, got %d",
			expectedGrown, bt.CurrentMaxBytes())
	}
}

func TestBatchTuner_MultipleFailuresShrinkMultipleTimes(t *testing.T) {
	bt := NewBatchTuner(BatchTunerConfig{
		Enabled:       true,
		MinBytes:      100,
		MaxBytes:      100000,
		SuccessStreak: 10,
		GrowFactor:    1.25,
		ShrinkFactor:  0.5,
	})

	initial := bt.CurrentMaxBytes()
	err := makeGenericError()

	bt.RecordFailure(err)
	after1 := bt.CurrentMaxBytes()
	expected1 := int(float64(initial) * 0.5)
	if after1 != expected1 {
		t.Errorf("after 1st failure: expected %d, got %d", expected1, after1)
	}

	bt.RecordFailure(err)
	after2 := bt.CurrentMaxBytes()
	expected2 := int(float64(after1) * 0.5)
	if after2 != expected2 {
		t.Errorf("after 2nd failure: expected %d, got %d", expected2, after2)
	}

	bt.RecordFailure(err)
	after3 := bt.CurrentMaxBytes()
	expected3 := int(float64(after2) * 0.5)
	if after3 != expected3 {
		t.Errorf("after 3rd failure: expected %d, got %d", expected3, after3)
	}
}

func TestBatchTuner_HTTP413CeilingClampsToMinBytes(t *testing.T) {
	// When the current value is already very small, ceiling should not fall below minBytes.
	bt := NewBatchTuner(BatchTunerConfig{
		Enabled:       true,
		MinBytes:      1000,
		MaxBytes:      2000,
		SuccessStreak: 10,
		GrowFactor:    1.25,
		ShrinkFactor:  0.5,
	})

	// Force the current max very low.
	bt.currentMaxBytes.Store(800)

	bt.RecordFailure(make413Error())

	ceiling := bt.hardCeiling.Load()
	// 80% of 800 = 640, which is below minBytes=1000 — so ceiling should clamp to minBytes.
	if ceiling != 1000 {
		t.Errorf("expected ceiling clamped to minBytes=1000, got %d", ceiling)
	}
}

func TestBatchTuner_GrowResetStreakCounter(t *testing.T) {
	// Use a tuner with large MaxBytes and start low so there is room for multiple growths.
	bt := NewBatchTuner(BatchTunerConfig{
		Enabled:       true,
		MinBytes:      100,
		MaxBytes:      1000000,
		SuccessStreak: 10,
		GrowFactor:    1.25,
		ShrinkFactor:  0.5,
	})

	// Start at a low value so growth has room.
	bt.currentMaxBytes.Store(1000)

	// Trigger a growth (10 successes).
	recordSuccesses(t, bt, 10)
	afterFirst := bt.CurrentMaxBytes()
	expectedFirst := int(float64(1000) * 1.25) // 1250
	if afterFirst != expectedFirst {
		t.Errorf("after first streak: expected %d, got %d", expectedFirst, afterFirst)
	}

	// After growth the streak resets; 9 more successes should NOT trigger growth.
	recordSuccesses(t, bt, 9)
	if bt.CurrentMaxBytes() != afterFirst {
		t.Errorf("expected no growth after 9 successes post-grow: was %d, now %d",
			afterFirst, bt.CurrentMaxBytes())
	}

	// The 10th success triggers a second growth.
	bt.RecordSuccess(time.Millisecond, 1000, 500)
	expectedSecond := int(float64(afterFirst) * 1.25)
	if bt.CurrentMaxBytes() != expectedSecond {
		t.Errorf("after second streak: expected %d, got %d",
			expectedSecond, bt.CurrentMaxBytes())
	}
}

func TestBatchTuner_Is413Detection(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"generic error", fmt.Errorf("some error"), false},
		{"ExportError 500", &ExportError{
			Err: fmt.Errorf("500"), StatusCode: 500,
		}, false},
		{"ExportError 413", &ExportError{
			Err: fmt.Errorf("413"), StatusCode: http.StatusRequestEntityTooLarge,
		}, true},
		{"ExportError 400", &ExportError{
			Err: fmt.Errorf("400"), StatusCode: 400,
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := is413(tt.err); got != tt.expected {
				t.Errorf("is413(%v) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}

func TestBatchTuner_CustomConfig(t *testing.T) {
	bt := NewBatchTuner(BatchTunerConfig{
		Enabled:       true,
		MinBytes:      256,
		MaxBytes:      8192,
		SuccessStreak: 5,
		GrowFactor:    1.5,
		ShrinkFactor:  0.25,
	})

	if bt.minBytes != 256 {
		t.Errorf("expected minBytes=256, got %d", bt.minBytes)
	}
	if bt.maxBytes != 8192 {
		t.Errorf("expected maxBytes=8192, got %d", bt.maxBytes)
	}
	if bt.successStreak != 5 {
		t.Errorf("expected successStreak=5, got %d", bt.successStreak)
	}
	if bt.growFactor != 1.5 {
		t.Errorf("expected growFactor=1.5, got %f", bt.growFactor)
	}
	if bt.shrinkFactor != 0.25 {
		t.Errorf("expected shrinkFactor=0.25, got %f", bt.shrinkFactor)
	}
	if bt.CurrentMaxBytes() != 8192 {
		t.Errorf("expected initial CurrentMaxBytes=8192, got %d", bt.CurrentMaxBytes())
	}

	// 5 successes should trigger growth with factor 1.5.
	recordSuccesses(t, bt, 5)
	// 8192 is already MaxBytes, so growth is capped.
	if bt.CurrentMaxBytes() != 8192 {
		t.Errorf("expected CurrentMaxBytes capped at MaxBytes=8192, got %d", bt.CurrentMaxBytes())
	}

	// Shrink, then grow.
	bt.RecordFailure(makeGenericError())
	afterShrink := bt.CurrentMaxBytes()
	expected := int(float64(8192) * 0.25)
	if afterShrink != expected {
		t.Errorf("expected CurrentMaxBytes=%d after shrink, got %d", expected, afterShrink)
	}

	recordSuccesses(t, bt, 5)
	afterGrow := bt.CurrentMaxBytes()
	expectedGrow := int(float64(afterShrink) * 1.5)
	if afterGrow != expectedGrow {
		t.Errorf("expected CurrentMaxBytes=%d after grow, got %d", expectedGrow, afterGrow)
	}
}

func TestBatchTuner_InvalidGrowFactorUsesDefault(t *testing.T) {
	// GrowFactor <= 1.0 should be replaced with 1.25.
	bt := NewBatchTuner(BatchTunerConfig{
		Enabled:    true,
		GrowFactor: 0.9, // Invalid: must be > 1.0.
	})
	if bt.growFactor != 1.25 {
		t.Errorf("expected default growFactor=1.25 for invalid input, got %f", bt.growFactor)
	}

	bt2 := NewBatchTuner(BatchTunerConfig{
		Enabled:    true,
		GrowFactor: 1.0, // Boundary: exactly 1.0 is not useful growth.
	})
	if bt2.growFactor != 1.25 {
		t.Errorf("expected default growFactor=1.25 for boundary input 1.0, got %f", bt2.growFactor)
	}
}

func TestBatchTuner_InvalidShrinkFactorUsesDefault(t *testing.T) {
	// ShrinkFactor <= 0 or >= 1.0 should be replaced with 0.5.
	tests := []struct {
		name  string
		value float64
	}{
		{"negative", -0.5},
		{"zero", 0.0},
		{"one", 1.0},
		{"greater_than_one", 1.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bt := NewBatchTuner(BatchTunerConfig{
				Enabled:      true,
				ShrinkFactor: tt.value,
			})
			if bt.shrinkFactor != 0.5 {
				t.Errorf("expected default shrinkFactor=0.5 for input %f, got %f",
					tt.value, bt.shrinkFactor)
			}
		})
	}
}

func TestBatchTuner_GrowthStopsAtMaxWhenNoCeiling(t *testing.T) {
	bt := NewBatchTuner(BatchTunerConfig{
		Enabled:       true,
		MinBytes:      100,
		MaxBytes:      10000,
		SuccessStreak: 1,
		GrowFactor:    10.0, // Aggressive growth for fast test.
		ShrinkFactor:  0.5,
	})

	// Shrink first to create room.
	bt.RecordFailure(makeGenericError())
	bt.RecordFailure(makeGenericError())

	// Now grow aggressively.
	for i := 0; i < 50; i++ {
		bt.RecordSuccess(time.Millisecond, 1000, 500)
	}

	if bt.CurrentMaxBytes() != 10000 {
		t.Errorf("expected CurrentMaxBytes capped at MaxBytes=10000, got %d", bt.CurrentMaxBytes())
	}
}

// --- Race condition tests ---

func TestRace_BatchTuner_ConcurrentRecordAndRead(t *testing.T) {
	bt := newTestTuner(t)

	var wg sync.WaitGroup

	// Writers: RecordSuccess
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				bt.RecordSuccess(time.Millisecond, 1000, 500)
				runtime.Gosched()
			}
		}()
	}

	// Writers: RecordFailure
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if j%5 == 0 {
					bt.RecordFailure(make413Error())
				} else {
					bt.RecordFailure(makeGenericError())
				}
				runtime.Gosched()
			}
		}()
	}

	// Readers: CurrentMaxBytes
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				v := bt.CurrentMaxBytes()
				if v < bt.minBytes {
					t.Errorf("CurrentMaxBytes=%d < minBytes=%d", v, bt.minBytes)
					return
				}
				runtime.Gosched()
			}
		}()
	}

	wg.Wait()

	// Final state: CurrentMaxBytes must be within [minBytes, maxBytes].
	final := bt.CurrentMaxBytes()
	if final < bt.minBytes || final > bt.maxBytes {
		t.Errorf("final CurrentMaxBytes=%d outside [%d, %d]",
			final, bt.minBytes, bt.maxBytes)
	}
}

func TestRace_BatchTuner_ConcurrentSuccessOnly(t *testing.T) {
	bt := NewBatchTuner(BatchTunerConfig{
		Enabled:       true,
		MinBytes:      100,
		MaxBytes:      100000,
		SuccessStreak: 2,
		GrowFactor:    1.1,
		ShrinkFactor:  0.5,
	})

	// Shrink first to give room to grow.
	for i := 0; i < 5; i++ {
		bt.RecordFailure(makeGenericError())
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 300; j++ {
				bt.RecordSuccess(time.Millisecond, 1000, 500)
				runtime.Gosched()
			}
		}()
	}

	wg.Wait()

	if bt.CurrentMaxBytes() > 100000 {
		t.Errorf("CurrentMaxBytes=%d exceeds MaxBytes=100000", bt.CurrentMaxBytes())
	}
}

func TestRace_BatchTuner_ConcurrentFailureOnly(t *testing.T) {
	bt := newTestTuner(t)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				bt.RecordFailure(makeGenericError())
				runtime.Gosched()
			}
		}()
	}

	wg.Wait()

	if bt.CurrentMaxBytes() < bt.minBytes {
		t.Errorf("CurrentMaxBytes=%d fell below minBytes=%d", bt.CurrentMaxBytes(), bt.minBytes)
	}
}

// --- Memory leak test ---

func TestMemLeak_BatchTuner_NoAllocationGrowth(t *testing.T) {
	bt := newTestTuner(t)
	err := makeGenericError()

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	// Run 10000 mixed RecordSuccess/RecordFailure calls.
	for i := 0; i < 10000; i++ {
		if i%11 == 0 {
			bt.RecordFailure(err)
		} else {
			bt.RecordSuccess(time.Millisecond, 1000, 500)
		}
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("BatchTuner 10K ops: heap_before=%dKB, heap_after=%dKB",
		heapBefore/1024, heapAfter/1024)

	if heapAfter > heapBefore+10*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after 10K RecordSuccess/Failure calls",
			heapBefore/1024, heapAfter/1024)
	}
}

func TestMemLeak_BatchTuner_ManyInstances(t *testing.T) {
	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	// Create and exercise many tuner instances.
	for i := 0; i < 1000; i++ {
		bt := NewBatchTuner(BatchTunerConfig{
			Enabled:       true,
			MinBytes:      100,
			MaxBytes:      10000,
			SuccessStreak: 2,
			GrowFactor:    1.5,
			ShrinkFactor:  0.5,
		})
		for j := 0; j < 10; j++ {
			bt.RecordSuccess(time.Millisecond, 1000, 500)
		}
		bt.RecordFailure(makeGenericError())
		_ = bt.CurrentMaxBytes()
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("BatchTuner 1K instances: heap_before=%dKB, heap_after=%dKB",
		heapBefore/1024, heapAfter/1024)

	if heapAfter > heapBefore+10*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after creating 1K tuners",
			heapBefore/1024, heapAfter/1024)
	}
}
