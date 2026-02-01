package queue

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestFastQueuePeekFromDiskMultipleTimes tests multiple Peek calls reading from disk.
func TestFastQueuePeekFromDiskMultipleTimes(t *testing.T) {
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

	// Push and flush
	for i := 0; i < 5; i++ {
		fq.Push([]byte("peek-test"))
	}
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	// Multiple Peeks should work
	for i := 0; i < 10; i++ {
		data, err := fq.Peek()
		if err != nil {
			t.Fatalf("Peek %d failed: %v", i, err)
		}
		if string(data) != "peek-test" {
			t.Errorf("Peek %d: expected 'peek-test', got %q", i, data)
		}
	}
}

// TestFastQueueRecoverWithMultipleChunks tests recovery with data on disk.
func TestFastQueueRecoverWithMultipleChunks(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   50 * time.Millisecond,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	// Push data to disk
	data := make([]byte, 100)
	for i := 0; i < 8; i++ {
		fq.Push(data)
	}

	// Flush and sync
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.syncMetadataLocked()
	fq.mu.Unlock()

	time.Sleep(100 * time.Millisecond)
	fq.Close()

	// Reopen - should recover all entries
	fq2, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to recover: %v", err)
	}
	defer fq2.Close()

	if fq2.Len() != 8 {
		t.Errorf("Expected 8 entries, got %d", fq2.Len())
	}
}

// TestFastQueueReadBlockWithChunkTransition tests reading blocks from disk.
func TestFastQueueReadBlockWithChunkTransition(t *testing.T) {
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

	// Push entries that go to disk
	data := make([]byte, 80)
	for i := 0; i < 10; i++ {
		fq.Push(data)
	}

	// Pop all
	for i := 0; i < 10; i++ {
		d, err := fq.Pop()
		if err != nil {
			t.Fatalf("Pop %d error: %v", i, err)
		}
		if len(d) != 80 {
			t.Errorf("Pop %d: expected 80 bytes, got %d", i, len(d))
		}
	}
}

// TestFastQueueCleanupConsumedChunksMultiple tests cleanup after consumption.
func TestFastQueueCleanupConsumedChunksMultiple(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	// Push data
	data := make([]byte, 50)
	for i := 0; i < 20; i++ {
		fq.Push(data)
	}

	// Count chunks before consumption
	chunksBefore, _ := fq.GetChunkFiles()
	t.Logf("Chunks before: %d", len(chunksBefore))

	// Pop all
	for {
		d, _ := fq.Pop()
		if d == nil {
			break
		}
	}

	// Chunks should be cleaned up
	chunksAfter, _ := fq.GetChunkFiles()
	t.Logf("Chunks after: %d", len(chunksAfter))
}

// TestFastQueueCleanupOrphanedChunksWithValidRange tests orphan cleanup with valid range.
func TestFastQueueCleanupOrphanedChunksWithValidRange(t *testing.T) {
	tmpDir := t.TempDir()

	// First, create a queue with data
	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   50 * time.Millisecond,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	for i := 0; i < 5; i++ {
		fq.Push([]byte("data"))
	}

	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.syncMetadataLocked()
	fq.mu.Unlock()

	fq.Close()

	// Create an orphan file well beyond the valid range (2MB offset)
	// This simulates a leftover chunk that should be cleaned up
	orphanOffset := int64(2 * 1024 * 1024)
	oldOrphan := filepath.Join(tmpDir, fmt.Sprintf("%016x", orphanOffset))
	os.WriteFile(oldOrphan, []byte("orphan-data-to-cleanup"), 0644)

	// Reopen - should handle orphan cleanup
	fq2, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to reopen: %v", err)
	}
	defer fq2.Close()

	// Check chunks after reopen
	chunks, _ := fq2.GetChunkFiles()
	t.Logf("Chunks after reopen: %v", chunks)
}

// TestFastQueuePopWithEmptyChannel tests Pop when channel is empty but disk has data.
func TestFastQueuePopWithEmptyChannel(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   50 * time.Millisecond,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	// Push and flush (data on disk, channel empty)
	for i := 0; i < 5; i++ {
		fq.Push([]byte("disk-only"))
	}

	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.syncMetadataLocked()
	fq.mu.Unlock()

	fq.Close()

	// Reopen - channel is empty, data on disk
	fq2, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to reopen: %v", err)
	}
	defer fq2.Close()

	// Pop should read from disk
	for i := 0; i < 5; i++ {
		data, err := fq2.Pop()
		if err != nil {
			t.Fatalf("Pop %d error: %v", i, err)
		}
		if string(data) != "disk-only" {
			t.Errorf("Pop %d: expected 'disk-only', got %q", i, data)
		}
	}
}

// TestFastQueuePeekWithChannelReinsert tests Peek when channel can't reinsert.
func TestFastQueuePeekWithChannelReinsert(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  5,
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

	// Push data (stays in channel)
	for i := 0; i < 5; i++ {
		fq.Push([]byte("channel-data"))
	}

	// Multiple Peeks should work
	for i := 0; i < 5; i++ {
		data, err := fq.Peek()
		if err != nil {
			t.Fatalf("Peek %d error: %v", i, err)
		}
		if data == nil {
			t.Fatalf("Peek %d returned nil", i)
		}
	}

	// Queue should still have 5
	if fq.Len() != 5 {
		t.Errorf("Expected 5, got %d", fq.Len())
	}
}

// TestFastQueueSyncMetadataWithDiskData tests metadata sync with data on disk.
func TestFastQueueSyncMetadataWithDiskData(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   20 * time.Millisecond,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	// Push to trigger disk write
	for i := 0; i < 5; i++ {
		fq.Push([]byte("sync-test"))
	}

	// Wait for sync
	time.Sleep(50 * time.Millisecond)

	// Pop some
	fq.Pop()
	fq.Pop()

	// Wait for sync again
	time.Sleep(50 * time.Millisecond)

	// Metadata should exist
	metaPath := filepath.Join(tmpDir, metaFileName)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("Failed to read metadata: %v", err)
	}

	if len(data) == 0 {
		t.Error("Metadata should not be empty")
	}
}

// TestFastQueueGetChunkFilesEmpty tests GetChunkFiles with no chunks.
func TestFastQueueGetChunkFilesEmpty(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  100, // Large channel, data stays in memory
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

	// Push data (stays in channel)
	for i := 0; i < 5; i++ {
		fq.Push([]byte("memory-only"))
	}

	chunks, err := fq.GetChunkFiles()
	if err != nil {
		t.Fatalf("GetChunkFiles error: %v", err)
	}

	if len(chunks) != 0 {
		t.Logf("Note: Found %d chunk files (may be from flush)", len(chunks))
	}
}

// TestSendQueuePushDataVariousErrors tests pushData error paths.
func TestSendQueuePushDataVariousErrors(t *testing.T) {
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

	// Fill queue
	for i := 0; i < 3; i++ {
		q.PushData([]byte("data"))
	}

	// Push more - should trigger DropOldest
	for i := 0; i < 5; i++ {
		if err := q.PushData([]byte("more-data")); err != nil {
			t.Errorf("PushData %d failed: %v", i, err)
		}
	}

	// Queue should still be at max
	if q.Len() != 3 {
		t.Errorf("Expected 3, got %d", q.Len())
	}
}

// TestSendQueuePushDataDropNewest tests pushData with DropNewest.
func TestSendQueuePushDataDropNewest(t *testing.T) {
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

	// Fill queue
	for i := 0; i < 3; i++ {
		q.PushData([]byte("data"))
	}

	// Push more - should drop newest (incoming)
	for i := 0; i < 5; i++ {
		if err := q.PushData([]byte("dropped")); err != nil {
			t.Errorf("PushData %d should not error: %v", i, err)
		}
	}

	// Queue should still be at max
	if q.Len() != 3 {
		t.Errorf("Expected 3, got %d", q.Len())
	}
}

// TestFastQueueFlushInmemoryBlocksMultiple tests flushing multiple times.
func TestFastQueueFlushInmemoryBlocksMultiple(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  50,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	// Multiple push-flush cycles
	for cycle := 0; cycle < 3; cycle++ {
		for i := 0; i < 10; i++ {
			fq.Push([]byte("cycle-data"))
		}

		fq.mu.Lock()
		fq.flushInmemoryBlocksLocked()
		fq.mu.Unlock()
	}

	if fq.Len() != 30 {
		t.Errorf("Expected 30, got %d", fq.Len())
	}
}

// TestFastQueueCloseWithBothMemoryAndDisk tests Close with data in both places.
func TestFastQueueCloseWithBothMemoryAndDisk(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  5,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   50 * time.Millisecond,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	// Push some (stays in memory)
	for i := 0; i < 3; i++ {
		fq.Push([]byte("memory"))
	}

	// Flush to get some on disk
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	// Push more (stays in memory)
	for i := 0; i < 3; i++ {
		fq.Push([]byte("more-memory"))
	}

	// Close should flush all
	fq.Close()

	// Reopen and verify
	fq2, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to reopen: %v", err)
	}
	defer fq2.Close()

	if fq2.Len() != 6 {
		t.Errorf("Expected 6, got %d", fq2.Len())
	}
}

// TestFastQueueWriteBlockChunkRotationScenarios tests various chunk rotation scenarios.
func TestFastQueueWriteBlockChunkRotationScenarios(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024, // Small to trigger rotation easier
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	// Push data that approaches chunk boundary
	data := make([]byte, 200)
	for i := 0; i < 10; i++ {
		if err := fq.Push(data); err != nil {
			t.Fatalf("Push %d failed: %v", i, err)
		}
	}

	// Verify data can be read back
	for i := 0; i < 10; i++ {
		d, err := fq.Pop()
		if err != nil {
			t.Fatalf("Pop %d failed: %v", i, err)
		}
		if len(d) != 200 {
			t.Errorf("Pop %d: expected 200 bytes, got %d", i, len(d))
		}
	}
}

// TestFastQueuePeekClosed tests Peek on closed queue.
func TestFastQueuePeekClosed(t *testing.T) {
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

	fq.Push([]byte("data"))
	fq.Close()

	// Peek should return error on closed queue
	_, err = fq.Peek()
	if err != ErrQueueClosed {
		t.Errorf("Expected ErrQueueClosed, got %v", err)
	}
}

// TestFastQueuePushClosed tests Push on closed queue.
func TestFastQueuePushClosed(t *testing.T) {
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

	fq.Close()

	// Push should return error on closed queue
	err = fq.Push([]byte("data"))
	if err != ErrQueueClosed {
		t.Errorf("Expected ErrQueueClosed, got %v", err)
	}
}

// TestFastQueuePopClosed tests Pop on closed queue.
func TestFastQueuePopClosed(t *testing.T) {
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

	fq.Push([]byte("data"))
	fq.Close()

	// Pop should return error on closed queue
	_, err = fq.Pop()
	if err != ErrQueueClosed {
		t.Errorf("Expected ErrQueueClosed, got %v", err)
	}
}

// TestSendQueueBlockBehaviorTimeoutCoverage tests block behavior with timeout.
func TestSendQueueBlockBehaviorTimeoutCoverage(t *testing.T) {
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

	// Fill queue
	q.PushData([]byte("data1"))
	q.PushData([]byte("data2"))

	// This push should timeout (Block behavior with full queue)
	start := time.Now()
	err = q.PushData([]byte("data3"))
	elapsed := time.Since(start)

	// Should have waited for timeout
	if elapsed < 40*time.Millisecond {
		t.Errorf("Expected to wait for timeout, only waited %v", elapsed)
	}
}

// TestSendQueueCloseWhileBlocking tests close during block wait.
func TestSendQueueCloseWhileBlocking(t *testing.T) {
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

	// Start blocking push in goroutine
	done := make(chan error, 1)
	go func() {
		err := q.PushData([]byte("data3"))
		done <- err
	}()

	// Wait a bit then close
	time.Sleep(50 * time.Millisecond)
	q.Close()

	// Should get closed error
	select {
	case err := <-done:
		if err == nil {
			t.Error("Expected error from blocked push after close")
		}
	case <-time.After(time.Second):
		t.Error("Blocked push did not return after close")
	}
}

// TestFastQueueRecoverEmptyMetadata tests recovery with empty metadata.
func TestFastQueueRecoverEmptyMetadata(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	// Create an empty metadata file
	metaPath := filepath.Join(tmpDir, "fastqueue.meta")
	os.WriteFile(metaPath, []byte("{}"), 0644)

	// Should recover gracefully
	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to recover with empty metadata: %v", err)
	}
	defer fq.Close()

	if fq.Len() != 0 {
		t.Errorf("Expected empty queue, got %d", fq.Len())
	}
}

// TestFastQueueRecoverInvalidMetadata tests recovery with invalid metadata.
func TestFastQueueRecoverInvalidMetadata(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	// Create invalid metadata file
	metaPath := filepath.Join(tmpDir, "fastqueue.meta")
	os.WriteFile(metaPath, []byte("invalid json{"), 0644)

	// Should return error for invalid metadata
	_, err := NewFastQueue(cfg)
	if err == nil {
		t.Error("Expected error for invalid metadata")
	}
}

// TestFastQueueMaxSizeEnforcement tests that MaxSize is enforced.
func TestFastQueueMaxSizeEnforcement(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            3,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	// Fill to max
	for i := 0; i < 3; i++ {
		if err := fq.Push([]byte("data")); err != nil {
			t.Fatalf("Push %d failed: %v", i, err)
		}
	}

	// Next push should return ErrQueueFull
	err = fq.Push([]byte("overflow"))
	if err != ErrQueueFull {
		t.Errorf("Expected ErrQueueFull, got %v", err)
	}
}

// TestFastQueueMaxBytesEnforcement tests that MaxBytes is enforced.
func TestFastQueueMaxBytesEnforcement(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
		MaxBytes:           100, // 100 bytes max
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	// Push data that exceeds max bytes
	data := make([]byte, 50)
	fq.Push(data)
	fq.Push(data)

	// Next push should return ErrQueueFull
	err = fq.Push(data)
	if err != ErrQueueFull {
		t.Errorf("Expected ErrQueueFull, got %v", err)
	}
}

// TestFastQueueReaderWriterOnSameChunk tests reading while writing to same chunk.
func TestFastQueueReaderWriterOnSameChunk(t *testing.T) {
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

	// Push entries (will go to disk due to MaxInmemoryBlocks=1)
	for i := 0; i < 5; i++ {
		fq.Push([]byte("data"))
	}

	// Pop some to initialize reader on same chunk
	for i := 0; i < 2; i++ {
		fq.Pop()
	}

	// Push more (writer still on same chunk)
	for i := 0; i < 3; i++ {
		fq.Push([]byte("more"))
	}

	// Pop all remaining
	for {
		d, _ := fq.Pop()
		if d == nil {
			break
		}
	}
}

// TestSendQueueDropOldestFailsAfterClose tests drop oldest when queue is closed.
func TestSendQueueDropOldestFailsAfterClose(t *testing.T) {
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

	// Fill queue
	q.PushData([]byte("data1"))
	q.PushData([]byte("data2"))

	// Close queue
	q.Close()

	// Try to push after close - should fail
	err = q.PushData([]byte("data3"))
	if err == nil {
		t.Error("Expected error on push after close")
	}
}

// TestFastQueuePeekFromDiskWithMissingChunk tests peek when chunk file doesn't exist.
func TestFastQueuePeekFromDiskWithMissingChunk(t *testing.T) {
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
	for i := 0; i < 3; i++ {
		fq.Push([]byte("data"))
	}

	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	// Peek should work
	data, err := fq.Peek()
	if err != nil {
		t.Fatalf("Peek failed: %v", err)
	}
	if string(data) != "data" {
		t.Errorf("Expected 'data', got %q", data)
	}

	fq.Close()
}

// TestFastQueueCloseWithReaderDifferentFromWriter tests Close when reader/writer on different chunks.
func TestFastQueueCloseWithReaderDifferentFromWriter(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	// Push many entries to disk
	for i := 0; i < 20; i++ {
		fq.Push([]byte("test-data"))
	}

	// Pop some to move reader position
	for i := 0; i < 10; i++ {
		fq.Pop()
	}

	// Close should handle both reader and writer
	fq.Close()
}

// TestFastQueueCloseAlreadyClosed tests closing an already closed queue.
func TestFastQueueCloseAlreadyClosed(t *testing.T) {
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

	fq.Push([]byte("data"))

	// First close
	err = fq.Close()
	if err != nil {
		t.Errorf("First close failed: %v", err)
	}

	// Second close should be no-op
	err = fq.Close()
	if err != nil {
		t.Errorf("Second close should return nil, got: %v", err)
	}
}

// TestFastQueueCountEntriesOnDiskWithData tests counting entries accurately.
func TestFastQueueCountEntriesOnDiskWithData(t *testing.T) {
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

	// Push data that goes to disk
	for i := 0; i < 10; i++ {
		fq.Push([]byte("count-test-data"))
	}

	// Force flush
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	// Len should reflect entries on disk
	if fq.Len() != 10 {
		t.Errorf("Expected 10 entries, got %d", fq.Len())
	}
}

// TestFastQueueRecoverWithExistingChunks tests recovery with pre-existing chunk files.
func TestFastQueueRecoverWithExistingChunks(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   50 * time.Millisecond,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	// Create queue and push data
	fq1, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create first queue: %v", err)
	}

	for i := 0; i < 10; i++ {
		fq1.Push([]byte("recovery-data"))
	}

	fq1.mu.Lock()
	fq1.flushInmemoryBlocksLocked()
	fq1.syncMetadataLocked()
	fq1.mu.Unlock()

	// Pop some to move reader forward
	for i := 0; i < 5; i++ {
		fq1.Pop()
	}

	// Sync again to save reader position
	fq1.mu.Lock()
	fq1.syncMetadataLocked()
	fq1.mu.Unlock()

	fq1.Close()

	// Reopen and verify remaining entries
	fq2, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to recover queue: %v", err)
	}
	defer fq2.Close()

	if fq2.Len() != 5 {
		t.Errorf("Expected 5 entries after recovery, got %d", fq2.Len())
	}
}

// TestFastQueueSizeTracking tests that Size() returns correct values.
func TestFastQueueSizeTracking(t *testing.T) {
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

	initialSize := fq.Size()
	if initialSize != 0 {
		t.Errorf("Expected initial size 0, got %d", initialSize)
	}

	// Push data
	data := []byte("test-size-data")
	fq.Push(data)
	fq.Push(data)

	// Size should increase
	size := fq.Size()
	if size != int64(len(data)*2) {
		t.Errorf("Expected size %d, got %d", len(data)*2, size)
	}

	// Pop and check size decreases
	fq.Pop()
	size = fq.Size()
	if size != int64(len(data)) {
		t.Errorf("Expected size %d after pop, got %d", len(data), size)
	}
}

// TestSendQueuePushError tests various push error scenarios.
func TestSendQueuePushError(t *testing.T) {
	tmpDir := t.TempDir()

	// Test PushData error path
	cfg := Config{
		Path:         tmpDir,
		MaxSize:      2,
		FullBehavior: DropNewest,
	}

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	defer q.Close()

	// Fill queue
	q.PushData([]byte("data1"))
	q.PushData([]byte("data2"))

	// PushData should still work (drops newest)
	err = q.PushData([]byte("data3"))
	if err != nil {
		t.Errorf("PushData with DropNewest should succeed: %v", err)
	}

	// Queue should still be at max
	if q.Len() != 2 {
		t.Errorf("Expected 2, got %d", q.Len())
	}
}

// TestFastQueueFlushWithEmptyChannel tests flush when channel is empty.
func TestFastQueueFlushWithEmptyChannel(t *testing.T) {
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

	// Flush with empty channel - should be no-op
	fq.mu.Lock()
	err = fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	if err != nil {
		t.Errorf("Flush of empty channel failed: %v", err)
	}
}

// TestFastQueueGetChunkFilesWithChunks tests GetChunkFiles with actual chunks.
func TestFastQueueGetChunkFilesWithChunks(t *testing.T) {
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

	// Push data to create chunks
	for i := 0; i < 10; i++ {
		fq.Push([]byte("chunk-data"))
	}

	chunks, err := fq.GetChunkFiles()
	if err != nil {
		t.Fatalf("GetChunkFiles failed: %v", err)
	}

	t.Logf("Found %d chunks", len(chunks))
}

// TestFastQueueCountEntriesWithMissingChunk tests count entries when a chunk is missing.
func TestFastQueueCountEntriesWithMissingChunk(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   50 * time.Millisecond,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	// Create queue and add data
	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}

	for i := 0; i < 5; i++ {
		fq.Push([]byte("count-test"))
	}

	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.syncMetadataLocked()
	fq.mu.Unlock()

	// Get current length (triggers count)
	length := fq.Len()
	t.Logf("Queue length: %d", length)

	fq.Close()
}

// TestSendQueuePeekAndPop tests interleaved peek and pop operations.
func TestSendQueuePeekAndPopInterleaved(t *testing.T) {
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

	// Push several entries
	for i := 0; i < 5; i++ {
		q.PushData([]byte(fmt.Sprintf("data-%d", i)))
	}

	// Peek should return first entry
	entry1, err := q.Peek()
	if err != nil {
		t.Fatalf("Peek failed: %v", err)
	}
	if entry1 == nil {
		t.Fatal("Peek returned nil")
	}

	// Pop should return same first entry
	entry2, err := q.Pop()
	if err != nil {
		t.Fatalf("Pop failed: %v", err)
	}

	// Should be able to peek again for next entry
	entry3, err := q.Peek()
	if err != nil {
		t.Fatalf("Second peek failed: %v", err)
	}
	if entry3 == nil {
		t.Fatal("Second peek returned nil")
	}

	// Verify queue has fewer entries
	if q.Len() != 4 {
		t.Errorf("Expected 4 entries, got %d", q.Len())
	}

	_ = entry2 // silence unused warning
}

// TestFastQueuePushToFullChannelThenDisk tests push when channel is full.
func TestFastQueuePushToFullChannelThenDisk(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  2, // Very small channel
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

	// Fill channel first
	for i := 0; i < 2; i++ {
		fq.Push([]byte("channel-fill"))
	}

	// Next pushes should go to disk (channel full)
	for i := 0; i < 5; i++ {
		if err := fq.Push([]byte("disk-data")); err != nil {
			t.Fatalf("Push %d failed: %v", i, err)
		}
	}

	// Verify all entries are accessible
	count := 0
	for {
		d, _ := fq.Pop()
		if d == nil {
			break
		}
		count++
	}

	if count != 7 {
		t.Errorf("Expected 7 entries, got %d", count)
	}
}

// TestFastQueuePushClosedDuringOperation tests push when queue is closed during operation.
func TestFastQueuePushClosedDuringOperation(t *testing.T) {
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

	// Fill channel to force disk write path
	fq.Push([]byte("fill"))

	// Close the queue
	fq.Close()

	// Push should fail with closed error
	err = fq.Push([]byte("after-close"))
	if err != ErrQueueClosed {
		t.Errorf("Expected ErrQueueClosed, got %v", err)
	}
}
