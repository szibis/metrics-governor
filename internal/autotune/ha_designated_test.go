package autotune

import (
	"context"
	"testing"
)

func TestDesignatedElector_Leader(t *testing.T) {
	e := NewDesignatedElector(true)
	if !e.IsLeader() {
		t.Error("expected IsLeader=true for designated leader")
	}

	// Start/Stop should be no-ops.
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	e.Stop()
}

func TestDesignatedElector_Follower(t *testing.T) {
	e := NewDesignatedElector(false)
	if e.IsLeader() {
		t.Error("expected IsLeader=false for non-designated pod")
	}
}
