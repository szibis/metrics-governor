package pipeline

import (
	"math"
	"sync"
	"testing"
)

func TestPipelineHealth_AllHealthy(t *testing.T) {
	h := NewPipelineHealth()
	// Default state: everything zero → score ~0.0
	if s := h.Score(); s != 0.0 {
		t.Errorf("expected score 0.0, got %f", s)
	}
	if h.IsOverloaded(0.5) {
		t.Error("should not be overloaded at score 0.0")
	}
}

func TestPipelineHealth_QueuePressure(t *testing.T) {
	h := NewPipelineHealth()
	h.SetQueuePressure(0.9)
	// Queue weight is 0.35, so 0.9 * 0.35 = 0.315
	s := h.Score()
	if s < 0.30 || s > 0.35 {
		t.Errorf("expected score ~0.315 with 90%% queue pressure, got %f", s)
	}
}

func TestPipelineHealth_ExportLatencySlow(t *testing.T) {
	h := NewPipelineHealth()
	h.SetExportLatency(2000) // 2s → normalized to 1.0
	// Export weight is 0.20, so 1.0 * 0.20 = 0.20
	s := h.Score()
	if s < 0.18 || s > 0.22 {
		t.Errorf("expected score ~0.20 with 2s latency, got %f", s)
	}
}

func TestPipelineHealth_CircuitOpen(t *testing.T) {
	h := NewPipelineHealth()
	h.SetCircuitOpen(true)
	// Circuit weight is 0.15, so 1.0 * 0.15 = 0.15
	s := h.Score()
	if s < 0.13 || s > 0.17 {
		t.Errorf("expected score ~0.15 with circuit open, got %f", s)
	}
}

func TestPipelineHealth_BufferPressure(t *testing.T) {
	h := NewPipelineHealth()
	h.SetBufferPressure(0.8)
	// Buffer weight is 0.30, so 0.8 * 0.30 = 0.24
	s := h.Score()
	if s < 0.22 || s > 0.26 {
		t.Errorf("expected score ~0.24 with 80%% buffer pressure, got %f", s)
	}
}

func TestPipelineHealth_CombinedPressure(t *testing.T) {
	h := NewPipelineHealth()
	h.SetQueuePressure(0.5)  // 0.5 * 0.35 = 0.175
	h.SetBufferPressure(0.6) // 0.6 * 0.30 = 0.18
	h.SetExportLatency(500)  // 0.25 * 0.20 = 0.05
	// Total: 0.175 + 0.18 + 0.05 = 0.405
	s := h.Score()
	if s < 0.38 || s > 0.43 {
		t.Errorf("expected score ~0.405 with combined pressure, got %f", s)
	}
}

func TestPipelineHealth_AllOverloaded(t *testing.T) {
	h := NewPipelineHealth()
	h.SetQueuePressure(1.0)
	h.SetBufferPressure(1.0)
	h.SetExportLatency(5000) // well above 2000ms normalization
	h.SetCircuitOpen(true)
	// All components at 1.0, weights sum to 1.0 → score = 1.0
	s := h.Score()
	if math.Abs(s-1.0) > 0.01 {
		t.Errorf("expected score ~1.0 when all components overloaded, got %f", s)
	}
	if !h.IsOverloaded(0.5) {
		t.Error("should be overloaded at score 1.0 with threshold 0.5")
	}
}

func TestPipelineHealth_IsOverloaded_Threshold(t *testing.T) {
	h := NewPipelineHealth()
	h.SetQueuePressure(0.5)  // 0.175
	h.SetBufferPressure(0.5) // 0.15
	// Score ~ 0.325

	if h.IsOverloaded(0.5) {
		t.Error("should not be overloaded below threshold 0.5")
	}
	if !h.IsOverloaded(0.3) {
		t.Error("should be overloaded above threshold 0.3")
	}
}

func TestPipelineHealth_ScoreMonotonic(t *testing.T) {
	// Verify: worse inputs always produce higher (worse) scores
	h := NewPipelineHealth()

	var lastScore float64
	for pressure := 0.0; pressure <= 1.0; pressure += 0.1 {
		h.SetQueuePressure(pressure)
		s := h.Score()
		if s < lastScore-0.001 { // small epsilon for float rounding
			t.Errorf("score decreased from %f to %f at pressure %f", lastScore, s, pressure)
		}
		lastScore = s
	}
}

func TestPipelineHealth_ClampInput(t *testing.T) {
	h := NewPipelineHealth()

	// Values above 1.0 should be clamped
	h.SetQueuePressure(2.0)
	h.SetBufferPressure(5.0)
	s := h.Score()
	// Clamped to 1.0: queue=0.35, buffer=0.30 → 0.65
	if s < 0.60 || s > 0.70 {
		t.Errorf("expected clamped score ~0.65, got %f", s)
	}

	// Negative values should be clamped to 0
	h.SetQueuePressure(-1.0)
	h.SetBufferPressure(-1.0)
	s = h.Score()
	if s != 0.0 {
		t.Errorf("expected score 0.0 with negative inputs, got %f", s)
	}
}

func TestPipelineHealth_ExportLatencyNormalization(t *testing.T) {
	h := NewPipelineHealth()

	// 0ms → 0.0
	h.SetExportLatency(0)
	if s := h.Score(); s != 0.0 {
		t.Errorf("expected 0.0 at 0ms latency, got %f", s)
	}

	// 1000ms → 0.5 normalized → 0.5 * 0.20 = 0.10
	h.SetExportLatency(1000)
	s := h.Score()
	if s < 0.08 || s > 0.12 {
		t.Errorf("expected ~0.10 at 1000ms latency, got %f", s)
	}

	// 4000ms → clamped to 1.0 → 0.20
	h.SetExportLatency(4000)
	s = h.Score()
	if s < 0.18 || s > 0.22 {
		t.Errorf("expected ~0.20 at 4000ms (capped) latency, got %f", s)
	}
}

func TestPipelineHealth_Concurrent(t *testing.T) {
	h := NewPipelineHealth()
	var wg sync.WaitGroup

	// 8 goroutines reading health while components update concurrently
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				pressure := float64(j) / 1000.0
				switch id {
				case 0:
					h.SetQueuePressure(pressure)
				case 1:
					h.SetBufferPressure(pressure)
				case 2:
					h.SetExportLatency(pressure * 2000)
				case 3:
					h.SetCircuitOpen(j%2 == 0)
				}
			}
		}(i)
	}

	// 4 readers
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				s := h.Score()
				if s < 0 || s > 1.0 {
					t.Errorf("score out of range: %f", s)
				}
				h.IsOverloaded(0.5)
			}
		}()
	}

	wg.Wait()
}
