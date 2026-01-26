package receiver

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/slawomirskowron/metrics-governor/internal/buffer"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// mockExporter implements buffer.Exporter for testing.
type mockExporter struct {
	exported []*colmetricspb.ExportMetricsServiceRequest
}

func (m *mockExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	m.exported = append(m.exported, req)
	return nil
}

func newTestBuffer() *buffer.MetricsBuffer {
	return buffer.New(100, 50, time.Second, &mockExporter{}, nil, nil)
}

func TestNewGRPC(t *testing.T) {
	buf := newTestBuffer()

	r := NewGRPC(":4317", buf)
	if r == nil {
		t.Fatal("expected non-nil receiver")
	}
	if r.addr != ":4317" {
		t.Errorf("expected addr ':4317', got '%s'", r.addr)
	}
	if r.buffer != buf {
		t.Error("buffer not set correctly")
	}
}

func TestGRPCExport(t *testing.T) {
	buf := newTestBuffer()

	r := NewGRPC(":4317", buf)

	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{Name: "test.metric"},
						},
					},
				},
			},
		},
	}

	resp, err := r.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
	if resp == nil {
		t.Error("expected non-nil response")
	}
}

func TestNewHTTP(t *testing.T) {
	buf := newTestBuffer()

	r := NewHTTP(":4318", buf)
	if r == nil {
		t.Fatal("expected non-nil receiver")
	}
	if r.addr != ":4318" {
		t.Errorf("expected addr ':4318', got '%s'", r.addr)
	}
	if r.buffer != buf {
		t.Error("buffer not set correctly")
	}
	if r.server == nil {
		t.Error("expected server to be created")
	}
}

func TestHTTPHandleMetrics(t *testing.T) {
	buf := newTestBuffer()

	r := NewHTTP(":4318", buf)

	// Create a test request
	exportReq := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{Name: "http.test.metric"},
						},
					},
				},
			},
		},
	}

	body, err := proto.Marshal(exportReq)
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")

	rec := httptest.NewRecorder()
	r.handleMetrics(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	if rec.Header().Get("Content-Type") != "application/x-protobuf" {
		t.Errorf("expected Content-Type 'application/x-protobuf', got '%s'", rec.Header().Get("Content-Type"))
	}
}

func TestHTTPHandleMetricsMethodNotAllowed(t *testing.T) {
	buf := newTestBuffer()

	r := NewHTTP(":4318", buf)

	req := httptest.NewRequest(http.MethodGet, "/v1/metrics", nil)
	rec := httptest.NewRecorder()
	r.handleMetrics(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rec.Code)
	}
}

func TestHTTPHandleMetricsUnsupportedContentType(t *testing.T) {
	buf := newTestBuffer()

	r := NewHTTP(":4318", buf)

	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.handleMetrics(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("expected status 415, got %d", rec.Code)
	}
}

func TestHTTPHandleMetricsInvalidProtobuf(t *testing.T) {
	buf := newTestBuffer()

	r := NewHTTP(":4318", buf)

	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader([]byte("invalid protobuf")))
	req.Header.Set("Content-Type", "application/x-protobuf")

	rec := httptest.NewRecorder()
	r.handleMetrics(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rec.Code)
	}
}

func TestHTTPStopServer(t *testing.T) {
	buf := newTestBuffer()

	r := NewHTTP("127.0.0.1:0", buf)

	// Start server in background
	serverStarted := make(chan struct{})
	go func() {
		close(serverStarted)
		r.Start()
	}()

	<-serverStarted
	time.Sleep(50 * time.Millisecond) // Give server time to start

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := r.Stop(ctx)
	if err != nil {
		t.Errorf("stop failed: %v", err)
	}
}

func TestGRPCStartStop(t *testing.T) {
	buf := newTestBuffer()

	r := NewGRPC("127.0.0.1:0", buf)

	// Start server in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- r.Start()
	}()

	time.Sleep(50 * time.Millisecond) // Give server time to start

	r.Stop()

	// Should not get an error (graceful shutdown)
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("unexpected error from Start: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("timeout waiting for server to stop")
	}
}
