package buffer

import (
	"testing"

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
