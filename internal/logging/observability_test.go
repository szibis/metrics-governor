package logging

import (
	"bytes"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// getCounterValue reads the current value of a counter vec for given labels.
func getCounterValue(t *testing.T, cv *prometheus.CounterVec, labels ...string) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := cv.WithLabelValues(labels...).Write(m); err != nil {
		t.Fatalf("failed to read counter: %v", err)
	}
	return m.GetCounter().GetValue()
}

func TestLogMetrics_LevelAndComponent(t *testing.T) {
	var buf bytes.Buffer
	originalOutput := defaultLogger.output
	originalLevel := defaultLogger.minLevel
	SetOutput(&buf)
	SetLevel(LevelInfo) // Allow all output for this test
	defer func() {
		SetOutput(originalOutput)
		SetLevel(originalLevel)
	}()

	// Record baseline
	baseInfo := getCounterValue(t, logMessagesTotal, "INFO", "logging", "general")
	baseWarn := getCounterValue(t, logMessagesTotal, "WARN", "logging", "general")
	baseError := getCounterValue(t, logMessagesTotal, "ERROR", "logging", "general")

	Info("test info message")
	Warn("test warn message")
	Error("test error message")

	// Check counters incremented (component should be "logging" since we're in this package)
	afterInfo := getCounterValue(t, logMessagesTotal, "INFO", "logging", "general")
	afterWarn := getCounterValue(t, logMessagesTotal, "WARN", "logging", "general")
	afterError := getCounterValue(t, logMessagesTotal, "ERROR", "logging", "general")

	if afterInfo-baseInfo != 1 {
		t.Errorf("expected INFO counter to increment by 1, got %f", afterInfo-baseInfo)
	}
	if afterWarn-baseWarn != 1 {
		t.Errorf("expected WARN counter to increment by 1, got %f", afterWarn-baseWarn)
	}
	if afterError-baseError != 1 {
		t.Errorf("expected ERROR counter to increment by 1, got %f", afterError-baseError)
	}
}

func TestLogMetrics_ExplicitComponent(t *testing.T) {
	var buf bytes.Buffer
	originalOutput := defaultLogger.output
	originalLevel := defaultLogger.minLevel
	SetOutput(&buf)
	SetLevel(LevelInfo)
	defer func() {
		SetOutput(originalOutput)
		SetLevel(originalLevel)
	}()

	// Explicit component overrides auto-detected
	base := getCounterValue(t, logMessagesTotal, "ERROR", "custom_exporter", "export")
	Error("export failed", F("component", "custom_exporter"))
	after := getCounterValue(t, logMessagesTotal, "ERROR", "custom_exporter", "export")

	if after-base != 1 {
		t.Errorf("expected explicit component counter to increment by 1, got %f", after-base)
	}
}

func TestLogMetrics_OperationAutoDetect(t *testing.T) {
	var buf bytes.Buffer
	originalOutput := defaultLogger.output
	originalLevel := defaultLogger.minLevel
	SetOutput(&buf)
	SetLevel(LevelInfo)
	defer func() {
		SetOutput(originalOutput)
		SetLevel(originalLevel)
	}()

	tests := []struct {
		msg       string
		operation string
	}{
		{"circuit breaker opened", "circuit_breaker"},
		{"export failed", "export"},
		{"retry succeeded", "retry"},
		{"queue push failed", "queue"},
		{"drain completed", "drain"},
		{"backoff increased", "backoff"},
		{"config reload failed", "config"},
		{"limit exceeded", "limits"},
		{"cardinality tracked", "cardinality"},
		{"sharding endpoints changed", "sharding"},
		{"receiver started", "receive"}, // "receive" keyword in "receiver" matches first
		{"some generic message", "general"},
	}

	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			base := getCounterValue(t, logMessagesTotal, "INFO", "logging", tt.operation)
			Info(tt.msg)
			after := getCounterValue(t, logMessagesTotal, "INFO", "logging", tt.operation)
			if after-base != 1 {
				t.Errorf("expected operation=%q for msg=%q, counter did not increment", tt.operation, tt.msg)
			}
		})
	}
}

func TestLogMetrics_ExplicitOperation(t *testing.T) {
	var buf bytes.Buffer
	originalOutput := defaultLogger.output
	originalLevel := defaultLogger.minLevel
	SetOutput(&buf)
	SetLevel(LevelInfo)
	defer func() {
		SetOutput(originalOutput)
		SetLevel(originalLevel)
	}()

	// Explicit operation overrides auto-detected
	base := getCounterValue(t, logMessagesTotal, "WARN", "logging", "custom_op")
	Warn("some message", F("operation", "custom_op"))
	after := getCounterValue(t, logMessagesTotal, "WARN", "logging", "custom_op")

	if after-base != 1 {
		t.Errorf("expected explicit operation counter to increment by 1, got %f", after-base)
	}
}

func TestSetLevel_SuppressesOutput(t *testing.T) {
	var buf bytes.Buffer
	originalOutput := defaultLogger.output
	originalLevel := defaultLogger.minLevel
	SetOutput(&buf)
	defer func() {
		SetOutput(originalOutput)
		SetLevel(originalLevel)
	}()

	// Set level to ERROR — INFO and WARN should be suppressed
	SetLevel(LevelError)

	Info("should not appear")
	Warn("should not appear")
	Error("should appear")

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	// Only the ERROR message should appear in output
	if len(lines) != 1 {
		t.Errorf("expected 1 output line, got %d: %q", len(lines), output)
	}
	if !strings.Contains(output, "should appear") {
		t.Error("ERROR message should appear in output")
	}
	if strings.Contains(output, "should not appear") {
		t.Error("INFO/WARN messages should be suppressed")
	}
}

func TestSetLevel_MetricsStillEmitted(t *testing.T) {
	var buf bytes.Buffer
	originalOutput := defaultLogger.output
	originalLevel := defaultLogger.minLevel
	SetOutput(&buf)
	defer func() {
		SetOutput(originalOutput)
		SetLevel(originalLevel)
	}()

	// Set level to FATAL — everything below FATAL is suppressed
	SetLevel(LevelFatal)

	baseInfo := getCounterValue(t, logMessagesTotal, "INFO", "logging", "general")
	baseWarn := getCounterValue(t, logMessagesTotal, "WARN", "logging", "general")
	baseError := getCounterValue(t, logMessagesTotal, "ERROR", "logging", "general")

	Info("suppressed info")
	Warn("suppressed warn")
	Error("suppressed error")

	// Output should be empty (all suppressed)
	if buf.Len() > 0 {
		t.Errorf("expected no output with FATAL level, got: %q", buf.String())
	}

	// But metrics MUST still be incremented
	afterInfo := getCounterValue(t, logMessagesTotal, "INFO", "logging", "general")
	afterWarn := getCounterValue(t, logMessagesTotal, "WARN", "logging", "general")
	afterError := getCounterValue(t, logMessagesTotal, "ERROR", "logging", "general")

	if afterInfo-baseInfo != 1 {
		t.Error("INFO counter should increment even when output is suppressed")
	}
	if afterWarn-baseWarn != 1 {
		t.Error("WARN counter should increment even when output is suppressed")
	}
	if afterError-baseError != 1 {
		t.Error("ERROR counter should increment even when output is suppressed")
	}
}

func TestSetLevel_HookSuppressed(t *testing.T) {
	var buf bytes.Buffer
	originalOutput := defaultLogger.output
	originalLevel := defaultLogger.minLevel
	SetOutput(&buf)
	defer func() {
		SetOutput(originalOutput)
		SetLevel(originalLevel)
		SetHook(nil)
	}()

	hookCalls := 0
	SetHook(func(level Level, msg string, attrs map[string]interface{}) {
		hookCalls++
	})

	SetLevel(LevelError)

	Info("suppressed")
	Warn("suppressed")
	Error("visible")

	// Hook should only fire for ERROR (above minimum level)
	if hookCalls != 1 {
		t.Errorf("expected 1 hook call, got %d", hookCalls)
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected Level
	}{
		{"INFO", LevelInfo},
		{"info", LevelInfo},
		{"WARN", LevelWarn},
		{"warn", LevelWarn},
		{"WARNING", LevelWarn},
		{"ERROR", LevelError},
		{"error", LevelError},
		{"FATAL", LevelFatal},
		{"fatal", LevelFatal},
		{"unknown", LevelInfo}, // default
		{"", LevelInfo},        // default
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := ParseLevel(tt.input); got != tt.expected {
				t.Errorf("ParseLevel(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestGetLevel(t *testing.T) {
	originalLevel := defaultLogger.minLevel
	defer SetLevel(originalLevel)

	SetLevel(LevelWarn)
	if got := GetLevel(); got != LevelWarn {
		t.Errorf("GetLevel() = %q, want WARN", got)
	}

	SetLevel(LevelInfo)
	if got := GetLevel(); got != LevelInfo {
		t.Errorf("GetLevel() = %q, want INFO", got)
	}
}

func TestGetLevel_DefaultIsInfo(t *testing.T) {
	originalLevel := defaultLogger.minLevel
	defer SetLevel(originalLevel)

	// Set to empty string (uninitialized)
	defaultLogger.mu.Lock()
	defaultLogger.minLevel = ""
	defaultLogger.mu.Unlock()

	if got := GetLevel(); got != LevelInfo {
		t.Errorf("GetLevel() default = %q, want INFO", got)
	}
}

func TestDetectOperation(t *testing.T) {
	tests := []struct {
		msg      string
		expected string
	}{
		{"circuit breaker opened due to failures", "circuit_breaker"},
		{"circuit opened", "circuit_breaker"},
		{"export failed", "export"},
		{"retry succeeded after backoff", "retry"},
		{"queue push failed", "queue"},
		{"drain completed", "drain"},
		{"backoff delay increased", "backoff"},
		{"config reload success", "config"},
		{"reload completed", "config"},
		{"limit exceeded for rule", "limits"},
		{"bloom filter initialized", "cardinality"},
		{"cardinality tracking started", "cardinality"},
		{"sharding endpoints updated", "sharding"},
		{"startup complete", "lifecycle"},
		{"shutdown initiated", "lifecycle"},
		{"server started", "lifecycle"},
		{"some unknown message", "general"},
	}

	for _, tt := range tests {
		t.Run(tt.msg, func(t *testing.T) {
			if got := detectOperation(tt.msg); got != tt.expected {
				t.Errorf("detectOperation(%q) = %q, want %q", tt.msg, got, tt.expected)
			}
		})
	}
}

func TestCallerComponent_ViaLogCall(t *testing.T) {
	// Verify that callerComponent auto-detects the correct package name
	// when called through the normal Info/Warn/Error → log → callerComponent chain.
	// Since this test file is in internal/logging/, the component should be "logging".
	var buf bytes.Buffer
	originalOutput := defaultLogger.output
	originalLevel := defaultLogger.minLevel
	SetOutput(&buf)
	SetLevel(LevelInfo)
	defer func() {
		SetOutput(originalOutput)
		SetLevel(originalLevel)
	}()

	// The component auto-detected from this test will be "logging"
	// (since we're in the logging package). Verify via metrics.
	base := getCounterValue(t, logMessagesTotal, "INFO", "logging", "general")
	Info("generic test message") // "general" operation, "logging" component
	after := getCounterValue(t, logMessagesTotal, "INFO", "logging", "general")

	if after-base != 1 {
		t.Errorf("expected auto-detected component 'logging', counter did not increment (delta=%f)", after-base)
	}
}
