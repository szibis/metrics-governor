package receiver

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"

	"github.com/slawomirskowron/metrics-governor/internal/auth"
	"github.com/slawomirskowron/metrics-governor/internal/buffer"
	"github.com/slawomirskowron/metrics-governor/internal/logging"
	tlspkg "github.com/slawomirskowron/metrics-governor/internal/tls"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// HTTPConfig holds the HTTP receiver configuration.
type HTTPConfig struct {
	// Addr is the listen address.
	Addr string
	// TLS configuration for secure connections.
	TLS tlspkg.ServerConfig
	// Auth configuration for authentication.
	Auth auth.ServerConfig
}

// HTTPReceiver receives metrics via OTLP HTTP.
type HTTPReceiver struct {
	server    *http.Server
	buffer    *buffer.MetricsBuffer
	addr      string
	tlsConfig *tls.Config
}

// NewHTTP creates a new HTTP receiver with default configuration.
func NewHTTP(addr string, buf *buffer.MetricsBuffer) *HTTPReceiver {
	return NewHTTPWithConfig(HTTPConfig{Addr: addr}, buf)
}

// NewHTTPWithConfig creates a new HTTP receiver with the given configuration.
func NewHTTPWithConfig(cfg HTTPConfig, buf *buffer.MetricsBuffer) *HTTPReceiver {
	r := &HTTPReceiver{
		buffer: buf,
		addr:   cfg.Addr,
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

	r.server = &http.Server{
		Addr:      cfg.Addr,
		Handler:   handler,
		TLSConfig: r.tlsConfig,
	}

	return r
}

// handleMetrics handles incoming OTLP HTTP metrics requests.
func (r *HTTPReceiver) handleMetrics(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(req.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	defer req.Body.Close()

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
