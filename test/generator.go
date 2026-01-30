package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
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

// GeneratorStats tracks metrics generation statistics
type GeneratorStats struct {
	StartTime              time.Time
	TotalMetricsSent       atomic.Int64
	TotalDatapointsSent    atomic.Int64
	TotalBytesSent         atomic.Int64
	TotalBatchesSent       atomic.Int64
	TotalErrors            atomic.Int64
	LastBatchTime          atomic.Int64 // Unix nano
	MinBatchLatency        atomic.Int64 // Nanoseconds
	MaxBatchLatency        atomic.Int64 // Nanoseconds
	TotalBatchLatency      atomic.Int64 // Nanoseconds (for average)

	// High cardinality tracking
	HighCardinalityMetrics atomic.Int64
	UniqueLabels           atomic.Int64

	// Burst tracking
	BurstsSent             atomic.Int64
	BurstMetricsSent       atomic.Int64
}

var stats = &GeneratorStats{}

func main() {
	endpoint := getEnv("OTLP_ENDPOINT", "localhost:4317")
	intervalStr := getEnv("METRICS_INTERVAL", "100ms")
	servicesStr := getEnv("SERVICES", "payment-api,order-api,inventory-api,user-api,auth-api,legacy-app")
	environmentsStr := getEnv("ENVIRONMENTS", "prod,staging,dev,qa")
	enableEdgeCases := getEnvBool("ENABLE_EDGE_CASES", true)
	enableHighCardinality := getEnvBool("ENABLE_HIGH_CARDINALITY", true)
	enableBurstTraffic := getEnvBool("ENABLE_BURST_TRAFFIC", true)
	highCardinalityCount := getEnvInt("HIGH_CARDINALITY_COUNT", 100)
	burstSize := getEnvInt("BURST_SIZE", 2000)
	burstIntervalSec := getEnvInt("BURST_INTERVAL_SEC", 15)
	statsIntervalSec := getEnvInt("STATS_INTERVAL_SEC", 10)
	enableStatsOutput := getEnvBool("ENABLE_STATS_OUTPUT", true)
	targetMetricsPerSec := getEnvInt("TARGET_METRICS_PER_SEC", 1000)
	targetDatapointsPerSec := getEnvInt("TARGET_DATAPOINTS_PER_SEC", 10000)

	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		log.Fatalf("Invalid interval: %v", err)
	}

	services := strings.Split(servicesStr, ",")
	environments := strings.Split(environmentsStr, ",")

	log.Printf("========================================")
	log.Printf("  METRICS GENERATOR STARTING")
	log.Printf("========================================")
	log.Printf("Configuration:")
	log.Printf("  Endpoint: %s", endpoint)
	log.Printf("  Interval: %s", interval)
	log.Printf("  Services: %v", services)
	log.Printf("  Environments: %v", environments)
	log.Printf("  Edge cases: %v", enableEdgeCases)
	log.Printf("  High cardinality: %v (count: %d)", enableHighCardinality, highCardinalityCount)
	log.Printf("  Burst traffic: %v (size: %d, interval: %ds)", enableBurstTraffic, burstSize, burstIntervalSec)
	log.Printf("  Stats output: %v (interval: %ds)", enableStatsOutput, statsIntervalSec)
	log.Printf("  Target metrics/sec: %d", targetMetricsPerSec)
	log.Printf("  Target datapoints/sec: %d", targetDatapointsPerSec)
	log.Printf("========================================")

	// Suppress unused variable warnings
	_ = targetMetricsPerSec
	_ = targetDatapointsPerSec

	ctx := context.Background()
	stats.StartTime = time.Now()

	// Wait for metrics-governor to be ready
	log.Println("Waiting for OTLP endpoint to be ready...")
	time.Sleep(5 * time.Second)

	// Setup OTLP exporter with larger message size
	conn, err := grpc.NewClient(endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallSendMsgSize(64*1024*1024), // 64MB
			grpc.MaxCallRecvMsgSize(64*1024*1024), // 64MB
		),
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

	// Verification metrics - these help track what was sent
	verificationCounter, _ := meter.Int64Counter("generator_verification_counter",
		metric.WithDescription("Counter for verifying data delivery"))

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

	// Stats output ticker
	var statsTicker *time.Ticker
	if enableStatsOutput {
		statsTicker = time.NewTicker(time.Duration(statsIntervalSec) * time.Second)
		defer statsTicker.Stop()
	}

	// Start Prometheus metrics endpoint
	metricsPort := getEnv("METRICS_PORT", "9091")
	go startMetricsServer(metricsPort)

	iteration := 0
	verificationID := int64(0)

	for {
		select {
		case <-ticker.C:
			batchStart := time.Now()
			iteration++
			batchMetrics := int64(0)
			batchDatapoints := int64(0)

			// Standard metrics generation
			for _, service := range services {
				for _, env := range environments {
					// Generate HTTP request metrics - increased for higher throughput
					numRequests := rand.Intn(50) + 25
					for i := 0; i < numRequests; i++ {
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
						batchMetrics += 2
						batchDatapoints += 2
					}

					// Generate active connections
					activeConnections.Add(ctx, int64(rand.Intn(10)-5),
						metric.WithAttributes(
							attribute.String("service", service),
							attribute.String("env", env),
						))
					batchMetrics++
					batchDatapoints++

					// Generate high-cardinality legacy metrics (to test limits)
					if service == "legacy-app" || strings.HasPrefix(service, "legacy") {
						numLegacy := rand.Intn(50) + 10
						for i := 0; i < numLegacy; i++ {
							reqID := fmt.Sprintf("req-%s-%d-%d", service, iteration, i)
							legacyAppRequestCount.Add(ctx, 1,
								metric.WithAttributes(
									attribute.String("service", service),
									attribute.String("env", env),
									attribute.String("request_id", reqID),
								))
							batchDatapoints++
						}
						batchMetrics++
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
					batchDatapoints++
				}
				batchMetrics++

				// Counter edge cases (only positive values for counters)
				for i := 0; i < 5; i++ {
					edgeCaseCounter.Add(ctx, int64(math.Abs(edgeValues[i%len(edgeValues)])*1000),
						metric.WithAttributes(
							attribute.String("edge_type", fmt.Sprintf("counter_type_%d", i)),
						))
					batchDatapoints++
				}
				batchMetrics++

				// Many datapoints histogram
				for i := 0; i < 100; i++ {
					manyDatapointsHistogram.Record(ctx, rand.Float64()*10,
						metric.WithAttributes(
							attribute.String("source", "edge_test"),
						))
					batchDatapoints++
				}
				batchMetrics++
			}

			// High cardinality metrics
			if enableHighCardinality {
				for i := 0; i < highCardinalityCount; i++ {
					highCardinalityMetric.Add(ctx, 1,
						metric.WithAttributes(
							attribute.String("user_id", fmt.Sprintf("user_%d", rand.Intn(10000))),
							attribute.String("session_id", fmt.Sprintf("sess_%d_%d", iteration, i)),
							attribute.String("request_path", fmt.Sprintf("/api/v%d/resource/%d", rand.Intn(5), rand.Intn(1000))),
							attribute.String("region", fmt.Sprintf("region_%d", rand.Intn(20))),
							attribute.String("instance", fmt.Sprintf("instance_%d", rand.Intn(50))),
						))
					batchDatapoints++
				}
				batchMetrics++
				stats.HighCardinalityMetrics.Add(int64(highCardinalityCount))
				stats.UniqueLabels.Add(int64(highCardinalityCount)) // Approximate
			}

			// Verification counter - increment with known value
			verificationID++
			verificationCounter.Add(ctx, verificationID,
				metric.WithAttributes(
					attribute.Int64("batch_id", int64(iteration)),
					attribute.Int64("verification_id", verificationID),
				))
			batchMetrics++
			batchDatapoints++

			// Update stats
			batchLatency := time.Since(batchStart).Nanoseconds()
			stats.TotalMetricsSent.Add(batchMetrics)
			stats.TotalDatapointsSent.Add(batchDatapoints)
			stats.TotalBatchesSent.Add(1)
			stats.LastBatchTime.Store(time.Now().UnixNano())
			stats.TotalBatchLatency.Add(batchLatency)

			// Update min/max latency
			for {
				oldMin := stats.MinBatchLatency.Load()
				if oldMin != 0 && oldMin <= batchLatency {
					break
				}
				if stats.MinBatchLatency.CompareAndSwap(oldMin, batchLatency) {
					break
				}
			}
			for {
				oldMax := stats.MaxBatchLatency.Load()
				if oldMax >= batchLatency {
					break
				}
				if stats.MaxBatchLatency.CompareAndSwap(oldMax, batchLatency) {
					break
				}
			}

			if iteration%10 == 0 {
				log.Printf("Generated %d iterations of metrics", iteration)
			}

		case <-burstTicker.C:
			if enableBurstTraffic {
				burstStart := time.Now()
				log.Printf("Generating burst traffic: %d metrics", burstSize)

				for i := 0; i < burstSize; i++ {
					burstMetric.Add(ctx, 1,
						metric.WithAttributes(
							attribute.String("burst_id", fmt.Sprintf("burst_%d", iteration)),
							attribute.String("index", strconv.Itoa(i)),
							attribute.String("service", services[rand.Intn(len(services))]),
						))
				}

				burstDuration := time.Since(burstStart)
				stats.BurstsSent.Add(1)
				stats.BurstMetricsSent.Add(int64(burstSize))
				stats.TotalDatapointsSent.Add(int64(burstSize))

				log.Printf("Burst complete in %v (%.0f metrics/sec)", burstDuration, float64(burstSize)/burstDuration.Seconds())
			}

		case <-func() <-chan time.Time {
			if statsTicker != nil {
				return statsTicker.C
			}
			return nil
		}():
			printStats()
		}
	}
}

func printStats() {
	elapsed := time.Since(stats.StartTime)
	totalMetrics := stats.TotalMetricsSent.Load()
	totalDatapoints := stats.TotalDatapointsSent.Load()
	totalBatches := stats.TotalBatchesSent.Load()
	totalErrors := stats.TotalErrors.Load()
	highCardMetrics := stats.HighCardinalityMetrics.Load()
	bursts := stats.BurstsSent.Load()
	burstMetrics := stats.BurstMetricsSent.Load()

	avgLatency := float64(0)
	if totalBatches > 0 {
		avgLatency = float64(stats.TotalBatchLatency.Load()) / float64(totalBatches) / 1e6 // Convert to ms
	}

	metricsPerSec := float64(totalMetrics) / elapsed.Seconds()
	datapointsPerSec := float64(totalDatapoints) / elapsed.Seconds()

	log.Printf("========================================")
	log.Printf("  GENERATOR STATISTICS")
	log.Printf("========================================")
	log.Printf("Runtime: %s", elapsed.Round(time.Second))
	log.Printf("")
	log.Printf("THROUGHPUT:")
	log.Printf("  Total metrics sent:     %d", totalMetrics)
	log.Printf("  Total datapoints sent:  %d", totalDatapoints)
	log.Printf("  Metrics/sec:            %.2f", metricsPerSec)
	log.Printf("  Datapoints/sec:         %.2f", datapointsPerSec)
	log.Printf("")
	log.Printf("BATCHES:")
	log.Printf("  Total batches:          %d", totalBatches)
	log.Printf("  Avg batch latency:      %.2f ms", avgLatency)
	log.Printf("  Min batch latency:      %.2f ms", float64(stats.MinBatchLatency.Load())/1e6)
	log.Printf("  Max batch latency:      %.2f ms", float64(stats.MaxBatchLatency.Load())/1e6)
	log.Printf("")
	log.Printf("HIGH CARDINALITY:")
	log.Printf("  High-card metrics:      %d", highCardMetrics)
	log.Printf("  Unique labels (approx): %d", stats.UniqueLabels.Load())
	log.Printf("")
	log.Printf("BURST TRAFFIC:")
	log.Printf("  Bursts sent:            %d", bursts)
	log.Printf("  Burst metrics total:    %d", burstMetrics)
	log.Printf("")
	log.Printf("ERRORS:")
	log.Printf("  Total errors:           %d", totalErrors)
	log.Printf("  Error rate:             %.4f%%", float64(totalErrors)/float64(totalBatches)*100)
	log.Printf("========================================")
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

// startMetricsServer starts an HTTP server for Prometheus metrics
func startMetricsServer(port string) {
	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		elapsed := time.Since(stats.StartTime).Seconds()
		totalMetrics := stats.TotalMetricsSent.Load()
		totalDatapoints := stats.TotalDatapointsSent.Load()
		totalBatches := stats.TotalBatchesSent.Load()
		totalErrors := stats.TotalErrors.Load()
		highCardMetrics := stats.HighCardinalityMetrics.Load()
		bursts := stats.BurstsSent.Load()
		burstMetrics := stats.BurstMetricsSent.Load()

		avgLatency := float64(0)
		if totalBatches > 0 {
			avgLatency = float64(stats.TotalBatchLatency.Load()) / float64(totalBatches) / 1e9 // Convert to seconds
		}

		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		// Runtime
		fmt.Fprintf(w, "# HELP generator_runtime_seconds Total runtime of the generator\n")
		fmt.Fprintf(w, "# TYPE generator_runtime_seconds counter\n")
		fmt.Fprintf(w, "generator_runtime_seconds %.3f\n", elapsed)

		// Throughput metrics
		fmt.Fprintf(w, "# HELP generator_metrics_sent_total Total number of metrics sent\n")
		fmt.Fprintf(w, "# TYPE generator_metrics_sent_total counter\n")
		fmt.Fprintf(w, "generator_metrics_sent_total %d\n", totalMetrics)

		fmt.Fprintf(w, "# HELP generator_datapoints_sent_total Total number of datapoints sent\n")
		fmt.Fprintf(w, "# TYPE generator_datapoints_sent_total counter\n")
		fmt.Fprintf(w, "generator_datapoints_sent_total %d\n", totalDatapoints)

		// Batch metrics
		fmt.Fprintf(w, "# HELP generator_batches_sent_total Total number of batches sent\n")
		fmt.Fprintf(w, "# TYPE generator_batches_sent_total counter\n")
		fmt.Fprintf(w, "generator_batches_sent_total %d\n", totalBatches)

		fmt.Fprintf(w, "# HELP generator_batch_latency_avg_seconds Average batch latency\n")
		fmt.Fprintf(w, "# TYPE generator_batch_latency_avg_seconds gauge\n")
		fmt.Fprintf(w, "generator_batch_latency_avg_seconds %.6f\n", avgLatency)

		fmt.Fprintf(w, "# HELP generator_batch_latency_min_seconds Minimum batch latency\n")
		fmt.Fprintf(w, "# TYPE generator_batch_latency_min_seconds gauge\n")
		fmt.Fprintf(w, "generator_batch_latency_min_seconds %.6f\n", float64(stats.MinBatchLatency.Load())/1e9)

		fmt.Fprintf(w, "# HELP generator_batch_latency_max_seconds Maximum batch latency\n")
		fmt.Fprintf(w, "# TYPE generator_batch_latency_max_seconds gauge\n")
		fmt.Fprintf(w, "generator_batch_latency_max_seconds %.6f\n", float64(stats.MaxBatchLatency.Load())/1e9)

		// High cardinality metrics
		fmt.Fprintf(w, "# HELP generator_high_cardinality_metrics_total Total high cardinality metrics generated\n")
		fmt.Fprintf(w, "# TYPE generator_high_cardinality_metrics_total counter\n")
		fmt.Fprintf(w, "generator_high_cardinality_metrics_total %d\n", highCardMetrics)

		fmt.Fprintf(w, "# HELP generator_unique_labels_total Approximate unique label combinations\n")
		fmt.Fprintf(w, "# TYPE generator_unique_labels_total counter\n")
		fmt.Fprintf(w, "generator_unique_labels_total %d\n", stats.UniqueLabels.Load())

		// Burst metrics
		fmt.Fprintf(w, "# HELP generator_bursts_sent_total Total number of bursts sent\n")
		fmt.Fprintf(w, "# TYPE generator_bursts_sent_total counter\n")
		fmt.Fprintf(w, "generator_bursts_sent_total %d\n", bursts)

		fmt.Fprintf(w, "# HELP generator_burst_metrics_total Total metrics sent in bursts\n")
		fmt.Fprintf(w, "# TYPE generator_burst_metrics_total counter\n")
		fmt.Fprintf(w, "generator_burst_metrics_total %d\n", burstMetrics)

		// Error metrics
		fmt.Fprintf(w, "# HELP generator_errors_total Total number of errors\n")
		fmt.Fprintf(w, "# TYPE generator_errors_total counter\n")
		fmt.Fprintf(w, "generator_errors_total %d\n", totalErrors)

		// Rate metrics (calculated)
		if elapsed > 0 {
			fmt.Fprintf(w, "# HELP generator_metrics_per_second Current metrics rate\n")
			fmt.Fprintf(w, "# TYPE generator_metrics_per_second gauge\n")
			fmt.Fprintf(w, "generator_metrics_per_second %.2f\n", float64(totalMetrics)/elapsed)

			fmt.Fprintf(w, "# HELP generator_datapoints_per_second Current datapoints rate\n")
			fmt.Fprintf(w, "# TYPE generator_datapoints_per_second gauge\n")
			fmt.Fprintf(w, "generator_datapoints_per_second %.2f\n", float64(totalDatapoints)/elapsed)
		}
	})

	log.Printf("Starting metrics server on :%s/metrics", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Printf("Metrics server error: %v", err)
	}
}
