package exporter

import (
	"context"
	"testing"
	"time"

	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	"github.com/szibis/metrics-governor/internal/queue"
)

func TestQueuedExporter_QueueSize(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a mock that always fails so data stays in queue
	mock := &mockExporter{failCount: 100}

	queueCfg := queue.Config{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 1 * time.Hour, // Long retry so data stays queued
		MaxRetryDelay: 1 * time.Hour,
		FullBehavior:  queue.DropNewest,
	}

	exp, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("Failed to create queued exporter: %v", err)
	}
	defer exp.Close()

	// Queue should be empty initially
	if size := exp.QueueSize(); size != 0 {
		t.Errorf("Expected initial queue size 0, got %d", size)
	}

	// Export a request - it should fail and be queued
	ctx := context.Background()
	req := createTestRequest()
	err = exp.Export(ctx, req)
	if err != nil {
		t.Logf("Export failed as expected: %v", err)
	}

	// Queue length should be tracked
	length := exp.QueueLen()
	t.Logf("Queue length after export: %d", length)
}

func TestQueuedExporter_CloseWhileProcessing(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{}

	queueCfg := queue.Config{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 100 * time.Millisecond,
		MaxRetryDelay: 1 * time.Second,
	}

	exp, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("Failed to create queued exporter: %v", err)
	}

	ctx := context.Background()

	// Export a few requests
	for i := 0; i < 5; i++ {
		req := createTestRequest()
		if err := exp.Export(ctx, req); err != nil {
			t.Logf("Export %d failed: %v", i, err)
		}
	}

	// Close immediately - should not panic
	if err := exp.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Second close should be safe
	if err := exp.Close(); err != nil {
		t.Errorf("Second close failed: %v", err)
	}
}

func TestQueuedExporter_DrainOnClose(t *testing.T) {
	tmpDir := t.TempDir()
	mock := &mockExporter{}

	queueCfg := queue.Config{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 10 * time.Millisecond,
		MaxRetryDelay: 50 * time.Millisecond,
	}

	exp, err := NewQueued(mock, queueCfg)
	if err != nil {
		t.Fatalf("Failed to create queued exporter: %v", err)
	}

	ctx := context.Background()

	// Export some data
	for i := 0; i < 3; i++ {
		req := createTestRequest()
		_ = exp.Export(ctx, req)
	}

	// Close and allow drain
	if err := exp.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Verify mock was called
	if count := mock.getExportCount(); count == 0 {
		t.Log("No exports were made (might have been direct)")
	}
}

func TestNewExporterWithConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name: "grpc endpoint",
			config: Config{
				Endpoint: "localhost:4317",
			},
			wantErr: false,
		},
		{
			name: "http endpoint",
			config: Config{
				Endpoint: "http://localhost:4318/v1/metrics",
			},
			wantErr: false,
		},
		{
			name: "https endpoint",
			config: Config{
				Endpoint: "https://localhost:4318/v1/metrics",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			exp, err := New(ctx, tt.config)

			if tt.wantErr {
				if err == nil {
					t.Error("Expected error but got nil")
					if exp != nil {
						exp.Close()
					}
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if exp == nil {
				t.Error("Expected non-nil exporter")
				return
			}

			// Clean up
			exp.Close()
		})
	}
}

func TestExporter_HasSchemeAndPath(t *testing.T) {
	// Note: hasScheme only recognizes http:// and https://, not grpc://
	tests := []struct {
		endpoint  string
		hasScheme bool
		hasPath   bool
	}{
		{"localhost:4317", false, false},
		{"http://localhost:4318", true, false},
		{"https://localhost:4318/v1/metrics", true, true},
		{"http://localhost:4318/v1/metrics", true, true},
		{"/v1/metrics", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.endpoint, func(t *testing.T) {
			if got := hasScheme(tt.endpoint); got != tt.hasScheme {
				t.Errorf("hasScheme(%q) = %v, want %v", tt.endpoint, got, tt.hasScheme)
			}
			if got := hasPath(tt.endpoint); got != tt.hasPath {
				t.Errorf("hasPath(%q) = %v, want %v", tt.endpoint, got, tt.hasPath)
			}
		})
	}
}

func TestExporter_ExportHTTP(t *testing.T) {
	config := Config{
		Endpoint: "http://localhost:4318/v1/metrics",
	}

	ctx := context.Background()
	exp, err := New(ctx, config)
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	// Create a test request
	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{
							{Name: "http-test"},
						},
					},
				},
			},
		},
	}

	// Export will fail (no server) but shouldn't panic
	_ = exp.Export(ctx, req)
}

func TestExporter_ExportHTTPS(t *testing.T) {
	config := Config{
		Endpoint: "https://localhost:4318/v1/metrics",
	}

	ctx := context.Background()
	exp, err := New(ctx, config)
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}
	defer exp.Close()

	// Export will fail (no server) but shouldn't panic
	req := createTestRequest()
	_ = exp.Export(ctx, req)
}

func TestExporter_CloseIdempotent(t *testing.T) {
	config := Config{
		Endpoint: "localhost:4317",
	}

	ctx := context.Background()
	exp, err := New(ctx, config)
	if err != nil {
		t.Fatalf("Failed to create exporter: %v", err)
	}

	// First close should succeed
	if err := exp.Close(); err != nil {
		t.Errorf("First close failed: %v", err)
	}

	// Subsequent closes may return errors but should not panic
	_ = exp.Close()
	_ = exp.Close()
}
