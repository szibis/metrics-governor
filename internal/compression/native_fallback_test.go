//go:build !cgo || !native_compress

package compression

import (
	"bytes"
	"testing"
)

// TestFallback_Available confirms that native compression is NOT available
// when the native_compress build tag is absent.
func TestFallback_Available(t *testing.T) {
	if nativeCompressionAvailable {
		t.Fatal("expected nativeCompressionAvailable=false without native_compress build tag")
	}
}

// TestFallback_AllAlgorithmsUsePureGo verifies that all algorithms work
// correctly via the pure Go path when native compression is disabled.
func TestFallback_AllAlgorithmsUsePureGo(t *testing.T) {
	payload := bytes.Repeat([]byte("fallback pure Go test "), 200)

	algorithms := []struct {
		name string
		cfg  Config
	}{
		{"gzip", Config{Type: TypeGzip, Level: LevelDefault}},
		{"zstd", Config{Type: TypeZstd, Level: LevelDefault}},
		{"snappy", Config{Type: TypeSnappy}},
		{"zlib", Config{Type: TypeZlib, Level: LevelDefault}},
		{"deflate", Config{Type: TypeDeflate, Level: LevelDefault}},
	}

	for _, alg := range algorithms {
		t.Run(alg.name, func(t *testing.T) {
			compressed, err := Compress(payload, alg.cfg)
			if err != nil {
				t.Fatalf("Compress: %v", err)
			}
			decompressed, err := Decompress(compressed, alg.cfg.Type)
			if err != nil {
				t.Fatalf("Decompress: %v", err)
			}
			if !bytes.Equal(decompressed, payload) {
				t.Fatalf("round-trip mismatch: got %d bytes, want %d", len(decompressed), len(payload))
			}
		})
	}
}
