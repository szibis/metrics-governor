package exporter

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"

	"github.com/szibis/metrics-governor/internal/compression"
)

// makeTestRequest creates a realistic OTLP export request with multiple metrics.
func makeTestRequest(metricCount int) *colmetricspb.ExportMetricsServiceRequest {
	metrics := make([]*metricspb.Metric, metricCount)
	for i := 0; i < metricCount; i++ {
		metrics[i] = &metricspb.Metric{
			Name:        "test.metric." + string(rune('a'+i%26)),
			Description: "A test metric for compression verification",
			Data: &metricspb.Metric_Gauge{
				Gauge: &metricspb.Gauge{
					DataPoints: []*metricspb.NumberDataPoint{
						{
							Attributes: []*commonpb.KeyValue{
								{Key: "host", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "server-01"}}},
								{Key: "region", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "us-east-1"}}},
							},
							Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 42.5},
						},
					},
				},
			},
		}
	}

	return &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				Resource: &resourcepb.Resource{
					Attributes: []*commonpb.KeyValue{
						{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "test-service"}}},
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

// TestHTTPExporter_CompressionTypes verifies that the HTTP exporter correctly
// compresses payloads with each supported compression type and the server
// receives valid, decompressible data.
func TestHTTPExporter_CompressionTypes(t *testing.T) {
	compressionTypes := []struct {
		name           string
		cfg            compression.Config
		expectedHeader string
	}{
		{"gzip", compression.Config{Type: compression.TypeGzip, Level: compression.LevelDefault}, "gzip"},
		{"zstd", compression.Config{Type: compression.TypeZstd, Level: compression.LevelDefault}, "zstd"},
		{"snappy", compression.Config{Type: compression.TypeSnappy}, "snappy"},
		{"zlib", compression.Config{Type: compression.TypeZlib, Level: compression.LevelDefault}, "zlib"},
		{"deflate", compression.Config{Type: compression.TypeDeflate, Level: compression.LevelDefault}, "deflate"},
		{"none", compression.Config{Type: compression.TypeNone}, ""},
	}

	req := makeTestRequest(10)
	expectedBody, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal request: %v", err)
	}

	for _, tt := range compressionTypes {
		t.Run(tt.name, func(t *testing.T) {
			var receivedBody []byte
			var receivedEncoding string
			var receivedContentType string

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				receivedEncoding = r.Header.Get("Content-Encoding")
				receivedContentType = r.Header.Get("Content-Type")

				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("failed to read body: %v", err)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}

				// Decompress if compressed
				if receivedEncoding != "" {
					ct := compression.ParseContentEncoding(receivedEncoding)
					decompressed, err := compression.Decompress(body, ct)
					if err != nil {
						t.Errorf("failed to decompress %s body: %v", receivedEncoding, err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					receivedBody = decompressed
				} else {
					receivedBody = body
				}

				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			cfg := Config{
				Endpoint:    server.URL,
				Protocol:    ProtocolHTTP,
				Insecure:    true,
				Timeout:     5 * time.Second,
				Compression: tt.cfg,
			}

			exp, err := New(context.Background(), cfg)
			if err != nil {
				t.Fatalf("failed to create exporter: %v", err)
			}
			defer exp.Close()

			err = exp.Export(context.Background(), req)
			if err != nil {
				t.Fatalf("export failed: %v", err)
			}

			// Verify Content-Encoding header
			if receivedEncoding != tt.expectedHeader {
				t.Errorf("Content-Encoding: got %q, want %q", receivedEncoding, tt.expectedHeader)
			}

			// Verify Content-Type
			if receivedContentType != "application/x-protobuf" {
				t.Errorf("Content-Type: got %q, want %q", receivedContentType, "application/x-protobuf")
			}

			// Verify decompressed body matches original protobuf
			if !bytes.Equal(receivedBody, expectedBody) {
				t.Errorf("body mismatch: got %d bytes, want %d bytes", len(receivedBody), len(expectedBody))
			}

			// Verify the decompressed body is a valid OTLP request
			var decoded colmetricspb.ExportMetricsServiceRequest
			if err := proto.Unmarshal(receivedBody, &decoded); err != nil {
				t.Errorf("failed to unmarshal decompressed body: %v", err)
			}
			if len(decoded.ResourceMetrics) != 1 {
				t.Errorf("expected 1 resource metrics, got %d", len(decoded.ResourceMetrics))
			}
		})
	}
}

// TestHTTPExporter_CompressionReducesSize verifies that compression actually
// reduces payload size for compressible metric data.
func TestHTTPExporter_CompressionReducesSize(t *testing.T) {
	compressionTypes := []compression.Config{
		{Type: compression.TypeGzip, Level: compression.LevelDefault},
		{Type: compression.TypeZstd, Level: compression.LevelDefault},
		{Type: compression.TypeSnappy},
	}

	// Create a large request that should be compressible
	req := makeTestRequest(50)
	uncompressedBody, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	for _, compCfg := range compressionTypes {
		t.Run(string(compCfg.Type), func(t *testing.T) {
			var receivedSize int

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				receivedSize = len(body)
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			cfg := Config{
				Endpoint:    server.URL,
				Protocol:    ProtocolHTTP,
				Insecure:    true,
				Timeout:     5 * time.Second,
				Compression: compCfg,
			}

			exp, err := New(context.Background(), cfg)
			if err != nil {
				t.Fatalf("failed to create exporter: %v", err)
			}
			defer exp.Close()

			err = exp.Export(context.Background(), req)
			if err != nil {
				t.Fatalf("export failed: %v", err)
			}

			if receivedSize >= len(uncompressedBody) {
				t.Errorf("%s compression did not reduce size: compressed=%d, uncompressed=%d",
					compCfg.Type, receivedSize, len(uncompressedBody))
			}
			t.Logf("%s: %d -> %d bytes (%.1f%% reduction)",
				compCfg.Type, len(uncompressedBody), receivedSize,
				100*(1-float64(receivedSize)/float64(len(uncompressedBody))))
		})
	}
}

// TestHTTPExporter_ConcurrentCompressedExports verifies that concurrent HTTP
// exports with compression don't corrupt data due to pool sharing issues.
func TestHTTPExporter_ConcurrentCompressedExports(t *testing.T) {
	compressionTypes := []compression.Config{
		{Type: compression.TypeGzip, Level: compression.LevelDefault},
		{Type: compression.TypeZstd, Level: compression.LevelDefault},
		{Type: compression.TypeSnappy},
	}

	for _, compCfg := range compressionTypes {
		t.Run(string(compCfg.Type), func(t *testing.T) {
			var mu sync.Mutex
			var receivedBodies [][]byte

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				encoding := r.Header.Get("Content-Encoding")

				var decompressed []byte
				if encoding != "" {
					ct := compression.ParseContentEncoding(encoding)
					var err error
					decompressed, err = compression.Decompress(body, ct)
					if err != nil {
						t.Errorf("decompress failed: %v", err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
				} else {
					decompressed = body
				}

				mu.Lock()
				receivedBodies = append(receivedBodies, decompressed)
				mu.Unlock()

				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			cfg := Config{
				Endpoint:    server.URL,
				Protocol:    ProtocolHTTP,
				Insecure:    true,
				Timeout:     5 * time.Second,
				Compression: compCfg,
			}

			exp, err := New(context.Background(), cfg)
			if err != nil {
				t.Fatalf("failed to create exporter: %v", err)
			}
			defer exp.Close()

			const goroutines = 20
			var wg sync.WaitGroup

			for i := 0; i < goroutines; i++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					req := makeTestRequest(5 + id%10)
					if err := exp.Export(context.Background(), req); err != nil {
						t.Errorf("goroutine %d: export failed: %v", id, err)
					}
				}(i)
			}

			wg.Wait()

			mu.Lock()
			defer mu.Unlock()

			if len(receivedBodies) != goroutines {
				t.Errorf("expected %d bodies, got %d", goroutines, len(receivedBodies))
			}

			// Verify all bodies are valid OTLP requests
			for i, body := range receivedBodies {
				var decoded colmetricspb.ExportMetricsServiceRequest
				if err := proto.Unmarshal(body, &decoded); err != nil {
					t.Errorf("body %d: failed to unmarshal: %v", i, err)
				}
			}
		})
	}
}
