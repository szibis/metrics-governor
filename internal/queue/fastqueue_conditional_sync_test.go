package queue

import (
	"fmt"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/compression"
)

// TestFastQueue_ConditionalSync_SkipsWhenIdle verifies that the meta sync loop
// skips fsync operations when no data has been written or read.
func TestFastQueue_ConditionalSync_SkipsWhenIdle(t *testing.T) {
	dir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               dir,
		MaxInmemoryBlocks:  64,
		ChunkFileSize:      64 * 1024,
		MetaSyncInterval:   50 * time.Millisecond, // Fast interval for test
		StaleFlushInterval: 1 * time.Hour,         // Disable stale flush
		WriteBufferSize:    1024,
		Compression:        compression.TypeSnappy,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue: %v", err)
	}
	defer fq.Close()

	// Wait for several sync intervals without writing anything
	time.Sleep(300 * time.Millisecond)

	// The dirty flag should remain false since nothing was written
	if fq.metaDirty.Load() {
		t.Error("metaDirty should be false when queue is idle")
	}
}

// TestFastQueue_ConditionalSync_DirtyAfterWrite verifies that pushing data
// to disk sets the dirty flag, enabling the next meta sync.
func TestFastQueue_ConditionalSync_DirtyAfterWrite(t *testing.T) {
	dir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               dir,
		MaxInmemoryBlocks:  2, // Small channel to force disk writes
		ChunkFileSize:      64 * 1024,
		MetaSyncInterval:   1 * time.Hour, // Disable auto-sync for this test
		StaleFlushInterval: 1 * time.Hour,
		WriteBufferSize:    1024,
		Compression:        compression.TypeSnappy,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue: %v", err)
	}
	defer fq.Close()

	// Fill in-memory channel
	for i := 0; i < 2; i++ {
		if err := fq.Push([]byte(fmt.Sprintf("inmemory-%d", i))); err != nil {
			t.Fatalf("Push %d: %v", i, err)
		}
	}

	// This push forces a disk write (channel full)
	if err := fq.Push([]byte("disk-write")); err != nil {
		t.Fatalf("Push (disk): %v", err)
	}

	// Dirty flag should be set after disk write
	if !fq.metaDirty.Load() {
		t.Error("metaDirty should be true after disk write")
	}
}

// TestFastQueue_ConditionalSync_ClearedAfterSync verifies that the dirty flag
// is cleared after a successful metadata sync.
func TestFastQueue_ConditionalSync_ClearedAfterSync(t *testing.T) {
	dir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               dir,
		MaxInmemoryBlocks:  2,
		ChunkFileSize:      64 * 1024,
		MetaSyncInterval:   50 * time.Millisecond, // Fast sync for test
		StaleFlushInterval: 50 * time.Millisecond,
		WriteBufferSize:    1024,
		Compression:        compression.TypeSnappy,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue: %v", err)
	}
	defer fq.Close()

	// Force disk writes to set dirty flag
	for i := 0; i < 5; i++ {
		if err := fq.Push([]byte(fmt.Sprintf("data-%d", i))); err != nil {
			t.Fatalf("Push %d: %v", i, err)
		}
	}

	// Wait for meta sync loop to fire and clear the flag
	time.Sleep(200 * time.Millisecond)

	// After sync, dirty flag should be cleared (assuming no new writes)
	if fq.metaDirty.Load() {
		t.Error("metaDirty should be false after sync completes")
	}
}

// TestFastQueue_ConditionalSync_DirtyAfterPop verifies that popping from disk
// also sets the dirty flag (reader offset changes).
func TestFastQueue_ConditionalSync_DirtyAfterPop(t *testing.T) {
	dir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               dir,
		MaxInmemoryBlocks:  2,
		ChunkFileSize:      64 * 1024,
		MetaSyncInterval:   1 * time.Hour, // Disable auto-sync
		StaleFlushInterval: 50 * time.Millisecond,
		WriteBufferSize:    1024,
		Compression:        compression.TypeSnappy,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue: %v", err)
	}
	defer fq.Close()

	// Write enough to force disk
	for i := 0; i < 5; i++ {
		if err := fq.Push([]byte(fmt.Sprintf("data-%d", i))); err != nil {
			t.Fatalf("Push %d: %v", i, err)
		}
	}

	// Wait for stale flush to move data to disk
	time.Sleep(200 * time.Millisecond)

	// Manually clear dirty to test pop behavior
	fq.metaDirty.Store(false)

	// Pop from disk should set dirty (reader offset changes)
	data, err := fq.Pop()
	if err != nil {
		t.Fatalf("Pop: %v", err)
	}
	if data == nil {
		t.Fatal("expected data from Pop")
	}

	// Check if dirty was set by disk read
	// Note: Pop from in-memory channel doesn't set dirty (no offset change)
	// If data came from disk, dirty should be set
	// This test forces disk writes first, so Pop reads from disk
	if fq.readerOffset > 0 && !fq.metaDirty.Load() {
		t.Error("metaDirty should be true after reading from disk")
	}
}

// TestFastQueue_ConditionalSync_NoDataLossOnClose verifies that Close()
// always syncs metadata regardless of dirty flag state.
func TestFastQueue_ConditionalSync_NoDataLossOnClose(t *testing.T) {
	dir := t.TempDir()

	cfg := FastQueueConfig{
		Path:               dir,
		MaxInmemoryBlocks:  2,
		ChunkFileSize:      64 * 1024,
		MetaSyncInterval:   1 * time.Hour, // Disable auto-sync
		StaleFlushInterval: 50 * time.Millisecond,
		WriteBufferSize:    1024,
		Compression:        compression.TypeSnappy,
	}

	const count = 10
	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue: %v", err)
	}

	for i := 0; i < count; i++ {
		if err := fq.Push([]byte(fmt.Sprintf("block-%04d", i))); err != nil {
			t.Fatalf("Push %d: %v", i, err)
		}
	}

	// Wait for stale flush
	time.Sleep(200 * time.Millisecond)

	// Close should sync metadata unconditionally
	if err := fq.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen and verify all data is recoverable
	fq2, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("NewFastQueue (reopen): %v", err)
	}
	defer fq2.Close()

	recovered := 0
	for {
		data, err := fq2.Pop()
		if err != nil {
			t.Fatalf("Pop: %v", err)
		}
		if data == nil {
			break
		}
		recovered++
	}

	if recovered != count {
		t.Errorf("recovered %d blocks, want %d", recovered, count)
	}
}

// TestFastQueue_MetaSyncInterval_Configurable verifies that different profiles
// can use different meta sync intervals.
func TestFastQueue_MetaSyncInterval_Configurable(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
	}{
		{"1s_safety", 1 * time.Second},
		{"2s_performance", 2 * time.Second},
		{"3s_resilient", 3 * time.Second},
		{"5s_observable", 5 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := FastQueueConfig{
				Path:               dir,
				MaxInmemoryBlocks:  64,
				ChunkFileSize:      64 * 1024,
				MetaSyncInterval:   tt.interval,
				StaleFlushInterval: 1 * time.Hour,
				WriteBufferSize:    1024,
				Compression:        compression.TypeSnappy,
			}

			fq, err := NewFastQueue(cfg)
			if err != nil {
				t.Fatalf("NewFastQueue: %v", err)
			}
			defer fq.Close()

			if fq.cfg.MetaSyncInterval != tt.interval {
				t.Errorf("MetaSyncInterval = %v, want %v", fq.cfg.MetaSyncInterval, tt.interval)
			}
		})
	}
}
