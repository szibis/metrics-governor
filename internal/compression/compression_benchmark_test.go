package compression

import (
	"testing"
)

// Test data sizes
var testDataSizes = []struct {
	name string
	size int
}{
	{"1KB", 1024},
	{"10KB", 10 * 1024},
	{"100KB", 100 * 1024},
	{"1MB", 1024 * 1024},
}

// generateTestData creates pseudo-random but deterministic test data
func generateTestData(size int) []byte {
	data := make([]byte, size)
	// Create semi-compressible data (simulates protobuf metrics)
	for i := 0; i < size; i++ {
		// Mix of repeated patterns and pseudo-random data
		if i%100 < 70 {
			// Repeated pattern (common in metrics data)
			data[i] = byte(i%256 ^ (i/256)%256)
		} else {
			// Less compressible data
			data[i] = byte((i*7 + 13) % 256)
		}
	}
	return data
}

// BenchmarkCompress_Gzip benchmarks gzip compression at different sizes
func BenchmarkCompress_Gzip(b *testing.B) {
	for _, ts := range testDataSizes {
		data := generateTestData(ts.size)
		cfg := Config{Type: TypeGzip, Level: LevelDefault}

		b.Run(ts.name, func(b *testing.B) {
			b.SetBytes(int64(ts.size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = Compress(data, cfg)
			}
		})
	}
}

// BenchmarkDecompress_Gzip benchmarks gzip decompression at different sizes
func BenchmarkDecompress_Gzip(b *testing.B) {
	for _, ts := range testDataSizes {
		data := generateTestData(ts.size)
		cfg := Config{Type: TypeGzip, Level: LevelDefault}
		compressed, _ := Compress(data, cfg)

		b.Run(ts.name, func(b *testing.B) {
			b.SetBytes(int64(len(compressed)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = Decompress(compressed, TypeGzip)
			}
		})
	}
}

// BenchmarkCompress_Zstd benchmarks zstd compression at different sizes
func BenchmarkCompress_Zstd(b *testing.B) {
	for _, ts := range testDataSizes {
		data := generateTestData(ts.size)
		cfg := Config{Type: TypeZstd, Level: LevelDefault}

		b.Run(ts.name, func(b *testing.B) {
			b.SetBytes(int64(ts.size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = Compress(data, cfg)
			}
		})
	}
}

// BenchmarkDecompress_Zstd benchmarks zstd decompression at different sizes
func BenchmarkDecompress_Zstd(b *testing.B) {
	for _, ts := range testDataSizes {
		data := generateTestData(ts.size)
		cfg := Config{Type: TypeZstd, Level: LevelDefault}
		compressed, _ := Compress(data, cfg)

		b.Run(ts.name, func(b *testing.B) {
			b.SetBytes(int64(len(compressed)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = Decompress(compressed, TypeZstd)
			}
		})
	}
}

// BenchmarkCompress_Snappy benchmarks snappy compression at different sizes
func BenchmarkCompress_Snappy(b *testing.B) {
	for _, ts := range testDataSizes {
		data := generateTestData(ts.size)
		cfg := Config{Type: TypeSnappy, Level: LevelDefault}

		b.Run(ts.name, func(b *testing.B) {
			b.SetBytes(int64(ts.size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = Compress(data, cfg)
			}
		})
	}
}

// BenchmarkDecompress_Snappy benchmarks snappy decompression at different sizes
func BenchmarkDecompress_Snappy(b *testing.B) {
	for _, ts := range testDataSizes {
		data := generateTestData(ts.size)
		cfg := Config{Type: TypeSnappy, Level: LevelDefault}
		compressed, _ := Compress(data, cfg)

		b.Run(ts.name, func(b *testing.B) {
			b.SetBytes(int64(len(compressed)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = Decompress(compressed, TypeSnappy)
			}
		})
	}
}

// BenchmarkCompress_LZ4 benchmarks lz4 compression at different sizes
func BenchmarkCompress_LZ4(b *testing.B) {
	for _, ts := range testDataSizes {
		data := generateTestData(ts.size)
		cfg := Config{Type: TypeLZ4, Level: LevelDefault}

		b.Run(ts.name, func(b *testing.B) {
			b.SetBytes(int64(ts.size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = Compress(data, cfg)
			}
		})
	}
}

// BenchmarkDecompress_LZ4 benchmarks lz4 decompression at different sizes
func BenchmarkDecompress_LZ4(b *testing.B) {
	for _, ts := range testDataSizes {
		data := generateTestData(ts.size)
		cfg := Config{Type: TypeLZ4, Level: LevelDefault}
		compressed, _ := Compress(data, cfg)

		b.Run(ts.name, func(b *testing.B) {
			b.SetBytes(int64(len(compressed)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = Decompress(compressed, TypeLZ4)
			}
		})
	}
}

// BenchmarkCompress_Zlib benchmarks zlib compression at different sizes
func BenchmarkCompress_Zlib(b *testing.B) {
	for _, ts := range testDataSizes {
		data := generateTestData(ts.size)
		cfg := Config{Type: TypeZlib, Level: LevelDefault}

		b.Run(ts.name, func(b *testing.B) {
			b.SetBytes(int64(ts.size))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = Compress(data, cfg)
			}
		})
	}
}

// BenchmarkCompress_AllAlgorithms compares all compression algorithms
func BenchmarkCompress_AllAlgorithms(b *testing.B) {
	data := generateTestData(100 * 1024) // 100KB test data

	algorithms := []struct {
		name string
		cfg  Config
	}{
		{"gzip", Config{Type: TypeGzip, Level: LevelDefault}},
		{"zstd", Config{Type: TypeZstd, Level: LevelDefault}},
		{"snappy", Config{Type: TypeSnappy, Level: LevelDefault}},
		{"lz4", Config{Type: TypeLZ4, Level: LevelDefault}},
		{"zlib", Config{Type: TypeZlib, Level: LevelDefault}},
		{"deflate", Config{Type: TypeDeflate, Level: LevelDefault}},
	}

	for _, alg := range algorithms {
		b.Run(alg.name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = Compress(data, alg.cfg)
			}
		})
	}
}

// BenchmarkGzip_Levels benchmarks different gzip compression levels
func BenchmarkGzip_Levels(b *testing.B) {
	data := generateTestData(100 * 1024) // 100KB

	levels := []struct {
		name  string
		level Level
	}{
		{"fastest", GzipBestSpeed},
		{"default", GzipDefaultCompression},
		{"best", GzipBestCompression},
	}

	for _, l := range levels {
		cfg := Config{Type: TypeGzip, Level: l.level}
		b.Run(l.name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = Compress(data, cfg)
			}
		})
	}
}

// BenchmarkZstd_Levels benchmarks different zstd compression levels
func BenchmarkZstd_Levels(b *testing.B) {
	data := generateTestData(100 * 1024) // 100KB

	levels := []struct {
		name  string
		level Level
	}{
		{"fastest", ZstdSpeedFastest},
		{"default", ZstdSpeedDefault},
		{"better", ZstdSpeedBetterCompression},
		{"best", ZstdSpeedBestCompression},
	}

	for _, l := range levels {
		cfg := Config{Type: TypeZstd, Level: l.level}
		b.Run(l.name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = Compress(data, cfg)
			}
		})
	}
}

// BenchmarkRoundTrip_AllAlgorithms benchmarks compress+decompress cycle
func BenchmarkRoundTrip_AllAlgorithms(b *testing.B) {
	data := generateTestData(100 * 1024) // 100KB

	algorithms := []struct {
		name    string
		cfg     Config
		compTyp Type
	}{
		{"gzip", Config{Type: TypeGzip, Level: LevelDefault}, TypeGzip},
		{"zstd", Config{Type: TypeZstd, Level: LevelDefault}, TypeZstd},
		{"snappy", Config{Type: TypeSnappy, Level: LevelDefault}, TypeSnappy},
		{"lz4", Config{Type: TypeLZ4, Level: LevelDefault}, TypeLZ4},
	}

	for _, alg := range algorithms {
		b.Run(alg.name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				compressed, _ := Compress(data, alg.cfg)
				_, _ = Decompress(compressed, alg.compTyp)
			}
		})
	}
}
