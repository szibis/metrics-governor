package exporter

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/szibis/metrics-governor/internal/auth"
	"github.com/szibis/metrics-governor/internal/compression"
	"github.com/szibis/metrics-governor/internal/prw"
	tlspkg "github.com/szibis/metrics-governor/internal/tls"
	"golang.org/x/net/http2"
)

var (
	// prwExportRequestsTotal tracks the number of PRW export requests
	prwExportRequestsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_prw_export_requests_total",
		Help: "Total number of PRW export requests",
	})

	// prwExportErrorsTotal tracks the number of PRW export errors by type
	prwExportErrorsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_prw_export_errors_total",
		Help: "Total number of PRW export errors by error type",
	}, []string{"error_type"})

	// prwExportTimeseriesTotal tracks the number of timeseries exported
	prwExportTimeseriesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_prw_export_timeseries_total",
		Help: "Total number of timeseries exported via PRW",
	})

	// prwExportSamplesTotal tracks the number of samples/datapoints exported
	prwExportSamplesTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_prw_export_samples_total",
		Help: "Total number of samples/datapoints exported via PRW",
	})

	// prwExportBytesTotal tracks bytes sent to PRW backend
	prwExportBytesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_prw_export_bytes_total",
		Help: "Total bytes exported to PRW backend",
	}, []string{"compression"})

	// prwRetryFailureTotal tracks PRW queue retry failures by error type
	prwRetryFailureTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_prw_retry_failure_total",
		Help: "Total number of PRW retry failures by error type",
	}, []string{"error_type"})

	// prwRetryTotal tracks total PRW retry attempts
	prwRetryTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_prw_retry_total",
		Help: "Total number of PRW retry attempts",
	})

	// prwRetrySuccessTotal tracks successful PRW retries
	prwRetrySuccessTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_prw_retry_success_total",
		Help: "Total number of successful PRW retries",
	})
)

func init() {
	prometheus.MustRegister(prwExportRequestsTotal)
	prometheus.MustRegister(prwExportErrorsTotal)
	prometheus.MustRegister(prwExportTimeseriesTotal)
	prometheus.MustRegister(prwExportSamplesTotal)
	prometheus.MustRegister(prwExportBytesTotal)
	prometheus.MustRegister(prwRetryFailureTotal)
	prometheus.MustRegister(prwRetryTotal)
	prometheus.MustRegister(prwRetrySuccessTotal)

	// Initialize all counters with 0 so they appear in /metrics immediately
	prwExportRequestsTotal.Add(0)
	prwExportTimeseriesTotal.Add(0)
	prwExportSamplesTotal.Add(0)
	prwRetryTotal.Add(0)
	prwRetrySuccessTotal.Add(0)
	// Initialize counter vectors with all known label values
	prwExportBytesTotal.WithLabelValues("none").Add(0)
	prwExportBytesTotal.WithLabelValues("snappy").Add(0)
	prwExportBytesTotal.WithLabelValues("zstd").Add(0)
	// Initialize error types
	prwExportErrorsTotal.WithLabelValues(string(ErrorTypeNetwork)).Add(0)
	prwExportErrorsTotal.WithLabelValues(string(ErrorTypeTimeout)).Add(0)
	prwExportErrorsTotal.WithLabelValues(string(ErrorTypeServerError)).Add(0)
	prwExportErrorsTotal.WithLabelValues(string(ErrorTypeClientError)).Add(0)
	prwExportErrorsTotal.WithLabelValues(string(ErrorTypeAuth)).Add(0)
	prwExportErrorsTotal.WithLabelValues(string(ErrorTypeRateLimit)).Add(0)
	prwExportErrorsTotal.WithLabelValues(string(ErrorTypeUnknown)).Add(0)
	// Initialize retry failure error types
	prwRetryFailureTotal.WithLabelValues(string(ErrorTypeNetwork)).Add(0)
	prwRetryFailureTotal.WithLabelValues(string(ErrorTypeTimeout)).Add(0)
	prwRetryFailureTotal.WithLabelValues(string(ErrorTypeServerError)).Add(0)
	prwRetryFailureTotal.WithLabelValues(string(ErrorTypeClientError)).Add(0)
	prwRetryFailureTotal.WithLabelValues(string(ErrorTypeAuth)).Add(0)
	prwRetryFailureTotal.WithLabelValues(string(ErrorTypeRateLimit)).Add(0)
	prwRetryFailureTotal.WithLabelValues(string(ErrorTypeUnknown)).Add(0)
}

// PRWExporterConfig holds PRW exporter configuration.
type PRWExporterConfig struct {
	// Endpoint is the target PRW endpoint URL.
	Endpoint string
	// DefaultPath is the path to append when endpoint has no path (default: /api/v1/write).
	DefaultPath string
	// Timeout is the request timeout.
	Timeout time.Duration
	// TLS configuration for secure connections.
	TLS tlspkg.ClientConfig
	// Auth configuration for authentication.
	Auth auth.ClientConfig
	// Version is the PRW protocol version to use.
	Version prw.Version
	// VMMode enables VictoriaMetrics-specific features.
	VMMode bool
	// VMOptions holds VictoriaMetrics-specific options.
	VMOptions VMRemoteWriteOptions
	// HTTPClient configuration for connection pooling.
	HTTPClient HTTPClientConfig
}

// VMRemoteWriteOptions holds VictoriaMetrics-specific remote write options.
type VMRemoteWriteOptions struct {
	// ExtraLabels are added to all metrics before sending.
	ExtraLabels map[string]string
	// Compression is the compression type: "snappy" or "zstd".
	Compression string
	// UseShortEndpoint uses /write instead of /api/v1/write.
	UseShortEndpoint bool
}

// PRWExporter exports metrics via Prometheus Remote Write protocol.
type PRWExporter struct {
	config      PRWExporterConfig
	httpClient  *http.Client
	endpoint    string
	version     prw.Version
	compression compression.Type
	extraLabels []prw.Label
}

// NewPRW creates a new PRW exporter.
func NewPRW(ctx context.Context, cfg PRWExporterConfig) (*PRWExporter, error) {
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
	if cfg.TLS.Enabled {
		tlsConfig, err := tlspkg.NewClientTLSConfig(cfg.TLS)
		if err != nil {
			return nil, fmt.Errorf("failed to create TLS config: %w", err)
		}
		transport.TLSClientConfig = tlsConfig
	} else {
		// Default TLS config for HTTPS endpoints
		transport.TLSClientConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
		}
	}

	var roundTripper http.RoundTripper = transport

	// Configure HTTP/2 settings if enabled
	if cfg.HTTPClient.ForceAttemptHTTP2 || transport.TLSClientConfig != nil {
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
		endpoint = "http://localhost:9090"
	}

	// Add scheme if missing
	if !hasScheme(endpoint) {
		if cfg.TLS.Enabled {
			endpoint = "https://" + endpoint
		} else {
			endpoint = "http://" + endpoint
		}
	}

	// Add path if missing
	if !hasPath(endpoint) {
		var path string
		if cfg.DefaultPath != "" {
			path = cfg.DefaultPath
		} else if cfg.VMMode && cfg.VMOptions.UseShortEndpoint {
			path = "/write"
		} else {
			path = "/api/v1/write"
		}
		endpoint = endpoint + path
	}

	// Determine compression type
	compressionType := compression.TypeSnappy // Default for PRW
	if cfg.VMMode && cfg.VMOptions.Compression != "" {
		switch cfg.VMOptions.Compression {
		case "zstd":
			compressionType = compression.TypeZstd
		case "snappy":
			compressionType = compression.TypeSnappy
		case "none", "":
			compressionType = compression.TypeSnappy
		}
	}

	// Build extra labels
	var extraLabels []prw.Label
	if cfg.VMMode && len(cfg.VMOptions.ExtraLabels) > 0 {
		for name, value := range cfg.VMOptions.ExtraLabels {
			extraLabels = append(extraLabels, prw.Label{Name: name, Value: value})
		}
	}

	// Determine version
	version := cfg.Version
	if version == "" {
		version = prw.VersionAuto
	}

	return &PRWExporter{
		config:      cfg,
		httpClient:  client,
		endpoint:    endpoint,
		version:     version,
		compression: compressionType,
		extraLabels: extraLabels,
	}, nil
}

// Export sends a PRW WriteRequest to the configured endpoint.
func (e *PRWExporter) Export(ctx context.Context, req *prw.WriteRequest) error {
	if req == nil || len(req.Timeseries) == 0 {
		return nil
	}

	// Count timeseries and samples for metrics
	timeseriesCount := len(req.Timeseries)
	samplesCount := countPRWSamples(req)

	// Apply extra labels if configured
	if len(e.extraLabels) > 0 {
		req = e.applyExtraLabels(req)
	}

	// Marshal the request to protobuf
	body, err := req.Marshal()
	if err != nil {
		return fmt.Errorf("failed to marshal PRW request: %w", err)
	}

	// Compress the body
	compressedBody, err := compression.Compress(body, compression.Config{
		Type: e.compression,
	})
	if err != nil {
		return fmt.Errorf("failed to compress PRW request: %w", err)
	}

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(compressedBody))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	httpReq.Header.Set("Content-Type", "application/x-protobuf")
	httpReq.Header.Set("Content-Encoding", e.compression.ContentEncoding())

	// Set PRW version header
	switch e.effectiveVersion(req) {
	case prw.Version1:
		httpReq.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")
	case prw.Version2:
		httpReq.Header.Set("X-Prometheus-Remote-Write-Version", "2.0.0")
	}

	prwExportRequestsTotal.Inc()

	// Send request
	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		errType := classifyError(err)
		prwExportErrorsTotal.WithLabelValues(string(errType)).Inc()
		return fmt.Errorf("failed to send PRW request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body for error detection
	bodyBytes, _ := io.ReadAll(resp.Body)
	bodyStr := string(bodyBytes)

	// Check response status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errType := classifyHTTPStatusCode(resp.StatusCode)
		prwExportErrorsTotal.WithLabelValues(string(errType)).Inc()

		msg := bodyStr
		if msg == "" {
			msg = fmt.Sprintf("PRW endpoint returned status %d", resp.StatusCode)
		}

		// 4xx errors
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			innerErr := &PRWClientError{
				StatusCode: resp.StatusCode,
				Message:    msg,
			}
			return &ExportError{
				Err:        innerErr,
				Type:       errType,
				StatusCode: resp.StatusCode,
				Message:    msg,
			}
		}
		// 5xx errors can be retried
		innerErr := &PRWServerError{
			StatusCode: resp.StatusCode,
			Message:    msg,
		}
		return &ExportError{
			Err:        innerErr,
			Type:       errType,
			StatusCode: resp.StatusCode,
			Message:    msg,
		}
	}

	// Track successful export metrics
	prwExportTimeseriesTotal.Add(float64(timeseriesCount))
	prwExportSamplesTotal.Add(float64(samplesCount))
	prwExportBytesTotal.WithLabelValues(string(e.compression)).Add(float64(len(compressedBody)))

	return nil
}

// countPRWSamples counts the total number of samples in a PRW WriteRequest.
func countPRWSamples(req *prw.WriteRequest) int64 {
	var count int64
	for _, ts := range req.Timeseries {
		count += int64(len(ts.Samples))
		count += int64(len(ts.Histograms))
	}
	return count
}

// effectiveVersion returns the effective PRW version for a request.
func (e *PRWExporter) effectiveVersion(req *prw.WriteRequest) prw.Version {
	if e.version != prw.VersionAuto {
		return e.version
	}
	if req.IsPRW2() {
		return prw.Version2
	}
	return prw.Version1
}

// applyExtraLabels adds extra labels to all time series in the request.
func (e *PRWExporter) applyExtraLabels(req *prw.WriteRequest) *prw.WriteRequest {
	if len(e.extraLabels) == 0 {
		return req
	}

	// Clone the request to avoid modifying the original
	clone := req.Clone()
	for i := range clone.Timeseries {
		// Append extra labels
		clone.Timeseries[i].Labels = append(clone.Timeseries[i].Labels, e.extraLabels...)
		// Sort labels for consistency
		clone.Timeseries[i].SortLabels()
	}
	return clone
}

// Close closes the exporter and releases resources.
func (e *PRWExporter) Close() error {
	if e.httpClient != nil {
		e.httpClient.CloseIdleConnections()
	}
	return nil
}

// PRWClientError represents a client error (4xx) from the PRW endpoint.
type PRWClientError struct {
	StatusCode int
	Message    string
}

func (e *PRWClientError) Error() string {
	return e.Message
}

// IsRetryable returns false for client errors (should not retry).
func (e *PRWClientError) IsRetryable() bool {
	return false
}

// PRWServerError represents a server error (5xx) from the PRW endpoint.
type PRWServerError struct {
	StatusCode int
	Message    string
}

func (e *PRWServerError) Error() string {
	return e.Message
}

// IsRetryable returns true for server errors (can retry).
func (e *PRWServerError) IsRetryable() bool {
	return true
}

// IsPRWRetryableError returns true if the error is retryable.
func IsPRWRetryableError(err error) bool {
	if err == nil {
		return false
	}
	var exportErr *ExportError
	if errors.As(err, &exportErr) {
		return exportErr.IsRetryable()
	}
	if se, ok := err.(*PRWServerError); ok {
		return se.IsRetryable()
	}
	if ce, ok := err.(*PRWClientError); ok {
		return ce.IsRetryable()
	}
	// Network errors are generally retryable
	return true
}
