package prw

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestNewQueue(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "prw-queue-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	exporter := &mockExporter{}
	cfg := QueueConfig{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 100 * time.Millisecond,
		MaxRetryDelay: 1 * time.Second,
	}

	q, err := NewQueue(cfg, exporter)
	if err != nil {
		t.Fatalf("NewQueue() error = %v", err)
	}
	defer q.Close()

	if q.Len() != 0 {
		t.Errorf("new queue Len() = %d, want 0", q.Len())
	}
}

func TestQueue_Push(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "prw-queue-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	exporter := &mockExporter{}
	cfg := QueueConfig{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 1 * time.Hour, // Long interval to prevent automatic processing
		MaxRetryDelay: 1 * time.Hour,
	}

	q, err := NewQueue(cfg, exporter)
	if err != nil {
		t.Fatalf("NewQueue() error = %v", err)
	}
	defer q.Close()

	// Push a request
	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	if err := q.Push(req); err != nil {
		t.Errorf("Push() error = %v", err)
	}

	if q.Len() != 1 {
		t.Errorf("after Push(), Len() = %d, want 1", q.Len())
	}
}

func TestQueue_Push_EmptyRequest(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "prw-queue-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	exporter := &mockExporter{}
	cfg := QueueConfig{
		Path:    tmpDir,
		MaxSize: 100,
	}

	q, err := NewQueue(cfg, exporter)
	if err != nil {
		t.Fatalf("NewQueue() error = %v", err)
	}
	defer q.Close()

	// Nil request
	if err := q.Push(nil); err != nil {
		t.Errorf("Push(nil) error = %v, want nil", err)
	}

	// Empty timeseries
	if err := q.Push(&WriteRequest{}); err != nil {
		t.Errorf("Push(empty) error = %v, want nil", err)
	}

	if q.Len() != 0 {
		t.Errorf("Len() = %d, want 0", q.Len())
	}
}

func TestQueue_Export_Success(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "prw-queue-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	exporter := &mockExporter{}
	cfg := QueueConfig{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 1 * time.Hour,
	}

	q, err := NewQueue(cfg, exporter)
	if err != nil {
		t.Fatalf("NewQueue() error = %v", err)
	}
	defer q.Close()

	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	// Export should succeed and not queue
	if err := q.Export(context.Background(), req); err != nil {
		t.Errorf("Export() error = %v", err)
	}

	if q.Len() != 0 {
		t.Errorf("after successful Export(), Len() = %d, want 0", q.Len())
	}

	exported := exporter.getExported()
	if len(exported) != 1 {
		t.Errorf("exporter exported count = %d, want 1", len(exported))
	}
}

func TestQueue_Export_Failure_Queues(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "prw-queue-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	exporter := &mockExporter{
		failCount: 1000, // Always fail
		failErr:   errors.New("export failed"),
	}
	cfg := QueueConfig{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 1 * time.Hour, // Long to prevent auto-retry
	}

	q, err := NewQueue(cfg, exporter)
	if err != nil {
		t.Fatalf("NewQueue() error = %v", err)
	}
	defer q.Close()

	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	// Export should fail but queue the request
	err = q.Export(context.Background(), req)
	if err != nil {
		t.Errorf("Export() returned error = %v, want nil (queued for retry)", err)
	}

	if q.Len() != 1 {
		t.Errorf("after failed Export(), Len() = %d, want 1", q.Len())
	}
}

func TestQueue_RetryLoop(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "prw-queue-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	exporter := &mockExporter{
		failCount: 2, // Fail first 2 attempts
		failErr:   errors.New("export failed"),
	}
	cfg := QueueConfig{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 50 * time.Millisecond,
		MaxRetryDelay: 100 * time.Millisecond,
	}

	q, err := NewQueue(cfg, exporter)
	if err != nil {
		t.Fatalf("NewQueue() error = %v", err)
	}

	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	// Push directly to queue (skipping initial export attempt)
	if err := q.Push(req); err != nil {
		t.Fatalf("Push() error = %v", err)
	}

	// Wait for retry loop to process
	time.Sleep(500 * time.Millisecond)

	q.Close()

	// Should have successfully exported after retries
	exported := exporter.getExported()
	if len(exported) == 0 {
		t.Error("expected export to eventually succeed after retries")
	}
}

func TestQueue_Close(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "prw-queue-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	exporter := &mockExporter{}
	cfg := QueueConfig{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 1 * time.Hour,
	}

	q, err := NewQueue(cfg, exporter)
	if err != nil {
		t.Fatalf("NewQueue() error = %v", err)
	}

	// Close should not error
	if err := q.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}

	// Double close should not error
	if err := q.Close(); err != nil {
		t.Errorf("second Close() error = %v", err)
	}
}

func TestQueue_SetExporter(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "prw-queue-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	exporter1 := &mockExporter{
		failCount: 1000, // Always fail
		failErr:   errors.New("old exporter fails"),
	}
	cfg := QueueConfig{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 50 * time.Millisecond,
		MaxRetryDelay: 100 * time.Millisecond,
	}

	q, err := NewQueue(cfg, exporter1)
	if err != nil {
		t.Fatalf("NewQueue() error = %v", err)
	}
	defer q.Close()

	// Push a request
	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	if err := q.Push(req); err != nil {
		t.Fatalf("Push() error = %v", err)
	}

	// Wait a bit, then replace exporter with one that succeeds
	time.Sleep(100 * time.Millisecond)

	exporter2 := &mockExporter{}
	q.SetExporter(exporter2)

	// Wait for retry to use new exporter
	time.Sleep(300 * time.Millisecond)

	exported := exporter2.getExported()
	if len(exported) == 0 {
		t.Error("new exporter should have been used")
	}
}

func TestDefaultQueueConfig(t *testing.T) {
	cfg := DefaultQueueConfig()

	if cfg.Path == "" {
		t.Error("default Path should not be empty")
	}
	if cfg.MaxSize == 0 {
		t.Error("default MaxSize should not be 0")
	}
	if cfg.RetryInterval == 0 {
		t.Error("default RetryInterval should not be 0")
	}
	if cfg.MaxRetryDelay == 0 {
		t.Error("default MaxRetryDelay should not be 0")
	}
}

func TestQueue_Size(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "prw-queue-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	exporter := &mockExporter{}
	cfg := QueueConfig{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 1 * time.Hour,
	}

	q, err := NewQueue(cfg, exporter)
	if err != nil {
		t.Fatalf("NewQueue() error = %v", err)
	}
	defer q.Close()

	if q.Size() != 0 {
		t.Errorf("empty queue Size() = %d, want 0", q.Size())
	}

	// Push a request
	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	if err := q.Push(req); err != nil {
		t.Fatalf("Push() error = %v", err)
	}

	if q.Size() == 0 {
		t.Error("queue Size() should be > 0 after push")
	}
}
