package exporter

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/szibis/metrics-governor/internal/auth"
	"github.com/szibis/metrics-governor/internal/compression"
	tlspkg "github.com/szibis/metrics-governor/internal/tls"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

// Protocol represents the export protocol.
type Protocol string

const (
	// ProtocolGRPC uses OTLP gRPC protocol.
	ProtocolGRPC Protocol = "grpc"
	// ProtocolHTTP uses OTLP HTTP protocol.
	ProtocolHTTP Protocol = "http"
)

// HTTPClientConfig holds HTTP client connection pool settings.
type HTTPClientConfig struct {
	// MaxIdleConns controls the maximum number of idle (keep-alive) connections
	// across all hosts. Zero means no limit.
	MaxIdleConns int
	// MaxIdleConnsPerHost controls the maximum idle (keep-alive) connections
	// to keep per-host. If zero, DefaultMaxIdleConnsPerHost is used.
	MaxIdleConnsPerHost int
	// MaxConnsPerHost limits the total number of connections per host.
	// Zero means no limit.
	MaxConnsPerHost int
	// IdleConnTimeout is the maximum amount of time an idle connection will
	// remain idle before closing itself. Zero means no limit.
	IdleConnTimeout time.Duration
	// DisableKeepAlives, if true, disables HTTP keep-alives and will only use
	// the connection to the server for a single HTTP request.
	DisableKeepAlives bool
	// ForceAttemptHTTP2 controls whether HTTP/2 is enabled when a non-zero
	// TLSClientConfig is provided.
	ForceAttemptHTTP2 bool
	// HTTP2ReadIdleTimeout is the timeout after which a health check using ping
	// frame will be carried out if no frame is received on the connection.
	// Note: this is for HTTP/2 connections.
	HTTP2ReadIdleTimeout time.Duration
	// HTTP2PingTimeout is the timeout after which the connection will be closed
	// if a response to Ping is not received.
	HTTP2PingTimeout time.Duration
}

// Config holds the exporter configuration.
type Config struct {
	// Endpoint is the target endpoint (host:port for gRPC, URL for HTTP).
	Endpoint string
	// Protocol is the export protocol (grpc or http).
	Protocol Protocol
	// Insecure uses insecure connection (no TLS).
	Insecure bool
	// Timeout is the request timeout.
	Timeout time.Duration
	// TLS configuration for secure connections.
	TLS tlspkg.ClientConfig
	// Auth configuration for authentication.
	Auth auth.ClientConfig
	// Compression configuration for HTTP exporter.
	Compression compression.Config
	// HTTPClient configuration for HTTP connection pooling.
	HTTPClient HTTPClientConfig
}

// Exporter defines the interface for metric exporters.
type Exporter interface {
	Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error
	Close() error
}

// OTLPExporter exports metrics via OTLP (gRPC or HTTP).
type OTLPExporter struct {
	protocol    Protocol
	timeout     time.Duration
	compression compression.Config

	// gRPC client
	grpcConn   *grpc.ClientConn
	grpcClient colmetricspb.MetricsServiceClient

	// HTTP client
	httpClient   *http.Client
	httpEndpoint string
}

// New creates a new OTLPExporter based on the configuration.
func New(ctx context.Context, cfg Config) (*OTLPExporter, error) {
	// Default to gRPC if not specified
	if cfg.Protocol == "" {
		cfg.Protocol = ProtocolGRPC
	}

	switch cfg.Protocol {
	case ProtocolGRPC:
		return newGRPCExporter(ctx, cfg)
	case ProtocolHTTP:
		return newHTTPExporter(ctx, cfg)
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", cfg.Protocol)
	}
}

// newGRPCExporter creates a gRPC-based exporter.
func newGRPCExporter(_ context.Context, cfg Config) (*OTLPExporter, error) {
	var opts []grpc.DialOption

	// Configure TLS or insecure connection
	if cfg.Insecure {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else if cfg.TLS.Enabled {
		tlsConfig, err := tlspkg.NewClientTLSConfig(cfg.TLS)
		if err != nil {
			return nil, fmt.Errorf("failed to create TLS config: %w", err)
		}
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	} else {
		// Default to system TLS when not insecure and no custom TLS config
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			MinVersion: tls.VersionTLS12,
		})))
	}

	// Configure authentication
	if cfg.Auth.BearerToken != "" || cfg.Auth.BasicAuthUsername != "" || len(cfg.Auth.Headers) > 0 {
		opts = append(opts, grpc.WithUnaryInterceptor(auth.GRPCClientInterceptor(cfg.Auth)))
	}

	conn, err := grpc.NewClient(cfg.Endpoint, opts...)
	if err != nil {
		return nil, err
	}

	client := colmetricspb.NewMetricsServiceClient(conn)

	return &OTLPExporter{
		protocol:   ProtocolGRPC,
		timeout:    cfg.Timeout,
		grpcConn:   conn,
		grpcClient: client,
	}, nil
}

// newHTTPExporter creates an HTTP-based exporter.
func newHTTPExporter(_ context.Context, cfg Config) (*OTLPExporter, error) {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     cfg.HTTPClient.ForceAttemptHTTP2,
		MaxIdleConns:          cfg.HTTPClient.MaxIdleConns,
		MaxIdleConnsPerHost:   cfg.HTTPClient.MaxIdleConnsPerHost,
		MaxConnsPerHost:       cfg.HTTPClient.MaxConnsPerHost,
		IdleConnTimeout:       cfg.HTTPClient.IdleConnTimeout,
		DisableKeepAlives:     cfg.HTTPClient.DisableKeepAlives,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	// Apply default values if not set
	if transport.MaxIdleConns == 0 {
		transport.MaxIdleConns = 100
	}
	if transport.MaxIdleConnsPerHost == 0 {
		transport.MaxIdleConnsPerHost = 100
	}
	if transport.IdleConnTimeout == 0 {
		transport.IdleConnTimeout = 90 * time.Second
	}

	// Configure TLS
	if !cfg.Insecure {
		if cfg.TLS.Enabled {
			tlsConfig, err := tlspkg.NewClientTLSConfig(cfg.TLS)
			if err != nil {
				return nil, fmt.Errorf("failed to create TLS config: %w", err)
			}
			transport.TLSClientConfig = tlsConfig
		} else {
			// Default TLS config
			transport.TLSClientConfig = &tls.Config{
				MinVersion: tls.VersionTLS12,
			}
		}
	}

	var roundTripper http.RoundTripper = transport

	// Configure HTTP/2 settings if enabled
	if cfg.HTTPClient.ForceAttemptHTTP2 || (!cfg.Insecure && transport.TLSClientConfig != nil) {
		http2Transport, err := http2.ConfigureTransports(transport)
		if err == nil && http2Transport != nil {
			if cfg.HTTPClient.HTTP2ReadIdleTimeout > 0 {
				http2Transport.ReadIdleTimeout = cfg.HTTPClient.HTTP2ReadIdleTimeout
			}
			if cfg.HTTPClient.HTTP2PingTimeout > 0 {
				http2Transport.PingTimeout = cfg.HTTPClient.HTTP2PingTimeout
			}
		}
	}

	// Configure authentication
	if cfg.Auth.BearerToken != "" || cfg.Auth.BasicAuthUsername != "" || len(cfg.Auth.Headers) > 0 {
		roundTripper = auth.HTTPTransport(cfg.Auth, roundTripper)
	}

	client := &http.Client{
		Transport: roundTripper,
		Timeout:   cfg.Timeout,
	}

	// Build endpoint URL
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = "localhost:4318"
	}

	// Add scheme if missing
	scheme := "http"
	if !cfg.Insecure {
		scheme = "https"
	}
	if !hasScheme(endpoint) {
		endpoint = fmt.Sprintf("%s://%s", scheme, endpoint)
	}

	// Add path if missing
	if !hasPath(endpoint) {
		endpoint = endpoint + "/v1/metrics"
	}

	return &OTLPExporter{
		protocol:     ProtocolHTTP,
		timeout:      cfg.Timeout,
		compression:  cfg.Compression,
		httpClient:   client,
		httpEndpoint: endpoint,
	}, nil
}

// Export sends metrics to the configured endpoint.
func (e *OTLPExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	switch e.protocol {
	case ProtocolGRPC:
		_, err := e.grpcClient.Export(ctx, req)
		return err
	case ProtocolHTTP:
		return e.exportHTTP(ctx, req)
	default:
		return fmt.Errorf("unsupported protocol: %s", e.protocol)
	}
}

// exportHTTP exports metrics via HTTP.
func (e *OTLPExporter) exportHTTP(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	body, err := proto.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Apply compression if configured
	if e.compression.Type != compression.TypeNone && e.compression.Type != "" {
		body, err = compression.Compress(body, e.compression)
		if err != nil {
			return fmt.Errorf("failed to compress request: %w", err)
		}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.httpEndpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/x-protobuf")

	// Set Content-Encoding header if compression is used
	if encoding := e.compression.Type.ContentEncoding(); encoding != "" {
		httpReq.Header.Set("Content-Encoding", encoding)
	}

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read and discard body to allow connection reuse
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

// Close closes the exporter connection.
func (e *OTLPExporter) Close() error {
	switch e.protocol {
	case ProtocolGRPC:
		if e.grpcConn != nil {
			return e.grpcConn.Close()
		}
	case ProtocolHTTP:
		if e.httpClient != nil {
			e.httpClient.CloseIdleConnections()
		}
	}
	return nil
}

// hasScheme checks if a URL has a scheme.
func hasScheme(url string) bool {
	return len(url) >= 7 && (url[:7] == "http://" || (len(url) >= 8 && url[:8] == "https://"))
}

// hasPath checks if a URL has a path component.
func hasPath(url string) bool {
	// Find the host portion
	start := 0
	if hasScheme(url) {
		if len(url) >= 8 && url[:8] == "https://" {
			start = 8
		} else {
			start = 7
		}
	}
	// Check if there's a / after the host
	for i := start; i < len(url); i++ {
		if url[i] == '/' {
			return true
		}
	}
	return false
}
