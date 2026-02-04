package exporter

import (
	"os"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/queue"
	"go.uber.org/goleak"
)

func TestLeakCheck_QueuedExporter(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	tmpDir, err := os.MkdirTemp("", "queued-leak-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	exp := &mockExporter{}
	cfg := queue.Config{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 1 * time.Hour, // Long interval to avoid background activity
		MaxRetryDelay: 1 * time.Hour,
	}

	qe, err := NewQueued(exp, cfg)
	if err != nil {
		t.Fatalf("NewQueued() error = %v", err)
	}

	// Push a request
	req := createTestRequest()
	if err := qe.Export(nil, req); err != nil {
		t.Logf("Export error (expected if context is nil): %v", err)
	}

	if err := qe.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestLeakCheck_PRWQueuedExporter(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	tmpDir, err := os.MkdirTemp("", "prw-queued-leak-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	exp := &mockPRWExporter{}
	cfg := PRWQueueConfig{
		Path:          tmpDir,
		MaxSize:       100,
		RetryInterval: 1 * time.Hour,
		MaxRetryDelay: 1 * time.Hour,
	}

	qe, err := NewPRWQueued(exp, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued() error = %v", err)
	}

	if err := qe.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
