package buffer

import (
	"fmt"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

var (
	memqueueSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_memqueue_size",
		Help: "Current number of entries in the in-memory failover queue",
	})

	memqueueBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_memqueue_bytes",
		Help: "Current total bytes in the in-memory failover queue",
	})

	memqueueEvictionsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_memqueue_evictions_total",
		Help: "Total entries evicted from the in-memory failover queue when full",
	})
)

func init() {
	prometheus.MustRegister(memqueueSize)
	prometheus.MustRegister(memqueueBytes)
	prometheus.MustRegister(memqueueEvictionsTotal)

	memqueueSize.Set(0)
	memqueueBytes.Set(0)
	memqueueEvictionsTotal.Add(0)
}

// MemoryQueue is a bounded in-memory queue for failover.
// Bounded by both object count and total byte size.
// When full, oldest entries are evicted to make room.
type MemoryQueue struct {
	mu       sync.Mutex
	entries  []*colmetricspb.ExportMetricsServiceRequest
	bytes    int64
	maxSize  int
	maxBytes int64
}

// NewMemoryQueue creates a new bounded in-memory failover queue.
func NewMemoryQueue(maxSize int, maxBytes int64) *MemoryQueue {
	if maxSize <= 0 {
		maxSize = 1000
	}
	if maxBytes <= 0 {
		maxBytes = 256 * 1024 * 1024 // 256MB
	}
	return &MemoryQueue{
		entries:  make([]*colmetricspb.ExportMetricsServiceRequest, 0),
		maxSize:  maxSize,
		maxBytes: maxBytes,
	}
}

// Push adds an entry to the queue. If the queue is full (by count or bytes),
// oldest entries are evicted to make room. Returns error only if the single
// entry exceeds maxBytes.
func (q *MemoryQueue) Push(req *colmetricspb.ExportMetricsServiceRequest) error {
	entrySize := int64(proto.Size(req))

	if entrySize > q.maxBytes {
		return fmt.Errorf("entry size %d exceeds max queue bytes %d", entrySize, q.maxBytes)
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	// Evict oldest entries to make room by count
	for len(q.entries) >= q.maxSize {
		q.evictOldest()
	}

	// Evict oldest entries to make room by bytes
	for q.bytes+entrySize > q.maxBytes && len(q.entries) > 0 {
		q.evictOldest()
	}

	q.entries = append(q.entries, req)
	q.bytes += entrySize

	memqueueSize.Set(float64(len(q.entries)))
	memqueueBytes.Set(float64(q.bytes))

	return nil
}

// Pop removes and returns the oldest entry from the queue.
// Returns nil if the queue is empty.
func (q *MemoryQueue) Pop() *colmetricspb.ExportMetricsServiceRequest {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.entries) == 0 {
		return nil
	}

	entry := q.entries[0]
	q.entries[0] = nil // allow GC to collect the entry
	q.entries = q.entries[1:]
	q.bytes -= int64(proto.Size(entry))
	if q.bytes < 0 {
		q.bytes = 0
	}

	// Compact the slice to prevent unbounded capacity growth
	q.maybeCompact()

	memqueueSize.Set(float64(len(q.entries)))
	memqueueBytes.Set(float64(q.bytes))

	return entry
}

// Len returns the current number of entries in the queue.
func (q *MemoryQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.entries)
}

// Size returns the current total bytes in the queue.
func (q *MemoryQueue) Size() int64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.bytes
}

// evictOldest removes the oldest entry. Must be called with q.mu held.
func (q *MemoryQueue) evictOldest() {
	if len(q.entries) == 0 {
		return
	}
	evicted := q.entries[0]
	q.entries[0] = nil // allow GC to collect the entry
	q.entries = q.entries[1:]
	q.bytes -= int64(proto.Size(evicted))
	if q.bytes < 0 {
		q.bytes = 0
	}
	memqueueEvictionsTotal.Inc()
	q.maybeCompact()
}

// maybeCompact compacts the slice if capacity is significantly larger than length.
// Must be called with q.mu held.
func (q *MemoryQueue) maybeCompact() {
	if cap(q.entries) > 256 && cap(q.entries) > len(q.entries)+64 {
		compacted := make([]*colmetricspb.ExportMetricsServiceRequest, len(q.entries))
		copy(compacted, q.entries)
		q.entries = compacted
	}
}
