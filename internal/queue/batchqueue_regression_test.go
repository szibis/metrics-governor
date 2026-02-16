package queue

import (
	"fmt"
	"sync"
	"testing"
	"time"

	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
)

// TestRegression_BalancedProfile_MemoryQueue_NoDataLoss pushes 10k batches
// concurrently (100 goroutines x 100 batches each) into a MemoryBatchQueue
// configured with balanced-profile-optimized parameters (MaxSize=5000,
// MaxBytes=42MB) and verifies zero data loss.
func TestRegression_BalancedProfile_MemoryQueue_NoDataLoss(t *testing.T) {
	t.Parallel()

	const (
		numGoroutines = 100
		batchesPerGR  = 100
		totalBatches  = numGoroutines * batchesPerGR
		maxBytes      = int64(44040192) // 42MB (balanced profile: 0.05 * 850MB GOMEMLIMIT)
	)

	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      totalBatches + 100, // large enough so count limit is not the bottleneck
		MaxBytes:     maxBytes,
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
										{Name: fmt.Sprintf("balanced_batch_%d", batchID)},
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
		key := fmt.Sprintf("balanced_batch_%d", i)
		if _, ok := seen.Load(key); !ok {
			t.Errorf("missing batch: %s", key)
		}
	}
}

// TestRegression_BalancedProfile_MemoryQueue_OrderPreserved pushes 2000
// sequentially numbered batches (matching balanced profile BufferSize=2000)
// into the queue and verifies strict FIFO ordering on pop.
func TestRegression_BalancedProfile_MemoryQueue_OrderPreserved(t *testing.T) {
	t.Parallel()

	const numBatches = 2000 // balanced profile BufferSize

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
								{Name: fmt.Sprintf("balanced_seq_%d", i)},
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
		expected := fmt.Sprintf("balanced_seq_%d", i)
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

// TestRegression_BalancedProfile_MemoryQueue_MemoryBounded pushes batches
// with EstimatedBytes=200 into a queue with a byte limit until the queue
// rejects (DropNewest policy). It verifies that queue Size() never exceeds
// MaxBytes throughout the process.
func TestRegression_BalancedProfile_MemoryQueue_MemoryBounded(t *testing.T) {
	t.Parallel()

	const (
		maxBytes       = int64(44040192) // 42MB (balanced profile)
		estimatedBytes = 200
		// Push enough batches to exceed the byte limit.
		numBatches = int(maxBytes/estimatedBytes) + 100
	)

	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      numBatches + 100, // count limit should not be the bottleneck
		MaxBytes:     maxBytes,
		FullBehavior: DropNewest,
	})
	defer q.Close()

	rejected := 0
	for i := 0; i < numBatches; i++ {
		batch := &ExportBatch{
			ResourceMetrics: []*metricspb.ResourceMetrics{{}},
			Timestamp:       time.Now(),
			EstimatedBytes:  estimatedBytes,
		}
		err := q.PushBatch(batch)
		if err == ErrBatchQueueFull {
			rejected++
		} else if err != nil {
			t.Fatalf("unexpected error at push %d: %v", i, err)
		}

		// Verify the queue never exceeds the byte limit.
		currentSize := q.Size()
		if currentSize > maxBytes {
			t.Fatalf("queue Size() %d exceeds MaxBytes %d after push %d", currentSize, maxBytes, i)
		}
	}

	if rejected == 0 {
		t.Fatal("expected at least one rejection when exceeding byte limit")
	}

	t.Logf("pushed %d batches, %d rejected, final Size()=%d, MaxBytes=%d",
		numBatches, rejected, q.Size(), maxBytes)
}

// TestRegression_BalancedProfile_MemoryQueue_ReducedBlocks creates a queue
// with MaxSize=512 (matching balanced profile QueueInmemoryBlocks=512),
// pushes exactly 512 batches to fill it, verifies all are retrievable,
// then confirms the 513th batch is rejected with DropNewest.
func TestRegression_BalancedProfile_MemoryQueue_ReducedBlocks(t *testing.T) {
	t.Parallel()

	const maxSize = 512 // balanced profile QueueInmemoryBlocks

	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      maxSize,
		FullBehavior: DropNewest,
	})
	defer q.Close()

	// Push exactly maxSize batches; all should succeed.
	for i := 0; i < maxSize; i++ {
		batch := &ExportBatch{
			ResourceMetrics: []*metricspb.ResourceMetrics{
				{
					ScopeMetrics: []*metricspb.ScopeMetrics{
						{
							Metrics: []*metricspb.Metric{
								{Name: fmt.Sprintf("block_%d", i)},
							},
						},
					},
				},
			},
			Timestamp:      time.Now(),
			EstimatedBytes: 100,
		}
		if err := q.PushBatch(batch); err != nil {
			t.Fatalf("push %d of %d failed: %v", i, maxSize, err)
		}
	}

	if q.Len() != maxSize {
		t.Fatalf("expected Len()=%d after filling queue, got %d", maxSize, q.Len())
	}

	// The 257th push must be rejected.
	overflowBatch := &ExportBatch{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{Name: "overflow_batch"},
						},
					},
				},
			},
		},
		Timestamp:      time.Now(),
		EstimatedBytes: 100,
	}
	err := q.PushBatch(overflowBatch)
	if err != ErrBatchQueueFull {
		t.Fatalf("expected ErrBatchQueueFull for batch %d, got: %v", maxSize+1, err)
	}

	// Queue length should remain at maxSize.
	if q.Len() != maxSize {
		t.Fatalf("expected Len()=%d after rejected push, got %d", maxSize, q.Len())
	}

	// Pop all and verify all 256 batches are retrievable in order.
	for i := 0; i < maxSize; i++ {
		batch, popErr := q.PopBatch()
		if popErr != nil {
			t.Fatalf("pop %d error: %v", i, popErr)
		}
		if batch == nil {
			t.Fatalf("pop %d returned nil, expected batch", i)
		}
		expected := fmt.Sprintf("block_%d", i)
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
