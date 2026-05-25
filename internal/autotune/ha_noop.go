package autotune

import "context"

// NoopElector always reports as leader. Used for single-pod deployments.
type NoopElector struct{}

// NewNoopElector creates a noop leader elector.
func NewNoopElector() *NoopElector {
	return &NoopElector{}
}

// IsLeader always returns true.
func (n *NoopElector) IsLeader() bool { return true }

// Start is a no-op.
func (n *NoopElector) Start(_ context.Context) error { return nil }

// Stop is a no-op.
func (n *NoopElector) Stop() {}
