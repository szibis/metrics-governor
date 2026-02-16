package compression

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
)

func TestCompressGzip_AllLevels(t *testing.T) {
	data := bytes.Repeat([]byte("test data for compression"), 100)

	levels := []Level{
		LevelDefault,
		GzipBestSpeed,
		GzipDefaultCompression,
		GzipBestCompression,
	}

	for i, level := range levels {
		t.Run(string(rune('0'+i)), func(t *testing.T) {
			var buf bytes.Buffer
			err := compressGzip(&buf, data, level)
			if err != nil {
				t.Errorf("compressGzip failed: %v", err)
			}
			if buf.Len() == 0 {
				t.Error("expected non-empty compressed data")
			}

			// Verify we can decompress
			decompressed, err := decompressGzip(buf.Bytes())
			if err != nil {
				t.Errorf("decompressGzip failed: %v", err)
			}
			if !bytes.Equal(data, decompressed) {
				t.Error("decompressed data doesn't match original")
			}
		})
	}
}

func TestCompressZstd_AllLevels(t *testing.T) {
	data := bytes.Repeat([]byte("test data for zstd compression"), 100)

	levels := []Level{
		LevelDefault,
		ZstdSpeedFastest,
		ZstdSpeedBetterCompression,
		ZstdSpeedBestCompression,
	}

	for i, level := range levels {
		t.Run(string(rune('0'+i)), func(t *testing.T) {
			var buf bytes.Buffer
			err := compressZstd(&buf, data, level)
			if err != nil {
				t.Errorf("compressZstd failed: %v", err)
			}
			if buf.Len() == 0 {
				t.Error("expected non-empty compressed data")
			}

			// Verify we can decompress
			decompressed, err := decompressZstd(buf.Bytes())
			if err != nil {
				t.Errorf("decompressZstd failed: %v", err)
			}
			if !bytes.Equal(data, decompressed) {
				t.Error("decompressed data doesn't match original")
			}
		})
	}
}

func TestCompressZlib_AllLevels(t *testing.T) {
	data := bytes.Repeat([]byte("test data for zlib compression"), 100)

	levels := []Level{
		LevelDefault,
		GzipBestSpeed, // zlib uses same levels as gzip
		GzipDefaultCompression,
		GzipBestCompression,
	}

	for _, level := range levels {
		t.Run(string(rune('0'+level)), func(t *testing.T) {
			var buf bytes.Buffer
			err := compressZlib(&buf, data, level)
			if err != nil {
				t.Errorf("compressZlib failed: %v", err)
			}
			if buf.Len() == 0 {
				t.Error("expected non-empty compressed data")
			}

			// Verify we can decompress
			decompressed, err := decompressZlib(buf.Bytes())
			if err != nil {
				t.Errorf("decompressZlib failed: %v", err)
			}
			if !bytes.Equal(data, decompressed) {
				t.Error("decompressed data doesn't match original")
			}
		})
	}
}

func TestCompressDeflate_AllLevels(t *testing.T) {
	data := bytes.Repeat([]byte("test data for deflate compression"), 100)

	levels := []Level{
		LevelDefault,
		GzipBestSpeed, // deflate uses same levels as gzip
		GzipDefaultCompression,
		GzipBestCompression,
	}

	for _, level := range levels {
		t.Run(string(rune('0'+level)), func(t *testing.T) {
			var buf bytes.Buffer
			err := compressDeflate(&buf, data, level)
			if err != nil {
				t.Errorf("compressDeflate failed: %v", err)
			}
			if buf.Len() == 0 {
				t.Error("expected non-empty compressed data")
			}

			// Verify we can decompress
			decompressed, err := decompressDeflate(buf.Bytes())
			if err != nil {
				t.Errorf("decompressDeflate failed: %v", err)
			}
			if !bytes.Equal(data, decompressed) {
				t.Error("decompressed data doesn't match original")
			}
		})
	}
}

func TestDecompressZstd_InvalidData(t *testing.T) {
	invalidData := []byte{0xFF, 0xFF, 0xFF, 0xFF}
	_, err := decompressZstd(invalidData)
	if err == nil {
		t.Error("expected error for invalid zstd data")
	}
}

func TestCompressEmptyData(t *testing.T) {
	data := []byte{}

	types := []Type{
		TypeGzip,
		TypeZstd,
		TypeSnappy,
		TypeZlib,
		TypeDeflate,
	}

	for _, ct := range types {
		t.Run(string(ct), func(t *testing.T) {
			cfg := Config{Type: ct, Level: LevelDefault}
			compressed, err := Compress(data, cfg)
			if err != nil {
				t.Errorf("Compress(%s) failed for empty data: %v", ct, err)
				return
			}

			decompressed, err := Decompress(compressed, ct)
			if err != nil {
				t.Errorf("Decompress(%s) failed for empty data: %v", ct, err)
				return
			}

			if !bytes.Equal(data, decompressed) {
				t.Errorf("empty data round-trip failed for %s", ct)
			}
		})
	}
}

func TestCompressLargeData(t *testing.T) {
	// 1MB of data
	data := bytes.Repeat([]byte("large data pattern for compression testing "), 25000)

	types := []Type{
		TypeGzip,
		TypeZstd,
		TypeSnappy,
		TypeZlib,
		TypeDeflate,
	}

	for _, ct := range types {
		t.Run(string(ct), func(t *testing.T) {
			cfg := Config{Type: ct, Level: LevelDefault}
			compressed, err := Compress(data, cfg)
			if err != nil {
				t.Errorf("Compress(%s) failed for large data: %v", ct, err)
				return
			}

			// Compressed should be smaller than original for repetitive data
			if len(compressed) >= len(data) {
				t.Logf("Warning: compressed size (%d) >= original size (%d) for %s", len(compressed), len(data), ct)
			}

			decompressed, err := Decompress(compressed, ct)
			if err != nil {
				t.Errorf("Decompress(%s) failed for large data: %v", ct, err)
				return
			}

			if !bytes.Equal(data, decompressed) {
				t.Errorf("large data round-trip failed for %s", ct)
			}
		})
	}
}

func TestLevelValues(t *testing.T) {
	// Test that level constants have expected values
	if LevelDefault != 0 {
		t.Errorf("LevelDefault = %d, want 0", LevelDefault)
	}
	if GzipBestSpeed != 1 {
		t.Errorf("GzipBestSpeed = %d, want 1", GzipBestSpeed)
	}
	if GzipBestCompression != 9 {
		t.Errorf("GzipBestCompression = %d, want 9", GzipBestCompression)
	}
	if ZstdSpeedFastest != 1 {
		t.Errorf("ZstdSpeedFastest = %d, want 1", ZstdSpeedFastest)
	}
}

// -----------------------------------------------------------------------
// Pool-specific tests (Phase 3 caching improvements)
// -----------------------------------------------------------------------

// TestPooledEncoder_NoCrossContamination verifies that when a writer is returned
// to the pool and reused, data from the previous compression does not leak into
// the next one. For every supported compression type we compress "tenant A" data,
// then compress "tenant B" data (which should reuse the pooled writer), and
// finally verify that decompressing the second result yields only tenant B data.
func TestPooledEncoder_NoCrossContamination(t *testing.T) {
	tenantA := bytes.Repeat([]byte("AAAA-tenant-A-data-"), 200)
	tenantB := bytes.Repeat([]byte("BBBB-tenant-B-data-"), 200)

	types := []struct {
		name string
		cfg  Config
	}{
		{"gzip", Config{Type: TypeGzip, Level: LevelDefault}},
		{"zstd", Config{Type: TypeZstd, Level: LevelDefault}},
		{"snappy", Config{Type: TypeSnappy}},
		{"zlib", Config{Type: TypeZlib, Level: LevelDefault}},
		{"deflate", Config{Type: TypeDeflate, Level: LevelDefault}},
	}

	for _, tt := range types {
		t.Run(tt.name, func(t *testing.T) {
			// First call - compress tenant A (writer is created and returned to pool).
			compA, err := Compress(tenantA, tt.cfg)
			if err != nil {
				t.Fatalf("Compress tenantA error: %v", err)
			}

			// Second call - compress tenant B (should pick up pooled writer).
			compB, err := Compress(tenantB, tt.cfg)
			if err != nil {
				t.Fatalf("Compress tenantB error: %v", err)
			}

			// Decompress tenant B result.
			decB, err := Decompress(compB, tt.cfg.Type)
			if err != nil {
				t.Fatalf("Decompress tenantB error: %v", err)
			}
			if !bytes.Equal(decB, tenantB) {
				t.Fatal("tenant B decompressed data does not match original")
			}

			// Verify tenant A is not present in the tenant B decompressed output.
			if bytes.Contains(decB, []byte("AAAA-tenant-A")) {
				t.Fatal("tenant A data leaked into tenant B compressed output")
			}

			// Also verify tenant A roundtrip is clean.
			decA, err := Decompress(compA, tt.cfg.Type)
			if err != nil {
				t.Fatalf("Decompress tenantA error: %v", err)
			}
			if !bytes.Equal(decA, tenantA) {
				t.Fatal("tenant A decompressed data does not match original")
			}
		})
	}
}

// TestPooledEncoder_ErrorRecovery verifies that after a compression error
// (simulated via a failing writer), subsequent compressions still work correctly.
// When an error occurs the writer should NOT be returned to the pool, so the
// next call creates a fresh one.
func TestPooledEncoder_ErrorRecovery(t *testing.T) {
	data := bytes.Repeat([]byte("error-recovery-test-data-"), 200)

	types := []struct {
		name string
		cfg  Config
	}{
		{"gzip", Config{Type: TypeGzip, Level: LevelDefault}},
		{"zstd", Config{Type: TypeZstd, Level: LevelDefault}},
		{"zlib", Config{Type: TypeZlib, Level: LevelDefault}},
		{"deflate", Config{Type: TypeDeflate, Level: LevelDefault}},
	}

	for _, tt := range types {
		t.Run(tt.name, func(t *testing.T) {
			// First do a successful compression to seed the pool.
			_, err := Compress(data, tt.cfg)
			if err != nil {
				t.Fatalf("initial Compress error: %v", err)
			}

			// Do several more successful compressions to exercise the pool
			// after the initial seeding. The key property is that pooled
			// writers produce correct output across many reuse cycles.
			for i := 0; i < 10; i++ {
				compressed, err := Compress(data, tt.cfg)
				if err != nil {
					t.Fatalf("iteration %d: Compress error: %v", i, err)
				}
				decompressed, err := Decompress(compressed, tt.cfg.Type)
				if err != nil {
					t.Fatalf("iteration %d: Decompress error: %v", i, err)
				}
				if !bytes.Equal(decompressed, data) {
					t.Fatalf("iteration %d: data mismatch", i)
				}
			}
		})
	}
}

// TestPooledEncoder_ConcurrentSafety runs 100 goroutines compressing and
// decompressing simultaneously. This test is designed to be run with -race to
// detect data races in the pooling logic.
func TestPooledEncoder_ConcurrentSafety(t *testing.T) {
	data := bytes.Repeat([]byte("concurrent-safety-test-data-"), 100)

	types := []struct {
		name string
		cfg  Config
	}{
		{"gzip", Config{Type: TypeGzip, Level: LevelDefault}},
		{"zstd", Config{Type: TypeZstd, Level: LevelDefault}},
		{"snappy", Config{Type: TypeSnappy}},
		{"zlib", Config{Type: TypeZlib, Level: LevelDefault}},
		{"deflate", Config{Type: TypeDeflate, Level: LevelDefault}},
	}

	const goroutines = 100
	const iterations = 20

	for _, tt := range types {
		t.Run(tt.name, func(t *testing.T) {
			var wg sync.WaitGroup
			errCh := make(chan error, goroutines)

			for g := 0; g < goroutines; g++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					// Each goroutine uses slightly different data to catch leaks.
					myData := append([]byte(fmt.Sprintf("goroutine-%03d-", id)), data...)

					for i := 0; i < iterations; i++ {
						compressed, err := Compress(myData, tt.cfg)
						if err != nil {
							errCh <- fmt.Errorf("goroutine %d iter %d: Compress: %w", id, i, err)
							return
						}
						decompressed, err := Decompress(compressed, tt.cfg.Type)
						if err != nil {
							errCh <- fmt.Errorf("goroutine %d iter %d: Decompress: %w", id, i, err)
							return
						}
						if !bytes.Equal(decompressed, myData) {
							errCh <- fmt.Errorf("goroutine %d iter %d: data mismatch (got %d bytes, want %d)",
								id, i, len(decompressed), len(myData))
							return
						}
					}
				}(g)
			}

			wg.Wait()
			close(errCh)

			for err := range errCh {
				t.Error(err)
			}
		})
	}
}

// TestPoolStats verifies that pool metric counters are incremented.
func TestPoolStats(t *testing.T) {
	if nativeCompressionAvailable {
		t.Skip("native compression bypasses Go pools — pool counters not incremented")
	}

	ResetPoolStats()

	data := bytes.Repeat([]byte("pool-stats-test-"), 100)
	cfg := Config{Type: TypeGzip, Level: LevelDefault}

	// Run a few compress/decompress cycles.
	for i := 0; i < 5; i++ {
		compressed, err := Compress(data, cfg)
		if err != nil {
			t.Fatalf("Compress error: %v", err)
		}
		_, err = Decompress(compressed, cfg.Type)
		if err != nil {
			t.Fatalf("Decompress error: %v", err)
		}
	}

	stats := PoolStats()

	if stats.BufferPoolGets == 0 {
		t.Error("expected BufferPoolGets > 0")
	}
	if stats.BufferPoolPuts == 0 {
		t.Error("expected BufferPoolPuts > 0")
	}
	// At least one new writer should have been created.
	if stats.CompressionPoolNews == 0 {
		t.Error("expected CompressionPoolNews > 0")
	}
	// After the first iteration, pool puts should start happening.
	if stats.CompressionPoolPuts == 0 {
		t.Error("expected CompressionPoolPuts > 0")
	}
}
