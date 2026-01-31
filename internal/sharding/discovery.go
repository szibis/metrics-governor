package sharding

import (
	"context"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/slawomirskowron/metrics-governor/internal/logging"
)

// DiscoveryConfig configures DNS-based endpoint discovery.
type DiscoveryConfig struct {
	// HeadlessService is the DNS name of the Kubernetes headless service.
	// Format: "service-name.namespace.svc.cluster.local:port"
	HeadlessService string

	// RefreshInterval is how often to refresh the endpoint list.
	RefreshInterval time.Duration

	// Timeout is the DNS lookup timeout.
	Timeout time.Duration

	// FallbackEndpoint is used when DNS returns no results and FallbackOnEmpty is true.
	FallbackEndpoint string

	// FallbackOnEmpty uses FallbackEndpoint when DNS returns empty.
	FallbackOnEmpty bool
}

// DefaultDiscoveryConfig returns a default discovery configuration.
func DefaultDiscoveryConfig() DiscoveryConfig {
	return DiscoveryConfig{
		RefreshInterval: 30 * time.Second,
		Timeout:         5 * time.Second,
		FallbackOnEmpty: true,
	}
}

// Discovery performs DNS-based endpoint discovery for Kubernetes headless services.
type Discovery struct {
	config   DiscoveryConfig
	resolver Resolver

	mu        sync.RWMutex
	endpoints []string

	onChange func([]string)
	stopCh   chan struct{}
	doneCh   chan struct{}
}

// Resolver is an interface for DNS resolution (allows mocking in tests).
type Resolver interface {
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// NetResolver wraps net.Resolver to implement the Resolver interface.
type NetResolver struct {
	resolver *net.Resolver
}

// NewNetResolver creates a new NetResolver.
func NewNetResolver() *NetResolver {
	return &NetResolver{
		resolver: &net.Resolver{},
	}
}

// LookupHost performs a DNS lookup for the given host.
func (r *NetResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return r.resolver.LookupHost(ctx, host)
}

// NewDiscovery creates a new DNS-based endpoint discovery.
func NewDiscovery(cfg DiscoveryConfig, onChange func([]string)) *Discovery {
	if cfg.RefreshInterval <= 0 {
		cfg.RefreshInterval = 30 * time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}

	return &Discovery{
		config:    cfg,
		resolver:  NewNetResolver(),
		endpoints: []string{},
		onChange:  onChange,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
	}
}

// NewDiscoveryWithResolver creates a new Discovery with a custom resolver (for testing).
func NewDiscoveryWithResolver(cfg DiscoveryConfig, onChange func([]string), resolver Resolver) *Discovery {
	d := NewDiscovery(cfg, onChange)
	d.resolver = resolver
	return d
}

// Start begins periodic endpoint discovery.
func (d *Discovery) Start(ctx context.Context) {
	// Perform initial refresh
	d.refresh(ctx)

	// Start refresh loop
	go d.refreshLoop(ctx)
}

// Stop stops the discovery refresh loop.
func (d *Discovery) Stop() {
	select {
	case <-d.stopCh:
		// Already stopped
		return
	default:
		close(d.stopCh)
	}

	// Wait for refresh loop to finish with timeout
	select {
	case <-d.doneCh:
	case <-time.After(10 * time.Second):
		logging.Warn("timeout waiting for discovery refresh loop to stop")
	}
}

// GetEndpoints returns a copy of the current endpoint list.
func (d *Discovery) GetEndpoints() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make([]string, len(d.endpoints))
	copy(result, d.endpoints)
	return result
}

// refreshLoop periodically refreshes the endpoint list.
func (d *Discovery) refreshLoop(ctx context.Context) {
	defer close(d.doneCh)

	ticker := time.NewTicker(d.config.RefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-d.stopCh:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.refresh(ctx)
		}
	}
}

// refresh performs a single DNS lookup and updates endpoints if changed.
func (d *Discovery) refresh(ctx context.Context) {
	IncrementDNSRefresh()

	startTime := time.Now()

	newEndpoints, err := d.lookupEndpoints(ctx)
	if err != nil {
		IncrementDNSErrors()
		logging.Error("DNS lookup failed", logging.F(
			"headless_service", d.config.HeadlessService,
			"error", err.Error(),
		))
		return
	}

	ObserveDNSLatency(time.Since(startTime).Seconds())

	d.updateEndpoints(newEndpoints)
}

// lookupEndpoints performs DNS lookup and returns endpoint list.
func (d *Discovery) lookupEndpoints(ctx context.Context) ([]string, error) {
	// Parse host and port from HeadlessService
	host, port := d.parseHostPort()

	// Create timeout context for DNS lookup
	lookupCtx, cancel := context.WithTimeout(ctx, d.config.Timeout)
	defer cancel()

	// Perform DNS lookup
	ips, err := d.resolver.LookupHost(lookupCtx, host)
	if err != nil {
		return nil, err
	}

	// Build endpoint list with port
	endpoints := make([]string, 0, len(ips))
	for _, ip := range ips {
		if ip == "" {
			continue
		}
		endpoint := ip
		if port != "" {
			// Handle IPv6 addresses
			if strings.Contains(ip, ":") {
				endpoint = "[" + ip + "]:" + port
			} else {
				endpoint = ip + ":" + port
			}
		}
		endpoints = append(endpoints, endpoint)
	}

	// Sort for deterministic ordering
	sort.Strings(endpoints)

	// Handle empty results
	if len(endpoints) == 0 && d.config.FallbackOnEmpty && d.config.FallbackEndpoint != "" {
		logging.Warn("DNS returned no results, using fallback endpoint", logging.F(
			"headless_service", d.config.HeadlessService,
			"fallback_endpoint", d.config.FallbackEndpoint,
		))
		return []string{d.config.FallbackEndpoint}, nil
	}

	return endpoints, nil
}

// parseHostPort extracts host and port from HeadlessService.
func (d *Discovery) parseHostPort() (host, port string) {
	service := d.config.HeadlessService

	// Check for port suffix
	if idx := strings.LastIndex(service, ":"); idx != -1 {
		// Make sure this is actually a port (not part of IPv6)
		possiblePort := service[idx+1:]
		if !strings.Contains(possiblePort, ":") && !strings.Contains(possiblePort, ".") {
			host = service[:idx]
			port = possiblePort
			return
		}
	}

	host = service
	return
}

// updateEndpoints updates the endpoint list if it has changed.
func (d *Discovery) updateEndpoints(newEndpoints []string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Check if changed
	if endpointsEqual(d.endpoints, newEndpoints) {
		return
	}

	// Log the change
	added, removed := endpointsDiff(d.endpoints, newEndpoints)
	if len(added) > 0 || len(removed) > 0 {
		logging.Info("endpoints changed", logging.F(
			"added", strings.Join(added, ","),
			"removed", strings.Join(removed, ","),
			"total", len(newEndpoints),
		))
	}

	// Update endpoints
	d.endpoints = newEndpoints

	// Update metrics
	SetEndpointsTotal(len(newEndpoints))
	IncrementRehash()

	// Notify callback (outside of lock to avoid deadlock)
	if d.onChange != nil {
		// Make a copy to pass to callback
		callbackEndpoints := make([]string, len(newEndpoints))
		copy(callbackEndpoints, newEndpoints)

		// Call onChange asynchronously to avoid holding the lock
		go d.onChange(callbackEndpoints)
	}
}

// endpointsEqual compares two sorted endpoint lists.
func endpointsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// endpointsDiff returns added and removed endpoints.
func endpointsDiff(old, new []string) (added, removed []string) {
	oldSet := make(map[string]bool, len(old))
	for _, ep := range old {
		oldSet[ep] = true
	}

	newSet := make(map[string]bool, len(new))
	for _, ep := range new {
		newSet[ep] = true
	}

	for _, ep := range new {
		if !oldSet[ep] {
			added = append(added, ep)
		}
	}

	for _, ep := range old {
		if !newSet[ep] {
			removed = append(removed, ep)
		}
	}

	return
}

// ForceRefresh triggers an immediate refresh (useful for testing).
func (d *Discovery) ForceRefresh(ctx context.Context) {
	d.refresh(ctx)
}
