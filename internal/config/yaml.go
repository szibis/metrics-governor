package config

import (
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// YAMLConfig represents the YAML configuration file structure.
type YAMLConfig struct {
	Receiver ReceiverYAMLConfig `yaml:"receiver"`
	Exporter ExporterYAMLConfig `yaml:"exporter"`
	Buffer   BufferYAMLConfig   `yaml:"buffer"`
	Stats    StatsYAMLConfig    `yaml:"stats"`
	Limits   LimitsYAMLConfig   `yaml:"limits"`
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
	Address string                  `yaml:"address"`
	Server  HTTPServerYAMLConfig    `yaml:"server"`
}

// HTTPServerYAMLConfig holds HTTP server timeout settings.
type HTTPServerYAMLConfig struct {
	MaxRequestBodySize int64    `yaml:"max_request_body_size"`
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
	Endpoint    string                  `yaml:"endpoint"`
	Protocol    string                  `yaml:"protocol"`
	Insecure    *bool                   `yaml:"insecure"`
	Timeout     Duration                `yaml:"timeout"`
	TLS         TLSClientYAMLConfig     `yaml:"tls"`
	Auth        AuthClientYAMLConfig    `yaml:"auth"`
	Compression CompressionYAMLConfig   `yaml:"compression"`
	HTTPClient  HTTPClientYAMLConfig    `yaml:"http_client"`
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
	DisableKeepAlives    bool     `yaml:"disable_keep_alives"`
	ForceHTTP2           bool     `yaml:"force_http2"`
	HTTP2ReadIdleTimeout Duration `yaml:"http2_read_idle_timeout"`
	HTTP2PingTimeout     Duration `yaml:"http2_ping_timeout"`
}

// BufferYAMLConfig holds buffer configuration.
type BufferYAMLConfig struct {
	Size          int      `yaml:"size"`
	BatchSize     int      `yaml:"batch_size"`
	FlushInterval Duration `yaml:"flush_interval"`
}

// StatsYAMLConfig holds stats configuration.
type StatsYAMLConfig struct {
	Address string   `yaml:"address"`
	Labels  []string `yaml:"labels"`
}

// LimitsYAMLConfig holds limits configuration.
type LimitsYAMLConfig struct {
	DryRun *bool `yaml:"dry_run"`
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
		y.Buffer.BatchSize = 1000
	}
	if y.Buffer.FlushInterval == 0 {
		y.Buffer.FlushInterval = Duration(5 * time.Second)
	}

	// Stats defaults
	if y.Stats.Address == "" {
		y.Stats.Address = ":9090"
	}

	// Limits defaults
	if y.Limits.DryRun == nil {
		dryRun := true
		y.Limits.DryRun = &dryRun
	}
}

// ToConfig converts YAMLConfig to the flat Config struct.
func (y *YAMLConfig) ToConfig() *Config {
	cfg := &Config{
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
		ReceiverMaxRequestBodySize: y.Receiver.HTTP.Server.MaxRequestBodySize,
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
		ExporterDisableKeepAlives:    y.Exporter.HTTPClient.DisableKeepAlives,
		ExporterForceHTTP2:           y.Exporter.HTTPClient.ForceHTTP2,
		ExporterHTTP2ReadIdleTimeout: time.Duration(y.Exporter.HTTPClient.HTTP2ReadIdleTimeout),
		ExporterHTTP2PingTimeout:     time.Duration(y.Exporter.HTTPClient.HTTP2PingTimeout),

		// Buffer
		BufferSize:    y.Buffer.Size,
		FlushInterval: time.Duration(y.Buffer.FlushInterval),
		MaxBatchSize:  y.Buffer.BatchSize,

		// Stats
		StatsAddr:   y.Stats.Address,
		StatsLabels: strings.Join(y.Stats.Labels, ","),

		// Limits
		LimitsDryRun: *y.Limits.DryRun,
	}

	return cfg
}

// headersMapToString converts a headers map to the comma-separated format.
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
