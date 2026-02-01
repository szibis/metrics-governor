package prw

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testPRWStatsCollector implements PRWStatsCollector for testing
type testPRWStatsCollector struct {
	receivedDP    int64
	receivedTS    int64
	exportedDP    int64
	exportedTS    int64
	errors        int64
	receivedBytes int64
	sentBytes     int64
}

func (m *testPRWStatsCollector) RecordPRWReceived(datapointCount, timeseriesCount int) {
	atomic.AddInt64(&m.receivedDP, int64(datapointCount))
	atomic.AddInt64(&m.receivedTS, int64(timeseriesCount))
}

func (m *testPRWStatsCollector) RecordPRWExport(datapointCount, timeseriesCount int) {
	atomic.AddInt64(&m.exportedDP, int64(datapointCount))
	atomic.AddInt64(&m.exportedTS, int64(timeseriesCount))
}

func (m *testPRWStatsCollector) RecordPRWExportError() {
	atomic.AddInt64(&m.errors, 1)
}

func (m *testPRWStatsCollector) RecordPRWBytesReceived(bytes int) {
	atomic.AddInt64(&m.receivedBytes, int64(bytes))
}

func (m *testPRWStatsCollector) RecordPRWBytesReceivedCompressed(bytes int) {}

func (m *testPRWStatsCollector) RecordPRWBytesSent(bytes int) {
	atomic.AddInt64(&m.sentBytes, int64(bytes))
}

func (m *testPRWStatsCollector) RecordPRWBytesSentCompressed(bytes int) {}

func (m *testPRWStatsCollector) SetPRWBufferSize(size int) {}

// testPRWLimits implements PRWLimitsEnforcer for testing
type testPRWLimits struct {
	dropAll bool
}

func (m *testPRWLimits) Process(req *WriteRequest) *WriteRequest {
	if m.dropAll {
		return nil
	}
	return req
}

// testBufferExporter for testing buffer
type testBufferExporter struct {
	mu          sync.Mutex
	exports     []*WriteRequest
	exportCount int64
}

func (m *testBufferExporter) Export(ctx context.Context, req *WriteRequest) error {
	atomic.AddInt64(&m.exportCount, 1)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.exports = append(m.exports, req)
	return nil
}

func (m *testBufferExporter) Close() error {
	return nil
}

func (m *testBufferExporter) getExportCount() int64 {
	return atomic.LoadInt64(&m.exportCount)
}

func TestBuffer_Add_WithStats(t *testing.T) {
	stats := &testPRWStatsCollector{}
	exp := &testBufferExporter{}

	cfg := BufferConfig{
		MaxSize:       100,
		MaxBatchSize:  10,
		FlushInterval: time.Hour, // Don't auto-flush
	}

	buf := NewBuffer(cfg, exp, stats, nil, nil)

	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
			{
				Labels:  []Label{{Name: "__name__", Value: "test2"}},
				Samples: []Sample{{Value: 2.0, Timestamp: 2000}},
			},
		},
	}

	buf.Add(req)

	// Check stats were recorded
	if stats.receivedDP == 0 {
		t.Error("Expected receivedDP > 0")
	}
	if stats.receivedTS != 2 {
		t.Errorf("Expected receivedTS = 2, got %d", stats.receivedTS)
	}
}

func TestBuffer_Add_WithLimits(t *testing.T) {
	limits := &testPRWLimits{dropAll: true}
	exp := &testBufferExporter{}

	cfg := BufferConfig{
		MaxSize:       100,
		MaxBatchSize:  10,
		FlushInterval: time.Hour,
	}

	buf := NewBuffer(cfg, exp, nil, limits, nil)

	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	buf.Add(req)

	// Buffer should be empty because limits dropped all
	buf.mu.Lock()
	tsCount := len(buf.timeseries)
	buf.mu.Unlock()

	if tsCount != 0 {
		t.Errorf("Expected 0 timeseries (limits dropped all), got %d", tsCount)
	}
}

func TestBuffer_Add_WithMetadata(t *testing.T) {
	exp := &testBufferExporter{}

	cfg := BufferConfig{
		MaxSize:       100,
		MaxBatchSize:  10,
		FlushInterval: time.Hour,
	}

	buf := NewBuffer(cfg, exp, nil, nil, nil)

	// Add request with metadata
	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
		Metadata: []MetricMetadata{
			{MetricFamilyName: "test", Type: MetricTypeCounter, Help: "A test counter"},
		},
	}

	buf.Add(req)

	// Add another request with same metadata family (should update)
	req2 := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test"}},
				Samples: []Sample{{Value: 2.0, Timestamp: 2000}},
			},
		},
		Metadata: []MetricMetadata{
			{MetricFamilyName: "test", Type: MetricTypeCounter, Help: "Updated help"},
		},
	}

	buf.Add(req2)

	buf.mu.Lock()
	metaCount := len(buf.metadata)
	buf.mu.Unlock()

	// Should only have 1 metadata entry (updated)
	if metaCount != 1 {
		t.Errorf("Expected 1 metadata entry, got %d", metaCount)
	}
}

func TestBuffer_Add_TriggersFlush(t *testing.T) {
	exp := &testBufferExporter{}

	cfg := BufferConfig{
		MaxSize:       5, // Small size to trigger flush
		MaxBatchSize:  10,
		FlushInterval: time.Hour,
	}

	buf := NewBuffer(cfg, exp, nil, nil, nil)

	// Start the buffer in background
	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Add enough items to trigger flush
	for i := 0; i < 10; i++ {
		req := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "test"}},
					Samples: []Sample{{Value: float64(i), Timestamp: int64(i * 1000)}},
				},
			},
		}
		buf.Add(req)
	}

	// Wait for flush
	time.Sleep(100 * time.Millisecond)

	// Cancel and wait
	cancel()
	buf.Wait()

	// Exporter should have been called
	if exp.getExportCount() == 0 {
		t.Error("Expected exporter to be called")
	}
}

func TestBuffer_Add_NilRequest(t *testing.T) {
	exp := &testBufferExporter{}

	cfg := DefaultBufferConfig()
	buf := NewBuffer(cfg, exp, nil, nil, nil)

	// Should not panic
	buf.Add(nil)

	buf.mu.Lock()
	tsCount := len(buf.timeseries)
	buf.mu.Unlock()

	if tsCount != 0 {
		t.Errorf("Expected 0 timeseries for nil request, got %d", tsCount)
	}
}

func TestLimitsEnforcer_Stop(t *testing.T) {
	cfg := LimitsConfig{
		Rules: []LimitRule{
			{
				Name:                   "test",
				MetricPattern:          "test_.*",
				MaxDatapointsPerSecond: 100,
			},
		},
	}

	enforcer := NewLimitsEnforcer(cfg, false)

	// Stop should not panic
	enforcer.Stop()

	// Multiple stops should be safe
	enforcer.Stop()
}

func TestLimitsEnforcer_Process(t *testing.T) {
	cfg := LimitsConfig{
		Rules: []LimitRule{
			{
				Name:                   "test",
				MetricPattern:          "test_.*",
				MaxDatapointsPerSecond: 1000,
			},
		},
	}

	enforcer := NewLimitsEnforcer(cfg, false)
	defer enforcer.Stop()

	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	result := enforcer.Process(req)
	if result == nil {
		t.Error("Expected non-nil result for under-limit request")
	}
}

func TestLimitsEnforcer_ResetCardinality(t *testing.T) {
	cfg := LimitsConfig{
		Rules: []LimitRule{
			{
				Name:           "card_test",
				MetricPattern:  "card_.*",
				MaxCardinality: 100,
			},
		},
	}

	enforcer := NewLimitsEnforcer(cfg, false)
	defer enforcer.Stop()

	// Process some requests to build cardinality
	for i := 0; i < 10; i++ {
		req := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "card_metric"}, {Name: "id", Value: string(rune('0' + i))}},
					Samples: []Sample{{Value: float64(i), Timestamp: int64(i * 1000)}},
				},
			},
		}
		enforcer.Process(req)
	}

	// Reset cardinality
	enforcer.ResetCardinality()

	// Cardinality map should be empty
	enforcer.mu.Lock()
	cardLen := len(enforcer.cardinalityMap)
	enforcer.mu.Unlock()

	if cardLen != 0 {
		t.Errorf("Expected empty cardinality map after reset, got %d", cardLen)
	}
}

func TestLimitsEnforcer_Stats(t *testing.T) {
	cfg := LimitsConfig{
		Rules: []LimitRule{
			{
				Name:                   "stats_test",
				MetricPattern:          "stats_.*",
				MaxDatapointsPerSecond: 10,
			},
		},
	}

	enforcer := NewLimitsEnforcer(cfg, false)
	defer enforcer.Stop()

	// Process some requests
	for i := 0; i < 20; i++ {
		req := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "stats_metric"}},
					Samples: []Sample{{Value: float64(i), Timestamp: int64(i * 1000)}},
				},
			},
		}
		enforcer.Process(req)
	}

	// Get stats - returns two int64 values
	dropped, violations := enforcer.Stats()
	t.Logf("Stats: dropped=%d, violations=%d", dropped, violations)
}

func TestLimitsEnforcer_ResetStats(t *testing.T) {
	cfg := LimitsConfig{
		Rules: []LimitRule{
			{
				Name:                   "reset_test",
				MetricPattern:          "reset_.*",
				MaxDatapointsPerSecond: 10,
			},
		},
	}

	enforcer := NewLimitsEnforcer(cfg, false)
	defer enforcer.Stop()

	// Process some requests
	for i := 0; i < 5; i++ {
		req := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "reset_metric"}},
					Samples: []Sample{{Value: float64(i), Timestamp: int64(i * 1000)}},
				},
			},
		}
		enforcer.Process(req)
	}

	// Reset stats
	enforcer.ResetStats()

	// Stats should be cleared
	dropped, violations := enforcer.Stats()
	if dropped != 0 || violations != 0 {
		t.Errorf("Expected stats to be reset, got dropped=%d, violations=%d", dropped, violations)
	}
}

func TestBuffer_Flush_WithStats(t *testing.T) {
	stats := &testPRWStatsCollector{}
	exp := &testBufferExporter{}

	cfg := BufferConfig{
		MaxSize:       100,
		MaxBatchSize:  5,
		FlushInterval: 50 * time.Millisecond, // Short interval
	}

	buf := NewBuffer(cfg, exp, stats, nil, nil)

	// Start buffer
	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Add data
	for i := 0; i < 10; i++ {
		req := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "flush_test"}},
					Samples: []Sample{{Value: float64(i), Timestamp: int64(i * 1000)}},
				},
			},
		}
		buf.Add(req)
	}

	// Wait for flush
	time.Sleep(150 * time.Millisecond)

	cancel()
	buf.Wait()

	// Check that exports happened
	if stats.exportedDP == 0 {
		t.Error("Expected exportedDP > 0 after flush")
	}
}

func TestBuffer_Flush_Empty(t *testing.T) {
	exp := &testBufferExporter{}

	cfg := BufferConfig{
		MaxSize:       100,
		MaxBatchSize:  10,
		FlushInterval: 50 * time.Millisecond,
	}

	buf := NewBuffer(cfg, exp, nil, nil, nil)

	// Start buffer without adding data
	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Wait for interval to pass
	time.Sleep(100 * time.Millisecond)

	cancel()
	buf.Wait()

	// Exporter should not have been called (nothing to flush)
	if exp.getExportCount() != 0 {
		t.Errorf("Expected 0 exports for empty buffer, got %d", exp.getExportCount())
	}
}

func TestBuffer_BatchSize(t *testing.T) {
	exp := &testBufferExporter{}

	cfg := BufferConfig{
		MaxSize:       100,
		MaxBatchSize:  3, // Small batch size
		FlushInterval: time.Hour,
	}

	buf := NewBuffer(cfg, exp, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Add more than batch size
	for i := 0; i < 10; i++ {
		req := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "batch_test"}},
					Samples: []Sample{{Value: float64(i), Timestamp: int64(i * 1000)}},
				},
			},
		}
		buf.Add(req)
	}

	// Wait for batch flushes
	time.Sleep(100 * time.Millisecond)

	cancel()
	buf.Wait()

	// Multiple batches should have been exported
	if exp.getExportCount() < 2 {
		t.Errorf("Expected multiple batch exports, got %d", exp.getExportCount())
	}
}

func TestLimitsEnforcer_DryRun(t *testing.T) {
	cfg := LimitsConfig{
		Rules: []LimitRule{
			{
				Name:                   "dryrun_test",
				MetricPattern:          "dry_.*",
				MaxDatapointsPerSecond: 5, // Low limit
			},
		},
	}

	// Create with dry run enabled
	enforcer := NewLimitsEnforcer(cfg, true)
	defer enforcer.Stop()

	// Process many requests - should not drop in dry run
	for i := 0; i < 20; i++ {
		req := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "dry_metric"}},
					Samples: []Sample{{Value: float64(i), Timestamp: int64(i * 1000)}},
				},
			},
		}
		result := enforcer.Process(req)
		// In dry run mode, nothing should be dropped
		if result == nil {
			t.Errorf("Dry run mode should not drop metrics, iteration %d", i)
		}
	}
}

func TestLimitsEnforcer_NoRules(t *testing.T) {
	cfg := LimitsConfig{
		Rules: []LimitRule{}, // No rules
	}

	enforcer := NewLimitsEnforcer(cfg, false)
	defer enforcer.Stop()

	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "any_metric"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	result := enforcer.Process(req)
	// Without rules, all metrics should pass
	if result == nil {
		t.Error("Expected metrics to pass with no rules")
	}
}

func TestLimitsEnforcer_NoMatch(t *testing.T) {
	cfg := LimitsConfig{
		Rules: []LimitRule{
			{
				Name:                   "specific_rule",
				MetricPattern:          "specific_.*", // Pattern that won't match
				MaxDatapointsPerSecond: 10,
			},
		},
	}

	enforcer := NewLimitsEnforcer(cfg, false)
	defer enforcer.Stop()

	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "other_metric"}}, // Doesn't match
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	result := enforcer.Process(req)
	// Non-matching metrics should pass
	if result == nil {
		t.Error("Expected non-matching metrics to pass")
	}
}

func TestLimitsEnforcer_Cardinality(t *testing.T) {
	cfg := LimitsConfig{
		Rules: []LimitRule{
			{
				Name:           "card_limit",
				MetricPattern:  "card_.*",
				MaxCardinality: 5, // Low cardinality limit
			},
		},
	}

	enforcer := NewLimitsEnforcer(cfg, false)
	defer enforcer.Stop()

	// Process many unique series
	for i := 0; i < 10; i++ {
		req := &WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels: []Label{
						{Name: "__name__", Value: "card_metric"},
						{Name: "id", Value: string(rune('a' + i))}, // Unique id
					},
					Samples: []Sample{{Value: float64(i), Timestamp: int64(i * 1000)}},
				},
			},
		}
		enforcer.Process(req)
	}

	// Check stats
	dropped, _ := enforcer.Stats()
	// Should have dropped some due to cardinality limit
	t.Logf("Dropped due to cardinality: %d", dropped)
}

func TestBuffer_WithExemplar(t *testing.T) {
	exp := &testBufferExporter{}

	cfg := BufferConfig{
		MaxSize:       100,
		MaxBatchSize:  10,
		FlushInterval: time.Hour,
	}

	buf := NewBuffer(cfg, exp, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Add request with exemplars
	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels: []Label{{Name: "__name__", Value: "with_exemplar"}},
				Histograms: []Histogram{
					{
						Timestamp: 1000,
						Sum:       10.0,
						Count:     5,
					},
				},
			},
		},
	}

	buf.Add(req)

	// Force flush by adding enough to trigger
	for i := 0; i < 10; i++ {
		buf.Add(&WriteRequest{
			Timeseries: []TimeSeries{
				{
					Labels:  []Label{{Name: "__name__", Value: "test"}},
					Samples: []Sample{{Value: float64(i), Timestamp: int64(i * 1000)}},
				},
			},
		})
	}

	time.Sleep(100 * time.Millisecond)
	cancel()
	buf.Wait()
}
