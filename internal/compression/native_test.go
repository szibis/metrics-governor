//go:build cgo && native_compress

package compression

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Native FFI round-trip tests
// ---------------------------------------------------------------------------

// TestNative_RoundTrip verifies that each algorithm compresses and decompresses
// correctly through the Rust FFI path.
func TestNative_RoundTrip(t *testing.T) {
	payload := bytes.Repeat([]byte("native FFI round-trip test payload "), 200)

	algorithms := []struct {
		name string
		cfg  Config
	}{
		{"gzip", Config{Type: TypeGzip, Level: LevelDefault}},
		{"gzip-fastest", Config{Type: TypeGzip, Level: GzipBestSpeed}},
		{"gzip-best", Config{Type: TypeGzip, Level: GzipBestCompression}},
		{"zstd", Config{Type: TypeZstd, Level: LevelDefault}},
		{"zstd-fastest", Config{Type: TypeZstd, Level: ZstdSpeedFastest}},
		{"zstd-best", Config{Type: TypeZstd, Level: ZstdSpeedBestCompression}},
		{"zlib", Config{Type: TypeZlib, Level: LevelDefault}},
		{"deflate", Config{Type: TypeDeflate, Level: LevelDefault}},
	}

	for _, alg := range algorithms {
		t.Run(alg.name, func(t *testing.T) {
			compressed, err := nativeCompress(payload, alg.cfg)
			if err != nil {
				t.Fatalf("nativeCompress: %v", err)
			}
			if len(compressed) == 0 {
				t.Fatal("nativeCompress returned empty output")
			}
			if len(compressed) >= len(payload) {
				t.Logf("warning: compressed size %d >= input %d (low compressibility)", len(compressed), len(payload))
			}

			decompressed, err := nativeDecompress(compressed, alg.cfg.Type)
			if err != nil {
				t.Fatalf("nativeDecompress: %v", err)
			}
			if !bytes.Equal(decompressed, payload) {
				t.Fatalf("round-trip mismatch: got %d bytes, want %d", len(decompressed), len(payload))
			}
		})
	}
}

// TestNative_EmptyInput verifies that empty input is handled correctly.
func TestNative_EmptyInput(t *testing.T) {
	for _, typ := range []Type{TypeGzip, TypeZstd, TypeZlib, TypeDeflate} {
		t.Run(string(typ), func(t *testing.T) {
			compressed, err := nativeCompress(nil, Config{Type: typ, Level: LevelDefault})
			if err != nil {
				t.Fatalf("nativeCompress(nil): %v", err)
			}
			if len(compressed) != 0 {
				t.Fatalf("expected empty output for nil input, got %d bytes", len(compressed))
			}

			decompressed, err := nativeDecompress(nil, typ)
			if err != nil {
				t.Fatalf("nativeDecompress(nil): %v", err)
			}
			if len(decompressed) != 0 {
				t.Fatalf("expected empty output for nil input, got %d bytes", len(decompressed))
			}
		})
	}
}

// TestNative_LargePayload verifies correct handling of 1MB payloads.
func TestNative_LargePayload(t *testing.T) {
	payload := make([]byte, 1024*1024)
	for i := range payload {
		payload[i] = byte(i % 256)
	}

	for _, typ := range []Type{TypeGzip, TypeZstd, TypeZlib, TypeDeflate} {
		t.Run(string(typ), func(t *testing.T) {
			compressed, err := nativeCompress(payload, Config{Type: typ, Level: LevelDefault})
			if err != nil {
				t.Fatalf("nativeCompress: %v", err)
			}
			t.Logf("%s: 1MB -> %d bytes (%.1f%%)", typ, len(compressed), float64(len(compressed))/float64(len(payload))*100)

			decompressed, err := nativeDecompress(compressed, typ)
			if err != nil {
				t.Fatalf("nativeDecompress: %v", err)
			}
			if !bytes.Equal(decompressed, payload) {
				t.Fatalf("round-trip mismatch: got %d bytes, want %d", len(decompressed), len(payload))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Cross-compatibility: native <-> pure Go
// ---------------------------------------------------------------------------

// TestCrossCompat_NativeCompressGoDecompress compresses with the Rust FFI
// and decompresses with the pure Go implementation.
func TestCrossCompat_NativeCompressGoDecompress(t *testing.T) {
	payload := bytes.Repeat([]byte("cross-compat native→Go test data "), 200)

	tests := []struct {
		name       string
		cfg        Config
		goDecomp   func([]byte) ([]byte, error)
	}{
		{"gzip", Config{Type: TypeGzip, Level: LevelDefault}, decompressGzip},
		{"zstd", Config{Type: TypeZstd, Level: LevelDefault}, decompressZstd},
		{"zlib", Config{Type: TypeZlib, Level: LevelDefault}, decompressZlib},
		{"deflate", Config{Type: TypeDeflate, Level: LevelDefault}, decompressDeflate},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compressed, err := nativeCompress(payload, tt.cfg)
			if err != nil {
				t.Fatalf("nativeCompress: %v", err)
			}

			decompressed, err := tt.goDecomp(compressed)
			if err != nil {
				t.Fatalf("Go decompress: %v", err)
			}
			if !bytes.Equal(decompressed, payload) {
				t.Fatalf("native compress → Go decompress mismatch: got %d bytes, want %d", len(decompressed), len(payload))
			}
		})
	}
}

// TestCrossCompat_GoCompressNativeDecompress compresses with pure Go
// and decompresses with the Rust FFI.
func TestCrossCompat_GoCompressNativeDecompress(t *testing.T) {
	payload := bytes.Repeat([]byte("cross-compat Go→native test data "), 200)

	tests := []struct {
		name     string
		cfg      Config
		goComp   func(*bytes.Buffer, []byte, Level) error
	}{
		{"gzip", Config{Type: TypeGzip, Level: LevelDefault}, func(buf *bytes.Buffer, data []byte, level Level) error { return compressGzip(buf, data, level) }},
		{"zstd", Config{Type: TypeZstd, Level: LevelDefault}, func(buf *bytes.Buffer, data []byte, level Level) error { return compressZstd(buf, data, level) }},
		{"zlib", Config{Type: TypeZlib, Level: LevelDefault}, func(buf *bytes.Buffer, data []byte, level Level) error { return compressZlib(buf, data, level) }},
		{"deflate", Config{Type: TypeDeflate, Level: LevelDefault}, func(buf *bytes.Buffer, data []byte, level Level) error { return compressDeflate(buf, data, level) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tt.goComp(&buf, payload, tt.cfg.Level); err != nil {
				t.Fatalf("Go compress: %v", err)
			}

			decompressed, err := nativeDecompress(buf.Bytes(), tt.cfg.Type)
			if err != nil {
				t.Fatalf("nativeDecompress: %v", err)
			}
			if !bytes.Equal(decompressed, payload) {
				t.Fatalf("Go compress → native decompress mismatch: got %d bytes, want %d", len(decompressed), len(payload))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Native FFI concurrent safety
// ---------------------------------------------------------------------------

// TestRace_Native_ConcurrentCompressDecompress verifies that concurrent
// calls to the Rust FFI are thread-safe (no data races, correct results).
func TestRace_Native_ConcurrentCompressDecompress(t *testing.T) {
	const goroutines = 8
	const iterations = 200

	payload := bytes.Repeat([]byte("native concurrent test "), 100)

	for _, typ := range []Type{TypeGzip, TypeZstd, TypeZlib, TypeDeflate} {
		t.Run(string(typ), func(t *testing.T) {
			cfg := Config{Type: typ, Level: LevelDefault}
			var wg sync.WaitGroup
			errCh := make(chan error, goroutines)

			wg.Add(goroutines)
			for i := 0; i < goroutines; i++ {
				go func(id int) {
					defer wg.Done()
					for j := 0; j < iterations; j++ {
						compressed, err := nativeCompress(payload, cfg)
						if err != nil {
							errCh <- fmt.Errorf("goroutine %d iter %d: compress: %w", id, j, err)
							return
						}
						decompressed, err := nativeDecompress(compressed, typ)
						if err != nil {
							errCh <- fmt.Errorf("goroutine %d iter %d: decompress: %w", id, j, err)
							return
						}
						if !bytes.Equal(decompressed, payload) {
							errCh <- fmt.Errorf("goroutine %d iter %d: mismatch", id, j)
							return
						}
					}
				}(i)
			}

			wg.Wait()
			close(errCh)
			for err := range errCh {
				t.Error(err)
			}
		})
	}
}

// TestRace_Native_MixedAlgorithms runs all FFI algorithms concurrently.
func TestRace_Native_MixedAlgorithms(t *testing.T) {
	const iterations = 200
	payload := bytes.Repeat([]byte("mixed algo test "), 150)

	types := []Type{TypeGzip, TypeZstd, TypeZlib, TypeDeflate}
	var wg sync.WaitGroup
	errCh := make(chan error, len(types)*4)

	wg.Add(len(types) * 4)
	for _, typ := range types {
		for i := 0; i < 4; i++ {
			go func(typ Type, id int) {
				defer wg.Done()
				cfg := Config{Type: typ, Level: LevelDefault}
				for j := 0; j < iterations; j++ {
					compressed, err := nativeCompress(payload, cfg)
					if err != nil {
						errCh <- fmt.Errorf("%s goroutine %d: compress: %w", typ, id, err)
						return
					}
					decompressed, err := nativeDecompress(compressed, typ)
					if err != nil {
						errCh <- fmt.Errorf("%s goroutine %d: decompress: %w", typ, id, err)
						return
					}
					if !bytes.Equal(decompressed, payload) {
						errCh <- fmt.Errorf("%s goroutine %d: mismatch", typ, id)
						return
					}
				}
			}(typ, i)
		}
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

// ---------------------------------------------------------------------------
// Verify nativeCompressionAvailable is true
// ---------------------------------------------------------------------------

// TestNative_Available confirms the native_compress build tag is active.
func TestNative_Available(t *testing.T) {
	if !nativeCompressionAvailable {
		t.Fatal("expected nativeCompressionAvailable=true with native_compress build tag")
	}
}

// ---------------------------------------------------------------------------
// Native FFI memory stability
// ---------------------------------------------------------------------------

// TestNative_MemoryStability performs sustained native compress/decompress
// cycles and verifies heap usage remains bounded.
func TestNative_MemoryStability(t *testing.T) {
	const iterations = 5000
	payload := bytes.Repeat([]byte("memory stability test "), 500)

	for _, typ := range []Type{TypeGzip, TypeZstd} {
		t.Run(string(typ), func(t *testing.T) {
			cfg := Config{Type: typ, Level: LevelDefault}

			// Warm up
			for i := 0; i < 50; i++ {
				compressed, _ := nativeCompress(payload, cfg)
				_, _ = nativeDecompress(compressed, typ)
			}

			baseline := heapInUseMB(t)

			for i := 0; i < iterations; i++ {
				compressed, err := nativeCompress(payload, cfg)
				if err != nil {
					t.Fatalf("iter %d: compress: %v", i, err)
				}
				decompressed, err := nativeDecompress(compressed, typ)
				if err != nil {
					t.Fatalf("iter %d: decompress: %v", i, err)
				}
				if !bytes.Equal(decompressed, payload) {
					t.Fatalf("iter %d: mismatch", i)
				}
			}

			final := heapInUseMB(t)
			growth := final - baseline
			t.Logf("%s: baseline=%.2f MB, final=%.2f MB, growth=%.2f MB", typ, baseline, final, growth)

			if growth > 50.0 {
				t.Errorf("%s: possible memory leak: heap grew by %.2f MB", typ, growth)
			}
		})
	}
}
