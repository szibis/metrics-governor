package sharding

import (
	"testing"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
)

// Helper functions for creating test data

func stringAttr(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key:   key,
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}},
	}
}

func intAttr(key string, value int64) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key:   key,
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: value}},
	}
}

func createGaugeDataPoint(attrs ...*commonpb.KeyValue) *metricspb.NumberDataPoint {
	return &metricspb.NumberDataPoint{
		Attributes:   attrs,
		TimeUnixNano: 1000000000,
		Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: 42.0},
	}
}

func createSumDataPoint(attrs ...*commonpb.KeyValue) *metricspb.NumberDataPoint {
	return &metricspb.NumberDataPoint{
		Attributes:   attrs,
		TimeUnixNano: 1000000000,
		Value:        &metricspb.NumberDataPoint_AsInt{AsInt: 100},
	}
}

func createHistogramDataPoint(attrs ...*commonpb.KeyValue) *metricspb.HistogramDataPoint {
	return &metricspb.HistogramDataPoint{
		Attributes:     attrs,
		TimeUnixNano:   1000000000,
		Count:          10,
		Sum:            ptrFloat64(100.0),
		BucketCounts:   []uint64{2, 5, 3},
		ExplicitBounds: []float64{10, 50},
	}
}

func createExponentialHistogramDataPoint(attrs ...*commonpb.KeyValue) *metricspb.ExponentialHistogramDataPoint {
	return &metricspb.ExponentialHistogramDataPoint{
		Attributes:   attrs,
		TimeUnixNano: 1000000000,
		Count:        10,
		Sum:          ptrFloat64(100.0),
		Scale:        3,
	}
}

func createSummaryDataPoint(attrs ...*commonpb.KeyValue) *metricspb.SummaryDataPoint {
	return &metricspb.SummaryDataPoint{
		Attributes:   attrs,
		TimeUnixNano: 1000000000,
		Count:        10,
		Sum:          100.0,
	}
}

func ptrFloat64(f float64) *float64 {
	return &f
}

func createGaugeMetric(name string, datapoints ...*metricspb.NumberDataPoint) *metricspb.Metric {
	return &metricspb.Metric{
		Name:        name,
		Description: name + " description",
		Unit:        "1",
		Data: &metricspb.Metric_Gauge{
			Gauge: &metricspb.Gauge{
				DataPoints: datapoints,
			},
		},
	}
}

func createSumMetric(name string, datapoints ...*metricspb.NumberDataPoint) *metricspb.Metric {
	return &metricspb.Metric{
		Name:        name,
		Description: name + " description",
		Unit:        "1",
		Data: &metricspb.Metric_Sum{
			Sum: &metricspb.Sum{
				DataPoints:             datapoints,
				AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
				IsMonotonic:            true,
			},
		},
	}
}

func createHistogramMetric(name string, datapoints ...*metricspb.HistogramDataPoint) *metricspb.Metric {
	return &metricspb.Metric{
		Name:        name,
		Description: name + " description",
		Unit:        "s",
		Data: &metricspb.Metric_Histogram{
			Histogram: &metricspb.Histogram{
				DataPoints:             datapoints,
				AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
			},
		},
	}
}

func createExponentialHistogramMetric(name string, datapoints ...*metricspb.ExponentialHistogramDataPoint) *metricspb.Metric {
	return &metricspb.Metric{
		Name:        name,
		Description: name + " description",
		Unit:        "s",
		Data: &metricspb.Metric_ExponentialHistogram{
			ExponentialHistogram: &metricspb.ExponentialHistogram{
				DataPoints:             datapoints,
				AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
			},
		},
	}
}

func createSummaryMetric(name string, datapoints ...*metricspb.SummaryDataPoint) *metricspb.Metric {
	return &metricspb.Metric{
		Name:        name,
		Description: name + " description",
		Unit:        "s",
		Data: &metricspb.Metric_Summary{
			Summary: &metricspb.Summary{
				DataPoints: datapoints,
			},
		},
	}
}

func createResourceMetrics(resourceAttrs []*commonpb.KeyValue, metrics ...*metricspb.Metric) *metricspb.ResourceMetrics {
	return &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{
			Attributes: resourceAttrs,
		},
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{
				Scope: &commonpb.InstrumentationScope{
					Name:    "test",
					Version: "1.0",
				},
				Metrics: metrics,
			},
		},
	}
}

// Tests

func TestMetricsSplitter_New(t *testing.T) {
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service"}})
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"ep1", "ep2"})

	splitter := NewMetricsSplitter(keyBuilder, ring)
	if splitter == nil {
		t.Fatal("expected non-nil splitter")
	}
}

func TestMetricsSplitter_Split_EmptyInput(t *testing.T) {
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service"}})
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"ep1", "ep2"})
	splitter := NewMetricsSplitter(keyBuilder, ring)

	result := splitter.Split(nil)
	if result != nil {
		t.Errorf("expected nil for nil input, got %v", result)
	}

	result = splitter.Split([]*metricspb.ResourceMetrics{})
	if result != nil {
		t.Errorf("expected nil for empty input, got %v", result)
	}
}

func TestMetricsSplitter_Split_EmptyRing(t *testing.T) {
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service"}})
	ring := NewHashRing(100)
	// Don't add any endpoints
	splitter := NewMetricsSplitter(keyBuilder, ring)

	rm := createResourceMetrics(nil, createGaugeMetric("test_metric",
		createGaugeDataPoint(stringAttr("service", "api")),
	))

	result := splitter.Split([]*metricspb.ResourceMetrics{rm})
	if result != nil {
		t.Errorf("expected nil for empty ring, got %v", result)
	}
}

func TestMetricsSplitter_Split_SingleEndpoint(t *testing.T) {
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service"}})
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"ep1"})
	splitter := NewMetricsSplitter(keyBuilder, ring)

	rm := createResourceMetrics(nil, createGaugeMetric("test_metric",
		createGaugeDataPoint(stringAttr("service", "api")),
		createGaugeDataPoint(stringAttr("service", "web")),
	))

	result := splitter.Split([]*metricspb.ResourceMetrics{rm})

	if len(result) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(result))
	}

	if _, ok := result["ep1"]; !ok {
		t.Error("expected ep1 in result")
	}

	// All datapoints should go to the single endpoint
	totalDatapoints := CountDatapoints(result["ep1"])
	if totalDatapoints != 2 {
		t.Errorf("expected 2 datapoints, got %d", totalDatapoints)
	}
}

func TestMetricsSplitter_Split_TwoEndpoints(t *testing.T) {
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service"}})
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"ep1", "ep2"})
	splitter := NewMetricsSplitter(keyBuilder, ring)

	// Create metrics with different service values that will likely hash to different endpoints
	rm := createResourceMetrics(nil, createGaugeMetric("test_metric",
		createGaugeDataPoint(stringAttr("service", "api")),
		createGaugeDataPoint(stringAttr("service", "web")),
		createGaugeDataPoint(stringAttr("service", "db")),
		createGaugeDataPoint(stringAttr("service", "cache")),
	))

	result := splitter.Split([]*metricspb.ResourceMetrics{rm})

	// Count total datapoints across all endpoints
	totalDatapoints := 0
	for _, rms := range result {
		totalDatapoints += CountDatapoints(rms)
	}

	if totalDatapoints != 4 {
		t.Errorf("expected 4 total datapoints, got %d", totalDatapoints)
	}
}

func TestMetricsSplitter_Split_PreservesResource(t *testing.T) {
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service"}})
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"ep1"})
	splitter := NewMetricsSplitter(keyBuilder, ring)

	resourceAttrs := []*commonpb.KeyValue{
		stringAttr("host.name", "server1"),
		stringAttr("service.name", "myservice"),
	}

	rm := createResourceMetrics(resourceAttrs, createGaugeMetric("test_metric",
		createGaugeDataPoint(stringAttr("service", "api")),
	))

	result := splitter.Split([]*metricspb.ResourceMetrics{rm})

	splitRM := result["ep1"][0]
	splitResourceAttrs := splitRM.GetResource().GetAttributes()

	if len(splitResourceAttrs) != 2 {
		t.Fatalf("expected 2 resource attributes, got %d", len(splitResourceAttrs))
	}
}

func TestMetricsSplitter_Split_PreservesScope(t *testing.T) {
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service"}})
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"ep1"})
	splitter := NewMetricsSplitter(keyBuilder, ring)

	rm := createResourceMetrics(nil, createGaugeMetric("test_metric",
		createGaugeDataPoint(stringAttr("service", "api")),
	))

	result := splitter.Split([]*metricspb.ResourceMetrics{rm})

	splitRM := result["ep1"][0]
	scope := splitRM.GetScopeMetrics()[0].GetScope()

	if scope.Name != "test" {
		t.Errorf("expected scope name 'test', got %q", scope.Name)
	}
	if scope.Version != "1.0" {
		t.Errorf("expected scope version '1.0', got %q", scope.Version)
	}
}

func TestMetricsSplitter_Split_PreservesMetricMetadata(t *testing.T) {
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service"}})
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"ep1"})
	splitter := NewMetricsSplitter(keyBuilder, ring)

	rm := createResourceMetrics(nil, createGaugeMetric("my_metric",
		createGaugeDataPoint(stringAttr("service", "api")),
	))

	result := splitter.Split([]*metricspb.ResourceMetrics{rm})

	splitRM := result["ep1"][0]
	metric := splitRM.GetScopeMetrics()[0].GetMetrics()[0]

	if metric.Name != "my_metric" {
		t.Errorf("expected metric name 'my_metric', got %q", metric.Name)
	}
	if metric.Description != "my_metric description" {
		t.Errorf("expected metric description, got %q", metric.Description)
	}
	if metric.Unit != "1" {
		t.Errorf("expected metric unit '1', got %q", metric.Unit)
	}
}

func TestMetricsSplitter_Split_Gauge(t *testing.T) {
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service"}})
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"ep1", "ep2", "ep3"})
	splitter := NewMetricsSplitter(keyBuilder, ring)

	rm := createResourceMetrics(nil, createGaugeMetric("cpu_usage",
		createGaugeDataPoint(stringAttr("service", "api")),
		createGaugeDataPoint(stringAttr("service", "web")),
	))

	result := splitter.Split([]*metricspb.ResourceMetrics{rm})

	totalDatapoints := 0
	for _, rms := range result {
		totalDatapoints += CountDatapoints(rms)
	}

	if totalDatapoints != 2 {
		t.Errorf("expected 2 total datapoints, got %d", totalDatapoints)
	}
}

func TestMetricsSplitter_Split_Sum(t *testing.T) {
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service"}})
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"ep1", "ep2"})
	splitter := NewMetricsSplitter(keyBuilder, ring)

	rm := createResourceMetrics(nil, createSumMetric("request_count",
		createSumDataPoint(stringAttr("service", "api")),
		createSumDataPoint(stringAttr("service", "web")),
	))

	result := splitter.Split([]*metricspb.ResourceMetrics{rm})

	totalDatapoints := 0
	for _, rms := range result {
		totalDatapoints += CountDatapoints(rms)
	}

	if totalDatapoints != 2 {
		t.Errorf("expected 2 total datapoints, got %d", totalDatapoints)
	}

	// Verify Sum properties are preserved
	for _, rms := range result {
		for _, splitRM := range rms {
			for _, sm := range splitRM.GetScopeMetrics() {
				for _, m := range sm.GetMetrics() {
					sum := m.GetSum()
					if sum == nil {
						t.Error("expected Sum metric")
						continue
					}
					if sum.AggregationTemporality != metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE {
						t.Error("expected CUMULATIVE aggregation temporality")
					}
					if !sum.IsMonotonic {
						t.Error("expected IsMonotonic=true")
					}
				}
			}
		}
	}
}

func TestMetricsSplitter_Split_Histogram(t *testing.T) {
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service"}})
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"ep1", "ep2"})
	splitter := NewMetricsSplitter(keyBuilder, ring)

	rm := createResourceMetrics(nil, createHistogramMetric("request_duration",
		createHistogramDataPoint(stringAttr("service", "api")),
		createHistogramDataPoint(stringAttr("service", "web")),
	))

	result := splitter.Split([]*metricspb.ResourceMetrics{rm})

	totalDatapoints := 0
	for _, rms := range result {
		totalDatapoints += CountDatapoints(rms)
	}

	if totalDatapoints != 2 {
		t.Errorf("expected 2 total datapoints, got %d", totalDatapoints)
	}
}

func TestMetricsSplitter_Split_ExponentialHistogram(t *testing.T) {
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service"}})
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"ep1", "ep2"})
	splitter := NewMetricsSplitter(keyBuilder, ring)

	rm := createResourceMetrics(nil, createExponentialHistogramMetric("request_duration",
		createExponentialHistogramDataPoint(stringAttr("service", "api")),
		createExponentialHistogramDataPoint(stringAttr("service", "web")),
	))

	result := splitter.Split([]*metricspb.ResourceMetrics{rm})

	totalDatapoints := 0
	for _, rms := range result {
		totalDatapoints += CountDatapoints(rms)
	}

	if totalDatapoints != 2 {
		t.Errorf("expected 2 total datapoints, got %d", totalDatapoints)
	}
}

func TestMetricsSplitter_Split_Summary(t *testing.T) {
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service"}})
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"ep1", "ep2"})
	splitter := NewMetricsSplitter(keyBuilder, ring)

	rm := createResourceMetrics(nil, createSummaryMetric("request_duration",
		createSummaryDataPoint(stringAttr("service", "api")),
		createSummaryDataPoint(stringAttr("service", "web")),
	))

	result := splitter.Split([]*metricspb.ResourceMetrics{rm})

	totalDatapoints := 0
	for _, rms := range result {
		totalDatapoints += CountDatapoints(rms)
	}

	if totalDatapoints != 2 {
		t.Errorf("expected 2 total datapoints, got %d", totalDatapoints)
	}
}

func TestMetricsSplitter_Split_MixedTypes(t *testing.T) {
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service"}})
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"ep1"})
	splitter := NewMetricsSplitter(keyBuilder, ring)

	rm := createResourceMetrics(nil,
		createGaugeMetric("cpu_usage",
			createGaugeDataPoint(stringAttr("service", "api")),
		),
		createSumMetric("request_count",
			createSumDataPoint(stringAttr("service", "api")),
		),
		createHistogramMetric("request_duration",
			createHistogramDataPoint(stringAttr("service", "api")),
		),
	)

	result := splitter.Split([]*metricspb.ResourceMetrics{rm})

	totalDatapoints := 0
	for _, rms := range result {
		totalDatapoints += CountDatapoints(rms)
	}

	if totalDatapoints != 3 {
		t.Errorf("expected 3 total datapoints, got %d", totalDatapoints)
	}
}

func TestMetricsSplitter_Split_MergesResourceAttrs(t *testing.T) {
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service", "env"}})
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"ep1"})
	splitter := NewMetricsSplitter(keyBuilder, ring)

	// Resource has "env" attribute, datapoint has "service"
	resourceAttrs := []*commonpb.KeyValue{
		stringAttr("env", "prod"),
	}

	rm := createResourceMetrics(resourceAttrs, createGaugeMetric("test_metric",
		createGaugeDataPoint(stringAttr("service", "api")),
	))

	result := splitter.Split([]*metricspb.ResourceMetrics{rm})

	// Should have split using merged attributes (env from resource, service from datapoint)
	if len(result) != 1 {
		t.Errorf("expected 1 endpoint, got %d", len(result))
	}
}

func TestMetricsSplitter_Split_DatapointAttrsPriority(t *testing.T) {
	// When both resource and datapoint have same attribute, datapoint wins
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service"}})
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"ep1", "ep2"})
	splitter := NewMetricsSplitter(keyBuilder, ring)

	// Resource has service=api
	resourceAttrs := []*commonpb.KeyValue{
		stringAttr("service", "api"),
	}

	// Datapoint overrides to service=web
	rm := createResourceMetrics(resourceAttrs, createGaugeMetric("test_metric",
		createGaugeDataPoint(stringAttr("service", "web")),
	))

	result := splitter.Split([]*metricspb.ResourceMetrics{rm})

	// Verify that service=web was used for routing (datapoint wins)
	totalDatapoints := 0
	for _, rms := range result {
		totalDatapoints += CountDatapoints(rms)
	}

	if totalDatapoints != 1 {
		t.Errorf("expected 1 total datapoint, got %d", totalDatapoints)
	}
}

func TestMetricsSplitter_Split_EmptyDatapoints(t *testing.T) {
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service"}})
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"ep1"})
	splitter := NewMetricsSplitter(keyBuilder, ring)

	// Metric with no datapoints
	rm := createResourceMetrics(nil, &metricspb.Metric{
		Name: "empty_metric",
		Data: &metricspb.Metric_Gauge{
			Gauge: &metricspb.Gauge{
				DataPoints: []*metricspb.NumberDataPoint{},
			},
		},
	})

	result := splitter.Split([]*metricspb.ResourceMetrics{rm})

	// Should have no result since no datapoints
	totalDatapoints := 0
	for _, rms := range result {
		totalDatapoints += CountDatapoints(rms)
	}

	if totalDatapoints != 0 {
		t.Errorf("expected 0 total datapoints, got %d", totalDatapoints)
	}
}

func TestMetricsSplitter_Split_NoLabelsConfigured(t *testing.T) {
	// When no labels configured, shard key is just metric name
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{}})
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"ep1", "ep2", "ep3"})
	splitter := NewMetricsSplitter(keyBuilder, ring)

	// All datapoints with same metric name should go to same endpoint
	rm := createResourceMetrics(nil, createGaugeMetric("test_metric",
		createGaugeDataPoint(stringAttr("service", "api")),
		createGaugeDataPoint(stringAttr("service", "web")),
		createGaugeDataPoint(stringAttr("service", "db")),
	))

	result := splitter.Split([]*metricspb.ResourceMetrics{rm})

	// All should go to same endpoint since shard key is just metric name
	if len(result) != 1 {
		t.Errorf("expected all datapoints to go to 1 endpoint, got %d endpoints", len(result))
	}

	totalDatapoints := 0
	for _, rms := range result {
		totalDatapoints += CountDatapoints(rms)
	}

	if totalDatapoints != 3 {
		t.Errorf("expected 3 total datapoints, got %d", totalDatapoints)
	}
}

func TestMetricsSplitter_Split_NilResourceMetrics(t *testing.T) {
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service"}})
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"ep1"})
	splitter := NewMetricsSplitter(keyBuilder, ring)

	// Mix of nil and valid ResourceMetrics
	result := splitter.Split([]*metricspb.ResourceMetrics{
		nil,
		createResourceMetrics(nil, createGaugeMetric("test_metric",
			createGaugeDataPoint(stringAttr("service", "api")),
		)),
		nil,
	})

	totalDatapoints := 0
	for _, rms := range result {
		totalDatapoints += CountDatapoints(rms)
	}

	if totalDatapoints != 1 {
		t.Errorf("expected 1 total datapoint, got %d", totalDatapoints)
	}
}

func TestMetricsSplitter_Split_ManyEndpoints(t *testing.T) {
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service"}})
	ring := NewHashRing(100)

	// 10 endpoints
	endpoints := make([]string, 10)
	for i := 0; i < 10; i++ {
		endpoints[i] = string(rune('A' + i))
	}
	ring.UpdateEndpoints(endpoints)
	splitter := NewMetricsSplitter(keyBuilder, ring)

	// 100 datapoints with different services
	datapoints := make([]*metricspb.NumberDataPoint, 100)
	for i := 0; i < 100; i++ {
		datapoints[i] = createGaugeDataPoint(stringAttr("service", string(rune('0'+i%10))+string(rune('A'+i/10))))
	}

	rm := createResourceMetrics(nil, createGaugeMetric("test_metric", datapoints...))

	result := splitter.Split([]*metricspb.ResourceMetrics{rm})

	// Count total datapoints
	totalDatapoints := 0
	for _, rms := range result {
		totalDatapoints += CountDatapoints(rms)
	}

	if totalDatapoints != 100 {
		t.Errorf("expected 100 total datapoints, got %d", totalDatapoints)
	}
}

// CountDatapoints tests

func TestCountDatapoints(t *testing.T) {
	tests := []struct {
		name     string
		rms      []*metricspb.ResourceMetrics
		expected int
	}{
		{
			name:     "nil input",
			rms:      nil,
			expected: 0,
		},
		{
			name:     "empty input",
			rms:      []*metricspb.ResourceMetrics{},
			expected: 0,
		},
		{
			name: "single gauge",
			rms: []*metricspb.ResourceMetrics{
				createResourceMetrics(nil, createGaugeMetric("m",
					createGaugeDataPoint(),
				)),
			},
			expected: 1,
		},
		{
			name: "multiple metrics",
			rms: []*metricspb.ResourceMetrics{
				createResourceMetrics(nil,
					createGaugeMetric("m1", createGaugeDataPoint(), createGaugeDataPoint()),
					createSumMetric("m2", createSumDataPoint()),
				),
			},
			expected: 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			count := CountDatapoints(tc.rms)
			if count != tc.expected {
				t.Errorf("expected %d, got %d", tc.expected, count)
			}
		})
	}
}

// Benchmarks

func BenchmarkMetricsSplitter_Split_10Metrics(b *testing.B) {
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service", "env"}})
	ring := NewHashRing(150)
	ring.UpdateEndpoints([]string{"ep1", "ep2", "ep3"})
	splitter := NewMetricsSplitter(keyBuilder, ring)

	datapoints := make([]*metricspb.NumberDataPoint, 10)
	for i := 0; i < 10; i++ {
		datapoints[i] = createGaugeDataPoint(
			stringAttr("service", "api"),
			stringAttr("env", "prod"),
		)
	}
	rm := createResourceMetrics(nil, createGaugeMetric("test_metric", datapoints...))
	rms := []*metricspb.ResourceMetrics{rm}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		splitter.Split(rms)
	}
}

func BenchmarkMetricsSplitter_Split_100Metrics(b *testing.B) {
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service", "env"}})
	ring := NewHashRing(150)
	ring.UpdateEndpoints([]string{"ep1", "ep2", "ep3"})
	splitter := NewMetricsSplitter(keyBuilder, ring)

	datapoints := make([]*metricspb.NumberDataPoint, 100)
	for i := 0; i < 100; i++ {
		datapoints[i] = createGaugeDataPoint(
			stringAttr("service", string(rune('A'+i%26))),
			stringAttr("env", "prod"),
		)
	}
	rm := createResourceMetrics(nil, createGaugeMetric("test_metric", datapoints...))
	rms := []*metricspb.ResourceMetrics{rm}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		splitter.Split(rms)
	}
}

func BenchmarkMetricsSplitter_Split_1000Metrics(b *testing.B) {
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service", "env"}})
	ring := NewHashRing(150)
	ring.UpdateEndpoints([]string{"ep1", "ep2", "ep3"})
	splitter := NewMetricsSplitter(keyBuilder, ring)

	datapoints := make([]*metricspb.NumberDataPoint, 1000)
	for i := 0; i < 1000; i++ {
		datapoints[i] = createGaugeDataPoint(
			stringAttr("service", string(rune('A'+i%26))),
			stringAttr("env", "prod"),
		)
	}
	rm := createResourceMetrics(nil, createGaugeMetric("test_metric", datapoints...))
	rms := []*metricspb.ResourceMetrics{rm}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		splitter.Split(rms)
	}
}
