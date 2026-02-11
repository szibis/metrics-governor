package receiver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/klauspost/compress/zstd"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/szibis/metrics-governor/internal/auth"
	"github.com/szibis/metrics-governor/internal/buffer"
	"github.com/szibis/metrics-governor/internal/logging"
	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	tlspkg "github.com/szibis/metrics-governor/internal/tls"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/encoding"
	_ "google.golang.org/grpc/encoding/gzip" // Register gzip compressor
	"google.golang.org/grpc/stats"
	"google.golang.org/grpc/status"
)

func init() {
	// Register zstd compressor for gRPC
	encoding.RegisterCompressor(&zstdCompressor{})
}

// zstdCompressor implements grpc encoding.Compressor for zstd.
type zstdCompressor struct{}

func (c *zstdCompressor) Name() string {
	return "zstd"
}

func (c *zstdCompressor) Compress(w io.Writer) (io.WriteCloser, error) {
	return zstdWriterPoolW.Get(w)
}

func (c *zstdCompressor) Decompress(r io.Reader) (io.Reader, error) {
	return zstdReaderPoolW.Get(r)
}

// zstd encoder/decoder pools for performance
var zstdWriterPool = &sync.Pool{
	New: func() interface{} {
		w, _ := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
		return w
	},
}

var zstdReaderPool = &sync.Pool{
	New: func() interface{} {
		r, _ := zstd.NewReader(nil)
		return r
	},
}

type pooledZstdWriter struct {
	*zstd.Encoder
	underlying io.Writer
}

func (p *pooledZstdWriter) Close() error {
	err := p.Encoder.Close()
	p.Encoder.Reset(nil)
	zstdWriterPool.Put(p.Encoder)
	return err
}

type pooledZstdReader struct {
	*zstd.Decoder
}

func (p *pooledZstdReader) Read(b []byte) (int, error) {
	n, err := p.Decoder.Read(b)
	if err == io.EOF {
		_ = p.Decoder.Reset(nil)
		zstdReaderPool.Put(p.Decoder)
	}
	return n, err
}

// zstdWriterPoolWrapper wraps the pool to return io.WriteCloser
type zstdWriterPoolWrapper struct{}

func (p *zstdWriterPoolWrapper) Get(w io.Writer) (io.WriteCloser, error) {
	encoder := zstdWriterPool.New().(*zstd.Encoder)
	encoder.Reset(w)
	return &pooledZstdWriter{Encoder: encoder, underlying: w}, nil
}

// zstdReaderPoolWrapper wraps the pool to return io.Reader
type zstdReaderPoolWrapper struct{}

func (p *zstdReaderPoolWrapper) Get(r io.Reader) (io.Reader, error) {
	decoder := zstdReaderPool.New().(*zstd.Decoder)
	if err := decoder.Reset(r); err != nil {
		return nil, err
	}
	return &pooledZstdReader{Decoder: decoder}, nil
}

var zstdWriterPoolW = &zstdWriterPoolWrapper{}
var zstdReaderPoolW = &zstdReaderPoolWrapper{}

// Prometheus metrics for gRPC receiver
var (
	grpcReceivedBytesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_grpc_received_bytes_total",
		Help: "Total gRPC bytes received by compression state",
	}, []string{"compression"})
)

func init() {
	prometheus.MustRegister(grpcReceivedBytesTotal)
	// Initialize counters
	grpcReceivedBytesTotal.WithLabelValues("compressed").Add(0)
	grpcReceivedBytesTotal.WithLabelValues("uncompressed").Add(0)
}

// grpcStatsHandler tracks wire bytes for compression metrics
type grpcStatsHandler struct{}

func (h *grpcStatsHandler) TagRPC(ctx context.Context, info *stats.RPCTagInfo) context.Context {
	return ctx
}

func (h *grpcStatsHandler) HandleRPC(ctx context.Context, s stats.RPCStats) {
	if in, ok := s.(*stats.InPayload); ok {
		// WireLength is the compressed size, Length is decompressed
		if in.WireLength > 0 && in.WireLength < in.Length {
			// Data was compressed
			grpcReceivedBytesTotal.WithLabelValues("compressed").Add(float64(in.WireLength))
			grpcReceivedBytesTotal.WithLabelValues("uncompressed").Add(float64(in.Length))
		} else {
			// Data was not compressed
			grpcReceivedBytesTotal.WithLabelValues("uncompressed").Add(float64(in.Length))
		}
	}
}

func (h *grpcStatsHandler) TagConn(ctx context.Context, info *stats.ConnTagInfo) context.Context {
	return ctx
}

func (h *grpcStatsHandler) HandleConn(ctx context.Context, s stats.ConnStats) {}

// GRPCConfig holds the gRPC receiver configuration.
type GRPCConfig struct {
	// Addr is the listen address.
	Addr string
	// TLS configuration for secure connections.
	TLS tlspkg.ServerConfig
	// Auth configuration for authentication.
	Auth auth.ServerConfig
}

// GRPCReceiver receives metrics via OTLP gRPC.
type GRPCReceiver struct {
	colmetricspb.UnimplementedMetricsServiceServer
	server                *grpc.Server
	buffer                *buffer.MetricsBuffer
	addr                  string
	running               atomic.Bool
	health                PipelineHealthChecker
	loadSheddingThreshold float64
}

// NewGRPC creates a new gRPC receiver with default configuration.
func NewGRPC(addr string, buf *buffer.MetricsBuffer) *GRPCReceiver {
	return NewGRPCWithConfig(GRPCConfig{Addr: addr}, buf)
}

// NewGRPCWithConfig creates a new gRPC receiver with the given configuration.
func NewGRPCWithConfig(cfg GRPCConfig, buf *buffer.MetricsBuffer) *GRPCReceiver {
	var opts []grpc.ServerOption

	// Configure max message size (64MB to handle large batches)
	maxMsgSize := 64 * 1024 * 1024 // 64MB
	opts = append(opts,
		grpc.MaxRecvMsgSize(maxMsgSize),
		grpc.MaxSendMsgSize(maxMsgSize),
		grpc.StatsHandler(&grpcStatsHandler{}),
	)

	// Configure TLS
	if cfg.TLS.Enabled {
		tlsConfig, err := tlspkg.NewServerTLSConfig(cfg.TLS)
		if err != nil {
			logging.Error("failed to create TLS config", logging.F("error", err.Error()))
		} else {
			opts = append(opts, grpc.Creds(credentials.NewTLS(tlsConfig)))
		}
	}

	// Configure authentication
	if cfg.Auth.Enabled {
		opts = append(opts, grpc.UnaryInterceptor(auth.GRPCServerInterceptor(cfg.Auth)))
	}

	return &GRPCReceiver{
		server: grpc.NewServer(opts...),
		buffer: buf,
		addr:   cfg.Addr,
	}
}

// SetPipelineHealth configures pipeline health checking for load shedding.
func (r *GRPCReceiver) SetPipelineHealth(h PipelineHealthChecker, threshold float64) {
	r.health = h
	r.loadSheddingThreshold = threshold
}

// Export implements the OTLP MetricsService Export method.
func (r *GRPCReceiver) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) (*colmetricspb.ExportMetricsServiceResponse, error) {
	IncrementReceiverRequests("grpc")

	// Load shedding: reject if pipeline is overloaded
	if r.health != nil && r.health.IsOverloaded(r.loadSheddingThreshold) {
		IncrementLoadShedding("grpc")
		return nil, status.Error(codes.ResourceExhausted, "pipeline overloaded, retry later")
	}

	if err := r.buffer.Add(req.ResourceMetrics); err != nil {
		if errors.Is(err, buffer.ErrBufferFull) {
			return nil, status.Error(codes.ResourceExhausted, "buffer capacity exceeded")
		}
		return nil, status.Errorf(codes.Internal, "buffer add failed: %v", err)
	}
	return &colmetricspb.ExportMetricsServiceResponse{}, nil
}

// Start starts the gRPC server.
func (r *GRPCReceiver) Start() error {
	lis, err := net.Listen("tcp", r.addr)
	if err != nil {
		return err
	}

	colmetricspb.RegisterMetricsServiceServer(r.server, r)

	r.running.Store(true)
	logging.Info("gRPC receiver started", logging.F("addr", r.addr))
	err = r.server.Serve(lis)
	r.running.Store(false)
	return err
}

// Stop gracefully stops the gRPC server.
func (r *GRPCReceiver) Stop() {
	r.server.GracefulStop()
}

// HealthCheck returns nil if the gRPC receiver is running and accepting connections.
func (r *GRPCReceiver) HealthCheck() error {
	if !r.running.Load() {
		return fmt.Errorf("gRPC receiver not running on %s", r.addr)
	}
	return nil
}
