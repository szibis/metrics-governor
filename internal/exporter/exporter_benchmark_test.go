package exporter

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	"github.com/szibis/metrics-governor/internal/compression"
)

// mockGRPCServer is a mock OTLP gRPC server for benchmarking
type mockGRPCServer struct {
	colmetricspb.UnimplementedMetricsServiceServer
}

func (m *mockGRPCServer) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) (*colmetricspb.ExportMetricsServiceResponse, error) {
	return &colmetricspb.ExportMetricsServiceResponse{}, nil
}

func startMockGRPCServer(b *testing.B) (string, func()) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("Failed to listen: %v", err)
	}

	server := grpc.NewServer()
	colmetricspb.RegisterMetricsServiceServer(server, &mockGRPCServer{})

	go server.Serve(l)

	return l.Addr().String(), func() {
		server.Stop()
	}
}

// BenchmarkExporter_GRPC benchmarks gRPC export
func BenchmarkExporter_GRPC(b *testing.B) {
	addr, cleanup := startMockGRPCServer(b)
	defer cleanup()

	ctx := context.Background()
	exp, err := New(ctx, Config{
		Endpoint: addr,
		Protocol: ProtocolGRPC,
		Insecure: true,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		b.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	req := createBenchmarkRequest(100, 10)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = exp.Export(ctx, req)
	}
}

// BenchmarkExporter_GRPC_Concurrent benchmarks concurrent gRPC exports
func BenchmarkExporter_GRPC_Concurrent(b *testing.B) {
	addr, cleanup := startMockGRPCServer(b)
	defer cleanup()

	ctx := context.Background()
	exp, err := New(ctx, Config{
		Endpoint: addr,
		Protocol: ProtocolGRPC,
		Insecure: true,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		b.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	req := createBenchmarkRequest(100, 10)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = exp.Export(ctx, req)
		}
	})
}

// BenchmarkExporter_HTTP benchmarks HTTP export
func BenchmarkExporter_HTTP(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx := context.Background()
	exp, err := New(ctx, Config{
		Endpoint: server.URL,
		Protocol: ProtocolHTTP,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		b.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	req := createBenchmarkRequest(100, 10)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = exp.Export(ctx, req)
	}
}

// BenchmarkExporter_HTTP_Concurrent benchmarks concurrent HTTP exports
func BenchmarkExporter_HTTP_Concurrent(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx := context.Background()
	exp, err := New(ctx, Config{
		Endpoint: server.URL,
		Protocol: ProtocolHTTP,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		b.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	req := createBenchmarkRequest(100, 10)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = exp.Export(ctx, req)
		}
	})
}

// BenchmarkExporter_HTTP_WithCompression benchmarks HTTP export with compression
func BenchmarkExporter_HTTP_WithCompression(b *testing.B) {
	compressionTypes := []struct {
		name string
		cfg  compression.Config
	}{
		{"none", compression.Config{Type: compression.TypeNone}},
		{"gzip", compression.Config{Type: compression.TypeGzip, Level: compression.LevelDefault}},
		{"zstd", compression.Config{Type: compression.TypeZstd, Level: compression.LevelDefault}},
		{"snappy", compression.Config{Type: compression.TypeSnappy}},
	}

	for _, ct := range compressionTypes {
		b.Run(ct.name, func(b *testing.B) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			ctx := context.Background()
			exp, err := New(ctx, Config{
				Endpoint:    server.URL,
				Protocol:    ProtocolHTTP,
				Timeout:     5 * time.Second,
				Compression: ct.cfg,
			})
			if err != nil {
				b.Fatalf("Failed to create exporter: %v", err)
			}
			defer exp.Close()

			req := createBenchmarkRequest(100, 100)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = exp.Export(ctx, req)
			}
		})
	}
}

// BenchmarkExporter_GRPC_Scale benchmarks gRPC export at different scales
func BenchmarkExporter_GRPC_Scale(b *testing.B) {
	scales := []struct {
		name       string
		metrics    int
		datapoints int
	}{
		{"small_10x10", 10, 10},
		{"medium_100x100", 100, 100},
		{"large_500x100", 500, 100},
	}

	for _, scale := range scales {
		b.Run(scale.name, func(b *testing.B) {
			addr, cleanup := startMockGRPCServer(b)
			defer cleanup()

			ctx := context.Background()
			exp, err := New(ctx, Config{
				Endpoint: addr,
				Protocol: ProtocolGRPC,
				Insecure: true,
				Timeout:  5 * time.Second,
			})
			if err != nil {
				b.Fatalf("Failed to create exporter: %v", err)
			}
			defer exp.Close()

			req := createBenchmarkRequest(scale.metrics, scale.datapoints)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = exp.Export(ctx, req)
			}
		})
	}
}

// BenchmarkProtobuf_Marshal benchmarks protobuf marshaling (baseline)
func BenchmarkProtobuf_Marshal(b *testing.B) {
	sizes := []struct {
		name       string
		metrics    int
		datapoints int
	}{
		{"small_10x10", 10, 10},
		{"medium_100x100", 100, 100},
		{"large_500x100", 500, 100},
	}

	for _, size := range sizes {
		b.Run(size.name, func(b *testing.B) {
			req := createBenchmarkRequest(size.metrics, size.datapoints)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = proto.Marshal(req)
			}
		})
	}
}

// Helper function

func createBenchmarkRequest(numMetrics, datapointsPerMetric int) *colmetricspb.ExportMetricsServiceRequest {
	metrics := make([]*metricspb.Metric, numMetrics)
	for i := 0; i < numMetrics; i++ {
		dps := make([]*metricspb.NumberDataPoint, datapointsPerMetric)
		for j := 0; j < datapointsPerMetric; j++ {
			dps[j] = &metricspb.NumberDataPoint{
				TimeUnixNano: uint64(time.Now().UnixNano()),
				Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(j)},
				Attributes: []*commonpb.KeyValue{
					{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "benchmark-service"}}},
					{Key: "env", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "prod"}}},
				},
			}
		}
		metrics[i] = &metricspb.Metric{
			Name: fmt.Sprintf("benchmark_metric_%d", i),
			Data: &metricspb.Metric_Gauge{
				Gauge: &metricspb.Gauge{
					DataPoints: dps,
				},
			},
		}
	}

	return &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "benchmark-service"}}},
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
