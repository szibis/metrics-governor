package queue

import (
	"os"
	"sync"
	"testing"
	"time"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

func createTestRequest(name string) *colmetricspb.ExportMetricsServiceRequest {
	return &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{
								Name: name,
							},
						},
					},
				},
			},
		},
	}
}

func TestNewQueue(t *testing.T) {
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

	if q.Len() != 0 {
		t.Errorf("Expected empty queue, got %d entries", q.Len())
	}
}

func TestPushPop(t *testing.T) {
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

	req := createTestRequest("test-metric")
	if err := q.Push(req); err != nil {
		t.Fatalf("Failed to push: %v", err)
	}

	if q.Len() != 1 {
		t.Errorf("Expected 1 entry, got %d", q.Len())
	}

	entry, err := q.Pop()
	if err != nil {
		t.Fatalf("Failed to pop: %v", err)
	}

	if entry == nil {
		t.Fatal("Expected entry, got nil")
	}

	gotReq, err := entry.GetRequest()
	if err != nil {
		t.Fatalf("Failed to get request: %v", err)
	}

	if len(gotReq.ResourceMetrics) != 1 {
		t.Errorf("Expected 1 resource metric, got %d", len(gotReq.ResourceMetrics))
	}

	if q.Len() != 0 {
		t.Errorf("Expected 0 entries after pop, got %d", q.Len())
	}
}

func TestPeek(t *testing.T) {
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

	req := createTestRequest("test-metric")
	if err := q.Push(req); err != nil {
		t.Fatalf("Failed to push: %v", err)
	}

	entry, err := q.Peek()
	if err != nil {
		t.Fatalf("Failed to peek: %v", err)
	}

	if entry == nil {
		t.Fatal("Expected entry, got nil")
	}

	// Queue should still have the entry
	if q.Len() != 1 {
		t.Errorf("Expected 1 entry after peek, got %d", q.Len())
	}
}

func TestRemove(t *testing.T) {
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

	req := createTestRequest("test-metric")
	if err := q.Push(req); err != nil {
		t.Fatalf("Failed to push: %v", err)
	}

	entry, _ := q.Peek()
	if err := q.Remove(entry.ID); err != nil {
		t.Fatalf("Failed to remove: %v", err)
	}

	if q.Len() != 0 {
		t.Errorf("Expected 0 entries after remove, got %d", q.Len())
	}
}

func TestPersistence(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	// Create queue and push entries
	q1, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	for i := 0; i < 5; i++ {
		req := createTestRequest("test-metric")
		if err := q1.Push(req); err != nil {
			t.Fatalf("Failed to push: %v", err)
		}
	}

	if q1.Len() != 5 {
		t.Errorf("Expected 5 entries, got %d", q1.Len())
	}

	size1 := q1.Size()
	q1.Close()

	// Create new queue from same directory
	q2, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create second queue: %v", err)
	}
	defer q2.Close()

	if q2.Len() != 5 {
		t.Errorf("Expected 5 entries after restart, got %d", q2.Len())
	}

	if q2.Size() != size1 {
		t.Errorf("Expected size %d after restart, got %d", size1, q2.Size())
	}
}

func TestDropOldest(t *testing.T) {
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

	// Push 5 entries with a small delay between each
	for i := 0; i < 5; i++ {
		req := createTestRequest("test-metric")
		if err := q.Push(req); err != nil {
			t.Fatalf("Failed to push: %v", err)
		}
		time.Sleep(1 * time.Millisecond)
	}

	if q.Len() != 3 {
		t.Errorf("Expected 3 entries (max), got %d", q.Len())
	}
}

func TestDropNewest(t *testing.T) {
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

	// Push 5 entries
	for i := 0; i < 5; i++ {
		req := createTestRequest("test-metric")
		if err := q.Push(req); err != nil {
			t.Fatalf("Failed to push: %v", err)
		}
	}

	if q.Len() != 3 {
		t.Errorf("Expected 3 entries (max), got %d", q.Len())
	}
}

func TestBlock(t *testing.T) {
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
	for i := 0; i < 2; i++ {
		req := createTestRequest("test-metric")
		if err := q.Push(req); err != nil {
			t.Fatalf("Failed to push: %v", err)
		}
	}

	// Try to push when full - should timeout
	start := time.Now()
	req := createTestRequest("test-metric")
	err = q.Push(req)
	elapsed := time.Since(start)

	if err == nil {
		t.Error("Expected error when blocking times out")
	}

	if elapsed < 100*time.Millisecond {
		t.Errorf("Expected to block for at least 100ms, blocked for %v", elapsed)
	}
}

func TestBlockWithSpace(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      2,
		FullBehavior: Block,
		BlockTimeout: 1 * time.Second,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Fill the queue
	for i := 0; i < 2; i++ {
		req := createTestRequest("test-metric")
		if err := q.Push(req); err != nil {
			t.Fatalf("Failed to push: %v", err)
		}
	}

	// Pop one entry after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		q.Pop()
	}()

	// Try to push when full - should succeed after pop
	req := createTestRequest("test-metric")
	err = q.Push(req)

	if err != nil {
		t.Errorf("Expected push to succeed after space freed: %v", err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 1000,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	var wg sync.WaitGroup
	pushCount := 100
	popCount := 50

	// Concurrent pushes
	for i := 0; i < pushCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := createTestRequest("test-metric")
			q.Push(req)
		}()
	}

	// Concurrent pops
	for i := 0; i < popCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q.Pop()
		}()
	}

	wg.Wait()

	// Should have pushCount - popCount entries (approximately)
	remaining := q.Len()
	if remaining > pushCount || remaining < 0 {
		t.Errorf("Unexpected queue length: %d", remaining)
	}
}

func TestMaxBytes(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:            tmpDir,
		MaxSize:         1000,
		MaxBytes:        500, // Very small limit
		AdaptiveEnabled: false,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Push several entries
	for i := 0; i < 10; i++ {
		req := createTestRequest("test-metric")
		q.Push(req)
	}

	// Queue size should be limited
	if q.Size() > 600 { // Some overhead for headers
		t.Errorf("Expected queue size <= ~600 (including overhead), got %d", q.Size())
	}
}

func TestEmptyPop(t *testing.T) {
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

	entry, err := q.Pop()
	if err != nil {
		t.Fatalf("Failed to pop from empty queue: %v", err)
	}

	if entry != nil {
		t.Error("Expected nil entry from empty queue")
	}
}

func TestEmptyPeek(t *testing.T) {
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
		t.Fatalf("Failed to peek empty queue: %v", err)
	}

	if entry != nil {
		t.Error("Expected nil entry from empty queue")
	}
}

func TestUpdateRetries(t *testing.T) {
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

	req := createTestRequest("test-metric")
	if err := q.Push(req); err != nil {
		t.Fatalf("Failed to push: %v", err)
	}

	entry, _ := q.Peek()
	if entry.Retries != 0 {
		t.Errorf("Expected 0 retries, got %d", entry.Retries)
	}

	q.UpdateRetries(entry.ID, 5)

	entry2, _ := q.Peek()
	if entry2.Retries != 5 {
		t.Errorf("Expected 5 retries, got %d", entry2.Retries)
	}
}

func TestClosedQueue(t *testing.T) {
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

	req := createTestRequest("test-metric")
	err = q.Push(req)
	if err == nil {
		t.Error("Expected error when pushing to closed queue")
	}
}

func TestOrdering(t *testing.T) {
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

	// Push entries with delay to ensure different timestamps
	for i := 0; i < 5; i++ {
		req := createTestRequest("test-metric")
		if err := q.Push(req); err != nil {
			t.Fatalf("Failed to push: %v", err)
		}
		time.Sleep(1 * time.Millisecond)
	}

	// Pop and verify ordering
	var prevTimestamp time.Time
	for i := 0; i < 5; i++ {
		entry, err := q.Pop()
		if err != nil {
			t.Fatalf("Failed to pop: %v", err)
		}
		if entry == nil {
			t.Fatalf("Expected entry at position %d, got nil", i)
		}
		if !prevTimestamp.IsZero() && entry.Timestamp.Before(prevTimestamp) {
			t.Error("Entries not in FIFO order")
		}
		prevTimestamp = entry.Timestamp
	}
}

func TestWALRecovery(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	// Create queue, push entries, and close
	q1, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	for i := 0; i < 10; i++ {
		req := createTestRequest("test-metric")
		if err := q1.Push(req); err != nil {
			t.Fatalf("Failed to push: %v", err)
		}
	}

	// Pop some entries
	for i := 0; i < 3; i++ {
		q1.Pop()
	}

	originalLen := q1.Len()
	q1.Close()

	// Verify WAL files exist
	if _, err := os.Stat(tmpDir + "/queue.wal"); os.IsNotExist(err) {
		t.Error("WAL file should exist")
	}
	if _, err := os.Stat(tmpDir + "/queue.idx"); os.IsNotExist(err) {
		t.Error("Index file should exist")
	}

	// Recover
	q2, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to recover queue: %v", err)
	}
	defer q2.Close()

	if q2.Len() != originalLen {
		t.Errorf("Expected %d entries after recovery, got %d", originalLen, q2.Len())
	}
}

func TestEffectiveLimits(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:              tmpDir,
		MaxSize:           10000,
		MaxBytes:          1073741824, // 1GB
		AdaptiveEnabled:   true,
		TargetUtilization: 0.85,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Effective limits should be set
	effectiveMaxSize := q.EffectiveMaxSize()
	effectiveMaxBytes := q.EffectiveMaxBytes()

	if effectiveMaxSize <= 0 {
		t.Error("Expected positive effective max size")
	}
	if effectiveMaxBytes <= 0 {
		t.Error("Expected positive effective max bytes")
	}

	// Effective limits should not exceed configured limits
	if effectiveMaxSize > cfg.MaxSize {
		t.Errorf("Effective max size %d exceeds configured %d", effectiveMaxSize, cfg.MaxSize)
	}
	if effectiveMaxBytes > cfg.MaxBytes {
		t.Errorf("Effective max bytes %d exceeds configured %d", effectiveMaxBytes, cfg.MaxBytes)
	}
}

func TestAdaptiveDisabled(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:            tmpDir,
		MaxSize:         100,
		MaxBytes:        10000,
		AdaptiveEnabled: false,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// When adaptive is disabled, effective limits should equal configured limits
	if q.EffectiveMaxSize() != cfg.MaxSize {
		t.Errorf("Expected effective max size %d, got %d", cfg.MaxSize, q.EffectiveMaxSize())
	}
	if q.EffectiveMaxBytes() != cfg.MaxBytes {
		t.Errorf("Expected effective max bytes %d, got %d", cfg.MaxBytes, q.EffectiveMaxBytes())
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.MaxSize != 10000 {
		t.Errorf("Expected default MaxSize 10000, got %d", cfg.MaxSize)
	}
	if cfg.MaxBytes != 1073741824 {
		t.Errorf("Expected default MaxBytes 1GB, got %d", cfg.MaxBytes)
	}
	if cfg.RetryInterval != 5*time.Second {
		t.Errorf("Expected default RetryInterval 5s, got %v", cfg.RetryInterval)
	}
	if cfg.MaxRetryDelay != 5*time.Minute {
		t.Errorf("Expected default MaxRetryDelay 5m, got %v", cfg.MaxRetryDelay)
	}
	if cfg.FullBehavior != DropOldest {
		t.Errorf("Expected default FullBehavior DropOldest, got %v", cfg.FullBehavior)
	}
	if cfg.TargetUtilization != 0.85 {
		t.Errorf("Expected default TargetUtilization 0.85, got %v", cfg.TargetUtilization)
	}
	if !cfg.AdaptiveEnabled {
		t.Error("Expected default AdaptiveEnabled true")
	}
	if cfg.CompactThreshold != 0.5 {
		t.Errorf("Expected default CompactThreshold 0.5, got %v", cfg.CompactThreshold)
	}
}

func TestRemoveNonExistent(t *testing.T) {
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

	// Try to remove non-existent entry
	err = q.Remove("non-existent-id")
	if err == nil {
		t.Error("Expected error when removing non-existent entry")
	}
}

func TestUpdateRetriesNonExistent(t *testing.T) {
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

	// Try to update retries for non-existent entry (UpdateRetries doesn't return error)
	q.UpdateRetries("non-existent-id", 5)
	// If we get here without panic, the test passes
}

func TestClosedQueuePop(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	req := createTestRequest("test-metric")
	q.Push(req)
	q.Close()

	_, err = q.Pop()
	if err == nil {
		t.Error("Expected error when popping from closed queue")
	}
}

func TestClosedQueuePeek(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	req := createTestRequest("test-metric")
	q.Push(req)
	q.Close()

	_, err = q.Peek()
	if err == nil {
		t.Error("Expected error when peeking closed queue")
	}
}

func TestClosedQueueRemove(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	req := createTestRequest("test-metric")
	q.Push(req)
	entry, _ := q.Peek()
	q.Close()

	err = q.Remove(entry.ID)
	if err == nil {
		t.Error("Expected error when removing from closed queue")
	}
}

func TestCompaction(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:             tmpDir,
		MaxSize:          100,
		CompactThreshold: 0.3, // Compact when 30% consumed
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Push entries
	for i := 0; i < 10; i++ {
		req := createTestRequest("test-metric")
		if err := q.Push(req); err != nil {
			t.Fatalf("Failed to push: %v", err)
		}
	}

	initialLen := q.Len()
	if initialLen != 10 {
		t.Errorf("Expected 10 entries after push, got %d", initialLen)
	}

	// Pop most entries to trigger compaction
	for i := 0; i < 8; i++ {
		_, err := q.Pop()
		if err != nil {
			t.Fatalf("Failed to pop: %v", err)
		}
	}

	// Remaining entries should still be accessible
	remaining := q.Len()
	if remaining <= 0 {
		t.Errorf("Expected some remaining entries, got %d", remaining)
	}

	// Pop remaining to verify they're intact
	for i := 0; i < remaining; i++ {
		entry, err := q.Pop()
		if err != nil {
			t.Fatalf("Failed to pop after compaction: %v", err)
		}
		if entry == nil {
			t.Error("Expected entry after compaction")
		}
	}
}

func TestMetricsRegistration(t *testing.T) {
	// Test that metrics functions don't panic
	SetCapacityMetrics(100, 1000)
	SetEffectiveCapacityMetrics(80, 800)
	SetDiskAvailableBytes(1000000)
	UpdateQueueMetrics(5, 500, 50)
	IncrementWALWrite()
	IncrementWALCompact()
	IncrementRetryTotal()
	IncrementRetrySuccessTotal()
	IncrementDiskFull()
}

func TestQueueSizeTracking(t *testing.T) {
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

	initialSize := q.Size()
	if initialSize != 0 {
		t.Errorf("Expected initial size 0, got %d", initialSize)
	}

	req := createTestRequest("test-metric")
	q.Push(req)

	afterPushSize := q.Size()
	if afterPushSize <= 0 {
		t.Error("Expected positive size after push")
	}

	q.Pop()

	afterPopSize := q.Size()
	if afterPopSize != 0 {
		t.Errorf("Expected size 0 after pop, got %d", afterPopSize)
	}
}

func TestInvalidPath(t *testing.T) {
	cfg := Config{
		Path:    "/nonexistent/deeply/nested/path/that/should/not/exist",
		MaxSize: 100,
	}

	_, err := New(cfg)
	if err == nil {
		t.Error("Expected error for invalid path")
	}
}
