package buffer

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/szibis/metrics-governor/internal/exporter"
	"github.com/szibis/metrics-governor/internal/logging"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

var (
	exportConcurrentWorkers = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_export_concurrent_workers",
		Help: "Number of active export worker goroutines",
	})

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
)

func init() {
	prometheus.MustRegister(exportConcurrentWorkers)
	prometheus.MustRegister(exportRetrySplitTotal)
	prometheus.MustRegister(failoverQueuePushTotal)
	prometheus.MustRegister(failoverQueueDrainTotal)
	prometheus.MustRegister(failoverQueueDrainErrorsTotal)

	exportConcurrentWorkers.Set(0)
	exportRetrySplitTotal.Add(0)
	failoverQueuePushTotal.Add(0)
	failoverQueueDrainTotal.Add(0)
	failoverQueueDrainErrorsTotal.Add(0)
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

// LogAggregator aggregates similar log messages.
type LogAggregator interface {
	Error(key string, message string, fields map[string]interface{}, datapoints int64)
	Stop()
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
func WithConcurrency(n int) BufferOption {
	return func(b *MetricsBuffer) {
		b.concurrency = exporter.NewConcurrencyLimiter(n)
	}
}

// WithFailoverQueue sets a failover queue for the buffer. When all export
// attempts fail (including splitting), failed batches are pushed to this
// queue instead of being silently dropped.
func WithFailoverQueue(q FailoverQueue) BufferOption {
	return func(b *MetricsBuffer) { b.failoverQueue = q }
}

// MetricsBuffer buffers incoming metrics and flushes them periodically.
type MetricsBuffer struct {
	mu            sync.Mutex
	metrics       []*metricspb.ResourceMetrics
	maxSize       int
	maxBatchSize  int
	maxBatchBytes int
	flushInterval time.Duration
	exporter      Exporter
	stats         StatsCollector
	limits        LimitsEnforcer
	flushChan     chan struct{}
	doneChan      chan struct{}
	logAggregator LogAggregator
	concurrency   *exporter.ConcurrencyLimiter
	failoverQueue FailoverQueue
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
	}
	for _, opt := range opts {
		opt(buf)
	}
	return buf
}

// Add adds metrics to the buffer.
func (b *MetricsBuffer) Add(resourceMetrics []*metricspb.ResourceMetrics) {
	// Track received datapoints and bytes
	if b.stats != nil {
		receivedCount := countDatapoints(resourceMetrics)
		b.stats.RecordReceived(receivedCount)
		// Estimate received bytes (uncompressed)
		b.stats.RecordOTLPBytesReceived(estimateResourceMetricsSize(resourceMetrics))
	}

	// Process stats before any filtering
	if b.stats != nil {
		b.stats.Process(resourceMetrics)
	}

	// Apply limits enforcement (may filter/sample metrics)
	if b.limits != nil {
		resourceMetrics = b.limits.Process(resourceMetrics)
	}

	if len(resourceMetrics) == 0 {
		return
	}

	b.mu.Lock()
	b.metrics = append(b.metrics, resourceMetrics...)
	bufferSize := len(b.metrics)
	b.mu.Unlock()

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

	// Update buffer size metric (now empty after taking metrics)
	if b.stats != nil {
		b.stats.SetOTLPBufferSize(0)
	}

	// Step 1: Split by count (maxBatchSize)
	// Step 2: Split by bytes (maxBatchBytes) via recursive binary split
	var allBatches [][]*metricspb.ResourceMetrics
	for i := 0; i < len(toSend); i += b.maxBatchSize {
		end := i + b.maxBatchSize
		if end > len(toSend) {
			end = len(toSend)
		}
		countBatch := toSend[i:end]
		allBatches = append(allBatches, splitByBytes(countBatch, b.maxBatchBytes)...)
	}

	// Step 3: Export batches (concurrently if concurrency limiter is set)
	if b.concurrency == nil {
		for _, batch := range allBatches {
			b.exportBatch(ctx, batch)
		}
		return
	}

	var wg sync.WaitGroup
	for _, batch := range allBatches {
		wg.Add(1)
		b.concurrency.Acquire()
		exportConcurrentWorkers.Inc()
		go func() {
			defer wg.Done()
			defer b.concurrency.Release()
			defer exportConcurrentWorkers.Dec()
			b.exportBatch(ctx, batch)
		}()
	}
	wg.Wait()
}

// exportBatch exports a single batch with error-aware retry and split-on-error.
func (b *MetricsBuffer) exportBatch(ctx context.Context, batch []*metricspb.ResourceMetrics) {
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
