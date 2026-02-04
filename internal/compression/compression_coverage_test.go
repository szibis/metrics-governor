package compression

import (
	"bytes"
	"io"
	"testing"
)

// ---------------------------------------------------------------------------
// Compress / Decompress with unsupported types
// ---------------------------------------------------------------------------

func TestCompress_UnsupportedType_ReturnsError(t *testing.T) {
	data := []byte("test data")
	cfg := Config{Type: Type("brotli")} // unsupported

	_, err := Compress(data, cfg)
	if err == nil {
		t.Error("expected error for unsupported compression type, got nil")
	}
}

func TestDecompress_UnsupportedType_ReturnsError(t *testing.T) {
	data := []byte("test data")

	_, err := Decompress(data, Type("brotli"))
	if err == nil {
		t.Error("expected error for unsupported decompression type, got nil")
	}
}

func TestCompress_EmptyType_PassThrough(t *testing.T) {
	data := []byte("test data")
	cfg := Config{Type: ""}

	result, err := Compress(data, cfg)
	if err != nil {
		t.Fatalf("Compress with empty type should pass through, got error: %v", err)
	}
	if !bytes.Equal(result, data) {
		t.Error("expected data to pass through unchanged")
	}
}

func TestDecompress_EmptyType_PassThrough(t *testing.T) {
	data := []byte("test data")

	result, err := Decompress(data, "")
	if err != nil {
		t.Fatalf("Decompress with empty type should pass through, got error: %v", err)
	}
	if !bytes.Equal(result, data) {
		t.Error("expected data to pass through unchanged")
	}
}

// ---------------------------------------------------------------------------
// Decompression of corrupted/invalid data triggers error paths
// ---------------------------------------------------------------------------

func TestDecompressGzip_CorruptedData(t *testing.T) {
	// Not valid gzip data
	corrupted := []byte{0x1f, 0x8b, 0x08, 0x00, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	_, err := decompressGzip(corrupted)
	if err == nil {
		t.Error("expected error for corrupted gzip data")
	}
}

func TestDecompressGzip_TotalGarbage(t *testing.T) {
	_, err := decompressGzip([]byte{0x00, 0x01, 0x02, 0x03})
	if err == nil {
		t.Error("expected error for garbage gzip data")
	}
}

func TestDecompressZstd_CorruptedData(t *testing.T) {
	// Corrupted zstd data (not a valid zstd frame)
	corrupted := []byte{0x28, 0xB5, 0x2F, 0xFD, 0xFF, 0xFF, 0xFF}
	_, err := decompressZstd(corrupted)
	if err == nil {
		t.Error("expected error for corrupted zstd data")
	}
}

func TestDecompressZstd_CompleteGarbage(t *testing.T) {
	_, err := decompressZstd([]byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05})
	if err == nil {
		t.Error("expected error for garbage zstd data")
	}
}

func TestDecompressZstd_PooledDecoderReset(t *testing.T) {
	// First, do a valid compress/decompress cycle to populate the decoder pool.
	validData := bytes.Repeat([]byte("valid zstd data for pool seeding"), 100)
	compressed, err := Compress(validData, Config{Type: TypeZstd, Level: LevelDefault})
	if err != nil {
		t.Fatalf("Compress error: %v", err)
	}

	// Decompress valid data to seed the pool with a decoder.
	result, err := Decompress(compressed, TypeZstd)
	if err != nil {
		t.Fatalf("Decompress valid data error: %v", err)
	}
	if !bytes.Equal(result, validData) {
		t.Fatal("valid data round-trip mismatch")
	}

	// Now attempt to decompress corrupted data.
	// The pooled decoder will try Reset, which may fail on bad data,
	// exercising the error recovery path.
	corrupted := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07}
	_, err = decompressZstd(corrupted)
	if err == nil {
		t.Error("expected error for corrupted zstd data after pool use")
	}

	// Verify the pool still works after error recovery.
	result2, err := Decompress(compressed, TypeZstd)
	if err != nil {
		t.Fatalf("Decompress after error recovery failed: %v", err)
	}
	if !bytes.Equal(result2, validData) {
		t.Fatal("data mismatch after error recovery")
	}
}

func TestDecompressSnappy_CorruptedData(t *testing.T) {
	// Not valid snappy data
	_, err := decompressSnappy([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	if err == nil {
		t.Error("expected error for corrupted snappy data")
	}
}

func TestDecompressZlib_CorruptedData(t *testing.T) {
	// Not valid zlib data
	_, err := decompressZlib([]byte{0x78, 0x9C, 0xFF, 0xFF, 0xFF, 0xFF})
	if err == nil {
		t.Error("expected error for corrupted zlib data")
	}
}

func TestDecompressZlib_TotalGarbage(t *testing.T) {
	_, err := decompressZlib([]byte{0x00, 0x01, 0x02, 0x03})
	if err == nil {
		t.Error("expected error for garbage zlib data")
	}
}

func TestDecompressDeflate_CorruptedData(t *testing.T) {
	// Not valid deflate data
	_, err := decompressDeflate([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})
	if err == nil {
		t.Error("expected error for corrupted deflate data")
	}
}

// ---------------------------------------------------------------------------
// failWriter forces write errors to test writer.Write() and writer.Close()
// error paths in compression functions.
// ---------------------------------------------------------------------------

type failWriter struct {
	failOnWrite int  // fail after this many bytes written (0 = immediately)
	failOnClose bool // fail on Close()
	written     int
}

func (fw *failWriter) Write(p []byte) (int, error) {
	if fw.written+len(p) > fw.failOnWrite {
		// Write some bytes then fail
		n := fw.failOnWrite - fw.written
		if n < 0 {
			n = 0
		}
		fw.written += n
		return n, io.ErrShortWrite
	}
	fw.written += len(p)
	return len(p), nil
}

func TestCompressGzip_WriteError(t *testing.T) {
	data := bytes.Repeat([]byte("data to compress"), 100)
	fw := &failWriter{failOnWrite: 0}
	err := compressGzip(fw, data, LevelDefault)
	if err == nil {
		t.Error("expected error when writer fails")
	}
}

func TestCompressZstd_WriteError(t *testing.T) {
	data := bytes.Repeat([]byte("data to compress"), 100)
	fw := &failWriter{failOnWrite: 0}
	err := compressZstd(fw, data, LevelDefault)
	if err == nil {
		t.Error("expected error when writer fails")
	}
}

func TestCompressZlib_WriteError(t *testing.T) {
	data := bytes.Repeat([]byte("data to compress"), 100)
	fw := &failWriter{failOnWrite: 0}
	err := compressZlib(fw, data, LevelDefault)
	if err == nil {
		t.Error("expected error when writer fails")
	}
}

func TestCompressDeflate_WriteError(t *testing.T) {
	data := bytes.Repeat([]byte("data to compress"), 100)
	fw := &failWriter{failOnWrite: 0}
	err := compressDeflate(fw, data, LevelDefault)
	if err == nil {
		t.Error("expected error when writer fails")
	}
}

// ---------------------------------------------------------------------------
// Pool discard metrics after errors
// ---------------------------------------------------------------------------

func TestPoolDiscardMetrics_AfterWriteError(t *testing.T) {
	ResetPoolStats()

	data := bytes.Repeat([]byte("pool discard test data"), 100)
	fw := &failWriter{failOnWrite: 0}

	// Force write errors that should trigger pool discards.
	_ = compressGzip(fw, data, LevelDefault)
	_ = compressZstd(&failWriter{failOnWrite: 0}, data, LevelDefault)
	_ = compressZlib(&failWriter{failOnWrite: 0}, data, LevelDefault)
	_ = compressDeflate(&failWriter{failOnWrite: 0}, data, LevelDefault)

	stats := PoolStats()
	if stats.CompressionPoolDiscards == 0 {
		t.Error("expected pool discards > 0 after write errors")
	}
}

// ---------------------------------------------------------------------------
// Snappy round-trip (for completeness)
// ---------------------------------------------------------------------------

func TestSnappy_RoundTrip(t *testing.T) {
	data := bytes.Repeat([]byte("snappy round-trip test"), 100)

	compressed := compressSnappy(data)
	decompressed, err := decompressSnappy(compressed)
	if err != nil {
		t.Fatalf("decompressSnappy error: %v", err)
	}
	if !bytes.Equal(decompressed, data) {
		t.Error("snappy round-trip failed")
	}
}

// ---------------------------------------------------------------------------
// Compress snappy via public API (fast path, no buffer pool)
// ---------------------------------------------------------------------------

func TestCompress_SnappyUsesDirectPath(t *testing.T) {
	ResetPoolStats()

	data := []byte("snappy direct path test")
	cfg := Config{Type: TypeSnappy}

	compressed, err := Compress(data, cfg)
	if err != nil {
		t.Fatalf("Compress snappy error: %v", err)
	}

	// Snappy should NOT use buffer pool.
	stats := PoolStats()
	if stats.BufferPoolGets > 0 {
		t.Error("expected no buffer pool gets for snappy compression")
	}

	decompressed, err := Decompress(compressed, TypeSnappy)
	if err != nil {
		t.Fatalf("Decompress snappy error: %v", err)
	}
	if !bytes.Equal(decompressed, data) {
		t.Error("snappy round-trip mismatch")
	}
}

// ---------------------------------------------------------------------------
// ResetPoolStats
// ---------------------------------------------------------------------------

func TestResetPoolStats_Zeroes(t *testing.T) {
	// Do some work so counters are non-zero.
	data := bytes.Repeat([]byte("test"), 50)
	cfg := Config{Type: TypeGzip, Level: LevelDefault}
	_, _ = Compress(data, cfg)

	ResetPoolStats()

	stats := PoolStats()
	if stats.CompressionPoolGets != 0 ||
		stats.CompressionPoolPuts != 0 ||
		stats.CompressionPoolDiscards != 0 ||
		stats.CompressionPoolNews != 0 ||
		stats.BufferPoolGets != 0 ||
		stats.BufferPoolPuts != 0 {
		t.Errorf("expected all pool stats to be 0 after reset, got %+v", stats)
	}
}
