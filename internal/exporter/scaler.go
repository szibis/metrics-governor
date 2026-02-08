package exporter

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/szibis/metrics-governor/internal/logging"
	"github.com/szibis/metrics-governor/internal/queue"
)

// ScalerConfig holds configuration for adaptive worker scaling.
type ScalerConfig struct {
	// Enabled activates adaptive worker scaling.
	Enabled bool
	// MinWorkers is the minimum worker count (default: 1).
	MinWorkers int
	// MaxWorkers is the maximum worker count (default: NumCPU*4).
	MaxWorkers int
	// ScaleInterval is the interval between scaling decisions (default: 5s).
	ScaleInterval time.Duration
	// HighWaterMark is the queue depth that triggers scale-up (default: 100).
	HighWaterMark int
	// LowWaterMark is the queue depth for idle detection (default: 10).
	LowWaterMark int
	// MaxLatency is the max export latency before suppressing scale-up (default: 500ms).
	MaxLatency time.Duration
	// SustainedIdleSecs is the idle duration before scale-down (default: 30).
	SustainedIdleSecs int
}

// WorkerScaler implements AIMD-based adaptive worker scaling.
// It monitors queue depth and export latency to scale workers up or down.
type WorkerScaler struct {
	config ScalerConfig

	// Latency tracking (EWMA)
	latencyEWMA atomic.Int64 // stored as nanoseconds
	ewmaAlpha   float64      // smoothing factor (default: 0.3)

	// Idle tracking
	idleStart    atomic.Int64 // Unix timestamp when idle started, 0 = not idle
	currentCount atomic.Int32

	// Control
	mu      sync.Mutex
	started bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

// NewWorkerScaler creates a new adaptive worker scaler.
func NewWorkerScaler(cfg ScalerConfig) *WorkerScaler {
	if cfg.MinWorkers <= 0 {
		cfg.MinWorkers = 1
	}
	if cfg.MaxWorkers <= 0 {
		cfg.MaxWorkers = cfg.MinWorkers * 4
	}
	if cfg.ScaleInterval <= 0 {
		cfg.ScaleInterval = 5 * time.Second
	}
	if cfg.HighWaterMark <= 0 {
		cfg.HighWaterMark = 100
	}
	if cfg.LowWaterMark <= 0 {
		cfg.LowWaterMark = 10
	}
	if cfg.MaxLatency <= 0 {
		cfg.MaxLatency = 500 * time.Millisecond
	}
	if cfg.SustainedIdleSecs <= 0 {
		cfg.SustainedIdleSecs = 30
	}

	ws := &WorkerScaler{
		config:    cfg,
		ewmaAlpha: 0.3,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}

	return ws
}

// Start begins the scaling loop. queueDepthFn returns current queue depth,
// addWorker spawns a new worker, removeWorker signals one to exit.
func (ws *WorkerScaler) Start(ctx context.Context, initialWorkers int, queueDepthFn func() int, addWorker func(), removeWorker func()) {
	ws.mu.Lock()
	if ws.started {
		ws.mu.Unlock()
		return
	}
	ws.started = true
	ws.mu.Unlock()

	ws.currentCount.Store(int32(initialWorkers))
	queue.SetWorkersDesired(float64(initialWorkers))

	go ws.scalingLoop(ctx, queueDepthFn, addWorker, removeWorker)
}

// Stop terminates the scaling loop.
func (ws *WorkerScaler) Stop() {
	ws.mu.Lock()
	if !ws.started {
		ws.mu.Unlock()
		return
	}
	ws.mu.Unlock()

	close(ws.stopCh)
	<-ws.doneCh
}

// RecordLatency records an export latency observation for EWMA.
func (ws *WorkerScaler) RecordLatency(d time.Duration) {
	ns := d.Nanoseconds()
	for {
		old := ws.latencyEWMA.Load()
		if old == 0 {
			// First observation — initialize directly
			if ws.latencyEWMA.CompareAndSwap(0, ns) {
				break
			}
			continue
		}
		// EWMA: new = alpha * observation + (1 - alpha) * old
		newVal := int64(ws.ewmaAlpha*float64(ns) + (1-ws.ewmaAlpha)*float64(old))
		if ws.latencyEWMA.CompareAndSwap(old, newVal) {
			break
		}
	}
	queue.SetExportLatencyEWMA(time.Duration(ws.latencyEWMA.Load()).Seconds())
}

// scalingLoop runs the periodic scaling decision.
func (ws *WorkerScaler) scalingLoop(ctx context.Context, queueDepthFn func() int, addWorker func(), removeWorker func()) {
	defer close(ws.doneCh)

	ticker := time.NewTicker(ws.config.ScaleInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ws.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			ws.decide(queueDepthFn, addWorker, removeWorker)
		}
	}
}

// decide makes a single scaling decision.
func (ws *WorkerScaler) decide(queueDepthFn func() int, addWorker func(), removeWorker func()) {
	depth := queueDepthFn()
	current := int(ws.currentCount.Load())
	latency := time.Duration(ws.latencyEWMA.Load())

	// Scale up: queue depth high AND latency acceptable
	if depth > ws.config.HighWaterMark && current < ws.config.MaxWorkers {
		if latency <= ws.config.MaxLatency || latency == 0 {
			// Add one worker (additive increase)
			addWorker()
			newCount := ws.currentCount.Add(1)
			ws.idleStart.Store(0) // Reset idle
			queue.SetWorkersDesired(float64(newCount))
			queue.IncrementScalerAdjustment("up")
			logging.Info("scaler: added worker", logging.F(
				"workers", newCount,
				"queue_depth", depth,
				"latency_ms", latency.Milliseconds(),
			))
			return
		}
	}

	// Idle tracking
	if depth < ws.config.LowWaterMark {
		idleStart := ws.idleStart.Load()
		if idleStart == 0 {
			// Start idle timer
			ws.idleStart.Store(time.Now().Unix())
		} else {
			// Check if we've been idle long enough
			idleDuration := time.Now().Unix() - idleStart
			if idleDuration >= int64(ws.config.SustainedIdleSecs) && current > ws.config.MinWorkers {
				// Scale down: halve workers (multiplicative decrease), floor at min
				target := current / 2
				if target < ws.config.MinWorkers {
					target = ws.config.MinWorkers
				}
				toRemove := current - target
				for i := 0; i < toRemove; i++ {
					removeWorker()
				}
				ws.currentCount.Store(int32(target))
				ws.idleStart.Store(0) // Reset idle timer after scale-down
				queue.SetWorkersDesired(float64(target))
				queue.IncrementScalerAdjustment("down")
				logging.Info("scaler: removed workers (sustained idle)", logging.F(
					"workers", target,
					"removed", toRemove,
					"queue_depth", depth,
					"idle_seconds", idleDuration,
				))
			}
		}
	} else {
		// Not idle — reset timer
		ws.idleStart.Store(0)
	}
}

// CurrentWorkers returns the current worker count.
func (ws *WorkerScaler) CurrentWorkers() int {
	return int(ws.currentCount.Load())
}

// LatencyEWMA returns the current EWMA latency.
func (ws *WorkerScaler) LatencyEWMA() time.Duration {
	return time.Duration(ws.latencyEWMA.Load())
}
