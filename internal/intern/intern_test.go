package intern

import (
	"fmt"
	"sync"
	"testing"
)

func TestPool_Intern(t *testing.T) {
	p := NewPool()

	// Test basic interning
	s1 := p.Intern("test")
	s2 := p.Intern("test")

	if s1 != s2 {
		t.Error("expected same string reference")
	}

	// Verify stats
	hits, misses := p.Stats()
	if hits != 1 || misses != 1 {
		t.Errorf("expected 1 hit and 1 miss, got %d hits and %d misses", hits, misses)
	}
}

func TestPool_InternEmpty(t *testing.T) {
	p := NewPool()

	s := p.Intern("")
	if s != "" {
		t.Error("expected empty string")
	}

	// Empty string should not affect stats
	hits, misses := p.Stats()
	if hits != 0 || misses != 0 {
		t.Errorf("expected 0 hits and 0 misses for empty string, got %d and %d", hits, misses)
	}
}

func TestPool_InternBytes(t *testing.T) {
	p := NewPool()

	// First intern using string
	s1 := p.Intern("hello")

	// Then lookup using bytes - should hit cache
	b := []byte("hello")
	s2 := p.InternBytes(b)

	if s1 != s2 {
		t.Error("expected same string reference from bytes")
	}

	hits, _ := p.Stats()
	if hits != 1 {
		t.Errorf("expected 1 hit, got %d", hits)
	}
}

func TestPool_InternBytesEmpty(t *testing.T) {
	p := NewPool()

	s := p.InternBytes(nil)
	if s != "" {
		t.Error("expected empty string for nil bytes")
	}

	s = p.InternBytes([]byte{})
	if s != "" {
		t.Error("expected empty string for empty bytes")
	}
}

func TestPool_Clone(t *testing.T) {
	p := NewPool()

	// Create a large buffer and slice it
	large := make([]byte, 1000)
	copy(large[500:], "small")

	// Intern the slice - should clone to avoid retaining large buffer
	s := p.InternBytes(large[500:505])
	if s != "small" {
		t.Errorf("expected 'small', got %q", s)
	}
}

func TestPool_Size(t *testing.T) {
	p := NewPool()

	if p.Size() != 0 {
		t.Error("expected empty pool")
	}

	p.Intern("one")
	p.Intern("two")
	p.Intern("three")
	p.Intern("one") // duplicate

	if p.Size() != 3 {
		t.Errorf("expected size 3, got %d", p.Size())
	}
}

func TestPool_HitRate(t *testing.T) {
	p := NewPool()

	// No lookups yet
	if p.HitRate() != 0 {
		t.Error("expected 0 hit rate initially")
	}

	// One miss, one hit
	p.Intern("test")
	p.Intern("test")

	rate := p.HitRate()
	expected := 0.5
	if rate != expected {
		t.Errorf("expected hit rate %f, got %f", expected, rate)
	}
}

func TestPool_Reset(t *testing.T) {
	p := NewPool()

	p.Intern("one")
	p.Intern("two")
	p.Intern("one")

	p.Reset()

	if p.Size() != 0 {
		t.Error("expected empty pool after reset")
	}

	hits, misses := p.Stats()
	if hits != 0 || misses != 0 {
		t.Error("expected stats reset")
	}
}

func TestPool_Concurrent(t *testing.T) {
	p := NewPool()
	const goroutines = 100
	const iterations = 1000

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				// Each goroutine interns some shared and some unique strings
				key := fmt.Sprintf("shared_%d", i%10)
				p.Intern(key)

				if i%100 == 0 {
					unique := fmt.Sprintf("unique_%d_%d", id, i)
					p.Intern(unique)
				}
			}
		}(g)
	}

	wg.Wait()

	// Verify pool is consistent
	size := p.Size()
	if size == 0 {
		t.Error("expected non-empty pool")
	}

	// Verify stats are consistent
	hits, misses := p.Stats()
	total := hits + misses
	expected := uint64(goroutines * iterations)
	// We also have unique strings
	expectedUnique := uint64(goroutines * (iterations / 100))
	if total < expected {
		t.Errorf("expected at least %d lookups, got %d", expected, total)
	}
	t.Logf("Concurrent test: size=%d, hits=%d, misses=%d, unique_expected=%d", size, hits, misses, expectedUnique)
}

func TestCommonLabels(t *testing.T) {
	p := CommonLabels()

	// Common labels should already be interned
	name := p.Intern("__name__")
	if name != "__name__" {
		t.Error("expected __name__ to be interned")
	}

	// First lookup after init should be a hit
	hits1, _ := p.Stats()

	// Second lookup should also be a hit
	p.Intern("__name__")
	hits2, _ := p.Stats()

	if hits2 <= hits1 {
		t.Error("expected hit count to increase")
	}
}

// Benchmarks

func BenchmarkPool_Intern_Miss(b *testing.B) {
	p := NewPool()
	strings := make([]string, b.N)
	for i := range strings {
		strings[i] = fmt.Sprintf("metric_%d", i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Intern(strings[i])
	}
}

func BenchmarkPool_Intern_Hit(b *testing.B) {
	p := NewPool()
	// Pre-populate with strings
	for i := 0; i < 100; i++ {
		p.Intern(fmt.Sprintf("metric_%d", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Intern(fmt.Sprintf("metric_%d", i%100))
	}
}

func BenchmarkPool_Intern_Parallel(b *testing.B) {
	p := NewPool()
	// Pre-populate with strings
	for i := 0; i < 100; i++ {
		p.Intern(fmt.Sprintf("metric_%d", i))
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			p.Intern(fmt.Sprintf("metric_%d", i%100))
			i++
		}
	})
}

func BenchmarkPool_InternBytes_Hit(b *testing.B) {
	p := NewPool()
	// Pre-populate
	for i := 0; i < 100; i++ {
		p.Intern(fmt.Sprintf("label_%d", i))
	}

	// Prepare byte slices
	bytes := make([][]byte, 100)
	for i := range bytes {
		bytes[i] = []byte(fmt.Sprintf("label_%d", i))
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.InternBytes(bytes[i%100])
	}
}

func BenchmarkNoIntern_StringAlloc(b *testing.B) {
	// Baseline: normal string creation without interning
	bytes := make([][]byte, 100)
	for i := range bytes {
		bytes[i] = []byte(fmt.Sprintf("label_%d", i))
	}

	var sink string
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sink = string(bytes[i%100])
	}
	_ = sink
}

func BenchmarkCommonLabels_Lookup(b *testing.B) {
	p := CommonLabels()
	labels := []string{"__name__", "job", "instance", "service", "env"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Intern(labels[i%len(labels)])
	}
}
