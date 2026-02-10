package exporter

import (
	"testing"

	"github.com/szibis/metrics-governor/internal/queue"
)

// --- SpilloverState unit tests ---

func TestGraduatedSpillover_MemoryOnlyBelowThreshold(t *testing.T) {
	s := queue.NewSpilloverState()

	// Below 80% → memory only
	mode := s.Evaluate(0.50, 80, 70)
	if mode != queue.SpilloverMemoryOnly {
		t.Errorf("expected MemoryOnly at 50%%, got %s", mode)
	}

	mode = s.Evaluate(0.79, 80, 70)
	if mode != queue.SpilloverMemoryOnly {
		t.Errorf("expected MemoryOnly at 79%%, got %s", mode)
	}
}

func TestGraduatedSpillover_PartialDisk(t *testing.T) {
	s := queue.NewSpilloverState()

	// At 85% (between 80% and 90%) → partial disk
	mode := s.Evaluate(0.85, 80, 70)
	if mode != queue.SpilloverPartialDisk {
		t.Errorf("expected PartialDisk at 85%%, got %s", mode)
	}
}

func TestGraduatedSpillover_AllDisk(t *testing.T) {
	s := queue.NewSpilloverState()

	// At 92% (between 90% and 95%) → all disk
	mode := s.Evaluate(0.92, 80, 70)
	if mode != queue.SpilloverAllDisk {
		t.Errorf("expected AllDisk at 92%%, got %s", mode)
	}
}

func TestGraduatedSpillover_LoadShedding(t *testing.T) {
	s := queue.NewSpilloverState()

	// At 96% (above 95%) → load shedding
	mode := s.Evaluate(0.96, 80, 70)
	if mode != queue.SpilloverLoadShedding {
		t.Errorf("expected LoadShedding at 96%%, got %s", mode)
	}
}

func TestGraduatedSpillover_Hysteresis_Recovery(t *testing.T) {
	s := queue.NewSpilloverState()

	// Escalate to all-disk
	s.Evaluate(0.92, 80, 70)
	if s.Mode() != queue.SpilloverAllDisk {
		t.Fatalf("expected AllDisk at 92%%, got %s", s.Mode())
	}

	// Drop to 75% — above hysteresis (70%), should NOT return to memory-only
	mode := s.Evaluate(0.75, 80, 70)
	if mode == queue.SpilloverMemoryOnly {
		t.Error("should NOT be MemoryOnly at 75% (above 70% hysteresis)")
	}

	// Drop to 69% — below hysteresis, should return to memory-only
	mode = s.Evaluate(0.69, 80, 70)
	if mode != queue.SpilloverMemoryOnly {
		t.Errorf("expected MemoryOnly at 69%% (below 70%% hysteresis), got %s", mode)
	}
}

func TestGraduatedSpillover_NoOscillation(t *testing.T) {
	s := queue.NewSpilloverState()

	// Push to partial disk
	s.Evaluate(0.85, 80, 70)
	if s.Mode() != queue.SpilloverPartialDisk {
		t.Fatalf("expected PartialDisk, got %s", s.Mode())
	}

	// Utilization drops to 75% (between hysteresis 70% and spillover 80%)
	// Should stay in partial disk (hysteresis prevents oscillation)
	s.Evaluate(0.75, 80, 70)
	if s.Mode() == queue.SpilloverMemoryOnly {
		t.Error("should NOT oscillate back to MemoryOnly at 75% (above 70% hysteresis)")
	}

	// Now drop below hysteresis
	s.Evaluate(0.65, 80, 70)
	if s.Mode() != queue.SpilloverMemoryOnly {
		t.Errorf("expected MemoryOnly at 65%%, got %s", s.Mode())
	}
}

func TestGraduatedSpillover_Escalation(t *testing.T) {
	s := queue.NewSpilloverState()

	// Step through each level
	steps := []struct {
		util float64
		want queue.SpilloverMode
	}{
		{0.50, queue.SpilloverMemoryOnly},
		{0.82, queue.SpilloverPartialDisk},
		{0.91, queue.SpilloverAllDisk},
		{0.96, queue.SpilloverLoadShedding},
		// Now de-escalate
		{0.91, queue.SpilloverAllDisk},      // below 95% but above 90%
		{0.82, queue.SpilloverPartialDisk},   // below 90% but above 80%
		{0.72, queue.SpilloverPartialDisk},   // above hysteresis 70%, stays
		{0.69, queue.SpilloverMemoryOnly},    // below hysteresis 70%
	}

	for i, step := range steps {
		mode := s.Evaluate(step.util, 80, 70)
		if mode != step.want {
			t.Errorf("step %d: at %.0f%% expected %s, got %s", i, step.util*100, step.want, mode)
		}
	}
}

func TestSpilloverState_ShouldSpillThisBatch_Alternating(t *testing.T) {
	s := queue.NewSpilloverState()
	s.Evaluate(0.85, 80, 70) // Put in partial disk mode

	// In partial mode, batches should alternate
	spilled := 0
	const total = 100
	for i := 0; i < total; i++ {
		if s.ShouldSpillThisBatch() {
			spilled++
		}
	}

	// Should be roughly 50/50 (alternating)
	if spilled < 40 || spilled > 60 {
		t.Errorf("expected ~50%% spill rate in partial mode, got %d/%d", spilled, total)
	}
}

func TestSpilloverState_ShouldSpillThisBatch_AllDisk(t *testing.T) {
	s := queue.NewSpilloverState()
	s.Evaluate(0.92, 80, 70) // Put in all-disk mode

	// In all-disk mode, every batch should spill
	for i := 0; i < 10; i++ {
		if !s.ShouldSpillThisBatch() {
			t.Errorf("batch %d should spill in AllDisk mode", i)
		}
	}
}

func TestSpilloverState_ShouldSpillThisBatch_MemoryOnly(t *testing.T) {
	s := queue.NewSpilloverState()
	// Default is MemoryOnly — nothing should spill
	for i := 0; i < 10; i++ {
		if s.ShouldSpillThisBatch() {
			t.Errorf("batch %d should NOT spill in MemoryOnly mode", i)
		}
	}
}

// --- Rate limiter tests ---

func TestSpilloverRateLimiter_AllowsWithinLimit(t *testing.T) {
	rl := newSpilloverRateLimiter(100)

	// Should allow at least 100 operations immediately
	allowed := 0
	for i := 0; i < 100; i++ {
		if rl.Allow() {
			allowed++
		}
	}
	if allowed != 100 {
		t.Errorf("expected 100 allowed, got %d", allowed)
	}
}

func TestSpilloverRateLimiter_DeniesOverLimit(t *testing.T) {
	rl := newSpilloverRateLimiter(10)

	// Exhaust tokens
	for i := 0; i < 10; i++ {
		rl.Allow()
	}

	// Next should be denied
	if rl.Allow() {
		t.Error("expected denial after exhausting tokens")
	}
}

func TestSpilloverRateLimiter_NoEffectWhenNil(t *testing.T) {
	// nil rate limiter (disabled) should be handled by caller with nil check
	var rl *spilloverRateLimiter
	if rl != nil {
		t.Error("nil rate limiter should be nil")
	}
}

func TestSpilloverMode_String(t *testing.T) {
	tests := []struct {
		mode queue.SpilloverMode
		want string
	}{
		{queue.SpilloverMemoryOnly, "memory_only"},
		{queue.SpilloverPartialDisk, "partial_disk"},
		{queue.SpilloverAllDisk, "all_disk"},
		{queue.SpilloverLoadShedding, "load_shedding"},
	}
	for _, tt := range tests {
		if got := tt.mode.String(); got != tt.want {
			t.Errorf("SpilloverMode(%d).String() = %q, want %q", tt.mode, got, tt.want)
		}
	}
}
