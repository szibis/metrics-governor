package exporter

import (
	"context"
	"sync"
	"time"

	"github.com/szibis/metrics-governor/internal/logging"
	"github.com/szibis/metrics-governor/internal/prw"
)

// PRWQueueConfig holds configuration for the PRW queued exporter.
type PRWQueueConfig struct {
	// Path is the directory for queue storage.
	Path string
	// MaxSize is the maximum number of entries in the queue.
	MaxSize int
	// MaxBytes is the maximum total size of the queue in bytes.
	MaxBytes int64
	// RetryInterval is the initial retry interval.
	RetryInterval time.Duration
	// MaxRetryDelay is the maximum retry backoff delay.
	MaxRetryDelay time.Duration
}

// DefaultPRWQueueConfig returns default queue configuration.
func DefaultPRWQueueConfig() PRWQueueConfig {
	return PRWQueueConfig{
		Path:          "./prw-queue",
		MaxSize:       10000,
		MaxBytes:      1073741824, // 1GB
		RetryInterval: 5 * time.Second,
		MaxRetryDelay: 5 * time.Minute,
	}
}

// prwQueueEntry represents an entry in the PRW retry queue.
type prwQueueEntry struct {
	req     *prw.WriteRequest
	retries int
}

// prwExporterInterface is an interface for PRW exporters to enable testing.
type prwExporterInterface interface {
	Export(ctx context.Context, req *prw.WriteRequest) error
	Close() error
}

// PRWQueuedExporter wraps a PRWExporter with an in-memory retry queue.
type PRWQueuedExporter struct {
	exporter   prwExporterInterface
	queue      []*prwQueueEntry
	maxSize    int
	baseDelay  time.Duration
	maxDelay   time.Duration
	retryStop  chan struct{}
	retryDone  chan struct{}
	mu         sync.Mutex
}

// NewPRWQueued creates a new queued PRW exporter.
func NewPRWQueued(exporter prwExporterInterface, cfg PRWQueueConfig) (*PRWQueuedExporter, error) {
	maxSize := cfg.MaxSize
	if maxSize == 0 {
		maxSize = 10000
	}

	baseDelay := cfg.RetryInterval
	if baseDelay == 0 {
		baseDelay = 5 * time.Second
	}

	maxDelay := cfg.MaxRetryDelay
	if maxDelay == 0 {
		maxDelay = 5 * time.Minute
	}

	qe := &PRWQueuedExporter{
		exporter:  exporter,
		queue:     make([]*prwQueueEntry, 0, maxSize),
		maxSize:   maxSize,
		baseDelay: baseDelay,
		maxDelay:  maxDelay,
		retryStop: make(chan struct{}),
		retryDone: make(chan struct{}),
	}

	// Start retry loop
	go qe.retryLoop()

	return qe, nil
}

// Export sends the PRW request, queuing it for retry on failure.
func (qe *PRWQueuedExporter) Export(ctx context.Context, req *prw.WriteRequest) error {
	if req == nil || len(req.Timeseries) == 0 {
		return nil
	}

	// Try immediate export
	err := qe.exporter.Export(ctx, req)
	if err == nil {
		return nil
	}

	// Check if error is retryable
	if !IsPRWRetryableError(err) {
		logging.Error("PRW export failed with non-retryable error", logging.F(
			"error", err.Error(),
			"timeseries", len(req.Timeseries),
		))
		return err
	}

	// Queue for retry
	qe.mu.Lock()
	defer qe.mu.Unlock()

	if len(qe.queue) >= qe.maxSize {
		// Drop oldest entry
		qe.queue = qe.queue[1:]
		logging.Warn("PRW queue full, dropping oldest entry")
	}

	// Clone the request before queueing
	qe.queue = append(qe.queue, &prwQueueEntry{
		req:     req.Clone(),
		retries: 0,
	})

	logging.Info("PRW request queued for retry", logging.F(
		"timeseries", len(req.Timeseries),
		"queue_size", len(qe.queue),
	))

	return nil
}

// retryLoop continuously retries queued requests.
func (qe *PRWQueuedExporter) retryLoop() {
	defer close(qe.retryDone)

	delay := qe.baseDelay
	timer := time.NewTimer(delay)
	defer timer.Stop()

	for {
		select {
		case <-qe.retryStop:
			// Drain remaining items best-effort
			qe.drainQueue()
			return
		case <-timer.C:
			if qe.processQueue() {
				// Success, reset delay
				delay = qe.baseDelay
			} else {
				// Failure, increase delay
				delay = minDuration(delay*2, qe.maxDelay)
			}
			timer.Reset(delay)
		}
	}
}

// processQueue processes one item from the queue.
func (qe *PRWQueuedExporter) processQueue() bool {
	qe.mu.Lock()
	if len(qe.queue) == 0 {
		qe.mu.Unlock()
		return true // Queue is empty
	}

	entry := qe.queue[0]
	qe.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := qe.exporter.Export(ctx, entry.req); err != nil {
		if !IsPRWRetryableError(err) {
			// Non-retryable error, remove from queue
			qe.mu.Lock()
			if len(qe.queue) > 0 {
				qe.queue = qe.queue[1:]
			}
			qe.mu.Unlock()
			logging.Error("PRW retry failed with non-retryable error, removing from queue", logging.F(
				"error", err.Error(),
				"timeseries", len(entry.req.Timeseries),
			))
			return false
		}

		// Increment retry count
		qe.mu.Lock()
		if len(qe.queue) > 0 {
			qe.queue[0].retries++
		}
		qe.mu.Unlock()
		return false
	}

	// Success, remove from queue
	qe.mu.Lock()
	if len(qe.queue) > 0 {
		qe.queue = qe.queue[1:]
	}
	qe.mu.Unlock()

	logging.Info("PRW queued request exported successfully", logging.F(
		"timeseries", len(entry.req.Timeseries),
		"retries", entry.retries,
	))
	return true
}

// drainQueue attempts to export all remaining queued items.
func (qe *PRWQueuedExporter) drainQueue() {
	for {
		qe.mu.Lock()
		if len(qe.queue) == 0 {
			qe.mu.Unlock()
			return
		}
		entry := qe.queue[0]
		qe.queue = qe.queue[1:]
		qe.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := qe.exporter.Export(ctx, entry.req)
		cancel()

		if err != nil {
			// Best effort, just log and continue
			logging.Warn("PRW drain failed", logging.F(
				"error", err.Error(),
				"timeseries", len(entry.req.Timeseries),
			))
		}
	}
}

// Close stops the retry loop and closes the underlying exporter.
func (qe *PRWQueuedExporter) Close() error {
	close(qe.retryStop)
	<-qe.retryDone
	return qe.exporter.Close()
}

// QueueSize returns the current number of items in the queue.
func (qe *PRWQueuedExporter) QueueSize() int {
	qe.mu.Lock()
	defer qe.mu.Unlock()
	return len(qe.queue)
}

// minDuration returns the smaller of two durations.
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
