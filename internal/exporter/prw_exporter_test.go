package exporter

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/slawomirskowron/metrics-governor/internal/compression"
	"github.com/slawomirskowron/metrics-governor/internal/prw"
)

func TestNewPRW_DefaultEndpoint(t *testing.T) {
	exp, err := NewPRW(context.Background(), PRWExporterConfig{})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	if exp.endpoint != "http://localhost:9090/api/v1/write" {
		t.Errorf("Default endpoint = %s, want http://localhost:9090/api/v1/write", exp.endpoint)
	}
}

func TestNewPRW_EndpointURLParsing(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     string
	}{
		{"with_scheme_and_path", "http://example.com/api/v1/write", "http://example.com/api/v1/write"},
		{"with_scheme_no_path", "http://example.com", "http://example.com/api/v1/write"},
		{"no_scheme_no_path", "example.com:9090", "http://example.com:9090/api/v1/write"},
		{"full_url", "https://metrics.example.com:8443/api/v1/write", "https://metrics.example.com:8443/api/v1/write"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp, err := NewPRW(context.Background(), PRWExporterConfig{
				Endpoint: tt.endpoint,
			})
			if err != nil {
				t.Fatalf("NewPRW() error = %v", err)
			}
			defer exp.Close()

			if exp.endpoint != tt.want {
				t.Errorf("endpoint = %s, want %s", exp.endpoint, tt.want)
			}
		})
	}
}

func TestNewPRW_VMMode_ShortEndpoint(t *testing.T) {
	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: "http://victoriametrics:8428",
		VMMode:   true,
		VMOptions: VMRemoteWriteOptions{
			UseShortEndpoint: true,
		},
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	if exp.endpoint != "http://victoriametrics:8428/write" {
		t.Errorf("VM short endpoint = %s, want http://victoriametrics:8428/write", exp.endpoint)
	}
}

func TestNewPRW_CompressionTypes(t *testing.T) {
	tests := []struct {
		name        string
		vmMode      bool
		compression string
		want        compression.Type
	}{
		{"default_snappy", false, "", compression.TypeSnappy},
		{"vm_snappy", true, "snappy", compression.TypeSnappy},
		{"vm_zstd", true, "zstd", compression.TypeZstd},
		{"vm_none", true, "none", compression.TypeSnappy},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exp, err := NewPRW(context.Background(), PRWExporterConfig{
				VMMode: tt.vmMode,
				VMOptions: VMRemoteWriteOptions{
					Compression: tt.compression,
				},
			})
			if err != nil {
				t.Fatalf("NewPRW() error = %v", err)
			}
			defer exp.Close()

			if exp.compression != tt.want {
				t.Errorf("compression = %v, want %v", exp.compression, tt.want)
			}
		})
	}
}

func TestPRWExporter_Export_Success(t *testing.T) {
	var receivedBody []byte
	var receivedHeaders http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
		Timeout:  10 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	err = exp.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	// Check Content-Type header
	if ct := receivedHeaders.Get("Content-Type"); ct != "application/x-protobuf" {
		t.Errorf("Content-Type = %s, want application/x-protobuf", ct)
	}

	// Check Content-Encoding header
	if ce := receivedHeaders.Get("Content-Encoding"); ce != "snappy" {
		t.Errorf("Content-Encoding = %s, want snappy", ce)
	}

	// Check version header
	if v := receivedHeaders.Get("X-Prometheus-Remote-Write-Version"); v != "0.1.0" {
		t.Errorf("X-Prometheus-Remote-Write-Version = %s, want 0.1.0", v)
	}

	// Verify body is not empty (compressed data)
	if len(receivedBody) == 0 {
		t.Error("Received body is empty")
	}
}

func TestPRWExporter_Export_PRW2(t *testing.T) {
	var receivedHeaders http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	// PRW 2.0 request with histograms
	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels: []prw.Label{{Name: "__name__", Value: "test_histogram"}},
				Histograms: []prw.Histogram{
					{Count: 100, Sum: 50.5, Timestamp: 1000},
				},
			},
		},
	}

	err = exp.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	// Check version header for PRW 2.0
	if v := receivedHeaders.Get("X-Prometheus-Remote-Write-Version"); v != "2.0.0" {
		t.Errorf("X-Prometheus-Remote-Write-Version = %s, want 2.0.0", v)
	}
}

func TestPRWExporter_Export_ZstdCompression(t *testing.T) {
	var receivedBody []byte
	var receivedHeaders http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
		VMMode:   true,
		VMOptions: VMRemoteWriteOptions{
			Compression: "zstd",
		},
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	err = exp.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	// Check Content-Encoding header
	if ce := receivedHeaders.Get("Content-Encoding"); ce != "zstd" {
		t.Errorf("Content-Encoding = %s, want zstd", ce)
	}

	// Decompress and verify
	decompressed, err := compression.Decompress(receivedBody, compression.TypeZstd)
	if err != nil {
		t.Fatalf("Failed to decompress zstd body: %v", err)
	}

	// Unmarshal and verify
	result := &prw.WriteRequest{}
	if err := result.Unmarshal(decompressed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if len(result.Timeseries) != 1 {
		t.Errorf("Timeseries count = %d, want 1", len(result.Timeseries))
	}
}

func TestPRWExporter_Export_ExtraLabels(t *testing.T) {
	var receivedBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
		VMMode:   true,
		VMOptions: VMRemoteWriteOptions{
			ExtraLabels: map[string]string{
				"env":    "prod",
				"region": "us-east-1",
			},
		},
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	err = exp.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	// Decompress and verify
	decompressed, err := compression.Decompress(receivedBody, compression.TypeSnappy)
	if err != nil {
		t.Fatalf("Failed to decompress body: %v", err)
	}

	// Unmarshal and verify
	result := &prw.WriteRequest{}
	if err := result.Unmarshal(decompressed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if len(result.Timeseries) != 1 {
		t.Errorf("Timeseries count = %d, want 1", len(result.Timeseries))
	}

	// Check for extra labels (should have __name__, env, region)
	ts := result.Timeseries[0]
	if len(ts.Labels) != 3 {
		t.Errorf("Labels count = %d, want 3 (including extra labels)", len(ts.Labels))
	}

	hasEnv := false
	hasRegion := false
	for _, l := range ts.Labels {
		if l.Name == "env" && l.Value == "prod" {
			hasEnv = true
		}
		if l.Name == "region" && l.Value == "us-east-1" {
			hasRegion = true
		}
	}

	if !hasEnv {
		t.Error("Extra label 'env' not found")
	}
	if !hasRegion {
		t.Error("Extra label 'region' not found")
	}
}

func TestPRWExporter_Export_NilRequest(t *testing.T) {
	exp, err := NewPRW(context.Background(), PRWExporterConfig{})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	err = exp.Export(context.Background(), nil)
	if err != nil {
		t.Errorf("Export(nil) should not return error, got %v", err)
	}
}

func TestPRWExporter_Export_EmptyRequest(t *testing.T) {
	exp, err := NewPRW(context.Background(), PRWExporterConfig{})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	err = exp.Export(context.Background(), &prw.WriteRequest{})
	if err != nil {
		t.Errorf("Export(empty) should not return error, got %v", err)
	}
}

func TestPRWExporter_Export_ClientError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	err = exp.Export(context.Background(), req)
	if err == nil {
		t.Fatal("Export() should return error for 4xx response")
	}

	clientErr, ok := err.(*PRWClientError)
	if !ok {
		t.Fatalf("Error should be *PRWClientError, got %T", err)
	}

	if clientErr.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode = %d, want %d", clientErr.StatusCode, http.StatusBadRequest)
	}

	if clientErr.IsRetryable() {
		t.Error("Client error should not be retryable")
	}
}

func TestPRWExporter_Export_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	err = exp.Export(context.Background(), req)
	if err == nil {
		t.Fatal("Export() should return error for 5xx response")
	}

	serverErr, ok := err.(*PRWServerError)
	if !ok {
		t.Fatalf("Error should be *PRWServerError, got %T", err)
	}

	if serverErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want %d", serverErr.StatusCode, http.StatusInternalServerError)
	}

	if !serverErr.IsRetryable() {
		t.Error("Server error should be retryable")
	}
}

func TestPRWExporter_Export_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
		Timeout:  50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	err = exp.Export(context.Background(), req)
	if err == nil {
		t.Fatal("Export() should return error for timeout")
	}

	if !strings.Contains(err.Error(), "context deadline exceeded") &&
		!strings.Contains(err.Error(), "Client.Timeout") {
		t.Errorf("Expected timeout error, got: %v", err)
	}
}

func TestPRWExporter_Export_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
		Timeout:  5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	err = exp.Export(ctx, req)
	if err == nil {
		t.Fatal("Export() should return error for context cancellation")
	}

	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("Expected context canceled error, got: %v", err)
	}
}

func TestPRWExporter_VersionHeader(t *testing.T) {
	tests := []struct {
		name       string
		version    prw.Version
		hasPRW2    bool
		wantHeader string
	}{
		{"auto_prw1", prw.VersionAuto, false, "0.1.0"},
		{"auto_prw2", prw.VersionAuto, true, "2.0.0"},
		{"force_prw1", prw.Version1, false, "0.1.0"},
		{"force_prw1_with_prw2_features", prw.Version1, true, "0.1.0"},
		{"force_prw2", prw.Version2, false, "2.0.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var receivedVersion string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedVersion = r.Header.Get("X-Prometheus-Remote-Write-Version")
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()

			exp, err := NewPRW(context.Background(), PRWExporterConfig{
				Endpoint: server.URL,
				Version:  tt.version,
			})
			if err != nil {
				t.Fatalf("NewPRW() error = %v", err)
			}
			defer exp.Close()

			req := &prw.WriteRequest{
				Timeseries: []prw.TimeSeries{
					{
						Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
						Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
					},
				},
			}

			if tt.hasPRW2 {
				req.Timeseries[0].Histograms = []prw.Histogram{
					{Count: 100, Sum: 50.0, Timestamp: 1000},
				}
			}

			err = exp.Export(context.Background(), req)
			if err != nil {
				t.Fatalf("Export() error = %v", err)
			}

			if receivedVersion != tt.wantHeader {
				t.Errorf("X-Prometheus-Remote-Write-Version = %s, want %s", receivedVersion, tt.wantHeader)
			}
		})
	}
}

func TestPRWExporter_Close(t *testing.T) {
	exp, err := NewPRW(context.Background(), PRWExporterConfig{})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}

	err = exp.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestIsPRWRetryableError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil_error", nil, false},
		{"client_error", &PRWClientError{StatusCode: 400, Message: "bad request"}, false},
		{"server_error", &PRWServerError{StatusCode: 500, Message: "server error"}, true},
		{"generic_error", io.EOF, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsPRWRetryableError(tt.err); got != tt.want {
				t.Errorf("IsPRWRetryableError() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPRWExporter_ConcurrentExports(t *testing.T) {
	var requestCount int64

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	numGoroutines := 10
	numRequests := 100

	done := make(chan bool)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			for j := 0; j < numRequests; j++ {
				req := &prw.WriteRequest{
					Timeseries: []prw.TimeSeries{
						{
							Labels:  []prw.Label{{Name: "__name__", Value: "test_metric"}},
							Samples: []prw.Sample{{Value: float64(id*numRequests + j), Timestamp: int64(id*numRequests + j)}},
						},
					},
				}
				_ = exp.Export(context.Background(), req)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines to finish
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	expectedRequests := int64(numGoroutines * numRequests)
	if atomic.LoadInt64(&requestCount) != expectedRequests {
		t.Errorf("Request count = %d, want %d", requestCount, expectedRequests)
	}
}

func TestPRWExporter_Export_LargePayload(t *testing.T) {
	var receivedBodySize int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBodySize = len(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	exp, err := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
	})
	if err != nil {
		t.Fatalf("NewPRW() error = %v", err)
	}
	defer exp.Close()

	// Create a large request with many time series
	req := &prw.WriteRequest{
		Timeseries: make([]prw.TimeSeries, 1000),
	}

	for i := 0; i < 1000; i++ {
		req.Timeseries[i] = prw.TimeSeries{
			Labels: []prw.Label{
				{Name: "__name__", Value: "test_metric"},
				{Name: "instance", Value: "localhost:8080"},
				{Name: "job", Value: "test"},
				{Name: "index", Value: string(rune('0' + i%10))},
			},
			Samples: []prw.Sample{
				{Value: float64(i), Timestamp: int64(1000 + i)},
			},
		}
	}

	err = exp.Export(context.Background(), req)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	// Body should be compressed and non-trivial size
	if receivedBodySize == 0 {
		t.Error("Received body size is zero")
	}

	t.Logf("Large payload (1000 timeseries) compressed to %d bytes", receivedBodySize)
}

func TestPRWClientError_Error(t *testing.T) {
	err := &PRWClientError{
		StatusCode: 400,
		Message:    "test error",
	}

	if err.Error() != "test error" {
		t.Errorf("Error() = %s, want 'test error'", err.Error())
	}
}

func TestPRWServerError_Error(t *testing.T) {
	err := &PRWServerError{
		StatusCode: 500,
		Message:    "test error",
	}

	if err.Error() != "test error" {
		t.Errorf("Error() = %s, want 'test error'", err.Error())
	}
}

// Benchmarks

func BenchmarkPRWExporter_Export_Snappy(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	exp, _ := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
	})
	defer exp.Close()

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
			Samples: []prw.Sample{{Value: float64(i), Timestamp: int64(1609459200000 + i)}},
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = exp.Export(context.Background(), req)
	}
}

func BenchmarkPRWExporter_Export_Zstd(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	exp, _ := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
		VMMode:   true,
		VMOptions: VMRemoteWriteOptions{
			Compression: "zstd",
		},
	})
	defer exp.Close()

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
			Samples: []prw.Sample{{Value: float64(i), Timestamp: int64(1609459200000 + i)}},
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = exp.Export(context.Background(), req)
	}
}

func BenchmarkPRWExporter_Export_LargePayload(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	exp, _ := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
	})
	defer exp.Close()

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
			},
			Samples: []prw.Sample{{Value: float64(i), Timestamp: int64(1609459200000 + i)}},
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = exp.Export(context.Background(), req)
	}
}

func BenchmarkPRWExporter_Export_Parallel(b *testing.B) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	exp, _ := NewPRW(context.Background(), PRWExporterConfig{
		Endpoint: server.URL,
	})
	defer exp.Close()

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels: []prw.Label{
					{Name: "__name__", Value: "http_requests_total"},
					{Name: "method", Value: "GET"},
				},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1609459200000}},
			},
		},
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = exp.Export(context.Background(), req)
		}
	})
}
