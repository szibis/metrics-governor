package buffer

import (
	"context"
	"sync"
	"time"

	"github.com/szibis/metrics-governor/internal/logging"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

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

// MetricsBuffer buffers incoming metrics and flushes them periodically.
type MetricsBuffer struct {
	mu            sync.Mutex
	metrics       []*metricspb.ResourceMetrics
	maxSize       int
	maxBatchSize  int
	flushInterval time.Duration
	exporter      Exporter
	stats         StatsCollector
	limits        LimitsEnforcer
	flushChan     chan struct{}
	doneChan      chan struct{}
	logAggregator LogAggregator
}

// New creates a new MetricsBuffer.
func New(maxSize, maxBatchSize int, flushInterval time.Duration, exporter Exporter, stats StatsCollector, limits LimitsEnforcer, logAggregator LogAggregator) *MetricsBuffer {
	return &MetricsBuffer{
		metrics:       make([]*metricspb.ResourceMetrics, 0, maxSize),
		maxSize:       maxSize,
		maxBatchSize:  maxBatchSize,
		flushInterval: flushInterval,
		exporter:      exporter,
		stats:         stats,
		limits:        limits,
		flushChan:     make(chan struct{}, 1),
		doneChan:      make(chan struct{}),
		logAggregator: logAggregator,
	}
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
		}
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

	// Send in batches
	for i := 0; i < len(toSend); i += b.maxBatchSize {
		end := i + b.maxBatchSize
		if end > len(toSend) {
			end = len(toSend)
		}

		batch := toSend[i:end]
		req := &colmetricspb.ExportMetricsServiceRequest{
			ResourceMetrics: batch,
		}

		// Estimate bytes before sending (uncompressed size)
		byteSize := estimateResourceMetricsSize(batch)

		if err := b.exporter.Export(ctx, req); err != nil {
			// Only count datapoints when needed for logging
			datapointCount := countDatapoints(batch)
			if b.logAggregator != nil {
				// Use aggregated logging to reduce log noise at high throughput
				logKey := "export_error"
				b.logAggregator.Error(logKey, "export failed", map[string]interface{}{
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
			if b.stats != nil {
				b.stats.RecordExportError()
			}
			continue
		}

		// Record successful export
		if b.stats != nil {
			datapointCount := countDatapoints(batch)
			b.stats.RecordExport(datapointCount)
			b.stats.RecordOTLPBytesSent(byteSize)
		}
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
