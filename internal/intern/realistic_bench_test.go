package intern_test

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/szibis/metrics-governor/internal/intern"
)

// simulateMetricBatch simulates processing a batch of metrics with repeated labels
func simulateMetricBatch(labels []string, useInterning bool, pool *intern.Pool) []string {
	result := make([]string, 0, len(labels)*100)
	
	// Simulate 100 metrics, each with the same label set (common in real workloads)
	for i := 0; i < 100; i++ {
		for _, label := range labels {
			if useInterning {
				result = append(result, pool.Intern(label))
			} else {
				// Without interning: each string is a new allocation
				result = append(result, string([]byte(label)))
			}
		}
	}
	return result
}

func BenchmarkRealisticWorkload_WithInterning(b *testing.B) {
	labels := []string{
		"service.name", "k8s.pod.name", "k8s.namespace.name", "k8s.node.name",
		"http.method", "http.status_code", "http.route",
		"host.name", "cloud.region", "deployment.environment",
	}
	pool := intern.CommonLabels()
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		_ = simulateMetricBatch(labels, true, pool)
	}
}

func BenchmarkRealisticWorkload_WithoutInterning(b *testing.B) {
	labels := []string{
		"service.name", "k8s.pod.name", "k8s.namespace.name", "k8s.node.name",
		"http.method", "http.status_code", "http.route",
		"host.name", "cloud.region", "deployment.environment",
	}
	
	b.ResetTimer()
	b.ReportAllocs()
	
	for i := 0; i < b.N; i++ {
		_ = simulateMetricBatch(labels, false, nil)
	}
}

// BenchmarkMemoryPressure tests memory allocation under high load
func BenchmarkMemoryPressure(b *testing.B) {
	// Simulate OTLP resource and span attributes
	resourceAttrs := map[string]string{
		"service.name":           "payment-service",
		"service.namespace":      "production",
		"service.version":        "1.2.3",
		"k8s.pod.name":           "payment-service-abc123",
		"k8s.namespace.name":     "default",
		"k8s.node.name":          "node-1",
		"host.name":              "ip-10-0-1-100",
		"cloud.region":           "us-east-1",
		"cloud.provider":         "aws",
		"deployment.environment": "production",
	}
	
	spanAttrs := map[string]string{
		"http.method":      "POST",
		"http.status_code": "200",
		"http.route":       "/api/v1/payments",
		"db.system":        "postgresql",
		"db.name":          "payments",
		"rpc.service":      "PaymentService",
	}
	
	pool := intern.CommonLabels()
	
	b.Run("WithInterning", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			// Simulate processing attributes
			for k, v := range resourceAttrs {
				_ = pool.Intern(k)
				if len(v) <= 64 {
					_ = pool.Intern(v)
				}
			}
			for k, v := range spanAttrs {
				_ = pool.Intern(k)
				if len(v) <= 64 {
					_ = pool.Intern(v)
				}
			}
		}
	})
	
	b.Run("WithoutInterning", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			// Simulate processing without interning
			for k, v := range resourceAttrs {
				_ = string([]byte(k))
				_ = string([]byte(v))
			}
			for k, v := range spanAttrs {
				_ = string([]byte(k))
				_ = string([]byte(v))
			}
		}
	})
}

func TestRealisticMemoryComparison(t *testing.T) {
	fmt.Println("\n=== REALISTIC MEMORY COMPARISON ===")
	
	labels := []string{
		"service.name", "k8s.pod.name", "k8s.namespace.name",
		"http.method", "http.status_code", "cloud.region",
	}
	
	// Measure memory with interning
	runtime.GC()
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)
	
	pool := intern.CommonLabels()
	for i := 0; i < 10000; i++ {
		for _, label := range labels {
			_ = pool.Intern(label)
		}
	}
	
	runtime.GC()
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)
	
	internAllocs := m2.TotalAlloc - m1.TotalAlloc
	
	// Measure memory without interning
	runtime.GC()
	var m3 runtime.MemStats
	runtime.ReadMemStats(&m3)
	
	results := make([]string, 0, 60000)
	for i := 0; i < 10000; i++ {
		for _, label := range labels {
			results = append(results, string([]byte(label)))
		}
	}
	_ = results // prevent optimization
	
	runtime.GC()
	var m4 runtime.MemStats
	runtime.ReadMemStats(&m4)
	
	noInternAllocs := m4.TotalAlloc - m3.TotalAlloc
	
	fmt.Printf("Labels: %d, Iterations: 10000\n", len(labels))
	fmt.Printf("Total lookups: %d\n", len(labels)*10000)
	fmt.Println()
	fmt.Printf("With interning:    %d bytes allocated\n", internAllocs)
	fmt.Printf("Without interning: %d bytes allocated\n", noInternAllocs)
	
	if noInternAllocs > 0 {
		reduction := float64(noInternAllocs-internAllocs) / float64(noInternAllocs) * 100
		fmt.Printf("Memory reduction:  %.1f%%\n", reduction)
	}
	
	hits, misses := pool.Stats()
	fmt.Printf("\nIntern pool hits: %d, misses: %d\n", hits, misses)
	fmt.Printf("Hit rate: %.2f%%\n", float64(hits)/float64(hits+misses)*100)
}
