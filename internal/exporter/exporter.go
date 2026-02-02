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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/szibis/metrics-governor/internal/auth"
	"github.com/szibis/metrics-governor/internal/compression"
	tlspkg "github.com/szibis/metrics-governor/internal/tls"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// ErrorType represents a category of export error for metrics.
type ErrorType string

const (
	// ErrorTypeNetwork represents network-level errors (DNS, connection refused, etc.)
	ErrorTypeNetwork ErrorType = "network"
	// ErrorTypeTimeout represents timeout errors
	ErrorTypeTimeout ErrorType = "timeout"
	// ErrorTypeServerError represents server-side errors (5xx status codes)
	ErrorTypeServerError ErrorType = "server_error"
	// ErrorTypeClientError represents client-side errors (4xx status codes)
	ErrorTypeClientError ErrorType = "client_error"
	// ErrorTypeAuth represents authentication/authorization errors (401, 403)
	ErrorTypeAuth ErrorType = "auth"
	// ErrorTypeRateLimit represents rate limiting errors (429)
	ErrorTypeRateLimit ErrorType = "rate_limit"
	// ErrorTypeUnknown represents unclassified errors
	ErrorTypeUnknown ErrorType = "unknown"
)

var (
	// otlpExportBytesTotal tracks actual bytes sent to the OTLP backend
	otlpExportBytesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_otlp_export_bytes_total",
		Help: "Total bytes exported to OTLP backend",
	}, []string{"compression"})

	// otlpExportRequestsTotal tracks the number of export requests
	otlpExportRequestsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_otlp_export_requests_total",
		Help: "Total number of OTLP export requests",
	})

	// otlpExportErrorsTotal tracks the number of export errors by type
	otlpExportErrorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_otlp_export_errors_total",
		Help: "Total number of OTLP export errors by error type",
	}, []string{"error_type"})

	// otlpExportDatapointsTotal tracks the number of datapoints exported
	otlpExportDatapointsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_otlp_export_datapoints_total",
		Help: "Total number of datapoints exported to OTLP backend",
	})
)

func init() {
	prometheus.MustRegister(otlpExportBytesTotal)
	prometheus.MustRegister(otlpExportRequestsTotal)
	prometheus.MustRegister(otlpExportErrorsTotal)
	prometheus.MustRegister(otlpExportDatapointsTotal)
}

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
	// DefaultPath is the path to append when endpoint has no path (default: /v1/metrics).
	DefaultPath string
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
		defaultPath := cfg.DefaultPath
		if defaultPath == "" {
			defaultPath = "/v1/metrics"
		}
		endpoint = endpoint + defaultPath
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
		return e.exportGRPC(ctx, req)
	case ProtocolHTTP:
		return e.exportHTTP(ctx, req)
	default:
		return fmt.Errorf("unsupported protocol: %s", e.protocol)
	}
}

// exportGRPC exports metrics via gRPC.
func (e *OTLPExporter) exportGRPC(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	// Estimate size for metrics tracking (gRPC handles compression internally)
	size := proto.Size(req)
	datapoints := countDatapoints(req)

	otlpExportRequestsTotal.Inc()

	_, err := e.grpcClient.Export(ctx, req)
	if err != nil {
		errType := classifyGRPCError(err)
		recordExportError(errType)
		return err
	}

	// Track as uncompressed since gRPC compression is handled at transport level
	otlpExportBytesTotal.WithLabelValues("grpc").Add(float64(size))
	recordExportSuccess(datapoints)

	return nil
}

// classifyGRPCError categorizes a gRPC error into an error type.
func classifyGRPCError(err error) ErrorType {
	if err == nil {
		return ErrorTypeUnknown
	}

	// Check for gRPC status codes
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.DeadlineExceeded:
			return ErrorTypeTimeout
		case codes.Unavailable:
			return ErrorTypeNetwork
		case codes.Unauthenticated:
			return ErrorTypeAuth
		case codes.PermissionDenied:
			return ErrorTypeAuth
		case codes.ResourceExhausted:
			return ErrorTypeRateLimit
		case codes.InvalidArgument, codes.FailedPrecondition, codes.OutOfRange:
			return ErrorTypeClientError
		case codes.Internal, codes.Unknown, codes.DataLoss, codes.Aborted:
			return ErrorTypeServerError
		}
	}

	// Fall back to generic error classification
	return classifyError(err)
}

// exportHTTP exports metrics via HTTP.
func (e *OTLPExporter) exportHTTP(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	body, err := proto.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	// Track uncompressed size and datapoints
	uncompressedSize := len(body)
	datapoints := countDatapoints(req)
	compressionLabel := "none"

	// Apply compression if configured
	if e.compression.Type != compression.TypeNone && e.compression.Type != "" {
		body, err = compression.Compress(body, e.compression)
		if err != nil {
			return fmt.Errorf("failed to compress request: %w", err)
		}
		compressionLabel = string(e.compression.Type)
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

	otlpExportRequestsTotal.Inc()

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		errType := classifyError(err)
		recordExportError(errType)
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read and discard body to allow connection reuse
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		errType := classifyHTTPStatusCode(resp.StatusCode)
		recordExportError(errType)
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Track exported bytes and datapoints
	otlpExportBytesTotal.WithLabelValues(compressionLabel).Add(float64(len(body)))
	if compressionLabel != "none" {
		// Also track uncompressed for comparison
		otlpExportBytesTotal.WithLabelValues("uncompressed").Add(float64(uncompressedSize))
	}
	recordExportSuccess(datapoints)

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

// classifyError categorizes an error into a low-cardinality error type.
func classifyError(err error) ErrorType {
	if err == nil {
		return ErrorTypeUnknown
	}

	errStr := err.Error()

	// Check for timeout errors
	if isTimeoutError(err) {
		return ErrorTypeTimeout
	}

	// Check for network errors
	if isNetworkError(err) {
		return ErrorTypeNetwork
	}

	// Check for common error patterns in error string
	if contains(errStr, "connection refused") ||
		contains(errStr, "no such host") ||
		contains(errStr, "network is unreachable") ||
		contains(errStr, "connection reset") ||
		contains(errStr, "broken pipe") {
		return ErrorTypeNetwork
	}

	if contains(errStr, "timeout") ||
		contains(errStr, "deadline exceeded") {
		return ErrorTypeTimeout
	}

	return ErrorTypeUnknown
}

// classifyHTTPStatusCode categorizes an HTTP status code into an error type.
func classifyHTTPStatusCode(statusCode int) ErrorType {
	switch {
	case statusCode == 401 || statusCode == 403:
		return ErrorTypeAuth
	case statusCode == 429:
		return ErrorTypeRateLimit
	case statusCode >= 400 && statusCode < 500:
		return ErrorTypeClientError
	case statusCode >= 500:
		return ErrorTypeServerError
	default:
		return ErrorTypeUnknown
	}
}

// isTimeoutError checks if the error is a timeout error.
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	// Check for net.Error timeout
	if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
		return true
	}
	// Check for context deadline exceeded
	if err == context.DeadlineExceeded {
		return true
	}
	return false
}

// isNetworkError checks if the error is a network error.
func isNetworkError(err error) bool {
	if err == nil {
		return false
	}
	// Check for net.Error (but not timeout)
	if netErr, ok := err.(net.Error); ok && !netErr.Timeout() {
		return true
	}
	// Check for DNS errors
	if _, ok := err.(*net.DNSError); ok {
		return true
	}
	// Check for OpError
	if _, ok := err.(*net.OpError); ok {
		return true
	}
	return false
}

// contains is a simple case-insensitive substring check.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && containsLower(toLower(s), toLower(substr))))
}

func containsLower(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

// countDatapoints counts the total number of datapoints in a request.
func countDatapoints(req *colmetricspb.ExportMetricsServiceRequest) int64 {
	var count int64
	for _, rm := range req.ResourceMetrics {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				switch data := m.Data.(type) {
				case *metricspb.Metric_Gauge:
					if data.Gauge != nil {
						count += int64(len(data.Gauge.DataPoints))
					}
				case *metricspb.Metric_Sum:
					if data.Sum != nil {
						count += int64(len(data.Sum.DataPoints))
					}
				case *metricspb.Metric_Histogram:
					if data.Histogram != nil {
						count += int64(len(data.Histogram.DataPoints))
					}
				case *metricspb.Metric_ExponentialHistogram:
					if data.ExponentialHistogram != nil {
						count += int64(len(data.ExponentialHistogram.DataPoints))
					}
				case *metricspb.Metric_Summary:
					if data.Summary != nil {
						count += int64(len(data.Summary.DataPoints))
					}
				}
			}
		}
	}
	return count
}

// recordExportError increments the error counter with the appropriate error type.
func recordExportError(errType ErrorType) {
	otlpExportErrorsTotal.WithLabelValues(string(errType)).Inc()
}

// recordExportSuccess tracks successful export metrics.
func recordExportSuccess(datapoints int64) {
	otlpExportDatapointsTotal.Add(float64(datapoints))
}
