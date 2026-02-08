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
	"github.com/szibis/metrics-governor/internal/prw"
	"github.com/szibis/metrics-governor/internal/queue"
	"golang.org/x/sync/semaphore"
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
	CircuitFailureThreshold int           // Failures before opening (default: 5)
	CircuitResetTimeout     time.Duration // Time to wait before half-open (default: 30s)
	// Direct export timeout — fail fast on slow destinations to trigger CB → queue.
	// Default: 0 (disabled; caller must set explicitly).
	DirectExportTimeout time.Duration
	// Drain settings
	BatchDrainSize     int           // Entries per retry tick (default: 10)
	BurstDrainSize     int           // Entries on recovery (default: 100)
	RetryExportTimeout time.Duration // Per-retry export timeout (default: 10s)
	CloseTimeout       time.Duration // Close() wait timeout (default: 60s)
	DrainTimeout       time.Duration // drainQueue overall timeout (default: 30s)
	DrainEntryTimeout  time.Duration // Per-entry drain timeout (default: 5s)
	// AlwaysQueue routes all data through the queue instead of trying direct export.
	AlwaysQueue bool
	// Workers is the number of worker goroutines (default: 2 × NumCPU).
	Workers int
	// Pipeline split settings
	PipelineSplitEnabled bool
	PreparerCount        int
	SenderCount          int
	PipelineChannelSize  int
	// Async send settings
	MaxConcurrentSends int
	GlobalSendLimit    int
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
		CircuitFailureThreshold: 5,
		CircuitResetTimeout:     30 * time.Second,
		AlwaysQueue:             true,
		Workers:                 0, // 0 = 2 × NumCPU
	}
}

// prwExporterInterface is an interface for PRW exporters to enable testing.
type prwExporterInterface interface {
	Export(ctx context.Context, req *prw.WriteRequest) error
	Close() error
}

// prwDataCompressor is an optional interface for PRW exporters that can compress raw bytes.
type prwDataCompressor interface {
	CompressData(data []byte) (compressed []byte, contentEncoding string, err error)
}

// prwCompressedSender is an optional interface for PRW exporters that can send pre-compressed data.
type prwCompressedSender interface {
	SendCompressed(ctx context.Context, compressedData []byte, contentEncoding string, uncompressedSize int) error
}

// PRWQueuedExporter wraps a PRWExporter with a persistent disk-backed retry queue.
type PRWQueuedExporter struct {
	exporter       prwExporterInterface
	queue          *queue.SendQueue
	baseDelay      time.Duration
	maxDelay       time.Duration
	circuitBreaker *CircuitBreaker

	// Always-queue mode
	alwaysQueue bool
	workers     int

	// Direct export timeout — prevents slow destinations from blocking flush goroutines.
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

	// Pipeline split: separate CPU-bound preparers from I/O-bound senders.
	pipelineSplitEnabled bool
	preparerCount        int
	senderCount          int
	preparedCh           chan *PreparedEntry

	// Async send: semaphore-bounded concurrent HTTP sends per sender.
	maxConcurrentSends int
	globalSendSem      *semaphore.Weighted

	// Adaptive worker scaling
	scaler *WorkerScaler

	// Batch tuner: AIMD batch size auto-tuning.
	batchTuner *BatchTuner

	// Worker management for dynamic scaling
	workerWg     sync.WaitGroup
	nextWorkerID atomic.Int32
	workerStops  sync.Map // map[int]chan struct{}

	retryStop   chan struct{}
	retryDone   chan struct{}
	workersDone chan struct{}
	mu          sync.Mutex
	closed      bool
	closedOnce  sync.Once
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

	// Apply defaults for drain settings
	batchDrainSize := cfg.BatchDrainSize
	if batchDrainSize <= 0 {
		batchDrainSize = 10
	}
	burstDrainSize := cfg.BurstDrainSize
	if burstDrainSize <= 0 {
		burstDrainSize = 100
	}
	retryExportTimeout := cfg.RetryExportTimeout
	if retryExportTimeout <= 0 {
		retryExportTimeout = 10 * time.Second
	}
	closeTimeout := cfg.CloseTimeout
	if closeTimeout <= 0 {
		closeTimeout = 60 * time.Second
	}
	drainTimeout := cfg.DrainTimeout
	if drainTimeout <= 0 {
		drainTimeout = 30 * time.Second
	}
	drainEntryTimeout := cfg.DrainEntryTimeout
	if drainEntryTimeout <= 0 {
		drainEntryTimeout = 5 * time.Second
	}

	// Worker count: default to NumCPU (I/O-bound proxy)
	workers := cfg.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	// Pipeline split configuration
	pipelineSplitEnabled := cfg.PipelineSplitEnabled
	preparerCount := cfg.PreparerCount
	if preparerCount <= 0 {
		preparerCount = runtime.NumCPU()
	}
	senderCount := cfg.SenderCount
	if senderCount <= 0 {
		senderCount = runtime.NumCPU() * 2
	}
	pipelineChannelSize := cfg.PipelineChannelSize
	if pipelineChannelSize <= 0 {
		pipelineChannelSize = 256
	}

	// Async send configuration
	maxConcurrentSends := cfg.MaxConcurrentSends
	if maxConcurrentSends <= 0 {
		maxConcurrentSends = 4
	}
	globalSendLimit := cfg.GlobalSendLimit
	if globalSendLimit <= 0 {
		globalSendLimit = runtime.NumCPU() * 8
	}

	qe := &PRWQueuedExporter{
		exporter:             exporter,
		queue:                q,
		baseDelay:            baseDelay,
		maxDelay:             maxDelay,
		directExportTimeout:  cfg.DirectExportTimeout,
		alwaysQueue:          cfg.AlwaysQueue,
		workers:              workers,
		backoffEnabled:       cfg.BackoffEnabled,
		backoffMultiplier:    backoffMultiplier,
		batchDrainSize:       batchDrainSize,
		burstDrainSize:       burstDrainSize,
		retryExportTimeout:   retryExportTimeout,
		closeTimeout:         closeTimeout,
		drainTimeout:         drainTimeout,
		drainEntryTimeout:    drainEntryTimeout,
		pipelineSplitEnabled: pipelineSplitEnabled,
		preparerCount:        preparerCount,
		senderCount:          senderCount,
		maxConcurrentSends:   maxConcurrentSends,
		globalSendSem:        semaphore.NewWeighted(int64(globalSendLimit)),
		retryStop:            make(chan struct{}),
		retryDone:            make(chan struct{}),
	}

	if cfg.DirectExportTimeout > 0 {
		logging.Info("PRW direct export timeout enabled", logging.F(
			"timeout", cfg.DirectExportTimeout.String(),
		))
	}

	// Initialize circuit breaker if enabled
	if cfg.CircuitBreakerEnabled {
		threshold := cfg.CircuitFailureThreshold
		if threshold == 0 {
			threshold = 5
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

	if qe.alwaysQueue {
		qe.workersDone = make(chan struct{})

		if pipelineSplitEnabled {
			qe.preparedCh = make(chan *PreparedEntry, pipelineChannelSize)
			logging.Info("PRW pipeline split mode enabled", logging.F(
				"preparers", preparerCount,
				"senders", senderCount,
				"channel_size", pipelineChannelSize,
				"max_concurrent_sends", maxConcurrentSends,
			))
			qe.startPipelineSplitWorkers()
		} else {
			logging.Info("PRW always-queue mode enabled with worker pool", logging.F(
				"workers", workers,
			))
			qe.startWorkers()
		}
	} else {
		// Start legacy retry loop
		go qe.retryLoop()
	}

	return qe, nil
}

// Export sends the PRW request. In always-queue mode, data is pushed to the queue
// instantly and workers handle the actual export.
func (qe *PRWQueuedExporter) Export(ctx context.Context, req *prw.WriteRequest) error {
	if req == nil || len(req.Timeseries) == 0 {
		return nil
	}

	if qe.alwaysQueue {
		// Count timeseries/samples at push time since the fast-path worker
		// (ExportData) sends raw bytes without deserializing.
		timeseriesCount := len(req.Timeseries)
		samplesCount := countPRWSamples(req)

		marshalStart := time.Now()
		data, marshalErr := req.Marshal()
		pipeline.Record("serialize", pipeline.Since(marshalStart))
		if marshalErr != nil {
			return fmt.Errorf("marshal failed: %w", marshalErr)
		}
		pipeline.RecordBytes("serialize", len(data))
		pushStart := time.Now()
		if pushErr := qe.queue.PushData(data); pushErr != nil {
			return fmt.Errorf("queue push failed: %w", pushErr)
		}
		pipeline.Record("queue_push", pipeline.Since(pushStart))
		pipeline.RecordBytes("queue_push", len(data))

		// Record metrics at push time (workers won't have deserialized request)
		prwExportTimeseriesTotal.Add(float64(timeseriesCount))
		prwExportSamplesTotal.Add(float64(samplesCount))
		return nil
	}

	return qe.legacyExport(ctx, req)
}

// legacyExport implements the original try-direct/queue-on-failure behavior.
func (qe *PRWQueuedExporter) legacyExport(ctx context.Context, req *prw.WriteRequest) error {
	// Check circuit breaker BEFORE attempting direct export
	if qe.circuitBreaker != nil && !qe.circuitBreaker.AllowRequest() {
		queue.IncrementCircuitRejected()
		data, marshalErr := req.Marshal()
		if marshalErr != nil {
			return fmt.Errorf("circuit open and marshal failed: %w", marshalErr)
		}
		if pushErr := qe.queue.PushData(data); pushErr != nil {
			return fmt.Errorf("circuit open and queue push failed: %w", pushErr)
		}
		return ErrExportQueued
	}

	// Circuit closed or half-open — try direct export.
	exportCtx := ctx
	if qe.directExportTimeout > 0 {
		var cancel context.CancelFunc
		exportCtx, cancel = context.WithTimeout(ctx, qe.directExportTimeout)
		defer cancel()
	}

	err := qe.exporter.Export(exportCtx, req)
	if err == nil {
		if qe.circuitBreaker != nil {
			qe.circuitBreaker.RecordSuccess()
		}
		return nil
	}

	// Track direct export timeouts specifically
	if qe.directExportTimeout > 0 && exportCtx.Err() == context.DeadlineExceeded {
		queue.IncrementDirectExportTimeout()
	}

	// Record failure with circuit breaker
	if qe.circuitBreaker != nil {
		qe.circuitBreaker.RecordFailure()
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

	return ErrExportQueued
}

// startWorkers launches N unified worker goroutines for PRW export.
func (qe *PRWQueuedExporter) startWorkers() {
	for i := 0; i < qe.workers; i++ {
		qe.workerWg.Add(1)
		id := int(qe.nextWorkerID.Add(1))
		go func() {
			defer qe.workerWg.Done()
			qe.workerLoop(id)
		}()
	}

	go func() {
		qe.workerWg.Wait()
		close(qe.workersDone)
	}()
}

// startPipelineSplitWorkers launches preparer and sender goroutines for PRW.
func (qe *PRWQueuedExporter) startPipelineSplitWorkers() {
	queue.SetPreparersTotal(float64(qe.preparerCount))
	queue.SetSendersTotal(float64(qe.senderCount))

	var preparerWg sync.WaitGroup
	preparerWg.Add(qe.preparerCount)
	for i := 0; i < qe.preparerCount; i++ {
		id := i
		go func() {
			defer preparerWg.Done()
			qe.prwPreparerLoop(id)
		}()
	}

	for i := 0; i < qe.senderCount; i++ {
		qe.workerWg.Add(1)
		id := int(qe.nextWorkerID.Add(1))
		stopCh := make(chan struct{})
		qe.workerStops.Store(id, stopCh)
		go func() {
			defer qe.workerWg.Done()
			qe.prwSenderLoop(id, stopCh)
		}()
	}

	go func() {
		preparerWg.Wait()
		close(qe.preparedCh)
		qe.workerWg.Wait()
		close(qe.workersDone)
	}()
}

// prwPreparerLoop pops from queue → compresses → pushes to preparedCh.
//
//nolint:dupl // Mirrors OTLP preparerLoop — intentional pipeline parity
func (qe *PRWQueuedExporter) prwPreparerLoop(id int) {
	dc, hasCompress := qe.exporter.(prwDataCompressor)

	for {
		select {
		case <-qe.retryStop:
			return
		default:
		}

		popStart := time.Now()
		entry, err := qe.queue.Pop()
		pipeline.Record("queue_pop", pipeline.Since(popStart))
		if err != nil {
			qe.workerSleep(100 * time.Millisecond)
			continue
		}
		if entry == nil {
			qe.workerSleep(100 * time.Millisecond)
			continue
		}

		if qe.circuitBreaker != nil && !qe.circuitBreaker.AllowRequest() {
			queue.IncrementCircuitRejected()
			_ = qe.queue.PushData(entry.Data)
			qe.workerSleep(100 * time.Millisecond)
			continue
		}

		queue.IncrementPreparersActive()
		prepareStart := time.Now()

		var prepared *PreparedEntry
		if hasCompress {
			compressed, encoding, compErr := dc.CompressData(entry.Data)
			if compErr != nil {
				queue.DecrementPreparersActive()
				logging.Error("PRW preparer: compression failed", logging.F(
					"preparer_id", id,
					"error", compErr.Error(),
				))
				_ = qe.queue.PushData(entry.Data)
				continue
			}
			prepared = &PreparedEntry{
				CompressedData:   compressed,
				RawData:          entry.Data,
				ContentEncoding:  encoding,
				UncompressedSize: len(entry.Data),
			}
		} else {
			prepared = &PreparedEntry{
				CompressedData:   entry.Data,
				RawData:          entry.Data,
				ContentEncoding:  "",
				UncompressedSize: len(entry.Data),
			}
		}

		pipeline.Record("prepare", pipeline.Since(prepareStart))
		queue.DecrementPreparersActive()

		select {
		case qe.preparedCh <- prepared:
			queue.SetPreparedChannelLength(float64(len(qe.preparedCh)))
		case <-qe.retryStop:
			_ = qe.queue.PushData(entry.Data)
			return
		}
	}
}

// prwSenderLoop pops from preparedCh → HTTP send → re-queue on failure.
func (qe *PRWQueuedExporter) prwSenderLoop(id int, stopCh chan struct{}) {
	cs, hasCompressedSend := qe.exporter.(prwCompressedSender)
	de, hasDataExport := qe.exporter.(prwDataExporter)
	currentBackoff := qe.baseDelay
	if currentBackoff <= 0 {
		currentBackoff = 5 * time.Second
	}

	for {
		select {
		case <-qe.retryStop:
			return
		case <-stopCh:
			return
		default:
		}

		var prepared *PreparedEntry
		var ok bool
		select {
		case prepared, ok = <-qe.preparedCh:
			if !ok {
				return
			}
		case <-qe.retryStop:
			return
		case <-stopCh:
			return
		}

		queue.SetPreparedChannelLength(float64(len(qe.preparedCh)))

		if err := qe.globalSendSem.Acquire(context.Background(), 1); err != nil {
			_ = qe.queue.PushData(prepared.RawData)
			continue
		}

		queue.IncrementSendersActive()
		prwRetryTotal.Inc()
		exportStart := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), qe.retryExportTimeout)

		var exportErr error
		usedFastPath := false

		if hasCompressedSend {
			exportErr = cs.SendCompressed(ctx, prepared.CompressedData, prepared.ContentEncoding, prepared.UncompressedSize)
			if errors.Is(exportErr, ErrExtraLabelsRequireDeserialize) {
				// Extra labels configured — fall through to slow path
				hasCompressedSend = false
				cs = nil
				exportErr = nil
			} else {
				usedFastPath = true
			}
		}

		if !usedFastPath && hasDataExport {
			exportErr = de.ExportData(ctx, prepared.RawData)
			if errors.Is(exportErr, ErrExtraLabelsRequireDeserialize) {
				hasDataExport = false
				de = nil
				exportErr = nil
			} else {
				usedFastPath = true
			}
		}

		if !usedFastPath {
			req, deserErr := prw.UnmarshalWriteRequest(prepared.RawData)
			if deserErr != nil {
				cancel()
				queue.DecrementSendersActive()
				qe.globalSendSem.Release(1)
				logging.Error("PRW sender: failed to deserialize", logging.F("sender_id", id, "error", deserErr.Error()))
				continue
			}
			exportErr = qe.exporter.Export(ctx, req)
		}

		cancel()
		sendDuration := pipeline.Since(exportStart)
		pipeline.Record("send", sendDuration)
		queue.DecrementSendersActive()
		qe.globalSendSem.Release(1)

		if exportErr == nil {
			if qe.circuitBreaker != nil {
				qe.circuitBreaker.RecordSuccess()
			}
			prwRetrySuccessTotal.Inc()
			if qe.scaler != nil {
				qe.scaler.RecordLatency(time.Duration(sendDuration))
			}
			if qe.batchTuner != nil {
				qe.batchTuner.RecordSuccess(time.Duration(sendDuration), prepared.UncompressedSize, len(prepared.CompressedData))
			}
			currentBackoff = qe.baseDelay
			if currentBackoff <= 0 {
				currentBackoff = 5 * time.Second
			}
			continue
		}

		// Failure
		if qe.circuitBreaker != nil {
			qe.circuitBreaker.RecordFailure()
		}
		if qe.batchTuner != nil {
			qe.batchTuner.RecordFailure(exportErr)
		}

		errType := classifyPRWError(exportErr)
		prwRetryFailureTotal.WithLabelValues(string(errType)).Inc()

		var expErr *ExportError
		if errors.As(exportErr, &expErr) {
			if expErr.IsSplittable() {
				req, deserErr := prw.UnmarshalWriteRequest(prepared.RawData)
				if deserErr == nil && len(req.Timeseries) > 1 {
					mid := len(req.Timeseries) / 2
					req1 := &prw.WriteRequest{Timeseries: req.Timeseries[:mid], Metadata: req.Metadata}
					req2 := &prw.WriteRequest{Timeseries: req.Timeseries[mid:], Metadata: req.Metadata}
					data1, _ := req1.Marshal()
					data2, _ := req2.Marshal()
					_ = qe.queue.PushData(data1)
					_ = qe.queue.PushData(data2)
					continue
				}
			}
			if !expErr.IsRetryable() {
				logging.Warn("PRW sender: dropping non-retryable entry", logging.F(
					"sender_id", id,
					"error", exportErr.Error(),
					"error_type", string(expErr.Type),
				))
				queue.IncrementNonRetryableDropped(string(expErr.Type))
				continue
			}
		}

		if pushErr := qe.queue.PushData(prepared.RawData); pushErr != nil {
			queue.IncrementExportDataLoss()
			logging.Error("PRW sender: re-push failed, data lost", logging.F(
				"sender_id", id,
				"error", pushErr.Error(),
				"bytes", len(prepared.RawData),
			))
		}

		if qe.backoffEnabled {
			qe.workerSleep(currentBackoff)
			newBackoff := time.Duration(float64(currentBackoff) * qe.backoffMultiplier)
			if newBackoff > qe.maxDelay && qe.maxDelay > 0 {
				newBackoff = qe.maxDelay
			}
			currentBackoff = newBackoff
		} else {
			qe.workerSleep(currentBackoff)
		}
	}
}

// SetBatchTuner sets the batch tuner for AIMD batch size optimization.
func (qe *PRWQueuedExporter) SetBatchTuner(bt *BatchTuner) {
	qe.batchTuner = bt
}

// prwDataExporter is an optional interface for exporters that can send
// pre-serialized proto bytes directly, skipping unmarshal→remarshal.
type prwDataExporter interface {
	ExportData(ctx context.Context, data []byte) error
}

// workerLoop is the main loop for each PRW worker goroutine.
//
// Fast path: When the exporter supports ExportData (no extra labels),
// workers send queue entry bytes directly → compress → HTTP, skipping
// the costly unmarshal + remarshal roundtrip.
func (qe *PRWQueuedExporter) workerLoop(id int) {
	currentBackoff := qe.baseDelay
	if currentBackoff <= 0 {
		currentBackoff = 5 * time.Second
	}

	// Check once if exporter supports the raw-bytes fast path
	de, hasDataExport := qe.exporter.(prwDataExporter)

	for {
		select {
		case <-qe.retryStop:
			return
		default:
		}

		popStart := time.Now()
		entry, err := qe.queue.Pop()
		pipeline.Record("queue_pop", pipeline.Since(popStart))
		if err != nil {
			qe.workerSleep(100 * time.Millisecond)
			continue
		}
		if entry == nil {
			qe.workerSleep(100 * time.Millisecond)
			continue
		}

		// Check circuit breaker — use raw bytes for re-push
		if qe.circuitBreaker != nil && !qe.circuitBreaker.AllowRequest() {
			queue.IncrementCircuitRejected()
			_ = qe.queue.PushData(entry.Data)
			qe.workerSleep(currentBackoff)
			continue
		}

		prwRetryTotal.Inc()

		// Track workers actively exporting
		queue.IncrementWorkersActive()
		exportStart := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), qe.retryExportTimeout)

		var exportErr error
		usedFastPath := false

		if hasDataExport {
			// Fast path: send raw proto bytes directly (skip unmarshal+remarshal)
			exportErr = de.ExportData(ctx, entry.Data)
			if errors.Is(exportErr, ErrExtraLabelsRequireDeserialize) {
				// Extra labels configured — permanently disable fast path for this worker
				hasDataExport = false
				de = nil
				exportErr = nil
			} else {
				usedFastPath = true
			}
		}

		if !usedFastPath {
			// Slow path: full unmarshal + export
			deserStart := time.Now()
			req, deserErr := prw.UnmarshalWriteRequest(entry.Data)
			pipeline.Record("serialize", pipeline.Since(deserStart))
			if deserErr != nil {
				cancel()
				queue.DecrementWorkersActive()
				logging.Error("PRW worker: failed to deserialize", logging.F("worker_id", id, "error", deserErr.Error()))
				continue
			}
			exportErr = qe.exporter.Export(ctx, req)
		}

		cancel()
		exportDuration := pipeline.Since(exportStart)
		pipeline.Record("export_http", exportDuration)
		queue.DecrementWorkersActive()

		if exportErr == nil {
			if qe.circuitBreaker != nil {
				qe.circuitBreaker.RecordSuccess()
			}
			prwRetrySuccessTotal.Inc()
			if qe.scaler != nil {
				qe.scaler.RecordLatency(time.Duration(exportDuration))
			}
			if qe.batchTuner != nil {
				qe.batchTuner.RecordSuccess(time.Duration(exportDuration), len(entry.Data), len(entry.Data))
			}
			currentBackoff = qe.baseDelay
			if currentBackoff <= 0 {
				currentBackoff = 5 * time.Second
			}
			continue
		}

		// Failure
		if qe.circuitBreaker != nil {
			qe.circuitBreaker.RecordFailure()
		}
		if qe.batchTuner != nil {
			qe.batchTuner.RecordFailure(exportErr)
		}

		errType := classifyPRWError(exportErr)
		prwRetryFailureTotal.WithLabelValues(string(errType)).Inc()

		// Check if splittable — requires deserialization (rare 413 case)
		var expErr *ExportError
		if errors.As(exportErr, &expErr) {
			if expErr.IsSplittable() {
				req, deserErr := prw.UnmarshalWriteRequest(entry.Data)
				if deserErr == nil && len(req.Timeseries) > 1 {
					mid := len(req.Timeseries) / 2
					req1 := &prw.WriteRequest{Timeseries: req.Timeseries[:mid], Metadata: req.Metadata}
					req2 := &prw.WriteRequest{Timeseries: req.Timeseries[mid:], Metadata: req.Metadata}
					data1, _ := req1.Marshal()
					data2, _ := req2.Marshal()
					_ = qe.queue.PushData(data1)
					_ = qe.queue.PushData(data2)
					continue
				}
			}
			if !expErr.IsRetryable() {
				logging.Warn("PRW worker: dropping non-retryable entry", logging.F(
					"worker_id", id,
					"error", exportErr.Error(),
				))
				queue.IncrementNonRetryableDropped(string(expErr.Type))
				continue
			}
		}

		// Re-push raw bytes for retry
		_ = qe.queue.PushData(entry.Data)

		if qe.backoffEnabled {
			qe.workerSleep(currentBackoff)
			newBackoff := time.Duration(float64(currentBackoff) * qe.backoffMultiplier)
			if newBackoff > qe.maxDelay && qe.maxDelay > 0 {
				newBackoff = qe.maxDelay
			}
			currentBackoff = newBackoff
		} else {
			qe.workerSleep(currentBackoff)
		}
	}
}

// workerSleep sleeps with jitter and shutdown awareness.
func (qe *PRWQueuedExporter) workerSleep(d time.Duration) {
	jitter := time.Duration(float64(d) * 0.1 * (2*rand.Float64() - 1)) //nolint:gosec // jitter doesn't need crypto randomness
	d += jitter
	if d <= 0 {
		d = time.Millisecond
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-qe.retryStop:
	case <-timer.C:
	}
}

// retryLoop continuously retries queued requests (legacy mode).
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

			// Batch drain: process up to batchDrainSize entries per tick
			drained := 0
			overallSuccess := false
			for drained < qe.batchDrainSize {
				success := qe.processQueue()
				if !success {
					break
				}
				drained++
				overallSuccess = true
				if qe.queue.Len() == 0 {
					break
				}
			}

			// If nothing was drained and queue is empty, count as success
			if drained == 0 && qe.queue.Len() == 0 {
				overallSuccess = true
			}

			if overallSuccess {
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
				// Burst drain on recovery
				if drained > 0 && qe.queue.Len() > 0 {
					burstDrained := 0
					for burstDrained < qe.burstDrainSize && qe.queue.Len() > 0 {
						if !qe.processQueue() {
							break
						}
						burstDrained++
					}
					if burstDrained > 0 {
						logging.Info("PRW burst drain completed", logging.F(
							"drained", burstDrained,
							"queue_remaining", qe.queue.Len(),
						))
					}
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

	ctx, cancel := context.WithTimeout(context.Background(), qe.retryExportTimeout)
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
			queue.IncrementNonRetryableDropped(string(expErr.Type))
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

	ctx, cancel := context.WithTimeout(context.Background(), qe.drainTimeout)
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

		exportCtx, exportCancel := context.WithTimeout(ctx, qe.drainEntryTimeout)
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

		// Stop adaptive scaler first
		if qe.scaler != nil {
			qe.scaler.Stop()
		}

		// Signal workers/retry loop to stop
		close(qe.retryStop)

		if qe.alwaysQueue {
			select {
			case <-qe.workersDone:
			case <-time.After(qe.closeTimeout):
				logging.Warn("PRW timeout waiting for workers to finish", logging.F(
					"close_timeout", qe.closeTimeout.String(),
				))
			}
			qe.drainQueue()
		} else {
			// Wait for retry loop to finish (includes drain) before closing queue
			select {
			case <-qe.retryDone:
			case <-time.After(qe.closeTimeout):
				logging.Warn("PRW timeout waiting for retry loop to finish", logging.F(
					"close_timeout", qe.closeTimeout.String(),
				))
			}
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
