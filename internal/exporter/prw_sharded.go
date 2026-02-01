package exporter

import (
	"context"
	"fmt"
	"sync"

	"github.com/szibis/metrics-governor/internal/logging"
	"github.com/szibis/metrics-governor/internal/prw"
	"github.com/szibis/metrics-governor/internal/sharding"
)

// PRWShardedConfig holds configuration for sharded PRW export.
type PRWShardedConfig struct {
	// BaseConfig is the base PRW exporter configuration (endpoint is overwritten per shard).
	BaseConfig PRWExporterConfig
	// ShardKeyLabels are the labels used for sharding (metric name is always included).
	ShardKeyLabels []string
	// VirtualNodes is the number of virtual nodes per endpoint in the hash ring.
	VirtualNodes int
	// Endpoints are the initial backend endpoints.
	Endpoints []string
	// Discovery enables dynamic endpoint discovery.
	Discovery *sharding.DiscoveryConfig
	// QueueEnabled enables persistent queue per shard.
	QueueEnabled bool
	// QueueConfig is the queue configuration (if enabled).
	QueueConfig prw.QueueConfig
	// ExportConcurrency limits parallel export goroutines (0 = NumCPU * 4).
	ExportConcurrency int
}

// PRWShardedExporter exports PRW metrics across multiple sharded backends.
type PRWShardedExporter struct {
	config     PRWShardedConfig
	splitter   *prw.Splitter
	hashRing   *sharding.HashRing
	discovery  *sharding.Discovery
	limiter    *ConcurrencyLimiter
	exporters  map[string]prw.PRWExporter
	queues     map[string]*prw.Queue
	mu         sync.RWMutex
	closed     bool
	closedOnce sync.Once
}

// NewPRWSharded creates a new sharded PRW exporter.
func NewPRWSharded(ctx context.Context, cfg PRWShardedConfig) (*PRWShardedExporter, error) {
	// Apply defaults
	virtualNodes := cfg.VirtualNodes
	if virtualNodes <= 0 {
		virtualNodes = sharding.DefaultVirtualNodes
	}

	// Create hash ring
	hashRing := sharding.NewHashRing(virtualNodes)

	// Create shard key builder
	keyBuilder := prw.NewShardKeyBuilder(prw.ShardKeyConfig{
		Labels: cfg.ShardKeyLabels,
	})

	// Create splitter
	splitter := prw.NewSplitter(keyBuilder, hashRing)

	exp := &PRWShardedExporter{
		config:    cfg,
		splitter:  splitter,
		hashRing:  hashRing,
		limiter:   NewConcurrencyLimiter(cfg.ExportConcurrency),
		exporters: make(map[string]prw.PRWExporter),
		queues:    make(map[string]*prw.Queue),
	}

	// Set up discovery if configured
	if cfg.Discovery != nil {
		discovery := sharding.NewDiscovery(*cfg.Discovery, exp.onEndpointsChanged)
		exp.discovery = discovery

		// Start discovery
		go discovery.Start(ctx)
	} else if len(cfg.Endpoints) > 0 {
		// Use static endpoints
		exp.onEndpointsChanged(cfg.Endpoints)
	}

	return exp, nil
}

// onEndpointsChanged handles endpoint updates from discovery or configuration.
func (e *PRWShardedExporter) onEndpointsChanged(endpoints []string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return
	}

	// Update hash ring
	e.hashRing.UpdateEndpoints(endpoints)

	// Track which endpoints to keep
	newEndpoints := make(map[string]bool)
	for _, ep := range endpoints {
		newEndpoints[ep] = true
	}

	// Close exporters for removed endpoints
	for ep, exp := range e.exporters {
		if !newEndpoints[ep] {
			exp.Close()
			delete(e.exporters, ep)
			if q, ok := e.queues[ep]; ok {
				q.Close()
				delete(e.queues, ep)
			}
		}
	}

	logging.Info("PRW sharded exporter endpoints updated", logging.F(
		"endpoints", len(endpoints),
	))
}

// Export sends a PRW WriteRequest across sharded backends.
func (e *PRWShardedExporter) Export(ctx context.Context, req *prw.WriteRequest) error {
	if req == nil || len(req.Timeseries) == 0 {
		return nil
	}

	// Split request by shard
	shardedReqs := e.splitter.Split(req)
	if len(shardedReqs) == 0 {
		return nil
	}

	// Export to each shard with concurrency limiting
	var errs []error
	var errMu sync.Mutex
	var wg sync.WaitGroup

	for endpoint, shardReq := range shardedReqs {
		wg.Add(1)
		go func(ep string, r *prw.WriteRequest) {
			defer wg.Done()

			// Acquire semaphore slot to limit concurrency
			e.limiter.Acquire()
			defer e.limiter.Release()

			if err := e.exportToEndpoint(ctx, ep, r); err != nil {
				errMu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", ep, err))
				errMu.Unlock()
			}
		}(endpoint, shardReq)
	}

	wg.Wait()

	if len(errs) > 0 {
		return fmt.Errorf("export errors: %v", errs)
	}

	return nil
}

// exportToEndpoint exports to a specific endpoint.
func (e *PRWShardedExporter) exportToEndpoint(ctx context.Context, endpoint string, req *prw.WriteRequest) error {
	exp, err := e.getOrCreateExporter(ctx, endpoint)
	if err != nil {
		return err
	}

	// If queue is enabled, use it for retry
	if e.config.QueueEnabled {
		q, err := e.getOrCreateQueue(endpoint, exp)
		if err != nil {
			return exp.Export(ctx, req) // Fall back to direct export
		}
		return q.Export(ctx, req)
	}

	return exp.Export(ctx, req)
}

// getOrCreateExporter gets or creates an exporter for an endpoint.
func (e *PRWShardedExporter) getOrCreateExporter(ctx context.Context, endpoint string) (prw.PRWExporter, error) {
	e.mu.RLock()
	exp, ok := e.exporters[endpoint]
	e.mu.RUnlock()

	if ok {
		return exp, nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Double-check after acquiring write lock
	if exp, ok := e.exporters[endpoint]; ok {
		return exp, nil
	}

	// Create new exporter for this endpoint
	cfg := e.config.BaseConfig
	cfg.Endpoint = endpoint

	newExp, err := NewPRW(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create exporter for %s: %w", endpoint, err)
	}

	e.exporters[endpoint] = newExp
	return newExp, nil
}

// getOrCreateQueue gets or creates a queue for an endpoint.
func (e *PRWShardedExporter) getOrCreateQueue(endpoint string, exp prw.PRWExporter) (*prw.Queue, error) {
	e.mu.RLock()
	q, ok := e.queues[endpoint]
	e.mu.RUnlock()

	if ok {
		return q, nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Double-check after acquiring write lock
	if q, ok := e.queues[endpoint]; ok {
		return q, nil
	}

	// Create new queue for this endpoint
	cfg := e.config.QueueConfig
	// Use endpoint-specific path
	cfg.Path = fmt.Sprintf("%s/%s", cfg.Path, sanitizeEndpoint(endpoint))

	newQ, err := prw.NewQueue(cfg, exp)
	if err != nil {
		return nil, fmt.Errorf("failed to create queue for %s: %w", endpoint, err)
	}

	e.queues[endpoint] = newQ
	return newQ, nil
}

// Close closes the sharded exporter and all underlying exporters.
func (e *PRWShardedExporter) Close() error {
	var closeErr error

	e.closedOnce.Do(func() {
		e.mu.Lock()
		defer e.mu.Unlock()

		e.closed = true

		// Stop discovery
		if e.discovery != nil {
			e.discovery.Stop()
		}

		// Close all queues
		for _, q := range e.queues {
			if err := q.Close(); err != nil && closeErr == nil {
				closeErr = err
			}
		}

		// Close all exporters
		for _, exp := range e.exporters {
			if err := exp.Close(); err != nil && closeErr == nil {
				closeErr = err
			}
		}
	})

	return closeErr
}

// GetEndpoints returns the current list of endpoints.
func (e *PRWShardedExporter) GetEndpoints() []string {
	return e.hashRing.GetEndpoints()
}

// EndpointCount returns the number of active endpoints.
func (e *PRWShardedExporter) EndpointCount() int {
	return len(e.hashRing.GetEndpoints())
}

// sanitizeEndpoint creates a filesystem-safe name from an endpoint.
func sanitizeEndpoint(endpoint string) string {
	result := make([]byte, 0, len(endpoint))
	for i := 0; i < len(endpoint); i++ {
		c := endpoint[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			result = append(result, c)
		} else {
			result = append(result, '_')
		}
	}
	return string(result)
}

// PRWShardedBuffer wraps a PRW buffer with sharding support.
type PRWShardedBuffer struct {
	buffer   *prw.Buffer
	splitter *prw.Splitter
	hashRing *sharding.HashRing
}

// NewPRWShardedBuffer creates a new sharded PRW buffer.
func NewPRWShardedBuffer(cfg prw.BufferConfig, shardedExp *PRWShardedExporter, stats prw.PRWStatsCollector, limits prw.PRWLimitsEnforcer, logAggregator prw.LogAggregator) *PRWShardedBuffer {
	buf := prw.NewBuffer(cfg, shardedExp, stats, limits, logAggregator)
	return &PRWShardedBuffer{
		buffer:   buf,
		splitter: shardedExp.splitter,
		hashRing: shardedExp.hashRing,
	}
}

// Add adds a WriteRequest to the buffer.
func (b *PRWShardedBuffer) Add(req *prw.WriteRequest) {
	b.buffer.Add(req)
}

// Start starts the buffer.
func (b *PRWShardedBuffer) Start(ctx context.Context) {
	b.buffer.Start(ctx)
}

// Wait waits for the buffer to finish.
func (b *PRWShardedBuffer) Wait() {
	b.buffer.Wait()
}

// SetExporter sets the exporter.
func (b *PRWShardedBuffer) SetExporter(exp prw.PRWExporter) {
	b.buffer.SetExporter(exp)
}

// ensure PRWShardedExporter implements PRWExporter
var _ prw.PRWExporter = (*PRWShardedExporter)(nil)
