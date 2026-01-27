package config

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/slawomirskowron/metrics-governor/internal/auth"
	"github.com/slawomirskowron/metrics-governor/internal/compression"
	"github.com/slawomirskowron/metrics-governor/internal/exporter"
	"github.com/slawomirskowron/metrics-governor/internal/receiver"
	tlspkg "github.com/slawomirskowron/metrics-governor/internal/tls"
)

// version is set at build time via ldflags
var version = "dev"

// Config holds the application configuration.
type Config struct {
	// Receiver settings
	GRPCListenAddr string
	HTTPListenAddr string

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
	ExporterEndpoint string
	ExporterProtocol string
	ExporterInsecure bool
	ExporterTimeout  time.Duration

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
	ExporterMaxIdleConns        int
	ExporterMaxIdleConnsPerHost int
	ExporterMaxConnsPerHost     int
	ExporterIdleConnTimeout     time.Duration
	ExporterDisableKeepAlives   bool
	ExporterForceHTTP2          bool
	ExporterHTTP2ReadIdleTimeout time.Duration
	ExporterHTTP2PingTimeout    time.Duration

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
	LimitsConfig string
	LimitsDryRun bool

	// Flags
	ShowHelp    bool
	ShowVersion bool
}

// ParseFlags parses command line flags and returns the configuration.
func ParseFlags() *Config {
	cfg := &Config{}

	// Receiver flags
	flag.StringVar(&cfg.GRPCListenAddr, "grpc-listen", ":4317", "gRPC receiver listen address")
	flag.StringVar(&cfg.HTTPListenAddr, "http-listen", ":4318", "HTTP receiver listen address")

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

	// Exporter flags
	flag.StringVar(&cfg.ExporterEndpoint, "exporter-endpoint", "localhost:4317", "OTLP exporter endpoint")
	flag.StringVar(&cfg.ExporterProtocol, "exporter-protocol", "grpc", "Exporter protocol: grpc or http")
	flag.BoolVar(&cfg.ExporterInsecure, "exporter-insecure", true, "Use insecure connection for exporter")
	flag.DurationVar(&cfg.ExporterTimeout, "exporter-timeout", 30*time.Second, "Exporter request timeout")

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

	// Exporter Compression flags
	flag.StringVar(&cfg.ExporterCompression, "exporter-compression", "none", "Compression type for HTTP exporter: none, gzip, zstd, snappy, zlib, deflate, lz4")
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

	// Help and version
	flag.BoolVar(&cfg.ShowHelp, "help", false, "Show help message")
	flag.BoolVar(&cfg.ShowHelp, "h", false, "Show help message (shorthand)")
	flag.BoolVar(&cfg.ShowVersion, "version", false, "Show version")
	flag.BoolVar(&cfg.ShowVersion, "v", false, "Show version (shorthand)")

	flag.Usage = PrintUsage

	flag.Parse()

	return cfg
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
		TLS:         c.ExporterTLSConfig(),
		Auth:        c.ExporterAuthConfig(),
		Compression: c.ExporterCompressionConfig(),
		HTTPClient:  c.ExporterHTTPClientConfig(),
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

    General:
        -h, -help                        Show this help message
        -v, -version                     Show version

EXAMPLES:
    # Start with default settings
    metrics-governor

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

`)
}

// PrintVersion prints the version and exits.
func PrintVersion() {
	fmt.Printf("metrics-governor version %s\n", version)
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		GRPCListenAddr:              ":4317",
		HTTPListenAddr:              ":4318",
		ExporterEndpoint:            "localhost:4317",
		ExporterProtocol:            "grpc",
		ExporterInsecure:            true,
		ExporterTimeout:             30 * time.Second,
		ExporterCompression:         "none",
		ExporterCompressionLevel:    0,
		ExporterMaxIdleConns:        100,
		ExporterMaxIdleConnsPerHost: 100,
		ExporterMaxConnsPerHost:     0,
		ExporterIdleConnTimeout:     90 * time.Second,
		ExporterDisableKeepAlives:   false,
		ExporterForceHTTP2:          false,
		ReceiverMaxRequestBodySize:  0,
		ReceiverReadTimeout:         0,
		ReceiverReadHeaderTimeout:   1 * time.Minute,
		ReceiverWriteTimeout:        30 * time.Second,
		ReceiverIdleTimeout:         1 * time.Minute,
		ReceiverKeepAlivesEnabled:   true,
		BufferSize:                  10000,
		FlushInterval:               5 * time.Second,
		MaxBatchSize:                1000,
		StatsAddr:                   ":9090",
		StatsLabels:                 "",
		LimitsConfig:                "",
		LimitsDryRun:                true,
	}
}
