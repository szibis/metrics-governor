package buffer

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/szibis/metrics-governor/internal/exporter"
	"github.com/szibis/metrics-governor/internal/logging"
	"github.com/szibis/metrics-governor/internal/pipeline"
	"github.com/szibis/metrics-governor/internal/queue"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// ErrBufferFull is returned when the buffer's byte capacity is exceeded
// and the full policy is "reject". Receivers should return 429/ResourceExhausted.
var ErrBufferFull = errors.New("buffer capacity exceeded")

var (
	exportRetrySplitTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_export_retry_split_total",
		Help: "Total number of split-on-error retries",
	})

	failoverQueuePushTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_failover_queue_push_total",
		Help: "Total number of batches saved to failover queue on export failure",
	})

	failoverQueueDrainTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_failover_queue_drain_total",
		Help: "Total number of batches successfully drained from failover queue",
	})

	failoverQueueDrainErrorsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_failover_queue_drain_errors_total",
		Help: "Total number of failover queue drain errors",
	})

	bufferBytesGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_buffer_bytes",
		Help: "Current buffer memory usage in bytes",
	})

	bufferMaxBytesGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_buffer_max_bytes",
		Help: "Configured buffer capacity limit in bytes",
	})

	bufferRejectedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_buffer_rejected_total",
		Help: "Total number of batches rejected due to buffer capacity (full policy: reject)",
	})

	bufferEvictionsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_buffer_evictions_total",
		Help: "Total number of entries evicted due to buffer capacity (full policy: drop_oldest)",
	})

	exportDataLossTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_export_data_loss_total",
		Help: "Batches permanently lost (export failed AND failover queue push failed)",
	})
)

func init() {
	prometheus.MustRegister(exportRetrySplitTotal)
	prometheus.MustRegister(failoverQueuePushTotal)
	prometheus.MustRegister(failoverQueueDrainTotal)
	prometheus.MustRegister(failoverQueueDrainErrorsTotal)
	prometheus.MustRegister(bufferBytesGauge)
	prometheus.MustRegister(bufferMaxBytesGauge)
	prometheus.MustRegister(bufferRejectedTotal)
	prometheus.MustRegister(bufferEvictionsTotal)
	prometheus.MustRegister(exportDataLossTotal)

	exportRetrySplitTotal.Add(0)
	failoverQueuePushTotal.Add(0)
	failoverQueueDrainTotal.Add(0)
	failoverQueueDrainErrorsTotal.Add(0)
	bufferBytesGauge.Set(0)
	bufferMaxBytesGauge.Set(0)
	bufferRejectedTotal.Add(0)
	bufferEvictionsTotal.Add(0)
	exportDataLossTotal.Add(0)
}

// Exporter defines the interface for sending metrics.
type Exporter interface {
	Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error
}

// StatsCollector defines the interface for collecting stats.
type StatsCollector interface {
	Process(resourceMetrics []*metricspb.ResourceMetrics)
	RecordReceived(count int)
	RecordExport(datapointCount int)
	RecordExportError()
	RecordOTLPBytesReceived(bytes int)
	RecordOTLPBytesReceivedCompressed(bytes int)
	RecordOTLPBytesSent(bytes int)
	SetOTLPBufferSize(size int)
}

// LimitsEnforcer defines the interface for enforcing limits.
type LimitsEnforcer interface {
	Process(resourceMetrics []*metricspb.ResourceMetrics) []*metricspb.ResourceMetrics
}

// MetricSampler defines the interface for sampling metrics before limits.
type MetricSampler interface {
	Sample(resourceMetrics []*metricspb.ResourceMetrics) []*metricspb.ResourceMetrics
}

// MetricProcessor is an alias for MetricSampler — the new name for the processing stage.
type MetricProcessor = MetricSampler

// TenantProcessor detects tenants and applies per-tenant quotas.
// It runs before the LimitsEnforcer in the pipeline.
type TenantProcessor interface {
	// ProcessBatch detects tenants in a batch and applies quotas.
	// Returns the surviving metrics after quota enforcement.
	// headerTenant is the tenant extracted from the request header (empty if not applicable).
	ProcessBatch(resourceMetrics []*metricspb.ResourceMetrics, headerTenant string) []*metricspb.ResourceMetrics
}

// LogAggregator aggregates similar log messages.
type LogAggregator interface {
	Error(key string, message string, fields map[string]interface{}, datapoints int64)
	Stop()
}

// MetricRelabeler defines the interface for relabeling metrics before export.
type MetricRelabeler interface {
	Relabel(resourceMetrics []*metricspb.ResourceMetrics) []*metricspb.ResourceMetrics
}

// FailoverQueue is the interface for a queue used as safety net on export failure.
type FailoverQueue interface {
	Push(req *colmetricspb.ExportMetricsServiceRequest) error
	Pop() *colmetricspb.ExportMetricsServiceRequest
	Len() int
	Size() int64
}

// BufferOption is a functional option for MetricsBuffer.
type BufferOption func(*MetricsBuffer)

// WithMaxBatchBytes sets the maximum batch size in bytes. Batches exceeding
// this limit are recursively split in half (VMAgent pattern).
func WithMaxBatchBytes(n int) BufferOption {
	return func(b *MetricsBuffer) { b.maxBatchBytes = n }
}

// WithConcurrency sets the number of concurrent export workers per flush cycle.
// Deprecated: In always-queue mode, concurrency is managed by the worker pool
// in QueuedExporter. This option is kept for backwards compatibility with
// non-always-queue configurations.
func WithConcurrency(n int) BufferOption {
	return func(b *MetricsBuffer) {
		b.concurrency = exporter.NewConcurrencyLimiter(n)
	}
}

// WithRelabeler sets a metric relabeler for the buffer. The relabeler is
// applied to each batch in exportBatch() before sending to the exporter.
func WithRelabeler(r MetricRelabeler) BufferOption {
	return func(b *MetricsBuffer) { b.relabeler = r }
}

// WithSampler sets a metric sampler for the buffer. The sampler is
// applied in Add() before limits enforcement.
func WithSampler(s MetricSampler) BufferOption {
	return func(b *MetricsBuffer) { b.sampler = s }
}

// WithProcessor sets a metric processor for the buffer.
// This is the preferred name for WithSampler.
func WithProcessor(p MetricProcessor) BufferOption {
	return WithSampler(p)
}

// WithTenantProcessor sets a tenant processor for the buffer. When set,
// it runs before limits enforcement to detect tenants and apply quotas.
func WithTenantProcessor(tp TenantProcessor) BufferOption {
	return func(b *MetricsBuffer) { b.tenantProcessor = tp }
}

// WithFailoverQueue sets a failover queue for the buffer. When all export
// attempts fail (including splitting), failed batches are pushed to this
// queue instead of being silently dropped.
func WithFailoverQueue(q FailoverQueue) BufferOption {
	return func(b *MetricsBuffer) { b.failoverQueue = q }
}

// BatchSizer returns the current effective max batch bytes.
// Used by BatchTuner for AIMD auto-tuning of batch sizes.
type BatchSizer interface {
	CurrentMaxBytes() int
}

// WithBatchTuner sets an AIMD batch tuner. When set, the flush cycle reads
// the tuner's CurrentMaxBytes() at the start of each flush and uses that
// value for batch splitting instead of the static maxBatchBytes.
func WithBatchTuner(sizer BatchSizer) BufferOption {
	return func(b *MetricsBuffer) { b.batchTuner = sizer }
}

// FusedTenantLimitsProcessor combines tenant and limits into a single pipeline step.
type FusedTenantLimitsProcessor interface {
	Process(resourceMetrics []*metricspb.ResourceMetrics, headerTenant string) []*metricspb.ResourceMetrics
}

// WithFusedProcessor sets a fused tenant+limits processor. When set, the
// separate tenant and limits steps are skipped in favor of this single call.
func WithFusedProcessor(fp FusedTenantLimitsProcessor) BufferOption {
	return func(b *MetricsBuffer) { b.fusedProcessor = fp }
}

// WithFlushTimeout sets the maximum time for a flush cycle. If exceeded,
// the flush context is canceled — ongoing exports see ctx.Err() and fail.
// Default 0 means no limit.
func WithFlushTimeout(d time.Duration) BufferOption {
	return func(b *MetricsBuffer) { b.flushTimeout = d }
}

// WithMaxBufferBytes sets the maximum buffer memory capacity in bytes.
// When exceeded, the buffer's full policy determines behavior:
// reject (return ErrBufferFull), drop_oldest (evict), or block.
func WithMaxBufferBytes(n int64) BufferOption {
	return func(b *MetricsBuffer) {
		b.maxBufferBytes = n
		bufferMaxBytesGauge.Set(float64(n))
	}
}

// WithBufferFullPolicy sets the behavior when the buffer exceeds its byte capacity.
func WithBufferFullPolicy(p queue.FullBehavior) BufferOption {
	return func(b *MetricsBuffer) { b.fullPolicy = p }
}

// MetricsBuffer buffers incoming metrics and flushes them periodically.
type MetricsBuffer struct {
	mu              sync.Mutex
	metrics         []*metricspb.ResourceMetrics
	maxSize         int
	maxBatchSize    int
	maxBatchBytes   int
	flushInterval   time.Duration
	flushTimeout    time.Duration // 0 = no limit
	exporter        Exporter
	stats           StatsCollector
	limits          LimitsEnforcer
	flushChan       chan struct{}
	doneChan        chan struct{}
	logAggregator   LogAggregator
	concurrency     *exporter.ConcurrencyLimiter
	relabeler       MetricRelabeler
	sampler         MetricSampler
	failoverQueue   FailoverQueue
	tenantProcessor TenantProcessor
	fusedProcessor  FusedTenantLimitsProcessor
	maxBufferBytes  int64              // 0 = no limit
	currentBytes    atomic.Int64       // Current buffer size in bytes
	fullPolicy      queue.FullBehavior // Default: reject (DropNewest)
	spaceCond       *sync.Cond         // For block policy
	batchTuner      BatchSizer         // AIMD batch auto-tuner (nil = static maxBatchBytes)
}

// New creates a new MetricsBuffer.
func New(maxSize, maxBatchSize int, flushInterval time.Duration, exp Exporter, stats StatsCollector, limits LimitsEnforcer, logAggregator LogAggregator, opts ...BufferOption) *MetricsBuffer {
	buf := &MetricsBuffer{
		metrics:       make([]*metricspb.ResourceMetrics, 0, maxSize),
		maxSize:       maxSize,
		maxBatchSize:  maxBatchSize,
		flushInterval: flushInterval,
		exporter:      exp,
		stats:         stats,
		limits:        limits,
		flushChan:     make(chan struct{}, 1),
		doneChan:      make(chan struct{}),
		logAggregator: logAggregator,
		fullPolicy:    queue.DropNewest, // Default: reject incoming data
	}
	for _, opt := range opts {
		opt(buf)
	}
	// Initialize spaceCond for block policy
	buf.spaceCond = sync.NewCond(&buf.mu)
	return buf
}

// AddWithTenant adds metrics to the buffer with a pre-extracted tenant header.
// This is used by receivers that can extract tenant from HTTP/gRPC headers.
func (b *MetricsBuffer) AddWithTenant(resourceMetrics []*metricspb.ResourceMetrics, headerTenant string) error {
	return b.addInternal(resourceMetrics, headerTenant)
}

// Add adds metrics to the buffer.
func (b *MetricsBuffer) Add(resourceMetrics []*metricspb.ResourceMetrics) error {
	return b.addInternal(resourceMetrics, "")
}

// AddAggregated adds aggregated metrics to the buffer, bypassing stats and processing
// to avoid infinite loops. Tenant and limits enforcement are still applied.
func (b *MetricsBuffer) AddAggregated(resourceMetrics []*metricspb.ResourceMetrics) {
	if len(resourceMetrics) == 0 {
		return
	}

	// Apply tenant detection + limits enforcement (fused or separate).
	if b.fusedProcessor != nil {
		resourceMetrics = b.fusedProcessor.Process(resourceMetrics, "")
	} else {
		if b.tenantProcessor != nil {
			start := time.Now()
			resourceMetrics = b.tenantProcessor.ProcessBatch(resourceMetrics, "")
			pipeline.Record("tenant", pipeline.Since(start))
		}
		if b.limits != nil {
			start := time.Now()
			resourceMetrics = b.limits.Process(resourceMetrics)
			pipeline.Record("limits", pipeline.Since(start))
		}
	}

	if len(resourceMetrics) == 0 {
		return
	}

	// Check buffer byte capacity before adding.
	incomingBytes := int64(estimateResourceMetricsSize(resourceMetrics))
	if b.maxBufferBytes > 0 {
		if err := b.enforceCapacity(incomingBytes); err != nil {
			return // Silently drop — aggregate output is best-effort.
		}
	}

	b.mu.Lock()
	b.metrics = append(b.metrics, resourceMetrics...)
	bufferSize := len(b.metrics)
	b.mu.Unlock()

	// Update byte tracking.
	b.currentBytes.Add(incomingBytes)
	bufferBytesGauge.Set(float64(b.currentBytes.Load()))

	if b.stats != nil {
		b.stats.SetOTLPBufferSize(bufferSize)
	}

	// Trigger flush if buffer is full.
	if bufferSize >= b.maxSize {
		select {
		case b.flushChan <- struct{}{}:
		default:
		}
	}
}

// addInternal is the shared implementation for Add and AddWithTenant.
func (b *MetricsBuffer) addInternal(resourceMetrics []*metricspb.ResourceMetrics, headerTenant string) error {
	// Track received datapoints and bytes
	if b.stats != nil {
		receivedCount := countDatapoints(resourceMetrics)
		b.stats.RecordReceived(receivedCount)
		// Estimate received bytes (uncompressed)
		b.stats.RecordOTLPBytesReceived(estimateResourceMetricsSize(resourceMetrics))
	}

	// Process stats before any filtering
	if b.stats != nil {
		start := time.Now()
		b.stats.Process(resourceMetrics)
		pipeline.Record("stats", pipeline.Since(start))
	}

	// Apply processing/sampling filter (reduces volume before limits)
	if b.sampler != nil {
		start := time.Now()
		resourceMetrics = b.sampler.Sample(resourceMetrics)
		pipeline.Record("processing", pipeline.Since(start))
		if len(resourceMetrics) == 0 {
			return nil
		}
	}

	// Apply tenant detection + limits enforcement (fused or separate)
	if b.fusedProcessor != nil {
		resourceMetrics = b.fusedProcessor.Process(resourceMetrics, headerTenant)
	} else {
		if b.tenantProcessor != nil {
			start := time.Now()
			resourceMetrics = b.tenantProcessor.ProcessBatch(resourceMetrics, headerTenant)
			pipeline.Record("tenant", pipeline.Since(start))
		}
		if b.limits != nil {
			start := time.Now()
			resourceMetrics = b.limits.Process(resourceMetrics)
			pipeline.Record("limits", pipeline.Since(start))
		}
	}

	if len(resourceMetrics) == 0 {
		return nil
	}

	// Check buffer byte capacity before adding
	incomingBytes := int64(estimateResourceMetricsSize(resourceMetrics))
	if b.maxBufferBytes > 0 {
		if err := b.enforceCapacity(incomingBytes); err != nil {
			return err
		}
	}

	b.mu.Lock()
	b.metrics = append(b.metrics, resourceMetrics...)
	bufferSize := len(b.metrics)
	b.mu.Unlock()

	// Update byte tracking
	b.currentBytes.Add(incomingBytes)
	bufferBytesGauge.Set(float64(b.currentBytes.Load()))

	// Update buffer size metric
	if b.stats != nil {
		b.stats.SetOTLPBufferSize(bufferSize)
	}

	// Trigger flush if buffer is full
	if bufferSize >= b.maxSize {
		select {
		case b.flushChan <- struct{}{}:
		default:
		}
	}

	return nil
}

// enforceCapacity checks if adding incomingBytes would exceed the buffer limit
// and applies the configured full policy.
func (b *MetricsBuffer) enforceCapacity(incomingBytes int64) error {
	current := b.currentBytes.Load()
	if current+incomingBytes <= b.maxBufferBytes {
		return nil
	}

	switch b.fullPolicy {
	case queue.DropNewest:
		// Reject incoming data
		bufferRejectedTotal.Inc()
		return ErrBufferFull

	case queue.DropOldest:
		// Evict oldest entries to make room
		b.evictOldest(incomingBytes)
		return nil

	case queue.Block:
		// Block until space is available
		b.mu.Lock()
		for b.currentBytes.Load()+incomingBytes > b.maxBufferBytes {
			b.spaceCond.Wait()
		}
		b.mu.Unlock()
		return nil

	default:
		bufferRejectedTotal.Inc()
		return ErrBufferFull
	}
}

// evictOldest removes the oldest entries from the buffer to make room for incomingBytes.
func (b *MetricsBuffer) evictOldest(incomingBytes int64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	needed := b.currentBytes.Load() + incomingBytes - b.maxBufferBytes
	evicted := int64(0)
	evictCount := 0

	for evicted < needed && len(b.metrics) > 0 {
		entrySize := int64(proto.Size(b.metrics[0]))
		b.metrics = b.metrics[1:]
		evicted += entrySize
		evictCount++
	}

	b.currentBytes.Add(-evicted)
	bufferBytesGauge.Set(float64(b.currentBytes.Load()))
	bufferEvictionsTotal.Add(float64(evictCount))
}

// Start starts the background flush routine.
func (b *MetricsBuffer) Start(ctx context.Context) {
	ticker := time.NewTicker(b.flushInterval)
	defer ticker.Stop()

	// Drain ticker for failover queue: attempt to re-export queued entries every 5s
	drainTicker := time.NewTicker(5 * time.Second)
	defer drainTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			b.flush(context.Background()) // Final flush
			close(b.doneChan)
			return
		case <-ticker.C:
			b.flush(ctx)
		case <-b.flushChan:
			b.flush(ctx)
		case <-drainTicker.C:
			b.drainFailoverQueue(ctx)
		}
	}
}

// drainFailoverQueue pops entries from the failover queue and re-exports them.
// Up to 10 entries are processed per tick. Failed entries are pushed back.
func (b *MetricsBuffer) drainFailoverQueue(ctx context.Context) {
	if b.failoverQueue == nil || b.failoverQueue.Len() == 0 {
		return
	}

	const maxDrainPerTick = 10
	for i := 0; i < maxDrainPerTick; i++ {
		req := b.failoverQueue.Pop()
		if req == nil {
			return
		}

		if err := b.exporter.Export(ctx, req); err != nil {
			// Re-push to failover queue for later retry
			failoverQueueDrainErrorsTotal.Inc()
			if pushErr := b.failoverQueue.Push(req); pushErr != nil {
				exportDataLossTotal.Inc()
				logging.Error("failover drain: re-push failed, data lost", logging.F(
					"error", err.Error(),
					"push_error", pushErr.Error(),
				))
			}
			return // Stop draining on first failure to avoid hammering a down backend
		}
		failoverQueueDrainTotal.Inc()
	}
}

// flush sends buffered metrics to the exporter.
func (b *MetricsBuffer) flush(ctx context.Context) {
	b.mu.Lock()
	if len(b.metrics) == 0 {
		b.mu.Unlock()
		return
	}

	// Take metrics from buffer
	toSend := b.metrics
	b.metrics = make([]*metricspb.ResourceMetrics, 0, b.maxSize)
	b.mu.Unlock()

	// Reset byte tracking — data is now in toSend, no longer in buffer
	flushedBytes := b.currentBytes.Swap(0)
	bufferBytesGauge.Set(0)
	_ = flushedBytes // Used for tracking; the data size is in toSend

	// Signal blocked Add() calls that space is available
	if b.spaceCond != nil {
		b.spaceCond.Broadcast()
	}

	// Optional: cap the entire flush cycle duration
	if b.flushTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, b.flushTimeout)
		defer cancel()
	}

	// Update buffer size metric (now empty after taking metrics)
	if b.stats != nil {
		b.stats.SetOTLPBufferSize(0)
	}

	// Snapshot effective max batch bytes: tuner (dynamic AIMD) or static config.
	// Read once at flush start — consistent for the entire flush cycle.
	effectiveMaxBatchBytes := b.maxBatchBytes
	if b.batchTuner != nil {
		if tunerMax := b.batchTuner.CurrentMaxBytes(); tunerMax > 0 {
			effectiveMaxBatchBytes = tunerMax
		}
	}

	// Step 1: Split by count (maxBatchSize)
	// Step 2: Split by bytes (effectiveMaxBatchBytes) via recursive binary split
	var allBatches [][]*metricspb.ResourceMetrics
	splitStart := time.Now()
	for i := 0; i < len(toSend); i += b.maxBatchSize {
		end := i + b.maxBatchSize
		if end > len(toSend) {
			end = len(toSend)
		}
		countBatch := toSend[i:end]
		allBatches = append(allBatches, splitByBytes(countBatch, effectiveMaxBatchBytes)...)
	}
	pipeline.Record("batch_split", pipeline.Since(splitStart))

	// Step 3: Export batches
	// In always-queue mode, each exportBatch() returns instantly (queue push is µs).
	// In legacy mode with concurrency limiter, run in parallel with wg.Wait().
	if b.concurrency == nil {
		for _, batch := range allBatches {
			b.exportBatch(ctx, batch)
		}
		return
	}

	var wg sync.WaitGroup
	for _, batch := range allBatches {
		if !b.concurrency.TryAcquire() {
			// All concurrency slots busy — export directly in the flush goroutine.
			b.exportBatch(ctx, batch)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer b.concurrency.Release()
			b.exportBatch(ctx, batch)
		}()
	}
	wg.Wait()
}

// exportBatch exports a single batch with error-aware retry and split-on-error.
func (b *MetricsBuffer) exportBatch(ctx context.Context, batch []*metricspb.ResourceMetrics) {
	// Apply relabeling before export (may filter or transform metrics)
	if b.relabeler != nil {
		start := time.Now()
		batch = b.relabeler.Relabel(batch)
		pipeline.Record("relabel", pipeline.Since(start))
		if len(batch) == 0 {
			return
		}
	}

	req := &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: batch}
	byteSize := estimateResourceMetricsSize(batch)
	datapointCount := countDatapoints(batch)

	err := b.exporter.Export(ctx, req)
	if err == nil {
		if b.stats != nil {
			b.stats.RecordExport(datapointCount)
			b.stats.RecordOTLPBytesSent(byteSize)
		}
		return
	}

	// Data queued for retry — not an error, but don't count as exported
	if errors.Is(err, exporter.ErrExportQueued) {
		return
	}

	// Check if error indicates payload too large -- split and retry
	var exportErr *exporter.ExportError
	if errors.As(err, &exportErr) && exportErr.IsSplittable() && len(batch) > 1 {
		exportRetrySplitTotal.Inc()
		mid := len(batch) / 2
		b.exportBatch(ctx, batch[:mid])
		b.exportBatch(ctx, batch[mid:])
		return
	}

	// All export attempts exhausted -- push to failover queue instead of dropping
	if b.failoverQueue != nil {
		if qErr := b.failoverQueue.Push(req); qErr != nil {
			exportDataLossTotal.Inc()
			logging.Error("CRITICAL: export failed and failover queue push failed, data lost", logging.F(
				"error", err.Error(),
				"queue_error", qErr.Error(),
				"datapoints", datapointCount,
				"batch_size", len(batch),
			))
		} else {
			failoverQueuePushTotal.Inc()
			logging.Warn("export failed, batch pushed to failover queue", logging.F(
				"error", err.Error(),
				"datapoints", datapointCount,
				"queue_size", b.failoverQueue.Len(),
			))
		}
	} else {
		// No failover queue -- legacy behavior: log + drop
		if b.logAggregator != nil {
			b.logAggregator.Error("export_error", "export failed", map[string]interface{}{
				"error":      err.Error(),
				"batch_size": len(batch),
			}, int64(datapointCount))
		} else {
			logging.Error("export failed", logging.F(
				"error", err.Error(),
				"batch_size", len(batch),
				"datapoints", datapointCount,
			))
		}
	}

	if b.stats != nil {
		b.stats.RecordExportError()
	}
}

// Wait waits for the buffer to finish flushing.
func (b *MetricsBuffer) Wait() {
	<-b.doneChan
}

// CurrentBytes returns the current buffer size in bytes.
func (b *MetricsBuffer) CurrentBytes() int64 {
	return b.currentBytes.Load()
}

// countDatapoints counts total datapoints in a slice of resource metrics.
func countDatapoints(resourceMetrics []*metricspb.ResourceMetrics) int {
	count := 0
	for _, rm := range resourceMetrics {
		for _, sm := range rm.GetScopeMetrics() {
			for _, m := range sm.GetMetrics() {
				switch d := m.Data.(type) {
				case *metricspb.Metric_Gauge:
					count += len(d.Gauge.GetDataPoints())
				case *metricspb.Metric_Sum:
					count += len(d.Sum.GetDataPoints())
				case *metricspb.Metric_Histogram:
					count += len(d.Histogram.GetDataPoints())
				case *metricspb.Metric_ExponentialHistogram:
					count += len(d.ExponentialHistogram.GetDataPoints())
				case *metricspb.Metric_Summary:
					count += len(d.Summary.GetDataPoints())
				}
			}
		}
	}
	return count
}

// estimateResourceMetricsSize estimates the serialized size of resource metrics in bytes.
func estimateResourceMetricsSize(resourceMetrics []*metricspb.ResourceMetrics) int {
	size := 0
	for _, rm := range resourceMetrics {
		size += proto.Size(rm)
	}
	return size
}
