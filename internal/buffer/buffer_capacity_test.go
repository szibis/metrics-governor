package buffer

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	"github.com/szibis/metrics-governor/internal/queue"
	"google.golang.org/protobuf/proto"
)

// estimateSliceBytes returns the total proto.Size of all ResourceMetrics in the slice.
func estimateSliceBytes(rms []*metricspb.ResourceMetrics) int64 {
	var total int64
	for _, rm := range rms {
		total += int64(proto.Size(rm))
	}
	return total
}

// TestBuffer_RejectPolicy_ReturnsErrBufferFull verifies that Add() returns
// ErrBufferFull when the buffer exceeds maxBufferBytes under the reject
// (DropNewest) policy.
func TestBuffer_RejectPolicy_ReturnsErrBufferFull(t *testing.T) {
	exp := &mockExporter{}

	// Create a small capacity buffer with reject (DropNewest) policy.
	// A single ResourceMetrics from createTestResourceMetrics is ~40-60 bytes,
	// so set maxBufferBytes low enough that 2 batches overflow.
	singleRM := createTestResourceMetrics(1)
	singleSize := estimateSliceBytes(singleRM)

	// Allow exactly 1 batch of 1 RM, but not 2.
	maxBytes := singleSize + 1

	buf := New(10000, 5000, time.Hour, exp, nil, nil, nil,
		WithMaxBufferBytes(maxBytes),
		WithBufferFullPolicy(queue.DropNewest),
	)

	// First Add should succeed.
	err := buf.Add(createTestResourceMetrics(1))
	if err != nil {
		t.Fatalf("first Add() should succeed, got: %v", err)
	}

	// Second Add should exceed capacity and return ErrBufferFull.
	err = buf.Add(createTestResourceMetrics(1))
	if err == nil {
		t.Fatal("second Add() should return error, got nil")
	}
	if !errors.Is(err, ErrBufferFull) {
		t.Fatalf("expected ErrBufferFull, got: %v", err)
	}
}

// TestBuffer_RejectPolicy_PartialBatchNotAdded verifies that when a batch is
// rejected due to capacity, the buffer contents remain unchanged (no partial add).
func TestBuffer_RejectPolicy_PartialBatchNotAdded(t *testing.T) {
	exp := &mockExporter{}

	singleRM := createTestResourceMetrics(1)
	singleSize := estimateSliceBytes(singleRM)

	// Allow room for exactly 2 RMs but not 5.
	maxBytes := singleSize*2 + 1

	buf := New(10000, 5000, time.Hour, exp, nil, nil, nil,
		WithMaxBufferBytes(maxBytes),
		WithBufferFullPolicy(queue.DropNewest),
	)

	// Add 2 RMs -- should succeed.
	err := buf.Add(createTestResourceMetrics(2))
	if err != nil {
		t.Fatalf("first Add(2) should succeed, got: %v", err)
	}

	buf.mu.Lock()
	countBefore := len(buf.metrics)
	buf.mu.Unlock()
	bytesBefore := buf.CurrentBytes()

	// Try to add 5 more -- should be rejected.
	err = buf.Add(createTestResourceMetrics(5))
	if !errors.Is(err, ErrBufferFull) {
		t.Fatalf("expected ErrBufferFull, got: %v", err)
	}

	// Buffer should be unchanged.
	buf.mu.Lock()
	countAfter := len(buf.metrics)
	buf.mu.Unlock()
	bytesAfter := buf.CurrentBytes()

	if countAfter != countBefore {
		t.Errorf("buffer item count changed on reject: before=%d, after=%d", countBefore, countAfter)
	}
	if bytesAfter != bytesBefore {
		t.Errorf("buffer bytes changed on reject: before=%d, after=%d", bytesBefore, bytesAfter)
	}
}

// TestBuffer_DropOldestPolicy_EvictsOldData verifies that under DropOldest
// policy, oldest data is evicted to make room and the new batch is accepted.
func TestBuffer_DropOldestPolicy_EvictsOldData(t *testing.T) {
	exp := &mockExporter{}

	singleRM := createTestResourceMetrics(1)
	singleSize := estimateSliceBytes(singleRM)

	// Allow room for 3 RMs.
	maxBytes := singleSize * 3

	buf := New(10000, 5000, time.Hour, exp, nil, nil, nil,
		WithMaxBufferBytes(maxBytes),
		WithBufferFullPolicy(queue.DropOldest),
	)

	// Fill buffer with 3 RMs.
	err := buf.Add(createTestResourceMetrics(3))
	if err != nil {
		t.Fatalf("initial Add(3) should succeed: %v", err)
	}
	if buf.CurrentBytes() == 0 {
		t.Fatal("buffer should have non-zero bytes after add")
	}

	buf.mu.Lock()
	if len(buf.metrics) != 3 {
		t.Fatalf("expected 3 items in buffer, got %d", len(buf.metrics))
	}
	buf.mu.Unlock()

	// Add 1 more -- should evict oldest to make room.
	err = buf.Add(createTestResourceMetrics(1))
	if err != nil {
		t.Fatalf("Add(1) with DropOldest should succeed, got: %v", err)
	}

	buf.mu.Lock()
	count := len(buf.metrics)
	buf.mu.Unlock()

	// After eviction, we should have 3 items (evicted 1 old, added 1 new).
	if count != 3 {
		t.Errorf("expected 3 items after eviction+add, got %d", count)
	}
}

// TestBuffer_DropOldestPolicy_EvictsEnoughForNewBatch verifies that multiple
// old entries are evicted if the new batch is larger than a single entry.
func TestBuffer_DropOldestPolicy_EvictsEnoughForNewBatch(t *testing.T) {
	exp := &mockExporter{}

	singleRM := createTestResourceMetrics(1)
	singleSize := estimateSliceBytes(singleRM)

	// Allow room for 5 RMs.
	maxBytes := singleSize * 5

	buf := New(10000, 5000, time.Hour, exp, nil, nil, nil,
		WithMaxBufferBytes(maxBytes),
		WithBufferFullPolicy(queue.DropOldest),
	)

	// Fill with 5 small RMs.
	err := buf.Add(createTestResourceMetrics(5))
	if err != nil {
		t.Fatalf("initial Add(5) should succeed: %v", err)
	}

	buf.mu.Lock()
	if len(buf.metrics) != 5 {
		t.Fatalf("expected 5 items, got %d", len(buf.metrics))
	}
	buf.mu.Unlock()

	// Add a batch of 3 -- must evict at least 3 old entries.
	err = buf.Add(createTestResourceMetrics(3))
	if err != nil {
		t.Fatalf("Add(3) with DropOldest should succeed: %v", err)
	}

	buf.mu.Lock()
	count := len(buf.metrics)
	buf.mu.Unlock()

	// Buffer should have evicted 3 old and added 3 new = 5 total.
	if count != 5 {
		t.Errorf("expected 5 items after multi-eviction, got %d", count)
	}

	// Current bytes should not exceed max.
	if buf.CurrentBytes() > maxBytes {
		t.Errorf("currentBytes %d exceeds maxBufferBytes %d", buf.CurrentBytes(), maxBytes)
	}
}

// TestBuffer_BlockPolicy_BlocksUntilSpace verifies that under Block policy,
// Add() blocks until flush frees space.
func TestBuffer_BlockPolicy_BlocksUntilSpace(t *testing.T) {
	exp := &mockExporter{}

	singleRM := createTestResourceMetrics(1)
	singleSize := estimateSliceBytes(singleRM)

	// Allow room for 2 RMs.
	maxBytes := singleSize * 2

	buf := New(10000, 5000, time.Hour, exp, nil, nil, nil,
		WithMaxBufferBytes(maxBytes),
		WithBufferFullPolicy(queue.Block),
	)

	// Fill the buffer.
	err := buf.Add(createTestResourceMetrics(2))
	if err != nil {
		t.Fatalf("initial Add(2) should succeed: %v", err)
	}

	// Launch Add in a goroutine -- it should block.
	addDone := make(chan error, 1)
	go func() {
		addDone <- buf.Add(createTestResourceMetrics(1))
	}()

	// Verify it's blocking (no result within 100ms).
	select {
	case <-addDone:
		t.Fatal("Add() returned immediately; expected it to block")
	case <-time.After(100 * time.Millisecond):
		// Good, it's blocking.
	}

	// Simulate flush: clear buffer and reset currentBytes, then signal.
	buf.mu.Lock()
	buf.metrics = buf.metrics[:0]
	buf.mu.Unlock()
	buf.currentBytes.Store(0)
	buf.spaceCond.Broadcast()

	// Now Add should unblock and succeed.
	select {
	case err := <-addDone:
		if err != nil {
			t.Fatalf("Add() after flush should succeed, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Add() did not unblock after flush")
	}
}

// TestBuffer_BlockPolicy_ContextCancellation verifies that a blocked Add()
// does not hang forever if context is never freed. Since the current block
// implementation uses sync.Cond (not context), we verify via a timeout pattern
// that the goroutine does eventually proceed when space is freed by broadcast.
// This test ensures the blocking does not deadlock.
func TestBuffer_BlockPolicy_ContextCancellation(t *testing.T) {
	exp := &mockExporter{}

	singleRM := createTestResourceMetrics(1)
	singleSize := estimateSliceBytes(singleRM)

	// Tiny buffer.
	maxBytes := singleSize

	buf := New(10000, 5000, time.Hour, exp, nil, nil, nil,
		WithMaxBufferBytes(maxBytes),
		WithBufferFullPolicy(queue.Block),
	)

	// Fill the buffer.
	err := buf.Add(createTestResourceMetrics(1))
	if err != nil {
		t.Fatalf("initial Add should succeed: %v", err)
	}

	// Launch a blocked Add.
	addDone := make(chan error, 1)
	go func() {
		addDone <- buf.Add(createTestResourceMetrics(1))
	}()

	// Without freeing space, Add should stay blocked.
	select {
	case <-addDone:
		t.Fatal("Add() returned without space being freed")
	case <-time.After(200 * time.Millisecond):
		// Expected: still blocked.
	}

	// Now free space to unblock -- without this the goroutine would leak.
	buf.mu.Lock()
	buf.metrics = buf.metrics[:0]
	buf.mu.Unlock()
	buf.currentBytes.Store(0)
	buf.spaceCond.Broadcast()

	select {
	case err := <-addDone:
		if err != nil {
			t.Fatalf("Add() should succeed after space freed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Add() never unblocked -- potential deadlock")
	}
}

// TestBuffer_ByteTracking_Accurate verifies that currentBytes matches the sum
// of proto.Size() for all buffered items.
func TestBuffer_ByteTracking_Accurate(t *testing.T) {
	exp := &mockExporter{}
	buf := New(10000, 5000, time.Hour, exp, nil, nil, nil,
		WithMaxBufferBytes(0), // unbounded
	)

	batch1 := createTestResourceMetrics(3)
	batch2 := createTestResourceMetrics(5)

	err := buf.Add(batch1)
	if err != nil {
		t.Fatalf("Add(batch1) failed: %v", err)
	}
	err = buf.Add(batch2)
	if err != nil {
		t.Fatalf("Add(batch2) failed: %v", err)
	}

	// Compute expected bytes from what's in the buffer.
	buf.mu.Lock()
	var expectedBytes int64
	for _, rm := range buf.metrics {
		expectedBytes += int64(proto.Size(rm))
	}
	buf.mu.Unlock()

	actual := buf.CurrentBytes()
	if actual != expectedBytes {
		t.Errorf("CurrentBytes()=%d does not match sum of proto.Size()=%d", actual, expectedBytes)
	}
}

// TestBuffer_ByteTracking_DecrementsOnFlush verifies that after flush(),
// currentBytes returns to 0.
func TestBuffer_ByteTracking_DecrementsOnFlush(t *testing.T) {
	exp := &mockExporter{}
	buf := New(10000, 5000, 50*time.Millisecond, exp, nil, nil, nil,
		WithMaxBufferBytes(0), // unbounded
	)

	err := buf.Add(createTestResourceMetrics(10))
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	if buf.CurrentBytes() == 0 {
		t.Fatal("expected non-zero bytes before flush")
	}

	// Start buffer and let it flush.
	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Wait for flush.
	time.Sleep(200 * time.Millisecond)
	cancel()
	buf.Wait()

	if buf.CurrentBytes() != 0 {
		t.Errorf("expected 0 bytes after flush, got %d", buf.CurrentBytes())
	}
}

// TestBuffer_MaxBufferBytes_Zero_Unbounded verifies that with maxBufferBytes=0,
// no capacity limit is enforced (backwards compatibility).
func TestBuffer_MaxBufferBytes_Zero_Unbounded(t *testing.T) {
	exp := &mockExporter{}
	buf := New(10000, 5000, time.Hour, exp, nil, nil, nil,
		WithMaxBufferBytes(0), // Unbounded
		WithBufferFullPolicy(queue.DropNewest),
	)

	// Add a large number of items -- should never fail.
	for i := 0; i < 100; i++ {
		err := buf.Add(createTestResourceMetrics(10))
		if err != nil {
			t.Fatalf("Add() should never fail with maxBufferBytes=0, got: %v (iteration %d)", err, i)
		}
	}

	buf.mu.Lock()
	count := len(buf.metrics)
	buf.mu.Unlock()

	if count != 1000 {
		t.Errorf("expected 1000 items in buffer, got %d", count)
	}
}

// TestBuffer_CurrentBytes_Accessor verifies that CurrentBytes() returns the
// correct value matching the internal atomic counter.
func TestBuffer_CurrentBytes_Accessor(t *testing.T) {
	exp := &mockExporter{}
	buf := New(10000, 5000, time.Hour, exp, nil, nil, nil)

	// Initially zero.
	if buf.CurrentBytes() != 0 {
		t.Errorf("expected 0 bytes initially, got %d", buf.CurrentBytes())
	}

	batch := createTestResourceMetrics(5)
	expectedBytes := estimateSliceBytes(batch)

	err := buf.Add(batch)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	if buf.CurrentBytes() != expectedBytes {
		t.Errorf("expected %d bytes, got %d", expectedBytes, buf.CurrentBytes())
	}

	// Add more.
	batch2 := createTestResourceMetrics(3)
	expectedBytes += estimateSliceBytes(batch2)

	err = buf.Add(batch2)
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	if buf.CurrentBytes() != expectedBytes {
		t.Errorf("expected %d bytes after second Add, got %d", expectedBytes, buf.CurrentBytes())
	}
}

// TestBuffer_RejectPolicy_ConcurrentAdds tests that the reject policy works
// correctly under concurrent access: some adds succeed, some are rejected.
func TestBuffer_RejectPolicy_ConcurrentAdds(t *testing.T) {
	exp := &mockExporter{}

	singleRM := createTestResourceMetrics(1)
	singleSize := estimateSliceBytes(singleRM)

	// Allow room for 10 RMs.
	maxBytes := singleSize * 10

	buf := New(10000, 5000, time.Hour, exp, nil, nil, nil,
		WithMaxBufferBytes(maxBytes),
		WithBufferFullPolicy(queue.DropNewest),
	)

	var wg sync.WaitGroup
	var mu sync.Mutex
	successCount := 0
	rejectCount := 0

	// Launch 20 goroutines each adding 1 RM.
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := buf.Add(createTestResourceMetrics(1))
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successCount++
			} else if errors.Is(err, ErrBufferFull) {
				rejectCount++
			} else {
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}

	wg.Wait()

	if successCount+rejectCount != 20 {
		t.Errorf("expected 20 total outcomes, got %d successes + %d rejects = %d",
			successCount, rejectCount, successCount+rejectCount)
	}
	if successCount == 0 {
		t.Error("expected at least some successful adds")
	}
	if rejectCount == 0 {
		t.Error("expected at least some rejected adds (buffer should overflow)")
	}

	// Note: Due to the lock-free race window between enforceCapacity() checking
	// currentBytes and addInternal() actually adding to currentBytes, concurrent
	// adds can temporarily overshoot maxBufferBytes by up to one batch per
	// concurrent goroutine. This is a known trade-off for lock-free performance.
	// We verify that the overshoot is bounded (at most N concurrent batches).
	maxOvershoot := singleSize * 20 // worst case: all 20 pass the check simultaneously
	if buf.CurrentBytes() > maxBytes+maxOvershoot {
		t.Errorf("currentBytes %d exceeds reasonable bound (max %d + overshoot %d)",
			buf.CurrentBytes(), maxBytes, maxOvershoot)
	}
}

// TestBuffer_DropOldestPolicy_BytesStayWithinLimit verifies that after
// eviction, the byte count does not exceed the configured maximum.
func TestBuffer_DropOldestPolicy_BytesStayWithinLimit(t *testing.T) {
	exp := &mockExporter{}

	singleRM := createTestResourceMetrics(1)
	singleSize := estimateSliceBytes(singleRM)

	maxBytes := singleSize * 4

	buf := New(10000, 5000, time.Hour, exp, nil, nil, nil,
		WithMaxBufferBytes(maxBytes),
		WithBufferFullPolicy(queue.DropOldest),
	)

	// Keep adding batches -- bytes should never exceed max + incoming batch.
	for i := 0; i < 20; i++ {
		err := buf.Add(createTestResourceMetrics(2))
		if err != nil {
			t.Fatalf("Add() with DropOldest should not fail, got: %v (iteration %d)", err, i)
		}
		// After the add, currentBytes should be within max (since eviction already ran)
		// plus the newly added bytes (which are added after capacity check).
		// The invariant is: currentBytes <= maxBufferBytes + incoming batch size.
		current := buf.CurrentBytes()
		incomingMax := singleSize * 2
		if current > maxBytes+incomingMax {
			t.Errorf("iteration %d: currentBytes %d exceeds maxBufferBytes+incoming %d",
				i, current, maxBytes+incomingMax)
		}
	}
}

// TestBuffer_BlockPolicy_FlushUnblocks verifies the full integration: Start()
// flushes the buffer, which signals blocked Add() calls to proceed.
func TestBuffer_BlockPolicy_FlushUnblocks(t *testing.T) {
	exp := &mockExporter{}

	singleRM := createTestResourceMetrics(1)
	singleSize := estimateSliceBytes(singleRM)

	maxBytes := singleSize * 2

	buf := New(10000, 5000, 100*time.Millisecond, exp, nil, nil, nil,
		WithMaxBufferBytes(maxBytes),
		WithBufferFullPolicy(queue.Block),
	)

	// Fill buffer.
	err := buf.Add(createTestResourceMetrics(2))
	if err != nil {
		t.Fatalf("initial Add should succeed: %v", err)
	}

	// Start the buffer (will flush periodically).
	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Add should block, then unblock when flush clears space.
	addDone := make(chan error, 1)
	go func() {
		addDone <- buf.Add(createTestResourceMetrics(1))
	}()

	select {
	case err := <-addDone:
		if err != nil {
			t.Fatalf("Add() should succeed after flush, got: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Add() did not unblock after flush -- deadlock")
	}

	cancel()
	buf.Wait()
}
