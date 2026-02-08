package exporter

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/szibis/metrics-governor/internal/logging"
)

var (
	connectionWarmupTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_connection_warmup_total",
		Help: "Total connection warmup attempts by status",
	}, []string{"status"})
)

func init() {
	prometheus.MustRegister(connectionWarmupTotal)
	connectionWarmupTotal.WithLabelValues("success").Add(0)
	connectionWarmupTotal.WithLabelValues("failure").Add(0)
}

// WarmupConnections sends lightweight HEAD requests to establish HTTP connections
// at startup, avoiding cold-start latency spikes on the first real export.
// This is fire-and-forget: failures are logged but don't block the exporter.
func WarmupConnections(ctx context.Context, endpoints []string, client *http.Client, timeout time.Duration) {
	if len(endpoints) == 0 || client == nil {
		return
	}

	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	warmupCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for _, endpoint := range endpoints {
		req, err := http.NewRequestWithContext(warmupCtx, http.MethodHead, endpoint, nil)
		if err != nil {
			logging.Info("connection warmup: failed to create request", logging.F(
				"endpoint", endpoint,
				"error", err.Error(),
			))
			connectionWarmupTotal.WithLabelValues("failure").Inc()
			continue
		}

		resp, err := client.Do(req)
		if err != nil {
			// Connection established but server may not support HEAD — still warms the pool.
			logging.Info("connection warmup: request completed with error (connection may still be established)", logging.F(
				"endpoint", endpoint,
				"error", err.Error(),
			))
			connectionWarmupTotal.WithLabelValues("failure").Inc()
			continue
		}
		resp.Body.Close()

		connectionWarmupTotal.WithLabelValues("success").Inc()
		logging.Info("connection warmup: established", logging.F(
			"endpoint", endpoint,
			"status", resp.StatusCode,
		))
	}
}
