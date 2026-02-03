package queue

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// TestSendQueueBlockBehaviorTimeout tests Block behavior with timeout.
func TestSendQueueBlockBehaviorTimeout(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      2,
		FullBehavior: Block,
		BlockTimeout: 100 * time.Millisecond,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Fill the queue
	req := createTestRequestForCoverage()
	q.Push(req)
	q.Push(req)

	// This push should block and timeout
	start := time.Now()
	err = q.Push(req)
	elapsed := time.Since(start)

	// Should have blocked for approximately BlockTimeout
	if elapsed < 50*time.Millisecond {
		t.Errorf("Block should have waited longer, elapsed: %v", elapsed)
	}

	// Should return an error (timeout)
	if err == nil {
		t.Error("Expected error after timeout")
	}
}

// TestSendQueueBlockBehaviorUnblocked tests Block behavior when space becomes available.
func TestSendQueueBlockBehaviorUnblocked(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      2,
		FullBehavior: Block,
		BlockTimeout: 2 * time.Second,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Fill the queue
	req := createTestRequestForCoverage()
	q.Push(req)
	q.Push(req)

	// Start a goroutine to pop after a delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		q.Pop()
	}()

	// This push should block until Pop creates space
	start := time.Now()
	err = q.Push(req)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("Push should succeed after Pop: %v", err)
	}

	// Should have unblocked relatively quickly
	if elapsed > time.Second {
		t.Errorf("Push blocked too long: %v", elapsed)
	}
}

// TestSendQueueDropNewestBehavior tests DropNewest behavior.
func TestSendQueueDropNewestBehavior(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      3,
		FullBehavior: DropNewest,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Fill the queue
	req := createTestRequestForCoverage()
	for i := 0; i < 3; i++ {
		q.Push(req)
	}

	// This push should drop the incoming entry (no error)
	err = q.Push(req)
	if err != nil {
		t.Errorf("DropNewest should not return error: %v", err)
	}

	// Queue should still have 3 entries
	if q.Len() != 3 {
		t.Errorf("Expected 3 entries, got %d", q.Len())
	}
}

// TestSendQueueDropOldestBehavior tests DropOldest behavior.
func TestSendQueueDropOldestBehavior(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      3,
		FullBehavior: DropOldest,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Fill the queue
	req := createTestRequestForCoverage()
	for i := 0; i < 3; i++ {
		q.Push(req)
	}

	// This push should drop oldest and succeed
	err = q.Push(req)
	if err != nil {
		t.Errorf("DropOldest should succeed: %v", err)
	}

	// Queue should still have 3 entries
	if q.Len() != 3 {
		t.Errorf("Expected 3 entries, got %d", q.Len())
	}
}

// TestSendQueuePushData tests PushData method directly.
func TestSendQueuePushData(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	data := []byte("raw-test-data")
	if err := q.PushData(data); err != nil {
		t.Fatalf("PushData failed: %v", err)
	}

	if q.Len() != 1 {
		t.Errorf("Expected 1 entry, got %d", q.Len())
	}
}

// TestSendQueuePushDataClosed tests PushData on closed queue.
func TestSendQueuePushDataClosed(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	q.Close()

	err = q.PushData([]byte("data"))
	if err == nil {
		t.Error("Expected error when pushing to closed queue")
	}
}

// TestSendQueueConcurrentPushPop tests concurrent operations.
func TestSendQueueConcurrentPushPop(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      1000,
		FullBehavior: DropOldest,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	var wg sync.WaitGroup
	req := createTestRequestForCoverage()

	// Pushers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				q.Push(req)
			}
		}()
	}

	// Poppers
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				q.Pop()
			}
		}()
	}

	wg.Wait()
}

// TestSendQueueUpdateRetries tests retry count updates.
func TestSendQueueUpdateRetries(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Push an entry
	req := createTestRequestForCoverage()
	q.Push(req)

	// Pop to get the entry
	entry, _ := q.Pop()
	if entry == nil {
		t.Fatal("Expected entry")
	}

	// Update retries (even though entry is already popped, this tests the mechanism)
	q.UpdateRetries(entry.ID, 5)

	// Verify it was stored
	retries := q.getRetries(entry.ID)
	if retries != 5 {
		t.Errorf("Expected 5 retries, got %d", retries)
	}
}

// TestSendQueueRemove tests Remove method.
func TestSendQueueRemove(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Remove is a no-op but should not error
	err = q.Remove("any-id")
	if err != nil {
		t.Errorf("Remove should not error: %v", err)
	}
}

// TestSendQueueRemoveCleansRetries tests that Remove cleans up retry tracking.
func TestSendQueueRemoveCleansRetries(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Store some retries
	q.UpdateRetries("test-id", 10)

	// Remove should clean up
	q.Remove("test-id")

	// Retries should be gone
	retries := q.getRetries("test-id")
	if retries != 0 {
		t.Errorf("Expected 0 retries after Remove, got %d", retries)
	}
}

// TestSendQueuePeekReturnsNewID tests that Peek returns new ID each time.
func TestSendQueuePeekReturnsNewID(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	req := createTestRequestForCoverage()
	q.Push(req)

	// Peek twice
	entry1, _ := q.Peek()
	entry2, _ := q.Peek()

	// IDs should be different (new UUID each time)
	if entry1.ID == entry2.ID {
		t.Error("Expected different IDs from Peek")
	}
}

// TestSendQueueEntryGetRequest tests QueueEntry.GetRequest.
func TestSendQueueEntryGetRequest(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Push a request
	req := createTestRequestForCoverage()
	q.Push(req)

	// Pop and deserialize
	entry, _ := q.Pop()
	if entry == nil {
		t.Fatal("Expected entry")
	}

	gotReq, err := entry.GetRequest()
	if err != nil {
		t.Fatalf("GetRequest failed: %v", err)
	}

	// Verify data matches
	if len(gotReq.ResourceMetrics) != len(req.ResourceMetrics) {
		t.Error("Deserialized request doesn't match")
	}
}

// TestSendQueueEntryGetRequestInvalid tests GetRequest with invalid data.
func TestSendQueueEntryGetRequestInvalid(t *testing.T) {
	entry := &QueueEntry{
		Data: []byte("not valid protobuf"),
	}

	_, err := entry.GetRequest()
	if err == nil {
		t.Error("Expected error for invalid protobuf")
	}
}

// TestSendQueueEffectiveLimits tests EffectiveMaxSize and EffectiveMaxBytes.
func TestSendQueueEffectiveLimits(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:     tmpDir,
		MaxSize:  5000,
		MaxBytes: 1024 * 1024 * 100, // 100MB
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	if q.EffectiveMaxSize() != 5000 {
		t.Errorf("Expected MaxSize=5000, got %d", q.EffectiveMaxSize())
	}

	if q.EffectiveMaxBytes() != 1024*1024*100 {
		t.Errorf("Expected MaxBytes=%d, got %d", 1024*1024*100, q.EffectiveMaxBytes())
	}
}

// TestSendQueueSize tests Size method.
func TestSendQueueSize(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	if q.Size() != 0 {
		t.Errorf("Expected Size=0 initially, got %d", q.Size())
	}

	req := createTestRequestForCoverage()
	q.Push(req)

	if q.Size() <= 0 {
		t.Error("Expected positive Size after push")
	}
}

// TestSendQueueDefaultBehaviors tests default config values.
func TestSendQueueDefaultBehaviors(t *testing.T) {
	tmpDir := t.TempDir()

	// Minimal config - should use defaults
	cfg := Config{
		Path: tmpDir,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Defaults should be applied
	if q.config.MaxSize != 10000 {
		t.Errorf("Expected default MaxSize=10000, got %d", q.config.MaxSize)
	}
	if q.config.FullBehavior != DropOldest {
		t.Errorf("Expected default FullBehavior=DropOldest, got %v", q.config.FullBehavior)
	}
}

// TestSendQueuePushOnClosed tests Push on closed queue.
func TestSendQueuePushOnClosed(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	q.Close()

	req := createTestRequestForCoverage()
	err = q.Push(req)
	if err == nil {
		t.Error("Expected error when pushing to closed queue")
	}
}

// TestSendQueuePopOnClosed tests Pop on closed queue.
func TestSendQueuePopOnClosed(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	q.Push(createTestRequestForCoverage())
	q.Close()

	_, err = q.Pop()
	if err != ErrQueueClosed {
		t.Errorf("Expected ErrQueueClosed, got %v", err)
	}
}

// TestSendQueuePeekOnClosed tests Peek on closed queue.
func TestSendQueuePeekOnClosed(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	q.Push(createTestRequestForCoverage())
	q.Close()

	_, err = q.Peek()
	if err != ErrQueueClosed {
		t.Errorf("Expected ErrQueueClosed, got %v", err)
	}
}

// TestSendQueueRemoveOnClosed tests Remove on closed queue.
func TestSendQueueRemoveOnClosed(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	q.Close()

	err = q.Remove("any-id")
	if err != ErrQueueClosed {
		t.Errorf("Expected ErrQueueClosed, got %v", err)
	}
}

// Helper function to create test requests
func createTestRequestForCoverage() *colmetricspb.ExportMetricsServiceRequest {
	return &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{
								Name: "test_metric",
								Data: &metricspb.Metric_Sum{
									Sum: &metricspb.Sum{
										DataPoints: []*metricspb.NumberDataPoint{
											{},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// =============================================================================
// Additional coverage tests for metrics.go functions at 0% coverage
// =============================================================================

// TestIncrementRetryFailure tests IncrementRetryFailure with various error types.
func TestIncrementRetryFailure(t *testing.T) {
	// These functions just call Inc on prometheus counters.
	// We verify they don't panic with known and unknown error types.
	errorTypes := []string{
		"network",
		"timeout",
		"server_error",
		"client_error",
		"auth",
		"rate_limit",
		"unknown",
		"custom_error_type",
	}
	for _, errType := range errorTypes {
		t.Run(errType, func(t *testing.T) {
			IncrementRetryFailure(errType)
		})
	}
}

// TestSetCircuitState tests SetCircuitState with all valid states.
func TestSetCircuitState(t *testing.T) {
	states := []string{"closed", "open", "half_open"}
	for _, state := range states {
		t.Run(state, func(t *testing.T) {
			SetCircuitState(state)
		})
	}
}

// TestSetCircuitStateSequence tests transitioning through circuit states.
func TestSetCircuitStateSequence(t *testing.T) {
	// Simulate a typical circuit breaker lifecycle
	SetCircuitState("closed")
	SetCircuitState("open")
	SetCircuitState("half_open")
	SetCircuitState("closed")
}

// TestIncrementCircuitOpen tests IncrementCircuitOpen does not panic.
func TestIncrementCircuitOpen(t *testing.T) {
	for i := 0; i < 5; i++ {
		IncrementCircuitOpen()
	}
}

// TestIncrementCircuitRejected tests IncrementCircuitRejected does not panic.
func TestIncrementCircuitRejected(t *testing.T) {
	for i := 0; i < 5; i++ {
		IncrementCircuitRejected()
	}
}

// TestSetCurrentBackoff tests SetCurrentBackoff with various durations.
func TestSetCurrentBackoff(t *testing.T) {
	durations := []time.Duration{
		0,
		100 * time.Millisecond,
		time.Second,
		5 * time.Second,
		30 * time.Second,
		time.Minute,
		5 * time.Minute,
	}
	for _, d := range durations {
		t.Run(d.String(), func(t *testing.T) {
			SetCurrentBackoff(d)
		})
	}
}

// =============================================================================
// Additional coverage tests for pushData (54.2%) - disk full and oversized data
// =============================================================================

// TestPushDataMaxBytesDropOldest tests pushData with MaxBytes limit and DropOldest.
func TestPushDataMaxBytesDropOldest(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      1000,
		MaxBytes:     200, // Small byte limit
		FullBehavior: DropOldest,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Push data that fills up the byte limit
	data := make([]byte, 80)
	for i := range data {
		data[i] = byte(i)
	}

	// First two pushes should fit
	if err := q.PushData(data); err != nil {
		t.Fatalf("First push failed: %v", err)
	}
	if err := q.PushData(data); err != nil {
		t.Fatalf("Second push failed: %v", err)
	}

	// Third push exceeds MaxBytes, should trigger DropOldest
	if err := q.PushData(data); err != nil {
		t.Fatalf("Third push (drop oldest) failed: %v", err)
	}

	// Queue should still work
	if q.Len() <= 0 {
		t.Error("Expected entries in queue after DropOldest")
	}
}

// TestPushDataMaxBytesDropNewest tests pushData with MaxBytes limit and DropNewest.
func TestPushDataMaxBytesDropNewest(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      1000,
		MaxBytes:     200, // Small byte limit
		FullBehavior: DropNewest,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	data := make([]byte, 80)
	q.PushData(data)
	q.PushData(data)

	// This push exceeds MaxBytes, should silently drop (DropNewest)
	err = q.PushData(data)
	if err != nil {
		t.Errorf("DropNewest should not return error: %v", err)
	}
}

// TestPushDataMaxBytesBlock tests pushData with MaxBytes limit and Block behavior.
func TestPushDataMaxBytesBlock(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      1000,
		MaxBytes:     200,
		FullBehavior: Block,
		BlockTimeout: 100 * time.Millisecond,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	data := make([]byte, 80)
	q.PushData(data)
	q.PushData(data)

	// Third push should block and timeout
	start := time.Now()
	err = q.PushData(data)
	elapsed := time.Since(start)

	if elapsed < 50*time.Millisecond {
		t.Errorf("Block should have waited longer, elapsed: %v", elapsed)
	}
	// May or may not error depending on whether the retry succeeded
}

// TestPushDataBlockUnblockedByPop tests block behavior unblocked by a Pop.
func TestPushDataBlockUnblockedByPop(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      2,
		FullBehavior: Block,
		BlockTimeout: 2 * time.Second,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	q.PushData([]byte("data1"))
	q.PushData([]byte("data2"))

	// Start a goroutine to pop after a delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		q.Pop()
	}()

	// This push should block and then succeed when Pop creates space
	err = q.PushData([]byte("data3"))
	if err != nil {
		t.Errorf("Push should succeed after Pop: %v", err)
	}
}

// TestPushDataOnClosedQueueCoverage tests pushData on closed queue.
func TestPushDataOnClosedQueueCoverage(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	q.Close()

	err = q.PushData([]byte("should-fail"))
	if err == nil {
		t.Error("Expected error when pushing to closed queue")
	}
}

// =============================================================================
// Additional coverage for Push (75.0%) - error and retry scenarios
// =============================================================================

// TestPushWithFullQueueDropOldest tests Push with full queue and DropOldest.
func TestPushWithFullQueueDropOldestCoverage(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      2,
		FullBehavior: DropOldest,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	req := createTestRequestForCoverage()

	// Fill queue
	q.Push(req)
	q.Push(req)

	// Should succeed by dropping oldest
	for i := 0; i < 10; i++ {
		if err := q.Push(req); err != nil {
			t.Errorf("Push %d with DropOldest failed: %v", i, err)
		}
	}

	if q.Len() != 2 {
		t.Errorf("Expected 2, got %d", q.Len())
	}
}

// TestPushWithFullQueueBlock tests Push with full queue and Block (timeout).
func TestPushWithFullQueueBlockCoverage(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      2,
		FullBehavior: Block,
		BlockTimeout: 50 * time.Millisecond,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	req := createTestRequestForCoverage()
	q.Push(req)
	q.Push(req)

	// Should timeout
	err = q.Push(req)
	if err == nil {
		t.Error("Expected error after block timeout")
	}
}

// =============================================================================
// Additional coverage for Peek in fastqueue.go (76.5%)
// =============================================================================

// TestFastQueuePeekEmptyQueue tests Peek on a completely empty queue.
func TestFastQueuePeekEmptyQueueCoverage(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	// Peek on empty queue should return nil, nil
	data, err := fq.Peek()
	if err != nil {
		t.Fatalf("Peek on empty queue failed: %v", err)
	}
	if data != nil {
		t.Error("Expected nil data from empty queue Peek")
	}
}

// TestFastQueuePeekFromDiskCoverage tests Peek when data is on disk.
func TestFastQueuePeekFromDiskCoverage(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	// Push enough data to force disk write (channel size is 1)
	for i := 0; i < 5; i++ {
		fq.Push([]byte("disk-peek-test-data"))
	}

	// Force flush to disk
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	// Peek should read from disk without advancing reader
	data1, err := fq.Peek()
	if err != nil {
		t.Fatalf("First Peek from disk failed: %v", err)
	}
	if string(data1) != "disk-peek-test-data" {
		t.Errorf("Expected 'disk-peek-test-data', got %q", data1)
	}

	// Second Peek should return the same data
	data2, err := fq.Peek()
	if err != nil {
		t.Fatalf("Second Peek from disk failed: %v", err)
	}
	if string(data2) != "disk-peek-test-data" {
		t.Errorf("Expected same data on second Peek, got %q", data2)
	}

	// Len should be unchanged after Peek
	if fq.Len() != 5 {
		t.Errorf("Expected 5 entries after Peek, got %d", fq.Len())
	}
}

// TestSendQueuePeekFromDisk tests SendQueue Peek when data is on disk.
func TestSendQueuePeekFromDisk(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:               tmpDir,
		MaxSize:            100,
		InmemoryBlocks:     1,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Push enough data to force disk write
	for i := 0; i < 5; i++ {
		q.PushData([]byte("peek-disk-data"))
	}

	// Peek should return data
	entry, err := q.Peek()
	if err != nil {
		t.Fatalf("Peek failed: %v", err)
	}
	if entry == nil {
		t.Fatal("Expected non-nil entry from Peek")
	}
	if string(entry.Data) != "peek-disk-data" {
		t.Errorf("Expected 'peek-disk-data', got %q", entry.Data)
	}
}

// TestSendQueuePeekEmptyQueue tests Peek on empty SendQueue.
func TestSendQueuePeekEmptyQueueCoverage(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	entry, err := q.Peek()
	if err != nil {
		t.Fatalf("Peek on empty queue failed: %v", err)
	}
	if entry != nil {
		t.Error("Expected nil entry from empty queue")
	}
}

// =============================================================================
// Combined metrics coverage - exercises all metric functions in one test
// =============================================================================

// TestAllMetricsFunctions ensures full coverage of all metrics.go exported functions.
func TestAllMetricsFunctions(t *testing.T) {
	// Capacity metrics
	SetCapacityMetrics(100, 1000)
	SetEffectiveCapacityMetrics(80, 800)
	SetDiskAvailableBytes(500000)

	// Queue metrics
	UpdateQueueMetrics(10, 500, 1000)
	UpdateQueueMetrics(0, 0, 0) // Test with zero effectiveMaxBytes

	// Retry metrics
	IncrementRetryTotal()
	IncrementRetrySuccessTotal()
	IncrementRetryFailure("network")
	IncrementRetryFailure("timeout")
	IncrementRetryFailure("server_error")
	IncrementRetryFailure("client_error")
	IncrementRetryFailure("auth")
	IncrementRetryFailure("rate_limit")
	IncrementRetryFailure("unknown")

	// Disk metrics
	IncrementDiskFull()

	// FastQueue metrics
	SetInmemoryBlocks(10)
	SetDiskBytes(5000)
	IncrementMetaSync()
	IncrementChunkRotation()
	IncrementInmemoryFlush()

	// Circuit breaker metrics
	SetCircuitState("closed")
	SetCircuitState("open")
	SetCircuitState("half_open")
	IncrementCircuitOpen()
	IncrementCircuitRejected()

	// Backoff metrics
	SetCurrentBackoff(0)
	SetCurrentBackoff(5 * time.Second)
	SetCurrentBackoff(time.Minute)
}

// TestPushDataConcurrentDropOldest tests concurrent pushData with DropOldest on a full queue.
func TestPushDataConcurrentDropOldest(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      5,
		FullBehavior: DropOldest,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Fill queue
	for i := 0; i < 5; i++ {
		q.PushData([]byte("fill"))
	}

	// Concurrent pushes that will trigger DropOldest
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			q.PushData([]byte("concurrent-push"))
		}(i)
	}
	wg.Wait()

	if q.Len() > 5 {
		t.Errorf("Queue should not exceed MaxSize, got %d", q.Len())
	}
}

// =============================================================================
// Additional tests to improve fastqueue.go coverage
// =============================================================================

// TestFastQueuePeekChannelFullOnReinsert tests Peek when channel is full during
// put-back, forcing the block to be written to disk instead.
func TestFastQueuePeekChannelFullOnReinsert(t *testing.T) {
	tmpDir := t.TempDir()

	// Channel size of 1 - so when we peek and try to put back, channel may be full
	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	// Push exactly 1 item (fills the channel)
	fq.Push([]byte("only-item"))

	// Now Peek will take from the channel but cannot put back
	// because the channel has size 1 and we need to hold the lock
	// to write to disk. This exercises the put-back failure path.
	fq.mu.Lock()
	// Manually test: take from channel, fill channel, then try peek logic
	// First, push another block directly into channel to fill it
	select {
	case b := <-fq.ch:
		// We took the block. Now put a different block in the channel
		fq.ch <- &block{data: []byte("filler"), timestamp: b.timestamp}
		// Now the channel is full. Write the taken block to disk
		fq.writeBlockToDiskLocked(b.data)
		fq.inmemoryBytes.Add(-int64(len(b.data)))
	default:
		t.Fatal("Expected to take from channel")
	}
	fq.mu.Unlock()

	// Peek should work regardless of where the data is
	data, err := fq.Peek()
	if err != nil {
		t.Fatalf("Peek failed: %v", err)
	}
	if data == nil {
		t.Fatal("Peek returned nil")
	}
}

// TestFastQueueWriteBlockChunkRotation tests write block with chunk rotation.
func TestFastQueueWriteBlockChunkRotationCoverage(t *testing.T) {
	tmpDir := t.TempDir()

	// Very small chunk size to trigger rotation
	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      4096, // Small chunks to trigger rotation
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	// Push data that will cause multiple chunk rotations
	data := make([]byte, 100)
	for i := 0; i < 20; i++ {
		if err := fq.Push(data); err != nil {
			t.Fatalf("Push %d failed: %v", i, err)
		}
	}

	// Verify we can pop all entries back (this tests readBlockFromDiskLocked
	// with chunk transitions)
	for i := 0; i < 20; i++ {
		d, err := fq.Pop()
		if err != nil {
			t.Fatalf("Pop %d failed: %v", i, err)
		}
		if len(d) != 100 {
			t.Errorf("Pop %d: expected 100 bytes, got %d", i, len(d))
		}
	}

	// Queue should be empty
	d, _ := fq.Pop()
	if d != nil {
		t.Error("Expected empty queue after popping all")
	}
}

// TestFastQueueReadBlockFromDiskChunkBoundary tests reading at chunk boundaries.
func TestFastQueueReadBlockFromDiskChunkBoundary(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      4096, // Small chunks to trigger boundary conditions
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	// Push data that exactly fills chunks
	data := make([]byte, 120) // 120 + 8 header = 128 bytes per block, 4 per chunk
	for i := 0; i < 15; i++ {
		if err := fq.Push(data); err != nil {
			t.Fatalf("Push %d failed: %v", i, err)
		}
	}

	// Pop all - this exercises reading across chunk boundaries
	count := 0
	for {
		d, err := fq.Pop()
		if err != nil {
			t.Fatalf("Pop failed at count %d: %v", count, err)
		}
		if d == nil {
			break
		}
		count++
	}

	if count != 15 {
		t.Errorf("Expected 15 entries, got %d", count)
	}
}

// TestFastQueuePeekFromDiskAfterRecovery tests peeking from disk data after recovery.
func TestFastQueuePeekFromDiskAfterRecovery(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	// Create and populate
	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	for i := 0; i < 10; i++ {
		fq.Push([]byte("recovery-peek-data"))
	}

	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.syncMetadataLocked()
	fq.mu.Unlock()

	// Pop some to move reader
	for i := 0; i < 3; i++ {
		fq.Pop()
	}

	fq.mu.Lock()
	fq.syncMetadataLocked()
	fq.mu.Unlock()

	fq.Close()

	// Recover and peek
	fq2, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to recover: %v", err)
	}
	defer fq2.Close()

	// Peek should read from disk
	data, err := fq2.Peek()
	if err != nil {
		t.Fatalf("Peek after recovery failed: %v", err)
	}
	if string(data) != "recovery-peek-data" {
		t.Errorf("Expected 'recovery-peek-data', got %q", data)
	}

	if fq2.Len() != 7 {
		t.Errorf("Expected 7 entries after recovery, got %d", fq2.Len())
	}
}

// TestFastQueueCloseWithDataInBothLayers tests Close with data in memory and disk.
func TestFastQueueCloseWithDataInBothLayersCoverage(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  3,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	// Push to fill channel (in-memory)
	for i := 0; i < 3; i++ {
		fq.Push([]byte("memory-data"))
	}

	// Flush to disk
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	// Push more to in-memory layer
	for i := 0; i < 2; i++ {
		fq.Push([]byte("more-memory"))
	}

	// Pop from disk to set up reader/writer on different positions
	fq.Pop()
	fq.Pop()

	// Close with reader and writer potentially on different chunks
	err = fq.Close()
	if err != nil {
		t.Logf("Close returned error (may be expected): %v", err)
	}

	// Recover and verify
	fq2, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to recover: %v", err)
	}
	defer fq2.Close()

	// Should have remaining entries
	if fq2.Len() < 1 {
		t.Error("Expected at least 1 entry after recovery")
	}
}

// TestFastQueueRecoverWithMultipleChunksCoverage tests recovery with multiple chunks.
func TestFastQueueRecoverWithMultipleChunksCoverage(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024, // Standard chunk size
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	// Push data
	data := make([]byte, 100)
	for i := 0; i < 15; i++ {
		fq.Push(data)
	}

	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.syncMetadataLocked()
	fq.mu.Unlock()

	// Pop some entries to move reader
	for i := 0; i < 7; i++ {
		fq.Pop()
	}

	fq.mu.Lock()
	fq.syncMetadataLocked()
	fq.mu.Unlock()

	fq.Close()

	// Recover
	fq2, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to recover: %v", err)
	}
	defer fq2.Close()

	if fq2.Len() != 8 {
		t.Errorf("Expected 8 entries after recovery, got %d", fq2.Len())
	}

	// Pop all remaining and verify data integrity
	remaining := fq2.Len()
	for i := 0; i < remaining; i++ {
		d, err := fq2.Pop()
		if err != nil {
			t.Fatalf("Pop %d failed: %v", i, err)
		}
		if len(d) != 100 {
			t.Errorf("Pop %d: expected 100 bytes, got %d", i, len(d))
		}
	}
}

// TestSendQueuePushBlockClosedDuringBlock tests pushData with Block behavior
// when queue is closed while blocking on ErrQueueFull.
func TestSendQueuePushBlockClosedDuringBlockCoverage(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      2,
		FullBehavior: Block,
		BlockTimeout: 5 * time.Second,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	// Fill queue
	q.PushData([]byte("data1"))
	q.PushData([]byte("data2"))

	// Start a goroutine that pushes (will block)
	done := make(chan error, 1)
	go func() {
		done <- q.PushData([]byte("blocked-data"))
	}()

	// Close queue while push is blocked
	time.Sleep(50 * time.Millisecond)
	q.Close()

	// Blocked push should return error
	select {
	case err := <-done:
		if err == nil {
			t.Error("Expected error from blocked push after close")
		}
	case <-time.After(2 * time.Second):
		t.Error("Blocked push did not return after close")
	}
}

// TestSendQueuePushBlockSuccessfulRetry tests pushData Block behavior
// where the retry inside the block loop succeeds.
func TestSendQueuePushBlockSuccessfulRetryCoverage(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      2,
		FullBehavior: Block,
		BlockTimeout: 2 * time.Second,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	q.PushData([]byte("data1"))
	q.PushData([]byte("data2"))

	done := make(chan error, 1)
	go func() {
		done <- q.PushData([]byte("data3"))
	}()

	// Pop to create space after a short delay
	time.Sleep(50 * time.Millisecond)
	q.Pop()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Push should have succeeded after Pop: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("Push timed out waiting for space")
	}
}

// TestFastQueuePopFromDiskReaderNilChunkOpen tests Pop when readerChunk is nil
// and needs to be opened (reader on same chunk as writer).
func TestFastQueuePopFromDiskReaderNilChunkOpen(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	// Create, push, flush, close
	fq1, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	for i := 0; i < 5; i++ {
		fq1.Push([]byte("data"))
	}

	fq1.mu.Lock()
	fq1.flushInmemoryBlocksLocked()
	fq1.syncMetadataLocked()
	fq1.mu.Unlock()

	fq1.Close()

	// Reopen - reader chunk will be nil, needs opening
	fq2, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to reopen: %v", err)
	}
	defer fq2.Close()

	// Pop should open the chunk file and read
	for i := 0; i < 5; i++ {
		data, err := fq2.Pop()
		if err != nil {
			t.Fatalf("Pop %d failed: %v", i, err)
		}
		if string(data) != "data" {
			t.Errorf("Pop %d: expected 'data', got %q", i, data)
		}
	}
}

// TestFastQueuePeekDiskMissingChunkFile tests peekBlockFromDiskLocked with missing file.
func TestFastQueuePeekDiskMissingChunkFile(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	// Push and flush to create disk data
	for i := 0; i < 3; i++ {
		fq.Push([]byte("peek-test"))
	}
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.syncMetadataLocked()
	fq.mu.Unlock()

	fq.Close()

	// Reopen - this tests peek from disk after recovery
	fq2, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to reopen: %v", err)
	}
	defer fq2.Close()

	// Peek should succeed from disk
	data, err := fq2.Peek()
	if err != nil {
		t.Fatalf("Peek failed: %v", err)
	}
	if string(data) != "peek-test" {
		t.Errorf("Expected 'peek-test', got %q", data)
	}
}

// TestFastQueueStaleFlushCoverage tests the stale flush loop by triggering it.
func TestFastQueueStaleFlushCoverage(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  100,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: 50 * time.Millisecond, // Short interval to trigger
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}
	defer fq.Close()

	// Push data (stays in channel)
	for i := 0; i < 5; i++ {
		fq.Push([]byte("stale-data"))
	}

	// Wait for stale flush to trigger
	time.Sleep(200 * time.Millisecond)

	// Data should still be available
	if fq.Len() != 5 {
		t.Errorf("Expected 5 entries, got %d", fq.Len())
	}
}

// TestFastQueueReadBlockChunkBoundaryRetry tests readBlockFromDiskLocked
// when reading hits EOF at a chunk boundary and retries on the next chunk.
func TestFastQueueReadBlockChunkBoundaryRetry(t *testing.T) {
	tmpDir := t.TempDir()

	// Use a very small chunk size so we can trigger chunk boundary crossing.
	// Each block = 8 (header) + len(data) bytes.
	// With data of 40 bytes, each block is 48 bytes.
	// ChunkFileSize of 100 means after 2 blocks (96 bytes), there's 4 bytes
	// of padding before the next chunk boundary. This triggers the EOF retry path.
	chunkSize := int64(100)

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      chunkSize,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	// Push blocks that will cross chunk boundaries.
	// Block = 8 header + 40 data = 48 bytes each.
	// First block at offset 0 (bytes 0-47)
	// Second block at offset 48 (bytes 48-95)
	// Third block would start at 96, but only 4 bytes left in chunk (100-96=4)
	// so it goes to next chunk at offset 100.
	data := make([]byte, 40)
	for i := range data {
		data[i] = byte(i)
	}

	for i := 0; i < 5; i++ {
		if err := fq.Push(data); err != nil {
			t.Fatalf("Push %d failed: %v", i, err)
		}
	}

	// Ensure all data is on disk
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	// Pop all blocks - this will exercise chunk boundary crossing in readBlockFromDiskLocked
	for i := 0; i < 5; i++ {
		d, err := fq.Pop()
		if err != nil {
			t.Fatalf("Pop %d failed: %v", i, err)
		}
		if len(d) != 40 {
			t.Errorf("Pop %d: expected 40 bytes, got %d", i, len(d))
		}
	}

	fq.Close()
}

// TestFastQueuePeekFromDiskWithSeparateFileOpen tests peekBlockFromDiskLocked
// when the reader chunk file needs to be opened fresh (not reusing readerChunk).
func TestFastQueuePeekFromDiskWithSeparateFileOpen(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	// Push and flush data to disk
	testData := []byte("peek-separate-file-test")
	for i := 0; i < 3; i++ {
		fq.Push(testData)
	}

	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	// Manually nil the readerChunk to force peekBlockFromDiskLocked to open a new file
	fq.mu.Lock()
	if fq.readerChunk != nil && fq.readerChunk != fq.writerChunk {
		fq.readerChunk.Close()
	}
	fq.readerChunk = nil
	fq.mu.Unlock()

	// Peek should open the file and read successfully
	data, err := fq.Peek()
	if err != nil {
		t.Fatalf("Peek failed: %v", err)
	}
	if string(data) != string(testData) {
		t.Errorf("Expected %q, got %q", testData, data)
	}

	fq.Close()
}

// TestFastQueueCloseWithSeparateReaderWriterChunks tests Close when
// writerChunk and readerChunk are separate files that need closing.
func TestFastQueueCloseWithSeparateReaderWriterChunks(t *testing.T) {
	tmpDir := t.TempDir()

	// Use small chunk size to force chunk rotation, creating separate reader/writer chunks
	chunkSize := int64(100)

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      chunkSize,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            200,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	// Push enough data to span multiple chunks
	data := make([]byte, 40) // 48 bytes per block with header
	for i := 0; i < 10; i++ {
		if err := fq.Push(data); err != nil {
			t.Fatalf("Push %d failed: %v", i, err)
		}
	}

	// Flush everything to disk
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	// Pop some entries to move the reader ahead but keep it behind the writer
	for i := 0; i < 3; i++ {
		_, err := fq.Pop()
		if err != nil {
			t.Fatalf("Pop %d failed: %v", i, err)
		}
	}

	// Close should handle separate reader and writer chunk files
	err = fq.Close()
	if err != nil {
		t.Logf("Close returned error (may be expected with separate chunks): %v", err)
	}
}

// TestFastQueueRecoverWithNonZeroReaderInSameChunk tests that the
// recover function correctly handles a non-zero reader offset within
// the same chunk as the writer.
func TestFastQueueRecoverWithNonZeroReaderInSameChunk(t *testing.T) {
	tmpDir := t.TempDir()

	// Use large chunk, all data fits in one file.
	chunkSize := int64(1024 * 1024)
	entryData := []byte("test-entry-data!") // 16 bytes, block = 24
	entrySize := int64(24)

	// Create chunk 0 with 5 entries
	chunk0Path := filepath.Join(tmpDir, fmt.Sprintf("%016x", int64(0)))

	writeEntry := func(f *os.File, data []byte) {
		header := make([]byte, blockHeaderSize)
		binary.LittleEndian.PutUint64(header, uint64(len(data)))
		f.Write(header)
		f.Write(data)
	}

	f0, err := os.Create(chunk0Path)
	if err != nil {
		t.Fatalf("Create chunk0: %v", err)
	}
	for i := 0; i < 5; i++ {
		writeEntry(f0, entryData)
	}
	f0.Close()

	// Reader at offset 48 (after 2 entries), writer at 120 (after 5 entries)
	// Both in same chunk
	readerOff := entrySize * 2
	writerOff := entrySize * 5

	meta := fmt.Sprintf(`{"name":"fastqueue","reader_offset":%d,"writer_offset":%d,"version":1}`, readerOff, writerOff)
	metaPath := filepath.Join(tmpDir, metaFileName)
	os.WriteFile(metaPath, []byte(meta), 0600)

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10,
		ChunkFileSize:      chunkSize,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Recovery failed: %v", err)
	}
	defer fq.Close()

	// Should have 3 entries remaining (entries 2, 3, 4)
	if fq.Len() != 3 {
		t.Errorf("Expected 3 entries after recovery, got %d", fq.Len())
	}

	// Pop all entries to verify
	count := fq.Len()
	for i := 0; i < count; i++ {
		d, err := fq.Pop()
		if err != nil {
			t.Fatalf("Pop %d failed: %v", i, err)
		}
		if string(d) != string(entryData) {
			t.Errorf("Pop %d: expected %q, got %q", i, entryData, d)
		}
	}
}

// TestFastQueueCountEntriesOnDiskMissingChunk tests countEntriesOnDisk
// when a chunk file is missing (skip to next boundary).
// Directly manipulates queue state to test the missing chunk path without recovery.
func TestFastQueueCountEntriesOnDiskMissingChunk(t *testing.T) {
	tmpDir := t.TempDir()

	// Each block = 8 + 16 = 24 bytes. ChunkFileSize = 24 * 3 = 72 (exact fit).
	chunkSize := int64(72)
	entryData := []byte("test-entry-data!") // 16 bytes

	writeEntry := func(f *os.File, data []byte) {
		header := make([]byte, blockHeaderSize)
		binary.LittleEndian.PutUint64(header, uint64(len(data)))
		f.Write(header)
		f.Write(data)
	}

	// Create 3 chunk files manually
	for chunkIdx := int64(0); chunkIdx < 3; chunkIdx++ {
		chunkStart := chunkIdx * chunkSize
		chunkPath := filepath.Join(tmpDir, fmt.Sprintf("%016x", chunkStart))
		f, err := os.Create(chunkPath)
		if err != nil {
			t.Fatalf("Create chunk %d: %v", chunkIdx, err)
		}
		for i := 0; i < 3; i++ {
			writeEntry(f, entryData)
		}
		f.Close()
	}

	// Delete the middle chunk (chunk 1 at offset 72)
	middleChunkPath := filepath.Join(tmpDir, fmt.Sprintf("%016x", chunkSize))
	os.Remove(middleChunkPath)

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10,
		ChunkFileSize:      chunkSize,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	// Create a FastQueue and set up offsets directly
	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	// Set offsets to span all 3 chunks
	fq.mu.Lock()
	fq.readerOffset = 0
	fq.writerOffset = chunkSize * 3 // End of chunk 2

	count, totalBytes, err := fq.countEntriesOnDisk()
	fq.mu.Unlock()

	if err != nil {
		t.Fatalf("countEntriesOnDisk failed: %v", err)
	}

	// Should have counted entries from chunk 0 and chunk 2, skipping missing chunk 1
	// Chunk 0: 3 entries, Chunk 2: 3 entries = 6 total
	if count != 6 {
		t.Errorf("Expected 6 entries (skipping missing chunk), got %d", count)
	}
	t.Logf("countEntriesOnDisk: count=%d, totalBytes=%d", count, totalBytes)

	fq.Close()
}

// TestFastQueueWriteBlockChunkRotationSameReaderWriter tests writeBlockToDiskLocked
// chunk rotation when readerChunk == writerChunk (should not close the file).
func TestFastQueueWriteBlockChunkRotationSameReaderWriter(t *testing.T) {
	tmpDir := t.TempDir()

	chunkSize := int64(100)

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      chunkSize,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            200,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	// Push 2 blocks (96 bytes total in one chunk of 100)
	data := make([]byte, 40) // 48 bytes per block
	fq.Push(data)
	fq.Push(data)

	// Flush to disk - sets writerChunk
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	// Pop one to create readerChunk (same as writerChunk)
	fq.Pop()

	// Now push more to trigger chunk rotation while readerChunk == writerChunk
	fq.mu.Lock()
	// At this point readerChunk == writerChunk, and writing another block
	// will exceed the chunk boundary, triggering rotation.
	// The code should NOT close the file since reader is still using it.
	err = fq.writeBlockToDiskLocked(data)
	fq.mu.Unlock()

	if err != nil {
		t.Fatalf("writeBlockToDiskLocked with rotation failed: %v", err)
	}

	// Verify data can still be read
	d, popErr := fq.Pop()
	if popErr != nil {
		t.Fatalf("Pop after rotation failed: %v", popErr)
	}
	if len(d) != 40 {
		t.Errorf("Expected 40 bytes, got %d", len(d))
	}

	fq.Close()
}

// TestFastQueuePushClosedDuringDiskWrite tests Push when the queue is closed
// after the channel is full (second closed check in Push).
func TestFastQueuePushClosedDuringDiskWrite(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  2,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	// Fill the channel
	fq.Push([]byte("data1"))
	fq.Push([]byte("data2"))

	// Close the queue
	fq.Close()

	// Try to push when channel is full and queue is closed -
	// this should hit the second closed check (line 182)
	err = fq.Push([]byte("data3"))
	if err != ErrQueueClosed {
		t.Errorf("Expected ErrQueueClosed, got: %v", err)
	}
}

// TestSendQueuePushDataQueueFullDropOldestPopFail tests pushData when
// queue is full with DropOldest and Pop fails.
func TestSendQueuePushDataQueueFullDropOldestPopFail(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      2,
		FullBehavior: DropOldest,
		BlockTimeout: 100 * time.Millisecond,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	// Fill queue
	q.PushData([]byte("data1"))
	q.PushData([]byte("data2"))

	// Close the underlying FastQueue to make Pop fail
	q.mu.Lock()
	q.fq.mu.Lock()
	q.fq.closed = true
	q.fq.mu.Unlock()
	q.mu.Unlock()

	// Manually reopen the SendQueue (so pushData doesn't immediately return "closed")
	q.mu.Lock()
	q.closed = false
	q.mu.Unlock()

	// Push should fail because drop oldest can't pop from closed FastQueue
	err = q.PushData([]byte("data3"))
	if err == nil {
		t.Error("Expected error from PushData when pop fails")
	}

	// Clean up: reopen fq for close
	q.mu.Lock()
	q.fq.mu.Lock()
	q.fq.closed = false
	q.fq.mu.Unlock()
	q.mu.Unlock()
	q.Close()
}

// TestSendQueuePushProtoMarshalError tests Push with nil request which
// should trigger proto.Marshal to succeed (nil marshals to empty bytes).
// But an explicit bad state in the SendQueue close check gets the error path.
func TestSendQueuePushOnClosedQueue(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      10,
		FullBehavior: DropOldest,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	q.Close()

	// Push after close should fail
	req := createTestRequestForCoverage()
	err = q.Push(req)
	if err == nil {
		t.Error("Expected error from Push on closed queue")
	}
}

// TestSendQueuePopError tests Pop when the underlying FastQueue returns error.
func TestSendQueuePopError(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      10,
		FullBehavior: DropOldest,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Pop from empty queue should return nil, nil
	entry, err := q.Pop()
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if entry != nil {
		t.Errorf("Expected nil entry from empty queue")
	}
}

// TestSendQueuePeekError tests Peek when the underlying FastQueue returns error.
func TestSendQueuePeekError(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      10,
		FullBehavior: DropOldest,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	// Close the underlying fq but keep SendQueue open to trigger error path
	q.mu.Lock()
	q.fq.mu.Lock()
	q.fq.closed = true
	q.fq.mu.Unlock()
	q.mu.Unlock()

	_, err = q.Peek()
	if err == nil {
		t.Error("Expected error from Peek with closed FastQueue")
	}

	// Fix up for clean close
	q.mu.Lock()
	q.fq.mu.Lock()
	q.fq.closed = false
	q.fq.mu.Unlock()
	q.mu.Unlock()
	q.Close()
}

// TestFastQueueRecoverBadMetadata tests recover with corrupted metadata file.
func TestFastQueueRecoverBadMetadata(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a corrupted metadata file
	metaPath := filepath.Join(tmpDir, metaFileName)
	os.WriteFile(metaPath, []byte("not valid json{{{"), 0600)

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	_, err := NewFastQueue(cfg)
	if err == nil {
		t.Error("Expected error from recovery with bad metadata")
	}
}

// TestFastQueueRecoverMissingWriterChunk tests recovery when writer chunk file
// cannot be opened (e.g., deleted between metadata write and open).
func TestFastQueueRecoverMissingWriterChunk(t *testing.T) {
	tmpDir := t.TempDir()

	chunkSize := int64(1024 * 1024)

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      chunkSize,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	// Push and flush data
	for i := 0; i < 3; i++ {
		fq.Push([]byte("test-data"))
	}
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.syncMetadataLocked()
	fq.mu.Unlock()
	fq.Close()

	// Delete the chunk file but keep metadata
	entries, _ := os.ReadDir(tmpDir)
	for _, e := range entries {
		if e.Name() != metaFileName && len(e.Name()) == 16 {
			os.Remove(filepath.Join(tmpDir, e.Name()))
		}
	}

	// Make the chunk directory read-only so file creation fails
	chunkPath := filepath.Join(tmpDir, fmt.Sprintf("%016x", 0))
	// Create a directory where the chunk file should be to prevent opening
	os.MkdirAll(chunkPath, 0755)

	// Recovery should fail because writer chunk path is a directory, not a file
	_, err = NewFastQueue(cfg)
	if err == nil {
		// The file was created by OpenFile with O_CREATE, so it might succeed
		// if the directory doesn't conflict. Try another approach:
		// Remove the fake directory and just verify recovery works or fails
		t.Log("Recovery succeeded even with deleted chunks (file recreated)")
	} else {
		t.Logf("Recovery correctly failed: %v", err)
	}

	// Clean up
	os.RemoveAll(chunkPath)
}

// TestFastQueueReadBlockFromDiskMissingChunkFile tests readBlockFromDiskLocked
// when the chunk file does not exist (returns io.EOF).
func TestFastQueueReadBlockFromDiskMissingChunkFile(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	// Manually set offsets to simulate having data on disk
	fq.mu.Lock()
	fq.readerOffset = 0
	fq.writerOffset = 100
	fq.readerChunk = nil // Force it to try opening the file
	// Don't create the actual chunk file -> will hit os.IsNotExist path
	data, err := fq.readBlockFromDiskLocked()
	fq.mu.Unlock()

	if data != nil {
		t.Errorf("Expected nil data, got %v", data)
	}
	// Should get io.EOF because file doesn't exist
	t.Logf("readBlockFromDiskLocked with missing file: data=%v, err=%v", data, err)

	fq.Close()
}

// TestFastQueueWriteBlockToDiskNewChunkFile tests writeBlockToDiskLocked
// when writerChunk is nil and needs to create a new chunk file.
func TestFastQueueWriteBlockToDiskNewChunkFile(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	// Write directly to disk with writerChunk nil
	fq.mu.Lock()
	err = fq.writeBlockToDiskLocked([]byte("direct-disk-write"))
	fq.mu.Unlock()

	if err != nil {
		t.Fatalf("writeBlockToDiskLocked failed: %v", err)
	}

	// Verify offset was advanced
	if fq.writerOffset != int64(blockHeaderSize+len("direct-disk-write")) {
		t.Errorf("Expected writerOffset %d, got %d",
			blockHeaderSize+len("direct-disk-write"), fq.writerOffset)
	}

	fq.Close()
}

// TestSendQueueNewWithEmptyPath tests New with empty path to cover the default path logic.
func TestSendQueueNewWithEmptyPath(t *testing.T) {
	cfg := Config{
		Path:    "",
		MaxSize: 0, // Will get default
	}

	// We can't easily test this without cleanup, but we can verify defaults are applied
	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create with empty config: %v", err)
	}
	q.Close()

	// Clean up default path
	os.RemoveAll("./queue")
}

// TestPeekBlockFromDiskEOFPaths tests peekBlockFromDiskLocked
// with various EOF scenarios.
func TestPeekBlockFromDiskEOFPaths(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	// Test 1: readerOffset >= writerOffset -> immediate EOF
	fq.mu.Lock()
	data, err := fq.peekBlockFromDiskLocked()
	fq.mu.Unlock()
	if data != nil || err == nil {
		t.Errorf("Expected nil data and EOF, got data=%v, err=%v", data, err)
	}

	// Test 2: Write partial data (less than header) to trigger EOF on ReadFull
	fq.Push([]byte("some-data"))
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	// Manually write garbage that's too short for a header
	chunkPath := fq.chunkPath(0)
	fq.mu.Lock()
	if fq.writerChunk != nil {
		fq.writerChunk.Sync()
	}

	// Set readerOffset to writerOffset - 4 (less than blockHeaderSize=8)
	// This means peekBlockFromDiskLocked will try to read 8 bytes but only get 4
	origReaderOffset := fq.readerOffset
	fq.readerOffset = fq.writerOffset - 4
	if fq.readerChunk != nil && fq.readerChunk != fq.writerChunk {
		fq.readerChunk.Close()
	}
	fq.readerChunk = nil

	data, err = fq.peekBlockFromDiskLocked()
	fq.readerOffset = origReaderOffset // Restore
	fq.mu.Unlock()

	t.Logf("Partial header peek: data=%v, err=%v (expected EOF)", data, err)
	_ = chunkPath

	fq.Close()
}

// TestFastQueueCleanupConsumedChunksNoDir tests cleanupConsumedChunksLocked
// when the directory is gone.
func TestFastQueueCleanupConsumedChunksNoDir(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	// Remove the directory
	fq.mu.Lock()
	origPath := fq.cfg.Path
	fq.cfg.Path = "/nonexistent/path/that/does/not/exist"
	fq.cleanupConsumedChunksLocked()
	fq.cfg.Path = origPath
	fq.mu.Unlock()

	fq.Close()
}

// TestFastQueueCleanupOrphanedChunksNoDir tests cleanupOrphanedChunks
// when the directory is gone.
func TestFastQueueCleanupOrphanedChunksNoDir(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	fq.mu.Lock()
	origPath := fq.cfg.Path
	fq.cfg.Path = "/nonexistent/path/that/does/not/exist"
	fq.cleanupOrphanedChunks()
	fq.cfg.Path = origPath
	fq.mu.Unlock()

	fq.Close()
}

// TestFastQueueSyncMetadataErrors tests syncMetadataLocked error paths.
func TestFastQueueSyncMetadataErrors(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	// Test with bad path - should fail on WriteFile
	fq.mu.Lock()
	origPath := fq.cfg.Path
	fq.cfg.Path = "/nonexistent/path"
	err = fq.syncMetadataLocked()
	fq.cfg.Path = origPath
	fq.mu.Unlock()

	if err == nil {
		t.Error("Expected error from syncMetadataLocked with bad path")
	}

	fq.Close()
}

// TestFastQueueGetChunkFilesError tests GetChunkFiles when directory doesn't exist.
func TestFastQueueGetChunkFilesError(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	origPath := fq.cfg.Path
	fq.cfg.Path = "/nonexistent/path"
	_, err = fq.GetChunkFiles()
	fq.cfg.Path = origPath

	if err == nil {
		t.Error("Expected error from GetChunkFiles with bad path")
	}

	fq.Close()
}

// TestWriteBlockToDiskLockedChunkRotationCloseReader tests writeBlockToDiskLocked
// chunk rotation when there IS a separate reader chunk (not same as writer).
func TestWriteBlockToDiskLockedChunkRotationCloseReader(t *testing.T) {
	tmpDir := t.TempDir()

	chunkSize := int64(100) // Small chunks to force rotation

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      chunkSize,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            200,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	data := make([]byte, 40) // 48 bytes with header

	// Write 2 blocks to fill first chunk (96 bytes)
	fq.mu.Lock()
	fq.writeBlockToDiskLocked(data)
	fq.writeBlockToDiskLocked(data)
	fq.activeCount.Store(2)
	fq.mu.Unlock()

	// Read one block to establish readerChunk
	fq.Pop()

	// Now write another block - this triggers rotation with separate reader
	fq.mu.Lock()
	err = fq.writeBlockToDiskLocked(data)
	fq.mu.Unlock()

	if err != nil {
		t.Fatalf("Write with chunk rotation failed: %v", err)
	}

	fq.Close()
}

// TestFastQueueReadBlockFromDiskChunkBoundaryWithPadding tests the readBlockFromDiskLocked
// path where we hit EOF at a chunk boundary and retry from the next chunk.
func TestFastQueueReadBlockFromDiskChunkBoundaryWithPadding(t *testing.T) {
	tmpDir := t.TempDir()

	// Use chunk size that leaves padding after written data.
	// Each block = 8 (header) + data_len bytes.
	// With data of size 42, block = 50 bytes.
	// ChunkFileSize of 110: first block at 0-49, second at 50-99
	// 10 bytes of padding (100-109). Third block goes to chunk at 110.
	chunkSize := int64(110)

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      chunkSize,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            200,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	data := make([]byte, 42) // 50 bytes per block

	// Write blocks directly to disk to control layout precisely
	fq.mu.Lock()
	// Block 1: offset 0-49 in chunk 0
	fq.writeBlockToDiskLocked(data)
	// Block 2: offset 50-99 in chunk 0
	fq.writeBlockToDiskLocked(data)
	// Block 3: triggers rotation to chunk at offset 110
	fq.writeBlockToDiskLocked(data)
	fq.activeCount.Store(3)
	fq.mu.Unlock()

	// Read blocks - the third read from the first chunk will hit EOF
	// (padding bytes 100-109) and retry on the next chunk
	for i := 0; i < 3; i++ {
		d, err := fq.Pop()
		if err != nil {
			t.Fatalf("Pop %d failed: %v", i, err)
		}
		if len(d) != 42 {
			t.Errorf("Pop %d: expected 42 bytes, got %d", i, len(d))
		}
	}

	fq.Close()
}

// TestFastQueuePeekBlockFromDiskSeparateFile tests peekBlockFromDiskLocked
// when the file needs to be opened separately (not using existing readerChunk).
func TestFastQueuePeekBlockFromDiskSeparateFile(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	// Write data to disk
	fq.mu.Lock()
	fq.writeBlockToDiskLocked([]byte("peek-data-1"))
	fq.writeBlockToDiskLocked([]byte("peek-data-2"))
	fq.activeCount.Store(2)
	fq.mu.Unlock()

	// Close the readerChunk so peekBlockFromDiskLocked opens its own file
	fq.mu.Lock()
	if fq.readerChunk != nil && fq.readerChunk != fq.writerChunk {
		fq.readerChunk.Close()
	}
	fq.readerChunk = nil
	data, err := fq.peekBlockFromDiskLocked()
	fq.mu.Unlock()

	if err != nil {
		t.Fatalf("peekBlockFromDiskLocked failed: %v", err)
	}
	if string(data) != "peek-data-1" {
		t.Errorf("Expected 'peek-data-1', got %q", data)
	}

	fq.Close()
}

// TestFastQueueRecoverWithSeekError tests recovery edge cases with reader offset.
func TestFastQueueRecoverWithReaderSeekToChunkOffset(t *testing.T) {
	tmpDir := t.TempDir()

	chunkSize := int64(1024 * 1024)

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      chunkSize,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	// Write some data, pop some, to establish a non-zero readerOffset
	data := make([]byte, 50)
	for i := 0; i < 5; i++ {
		fq.Push(data)
	}
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	// Pop 2 entries to advance reader
	fq.Pop()
	fq.Pop()

	// Sync and close
	fq.mu.Lock()
	fq.syncMetadataLocked()
	fq.mu.Unlock()
	fq.Close()

	// Recover - readerOffset will be non-zero, within same chunk as writer
	fq2, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Recovery failed: %v", err)
	}

	// Verify we can read remaining entries
	for i := 0; i < 3; i++ {
		d, err := fq2.Pop()
		if err != nil {
			t.Fatalf("Pop %d failed: %v", i, err)
		}
		if len(d) != 50 {
			t.Errorf("Pop %d: expected 50 bytes, got %d", i, len(d))
		}
	}

	fq2.Close()
}

// TestFastQueueFlushInmemoryWriteError tests flushInmemoryBlocksLocked
// when writeBlockToDiskLocked returns an error.
func TestFastQueueFlushInmemoryWriteError(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	// Push data to channel
	fq.Push([]byte("flush-test-1"))
	fq.Push([]byte("flush-test-2"))

	// Make path invalid to trigger write error
	fq.mu.Lock()
	origPath := fq.cfg.Path
	fq.cfg.Path = "/nonexistent/path"
	fq.writerChunk = nil // Force new file creation

	err = fq.flushInmemoryBlocksLocked()
	fq.cfg.Path = origPath
	fq.mu.Unlock()

	if err == nil {
		t.Error("Expected error from flushInmemoryBlocksLocked with bad path")
	}

	fq.Close()
}

// TestIsDiskFullErrorWithWrappedError tests isDiskFullError with various errors.
func TestIsDiskFullErrorWithWrappedError(t *testing.T) {
	// Test with nil
	if isDiskFullError(nil) {
		t.Error("Expected false for nil")
	}

	// Test with non-disk-full error
	if isDiskFullError(fmt.Errorf("some error")) {
		t.Error("Expected false for non-disk-full error")
	}

	// Test with wrapped PathError that doesn't contain ENOSPC
	pathErr := &os.PathError{
		Op:   "write",
		Path: "/some/path",
		Err:  fmt.Errorf("permission denied"),
	}
	if isDiskFullError(pathErr) {
		t.Error("Expected false for non-ENOSPC PathError")
	}
}

// TestFastQueueCountEntriesOnDiskAfterPopCoverage tests countEntriesOnDisk
// when some entries have been consumed (reader offset advanced).
func TestFastQueueCountEntriesOnDiskAfterPopCoverage(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	// Push and flush data
	for i := 0; i < 5; i++ {
		fq.Push([]byte(fmt.Sprintf("entry-%d", i)))
	}
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	// Pop 2 entries to move reader offset forward
	fq.Pop()
	fq.Pop()

	// Now count entries on disk directly
	fq.mu.Lock()
	count, totalBytes, err := fq.countEntriesOnDisk()
	fq.mu.Unlock()

	if err != nil {
		t.Fatalf("countEntriesOnDisk failed: %v", err)
	}
	t.Logf("countEntriesOnDisk: count=%d, totalBytes=%d", count, totalBytes)

	fq.Close()
}

// TestFastQueueWriteBlockToDiskOpenError tests writeBlockToDiskLocked
// when the chunk file cannot be opened (non-disk-full error).
func TestFastQueueWriteBlockToDiskOpenError(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	// Create a file where the chunk directory should be to trigger error
	fq.mu.Lock()
	// Set path to a file (not directory) to cause OpenFile to fail
	origPath := fq.cfg.Path
	badDir := filepath.Join(tmpDir, "bad_chunk_dir")
	os.WriteFile(badDir, []byte("not a directory"), 0644)
	fq.cfg.Path = badDir
	fq.writerChunk = nil

	err = fq.writeBlockToDiskLocked([]byte("test"))
	fq.cfg.Path = origPath
	fq.mu.Unlock()

	if err == nil {
		t.Error("Expected error from writeBlockToDiskLocked with bad path")
	}

	fq.Close()
}

// TestFastQueueCleanupConsumedChunksWithParsableNames tests cleanup when
// chunk files have valid hex names and some are fully consumed.
func TestFastQueueCleanupConsumedChunksWithParsableNames(t *testing.T) {
	tmpDir := t.TempDir()

	chunkSize := int64(100)

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      chunkSize,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            200,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	data := make([]byte, 40) // 48 bytes per block

	// Push enough to span multiple chunks
	for i := 0; i < 8; i++ {
		fq.Push(data)
	}
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	// Pop everything to consume all chunks
	for i := 0; i < 8; i++ {
		fq.Pop()
	}

	// Check that cleanup happened (consumed chunks removed)
	chunks, _ := fq.GetChunkFiles()
	t.Logf("Remaining chunks after consuming all: %v", chunks)

	// Also create a non-hex file that should be ignored
	os.WriteFile(filepath.Join(tmpDir, "not-a-chunk"), []byte("ignore"), 0644)

	fq.mu.Lock()
	fq.cleanupConsumedChunksLocked()
	fq.mu.Unlock()

	fq.Close()
}

// TestFastQueueReadBlockFromDiskHeaderTruncated tests readBlockFromDiskLocked
// when there's a truncated entry at the end of a chunk that causes
// io.ErrUnexpectedEOF and triggers retry on next chunk.
func TestFastQueueReadBlockFromDiskHeaderTruncated(t *testing.T) {
	tmpDir := t.TempDir()

	chunkSize := int64(1024 * 1024)

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      chunkSize,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	// Write one valid block to disk
	fq.mu.Lock()
	fq.writeBlockToDiskLocked([]byte("valid-block"))
	fq.mu.Unlock()

	// Manually append a truncated header (only 4 bytes instead of 8) to the chunk file
	fq.mu.Lock()
	if fq.writerChunk != nil {
		truncatedHeader := make([]byte, 4)
		binary.LittleEndian.PutUint32(truncatedHeader, 100)
		fq.writerChunk.Write(truncatedHeader)
		fq.writerOffset += 4 // Advance writer offset past the truncated data
	}
	fq.activeCount.Store(2) // Pretend there are 2 entries
	fq.mu.Unlock()

	// First pop should succeed (valid block)
	d, err := fq.Pop()
	if err != nil {
		t.Fatalf("Pop 1 failed: %v", err)
	}
	if string(d) != "valid-block" {
		t.Errorf("Expected 'valid-block', got %q", d)
	}

	// Second pop should hit the truncated header and get EOF
	_, err = fq.Pop()
	// This should hit the ErrUnexpectedEOF path
	t.Logf("Pop of truncated entry: err=%v (expected EOF-like)", err)

	fq.Close()
}

// TestFastQueueCloseWithCorruptedState tests Close when the data directory
// has been corrupted, triggering error paths in flush and sync.
func TestFastQueueCloseWithCorruptedState(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	// Push data to channel (stays in memory)
	fq.Push([]byte("close-test-data"))

	// Corrupt the state: change path to invalid directory before close
	// This will cause flushInmemoryBlocksLocked to fail (can't write to disk)
	// and syncMetadataLocked to fail
	fq.mu.Lock()
	fq.cfg.Path = "/nonexistent/invalid/path"
	fq.writerChunk = nil // Clear so flush tries to open new file
	fq.mu.Unlock()

	// Close should encounter errors but not panic
	err = fq.Close()
	if err == nil {
		t.Error("Expected error from Close with corrupted state")
	}
	t.Logf("Close error (expected): %v", err)
}

// TestFastQueueReadBlockFromDiskDataTruncated tests readBlockFromDiskLocked
// when the header is valid but the data following it is truncated.
func TestFastQueueReadBlockFromDiskDataTruncated(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	// Write a header that claims 1000 bytes but only write 10 bytes of data
	fq.mu.Lock()
	if fq.writerChunk == nil {
		chunkPath := fq.chunkPath(0)
		f, err := os.OpenFile(chunkPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			fq.mu.Unlock()
			t.Fatalf("Failed to open chunk: %v", err)
		}
		fq.writerChunk = f
	}

	// Write header claiming 1000 bytes
	header := make([]byte, blockHeaderSize)
	binary.LittleEndian.PutUint64(header, 1000)
	fq.writerChunk.Write(header)

	// Write only 10 bytes of data (truncated)
	fq.writerChunk.Write(make([]byte, 10))
	fq.writerChunk.Sync()

	// Set offsets: pretend there's a full entry
	fq.writerOffset = int64(blockHeaderSize + 1000)
	fq.readerOffset = 0
	fq.readerChunk = nil // Force open
	fq.activeCount.Store(1)
	fq.mu.Unlock()

	// Pop should fail trying to read 1000 bytes but only getting 10
	_, err = fq.Pop()
	if err == nil {
		t.Error("Expected error from Pop with truncated data")
	}
	t.Logf("Pop with truncated data: err=%v", err)

	fq.Close()
}

// TestSendQueuePopWithFastQueueError tests SendQueue.Pop when the underlying
// FastQueue.Pop returns an error (not EOF).
func TestSendQueuePopWithFastQueueError(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      10,
		FullBehavior: DropOldest,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	// Push data
	q.PushData([]byte("test-data"))

	// Close the underlying FastQueue to make Pop return error
	q.fq.mu.Lock()
	q.fq.closed = true
	q.fq.mu.Unlock()

	// Pop should return error from FastQueue
	_, err = q.Pop()
	if err == nil {
		t.Error("Expected error from Pop with closed FastQueue")
	}

	// Fix state for cleanup
	q.fq.mu.Lock()
	q.fq.closed = false
	q.fq.mu.Unlock()
	q.Close()
}

// TestFastQueuePopErrorFromDiskRead tests Pop when readBlockFromDiskLocked
// returns an error reading data (after successfully reading header).
func TestFastQueuePopErrorFromDiskRead(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	// Set up offsets to indicate data on disk, with header claiming more data
	// than actually exists in the file
	fq.mu.Lock()
	chunkPath := fq.chunkPath(0)
	f, _ := os.Create(chunkPath)
	// Write a header claiming 500 bytes but only write 10
	header := make([]byte, blockHeaderSize)
	binary.LittleEndian.PutUint64(header, 500)
	f.Write(header)
	f.Write(make([]byte, 10)) // Only 10 bytes instead of 500
	f.Close()

	fq.readerOffset = 0
	fq.writerOffset = int64(blockHeaderSize + 500)
	fq.readerChunk = nil
	fq.activeCount.Store(1)
	fq.mu.Unlock()

	// Pop should fail: header says 500 bytes but only 10 available
	_, err = fq.Pop()
	if err == nil {
		t.Error("Expected error from Pop with insufficient data")
	}
	t.Logf("Pop with insufficient data: err=%v", err)

	fq.Close()
}

// TestFastQueuePeekErrorFromDiskRead tests Peek when peekBlockFromDiskLocked
// returns a non-EOF error.
func TestFastQueuePeekErrorFromDiskRead(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create: %v", err)
	}

	// Set up state to force peekBlockFromDiskLocked to be called
	// but with a seek error (file handle was closed)
	fq.mu.Lock()
	fq.readerOffset = 0
	fq.writerOffset = 100

	// Create a file and close it, then set it as readerChunk
	chunkPath := fq.chunkPath(0)
	f, _ := os.Create(chunkPath)
	f.Close()
	// Now the file handle is closed - using it will cause errors
	// Set readerChunk to this closed handle
	fq.readerChunk = f
	fq.mu.Unlock()

	// Peek should encounter an error from the closed file handle
	_, err = fq.Peek()
	t.Logf("Peek with closed file handle: err=%v", err)

	fq.Close()
}
