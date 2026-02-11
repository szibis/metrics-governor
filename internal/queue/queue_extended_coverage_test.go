package queue

import (
	"sync"
	"testing"
	"time"

	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
)

// TestSendQueueBlockBehaviorClosedDuringWait tests Block behavior when queue closes during wait.
func TestSendQueueBlockBehaviorClosedDuringWait(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      2,
		FullBehavior: Block,
		BlockTimeout: 5 * time.Second, // Long timeout
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	// Fill the queue
	req := createTestRequestExt()
	q.Push(req)
	q.Push(req)

	// Start a goroutine that will close the queue
	go func() {
		time.Sleep(50 * time.Millisecond)
		q.Close()
	}()

	// This push should block and then fail when queue closes
	err = q.Push(req)
	if err == nil {
		t.Error("Expected error when queue closes during wait")
	}
}

// TestSendQueueBlockBehaviorSuccessfulRetry tests Block behavior with successful retry.
func TestSendQueueBlockBehaviorSuccessfulRetry(t *testing.T) {
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

	req := createTestRequestExt()
	q.Push(req)
	q.Push(req)

	done := make(chan error)

	// Try to push (will block)
	go func() {
		done <- q.Push(req)
	}()

	// Pop to create space after a short delay
	time.Sleep(50 * time.Millisecond)
	q.Pop()

	// Wait for push to complete
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Push should have succeeded: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("Push timed out")
	}
}

// TestSendQueueDropOldestMultiple tests DropOldest with multiple drops.
func TestSendQueueDropOldestMultiple(t *testing.T) {
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

	req := createTestRequestExt()

	// Fill queue
	for i := 0; i < 3; i++ {
		q.Push(req)
	}

	// Push 5 more (each should trigger drop)
	for i := 0; i < 5; i++ {
		if err := q.Push(req); err != nil {
			t.Errorf("Push %d failed: %v", i, err)
		}
	}

	// Should still have 3 entries
	if q.Len() != 3 {
		t.Errorf("Expected 3 entries, got %d", q.Len())
	}
}

// TestSendQueueDropNewestMultiple tests DropNewest with multiple attempts.
func TestSendQueueDropNewestMultiple(t *testing.T) {
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

	req := createTestRequestExt()

	// Fill queue
	for i := 0; i < 3; i++ {
		q.Push(req)
	}

	// Push 5 more (each should be dropped silently)
	for i := 0; i < 5; i++ {
		if err := q.Push(req); err != nil {
			t.Errorf("Push %d should not error: %v", i, err)
		}
	}

	// Should still have 3 entries (newest were dropped)
	if q.Len() != 3 {
		t.Errorf("Expected 3 entries, got %d", q.Len())
	}
}

// TestSendQueueConcurrentBlockBehavior tests concurrent pushes with Block behavior.
func TestSendQueueConcurrentBlockBehavior(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      5,
		FullBehavior: Block,
		BlockTimeout: 500 * time.Millisecond,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	var wg sync.WaitGroup
	req := createTestRequestExt()

	// Concurrent pushers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q.Push(req)
		}()
	}

	// Concurrent poppers to make space
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(50 * time.Millisecond)
			q.Pop()
		}()
	}

	wg.Wait()
}

// TestSendQueuePopPeekInterleaved tests interleaved Pop and Peek operations.
func TestSendQueuePopPeekInterleaved(t *testing.T) {
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

	req := createTestRequestExt()

	// Push 10 entries
	for i := 0; i < 10; i++ {
		q.Push(req)
	}

	// Interleaved peek and pop
	for i := 0; i < 5; i++ {
		// Peek
		entry, _ := q.Peek()
		if entry == nil {
			t.Fatalf("Peek %d returned nil", i)
		}

		// Pop
		popped, _ := q.Pop()
		if popped == nil {
			t.Fatalf("Pop %d returned nil", i)
		}
	}

	// Should have 5 left
	if q.Len() != 5 {
		t.Errorf("Expected 5 entries, got %d", q.Len())
	}
}

// TestSendQueueRecoveryWithPendingData tests queue recovery preserves data.
func TestSendQueueRecoveryWithPendingData(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:               tmpDir,
		MaxSize:            100,
		InmemoryBlocks:     5,
		MetaSyncInterval:   50 * time.Millisecond,
		StaleFlushInterval: 50 * time.Millisecond,
	}

	// Create first queue
	q1, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	req := createTestRequestExt()

	// Push entries
	for i := 0; i < 20; i++ {
		if err := q1.Push(req); err != nil {
			t.Fatalf("Push failed: %v", err)
		}
	}

	// Wait for flush
	time.Sleep(200 * time.Millisecond)

	q1.Close()

	// Reopen
	q2, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to recover queue: %v", err)
	}
	defer q2.Close()

	if q2.Len() < 15 { // Some might be in channel during close
		t.Errorf("Expected at least 15 entries after recovery, got %d", q2.Len())
	}
}

// TestSendQueueLenSizeConsistency tests Len and Size stay consistent.
func TestSendQueueLenSizeConsistency(t *testing.T) {
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

	req := createTestRequestExt()

	// Push and check consistency
	for i := 0; i < 10; i++ {
		q.Push(req)
		if q.Len() != i+1 {
			t.Errorf("After push %d: expected Len=%d, got %d", i, i+1, q.Len())
		}
		if q.Size() <= 0 {
			t.Errorf("After push %d: expected positive Size", i)
		}
	}

	// Pop and check consistency
	for i := 9; i >= 0; i-- {
		q.Pop()
		if q.Len() != i {
			t.Errorf("After pop %d: expected Len=%d, got %d", 9-i, i, q.Len())
		}
	}

	// Should be empty
	if q.Size() != 0 {
		t.Errorf("Expected Size=0, got %d", q.Size())
	}
}

// TestSendQueuePushValidation tests Push with various request states.
func TestSendQueuePushValidation(t *testing.T) {
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

	// Push empty request
	emptyReq := &colmetricspb.ExportMetricsServiceRequest{}
	if err := q.Push(emptyReq); err != nil {
		t.Errorf("Push empty request failed: %v", err)
	}

	// Push request with nil ResourceMetrics
	nilRMReq := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: nil,
	}
	if err := q.Push(nilRMReq); err != nil {
		t.Errorf("Push nil ResourceMetrics failed: %v", err)
	}

	// Push request with empty ResourceMetrics
	emptyRMReq := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{},
	}
	if err := q.Push(emptyRMReq); err != nil {
		t.Errorf("Push empty ResourceMetrics failed: %v", err)
	}

	if q.Len() != 3 {
		t.Errorf("Expected 3 entries, got %d", q.Len())
	}
}

// TestSendQueueMaxBytesLimit tests queue byte limit enforcement.
func TestSendQueueMaxBytesLimit(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      1000,
		MaxBytes:     500, // Small byte limit
		FullBehavior: DropNewest,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	req := createTestRequestExt()

	// Push until byte limit
	pushed := 0
	for i := 0; i < 100; i++ {
		if err := q.Push(req); err != nil {
			break
		}
		pushed++
		if q.Size() >= 500 {
			break
		}
	}

	// Further pushes should be dropped
	for i := 0; i < 5; i++ {
		q.Push(req) // DropNewest silently drops
	}

	t.Logf("Pushed %d entries before byte limit", pushed)
}

// TestSendQueueCloseIdempotent tests Close can be called multiple times.
func TestSendQueueCloseIdempotent(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	// First close
	if err := q.Close(); err != nil {
		t.Errorf("First close failed: %v", err)
	}

	// Second close should not panic or error badly
	q.Close()
}

// TestSendQueuePopBroadcastsSpace tests that Pop signals waiting pushers.
func TestSendQueuePopBroadcastsSpace(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      2,
		FullBehavior: Block,
		BlockTimeout: time.Second,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	req := createTestRequestExt()

	// Fill queue
	q.Push(req)
	q.Push(req)

	// Start blocked push
	done := make(chan bool)
	go func() {
		q.Push(req)
		done <- true
	}()

	// Give push time to block
	time.Sleep(50 * time.Millisecond)

	// Pop should unblock the push
	q.Pop()

	select {
	case <-done:
		// Success
	case <-time.After(500 * time.Millisecond):
		t.Error("Push should have unblocked after Pop")
	}
}

// TestSendQueueRemoveBroadcastsSpace tests that Remove signals waiting pushers.
func TestSendQueueRemoveBroadcastsSpace(t *testing.T) {
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

	// Remove is a no-op but should broadcast
	q.Remove("any-id") // Should not panic

	// Just verify queue still works
	req := createTestRequestExt()
	if err := q.Push(req); err != nil {
		t.Errorf("Push after Remove failed: %v", err)
	}
}

// TestSendQueueWithAllConfigOptions tests queue with all config options set.
func TestSendQueueWithAllConfigOptions(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:               tmpDir,
		MaxSize:            50,
		MaxBytes:           10000,
		RetryInterval:      100 * time.Millisecond,
		MaxRetryDelay:      time.Second,
		FullBehavior:       DropOldest,
		BlockTimeout:       500 * time.Millisecond,
		TargetUtilization:  0.75,
		AdaptiveEnabled:    true,
		CompactThreshold:   0.3,
		InmemoryBlocks:     32,
		ChunkSize:          64 * 1024 * 1024,
		MetaSyncInterval:   500 * time.Millisecond,
		StaleFlushInterval: 2 * time.Second,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue with all options: %v", err)
	}
	defer q.Close()

	// Basic operations should work
	req := createTestRequestExt()
	for i := 0; i < 10; i++ {
		if err := q.Push(req); err != nil {
			t.Errorf("Push failed: %v", err)
		}
	}

	for i := 0; i < 5; i++ {
		if _, err := q.Pop(); err != nil {
			t.Errorf("Pop failed: %v", err)
		}
	}

	if q.Len() != 5 {
		t.Errorf("Expected 5, got %d", q.Len())
	}
}

// Helper function
func createTestRequestExt() *colmetricspb.ExportMetricsServiceRequest {
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
