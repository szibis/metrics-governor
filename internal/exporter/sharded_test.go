package exporter

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/sharding"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// MockExporterFactory creates mock exporters for testing.
type MockExporterFactory struct {
	mu        sync.Mutex
	exporters map[string]*MockExporter
	callCount int32
}

func NewMockExporterFactory() *MockExporterFactory {
	return &MockExporterFactory{
		exporters: make(map[string]*MockExporter),
	}
}

func (f *MockExporterFactory) GetExporter(endpoint string) *MockExporter {
	f.mu.Lock()
	defer f.mu.Unlock()

	if exp, ok := f.exporters[endpoint]; ok {
		return exp
	}

	exp := NewMockExporter()
	f.exporters[endpoint] = exp
	return exp
}

// MockExporter is a mock exporter for testing.
type MockExporter struct {
	mu        sync.Mutex
	exports   []*colmetricspb.ExportMetricsServiceRequest
	err       error
	closed    bool
	exportFn  func(*colmetricspb.ExportMetricsServiceRequest) error
	callCount int32
}

func NewMockExporter() *MockExporter {
	return &MockExporter{
		exports: make([]*colmetricspb.ExportMetricsServiceRequest, 0),
	}
}

func (m *MockExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	atomic.AddInt32(&m.callCount, 1)

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return errors.New("exporter is closed")
	}

	m.exports = append(m.exports, req)

	if m.exportFn != nil {
		return m.exportFn(req)
	}

	return m.err
}

func (m *MockExporter) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *MockExporter) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

func (m *MockExporter) GetExports() []*colmetricspb.ExportMetricsServiceRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*colmetricspb.ExportMetricsServiceRequest, len(m.exports))
	copy(result, m.exports)
	return result
}

func (m *MockExporter) GetCallCount() int32 {
	return atomic.LoadInt32(&m.callCount)
}

func (m *MockExporter) IsClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// Helper functions

func stringAttr(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key:   key,
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}},
	}
}

func createGaugeDataPoint(attrs ...*commonpb.KeyValue) *metricspb.NumberDataPoint {
	return &metricspb.NumberDataPoint{
		Attributes:   attrs,
		TimeUnixNano: 1000000000,
		Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: 42.0},
	}
}

func createGaugeMetric(name string, datapoints ...*metricspb.NumberDataPoint) *metricspb.Metric {
	return &metricspb.Metric{
		Name: name,
		Data: &metricspb.Metric_Gauge{
			Gauge: &metricspb.Gauge{
				DataPoints: datapoints,
			},
		},
	}
}

func createResourceMetrics(resourceAttrs []*commonpb.KeyValue, metrics ...*metricspb.Metric) *metricspb.ResourceMetrics {
	return &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: resourceAttrs,
		},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{
				Scope: &commonpb.InstrumentationScope{
					Name:    "test",
					Version: "1.0",
				},
				Metrics: metrics,
			},
		},
	}
}

func createExportRequest(rms ...*metricspb.ResourceMetrics) *colmetricspb.ExportMetricsServiceRequest {
	return &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: rms,
	}
}

// Tests

func TestShardedExporter_NewSharded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := ShardedExporterConfig{
		BaseConfig: Config{
			Endpoint: "fallback:8428",
			Protocol: ProtocolGRPC,
			Insecure: true,
		},
		HeadlessService:    "vminsert.monitoring.svc:8428",
		DNSRefreshInterval: 100 * time.Millisecond,
		DNSTimeout:         1 * time.Second,
		Labels:             []string{"service"},
		VirtualNodes:       100,
		FallbackOnEmpty:    true,
	}

	exp, err := NewSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer exp.Close()

	if exp == nil {
		t.Fatal("expected non-nil exporter")
	}
}

func TestShardedExporter_NewSharded_AppliesDefaults(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := ShardedExporterConfig{
		BaseConfig: Config{
			Endpoint: "fallback:8428",
			Protocol: ProtocolGRPC,
			Insecure: true,
		},
		HeadlessService: "vminsert.svc:8428",
	}

	exp, err := NewSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer exp.Close()

	if exp.config.DNSRefreshInterval != 30*time.Second {
		t.Errorf("expected default DNSRefreshInterval=30s, got %v", exp.config.DNSRefreshInterval)
	}
	if exp.config.DNSTimeout != 5*time.Second {
		t.Errorf("expected default DNSTimeout=5s, got %v", exp.config.DNSTimeout)
	}
	if exp.config.VirtualNodes != sharding.DefaultVirtualNodes {
		t.Errorf("expected default VirtualNodes=%d, got %d", sharding.DefaultVirtualNodes, exp.config.VirtualNodes)
	}
}

func TestShardedExporter_Export_EmptyRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := ShardedExporterConfig{
		BaseConfig: Config{
			Endpoint: "fallback:8428",
			Protocol: ProtocolGRPC,
			Insecure: true,
		},
		HeadlessService: "vminsert.svc:8428",
		FallbackOnEmpty: true,
	}

	exp, err := NewSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer exp.Close()

	// Nil request
	err = exp.Export(ctx, nil)
	if err != nil {
		t.Errorf("expected no error for nil request, got %v", err)
	}

	// Empty request
	err = exp.Export(ctx, &colmetricspb.ExportMetricsServiceRequest{})
	if err != nil {
		t.Errorf("expected no error for empty request, got %v", err)
	}
}

func TestShardedExporter_EndpointCount(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := ShardedExporterConfig{
		BaseConfig: Config{
			Endpoint: "fallback:8428",
			Protocol: ProtocolGRPC,
			Insecure: true,
		},
		HeadlessService: "vminsert.svc:8428",
	}

	exp, err := NewSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer exp.Close()

	// Initially empty (DNS resolution pending)
	// Note: Count may be 0 or fallback depending on DNS timing
	count := exp.EndpointCount()
	if count < 0 {
		t.Errorf("unexpected negative endpoint count: %d", count)
	}
}

func TestShardedExporter_GetEndpoints(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := ShardedExporterConfig{
		BaseConfig: Config{
			Endpoint: "fallback:8428",
			Protocol: ProtocolGRPC,
			Insecure: true,
		},
		HeadlessService: "vminsert.svc:8428",
	}

	exp, err := NewSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer exp.Close()

	endpoints := exp.GetEndpoints()
	if endpoints == nil {
		t.Error("expected non-nil endpoints slice")
	}
}

func TestShardedExporter_Close(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := ShardedExporterConfig{
		BaseConfig: Config{
			Endpoint: "fallback:8428",
			Protocol: ProtocolGRPC,
			Insecure: true,
		},
		HeadlessService: "vminsert.svc:8428",
	}

	exp, err := NewSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Close should not panic or hang
	done := make(chan struct{})
	go func() {
		exp.Close()
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(5 * time.Second):
		t.Error("Close took too long")
	}
}

func TestShardedExporter_Close_Idempotent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := ShardedExporterConfig{
		BaseConfig: Config{
			Endpoint: "fallback:8428",
			Protocol: ProtocolGRPC,
			Insecure: true,
		},
		HeadlessService: "vminsert.svc:8428",
	}

	exp, err := NewSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Multiple closes should not panic
	exp.Close()
	exp.Close()
	exp.Close()
}

func TestShardedExporter_onEndpointsChanged(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := ShardedExporterConfig{
		BaseConfig: Config{
			Endpoint: "fallback:8428",
			Protocol: ProtocolGRPC,
			Insecure: true,
		},
		HeadlessService: "vminsert.svc:8428",
		VirtualNodes:    100,
	}

	exp, err := NewSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer exp.Close()

	// Simulate endpoint change
	exp.onEndpointsChanged([]string{"10.0.0.1:8428", "10.0.0.2:8428"})

	endpoints := exp.GetEndpoints()
	if len(endpoints) != 2 {
		t.Errorf("expected 2 endpoints, got %d", len(endpoints))
	}
}

func TestShardedExporter_getOrCreateExporter_Cached(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := ShardedExporterConfig{
		BaseConfig: Config{
			Endpoint: "fallback:8428",
			Protocol: ProtocolGRPC,
			Insecure: true,
		},
		HeadlessService: "vminsert.svc:8428",
	}

	exp, err := NewSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer exp.Close()

	// Get exporter twice - should return same instance
	exp1 := exp.getOrCreateExporter("test:8428")
	exp2 := exp.getOrCreateExporter("test:8428")

	if exp1 != exp2 {
		t.Error("expected cached exporter")
	}
}

func TestShardedExporter_cleanupStaleExporters(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := ShardedExporterConfig{
		BaseConfig: Config{
			Endpoint: "fallback:8428",
			Protocol: ProtocolGRPC,
			Insecure: true,
		},
		HeadlessService: "vminsert.svc:8428",
	}

	exp, err := NewSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer exp.Close()

	// Create exporters for endpoints
	_ = exp.getOrCreateExporter("10.0.0.1:8428")
	_ = exp.getOrCreateExporter("10.0.0.2:8428")
	_ = exp.getOrCreateExporter("10.0.0.3:8428")

	exp.mu.RLock()
	initialCount := len(exp.exporters)
	exp.mu.RUnlock()

	if initialCount != 3 {
		t.Errorf("expected 3 exporters, got %d", initialCount)
	}

	// Cleanup - keep only 2 endpoints
	exp.cleanupStaleExporters([]string{"10.0.0.1:8428", "10.0.0.2:8428"})

	exp.mu.RLock()
	finalCount := len(exp.exporters)
	exp.mu.RUnlock()

	if finalCount != 2 {
		t.Errorf("expected 2 exporters after cleanup, got %d", finalCount)
	}
}

// Helper function tests

func TestHashEndpoint(t *testing.T) {
	// Same endpoint should produce same hash
	hash1 := hashEndpoint("10.0.0.1:8428")
	hash2 := hashEndpoint("10.0.0.1:8428")
	if hash1 != hash2 {
		t.Errorf("expected same hash, got %s and %s", hash1, hash2)
	}

	// Different endpoints should produce different hashes
	hash3 := hashEndpoint("10.0.0.2:8428")
	if hash1 == hash3 {
		t.Error("expected different hashes for different endpoints")
	}

	// Hash should be 16 characters (8 bytes hex encoded)
	if len(hash1) != 16 {
		t.Errorf("expected hash length 16, got %d", len(hash1))
	}
}

func TestCollectErrors(t *testing.T) {
	tests := []struct {
		name     string
		errors   []error
		hasError bool
	}{
		{
			name:     "no errors",
			errors:   []error{},
			hasError: false,
		},
		{
			name:     "single error",
			errors:   []error{errors.New("error 1")},
			hasError: true,
		},
		{
			name:     "multiple errors",
			errors:   []error{errors.New("error 1"), errors.New("error 2")},
			hasError: true,
		},
		{
			name:     "nil errors",
			errors:   []error{nil, nil},
			hasError: false,
		},
		{
			name:     "mixed errors",
			errors:   []error{nil, errors.New("error 1"), nil},
			hasError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			errCh := make(chan error, len(tc.errors))
			for _, err := range tc.errors {
				errCh <- err
			}
			close(errCh)

			result := collectErrors(errCh)
			if tc.hasError && result == nil {
				t.Error("expected error, got nil")
			}
			if !tc.hasError && result != nil {
				t.Errorf("expected no error, got %v", result)
			}
		})
	}
}

func TestErrorExporter(t *testing.T) {
	exp := &errorExporter{
		endpoint: "test:8428",
		err:      errors.New("init error"),
	}

	err := exp.Export(context.Background(), nil)
	if err == nil {
		t.Error("expected error")
	}
	if exp.Close() != nil {
		t.Error("expected nil from Close")
	}
}

// Benchmarks

func BenchmarkHashEndpoint(b *testing.B) {
	endpoint := "vminsert-0.vminsert-headless.monitoring.svc.cluster.local:8428"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hashEndpoint(endpoint)
	}
}
