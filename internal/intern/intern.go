// Package intern provides string interning to reduce memory allocations
// by deduplicating repeated strings.
package intern

import (
	"sync"
	"sync/atomic"
	"unsafe"
)

// Pool provides string interning with periodic cleanup.
// It uses sync.Map for lock-free concurrent reads.
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
// If s was seen before, returns the existing copy.
// Otherwise stores and returns a new copy.
func (p *Pool) Intern(s string) string {
	if interned, ok := p.strings.Load(s); ok {
		p.hits.Add(1)
		return interned.(string)
	}

	// Clone string to avoid holding reference to larger buffer
	clone := cloneString(s)
	actual, loaded := p.strings.LoadOrStore(clone, clone)
	if loaded {
		p.hits.Add(1)
	} else {
		p.misses.Add(1)
	}
	return actual.(string)
}

// InternBytes interns a string from a byte slice without allocating
// an intermediate string for the lookup.
func (p *Pool) InternBytes(b []byte) string {
	// Fast path: check if already interned
	s := unsafeString(b)
	if interned, ok := p.strings.Load(s); ok {
		p.hits.Add(1)
		return interned.(string)
	}

	// Slow path: need to allocate
	clone := string(b)
	actual, loaded := p.strings.LoadOrStore(clone, clone)
	if loaded {
		p.hits.Add(1)
	} else {
		p.misses.Add(1)
	}
	return actual.(string)
}

// Stats returns hit/miss statistics.
func (p *Pool) Stats() (hits, misses uint64) {
	return p.hits.Load(), p.misses.Load()
}

// Size returns the number of interned strings.
func (p *Pool) Size() int {
	count := 0
	p.strings.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

// Reset clears all interned strings and resets statistics.
func (p *Pool) Reset() {
	p.strings = sync.Map{}
	p.hits.Store(0)
	p.misses.Store(0)
}

// cloneString creates a new string allocation to avoid
// keeping references to larger underlying buffers.
func cloneString(s string) string {
	return string([]byte(s))
}

// unsafeString converts a byte slice to a string without allocation.
// The returned string is only valid while the byte slice is not modified.
func unsafeString(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	// This is safe because we only use it for map lookup,
	// never storing the result.
	return unsafe.String(unsafe.SliceData(b), len(b))
}

// Global pools for common use cases
var (
	// LabelNames is a pool for metric label names (small fixed set).
	LabelNames = NewPool()

	// MetricNames is a pool for metric names.
	MetricNames = NewPool()

	// commonLabelsPool is initialized once with common PRW and OTLP label names.
	commonLabelsPool *Pool
	commonLabelsOnce sync.Once
)

// CommonLabels returns a shared pool pre-populated with common PRW and OTLP label names.
// This provides high hit rates for typical metrics workloads.
func CommonLabels() *Pool {
	commonLabelsOnce.Do(func() {
		commonLabelsPool = NewPool()

		// Pre-populate with common PRW labels
		prwLabels := []string{
			"__name__", "job", "instance", "le", "quantile",
			"service", "env", "cluster", "namespace", "pod",
			"container", "node", "host", "region", "method",
			"endpoint", "status", "code", "version", "name",
		}

		// Pre-populate with common OTLP semantic convention attributes
		otlpLabels := []string{
			// Resource attributes
			"service.name", "service.namespace", "service.version", "service.instance.id",
			"deployment.environment",
			"k8s.pod.name", "k8s.namespace.name", "k8s.container.name", "k8s.node.name",
			"k8s.deployment.name", "k8s.cluster.name",
			"host.name", "host.id", "host.type", "host.arch",
			"cloud.provider", "cloud.region", "cloud.availability_zone", "cloud.account.id",
			"container.id", "container.name", "container.image.name",
			"process.pid", "process.executable.name",
			"telemetry.sdk.name", "telemetry.sdk.version", "telemetry.sdk.language",
			// Span/Metric attributes
			"http.method", "http.status_code", "http.route", "http.scheme", "http.url",
			"http.request.method", "http.response.status_code",
			"url.scheme", "url.path", "url.full",
			"server.address", "server.port", "client.address",
			"db.system", "db.name", "db.operation", "db.statement",
			"rpc.system", "rpc.service", "rpc.method", "rpc.grpc.status_code",
			"messaging.system", "messaging.destination", "messaging.operation",
			"net.peer.name", "net.peer.port", "net.host.name",
			"error.type", "exception.type", "exception.message",
			"otel.status_code", "otel.library.name",
		}

		// Pre-intern all labels
		for _, label := range prwLabels {
			commonLabelsPool.Intern(label)
		}
		for _, label := range otlpLabels {
			commonLabelsPool.Intern(label)
		}
	})
	return commonLabelsPool
}
