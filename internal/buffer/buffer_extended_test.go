package buffer

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

func TestCountDatapoints_Gauge(t *testing.T) {
	rm := []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "test_gauge",
							Data: &metricspb.Metric_Gauge{
								Gauge: &metricspb.Gauge{
									DataPoints: []*metricspb.NumberDataPoint{
										{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 1.0}},
										{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 2.0}},
										{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 3.0}},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	count := countDatapoints(rm)
	if count != 3 {
		t.Errorf("expected 3 datapoints, got %d", count)
	}
}

func TestCountDatapoints_Sum(t *testing.T) {
	rm := []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "test_sum",
							Data: &metricspb.Metric_Sum{
								Sum: &metricspb.Sum{
									DataPoints: []*metricspb.NumberDataPoint{
										{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 10.0}},
										{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 20.0}},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	count := countDatapoints(rm)
	if count != 2 {
		t.Errorf("expected 2 datapoints, got %d", count)
	}
}

func TestCountDatapoints_Histogram(t *testing.T) {
	rm := []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "test_histogram",
							Data: &metricspb.Metric_Histogram{
								Histogram: &metricspb.Histogram{
									DataPoints: []*metricspb.HistogramDataPoint{
										{Count: 10, Sum: func() *float64 { v := 100.0; return &v }()},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	count := countDatapoints(rm)
	if count != 1 {
		t.Errorf("expected 1 datapoint, got %d", count)
	}
}

func TestCountDatapoints_ExponentialHistogram(t *testing.T) {
	rm := []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "test_exp_histogram",
							Data: &metricspb.Metric_ExponentialHistogram{
								ExponentialHistogram: &metricspb.ExponentialHistogram{
									DataPoints: []*metricspb.ExponentialHistogramDataPoint{
										{Count: 5},
										{Count: 10},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	count := countDatapoints(rm)
	if count != 2 {
		t.Errorf("expected 2 datapoints, got %d", count)
	}
}

func TestCountDatapoints_Summary(t *testing.T) {
	rm := []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "test_summary",
							Data: &metricspb.Metric_Summary{
								Summary: &metricspb.Summary{
									DataPoints: []*metricspb.SummaryDataPoint{
										{Count: 100},
										{Count: 200},
										{Count: 300},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	count := countDatapoints(rm)
	if count != 3 {
		t.Errorf("expected 3 datapoints, got %d", count)
	}
}

func TestCountDatapoints_Mixed(t *testing.T) {
	rm := []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "gauge",
							Data: &metricspb.Metric_Gauge{
								Gauge: &metricspb.Gauge{
									DataPoints: []*metricspb.NumberDataPoint{{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 1.0}}},
								},
							},
						},
						{
							Name: "sum",
							Data: &metricspb.Metric_Sum{
								Sum: &metricspb.Sum{
									DataPoints: []*metricspb.NumberDataPoint{{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 2.0}}},
								},
							},
						},
						{
							Name: "histogram",
							Data: &metricspb.Metric_Histogram{
								Histogram: &metricspb.Histogram{
									DataPoints: []*metricspb.HistogramDataPoint{{Count: 1}},
								},
							},
						},
					},
				},
			},
		},
	}

	count := countDatapoints(rm)
	if count != 3 {
		t.Errorf("expected 3 datapoints (1+1+1), got %d", count)
	}
}

func TestCountDatapoints_Empty(t *testing.T) {
	var rm []*metricspb.ResourceMetrics
	count := countDatapoints(rm)
	if count != 0 {
		t.Errorf("expected 0 datapoints for empty input, got %d", count)
	}
}

func TestCountDatapoints_NilScopeMetrics(t *testing.T) {
	rm := []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: nil,
		},
	}
	count := countDatapoints(rm)
	if count != 0 {
		t.Errorf("expected 0 datapoints for nil scope metrics, got %d", count)
	}
}

func TestCountDatapoints_MultipleResources(t *testing.T) {
	rm := []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "gauge1",
							Data: &metricspb.Metric_Gauge{
								Gauge: &metricspb.Gauge{
									DataPoints: []*metricspb.NumberDataPoint{
										{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 1.0}},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "gauge2",
							Data: &metricspb.Metric_Gauge{
								Gauge: &metricspb.Gauge{
									DataPoints: []*metricspb.NumberDataPoint{
										{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 2.0}},
										{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 3.0}},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	count := countDatapoints(rm)
	if count != 3 {
		t.Errorf("expected 3 datapoints (1+2), got %d", count)
	}
}

func TestCountDatapoints_MultipleScopes(t *testing.T) {
	rm := []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "scope1_gauge",
							Data: &metricspb.Metric_Gauge{
								Gauge: &metricspb.Gauge{
									DataPoints: []*metricspb.NumberDataPoint{
										{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 1.0}},
									},
								},
							},
						},
					},
				},
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "scope2_sum",
							Data: &metricspb.Metric_Sum{
								Sum: &metricspb.Sum{
									DataPoints: []*metricspb.NumberDataPoint{
										{Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 2.0}},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	count := countDatapoints(rm)
	if count != 2 {
		t.Errorf("expected 2 datapoints (1+1), got %d", count)
	}
}

// datapointTrackingExporter records the datapoint count seen via stats on each export,
// and can be configured to fail on specific calls for testing error vs success paths.
type datapointTrackingExporter struct {
	mu        sync.Mutex
	callCount int
	failOn    map[int]bool // which call indices should return an error
}

func (d *datapointTrackingExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	d.mu.Lock()
	idx := d.callCount
	d.callCount++
	d.mu.Unlock()

	if d.failOn != nil && d.failOn[idx] {
		return fmt.Errorf("simulated export error on call %d", idx)
	}
	return nil
}

// datapointTrackingStats captures each RecordExport and RecordExportError call
// to verify datapointCount consistency.
type datapointTrackingStats struct {
	mu             sync.Mutex
	exportCounts   []int // datapointCount passed to RecordExport on success
	exportErrors   int
	bytesSent      []int
	bytesReceived  int
	received       int
	otlpBufferSize int
}

func (d *datapointTrackingStats) Process(resourceMetrics []*metricspb.ResourceMetrics) {}
func (d *datapointTrackingStats) RecordReceived(count int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.received += count
}
func (d *datapointTrackingStats) RecordExport(datapointCount int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.exportCounts = append(d.exportCounts, datapointCount)
}
func (d *datapointTrackingStats) RecordExportError() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.exportErrors++
}
func (d *datapointTrackingStats) RecordOTLPBytesReceived(bytes int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.bytesReceived += bytes
}
func (d *datapointTrackingStats) RecordOTLPBytesReceivedCompressed(bytes int) {}
func (d *datapointTrackingStats) RecordOTLPBytesSent(bytes int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.bytesSent = append(d.bytesSent, bytes)
}
func (d *datapointTrackingStats) SetOTLPBufferSize(size int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.otlpBufferSize = size
}

func (d *datapointTrackingStats) getExportCounts() []int {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := make([]int, len(d.exportCounts))
	copy(result, d.exportCounts)
	return result
}

func (d *datapointTrackingStats) getExportErrors() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.exportErrors
}

// createTestMetricsWithDatapoints creates ResourceMetrics with a known number of datapoints.
func createTestMetricsWithDatapoints(numMetrics, datapointsPerMetric int) []*metricspb.ResourceMetrics {
	metrics := make([]*metricspb.Metric, numMetrics)
	for i := 0; i < numMetrics; i++ {
		dps := make([]*metricspb.NumberDataPoint, datapointsPerMetric)
		for j := 0; j < datapointsPerMetric; j++ {
			dps[j] = &metricspb.NumberDataPoint{
				Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(j)},
			}
		}
		metrics[i] = &metricspb.Metric{
			Name: fmt.Sprintf("test_metric_%d", i),
			Data: &metricspb.Metric_Gauge{
				Gauge: &metricspb.Gauge{
					DataPoints: dps,
				},
			},
		}
	}
	return []*metricspb.ResourceMetrics{
		{
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: metrics,
				},
			},
		},
	}
}

func TestFlush_DatapointCountConsistency(t *testing.T) {
	t.Run("success_path_records_correct_datapoints", func(t *testing.T) {
		// Create metrics with known datapoint count: 5 metrics * 3 datapoints = 15
		exp := &datapointTrackingExporter{}
		stats := &datapointTrackingStats{}
		buf := New(100, 100, time.Hour, exp, stats, nil, nil)

		metrics := createTestMetricsWithDatapoints(5, 3)
		expectedCount := countDatapoints(metrics) // should be 15

		buf.Add(metrics)
		buf.flush(context.Background())

		exportCounts := stats.getExportCounts()
		if len(exportCounts) != 1 {
			t.Fatalf("expected 1 export call, got %d", len(exportCounts))
		}
		if exportCounts[0] != expectedCount {
			t.Errorf("expected datapointCount %d on success, got %d", expectedCount, exportCounts[0])
		}
	})

	t.Run("error_path_does_not_record_export", func(t *testing.T) {
		// When export fails, RecordExport should NOT be called, only RecordExportError
		exp := &datapointTrackingExporter{failOn: map[int]bool{0: true}}
		stats := &datapointTrackingStats{}
		buf := New(100, 100, time.Hour, exp, stats, nil, nil)

		metrics := createTestMetricsWithDatapoints(5, 3)
		buf.Add(metrics)
		buf.flush(context.Background())

		exportCounts := stats.getExportCounts()
		if len(exportCounts) != 0 {
			t.Errorf("expected 0 export calls on error path, got %d", len(exportCounts))
		}
		if stats.getExportErrors() != 1 {
			t.Errorf("expected 1 export error, got %d", stats.getExportErrors())
		}
	})

	t.Run("mixed_batches_consistent_counts", func(t *testing.T) {
		// With batch size 1, each ResourceMetrics is its own batch.
		// Fail the first batch, succeed on the second.
		exp := &datapointTrackingExporter{failOn: map[int]bool{0: true}}
		stats := &datapointTrackingStats{}
		buf := New(100, 1, time.Hour, exp, stats, nil, nil)

		// Add 2 ResourceMetrics, each with 4 datapoints (2 metrics * 2 dp)
		rm1 := createTestMetricsWithDatapoints(2, 2) // 4 datapoints
		rm2 := createTestMetricsWithDatapoints(2, 2) // 4 datapoints
		buf.Add(rm1)
		buf.Add(rm2)

		expectedPerBatch := countDatapoints(rm1) // 4

		buf.flush(context.Background())

		// First batch fails, second succeeds
		if stats.getExportErrors() != 1 {
			t.Errorf("expected 1 export error, got %d", stats.getExportErrors())
		}
		exportCounts := stats.getExportCounts()
		if len(exportCounts) != 1 {
			t.Fatalf("expected 1 successful export, got %d", len(exportCounts))
		}
		if exportCounts[0] != expectedPerBatch {
			t.Errorf("expected datapointCount %d on success, got %d", expectedPerBatch, exportCounts[0])
		}
	})

	t.Run("datapoint_count_matches_direct_computation", func(t *testing.T) {
		// Verify that the datapointCount recorded in stats matches what
		// countDatapoints would return for the same batch.
		exp := &datapointTrackingExporter{}
		stats := &datapointTrackingStats{}
		// Batch size 2 so we get multiple batches from 5 ResourceMetrics
		buf := New(100, 2, time.Hour, exp, stats, nil, nil)

		// Create 5 separate ResourceMetrics with varying datapoints
		for i := 1; i <= 5; i++ {
			rm := createTestMetricsWithDatapoints(1, i) // i datapoints each
			buf.Add(rm)
		}

		buf.flush(context.Background())

		exportCounts := stats.getExportCounts()
		// 5 ResourceMetrics with batch size 2 = 3 batches (2, 2, 1)
		if len(exportCounts) != 3 {
			t.Fatalf("expected 3 batches, got %d", len(exportCounts))
		}

		// Total datapoints should be 1+2+3+4+5 = 15
		total := 0
		for _, c := range exportCounts {
			total += c
		}
		if total != 15 {
			t.Errorf("expected total 15 datapoints across all batches, got %d", total)
		}
	})
}
