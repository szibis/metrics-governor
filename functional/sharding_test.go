package functional

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"

	colmetrics "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"

	"github.com/szibis/metrics-governor/internal/exporter"
	"github.com/szibis/metrics-governor/internal/sharding"
)

// mockResolver implements sharding.Resolver for testing
type mockResolver struct {
	hosts []string
}

func (m *mockResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	return m.hosts, nil
}

// mockShardBackend tracks metrics sent to each endpoint
type mockShardBackend struct {
	colmetrics.UnimplementedMetricsServiceServer
	addr            string
	mu              sync.Mutex
	receivedCount   int64
	receivedMetrics []*metricspb.ResourceMetrics
	server          *grpc.Server
}

func (m *mockShardBackend) Export(ctx context.Context, req *colmetrics.ExportMetricsServiceRequest) (*colmetrics.ExportMetricsServiceResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, rm := range req.ResourceMetrics {
		m.receivedMetrics = append(m.receivedMetrics, rm)
		atomic.AddInt64(&m.receivedCount, 1)
	}

	return &colmetrics.ExportMetricsServiceResponse{}, nil
}

func (m *mockShardBackend) getReceivedCount() int64 {
	return atomic.LoadInt64(&m.receivedCount)
}

func (m *mockShardBackend) getReceivedMetrics() []*metricspb.ResourceMetrics {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.receivedMetrics
}

func startMockShardBackend(t *testing.T) *mockShardBackend {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}

	backend := &mockShardBackend{
		addr: lis.Addr().String(),
	}

	server := grpc.NewServer()
	colmetrics.RegisterMetricsServiceServer(server, backend)
	backend.server = server

	go server.Serve(lis)

	return backend
}

func (m *mockShardBackend) stop() {
	if m.server != nil {
		m.server.Stop()
	}
}

func createShardedMetrics(metricName, service, env string, datapointCount int) *metricspb.ResourceMetrics {
	datapoints := make([]*metricspb.NumberDataPoint, datapointCount)
	for i := 0; i < datapointCount; i++ {
		datapoints[i] = &metricspb.NumberDataPoint{
			TimeUnixNano: uint64(time.Now().UnixNano()),
			Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)},
			Attributes: []*commonpb.KeyValue{
				{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: service}}},
				{Key: "env", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: env}}},
			},
		}
	}

	return &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: []*commonpb.KeyValue{
				{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: service}}},
			},
		},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{
				Metrics: []*metricspb.Metric{
					{
						Name: metricName,
						Data: &metricspb.Metric_Gauge{
							Gauge: &metricspb.Gauge{
								DataPoints: datapoints,
							},
						},
					},
				},
			},
		},
	}
}

// TestFunctional_Sharding_HashRingDistribution tests even distribution across endpoints
func TestFunctional_Sharding_HashRingDistribution(t *testing.T) {
	ring := sharding.NewHashRing(150) // 150 virtual nodes

	endpoints := []string{
		"10.0.0.1:8428",
		"10.0.0.2:8428",
		"10.0.0.3:8428",
	}
	ring.UpdateEndpoints(endpoints)

	// Test distribution of many keys
	distribution := make(map[string]int)
	keyCount := 10000

	for i := 0; i < keyCount; i++ {
		key := fmt.Sprintf("http_requests_total|service=api-%d|env=prod", i%100)
		endpoint := ring.GetEndpoint(key)
		distribution[endpoint]++
	}

	// Check distribution is roughly even (within 40% of average)
	average := keyCount / len(endpoints)
	tolerance := float64(average) * 0.4

	for endpoint, count := range distribution {
		diff := float64(count) - float64(average)
		if diff < 0 {
			diff = -diff
		}
		if diff > tolerance {
			t.Errorf("Endpoint %s has uneven distribution: %d (expected ~%d, tolerance %.0f)",
				endpoint, count, average, tolerance)
		}
		t.Logf("Endpoint %s: %d keys (%.1f%%)", endpoint, count, float64(count)/float64(keyCount)*100)
	}
}

// TestFunctional_Sharding_ConsistentHashing tests that same key goes to same endpoint
func TestFunctional_Sharding_ConsistentHashing(t *testing.T) {
	ring := sharding.NewHashRing(150)

	endpoints := []string{"ep1:8428", "ep2:8428", "ep3:8428"}
	ring.UpdateEndpoints(endpoints)

	// Same key should always map to same endpoint
	key := "http_requests_total|service=api|env=prod"
	expected := ring.GetEndpoint(key)

	for i := 0; i < 100; i++ {
		got := ring.GetEndpoint(key)
		if got != expected {
			t.Errorf("Inconsistent mapping: expected %s, got %s", expected, got)
		}
	}
}

// TestFunctional_Sharding_MinimalRehash tests that adding endpoint moves minimal keys
func TestFunctional_Sharding_MinimalRehash(t *testing.T) {
	ring := sharding.NewHashRing(150)

	// Start with 3 endpoints
	endpoints := []string{"ep1:8428", "ep2:8428", "ep3:8428"}
	ring.UpdateEndpoints(endpoints)

	// Map keys before adding endpoint
	keyCount := 1000
	mappingBefore := make(map[string]string)
	for i := 0; i < keyCount; i++ {
		key := fmt.Sprintf("metric_%d|service=svc|env=prod", i)
		mappingBefore[key] = ring.GetEndpoint(key)
	}

	// Add 4th endpoint
	endpoints = append(endpoints, "ep4:8428")
	ring.UpdateEndpoints(endpoints)

	// Check how many keys moved
	movedCount := 0
	for key, oldEndpoint := range mappingBefore {
		newEndpoint := ring.GetEndpoint(key)
		if newEndpoint != oldEndpoint {
			movedCount++
		}
	}

	// Should move approximately 1/4 of keys (25%)
	movedPercent := float64(movedCount) / float64(keyCount) * 100
	expectedPercent := 25.0
	tolerance := 10.0 // Allow 15-35%

	if movedPercent < expectedPercent-tolerance || movedPercent > expectedPercent+tolerance {
		t.Errorf("Moved %.1f%% of keys (expected ~%.0f%% +/- %.0f%%)", movedPercent, expectedPercent, tolerance)
	} else {
		t.Logf("Moved %.1f%% of keys (expected ~%.0f%%)", movedPercent, expectedPercent)
	}
}

// TestFunctional_Sharding_ShardKeyBuilder tests shard key construction
func TestFunctional_Sharding_ShardKeyBuilder(t *testing.T) {
	builder := sharding.NewShardKeyBuilder(sharding.ShardKeyConfig{
		Labels: []string{"service", "env"},
	})

	tests := []struct {
		metricName string
		attrs      map[string]string
		expected   string
	}{
		{
			metricName: "http_requests_total",
			attrs:      map[string]string{"service": "api", "env": "prod", "method": "GET"},
			expected:   "http_requests_total|env=prod|service=api", // sorted alphabetically
		},
		{
			metricName: "http_requests_total",
			attrs:      map[string]string{"service": "api"}, // missing env
			expected:   "http_requests_total|service=api",
		},
		{
			metricName: "db_queries",
			attrs:      map[string]string{}, // no labels
			expected:   "db_queries",
		},
	}

	for _, tt := range tests {
		got := builder.BuildKey(tt.metricName, tt.attrs)
		if got != tt.expected {
			t.Errorf("BuildKey(%s, %v) = %s, expected %s", tt.metricName, tt.attrs, got, tt.expected)
		}
	}
}

// TestFunctional_Sharding_MetricsSplitter tests splitting metrics by shard
func TestFunctional_Sharding_MetricsSplitter(t *testing.T) {
	ring := sharding.NewHashRing(150)
	endpoints := []string{"ep1:8428", "ep2:8428", "ep3:8428"}
	ring.UpdateEndpoints(endpoints)

	keyBuilder := sharding.NewShardKeyBuilder(sharding.ShardKeyConfig{
		Labels: []string{"service"},
	})

	splitter := sharding.NewMetricsSplitter(keyBuilder, ring)

	// Create metrics from different services
	metrics := []*metricspb.ResourceMetrics{
		createShardedMetrics("http_requests", "api", "prod", 5),
		createShardedMetrics("http_requests", "web", "prod", 5),
		createShardedMetrics("http_requests", "worker", "prod", 5),
	}

	shards := splitter.Split(metrics)

	// Should have distributed across endpoints
	totalDatapoints := 0
	for endpoint, rms := range shards {
		count := 0
		for _, rm := range rms {
			count += sharding.CountDatapoints([]*metricspb.ResourceMetrics{rm})
		}
		totalDatapoints += count
		t.Logf("Endpoint %s: %d datapoints", endpoint, count)
	}

	expectedDatapoints := 15 // 3 services * 5 datapoints
	if totalDatapoints != expectedDatapoints {
		t.Errorf("Expected %d total datapoints, got %d", expectedDatapoints, totalDatapoints)
	}
}

// TestFunctional_Sharding_ShardedExporter tests sharded exporter with multiple backends
func TestFunctional_Sharding_ShardedExporter(t *testing.T) {
	// Start multiple mock backends
	backends := make([]*mockShardBackend, 3)
	endpoints := make([]string, 3)

	for i := 0; i < 3; i++ {
		backends[i] = startMockShardBackend(t)
		endpoints[i] = backends[i].addr
		defer backends[i].stop()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create sharded exporter with static endpoints (simulating DNS discovery)
	cfg := exporter.ShardedExporterConfig{
		BaseConfig: exporter.Config{
			Protocol: "grpc",
			Insecure: true,
			Timeout:  5 * time.Second,
		},
		Labels:       []string{"service"},
		VirtualNodes: 150,
	}

	// Note: In real usage, discovery would populate endpoints
	// For testing, we'll use the hash ring directly
	ring := sharding.NewHashRing(150)
	ring.UpdateEndpoints(endpoints)

	keyBuilder := sharding.NewShardKeyBuilder(sharding.ShardKeyConfig{
		Labels: cfg.Labels,
	})

	splitter := sharding.NewMetricsSplitter(keyBuilder, ring)

	// Create and split metrics
	services := []string{"api", "web", "worker", "scheduler", "cron"}
	var allMetrics []*metricspb.ResourceMetrics

	for _, svc := range services {
		for i := 0; i < 10; i++ {
			rm := createShardedMetrics("http_requests", svc, "prod", 5)
			allMetrics = append(allMetrics, rm)
		}
	}

	shards := splitter.Split(allMetrics)

	// Export each shard to its endpoint
	for endpoint, rms := range shards {
		// Find the backend for this endpoint
		var backend *mockShardBackend
		for _, b := range backends {
			if b.addr == endpoint {
				backend = b
				break
			}
		}

		if backend == nil {
			t.Logf("Endpoint %s not in test backends (OK for hash ring distribution)", endpoint)
			continue
		}

		// Create exporter for this endpoint
		exp, err := exporter.New(ctx, exporter.Config{
			Endpoint: endpoint,
			Protocol: "grpc",
			Insecure: true,
			Timeout:  5 * time.Second,
		})
		if err != nil {
			t.Fatalf("Failed to create exporter: %v", err)
		}

		req := &colmetrics.ExportMetricsServiceRequest{
			ResourceMetrics: rms,
		}
		if err := exp.Export(ctx, req); err != nil {
			t.Errorf("Export failed: %v", err)
		}
		exp.Close()
	}

	// Check distribution across backends
	var totalReceived int64
	for i, backend := range backends {
		count := backend.getReceivedCount()
		totalReceived += count
		t.Logf("Backend %d (%s): received %d metrics", i, backend.addr, count)
	}

	t.Logf("Total metrics distributed: %d", totalReceived)
}

// TestFunctional_Sharding_Discovery tests DNS discovery mock
func TestFunctional_Sharding_Discovery(t *testing.T) {
	// Create mock resolver
	resolver := &mockResolver{
		hosts: []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"},
	}

	var discoveredEndpoints []string
	var mu sync.Mutex

	onChange := func(endpoints []string) {
		mu.Lock()
		discoveredEndpoints = endpoints
		mu.Unlock()
	}

	cfg := sharding.DiscoveryConfig{
		HeadlessService: "test-service.namespace.svc:8428",
		RefreshInterval: 100 * time.Millisecond,
		Timeout:         1 * time.Second,
	}

	discovery := sharding.NewDiscoveryWithResolver(cfg, onChange, resolver)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	discovery.Start(ctx)

	// Wait for discovery
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	endpointCount := len(discoveredEndpoints)
	mu.Unlock()

	if endpointCount != 3 {
		t.Errorf("Expected 3 discovered endpoints, got %d", endpointCount)
	}

	discovery.Stop()
}

// TestFunctional_Sharding_EndpointChange tests behavior when endpoints change
func TestFunctional_Sharding_EndpointChange(t *testing.T) {
	ring := sharding.NewHashRing(150)

	// Initial endpoints
	endpoints := []string{"ep1:8428", "ep2:8428", "ep3:8428"}
	ring.UpdateEndpoints(endpoints)

	// Map some keys
	key1 := "metric_1|service=api"
	key2 := "metric_2|service=web"

	ep1Before := ring.GetEndpoint(key1)
	ep2Before := ring.GetEndpoint(key2)

	// Remove one endpoint
	endpoints = []string{"ep1:8428", "ep3:8428"}
	ring.UpdateEndpoints(endpoints)

	ep1After := ring.GetEndpoint(key1)
	ep2After := ring.GetEndpoint(key2)

	// Keys that were on removed endpoint should move
	// Keys on remaining endpoints should mostly stay
	t.Logf("Key1: %s -> %s", ep1Before, ep1After)
	t.Logf("Key2: %s -> %s", ep2Before, ep2After)

	// Both should map to valid endpoints
	validEndpoints := map[string]bool{"ep1:8428": true, "ep3:8428": true}
	if !validEndpoints[ep1After] {
		t.Errorf("Key1 mapped to invalid endpoint: %s", ep1After)
	}
	if !validEndpoints[ep2After] {
		t.Errorf("Key2 mapped to invalid endpoint: %s", ep2After)
	}
}

// TestFunctional_Sharding_HighThroughput tests sharding under high load
func TestFunctional_Sharding_HighThroughput(t *testing.T) {
	ring := sharding.NewHashRing(150)
	endpoints := []string{"ep1:8428", "ep2:8428", "ep3:8428", "ep4:8428", "ep5:8428"}
	ring.UpdateEndpoints(endpoints)

	keyBuilder := sharding.NewShardKeyBuilder(sharding.ShardKeyConfig{
		Labels: []string{"service", "env"},
	})

	splitter := sharding.NewMetricsSplitter(keyBuilder, ring)

	// High volume splitting
	start := time.Now()
	metricsCount := 10000

	var allMetrics []*metricspb.ResourceMetrics
	for i := 0; i < metricsCount; i++ {
		service := fmt.Sprintf("service-%d", i%50)
		env := []string{"prod", "staging", "dev"}[i%3]
		rm := createShardedMetrics("test_metric", service, env, 5)
		allMetrics = append(allMetrics, rm)
	}

	shards := splitter.Split(allMetrics)

	elapsed := time.Since(start)
	t.Logf("Split %d metrics in %v (%.0f metrics/sec)", metricsCount, elapsed, float64(metricsCount)/elapsed.Seconds())

	// Verify all datapoints accounted for
	totalDatapoints := 0
	for endpoint, rms := range shards {
		count := 0
		for _, rm := range rms {
			count += sharding.CountDatapoints([]*metricspb.ResourceMetrics{rm})
		}
		totalDatapoints += count
		t.Logf("Endpoint %s: %d datapoints (%.1f%%)", endpoint, count, float64(count)/float64(metricsCount*5)*100)
	}

	expectedDatapoints := metricsCount * 5
	if totalDatapoints != expectedDatapoints {
		t.Errorf("Expected %d total datapoints, got %d", expectedDatapoints, totalDatapoints)
	}
}
