package exporter

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/szibis/metrics-governor/internal/logging"
	"github.com/szibis/metrics-governor/internal/queue"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
)

// CircuitState represents the state of a circuit breaker.
type CircuitState int32

const (
	// CircuitClosed means the circuit is operating normally.
	CircuitClosed CircuitState = iota
	// CircuitOpen means the circuit is open and requests are blocked.
	CircuitOpen
	// CircuitHalfOpen means the circuit is testing if the service is recovered.
	CircuitHalfOpen
)

// CircuitBreaker implements the circuit breaker pattern to prevent cascading failures.
type CircuitBreaker struct {
	state            atomic.Int32
	consecutiveFails atomic.Int32
	lastFailure      atomic.Int64 // Unix timestamp
	lastStateChange  atomic.Int64 // Unix timestamp

	// Configuration
	failureThreshold int           // Number of failures before opening circuit
	resetTimeout     time.Duration // Time to wait before trying again (half-open)
	halfOpenMaxTries int           // Max attempts in half-open state
}

// NewCircuitBreaker creates a new circuit breaker with the given configuration.
func NewCircuitBreaker(failureThreshold int, resetTimeout time.Duration) *CircuitBreaker {
	cb := &CircuitBreaker{
		failureThreshold: failureThreshold,
		resetTimeout:     resetTimeout,
		halfOpenMaxTries: 1,
	}
	cb.state.Store(int32(CircuitClosed))
	return cb
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() CircuitState {
	return CircuitState(cb.state.Load())
}

// ConsecutiveFailures returns the current consecutive failure count.
func (cb *CircuitBreaker) ConsecutiveFailures() int {
	return int(cb.consecutiveFails.Load())
}

// AllowRequest checks if a request should be allowed through.
func (cb *CircuitBreaker) AllowRequest() bool {
	state := CircuitState(cb.state.Load())

	switch state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		// Check if reset timeout has passed
		lastFail := cb.lastFailure.Load()
		if time.Now().Unix()-lastFail >= int64(cb.resetTimeout.Seconds()) {
			// Transition to half-open
			cb.state.Store(int32(CircuitHalfOpen))
			cb.lastStateChange.Store(time.Now().Unix())
			queue.SetCircuitState("half_open")
			logging.Info("circuit breaker transitioning to half-open", logging.F(
				"reset_timeout", cb.resetTimeout.String(),
			))
			return true
		}
		return false
	case CircuitHalfOpen:
		// Allow limited requests in half-open state
		return true
	default:
		return true
	}
}

// RecordSuccess records a successful request.
func (cb *CircuitBreaker) RecordSuccess() {
	state := CircuitState(cb.state.Load())

	// Reset consecutive failures
	cb.consecutiveFails.Store(0)

	if state == CircuitHalfOpen {
		// Success in half-open state, close the circuit
		cb.state.Store(int32(CircuitClosed))
		cb.lastStateChange.Store(time.Now().Unix())
		queue.SetCircuitState("closed")
		logging.Info("circuit breaker closed after successful request")
	}
}

// RecordFailure records a failed request.
func (cb *CircuitBreaker) RecordFailure() {
	fails := cb.consecutiveFails.Add(1)
	cb.lastFailure.Store(time.Now().Unix())

	state := CircuitState(cb.state.Load())

	if state == CircuitHalfOpen {
		// Failure in half-open state, reopen the circuit
		cb.state.Store(int32(CircuitOpen))
		cb.lastStateChange.Store(time.Now().Unix())
		queue.SetCircuitState("open")
		queue.IncrementCircuitOpen()
		logging.Warn("circuit breaker reopened after half-open failure", logging.F(
			"consecutive_failures", fails,
		))
		return
	}

	if state == CircuitClosed && int(fails) >= cb.failureThreshold {
		// Too many failures, open the circuit
		cb.state.Store(int32(CircuitOpen))
		cb.lastStateChange.Store(time.Now().Unix())
		queue.SetCircuitState("open")
		queue.IncrementCircuitOpen()
		logging.Warn("circuit breaker opened due to consecutive failures", logging.F(
			"consecutive_failures", fails,
			"threshold", cb.failureThreshold,
			"reset_timeout", cb.resetTimeout.String(),
		))
	}
}

// QueuedExporter wraps an Exporter with a persistent queue for retry.
type QueuedExporter struct {
	exporter       Exporter
	queue          *queue.SendQueue
	baseDelay      time.Duration
	maxDelay       time.Duration
	circuitBreaker *CircuitBreaker

	// Backoff configuration
	backoffEnabled    bool
	backoffMultiplier float64

	// Backoff state
	currentDelay atomic.Int64 // Current backoff delay in nanoseconds

	retryStop chan struct{}
	retryDone chan struct{}

	mu     sync.Mutex
	closed bool
}

// NewQueued creates a new QueuedExporter.
// Configuration is read from queueCfg including backoff and circuit breaker settings.
func NewQueued(exporter Exporter, queueCfg queue.Config) (*QueuedExporter, error) {
	q, err := queue.New(queueCfg)
	if err != nil {
		return nil, err
	}

	// Apply defaults for backoff settings
	backoffEnabled := queueCfg.BackoffEnabled
	backoffMultiplier := queueCfg.BackoffMultiplier
	if backoffMultiplier <= 0 {
		backoffMultiplier = 2.0 // Default multiplier
	}

	qe := &QueuedExporter{
		exporter:          exporter,
		queue:             q,
		baseDelay:         queueCfg.RetryInterval,
		maxDelay:          queueCfg.MaxRetryDelay,
		backoffEnabled:    backoffEnabled,
		backoffMultiplier: backoffMultiplier,
		retryStop:         make(chan struct{}),
		retryDone:         make(chan struct{}),
	}

	// Initialize current delay to base delay
	qe.currentDelay.Store(int64(queueCfg.RetryInterval))

	// Initialize circuit breaker if enabled
	if queueCfg.CircuitBreakerEnabled {
		threshold := queueCfg.CircuitBreakerThreshold
		if threshold <= 0 {
			threshold = 10 // Default threshold
		}
		resetTimeout := queueCfg.CircuitBreakerResetTimeout
		if resetTimeout <= 0 {
			resetTimeout = 30 * time.Second // Default timeout
		}
		qe.circuitBreaker = NewCircuitBreaker(threshold, resetTimeout)
		queue.SetCircuitState("closed")
		logging.Info("circuit breaker initialized", logging.F(
			"failure_threshold", threshold,
			"reset_timeout", resetTimeout.String(),
		))
	}

	if backoffEnabled {
		logging.Info("exponential backoff enabled", logging.F(
			"multiplier", backoffMultiplier,
			"base_delay", queueCfg.RetryInterval.String(),
			"max_delay", queueCfg.MaxRetryDelay.String(),
		))
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

	// Start with base delay (ensure positive for ticker)
	currentDelay := e.baseDelay
	if currentDelay <= 0 {
		currentDelay = 5 * time.Second // Default if not set
	}
	ticker := time.NewTicker(currentDelay)
	defer ticker.Stop()

	for {
		select {
		case <-e.retryStop:
			// Drain remaining entries before stopping
			e.drainQueue()
			return

		case <-ticker.C:
			// Check circuit breaker before processing
			if e.circuitBreaker != nil && !e.circuitBreaker.AllowRequest() {
				// Circuit breaker is open, skip retry
				queue.IncrementCircuitRejected()
				continue
			}

			success := e.processQueue()

			// Adjust backoff based on result
			if success {
				// Reset to base delay on success
				baseDelay := e.baseDelay
				if baseDelay <= 0 {
					baseDelay = 5 * time.Second
				}
				if currentDelay != baseDelay {
					currentDelay = baseDelay
					ticker.Reset(currentDelay)
					logging.Info("backoff reset to base delay", logging.F(
						"delay", currentDelay.String(),
					))
				}
			} else {
				// Exponential backoff on failure (only if enabled and we actually tried)
				if e.backoffEnabled && e.queue.Len() > 0 {
					newDelay := time.Duration(float64(currentDelay) * e.backoffMultiplier)
					if newDelay > e.maxDelay && e.maxDelay > 0 {
						newDelay = e.maxDelay
					}
					// Ensure delay is positive for ticker
					if newDelay <= 0 {
						newDelay = 5 * time.Second
					}
					if newDelay != currentDelay {
						currentDelay = newDelay
						ticker.Reset(currentDelay)
						logging.Info("exponential backoff increased", logging.F(
							"delay", currentDelay.String(),
							"max_delay", e.maxDelay.String(),
							"multiplier", e.backoffMultiplier,
						))
					}
				}
			}

			// Update metric
			queue.SetCurrentBackoff(currentDelay)
		}
	}
}

// processQueue attempts to process one entry from the queue.
// Returns true if successful or queue is empty, false on failure.
func (e *QueuedExporter) processQueue() bool {
	// Pop the entry from the queue - with FastQueue, we must use Pop() not Peek()
	// because Remove() is a no-op in FastQueue
	entry, err := e.queue.Pop()
	if err != nil {
		logging.Error("failed to pop queue", logging.F("error", err.Error()))
		return false
	}

	if entry == nil {
		// Queue is empty - considered success for backoff purposes
		return true
	}

	req, err := entry.GetRequest()
	if err != nil {
		logging.Error("failed to deserialize queued request", logging.F("error", err.Error()))
		// Don't re-push corrupted data
		return false
	}

	queue.IncrementRetryTotal()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	err = e.exporter.Export(ctx, req)
	cancel()

	if err == nil {
		// Success - entry already removed by Pop()
		queue.IncrementRetrySuccessTotal()

		// Record success with circuit breaker
		if e.circuitBreaker != nil {
			e.circuitBreaker.RecordSuccess()
		}

		logging.Info("retry succeeded", logging.F(
			"queue_size", e.queue.Len(),
		))
		return true
	}

	// Failure - record with circuit breaker
	if e.circuitBreaker != nil {
		e.circuitBreaker.RecordFailure()
	}

	// Re-push to queue for later retry
	// Note: This puts it at the back of the queue
	if pushErr := e.queue.Push(req); pushErr != nil {
		logging.Error("failed to re-queue failed entry", logging.F("error", pushErr.Error()))
	}
	logging.Info("retry failed", logging.F(
		"error", err.Error(),
		"queue_size", e.queue.Len(),
		"circuit_state", e.getCircuitState(),
	))
	return false
}

// getCircuitState returns the current circuit breaker state as a string.
func (e *QueuedExporter) getCircuitState() string {
	if e.circuitBreaker == nil {
		return "disabled"
	}
	switch e.circuitBreaker.State() {
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

// drainQueue attempts to export all remaining entries.
// Entries that fail to export are re-pushed to the queue for recovery on restart.
func (e *QueuedExporter) drainQueue() {
	logging.Info("draining queue", logging.F("queue_size", e.queue.Len()))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Track failed entries to re-push after draining
	var failedEntries []*colmetricspb.ExportMetricsServiceRequest

	for {
		select {
		case <-ctx.Done():
			logging.Warn("timeout draining queue", logging.F("remaining", e.queue.Len()))
			// Re-push failed entries
			for _, req := range failedEntries {
				_ = e.queue.Push(req)
			}
			return
		default:
		}

		entry, err := e.queue.Pop()
		if err != nil || entry == nil {
			break
		}

		req, err := entry.GetRequest()
		if err != nil {
			// Skip corrupted entries
			continue
		}

		exportCtx, exportCancel := context.WithTimeout(ctx, 5*time.Second)
		err = e.exporter.Export(exportCtx, req)
		exportCancel()

		if err == nil {
			queue.IncrementRetrySuccessTotal()
		} else {
			// Save for re-pushing after drain
			failedEntries = append(failedEntries, req)
			logging.Warn("failed to drain queue entry, will persist for recovery", logging.F("error", err.Error()))
		}
	}

	// Re-push failed entries for persistence
	for _, req := range failedEntries {
		_ = e.queue.Push(req)
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
