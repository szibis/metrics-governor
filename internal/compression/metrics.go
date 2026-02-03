package compression

import "github.com/prometheus/client_golang/prometheus"

func init() {
	prometheus.MustRegister(
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "metrics_governor_compression_pool_gets_total",
			Help: "Pool.Get() calls for compression encoders",
		}, func() float64 { return float64(compressionPoolGets.Load()) }),

		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "metrics_governor_compression_pool_puts_total",
			Help: "Pool.Put() calls for compression encoders",
		}, func() float64 { return float64(compressionPoolPuts.Load()) }),

		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "metrics_governor_compression_pool_discards_total",
			Help: "Compression encoders discarded (not returned to pool)",
		}, func() float64 { return float64(compressionPoolDiscards.Load()) }),

		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "metrics_governor_compression_pool_new_total",
			Help: "New compression encoders created (pool miss)",
		}, func() float64 { return float64(compressionPoolNews.Load()) }),

		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "metrics_governor_compression_buffer_pool_gets_total",
			Help: "Buffer pool Get() calls",
		}, func() float64 { return float64(bufferPoolGets.Load()) }),

		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name: "metrics_governor_compression_buffer_pool_puts_total",
			Help: "Buffer pool Put() calls",
		}, func() float64 { return float64(bufferPoolPuts.Load()) }),
	)
}
