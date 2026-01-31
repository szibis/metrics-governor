package sharding

import "github.com/prometheus/client_golang/prometheus"

var (
	shardingEndpointsTotal = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_sharding_endpoints_total",
		Help: "Current number of active sharding endpoints",
	})

	shardingDatapointsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_sharding_datapoints_total",
		Help: "Total datapoints sent per endpoint",
	}, []string{"endpoint"})

	shardingExportErrorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_sharding_export_errors_total",
		Help: "Total export errors per endpoint",
	}, []string{"endpoint"})

	shardingRehashTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_sharding_rehash_total",
		Help: "Total number of hash ring rehash events",
	})

	shardingDNSRefreshTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_sharding_dns_refresh_total",
		Help: "Total DNS refresh attempts",
	})

	shardingDNSErrorsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_sharding_dns_errors_total",
		Help: "Total DNS lookup errors",
	})

	shardingDNSLatencySeconds = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "metrics_governor_sharding_dns_latency_seconds",
		Help:    "DNS lookup latency in seconds",
		Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0},
	})

	shardingExportLatencySeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "metrics_governor_sharding_export_latency_seconds",
		Help:    "Export latency per endpoint in seconds",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0},
	}, []string{"endpoint"})
)

func init() {
	prometheus.MustRegister(shardingEndpointsTotal)
	prometheus.MustRegister(shardingDatapointsTotal)
	prometheus.MustRegister(shardingExportErrorsTotal)
	prometheus.MustRegister(shardingRehashTotal)
	prometheus.MustRegister(shardingDNSRefreshTotal)
	prometheus.MustRegister(shardingDNSErrorsTotal)
	prometheus.MustRegister(shardingDNSLatencySeconds)
	prometheus.MustRegister(shardingExportLatencySeconds)
}

// SetEndpointsTotal sets the current number of endpoints.
func SetEndpointsTotal(count int) {
	shardingEndpointsTotal.Set(float64(count))
}

// IncrementDatapoints increments the datapoints counter for an endpoint.
func IncrementDatapoints(endpoint string, count int) {
	shardingDatapointsTotal.WithLabelValues(endpoint).Add(float64(count))
}

// IncrementExportError increments the export error counter for an endpoint.
func IncrementExportError(endpoint string) {
	shardingExportErrorsTotal.WithLabelValues(endpoint).Inc()
}

// IncrementRehash increments the rehash counter.
func IncrementRehash() {
	shardingRehashTotal.Inc()
}

// IncrementDNSRefresh increments the DNS refresh counter.
func IncrementDNSRefresh() {
	shardingDNSRefreshTotal.Inc()
}

// IncrementDNSErrors increments the DNS errors counter.
func IncrementDNSErrors() {
	shardingDNSErrorsTotal.Inc()
}

// ObserveDNSLatency records a DNS lookup latency observation.
func ObserveDNSLatency(seconds float64) {
	shardingDNSLatencySeconds.Observe(seconds)
}

// ObserveExportLatency records an export latency observation for an endpoint.
func ObserveExportLatency(endpoint string, seconds float64) {
	shardingExportLatencySeconds.WithLabelValues(endpoint).Observe(seconds)
}
