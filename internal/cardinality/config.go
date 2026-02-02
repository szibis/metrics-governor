package cardinality

// Mode determines the cardinality tracking implementation.
type Mode int

const (
	// ModeBloom uses Bloom filter for memory-efficient tracking (~98% memory savings).
	// May slightly undercount due to false positives (configurable via FalsePositiveRate).
	ModeBloom Mode = iota

	// ModeExact uses map[string]struct{} for 100% accurate tracking.
	// Higher memory usage but no false positives.
	ModeExact
)

// String returns the string representation of the mode.
func (m Mode) String() string {
	switch m {
	case ModeBloom:
		return "bloom"
	case ModeExact:
		return "exact"
	default:
		return "unknown"
	}
}

// ParseMode parses a mode string.
func ParseMode(s string) Mode {
	switch s {
	case "exact":
		return ModeExact
	default:
		return ModeBloom
	}
}

// Config holds configuration for cardinality tracking.
type Config struct {
	// Mode determines tracking implementation (bloom or exact).
	Mode Mode

	// ExpectedItems is the expected number of unique items per tracker.
	// Used for Bloom filter sizing. Higher values use more memory but reduce false positives.
	ExpectedItems uint

	// FalsePositiveRate is the target false positive rate for Bloom filter mode.
	// 0.01 = 1% false positive rate (default).
	// Lower values use more memory but are more accurate.
	FalsePositiveRate float64
}

// DefaultConfig returns sensible defaults for metrics tracking.
func DefaultConfig() Config {
	return Config{
		Mode:              ModeBloom,
		ExpectedItems:     100000, // 100K unique series per group
		FalsePositiveRate: 0.01,   // 1% false positive rate
	}
}

// GlobalConfig holds application-wide cardinality settings.
// This is set by main.go based on CLI flags.
var GlobalConfig = DefaultConfig()

// GlobalTrackerStore is the application-wide persistent tracker store.
// This is set by main.go if persistence is enabled.
var GlobalTrackerStore *TrackerStore

// NewTracker creates a tracker based on the provided config.
func NewTracker(cfg Config) Tracker {
	if cfg.Mode == ModeExact {
		return NewExactTracker()
	}
	return NewBloomTracker(cfg)
}

// NewTrackerFromGlobal creates a tracker based on global config.
// If persistence is enabled, returns a persistent tracker.
func NewTrackerFromGlobal() Tracker {
	return NewTracker(GlobalConfig)
}

// NewPersistentTrackerFromGlobal creates a persistent tracker if persistence is enabled,
// otherwise falls back to a regular tracker.
func NewPersistentTrackerFromGlobal(key string) Tracker {
	if GlobalTrackerStore != nil {
		return GlobalTrackerStore.GetOrCreate(key)
	}
	// Fallback to non-persistent tracker
	return NewTrackerFromGlobal()
}
