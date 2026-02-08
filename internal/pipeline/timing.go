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

	// Pre-resolved counters — avoids WithLabelValues() mutex+map lookup on every call.
	secondsCounters map[string]prometheus.Counter
	bytesCounters   map[string]prometheus.Counter
)

// knownComponents lists all components for pre-initialization.
var knownComponents = []string{
	"receive_decompress", "receive_unmarshal",
	"stats", "processing", "tenant", "limits",
	"buffer_mgmt", "relabel", "batch_split",
	"serialize", "compress", "export_http",
	"queue_push", "queue_pop",
	"prepare", "send",
}

func init() {
	prometheus.MustRegister(componentSeconds)
	prometheus.MustRegister(componentBytesProcessed)

	secondsCounters = make(map[string]prometheus.Counter, len(knownComponents))
	bytesCounters = make(map[string]prometheus.Counter, len(knownComponents))
	for _, c := range knownComponents {
		sc := componentSeconds.WithLabelValues(c)
		sc.Add(0)
		secondsCounters[c] = sc

		bc := componentBytesProcessed.WithLabelValues(c)
		bc.Add(0)
		bytesCounters[c] = bc
	}
}

// Record adds elapsed time to the named component's counter.
// Uses pre-resolved counter — no map lookup or mutex in the hot path.
func Record(component string, d time.Duration) {
	if c, ok := secondsCounters[component]; ok {
		c.Add(d.Seconds())
	}
}

// RecordBytes adds processed bytes to the named component's counter.
func RecordBytes(component string, n int) {
	if n > 0 {
		if c, ok := bytesCounters[component]; ok {
			c.Add(float64(n))
		}
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
	c := secondsCounters[component]
	return func() {
		if c != nil {
			c.Add(time.Since(start).Seconds())
		}
	}
}
