package exporter

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/szibis/metrics-governor/internal/auth"
	"github.com/szibis/metrics-governor/internal/compression"
	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	"github.com/szibis/metrics-governor/internal/pipeline"
	tlspkg "github.com/szibis/metrics-governor/internal/tls"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// marshalBufPool pools byte slices for MarshalToVT to avoid per-export allocation.
var marshalBufPool = sync.Pool{New: func() any {
	b := make([]byte, 0, 64*1024)
	return &b
}}

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

	// Initialize all counters with 0 so they appear in /metrics immediately
	otlpExportRequestsTotal.Add(0)
	otlpExportDatapointsTotal.Add(0)
	// Initialize counter vectors with all known label values
	otlpExportBytesTotal.WithLabelValues("none").Add(0)
	otlpExportBytesTotal.WithLabelValues("gzip").Add(0)
	otlpExportBytesTotal.WithLabelValues("zstd").Add(0)
	otlpExportBytesTotal.WithLabelValues("snappy").Add(0)
	// Initialize error types
	otlpExportErrorsTotal.WithLabelValues(string(ErrorTypeNetwork)).Add(0)
	otlpExportErrorsTotal.WithLabelValues(string(ErrorTypeTimeout)).Add(0)
	otlpExportErrorsTotal.WithLabelValues(string(ErrorTypeServerError)).Add(0)
	otlpExportErrorsTotal.WithLabelValues(string(ErrorTypeClientError)).Add(0)
	otlpExportErrorsTotal.WithLabelValues(string(ErrorTypeAuth)).Add(0)
	otlpExportErrorsTotal.WithLabelValues(string(ErrorTypeRateLimit)).Add(0)
	otlpExportErrorsTotal.WithLabelValues(string(ErrorTypeUnknown)).Add(0)
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
	// DialTimeout is the TCP connection setup timeout (default: 30s).
	DialTimeout time.Duration
	// KeepAliveInterval controls how often TCP keep-alive probes are sent on
	// idle connections. Zero means 30s (Go default).
	KeepAliveInterval time.Duration
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
	// PrewarmConnections sends HEAD requests at startup to establish HTTP connections.
	PrewarmConnections bool
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
	dialTimeout := cfg.HTTPClient.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = 30 * time.Second
	}

	keepAliveInterval := cfg.HTTPClient.KeepAliveInterval
	if keepAliveInterval <= 0 {
		keepAliveInterval = 30 * time.Second
	}

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: keepAliveInterval,
		}).DialContext,
		ForceAttemptHTTP2:     cfg.HTTPClient.ForceAttemptHTTP2,
		MaxIdleConns:          cfg.HTTPClient.MaxIdleConns,
		MaxIdleConnsPerHost:   cfg.HTTPClient.MaxIdleConnsPerHost,
		MaxConnsPerHost:       cfg.HTTPClient.MaxConnsPerHost,
		IdleConnTimeout:       cfg.HTTPClient.IdleConnTimeout,
		DisableKeepAlives:     cfg.HTTPClient.DisableKeepAlives,
		DisableCompression:    true,      // Disable Go's automatic Accept-Encoding/response decompression; our manual gzip/zstd/snappy compression on the request body is unaffected
		WriteBufferSize:       64 * 1024, // Reduce write syscalls for large payloads
		ReadBufferSize:        4 * 1024,  // Responses are small (204 No Content)
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 0, // Disable Expect: 100-Continue for same-datacenter traffic
	}

	// Apply default values if not set
	if transport.MaxIdleConns == 0 {
		transport.MaxIdleConns = 100
	}
	if transport.MaxIdleConnsPerHost == 0 {
		transport.MaxIdleConnsPerHost = 100
	}
	if transport.MaxConnsPerHost == 0 {
		transport.MaxConnsPerHost = 100
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

	exp := &OTLPExporter{
		protocol:     ProtocolHTTP,
		timeout:      cfg.Timeout,
		compression:  cfg.Compression,
		httpClient:   client,
		httpEndpoint: endpoint,
	}

	// Fire-and-forget connection warmup
	if cfg.PrewarmConnections {
		go WarmupConnections(context.Background(), []string{endpoint}, client, 5*time.Second)
	}

	return exp, nil
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
	size := req.SizeVT()
	datapoints := countDatapoints(req)

	otlpExportRequestsTotal.Inc()

	_, err := e.grpcClient.Export(ctx, req)
	if err != nil {
		errType := classifyGRPCError(err)
		recordExportError(errType)
		statusCode := 0
		message := err.Error()
		if st, ok := status.FromError(err); ok {
			statusCode = int(st.Code())
			message = st.Message()
		}
		return &ExportError{
			Err:        err,
			Type:       errType,
			StatusCode: statusCode,
			Message:    message,
		}
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
	// Marshal into pooled buffer using vtprotobuf's reflection-free MarshalToVT.
	bp := marshalBufPool.Get().(*[]byte)
	marshalStart := time.Now()
	size := req.SizeVT()
	if cap(*bp) < size {
		*bp = make([]byte, size)
	} else {
		*bp = (*bp)[:size]
	}
	n, err := req.MarshalToVT(*bp)
	pipeline.Record("serialize", pipeline.Since(marshalStart))
	if err != nil {
		*bp = (*bp)[:0]
		marshalBufPool.Put(bp)
		return fmt.Errorf("failed to marshal request: %w", err)
	}
	marshaledBody := (*bp)[:n]
	pipeline.RecordBytes("serialize", len(marshaledBody))

	// Track uncompressed size and datapoints
	uncompressedSize := len(marshaledBody)
	datapoints := countDatapoints(req)
	compressionLabel := "none"

	// sendBody holds the final bytes to send. It may point to the marshal buffer
	// (no compression) or to a compression output buffer.
	sendBody := marshaledBody
	var compBuf *bytes.Buffer // non-nil when compression is used

	// Apply compression if configured
	if e.compression.Type != compression.TypeNone && e.compression.Type != "" {
		compBuf = compression.GetBuffer()
		compressStart := time.Now()
		if err := compression.CompressToBuf(compBuf, marshaledBody, e.compression); err != nil {
			compression.ReleaseBuffer(compBuf)
			*bp = marshaledBody[:0]
			marshalBufPool.Put(bp)
			return fmt.Errorf("failed to compress request: %w", err)
		}
		pipeline.Record("compress", pipeline.Since(compressStart))
		pipeline.RecordBytes("compress", compBuf.Len())
		compressionLabel = string(e.compression.Type)

		// Marshal buffer consumed by compression — return it now.
		*bp = marshaledBody[:0]
		marshalBufPool.Put(bp)
		bp = nil

		sendBody = compBuf.Bytes()
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.httpEndpoint, bytes.NewReader(sendBody))
	if err != nil {
		if compBuf != nil {
			compression.ReleaseBuffer(compBuf)
		}
		if bp != nil {
			*bp = marshaledBody[:0]
			marshalBufPool.Put(bp)
		}
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/x-protobuf")

	// Set Content-Encoding header if compression is used
	if encoding := e.compression.Type.ContentEncoding(); encoding != "" {
		httpReq.Header.Set("Content-Encoding", encoding)
	}

	otlpExportRequestsTotal.Inc()

	httpStart := time.Now()
	resp, err := e.httpClient.Do(httpReq)
	pipeline.Record("export_http", pipeline.Since(httpStart))

	// Release buffers after HTTP send completes — bytes.NewReader has consumed them.
	if compBuf != nil {
		compression.ReleaseBuffer(compBuf)
	}
	if bp != nil {
		*bp = marshaledBody[:0]
		marshalBufPool.Put(bp)
	}

	if err != nil {
		errType := classifyError(err)
		recordExportError(errType)
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read body (up to 4KB) for error message before discarding
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_, _ = io.Copy(io.Discard, resp.Body)
		errType := classifyHTTPStatusCode(resp.StatusCode)
		recordExportError(errType)
		return &ExportError{
			Err:        fmt.Errorf("unexpected status code: %d: %s", resp.StatusCode, string(bodyBytes)),
			Type:       errType,
			StatusCode: resp.StatusCode,
			Message:    string(bodyBytes),
		}
	}

	// Read and discard body to allow connection reuse
	_, _ = io.Copy(io.Discard, resp.Body)

	// Track exported bytes and datapoints
	otlpExportBytesTotal.WithLabelValues(compressionLabel).Add(float64(len(sendBody)))
	if compressionLabel != "none" {
		// Also track uncompressed for comparison
		otlpExportBytesTotal.WithLabelValues("uncompressed").Add(float64(uncompressedSize))
	}
	recordExportSuccess(datapoints)

	return nil
}

// ExportData exports pre-serialized proto bytes, skipping the unmarshal→remarshal
// roundtrip that happens when data flows through the disk queue. This is the fast
// path for always-queue workers: raw bytes → compress → HTTP send.
// For gRPC protocol, this falls back to unmarshal + standard export.
func (e *OTLPExporter) ExportData(ctx context.Context, data []byte) error {
	if e.protocol == ProtocolGRPC {
		// gRPC requires the Go struct; fall back to unmarshal + export.
		// vtprotobuf pool: ResetVT preserves slice capacities for near-zero allocs after warmup.
		req := colmetricspb.ExportMetricsServiceRequestFromVTPool()
		if err := req.UnmarshalVT(data); err != nil {
			req.ReturnToVTPool()
			return fmt.Errorf("failed to unmarshal for gRPC export: %w", err)
		}
		err := e.exportGRPC(ctx, req)
		req.ReturnToVTPool()
		return err
	}
	return e.exportHTTPData(ctx, data)
}

// exportHTTPData sends pre-serialized proto bytes via HTTP, bypassing marshal.
// This eliminates the double-serialization: queue stores proto bytes, and we send
// those bytes directly after compression instead of unmarshal→remarshal.
func (e *OTLPExporter) exportHTTPData(ctx context.Context, data []byte) error {
	sendBody := data
	uncompressedSize := len(data)
	compressionLabel := "none"
	var compBuf *bytes.Buffer

	// Apply compression if configured — use pooled buffer to avoid copy.
	if e.compression.Type != compression.TypeNone && e.compression.Type != "" {
		compBuf = compression.GetBuffer()
		compressStart := time.Now()
		if err := compression.CompressToBuf(compBuf, data, e.compression); err != nil {
			compression.ReleaseBuffer(compBuf)
			return fmt.Errorf("failed to compress request: %w", err)
		}
		pipeline.Record("compress", pipeline.Since(compressStart))
		pipeline.RecordBytes("compress", compBuf.Len())
		compressionLabel = string(e.compression.Type)
		sendBody = compBuf.Bytes()
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.httpEndpoint, bytes.NewReader(sendBody))
	if err != nil {
		if compBuf != nil {
			compression.ReleaseBuffer(compBuf)
		}
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/x-protobuf")

	// Set Content-Encoding header if compression is used
	if encoding := e.compression.Type.ContentEncoding(); encoding != "" {
		httpReq.Header.Set("Content-Encoding", encoding)
	}

	otlpExportRequestsTotal.Inc()

	httpStart := time.Now()
	resp, err := e.httpClient.Do(httpReq)
	pipeline.Record("export_http", pipeline.Since(httpStart))

	// Release compression buffer after HTTP send.
	if compBuf != nil {
		compression.ReleaseBuffer(compBuf)
	}

	if err != nil {
		errType := classifyError(err)
		recordExportError(errType)
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read body (up to 4KB) for error message before discarding
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_, _ = io.Copy(io.Discard, resp.Body)
		errType := classifyHTTPStatusCode(resp.StatusCode)
		recordExportError(errType)
		return &ExportError{
			Err:        fmt.Errorf("unexpected status code: %d: %s", resp.StatusCode, string(bodyBytes)),
			Type:       errType,
			StatusCode: resp.StatusCode,
			Message:    string(bodyBytes),
		}
	}

	// Read and discard body to allow connection reuse
	_, _ = io.Copy(io.Discard, resp.Body)

	// Track exported bytes
	otlpExportBytesTotal.WithLabelValues(compressionLabel).Add(float64(len(sendBody)))
	if compressionLabel != "none" {
		otlpExportBytesTotal.WithLabelValues("uncompressed").Add(float64(uncompressedSize))
	}
	// Note: datapoint counting is handled at push time in always-queue mode

	return nil
}

// CompressData compresses raw proto bytes using the exporter's compression config.
// Returns the compressed data, content encoding string, and any error.
// Note: the returned slice is owned by the caller (safe copy from pooled buffer).
func (e *OTLPExporter) CompressData(data []byte) ([]byte, string, error) {
	if e.compression.Type == compression.TypeNone || e.compression.Type == "" {
		return data, "", nil
	}

	compressStart := time.Now()
	compressed, err := compression.Compress(data, e.compression)
	pipeline.Record("compress", pipeline.Since(compressStart))
	if err != nil {
		return nil, "", fmt.Errorf("failed to compress: %w", err)
	}
	pipeline.RecordBytes("compress", len(compressed))
	return compressed, string(e.compression.Type), nil
}

// SendCompressed sends pre-compressed data via HTTP, skipping the marshal and compress steps.
func (e *OTLPExporter) SendCompressed(ctx context.Context, compressedData []byte, contentEncoding string, uncompressedSize int) error {
	if e.protocol == ProtocolGRPC {
		return fmt.Errorf("SendCompressed not supported for gRPC protocol")
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.httpEndpoint, bytes.NewReader(compressedData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	if contentEncoding != "" {
		httpReq.Header.Set("Content-Encoding", contentEncoding)
	}

	otlpExportRequestsTotal.Inc()

	sendStart := time.Now()
	resp, err := e.httpClient.Do(httpReq)
	pipeline.Record("send", pipeline.Since(sendStart))
	if err != nil {
		errType := classifyError(err)
		recordExportError(errType)
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_, _ = io.Copy(io.Discard, resp.Body)
		errType := classifyHTTPStatusCode(resp.StatusCode)
		recordExportError(errType)
		return &ExportError{
			Err:        fmt.Errorf("unexpected status code: %d: %s", resp.StatusCode, string(bodyBytes)),
			Type:       errType,
			StatusCode: resp.StatusCode,
			Message:    string(bodyBytes),
		}
	}

	_, _ = io.Copy(io.Discard, resp.Body)

	compressionLabel := "none"
	if contentEncoding != "" {
		compressionLabel = contentEncoding
	}
	otlpExportBytesTotal.WithLabelValues(compressionLabel).Add(float64(len(compressedData)))
	if compressionLabel != "none" {
		otlpExportBytesTotal.WithLabelValues("uncompressed").Add(float64(uncompressedSize))
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
