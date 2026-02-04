package stats

import (
	"context"
	"testing"
	"time"

	"go.uber.org/goleak"
)

func TestLeakCheck_StatsCollector(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	c := NewCollector([]string{"service"})

	ctx, cancel := context.WithCancel(context.Background())

	// Start periodic logging in background
	done := make(chan struct{})
	go func() {
		c.StartPeriodicLogging(ctx, 100*time.Millisecond)
		close(done)
	}()

	// Process some data
	rm := makeResourceMetrics(t, 0, 0)
	c.Process(rm)

	// Let a few ticks pass
	time.Sleep(300 * time.Millisecond)

	// Shutdown
	cancel()
	<-done
}
