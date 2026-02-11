package queue

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
)

func makeTestBatch(size int) *ExportBatch {
	rms := make([]*metricspb.ResourceMetrics, size)
	for i := range rms {
		rms[i] = &metricspb.ResourceMetrics{}
	}
	return &ExportBatch{
		ResourceMetrics: rms,
		Timestamp:       time.Now(),
		EstimatedBytes:  size * 100, // rough estimate
	}
}

func TestNewExportBatch(t *testing.T) {
	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{},
		},
	}
	batch := NewExportBatch(req)
	if batch == nil {
		t.Fatal("expected non-nil batch")
	}
	if len(batch.ResourceMetrics) != 1 {
		t.Fatalf("expected 1 RM, got %d", len(batch.ResourceMetrics))
	}
	if batch.EstimatedBytes <= 0 {
		t.Fatal("expected positive EstimatedBytes")
	}
	if batch.Timestamp.IsZero() {
		t.Fatal("expected non-zero timestamp")
	}
}

func TestExportBatch_ToRequest(t *testing.T) {
	batch := makeTestBatch(3)
	req := batch.ToRequest()
	if req == nil {
		t.Fatal("expected non-nil request")
	}
	if len(req.ResourceMetrics) != 3 {
		t.Fatalf("expected 3 RMs, got %d", len(req.ResourceMetrics))
	}
	// Second call should return same cached instance
	req2 := batch.ToRequest()
	if req != req2 {
		t.Fatal("expected cached request instance")
	}
}

func TestMemoryBatchQueue_PushPop(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      10,
		FullBehavior: DropNewest,
	})
	defer q.Close()

	batch := makeTestBatch(1)
	if err := q.PushBatch(batch); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	if q.Len() != 1 {
		t.Fatalf("expected len 1, got %d", q.Len())
	}

	popped, err := q.PopBatch()
	if err != nil {
		t.Fatalf("pop failed: %v", err)
	}
	if popped == nil {
		t.Fatal("expected non-nil batch")
	}
	if len(popped.ResourceMetrics) != 1 {
		t.Fatal("popped batch has wrong RM count")
	}
	if q.Len() != 0 {
		t.Fatalf("expected len 0, got %d", q.Len())
	}
}

func TestMemoryBatchQueue_PopEmpty(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{MaxSize: 10})
	defer q.Close()

	batch, err := q.PopBatch()
	if err != nil {
		t.Fatalf("pop empty should not error, got: %v", err)
	}
	if batch != nil {
		t.Fatal("expected nil batch from empty queue")
	}
}

func TestMemoryBatchQueue_DropNewest(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      2,
		FullBehavior: DropNewest,
	})
	defer q.Close()

	b1 := makeTestBatch(1)
	b2 := makeTestBatch(1)
	b3 := makeTestBatch(1)

	if err := q.PushBatch(b1); err != nil {
		t.Fatalf("push 1: %v", err)
	}
	if err := q.PushBatch(b2); err != nil {
		t.Fatalf("push 2: %v", err)
	}

	// Third push should fail (drop newest)
	err := q.PushBatch(b3)
	if err != ErrBatchQueueFull {
		t.Fatalf("expected ErrBatchQueueFull, got: %v", err)
	}

	// Original batches should be intact
	if q.Len() != 2 {
		t.Fatalf("expected len 2, got %d", q.Len())
	}
}

func TestMemoryBatchQueue_DropOldest(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      2,
		FullBehavior: DropOldest,
	})
	defer q.Close()

	b1 := makeTestBatch(1)
	b1.EstimatedBytes = 100
	b2 := makeTestBatch(1)
	b2.EstimatedBytes = 200
	b3 := makeTestBatch(1)
	b3.EstimatedBytes = 300

	_ = q.PushBatch(b1)
	_ = q.PushBatch(b2)

	// Should drop oldest (b1) and push b3
	if err := q.PushBatch(b3); err != nil {
		t.Fatalf("push 3 with drop_oldest: %v", err)
	}

	if q.Len() != 2 {
		t.Fatalf("expected len 2, got %d", q.Len())
	}

	// First pop should be b2 (b1 was dropped)
	popped, _ := q.PopBatch()
	if popped.EstimatedBytes != 200 {
		t.Fatalf("expected b2 (200 bytes), got %d bytes", popped.EstimatedBytes)
	}
}

func TestMemoryBatchQueue_ByteLimit(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      100, // high count limit
		MaxBytes:     500, // low byte limit
		FullBehavior: DropNewest,
	})
	defer q.Close()

	b1 := makeTestBatch(1)
	b1.EstimatedBytes = 300
	b2 := makeTestBatch(1)
	b2.EstimatedBytes = 300

	if err := q.PushBatch(b1); err != nil {
		t.Fatalf("push 1: %v", err)
	}

	// Second push exceeds byte limit
	err := q.PushBatch(b2)
	if err != ErrBatchQueueFull {
		t.Fatalf("expected ErrBatchQueueFull on byte limit, got: %v", err)
	}
}

func TestMemoryBatchQueue_BlockTimeout(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      1,
		FullBehavior: Block,
		BlockTimeout: 50 * time.Millisecond,
	})
	defer q.Close()

	_ = q.PushBatch(makeTestBatch(1))

	start := time.Now()
	err := q.PushBatch(makeTestBatch(1))
	elapsed := time.Since(start)

	if err != ErrBatchQueueFull {
		t.Fatalf("expected ErrBatchQueueFull after timeout, got: %v", err)
	}
	if elapsed < 40*time.Millisecond {
		t.Fatalf("block didn't wait long enough: %v", elapsed)
	}
}

func TestMemoryBatchQueue_BlockSuccess(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      1,
		FullBehavior: Block,
		BlockTimeout: 2 * time.Second,
	})
	defer q.Close()

	_ = q.PushBatch(makeTestBatch(1))

	// Pop in background to make space
	go func() {
		time.Sleep(50 * time.Millisecond)
		_, _ = q.PopBatch()
	}()

	err := q.PushBatch(makeTestBatch(1))
	if err != nil {
		t.Fatalf("expected successful push after space freed, got: %v", err)
	}
}

func TestMemoryBatchQueue_Close(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{MaxSize: 10})
	q.Close()

	err := q.PushBatch(makeTestBatch(1))
	if err != ErrBatchQueueClosed {
		t.Fatalf("expected ErrBatchQueueClosed, got: %v", err)
	}
}

func TestMemoryBatchQueue_PopAfterClose(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{MaxSize: 10})
	_ = q.PushBatch(makeTestBatch(1))
	q.Close()

	// Should still be able to drain
	batch, err := q.PopBatch()
	if err != nil {
		t.Fatalf("pop after close should drain, got error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected to drain batch after close")
	}

	// Next pop should return closed error
	_, err = q.PopBatch()
	if err != ErrBatchQueueClosed {
		t.Fatalf("expected ErrBatchQueueClosed after drain, got: %v", err)
	}
}

func TestMemoryBatchQueue_PopBatchBlocking(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{MaxSize: 10})
	defer q.Close()

	done := make(chan struct{})

	// Push in background
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = q.PushBatch(makeTestBatch(1))
	}()

	batch, err := q.PopBatchBlocking(done)
	if err != nil {
		t.Fatalf("blocking pop error: %v", err)
	}
	if batch == nil {
		t.Fatal("expected non-nil batch from blocking pop")
	}
}

func TestMemoryBatchQueue_PopBatchBlockingDone(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{MaxSize: 10})
	defer q.Close()

	done := make(chan struct{})
	close(done) // signal done immediately

	batch, err := q.PopBatchBlocking(done)
	if err != nil {
		t.Fatalf("expected nil error on done, got: %v", err)
	}
	if batch != nil {
		t.Fatal("expected nil batch when done signaled")
	}
}

func TestMemoryBatchQueue_Utilization(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:  10,
		MaxBytes: 1000,
	})
	defer q.Close()

	batch := makeTestBatch(1)
	batch.EstimatedBytes = 500
	_ = q.PushBatch(batch)

	util := q.Utilization()
	// Byte utilization = 500/1000 = 0.5, count = 1/10 = 0.1, max = 0.5
	if util < 0.4 || util > 0.6 {
		t.Fatalf("expected utilization ~0.5, got %f", util)
	}

	if !q.IsFull() == false {
		// not full yet
	}
}

func TestMemoryBatchQueue_ConcurrentPushPop(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      100,
		FullBehavior: DropOldest,
	})
	defer q.Close()

	var pushed, popped atomic.Int64
	var wg sync.WaitGroup

	// 10 pushers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if err := q.PushBatch(makeTestBatch(1)); err == nil {
					pushed.Add(1)
				}
			}
		}()
	}

	// 5 poppers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if b, _ := q.PopBatch(); b != nil {
					popped.Add(1)
				}
				time.Sleep(time.Microsecond)
			}
		}()
	}

	wg.Wait()
	t.Logf("pushed=%d popped=%d remaining=%d", pushed.Load(), popped.Load(), q.Len())
}

func TestMemoryBatchQueue_IsFull_CountLimit(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      2,
		FullBehavior: DropNewest,
	})
	defer q.Close()

	_ = q.PushBatch(makeTestBatch(1))
	if q.IsFull() {
		t.Fatal("should not be full with 1/2")
	}
	_ = q.PushBatch(makeTestBatch(1))
	if !q.IsFull() {
		t.Fatal("should be full with 2/2")
	}
}

func TestMemoryBatchQueue_IsFull_ByteLimit(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      100,
		MaxBytes:     200,
		FullBehavior: DropNewest,
	})
	defer q.Close()

	b := makeTestBatch(1)
	b.EstimatedBytes = 200
	_ = q.PushBatch(b)

	if !q.IsFull() {
		t.Fatal("should be full when byte limit reached")
	}
}

func TestQueueMode_String(t *testing.T) {
	tests := []struct {
		mode QueueMode
		want string
	}{
		{QueueModeMemory, "memory (zero-serialization, no persistence)"},
		{QueueModeDisk, "disk (serialized, full persistence)"},
		{QueueModeHybrid, "hybrid (memory L1 + disk L2 spillover)"},
		{QueueMode("unknown"), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.mode.String(); got != tt.want {
			t.Errorf("QueueMode(%q).String() = %q, want %q", tt.mode, got, tt.want)
		}
	}
}

func TestIsValidQueueMode(t *testing.T) {
	if !IsValidQueueMode("memory") {
		t.Fatal("memory should be valid")
	}
	if !IsValidQueueMode("disk") {
		t.Fatal("disk should be valid")
	}
	if !IsValidQueueMode("hybrid") {
		t.Fatal("hybrid should be valid")
	}
	if IsValidQueueMode("invalid") {
		t.Fatal("invalid should not be valid")
	}
}

func TestMemoryBatchQueue_DoubleClose(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{MaxSize: 10})
	q.Close()
	q.Close() // should not panic
}

func TestMemoryBatchQueue_DefaultConfig(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{})
	defer q.Close()

	if q.config.MaxSize != 1024 {
		t.Fatalf("expected default MaxSize 1024, got %d", q.config.MaxSize)
	}
	if q.config.FullBehavior != DropOldest {
		t.Fatalf("expected default FullBehavior drop_oldest, got %s", q.config.FullBehavior)
	}
}
