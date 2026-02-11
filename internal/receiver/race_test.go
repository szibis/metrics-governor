package receiver

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/compression"
	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	"github.com/szibis/metrics-governor/internal/prw"
	"google.golang.org/protobuf/proto"
)

// raceMockPRWBuffer is a thread-safe mock PRWBuffer for race condition testing.
type raceMockPRWBuffer struct {
	mu    sync.Mutex
	count int
}

func (m *raceMockPRWBuffer) Add(req *prw.WriteRequest) {
	m.mu.Lock()
	m.count++
	m.mu.Unlock()
}

func (m *raceMockPRWBuffer) getCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.count
}

// makeCompressedPRWRequest creates a valid snappy-compressed PRW request body
// with the given number of time series.
func makeCompressedPRWRequest(numTimeseries int) ([]byte, error) {
	req := &prw.WriteRequest{
		Timeseries: make([]prw.TimeSeries, numTimeseries),
	}
	for i := 0; i < numTimeseries; i++ {
		req.Timeseries[i] = prw.TimeSeries{
			Labels: []prw.Label{
				{Name: "__name__", Value: fmt.Sprintf("test_metric_%d", i)},
				{Name: "instance", Value: "localhost:8080"},
			},
			Samples: []prw.Sample{
				{Value: float64(i), Timestamp: int64(i) * 1000},
			},
		}
	}

	body, err := req.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	compressed, err := compression.Compress(body, compression.Config{Type: compression.TypeSnappy})
	if err != nil {
		return nil, fmt.Errorf("compress: %w", err)
	}
	return compressed, nil
}

// --- Race condition tests ---

func TestRace_PRWReceiver_ConcurrentRequests(t *testing.T) {
	buf := &raceMockPRWBuffer{}
	receiver := NewPRW(":0", buf)

	compressed, err := makeCompressedPRWRequest(10)
	if err != nil {
		t.Fatalf("Failed to create test PRW request: %v", err)
	}

	const goroutines = 8
	const requestsPerGoroutine = 50

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < requestsPerGoroutine; i++ {
				httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/write", bytes.NewReader(compressed))
				httpReq.Header.Set("Content-Type", "application/x-protobuf")
				httpReq.Header.Set("Content-Encoding", "snappy")

				w := httptest.NewRecorder()
				receiver.server.Handler.ServeHTTP(w, httpReq)

				if w.Code != http.StatusNoContent {
					t.Errorf("unexpected status code: %d", w.Code)
				}
			}
		}()
	}

	wg.Wait()

	expected := goroutines * requestsPerGoroutine
	got := buf.getCount()
	if got != expected {
		t.Errorf("buffer received %d requests, want %d", got, expected)
	}
}

func TestRace_PRWReceiver_StartStop(t *testing.T) {
	const iterations = 5

	for i := 0; i < iterations; i++ {
		buf := &raceMockPRWBuffer{}
		receiver := NewPRW("127.0.0.1:0", buf)

		go receiver.Start()
		time.Sleep(100 * time.Millisecond)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := receiver.Stop(ctx); err != nil {
			t.Errorf("iteration %d: Stop() error = %v", i, err)
		}
		cancel()
	}
}

func TestRace_MockPRWBuffer_ConcurrentAdd(t *testing.T) {
	buf := &raceMockPRWBuffer{}

	const goroutines = 16
	const addsPerGoroutine = 200

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < addsPerGoroutine; i++ {
				req := &prw.WriteRequest{
					Timeseries: []prw.TimeSeries{
						{
							Labels: []prw.Label{
								{Name: "__name__", Value: fmt.Sprintf("metric_%d_%d", id, i)},
							},
							Samples: []prw.Sample{
								{Value: float64(i), Timestamp: int64(i) * 1000},
							},
						},
					},
				}
				buf.Add(req)
				runtime.Gosched()
			}
		}(g)
	}

	wg.Wait()

	expected := goroutines * addsPerGoroutine
	got := buf.getCount()
	if got != expected {
		t.Errorf("buffer count = %d, want %d", got, expected)
	}
}

// --- HTTP receiver pool race tests ---

func TestRace_HTTPReceiver_PooledUnmarshal(t *testing.T) {
	buf := newTestBuffer()
	r := NewHTTP(":0", buf)

	// 8 goroutines sending concurrent HTTP requests through the pooled unmarshal path
	const goroutines = 8
	const requestsPerGoroutine = 100

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < requestsPerGoroutine; i++ {
				exportReq := &colmetricspb.ExportMetricsServiceRequest{
					ResourceMetrics: []*metricspb.ResourceMetrics{{
						ScopeMetrics: []*metricspb.ScopeMetrics{{
							Metrics: []*metricspb.Metric{{Name: fmt.Sprintf("race_metric_%d_%d", id, i)}},
						}},
					}},
				}
				body, _ := proto.Marshal(exportReq)
				httpReq := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(body))
				httpReq.Header.Set("Content-Type", "application/x-protobuf")
				rec := httptest.NewRecorder()
				r.handleMetrics(rec, httpReq)
				if rec.Code != http.StatusOK {
					t.Errorf("goroutine %d iter %d: expected 200, got %d", id, i, rec.Code)
				}
			}
		}(g)
	}
	wg.Wait()
}

func TestRace_GRPCReceiver_PooledExport(t *testing.T) {
	buf := newTestBuffer()
	r := NewGRPC(":0", buf)

	// 8 goroutines calling Export concurrently
	const goroutines = 8
	const exportsPerGoroutine = 100

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < exportsPerGoroutine; i++ {
				req := &colmetricspb.ExportMetricsServiceRequest{
					ResourceMetrics: []*metricspb.ResourceMetrics{{
						ScopeMetrics: []*metricspb.ScopeMetrics{{
							Metrics: []*metricspb.Metric{{Name: fmt.Sprintf("grpc_race_%d_%d", id, i)}},
						}},
					}},
				}
				if _, err := r.Export(context.Background(), req); err != nil {
					t.Errorf("goroutine %d iter %d: Export failed: %v", id, i, err)
				}
			}
		}(g)
	}
	wg.Wait()
}

// --- Memory leak tests ---

func TestMemLeak_PRWReceiver_RequestCycles(t *testing.T) {
	buf := &raceMockPRWBuffer{}
	receiver := NewPRW(":0", buf)

	compressed, err := makeCompressedPRWRequest(10)
	if err != nil {
		t.Fatalf("Failed to create test PRW request: %v", err)
	}

	// Warm up: run a few requests to stabilize allocations.
	for i := 0; i < 50; i++ {
		httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/write", bytes.NewReader(compressed))
		httpReq.Header.Set("Content-Type", "application/x-protobuf")
		httpReq.Header.Set("Content-Encoding", "snappy")

		w := httptest.NewRecorder()
		receiver.server.Handler.ServeHTTP(w, httpReq)
	}

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	const cycles = 500
	for i := 0; i < cycles; i++ {
		httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/write", bytes.NewReader(compressed))
		httpReq.Header.Set("Content-Type", "application/x-protobuf")
		httpReq.Header.Set("Content-Encoding", "snappy")

		w := httptest.NewRecorder()
		receiver.server.Handler.ServeHTTP(w, httpReq)
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("PRWReceiver request cycles: heap_before=%dKB, heap_after=%dKB, requests=%d",
		heapBefore/1024, heapAfter/1024, cycles)

	const maxGrowthBytes = 20 * 1024 * 1024 // 20MB threshold
	if heapAfter > heapBefore+maxGrowthBytes {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after %d request cycles",
			heapBefore/1024, heapAfter/1024, cycles)
	}
}

func TestMemLeak_HTTPReceiver_PooledRequests(t *testing.T) {
	buf := newTestBuffer()
	r := NewHTTP(":0", buf)

	exportReq := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{{
			ScopeMetrics: []*metricspb.ScopeMetrics{{
				Metrics: []*metricspb.Metric{{Name: "leak_test_metric"}},
			}},
		}},
	}
	body, _ := proto.Marshal(exportReq)

	// Warm up pool
	for i := 0; i < 50; i++ {
		httpReq := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/x-protobuf")
		rec := httptest.NewRecorder()
		r.handleMetrics(rec, httpReq)
	}

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	const cycles = 500
	for i := 0; i < cycles; i++ {
		httpReq := httptest.NewRequest(http.MethodPost, "/v1/metrics", bytes.NewReader(body))
		httpReq.Header.Set("Content-Type", "application/x-protobuf")
		rec := httptest.NewRecorder()
		r.handleMetrics(rec, httpReq)
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("HTTPReceiver pooled requests: heap_before=%dKB, heap_after=%dKB, requests=%d",
		heapBefore/1024, heapAfter/1024, cycles)

	const maxGrowthBytes = 20 * 1024 * 1024 // 20MB threshold
	if heapAfter > heapBefore+maxGrowthBytes {
		t.Errorf("Possible memory leak in pooled receiver: heap grew from %dKB to %dKB after %d request cycles",
			heapBefore/1024, heapAfter/1024, cycles)
	}
}
