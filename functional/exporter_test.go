package functional

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"

	"github.com/slawomirskowron/metrics-governor/internal/exporter"
)

// TestFunctional_Exporter_GRPC tests exporter with gRPC protocol
func TestFunctional_Exporter_GRPC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Start mock gRPC backend
	backend, addr := startMockBackend(t)
	defer backend.Stop()

	// Create exporter
	exp, err := exporter.New(ctx, exporter.Config{
		Endpoint: addr,
		Protocol: "grpc",
		Insecure: true,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	// Export metrics
	req := createTestExportRequest("exporter-grpc-test", "exporter-grpc-metric", 5)
	err = exp.Export(ctx, req)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Verify backend received metrics
	received := backend.GetReceived()
	if len(received) == 0 {
		t.Fatal("Backend did not receive metrics")
	}

	// Verify content
	found := false
	for _, rm := range received {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if m.Name == "exporter-grpc-metric" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Error("Expected metric not found in backend")
	}
}

// TestFunctional_Exporter_HTTP tests exporter with HTTP protocol
func TestFunctional_Exporter_HTTP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Start mock HTTP backend
	var received []*metricspb.ResourceMetrics
	var mu sync.Mutex

	httpBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/metrics" {
			t.Errorf("Expected /v1/metrics, got %s", r.URL.Path)
		}

		body := make([]byte, r.ContentLength)
		r.Body.Read(body)

		var req colmetrics.ExportMetricsServiceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			t.Errorf("Failed to unmarshal: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		mu.Lock()
		received = append(received, req.ResourceMetrics...)
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	defer httpBackend.Close()

	// Create exporter
	exp, err := exporter.New(ctx, exporter.Config{
		Endpoint: httpBackend.URL,
		Protocol: "http",
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	// Export metrics
	req := createTestExportRequest("exporter-http-test", "exporter-http-metric", 3)
	err = exp.Export(ctx, req)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Verify
	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Fatal("Backend did not receive metrics")
	}
}

// TestFunctional_Exporter_Timeout tests exporter timeout handling
func TestFunctional_Exporter_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start slow backend
	slowBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(3 * time.Second) // Slow response
		w.WriteHeader(http.StatusOK)
	}))
	defer slowBackend.Close()

	// Create exporter with short timeout
	exp, err := exporter.New(ctx, exporter.Config{
		Endpoint: slowBackend.URL,
		Protocol: "http",
		Timeout:  500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	// Export should timeout
	req := createTestExportRequest("timeout-test", "timeout-metric", 1)
	err = exp.Export(ctx, req)
	if err == nil {
		t.Error("Expected timeout error")
	}
}

// TestFunctional_Exporter_LargePayload tests exporter with large payloads
func TestFunctional_Exporter_LargePayload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start mock backend
	backend, addr := startMockBackend(t)
	defer backend.Stop()

	// Create exporter
	exp, err := exporter.New(ctx, exporter.Config{
		Endpoint: addr,
		Protocol: "grpc",
		Insecure: true,
		Timeout:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	// Create large payload (multiple metrics with many datapoints)
	req := createTestExportRequestWithMultipleMetrics("large-payload-test", 100, 100)

	err = exp.Export(ctx, req)
	if err != nil {
		t.Fatalf("Large payload export failed: %v", err)
	}

	// Verify
	received := backend.GetReceived()
	if len(received) == 0 {
		t.Fatal("Backend did not receive large payload")
	}
	t.Logf("Successfully exported %d resource metrics", len(received))
}

// TestFunctional_Exporter_ConcurrentExports tests concurrent exports
func TestFunctional_Exporter_ConcurrentExports(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start mock backend
	backend, addr := startMockBackend(t)
	defer backend.Stop()

	// Create exporter
	exp, err := exporter.New(ctx, exporter.Config{
		Endpoint: addr,
		Protocol: "grpc",
		Insecure: true,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	// Launch concurrent exports
	numGoroutines := 10
	exportsPerGoroutine := 50
	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines*exportsPerGoroutine)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < exportsPerGoroutine; j++ {
				req := createTestExportRequest("concurrent-test", "concurrent-metric", 1)
				if err := exp.Export(ctx, req); err != nil {
					errors <- err
				}
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	errorCount := 0
	for err := range errors {
		t.Logf("Export error: %v", err)
		errorCount++
	}

	if errorCount > 0 {
		t.Errorf("Had %d errors during concurrent exports", errorCount)
	}

	// Verify backend received metrics
	received := backend.GetReceived()
	expectedMin := numGoroutines * exportsPerGoroutine / 2 // Allow some failures
	if len(received) < expectedMin {
		t.Errorf("Expected at least %d metrics, got %d", expectedMin, len(received))
	}
	t.Logf("Backend received %d resource metrics from concurrent exports", len(received))
}

// Helper types and functions

type mockGRPCBackend struct {
	colmetrics.UnimplementedMetricsServiceServer
	server   *grpc.Server
	received []*metricspb.ResourceMetrics
	mu       sync.Mutex
}

func startMockBackend(t *testing.T) (*mockGRPCBackend, string) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}

	backend := &mockGRPCBackend{
		server:   grpc.NewServer(),
		received: make([]*metricspb.ResourceMetrics, 0),
	}
	colmetrics.RegisterMetricsServiceServer(backend.server, backend)

	go backend.server.Serve(l)

	return backend, l.Addr().String()
}

func (b *mockGRPCBackend) Export(ctx context.Context, req *colmetrics.ExportMetricsServiceRequest) (*colmetrics.ExportMetricsServiceResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.received = append(b.received, req.ResourceMetrics...)
	return &colmetrics.ExportMetricsServiceResponse{}, nil
}

func (b *mockGRPCBackend) GetReceived() []*metricspb.ResourceMetrics {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := make([]*metricspb.ResourceMetrics, len(b.received))
	copy(result, b.received)
	return result
}

func (b *mockGRPCBackend) Stop() {
	b.server.GracefulStop()
}

func createTestExportRequest(serviceName, metricName string, datapoints int) *colmetrics.ExportMetricsServiceRequest {
	dps := make([]*metricspb.NumberDataPoint, datapoints)
	for i := 0; i < datapoints; i++ {
		dps[i] = &metricspb.NumberDataPoint{
			TimeUnixNano: uint64(time.Now().UnixNano()),
			Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)},
		}
	}

	return &colmetrics.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: serviceName}}},
					},
				},
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{
								Name: metricName,
								Data: &metricspb.Metric_Gauge{
									Gauge: &metricspb.Gauge{
										DataPoints: dps,
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func createTestExportRequestWithMultipleMetrics(serviceName string, numMetrics, datapointsPerMetric int) *colmetrics.ExportMetricsServiceRequest {
	metrics := make([]*metricspb.Metric, numMetrics)
	for i := 0; i < numMetrics; i++ {
		dps := make([]*metricspb.NumberDataPoint, datapointsPerMetric)
		for j := 0; j < datapointsPerMetric; j++ {
			dps[j] = &metricspb.NumberDataPoint{
				TimeUnixNano: uint64(time.Now().UnixNano()),
				Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(j)},
			}
		}
		metrics[i] = &metricspb.Metric{
			Name: serviceName + "-metric-" + string(rune('a'+i%26)),
			Data: &metricspb.Metric_Gauge{
				Gauge: &metricspb.Gauge{
					DataPoints: dps,
				},
			},
		}
	}

	return &colmetrics.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: serviceName}}},
					},
				},
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: metrics,
					},
				},
			},
		},
	}
}
