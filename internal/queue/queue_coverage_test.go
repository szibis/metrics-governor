package queue

import (
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
