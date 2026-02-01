package queue

import "github.com/prometheus/client_golang/prometheus"

var (
	queueSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_size",
		Help: "Current number of batches in the send queue",
	})

	queueBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_bytes",
		Help: "Current size of the send queue in bytes",
	})

	queueMaxSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_max_size",
		Help: "Maximum number of batches allowed in the send queue",
	})

	queueMaxBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_max_bytes",
		Help: "Maximum size of the send queue in bytes",
	})

	queueEffectiveMaxSize = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_effective_max_size",
		Help: "Effective maximum batches (adaptive based on disk space)",
	})

	queueEffectiveMaxBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_effective_max_bytes",
		Help: "Effective maximum bytes (adaptive based on disk space)",
	})

	queueUtilizationRatio = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_utilization_ratio",
		Help: "Current queue utilization ratio (0.0-1.0)",
	})

	queueDiskAvailableBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_disk_available_bytes",
		Help: "Available disk space for queue storage",
	})

	queuePushTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_queue_push_total",
		Help: "Total number of batches pushed to the queue",
	})

	queueDroppedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_queue_dropped_total",
		Help: "Total number of batches dropped from the queue",
	}, []string{"reason"})

	queueRetryTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_queue_retry_total",
		Help: "Total number of retry attempts",
	})

	queueRetrySuccessTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_queue_retry_success_total",
		Help: "Total number of successful retries",
	})

	queueWALWriteTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_queue_wal_write_total",
		Help: "Total number of WAL writes",
	})

	queueWALCompactTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_queue_wal_compact_total",
		Help: "Total number of WAL compactions",
	})

	queueDiskFullTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_queue_disk_full_total",
		Help: "Total number of disk full events",
	})

	// I/O optimization metrics
	queueSyncTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_queue_sync_total",
		Help: "Total number of WAL sync operations",
	})

	queueBytesWritten = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_queue_bytes_written_total",
		Help: "Total bytes written to WAL",
	})

	queueBytesCompressed = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_queue_bytes_compressed_total",
		Help: "Total bytes written after compression",
	})

	queueCompressionRatio = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_compression_ratio",
		Help: "Current compression ratio (compressed/original), lower is better",
	})

	queuePendingSyncs = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_pending_syncs",
		Help: "Number of pending writes waiting for sync in batched mode",
	})

	queueSyncLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "metrics_governor_queue_sync_latency_seconds",
		Help:    "Latency of WAL sync operations",
		Buckets: []float64{.0001, .0005, .001, .005, .01, .025, .05, .1, .25, .5, 1},
	})
)

func init() {
	prometheus.MustRegister(queueSize)
	prometheus.MustRegister(queueBytes)
	prometheus.MustRegister(queueMaxSize)
	prometheus.MustRegister(queueMaxBytes)
	prometheus.MustRegister(queueEffectiveMaxSize)
	prometheus.MustRegister(queueEffectiveMaxBytes)
	prometheus.MustRegister(queueUtilizationRatio)
	prometheus.MustRegister(queueDiskAvailableBytes)
	prometheus.MustRegister(queuePushTotal)
	prometheus.MustRegister(queueDroppedTotal)
	prometheus.MustRegister(queueRetryTotal)
	prometheus.MustRegister(queueRetrySuccessTotal)
	prometheus.MustRegister(queueWALWriteTotal)
	prometheus.MustRegister(queueWALCompactTotal)
	prometheus.MustRegister(queueDiskFullTotal)
	// I/O optimization metrics
	prometheus.MustRegister(queueSyncTotal)
	prometheus.MustRegister(queueBytesWritten)
	prometheus.MustRegister(queueBytesCompressed)
	prometheus.MustRegister(queueCompressionRatio)
	prometheus.MustRegister(queuePendingSyncs)
	prometheus.MustRegister(queueSyncLatency)
}

// IncrementRetryTotal increments the retry counter.
func IncrementRetryTotal() {
	queueRetryTotal.Inc()
}

// IncrementRetrySuccessTotal increments the successful retry counter.
func IncrementRetrySuccessTotal() {
	queueRetrySuccessTotal.Inc()
}

// SetCapacityMetrics sets the configured capacity metrics.
func SetCapacityMetrics(maxSize int, maxBytes int64) {
	queueMaxSize.Set(float64(maxSize))
	queueMaxBytes.Set(float64(maxBytes))
}

// SetEffectiveCapacityMetrics sets the adaptive capacity metrics.
func SetEffectiveCapacityMetrics(effectiveMaxSize int, effectiveMaxBytes int64) {
	queueEffectiveMaxSize.Set(float64(effectiveMaxSize))
	queueEffectiveMaxBytes.Set(float64(effectiveMaxBytes))
}

// SetDiskAvailableBytes sets the available disk space metric.
func SetDiskAvailableBytes(bytes int64) {
	queueDiskAvailableBytes.Set(float64(bytes))
}

// UpdateQueueMetrics updates current queue state metrics.
func UpdateQueueMetrics(size int, bytes int64, effectiveMaxBytes int64) {
	queueSize.Set(float64(size))
	queueBytes.Set(float64(bytes))
	if effectiveMaxBytes > 0 {
		queueUtilizationRatio.Set(float64(bytes) / float64(effectiveMaxBytes))
	}
}

// IncrementWALWrite increments WAL write counter.
func IncrementWALWrite() {
	queueWALWriteTotal.Inc()
}

// IncrementWALCompact increments WAL compaction counter.
func IncrementWALCompact() {
	queueWALCompactTotal.Inc()
}

// IncrementDiskFull increments disk full counter.
func IncrementDiskFull() {
	queueDiskFullTotal.Inc()
}

// IncrementSync increments sync operation counter.
func IncrementSync() {
	queueSyncTotal.Inc()
}

// RecordBytesWritten records bytes written before and after compression.
func RecordBytesWritten(originalBytes, compressedBytes int) {
	queueBytesWritten.Add(float64(originalBytes))
	queueBytesCompressed.Add(float64(compressedBytes))
	if originalBytes > 0 {
		queueCompressionRatio.Set(float64(compressedBytes) / float64(originalBytes))
	}
}

// SetPendingSyncs sets the number of pending sync operations.
func SetPendingSyncs(count int) {
	queuePendingSyncs.Set(float64(count))
}

// RecordSyncLatency records the latency of a sync operation.
func RecordSyncLatency(seconds float64) {
	queueSyncLatency.Observe(seconds)
}
