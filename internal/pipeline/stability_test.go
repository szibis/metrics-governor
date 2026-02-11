package pipeline_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/buffer"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	"github.com/szibis/metrics-governor/internal/pipeline"
	"github.com/szibis/metrics-governor/internal/stats"
)

// --- Stability test mocks ---

// degradableStatsCollector wraps stats.Collector to track degradation events.
type degradableStatsCollector struct {
	*stats.Collector
	degradations atomic.Int64
}

func newDegradableStats(level stats.StatsLevel) *degradableStatsCollector {
	return &degradableStatsCollector{
		Collector: stats.NewCollector([]string{"service"}, level),
	}
}

func (d *degradableStatsCollector) DegradeAndTrack() bool {
	ok := d.Collector.Degrade()
	if ok {
		d.degradations.Add(1)
	}
	return ok
}

// --- Tests ---

// TestResilience_Pipeline_GracefulDegradation_FullSequence verifies the full
// degradation chain: full → basic → none, ensuring the proxy keeps functioning.
func TestResilience_Pipeline_GracefulDegradation_FullSequence(t *testing.T) {
	sc := newDegradableStats(stats.StatsLevelFull)
	exp := &countingExporter{}

	buf := buffer.New(
		1000,
		50,
		50*time.Millisecond,
		exp,
		sc.Collector,
		&noopLimitsEnforcer{},
		&noopLogAggregator{},
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Phase 1: full stats — send 20 batches
	for i := 0; i < 20; i++ {
		buf.Add([]*metricspb.ResourceMetrics{makeGaugeRM(fmt.Sprintf("full_%d", i), 10)})
	}
	time.Sleep(200 * time.Millisecond)

	if sc.Level() != stats.StatsLevelFull {
		t.Fatalf("expected full level, got %s", sc.Level())
	}
	dp1 := exp.getDatapoints()
	if dp1 == 0 {
		t.Fatal("no datapoints exported during full stats phase")
	}

	// Phase 2: degrade full → basic
	if !sc.DegradeAndTrack() {
		t.Fatal("degradation full→basic failed")
	}
	if sc.Level() != stats.StatsLevelBasic {
		t.Fatalf("expected basic, got %s", sc.Level())
	}

	// Send more batches — proxy should still work
	for i := 0; i < 20; i++ {
		buf.Add([]*metricspb.ResourceMetrics{makeGaugeRM(fmt.Sprintf("basic_%d", i), 10)})
	}
	time.Sleep(200 * time.Millisecond)

	dp2 := exp.getDatapoints()
	if dp2 <= dp1 {
		t.Errorf("no new datapoints after degradation to basic: before=%d after=%d", dp1, dp2)
	}

	// Phase 3: degrade basic → none
	if !sc.DegradeAndTrack() {
		t.Fatal("degradation basic→none failed")
	}
	if sc.Level() != stats.StatsLevelNone {
		t.Fatalf("expected none, got %s", sc.Level())
	}

	// Send more batches — proxy should still forward data
	for i := 0; i < 20; i++ {
		buf.Add([]*metricspb.ResourceMetrics{makeGaugeRM(fmt.Sprintf("none_%d", i), 10)})
	}
	time.Sleep(200 * time.Millisecond)

	dp3 := exp.getDatapoints()
	if dp3 <= dp2 {
		t.Errorf("no new datapoints after degradation to none: before=%d after=%d", dp2, dp3)
	}

	// Verify configured level is preserved
	if sc.ConfiguredLevel() != stats.StatsLevelFull {
		t.Errorf("configured level changed to %s, expected full", sc.ConfiguredLevel())
	}

	cancel()
	buf.Wait()

	// Total: 60 batches × 10 dp = 600
	if dp3 != 600 {
		t.Errorf("expected 600 total datapoints, got %d", dp3)
	}

	if sc.degradations.Load() != 2 {
		t.Errorf("expected 2 degradation events, got %d", sc.degradations.Load())
	}
}

// TestResilience_Pipeline_LoadSheddingRecovery verifies that after overloading
// the pipeline (high health score), reducing load causes shedding to stop.
func TestResilience_Pipeline_LoadSheddingRecovery(t *testing.T) {
	health := pipeline.NewPipelineHealth()

	// Simulate overload — push all components high enough to exceed 0.85
	health.SetQueuePressure(0.95)
	health.SetExportLatency(5000) // 5s in ms → normalized to 1.0
	health.SetBufferPressure(0.95)
	health.SetCircuitOpen(true)

	threshold := 0.85
	if !health.IsOverloaded(threshold) {
		t.Fatal("expected pipeline to be overloaded")
	}
	score := health.Score()
	if score < threshold {
		t.Errorf("score %f should be >= threshold %f", score, threshold)
	}

	// Simulate recovery
	health.SetQueuePressure(0.10)
	health.SetExportLatency(10) // 10ms — healthy
	health.SetBufferPressure(0.10)
	health.SetCircuitOpen(false)

	if health.IsOverloaded(threshold) {
		t.Error("pipeline should NOT be overloaded after recovery")
	}
	recovered := health.Score()
	if recovered >= threshold {
		t.Errorf("recovered score %f should be < threshold %f", recovered, threshold)
	}
}

// TestResilience_Pipeline_HealthScoreUnderLoad verifies that health score
// reflects pipeline state accurately under varied pressure levels.
func TestResilience_Pipeline_HealthScoreUnderLoad(t *testing.T) {
	health := pipeline.NewPipelineHealth()

	// Idle baseline
	health.SetQueuePressure(0.0)
	health.SetExportLatency(1) // 1ms
	health.SetBufferPressure(0.0)
	health.SetCircuitOpen(false)
	idle := health.Score()
	if idle > 0.1 {
		t.Errorf("idle score %f too high", idle)
	}

	// Moderate load
	health.SetQueuePressure(0.5)
	health.SetExportLatency(500) // 500ms
	health.SetBufferPressure(0.5)
	moderate := health.Score()
	if moderate <= idle {
		t.Errorf("moderate score %f should be > idle %f", moderate, idle)
	}

	// Heavy load
	health.SetQueuePressure(0.9)
	health.SetExportLatency(3000) // 3s
	health.SetBufferPressure(0.9)
	heavy := health.Score()
	if heavy <= moderate {
		t.Errorf("heavy score %f should be > moderate %f", heavy, moderate)
	}
}

// TestDataIntegrity_Pipeline_DegradationPreservesAccounting verifies that
// degrading stats does not cause datapoint accounting mismatches.
func TestDataIntegrity_Pipeline_DegradationPreservesAccounting(t *testing.T) {
	sc := newDegradableStats(stats.StatsLevelFull)
	exp := &countingExporter{}

	buf := buffer.New(
		5000,
		100,
		30*time.Millisecond,
		exp,
		sc.Collector,
		&noopLimitsEnforcer{},
		&noopLogAggregator{},
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	totalSent := int64(0)
	var wg sync.WaitGroup

	// 4 goroutines sending batches, with degradation happening mid-stream
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 250; i++ {
				dpCount := 10
				buf.Add([]*metricspb.ResourceMetrics{
					makeGaugeRM(fmt.Sprintf("svc_%d_%d", id, i), dpCount),
				})
				atomic.AddInt64(&totalSent, int64(dpCount))
			}
		}(g)
	}

	// Degrade mid-stream
	time.Sleep(50 * time.Millisecond)
	sc.DegradeAndTrack() // full → basic
	time.Sleep(50 * time.Millisecond)
	sc.DegradeAndTrack() // basic → none

	wg.Wait()
	time.Sleep(500 * time.Millisecond) // let buffer flush
	cancel()
	buf.Wait()

	sent := atomic.LoadInt64(&totalSent)
	exported := exp.getDatapoints()

	// All datapoints should be exported regardless of stats level changes
	if exported != sent {
		t.Errorf("datapoint mismatch: sent=%d exported=%d", sent, exported)
	}
}

// TestResilience_Pipeline_ConcurrentHealthUpdates verifies that concurrent
// health score updates from multiple pipeline components don't race.
func TestResilience_Pipeline_ConcurrentHealthUpdates(t *testing.T) {
	health := pipeline.NewPipelineHealth()
	var wg sync.WaitGroup

	// 4 goroutines updating different components concurrently
	updaters := []func(float64){
		health.SetQueuePressure,
		health.SetBufferPressure,
		func(v float64) { health.SetExportLatency(v * 1000) }, // convert to ms
		func(v float64) { health.SetCircuitOpen(v > 0.5) },
	}
	for _, fn := range updaters {
		wg.Add(1)
		go func(update func(float64)) {
			defer wg.Done()
			for i := 0; i < 10000; i++ {
				v := float64(i%100) / 100.0
				update(v)
				_ = health.Score()
				_ = health.IsOverloaded(0.85)
			}
		}(fn)
	}

	wg.Wait()

	// Should not have panicked and score should be valid
	score := health.Score()
	if score < 0 || score > 1 {
		t.Errorf("invalid score %f after concurrent updates", score)
	}
}
