package compression

import (
	"runtime"
	"testing"
)

func TestResourceUsage_CompressionPooling(t *testing.T) {
	testData := make([]byte, 10*1024) // 10KB
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	cfg := Config{Type: TypeGzip, Level: LevelDefault}

	var mBefore, mAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&mBefore)

	for i := 0; i < 10000; i++ {
		compressed, err := Compress(testData, cfg)
		if err != nil {
			t.Fatalf("Compress error at iteration %d: %v", i, err)
		}
		_, err = Decompress(compressed, cfg.Type)
		if err != nil {
			t.Fatalf("Decompress error at iteration %d: %v", i, err)
		}
	}

	runtime.GC()
	runtime.ReadMemStats(&mAfter)

	totalAllocs := mAfter.TotalAlloc - mBefore.TotalAlloc
	allocsPerIter := totalAllocs / 10000
	t.Logf("Total allocations: %d bytes over 10000 iterations", totalAllocs)
	t.Logf("Allocs per iteration: %d bytes", allocsPerIter)
	t.Logf("GC cycles: %d", mAfter.NumGC-mBefore.NumGC)
	t.Logf("Heap in use: before=%d after=%d", mBefore.HeapInuse, mAfter.HeapInuse)

	// Gzip compress+decompress allocates per iteration due to reader creation
	// and buffer copies. With the race detector, sync.Pool is largely disabled,
	// so allocations are higher. Allow up to 512KB per iteration.
	maxAllocsPerIter := uint64(512 * 1024)
	if allocsPerIter > maxAllocsPerIter {
		t.Errorf("Allocation budget exceeded: %d > %d bytes/iter", allocsPerIter, maxAllocsPerIter)
	}
}

func TestResourceUsage_CompressionPooling_Zstd(t *testing.T) {
	testData := make([]byte, 10*1024) // 10KB
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	cfg := Config{Type: TypeZstd, Level: LevelDefault}

	var mBefore, mAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&mBefore)

	for i := 0; i < 10000; i++ {
		compressed, err := Compress(testData, cfg)
		if err != nil {
			t.Fatalf("Compress error at iteration %d: %v", i, err)
		}
		_, err = Decompress(compressed, cfg.Type)
		if err != nil {
			t.Fatalf("Decompress error at iteration %d: %v", i, err)
		}
	}

	runtime.GC()
	runtime.ReadMemStats(&mAfter)

	totalAllocs := mAfter.TotalAlloc - mBefore.TotalAlloc
	allocsPerIter := totalAllocs / 10000
	t.Logf("Total allocations: %d bytes over 10000 iterations", totalAllocs)
	t.Logf("Allocs per iteration: %d bytes", allocsPerIter)
	t.Logf("GC cycles: %d", mAfter.NumGC-mBefore.NumGC)
	t.Logf("Heap in use: before=%d after=%d", mBefore.HeapInuse, mAfter.HeapInuse)

	// Zstd encoder/decoder are large structures. With the race detector,
	// sync.Pool is largely disabled, so each iteration creates fresh encoder/decoder.
	// Allow up to 2MB per iteration to accommodate race detector overhead.
	maxAllocsPerIter := uint64(2 * 1024 * 1024)
	if allocsPerIter > maxAllocsPerIter {
		t.Errorf("Allocation budget exceeded: %d > %d bytes/iter", allocsPerIter, maxAllocsPerIter)
	}
}

func TestResourceUsage_CompressionPooling_Snappy(t *testing.T) {
	testData := make([]byte, 10*1024) // 10KB
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	cfg := Config{Type: TypeSnappy}

	var mBefore, mAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&mBefore)

	for i := 0; i < 10000; i++ {
		compressed, err := Compress(testData, cfg)
		if err != nil {
			t.Fatalf("Compress error at iteration %d: %v", i, err)
		}
		_, err = Decompress(compressed, cfg.Type)
		if err != nil {
			t.Fatalf("Decompress error at iteration %d: %v", i, err)
		}
	}

	runtime.GC()
	runtime.ReadMemStats(&mAfter)

	totalAllocs := mAfter.TotalAlloc - mBefore.TotalAlloc
	allocsPerIter := totalAllocs / 10000
	t.Logf("Total allocations: %d bytes over 10000 iterations", totalAllocs)
	t.Logf("Allocs per iteration: %d bytes", allocsPerIter)
	t.Logf("GC cycles: %d", mAfter.NumGC-mBefore.NumGC)
	t.Logf("Heap in use: before=%d after=%d", mBefore.HeapInuse, mAfter.HeapInuse)

	// Snappy is lightweight; expect under 50KB per iteration for 10KB data
	maxAllocsPerIter := uint64(50 * 1024)
	if allocsPerIter > maxAllocsPerIter {
		t.Errorf("Allocation budget exceeded: %d > %d bytes/iter", allocsPerIter, maxAllocsPerIter)
	}
}
