package prw

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/szibis/metrics-governor/internal/logging"
	"github.com/szibis/metrics-governor/internal/queue"
)

// QueueConfig holds PRW queue configuration.
type QueueConfig struct {
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
	FullBehavior queue.FullBehavior
}

// DefaultQueueConfig returns default queue configuration.
func DefaultQueueConfig() QueueConfig {
	return QueueConfig{
		Path:          "./prw-queue",
		MaxSize:       10000,
		MaxBytes:      1073741824, // 1GB
		RetryInterval: 5 * time.Second,
		MaxRetryDelay: 5 * time.Minute,
		FullBehavior:  queue.DropOldest,
	}
}

// Queue is a WAL-based persistent queue for PRW WriteRequests.
type Queue struct {
	sendQueue *queue.SendQueue
	exporter  PRWExporter
	config    QueueConfig

	retryStop chan struct{}
	retryDone chan struct{}
	mu        sync.Mutex
	closed    bool
}

// NewQueue creates a new persistent PRW queue.
func NewQueue(cfg QueueConfig, exporter PRWExporter) (*Queue, error) {
	// Apply defaults
	if cfg.Path == "" {
		cfg.Path = "./prw-queue"
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
		cfg.FullBehavior = queue.DropOldest
	}

	// Create underlying send queue
	sendQueue, err := queue.New(queue.Config{
		Path:          cfg.Path,
		MaxSize:       cfg.MaxSize,
		MaxBytes:      cfg.MaxBytes,
		RetryInterval: cfg.RetryInterval,
		MaxRetryDelay: cfg.MaxRetryDelay,
		FullBehavior:  cfg.FullBehavior,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create send queue: %w", err)
	}

	q := &Queue{
		sendQueue: sendQueue,
		exporter:  exporter,
		config:    cfg,
		retryStop: make(chan struct{}),
		retryDone: make(chan struct{}),
	}

	// Start retry loop
	go q.retryLoop()

	return q, nil
}

// Push adds a WriteRequest to the queue.
func (q *Queue) Push(req *WriteRequest) error {
	if req == nil || len(req.Timeseries) == 0 {
		return nil
	}

	// Marshal the request
	data, err := req.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal PRW request: %w", err)
	}

	// Push to underlying queue
	return q.sendQueue.PushData(data)
}

// Export sends a WriteRequest, queuing it for retry on failure.
func (q *Queue) Export(ctx context.Context, req *WriteRequest) error {
	if req == nil || len(req.Timeseries) == 0 {
		return nil
	}

	// Try immediate export
	err := q.exporter.Export(ctx, req)
	if err == nil {
		return nil
	}

	// Queue for retry
	if pushErr := q.Push(req); pushErr != nil {
		logging.Error("PRW queue push failed", logging.F(
			"error", pushErr.Error(),
			"timeseries", len(req.Timeseries),
		))
		return err // Return original export error
	}

	logging.Info("PRW request queued for retry", logging.F(
		"timeseries", len(req.Timeseries),
		"queue_size", q.sendQueue.Len(),
	))

	return nil // Queued successfully
}

// retryLoop continuously retries queued requests.
func (q *Queue) retryLoop() {
	defer close(q.retryDone)

	delay := q.config.RetryInterval
	timer := time.NewTimer(delay)
	defer timer.Stop()

	for {
		select {
		case <-q.retryStop:
			q.drainQueue()
			return
		case <-timer.C:
			if q.processQueue() {
				delay = q.config.RetryInterval
			} else {
				delay = minDuration(delay*2, q.config.MaxRetryDelay)
			}
			timer.Reset(delay)
		}
	}
}

// getExporter safely returns the current exporter.
func (q *Queue) getExporter() PRWExporter {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.exporter
}

// processQueue processes one item from the queue.
func (q *Queue) processQueue() bool {
	entry, err := q.sendQueue.Peek()
	if err != nil {
		logging.Error("PRW queue peek failed", logging.F("error", err.Error()))
		return false
	}
	if entry == nil {
		return true // Queue is empty
	}

	// Unmarshal the request
	req := &WriteRequest{}
	if err := req.Unmarshal(entry.Data); err != nil {
		// Invalid data, remove from queue
		logging.Error("PRW queue unmarshal failed, removing entry", logging.F("error", err.Error()))
		_ = q.sendQueue.Remove(entry.ID)
		return false
	}

	// Capture exporter reference to avoid race with SetExporter
	exporter := q.getExporter()
	if exporter == nil {
		return false
	}

	// Try to export
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := exporter.Export(ctx, req); err != nil {
		// Update retry count
		q.sendQueue.UpdateRetries(entry.ID, entry.Retries+1)
		logging.Warn("PRW queue retry failed", logging.F(
			"error", err.Error(),
			"timeseries", len(req.Timeseries),
			"retries", entry.Retries+1,
		))
		return false
	}

	// Success, remove from queue
	if err := q.sendQueue.Remove(entry.ID); err != nil {
		logging.Error("PRW queue remove failed", logging.F("error", err.Error()))
	}

	logging.Info("PRW queued request exported successfully", logging.F(
		"timeseries", len(req.Timeseries),
		"retries", entry.Retries,
	))

	return true
}

// drainQueue attempts to export all remaining queued items.
func (q *Queue) drainQueue() {
	// Capture exporter reference once at the start
	exporter := q.getExporter()
	if exporter == nil {
		return
	}

	for {
		entry, err := q.sendQueue.Pop()
		if err != nil || entry == nil {
			return
		}

		req := &WriteRequest{}
		if err := req.Unmarshal(entry.Data); err != nil {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = exporter.Export(ctx, req)
		cancel()

		if err != nil {
			logging.Warn("PRW drain failed", logging.F(
				"error", err.Error(),
				"timeseries", len(req.Timeseries),
			))
		}
	}
}

// Len returns the number of entries in the queue.
func (q *Queue) Len() int {
	return q.sendQueue.Len()
}

// Size returns the total bytes in the queue.
func (q *Queue) Size() int64 {
	return q.sendQueue.Size()
}

// Close closes the queue.
func (q *Queue) Close() error {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return nil
	}
	q.closed = true
	q.mu.Unlock()

	close(q.retryStop)
	<-q.retryDone

	return q.sendQueue.Close()
}

// SetExporter sets the exporter for the queue.
func (q *Queue) SetExporter(exp PRWExporter) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.exporter = exp
}

// minDuration returns the smaller of two durations.
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
