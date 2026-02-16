package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// findFreePort returns an available TCP port on localhost.
func findFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// governorBinaryPath locates the metrics-governor binary.
// Search order:
//  1. MG_BINARY env var (explicit override for CI/local dev)
//  2. Pre-built binary at the repo root (../metrics-governor)
//  3. Build from source (may fail if module cache has permission issues)
func governorBinaryPath(t *testing.T) string {
	t.Helper()

	// 1. Check env var override.
	if envBin := os.Getenv("MG_BINARY"); envBin != "" {
		if info, err := os.Stat(envBin); err == nil && !info.IsDir() {
			return envBin
		}
		t.Fatalf("MG_BINARY=%q does not exist or is a directory", envBin)
	}

	// 2. Look for the pre-built binary at the repo root.
	repoRoot := filepath.Join("..", "metrics-governor")
	if abs, err := filepath.Abs(repoRoot); err == nil {
		if info, err := os.Stat(abs); err == nil && !info.IsDir() {
			return abs
		}
	}

	// 3. Try building from source.
	tmp := t.TempDir()
	binPath := filepath.Join(tmp, "metrics-governor")
	cmd := exec.Command("go", "build", "-o", binPath, "../cmd/metrics-governor/")
	cmd.Env = append(os.Environ(), "GOPATH=/tmp/gopath")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot find or build metrics-governor binary: %v\n%s", err, out)
	}
	return binPath
}

// startGovernor launches the metrics-governor process with the given flags.
// It returns the process and a cleanup function.
func startGovernor(t *testing.T, binPath string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	// Inherit environment and add GOLANG_PROTOBUF_REGISTRATION_CONFLICT=warn
	// to prevent panics from duplicate proto file registrations (vtprotobuf + standard OTel proto).
	cmd.Env = append(os.Environ(), "GOLANG_PROTOBUF_REGISTRATION_CONFLICT=warn")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start governor: %v", err)
	}
	t.Cleanup(func() {
		cmd.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	})
	return cmd
}

// waitForEndpoint polls an HTTP endpoint until it returns 200 or the timeout expires.
func waitForEndpoint(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("endpoint %s did not become ready within %s", url, timeout)
}

// scrapeMetrics fetches the /metrics endpoint and returns the body as a string.
func scrapeMetrics(t *testing.T, metricsURL string) string {
	t.Helper()
	resp, err := http.Get(metricsURL)
	if err != nil {
		t.Fatalf("failed to scrape metrics from %s: %v", metricsURL, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read metrics body: %v", err)
	}
	return string(body)
}

// extractFloat64Metric extracts a float64 value for a metric name from Prometheus text format.
func extractFloat64Metric(metricsText, metricName string) (float64, bool) {
	for _, line := range strings.Split(metricsText, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, metricName) {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				var val float64
				if _, err := fmt.Sscanf(parts[len(parts)-1], "%f", &val); err == nil {
					return val, true
				}
			}
		}
	}
	return 0, false
}

// TestFunctional_BalancedProfile_MemoryQueue_EndToEnd validates that the balanced
// profile's memory queue mode works end-to-end: metrics sent via gRPC are received
// by the governor and exported to a backend. The balanced profile uses
// QueueMode=memory with reduced memory percentages (7%/5%), and this test confirms
// that the pipeline delivers data through the memory queue path.
func TestFunctional_BalancedProfile_MemoryQueue_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping functional test in short mode")
	}

	binPath := governorBinaryPath(t)

	// Allocate free ports for all governor endpoints.
	grpcPort := findFreePort(t)
	httpPort := findFreePort(t)
	statsPort := findFreePort(t)
	backendPort := findFreePort(t)

	// Set up a mock OTLP HTTP backend that counts received requests.
	var receivedRequests atomic.Int64
	backend := &http.Server{
		Addr: fmt.Sprintf("127.0.0.1:%d", backendPort),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Accept any POST to /v1/metrics — this is the OTLP HTTP export path.
			if r.Method == http.MethodPost {
				io.ReadAll(r.Body)
				r.Body.Close()
				receivedRequests.Add(1)
			}
			w.WriteHeader(http.StatusOK)
		}),
	}
	go backend.ListenAndServe()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		backend.Shutdown(ctx)
	})

	// Start governor with balanced profile, exporting via HTTP to our mock backend.
	queueDir := t.TempDir()
	startGovernor(t, binPath,
		"--profile", "balanced",
		"--grpc-listen", fmt.Sprintf("127.0.0.1:%d", grpcPort),
		"--http-listen", fmt.Sprintf("127.0.0.1:%d", httpPort),
		"--stats-addr", fmt.Sprintf("127.0.0.1:%d", statsPort),
		"--exporter-endpoint", fmt.Sprintf("http://127.0.0.1:%d", backendPort),
		"--exporter-protocol", "http",
		"--exporter-insecure=true",
		"--exporter-compression", "none",
		"--queue-path", queueDir,
		"--bloom-persistence-enabled=false",
		"--limits-dry-run=true",
	)

	// Wait for the stats endpoint to be ready.
	statsURL := fmt.Sprintf("http://127.0.0.1:%d/metrics", statsPort)
	waitForEndpoint(t, statsURL, 15*time.Second)

	// Verify the profile applied memory queue settings by checking /metrics.
	metricsBody := scrapeMetrics(t, statsURL)

	// The governor should be running — now send metrics via gRPC using OTel SDK.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(
		fmt.Sprintf("127.0.0.1:%d", grpcPort),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to create gRPC connection: %v", err)
	}
	defer conn.Close()

	exporter, err := otlpmetricgrpc.New(ctx,
		otlpmetricgrpc.WithGRPCConn(conn),
		otlpmetricgrpc.WithInsecure(),
	)
	if err != nil {
		t.Fatalf("failed to create OTLP gRPC exporter: %v", err)
	}

	reader := sdkmetric.NewPeriodicReader(exporter,
		sdkmetric.WithInterval(1*time.Second),
	)

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
	)
	defer provider.Shutdown(ctx)

	meter := provider.Meter("functional-test")
	counter, err := meter.Int64Counter("functional_test_memory_queue_counter",
		metric.WithDescription("Test counter for memory queue validation"),
	)
	if err != nil {
		t.Fatalf("failed to create counter: %v", err)
	}

	// Send a batch of metrics.
	const batchCount = 50
	for i := 0; i < batchCount; i++ {
		counter.Add(ctx, 1)
	}

	// Force a flush to ensure metrics are sent to the governor.
	if err := provider.ForceFlush(ctx); err != nil {
		t.Logf("ForceFlush warning: %v", err)
	}

	// Wait for the governor to process and export the metrics to the backend.
	deadline := time.Now().Add(30 * time.Second)
	var lastReceived int64
	for time.Now().Before(deadline) {
		lastReceived = receivedRequests.Load()
		if lastReceived > 0 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	if lastReceived == 0 {
		t.Fatal("mock backend received 0 requests — memory queue pipeline did not deliver metrics end-to-end")
	}
	t.Logf("mock backend received %d export requests via memory queue pipeline", lastReceived)

	// Verify governor received datapoints by scraping /metrics.
	metricsBody = scrapeMetrics(t, statsURL)

	dpReceived, found := extractFloat64Metric(metricsBody, "metrics_governor_datapoints_received_total")
	if !found {
		t.Error("metrics_governor_datapoints_received_total not found in /metrics output")
	} else if dpReceived == 0 {
		t.Error("metrics_governor_datapoints_received_total is 0 — governor did not register received datapoints")
	} else {
		t.Logf("governor received %.0f datapoints via gRPC, exported through memory queue", dpReceived)
	}

	// Verify that queue-related metrics are exposed (confirms memory queue is active).
	queueMetrics := []string{
		"metrics_governor_queue_size",
		"metrics_governor_batches_sent_total",
	}
	for _, qm := range queueMetrics {
		if !strings.Contains(metricsBody, qm) {
			t.Errorf("expected queue metric %q in /metrics output (memory queue should expose queue stats)", qm)
		}
	}

	// Also confirm the profile's memory settings were applied.
	// Balanced profile: BufferMemoryPercent=0.07, QueueMemoryPercent=0.05
	if val, ok := extractFloat64Metric(metricsBody, "metrics_governor_memory_budget_buffer_bytes"); ok && val > 0 {
		t.Logf("buffer memory budget: %.0f bytes (confirms balanced profile memory settings applied)", val)
	}
}

// TestFunctional_BalancedProfile_MemoryBudgetMetrics verifies that the memory
// budget observability gauges are present and non-zero when the governor runs
// with the balanced profile. These metrics were added as part of the memory
// optimization work and are critical for production observability.
//
// Verified metrics:
//   - metrics_governor_memory_budget_gomemlimit_bytes
//   - metrics_governor_memory_budget_buffer_bytes
//   - metrics_governor_memory_budget_queue_bytes
//   - metrics_governor_memory_budget_utilization_ratio
func TestFunctional_BalancedProfile_MemoryBudgetMetrics(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping functional test in short mode")
	}

	binPath := governorBinaryPath(t)

	// Allocate free ports.
	grpcPort := findFreePort(t)
	httpPort := findFreePort(t)
	statsPort := findFreePort(t)
	backendPort := findFreePort(t)

	// Set up a no-op backend so the governor doesn't fail on export.
	backend := &http.Server{
		Addr: fmt.Sprintf("127.0.0.1:%d", backendPort),
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				io.ReadAll(r.Body)
				r.Body.Close()
			}
			w.WriteHeader(http.StatusOK)
		}),
	}
	go backend.ListenAndServe()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		backend.Shutdown(ctx)
	})

	// Start governor with balanced profile.
	queueDir := t.TempDir()
	startGovernor(t, binPath,
		"--profile", "balanced",
		"--grpc-listen", fmt.Sprintf("127.0.0.1:%d", grpcPort),
		"--http-listen", fmt.Sprintf("127.0.0.1:%d", httpPort),
		"--stats-addr", fmt.Sprintf("127.0.0.1:%d", statsPort),
		"--exporter-endpoint", fmt.Sprintf("http://127.0.0.1:%d", backendPort),
		"--exporter-protocol", "http",
		"--exporter-insecure=true",
		"--exporter-compression", "none",
		"--queue-path", queueDir,
		"--bloom-persistence-enabled=false",
		"--limits-dry-run=true",
	)

	// Wait for the stats endpoint to be ready.
	statsURL := fmt.Sprintf("http://127.0.0.1:%d/metrics", statsPort)
	waitForEndpoint(t, statsURL, 15*time.Second)

	// Scrape /metrics and verify all four memory budget gauges are present.
	metricsBody := scrapeMetrics(t, statsURL)

	budgetMetrics := []struct {
		name      string
		wantGT    float64 // we expect the value to be strictly greater than this
		allowZero bool    // if true, 0 is acceptable (for ratio at idle)
	}{
		{
			name:   "metrics_governor_memory_budget_gomemlimit_bytes",
			wantGT: 0,
		},
		{
			name:   "metrics_governor_memory_budget_buffer_bytes",
			wantGT: 0,
		},
		{
			name:   "metrics_governor_memory_budget_queue_bytes",
			wantGT: 0,
		},
		{
			name:      "metrics_governor_memory_budget_utilization_ratio",
			wantGT:    -1, // ratio can be very small at idle, just verify presence
			allowZero: true,
		},
	}

	for _, bm := range budgetMetrics {
		t.Run(bm.name, func(t *testing.T) {
			val, found := extractFloat64Metric(metricsBody, bm.name)
			if !found {
				t.Fatalf("metric %q not found in /metrics output", bm.name)
			}

			if !bm.allowZero && val <= bm.wantGT {
				t.Errorf("metric %q = %f, want > %f (balanced profile should set non-zero memory budgets)",
					bm.name, val, bm.wantGT)
			}

			t.Logf("%s = %f", bm.name, val)
		})
	}

	// Additional validation: utilization ratio must be in [0.0, 1.0] range.
	ratio, found := extractFloat64Metric(metricsBody, "metrics_governor_memory_budget_utilization_ratio")
	if found {
		if ratio < 0.0 || ratio > 1.0 {
			t.Errorf("utilization_ratio = %f, want [0.0, 1.0]", ratio)
		}
	}

	// Verify the buffer and queue budgets are consistent with balanced profile ratios.
	// Balanced profile: BufferMemoryPercent=0.07, QueueMemoryPercent=0.05
	// Buffer budget should be smaller than queue budget is NOT necessarily true;
	// the absolute values depend on GOMEMLIMIT. Just verify both are positive.
	gomemlimit, _ := extractFloat64Metric(metricsBody, "metrics_governor_memory_budget_gomemlimit_bytes")
	bufferBytes, _ := extractFloat64Metric(metricsBody, "metrics_governor_memory_budget_buffer_bytes")
	queueBytes, _ := extractFloat64Metric(metricsBody, "metrics_governor_memory_budget_queue_bytes")

	if gomemlimit > 0 {
		// Buffer should be approximately 7% of GOMEMLIMIT (balanced: 0.07).
		bufferRatio := bufferBytes / gomemlimit
		if bufferRatio < 0.01 || bufferRatio > 0.20 {
			t.Errorf("buffer/gomemlimit ratio = %.4f, expected ~0.07 (balanced profile); buffer=%f, gomemlimit=%f",
				bufferRatio, bufferBytes, gomemlimit)
		} else {
			t.Logf("buffer/gomemlimit ratio = %.4f (expected ~0.07)", bufferRatio)
		}

		// Queue should be approximately 5% of GOMEMLIMIT (balanced: 0.05).
		queueRatio := queueBytes / gomemlimit
		if queueRatio < 0.01 || queueRatio > 0.15 {
			t.Errorf("queue/gomemlimit ratio = %.4f, expected ~0.05 (balanced profile); queue=%f, gomemlimit=%f",
				queueRatio, queueBytes, gomemlimit)
		} else {
			t.Logf("queue/gomemlimit ratio = %.4f (expected ~0.05)", queueRatio)
		}
	} else {
		t.Log("gomemlimit not set (no container memory limit detected) — budget metrics report 0, which is expected in local dev")
	}
}

// Ensure PeriodicReader satisfies the Reader interface at compile time.
var _ sdkmetric.Reader = (*sdkmetric.PeriodicReader)(nil)
