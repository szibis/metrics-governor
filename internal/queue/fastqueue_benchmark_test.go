package queue

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/compression"
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
		Path:               tmpDir,
		MaxInmemoryBlocks:  5,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   1 * time.Hour,
		StaleFlushInterval: 1 * time.Hour,
		MaxSize:            1000,
		MaxBytes:           10 * 1024 * 1024,
	}

	// Setup: create queue with minimal data (fast setup)
	fq, _ := NewFastQueue(cfg)
	for i := 0; i < 20; i++ {
		fq.Push([]byte("bench-data"))
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
		Path:               tmpDir,
		MaxInmemoryBlocks:  5,
		ChunkFileSize:      1024 * 1024,
		MetaSyncInterval:   1 * time.Hour,
		StaleFlushInterval: 1 * time.Hour,
		MaxSize:            1000,
		MaxBytes:           10 * 1024 * 1024,
	}

	// Setup: create queue with minimal data
	fq, _ := NewFastQueue(cfg)
	for i := 0; i < 20; i++ {
		fq.Push([]byte("bench-data"))
	}
	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.syncMetadataLocked()
	fq.mu.Unlock()
	fq.Close()

	// Convert to legacy metadata (remove counts)
	metaPath := tmpDir + "/fastqueue.meta"
	data, _ := os.ReadFile(metaPath)
	var meta fastqueueMeta
	json.Unmarshal(data, &meta)
	legacyMeta := fmt.Sprintf(`{"name":"fastqueue","reader_offset":%d,"writer_offset":%d,"version":1}`,
		meta.ReaderOffset, meta.WriterOffset)
	os.WriteFile(metaPath, []byte(legacyMeta), 0600)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Rewrite legacy meta each iteration since recovery updates it
		os.WriteFile(metaPath, []byte(legacyMeta), 0600)
		b.StartTimer()
		fq2, _ := NewFastQueue(cfg)
		fq2.Close()
	}
}

// BenchmarkFastQueue_Compression benchmarks push+pop with different compression modes.
func BenchmarkFastQueue_Compression(b *testing.B) {
	compressions := []struct {
		name string
		comp compression.Type
	}{
		{"none", compression.TypeNone},
		{"snappy", compression.TypeSnappy},
	}

	for _, c := range compressions {
		b.Run(c.name, func(b *testing.B) {
			tmpDir := b.TempDir()

			cfg := FastQueueConfig{
				Path:               tmpDir,
				MaxInmemoryBlocks:  10, // Force disk spill
				ChunkFileSize:      64 * 1024 * 1024,
				MetaSyncInterval:   time.Hour,
				StaleFlushInterval: time.Hour,
				MaxSize:            1000000,
				Compression:        c.comp,
			}

			fq, err := NewFastQueue(cfg)
			if err != nil {
				b.Fatalf("Failed to create FastQueue: %v", err)
			}
			defer fq.Close()

			// Create realistic payload (repeated patterns compress well)
			data := make([]byte, 1024)
			for i := range data {
				data[i] = byte(i % 64)
			}

			b.ResetTimer()
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))

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

// BenchmarkFastQueue_BufferedWriter benchmarks disk spill with different buffer sizes.
func BenchmarkFastQueue_BufferedWriter(b *testing.B) {
	bufSizes := []struct {
		name string
		size int
	}{
		{"no_buffer", 0},
		{"4KB", 4096},
		{"64KB", 65536},
		{"256KB", 262144},
	}

	for _, bs := range bufSizes {
		b.Run(bs.name, func(b *testing.B) {
			tmpDir := b.TempDir()

			cfg := FastQueueConfig{
				Path:               tmpDir,
				MaxInmemoryBlocks:  10,
				ChunkFileSize:      64 * 1024 * 1024,
				MetaSyncInterval:   time.Hour,
				StaleFlushInterval: time.Hour,
				MaxSize:            1000000,
				WriteBufferSize:    bs.size,
				Compression:        compression.TypeNone,
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

			// Pop all to clean up
			for i := 0; i < b.N; i++ {
				if _, err := fq.Pop(); err != nil {
					break
				}
			}
		})
	}
}

// BenchmarkFastQueue_CoalescedFlush benchmarks flushing many blocks at once.
func BenchmarkFastQueue_CoalescedFlush(b *testing.B) {
	batchSizes := []int{64, 256, 1024}

	for _, batchSize := range batchSizes {
		b.Run(fmt.Sprintf("batch_%d", batchSize), func(b *testing.B) {
			tmpDir := b.TempDir()

			cfg := FastQueueConfig{
				Path:               tmpDir,
				MaxInmemoryBlocks:  batchSize + 100, // Room for all blocks
				ChunkFileSize:      256 * 1024 * 1024,
				MetaSyncInterval:   time.Hour,
				StaleFlushInterval: time.Hour,
				MaxSize:            10000000,
				MaxBytes:           1024 * 1024 * 1024,
				Compression:        compression.TypeNone,
			}

			fq, err := NewFastQueue(cfg)
			if err != nil {
				b.Fatalf("Failed to create FastQueue: %v", err)
			}
			defer fq.Close()

			data := make([]byte, 64) // Small payload

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				// Fill channel
				for j := 0; j < batchSize; j++ {
					if err := fq.Push(data); err != nil {
						b.Fatalf("Push failed at iter %d block %d: %v", i, j, err)
					}
				}
				// Coalesced flush
				fq.mu.Lock()
				if err := fq.flushInmemoryBlocksLocked(); err != nil {
					fq.mu.Unlock()
					b.Fatalf("Flush failed: %v", err)
				}
				fq.mu.Unlock()

				// Drain all entries
				b.StopTimer()
				for {
					d, err := fq.Pop()
					if err != nil || d == nil {
						break
					}
				}
				b.StartTimer()
			}
		})
	}
}

// BenchmarkFastQueue_StorageEfficiency measures compressed vs uncompressed bytes.
func BenchmarkFastQueue_StorageEfficiency(b *testing.B) {
	compressions := []struct {
		name string
		comp compression.Type
	}{
		{"none", compression.TypeNone},
		{"snappy", compression.TypeSnappy},
	}

	for _, c := range compressions {
		b.Run(c.name, func(b *testing.B) {
			tmpDir := b.TempDir()

			cfg := FastQueueConfig{
				Path:               tmpDir,
				MaxInmemoryBlocks:  2,
				ChunkFileSize:      64 * 1024 * 1024,
				MetaSyncInterval:   time.Hour,
				StaleFlushInterval: time.Hour,
				MaxSize:            1000000,
				Compression:        c.comp,
			}

			fq, err := NewFastQueue(cfg)
			if err != nil {
				b.Fatalf("Failed to create FastQueue: %v", err)
			}

			// Create realistic protobuf-like payload (repeated field tags, varint lengths)
			data := make([]byte, 1024)
			for i := range data {
				// Simulate protobuf-like patterns
				switch i % 8 {
				case 0:
					data[i] = 0x0a // field tag
				case 1:
					data[i] = byte(i % 128) // varint length
				default:
					data[i] = byte(i%96 + 32) // printable ASCII-like
				}
			}

			const pushCount = 1000
			for i := 0; i < pushCount; i++ {
				if err := fq.Push(data); err != nil {
					b.Fatalf("Push failed: %v", err)
				}
			}

			fq.mu.Lock()
			fq.flushInmemoryBlocksLocked()
			if fq.writerBuf != nil {
				fq.writerBuf.Flush()
			}
			fq.mu.Unlock()

			// Report storage used
			b.ReportMetric(float64(fq.diskBytes.Load()), "disk_bytes")
			b.ReportMetric(float64(pushCount*len(data)), "raw_bytes")
			if fq.diskBytes.Load() > 0 {
				ratio := float64(fq.diskBytes.Load()) / float64(pushCount*len(data))
				b.ReportMetric(ratio, "compression_ratio")
			}

			fq.Close()
		})
	}
}

// BenchmarkFastQueue_BatchSizes benchmarks push with different payload sizes.
func BenchmarkFastQueue_BatchSizes(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"1KB", 1024},
		{"5KB", 5 * 1024},
		{"10KB", 10 * 1024},
		{"50KB", 50 * 1024},
	}

	for _, s := range sizes {
		b.Run(s.name, func(b *testing.B) {
			tmpDir := b.TempDir()

			cfg := FastQueueConfig{
				Path:               tmpDir,
				MaxInmemoryBlocks:  10,
				ChunkFileSize:      64 * 1024 * 1024,
				MetaSyncInterval:   time.Hour,
				StaleFlushInterval: time.Hour,
				MaxSize:            1000000,
				Compression:        compression.TypeSnappy,
			}

			fq, err := NewFastQueue(cfg)
			if err != nil {
				b.Fatalf("Failed to create FastQueue: %v", err)
			}
			defer fq.Close()

			data := make([]byte, s.size)
			for i := range data {
				data[i] = byte(i % 256)
			}

			b.ResetTimer()
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))

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
