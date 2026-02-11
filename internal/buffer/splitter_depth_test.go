package buffer

import (
	"testing"

	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	"google.golang.org/protobuf/proto"
)

// TestSplitByBytes_MaxDepth_StopsRecursion verifies that with
// defaultMaxSplitDepth=4, splitting produces at most 2^4=16 sub-batches
// even when the batch is very large relative to maxBytes.
func TestSplitByBytes_MaxDepth_StopsRecursion(t *testing.T) {
	// Create 32 ResourceMetrics, each ~1KB.
	batch := make([]*metricspb.ResourceMetrics, 32)
	for i := 0; i < 32; i++ {
		batch[i] = createTestResourceMetricsWithSize(1024)
	}

	// Set maxBytes so small that full splitting would require depth > 4.
	// Each RM is ~1KB, so 32KB total. With maxBytes = 1KB, full split would
	// need 32 sub-batches (depth 5), but depth is capped at 4 (16 sub-batches).
	elementSize := proto.Size(batch[0])
	maxBytes := elementSize + 10 // Just over 1 element

	result := splitByBytes(batch, maxBytes)

	// With depth 4, max sub-batches = 2^4 = 16.
	maxSubBatches := 1 << defaultMaxSplitDepth // 16
	if len(result) > maxSubBatches {
		t.Errorf("expected at most %d sub-batches (depth %d), got %d",
			maxSubBatches, defaultMaxSplitDepth, len(result))
	}

	// Verify all elements preserved.
	totalElements := 0
	for _, sub := range result {
		totalElements += len(sub)
	}
	if totalElements != 32 {
		t.Errorf("expected 32 total elements preserved, got %d", totalElements)
	}

	// Some sub-batches may exceed maxBytes (because depth was hit).
	// Verify that at least some splitting happened.
	if len(result) < 2 {
		t.Errorf("expected at least 2 sub-batches from a 32-element batch, got %d", len(result))
	}
}

// TestSplitByBytes_MaxDepth_LargeBatch verifies splitting a batch with many
// elements where the total exceeds maxBytes. The depth limit constrains output.
func TestSplitByBytes_MaxDepth_LargeBatch(t *testing.T) {
	// Create 64 elements, each ~2KB = ~128KB total.
	// Using moderate sizes to keep test fast.
	const numElements = 64
	const targetElementSize = 2048

	batch := make([]*metricspb.ResourceMetrics, numElements)
	for i := 0; i < numElements; i++ {
		batch[i] = createTestResourceMetricsWithSize(targetElementSize)
	}

	totalSize := estimateBatchSize(batch)
	t.Logf("total batch size: %d bytes (%d elements)", totalSize, numElements)

	// Set maxBytes to ~8KB, forcing deep splits.
	maxBytes := 8 * 1024

	result := splitByBytes(batch, maxBytes)

	// Depth 4 => at most 16 sub-batches.
	maxSubBatches := 1 << defaultMaxSplitDepth
	if len(result) > maxSubBatches {
		t.Errorf("expected at most %d sub-batches, got %d", maxSubBatches, len(result))
	}

	// All elements should be preserved.
	totalElements := 0
	for _, sub := range result {
		totalElements += len(sub)
	}
	if totalElements != numElements {
		t.Errorf("expected %d total elements, got %d", numElements, totalElements)
	}

	// Log sub-batch sizes for debugging.
	for i, sub := range result {
		subSize := estimateBatchSize(sub)
		t.Logf("sub-batch %d: %d elements, %d bytes (max: %d)", i, len(sub), subSize, maxBytes)
	}
}

// TestSplitByBytes_SingleElement_NeverSplit verifies that a single-element
// batch is returned as-is even if it exceeds maxBytes.
func TestSplitByBytes_SingleElement_NeverSplit(t *testing.T) {
	// Create a single large element (~10KB).
	rm := createTestResourceMetricsWithSize(10 * 1024)
	batch := []*metricspb.ResourceMetrics{rm}

	elementSize := proto.Size(rm)
	t.Logf("single element size: %d bytes", elementSize)

	// Set maxBytes to 100 bytes -- much less than the element.
	result := splitByBytes(batch, 100)

	if len(result) != 1 {
		t.Errorf("expected 1 batch (single element), got %d", len(result))
	}
	if len(result[0]) != 1 {
		t.Errorf("expected 1 element in the batch, got %d", len(result[0]))
	}
	if proto.Size(result[0][0]) != elementSize {
		t.Errorf("element size changed: expected %d, got %d", elementSize, proto.Size(result[0][0]))
	}
}

// TestSplitByBytes_DisabledWhenMaxBytesZero verifies that maxBytes<=0 returns
// the batch as-is without splitting.
func TestSplitByBytes_DisabledWhenMaxBytesZero(t *testing.T) {
	batch := make([]*metricspb.ResourceMetrics, 20)
	for i := 0; i < 20; i++ {
		batch[i] = createTestResourceMetricsWithSize(2048)
	}

	tests := []struct {
		name     string
		maxBytes int
	}{
		{"zero", 0},
		{"negative", -1},
		{"large_negative", -1000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitByBytes(batch, tt.maxBytes)
			if len(result) != 1 {
				t.Errorf("expected 1 batch (disabled), got %d", len(result))
			}
			if len(result[0]) != 20 {
				t.Errorf("expected 20 elements, got %d", len(result[0]))
			}
		})
	}
}

// TestSplitByBytesWithDepth_ExplicitDepthLimits directly tests
// splitByBytesWithDepth at various depth levels.
func TestSplitByBytesWithDepth_ExplicitDepthLimits(t *testing.T) {
	// Create 8 elements, each ~1KB.
	batch := make([]*metricspb.ResourceMetrics, 8)
	for i := 0; i < 8; i++ {
		batch[i] = createTestResourceMetricsWithSize(1024)
	}

	elementSize := proto.Size(batch[0])
	maxBytes := elementSize + 10 // Forces splitting down to single elements.

	tests := []struct {
		name          string
		depth         int
		maxSubBatches int
	}{
		{"depth_0_max_16", 0, 16},
		{"depth_1_max_8", 1, 8},
		{"depth_2_max_4", 2, 4},
		{"depth_3_max_2", 3, 2},
		{"depth_4_max_1", 4, 1}, // At max depth, no splitting occurs.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitByBytesWithDepth(batch, maxBytes, tt.depth)

			if len(result) > tt.maxSubBatches {
				t.Errorf("at depth %d, expected at most %d sub-batches, got %d",
					tt.depth, tt.maxSubBatches, len(result))
			}

			// All elements preserved.
			totalElements := 0
			for _, sub := range result {
				totalElements += len(sub)
			}
			if totalElements != 8 {
				t.Errorf("expected 8 total elements, got %d", totalElements)
			}
		})
	}
}

// TestSplitByBytes_TwoElements_JustOverLimit verifies splitting of a
// two-element batch where total exceeds maxBytes.
func TestSplitByBytes_TwoElements_JustOverLimit(t *testing.T) {
	rm1 := createTestResourceMetricsWithSize(1024)
	rm2 := createTestResourceMetricsWithSize(1024)
	batch := []*metricspb.ResourceMetrics{rm1, rm2}

	// maxBytes = size of one element (forces a split into two).
	maxBytes := proto.Size(rm1) + 10

	result := splitByBytes(batch, maxBytes)

	if len(result) != 2 {
		t.Errorf("expected 2 sub-batches, got %d", len(result))
	}

	totalElements := 0
	for _, sub := range result {
		totalElements += len(sub)
	}
	if totalElements != 2 {
		t.Errorf("expected 2 total elements, got %d", totalElements)
	}
}

// TestSplitByBytes_ElementOrderPreserved verifies that the order of elements
// is preserved after splitting.
func TestSplitByBytes_ElementOrderPreserved(t *testing.T) {
	// Create 8 elements with distinct names.
	batch := make([]*metricspb.ResourceMetrics, 8)
	for i := 0; i < 8; i++ {
		batch[i] = createTestResourceMetricsWithSize(1024)
		// Tag each with a unique metric name.
		batch[i].ScopeMetrics[0].Metrics[0].Name = string(rune('A' + i))
	}

	elementSize := proto.Size(batch[0])
	maxBytes := elementSize*2 + 10 // ~2 elements per sub-batch.

	result := splitByBytes(batch, maxBytes)

	// Flatten and check order.
	var names []string
	for _, sub := range result {
		for _, rm := range sub {
			names = append(names, rm.ScopeMetrics[0].Metrics[0].Name)
		}
	}

	expected := []string{"A", "B", "C", "D", "E", "F", "G", "H"}
	if len(names) != len(expected) {
		t.Fatalf("expected %d names, got %d", len(expected), len(names))
	}
	for i := range expected {
		if names[i] != expected[i] {
			t.Errorf("position %d: expected %q, got %q", i, expected[i], names[i])
		}
	}
}
