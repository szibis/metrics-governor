package exporter

import (
	"context"
	"net"
	"testing"
	"time"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/grpc"
)

// mockMetricsServer is a mock OTLP metrics server for testing.
type mockMetricsServer struct {
	colmetricspb.UnimplementedMetricsServiceServer
	received []*colmetricspb.ExportMetricsServiceRequest
}

func (m *mockMetricsServer) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) (*colmetricspb.ExportMetricsServiceResponse, error) {
	m.received = append(m.received, req)
	return &colmetricspb.ExportMetricsServiceResponse{}, nil
}

func TestNewExporter(t *testing.T) {
	// Start a mock server
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	server := grpc.NewServer()
	colmetricspb.RegisterMetricsServiceServer(server, &mockMetricsServer{})
	go server.Serve(lis)
	defer server.Stop()

	cfg := Config{
		Endpoint: lis.Addr().String(),
		Insecure: true,
		Timeout:  5 * time.Second,
	}

	exp, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("failed to create exporter: %v", err)
	}
	defer exp.Close()

	if exp.timeout != 5*time.Second {
		t.Errorf("expected timeout 5s, got %v", exp.timeout)
	}
}

func TestExporterExport(t *testing.T) {
	mockServer := &mockMetricsServer{}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	server := grpc.NewServer()
	colmetricspb.RegisterMetricsServiceServer(server, mockServer)
	go server.Serve(lis)
	defer server.Stop()

	cfg := Config{
		Endpoint: lis.Addr().String(),
		Insecure: true,
		Timeout:  5 * time.Second,
	}

	exp, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("failed to create exporter: %v", err)
	}
	defer exp.Close()

	// Create a test request
	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{
								Name: "test.metric",
							},
						},
					},
				},
			},
		},
	}

	err = exp.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}

	// Verify the server received the request
	if len(mockServer.received) != 1 {
		t.Errorf("expected 1 request, got %d", len(mockServer.received))
	}
}

func TestExporterClose(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	server := grpc.NewServer()
	colmetricspb.RegisterMetricsServiceServer(server, &mockMetricsServer{})
	go server.Serve(lis)
	defer server.Stop()

	cfg := Config{
		Endpoint: lis.Addr().String(),
		Insecure: true,
		Timeout:  5 * time.Second,
	}

	exp, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("failed to create exporter: %v", err)
	}

	err = exp.Close()
	if err != nil {
		t.Errorf("close failed: %v", err)
	}
}

func TestConfigDefaults(t *testing.T) {
	cfg := Config{}

	if cfg.Endpoint != "" {
		t.Errorf("expected empty endpoint, got %s", cfg.Endpoint)
	}
	if cfg.Insecure != false {
		t.Error("expected Insecure to be false by default")
	}
	if cfg.Timeout != 0 {
		t.Errorf("expected zero timeout, got %v", cfg.Timeout)
	}
}

func TestExporterWithSecureConnection(t *testing.T) {
	// grpc.NewClient without credentials fails immediately in newer gRPC versions
	// This test verifies that behavior - secure connections require explicit credentials
	cfg := Config{
		Endpoint: "localhost:4317",
		Insecure: false,
		Timeout:  5 * time.Second,
	}

	_, err := New(context.Background(), cfg)
	// Without transport credentials, grpc.NewClient returns an error
	if err == nil {
		t.Error("expected error when creating exporter without transport credentials")
	}
}
