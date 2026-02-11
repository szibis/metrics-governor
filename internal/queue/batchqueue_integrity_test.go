package queue

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
)

// TestDataIntegrity_MemoryQueue_NoDataLoss pushes N batches concurrently from
// 100 goroutines (10 batches each) and verifies every batch is received
// exactly once with no duplicates or losses.
func TestDataIntegrity_MemoryQueue_NoDataLoss(t *testing.T) {
	t.Parallel()

	const (
		numGoroutines = 100
		batchesPerGR  = 10
		totalBatches  = numGoroutines * batchesPerGR
	)

	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      totalBatches + 100, // large enough so nothing is dropped
		FullBehavior: DropNewest,
	})
	defer q.Close()

	// Each batch gets a unique ID encoded in the metric name.
	var wg sync.WaitGroup
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for b := 0; b < batchesPerGR; b++ {
				batchID := goroutineID*batchesPerGR + b
				batch := &ExportBatch{
					ResourceMetrics: []*metricspb.ResourceMetrics{
						{
							ScopeMetrics: []*metricspb.ScopeMetrics{
								{
									Metrics: []*metricspb.Metric{
										{Name: fmt.Sprintf("batch_%d", batchID)},
									},
								},
							},
						},
					},
					Timestamp:      time.Now(),
					EstimatedBytes: 100,
				}
				if err := q.PushBatch(batch); err != nil {
					t.Errorf("push failed for batch %d: %v", batchID, err)
				}
			}
		}(g)
	}
	wg.Wait()

	// Pop all batches and track which IDs we received.
	seen := &sync.Map{}
	received := 0
	for {
		batch, err := q.PopBatch()
		if err != nil {
			t.Fatalf("unexpected pop error: %v", err)
		}
		if batch == nil {
			break
		}
		received++
		name := batch.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].Name
		if _, loaded := seen.LoadOrStore(name, true); loaded {
			t.Errorf("duplicate batch received: %s", name)
		}
	}

	if received != totalBatches {
		t.Fatalf("expected %d batches, got %d", totalBatches, received)
	}

	// Verify all IDs are present.
	for i := 0; i < totalBatches; i++ {
		key := fmt.Sprintf("batch_%d", i)
		if _, ok := seen.Load(key); !ok {
			t.Errorf("missing batch: %s", key)
		}
	}
}

// TestDataIntegrity_MemoryQueue_OrderPreservation pushes 1000 sequentially
// numbered batches from a single goroutine and verifies FIFO ordering on pop.
func TestDataIntegrity_MemoryQueue_OrderPreservation(t *testing.T) {
	t.Parallel()

	const numBatches = 1000

	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      numBatches + 100,
		FullBehavior: DropNewest,
	})
	defer q.Close()

	// Push sequentially.
	for i := 0; i < numBatches; i++ {
		batch := &ExportBatch{
			ResourceMetrics: []*metricspb.ResourceMetrics{
				{
					ScopeMetrics: []*metricspb.ScopeMetrics{
						{
							Metrics: []*metricspb.Metric{
								{Name: fmt.Sprintf("seq_%d", i)},
							},
						},
					},
				},
			},
			Timestamp:      time.Now(),
			EstimatedBytes: 50,
		}
		if err := q.PushBatch(batch); err != nil {
			t.Fatalf("push %d failed: %v", i, err)
		}
	}

	// Pop and verify order.
	for i := 0; i < numBatches; i++ {
		batch, err := q.PopBatch()
		if err != nil {
			t.Fatalf("pop %d error: %v", i, err)
		}
		if batch == nil {
			t.Fatalf("pop %d returned nil, expected batch", i)
		}
		expected := fmt.Sprintf("seq_%d", i)
		got := batch.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].Name
		if got != expected {
			t.Fatalf("order violation at position %d: expected %q, got %q", i, expected, got)
		}
	}

	// Queue should be empty.
	batch, _ := q.PopBatch()
	if batch != nil {
		t.Fatal("queue should be empty after draining all batches")
	}
}

// TestDataIntegrity_MemoryQueue_ContentPreservation pushes batches with known
// ResourceMetrics content and verifies the popped content is identical.
func TestDataIntegrity_MemoryQueue_ContentPreservation(t *testing.T) {
	t.Parallel()

	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      100,
		FullBehavior: DropNewest,
	})
	defer q.Close()

	type testCase struct {
		metricName string
		labelKey   string
		labelValue string
		dpValue    float64
		dpCount    int
	}

	cases := []testCase{
		{"http_requests_total", "method", "GET", 42.0, 1},
		{"cpu_usage_percent", "host", "node-01", 87.5, 1},
		{"memory_bytes_used", "region", "us-east-1", 1073741824.0, 1},
		{"request_duration_seconds", "service", "api-gateway", 0.125, 1},
		{"error_count", "code", "500", 7.0, 1},
	}

	// Push batches with rich content.
	for _, tc := range cases {
		dps := make([]*metricspb.NumberDataPoint, tc.dpCount)
		for d := 0; d < tc.dpCount; d++ {
			dps[d] = &metricspb.NumberDataPoint{
				Attributes: []*commonpb.KeyValue{
					{
						Key:   tc.labelKey,
						Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: tc.labelValue}},
					},
				},
				Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: tc.dpValue},
			}
		}

		batch := &ExportBatch{
			ResourceMetrics: []*metricspb.ResourceMetrics{
				{
					Resource: &resourcepb.Resource{
						Attributes: []*commonpb.KeyValue{
							{
								Key:   "service.name",
								Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "test-svc"}},
							},
						},
					},
					ScopeMetrics: []*metricspb.ScopeMetrics{
						{
							Metrics: []*metricspb.Metric{
								{
									Name: tc.metricName,
									Data: &metricspb.Metric_Gauge{
										Gauge: &metricspb.Gauge{
											DataPoints: dps,
										},
									},
								},
							},
						},
					},
				},
			},
			Timestamp:      time.Now(),
			EstimatedBytes: 200,
		}
		if err := q.PushBatch(batch); err != nil {
			t.Fatalf("push %s failed: %v", tc.metricName, err)
		}
	}

	// Pop and verify content preservation.
	for _, tc := range cases {
		batch, err := q.PopBatch()
		if err != nil {
			t.Fatalf("pop error for %s: %v", tc.metricName, err)
		}
		if batch == nil {
			t.Fatalf("pop returned nil for expected metric %s", tc.metricName)
		}

		rm := batch.ResourceMetrics[0]

		// Verify resource attributes.
		if len(rm.Resource.Attributes) != 1 {
			t.Fatalf("expected 1 resource attribute, got %d", len(rm.Resource.Attributes))
		}
		if rm.Resource.Attributes[0].Key != "service.name" {
			t.Fatalf("expected resource key 'service.name', got %q", rm.Resource.Attributes[0].Key)
		}
		svcName := rm.Resource.Attributes[0].Value.GetStringValue()
		if svcName != "test-svc" {
			t.Fatalf("expected resource value 'test-svc', got %q", svcName)
		}

		// Verify metric name.
		metric := rm.ScopeMetrics[0].Metrics[0]
		if metric.Name != tc.metricName {
			t.Fatalf("expected metric name %q, got %q", tc.metricName, metric.Name)
		}

		// Verify data points.
		gauge := metric.GetGauge()
		if gauge == nil {
			t.Fatalf("expected gauge data for %s, got nil", tc.metricName)
		}
		if len(gauge.DataPoints) != tc.dpCount {
			t.Fatalf("expected %d data points for %s, got %d", tc.dpCount, tc.metricName, len(gauge.DataPoints))
		}

		dp := gauge.DataPoints[0]

		// Verify label key/value.
		if len(dp.Attributes) != 1 {
			t.Fatalf("expected 1 attribute on dp, got %d", len(dp.Attributes))
		}
		if dp.Attributes[0].Key != tc.labelKey {
			t.Fatalf("expected label key %q, got %q", tc.labelKey, dp.Attributes[0].Key)
		}
		if dp.Attributes[0].Value.GetStringValue() != tc.labelValue {
			t.Fatalf("expected label value %q, got %q", tc.labelValue, dp.Attributes[0].Value.GetStringValue())
		}

		// Verify dp value.
		if dp.GetAsDouble() != tc.dpValue {
			t.Fatalf("expected dp value %f for %s, got %f", tc.dpValue, tc.metricName, dp.GetAsDouble())
		}
	}
}

// TestDataIntegrity_MemoryQueue_ByteTracking pushes batches with known
// EstimatedBytes, verifies Size() grows correctly, pops some, verifies
// Size() shrinks correctly, and verifies the end state is 0.
func TestDataIntegrity_MemoryQueue_ByteTracking(t *testing.T) {
	t.Parallel()

	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      100,
		FullBehavior: DropNewest,
	})
	defer q.Close()

	if q.Size() != 0 {
		t.Fatalf("initial Size() should be 0, got %d", q.Size())
	}
	if q.Len() != 0 {
		t.Fatalf("initial Len() should be 0, got %d", q.Len())
	}

	// Push batches with specific sizes and verify cumulative tracking.
	sizes := []int{100, 250, 75, 500, 125}
	expectedBytes := int64(0)
	for i, sz := range sizes {
		batch := &ExportBatch{
			ResourceMetrics: []*metricspb.ResourceMetrics{{}},
			Timestamp:       time.Now(),
			EstimatedBytes:  sz,
		}
		if err := q.PushBatch(batch); err != nil {
			t.Fatalf("push %d failed: %v", i, err)
		}
		expectedBytes += int64(sz)
		if q.Size() != expectedBytes {
			t.Fatalf("after push %d: expected Size()=%d, got %d", i, expectedBytes, q.Size())
		}
		if q.Len() != i+1 {
			t.Fatalf("after push %d: expected Len()=%d, got %d", i, i+1, q.Len())
		}
	}

	// Pop first 3 batches and verify byte tracking shrinks.
	for i := 0; i < 3; i++ {
		batch, err := q.PopBatch()
		if err != nil {
			t.Fatalf("pop %d error: %v", i, err)
		}
		if batch == nil {
			t.Fatalf("pop %d returned nil", i)
		}
		expectedBytes -= int64(batch.EstimatedBytes)
		if q.Size() != expectedBytes {
			t.Fatalf("after pop %d: expected Size()=%d, got %d", i, expectedBytes, q.Size())
		}
	}

	// Verify remaining count.
	remaining := len(sizes) - 3
	if q.Len() != remaining {
		t.Fatalf("expected Len()=%d after popping 3, got %d", remaining, q.Len())
	}

	// Pop the rest.
	for i := 0; i < remaining; i++ {
		batch, err := q.PopBatch()
		if err != nil {
			t.Fatalf("drain pop %d error: %v", i, err)
		}
		if batch == nil {
			t.Fatalf("drain pop %d returned nil", i)
		}
		expectedBytes -= int64(batch.EstimatedBytes)
	}

	// End state: everything should be 0.
	if q.Size() != 0 {
		t.Fatalf("final Size() should be 0, got %d", q.Size())
	}
	if q.Len() != 0 {
		t.Fatalf("final Len() should be 0, got %d", q.Len())
	}
	if expectedBytes != 0 {
		t.Fatalf("expected running byte total to be 0, got %d", expectedBytes)
	}
}

// TestDataIntegrity_MemoryQueue_ConcurrentPushPop runs pushers and poppers
// concurrently for 2 seconds and verifies no panics, no data corruption,
// and total popped <= total pushed.
func TestDataIntegrity_MemoryQueue_ConcurrentPushPop(t *testing.T) {
	t.Parallel()

	const (
		numPushers = 10
		numPoppers = 5
		duration   = 2 * time.Second
	)

	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      500,
		FullBehavior: DropOldest,
	})

	var totalPushed atomic.Int64
	var totalPopped atomic.Int64
	stop := make(chan struct{})

	var wg sync.WaitGroup

	// Start pushers.
	for p := 0; p < numPushers; p++ {
		wg.Add(1)
		go func(pusherID int) {
			defer wg.Done()
			seq := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				batch := &ExportBatch{
					ResourceMetrics: []*metricspb.ResourceMetrics{
						{
							ScopeMetrics: []*metricspb.ScopeMetrics{
								{
									Metrics: []*metricspb.Metric{
										{Name: fmt.Sprintf("pusher_%d_seq_%d", pusherID, seq)},
									},
								},
							},
						},
					},
					Timestamp:      time.Now(),
					EstimatedBytes: 64,
				}
				if err := q.PushBatch(batch); err == nil {
					totalPushed.Add(1)
				}
				seq++
			}
		}(p)
	}

	// Start poppers.
	for p := 0; p < numPoppers; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				batch, err := q.PopBatch()
				if err != nil {
					return
				}
				if batch == nil {
					// Queue is temporarily empty; yield.
					time.Sleep(100 * time.Microsecond)
					continue
				}
				totalPopped.Add(1)

				// Verify the batch is not corrupted: must have valid structure.
				if len(batch.ResourceMetrics) != 1 {
					t.Errorf("corrupted batch: expected 1 ResourceMetrics, got %d", len(batch.ResourceMetrics))
					return
				}
				if len(batch.ResourceMetrics[0].ScopeMetrics) != 1 {
					t.Errorf("corrupted batch: expected 1 ScopeMetrics, got %d", len(batch.ResourceMetrics[0].ScopeMetrics))
					return
				}
				if len(batch.ResourceMetrics[0].ScopeMetrics[0].Metrics) != 1 {
					t.Errorf("corrupted batch: expected 1 Metric, got %d", len(batch.ResourceMetrics[0].ScopeMetrics[0].Metrics))
					return
				}
				name := batch.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].Name
				if len(name) == 0 {
					t.Errorf("corrupted batch: empty metric name")
					return
				}
			}
		}()
	}

	// Let them run for the specified duration.
	time.Sleep(duration)
	close(stop)

	// Close the queue so poppers unblock and drain.
	q.Close()
	wg.Wait()

	pushed := totalPushed.Load()
	popped := totalPopped.Load()

	t.Logf("pushed=%d popped=%d", pushed, popped)

	if pushed == 0 {
		t.Fatal("no batches were pushed; test is ineffective")
	}
	if popped == 0 {
		t.Fatal("no batches were popped; test is ineffective")
	}
	if popped > pushed {
		t.Fatalf("popped (%d) exceeds pushed (%d); phantom data", popped, pushed)
	}
}
