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

	queueRetryFailureTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_queue_retry_failure_total",
		Help: "Total number of failed retries by error type",
	}, []string{"error_type"})

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

	fastqueueMetaSyncSkipTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_fastqueue_meta_sync_skip_total",
		Help: "Total number of metadata sync operations skipped (queue idle, no dirty data)",
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

	// Direct export timeout metric
	directExportTimeoutTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_direct_export_timeout_total",
		Help: "Total number of direct exports that timed out (triggering circuit breaker)",
	})

	// Backoff metrics
	currentBackoffSeconds = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_backoff_seconds",
		Help: "Current exponential backoff delay in seconds",
	})

	// Worker pool metrics
	queueWorkersActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_workers_active",
		Help: "Number of active worker goroutines pulling from queue",
	})

	queueWorkersTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_workers_total",
		Help: "Configured number of worker goroutines",
	})

	// Non-retryable drop metric
	queueNonRetryableDroppedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_queue_non_retryable_dropped_total",
		Help: "Total entries dropped because the error was non-retryable",
	}, []string{"error_type"})

	// Pipeline split metrics
	queuePreparersActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_preparers_active",
		Help: "Number of preparer goroutines actively compressing data",
	})

	queueSendersActive = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_senders_active",
		Help: "Number of sender goroutines actively sending HTTP requests",
	})

	queuePreparersTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_preparers_total",
		Help: "Configured number of preparer goroutines",
	})

	queueSendersTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_senders_total",
		Help: "Configured number of sender goroutines",
	})

	queuePreparedChannelLength = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_prepared_channel_length",
		Help: "Current number of prepared entries in the channel between preparers and senders",
	})

	// Export data loss metric (for async re-push failures in pipeline split senders)
	queueRequeueDataLossTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_queue_requeue_data_loss_total",
		Help: "Total entries lost due to failed re-push after export failure",
	})

	// Async send metrics
	queueSendsInflight = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_sends_inflight",
		Help: "Current number of in-flight HTTP send requests",
	})

	queueSendsInflightMax = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_sends_inflight_max",
		Help: "Maximum configured in-flight HTTP sends per sender",
	})

	// Adaptive scaler metrics
	queueWorkersDesired = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_workers_desired",
		Help: "Desired number of worker goroutines (from adaptive scaler)",
	})

	queueScalerAdjustmentsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_queue_scaler_adjustments_total",
		Help: "Total scaler adjustments by direction",
	}, []string{"direction"})

	queueExportLatencyEWMA = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_queue_export_latency_ewma_seconds",
		Help: "Exponentially weighted moving average of export latency",
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
	prometheus.MustRegister(queueRetryFailureTotal)
	prometheus.MustRegister(queueDiskFullTotal)
	// FastQueue metrics
	prometheus.MustRegister(fastqueueInmemoryBlocks)
	prometheus.MustRegister(fastqueueDiskBytes)
	prometheus.MustRegister(fastqueueMetaSyncTotal)
	prometheus.MustRegister(fastqueueMetaSyncSkipTotal)
	prometheus.MustRegister(fastqueueChunkRotations)
	prometheus.MustRegister(fastqueueInmemoryFlushes)
	// Circuit breaker metrics
	prometheus.MustRegister(circuitBreakerState)
	prometheus.MustRegister(circuitBreakerOpenTotal)
	prometheus.MustRegister(circuitBreakerRejectedTotal)
	prometheus.MustRegister(directExportTimeoutTotal)
	prometheus.MustRegister(currentBackoffSeconds)
	// Worker pool metrics
	prometheus.MustRegister(queueWorkersActive)
	prometheus.MustRegister(queueWorkersTotal)
	// Non-retryable drop metric
	prometheus.MustRegister(queueNonRetryableDroppedTotal)
	// Pipeline split metrics
	prometheus.MustRegister(queuePreparersActive)
	prometheus.MustRegister(queueSendersActive)
	prometheus.MustRegister(queuePreparersTotal)
	prometheus.MustRegister(queueSendersTotal)
	prometheus.MustRegister(queuePreparedChannelLength)
	prometheus.MustRegister(queueRequeueDataLossTotal)
	// Async send metrics
	prometheus.MustRegister(queueSendsInflight)
	prometheus.MustRegister(queueSendsInflightMax)
	// Adaptive scaler metrics
	prometheus.MustRegister(queueWorkersDesired)
	prometheus.MustRegister(queueScalerAdjustmentsTotal)
	prometheus.MustRegister(queueExportLatencyEWMA)

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
	queueWorkersActive.Set(0)
	queueWorkersTotal.Set(0)
	queuePreparersActive.Set(0)
	queueSendersActive.Set(0)
	queuePreparersTotal.Set(0)
	queueSendersTotal.Set(0)
	queuePreparedChannelLength.Set(0)
	queueRequeueDataLossTotal.Add(0)
	queueSendsInflight.Set(0)
	queueSendsInflightMax.Set(0)
	queueWorkersDesired.Set(0)
	queueExportLatencyEWMA.Set(0)

	// Initialize counters to 0
	queuePushTotal.Add(0)
	queueRetryTotal.Add(0)
	queueRetrySuccessTotal.Add(0)
	queueDiskFullTotal.Add(0)
	fastqueueMetaSyncTotal.Add(0)
	fastqueueMetaSyncSkipTotal.Add(0)
	fastqueueChunkRotations.Add(0)
	fastqueueInmemoryFlushes.Add(0)
	circuitBreakerOpenTotal.Add(0)
	circuitBreakerRejectedTotal.Add(0)
	directExportTimeoutTotal.Add(0)

	// Initialize counter vectors with known label values
	queueDroppedTotal.WithLabelValues("full").Add(0)
	queueDroppedTotal.WithLabelValues("disk_full").Add(0)
	queueDroppedTotal.WithLabelValues("error").Add(0)
	queueRetryFailureTotal.WithLabelValues("network").Add(0)
	queueRetryFailureTotal.WithLabelValues("timeout").Add(0)
	queueRetryFailureTotal.WithLabelValues("server_error").Add(0)
	queueRetryFailureTotal.WithLabelValues("client_error").Add(0)
	queueRetryFailureTotal.WithLabelValues("auth").Add(0)
	queueRetryFailureTotal.WithLabelValues("rate_limit").Add(0)
	queueRetryFailureTotal.WithLabelValues("unknown").Add(0)

	// Initialize scaler adjustment labels
	queueScalerAdjustmentsTotal.WithLabelValues("up").Add(0)
	queueScalerAdjustmentsTotal.WithLabelValues("down").Add(0)

	// Initialize non-retryable drop labels
	queueNonRetryableDroppedTotal.WithLabelValues("auth").Add(0)
	queueNonRetryableDroppedTotal.WithLabelValues("client_error").Add(0)
	queueNonRetryableDroppedTotal.WithLabelValues("unknown").Add(0)

	// Initialize circuit breaker states
	circuitBreakerState.WithLabelValues("closed").Set(1) // Start in closed state
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

// IncrementRetryFailure increments the failed retry counter by error type.
func IncrementRetryFailure(errorType string) {
	queueRetryFailureTotal.WithLabelValues(errorType).Inc()
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

// IncrementMetaSyncSkip increments the skipped metadata sync counter.
func IncrementMetaSyncSkip() {
	fastqueueMetaSyncSkipTotal.Inc()
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

// IncrementDirectExportTimeout increments the direct export timeout counter.
func IncrementDirectExportTimeout() {
	directExportTimeoutTotal.Inc()
}

// SetCurrentBackoff sets the current backoff delay metric.
func SetCurrentBackoff(d time.Duration) {
	currentBackoffSeconds.Set(d.Seconds())
}

// IncrementWorkersActive increments the active workers gauge.
func IncrementWorkersActive() {
	queueWorkersActive.Inc()
}

// DecrementWorkersActive decrements the active workers gauge.
func DecrementWorkersActive() {
	queueWorkersActive.Dec()
}

// SetWorkersTotal sets the configured worker count gauge.
func SetWorkersTotal(n float64) {
	queueWorkersTotal.Set(n)
}

// IncrementNonRetryableDropped increments the non-retryable drop counter by error type.
func IncrementNonRetryableDropped(errorType string) {
	queueNonRetryableDroppedTotal.WithLabelValues(errorType).Inc()
}

// IncrementPreparersActive increments the active preparers gauge.
func IncrementPreparersActive() {
	queuePreparersActive.Inc()
}

// DecrementPreparersActive decrements the active preparers gauge.
func DecrementPreparersActive() {
	queuePreparersActive.Dec()
}

// IncrementSendersActive increments the active senders gauge.
func IncrementSendersActive() {
	queueSendersActive.Inc()
}

// DecrementSendersActive decrements the active senders gauge.
func DecrementSendersActive() {
	queueSendersActive.Dec()
}

// SetPreparersTotal sets the configured preparer count gauge.
func SetPreparersTotal(n float64) {
	queuePreparersTotal.Set(n)
}

// SetSendersTotal sets the configured sender count gauge.
func SetSendersTotal(n float64) {
	queueSendersTotal.Set(n)
}

// SetPreparedChannelLength sets the current prepared channel length.
func SetPreparedChannelLength(n float64) {
	queuePreparedChannelLength.Set(n)
}

// IncrementExportDataLoss increments the re-queue data loss counter.
func IncrementExportDataLoss() {
	queueRequeueDataLossTotal.Inc()
}

// SetSendsInflight sets the current in-flight sends gauge.
func SetSendsInflight(n float64) {
	queueSendsInflight.Set(n)
}

// SetSendsInflightMax sets the max in-flight sends gauge.
func SetSendsInflightMax(n float64) {
	queueSendsInflightMax.Set(n)
}

// SetWorkersDesired sets the desired worker count gauge.
func SetWorkersDesired(n float64) {
	queueWorkersDesired.Set(n)
}

// IncrementScalerAdjustment increments the scaler adjustment counter by direction.
func IncrementScalerAdjustment(direction string) {
	queueScalerAdjustmentsTotal.WithLabelValues(direction).Inc()
}

// SetExportLatencyEWMA sets the export latency EWMA gauge.
func SetExportLatencyEWMA(seconds float64) {
	queueExportLatencyEWMA.Set(seconds)
}
