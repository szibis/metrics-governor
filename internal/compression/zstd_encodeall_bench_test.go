package compression

import (
	"testing"
)

// BenchmarkZstdCompress_EncodeAll_100KB benchmarks the production Compress() path
// for zstd, which uses EncodeAll with pooled encoder + pooled destination buffer.
func BenchmarkZstdCompress_EncodeAll_100KB(b *testing.B) {
	data := makeTestData(100 * 1024)
	cfg := Config{Type: TypeZstd, Level: ZstdSpeedDefault}
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for b.Loop() {
		result, err := Compress(data, cfg)
		if err != nil {
			b.Fatal(err)
		}
		_ = result
	}
}

// BenchmarkZstdDecompress_DecodeAll_100KB benchmarks the production Decompress() path
// for zstd, which uses DecodeAll with pooled decoder + pooled destination buffer.
func BenchmarkZstdDecompress_DecodeAll_100KB(b *testing.B) {
	data := makeTestData(100 * 1024)
	compressed, err := Compress(data, Config{Type: TypeZstd, Level: ZstdSpeedDefault})
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for b.Loop() {
		result, err := Decompress(compressed, TypeZstd)
		if err != nil {
			b.Fatal(err)
		}
		_ = result
	}
}

// BenchmarkZstdRoundTrip_EncodeAll_DecodeAll_100KB benchmarks a full
// compress → decompress roundtrip through the production API.
func BenchmarkZstdRoundTrip_EncodeAll_DecodeAll_100KB(b *testing.B) {
	data := makeTestData(100 * 1024)
	cfg := Config{Type: TypeZstd, Level: ZstdSpeedDefault}
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for b.Loop() {
		compressed, err := Compress(data, cfg)
		if err != nil {
			b.Fatal(err)
		}
		result, err := Decompress(compressed, TypeZstd)
		if err != nil {
			b.Fatal(err)
		}
		_ = result
	}
}

func makeTestData(size int) []byte {
	// Semi-realistic metrics-like data with repeated patterns
	data := make([]byte, size)
	pattern := []byte(`{"metric_name":"http_request_duration_seconds","labels":{"method":"GET","status":"200","path":"/api/v1/query"},"value":0.042,"timestamp":1700000000}`)
	for i := 0; i < len(data); i += len(pattern) {
		copy(data[i:], pattern)
	}
	return data
}
