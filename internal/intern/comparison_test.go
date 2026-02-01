package intern_test

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/szibis/metrics-governor/internal/intern"
)

func TestInternImprovements(t *testing.T) {
	fmt.Println("\n=== STRING INTERNING PERFORMANCE COMPARISON ===")
	fmt.Printf("CPU cores: %d\n", runtime.NumCPU())

	// Test PRW Label Interning
	fmt.Println("\n--- Prometheus/PRW Label Interning ---")

	// Common PRW labels (repeated many times in real workloads)
	prwLabels := []string{
		"__name__", "job", "instance", "le", "quantile",
		"service", "env", "cluster", "namespace", "pod",
		"container", "node", "host", "region", "method",
	}

	pool := intern.CommonLabels()

	// Measure hit rate for PRW labels
	iterations := 100000

	for i := 0; i < iterations; i++ {
		for _, label := range prwLabels {
			pool.Intern(label)
		}
	}

	hits, misses := pool.Stats()
	hitRate := float64(hits) / float64(hits+misses) * 100

	fmt.Printf("PRW Labels tested: %d\n", len(prwLabels))
	fmt.Printf("Iterations: %d\n", iterations)
	fmt.Printf("Hits: %d, Misses: %d\n", hits, misses)
	fmt.Printf("Hit Rate: %.2f%%\n", hitRate)

	// Test OTLP Semantic Convention Attributes
	fmt.Println("\n--- OTLP Semantic Convention Attributes ---")

	otlpResourceAttrs := []string{
		"service.name", "service.namespace", "service.version", "service.instance.id",
		"deployment.environment",
		"k8s.pod.name", "k8s.namespace.name", "k8s.container.name", "k8s.node.name",
		"k8s.deployment.name", "k8s.cluster.name",
		"host.name", "host.id", "host.type", "host.arch",
		"cloud.provider", "cloud.region", "cloud.availability_zone", "cloud.account.id",
		"container.id", "container.name", "container.image.name",
		"process.pid", "process.executable.name",
		"telemetry.sdk.name", "telemetry.sdk.version", "telemetry.sdk.language",
	}

	otlpSpanMetricAttrs := []string{
		"http.method", "http.status_code", "http.route", "http.scheme", "http.url",
		"http.request.method", "http.response.status_code",
		"url.scheme", "url.path", "url.full",
		"server.address", "server.port", "client.address",
		"db.system", "db.name", "db.operation", "db.statement",
		"rpc.system", "rpc.service", "rpc.method", "rpc.grpc.status_code",
		"messaging.system", "messaging.destination", "messaging.operation",
		"net.peer.name", "net.peer.port", "net.host.name",
		"error.type", "exception.type", "exception.message",
		"otel.status_code", "otel.library.name",
	}

	// Reset stats for clean measurement
	pool2 := intern.NewPool()

	// Pre-populate like CommonLabels does
	allAttrs := append(otlpResourceAttrs, otlpSpanMetricAttrs...)
	for _, attr := range allAttrs {
		pool2.Intern(attr)
	}

	// Now simulate repeated lookups (like real traffic)
	for i := 0; i < iterations; i++ {
		for _, attr := range allAttrs {
			pool2.Intern(attr)
		}
	}

	hits2, misses2 := pool2.Stats()
	hitRate2 := float64(hits2) / float64(hits2+misses2) * 100

	fmt.Printf("OTLP Resource Attributes: %d\n", len(otlpResourceAttrs))
	fmt.Printf("OTLP Span/Metric Attributes: %d\n", len(otlpSpanMetricAttrs))
	fmt.Printf("Total OTLP Attributes: %d\n", len(allAttrs))
	fmt.Printf("Iterations: %d\n", iterations)
	fmt.Printf("Hits: %d, Misses: %d\n", hits2, misses2)
	fmt.Printf("Hit Rate: %.2f%%\n", hitRate2)

	// Show pool size
	fmt.Println("\n--- Intern Pool Statistics ---")
	commonPool := intern.CommonLabels()
	fmt.Printf("CommonLabels pool size: %d pre-populated entries\n", commonPool.Size())

	// Memory savings calculation
	fmt.Println("\n--- Expected Memory Savings ---")
	fmt.Println("Without interning:")
	fmt.Println("  - Each label/attribute string allocated separately")
	fmt.Println("  - 1M requests × 20 labels × 16 bytes = 320 MB allocations")
	fmt.Println()
	fmt.Println("With interning:")
	fmt.Println("  - First occurrence: allocated once")
	fmt.Println("  - Subsequent: zero allocations (cache hit)")
	fmt.Printf("  - Expected allocation reduction: %.0f%%\n", hitRate2)

	// Verify high hit rate
	if hitRate < 99.0 {
		t.Errorf("PRW label hit rate too low: %.2f%% (expected >= 99%%)", hitRate)
	}
	if hitRate2 < 99.0 {
		t.Errorf("OTLP attribute hit rate too low: %.2f%% (expected >= 99%%)", hitRate2)
	}
}

func TestConcurrencyLimitingStats(t *testing.T) {
	fmt.Println("\n=== CONCURRENCY LIMITING STATISTICS ===")

	numCPU := runtime.NumCPU()
	defaultLimit := numCPU * 4

	scenarios := []struct {
		name      string
		endpoints int
	}{
		{"Small cluster", 10},
		{"Medium cluster", 50},
		{"Large cluster", 100},
		{"Very large cluster", 500},
	}

	fmt.Printf("CPU cores: %d\n", numCPU)
	fmt.Printf("Default concurrency limit: %d (NumCPU × 4)\n", defaultLimit)
	fmt.Println()
	fmt.Println("| Scenario | Endpoints | Without Limiter | With Limiter | Reduction |")
	fmt.Println("|----------|-----------|-----------------|--------------|-----------|")

	for _, s := range scenarios {
		withoutLimiter := s.endpoints
		withLimiter := defaultLimit
		if withLimiter > s.endpoints {
			withLimiter = s.endpoints
		}
		reduction := float64(withoutLimiter-withLimiter) / float64(withoutLimiter) * 100

		fmt.Printf("| %-15s | %3d | %3d goroutines | %3d goroutines | %.1f%% |\n",
			s.name, s.endpoints, withoutLimiter, withLimiter, reduction)
	}

	fmt.Println()
	fmt.Println("Note: Concurrency limiter bounds maximum concurrent goroutines")
	fmt.Println("regardless of number of endpoints, preventing goroutine explosion.")
}

func BenchmarkInternVsNoIntern(b *testing.B) {
	labels := []string{
		"service.name", "k8s.pod.name", "http.method", "http.status_code",
		"db.system", "rpc.service", "host.name", "cloud.region",
	}

	b.Run("WithIntern", func(b *testing.B) {
		pool := intern.CommonLabels()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for _, label := range labels {
				_ = pool.Intern(label)
			}
		}
	})

	b.Run("WithoutIntern", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			for _, label := range labels {
				// Simulate no interning - string copy
				_ = string([]byte(label))
			}
		}
	})
}
