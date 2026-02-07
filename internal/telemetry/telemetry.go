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
	Endpoint         string            // OTLP endpoint (empty = disabled)
	Protocol         string            // "grpc" or "http"
	Insecure         bool              // use insecure connection
	Timeout          time.Duration     // per-export timeout (default: SDK default 10s)
	PushInterval     time.Duration     // metric push interval (default: 30s)
	Compression      string            // "gzip" or "" (default: "")
	Headers          map[string]string // custom headers (auth, etc.)
	ShutdownTimeout  time.Duration     // shutdown grace period (default: 5s)
	RetryEnabled     bool              // enable retry (default: true, matches SDK)
	RetryInitial     time.Duration     // initial retry interval (default: SDK default 5s)
	RetryMaxInterval time.Duration     // max retry interval (default: SDK default 30s)
	RetryMaxElapsed  time.Duration     // max total retry time (default: SDK default 1m)
}

// Telemetry holds the OTEL SDK providers for self-monitoring.
type Telemetry struct {
	logProvider     *sdklog.LoggerProvider
	meterProvider   *metric.MeterProvider
	logger          otellog.Logger
	shutdownFuncs   []func(context.Context) error
	shutdownTimeout time.Duration
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

// ShutdownTimeout returns the configured shutdown timeout.
func (t *Telemetry) ShutdownTimeout() time.Duration {
	if t == nil || t.shutdownTimeout <= 0 {
		return 5 * time.Second
	}
	return t.shutdownTimeout
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

	t := &Telemetry{
		shutdownTimeout: cfg.ShutdownTimeout,
	}

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

	pushInterval := cfg.PushInterval
	if pushInterval <= 0 {
		pushInterval = 30 * time.Second
	}

	t.meterProvider = metric.NewMeterProvider(
		metric.WithResource(res),
		metric.WithReader(
			metric.NewPeriodicReader(metricExporter,
				metric.WithInterval(pushInterval),
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

//nolint:dupl // OTEL SDK uses distinct option types per exporter; structural similarity is unavoidable.
func newLogExporter(ctx context.Context, cfg Config) (sdklog.Exporter, error) {
	switch cfg.Protocol {
	case "http":
		opts := []otlploghttp.Option{
			otlploghttp.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			opts = append(opts, otlploghttp.WithInsecure())
		}
		if cfg.Timeout > 0 {
			opts = append(opts, otlploghttp.WithTimeout(cfg.Timeout))
		}
		if cfg.Compression == "gzip" {
			opts = append(opts, otlploghttp.WithCompression(otlploghttp.GzipCompression))
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlploghttp.WithHeaders(cfg.Headers))
		}
		if cfg.RetryEnabled {
			opts = append(opts, otlploghttp.WithRetry(otlploghttp.RetryConfig{
				Enabled:         true,
				InitialInterval: cfg.RetryInitial,
				MaxInterval:     cfg.RetryMaxInterval,
				MaxElapsedTime:  cfg.RetryMaxElapsed,
			}))
		}
		return otlploghttp.New(ctx, opts...)
	default: // grpc
		opts := []otlploggrpc.Option{
			otlploggrpc.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			opts = append(opts, otlploggrpc.WithInsecure())
		}
		if cfg.Timeout > 0 {
			opts = append(opts, otlploggrpc.WithTimeout(cfg.Timeout))
		}
		if cfg.Compression == "gzip" {
			opts = append(opts, otlploggrpc.WithCompressor("gzip"))
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlploggrpc.WithHeaders(cfg.Headers))
		}
		if cfg.RetryEnabled {
			opts = append(opts, otlploggrpc.WithRetry(otlploggrpc.RetryConfig{
				Enabled:         true,
				InitialInterval: cfg.RetryInitial,
				MaxInterval:     cfg.RetryMaxInterval,
				MaxElapsedTime:  cfg.RetryMaxElapsed,
			}))
		}
		return otlploggrpc.New(ctx, opts...)
	}
}

//nolint:dupl // OTEL SDK uses distinct option types per exporter; structural similarity is unavoidable.
func newMetricExporter(ctx context.Context, cfg Config) (metric.Exporter, error) {
	switch cfg.Protocol {
	case "http":
		opts := []otlpmetrichttp.Option{
			otlpmetrichttp.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			opts = append(opts, otlpmetrichttp.WithInsecure())
		}
		if cfg.Timeout > 0 {
			opts = append(opts, otlpmetrichttp.WithTimeout(cfg.Timeout))
		}
		if cfg.Compression == "gzip" {
			opts = append(opts, otlpmetrichttp.WithCompression(otlpmetrichttp.GzipCompression))
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlpmetrichttp.WithHeaders(cfg.Headers))
		}
		if cfg.RetryEnabled {
			opts = append(opts, otlpmetrichttp.WithRetry(otlpmetrichttp.RetryConfig{
				Enabled:         true,
				InitialInterval: cfg.RetryInitial,
				MaxInterval:     cfg.RetryMaxInterval,
				MaxElapsedTime:  cfg.RetryMaxElapsed,
			}))
		}
		return otlpmetrichttp.New(ctx, opts...)
	default: // grpc
		opts := []otlpmetricgrpc.Option{
			otlpmetricgrpc.WithEndpoint(cfg.Endpoint),
		}
		if cfg.Insecure {
			opts = append(opts, otlpmetricgrpc.WithInsecure())
		}
		if cfg.Timeout > 0 {
			opts = append(opts, otlpmetricgrpc.WithTimeout(cfg.Timeout))
		}
		if cfg.Compression == "gzip" {
			opts = append(opts, otlpmetricgrpc.WithCompressor("gzip"))
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, otlpmetricgrpc.WithHeaders(cfg.Headers))
		}
		if cfg.RetryEnabled {
			opts = append(opts, otlpmetricgrpc.WithRetry(otlpmetricgrpc.RetryConfig{
				Enabled:         true,
				InitialInterval: cfg.RetryInitial,
				MaxInterval:     cfg.RetryMaxInterval,
				MaxElapsedTime:  cfg.RetryMaxElapsed,
			}))
		}
		return otlpmetricgrpc.New(ctx, opts...)
	}
}
