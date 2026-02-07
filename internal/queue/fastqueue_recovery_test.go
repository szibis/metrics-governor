package queue

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeMeta is a test helper that writes fastqueue metadata to disk,
// simulating a queue that was interrupted mid-operation. This is the
// exact state a queue enters after a crash and restart.
func writeMeta(t *testing.T, dir string, reader, writer int64) {
	t.Helper()
	meta := fastqueueMeta{
		Name:         "test",
		ReaderOffset: reader,
		WriterOffset: writer,
		Version:      2,
		EntryCount:   1,
		TotalBytes:   100,
	}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, metaFileName), data, 0644); err != nil {
		t.Fatal(err)
	}
}

// writeMetaEx writes metadata with explicit entry count and total bytes.
func writeMetaEx(t *testing.T, dir string, reader, writer, entries, totalBytes int64) {
	t.Helper()
	meta := fastqueueMeta{
		Name:         "test",
		ReaderOffset: reader,
		WriterOffset: writer,
		Version:      2,
		EntryCount:   entries,
		TotalBytes:   totalBytes,
	}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, metaFileName), data, 0644); err != nil {
		t.Fatal(err)
	}
}

// writeChunkFile creates a dummy chunk file so that the recovery open succeeds.
// Uses the same naming convention as FastQueue.chunkPath: hex-formatted chunk start.
func writeChunkFile(t *testing.T, dir string, offset, chunkFileSize int64) {
	t.Helper()
	chunkStart := (offset / chunkFileSize) * chunkFileSize
	name := filepath.Join(dir, fmt.Sprintf("%016x", chunkStart))
	if err := os.WriteFile(name, make([]byte, offset%chunkFileSize+1), 0644); err != nil {
		t.Fatal(err)
	}
}

// testRecoveryCfg returns a FastQueueConfig suitable for recovery tests.
func testRecoveryCfg(dir string) FastQueueConfig {
	return FastQueueConfig{
		Path:               dir,
		MaxInmemoryBlocks:  4, // small so channel overflows easily
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   100 * time.Millisecond,
		StaleFlushInterval: 100 * time.Millisecond,
		MaxSize:            1000,
		MaxBytes:           10 * 1024 * 1024,
		WriteBufferSize:    4096,
	}
}

// TestFastQueue_RecoveryThenPush_NoNilPointer reproduces the exact production
// crash: recovery sets writerChunk but not writerBuf → Push overflows the
// channel → writeBlockToDiskLocked dereferences nil writerBuf.
func TestFastQueue_RecoveryThenPush_NoNilPointer(t *testing.T) {
	dir := t.TempDir()
	cfg := testRecoveryCfg(dir)

	// Simulate prior data at offset 100 within a chunk
	writeMeta(t, dir, 0, 100)
	writeChunkFile(t, dir, 100, cfg.ChunkFileSize)

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue after recovery: %v", err)
	}
	defer fq.Close()

	// Fill the in-memory channel to force disk writes
	for i := 0; i < cfg.MaxInmemoryBlocks+2; i++ {
		if err := fq.Push([]byte("data-after-recovery")); err != nil {
			t.Fatalf("Push #%d after recovery failed: %v", i, err)
		}
	}
}

// TestFastQueue_RecoveryThenStaleFlush_NoNilPointer tests the staleFlushLoop
// path: after recovery, stale flush must not crash on nil writerBuf.
func TestFastQueue_RecoveryThenStaleFlush_NoNilPointer(t *testing.T) {
	dir := t.TempDir()
	cfg := testRecoveryCfg(dir)
	cfg.StaleFlushInterval = 50 * time.Millisecond

	writeMeta(t, dir, 0, 100)
	writeChunkFile(t, dir, 100, cfg.ChunkFileSize)

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue after recovery: %v", err)
	}
	defer fq.Close()

	// Push one item into the channel (don't overflow, let stale flush handle it)
	if err := fq.Push([]byte("stale-flush-test")); err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	// Wait for stale flush to trigger — if writerBuf is nil, this panics
	time.Sleep(200 * time.Millisecond)
}

// TestFastQueue_RecoveryWriterBufNotNil verifies that writerBuf is non-nil
// after recovery when writerOffset > 0 (the invariant that was violated).
func TestFastQueue_RecoveryWriterBufNotNil(t *testing.T) {
	dir := t.TempDir()
	cfg := testRecoveryCfg(dir)

	writeMeta(t, dir, 0, 100)
	writeChunkFile(t, dir, 100, cfg.ChunkFileSize)

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue after recovery: %v", err)
	}
	defer fq.Close()

	fq.mu.Lock()
	defer fq.mu.Unlock()

	if fq.writerChunk == nil {
		t.Fatal("expected writerChunk to be non-nil after recovery with writerOffset > 0")
	}
	if fq.writerBuf == nil {
		t.Fatal("expected writerBuf to be non-nil after recovery with writerOffset > 0")
	}
}

// TestFastQueue_RecoveryFreshQueue_NilWriterOK verifies that a fresh queue
// (no metadata on disk) keeps writerChunk/writerBuf nil — lazy init still works.
func TestFastQueue_RecoveryFreshQueue_NilWriterOK(t *testing.T) {
	dir := t.TempDir()
	cfg := testRecoveryCfg(dir)

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue fresh: %v", err)
	}
	defer fq.Close()

	fq.mu.Lock()
	defer fq.mu.Unlock()

	if fq.writerChunk != nil {
		t.Error("expected writerChunk to be nil for fresh queue")
	}
	if fq.writerBuf != nil {
		t.Error("expected writerBuf to be nil for fresh queue")
	}
}

// TestFastQueue_RecoverThenChannelOverflow_FlushesToDisk exercises the full
// production crash scenario: recovery → channel fills → overflow triggers
// flushInmemoryBlocksLocked → writeBlockToDiskLocked. Uses reader==writer
// (no stale data) but writerOffset > 0 to trigger the writerChunk open path.
// Verifies newly pushed data is persisted and retrievable.
func TestFastQueue_RecoverThenChannelOverflow_FlushesToDisk(t *testing.T) {
	dir := t.TempDir()
	cfg := testRecoveryCfg(dir)

	// reader == writer at offset 100: no pending old data, but recovery
	// still opens writerChunk/writerBuf at that offset (the crash scenario).
	writeMetaEx(t, dir, 100, 100, 0, 0)
	writeChunkFile(t, dir, 100, cfg.ChunkFileSize)

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue after recovery: %v", err)
	}
	defer fq.Close()

	// Push enough to overflow the channel and trigger disk writes
	pushed := cfg.MaxInmemoryBlocks + 5
	for i := 0; i < pushed; i++ {
		if err := fq.Push([]byte("overflow-test-data")); err != nil {
			t.Fatalf("Push #%d failed: %v", i, err)
		}
	}

	// Verify entries are retrievable (proves disk write succeeded)
	for i := 0; i < pushed; i++ {
		data, err := fq.Pop()
		if err != nil {
			t.Fatalf("Pop #%d failed: %v", i, err)
		}
		if string(data) != "overflow-test-data" {
			t.Errorf("Pop #%d: got %q, want %q", i, data, "overflow-test-data")
		}
	}
}
