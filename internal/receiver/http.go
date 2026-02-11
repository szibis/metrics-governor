package receiver

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

	"github.com/szibis/metrics-governor/internal/auth"
	"github.com/szibis/metrics-governor/internal/buffer"
	"github.com/szibis/metrics-governor/internal/compression"
	"github.com/szibis/metrics-governor/internal/logging"
	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	"github.com/szibis/metrics-governor/internal/pipeline"
	tlspkg "github.com/szibis/metrics-governor/internal/tls"
	"google.golang.org/protobuf/encoding/protojson"
)

// exportReqPool uses vtprotobuf's built-in sync.Pool with ResetVT (clears fields
// but preserves slice capacities). After warmup, UnmarshalVT into a pooled request
// reuses existing memory — near-zero allocations for the message structure.
//
// Legacy exportReqPool replaced by colmetricspb.ExportMetricsServiceRequestFromVTPool().

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
	// Path is the URL path for receiving metrics (default: /v1/metrics).
	Path string
	// TLS configuration for secure connections.
	TLS tlspkg.ServerConfig
	// Auth configuration for authentication.
	Auth auth.ServerConfig
	// Server configuration for HTTP server settings.
	Server HTTPServerConfig
}

// HTTPReceiver receives metrics via OTLP HTTP.
type HTTPReceiver struct {
	server                *http.Server
	buffer                *buffer.MetricsBuffer
	addr                  string
	tlsConfig             *tls.Config
	maxRequestBodySize    int64
	health                PipelineHealthChecker
	loadSheddingThreshold float64
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

	// Use default path if not specified
	path := cfg.Path
	if path == "" {
		path = "/v1/metrics"
	}

	mux := http.NewServeMux()
	mux.HandleFunc(path, r.handleMetrics)

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
// SetPipelineHealth configures pipeline health checking for load shedding.
func (r *HTTPReceiver) SetPipelineHealth(h PipelineHealthChecker, threshold float64) {
	r.health = h
	r.loadSheddingThreshold = threshold
}

func (r *HTTPReceiver) handleMetrics(w http.ResponseWriter, req *http.Request) {
	IncrementReceiverRequests("http")

	if req.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Load shedding: reject if pipeline is overloaded
	if r.health != nil && r.health.IsOverloaded(r.loadSheddingThreshold) {
		IncrementLoadShedding("http")
		w.Header().Set("Retry-After", "5")
		http.Error(w, "pipeline overloaded, retry later", http.StatusTooManyRequests)
		return
	}

	// Limit request body size if configured
	var bodyReader io.Reader = req.Body
	if r.maxRequestBodySize > 0 {
		bodyReader = io.LimitReader(req.Body, r.maxRequestBodySize)
	}

	// Read body into pooled buffer to avoid per-request allocation.
	readBuf := compression.GetBuffer()
	if _, err := readBuf.ReadFrom(bodyReader); err != nil {
		compression.ReleaseBuffer(readBuf)
		IncrementReceiverError("read")
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	req.Body.Close()

	// body points to pooled buffer bytes; valid until the owning buffer is released.
	body := readBuf.Bytes()
	var decompBuf *bytes.Buffer // non-nil when decompression replaces body

	// Decompress body if Content-Encoding is set
	contentEncoding := req.Header.Get("Content-Encoding")
	if contentEncoding != "" {
		compressionType := compression.ParseContentEncoding(contentEncoding)
		if compressionType != compression.TypeNone {
			start := time.Now()
			decompBuf = compression.GetBuffer()
			if err := compression.DecompressToBuf(decompBuf, body, compressionType); err != nil {
				compression.ReleaseBuffer(decompBuf)
				compression.ReleaseBuffer(readBuf)
				IncrementReceiverError("decompress")
				logging.Error("failed to decompress request body", logging.F("encoding", contentEncoding, "error", err.Error()))
				http.Error(w, "Failed to decompress body", http.StatusBadRequest)
				return
			}
			pipeline.Record("receive_decompress", pipeline.Since(start))
			// Decompression consumed the read buffer — release it early.
			compression.ReleaseBuffer(readBuf)
			readBuf = nil
			body = decompBuf.Bytes()
			pipeline.RecordBytes("receive_decompress", len(body))
		}
	}

	// Release whichever buffer is still active after unmarshal.
	defer func() {
		if readBuf != nil {
			compression.ReleaseBuffer(readBuf)
		}
		if decompBuf != nil {
			compression.ReleaseBuffer(decompBuf)
		}
	}()

	exportReq := colmetricspb.ExportMetricsServiceRequestFromVTPool()
	defer exportReq.ReturnToVTPool() // ResetVT preserves slice capacities for reuse

	contentType := req.Header.Get("Content-Type")
	isJSON := false
	unmarshalStart := time.Now()
	switch contentType {
	case "application/x-protobuf", "application/protobuf":
		if err := exportReq.UnmarshalVT(body); err != nil {
			IncrementReceiverError("decode")
			http.Error(w, "Failed to unmarshal protobuf", http.StatusBadRequest)
			return
		}
	case "application/json":
		isJSON = true
		if err := protojson.Unmarshal(body, exportReq); err != nil {
			IncrementReceiverError("decode")
			http.Error(w, "Failed to unmarshal JSON", http.StatusBadRequest)
			return
		}
	default:
		IncrementReceiverError("decode")
		http.Error(w, "Unsupported content type; expected application/x-protobuf or application/json", http.StatusUnsupportedMediaType)
		return
	}
	pipeline.Record("receive_unmarshal", pipeline.Since(unmarshalStart))
	pipeline.RecordBytes("receive_unmarshal", len(body))

	if err := r.buffer.Add(exportReq.ResourceMetrics); err != nil {
		if errors.Is(err, buffer.ErrBufferFull) {
			w.Header().Set("Retry-After", "5")
			http.Error(w, "buffer capacity exceeded", http.StatusTooManyRequests)
			return
		}
		http.Error(w, "buffer add failed", http.StatusInternalServerError)
		return
	}
	// Detach ResourceMetrics from pooled request: buffer now owns these
	// pointers, and ResetVT would otherwise clear them on pool return.
	exportReq.ResourceMetrics = nil

	// Return response in the same format as the request
	resp := &colmetricspb.ExportMetricsServiceResponse{}
	if isJSON {
		respBytes, err := protojson.Marshal(resp)
		if err != nil {
			http.Error(w, "Failed to marshal JSON response", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(respBytes)
	} else {
		respBytes, err := resp.MarshalVT()
		if err != nil {
			http.Error(w, "Failed to marshal response", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(respBytes)
	}
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

// HealthCheck returns nil if the HTTP receiver port is accepting connections.
func (r *HTTPReceiver) HealthCheck() error {
	conn, err := net.DialTimeout("tcp", r.addr, 1*time.Second)
	if err != nil {
		return fmt.Errorf("HTTP receiver not reachable on %s: %w", r.addr, err)
	}
	conn.Close()
	return nil
}
