package exporter

import (
	"context"
	"time"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// OTLPExporter exports metrics via OTLP gRPC.
type OTLPExporter struct {
	conn    *grpc.ClientConn
	client  colmetricspb.MetricsServiceClient
	timeout time.Duration
}

// Config holds the exporter configuration.
type Config struct {
	Endpoint string
	Insecure bool
	Timeout  time.Duration
}

// New creates a new OTLPExporter.
func New(ctx context.Context, cfg Config) (*OTLPExporter, error) {
	var opts []grpc.DialOption

	if cfg.Insecure {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	conn, err := grpc.NewClient(cfg.Endpoint, opts...)
	if err != nil {
		return nil, err
	}

	client := colmetricspb.NewMetricsServiceClient(conn)

	return &OTLPExporter{
		conn:    conn,
		client:  client,
		timeout: cfg.Timeout,
	}, nil
}

// Export sends metrics to the configured endpoint.
func (e *OTLPExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	_, err := e.client.Export(ctx, req)
	return err
}

// Close closes the exporter connection.
func (e *OTLPExporter) Close() error {
	return e.conn.Close()
}
