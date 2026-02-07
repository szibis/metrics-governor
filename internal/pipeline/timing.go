package pipeline

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	componentSeconds = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_component_seconds_total",
		Help: "Wall-clock seconds spent in each pipeline component",
	}, []string{"component"})

	componentBytesProcessed = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_component_bytes_processed_total",
		Help: "Total bytes processed by each pipeline component",
	}, []string{"component"})
)

// knownComponents lists all components for pre-initialization.
var knownComponents = []string{
	"receive_decompress", "receive_unmarshal",
	"stats", "sampling", "tenant", "limits",
	"buffer_mgmt", "relabel", "batch_split",
	"serialize", "compress", "export_http",
	"queue_push", "queue_pop",
}

func init() {
	prometheus.MustRegister(componentSeconds)
	prometheus.MustRegister(componentBytesProcessed)
	for _, c := range knownComponents {
		componentSeconds.WithLabelValues(c).Add(0)
		componentBytesProcessed.WithLabelValues(c).Add(0)
	}
}

// Record adds elapsed time to the named component's counter.
func Record(component string, d time.Duration) {
	componentSeconds.WithLabelValues(component).Add(d.Seconds())
}

// RecordBytes adds processed bytes to the named component's counter.
func RecordBytes(component string, n int) {
	if n > 0 {
		componentBytesProcessed.WithLabelValues(component).Add(float64(n))
	}
}

// Since returns elapsed time since start.
func Since(start time.Time) time.Duration {
	return time.Since(start)
}

// Track starts timing and returns a func that records when called.
//
//	defer pipeline.Track("export_http")()
func Track(component string) func() {
	start := time.Now()
	return func() {
		componentSeconds.WithLabelValues(component).Add(time.Since(start).Seconds())
	}
}
