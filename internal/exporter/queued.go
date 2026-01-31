package exporter

import (
	"context"
	"sync"
	"time"

	"github.com/szibis/metrics-governor/internal/logging"
	"github.com/szibis/metrics-governor/internal/queue"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
)

// QueuedExporter wraps an Exporter with a persistent queue for retry.
type QueuedExporter struct {
	exporter  Exporter
	queue     *queue.SendQueue
	baseDelay time.Duration
	maxDelay  time.Duration

	retryStop chan struct{}
	retryDone chan struct{}

	mu     sync.Mutex
	closed bool
}

// NewQueued creates a new QueuedExporter.
func NewQueued(exporter Exporter, queueCfg queue.Config) (*QueuedExporter, error) {
	q, err := queue.New(queueCfg)
	if err != nil {
		return nil, err
	}

	qe := &QueuedExporter{
		exporter:  exporter,
		queue:     q,
		baseDelay: queueCfg.RetryInterval,
		maxDelay:  queueCfg.MaxRetryDelay,
		retryStop: make(chan struct{}),
		retryDone: make(chan struct{}),
	}

	// Start the retry loop
	go qe.retryLoop()

	return qe, nil
}

// Export attempts to export immediately, queueing on failure.
func (e *QueuedExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	// Try immediate export
	err := e.exporter.Export(ctx, req)
	if err == nil {
		return nil
	}

	// On failure, queue for retry
	logging.Info("export failed, queueing for retry", logging.F(
		"error", err.Error(),
		"queue_size", e.queue.Len(),
	))

	if queueErr := e.queue.Push(req); queueErr != nil {
		logging.Error("failed to queue export request", logging.F(
			"error", queueErr.Error(),
			"original_error", err.Error(),
		))
		return err
	}

	// Return nil - data is queued, will be retried
	return nil
}

// Close stops the retry loop and closes the queue.
func (e *QueuedExporter) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	e.mu.Unlock()

	// Signal retry loop to stop
	close(e.retryStop)

	// Wait for retry loop to finish (with timeout)
	select {
	case <-e.retryDone:
	case <-time.After(30 * time.Second):
		logging.Warn("timeout waiting for retry loop to finish")
	}

	// Close the queue
	if err := e.queue.Close(); err != nil {
		logging.Error("failed to close queue", logging.F("error", err.Error()))
	}

	// Close underlying exporter
	return e.exporter.Close()
}

// retryLoop processes queued entries in the background.
func (e *QueuedExporter) retryLoop() {
	defer close(e.retryDone)

	ticker := time.NewTicker(e.baseDelay)
	defer ticker.Stop()

	for {
		select {
		case <-e.retryStop:
			// Drain remaining entries before stopping
			e.drainQueue()
			return

		case <-ticker.C:
			e.processQueue()
		}
	}
}

// processQueue attempts to process one entry from the queue.
func (e *QueuedExporter) processQueue() {
	entry, err := e.queue.Peek()
	if err != nil {
		logging.Error("failed to peek queue", logging.F("error", err.Error()))
		return
	}

	if entry == nil {
		return
	}

	// Calculate backoff delay
	delay := e.calculateBackoff(entry.Retries)

	// Check if enough time has passed since last attempt
	if time.Since(entry.Timestamp) < delay && entry.Retries > 0 {
		return
	}

	req, err := entry.GetRequest()
	if err != nil {
		logging.Error("failed to deserialize queued request", logging.F("error", err.Error()))
		_ = e.queue.Remove(entry.ID)
		return
	}

	queue.IncrementRetryTotal()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	err = e.exporter.Export(ctx, req)
	cancel()

	if err == nil {
		// Success - remove from queue
		if removeErr := e.queue.Remove(entry.ID); removeErr != nil {
			logging.Error("failed to remove entry from queue", logging.F("error", removeErr.Error()))
		}
		queue.IncrementRetrySuccessTotal()
		logging.Info("retry succeeded", logging.F(
			"retries", entry.Retries,
			"queue_size", e.queue.Len(),
		))
	} else {
		// Failure - update retry count
		e.queue.UpdateRetries(entry.ID, entry.Retries+1)
		logging.Info("retry failed", logging.F(
			"error", err.Error(),
			"retries", entry.Retries+1,
			"queue_size", e.queue.Len(),
		))
	}
}

// drainQueue attempts to export all remaining entries.
// Entries that fail to export remain in the queue for recovery on restart.
func (e *QueuedExporter) drainQueue() {
	logging.Info("draining queue", logging.F("queue_size", e.queue.Len()))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	triedOffsets := make(map[int64]bool)

	for {
		select {
		case <-ctx.Done():
			logging.Warn("timeout draining queue", logging.F("remaining", e.queue.Len()))
			return
		default:
		}

		entry, err := e.queue.Peek()
		if err != nil || entry == nil {
			return
		}

		// Skip if we've already tried this entry
		if triedOffsets[entry.Offset] {
			return // All remaining entries have been tried
		}
		triedOffsets[entry.Offset] = true

		req, err := entry.GetRequest()
		if err != nil {
			_ = e.queue.Remove(entry.ID)
			continue
		}

		exportCtx, exportCancel := context.WithTimeout(ctx, 5*time.Second)
		err = e.exporter.Export(exportCtx, req)
		exportCancel()

		if err == nil {
			_ = e.queue.Remove(entry.ID)
			queue.IncrementRetrySuccessTotal()
		} else {
			// Leave entry in queue for recovery on restart
			logging.Warn("failed to drain queue entry, will persist for recovery", logging.F("error", err.Error()))
		}
	}
}

// calculateBackoff returns the delay for the given retry count.
func (e *QueuedExporter) calculateBackoff(retries int) time.Duration {
	if retries <= 0 {
		return 0
	}

	delay := e.baseDelay
	for i := 0; i < retries; i++ {
		delay *= 2
		if delay > e.maxDelay {
			return e.maxDelay
		}
	}
	return delay
}

// QueueLen returns the current queue length (for testing/monitoring).
func (e *QueuedExporter) QueueLen() int {
	return e.queue.Len()
}

// QueueSize returns the current queue size in bytes (for testing/monitoring).
func (e *QueuedExporter) QueueSize() int64 {
	return e.queue.Size()
}
