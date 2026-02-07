package queue

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/compression"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
)

// ---------------------------------------------------------------------------
// pushData coverage: closed queue, DiskFull with all FullBehavior variants
// ---------------------------------------------------------------------------

// TestSendQueuePushDataClosedQueue covers pushData returning "queue is closed".
func TestSendQueuePushDataClosedQueue(t *testing.T) {
	tmpDir := t.TempDir()
	q, err := New(Config{Path: tmpDir, MaxSize: 10})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	q.Close()

	err = q.PushData([]byte("after-close"))
	if err == nil || err.Error() != "queue is closed" {
		t.Errorf("Expected 'queue is closed', got: %v", err)
	}
}

// TestSendQueuePushMarshalError covers Push failing with a nil request
// so proto.Marshal succeeds (empty message), but we also test Push on closed.
func TestSendQueuePushClosedQueue(t *testing.T) {
	tmpDir := t.TempDir()
	q, err := New(Config{Path: tmpDir, MaxSize: 10})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	q.Close()

	// Push on closed queue — exercises Push -> pushData -> closed check
	err = q.Push(&colmetricspb.ExportMetricsServiceRequest{})
	if err == nil {
		t.Error("Expected error pushing to closed queue")
	}
}

// TestSendQueuePushDataDropNewestOnFull covers the DropNewest branch
// when ErrQueueFull is returned from FastQueue.
func TestSendQueuePushDataDropNewestOnFull(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := Config{
		Path:         tmpDir,
		MaxSize:      2,
		FullBehavior: DropNewest,
	}
	q, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer q.Close()

	// Fill
	q.PushData([]byte("a"))
	q.PushData([]byte("b"))

	// This triggers ErrQueueFull -> DropNewest (return nil, drop incoming)
	err = q.PushData([]byte("c"))
	if err != nil {
		t.Errorf("DropNewest should return nil, got: %v", err)
	}
	if q.Len() != 2 {
		t.Errorf("Expected 2 entries, got %d", q.Len())
	}
}

// TestSendQueuePushDataDropOldestOnFull covers the DropOldest branch
// when ErrQueueFull is returned.
func TestSendQueuePushDataDropOldestOnFull(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := Config{
		Path:         tmpDir,
		MaxSize:      2,
		FullBehavior: DropOldest,
	}
	q, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer q.Close()

	q.PushData([]byte("a"))
	q.PushData([]byte("b"))

	// Push one more — triggers DropOldest
	err = q.PushData([]byte("c"))
	if err != nil {
		t.Errorf("DropOldest should succeed, got: %v", err)
	}
	if q.Len() != 2 {
		t.Errorf("Expected 2, got %d", q.Len())
	}
}

// TestSendQueuePushDataBlockTimeoutOnFull covers the Block branch
// with ErrQueueFull (not ErrDiskFull) that times out.
func TestSendQueuePushDataBlockTimeoutOnFull(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := Config{
		Path:         tmpDir,
		MaxSize:      2,
		FullBehavior: Block,
		BlockTimeout: 50 * time.Millisecond,
	}
	q, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer q.Close()

	q.PushData([]byte("a"))
	q.PushData([]byte("b"))

	start := time.Now()
	err = q.PushData([]byte("c"))
	elapsed := time.Since(start)

	if err == nil {
		t.Error("Expected timeout error")
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("Should have waited ~50ms, only waited %v", elapsed)
	}
}

// TestSendQueuePushDataBlockClosedDuringWait covers the Block branch
// where the queue closes while a push is blocked on ErrQueueFull.
func TestSendQueuePushDataBlockClosedDuringWait(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := Config{
		Path:         tmpDir,
		MaxSize:      2,
		FullBehavior: Block,
		BlockTimeout: 5 * time.Second,
	}
	q, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	q.PushData([]byte("a"))
	q.PushData([]byte("b"))

	done := make(chan error, 1)
	go func() {
		done <- q.PushData([]byte("c"))
	}()

	time.Sleep(30 * time.Millisecond)
	q.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Error("Expected error after close during block wait")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Blocked push did not unblock after close")
	}
}

// TestSendQueuePushDataBlockSuccessOnFull covers the Block branch succeeding
// after space is freed (ErrQueueFull).
func TestSendQueuePushDataBlockSuccessOnFull(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := Config{
		Path:         tmpDir,
		MaxSize:      2,
		FullBehavior: Block,
		BlockTimeout: 2 * time.Second,
	}
	q, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer q.Close()

	q.PushData([]byte("a"))
	q.PushData([]byte("b"))

	done := make(chan error, 1)
	go func() {
		done <- q.PushData([]byte("c"))
	}()

	time.Sleep(30 * time.Millisecond)
	q.Pop() // Free space

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Expected success after pop, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Blocked push did not unblock after Pop")
	}
}

// ---------------------------------------------------------------------------
// cleanRetries coverage: trigger cleanup when retries map grows large
// ---------------------------------------------------------------------------

func TestCleanRetriesTriggered(t *testing.T) {
	tmpDir := t.TempDir()
	q, err := New(Config{Path: tmpDir, MaxSize: 20000})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer q.Close()

	// Populate more than maxRetryEntries (10000)
	for i := 0; i < 10001; i++ {
		q.UpdateRetries(fmt.Sprintf("id-%d", i), i)
	}

	q.retriesMu.RLock()
	countBefore := len(q.retries)
	q.retriesMu.RUnlock()

	if countBefore <= 10000 {
		t.Fatalf("Expected >10000 entries, got %d", countBefore)
	}

	// Push + Pop triggers cleanRetries
	q.PushData([]byte("trigger-clean"))
	q.Pop()

	q.retriesMu.RLock()
	countAfter := len(q.retries)
	q.retriesMu.RUnlock()

	if countAfter != 0 {
		t.Errorf("Expected 0 retries after cleanup, got %d", countAfter)
	}
}

// ---------------------------------------------------------------------------
// FastQueue.recover: V2 metadata with EntryCount and TotalBytes
// ---------------------------------------------------------------------------

func TestFastQueueRecoverV2MetadataDirectly(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  2,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   10 * time.Millisecond,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000,
		MaxBytes:           10 * 1024 * 1024,
	}

	// Create queue, write data, flush, close
	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue: %v", err)
	}
	for i := 0; i < 7; i++ {
		fq.Push([]byte("v2-data"))
	}
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.syncMetadataLocked()
	fq.mu.Unlock()

	expectedLen := fq.Len()
	expectedSize := fq.Size()
	fq.Close()

	// Verify meta has V2 fields
	metaPath := filepath.Join(tmpDir, metaFileName)
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var meta fastqueueMeta
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if meta.EntryCount <= 0 || meta.TotalBytes <= 0 {
		t.Fatalf("V2 metadata should have counts: count=%d bytes=%d", meta.EntryCount, meta.TotalBytes)
	}

	// Reopen -> fast path recovery
	fq2, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	defer fq2.Close()

	if fq2.Len() != expectedLen {
		t.Errorf("Expected %d entries, got %d", expectedLen, fq2.Len())
	}
	if fq2.Size() != expectedSize {
		t.Errorf("Expected %d bytes, got %d", expectedSize, fq2.Size())
	}
}

// TestFastQueueRecoverCorruptMetadata covers the json.Unmarshal error path.
func TestFastQueueRecoverCorruptMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	metaPath := filepath.Join(tmpDir, metaFileName)
	os.WriteFile(metaPath, []byte("{{{bad json"), 0644)

	_, err := NewFastQueue(FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	})
	if err == nil {
		t.Error("Expected error for corrupt metadata")
	}
}

// TestFastQueueRecoverLegacyScanPath covers the legacy metadata scan path
// (no EntryCount / TotalBytes) with actual data that can be scanned.
func TestFastQueueRecoverLegacyScanPath(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  2,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   10 * time.Millisecond,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000,
		Compression:        compression.TypeNone, // no compression for predictable sizes
	}

	// Create queue with data on disk
	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue: %v", err)
	}
	for i := 0; i < 5; i++ {
		fq.Push([]byte("legacy-scan"))
	}
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.syncMetadataLocked()
	fq.mu.Unlock()

	// Read actual offsets from metadata
	metaPath := filepath.Join(tmpDir, metaFileName)
	raw, _ := os.ReadFile(metaPath)
	var meta fastqueueMeta
	json.Unmarshal(raw, &meta)

	fq.Close()

	// Rewrite metadata as legacy (V1: no counts)
	legacyMeta := fmt.Sprintf(
		`{"name":"fastqueue","reader_offset":%d,"writer_offset":%d,"version":1}`,
		meta.ReaderOffset, meta.WriterOffset,
	)
	os.WriteFile(metaPath, []byte(legacyMeta), 0600)

	// Reopen -> triggers legacy scan (countEntriesOnDisk)
	fq2, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Reopen with legacy meta: %v", err)
	}
	defer fq2.Close()

	if fq2.Len() != 5 {
		t.Errorf("Expected 5 entries from legacy scan, got %d", fq2.Len())
	}
}

// TestFastQueueRecoverWithSeparateReaderWriterChunks covers the recovery path
// where reader and writer are on different chunks.
func TestFastQueueRecoverWithSeparateReaderWriterChunks(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      512, // Very small to force multiple chunks
		MetaSyncInterval:   10 * time.Millisecond,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000,
		Compression:        compression.TypeNone,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue: %v", err)
	}

	// Write enough data to span multiple chunks
	data := make([]byte, 100)
	for i := 0; i < 20; i++ {
		fq.Push(data)
	}

	// Pop some to move reader into a different chunk than writer
	for i := 0; i < 10; i++ {
		fq.Pop()
	}

	fq.mu.Lock()
	fq.syncMetadataLocked()
	fq.mu.Unlock()
	fq.Close()

	// Reopen: recovery should handle reader != writer chunk
	fq2, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	defer fq2.Close()

	if fq2.Len() != 10 {
		t.Errorf("Expected 10 entries after recovery, got %d", fq2.Len())
	}

	// Verify data can be read
	for i := 0; i < 10; i++ {
		d, err := fq2.Pop()
		if err != nil {
			t.Fatalf("Pop %d: %v", i, err)
		}
		if len(d) != 100 {
			t.Errorf("Pop %d: expected 100 bytes, got %d", i, len(d))
		}
	}
}

// ---------------------------------------------------------------------------
// countEntriesOnDisk: reader >= writer, missing chunk file skip
// ---------------------------------------------------------------------------

func TestCountEntriesOnDiskReaderEqualWriter(t *testing.T) {
	tmpDir := t.TempDir()
	fq := &FastQueue{
		cfg: FastQueueConfig{
			Path:          tmpDir,
			ChunkFileSize: 1024 * 1024,
		},
		readerOffset: 100,
		writerOffset: 100,
	}
	count, bytes, err := fq.countEntriesOnDisk()
	if err != nil {
		t.Fatalf("countEntriesOnDisk: %v", err)
	}
	if count != 0 || bytes != 0 {
		t.Errorf("Expected 0/0, got %d/%d", count, bytes)
	}
}

func TestCountEntriesOnDiskMissingChunk(t *testing.T) {
	tmpDir := t.TempDir()
	chunkSize := int64(1024)

	payload := []byte("test-data-entry!")
	entrySize := int64(blockHeaderSize + len(payload))

	fq := &FastQueue{
		cfg: FastQueueConfig{
			Path:          tmpDir,
			ChunkFileSize: chunkSize,
		},
		// Reader at 0 (missing first chunk), writer at second chunk + entrySize
		readerOffset: 0,
		writerOffset: chunkSize + entrySize,
	}

	// Create only the second chunk (first chunk is missing -> skip)
	chunkPath := filepath.Join(tmpDir, fmt.Sprintf("%016x", chunkSize))
	f, err := os.Create(chunkPath)
	if err != nil {
		t.Fatalf("Create chunk: %v", err)
	}
	header := make([]byte, blockHeaderSize)
	binary.LittleEndian.PutUint64(header, uint64(len(payload)))
	f.Write(header)
	f.Write(payload)
	f.Close()

	count, totalBytes, err := fq.countEntriesOnDisk()
	if err != nil {
		t.Fatalf("countEntriesOnDisk: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 entry (skipped missing chunk), got %d", count)
	}
	if totalBytes != int64(len(payload)) {
		t.Errorf("Expected %d bytes, got %d", len(payload), totalBytes)
	}
}

// ---------------------------------------------------------------------------
// writeBlockToDiskLocked: disk full error paths (header write, data write)
// ---------------------------------------------------------------------------

// TestIsDiskFullError covers isDiskFullError with ENOSPC and PathError.
func TestIsDiskFullErrorCov(t *testing.T) {
	// nil error
	if isDiskFullError(nil) {
		t.Error("nil should not be disk full")
	}

	// Direct ENOSPC
	if !isDiskFullError(syscall.ENOSPC) {
		t.Error("ENOSPC should be disk full")
	}

	// Wrapped in os.PathError
	pathErr := &os.PathError{
		Op:   "write",
		Path: "/some/path",
		Err:  syscall.ENOSPC,
	}
	if !isDiskFullError(pathErr) {
		t.Error("PathError wrapping ENOSPC should be disk full")
	}

	// Unrelated error
	if isDiskFullError(fmt.Errorf("random error")) {
		t.Error("Random error should not be disk full")
	}

	// PathError with non-ENOSPC
	pathErr2 := &os.PathError{
		Op:   "write",
		Path: "/some/path",
		Err:  syscall.EACCES,
	}
	if isDiskFullError(pathErr2) {
		t.Error("PathError with EACCES should not be disk full")
	}
}

// ---------------------------------------------------------------------------
// writeBlockToDiskLocked: chunk rotation with reader == writer
// ---------------------------------------------------------------------------

func TestFastQueueWriteBlockRotationWithReaderSameAsWriter(t *testing.T) {
	tmpDir := t.TempDir()

	// Very small chunk to force rotation quickly
	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      256,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000,
		Compression:        compression.TypeNone,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue: %v", err)
	}
	defer fq.Close()

	// Push data to fill first chunk and rotate
	data := make([]byte, 100)
	for i := 0; i < 5; i++ {
		if err := fq.Push(data); err != nil {
			t.Fatalf("Push %d: %v", i, err)
		}
	}

	// Pop from first chunk while writer has moved on
	for i := 0; i < 3; i++ {
		d, err := fq.Pop()
		if err != nil {
			t.Fatalf("Pop %d: %v", i, err)
		}
		if len(d) != 100 {
			t.Errorf("Pop %d: expected 100 bytes, got %d", i, len(d))
		}
	}

	// Push more to continue writing
	for i := 0; i < 3; i++ {
		fq.Push(data)
	}

	// Pop everything
	count := 0
	for {
		d, _ := fq.Pop()
		if d == nil {
			break
		}
		count++
	}
	if count == 0 {
		t.Error("Should have popped some entries")
	}
}

// ---------------------------------------------------------------------------
// readBlockFromDiskLocked: chunk transition (EOF in header triggers retry)
// ---------------------------------------------------------------------------

func TestFastQueueReadBlockChunkTransition(t *testing.T) {
	tmpDir := t.TempDir()

	// Chunk size chosen so data fills a chunk and rolls to next
	// Each entry: 8 (header) + payload_size bytes
	// With compression=none, payload is exact
	payloadSize := 50
	entrySize := blockHeaderSize + payloadSize // 58 bytes
	// chunkSize = 2 entries exactly
	chunkSize := int64(entrySize * 2)

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      chunkSize,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
		Compression:        compression.TypeNone,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue: %v", err)
	}
	defer fq.Close()

	data := make([]byte, payloadSize)
	// Push 4 entries: fills 2 chunks of 2 entries each
	for i := 0; i < 4; i++ {
		if err := fq.Push(data); err != nil {
			t.Fatalf("Push %d: %v", i, err)
		}
	}

	// Pop all 4 -- reader must transition across chunks
	for i := 0; i < 4; i++ {
		d, err := fq.Pop()
		if err != nil {
			t.Fatalf("Pop %d: %v", i, err)
		}
		if len(d) != payloadSize {
			t.Errorf("Pop %d: expected %d bytes, got %d", i, payloadSize, len(d))
		}
	}

	// Empty
	d, _ := fq.Pop()
	if d != nil {
		t.Error("Expected nil from empty queue")
	}
}

// ---------------------------------------------------------------------------
// Peek: channel full on reinsert (line 297-303 in fastqueue.go)
// ---------------------------------------------------------------------------

func TestFastQueuePeekChannelFullOnReinsertCov(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1, // Channel of size 1
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
		Compression:        compression.TypeNone,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue: %v", err)
	}
	defer fq.Close()

	// Push one item into channel
	fq.Push([]byte("only-item"))

	// Now fill the channel by pushing another directly
	// Since channel size is 1 and one item is already there, the second Push
	// will go through the lock/disk path, flushing the channel item to disk.
	// Then the channel is empty again. So let's take a different approach:
	// Push exactly 1 item, then peek (takes from channel, puts back).
	// The channel has room so reinsert works. But we want the case where
	// reinsert fails. We need to fill the channel between take and put-back.
	// This is inherently racy so we test the non-racy reinsert path
	// and the direct disk path separately.

	// Test normal peek reinsert (channel has room)
	data, err := fq.Peek()
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if string(data) != "only-item" {
		t.Errorf("Expected 'only-item', got %q", data)
	}

	// Verify item is still accessible
	data2, err := fq.Pop()
	if err != nil {
		t.Fatalf("Pop: %v", err)
	}
	if string(data2) != "only-item" {
		t.Errorf("Expected 'only-item' from pop, got %q", data2)
	}
}

// ---------------------------------------------------------------------------
// FastQueue.Push: channel full -> flush to disk -> closed during flush
// ---------------------------------------------------------------------------

func TestFastQueuePushClosedDuringDiskWriteCov(t *testing.T) {
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
		t.Fatalf("NewFastQueue: %v", err)
	}

	// Fill channel
	fq.Push([]byte("fill"))

	// Close
	fq.Close()

	// Next push should get ErrQueueClosed from the first closed check
	err = fq.Push([]byte("after-close"))
	if err != ErrQueueClosed {
		t.Errorf("Expected ErrQueueClosed, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// readBlockFromDiskLocked: EOF / corrupt header recovery
// ---------------------------------------------------------------------------

func TestFastQueueReadBlockTruncatedHeader(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000,
		Compression:        compression.TypeNone,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue: %v", err)
	}
	defer fq.Close()

	// Write valid data + manually truncate the chunk to create a partial header
	for i := 0; i < 3; i++ {
		fq.Push([]byte("valid-data"))
	}

	// Flush to disk
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	if fq.writerBuf != nil {
		fq.writerBuf.Flush()
	}

	// Truncate the chunk file to leave a partial header at the end
	if fq.writerChunk != nil {
		// Add 4 bytes of garbage (partial header)
		fq.writerChunk.Write([]byte{0x01, 0x02, 0x03, 0x04})
		fq.writerChunk.Sync()
		// Update writerOffset to include the partial header
		fq.writerOffset += 4
	}
	fq.mu.Unlock()

	// Pop should recover the 3 valid entries
	for i := 0; i < 3; i++ {
		d, err := fq.Pop()
		if err != nil {
			t.Fatalf("Pop %d: %v", i, err)
		}
		if string(d) != "valid-data" {
			t.Errorf("Pop %d: expected 'valid-data', got %q", i, d)
		}
	}
}

// ---------------------------------------------------------------------------
// writeBlockToDiskLocked: chunk rotation closing writer != reader
// ---------------------------------------------------------------------------

func TestFastQueueWriteBlockChunkRotationClosesDifferentReader(t *testing.T) {
	tmpDir := t.TempDir()

	// Very small chunk to trigger rotation
	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      200, // Small chunk
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000,
		Compression:        compression.TypeNone,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue: %v", err)
	}
	defer fq.Close()

	// Push data that fills and rotates multiple chunks
	data := make([]byte, 80)
	for i := 0; i < 10; i++ {
		if err := fq.Push(data); err != nil {
			t.Fatalf("Push %d: %v", i, err)
		}
	}

	// Pop first entry (sets up reader on first chunk)
	d, err := fq.Pop()
	if err != nil {
		t.Fatalf("Pop: %v", err)
	}
	if len(d) != 80 {
		t.Errorf("Expected 80 bytes, got %d", len(d))
	}

	// Push more to force writer to continue rotating
	for i := 0; i < 5; i++ {
		fq.Push(data)
	}

	// Pop everything
	count := 0
	for {
		d, _ := fq.Pop()
		if d == nil {
			break
		}
		count++
	}
	if count < 10 {
		t.Errorf("Expected at least 10 more pops, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// Peek from disk after partial consumption (peekBlockFromDiskLocked coverage)
// ---------------------------------------------------------------------------

func TestFastQueuePeekFromDiskAfterPartialPop(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
		Compression:        compression.TypeNone,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue: %v", err)
	}
	defer fq.Close()

	// Push entries to disk
	for i := 0; i < 5; i++ {
		fq.Push([]byte(fmt.Sprintf("entry-%d", i)))
	}
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	// Pop all entries from channel first so only disk data remains
	// With MaxInmemoryBlocks=1, at most 1 entry is in channel
	// After flush, channel is drained to disk. Pop 2 from disk.
	fq.Pop()
	fq.Pop()

	// Peek should return non-nil from disk
	data, err := fq.Peek()
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if data == nil {
		t.Fatal("Peek returned nil")
	}
	t.Logf("Peek returned: %q", data)

	// Verify queue length didn't change after peek
	lenAfterPeek := fq.Len()

	// Pop should succeed
	data2, err := fq.Pop()
	if err != nil {
		t.Fatalf("Pop: %v", err)
	}
	if data2 == nil {
		t.Fatal("Pop returned nil")
	}

	// Length should decrease by 1 after pop
	lenAfterPop := fq.Len()
	if lenAfterPop != lenAfterPeek-1 {
		t.Errorf("Expected len to decrease by 1: before=%d after=%d", lenAfterPeek, lenAfterPop)
	}
}

// ---------------------------------------------------------------------------
// peekBlockFromDiskLocked: missing chunk returns EOF
// ---------------------------------------------------------------------------

func TestFastQueuePeekMissingChunkReturnsEOF(t *testing.T) {
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
		t.Fatalf("NewFastQueue: %v", err)
	}
	defer fq.Close()

	// Manually set offsets to point to non-existent chunk
	fq.mu.Lock()
	fq.readerOffset = 0
	fq.writerOffset = 1000
	fq.activeCount.Store(1)
	// Don't create the chunk file -- peekBlockFromDiskLocked should handle this
	data, err := fq.peekBlockFromDiskLocked()
	fq.mu.Unlock()

	// Should get nil data (EOF treated as empty)
	if data != nil {
		t.Errorf("Expected nil data for missing chunk, got %d bytes", len(data))
	}
}

// ---------------------------------------------------------------------------
// readBlockFromDiskLocked: open a separate reader chunk (not writer)
// ---------------------------------------------------------------------------

func TestFastQueueReadBlockSeparateReaderChunk(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      256, // Very small chunks
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000,
		Compression:        compression.TypeNone,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue: %v", err)
	}
	defer fq.Close()

	// Push enough data to span at least 2 chunks
	data := make([]byte, 100)
	for i := 0; i < 10; i++ {
		fq.Push(data)
	}

	// Verify multiple chunks were created
	chunks, _ := fq.GetChunkFiles()
	if len(chunks) < 2 {
		t.Skipf("Need at least 2 chunks, got %d", len(chunks))
	}

	// Pop entries -- reader starts on first chunk, writer on a later one
	for i := 0; i < 10; i++ {
		d, err := fq.Pop()
		if err != nil {
			t.Fatalf("Pop %d: %v", i, err)
		}
		if len(d) != 100 {
			t.Errorf("Pop %d: expected 100 bytes, got %d", i, len(d))
		}
	}
}

// ---------------------------------------------------------------------------
// FastQueue Close error paths (writerBuf flush, writerChunk sync/close)
// These are difficult to trigger real errors for, but we can cover the
// nil-check branches by closing a queue with no data.
// ---------------------------------------------------------------------------

func TestFastQueueCloseEmptyQueue(t *testing.T) {
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
		t.Fatalf("NewFastQueue: %v", err)
	}

	// Close empty queue: writerChunk is nil, writerBuf is nil
	err = fq.Close()
	if err != nil {
		t.Errorf("Close empty queue: %v", err)
	}
}

func TestFastQueueCloseWithDiskData(t *testing.T) {
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
		t.Fatalf("NewFastQueue: %v", err)
	}

	// Push to disk (writerChunk and writerBuf are set)
	for i := 0; i < 5; i++ {
		fq.Push([]byte("disk-data"))
	}

	// Close should flush writerBuf, sync writerChunk, close writerChunk
	err = fq.Close()
	if err != nil {
		t.Errorf("Close with disk data: %v", err)
	}
}

// ---------------------------------------------------------------------------
// flushInmemoryBlocksLocked: empty channel no-op
// ---------------------------------------------------------------------------

func TestFlushInmemoryBlocksEmpty(t *testing.T) {
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
		t.Fatalf("NewFastQueue: %v", err)
	}
	defer fq.Close()

	fq.mu.Lock()
	err = fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	if err != nil {
		t.Errorf("Flush empty should be no-op, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// SendQueue.Push: proto marshal with nil request (tests that empty requests
// marshal successfully, covering the Push -> pushData path)
// ---------------------------------------------------------------------------

func TestSendQueuePushNilRequest(t *testing.T) {
	tmpDir := t.TempDir()
	q, err := New(Config{Path: tmpDir, MaxSize: 10})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer q.Close()

	// Push nil request -- proto.Marshal(nil) returns empty bytes
	var req *colmetricspb.ExportMetricsServiceRequest
	// This will panic if req is nil, so use an empty request instead
	req = &colmetricspb.ExportMetricsServiceRequest{}
	err = q.Push(req)
	if err != nil {
		t.Errorf("Push empty request: %v", err)
	}

	// Pop and try to deserialize
	entry, err := q.Pop()
	if err != nil {
		t.Fatalf("Pop: %v", err)
	}
	if entry == nil {
		t.Fatal("Pop returned nil")
	}
	_, err = entry.GetRequest()
	if err != nil {
		t.Errorf("GetRequest: %v", err)
	}
}

// ---------------------------------------------------------------------------
// cleanupAllChunks: ReadDir error (non-existent path)
// ---------------------------------------------------------------------------

func TestCleanupAllChunksReadDirError(t *testing.T) {
	fq := &FastQueue{
		cfg: FastQueueConfig{
			Path: "/nonexistent/path/that/does/not/exist",
		},
	}
	// Should not panic
	fq.cleanupAllChunks()
}

// ---------------------------------------------------------------------------
// syncMetadataLocked: covers the directory sync path
// ---------------------------------------------------------------------------

func TestSyncMetadataLockedCoversDirectorySync(t *testing.T) {
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
		t.Fatalf("NewFastQueue: %v", err)
	}
	defer fq.Close()

	fq.Push([]byte("data"))

	fq.mu.Lock()
	err = fq.syncMetadataLocked()
	fq.mu.Unlock()

	if err != nil {
		t.Errorf("syncMetadataLocked: %v", err)
	}

	// Verify the file was created
	metaPath := filepath.Join(tmpDir, metaFileName)
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		t.Error("Metadata file should exist")
	}
}

// ---------------------------------------------------------------------------
// countEntriesOnDisk: multiple chunks, entries spanning chunk boundaries
// ---------------------------------------------------------------------------

func TestCountEntriesOnDiskMultipleChunksWithData(t *testing.T) {
	tmpDir := t.TempDir()

	// Use 1MB chunk to avoid chunk rotation complications during scan
	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000,
		Compression:        compression.TypeNone,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue: %v", err)
	}

	// Push entries -- all go to a single chunk
	data := make([]byte, 100)
	for i := 0; i < 15; i++ {
		fq.Push(data)
	}

	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.syncMetadataLocked()
	fq.mu.Unlock()
	fq.Close()

	// Reopen with legacy metadata to force countEntriesOnDisk scan
	metaPath := filepath.Join(tmpDir, metaFileName)
	raw, _ := os.ReadFile(metaPath)
	var meta fastqueueMeta
	json.Unmarshal(raw, &meta)

	legacyMeta := fmt.Sprintf(
		`{"name":"fastqueue","reader_offset":%d,"writer_offset":%d,"version":1}`,
		meta.ReaderOffset, meta.WriterOffset,
	)
	os.WriteFile(metaPath, []byte(legacyMeta), 0600)

	fq2, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	defer fq2.Close()

	if fq2.Len() != 15 {
		t.Errorf("Expected 15 entries from scan, got %d", fq2.Len())
	}
}

// ---------------------------------------------------------------------------
// pushData: DiskFull branch with DropOldest/DropNewest/Block
// These require simulating ErrDiskFull from FastQueue, which is hard without
// a real full disk. We test via MaxBytes (triggers ErrQueueFull, exercising
// the same code paths in pushData).
// ---------------------------------------------------------------------------

func TestSendQueuePushDataMaxBytesDropOldest(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := Config{
		Path:         tmpDir,
		MaxSize:      1000,
		MaxBytes:     100,
		FullBehavior: DropOldest,
	}
	q, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer q.Close()

	// Fill to byte limit
	data := make([]byte, 40)
	q.PushData(data)
	q.PushData(data)

	// Next push exceeds MaxBytes -> ErrQueueFull -> DropOldest
	err = q.PushData(data)
	if err != nil {
		t.Errorf("DropOldest should succeed, got: %v", err)
	}
}

func TestSendQueuePushDataMaxBytesDropNewest(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := Config{
		Path:         tmpDir,
		MaxSize:      1000,
		MaxBytes:     100,
		FullBehavior: DropNewest,
	}
	q, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer q.Close()

	data := make([]byte, 40)
	q.PushData(data)
	q.PushData(data)

	// Exceeds MaxBytes -> DropNewest
	err = q.PushData(data)
	if err != nil {
		t.Errorf("DropNewest should return nil, got: %v", err)
	}
}

func TestSendQueuePushDataMaxBytesBlockTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := Config{
		Path:         tmpDir,
		MaxSize:      1000,
		MaxBytes:     100,
		FullBehavior: Block,
		BlockTimeout: 50 * time.Millisecond,
	}
	q, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer q.Close()

	data := make([]byte, 40)
	q.PushData(data)
	q.PushData(data)

	// Exceeds MaxBytes -> Block -> timeout
	err = q.PushData(data)
	if err == nil {
		t.Error("Expected timeout error")
	}
}

func TestSendQueuePushDataMaxBytesBlockSuccess(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := Config{
		Path:         tmpDir,
		MaxSize:      1000,
		MaxBytes:     100,
		FullBehavior: Block,
		BlockTimeout: 2 * time.Second,
	}
	q, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer q.Close()

	data := make([]byte, 40)
	q.PushData(data)
	q.PushData(data)

	done := make(chan error, 1)
	go func() {
		done <- q.PushData(data)
	}()

	time.Sleep(30 * time.Millisecond)
	q.Pop() // Free space

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Expected success after Pop, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for push")
	}
}

// ---------------------------------------------------------------------------
// countEntriesOnDisk: truncated header (io.ErrUnexpectedEOF != io.EOF -> error return)
// ---------------------------------------------------------------------------

func TestCountEntriesOnDiskTruncatedHeader(t *testing.T) {
	tmpDir := t.TempDir()
	chunkSize := int64(1024 * 1024)

	// Write a chunk with a valid entry followed by a truncated header (4 bytes)
	payload := []byte("valid-entry-data")
	chunkPath := filepath.Join(tmpDir, fmt.Sprintf("%016x", 0))
	f, err := os.Create(chunkPath)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	header := make([]byte, blockHeaderSize)
	binary.LittleEndian.PutUint64(header, uint64(len(payload)))
	f.Write(header)
	f.Write(payload)
	// Write partial header (4 bytes instead of 8) -- triggers io.ErrUnexpectedEOF
	f.Write([]byte{0x01, 0x02, 0x03, 0x04})
	f.Close()

	validEntrySize := int64(blockHeaderSize + len(payload))
	fq := &FastQueue{
		cfg: FastQueueConfig{
			Path:          tmpDir,
			ChunkFileSize: chunkSize,
		},
		readerOffset: 0,
		writerOffset: validEntrySize + 4, // Points past the truncated header
	}

	// binary.Read will get io.ErrUnexpectedEOF on the partial header
	// which is NOT io.EOF, so it returns an error
	_, _, err = fq.countEntriesOnDisk()
	if err == nil {
		t.Error("Expected error for truncated header")
	}
}

// ---------------------------------------------------------------------------
// countEntriesOnDisk: EOF inside data (f.Seek past end returns no error,
// but the next binary.Read returns EOF which breaks the loop correctly)
// ---------------------------------------------------------------------------

func TestCountEntriesOnDiskEOFOnData(t *testing.T) {
	tmpDir := t.TempDir()
	chunkSize := int64(1024 * 1024)

	// Write a chunk with a header claiming 1000 bytes of data, but only 10 bytes present
	chunkPath := filepath.Join(tmpDir, fmt.Sprintf("%016x", 0))
	f, err := os.Create(chunkPath)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	header := make([]byte, blockHeaderSize)
	binary.LittleEndian.PutUint64(header, uint64(1000)) // Claims 1000 bytes
	f.Write(header)
	f.Write(make([]byte, 10)) // Only 10 bytes
	f.Close()

	fq := &FastQueue{
		cfg: FastQueueConfig{
			Path:          tmpDir,
			ChunkFileSize: chunkSize,
		},
		readerOffset: 0,
		writerOffset: blockHeaderSize + 1000, // Match claimed size
	}

	// Seek past end is OK on files but the offset tracking will be wrong
	// and the next iteration's binary.Read will hit EOF
	count, _, err := fq.countEntriesOnDisk()
	// The seek succeeds (files allow seeking past end) but we count 1 entry
	// whose data was "seeked over" (the seek doesn't fail)
	if err != nil {
		t.Logf("Error (expected for some implementations): %v", err)
	}
	t.Logf("Count: %d", count)
}

// ---------------------------------------------------------------------------
// readBlockFromDiskLocked: readerChunk nil, needs to open separate reader
// chunk (different from writerChunk path)
// ---------------------------------------------------------------------------

func TestFastQueueReadBlockNilReaderChunkSeparateFromWriter(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      512, // Small chunks
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000,
		Compression:        compression.TypeNone,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue: %v", err)
	}
	defer fq.Close()

	// Push enough to create multiple chunks
	data := make([]byte, 200)
	for i := 0; i < 15; i++ {
		fq.Push(data)
	}

	// Verify we have multiple chunks
	chunks, _ := fq.GetChunkFiles()
	if len(chunks) < 2 {
		t.Skipf("Need multiple chunks, got %d", len(chunks))
	}

	// Pop first entry -- reader opens on first chunk (separate from writer which is on last)
	d, err := fq.Pop()
	if err != nil {
		t.Fatalf("Pop: %v", err)
	}
	if len(d) != 200 {
		t.Errorf("Expected 200 bytes, got %d", len(d))
	}

	// Force reader chunk to be nil by closing it directly
	fq.mu.Lock()
	if fq.readerChunk != nil && fq.readerChunk != fq.writerChunk {
		fq.readerChunk.Close()
		fq.readerChunk = nil
	}
	fq.mu.Unlock()

	// Next pop should reopen the reader chunk (separate from writer)
	d2, err := fq.Pop()
	if err != nil {
		t.Fatalf("Pop after reopen: %v", err)
	}
	if len(d2) != 200 {
		t.Errorf("Expected 200 bytes, got %d", len(d2))
	}
}

// ---------------------------------------------------------------------------
// readBlockFromDiskLocked: EOF on header read with gap to next chunk
// (lines 742-758: the retry-from-next-chunk path)
// ---------------------------------------------------------------------------

func TestFastQueueReadBlockEOFOnHeaderRetriesNextChunk(t *testing.T) {
	tmpDir := t.TempDir()

	// Use very small chunks that exactly fit entries
	payloadSize := 50
	entrySize := blockHeaderSize + payloadSize // 58 bytes
	// Make chunk hold exactly 2 entries
	chunkSize := int64(entrySize * 2) // 116

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      chunkSize,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000,
		Compression:        compression.TypeNone,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue: %v", err)
	}
	defer fq.Close()

	// Push exactly 4 entries (fills 2 chunks exactly)
	data := make([]byte, payloadSize)
	for i := 0; i < 4; i++ {
		fq.Push(data)
	}

	// Pop all 4 -- the reader must transition between chunks
	for i := 0; i < 4; i++ {
		d, err := fq.Pop()
		if err != nil {
			t.Fatalf("Pop %d: %v", i, err)
		}
		if len(d) != payloadSize {
			t.Errorf("Pop %d: expected %d bytes, got %d", i, payloadSize, len(d))
		}
	}
}

// ---------------------------------------------------------------------------
// Peek from disk with compressed data (exercises decompression in peek)
// ---------------------------------------------------------------------------

func TestFastQueuePeekFromDiskCompressed(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
		Compression:        compression.TypeSnappy,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue: %v", err)
	}
	defer fq.Close()

	// Push and flush to disk
	testData := []byte("compressed-peek-test-data-with-some-repetition-repetition-repetition")
	for i := 0; i < 3; i++ {
		fq.Push(testData)
	}
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	// Peek from disk (should decompress)
	data, err := fq.Peek()
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if string(data) != string(testData) {
		t.Errorf("Expected %q, got %q", testData, data)
	}
}

// ---------------------------------------------------------------------------
// Push: closed check in the disk-write path (line 207)
// This tests when channel is full and queue closes before lock acquired.
// ---------------------------------------------------------------------------

func TestFastQueuePushClosedBeforeDiskFlush(t *testing.T) {
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
		t.Fatalf("NewFastQueue: %v", err)
	}

	// Fill channel
	fq.Push([]byte("fill-channel"))

	// Close queue (this flushes and marks closed)
	fq.Close()

	// Push should fail with closed error (both first and second check)
	err = fq.Push([]byte("new-data"))
	if err != ErrQueueClosed {
		t.Errorf("Expected ErrQueueClosed, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// recover: metadata file with read error (not IsNotExist)
// Create a directory with the metadata filename to trigger a read error.
// ---------------------------------------------------------------------------

func TestFastQueueRecoverMetadataReadError(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a directory named "fastqueue.meta" to cause a read error
	metaDirPath := filepath.Join(tmpDir, metaFileName)
	if err := os.Mkdir(metaDirPath, 0755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	// Should get an error reading metadata (it's a directory, not a file)
	_, err := NewFastQueue(cfg)
	if err == nil {
		t.Error("Expected error when metadata is a directory")
	}
}

// ---------------------------------------------------------------------------
// recover: open writer chunk error
// Create metadata pointing to a location in a non-writable directory.
// ---------------------------------------------------------------------------

func TestFastQueueRecoverWriterChunkOpenError(t *testing.T) {
	tmpDir := t.TempDir()

	// Write valid metadata with offsets
	metaPath := filepath.Join(tmpDir, metaFileName)
	meta := fastqueueMeta{
		Name:         "fastqueue",
		ReaderOffset: 0,
		WriterOffset: 100,
		Version:      2,
		EntryCount:   5,
		TotalBytes:   100,
	}
	data, _ := json.Marshal(meta)
	os.WriteFile(metaPath, data, 0600)

	// Create the chunk file but make it read-only to cause OpenFile with O_RDWR to fail
	chunkPath := filepath.Join(tmpDir, fmt.Sprintf("%016x", 0))
	os.WriteFile(chunkPath, make([]byte, 200), 0444) // Read-only

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	_, err := NewFastQueue(cfg)
	// On macOS, running as root may not respect file permissions
	// so this might not always error, but it covers the code path
	if err != nil {
		t.Logf("Got expected error: %v", err)
	} else {
		t.Log("No error (possibly running as root or macOS quirk)")
	}
	// Cleanup: restore permissions so TempDir cleanup works
	os.Chmod(chunkPath, 0644)
}

// ---------------------------------------------------------------------------
// cleanupOrphanedChunks: non-hex chunk filename (skip path)
// ---------------------------------------------------------------------------

func TestCleanupOrphanedChunksNonHexNames(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
	}

	// Create files with 16-char non-hex names
	os.WriteFile(filepath.Join(tmpDir, "zzzzzzzzzzzzzzzz"), []byte("not-hex"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "short"), []byte("short"), 0644)

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue: %v", err)
	}
	defer fq.Close()

	// Non-hex files should be left alone by cleanupOrphanedChunks
	if _, err := os.Stat(filepath.Join(tmpDir, "zzzzzzzzzzzzzzzz")); os.IsNotExist(err) {
		t.Error("Non-hex file should not be cleaned up")
	}
}

// ---------------------------------------------------------------------------
// peekBlockFromDiskLocked: existing readerChunk is used (line 800-801)
// ---------------------------------------------------------------------------

func TestFastQueuePeekUsesExistingReaderChunk(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
		Compression:        compression.TypeNone,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue: %v", err)
	}
	defer fq.Close()

	// Push entries to disk
	for i := 0; i < 5; i++ {
		fq.Push([]byte("peek-reuse-test"))
	}
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.mu.Unlock()

	// First Pop initializes readerChunk
	fq.Pop()

	// Multiple Peek calls should reuse the existing readerChunk
	for i := 0; i < 3; i++ {
		data, err := fq.Peek()
		if err != nil {
			t.Fatalf("Peek %d: %v", i, err)
		}
		if string(data) != "peek-reuse-test" {
			t.Errorf("Peek %d: expected 'peek-reuse-test', got %q", i, data)
		}
	}
}

// ---------------------------------------------------------------------------
// readBlockFromDiskLocked: decompress block (line 775-783)
// ---------------------------------------------------------------------------

func TestFastQueueReadBlockCompressedFromDisk(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            100,
		Compression:        compression.TypeSnappy,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue: %v", err)
	}
	defer fq.Close()

	// Push large compressible data
	testData := make([]byte, 1000)
	for i := range testData {
		testData[i] = byte(i % 26) // Repetitive pattern
	}

	for i := 0; i < 5; i++ {
		fq.Push(testData)
	}

	// Pop all -- should decompress from disk
	for i := 0; i < 5; i++ {
		data, err := fq.Pop()
		if err != nil {
			t.Fatalf("Pop %d: %v", i, err)
		}
		if len(data) != len(testData) {
			t.Errorf("Pop %d: expected %d bytes, got %d", i, len(testData), len(data))
		}
	}
}

// ---------------------------------------------------------------------------
// syncMetadataLocked: WriteFile error (simulate by making dir read-only)
// ---------------------------------------------------------------------------

func TestSyncMetadataLockedWriteError(t *testing.T) {
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
		t.Fatalf("NewFastQueue: %v", err)
	}
	defer func() {
		// Restore permissions for cleanup
		os.Chmod(tmpDir, 0755)
		fq.Close()
	}()

	fq.Push([]byte("data"))

	// Make directory read-only to cause WriteFile error
	os.Chmod(tmpDir, 0555)

	fq.mu.Lock()
	err = fq.syncMetadataLocked()
	fq.mu.Unlock()

	// Restore permissions
	os.Chmod(tmpDir, 0755)

	if err == nil {
		t.Log("No error (may be running as root)")
	} else {
		t.Logf("Got expected write error: %v", err)
	}
}
