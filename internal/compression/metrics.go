package compression

import (
	"runtime"

	"github.com/prometheus/client_golang/prometheus"
)

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

		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "metrics_governor_compression_buffers_active",
			Help: "Number of compression buffers currently checked out from pool",
		}, func() float64 { return float64(bufferActive.Load()) }),

		// Estimated pool memory: sync.Pool keeps up to GOMAXPROCS items per pool.
		// Encoder memory: zstd ~256KB, gzip ~32KB, snappy ~1KB.
		// Buffer pool: ~32KB per buffer (bytes.Buffer initial capacity).
		// We use 256KB as conservative estimate (zstd is default recommendation).
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "metrics_governor_compression_pool_estimated_bytes",
			Help: "Estimated memory held by compression encoder and buffer pools (GOMAXPROCS * encoder_size)",
		}, func() float64 {
			procs := runtime.GOMAXPROCS(0)
			// Each P can hold: 1 encoder (~256KB) + 1 buffer (~32KB) in pool
			return float64(procs) * (256*1024 + 32*1024)
		}),
	)
}
