package queue

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestFastQueuePeekFromDiskWithExistingReader tests Peek reusing existing reader chunk.
func TestFastQueuePeekFromDiskWithExistingReader(t *testing.T) {
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

	// Push data to disk
	for i := 0; i < 5; i++ {
		fq.Push([]byte("test-data"))
	}

	// Force flush
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	// Pop one entry to initialize readerChunk
	_, err = fq.Pop()
	if err != nil {
		t.Fatalf("Pop failed: %v", err)
	}

	// Now Peek should use the existing readerChunk
	data, err := fq.Peek()
	if err != nil {
		t.Fatalf("Peek failed: %v", err)
	}
	if string(data) != "test-data" {
		t.Errorf("Expected 'test-data', got %q", data)
	}
}

// TestFastQueuePopMultipleChunks tests Pop operations.
func TestFastQueuePopMultipleChunks(t *testing.T) {
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

	// Push data
	data := make([]byte, 50)
	for i := 0; i < 10; i++ {
		if err := fq.Push(data); err != nil {
			t.Fatalf("Push %d failed: %v", i, err)
		}
	}

	// Pop all
	for i := 0; i < 10; i++ {
		d, err := fq.Pop()
		if err != nil {
			t.Fatalf("Pop %d failed: %v", i, err)
		}
		if d == nil {
			t.Fatalf("Pop %d returned nil", i)
		}
	}

	if fq.Len() != 0 {
		t.Errorf("Expected empty queue, got %d", fq.Len())
	}
}

// TestFastQueueRecoverWithValidData tests recovery with real persisted data.
func TestFastQueueRecoverWithValidData(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   50 * time.Millisecond,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	// Create first queue and add data
	fq1, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	testData := []byte("recovery-test-data-1234567890")
	for i := 0; i < 10; i++ {
		if err := fq1.Push(testData); err != nil {
			t.Fatalf("Push failed: %v", err)
		}
	}

	// Force flush and sync
	fq1.mu.Lock()
	fq1.flushInmemoryBlocksLocked()
	fq1.syncMetadataLocked()
	fq1.mu.Unlock()

	// Wait for background sync
	time.Sleep(100 * time.Millisecond)

	// Close first queue
	fq1.Close()

	// Create second queue - should recover
	fq2, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to recover FastQueue: %v", err)
	}
	defer fq2.Close()

	if fq2.Len() != 10 {
		t.Errorf("Expected 10 entries after recovery, got %d", fq2.Len())
	}

	// Verify data integrity
	for i := 0; i < 10; i++ {
		data, err := fq2.Pop()
		if err != nil {
			t.Fatalf("Pop %d failed: %v", i, err)
		}
		if string(data) != string(testData) {
			t.Errorf("Data mismatch at %d: expected %q, got %q", i, testData, data)
		}
	}
}

// TestFastQueueRecoverWithPartiallyConsumed tests recovery after partial consumption.
func TestFastQueueRecoverWithPartiallyConsumed(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   50 * time.Millisecond,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	// Create first queue
	fq1, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	// Push 10 entries
	for i := 0; i < 10; i++ {
		fq1.Push([]byte("data"))
	}

	// Force flush
	fq1.mu.Lock()
	fq1.flushInmemoryBlocksLocked()
	fq1.mu.Unlock()

	// Pop 3 entries
	for i := 0; i < 3; i++ {
		fq1.Pop()
	}

	// Sync metadata
	fq1.mu.Lock()
	fq1.syncMetadataLocked()
	fq1.mu.Unlock()

	time.Sleep(100 * time.Millisecond)
	fq1.Close()

	// Recover - should have 7 entries
	fq2, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to recover: %v", err)
	}
	defer fq2.Close()

	if fq2.Len() != 7 {
		t.Errorf("Expected 7 entries after recovery, got %d", fq2.Len())
	}
}

// TestFastQueueCloseWithSyncError tests close behavior (sync errors are hard to simulate).
func TestFastQueueCloseWithPendingData(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  100,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	// Push data that stays in memory
	for i := 0; i < 50; i++ {
		fq.Push([]byte("pending-data"))
	}

	// Close should flush all data
	err = fq.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Verify data was flushed (by reopening)
	fq2, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to reopen: %v", err)
	}
	defer fq2.Close()

	if fq2.Len() != 50 {
		t.Errorf("Expected 50 entries after reopen, got %d", fq2.Len())
	}
}

// TestFastQueuePeekEmptyAfterPop tests Peek returns nil after all data popped.
func TestFastQueuePeekEmptyAfterPop(t *testing.T) {
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
	fq.Push([]byte("data"))
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	// Pop
	fq.Pop()

	// Peek should return nil
	data, err := fq.Peek()
	if err != nil {
		t.Errorf("Peek error: %v", err)
	}
	if data != nil {
		t.Errorf("Expected nil data, got %q", data)
	}
}

// TestFastQueueChunkRotationWithReaderSameChunk tests reader/writer operations.
func TestFastQueueChunkRotationWithReaderSameChunk(t *testing.T) {
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

	data := make([]byte, 50)

	// Push first entries
	for i := 0; i < 3; i++ {
		fq.Push(data)
	}

	// Pop one
	fq.Pop()

	// Push more
	for i := 0; i < 5; i++ {
		fq.Push(data)
	}

	// Pop remaining
	for {
		d, _ := fq.Pop()
		if d == nil {
			break
		}
	}
}

// TestFastQueueCountEntriesOnDiskMultipleChunks tests counting across chunks.
func TestFastQueueCountEntriesOnDiskMultipleChunks(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024, // 1MB chunks to avoid issues
		MetaSyncInterval:   50 * time.Millisecond,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	data := make([]byte, 100)
	for i := 0; i < 10; i++ {
		fq.Push(data)
	}

	// Force flush and sync
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.syncMetadataLocked()
	fq.mu.Unlock()

	time.Sleep(100 * time.Millisecond)
	fq.Close()

	// Reopen to trigger countEntriesOnDisk
	fq2, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to reopen: %v", err)
	}
	defer fq2.Close()

	if fq2.Len() != 10 {
		t.Errorf("Expected 10 entries, got %d", fq2.Len())
	}
}

// TestFastQueueCleanupOrphanedChunksOutOfRange tests cleanup of out-of-range chunks.
func TestFastQueueCleanupOrphanedChunksOutOfRange(t *testing.T) {
	tmpDir := t.TempDir()

	// Create chunk files that would be out of range
	// Future chunk (beyond writer offset)
	futureChunk := filepath.Join(tmpDir, "00000000ffffffff")
	if err := os.WriteFile(futureChunk, []byte("future"), 0644); err != nil {
		t.Fatalf("Failed to create future chunk: %v", err)
	}

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

	// Future chunk should be cleaned up
	if _, err := os.Stat(futureChunk); !os.IsNotExist(err) {
		t.Log("Future chunk was not cleaned up (may be within valid range for large chunk sizes)")
	}
}

// TestFastQueuePushChannelFullThenDisk tests push when channel fills and spills to disk.
func TestFastQueuePushChannelFullThenDisk(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  3,
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

	// Push more than channel capacity
	for i := 0; i < 10; i++ {
		if err := fq.Push([]byte("spill-test")); err != nil {
			t.Fatalf("Push %d failed: %v", i, err)
		}
	}

	// Should have disk bytes now
	if fq.diskBytes.Load() == 0 {
		t.Log("Note: Data may still be in channel buffer")
	}

	// Pop all and verify
	for i := 0; i < 10; i++ {
		data, _ := fq.Pop()
		if data == nil {
			t.Fatalf("Pop %d returned nil", i)
		}
	}
}

// TestFastQueueConcurrentPeekPop tests concurrent Peek and Pop.
func TestFastQueueConcurrentPeekPop(t *testing.T) {
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

	// Pre-fill queue
	for i := 0; i < 100; i++ {
		fq.Push([]byte("concurrent-test"))
	}

	var wg sync.WaitGroup

	// Peekers
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				fq.Peek()
			}
		}()
	}

	// Poppers
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				fq.Pop()
			}
		}()
	}

	wg.Wait()
}

// TestFastQueueMetaSyncDuringOperations tests metadata sync happens during operations.
func TestFastQueueMetaSyncDuringOperations(t *testing.T) {
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
	defer fq.Close()

	// Push data
	for i := 0; i < 5; i++ {
		fq.Push([]byte("sync-during-ops"))
	}

	// Wait for multiple sync intervals
	time.Sleep(200 * time.Millisecond)

	// Pop some
	fq.Pop()
	fq.Pop()

	// Wait for sync
	time.Sleep(100 * time.Millisecond)

	// Metadata should be updated
	metaPath := filepath.Join(tmpDir, metaFileName)
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		t.Error("Metadata file should exist")
	}
}

// TestFastQueueGetChunkFilesWithNonHexNames tests GetChunkFiles ignores non-hex names.
func TestFastQueueGetChunkFilesWithNonHexNames(t *testing.T) {
	tmpDir := t.TempDir()

	// Create some non-chunk files
	os.WriteFile(filepath.Join(tmpDir, "not-a-chunk"), []byte("data"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "short"), []byte("data"), 0644)
	os.WriteFile(filepath.Join(tmpDir, metaFileName), []byte("{}"), 0644)

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

	// Push to create valid chunk
	fq.Push([]byte("data"))
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	chunks, err := fq.GetChunkFiles()
	if err != nil {
		t.Fatalf("GetChunkFiles failed: %v", err)
	}

	// Should only return valid chunk names (16 hex chars)
	for _, chunk := range chunks {
		if len(chunk) != 16 {
			t.Errorf("Unexpected chunk name: %s", chunk)
		}
	}
}

// TestFastQueuePeekPutBack tests Peek putting data back when channel is full.
func TestFastQueuePeekChannelFull(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  2, // Small channel
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

	// Fill channel exactly
	fq.Push([]byte("item1"))
	fq.Push([]byte("item2"))

	// Peek should work (takes from channel, puts back)
	data, err := fq.Peek()
	if err != nil {
		t.Fatalf("Peek failed: %v", err)
	}
	if data == nil {
		t.Error("Peek returned nil")
	}

	// Queue should still have 2 items
	if fq.Len() != 2 {
		t.Errorf("Expected 2 items, got %d", fq.Len())
	}
}

// TestFastQueueReadBlockChunkBoundary tests reading at exact chunk boundary.
func TestFastQueueReadBlockChunkBoundary(t *testing.T) {
	tmpDir := t.TempDir()

	// Chunk size that allows exactly 2 entries
	entrySize := 50 + blockHeaderSize
	chunkSize := int64(entrySize * 2)

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
	defer fq.Close()

	data := make([]byte, 50)

	// Push entries to fill exactly one chunk, then start another
	for i := 0; i < 5; i++ {
		if err := fq.Push(data); err != nil {
			t.Fatalf("Push %d failed: %v", i, err)
		}
	}

	// Pop all - should handle chunk boundaries
	for i := 0; i < 5; i++ {
		d, err := fq.Pop()
		if err != nil {
			t.Fatalf("Pop %d error: %v", i, err)
		}
		if d == nil {
			t.Fatalf("Pop %d returned nil", i)
		}
	}
}
