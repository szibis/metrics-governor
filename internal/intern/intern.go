// Package intern provides string interning to reduce memory allocations
// and GC pressure for frequently repeated strings like metric names and labels.
//
// This implementation is inspired by concepts described in VictoriaMetrics blog articles
// on TSDB optimization techniques (https://valyala.medium.com/). The code itself is an
// original implementation using standard Go patterns (sync.Map, unsafe.String).
package intern

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

// Pool provides string interning with a concurrent-safe map.
// It deduplicates repeated strings to reduce memory allocations and GC pressure.
type Pool struct {
	strings sync.Map
	hits    atomic.Uint64
	misses  atomic.Uint64
}

// NewPool creates a new intern pool.
func NewPool() *Pool {
	return &Pool{}
}

// Intern returns an interned copy of s.
// If s was previously interned, the same string pointer is returned.
// This reduces memory usage when the same strings appear frequently.
func (p *Pool) Intern(s string) string {
	if s == "" {
		return s
	}

	// Fast path: check if already interned
	if interned, ok := p.strings.Load(s); ok {
		p.hits.Add(1)
		return interned.(string)
	}

	// Clone string to avoid holding reference to larger buffer
	// This is important when s is a slice of a larger string
	clone := cloneString(s)

	// LoadOrStore handles the race where another goroutine may have
	// stored the same string concurrently
	actual, loaded := p.strings.LoadOrStore(clone, clone)
	if loaded {
		p.hits.Add(1)
	} else {
		p.misses.Add(1)
	}
	return actual.(string)
}

// InternBytes interns a string from a byte slice without extra allocation
// when the string is already present in the pool.
func (p *Pool) InternBytes(b []byte) string {
	if len(b) == 0 {
		return ""
	}

	// Use unsafe conversion for lookup only (doesn't escape)
	s := unsafeString(b)

	// Fast path: check if already interned
	if interned, ok := p.strings.Load(s); ok {
		p.hits.Add(1)
		return interned.(string)
	}

	// Must create a proper string copy for storage
	clone := string(b)
	actual, loaded := p.strings.LoadOrStore(clone, clone)
	if loaded {
		p.hits.Add(1)
	} else {
		p.misses.Add(1)
	}
	return actual.(string)
}

// Stats returns hit/miss statistics for monitoring cache effectiveness.
func (p *Pool) Stats() (hits, misses uint64) {
	return p.hits.Load(), p.misses.Load()
}

// HitRate returns the cache hit rate as a percentage (0.0 to 1.0).
// Returns 0 if no lookups have been performed.
func (p *Pool) HitRate() float64 {
	hits := p.hits.Load()
	misses := p.misses.Load()
	total := hits + misses
	if total == 0 {
		return 0
	}
	return float64(hits) / float64(total)
}

// Size returns the approximate number of interned strings.
func (p *Pool) Size() int {
	size := 0
	p.strings.Range(func(_, _ interface{}) bool {
		size++
		return true
	})
	return size
}

// Reset clears all interned strings and resets statistics.
// This should only be called when the pool is not in use.
func (p *Pool) Reset() {
	p.strings = sync.Map{}
	p.hits.Store(0)
	p.misses.Store(0)
}

// cloneString creates a new string allocation to avoid holding
// a reference to a potentially larger underlying buffer.
func cloneString(s string) string {
	return string([]byte(s))
}

// unsafeString converts a byte slice to a string without allocation.
// This is safe for read-only operations like map lookups.
// The returned string must not be stored or modified.
func unsafeString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	// This is safe because:
	// 1. We only use it for map lookups (read-only)
	// 2. The byte slice is not modified during the lookup
	// 3. The returned string is not stored
	return unsafe.String(unsafe.SliceData(b), len(b))
}

// Global pool for common label names that are known at compile time.
var commonLabels = NewPool()

// init pre-populates common Prometheus/metrics label names.
func init() {
	// Pre-intern common Prometheus label names
	commonNames := []string{
		"__name__",
		"job",
		"instance",
		"le",
		"quantile",
		"service",
		"env",
		"environment",
		"cluster",
		"namespace",
		"pod",
		"container",
		"node",
		"host",
		"region",
		"zone",
		"dc",
		"datacenter",
		"method",
		"path",
		"status",
		"status_code",
		"code",
		"type",
		"name",
		"id",
		"version",
		"app",
		"application",
	}
	for _, name := range commonNames {
		commonLabels.Intern(name)
	}
}

// CommonLabels returns the global pool for common label names.
// This pool is pre-populated with frequently used Prometheus label names.
func CommonLabels() *Pool {
	return commonLabels
}
