package receiver

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/buffer"
	"github.com/szibis/metrics-governor/internal/queue"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// makeExportRequest builds an ExportMetricsServiceRequest with a single gauge
// datapoint. The serialized size is large enough (~100+ bytes) to exceed a
// 1-byte buffer limit, triggering ErrBufferFull reliably.
func makeExportRequest() *colmetricspb.ExportMetricsServiceRequest {
	return &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "backpressure-test"}}},
					},
				},
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{
								Name: "test.backpressure.gauge",
								Data: &metricspb.Metric_Gauge{
									Gauge: &metricspb.Gauge{
										DataPoints: []*metricspb.NumberDataPoint{
											{
												Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 42.0},
											},
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

// newFullBuffer returns a MetricsBuffer with a 1-byte capacity and reject
// (DropNewest) policy. Any Add() with real metric data will exceed capacity
// and return buffer.ErrBufferFull.
func newFullBuffer() *buffer.MetricsBuffer {
	return buffer.New(
		100,             // maxSize
		50,              // maxBatchSize
		time.Second,     // flushInterval
		&mockExporter{}, // exporter
		nil,             // stats
		nil,             // limits
		nil,             // logAggregator
		buffer.WithMaxBufferBytes(1),
		buffer.WithBufferFullPolicy(queue.DropNewest),
	)
}

// ---------------------------------------------------------------------------
// HTTP backpressure tests
// ---------------------------------------------------------------------------

func TestHTTP_BufferFull_Returns429(t *testing.T) {
	buf := newFullBuffer()
	r := NewHTTP(":4318", buf)

	body, err := proto.Marshal(makeExportRequest())
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")

	rec := httptest.NewRecorder()
	r.handleMetrics(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("expected HTTP 429, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

func TestHTTP_BufferFull_RetryAfterHeader(t *testing.T) {
	buf := newFullBuffer()
	r := NewHTTP(":4318", buf)

	body, err := proto.Marshal(makeExportRequest())
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")

	rec := httptest.NewRecorder()
	r.handleMetrics(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected HTTP 429, got %d", rec.Code)
	}

	retryAfter := rec.Header().Get("Retry-After")
	if retryAfter != "5" {
		t.Errorf("expected Retry-After header '5', got '%s'", retryAfter)
	}
}

func TestHTTP_BufferOK_Returns200(t *testing.T) {
	buf := newTestBuffer() // normal buffer with plenty of capacity
	r := NewHTTP(":4318", buf)

	body, err := proto.Marshal(makeExportRequest())
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")

	rec := httptest.NewRecorder()
	r.handleMetrics(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected HTTP 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// gRPC backpressure tests
// ---------------------------------------------------------------------------

func TestGRPC_BufferFull_ReturnsResourceExhausted(t *testing.T) {
	buf := newFullBuffer()
	r := NewGRPC(":4317", buf)

	resp, err := r.Export(context.Background(), makeExportRequest())
	if err == nil {
		t.Fatal("expected error from Export when buffer is full, got nil")
	}
	if resp != nil {
		t.Error("expected nil response on error")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}
	if st.Code() != codes.ResourceExhausted {
		t.Errorf("expected codes.ResourceExhausted, got %s", st.Code())
	}
}

func TestGRPC_BufferFull_MessageContainsCapacity(t *testing.T) {
	buf := newFullBuffer()
	r := NewGRPC(":4317", buf)

	_, err := r.Export(context.Background(), makeExportRequest())
	if err == nil {
		t.Fatal("expected error from Export when buffer is full, got nil")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("expected gRPC status error, got: %v", err)
	}
	if !strings.Contains(st.Message(), "buffer capacity exceeded") {
		t.Errorf("expected message to contain 'buffer capacity exceeded', got: %s", st.Message())
	}
}

func TestGRPC_BufferOK_ReturnsSuccess(t *testing.T) {
	buf := newTestBuffer() // normal buffer with plenty of capacity
	r := NewGRPC(":4317", buf)

	resp, err := r.Export(context.Background(), makeExportRequest())
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if resp == nil {
		t.Error("expected non-nil response on success")
	}
}
