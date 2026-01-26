package receiver

import (
	"context"
	"io"
	"log"
	"net/http"

	"github.com/slawomirskowron/metrics-governor/internal/buffer"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// HTTPReceiver receives metrics via OTLP HTTP.
type HTTPReceiver struct {
	server *http.Server
	buffer *buffer.MetricsBuffer
	addr   string
}

// NewHTTP creates a new HTTP receiver.
func NewHTTP(addr string, buf *buffer.MetricsBuffer) *HTTPReceiver {
	r := &HTTPReceiver{
		buffer: buf,
		addr:   addr,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/metrics", r.handleMetrics)

	r.server = &http.Server{
		Addr:    addr,
		Handler: mux,
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
	log.Printf("HTTP receiver listening on %s", r.addr)
	return r.server.ListenAndServe()
}

// Stop gracefully stops the HTTP server.
func (r *HTTPReceiver) Stop(ctx context.Context) error {
	return r.server.Shutdown(ctx)
}
