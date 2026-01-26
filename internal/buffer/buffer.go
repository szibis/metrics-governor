package buffer

import (
	"context"
	"sync"
	"time"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// Exporter defines the interface for sending metrics.
type Exporter interface {
	Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error
}

// StatsCollector defines the interface for collecting stats.
type StatsCollector interface {
	Process(resourceMetrics []*metricspb.ResourceMetrics)
}

// LimitsEnforcer defines the interface for enforcing limits.
type LimitsEnforcer interface {
	Process(resourceMetrics []*metricspb.ResourceMetrics) []*metricspb.ResourceMetrics
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
}

// New creates a new MetricsBuffer.
func New(maxSize, maxBatchSize int, flushInterval time.Duration, exporter Exporter, stats StatsCollector, limits LimitsEnforcer) *MetricsBuffer {
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
	}
}

// Add adds metrics to the buffer.
func (b *MetricsBuffer) Add(resourceMetrics []*metricspb.ResourceMetrics) {
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
	defer b.mu.Unlock()

	b.metrics = append(b.metrics, resourceMetrics...)

	// Trigger flush if buffer is full
	if len(b.metrics) >= b.maxSize {
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

		if err := b.exporter.Export(ctx, req); err != nil {
			// TODO: implement retry logic or dead letter queue
			// For now, just log and continue
			continue
		}
	}
}

// Wait waits for the buffer to finish flushing.
func (b *MetricsBuffer) Wait() {
	<-b.doneChan
}
