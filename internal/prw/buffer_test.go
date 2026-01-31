package prw

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockExporter is a mock PRW exporter for testing.
type mockExporter struct {
	exported  []*WriteRequest
	mu        sync.Mutex
	failCount int
	failErr   error
}

func (m *mockExporter) Export(ctx context.Context, req *WriteRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failCount > 0 {
		m.failCount--
		return m.failErr
	}
	m.exported = append(m.exported, req)
	return nil
}

func (m *mockExporter) Close() error {
	return nil
}

func (m *mockExporter) getExported() []*WriteRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*WriteRequest, len(m.exported))
	copy(result, m.exported)
	return result
}

// mockStats is a mock stats collector for testing.
type mockStats struct {
	receivedDatapoints int64
	receivedTimeseries int64
	exportedDatapoints int64
	exportedTimeseries int64
	exportErrors       int64
}

func (m *mockStats) RecordPRWReceived(datapointCount, timeseriesCount int) {
	atomic.AddInt64(&m.receivedDatapoints, int64(datapointCount))
	atomic.AddInt64(&m.receivedTimeseries, int64(timeseriesCount))
}

func (m *mockStats) RecordPRWExport(datapointCount, timeseriesCount int) {
	atomic.AddInt64(&m.exportedDatapoints, int64(datapointCount))
	atomic.AddInt64(&m.exportedTimeseries, int64(timeseriesCount))
}

func (m *mockStats) RecordPRWExportError() {
	atomic.AddInt64(&m.exportErrors, 1)
}

func TestBuffer_Add(t *testing.T) {
	exp := &mockExporter{}
	cfg := BufferConfig{
		MaxSize:       100,
		MaxBatchSize:  10,
		FlushInterval: time.Hour, // Don't auto-flush
	}
	buf := NewBuffer(cfg, exp, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Start(ctx)

	// Add a request
	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}
	buf.Add(req)

	// Buffer should have the timeseries
	buf.mu.Lock()
	count := len(buf.timeseries)
	buf.mu.Unlock()

	if count != 1 {
		t.Errorf("Buffer timeseries count = %d, want 1", count)
	}
}

func TestBuffer_Add_Concurrent(t *testing.T) {
	exp := &mockExporter{}
	cfg := BufferConfig{
		MaxSize:       1000,
		MaxBatchSize:  100,
		FlushInterval: time.Hour,
	}
	buf := NewBuffer(cfg, exp, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go buf.Start(ctx)

	// Add concurrently
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				req := &WriteRequest{
					Timeseries: []TimeSeries{
						{
							Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
							Samples: []Sample{{Value: float64(id*10 + j), Timestamp: int64(id*10 + j)}},
						},
					},
				}
				buf.Add(req)
			}
		}(i)
	}
	wg.Wait()

	// Buffer should have 100 timeseries
	buf.mu.Lock()
	count := len(buf.timeseries)
	buf.mu.Unlock()

	if count != 100 {
		t.Errorf("Buffer timeseries count = %d, want 100", count)
	}
}

func TestBuffer_Flush(t *testing.T) {
	exp := &mockExporter{}
	cfg := BufferConfig{
		MaxSize:       100,
		MaxBatchSize:  10,
		FlushInterval: 50 * time.Millisecond,
	}
	buf := NewBuffer(cfg, exp, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Add requests
	for i := 0; i < 5; i++ {
		req := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
					Samples: []Sample{{Value: float64(i), Timestamp: int64(i)}},
				},
			},
		}
		buf.Add(req)
	}

	// Wait for flush
	time.Sleep(150 * time.Millisecond)

	// Stop buffer
	cancel()
	buf.Wait()

	// Check exported
	exported := exp.getExported()
	if len(exported) == 0 {
		t.Fatal("No requests exported")
	}

	totalTimeseries := 0
	for _, req := range exported {
		totalTimeseries += len(req.Timeseries)
	}
	if totalTimeseries != 5 {
		t.Errorf("Total exported timeseries = %d, want 5", totalTimeseries)
	}
}

func TestBuffer_FlushBatching(t *testing.T) {
	exp := &mockExporter{}
	cfg := BufferConfig{
		MaxSize:       100,
		MaxBatchSize:  3, // Small batch size
		FlushInterval: 50 * time.Millisecond,
	}
	buf := NewBuffer(cfg, exp, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Add 10 timeseries
	for i := 0; i < 10; i++ {
		req := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
					Samples: []Sample{{Value: float64(i), Timestamp: int64(i)}},
				},
			},
		}
		buf.Add(req)
	}

	// Wait for flush
	time.Sleep(150 * time.Millisecond)
	cancel()
	buf.Wait()

	// Should have multiple batches (10 / 3 = 4 batches)
	exported := exp.getExported()
	if len(exported) < 3 {
		t.Errorf("Expected at least 3 batches, got %d", len(exported))
	}

	// Each batch should have at most 3 timeseries
	for i, req := range exported {
		if len(req.Timeseries) > 3 {
			t.Errorf("Batch %d has %d timeseries, want <= 3", i, len(req.Timeseries))
		}
	}
}

func TestBuffer_FlushOnMaxSize(t *testing.T) {
	exp := &mockExporter{}
	cfg := BufferConfig{
		MaxSize:       5,
		MaxBatchSize:  10,
		FlushInterval: time.Hour, // Don't auto-flush
	}
	buf := NewBuffer(cfg, exp, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Add enough to trigger flush
	for i := 0; i < 6; i++ {
		req := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
					Samples: []Sample{{Value: float64(i), Timestamp: int64(i)}},
				},
			},
		}
		buf.Add(req)
	}

	// Give time for flush
	time.Sleep(50 * time.Millisecond)
	cancel()
	buf.Wait()

	// Should have exported
	exported := exp.getExported()
	if len(exported) == 0 {
		t.Error("No requests exported after buffer full")
	}
}

func TestBuffer_WithStats(t *testing.T) {
	exp := &mockExporter{}
	stats := &mockStats{}
	cfg := BufferConfig{
		MaxSize:       100,
		MaxBatchSize:  10,
		FlushInterval: 50 * time.Millisecond,
	}
	buf := NewBuffer(cfg, exp, stats, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Add requests
	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
			{
				Labels: []Label{{Name: "__name__", Value: "test_metric2"}},
				Samples: []Sample{
					{Value: 2.0, Timestamp: 2000},
					{Value: 3.0, Timestamp: 3000},
				},
			},
		},
	}
	buf.Add(req)

	// Wait for flush
	time.Sleep(150 * time.Millisecond)
	cancel()
	buf.Wait()

	// Check stats
	if stats.receivedDatapoints != 3 {
		t.Errorf("receivedDatapoints = %d, want 3", stats.receivedDatapoints)
	}
	if stats.receivedTimeseries != 2 {
		t.Errorf("receivedTimeseries = %d, want 2", stats.receivedTimeseries)
	}
	if stats.exportedDatapoints != 3 {
		t.Errorf("exportedDatapoints = %d, want 3", stats.exportedDatapoints)
	}
	if stats.exportedTimeseries != 2 {
		t.Errorf("exportedTimeseries = %d, want 2", stats.exportedTimeseries)
	}
}

func TestBuffer_Stop(t *testing.T) {
	exp := &mockExporter{}
	cfg := BufferConfig{
		MaxSize:       100,
		MaxBatchSize:  10,
		FlushInterval: time.Hour,
	}
	buf := NewBuffer(cfg, exp, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Add data
	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}
	buf.Add(req)

	// Stop - should flush remaining
	cancel()
	buf.Wait()

	// Check that data was flushed
	exported := exp.getExported()
	if len(exported) != 1 {
		t.Errorf("Expected 1 exported request on shutdown, got %d", len(exported))
	}
}

func TestBuffer_NilRequest(t *testing.T) {
	exp := &mockExporter{}
	cfg := DefaultBufferConfig()
	buf := NewBuffer(cfg, exp, nil, nil, nil)

	// Should not panic
	buf.Add(nil)

	buf.mu.Lock()
	count := len(buf.timeseries)
	buf.mu.Unlock()

	if count != 0 {
		t.Errorf("Buffer should be empty after adding nil, got %d", count)
	}
}

func TestBuffer_EmptyRequest(t *testing.T) {
	exp := &mockExporter{}
	cfg := DefaultBufferConfig()
	buf := NewBuffer(cfg, exp, nil, nil, nil)

	// Empty request
	buf.Add(&WriteRequest{})

	buf.mu.Lock()
	count := len(buf.timeseries)
	buf.mu.Unlock()

	if count != 0 {
		t.Errorf("Buffer should be empty after adding empty request, got %d", count)
	}
}

func TestBuffer_SetExporter(t *testing.T) {
	exp1 := &mockExporter{}
	exp2 := &mockExporter{}
	cfg := BufferConfig{
		MaxSize:       100,
		MaxBatchSize:  10,
		FlushInterval: 50 * time.Millisecond,
	}
	buf := NewBuffer(cfg, exp1, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Add data
	buf.Add(&WriteRequest{
		Timeseries: []TimeSeries{
			{Labels: []Label{{Name: "__name__", Value: "test1"}}, Samples: []Sample{{Value: 1.0, Timestamp: 1000}}},
		},
	})

	// Wait for flush to exp1
	time.Sleep(100 * time.Millisecond)

	// Change exporter
	buf.SetExporter(exp2)

	// Add more data
	buf.Add(&WriteRequest{
		Timeseries: []TimeSeries{
			{Labels: []Label{{Name: "__name__", Value: "test2"}}, Samples: []Sample{{Value: 2.0, Timestamp: 2000}}},
		},
	})

	// Wait for flush to exp2
	time.Sleep(100 * time.Millisecond)
	cancel()
	buf.Wait()

	// exp1 should have first request
	if len(exp1.getExported()) == 0 {
		t.Error("exp1 should have received data")
	}

	// exp2 should have second request
	if len(exp2.getExported()) == 0 {
		t.Error("exp2 should have received data")
	}
}

func TestDefaultBufferConfig(t *testing.T) {
	cfg := DefaultBufferConfig()

	if cfg.MaxSize != 10000 {
		t.Errorf("MaxSize = %d, want 10000", cfg.MaxSize)
	}
	if cfg.MaxBatchSize != 1000 {
		t.Errorf("MaxBatchSize = %d, want 1000", cfg.MaxBatchSize)
	}
	if cfg.FlushInterval != 5*time.Second {
		t.Errorf("FlushInterval = %v, want 5s", cfg.FlushInterval)
	}
}

func BenchmarkBuffer_Add(b *testing.B) {
	exp := &mockExporter{}
	cfg := BufferConfig{
		MaxSize:       100000,
		MaxBatchSize:  1000,
		FlushInterval: time.Hour,
	}
	buf := NewBuffer(cfg, exp, nil, nil, nil)

	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels: []Label{
					{Name: "__name__", Value: "test_metric"},
					{Name: "method", Value: "GET"},
				},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Add(req)
	}
}

func BenchmarkBuffer_Add_Parallel(b *testing.B) {
	exp := &mockExporter{}
	cfg := BufferConfig{
		MaxSize:       100000,
		MaxBatchSize:  1000,
		FlushInterval: time.Hour,
	}
	buf := NewBuffer(cfg, exp, nil, nil, nil)

	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels: []Label{
					{Name: "__name__", Value: "test_metric"},
					{Name: "method", Value: "GET"},
				},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buf.Add(req)
		}
	})
}
