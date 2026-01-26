package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	endpoint := getEnv("OTLP_ENDPOINT", "localhost:4317")
	intervalStr := getEnv("METRICS_INTERVAL", "1s")
	servicesStr := getEnv("SERVICES", "payment-api,order-api,inventory-api")
	environmentsStr := getEnv("ENVIRONMENTS", "prod,staging,dev")

	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		log.Fatalf("Invalid interval: %v", err)
	}

	services := strings.Split(servicesStr, ",")
	environments := strings.Split(environmentsStr, ",")

	log.Printf("Starting metrics generator")
	log.Printf("  Endpoint: %s", endpoint)
	log.Printf("  Interval: %s", interval)
	log.Printf("  Services: %v", services)
	log.Printf("  Environments: %v", environments)

	ctx := context.Background()

	// Wait for metrics-governor to be ready
	log.Println("Waiting for OTLP endpoint to be ready...")
	time.Sleep(5 * time.Second)

	// Setup OTLP exporter
	conn, err := grpc.NewClient(endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("Failed to create gRPC connection: %v", err)
	}
	defer conn.Close()

	exporter, err := otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithGRPCConn(conn))
	if err != nil {
		log.Fatalf("Failed to create exporter: %v", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName("metrics-generator"),
			semconv.ServiceVersion("1.0.0"),
		),
	)
	if err != nil {
		log.Fatalf("Failed to create resource: %v", err)
	}

	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(interval))),
		sdkmetric.WithResource(res),
	)
	defer meterProvider.Shutdown(ctx)

	otel.SetMeterProvider(meterProvider)

	meter := otel.Meter("test-generator")

	// Create metrics
	httpRequestsTotal, _ := meter.Int64Counter("http_requests_total",
		metric.WithDescription("Total HTTP requests"))
	httpRequestDuration, _ := meter.Float64Histogram("http_request_duration_seconds",
		metric.WithDescription("HTTP request duration in seconds"))
	activeConnections, _ := meter.Int64UpDownCounter("active_connections",
		metric.WithDescription("Number of active connections"))
	legacyAppRequestCount, _ := meter.Int64Counter("legacy_app_request_count",
		metric.WithDescription("Legacy app request count - high cardinality"))

	methods := []string{"GET", "POST", "PUT", "DELETE"}
	endpoints := []string{"/api/users", "/api/orders", "/api/products", "/api/payments", "/api/inventory"}
	statuses := []string{"200", "201", "400", "404", "500"}

	log.Println("Starting to generate metrics...")

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	iteration := 0
	for range ticker.C {
		iteration++

		for _, service := range services {
			for _, env := range environments {
				// Generate HTTP request metrics
				for i := 0; i < rand.Intn(10)+5; i++ {
					method := methods[rand.Intn(len(methods))]
					endpoint := endpoints[rand.Intn(len(endpoints))]
					status := statuses[rand.Intn(len(statuses))]

					attrs := []attribute.KeyValue{
						attribute.String("service", service),
						attribute.String("env", env),
						attribute.String("method", method),
						attribute.String("endpoint", endpoint),
						attribute.String("status", status),
					}

					httpRequestsTotal.Add(ctx, 1, metric.WithAttributes(attrs...))
					httpRequestDuration.Record(ctx, rand.Float64()*0.5, metric.WithAttributes(attrs...))
				}

				// Generate active connections
				activeConnections.Add(ctx, int64(rand.Intn(10)-5),
					metric.WithAttributes(
						attribute.String("service", service),
						attribute.String("env", env),
					))

				// Generate high-cardinality legacy metrics (to test limits)
				if service == "legacy-app" || strings.HasPrefix(service, "legacy") {
					for i := 0; i < rand.Intn(50)+10; i++ {
						// High cardinality: unique request ID
						reqID := fmt.Sprintf("req-%s-%d-%d", service, iteration, i)
						legacyAppRequestCount.Add(ctx, 1,
							metric.WithAttributes(
								attribute.String("service", service),
								attribute.String("env", env),
								attribute.String("request_id", reqID),
							))
					}
				}
			}
		}

		if iteration%10 == 0 {
			log.Printf("Generated %d iterations of metrics", iteration)
		}
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
