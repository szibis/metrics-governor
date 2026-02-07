package exporter

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/szibis/metrics-governor/internal/logging"
	"github.com/szibis/metrics-governor/internal/pipeline"
	"github.com/szibis/metrics-governor/internal/queue"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
)

// ErrExportQueued is a sentinel error indicating data was queued for retry
// rather than exported directly. Callers can use errors.Is to distinguish
// "exported" from "queued" for accurate metrics tracking.
var ErrExportQueued = errors.New("export queued for retry")

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
	halfOpenProbe    atomic.Int32 // 1 if a half-open probe is in flight, 0 otherwise

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
			// CAS: only one goroutine wins the Open → HalfOpen transition.
			// This goroutine also becomes the half-open probe.
			if cb.state.CompareAndSwap(int32(CircuitOpen), int32(CircuitHalfOpen)) {
				cb.halfOpenProbe.Store(1) // This goroutine is the probe
				cb.lastStateChange.Store(time.Now().Unix())
				queue.SetCircuitState("half_open")
				logging.Info("circuit breaker transitioning to half-open", logging.F(
					"reset_timeout", cb.resetTimeout.String(),
				))
				return true
			}
			// Another goroutine already transitioned — reject this one
			return false
		}
		return false
	case CircuitHalfOpen:
		// Only allow one probe request in half-open state.
		// CAS: if no probe is in flight (0→1), this goroutine becomes the probe.
		// All other goroutines are rejected → their data gets queued.
		if cb.halfOpenProbe.CompareAndSwap(0, 1) {
			return true
		}
		return false
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
		cb.halfOpenProbe.Store(0)
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
		cb.halfOpenProbe.Store(0)
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
// When AlwaysQueue is true (default), Export() always pushes to the queue
// (returns instantly in µs) and N worker goroutines pull from the queue
// and export concurrently. This prevents slow destinations from blocking
// the buffer flush path.
type QueuedExporter struct {
	exporter       Exporter
	queue          *queue.SendQueue
	baseDelay      time.Duration
	maxDelay       time.Duration
	circuitBreaker *CircuitBreaker

	// Always-queue mode: when true, Export() pushes to queue, workers export.
	alwaysQueue bool
	// Number of worker goroutines for always-queue mode.
	workers int

	// Direct export timeout — prevents slow destinations from blocking flush goroutines.
	// Only used in legacy (non-always-queue) mode.
	directExportTimeout time.Duration

	// Backoff configuration
	backoffEnabled    bool
	backoffMultiplier float64

	// Drain configuration
	batchDrainSize     int           // Entries processed per retry tick (default: 10)
	burstDrainSize     int           // Entries drained on recovery (default: 100)
	retryExportTimeout time.Duration // Per-retry export timeout (default: 10s)
	closeTimeout       time.Duration // Close() wait for retry loop (default: 60s)
	drainTimeout       time.Duration // drainQueue overall timeout (default: 30s)
	drainEntryTimeout  time.Duration // Per-entry timeout during drain (default: 5s)

	// Backoff state
	currentDelay atomic.Int64 // Current backoff delay in nanoseconds

	retryStop chan struct{}
	retryDone chan struct{}
	// workersDone is closed when all worker goroutines have exited (always-queue mode).
	workersDone chan struct{}

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

	// Apply defaults for drain settings
	batchDrainSize := queueCfg.BatchDrainSize
	if batchDrainSize <= 0 {
		batchDrainSize = 10
	}
	burstDrainSize := queueCfg.BurstDrainSize
	if burstDrainSize <= 0 {
		burstDrainSize = 100
	}
	retryExportTimeout := queueCfg.RetryExportTimeout
	if retryExportTimeout <= 0 {
		retryExportTimeout = 10 * time.Second
	}
	closeTimeout := queueCfg.CloseTimeout
	if closeTimeout <= 0 {
		closeTimeout = 60 * time.Second
	}
	drainTimeout := queueCfg.DrainTimeout
	if drainTimeout <= 0 {
		drainTimeout = 30 * time.Second
	}
	drainEntryTimeout := queueCfg.DrainEntryTimeout
	if drainEntryTimeout <= 0 {
		drainEntryTimeout = 5 * time.Second
	}

	// Direct export timeout: fail fast on slow destinations to trigger CB → queue path
	directExportTimeout := queueCfg.DirectExportTimeout
	// 0 means disabled (caller must explicitly set)

	// Worker count: default to NumCPU (I/O-bound proxy)
	workers := queueCfg.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	qe := &QueuedExporter{
		exporter:            exporter,
		queue:               q,
		baseDelay:           queueCfg.RetryInterval,
		maxDelay:            queueCfg.MaxRetryDelay,
		directExportTimeout: directExportTimeout,
		alwaysQueue:         queueCfg.AlwaysQueue,
		workers:             workers,
		backoffEnabled:      backoffEnabled,
		backoffMultiplier:   backoffMultiplier,
		batchDrainSize:      batchDrainSize,
		burstDrainSize:      burstDrainSize,
		retryExportTimeout:  retryExportTimeout,
		closeTimeout:        closeTimeout,
		drainTimeout:        drainTimeout,
		drainEntryTimeout:   drainEntryTimeout,
		retryStop:           make(chan struct{}),
		retryDone:           make(chan struct{}),
	}

	// Initialize current delay to base delay
	qe.currentDelay.Store(int64(queueCfg.RetryInterval))

	// Initialize circuit breaker if enabled
	if queueCfg.CircuitBreakerEnabled {
		threshold := queueCfg.CircuitBreakerThreshold
		if threshold <= 0 {
			threshold = 5 // Default threshold
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

	if qe.alwaysQueue {
		// Always-queue mode: start N worker goroutines that pull from queue
		logging.Info("always-queue mode enabled with worker pool", logging.F(
			"workers", workers,
		))
		qe.workersDone = make(chan struct{})
		qe.startWorkers()
	} else {
		// Legacy mode: single retry loop
		if directExportTimeout > 0 {
			logging.Info("direct export timeout enabled", logging.F(
				"timeout", directExportTimeout.String(),
			))
		}

		if backoffEnabled {
			logging.Info("exponential backoff enabled", logging.F(
				"multiplier", backoffMultiplier,
				"base_delay", queueCfg.RetryInterval.String(),
				"max_delay", queueCfg.MaxRetryDelay.String(),
			))
		}

		// Start the single retry loop (legacy mode)
		go qe.retryLoop()
	}

	return qe, nil
}

// Export sends data for export. In always-queue mode, data is pushed to the queue
// instantly (µs) and workers handle the actual export. In legacy mode, data is
// exported directly with fallback to the queue on failure.
func (e *QueuedExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	if e.alwaysQueue {
		// Always-queue mode: push to queue, workers will export.
		// This returns in microseconds regardless of destination speed.
		//
		// Count datapoints at push time since the fast-path worker
		// (ExportData) sends raw bytes without deserializing.
		datapoints := countDatapoints(req)
		start := time.Now()
		if err := e.queue.Push(req); err != nil {
			return fmt.Errorf("queue push failed: %w", err)
		}
		pipeline.Record("queue_push", pipeline.Since(start))
		// Queuing IS the success path — return nil, not ErrExportQueued.
		// The buffer layer doesn't need to distinguish queued vs exported.
		recordExportSuccess(datapoints)
		return nil
	}

	// Legacy mode: try direct export, queue on failure
	return e.legacyExport(ctx, req)
}

// legacyExport implements the original try-direct/queue-on-failure behavior.
func (e *QueuedExporter) legacyExport(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	// Check circuit breaker BEFORE attempting direct export
	if e.circuitBreaker != nil && !e.circuitBreaker.AllowRequest() {
		queue.IncrementCircuitRejected()
		if queueErr := e.queue.Push(req); queueErr != nil {
			return fmt.Errorf("circuit open and queue push failed: %w", queueErr)
		}
		return ErrExportQueued
	}

	// Circuit closed or half-open — try direct export.
	// Wrap with directExportTimeout to fail fast on slow destinations,
	// preventing flush goroutines from blocking for the full exporter timeout.
	exportCtx := ctx
	if e.directExportTimeout > 0 {
		var cancel context.CancelFunc
		exportCtx, cancel = context.WithTimeout(ctx, e.directExportTimeout)
		defer cancel()
	}

	err := e.exporter.Export(exportCtx, req)
	if err == nil {
		if e.circuitBreaker != nil {
			e.circuitBreaker.RecordSuccess()
		}
		return nil
	}

	// Track direct export timeouts specifically
	if e.directExportTimeout > 0 && exportCtx.Err() == context.DeadlineExceeded {
		queue.IncrementDirectExportTimeout()
	}

	// Record failure with circuit breaker
	if e.circuitBreaker != nil {
		e.circuitBreaker.RecordFailure()
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

	// Data is safe in queue — return sentinel so callers can track accurately
	return ErrExportQueued
}

// Close stops workers/retry loop and closes the queue.
func (e *QueuedExporter) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	e.mu.Unlock()

	// Signal workers/retry loop to stop
	close(e.retryStop)

	if e.alwaysQueue {
		// Wait for all workers to finish
		select {
		case <-e.workersDone:
		case <-time.After(e.closeTimeout):
			logging.Warn("timeout waiting for workers to finish", logging.F(
				"close_timeout", e.closeTimeout.String(),
			))
		}

		// Drain remaining queue entries best-effort
		e.drainQueue()
	} else {
		// Wait for retry loop to finish (includes drain) before closing queue
		select {
		case <-e.retryDone:
		case <-time.After(e.closeTimeout):
			logging.Warn("timeout waiting for retry loop to finish", logging.F(
				"close_timeout", e.closeTimeout.String(),
			))
		}
	}

	// Safe to close queue now — drain is done or timed out
	if err := e.queue.Close(); err != nil {
		logging.Error("failed to close queue", logging.F("error", err.Error()))
	}

	// Close underlying exporter
	return e.exporter.Close()
}

// startWorkers launches N worker goroutines that pull from the queue and export.
func (e *QueuedExporter) startWorkers() {
	var wg sync.WaitGroup
	wg.Add(e.workers)
	queue.SetWorkersTotal(float64(e.workers))

	for i := 0; i < e.workers; i++ {
		go func(id int) {
			defer wg.Done()
			e.workerLoop(id)
		}(i)
	}

	go func() {
		wg.Wait()
		close(e.workersDone)
	}()
}

// dataExporter is an optional interface for exporters that can send pre-serialized
// proto bytes directly, skipping the unmarshal→remarshal roundtrip.
type dataExporter interface {
	ExportData(ctx context.Context, data []byte) error
}

// workerLoop is the main loop for each worker goroutine. Workers pull entries
// from the queue and export them. On failure, entries are re-pushed with backoff.
//
// Fast path (ExportData): When the exporter supports raw-bytes export, workers
// send queue entry bytes directly → compress → HTTP, skipping the costly
// proto.Unmarshal + proto.Marshal roundtrip (~147ms saved per request).
// Deserialization only happens on splittable errors (rare 413 responses).
func (e *QueuedExporter) workerLoop(id int) {
	// Per-worker backoff state
	currentBackoff := e.baseDelay
	if currentBackoff <= 0 {
		currentBackoff = 5 * time.Second
	}

	// Check once if exporter supports the raw-bytes fast path
	de, hasDataExport := e.exporter.(dataExporter)

	for {
		// Check for shutdown
		select {
		case <-e.retryStop:
			return
		default:
		}

		// Pop from queue (non-blocking)
		popStart := time.Now()
		entry, err := e.queue.Pop()
		pipeline.Record("queue_pop", pipeline.Since(popStart))
		if err != nil {
			logging.Error("worker: failed to pop queue", logging.F(
				"worker_id", id,
				"error", err.Error(),
			))
			// Back off on pop errors
			e.workerSleep(100 * time.Millisecond)
			continue
		}

		if entry == nil {
			// Queue empty — back off to avoid busy-loop
			e.workerSleep(100 * time.Millisecond)
			continue
		}

		// Check circuit breaker before export — use raw bytes for re-push
		if e.circuitBreaker != nil && !e.circuitBreaker.AllowRequest() {
			queue.IncrementCircuitRejected()
			_ = e.queue.PushData(entry.Data) // Re-push raw bytes, no unmarshal needed
			e.workerSleep(currentBackoff)
			continue
		}

		queue.IncrementRetryTotal()

		// Export with timeout — track workers actively exporting
		queue.IncrementWorkersActive()
		exportStart := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), e.retryExportTimeout)

		if hasDataExport {
			// Fast path: send raw proto bytes directly (skip unmarshal+remarshal)
			err = de.ExportData(ctx, entry.Data)
		} else {
			// Fallback: unmarshal and use standard Export
			deserStart := time.Now()
			var req *colmetricspb.ExportMetricsServiceRequest
			req, err = entry.GetRequest()
			pipeline.Record("serialize", pipeline.Since(deserStart))
			if err != nil {
				cancel()
				queue.DecrementWorkersActive()
				logging.Error("worker: failed to deserialize queued request", logging.F(
					"worker_id", id,
					"error", err.Error(),
				))
				continue
			}
			err = e.exporter.Export(ctx, req)
		}

		cancel()
		pipeline.Record("export_http", pipeline.Since(exportStart))
		queue.DecrementWorkersActive()

		if err == nil {
			// Success
			queue.IncrementRetrySuccessTotal()
			if e.circuitBreaker != nil {
				e.circuitBreaker.RecordSuccess()
			}
			// Reset backoff on success
			currentBackoff = e.baseDelay
			if currentBackoff <= 0 {
				currentBackoff = 5 * time.Second
			}
			continue
		}

		// Failure — record with circuit breaker
		if e.circuitBreaker != nil {
			e.circuitBreaker.RecordFailure()
		}

		// Classify error for metrics
		errType := classifyExportError(err)
		queue.IncrementRetryFailure(string(errType))

		// Check if error is splittable (payload too large) — requires deserialization
		var exportErr *ExportError
		if errors.As(err, &exportErr) {
			if exportErr.IsSplittable() {
				// Only unmarshal for splitting — this is the rare 413 case
				req, deserErr := entry.GetRequest()
				if deserErr == nil && len(req.ResourceMetrics) > 1 {
					mid := len(req.ResourceMetrics) / 2
					req1 := &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: req.ResourceMetrics[:mid]}
					req2 := &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: req.ResourceMetrics[mid:]}
					_ = e.queue.Push(req1)
					_ = e.queue.Push(req2)
					logging.Info("worker: split and re-queued", logging.F(
						"worker_id", id,
						"original_size", len(req.ResourceMetrics),
					))
					continue // No backoff needed — split may work at smaller size
				}
			}
			if !exportErr.IsRetryable() {
				logging.Warn("worker: dropping non-retryable entry", logging.F(
					"worker_id", id,
					"error", err.Error(),
					"error_type", string(exportErr.Type),
				))
				queue.IncrementNonRetryableDropped(string(exportErr.Type))
				continue
			}
		}

		// Re-push raw bytes for retry — no unmarshal needed
		if pushErr := e.queue.PushData(entry.Data); pushErr != nil {
			logging.Error("worker: failed to re-queue", logging.F(
				"worker_id", id,
				"error", pushErr.Error(),
			))
		}

		// Exponential backoff with jitter
		if e.backoffEnabled {
			e.workerSleep(currentBackoff)
			newBackoff := time.Duration(float64(currentBackoff) * e.backoffMultiplier)
			if newBackoff > e.maxDelay && e.maxDelay > 0 {
				newBackoff = e.maxDelay
			}
			currentBackoff = newBackoff
		} else {
			e.workerSleep(currentBackoff)
		}
	}
}

// workerSleep sleeps for the given duration but wakes up early on shutdown.
// Adds ±10% jitter to prevent thundering herd.
func (e *QueuedExporter) workerSleep(d time.Duration) {
	// Add jitter: ±10%
	jitter := time.Duration(float64(d) * 0.1 * (2*rand.Float64() - 1)) //nolint:gosec // jitter doesn't need crypto randomness
	d += jitter
	if d <= 0 {
		d = time.Millisecond
	}

	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-e.retryStop:
	case <-timer.C:
	}
}

// retryLoop processes queued entries in the background (legacy mode only).
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

			// Batch drain: process up to batchDrainSize entries per tick
			drained := 0
			overallSuccess := false
			for drained < e.batchDrainSize {
				success := e.processQueue()
				if !success {
					break // Stop on first failure
				}
				drained++
				overallSuccess = true
				if e.queue.Len() == 0 {
					break
				}
			}

			// If nothing was drained and queue is empty, count as success for backoff
			if drained == 0 && e.queue.Len() == 0 {
				overallSuccess = true
			}

			// Adjust backoff based on result
			if overallSuccess {
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
				// Burst drain on recovery: rapidly clear backlog
				if drained > 0 && e.queue.Len() > 0 {
					burstDrained := 0
					for burstDrained < e.burstDrainSize && e.queue.Len() > 0 {
						if !e.processQueue() {
							break
						}
						burstDrained++
					}
					if burstDrained > 0 {
						logging.Info("burst drain completed", logging.F(
							"drained", burstDrained,
							"queue_remaining", e.queue.Len(),
						))
					}
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

	ctx, cancel := context.WithTimeout(context.Background(), e.retryExportTimeout)
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

	// Classify the error for metrics
	errType := classifyExportError(err)
	queue.IncrementRetryFailure(string(errType))

	// Check if error is splittable (payload too large) -- split and re-queue halves
	var exportErr *ExportError
	if errors.As(err, &exportErr) {
		if exportErr.IsSplittable() && len(req.ResourceMetrics) > 1 {
			mid := len(req.ResourceMetrics) / 2
			req1 := &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: req.ResourceMetrics[:mid]}
			req2 := &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: req.ResourceMetrics[mid:]}
			_ = e.queue.Push(req1)
			_ = e.queue.Push(req2)
			logging.Info("retry failed with splittable error, split and re-queued", logging.F(
				"original_size", len(req.ResourceMetrics),
				"queue_size", e.queue.Len(),
			))
			return false
		}
		if !exportErr.IsRetryable() {
			// Drop non-retryable errors (e.g. auth) instead of infinite retry
			logging.Warn("dropping non-retryable queued entry", logging.F(
				"error", err.Error(),
				"error_type", string(exportErr.Type),
				"batch_size", len(req.ResourceMetrics),
			))
			queue.IncrementNonRetryableDropped(string(exportErr.Type))
			return false
		}
	}

	// Re-push to queue for later retry (retryable errors)
	// Note: This puts it at the back of the queue
	if pushErr := e.queue.Push(req); pushErr != nil {
		logging.Error("failed to re-queue failed entry", logging.F("error", pushErr.Error()))
	}
	logging.Info("retry failed", logging.F(
		"error", err.Error(),
		"error_type", string(errType),
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

// classifyExportError categorizes an export error for metrics.
// Uses the same error types as the main exporter for consistency.
// Lowercases the error string once to avoid repeated allocations.
func classifyExportError(err error) ErrorType {
	if err == nil {
		return ErrorTypeUnknown
	}

	errLower := strings.ToLower(err.Error())

	// Check for timeout patterns
	if strings.Contains(errLower, "timeout") ||
		strings.Contains(errLower, "deadline exceeded") ||
		strings.Contains(errLower, "context deadline") {
		return ErrorTypeTimeout
	}

	// Check for network patterns
	if strings.Contains(errLower, "connection refused") ||
		strings.Contains(errLower, "no such host") ||
		strings.Contains(errLower, "network is unreachable") ||
		strings.Contains(errLower, "connection reset") ||
		strings.Contains(errLower, "broken pipe") ||
		strings.Contains(errLower, "eof") ||
		strings.Contains(errLower, "i/o timeout") {
		return ErrorTypeNetwork
	}

	// Check for HTTP status codes in error message
	if strings.Contains(errLower, "status code: 401") ||
		strings.Contains(errLower, "status code: 403") ||
		strings.Contains(errLower, "unauthenticated") ||
		strings.Contains(errLower, "permissiondenied") {
		return ErrorTypeAuth
	}

	if strings.Contains(errLower, "status code: 429") ||
		strings.Contains(errLower, "resourceexhausted") {
		return ErrorTypeRateLimit
	}

	if strings.Contains(errLower, "status code: 5") ||
		strings.Contains(errLower, "internal") ||
		strings.Contains(errLower, "unavailable") {
		return ErrorTypeServerError
	}

	if strings.Contains(errLower, "status code: 4") {
		return ErrorTypeClientError
	}

	return ErrorTypeUnknown
}

// drainQueue attempts to export all remaining entries.
// Entries that fail to export are re-pushed to the queue for recovery on restart.
func (e *QueuedExporter) drainQueue() {
	logging.Info("draining queue", logging.F("queue_size", e.queue.Len()))

	ctx, cancel := context.WithTimeout(context.Background(), e.drainTimeout)
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

		exportCtx, exportCancel := context.WithTimeout(ctx, e.drainEntryTimeout)
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

// AlwaysQueue returns whether always-queue mode is enabled.
func (e *QueuedExporter) AlwaysQueue() bool {
	return e.alwaysQueue
}

// Workers returns the configured number of worker goroutines.
func (e *QueuedExporter) Workers() int {
	return e.workers
}
