package receiver

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
	"github.com/szibis/metrics-governor/internal/logging"
	"github.com/szibis/metrics-governor/internal/pipeline"
	"github.com/szibis/metrics-governor/internal/prw"
	tlspkg "github.com/szibis/metrics-governor/internal/tls"
)

// PRWServerConfig holds PRW HTTP server settings.
type PRWServerConfig struct {
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

// PRWConfig holds the PRW receiver configuration.
type PRWConfig struct {
	// Addr is the listen address.
	Addr string
	// Path is the URL path for receiving PRW data (default: /api/v1/write).
	// If empty, both /api/v1/write and /write are registered.
	Path string
	// TLS configuration for secure connections.
	TLS tlspkg.ServerConfig
	// Auth configuration for authentication.
	Auth auth.ServerConfig
	// Server configuration for HTTP server settings.
	Server PRWServerConfig
	// Version is the PRW protocol version to accept.
	Version prw.Version
}

// PRWBuffer is the interface the receiver uses to add PRW data.
type PRWBuffer interface {
	Add(req *prw.WriteRequest)
}

// PRWReceiver receives metrics via Prometheus Remote Write protocol.
type PRWReceiver struct {
	server                *http.Server
	buffer                PRWBuffer
	addr                  string
	tlsConfig             *tls.Config
	maxRequestBodySize    int64
	version               prw.Version
	health                PipelineHealthChecker
	loadSheddingThreshold float64
}

// NewPRW creates a new PRW receiver with default configuration.
func NewPRW(addr string, buf PRWBuffer) *PRWReceiver {
	return NewPRWWithConfig(PRWConfig{Addr: addr}, buf)
}

// NewPRWWithConfig creates a new PRW receiver with the given configuration.
func NewPRWWithConfig(cfg PRWConfig, buf PRWBuffer) *PRWReceiver {
	r := &PRWReceiver{
		buffer:             buf,
		addr:               cfg.Addr,
		maxRequestBodySize: cfg.Server.MaxRequestBodySize,
		version:            cfg.Version,
	}

	// Configure TLS
	if cfg.TLS.Enabled {
		tlsConfig, err := tlspkg.NewServerTLSConfig(cfg.TLS)
		if err != nil {
			logging.Error("failed to create TLS config for PRW receiver", logging.F("error", err.Error()))
		} else {
			r.tlsConfig = tlsConfig
		}
	}

	mux := http.NewServeMux()
	if cfg.Path != "" {
		// Use custom path if specified
		mux.HandleFunc(cfg.Path, r.handleWrite)
	} else {
		// Default: register both standard and VM shorthand endpoints
		mux.HandleFunc("/api/v1/write", r.handleWrite)
		mux.HandleFunc("/write", r.handleWrite)
	}

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

// handleWrite handles incoming PRW write requests.
// SetPipelineHealth configures pipeline health checking for load shedding.
func (r *PRWReceiver) SetPipelineHealth(h PipelineHealthChecker, threshold float64) {
	r.health = h
	r.loadSheddingThreshold = threshold
}

func (r *PRWReceiver) handleWrite(w http.ResponseWriter, req *http.Request) {
	IncrementReceiverRequests("prw")

	if req.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Load shedding: reject if pipeline is overloaded
	if r.health != nil && r.health.IsOverloaded(r.loadSheddingThreshold) {
		IncrementLoadShedding("prw")
		w.Header().Set("Retry-After", "5")
		http.Error(w, "pipeline overloaded, retry later", http.StatusTooManyRequests)
		return
	}

	// Validate Content-Type
	contentType := req.Header.Get("Content-Type")
	if contentType != "" && contentType != "application/x-protobuf" {
		IncrementReceiverError("decode")
		http.Error(w, "Unsupported content type, expected application/x-protobuf", http.StatusUnsupportedMediaType)
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

	body := readBuf.Bytes()
	var decompBuf *bytes.Buffer

	// Decompress body based on Content-Encoding
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
				logging.Error("failed to decompress PRW request body", logging.F(
					"encoding", contentEncoding,
					"error", err.Error(),
				))
				http.Error(w, "Failed to decompress body", http.StatusBadRequest)
				return
			}
			pipeline.Record("receive_decompress", pipeline.Since(start))
			compression.ReleaseBuffer(readBuf)
			readBuf = nil
			body = decompBuf.Bytes()
			pipeline.RecordBytes("receive_decompress", len(body))
		}
	}

	defer func() {
		if readBuf != nil {
			compression.ReleaseBuffer(readBuf)
		}
		if decompBuf != nil {
			compression.ReleaseBuffer(decompBuf)
		}
	}()

	// Unmarshal the WriteRequest
	unmarshalStart := time.Now()
	var writeReq prw.WriteRequest
	if err := writeReq.Unmarshal(body); err != nil {
		IncrementReceiverError("decode")
		logging.Error("failed to unmarshal PRW request", logging.F("error", err.Error()))
		http.Error(w, "Failed to unmarshal protobuf", http.StatusBadRequest)
		return
	}
	pipeline.Record("receive_unmarshal", pipeline.Since(unmarshalStart))
	pipeline.RecordBytes("receive_unmarshal", len(body))

	// Validate version if configured
	if r.version != prw.VersionAuto && r.version != "" {
		isPRW2 := writeReq.IsPRW2()
		if r.version == prw.Version1 && isPRW2 {
			http.Error(w, "PRW 2.0 features not supported, configure version: 2.0 or auto", http.StatusBadRequest)
			return
		}
	}

	// Add to buffer
	r.buffer.Add(&writeReq)

	// Return success (204 No Content is standard for PRW)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	// Add compliance headers
	samplesWritten := 0
	histogramsWritten := 0
	for i := range writeReq.Timeseries {
		samplesWritten += len(writeReq.Timeseries[i].Samples)
		histogramsWritten += len(writeReq.Timeseries[i].Histograms)
	}

	// PRW compliance headers
	if samplesWritten > 0 {
		w.Header().Set("X-Prometheus-Remote-Write-Samples-Written", itoa(samplesWritten))
	}
	if histogramsWritten > 0 {
		w.Header().Set("X-Prometheus-Remote-Write-Histograms-Written", itoa(histogramsWritten))
	}

	w.WriteHeader(http.StatusNoContent)
}

// Start starts the PRW HTTP server.
func (r *PRWReceiver) Start() error {
	logging.Info("PRW receiver started", logging.F(
		"addr", r.addr,
		"tls", r.tlsConfig != nil,
		"version", string(r.version),
	))
	if r.tlsConfig != nil {
		// TLS certificate is already configured in the server's TLSConfig
		return r.server.ListenAndServeTLS("", "")
	}
	return r.server.ListenAndServe()
}

// Stop gracefully stops the PRW HTTP server.
func (r *PRWReceiver) Stop(ctx context.Context) error {
	return r.server.Shutdown(ctx)
}

// HealthCheck returns nil if the PRW receiver port is accepting connections.
func (r *PRWReceiver) HealthCheck() error {
	conn, err := net.DialTimeout("tcp", r.addr, 1*time.Second)
	if err != nil {
		return fmt.Errorf("PRW receiver not reachable on %s: %w", r.addr, err)
	}
	conn.Close()
	return nil
}

// itoa converts an int to a string without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}

	negative := n < 0
	if negative {
		n = -n
	}

	// Maximum 10 digits for int32
	buf := make([]byte, 0, 11)
	for n > 0 {
		buf = append(buf, byte('0'+n%10))
		n /= 10
	}

	if negative {
		buf = append(buf, '-')
	}

	// Reverse
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}

	return string(buf)
}
