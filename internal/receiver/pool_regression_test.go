package receiver

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
	"google.golang.org/protobuf/proto"
)

// =============================================================================
// Helpers
// =============================================================================

// poolTestExportRequest creates an ExportMetricsServiceRequest with a single
// metric of the given name and a gauge data point.
func poolTestExportRequest(metricName string) *colmetricspb.ExportMetricsServiceRequest {
	return &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						{Key: "service.name", Value: &commonpb.AnyValue{
							Value: &commonpb.AnyValue_StringValue{StringValue: "pool-test"},
						}},
					},
				},
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{
								Name: metricName,
								Data: &metricspb.Metric_Gauge{
									Gauge: &metricspb.Gauge{
										DataPoints: []*metricspb.NumberDataPoint{
											{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 42.0}},
										},
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

// =============================================================================
// Test 1: Pool does not leak stale data between sequential requests
// =============================================================================

func TestHTTPReceiver_ExportReqPool_NoStaleData(t *testing.T) {
	buf := newTestBuffer()
	r := NewHTTP(":0", buf)

	// --- Request A: metric_A ---
	reqA := poolTestExportRequest("metric_A")
	bodyA, err := proto.Marshal(reqA)
	if err != nil {
		t.Fatalf("failed to marshal request A: %v", err)
	}

	httpReqA := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(bodyA))
	httpReqA.Header.Set("Content-Type", "application/x-protobuf")
	recA := httptest.NewRecorder()
	r.handleMetrics(recA, httpReqA)

	if recA.Code != http.StatusOK {
		t.Fatalf("request A: expected 200, got %d; body: %s", recA.Code, recA.Body.String())
	}

	// --- Request B: metric_B ---
	reqB := poolTestExportRequest("metric_B")
	bodyB, err := proto.Marshal(reqB)
	if err != nil {
		t.Fatalf("failed to marshal request B: %v", err)
	}

	httpReqB := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(bodyB))
	httpReqB.Header.Set("Content-Type", "application/x-protobuf")
	recB := httptest.NewRecorder()
	r.handleMetrics(recB, httpReqB)

	if recB.Code != http.StatusOK {
		t.Fatalf("request B: expected 200, got %d; body: %s", recB.Code, recB.Body.String())
	}

	// Verify the response for B is a valid ExportMetricsServiceResponse (no stale
	// data from A leaking into B's response).
	var resp colmetricspb.ExportMetricsServiceResponse
	if err := proto.Unmarshal(recB.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response B: %v", err)
	}

	// The ExportMetricsServiceResponse should have no partial_success or stale
	// ResourceMetrics from request A. A correct pooled implementation resets the
	// request before unmarshal and nils ResourceMetrics before returning to pool.
	if resp.PartialSuccess != nil && resp.PartialSuccess.RejectedDataPoints != 0 {
		t.Errorf("unexpected partial success in response B: %v", resp.PartialSuccess)
	}
}

// =============================================================================
// Test 2: Pool object is returned even on unmarshal error
// =============================================================================

func TestHTTPReceiver_ExportReqPool_ReleasedOnError(t *testing.T) {
	buf := newTestBuffer()
	r := NewHTTP(":0", buf)

	// Send malformed protobuf 100 times. If pool objects leaked on the error
	// path, we would observe growing memory or pool exhaustion. The test
	// verifies the error path returns 400 consistently without panics.
	invalidBody := []byte{0xFF, 0xFE, 0xFD, 0xFC, 0xFB, 0xFA, 0x00, 0x01}

	for i := 0; i < 100; i++ {
		httpReq := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(invalidBody))
		httpReq.Header.Set("Content-Type", "application/x-protobuf")
		rec := httptest.NewRecorder()
		r.handleMetrics(rec, httpReq)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("iteration %d: expected 400, got %d", i, rec.Code)
		}
	}
}

// =============================================================================
// Test 3: Concurrent HTTP requests through the pool
// =============================================================================

func TestHTTPReceiver_ExportReqPool_ConcurrentRequests(t *testing.T) {
	buf := newTestBuffer()
	r := NewHTTP(":0", buf)

	const goroutines = 16
	const requestsPerGoroutine = 50

	var wg sync.WaitGroup
	errors := make(chan string, goroutines*requestsPerGoroutine)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < requestsPerGoroutine; i++ {
				metricName := fmt.Sprintf("concurrent_metric_%d_%d", id, i)
				exportReq := poolTestExportRequest(metricName)
				body, err := proto.Marshal(exportReq)
				if err != nil {
					errors <- fmt.Sprintf("goroutine %d iter %d: marshal failed: %v", id, i, err)
					continue
				}

				httpReq := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(body))
				httpReq.Header.Set("Content-Type", "application/x-protobuf")
				rec := httptest.NewRecorder()
				r.handleMetrics(rec, httpReq)

				if rec.Code != http.StatusOK {
					errors <- fmt.Sprintf("goroutine %d iter %d: expected 200, got %d", id, i, rec.Code)
				}
			}
		}(g)
	}

	wg.Wait()
	close(errors)

	for errMsg := range errors {
		t.Error(errMsg)
	}
}

// =============================================================================
// Test 4: Concurrent gRPC exports (no pool, but tested for completeness)
// =============================================================================

func TestGRPCReceiver_ExportReqPool_ConcurrentExports(t *testing.T) {
	buf := newTestBuffer()
	r := NewGRPC(":0", buf)

	const goroutines = 16
	const exportsPerGoroutine = 50

	var wg sync.WaitGroup
	errors := make(chan string, goroutines*exportsPerGoroutine)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < exportsPerGoroutine; i++ {
				metricName := fmt.Sprintf("grpc_concurrent_metric_%d_%d", id, i)
				req := poolTestExportRequest(metricName)

				resp, err := r.Export(context.Background(), req)
				if err != nil {
					errors <- fmt.Sprintf("goroutine %d iter %d: Export failed: %v", id, i, err)
					continue
				}
				if resp == nil {
					errors <- fmt.Sprintf("goroutine %d iter %d: nil response", id, i)
				}
			}
		}(g)
	}

	wg.Wait()
	close(errors)

	for errMsg := range errors {
		t.Error(errMsg)
	}
}

// =============================================================================
// Benchmark 5: HTTP receiver handleMetrics allocation tracking
// =============================================================================

func BenchmarkHTTPReceiver_HandleMetrics_Pooled(b *testing.B) {
	scales := []struct {
		name       string
		metrics    int
		datapoints int
	}{
		{"small_10x10", 10, 10},
		{"medium_100x100", 100, 100},
	}

	for _, scale := range scales {
		b.Run(scale.name, func(b *testing.B) {
			buf := newBenchmarkBuffer()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go buf.Start(ctx)

			r := NewHTTP(":0", buf)
			exportReq := createBenchmarkExportRequest(scale.metrics, scale.datapoints)
			body, err := proto.Marshal(exportReq)
			if err != nil {
				b.Fatalf("failed to marshal export request: %v", err)
			}

			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
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

// =============================================================================
// Benchmark 6: Pooled vs fresh protobuf unmarshal
// =============================================================================

func BenchmarkProtobuf_Unmarshal_Pooled(b *testing.B) {
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
		exportReq := createBenchmarkExportRequest(scale.metrics, scale.datapoints)
		body, err := proto.Marshal(exportReq)
		if err != nil {
			b.Fatalf("failed to marshal export request: %v", err)
		}

		b.Run("fresh/"+scale.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var req colmetricspb.ExportMetricsServiceRequest
				_ = proto.Unmarshal(body, &req)
			}
		})

		b.Run("pooled/"+scale.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				req := colmetricspb.ExportMetricsServiceRequestFromVTPool()
				_ = req.UnmarshalVT(body)
				req.ReturnToVTPool()
			}
		})
	}
}
