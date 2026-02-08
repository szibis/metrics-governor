package exporter

import (
	"net/http"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/szibis/metrics-governor/internal/logging"
)

var (
	batchCurrentMaxBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_batch_current_max_bytes",
		Help: "Current batch max bytes (auto-tuned or static)",
	})

	batchHardCeilingBytes = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_batch_hard_ceiling_bytes",
		Help: "Hard ceiling for batch bytes discovered via HTTP 413 (0 = no ceiling)",
	})

	batchTuningAdjustmentsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_batch_tuning_adjustments_total",
		Help: "Total batch tuning adjustments by direction",
	}, []string{"direction"})

	batchSuccessStreak = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_batch_success_streak",
		Help: "Current consecutive successful export streak",
	})
)

func init() {
	prometheus.MustRegister(batchCurrentMaxBytes)
	prometheus.MustRegister(batchHardCeilingBytes)
	prometheus.MustRegister(batchTuningAdjustmentsTotal)
	prometheus.MustRegister(batchSuccessStreak)

	batchCurrentMaxBytes.Set(0)
	batchHardCeilingBytes.Set(0)
	batchTuningAdjustmentsTotal.WithLabelValues("up").Add(0)
	batchTuningAdjustmentsTotal.WithLabelValues("down").Add(0)
	batchSuccessStreak.Set(0)
}

// BatchTunerConfig holds configuration for the AIMD batch auto-tuner.
type BatchTunerConfig struct {
	// Enabled activates batch auto-tuning.
	Enabled bool
	// MinBytes is the minimum batch size in bytes (default: 512).
	MinBytes int
	// MaxBytes is the maximum batch size in bytes (default: 16MB).
	MaxBytes int
	// SuccessStreak is the number of consecutive successes before growing (default: 10).
	SuccessStreak int
	// GrowFactor is the multiplicative growth factor (default: 1.25 = 25% growth).
	GrowFactor float64
	// ShrinkFactor is the multiplicative shrink factor on failure (default: 0.5 = 50% shrink).
	ShrinkFactor float64
}

// BatchTuner implements AIMD (Additive Increase / Multiplicative Decrease) for batch sizing.
// All methods are safe for concurrent use via atomic operations.
type BatchTuner struct {
	currentMaxBytes atomic.Int64
	hardCeiling     atomic.Int64 // discovered via HTTP 413; 0 = no ceiling
	consecutiveOK   atomic.Int32

	// Metrics
	adjustmentsUp   atomic.Int64
	adjustmentsDown atomic.Int64

	// Configuration (immutable after creation)
	minBytes      int
	maxBytes      int
	successStreak int
	growFactor    float64
	shrinkFactor  float64
}

// NewBatchTuner creates a new AIMD batch tuner.
func NewBatchTuner(cfg BatchTunerConfig) *BatchTuner {
	if cfg.MinBytes <= 0 {
		cfg.MinBytes = 512
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = 16 * 1024 * 1024 // 16MB
	}
	if cfg.SuccessStreak <= 0 {
		cfg.SuccessStreak = 10
	}
	if cfg.GrowFactor <= 1.0 {
		cfg.GrowFactor = 1.25
	}
	if cfg.ShrinkFactor <= 0 || cfg.ShrinkFactor >= 1.0 {
		cfg.ShrinkFactor = 0.5
	}

	bt := &BatchTuner{
		minBytes:      cfg.MinBytes,
		maxBytes:      cfg.MaxBytes,
		successStreak: cfg.SuccessStreak,
		growFactor:    cfg.GrowFactor,
		shrinkFactor:  cfg.ShrinkFactor,
	}
	bt.currentMaxBytes.Store(int64(cfg.MaxBytes))
	batchCurrentMaxBytes.Set(float64(cfg.MaxBytes))

	logging.Info("batch auto-tuner initialized", logging.F(
		"min_bytes", cfg.MinBytes,
		"max_bytes", cfg.MaxBytes,
		"success_streak", cfg.SuccessStreak,
		"grow_factor", cfg.GrowFactor,
		"shrink_factor", cfg.ShrinkFactor,
	))

	return bt
}

// CurrentMaxBytes returns the current effective max batch bytes (lock-free atomic read).
func (bt *BatchTuner) CurrentMaxBytes() int {
	return int(bt.currentMaxBytes.Load())
}

// RecordSuccess records a successful export. After SuccessStreak consecutive successes,
// the batch size is increased by GrowFactor (additive increase).
func (bt *BatchTuner) RecordSuccess(_ time.Duration, _, _ int) {
	streak := bt.consecutiveOK.Add(1)
	batchSuccessStreak.Set(float64(streak))

	if int(streak) >= bt.successStreak {
		// Grow: additive increase
		bt.consecutiveOK.Store(0)
		batchSuccessStreak.Set(0)

		current := bt.currentMaxBytes.Load()
		newMax := int64(float64(current) * bt.growFactor)

		// Enforce hard ceiling if discovered
		ceiling := bt.hardCeiling.Load()
		if ceiling > 0 && newMax > ceiling {
			newMax = ceiling
		}

		// Enforce configured maximum
		if newMax > int64(bt.maxBytes) {
			newMax = int64(bt.maxBytes)
		}

		if newMax != current {
			bt.currentMaxBytes.Store(newMax)
			bt.adjustmentsUp.Add(1)
			batchCurrentMaxBytes.Set(float64(newMax))
			batchTuningAdjustmentsTotal.WithLabelValues("up").Inc()

			logging.Info("batch tuner: increased max bytes", logging.F(
				"old_bytes", current,
				"new_bytes", newMax,
			))
		}
	}
}

// RecordFailure records a failed export. The batch size is immediately shrunk by
// ShrinkFactor (multiplicative decrease). If the error is an HTTP 413, a hard
// ceiling is set at 80% of the current max.
func (bt *BatchTuner) RecordFailure(err error) {
	bt.consecutiveOK.Store(0)
	batchSuccessStreak.Set(0)

	current := bt.currentMaxBytes.Load()

	// Check for HTTP 413 → set hard ceiling
	if is413(err) {
		ceiling := int64(float64(current) * 0.8)
		if ceiling < int64(bt.minBytes) {
			ceiling = int64(bt.minBytes)
		}
		bt.hardCeiling.Store(ceiling)
		batchHardCeilingBytes.Set(float64(ceiling))

		logging.Warn("batch tuner: HTTP 413 detected, setting hard ceiling", logging.F(
			"ceiling_bytes", ceiling,
			"current_bytes", current,
		))
	}

	// Shrink: multiplicative decrease
	newMax := int64(float64(current) * bt.shrinkFactor)

	// Enforce minimum
	if newMax < int64(bt.minBytes) {
		newMax = int64(bt.minBytes)
	}

	if newMax != current {
		bt.currentMaxBytes.Store(newMax)
		bt.adjustmentsDown.Add(1)
		batchCurrentMaxBytes.Set(float64(newMax))
		batchTuningAdjustmentsTotal.WithLabelValues("down").Inc()

		logging.Info("batch tuner: decreased max bytes", logging.F(
			"old_bytes", current,
			"new_bytes", newMax,
		))
	}
}

// is413 checks if an error indicates an HTTP 413 Payload Too Large response.
func is413(err error) bool {
	if err == nil {
		return false
	}
	// Check for ExportError with 413 status code
	if ee, ok := err.(*ExportError); ok {
		return ee.StatusCode == http.StatusRequestEntityTooLarge
	}
	return false
}
