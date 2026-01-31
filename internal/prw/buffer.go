package prw

import (
	"context"
	"sync"
	"time"

	"github.com/slawomirskowron/metrics-governor/internal/logging"
)

// PRWExporter defines the interface for sending PRW metrics.
type PRWExporter interface {
	Export(ctx context.Context, req *WriteRequest) error
	Close() error
}

// PRWStatsCollector defines the interface for collecting PRW stats.
type PRWStatsCollector interface {
	RecordPRWReceived(datapointCount, timeseriesCount int)
	RecordPRWExport(datapointCount, timeseriesCount int)
	RecordPRWExportError()
}

// PRWLimitsEnforcer defines the interface for enforcing limits on PRW data.
type PRWLimitsEnforcer interface {
	Process(req *WriteRequest) *WriteRequest
}

// LogAggregator aggregates similar log messages.
type LogAggregator interface {
	Error(key string, message string, fields map[string]interface{}, datapoints int64)
	Stop()
}

// BufferConfig holds PRW buffer configuration.
type BufferConfig struct {
	// MaxSize is the maximum number of WriteRequests to buffer.
	MaxSize int
	// MaxBatchSize is the maximum number of time series per export batch.
	MaxBatchSize int
	// FlushInterval is the interval at which the buffer is flushed.
	FlushInterval time.Duration
}

// DefaultBufferConfig returns default buffer configuration.
func DefaultBufferConfig() BufferConfig {
	return BufferConfig{
		MaxSize:       10000,
		MaxBatchSize:  1000,
		FlushInterval: 5 * time.Second,
	}
}

// Buffer buffers incoming PRW requests and flushes them periodically.
type Buffer struct {
	mu            sync.Mutex
	timeseries    []TimeSeries
	metadata      []MetricMetadata
	config        BufferConfig
	exporter      PRWExporter
	stats         PRWStatsCollector
	limits        PRWLimitsEnforcer
	flushChan     chan struct{}
	doneChan      chan struct{}
	logAggregator LogAggregator
}

// NewBuffer creates a new PRW buffer.
func NewBuffer(cfg BufferConfig, exporter PRWExporter, stats PRWStatsCollector, limits PRWLimitsEnforcer, logAggregator LogAggregator) *Buffer {
	return &Buffer{
		timeseries:    make([]TimeSeries, 0, cfg.MaxSize),
		metadata:      make([]MetricMetadata, 0),
		config:        cfg,
		exporter:      exporter,
		stats:         stats,
		limits:        limits,
		flushChan:     make(chan struct{}, 1),
		doneChan:      make(chan struct{}),
		logAggregator: logAggregator,
	}
}

// Add adds a WriteRequest to the buffer.
func (b *Buffer) Add(req *WriteRequest) {
	if req == nil {
		return
	}

	// Track received datapoints
	if b.stats != nil {
		datapointCount := req.TotalDatapoints()
		timeseriesCount := req.TotalTimeseries()
		b.stats.RecordPRWReceived(datapointCount, timeseriesCount)
	}

	// Apply limits enforcement (may filter/sample metrics)
	if b.limits != nil {
		req = b.limits.Process(req)
	}

	if req == nil || len(req.Timeseries) == 0 {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	// Append timeseries
	b.timeseries = append(b.timeseries, req.Timeseries...)

	// Append metadata (deduplicate by metric family name)
	for _, md := range req.Metadata {
		found := false
		for i := range b.metadata {
			if b.metadata[i].MetricFamilyName == md.MetricFamilyName {
				// Update existing metadata
				b.metadata[i] = md
				found = true
				break
			}
		}
		if !found {
			b.metadata = append(b.metadata, md)
		}
	}

	// Trigger flush if buffer is full
	if len(b.timeseries) >= b.config.MaxSize {
		select {
		case b.flushChan <- struct{}{}:
		default:
		}
	}
}

// Start starts the background flush routine.
func (b *Buffer) Start(ctx context.Context) {
	ticker := time.NewTicker(b.config.FlushInterval)
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

// flush sends buffered PRW data to the exporter.
func (b *Buffer) flush(ctx context.Context) {
	b.mu.Lock()
	if len(b.timeseries) == 0 {
		b.mu.Unlock()
		return
	}

	// Take timeseries and metadata from buffer
	toSend := b.timeseries
	metadata := b.metadata
	b.timeseries = make([]TimeSeries, 0, b.config.MaxSize)
	// Capture exporter reference while holding lock to avoid race with SetExporter
	exporter := b.exporter
	// Keep metadata across flushes (it doesn't need to be sent every time)
	b.mu.Unlock()

	if exporter == nil {
		return
	}

	// Send in batches
	for i := 0; i < len(toSend); i += b.config.MaxBatchSize {
		end := i + b.config.MaxBatchSize
		if end > len(toSend) {
			end = len(toSend)
		}

		batch := toSend[i:end]
		datapointCount := countBatchDatapoints(batch)
		timeseriesCount := len(batch)

		req := &WriteRequest{
			Timeseries: batch,
		}

		// Include metadata only in the first batch
		if i == 0 && len(metadata) > 0 {
			req.Metadata = metadata
		}

		if err := exporter.Export(ctx, req); err != nil {
			if b.logAggregator != nil {
				// Use aggregated logging to reduce log noise at high throughput
				logKey := "prw_export_error"
				b.logAggregator.Error(logKey, "PRW export failed", map[string]interface{}{
					"error":           err.Error(),
					"timeseries":      timeseriesCount,
					"datapoints":      datapointCount,
				}, int64(datapointCount))
			} else {
				logging.Error("PRW export failed", logging.F(
					"error", err.Error(),
					"timeseries", timeseriesCount,
					"datapoints", datapointCount,
				))
			}
			if b.stats != nil {
				b.stats.RecordPRWExportError()
			}
			continue
		}

		// Record successful export
		if b.stats != nil {
			b.stats.RecordPRWExport(datapointCount, timeseriesCount)
		}
	}
}

// Wait waits for the buffer to finish flushing.
func (b *Buffer) Wait() {
	<-b.doneChan
}

// SetExporter sets the exporter for the buffer.
func (b *Buffer) SetExporter(exp PRWExporter) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.exporter = exp
}

// countBatchDatapoints counts total datapoints in a slice of time series.
func countBatchDatapoints(timeseries []TimeSeries) int {
	count := 0
	for i := range timeseries {
		count += timeseries[i].DatapointCount()
	}
	return count
}
