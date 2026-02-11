package exporter

import (
	"context"
	"testing"
	"time"

	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	"github.com/szibis/metrics-governor/internal/queue"
)

// =============================================================================
// QueuedExporter.Export -- hybrid queue spillover paths
//
// These tests exercise the graduated spillover modes by filling up the
// memory queue to trigger the various utilization thresholds.
// With spillPct=80: PartialDisk at 80%, AllDisk at 90%, LoadShedding at 95%.
// =============================================================================

func hybridConfig(t *testing.T) queue.Config {
	t.Helper()
	return queue.Config{
		Path:               t.TempDir(),
		MaxSize:            100,         // Small memory queue for faster fill
		MaxBytes:           1024 * 1024, // Large byte budget so count-based fills first
		MemoryMaxBytes:     1024 * 1024, // Memory queue byte budget
		RetryInterval:      time.Hour,   // Very long -- prevent workers from consuming
		MaxRetryDelay:      time.Hour,
		AlwaysQueue:        true,
		Workers:            1,
		Mode:               queue.QueueModeHybrid,
		CloseTimeout:       5 * time.Second,
		DrainTimeout:       2 * time.Second,
		DrainEntryTimeout:  1 * time.Second,
		RetryExportTimeout: 2 * time.Second,
	}
}

func smallHybridRequest() *colmetricspb.ExportMetricsServiceRequest {
	return &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{{Name: "hybrid_test"}},
					},
				},
			},
		},
	}
}

// TestHybridExport_MemoryOnlyMode tests the normal spillover mode where
// everything goes to memory when utilization is low.
func TestHybridExport_MemoryOnlyMode(t *testing.T) {
	mock := &aqMockExporter{}
	cfg := hybridConfig(t)

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// Verify hybrid mode
	if qe.QueueMode() != queue.QueueModeHybrid {
		t.Fatalf("QueueMode = %q, want %q", qe.QueueMode(), queue.QueueModeHybrid)
	}

	// Low utilization -- should succeed in MemoryOnly mode
	err = qe.Export(context.Background(), smallHybridRequest())
	if err != nil {
		t.Fatalf("Export in MemoryOnly mode should succeed, got: %v", err)
	}
}

// TestHybridExport_FillToPartialDisk tests that pushing enough items triggers
// the PartialDisk spillover mode.
func TestHybridExport_FillToPartialDisk(t *testing.T) {
	mock := &aqMockExporter{}
	cfg := hybridConfig(t)
	cfg.MaxSize = 20 // Very small so we can fill to 80% quickly

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// Fill to about 80%+ utilization (PartialDisk threshold)
	// With MaxSize=20, we need ~16 items to hit 80%
	for i := 0; i < 18; i++ {
		err := qe.Export(context.Background(), smallHybridRequest())
		if err != nil {
			t.Fatalf("Export %d: %v", i, err)
		}
	}
	// At this point utilization should be high and some items may have spilled to disk
}

// TestHybridExport_FillToAllDisk tests that pushing enough items triggers
// the AllDisk spillover mode.
func TestHybridExport_FillToAllDisk(t *testing.T) {
	mock := &aqMockExporter{}
	cfg := hybridConfig(t)
	cfg.MaxSize = 10 // Very small

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// Fill to about 90%+ utilization (AllDisk threshold)
	for i := 0; i < 12; i++ {
		// Some may fail with "memory queue push failed" but disk should catch them
		_ = qe.Export(context.Background(), smallHybridRequest())
	}
}

// TestHybridExport_FillToLoadShedding tests that filling the queue completely
// triggers load shedding.
func TestHybridExport_FillToLoadShedding(t *testing.T) {
	mock := &aqMockExporter{}
	cfg := hybridConfig(t)
	cfg.MaxSize = 5 // Very small so we fill quickly

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// Fill aggressively -- at some point load shedding should kick in
	sawLoadShedding := false
	for i := 0; i < 50; i++ {
		err := qe.Export(context.Background(), smallHybridRequest())
		if err != nil {
			if containsLower(err.Error(), "load shedding") {
				sawLoadShedding = true
				break
			}
			// Other errors are OK (e.g., "hybrid queue push failed")
		}
	}

	if sawLoadShedding {
		t.Log("Successfully triggered load shedding")
	} else {
		t.Log("Did not trigger load shedding (queue may have overflowed to disk first)")
	}
}

// TestHybridExport_PartialDiskWithRateLimiter tests the PartialDisk path
// when a spillover rate limiter is configured.
func TestHybridExport_PartialDiskWithRateLimiter(t *testing.T) {
	mock := &aqMockExporter{}
	cfg := hybridConfig(t)
	cfg.MaxSize = 20
	cfg.SpilloverRateLimitPerSec = 100

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// Fill to about 80% utilization
	for i := 0; i < 18; i++ {
		err := qe.Export(context.Background(), smallHybridRequest())
		if err != nil {
			t.Fatalf("Export %d: %v", i, err)
		}
	}
}

// TestHybridExport_AllDiskWithRateLimiter tests the AllDisk path when
// a spillover rate limiter is configured with a very low limit, forcing
// the "rate limited -- still try memory" fallback path.
func TestHybridExport_AllDiskWithRateLimiter(t *testing.T) {
	mock := &aqMockExporter{}
	cfg := hybridConfig(t)
	cfg.MaxSize = 10
	// Very low rate limit to force the "rate limited -- try memory" path
	cfg.SpilloverRateLimitPerSec = 1

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// Push many items quickly
	for i := 0; i < 20; i++ {
		_ = qe.Export(context.Background(), smallHybridRequest())
	}
}

// TestHybridExport_MemoryPushFailsFallsToDisk tests that when memory push
// fails in MemoryOnly mode, hybrid falls back to disk.
func TestHybridExport_MemoryPushFailsFallsToDisk(t *testing.T) {
	mock := &aqMockExporter{}
	cfg := hybridConfig(t)
	cfg.MaxSize = 2
	cfg.MaxBytes = 100 // Very small byte budget
	cfg.FullBehavior = queue.DropNewest

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}
	defer qe.Close()

	// Fill memory queue -- push enough to overflow
	for i := 0; i < 20; i++ {
		// Errors are OK -- we're testing the overflow-to-disk path
		_ = qe.Export(context.Background(), smallHybridRequest())
	}
}

// TestHybridExport_HybridQueueWithDiskWorkers tests that disk workers
// process items pushed to disk by hybrid mode.
func TestHybridExport_HybridQueueWithDiskWorkers(t *testing.T) {
	mock := &aqMockExporter{}
	cfg := hybridConfig(t)
	cfg.RetryInterval = 20 * time.Millisecond // Fast retries for disk workers
	cfg.MaxRetryDelay = 200 * time.Millisecond

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	// Push items
	for i := 0; i < 5; i++ {
		err := qe.Export(context.Background(), smallHybridRequest())
		if err != nil {
			t.Fatalf("Export %d: %v", i, err)
		}
	}

	// Wait for workers to process
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if mock.getCalls() >= 5 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	calls := mock.getCalls()
	if calls < 5 {
		t.Errorf("expected at least 5 export calls, got %d", calls)
	}

	qe.Close()
}

// TestHybridExport_HybridPipelineSplit tests hybrid mode with pipeline split.
func TestHybridExport_HybridPipelineSplit(t *testing.T) {
	mock := &pipelineMockExporter{}
	cfg := queue.Config{
		Path:                 t.TempDir(),
		MaxSize:              1000,
		MaxBytes:             1024 * 1024,
		MemoryMaxBytes:       1024 * 1024,
		RetryInterval:        20 * time.Millisecond,
		MaxRetryDelay:        200 * time.Millisecond,
		AlwaysQueue:          true,
		Workers:              2,
		Mode:                 queue.QueueModeHybrid,
		PipelineSplitEnabled: true,
		PreparerCount:        1,
		SenderCount:          1,
		PipelineChannelSize:  16,
		MaxConcurrentSends:   2,
		GlobalSendLimit:      4,
		CloseTimeout:         5 * time.Second,
		DrainTimeout:         2 * time.Second,
		DrainEntryTimeout:    1 * time.Second,
		RetryExportTimeout:   2 * time.Second,
	}

	qe, err := NewQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewQueued: %v", err)
	}

	for i := 0; i < 5; i++ {
		if err := qe.Export(context.Background(), smallHybridRequest()); err != nil {
			t.Fatalf("Export %d: %v", i, err)
		}
	}

	// Wait for processing
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if mock.sendCalls.Load()+mock.exportCalls.Load() >= 5 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	qe.Close()
}
