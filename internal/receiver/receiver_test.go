package receiver

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/buffer"
	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
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
	return buffer.New(100, 50, time.Second, &mockExporter{}, nil, nil, nil)
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
	req.Header.Set("Content-Type", "text/xml")

	rec := httptest.NewRecorder()
	r.handleMetrics(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("expected status 415, got %d", rec.Code)
	}
}

func TestHTTPHandleMetricsJSON(t *testing.T) {
	buf := newTestBuffer()

	r := NewHTTP(":4318", buf)

	// Minimal valid OTLP JSON request
	jsonBody := []byte(`{"resourceMetrics":[{"scopeMetrics":[{"metrics":[{"name":"json.test.metric"}]}]}]}`)

	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.handleMetrics(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d; body: %s", rec.Code, rec.Body.String())
	}

	if rec.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type 'application/json', got '%s'", rec.Header().Get("Content-Type"))
	}
}

func TestHTTPHandleMetricsInvalidJSON(t *testing.T) {
	buf := newTestBuffer()

	r := NewHTTP(":4318", buf)

	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader([]byte("{invalid json")))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.handleMetrics(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 for invalid JSON, got %d", rec.Code)
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

func TestNewGRPCWithConfig(t *testing.T) {
	buf := newTestBuffer()

	cfg := GRPCConfig{
		Addr: ":4317",
	}

	r := NewGRPCWithConfig(cfg, buf)
	if r == nil {
		t.Fatal("expected non-nil receiver")
	}
	if r.addr != ":4317" {
		t.Errorf("expected addr ':4317', got '%s'", r.addr)
	}
}

func TestNewHTTPWithConfig(t *testing.T) {
	buf := newTestBuffer()

	cfg := HTTPConfig{
		Addr: ":4318",
		Server: HTTPServerConfig{
			ReadTimeout:       30 * time.Second,
			ReadHeaderTimeout: 10 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       120 * time.Second,
			KeepAlivesEnabled: true,
		},
	}

	r := NewHTTPWithConfig(cfg, buf)
	if r == nil {
		t.Fatal("expected non-nil receiver")
	}
	if r.addr != ":4318" {
		t.Errorf("expected addr ':4318', got '%s'", r.addr)
	}
	if r.server.ReadTimeout != 30*time.Second {
		t.Errorf("expected ReadTimeout 30s, got %v", r.server.ReadTimeout)
	}
	if r.server.WriteTimeout != 60*time.Second {
		t.Errorf("expected WriteTimeout 60s, got %v", r.server.WriteTimeout)
	}
}

func TestHTTPWithConfigMaxRequestBodySize(t *testing.T) {
	buf := newTestBuffer()

	cfg := HTTPConfig{
		Addr: ":4318",
		Server: HTTPServerConfig{
			MaxRequestBodySize: 10, // Very small limit
		},
	}

	r := NewHTTPWithConfig(cfg, buf)

	// Create a request larger than the limit
	exportReq := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{Name: "http.test.metric.with.a.very.long.name.that.exceeds.the.limit"},
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
	req.ContentLength = int64(len(body))

	rec := httptest.NewRecorder()
	r.handleMetrics(rec, req)

	// With LimitReader, the body gets truncated, causing protobuf unmarshal to fail
	// So we expect a 400 Bad Request (protobuf parse error)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected status 400 (truncated body fails to parse), got %d", rec.Code)
	}
}

func TestHTTPHandleMetricsEmptyBody(t *testing.T) {
	buf := newTestBuffer()

	r := NewHTTP(":4318", buf)

	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader([]byte{}))
	req.Header.Set("Content-Type", "application/x-protobuf")

	rec := httptest.NewRecorder()
	r.handleMetrics(rec, req)

	// Empty body should be handled (empty protobuf is valid)
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}
}

func TestHTTPHandleMetricsNoContentType(t *testing.T) {
	buf := newTestBuffer()

	r := NewHTTP(":4318", buf)

	exportReq := &colmetricspb.ExportMetricsServiceRequest{}
	body, _ := proto.Marshal(exportReq)

	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(body))
	// No Content-Type header

	rec := httptest.NewRecorder()
	r.handleMetrics(rec, req)

	// Without content type, should still try protobuf
	if rec.Code != http.StatusOK && rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("expected status 200 or 415, got %d", rec.Code)
	}
}

func TestGRPCExportEmptyRequest(t *testing.T) {
	buf := newTestBuffer()

	r := NewGRPC(":4317", buf)

	req := &colmetricspb.ExportMetricsServiceRequest{}

	resp, err := r.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
	if resp == nil {
		t.Error("expected non-nil response")
	}
}

func TestGRPCExportMultipleMetrics(t *testing.T) {
	buf := newTestBuffer()

	r := NewGRPC(":4317", buf)

	metrics := make([]*metricspb.Metric, 100)
	for i := 0; i < 100; i++ {
		metrics[i] = &metricspb.Metric{Name: "test.metric"}
	}

	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: metrics,
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

func TestHTTPReceiverCustomPath(t *testing.T) {
	buf := newTestBuffer()

	cfg := HTTPConfig{
		Addr: ":4318",
		Path: "/custom/otlp/metrics",
	}

	r := NewHTTPWithConfig(cfg, buf)

	// Create a test request
	exportReq := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{Name: "custom.path.test.metric"},
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

	// Request to custom path should work
	req := httptest.NewRequest(http.MethodPost, "/custom/otlp/metrics", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")

	rec := httptest.NewRecorder()
	r.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("custom path: expected status 200, got %d", rec.Code)
	}

	// Request to default path should 404 when custom path is set
	req2 := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "application/x-protobuf")

	rec2 := httptest.NewRecorder()
	r.server.Handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusNotFound {
		t.Errorf("default path with custom config: expected status 404, got %d", rec2.Code)
	}
}

func TestHTTPReceiverDefaultPath(t *testing.T) {
	buf := newTestBuffer()

	// When Path is empty, default /v1/metrics should be used
	r := NewHTTP(":4318", buf)

	exportReq := &colmetricspb.ExportMetricsServiceRequest{}
	body, _ := proto.Marshal(exportReq)

	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")

	rec := httptest.NewRecorder()
	r.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("default path: expected status 200, got %d", rec.Code)
	}
}

func TestHTTPReceiverVictoriaMetricsPath(t *testing.T) {
	buf := newTestBuffer()

	cfg := HTTPConfig{
		Addr: ":4318",
		Path: "/opentelemetry/v1/metrics",
	}

	r := NewHTTPWithConfig(cfg, buf)

	exportReq := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{Name: "vm.path.test"},
						},
					},
				},
			},
		},
	}

	body, _ := proto.Marshal(exportReq)

	// VictoriaMetrics OTLP path
	req := httptest.NewRequest(http.MethodPost, "/opentelemetry/v1/metrics", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")

	rec := httptest.NewRecorder()
	r.server.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("VictoriaMetrics OTLP path: expected status 200, got %d", rec.Code)
	}
}
