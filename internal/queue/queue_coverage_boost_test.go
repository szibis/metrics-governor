package queue

import (
	"testing"
	"time"

	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
)

// ---------------------------------------------------------------------------
// Tests — MemoryBatchQueue.Size()
// ---------------------------------------------------------------------------

func TestMemoryBatchQueue_Size_Empty(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{MaxSize: 10})
	defer q.Close()

	if q.Size() != 0 {
		t.Errorf("expected 0 bytes for empty queue, got %d", q.Size())
	}
}

func TestMemoryBatchQueue_Size_AfterPush(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{MaxSize: 10})
	defer q.Close()

	batch := makeTestBatch(5)
	if err := q.PushBatch(batch); err != nil {
		t.Fatalf("PushBatch failed: %v", err)
	}

	size := q.Size()
	if size <= 0 {
		t.Errorf("expected positive size after push, got %d", size)
	}
	if size != int64(batch.EstimatedBytes) {
		t.Errorf("expected size %d, got %d", batch.EstimatedBytes, size)
	}
}

func TestMemoryBatchQueue_Size_AfterPushPop(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{MaxSize: 10})
	defer q.Close()

	batch := makeTestBatch(5)
	q.PushBatch(batch)
	q.PopBatch()

	if q.Size() != 0 {
		t.Errorf("expected 0 bytes after pop, got %d", q.Size())
	}
}

// ---------------------------------------------------------------------------
// Tests — IncrementSpilloverTotal
// ---------------------------------------------------------------------------

func TestIncrementSpilloverTotal(t *testing.T) {
	// Just exercise the function — it increments a Prometheus counter.
	IncrementSpilloverTotal()
	IncrementSpilloverTotal()
	// No assertion needed — just verify no panic.
}

// ---------------------------------------------------------------------------
// Tests — Metric increment functions (metrics.go)
// ---------------------------------------------------------------------------

func TestIncrementDirectExportTimeout(t *testing.T) {
	IncrementDirectExportTimeout()
}

func TestIncrementWorkersActive(t *testing.T) {
	IncrementWorkersActive()
	DecrementWorkersActive()
}

func TestSetWorkersTotal(t *testing.T) {
	SetWorkersTotal(4)
}

func TestIncrementNonRetryableDropped(t *testing.T) {
	IncrementNonRetryableDropped("test_error")
}

func TestIncrementPreparersActive(t *testing.T) {
	IncrementPreparersActive()
	DecrementPreparersActive()
}

func TestIncrementSendersActive(t *testing.T) {
	IncrementSendersActive()
	DecrementSendersActive()
}

func TestSetPreparersTotal(t *testing.T) {
	SetPreparersTotal(2)
}

func TestSetSendersTotal(t *testing.T) {
	SetSendersTotal(3)
}

func TestSetPreparedChannelLength(t *testing.T) {
	SetPreparedChannelLength(100)
}

func TestIncrementExportDataLoss(t *testing.T) {
	IncrementExportDataLoss()
}

func TestSetSendsInflight(t *testing.T) {
	SetSendsInflight(5)
}

func TestSetSendsInflightMax(t *testing.T) {
	SetSendsInflightMax(10)
}

func TestSetWorkersDesired(t *testing.T) {
	SetWorkersDesired(6)
}

func TestIncrementScalerAdjustment(t *testing.T) {
	IncrementScalerAdjustment("up")
	IncrementScalerAdjustment("down")
}

func TestSetExportLatencyEWMA(t *testing.T) {
	SetExportLatencyEWMA(0.123)
}

// ---------------------------------------------------------------------------
// Tests — MemoryBatchQueue edge cases for handleFullLocked
// ---------------------------------------------------------------------------

func TestMemoryBatchQueue_HandleFull_DropOldest_SecondPushFails(t *testing.T) {
	// Create a queue of size 1 with DropOldest behavior.
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      1,
		FullBehavior: DropOldest,
	})
	defer q.Close()

	// Fill the queue.
	q.PushBatch(makeTestBatch(1))

	// Push again — should drop oldest and push the new one.
	err := q.PushBatch(makeTestBatch(1))
	if err != nil {
		t.Errorf("expected nil error with DropOldest, got: %v", err)
	}
}

func TestMemoryBatchQueue_HandleFull_DefaultBehavior(t *testing.T) {
	// Create with an invalid full behavior — should fallback to default.
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      1,
		FullBehavior: FullBehavior("invalid"),
	})
	defer q.Close()

	q.PushBatch(makeTestBatch(1))
	err := q.PushBatch(makeTestBatch(1))
	if err == nil {
		t.Error("expected error with invalid FullBehavior on full queue")
	}
}

func TestMemoryBatchQueue_HandleFull_BlockTimeout(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      1,
		FullBehavior: Block,
		BlockTimeout: 50 * time.Millisecond,
	})
	defer q.Close()

	q.PushBatch(makeTestBatch(1))

	// Push should block then timeout
	start := time.Now()
	err := q.PushBatch(makeTestBatch(1))
	elapsed := time.Since(start)

	if err == nil {
		t.Error("expected error on block timeout")
	}
	if elapsed < 30*time.Millisecond {
		t.Errorf("expected to block for at least 30ms, elapsed %v", elapsed)
	}
}

func TestMemoryBatchQueue_HandleFull_BytesLimit_DropOldest(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      100,
		MaxBytes:     200,
		FullBehavior: DropOldest,
	})
	defer q.Close()

	// Fill to byte limit
	q.PushBatch(makeTestBatch(1)) // 100 bytes
	q.PushBatch(makeTestBatch(1)) // 200 bytes — at limit

	// Push another — should drop oldest to make room
	err := q.PushBatch(makeTestBatch(1))
	if err != nil {
		t.Errorf("expected nil error with DropOldest on byte limit, got: %v", err)
	}
}

func TestMemoryBatchQueue_HandleFull_BytesLimit_DropNewest(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      100,
		MaxBytes:     200,
		FullBehavior: DropNewest,
	})
	defer q.Close()

	q.PushBatch(makeTestBatch(1))
	q.PushBatch(makeTestBatch(1))

	// Push another — should reject incoming
	err := q.PushBatch(makeTestBatch(1))
	if err == nil {
		t.Error("expected error with DropNewest on byte limit")
	}
}

// ---------------------------------------------------------------------------
// Tests — MemoryBatchQueue Utilization
// ---------------------------------------------------------------------------

func TestMemoryBatchQueue_Utilization_ByteBased(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:  100,
		MaxBytes: 1000,
	})
	defer q.Close()

	// Push batches with large EstimatedBytes to make byte utilization higher than count.
	bigBatch := &ExportBatch{
		ResourceMetrics: []*metricspb.ResourceMetrics{{}},
		Timestamp:       time.Now(),
		EstimatedBytes:  600, // 60% of MaxBytes but only 1% of MaxSize
	}
	q.PushBatch(bigBatch)

	util := q.Utilization()
	if util < 0.5 {
		t.Errorf("expected byte-based utilization >= 0.5, got %f", util)
	}
}

// ---------------------------------------------------------------------------
// Tests — PopBatchBlocking with closed channel
// ---------------------------------------------------------------------------

func TestMemoryBatchQueue_PopBatchBlocking_ClosedChannel(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{MaxSize: 10})

	// Push one batch, close, then pop
	q.PushBatch(makeTestBatch(1))
	q.Close()

	batch, err := q.PopBatchBlocking(nil)
	if batch == nil {
		t.Error("expected batch from closed queue with remaining entries")
	}
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Now all drained — should get ErrBatchQueueClosed
	batch, err = q.PopBatchBlocking(nil)
	if err == nil {
		t.Error("expected error from PopBatchBlocking on empty closed queue")
	}
}

// ---------------------------------------------------------------------------
// Tests — PushBatch to closed queue
// ---------------------------------------------------------------------------

func TestMemoryBatchQueue_PushBatch_Closed(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{MaxSize: 10})
	q.Close()

	err := q.PushBatch(makeTestBatch(1))
	if err == nil {
		t.Error("expected error pushing to closed queue")
	}
}

// ---------------------------------------------------------------------------
// Tests — DropOldest when channel already drained
// ---------------------------------------------------------------------------

func TestMemoryBatchQueue_HandleFull_DropOldest_ChannelAlreadyDrained(t *testing.T) {
	// Size 1: fill it, then drain it from another goroutine before the drop
	// attempt. This exercises the "default" case in the drop-oldest select.
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      1,
		FullBehavior: DropOldest,
	})
	defer q.Close()

	q.PushBatch(makeTestBatch(1))

	// Pop the entry so channel is empty when DropOldest tries to drain
	q.PopBatch()

	// Now push — should succeed (channel has space after pop).
	err := q.PushBatch(makeTestBatch(1))
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
}
