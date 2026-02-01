package functional

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/compression"
	"github.com/szibis/metrics-governor/internal/exporter"
	"github.com/szibis/metrics-governor/internal/prw"
	"github.com/szibis/metrics-governor/internal/receiver"
)

// mockPRWExporter implements prw.Exporter for testing
type mockPRWExporter struct {
	exported []*prw.WriteRequest
}

func (m *mockPRWExporter) Export(ctx context.Context, req *prw.WriteRequest) error {
	m.exported = append(m.exported, req)
	return nil
}

func (m *mockPRWExporter) Close() error {
	return nil
}

// mockPRWStats implements prw.StatsCollector for testing
type mockPRWStats struct {
	receivedDatapoints int64
	receivedTimeseries int64
	exportedDatapoints int64
	exportedTimeseries int64
	receivedBytes      int64
	sentBytes          int64
}

func (m *mockPRWStats) RecordPRWReceived(datapointCount, timeseriesCount int) {
	atomic.AddInt64(&m.receivedDatapoints, int64(datapointCount))
	atomic.AddInt64(&m.receivedTimeseries, int64(timeseriesCount))
}

func (m *mockPRWStats) RecordPRWExport(datapointCount, timeseriesCount int) {
	atomic.AddInt64(&m.exportedDatapoints, int64(datapointCount))
	atomic.AddInt64(&m.exportedTimeseries, int64(timeseriesCount))
}

func (m *mockPRWStats) RecordPRWExportError() {}

func (m *mockPRWStats) RecordPRWBytesReceived(bytes int) {
	atomic.AddInt64(&m.receivedBytes, int64(bytes))
}

func (m *mockPRWStats) RecordPRWBytesReceivedCompressed(bytes int) {}

func (m *mockPRWStats) RecordPRWBytesSent(bytes int) {
	atomic.AddInt64(&m.sentBytes, int64(bytes))
}

func (m *mockPRWStats) RecordPRWBytesSentCompressed(bytes int) {}

func (m *mockPRWStats) SetPRWBufferSize(size int) {}

func newTestPRWBuffer(exp prw.PRWExporter, stats prw.PRWStatsCollector) *prw.Buffer {
	cfg := prw.BufferConfig{
		MaxSize:       1000,
		MaxBatchSize:  100,
		FlushInterval: 100 * time.Millisecond,
	}
	return prw.NewBuffer(cfg, exp, stats, nil, nil)
}

func createTestPRWRequest(metricName string, samples int) *prw.WriteRequest {
	s := make([]prw.Sample, samples)
	for i := 0; i < samples; i++ {
		s[i] = prw.Sample{
			Value:     float64(i),
			Timestamp: time.Now().UnixMilli() + int64(i),
		}
	}

	return &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels: []prw.Label{
					{Name: "__name__", Value: metricName},
					{Name: "instance", Value: "localhost:8080"},
					{Name: "job", Value: "test"},
				},
				Samples: s,
			},
		},
	}
}

// TestFunctional_PRW_Pipeline_V1 tests the complete PRW 1.0 pipeline
func TestFunctional_PRW_Pipeline_V1(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create mock exporter and buffer
	mockExp := &mockPRWExporter{}
	mockStats := &mockPRWStats{}
	buf := newTestPRWBuffer(mockExp, mockStats)
	go buf.Start(ctx)

	// Create receiver
	addr := getPRWFreeAddr(t)
	cfg := receiver.PRWConfig{
		Addr:    addr,
		Version: "1.0",
	}
	prwReceiver := receiver.NewPRWWithConfig(cfg, buf)
	go prwReceiver.Start()
	defer prwReceiver.Stop(ctx)

	time.Sleep(100 * time.Millisecond)

	// Create and send PRW request
	req := createTestPRWRequest("prw_v1_test_metric", 5)
	body, err := req.Marshal()
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	// Compress with snappy
	compressedBody, err := compression.Compress(body, compression.Config{Type: compression.TypeSnappy})
	if err != nil {
		t.Fatalf("Failed to compress: %v", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/api/v1/write", bytes.NewReader(compressedBody))
	if err != nil {
		t.Fatalf("Failed to create HTTP request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Content-Encoding", "snappy")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected 204, got %d: %s", resp.StatusCode, string(body))
	}

	// Wait for flush
	time.Sleep(200 * time.Millisecond)

	// Verify stats
	if mockStats.receivedTimeseries != 1 {
		t.Errorf("receivedTimeseries = %d, want 1", mockStats.receivedTimeseries)
	}
	if mockStats.receivedDatapoints != 5 {
		t.Errorf("receivedDatapoints = %d, want 5", mockStats.receivedDatapoints)
	}

	t.Log("PRW 1.0 pipeline test passed")
}

// TestFunctional_PRW_Pipeline_V2 tests the complete PRW 2.0 pipeline with histograms
func TestFunctional_PRW_Pipeline_V2(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mockExp := &mockPRWExporter{}
	mockStats := &mockPRWStats{}
	buf := newTestPRWBuffer(mockExp, mockStats)
	go buf.Start(ctx)

	addr := getPRWFreeAddr(t)
	cfg := receiver.PRWConfig{
		Addr:    addr,
		Version: "2.0",
	}
	prwReceiver := receiver.NewPRWWithConfig(cfg, buf)
	go prwReceiver.Start()
	defer prwReceiver.Stop(ctx)

	time.Sleep(100 * time.Millisecond)

	// Create PRW 2.0 request with histogram
	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels: []prw.Label{
					{Name: "__name__", Value: "prw_v2_histogram"},
				},
				Histograms: []prw.Histogram{
					{
						Count:     100,
						Sum:       50.5,
						Schema:    3,
						Timestamp: time.Now().UnixMilli(),
						PositiveSpans: []prw.BucketSpan{
							{Offset: 0, Length: 3},
						},
						PositiveDeltas: []int64{10, 5, -3},
					},
				},
			},
		},
	}

	body, err := req.Marshal()
	if err != nil {
		t.Fatalf("Failed to marshal request: %v", err)
	}

	compressedBody, err := compression.Compress(body, compression.Config{Type: compression.TypeSnappy})
	if err != nil {
		t.Fatalf("Failed to compress: %v", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/api/v1/write", bytes.NewReader(compressedBody))
	if err != nil {
		t.Fatalf("Failed to create HTTP request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Content-Encoding", "snappy")
	httpReq.Header.Set("X-Prometheus-Remote-Write-Version", "2.0.0")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("Expected 204, got %d: %s", resp.StatusCode, string(body))
	}

	time.Sleep(200 * time.Millisecond)

	if mockStats.receivedTimeseries != 1 {
		t.Errorf("receivedTimeseries = %d, want 1", mockStats.receivedTimeseries)
	}

	t.Log("PRW 2.0 pipeline test passed")
}

// TestFunctional_PRW_Compression_Zstd tests zstd compression
func TestFunctional_PRW_Compression_Zstd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mockExp := &mockPRWExporter{}
	buf := newTestPRWBuffer(mockExp, nil)
	go buf.Start(ctx)

	addr := getPRWFreeAddr(t)
	cfg := receiver.PRWConfig{Addr: addr}
	prwReceiver := receiver.NewPRWWithConfig(cfg, buf)
	go prwReceiver.Start()
	defer prwReceiver.Stop(ctx)

	time.Sleep(100 * time.Millisecond)

	req := createTestPRWRequest("zstd_test_metric", 10)
	body, _ := req.Marshal()

	// Compress with zstd
	compressedBody, err := compression.Compress(body, compression.Config{Type: compression.TypeZstd})
	if err != nil {
		t.Fatalf("Failed to compress with zstd: %v", err)
	}

	httpReq, _ := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/api/v1/write", bytes.NewReader(compressedBody))
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Content-Encoding", "zstd")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("Expected 204, got %d", resp.StatusCode)
	}

	t.Log("Zstd compression test passed")
}

// TestFunctional_PRW_EndToEnd_WithExporter tests end-to-end with mock backend
func TestFunctional_PRW_EndToEnd_WithExporter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var receivedRequests int64

	// Create mock PRW backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&receivedRequests, 1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	// Create real exporter pointing to mock backend
	expCfg := exporter.PRWExporterConfig{
		Endpoint: backend.URL,
		Timeout:  5 * time.Second,
	}
	prwExp, err := exporter.NewPRW(ctx, expCfg)
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer prwExp.Close()

	// Create buffer with real exporter
	bufCfg := prw.BufferConfig{
		MaxSize:       1000,
		MaxBatchSize:  100,
		FlushInterval: 50 * time.Millisecond,
	}
	buf := prw.NewBuffer(bufCfg, prwExp, nil, nil, nil)
	go buf.Start(ctx)

	// Create receiver
	addr := getPRWFreeAddr(t)
	cfg := receiver.PRWConfig{Addr: addr}
	prwReceiver := receiver.NewPRWWithConfig(cfg, buf)
	go prwReceiver.Start()
	defer prwReceiver.Stop(ctx)

	time.Sleep(100 * time.Millisecond)

	// Send request through the pipeline
	req := createTestPRWRequest("e2e_metric", 3)
	body, _ := req.Marshal()
	compressedBody, _ := compression.Compress(body, compression.Config{Type: compression.TypeSnappy})

	httpReq, _ := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/api/v1/write", bytes.NewReader(compressedBody))
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Content-Encoding", "snappy")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("Expected 204, got %d", resp.StatusCode)
	}

	// Wait for flush and export
	time.Sleep(200 * time.Millisecond)

	// Verify backend received the data
	if atomic.LoadInt64(&receivedRequests) == 0 {
		t.Error("Backend did not receive any requests")
	}

	t.Log("End-to-end with exporter test passed")
}

// TestFunctional_PRW_HighVolume tests high volume ingestion
func TestFunctional_PRW_HighVolume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	mockExp := &mockPRWExporter{}
	mockStats := &mockPRWStats{}
	buf := newTestPRWBuffer(mockExp, mockStats)
	go buf.Start(ctx)

	addr := getPRWFreeAddr(t)
	cfg := receiver.PRWConfig{Addr: addr}
	prwReceiver := receiver.NewPRWWithConfig(cfg, buf)
	go prwReceiver.Start()
	defer prwReceiver.Stop(ctx)

	time.Sleep(100 * time.Millisecond)

	// Send many requests
	numRequests := 100
	for i := 0; i < numRequests; i++ {
		req := createTestPRWRequest("high_volume_metric", 10)
		body, _ := req.Marshal()
		compressedBody, _ := compression.Compress(body, compression.Config{Type: compression.TypeSnappy})

		httpReq, _ := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/api/v1/write", bytes.NewReader(compressedBody))
		httpReq.Header.Set("Content-Type", "application/x-protobuf")
		httpReq.Header.Set("Content-Encoding", "snappy")

		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			t.Fatalf("Request %d failed: %v", i, err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("Request %d: expected 204, got %d", i, resp.StatusCode)
		}
	}

	// Wait for all flushes
	time.Sleep(500 * time.Millisecond)

	expectedDatapoints := int64(numRequests * 10)
	if mockStats.receivedDatapoints != expectedDatapoints {
		t.Errorf("receivedDatapoints = %d, want %d", mockStats.receivedDatapoints, expectedDatapoints)
	}

	t.Logf("High volume test passed: %d requests, %d datapoints", numRequests, mockStats.receivedDatapoints)
}

// TestFunctional_PRW_LargePayload tests large payload handling
func TestFunctional_PRW_LargePayload(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mockExp := &mockPRWExporter{}
	buf := newTestPRWBuffer(mockExp, nil)
	go buf.Start(ctx)

	addr := getPRWFreeAddr(t)
	cfg := receiver.PRWConfig{Addr: addr}
	prwReceiver := receiver.NewPRWWithConfig(cfg, buf)
	go prwReceiver.Start()
	defer prwReceiver.Stop(ctx)

	time.Sleep(100 * time.Millisecond)

	// Create large request with many time series
	req := &prw.WriteRequest{
		Timeseries: make([]prw.TimeSeries, 1000),
	}
	for i := 0; i < 1000; i++ {
		req.Timeseries[i] = prw.TimeSeries{
			Labels: []prw.Label{
				{Name: "__name__", Value: "large_payload_metric"},
				{Name: "index", Value: string(rune('0' + i%10))},
				{Name: "instance", Value: "localhost:8080"},
			},
			Samples: []prw.Sample{
				{Value: float64(i), Timestamp: time.Now().UnixMilli()},
			},
		}
	}

	body, _ := req.Marshal()
	compressedBody, _ := compression.Compress(body, compression.Config{Type: compression.TypeSnappy})

	t.Logf("Sending large payload: %d bytes compressed (originally %d bytes)", len(compressedBody), len(body))

	httpReq, _ := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/api/v1/write", bytes.NewReader(compressedBody))
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Content-Encoding", "snappy")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("Expected 204, got %d", resp.StatusCode)
	}

	t.Log("Large payload test passed")
}

// TestFunctional_PRW_VMShortEndpoint tests VictoriaMetrics short endpoint
func TestFunctional_PRW_VMShortEndpoint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mockExp := &mockPRWExporter{}
	buf := newTestPRWBuffer(mockExp, nil)
	go buf.Start(ctx)

	addr := getPRWFreeAddr(t)
	cfg := receiver.PRWConfig{Addr: addr}
	prwReceiver := receiver.NewPRWWithConfig(cfg, buf)
	go prwReceiver.Start()
	defer prwReceiver.Stop(ctx)

	time.Sleep(100 * time.Millisecond)

	req := createTestPRWRequest("vm_short_endpoint_metric", 3)
	body, _ := req.Marshal()
	compressedBody, _ := compression.Compress(body, compression.Config{Type: compression.TypeSnappy})

	// Use /write instead of /api/v1/write
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/write", bytes.NewReader(compressedBody))
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Content-Encoding", "snappy")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("Expected 204, got %d", resp.StatusCode)
	}

	t.Log("VM short endpoint test passed")
}

// TestFunctional_PRW_ConcurrentClients tests concurrent client handling
func TestFunctional_PRW_ConcurrentClients(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	mockExp := &mockPRWExporter{}
	mockStats := &mockPRWStats{}
	buf := newTestPRWBuffer(mockExp, mockStats)
	go buf.Start(ctx)

	addr := getPRWFreeAddr(t)
	cfg := receiver.PRWConfig{Addr: addr}
	prwReceiver := receiver.NewPRWWithConfig(cfg, buf)
	go prwReceiver.Start()
	defer prwReceiver.Stop(ctx)

	time.Sleep(100 * time.Millisecond)

	numClients := 10
	numRequestsPerClient := 50
	done := make(chan bool, numClients)

	for c := 0; c < numClients; c++ {
		go func(clientID int) {
			for i := 0; i < numRequestsPerClient; i++ {
				req := createTestPRWRequest("concurrent_metric", 5)
				body, _ := req.Marshal()
				compressedBody, _ := compression.Compress(body, compression.Config{Type: compression.TypeSnappy})

				httpReq, _ := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/api/v1/write", bytes.NewReader(compressedBody))
				httpReq.Header.Set("Content-Type", "application/x-protobuf")
				httpReq.Header.Set("Content-Encoding", "snappy")

				resp, err := http.DefaultClient.Do(httpReq)
				if err != nil {
					t.Logf("Client %d request %d failed: %v", clientID, i, err)
					continue
				}
				resp.Body.Close()
			}
			done <- true
		}(c)
	}

	// Wait for all clients
	for i := 0; i < numClients; i++ {
		<-done
	}

	// Wait for all flushes
	time.Sleep(500 * time.Millisecond)

	expectedDatapoints := int64(numClients * numRequestsPerClient * 5)
	if mockStats.receivedDatapoints < expectedDatapoints/2 {
		t.Errorf("receivedDatapoints = %d, expected at least %d", mockStats.receivedDatapoints, expectedDatapoints/2)
	}

	t.Logf("Concurrent clients test passed: %d clients, %d datapoints received", numClients, mockStats.receivedDatapoints)
}

// TestFunctional_PRW_Exporter_ExtraLabels tests exporter adding extra labels
func TestFunctional_PRW_Exporter_ExtraLabels(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var receivedBody []byte

	// Create mock backend that captures the request
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()

	// Create exporter with extra labels (VM mode)
	expCfg := exporter.PRWExporterConfig{
		Endpoint: backend.URL,
		Timeout:  5 * time.Second,
		VMMode:   true,
		VMOptions: exporter.VMRemoteWriteOptions{
			ExtraLabels: map[string]string{
				"env":    "test",
				"region": "us-east-1",
			},
		},
	}
	prwExp, err := exporter.NewPRW(ctx, expCfg)
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer prwExp.Close()

	// Send request
	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "extra_labels_metric"}},
				Samples: []prw.Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	err = prwExp.Export(ctx, req)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	// Decompress and verify
	decompressed, err := compression.Decompress(receivedBody, compression.TypeSnappy)
	if err != nil {
		t.Fatalf("Failed to decompress: %v", err)
	}

	result := &prw.WriteRequest{}
	if err := result.Unmarshal(decompressed); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	// Verify extra labels were added
	if len(result.Timeseries) != 1 {
		t.Fatalf("Expected 1 timeseries, got %d", len(result.Timeseries))
	}

	hasEnv := false
	hasRegion := false
	for _, l := range result.Timeseries[0].Labels {
		if l.Name == "env" && l.Value == "test" {
			hasEnv = true
		}
		if l.Name == "region" && l.Value == "us-east-1" {
			hasRegion = true
		}
	}

	if !hasEnv || !hasRegion {
		t.Errorf("Extra labels not found. Labels: %v", result.Timeseries[0].Labels)
	}

	t.Log("Exporter extra labels test passed")
}

// Helper function
func getPRWFreeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to get free address: %v", err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}
