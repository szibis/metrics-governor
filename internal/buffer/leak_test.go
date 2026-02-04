package buffer

import (
	"context"
	"testing"
	"time"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"go.uber.org/goleak"
)

func TestLeakCheck_MetricsBuffer(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	exporter := &mockExporter{}
	stats := &mockStatsCollector{}
	buf := New(100, 50, 100*time.Millisecond, exporter, stats, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go buf.Start(ctx)

	// Add some data and let it flush
	buf.Add(createTestResourceMetrics(10))
	time.Sleep(200 * time.Millisecond)

	cancel()
	buf.Wait()
}

func TestLeakCheck_MemoryQueue(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	q := NewMemoryQueue(100, 1024*1024)

	// Push and pop some entries
	for i := 0; i < 10; i++ {
		rm := createTestResourceMetrics(1)
		req := &colmetricspb.ExportMetricsServiceRequest{
			ResourceMetrics: rm,
		}
		if err := q.Push(req); err != nil {
			t.Fatalf("Push failed: %v", err)
		}
	}

	for q.Len() > 0 {
		q.Pop()
	}
}

