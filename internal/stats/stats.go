package stats

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/szibis/metrics-governor/internal/cardinality"
	"github.com/szibis/metrics-governor/internal/intern"
	"github.com/szibis/metrics-governor/internal/logging"
	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
)

var (
	statsDegradationTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_stats_degradation_total",
		Help: "Total number of stats level degradations",
	})

	statsLevelCurrent = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_stats_level_current",
		Help: "Current stats level (0=none, 1=basic, 2=full)",
	})
)

func init() {
	prometheus.MustRegister(statsDegradationTotal)
	prometheus.MustRegister(statsLevelCurrent)
}

// statsLevelToNum converts a StatsLevel to a numeric value for Prometheus.
func statsLevelToNum(level StatsLevel) float64 {
	switch level {
	case StatsLevelFull:
		return 2
	case StatsLevelBasic:
		return 1
	default:
		return 0
	}
}

// StatsLevel defines the granularity of stats collection.
type StatsLevel string

const (
	// StatsLevelNone disables all stats collection. The collector will be nil.
	StatsLevelNone StatsLevel = "none"
	// StatsLevelBasic enables per-metric datapoint counts and pipeline timings.
	// Skips Bloom filters, series key extraction, and per-label cardinality.
	// CPU overhead: ~3-5% at 100k dps.
	StatsLevelBasic StatsLevel = "basic"
	// StatsLevelFull enables everything including Bloom-based cardinality tracking.
	// CPU overhead: ~30-40% at 100k dps.
	StatsLevelFull StatsLevel = "full"
)

// level constants for atomic operations (must match StatsLevel values).
const (
	levelNone  uint32 = 0
	levelBasic uint32 = 1
	levelFull  uint32 = 2
)

func levelFromString(s StatsLevel) uint32 {
	switch s {
	case StatsLevelFull:
		return levelFull
	case StatsLevelBasic:
		return levelBasic
	default:
		return levelNone
	}
}

func levelToString(l uint32) StatsLevel {
	switch l {
	case levelFull:
		return StatsLevelFull
	case levelBasic:
		return StatsLevelBasic
	default:
		return StatsLevelNone
	}
}

// Collector tracks cardinality and datapoints per metric and label combinations.
//
// Counter fields use atomic operations for lock-free updates from Record* methods.
// Counter groups are separated by cache line padding (128 bytes for ARM64) to
// prevent false sharing between goroutines writing to different counter groups.
// The mu lock protects only metricStats/labelStats map mutations.
type Collector struct {
	mu sync.RWMutex

	// Level controls what stats are collected.
	level StatsLevel

	// configuredLevel is the level set at creation time (never changes).
	configuredLevel StatsLevel

	// effectiveLevel is the current operational level, stored atomically for
	// lock-free reads in the hot path. Updated by Degrade().
	effectiveLevel atomic.Uint32

	// Labels to track for grouping (e.g., service, env, cluster)
	trackLabels []string

	// Per-metric stats: metric_name -> MetricStats (protected by mu)
	metricStats map[string]*MetricStats

	// Per-label-combination stats: "label1=val1,label2=val2" -> LabelStats (protected by mu)
	labelStats map[string]*LabelStats

	// Config knobs for full-mode tuning
	statsCardinalityThreshold int // Only create Bloom trackers for metrics with > N datapoints (0 = track all)
	statsMaxLabelCombinations int // Max label combo entries (0 = unlimited)

	// --- Group 1: Global counters (processFull goroutine) ---
	totalDatapoints atomic.Uint64
	totalMetrics    atomic.Uint64
	_pad1           [128]byte //nolint:unused // cache-line padding to prevent false sharing

	// --- Group 2: OTLP pipeline counters (exporter goroutine) ---
	datapointsReceived atomic.Uint64
	datapointsSent     atomic.Uint64
	batchesSent        atomic.Uint64
	exportErrors       atomic.Uint64
	_pad2              [128]byte //nolint:unused // cache-line padding to prevent false sharing

	// --- Group 3: PRW receiver counters (PRW receiver goroutines) ---
	prwDatapointsReceived atomic.Uint64
	prwTimeseriesReceived atomic.Uint64
	_pad3                 [128]byte //nolint:unused // cache-line padding to prevent false sharing

	// --- Group 4: PRW exporter counters (PRW exporter goroutine) ---
	prwDatapointsSent atomic.Uint64
	prwTimeseriesSent atomic.Uint64
	prwBatchesSent    atomic.Uint64
	prwExportErrors   atomic.Uint64
	_pad4             [128]byte //nolint:unused // cache-line padding to prevent false sharing

	// --- Group 5: OTLP byte counters (decompression goroutine) ---
	otlpBytesReceivedUncompressed atomic.Uint64
	otlpBytesReceivedCompressed   atomic.Uint64
	otlpBytesSentUncompressed     atomic.Uint64
	otlpBytesSentCompressed       atomic.Uint64
	_pad5                         [128]byte //nolint:unused // cache-line padding to prevent false sharing

	// --- Group 6: PRW byte counters (PRW decompression goroutine) ---
	prwBytesReceivedUncompressed atomic.Uint64
	prwBytesReceivedCompressed   atomic.Uint64
	prwBytesSentUncompressed     atomic.Uint64
	prwBytesSentCompressed       atomic.Uint64
	_pad6                        [128]byte //nolint:unused // cache-line padding to prevent false sharing

	// --- Group 7: Buffer sizes (infrequent updates) ---
	prwBufferSize  atomic.Int64
	otlpBufferSize atomic.Int64

	// SLI tracker for governor-computed SLI metrics (nil when disabled)
	sliTracker *SLITracker
}

// MetricStats holds stats for a single metric name.
type MetricStats struct {
	Name       string
	Datapoints uint64
	// Cardinality is tracked as unique series (metric + all attributes)
	cardinality cardinality.Tracker
}

// LabelStats holds stats for a label combination across all metrics.
type LabelStats struct {
	Labels     string
	Datapoints uint64
	// Cardinality: unique metric+series combinations for this label combo
	cardinality cardinality.Tracker
}

// NewCollector creates a new stats collector with the given level.
// For StatsLevelNone, callers should pass nil instead of creating a collector.
func NewCollector(trackLabels []string, level StatsLevel) *Collector {
	if level == "" {
		level = StatsLevelFull // backward-compatible default
	}
	c := &Collector{
		level:           level,
		configuredLevel: level,
		trackLabels:     trackLabels,
		metricStats:     make(map[string]*MetricStats),
		labelStats:      make(map[string]*LabelStats),
	}
	c.effectiveLevel.Store(levelFromString(level))
	statsLevelCurrent.Set(statsLevelToNum(level))
	return c
}

// SetStatsCardinalityThreshold sets the minimum datapoint count before creating a Bloom tracker.
// Metrics below the threshold get datapoint counting only. 0 = track all (default).
func (c *Collector) SetStatsCardinalityThreshold(threshold int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.statsCardinalityThreshold = threshold
}

// SetStatsMaxLabelCombinations sets the maximum number of label combination entries.
// When exceeded, no new LabelStats entries are created. 0 = unlimited (default).
func (c *Collector) SetStatsMaxLabelCombinations(max int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.statsMaxLabelCombinations = max
}

// Level returns the current effective stats collection level.
// This is safe for concurrent reads (uses atomic load).
func (c *Collector) Level() StatsLevel {
	return levelToString(c.effectiveLevel.Load())
}

// ConfiguredLevel returns the original stats level set at creation time.
func (c *Collector) ConfiguredLevel() StatsLevel {
	return c.configuredLevel
}

// Degrade drops the stats collection level by one step: full → basic → none.
// Returns true if degradation occurred, false if already at the lowest level.
// Existing collected data is preserved; new batches use the lower level.
// This is safe for concurrent use.
func (c *Collector) Degrade() bool {
	for {
		current := c.effectiveLevel.Load()
		if current == levelNone {
			return false
		}
		next := current - 1
		if c.effectiveLevel.CompareAndSwap(current, next) {
			newLevel := levelToString(next)
			oldLevel := levelToString(current)
			c.level = newLevel // Update for legacy code paths
			statsDegradationTotal.Inc()
			statsLevelCurrent.Set(statsLevelToNum(newLevel))
			logging.Warn("stats level degraded due to memory pressure",
				logging.F("from", string(oldLevel)),
				logging.F("to", string(newLevel)),
			)
			return true
		}
		// CAS failed (concurrent Degrade), retry
	}
}

// Process processes incoming metrics and updates stats.
// At basic level, only per-metric datapoint counts are tracked (no Bloom filters).
// At full level, Bloom-based cardinality and per-label stats are also tracked.
func (c *Collector) Process(resourceMetrics []*metricspb.ResourceMetrics) {
	level := c.effectiveLevel.Load()
	if level == levelNone {
		return // degraded to none — skip all stats
	}
	if level == levelBasic {
		c.processBasic(resourceMetrics)
		return
	}
	c.processFull(resourceMetrics)
}

// processBasic counts datapoints per metric name without Bloom filters or attribute extraction.
// CPU overhead: ~3-5% at 100k dps.
func (c *Collector) processBasic(resourceMetrics []*metricspb.ResourceMetrics) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, rm := range resourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				datapoints := c.countDatapoints(m)
				c.totalDatapoints.Add(uint64(datapoints))

				ms, ok := c.metricStats[m.Name]
				if !ok {
					ms = &MetricStats{Name: m.Name}
					c.metricStats[m.Name] = ms
					c.totalMetrics.Add(1)
				}
				ms.Datapoints += uint64(datapoints)
			}
		}
	}
}

// attrsPool pools map[string]string used in attribute extraction to reduce GC pressure.
var attrsPool = sync.Pool{New: func() any { m := make(map[string]string, 16); return &m }}

// getPooledMap gets a map from the pool (or allocates one).
func getPooledMap() map[string]string {
	mp := attrsPool.Get().(*map[string]string)
	return *mp
}

// putPooledMap returns a map to the pool after clearing it.
func putPooledMap(m map[string]string) {
	if m == nil || len(m) > 128 {
		return // Don't pool oversized maps
	}
	clear(m)
	attrsPool.Put(&m)
}

// precomputedDatapoint holds pre-extracted attributes and computed keys for a single datapoint.
type precomputedDatapoint struct {
	seriesKey []byte
	labelKey  string
}

// precompSlicePool pools []precomputedDatapoint slices to reduce per-processFull allocations.
var precompSlicePool = sync.Pool{New: func() any {
	s := make([]precomputedDatapoint, 0, 1024)
	return &s
}}

// concatBufPool pools []byte buffers used for label key + series key concatenation in Bloom filter Add.
var concatBufPool = sync.Pool{New: func() any {
	b := make([]byte, 0, 512)
	return &b
}}

// processFull runs the full cardinality tracking with Bloom filters and per-label stats.
//
// Optimization summary:
//   - Phase 1 uses dual-map key building (no mergeAttrs allocation)
//   - Series keys built directly as []byte via pooled buffers (no string→[]byte copy)
//   - Phase 2 uses per-metric lock scope (cardinality Add() outside collector lock)
//   - Pooled concat buffers for label+series key Bloom filter entries
func (c *Collector) processFull(resourceMetrics []*metricspb.ResourceMetrics) {
	// Phase 1: Pre-compute all attribute extraction and key building
	// outside the lock. Uses dual-map approach — no mergeAttrs allocation.
	type metricBatch struct {
		name       string
		datapoints int
		precomp    []precomputedDatapoint
	}

	var batches []metricBatch
	hasLabelTracking := len(c.trackLabels) > 0

	// Get a pooled precomp slice for reuse across metrics
	precompPooled := precompSlicePool.Get().(*[]precomputedDatapoint)

	for _, rm := range resourceMetrics {
		resourceAttrs := extractAttributes(rm.Resource.GetAttributes())

		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				dpCount := c.countDatapoints(m)
				if dpCount == 0 {
					batches = append(batches, metricBatch{name: m.Name})
					continue
				}

				dpAttrs := extractAllDatapointAttrs(m)

				// Reuse pooled slice, growing as needed
				precomp := (*precompPooled)[:0]
				if cap(precomp) < len(dpAttrs) {
					precomp = make([]precomputedDatapoint, 0, len(dpAttrs))
				}

				for _, dpAttr := range dpAttrs {
					// Build series key directly as []byte from dual maps (no merge, no string conversion)
					seriesKey := buildSeriesKeyDualBytes(resourceAttrs, dpAttr)

					var labelKey string
					if hasLabelTracking {
						labelKey = c.buildLabelKeyDual(resourceAttrs, dpAttr)
					}

					precomp = append(precomp, precomputedDatapoint{
						seriesKey: seriesKey,
						labelKey:  labelKey,
					})

					putPooledMap(dpAttr)
				}

				// Copy precomp to owned slice (so pooled slice can be reused for next metric)
				owned := make([]precomputedDatapoint, len(precomp))
				copy(owned, precomp)

				batches = append(batches, metricBatch{
					name:       m.Name,
					datapoints: dpCount,
					precomp:    owned,
				})
			}
		}
	}

	// Return pooled precomp slice
	if cap(*precompPooled) <= 4096 {
		*precompPooled = (*precompPooled)[:0]
		precompSlicePool.Put(precompPooled)
	}

	// Phase 2: Per-metric lock scope — hold lock only for map operations,
	// release between metrics so cardinality Add() runs outside lock.
	concatBufp := concatBufPool.Get().(*[]byte)
	concatBuf := *concatBufp

	for _, batch := range batches {
		c.totalDatapoints.Add(uint64(batch.datapoints))

		// Short lock: get-or-create MetricStats entry, update datapoint counter
		c.mu.Lock()
		ms, ok := c.metricStats[batch.name]
		if !ok {
			tracker := cardinality.NewTrackerFromGlobal()
			// Apply cardinality threshold: skip Bloom tracker for low-volume metrics
			if c.statsCardinalityThreshold > 0 && batch.datapoints < c.statsCardinalityThreshold {
				tracker = nil
			}
			ms = &MetricStats{
				Name:        batch.name,
				cardinality: tracker,
			}
			c.metricStats[batch.name] = ms
			c.totalMetrics.Add(1)
		}
		ms.Datapoints += uint64(batch.datapoints)
		c.mu.Unlock()

		// Cardinality updates with tracker's own lock (no Collector lock needed).
		// Bloom filter Add() hashes immediately and does not retain the input bytes.
		if ms.cardinality != nil {
			prefixBytes := []byte(batch.name + "|")
			for _, pc := range batch.precomp {
				ms.cardinality.Add(pc.seriesKey)

				// Per-label stats (if this datapoint has label tracking)
				if pc.labelKey != "" {
					c.mu.Lock()
					ls, ok := c.labelStats[pc.labelKey]
					if !ok {
						// Apply label combination cap
						if c.statsMaxLabelCombinations > 0 && len(c.labelStats) >= c.statsMaxLabelCombinations {
							c.mu.Unlock()
							continue
						}
						ls = &LabelStats{
							Labels:      pc.labelKey,
							cardinality: cardinality.NewTrackerFromGlobal(),
						}
						c.labelStats[pc.labelKey] = ls
					}
					ls.Datapoints++
					c.mu.Unlock()

					// Bloom filter Add for label cardinality (outside lock)
					concatBuf = concatBuf[:0]
					concatBuf = append(concatBuf, prefixBytes...)
					concatBuf = append(concatBuf, pc.seriesKey...)
					ls.cardinality.Add(concatBuf)
				}
			}
		} else {
			// No cardinality tracker, but still need label stats
			if hasLabelTracking {
				c.mu.Lock()
				for _, pc := range batch.precomp {
					if pc.labelKey != "" {
						ls, ok := c.labelStats[pc.labelKey]
						if !ok {
							if c.statsMaxLabelCombinations > 0 && len(c.labelStats) >= c.statsMaxLabelCombinations {
								continue
							}
							ls = &LabelStats{
								Labels:      pc.labelKey,
								cardinality: cardinality.NewTrackerFromGlobal(),
							}
							c.labelStats[pc.labelKey] = ls
						}
						ls.Datapoints++
					}
				}
				c.mu.Unlock()
			}
		}
	}

	// Return concat buffer to pool
	if cap(concatBuf) <= 4096 {
		*concatBufp = concatBuf
		concatBufPool.Put(concatBufp)
	}
}

// extractAllDatapointAttrs extracts attributes from all datapoints in a metric.
// Uses pooled maps to reduce allocation pressure.
func extractAllDatapointAttrs(m *metricspb.Metric) []map[string]string {
	switch d := m.Data.(type) {
	case *metricspb.Metric_Gauge:
		result := make([]map[string]string, len(d.Gauge.DataPoints))
		for i, dp := range d.Gauge.DataPoints {
			result[i] = extractAttributesPooled(dp.Attributes)
		}
		return result
	case *metricspb.Metric_Sum:
		result := make([]map[string]string, len(d.Sum.DataPoints))
		for i, dp := range d.Sum.DataPoints {
			result[i] = extractAttributesPooled(dp.Attributes)
		}
		return result
	case *metricspb.Metric_Histogram:
		result := make([]map[string]string, len(d.Histogram.DataPoints))
		for i, dp := range d.Histogram.DataPoints {
			result[i] = extractAttributesPooled(dp.Attributes)
		}
		return result
	case *metricspb.Metric_ExponentialHistogram:
		result := make([]map[string]string, len(d.ExponentialHistogram.DataPoints))
		for i, dp := range d.ExponentialHistogram.DataPoints {
			result[i] = extractAttributesPooled(dp.Attributes)
		}
		return result
	case *metricspb.Metric_Summary:
		result := make([]map[string]string, len(d.Summary.DataPoints))
		for i, dp := range d.Summary.DataPoints {
			result[i] = extractAttributesPooled(dp.Attributes)
		}
		return result
	}
	return nil
}

// extractAttributesPooled extracts attributes into a pooled map.
func extractAttributesPooled(attrs []*commonpb.KeyValue) map[string]string {
	result := getPooledMap()
	for _, kv := range attrs {
		if kv.Value != nil {
			if sv := kv.Value.GetStringValue(); sv != "" {
				result[kv.Key] = sv
			}
		}
	}
	return result
}

// buildLabelKeyFromAttrs builds a label combination key from merged attributes.
// This is the lock-free equivalent of buildLabelKey — reads only c.trackLabels
// which is immutable after construction.
func (c *Collector) buildLabelKeyFromAttrs(attrs map[string]string) string {
	var sb strings.Builder
	sb.Grow(len(c.trackLabels) * 24)
	first := true
	for _, label := range c.trackLabels {
		if val, ok := attrs[label]; ok {
			if !first {
				sb.WriteByte(',')
			}
			sb.WriteString(label)
			sb.WriteByte('=')
			sb.WriteString(val)
			first = false
		}
	}
	return sb.String()
}

// buildLabelKeyDual builds a label combination key from two maps (resource + dp attrs).
// dp attrs take priority over resource attrs. Reads only c.trackLabels (immutable).
func (c *Collector) buildLabelKeyDual(resAttrs, dpAttrs map[string]string) string {
	var sb strings.Builder
	sb.Grow(len(c.trackLabels) * 24)
	first := true
	for _, label := range c.trackLabels {
		val, ok := dpAttrs[label]
		if !ok {
			val, ok = resAttrs[label]
		}
		if ok {
			if !first {
				sb.WriteByte(',')
			}
			sb.WriteString(label)
			sb.WriteByte('=')
			sb.WriteString(val)
			first = false
		}
	}
	return sb.String()
}

// countDatapoints counts the number of datapoints in a metric.
func (c *Collector) countDatapoints(m *metricspb.Metric) int {
	switch d := m.Data.(type) {
	case *metricspb.Metric_Gauge:
		return len(d.Gauge.DataPoints)
	case *metricspb.Metric_Sum:
		return len(d.Sum.DataPoints)
	case *metricspb.Metric_Histogram:
		return len(d.Histogram.DataPoints)
	case *metricspb.Metric_ExponentialHistogram:
		return len(d.ExponentialHistogram.DataPoints)
	case *metricspb.Metric_Summary:
		return len(d.Summary.DataPoints)
	}
	return 0
}

// GetGlobalStats returns global statistics.
// At basic level, totalCardinality is always 0 (no Bloom filters allocated).
// Counter snapshots are best-effort (no cross-field consistency guarantee).
func (c *Collector) GetGlobalStats() (datapoints uint64, uniqueMetrics uint64, totalCardinality int64) {
	datapoints = c.totalDatapoints.Load()
	uniqueMetrics = c.totalMetrics.Load()

	if c.level == StatsLevelFull {
		c.mu.RLock()
		for _, ms := range c.metricStats {
			if ms.cardinality != nil {
				totalCardinality += ms.cardinality.Count()
			}
		}
		c.mu.RUnlock()
	}
	return
}

// RecordReceived records datapoints received (before any filtering).
// Lock-free: uses atomic counter.
func (c *Collector) RecordReceived(count int) {
	c.datapointsReceived.Add(uint64(count))
}

// RecordExport records a successful batch export.
// Lock-free: uses atomic counters.
func (c *Collector) RecordExport(datapointCount int) {
	c.batchesSent.Add(1)
	c.datapointsSent.Add(uint64(datapointCount))
}

// RecordExportError records a failed export attempt.
// Lock-free: uses atomic counter.
func (c *Collector) RecordExportError() {
	c.exportErrors.Add(1)
}

// PRW Stats methods - implements prw.PRWStatsCollector interface

// RecordPRWReceived records PRW datapoints and timeseries received.
// Lock-free: uses atomic counters.
func (c *Collector) RecordPRWReceived(datapointCount, timeseriesCount int) {
	c.prwDatapointsReceived.Add(uint64(datapointCount))
	c.prwTimeseriesReceived.Add(uint64(timeseriesCount))
}

// RecordPRWExport records a successful PRW export.
// Lock-free: uses atomic counters.
func (c *Collector) RecordPRWExport(datapointCount, timeseriesCount int) {
	c.prwBatchesSent.Add(1)
	c.prwDatapointsSent.Add(uint64(datapointCount))
	c.prwTimeseriesSent.Add(uint64(timeseriesCount))
}

// RecordPRWExportError records a failed PRW export attempt.
// Lock-free: uses atomic counter.
func (c *Collector) RecordPRWExportError() {
	c.prwExportErrors.Add(1)
}

// RecordPRWBytesReceived records uncompressed bytes received.
// Lock-free: uses atomic counter.
func (c *Collector) RecordPRWBytesReceived(bytes int) {
	c.prwBytesReceivedUncompressed.Add(uint64(bytes))
}

// RecordPRWBytesReceivedCompressed records compressed bytes received (on wire).
// Lock-free: uses atomic counter.
func (c *Collector) RecordPRWBytesReceivedCompressed(bytes int) {
	c.prwBytesReceivedCompressed.Add(uint64(bytes))
}

// RecordPRWBytesSent records uncompressed bytes sent.
// Lock-free: uses atomic counter.
func (c *Collector) RecordPRWBytesSent(bytes int) {
	c.prwBytesSentUncompressed.Add(uint64(bytes))
}

// RecordPRWBytesSentCompressed records compressed bytes sent (on wire).
// Lock-free: uses atomic counter.
func (c *Collector) RecordPRWBytesSentCompressed(bytes int) {
	c.prwBytesSentCompressed.Add(uint64(bytes))
}

// RecordOTLPBytesReceived records uncompressed OTLP bytes received.
// Lock-free: uses atomic counter.
func (c *Collector) RecordOTLPBytesReceived(bytes int) {
	c.otlpBytesReceivedUncompressed.Add(uint64(bytes))
}

// RecordOTLPBytesReceivedCompressed records compressed OTLP bytes received (on wire).
// Lock-free: uses atomic counter.
func (c *Collector) RecordOTLPBytesReceivedCompressed(bytes int) {
	c.otlpBytesReceivedCompressed.Add(uint64(bytes))
}

// RecordOTLPBytesSent records uncompressed OTLP bytes sent.
// Lock-free: uses atomic counter.
func (c *Collector) RecordOTLPBytesSent(bytes int) {
	c.otlpBytesSentUncompressed.Add(uint64(bytes))
}

// RecordOTLPBytesSentCompressed records compressed OTLP bytes sent (on wire).
// Lock-free: uses atomic counter.
func (c *Collector) RecordOTLPBytesSentCompressed(bytes int) {
	c.otlpBytesSentCompressed.Add(uint64(bytes))
}

// SetPRWBufferSize sets the current PRW buffer size.
// Lock-free: uses atomic store.
func (c *Collector) SetPRWBufferSize(size int) {
	c.prwBufferSize.Store(int64(size))
}

// SetOTLPBufferSize sets the current OTLP buffer size.
// Lock-free: uses atomic store.
func (c *Collector) SetOTLPBufferSize(size int) {
	c.otlpBufferSize.Store(int64(size))
}

// SetSLITracker sets the SLI tracker for governor-computed SLI metrics.
// Must be called before StartPeriodicLogging.
func (c *Collector) SetSLITracker(tracker *SLITracker) {
	c.sliTracker = tracker
}

// StartPeriodicLogging starts logging global stats every interval.
// It also resets cardinality tracking to prevent unbounded memory growth,
// and records SLI snapshots when the SLI tracker is configured.
func (c *Collector) StartPeriodicLogging(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Reset cardinality more frequently (every 60s) to bound memory
	resetTicker := time.NewTicker(60 * time.Second)
	defer resetTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			datapoints, uniqueMetrics, totalCardinality := c.GetGlobalStats()
			logging.Info("stats", logging.F(
				"datapoints_total", datapoints,
				"unique_metrics", uniqueMetrics,
				"total_cardinality", totalCardinality,
			))
			// Record SLI snapshot (lock-free counter reads)
			if c.sliTracker != nil {
				c.sliTracker.RecordSnapshot(counterReader{
					otlpReceived:     c.datapointsReceived.Load(),
					otlpSent:         c.datapointsSent.Load(),
					prwReceived:      c.prwDatapointsReceived.Load(),
					prwSent:          c.prwDatapointsSent.Load(),
					otlpBatchesSent:  c.batchesSent.Load(),
					otlpExportErrors: c.exportErrors.Load(),
					prwBatchesSent:   c.prwBatchesSent.Load(),
					prwExportErrors:  c.prwExportErrors.Load(),
				})
			}
		case <-resetTicker.C:
			c.ResetCardinality()
		}
	}
}

// ResetCardinality resets the cardinality tracking to prevent unbounded memory growth.
// Maps are always replaced to prevent slow accumulation of stale entries.
// At basic level, only resets the metric counts map (no Bloom filters or label stats exist).
func (c *Collector) ResetCardinality() {
	c.mu.Lock()
	defer c.mu.Unlock()

	prevMetrics := len(c.metricStats)
	prevLabels := len(c.labelStats)

	c.metricStats = make(map[string]*MetricStats)
	c.totalMetrics.Store(0)

	if c.level == StatsLevelFull {
		c.labelStats = make(map[string]*LabelStats)
	}

	if prevMetrics > 0 || prevLabels > 0 {
		logging.Info("stats maps reset", logging.F(
			"previous_metrics", prevMetrics,
			"previous_labels", prevLabels,
		))
	}

	// Reset intern pools if they've grown too large (only relevant at full level)
	if c.level == StatsLevelFull {
		const maxInternEntries = 100000
		intern.LabelNames.ResetIfLarge(maxInternEntries)
		intern.MetricNames.ResetIfLarge(maxInternEntries)
	}
}

// buildLabelKey builds a key from tracked labels.
func (c *Collector) buildLabelKey(attrs map[string]string) string {
	var sb strings.Builder
	first := true
	for _, label := range c.trackLabels {
		if val, ok := attrs[label]; ok {
			if !first {
				sb.WriteByte(',')
			}
			sb.WriteString(label)
			sb.WriteByte('=')
			sb.WriteString(val)
			first = false
		}
	}
	return sb.String()
}

// parseLabelKey parses a label key back to map.
func parseLabelKey(key string) map[string]string {
	result := make(map[string]string)
	parts := strings.Split(key, ",")
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) == 2 {
			result[kv[0]] = kv[1]
		}
	}
	return result
}

// ServeHTTP implements http.Handler for Prometheus metrics endpoint.
// Atomic counter snapshots are best-effort (no cross-field consistency guarantee).
// The mu.RLock is only held while iterating metricStats/labelStats maps.
func (c *Collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	// Snapshot atomic counters outside any lock
	totalDatapoints := c.totalDatapoints.Load()
	totalMetrics := c.totalMetrics.Load()
	dpReceived := c.datapointsReceived.Load()
	dpSent := c.datapointsSent.Load()
	bSent := c.batchesSent.Load()
	expErrors := c.exportErrors.Load()

	prwDpRecv := c.prwDatapointsReceived.Load()
	prwTsRecv := c.prwTimeseriesReceived.Load()
	prwDpSent := c.prwDatapointsSent.Load()
	prwTsSent := c.prwTimeseriesSent.Load()
	prwBSent := c.prwBatchesSent.Load()
	prwExpErr := c.prwExportErrors.Load()

	prwBytesRecvUncomp := c.prwBytesReceivedUncompressed.Load()
	prwBytesRecvComp := c.prwBytesReceivedCompressed.Load()
	prwBytesSentUncomp := c.prwBytesSentUncompressed.Load()
	prwBytesSentComp := c.prwBytesSentCompressed.Load()

	otlpBytesRecvUncomp := c.otlpBytesReceivedUncompressed.Load()
	otlpBytesRecvComp := c.otlpBytesReceivedCompressed.Load()
	otlpBytesSentUncomp := c.otlpBytesSentUncompressed.Load()
	otlpBytesSentComp := c.otlpBytesSentCompressed.Load()

	prwBufSize := c.prwBufferSize.Load()
	otlpBufSize := c.otlpBufferSize.Load()

	// Global stats
	fmt.Fprintf(w, "# HELP metrics_governor_datapoints_total Total number of datapoints processed\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_datapoints_total counter\n")
	fmt.Fprintf(w, "metrics_governor_datapoints_total %d\n", totalDatapoints)

	fmt.Fprintf(w, "# HELP metrics_governor_metrics_total Total number of unique metric names\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_metrics_total gauge\n")
	fmt.Fprintf(w, "metrics_governor_metrics_total %d\n", totalMetrics)

	// Export stats
	fmt.Fprintf(w, "# HELP metrics_governor_datapoints_received_total Total number of datapoints received (before filtering)\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_datapoints_received_total counter\n")
	fmt.Fprintf(w, "metrics_governor_datapoints_received_total %d\n", dpReceived)

	fmt.Fprintf(w, "# HELP metrics_governor_datapoints_sent_total Total number of datapoints sent to backend\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_datapoints_sent_total counter\n")
	fmt.Fprintf(w, "metrics_governor_datapoints_sent_total %d\n", dpSent)

	fmt.Fprintf(w, "# HELP metrics_governor_batches_sent_total Total number of batches exported\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_batches_sent_total counter\n")
	fmt.Fprintf(w, "metrics_governor_batches_sent_total %d\n", bSent)

	fmt.Fprintf(w, "# HELP metrics_governor_export_errors_total Total number of export errors\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_export_errors_total counter\n")
	fmt.Fprintf(w, "metrics_governor_export_errors_total %d\n", expErrors)

	// PRW stats
	fmt.Fprintf(w, "# HELP metrics_governor_prw_datapoints_received_total Total PRW datapoints received\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_prw_datapoints_received_total counter\n")
	fmt.Fprintf(w, "metrics_governor_prw_datapoints_received_total %d\n", prwDpRecv)

	fmt.Fprintf(w, "# HELP metrics_governor_prw_timeseries_received_total Total PRW timeseries received\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_prw_timeseries_received_total counter\n")
	fmt.Fprintf(w, "metrics_governor_prw_timeseries_received_total %d\n", prwTsRecv)

	fmt.Fprintf(w, "# HELP metrics_governor_prw_datapoints_sent_total Total PRW datapoints sent to backend\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_prw_datapoints_sent_total counter\n")
	fmt.Fprintf(w, "metrics_governor_prw_datapoints_sent_total %d\n", prwDpSent)

	fmt.Fprintf(w, "# HELP metrics_governor_prw_timeseries_sent_total Total PRW timeseries sent to backend\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_prw_timeseries_sent_total counter\n")
	fmt.Fprintf(w, "metrics_governor_prw_timeseries_sent_total %d\n", prwTsSent)

	fmt.Fprintf(w, "# HELP metrics_governor_prw_batches_sent_total Total PRW batches exported\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_prw_batches_sent_total counter\n")
	fmt.Fprintf(w, "metrics_governor_prw_batches_sent_total %d\n", prwBSent)

	fmt.Fprintf(w, "# HELP metrics_governor_prw_export_errors_total Total PRW export errors\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_prw_export_errors_total counter\n")
	fmt.Fprintf(w, "metrics_governor_prw_export_errors_total %d\n", prwExpErr)

	// PRW byte stats with compression labels
	fmt.Fprintf(w, "# HELP metrics_governor_prw_bytes_total Total PRW bytes by direction and compression state\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_prw_bytes_total counter\n")
	fmt.Fprintf(w, "metrics_governor_prw_bytes_total{direction=\"in\",compression=\"uncompressed\"} %d\n", prwBytesRecvUncomp)
	fmt.Fprintf(w, "metrics_governor_prw_bytes_total{direction=\"in\",compression=\"compressed\"} %d\n", prwBytesRecvComp)
	fmt.Fprintf(w, "metrics_governor_prw_bytes_total{direction=\"out\",compression=\"uncompressed\"} %d\n", prwBytesSentUncomp)
	fmt.Fprintf(w, "metrics_governor_prw_bytes_total{direction=\"out\",compression=\"compressed\"} %d\n", prwBytesSentComp)

	// OTLP byte stats with compression labels
	fmt.Fprintf(w, "# HELP metrics_governor_otlp_bytes_total Total OTLP bytes by direction and compression state\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_otlp_bytes_total counter\n")
	fmt.Fprintf(w, "metrics_governor_otlp_bytes_total{direction=\"in\",compression=\"uncompressed\"} %d\n", otlpBytesRecvUncomp)
	fmt.Fprintf(w, "metrics_governor_otlp_bytes_total{direction=\"in\",compression=\"compressed\"} %d\n", otlpBytesRecvComp)
	fmt.Fprintf(w, "metrics_governor_otlp_bytes_total{direction=\"out\",compression=\"uncompressed\"} %d\n", otlpBytesSentUncomp)
	fmt.Fprintf(w, "metrics_governor_otlp_bytes_total{direction=\"out\",compression=\"compressed\"} %d\n", otlpBytesSentComp)

	// Buffer size stats
	fmt.Fprintf(w, "# HELP metrics_governor_buffer_size Current buffer size by protocol\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_buffer_size gauge\n")
	fmt.Fprintf(w, "metrics_governor_buffer_size{protocol=\"prw\"} %d\n", prwBufSize)
	fmt.Fprintf(w, "metrics_governor_buffer_size{protocol=\"otlp\"} %d\n", otlpBufSize)

	// Per-metric stats (available at basic and full levels) — needs map lock
	c.mu.RLock()

	fmt.Fprintf(w, "# HELP metrics_governor_metric_datapoints_total Datapoints per metric name\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_metric_datapoints_total counter\n")
	for name, ms := range c.metricStats {
		fmt.Fprintf(w, "metrics_governor_metric_datapoints_total{metric_name=%q} %d\n", name, ms.Datapoints)
	}

	// Cardinality and label stats (full level only — requires Bloom filters)
	if c.level == StatsLevelFull {
		fmt.Fprintf(w, "# HELP metrics_governor_metric_cardinality Cardinality (unique series) per metric name\n")
		fmt.Fprintf(w, "# TYPE metrics_governor_metric_cardinality gauge\n")
		for name, ms := range c.metricStats {
			if ms.cardinality != nil {
				fmt.Fprintf(w, "metrics_governor_metric_cardinality{metric_name=%q} %d\n", name, ms.cardinality.Count())
			}
		}

		// Per-label-combination stats
		if len(c.labelStats) > 0 {
			fmt.Fprintf(w, "# HELP metrics_governor_label_datapoints_total Datapoints per label combination\n")
			fmt.Fprintf(w, "# TYPE metrics_governor_label_datapoints_total counter\n")
			for _, ls := range c.labelStats {
				labels := parseLabelKey(ls.Labels)
				labelStr := formatLabels(labels)
				fmt.Fprintf(w, "metrics_governor_label_datapoints_total{%s} %d\n", labelStr, ls.Datapoints)
			}

			fmt.Fprintf(w, "# HELP metrics_governor_label_cardinality Cardinality per label combination\n")
			fmt.Fprintf(w, "# TYPE metrics_governor_label_cardinality gauge\n")
			for _, ls := range c.labelStats {
				labels := parseLabelKey(ls.Labels)
				labelStr := formatLabels(labels)
				fmt.Fprintf(w, "metrics_governor_label_cardinality{%s} %d\n", labelStr, ls.cardinality.Count())
			}
		}

		// Cardinality tracking metrics (Bloom filter observability)
		var totalMemoryBytes uint64
		trackerCount := len(c.metricStats) + len(c.labelStats)
		for _, ms := range c.metricStats {
			if ms.cardinality != nil {
				totalMemoryBytes += ms.cardinality.MemoryUsage()
			}
		}
		for _, ls := range c.labelStats {
			totalMemoryBytes += ls.cardinality.MemoryUsage()
		}

		fmt.Fprintf(w, "# HELP metrics_governor_cardinality_trackers_total Number of active cardinality trackers\n")
		fmt.Fprintf(w, "# TYPE metrics_governor_cardinality_trackers_total gauge\n")
		fmt.Fprintf(w, "metrics_governor_cardinality_trackers_total %d\n", trackerCount)

		fmt.Fprintf(w, "# HELP metrics_governor_cardinality_memory_bytes Total memory used by cardinality trackers\n")
		fmt.Fprintf(w, "# TYPE metrics_governor_cardinality_memory_bytes gauge\n")
		fmt.Fprintf(w, "metrics_governor_cardinality_memory_bytes %d\n", totalMemoryBytes)

		fmt.Fprintf(w, "# HELP metrics_governor_cardinality_mode Cardinality tracking mode (1=active)\n")
		fmt.Fprintf(w, "# TYPE metrics_governor_cardinality_mode gauge\n")
		switch cardinality.GlobalConfig.Mode {
		case cardinality.ModeBloom:
			fmt.Fprintf(w, "metrics_governor_cardinality_mode{mode=\"bloom\"} 1\n")
		case cardinality.ModeExact:
			fmt.Fprintf(w, "metrics_governor_cardinality_mode{mode=\"exact\"} 1\n")
		case cardinality.ModeHybrid:
			fmt.Fprintf(w, "metrics_governor_cardinality_mode{mode=\"hybrid\"} 1\n")
		}

		fmt.Fprintf(w, "# HELP metrics_governor_cardinality_config_expected_items Configured expected items per tracker\n")
		fmt.Fprintf(w, "# TYPE metrics_governor_cardinality_config_expected_items gauge\n")
		fmt.Fprintf(w, "metrics_governor_cardinality_config_expected_items %d\n", cardinality.GlobalConfig.ExpectedItems)

		fmt.Fprintf(w, "# HELP metrics_governor_cardinality_config_fp_rate Configured false positive rate for Bloom filter\n")
		fmt.Fprintf(w, "# TYPE metrics_governor_cardinality_config_fp_rate gauge\n")
		fmt.Fprintf(w, "metrics_governor_cardinality_config_fp_rate %f\n", cardinality.GlobalConfig.FalsePositiveRate)

		// Series key pool metrics
		fmt.Fprintf(w, "# HELP metrics_governor_serieskey_pool_gets_total Pool.Get() calls for series key slices\n")
		fmt.Fprintf(w, "# TYPE metrics_governor_serieskey_pool_gets_total counter\n")
		fmt.Fprintf(w, "metrics_governor_serieskey_pool_gets_total %d\n", seriesKeyPoolGets.Load())

		fmt.Fprintf(w, "# HELP metrics_governor_serieskey_pool_puts_total Pool.Put() calls for series key slices\n")
		fmt.Fprintf(w, "# TYPE metrics_governor_serieskey_pool_puts_total counter\n")
		fmt.Fprintf(w, "metrics_governor_serieskey_pool_puts_total %d\n", seriesKeyPoolPuts.Load())

		fmt.Fprintf(w, "# HELP metrics_governor_serieskey_pool_discards_total Series key slices discarded (too large for pool)\n")
		fmt.Fprintf(w, "# TYPE metrics_governor_serieskey_pool_discards_total counter\n")
		fmt.Fprintf(w, "metrics_governor_serieskey_pool_discards_total %d\n", seriesKeyPoolDiscards.Load())
	}

	c.mu.RUnlock()

	// Emit stats level gauge so dashboards/alerts can detect the mode
	fmt.Fprintf(w, "# HELP metrics_governor_stats_level Current stats collection level (1=active)\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_stats_level gauge\n")
	fmt.Fprintf(w, "metrics_governor_stats_level{level=%q} 1\n", string(c.level))

	// Write SLI metrics (separate lock scope — no contention with hot path)
	if c.sliTracker != nil {
		c.sliTracker.WriteSLIMetrics(w)
	}
}

// formatLabels formats a label map as Prometheus label string.
func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	// Estimate capacity: key="value", ~25 chars per label
	sb.Grow(len(keys) * 25)
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(k)
		sb.WriteString(`="`)
		sb.WriteString(labels[k])
		sb.WriteByte('"')
	}
	return sb.String()
}

// Helper functions

func extractAttributes(attrs []*commonpb.KeyValue) map[string]string {
	result := make(map[string]string, len(attrs))
	for _, kv := range attrs {
		if kv.Value != nil {
			if sv := kv.Value.GetStringValue(); sv != "" {
				result[kv.Key] = sv
			}
		}
	}
	return result
}

// keysPool pools []string slices used for sorting attribute keys in series key building.
var keysPool = sync.Pool{New: func() any { s := make([]string, 0, 16); return &s }}

// keyBufPool pools []byte buffers used for building series keys directly as []byte.
var keyBufPool = sync.Pool{New: func() any {
	b := make([]byte, 0, 256)
	return &b
}}

var (
	seriesKeyPoolGets     atomic.Int64
	seriesKeyPoolPuts     atomic.Int64
	seriesKeyPoolDiscards atomic.Int64
)

// buildSeriesKey builds a series key string from a single attribute map.
// Retained for backward compatibility (tests, basic-mode callers).
func buildSeriesKey(attrs map[string]string) string {
	if len(attrs) == 0 {
		return ""
	}

	keysp := keysPool.Get().(*[]string)
	seriesKeyPoolGets.Add(1)
	keys := (*keysp)[:0]

	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.Grow(len(keys) * 32)
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(attrs[k])
	}

	if cap(keys) > 64 {
		seriesKeyPoolDiscards.Add(1)
	} else {
		*keysp = keys
		keysPool.Put(keysp)
		seriesKeyPoolPuts.Add(1)
	}

	return sb.String()
}

// buildSeriesKeyDualBytes builds a series key directly as []byte from two attribute maps.
// dp attrs take priority over resource attrs (same semantics as mergeAttrs + buildSeriesKey).
// Returns an owned []byte — the caller owns the result.
//
// This eliminates both the mergeAttrs map allocation and the string→[]byte conversion
// that previously occurred in processFull.
func buildSeriesKeyDualBytes(resAttrs, dpAttrs map[string]string) []byte {
	if len(resAttrs) == 0 && len(dpAttrs) == 0 {
		return nil
	}

	// Collect unique keys (dp overrides resource)
	kp := keysPool.Get().(*[]string)
	seriesKeyPoolGets.Add(1)
	keys := (*kp)[:0]

	for k := range resAttrs {
		keys = append(keys, k)
	}
	for k := range dpAttrs {
		if _, exists := resAttrs[k]; !exists {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	// Build key into pooled byte buffer
	bp := keyBufPool.Get().(*[]byte)
	buf := (*bp)[:0]
	for i, k := range keys {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, k...)
		buf = append(buf, '=')
		if v, ok := dpAttrs[k]; ok {
			buf = append(buf, v...) // dp attrs take priority
		} else {
			buf = append(buf, resAttrs[k]...)
		}
	}

	// Copy to owned []byte with exact size (buffer returns to pool)
	result := make([]byte, len(buf))
	copy(result, buf)

	// Return pooled buffers
	if cap(*bp) <= 4096 {
		*bp = buf
		keyBufPool.Put(bp)
	}
	if cap(keys) > 64 {
		seriesKeyPoolDiscards.Add(1)
	} else {
		*kp = keys
		keysPool.Put(kp)
		seriesKeyPoolPuts.Add(1)
	}

	return result
}
