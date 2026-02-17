package stats

import (
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"
)

// Default SLI configuration values.
const (
	DefaultSLIEnabled       = true
	DefaultDeliveryTarget   = 0.999
	DefaultExportTarget     = 0.995
	DefaultBudgetWindow     = 720 * time.Hour // 30 days
	DefaultSnapshotInterval = 30 * time.Second
	DefaultRingSize         = 720 // 6h at 30s intervals
)

// SLI window durations for ratio/burn-rate computation.
var sliWindows = []struct {
	Label string
	Slots int
}{
	{"5m", 10},  // 10 × 30s = 5 min
	{"30m", 60}, // 60 × 30s = 30 min
	{"1h", 120}, // 120 × 30s = 1 hour
	{"6h", 720}, // 720 × 30s = 6 hours
}

// SLIConfig holds SLI/SLO configuration.
type SLIConfig struct {
	Enabled        bool
	DeliveryTarget float64
	ExportTarget   float64
	BudgetWindow   time.Duration
}

// sliSnapshot holds counter values at a point in time.
type sliSnapshot struct {
	timestamp        int64 // unix seconds
	otlpReceived     uint64
	otlpSent         uint64
	prwReceived      uint64
	prwSent          uint64
	intentionalDrops int64 // from limits enforcer TotalDropped()
	otlpBatchesSent  uint64
	otlpExportErrors uint64
	prwBatchesSent   uint64
	prwExportErrors  uint64
}

// SLITracker computes SLI ratios, burn rates, and error budgets from
// periodic counter snapshots stored in a fixed-size ring buffer.
//
// Thread safety:
//   - Single writer: RecordSnapshot called from periodic loop under mu.Lock
//   - Concurrent readers: WriteSLIMetrics under mu.RLock
//   - Independent from Collector.mu — no contention with hot processing path
type SLITracker struct {
	mu     sync.RWMutex
	config SLIConfig

	ring  []sliSnapshot // fixed-size ring buffer
	head  int           // next write position
	count int           // number of valid entries (up to len(ring))

	startSnapshot *sliSnapshot // first-ever snapshot for error budget baseline
	startTime     time.Time    // when tracking started

	dropsProvider func() int64 // injected from limits enforcer

	snapshotsTotal uint64 // total snapshots recorded
}

// NewSLITracker creates a new SLI tracker.
// dropsProvider returns intentional drops from the limits enforcer;
// pass nil if no limits enforcer is configured.
func NewSLITracker(cfg SLIConfig, dropsProvider func() int64) *SLITracker {
	if dropsProvider == nil {
		dropsProvider = func() int64 { return 0 }
	}
	return &SLITracker{
		config:        cfg,
		ring:          make([]sliSnapshot, DefaultRingSize),
		dropsProvider: dropsProvider,
		startTime:     time.Now(),
	}
}

// counterReader provides atomic counter values from the Collector.
// This decouples the SLI tracker from the Collector struct.
type counterReader struct {
	otlpReceived     uint64
	otlpSent         uint64
	prwReceived      uint64
	prwSent          uint64
	otlpBatchesSent  uint64
	otlpExportErrors uint64
	prwBatchesSent   uint64
	prwExportErrors  uint64
}

// RecordSnapshot captures a point-in-time snapshot of all counters.
// Called every 30s from the periodic logging goroutine.
func (t *SLITracker) RecordSnapshot(r counterReader) {
	now := time.Now().Unix()
	drops := t.dropsProvider()

	snap := sliSnapshot{
		timestamp:        now,
		otlpReceived:     r.otlpReceived,
		otlpSent:         r.otlpSent,
		prwReceived:      r.prwReceived,
		prwSent:          r.prwSent,
		intentionalDrops: drops,
		otlpBatchesSent:  r.otlpBatchesSent,
		otlpExportErrors: r.otlpExportErrors,
		prwBatchesSent:   r.prwBatchesSent,
		prwExportErrors:  r.prwExportErrors,
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.startSnapshot == nil {
		cp := snap
		t.startSnapshot = &cp
	}

	t.ring[t.head] = snap
	t.head = (t.head + 1) % len(t.ring)
	if t.count < len(t.ring) {
		t.count++
	}
	t.snapshotsTotal++
}

// getSnapshotAt returns the snapshot at the given number of slots back from head.
// Must be called under mu.RLock.
func (t *SLITracker) getSnapshotAt(slotsBack int) (sliSnapshot, bool) {
	if slotsBack >= t.count {
		return sliSnapshot{}, false
	}
	idx := (t.head - 1 - slotsBack + len(t.ring)) % len(t.ring)
	return t.ring[idx], true
}

// latest returns the most recent snapshot. Must be called under mu.RLock.
func (t *SLITracker) latest() (sliSnapshot, bool) {
	return t.getSnapshotAt(0)
}

// computeDeliveryRatio computes the delivery SLI ratio over a window.
// delivery_ratio = good / eligible
// good = delta(otlpSent + prwSent)
// eligible = delta(otlpReceived + prwReceived) - delta(intentionalDrops)
func computeDeliveryRatio(newer, older sliSnapshot) float64 {
	good := float64(newer.otlpSent-older.otlpSent) +
		float64(newer.prwSent-older.prwSent)
	totalReceived := float64(newer.otlpReceived-older.otlpReceived) +
		float64(newer.prwReceived-older.prwReceived)
	drops := float64(newer.intentionalDrops - older.intentionalDrops)
	eligible := totalReceived - drops

	if eligible <= 0 {
		return 1.0 // no eligible events = perfect
	}
	ratio := good / eligible
	if ratio > 1.0 {
		return 1.0
	}
	if ratio < 0.0 {
		return 0.0
	}
	return ratio
}

// computeExportSuccessRatio computes the export success SLI ratio.
// export_success = good / total
// good = delta(otlpBatchesSent + prwBatchesSent)
// total = good + delta(otlpExportErrors + prwExportErrors)
func computeExportSuccessRatio(newer, older sliSnapshot) float64 {
	good := float64(newer.otlpBatchesSent-older.otlpBatchesSent) +
		float64(newer.prwBatchesSent-older.prwBatchesSent)
	errors := float64(newer.otlpExportErrors-older.otlpExportErrors) +
		float64(newer.prwExportErrors-older.prwExportErrors)
	total := good + errors

	if total <= 0 {
		return 1.0 // no batches = perfect
	}
	ratio := good / total
	if ratio > 1.0 {
		return 1.0
	}
	if ratio < 0.0 {
		return 0.0
	}
	return ratio
}

// computeBurnRate computes the burn rate: how fast the error budget is being consumed.
// burn_rate = actual_error_rate / allowed_error_rate
// A burn rate of 1.0 means the SLO pace is exactly sustainable.
// 14.4x means the budget will be exhausted in ~2 days (30d / 14.4 ≈ 2.08d).
func computeBurnRate(ratio, target float64) float64 {
	allowedErrorRate := 1.0 - target
	if allowedErrorRate <= 0 {
		return 0.0 // target=1.0 means no errors allowed, can't compute
	}
	actualErrorRate := 1.0 - ratio
	return actualErrorRate / allowedErrorRate
}

// computeErrorBudgetRemaining computes the fraction of error budget remaining.
// Uses startSnapshot → latest, projected over the configured budget window.
//
// budget_remaining = 1 - (consumed_errors / total_budget)
// total_budget = (1 - target) × eligible_events_projected_over_budget_window
func (t *SLITracker) computeErrorBudgetRemaining(
	start, latest sliSnapshot,
	ratioFn func(newer, older sliSnapshot) float64,
	target float64,
) float64 {
	elapsed := time.Since(t.startTime).Seconds()
	if elapsed < 60 {
		return 1.0 // not enough data
	}

	ratio := ratioFn(latest, start)
	actualErrorRate := 1.0 - ratio
	allowedErrorRate := 1.0 - target
	if allowedErrorRate <= 0 {
		return 1.0
	}

	// Consumed fraction of budget = actual_error_rate / allowed_error_rate
	// Projected over budget window, but we only have data for elapsed time.
	// budget_remaining = 1 - (actual_error_rate / allowed_error_rate)
	consumed := actualErrorRate / allowedErrorRate
	remaining := 1.0 - consumed

	// Clamp to [0, 1]
	if remaining > 1.0 {
		return 1.0
	}
	if remaining < 0.0 {
		return 0.0
	}
	return remaining
}

// WriteSLIMetrics writes all SLI Prometheus metrics to the response writer.
// Called from Collector.ServeHTTP under a separate read lock.
func (t *SLITracker) WriteSLIMetrics(w http.ResponseWriter) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	now := t.latest
	latestSnap, hasLatest := now()
	if !hasLatest || t.count < 2 {
		// Not enough data yet — emit config metrics only
		t.writeConfigMetrics(w)
		return
	}

	// SLI ratios and burn rates for each window
	fmt.Fprintf(w, "# HELP metrics_governor_sli_delivery_ratio Delivery SLI ratio (good/eligible) over window\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_sli_delivery_ratio gauge\n")
	for _, win := range sliWindows {
		older, ok := t.getSnapshotAt(win.Slots - 1)
		if !ok {
			continue
		}
		ratio := computeDeliveryRatio(latestSnap, older)
		fmt.Fprintf(w, "metrics_governor_sli_delivery_ratio{window=%q} %g\n", win.Label, ratio)
	}

	fmt.Fprintf(w, "# HELP metrics_governor_sli_export_success_ratio Export success SLI ratio (good/total) over window\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_sli_export_success_ratio gauge\n")
	for _, win := range sliWindows {
		older, ok := t.getSnapshotAt(win.Slots - 1)
		if !ok {
			continue
		}
		ratio := computeExportSuccessRatio(latestSnap, older)
		fmt.Fprintf(w, "metrics_governor_sli_export_success_ratio{window=%q} %g\n", win.Label, ratio)
	}

	fmt.Fprintf(w, "# HELP metrics_governor_sli_delivery_burn_rate Delivery SLI burn rate (1.0 = at SLO pace)\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_sli_delivery_burn_rate gauge\n")
	for _, win := range sliWindows {
		older, ok := t.getSnapshotAt(win.Slots - 1)
		if !ok {
			continue
		}
		ratio := computeDeliveryRatio(latestSnap, older)
		burnRate := computeBurnRate(ratio, t.config.DeliveryTarget)
		fmt.Fprintf(w, "metrics_governor_sli_delivery_burn_rate{window=%q} %g\n", win.Label, burnRate)
	}

	fmt.Fprintf(w, "# HELP metrics_governor_sli_export_burn_rate Export SLI burn rate (1.0 = at SLO pace)\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_sli_export_burn_rate gauge\n")
	for _, win := range sliWindows {
		older, ok := t.getSnapshotAt(win.Slots - 1)
		if !ok {
			continue
		}
		ratio := computeExportSuccessRatio(latestSnap, older)
		burnRate := computeBurnRate(ratio, t.config.ExportTarget)
		fmt.Fprintf(w, "metrics_governor_sli_export_burn_rate{window=%q} %g\n", win.Label, burnRate)
	}

	// Error budget remaining
	if t.startSnapshot != nil {
		fmt.Fprintf(w, "# HELP metrics_governor_sli_delivery_budget_remaining Fraction of delivery error budget remaining (0-1)\n")
		fmt.Fprintf(w, "# TYPE metrics_governor_sli_delivery_budget_remaining gauge\n")
		deliveryBudget := t.computeErrorBudgetRemaining(
			*t.startSnapshot, latestSnap,
			computeDeliveryRatio, t.config.DeliveryTarget,
		)
		fmt.Fprintf(w, "metrics_governor_sli_delivery_budget_remaining %g\n", deliveryBudget)

		fmt.Fprintf(w, "# HELP metrics_governor_sli_export_budget_remaining Fraction of export error budget remaining (0-1)\n")
		fmt.Fprintf(w, "# TYPE metrics_governor_sli_export_budget_remaining gauge\n")
		exportBudget := t.computeErrorBudgetRemaining(
			*t.startSnapshot, latestSnap,
			computeExportSuccessRatio, t.config.ExportTarget,
		)
		fmt.Fprintf(w, "metrics_governor_sli_export_budget_remaining %g\n", exportBudget)
	}

	t.writeConfigMetrics(w)
}

// writeConfigMetrics writes SLI configuration visibility metrics.
// Must be called under mu.RLock.
func (t *SLITracker) writeConfigMetrics(w http.ResponseWriter) {
	fmt.Fprintf(w, "# HELP metrics_governor_slo_target Configured SLO target value\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_slo_target gauge\n")
	fmt.Fprintf(w, "metrics_governor_slo_target{sli=\"delivery\"} %g\n", t.config.DeliveryTarget)
	fmt.Fprintf(w, "metrics_governor_slo_target{sli=\"export\"} %g\n", t.config.ExportTarget)

	fmt.Fprintf(w, "# HELP metrics_governor_sli_uptime_seconds Seconds since SLI tracking started\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_sli_uptime_seconds gauge\n")
	fmt.Fprintf(w, "metrics_governor_sli_uptime_seconds %g\n", math.Floor(time.Since(t.startTime).Seconds()))

	fmt.Fprintf(w, "# HELP metrics_governor_sli_snapshots_total Total SLI snapshots recorded\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_sli_snapshots_total counter\n")
	fmt.Fprintf(w, "metrics_governor_sli_snapshots_total %d\n", t.snapshotsTotal)
}
