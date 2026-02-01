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
	StartTime           time.Time
	TotalMetricsSent    atomic.Int64
	TotalDatapointsSent atomic.Int64
	TotalBytesSent      atomic.Int64
	TotalBatchesSent    atomic.Int64
	TotalErrors         atomic.Int64
	LastBatchTime       atomic.Int64 // Unix nano
	MinBatchLatency     atomic.Int64 // Nanoseconds
	MaxBatchLatency     atomic.Int64 // Nanoseconds
	TotalBatchLatency   atomic.Int64 // Nanoseconds (for average)

	// High cardinality tracking
	HighCardinalityMetrics atomic.Int64
	UniqueLabels           atomic.Int64

	// Burst tracking
	BurstsSent       atomic.Int64
	BurstMetricsSent atomic.Int64
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
	enableDiverseMetrics := getEnvBool("ENABLE_DIVERSE_METRICS", true)
	highCardinalityCount := getEnvInt("HIGH_CARDINALITY_COUNT", 100)
	diverseMetricCount := getEnvInt("DIVERSE_METRIC_COUNT", 200) // Number of unique metric names to generate
	burstSize := getEnvInt("BURST_SIZE", 2000)
	burstIntervalSec := getEnvInt("BURST_INTERVAL_SEC", 15)
	statsIntervalSec := getEnvInt("STATS_INTERVAL_SEC", 10)
	enableStatsOutput := getEnvBool("ENABLE_STATS_OUTPUT", true)
	targetMetricsPerSec := getEnvInt("TARGET_METRICS_PER_SEC", 1000)
	targetDatapointsPerSec := getEnvInt("TARGET_DATAPOINTS_PER_SEC", 10000)

	// Stable mode configuration - for predictable testing
	enableStableMode := getEnvBool("ENABLE_STABLE_MODE", false)
	stableMetricCount := getEnvInt("STABLE_METRIC_COUNT", 100)        // Number of stable metrics
	stableCardinalityPerMetric := getEnvInt("STABLE_CARDINALITY", 10) // Series per metric
	stableDatapointsPerInterval := getEnvInt("STABLE_DATAPOINTS", 1)  // Datapoints per series per interval

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
	log.Printf("  Diverse metrics: %v (count: %d)", enableDiverseMetrics, diverseMetricCount)
	log.Printf("  Burst traffic: %v (size: %d, interval: %ds)", enableBurstTraffic, burstSize, burstIntervalSec)
	log.Printf("  Stats output: %v (interval: %ds)", enableStatsOutput, statsIntervalSec)
	log.Printf("  Target metrics/sec: %d", targetMetricsPerSec)
	log.Printf("  Target datapoints/sec: %d", targetDatapointsPerSec)
	if enableStableMode {
		log.Printf("  STABLE MODE ENABLED:")
		log.Printf("    Standard metrics: DISABLED (stable only)")
		log.Printf("    Stable metrics: %d", stableMetricCount)
		log.Printf("    Series per metric: %d", stableCardinalityPerMetric)
		totalSeries := stableMetricCount * stableCardinalityPerMetric
		log.Printf("    Total unique series: %d", totalSeries)
		log.Printf("    NOTE: OTel SDK uses cumulative aggregation")
		log.Printf("    Expected OTLP datapoints/export: %d (1 per series)", totalSeries)
	}
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

	// High cardinality metrics - multiple metrics with different cardinality profiles
	highCardinalityMetric, _ := meter.Int64Counter("high_cardinality_metric",
		metric.WithDescription("Metric with many unique label combinations"))

	// Additional high cardinality metrics for testing limits with different patterns
	highCardUserEvents, _ := meter.Int64Counter("high_card_user_events",
		metric.WithDescription("User events with high user_id cardinality"))
	highCardAPIRequests, _ := meter.Int64Counter("high_card_api_requests",
		metric.WithDescription("API requests with high endpoint cardinality"))
	highCardDBQueries, _ := meter.Int64Counter("high_card_db_queries",
		metric.WithDescription("DB queries with high query_hash cardinality"))
	highCardCacheOps, _ := meter.Int64Counter("high_card_cache_operations",
		metric.WithDescription("Cache operations with high cache_key cardinality"))
	highCardHTTPPaths, _ := meter.Float64Histogram("high_card_http_duration",
		metric.WithDescription("HTTP request duration with high path cardinality"),
		metric.WithExplicitBucketBoundaries(0.01, 0.05, 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0))

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

	// Diverse metrics - many unique metric names for testing VM storage
	// Infrastructure metrics (CPU, memory, disk, network)
	var diverseCounters []metric.Int64Counter
	var diverseGauges []metric.Float64Gauge
	var diverseHistograms []metric.Float64Histogram

	if enableDiverseMetrics {
		// CPU metrics per core (simulating multi-core systems)
		cpuMetrics := []string{
			"cpu_user_percent", "cpu_system_percent", "cpu_idle_percent",
			"cpu_iowait_percent", "cpu_irq_percent", "cpu_softirq_percent",
			"cpu_steal_percent", "cpu_guest_percent", "cpu_nice_percent",
		}
		for _, name := range cpuMetrics {
			g, _ := meter.Float64Gauge("node_"+name, metric.WithDescription("CPU metric: "+name))
			diverseGauges = append(diverseGauges, g)
		}

		// Memory metrics
		memMetrics := []string{
			"memory_total_bytes", "memory_free_bytes", "memory_available_bytes",
			"memory_buffers_bytes", "memory_cached_bytes", "memory_swap_total_bytes",
			"memory_swap_free_bytes", "memory_swap_used_bytes", "memory_dirty_bytes",
			"memory_writeback_bytes", "memory_mapped_bytes", "memory_shmem_bytes",
			"memory_slab_bytes", "memory_page_tables_bytes", "memory_commit_limit_bytes",
		}
		for _, name := range memMetrics {
			g, _ := meter.Float64Gauge("node_"+name, metric.WithDescription("Memory metric: "+name))
			diverseGauges = append(diverseGauges, g)
		}

		// Disk metrics per device
		diskMetrics := []string{
			"disk_read_bytes_total", "disk_written_bytes_total",
			"disk_read_time_seconds_total", "disk_write_time_seconds_total",
			"disk_io_time_seconds_total", "disk_reads_completed_total",
			"disk_writes_completed_total", "disk_io_now", "disk_discards_completed_total",
		}
		for _, name := range diskMetrics {
			c, _ := meter.Int64Counter("node_"+name, metric.WithDescription("Disk metric: "+name))
			diverseCounters = append(diverseCounters, c)
		}

		// Filesystem metrics
		fsMetrics := []string{
			"filesystem_size_bytes", "filesystem_free_bytes", "filesystem_avail_bytes",
			"filesystem_files", "filesystem_files_free",
		}
		for _, name := range fsMetrics {
			g, _ := meter.Float64Gauge("node_"+name, metric.WithDescription("Filesystem metric: "+name))
			diverseGauges = append(diverseGauges, g)
		}

		// Network metrics per interface
		netMetrics := []string{
			"network_receive_bytes_total", "network_transmit_bytes_total",
			"network_receive_packets_total", "network_transmit_packets_total",
			"network_receive_errs_total", "network_transmit_errs_total",
			"network_receive_drop_total", "network_transmit_drop_total",
			"network_receive_fifo_total", "network_transmit_fifo_total",
			"network_receive_multicast_total", "network_receive_compressed_total",
		}
		for _, name := range netMetrics {
			c, _ := meter.Int64Counter("node_"+name, metric.WithDescription("Network metric: "+name))
			diverseCounters = append(diverseCounters, c)
		}

		// Process metrics
		procMetrics := []string{
			"process_cpu_seconds_total", "process_virtual_memory_bytes",
			"process_resident_memory_bytes", "process_start_time_seconds",
			"process_open_fds", "process_max_fds", "process_threads",
		}
		for _, name := range procMetrics {
			c, _ := meter.Int64Counter(name, metric.WithDescription("Process metric: "+name))
			diverseCounters = append(diverseCounters, c)
		}

		// Application metrics (simulating various microservices)
		appMetrics := []string{
			"db_connections_active", "db_connections_idle", "db_connections_max",
			"db_query_duration_seconds", "db_query_rows_affected",
			"cache_hits_total", "cache_misses_total", "cache_evictions_total",
			"cache_size_bytes", "cache_items",
			"queue_length", "queue_oldest_message_age_seconds",
			"queue_messages_published_total", "queue_messages_consumed_total",
			"thread_pool_active", "thread_pool_idle", "thread_pool_size",
			"gc_pause_seconds", "gc_collections_total", "gc_heap_bytes",
		}
		for _, name := range appMetrics {
			c, _ := meter.Int64Counter("app_"+name, metric.WithDescription("Application metric: "+name))
			diverseCounters = append(diverseCounters, c)
		}

		// HTTP endpoint metrics (different paths)
		httpEndpoints := []string{
			"/health", "/ready", "/metrics", "/api/v1/users", "/api/v1/orders",
			"/api/v1/products", "/api/v1/payments", "/api/v1/inventory",
			"/api/v1/auth/login", "/api/v1/auth/logout", "/api/v1/auth/refresh",
			"/api/v2/users", "/api/v2/orders", "/api/v2/products",
			"/graphql", "/ws/events", "/sse/updates",
		}
		for i, endpoint := range httpEndpoints {
			h, _ := meter.Float64Histogram(fmt.Sprintf("endpoint_%d_duration_seconds", i),
				metric.WithDescription("Request duration for "+endpoint))
			diverseHistograms = append(diverseHistograms, h)
			c, _ := meter.Int64Counter(fmt.Sprintf("endpoint_%d_requests_total", i),
				metric.WithDescription("Request count for "+endpoint))
			diverseCounters = append(diverseCounters, c)
		}

		// Generate additional unique metrics up to diverseMetricCount
		currentCount := len(diverseCounters) + len(diverseGauges) + len(diverseHistograms)
		for i := currentCount; i < diverseMetricCount; i++ {
			metricType := i % 3
			switch metricType {
			case 0:
				c, _ := meter.Int64Counter(fmt.Sprintf("custom_counter_%d", i),
					metric.WithDescription(fmt.Sprintf("Custom counter metric %d", i)))
				diverseCounters = append(diverseCounters, c)
			case 1:
				g, _ := meter.Float64Gauge(fmt.Sprintf("custom_gauge_%d", i),
					metric.WithDescription(fmt.Sprintf("Custom gauge metric %d", i)))
				diverseGauges = append(diverseGauges, g)
			case 2:
				h, _ := meter.Float64Histogram(fmt.Sprintf("custom_histogram_%d", i),
					metric.WithDescription(fmt.Sprintf("Custom histogram metric %d", i)))
				diverseHistograms = append(diverseHistograms, h)
			}
		}

		log.Printf("Created %d diverse counters, %d gauges, %d histograms (total: %d unique metric names)",
			len(diverseCounters), len(diverseGauges), len(diverseHistograms),
			len(diverseCounters)+len(diverseGauges)+len(diverseHistograms))
	}

	// Stable metrics - predictable counters for testing
	var stableCounters []metric.Int64Counter
	if enableStableMode {
		for i := 0; i < stableMetricCount; i++ {
			c, _ := meter.Int64Counter(fmt.Sprintf("stable_metric_%03d", i),
				metric.WithDescription(fmt.Sprintf("Stable test metric %d with fixed cardinality", i)))
			stableCounters = append(stableCounters, c)
		}
		log.Printf("Created %d stable counters with %d series each = %d total series",
			stableMetricCount, stableCardinalityPerMetric, stableMetricCount*stableCardinalityPerMetric)
	}

	methods := []string{"GET", "POST", "PUT", "DELETE"}
	endpoints := []string{"/api/users", "/api/orders", "/api/products", "/api/payments", "/api/inventory"}
	statuses := []string{"200", "201", "400", "404", "500"}

	// Edge case values for testing
	edgeValues := []float64{
		0.0,                  // Zero
		-1.0,                 // Negative (for gauges)
		math.MaxFloat64 / 2,  // Very large positive
		-math.MaxFloat64 / 2, // Very large negative
		0.0000001,            // Very small positive
		-0.0000001,           // Very small negative
		1e10,                 // Scientific notation large
		1e-10,                // Scientific notation small
		math.Pi,              // Irrational number
		math.E,               // Euler's number
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

			// Standard metrics generation (skip in stable mode for predictability)
			if !enableStableMode {
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
								// Use bounded request_id pool to prevent unbounded cardinality
								reqID := fmt.Sprintf("req-%s-%d", service, rand.Intn(1000))
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
			} // end if !enableStableMode

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
							// Use very small bounded pools so cardinality stabilizes quickly
							// Max combinations: 10 * 20 * 5 * 5 * 5 = 2,500 (stabilizes in seconds)
							attribute.String("user_id", fmt.Sprintf("user_%d", rand.Intn(10))),
							attribute.String("session_id", fmt.Sprintf("sess_%d", rand.Intn(20))),
							attribute.String("request_path", fmt.Sprintf("/api/v%d/resource/%d", rand.Intn(1)+1, rand.Intn(5))),
							attribute.String("region", fmt.Sprintf("region_%d", rand.Intn(5))),
							attribute.String("instance", fmt.Sprintf("instance_%d", rand.Intn(5))),
						))
					batchDatapoints++
				}
				batchMetrics++
				stats.HighCardinalityMetrics.Add(int64(highCardinalityCount))
				stats.UniqueLabels.Add(int64(highCardinalityCount)) // Approximate

				// Additional high cardinality metrics with different cardinality profiles
				// These test different limit scenarios:

				// 1. User events - high user_id cardinality (1000 users x 5 event types x 4 services = 20,000)
				for i := 0; i < highCardinalityCount/2; i++ {
					highCardUserEvents.Add(ctx, 1,
						metric.WithAttributes(
							attribute.String("service", services[rand.Intn(len(services))]),
							attribute.String("user_id", fmt.Sprintf("uid_%d", rand.Intn(1000))),
							attribute.String("event_type", fmt.Sprintf("event_%d", rand.Intn(5))),
						))
					batchDatapoints++
				}
				batchMetrics++

				// 2. API requests - high endpoint cardinality (200 paths x 4 methods x 5 status = 4,000)
				for i := 0; i < highCardinalityCount/2; i++ {
					highCardAPIRequests.Add(ctx, 1,
						metric.WithAttributes(
							attribute.String("service", services[rand.Intn(len(services))]),
							attribute.String("path", fmt.Sprintf("/api/v%d/entity/%d/action/%d", rand.Intn(3)+1, rand.Intn(50), rand.Intn(10))),
							attribute.String("method", methods[rand.Intn(len(methods))]),
							attribute.String("status", statuses[rand.Intn(len(statuses))]),
						))
					batchDatapoints++
				}
				batchMetrics++

				// 3. DB queries - high query_hash cardinality (500 hashes x 4 services = 2,000)
				for i := 0; i < highCardinalityCount/4; i++ {
					highCardDBQueries.Add(ctx, 1,
						metric.WithAttributes(
							attribute.String("service", services[rand.Intn(len(services))]),
							attribute.String("query_hash", fmt.Sprintf("qh_%08x", rand.Intn(500))),
							attribute.String("database", fmt.Sprintf("db_%d", rand.Intn(3))),
							attribute.String("operation", []string{"SELECT", "INSERT", "UPDATE", "DELETE"}[rand.Intn(4)]),
						))
					batchDatapoints++
				}
				batchMetrics++

				// 4. Cache operations - high cache_key cardinality (2000 keys x 3 ops = 6,000)
				for i := 0; i < highCardinalityCount/4; i++ {
					highCardCacheOps.Add(ctx, 1,
						metric.WithAttributes(
							attribute.String("service", services[rand.Intn(len(services))]),
							attribute.String("cache_key", fmt.Sprintf("key:%s:%d", []string{"user", "session", "product", "order", "inventory"}[rand.Intn(5)], rand.Intn(400))),
							attribute.String("operation", []string{"get", "set", "delete"}[rand.Intn(3)]),
							attribute.Bool("hit", rand.Float64() > 0.3),
						))
					batchDatapoints++
				}
				batchMetrics++

				// 5. HTTP duration histogram - high path cardinality (100 paths x services = 400+)
				for i := 0; i < highCardinalityCount/4; i++ {
					highCardHTTPPaths.Record(ctx, rand.Float64()*2.0,
						metric.WithAttributes(
							attribute.String("service", services[rand.Intn(len(services))]),
							attribute.String("path", fmt.Sprintf("/v%d/%s/%d", rand.Intn(2)+1, []string{"users", "orders", "products", "inventory", "payments"}[rand.Intn(5)], rand.Intn(20))),
							attribute.String("method", methods[rand.Intn(len(methods))]),
						))
					batchDatapoints++
				}
				batchMetrics++
			}

			// Stable mode metrics - completely predictable for testing
			if enableStableMode {
				for i, c := range stableCounters {
					// Each metric has exactly stableCardinalityPerMetric series
					// using deterministic label values (not random)
					for j := 0; j < stableCardinalityPerMetric; j++ {
						for k := 0; k < stableDatapointsPerInterval; k++ {
							c.Add(ctx, 1,
								metric.WithAttributes(
									attribute.String("series", fmt.Sprintf("s%02d", j)),
									attribute.Int("metric_id", i),
								))
							batchDatapoints++
						}
					}
				}
				batchMetrics += int64(len(stableCounters))
			}

			// Diverse metrics - generate values for all unique metric names
			if enableDiverseMetrics {
				devices := []string{"sda", "sdb", "nvme0n1", "nvme1n1"}
				interfaces := []string{"eth0", "eth1", "lo", "docker0", "br-0"}
				mounts := []string{"/", "/var", "/tmp", "/home", "/data"}
				cpuCores := []string{"cpu0", "cpu1", "cpu2", "cpu3", "cpu4", "cpu5", "cpu6", "cpu7"}

				// Record counter values
				for i, c := range diverseCounters {
					// Vary attributes based on metric type
					var attrs []attribute.KeyValue
					switch {
					case i < 9: // disk metrics
						attrs = []attribute.KeyValue{
							attribute.String("device", devices[i%len(devices)]),
							attribute.String("service", services[rand.Intn(len(services))]),
						}
					case i < 21: // network metrics
						attrs = []attribute.KeyValue{
							attribute.String("interface", interfaces[i%len(interfaces)]),
							attribute.String("service", services[rand.Intn(len(services))]),
						}
					default:
						attrs = []attribute.KeyValue{
							attribute.String("service", services[rand.Intn(len(services))]),
							attribute.String("env", environments[rand.Intn(len(environments))]),
							attribute.String("instance", fmt.Sprintf("inst_%d", rand.Intn(10))),
						}
					}
					c.Add(ctx, int64(rand.Intn(1000)+1), metric.WithAttributes(attrs...))
					batchDatapoints++
				}
				batchMetrics += int64(len(diverseCounters))

				// Record gauge values
				for i, g := range diverseGauges {
					var attrs []attribute.KeyValue
					var value float64
					switch {
					case i < 9: // CPU metrics
						attrs = []attribute.KeyValue{
							attribute.String("cpu", cpuCores[i%len(cpuCores)]),
							attribute.String("service", services[rand.Intn(len(services))]),
						}
						value = rand.Float64() * 100 // percent
					case i < 24: // memory metrics
						attrs = []attribute.KeyValue{
							attribute.String("service", services[rand.Intn(len(services))]),
						}
						value = float64(rand.Int63n(16 * 1024 * 1024 * 1024)) // up to 16GB
					case i < 29: // filesystem metrics
						attrs = []attribute.KeyValue{
							attribute.String("mountpoint", mounts[i%len(mounts)]),
							attribute.String("device", devices[rand.Intn(len(devices))]),
							attribute.String("fstype", "ext4"),
						}
						value = float64(rand.Int63n(500 * 1024 * 1024 * 1024)) // up to 500GB
					default:
						attrs = []attribute.KeyValue{
							attribute.String("service", services[rand.Intn(len(services))]),
							attribute.String("env", environments[rand.Intn(len(environments))]),
						}
						value = rand.Float64() * 1000
					}
					g.Record(ctx, value, metric.WithAttributes(attrs...))
					batchDatapoints++
				}
				batchMetrics += int64(len(diverseGauges))

				// Record histogram values
				for _, h := range diverseHistograms {
					attrs := []attribute.KeyValue{
						attribute.String("service", services[rand.Intn(len(services))]),
						attribute.String("env", environments[rand.Intn(len(environments))]),
						attribute.String("method", methods[rand.Intn(len(methods))]),
					}
					// Generate multiple samples per histogram
					for j := 0; j < 5; j++ {
						h.Record(ctx, rand.Float64()*2.0, metric.WithAttributes(attrs...)) // 0-2 seconds
						batchDatapoints++
					}
				}
				batchMetrics += int64(len(diverseHistograms))
			}

			// Verification counter - increment with known value
			// Use bounded batch_id to prevent unbounded cardinality (verifier only uses max value)
			verificationID++
			verificationCounter.Add(ctx, verificationID,
				metric.WithAttributes(
					attribute.Int64("batch_id", int64(iteration%100)),
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
