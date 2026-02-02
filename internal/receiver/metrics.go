package receiver

import (
	"github.com/prometheus/client_golang/prometheus"
)

var (
	receiverErrorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_receiver_errors_total",
		Help: "Total number of receiver errors",
	}, []string{"type"})

	receiverRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_receiver_requests_total",
		Help: "Total number of requests received",
	}, []string{"protocol"})

	receiverDatapointsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_receiver_datapoints_total",
		Help: "Total number of datapoints received",
	})
)

func init() {
	prometheus.MustRegister(receiverErrorsTotal)
	prometheus.MustRegister(receiverRequestsTotal)
	prometheus.MustRegister(receiverDatapointsTotal)

	// Initialize counters with 0 so they appear in /metrics immediately
	receiverErrorsTotal.WithLabelValues("decode").Add(0)
	receiverErrorsTotal.WithLabelValues("auth").Add(0)
	receiverErrorsTotal.WithLabelValues("decompress").Add(0)
	receiverErrorsTotal.WithLabelValues("read").Add(0)
	receiverRequestsTotal.WithLabelValues("grpc").Add(0)
	receiverRequestsTotal.WithLabelValues("http").Add(0)
	receiverRequestsTotal.WithLabelValues("prw").Add(0)
	receiverDatapointsTotal.Add(0)
}

// IncrementReceiverError increments the receiver error counter for a specific type.
func IncrementReceiverError(errorType string) {
	receiverErrorsTotal.WithLabelValues(errorType).Inc()
}

// IncrementReceiverRequests increments the receiver requests counter for a protocol.
func IncrementReceiverRequests(protocol string) {
	receiverRequestsTotal.WithLabelValues(protocol).Inc()
}

// AddReceiverDatapoints increments the receiver datapoints counter.
func AddReceiverDatapoints(count int) {
	receiverDatapointsTotal.Add(float64(count))
}
