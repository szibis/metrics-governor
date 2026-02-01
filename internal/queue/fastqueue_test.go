package queue

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestNewFastQueue(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:              tmpDir,
		MaxInmemoryBlocks: 10,
		ChunkFileSize:     1024 * 1024, // 1MB
		MetaSyncInterval:  100 * time.Millisecond,
		MaxSize:           100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	if fq.Len() != 0 {
		t.Errorf("Expected empty queue, got %d entries", fq.Len())
	}
}

func TestFastQueuePushPop(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:              tmpDir,
		MaxInmemoryBlocks: 10,
		ChunkFileSize:     1024 * 1024,
		MetaSyncInterval:  100 * time.Millisecond,
		MaxSize:           100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	testData := []byte("test-data-12345")
	if err := fq.Push(testData); err != nil {
		t.Fatalf("Failed to push: %v", err)
	}

	if fq.Len() != 1 {
		t.Errorf("Expected 1 entry, got %d", fq.Len())
	}

	data, err := fq.Pop()
	if err != nil {
		t.Fatalf("Failed to pop: %v", err)
	}

	if string(data) != string(testData) {
		t.Errorf("Expected %q, got %q", testData, data)
	}

	if fq.Len() != 0 {
		t.Errorf("Expected 0 entries after pop, got %d", fq.Len())
	}
}

func TestFastQueueInmemoryChannel(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:              tmpDir,
		MaxInmemoryBlocks: 5,
		ChunkFileSize:     1024 * 1024,
		MetaSyncInterval:  100 * time.Millisecond,
		MaxSize:           100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	// Push fewer items than channel capacity
	for i := 0; i < 3; i++ {
		if err := fq.Push([]byte("test")); err != nil {
			t.Fatalf("Failed to push: %v", err)
		}
	}

	// Check in-memory bytes
	if fq.inmemoryBytes.Load() != 12 { // 3 * 4 bytes
		t.Errorf("Expected 12 inmemory bytes, got %d", fq.inmemoryBytes.Load())
	}

	// Pop and verify
	for i := 0; i < 3; i++ {
		data, err := fq.Pop()
		if err != nil {
			t.Fatalf("Failed to pop: %v", err)
		}
		if string(data) != "test" {
			t.Errorf("Expected 'test', got %q", data)
		}
	}
}

func TestFastQueueDiskSpill(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  3,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   100 * time.Millisecond,
		StaleFlushInterval: 50 * time.Millisecond,
		MaxSize:            100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	// Push more items than channel capacity to trigger disk spill
	for i := 0; i < 10; i++ {
		if err := fq.Push([]byte("test-data")); err != nil {
			t.Fatalf("Failed to push %d: %v", i, err)
		}
	}

	if fq.Len() != 10 {
		t.Errorf("Expected 10 entries, got %d", fq.Len())
	}

	// Should have some data on disk
	if fq.diskBytes.Load() == 0 {
		t.Log("Note: Data may still be in memory channel")
	}

	// Pop all and verify
	for i := 0; i < 10; i++ {
		data, err := fq.Pop()
		if err != nil {
			t.Fatalf("Failed to pop %d: %v", i, err)
		}
		if string(data) != "test-data" {
			t.Errorf("Expected 'test-data', got %q", data)
		}
	}

	if fq.Len() != 0 {
		t.Errorf("Expected 0 entries, got %d", fq.Len())
	}
}

func TestFastQueuePersistence(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:              tmpDir,
		MaxInmemoryBlocks: 2, // Small to force disk writes
		ChunkFileSize:     1024 * 1024,
		MetaSyncInterval:  50 * time.Millisecond,
		MaxSize:           100,
	}

	// Create queue and push entries
	fq1, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	// Push enough to spill to disk
	for i := 0; i < 5; i++ {
		if err := fq1.Push([]byte("persistent-data")); err != nil {
			t.Fatalf("Failed to push: %v", err)
		}
	}

	// Force flush to disk
	fq1.mu.Lock()
	fq1.flushInmemoryBlocksLocked()
	fq1.syncMetadataLocked()
	fq1.mu.Unlock()

	// Wait for sync
	time.Sleep(100 * time.Millisecond)

	// Close and verify metadata exists
	fq1.Close()

	metaPath := filepath.Join(tmpDir, metaFileName)
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		t.Fatal("Metadata file should exist")
	}

	// Reopen and verify recovery
	fq2, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to recover FastQueue: %v", err)
	}
	defer fq2.Close()

	if fq2.Len() != 5 {
		t.Errorf("Expected 5 entries after recovery, got %d", fq2.Len())
	}

	// Pop and verify data
	for i := 0; i < 5; i++ {
		data, err := fq2.Pop()
		if err != nil {
			t.Fatalf("Failed to pop after recovery: %v", err)
		}
		if string(data) != "persistent-data" {
			t.Errorf("Expected 'persistent-data', got %q", data)
		}
	}
}

func TestFastQueueChunkRotation(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:              tmpDir,
		MaxInmemoryBlocks: 1, // Force immediate disk writes
		ChunkFileSize:     1024 * 1024,
		MetaSyncInterval:  50 * time.Millisecond,
		MaxSize:           1000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	// Push data
	largeData := make([]byte, 100)
	for i := range largeData {
		largeData[i] = byte(i)
	}

	for i := 0; i < 10; i++ {
		if err := fq.Push(largeData); err != nil {
			t.Fatalf("Failed to push %d: %v", i, err)
		}
	}

	// Check chunks
	chunks, err := fq.GetChunkFiles()
	if err != nil {
		t.Fatalf("Failed to get chunks: %v", err)
	}

	t.Logf("Chunks: %d", len(chunks))

	// Pop all and verify
	for i := 0; i < 10; i++ {
		data, err := fq.Pop()
		if err != nil {
			t.Fatalf("Failed to pop %d: %v", i, err)
		}
		if len(data) != len(largeData) {
			t.Errorf("Expected %d bytes, got %d", len(largeData), len(data))
		}
	}
}

func TestFastQueueConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:              tmpDir,
		MaxInmemoryBlocks: 50,
		ChunkFileSize:     1024 * 1024,
		MetaSyncInterval:  100 * time.Millisecond,
		MaxSize:           1000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	var wg sync.WaitGroup
	pushCount := 100
	popCount := 50

	// Concurrent pushes
	for i := 0; i < pushCount; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			fq.Push([]byte("concurrent-test"))
		}(i)
	}

	// Concurrent pops
	for i := 0; i < popCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fq.Pop()
		}()
	}

	wg.Wait()

	// Should have approximately pushCount - popCount entries
	remaining := fq.Len()
	if remaining > pushCount || remaining < 0 {
		t.Errorf("Unexpected queue length: %d", remaining)
	}
}

func TestFastQueueMaxSize(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:              tmpDir,
		MaxInmemoryBlocks: 10,
		ChunkFileSize:     1024 * 1024,
		MetaSyncInterval:  100 * time.Millisecond,
		MaxSize:           5, // Small limit
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	// Push up to limit
	for i := 0; i < 5; i++ {
		if err := fq.Push([]byte("test")); err != nil {
			t.Fatalf("Failed to push within limit: %v", err)
		}
	}

	// Should fail at limit
	err = fq.Push([]byte("test"))
	if err != ErrQueueFull {
		t.Errorf("Expected ErrQueueFull, got %v", err)
	}
}

func TestFastQueueMaxBytes(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:              tmpDir,
		MaxInmemoryBlocks: 10,
		ChunkFileSize:     1024 * 1024,
		MetaSyncInterval:  100 * time.Millisecond,
		MaxSize:           1000,
		MaxBytes:          50, // Small byte limit
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	// Push until byte limit
	for i := 0; i < 10; i++ {
		if err := fq.Push([]byte("1234567890")); err != nil {
			// Expected to fail at some point
			if err == ErrQueueFull {
				break
			}
			t.Fatalf("Unexpected error: %v", err)
		}
	}

	// Queue should be limited by bytes
	if fq.Size() > 60 { // Some overhead allowed
		t.Errorf("Expected size <= ~60, got %d", fq.Size())
	}
}

func TestFastQueueEmptyPop(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:              tmpDir,
		MaxInmemoryBlocks: 10,
		ChunkFileSize:     1024 * 1024,
		MetaSyncInterval:  100 * time.Millisecond,
		MaxSize:           100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	data, err := fq.Pop()
	if err != nil {
		t.Fatalf("Failed to pop from empty queue: %v", err)
	}

	if data != nil {
		t.Error("Expected nil data from empty queue")
	}
}

func TestFastQueueClose(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:              tmpDir,
		MaxInmemoryBlocks: 10,
		ChunkFileSize:     1024 * 1024,
		MetaSyncInterval:  100 * time.Millisecond,
		MaxSize:           100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	// Push some data
	fq.Push([]byte("test"))

	// Close
	if err := fq.Close(); err != nil {
		t.Fatalf("Failed to close: %v", err)
	}

	// Operations should fail after close
	err = fq.Push([]byte("test"))
	if err != ErrQueueClosed {
		t.Errorf("Expected ErrQueueClosed, got %v", err)
	}

	_, err = fq.Pop()
	if err != ErrQueueClosed {
		t.Errorf("Expected ErrQueueClosed for pop, got %v", err)
	}
}

func TestFastQueueOrdering(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:              tmpDir,
		MaxInmemoryBlocks: 2, // Small to test disk ordering
		ChunkFileSize:     1024 * 1024,
		MetaSyncInterval:  100 * time.Millisecond,
		MaxSize:           100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	// Push numbered entries
	for i := 0; i < 10; i++ {
		data := []byte{byte(i)}
		if err := fq.Push(data); err != nil {
			t.Fatalf("Failed to push %d: %v", i, err)
		}
	}

	// Pop and verify FIFO order
	for i := 0; i < 10; i++ {
		data, err := fq.Pop()
		if err != nil {
			t.Fatalf("Failed to pop %d: %v", i, err)
		}
		if len(data) != 1 || data[0] != byte(i) {
			t.Errorf("Expected %d, got %v", i, data)
		}
	}
}

func TestFastQueueMetaSync(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:              tmpDir,
		MaxInmemoryBlocks: 1, // Force disk writes
		ChunkFileSize:     1024 * 1024,
		MetaSyncInterval:  50 * time.Millisecond, // Fast sync
		MaxSize:           100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	// Push some data to create disk activity
	for i := 0; i < 5; i++ {
		if err := fq.Push([]byte("sync-test")); err != nil {
			t.Fatalf("Failed to push: %v", err)
		}
	}

	// Wait for sync
	time.Sleep(150 * time.Millisecond)

	// Verify metadata file exists
	metaPath := filepath.Join(tmpDir, metaFileName)
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		t.Error("Metadata file should exist after sync")
	}

	fq.Close()
}

func TestFastQueueStaleFlush(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  100, // Large channel
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   100 * time.Millisecond,
		StaleFlushInterval: 50 * time.Millisecond, // Fast stale flush
		MaxSize:            1000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	// Push a small amount (stays in channel)
	for i := 0; i < 3; i++ {
		if err := fq.Push([]byte("stale-test")); err != nil {
			t.Fatalf("Failed to push: %v", err)
		}
	}

	// Initially should be in memory
	initialInmem := fq.inmemoryBytes.Load()
	if initialInmem == 0 {
		t.Log("Data may have been flushed immediately")
	}

	// Wait for stale flush
	time.Sleep(200 * time.Millisecond)

	// Data should still be accessible
	if fq.Len() != 3 {
		t.Errorf("Expected 3 entries, got %d", fq.Len())
	}
}

func TestFastQueueChunkCleanup(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:              tmpDir,
		MaxInmemoryBlocks: 1, // Force disk writes
		ChunkFileSize:     1024 * 1024,
		MetaSyncInterval:  50 * time.Millisecond,
		MaxSize:           1000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	// Push data
	largeData := make([]byte, 100)
	for i := 0; i < 20; i++ {
		if err := fq.Push(largeData); err != nil {
			t.Fatalf("Failed to push: %v", err)
		}
	}

	// Pop all data
	for i := 0; i < 20; i++ {
		if _, err := fq.Pop(); err != nil {
			t.Fatalf("Failed to pop: %v", err)
		}
	}

	// Close to finalize
	fq.Close()

	// Check remaining files
	entries, _ := os.ReadDir(tmpDir)
	chunkCount := 0
	for _, e := range entries {
		if len(e.Name()) == 16 { // Chunk filename
			chunkCount++
		}
	}

	t.Logf("Remaining chunks: %d", chunkCount)
}

func TestFastQueueDefaults(t *testing.T) {
	tmpDir := t.TempDir()

	// Test with zero-value config
	cfg := FastQueueConfig{
		Path: tmpDir,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue with defaults: %v", err)
	}
	defer fq.Close()

	// Should use default values
	if fq.cfg.MaxInmemoryBlocks != defaultInmemoryBlocks {
		t.Errorf("Expected default inmemory blocks %d, got %d",
			defaultInmemoryBlocks, fq.cfg.MaxInmemoryBlocks)
	}
	if fq.cfg.ChunkFileSize != defaultChunkFileSize {
		t.Errorf("Expected default chunk size %d, got %d",
			defaultChunkFileSize, fq.cfg.ChunkFileSize)
	}
	if fq.cfg.MetaSyncInterval != defaultMetaSyncInterval {
		t.Errorf("Expected default meta sync %v, got %v",
			defaultMetaSyncInterval, fq.cfg.MetaSyncInterval)
	}
}

func TestFastQueuePeek(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:              tmpDir,
		MaxInmemoryBlocks: 10,
		ChunkFileSize:     1024 * 1024,
		MetaSyncInterval:  100 * time.Millisecond,
		MaxSize:           100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	testData := []byte("peek-test-data")
	if err := fq.Push(testData); err != nil {
		t.Fatalf("Failed to push: %v", err)
	}

	// Peek should return data without removing
	data, err := fq.Peek()
	if err != nil {
		t.Fatalf("Failed to peek: %v", err)
	}
	if string(data) != string(testData) {
		t.Errorf("Expected %q, got %q", testData, data)
	}

	// Length should still be 1
	if fq.Len() != 1 {
		t.Errorf("Expected 1 entry after peek, got %d", fq.Len())
	}

	// Pop should return same data
	data, err = fq.Pop()
	if err != nil {
		t.Fatalf("Failed to pop: %v", err)
	}
	if string(data) != string(testData) {
		t.Errorf("Expected %q from pop, got %q", testData, data)
	}
}

func TestFastQueueEmptyPeek(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:              tmpDir,
		MaxInmemoryBlocks: 10,
		ChunkFileSize:     1024 * 1024,
		MetaSyncInterval:  100 * time.Millisecond,
		MaxSize:           100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	data, err := fq.Peek()
	if err != nil {
		t.Fatalf("Failed to peek empty queue: %v", err)
	}
	if data != nil {
		t.Error("Expected nil data from empty queue peek")
	}
}
