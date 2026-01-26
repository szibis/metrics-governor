package stats

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/slawomirskowron/metrics-governor/internal/logging"
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

// StartPeriodicLogging starts logging global stats every interval.
func (c *Collector) StartPeriodicLogging(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

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

func extractAttributes(attrs []*commonpb.KeyValue) map[string]string {
	result := make(map[string]string)
	for _, kv := range attrs {
		if kv.Value != nil {
			if sv := kv.Value.GetStringValue(); sv != "" {
				result[kv.Key] = sv
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
