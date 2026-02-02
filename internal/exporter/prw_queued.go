package exporter

import (
	"context"
	"sync"
	"time"

	"github.com/szibis/metrics-governor/internal/logging"
	"github.com/szibis/metrics-governor/internal/prw"
	"github.com/szibis/metrics-governor/internal/queue"
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
	// Circuit breaker settings
	CircuitBreakerEnabled   bool          // Enable circuit breaker (default: true)
	CircuitFailureThreshold int           // Failures before opening (default: 10)
	CircuitResetTimeout     time.Duration // Time to wait before half-open (default: 30s)
}

// DefaultPRWQueueConfig returns default queue configuration.
func DefaultPRWQueueConfig() PRWQueueConfig {
	return PRWQueueConfig{
		Path:                    "./prw-queue",
		MaxSize:                 10000,
		MaxBytes:                1073741824, // 1GB
		RetryInterval:           5 * time.Second,
		MaxRetryDelay:           5 * time.Minute,
		CircuitBreakerEnabled:   true,
		CircuitFailureThreshold: 10,
		CircuitResetTimeout:     30 * time.Second,
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
	exporter       prwExporterInterface
	queue          []*prwQueueEntry
	maxSize        int
	baseDelay      time.Duration
	maxDelay       time.Duration
	circuitBreaker *CircuitBreaker
	retryStop      chan struct{}
	retryDone      chan struct{}
	mu             sync.Mutex
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

	// Initialize circuit breaker if enabled
	if cfg.CircuitBreakerEnabled {
		threshold := cfg.CircuitFailureThreshold
		if threshold == 0 {
			threshold = 10
		}
		resetTimeout := cfg.CircuitResetTimeout
		if resetTimeout == 0 {
			resetTimeout = 30 * time.Second
		}
		qe.circuitBreaker = NewCircuitBreaker(threshold, resetTimeout)
		logging.Info("PRW circuit breaker initialized", logging.F(
			"failure_threshold", threshold,
			"reset_timeout", resetTimeout.String(),
		))
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
			// Check circuit breaker before processing
			if qe.circuitBreaker != nil && !qe.circuitBreaker.AllowRequest() {
				// PRW circuit breaker is open, skip retry
				queue.IncrementCircuitRejected()
				timer.Reset(delay)
				continue
			}

			if qe.processQueue() {
				// Success, reset delay
				if delay != qe.baseDelay {
					delay = qe.baseDelay
					logging.Info("PRW backoff reset to base delay", logging.F(
						"delay", delay.String(),
					))
				}
			} else {
				// Failure, increase delay (only if queue has items)
				qe.mu.Lock()
				hasItems := len(qe.queue) > 0
				qe.mu.Unlock()
				if hasItems {
					newDelay := minDuration(delay*2, qe.maxDelay)
					if newDelay != delay {
						delay = newDelay
						logging.Info("PRW exponential backoff increased", logging.F(
							"delay", delay.String(),
							"max_delay", qe.maxDelay.String(),
						))
					}
				}
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
		// Record failure with circuit breaker
		if qe.circuitBreaker != nil {
			qe.circuitBreaker.RecordFailure()
		}

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
		queueSize := len(qe.queue)
		qe.mu.Unlock()

		logging.Info("PRW retry failed", logging.F(
			"error", err.Error(),
			"timeseries", len(entry.req.Timeseries),
			"queue_size", queueSize,
			"circuit_state", qe.getCircuitState(),
		))
		return false
	}

	// Success - record with circuit breaker
	if qe.circuitBreaker != nil {
		qe.circuitBreaker.RecordSuccess()
	}

	// Remove from queue
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

// getCircuitState returns the current circuit breaker state as a string.
func (qe *PRWQueuedExporter) getCircuitState() string {
	if qe.circuitBreaker == nil {
		return "disabled"
	}
	switch qe.circuitBreaker.State() {
	case CircuitClosed:
		return "closed"
	case CircuitOpen:
		return "open"
	case CircuitHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
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
