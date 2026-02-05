package compression

import (
	"bytes"
	"runtime"
	"sync"
	"testing"
)

// testData is a shared payload used by all race and memory-leak tests.
var testData = bytes.Repeat([]byte("test data for compression benchmarking "), 100)

// ---------------------------------------------------------------------------
// Race condition tests
// ---------------------------------------------------------------------------

// TestRace_Compress_ConcurrentGzip verifies that concurrent gzip compression
// through the shared sync.Pool does not trigger the race detector.
func TestRace_Compress_ConcurrentGzip(t *testing.T) {
	const goroutines = 8
	const iterations = 500

	cfg := Config{Type: TypeGzip, Level: LevelDefault}

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				out, err := Compress(testData, cfg)
				if err != nil {
					t.Errorf("Compress(gzip) failed: %v", err)
					return
				}
				if len(out) == 0 {
					t.Errorf("Compress(gzip) returned empty result")
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestRace_Compress_ConcurrentZstd verifies that concurrent zstd compression
// through the shared sync.Pool does not trigger the race detector.
func TestRace_Compress_ConcurrentZstd(t *testing.T) {
	const goroutines = 8
	const iterations = 500

	cfg := Config{Type: TypeZstd, Level: LevelDefault}

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				out, err := Compress(testData, cfg)
				if err != nil {
					t.Errorf("Compress(zstd) failed: %v", err)
					return
				}
				if len(out) == 0 {
					t.Errorf("Compress(zstd) returned empty result")
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestRace_Compress_ConcurrentSnappy verifies that concurrent snappy
// compression does not trigger the race detector.
func TestRace_Compress_ConcurrentSnappy(t *testing.T) {
	const goroutines = 8
	const iterations = 500

	cfg := Config{Type: TypeSnappy, Level: LevelDefault}

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				out, err := Compress(testData, cfg)
				if err != nil {
					t.Errorf("Compress(snappy) failed: %v", err)
					return
				}
				if len(out) == 0 {
					t.Errorf("Compress(snappy) returned empty result")
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestRace_Compress_MixedTypes runs different compression types concurrently
// to verify there is no cross-pool contention or data race.
func TestRace_Compress_MixedTypes(t *testing.T) {
	const iterations = 300

	types := []Config{
		{Type: TypeGzip, Level: LevelDefault},
		{Type: TypeZstd, Level: LevelDefault},
		{Type: TypeSnappy, Level: LevelDefault},
		{Type: TypeZlib, Level: LevelDefault},
		{Type: TypeDeflate, Level: LevelDefault},
		{Type: TypeGzip, Level: LevelFastest},
		{Type: TypeZstd, Level: LevelFastest},
		{Type: TypeNone},
	}

	var wg sync.WaitGroup
	wg.Add(len(types))
	for _, cfg := range types {
		go func(c Config) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				out, err := Compress(testData, c)
				if err != nil {
					t.Errorf("Compress(%s) failed: %v", c.Type, err)
					return
				}
				if len(out) == 0 {
					t.Errorf("Compress(%s) returned empty result", c.Type)
					return
				}
			}
		}(cfg)
	}
	wg.Wait()
}

// TestRace_Decompress_ConcurrentGzip compresses once, then has 8 goroutines
// decompress the same payload concurrently.
func TestRace_Decompress_ConcurrentGzip(t *testing.T) {
	const goroutines = 8
	const iterations = 500

	compressed, err := Compress(testData, Config{Type: TypeGzip, Level: LevelDefault})
	if err != nil {
		t.Fatalf("setup: Compress(gzip) failed: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				out, err := Decompress(compressed, TypeGzip)
				if err != nil {
					t.Errorf("Decompress(gzip) failed: %v", err)
					return
				}
				if !bytes.Equal(out, testData) {
					t.Errorf("Decompress(gzip) round-trip mismatch")
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestRace_Decompress_ConcurrentZstd compresses once, then has 8 goroutines
// decompress the same payload concurrently using the pooled zstd decoder.
func TestRace_Decompress_ConcurrentZstd(t *testing.T) {
	const goroutines = 8
	const iterations = 500

	compressed, err := Compress(testData, Config{Type: TypeZstd, Level: LevelDefault})
	if err != nil {
		t.Fatalf("setup: Compress(zstd) failed: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				out, err := Decompress(compressed, TypeZstd)
				if err != nil {
					t.Errorf("Decompress(zstd) failed: %v", err)
					return
				}
				if !bytes.Equal(out, testData) {
					t.Errorf("Decompress(zstd) round-trip mismatch")
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestRace_CompressDecompress_ConcurrentRoundtrip has each goroutine compress
// then immediately decompress, exercising both encoder and decoder pools
// concurrently.
func TestRace_CompressDecompress_ConcurrentRoundtrip(t *testing.T) {
	const goroutines = 8
	const iterations = 200

	types := []struct {
		cfg  Config
		typ  Type
		name string
	}{
		{Config{Type: TypeGzip, Level: LevelDefault}, TypeGzip, "gzip"},
		{Config{Type: TypeZstd, Level: LevelDefault}, TypeZstd, "zstd"},
		{Config{Type: TypeSnappy, Level: LevelDefault}, TypeSnappy, "snappy"},
		{Config{Type: TypeZlib, Level: LevelDefault}, TypeZlib, "zlib"},
	}

	var wg sync.WaitGroup
	wg.Add(goroutines * len(types))
	for _, tc := range types {
		for i := 0; i < goroutines; i++ {
			go func(cfg Config, decomTyp Type, name string) {
				defer wg.Done()
				for j := 0; j < iterations; j++ {
					compressed, err := Compress(testData, cfg)
					if err != nil {
						t.Errorf("Compress(%s) failed: %v", name, err)
						return
					}
					decompressed, err := Decompress(compressed, decomTyp)
					if err != nil {
						t.Errorf("Decompress(%s) failed: %v", name, err)
						return
					}
					if !bytes.Equal(decompressed, testData) {
						t.Errorf("round-trip mismatch for %s", name)
						return
					}
				}
			}(tc.cfg, tc.typ, tc.name)
		}
	}
	wg.Wait()
}

// TestRace_PoolStats_ConcurrentAccess reads PoolStats and calls
// ResetPoolStats concurrently while compression is running, verifying that
// atomic counter access is race-free.
func TestRace_PoolStats_ConcurrentAccess(t *testing.T) {
	const goroutines = 8
	const iterations = 300

	cfg := Config{Type: TypeGzip, Level: LevelDefault}

	var wg sync.WaitGroup

	// Goroutines doing compression work.
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_, _ = Compress(testData, cfg)
			}
		}()
	}

	// Goroutines reading stats.
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				snap := PoolStats()
				// Access all fields to ensure they are readable without races.
				_ = snap.CompressionPoolGets
				_ = snap.CompressionPoolPuts
				_ = snap.CompressionPoolDiscards
				_ = snap.CompressionPoolNews
				_ = snap.BufferPoolGets
				_ = snap.BufferPoolPuts
			}
		}()
	}

	// Goroutines resetting stats.
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				ResetPoolStats()
			}
		}()
	}

	wg.Wait()
}

// ---------------------------------------------------------------------------
// Memory leak tests
// ---------------------------------------------------------------------------

// heapInUseMB forces a GC cycle and returns the current HeapInuse in MB.
func heapInUseMB(t *testing.T) float64 {
	t.Helper()
	runtime.GC()
	runtime.GC() // two cycles to allow finalizers to run
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.HeapInuse) / (1024 * 1024)
}

// TestMemLeak_Compress_RepeatedCycles compresses many times and checks that
// heap usage remains bounded, verifying that sync.Pool buffers are properly
// recycled and not leaked.
func TestMemLeak_Compress_RepeatedCycles(t *testing.T) {
	const totalIterations = 5000
	const maxHeapGrowthMB = 30.0

	cfg := Config{Type: TypeGzip, Level: LevelDefault}

	// Warm up the pool with a few iterations so initial allocations settle.
	for i := 0; i < 50; i++ {
		_, _ = Compress(testData, cfg)
	}

	baseline := heapInUseMB(t)
	t.Logf("baseline heap: %.2f MB", baseline)

	for i := 0; i < totalIterations; i++ {
		out, err := Compress(testData, cfg)
		if err != nil {
			t.Fatalf("Compress(gzip) iteration %d failed: %v", i, err)
		}
		if len(out) == 0 {
			t.Fatalf("Compress(gzip) returned empty result at iteration %d", i)
		}
	}

	final := heapInUseMB(t)
	growth := final - baseline
	t.Logf("final heap: %.2f MB, growth: %.2f MB", final, growth)

	if growth > maxHeapGrowthMB {
		t.Errorf("possible memory leak: heap grew by %.2f MB (threshold %.2f MB)",
			growth, maxHeapGrowthMB)
	}
}

// TestMemLeak_CompressDecompress_Cycles performs repeated compress/decompress
// round-trips and verifies that heap usage stays bounded, ensuring no leaks
// from encoder or decoder pools.
func TestMemLeak_CompressDecompress_Cycles(t *testing.T) {
	const totalIterations = 5000
	const maxHeapGrowthMB = 30.0

	types := []struct {
		cfg  Config
		typ  Type
		name string
	}{
		{Config{Type: TypeGzip, Level: LevelDefault}, TypeGzip, "gzip"},
		{Config{Type: TypeZstd, Level: LevelDefault}, TypeZstd, "zstd"},
		{Config{Type: TypeSnappy, Level: LevelDefault}, TypeSnappy, "snappy"},
	}

	for _, tc := range types {
		t.Run(tc.name, func(t *testing.T) {
			// Warm up the pools.
			for i := 0; i < 50; i++ {
				compressed, err := Compress(testData, tc.cfg)
				if err != nil {
					t.Fatalf("warmup Compress(%s) failed: %v", tc.name, err)
				}
				_, err = Decompress(compressed, tc.typ)
				if err != nil {
					t.Fatalf("warmup Decompress(%s) failed: %v", tc.name, err)
				}
			}

			baseline := heapInUseMB(t)
			t.Logf("[%s] baseline heap: %.2f MB", tc.name, baseline)

			for i := 0; i < totalIterations; i++ {
				compressed, err := Compress(testData, tc.cfg)
				if err != nil {
					t.Fatalf("Compress(%s) iteration %d failed: %v", tc.name, i, err)
				}
				decompressed, err := Decompress(compressed, tc.typ)
				if err != nil {
					t.Fatalf("Decompress(%s) iteration %d failed: %v", tc.name, i, err)
				}
				if !bytes.Equal(decompressed, testData) {
					t.Fatalf("round-trip mismatch for %s at iteration %d", tc.name, i)
				}
			}

			final := heapInUseMB(t)
			growth := final - baseline
			t.Logf("[%s] final heap: %.2f MB, growth: %.2f MB", tc.name, final, growth)

			if growth > maxHeapGrowthMB {
				t.Errorf("[%s] possible memory leak: heap grew by %.2f MB (threshold %.2f MB)",
					tc.name, growth, maxHeapGrowthMB)
			}
		})
	}
}
