package exporter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/szibis/metrics-governor/internal/logging"
	"github.com/szibis/metrics-governor/internal/queue"
	"github.com/szibis/metrics-governor/internal/sharding"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// ShardedExporterConfig configures a sharded exporter.
type ShardedExporterConfig struct {
	// BaseConfig is the base exporter config used for each endpoint
	BaseConfig Config

	// Sharding config
	HeadlessService    string
	DNSRefreshInterval time.Duration
	DNSTimeout         time.Duration
	Labels             []string
	VirtualNodes       int
	FallbackOnEmpty    bool

	// Queue config (applied per-endpoint if enabled)
	QueueEnabled bool
	QueueConfig  queue.Config
}

// ShardedExporter distributes metrics across multiple endpoints using consistent hashing.
type ShardedExporter struct {
	config    ShardedExporterConfig
	discovery *sharding.Discovery
	hashRing  *sharding.HashRing
	splitter  *sharding.MetricsSplitter

	mu        sync.RWMutex
	exporters map[string]Exporter // endpoint -> Exporter

	ctx    context.Context
	cancel context.CancelFunc
}

// NewSharded creates a new ShardedExporter.
func NewSharded(ctx context.Context, cfg ShardedExporterConfig) (*ShardedExporter, error) {
	// Apply defaults
	if cfg.DNSRefreshInterval <= 0 {
		cfg.DNSRefreshInterval = 30 * time.Second
	}
	if cfg.DNSTimeout <= 0 {
		cfg.DNSTimeout = 5 * time.Second
	}
	if cfg.VirtualNodes <= 0 {
		cfg.VirtualNodes = sharding.DefaultVirtualNodes
	}

	// Create context for the exporter
	exporterCtx, cancel := context.WithCancel(ctx)

	e := &ShardedExporter{
		config:    cfg,
		hashRing:  sharding.NewHashRing(cfg.VirtualNodes),
		exporters: make(map[string]Exporter),
		ctx:       exporterCtx,
		cancel:    cancel,
	}

	// Create shard key builder
	keyBuilder := sharding.NewShardKeyBuilder(sharding.ShardKeyConfig{
		Labels: cfg.Labels,
	})

	// Create splitter
	e.splitter = sharding.NewMetricsSplitter(keyBuilder, e.hashRing)

	// Create discovery
	discoveryCfg := sharding.DiscoveryConfig{
		HeadlessService:  cfg.HeadlessService,
		RefreshInterval:  cfg.DNSRefreshInterval,
		Timeout:          cfg.DNSTimeout,
		FallbackEndpoint: cfg.BaseConfig.Endpoint,
		FallbackOnEmpty:  cfg.FallbackOnEmpty,
	}

	e.discovery = sharding.NewDiscovery(discoveryCfg, e.onEndpointsChanged)

	// Start discovery
	e.discovery.Start(exporterCtx)

	return e, nil
}

// Export sends metrics to the appropriate shard endpoints.
func (e *ShardedExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	if req == nil || len(req.ResourceMetrics) == 0 {
		return nil
	}

	// Split metrics by shard
	shards := e.splitter.Split(req.ResourceMetrics)
	if len(shards) == 0 {
		// Empty ring - log warning
		logging.Warn("no endpoints available for sharding")
		return fmt.Errorf("no endpoints available")
	}

	// Export each shard in parallel
	var wg sync.WaitGroup
	errCh := make(chan error, len(shards))

	for endpoint, metrics := range shards {
		wg.Add(1)
		go func(ep string, ms []*metricspb.ResourceMetrics) {
			defer wg.Done()

			startTime := time.Now()

			exp := e.getOrCreateExporter(ep)
			shardReq := &colmetricspb.ExportMetricsServiceRequest{
				ResourceMetrics: ms,
			}

			// Count datapoints for metrics
			datapointCount := sharding.CountDatapoints(ms)
			sharding.IncrementDatapoints(ep, datapointCount)

			if err := exp.Export(ctx, shardReq); err != nil {
				sharding.IncrementExportError(ep)
				errCh <- fmt.Errorf("endpoint %s: %w", ep, err)
			}

			sharding.ObserveExportLatency(ep, time.Since(startTime).Seconds())
		}(endpoint, metrics)
	}

	wg.Wait()
	close(errCh)

	// Collect errors
	return collectErrors(errCh)
}

// Close stops the discovery and closes all exporters.
func (e *ShardedExporter) Close() error {
	// Stop discovery
	e.discovery.Stop()

	// Cancel context
	e.cancel()

	// Close all exporters
	e.mu.Lock()
	defer e.mu.Unlock()

	var firstErr error
	for endpoint, exp := range e.exporters {
		if err := exp.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("closing exporter for %s: %w", endpoint, err)
		}
	}
	e.exporters = make(map[string]Exporter)

	return firstErr
}

// onEndpointsChanged is called when the endpoint list changes.
func (e *ShardedExporter) onEndpointsChanged(endpoints []string) {
	// Update hash ring
	e.hashRing.UpdateEndpoints(endpoints)

	// Cleanup stale exporters
	e.cleanupStaleExporters(endpoints)

	logging.Info("sharding endpoints updated", logging.F(
		"count", len(endpoints),
	))
}

// getOrCreateExporter gets or creates an exporter for the given endpoint.
func (e *ShardedExporter) getOrCreateExporter(endpoint string) Exporter {
	e.mu.RLock()
	if exp, ok := e.exporters[endpoint]; ok {
		e.mu.RUnlock()
		return exp
	}
	e.mu.RUnlock()

	e.mu.Lock()
	defer e.mu.Unlock()

	// Double-check after acquiring write lock
	if exp, ok := e.exporters[endpoint]; ok {
		return exp
	}

	// Create base exporter for this endpoint
	cfg := e.config.BaseConfig
	cfg.Endpoint = endpoint

	baseExp, err := New(e.ctx, cfg)
	if err != nil {
		logging.Error("failed to create exporter", logging.F(
			"endpoint", endpoint,
			"error", err.Error(),
		))
		// Return a noop exporter that just returns errors
		return &errorExporter{endpoint: endpoint, err: err}
	}

	var exp Exporter = baseExp

	// Wrap with QueuedExporter if queue enabled
	if e.config.QueueEnabled {
		queueCfg := e.config.QueueConfig
		// Use endpoint-specific queue path
		queueCfg.Path = filepath.Join(queueCfg.Path, hashEndpoint(endpoint))

		queuedExp, err := NewQueued(baseExp, queueCfg)
		if err != nil {
			logging.Error("failed to create queued exporter", logging.F(
				"endpoint", endpoint,
				"error", err.Error(),
			))
			// Fall back to non-queued exporter
		} else {
			exp = queuedExp
		}
	}

	e.exporters[endpoint] = exp
	return exp
}

// cleanupStaleExporters closes exporters for endpoints no longer in the list.
func (e *ShardedExporter) cleanupStaleExporters(currentEndpoints []string) {
	currentSet := make(map[string]bool, len(currentEndpoints))
	for _, ep := range currentEndpoints {
		currentSet[ep] = true
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	for endpoint, exp := range e.exporters {
		if !currentSet[endpoint] {
			logging.Info("closing stale exporter", logging.F("endpoint", endpoint))
			if err := exp.Close(); err != nil {
				logging.Error("failed to close stale exporter", logging.F(
					"endpoint", endpoint,
					"error", err.Error(),
				))
			}
			delete(e.exporters, endpoint)
		}
	}
}

// hashEndpoint creates a short hash of the endpoint for use in file paths.
func hashEndpoint(endpoint string) string {
	h := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(h[:8]) // First 8 bytes = 16 hex chars
}

// collectErrors collects errors from a channel and returns a combined error.
func collectErrors(errCh <-chan error) error {
	var errs []error
	for err := range errCh {
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) == 0 {
		return nil
	}

	if len(errs) == 1 {
		return errs[0]
	}

	// Combine multiple errors
	msg := "multiple export errors:"
	for _, err := range errs {
		msg += " [" + err.Error() + "]"
	}
	return errors.New(msg)
}

// errorExporter is an exporter that always returns an error.
type errorExporter struct {
	endpoint string
	err      error
}

func (e *errorExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	return fmt.Errorf("exporter for %s failed to initialize: %w", e.endpoint, e.err)
}

func (e *errorExporter) Close() error {
	return nil
}

// EndpointCount returns the number of active endpoints (for testing/monitoring).
func (e *ShardedExporter) EndpointCount() int {
	return e.hashRing.Size()
}

// GetEndpoints returns the current endpoint list (for testing/monitoring).
func (e *ShardedExporter) GetEndpoints() []string {
	return e.hashRing.GetEndpoints()
}
