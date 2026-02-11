package buffer

import (
	"testing"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
	"google.golang.org/protobuf/proto"
)

// createTestResourceMetricsWithSize creates ResourceMetrics with roughly the given byte size.
func createTestResourceMetricsWithSize(targetBytes int) *metricspb.ResourceMetrics {
	// Each attribute adds ~20-30 bytes; each datapoint adds ~16 bytes
	// Build up until we reach approximate target
	attrs := make([]*commonpb.KeyValue, 0)
	dps := make([]*metricspb.NumberDataPoint, 0)

	currentSize := 0
	for currentSize < targetBytes {
		attrs = append(attrs, &commonpb.KeyValue{
			Key:   "key_padding_label_for_size",
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "value_padding_for_size_estimation"}},
		})
		dps = append(dps, &metricspb.NumberDataPoint{
			Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 42.0},
		})

		rm := &metricspb.ResourceMetrics{
			Resource: &resourcepb.Resource{Attributes: attrs},
			ScopeMetrics: []*metricspb.ScopeMetrics{{
				Metrics: []*metricspb.Metric{{
					Name: "test_metric",
					Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: dps}},
				}},
			}},
		}
		currentSize = proto.Size(rm)
	}

	return &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{Attributes: attrs},
		ScopeMetrics: []*metricspb.ScopeMetrics{{
			Metrics: []*metricspb.Metric{{
				Name: "test_metric",
				Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{DataPoints: dps}},
			}},
		}},
	}
}

func TestSplitByBytes_UnderLimit(t *testing.T) {
	batch := createTestResourceMetrics(5)
	maxBytes := 1024 * 1024 // 1MB -- way over what 5 tiny RMs would be

	result := splitByBytes(batch, maxBytes)
	if len(result) != 1 {
		t.Errorf("expected 1 batch (under limit), got %d", len(result))
	}
	if len(result[0]) != 5 {
		t.Errorf("expected 5 elements in batch, got %d", len(result[0]))
	}
}

func TestSplitByBytes_OverLimit(t *testing.T) {
	// Create 10 ResourceMetrics, each ~1KB
	batch := make([]*metricspb.ResourceMetrics, 10)
	for i := 0; i < 10; i++ {
		batch[i] = createTestResourceMetricsWithSize(1024)
	}

	totalSize := estimateBatchSize(batch)
	// Set maxBytes to half the total, forcing at least one split
	maxBytes := totalSize / 2

	result := splitByBytes(batch, maxBytes)
	if len(result) < 2 {
		t.Errorf("expected at least 2 sub-batches, got %d", len(result))
	}

	// Verify all elements are preserved
	totalElements := 0
	for _, sub := range result {
		totalElements += len(sub)
	}
	if totalElements != 10 {
		t.Errorf("expected 10 total elements, got %d", totalElements)
	}

	// Verify each sub-batch is within limit
	for i, sub := range result {
		subSize := estimateBatchSize(sub)
		if subSize > maxBytes && len(sub) > 1 {
			t.Errorf("sub-batch %d size %d exceeds maxBytes %d (elements: %d)", i, subSize, maxBytes, len(sub))
		}
	}
}

func TestSplitByBytes_SingleElement(t *testing.T) {
	batch := []*metricspb.ResourceMetrics{createTestResourceMetricsWithSize(2048)}

	// Set maxBytes lower than element size -- cannot split further
	result := splitByBytes(batch, 100)
	if len(result) != 1 {
		t.Errorf("expected 1 batch (single element), got %d", len(result))
	}
	if len(result[0]) != 1 {
		t.Errorf("expected 1 element in batch, got %d", len(result[0]))
	}
}

func TestSplitByBytes_ZeroLimit(t *testing.T) {
	batch := createTestResourceMetrics(5)

	result := splitByBytes(batch, 0)
	if len(result) != 1 {
		t.Errorf("expected 1 batch (disabled), got %d", len(result))
	}
}

func TestSplitByBytes_NegativeLimit(t *testing.T) {
	batch := createTestResourceMetrics(5)

	result := splitByBytes(batch, -1)
	if len(result) != 1 {
		t.Errorf("expected 1 batch (disabled), got %d", len(result))
	}
}

func TestSplitByBytes_RecursiveSplit(t *testing.T) {
	// Create 16 ResourceMetrics, each ~2KB
	batch := make([]*metricspb.ResourceMetrics, 16)
	for i := 0; i < 16; i++ {
		batch[i] = createTestResourceMetricsWithSize(2048)
	}

	// Set maxBytes to roughly 1 element's worth, forcing max recursion
	elementSize := proto.Size(batch[0])
	maxBytes := elementSize + 10 // Just over 1 element

	result := splitByBytes(batch, maxBytes)

	// Should split all the way down to individual elements
	if len(result) != 16 {
		t.Errorf("expected 16 single-element batches, got %d", len(result))
	}

	// Verify all elements preserved
	totalElements := 0
	for _, sub := range result {
		totalElements += len(sub)
	}
	if totalElements != 16 {
		t.Errorf("expected 16 total elements, got %d", totalElements)
	}
}

func TestSplitByBytes_EmptyBatch(t *testing.T) {
	var batch []*metricspb.ResourceMetrics

	result := splitByBytes(batch, 1024)
	if len(result) != 1 {
		t.Errorf("expected 1 batch (empty), got %d", len(result))
	}
	if len(result[0]) != 0 {
		t.Errorf("expected 0 elements, got %d", len(result[0]))
	}
}

func BenchmarkSplitByBytes(b *testing.B) {
	// Create a batch of 100 ResourceMetrics, each ~1KB
	batch := make([]*metricspb.ResourceMetrics, 100)
	for i := 0; i < 100; i++ {
		batch[i] = createTestResourceMetricsWithSize(1024)
	}
	maxBytes := 8 * 1024 * 1024 // 8MB

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		splitByBytes(batch, maxBytes)
	}
}

func BenchmarkSplitByBytes_Splitting(b *testing.B) {
	// Force splitting by using low maxBytes
	batch := make([]*metricspb.ResourceMetrics, 100)
	for i := 0; i < 100; i++ {
		batch[i] = createTestResourceMetricsWithSize(1024)
	}
	maxBytes := 4 * 1024 // 4KB -- force many splits

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		splitByBytes(batch, maxBytes)
	}
}
