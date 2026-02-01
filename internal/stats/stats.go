package stats

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/szibis/metrics-governor/internal/intern"
	"github.com/szibis/metrics-governor/internal/logging"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// Collector tracks cardinality and datapoints per metric and label combinations.
type Collector struct {
	mu sync.RWMutex

	// Labels to track for grouping (e.g., service, env, cluster)
	trackLabels []string

	// Per-metric stats: metric_name -> MetricStats
	metricStats map[string]*MetricStats

	// Per-label-combination stats: "label1=val1,label2=val2" -> LabelStats
	labelStats map[string]*LabelStats

	// Global counters
	totalDatapoints uint64
	totalMetrics    uint64

	// OTLP Export counters
	datapointsReceived uint64
	datapointsSent     uint64
	batchesSent        uint64
	exportErrors       uint64

	// PRW counters
	prwDatapointsReceived uint64
	prwTimeseriesReceived uint64
	prwDatapointsSent     uint64
	prwTimeseriesSent     uint64
	prwBatchesSent        uint64
	prwExportErrors       uint64
}

// MetricStats holds stats for a single metric name.
type MetricStats struct {
	Name       string
	Datapoints uint64
	// Cardinality is tracked as unique series (metric + all attributes)
	UniqueSeries map[string]struct{}
}

// LabelStats holds stats for a label combination across all metrics.
type LabelStats struct {
	Labels     string
	Datapoints uint64
	// Cardinality: unique metric+series combinations for this label combo
	UniqueSeries map[string]struct{}
}

// NewCollector creates a new stats collector.
func NewCollector(trackLabels []string) *Collector {
	return &Collector{
		trackLabels: trackLabels,
		metricStats: make(map[string]*MetricStats),
		labelStats:  make(map[string]*LabelStats),
	}
}

// Process processes incoming metrics and updates stats.
func (c *Collector) Process(resourceMetrics []*metricspb.ResourceMetrics) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, rm := range resourceMetrics {
		// Extract resource attributes
		resourceAttrs := extractAttributes(rm.Resource.GetAttributes())

		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				metricName := m.Name
				datapoints := c.countDatapoints(m)

				c.totalDatapoints += uint64(datapoints)

				// Update per-metric stats
				ms, ok := c.metricStats[metricName]
				if !ok {
					ms = &MetricStats{
						Name:         metricName,
						UniqueSeries: make(map[string]struct{}),
					}
					c.metricStats[metricName] = ms
					c.totalMetrics++
				}
				ms.Datapoints += uint64(datapoints)

				// Process each datapoint for cardinality
				c.processDatapointsForCardinality(m, resourceAttrs, ms)

				// Update per-label-combination stats
				c.updateLabelStats(metricName, m, resourceAttrs)
			}
		}
	}
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

// processDatapointsForCardinality extracts unique series identifiers.
func (c *Collector) processDatapointsForCardinality(m *metricspb.Metric, resourceAttrs map[string]string, ms *MetricStats) {
	var allAttrs []map[string]string

	switch d := m.Data.(type) {
	case *metricspb.Metric_Gauge:
		for _, dp := range d.Gauge.DataPoints {
			allAttrs = append(allAttrs, extractAttributes(dp.Attributes))
		}
	case *metricspb.Metric_Sum:
		for _, dp := range d.Sum.DataPoints {
			allAttrs = append(allAttrs, extractAttributes(dp.Attributes))
		}
	case *metricspb.Metric_Histogram:
		for _, dp := range d.Histogram.DataPoints {
			allAttrs = append(allAttrs, extractAttributes(dp.Attributes))
		}
	case *metricspb.Metric_ExponentialHistogram:
		for _, dp := range d.ExponentialHistogram.DataPoints {
			allAttrs = append(allAttrs, extractAttributes(dp.Attributes))
		}
	case *metricspb.Metric_Summary:
		for _, dp := range d.Summary.DataPoints {
			allAttrs = append(allAttrs, extractAttributes(dp.Attributes))
		}
	}

	for _, attrs := range allAttrs {
		// Merge resource and datapoint attributes
		merged := mergeAttrs(resourceAttrs, attrs)
		seriesKey := buildSeriesKey(merged)
		ms.UniqueSeries[seriesKey] = struct{}{}
	}
}

// updateLabelStats updates stats for configured label combinations.
func (c *Collector) updateLabelStats(metricName string, m *metricspb.Metric, resourceAttrs map[string]string) {
	if len(c.trackLabels) == 0 {
		return
	}

	var allAttrs []map[string]string

	switch d := m.Data.(type) {
	case *metricspb.Metric_Gauge:
		for _, dp := range d.Gauge.DataPoints {
			allAttrs = append(allAttrs, extractAttributes(dp.Attributes))
		}
	case *metricspb.Metric_Sum:
		for _, dp := range d.Sum.DataPoints {
			allAttrs = append(allAttrs, extractAttributes(dp.Attributes))
		}
	case *metricspb.Metric_Histogram:
		for _, dp := range d.Histogram.DataPoints {
			allAttrs = append(allAttrs, extractAttributes(dp.Attributes))
		}
	case *metricspb.Metric_ExponentialHistogram:
		for _, dp := range d.ExponentialHistogram.DataPoints {
			allAttrs = append(allAttrs, extractAttributes(dp.Attributes))
		}
	case *metricspb.Metric_Summary:
		for _, dp := range d.Summary.DataPoints {
			allAttrs = append(allAttrs, extractAttributes(dp.Attributes))
		}
	}

	for _, attrs := range allAttrs {
		merged := mergeAttrs(resourceAttrs, attrs)

		// Build label combination key from tracked labels
		labelKey := c.buildLabelKey(merged)
		if labelKey == "" {
			continue // No tracked labels present
		}

		ls, ok := c.labelStats[labelKey]
		if !ok {
			ls = &LabelStats{
				Labels:       labelKey,
				UniqueSeries: make(map[string]struct{}),
			}
			c.labelStats[labelKey] = ls
		}
		ls.Datapoints++

		// Series key includes metric name + all attributes
		seriesKey := metricName + "|" + buildSeriesKey(merged)
		ls.UniqueSeries[seriesKey] = struct{}{}
	}
}

// GetGlobalStats returns global statistics.
func (c *Collector) GetGlobalStats() (datapoints uint64, uniqueMetrics uint64, totalCardinality int) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	datapoints = c.totalDatapoints
	uniqueMetrics = c.totalMetrics

	for _, ms := range c.metricStats {
		totalCardinality += len(ms.UniqueSeries)
	}
	return
}

// RecordReceived records datapoints received (before any filtering).
func (c *Collector) RecordReceived(count int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.datapointsReceived += uint64(count)
}

// RecordExport records a successful batch export.
func (c *Collector) RecordExport(datapointCount int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.batchesSent++
	c.datapointsSent += uint64(datapointCount)
}

// RecordExportError records a failed export attempt.
func (c *Collector) RecordExportError() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.exportErrors++
}

// PRW Stats methods - implements prw.PRWStatsCollector interface

// RecordPRWReceived records PRW datapoints and timeseries received.
func (c *Collector) RecordPRWReceived(datapointCount, timeseriesCount int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prwDatapointsReceived += uint64(datapointCount)
	c.prwTimeseriesReceived += uint64(timeseriesCount)
}

// RecordPRWExport records a successful PRW export.
func (c *Collector) RecordPRWExport(datapointCount, timeseriesCount int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prwBatchesSent++
	c.prwDatapointsSent += uint64(datapointCount)
	c.prwTimeseriesSent += uint64(timeseriesCount)
}

// RecordPRWExportError records a failed PRW export attempt.
func (c *Collector) RecordPRWExportError() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prwExportErrors++
}

// StartPeriodicLogging starts logging global stats every interval.
// It also resets cardinality tracking to prevent unbounded memory growth.
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
		case <-resetTicker.C:
			c.ResetCardinality()
		}
	}
}

// ResetCardinality resets the cardinality tracking maps to prevent unbounded memory growth.
// This keeps counters intact but clears the per-metric and per-label cardinality tracking.
func (c *Collector) ResetCardinality() {
	c.mu.Lock()
	defer c.mu.Unlock()

	// If maps are too large, recreate them entirely to release memory
	const maxMetrics = 10000
	const maxLabels = 5000

	if len(c.metricStats) > maxMetrics {
		c.metricStats = make(map[string]*MetricStats)
		c.totalMetrics = 0
		logging.Info("metric stats map reset due to size", logging.F("previous_size", len(c.metricStats)))
	} else {
		// Reset per-metric cardinality (keep datapoint counts, clear series tracking)
		for _, ms := range c.metricStats {
			ms.UniqueSeries = make(map[string]struct{})
		}
	}

	if len(c.labelStats) > maxLabels {
		c.labelStats = make(map[string]*LabelStats)
		logging.Info("label stats map reset due to size", logging.F("previous_size", len(c.labelStats)))
	} else {
		// Reset per-label cardinality
		for _, ls := range c.labelStats {
			ls.UniqueSeries = make(map[string]struct{})
		}
	}
}

// buildLabelKey builds a key from tracked labels.
func (c *Collector) buildLabelKey(attrs map[string]string) string {
	var parts []string
	for _, label := range c.trackLabels {
		if val, ok := attrs[label]; ok {
			parts = append(parts, fmt.Sprintf("%s=%s", label, val))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ",")
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
func (c *Collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	// Global stats
	fmt.Fprintf(w, "# HELP metrics_governor_datapoints_total Total number of datapoints processed\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_datapoints_total counter\n")
	fmt.Fprintf(w, "metrics_governor_datapoints_total %d\n", c.totalDatapoints)

	fmt.Fprintf(w, "# HELP metrics_governor_metrics_total Total number of unique metric names\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_metrics_total gauge\n")
	fmt.Fprintf(w, "metrics_governor_metrics_total %d\n", c.totalMetrics)

	// Export stats
	fmt.Fprintf(w, "# HELP metrics_governor_datapoints_received_total Total number of datapoints received (before filtering)\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_datapoints_received_total counter\n")
	fmt.Fprintf(w, "metrics_governor_datapoints_received_total %d\n", c.datapointsReceived)

	fmt.Fprintf(w, "# HELP metrics_governor_datapoints_sent_total Total number of datapoints sent to backend\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_datapoints_sent_total counter\n")
	fmt.Fprintf(w, "metrics_governor_datapoints_sent_total %d\n", c.datapointsSent)

	fmt.Fprintf(w, "# HELP metrics_governor_batches_sent_total Total number of batches exported\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_batches_sent_total counter\n")
	fmt.Fprintf(w, "metrics_governor_batches_sent_total %d\n", c.batchesSent)

	fmt.Fprintf(w, "# HELP metrics_governor_export_errors_total Total number of export errors\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_export_errors_total counter\n")
	fmt.Fprintf(w, "metrics_governor_export_errors_total %d\n", c.exportErrors)

	// PRW stats
	fmt.Fprintf(w, "# HELP metrics_governor_prw_datapoints_received_total Total PRW datapoints received\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_prw_datapoints_received_total counter\n")
	fmt.Fprintf(w, "metrics_governor_prw_datapoints_received_total %d\n", c.prwDatapointsReceived)

	fmt.Fprintf(w, "# HELP metrics_governor_prw_timeseries_received_total Total PRW timeseries received\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_prw_timeseries_received_total counter\n")
	fmt.Fprintf(w, "metrics_governor_prw_timeseries_received_total %d\n", c.prwTimeseriesReceived)

	fmt.Fprintf(w, "# HELP metrics_governor_prw_datapoints_sent_total Total PRW datapoints sent to backend\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_prw_datapoints_sent_total counter\n")
	fmt.Fprintf(w, "metrics_governor_prw_datapoints_sent_total %d\n", c.prwDatapointsSent)

	fmt.Fprintf(w, "# HELP metrics_governor_prw_timeseries_sent_total Total PRW timeseries sent to backend\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_prw_timeseries_sent_total counter\n")
	fmt.Fprintf(w, "metrics_governor_prw_timeseries_sent_total %d\n", c.prwTimeseriesSent)

	fmt.Fprintf(w, "# HELP metrics_governor_prw_batches_sent_total Total PRW batches exported\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_prw_batches_sent_total counter\n")
	fmt.Fprintf(w, "metrics_governor_prw_batches_sent_total %d\n", c.prwBatchesSent)

	fmt.Fprintf(w, "# HELP metrics_governor_prw_export_errors_total Total PRW export errors\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_prw_export_errors_total counter\n")
	fmt.Fprintf(w, "metrics_governor_prw_export_errors_total %d\n", c.prwExportErrors)

	// Per-metric stats
	fmt.Fprintf(w, "# HELP metrics_governor_metric_datapoints_total Datapoints per metric name\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_metric_datapoints_total counter\n")
	for name, ms := range c.metricStats {
		fmt.Fprintf(w, "metrics_governor_metric_datapoints_total{metric_name=%q} %d\n", name, ms.Datapoints)
	}

	fmt.Fprintf(w, "# HELP metrics_governor_metric_cardinality Cardinality (unique series) per metric name\n")
	fmt.Fprintf(w, "# TYPE metrics_governor_metric_cardinality gauge\n")
	for name, ms := range c.metricStats {
		fmt.Fprintf(w, "metrics_governor_metric_cardinality{metric_name=%q} %d\n", name, len(ms.UniqueSeries))
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
			fmt.Fprintf(w, "metrics_governor_label_cardinality{%s} %d\n", labelStr, len(ls.UniqueSeries))
		}
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

	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q", k, labels[k]))
	}
	return strings.Join(parts, ",")
}

// Helper functions

// attributeIntern is used for interning OTLP attribute keys and values
var attributeIntern = intern.CommonLabels()

// InternMaxValueLength controls max value length for interning (package-level for testing)
var statsInternMaxValueLength = 64

func extractAttributes(attrs []*commonpb.KeyValue) map[string]string {
	result := make(map[string]string)
	for _, kv := range attrs {
		if kv.Value != nil {
			if sv := kv.Value.GetStringValue(); sv != "" {
				// Intern attribute keys (always, as they're from a fixed set)
				key := attributeIntern.Intern(kv.Key)
				// Intern short values (common values like env names, status codes)
				var value string
				if len(sv) <= statsInternMaxValueLength {
					value = attributeIntern.Intern(sv)
				} else {
					value = sv
				}
				result[key] = value
			}
		}
	}
	return result
}

func mergeAttrs(a, b map[string]string) map[string]string {
	result := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		result[k] = v
	}
	for k, v := range b {
		result[k] = v
	}
	return result
}

func buildSeriesKey(attrs map[string]string) string {
	if len(attrs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, attrs[k]))
	}
	return strings.Join(parts, ",")
}
