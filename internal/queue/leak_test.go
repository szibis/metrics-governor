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
