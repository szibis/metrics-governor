package exporter

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/slawomirskowron/metrics-governor/internal/auth"
	tlspkg "github.com/slawomirskowron/metrics-governor/internal/tls"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
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
}

// Exporter defines the interface for metric exporters.
type Exporter interface {
	Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error
	Close() error
}

// OTLPExporter exports metrics via OTLP (gRPC or HTTP).
type OTLPExporter struct {
	protocol Protocol
	timeout  time.Duration

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
func newGRPCExporter(ctx context.Context, cfg Config) (*OTLPExporter, error) {
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
func newHTTPExporter(ctx context.Context, cfg Config) (*OTLPExporter, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()

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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.httpEndpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/x-protobuf")

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read and discard body to allow connection reuse
	io.Copy(io.Discard, resp.Body)

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
