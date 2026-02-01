package cardinality

import (
	"fmt"
	"sync"
	"testing"
)

func TestBloomTracker_Add(t *testing.T) {
	tracker := NewBloomTracker(DefaultConfig())

	// First add should return true (new element)
	if !tracker.Add([]byte("key1")) {
		t.Error("First add should return true for new element")
	}

	// Second add of same key should return false (already exists)
	if tracker.Add([]byte("key1")) {
		t.Error("Second add should return false for existing element")
	}

	// Different key should return true
	if !tracker.Add([]byte("key2")) {
		t.Error("Add should return true for new element")
	}
}

func TestBloomTracker_TestOnly(t *testing.T) {
	tracker := NewBloomTracker(DefaultConfig())

	// Before adding, TestOnly should return false
	if tracker.TestOnly([]byte("key1")) {
		t.Error("TestOnly should return false for non-existent element")
	}

	tracker.Add([]byte("key1"))

	// After adding, TestOnly should return true
	if !tracker.TestOnly([]byte("key1")) {
		t.Error("TestOnly should return true for existing element")
	}

	// TestOnly should not modify state
	if tracker.Count() != 1 {
		t.Errorf("TestOnly should not change count, got %d", tracker.Count())
	}
}

func TestBloomTracker_Count(t *testing.T) {
	tracker := NewBloomTracker(DefaultConfig())

	if tracker.Count() != 0 {
		t.Errorf("Initial count should be 0, got %d", tracker.Count())
	}

	tracker.Add([]byte("key1"))
	tracker.Add([]byte("key2"))
	tracker.Add([]byte("key3"))

	if tracker.Count() != 3 {
		t.Errorf("Count should be 3, got %d", tracker.Count())
	}

	// Adding duplicates should not increase count
	tracker.Add([]byte("key1"))
	tracker.Add([]byte("key2"))

	if tracker.Count() != 3 {
		t.Errorf("Count should still be 3 after duplicates, got %d", tracker.Count())
	}
}

func TestBloomTracker_Reset(t *testing.T) {
	tracker := NewBloomTracker(DefaultConfig())

	tracker.Add([]byte("key1"))
	tracker.Add([]byte("key2"))

	tracker.Reset()

	if tracker.Count() != 0 {
		t.Errorf("Count should be 0 after reset, got %d", tracker.Count())
	}

	if tracker.TestOnly([]byte("key1")) {
		t.Error("TestOnly should return false after reset")
	}

	// Should be able to add same key again after reset
	if !tracker.Add([]byte("key1")) {
		t.Error("Should be able to add key after reset")
	}
}

func TestBloomTracker_MemoryUsage(t *testing.T) {
	cfg := Config{
		ExpectedItems:     1000000, // 1M items
		FalsePositiveRate: 0.01,
	}
	tracker := NewBloomTracker(cfg)

	mem := tracker.MemoryUsage()
	// For 1M items at 1% FPR, should be around 1.2MB
	if mem < 1000000 || mem > 2000000 {
		t.Errorf("Memory usage for 1M items should be ~1.2MB, got %d bytes", mem)
	}
}

func TestBloomTracker_Concurrent(t *testing.T) {
	tracker := NewBloomTracker(DefaultConfig())

	var wg sync.WaitGroup
	numGoroutines := 10
	keysPerGoroutine := 1000

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < keysPerGoroutine; j++ {
				key := fmt.Sprintf("g%d-key%d", id, j)
				tracker.Add([]byte(key))
				tracker.TestOnly([]byte(key))
				tracker.Count()
			}
		}(i)
	}

	wg.Wait()

	// Should have added approximately numGoroutines * keysPerGoroutine unique keys
	// (may be slightly less due to false positives)
	expected := int64(numGoroutines * keysPerGoroutine)
	actual := tracker.Count()
	// Allow up to 2% deviation due to false positives
	deviation := float64(expected-actual) / float64(expected)
	if deviation > 0.02 {
		t.Errorf("Count deviation too high: expected ~%d, got %d (%.2f%% deviation)",
			expected, actual, deviation*100)
	}
}

// ExactTracker tests

func TestExactTracker_Add(t *testing.T) {
	tracker := NewExactTracker()

	if !tracker.Add([]byte("key1")) {
		t.Error("First add should return true for new element")
	}

	if tracker.Add([]byte("key1")) {
		t.Error("Second add should return false for existing element")
	}

	if !tracker.Add([]byte("key2")) {
		t.Error("Add should return true for new element")
	}
}

func TestExactTracker_TestOnly(t *testing.T) {
	tracker := NewExactTracker()

	if tracker.TestOnly([]byte("key1")) {
		t.Error("TestOnly should return false for non-existent element")
	}

	tracker.Add([]byte("key1"))

	if !tracker.TestOnly([]byte("key1")) {
		t.Error("TestOnly should return true for existing element")
	}
}

func TestExactTracker_Count(t *testing.T) {
	tracker := NewExactTracker()

	if tracker.Count() != 0 {
		t.Errorf("Initial count should be 0, got %d", tracker.Count())
	}

	tracker.Add([]byte("key1"))
	tracker.Add([]byte("key2"))
	tracker.Add([]byte("key3"))

	if tracker.Count() != 3 {
		t.Errorf("Count should be 3, got %d", tracker.Count())
	}
}

func TestExactTracker_Reset(t *testing.T) {
	tracker := NewExactTracker()

	tracker.Add([]byte("key1"))
	tracker.Add([]byte("key2"))

	tracker.Reset()

	if tracker.Count() != 0 {
		t.Errorf("Count should be 0 after reset, got %d", tracker.Count())
	}

	if tracker.TestOnly([]byte("key1")) {
		t.Error("TestOnly should return false after reset")
	}
}

func TestExactTracker_Concurrent(t *testing.T) {
	tracker := NewExactTracker()

	var wg sync.WaitGroup
	numGoroutines := 10
	keysPerGoroutine := 1000

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < keysPerGoroutine; j++ {
				key := fmt.Sprintf("g%d-key%d", id, j)
				tracker.Add([]byte(key))
				tracker.TestOnly([]byte(key))
				tracker.Count()
			}
		}(i)
	}

	wg.Wait()

	expected := int64(numGoroutines * keysPerGoroutine)
	if tracker.Count() != expected {
		t.Errorf("Count should be %d, got %d", expected, tracker.Count())
	}
}

// Config and factory tests

func TestParseMode(t *testing.T) {
	tests := []struct {
		input    string
		expected Mode
	}{
		{"bloom", ModeBloom},
		{"exact", ModeExact},
		{"unknown", ModeBloom}, // Default to bloom
		{"", ModeBloom},
	}

	for _, tt := range tests {
		result := ParseMode(tt.input)
		if result != tt.expected {
			t.Errorf("ParseMode(%q) = %v, want %v", tt.input, result, tt.expected)
		}
	}
}

func TestModeString(t *testing.T) {
	tests := []struct {
		mode     Mode
		expected string
	}{
		{ModeBloom, "bloom"},
		{ModeExact, "exact"},
		{Mode(99), "unknown"},
	}

	for _, tt := range tests {
		result := tt.mode.String()
		if result != tt.expected {
			t.Errorf("Mode(%d).String() = %q, want %q", tt.mode, result, tt.expected)
		}
	}
}

func TestNewTracker(t *testing.T) {
	bloomCfg := Config{
		Mode:              ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}
	bloomTracker := NewTracker(bloomCfg)
	if _, ok := bloomTracker.(*BloomTracker); !ok {
		t.Error("NewTracker with ModeBloom should return *BloomTracker")
	}

	exactCfg := Config{
		Mode: ModeExact,
	}
	exactTracker := NewTracker(exactCfg)
	if _, ok := exactTracker.(*ExactTracker); !ok {
		t.Error("NewTracker with ModeExact should return *ExactTracker")
	}
}

func TestNewTrackerFromGlobal(t *testing.T) {
	// Save original config
	original := GlobalConfig

	// Test with bloom mode
	GlobalConfig = Config{
		Mode:              ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}
	tracker := NewTrackerFromGlobal()
	if _, ok := tracker.(*BloomTracker); !ok {
		t.Error("NewTrackerFromGlobal with ModeBloom should return *BloomTracker")
	}

	// Test with exact mode
	GlobalConfig = Config{Mode: ModeExact}
	tracker = NewTrackerFromGlobal()
	if _, ok := tracker.(*ExactTracker); !ok {
		t.Error("NewTrackerFromGlobal with ModeExact should return *ExactTracker")
	}

	// Restore original config
	GlobalConfig = original
}

// Benchmarks

func BenchmarkBloomTracker_Add(b *testing.B) {
	tracker := NewBloomTracker(DefaultConfig())
	keys := make([][]byte, b.N)
	for i := 0; i < b.N; i++ {
		keys[i] = []byte(fmt.Sprintf("metric{service=\"svc%d\",env=\"prod\",cluster=\"us-east-1\",pod=\"pod-%d\"}", i%100, i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tracker.Add(keys[i])
	}
}

func BenchmarkExactTracker_Add(b *testing.B) {
	tracker := NewExactTracker()
	keys := make([][]byte, b.N)
	for i := 0; i < b.N; i++ {
		keys[i] = []byte(fmt.Sprintf("metric{service=\"svc%d\",env=\"prod\",cluster=\"us-east-1\",pod=\"pod-%d\"}", i%100, i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tracker.Add(keys[i])
	}
}

func BenchmarkBloomTracker_TestOnly(b *testing.B) {
	tracker := NewBloomTracker(DefaultConfig())
	for i := 0; i < 10000; i++ {
		tracker.Add([]byte(fmt.Sprintf("key%d", i)))
	}

	keys := make([][]byte, b.N)
	for i := 0; i < b.N; i++ {
		keys[i] = []byte(fmt.Sprintf("key%d", i%20000)) // 50% hit rate
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tracker.TestOnly(keys[i])
	}
}

func BenchmarkExactTracker_TestOnly(b *testing.B) {
	tracker := NewExactTracker()
	for i := 0; i < 10000; i++ {
		tracker.Add([]byte(fmt.Sprintf("key%d", i)))
	}

	keys := make([][]byte, b.N)
	for i := 0; i < b.N; i++ {
		keys[i] = []byte(fmt.Sprintf("key%d", i%20000)) // 50% hit rate
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tracker.TestOnly(keys[i])
	}
}

// Memory comparison benchmark
func BenchmarkMemoryComparison(b *testing.B) {
	numItems := 100000

	b.Run("BloomTracker", func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			tracker := NewBloomTracker(Config{
				ExpectedItems:     uint(numItems),
				FalsePositiveRate: 0.01,
			})
			for i := 0; i < numItems; i++ {
				tracker.Add([]byte(fmt.Sprintf("service=\"svc%d\",env=\"prod\",cluster=\"us-east-1\",instance=\"i-%d\"", i%100, i)))
			}
			b.ReportMetric(float64(tracker.MemoryUsage()), "bytes")
		}
	})

	b.Run("ExactTracker", func(b *testing.B) {
		for n := 0; n < b.N; n++ {
			tracker := NewExactTracker()
			for i := 0; i < numItems; i++ {
				tracker.Add([]byte(fmt.Sprintf("service=\"svc%d\",env=\"prod\",cluster=\"us-east-1\",instance=\"i-%d\"", i%100, i)))
			}
			b.ReportMetric(float64(tracker.MemoryUsage()), "bytes")
		}
	})
}

// Test false positive rate
func TestBloomTracker_FalsePositiveRate(t *testing.T) {
	cfg := Config{
		ExpectedItems:     10000,
		FalsePositiveRate: 0.01, // 1%
	}
	tracker := NewBloomTracker(cfg)

	// Add 10000 items
	for i := 0; i < 10000; i++ {
		tracker.Add([]byte(fmt.Sprintf("existing-key-%d", i)))
	}

	// Test 10000 items that were NOT added
	falsePositives := 0
	for i := 0; i < 10000; i++ {
		if tracker.TestOnly([]byte(fmt.Sprintf("non-existing-key-%d", i))) {
			falsePositives++
		}
	}

	fpr := float64(falsePositives) / 10000.0
	// Allow 2x the configured rate as margin (filters can vary)
	if fpr > 0.02 {
		t.Errorf("False positive rate too high: %.2f%% (expected <2%%)", fpr*100)
	}
	t.Logf("Actual false positive rate: %.2f%%", fpr*100)
}
