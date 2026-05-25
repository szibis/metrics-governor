package autotune

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Backend identifies a cardinality signal source backend.
type Backend string

const (
	BackendVM         Backend = "vm"
	BackendPrometheus Backend = "prometheus"
	BackendMimir      Backend = "mimir"
	BackendThanos     Backend = "thanos"
	BackendPromQL     Backend = "promql"
	BackendMulti      Backend = "multi"
)

// PersistMode identifies how autotune changes are persisted.
type PersistMode string

const (
	PersistMemory PersistMode = "memory"
	PersistFile   PersistMode = "file"
	PersistGit    PersistMode = "git"
)

// HAMode identifies the HA coordination mode.
type HAMode string

const (
	HANoop       HAMode = "noop"
	HADesignated HAMode = "designated"
	HALease      HAMode = "lease"
)

// PropagationMode identifies how config is propagated leader→followers.
type PropagationMode string

const (
	PropConfigMap PropagationMode = "configmap"
	PropPeer      PropagationMode = "peer"
	PropPull      PropagationMode = "pull"
)

// AuthType identifies the authentication method for source queries.
type AuthType string

const (
	AuthNone   AuthType = "none"
	AuthBasic  AuthType = "basic"
	AuthBearer AuthType = "bearer"
	AuthHeader AuthType = "header"
)

// MergeStrategy for MultiClient fan-out.
type MergeStrategy string

const (
	MergeSum   MergeStrategy = "sum"
	MergeMax   MergeStrategy = "max"
	MergeUnion MergeStrategy = "union"
)

// Config holds all autotune configuration.
type Config struct {
	Enabled  bool
	Interval time.Duration

	// Tier 1: Cardinality source.
	Source SourceConfig

	// Tier 2: vmanomaly.
	VManomalyEnabled bool
	VManomalyURL     string
	VManomalyMetric  string

	// Tier 3: AI/LLM (future).
	AIEnabled  bool
	AIEndpoint string
	AIModel    string
	AIAPIKey   string

	// Policy thresholds.
	Policy PolicyConfig

	// Safeguards.
	Safeguards SafeguardsConfig

	// Persistence.
	Persist PersistConfig

	// HA coordination.
	HA HAConfig

	// Config propagation.
	Propagation PropagationConfig
}

// SourceConfig configures the cardinality signal source (Tier 1).
type SourceConfig struct {
	Enabled  bool
	Backend  Backend
	URL      string
	Timeout  time.Duration
	TopN     int
	TenantID string
	Auth     AuthConfig

	// VM-specific.
	VM VMSourceConfig

	// Mimir-specific.
	Mimir MimirSourceConfig

	// Thanos-specific.
	Thanos ThanosSourceConfig

	// PromQL-specific.
	PromQL PromQLSourceConfig

	// Multi-backend fan-out.
	Multi MultiSourceConfig
}

// AuthConfig configures authentication for source queries.
type AuthConfig struct {
	Type        AuthType
	Username    string
	Password    string
	Token       string
	HeaderName  string
	HeaderValue string
}

// VMSourceConfig configures VictoriaMetrics-specific options.
type VMSourceConfig struct {
	TSDBInsights        bool
	CardinalityExplorer bool
	ExplorerInterval    time.Duration
	ExplorerMatch       []string
	ExplorerFocusLabel  string
	ExplorerDate        string
}

// MimirSourceConfig configures Grafana Mimir-specific options.
type MimirSourceConfig struct {
	CountMethod string // "inmemory" or "active"
}

// ThanosSourceConfig configures Thanos-specific options.
type ThanosSourceConfig struct {
	AllTenants bool
	Dedup      bool
}

// PromQLSourceConfig configures PromQL query-based signals.
type PromQLSourceConfig struct {
	Queries  map[string]string // name -> PromQL expression
	Interval time.Duration
}

// MultiSourceConfig configures multi-backend fan-out.
type MultiSourceConfig struct {
	Sources       []SourceConfig
	MergeStrategy MergeStrategy
}

// PolicyConfig configures the autotune policy engine thresholds.
type PolicyConfig struct {
	IncreaseThreshold   float64       // utilization > this → grow (default: 0.85)
	DecreaseThreshold   float64       // utilization < this → shrink (default: 0.30)
	DecreaseSustainTime time.Duration // must stay low for this long (default: 1h)
	AnomalyTightenScore float64       // anomaly > this → tighten (default: 0.7)
	AnomalySafeScore    float64       // anomaly < this → safe to grow (default: 0.5)
	GrowFactor          float64       // multiply limit on increase (default: 1.25)
	ShrinkFactor        float64       // multiply limit on decrease (default: 0.75)
	TightenFactor       float64       // multiply limit on anomaly tighten (default: 0.80)
	MaxChangePct        float64       // max change per cycle (default: 0.50)
	Cooldown            time.Duration // min time between adjustments per rule (default: 5m)
	MinCardinality      int64         // absolute floor (default: 100)
	MaxCardinality      int64         // absolute ceiling (default: 10_000_000)
	RollbackDropPct     float64       // rollback if drops > this % (default: 0.05)
}

// SafeguardsConfig configures all autotune safety mechanisms.
type SafeguardsConfig struct {
	ReloadMinInterval time.Duration
	ReloadTimeout     time.Duration

	CircuitBreaker CircuitBreakerConfig
	Verification   VerificationConfig
	Oscillation    OscillationConfig
	ErrorBudget    ErrorBudgetConfig
	Git            GitSafeguardConfig
	Propagation    PropagationSafeguardConfig
}

// CircuitBreakerConfig configures the circuit breaker.
type CircuitBreakerConfig struct {
	MaxConsecutiveFailures int
	ResetTimeout           time.Duration
}

// VerificationConfig configures post-apply verification.
type VerificationConfig struct {
	Warmup                 time.Duration
	Window                 time.Duration
	RollbackCooldownFactor int
}

// OscillationConfig configures oscillation detection.
type OscillationConfig struct {
	LookbackCount  int
	Threshold      float64
	FreezeDuration time.Duration
}

// ErrorBudgetConfig configures the cumulative error budget.
type ErrorBudgetConfig struct {
	Window        time.Duration
	MaxErrors     int
	PauseDuration time.Duration
}

// GitSafeguardConfig configures git operation safeguards.
type GitSafeguardConfig struct {
	PushTimeout      time.Duration
	MaxRetries       int
	RetryBackoffBase time.Duration
	LockStaleTimeout time.Duration
}

// PropagationSafeguardConfig configures propagation safeguards.
type PropagationSafeguardConfig struct {
	Timeout                    time.Duration
	MaxRetries                 int
	RetryBackoffBase           time.Duration
	PeerCircuitBreakerFailures int
	PeerCircuitBreakerReset    time.Duration
	StaleLeaderThreshold       time.Duration
}

// PersistConfig configures change persistence.
type PersistConfig struct {
	Mode PersistMode
	File FilePersistConfig
	Git  GitPersistConfig
}

// FilePersistConfig configures file-based persistence.
type FilePersistConfig struct {
	Path string
}

// GitPersistConfig configures git-based persistence.
type GitPersistConfig struct {
	RepoURL           string
	Branch            string
	Path              string
	CommitPrefix      string
	AuthorName        string
	AuthorEmail       string
	CredentialsSecret string
}

// HAConfig configures HA coordination.
type HAConfig struct {
	Mode               HAMode
	LeaseName          string
	LeaseNamespace     string
	LeaseDuration      time.Duration
	LeaseRenewDeadline time.Duration
	DesignatedLeader   bool
}

// PropagationConfig configures leader→follower config propagation.
type PropagationConfig struct {
	Mode               PropagationMode
	ConfigMapName      string
	ConfigMapNamespace string
	PeerService        string
	PullInterval       time.Duration
}

// DefaultConfig returns a Config with conservative defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:  false,
		Interval: 60 * time.Second,
		Source: SourceConfig{
			Enabled: false,
			Backend: BackendVM,
			Timeout: 10 * time.Second,
			TopN:    100,
			Auth:    AuthConfig{Type: AuthNone},
			VM: VMSourceConfig{
				TSDBInsights:        true,
				CardinalityExplorer: false,
				ExplorerInterval:    5 * time.Minute,
			},
			Mimir: MimirSourceConfig{
				CountMethod: "active",
			},
			Thanos: ThanosSourceConfig{
				Dedup: true,
			},
			PromQL: PromQLSourceConfig{
				Queries:  make(map[string]string),
				Interval: 30 * time.Second,
			},
			Multi: MultiSourceConfig{
				MergeStrategy: MergeUnion,
			},
		},
		VManomalyMetric: "anomaly_score",
		Policy:          DefaultPolicyConfig(),
		Safeguards:      DefaultSafeguardsConfig(),
		Persist: PersistConfig{
			Mode: PersistMemory,
			Git: GitPersistConfig{
				Branch:       "autotune",
				Path:         "clusters/default",
				CommitPrefix: "autotune: ",
				AuthorName:   "metrics-governor",
				AuthorEmail:  "governor@example.com",
			},
		},
		HA: HAConfig{
			Mode:               HANoop,
			LeaseName:          "governor-autotune",
			LeaseDuration:      15 * time.Second,
			LeaseRenewDeadline: 10 * time.Second,
		},
		Propagation: PropagationConfig{
			Mode:         PropConfigMap,
			PullInterval: 10 * time.Second,
		},
	}
}

// DefaultPolicyConfig returns conservative policy defaults.
func DefaultPolicyConfig() PolicyConfig {
	return PolicyConfig{
		IncreaseThreshold:   0.85,
		DecreaseThreshold:   0.30,
		DecreaseSustainTime: time.Hour,
		AnomalyTightenScore: 0.7,
		AnomalySafeScore:    0.5,
		GrowFactor:          1.25,
		ShrinkFactor:        0.75,
		TightenFactor:       0.80,
		MaxChangePct:        0.50,
		Cooldown:            5 * time.Minute,
		MinCardinality:      100,
		MaxCardinality:      10_000_000,
		RollbackDropPct:     0.05,
	}
}

// DefaultSafeguardsConfig returns conservative safeguard defaults.
func DefaultSafeguardsConfig() SafeguardsConfig {
	return SafeguardsConfig{
		ReloadMinInterval: 60 * time.Second,
		ReloadTimeout:     10 * time.Second,
		CircuitBreaker: CircuitBreakerConfig{
			MaxConsecutiveFailures: 3,
			ResetTimeout:           5 * time.Minute,
		},
		Verification: VerificationConfig{
			Warmup:                 30 * time.Second,
			Window:                 2 * time.Minute,
			RollbackCooldownFactor: 3,
		},
		Oscillation: OscillationConfig{
			LookbackCount:  6,
			Threshold:      0.75,
			FreezeDuration: time.Hour,
		},
		ErrorBudget: ErrorBudgetConfig{
			Window:        time.Hour,
			MaxErrors:     10,
			PauseDuration: 30 * time.Minute,
		},
		Git: GitSafeguardConfig{
			PushTimeout:      30 * time.Second,
			MaxRetries:       3,
			RetryBackoffBase: time.Second,
			LockStaleTimeout: 5 * time.Minute,
		},
		Propagation: PropagationSafeguardConfig{
			Timeout:                    10 * time.Second,
			MaxRetries:                 3,
			RetryBackoffBase:           time.Second,
			PeerCircuitBreakerFailures: 3,
			PeerCircuitBreakerReset:    5 * time.Minute,
			StaleLeaderThreshold:       5 * time.Minute,
		},
	}
}

// validBackends maps valid backend names for validation.
var validBackends = map[Backend]bool{
	BackendVM:         true,
	BackendPrometheus: true,
	BackendMimir:      true,
	BackendThanos:     true,
	BackendPromQL:     true,
	BackendMulti:      true,
}

// Validate checks the config for internal consistency.
func (c *Config) Validate() error {
	if !c.Enabled {
		return nil // nothing to validate when disabled
	}

	var errs []string

	// At least one signal tier must be enabled.
	if !c.Source.Enabled && !c.VManomalyEnabled && !c.AIEnabled {
		errs = append(errs, "at least one signal tier must be enabled (source, vmanomaly, or ai)")
	}

	// Source validation.
	if c.Source.Enabled {
		if c.Source.URL == "" {
			errs = append(errs, "source.url required when source.enabled=true")
		}
		if !validBackends[c.Source.Backend] {
			errs = append(errs, fmt.Sprintf("unknown backend: %s", c.Source.Backend))
		}
		if c.Source.Backend == BackendVM {
			if !c.Source.VM.TSDBInsights && !c.Source.VM.CardinalityExplorer {
				errs = append(errs, "at least one VM mode must be enabled when backend=vm")
			}
			if c.Source.VM.CardinalityExplorer && c.Source.VM.ExplorerInterval < time.Minute {
				errs = append(errs, "vm.explorer_interval must be >= 1m")
			}
		}
		if c.Source.Timeout <= 0 {
			errs = append(errs, "source.timeout must be > 0")
		}
		if c.Source.TopN <= 0 {
			errs = append(errs, "source.top_n must be > 0")
		}
	}

	// vmanomaly validation.
	if c.VManomalyEnabled && c.VManomalyURL == "" {
		errs = append(errs, "vmanomaly_url required when vmanomaly_enabled=true")
	}

	// AI validation.
	if c.AIEnabled && c.AIEndpoint == "" {
		errs = append(errs, "ai_endpoint required when ai_enabled=true")
	}

	// Policy validation.
	if c.Policy.GrowFactor <= 1.0 {
		errs = append(errs, fmt.Sprintf("grow_factor must be > 1.0, got %f", c.Policy.GrowFactor))
	}
	if c.Policy.ShrinkFactor >= 1.0 || c.Policy.ShrinkFactor <= 0 {
		errs = append(errs, fmt.Sprintf("shrink_factor must be between 0 and 1.0, got %f", c.Policy.ShrinkFactor))
	}
	if c.Policy.TightenFactor >= 1.0 || c.Policy.TightenFactor <= 0 {
		errs = append(errs, fmt.Sprintf("tighten_factor must be between 0 and 1.0, got %f", c.Policy.TightenFactor))
	}
	if c.Policy.MinCardinality > c.Policy.MaxCardinality {
		errs = append(errs, "min_cardinality must be <= max_cardinality")
	}
	if c.Policy.MaxChangePct <= 0 || c.Policy.MaxChangePct > 1.0 {
		errs = append(errs, fmt.Sprintf("max_change_pct must be between 0 and 1.0, got %f", c.Policy.MaxChangePct))
	}
	if c.Policy.RollbackDropPct <= 0 || c.Policy.RollbackDropPct > 1.0 {
		errs = append(errs, fmt.Sprintf("rollback_drop_pct must be between 0 and 1.0, got %f", c.Policy.RollbackDropPct))
	}
	if c.Policy.IncreaseThreshold <= 0 || c.Policy.IncreaseThreshold > 1.0 {
		errs = append(errs, fmt.Sprintf("increase_threshold must be between 0 and 1.0, got %f", c.Policy.IncreaseThreshold))
	}
	if c.Policy.DecreaseThreshold < 0 || c.Policy.DecreaseThreshold >= 1.0 {
		errs = append(errs, fmt.Sprintf("decrease_threshold must be between 0 and 1.0, got %f", c.Policy.DecreaseThreshold))
	}
	if c.Policy.Cooldown <= 0 {
		errs = append(errs, "policy.cooldown must be > 0")
	}

	// Safeguards validation.
	if c.Safeguards.ReloadTimeout <= 0 {
		errs = append(errs, "reload_timeout must be > 0")
	}
	if c.Safeguards.ReloadMinInterval <= 0 {
		errs = append(errs, "reload_min_interval must be > 0")
	}

	// Interval validation.
	if c.Interval <= 0 {
		errs = append(errs, "interval must be > 0")
	}

	if len(errs) == 0 {
		return nil
	}
	return errors.New("autotune configuration validation failed:\n  - " + strings.Join(errs, "\n  - "))
}

// ActiveTiers returns the list of active tier numbers based on config.
func (c *Config) ActiveTiers() []int {
	tiers := []int{0} // Tier 0 always active
	if c.Source.Enabled {
		tiers = append(tiers, 1)
	}
	if c.VManomalyEnabled {
		tiers = append(tiers, 2)
	}
	if c.AIEnabled {
		tiers = append(tiers, 3)
	}
	return tiers
}
