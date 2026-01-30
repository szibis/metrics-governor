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
	buf := buffer.New(1000, 10, 100*time.Millisecond, exp, statsCollector, limitsEnforcer, nil)
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
	buf := buffer.New(1000, 10, 100*time.Millisecond, exp, statsCollector, limitsEnforcer, nil)
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
	buf := buffer.New(1000, 1000, 1*time.Hour, exp, statsCollector, limitsEnforcer, nil)
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
	buf := buffer.New(10000, 100, 100*time.Millisecond, exp, statsCollector, limitsEnforcer, nil)
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

// TestE2E_HighCardinality tests handling of high cardinality metrics
func TestE2E_HighCardinality(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Start mock backend
	backend, backendAddr := startMockGRPCBackend(t)
	defer backend.Stop()

	// Create exporter
	exp, err := exporter.New(ctx, exporter.Config{
		Endpoint: backendAddr,
		Insecure: true,
		Protocol: "grpc",
		Timeout:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	// Create components with label tracking
	statsCollector := stats.NewCollector([]string{"service", "user_id", "request_id"})
	limitsEnforcer := limits.NewEnforcer(&limits.Config{}, true)

	// Create buffer
	buf := buffer.New(100000, 1000, 100*time.Millisecond, exp, statsCollector, limitsEnforcer, nil)
	go buf.Start(ctx)

	// Create gRPC receiver
	grpcAddr := getFreeAddr(t)
	grpcReceiver := receiver.NewGRPC(grpcAddr, buf)
	go grpcReceiver.Start()
	defer grpcReceiver.Stop()

	time.Sleep(100 * time.Millisecond)

	// Connect client
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := colmetrics.NewMetricsServiceClient(conn)

	// Send high cardinality metrics (many unique label combinations)
	numUniqueUsers := 1000
	for i := 0; i < numUniqueUsers; i++ {
		req := createHighCardinalityRequest(
			"high-cardinality-service",
			fmt.Sprintf("user_%d", i),
			fmt.Sprintf("req_%d", i),
			5, // datapoints per user
		)
		_, err := client.Export(ctx, req)
		if err != nil {
			t.Fatalf("Export %d failed: %v", i, err)
		}
	}

	// Wait for buffer to flush
	time.Sleep(2 * time.Second)

	// Verify backend received metrics
	received := backend.GetReceivedMetrics()
	if len(received) == 0 {
		t.Fatal("Backend did not receive any metrics")
	}

	// Count unique label combinations
	uniqueCombinations := make(map[string]bool)
	totalDatapoints := 0
	for _, rm := range received {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if g := m.GetGauge(); g != nil {
					for _, dp := range g.DataPoints {
						totalDatapoints++
						key := ""
						for _, attr := range dp.Attributes {
							key += fmt.Sprintf("%s=%v,", attr.Key, attr.Value)
						}
						uniqueCombinations[key] = true
					}
				}
			}
		}
	}

	t.Logf("High cardinality test: received %d resource metrics, %d total datapoints, %d unique label combinations",
		len(received), totalDatapoints, len(uniqueCombinations))

	// Verify we got a reasonable number of unique combinations
	expectedMin := numUniqueUsers / 2 // Allow some batching/dedup
	if len(uniqueCombinations) < expectedMin {
		t.Errorf("Expected at least %d unique combinations, got %d", expectedMin, len(uniqueCombinations))
	}
}

// TestE2E_ManyDatapoints tests handling of metrics with many datapoints
func TestE2E_ManyDatapoints(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Start mock backend
	backend, backendAddr := startMockGRPCBackend(t)
	defer backend.Stop()

	// Create exporter
	exp, err := exporter.New(ctx, exporter.Config{
		Endpoint: backendAddr,
		Insecure: true,
		Protocol: "grpc",
		Timeout:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	// Create components
	statsCollector := stats.NewCollector([]string{"service"})
	limitsEnforcer := limits.NewEnforcer(&limits.Config{}, true)

	// Create buffer
	buf := buffer.New(100000, 1000, 100*time.Millisecond, exp, statsCollector, limitsEnforcer, nil)
	go buf.Start(ctx)

	// Create gRPC receiver
	grpcAddr := getFreeAddr(t)
	grpcReceiver := receiver.NewGRPC(grpcAddr, buf)
	go grpcReceiver.Start()
	defer grpcReceiver.Stop()

	time.Sleep(100 * time.Millisecond)

	// Connect client
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := colmetrics.NewMetricsServiceClient(conn)

	// Send metrics with many datapoints
	numRequests := 10
	datapointsPerMetric := 10000

	totalSent := 0
	for i := 0; i < numRequests; i++ {
		req := createTestExportRequest("many-datapoints-service", "many_datapoints_metric", datapointsPerMetric)
		_, err := client.Export(ctx, req)
		if err != nil {
			t.Fatalf("Export %d failed: %v", i, err)
		}
		totalSent += datapointsPerMetric
	}

	t.Logf("Sent %d datapoints across %d requests", totalSent, numRequests)

	// Wait for buffer to flush
	time.Sleep(3 * time.Second)

	// Verify backend received metrics
	received := backend.GetReceivedMetrics()
	if len(received) == 0 {
		t.Fatal("Backend did not receive any metrics")
	}

	// Count total datapoints received
	totalReceived := 0
	for _, rm := range received {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if g := m.GetGauge(); g != nil {
					totalReceived += len(g.DataPoints)
				}
			}
		}
	}

	t.Logf("Many datapoints test: received %d resource metrics, %d total datapoints", len(received), totalReceived)

	// Verify we received a reasonable number of datapoints
	expectedMin := totalSent / 2 // Allow some loss during test
	if totalReceived < expectedMin {
		t.Errorf("Expected at least %d datapoints, got %d", expectedMin, totalReceived)
	}
}

// TestE2E_BurstTraffic tests handling of burst traffic patterns
func TestE2E_BurstTraffic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Start mock backend
	backend, backendAddr := startMockGRPCBackend(t)
	defer backend.Stop()

	// Create exporter
	exp, err := exporter.New(ctx, exporter.Config{
		Endpoint: backendAddr,
		Insecure: true,
		Protocol: "grpc",
		Timeout:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	// Create buffer with smaller batch size for faster processing
	buf := buffer.New(100000, 500, 50*time.Millisecond, exp, nil, nil, nil)
	go buf.Start(ctx)

	// Create gRPC receiver
	grpcAddr := getFreeAddr(t)
	grpcReceiver := receiver.NewGRPC(grpcAddr, buf)
	go grpcReceiver.Start()
	defer grpcReceiver.Stop()

	time.Sleep(100 * time.Millisecond)

	// Connect client
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := colmetrics.NewMetricsServiceClient(conn)

	// Burst pattern: send many requests rapidly, then pause
	bursts := 5
	requestsPerBurst := 500
	var wg sync.WaitGroup

	start := time.Now()

	for burst := 0; burst < bursts; burst++ {
		t.Logf("Starting burst %d", burst+1)
		burstStart := time.Now()

		// Send burst concurrently
		for i := 0; i < requestsPerBurst; i++ {
			wg.Add(1)
			go func(burstID, reqID int) {
				defer wg.Done()
				req := createTestExportRequest(
					fmt.Sprintf("burst-service-%d", burstID),
					fmt.Sprintf("burst-metric-%d", reqID),
					10,
				)
				_, err := client.Export(ctx, req)
				if err != nil {
					// Log but don't fail - some errors expected under burst load
					t.Logf("Burst %d req %d error: %v", burstID, reqID, err)
				}
			}(burst, i)
		}

		wg.Wait()
		t.Logf("Burst %d completed in %v", burst+1, time.Since(burstStart))

		// Pause between bursts
		time.Sleep(200 * time.Millisecond)
	}

	totalDuration := time.Since(start)
	totalSent := bursts * requestsPerBurst

	// Wait for final flush
	time.Sleep(2 * time.Second)

	// Verify backend received metrics
	received := backend.GetReceivedMetrics()
	successRate := float64(len(received)) / float64(totalSent) * 100

	t.Logf("Burst traffic test completed:")
	t.Logf("  Total bursts: %d", bursts)
	t.Logf("  Requests per burst: %d", requestsPerBurst)
	t.Logf("  Total sent: %d", totalSent)
	t.Logf("  Total received: %d", len(received))
	t.Logf("  Success rate: %.1f%%", successRate)
	t.Logf("  Total duration: %v", totalDuration)
	t.Logf("  Throughput: %.0f req/s", float64(totalSent)/totalDuration.Seconds())

	// Verify reasonable success rate
	if successRate < 50 {
		t.Errorf("Success rate too low: %.1f%% (expected >= 50%%)", successRate)
	}
}

// TestE2E_EdgeCaseValues tests handling of edge case metric values
func TestE2E_EdgeCaseValues(t *testing.T) {
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

	// Create buffer
	buf := buffer.New(1000, 100, 100*time.Millisecond, exp, nil, nil, nil)
	go buf.Start(ctx)

	// Create gRPC receiver
	grpcAddr := getFreeAddr(t)
	grpcReceiver := receiver.NewGRPC(grpcAddr, buf)
	go grpcReceiver.Start()
	defer grpcReceiver.Stop()

	time.Sleep(100 * time.Millisecond)

	// Connect client
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := colmetrics.NewMetricsServiceClient(conn)

	// Send edge case values
	req := createEdgeCaseRequest()
	_, err = client.Export(ctx, req)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Wait for buffer to flush
	time.Sleep(500 * time.Millisecond)

	// Verify backend received metrics
	received := backend.GetReceivedMetrics()
	if len(received) == 0 {
		t.Fatal("Backend did not receive edge case metrics")
	}

	t.Log("Edge case values test passed")
}

// Helper functions for new tests

func createHighCardinalityRequest(serviceName, userID, requestID string, datapoints int) *colmetrics.ExportMetricsServiceRequest {
	dps := make([]*metricspb.NumberDataPoint, datapoints)
	for i := 0; i < datapoints; i++ {
		dps[i] = &metricspb.NumberDataPoint{
			TimeUnixNano: uint64(time.Now().UnixNano()),
			Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)},
			Attributes: []*commonpb.KeyValue{
				{Key: "user_id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: userID}}},
				{Key: "request_id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: requestID}}},
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
						Metrics: []*metricspb.Metric{
							{
								Name: "high_cardinality_metric",
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

func createEdgeCaseRequest() *colmetrics.ExportMetricsServiceRequest {
	edgeValues := []float64{
		0.0,                        // Zero
		-0.0,                       // Negative zero
		1.0,                        // Normal positive
		-1.0,                       // Normal negative
		1.7976931348623157e+308,    // Very large positive (close to MaxFloat64)
		-1.7976931348623157e+308,   // Very large negative
		1e-300,                     // Very small positive
		-1e-300,                    // Very small negative
		3.141592653589793,          // Pi
		2.718281828459045,          // e
	}

	dps := make([]*metricspb.NumberDataPoint, len(edgeValues))
	for i, val := range edgeValues {
		dps[i] = &metricspb.NumberDataPoint{
			TimeUnixNano: uint64(time.Now().UnixNano()),
			Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: val},
			Attributes: []*commonpb.KeyValue{
				{Key: "edge_type", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprintf("type_%d", i)}}},
			},
		}
	}

	return &colmetrics.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "edge-case-service"}}},
					},
				},
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{
								Name: "edge_case_gauge",
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
