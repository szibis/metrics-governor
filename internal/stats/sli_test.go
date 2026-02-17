package stats

import (
	"math"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func defaultSLIConfig() SLIConfig {
	return SLIConfig{
		Enabled:        true,
		DeliveryTarget: 0.999,
		ExportTarget:   0.995,
		BudgetWindow:   720 * time.Hour,
	}
}

func TestNewSLITracker(t *testing.T) {
	tracker := NewSLITracker(defaultSLIConfig(), nil)
	if tracker == nil {
		t.Fatal("expected non-nil tracker")
	}
	if len(tracker.ring) != DefaultRingSize {
		t.Errorf("ring size = %d, want %d", len(tracker.ring), DefaultRingSize)
	}
	if tracker.count != 0 {
		t.Errorf("count = %d, want 0", tracker.count)
	}
	// dropsProvider should default to returning 0
	if tracker.dropsProvider() != 0 {
		t.Error("default dropsProvider should return 0")
	}
}

func TestRecordSnapshot(t *testing.T) {
	tracker := NewSLITracker(defaultSLIConfig(), nil)

	r := counterReader{
		otlpReceived:     1000,
		otlpSent:         999,
		prwReceived:      500,
		prwSent:          498,
		otlpBatchesSent:  100,
		otlpExportErrors: 1,
		prwBatchesSent:   50,
		prwExportErrors:  0,
	}

	tracker.RecordSnapshot(r)

	tracker.mu.RLock()
	defer tracker.mu.RUnlock()

	if tracker.count != 1 {
		t.Errorf("count = %d, want 1", tracker.count)
	}
	if tracker.startSnapshot == nil {
		t.Fatal("startSnapshot should be set after first recording")
	}
	if tracker.snapshotsTotal != 1 {
		t.Errorf("snapshotsTotal = %d, want 1", tracker.snapshotsTotal)
	}

	snap, ok := tracker.getSnapshotAt(0)
	if !ok {
		t.Fatal("should be able to get latest snapshot")
	}
	if snap.otlpReceived != 1000 {
		t.Errorf("otlpReceived = %d, want 1000", snap.otlpReceived)
	}
}

func TestRingBufferWrap(t *testing.T) {
	cfg := defaultSLIConfig()
	tracker := NewSLITracker(cfg, nil)

	// Fill the ring and then some
	total := DefaultRingSize + 10
	for i := 0; i < total; i++ {
		tracker.RecordSnapshot(counterReader{
			otlpReceived: uint64(i * 100),
			otlpSent:     uint64(i * 99),
		})
	}

	tracker.mu.RLock()
	defer tracker.mu.RUnlock()

	if tracker.count != DefaultRingSize {
		t.Errorf("count = %d, want %d (should cap at ring size)", tracker.count, DefaultRingSize)
	}
	if tracker.snapshotsTotal != uint64(total) {
		t.Errorf("snapshotsTotal = %d, want %d", tracker.snapshotsTotal, total)
	}

	// Latest should be the last recorded
	snap, ok := tracker.latest()
	if !ok {
		t.Fatal("should have latest snapshot")
	}
	expected := uint64((total - 1) * 100)
	if snap.otlpReceived != expected {
		t.Errorf("latest otlpReceived = %d, want %d", snap.otlpReceived, expected)
	}
}

func TestComputeDeliveryRatio(t *testing.T) {
	tests := []struct {
		name   string
		older  sliSnapshot
		newer  sliSnapshot
		expect float64
	}{
		{
			name:  "perfect delivery",
			older: sliSnapshot{otlpReceived: 0, otlpSent: 0, prwReceived: 0, prwSent: 0},
			newer: sliSnapshot{otlpReceived: 1000, otlpSent: 1000, prwReceived: 500, prwSent: 500},
			// good=1500, eligible=1500, ratio=1.0
			expect: 1.0,
		},
		{
			name:  "some loss",
			older: sliSnapshot{},
			newer: sliSnapshot{otlpReceived: 1000, otlpSent: 990, prwReceived: 500, prwSent: 495},
			// good=1485, eligible=1500, ratio=0.99
			expect: 0.99,
		},
		{
			name:  "with intentional drops",
			older: sliSnapshot{intentionalDrops: 0},
			newer: sliSnapshot{
				otlpReceived: 1000, otlpSent: 900,
				prwReceived: 0, prwSent: 0,
				intentionalDrops: 100,
			},
			// good=900, eligible=1000-100=900, ratio=1.0
			expect: 1.0,
		},
		{
			name:  "no eligible events",
			older: sliSnapshot{},
			newer: sliSnapshot{},
			// eligible=0, should return 1.0
			expect: 1.0,
		},
		{
			name:  "all dropped intentionally",
			older: sliSnapshot{},
			newer: sliSnapshot{
				otlpReceived: 1000, otlpSent: 0,
				intentionalDrops: 1000,
			},
			// good=0, eligible=0, ratio=1.0 (all drops are intentional)
			expect: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeDeliveryRatio(tt.newer, tt.older)
			if math.Abs(got-tt.expect) > 0.001 {
				t.Errorf("delivery ratio = %g, want %g", got, tt.expect)
			}
		})
	}
}

func TestComputeExportSuccessRatio(t *testing.T) {
	tests := []struct {
		name   string
		older  sliSnapshot
		newer  sliSnapshot
		expect float64
	}{
		{
			name:   "perfect export",
			older:  sliSnapshot{},
			newer:  sliSnapshot{otlpBatchesSent: 100, prwBatchesSent: 50},
			expect: 1.0,
		},
		{
			name:  "some errors",
			older: sliSnapshot{},
			newer: sliSnapshot{
				otlpBatchesSent: 100, otlpExportErrors: 5,
				prwBatchesSent: 50, prwExportErrors: 2,
			},
			// good=150, total=157, ratio=150/157≈0.9554
			expect: 150.0 / 157.0,
		},
		{
			name:   "no batches",
			older:  sliSnapshot{},
			newer:  sliSnapshot{},
			expect: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeExportSuccessRatio(tt.newer, tt.older)
			if math.Abs(got-tt.expect) > 0.001 {
				t.Errorf("export ratio = %g, want %g", got, tt.expect)
			}
		})
	}
}

func TestComputeBurnRate(t *testing.T) {
	tests := []struct {
		name   string
		ratio  float64
		target float64
		expect float64
	}{
		{
			name:   "at SLO pace",
			ratio:  0.999,
			target: 0.999,
			expect: 1.0,
		},
		{
			name:   "14.4x burn",
			ratio:  1.0 - 14.4*0.001,
			target: 0.999,
			// error_rate = 0.0144, allowed = 0.001, burn = 14.4
			expect: 14.4,
		},
		{
			name:   "no errors",
			ratio:  1.0,
			target: 0.999,
			expect: 0.0,
		},
		{
			name:   "100% target",
			ratio:  0.999,
			target: 1.0,
			expect: 0.0, // can't compute with target=1.0
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeBurnRate(tt.ratio, tt.target)
			if math.Abs(got-tt.expect) > 0.01 {
				t.Errorf("burn rate = %g, want %g", got, tt.expect)
			}
		})
	}
}

func TestErrorBudgetRemaining(t *testing.T) {
	cfg := defaultSLIConfig()
	tracker := NewSLITracker(cfg, nil)
	tracker.startTime = time.Now().Add(-2 * time.Minute) // enough elapsed time

	// Start: no events
	start := sliSnapshot{}
	// Latest: perfect delivery
	latest := sliSnapshot{
		otlpReceived: 10000,
		otlpSent:     10000,
	}

	budget := tracker.computeErrorBudgetRemaining(start, latest, computeDeliveryRatio, 0.999)
	if math.Abs(budget-1.0) > 0.001 {
		t.Errorf("budget remaining = %g, want 1.0 (perfect delivery)", budget)
	}

	// 50% of budget consumed
	latestLoss := sliSnapshot{
		otlpReceived: 10000,
		otlpSent:     9995, // 5 lost out of 10000
		// allowed: 10000 * 0.001 = 10 errors over budget window
		// actual: 5 errors, so consumed = 5/10 = 0.5, remaining = 0.5
	}
	budget = tracker.computeErrorBudgetRemaining(start, latestLoss, computeDeliveryRatio, 0.999)
	if math.Abs(budget-0.5) > 0.001 {
		t.Errorf("budget remaining = %g, want 0.5", budget)
	}
}

func TestErrorBudgetTooEarly(t *testing.T) {
	cfg := defaultSLIConfig()
	tracker := NewSLITracker(cfg, nil)
	tracker.startTime = time.Now() // just started, < 60s

	start := sliSnapshot{}
	latest := sliSnapshot{otlpReceived: 100, otlpSent: 50}

	budget := tracker.computeErrorBudgetRemaining(start, latest, computeDeliveryRatio, 0.999)
	if budget != 1.0 {
		t.Errorf("budget = %g, want 1.0 (too early to compute)", budget)
	}
}

func TestWriteSLIMetrics_NoData(t *testing.T) {
	tracker := NewSLITracker(defaultSLIConfig(), nil)

	rec := httptest.NewRecorder()
	tracker.WriteSLIMetrics(rec)

	body := rec.Body.String()
	// Should still emit config metrics
	if !strings.Contains(body, "metrics_governor_slo_target") {
		t.Error("expected config metrics even with no data")
	}
	// Should NOT emit ratios (not enough data)
	if strings.Contains(body, "metrics_governor_sli_delivery_ratio") {
		t.Error("should not emit ratios with < 2 snapshots")
	}
}

func TestWriteSLIMetrics_WithData(t *testing.T) {
	drops := int64(100)
	tracker := NewSLITracker(defaultSLIConfig(), func() int64 { return drops })
	tracker.startTime = time.Now().Add(-10 * time.Minute) // enough for budget

	// Record multiple snapshots to fill at least the 5m window
	for i := 0; i < 15; i++ {
		tracker.RecordSnapshot(counterReader{
			otlpReceived:     uint64(1000 * (i + 1)),
			otlpSent:         uint64(990 * (i + 1)),
			prwReceived:      uint64(500 * (i + 1)),
			prwSent:          uint64(498 * (i + 1)),
			otlpBatchesSent:  uint64(100 * (i + 1)),
			otlpExportErrors: uint64(i), // increasing errors
			prwBatchesSent:   uint64(50 * (i + 1)),
			prwExportErrors:  0,
		})
	}

	rec := httptest.NewRecorder()
	tracker.WriteSLIMetrics(rec)

	body := rec.Body.String()

	expectedMetrics := []string{
		"metrics_governor_sli_delivery_ratio",
		"metrics_governor_sli_export_success_ratio",
		"metrics_governor_sli_delivery_burn_rate",
		"metrics_governor_sli_export_burn_rate",
		"metrics_governor_sli_delivery_budget_remaining",
		"metrics_governor_sli_export_budget_remaining",
		"metrics_governor_slo_target",
		"metrics_governor_sli_uptime_seconds",
		"metrics_governor_sli_snapshots_total",
	}
	for _, metric := range expectedMetrics {
		if !strings.Contains(body, metric) {
			t.Errorf("expected metric %q in output", metric)
		}
	}

	// Check window labels
	for _, win := range []string{"5m"} {
		if !strings.Contains(body, `window="`+win+`"`) {
			t.Errorf("expected window=%q label", win)
		}
	}
}

func TestWriteSLIMetrics_SLOTargets(t *testing.T) {
	cfg := SLIConfig{
		Enabled:        true,
		DeliveryTarget: 0.999,
		ExportTarget:   0.995,
		BudgetWindow:   720 * time.Hour,
	}
	tracker := NewSLITracker(cfg, nil)

	rec := httptest.NewRecorder()
	tracker.WriteSLIMetrics(rec)
	body := rec.Body.String()

	if !strings.Contains(body, `metrics_governor_slo_target{sli="delivery"} 0.999`) {
		t.Error("expected delivery target 0.999")
	}
	if !strings.Contains(body, `metrics_governor_slo_target{sli="export"} 0.995`) {
		t.Error("expected export target 0.995")
	}
}

func TestDropsProvider(t *testing.T) {
	drops := int64(0)
	tracker := NewSLITracker(defaultSLIConfig(), func() int64 { return drops })

	// Record baseline
	tracker.RecordSnapshot(counterReader{otlpReceived: 0})

	// Simulate: 1000 received, 900 sent, 100 intentionally dropped
	drops = 100
	tracker.RecordSnapshot(counterReader{
		otlpReceived: 1000,
		otlpSent:     900,
	})

	tracker.mu.RLock()
	latest, _ := tracker.latest()
	older, _ := tracker.getSnapshotAt(1)
	tracker.mu.RUnlock()

	ratio := computeDeliveryRatio(latest, older)
	// good=900, eligible=1000-100=900, ratio=1.0
	if math.Abs(ratio-1.0) > 0.001 {
		t.Errorf("delivery ratio with drops = %g, want 1.0", ratio)
	}
}

func TestSLIConcurrentAccess(t *testing.T) {
	tracker := NewSLITracker(defaultSLIConfig(), nil)
	var wg sync.WaitGroup

	// Writer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			tracker.RecordSnapshot(counterReader{
				otlpReceived: uint64(i * 100),
				otlpSent:     uint64(i * 99),
			})
		}
	}()

	// Reader goroutines
	for j := 0; j < 4; j++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				rec := httptest.NewRecorder()
				tracker.WriteSLIMetrics(rec)
			}
		}()
	}

	wg.Wait()
}

func TestGetSnapshotAtOutOfRange(t *testing.T) {
	tracker := NewSLITracker(defaultSLIConfig(), nil)

	tracker.mu.RLock()
	_, ok := tracker.getSnapshotAt(0)
	tracker.mu.RUnlock()
	if ok {
		t.Error("should return false for empty ring")
	}

	tracker.RecordSnapshot(counterReader{otlpReceived: 100})

	tracker.mu.RLock()
	_, ok = tracker.getSnapshotAt(5)
	tracker.mu.RUnlock()
	if ok {
		t.Error("should return false for out-of-range slot")
	}
}

func TestDeliveryRatioClamp(t *testing.T) {
	// Test that ratio is clamped to [0, 1]
	// Scenario: sent > received (shouldn't happen but test defensively)
	older := sliSnapshot{}
	newer := sliSnapshot{
		otlpReceived: 100,
		otlpSent:     200, // more sent than received
	}
	ratio := computeDeliveryRatio(newer, older)
	if ratio > 1.0 {
		t.Errorf("ratio should be clamped to 1.0, got %g", ratio)
	}
}

func BenchmarkRecordSnapshot(b *testing.B) {
	tracker := NewSLITracker(defaultSLIConfig(), func() int64 { return 42 })
	r := counterReader{
		otlpReceived:     1000000,
		otlpSent:         999900,
		prwReceived:      500000,
		prwSent:          499800,
		otlpBatchesSent:  10000,
		otlpExportErrors: 5,
		prwBatchesSent:   5000,
		prwExportErrors:  2,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tracker.RecordSnapshot(r)
	}
}

func BenchmarkWriteSLIMetrics(b *testing.B) {
	tracker := NewSLITracker(defaultSLIConfig(), func() int64 { return 42 })
	tracker.startTime = time.Now().Add(-10 * time.Minute)

	// Fill ring
	for i := 0; i < DefaultRingSize; i++ {
		tracker.RecordSnapshot(counterReader{
			otlpReceived:     uint64(1000 * (i + 1)),
			otlpSent:         uint64(999 * (i + 1)),
			prwReceived:      uint64(500 * (i + 1)),
			prwSent:          uint64(499 * (i + 1)),
			otlpBatchesSent:  uint64(100 * (i + 1)),
			otlpExportErrors: uint64(i),
			prwBatchesSent:   uint64(50 * (i + 1)),
			prwExportErrors:  0,
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		tracker.WriteSLIMetrics(rec)
	}
}
