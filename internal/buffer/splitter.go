package buffer

import (
	"github.com/prometheus/client_golang/prometheus"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

var (
	batchSplitsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_batch_splits_total",
		Help: "Total number of byte-size-triggered batch splits",
	})

	batchBytes = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "metrics_governor_batch_bytes",
		Help:    "Batch sizes in bytes before export",
		Buckets: []float64{64 * 1024, 256 * 1024, 1024 * 1024, 2 * 1024 * 1024, 4 * 1024 * 1024, 8 * 1024 * 1024, 16 * 1024 * 1024, 32 * 1024 * 1024},
	})

	batchTooLargeTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_batch_too_large_total",
		Help: "Total number of batches that exceeded max batch bytes",
	})
)

func init() {
	prometheus.MustRegister(batchSplitsTotal)
	prometheus.MustRegister(batchBytes)
	prometheus.MustRegister(batchTooLargeTotal)

	batchSplitsTotal.Add(0)
	batchTooLargeTotal.Add(0)
}

// splitByBytes recursively splits a batch of ResourceMetrics into sub-batches
// that each fit within maxBytes. Uses the VMAgent-inspired binary split pattern.
// If maxBytes <= 0, the batch is returned as-is (splitting disabled).
// A single-element batch is never split further.
func splitByBytes(batch []*metricspb.ResourceMetrics, maxBytes int) [][]*metricspb.ResourceMetrics {
	if maxBytes <= 0 || len(batch) <= 1 {
		return [][]*metricspb.ResourceMetrics{batch}
	}

	size := estimateBatchSize(batch)
	batchBytes.Observe(float64(size))

	if size <= maxBytes {
		return [][]*metricspb.ResourceMetrics{batch}
	}

	batchTooLargeTotal.Inc()
	batchSplitsTotal.Inc()

	// Recursive binary split
	mid := len(batch) / 2
	left := splitByBytes(batch[:mid], maxBytes)
	right := splitByBytes(batch[mid:], maxBytes)
	return append(left, right...)
}

// estimateBatchSize estimates the serialized protobuf size of a batch.
func estimateBatchSize(batch []*metricspb.ResourceMetrics) int {
	size := 0
	for _, rm := range batch {
		size += proto.Size(rm)
	}
	return size
}
