package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"

	"github.com/slawomirskowron/metrics-governor/internal/buffer"
	"github.com/slawomirskowron/metrics-governor/internal/exporter"
	"github.com/slawomirskowron/metrics-governor/internal/limits"
	"github.com/slawomirskowron/metrics-governor/internal/receiver"
	"github.com/slawomirskowron/metrics-governor/internal/stats"
)

// TestE2E_FullPipeline_GRPC tests the complete flow: gRPC client -> receiver -> buffer -> exporter -> backend
func TestE2E_FullPipeline_GRPC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start mock backend (gRPC server that receives exported metrics)
	backend, backendAddr := startMockGRPCBackend(t)
	defer backend.Stop()

	// Create exporter pointing to mock backend
	exp, err := exporter.New(ctx, exporter.Config{
		Endpoint: backendAddr,
		Insecure: true,
		Protocol: "grpc",
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	// Create components
	statsCollector := stats.NewCollector(nil)
	limitsEnforcer := limits.NewEnforcer(&limits.Config{}, true) // dry run mode

	// Create buffer
	buf := buffer.New(1000, 10, 100*time.Millisecond, exp, statsCollector, limitsEnforcer)
	go buf.Start(ctx)
	// Buffer stops when context is canceled

	// Create gRPC receiver
	grpcAddr := getFreeAddr(t)
	grpcReceiver := receiver.NewGRPC(grpcAddr, buf)
	go grpcReceiver.Start()
	defer grpcReceiver.Stop()

	// Wait for receiver to start
	time.Sleep(100 * time.Millisecond)

	// Send metrics via gRPC client
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to connect to receiver: %v", err)
	}
	defer conn.Close()

	client := colmetrics.NewMetricsServiceClient(conn)
	req := createTestExportRequest("test-service", "test-metric", 5)

	resp, err := client.Export(ctx, req)
	if err != nil {
		t.Fatalf("Failed to export metrics: %v", err)
	}
	if resp == nil {
		t.Fatal("Expected non-nil response")
	}

	// Wait for buffer to flush
	time.Sleep(500 * time.Millisecond)

	// Verify backend received the metrics
	received := backend.GetReceivedMetrics()
	if len(received) == 0 {
		t.Fatal("Backend did not receive any metrics")
	}

	// Verify metric content
	found := false
	for _, rm := range received {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if m.Name == "test-metric" {
					found = true
					break
				}
			}
		}
	}
	if !found {
		t.Error("Expected metric 'test-metric' not found in backend")
	}

	t.Log("E2E gRPC pipeline test passed")
}

// TestE2E_FullPipeline_HTTP tests the complete flow: HTTP client -> receiver -> buffer -> exporter -> backend
func TestE2E_FullPipeline_HTTP(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start mock backend
	backend, backendAddr := startMockGRPCBackend(t)
	defer backend.Stop()

	// Create exporter
	exp, err := exporter.New(ctx, exporter.Config{
		Endpoint: backendAddr,
		Insecure: true,
		Protocol: "grpc",
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	// Create components
	statsCollector := stats.NewCollector(nil)
	limitsEnforcer := limits.NewEnforcer(&limits.Config{}, true)

	// Create buffer
	buf := buffer.New(1000, 10, 100*time.Millisecond, exp, statsCollector, limitsEnforcer)
	go buf.Start(ctx)
	// Buffer stops when context is canceled

	// Create HTTP receiver
	httpAddr := getFreeAddr(t)
	httpReceiver := receiver.NewHTTP(httpAddr, buf)
	go httpReceiver.Start()
	defer httpReceiver.Stop(ctx)

	// Wait for receiver to start
	time.Sleep(100 * time.Millisecond)

	// Send metrics via HTTP client
	req := createTestExportRequest("http-service", "http-metric", 3)
	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("http://%s/v1/metrics", httpAddr), bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Failed to create HTTP request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")

	httpResp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("Failed to send HTTP request: %v", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(httpResp.Body)
		t.Fatalf("Expected status 200, got %d: %s", httpResp.StatusCode, string(body))
	}

	// Wait for buffer to flush
	time.Sleep(500 * time.Millisecond)

	// Verify backend received the metrics
	received := backend.GetReceivedMetrics()
	if len(received) == 0 {
		t.Fatal("Backend did not receive any metrics")
	}

	t.Log("E2E HTTP pipeline test passed")
}

// TestE2E_BufferFlushOnClose tests that buffer flushes remaining metrics when context is canceled
func TestE2E_BufferFlushOnClose(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start mock backend
	backend, backendAddr := startMockGRPCBackend(t)
	defer backend.Stop()

	// Create exporter
	exp, err := exporter.New(ctx, exporter.Config{
		Endpoint: backendAddr,
		Insecure: true,
		Protocol: "grpc",
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	// Create components
	statsCollector := stats.NewCollector(nil)
	limitsEnforcer := limits.NewEnforcer(&limits.Config{}, true)

	// Create buffer with a separate context so we can cancel it
	bufCtx, bufCancel := context.WithCancel(ctx)

	// Create buffer with long flush interval (won't auto-flush)
	buf := buffer.New(1000, 1000, 1*time.Hour, exp, statsCollector, limitsEnforcer)
	go buf.Start(bufCtx)

	// Create gRPC receiver
	grpcAddr := getFreeAddr(t)
	grpcReceiver := receiver.NewGRPC(grpcAddr, buf)
	go grpcReceiver.Start()
	defer grpcReceiver.Stop()

	time.Sleep(100 * time.Millisecond)

	// Send metrics
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := colmetrics.NewMetricsServiceClient(conn)
	req := createTestExportRequest("flush-test", "flush-metric", 5)
	_, err = client.Export(ctx, req)
	if err != nil {
		t.Fatalf("Failed to export: %v", err)
	}

	// Backend should NOT have received metrics yet (buffer not flushed)
	time.Sleep(100 * time.Millisecond)
	if len(backend.GetReceivedMetrics()) > 0 {
		t.Fatal("Backend received metrics before buffer context cancel")
	}

	// Cancel buffer context - should trigger final flush
	bufCancel()

	// Wait for flush
	time.Sleep(500 * time.Millisecond)

	// Now backend should have the metrics
	received := backend.GetReceivedMetrics()
	if len(received) == 0 {
		t.Fatal("Backend did not receive metrics after buffer context cancel")
	}

	t.Log("E2E buffer flush on close test passed")
}

// TestE2E_ConcurrentClients tests handling of multiple concurrent clients
func TestE2E_ConcurrentClients(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start mock backend
	backend, backendAddr := startMockGRPCBackend(t)
	defer backend.Stop()

	// Create exporter
	exp, err := exporter.New(ctx, exporter.Config{
		Endpoint: backendAddr,
		Insecure: true,
		Protocol: "grpc",
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	// Create components
	statsCollector := stats.NewCollector(nil)
	limitsEnforcer := limits.NewEnforcer(&limits.Config{}, true)

	// Create buffer
	buf := buffer.New(10000, 100, 100*time.Millisecond, exp, statsCollector, limitsEnforcer)
	go buf.Start(ctx)
	// Buffer stops when context is canceled

	// Create gRPC receiver
	grpcAddr := getFreeAddr(t)
	grpcReceiver := receiver.NewGRPC(grpcAddr, buf)
	go grpcReceiver.Start()
	defer grpcReceiver.Stop()

	time.Sleep(100 * time.Millisecond)

	// Launch concurrent clients
	numClients := 10
	metricsPerClient := 50
	var wg sync.WaitGroup

	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()

			conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				t.Errorf("Client %d failed to connect: %v", clientID, err)
				return
			}
			defer conn.Close()

			client := colmetrics.NewMetricsServiceClient(conn)

			for j := 0; j < metricsPerClient; j++ {
				req := createTestExportRequest(
					fmt.Sprintf("service-%d", clientID),
					fmt.Sprintf("metric-%d-%d", clientID, j),
					1,
				)
				_, err := client.Export(ctx, req)
				if err != nil {
					t.Errorf("Client %d export %d failed: %v", clientID, j, err)
				}
			}
		}(i)
	}

	wg.Wait()

	// Wait for all metrics to be processed
	time.Sleep(1 * time.Second)

	// Verify backend received metrics from all clients
	received := backend.GetReceivedMetrics()
	if len(received) == 0 {
		t.Fatal("Backend did not receive any metrics")
	}

	t.Logf("E2E concurrent clients test passed - backend received %d resource metrics batches", len(received))
}

// Helper functions

func getFreeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to get free address: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
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

// MockGRPCBackend is a mock OTLP gRPC backend for testing
type MockGRPCBackend struct {
	colmetrics.UnimplementedMetricsServiceServer
	server   *grpc.Server
	received []*metricspb.ResourceMetrics
	mu       sync.Mutex
}

func startMockGRPCBackend(t *testing.T) (*MockGRPCBackend, string) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}

	backend := &MockGRPCBackend{
		server:   grpc.NewServer(),
		received: make([]*metricspb.ResourceMetrics, 0),
	}
	colmetrics.RegisterMetricsServiceServer(backend.server, backend)

	go backend.server.Serve(l)

	return backend, l.Addr().String()
}

func (b *MockGRPCBackend) Export(ctx context.Context, req *colmetrics.ExportMetricsServiceRequest) (*colmetrics.ExportMetricsServiceResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.received = append(b.received, req.ResourceMetrics...)
	return &colmetrics.ExportMetricsServiceResponse{}, nil
}

func (b *MockGRPCBackend) GetReceivedMetrics() []*metricspb.ResourceMetrics {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := make([]*metricspb.ResourceMetrics, len(b.received))
	copy(result, b.received)
	return result
}

func (b *MockGRPCBackend) Stop() {
	b.server.GracefulStop()
}
