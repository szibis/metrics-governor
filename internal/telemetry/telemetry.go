package telemetry

import (
	"context"
	"fmt"
	"time"

	prombridge "go.opentelemetry.io/contrib/bridges/prometheus"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Config holds configuration for OTLP telemetry export.
type Config struct {
	Endpoint string // OTLP endpoint (empty = disabled)
	Protocol string // "grpc" or "http"
	Insecure bool   // use insecure connection
}

// Telemetry holds the OTEL SDK providers for self-monitoring.
type Telemetry struct {
	logProvider   *sdklog.LoggerProvider
	meterProvider *metric.MeterProvider
	logger        otellog.Logger
	shutdownFuncs []func(context.Context) error
}

// Enabled returns true if telemetry is configured.
func (t *Telemetry) Enabled() bool {
	return t != nil && t.logger != nil
}

// Logger returns the OTEL logger for emitting log records.
func (t *Telemetry) Logger() otellog.Logger {
	if t == nil {
		return nil
	}
	return t.logger
}

// Init creates and starts OTLP log and metric exporters.
// Returns nil if cfg.Endpoint is empty (telemetry disabled).
func Init(ctx context.Context, cfg Config, serviceName, serviceVersion string) (*Telemetry, error) {
	if cfg.Endpoint == "" {
		return nil, nil
	}

	if cfg.Protocol == "" {
		cfg.Protocol = "grpc"
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(serviceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: create resource: %w", err)
	}

	t := &Telemetry{}

	// Set up log exporter
	logExporter, err := newLogExporter(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("telemetry: create log exporter: %w", err)
	}

	t.logProvider = sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
	)
	t.shutdownFuncs = append(t.shutdownFuncs, t.logProvider.Shutdown)
	t.logger = t.logProvider.Logger("metrics-governor")

	// Set up metric exporter with Prometheus bridge
	metricExporter, err := newMetricExporter(ctx, cfg)
	if err != nil {
		_ = t.Shutdown(ctx)
		return nil, fmt.Errorf("telemetry: create metric exporter: %w", err)
	}

	// Bridge Prometheus registry metrics into OTEL
	bridge := prombridge.NewMetricProducer()

	t.meterProvider = metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(
			metric.NewPeriodicReader(metricExporter,
				metric.WithInterval(30*time.Second),
				metric.WithProducer(bridge),
			),
		),
	)
	t.shutdownFuncs = append(t.shutdownFuncs, t.meterProvider.Shutdown)

	return t, nil
}

// Shutdown gracefully shuts down all telemetry providers.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	if t == nil {
		return nil
	}
	var firstErr error
	for _, fn := range t.shutdownFuncs {
		if err := fn(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func newLogExporter(ctx context.Context, cfg Config) (sdklog.Exporter, error) {
	switch cfg.Protocol {
	case "http":
		opts := []otlploghttp.Option{
			otlploghttp.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			opts = append(opts, otlploghttp.WithInsecure())
		}
		return otlploghttp.New(ctx, opts...)
	default: // grpc
		opts := []otlploggrpc.Option{
			otlploggrpc.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			opts = append(opts, otlploggrpc.WithInsecure())
		}
		return otlploggrpc.New(ctx, opts...)
	}
}

func newMetricExporter(ctx context.Context, cfg Config) (metric.Exporter, error) {
	switch cfg.Protocol {
	case "http":
		opts := []otlpmetrichttp.Option{
			otlpmetrichttp.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			opts = append(opts, otlpmetrichttp.WithInsecure())
		}
		return otlpmetrichttp.New(ctx, opts...)
	default: // grpc
		opts := []otlpmetricgrpc.Option{
			otlpmetricgrpc.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			opts = append(opts, otlpmetricgrpc.WithInsecure())
		}
		return otlpmetricgrpc.New(ctx, opts...)
	}
}
