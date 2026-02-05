package prw

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Noop mock implementations
// ---------------------------------------------------------------------------

// noopExporter is a minimal PRWExporter that does nothing.
type noopExporter struct{}

func (e *noopExporter) Export(_ context.Context, _ *WriteRequest) error { return nil }
func (e *noopExporter) Close() error                                    { return nil }

// noopStats is a minimal PRWStatsCollector where every method is a no-op.
type noopStats struct{}

func (s *noopStats) RecordPRWReceived(_, _ int)             {}
func (s *noopStats) RecordPRWExport(_, _ int)               {}
func (s *noopStats) RecordPRWExportError()                  {}
func (s *noopStats) RecordPRWBytesReceived(_ int)           {}
func (s *noopStats) RecordPRWBytesReceivedCompressed(_ int) {}
func (s *noopStats) RecordPRWBytesSent(_ int)               {}
func (s *noopStats) RecordPRWBytesSentCompressed(_ int)     {}
func (s *noopStats) SetPRWBufferSize(_ int)                 {}

// noopLimits is a minimal PRWLimitsEnforcer that passes requests through unchanged.
type noopLimits struct{}

func (l *noopLimits) Process(req *WriteRequest) *WriteRequest { return req }

// noopLogAggregator is a minimal LogAggregator that does nothing.
type noopLogAggregator struct{}

func (a *noopLogAggregator) Error(_ string, _ string, _ map[string]interface{}, _ int64) {}
func (a *noopLogAggregator) Stop()                                                       {}

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

// makeRaceWriteRequest builds a WriteRequest with the given number of time
// series, each carrying a single sample.
func makeRaceWriteRequest(service string, metricName string, samples int) *WriteRequest {
	ts := make([]TimeSeries, samples)
	for i := 0; i < samples; i++ {
		ts[i] = TimeSeries{
			Labels: []Label{
				{Name: "__name__", Value: metricName},
				{Name: "service", Value: service},
				{Name: "instance", Value: fmt.Sprintf("instance-%d", i)},
			},
			Samples: []Sample{{Value: float64(i), Timestamp: time.Now().UnixMilli()}},
		}
	}
	return &WriteRequest{Timeseries: ts}
}

// raceBufferConfig returns a BufferConfig tuned for race/leak tests.
func raceBufferConfig() BufferConfig {
	return BufferConfig{
		MaxSize:       1000,
		MaxBatchSize:  100,
		FlushInterval: 50 * time.Millisecond,
	}
}

// ---------------------------------------------------------------------------
// Race condition tests
// ---------------------------------------------------------------------------

// TestRace_Buffer_ConcurrentAdd verifies that 8 goroutines calling Add
// concurrently do not trigger the race detector.
func TestRace_Buffer_ConcurrentAdd(t *testing.T) {
	buf := NewBuffer(
		raceBufferConfig(),
		&noopExporter{},
		&noopStats{},
		&noopLimits{},
		&noopLogAggregator{},
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	const goroutines = 8
	const iterations = 500

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				req := makeRaceWriteRequest(
					fmt.Sprintf("svc-%d", id),
					fmt.Sprintf("metric_%d_%d", id, i),
					3,
				)
				buf.Add(req)
			}
		}(g)
	}

	wg.Wait()
	cancel()
	buf.Wait()
}

// TestRace_Buffer_AddDuringFlush verifies that adding requests while the
// buffer is being flushed by the background goroutine does not race.
func TestRace_Buffer_AddDuringFlush(t *testing.T) {
	buf := NewBuffer(
		raceBufferConfig(),
		&noopExporter{},
		&noopStats{},
		&noopLimits{},
		&noopLogAggregator{},
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx) // flushes every 50ms

	const goroutines = 4
	const iterations = 300

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				req := makeRaceWriteRequest(
					fmt.Sprintf("flush-svc-%d", id),
					"flush_metric",
					5,
				)
				buf.Add(req)
				// Small sleep to interleave with ticker-driven flushes.
				if i%50 == 0 {
					time.Sleep(10 * time.Millisecond)
				}
			}
		}(g)
	}

	wg.Wait()
	cancel()
	buf.Wait()
}

// TestRace_Buffer_SetExporterDuringAdd verifies that calling SetExporter
// while Add is running concurrently does not race.
func TestRace_Buffer_SetExporterDuringAdd(t *testing.T) {
	exp1 := &noopExporter{}
	exp2 := &noopExporter{}

	buf := NewBuffer(
		raceBufferConfig(),
		exp1,
		&noopStats{},
		&noopLimits{},
		&noopLogAggregator{},
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	const addIterations = 500
	const swapIterations = 200

	var wg sync.WaitGroup

	// Goroutine that continuously adds requests.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < addIterations; i++ {
			req := makeRaceWriteRequest("swap-svc", "swap_metric", 2)
			buf.Add(req)
		}
	}()

	// Goroutine that swaps the exporter back and forth.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < swapIterations; i++ {
			if i%2 == 0 {
				buf.SetExporter(exp2)
			} else {
				buf.SetExporter(exp1)
			}
		}
	}()

	wg.Wait()
	cancel()
	buf.Wait()
}

// TestRace_Buffer_StartWait starts the buffer, fires concurrent Adds, then
// cancels the context and calls Wait. This stresses the shutdown path under
// concurrent load.
func TestRace_Buffer_StartWait(t *testing.T) {
	buf := NewBuffer(
		raceBufferConfig(),
		&noopExporter{},
		&noopStats{},
		&noopLimits{},
		&noopLogAggregator{},
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	const goroutines = 8
	const iterations = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				req := makeRaceWriteRequest(
					fmt.Sprintf("start-wait-svc-%d", id),
					"startwait_metric",
					4,
				)
				buf.Add(req)
			}
		}(g)
	}

	wg.Wait()
	cancel()
	buf.Wait()
}

// ---------------------------------------------------------------------------
// Memory leak tests
// ---------------------------------------------------------------------------

// heapAllocMB returns the current heap allocation in megabytes after forcing
// a garbage collection cycle.
func heapAllocMB() float64 {
	runtime.GC()
	runtime.GC() // two passes for finalizers
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.HeapAlloc) / (1024 * 1024)
}

// TestMemLeak_Buffer_AddCycles creates a buffer, adds many requests through
// it, and verifies that heap growth stays bounded after GC.
func TestMemLeak_Buffer_AddCycles(t *testing.T) {
	buf := NewBuffer(
		raceBufferConfig(),
		&noopExporter{},
		&noopStats{},
		&noopLimits{},
		&noopLogAggregator{},
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	const cycles = 50
	const requestsPerCycle = 200
	const samplesPerReq = 10

	// Measure baseline after GC.
	baselineMB := heapAllocMB()

	for c := 0; c < cycles; c++ {
		for r := 0; r < requestsPerCycle; r++ {
			req := makeRaceWriteRequest("leak-svc", "leak_metric", samplesPerReq)
			buf.Add(req)
		}
		// Allow flush to drain the buffer periodically.
		time.Sleep(60 * time.Millisecond)
	}

	cancel()
	buf.Wait()

	afterMB := heapAllocMB()
	growthMB := afterMB - baselineMB

	const thresholdMB = 30.0
	if growthMB > thresholdMB {
		t.Errorf("Heap grew by %.2f MB (baseline=%.2f MB, after=%.2f MB), exceeds threshold of %.0f MB",
			growthMB, baselineMB, afterMB, thresholdMB)
	} else {
		t.Logf("Heap growth: %.2f MB (baseline=%.2f MB, after=%.2f MB)", growthMB, baselineMB, afterMB)
	}
}

// TestMemLeak_WriteRequest_CreationCycles allocates a large number of
// WriteRequests and verifies that GC reclaims the memory.
func TestMemLeak_WriteRequest_CreationCycles(t *testing.T) {
	baselineMB := heapAllocMB()

	const cycles = 100
	const requestsPerCycle = 500
	const samplesPerReq = 10

	for c := 0; c < cycles; c++ {
		// Allocate a batch of WriteRequests; they should become garbage
		// once the slice goes out of scope at the end of each cycle.
		reqs := make([]*WriteRequest, requestsPerCycle)
		for r := 0; r < requestsPerCycle; r++ {
			reqs[r] = makeRaceWriteRequest("wr-svc", "wr_metric", samplesPerReq)
		}
		// Explicitly nil out to make eligible for GC.
		_ = reqs
	}

	afterMB := heapAllocMB()
	growthMB := afterMB - baselineMB

	const thresholdMB = 30.0
	if growthMB > thresholdMB {
		t.Errorf("Heap grew by %.2f MB (baseline=%.2f MB, after=%.2f MB), exceeds threshold of %.0f MB",
			growthMB, baselineMB, afterMB, thresholdMB)
	} else {
		t.Logf("Heap growth: %.2f MB (baseline=%.2f MB, after=%.2f MB)", growthMB, baselineMB, afterMB)
	}
}
