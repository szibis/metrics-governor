package queue

import (
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
