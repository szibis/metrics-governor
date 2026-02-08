package exporter

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/queue"
	"go.uber.org/goleak"
)

// TestLeakCheck_PipelineSplitExporter verifies that creating a pipeline-split
// QueuedExporter, exporting items through it, and closing it does not leak
// any goroutines (preparers, senders, scaler, etc.).
func TestLeakCheck_PipelineSplitExporter(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	tmpDir, err := os.MkdirTemp("", "pipeline-split-leak-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	exp := &mockExporter{}
	cfg := queue.Config{
		Path:                 tmpDir,
		MaxSize:              100,
		RetryInterval:        1 * time.Hour,
		MaxRetryDelay:        1 * time.Hour,
		AlwaysQueue:          true,
		Workers:              0,
		PipelineSplitEnabled: true,
		PreparerCount:        2,
		SenderCount:          4,
		PipelineChannelSize:  16,
		MaxConcurrentSends:   2,
		GlobalSendLimit:      8,
	}

	qe, err := NewQueued(exp, cfg)
	if err != nil {
		t.Fatalf("NewQueued() error = %v", err)
	}

	// Export 10 items through the pipeline
	for i := 0; i < 10; i++ {
		req := createTestRequest()
		if err := qe.Export(context.Background(), req); err != nil {
			t.Fatalf("Export() error on item %d: %v", i, err)
		}
	}

	// Allow workers to process the items
	time.Sleep(200 * time.Millisecond)

	if err := qe.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

// TestLeakCheck_PipelineSplitExporter_NoExports verifies that creating a
// pipeline-split QueuedExporter and immediately closing it (without any
// exports) does not leak goroutines.
func TestLeakCheck_PipelineSplitExporter_NoExports(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	tmpDir, err := os.MkdirTemp("", "pipeline-split-noexport-leak-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	exp := &mockExporter{}
	cfg := queue.Config{
		Path:                 tmpDir,
		MaxSize:              100,
		RetryInterval:        1 * time.Hour,
		MaxRetryDelay:        1 * time.Hour,
		AlwaysQueue:          true,
		Workers:              0,
		PipelineSplitEnabled: true,
		PreparerCount:        2,
		SenderCount:          4,
		PipelineChannelSize:  16,
		MaxConcurrentSends:   2,
		GlobalSendLimit:      8,
	}

	qe, err := NewQueued(exp, cfg)
	if err != nil {
		t.Fatalf("NewQueued() error = %v", err)
	}

	if err := qe.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

// TestLeakCheck_PipelineSplitExporter_RepeatedCreateDestroy verifies that
// repeatedly creating and destroying pipeline-split exporters does not
// accumulate leaked goroutines across cycles.
func TestLeakCheck_PipelineSplitExporter_RepeatedCreateDestroy(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	for cycle := 0; cycle < 10; cycle++ {
		tmpDir, err := os.MkdirTemp("", "pipeline-split-cycle-leak-test")
		if err != nil {
			t.Fatalf("cycle %d: failed to create temp dir: %v", cycle, err)
		}

		exp := &mockExporter{}
		cfg := queue.Config{
			Path:                 tmpDir,
			MaxSize:              100,
			RetryInterval:        1 * time.Hour,
			MaxRetryDelay:        1 * time.Hour,
			AlwaysQueue:          true,
			Workers:              0,
			PipelineSplitEnabled: true,
			PreparerCount:        2,
			SenderCount:          4,
			PipelineChannelSize:  16,
			MaxConcurrentSends:   2,
			GlobalSendLimit:      8,
		}

		qe, err := NewQueued(exp, cfg)
		if err != nil {
			os.RemoveAll(tmpDir)
			t.Fatalf("cycle %d: NewQueued() error = %v", cycle, err)
		}

		// Export a few items each cycle
		for i := 0; i < 3; i++ {
			req := createTestRequest()
			if err := qe.Export(context.Background(), req); err != nil {
				os.RemoveAll(tmpDir)
				t.Fatalf("cycle %d: Export() error = %v", cycle, err)
			}
		}

		// Allow workers to process
		time.Sleep(100 * time.Millisecond)

		if err := qe.Close(); err != nil {
			os.RemoveAll(tmpDir)
			t.Fatalf("cycle %d: Close() error = %v", cycle, err)
		}

		os.RemoveAll(tmpDir)
	}
}

// TestMemLeak_PipelineSplit_HeapStable verifies that exporting a large number
// of items through a pipeline-split exporter and then closing it does not
// cause unbounded heap growth (heap increase must stay under 10MB).
func TestMemLeak_PipelineSplit_HeapStable(t *testing.T) {
	// Force GC and take baseline measurement
	runtime.GC()
	runtime.GC()
	var mBefore runtime.MemStats
	runtime.ReadMemStats(&mBefore)
	heapBefore := mBefore.HeapInuse

	tmpDir, err := os.MkdirTemp("", "pipeline-split-mem-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	exp := &mockExporter{}
	cfg := queue.Config{
		Path:                 tmpDir,
		MaxSize:              10000,
		RetryInterval:        1 * time.Hour,
		MaxRetryDelay:        1 * time.Hour,
		AlwaysQueue:          true,
		Workers:              0,
		PipelineSplitEnabled: true,
		PreparerCount:        2,
		SenderCount:          4,
		PipelineChannelSize:  16,
		MaxConcurrentSends:   2,
		GlobalSendLimit:      8,
	}

	qe, err := NewQueued(exp, cfg)
	if err != nil {
		t.Fatalf("NewQueued() error = %v", err)
	}

	// Export 1000 items through the pipeline
	for i := 0; i < 1000; i++ {
		req := createTestRequest()
		if err := qe.Export(context.Background(), req); err != nil {
			t.Fatalf("Export() error on item %d: %v", i, err)
		}
	}

	// Allow workers to fully process all items
	time.Sleep(500 * time.Millisecond)

	if err := qe.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Force GC and measure heap after
	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)
	var mAfter runtime.MemStats
	runtime.ReadMemStats(&mAfter)
	heapAfter := mAfter.HeapInuse

	const maxHeapGrowth = 10 * 1024 * 1024 // 10MB
	if heapAfter > heapBefore+maxHeapGrowth {
		t.Errorf("heap grew by %dKB (from %dKB to %dKB), exceeds 10MB threshold",
			(heapAfter-heapBefore)/1024,
			heapBefore/1024,
			heapAfter/1024,
		)
	}
}
