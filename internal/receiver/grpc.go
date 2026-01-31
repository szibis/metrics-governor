package receiver

import (
	"context"
	"io"
	"net"
	"sync"

	"github.com/klauspost/compress/zstd"
	"github.com/szibis/metrics-governor/internal/auth"
	"github.com/szibis/metrics-governor/internal/buffer"
	"github.com/szibis/metrics-governor/internal/logging"
	tlspkg "github.com/szibis/metrics-governor/internal/tls"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/encoding"
	_ "google.golang.org/grpc/encoding/gzip" // Register gzip compressor
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
	server *grpc.Server
	buffer *buffer.MetricsBuffer
	addr   string
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

// Export implements the OTLP MetricsService Export method.
func (r *GRPCReceiver) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) (*colmetricspb.ExportMetricsServiceResponse, error) {
	r.buffer.Add(req.ResourceMetrics)
	return &colmetricspb.ExportMetricsServiceResponse{}, nil
}

// Start starts the gRPC server.
func (r *GRPCReceiver) Start() error {
	lis, err := net.Listen("tcp", r.addr)
	if err != nil {
		return err
	}

	colmetricspb.RegisterMetricsServiceServer(r.server, r)

	logging.Info("gRPC receiver started", logging.F("addr", r.addr))
	return r.server.Serve(lis)
}

// Stop gracefully stops the gRPC server.
func (r *GRPCReceiver) Stop() {
	r.server.GracefulStop()
}
