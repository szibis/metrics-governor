package exporter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	prw "github.com/szibis/metrics-governor/internal/prw"
)

// TestPRWShardedBuffer_StartAndWait tests the Start and Wait methods
// of PRWShardedBuffer by using a short-lived context.
func TestPRWShardedBuffer_StartAndWait(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	shardedCfg := PRWShardedConfig{
		BaseConfig: PRWExporterConfig{
			Endpoint: server.URL,
			Timeout:  5 * time.Second,
		},
		Endpoints:    []string{server.URL},
		VirtualNodes: 10,
	}

	shardedExp, err := NewPRWSharded(context.Background(), shardedCfg)
	if err != nil {
		t.Fatalf("NewPRWSharded: %v", err)
	}
	defer shardedExp.Close()

	bufCfg := prw.BufferConfig{
		MaxSize:       100,
		MaxBatchSize:  10,
		FlushInterval: 50 * time.Millisecond, // Short so the ticker fires quickly
	}

	buf := NewPRWShardedBuffer(
		bufCfg,
		shardedExp,
		&mockPRWStatsCollector{},
		&mockPRWLimitsEnforcer{},
		&mockLogAggregator{},
	)

	if buf == nil {
		t.Fatal("NewPRWShardedBuffer returned nil")
	}

	// Add a request before starting
	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels:  []prw.Label{{Name: "__name__", Value: "test_metric_start"}},
				Samples: []prw.Sample{{Value: 42, Timestamp: time.Now().UnixMilli()}},
			},
		},
	}
	buf.Add(req)

	// Start with a context that we cancel shortly after
	ctx, cancel := context.WithCancel(context.Background())

	// Start the buffer loop
	go buf.Start(ctx)

	// Give it a moment to start processing
	time.Sleep(100 * time.Millisecond)

	// Cancel the context to trigger shutdown
	cancel()

	// Wait should return once the context is canceled and the loop exits
	done := make(chan struct{})
	go func() {
		buf.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Log("Start/Wait completed successfully")
	case <-time.After(5 * time.Second):
		t.Error("Wait did not return within 5 seconds")
	}
}

// TestSpilloverRateLimiter_Exhaustion tests that the rate limiter
// properly exhausts tokens and then refills them.
func TestSpilloverRateLimiter_Exhaustion(t *testing.T) {
	rl := newSpilloverRateLimiter(5) // 5 ops/sec

	// Should allow 5 operations immediately
	for i := 0; i < 5; i++ {
		if !rl.Allow() {
			t.Fatalf("Allow() returned false on call %d, expected true", i)
		}
	}

	// 6th should be denied
	if rl.Allow() {
		t.Error("Allow() should return false after exhausting tokens")
	}

	// Wait for refill (at least 1 second for 5 ops/sec)
	time.Sleep(1100 * time.Millisecond)

	// Should allow again after refill
	if !rl.Allow() {
		t.Error("Allow() should return true after refill")
	}
}

// TestSpilloverRateLimiter_TokenCapping tests that tokens don't exceed maxPerSec.
func TestSpilloverRateLimiter_TokenCapping(t *testing.T) {
	rl := newSpilloverRateLimiter(3) // 3 ops/sec

	// Wait a long time -- tokens should cap at maxPerSec
	time.Sleep(2100 * time.Millisecond)

	// Should allow exactly 3
	count := 0
	for i := 0; i < 10; i++ {
		if rl.Allow() {
			count++
		}
	}

	if count != 3 {
		t.Errorf("expected 3 allows after long wait (capped at maxPerSec), got %d", count)
	}
}
