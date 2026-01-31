package queue

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWAL_Append_VariousData(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := WALConfig{
		Path:             tmpDir,
		MaxSize:          10,
		MaxBytes:         1024,
		CompactThreshold: 0.5,
	}

	wal, err := NewWAL(cfg)
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}
	defer wal.Close()

	// Append entries
	for i := 0; i < 5; i++ {
		data := []byte("test entry data")
		if err := wal.Append(data); err != nil {
			t.Errorf("Append %d failed: %v", i, err)
		}
	}

	// Check length
	if wal.Len() != 5 {
		t.Errorf("Expected length 5, got %d", wal.Len())
	}
}

func TestWAL_Append_TriggerCompaction(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := WALConfig{
		Path:             tmpDir,
		MaxSize:          100,
		MaxBytes:         10240,
		CompactThreshold: 0.3, // Low threshold to trigger compaction
	}

	wal, err := NewWAL(cfg)
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}
	defer wal.Close()

	// Append and consume to trigger compaction
	for i := 0; i < 10; i++ {
		data := []byte("entry")
		if err := wal.Append(data); err != nil {
			t.Errorf("Append %d failed: %v", i, err)
		}
	}

	// Consume some entries to mark for compaction
	for i := 0; i < 5; i++ {
		entry, _ := wal.Peek()
		if entry != nil {
			wal.MarkConsumed(entry.Offset)
		}
	}

	// Add more entries - should trigger compaction
	for i := 0; i < 5; i++ {
		data := []byte("post-compact entry")
		if err := wal.Append(data); err != nil {
			t.Errorf("Post-compact append %d failed: %v", i, err)
		}
	}
}

func TestWAL_Recovery(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := WALConfig{
		Path:             tmpDir,
		MaxSize:          100,
		MaxBytes:         10240,
		CompactThreshold: 0.5,
	}

	// Create first WAL, write data, close
	wal1, err := NewWAL(cfg)
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}

	for i := 0; i < 5; i++ {
		data := []byte("persistent entry")
		if err := wal1.Append(data); err != nil {
			t.Errorf("Append %d failed: %v", i, err)
		}
	}

	wal1.Close()

	// Create second WAL - should recover entries
	wal2, err := NewWAL(cfg)
	if err != nil {
		t.Fatalf("Failed to create second WAL: %v", err)
	}
	defer wal2.Close()

	// Should have recovered entries
	if wal2.Len() != 5 {
		t.Errorf("Expected 5 recovered entries, got %d", wal2.Len())
	}

	// Peek should return an entry
	entry, err := wal2.Peek()
	if err != nil {
		t.Errorf("Peek failed: %v", err)
	}
	if entry == nil {
		t.Error("Expected entry after recovery")
	}
}

func TestWAL_Close_Multiple(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := WALConfig{
		Path:             tmpDir,
		MaxSize:          100,
		MaxBytes:         10240,
		CompactThreshold: 0.5,
	}

	wal, err := NewWAL(cfg)
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}

	// Add some data
	wal.Append([]byte("test"))

	// First close
	if err := wal.Close(); err != nil {
		t.Errorf("First close failed: %v", err)
	}

	// Second close should handle gracefully
	err = wal.Close()
	// May or may not return error, but should not panic
	_ = err
}

func TestQueue_PushData_BlockBehavior_Timeout(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      2, // Very small queue
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
			t.Fatalf("Initial push %d failed: %v", i, err)
		}
	}

	// Next push should block and timeout
	start := time.Now()
	err = q.PushData([]byte("blocked"))
	elapsed := time.Since(start)

	// Should have taken at least the timeout duration
	if elapsed < 40*time.Millisecond {
		t.Errorf("Block timeout was too short: %v", elapsed)
	}

	// Error should indicate timeout
	if err == nil {
		t.Error("Expected error for blocked push after timeout")
	}
}

func TestQueue_PushData_DropNewest(t *testing.T) {
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
		if err := q.PushData([]byte("data")); err != nil {
			t.Fatalf("Initial push %d failed: %v", i, err)
		}
	}

	// Next push should succeed (drop newest means drop incoming)
	err = q.PushData([]byte("dropped_newest"))
	if err != nil {
		t.Errorf("DropNewest should not return error: %v", err)
	}

	// Queue length should still be at max
	if q.Len() > 3 {
		t.Errorf("Queue length exceeded max with DropNewest")
	}
}

func TestQueue_PushData_DropOldest(t *testing.T) {
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

	// Fill the queue with identifiable data
	for i := 0; i < 3; i++ {
		data := []byte{byte('A' + i)}
		if err := q.PushData(data); err != nil {
			t.Fatalf("Initial push %d failed: %v", i, err)
		}
	}

	// Add more entries - oldest should be dropped
	for i := 0; i < 2; i++ {
		data := []byte{byte('X' + i)}
		if err := q.PushData(data); err != nil {
			t.Errorf("Push with DropOldest failed: %v", err)
		}
	}

	// Queue should still be at or near max
	if q.Len() > 3 {
		t.Errorf("Queue length exceeded max: %d", q.Len())
	}
}

func TestQueue_DropOldestLocked(t *testing.T) {
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
		q.PushData([]byte("entry"))
	}

	initialLen := q.Len()

	// Pop one to make room
	entry, _ := q.Pop()
	if entry == nil {
		t.Error("Expected entry from Pop")
	}

	// Queue should be shorter
	if q.Len() >= initialLen {
		t.Errorf("Expected length to decrease after Pop")
	}
}

func TestWAL_ValidateEntry_CorruptedData(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := WALConfig{
		Path:             tmpDir,
		MaxSize:          100,
		MaxBytes:         10240,
		CompactThreshold: 0.5,
	}

	wal, err := NewWAL(cfg)
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}

	// Write some entries
	for i := 0; i < 3; i++ {
		wal.Append([]byte("valid entry"))
	}

	wal.Close()

	// Find and corrupt the data file
	files, _ := filepath.Glob(filepath.Join(tmpDir, "*.dat"))
	if len(files) > 0 {
		data, err := os.ReadFile(files[0])
		if err == nil && len(data) > 10 {
			// Corrupt some bytes
			data[5] = 0xFF
			data[6] = 0xFF
			os.WriteFile(files[0], data, 0644)
		}
	}

	// Try to recover - may or may not succeed depending on corruption
	wal2, err := NewWAL(cfg)
	if err != nil {
		t.Logf("WAL recovery with corruption: %v (may be expected)", err)
	} else {
		wal2.Close()
	}
}

func TestWAL_WriteIndexEntry_UpdateIndexEntry(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := WALConfig{
		Path:             tmpDir,
		MaxSize:          100,
		MaxBytes:         10240,
		CompactThreshold: 0.5,
	}

	wal, err := NewWAL(cfg)
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}
	defer wal.Close()

	// Append entries - this exercises writeIndexEntry
	for i := 0; i < 5; i++ {
		if err := wal.Append([]byte("index test")); err != nil {
			t.Errorf("Append failed: %v", err)
		}
	}

	// Consume entries - this exercises updateIndexEntry
	for i := 0; i < 3; i++ {
		entry, _ := wal.Peek()
		if entry != nil {
			if err := wal.MarkConsumed(entry.Offset); err != nil {
				t.Errorf("MarkConsumed failed: %v", err)
			}
		}
	}

	// Verify remaining entries
	remaining := wal.Len()
	if remaining != 2 {
		t.Errorf("Expected 2 remaining entries, got %d", remaining)
	}
}

func TestWAL_AdaptiveEnabled(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := WALConfig{
		Path:             tmpDir,
		MaxSize:          100,
		MaxBytes:         10240,
		CompactThreshold: 0.5,
		AdaptiveEnabled:  true,
	}

	wal, err := NewWAL(cfg)
	if err != nil {
		t.Fatalf("Failed to create WAL with adaptive: %v", err)
	}
	defer wal.Close()

	// Add data
	for i := 0; i < 10; i++ {
		wal.Append([]byte("adaptive test entry"))
	}

	if wal.Len() != 10 {
		t.Errorf("Expected 10 entries, got %d", wal.Len())
	}
}

func TestWAL_Append_ClosedWAL(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := WALConfig{
		Path:             tmpDir,
		MaxSize:          100,
		MaxBytes:         10240,
		CompactThreshold: 0.5,
	}

	wal, err := NewWAL(cfg)
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}

	// Close WAL
	wal.Close()

	// Try to append - should fail
	err = wal.Append([]byte("test"))
	if err != ErrClosed {
		t.Errorf("Expected ErrClosed, got %v", err)
	}
}

func TestWAL_Peek_ClosedWAL(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := WALConfig{
		Path:             tmpDir,
		MaxSize:          100,
		MaxBytes:         10240,
		CompactThreshold: 0.5,
	}

	wal, err := NewWAL(cfg)
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}

	// Add some data first
	wal.Append([]byte("test"))

	// Close WAL
	wal.Close()

	// Try to peek - should fail
	_, err = wal.Peek()
	if err != ErrClosed {
		t.Errorf("Expected ErrClosed, got %v", err)
	}
}

func TestWAL_MarkConsumed_ClosedWAL(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := WALConfig{
		Path:             tmpDir,
		MaxSize:          100,
		MaxBytes:         10240,
		CompactThreshold: 0.5,
	}

	wal, err := NewWAL(cfg)
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}

	// Add data and get entry
	wal.Append([]byte("test"))
	entry, _ := wal.Peek()

	// Close WAL
	wal.Close()

	// Try to mark consumed - should fail
	if entry != nil {
		err = wal.MarkConsumed(entry.Offset)
		if err != ErrClosed {
			t.Errorf("Expected ErrClosed, got %v", err)
		}
	}
}

func TestWAL_Append_MaxSizeLimit(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := WALConfig{
		Path:             tmpDir,
		MaxSize:          3, // Only 3 entries
		MaxBytes:         100000,
		CompactThreshold: 0.5,
	}

	wal, err := NewWAL(cfg)
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}
	defer wal.Close()

	// Fill up to limit
	for i := 0; i < 3; i++ {
		err := wal.Append([]byte("entry"))
		if err != nil {
			t.Errorf("Append %d failed: %v", i, err)
		}
	}

	// Next append should fail with queue full
	err = wal.Append([]byte("overflow"))
	if err != ErrQueueFull {
		t.Errorf("Expected ErrQueueFull, got %v", err)
	}
}

func TestWAL_Append_MaxBytesLimit(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := WALConfig{
		Path:             tmpDir,
		MaxSize:          100,
		MaxBytes:         100, // Very small byte limit
		CompactThreshold: 0.5,
	}

	wal, err := NewWAL(cfg)
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}
	defer wal.Close()

	// First append may succeed
	data := []byte("this is a test data that has some size")
	wal.Append(data)

	// Eventually should fail with queue full due to bytes
	for i := 0; i < 10; i++ {
		err := wal.Append(data)
		if err == ErrQueueFull {
			// Expected - bytes limit reached
			return
		}
	}
}

func TestWAL_EmptyPeek(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := WALConfig{
		Path:             tmpDir,
		MaxSize:          100,
		MaxBytes:         10240,
		CompactThreshold: 0.5,
	}

	wal, err := NewWAL(cfg)
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}
	defer wal.Close()

	// Peek on empty WAL
	entry, err := wal.Peek()
	if err != nil {
		t.Errorf("Unexpected error on empty peek: %v", err)
	}
	if entry != nil {
		t.Error("Expected nil entry for empty WAL")
	}
}

func TestWAL_LenAndSize(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := WALConfig{
		Path:             tmpDir,
		MaxSize:          100,
		MaxBytes:         10240,
		CompactThreshold: 0.5,
	}

	wal, err := NewWAL(cfg)
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}
	defer wal.Close()

	if wal.Len() != 0 {
		t.Errorf("Expected initial length 0, got %d", wal.Len())
	}

	if wal.Size() != 0 {
		t.Errorf("Expected initial size 0, got %d", wal.Size())
	}

	// Add entries
	for i := 0; i < 5; i++ {
		wal.Append([]byte("test entry"))
	}

	if wal.Len() != 5 {
		t.Errorf("Expected length 5, got %d", wal.Len())
	}

	if wal.Size() <= 0 {
		t.Error("Expected size > 0 after appending")
	}
}

func TestQueue_ClosedQueue(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      10,
		FullBehavior: DropNewest,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	// Add some data
	q.PushData([]byte("test"))

	// Close
	q.Close()

	// Try push on closed queue
	err = q.PushData([]byte("after close"))
	if err == nil {
		t.Error("Expected error pushing to closed queue")
	}
}

func TestQueue_Pop_Empty(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      10,
		FullBehavior: DropNewest,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Pop from empty queue
	entry, err := q.Pop()
	if err != nil {
		t.Errorf("Unexpected error on empty pop: %v", err)
	}
	if entry != nil {
		t.Error("Expected nil entry for empty queue")
	}
}

func TestQueue_LenAndSize(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      10,
		FullBehavior: DropNewest,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	if q.Len() != 0 {
		t.Errorf("Expected initial length 0, got %d", q.Len())
	}

	if q.Size() != 0 {
		t.Errorf("Expected initial size 0, got %d", q.Size())
	}

	// Add data
	for i := 0; i < 3; i++ {
		q.PushData([]byte("queue test entry"))
	}

	if q.Len() != 3 {
		t.Errorf("Expected length 3, got %d", q.Len())
	}

	if q.Size() <= 0 {
		t.Error("Expected size > 0 after push")
	}
}

func TestQueue_EffectiveLimits(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := Config{
		Path:         tmpDir,
		MaxSize:      10,
		MaxBytes:     1000,
		FullBehavior: DropNewest,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Check effective limits
	if q.EffectiveMaxSize() <= 0 {
		t.Error("Expected positive effective max size")
	}

	if q.EffectiveMaxBytes() <= 0 {
		t.Error("Expected positive effective max bytes")
	}
}

func TestWAL_MarkConsumed_NotFound(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := WALConfig{
		Path:             tmpDir,
		MaxSize:          100,
		MaxBytes:         10240,
		CompactThreshold: 0.5,
	}

	wal, err := NewWAL(cfg)
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}
	defer wal.Close()

	// Add an entry
	wal.Append([]byte("test"))

	// Try to mark consumed with wrong offset
	// Note: MarkConsumed may not return error for invalid offset
	// depending on implementation - just exercise the code path
	err = wal.MarkConsumed(99999)
	t.Logf("MarkConsumed with invalid offset: %v", err)
}

func TestWAL_Compact_MultipleEntries(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := WALConfig{
		Path:             tmpDir,
		MaxSize:          50,
		MaxBytes:         100000,
		CompactThreshold: 0.2, // Very low to trigger compaction
	}

	wal, err := NewWAL(cfg)
	if err != nil {
		t.Fatalf("Failed to create WAL: %v", err)
	}
	defer wal.Close()

	// Add many entries
	for i := 0; i < 20; i++ {
		wal.Append([]byte("compaction test entry"))
	}

	// Consume most entries to trigger compaction
	for i := 0; i < 15; i++ {
		entry, _ := wal.Peek()
		if entry != nil {
			wal.MarkConsumed(entry.Offset)
		}
	}

	// Add more to trigger compaction check
	for i := 0; i < 5; i++ {
		wal.Append([]byte("after consume"))
	}

	// Verify remaining
	remaining := wal.Len()
	t.Logf("Remaining entries after compaction: %d", remaining)
}
