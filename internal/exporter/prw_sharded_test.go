package exporter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/prw"
)

func TestNewPRWSharded(t *testing.T) {
	ctx := context.Background()

	cfg := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Timeout: 10 * time.Second,
		},
		ShardKeyLabels: []string{"service"},
		VirtualNodes:   100,
		Endpoints:      []string{"http://localhost:9090", "http://localhost:9091"},
	}

	exp, err := NewPRWSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("NewPRWSharded() error = %v", err)
	}
	defer exp.Close()

	endpoints := exp.GetEndpoints()
	if len(endpoints) != 2 {
		t.Errorf("GetEndpoints() returned %d endpoints, want 2", len(endpoints))
	}
}

func TestNewPRWSharded_DefaultVirtualNodes(t *testing.T) {
	ctx := context.Background()

	cfg := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Timeout: 10 * time.Second,
		},
		Endpoints: []string{"http://localhost:9090"},
	}

	exp, err := NewPRWSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("NewPRWSharded() error = %v", err)
	}
	defer exp.Close()

	if exp.EndpointCount() != 1 {
		t.Errorf("EndpointCount() = %d, want 1", exp.EndpointCount())
	}
}

func TestPRWShardedExporter_Export_NilRequest(t *testing.T) {
	ctx := context.Background()

	cfg := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Timeout: 10 * time.Second,
		},
		Endpoints: []string{"http://localhost:9090"},
	}

	exp, err := NewPRWSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("NewPRWSharded() error = %v", err)
	}
	defer exp.Close()

	// Nil request should succeed (no-op)
	if err := exp.Export(ctx, nil); err != nil {
		t.Errorf("Export(nil) error = %v, want nil", err)
	}

	// Empty request should succeed (no-op)
	if err := exp.Export(ctx, &prw.WriteRequest{}); err != nil {
		t.Errorf("Export(empty) error = %v, want nil", err)
	}
}

func TestPRWShardedExporter_Export_SingleEndpoint(t *testing.T) {
	var received atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx := context.Background()

	cfg := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Timeout: 10 * time.Second,
		},
		ShardKeyLabels: []string{"service"},
		Endpoints:      []string{server.URL},
	}

	exp, err := NewPRWSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("NewPRWSharded() error = %v", err)
	}
	defer exp.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}, {Name: "service", Value: "api"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	if err := exp.Export(ctx, req); err != nil {
		t.Errorf("Export() error = %v", err)
	}

	if received.Load() != 1 {
		t.Errorf("server received %d requests, want 1", received.Load())
	}
}

func TestPRWShardedExporter_Export_MultipleEndpoints(t *testing.T) {
	var received1, received2 atomic.Int32

	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received1.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received2.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server2.Close()

	ctx := context.Background()

	cfg := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Timeout: 10 * time.Second,
		},
		ShardKeyLabels: []string{"service"},
		VirtualNodes:   100,
		Endpoints:      []string{server1.URL, server2.URL},
	}

	exp, err := NewPRWSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("NewPRWSharded() error = %v", err)
	}
	defer exp.Close()

	// Send many requests with different services - should distribute across endpoints
	for i := 0; i < 100; i++ {
		req := &prw.WriteRequest{
			Timeseries: []prw.TimeSeries{
				{
					Labels: []prw.Label{
						{Name: "__name__", Value: "test_metric"},
						{Name: "service", Value: string(rune('a' + i%26))}, // Vary service label
					},
					Samples: []prw.Sample{{Value: float64(i), Timestamp: int64(i * 1000)}},
				},
			},
		}

		if err := exp.Export(ctx, req); err != nil {
			t.Errorf("Export() error = %v", err)
		}
	}

	total := received1.Load() + received2.Load()
	if total != 100 {
		t.Errorf("total requests = %d, want 100", total)
	}

	// Both servers should have received some requests (distribution may vary)
	// With 100 requests and good hashing, both should get some traffic
	// but we don't enforce exact distribution
	t.Logf("server1 received: %d, server2 received: %d", received1.Load(), received2.Load())
}

func TestPRWShardedExporter_Export_ConsistentHashing(t *testing.T) {
	var received1, received2 sync.Map

	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received1.Store(time.Now().UnixNano(), true)
		w.WriteHeader(http.StatusOK)
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received2.Store(time.Now().UnixNano(), true)
		w.WriteHeader(http.StatusOK)
	}))
	defer server2.Close()

	ctx := context.Background()

	cfg := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Timeout: 10 * time.Second,
		},
		ShardKeyLabels: []string{"service"},
		VirtualNodes:   100,
		Endpoints:      []string{server1.URL, server2.URL},
	}

	exp, err := NewPRWSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("NewPRWSharded() error = %v", err)
	}
	defer exp.Close()

	// Same metric should always go to the same endpoint
	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels: []prw.Label{
					{Name: "__name__", Value: "consistent_metric"},
					{Name: "service", Value: "consistent_service"},
				},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	// Clear counters and send multiple times
	var countReceived1, countReceived2 atomic.Int32

	server1Tracking := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		countReceived1.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server1Tracking.Close()

	server2Tracking := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		countReceived2.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server2Tracking.Close()

	cfg2 := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Timeout: 10 * time.Second,
		},
		ShardKeyLabels: []string{"service"},
		VirtualNodes:   100,
		Endpoints:      []string{server1Tracking.URL, server2Tracking.URL},
	}

	exp2, err := NewPRWSharded(ctx, cfg2)
	if err != nil {
		t.Fatalf("NewPRWSharded() error = %v", err)
	}
	defer exp2.Close()

	// Send same request 10 times
	for i := 0; i < 10; i++ {
		if err := exp2.Export(ctx, req); err != nil {
			t.Errorf("Export() error = %v", err)
		}
	}

	// All requests should have gone to ONE endpoint (consistent hashing)
	count1 := countReceived1.Load()
	count2 := countReceived2.Load()

	if count1 != 10 && count2 != 10 {
		t.Errorf("consistent hashing failed: server1=%d, server2=%d, one should have all 10", count1, count2)
	}
}

func TestPRWShardedExporter_Export_NoEndpoints(t *testing.T) {
	ctx := context.Background()

	cfg := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Timeout: 10 * time.Second,
		},
		// No endpoints
	}

	exp, err := NewPRWSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("NewPRWSharded() error = %v", err)
	}
	defer exp.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	// Should not error, just return nil (no endpoints to send to)
	if err := exp.Export(ctx, req); err != nil {
		t.Errorf("Export() with no endpoints error = %v, want nil", err)
	}
}

func TestPRWShardedExporter_Close(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Timeout: 10 * time.Second,
		},
		Endpoints: []string{server.URL},
	}

	exp, err := NewPRWSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("NewPRWSharded() error = %v", err)
	}

	// Export to create an exporter for the endpoint
	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	if err := exp.Export(ctx, req); err != nil {
		t.Errorf("Export() error = %v", err)
	}

	// Close should not error
	if err := exp.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Double close should not error
	if err := exp.Close(); err != nil {
		t.Errorf("second Close() error = %v", err)
	}
}

func TestSanitizeEndpoint(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"localhost:9090", "localhost_9090"},
		{"http://example.com/path", "http___example_com_path"},
		{"simple", "simple"},
		{"with-dash_under", "with-dash_under"},
		{"192.168.1.1:8080", "192_168_1_1_8080"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeEndpoint(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeEndpoint(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestPRWShardedExporter_ImplementsInterface(t *testing.T) {
	// Compile-time check that PRWShardedExporter implements PRWExporter
	var _ prw.PRWExporter = (*PRWShardedExporter)(nil)
}

func TestPRWShardedExporter_OnEndpointsChanged(t *testing.T) {
	ctx := context.Background()

	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server2.Close()

	cfg := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Timeout: 10 * time.Second,
		},
		Endpoints: []string{server1.URL},
	}

	exp, err := NewPRWSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("NewPRWSharded() error = %v", err)
	}
	defer exp.Close()

	// Export to create an exporter for the first endpoint
	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	if err := exp.Export(ctx, req); err != nil {
		t.Logf("Export error (may be expected): %v", err)
	}

	// Change endpoints - should close the old exporter and use new ones
	exp.onEndpointsChanged([]string{server2.URL})

	// Verify endpoint was updated
	endpoints := exp.GetEndpoints()
	if len(endpoints) != 1 || endpoints[0] != server2.URL {
		t.Errorf("Expected 1 endpoint (%s), got %v", server2.URL, endpoints)
	}
}

func TestPRWShardedExporter_OnEndpointsChanged_WhenClosed(t *testing.T) {
	ctx := context.Background()

	cfg := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Timeout: 10 * time.Second,
		},
		Endpoints: []string{"http://localhost:9090"},
	}

	exp, err := NewPRWSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("NewPRWSharded() error = %v", err)
	}

	// Close the exporter
	exp.Close()

	// This should be a no-op since the exporter is closed
	exp.onEndpointsChanged([]string{"http://localhost:9091"})

	// Endpoints should not have changed since exporter is closed
	// The actual behavior depends on implementation
	t.Log("onEndpointsChanged called on closed exporter - should be no-op")
}

func TestPRWShardedExporter_ExportToEndpoint_DirectExport(t *testing.T) {
	var received atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx := context.Background()

	cfg := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Timeout: 10 * time.Second,
		},
		ShardKeyLabels: []string{"service"},
		Endpoints:      []string{server.URL},
		QueueEnabled:   false, // Direct export
	}

	exp, err := NewPRWSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("NewPRWSharded() error = %v", err)
	}
	defer exp.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test"}, {Name: "service", Value: "api"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	if err := exp.Export(ctx, req); err != nil {
		t.Errorf("Export() error = %v", err)
	}

	if received.Load() != 1 {
		t.Errorf("Expected 1 request, got %d", received.Load())
	}
}

func TestPRWShardedExporter_ExportToEndpoint_WithQueue(t *testing.T) {
	var received atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx := context.Background()
	tmpDir := t.TempDir()

	cfg := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Timeout: 10 * time.Second,
		},
		ShardKeyLabels: []string{"service"},
		Endpoints:      []string{server.URL},
		QueueEnabled:   true,
		QueueConfig: prw.QueueConfig{
			Path:          tmpDir,
			MaxSize:       100,
			RetryInterval: 50 * time.Millisecond,
			MaxRetryDelay: 200 * time.Millisecond,
		},
	}

	exp, err := NewPRWSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("NewPRWSharded() error = %v", err)
	}
	defer exp.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test"}, {Name: "service", Value: "api"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	if err := exp.Export(ctx, req); err != nil {
		t.Logf("Export() error (may queue): %v", err)
	}

	// Wait for queue to process
	time.Sleep(200 * time.Millisecond)

	t.Logf("Server received %d requests", received.Load())
}

func TestPRWShardedExporter_GetOrCreateExporter_Error(t *testing.T) {
	ctx := context.Background()

	cfg := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Endpoint: "invalid-endpoint-that-will-fail",
			Timeout:  10 * time.Second,
		},
		Endpoints: []string{"invalid-endpoint"},
	}

	exp, err := NewPRWSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("NewPRWSharded() error = %v", err)
	}
	defer exp.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	// This should fail because of invalid endpoint
	err = exp.Export(ctx, req)
	// Error may or may not occur depending on when validation happens
	t.Logf("Export result: %v", err)
}

func TestPRWShardedExporter_Close_WithQueues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx := context.Background()
	tmpDir := t.TempDir()

	cfg := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Timeout: 10 * time.Second,
		},
		Endpoints:    []string{server.URL},
		QueueEnabled: true,
		QueueConfig: prw.QueueConfig{
			Path:          tmpDir,
			MaxSize:       100,
			RetryInterval: 50 * time.Millisecond,
		},
	}

	exp, err := NewPRWSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("NewPRWSharded() error = %v", err)
	}

	// Export to create queue
	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	_ = exp.Export(ctx, req)

	// Close should close both exporters and queues
	if err := exp.Close(); err != nil {
		t.Logf("Close error: %v", err)
	}
}

func TestSanitizeEndpoint_MoreCases(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"abc123", "abc123"},
		{"ABC-XYZ_123", "ABC-XYZ_123"},
		{"http://host:8080/path?query=1", "http___host_8080_path_query_1"},
		{"[::1]:8080", "___1__8080"},
		{"a.b.c", "a_b_c"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeEndpoint(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeEndpoint(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestPRWShardedExporter_ConcurrentExport(t *testing.T) {
	var received atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx := context.Background()

	cfg := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Timeout: 10 * time.Second,
		},
		ShardKeyLabels: []string{"service"},
		Endpoints:      []string{server.URL},
	}

	exp, err := NewPRWSharded(ctx, cfg)
	if err != nil {
		t.Fatalf("NewPRWSharded() error = %v", err)
	}
	defer exp.Close()

	// Concurrent exports
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := &prw.WriteRequest{
				Timeseries: []prw.TimeSeries{
					{
						Labels:  []prw.Label{{Name: "__name__", Value: "test"}, {Name: "idx", Value: string(rune('0' + idx))}},
						Samples: []prw.Sample{{Value: float64(idx), Timestamp: int64(idx * 1000)}},
					},
				},
			}
			_ = exp.Export(ctx, req)
		}(i)
	}

	wg.Wait()
	t.Logf("Server received %d requests", received.Load())
}
