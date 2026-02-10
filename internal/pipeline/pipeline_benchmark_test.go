package pipeline_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/buffer"
	"github.com/szibis/metrics-governor/internal/pipeline"
	"github.com/szibis/metrics-governor/internal/stats"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// --- Pipeline Health Benchmarks ---

// BenchmarkPipelineHealth_Score measures the read path for health score.
// This is on the hot path of every incoming request (receivers check before accepting).
func BenchmarkPipelineHealth_Score(b *testing.B) {
	h := pipeline.NewPipelineHealth()
	h.SetQueuePressure(0.5)
	h.SetBufferPressure(0.3)
	h.SetExportLatency(100)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = h.Score()
	}
}

// BenchmarkPipelineHealth_IsOverloaded measures the admission check on the hot path.
func BenchmarkPipelineHealth_IsOverloaded(b *testing.B) {
	h := pipeline.NewPipelineHealth()
	h.SetQueuePressure(0.5)
	h.SetBufferPressure(0.3)
	h.SetExportLatency(100)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = h.IsOverloaded(0.85)
	}
}

// BenchmarkPipelineHealth_SetQueuePressure measures the write path (lock + recompute).
func BenchmarkPipelineHealth_SetQueuePressure(b *testing.B) {
	h := pipeline.NewPipelineHealth()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.SetQueuePressure(float64(i%100) / 100.0)
	}
}

// BenchmarkPipelineHealth_ConcurrentReadWrite measures contention between
// readers (receiver admission checks) and writers (component updates).
func BenchmarkPipelineHealth_ConcurrentReadWrite(b *testing.B) {
	h := pipeline.NewPipelineHealth()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%4 == 0 {
				h.SetQueuePressure(float64(i%100) / 100.0)
			} else {
				_ = h.IsOverloaded(0.85)
			}
			i++
		}
	})
}

// BenchmarkPipelineHealth_ConcurrentAllWriters measures worst-case write contention
// when all 4 components update simultaneously.
func BenchmarkPipelineHealth_ConcurrentAllWriters(b *testing.B) {
	h := pipeline.NewPipelineHealth()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			v := float64(i%100) / 100.0
			switch i % 4 {
			case 0:
				h.SetQueuePressure(v)
			case 1:
				h.SetBufferPressure(v)
			case 2:
				h.SetExportLatency(v * 2000)
			case 3:
				h.SetCircuitOpen(i%2 == 0)
			}
			i++
		}
	})
}

// --- Buffer-to-Export Handoff Benchmarks ---

// BenchmarkBufferToExport_Throughput measures the full buffer → export path
// including stats processing, at various datapoint rates.
func BenchmarkBufferToExport_Throughput(b *testing.B) {
	sizes := []struct {
		name      string
		dpCount   int
		batchSize int
	}{
		{"small_10dp", 10, 50},
		{"medium_100dp", 100, 200},
		{"large_1000dp", 1000, 500},
	}

	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			exp := &countingExporter{}
			buf := buffer.New(
				50000,
				sz.batchSize,
				10*time.Millisecond,
				exp,
				&noopStatsCollector{},
				&noopLimitsEnforcer{},
				&noopLogAggregator{},
			)

			ctx, cancel := context.WithCancel(context.Background())
			go buf.Start(ctx)

			metrics := []*metricspb.ResourceMetrics{makeGaugeRM("bench", sz.dpCount)}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf.Add(metrics)
			}

			cancel()
			buf.Wait()
		})
	}
}

// BenchmarkBufferToExport_WithStats measures the overhead of full stats processing
// in the buffer flush path.
func BenchmarkBufferToExport_WithStats(b *testing.B) {
	for _, level := range []struct {
		name  string
		level stats.StatsLevel
	}{
		{"stats_none", stats.StatsLevelNone},
		{"stats_basic", stats.StatsLevelBasic},
		{"stats_full", stats.StatsLevelFull},
	} {
		b.Run(level.name, func(b *testing.B) {
			sc := stats.NewCollector([]string{"service"}, level.level)
			exp := &countingExporter{}
			buf := buffer.New(
				50000,
				200,
				10*time.Millisecond,
				exp,
				sc,
				&noopLimitsEnforcer{},
				&noopLogAggregator{},
			)

			ctx, cancel := context.WithCancel(context.Background())
			go buf.Start(ctx)

			metrics := []*metricspb.ResourceMetrics{makeGaugeRM("bench", 100)}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf.Add(metrics)
			}

			cancel()
			buf.Wait()
		})
	}
}

// BenchmarkBufferToExport_ConcurrentAdd measures concurrent buffer.Add throughput
// simulating multiple receiver goroutines feeding the pipeline.
func BenchmarkBufferToExport_ConcurrentAdd(b *testing.B) {
	goroutines := []int{1, 4, 8, 16}

	for _, g := range goroutines {
		b.Run(fmt.Sprintf("goroutines_%d", g), func(b *testing.B) {
			exp := &countingExporter{}
			buf := buffer.New(
				100000,
				500,
				10*time.Millisecond,
				exp,
				&noopStatsCollector{},
				&noopLimitsEnforcer{},
				&noopLogAggregator{},
			)

			ctx, cancel := context.WithCancel(context.Background())
			go buf.Start(ctx)

			metrics := []*metricspb.ResourceMetrics{makeGaugeRM("bench", 50)}

			b.ReportAllocs()
			b.ResetTimer()

			var wg sync.WaitGroup
			perGoroutine := b.N / g
			for gi := 0; gi < g; gi++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; i < perGoroutine; i++ {
						buf.Add(metrics)
					}
				}()
			}
			wg.Wait()

			cancel()
			buf.Wait()
		})
	}
}

// --- Stats Degradation Benchmarks ---

// BenchmarkStatsDegradation_Degrade measures the lock-free CAS loop cost.
func BenchmarkStatsDegradation_Degrade(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Create fresh collector each time since Degrade is one-way
		sc := stats.NewCollector([]string{"service"}, stats.StatsLevelFull)
		sc.Degrade() // full → basic
	}
}

// BenchmarkStatsDegradation_ProcessAfterDegrade measures the Process fast path
// after stats have been degraded to none.
func BenchmarkStatsDegradation_ProcessAfterDegrade(b *testing.B) {
	sc := stats.NewCollector([]string{"service"}, stats.StatsLevelFull)
	sc.Degrade() // full → basic
	sc.Degrade() // basic → none

	metrics := []*metricspb.ResourceMetrics{makeGaugeRM("bench", 100)}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sc.Process(metrics)
	}
}

// --- End-to-End Pipeline Benchmarks ---

// BenchmarkEndToEnd_SteadyTraffic measures full pipeline throughput
// at sustained traffic levels for 1 second bursts.
func BenchmarkEndToEnd_SteadyTraffic(b *testing.B) {
	dpRates := []struct {
		name    string
		dpCount int
	}{
		{"1k_dps", 10},
		{"10k_dps", 100},
		{"50k_dps", 500},
	}

	for _, rate := range dpRates {
		b.Run(rate.name, func(b *testing.B) {
			exp := &countingExporter{}
			sc := stats.NewCollector([]string{"service"}, stats.StatsLevelBasic)
			buf := buffer.New(
				100000,
				500,
				20*time.Millisecond,
				exp,
				sc,
				&noopLimitsEnforcer{},
				&noopLogAggregator{},
			)

			ctx, cancel := context.WithCancel(context.Background())
			go buf.Start(ctx)

			metrics := []*metricspb.ResourceMetrics{makeGaugeRM("bench", rate.dpCount)}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				buf.Add(metrics)
			}

			cancel()
			buf.Wait()
		})
	}
}

// noopLogAggregator, countingExporter, noopStatsCollector, noopLimitsEnforcer,
// and makeGaugeRM are defined in pipeline_test.go (same package).
