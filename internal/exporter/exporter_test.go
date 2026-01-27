package exporter

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
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
	// When Insecure is false and no TLS config is provided,
	// the exporter now uses a default TLS config with system CA pool.
	// This should succeed creating the exporter (connection fails at export time).
	cfg := Config{
		Endpoint: "localhost:4317",
		Insecure: false,
		Timeout:  5 * time.Second,
	}

	exp, err := New(context.Background(), cfg)
	if err != nil {
		t.Errorf("expected exporter creation to succeed with default TLS, got: %v", err)
	}
	if exp != nil {
		exp.Close()
	}
}

func TestNewHTTPExporter(t *testing.T) {
	cfg := Config{
		Endpoint: "localhost:4318",
		Protocol: ProtocolHTTP,
		Insecure: true,
		Timeout:  5 * time.Second,
	}

	exp, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("failed to create HTTP exporter: %v", err)
	}
	defer exp.Close()

	if exp.protocol != ProtocolHTTP {
		t.Errorf("expected protocol HTTP, got %v", exp.protocol)
	}
	if exp.httpEndpoint != "http://localhost:4318/v1/metrics" {
		t.Errorf("expected endpoint 'http://localhost:4318/v1/metrics', got '%s'", exp.httpEndpoint)
	}
}

func TestHTTPExporterExport(t *testing.T) {
	var received []byte
	// Create mock HTTP server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/metrics" {
			t.Errorf("expected /v1/metrics, got %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/x-protobuf" {
			t.Errorf("expected Content-Type 'application/x-protobuf', got '%s'", r.Header.Get("Content-Type"))
		}

		var err error
		received, err = readAll(r.Body)
		if err != nil {
			t.Errorf("failed to read body: %v", err)
		}

		// Return success with empty response
		resp := &colmetricspb.ExportMetricsServiceResponse{}
		respBytes, _ := proto.Marshal(resp)
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.Write(respBytes)
	}))
	defer server.Close()

	cfg := Config{
		Endpoint: server.URL,
		Protocol: ProtocolHTTP,
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

	// Verify the request was received
	if len(received) == 0 {
		t.Error("expected to receive request body")
	}
}

func TestHTTPExporterEndpointBuilding(t *testing.T) {
	tests := []struct {
		name       string
		endpoint   string
		insecure   bool
		wantScheme string
		wantPath   bool
	}{
		{
			name:       "bare host:port insecure",
			endpoint:   "localhost:4318",
			insecure:   true,
			wantScheme: "http://",
			wantPath:   true,
		},
		{
			name:       "bare host:port secure",
			endpoint:   "localhost:4318",
			insecure:   false,
			wantScheme: "https://",
			wantPath:   true,
		},
		{
			name:       "with http scheme",
			endpoint:   "http://localhost:4318",
			insecure:   true,
			wantScheme: "http://",
			wantPath:   true,
		},
		{
			name:       "with https scheme",
			endpoint:   "https://localhost:4318",
			insecure:   false,
			wantScheme: "https://",
			wantPath:   true,
		},
		{
			name:       "with full path",
			endpoint:   "http://localhost:4318/v1/metrics",
			insecure:   true,
			wantScheme: "http://",
			wantPath:   false, // Already has path
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				Endpoint: tt.endpoint,
				Protocol: ProtocolHTTP,
				Insecure: tt.insecure,
				Timeout:  5 * time.Second,
			}

			exp, err := New(context.Background(), cfg)
			if err != nil {
				t.Fatalf("failed to create exporter: %v", err)
			}
			defer exp.Close()

			if !hasScheme(exp.httpEndpoint) {
				t.Error("expected endpoint to have scheme")
			}
			if tt.wantPath && !hasPath(exp.httpEndpoint) {
				t.Error("expected endpoint to have path added")
			}
		})
	}
}

func TestProtocolDefault(t *testing.T) {
	// When no protocol is specified, should default to gRPC
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
		// Protocol not set
	}

	exp, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("failed to create exporter: %v", err)
	}
	defer exp.Close()

	if exp.protocol != ProtocolGRPC {
		t.Errorf("expected default protocol to be gRPC, got %v", exp.protocol)
	}
}

func TestUnsupportedProtocol(t *testing.T) {
	cfg := Config{
		Endpoint: "localhost:4317",
		Protocol: Protocol("invalid"),
		Insecure: true,
		Timeout:  5 * time.Second,
	}

	_, err := New(context.Background(), cfg)
	if err == nil {
		t.Error("expected error for unsupported protocol")
	}
}

// readAll reads all data from r and returns it.
func readAll(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	var result []byte
	buf := make([]byte, 1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			result = append(result, buf[:n]...)
		}
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return result, err
		}
	}
	return result, nil
}
