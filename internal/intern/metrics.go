package intern

import "github.com/prometheus/client_golang/prometheus"

func init() {
	prometheus.MustRegister(
		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name:        "metrics_governor_intern_hits_total",
			Help:        "Intern pool cache hits",
			ConstLabels: prometheus.Labels{"pool": "label_names"},
		}, func() float64 { h, _ := LabelNames.Stats(); return float64(h) }),

		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name:        "metrics_governor_intern_hits_total",
			Help:        "Intern pool cache hits",
			ConstLabels: prometheus.Labels{"pool": "metric_names"},
		}, func() float64 { h, _ := MetricNames.Stats(); return float64(h) }),

		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name:        "metrics_governor_intern_misses_total",
			Help:        "Intern pool cache misses",
			ConstLabels: prometheus.Labels{"pool": "label_names"},
		}, func() float64 { _, m := LabelNames.Stats(); return float64(m) }),

		prometheus.NewCounterFunc(prometheus.CounterOpts{
			Name:        "metrics_governor_intern_misses_total",
			Help:        "Intern pool cache misses",
			ConstLabels: prometheus.Labels{"pool": "metric_names"},
		}, func() float64 { _, m := MetricNames.Stats(); return float64(m) }),

		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name:        "metrics_governor_intern_pool_size",
			Help:        "Number of interned strings in pool",
			ConstLabels: prometheus.Labels{"pool": "label_names"},
		}, func() float64 { return float64(LabelNames.Size()) }),

		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name:        "metrics_governor_intern_pool_size",
			Help:        "Number of interned strings in pool",
			ConstLabels: prometheus.Labels{"pool": "metric_names"},
		}, func() float64 { return float64(MetricNames.Size()) }),
	)
}
