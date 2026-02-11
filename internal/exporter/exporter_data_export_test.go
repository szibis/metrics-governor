package exporter

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/compression"
	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// =============================================================================
// OTLPExporter.ExportData — gRPC fallback path
// =============================================================================

func TestExportData_GRPCFallback_Success(t *testing.T) {
	// gRPC path: data is unmarshalled and exported via gRPC
	mockServer := &mockMetricsServer{}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	server := grpc.NewServer()
	colmetricspb.RegisterMetricsServiceServer(server, mockServer)
	go server.Serve(lis)
	defer server.Stop()

	cfg := Config{
		Endpoint: lis.Addr().String(),
		Insecure: true,
		Timeout:  5 * time.Second,
	}

	exp, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer exp.Close()

	// Create a valid serialized request
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
	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("proto.Marshal: %v", err)
	}

	err = exp.ExportData(context.Background(), data)
	if err != nil {
		t.Fatalf("ExportData() error = %v", err)
	}

	if len(mockServer.received) != 1 {
		t.Errorf("expected 1 request, got %d", len(mockServer.received))
	}
}

func TestExportData_GRPCFallback_InvalidData(t *testing.T) {
	// gRPC path with invalid proto bytes should return unmarshal error
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer lis.Close()

	server := grpc.NewServer()
	colmetricspb.RegisterMetricsServiceServer(server, &mockMetricsServer{})
	go server.Serve(lis)
	defer server.Stop()

	cfg := Config{
		Endpoint: lis.Addr().String(),
		Insecure: true,
		Timeout:  5 * time.Second,
	}

	exp, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer exp.Close()

	// Invalid proto bytes
	err = exp.ExportData(context.Background(), []byte{0xFF, 0xFE, 0xFD})
	if err == nil {
		t.Fatal("expected unmarshal error for invalid data")
	}
}

// =============================================================================
// OTLPExporter.ExportData — HTTP path
// =============================================================================

func TestExportData_HTTPPath_Success(t *testing.T) {
	var receivedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := Config{
		Endpoint: server.URL,
		Protocol: ProtocolHTTP,
		Insecure: true,
		Timeout:  5 * time.Second,
	}

	exp, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer exp.Close()

	// Create a valid serialized request
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
	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("proto.Marshal: %v", err)
	}

	err = exp.ExportData(context.Background(), data)
	if err != nil {
		t.Fatalf("ExportData() error = %v", err)
	}

	if len(receivedBody) == 0 {
		t.Error("expected non-empty received body")
	}
}

func TestExportData_HTTPPath_WithCompression(t *testing.T) {
	var receivedEncoding string
	var receivedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedEncoding = r.Header.Get("Content-Encoding")
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := Config{
		Endpoint: server.URL,
		Protocol: ProtocolHTTP,
		Insecure: true,
		Timeout:  5 * time.Second,
		Compression: compression.Config{
			Type: compression.TypeGzip,
		},
	}

	exp, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer exp.Close()

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
	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("proto.Marshal: %v", err)
	}

	err = exp.ExportData(context.Background(), data)
	if err != nil {
		t.Fatalf("ExportData() error = %v", err)
	}

	if receivedEncoding != "gzip" {
		t.Errorf("expected Content-Encoding=gzip, got %q", receivedEncoding)
	}
	if len(receivedBody) == 0 {
		t.Error("expected non-empty received body")
	}
}

func TestExportData_HTTPPath_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	cfg := Config{
		Endpoint: server.URL,
		Protocol: ProtocolHTTP,
		Insecure: true,
		Timeout:  5 * time.Second,
	}

	exp, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer exp.Close()

	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{{Name: "m"}}}}},
		},
	}
	data, _ := proto.Marshal(req)

	err = exp.ExportData(context.Background(), data)
	if err == nil {
		t.Fatal("expected error for 500 response")
	}

	var exportErr *ExportError
	if errors.As(err, &exportErr) {
		if exportErr.StatusCode != 500 {
			t.Errorf("expected status 500, got %d", exportErr.StatusCode)
		}
	}
}

func TestExportData_HTTPPath_ConnectionError(t *testing.T) {
	exp := &OTLPExporter{
		protocol:     ProtocolHTTP,
		timeout:      1 * time.Second,
		httpClient:   &http.Client{Timeout: 1 * time.Second},
		httpEndpoint: "http://127.0.0.1:1/v1/metrics",
	}

	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{{Name: "m"}}}}},
		},
	}
	data, _ := proto.Marshal(req)

	err := exp.ExportData(context.Background(), data)
	if err == nil {
		t.Fatal("expected error for connection failure")
	}
}

// =============================================================================
// OTLPExporter.CompressData
// =============================================================================

func TestCompressData_NoCompression(t *testing.T) {
	exp := &OTLPExporter{
		protocol: ProtocolHTTP,
		compression: compression.Config{
			Type: compression.TypeNone,
		},
	}

	data := []byte("test data for compression")
	compressed, encoding, err := exp.CompressData(data)
	if err != nil {
		t.Fatalf("CompressData() error = %v", err)
	}
	if encoding != "" {
		t.Errorf("expected empty encoding, got %q", encoding)
	}
	if string(compressed) != string(data) {
		t.Error("expected data unchanged when compression is none")
	}
}

func TestCompressData_EmptyType(t *testing.T) {
	exp := &OTLPExporter{
		protocol:    ProtocolHTTP,
		compression: compression.Config{},
	}

	data := []byte("test data for compression")
	compressed, encoding, err := exp.CompressData(data)
	if err != nil {
		t.Fatalf("CompressData() error = %v", err)
	}
	if encoding != "" {
		t.Errorf("expected empty encoding, got %q", encoding)
	}
	if string(compressed) != string(data) {
		t.Error("expected data unchanged when compression type is empty")
	}
}

func TestCompressData_Gzip(t *testing.T) {
	exp := &OTLPExporter{
		protocol: ProtocolHTTP,
		compression: compression.Config{
			Type:  compression.TypeGzip,
			Level: compression.LevelDefault,
		},
	}

	data := []byte("test data for gzip compression, repeating pattern repeating pattern repeating pattern")
	compressed, encoding, err := exp.CompressData(data)
	if err != nil {
		t.Fatalf("CompressData() error = %v", err)
	}
	if encoding != "gzip" {
		t.Errorf("expected encoding=gzip, got %q", encoding)
	}
	// Compressed data should be different from original
	if string(compressed) == string(data) {
		t.Error("expected compressed data to differ from original")
	}
}

func TestCompressData_Snappy(t *testing.T) {
	exp := &OTLPExporter{
		protocol: ProtocolHTTP,
		compression: compression.Config{
			Type: compression.TypeSnappy,
		},
	}

	data := []byte("test data for snappy compression, repeating text repeating text")
	compressed, encoding, err := exp.CompressData(data)
	if err != nil {
		t.Fatalf("CompressData() error = %v", err)
	}
	if encoding != "snappy" {
		t.Errorf("expected encoding=snappy, got %q", encoding)
	}
	if len(compressed) == 0 {
		t.Error("expected non-empty compressed data")
	}
}

func TestCompressData_Zstd(t *testing.T) {
	exp := &OTLPExporter{
		protocol: ProtocolHTTP,
		compression: compression.Config{
			Type:  compression.TypeZstd,
			Level: compression.LevelDefault,
		},
	}

	data := []byte("test data for zstd compression, repeating text repeating text")
	compressed, encoding, err := exp.CompressData(data)
	if err != nil {
		t.Fatalf("CompressData() error = %v", err)
	}
	if encoding != "zstd" {
		t.Errorf("expected encoding=zstd, got %q", encoding)
	}
	if len(compressed) == 0 {
		t.Error("expected non-empty compressed data")
	}
}

// =============================================================================
// OTLPExporter.SendCompressed
// =============================================================================

func TestSendCompressed_Success(t *testing.T) {
	var receivedEncoding string
	var receivedContentType string
	var receivedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedEncoding = r.Header.Get("Content-Encoding")
		receivedContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	exp := &OTLPExporter{
		protocol:     ProtocolHTTP,
		timeout:      5 * time.Second,
		httpClient:   server.Client(),
		httpEndpoint: server.URL + "/v1/metrics",
	}

	compressedData := []byte("compressed-data-bytes")
	err := exp.SendCompressed(context.Background(), compressedData, "gzip", 100)
	if err != nil {
		t.Fatalf("SendCompressed() error = %v", err)
	}

	if receivedContentType != "application/x-protobuf" {
		t.Errorf("Content-Type = %q, want application/x-protobuf", receivedContentType)
	}
	if receivedEncoding != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", receivedEncoding)
	}
	if string(receivedBody) != "compressed-data-bytes" {
		t.Errorf("body = %q, want compressed-data-bytes", string(receivedBody))
	}
}

func TestSendCompressed_NoEncoding(t *testing.T) {
	var receivedEncoding string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedEncoding = r.Header.Get("Content-Encoding")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	exp := &OTLPExporter{
		protocol:     ProtocolHTTP,
		timeout:      5 * time.Second,
		httpClient:   server.Client(),
		httpEndpoint: server.URL + "/v1/metrics",
	}

	err := exp.SendCompressed(context.Background(), []byte("data"), "", 4)
	if err != nil {
		t.Fatalf("SendCompressed() error = %v", err)
	}

	if receivedEncoding != "" {
		t.Errorf("Content-Encoding = %q, want empty", receivedEncoding)
	}
}

func TestSendCompressed_GRPCNotSupported(t *testing.T) {
	exp := &OTLPExporter{
		protocol: ProtocolGRPC,
	}

	err := exp.SendCompressed(context.Background(), []byte("data"), "gzip", 4)
	if err == nil {
		t.Fatal("expected error for gRPC protocol")
	}
}

func TestSendCompressed_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte("service unavailable"))
	}))
	defer server.Close()

	exp := &OTLPExporter{
		protocol:     ProtocolHTTP,
		timeout:      5 * time.Second,
		httpClient:   server.Client(),
		httpEndpoint: server.URL + "/v1/metrics",
	}

	err := exp.SendCompressed(context.Background(), []byte("data"), "gzip", 4)
	if err == nil {
		t.Fatal("expected error for 503 response")
	}

	var exportErr *ExportError
	if errors.As(err, &exportErr) {
		if exportErr.StatusCode != 503 {
			t.Errorf("expected status 503, got %d", exportErr.StatusCode)
		}
	}
}

func TestSendCompressed_ConnectionError(t *testing.T) {
	exp := &OTLPExporter{
		protocol:     ProtocolHTTP,
		timeout:      1 * time.Second,
		httpClient:   &http.Client{Timeout: 1 * time.Second},
		httpEndpoint: "http://127.0.0.1:1/v1/metrics",
	}

	err := exp.SendCompressed(context.Background(), []byte("data"), "gzip", 4)
	if err == nil {
		t.Fatal("expected error for connection failure")
	}
}

// =============================================================================
// ExportHTTPData — additional error paths
// =============================================================================

func TestExportHTTPData_ClientError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("bad request"))
	}))
	defer server.Close()

	exp := &OTLPExporter{
		protocol:     ProtocolHTTP,
		timeout:      5 * time.Second,
		httpClient:   server.Client(),
		httpEndpoint: server.URL + "/v1/metrics",
	}

	data := []byte("some proto bytes")
	err := exp.exportHTTPData(context.Background(), data)
	if err == nil {
		t.Fatal("expected error for 400 response")
	}

	var exportErr *ExportError
	if errors.As(err, &exportErr) {
		if exportErr.StatusCode != 400 {
			t.Errorf("expected status 400, got %d", exportErr.StatusCode)
		}
	}
}

func TestExportHTTPData_SuccessNoCompression(t *testing.T) {
	var receivedSize int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedSize = len(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	exp := &OTLPExporter{
		protocol:     ProtocolHTTP,
		timeout:      5 * time.Second,
		httpClient:   server.Client(),
		httpEndpoint: server.URL + "/v1/metrics",
	}

	data := []byte("uncompressed proto data")
	err := exp.exportHTTPData(context.Background(), data)
	if err != nil {
		t.Fatalf("exportHTTPData() error = %v", err)
	}

	if receivedSize != len(data) {
		t.Errorf("received %d bytes, expected %d", receivedSize, len(data))
	}
}

func TestExportHTTPData_SuccessWithCompression(t *testing.T) {
	var receivedEncoding string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedEncoding = r.Header.Get("Content-Encoding")
		io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	exp := &OTLPExporter{
		protocol:     ProtocolHTTP,
		timeout:      5 * time.Second,
		httpClient:   server.Client(),
		httpEndpoint: server.URL + "/v1/metrics",
		compression: compression.Config{
			Type:  compression.TypeGzip,
			Level: compression.LevelDefault,
		},
	}

	data := []byte("proto data for compression test, repeating data repeating data")
	err := exp.exportHTTPData(context.Background(), data)
	if err != nil {
		t.Fatalf("exportHTTPData() error = %v", err)
	}

	if receivedEncoding != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", receivedEncoding)
	}
}

// =============================================================================
// Concurrent ExportData calls
// =============================================================================

func TestExportData_HTTPPath_Concurrent(t *testing.T) {
	var requestCount int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := Config{
		Endpoint: server.URL,
		Protocol: ProtocolHTTP,
		Insecure: true,
		Timeout:  5 * time.Second,
	}

	exp, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer exp.Close()

	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{ScopeMetrics: []*metricspb.ScopeMetrics{{Metrics: []*metricspb.Metric{{Name: "m"}}}}},
		},
	}
	data, _ := proto.Marshal(req)

	const goroutines = 10
	done := make(chan struct{}, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 10; j++ {
				_ = exp.ExportData(context.Background(), data)
			}
		}()
	}
	for i := 0; i < goroutines; i++ {
		<-done
	}

	expected := int64(goroutines * 10)
	got := atomic.LoadInt64(&requestCount)
	if got != expected {
		t.Errorf("request count = %d, want %d", got, expected)
	}
}
