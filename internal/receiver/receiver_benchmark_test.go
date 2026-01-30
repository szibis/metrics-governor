package receiver

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"

	"github.com/slawomirskowron/metrics-governor/internal/buffer"
)

// noopExporter discards all exports
type noopExporter struct{}

func (n *noopExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	return nil
}

func newBenchmarkBuffer() *buffer.MetricsBuffer {
	return buffer.New(100000, 1000, time.Hour, &noopExporter{}, nil, nil, nil)
}

// BenchmarkGRPCReceiver_Export benchmarks the gRPC Export method
func BenchmarkGRPCReceiver_Export(b *testing.B) {
	buf := newBenchmarkBuffer()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Start(ctx)

	r := NewGRPC(":0", buf)
	req := createBenchmarkExportRequest(100, 10)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = r.Export(context.Background(), req)
	}
}

// BenchmarkGRPCReceiver_Export_Concurrent benchmarks concurrent gRPC exports
func BenchmarkGRPCReceiver_Export_Concurrent(b *testing.B) {
	buf := newBenchmarkBuffer()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Start(ctx)

	r := NewGRPC(":0", buf)
	req := createBenchmarkExportRequest(100, 10)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = r.Export(context.Background(), req)
		}
	})
}

// BenchmarkGRPCReceiver_Export_Scale benchmarks at different scales
func BenchmarkGRPCReceiver_Export_Scale(b *testing.B) {
	scales := []struct {
		name       string
		metrics    int
		datapoints int
	}{
		{"small_10x10", 10, 10},
		{"medium_100x100", 100, 100},
		{"large_500x100", 500, 100},
		{"xlarge_1000x100", 1000, 100},
	}

	for _, scale := range scales {
		b.Run(scale.name, func(b *testing.B) {
			buf := newBenchmarkBuffer()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go buf.Start(ctx)

			r := NewGRPC(":0", buf)
			req := createBenchmarkExportRequest(scale.metrics, scale.datapoints)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = r.Export(context.Background(), req)
			}
		})
	}
}

// BenchmarkHTTPReceiver_HandleMetrics benchmarks HTTP request handling
func BenchmarkHTTPReceiver_HandleMetrics(b *testing.B) {
	buf := newBenchmarkBuffer()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Start(ctx)

	r := NewHTTP(":0", buf)
	exportReq := createBenchmarkExportRequest(100, 10)
	body, _ := proto.Marshal(exportReq)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/x-protobuf")
		rec := httptest.NewRecorder()
		r.handleMetrics(rec, req)
	}
}

// BenchmarkHTTPReceiver_HandleMetrics_Concurrent benchmarks concurrent HTTP requests
func BenchmarkHTTPReceiver_HandleMetrics_Concurrent(b *testing.B) {
	buf := newBenchmarkBuffer()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Start(ctx)

	r := NewHTTP(":0", buf)
	exportReq := createBenchmarkExportRequest(100, 10)
	body, _ := proto.Marshal(exportReq)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/x-protobuf")
			rec := httptest.NewRecorder()
			r.handleMetrics(rec, req)
		}
	})
}

// BenchmarkHTTPReceiver_HandleMetrics_Scale benchmarks at different payload sizes
func BenchmarkHTTPReceiver_HandleMetrics_Scale(b *testing.B) {
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
			buf := newBenchmarkBuffer()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go buf.Start(ctx)

			r := NewHTTP(":0", buf)
			exportReq := createBenchmarkExportRequest(scale.metrics, scale.datapoints)
			body, _ := proto.Marshal(exportReq)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/x-protobuf")
				rec := httptest.NewRecorder()
				r.handleMetrics(rec, req)
			}
		})
	}
}

// BenchmarkProtobuf_Unmarshal benchmarks protobuf unmarshaling (baseline)
func BenchmarkProtobuf_Unmarshal(b *testing.B) {
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
			exportReq := createBenchmarkExportRequest(size.metrics, size.datapoints)
			body, _ := proto.Marshal(exportReq)

			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var req colmetricspb.ExportMetricsServiceRequest
				_ = proto.Unmarshal(body, &req)
			}
		})
	}
}

// Helper function

func createBenchmarkExportRequest(numMetrics, datapointsPerMetric int) *colmetricspb.ExportMetricsServiceRequest {
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
