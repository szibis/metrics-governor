package compression

import (
	"bytes"
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
		GzipBestSpeed,          // zlib uses same levels as gzip
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
		GzipBestSpeed,          // deflate uses same levels as gzip
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

func TestCompressLZ4_Default(t *testing.T) {
	data := bytes.Repeat([]byte("test data for lz4 compression"), 100)

	var buf bytes.Buffer
	err := compressLZ4(&buf, data, LevelDefault)
	if err != nil {
		t.Errorf("compressLZ4 failed: %v", err)
	}
	if buf.Len() == 0 {
		t.Error("expected non-empty compressed data")
	}

	// Verify we can decompress
	decompressed, err := decompressLZ4(buf.Bytes())
	if err != nil {
		t.Errorf("decompressLZ4 failed: %v", err)
	}
	if !bytes.Equal(data, decompressed) {
		t.Error("decompressed data doesn't match original")
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
		TypeLZ4,
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
		TypeLZ4,
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
