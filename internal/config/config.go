package config

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/szibis/metrics-governor/internal/auth"
	"github.com/szibis/metrics-governor/internal/compression"
	"github.com/szibis/metrics-governor/internal/exporter"
	"github.com/szibis/metrics-governor/internal/prw"
	"github.com/szibis/metrics-governor/internal/receiver"
	tlspkg "github.com/szibis/metrics-governor/internal/tls"
)

// version is set at build time via ldflags
var version = "dev"

// Config holds the application configuration.
type Config struct {
	// Receiver settings
	GRPCListenAddr   string
	HTTPListenAddr   string
	HTTPReceiverPath string // Custom path for OTLP HTTP receiver (default: /v1/metrics)

	// Receiver TLS settings
	ReceiverTLSEnabled    bool
	ReceiverTLSCertFile   string
	ReceiverTLSKeyFile    string
	ReceiverTLSCAFile     string
	ReceiverTLSClientAuth bool

	// Receiver Auth settings
	ReceiverAuthEnabled       bool
	ReceiverAuthBearerToken   string
	ReceiverAuthBasicUsername string
	ReceiverAuthBasicPassword string

	// Exporter settings
	ExporterEndpoint    string
	ExporterProtocol    string
	ExporterInsecure    bool
	ExporterTimeout     time.Duration
	ExporterDefaultPath string // Default path for OTLP HTTP exporter when not in endpoint (default: /v1/metrics)

	// Exporter TLS settings
	ExporterTLSEnabled            bool
	ExporterTLSCertFile           string
	ExporterTLSKeyFile            string
	ExporterTLSCAFile             string
	ExporterTLSInsecureSkipVerify bool
	ExporterTLSServerName         string

	// Exporter Auth settings
	ExporterAuthBearerToken   string
	ExporterAuthBasicUsername string
	ExporterAuthBasicPassword string
	ExporterAuthHeaders       string

	// Exporter Compression settings
	ExporterCompression      string
	ExporterCompressionLevel int

	// Exporter HTTP client settings
	ExporterMaxIdleConns         int
	ExporterMaxIdleConnsPerHost  int
	ExporterMaxConnsPerHost      int
	ExporterIdleConnTimeout      time.Duration
	ExporterDisableKeepAlives    bool
	ExporterForceHTTP2           bool
	ExporterHTTP2ReadIdleTimeout time.Duration
	ExporterHTTP2PingTimeout     time.Duration

	// Receiver HTTP server settings
	ReceiverMaxRequestBodySize int64
	ReceiverReadTimeout        time.Duration
	ReceiverReadHeaderTimeout  time.Duration
	ReceiverWriteTimeout       time.Duration
	ReceiverIdleTimeout        time.Duration
	ReceiverKeepAlivesEnabled  bool

	// Buffer settings
	BufferSize    int
	FlushInterval time.Duration
	MaxBatchSize  int

	// Stats settings
	StatsAddr   string
	StatsLabels string

	// Limits settings
	LimitsConfig     string
	LimitsDryRun     bool
	RuleCacheMaxSize int

	// Queue settings
	QueueEnabled           bool
	QueuePath              string
	QueueMaxSize           int
	QueueMaxBytes          int64
	QueueRetryInterval     time.Duration
	QueueMaxRetryDelay     time.Duration
	QueueFullBehavior      string
	QueueTargetUtilization float64
	QueueAdaptiveEnabled   bool
	QueueCompactThreshold  float64
	// FastQueue settings
	QueueInmemoryBlocks     int           // In-memory channel size (default: 256)
	QueueChunkSize          int64         // Chunk file size (default: 512MB)
	QueueMetaSyncInterval   time.Duration // Metadata sync interval (default: 1s)
	QueueStaleFlushInterval time.Duration // Stale flush interval (default: 5s)
	// Backoff settings
	QueueBackoffEnabled    bool    // Enable exponential backoff (default: true)
	QueueBackoffMultiplier float64 // Backoff multiplier (default: 2.0)
	// Circuit breaker settings
	QueueCircuitBreakerEnabled      bool          // Enable circuit breaker (default: true)
	QueueCircuitBreakerThreshold    int           // Failures before opening (default: 10)
	QueueCircuitBreakerResetTimeout time.Duration // Time to wait before half-open (default: 30s)

	// Memory limit settings
	MemoryLimitRatio float64 // Ratio of container memory to use for GOMEMLIMIT (default: 0.9)

	// Sharding settings
	ShardingEnabled            bool
	ShardingHeadlessService    string
	ShardingDNSRefreshInterval time.Duration
	ShardingDNSTimeout         time.Duration
	ShardingLabels             string // Comma-separated
	ShardingVirtualNodes       int
	ShardingFallbackOnEmpty    bool

	// PRW Receiver settings
	PRWListenAddr                 string
	PRWReceiverPath               string // Custom path for PRW receiver (default: /api/v1/write)
	PRWReceiverVersion            string
	PRWReceiverTLSEnabled         bool
	PRWReceiverTLSCertFile        string
	PRWReceiverTLSKeyFile         string
	PRWReceiverTLSCAFile          string
	PRWReceiverTLSClientAuth      bool
	PRWReceiverAuthEnabled        bool
	PRWReceiverAuthBearerToken    string
	PRWReceiverMaxRequestBodySize int64
	PRWReceiverReadTimeout        time.Duration
	PRWReceiverWriteTimeout       time.Duration

	// PRW Exporter settings
	PRWExporterEndpoint        string
	PRWExporterDefaultPath     string // Default path for PRW exporter when not in endpoint (default: /api/v1/write)
	PRWExporterVersion         string
	PRWExporterTimeout         time.Duration
	PRWExporterTLSEnabled      bool
	PRWExporterTLSCertFile     string
	PRWExporterTLSKeyFile      string
	PRWExporterTLSCAFile       string
	PRWExporterTLSSkipVerify   bool
	PRWExporterAuthBearerToken string
	PRWExporterVMMode          bool
	PRWExporterVMCompression   string
	PRWExporterVMShortEndpoint bool
	PRWExporterVMExtraLabels   string

	// PRW Buffer settings
	PRWBufferSize    int
	PRWBatchSize     int
	PRWFlushInterval time.Duration

	// PRW Queue settings
	PRWQueueEnabled       bool
	PRWQueuePath          string
	PRWQueueMaxSize       int
	PRWQueueMaxBytes      int64
	PRWQueueRetryInterval time.Duration
	PRWQueueMaxRetryDelay time.Duration

	// PRW Limits settings
	PRWLimitsEnabled bool
	PRWLimitsConfig  string
	PRWLimitsDryRun  bool

	// Performance settings
	ExportConcurrency    int
	StringInterning      bool
	InternMaxValueLength int

	// Cardinality tracking settings
	CardinalityMode          string  // "bloom" or "exact"
	CardinalityExpectedItems uint    // Expected unique items per tracker
	CardinalityFPRate        float64 // False positive rate for Bloom filter

	// Bloom persistence settings
	BloomPersistenceEnabled          bool
	BloomPersistencePath             string
	BloomPersistenceSaveInterval     time.Duration
	BloomPersistenceStateTTL         time.Duration
	BloomPersistenceCleanupInterval  time.Duration
	BloomPersistenceMaxSize          int64 // Max disk space for bloom state
	BloomPersistenceMaxMemory        int64 // Max memory for in-memory bloom filters
	BloomPersistenceCompression      bool
	BloomPersistenceCompressionLevel int

	// Flags
	ShowHelp    bool
	ShowVersion bool
}

// ParseFlags parses command line flags and returns the configuration.
func ParseFlags() *Config {
	cfg := DefaultConfig()

	// Config file flag
	var configFile string
	flag.StringVar(&configFile, "config", "", "Path to YAML configuration file")

	// Receiver flags
	flag.StringVar(&cfg.GRPCListenAddr, "grpc-listen", ":4317", "gRPC receiver listen address")
	flag.StringVar(&cfg.HTTPListenAddr, "http-listen", ":4318", "HTTP receiver listen address")
	flag.StringVar(&cfg.HTTPReceiverPath, "http-receiver-path", "/v1/metrics", "HTTP receiver path for OTLP metrics")

	// Receiver TLS flags
	flag.BoolVar(&cfg.ReceiverTLSEnabled, "receiver-tls-enabled", false, "Enable TLS for receivers")
	flag.StringVar(&cfg.ReceiverTLSCertFile, "receiver-tls-cert", "", "Path to receiver TLS certificate file")
	flag.StringVar(&cfg.ReceiverTLSKeyFile, "receiver-tls-key", "", "Path to receiver TLS private key file")
	flag.StringVar(&cfg.ReceiverTLSCAFile, "receiver-tls-ca", "", "Path to CA certificate for client verification (mTLS)")
	flag.BoolVar(&cfg.ReceiverTLSClientAuth, "receiver-tls-client-auth", false, "Require client certificates (mTLS)")

	// Receiver Auth flags
	flag.BoolVar(&cfg.ReceiverAuthEnabled, "receiver-auth-enabled", false, "Enable authentication for receivers")
	flag.StringVar(&cfg.ReceiverAuthBearerToken, "receiver-auth-bearer-token", "", "Bearer token for receiver authentication")
	flag.StringVar(&cfg.ReceiverAuthBasicUsername, "receiver-auth-basic-username", "", "Basic auth username for receivers")
	flag.StringVar(&cfg.ReceiverAuthBasicPassword, "receiver-auth-basic-password", "", "Basic auth password for receivers")

	// Exporter flags (supports OTLP gRPC/HTTP to any backend: otel-collector, Prometheus, Mimir, VictoriaMetrics, etc.)
	flag.StringVar(&cfg.ExporterEndpoint, "exporter-endpoint", "localhost:4317", "OTLP exporter endpoint (host:port or URL)")
	flag.StringVar(&cfg.ExporterProtocol, "exporter-protocol", "grpc", "Exporter protocol: grpc (default, most backends) or http (OTLP/HTTP)")
	flag.BoolVar(&cfg.ExporterInsecure, "exporter-insecure", true, "Use insecure connection (no TLS) for exporter")
	flag.DurationVar(&cfg.ExporterTimeout, "exporter-timeout", 30*time.Second, "Exporter request timeout")
	flag.StringVar(&cfg.ExporterDefaultPath, "exporter-default-path", "/v1/metrics", "Default HTTP path when endpoint has no path (e.g., /v1/metrics for standard OTLP, /opentelemetry/v1/metrics for VictoriaMetrics)")

	// Exporter TLS flags
	flag.BoolVar(&cfg.ExporterTLSEnabled, "exporter-tls-enabled", false, "Enable custom TLS config for exporter")
	flag.StringVar(&cfg.ExporterTLSCertFile, "exporter-tls-cert", "", "Path to client certificate file (mTLS)")
	flag.StringVar(&cfg.ExporterTLSKeyFile, "exporter-tls-key", "", "Path to client private key file (mTLS)")
	flag.StringVar(&cfg.ExporterTLSCAFile, "exporter-tls-ca", "", "Path to CA certificate for server verification")
	flag.BoolVar(&cfg.ExporterTLSInsecureSkipVerify, "exporter-tls-skip-verify", false, "Skip TLS certificate verification")
	flag.StringVar(&cfg.ExporterTLSServerName, "exporter-tls-server-name", "", "Override server name for TLS verification")

	// Exporter Auth flags
	flag.StringVar(&cfg.ExporterAuthBearerToken, "exporter-auth-bearer-token", "", "Bearer token for exporter authentication")
	flag.StringVar(&cfg.ExporterAuthBasicUsername, "exporter-auth-basic-username", "", "Basic auth username for exporter")
	flag.StringVar(&cfg.ExporterAuthBasicPassword, "exporter-auth-basic-password", "", "Basic auth password for exporter")
	flag.StringVar(&cfg.ExporterAuthHeaders, "exporter-auth-headers", "", "Custom headers for exporter (format: key1=value1,key2=value2)")

	// Exporter Compression flags (gRPC uses built-in compression, HTTP uses Content-Encoding)
	flag.StringVar(&cfg.ExporterCompression, "exporter-compression", "none", "Compression: none, gzip (widely supported), zstd (high perf), snappy, zlib, deflate, lz4")
	flag.IntVar(&cfg.ExporterCompressionLevel, "exporter-compression-level", 0, "Compression level (algorithm-specific, 0 for default)")

	// Exporter HTTP client flags
	flag.IntVar(&cfg.ExporterMaxIdleConns, "exporter-max-idle-conns", 100, "Maximum idle connections across all hosts")
	flag.IntVar(&cfg.ExporterMaxIdleConnsPerHost, "exporter-max-idle-conns-per-host", 100, "Maximum idle connections per host")
	flag.IntVar(&cfg.ExporterMaxConnsPerHost, "exporter-max-conns-per-host", 0, "Maximum total connections per host (0 = no limit)")
	flag.DurationVar(&cfg.ExporterIdleConnTimeout, "exporter-idle-conn-timeout", 90*time.Second, "Idle connection timeout")
	flag.BoolVar(&cfg.ExporterDisableKeepAlives, "exporter-disable-keep-alives", false, "Disable HTTP keep-alives")
	flag.BoolVar(&cfg.ExporterForceHTTP2, "exporter-force-http2", false, "Force HTTP/2 for non-TLS connections")
	flag.DurationVar(&cfg.ExporterHTTP2ReadIdleTimeout, "exporter-http2-read-idle-timeout", 0, "HTTP/2 read idle timeout for health checks")
	flag.DurationVar(&cfg.ExporterHTTP2PingTimeout, "exporter-http2-ping-timeout", 0, "HTTP/2 ping timeout")

	// Receiver HTTP server flags
	flag.Int64Var(&cfg.ReceiverMaxRequestBodySize, "receiver-max-request-body-size", 0, "Maximum request body size in bytes (0 = no limit)")
	flag.DurationVar(&cfg.ReceiverReadTimeout, "receiver-read-timeout", 0, "HTTP server read timeout (0 = no timeout)")
	flag.DurationVar(&cfg.ReceiverReadHeaderTimeout, "receiver-read-header-timeout", 1*time.Minute, "HTTP server read header timeout")
	flag.DurationVar(&cfg.ReceiverWriteTimeout, "receiver-write-timeout", 30*time.Second, "HTTP server write timeout")
	flag.DurationVar(&cfg.ReceiverIdleTimeout, "receiver-idle-timeout", 1*time.Minute, "HTTP server idle timeout")
	flag.BoolVar(&cfg.ReceiverKeepAlivesEnabled, "receiver-keep-alives-enabled", true, "Enable HTTP keep-alives for receiver")

	// Buffer flags
	flag.IntVar(&cfg.BufferSize, "buffer-size", 10000, "Maximum number of metrics to buffer")
	flag.DurationVar(&cfg.FlushInterval, "flush-interval", 5*time.Second, "Buffer flush interval")
	flag.IntVar(&cfg.MaxBatchSize, "batch-size", 1000, "Maximum batch size for export")

	// Stats flags
	flag.StringVar(&cfg.StatsAddr, "stats-addr", ":9090", "Stats/metrics HTTP endpoint address")
	flag.StringVar(&cfg.StatsLabels, "stats-labels", "", "Comma-separated labels to track for grouping (e.g., service,env,cluster)")

	// Limits flags
	flag.StringVar(&cfg.LimitsConfig, "limits-config", "", "Path to limits configuration YAML file")
	flag.BoolVar(&cfg.LimitsDryRun, "limits-dry-run", true, "Dry run mode: log violations but don't drop/sample")
	flag.IntVar(&cfg.RuleCacheMaxSize, "rule-cache-max-size", 10000, "Maximum entries in the rule matching LRU cache (0 disables)")

	// Queue flags
	flag.BoolVar(&cfg.QueueEnabled, "queue-enabled", false, "Enable persistent queue for export retries")
	flag.StringVar(&cfg.QueuePath, "queue-path", "./queue", "Queue storage directory")
	flag.IntVar(&cfg.QueueMaxSize, "queue-max-size", 10000, "Maximum number of batches in queue")
	flag.Int64Var(&cfg.QueueMaxBytes, "queue-max-bytes", 1073741824, "Maximum total queue size in bytes (1GB default)")
	flag.DurationVar(&cfg.QueueRetryInterval, "queue-retry-interval", 5*time.Second, "Initial retry interval")
	flag.DurationVar(&cfg.QueueMaxRetryDelay, "queue-max-retry-delay", 5*time.Minute, "Maximum retry backoff delay")
	flag.StringVar(&cfg.QueueFullBehavior, "queue-full-behavior", "drop_oldest", "Queue full behavior: drop_oldest, drop_newest, or block")
	flag.Float64Var(&cfg.QueueTargetUtilization, "queue-target-utilization", 0.85, "Target disk utilization for adaptive sizing (0.0-1.0)")
	flag.BoolVar(&cfg.QueueAdaptiveEnabled, "queue-adaptive-enabled", true, "Enable adaptive queue sizing based on disk space")
	flag.Float64Var(&cfg.QueueCompactThreshold, "queue-compact-threshold", 0.5, "Ratio of consumed entries before compaction (0.0-1.0)")
	// FastQueue flags
	flag.IntVar(&cfg.QueueInmemoryBlocks, "queue-inmemory-blocks", 256, "In-memory channel size for FastQueue")
	flag.Int64Var(&cfg.QueueChunkSize, "queue-chunk-size", 536870912, "Chunk file size in bytes (512MB default)")
	flag.DurationVar(&cfg.QueueMetaSyncInterval, "queue-meta-sync", 1*time.Second, "Metadata sync interval (max data loss window)")
	flag.DurationVar(&cfg.QueueStaleFlushInterval, "queue-stale-flush", 5*time.Second, "Interval to flush stale in-memory blocks to disk")
	// Backoff flags
	flag.BoolVar(&cfg.QueueBackoffEnabled, "queue-backoff-enabled", true, "Enable exponential backoff for retries")
	flag.Float64Var(&cfg.QueueBackoffMultiplier, "queue-backoff-multiplier", 2.0, "Backoff delay multiplier on each failure")
	// Circuit breaker flags
	flag.BoolVar(&cfg.QueueCircuitBreakerEnabled, "queue-circuit-breaker-enabled", true, "Enable circuit breaker pattern for retries")
	flag.IntVar(&cfg.QueueCircuitBreakerThreshold, "queue-circuit-breaker-threshold", 10, "Consecutive failures before opening circuit")
	flag.DurationVar(&cfg.QueueCircuitBreakerResetTimeout, "queue-circuit-breaker-reset-timeout", 30*time.Second, "Time to wait before half-open state")

	// Memory limit flags
	flag.Float64Var(&cfg.MemoryLimitRatio, "memory-limit-ratio", 0.9, "Ratio of container memory to use for GOMEMLIMIT (0.0-1.0)")

	// Sharding flags
	flag.BoolVar(&cfg.ShardingEnabled, "sharding-enabled", false, "Enable consistent sharding")
	flag.StringVar(&cfg.ShardingHeadlessService, "sharding-headless-service", "", "K8s headless service DNS name")
	flag.DurationVar(&cfg.ShardingDNSRefreshInterval, "sharding-dns-refresh-interval", 30*time.Second, "DNS refresh interval")
	flag.DurationVar(&cfg.ShardingDNSTimeout, "sharding-dns-timeout", 5*time.Second, "DNS lookup timeout")
	flag.StringVar(&cfg.ShardingLabels, "sharding-labels", "", "Comma-separated labels for shard key")
	flag.IntVar(&cfg.ShardingVirtualNodes, "sharding-virtual-nodes", 150, "Virtual nodes per endpoint")
	flag.BoolVar(&cfg.ShardingFallbackOnEmpty, "sharding-fallback-on-empty", true, "Use static endpoint if no DNS results")

	// PRW Receiver flags
	flag.StringVar(&cfg.PRWListenAddr, "prw-listen", "", "PRW receiver listen address (empty = disabled)")
	flag.StringVar(&cfg.PRWReceiverPath, "prw-receiver-path", "/api/v1/write", "PRW receiver path")
	flag.StringVar(&cfg.PRWReceiverVersion, "prw-receiver-version", "auto", "PRW protocol version: 1.0, 2.0, or auto")
	flag.BoolVar(&cfg.PRWReceiverTLSEnabled, "prw-receiver-tls-enabled", false, "Enable TLS for PRW receiver")
	flag.StringVar(&cfg.PRWReceiverTLSCertFile, "prw-receiver-tls-cert", "", "Path to PRW receiver TLS certificate file")
	flag.StringVar(&cfg.PRWReceiverTLSKeyFile, "prw-receiver-tls-key", "", "Path to PRW receiver TLS private key file")
	flag.StringVar(&cfg.PRWReceiverTLSCAFile, "prw-receiver-tls-ca", "", "Path to CA certificate for PRW client verification (mTLS)")
	flag.BoolVar(&cfg.PRWReceiverTLSClientAuth, "prw-receiver-tls-client-auth", false, "Require client certificates for PRW (mTLS)")
	flag.BoolVar(&cfg.PRWReceiverAuthEnabled, "prw-receiver-auth-enabled", false, "Enable authentication for PRW receiver")
	flag.StringVar(&cfg.PRWReceiverAuthBearerToken, "prw-receiver-auth-bearer-token", "", "Bearer token for PRW receiver authentication")
	flag.Int64Var(&cfg.PRWReceiverMaxRequestBodySize, "prw-receiver-max-body-size", 0, "Maximum PRW request body size (0 = no limit)")
	flag.DurationVar(&cfg.PRWReceiverReadTimeout, "prw-receiver-read-timeout", 1*time.Minute, "PRW receiver read timeout")
	flag.DurationVar(&cfg.PRWReceiverWriteTimeout, "prw-receiver-write-timeout", 30*time.Second, "PRW receiver write timeout")

	// PRW Exporter flags (supports Prometheus Remote Write to any backend: Prometheus, Mimir, Cortex, Thanos, VictoriaMetrics, etc.)
	flag.StringVar(&cfg.PRWExporterEndpoint, "prw-exporter-endpoint", "", "PRW exporter endpoint URL (empty = disabled)")
	flag.StringVar(&cfg.PRWExporterDefaultPath, "prw-exporter-default-path", "/api/v1/write", "Default PRW path when endpoint has no path (/api/v1/write standard, /write for VM short)")
	flag.StringVar(&cfg.PRWExporterVersion, "prw-exporter-version", "auto", "PRW protocol version: 1.0 (standard), 2.0 (native histograms), or auto")
	flag.DurationVar(&cfg.PRWExporterTimeout, "prw-exporter-timeout", 30*time.Second, "PRW exporter request timeout")
	flag.BoolVar(&cfg.PRWExporterTLSEnabled, "prw-exporter-tls-enabled", false, "Enable TLS for PRW exporter")
	flag.StringVar(&cfg.PRWExporterTLSCertFile, "prw-exporter-tls-cert", "", "Path to PRW client certificate (mTLS)")
	flag.StringVar(&cfg.PRWExporterTLSKeyFile, "prw-exporter-tls-key", "", "Path to PRW client private key (mTLS)")
	flag.StringVar(&cfg.PRWExporterTLSCAFile, "prw-exporter-tls-ca", "", "Path to CA certificate for PRW server verification")
	flag.BoolVar(&cfg.PRWExporterTLSSkipVerify, "prw-exporter-tls-skip-verify", false, "Skip TLS certificate verification for PRW")
	flag.StringVar(&cfg.PRWExporterAuthBearerToken, "prw-exporter-auth-bearer-token", "", "Bearer token for PRW exporter authentication")
	flag.BoolVar(&cfg.PRWExporterVMMode, "prw-exporter-vm-mode", false, "Enable VictoriaMetrics-specific optimizations (zstd, extra labels)")
	flag.StringVar(&cfg.PRWExporterVMCompression, "prw-exporter-vm-compression", "snappy", "PRW compression: snappy (standard) or zstd (VM optimized)")
	flag.BoolVar(&cfg.PRWExporterVMShortEndpoint, "prw-exporter-vm-short-endpoint", false, "Use /write shorthand instead of /api/v1/write")
	flag.StringVar(&cfg.PRWExporterVMExtraLabels, "prw-exporter-vm-extra-labels", "", "Extra labels to add to all metrics (format: key1=val1,key2=val2)")

	// PRW Buffer flags
	flag.IntVar(&cfg.PRWBufferSize, "prw-buffer-size", 10000, "Maximum PRW requests to buffer")
	flag.IntVar(&cfg.PRWBatchSize, "prw-batch-size", 1000, "Maximum batch size for PRW export")
	flag.DurationVar(&cfg.PRWFlushInterval, "prw-flush-interval", 5*time.Second, "PRW buffer flush interval")

	// PRW Queue flags
	flag.BoolVar(&cfg.PRWQueueEnabled, "prw-queue-enabled", false, "Enable persistent queue for PRW retries")
	flag.StringVar(&cfg.PRWQueuePath, "prw-queue-path", "./prw-queue", "PRW queue storage directory")
	flag.IntVar(&cfg.PRWQueueMaxSize, "prw-queue-max-size", 10000, "Maximum PRW entries in queue")
	flag.Int64Var(&cfg.PRWQueueMaxBytes, "prw-queue-max-bytes", 1073741824, "Maximum PRW queue size in bytes")
	flag.DurationVar(&cfg.PRWQueueRetryInterval, "prw-queue-retry-interval", 5*time.Second, "PRW queue retry interval")
	flag.DurationVar(&cfg.PRWQueueMaxRetryDelay, "prw-queue-max-retry-delay", 5*time.Minute, "Maximum PRW retry delay")

	// PRW Limits flags
	flag.BoolVar(&cfg.PRWLimitsEnabled, "prw-limits-enabled", false, "Enable limits for PRW pipeline")
	flag.StringVar(&cfg.PRWLimitsConfig, "prw-limits-config", "", "Path to PRW limits configuration YAML")
	flag.BoolVar(&cfg.PRWLimitsDryRun, "prw-limits-dry-run", true, "PRW limits dry run mode")

	// Performance tuning flags
	flag.IntVar(&cfg.ExportConcurrency, "export-concurrency", 0, "Concurrency limit for parallel exports (0 = NumCPU * 4)")
	flag.BoolVar(&cfg.StringInterning, "string-interning", true, "Enable string interning for label deduplication")
	flag.IntVar(&cfg.InternMaxValueLength, "intern-max-value-length", 64, "Max length for value interning (longer values not interned)")

	// Cardinality tracking flags
	flag.StringVar(&cfg.CardinalityMode, "cardinality-mode", "bloom", "Cardinality tracking mode: bloom (memory-efficient) or exact (100% accurate)")
	flag.UintVar(&cfg.CardinalityExpectedItems, "cardinality-expected-items", 100000, "Expected unique items per tracker for Bloom filter sizing")
	flag.Float64Var(&cfg.CardinalityFPRate, "cardinality-fp-rate", 0.01, "Bloom filter false positive rate (0.01 = 1%)")

	// Bloom persistence flags
	flag.BoolVar(&cfg.BloomPersistenceEnabled, "bloom-persistence-enabled", false, "Enable bloom filter state persistence")
	flag.StringVar(&cfg.BloomPersistencePath, "bloom-persistence-path", "./bloom-state", "Directory for bloom filter state persistence")
	flag.DurationVar(&cfg.BloomPersistenceSaveInterval, "bloom-persistence-save-interval", 30*time.Second, "Interval between periodic saves")
	flag.DurationVar(&cfg.BloomPersistenceStateTTL, "bloom-persistence-state-ttl", time.Hour, "Time after which unused trackers are cleaned up")
	flag.DurationVar(&cfg.BloomPersistenceCleanupInterval, "bloom-persistence-cleanup-interval", 5*time.Minute, "Interval between cleanup runs")
	flag.Int64Var(&cfg.BloomPersistenceMaxSize, "bloom-persistence-max-size", 524288000, "Max disk space for bloom state in bytes (500MB)")
	flag.Int64Var(&cfg.BloomPersistenceMaxMemory, "bloom-persistence-max-memory", 268435456, "Max memory for in-memory bloom filters in bytes (256MB)")
	flag.BoolVar(&cfg.BloomPersistenceCompression, "bloom-persistence-compression", true, "Enable gzip compression for bloom state files")
	flag.IntVar(&cfg.BloomPersistenceCompressionLevel, "bloom-persistence-compression-level", 1, "Gzip compression level (1=fast, 9=best)")

	// Help and version
	flag.BoolVar(&cfg.ShowHelp, "help", false, "Show help message")
	flag.BoolVar(&cfg.ShowHelp, "h", false, "Show help message (shorthand)")
	flag.BoolVar(&cfg.ShowVersion, "version", false, "Show version")
	flag.BoolVar(&cfg.ShowVersion, "v", false, "Show version (shorthand)")

	flag.Usage = PrintUsage

	flag.Parse()

	// Load YAML config if specified
	if configFile != "" {
		yamlCfg, err := LoadYAML(configFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error loading config file %s: %v\n", configFile, err)
			os.Exit(1)
		}
		cfg = yamlCfg.ToConfig()
	}

	// Apply CLI overrides for explicitly set flags
	applyFlagOverrides(cfg)

	return cfg
}

// applyFlagOverrides applies CLI flag values that were explicitly set.
func applyFlagOverrides(cfg *Config) {
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "grpc-listen":
			cfg.GRPCListenAddr = f.Value.String()
		case "http-listen":
			cfg.HTTPListenAddr = f.Value.String()
		case "receiver-tls-enabled":
			cfg.ReceiverTLSEnabled = f.Value.String() == "true"
		case "receiver-tls-cert":
			cfg.ReceiverTLSCertFile = f.Value.String()
		case "receiver-tls-key":
			cfg.ReceiverTLSKeyFile = f.Value.String()
		case "receiver-tls-ca":
			cfg.ReceiverTLSCAFile = f.Value.String()
		case "receiver-tls-client-auth":
			cfg.ReceiverTLSClientAuth = f.Value.String() == "true"
		case "receiver-auth-enabled":
			cfg.ReceiverAuthEnabled = f.Value.String() == "true"
		case "receiver-auth-bearer-token":
			cfg.ReceiverAuthBearerToken = f.Value.String()
		case "receiver-auth-basic-username":
			cfg.ReceiverAuthBasicUsername = f.Value.String()
		case "receiver-auth-basic-password":
			cfg.ReceiverAuthBasicPassword = f.Value.String()
		case "exporter-endpoint":
			cfg.ExporterEndpoint = f.Value.String()
		case "exporter-protocol":
			cfg.ExporterProtocol = f.Value.String()
		case "exporter-insecure":
			cfg.ExporterInsecure = f.Value.String() == "true"
		case "exporter-timeout":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.ExporterTimeout = d
			}
		case "exporter-tls-enabled":
			cfg.ExporterTLSEnabled = f.Value.String() == "true"
		case "exporter-tls-cert":
			cfg.ExporterTLSCertFile = f.Value.String()
		case "exporter-tls-key":
			cfg.ExporterTLSKeyFile = f.Value.String()
		case "exporter-tls-ca":
			cfg.ExporterTLSCAFile = f.Value.String()
		case "exporter-tls-skip-verify":
			cfg.ExporterTLSInsecureSkipVerify = f.Value.String() == "true"
		case "exporter-tls-server-name":
			cfg.ExporterTLSServerName = f.Value.String()
		case "exporter-auth-bearer-token":
			cfg.ExporterAuthBearerToken = f.Value.String()
		case "exporter-auth-basic-username":
			cfg.ExporterAuthBasicUsername = f.Value.String()
		case "exporter-auth-basic-password":
			cfg.ExporterAuthBasicPassword = f.Value.String()
		case "exporter-auth-headers":
			cfg.ExporterAuthHeaders = f.Value.String()
		case "exporter-compression":
			cfg.ExporterCompression = f.Value.String()
		case "exporter-compression-level":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int); ok {
					cfg.ExporterCompressionLevel = i
				}
			}
		case "exporter-max-idle-conns":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int); ok {
					cfg.ExporterMaxIdleConns = i
				}
			}
		case "exporter-max-idle-conns-per-host":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int); ok {
					cfg.ExporterMaxIdleConnsPerHost = i
				}
			}
		case "exporter-max-conns-per-host":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int); ok {
					cfg.ExporterMaxConnsPerHost = i
				}
			}
		case "exporter-idle-conn-timeout":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.ExporterIdleConnTimeout = d
			}
		case "exporter-disable-keep-alives":
			cfg.ExporterDisableKeepAlives = f.Value.String() == "true"
		case "exporter-force-http2":
			cfg.ExporterForceHTTP2 = f.Value.String() == "true"
		case "exporter-http2-read-idle-timeout":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.ExporterHTTP2ReadIdleTimeout = d
			}
		case "exporter-http2-ping-timeout":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.ExporterHTTP2PingTimeout = d
			}
		case "receiver-max-request-body-size":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int64); ok {
					cfg.ReceiverMaxRequestBodySize = i
				}
			}
		case "receiver-read-timeout":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.ReceiverReadTimeout = d
			}
		case "receiver-read-header-timeout":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.ReceiverReadHeaderTimeout = d
			}
		case "receiver-write-timeout":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.ReceiverWriteTimeout = d
			}
		case "receiver-idle-timeout":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.ReceiverIdleTimeout = d
			}
		case "receiver-keep-alives-enabled":
			cfg.ReceiverKeepAlivesEnabled = f.Value.String() == "true"
		case "buffer-size":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int); ok {
					cfg.BufferSize = i
				}
			}
		case "flush-interval":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.FlushInterval = d
			}
		case "batch-size":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int); ok {
					cfg.MaxBatchSize = i
				}
			}
		case "stats-addr":
			cfg.StatsAddr = f.Value.String()
		case "stats-labels":
			cfg.StatsLabels = f.Value.String()
		case "limits-config":
			cfg.LimitsConfig = f.Value.String()
		case "limits-dry-run":
			cfg.LimitsDryRun = f.Value.String() == "true"
		case "rule-cache-max-size":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int); ok {
					cfg.RuleCacheMaxSize = i
				}
			}
		case "queue-enabled":
			cfg.QueueEnabled = f.Value.String() == "true"
		case "queue-path":
			cfg.QueuePath = f.Value.String()
		case "queue-max-size":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int); ok {
					cfg.QueueMaxSize = i
				}
			}
		case "queue-max-bytes":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int64); ok {
					cfg.QueueMaxBytes = i
				}
			}
		case "queue-retry-interval":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.QueueRetryInterval = d
			}
		case "queue-max-retry-delay":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.QueueMaxRetryDelay = d
			}
		case "queue-full-behavior":
			cfg.QueueFullBehavior = f.Value.String()
		case "queue-target-utilization":
			if v, ok := f.Value.(flag.Getter); ok {
				if fv, ok := v.Get().(float64); ok {
					cfg.QueueTargetUtilization = fv
				}
			}
		case "queue-adaptive-enabled":
			cfg.QueueAdaptiveEnabled = f.Value.String() == "true"
		case "queue-compact-threshold":
			if v, ok := f.Value.(flag.Getter); ok {
				if fv, ok := v.Get().(float64); ok {
					cfg.QueueCompactThreshold = fv
				}
			}
		case "queue-inmemory-blocks":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int); ok {
					cfg.QueueInmemoryBlocks = i
				}
			}
		case "queue-chunk-size":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int64); ok {
					cfg.QueueChunkSize = i
				}
			}
		case "queue-meta-sync":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.QueueMetaSyncInterval = d
			}
		case "queue-stale-flush":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.QueueStaleFlushInterval = d
			}
		case "queue-backoff-enabled":
			cfg.QueueBackoffEnabled = f.Value.String() == "true"
		case "queue-backoff-multiplier":
			if v, ok := f.Value.(flag.Getter); ok {
				if fv, ok := v.Get().(float64); ok {
					cfg.QueueBackoffMultiplier = fv
				}
			}
		case "queue-circuit-breaker-enabled":
			cfg.QueueCircuitBreakerEnabled = f.Value.String() == "true"
		case "queue-circuit-breaker-threshold":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int); ok {
					cfg.QueueCircuitBreakerThreshold = i
				}
			}
		case "queue-circuit-breaker-reset-timeout":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.QueueCircuitBreakerResetTimeout = d
			}
		case "memory-limit-ratio":
			if v, ok := f.Value.(flag.Getter); ok {
				if fv, ok := v.Get().(float64); ok {
					cfg.MemoryLimitRatio = fv
				}
			}
		case "sharding-enabled":
			cfg.ShardingEnabled = f.Value.String() == "true"
		case "sharding-headless-service":
			cfg.ShardingHeadlessService = f.Value.String()
		case "sharding-dns-refresh-interval":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.ShardingDNSRefreshInterval = d
			}
		case "sharding-dns-timeout":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.ShardingDNSTimeout = d
			}
		case "sharding-labels":
			cfg.ShardingLabels = f.Value.String()
		case "sharding-virtual-nodes":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int); ok {
					cfg.ShardingVirtualNodes = i
				}
			}
		case "sharding-fallback-on-empty":
			cfg.ShardingFallbackOnEmpty = f.Value.String() == "true"
		case "prw-listen":
			cfg.PRWListenAddr = f.Value.String()
		case "prw-receiver-version":
			cfg.PRWReceiverVersion = f.Value.String()
		case "prw-receiver-tls-enabled":
			cfg.PRWReceiverTLSEnabled = f.Value.String() == "true"
		case "prw-receiver-tls-cert":
			cfg.PRWReceiverTLSCertFile = f.Value.String()
		case "prw-receiver-tls-key":
			cfg.PRWReceiverTLSKeyFile = f.Value.String()
		case "prw-receiver-tls-ca":
			cfg.PRWReceiverTLSCAFile = f.Value.String()
		case "prw-receiver-tls-client-auth":
			cfg.PRWReceiverTLSClientAuth = f.Value.String() == "true"
		case "prw-receiver-auth-enabled":
			cfg.PRWReceiverAuthEnabled = f.Value.String() == "true"
		case "prw-receiver-auth-bearer-token":
			cfg.PRWReceiverAuthBearerToken = f.Value.String()
		case "prw-receiver-max-body-size":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int64); ok {
					cfg.PRWReceiverMaxRequestBodySize = i
				}
			}
		case "prw-receiver-read-timeout":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.PRWReceiverReadTimeout = d
			}
		case "prw-receiver-write-timeout":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.PRWReceiverWriteTimeout = d
			}
		case "prw-exporter-endpoint":
			cfg.PRWExporterEndpoint = f.Value.String()
		case "prw-exporter-version":
			cfg.PRWExporterVersion = f.Value.String()
		case "prw-exporter-timeout":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.PRWExporterTimeout = d
			}
		case "prw-exporter-tls-enabled":
			cfg.PRWExporterTLSEnabled = f.Value.String() == "true"
		case "prw-exporter-tls-cert":
			cfg.PRWExporterTLSCertFile = f.Value.String()
		case "prw-exporter-tls-key":
			cfg.PRWExporterTLSKeyFile = f.Value.String()
		case "prw-exporter-tls-ca":
			cfg.PRWExporterTLSCAFile = f.Value.String()
		case "prw-exporter-tls-skip-verify":
			cfg.PRWExporterTLSSkipVerify = f.Value.String() == "true"
		case "prw-exporter-auth-bearer-token":
			cfg.PRWExporterAuthBearerToken = f.Value.String()
		case "prw-exporter-vm-mode":
			cfg.PRWExporterVMMode = f.Value.String() == "true"
		case "prw-exporter-vm-compression":
			cfg.PRWExporterVMCompression = f.Value.String()
		case "prw-exporter-vm-short-endpoint":
			cfg.PRWExporterVMShortEndpoint = f.Value.String() == "true"
		case "prw-exporter-vm-extra-labels":
			cfg.PRWExporterVMExtraLabels = f.Value.String()
		case "prw-buffer-size":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int); ok {
					cfg.PRWBufferSize = i
				}
			}
		case "prw-batch-size":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int); ok {
					cfg.PRWBatchSize = i
				}
			}
		case "prw-flush-interval":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.PRWFlushInterval = d
			}
		case "prw-queue-enabled":
			cfg.PRWQueueEnabled = f.Value.String() == "true"
		case "prw-queue-path":
			cfg.PRWQueuePath = f.Value.String()
		case "prw-queue-max-size":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int); ok {
					cfg.PRWQueueMaxSize = i
				}
			}
		case "prw-queue-max-bytes":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int64); ok {
					cfg.PRWQueueMaxBytes = i
				}
			}
		case "prw-queue-retry-interval":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.PRWQueueRetryInterval = d
			}
		case "prw-queue-max-retry-delay":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.PRWQueueMaxRetryDelay = d
			}
		case "prw-limits-enabled":
			cfg.PRWLimitsEnabled = f.Value.String() == "true"
		case "prw-limits-config":
			cfg.PRWLimitsConfig = f.Value.String()
		case "prw-limits-dry-run":
			cfg.PRWLimitsDryRun = f.Value.String() == "true"
		case "export-concurrency":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int); ok {
					cfg.ExportConcurrency = i
				}
			}
		case "string-interning":
			cfg.StringInterning = f.Value.String() == "true"
		case "intern-max-value-length":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int); ok {
					cfg.InternMaxValueLength = i
				}
			}
		case "cardinality-mode":
			cfg.CardinalityMode = f.Value.String()
		case "cardinality-expected-items":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(uint); ok {
					cfg.CardinalityExpectedItems = i
				}
			}
		case "cardinality-fp-rate":
			if v, ok := f.Value.(flag.Getter); ok {
				if fv, ok := v.Get().(float64); ok {
					cfg.CardinalityFPRate = fv
				}
			}
		case "bloom-persistence-enabled":
			cfg.BloomPersistenceEnabled = f.Value.String() == "true"
		case "bloom-persistence-path":
			cfg.BloomPersistencePath = f.Value.String()
		case "bloom-persistence-save-interval":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.BloomPersistenceSaveInterval = d
			}
		case "bloom-persistence-state-ttl":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.BloomPersistenceStateTTL = d
			}
		case "bloom-persistence-cleanup-interval":
			if d, err := time.ParseDuration(f.Value.String()); err == nil {
				cfg.BloomPersistenceCleanupInterval = d
			}
		case "bloom-persistence-max-size":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int64); ok {
					cfg.BloomPersistenceMaxSize = i
				}
			}
		case "bloom-persistence-max-memory":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int64); ok {
					cfg.BloomPersistenceMaxMemory = i
				}
			}
		case "bloom-persistence-compression":
			cfg.BloomPersistenceCompression = f.Value.String() == "true"
		case "bloom-persistence-compression-level":
			if v, ok := f.Value.(flag.Getter); ok {
				if i, ok := v.Get().(int); ok {
					cfg.BloomPersistenceCompressionLevel = i
				}
			}
		case "help", "h":
			cfg.ShowHelp = f.Value.String() == "true"
		case "version", "v":
			cfg.ShowVersion = f.Value.String() == "true"
		}
	})
}

// ReceiverTLSConfig returns the TLS configuration for receivers.
func (c *Config) ReceiverTLSConfig() tlspkg.ServerConfig {
	return tlspkg.ServerConfig{
		Enabled:    c.ReceiverTLSEnabled,
		CertFile:   c.ReceiverTLSCertFile,
		KeyFile:    c.ReceiverTLSKeyFile,
		CAFile:     c.ReceiverTLSCAFile,
		ClientAuth: c.ReceiverTLSClientAuth,
	}
}

// ReceiverAuthConfig returns the auth configuration for receivers.
func (c *Config) ReceiverAuthConfig() auth.ServerConfig {
	return auth.ServerConfig{
		Enabled:           c.ReceiverAuthEnabled,
		BearerToken:       c.ReceiverAuthBearerToken,
		BasicAuthUsername: c.ReceiverAuthBasicUsername,
		BasicAuthPassword: c.ReceiverAuthBasicPassword,
	}
}

// GRPCReceiverConfig returns the full gRPC receiver configuration.
func (c *Config) GRPCReceiverConfig() receiver.GRPCConfig {
	return receiver.GRPCConfig{
		Addr: c.GRPCListenAddr,
		TLS:  c.ReceiverTLSConfig(),
		Auth: c.ReceiverAuthConfig(),
	}
}

// HTTPReceiverConfig returns the full HTTP receiver configuration.
func (c *Config) HTTPReceiverConfig() receiver.HTTPConfig {
	return receiver.HTTPConfig{
		Addr: c.HTTPListenAddr,
		Path: c.HTTPReceiverPath,
		TLS:  c.ReceiverTLSConfig(),
		Auth: c.ReceiverAuthConfig(),
		Server: receiver.HTTPServerConfig{
			MaxRequestBodySize: c.ReceiverMaxRequestBodySize,
			ReadTimeout:        c.ReceiverReadTimeout,
			ReadHeaderTimeout:  c.ReceiverReadHeaderTimeout,
			WriteTimeout:       c.ReceiverWriteTimeout,
			IdleTimeout:        c.ReceiverIdleTimeout,
			KeepAlivesEnabled:  c.ReceiverKeepAlivesEnabled,
		},
	}
}

// ExporterTLSConfig returns the TLS configuration for the exporter.
func (c *Config) ExporterTLSConfig() tlspkg.ClientConfig {
	return tlspkg.ClientConfig{
		Enabled:            c.ExporterTLSEnabled,
		CertFile:           c.ExporterTLSCertFile,
		KeyFile:            c.ExporterTLSKeyFile,
		CAFile:             c.ExporterTLSCAFile,
		InsecureSkipVerify: c.ExporterTLSInsecureSkipVerify,
		ServerName:         c.ExporterTLSServerName,
	}
}

// ExporterAuthConfig returns the auth configuration for the exporter.
func (c *Config) ExporterAuthConfig() auth.ClientConfig {
	headers := make(map[string]string)
	if c.ExporterAuthHeaders != "" {
		pairs := strings.Split(c.ExporterAuthHeaders, ",")
		for _, pair := range pairs {
			kv := strings.SplitN(pair, "=", 2)
			if len(kv) == 2 {
				headers[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
			}
		}
	}

	return auth.ClientConfig{
		BearerToken:       c.ExporterAuthBearerToken,
		BasicAuthUsername: c.ExporterAuthBasicUsername,
		BasicAuthPassword: c.ExporterAuthBasicPassword,
		Headers:           headers,
	}
}

// ExporterCompressionConfig returns the compression configuration for the exporter.
func (c *Config) ExporterCompressionConfig() compression.Config {
	compressionType, _ := compression.ParseType(c.ExporterCompression)
	return compression.Config{
		Type:  compressionType,
		Level: compression.Level(c.ExporterCompressionLevel),
	}
}

// ExporterHTTPClientConfig returns the HTTP client configuration for the exporter.
func (c *Config) ExporterHTTPClientConfig() exporter.HTTPClientConfig {
	return exporter.HTTPClientConfig{
		MaxIdleConns:         c.ExporterMaxIdleConns,
		MaxIdleConnsPerHost:  c.ExporterMaxIdleConnsPerHost,
		MaxConnsPerHost:      c.ExporterMaxConnsPerHost,
		IdleConnTimeout:      c.ExporterIdleConnTimeout,
		DisableKeepAlives:    c.ExporterDisableKeepAlives,
		ForceAttemptHTTP2:    c.ExporterForceHTTP2,
		HTTP2ReadIdleTimeout: c.ExporterHTTP2ReadIdleTimeout,
		HTTP2PingTimeout:     c.ExporterHTTP2PingTimeout,
	}
}

// ExporterConfig returns the full exporter configuration.
func (c *Config) ExporterConfig() exporter.Config {
	return exporter.Config{
		Endpoint:    c.ExporterEndpoint,
		Protocol:    exporter.Protocol(c.ExporterProtocol),
		Insecure:    c.ExporterInsecure,
		Timeout:     c.ExporterTimeout,
		DefaultPath: c.ExporterDefaultPath,
		TLS:         c.ExporterTLSConfig(),
		Auth:        c.ExporterAuthConfig(),
		Compression: c.ExporterCompressionConfig(),
		HTTPClient:  c.ExporterHTTPClientConfig(),
	}
}

// QueueConfig returns the queue configuration.
func (c *Config) QueueConfig() QueueConfig {
	return QueueConfig{
		Enabled:           c.QueueEnabled,
		Path:              c.QueuePath,
		MaxSize:           c.QueueMaxSize,
		MaxBytes:          c.QueueMaxBytes,
		RetryInterval:     c.QueueRetryInterval,
		MaxRetryDelay:     c.QueueMaxRetryDelay,
		FullBehavior:      c.QueueFullBehavior,
		TargetUtilization: c.QueueTargetUtilization,
		AdaptiveEnabled:   c.QueueAdaptiveEnabled,
		CompactThreshold:  c.QueueCompactThreshold,
	}
}

// QueueConfig holds queue configuration.
type QueueConfig struct {
	Enabled           bool
	Path              string
	MaxSize           int
	MaxBytes          int64
	RetryInterval     time.Duration
	MaxRetryDelay     time.Duration
	FullBehavior      string
	TargetUtilization float64
	AdaptiveEnabled   bool
	CompactThreshold  float64
}

// ShardingConfig holds sharding configuration.
type ShardingConfig struct {
	Enabled            bool
	HeadlessService    string
	DNSRefreshInterval time.Duration
	DNSTimeout         time.Duration
	Labels             []string
	VirtualNodes       int
	FallbackOnEmpty    bool
}

// ShardingConfig returns the sharding configuration.
func (c *Config) ShardingConfig() ShardingConfig {
	var labels []string
	if c.ShardingLabels != "" {
		for _, l := range strings.Split(c.ShardingLabels, ",") {
			labels = append(labels, strings.TrimSpace(l))
		}
	}
	return ShardingConfig{
		Enabled:            c.ShardingEnabled,
		HeadlessService:    c.ShardingHeadlessService,
		DNSRefreshInterval: c.ShardingDNSRefreshInterval,
		DNSTimeout:         c.ShardingDNSTimeout,
		Labels:             labels,
		VirtualNodes:       c.ShardingVirtualNodes,
		FallbackOnEmpty:    c.ShardingFallbackOnEmpty,
	}
}

// PerformanceConfig holds performance tuning configuration.
type PerformanceConfig struct {
	// ExportConcurrency limits parallel export goroutines (0 = NumCPU * 4)
	ExportConcurrency int
	// StringInterning enables string interning for label deduplication
	StringInterning bool
	// InternMaxValueLength is the max length for value interning
	InternMaxValueLength int
}

// PerformanceConfig returns the performance tuning configuration.
func (c *Config) PerformanceConfig() PerformanceConfig {
	return PerformanceConfig{
		ExportConcurrency:    c.ExportConcurrency,
		StringInterning:      c.StringInterning,
		InternMaxValueLength: c.InternMaxValueLength,
	}
}

// CardinalityConfig holds cardinality tracking configuration.
type CardinalityConfig struct {
	// Mode is the tracking mode: "bloom" or "exact"
	Mode string
	// ExpectedItems is the expected unique items per tracker (for Bloom sizing)
	ExpectedItems uint
	// FPRate is the false positive rate for Bloom filters (e.g., 0.01 = 1%)
	FPRate float64
}

// CardinalityConfig returns the cardinality tracking configuration.
func (c *Config) CardinalityConfig() CardinalityConfig {
	return CardinalityConfig{
		Mode:          c.CardinalityMode,
		ExpectedItems: c.CardinalityExpectedItems,
		FPRate:        c.CardinalityFPRate,
	}
}

// BloomPersistenceConfig holds bloom filter persistence configuration.
type BloomPersistenceConfig struct {
	// Enabled enables persistence.
	Enabled bool
	// Path is the directory for persistence files.
	Path string
	// SaveInterval is the interval between periodic saves.
	SaveInterval time.Duration
	// StateTTL is the time after which unused trackers are cleaned up.
	StateTTL time.Duration
	// CleanupInterval is the interval between cleanup runs.
	CleanupInterval time.Duration
	// MaxSize is the maximum disk space for bloom state (bytes).
	MaxSize int64
	// MaxMemory is the maximum memory for in-memory bloom filters (bytes).
	MaxMemory int64
	// Compression enables gzip compression.
	Compression bool
	// CompressionLevel is the gzip compression level (1-9).
	CompressionLevel int
}

// BloomPersistenceConfig returns the bloom persistence configuration.
func (c *Config) BloomPersistenceConfig() BloomPersistenceConfig {
	return BloomPersistenceConfig{
		Enabled:          c.BloomPersistenceEnabled,
		Path:             c.BloomPersistencePath,
		SaveInterval:     c.BloomPersistenceSaveInterval,
		StateTTL:         c.BloomPersistenceStateTTL,
		CleanupInterval:  c.BloomPersistenceCleanupInterval,
		MaxSize:          c.BloomPersistenceMaxSize,
		MaxMemory:        c.BloomPersistenceMaxMemory,
		Compression:      c.BloomPersistenceCompression,
		CompressionLevel: c.BloomPersistenceCompressionLevel,
	}
}

// PRWReceiverConfig returns the PRW receiver configuration.
func (c *Config) PRWReceiverConfig() receiver.PRWConfig {
	version, _ := prw.ParseVersion(c.PRWReceiverVersion)
	return receiver.PRWConfig{
		Addr:    c.PRWListenAddr,
		Path:    c.PRWReceiverPath,
		Version: version,
		TLS: tlspkg.ServerConfig{
			Enabled:    c.PRWReceiverTLSEnabled,
			CertFile:   c.PRWReceiverTLSCertFile,
			KeyFile:    c.PRWReceiverTLSKeyFile,
			CAFile:     c.PRWReceiverTLSCAFile,
			ClientAuth: c.PRWReceiverTLSClientAuth,
		},
		Auth: auth.ServerConfig{
			Enabled:     c.PRWReceiverAuthEnabled,
			BearerToken: c.PRWReceiverAuthBearerToken,
		},
		Server: receiver.PRWServerConfig{
			MaxRequestBodySize: c.PRWReceiverMaxRequestBodySize,
			ReadTimeout:        c.PRWReceiverReadTimeout,
			WriteTimeout:       c.PRWReceiverWriteTimeout,
			ReadHeaderTimeout:  c.PRWReceiverReadTimeout,
			IdleTimeout:        c.PRWReceiverReadTimeout,
			KeepAlivesEnabled:  true,
		},
	}
}

// PRWExporterConfig returns the PRW exporter configuration.
func (c *Config) PRWExporterConfig() exporter.PRWExporterConfig {
	version, _ := prw.ParseVersion(c.PRWExporterVersion)

	// Parse extra labels
	extraLabels := make(map[string]string)
	if c.PRWExporterVMExtraLabels != "" {
		pairs := strings.Split(c.PRWExporterVMExtraLabels, ",")
		for _, pair := range pairs {
			kv := strings.SplitN(pair, "=", 2)
			if len(kv) == 2 {
				extraLabels[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
			}
		}
	}

	return exporter.PRWExporterConfig{
		Endpoint:    c.PRWExporterEndpoint,
		DefaultPath: c.PRWExporterDefaultPath,
		Version:     version,
		Timeout:     c.PRWExporterTimeout,
		TLS: tlspkg.ClientConfig{
			Enabled:            c.PRWExporterTLSEnabled,
			CertFile:           c.PRWExporterTLSCertFile,
			KeyFile:            c.PRWExporterTLSKeyFile,
			CAFile:             c.PRWExporterTLSCAFile,
			InsecureSkipVerify: c.PRWExporterTLSSkipVerify,
		},
		Auth: auth.ClientConfig{
			BearerToken: c.PRWExporterAuthBearerToken,
		},
		VMMode: c.PRWExporterVMMode,
		VMOptions: exporter.VMRemoteWriteOptions{
			ExtraLabels:      extraLabels,
			Compression:      c.PRWExporterVMCompression,
			UseShortEndpoint: c.PRWExporterVMShortEndpoint,
		},
		HTTPClient: c.ExporterHTTPClientConfig(),
	}
}

// PRWBufferConfig returns the PRW buffer configuration.
func (c *Config) PRWBufferConfig() prw.BufferConfig {
	return prw.BufferConfig{
		MaxSize:       c.PRWBufferSize,
		MaxBatchSize:  c.PRWBatchSize,
		FlushInterval: c.PRWFlushInterval,
	}
}

// PRWQueueConfig returns the PRW queue configuration.
func (c *Config) PRWQueueConfig() exporter.PRWQueueConfig {
	return exporter.PRWQueueConfig{
		Path:          c.PRWQueuePath,
		MaxSize:       c.PRWQueueMaxSize,
		MaxBytes:      c.PRWQueueMaxBytes,
		RetryInterval: c.PRWQueueRetryInterval,
		MaxRetryDelay: c.PRWQueueMaxRetryDelay,
	}
}

// PrintUsage prints the help message.
func PrintUsage() {
	fmt.Fprintf(os.Stderr, `metrics-governor - OTLP metrics proxy with buffering

USAGE:
    metrics-governor [OPTIONS]

DESCRIPTION:
    Receives OTLP metrics via gRPC and HTTP, buffers them, and forwards
    to a configurable OTLP endpoint with batching support.

OPTIONS:
    Configuration:
        -config <path>                   Path to YAML configuration file
                                         CLI flags override config file values

    Receiver:
        -grpc-listen <addr>              gRPC receiver listen address (default: ":4317")
        -http-listen <addr>              HTTP receiver listen address (default: ":4318")

    Receiver TLS:
        -receiver-tls-enabled            Enable TLS for receivers (default: false)
        -receiver-tls-cert <path>        Path to server certificate file
        -receiver-tls-key <path>         Path to server private key file
        -receiver-tls-ca <path>          Path to CA certificate for client verification (mTLS)
        -receiver-tls-client-auth        Require client certificates (mTLS) (default: false)

    Receiver Authentication:
        -receiver-auth-enabled           Enable authentication for receivers (default: false)
        -receiver-auth-bearer-token      Expected bearer token for authentication
        -receiver-auth-basic-username    Basic auth username
        -receiver-auth-basic-password    Basic auth password

    Exporter:
        -exporter-endpoint <addr>        OTLP exporter endpoint (default: "localhost:4317")
        -exporter-protocol <proto>       Exporter protocol: grpc or http (default: "grpc")
        -exporter-insecure               Use insecure connection (default: true)
        -exporter-timeout <dur>          Exporter request timeout (default: 30s)

    Exporter TLS:
        -exporter-tls-enabled            Enable custom TLS config for exporter (default: false)
        -exporter-tls-cert <path>        Path to client certificate file (mTLS)
        -exporter-tls-key <path>         Path to client private key file (mTLS)
        -exporter-tls-ca <path>          Path to CA certificate for server verification
        -exporter-tls-skip-verify        Skip TLS certificate verification (default: false)
        -exporter-tls-server-name        Override server name for TLS verification

    Exporter Authentication:
        -exporter-auth-bearer-token      Bearer token to send with requests
        -exporter-auth-basic-username    Basic auth username
        -exporter-auth-basic-password    Basic auth password
        -exporter-auth-headers           Custom headers (format: key1=value1,key2=value2)

    Exporter Compression (HTTP only):
        -exporter-compression <type>     Compression type: none, gzip, zstd, snappy, zlib, deflate, lz4 (default: none)
        -exporter-compression-level <n>  Compression level (algorithm-specific, 0 for default)
                                         gzip/zlib/deflate: 1 (fastest) to 9 (best), -1 (default)
                                         zstd: 1 (fastest), 3 (default), 6 (better), 11 (best)
                                         snappy/lz4: no levels supported

    Exporter HTTP Client:
        -exporter-max-idle-conns <n>           Maximum idle connections across all hosts (default: 100)
        -exporter-max-idle-conns-per-host <n>  Maximum idle connections per host (default: 100)
        -exporter-max-conns-per-host <n>       Maximum total connections per host (0 = no limit)
        -exporter-idle-conn-timeout <dur>      Idle connection timeout (default: 90s)
        -exporter-disable-keep-alives          Disable HTTP keep-alives (default: false)
        -exporter-force-http2                  Force HTTP/2 for non-TLS connections (default: false)
        -exporter-http2-read-idle-timeout      HTTP/2 read idle timeout for health checks
        -exporter-http2-ping-timeout           HTTP/2 ping timeout

    Receiver HTTP Server:
        -receiver-max-request-body-size <n>    Maximum request body size in bytes (0 = no limit)
        -receiver-read-timeout <dur>           HTTP server read timeout (default: 0/no timeout)
        -receiver-read-header-timeout <dur>    HTTP server read header timeout (default: 1m)
        -receiver-write-timeout <dur>          HTTP server write timeout (default: 30s)
        -receiver-idle-timeout <dur>           HTTP server idle timeout (default: 1m)
        -receiver-keep-alives-enabled          Enable HTTP keep-alives for receiver (default: true)

    Buffer:
        -buffer-size <n>                 Maximum metrics to buffer (default: 10000)
        -flush-interval <dur>            Buffer flush interval (default: 5s)
        -batch-size <n>                  Maximum batch size for export (default: 1000)

    Stats:
        -stats-addr <addr>               Stats/metrics HTTP endpoint address (default: ":9090")
        -stats-labels <labels>           Comma-separated labels to track (e.g., service,env,cluster)

    Limits:
        -limits-config <path>            Path to limits configuration YAML file
        -limits-dry-run                  Dry run mode: log only, don't drop/sample (default: true)
        -rule-cache-max-size <n>         Maximum entries in the rule matching LRU cache (default: 10000, 0 disables)

    Cardinality Tracking:
        -cardinality-mode <mode>         Tracking mode: bloom (memory-efficient) or exact (100%% accurate) (default: bloom)
        -cardinality-expected-items <n>  Expected unique items per tracker for Bloom sizing (default: 100000)
        -cardinality-fp-rate <rate>      Bloom filter false positive rate (default: 0.01 = 1%%)

    Bloom Persistence:
        -bloom-persistence-enabled                Enable bloom filter state persistence (default: false)
        -bloom-persistence-path <path>            Directory for bloom state files (default: ./bloom-state)
        -bloom-persistence-save-interval <dur>    Interval between periodic saves (default: 30s)
        -bloom-persistence-state-ttl <dur>        Time after which unused trackers are cleaned up (default: 1h)
        -bloom-persistence-cleanup-interval <dur> Interval between cleanup runs (default: 5m)
        -bloom-persistence-max-size <n>           Max disk space for bloom state in bytes (default: 500MB)
        -bloom-persistence-max-memory <n>         Max memory for in-memory bloom filters (default: 256MB)
        -bloom-persistence-compression            Enable gzip compression (default: true)
        -bloom-persistence-compression-level <n>  Gzip compression level 1-9 (default: 1)

    Queue (FastQueue - High-Performance Persistent Retry):
        -queue-enabled                   Enable persistent queue for export retries (default: false)
        -queue-path <path>               Queue storage directory (default: ./queue)
        -queue-max-size <n>              Maximum number of batches in queue (default: 10000)
        -queue-max-bytes <n>             Maximum total queue size in bytes (default: 1GB)
        -queue-retry-interval <dur>      Initial retry interval (default: 5s)
        -queue-max-retry-delay <dur>     Maximum retry backoff delay (default: 5m)
        -queue-full-behavior <behavior>  Queue full behavior: drop_oldest, drop_newest, or block (default: drop_oldest)
        -queue-inmemory-blocks <n>       In-memory channel size (default: 256)
        -queue-chunk-size <n>            Chunk file size in bytes (default: 512MB)
        -queue-meta-sync <dur>           Metadata sync interval / max data loss window (default: 1s)
        -queue-stale-flush <dur>         Interval to flush stale in-memory blocks to disk (default: 5s)

    Queue Resilience (Backoff & Circuit Breaker):
        -queue-backoff-enabled           Enable exponential backoff for retries (default: true)
        -queue-backoff-multiplier <n>    Backoff delay multiplier on each failure (default: 2.0)
        -queue-circuit-breaker-enabled   Enable circuit breaker pattern (default: true)
        -queue-circuit-breaker-threshold <n>      Consecutive failures before opening circuit (default: 10)
        -queue-circuit-breaker-reset-timeout <dur> Time to wait before half-open state (default: 30s)

    Memory:
        -memory-limit-ratio <ratio>      Ratio of container memory for GOMEMLIMIT (0.0-1.0) (default: 0.9)
                                         Auto-detects container limits via cgroups (Docker/K8s)

    Sharding (Consistent Hash Distribution):
        -sharding-enabled                Enable consistent sharding (default: false)
        -sharding-headless-service       K8s headless service DNS name (e.g., vminsert-headless.monitoring.svc:8428)
        -sharding-dns-refresh-interval   DNS refresh interval (default: 30s)
        -sharding-dns-timeout            DNS lookup timeout (default: 5s)
        -sharding-labels                 Comma-separated labels for shard key (e.g., service,env,cluster)
        -sharding-virtual-nodes          Virtual nodes per endpoint (default: 150)
        -sharding-fallback-on-empty      Use static endpoint if no DNS results (default: true)

    General:
        -h, -help                        Show this help message
        -v, -version                     Show version

EXAMPLES:
    # Start with default settings
    metrics-governor

    # Use YAML configuration file
    metrics-governor -config /etc/metrics-governor/config.yaml

    # Use config file with CLI overrides
    metrics-governor -config config.yaml -exporter-endpoint otel:4317

    # Custom receiver ports
    metrics-governor -grpc-listen :5317 -http-listen :5318

    # Forward to remote gRPC endpoint
    metrics-governor -exporter-endpoint otel-collector:4317

    # Forward to remote HTTP endpoint
    metrics-governor -exporter-endpoint otel-collector:4318 -exporter-protocol http

    # Enable TLS for receivers
    metrics-governor -receiver-tls-enabled \
        -receiver-tls-cert /etc/certs/server.crt \
        -receiver-tls-key /etc/certs/server.key

    # Enable mTLS for receivers
    metrics-governor -receiver-tls-enabled \
        -receiver-tls-cert /etc/certs/server.crt \
        -receiver-tls-key /etc/certs/server.key \
        -receiver-tls-ca /etc/certs/ca.crt \
        -receiver-tls-client-auth

    # Enable bearer token authentication for receivers
    metrics-governor -receiver-auth-enabled \
        -receiver-auth-bearer-token "secret-token"

    # Connect to secure exporter with custom CA
    metrics-governor -exporter-insecure=false \
        -exporter-tls-enabled \
        -exporter-tls-ca /etc/certs/ca.crt

    # Connect to exporter with bearer token
    metrics-governor -exporter-auth-bearer-token "secret-token"

    # Adjust buffering
    metrics-governor -buffer-size 50000 -flush-interval 10s -batch-size 2000

    # Enable stats tracking by service and environment
    metrics-governor -stats-labels service,env,cluster

    # Enable limits enforcement with config file
    metrics-governor -limits-config /etc/metrics-governor/limits.yaml -limits-dry-run=false

    # Enable gzip compression for HTTP exporter
    metrics-governor -exporter-protocol http \
        -exporter-endpoint otel-collector:4318 \
        -exporter-compression gzip \
        -exporter-compression-level 6

    # Enable zstd compression with best compression level
    metrics-governor -exporter-protocol http \
        -exporter-compression zstd \
        -exporter-compression-level 11

    # Configure HTTP client connection pool
    metrics-governor -exporter-protocol http \
        -exporter-max-idle-conns 200 \
        -exporter-max-idle-conns-per-host 50 \
        -exporter-idle-conn-timeout 2m

    # Configure HTTP receiver server timeouts
    metrics-governor -receiver-read-timeout 30s \
        -receiver-write-timeout 1m \
        -receiver-max-request-body-size 10485760

    # Enable persistent queue for export retries
    metrics-governor -queue-enabled \
        -queue-path /var/lib/metrics-governor/queue \
        -queue-max-size 10000 \
        -queue-retry-interval 10s

`)
}

// PrintVersion prints the version and exits.
func PrintVersion() {
	fmt.Printf("metrics-governor version %s\n", version)
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		GRPCListenAddr:                  ":4317",
		HTTPListenAddr:                  ":4318",
		HTTPReceiverPath:                "/v1/metrics",
		ExporterEndpoint:                "localhost:4317",
		ExporterProtocol:                "grpc",
		ExporterInsecure:                true,
		ExporterTimeout:                 30 * time.Second,
		ExporterDefaultPath:             "/v1/metrics",
		ExporterCompression:             "none",
		ExporterCompressionLevel:        0,
		ExporterMaxIdleConns:            100,
		ExporterMaxIdleConnsPerHost:     100,
		ExporterMaxConnsPerHost:         0,
		ExporterIdleConnTimeout:         90 * time.Second,
		ExporterDisableKeepAlives:       false,
		ExporterForceHTTP2:              false,
		ReceiverMaxRequestBodySize:      0,
		ReceiverReadTimeout:             0,
		ReceiverReadHeaderTimeout:       1 * time.Minute,
		ReceiverWriteTimeout:            30 * time.Second,
		ReceiverIdleTimeout:             1 * time.Minute,
		ReceiverKeepAlivesEnabled:       true,
		BufferSize:                      10000,
		FlushInterval:                   5 * time.Second,
		MaxBatchSize:                    1000,
		StatsAddr:                       ":9090",
		StatsLabels:                     "",
		LimitsConfig:                    "",
		LimitsDryRun:                    true,
		RuleCacheMaxSize:                10000,
		QueueEnabled:                    false,
		QueuePath:                       "./queue",
		QueueMaxSize:                    10000,
		QueueMaxBytes:                   1073741824, // 1GB
		QueueRetryInterval:              5 * time.Second,
		QueueMaxRetryDelay:              5 * time.Minute,
		QueueFullBehavior:               "drop_oldest",
		QueueTargetUtilization:          0.85,
		QueueAdaptiveEnabled:            true,
		QueueCompactThreshold:           0.5,
		QueueInmemoryBlocks:             256,
		QueueChunkSize:                  536870912, // 512MB
		QueueMetaSyncInterval:           1 * time.Second,
		QueueStaleFlushInterval:         5 * time.Second,
		QueueBackoffEnabled:             true,
		QueueBackoffMultiplier:          2.0,
		QueueCircuitBreakerEnabled:      true,
		QueueCircuitBreakerThreshold:    10,
		QueueCircuitBreakerResetTimeout: 30 * time.Second,
		MemoryLimitRatio:                0.9,
		ShardingEnabled:                 false,
		ShardingDNSRefreshInterval:      30 * time.Second,
		ShardingDNSTimeout:              5 * time.Second,
		ShardingVirtualNodes:            150,
		ShardingFallbackOnEmpty:         true,
		// PRW defaults
		PRWListenAddr:            "", // Disabled by default
		PRWReceiverPath:          "/api/v1/write",
		PRWReceiverVersion:       "auto",
		PRWReceiverReadTimeout:   1 * time.Minute,
		PRWReceiverWriteTimeout:  30 * time.Second,
		PRWExporterEndpoint:      "", // Disabled by default
		PRWExporterDefaultPath:   "/api/v1/write",
		PRWExporterVersion:       "auto",
		PRWExporterTimeout:       30 * time.Second,
		PRWExporterVMCompression: "snappy",
		PRWBufferSize:            10000,
		PRWBatchSize:             1000,
		PRWFlushInterval:         5 * time.Second,
		PRWQueueEnabled:          false,
		PRWQueuePath:             "./prw-queue",
		PRWQueueMaxSize:          10000,
		PRWQueueMaxBytes:         1073741824, // 1GB
		PRWQueueRetryInterval:    5 * time.Second,
		PRWQueueMaxRetryDelay:    5 * time.Minute,
		PRWLimitsEnabled:         false,
		PRWLimitsDryRun:          true,
		// Performance defaults
		ExportConcurrency:    0, // 0 = NumCPU * 4
		StringInterning:      true,
		InternMaxValueLength: 64,
		// Cardinality tracking defaults (Bloom filter)
		CardinalityMode:          "bloom",
		CardinalityExpectedItems: 100000,
		CardinalityFPRate:        0.01,
		// Bloom persistence defaults
		BloomPersistenceEnabled:          false,
		BloomPersistencePath:             "./bloom-state",
		BloomPersistenceSaveInterval:     30 * time.Second,
		BloomPersistenceStateTTL:         time.Hour,
		BloomPersistenceCleanupInterval:  5 * time.Minute,
		BloomPersistenceMaxSize:          524288000, // 500MB
		BloomPersistenceMaxMemory:        268435456, // 256MB
		BloomPersistenceCompression:      true,
		BloomPersistenceCompressionLevel: 1,
	}
}
