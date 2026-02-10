package pipeline

import (
	"math"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	pipelineHealthScore = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_pipeline_health_score",
		Help: "Pipeline health score (0.0 = healthy, 1.0 = overloaded)",
	})

	pipelineHealthComponents = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "metrics_governor_pipeline_health_components",
		Help: "Individual pipeline health component scores",
	}, []string{"component"})
)

func init() {
	prometheus.MustRegister(pipelineHealthScore)
	prometheus.MustRegister(pipelineHealthComponents)

	pipelineHealthScore.Set(0)
	pipelineHealthComponents.WithLabelValues("queue").Set(0)
	pipelineHealthComponents.WithLabelValues("buffer").Set(0)
	pipelineHealthComponents.WithLabelValues("export").Set(0)
	pipelineHealthComponents.WithLabelValues("circuit").Set(0)
}

// PipelineHealth computes a unified health score from multiple pipeline signals.
// Score ranges from 0.0 (healthy) to 1.0 (overloaded). When the score exceeds
// a profile-specific threshold, receivers should reject incoming data.
type PipelineHealth struct {
	// Weights for each component (must sum to 1.0).
	queueWeight   float64
	bufferWeight  float64
	exportWeight  float64
	circuitWeight float64

	// Cached score for fast read (updated by Recompute).
	score atomic.Uint64 // float64 bits via math.Float64bits/frombits

	// Input signals (set by component owners).
	mu              sync.RWMutex
	queuePressure   float64 // 0.0-1.0 queue utilization
	bufferPressure  float64 // 0.0-1.0 buffer utilization
	exportLatencyMs float64 // EWMA export latency in ms
	circuitOpen     bool    // circuit breaker state
}

// NewPipelineHealth creates a health tracker with default weights.
func NewPipelineHealth() *PipelineHealth {
	return &PipelineHealth{
		queueWeight:   0.35,
		bufferWeight:  0.30,
		exportWeight:  0.20,
		circuitWeight: 0.15,
	}
}

// Score returns the most recently computed health score (0.0-1.0).
// This is safe for concurrent reads without locking.
func (h *PipelineHealth) Score() float64 {
	bits := h.score.Load()
	return math.Float64frombits(bits)
}

// IsOverloaded returns true if the pipeline score exceeds the given threshold.
func (h *PipelineHealth) IsOverloaded(threshold float64) bool {
	return h.Score() > threshold
}

// SetQueuePressure updates the queue utilization signal (0.0-1.0).
func (h *PipelineHealth) SetQueuePressure(pressure float64) {
	h.mu.Lock()
	h.queuePressure = clamp01(pressure)
	h.mu.Unlock()
	h.recompute()
}

// SetBufferPressure updates the buffer utilization signal (0.0-1.0).
func (h *PipelineHealth) SetBufferPressure(pressure float64) {
	h.mu.Lock()
	h.bufferPressure = clamp01(pressure)
	h.mu.Unlock()
	h.recompute()
}

// SetExportLatency updates the export latency EWMA signal (in milliseconds).
// Normalized: 0ms=0.0, 2000ms=1.0.
func (h *PipelineHealth) SetExportLatency(latencyMs float64) {
	h.mu.Lock()
	h.exportLatencyMs = latencyMs
	h.mu.Unlock()
	h.recompute()
}

// SetCircuitOpen updates the circuit breaker state.
func (h *PipelineHealth) SetCircuitOpen(open bool) {
	h.mu.Lock()
	h.circuitOpen = open
	h.mu.Unlock()
	h.recompute()
}

// recompute recalculates the health score from current inputs.
func (h *PipelineHealth) recompute() {
	h.mu.RLock()
	queueP := h.queuePressure
	bufferP := h.bufferPressure
	exportP := clamp01(h.exportLatencyMs / 2000.0) // Normalize: 2s → 1.0
	var circuitP float64
	if h.circuitOpen {
		circuitP = 1.0
	}
	h.mu.RUnlock()

	score := queueP*h.queueWeight +
		bufferP*h.bufferWeight +
		exportP*h.exportWeight +
		circuitP*h.circuitWeight

	score = clamp01(score)

	h.score.Store(math.Float64bits(score))

	// Update Prometheus metrics
	pipelineHealthScore.Set(score)
	pipelineHealthComponents.WithLabelValues("queue").Set(queueP)
	pipelineHealthComponents.WithLabelValues("buffer").Set(bufferP)
	pipelineHealthComponents.WithLabelValues("export").Set(exportP)
	pipelineHealthComponents.WithLabelValues("circuit").Set(circuitP)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
