package receiver

import (
	"context"
	"net"

	"github.com/slawomirskowron/metrics-governor/internal/buffer"
	"github.com/slawomirskowron/metrics-governor/internal/logging"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/grpc"
)

// GRPCReceiver receives metrics via OTLP gRPC.
type GRPCReceiver struct {
	colmetricspb.UnimplementedMetricsServiceServer
	server *grpc.Server
	buffer *buffer.MetricsBuffer
	addr   string
}

// NewGRPC creates a new gRPC receiver.
func NewGRPC(addr string, buf *buffer.MetricsBuffer) *GRPCReceiver {
	return &GRPCReceiver{
		server: grpc.NewServer(),
		buffer: buf,
		addr:   addr,
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
