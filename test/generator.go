package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"strconv"
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
	enableEdgeCases := getEnvBool("ENABLE_EDGE_CASES", true)
	enableHighCardinality := getEnvBool("ENABLE_HIGH_CARDINALITY", true)
	enableBurstTraffic := getEnvBool("ENABLE_BURST_TRAFFIC", true)
	highCardinalityCount := getEnvInt("HIGH_CARDINALITY_COUNT", 100)
	burstSize := getEnvInt("BURST_SIZE", 1000)
	burstIntervalSec := getEnvInt("BURST_INTERVAL_SEC", 30)

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
	log.Printf("  Edge cases: %v", enableEdgeCases)
	log.Printf("  High cardinality: %v (count: %d)", enableHighCardinality, highCardinalityCount)
	log.Printf("  Burst traffic: %v (size: %d, interval: %ds)", enableBurstTraffic, burstSize, burstIntervalSec)

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

	// Standard metrics
	httpRequestsTotal, _ := meter.Int64Counter("http_requests_total",
		metric.WithDescription("Total HTTP requests"))
	httpRequestDuration, _ := meter.Float64Histogram("http_request_duration_seconds",
		metric.WithDescription("HTTP request duration in seconds"))
	activeConnections, _ := meter.Int64UpDownCounter("active_connections",
		metric.WithDescription("Number of active connections"))
	legacyAppRequestCount, _ := meter.Int64Counter("legacy_app_request_count",
		metric.WithDescription("Legacy app request count - high cardinality"))

	// Edge case metrics
	edgeCaseGauge, _ := meter.Float64Gauge("edge_case_gauge",
		metric.WithDescription("Gauge with edge case values"))
	edgeCaseCounter, _ := meter.Int64Counter("edge_case_counter",
		metric.WithDescription("Counter with edge case values"))

	// High cardinality metrics
	highCardinalityMetric, _ := meter.Int64Counter("high_cardinality_metric",
		metric.WithDescription("Metric with many unique label combinations"))

	// Burst traffic metric
	burstMetric, _ := meter.Int64Counter("burst_traffic_metric",
		metric.WithDescription("Metric for burst traffic testing"))

	// Many datapoints metric (histogram with many buckets)
	manyDatapointsHistogram, _ := meter.Float64Histogram("many_datapoints_histogram",
		metric.WithDescription("Histogram with many datapoints"),
		metric.WithExplicitBucketBoundaries(0.001, 0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1.0, 2.5, 5.0, 7.5, 10.0))

	methods := []string{"GET", "POST", "PUT", "DELETE"}
	endpoints := []string{"/api/users", "/api/orders", "/api/products", "/api/payments", "/api/inventory"}
	statuses := []string{"200", "201", "400", "404", "500"}

	// Edge case values for testing
	edgeValues := []float64{
		0.0,                   // Zero
		-1.0,                  // Negative (for gauges)
		math.MaxFloat64 / 2,   // Very large positive
		-math.MaxFloat64 / 2,  // Very large negative
		0.0000001,             // Very small positive
		-0.0000001,            // Very small negative
		1e10,                  // Scientific notation large
		1e-10,                 // Scientific notation small
		math.Pi,               // Irrational number
		math.E,                // Euler's number
	}

	log.Println("Starting to generate metrics...")

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	burstTicker := time.NewTicker(time.Duration(burstIntervalSec) * time.Second)
	defer burstTicker.Stop()

	iteration := 0
	for {
		select {
		case <-ticker.C:
			iteration++

			// Standard metrics generation
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

			// Edge case values
			if enableEdgeCases {
				for i, val := range edgeValues {
					edgeCaseGauge.Record(ctx, val,
						metric.WithAttributes(
							attribute.String("edge_type", fmt.Sprintf("type_%d", i)),
							attribute.String("iteration", strconv.Itoa(iteration)),
						))
				}

				// Counter edge cases (only positive values for counters)
				for i := 0; i < 5; i++ {
					edgeCaseCounter.Add(ctx, int64(math.Abs(edgeValues[i%len(edgeValues)])*1000),
						metric.WithAttributes(
							attribute.String("edge_type", fmt.Sprintf("counter_type_%d", i)),
						))
				}

				// Many datapoints histogram
				for i := 0; i < 100; i++ {
					manyDatapointsHistogram.Record(ctx, rand.Float64()*10,
						metric.WithAttributes(
							attribute.String("source", "edge_test"),
						))
				}
			}

			// High cardinality metrics
			if enableHighCardinality {
				for i := 0; i < highCardinalityCount; i++ {
					// Create unique label combinations
					highCardinalityMetric.Add(ctx, 1,
						metric.WithAttributes(
							attribute.String("user_id", fmt.Sprintf("user_%d", rand.Intn(10000))),
							attribute.String("session_id", fmt.Sprintf("sess_%d_%d", iteration, i)),
							attribute.String("request_path", fmt.Sprintf("/api/v%d/resource/%d", rand.Intn(5), rand.Intn(1000))),
							attribute.String("region", fmt.Sprintf("region_%d", rand.Intn(20))),
							attribute.String("instance", fmt.Sprintf("instance_%d", rand.Intn(50))),
						))
				}
			}

			if iteration%10 == 0 {
				log.Printf("Generated %d iterations of metrics", iteration)
			}

		case <-burstTicker.C:
			if enableBurstTraffic {
				log.Printf("Generating burst traffic: %d metrics", burstSize)
				start := time.Now()

				for i := 0; i < burstSize; i++ {
					burstMetric.Add(ctx, 1,
						metric.WithAttributes(
							attribute.String("burst_id", fmt.Sprintf("burst_%d", iteration)),
							attribute.String("index", strconv.Itoa(i)),
							attribute.String("service", services[rand.Intn(len(services))]),
						))
				}

				log.Printf("Burst complete in %v", time.Since(start))
			}
		}
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		b, err := strconv.ParseBool(value)
		if err != nil {
			return defaultValue
		}
		return b
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		i, err := strconv.Atoi(value)
		if err != nil {
			return defaultValue
		}
		return i
	}
	return defaultValue
}
