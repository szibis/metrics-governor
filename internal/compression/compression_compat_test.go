package compression

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"sync"
	"testing"
)

// TestCompat_RealisticPayload exercises all compression codecs with a payload
// that mimics serialized protobuf metrics data (mix of structured bytes and
// repeated patterns). This catches format-level regressions after library bumps.
func TestCompat_RealisticPayload(t *testing.T) {
	// Build a ~50KB payload that looks like serialized protobuf:
	// field tags, varints, repeated string fields, and some random bytes.
	var buf bytes.Buffer
	for i := 0; i < 500; i++ {
		// Simulate protobuf field tags + varint lengths
		buf.Write([]byte{0x0a, 0x20}) // field 1, length-delimited, 32 bytes
		// Metric name (repeated pattern)
		buf.WriteString(fmt.Sprintf("metric.http.request.duration.%04d", i%100))
		// Simulate label key-value pairs
		buf.Write([]byte{0x12, 0x10})
		buf.WriteString(fmt.Sprintf("host=srv-%03d", i%50))
		// Simulate a double value (8 bytes)
		buf.Write([]byte{0x19, 0x00, 0x00, 0x00, 0x00, 0x00, 0x80, 0x45, 0x40})
		// Some random bytes to prevent trivial compression
		noise := make([]byte, 16)
		rand.Read(noise)
		buf.Write(noise)
	}

	payload := buf.Bytes()
	t.Logf("test payload size: %d bytes", len(payload))

	types := []struct {
		name string
		cfg  Config
	}{
		{"gzip-default", Config{Type: TypeGzip, Level: LevelDefault}},
		{"gzip-best", Config{Type: TypeGzip, Level: GzipBestCompression}},
		{"zstd-default", Config{Type: TypeZstd, Level: LevelDefault}},
		{"zstd-fastest", Config{Type: TypeZstd, Level: ZstdSpeedFastest}},
		{"zstd-best", Config{Type: TypeZstd, Level: ZstdSpeedBestCompression}},
		{"snappy", Config{Type: TypeSnappy}},
		{"zlib-default", Config{Type: TypeZlib, Level: LevelDefault}},
		{"deflate-default", Config{Type: TypeDeflate, Level: LevelDefault}},
		{"lz4-default", Config{Type: TypeLZ4, Level: LevelDefault}},
	}

	for _, tt := range types {
		t.Run(tt.name, func(t *testing.T) {
			compressed, err := Compress(payload, tt.cfg)
			if err != nil {
				t.Fatalf("Compress() error: %v", err)
			}

			if len(compressed) == 0 {
				t.Fatal("compressed output is empty")
			}

			decompressed, err := Decompress(compressed, tt.cfg.Type)
			if err != nil {
				t.Fatalf("Decompress() error: %v", err)
			}

			if !bytes.Equal(decompressed, payload) {
				t.Fatalf("round-trip mismatch: got %d bytes, want %d bytes", len(decompressed), len(payload))
			}

			ratio := float64(len(compressed)) / float64(len(payload)) * 100
			t.Logf("%s: %d -> %d bytes (%.1f%%)", tt.name, len(payload), len(compressed), ratio)
		})
	}
}

// TestCompat_LargeProtobufLikePayload tests with a larger payload (~500KB)
// to exercise buffering and chunking behavior in compression libraries.
func TestCompat_LargeProtobufLikePayload(t *testing.T) {
	// ~500KB of repeated metric-like data
	var buf bytes.Buffer
	metricTemplate := []byte("metric_name{host=\"server-01\",region=\"us-east-1\",env=\"production\",cluster=\"main\"} 42.5 1700000000\n")
	for buf.Len() < 500*1024 {
		buf.Write(metricTemplate)
	}
	payload := buf.Bytes()

	types := []Config{
		{Type: TypeGzip, Level: LevelDefault},
		{Type: TypeZstd, Level: LevelDefault},
		{Type: TypeSnappy},
		{Type: TypeLZ4, Level: LevelDefault},
	}

	for _, cfg := range types {
		t.Run(string(cfg.Type), func(t *testing.T) {
			compressed, err := Compress(payload, cfg)
			if err != nil {
				t.Fatalf("Compress() error: %v", err)
			}

			decompressed, err := Decompress(compressed, cfg.Type)
			if err != nil {
				t.Fatalf("Decompress() error: %v", err)
			}

			if !bytes.Equal(decompressed, payload) {
				t.Fatalf("round-trip failed for %s: got %d bytes, want %d bytes",
					cfg.Type, len(decompressed), len(payload))
			}
		})
	}
}

// TestCompat_ConcurrentLZ4ReaderPool specifically stress-tests the LZ4 reader
// pool with concurrent decompressions to catch pool-related races after the
// lz4/v4 library bump (v4.1.22 → v4.1.25).
func TestCompat_ConcurrentLZ4ReaderPool(t *testing.T) {
	data := bytes.Repeat([]byte("lz4 concurrent reader pool test data "), 200)
	cfg := Config{Type: TypeLZ4, Level: LevelDefault}

	compressed, err := Compress(data, cfg)
	if err != nil {
		t.Fatalf("Compress error: %v", err)
	}

	const goroutines = 50
	const iterations = 30
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				decompressed, err := Decompress(compressed, TypeLZ4)
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d iter %d: Decompress: %w", id, i, err)
					return
				}
				if !bytes.Equal(decompressed, data) {
					errCh <- fmt.Errorf("goroutine %d iter %d: data mismatch (got %d bytes, want %d)",
						id, i, len(decompressed), len(data))
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
}

// TestCompat_ConcurrentSnappyRoundTrip stress-tests snappy with concurrent
// encode/decode to verify thread safety after golang/snappy v0.0.4 → v1.0.0.
func TestCompat_ConcurrentSnappyRoundTrip(t *testing.T) {
	const goroutines = 50
	const iterations = 50
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			data := bytes.Repeat([]byte(fmt.Sprintf("snappy-goroutine-%03d-", id)), 100)

			for i := 0; i < iterations; i++ {
				compressed, err := Compress(data, Config{Type: TypeSnappy})
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d iter %d: Compress: %w", id, i, err)
					return
				}
				decompressed, err := Decompress(compressed, TypeSnappy)
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d iter %d: Decompress: %w", id, i, err)
					return
				}
				if !bytes.Equal(decompressed, data) {
					errCh <- fmt.Errorf("goroutine %d iter %d: data mismatch", id, i)
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
}

// TestCompat_ZstdEncoderDecoderPoolIntegrity verifies that after many
// concurrent encode/decode cycles, the zstd encoder/decoder pools remain
// healthy and produce correct results. This catches issues with the
// klauspost/compress v1.18.0 → v1.18.3 bump.
func TestCompat_ZstdEncoderDecoderPoolIntegrity(t *testing.T) {
	const goroutines = 50
	const iterations = 30
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			data := bytes.Repeat([]byte(fmt.Sprintf("zstd-pool-test-%03d-data-", id)), 150)

			for i := 0; i < iterations; i++ {
				compressed, err := Compress(data, Config{Type: TypeZstd, Level: LevelDefault})
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d iter %d: Compress: %w", id, i, err)
					return
				}
				decompressed, err := Decompress(compressed, TypeZstd)
				if err != nil {
					errCh <- fmt.Errorf("goroutine %d iter %d: Decompress: %w", id, i, err)
					return
				}
				if !bytes.Equal(decompressed, data) {
					errCh <- fmt.Errorf("goroutine %d iter %d: data mismatch (got %d bytes, want %d)",
						id, i, len(decompressed), len(data))
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
}
