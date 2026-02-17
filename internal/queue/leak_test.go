package queue

import (
	"os"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestLeakCheck_SendQueue(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	tmpDir, err := os.MkdirTemp("", "queue-leak-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := DefaultConfig()
	cfg.Path = tmpDir

	q, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Push and pop some data
	for i := 0; i < 5; i++ {
		if err := q.PushData([]byte("test-data")); err != nil {
			t.Fatalf("PushData failed: %v", err)
		}
	}

	for i := 0; i < 5; i++ {
		entry, err := q.Pop()
		if err != nil {
			t.Fatalf("Pop failed: %v", err)
		}
		if entry == nil {
			t.Fatal("Pop returned nil entry")
		}
	}

	if err := q.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestLeakCheck_FastQueue(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	tmpDir, err := os.MkdirTemp("", "fq-leak-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  64,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   100 * time.Millisecond,
		StaleFlushInterval: 100 * time.Millisecond,
		MaxSize:            1000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue() error = %v", err)
	}

	// Push and pop
	for i := 0; i < 5; i++ {
		if err := fq.Push([]byte("test")); err != nil {
			t.Fatalf("Push failed: %v", err)
		}
	}

	for i := 0; i < 5; i++ {
		if _, err := fq.Pop(); err != nil {
			t.Fatalf("Pop failed: %v", err)
		}
	}

	if err := fq.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestLeakCheck_FastQueue_Compression(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	tmpDir, err := os.MkdirTemp("", "fq-compression-leak-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  64,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   100 * time.Millisecond,
		StaleFlushInterval: 100 * time.Millisecond,
		MaxSize:            1000,
		Compression:        "snappy",
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue() error = %v", err)
	}

	for i := 0; i < 10; i++ {
		if err := fq.Push([]byte("compressed-leak-test")); err != nil {
			t.Fatalf("Push failed: %v", err)
		}
	}

	for i := 0; i < 10; i++ {
		if _, err := fq.Pop(); err != nil {
			t.Fatalf("Pop failed: %v", err)
		}
	}

	if err := fq.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestLeakCheck_FastQueue_BufferedWriter(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	tmpDir, err := os.MkdirTemp("", "fq-buffered-leak-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  64,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   100 * time.Millisecond,
		StaleFlushInterval: 100 * time.Millisecond,
		MaxSize:            1000,
		WriteBufferSize:    262144,
		Compression:        "none",
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue() error = %v", err)
	}

	for i := 0; i < 10; i++ {
		if err := fq.Push([]byte("buffered-leak-test")); err != nil {
			t.Fatalf("Push failed: %v", err)
		}
	}

	for i := 0; i < 10; i++ {
		if _, err := fq.Pop(); err != nil {
			t.Fatalf("Pop failed: %v", err)
		}
	}

	if err := fq.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestLeakCheck_MemoryQueue_BalancedConfig(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      5000,
		MaxBytes:     44040192, // 42 MB
		FullBehavior: DropOldest,
	})

	// Push 100 batches
	for i := 0; i < 100; i++ {
		batch := &ExportBatch{
			Timestamp:      time.Now(),
			EstimatedBytes: 1000,
		}
		if err := q.PushBatch(batch); err != nil {
			t.Fatalf("PushBatch failed on batch %d: %v", i, err)
		}
	}

	// Pop all batches
	for i := 0; i < 100; i++ {
		batch, err := q.PopBatch()
		if err != nil {
			t.Fatalf("PopBatch failed on batch %d: %v", i, err)
		}
		if batch == nil {
			t.Fatalf("PopBatch returned nil on batch %d", i)
		}
	}

	q.Close()
}
