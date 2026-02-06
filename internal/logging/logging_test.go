package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestF(t *testing.T) {
	tests := []struct {
		name     string
		keyvals  []interface{}
		expected map[string]interface{}
	}{
		{
			name:     "single pair",
			keyvals:  []interface{}{"key", "value"},
			expected: map[string]interface{}{"key": "value"},
		},
		{
			name:     "multiple pairs",
			keyvals:  []interface{}{"key1", "val1", "key2", 123, "key3", true},
			expected: map[string]interface{}{"key1": "val1", "key2": 123, "key3": true},
		},
		{
			name:     "empty",
			keyvals:  []interface{}{},
			expected: map[string]interface{}{},
		},
		{
			name:     "odd number of args (last ignored)",
			keyvals:  []interface{}{"key1", "val1", "key2"},
			expected: map[string]interface{}{"key1": "val1"},
		},
		{
			name:     "non-string key (ignored)",
			keyvals:  []interface{}{123, "value", "realkey", "realvalue"},
			expected: map[string]interface{}{"realkey": "realvalue"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := F(tt.keyvals...)
			for k, v := range tt.expected {
				if result[k] != v {
					t.Errorf("F() key '%s' = %v, expected %v", k, result[k], v)
				}
			}
			if len(result) != len(tt.expected) {
				t.Errorf("F() returned %d fields, expected %d", len(result), len(tt.expected))
			}
		})
	}
}

func TestSetOutput(t *testing.T) {
	var buf bytes.Buffer

	// Save original output
	originalOutput := defaultLogger.output

	SetOutput(&buf)

	if defaultLogger.output != &buf {
		t.Error("SetOutput did not change output")
	}

	// Restore
	SetOutput(originalOutput)
}

func TestSetResource(t *testing.T) {
	originalResource := defaultLogger.resource
	defer func() {
		defaultLogger.mu.Lock()
		defaultLogger.resource = originalResource
		defaultLogger.mu.Unlock()
	}()

	res := map[string]string{
		"service.name":    "test-service",
		"service.version": "1.0.0",
	}
	SetResource(res)

	if defaultLogger.resource["service.name"] != "test-service" {
		t.Error("SetResource did not set service.name")
	}
	if defaultLogger.resource["service.version"] != "1.0.0" {
		t.Error("SetResource did not set service.version")
	}
}

func TestInfo(t *testing.T) {
	var buf bytes.Buffer
	originalOutput := defaultLogger.output
	SetOutput(&buf)
	defer SetOutput(originalOutput)

	Info("test message", F("key", "value"))

	output := buf.String()

	var entry LogEntry
	if err := json.Unmarshal([]byte(output), &entry); err != nil {
		t.Fatalf("failed to unmarshal log entry: %v", err)
	}

	if entry.SeverityText != "INFO" {
		t.Errorf("expected SeverityText 'INFO', got '%s'", entry.SeverityText)
	}
	if entry.SeverityNumber != 9 {
		t.Errorf("expected SeverityNumber 9, got %d", entry.SeverityNumber)
	}
	if entry.Body != "test message" {
		t.Errorf("expected Body 'test message', got '%s'", entry.Body)
	}
	if entry.Attributes["key"] != "value" {
		t.Errorf("expected attribute key='value', got '%v'", entry.Attributes["key"])
	}
	if entry.Timestamp == "" {
		t.Error("expected Timestamp to be set")
	}
}

func TestInfoWithoutFields(t *testing.T) {
	var buf bytes.Buffer
	originalOutput := defaultLogger.output
	SetOutput(&buf)
	defer SetOutput(originalOutput)

	Info("no fields")

	output := buf.String()

	var entry LogEntry
	if err := json.Unmarshal([]byte(output), &entry); err != nil {
		t.Fatalf("failed to unmarshal log entry: %v", err)
	}

	if entry.SeverityText != "INFO" {
		t.Errorf("expected SeverityText 'INFO', got '%s'", entry.SeverityText)
	}
	if entry.Body != "no fields" {
		t.Errorf("expected Body 'no fields', got '%s'", entry.Body)
	}
	if entry.Attributes != nil && len(entry.Attributes) > 0 {
		t.Errorf("expected no Attributes, got %v", entry.Attributes)
	}
}

func TestWarn(t *testing.T) {
	var buf bytes.Buffer
	originalOutput := defaultLogger.output
	SetOutput(&buf)
	defer SetOutput(originalOutput)

	Warn("warning message", F("warning", true))

	output := buf.String()

	var entry LogEntry
	if err := json.Unmarshal([]byte(output), &entry); err != nil {
		t.Fatalf("failed to unmarshal log entry: %v", err)
	}

	if entry.SeverityText != "WARN" {
		t.Errorf("expected SeverityText 'WARN', got '%s'", entry.SeverityText)
	}
	if entry.SeverityNumber != 13 {
		t.Errorf("expected SeverityNumber 13, got %d", entry.SeverityNumber)
	}
	if entry.Body != "warning message" {
		t.Errorf("expected Body 'warning message', got '%s'", entry.Body)
	}
}

func TestError(t *testing.T) {
	var buf bytes.Buffer
	originalOutput := defaultLogger.output
	SetOutput(&buf)
	defer SetOutput(originalOutput)

	Error("error message", F("error_code", 500))

	output := buf.String()

	var entry LogEntry
	if err := json.Unmarshal([]byte(output), &entry); err != nil {
		t.Fatalf("failed to unmarshal log entry: %v", err)
	}

	if entry.SeverityText != "ERROR" {
		t.Errorf("expected SeverityText 'ERROR', got '%s'", entry.SeverityText)
	}
	if entry.SeverityNumber != 17 {
		t.Errorf("expected SeverityNumber 17, got %d", entry.SeverityNumber)
	}
	if entry.Body != "error message" {
		t.Errorf("expected Body 'error message', got '%s'", entry.Body)
	}
}

func TestLoggerLog(t *testing.T) {
	var buf bytes.Buffer
	logger := &Logger{output: &buf}

	attrs := map[string]interface{}{"key": "value", "count": 42}
	logger.log(LevelInfo, "test", attrs)

	output := buf.String()

	// Verify it's valid JSON
	var entry LogEntry
	if err := json.Unmarshal([]byte(output), &entry); err != nil {
		t.Fatalf("failed to unmarshal log entry: %v", err)
	}

	// Verify OTEL fields
	if entry.SeverityText != "INFO" {
		t.Errorf("expected SeverityText 'INFO', got '%s'", entry.SeverityText)
	}
	if entry.SeverityNumber != 9 {
		t.Errorf("expected SeverityNumber 9, got %d", entry.SeverityNumber)
	}
	if entry.Body != "test" {
		t.Errorf("expected Body 'test', got '%s'", entry.Body)
	}
	if entry.Attributes["key"] != "value" {
		t.Errorf("expected attribute key='value'")
	}
	// JSON numbers are float64
	if entry.Attributes["count"].(float64) != 42 {
		t.Errorf("expected attribute count=42")
	}

	// Verify it ends with newline
	if !strings.HasSuffix(output, "\n") {
		t.Error("expected log entry to end with newline")
	}
}

func TestLoggerNilFields(t *testing.T) {
	var buf bytes.Buffer
	logger := &Logger{output: &buf}

	logger.log(LevelInfo, "test", nil)

	output := buf.String()

	var entry LogEntry
	if err := json.Unmarshal([]byte(output), &entry); err != nil {
		t.Fatalf("failed to unmarshal log entry: %v", err)
	}

	// Body should be in output
	if !strings.Contains(output, `"Body":"test"`) {
		t.Error("expected Body in output")
	}
}

func TestTimestampFormat(t *testing.T) {
	var buf bytes.Buffer
	originalOutput := defaultLogger.output
	SetOutput(&buf)
	defer SetOutput(originalOutput)

	Info("test")

	output := buf.String()

	var entry LogEntry
	if err := json.Unmarshal([]byte(output), &entry); err != nil {
		t.Fatalf("failed to unmarshal log entry: %v", err)
	}

	// Timestamp should be in RFC3339 format
	if !strings.Contains(entry.Timestamp, "T") || !strings.Contains(entry.Timestamp, "Z") {
		t.Errorf("Timestamp '%s' doesn't look like RFC3339 format", entry.Timestamp)
	}
}

func TestMultipleLogs(t *testing.T) {
	var buf bytes.Buffer
	originalOutput := defaultLogger.output
	SetOutput(&buf)
	defer SetOutput(originalOutput)

	Info("first")
	Info("second")
	Info("third")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 log lines, got %d", len(lines))
	}

	for i, line := range lines {
		var entry LogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("failed to unmarshal log line %d: %v", i, err)
		}
	}
}

func TestLogLevelConstants(t *testing.T) {
	if LevelInfo != "INFO" {
		t.Errorf("expected LevelInfo='INFO', got '%s'", LevelInfo)
	}
	if LevelWarn != "WARN" {
		t.Errorf("expected LevelWarn='WARN', got '%s'", LevelWarn)
	}
	if LevelError != "ERROR" {
		t.Errorf("expected LevelError='ERROR', got '%s'", LevelError)
	}
	if LevelFatal != "FATAL" {
		t.Errorf("expected LevelFatal='FATAL', got '%s'", LevelFatal)
	}
}

func TestSeverityNumbers(t *testing.T) {
	tests := []struct {
		level    Level
		expected int
	}{
		{LevelInfo, 9},
		{LevelWarn, 13},
		{LevelError, 17},
		{LevelFatal, 21},
	}

	for _, tt := range tests {
		t.Run(string(tt.level), func(t *testing.T) {
			if got := severityNumbers[tt.level]; got != tt.expected {
				t.Errorf("severity number for %s = %d, want %d", tt.level, got, tt.expected)
			}
		})
	}
}

func TestResourceIncluded(t *testing.T) {
	var buf bytes.Buffer
	originalOutput := defaultLogger.output
	originalResource := defaultLogger.resource
	SetOutput(&buf)
	SetResource(map[string]string{
		"service.name":    "metrics-governor",
		"service.version": "1.0.0",
	})
	defer func() {
		SetOutput(originalOutput)
		defaultLogger.mu.Lock()
		defaultLogger.resource = originalResource
		defaultLogger.mu.Unlock()
	}()

	Info("test with resource")

	output := buf.String()

	var entry LogEntry
	if err := json.Unmarshal([]byte(output), &entry); err != nil {
		t.Fatalf("failed to unmarshal log entry: %v", err)
	}

	if entry.Resource == nil {
		t.Fatal("expected Resource to be set")
	}
	if entry.Resource["service.name"] != "metrics-governor" {
		t.Errorf("expected service.name='metrics-governor', got '%s'", entry.Resource["service.name"])
	}
	if entry.Resource["service.version"] != "1.0.0" {
		t.Errorf("expected service.version='1.0.0', got '%s'", entry.Resource["service.version"])
	}
}

func TestResourceOmittedWhenNil(t *testing.T) {
	var buf bytes.Buffer
	logger := &Logger{output: &buf}

	logger.log(LevelInfo, "no resource", nil)

	output := buf.String()

	if strings.Contains(output, `"Resource"`) {
		t.Error("expected Resource to be omitted when nil")
	}
}

func TestFWithVariousTypes(t *testing.T) {
	fields := F(
		"string", "value",
		"int", 42,
		"int64", int64(100),
		"float", 3.14,
		"bool", true,
		"nil", nil,
	)

	if fields["string"] != "value" {
		t.Error("string field incorrect")
	}
	if fields["int"] != 42 {
		t.Error("int field incorrect")
	}
	if fields["int64"] != int64(100) {
		t.Error("int64 field incorrect")
	}
	if fields["float"] != 3.14 {
		t.Error("float field incorrect")
	}
	if fields["bool"] != true {
		t.Error("bool field incorrect")
	}
	if fields["nil"] != nil {
		t.Error("nil field incorrect")
	}
}

func TestSetHook(t *testing.T) {
	var buf bytes.Buffer
	originalOutput := defaultLogger.output
	SetOutput(&buf)
	defer func() {
		SetOutput(originalOutput)
		SetHook(nil)
	}()

	var hookCalls []struct {
		level Level
		msg   string
		attrs map[string]interface{}
	}

	SetHook(func(level Level, msg string, attrs map[string]interface{}) {
		hookCalls = append(hookCalls, struct {
			level Level
			msg   string
			attrs map[string]interface{}
		}{level, msg, attrs})
	})

	Info("hook test", F("key", "value"))
	Warn("hook warn")

	if len(hookCalls) != 2 {
		t.Fatalf("expected 2 hook calls, got %d", len(hookCalls))
	}
	if hookCalls[0].level != LevelInfo {
		t.Errorf("expected INFO, got %s", hookCalls[0].level)
	}
	if hookCalls[0].msg != "hook test" {
		t.Errorf("expected 'hook test', got %s", hookCalls[0].msg)
	}
	if hookCalls[0].attrs["key"] != "value" {
		t.Errorf("expected key=value in attrs")
	}
	if hookCalls[1].level != LevelWarn {
		t.Errorf("expected WARN, got %s", hookCalls[1].level)
	}
}

func TestSetHookNil(t *testing.T) {
	var buf bytes.Buffer
	originalOutput := defaultLogger.output
	SetOutput(&buf)
	defer SetOutput(originalOutput)

	// Setting nil hook should not panic
	SetHook(nil)
	Info("no hook")

	if buf.Len() == 0 {
		t.Error("expected output even without hook")
	}
}

func TestSeverityNumberPublicFunc(t *testing.T) {
	tests := []struct {
		level    Level
		expected int
	}{
		{LevelInfo, 9},
		{LevelWarn, 13},
		{LevelError, 17},
		{LevelFatal, 21},
	}
	for _, tt := range tests {
		if got := SeverityNumber(tt.level); got != tt.expected {
			t.Errorf("SeverityNumber(%s) = %d, want %d", tt.level, got, tt.expected)
		}
	}
	// Unknown level should return 0
	if got := SeverityNumber(Level("UNKNOWN")); got != 0 {
		t.Errorf("SeverityNumber(UNKNOWN) = %d, want 0", got)
	}
}

func TestHookCalledOutsideLock(t *testing.T) {
	// This test verifies that the hook is called outside the logger's mutex,
	// preventing deadlocks when a hook calls back into the logging package.
	// We use a sync.Once to prevent infinite recursion (hook -> Info -> hook -> ...).
	var buf bytes.Buffer
	originalOutput := defaultLogger.output
	SetOutput(&buf)
	defer func() {
		SetOutput(originalOutput)
		SetHook(nil)
	}()

	hookCalled := make(chan struct{}, 1)
	var reentryGuard int32 // atomic guard to prevent infinite recursion
	SetHook(func(level Level, msg string, attrs map[string]interface{}) {
		// Only re-enter once to prove it doesn't deadlock
		if val := atomic.AddInt32(&reentryGuard, 1); val == 1 {
			// This would deadlock if the hook were called inside the lock,
			// because Info() would try to acquire the same lock.
			Info("hook re-entry", F("source", "hook"))
			hookCalled <- struct{}{}
		}
	})

	Info("trigger hook")

	select {
	case <-hookCalled:
		// Success: hook completed without deadlock
	case <-time.After(2 * time.Second):
		t.Fatal("hook did not complete in time — possible deadlock")
	}

	// Verify both log lines were written
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) < 2 {
		t.Errorf("expected at least 2 log lines, got %d", len(lines))
	}
}

func TestOTELFieldNames(t *testing.T) {
	var buf bytes.Buffer
	logger := &Logger{output: &buf}

	logger.log(LevelWarn, "test otel", F("key", "value"))

	output := buf.String()

	// Verify OTEL-compatible field names are present
	required := []string{`"Timestamp"`, `"SeverityText"`, `"SeverityNumber"`, `"Body"`}
	for _, field := range required {
		if !strings.Contains(output, field) {
			t.Errorf("expected OTEL field %s in output: %s", field, output)
		}
	}

	// Verify old field names are NOT present
	old := []string{`"timestamp"`, `"level"`, `"message"`, `"fields"`}
	for _, field := range old {
		if strings.Contains(output, field) {
			t.Errorf("found old field name %s in output: %s", field, output)
		}
	}
}
