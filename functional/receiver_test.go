package functional

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net"
	"net/http"
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
	"github.com/szibis/metrics-governor/internal/receiver"
)

// mockExporter implements buffer.Exporter for testing
type mockExporter struct {
	exported []*colmetrics.ExportMetricsServiceRequest
}

func (m *mockExporter) Export(ctx context.Context, req *colmetrics.ExportMetricsServiceRequest) error {
	m.exported = append(m.exported, req)
	return nil
}

func (m *mockExporter) GetExported() []*colmetrics.ExportMetricsServiceRequest {
	return m.exported
}

// mockStatsCollector implements buffer.StatsCollector for testing
type mockStatsCollector struct{}

func (m *mockStatsCollector) Process(resourceMetrics []*metricspb.ResourceMetrics) {}
func (m *mockStatsCollector) RecordReceived(count int)                             {}
func (m *mockStatsCollector) RecordExport(datapointCount int)                      {}
func (m *mockStatsCollector) RecordExportError()                                   {}

// mockLimitsEnforcer implements buffer.LimitsEnforcer for testing
type mockLimitsEnforcer struct{}

func (m *mockLimitsEnforcer) Process(resourceMetrics []*metricspb.ResourceMetrics) []*metricspb.ResourceMetrics {
	return resourceMetrics // Pass through
}

func newTestBuffer() *buffer.MetricsBuffer {
	return buffer.New(1000, 100, 100*time.Millisecond, &mockExporter{}, &mockStatsCollector{}, &mockLimitsEnforcer{}, nil)
}

// TestFunctional_GRPCReceiver_BasicFlow tests basic gRPC receiver functionality
func TestFunctional_GRPCReceiver_BasicFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create buffer and receiver
	buf := newTestBuffer()
	go buf.Start(ctx)
	defer cancel()

	addr := getFreeAddr(t)
	grpcReceiver := receiver.NewGRPC(addr, buf)

	go grpcReceiver.Start()
	defer grpcReceiver.Stop()

	time.Sleep(100 * time.Millisecond)

	// Connect and send metrics
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := colmetrics.NewMetricsServiceClient(conn)
	req := createTestRequest("grpc-test", "grpc-metric", 3)

	resp, err := client.Export(ctx, req)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	if resp == nil {
		t.Fatal("Expected non-nil response")
	}

	t.Log("gRPC receiver basic flow test passed")
}

// TestFunctional_HTTPReceiver_BasicFlow tests basic HTTP receiver functionality
func TestFunctional_HTTPReceiver_BasicFlow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	buf := newTestBuffer()
	go buf.Start(ctx)
	defer cancel()

	addr := getFreeAddr(t)
	httpReceiver := receiver.NewHTTP(addr, buf)

	go httpReceiver.Start()
	defer httpReceiver.Stop(ctx)

	time.Sleep(100 * time.Millisecond)

	// Send metrics via HTTP
	req := createTestRequest("http-test", "http-metric", 5)
	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/v1/metrics", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	t.Log("HTTP receiver basic flow test passed")
}

// TestFunctional_HTTPReceiver_WithCompression tests HTTP receiver with gzip compression
func TestFunctional_HTTPReceiver_WithCompression(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	buf := newTestBuffer()
	go buf.Start(ctx)
	defer cancel()

	addr := getFreeAddr(t)
	httpReceiver := receiver.NewHTTP(addr, buf)

	go httpReceiver.Start()
	defer httpReceiver.Stop(ctx)

	time.Sleep(100 * time.Millisecond)

	// Create and compress request
	req := createTestRequest("compression-test", "compressed-metric", 10)
	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var compressed bytes.Buffer
	gzWriter := gzip.NewWriter(&compressed)
	_, err = gzWriter.Write(data)
	if err != nil {
		t.Fatalf("Failed to compress: %v", err)
	}
	gzWriter.Close()

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/v1/metrics", &compressed)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Content-Encoding", "gzip")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	t.Log("HTTP receiver compression test passed")
}

// TestFunctional_GRPCReceiver_MultipleClients tests gRPC receiver with multiple clients
func TestFunctional_GRPCReceiver_MultipleClients(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	buf := newTestBuffer()
	go buf.Start(ctx)
	defer cancel()

	addr := getFreeAddr(t)
	grpcReceiver := receiver.NewGRPC(addr, buf)

	go grpcReceiver.Start()
	defer grpcReceiver.Stop()

	time.Sleep(100 * time.Millisecond)

	// Create multiple clients and send metrics
	numClients := 5
	for i := 0; i < numClients; i++ {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatalf("Client %d failed to connect: %v", i, err)
		}

		client := colmetrics.NewMetricsServiceClient(conn)
		req := createTestRequest("multi-client-test", "multi-client-metric", 2)

		resp, err := client.Export(ctx, req)
		if err != nil {
			t.Fatalf("Client %d export failed: %v", i, err)
		}
		if resp == nil {
			t.Fatalf("Client %d: expected non-nil response", i)
		}

		conn.Close()
	}

	t.Logf("Successfully handled %d clients", numClients)
}

// TestFunctional_HTTPReceiver_MethodNotAllowed tests HTTP receiver rejects wrong methods
func TestFunctional_HTTPReceiver_MethodNotAllowed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	buf := newTestBuffer()
	go buf.Start(ctx)
	defer cancel()

	addr := getFreeAddr(t)
	httpReceiver := receiver.NewHTTP(addr, buf)

	go httpReceiver.Start()
	defer httpReceiver.Stop(ctx)

	time.Sleep(100 * time.Millisecond)

	// Try GET request (should fail)
	httpReq, _ := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/v1/metrics", nil)
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("Expected 405, got %d", resp.StatusCode)
	}
}

// TestFunctional_HTTPReceiver_InvalidProtobuf tests HTTP receiver rejects invalid protobuf
func TestFunctional_HTTPReceiver_InvalidProtobuf(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	buf := newTestBuffer()
	go buf.Start(ctx)
	defer cancel()

	addr := getFreeAddr(t)
	httpReceiver := receiver.NewHTTP(addr, buf)

	go httpReceiver.Start()
	defer httpReceiver.Stop(ctx)

	time.Sleep(100 * time.Millisecond)

	// Send invalid protobuf
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/v1/metrics", bytes.NewReader([]byte("invalid protobuf data")))
	httpReq.Header.Set("Content-Type", "application/x-protobuf")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("Request failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected 400, got %d", resp.StatusCode)
	}
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

func createTestRequest(serviceName, metricName string, datapoints int) *colmetrics.ExportMetricsServiceRequest {
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

// TestFunctional_GRPCReceiver_HighCardinality tests receiver with high cardinality metrics
func TestFunctional_GRPCReceiver_HighCardinality(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	buf := newTestBuffer()
	go buf.Start(ctx)

	addr := getFreeAddr(t)
	grpcReceiver := receiver.NewGRPC(addr, buf)

	go grpcReceiver.Start()
	defer grpcReceiver.Stop()

	time.Sleep(100 * time.Millisecond)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := colmetrics.NewMetricsServiceClient(conn)

	// Send many requests with unique label combinations
	numRequests := 500
	for i := 0; i < numRequests; i++ {
		req := createHighCardinalityRequest(i)
		_, err := client.Export(ctx, req)
		if err != nil {
			t.Fatalf("Export %d failed: %v", i, err)
		}
	}

	t.Logf("Successfully processed %d high cardinality requests", numRequests)
}

// TestFunctional_GRPCReceiver_ManyDatapoints tests receiver with many datapoints
func TestFunctional_GRPCReceiver_ManyDatapoints(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	buf := newTestBuffer()
	go buf.Start(ctx)

	addr := getFreeAddr(t)
	grpcReceiver := receiver.NewGRPC(addr, buf)

	go grpcReceiver.Start()
	defer grpcReceiver.Stop()

	time.Sleep(100 * time.Millisecond)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := colmetrics.NewMetricsServiceClient(conn)

	// Send request with many datapoints
	req := createTestRequest("many-datapoints-test", "many-datapoints-metric", 5000)
	_, err = client.Export(ctx, req)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	t.Log("Successfully processed request with 5000 datapoints")
}

// TestFunctional_GRPCReceiver_EdgeCaseValues tests receiver with edge case metric values
func TestFunctional_GRPCReceiver_EdgeCaseValues(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	buf := newTestBuffer()
	go buf.Start(ctx)

	addr := getFreeAddr(t)
	grpcReceiver := receiver.NewGRPC(addr, buf)

	go grpcReceiver.Start()
	defer grpcReceiver.Stop()

	time.Sleep(100 * time.Millisecond)

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := colmetrics.NewMetricsServiceClient(conn)

	// Send edge case values
	req := createEdgeCaseValuesRequest()
	_, err = client.Export(ctx, req)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	t.Log("Successfully processed edge case values")
}

// Helper functions for new tests

func createHighCardinalityRequest(index int) *colmetrics.ExportMetricsServiceRequest {
	dps := make([]*metricspb.NumberDataPoint, 5)
	for i := 0; i < 5; i++ {
		dps[i] = &metricspb.NumberDataPoint{
			TimeUnixNano: uint64(time.Now().UnixNano()),
			Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)},
			Attributes: []*commonpb.KeyValue{
				{Key: "user_id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "user_" + string(rune('0'+index%10))}}},
				{Key: "session_id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "sess_" + string(rune('0'+index))}}},
				{Key: "request_id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "req_" + string(rune('0'+index)) + "_" + string(rune('0'+i))}}},
			},
		}
	}

	return &colmetrics.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "high-cardinality-service"}}},
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

func createEdgeCaseValuesRequest() *colmetrics.ExportMetricsServiceRequest {
	edgeValues := []float64{
		0.0,
		-0.0,
		1.0,
		-1.0,
		1.7976931348623157e+308, // Very large
		-1.7976931348623157e+308,
		1e-300, // Very small
		-1e-300,
		3.141592653589793, // Pi
	}

	dps := make([]*metricspb.NumberDataPoint, len(edgeValues))
	for i, val := range edgeValues {
		dps[i] = &metricspb.NumberDataPoint{
			TimeUnixNano: uint64(time.Now().UnixNano()),
			Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: val},
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
								Name: "edge_case_metric",
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
