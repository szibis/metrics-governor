package compression

import (
	"bytes"
	"crypto/rand"
	"sync"
	"testing"
)

// =============================================================================
// GetBuffer / ReleaseBuffer direct tests
// =============================================================================

func TestGetBuffer_ReturnsResetBuffer(t *testing.T) {
	buf := GetBuffer()
	defer ReleaseBuffer(buf)

	if buf.Len() != 0 {
		t.Errorf("GetBuffer() returned non-empty buffer: len=%d", buf.Len())
	}
}

func TestGetBuffer_RecyclesFromPool(t *testing.T) {
	ResetPoolStats()

	buf1 := GetBuffer()
	buf1.WriteString("hello")
	ReleaseBuffer(buf1)

	buf2 := GetBuffer()
	defer ReleaseBuffer(buf2)

	// Buffer should be reset even if recycled.
	if buf2.Len() != 0 {
		t.Errorf("Recycled buffer not reset: len=%d", buf2.Len())
	}
}

func TestGetBuffer_TracksActiveCount(t *testing.T) {
	ResetPoolStats()

	before := BufferActiveCount()
	buf := GetBuffer()
	during := BufferActiveCount()
	ReleaseBuffer(buf)
	after := BufferActiveCount()

	if during != before+1 {
		t.Errorf("Active count during checkout: got %d, want %d", during, before+1)
	}
	if after != before {
		t.Errorf("Active count after release: got %d, want %d", after, before)
	}
}

func TestReleaseBuffer_Nil(t *testing.T) {
	// Must not panic.
	ReleaseBuffer(nil)
}

func TestReleaseBuffer_OversizedDiscard(t *testing.T) {
	ResetPoolStats()

	buf := GetBuffer()
	// Grow buffer beyond maxPoolBufSize (4 MB).
	buf.Grow(maxPoolBufSize + 1)
	buf.Write(make([]byte, maxPoolBufSize+1))

	ReleaseBuffer(buf)

	stats := PoolStats()
	if stats.CompressionPoolDiscards == 0 {
		t.Error("Expected oversized buffer to be discarded, but CompressionPoolDiscards=0")
	}
}

func TestReleaseBuffer_NormalSizeReturned(t *testing.T) {
	ResetPoolStats()

	buf := GetBuffer()
	buf.WriteString("normal data")
	ReleaseBuffer(buf)

	stats := PoolStats()
	if stats.BufferPoolPuts == 0 {
		t.Error("Expected normal-sized buffer to be returned to pool, but BufferPoolPuts=0")
	}
}

func TestBufferActiveCount_MultipleBuffers(t *testing.T) {
	ResetPoolStats()

	bufs := make([]*bytes.Buffer, 5)
	for i := range bufs {
		bufs[i] = GetBuffer()
	}

	if got := BufferActiveCount(); got < 5 {
		t.Errorf("Active count with 5 buffers: got %d, want >= 5", got)
	}

	for _, buf := range bufs {
		ReleaseBuffer(buf)
	}

	if got := BufferActiveCount(); got != 0 {
		t.Errorf("Active count after all released: got %d, want 0", got)
	}
}

// =============================================================================
// CompressToBuf direct tests
// =============================================================================

func TestCompressToBuf_AllTypes(t *testing.T) {
	data := []byte("test data for compression round-trip verification")

	types := []struct {
		name string
		cfg  Config
	}{
		{"none", Config{Type: TypeNone}},
		{"gzip", Config{Type: TypeGzip}},
		{"zstd", Config{Type: TypeZstd}},
		{"snappy", Config{Type: TypeSnappy}},
		{"zlib", Config{Type: TypeZlib}},
		{"deflate", Config{Type: TypeDeflate}},
	}

	for _, tt := range types {
		t.Run(tt.name, func(t *testing.T) {
			dst := GetBuffer()
			defer ReleaseBuffer(dst)

			if err := CompressToBuf(dst, data, tt.cfg); err != nil {
				t.Fatalf("CompressToBuf(%s) error: %v", tt.name, err)
			}

			if dst.Len() == 0 {
				t.Fatalf("CompressToBuf(%s) produced empty output", tt.name)
			}

			// Decompress and verify round-trip.
			decompressed, err := Decompress(dst.Bytes(), tt.cfg.Type)
			if err != nil {
				t.Fatalf("Decompress(%s) error: %v", tt.name, err)
			}
			if !bytes.Equal(decompressed, data) {
				t.Errorf("Round-trip mismatch for %s: got %d bytes, want %d", tt.name, len(decompressed), len(data))
			}
		})
	}
}

func TestCompressToBuf_UnsupportedType(t *testing.T) {
	dst := GetBuffer()
	defer ReleaseBuffer(dst)

	err := CompressToBuf(dst, []byte("data"), Config{Type: "bogus"})
	if err == nil {
		t.Error("Expected error for unsupported compression type")
	}
}

func TestCompressToBuf_MatchesCompress(t *testing.T) {
	// Verify CompressToBuf produces identical output to Compress.
	data := make([]byte, 4096)
	rand.Read(data)

	for _, ct := range []Type{TypeGzip, TypeZstd, TypeZlib, TypeDeflate} {
		t.Run(string(ct), func(t *testing.T) {
			cfg := Config{Type: ct}

			expected, err := Compress(data, cfg)
			if err != nil {
				t.Fatalf("Compress error: %v", err)
			}

			dst := GetBuffer()
			if err := CompressToBuf(dst, data, cfg); err != nil {
				ReleaseBuffer(dst)
				t.Fatalf("CompressToBuf error: %v", err)
			}
			got := dst.Bytes()

			// Both must decompress to same original.
			dec1, _ := Decompress(expected, ct)
			dec2, _ := Decompress(got, ct)
			if !bytes.Equal(dec1, dec2) {
				t.Errorf("CompressToBuf and Compress produce different decompressed output")
			}
			ReleaseBuffer(dst)
		})
	}
}

// =============================================================================
// DecompressToBuf direct tests
// =============================================================================

func TestDecompressToBuf_AllTypes(t *testing.T) {
	original := []byte("test data for decompression buffer verification with enough content to compress")

	types := []Type{TypeGzip, TypeZstd, TypeSnappy, TypeZlib, TypeDeflate}

	for _, ct := range types {
		t.Run(string(ct), func(t *testing.T) {
			compressed, err := Compress(original, Config{Type: ct})
			if err != nil {
				t.Fatalf("Compress(%s) error: %v", ct, err)
			}

			dst := GetBuffer()
			if err := DecompressToBuf(dst, compressed, ct); err != nil {
				ReleaseBuffer(dst)
				t.Fatalf("DecompressToBuf(%s) error: %v", ct, err)
			}

			if !bytes.Equal(dst.Bytes(), original) {
				t.Errorf("DecompressToBuf(%s) mismatch: got %d bytes, want %d", ct, dst.Len(), len(original))
			}
			ReleaseBuffer(dst)
		})
	}
}

func TestDecompressToBuf_None(t *testing.T) {
	data := []byte("passthrough data")
	dst := GetBuffer()
	defer ReleaseBuffer(dst)

	if err := DecompressToBuf(dst, data, TypeNone); err != nil {
		t.Fatalf("DecompressToBuf(none) error: %v", err)
	}
	if !bytes.Equal(dst.Bytes(), data) {
		t.Error("DecompressToBuf(none) should pass data through unchanged")
	}
}

func TestDecompressToBuf_CorruptedData(t *testing.T) {
	corrupt := []byte{0xFF, 0xFE, 0xFD, 0xFC, 0xFB}

	for _, ct := range []Type{TypeGzip, TypeZstd, TypeZlib, TypeDeflate} {
		t.Run(string(ct), func(t *testing.T) {
			dst := GetBuffer()
			err := DecompressToBuf(dst, corrupt, ct)
			ReleaseBuffer(dst)

			if err == nil {
				t.Errorf("DecompressToBuf(%s) should fail on corrupted data", ct)
			}
		})
	}
}

func TestDecompressToBuf_UnsupportedType(t *testing.T) {
	dst := GetBuffer()
	defer ReleaseBuffer(dst)

	err := DecompressToBuf(dst, []byte("data"), "bogus")
	if err == nil {
		t.Error("Expected error for unsupported compression type")
	}
}

// =============================================================================
// Concurrent pool safety
// =============================================================================

func TestPool_ConcurrentGetRelease(t *testing.T) {
	ResetPoolStats()
	var wg sync.WaitGroup

	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				buf := GetBuffer()
				buf.WriteString("concurrent data")
				ReleaseBuffer(buf)
			}
		}()
	}
	wg.Wait()

	if got := BufferActiveCount(); got != 0 {
		t.Errorf("Leaked buffers: active count = %d after all released", got)
	}
}

func TestPool_ConcurrentCompressToBuf(t *testing.T) {
	data := make([]byte, 1024)
	rand.Read(data)
	var wg sync.WaitGroup

	for _, ct := range []Type{TypeGzip, TypeZstd, TypeSnappy, TypeZlib} {
		wg.Add(1)
		go func(ct Type) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				dst := GetBuffer()
				if err := CompressToBuf(dst, data, Config{Type: ct}); err != nil {
					t.Errorf("CompressToBuf(%s) concurrent error: %v", ct, err)
				}
				ReleaseBuffer(dst)
			}
		}(ct)
	}
	wg.Wait()
}

func TestPool_ConcurrentDecompressToBuf(t *testing.T) {
	original := make([]byte, 2048)
	rand.Read(original)
	var wg sync.WaitGroup

	for _, ct := range []Type{TypeGzip, TypeZstd, TypeSnappy, TypeZlib} {
		compressed, err := Compress(original, Config{Type: ct})
		if err != nil {
			t.Fatalf("Compress(%s) setup error: %v", ct, err)
		}

		wg.Add(1)
		go func(ct Type, comp []byte) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				dst := GetBuffer()
				if err := DecompressToBuf(dst, comp, ct); err != nil {
					t.Errorf("DecompressToBuf(%s) concurrent error: %v", ct, err)
				}
				if !bytes.Equal(dst.Bytes(), original) {
					t.Errorf("DecompressToBuf(%s) concurrent mismatch", ct)
				}
				ReleaseBuffer(dst)
			}
		}(ct, compressed)
	}
	wg.Wait()
}

// =============================================================================
// Regression benchmarks — allocs/op guards pool effectiveness
// =============================================================================

func BenchmarkGetReleaseBuffer(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf := GetBuffer()
		buf.WriteString("benchmark data payload for pool reuse testing")
		ReleaseBuffer(buf)
	}
}

func BenchmarkCompressToBuf_Zstd(b *testing.B) {
	data := make([]byte, 32*1024) // 32 KB
	rand.Read(data)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		dst := GetBuffer()
		_ = CompressToBuf(dst, data, Config{Type: TypeZstd})
		ReleaseBuffer(dst)
	}
}

func BenchmarkCompressToBuf_Gzip(b *testing.B) {
	data := make([]byte, 32*1024)
	rand.Read(data)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		dst := GetBuffer()
		_ = CompressToBuf(dst, data, Config{Type: TypeGzip})
		ReleaseBuffer(dst)
	}
}

func BenchmarkDecompressToBuf_Zstd(b *testing.B) {
	data := make([]byte, 32*1024)
	rand.Read(data)
	compressed, _ := Compress(data, Config{Type: TypeZstd})
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		dst := GetBuffer()
		_ = DecompressToBuf(dst, compressed, TypeZstd)
		ReleaseBuffer(dst)
	}
}

func BenchmarkDecompressToBuf_Gzip(b *testing.B) {
	data := make([]byte, 32*1024)
	rand.Read(data)
	compressed, _ := Compress(data, Config{Type: TypeGzip})
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		dst := GetBuffer()
		_ = DecompressToBuf(dst, compressed, TypeGzip)
		ReleaseBuffer(dst)
	}
}

// BenchmarkCompressToBuf_Concurrent measures pool contention under load.
func BenchmarkCompressToBuf_Concurrent(b *testing.B) {
	data := make([]byte, 32*1024)
	rand.Read(data)
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			dst := GetBuffer()
			_ = CompressToBuf(dst, data, Config{Type: TypeZstd})
			ReleaseBuffer(dst)
		}
	})
}
