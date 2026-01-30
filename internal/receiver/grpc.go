package receiver

import (
	"context"
	"net"

	"github.com/slawomirskowron/metrics-governor/internal/auth"
	"github.com/slawomirskowron/metrics-governor/internal/buffer"
	"github.com/slawomirskowron/metrics-governor/internal/logging"
	tlspkg "github.com/slawomirskowron/metrics-governor/internal/tls"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

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
