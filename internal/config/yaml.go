package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// YAMLConfig represents the YAML configuration file structure.
type YAMLConfig struct {
	// Profile & simplified config (top-level)
	Profile         string   `yaml:"profile"`
	Parallelism     *int     `yaml:"parallelism"`
	MemoryBudgetPct *float64 `yaml:"memory_budget_percent"`
	ExportTimeout   Duration `yaml:"export_timeout"`
	ResilienceLevel string   `yaml:"resilience_level"`

	Receiver    ReceiverYAMLConfig    `yaml:"receiver"`
	Exporter    ExporterYAMLConfig    `yaml:"exporter"`
	Buffer      BufferYAMLConfig      `yaml:"buffer"`
	Stats       StatsYAMLConfig       `yaml:"stats"`
	Limits      LimitsYAMLConfig      `yaml:"limits"`
	Performance PerformanceYAMLConfig `yaml:"performance"`
	Memory      MemoryYAMLConfig      `yaml:"memory"`
	Telemetry   TelemetryYAMLConfig   `yaml:"telemetry"`
	Autotune    AutotuneYAMLConfig    `yaml:"autotune"`
}

// TelemetryYAMLConfig holds OTLP self-monitoring telemetry configuration.
type TelemetryYAMLConfig struct {
	Endpoint        string                   `yaml:"endpoint"`         // OTLP endpoint (empty = disabled)
	Protocol        string                   `yaml:"protocol"`         // "grpc" or "http" (default: "grpc")
	Insecure        *bool                    `yaml:"insecure"`         // Use insecure connection (default: true)
	Timeout         Duration                 `yaml:"timeout"`          // Per-export timeout (0 = SDK default 10s)
	PushInterval    Duration                 `yaml:"push_interval"`    // Metric push interval (default: 30s)
	Compression     string                   `yaml:"compression"`      // "gzip" or "" (default: "")
	ShutdownTimeout Duration                 `yaml:"shutdown_timeout"` // Shutdown grace period (default: 5s)
	Headers         map[string]string        `yaml:"headers"`          // Custom headers (auth, etc.)
	Retry           TelemetryRetryYAMLConfig `yaml:"retry"`            // Retry configuration
}

// TelemetryRetryYAMLConfig holds telemetry retry configuration.
type TelemetryRetryYAMLConfig struct {
	Enabled     *bool    `yaml:"enabled"`      // Enable retry (default: true)
	Initial     Duration `yaml:"initial"`      // Initial retry interval (default: 5s)
	MaxInterval Duration `yaml:"max_interval"` // Max retry interval (default: 30s)
	MaxElapsed  Duration `yaml:"max_elapsed"`  // Max total retry time (default: 1m)
}

// MemoryYAMLConfig holds memory limit configuration.
type MemoryYAMLConfig struct {
	// LimitRatio is the ratio of container memory to use for GOMEMLIMIT (0.0-1.0)
	LimitRatio float64 `yaml:"limit_ratio"`
	// BufferPercent is buffer capacity as % of detected memory limit (default: 0.15)
	BufferPercent float64 `yaml:"buffer_percent"`
	// QueuePercent is queue in-memory capacity as % of detected memory limit (default: 0.15)
	QueuePercent float64 `yaml:"queue_percent"`
}

// PerformanceYAMLConfig holds performance tuning configuration.
type PerformanceYAMLConfig struct {
	// ExportConcurrency limits parallel export goroutines (0 = NumCPU * 4)
	ExportConcurrency int `yaml:"export_concurrency"`
	// StringInterning enables string interning for label deduplication
	StringInterning *bool `yaml:"string_interning"`
	// InternMaxValueLength is the max length for value interning (longer values not interned)
	InternMaxValueLength int `yaml:"intern_max_value_length"`
}

// ReceiverYAMLConfig holds receiver configuration.
type ReceiverYAMLConfig struct {
	GRPC GRPCReceiverYAMLConfig `yaml:"grpc"`
	HTTP HTTPReceiverYAMLConfig `yaml:"http"`
	TLS  TLSServerYAMLConfig    `yaml:"tls"`
	Auth AuthServerYAMLConfig   `yaml:"auth"`
}

// GRPCReceiverYAMLConfig holds gRPC receiver settings.
type GRPCReceiverYAMLConfig struct {
	Address string `yaml:"address"`
}

// HTTPReceiverYAMLConfig holds HTTP receiver settings.
type HTTPReceiverYAMLConfig struct {
	Address string               `yaml:"address"`
	Server  HTTPServerYAMLConfig `yaml:"server"`
}

// HTTPServerYAMLConfig holds HTTP server timeout settings.
type HTTPServerYAMLConfig struct {
	MaxRequestBodySize ByteSize `yaml:"max_request_body_size"`
	ReadTimeout        Duration `yaml:"read_timeout"`
	ReadHeaderTimeout  Duration `yaml:"read_header_timeout"`
	WriteTimeout       Duration `yaml:"write_timeout"`
	IdleTimeout        Duration `yaml:"idle_timeout"`
	KeepAlivesEnabled  *bool    `yaml:"keep_alives_enabled"`
}

// TLSServerYAMLConfig holds TLS server configuration.
type TLSServerYAMLConfig struct {
	Enabled    bool   `yaml:"enabled"`
	CertFile   string `yaml:"cert_file"`
	KeyFile    string `yaml:"key_file"`
	CAFile     string `yaml:"ca_file"`
	ClientAuth bool   `yaml:"client_auth"`
}

// AuthServerYAMLConfig holds server authentication configuration.
type AuthServerYAMLConfig struct {
	Enabled       bool   `yaml:"enabled"`
	BearerToken   string `yaml:"bearer_token"`
	BasicUsername string `yaml:"basic_username"`
	BasicPassword string `yaml:"basic_password"`
}

// ExporterYAMLConfig holds exporter configuration.
type ExporterYAMLConfig struct {
	Endpoint           string                `yaml:"endpoint"`
	Protocol           string                `yaml:"protocol"`
	Insecure           *bool                 `yaml:"insecure"`
	Timeout            Duration              `yaml:"timeout"`
	DefaultPath        string                `yaml:"default_path"`
	PrewarmConnections *bool                 `yaml:"prewarm_connections"` // Pre-warm HTTP connections at startup
	TLS                TLSClientYAMLConfig   `yaml:"tls"`
	Auth               AuthClientYAMLConfig  `yaml:"auth"`
	Compression        CompressionYAMLConfig `yaml:"compression"`
	HTTPClient         HTTPClientYAMLConfig  `yaml:"http_client"`
	Queue              QueueYAMLConfig       `yaml:"queue"`
	Sharding           ShardingYAMLConfig    `yaml:"sharding"`
}

// ShardingYAMLConfig holds sharding configuration.
type ShardingYAMLConfig struct {
	Enabled            *bool    `yaml:"enabled"`
	HeadlessService    string   `yaml:"headless_service"`
	DNSRefreshInterval Duration `yaml:"dns_refresh_interval"`
	DNSTimeout         Duration `yaml:"dns_timeout"`
	Labels             []string `yaml:"labels"`
	VirtualNodes       int      `yaml:"virtual_nodes"`
	FallbackOnEmpty    *bool    `yaml:"fallback_on_empty"`
}

// QueueYAMLConfig holds queue configuration.
type QueueYAMLConfig struct {
	Type              string   `yaml:"type"`
	Enabled           *bool    `yaml:"enabled"`
	Path              string   `yaml:"path"`
	MaxSize           int      `yaml:"max_size"`
	MaxBytes          ByteSize `yaml:"max_bytes"`
	RetryInterval     Duration `yaml:"retry_interval"`
	MaxRetryDelay     Duration `yaml:"max_retry_delay"`
	FullBehavior      string   `yaml:"full_behavior"`
	TargetUtilization float64  `yaml:"target_utilization"`
	AdaptiveEnabled   *bool    `yaml:"adaptive_enabled"`
	CompactThreshold  float64  `yaml:"compact_threshold"`
	// FastQueue settings
	InmemoryBlocks     int      `yaml:"inmemory_blocks"`
	ChunkSize          ByteSize `yaml:"chunk_size"`
	MetaSyncInterval   Duration `yaml:"meta_sync_interval"`
	StaleFlushInterval Duration `yaml:"stale_flush_interval"`
	WriteBufferSize    ByteSize `yaml:"write_buffer_size"`
	Compression        string   `yaml:"compression"`
	// Backoff settings
	Backoff BackoffYAMLConfig `yaml:"backoff"`
	// Circuit breaker settings
	CircuitBreaker CircuitBreakerYAMLConfig `yaml:"circuit_breaker"`
	// Resilience settings (drain/retry tuning)
	BatchDrainSize    int      `yaml:"batch_drain_size"`
	BurstDrainSize    int      `yaml:"burst_drain_size"`
	RetryTimeout      Duration `yaml:"retry_timeout"`
	CloseTimeout      Duration `yaml:"close_timeout"`
	DrainTimeout      Duration `yaml:"drain_timeout"`
	DrainEntryTimeout Duration `yaml:"drain_entry_timeout"`
	// Worker pool settings
	AlwaysQueue *bool `yaml:"always_queue"` // Always route through queue (default: true)
	Workers     int   `yaml:"workers"`      // Worker goroutine count (0 = NumCPU)
	// Pipeline split settings
	PipelineSplit PipelineSplitYAMLConfig `yaml:"pipeline_split"`
	// Async send settings
	MaxConcurrentSends int `yaml:"max_concurrent_sends"` // Max concurrent HTTP sends per sender (default: 4)
	GlobalSendLimit    int `yaml:"global_send_limit"`    // Global max in-flight sends (default: NumCPU*8)
	// Adaptive worker scaling settings
	AdaptiveWorkers AdaptiveWorkersYAMLConfig `yaml:"adaptive_workers"`
}

// PipelineSplitYAMLConfig holds pipeline split configuration.
type PipelineSplitYAMLConfig struct {
	Enabled       *bool `yaml:"enabled"`
	PreparerCount int   `yaml:"preparer_count"` // CPU-bound compression goroutines (default: NumCPU)
	SenderCount   int   `yaml:"sender_count"`   // I/O-bound HTTP send goroutines (default: NumCPU*2)
	ChannelSize   int   `yaml:"channel_size"`   // Bounded channel between preparers and senders (default: 256)
}

// AdaptiveWorkersYAMLConfig holds adaptive worker scaling configuration.
type AdaptiveWorkersYAMLConfig struct {
	Enabled    *bool `yaml:"enabled"`
	MinWorkers int   `yaml:"min_workers"` // Minimum worker count (default: 1)
	MaxWorkers int   `yaml:"max_workers"` // Maximum worker count (default: NumCPU*4)
}

// BackoffYAMLConfig holds exponential backoff configuration.
type BackoffYAMLConfig struct {
	Enabled    *bool   `yaml:"enabled"`
	Multiplier float64 `yaml:"multiplier"`
}

// CircuitBreakerYAMLConfig holds circuit breaker configuration.
type CircuitBreakerYAMLConfig struct {
	Enabled      *bool    `yaml:"enabled"`
	Threshold    int      `yaml:"threshold"`
	ResetTimeout Duration `yaml:"reset_timeout"`
}

// TLSClientYAMLConfig holds TLS client configuration.
type TLSClientYAMLConfig struct {
	Enabled    bool   `yaml:"enabled"`
	CertFile   string `yaml:"cert_file"`
	KeyFile    string `yaml:"key_file"`
	CAFile     string `yaml:"ca_file"`
	SkipVerify bool   `yaml:"skip_verify"`
	ServerName string `yaml:"server_name"`
}

// AuthClientYAMLConfig holds client authentication configuration.
type AuthClientYAMLConfig struct {
	BearerToken   string            `yaml:"bearer_token"`
	BasicUsername string            `yaml:"basic_username"`
	BasicPassword string            `yaml:"basic_password"`
	Headers       map[string]string `yaml:"headers"`
}

// CompressionYAMLConfig holds compression settings.
type CompressionYAMLConfig struct {
	Type  string `yaml:"type"`
	Level int    `yaml:"level"`
}

// HTTPClientYAMLConfig holds HTTP client connection pool settings.
type HTTPClientYAMLConfig struct {
	MaxIdleConns         int      `yaml:"max_idle_conns"`
	MaxIdleConnsPerHost  int      `yaml:"max_idle_conns_per_host"`
	MaxConnsPerHost      int      `yaml:"max_conns_per_host"`
	IdleConnTimeout      Duration `yaml:"idle_conn_timeout"`
	KeepAliveInterval    Duration `yaml:"keep_alive_interval"`
	DisableKeepAlives    bool     `yaml:"disable_keep_alives"`
	ForceHTTP2           bool     `yaml:"force_http2"`
	HTTP2ReadIdleTimeout Duration `yaml:"http2_read_idle_timeout"`
	HTTP2PingTimeout     Duration `yaml:"http2_ping_timeout"`
	DialTimeout          Duration `yaml:"dial_timeout"`
}

// BufferYAMLConfig holds buffer configuration.
type BufferYAMLConfig struct {
	Size          int      `yaml:"size"`
	BatchSize     int      `yaml:"batch_size"`
	MaxBatchBytes ByteSize `yaml:"max_batch_bytes"`
	FlushInterval Duration `yaml:"flush_interval"`
	FlushTimeout  Duration `yaml:"flush_timeout"`
	FullPolicy    string   `yaml:"full_policy"` // reject, drop_oldest, block
	// Batch auto-tuning (AIMD)
	BatchAutoTune BatchAutoTuneYAMLConfig `yaml:"batch_auto_tune"`
}

// BatchAutoTuneYAMLConfig holds AIMD batch auto-tuning configuration.
type BatchAutoTuneYAMLConfig struct {
	Enabled       *bool   `yaml:"enabled"`
	MinBytes      int     `yaml:"min_bytes"`      // Minimum batch size (default: 512)
	MaxBytes      int     `yaml:"max_bytes"`      // Maximum batch size (default: 16MB)
	SuccessStreak int     `yaml:"success_streak"` // Consecutive successes before growth (default: 10)
	GrowFactor    float64 `yaml:"grow_factor"`    // Growth factor (default: 1.25)
	ShrinkFactor  float64 `yaml:"shrink_factor"`  // Shrink factor on failure (default: 0.5)
}

// StatsYAMLConfig holds stats configuration.
type StatsYAMLConfig struct {
	Address              string        `yaml:"address"`
	Labels               []string      `yaml:"labels"`
	CardinalityThreshold int           `yaml:"cardinality_threshold"`
	MaxLabelCombinations int           `yaml:"max_label_combinations"`
	SLI                  SLIYAMLConfig `yaml:"sli"`
	LLM                  LLMYAMLConfig `yaml:"llm"`
}

// SLIYAMLConfig holds SLI/SLO configuration.
type SLIYAMLConfig struct {
	Enabled        *bool    `yaml:"enabled"`
	DeliveryTarget float64  `yaml:"delivery_target"`
	ExportTarget   float64  `yaml:"export_target"`
	BudgetWindow   Duration `yaml:"budget_window"`
}

// LLMYAMLConfig holds LLM token budget tracking configuration.
type LLMYAMLConfig struct {
	Enabled      *bool            `yaml:"enabled"`
	TokenMetric  string           `yaml:"token_metric"`
	BudgetWindow Duration         `yaml:"budget_window"`
	Budgets      []BudgetRuleYAML `yaml:"budgets"`
}

// BudgetRuleYAML holds a single token budget rule.
type BudgetRuleYAML struct {
	Provider    string `yaml:"provider"`
	Model       string `yaml:"model"`
	DailyTokens int64  `yaml:"daily_tokens"`
}

// LimitsYAMLConfig holds limits configuration.
type LimitsYAMLConfig struct {
	DryRun           *bool `yaml:"dry_run"`
	RuleCacheMaxSize int   `yaml:"rule_cache_max_size"`
	StatsThreshold   int64 `yaml:"stats_threshold"` // Only report per-group stats for groups with >= N datapoints
}

// AutotuneYAMLConfig holds autotune closed-loop governance configuration.
type AutotuneYAMLConfig struct {
	Enabled  *bool    `yaml:"enabled"`
	Interval Duration `yaml:"interval"`
	Source   AutotuneSourceYAMLConfig `yaml:"source"`

	// Tier 2: vmanomaly.
	VManomalyEnabled *bool  `yaml:"vmanomaly_enabled"`
	VManomalyURL     string `yaml:"vmanomaly_url"`
	VManomalyMetric  string `yaml:"vmanomaly_metric"`

	// Persistence.
	PersistMode     string `yaml:"persist_mode"`
	PersistFilePath string `yaml:"persist_file_path"`

	// HA coordination.
	HAMode             string `yaml:"ha_mode"`
	HADesignatedLeader *bool  `yaml:"ha_designated_leader"`
	HALeaseName        string `yaml:"ha_lease_name"`

	// Config propagation.
	PropagationMode    string `yaml:"propagation_mode"`
	PropagationService string `yaml:"propagation_service"`

	// Tier 3: AI/LLM.
	AIEnabled  *bool  `yaml:"ai_enabled"`
	AIEndpoint string `yaml:"ai_endpoint"`
	AIModel    string `yaml:"ai_model"`
	AIAPIKey   string `yaml:"ai_api_key"`

	// External signal provider.
	ExternalURL          string   `yaml:"external_url"`
	ExternalPollInterval Duration `yaml:"external_poll_interval"`
}

// AutotuneSourceYAMLConfig holds cardinality source config for YAML.
type AutotuneSourceYAMLConfig struct {
	Backend  string   `yaml:"backend"`
	URL      string   `yaml:"url"`
	TenantID string   `yaml:"tenant_id"`
	Timeout  Duration `yaml:"timeout"`
	TopN     int      `yaml:"top_n"`
	VM       AutotuneVMYAMLConfig `yaml:"vm"`
}

// AutotuneVMYAMLConfig holds VM-specific autotune settings for YAML.
type AutotuneVMYAMLConfig struct {
	TSDBInsights        *bool    `yaml:"tsdb_insights"`
	CardinalityExplorer *bool    `yaml:"cardinality_explorer"`
	ExplorerInterval    Duration `yaml:"explorer_interval"`
	ExplorerMatch       string   `yaml:"explorer_match"`
	ExplorerFocusLabel  string   `yaml:"explorer_focus_label"`
}

// Duration is a wrapper for time.Duration that supports YAML unmarshaling.
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler for Duration.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	if s == "" {
		*d = 0
		return nil
	}
	duration, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(duration)
	return nil
}

// MarshalYAML implements yaml.Marshaler for Duration.
func (d Duration) MarshalYAML() (interface{}, error) {
	return time.Duration(d).String(), nil
}

// ByteSize is a wrapper for int64 that supports human-readable YAML values.
// Accepted formats: raw integer (bytes), or suffixed: Ki, Mi, Gi, Ti.
type ByteSize int64

// UnmarshalYAML implements yaml.Unmarshaler for ByteSize.
func (b *ByteSize) UnmarshalYAML(value *yaml.Node) error {
	// Try integer first
	var n int64
	if err := value.Decode(&n); err == nil {
		*b = ByteSize(n)
		return nil
	}
	// Try string with unit suffix
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	s = strings.TrimSpace(s)
	if s == "" {
		*b = 0
		return nil
	}
	parsed, err := ParseByteSize(s)
	if err != nil {
		return err
	}
	*b = ByteSize(parsed)
	return nil
}

// MarshalYAML implements yaml.Marshaler for ByteSize.
func (b ByteSize) MarshalYAML() (interface{}, error) {
	return FormatByteSize(int64(b)), nil
}

// ParseByteSize parses a human-readable byte size string.
// Accepted suffixes: Ki (1024), Mi (1048576), Gi (1073741824), Ti (1099511627776).
// Plain integers are treated as bytes.
func ParseByteSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	type suffix struct {
		name string
		mult int64
	}
	suffixes := []suffix{
		{"Ti", 1099511627776},
		{"Gi", 1073741824},
		{"Mi", 1048576},
		{"Ki", 1024},
	}
	for _, sf := range suffixes {
		if strings.HasSuffix(s, sf.name) {
			numStr := strings.TrimSpace(strings.TrimSuffix(s, sf.name))
			// Support float values like "1.5Gi"
			var f float64
			if _, err := fmt.Sscanf(numStr, "%f", &f); err != nil {
				return 0, fmt.Errorf("invalid byte size: %q", s)
			}
			return int64(f * float64(sf.mult)), nil
		}
	}
	// Plain integer — reject strings with non-numeric trailing characters (e.g. "256MB")
	var n int64
	var trail string
	if _, err := fmt.Sscanf(s, "%d%s", &n, &trail); err == nil && trail != "" {
		return 0, fmt.Errorf("invalid byte size: %q (use Ki, Mi, Gi, or Ti suffixes)", s)
	}
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return 0, fmt.Errorf("invalid byte size: %q", s)
	}
	return n, nil
}

// FormatByteSize formats bytes as a human-readable string with binary suffix.
func FormatByteSize(b int64) string {
	if b >= 1099511627776 && b%1099511627776 == 0 {
		return fmt.Sprintf("%dTi", b/1099511627776)
	}
	if b >= 1073741824 && b%1073741824 == 0 {
		return fmt.Sprintf("%dGi", b/1073741824)
	}
	if b >= 1048576 && b%1048576 == 0 {
		return fmt.Sprintf("%dMi", b/1048576)
	}
	if b >= 1024 && b%1024 == 0 {
		return fmt.Sprintf("%dKi", b/1024)
	}
	return fmt.Sprintf("%d", b)
}

// LoadYAML loads configuration from a YAML file.
func LoadYAML(path string) (*YAMLConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseYAML(data)
}

// ParseYAML parses YAML configuration from bytes.
func ParseYAML(data []byte) (*YAMLConfig, error) {
	cfg := &YAMLConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	cfg.ApplyDefaults()
	return cfg, nil
}

// ApplyDefaults sets default values for unspecified fields.
func (y *YAMLConfig) ApplyDefaults() {
	// Receiver GRPC defaults
	if y.Receiver.GRPC.Address == "" {
		y.Receiver.GRPC.Address = ":4317"
	}

	// Receiver HTTP defaults
	if y.Receiver.HTTP.Address == "" {
		y.Receiver.HTTP.Address = ":4318"
	}
	if y.Receiver.HTTP.Server.ReadHeaderTimeout == 0 {
		y.Receiver.HTTP.Server.ReadHeaderTimeout = Duration(1 * time.Minute)
	}
	if y.Receiver.HTTP.Server.WriteTimeout == 0 {
		y.Receiver.HTTP.Server.WriteTimeout = Duration(30 * time.Second)
	}
	if y.Receiver.HTTP.Server.IdleTimeout == 0 {
		y.Receiver.HTTP.Server.IdleTimeout = Duration(1 * time.Minute)
	}
	if y.Receiver.HTTP.Server.KeepAlivesEnabled == nil {
		keepAlives := true
		y.Receiver.HTTP.Server.KeepAlivesEnabled = &keepAlives
	}

	// Exporter defaults
	if y.Exporter.Endpoint == "" {
		y.Exporter.Endpoint = "localhost:4317"
	}
	if y.Exporter.Protocol == "" {
		y.Exporter.Protocol = "grpc"
	}
	if y.Exporter.Insecure == nil {
		insecure := true
		y.Exporter.Insecure = &insecure
	}
	if y.Exporter.Timeout == 0 {
		y.Exporter.Timeout = Duration(30 * time.Second)
	}
	if y.Exporter.Compression.Type == "" {
		y.Exporter.Compression.Type = "none"
	}

	// Exporter HTTP client defaults
	if y.Exporter.HTTPClient.MaxIdleConns == 0 {
		y.Exporter.HTTPClient.MaxIdleConns = 100
	}
	if y.Exporter.HTTPClient.MaxIdleConnsPerHost == 0 {
		y.Exporter.HTTPClient.MaxIdleConnsPerHost = 100
	}
	if y.Exporter.HTTPClient.IdleConnTimeout == 0 {
		y.Exporter.HTTPClient.IdleConnTimeout = Duration(90 * time.Second)
	}

	// Buffer defaults
	if y.Buffer.Size == 0 {
		y.Buffer.Size = 10000
	}
	if y.Buffer.BatchSize == 0 {
		y.Buffer.BatchSize = 5000
	}
	if y.Buffer.MaxBatchBytes == 0 {
		y.Buffer.MaxBatchBytes = 8388608 // 8MB
	}
	if y.Buffer.FlushInterval == 0 {
		y.Buffer.FlushInterval = Duration(5 * time.Second)
	}
	if y.Buffer.FlushTimeout == 0 {
		y.Buffer.FlushTimeout = Duration(30 * time.Second)
	}
	if y.Buffer.FullPolicy == "" {
		y.Buffer.FullPolicy = "reject"
	}

	// Stats defaults
	if y.Stats.Address == "" {
		y.Stats.Address = ":9090"
	}
	if y.Stats.SLI.Enabled == nil {
		enabled := true
		y.Stats.SLI.Enabled = &enabled
	}
	if y.Stats.SLI.DeliveryTarget == 0 {
		y.Stats.SLI.DeliveryTarget = 0.999
	}
	if y.Stats.SLI.ExportTarget == 0 {
		y.Stats.SLI.ExportTarget = 0.995
	}
	if y.Stats.SLI.BudgetWindow == 0 {
		y.Stats.SLI.BudgetWindow = Duration(720 * time.Hour)
	}

	// LLM defaults
	if y.Stats.LLM.Enabled == nil {
		enabled := false
		y.Stats.LLM.Enabled = &enabled
	}
	if y.Stats.LLM.TokenMetric == "" {
		y.Stats.LLM.TokenMetric = "gen_ai.client.token.usage"
	}
	if y.Stats.LLM.BudgetWindow == 0 {
		y.Stats.LLM.BudgetWindow = Duration(24 * time.Hour)
	}

	// Autotune defaults
	if y.Autotune.Enabled == nil {
		enabled := false
		y.Autotune.Enabled = &enabled
	}
	if y.Autotune.Interval == 0 {
		y.Autotune.Interval = Duration(60 * time.Second)
	}
	if y.Autotune.Source.Backend == "" {
		y.Autotune.Source.Backend = "vm"
	}
	if y.Autotune.Source.Timeout == 0 {
		y.Autotune.Source.Timeout = Duration(10 * time.Second)
	}
	if y.Autotune.Source.TopN == 0 {
		y.Autotune.Source.TopN = 100
	}
	if y.Autotune.Source.VM.TSDBInsights == nil {
		tsdb := true
		y.Autotune.Source.VM.TSDBInsights = &tsdb
	}
	if y.Autotune.Source.VM.CardinalityExplorer == nil {
		explorer := false
		y.Autotune.Source.VM.CardinalityExplorer = &explorer
	}
	if y.Autotune.Source.VM.ExplorerInterval == 0 {
		y.Autotune.Source.VM.ExplorerInterval = Duration(5 * time.Minute)
	}
	if y.Autotune.VManomalyEnabled == nil {
		enabled := false
		y.Autotune.VManomalyEnabled = &enabled
	}
	if y.Autotune.VManomalyMetric == "" {
		y.Autotune.VManomalyMetric = "anomaly_score"
	}
	if y.Autotune.PersistMode == "" {
		y.Autotune.PersistMode = "memory"
	}
	if y.Autotune.HAMode == "" {
		y.Autotune.HAMode = "noop"
	}
	if y.Autotune.HADesignatedLeader == nil {
		leader := false
		y.Autotune.HADesignatedLeader = &leader
	}
	if y.Autotune.HALeaseName == "" {
		y.Autotune.HALeaseName = "governor-autotune"
	}
	if y.Autotune.PropagationMode == "" {
		y.Autotune.PropagationMode = "configmap"
	}
	if y.Autotune.AIEnabled == nil {
		aiEnabled := false
		y.Autotune.AIEnabled = &aiEnabled
	}
	if y.Autotune.ExternalPollInterval == 0 {
		y.Autotune.ExternalPollInterval = Duration(5 * time.Minute)
	}

	// Limits defaults
	if y.Limits.DryRun == nil {
		dryRun := true
		y.Limits.DryRun = &dryRun
	}
	if y.Limits.RuleCacheMaxSize == 0 {
		y.Limits.RuleCacheMaxSize = 10000
	}

	// Queue defaults
	if y.Exporter.Queue.Type == "" {
		y.Exporter.Queue.Type = "memory"
	}
	if y.Exporter.Queue.Enabled == nil {
		enabled := true
		y.Exporter.Queue.Enabled = &enabled
	}
	if y.Exporter.Queue.Path == "" {
		y.Exporter.Queue.Path = "./queue"
	}
	if y.Exporter.Queue.MaxSize == 0 {
		y.Exporter.Queue.MaxSize = 10000
	}
	if y.Exporter.Queue.MaxBytes == 0 {
		y.Exporter.Queue.MaxBytes = 1073741824 // 1GB
	}
	if y.Exporter.Queue.RetryInterval == 0 {
		y.Exporter.Queue.RetryInterval = Duration(5 * time.Second)
	}
	if y.Exporter.Queue.MaxRetryDelay == 0 {
		y.Exporter.Queue.MaxRetryDelay = Duration(5 * time.Minute)
	}
	if y.Exporter.Queue.FullBehavior == "" {
		y.Exporter.Queue.FullBehavior = "drop_oldest"
	}
	if y.Exporter.Queue.TargetUtilization == 0 {
		y.Exporter.Queue.TargetUtilization = 0.85
	}
	if y.Exporter.Queue.AdaptiveEnabled == nil {
		enabled := true
		y.Exporter.Queue.AdaptiveEnabled = &enabled
	}
	if y.Exporter.Queue.CompactThreshold == 0 {
		y.Exporter.Queue.CompactThreshold = 0.5
	}
	// FastQueue defaults
	if y.Exporter.Queue.InmemoryBlocks == 0 {
		y.Exporter.Queue.InmemoryBlocks = 2048
	}
	if y.Exporter.Queue.ChunkSize == 0 {
		y.Exporter.Queue.ChunkSize = 512 * 1024 * 1024 // 512MB
	}
	if y.Exporter.Queue.MetaSyncInterval == 0 {
		y.Exporter.Queue.MetaSyncInterval = Duration(time.Second)
	}
	if y.Exporter.Queue.StaleFlushInterval == 0 {
		y.Exporter.Queue.StaleFlushInterval = Duration(30 * time.Second)
	}
	if y.Exporter.Queue.WriteBufferSize == 0 {
		y.Exporter.Queue.WriteBufferSize = 262144 // 256KB
	}
	if y.Exporter.Queue.Compression == "" {
		y.Exporter.Queue.Compression = "snappy"
	}
	// Backoff defaults
	if y.Exporter.Queue.Backoff.Enabled == nil {
		enabled := true
		y.Exporter.Queue.Backoff.Enabled = &enabled
	}
	if y.Exporter.Queue.Backoff.Multiplier == 0 {
		y.Exporter.Queue.Backoff.Multiplier = 2.0
	}
	// Circuit breaker defaults
	if y.Exporter.Queue.CircuitBreaker.Enabled == nil {
		enabled := true
		y.Exporter.Queue.CircuitBreaker.Enabled = &enabled
	}
	if y.Exporter.Queue.CircuitBreaker.Threshold == 0 {
		y.Exporter.Queue.CircuitBreaker.Threshold = 5
	}
	// Worker pool defaults
	if y.Exporter.Queue.AlwaysQueue == nil {
		alwaysQueue := true
		y.Exporter.Queue.AlwaysQueue = &alwaysQueue
	}
	if y.Exporter.Queue.CircuitBreaker.ResetTimeout == 0 {
		y.Exporter.Queue.CircuitBreaker.ResetTimeout = Duration(30 * time.Second)
	}
	// Queue resilience defaults
	if y.Exporter.Queue.BatchDrainSize == 0 {
		y.Exporter.Queue.BatchDrainSize = 10
	}
	if y.Exporter.Queue.BurstDrainSize == 0 {
		y.Exporter.Queue.BurstDrainSize = 100
	}
	if y.Exporter.Queue.RetryTimeout == 0 {
		y.Exporter.Queue.RetryTimeout = Duration(10 * time.Second)
	}
	if y.Exporter.Queue.CloseTimeout == 0 {
		y.Exporter.Queue.CloseTimeout = Duration(60 * time.Second)
	}
	if y.Exporter.Queue.DrainTimeout == 0 {
		y.Exporter.Queue.DrainTimeout = Duration(30 * time.Second)
	}
	if y.Exporter.Queue.DrainEntryTimeout == 0 {
		y.Exporter.Queue.DrainEntryTimeout = Duration(5 * time.Second)
	}

	// Pipeline split defaults
	if y.Exporter.Queue.PipelineSplit.Enabled == nil {
		disabled := false
		y.Exporter.Queue.PipelineSplit.Enabled = &disabled
	}
	// Adaptive workers defaults
	if y.Exporter.Queue.AdaptiveWorkers.Enabled == nil {
		disabled := false
		y.Exporter.Queue.AdaptiveWorkers.Enabled = &disabled
	}
	// Batch auto-tune defaults
	if y.Buffer.BatchAutoTune.Enabled == nil {
		disabled := false
		y.Buffer.BatchAutoTune.Enabled = &disabled
	}
	// Connection pre-warming defaults
	if y.Exporter.PrewarmConnections == nil {
		prewarm := true
		y.Exporter.PrewarmConnections = &prewarm
	}

	// Exporter HTTP client defaults
	if y.Exporter.HTTPClient.DialTimeout == 0 {
		y.Exporter.HTTPClient.DialTimeout = Duration(30 * time.Second)
	}

	// Sharding defaults
	if y.Exporter.Sharding.Enabled == nil {
		enabled := false
		y.Exporter.Sharding.Enabled = &enabled
	}
	if y.Exporter.Sharding.DNSRefreshInterval == 0 {
		y.Exporter.Sharding.DNSRefreshInterval = Duration(30 * time.Second)
	}
	if y.Exporter.Sharding.DNSTimeout == 0 {
		y.Exporter.Sharding.DNSTimeout = Duration(5 * time.Second)
	}
	if y.Exporter.Sharding.VirtualNodes == 0 {
		y.Exporter.Sharding.VirtualNodes = 150
	}
	if y.Exporter.Sharding.FallbackOnEmpty == nil {
		fallback := true
		y.Exporter.Sharding.FallbackOnEmpty = &fallback
	}

	// Performance defaults
	if y.Performance.StringInterning == nil {
		enabled := true
		y.Performance.StringInterning = &enabled
	}
	if y.Performance.InternMaxValueLength == 0 {
		y.Performance.InternMaxValueLength = 64
	}
	// ExportConcurrency defaults to 0 (which means NumCPU * 4 at runtime)

	// Memory defaults
	if y.Memory.LimitRatio == 0 {
		y.Memory.LimitRatio = 0.85
	}
	if y.Memory.BufferPercent == 0 {
		y.Memory.BufferPercent = 0.10
	}
	if y.Memory.QueuePercent == 0 {
		y.Memory.QueuePercent = 0.10
	}

	// Telemetry defaults
	if y.Telemetry.Protocol == "" {
		y.Telemetry.Protocol = "grpc"
	}
	if y.Telemetry.Insecure == nil {
		b := true
		y.Telemetry.Insecure = &b
	}
	if y.Telemetry.PushInterval == 0 {
		y.Telemetry.PushInterval = Duration(30 * time.Second)
	}
	if y.Telemetry.ShutdownTimeout == 0 {
		y.Telemetry.ShutdownTimeout = Duration(5 * time.Second)
	}
	if y.Telemetry.Retry.Enabled == nil {
		b := true
		y.Telemetry.Retry.Enabled = &b
	}
	if y.Telemetry.Retry.Initial == 0 {
		y.Telemetry.Retry.Initial = Duration(5 * time.Second)
	}
	if y.Telemetry.Retry.MaxInterval == 0 {
		y.Telemetry.Retry.MaxInterval = Duration(30 * time.Second)
	}
	if y.Telemetry.Retry.MaxElapsed == 0 {
		y.Telemetry.Retry.MaxElapsed = Duration(1 * time.Minute)
	}
}

// ToConfig converts YAMLConfig to the flat Config struct.
func (y *YAMLConfig) ToConfig() *Config {
	cfg := &Config{
		// Profile & simplified config
		Profile:           y.Profile,
		ExportTimeoutBase: time.Duration(y.ExportTimeout),
		ResilienceLevel:   y.ResilienceLevel,

		// Receiver
		GRPCListenAddr: y.Receiver.GRPC.Address,
		HTTPListenAddr: y.Receiver.HTTP.Address,

		// Receiver TLS
		ReceiverTLSEnabled:    y.Receiver.TLS.Enabled,
		ReceiverTLSCertFile:   y.Receiver.TLS.CertFile,
		ReceiverTLSKeyFile:    y.Receiver.TLS.KeyFile,
		ReceiverTLSCAFile:     y.Receiver.TLS.CAFile,
		ReceiverTLSClientAuth: y.Receiver.TLS.ClientAuth,

		// Receiver Auth
		ReceiverAuthEnabled:       y.Receiver.Auth.Enabled,
		ReceiverAuthBearerToken:   y.Receiver.Auth.BearerToken,
		ReceiverAuthBasicUsername: y.Receiver.Auth.BasicUsername,
		ReceiverAuthBasicPassword: y.Receiver.Auth.BasicPassword,

		// Receiver HTTP Server
		ReceiverMaxRequestBodySize: int64(y.Receiver.HTTP.Server.MaxRequestBodySize),
		ReceiverReadTimeout:        time.Duration(y.Receiver.HTTP.Server.ReadTimeout),
		ReceiverReadHeaderTimeout:  time.Duration(y.Receiver.HTTP.Server.ReadHeaderTimeout),
		ReceiverWriteTimeout:       time.Duration(y.Receiver.HTTP.Server.WriteTimeout),
		ReceiverIdleTimeout:        time.Duration(y.Receiver.HTTP.Server.IdleTimeout),
		ReceiverKeepAlivesEnabled:  *y.Receiver.HTTP.Server.KeepAlivesEnabled,

		// Exporter
		ExporterEndpoint: y.Exporter.Endpoint,
		ExporterProtocol: y.Exporter.Protocol,
		ExporterInsecure: *y.Exporter.Insecure,
		ExporterTimeout:  time.Duration(y.Exporter.Timeout),

		// Exporter TLS
		ExporterTLSEnabled:            y.Exporter.TLS.Enabled,
		ExporterTLSCertFile:           y.Exporter.TLS.CertFile,
		ExporterTLSKeyFile:            y.Exporter.TLS.KeyFile,
		ExporterTLSCAFile:             y.Exporter.TLS.CAFile,
		ExporterTLSInsecureSkipVerify: y.Exporter.TLS.SkipVerify,
		ExporterTLSServerName:         y.Exporter.TLS.ServerName,

		// Exporter Auth
		ExporterAuthBearerToken:   y.Exporter.Auth.BearerToken,
		ExporterAuthBasicUsername: y.Exporter.Auth.BasicUsername,
		ExporterAuthBasicPassword: y.Exporter.Auth.BasicPassword,
		ExporterAuthHeaders:       headersMapToString(y.Exporter.Auth.Headers),

		// Exporter Compression
		ExporterCompression:      y.Exporter.Compression.Type,
		ExporterCompressionLevel: y.Exporter.Compression.Level,

		// Exporter HTTP Client
		ExporterMaxIdleConns:         y.Exporter.HTTPClient.MaxIdleConns,
		ExporterMaxIdleConnsPerHost:  y.Exporter.HTTPClient.MaxIdleConnsPerHost,
		ExporterMaxConnsPerHost:      y.Exporter.HTTPClient.MaxConnsPerHost,
		ExporterIdleConnTimeout:      time.Duration(y.Exporter.HTTPClient.IdleConnTimeout),
		ExporterKeepAliveInterval:    time.Duration(y.Exporter.HTTPClient.KeepAliveInterval),
		ExporterDisableKeepAlives:    y.Exporter.HTTPClient.DisableKeepAlives,
		ExporterForceHTTP2:           y.Exporter.HTTPClient.ForceHTTP2,
		ExporterHTTP2ReadIdleTimeout: time.Duration(y.Exporter.HTTPClient.HTTP2ReadIdleTimeout),
		ExporterHTTP2PingTimeout:     time.Duration(y.Exporter.HTTPClient.HTTP2PingTimeout),
		ExporterDialTimeout:          time.Duration(y.Exporter.HTTPClient.DialTimeout),

		// Buffer
		BufferSize:       y.Buffer.Size,
		FlushInterval:    time.Duration(y.Buffer.FlushInterval),
		FlushTimeout:     time.Duration(y.Buffer.FlushTimeout),
		MaxBatchSize:     y.Buffer.BatchSize,
		MaxBatchBytes:    int(y.Buffer.MaxBatchBytes),
		BufferFullPolicy: y.Buffer.FullPolicy,

		// Stats
		StatsAddr:                 y.Stats.Address,
		StatsLabels:               strings.Join(y.Stats.Labels, ","),
		StatsCardinalityThreshold: y.Stats.CardinalityThreshold,
		StatsMaxLabelCombinations: y.Stats.MaxLabelCombinations,
		SLIEnabled:                *y.Stats.SLI.Enabled,
		SLIDeliveryTarget:         y.Stats.SLI.DeliveryTarget,
		SLIExportTarget:           y.Stats.SLI.ExportTarget,
		SLIBudgetWindow:           time.Duration(y.Stats.SLI.BudgetWindow),
		LLMEnabled:                *y.Stats.LLM.Enabled,
		LLMTokenMetric:            y.Stats.LLM.TokenMetric,
		LLMBudgetWindow:           time.Duration(y.Stats.LLM.BudgetWindow),
		LLMBudgets:                convertLLMBudgets(y.Stats.LLM.Budgets),

		// Autotune
		AutotuneEnabled:             *y.Autotune.Enabled,
		AutotuneInterval:            time.Duration(y.Autotune.Interval),
		AutotuneSourceBackend:       y.Autotune.Source.Backend,
		AutotuneSourceURL:           y.Autotune.Source.URL,
		AutotuneSourceTenantID:      y.Autotune.Source.TenantID,
		AutotuneSourceTimeout:       time.Duration(y.Autotune.Source.Timeout),
		AutotuneSourceTopN:          y.Autotune.Source.TopN,
		AutotuneVMTSDBInsights:      *y.Autotune.Source.VM.TSDBInsights,
		AutotuneVMExplorer:          *y.Autotune.Source.VM.CardinalityExplorer,
		AutotuneVMExplorerInterval:  time.Duration(y.Autotune.Source.VM.ExplorerInterval),
		AutotuneVMExplorerMatch:     y.Autotune.Source.VM.ExplorerMatch,
		AutotuneVMExplorerFocusLabel: y.Autotune.Source.VM.ExplorerFocusLabel,
		AutotuneVManomalyEnabled:    *y.Autotune.VManomalyEnabled,
		AutotuneVManomalyURL:        y.Autotune.VManomalyURL,
		AutotuneVManomalyMetric:     y.Autotune.VManomalyMetric,
		AutotunePersistMode:         y.Autotune.PersistMode,
		AutotunePersistFilePath:     y.Autotune.PersistFilePath,
		AutotuneHAMode:              y.Autotune.HAMode,
		AutotuneHADesignatedLeader:  *y.Autotune.HADesignatedLeader,
		AutotuneHALeaseName:         y.Autotune.HALeaseName,
		AutotunePropagationMode:     y.Autotune.PropagationMode,
		AutotunePropagationService:    y.Autotune.PropagationService,
		AutotuneAIEnabled:             *y.Autotune.AIEnabled,
		AutotuneAIEndpoint:            y.Autotune.AIEndpoint,
		AutotuneAIModel:               y.Autotune.AIModel,
		AutotuneAIAPIKey:              y.Autotune.AIAPIKey,
		AutotuneExternalURL:           y.Autotune.ExternalURL,
		AutotuneExternalPollInterval:  time.Duration(y.Autotune.ExternalPollInterval),

		// Limits
		LimitsDryRun:         *y.Limits.DryRun,
		RuleCacheMaxSize:     y.Limits.RuleCacheMaxSize,
		LimitsStatsThreshold: y.Limits.StatsThreshold,

		// Queue
		QueueType:              y.Exporter.Queue.Type,
		QueueEnabled:           *y.Exporter.Queue.Enabled,
		QueuePath:              y.Exporter.Queue.Path,
		QueueMaxSize:           y.Exporter.Queue.MaxSize,
		QueueMaxBytes:          int64(y.Exporter.Queue.MaxBytes),
		QueueRetryInterval:     time.Duration(y.Exporter.Queue.RetryInterval),
		QueueMaxRetryDelay:     time.Duration(y.Exporter.Queue.MaxRetryDelay),
		QueueFullBehavior:      y.Exporter.Queue.FullBehavior,
		QueueTargetUtilization: y.Exporter.Queue.TargetUtilization,
		QueueAdaptiveEnabled:   *y.Exporter.Queue.AdaptiveEnabled,
		QueueCompactThreshold:  y.Exporter.Queue.CompactThreshold,
		// FastQueue settings
		QueueInmemoryBlocks:     y.Exporter.Queue.InmemoryBlocks,
		QueueChunkSize:          int64(y.Exporter.Queue.ChunkSize),
		QueueMetaSyncInterval:   time.Duration(y.Exporter.Queue.MetaSyncInterval),
		QueueStaleFlushInterval: time.Duration(y.Exporter.Queue.StaleFlushInterval),
		QueueWriteBufferSize:    int(y.Exporter.Queue.WriteBufferSize),
		QueueCompression:        y.Exporter.Queue.Compression,
		// Backoff settings
		QueueBackoffEnabled:    *y.Exporter.Queue.Backoff.Enabled,
		QueueBackoffMultiplier: y.Exporter.Queue.Backoff.Multiplier,
		// Circuit breaker settings
		QueueCircuitBreakerEnabled:      *y.Exporter.Queue.CircuitBreaker.Enabled,
		QueueCircuitBreakerThreshold:    y.Exporter.Queue.CircuitBreaker.Threshold,
		QueueCircuitBreakerResetTimeout: time.Duration(y.Exporter.Queue.CircuitBreaker.ResetTimeout),
		// Queue resilience settings
		QueueBatchDrainSize:    y.Exporter.Queue.BatchDrainSize,
		QueueBurstDrainSize:    y.Exporter.Queue.BurstDrainSize,
		QueueRetryTimeout:      time.Duration(y.Exporter.Queue.RetryTimeout),
		QueueCloseTimeout:      time.Duration(y.Exporter.Queue.CloseTimeout),
		QueueDrainTimeout:      time.Duration(y.Exporter.Queue.DrainTimeout),
		QueueDrainEntryTimeout: time.Duration(y.Exporter.Queue.DrainEntryTimeout),
		QueueAlwaysQueue:       *y.Exporter.Queue.AlwaysQueue,
		QueueWorkers:           y.Exporter.Queue.Workers,
		// Pipeline split settings
		QueuePipelineSplitEnabled: *y.Exporter.Queue.PipelineSplit.Enabled,
		QueuePreparerCount:        y.Exporter.Queue.PipelineSplit.PreparerCount,
		QueueSenderCount:          y.Exporter.Queue.PipelineSplit.SenderCount,
		QueuePipelineChannelSize:  y.Exporter.Queue.PipelineSplit.ChannelSize,
		// Async send settings
		QueueMaxConcurrentSends: y.Exporter.Queue.MaxConcurrentSends,
		QueueGlobalSendLimit:    y.Exporter.Queue.GlobalSendLimit,
		// Adaptive worker scaling
		QueueAdaptiveWorkersEnabled: *y.Exporter.Queue.AdaptiveWorkers.Enabled,
		QueueMinWorkers:             y.Exporter.Queue.AdaptiveWorkers.MinWorkers,
		QueueMaxWorkers:             y.Exporter.Queue.AdaptiveWorkers.MaxWorkers,
		// Batch auto-tuning
		BufferBatchAutoTuneEnabled: *y.Buffer.BatchAutoTune.Enabled,
		BufferBatchMinBytes:        y.Buffer.BatchAutoTune.MinBytes,
		BufferBatchMaxBytes:        y.Buffer.BatchAutoTune.MaxBytes,
		BufferBatchSuccessStreak:   y.Buffer.BatchAutoTune.SuccessStreak,
		BufferBatchGrowFactor:      y.Buffer.BatchAutoTune.GrowFactor,
		BufferBatchShrinkFactor:    y.Buffer.BatchAutoTune.ShrinkFactor,
		// Connection pre-warming
		ExporterPrewarmConnections: *y.Exporter.PrewarmConnections,

		// Sharding
		ShardingEnabled:            *y.Exporter.Sharding.Enabled,
		ShardingHeadlessService:    y.Exporter.Sharding.HeadlessService,
		ShardingDNSRefreshInterval: time.Duration(y.Exporter.Sharding.DNSRefreshInterval),
		ShardingDNSTimeout:         time.Duration(y.Exporter.Sharding.DNSTimeout),
		ShardingLabels:             strings.Join(y.Exporter.Sharding.Labels, ","),
		ShardingVirtualNodes:       y.Exporter.Sharding.VirtualNodes,
		ShardingFallbackOnEmpty:    *y.Exporter.Sharding.FallbackOnEmpty,

		// Performance
		ExportConcurrency:    y.Performance.ExportConcurrency,
		StringInterning:      *y.Performance.StringInterning,
		InternMaxValueLength: y.Performance.InternMaxValueLength,

		// Memory
		MemoryLimitRatio:    y.Memory.LimitRatio,
		BufferMemoryPercent: y.Memory.BufferPercent,
		QueueMemoryPercent:  y.Memory.QueuePercent,

		// Telemetry
		TelemetryEndpoint:         y.Telemetry.Endpoint,
		TelemetryProtocol:         y.Telemetry.Protocol,
		TelemetryInsecure:         *y.Telemetry.Insecure,
		TelemetryTimeout:          time.Duration(y.Telemetry.Timeout),
		TelemetryPushInterval:     time.Duration(y.Telemetry.PushInterval),
		TelemetryCompression:      y.Telemetry.Compression,
		TelemetryShutdownTimeout:  time.Duration(y.Telemetry.ShutdownTimeout),
		TelemetryRetryEnabled:     *y.Telemetry.Retry.Enabled,
		TelemetryRetryInitial:     time.Duration(y.Telemetry.Retry.Initial),
		TelemetryRetryMaxInterval: time.Duration(y.Telemetry.Retry.MaxInterval),
		TelemetryRetryMaxElapsed:  time.Duration(y.Telemetry.Retry.MaxElapsed),
		TelemetryHeaders:          headersMapToString(y.Telemetry.Headers),
	}

	// Handle pointer-typed YAML fields (distinguish unset from zero)
	if y.Parallelism != nil {
		cfg.Parallelism = *y.Parallelism
	}
	if y.MemoryBudgetPct != nil {
		cfg.MemoryBudgetPct = *y.MemoryBudgetPct
	}

	return cfg
}

// headersMapToString converts a headers map to the comma-separated format.
// convertLLMBudgets converts YAML budget rules to config budget rules.
func convertLLMBudgets(budgets []BudgetRuleYAML) []LLMBudgetRule {
	if len(budgets) == 0 {
		return nil
	}
	rules := make([]LLMBudgetRule, len(budgets))
	for i, b := range budgets {
		rules[i] = LLMBudgetRule(b)
	}
	return rules
}

func headersMapToString(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	pairs := make([]string, 0, len(headers))
	for k, v := range headers {
		pairs = append(pairs, k+"="+v)
	}
	return strings.Join(pairs, ",")
}
