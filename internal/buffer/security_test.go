package buffer

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/exporter"
	"github.com/szibis/metrics-governor/internal/queue"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// secTestResourceMetrics creates N ResourceMetrics with realistic label payloads
// so proto.Size returns a meaningful byte count (~100+ bytes each).
func secTestResourceMetrics(n int) []*metricspb.ResourceMetrics {
	out := make([]*metricspb.ResourceMetrics, n)
	for i := 0; i < n; i++ {
		out[i] = &metricspb.ResourceMetrics{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					{
						Key: "service.name",
						Value: &commonpb.AnyValue{
							Value: &commonpb.AnyValue_StringValue{
								StringValue: fmt.Sprintf("svc-%d", i),
							},
						},
					},
					{
						Key: "host.name",
						Value: &commonpb.AnyValue{
							Value: &commonpb.AnyValue_StringValue{
								StringValue: "security-test-host",
							},
						},
					},
				},
			},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: fmt.Sprintf("sec_test_metric_%d", i),
							Data: &metricspb.Metric_Gauge{
								Gauge: &metricspb.Gauge{
									DataPoints: []*metricspb.NumberDataPoint{
										{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)}},
									},
								},
							},
						},
					},
				},
			},
		}
	}
	return out
}

// secSlowExporter simulates a destination that takes a configurable time per export.
type secSlowExporter struct {
	mu       sync.Mutex
	delay    time.Duration
	calls    int64
	exported int64
	err      error
}

func (s *secSlowExporter) Export(_ context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	atomic.AddInt64(&s.calls, 1)
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.exported += int64(len(req.ResourceMetrics))
	return nil
}

// failingExporter always returns an error.
type failingExporter struct {
	calls atomic.Int64
}

func (f *failingExporter) Export(_ context.Context, _ *colmetricspb.ExportMetricsServiceRequest) error {
	f.calls.Add(1)
	return errors.New("permanent failure")
}

// secCountingExporter counts Export calls atomically.
type secCountingExporter struct {
	calls    atomic.Int64
	exported atomic.Int64
}

func (c *secCountingExporter) Export(_ context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	c.calls.Add(1)
	c.exported.Add(int64(len(req.ResourceMetrics)))
	return nil
}

// ---------------------------------------------------------------------------
// Security tests
// ---------------------------------------------------------------------------

// TestSecurity_BufferOverflow_Bounded verifies that even with millions of Add()
// calls the buffer's byte accounting never exceeds maxBufferBytes + one batch
// size (the batch currently being processed). This is the core invariant that
// prevents OOM when producers outpace consumers.
func TestSecurity_BufferOverflow_Bounded(t *testing.T) {
	const maxBytes int64 = 50_000 // ~50 KB cap
	const addCount = 5000

	exp := &secCountingExporter{}
	buf := New(
		100, 50, 50*time.Millisecond, exp, nil, nil, nil,
		WithMaxBufferBytes(maxBytes),
		WithBufferFullPolicy(queue.DropOldest), // evict oldest to keep accepting
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)
	defer func() {
		cancel()
		buf.Wait()
	}()

	var peakBytes int64
	for i := 0; i < addCount; i++ {
		_ = buf.Add(secTestResourceMetrics(1))
		current := buf.CurrentBytes()
		if current > atomic.LoadInt64(&peakBytes) {
			atomic.StoreInt64(&peakBytes, current)
		}
	}

	// Allow final flush
	time.Sleep(200 * time.Millisecond)

	// Peak should never be much more than maxBytes. We allow 2x as headroom
	// for the race between Add() accounting and flush() reset, plus the size
	// of one batch currently in flight.
	limit := maxBytes * 2
	if peakBytes > limit {
		t.Fatalf("peak buffer bytes %d exceeded safety limit %d (maxBufferBytes=%d)",
			peakBytes, limit, maxBytes)
	}
	t.Logf("peak buffer bytes: %d (limit: %d)", peakBytes, limit)
}

// TestSecurity_NoUnboundedGrowth verifies that with a slow exporter and
// continuous Add(), heap memory stays bounded. This is the key regression
// test: without always-queue, the old code accumulated unbounded data.
func TestSecurity_NoUnboundedGrowth(t *testing.T) {
	const maxBytes int64 = 100_000 // 100 KB cap

	exp := &secSlowExporter{delay: 5 * time.Millisecond}
	buf := New(
		200, 100, 30*time.Millisecond, exp, nil, nil, nil,
		WithMaxBufferBytes(maxBytes),
		WithBufferFullPolicy(queue.DropNewest), // reject when full
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)
	defer func() {
		cancel()
		buf.Wait()
	}()

	// Force GC and take baseline heap snapshot
	runtime.GC()
	var baselineStats runtime.MemStats
	runtime.ReadMemStats(&baselineStats)
	baselineHeap := baselineStats.HeapAlloc

	// Hammer the buffer for a while
	const duration = 500 * time.Millisecond
	deadline := time.Now().Add(duration)
	for time.Now().Before(deadline) {
		_ = buf.Add(secTestResourceMetrics(5))
	}

	// Take heap snapshot after sustained load
	runtime.GC()
	var afterStats runtime.MemStats
	runtime.ReadMemStats(&afterStats)
	afterHeap := afterStats.HeapAlloc

	// Heap should not have grown by more than a generous threshold.
	// We allow 50 MB of growth to account for GC non-determinism, goroutine
	// stacks, test framework overhead, etc. The point is it should NOT grow
	// by hundreds of MB which would indicate unbounded buffering.
	const maxGrowth = 50 * 1024 * 1024 // 50 MB
	growth := int64(afterHeap) - int64(baselineHeap)
	if growth > int64(maxGrowth) {
		t.Fatalf("heap grew by %d bytes (%.1f MB) which exceeds %d MB threshold; "+
			"possible unbounded memory growth",
			growth, float64(growth)/(1024*1024), maxGrowth/(1024*1024))
	}
	t.Logf("heap growth: %d bytes (%.2f MB), baseline: %.2f MB",
		growth, float64(growth)/(1024*1024), float64(baselineHeap)/(1024*1024))
}

// TestSecurity_RejectPolicyPreventsOOM verifies that with reject (DropNewest)
// policy and a small maxBufferBytes, sustained high-throughput Add() calls
// are safely rejected. The rejected count must be > 0 and there must be no
// panic.
func TestSecurity_RejectPolicyPreventsOOM(t *testing.T) {
	const maxBytes int64 = 5_000 // very small cap
	const totalAdds = 10_000

	exp := &secSlowExporter{delay: 10 * time.Millisecond}
	buf := New(
		50, 25, 20*time.Millisecond, exp, nil, nil, nil,
		WithMaxBufferBytes(maxBytes),
		WithBufferFullPolicy(queue.DropNewest), // reject incoming
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)
	defer func() {
		cancel()
		buf.Wait()
	}()

	var rejected atomic.Int64
	var accepted atomic.Int64

	// No panic expected — wrap in a deferred recover just in case
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic during sustained Add(): %v", r)
			}
		}()

		for i := 0; i < totalAdds; i++ {
			err := buf.Add(secTestResourceMetrics(1))
			if errors.Is(err, ErrBufferFull) {
				rejected.Add(1)
			} else if err == nil {
				accepted.Add(1)
			}
		}
	}()

	rejCount := rejected.Load()
	accCount := accepted.Load()
	t.Logf("accepted: %d, rejected: %d out of %d", accCount, rejCount, totalAdds)

	if rejCount == 0 {
		t.Fatal("expected at least some rejections with small buffer and high throughput")
	}
	if accCount == 0 {
		t.Fatal("expected at least some accepted entries")
	}
}

// TestSecurity_ConcurrentAccessNoRace exercises multiple goroutines doing
// Add(), flush(), and CurrentBytes() simultaneously. This test relies on
// the -race detector to catch data races.
func TestSecurity_ConcurrentAccessNoRace(t *testing.T) {
	const maxBytes int64 = 200_000
	const goroutines = 8
	const opsPerGoroutine = 200

	exp := &secCountingExporter{}
	buf := New(
		500, 100, 25*time.Millisecond, exp, nil, nil, nil,
		WithMaxBufferBytes(maxBytes),
		WithBufferFullPolicy(queue.DropOldest),
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)
	defer func() {
		cancel()
		buf.Wait()
	}()

	var wg sync.WaitGroup

	// Concurrent Add() goroutines
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				_ = buf.Add(secTestResourceMetrics(1))
			}
		}()
	}

	// Concurrent CurrentBytes() readers
	for g := 0; g < goroutines/2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine*2; i++ {
				_ = buf.CurrentBytes()
				runtime.Gosched()
			}
		}()
	}

	wg.Wait()

	// Let pending flushes complete
	time.Sleep(200 * time.Millisecond)

	// Basic sanity: no panic, no deadlock, exporter was called
	if exp.calls.Load() == 0 {
		t.Fatal("expected at least one export call after concurrent operations")
	}
	t.Logf("total export calls: %d, total exported ResourceMetrics: %d",
		exp.calls.Load(), exp.exported.Load())
}

// TestSecurity_GracefulDegradation verifies that when the exporter fails
// continuously, the buffer rejects cleanly: no goroutine leaks, no panics,
// and Close() completes in bounded time.
func TestSecurity_GracefulDegradation(t *testing.T) {
	const maxBytes int64 = 50_000

	fail := &failingExporter{}
	buf := New(
		100, 50, 30*time.Millisecond, fail, nil, nil, nil,
		WithMaxBufferBytes(maxBytes),
		WithBufferFullPolicy(queue.DropNewest),
	)

	// Record goroutine count before starting
	runtime.GC()
	goroutinesBefore := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Produce data for a while; exporter always fails
	for i := 0; i < 500; i++ {
		_ = buf.Add(secTestResourceMetrics(2))
		if i%50 == 0 {
			time.Sleep(10 * time.Millisecond) // let flush cycles run
		}
	}

	// Shut down
	cancel()

	// Wait must complete in bounded time
	done := make(chan struct{})
	go func() {
		buf.Wait()
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(10 * time.Second):
		t.Fatal("Wait() blocked for more than 10s — possible goroutine leak or deadlock")
	}

	// Check goroutine count returned to near baseline
	runtime.GC()
	time.Sleep(100 * time.Millisecond) // let deferred goroutines exit
	goroutinesAfter := runtime.NumGoroutine()

	// Allow some slack (test framework goroutines, finalizers, etc.)
	leaked := goroutinesAfter - goroutinesBefore
	if leaked > 5 {
		t.Fatalf("possible goroutine leak: before=%d after=%d delta=%d",
			goroutinesBefore, goroutinesAfter, leaked)
	}
	t.Logf("goroutines: before=%d after=%d delta=%d, exporter calls=%d",
		goroutinesBefore, goroutinesAfter, leaked, fail.calls.Load())
}

// ---------------------------------------------------------------------------
// Memory leak tests
// ---------------------------------------------------------------------------

// queuedExporterForTest creates a QueuedExporter in always-queue mode with
// fast settings suitable for testing. The underlying exporter is a nop that
// always succeeds.
func queuedExporterForTest(t *testing.T, workers int) (*exporter.QueuedExporter, *countingQueueExporter) {
	t.Helper()
	mock := &countingQueueExporter{}
	cfg := queue.Config{
		Path:               t.TempDir(),
		MaxSize:            1000,
		RetryInterval:      10 * time.Millisecond,
		MaxRetryDelay:      100 * time.Millisecond,
		AlwaysQueue:        true,
		Workers:            workers,
		CloseTimeout:       5 * time.Second,
		DrainTimeout:       2 * time.Second,
		DrainEntryTimeout:  1 * time.Second,
		RetryExportTimeout: 2 * time.Second,
	}
	qe, err := exporter.NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	return qe, mock
}

// countingQueueExporter implements exporter.Exporter for the memory leak tests.
type countingQueueExporter struct {
	calls  atomic.Int64
	closed atomic.Int32
}

func (c *countingQueueExporter) Export(_ context.Context, _ *colmetricspb.ExportMetricsServiceRequest) error {
	c.calls.Add(1)
	return nil
}

func (c *countingQueueExporter) Close() error {
	c.closed.Store(1)
	return nil
}

// TestMemLeak_WorkerPoolCleanup creates and Close()s multiple QueuedExporters
// in a loop. The goroutine count should return to baseline, proving that worker
// goroutines are properly cleaned up.
func TestMemLeak_WorkerPoolCleanup(t *testing.T) {
	// Let the runtime settle
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	const iterations = 10
	const workers = 4

	for i := 0; i < iterations; i++ {
		qe, _ := queuedExporterForTest(t, workers)

		// Push a few items so workers have something to do
		for j := 0; j < 5; j++ {
			req := &colmetricspb.ExportMetricsServiceRequest{
				ResourceMetrics: secTestResourceMetrics(1),
			}
			_ = qe.Export(context.Background(), req)
		}

		// Give workers a moment to process
		time.Sleep(50 * time.Millisecond)

		// Close and ensure workers exit
		if err := qe.Close(); err != nil {
			t.Fatalf("iteration %d: Close() error: %v", i, err)
		}
	}

	// Allow goroutines to fully exit
	runtime.GC()
	time.Sleep(200 * time.Millisecond)

	final := runtime.NumGoroutine()
	delta := final - baseline

	// Allow a small margin for test framework goroutines
	if delta > 5 {
		t.Fatalf("goroutine leak detected: baseline=%d final=%d delta=%d "+
			"(created and closed %d QueuedExporters with %d workers each)",
			baseline, final, delta, iterations, workers)
	}
	t.Logf("goroutines: baseline=%d final=%d delta=%d", baseline, final, delta)
}

// TestMemLeak_BufferFlushCycle runs 1000 Add/flush cycles and verifies that
// CurrentBytes() returns to 0 after each flush and there is no byte
// accumulation over time.
func TestMemLeak_BufferFlushCycle(t *testing.T) {
	const cycles = 1000
	const batchSize = 5

	exp := &secCountingExporter{}
	buf := New(
		1000, 100, time.Hour, exp, nil, nil, nil, // hour interval = manual flush only
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)
	defer func() {
		cancel()
		buf.Wait()
	}()

	var nonZeroAfterFlush int
	for i := 0; i < cycles; i++ {
		// Add a batch
		err := buf.Add(secTestResourceMetrics(batchSize))
		if err != nil {
			t.Fatalf("cycle %d: Add() error: %v", i, err)
		}

		if buf.CurrentBytes() == 0 {
			t.Fatalf("cycle %d: CurrentBytes() should be > 0 after Add()", i)
		}

		// Trigger flush via the flushChan (simulate buffer-full trigger)
		buf.flush(ctx)

		// After flush, currentBytes should be 0
		remaining := buf.CurrentBytes()
		if remaining != 0 {
			nonZeroAfterFlush++
		}
	}

	// All flushes should have reset bytes to 0
	if nonZeroAfterFlush > 0 {
		t.Fatalf("CurrentBytes() was non-zero after flush in %d out of %d cycles — "+
			"possible byte tracking leak", nonZeroAfterFlush, cycles)
	}

	// Final verification: total exported should equal total added
	totalExpected := int64(cycles * batchSize)
	if exp.exported.Load() != totalExpected {
		t.Fatalf("expected %d exported ResourceMetrics, got %d",
			totalExpected, exp.exported.Load())
	}
	t.Logf("completed %d add/flush cycles, exported %d ResourceMetrics",
		cycles, exp.exported.Load())
}

// TestMemLeak_LargePayloadRelease adds large protobuf batches, flushes, and
// verifies via runtime.GC() + runtime.ReadMemStats that the heap does not
// grow monotonically. This catches cases where large protobuf slices are
// retained after flush.
func TestMemLeak_LargePayloadRelease(t *testing.T) {
	const rounds = 20
	const batchSize = 100 // 100 ResourceMetrics per Add — non-trivial size

	exp := &secCountingExporter{}
	buf := New(
		5000, 500, time.Hour, exp, nil, nil, nil,
	)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)
	defer func() {
		cancel()
		buf.Wait()
	}()

	heapSamples := make([]uint64, 0, rounds)

	for r := 0; r < rounds; r++ {
		// Add large batch
		_ = buf.Add(secTestResourceMetrics(batchSize))

		// Flush
		buf.flush(ctx)

		// Force GC and sample heap
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		heapSamples = append(heapSamples, ms.HeapAlloc)
	}

	// Verify the heap is NOT monotonically increasing. If the last 5 samples
	// are all increasing, that is a strong signal of a leak.
	if len(heapSamples) >= 5 {
		last5 := heapSamples[len(heapSamples)-5:]
		monotonic := true
		for i := 1; i < len(last5); i++ {
			if last5[i] <= last5[i-1] {
				monotonic = false
				break
			}
		}
		if monotonic {
			t.Logf("WARNING: last 5 heap samples are monotonically increasing: %v", last5)
			// Check if the growth is significant (> 5 MB over the window)
			growth := last5[len(last5)-1] - last5[0]
			if growth > 5*1024*1024 {
				t.Fatalf("heap grew monotonically by %d bytes (%.1f MB) over last 5 rounds — "+
					"possible memory leak", growth, float64(growth)/(1024*1024))
			}
		}
	}

	// Also verify first vs last sample: heap should not have grown by > 20 MB
	if len(heapSamples) >= 2 {
		first := heapSamples[0]
		last := heapSamples[len(heapSamples)-1]
		if last > first {
			growth := last - first
			const maxGrowth = 20 * 1024 * 1024 // 20 MB
			if growth > maxGrowth {
				t.Fatalf("heap grew from %d to %d (%d bytes / %.1f MB) over %d rounds — "+
					"possible memory leak",
					first, last, growth, float64(growth)/(1024*1024), rounds)
			}
		}
	}

	t.Logf("heap samples (bytes): first=%d last=%d, exported=%d",
		heapSamples[0], heapSamples[len(heapSamples)-1], exp.exported.Load())
}
