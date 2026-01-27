package receiver

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"time"

	"github.com/slawomirskowron/metrics-governor/internal/auth"
	"github.com/slawomirskowron/metrics-governor/internal/buffer"
	"github.com/slawomirskowron/metrics-governor/internal/compression"
	"github.com/slawomirskowron/metrics-governor/internal/logging"
	tlspkg "github.com/slawomirskowron/metrics-governor/internal/tls"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// HTTPServerConfig holds HTTP server timeout settings.
type HTTPServerConfig struct {
	// MaxRequestBodySize limits the maximum size of request body.
	// Zero means no limit.
	MaxRequestBodySize int64
	// ReadTimeout is the maximum duration for reading the entire
	// request, including the body. Zero means no timeout.
	ReadTimeout time.Duration
	// ReadHeaderTimeout is the maximum duration for reading request headers.
	// If zero, the value of ReadTimeout is used.
	ReadHeaderTimeout time.Duration
	// WriteTimeout is the maximum duration before timing out writes of the
	// response. Zero means no timeout.
	WriteTimeout time.Duration
	// IdleTimeout is the maximum amount of time to wait for the next request
	// when keep-alives are enabled. Zero means no timeout.
	IdleTimeout time.Duration
	// KeepAlivesEnabled controls whether HTTP keep-alives are enabled.
	KeepAlivesEnabled bool
}

// HTTPConfig holds the HTTP receiver configuration.
type HTTPConfig struct {
	// Addr is the listen address.
	Addr string
	// TLS configuration for secure connections.
	TLS tlspkg.ServerConfig
	// Auth configuration for authentication.
	Auth auth.ServerConfig
	// Server configuration for HTTP server settings.
	Server HTTPServerConfig
}

// HTTPReceiver receives metrics via OTLP HTTP.
type HTTPReceiver struct {
	server             *http.Server
	buffer             *buffer.MetricsBuffer
	addr               string
	tlsConfig          *tls.Config
	maxRequestBodySize int64
}

// NewHTTP creates a new HTTP receiver with default configuration.
func NewHTTP(addr string, buf *buffer.MetricsBuffer) *HTTPReceiver {
	return NewHTTPWithConfig(HTTPConfig{Addr: addr}, buf)
}

// NewHTTPWithConfig creates a new HTTP receiver with the given configuration.
func NewHTTPWithConfig(cfg HTTPConfig, buf *buffer.MetricsBuffer) *HTTPReceiver {
	r := &HTTPReceiver{
		buffer:             buf,
		addr:               cfg.Addr,
		maxRequestBodySize: cfg.Server.MaxRequestBodySize,
	}

	// Configure TLS
	if cfg.TLS.Enabled {
		tlsConfig, err := tlspkg.NewServerTLSConfig(cfg.TLS)
		if err != nil {
			logging.Error("failed to create TLS config", logging.F("error", err.Error()))
		} else {
			r.tlsConfig = tlsConfig
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/metrics", r.handleMetrics)

	// Wrap with auth middleware if enabled
	var handler http.Handler = mux
	if cfg.Auth.Enabled {
		handler = auth.HTTPMiddleware(cfg.Auth, mux)
	}

	// Apply default server timeouts if not set
	readHeaderTimeout := cfg.Server.ReadHeaderTimeout
	if readHeaderTimeout == 0 {
		readHeaderTimeout = 1 * time.Minute
	}
	writeTimeout := cfg.Server.WriteTimeout
	if writeTimeout == 0 {
		writeTimeout = 30 * time.Second
	}
	idleTimeout := cfg.Server.IdleTimeout
	if idleTimeout == 0 {
		idleTimeout = 1 * time.Minute
	}

	r.server = &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		TLSConfig:         r.tlsConfig,
		ReadTimeout:       cfg.Server.ReadTimeout,
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	// Disable keep-alives if configured
	if !cfg.Server.KeepAlivesEnabled && cfg.Server.ReadTimeout != 0 {
		r.server.SetKeepAlivesEnabled(false)
	}

	return r
}

// handleMetrics handles incoming OTLP HTTP metrics requests.
func (r *HTTPReceiver) handleMetrics(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Limit request body size if configured
	var bodyReader io.Reader = req.Body
	if r.maxRequestBodySize > 0 {
		bodyReader = io.LimitReader(req.Body, r.maxRequestBodySize)
	}

	body, err := io.ReadAll(bodyReader)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer req.Body.Close()

	// Decompress body if Content-Encoding is set
	contentEncoding := req.Header.Get("Content-Encoding")
	if contentEncoding != "" {
		compressionType := compression.ParseContentEncoding(contentEncoding)
		if compressionType != compression.TypeNone {
			body, err = compression.Decompress(body, compressionType)
			if err != nil {
				logging.Error("failed to decompress request body", logging.F("encoding", contentEncoding, "error", err.Error()))
				http.Error(w, "Failed to decompress body", http.StatusBadRequest)
				return
			}
		}
	}

	var exportReq colmetricspb.ExportMetricsServiceRequest

	contentType := req.Header.Get("Content-Type")
	switch contentType {
	case "application/x-protobuf":
		if err := proto.Unmarshal(body, &exportReq); err != nil {
			http.Error(w, "Failed to unmarshal protobuf", http.StatusBadRequest)
			return
		}
	default:
		// TODO: add JSON support
		http.Error(w, "Unsupported content type", http.StatusUnsupportedMediaType)
		return
	}

	r.buffer.Add(exportReq.ResourceMetrics)

	// Return empty response
	resp := &colmetricspb.ExportMetricsServiceResponse{}
	respBytes, err := proto.Marshal(resp)
	if err != nil {
		http.Error(w, "Failed to marshal response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	w.Write(respBytes)
}

// Start starts the HTTP server.
func (r *HTTPReceiver) Start() error {
	logging.Info("HTTP receiver started", logging.F("addr", r.addr, "tls", r.tlsConfig != nil))
	if r.tlsConfig != nil {
		// TLS certificate is already configured in the server's TLSConfig
		return r.server.ListenAndServeTLS("", "")
	}
	return r.server.ListenAndServe()
}

// Stop gracefully stops the HTTP server.
func (r *HTTPReceiver) Stop(ctx context.Context) error {
	return r.server.Shutdown(ctx)
}
