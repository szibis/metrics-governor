package queue

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

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

	queueDiskFullTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_queue_disk_full_total",
		Help: "Total number of disk full events",
	})

	// FastQueue metrics
	fastqueueInmemoryBlocks = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_fastqueue_inmemory_blocks",
		Help: "Current number of blocks in the in-memory channel",
	})

	fastqueueDiskBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_fastqueue_disk_bytes",
		Help: "Current bytes stored on disk",
	})

	fastqueueMetaSyncTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_fastqueue_meta_sync_total",
		Help: "Total number of metadata sync operations",
	})

	fastqueueChunkRotations = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_fastqueue_chunk_rotations",
		Help: "Total number of chunk file rotations",
	})

	fastqueueInmemoryFlushes = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_fastqueue_inmemory_flushes",
		Help: "Total number of in-memory channel flushes to disk",
	})

	// Circuit breaker metrics
	circuitBreakerState = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "metrics_governor_circuit_breaker_state",
		Help: "Current circuit breaker state (1 = active state): closed, open, half_open",
	}, []string{"state"})

	circuitBreakerOpenTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_circuit_breaker_open_total",
		Help: "Total number of times the circuit breaker opened",
	})

	circuitBreakerRejectedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_circuit_breaker_rejected_total",
		Help: "Total number of requests rejected by open circuit breaker",
	})

	// Backoff metrics
	currentBackoffSeconds = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_backoff_seconds",
		Help: "Current exponential backoff delay in seconds",
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
	prometheus.MustRegister(queueDiskFullTotal)
	// FastQueue metrics
	prometheus.MustRegister(fastqueueInmemoryBlocks)
	prometheus.MustRegister(fastqueueDiskBytes)
	prometheus.MustRegister(fastqueueMetaSyncTotal)
	prometheus.MustRegister(fastqueueChunkRotations)
	prometheus.MustRegister(fastqueueInmemoryFlushes)
	// Circuit breaker metrics
	prometheus.MustRegister(circuitBreakerState)
	prometheus.MustRegister(circuitBreakerOpenTotal)
	prometheus.MustRegister(circuitBreakerRejectedTotal)
	prometheus.MustRegister(currentBackoffSeconds)

	// Initialize gauges to 0 so they appear in Prometheus immediately
	// (before queue is created or used)
	queueSize.Set(0)
	queueBytes.Set(0)
	queueMaxSize.Set(0)
	queueMaxBytes.Set(0)
	queueEffectiveMaxSize.Set(0)
	queueEffectiveMaxBytes.Set(0)
	queueUtilizationRatio.Set(0)
	queueDiskAvailableBytes.Set(0)
	fastqueueInmemoryBlocks.Set(0)
	fastqueueDiskBytes.Set(0)
	currentBackoffSeconds.Set(0)
	// Initialize circuit breaker states
	circuitBreakerState.WithLabelValues("closed").Set(0)
	circuitBreakerState.WithLabelValues("open").Set(0)
	circuitBreakerState.WithLabelValues("half_open").Set(0)
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

// IncrementDiskFull increments disk full counter.
func IncrementDiskFull() {
	queueDiskFullTotal.Inc()
}

// SetInmemoryBlocks sets the current in-memory block count.
func SetInmemoryBlocks(count int) {
	fastqueueInmemoryBlocks.Set(float64(count))
}

// SetDiskBytes sets the current disk bytes.
func SetDiskBytes(bytes int64) {
	fastqueueDiskBytes.Set(float64(bytes))
}

// IncrementMetaSync increments the metadata sync counter.
func IncrementMetaSync() {
	fastqueueMetaSyncTotal.Inc()
}

// IncrementChunkRotation increments the chunk rotation counter.
func IncrementChunkRotation() {
	fastqueueChunkRotations.Inc()
}

// IncrementInmemoryFlush increments the in-memory flush counter.
func IncrementInmemoryFlush() {
	fastqueueInmemoryFlushes.Inc()
}

// SetCircuitState sets the circuit breaker state metric.
func SetCircuitState(state string) {
	// Reset all states
	circuitBreakerState.WithLabelValues("closed").Set(0)
	circuitBreakerState.WithLabelValues("open").Set(0)
	circuitBreakerState.WithLabelValues("half_open").Set(0)
	// Set the active state
	circuitBreakerState.WithLabelValues(state).Set(1)
}

// IncrementCircuitOpen increments the circuit breaker open counter.
func IncrementCircuitOpen() {
	circuitBreakerOpenTotal.Inc()
}

// IncrementCircuitRejected increments the circuit breaker rejected counter.
func IncrementCircuitRejected() {
	circuitBreakerRejectedTotal.Inc()
}

// SetCurrentBackoff sets the current backoff delay metric.
func SetCurrentBackoff(d time.Duration) {
	currentBackoffSeconds.Set(d.Seconds())
}
