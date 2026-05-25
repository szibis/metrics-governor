package autotune

import "context"

// DesignatedElector uses a static flag to determine leadership.
// Set DesignatedLeader=true on exactly one pod in the deployment.
type DesignatedElector struct {
	leader bool
}

// NewDesignatedElector creates a flag-based leader elector.
func NewDesignatedElector(isLeader bool) *DesignatedElector {
	return &DesignatedElector{leader: isLeader}
}

// IsLeader returns the configured leader flag.
func (d *DesignatedElector) IsLeader() bool { return d.leader }

// Start is a no-op for designated election.
func (d *DesignatedElector) Start(_ context.Context) error { return nil }

// Stop is a no-op for designated election.
func (d *DesignatedElector) Stop() {}
