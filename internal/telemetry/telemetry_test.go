package telemetry

import (
	"context"
	"testing"

	"github.com/szibis/metrics-governor/internal/logging"
)

func TestInit_Disabled(t *testing.T) {
	tel, err := Init(context.Background(), Config{}, "test", "1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tel != nil {
		t.Error("expected nil telemetry when endpoint is empty")
	}
}

func TestInit_DefaultProtocol(t *testing.T) {
	cfg := Config{
		Endpoint: "localhost:4317",
		Insecure: true,
	}
	// Init will fail to connect (no server) but should not error on setup
	tel, err := Init(context.Background(), cfg, "test", "1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tel == nil {
		t.Fatal("expected non-nil telemetry")
	}
	defer tel.Shutdown(context.Background())

	if !tel.Enabled() {
		t.Error("expected telemetry to be enabled")
	}
	if tel.Logger() == nil {
		t.Error("expected logger to be non-nil")
	}
}

func TestInit_HTTPProtocol(t *testing.T) {
	cfg := Config{
		Endpoint: "localhost:4318",
		Protocol: "http",
		Insecure: true,
	}
	tel, err := Init(context.Background(), cfg, "test", "1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tel == nil {
		t.Fatal("expected non-nil telemetry")
	}
	defer tel.Shutdown(context.Background())

	if !tel.Enabled() {
		t.Error("expected telemetry to be enabled")
	}
}

func TestTelemetry_Nil(t *testing.T) {
	var tel *Telemetry
	if tel.Enabled() {
		t.Error("nil telemetry should not be enabled")
	}
	if tel.Logger() != nil {
		t.Error("nil telemetry logger should be nil")
	}
	if err := tel.Shutdown(context.Background()); err != nil {
		t.Errorf("nil telemetry shutdown should not error: %v", err)
	}
}

func TestNewLogHook_Nil(t *testing.T) {
	var tel *Telemetry
	hook := tel.NewLogHook()
	if hook != nil {
		t.Error("nil telemetry should return nil hook")
	}
}

func TestNewLogHook_Emits(t *testing.T) {
	cfg := Config{
		Endpoint: "localhost:4317",
		Insecure: true,
	}
	tel, err := Init(context.Background(), cfg, "test", "1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer tel.Shutdown(context.Background())

	hook := tel.NewLogHook()
	if hook == nil {
		t.Fatal("expected non-nil hook")
	}

	// Should not panic — the record is batched but the exporter
	// will fail to send (no server); that's fine for a unit test.
	hook(logging.LevelInfo, "test message", map[string]interface{}{
		"key": "value",
		"num": 42,
	})
	hook(logging.LevelWarn, "warn message", nil)
	hook(logging.LevelError, "error message", map[string]interface{}{
		"float": 3.14,
		"bool":  true,
		"nil":   nil,
	})
}

func TestToOTELSeverity(t *testing.T) {
	tests := []struct {
		level    logging.Level
		expected string
	}{
		{logging.LevelInfo, "INFO"},
		{logging.LevelWarn, "WARN"},
		{logging.LevelError, "ERROR"},
		{logging.LevelFatal, "FATAL"},
	}

	for _, tt := range tests {
		t.Run(string(tt.level), func(t *testing.T) {
			sev := toOTELSeverity(tt.level)
			if sev.String() != tt.expected {
				t.Errorf("toOTELSeverity(%s) = %s, want %s", tt.level, sev.String(), tt.expected)
			}
		})
	}
}

func TestInit_InvalidProtocol(t *testing.T) {
	// Invalid protocol falls through to gRPC default — should succeed without error
	cfg := Config{
		Endpoint: "localhost:4317",
		Protocol: "invalid-protocol",
		Insecure: true,
	}
	tel, err := Init(context.Background(), cfg, "test", "1.0.0")
	if err != nil {
		t.Fatalf("unexpected error for invalid protocol: %v", err)
	}
	if tel == nil {
		t.Fatal("expected non-nil telemetry (falls back to gRPC)")
	}
	defer tel.Shutdown(context.Background())

	if !tel.Enabled() {
		t.Error("expected telemetry to be enabled")
	}
}

func TestInit_EmptyProtocol(t *testing.T) {
	// Empty protocol should default to gRPC
	cfg := Config{
		Endpoint: "localhost:4317",
		Protocol: "",
		Insecure: true,
	}
	tel, err := Init(context.Background(), cfg, "test", "1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tel == nil {
		t.Fatal("expected non-nil telemetry")
	}
	defer tel.Shutdown(context.Background())
}

func TestShutdown_MultipleProviders(t *testing.T) {
	cfg := Config{
		Endpoint: "localhost:4317",
		Insecure: true,
	}
	tel, err := Init(context.Background(), cfg, "test", "1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Shutdown twice should not panic. Connection errors are expected since
	// there's no real OTLP collector at localhost:4317 in unit tests.
	err = tel.Shutdown(context.Background())
	t.Logf("first shutdown: %v", err)
	err = tel.Shutdown(context.Background())
	t.Logf("second shutdown: %v", err)
}

func TestToOTELValue(t *testing.T) {
	tests := []struct {
		name  string
		input interface{}
	}{
		{"string", "hello"},
		{"int", 42},
		{"int64", int64(100)},
		{"float64", 3.14},
		{"bool", true},
		{"nil", nil},
		{"struct", struct{ A int }{1}}, // fallback to fmt.Sprint
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := toOTELValue(tt.input)
			// Just verify it doesn't panic and returns something
			if v.Empty() && tt.input != nil {
				t.Errorf("toOTELValue(%v) returned empty value", tt.input)
			}
		})
	}
}
