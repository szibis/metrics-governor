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
