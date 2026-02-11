package exporter

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/compression"
	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	"google.golang.org/protobuf/proto"
)

const poolTestTimeout = 10 * time.Second

func poolTestOKServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
}

func poolTestErrorServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
	}))
}

// =============================================================================
// Marshal buffer pool regression tests
// =============================================================================

func TestMarshalBufPool_ReuseAcrossExports(t *testing.T) {
	srv := poolTestOKServer()
	defer srv.Close()

	exp, err := New(context.Background(), Config{
		Endpoint: srv.URL, Protocol: ProtocolHTTP, Insecure: true, Timeout: poolTestTimeout,
	})
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	req := makePoolTestRequest(5)
	for i := 0; i < 10; i++ {
		if err := exp.Export(context.Background(), req); err != nil {
			t.Fatalf("Export %d failed: %v", i, err)
		}
	}
}

func TestExportHTTP_PoolBufferReleasedOnSuccess(t *testing.T) {
	compression.ResetPoolStats()
	srv := poolTestOKServer()
	defer srv.Close()

	exp, err := New(context.Background(), Config{
		Endpoint: srv.URL, Protocol: ProtocolHTTP, Insecure: true, Timeout: poolTestTimeout,
		Compression: compression.Config{Type: compression.TypeZstd},
	})
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	req := makePoolTestRequest(10)
	before := compression.BufferActiveCount()
	if err := exp.Export(context.Background(), req); err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	after := compression.BufferActiveCount()
	if after != before {
		t.Errorf("Buffer leak: active count before=%d, after=%d", before, after)
	}
}

func TestExportHTTP_PoolBufferReleasedOnError(t *testing.T) {
	compression.ResetPoolStats()
	srv := poolTestErrorServer()
	defer srv.Close()

	exp, err := New(context.Background(), Config{
		Endpoint: srv.URL, Protocol: ProtocolHTTP, Insecure: true, Timeout: poolTestTimeout,
		Compression: compression.Config{Type: compression.TypeGzip},
	})
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	req := makePoolTestRequest(5)
	before := compression.BufferActiveCount()
	_ = exp.Export(context.Background(), req) // expect error
	after := compression.BufferActiveCount()
	if after != before {
		t.Errorf("Buffer leak on error path: active count before=%d, after=%d", before, after)
	}
}

func TestExportHTTPData_PoolBufferReleasedOnSuccess(t *testing.T) {
	compression.ResetPoolStats()
	srv := poolTestOKServer()
	defer srv.Close()

	exp, err := New(context.Background(), Config{
		Endpoint: srv.URL, Protocol: ProtocolHTTP, Insecure: true, Timeout: poolTestTimeout,
		Compression: compression.Config{Type: compression.TypeSnappy},
	})
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	data, _ := proto.Marshal(makePoolTestRequest(5))
	before := compression.BufferActiveCount()
	if err := exp.ExportData(context.Background(), data); err != nil {
		t.Fatalf("ExportData failed: %v", err)
	}
	after := compression.BufferActiveCount()
	if after != before {
		t.Errorf("ExportData buffer leak: active count before=%d, after=%d", before, after)
	}
}

func TestExportHTTP_ConcurrentExports_NoPoolLeak(t *testing.T) {
	compression.ResetPoolStats()
	srv := poolTestOKServer()
	defer srv.Close()

	exp, err := New(context.Background(), Config{
		Endpoint: srv.URL, Protocol: ProtocolHTTP, Insecure: true, Timeout: poolTestTimeout,
		Compression: compression.Config{Type: compression.TypeZstd},
	})
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := makePoolTestRequest(3)
			for j := 0; j < 20; j++ {
				_ = exp.Export(context.Background(), req)
			}
		}()
	}
	wg.Wait()

	if got := compression.BufferActiveCount(); got != 0 {
		t.Errorf("Buffer leak after concurrent exports: active count = %d", got)
	}
}

// =============================================================================
// Allocation regression benchmarks
// =============================================================================

func BenchmarkExportHTTP_NoCompression_Pool(b *testing.B) {
	srv := poolTestOKServer()
	defer srv.Close()

	exp, _ := New(context.Background(), Config{
		Endpoint: srv.URL, Protocol: ProtocolHTTP, Insecure: true, Timeout: poolTestTimeout,
	})
	defer exp.Close()
	req := makePoolTestRequest(10)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = exp.Export(context.Background(), req)
	}
}

func BenchmarkExportHTTP_Zstd_Pool(b *testing.B) {
	srv := poolTestOKServer()
	defer srv.Close()

	exp, _ := New(context.Background(), Config{
		Endpoint: srv.URL, Protocol: ProtocolHTTP, Insecure: true, Timeout: poolTestTimeout,
		Compression: compression.Config{Type: compression.TypeZstd},
	})
	defer exp.Close()
	req := makePoolTestRequest(10)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = exp.Export(context.Background(), req)
	}
}

func BenchmarkExportHTTP_Concurrent_Zstd_Pool(b *testing.B) {
	srv := poolTestOKServer()
	defer srv.Close()

	exp, _ := New(context.Background(), Config{
		Endpoint: srv.URL, Protocol: ProtocolHTTP, Insecure: true, Timeout: poolTestTimeout,
		Compression: compression.Config{Type: compression.TypeZstd},
	})
	defer exp.Close()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		req := makePoolTestRequest(5)
		for pb.Next() {
			_ = exp.Export(context.Background(), req)
		}
	})
}

// =============================================================================
// Helpers
// =============================================================================

func makePoolTestRequest(metricCount int) *colmetricspb.ExportMetricsServiceRequest {
	metrics := make([]*metricspb.Metric, metricCount)
	for i := range metrics {
		metrics[i] = &metricspb.Metric{
			Name: "test.metric." + string(rune('a'+i)),
			Data: &metricspb.Metric_Gauge{
				Gauge: &metricspb.Gauge{
					DataPoints: []*metricspb.NumberDataPoint{
						{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)}},
					},
				},
			},
		}
	}
	return &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{ScopeMetrics: []*metricspb.ScopeMetrics{
				{Metrics: metrics},
			}},
		},
	}
}
