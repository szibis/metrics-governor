package queue

import (
	"fmt"
	"sync"
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
}

// DefaultConfig returns a default queue configuration.
func DefaultConfig() Config {
	return Config{
		Path:              "./queue",
		MaxSize:           10000,
		MaxBytes:          1073741824, // 1GB
		RetryInterval:     5 * time.Second,
		MaxRetryDelay:     5 * time.Minute,
		FullBehavior:      DropOldest,
		BlockTimeout:      30 * time.Second,
		TargetUtilization: 0.85,
		AdaptiveEnabled:   true,
		CompactThreshold:  0.5,
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
	// Offset is the WAL offset for this entry.
	Offset int64
}

// SendQueue is a WAL-based persistent queue for export requests.
type SendQueue struct {
	config Config
	wal    *WAL
	mu     sync.RWMutex
	closed bool

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

	// Create WAL
	walCfg := WALConfig{
		Path:              cfg.Path,
		MaxSize:           cfg.MaxSize,
		MaxBytes:          cfg.MaxBytes,
		TargetUtilization: cfg.TargetUtilization,
		CompactThreshold:  cfg.CompactThreshold,
		AdaptiveEnabled:   cfg.AdaptiveEnabled,
	}

	wal, err := NewWAL(walCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create WAL: %w", err)
	}

	q := &SendQueue{
		config: cfg,
		wal:    wal,
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

// pushData adds serialized data to the queue.
func (q *SendQueue) pushData(data []byte) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return fmt.Errorf("queue is closed")
	}

	// Try to append to WAL
	for {
		err := q.wal.Append(data)
		if err == nil {
			queuePushTotal.Inc()
			q.spaceCond.Broadcast()
			return nil
		}

		// Handle different error cases
		if err == ErrDiskFull {
			// Disk is full - handle according to FullBehavior
			switch q.config.FullBehavior {
			case DropOldest:
				// Try to make room by marking oldest as consumed
				if dropErr := q.dropOldestLocked(); dropErr != nil {
					queueDroppedTotal.WithLabelValues("disk_full_drop_failed").Inc()
					return fmt.Errorf("disk full and failed to drop oldest: %w", err)
				}
				queueDroppedTotal.WithLabelValues("disk_full").Inc()
				continue // Retry append

			case DropNewest:
				queueDroppedTotal.WithLabelValues("disk_full_newest").Inc()
				return nil // Drop incoming entry

			case Block:
				// Wait with timeout
				q.mu.Unlock()
				done := make(chan struct{})
				go func() {
					time.Sleep(q.config.BlockTimeout)
					close(done)
					q.spaceCond.Broadcast()
				}()

				q.mu.Lock()
				for {
					select {
					case <-done:
						queueDroppedTotal.WithLabelValues("disk_full_timeout").Inc()
						return fmt.Errorf("timeout waiting for disk space: %w", err)
					default:
						q.spaceCond.Wait()
						if q.closed {
							return fmt.Errorf("queue is closed")
						}
						// Retry append
						if retryErr := q.wal.Append(data); retryErr == nil {
							queuePushTotal.Inc()
							return nil
						}
					}
				}
			}
		}

		if err == ErrQueueFull {
			// Queue is full (by count or bytes limit)
			switch q.config.FullBehavior {
			case DropOldest:
				if dropErr := q.dropOldestLocked(); dropErr != nil {
					queueDroppedTotal.WithLabelValues("oldest_drop_failed").Inc()
					return fmt.Errorf("queue full and failed to drop oldest: %w", err)
				}
				queueDroppedTotal.WithLabelValues("oldest").Inc()
				continue // Retry append

			case DropNewest:
				queueDroppedTotal.WithLabelValues("newest").Inc()
				return nil // Drop incoming entry

			case Block:
				// Wait with timeout
				done := make(chan struct{})
				go func() {
					time.Sleep(q.config.BlockTimeout)
					close(done)
					q.spaceCond.Broadcast()
				}()

				for {
					select {
					case <-done:
						queueDroppedTotal.WithLabelValues("block_timeout").Inc()
						return fmt.Errorf("timeout waiting for queue space")
					default:
						q.spaceCond.Wait()
						if q.closed {
							return fmt.Errorf("queue is closed")
						}
						// Retry append
						if retryErr := q.wal.Append(data); retryErr == nil {
							queuePushTotal.Inc()
							return nil
						}
					}
				}
			}
		}

		// Other errors
		queueDroppedTotal.WithLabelValues("error").Inc()
		return fmt.Errorf("failed to append to queue: %w", err)
	}
}

// Pop removes and returns the oldest entry from the queue.
func (q *SendQueue) Pop() (*QueueEntry, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	entry, err := q.wal.Peek()
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, nil
	}

	// Mark as consumed
	if err := q.wal.MarkConsumed(entry.Offset); err != nil {
		return nil, err
	}

	// Signal waiting goroutines
	q.spaceCond.Broadcast()

	return &QueueEntry{
		ID:        uuid.New().String(),
		Timestamp: entry.Timestamp,
		Data:      entry.Data,
		Retries:   entry.Retries,
		Offset:    entry.Offset,
	}, nil
}

// Peek returns the oldest entry without removing it.
func (q *SendQueue) Peek() (*QueueEntry, error) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	entry, err := q.wal.Peek()
	if err != nil {
		return nil, err
	}
	if entry == nil {
		return nil, nil
	}

	return &QueueEntry{
		ID:        fmt.Sprintf("%d", entry.Offset), // Use offset as ID
		Timestamp: entry.Timestamp,
		Data:      entry.Data,
		Retries:   entry.Retries,
		Offset:    entry.Offset,
	}, nil
}

// Remove removes an entry by ID (offset).
func (q *SendQueue) Remove(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Parse offset from ID
	var offset int64
	if _, err := fmt.Sscanf(id, "%d", &offset); err != nil {
		return fmt.Errorf("invalid entry ID: %s", id)
	}

	if err := q.wal.MarkConsumed(offset); err != nil {
		return err
	}

	// Signal waiting goroutines
	q.spaceCond.Broadcast()

	return nil
}

// UpdateRetries updates the retry count for an entry.
func (q *SendQueue) UpdateRetries(id string, retries int) {
	var offset int64
	if _, err := fmt.Sscanf(id, "%d", &offset); err != nil {
		return
	}

	q.wal.UpdateRetries(offset, retries)
}

// Len returns the number of entries in the queue.
func (q *SendQueue) Len() int {
	return q.wal.Len()
}

// Size returns the total bytes in the queue.
func (q *SendQueue) Size() int64 {
	return q.wal.Size()
}

// EffectiveMaxSize returns the current effective max size (adaptive).
func (q *SendQueue) EffectiveMaxSize() int {
	return q.wal.EffectiveMaxSize()
}

// EffectiveMaxBytes returns the current effective max bytes (adaptive).
func (q *SendQueue) EffectiveMaxBytes() int64 {
	return q.wal.EffectiveMaxBytes()
}

// Close closes the queue.
func (q *SendQueue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.closed = true
	q.spaceCond.Broadcast()

	return q.wal.Close()
}

// dropOldestLocked drops the oldest entry (must hold lock).
func (q *SendQueue) dropOldestLocked() error {
	entry, err := q.wal.Peek()
	if err != nil {
		return err
	}
	if entry == nil {
		return fmt.Errorf("no entries to drop")
	}

	return q.wal.MarkConsumed(entry.Offset)
}

// GetRequest deserializes the data to an ExportMetricsServiceRequest.
func (e *QueueEntry) GetRequest() (*colmetricspb.ExportMetricsServiceRequest, error) {
	var req colmetricspb.ExportMetricsServiceRequest
	if err := proto.Unmarshal(e.Data, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal request: %w", err)
	}
	return &req, nil
}
