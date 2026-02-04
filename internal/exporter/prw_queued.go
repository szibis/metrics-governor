package exporter

import (
	"context"
	"errors"
	"fmt"
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
	// BackoffEnabled enables exponential backoff for retries.
	BackoffEnabled bool
	// BackoffMultiplier is the factor to multiply delay by on each failure (default: 2.0).
	BackoffMultiplier float64
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

// prwExporterInterface is an interface for PRW exporters to enable testing.
type prwExporterInterface interface {
	Export(ctx context.Context, req *prw.WriteRequest) error
	Close() error
}

// PRWQueuedExporter wraps a PRWExporter with a persistent disk-backed retry queue.
type PRWQueuedExporter struct {
	exporter       prwExporterInterface
	queue          *queue.SendQueue
	baseDelay      time.Duration
	maxDelay       time.Duration
	circuitBreaker *CircuitBreaker

	// Backoff configuration
	backoffEnabled    bool
	backoffMultiplier float64

	retryStop  chan struct{}
	retryDone  chan struct{}
	mu         sync.Mutex
	closed     bool
	closedOnce sync.Once
}

// NewPRWQueued creates a new queued PRW exporter.
func NewPRWQueued(exporter prwExporterInterface, cfg PRWQueueConfig) (*PRWQueuedExporter, error) {
	baseDelay := cfg.RetryInterval
	if baseDelay == 0 {
		baseDelay = 5 * time.Second
	}

	maxDelay := cfg.MaxRetryDelay
	if maxDelay == 0 {
		maxDelay = 5 * time.Minute
	}

	// Build queue.Config from PRWQueueConfig
	queuePath := cfg.Path
	if queuePath == "" {
		queuePath = "./prw-queue"
	}
	maxSize := cfg.MaxSize
	if maxSize == 0 {
		maxSize = 10000
	}
	maxBytes := cfg.MaxBytes
	if maxBytes == 0 {
		maxBytes = 1073741824 // 1GB
	}
	backoffMultiplier := cfg.BackoffMultiplier
	if backoffMultiplier <= 0 {
		backoffMultiplier = 2.0
	}

	qCfg := queue.Config{
		Path:          queuePath,
		MaxSize:       maxSize,
		MaxBytes:      maxBytes,
		RetryInterval: baseDelay,
		MaxRetryDelay: maxDelay,
	}

	q, err := queue.New(qCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create PRW queue: %w", err)
	}

	qe := &PRWQueuedExporter{
		exporter:          exporter,
		queue:             q,
		baseDelay:         baseDelay,
		maxDelay:          maxDelay,
		backoffEnabled:    cfg.BackoffEnabled,
		backoffMultiplier: backoffMultiplier,
		retryStop:         make(chan struct{}),
		retryDone:         make(chan struct{}),
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

	// Check if error is retryable or splittable
	var exportErr *ExportError
	isSplittable := errors.As(err, &exportErr) && exportErr.IsSplittable()
	if !IsPRWRetryableError(err) && !isSplittable {
		logging.Error("PRW export failed with non-retryable error", logging.F(
			"error", err.Error(),
			"timeseries", len(req.Timeseries),
		))
		return err
	}

	// Serialize and queue for retry
	data, marshalErr := req.Marshal()
	if marshalErr != nil {
		logging.Error("PRW failed to marshal request for queue", logging.F(
			"error", marshalErr.Error(),
		))
		return err
	}

	if pushErr := qe.queue.PushData(data); pushErr != nil {
		logging.Error("PRW failed to push to queue", logging.F(
			"error", pushErr.Error(),
			"timeseries", len(req.Timeseries),
		))
		return err
	}

	logging.Info("PRW request queued for retry", logging.F(
		"timeseries", len(req.Timeseries),
		"queue_size", qe.queue.Len(),
	))

	return nil
}

// retryLoop continuously retries queued requests.
func (qe *PRWQueuedExporter) retryLoop() {
	defer close(qe.retryDone)

	currentDelay := qe.baseDelay
	if currentDelay <= 0 {
		currentDelay = 5 * time.Second
	}
	ticker := time.NewTicker(currentDelay)
	defer ticker.Stop()

	for {
		select {
		case <-qe.retryStop:
			// Drain remaining items best-effort
			qe.drainQueue()
			return
		case <-ticker.C:
			// Check circuit breaker before processing
			if qe.circuitBreaker != nil && !qe.circuitBreaker.AllowRequest() {
				// PRW circuit breaker is open, skip retry
				queue.IncrementCircuitRejected()
				continue
			}

			success := qe.processQueue()

			if success {
				// Reset to base delay on success
				baseDelay := qe.baseDelay
				if baseDelay <= 0 {
					baseDelay = 5 * time.Second
				}
				if currentDelay != baseDelay {
					currentDelay = baseDelay
					ticker.Reset(currentDelay)
					logging.Info("PRW backoff reset to base delay", logging.F(
						"delay", currentDelay.String(),
					))
				}
			} else {
				// Exponential backoff on failure (only if enabled and we actually tried)
				if qe.backoffEnabled && qe.queue.Len() > 0 {
					newDelay := time.Duration(float64(currentDelay) * qe.backoffMultiplier)
					if newDelay > qe.maxDelay && qe.maxDelay > 0 {
						newDelay = qe.maxDelay
					}
					if newDelay <= 0 {
						newDelay = 5 * time.Second
					}
					if newDelay != currentDelay {
						currentDelay = newDelay
						ticker.Reset(currentDelay)
						logging.Info("PRW exponential backoff increased", logging.F(
							"delay", currentDelay.String(),
							"max_delay", qe.maxDelay.String(),
							"multiplier", qe.backoffMultiplier,
						))
					}
				} else if !qe.backoffEnabled && qe.queue.Len() > 0 {
					// Legacy behavior: double delay without multiplier config
					newDelay := minDuration(currentDelay*2, qe.maxDelay)
					if newDelay != currentDelay {
						currentDelay = newDelay
						ticker.Reset(currentDelay)
						logging.Info("PRW exponential backoff increased", logging.F(
							"delay", currentDelay.String(),
							"max_delay", qe.maxDelay.String(),
						))
					}
				}
			}
		}
	}
}

// processQueue processes one item from the queue.
func (qe *PRWQueuedExporter) processQueue() bool {
	entry, err := qe.queue.Pop()
	if err != nil {
		logging.Error("PRW failed to pop queue", logging.F("error", err.Error()))
		return false
	}
	if entry == nil {
		return true // Queue is empty
	}

	req, err := prw.UnmarshalWriteRequest(entry.Data)
	if err != nil {
		logging.Error("PRW failed to deserialize queued request", logging.F("error", err.Error()))
		return false
	}

	prwRetryTotal.Inc()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	exportErr := qe.exporter.Export(ctx, req)
	cancel()

	if exportErr == nil {
		// Success
		if qe.circuitBreaker != nil {
			qe.circuitBreaker.RecordSuccess()
		}
		prwRetrySuccessTotal.Inc()

		logging.Info("PRW queued request exported successfully", logging.F(
			"timeseries", len(req.Timeseries),
			"queue_size", qe.queue.Len(),
		))
		return true
	}

	// Failure - record with circuit breaker
	if qe.circuitBreaker != nil {
		qe.circuitBreaker.RecordFailure()
	}

	// Classify error for metrics
	errType := classifyPRWError(exportErr)
	prwRetryFailureTotal.WithLabelValues(string(errType)).Inc()

	// Check if error is splittable (payload too large) -- split and re-queue halves
	var expErr *ExportError
	if errors.As(exportErr, &expErr) {
		if expErr.IsSplittable() && len(req.Timeseries) > 1 {
			mid := len(req.Timeseries) / 2
			req1 := &prw.WriteRequest{Timeseries: req.Timeseries[:mid], Metadata: req.Metadata}
			req2 := &prw.WriteRequest{Timeseries: req.Timeseries[mid:], Metadata: req.Metadata}
			data1, _ := req1.Marshal()
			data2, _ := req2.Marshal()
			_ = qe.queue.PushData(data1)
			_ = qe.queue.PushData(data2)
			logging.Info("PRW retry failed with splittable error, split and re-queued", logging.F(
				"original_timeseries", len(req.Timeseries),
				"queue_size", qe.queue.Len(),
			))
			return false
		}
		if !expErr.IsRetryable() {
			// Drop non-retryable errors
			logging.Warn("PRW dropping non-retryable queued entry", logging.F(
				"error", exportErr.Error(),
				"error_type", string(expErr.Type),
				"timeseries", len(req.Timeseries),
			))
			return false
		}
	}

	// Re-push to queue for later retry
	if pushErr := qe.queue.PushData(entry.Data); pushErr != nil {
		logging.Error("PRW failed to re-queue failed entry", logging.F("error", pushErr.Error()))
	}

	logging.Info("PRW retry failed", logging.F(
		"error", exportErr.Error(),
		"error_type", string(errType),
		"timeseries", len(req.Timeseries),
		"queue_size", qe.queue.Len(),
		"circuit_state", qe.getCircuitState(),
	))
	return false
}

// classifyPRWError categorizes a PRW error for metrics.
func classifyPRWError(err error) ErrorType {
	if err == nil {
		return ErrorTypeUnknown
	}

	// Check for PRW-specific error types
	if clientErr, ok := err.(*PRWClientError); ok {
		return classifyHTTPStatusCode(clientErr.StatusCode)
	}
	if serverErr, ok := err.(*PRWServerError); ok {
		return classifyHTTPStatusCode(serverErr.StatusCode)
	}

	// Fall back to generic error classification
	errStr := err.Error()

	// Check for timeout patterns
	if containsStr(errStr, "timeout") ||
		containsStr(errStr, "deadline exceeded") ||
		containsStr(errStr, "context deadline") {
		return ErrorTypeTimeout
	}

	// Check for network patterns
	if containsStr(errStr, "connection refused") ||
		containsStr(errStr, "no such host") ||
		containsStr(errStr, "network is unreachable") ||
		containsStr(errStr, "connection reset") ||
		containsStr(errStr, "broken pipe") ||
		containsStr(errStr, "EOF") ||
		containsStr(errStr, "i/o timeout") {
		return ErrorTypeNetwork
	}

	return ErrorTypeUnknown
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
	logging.Info("PRW draining queue", logging.F("queue_size", qe.queue.Len()))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var failedEntries [][]byte

	for {
		select {
		case <-ctx.Done():
			logging.Warn("PRW timeout draining queue", logging.F("remaining", qe.queue.Len()))
			for _, data := range failedEntries {
				_ = qe.queue.PushData(data)
			}
			return
		default:
		}

		entry, err := qe.queue.Pop()
		if err != nil || entry == nil {
			break
		}

		req, err := prw.UnmarshalWriteRequest(entry.Data)
		if err != nil {
			continue // Skip corrupted entries
		}

		exportCtx, exportCancel := context.WithTimeout(ctx, 5*time.Second)
		err = qe.exporter.Export(exportCtx, req)
		exportCancel()

		if err != nil {
			failedEntries = append(failedEntries, entry.Data)
			logging.Warn("PRW drain failed, will persist for recovery", logging.F(
				"error", err.Error(),
				"timeseries", len(req.Timeseries),
			))
		} else {
			prwRetrySuccessTotal.Inc()
		}
	}

	// Re-push failed entries for persistence
	for _, data := range failedEntries {
		_ = qe.queue.PushData(data)
	}
}

// Close stops the retry loop and closes the underlying exporter.
func (qe *PRWQueuedExporter) Close() error {
	var closeErr error
	qe.closedOnce.Do(func() {
		qe.mu.Lock()
		qe.closed = true
		qe.mu.Unlock()

		// Signal retry loop to stop
		close(qe.retryStop)

		// Wait for retry loop to finish (with timeout)
		select {
		case <-qe.retryDone:
		case <-time.After(30 * time.Second):
			logging.Warn("PRW timeout waiting for retry loop to finish")
		}

		// Close the queue
		if err := qe.queue.Close(); err != nil {
			logging.Error("PRW failed to close queue", logging.F("error", err.Error()))
		}

		// Close underlying exporter
		closeErr = qe.exporter.Close()
	})
	return closeErr
}

// QueueSize returns the current number of items in the queue.
func (qe *PRWQueuedExporter) QueueSize() int {
	return qe.queue.Len()
}

// minDuration returns the smaller of two durations.
func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
