package queue

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"
)

// BenchmarkFastQueue_Push measures push performance.
func BenchmarkFastQueue_Push(b *testing.B) {
	tmpDir := b.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1024,
		ChunkFileSize:      64 * 1024 * 1024, // 64MB
		MetaSyncInterval:   time.Hour,        // Disable during benchmark
		StaleFlushInterval: time.Hour,
		MaxSize:            1000000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		b.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	data := make([]byte, 1024) // 1KB payload
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if err := fq.Push(data); err != nil {
			b.Fatalf("Push failed: %v", err)
		}
	}
}

// BenchmarkFastQueue_PushPop measures push+pop throughput.
func BenchmarkFastQueue_PushPop(b *testing.B) {
	tmpDir := b.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1024,
		ChunkFileSize:      64 * 1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		b.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	data := make([]byte, 1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if err := fq.Push(data); err != nil {
			b.Fatalf("Push failed: %v", err)
		}
		if _, err := fq.Pop(); err != nil {
			b.Fatalf("Pop failed: %v", err)
		}
	}
}

// BenchmarkFastQueue_InMemoryOnly tests pure in-memory performance.
func BenchmarkFastQueue_InMemoryOnly(b *testing.B) {
	tmpDir := b.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  100000, // Large channel to avoid disk
		ChunkFileSize:      64 * 1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		b.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	data := make([]byte, 1024)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if err := fq.Push(data); err != nil {
			b.Fatalf("Push failed: %v", err)
		}
		if _, err := fq.Pop(); err != nil {
			b.Fatalf("Pop failed: %v", err)
		}
	}
}

// BenchmarkFastQueue_DiskSpill tests disk spill performance.
func BenchmarkFastQueue_DiskSpill(b *testing.B) {
	tmpDir := b.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  10, // Force disk spill
		ChunkFileSize:      64 * 1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		b.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	data := make([]byte, 1024)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		if err := fq.Push(data); err != nil {
			b.Fatalf("Push failed: %v", err)
		}
	}

	// Pop all
	for i := 0; i < b.N; i++ {
		if _, err := fq.Pop(); err != nil {
			break
		}
	}
}

// BenchmarkFastQueue_ConcurrentPush tests concurrent push performance.
func BenchmarkFastQueue_ConcurrentPush(b *testing.B) {
	tmpDir := b.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1024,
		ChunkFileSize:      64 * 1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		b.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	data := make([]byte, 1024)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = fq.Push(data)
		}
	})
}

// BenchmarkFastQueue_ConcurrentPushPop tests concurrent push and pop.
func BenchmarkFastQueue_ConcurrentPushPop(b *testing.B) {
	tmpDir := b.TempDir()

	cfg := FastQueueConfig{
		Path:               tmpDir,
		MaxInmemoryBlocks:  1024,
		ChunkFileSize:      64 * 1024 * 1024,
		MetaSyncInterval:   time.Hour,
		StaleFlushInterval: time.Hour,
		MaxSize:            1000000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		b.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	data := make([]byte, 1024)

	// Pre-fill some data
	for i := 0; i < 1000; i++ {
		_ = fq.Push(data)
	}

	b.ResetTimer()
	b.ReportAllocs()

	var wg sync.WaitGroup
	wg.Add(2)

	// Pushers
	go func() {
		defer wg.Done()
		for i := 0; i < b.N; i++ {
			_ = fq.Push(data)
		}
	}()

	// Poppers
	go func() {
		defer wg.Done()
		for i := 0; i < b.N; i++ {
			_, _ = fq.Pop()
		}
	}()

	wg.Wait()
}

// BenchmarkFastQueue_PayloadSizes tests different payload sizes.
func BenchmarkFastQueue_PayloadSizes(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"64B", 64},
		{"256B", 256},
		{"1KB", 1024},
		{"4KB", 4096},
		{"16KB", 16384},
		{"64KB", 65536},
	}

	for _, s := range sizes {
		b.Run(s.name, func(b *testing.B) {
			tmpDir := b.TempDir()

			cfg := FastQueueConfig{
				Path:               tmpDir,
				MaxInmemoryBlocks:  1024,
				ChunkFileSize:      64 * 1024 * 1024,
				MetaSyncInterval:   time.Hour,
				StaleFlushInterval: time.Hour,
				MaxSize:            1000000,
			}

			fq, err := NewFastQueue(cfg)
			if err != nil {
				b.Fatalf("Failed to create FastQueue: %v", err)
			}
			defer fq.Close()

			data := make([]byte, s.size)

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				if err := fq.Push(data); err != nil {
					b.Fatalf("Push failed: %v", err)
				}
				if _, err := fq.Pop(); err != nil {
					b.Fatalf("Pop failed: %v", err)
				}
			}
		})
	}
}

// BenchmarkFastQueue_RecoverV2Metadata benchmarks O(1) recovery with V2 metadata
func BenchmarkFastQueue_RecoverV2Metadata(b *testing.B) {
	tmpDir := b.TempDir()

	cfg := FastQueueConfig{
		Path:              tmpDir,
		MaxInmemoryBlocks: 10,
		ChunkFileSize:     1024 * 1024,
		MetaSyncInterval:  1 * time.Hour,
		MaxSize:           10000,
		MaxBytes:          100 * 1024 * 1024,
	}

	// Setup: create queue with data
	fq, _ := NewFastQueue(cfg)
	for i := 0; i < 100; i++ {
		fq.Push([]byte("benchmark-recovery-test-data"))
	}
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.syncMetadataLocked()
	fq.mu.Unlock()
	fq.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fq2, _ := NewFastQueue(cfg)
		fq2.Close()
	}
}

// BenchmarkFastQueue_RecoverLegacyMetadata benchmarks O(n) recovery with legacy scan
func BenchmarkFastQueue_RecoverLegacyMetadata(b *testing.B) {
	tmpDir := b.TempDir()

	cfg := FastQueueConfig{
		Path:              tmpDir,
		MaxInmemoryBlocks: 10,
		ChunkFileSize:     1024 * 1024,
		MetaSyncInterval:  1 * time.Hour,
		MaxSize:           10000,
		MaxBytes:          100 * 1024 * 1024,
	}

	// Setup: create queue with data
	fq, _ := NewFastQueue(cfg)
	for i := 0; i < 100; i++ {
		fq.Push([]byte("benchmark-recovery-test-data"))
	}
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.syncMetadataLocked()
	fq.mu.Unlock()
	fq.Close()

	// Convert to legacy metadata (remove counts)
	metaPath := tmpDir + "/fastqueue.meta"
	data, _ := os.ReadFile(metaPath)
	// Parse and rewrite without counts
	var meta fastqueueMeta
	json.Unmarshal(data, &meta)
	legacyMeta := fmt.Sprintf(`{"name":"fastqueue","reader_offset":%d,"writer_offset":%d,"version":1}`,
		meta.ReaderOffset, meta.WriterOffset)
	os.WriteFile(metaPath, []byte(legacyMeta), 0600)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Rewrite legacy meta each iteration since recovery updates it
		os.WriteFile(metaPath, []byte(legacyMeta), 0600)
		fq2, _ := NewFastQueue(cfg)
		fq2.Close()
	}
}
