package receiver

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/compression"
	"github.com/szibis/metrics-governor/internal/prw"
)

// mockPRWBuffer is a mock PRW buffer for testing.
type mockPRWBuffer struct {
	mu       sync.Mutex
	requests []*prw.WriteRequest
}

func (m *mockPRWBuffer) Add(req *prw.WriteRequest) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, req)
}

func (m *mockPRWBuffer) getRequests() []*prw.WriteRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*prw.WriteRequest, len(m.requests))
	copy(result, m.requests)
	return result
}

func TestPRWReceiver_ValidRequest_V1(t *testing.T) {
	buf := &mockPRWBuffer{}
	receiver := NewPRW(":0", buf)

	// Create a valid PRW 1.0 request
	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels: []prw.Label{
					{Name: "__name__", Value: "http_requests_total"},
					{Name: "method", Value: "GET"},
				},
				Samples: []prw.Sample{
					{Value: 1.0, Timestamp: 1609459200000},
				},
			},
		},
	}

	body, _ := req.Marshal()
	compressed, _ := compression.Compress(body, compression.Config{Type: compression.TypeSnappy})

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/write", bytes.NewReader(compressed))
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Content-Encoding", "snappy")

	w := httptest.NewRecorder()
	receiver.server.Handler.ServeHTTP(w, httpReq)

	resp := w.Result()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("Status code = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	// Check that buffer received the request
	requests := buf.getRequests()
	if len(requests) != 1 {
		t.Fatalf("Buffer received %d requests, want 1", len(requests))
	}
	if len(requests[0].Timeseries) != 1 {
		t.Errorf("Timeseries count = %d, want 1", len(requests[0].Timeseries))
	}
}

func TestPRWReceiver_ValidRequest_V2(t *testing.T) {
	buf := &mockPRWBuffer{}
	receiver := NewPRW(":0", buf)

	// Create a PRW 2.0 request with metadata and histograms
	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels: []prw.Label{
					{Name: "__name__", Value: "http_request_duration_seconds"},
				},
				Histograms: []prw.Histogram{
					{
						Count:     100,
						Sum:       50.5,
						Schema:    3,
						Timestamp: 1609459200000,
					},
				},
			},
		},
		Metadata: []prw.MetricMetadata{
			{
				Type:             prw.MetricTypeHistogram,
				MetricFamilyName: "http_request_duration_seconds",
				Help:             "Request duration",
			},
		},
	}

	body, _ := req.Marshal()
	compressed, _ := compression.Compress(body, compression.Config{Type: compression.TypeSnappy})

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/write", bytes.NewReader(compressed))
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Content-Encoding", "snappy")

	w := httptest.NewRecorder()
	receiver.server.Handler.ServeHTTP(w, httpReq)

	resp := w.Result()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("Status code = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	// Check response headers
	histWritten := resp.Header.Get("X-Prometheus-Remote-Write-Histograms-Written")
	if histWritten != "1" {
		t.Errorf("X-Prometheus-Remote-Write-Histograms-Written = %s, want 1", histWritten)
	}

	// Check that buffer received the request
	requests := buf.getRequests()
	if len(requests) != 1 {
		t.Fatalf("Buffer received %d requests, want 1", len(requests))
	}
	if len(requests[0].Metadata) != 1 {
		t.Errorf("Metadata count = %d, want 1", len(requests[0].Metadata))
	}
}

func TestPRWReceiver_SnappyDecompression(t *testing.T) {
	buf := &mockPRWBuffer{}
	receiver := NewPRW(":0", buf)

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	body, _ := req.Marshal()
	compressed, _ := compression.Compress(body, compression.Config{Type: compression.TypeSnappy})

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/write", bytes.NewReader(compressed))
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Content-Encoding", "snappy")

	w := httptest.NewRecorder()
	receiver.server.Handler.ServeHTTP(w, httpReq)

	if w.Code != http.StatusNoContent {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestPRWReceiver_ZstdDecompression(t *testing.T) {
	buf := &mockPRWBuffer{}
	receiver := NewPRW(":0", buf)

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	body, _ := req.Marshal()
	compressed, _ := compression.Compress(body, compression.Config{Type: compression.TypeZstd})

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/write", bytes.NewReader(compressed))
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Content-Encoding", "zstd")

	w := httptest.NewRecorder()
	receiver.server.Handler.ServeHTTP(w, httpReq)

	if w.Code != http.StatusNoContent {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestPRWReceiver_InvalidContentType(t *testing.T) {
	buf := &mockPRWBuffer{}
	receiver := NewPRW(":0", buf)

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/write", bytes.NewReader([]byte("test")))
	httpReq.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	receiver.server.Handler.ServeHTTP(w, httpReq)

	if w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusUnsupportedMediaType)
	}
}

func TestPRWReceiver_InvalidMethod(t *testing.T) {
	buf := &mockPRWBuffer{}
	receiver := NewPRW(":0", buf)

	httpReq := httptest.NewRequest(http.MethodGet, "/api/v1/write", nil)

	w := httptest.NewRecorder()
	receiver.server.Handler.ServeHTTP(w, httpReq)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestPRWReceiver_EmptyBody(t *testing.T) {
	buf := &mockPRWBuffer{}
	receiver := NewPRW(":0", buf)

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/write", bytes.NewReader([]byte{}))
	httpReq.Header.Set("Content-Type", "application/x-protobuf")

	w := httptest.NewRecorder()
	receiver.server.Handler.ServeHTTP(w, httpReq)

	// Empty protobuf is valid (empty WriteRequest)
	if w.Code != http.StatusNoContent {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestPRWReceiver_MalformedProtobuf(t *testing.T) {
	buf := &mockPRWBuffer{}
	receiver := NewPRW(":0", buf)

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/write", bytes.NewReader([]byte{0xFF, 0xFF, 0xFF}))
	httpReq.Header.Set("Content-Type", "application/x-protobuf")

	w := httptest.NewRecorder()
	receiver.server.Handler.ServeHTTP(w, httpReq)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestPRWReceiver_MaxRequestBodySize(t *testing.T) {
	buf := &mockPRWBuffer{}
	cfg := PRWConfig{
		Addr: ":0",
		Server: PRWServerConfig{
			MaxRequestBodySize: 10, // Very small
		},
	}
	receiver := NewPRWWithConfig(cfg, buf)

	// Create a request larger than the limit
	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "very_long_metric_name_that_exceeds_limit"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}
	body, _ := req.Marshal()

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/write", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/x-protobuf")

	w := httptest.NewRecorder()
	receiver.server.Handler.ServeHTTP(w, httpReq)

	// Body will be truncated, causing unmarshal failure
	if w.Code != http.StatusBadRequest {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestPRWReceiver_ShortEndpoint(t *testing.T) {
	buf := &mockPRWBuffer{}
	receiver := NewPRW(":0", buf)

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	body, _ := req.Marshal()
	compressed, _ := compression.Compress(body, compression.Config{Type: compression.TypeSnappy})

	// Use VictoriaMetrics shorthand endpoint
	httpReq := httptest.NewRequest(http.MethodPost, "/write", bytes.NewReader(compressed))
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Content-Encoding", "snappy")

	w := httptest.NewRecorder()
	receiver.server.Handler.ServeHTTP(w, httpReq)

	if w.Code != http.StatusNoContent {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusNoContent)
	}

	requests := buf.getRequests()
	if len(requests) != 1 {
		t.Fatalf("Buffer received %d requests, want 1", len(requests))
	}
}

func TestPRWReceiver_Timeouts(t *testing.T) {
	buf := &mockPRWBuffer{}
	cfg := PRWConfig{
		Addr: ":0",
		Server: PRWServerConfig{
			ReadTimeout:       100 * time.Millisecond,
			WriteTimeout:      100 * time.Millisecond,
			ReadHeaderTimeout: 100 * time.Millisecond,
		},
	}
	receiver := NewPRWWithConfig(cfg, buf)

	// Verify configuration was applied
	if receiver.server.ReadTimeout != 100*time.Millisecond {
		t.Errorf("ReadTimeout = %v, want %v", receiver.server.ReadTimeout, 100*time.Millisecond)
	}
	if receiver.server.WriteTimeout != 100*time.Millisecond {
		t.Errorf("WriteTimeout = %v, want %v", receiver.server.WriteTimeout, 100*time.Millisecond)
	}
}

func TestPRWReceiver_StartStop(t *testing.T) {
	buf := &mockPRWBuffer{}
	receiver := NewPRW("127.0.0.1:0", buf)

	// Start in background
	go receiver.Start()

	// Give it time to start
	time.Sleep(50 * time.Millisecond)

	// Stop
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := receiver.Stop(ctx); err != nil {
		t.Errorf("Stop() error = %v", err)
	}
}

func TestPRWReceiver_SamplesWrittenHeader(t *testing.T) {
	buf := &mockPRWBuffer{}
	receiver := NewPRW(":0", buf)

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels: []prw.Label{{Name: "__name__", Value: "test1"}},
				Samples: []prw.Sample{
					{Value: 1.0, Timestamp: 1000},
					{Value: 2.0, Timestamp: 2000},
				},
			},
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test2"}},
				Samples: []prw.Sample{{Value: 3.0, Timestamp: 3000}},
			},
		},
	}

	body, _ := req.Marshal()
	compressed, _ := compression.Compress(body, compression.Config{Type: compression.TypeSnappy})

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/write", bytes.NewReader(compressed))
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Content-Encoding", "snappy")

	w := httptest.NewRecorder()
	receiver.server.Handler.ServeHTTP(w, httpReq)

	resp := w.Result()
	samplesWritten := resp.Header.Get("X-Prometheus-Remote-Write-Samples-Written")
	if samplesWritten != "3" {
		t.Errorf("X-Prometheus-Remote-Write-Samples-Written = %s, want 3", samplesWritten)
	}
}

func TestPRWReceiver_NoContentTypeHeader(t *testing.T) {
	buf := &mockPRWBuffer{}
	receiver := NewPRW(":0", buf)

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	body, _ := req.Marshal()

	// No Content-Type header, no compression
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/write", bytes.NewReader(body))

	w := httptest.NewRecorder()
	receiver.server.Handler.ServeHTTP(w, httpReq)

	// Should still work (empty Content-Type is allowed)
	if w.Code != http.StatusNoContent {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestPRWReceiver_InvalidDecompression(t *testing.T) {
	buf := &mockPRWBuffer{}
	receiver := NewPRW(":0", buf)

	// Invalid compressed data
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/write", bytes.NewReader([]byte{0x00, 0x01, 0x02}))
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Content-Encoding", "snappy")

	w := httptest.NewRecorder()
	receiver.server.Handler.ServeHTTP(w, httpReq)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Status code = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestItoa(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{1, "1"},
		{-1, "-1"},
		{123, "123"},
		{-456, "-456"},
		{1000000, "1000000"},
	}

	for _, tt := range tests {
		if got := itoa(tt.n); got != tt.want {
			t.Errorf("itoa(%d) = %s, want %s", tt.n, got, tt.want)
		}
	}
}

func BenchmarkPRWReceiver_HandleWrite(b *testing.B) {
	buf := &mockPRWBuffer{}
	receiver := NewPRW(":0", buf)

	req := &prw.WriteRequest{
		Timeseries: make([]prw.TimeSeries, 100),
	}
	for i := 0; i < 100; i++ {
		req.Timeseries[i] = prw.TimeSeries{
			Labels: []prw.Label{
				{Name: "__name__", Value: "http_requests_total"},
				{Name: "method", Value: "GET"},
				{Name: "status", Value: "200"},
			},
			Samples: []prw.Sample{
				{Value: float64(i), Timestamp: int64(i) * 1000},
			},
		}
	}

	body, _ := req.Marshal()
	compressed, _ := compression.Compress(body, compression.Config{Type: compression.TypeSnappy})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/write", bytes.NewReader(compressed))
		httpReq.Header.Set("Content-Type", "application/x-protobuf")
		httpReq.Header.Set("Content-Encoding", "snappy")

		w := httptest.NewRecorder()
		receiver.server.Handler.ServeHTTP(w, httpReq)

		// Read and discard body to reuse buffer
		io.Copy(io.Discard, w.Body)
	}
}

func BenchmarkPRWReceiver_HandleWrite_LargePayload(b *testing.B) {
	buf := &mockPRWBuffer{}
	receiver := NewPRW(":0", buf)

	req := &prw.WriteRequest{
		Timeseries: make([]prw.TimeSeries, 1000),
	}
	for i := 0; i < 1000; i++ {
		req.Timeseries[i] = prw.TimeSeries{
			Labels: []prw.Label{
				{Name: "__name__", Value: "http_requests_total"},
				{Name: "method", Value: "GET"},
				{Name: "status", Value: "200"},
				{Name: "instance", Value: "localhost:8080"},
				{Name: "job", Value: "prometheus"},
			},
			Samples: []prw.Sample{
				{Value: float64(i), Timestamp: int64(i) * 1000},
				{Value: float64(i + 1), Timestamp: int64(i+1) * 1000},
			},
		}
	}

	body, _ := req.Marshal()
	compressed, _ := compression.Compress(body, compression.Config{Type: compression.TypeSnappy})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/write", bytes.NewReader(compressed))
		httpReq.Header.Set("Content-Type", "application/x-protobuf")
		httpReq.Header.Set("Content-Encoding", "snappy")

		w := httptest.NewRecorder()
		receiver.server.Handler.ServeHTTP(w, httpReq)
	}
}
