package buffer

import (
	"sync"
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

func createTestRequest(n int) *colmetricspb.ExportMetricsServiceRequest {
	rms := make([]*metricspb.ResourceMetrics, n)
	for i := 0; i < n; i++ {
		rms[i] = &metricspb.ResourceMetrics{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "test"}}},
				},
			},
			ScopeMetrics: []*metricspb.ScopeMetrics{{
				Metrics: []*metricspb.Metric{{
					Name: "test_metric",
					Data: &metricspb.Metric_Gauge{Gauge: &metricspb.Gauge{
						DataPoints: []*metricspb.NumberDataPoint{{
							Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 1.0},
						}},
					}},
				}},
			}},
		}
	}
	return &colmetricspb.ExportMetricsServiceRequest{ResourceMetrics: rms}
}

func TestMemoryQueue_PushPop(t *testing.T) {
	q := NewMemoryQueue(10, 1024*1024)

	req := createTestRequest(1)
	if err := q.Push(req); err != nil {
		t.Fatalf("Push failed: %v", err)
	}

	if q.Len() != 1 {
		t.Errorf("expected Len=1, got %d", q.Len())
	}

	got := q.Pop()
	if got == nil {
		t.Fatal("expected non-nil Pop result")
	}
	if len(got.ResourceMetrics) != 1 {
		t.Errorf("expected 1 ResourceMetric, got %d", len(got.ResourceMetrics))
	}
	if q.Len() != 0 {
		t.Errorf("expected Len=0 after Pop, got %d", q.Len())
	}
}

func TestMemoryQueue_PopEmpty(t *testing.T) {
	q := NewMemoryQueue(10, 1024*1024)

	got := q.Pop()
	if got != nil {
		t.Error("expected nil from empty queue")
	}
}

func TestMemoryQueue_EvictionByCount(t *testing.T) {
	q := NewMemoryQueue(3, 100*1024*1024)

	for i := 0; i < 5; i++ {
		if err := q.Push(createTestRequest(1)); err != nil {
			t.Fatalf("Push %d failed: %v", i, err)
		}
	}

	if q.Len() != 3 {
		t.Errorf("expected Len=3 after eviction, got %d", q.Len())
	}
}

func TestMemoryQueue_EvictionByBytes(t *testing.T) {
	req := createTestRequest(1)
	reqSize := int64(proto.Size(req))

	// Allow 2.5 requests worth of bytes
	q := NewMemoryQueue(100, reqSize*2+reqSize/2)

	for i := 0; i < 5; i++ {
		if err := q.Push(createTestRequest(1)); err != nil {
			t.Fatalf("Push %d failed: %v", i, err)
		}
	}

	if q.Len() != 2 {
		t.Errorf("expected Len=2 after byte eviction, got %d", q.Len())
	}
}

func TestMemoryQueue_RejectOversized(t *testing.T) {
	q := NewMemoryQueue(100, 10) // Very small max bytes

	err := q.Push(createTestRequest(5)) // Definitely > 10 bytes
	if err == nil {
		t.Error("expected error for oversized entry")
	}
}

func TestMemoryQueue_Size(t *testing.T) {
	q := NewMemoryQueue(100, 1024*1024)

	if q.Size() != 0 {
		t.Errorf("expected Size=0 initially, got %d", q.Size())
	}

	req := createTestRequest(1)
	expectedSize := int64(proto.Size(req))
	_ = q.Push(req)

	if q.Size() != expectedSize {
		t.Errorf("expected Size=%d, got %d", expectedSize, q.Size())
	}
}

func TestMemoryQueue_Concurrent(t *testing.T) {
	q := NewMemoryQueue(1000, 100*1024*1024)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = q.Push(createTestRequest(1))
			}
		}()
	}
	wg.Wait()

	if q.Len() != 1000 {
		t.Errorf("expected Len=1000, got %d", q.Len())
	}

	// Pop all
	count := 0
	for q.Pop() != nil {
		count++
	}
	if count != 1000 {
		t.Errorf("expected 1000 pops, got %d", count)
	}
}

func TestMemoryQueue_FIFOOrder(t *testing.T) {
	q := NewMemoryQueue(10, 1024*1024)

	for i := 1; i <= 3; i++ {
		_ = q.Push(createTestRequest(i))
	}

	for i := 1; i <= 3; i++ {
		got := q.Pop()
		if got == nil {
			t.Fatalf("unexpected nil at position %d", i)
		}
		if len(got.ResourceMetrics) != i {
			t.Errorf("expected %d ResourceMetrics at position %d, got %d", i, i, len(got.ResourceMetrics))
		}
	}
}

func TestMemoryQueue_DefaultValues(t *testing.T) {
	q := NewMemoryQueue(0, 0)

	if q.maxSize != 1000 {
		t.Errorf("expected default maxSize=1000, got %d", q.maxSize)
	}
	if q.maxBytes != 256*1024*1024 {
		t.Errorf("expected default maxBytes=256MB, got %d", q.maxBytes)
	}
}
