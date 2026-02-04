package queue

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// FullBehavior defines what happens when the queue is full.
type FullBehavior string

const (
	// DropOldest drops the oldest entry when queue is full.
	DropOldest FullBehavior = "drop_oldest"
	// DropNewest drops the newest (incoming) entry when queue is full.
	DropNewest FullBehavior = "drop_newest"
	// Block blocks until space is available (with timeout).
	Block FullBehavior = "block"
)

// Config holds the queue configuration.
type Config struct {
	// Path is the directory for queue files.
	Path string
	// MaxSize is the maximum number of batches in queue.
	MaxSize int
	// MaxBytes is the maximum total size in bytes (0 = no limit).
	MaxBytes int64
	// RetryInterval is the initial retry interval.
	RetryInterval time.Duration
	// MaxRetryDelay is the maximum backoff delay.
	MaxRetryDelay time.Duration
	// FullBehavior defines behavior when queue is full.
	FullBehavior FullBehavior
	// BlockTimeout is the timeout for Block behavior (default: 30s).
	BlockTimeout time.Duration
	// TargetUtilization is the target disk utilization (default: 0.85).
	TargetUtilization float64
	// AdaptiveEnabled enables adaptive queue sizing based on disk space.
	AdaptiveEnabled bool
	// CompactThreshold is the ratio of consumed entries before compaction (default: 0.5).
	CompactThreshold float64

	// Backoff settings
	// BackoffEnabled enables exponential backoff for retries (default: true).
	BackoffEnabled bool
	// BackoffMultiplier is the factor to multiply delay by on each failure (default: 2.0).
	BackoffMultiplier float64

	// Circuit breaker settings
	// CircuitBreakerEnabled enables the circuit breaker pattern (default: true).
	CircuitBreakerEnabled bool
	// CircuitBreakerThreshold is the number of consecutive failures before opening (default: 10).
	CircuitBreakerThreshold int
	// CircuitBreakerResetTimeout is time to wait before half-open state (default: 30s).
	CircuitBreakerResetTimeout time.Duration

	// FastQueue settings
	// InmemoryBlocks is the in-memory channel size (default: 256).
	InmemoryBlocks int
	// ChunkSize is the chunk file size (default: 512MB).
	ChunkSize int64
	// MetaSyncInterval is the metadata sync interval (default: 1s).
	MetaSyncInterval time.Duration
	// StaleFlushInterval is the stale flush interval (default: 5s).
	StaleFlushInterval time.Duration
}

// DefaultConfig returns a default queue configuration.
func DefaultConfig() Config {
	return Config{
		Path:                       "./queue",
		MaxSize:                    10000,
		MaxBytes:                   1073741824, // 1GB
		RetryInterval:              5 * time.Second,
		MaxRetryDelay:              5 * time.Minute,
		FullBehavior:               DropOldest,
		BlockTimeout:               30 * time.Second,
		TargetUtilization:          0.85,
		AdaptiveEnabled:            true,
		CompactThreshold:           0.5,
		BackoffEnabled:             true,
		BackoffMultiplier:          2.0,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    10,
		CircuitBreakerResetTimeout: 30 * time.Second,
		InmemoryBlocks:             256,
		ChunkSize:                  512 * 1024 * 1024, // 512MB
		MetaSyncInterval:           time.Second,
		StaleFlushInterval:         5 * time.Second,
	}
}

// QueueEntry represents a single queued batch.
type QueueEntry struct {
	// ID is the unique identifier.
	ID string
	// Timestamp is when the entry was created.
	Timestamp time.Time
	// Data is the serialized protobuf.
	Data []byte
	// Retries is the number of retry attempts.
	Retries int
	// Offset is the offset for this entry (for compatibility).
	Offset int64
}

// SendQueue is a FastQueue-based persistent queue for export requests.
type SendQueue struct {
	config Config
	fq     *FastQueue
	mu     sync.RWMutex
	closed bool

	// Retry tracking (in-memory, lost on restart)
	retries   map[string]int
	retriesMu sync.RWMutex

	// For block behavior
	spaceCond *sync.Cond
}

// New creates a new SendQueue.
func New(cfg Config) (*SendQueue, error) {
	// Apply defaults for zero values
	if cfg.Path == "" {
		cfg.Path = "./queue"
	}
	if cfg.MaxSize == 0 {
		cfg.MaxSize = 10000
	}
	if cfg.RetryInterval == 0 {
		cfg.RetryInterval = 5 * time.Second
	}
	if cfg.MaxRetryDelay == 0 {
		cfg.MaxRetryDelay = 5 * time.Minute
	}
	if cfg.FullBehavior == "" {
		cfg.FullBehavior = DropOldest
	}
	if cfg.BlockTimeout == 0 {
		cfg.BlockTimeout = 30 * time.Second
	}
	if cfg.TargetUtilization <= 0 || cfg.TargetUtilization > 1 {
		cfg.TargetUtilization = 0.85
	}
	if cfg.CompactThreshold <= 0 || cfg.CompactThreshold > 1 {
		cfg.CompactThreshold = 0.5
	}
	if cfg.InmemoryBlocks <= 0 {
		cfg.InmemoryBlocks = 256
	}
	if cfg.ChunkSize <= 0 {
		cfg.ChunkSize = 512 * 1024 * 1024
	}
	if cfg.MetaSyncInterval <= 0 {
		cfg.MetaSyncInterval = time.Second
	}
	if cfg.StaleFlushInterval <= 0 {
		cfg.StaleFlushInterval = 5 * time.Second
	}

	// Create FastQueue
	fqCfg := FastQueueConfig{
		Path:               cfg.Path,
		MaxInmemoryBlocks:  cfg.InmemoryBlocks,
		ChunkFileSize:      cfg.ChunkSize,
		MetaSyncInterval:   cfg.MetaSyncInterval,
		StaleFlushInterval: cfg.StaleFlushInterval,
		MaxSize:            cfg.MaxSize,
		MaxBytes:           cfg.MaxBytes,
	}

	fq, err := NewFastQueue(fqCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create FastQueue: %w", err)
	}

	q := &SendQueue{
		config:  cfg,
		fq:      fq,
		retries: make(map[string]int),
	}
	q.spaceCond = sync.NewCond(&q.mu)

	return q, nil
}

// Push adds a new request to the queue.
func (q *SendQueue) Push(req *colmetricspb.ExportMetricsServiceRequest) error {
	data, err := proto.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	return q.pushData(data)
}

// PushData adds pre-serialized data to the queue.
// This allows other protocols (like PRW) to use the same queue infrastructure.
func (q *SendQueue) PushData(data []byte) error {
	return q.pushData(data)
}

// pushData adds serialized data to the queue.
func (q *SendQueue) pushData(data []byte) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return fmt.Errorf("queue is closed")
	}

	// Try to push to FastQueue
	for {
		err := q.fq.Push(data)
		if err == nil {
			q.spaceCond.Broadcast()
			return nil
		}

		// Handle different error cases
		if err == ErrDiskFull {
			// Disk is full - handle according to FullBehavior
			switch q.config.FullBehavior {
			case DropOldest:
				// Try to make room by popping oldest
				if _, dropErr := q.fq.Pop(); dropErr != nil {
					queueDroppedTotal.WithLabelValues("disk_full_drop_failed").Inc()
					return fmt.Errorf("disk full and failed to drop oldest: %w", err)
				}
				queueDroppedTotal.WithLabelValues("disk_full").Inc()
				continue // Retry push

			case DropNewest:
				queueDroppedTotal.WithLabelValues("disk_full_newest").Inc()
				return nil // Drop incoming entry

			case Block:
				// Wait with timeout
				var timedOut atomic.Bool
				timer := time.AfterFunc(q.config.BlockTimeout, func() {
					timedOut.Store(true)
					q.spaceCond.Broadcast()
				})

				for !timedOut.Load() {
					q.spaceCond.Wait()
					if q.closed {
						timer.Stop()
						return fmt.Errorf("queue is closed")
					}
					// Retry push
					if retryErr := q.fq.Push(data); retryErr == nil {
						timer.Stop()
						return nil
					}
				}
				timer.Stop()
				queueDroppedTotal.WithLabelValues("disk_full_timeout").Inc()
				return fmt.Errorf("timeout waiting for disk space: %w", err)
			}
		}

		if err == ErrQueueFull {
			// Queue is full (by count or bytes limit)
			switch q.config.FullBehavior {
			case DropOldest:
				if _, dropErr := q.fq.Pop(); dropErr != nil {
					queueDroppedTotal.WithLabelValues("oldest_drop_failed").Inc()
					return fmt.Errorf("queue full and failed to drop oldest: %w", err)
				}
				queueDroppedTotal.WithLabelValues("oldest").Inc()
				continue // Retry push

			case DropNewest:
				queueDroppedTotal.WithLabelValues("newest").Inc()
				return nil // Drop incoming entry

			case Block:
				// Wait with timeout
				var timedOut atomic.Bool
				timer := time.AfterFunc(q.config.BlockTimeout, func() {
					timedOut.Store(true)
					q.spaceCond.Broadcast()
				})

				for !timedOut.Load() {
					q.spaceCond.Wait()
					if q.closed {
						timer.Stop()
						return fmt.Errorf("queue is closed")
					}
					// Retry push
					if retryErr := q.fq.Push(data); retryErr == nil {
						timer.Stop()
						return nil
					}
				}
				timer.Stop()
				queueDroppedTotal.WithLabelValues("block_timeout").Inc()
				return fmt.Errorf("timeout waiting for queue space")
			}
		}

		// Other errors
		queueDroppedTotal.WithLabelValues("error").Inc()
		return fmt.Errorf("failed to push to queue: %w", err)
	}
}

// Pop removes and returns the oldest entry from the queue.
func (q *SendQueue) Pop() (*QueueEntry, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return nil, ErrQueueClosed
	}

	data, err := q.fq.Pop()
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}

	// Signal waiting goroutines
	q.spaceCond.Broadcast()

	id := uuid.New().String()

	// Periodically clean stale retry entries to prevent unbounded growth
	q.cleanRetries()

	return &QueueEntry{
		ID:        id,
		Timestamp: time.Now(),
		Data:      data,
		Retries:   0,
	}, nil
}

// Peek returns the oldest entry without removing it.
func (q *SendQueue) Peek() (*QueueEntry, error) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	if q.closed {
		return nil, ErrQueueClosed
	}

	data, err := q.fq.Peek()
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}

	// Reuse a stable ID for the peeked entry based on queue position
	// to avoid generating new UUIDs that leak in the retries map
	id := "peek-head"
	return &QueueEntry{
		ID:        id,
		Timestamp: time.Now(),
		Data:      data,
		Retries:   q.getRetries(id),
	}, nil
}

// Remove removes an entry by ID.
// Note: In FastQueue, entries are removed by Pop(), so this is a no-op.
func (q *SendQueue) Remove(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return ErrQueueClosed
	}

	// Signal waiting goroutines
	q.spaceCond.Broadcast()

	// Clean up retry tracking
	q.retriesMu.Lock()
	delete(q.retries, id)
	q.retriesMu.Unlock()

	return nil
}

// UpdateRetries updates the retry count for an entry.
func (q *SendQueue) UpdateRetries(id string, retries int) {
	q.retriesMu.Lock()
	q.retries[id] = retries
	q.retriesMu.Unlock()
}

// getRetries returns the retry count for an entry.
func (q *SendQueue) getRetries(id string) int {
	q.retriesMu.RLock()
	defer q.retriesMu.RUnlock()
	return q.retries[id]
}

// cleanRetries removes stale entries from the retries map when it grows too large.
func (q *SendQueue) cleanRetries() {
	const maxRetryEntries = 10000
	q.retriesMu.Lock()
	defer q.retriesMu.Unlock()
	if len(q.retries) > maxRetryEntries {
		q.retries = make(map[string]int)
	}
}

// Len returns the number of entries in the queue.
func (q *SendQueue) Len() int {
	return q.fq.Len()
}

// Size returns the total bytes in the queue.
func (q *SendQueue) Size() int64 {
	return q.fq.Size()
}

// EffectiveMaxSize returns the current effective max size.
func (q *SendQueue) EffectiveMaxSize() int {
	return q.config.MaxSize
}

// EffectiveMaxBytes returns the current effective max bytes.
func (q *SendQueue) EffectiveMaxBytes() int64 {
	return q.config.MaxBytes
}

// Close closes the queue.
func (q *SendQueue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.closed = true
	q.spaceCond.Broadcast()

	return q.fq.Close()
}

// GetRequest deserializes the data to an ExportMetricsServiceRequest.
func (e *QueueEntry) GetRequest() (*colmetricspb.ExportMetricsServiceRequest, error) {
	var req colmetricspb.ExportMetricsServiceRequest
	if err := proto.Unmarshal(e.Data, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal request: %w", err)
	}
	return &req, nil
}
