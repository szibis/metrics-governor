// Package prw provides Prometheus Remote Write protocol support.
package prw

import (
	"fmt"
	"sort"
)

// Version represents the PRW protocol version.
type Version string

const (
	// VersionAuto auto-detects the version based on request content.
	VersionAuto Version = "auto"
	// Version1 is PRW 1.0 (original format with samples only).
	Version1 Version = "1.0"
	// Version2 is PRW 2.0 (extended format with native histograms, exemplars, metadata).
	Version2 Version = "2.0"
)

// ParseVersion parses a version string.
func ParseVersion(s string) (Version, error) {
	switch s {
	case "", "auto":
		return VersionAuto, nil
	case "1.0", "1":
		return Version1, nil
	case "2.0", "2":
		return Version2, nil
	default:
		return VersionAuto, fmt.Errorf("unsupported PRW version: %s", s)
	}
}

// WriteRequest represents a Prometheus remote write request.
// This is compatible with both PRW 1.0 and PRW 2.0.
type WriteRequest struct {
	Timeseries []TimeSeries
	Metadata   []MetricMetadata // PRW 2.0 only
}

// TimeSeries represents a single time series with labels and data points.
type TimeSeries struct {
	Labels     []Label
	Samples    []Sample
	Exemplars  []Exemplar  // PRW 2.0 only
	Histograms []Histogram // PRW 2.0 only (native histograms)
}

// Label is a key-value pair identifying a time series.
type Label struct {
	Name  string
	Value string
}

// Sample represents a single data point.
type Sample struct {
	Value     float64
	Timestamp int64 // Unix timestamp in milliseconds
}

// Exemplar represents an exemplar (PRW 2.0).
type Exemplar struct {
	Labels    []Label
	Value     float64
	Timestamp int64 // Unix timestamp in milliseconds
}

// Histogram represents a native histogram (PRW 2.0).
type Histogram struct {
	// Count is the total number of observations.
	Count uint64
	// CountFloat is used when Count is a float (for summary histograms).
	CountFloat float64
	// Sum of all observed values.
	Sum float64
	// Schema defines the resolution of the histogram.
	// Negative values are for custom bucket boundaries.
	Schema int32
	// ZeroThreshold is the width of the zero bucket.
	ZeroThreshold float64
	// ZeroCount is the count of observations in the zero bucket.
	ZeroCount uint64
	// ZeroCountFloat is used when ZeroCount is a float.
	ZeroCountFloat float64
	// NegativeSpans define the negative buckets.
	NegativeSpans []BucketSpan
	// NegativeDeltas are the bucket counts (delta-encoded) for negative buckets.
	NegativeDeltas []int64
	// NegativeCounts are the bucket counts for negative buckets (float histograms).
	NegativeCounts []float64
	// PositiveSpans define the positive buckets.
	PositiveSpans []BucketSpan
	// PositiveDeltas are the bucket counts (delta-encoded) for positive buckets.
	PositiveDeltas []int64
	// PositiveCounts are the bucket counts for positive buckets (float histograms).
	PositiveCounts []float64
	// ResetHint indicates a counter reset.
	ResetHint HistogramResetHint
	// Timestamp in milliseconds.
	Timestamp int64
}

// BucketSpan defines a run of consecutive buckets.
type BucketSpan struct {
	Offset int32  // Gap to previous span (or starting bucket).
	Length uint32 // Number of consecutive buckets.
}

// HistogramResetHint indicates whether a counter reset occurred.
type HistogramResetHint int32

const (
	// HistogramResetHintUnknown indicates the reset status is unknown.
	HistogramResetHintUnknown HistogramResetHint = 0
	// HistogramResetHintYes indicates a counter reset occurred.
	HistogramResetHintYes HistogramResetHint = 1
	// HistogramResetHintNo indicates no counter reset occurred.
	HistogramResetHintNo HistogramResetHint = 2
	// HistogramResetHintGauge indicates this is a gauge histogram.
	HistogramResetHintGauge HistogramResetHint = 3
)

// MetricMetadata contains metadata about a metric (PRW 2.0).
type MetricMetadata struct {
	// Type is the metric type (counter, gauge, histogram, etc.).
	Type MetricType
	// MetricFamilyName is the metric family name.
	MetricFamilyName string
	// Help is the help text for the metric.
	Help string
	// Unit is the unit of the metric.
	Unit string
}

// MetricType identifies the type of a metric.
type MetricType int32

const (
	MetricTypeUnknown        MetricType = 0
	MetricTypeCounter        MetricType = 1
	MetricTypeGauge          MetricType = 2
	MetricTypeHistogram      MetricType = 3
	MetricTypeGaugeHistogram MetricType = 4
	MetricTypeSummary        MetricType = 5
	MetricTypeInfo           MetricType = 6
	MetricTypeStateset       MetricType = 7
)

// String returns a string representation of the metric type.
func (t MetricType) String() string {
	switch t {
	case MetricTypeCounter:
		return "counter"
	case MetricTypeGauge:
		return "gauge"
	case MetricTypeHistogram:
		return "histogram"
	case MetricTypeGaugeHistogram:
		return "gauge_histogram"
	case MetricTypeSummary:
		return "summary"
	case MetricTypeInfo:
		return "info"
	case MetricTypeStateset:
		return "stateset"
	default:
		return "unknown"
	}
}

// MetricName returns the __name__ label value from a TimeSeries.
func (ts *TimeSeries) MetricName() string {
	for _, l := range ts.Labels {
		if l.Name == "__name__" {
			return l.Value
		}
	}
	return ""
}

// GetLabelValue returns the value of a label by name.
func (ts *TimeSeries) GetLabelValue(name string) string {
	for _, l := range ts.Labels {
		if l.Name == name {
			return l.Value
		}
	}
	return ""
}

// DatapointCount returns the total number of data points in the TimeSeries.
func (ts *TimeSeries) DatapointCount() int {
	return len(ts.Samples) + len(ts.Exemplars) + len(ts.Histograms)
}

// TotalDatapoints returns the total number of data points in the WriteRequest.
func (req *WriteRequest) TotalDatapoints() int {
	count := 0
	for i := range req.Timeseries {
		count += req.Timeseries[i].DatapointCount()
	}
	return count
}

// TotalTimeseries returns the number of time series in the WriteRequest.
func (req *WriteRequest) TotalTimeseries() int {
	return len(req.Timeseries)
}

// IsPRW2 returns true if the request contains PRW 2.0 specific features.
func (req *WriteRequest) IsPRW2() bool {
	if len(req.Metadata) > 0 {
		return true
	}
	for i := range req.Timeseries {
		if len(req.Timeseries[i].Exemplars) > 0 || len(req.Timeseries[i].Histograms) > 0 {
			return true
		}
	}
	return false
}

// Clone creates a deep copy of the WriteRequest.
func (req *WriteRequest) Clone() *WriteRequest {
	if req == nil {
		return nil
	}
	clone := &WriteRequest{
		Timeseries: make([]TimeSeries, len(req.Timeseries)),
		Metadata:   make([]MetricMetadata, len(req.Metadata)),
	}
	for i := range req.Timeseries {
		clone.Timeseries[i] = req.Timeseries[i].Clone()
	}
	copy(clone.Metadata, req.Metadata)
	return clone
}

// Clone creates a deep copy of the TimeSeries.
func (ts TimeSeries) Clone() TimeSeries {
	clone := TimeSeries{
		Labels:     make([]Label, len(ts.Labels)),
		Samples:    make([]Sample, len(ts.Samples)),
		Exemplars:  make([]Exemplar, len(ts.Exemplars)),
		Histograms: make([]Histogram, len(ts.Histograms)),
	}
	copy(clone.Labels, ts.Labels)
	copy(clone.Samples, ts.Samples)
	copy(clone.Exemplars, ts.Exemplars)
	copy(clone.Histograms, ts.Histograms)
	return clone
}

// SortLabels sorts the labels by name for consistent ordering.
func (ts *TimeSeries) SortLabels() {
	sort.Slice(ts.Labels, func(i, j int) bool {
		return ts.Labels[i].Name < ts.Labels[j].Name
	})
}

// LabelsHash returns a hash of the labels for use in maps.
// Labels should be sorted before calling this function for consistent results.
func (ts *TimeSeries) LabelsHash() uint64 {
	// Simple FNV-1a hash
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	hash := uint64(offset64)
	for _, l := range ts.Labels {
		for i := 0; i < len(l.Name); i++ {
			hash ^= uint64(l.Name[i])
			hash *= prime64
		}
		hash ^= uint64(0xFF) // separator
		hash *= prime64
		for i := 0; i < len(l.Value); i++ {
			hash ^= uint64(l.Value[i])
			hash *= prime64
		}
		hash ^= uint64(0xFF) // separator
		hash *= prime64
	}
	return hash
}
