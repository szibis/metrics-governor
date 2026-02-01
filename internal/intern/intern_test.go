package intern

import (
	"sync"
	"testing"
)

func TestPoolIntern(t *testing.T) {
	p := NewPool()

	// First intern should create new string
	s1 := p.Intern("hello")
	if s1 != "hello" {
		t.Errorf("expected 'hello', got %q", s1)
	}

	// Second intern of same string should return same pointer
	s2 := p.Intern("hello")
	if s1 != s2 {
		t.Error("expected same string pointer for interned strings")
	}

	// Stats should show 1 miss, 1 hit
	hits, misses := p.Stats()
	if hits != 1 || misses != 1 {
		t.Errorf("expected 1 hit, 1 miss; got %d hits, %d misses", hits, misses)
	}
}

func TestPoolInternBytes(t *testing.T) {
	p := NewPool()

	b := []byte("world")
	s1 := p.InternBytes(b)
	if s1 != "world" {
		t.Errorf("expected 'world', got %q", s1)
	}

	// Second intern should hit cache
	s2 := p.InternBytes(b)
	if s1 != s2 {
		t.Error("expected same string pointer")
	}

	// Modifying original byte slice should not affect interned string
	b[0] = 'W'
	if s1 != "world" {
		t.Errorf("interned string was modified: %q", s1)
	}
}

func TestPoolSize(t *testing.T) {
	p := NewPool()

	if p.Size() != 0 {
		t.Errorf("expected empty pool, got size %d", p.Size())
	}

	p.Intern("a")
	p.Intern("b")
	p.Intern("c")
	p.Intern("a") // duplicate

	if p.Size() != 3 {
		t.Errorf("expected size 3, got %d", p.Size())
	}
}

func TestPoolReset(t *testing.T) {
	p := NewPool()

	p.Intern("test")
	p.Intern("test")

	hits, misses := p.Stats()
	if hits != 1 || misses != 1 {
		t.Errorf("expected 1 hit, 1 miss before reset; got %d/%d", hits, misses)
	}

	p.Reset()

	if p.Size() != 0 {
		t.Errorf("expected empty pool after reset, got %d", p.Size())
	}

	hits, misses = p.Stats()
	if hits != 0 || misses != 0 {
		t.Errorf("expected 0 hits/misses after reset; got %d/%d", hits, misses)
	}
}

func TestPoolConcurrent(t *testing.T) {
	p := NewPool()
	const goroutines = 100
	const iterations = 1000

	strings := []string{"foo", "bar", "baz", "qux", "hello", "world"}

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				s := strings[(id+j)%len(strings)]
				interned := p.Intern(s)
				if interned != s {
					t.Errorf("interned string mismatch: got %q, want %q", interned, s)
				}
			}
		}(i)
	}

	wg.Wait()

	// Should only have 6 unique strings
	if p.Size() != len(strings) {
		t.Errorf("expected %d unique strings, got %d", len(strings), p.Size())
	}
}

func TestGlobalPools(t *testing.T) {
	// Test that global pools work
	LabelNames.Reset()
	MetricNames.Reset()

	s1 := LabelNames.Intern("__name__")
	s2 := LabelNames.Intern("__name__")

	if s1 != s2 {
		t.Error("global LabelNames pool not working")
	}

	m1 := MetricNames.Intern("http_requests_total")
	m2 := MetricNames.Intern("http_requests_total")

	if m1 != m2 {
		t.Error("global MetricNames pool not working")
	}
}

func BenchmarkPoolIntern(b *testing.B) {
	p := NewPool()
	strings := []string{
		"__name__", "job", "instance", "env", "cluster",
		"service", "version", "pod", "namespace", "container",
	}

	// Pre-populate
	for _, s := range strings {
		p.Intern(s)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Intern(strings[i%len(strings)])
	}
}

func BenchmarkPoolInternBytes(b *testing.B) {
	p := NewPool()
	bytes := [][]byte{
		[]byte("__name__"), []byte("job"), []byte("instance"),
		[]byte("env"), []byte("cluster"), []byte("service"),
	}

	// Pre-populate
	for _, bb := range bytes {
		p.InternBytes(bb)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.InternBytes(bytes[i%len(bytes)])
	}
}

func BenchmarkPoolInternMiss(b *testing.B) {
	p := NewPool()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Each iteration creates a new unique string
		p.Intern(string(rune(i)))
	}
}

func BenchmarkWithoutIntern(b *testing.B) {
	strings := []string{
		"__name__", "job", "instance", "env", "cluster",
		"service", "version", "pod", "namespace", "container",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Without interning, just copy the string
		s := strings[i%len(strings)]
		_ = string([]byte(s))
	}
}
