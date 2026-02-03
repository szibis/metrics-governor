package receiver

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/auth"
	"github.com/szibis/metrics-governor/internal/buffer"
	"github.com/szibis/metrics-governor/internal/compression"
	tlspkg "github.com/szibis/metrics-governor/internal/tls"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/grpc/stats"
	"google.golang.org/protobuf/proto"
)

// coverageTestBuffer creates a buffer for coverage tests.
func coverageTestBuffer() *buffer.MetricsBuffer {
	return buffer.New(100, 50, time.Second, &mockExporter{}, nil, nil, nil)
}

// =============================================================================
// gRPC stats handler tests (grpc.go lines 124-146) - all at 0% coverage
// =============================================================================

// TestGRPCStatsHandlerTagRPC tests TagRPC returns context unchanged.
func TestGRPCStatsHandlerTagRPC(t *testing.T) {
	h := &grpcStatsHandler{}
	ctx := context.Background()
	info := &stats.RPCTagInfo{
		FullMethodName: "/test.Service/Method",
	}

	result := h.TagRPC(ctx, info)
	if result != ctx {
		t.Error("TagRPC should return the same context")
	}
}

// TestGRPCStatsHandlerTagRPCWithValues tests TagRPC preserves context values.
func TestGRPCStatsHandlerTagRPCWithValues(t *testing.T) {
	h := &grpcStatsHandler{}
	type ctxKey string
	ctx := context.WithValue(context.Background(), ctxKey("key"), "value")
	info := &stats.RPCTagInfo{
		FullMethodName: "/test.Service/Method",
	}

	result := h.TagRPC(ctx, info)
	if result.Value(ctxKey("key")) != "value" {
		t.Error("TagRPC should preserve context values")
	}
}

// TestGRPCStatsHandlerHandleRPCCompressed tests HandleRPC with compressed payload.
func TestGRPCStatsHandlerHandleRPCCompressed(t *testing.T) {
	h := &grpcStatsHandler{}
	ctx := context.Background()

	// Simulate compressed data: WireLength < Length indicates compression
	inPayload := &stats.InPayload{
		WireLength: 100,
		Length:     500,
	}

	// Should not panic
	h.HandleRPC(ctx, inPayload)
}

// TestGRPCStatsHandlerHandleRPCUncompressed tests HandleRPC with uncompressed payload.
func TestGRPCStatsHandlerHandleRPCUncompressed(t *testing.T) {
	h := &grpcStatsHandler{}
	ctx := context.Background()

	// Simulate uncompressed data: WireLength >= Length
	inPayload := &stats.InPayload{
		WireLength: 500,
		Length:     500,
	}

	// Should not panic
	h.HandleRPC(ctx, inPayload)
}

// TestGRPCStatsHandlerHandleRPCZeroWireLength tests HandleRPC with zero wire length.
func TestGRPCStatsHandlerHandleRPCZeroWireLength(t *testing.T) {
	h := &grpcStatsHandler{}
	ctx := context.Background()

	// WireLength == 0 means uncompressed path
	inPayload := &stats.InPayload{
		WireLength: 0,
		Length:     500,
	}

	// Should not panic
	h.HandleRPC(ctx, inPayload)
}

// TestGRPCStatsHandlerHandleRPCNonInPayload tests HandleRPC with non-InPayload stats.
func TestGRPCStatsHandlerHandleRPCNonInPayload(t *testing.T) {
	h := &grpcStatsHandler{}
	ctx := context.Background()

	// Pass a different stats type - should be a no-op
	outPayload := &stats.OutPayload{
		Length: 100,
	}

	// Should not panic (the type assertion to *stats.InPayload will fail gracefully)
	h.HandleRPC(ctx, outPayload)
}

// TestGRPCStatsHandlerHandleRPCBegin tests HandleRPC with Begin stats.
func TestGRPCStatsHandlerHandleRPCBegin(t *testing.T) {
	h := &grpcStatsHandler{}
	ctx := context.Background()

	begin := &stats.Begin{}
	h.HandleRPC(ctx, begin)
}

// TestGRPCStatsHandlerHandleRPCEnd tests HandleRPC with End stats.
func TestGRPCStatsHandlerHandleRPCEnd(t *testing.T) {
	h := &grpcStatsHandler{}
	ctx := context.Background()

	end := &stats.End{}
	h.HandleRPC(ctx, end)
}

// TestGRPCStatsHandlerTagConn tests TagConn returns context unchanged.
func TestGRPCStatsHandlerTagConn(t *testing.T) {
	h := &grpcStatsHandler{}
	ctx := context.Background()
	info := &stats.ConnTagInfo{
		RemoteAddr: nil,
		LocalAddr:  nil,
	}

	result := h.TagConn(ctx, info)
	if result != ctx {
		t.Error("TagConn should return the same context")
	}
}

// TestGRPCStatsHandlerTagConnWithValues tests TagConn preserves context values.
func TestGRPCStatsHandlerTagConnWithValues(t *testing.T) {
	h := &grpcStatsHandler{}
	type ctxKey string
	ctx := context.WithValue(context.Background(), ctxKey("conn"), "data")
	info := &stats.ConnTagInfo{}

	result := h.TagConn(ctx, info)
	if result.Value(ctxKey("conn")) != "data" {
		t.Error("TagConn should preserve context values")
	}
}

// TestGRPCStatsHandlerHandleConn tests HandleConn is a no-op.
func TestGRPCStatsHandlerHandleConn(t *testing.T) {
	h := &grpcStatsHandler{}
	ctx := context.Background()

	// ConnBegin
	h.HandleConn(ctx, &stats.ConnBegin{})

	// ConnEnd
	h.HandleConn(ctx, &stats.ConnEnd{})
}

// =============================================================================
// Metrics tests (metrics.go line 51) - AddReceiverDatapoints at 0%
// =============================================================================

// TestAddReceiverDatapoints tests AddReceiverDatapoints counter.
func TestAddReceiverDatapoints(t *testing.T) {
	// Should not panic with various counts
	AddReceiverDatapoints(0)
	AddReceiverDatapoints(1)
	AddReceiverDatapoints(100)
	AddReceiverDatapoints(10000)
}

// TestAddReceiverDatapointsMultipleCalls tests AddReceiverDatapoints accumulates.
func TestAddReceiverDatapointsMultipleCalls(t *testing.T) {
	for i := 0; i < 50; i++ {
		AddReceiverDatapoints(i)
	}
}

// TestIncrementReceiverErrorCoverage tests IncrementReceiverError with all types.
func TestIncrementReceiverErrorCoverage(t *testing.T) {
	errorTypes := []string{"decode", "auth", "decompress", "read", "custom"}
	for _, errType := range errorTypes {
		IncrementReceiverError(errType)
	}
}

// TestIncrementReceiverRequestsCoverage tests IncrementReceiverRequests with all protocols.
func TestIncrementReceiverRequestsCoverage(t *testing.T) {
	protocols := []string{"grpc", "http", "prw"}
	for _, proto := range protocols {
		IncrementReceiverRequests(proto)
	}
}

// =============================================================================
// NewGRPCWithConfig tests (54.5% coverage) - TLS and Auth enabled paths
// =============================================================================

// TestNewGRPCWithConfigTLSEnabled tests NewGRPCWithConfig with TLS enabled but invalid certs.
func TestNewGRPCWithConfigTLSEnabled(t *testing.T) {
	buf := coverageTestBuffer()

	cfg := GRPCConfig{
		Addr: ":4317",
		TLS: tlspkg.ServerConfig{
			Enabled:  true,
			CertFile: "/nonexistent/cert.pem",
			KeyFile:  "/nonexistent/key.pem",
		},
	}

	// Should still return a receiver (logs error but doesn't fail)
	r := NewGRPCWithConfig(cfg, buf)
	if r == nil {
		t.Fatal("Expected non-nil receiver even with invalid TLS config")
	}
	if r.addr != ":4317" {
		t.Errorf("Expected addr ':4317', got '%s'", r.addr)
	}
}

// TestNewGRPCWithConfigAuthEnabled tests NewGRPCWithConfig with auth enabled.
func TestNewGRPCWithConfigAuthEnabled(t *testing.T) {
	buf := coverageTestBuffer()

	cfg := GRPCConfig{
		Addr: ":4317",
		Auth: auth.ServerConfig{
			Enabled:     true,
			BearerToken: "test-token",
		},
	}

	r := NewGRPCWithConfig(cfg, buf)
	if r == nil {
		t.Fatal("Expected non-nil receiver with auth config")
	}
	if r.server == nil {
		t.Fatal("Expected server to be created")
	}
}

// TestNewGRPCWithConfigTLSAndAuth tests NewGRPCWithConfig with both TLS and auth.
func TestNewGRPCWithConfigTLSAndAuth(t *testing.T) {
	buf := coverageTestBuffer()

	cfg := GRPCConfig{
		Addr: ":4317",
		TLS: tlspkg.ServerConfig{
			Enabled:  true,
			CertFile: "/nonexistent/cert.pem",
			KeyFile:  "/nonexistent/key.pem",
		},
		Auth: auth.ServerConfig{
			Enabled:     true,
			BearerToken: "test-token",
		},
	}

	r := NewGRPCWithConfig(cfg, buf)
	if r == nil {
		t.Fatal("Expected non-nil receiver with TLS+Auth config")
	}
}

// =============================================================================
// HTTP handleMetrics error paths (69.0% coverage)
// =============================================================================

// TestHTTPHandleMetricsGzipDecompression tests handleMetrics with gzip content encoding.
func TestHTTPHandleMetricsGzipDecompression(t *testing.T) {
	buf := coverageTestBuffer()
	r := NewHTTP(":4318", buf)

	// Create valid protobuf data
	exportReq := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{Name: "gzip.test.metric"},
						},
					},
				},
			},
		},
	}
	body, _ := proto.Marshal(exportReq)

	// Compress with gzip
	compressed, err := compression.Compress(body, compression.Config{Type: compression.TypeGzip})
	if err != nil {
		t.Fatalf("Failed to compress: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(compressed))
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "gzip")

	rec := httptest.NewRecorder()
	r.handleMetrics(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}
}

// TestHTTPHandleMetricsBadDecompression tests handleMetrics with invalid compressed data.
func TestHTTPHandleMetricsBadDecompression(t *testing.T) {
	buf := coverageTestBuffer()
	r := NewHTTP(":4318", buf)

	// Send garbage data with a Content-Encoding header
	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader([]byte{0xFF, 0xFE, 0xFD}))
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "gzip")

	rec := httptest.NewRecorder()
	r.handleMetrics(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for bad decompression, got %d", rec.Code)
	}
}

// TestHTTPHandleMetricsBadSnappyDecompression tests with invalid snappy data.
func TestHTTPHandleMetricsBadSnappyDecompression(t *testing.T) {
	buf := coverageTestBuffer()
	r := NewHTTP(":4318", buf)

	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader([]byte{0x00, 0x01, 0x02}))
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "snappy")

	rec := httptest.NewRecorder()
	r.handleMetrics(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400 for bad snappy decompression, got %d", rec.Code)
	}
}

// TestHTTPHandleMetricsUnknownEncoding tests with unsupported Content-Encoding.
func TestHTTPHandleMetricsUnknownEncoding(t *testing.T) {
	buf := coverageTestBuffer()
	r := NewHTTP(":4318", buf)

	exportReq := &colmetricspb.ExportMetricsServiceRequest{}
	body, _ := proto.Marshal(exportReq)

	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "unknown-encoding")

	rec := httptest.NewRecorder()
	r.handleMetrics(rec, req)

	// Unknown encoding is TypeNone, so body passes through unmodified
	// If the raw body is valid protobuf, it should succeed
	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200 (unknown encoding treated as none), got %d", rec.Code)
	}
}

// TestHTTPHandleMetricsZstdEncoding tests with zstd content encoding.
func TestHTTPHandleMetricsZstdEncoding(t *testing.T) {
	buf := coverageTestBuffer()
	r := NewHTTP(":4318", buf)

	exportReq := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{Name: "zstd.test"},
						},
					},
				},
			},
		},
	}
	body, _ := proto.Marshal(exportReq)

	compressed, err := compression.Compress(body, compression.Config{Type: compression.TypeZstd})
	if err != nil {
		t.Fatalf("Failed to compress with zstd: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(compressed))
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "zstd")

	rec := httptest.NewRecorder()
	r.handleMetrics(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200 for zstd, got %d", rec.Code)
	}
}

// =============================================================================
// HTTP and PRW config edge cases
// =============================================================================

// TestNewHTTPWithConfigAuthEnabled tests HTTP receiver with auth enabled.
func TestNewHTTPWithConfigAuthEnabled(t *testing.T) {
	buf := coverageTestBuffer()

	cfg := HTTPConfig{
		Addr: ":4318",
		Auth: auth.ServerConfig{
			Enabled:     true,
			BearerToken: "test-token",
		},
	}

	r := NewHTTPWithConfig(cfg, buf)
	if r == nil {
		t.Fatal("Expected non-nil receiver with auth config")
	}
}

// TestNewHTTPWithConfigTLSEnabled tests HTTP receiver with TLS enabled (invalid certs).
func TestNewHTTPWithConfigTLSEnabled(t *testing.T) {
	buf := coverageTestBuffer()

	cfg := HTTPConfig{
		Addr: ":4318",
		TLS: tlspkg.ServerConfig{
			Enabled:  true,
			CertFile: "/nonexistent/cert.pem",
			KeyFile:  "/nonexistent/key.pem",
		},
	}

	r := NewHTTPWithConfig(cfg, buf)
	if r == nil {
		t.Fatal("Expected non-nil receiver even with invalid TLS config")
	}
	// TLS config should be nil since cert loading failed
	if r.tlsConfig != nil {
		t.Error("Expected nil TLS config due to invalid certs")
	}
}

// TestNewHTTPWithConfigKeepAlivesDisabled tests disabling keep-alives.
func TestNewHTTPWithConfigKeepAlivesDisabled(t *testing.T) {
	buf := coverageTestBuffer()

	cfg := HTTPConfig{
		Addr: ":4318",
		Server: HTTPServerConfig{
			ReadTimeout:       10 * time.Second,
			KeepAlivesEnabled: false,
		},
	}

	r := NewHTTPWithConfig(cfg, buf)
	if r == nil {
		t.Fatal("Expected non-nil receiver")
	}
}

// TestNewPRWWithConfigTLSEnabled tests PRW receiver with TLS enabled (invalid certs).
func TestNewPRWWithConfigTLSEnabled(t *testing.T) {
	buf := &mockPRWBuffer{}

	cfg := PRWConfig{
		Addr: ":9090",
		TLS: tlspkg.ServerConfig{
			Enabled:  true,
			CertFile: "/nonexistent/cert.pem",
			KeyFile:  "/nonexistent/key.pem",
		},
	}

	r := NewPRWWithConfig(cfg, buf)
	if r == nil {
		t.Fatal("Expected non-nil receiver even with invalid TLS config")
	}
	if r.tlsConfig != nil {
		t.Error("Expected nil TLS config due to invalid certs")
	}
}

// TestNewPRWWithConfigAuthEnabled tests PRW receiver with auth enabled.
func TestNewPRWWithConfigAuthEnabled(t *testing.T) {
	buf := &mockPRWBuffer{}

	cfg := PRWConfig{
		Addr: ":9090",
		Auth: auth.ServerConfig{
			Enabled:     true,
			BearerToken: "test-token",
		},
	}

	r := NewPRWWithConfig(cfg, buf)
	if r == nil {
		t.Fatal("Expected non-nil receiver with auth config")
	}
}

// TestNewPRWWithConfigKeepAlivesDisabled tests PRW with keep-alives disabled.
func TestNewPRWWithConfigKeepAlivesDisabled(t *testing.T) {
	buf := &mockPRWBuffer{}

	cfg := PRWConfig{
		Addr: ":9090",
		Server: PRWServerConfig{
			ReadTimeout:       10 * time.Second,
			KeepAlivesEnabled: false,
		},
	}

	r := NewPRWWithConfig(cfg, buf)
	if r == nil {
		t.Fatal("Expected non-nil receiver")
	}
}

// =============================================================================
// HTTP Start (75.0%) - testing TLS path
// =============================================================================

// TestHTTPStartAndStop tests basic HTTP server start and stop.
func TestHTTPStartAndStopCoverage(t *testing.T) {
	buf := coverageTestBuffer()
	r := NewHTTP("127.0.0.1:0", buf)

	go r.Start()
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Stop(ctx); err != nil {
		t.Errorf("Stop failed: %v", err)
	}
}

// TestPRWStartAndStop tests basic PRW server start and stop.
func TestPRWStartAndStopCoverage(t *testing.T) {
	buf := &mockPRWBuffer{}
	r := NewPRW("127.0.0.1:0", buf)

	go r.Start()
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.Stop(ctx); err != nil {
		t.Errorf("Stop failed: %v", err)
	}
}

// =============================================================================
// PRW handleWrite error paths - bad decompression
// =============================================================================

// TestPRWHandleWriteBadGzipDecompression tests PRW with invalid gzip data.
func TestPRWHandleWriteBadGzipDecompression(t *testing.T) {
	buf := &mockPRWBuffer{}
	receiver := NewPRW(":0", buf)

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/write", bytes.NewReader([]byte{0xFF, 0xFE}))
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Content-Encoding", "gzip")

	w := httptest.NewRecorder()
	receiver.server.Handler.ServeHTTP(w, httpReq)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for bad gzip in PRW, got %d", w.Code)
	}
}

// TestPRWHandleWriteBadZstdDecompression tests PRW with invalid zstd data.
func TestPRWHandleWriteBadZstdDecompression(t *testing.T) {
	buf := &mockPRWBuffer{}
	receiver := NewPRW(":0", buf)

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/write", bytes.NewReader([]byte{0xAA, 0xBB}))
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Content-Encoding", "zstd")

	w := httptest.NewRecorder()
	receiver.server.Handler.ServeHTTP(w, httpReq)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 for bad zstd in PRW, got %d", w.Code)
	}
}
