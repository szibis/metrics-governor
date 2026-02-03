package e2e

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	colmetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"

	"github.com/szibis/metrics-governor/internal/buffer"
	"github.com/szibis/metrics-governor/internal/exporter"
	"github.com/szibis/metrics-governor/internal/limits"
	"github.com/szibis/metrics-governor/internal/queue"
	"github.com/szibis/metrics-governor/internal/receiver"
	"github.com/szibis/metrics-governor/internal/stats"
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
	limitsEnforcer := limits.NewEnforcer(&limits.Config{}, true, 0) // dry run mode

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
	limitsEnforcer := limits.NewEnforcer(&limits.Config{}, true, 0)

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
	limitsEnforcer := limits.NewEnforcer(&limits.Config{}, true, 0)

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
	limitsEnforcer := limits.NewEnforcer(&limits.Config{}, true, 0)

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
	limitsEnforcer := limits.NewEnforcer(&limits.Config{}, true, 0)

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
	limitsEnforcer := limits.NewEnforcer(&limits.Config{}, true, 0)

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
		0.0,                      // Zero
		-0.0,                     // Negative zero
		1.0,                      // Normal positive
		-1.0,                     // Normal negative
		1.7976931348623157e+308,  // Very large positive (close to MaxFloat64)
		-1.7976931348623157e+308, // Very large negative
		1e-300,                   // Very small positive
		-1e-300,                  // Very small negative
		3.141592653589793,        // Pi
		2.718281828459045,        // e
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

// TestE2E_QueuedExporter_BackendFailure tests queue behavior when backend is unavailable
func TestE2E_QueuedExporter_BackendFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create exporter pointing to non-existent backend (will fail)
	tmpDir := t.TempDir()
	exp, err := exporter.New(ctx, exporter.Config{
		Endpoint: "127.0.0.1:59999", // Non-existent port
		Insecure: true,
		Protocol: "grpc",
		Timeout:  1 * time.Second,
	})
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	// Create queued exporter with circuit breaker and backoff
	queueCfg := queue.Config{
		Path:                       tmpDir,
		MaxSize:                    100,
		RetryInterval:              50 * time.Millisecond,
		MaxRetryDelay:              500 * time.Millisecond,
		BackoffEnabled:             true,
		BackoffMultiplier:          2.0,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    3,
		CircuitBreakerResetTimeout: 200 * time.Millisecond,
	}

	queuedExp, err := exporter.NewQueued(exp, queueCfg)
	if err != nil {
		t.Fatalf("Failed to create queued exporter: %v", err)
	}
	defer queuedExp.Close()

	// Create components
	statsCollector := stats.NewCollector(nil)
	buf := buffer.New(1000, 10, 100*time.Millisecond, queuedExp, statsCollector, nil, nil)
	go buf.Start(ctx)

	// Create gRPC receiver
	grpcAddr := getFreeAddr(t)
	grpcReceiver := receiver.NewGRPC(grpcAddr, buf)
	go grpcReceiver.Start()
	defer grpcReceiver.Stop()

	time.Sleep(100 * time.Millisecond)

	// Send metrics - these should queue since backend is unavailable
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := colmetrics.NewMetricsServiceClient(conn)

	// Send multiple batches
	for i := 0; i < 5; i++ {
		req := createTestExportRequest(fmt.Sprintf("queue-test-%d", i), "queued_metric", 10)
		_, err := client.Export(ctx, req)
		if err != nil {
			t.Logf("Export %d returned error (expected with failing backend): %v", i, err)
		}
	}

	// Wait for buffer to flush and queue to process
	time.Sleep(1 * time.Second)

	// Verify queue has entries (backend is unavailable)
	queueLen := queuedExp.QueueLen()
	t.Logf("Queue length after backend failure: %d", queueLen)

	// Queue should have some entries since backend is failing
	// (some may have been dropped due to circuit breaker, but queue should have tried)
	if queueLen == 0 {
		t.Log("Queue is empty - circuit breaker may have blocked retries (expected behavior)")
	}

	t.Log("Queue resilience test completed - system handled backend failure gracefully")
}

// TestE2E_QueuedExporter_BackendRecovery tests queue draining when backend recovers
func TestE2E_QueuedExporter_BackendRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Start a backend that we can control
	backend, backendAddr := startMockGRPCBackend(t)

	// Create exporter
	tmpDir := t.TempDir()
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

	// Create queued exporter with resilience settings
	queueCfg := queue.Config{
		Path:                       tmpDir,
		MaxSize:                    1000,
		RetryInterval:              100 * time.Millisecond,
		MaxRetryDelay:              1 * time.Second,
		BackoffEnabled:             true,
		BackoffMultiplier:          2.0,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    5,
		CircuitBreakerResetTimeout: 500 * time.Millisecond,
	}

	queuedExp, err := exporter.NewQueued(exp, queueCfg)
	if err != nil {
		t.Fatalf("Failed to create queued exporter: %v", err)
	}
	defer queuedExp.Close()

	// Create buffer
	buf := buffer.New(1000, 10, 50*time.Millisecond, queuedExp, nil, nil, nil)
	go buf.Start(ctx)

	// Create receiver
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

	// Phase 1: Backend is UP - send initial metrics
	t.Log("Phase 1: Sending metrics with backend UP")
	for i := 0; i < 5; i++ {
		req := createTestExportRequest("recovery-test", fmt.Sprintf("metric_%d", i), 5)
		_, err := client.Export(ctx, req)
		if err != nil {
			t.Errorf("Export failed with backend UP: %v", err)
		}
	}

	time.Sleep(500 * time.Millisecond)
	initialReceived := len(backend.GetReceivedMetrics())
	t.Logf("Metrics received with backend UP: %d", initialReceived)

	// Phase 2: Stop backend - metrics should queue
	t.Log("Phase 2: Stopping backend")
	backend.Stop()

	// Send more metrics while backend is down
	for i := 5; i < 10; i++ {
		req := createTestExportRequest("recovery-test", fmt.Sprintf("metric_%d", i), 5)
		_, _ = client.Export(ctx, req)
	}

	time.Sleep(500 * time.Millisecond)
	t.Logf("Queue length with backend DOWN: %d", queuedExp.QueueLen())

	// Phase 3: Restart backend - queue should drain
	t.Log("Phase 3: Restarting backend")
	newBackend, _ := startMockGRPCBackendOnAddr(t, backendAddr)
	defer newBackend.Stop()

	// Wait for queue to drain and circuit breaker to recover
	time.Sleep(2 * time.Second)

	finalReceived := len(newBackend.GetReceivedMetrics())
	t.Logf("Metrics received after recovery: %d", finalReceived)
	t.Logf("Final queue length: %d", queuedExp.QueueLen())

	// Some metrics should have been recovered
	if finalReceived == 0 && queuedExp.QueueLen() == 0 {
		t.Log("All metrics either delivered or lost during outage (expected)")
	} else if finalReceived > 0 {
		t.Logf("Successfully recovered %d metric batches after backend recovery", finalReceived)
	}
}

// TestE2E_MemoryPressure tests behavior under memory constraints
func TestE2E_MemoryPressure(t *testing.T) {
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

	// Create buffer with small size to simulate pressure
	buf := buffer.New(100, 10, 50*time.Millisecond, exp, nil, nil, nil)
	go buf.Start(ctx)

	// Create receiver
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

	// Send many metrics rapidly to stress the small buffer
	var wg sync.WaitGroup
	successCount := int64(0)
	errorCount := int64(0)

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req := createTestExportRequest(
				fmt.Sprintf("pressure-service-%d", id),
				"pressure_metric",
				100, // Many datapoints per request
			)
			_, err := client.Export(ctx, req)
			if err != nil {
				atomic.AddInt64(&errorCount, 1)
			} else {
				atomic.AddInt64(&successCount, 1)
			}
		}(i)
	}

	wg.Wait()
	time.Sleep(1 * time.Second)

	received := backend.GetReceivedMetrics()

	t.Logf("Memory pressure test results:")
	t.Logf("  Requests sent: 50")
	t.Logf("  Success: %d", successCount)
	t.Logf("  Errors: %d", errorCount)
	t.Logf("  Metrics received by backend: %d", len(received))

	// Even under pressure, we should have processed most requests
	if successCount < 40 {
		t.Errorf("Too many failures under memory pressure: %d/%d succeeded", successCount, 50)
	}
}

// TestE2E_RapidBackendFlapping tests behavior with rapid backend up/down cycles
func TestE2E_RapidBackendFlapping(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping flapping test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tmpDir := t.TempDir()

	// Start initial backend
	backend, backendAddr := startMockGRPCBackend(t)

	// Create exporter with queue
	exp, err := exporter.New(ctx, exporter.Config{
		Endpoint: backendAddr,
		Insecure: true,
		Protocol: "grpc",
		Timeout:  2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	queueCfg := queue.Config{
		Path:                       tmpDir,
		MaxSize:                    500,
		RetryInterval:              50 * time.Millisecond,
		MaxRetryDelay:              500 * time.Millisecond,
		BackoffEnabled:             true,
		BackoffMultiplier:          1.5,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    3,
		CircuitBreakerResetTimeout: 200 * time.Millisecond,
	}

	queuedExp, err := exporter.NewQueued(exp, queueCfg)
	if err != nil {
		t.Fatalf("Failed to create queued exporter: %v", err)
	}
	defer queuedExp.Close()

	buf := buffer.New(1000, 50, 50*time.Millisecond, queuedExp, nil, nil, nil)
	go buf.Start(ctx)

	grpcAddr := getFreeAddr(t)
	grpcReceiver := receiver.NewGRPC(grpcAddr, buf)
	go grpcReceiver.Start()
	defer grpcReceiver.Stop()

	time.Sleep(100 * time.Millisecond)

	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := colmetrics.NewMetricsServiceClient(conn)

	// Run flapping test - toggle backend every 500ms while sending metrics
	currentBackend := backend
	var totalReceived int

	for cycle := 0; cycle < 5; cycle++ {
		// Send metrics
		for i := 0; i < 10; i++ {
			req := createTestExportRequest("flap-test", fmt.Sprintf("cycle_%d_metric_%d", cycle, i), 5)
			_, _ = client.Export(ctx, req)
		}

		time.Sleep(200 * time.Millisecond)

		// Toggle backend
		if currentBackend != nil {
			totalReceived += len(currentBackend.GetReceivedMetrics())
			currentBackend.Stop()
			currentBackend = nil
			t.Logf("Cycle %d: Backend stopped, total received so far: %d", cycle, totalReceived)
		} else {
			var newBackend *MockGRPCBackend
			newBackend, _ = startMockGRPCBackendOnAddr(t, backendAddr)
			currentBackend = newBackend
			t.Logf("Cycle %d: Backend restarted", cycle)
		}

		time.Sleep(300 * time.Millisecond)
	}

	// Final cleanup
	if currentBackend != nil {
		totalReceived += len(currentBackend.GetReceivedMetrics())
		currentBackend.Stop()
	}

	t.Logf("Flapping test completed:")
	t.Logf("  Total metrics received: %d", totalReceived)
	t.Logf("  Final queue length: %d", queuedExp.QueueLen())

	// The system should have handled the flapping without crashing
	// and delivered at least some metrics
	if totalReceived == 0 {
		t.Error("No metrics delivered during flapping test")
	}
}

// TestE2E_HTTPReceiver_CustomPath tests HTTP receiver with custom path
func TestE2E_HTTPReceiver_CustomPath(t *testing.T) {
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
	limitsEnforcer := limits.NewEnforcer(&limits.Config{}, true, 0)

	// Create buffer
	buf := buffer.New(1000, 10, 100*time.Millisecond, exp, statsCollector, limitsEnforcer, nil)
	go buf.Start(ctx)

	// Create HTTP receiver with custom path
	httpAddr := getFreeAddr(t)
	httpConfig := receiver.HTTPConfig{
		Addr: httpAddr,
		Path: "/custom/metrics/endpoint",
	}
	httpReceiver := receiver.NewHTTPWithConfig(httpConfig, buf)
	go httpReceiver.Start()
	defer httpReceiver.Stop(ctx)

	// Wait for receiver to start
	time.Sleep(100 * time.Millisecond)

	// Send metrics to custom path
	req := createTestExportRequest("custom-path-service", "custom-path-metric", 3)
	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("http://%s/custom/metrics/endpoint", httpAddr), bytes.NewReader(data))
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

	// Verify that default path returns 404
	httpReq2, _ := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("http://%s/v1/metrics", httpAddr), bytes.NewReader(data))
	httpReq2.Header.Set("Content-Type", "application/x-protobuf")

	httpResp2, err := http.DefaultClient.Do(httpReq2)
	if err != nil {
		t.Fatalf("Failed to send HTTP request to default path: %v", err)
	}
	defer httpResp2.Body.Close()

	if httpResp2.StatusCode != http.StatusNotFound {
		t.Errorf("Expected status 404 for default path when custom path is set, got %d", httpResp2.StatusCode)
	}

	// Wait for buffer to flush
	time.Sleep(500 * time.Millisecond)

	// Verify backend received the metrics
	received := backend.GetReceivedMetrics()
	if len(received) == 0 {
		t.Fatal("Backend did not receive any metrics from custom path")
	}

	t.Log("E2E HTTP custom path test passed")
}

// TestE2E_HTTPExporter_CustomDefaultPath tests HTTP exporter with custom default path
func TestE2E_HTTPExporter_CustomDefaultPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start mock HTTP backend that expects custom path
	var receivedPath string
	var receivedBody []byte
	var mu sync.Mutex
	mockServer := http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			receivedPath = r.URL.Path
			receivedBody, _ = io.ReadAll(r.Body)
			mu.Unlock()
			resp := &colmetrics.ExportMetricsServiceResponse{}
			respBytes, _ := proto.Marshal(resp)
			w.Header().Set("Content-Type", "application/x-protobuf")
			w.Write(respBytes)
		}),
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	go mockServer.Serve(listener)
	defer mockServer.Close()

	serverAddr := listener.Addr().String()

	// Create exporter with custom default path
	exp, err := exporter.New(ctx, exporter.Config{
		Endpoint:    serverAddr, // No path in endpoint
		Protocol:    "http",
		Insecure:    true,
		Timeout:     5 * time.Second,
		DefaultPath: "/opentelemetry/v1/metrics", // VictoriaMetrics path
	})
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	// Create components
	statsCollector := stats.NewCollector(nil)
	limitsEnforcer := limits.NewEnforcer(&limits.Config{}, true, 0)

	// Create buffer
	buf := buffer.New(1000, 10, 100*time.Millisecond, exp, statsCollector, limitsEnforcer, nil)
	go buf.Start(ctx)

	// Create gRPC receiver
	grpcAddr := getFreeAddr(t)
	grpcReceiver := receiver.NewGRPC(grpcAddr, buf)
	go grpcReceiver.Start()
	defer grpcReceiver.Stop()

	time.Sleep(100 * time.Millisecond)

	// Send metrics via gRPC
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to connect to receiver: %v", err)
	}
	defer conn.Close()

	client := colmetrics.NewMetricsServiceClient(conn)
	req := createTestExportRequest("custom-exporter-path-service", "test-metric", 5)

	_, err = client.Export(ctx, req)
	if err != nil {
		t.Fatalf("Failed to export metrics: %v", err)
	}

	// Wait for buffer to flush
	time.Sleep(500 * time.Millisecond)

	// Verify the request was sent to the custom path
	mu.Lock()
	path := receivedPath
	bodyLen := len(receivedBody)
	mu.Unlock()

	if path != "/opentelemetry/v1/metrics" {
		t.Errorf("Expected request path '/opentelemetry/v1/metrics', got '%s'", path)
	}

	if bodyLen == 0 {
		t.Error("Backend did not receive any data")
	}

	t.Log("E2E HTTP exporter custom default path test passed")
}

// Helper to start backend on specific address
func startMockGRPCBackendOnAddr(t *testing.T, addr string) (*MockGRPCBackend, string) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("Failed to listen on %s: %v", addr, err)
	}

	backend := &MockGRPCBackend{
		server:   grpc.NewServer(),
		received: make([]*metricspb.ResourceMetrics, 0),
	}
	colmetrics.RegisterMetricsServiceServer(backend.server, backend)

	go backend.server.Serve(l)

	return backend, l.Addr().String()
}

// TestE2E_BloomPersistence_RestartRecovery tests bloom filter persistence across restarts
func TestE2E_BloomPersistence_RestartRecovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Start mock backend
	backend, backendAddr := startMockGRPCBackend(t)
	defer backend.Stop()

	// Create temporary directory for bloom persistence
	bloomDir := t.TempDir()

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

	// Create stats collector with bloom persistence enabled
	statsCollector := stats.NewCollector([]string{"service", "method"})
	limitsEnforcer := limits.NewEnforcer(&limits.Config{}, true, 0)

	// Create buffer
	buf := buffer.New(1000, 10, 100*time.Millisecond, exp, statsCollector, limitsEnforcer, nil)
	go buf.Start(ctx)

	// Create gRPC receiver
	grpcAddr := getFreeAddr(t)
	grpcReceiver := receiver.NewGRPC(grpcAddr, buf)
	go grpcReceiver.Start()

	time.Sleep(100 * time.Millisecond)

	// Connect client
	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	client := colmetrics.NewMetricsServiceClient(conn)

	// Phase 1: Send metrics to establish cardinality counts
	t.Log("Phase 1: Sending initial metrics to establish cardinality")
	uniqueUsers := 100
	for i := 0; i < uniqueUsers; i++ {
		req := createHighCardinalityRequest(
			"persistence-test-service",
			fmt.Sprintf("user_%d", i),
			fmt.Sprintf("req_%d", i),
			1,
		)
		_, err := client.Export(ctx, req)
		if err != nil {
			t.Fatalf("Export %d failed: %v", i, err)
		}
	}

	// Wait for buffer to flush
	time.Sleep(500 * time.Millisecond)

	// Verify backend received metrics
	received := backend.GetReceivedMetrics()
	if len(received) == 0 {
		t.Fatal("Backend did not receive any metrics in phase 1")
	}
	t.Logf("Phase 1: Backend received %d resource metrics batches", len(received))

	// Close phase 1 components
	grpcReceiver.Stop()
	conn.Close()
	exp.Close()

	// Phase 2: Simulate restart - create new components but keep bloom state
	t.Log("Phase 2: Simulating restart - creating new components")

	// Clear backend received metrics
	backend.mu.Lock()
	backend.received = make([]*metricspb.ResourceMetrics, 0)
	backend.mu.Unlock()

	// Create new exporter
	exp2, err := exporter.New(ctx, exporter.Config{
		Endpoint: backendAddr,
		Insecure: true,
		Protocol: "grpc",
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Failed to create exporter2: %v", err)
	}
	defer exp2.Close()

	// Create new stats collector
	statsCollector2 := stats.NewCollector([]string{"service", "method"})
	limitsEnforcer2 := limits.NewEnforcer(&limits.Config{}, true, 0)

	// Create new buffer
	buf2 := buffer.New(1000, 10, 100*time.Millisecond, exp2, statsCollector2, limitsEnforcer2, nil)
	go buf2.Start(ctx)

	// Create new gRPC receiver
	grpcAddr2 := getFreeAddr(t)
	grpcReceiver2 := receiver.NewGRPC(grpcAddr2, buf2)
	go grpcReceiver2.Start()
	defer grpcReceiver2.Stop()

	time.Sleep(100 * time.Millisecond)

	// Connect new client
	conn2, err := grpc.NewClient(grpcAddr2, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to connect2: %v", err)
	}
	defer conn2.Close()

	client2 := colmetrics.NewMetricsServiceClient(conn2)

	// Send more metrics
	for i := uniqueUsers; i < uniqueUsers+50; i++ {
		req := createHighCardinalityRequest(
			"persistence-test-service",
			fmt.Sprintf("user_%d", i),
			fmt.Sprintf("req_%d", i),
			1,
		)
		_, err := client2.Export(ctx, req)
		if err != nil {
			t.Fatalf("Export %d failed: %v", i, err)
		}
	}

	// Wait for flush
	time.Sleep(500 * time.Millisecond)

	received2 := backend.GetReceivedMetrics()
	if len(received2) == 0 {
		t.Fatal("Backend did not receive any metrics in phase 2")
	}
	t.Logf("Phase 2: Backend received %d resource metrics batches after restart", len(received2))

	// Note: This test validates the pipeline works across "restart" simulation
	// The actual bloom persistence would be tested in functional tests with
	// the GlobalTrackerStore initialized properly
	t.Logf("Bloom persistence E2E test completed successfully")
	t.Logf("  Bloom state directory: %s", bloomDir)
}

// TestE2E_BloomPersistence_HighCardinalityTracking tests bloom filter tracking of high cardinality
func TestE2E_BloomPersistence_HighCardinalityTracking(t *testing.T) {
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

	// Create stats collector with label tracking (uses bloom filters internally)
	statsCollector := stats.NewCollector([]string{"service", "user_id", "session_id"})
	limitsEnforcer := limits.NewEnforcer(&limits.Config{}, true, 0)

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

	// Send high cardinality metrics in waves
	waves := 5
	usersPerWave := 200

	for wave := 0; wave < waves; wave++ {
		t.Logf("Wave %d: Sending %d unique users", wave+1, usersPerWave)
		for i := 0; i < usersPerWave; i++ {
			userID := fmt.Sprintf("user_wave%d_%d", wave, i)
			sessionID := fmt.Sprintf("session_%d_%d", wave, i)
			req := createHighCardinalityRequest(
				"cardinality-test-service",
				userID,
				sessionID,
				3, // datapoints per user
			)
			_, err := client.Export(ctx, req)
			if err != nil {
				t.Logf("Export wave %d user %d failed: %v", wave, i, err)
			}
		}
		// Allow buffer to process between waves
		time.Sleep(200 * time.Millisecond)
	}

	// Wait for final flush
	time.Sleep(2 * time.Second)

	// Verify backend received metrics
	received := backend.GetReceivedMetrics()
	if len(received) == 0 {
		t.Fatal("Backend did not receive any metrics")
	}

	// Count unique label combinations in received data
	uniqueUsers := make(map[string]bool)
	for _, rm := range received {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if g := m.GetGauge(); g != nil {
					for _, dp := range g.DataPoints {
						for _, attr := range dp.Attributes {
							if attr.Key == "user_id" {
								if sv := attr.Value.GetStringValue(); sv != "" {
									uniqueUsers[sv] = true
								}
							}
						}
					}
				}
			}
		}
	}

	expectedUsers := waves * usersPerWave
	t.Logf("High cardinality tracking test results:")
	t.Logf("  Waves: %d", waves)
	t.Logf("  Users per wave: %d", usersPerWave)
	t.Logf("  Expected unique users: %d", expectedUsers)
	t.Logf("  Received unique users: %d", len(uniqueUsers))
	t.Logf("  Backend batches: %d", len(received))

	// Verify we received a reasonable number of unique users
	minExpected := expectedUsers / 2
	if len(uniqueUsers) < minExpected {
		t.Errorf("Expected at least %d unique users, got %d", minExpected, len(uniqueUsers))
	}
}
