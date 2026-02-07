package exporter

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/queue"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
)

// ---------------------------------------------------------------------------
// Benchmark: Error classification (Perf 1 — stdlib strings vs old custom)
// ---------------------------------------------------------------------------

// BenchmarkClassifyExportError measures the new stdlib-based error classifier.
// Old implementation: 18+ allocations (9 containsStr calls × 2 toLowerStr each)
// New implementation: 2 allocations (1 err.Error() + 1 strings.ToLower)
func BenchmarkClassifyExportError(b *testing.B) {
	testErr := errors.New("connection refused: dial tcp 10.0.0.1:4317: connect: connection refused")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = classifyExportError(testErr)
	}
}

func BenchmarkClassifyExportError_Timeout(b *testing.B) {
	testErr := errors.New("context deadline exceeded (Client.Timeout exceeded while awaiting headers)")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = classifyExportError(testErr)
	}
}

func BenchmarkClassifyExportError_ServerError(b *testing.B) {
	testErr := errors.New("HTTP 503 Service Unavailable: backend is starting")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = classifyExportError(testErr)
	}
}

// BenchmarkOldStyleContains simulates the old containsStr approach for comparison.
// Each call does 2 allocations (toLower on both s and substr).
func BenchmarkOldStyleContains(b *testing.B) {
	errMsg := "connection refused: dial tcp 10.0.0.1:4317: connect: connection refused"
	patterns := []string{"timeout", "deadline", "connection refused", "no such host",
		"network is unreachable", "connection reset", "broken pipe", "EOF", "transport"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, p := range patterns {
			// Old approach: toLower each time
			_ = strings.Contains(strings.ToLower(errMsg), strings.ToLower(p))
		}
	}
}

// BenchmarkNewStyleContains shows the optimized approach (toLower once).
func BenchmarkNewStyleContains(b *testing.B) {
	errMsg := "connection refused: dial tcp 10.0.0.1:4317: connect: connection refused"
	patterns := []string{"timeout", "deadline", "connection refused", "no such host",
		"network is unreachable", "connection reset", "broken pipe", "eof", "transport"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lower := strings.ToLower(errMsg)
		for _, p := range patterns {
			_ = strings.Contains(lower, p)
		}
	}
}

// ---------------------------------------------------------------------------
// Benchmark: CB gate in Export() — open circuit (fast path) vs closed (normal)
// ---------------------------------------------------------------------------

// noopExporter is a zero-allocation exporter for benchmarking.
type noopExporter struct{}

func (n *noopExporter) Export(_ context.Context, _ *colmetricspb.ExportMetricsServiceRequest) error {
	return nil
}
func (n *noopExporter) Close() error { return nil }

// BenchmarkExport_CircuitClosed measures Export() latency with circuit CLOSED.
// This is the normal path: calls underlying exporter.
func BenchmarkExport_CircuitClosed(b *testing.B) {
	tmpDir := b.TempDir()
	qe, err := NewQueued(&noopExporter{}, queue.Config{
		Path:                       tmpDir,
		MaxSize:                    10000,
		RetryInterval:              time.Hour,
		MaxRetryDelay:              time.Hour,
		FullBehavior:               queue.DropOldest,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    10,
		CircuitBreakerResetTimeout: time.Hour,
	})
	if err != nil {
		b.Fatal(err)
	}
	defer qe.Close()

	req := createTestRequest()
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = qe.Export(ctx, req)
	}
}

// BenchmarkExport_CircuitOpen measures Export() latency with circuit OPEN.
// This is the fast path added by Fix 1: queue immediately, no HTTP timeout.
// Expected: ~µs (just a queue push), vs old behavior of 30s timeout.
func BenchmarkExport_CircuitOpen(b *testing.B) {
	tmpDir := b.TempDir()
	qe, err := NewQueued(&noopExporter{}, queue.Config{
		Path:                       tmpDir,
		MaxSize:                    1000000,
		RetryInterval:              time.Hour,
		MaxRetryDelay:              time.Hour,
		FullBehavior:               queue.DropOldest,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    2,
		CircuitBreakerResetTimeout: time.Hour,
		InmemoryBlocks:             100000,
	})
	if err != nil {
		b.Fatal(err)
	}
	defer qe.Close()

	// Force circuit to OPEN
	qe.circuitBreaker.state.Store(int32(CircuitOpen))
	qe.circuitBreaker.lastFailure.Store(time.Now().Unix())

	req := createTestRequest()
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = qe.Export(ctx, req)
	}
}

// BenchmarkExport_NoCircuitBreaker measures Export() with CB disabled.
func BenchmarkExport_NoCircuitBreaker(b *testing.B) {
	tmpDir := b.TempDir()
	qe, err := NewQueued(&noopExporter{}, queue.Config{
		Path:                  tmpDir,
		MaxSize:               10000,
		RetryInterval:         time.Hour,
		MaxRetryDelay:         time.Hour,
		FullBehavior:          queue.DropOldest,
		CircuitBreakerEnabled: false,
	})
	if err != nil {
		b.Fatal(err)
	}
	defer qe.Close()

	req := createTestRequest()
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = qe.Export(ctx, req)
	}
}

// ---------------------------------------------------------------------------
// Benchmark: CircuitBreaker.AllowRequest() — CAS performance
// ---------------------------------------------------------------------------

// BenchmarkCircuitBreaker_AllowRequest_Closed measures AllowRequest in closed state.
func BenchmarkCircuitBreaker_AllowRequest_Closed(b *testing.B) {
	cb := &CircuitBreaker{
		failureThreshold: 10,
		resetTimeout:     time.Hour,
	}
	cb.state.Store(int32(CircuitClosed))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cb.AllowRequest()
	}
}

// BenchmarkCircuitBreaker_AllowRequest_Open measures AllowRequest in open state
// (before reset timeout — returns false immediately).
func BenchmarkCircuitBreaker_AllowRequest_Open(b *testing.B) {
	cb := &CircuitBreaker{
		failureThreshold: 10,
		resetTimeout:     time.Hour,
	}
	cb.state.Store(int32(CircuitOpen))
	cb.lastFailure.Store(time.Now().Unix())

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = cb.AllowRequest()
	}
}

// BenchmarkCircuitBreaker_AllowRequest_Parallel measures contended AllowRequest.
func BenchmarkCircuitBreaker_AllowRequest_Parallel(b *testing.B) {
	cb := &CircuitBreaker{
		failureThreshold: 10,
		resetTimeout:     time.Hour,
	}
	cb.state.Store(int32(CircuitClosed))

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = cb.AllowRequest()
		}
	})
}

// BenchmarkCircuitBreaker_RecordSuccess measures RecordSuccess overhead.
func BenchmarkCircuitBreaker_RecordSuccess(b *testing.B) {
	cb := &CircuitBreaker{
		failureThreshold: 10,
		resetTimeout:     time.Hour,
	}
	cb.state.Store(int32(CircuitClosed))

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cb.RecordSuccess()
	}
}

// ---------------------------------------------------------------------------
// Benchmark: Export throughput comparison
// ---------------------------------------------------------------------------

// countingExporter counts calls without doing real work.
type countingExporter struct {
	count atomic.Int64
}

func (c *countingExporter) Export(_ context.Context, _ *colmetricspb.ExportMetricsServiceRequest) error {
	c.count.Add(1)
	return nil
}
func (c *countingExporter) Close() error { return nil }

// BenchmarkExport_Throughput_Parallel measures parallel export throughput.
func BenchmarkExport_Throughput_Parallel(b *testing.B) {
	tmpDir := b.TempDir()
	exp := &countingExporter{}
	qe, err := NewQueued(exp, queue.Config{
		Path:                       tmpDir,
		MaxSize:                    100000,
		RetryInterval:              time.Hour,
		MaxRetryDelay:              time.Hour,
		FullBehavior:               queue.DropOldest,
		CircuitBreakerEnabled:      true,
		CircuitBreakerThreshold:    100,
		CircuitBreakerResetTimeout: time.Hour,
	})
	if err != nil {
		b.Fatal(err)
	}
	defer qe.Close()

	req := createTestRequest()
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = qe.Export(ctx, req)
		}
	})
}
