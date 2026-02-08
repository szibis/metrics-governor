package exporter

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// scalerTestHarness wires up a WorkerScaler with atomic counters so tests can
// observe add/remove calls and control the simulated queue depth.
type scalerTestHarness struct {
	scaler  *WorkerScaler
	depth   atomic.Int32
	added   atomic.Int32
	removed atomic.Int32
	ctx     context.Context
	cancel  context.CancelFunc
}

func newHarness(cfg ScalerConfig, initialWorkers int) *scalerTestHarness {
	h := &scalerTestHarness{}
	h.ctx, h.cancel = context.WithCancel(context.Background())
	h.scaler = NewWorkerScaler(cfg)
	h.scaler.Start(
		h.ctx,
		initialWorkers,
		func() int { return int(h.depth.Load()) },
		func() { h.added.Add(1) },
		func() { h.removed.Add(1) },
	)
	return h
}

func (h *scalerTestHarness) stop() {
	h.scaler.Stop()
	h.cancel()
}

// ---------------------------------------------------------------------------
// 1. TestScaler_DefaultConfig
// ---------------------------------------------------------------------------

func TestScaler_DefaultConfig(t *testing.T) {
	// Passing all zeros triggers every default path.
	ws := NewWorkerScaler(ScalerConfig{})

	if ws.config.MinWorkers != 1 {
		t.Errorf("MinWorkers: got %d, want 1", ws.config.MinWorkers)
	}
	// MaxWorkers default: MinWorkers * 4 = 4
	if ws.config.MaxWorkers != 4 {
		t.Errorf("MaxWorkers: got %d, want 4", ws.config.MaxWorkers)
	}
	if ws.config.ScaleInterval != 5*time.Second {
		t.Errorf("ScaleInterval: got %v, want 5s", ws.config.ScaleInterval)
	}
	if ws.config.HighWaterMark != 100 {
		t.Errorf("HighWaterMark: got %d, want 100", ws.config.HighWaterMark)
	}
	if ws.config.LowWaterMark != 10 {
		t.Errorf("LowWaterMark: got %d, want 10", ws.config.LowWaterMark)
	}
	if ws.config.MaxLatency != 500*time.Millisecond {
		t.Errorf("MaxLatency: got %v, want 500ms", ws.config.MaxLatency)
	}
	if ws.config.SustainedIdleSecs != 30 {
		t.Errorf("SustainedIdleSecs: got %d, want 30", ws.config.SustainedIdleSecs)
	}
	if ws.ewmaAlpha != 0.3 {
		t.Errorf("ewmaAlpha: got %f, want 0.3", ws.ewmaAlpha)
	}
}

// ---------------------------------------------------------------------------
// 2. TestScaler_ScalesUpOnHighWaterMark
// ---------------------------------------------------------------------------

func TestScaler_ScalesUpOnHighWaterMark(t *testing.T) {
	cfg := ScalerConfig{
		Enabled:           true,
		MinWorkers:        1,
		MaxWorkers:        10,
		ScaleInterval:     10 * time.Millisecond,
		HighWaterMark:     50,
		LowWaterMark:      5,
		MaxLatency:        500 * time.Millisecond,
		SustainedIdleSecs: 60, // disable scale-down for this test
	}

	h := newHarness(cfg, 2)
	defer h.stop()

	// Simulate queue depth above high watermark.
	h.depth.Store(100)

	// Wait for several scaling intervals.
	time.Sleep(80 * time.Millisecond)

	added := h.added.Load()
	if added < 1 {
		t.Errorf("expected at least 1 worker added, got %d", added)
	}

	workers := h.scaler.CurrentWorkers()
	if workers <= 2 {
		t.Errorf("expected workers > 2, got %d", workers)
	}
}

// ---------------------------------------------------------------------------
// 3. TestScaler_ScalesDownOnSustainedIdle
// ---------------------------------------------------------------------------

func TestScaler_ScalesDownOnSustainedIdle(t *testing.T) {
	cfg := ScalerConfig{
		Enabled:           true,
		MinWorkers:        1,
		MaxWorkers:        20,
		ScaleInterval:     10 * time.Millisecond,
		HighWaterMark:     50,
		LowWaterMark:      5,
		MaxLatency:        500 * time.Millisecond,
		SustainedIdleSecs: 1, // 1 second sustained idle
	}

	h := newHarness(cfg, 8)
	defer h.stop()

	// Queue depth below low watermark → idle.
	h.depth.Store(0)

	// Wait for the sustained idle period plus several scaling intervals.
	time.Sleep(1200 * time.Millisecond)

	removed := h.removed.Load()
	if removed < 1 {
		t.Errorf("expected at least 1 worker removed, got %d", removed)
	}

	workers := h.scaler.CurrentWorkers()
	if workers >= 8 {
		t.Errorf("expected workers < 8 after scale-down, got %d", workers)
	}
}

// ---------------------------------------------------------------------------
// 4. TestScaler_NeverBelowMinWorkers
// ---------------------------------------------------------------------------

func TestScaler_NeverBelowMinWorkers(t *testing.T) {
	cfg := ScalerConfig{
		Enabled:           true,
		MinWorkers:        3,
		MaxWorkers:        20,
		ScaleInterval:     10 * time.Millisecond,
		HighWaterMark:     50,
		LowWaterMark:      5,
		MaxLatency:        500 * time.Millisecond,
		SustainedIdleSecs: 1,
	}

	h := newHarness(cfg, 8)
	defer h.stop()

	h.depth.Store(0)

	// Allow enough time for multiple scale-down cycles.
	time.Sleep(3500 * time.Millisecond)

	workers := h.scaler.CurrentWorkers()
	if workers < cfg.MinWorkers {
		t.Errorf("workers fell below MinWorkers: got %d, min %d", workers, cfg.MinWorkers)
	}
}

// ---------------------------------------------------------------------------
// 5. TestScaler_NeverAboveMaxWorkers
// ---------------------------------------------------------------------------

func TestScaler_NeverAboveMaxWorkers(t *testing.T) {
	cfg := ScalerConfig{
		Enabled:           true,
		MinWorkers:        1,
		MaxWorkers:        5,
		ScaleInterval:     10 * time.Millisecond,
		HighWaterMark:     10,
		LowWaterMark:      2,
		MaxLatency:        1 * time.Second,
		SustainedIdleSecs: 60,
	}

	h := newHarness(cfg, 3)
	defer h.stop()

	// Deep queue to drive continuous scale-up.
	h.depth.Store(500)

	// Wait for many scaling intervals.
	time.Sleep(200 * time.Millisecond)

	workers := h.scaler.CurrentWorkers()
	if workers > cfg.MaxWorkers {
		t.Errorf("workers exceeded MaxWorkers: got %d, max %d", workers, cfg.MaxWorkers)
	}
}

// ---------------------------------------------------------------------------
// 6. TestScaler_LatencyGateBlocksScaleUp
// ---------------------------------------------------------------------------

func TestScaler_LatencyGateBlocksScaleUp(t *testing.T) {
	cfg := ScalerConfig{
		Enabled:           true,
		MinWorkers:        1,
		MaxWorkers:        10,
		ScaleInterval:     10 * time.Millisecond,
		HighWaterMark:     20,
		LowWaterMark:      5,
		MaxLatency:        100 * time.Millisecond,
		SustainedIdleSecs: 60,
	}

	h := newHarness(cfg, 2)
	defer h.stop()

	// Record very high latency to exceed MaxLatency.
	for i := 0; i < 20; i++ {
		h.scaler.RecordLatency(2 * time.Second)
	}

	// Queue depth above high watermark.
	h.depth.Store(200)

	// Wait for several intervals.
	time.Sleep(100 * time.Millisecond)

	added := h.added.Load()
	if added != 0 {
		t.Errorf("expected 0 workers added when latency is high, got %d", added)
	}

	workers := h.scaler.CurrentWorkers()
	if workers != 2 {
		t.Errorf("expected worker count unchanged at 2, got %d", workers)
	}
}

// ---------------------------------------------------------------------------
// 7. TestScaler_DisabledFixedWorkerCount
// ---------------------------------------------------------------------------

func TestScaler_DisabledFixedWorkerCount(t *testing.T) {
	// When Enabled is false, we still create the scaler and start it;
	// the scaling loop runs but the Enabled flag is a config choice made by
	// callers — the scaler itself always runs. We verify that if we set
	// conditions that would normally trigger scaling, it still works
	// (the scaler has no concept of "disabled" internally — that is handled
	// by the caller deciding not to Start it). So this test verifies the
	// scaler with Enabled=false but still started retains its normal behavior
	// (since the code does not check Enabled internally).
	//
	// Instead, we test "disabled" by simply not calling Start.
	cfg := ScalerConfig{
		MinWorkers:        4,
		MaxWorkers:        4,
		ScaleInterval:     10 * time.Millisecond,
		HighWaterMark:     10,
		LowWaterMark:      2,
		MaxLatency:        500 * time.Millisecond,
		SustainedIdleSecs: 1,
	}

	ws := NewWorkerScaler(cfg)

	// Never call Start — simulate "disabled".
	// CurrentWorkers should remain at the zero value.
	if w := ws.CurrentWorkers(); w != 0 {
		t.Errorf("expected 0 workers when not started, got %d", w)
	}

	// Ensure Stop is safe to call even without Start.
	ws.Stop() // should be a no-op, not deadlock.
}

// ---------------------------------------------------------------------------
// 8. TestScaler_RecordLatencyEWMA
// ---------------------------------------------------------------------------

func TestScaler_RecordLatencyEWMA(t *testing.T) {
	ws := NewWorkerScaler(ScalerConfig{
		MinWorkers:    1,
		MaxWorkers:    4,
		ScaleInterval: time.Hour, // never tick
	})

	// First observation initializes directly.
	ws.RecordLatency(100 * time.Millisecond)
	got := ws.LatencyEWMA()
	want := 100 * time.Millisecond
	if got != want {
		t.Errorf("after first record: got %v, want %v", got, want)
	}

	// Second observation: EWMA = 0.3 * 200ms + 0.7 * 100ms = 60ms + 70ms = 130ms
	ws.RecordLatency(200 * time.Millisecond)
	got = ws.LatencyEWMA()
	wantNs := int64(0.3*float64(200*time.Millisecond) + 0.7*float64(100*time.Millisecond))
	if got != time.Duration(wantNs) {
		t.Errorf("after second record: got %v, want %v", got, time.Duration(wantNs))
	}

	// Third observation: EWMA = 0.3 * 50ms + 0.7 * prev
	prev := wantNs
	ws.RecordLatency(50 * time.Millisecond)
	got = ws.LatencyEWMA()
	wantNs = int64(0.3*float64(50*time.Millisecond) + 0.7*float64(prev))
	if got != time.Duration(wantNs) {
		t.Errorf("after third record: got %v, want %v", got, time.Duration(wantNs))
	}
}

// ---------------------------------------------------------------------------
// 9. TestScaler_IdleTimerResetOnActivity
// ---------------------------------------------------------------------------

func TestScaler_IdleTimerResetOnActivity(t *testing.T) {
	cfg := ScalerConfig{
		Enabled:           true,
		MinWorkers:        1,
		MaxWorkers:        20,
		ScaleInterval:     10 * time.Millisecond,
		HighWaterMark:     100,
		LowWaterMark:      5,
		MaxLatency:        500 * time.Millisecond,
		SustainedIdleSecs: 3, // 3 second sustained idle required
	}

	h := newHarness(cfg, 8)
	defer h.stop()

	// Go idle for 1.5s (well under 3s threshold).
	h.depth.Store(0)
	time.Sleep(1500 * time.Millisecond)

	// Spike above low watermark — should reset idle timer.
	h.depth.Store(50) // above LowWaterMark but below HighWaterMark
	time.Sleep(200 * time.Millisecond)

	// Go idle again for 1.5s (well under 3s threshold).
	h.depth.Store(0)
	time.Sleep(1500 * time.Millisecond)

	// Total wall time ~3.2s but neither idle window reached the 3s threshold
	// continuously, because the activity spike reset the idle timer.
	workers := h.scaler.CurrentWorkers()
	if workers < 8 {
		t.Errorf("expected workers unchanged at 8 (idle timer was reset), got %d", workers)
	}
}

// ---------------------------------------------------------------------------
// 10. TestScaler_StartIsIdempotent
// ---------------------------------------------------------------------------

func TestScaler_StartIsIdempotent(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	cfg := ScalerConfig{
		Enabled:           true,
		MinWorkers:        1,
		MaxWorkers:        10,
		ScaleInterval:     50 * time.Millisecond,
		HighWaterMark:     100,
		LowWaterMark:      5,
		MaxLatency:        500 * time.Millisecond,
		SustainedIdleSecs: 60,
	}

	ws := NewWorkerScaler(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var depth atomic.Int32
	queueDepthFn := func() int { return int(depth.Load()) }
	addWorker := func() {}
	removeWorker := func() {}

	// Call Start twice — second call should be a no-op.
	ws.Start(ctx, 4, queueDepthFn, addWorker, removeWorker)
	ws.Start(ctx, 8, queueDepthFn, addWorker, removeWorker)

	// Worker count should be from the first Start call.
	if w := ws.CurrentWorkers(); w != 4 {
		t.Errorf("expected 4 workers from first Start, got %d", w)
	}

	ws.Stop()
}

// ---------------------------------------------------------------------------
// 11. TestRace_Scaler_RecordLatencyDuringScale
// ---------------------------------------------------------------------------

func TestRace_Scaler_RecordLatencyDuringScale(t *testing.T) {
	cfg := ScalerConfig{
		Enabled:           true,
		MinWorkers:        1,
		MaxWorkers:        50,
		ScaleInterval:     5 * time.Millisecond,
		HighWaterMark:     10,
		LowWaterMark:      2,
		MaxLatency:        1 * time.Second,
		SustainedIdleSecs: 60,
	}

	h := newHarness(cfg, 5)
	defer h.stop()

	// Drive scale-up by keeping queue deep.
	h.depth.Store(500)

	var wg sync.WaitGroup
	// 15 goroutines concurrently recording latency while scaler is active.
	for i := 0; i < 15; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				h.scaler.RecordLatency(time.Duration(id+1) * time.Millisecond)
				_ = h.scaler.LatencyEWMA()
				_ = h.scaler.CurrentWorkers()
				runtime.Gosched()
			}
		}(i)
	}

	wg.Wait()

	// No panic = success. Verify EWMA is positive.
	if h.scaler.LatencyEWMA() <= 0 {
		t.Errorf("expected positive EWMA after recording, got %v", h.scaler.LatencyEWMA())
	}
}

// ---------------------------------------------------------------------------
// 12. TestRace_Scaler_ConcurrentRecordLatency
// ---------------------------------------------------------------------------

func TestRace_Scaler_ConcurrentRecordLatency(t *testing.T) {
	ws := NewWorkerScaler(ScalerConfig{
		MinWorkers:    1,
		MaxWorkers:    4,
		ScaleInterval: time.Hour, // no ticking
	})

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				ws.RecordLatency(time.Duration(id*10+j) * time.Microsecond)
				_ = ws.LatencyEWMA()
				runtime.Gosched()
			}
		}(i)
	}

	wg.Wait()

	// EWMA should be positive after many observations.
	if ws.LatencyEWMA() <= 0 {
		t.Errorf("expected positive EWMA, got %v", ws.LatencyEWMA())
	}
}

// ---------------------------------------------------------------------------
// 13. TestLeakCheck_Scaler_NoGoroutineLeaksAfterStop
// ---------------------------------------------------------------------------

func TestLeakCheck_Scaler_NoGoroutineLeaksAfterStop(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	cfg := ScalerConfig{
		Enabled:           true,
		MinWorkers:        1,
		MaxWorkers:        10,
		ScaleInterval:     10 * time.Millisecond,
		HighWaterMark:     50,
		LowWaterMark:      5,
		MaxLatency:        500 * time.Millisecond,
		SustainedIdleSecs: 60,
	}

	ws := NewWorkerScaler(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var depth atomic.Int32
	depth.Store(100)

	ws.Start(ctx, 2, func() int { return int(depth.Load()) }, func() {}, func() {})

	// Let a few ticks run.
	time.Sleep(50 * time.Millisecond)

	ws.Stop()
	// goleak.VerifyNone checks that no goroutines are leaked.
}

// ---------------------------------------------------------------------------
// 14. TestLeakCheck_Scaler_ContextCancel
// ---------------------------------------------------------------------------

func TestLeakCheck_Scaler_ContextCancel(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	cfg := ScalerConfig{
		Enabled:           true,
		MinWorkers:        1,
		MaxWorkers:        10,
		ScaleInterval:     10 * time.Millisecond,
		HighWaterMark:     50,
		LowWaterMark:      5,
		MaxLatency:        500 * time.Millisecond,
		SustainedIdleSecs: 60,
	}

	ws := NewWorkerScaler(cfg)
	ctx, cancel := context.WithCancel(context.Background())

	var depth atomic.Int32

	ws.Start(ctx, 2, func() int { return int(depth.Load()) }, func() {}, func() {})

	// Let a few ticks run.
	time.Sleep(50 * time.Millisecond)

	// Cancel context instead of calling Stop — the scaling loop should exit.
	cancel()

	// Wait for the goroutine to finish via doneCh.
	// We read doneCh with a timeout to avoid hanging.
	select {
	case <-ws.doneCh:
		// Success.
	case <-time.After(2 * time.Second):
		t.Fatal("scaling loop did not exit after context cancel within 2s")
	}
	// goleak.VerifyNone checks no goroutines leaked.
}

// ---------------------------------------------------------------------------
// Additional edge-case tests
// ---------------------------------------------------------------------------

// TestScaler_ScaleDownHalvesCorrectly verifies the halving math precisely.
func TestScaler_ScaleDownHalvesCorrectly(t *testing.T) {
	cfg := ScalerConfig{
		Enabled:           true,
		MinWorkers:        1,
		MaxWorkers:        20,
		ScaleInterval:     10 * time.Millisecond,
		HighWaterMark:     50,
		LowWaterMark:      5,
		MaxLatency:        500 * time.Millisecond,
		SustainedIdleSecs: 1,
	}

	h := newHarness(cfg, 16)
	defer h.stop()

	h.depth.Store(0)

	// Wait for first scale-down (16 -> 8).
	time.Sleep(1200 * time.Millisecond)

	workers := h.scaler.CurrentWorkers()
	if workers != 8 {
		t.Errorf("after first scale-down: expected 8 workers, got %d", workers)
	}
}

// TestScaler_ZeroLatencyAllowsScaleUp verifies that a zero latency (no
// observations yet) does not block scale-up. The code treats latency == 0
// as acceptable.
func TestScaler_ZeroLatencyAllowsScaleUp(t *testing.T) {
	cfg := ScalerConfig{
		Enabled:           true,
		MinWorkers:        1,
		MaxWorkers:        10,
		ScaleInterval:     10 * time.Millisecond,
		HighWaterMark:     20,
		LowWaterMark:      5,
		MaxLatency:        100 * time.Millisecond,
		SustainedIdleSecs: 60,
	}

	h := newHarness(cfg, 2)
	defer h.stop()

	// Never record any latency — EWMA stays at 0.
	h.depth.Store(200)

	time.Sleep(80 * time.Millisecond)

	added := h.added.Load()
	if added < 1 {
		t.Errorf("expected scale-up with zero latency, got %d adds", added)
	}
}

// TestScaler_CustomConfigPreserved verifies that explicitly set config
// values are not overwritten by defaults.
func TestScaler_CustomConfigPreserved(t *testing.T) {
	cfg := ScalerConfig{
		Enabled:           true,
		MinWorkers:        3,
		MaxWorkers:        12,
		ScaleInterval:     2 * time.Second,
		HighWaterMark:     200,
		LowWaterMark:      20,
		MaxLatency:        1 * time.Second,
		SustainedIdleSecs: 60,
	}

	ws := NewWorkerScaler(cfg)

	if ws.config.MinWorkers != 3 {
		t.Errorf("MinWorkers: got %d, want 3", ws.config.MinWorkers)
	}
	if ws.config.MaxWorkers != 12 {
		t.Errorf("MaxWorkers: got %d, want 12", ws.config.MaxWorkers)
	}
	if ws.config.ScaleInterval != 2*time.Second {
		t.Errorf("ScaleInterval: got %v, want 2s", ws.config.ScaleInterval)
	}
	if ws.config.HighWaterMark != 200 {
		t.Errorf("HighWaterMark: got %d, want 200", ws.config.HighWaterMark)
	}
	if ws.config.LowWaterMark != 20 {
		t.Errorf("LowWaterMark: got %d, want 20", ws.config.LowWaterMark)
	}
	if ws.config.MaxLatency != 1*time.Second {
		t.Errorf("MaxLatency: got %v, want 1s", ws.config.MaxLatency)
	}
	if ws.config.SustainedIdleSecs != 60 {
		t.Errorf("SustainedIdleSecs: got %d, want 60", ws.config.SustainedIdleSecs)
	}
}

// Ensure runtime is referenced (used in race tests above).
var _ = runtime.NumCPU
