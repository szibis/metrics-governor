package queue

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// ExportBatch is a zero-serialization batch that flows through the memory queue.
// Instead of marshaling to bytes, it passes typed protobuf structs via a Go channel.
type ExportBatch struct {
	ResourceMetrics []*metricspb.ResourceMetrics
	Timestamp       time.Time
	EstimatedBytes  int

	// Lazy-initialized request wrapper — avoids allocation until needed.
	request *colmetricspb.ExportMetricsServiceRequest
}

// ToRequest returns the batch as an ExportMetricsServiceRequest.
// The request is created lazily and cached for reuse.
func (b *ExportBatch) ToRequest() *colmetricspb.ExportMetricsServiceRequest {
	if b.request == nil {
		b.request = &colmetricspb.ExportMetricsServiceRequest{
			ResourceMetrics: b.ResourceMetrics,
		}
	}
	return b.request
}

// NewExportBatch creates a new ExportBatch from a request.
// EstimatedBytes is computed via proto.Size() once at push time.
func NewExportBatch(req *colmetricspb.ExportMetricsServiceRequest) *ExportBatch {
	return &ExportBatch{
		ResourceMetrics: req.ResourceMetrics,
		Timestamp:       time.Now(),
		EstimatedBytes:  proto.Size(req),
		request:         req,
	}
}

// MemoryBatchQueueConfig configures the in-memory batch queue.
type MemoryBatchQueueConfig struct {
	// MaxSize is the maximum number of batches the channel can hold.
	MaxSize int

	// MaxBytes is the maximum total estimated bytes across all batches.
	// 0 means no byte limit (only count limit applies).
	MaxBytes int64

	// FullBehavior defines what happens when the queue is full.
	FullBehavior FullBehavior

	// BlockTimeout is the max time to wait when FullBehavior is "block".
	BlockTimeout time.Duration
}

// Errors returned by MemoryBatchQueue operations.
var (
	ErrBatchQueueClosed = errors.New("batch queue is closed")
	ErrBatchQueueFull   = errors.New("batch queue is full")
)

// MemoryBatchQueue is a zero-serialization in-memory queue that passes typed
// ExportBatch structs through a buffered Go channel. This eliminates the
// proto.Marshal/Unmarshal cost (~250µs/batch) of the disk-based SendQueue.
type MemoryBatchQueue struct {
	ch     chan *ExportBatch
	config MemoryBatchQueueConfig

	activeCount atomic.Int64 // current batch count
	activeBytes atomic.Int64 // current estimated memory usage

	totalPushed  atomic.Int64 // lifetime batches pushed
	totalPopped  atomic.Int64 // lifetime batches popped
	totalDropped atomic.Int64 // lifetime batches dropped
	totalBytes   atomic.Int64 // lifetime bytes pushed

	mu     sync.Mutex
	closed bool

	// For "block" full behavior
	spaceCond *sync.Cond
}

// Prometheus metrics for memory batch queue.
var (
	memBatchQueueSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_memory_batches",
		Help: "Current number of batches in the memory batch queue",
	})

	memBatchQueueBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_memory_bytes",
		Help: "Current estimated memory usage of the memory batch queue in bytes",
	})

	memBatchQueueUtilization = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_memory_utilization",
		Help: "Memory batch queue utilization as a ratio of max bytes (0.0-1.0)",
	})

	memBatchQueueDroppedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_queue_memory_dropped_total",
		Help: "Total batches dropped from the memory batch queue",
	}, []string{"reason"})

	memBatchQueueSpilloverTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_queue_spillover_total",
		Help: "Total batches spilled from memory to disk in hybrid mode",
	})
)

func init() {
	prometheus.MustRegister(
		memBatchQueueSize,
		memBatchQueueBytes,
		memBatchQueueUtilization,
		memBatchQueueDroppedTotal,
		memBatchQueueSpilloverTotal,
	)
}

// NewMemoryBatchQueue creates a new zero-serialization in-memory batch queue.
func NewMemoryBatchQueue(cfg MemoryBatchQueueConfig) *MemoryBatchQueue {
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = 1024
	}
	if cfg.FullBehavior == "" {
		cfg.FullBehavior = DropOldest
	}
	if cfg.BlockTimeout <= 0 {
		cfg.BlockTimeout = 30 * time.Second
	}

	q := &MemoryBatchQueue{
		ch:     make(chan *ExportBatch, cfg.MaxSize),
		config: cfg,
	}
	q.spaceCond = sync.NewCond(&q.mu)
	return q
}

// PushBatch adds a typed batch to the queue without any serialization.
// Returns nil on success, ErrBatchQueueClosed if closed, ErrBatchQueueFull
// if the queue is full and behavior is drop_newest.
func (q *MemoryBatchQueue) PushBatch(batch *ExportBatch) error {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return ErrBatchQueueClosed
	}

	// Check byte limit
	if q.config.MaxBytes > 0 && q.activeBytes.Load()+int64(batch.EstimatedBytes) > q.config.MaxBytes {
		return q.handleFullLocked(batch, "bytes_limit")
	}

	// Try non-blocking send
	select {
	case q.ch <- batch:
		q.mu.Unlock()
		q.activeCount.Add(1)
		q.activeBytes.Add(int64(batch.EstimatedBytes))
		q.totalPushed.Add(1)
		q.totalBytes.Add(int64(batch.EstimatedBytes))
		q.updateMetrics()
		return nil
	default:
		// Channel full
		return q.handleFullLocked(batch, "channel_full")
	}
}

// handleFullLocked handles a full queue condition. Caller must hold q.mu.
func (q *MemoryBatchQueue) handleFullLocked(batch *ExportBatch, reason string) error {
	switch q.config.FullBehavior {
	case DropOldest:
		// Drop the oldest entry to make room
		select {
		case dropped := <-q.ch:
			q.activeCount.Add(-1)
			q.activeBytes.Add(-int64(dropped.EstimatedBytes))
			q.totalDropped.Add(1)
			memBatchQueueDroppedTotal.WithLabelValues("oldest").Inc()
		default:
			// Channel was already drained by a consumer
		}
		// Now try pushing again
		select {
		case q.ch <- batch:
			q.mu.Unlock()
			q.activeCount.Add(1)
			q.activeBytes.Add(int64(batch.EstimatedBytes))
			q.totalPushed.Add(1)
			q.totalBytes.Add(int64(batch.EstimatedBytes))
			q.updateMetrics()
			return nil
		default:
			q.mu.Unlock()
			q.totalDropped.Add(1)
			memBatchQueueDroppedTotal.WithLabelValues(reason).Inc()
			return ErrBatchQueueFull
		}

	case DropNewest:
		q.mu.Unlock()
		q.totalDropped.Add(1)
		memBatchQueueDroppedTotal.WithLabelValues("newest").Inc()
		return ErrBatchQueueFull

	case Block:
		// Release lock and use a timer-based approach since sync.Cond.Wait
		// doesn't support timeouts.
		q.mu.Unlock()
		timer := time.NewTimer(q.config.BlockTimeout)
		defer timer.Stop()
		// Spin with short sleeps checking for space
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case q.ch <- batch:
				q.activeCount.Add(1)
				q.activeBytes.Add(int64(batch.EstimatedBytes))
				q.totalPushed.Add(1)
				q.totalBytes.Add(int64(batch.EstimatedBytes))
				q.updateMetrics()
				return nil
			case <-timer.C:
				q.totalDropped.Add(1)
				memBatchQueueDroppedTotal.WithLabelValues("block_timeout").Inc()
				return ErrBatchQueueFull
			case <-tick.C:
				// Check closed state
				q.mu.Lock()
				closed := q.closed
				q.mu.Unlock()
				if closed {
					return ErrBatchQueueClosed
				}
			}
		}

	default:
		q.mu.Unlock()
		q.totalDropped.Add(1)
		memBatchQueueDroppedTotal.WithLabelValues(reason).Inc()
		return ErrBatchQueueFull
	}
}

// PopBatch retrieves the next batch from the queue (non-blocking).
// Returns nil, nil if the queue is empty.
func (q *MemoryBatchQueue) PopBatch() (*ExportBatch, error) {
	select {
	case batch, ok := <-q.ch:
		if !ok {
			return nil, ErrBatchQueueClosed
		}
		q.activeCount.Add(-1)
		q.activeBytes.Add(-int64(batch.EstimatedBytes))
		q.totalPopped.Add(1)

		// Signal any blocked pushers
		q.spaceCond.Signal()

		q.updateMetrics()
		return batch, nil
	default:
		return nil, nil
	}
}

// PopBatchBlocking retrieves the next batch, blocking until one is available
// or the provided done channel is closed.
func (q *MemoryBatchQueue) PopBatchBlocking(done <-chan struct{}) (*ExportBatch, error) {
	select {
	case batch, ok := <-q.ch:
		if !ok {
			return nil, ErrBatchQueueClosed
		}
		q.activeCount.Add(-1)
		q.activeBytes.Add(-int64(batch.EstimatedBytes))
		q.totalPopped.Add(1)

		q.spaceCond.Signal()
		q.updateMetrics()
		return batch, nil
	case <-done:
		return nil, nil
	}
}

// Close closes the queue. After close, PushBatch returns ErrBatchQueueClosed.
// PopBatch can still drain remaining entries.
func (q *MemoryBatchQueue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	close(q.ch)
	q.spaceCond.Broadcast()
}

// Len returns the current number of batches in the queue.
func (q *MemoryBatchQueue) Len() int {
	return int(q.activeCount.Load())
}

// Size returns the current estimated bytes in the queue.
func (q *MemoryBatchQueue) Size() int64 {
	return q.activeBytes.Load()
}

// IsFull returns true if the queue has reached its capacity (by count or bytes).
func (q *MemoryBatchQueue) IsFull() bool {
	if int(q.activeCount.Load()) >= q.config.MaxSize {
		return true
	}
	if q.config.MaxBytes > 0 && q.activeBytes.Load() >= q.config.MaxBytes {
		return true
	}
	return false
}

// Utilization returns the queue utilization as a ratio (0.0-1.0).
// Uses the higher of count-based or byte-based utilization.
func (q *MemoryBatchQueue) Utilization() float64 {
	countUtil := float64(q.activeCount.Load()) / float64(q.config.MaxSize)
	if q.config.MaxBytes > 0 {
		byteUtil := float64(q.activeBytes.Load()) / float64(q.config.MaxBytes)
		if byteUtil > countUtil {
			return byteUtil
		}
	}
	return countUtil
}

func (q *MemoryBatchQueue) updateMetrics() {
	memBatchQueueSize.Set(float64(q.activeCount.Load()))
	memBatchQueueBytes.Set(float64(q.activeBytes.Load()))
	if q.config.MaxBytes > 0 {
		memBatchQueueUtilization.Set(float64(q.activeBytes.Load()) / float64(q.config.MaxBytes))
	} else {
		memBatchQueueUtilization.Set(float64(q.activeCount.Load()) / float64(q.config.MaxSize))
	}
}

// IncrementSpilloverTotal increments the spillover counter for hybrid mode.
func IncrementSpilloverTotal() {
	memBatchQueueSpilloverTotal.Inc()
}

// QueueMode defines the queue operating mode.
type QueueMode string

const (
	// QueueModeMemory uses a zero-serialization in-memory channel.
	// Highest throughput, no persistence, data lost on crash.
	QueueModeMemory QueueMode = "memory"

	// QueueModeDisk uses the existing FastQueue with proto serialization.
	// Lower throughput but full crash recovery.
	QueueModeDisk QueueMode = "disk"

	// QueueModeHybrid uses memory as L1 with disk spillover at L2.
	// Fast primary path with safety net for traffic spikes.
	QueueModeHybrid QueueMode = "hybrid"
)

// ValidQueueModes returns the set of valid queue modes.
func ValidQueueModes() []QueueMode {
	return []QueueMode{QueueModeMemory, QueueModeDisk, QueueModeHybrid}
}

// IsValidQueueMode reports whether mode is a recognized queue mode.
func IsValidQueueMode(mode string) bool {
	for _, m := range ValidQueueModes() {
		if string(m) == mode {
			return true
		}
	}
	return false
}

// String returns a human-readable description of the queue mode.
func (m QueueMode) String() string {
	switch m {
	case QueueModeMemory:
		return "memory (zero-serialization, no persistence)"
	case QueueModeDisk:
		return "disk (serialized, full persistence)"
	case QueueModeHybrid:
		return "hybrid (memory L1 + disk L2 spillover)"
	default:
		return string(m)
	}
}
