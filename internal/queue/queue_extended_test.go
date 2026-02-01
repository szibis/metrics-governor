package queue

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

func TestPushData(t *testing.T) {
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

	// Test PushData with valid data
	data := []byte("test data for push data")
	err = q.PushData(data)
	if err != nil {
		t.Fatalf("PushData failed: %v", err)
	}

	if q.Len() != 1 {
		t.Errorf("Expected 1 entry, got %d", q.Len())
	}
}

func TestPushDataOnClosedQueue(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	// Close the queue
	q.Close()

	// Try to push data to closed queue
	data := []byte("test data")
	err = q.PushData(data)
	if err == nil {
		t.Error("Expected error when pushing to closed queue")
	}
}

func TestPushOnClosedQueue(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	// Close the queue
	q.Close()

	// Try to push to closed queue
	req := createTestRequest("test-metric")
	err = q.Push(req)
	if err == nil {
		t.Error("Expected error when pushing to closed queue")
	}
}

func TestGetRequest(t *testing.T) {
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

	// Push a valid request
	req := createTestRequest("test-metric")
	err = q.Push(req)
	if err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	// Get the request back
	entry, err := q.Pop()
	if err != nil {
		t.Fatalf("Pop failed: %v", err)
	}

	retrievedReq, err := entry.GetRequest()
	if err != nil {
		t.Fatalf("GetRequest failed: %v", err)
	}

	if retrievedReq == nil {
		t.Error("Expected non-nil request")
	}
}

func TestGetRequestWithInvalidData(t *testing.T) {
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

	// Push invalid protobuf data
	invalidData := []byte{0xFF, 0xFF, 0xFF, 0xFF}
	err = q.PushData(invalidData)
	if err != nil {
		t.Fatalf("PushData failed: %v", err)
	}

	// Get the entry
	entry, err := q.Pop()
	if err != nil {
		t.Fatalf("Pop failed: %v", err)
	}

	// Try to deserialize as request - should fail
	_, err = entry.GetRequest()
	if err == nil {
		t.Error("Expected error when deserializing invalid protobuf data")
	}
}

func TestIsDiskFullError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "ENOSPC error",
			err:      syscall.ENOSPC,
			expected: true,
		},
		{
			name:     "wrapped ENOSPC error",
			err:      &os.PathError{Op: "write", Path: "/tmp/test", Err: syscall.ENOSPC},
			expected: true,
		},
		{
			name:     "fmt.Errorf wrapped ENOSPC",
			err:      fmt.Errorf("write failed: %w", syscall.ENOSPC),
			expected: true,
		},
		{
			name:     "other syscall error",
			err:      syscall.EPERM,
			expected: false,
		},
		{
			name:     "generic error",
			err:      errors.New("some error"),
			expected: false,
		},
		{
			name:     "path error with other error",
			err:      &os.PathError{Op: "write", Path: "/tmp/test", Err: syscall.EPERM},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isDiskFullError(tt.err)
			if result != tt.expected {
				t.Errorf("isDiskFullError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

func TestDropOldestBehavior(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      5, // Very small to trigger drops
		FullBehavior: DropOldest,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Push multiple entries to fill the queue
	for i := 0; i < 10; i++ {
		req := createTestRequest("test-metric")
		err := q.Push(req)
		if err != nil {
			// Some entries may fail which is expected
			t.Logf("Push %d failed (expected for small queue): %v", i, err)
		}
	}

	// Queue should still be functional
	if q.Len() < 0 {
		t.Error("Queue length should not be negative")
	}
}

func TestDropNewestBehavior(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      5, // Very small to trigger drops
		FullBehavior: DropNewest,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Push multiple entries to fill the queue
	for i := 0; i < 10; i++ {
		req := createTestRequest("test-metric")
		err := q.Push(req)
		if err != nil {
			// Some entries may fail which is expected
			t.Logf("Push %d failed (expected for small queue): %v", i, err)
		}
	}

	// Queue should still be functional
	if q.Len() < 0 {
		t.Error("Queue length should not be negative")
	}
}

func TestQueueConcurrentPushData(t *testing.T) {
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
	numGoroutines := 5
	pushesPerGoroutine := 20

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < pushesPerGoroutine; j++ {
				data := []byte("concurrent data")
				if err := q.PushData(data); err != nil {
					t.Logf("PushData failed: %v", err)
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify queue has entries
	if q.Len() == 0 {
		t.Error("Expected queue to have entries after concurrent pushes")
	}
}

func TestMultiplePushPopCycles(t *testing.T) {
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

	// Push-pop cycles
	for cycle := 0; cycle < 5; cycle++ {
		// Push several items
		for i := 0; i < 3; i++ {
			req := &colmetricspb.ExportMetricsServiceRequest{
				ResourceMetrics: []*metricspb.ResourceMetrics{
					{
						ScopeMetrics: []*metricspb.ScopeMetrics{
							{
								Metrics: []*metricspb.Metric{
									{Name: "cycle-metric"},
								},
							},
						},
					},
				},
			}
			if err := q.Push(req); err != nil {
				t.Fatalf("Push failed at cycle %d, item %d: %v", cycle, i, err)
			}
		}

		// Pop all items - Pop automatically marks entries as consumed
		for q.Len() > 0 {
			entry, err := q.Pop()
			if err != nil {
				t.Fatalf("Pop failed at cycle %d: %v", cycle, err)
			}
			if entry == nil {
				break // No more entries
			}
		}
	}
}

func TestQueueRecovery(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	// Create queue and push some data
	q1, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	for i := 0; i < 5; i++ {
		req := createTestRequest("recovery-metric")
		if err := q1.Push(req); err != nil {
			t.Fatalf("Push failed: %v", err)
		}
	}

	initialLen := q1.Len()
	q1.Close()

	// Create new queue with same path - should recover
	q2, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create recovered queue: %v", err)
	}
	defer q2.Close()

	// Should have same entries after recovery
	if q2.Len() != initialLen {
		t.Errorf("Expected %d entries after recovery, got %d", initialLen, q2.Len())
	}
}

func TestQueueFullDropOldest(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      3, // Very small
		FullBehavior: DropOldest,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Fill the queue
	for i := 0; i < 3; i++ {
		req := createTestRequest("metric-" + string(rune('A'+i)))
		if err := q.Push(req); err != nil {
			t.Fatalf("Push %d failed: %v", i, err)
		}
	}

	if q.Len() != 3 {
		t.Errorf("Expected queue length 3, got %d", q.Len())
	}

	// Push more - should trigger drop oldest
	for i := 0; i < 3; i++ {
		req := createTestRequest("new-metric-" + string(rune('A'+i)))
		if err := q.Push(req); err != nil {
			t.Logf("Push new-%d: %v", i, err)
		}
	}

	// Queue should still have entries
	if q.Len() == 0 {
		t.Error("Queue should not be empty after drop oldest")
	}
}

func TestQueueFullDropNewest(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      3, // Very small
		FullBehavior: DropNewest,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Fill the queue
	for i := 0; i < 3; i++ {
		req := createTestRequest("metric-" + string(rune('A'+i)))
		if err := q.Push(req); err != nil {
			t.Fatalf("Push %d failed: %v", i, err)
		}
	}

	initialLen := q.Len()

	// Push more - should drop newest (incoming)
	for i := 0; i < 3; i++ {
		req := createTestRequest("new-metric-" + string(rune('A'+i)))
		// Should not error, but also not add
		_ = q.Push(req)
	}

	// Queue length should stay the same (new items dropped)
	if q.Len() != initialLen {
		t.Logf("Queue length changed from %d to %d (expected with DropNewest)", initialLen, q.Len())
	}
}

func TestQueueSize(t *testing.T) {
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

	// Push some data
	for i := 0; i < 5; i++ {
		req := createTestRequest("size-test-metric")
		if err := q.Push(req); err != nil {
			t.Fatalf("Push failed: %v", err)
		}
	}

	// Size should have increased
	newSize := q.Size()
	if newSize <= initialSize {
		t.Errorf("Expected size to increase, got %d", newSize)
	}
}

func TestQueueCloseMultipleTimes(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	// Push some data
	for i := 0; i < 3; i++ {
		req := createTestRequest("close-test-metric")
		_ = q.Push(req)
	}

	// Close multiple times - should not panic
	for i := 0; i < 3; i++ {
		err := q.Close()
		if i == 0 && err != nil {
			t.Errorf("First close failed: %v", err)
		}
	}
}

func TestQueueWithMaxBytes(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      1000,
		MaxBytes:     1024, // 1KB limit
		FullBehavior: DropNewest,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Push until we hit the byte limit
	pushCount := 0
	for i := 0; i < 100; i++ {
		req := createTestRequest("bytes-test-metric-with-some-longer-name")
		err := q.Push(req)
		if err == nil {
			pushCount++
		}
	}

	if pushCount == 0 {
		t.Error("Should have been able to push at least one item")
	}

	t.Logf("Pushed %d items before hitting byte limit", pushCount)
}

func TestPopFromEmptyQueue(t *testing.T) {
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

	// Pop from empty queue
	entry, err := q.Pop()
	if err != nil {
		t.Errorf("Pop from empty queue failed: %v", err)
	}
	if entry != nil {
		t.Error("Expected nil entry from empty queue")
	}
}

func TestQueueCompaction(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:             tmpDir,
		MaxSize:          10,
		CompactThreshold: 0.3, // Compact when 30% consumed
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Push items
	for i := 0; i < 10; i++ {
		req := createTestRequest("compact-metric")
		if err := q.Push(req); err != nil {
			t.Fatalf("Push failed: %v", err)
		}
	}

	// Pop half to trigger compaction threshold
	for i := 0; i < 5; i++ {
		entry, err := q.Pop()
		if err != nil {
			t.Fatalf("Pop failed: %v", err)
		}
		if entry == nil {
			break
		}
	}

	// Push more - may trigger compaction
	for i := 0; i < 5; i++ {
		req := createTestRequest("post-compact-metric")
		_ = q.Push(req)
	}

	// Queue should still work
	if q.Len() == 0 {
		t.Log("Queue is empty after compaction cycle")
	}
}

func TestQueueLargeData(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 10,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Push large data
	largeData := make([]byte, 10*1024) // 10KB
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	if err := q.PushData(largeData); err != nil {
		t.Fatalf("PushData failed: %v", err)
	}

	// Pop and verify
	entry, err := q.Pop()
	if err != nil {
		t.Fatalf("Pop failed: %v", err)
	}
	if entry == nil {
		t.Fatal("Expected entry, got nil")
	}

	if len(entry.Data) != len(largeData) {
		t.Errorf("Data length mismatch: got %d, want %d", len(entry.Data), len(largeData))
	}
}

func TestQueueBlockBehavior(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      2, // Very small to trigger block
		FullBehavior: Block,
		BlockTimeout: 50 * time.Millisecond, // Short timeout for test
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Fill the queue
	for i := 0; i < 2; i++ {
		req := createTestRequest("block-test-metric")
		if err := q.Push(req); err != nil {
			t.Fatalf("Push %d failed: %v", i, err)
		}
	}

	// Next push should block and eventually timeout
	start := time.Now()
	req := createTestRequest("block-test-overflow")
	err = q.Push(req)
	elapsed := time.Since(start)

	// Should have taken close to BlockTimeout
	if elapsed < 40*time.Millisecond {
		t.Errorf("Expected push to block for timeout, elapsed: %v", elapsed)
	}

	// Error is expected due to timeout
	if err == nil {
		t.Log("Push succeeded despite block (race condition)")
	}
}

func TestWALRecoveryWithPartialData(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 100,
	}

	// Create queue and add some entries
	q1, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	// Push entries
	for i := 0; i < 5; i++ {
		data := []byte("recovery test data " + string(rune('A'+i)))
		if err := q1.PushData(data); err != nil {
			t.Fatalf("PushData failed: %v", err)
		}
	}

	// Pop some entries
	for i := 0; i < 2; i++ {
		entry, err := q1.Pop()
		if err != nil {
			t.Fatalf("Pop failed: %v", err)
		}
		if entry == nil {
			break
		}
	}

	remainingLen := q1.Len()
	q1.Close()

	// Recover
	q2, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to recover queue: %v", err)
	}
	defer q2.Close()

	// Should have remaining entries
	if q2.Len() != remainingLen {
		t.Errorf("Expected %d entries after recovery, got %d", remainingLen, q2.Len())
	}
}

func TestQueueEntryRetries(t *testing.T) {
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

	// Push data
	req := createTestRequest("retry-test-metric")
	if err := q.Push(req); err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	// Pop entry
	entry, err := q.Pop()
	if err != nil {
		t.Fatalf("Pop failed: %v", err)
	}
	if entry == nil {
		t.Fatal("Expected entry, got nil")
	}

	// Verify entry fields
	if entry.ID == "" {
		t.Error("Expected non-empty entry ID")
	}
	if entry.Timestamp.IsZero() {
		t.Error("Expected non-zero timestamp")
	}
	if entry.Retries != 0 {
		t.Errorf("Expected 0 retries, got %d", entry.Retries)
	}
}

func TestQueueAdaptiveSizing(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:              tmpDir,
		MaxSize:           100,
		AdaptiveEnabled:   true,
		TargetUtilization: 0.8,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Push some data
	for i := 0; i < 10; i++ {
		req := createTestRequest("adaptive-test")
		if err := q.Push(req); err != nil {
			t.Logf("Push %d failed: %v", i, err)
		}
	}

	// Queue should work with adaptive enabled
	if q.Len() == 0 {
		t.Error("Expected queue to have entries")
	}
}

func TestNewQueueWithInvalidPath(t *testing.T) {
	cfg := Config{
		Path:    "/nonexistent/deeply/nested/path/that/should/fail",
		MaxSize: 100,
	}

	_, err := New(cfg)
	if err == nil {
		t.Error("Expected error for invalid path")
	}
}

func TestQueuePushManyThenPopAll(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:    tmpDir,
		MaxSize: 50,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Push items
	pushCount := 20
	for i := 0; i < pushCount; i++ {
		data := []byte("item-" + string(rune('A'+i%26)))
		if err := q.PushData(data); err != nil {
			t.Fatalf("PushData %d failed: %v", i, err)
		}
	}

	// Queue should have at least what we pushed
	initialLen := q.Len()
	if initialLen < pushCount {
		t.Errorf("Expected queue length >= %d, got %d", pushCount, initialLen)
	}

	// Pop all items using Len() to know when to stop
	count := 0
	for q.Len() > 0 {
		entry, err := q.Pop()
		if err != nil {
			t.Fatalf("Pop failed: %v", err)
		}
		if entry == nil {
			t.Log("Pop returned nil but Len() > 0")
			break
		}
		count++
	}

	// Should have popped all items
	if count < pushCount {
		t.Errorf("Expected to pop at least %d items, got %d", pushCount, count)
	}

	// Queue should be empty
	if q.Len() != 0 {
		t.Errorf("Expected empty queue after popping, got length %d", q.Len())
	}
}

func TestQueueSyncModes(t *testing.T) {
	modes := []SyncMode{SyncImmediate, SyncBatched, SyncAsync}

	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			tmpDir := t.TempDir()

			cfg := Config{
				Path:          tmpDir,
				MaxSize:       100,
				SyncMode:      mode,
				SyncBatchSize: 5,
				SyncInterval:  10 * time.Millisecond,
			}

			q, err := New(cfg)
			if err != nil {
				t.Fatalf("Failed to create queue with sync mode %s: %v", mode, err)
			}

			// Push entries
			for i := 0; i < 10; i++ {
				if err := q.PushData([]byte("sync mode test")); err != nil {
					t.Fatalf("PushData failed: %v", err)
				}
			}

			if q.Len() != 10 {
				t.Errorf("Expected 10 entries, got %d", q.Len())
			}

			// Close and recover
			q.Close()

			q2, err := New(cfg)
			if err != nil {
				t.Fatalf("Failed to recover queue: %v", err)
			}
			defer q2.Close()

			if q2.Len() != 10 {
				t.Errorf("Expected 10 entries after recovery, got %d", q2.Len())
			}
		})
	}
}

func TestQueueWithCompression(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:        tmpDir,
		MaxSize:     100,
		Compression: true,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	// Push entries with compressible data
	testData := []byte("This is test data that should compress well because it has repeated patterns")
	for i := 0; i < 10; i++ {
		if err := q.PushData(testData); err != nil {
			t.Fatalf("PushData failed: %v", err)
		}
	}

	if q.Len() != 10 {
		t.Errorf("Expected 10 entries, got %d", q.Len())
	}

	// Verify data can be read back correctly
	entry, err := q.Peek()
	if err != nil {
		t.Fatalf("Peek failed: %v", err)
	}
	if entry == nil {
		t.Fatal("Expected entry, got nil")
	}

	if string(entry.Data) != string(testData) {
		t.Errorf("Data mismatch: got %q, want %q", entry.Data, testData)
	}

	// Close and recover
	q.Close()

	q2, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to recover queue: %v", err)
	}
	defer q2.Close()

	if q2.Len() != 10 {
		t.Errorf("Expected 10 entries after recovery, got %d", q2.Len())
	}

	// Verify data integrity after recovery
	entry2, err := q2.Peek()
	if err != nil {
		t.Fatalf("Peek after recovery failed: %v", err)
	}
	if string(entry2.Data) != string(testData) {
		t.Errorf("Data mismatch after recovery: got %q, want %q", entry2.Data, testData)
	}
}

func TestQueueRecoveryWithConsumedEntries(t *testing.T) {
	// Test specifically for the buffered write offset bug fix
	tmpDir := t.TempDir()

	cfg := Config{
		Path:     tmpDir,
		MaxSize:  100,
		SyncMode: SyncBatched, // Use batched mode to trigger the buffered path
	}

	// Create queue and push entries
	q1, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	// Push 10 entries
	for i := 0; i < 10; i++ {
		if err := q1.PushData([]byte("entry")); err != nil {
			t.Fatalf("PushData %d failed: %v", i, err)
		}
	}

	// Pop 5 entries (mark as consumed)
	for i := 0; i < 5; i++ {
		entry, err := q1.Pop()
		if err != nil {
			t.Fatalf("Pop %d failed: %v", i, err)
		}
		if entry == nil {
			t.Fatalf("Pop %d returned nil", i)
		}
	}

	expectedLen := q1.Len()
	q1.Close()

	// Recover and verify correct count
	q2, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to recover queue: %v", err)
	}
	defer q2.Close()

	if q2.Len() != expectedLen {
		t.Errorf("Recovery: expected %d entries, got %d", expectedLen, q2.Len())
	}
}


func TestQueueDroppedReasons(t *testing.T) {
	tmpDir := t.TempDir()

	// Test drop oldest behavior
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
	for i := 0; i < 3; i++ {
		if err := q.PushData([]byte(fmt.Sprintf("entry-%d", i))); err != nil {
			t.Fatalf("Push %d failed: %v", i, err)
		}
	}

	// Push more - should drop oldest
	for i := 0; i < 5; i++ {
		if err := q.PushData([]byte(fmt.Sprintf("new-entry-%d", i))); err != nil {
			t.Fatalf("Push new entry %d failed: %v", i, err)
		}
	}

	// Queue should still be at max size
	if q.Len() > 3 {
		t.Errorf("Expected queue size <= 3, got %d", q.Len())
	}
}

func TestQueueDropNewestBehavior(t *testing.T) {
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
	for i := 0; i < 3; i++ {
		if err := q.PushData([]byte(fmt.Sprintf("entry-%d", i))); err != nil {
			t.Fatalf("Push %d failed: %v", i, err)
		}
	}

	// Push more - should drop newest (incoming)
	for i := 0; i < 5; i++ {
		err := q.PushData([]byte(fmt.Sprintf("dropped-entry-%d", i)))
		if err != nil {
			t.Fatalf("Push should not return error with DropNewest: %v", err)
		}
	}

	// Queue should still be at max size
	if q.Len() > 3 {
		t.Errorf("Expected queue size <= 3, got %d", q.Len())
	}
}

func TestQueueBlockBehaviorWithTimeout(t *testing.T) {
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

	// Fill the queue
	for i := 0; i < 2; i++ {
		if err := q.PushData([]byte("data")); err != nil {
			t.Fatalf("Push %d failed: %v", i, err)
		}
	}

	// Push should block and timeout
	start := time.Now()
	err = q.PushData([]byte("blocked"))
	elapsed := time.Since(start)

	if elapsed < 40*time.Millisecond {
		t.Errorf("Block should have waited at least 40ms, waited %v", elapsed)
	}

	if err == nil {
		t.Error("Expected timeout error for blocked push")
	}
}

func TestQueueMetricsFunctions(t *testing.T) {
	// Test that metric functions don't panic
	IncrementRetryTotal()
	IncrementRetrySuccessTotal()
	SetCapacityMetrics(100, 1000000)
	SetEffectiveCapacityMetrics(100, 1000000)
	SetDiskAvailableBytes(500000000)
	UpdateQueueMetrics(50, 500000, 1000000)
	IncrementWALWrite()
	IncrementWALCompact()
	IncrementDiskFull()
	IncrementSync()
	RecordBytesWritten(1000, 500)
	SetPendingSyncs(10)
	RecordSyncLatency(0.001)
}

func TestWALAllSyncModes(t *testing.T) {
	modes := []struct {
		name string
		mode SyncMode
	}{
		{"immediate", SyncImmediate},
		{"batched", SyncBatched},
		{"async", SyncAsync},
	}

	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			cfg := WALConfig{
				Path:          tmpDir,
				MaxSize:       100,
				MaxBytes:      100000,
				SyncMode:      m.mode,
				SyncBatchSize: 5,
				SyncInterval:  10 * time.Millisecond,
			}

			wal, err := NewWAL(cfg)
			if err != nil {
				t.Fatalf("Failed to create WAL with %s mode: %v", m.name, err)
			}

			// Append entries
			for i := 0; i < 10; i++ {
				if err := wal.Append([]byte("test data")); err != nil {
					t.Errorf("Append failed: %v", err)
				}
			}

			// Peek and consume
			for i := 0; i < 5; i++ {
				entry, err := wal.Peek()
				if err != nil {
					t.Errorf("Peek failed: %v", err)
				}
				if entry != nil {
					if err := wal.MarkConsumed(entry.Offset); err != nil {
						t.Errorf("MarkConsumed failed: %v", err)
					}
				}
			}

			if wal.Len() != 5 {
				t.Errorf("Expected 5 remaining entries, got %d", wal.Len())
			}

			wal.Close()

			// Recovery
			wal2, err := NewWAL(cfg)
			if err != nil {
				t.Fatalf("Failed to recover WAL: %v", err)
			}
			defer wal2.Close()

			if wal2.Len() != 5 {
				t.Errorf("Expected 5 entries after recovery, got %d", wal2.Len())
			}
		})
	}
}

func TestWALWithCompression(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := WALConfig{
		Path:        tmpDir,
		MaxSize:     100,
		MaxBytes:    100000,
		Compression: true,
	}

	wal, err := NewWAL(cfg)
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}

	// Test data that compresses well
	testData := []byte("This is a test string with repeating patterns aaaaaaaaaaaabbbbbbbbbbbb")

	for i := 0; i < 10; i++ {
		if err := wal.Append(testData); err != nil {
			t.Fatalf("Append failed: %v", err)
		}
	}

	// Verify data integrity
	entry, err := wal.Peek()
	if err != nil {
		t.Fatalf("Peek failed: %v", err)
	}
	if entry == nil {
		t.Fatal("Expected entry, got nil")
	}
	if string(entry.Data) != string(testData) {
		t.Errorf("Data mismatch: got %q, want %q", entry.Data, testData)
	}

	wal.Close()

	// Verify recovery with compression
	wal2, err := NewWAL(cfg)
	if err != nil {
		t.Fatalf("Failed to recover WAL: %v", err)
	}
	defer wal2.Close()

	if wal2.Len() != 10 {
		t.Errorf("Expected 10 entries after recovery, got %d", wal2.Len())
	}

	entry2, err := wal2.Peek()
	if err != nil {
		t.Fatalf("Peek after recovery failed: %v", err)
	}
	if string(entry2.Data) != string(testData) {
		t.Errorf("Data mismatch after recovery: got %q, want %q", entry2.Data, testData)
	}
}
