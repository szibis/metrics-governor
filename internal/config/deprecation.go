package config

import (
	"fmt"
	"strconv"
	"strings"
)

// DeprecationStage represents the lifecycle stage of a deprecation.
type DeprecationStage int

const (
	StageAnnounced  DeprecationStage = iota // INFO log
	StageDeprecated                         // WARN log
	StageWarning                            // WARN + banner
	StageRemoved                            // ERROR / startup fail with --strict
)

func (s DeprecationStage) String() string {
	switch s {
	case StageAnnounced:
		return "ANNOUNCED"
	case StageDeprecated:
		return "DEPRECATED"
	case StageWarning:
		return "WARNING"
	case StageRemoved:
		return "REMOVED"
	default:
		return "UNKNOWN"
	}
}

// DeprecationEntry describes a single deprecated parameter.
type DeprecationEntry struct {
	OldName      string // deprecated param name (CLI flag name)
	NewName      string // replacement param name
	Message      string // human-readable migration instructions
	AnnouncedIn  string // version when first announced (e.g., "0.30.0")
	DeprecatedIn string // version when deprecated (e.g., "0.31.0")
	RemovedIn    string // version when removed (e.g., "1.0.0")
}

// DeprecationWarning is the result of checking a single deprecation against usage.
type DeprecationWarning struct {
	Entry   DeprecationEntry
	Stage   DeprecationStage
	InUse   bool   // true if the user is actively using the deprecated param
	Message string // formatted warning message
}

// DeprecationRegistry tracks all deprecations and checks usage against version.
type DeprecationRegistry struct {
	entries        []DeprecationEntry
	currentVersion semVersion
}

// semVersion is a minimal semver for comparison (major.minor.patch).
type semVersion struct {
	Major int
	Minor int
	Patch int
}

func parseSemVersion(s string) semVersion {
	s = strings.TrimPrefix(s, "v")
	parts := strings.SplitN(s, ".", 3)
	var v semVersion
	if len(parts) >= 1 {
		v.Major, _ = strconv.Atoi(parts[0])
	}
	if len(parts) >= 2 {
		v.Minor, _ = strconv.Atoi(parts[1])
	}
	if len(parts) >= 3 {
		// Handle pre-release suffixes like "0.30.0-beta"
		patch := parts[2]
		if idx := strings.IndexAny(patch, "-+"); idx >= 0 {
			patch = patch[:idx]
		}
		v.Patch, _ = strconv.Atoi(patch)
	}
	return v
}

// Less returns true if v is strictly before other.
func (v semVersion) Less(other semVersion) bool {
	if v.Major != other.Major {
		return v.Major < other.Major
	}
	if v.Minor != other.Minor {
		return v.Minor < other.Minor
	}
	return v.Patch < other.Patch
}

// Equal returns true if versions match.
func (v semVersion) Equal(other semVersion) bool {
	return v.Major == other.Major && v.Minor == other.Minor && v.Patch == other.Patch
}

// LessOrEqual returns true if v <= other.
func (v semVersion) LessOrEqual(other semVersion) bool {
	return v.Less(other) || v.Equal(other)
}

// MinorDistance returns the number of minor versions between v and other.
func (v semVersion) MinorDistance(other semVersion) int {
	if v.Major != other.Major {
		return 100 // large distance for cross-major
	}
	d := other.Minor - v.Minor
	if d < 0 {
		d = -d
	}
	return d
}

// NewDeprecationRegistry creates a registry with the given current version.
func NewDeprecationRegistry(currentVersion string) *DeprecationRegistry {
	return &DeprecationRegistry{
		currentVersion: parseSemVersion(currentVersion),
	}
}

// Register adds a deprecation entry.
func (r *DeprecationRegistry) Register(entry DeprecationEntry) {
	r.entries = append(r.entries, entry)
}

// computeStage determines the lifecycle stage based on current version.
func (r *DeprecationRegistry) computeStage(entry DeprecationEntry) DeprecationStage {
	removed := parseSemVersion(entry.RemovedIn)
	deprecated := parseSemVersion(entry.DeprecatedIn)

	// If current >= removedIn: REMOVED
	if removed.LessOrEqual(r.currentVersion) {
		return StageRemoved
	}

	// If deprecated version is set and current >= deprecatedIn
	if entry.DeprecatedIn != "" && deprecated.LessOrEqual(r.currentVersion) {
		// If within 2 minor versions of removal: WARNING
		if r.currentVersion.MinorDistance(removed) <= 2 && r.currentVersion.Major == removed.Major {
			return StageWarning
		}
		return StageDeprecated
	}

	return StageAnnounced
}

// CheckAndWarn evaluates all deprecations against current usage.
// explicitFields maps flag/YAML names to true when the user explicitly set them.
func (r *DeprecationRegistry) CheckAndWarn(explicitFields map[string]bool) []DeprecationWarning {
	var warnings []DeprecationWarning

	for _, entry := range r.entries {
		stage := r.computeStage(entry)
		inUse := explicitFields[entry.OldName]

		if !inUse {
			continue // only warn about params the user is actually using
		}

		var msg string
		switch stage {
		case StageAnnounced:
			msg = fmt.Sprintf("NOTE: --%s will be deprecated in %s, use --%s instead. %s",
				entry.OldName, entry.DeprecatedIn, entry.NewName, entry.Message)
		case StageDeprecated:
			msg = fmt.Sprintf("DEPRECATED: --%s is deprecated since %s, use --%s instead (removal: %s). %s",
				entry.OldName, entry.DeprecatedIn, entry.NewName, entry.RemovedIn, entry.Message)
		case StageWarning:
			msg = fmt.Sprintf("BREAKING CHANGE SOON: --%s will be REMOVED in %s, use --%s instead. %s",
				entry.OldName, entry.RemovedIn, entry.NewName, entry.Message)
		case StageRemoved:
			msg = fmt.Sprintf("REMOVED: --%s was removed in %s, use --%s instead. %s",
				entry.OldName, entry.RemovedIn, entry.NewName, entry.Message)
		}

		warnings = append(warnings, DeprecationWarning{
			Entry:   entry,
			Stage:   stage,
			InUse:   inUse,
			Message: msg,
		})
	}

	return warnings
}

// ListAll returns all registered deprecation entries.
func (r *DeprecationRegistry) ListAll() []DeprecationEntry {
	result := make([]DeprecationEntry, len(r.entries))
	copy(result, r.entries)
	return result
}

// ListActive returns entries in stages Announced, Deprecated, or Warning.
func (r *DeprecationRegistry) ListActive() []DeprecationEntry {
	var result []DeprecationEntry
	for _, entry := range r.entries {
		stage := r.computeStage(entry)
		if stage != StageRemoved {
			result = append(result, entry)
		}
	}
	return result
}

// DumpDeprecations returns a formatted table of all deprecations.
func (r *DeprecationRegistry) DumpDeprecations() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Deprecation Tracker (current version: v%d.%d.%d)\n",
		r.currentVersion.Major, r.currentVersion.Minor, r.currentVersion.Patch)
	b.WriteString(strings.Repeat("=", 75))
	b.WriteString("\n")
	fmt.Fprintf(&b, "%-11s %-30s %-22s %s\n", "Status", "Parameter", "Replacement", "Removal")
	b.WriteString(strings.Repeat("-", 75))
	b.WriteString("\n")

	for _, entry := range r.entries {
		stage := r.computeStage(entry)
		fmt.Fprintf(&b, "%-11s --%s\n", stage, entry.OldName)
		fmt.Fprintf(&b, "%11s Replacement: --%s\n", "", entry.NewName)
		fmt.Fprintf(&b, "%11s Removal: v%s\n", "", entry.RemovedIn)
		if entry.Message != "" {
			fmt.Fprintf(&b, "%11s %s\n", "", entry.Message)
		}
		b.WriteString("\n")
	}

	b.WriteString("\nUse --strict-deprecations to fail startup on removed params.\n")
	b.WriteString("Use --parallelism, --memory-budget-percent, --export-timeout, --resilience-level instead.\n")

	return b.String()
}

// DefaultDeprecations returns the initial set of deprecation entries for v0.30.
func DefaultDeprecations() []DeprecationEntry {
	return []DeprecationEntry{
		// Parallelism consolidation
		{
			OldName:      "queue-workers",
			NewName:      "parallelism",
			Message:      "Use --parallelism to set all worker counts from a single value.",
			AnnouncedIn:  "0.30.0",
			DeprecatedIn: "0.31.0",
			RemovedIn:    "1.0.0",
		},
		{
			OldName:      "export-concurrency",
			NewName:      "parallelism",
			Message:      "Use --parallelism to set all worker counts from a single value.",
			AnnouncedIn:  "0.30.0",
			DeprecatedIn: "0.31.0",
			RemovedIn:    "1.0.0",
		},

		// Memory budget consolidation
		{
			OldName:      "buffer-memory-percent",
			NewName:      "memory-budget-percent",
			Message:      "Use --memory-budget-percent for unified memory allocation.",
			AnnouncedIn:  "0.30.0",
			DeprecatedIn: "0.31.0",
			RemovedIn:    "1.0.0",
		},
		{
			OldName:      "queue-memory-percent",
			NewName:      "memory-budget-percent",
			Message:      "Use --memory-budget-percent for unified memory allocation.",
			AnnouncedIn:  "0.30.0",
			DeprecatedIn: "0.31.0",
			RemovedIn:    "1.0.0",
		},

		// Timeout cascade consolidation
		{
			OldName:      "queue-direct-export-timeout",
			NewName:      "export-timeout",
			Message:      "Use --export-timeout as base for all timeout values.",
			AnnouncedIn:  "0.30.0",
			DeprecatedIn: "0.31.0",
			RemovedIn:    "1.0.0",
		},
		{
			OldName:      "queue-retry-timeout",
			NewName:      "export-timeout",
			Message:      "Use --export-timeout as base for all timeout values.",
			AnnouncedIn:  "0.30.0",
			DeprecatedIn: "0.31.0",
			RemovedIn:    "1.0.0",
		},
		{
			OldName:      "queue-drain-timeout",
			NewName:      "export-timeout",
			Message:      "Use --export-timeout as base for all timeout values.",
			AnnouncedIn:  "0.30.0",
			DeprecatedIn: "0.31.0",
			RemovedIn:    "1.0.0",
		},
		{
			OldName:      "queue-drain-entry-timeout",
			NewName:      "export-timeout",
			Message:      "Use --export-timeout as base for all timeout values.",
			AnnouncedIn:  "0.30.0",
			DeprecatedIn: "0.31.0",
			RemovedIn:    "1.0.0",
		},
		{
			OldName:      "queue-close-timeout",
			NewName:      "export-timeout",
			Message:      "Use --export-timeout as base for all timeout values.",
			AnnouncedIn:  "0.30.0",
			DeprecatedIn: "0.31.0",
			RemovedIn:    "1.0.0",
		},

		// Resilience level consolidation
		{
			OldName:      "queue-backoff-multiplier",
			NewName:      "resilience-level",
			Message:      "Use --resilience-level (low/medium/high) to set all resilience params.",
			AnnouncedIn:  "0.30.0",
			DeprecatedIn: "0.31.0",
			RemovedIn:    "1.0.0",
		},
		{
			OldName:      "queue-circuit-breaker-enabled",
			NewName:      "resilience-level",
			Message:      "Use --resilience-level (low/medium/high) to set all resilience params.",
			AnnouncedIn:  "0.30.0",
			DeprecatedIn: "0.31.0",
			RemovedIn:    "1.0.0",
		},
		{
			OldName:      "queue-circuit-breaker-threshold",
			NewName:      "resilience-level",
			Message:      "Use --resilience-level (low/medium/high) to set all resilience params.",
			AnnouncedIn:  "0.30.0",
			DeprecatedIn: "0.31.0",
			RemovedIn:    "1.0.0",
		},
		{
			OldName:      "queue-batch-drain-size",
			NewName:      "resilience-level",
			Message:      "Use --resilience-level (low/medium/high) to set all resilience params.",
			AnnouncedIn:  "0.30.0",
			DeprecatedIn: "0.31.0",
			RemovedIn:    "1.0.0",
		},
		{
			OldName:      "queue-burst-drain-size",
			NewName:      "resilience-level",
			Message:      "Use --resilience-level (low/medium/high) to set all resilience params.",
			AnnouncedIn:  "0.30.0",
			DeprecatedIn: "0.31.0",
			RemovedIn:    "1.0.0",
		},
	}
}
