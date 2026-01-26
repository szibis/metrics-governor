package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
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

	if entry.Level != LevelInfo {
		t.Errorf("expected level 'info', got '%s'", entry.Level)
	}
	if entry.Message != "test message" {
		t.Errorf("expected message 'test message', got '%s'", entry.Message)
	}
	if entry.Fields["key"] != "value" {
		t.Errorf("expected field key='value', got '%v'", entry.Fields["key"])
	}
	if entry.Timestamp == "" {
		t.Error("expected timestamp to be set")
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

	if entry.Level != LevelInfo {
		t.Errorf("expected level 'info', got '%s'", entry.Level)
	}
	if entry.Message != "no fields" {
		t.Errorf("expected message 'no fields', got '%s'", entry.Message)
	}
	if entry.Fields != nil && len(entry.Fields) > 0 {
		t.Errorf("expected no fields, got %v", entry.Fields)
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

	if entry.Level != LevelWarn {
		t.Errorf("expected level 'warn', got '%s'", entry.Level)
	}
	if entry.Message != "warning message" {
		t.Errorf("expected message 'warning message', got '%s'", entry.Message)
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

	if entry.Level != LevelError {
		t.Errorf("expected level 'error', got '%s'", entry.Level)
	}
	if entry.Message != "error message" {
		t.Errorf("expected message 'error message', got '%s'", entry.Message)
	}
}

func TestLoggerLog(t *testing.T) {
	var buf bytes.Buffer
	logger := &Logger{output: &buf}

	fields := map[string]interface{}{"key": "value", "count": 42}
	logger.log(LevelInfo, "test", fields)

	output := buf.String()

	// Verify it's valid JSON
	var entry LogEntry
	if err := json.Unmarshal([]byte(output), &entry); err != nil {
		t.Fatalf("failed to unmarshal log entry: %v", err)
	}

	// Verify fields
	if entry.Level != LevelInfo {
		t.Errorf("expected level 'info', got '%s'", entry.Level)
	}
	if entry.Message != "test" {
		t.Errorf("expected message 'test', got '%s'", entry.Message)
	}
	if entry.Fields["key"] != "value" {
		t.Errorf("expected field key='value'")
	}
	// JSON numbers are float64
	if entry.Fields["count"].(float64) != 42 {
		t.Errorf("expected field count=42")
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

	// Fields should be omitted from JSON when nil
	if !strings.Contains(output, "\"message\":\"test\"") {
		t.Error("expected message in output")
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
		t.Errorf("timestamp '%s' doesn't look like RFC3339 format", entry.Timestamp)
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
	if LevelInfo != "info" {
		t.Errorf("expected LevelInfo='info', got '%s'", LevelInfo)
	}
	if LevelWarn != "warn" {
		t.Errorf("expected LevelWarn='warn', got '%s'", LevelWarn)
	}
	if LevelError != "error" {
		t.Errorf("expected LevelError='error', got '%s'", LevelError)
	}
	if LevelFatal != "fatal" {
		t.Errorf("expected LevelFatal='fatal', got '%s'", LevelFatal)
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
