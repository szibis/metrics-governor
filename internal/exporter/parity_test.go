package exporter

import (
	"context"
	"sync"
	"testing"
	"time"

	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	"github.com/szibis/metrics-governor/internal/prw"
	"github.com/szibis/metrics-governor/internal/queue"
)

// TestParity_PersistentQueue verifies both OTLP and PRW use disk-backed queues.
func TestParity_PersistentQueue(t *testing.T) {
	t.Run("OTLP uses SendQueue", func(t *testing.T) {
		exp := &mockOTLPExporter{}
		qCfg := queue.Config{
			Path:          t.TempDir(),
			MaxSize:       100,
			RetryInterval: time.Hour,
			MaxRetryDelay: time.Hour,
		}

		qe, err := NewQueued(exp, qCfg)
		if err != nil {
			t.Fatalf("NewQueued() error = %v", err)
		}
		defer qe.Close()

		if qe.queue == nil {
			t.Error("OTLP QueuedExporter should use SendQueue")
		}
	})

	t.Run("PRW uses SendQueue", func(t *testing.T) {
		exp := &mockPRWExporter{}
		cfg := PRWQueueConfig{
			Path:          t.TempDir(),
			MaxSize:       100,
			RetryInterval: time.Hour,
			MaxRetryDelay: time.Hour,
		}

		qe, err := NewPRWQueued(exp, cfg)
		if err != nil {
			t.Fatalf("NewPRWQueued() error = %v", err)
		}
		defer qe.Close()

		if qe.queue == nil {
			t.Error("PRW QueuedExporter should use SendQueue")
		}
	})
}

// TestParity_SplitOnError verifies both pipelines split on 413/too-large errors.
func TestParity_SplitOnError(t *testing.T) {
	t.Run("OTLP split on 413", func(t *testing.T) {
		exp := &mockOTLPExporter{
			failCount: 1,
			failErr: &ExportError{
				Type:       ErrorTypeClientError,
				StatusCode: 413,
				Message:    "request entity too large",
			},
		}
		qCfg := queue.Config{
			Path:          t.TempDir(),
			MaxSize:       100,
			RetryInterval: 50 * time.Millisecond,
			MaxRetryDelay: time.Second,
		}

		qe, err := NewQueued(exp, qCfg)
		if err != nil {
			t.Fatalf("NewQueued() error = %v", err)
		}
		defer qe.Close()

		req := &colmetricspb.ExportMetricsServiceRequest{
			ResourceMetrics: []*metricspb.ResourceMetrics{
				{ScopeMetrics: []*metricspb.ScopeMetrics{{}}},
				{ScopeMetrics: []*metricspb.ScopeMetrics{{}}},
			},
		}

		_ = qe.Export(context.Background(), req)

		// Wait for retry and split
		time.Sleep(300 * time.Millisecond)

		exported := exp.getExported()
		totalRM := 0
		for _, e := range exported {
			totalRM += len(e.ResourceMetrics)
		}
		if totalRM != 2 {
			t.Errorf("OTLP total ResourceMetrics = %d, want 2", totalRM)
		}
	})

	t.Run("PRW split on 413", func(t *testing.T) {
		exp := &mockPRWExporter{
			failCount: 1,
			failErr: &ExportError{
				Type:       ErrorTypeClientError,
				StatusCode: 413,
				Message:    "request entity too large",
			},
		}
		cfg := PRWQueueConfig{
			Path:          t.TempDir(),
			MaxSize:       100,
			RetryInterval: 50 * time.Millisecond,
			MaxRetryDelay: time.Second,
		}

		qe, err := NewPRWQueued(exp, cfg)
		if err != nil {
			t.Fatalf("NewPRWQueued() error = %v", err)
		}
		defer qe.Close()

		req := &prw.WriteRequest{
			Timeseries: []prw.TimeSeries{
				{Labels: []prw.Label{{Name: "__name__", Value: "m1"}}, Samples: []prw.Sample{{Value: 1, Timestamp: 1}}},
				{Labels: []prw.Label{{Name: "__name__", Value: "m2"}}, Samples: []prw.Sample{{Value: 2, Timestamp: 2}}},
			},
		}

		_ = qe.Export(context.Background(), req)

		time.Sleep(300 * time.Millisecond)

		exported := exp.getExported()
		totalTS := 0
		for _, e := range exported {
			totalTS += len(e.Timeseries)
		}
		if totalTS != 2 {
			t.Errorf("PRW total Timeseries = %d, want 2", totalTS)
		}
	})
}

// TestParity_CircuitBreaker verifies both pipelines use CircuitBreaker.
func TestParity_CircuitBreaker(t *testing.T) {
	t.Run("OTLP circuit breaker", func(t *testing.T) {
		exp := &mockOTLPExporter{}
		qCfg := queue.Config{
			Path:                       t.TempDir(),
			MaxSize:                    100,
			RetryInterval:              time.Hour,
			MaxRetryDelay:              time.Hour,
			CircuitBreakerEnabled:      true,
			CircuitBreakerThreshold:    5,
			CircuitBreakerResetTimeout: 10 * time.Second,
		}

		qe, err := NewQueued(exp, qCfg)
		if err != nil {
			t.Fatalf("NewQueued() error = %v", err)
		}
		defer qe.Close()

		if qe.circuitBreaker == nil {
			t.Error("OTLP should have circuit breaker enabled")
		}
		if qe.circuitBreaker.State() != CircuitClosed {
			t.Errorf("Initial state should be closed, got %v", qe.circuitBreaker.State())
		}
	})

	t.Run("PRW circuit breaker", func(t *testing.T) {
		exp := &mockPRWExporter{}
		cfg := PRWQueueConfig{
			Path:                    t.TempDir(),
			MaxSize:                 100,
			RetryInterval:           time.Hour,
			MaxRetryDelay:           time.Hour,
			CircuitBreakerEnabled:   true,
			CircuitFailureThreshold: 5,
			CircuitResetTimeout:     10 * time.Second,
		}

		qe, err := NewPRWQueued(exp, cfg)
		if err != nil {
			t.Fatalf("NewPRWQueued() error = %v", err)
		}
		defer qe.Close()

		if qe.circuitBreaker == nil {
			t.Error("PRW should have circuit breaker enabled")
		}
		if qe.circuitBreaker.State() != CircuitClosed {
			t.Errorf("Initial state should be closed, got %v", qe.circuitBreaker.State())
		}
	})
}

// TestParity_ExponentialBackoff verifies both pipelines support exponential backoff.
func TestParity_ExponentialBackoff(t *testing.T) {
	t.Run("OTLP backoff", func(t *testing.T) {
		exp := &mockOTLPExporter{}
		qCfg := queue.Config{
			Path:              t.TempDir(),
			MaxSize:           100,
			RetryInterval:     time.Second,
			MaxRetryDelay:     time.Minute,
			BackoffEnabled:    true,
			BackoffMultiplier: 2.0,
		}

		qe, err := NewQueued(exp, qCfg)
		if err != nil {
			t.Fatalf("NewQueued() error = %v", err)
		}
		defer qe.Close()

		if !qe.backoffEnabled {
			t.Error("OTLP backoff should be enabled")
		}
		if qe.backoffMultiplier != 2.0 {
			t.Errorf("OTLP backoff multiplier = %v, want 2.0", qe.backoffMultiplier)
		}
	})

	t.Run("PRW backoff", func(t *testing.T) {
		exp := &mockPRWExporter{}
		cfg := PRWQueueConfig{
			Path:              t.TempDir(),
			MaxSize:           100,
			RetryInterval:     time.Second,
			MaxRetryDelay:     time.Minute,
			BackoffEnabled:    true,
			BackoffMultiplier: 2.0,
		}

		qe, err := NewPRWQueued(exp, cfg)
		if err != nil {
			t.Fatalf("NewPRWQueued() error = %v", err)
		}
		defer qe.Close()

		if !qe.backoffEnabled {
			t.Error("PRW backoff should be enabled")
		}
		if qe.backoffMultiplier != 2.0 {
			t.Errorf("PRW backoff multiplier = %v, want 2.0", qe.backoffMultiplier)
		}
	})
}

// TestParity_GracefulDrain verifies both pipelines drain on shutdown.
func TestParity_GracefulDrain(t *testing.T) {
	t.Run("OTLP drain on close", func(t *testing.T) {
		exp := &mockOTLPExporter{
			failCount: 1,
			failErr: &ExportError{
				Type:       ErrorTypeServerError,
				StatusCode: 500,
				Message:    "server error",
			},
		}
		qCfg := queue.Config{
			Path:          t.TempDir(),
			MaxSize:       100,
			RetryInterval: time.Hour,
			MaxRetryDelay: time.Hour,
		}

		qe, err := NewQueued(exp, qCfg)
		if err != nil {
			t.Fatalf("NewQueued() error = %v", err)
		}

		req := &colmetricspb.ExportMetricsServiceRequest{
			ResourceMetrics: []*metricspb.ResourceMetrics{
				{ScopeMetrics: []*metricspb.ScopeMetrics{{}}},
			},
		}

		_ = qe.Export(context.Background(), req)

		// Close should drain
		err = qe.Close()
		if err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	t.Run("PRW drain on close", func(t *testing.T) {
		exp := &mockPRWExporter{
			failCount: 1,
			failErr: &ExportError{
				Type:       ErrorTypeServerError,
				StatusCode: 500,
				Message:    "server error",
			},
		}
		cfg := PRWQueueConfig{
			Path:          t.TempDir(),
			MaxSize:       100,
			RetryInterval: time.Hour,
			MaxRetryDelay: time.Hour,
		}

		qe, err := NewPRWQueued(exp, cfg)
		if err != nil {
			t.Fatalf("NewPRWQueued() error = %v", err)
		}

		req := &prw.WriteRequest{
			Timeseries: []prw.TimeSeries{
				{Labels: []prw.Label{{Name: "__name__", Value: "m1"}}, Samples: []prw.Sample{{Value: 1, Timestamp: 1}}},
			},
		}

		_ = qe.Export(context.Background(), req)

		// Close should drain
		err = qe.Close()
		if err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
}

// mockOTLPExporter is a mock OTLP exporter for parity tests.
type mockOTLPExporter struct {
	exported  []*colmetricspb.ExportMetricsServiceRequest
	mu        sync.Mutex
	failCount int
	failErr   error
}

func (m *mockOTLPExporter) Export(ctx context.Context, req *colmetricspb.ExportMetricsServiceRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failCount > 0 {
		m.failCount--
		return m.failErr
	}

	m.exported = append(m.exported, req)
	return nil
}

func (m *mockOTLPExporter) Close() error {
	return nil
}

func (m *mockOTLPExporter) getExported() []*colmetricspb.ExportMetricsServiceRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]*colmetricspb.ExportMetricsServiceRequest, len(m.exported))
	copy(result, m.exported)
	return result
}
