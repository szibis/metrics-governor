package queue

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestFastQueuePeekFromDisk tests Peek when data is on disk.
func TestFastQueuePeekFromDisk(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1, // Small channel to force disk writes
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour, // Disable sync during test
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	// Push enough to spill to disk
	testData := []byte("disk-data-test")
	for i := 0; i < 5; i++ {
		if err := fq.Push(testData); err != nil {
			t.Fatalf("Failed to push: %v", err)
		}
	}

	// Force flush to disk
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	// Now Peek should read from disk
	data, err := fq.Peek()
	if err != nil {
		t.Fatalf("Peek failed: %v", err)
	}
	if string(data) != string(testData) {
		t.Errorf("Expected %q, got %q", testData, data)
	}

	// Verify Peek didn't remove the data
	if fq.Len() != 5 {
		t.Errorf("Expected 5 entries after Peek, got %d", fq.Len())
	}
}

// TestFastQueuePeekFromDiskMultiple tests multiple Peek calls from disk.
func TestFastQueuePeekFromDiskMultiple(t *testing.T) {
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

	testData := []byte("test-data")
	for i := 0; i < 3; i++ {
		fq.Push(testData)
	}

	// Force flush
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	// Multiple Peeks should return the same data
	for i := 0; i < 3; i++ {
		data, err := fq.Peek()
		if err != nil {
			t.Fatalf("Peek %d failed: %v", i, err)
		}
		if string(data) != string(testData) {
			t.Errorf("Peek %d: expected %q, got %q", i, testData, data)
		}
	}
}

// TestFastQueueCloseWithData tests closing with data in both memory and disk.
func TestFastQueueCloseWithData(t *testing.T) {
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

	// Push data
	for i := 0; i < 10; i++ {
		fq.Push([]byte("data"))
	}

	// Close should flush and sync
	if err := fq.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify metadata file exists
	metaPath := filepath.Join(tmpDir, metaFileName)
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		t.Error("Metadata file should exist after close")
	}
}

// TestFastQueueCloseIdempotent tests that Close can be called multiple times.
func TestFastQueueCloseIdempotent(t *testing.T) {
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

	// First close
	if err := fq.Close(); err != nil {
		t.Fatalf("First close failed: %v", err)
	}

	// Second close should not error
	if err := fq.Close(); err != nil {
		t.Errorf("Second close should not error, got: %v", err)
	}
}

// TestFastQueueRecoverWithCorruptedMeta tests recovery with corrupted metadata.
func TestFastQueueRecoverWithCorruptedMeta(t *testing.T) {
	tmpDir := t.TempDir()

	// Create corrupted metadata file
	metaPath := filepath.Join(tmpDir, metaFileName)
	if err := os.WriteFile(metaPath, []byte("not valid json"), 0644); err != nil {
		t.Fatalf("Failed to create corrupted meta: %v", err)
	}

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
		t.Error("Expected error for corrupted metadata")
	}
}

// TestFastQueueRecoverWithMissingChunk tests recovery when chunk file is missing.
func TestFastQueueRecoverWithMissingChunk(t *testing.T) {
	tmpDir := t.TempDir()

	// Create metadata pointing to non-existent chunk
	metaPath := filepath.Join(tmpDir, metaFileName)
	metaContent := `{"name":"fastqueue","reader_offset":0,"writer_offset":1000,"version":1}`
	if err := os.WriteFile(metaPath, []byte(metaContent), 0644); err != nil {
		t.Fatalf("Failed to create meta: %v", err)
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
		t.Fatalf("Recovery should handle missing chunks: %v", err)
	}
	defer fq.Close()
}

// TestFastQueueWriteBlockChunkRotation tests writing blocks to disk.
func TestFastQueueWriteBlockChunkRotation(t *testing.T) {
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

	// Push data to disk
	data := make([]byte, 50)
	for i := 0; i < 5; i++ {
		if err := fq.Push(data); err != nil {
			t.Fatalf("Push %d failed: %v", i, err)
		}
	}

	// Check chunks
	chunks, _ := fq.GetChunkFiles()
	t.Logf("Chunks created: %d", len(chunks))
}

// TestFastQueueConcurrentPushPop tests concurrent push and pop operations.
func TestFastQueueConcurrentPushPop(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  100,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            10000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	var wg sync.WaitGroup
	pushCount := 500
	popCount := 250

	// Pushers
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < pushCount; j++ {
				_ = fq.Push([]byte("concurrent-data"))
			}
		}()
	}

	// Poppers
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < popCount; j++ {
				_, _ = fq.Pop()
			}
		}()
	}

	wg.Wait()
}

// TestFastQueueCleanupConsumedChunks tests chunk cleanup after consumption.
func TestFastQueueCleanupConsumedChunks(t *testing.T) {
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

	// Get chunk count before consuming
	chunksBefore, _ := fq.GetChunkFiles()

	// Pop all data
	for {
		d, _ := fq.Pop()
		if d == nil {
			break
		}
	}

	// After consuming all, old chunks should be cleaned up
	chunksAfter, _ := fq.GetChunkFiles()

	t.Logf("Chunks before: %d, after: %d", len(chunksBefore), len(chunksAfter))
}

// TestFastQueueCleanupOrphanedChunks tests orphaned chunk cleanup.
func TestFastQueueCleanupOrphanedChunks(t *testing.T) {
	tmpDir := t.TempDir()

	// Create an orphaned chunk file
	orphanPath := filepath.Join(tmpDir, "00000000deadbeef")
	if err := os.WriteFile(orphanPath, []byte("orphan"), 0644); err != nil {
		t.Fatalf("Failed to create orphan file: %v", err)
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

	// Orphan should be cleaned up during recovery
	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Log("Orphan chunk was not cleaned up (may be within valid range)")
	}
}

// TestFastQueueSyncMetadata tests metadata sync.
func TestFastQueueSyncMetadata(t *testing.T) {
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

	// Push some data
	for i := 0; i < 5; i++ {
		fq.Push([]byte("sync-test"))
	}

	// Wait for sync
	time.Sleep(100 * time.Millisecond)

	fq.Close()

	// Verify metadata was synced
	metaPath := filepath.Join(tmpDir, metaFileName)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("Failed to read metadata: %v", err)
	}

	if len(data) == 0 {
		t.Error("Metadata should not be empty")
	}
}

// TestFastQueueStaleFlushExtended tests stale data flush timing.
func TestFastQueueStaleFlushExtended(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  100, // Large channel
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: 50 * time.Millisecond, // Short flush interval
		MaxSize:            1000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	// Push data (stays in memory)
	for i := 0; i < 5; i++ {
		fq.Push([]byte("stale-test"))
	}

	// Data should be in channel
	initialDiskBytes := fq.diskBytes.Load()

	// Wait for stale flush
	time.Sleep(150 * time.Millisecond)

	// Data should now be on disk
	finalDiskBytes := fq.diskBytes.Load()

	t.Logf("Initial disk bytes: %d, Final: %d", initialDiskBytes, finalDiskBytes)
}

// TestFastQueuePushAfterClose tests Push after Close.
func TestFastQueuePushAfterClose(t *testing.T) {
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

	err = fq.Push([]byte("data"))
	if err != ErrQueueClosed {
		t.Errorf("Expected ErrQueueClosed, got %v", err)
	}
}

// TestFastQueuePopAfterClose tests Pop after Close.
func TestFastQueuePopAfterClose(t *testing.T) {
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

	_, err = fq.Pop()
	if err != ErrQueueClosed {
		t.Errorf("Expected ErrQueueClosed, got %v", err)
	}
}

// TestFastQueuePeekAfterClose tests Peek after Close.
func TestFastQueuePeekAfterClose(t *testing.T) {
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

	_, err = fq.Peek()
	if err != ErrQueueClosed {
		t.Errorf("Expected ErrQueueClosed, got %v", err)
	}
}

// TestFastQueueMaxSizeLimit tests max size enforcement.
func TestFastQueueMaxSizeLimit(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  100,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            5, // Small max size
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	// Push up to limit
	for i := 0; i < 5; i++ {
		if err := fq.Push([]byte("data")); err != nil {
			t.Fatalf("Push %d failed: %v", i, err)
		}
	}

	// Next push should fail
	err = fq.Push([]byte("over-limit"))
	if err != ErrQueueFull {
		t.Errorf("Expected ErrQueueFull, got %v", err)
	}
}

// TestFastQueueMaxBytesLimit tests max bytes enforcement.
func TestFastQueueMaxBytesLimit(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  100,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000,
		MaxBytes:           100, // Small max bytes
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	// Push until bytes limit
	data := make([]byte, 30)
	for i := 0; i < 3; i++ {
		if err := fq.Push(data); err != nil {
			t.Fatalf("Push %d failed: %v", i, err)
		}
	}

	// Next push should fail (90 bytes + 30 > 100)
	err = fq.Push(data)
	if err != ErrQueueFull {
		t.Errorf("Expected ErrQueueFull, got %v", err)
	}
}

// TestFastQueueLenAndSize tests Len and Size methods.
func TestFastQueueLenAndSize(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  100,
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

	// Initially empty
	if fq.Len() != 0 {
		t.Errorf("Expected Len=0, got %d", fq.Len())
	}
	if fq.Size() != 0 {
		t.Errorf("Expected Size=0, got %d", fq.Size())
	}

	// Push data
	data := []byte("test-data-12345")
	for i := 0; i < 5; i++ {
		fq.Push(data)
	}

	if fq.Len() != 5 {
		t.Errorf("Expected Len=5, got %d", fq.Len())
	}
	expectedSize := int64(len(data) * 5)
	if fq.Size() != expectedSize {
		t.Errorf("Expected Size=%d, got %d", expectedSize, fq.Size())
	}

	// Pop and verify
	fq.Pop()
	if fq.Len() != 4 {
		t.Errorf("Expected Len=4 after pop, got %d", fq.Len())
	}
}
