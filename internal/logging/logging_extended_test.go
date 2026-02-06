package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestLoggerLogWithFields(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	defer SetOutput(nil)

	fields := map[string]interface{}{
		"key1": "value1",
		"key2": 123,
		"key3": true,
	}

	Info("test message", fields)

	output := buf.String()

	// Verify JSON structure
	var entry LogEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &entry); err != nil {
		t.Fatalf("Failed to parse log output: %v", err)
	}

	if entry.SeverityText != string(LevelInfo) {
		t.Errorf("Expected SeverityText 'INFO', got '%s'", entry.SeverityText)
	}

	if entry.Body != "test message" {
		t.Errorf("Expected Body 'test message', got '%s'", entry.Body)
	}

	if entry.Attributes["key1"] != "value1" {
		t.Errorf("Expected attribute key1='value1', got '%v'", entry.Attributes["key1"])
	}
}

func TestLoggerWarn(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	defer SetOutput(nil)

	Warn("warning message", map[string]interface{}{"code": 500})

	output := buf.String()
	var entry LogEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &entry); err != nil {
		t.Fatalf("Failed to parse log output: %v", err)
	}

	if entry.SeverityText != string(LevelWarn) {
		t.Errorf("Expected SeverityText 'WARN', got '%s'", entry.SeverityText)
	}
}

func TestLoggerError(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	defer SetOutput(nil)

	Error("error message")

	output := buf.String()
	var entry LogEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &entry); err != nil {
		t.Fatalf("Failed to parse log output: %v", err)
	}

	if entry.SeverityText != string(LevelError) {
		t.Errorf("Expected SeverityText 'ERROR', got '%s'", entry.SeverityText)
	}
}

func TestLoggerFHelper(t *testing.T) {
	fields := F("key1", "value1", "key2", 123, "key3", true)

	if fields["key1"] != "value1" {
		t.Errorf("Expected key1='value1', got '%v'", fields["key1"])
	}
	if fields["key2"] != 123 {
		t.Errorf("Expected key2=123, got '%v'", fields["key2"])
	}
	if fields["key3"] != true {
		t.Errorf("Expected key3=true, got '%v'", fields["key3"])
	}
}

func TestLoggerFHelperOddArgs(t *testing.T) {
	// F with odd number of args should skip the last one
	fields := F("key1", "value1", "key2")

	if fields["key1"] != "value1" {
		t.Errorf("Expected key1='value1', got '%v'", fields["key1"])
	}
	if _, ok := fields["key2"]; ok {
		t.Error("key2 should not be present")
	}
}

func TestLoggerFHelperNonStringKey(t *testing.T) {
	// F with non-string key should skip that pair
	fields := F(123, "value1", "key2", "value2")

	if _, ok := fields["123"]; ok {
		t.Error("Non-string key should not be present")
	}
	if fields["key2"] != "value2" {
		t.Errorf("Expected key2='value2', got '%v'", fields["key2"])
	}
}

func TestLoggerInfoNoFields(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	defer SetOutput(nil)

	Info("simple message")

	output := buf.String()
	var entry LogEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &entry); err != nil {
		t.Fatalf("Failed to parse log output: %v", err)
	}

	if entry.Body != "simple message" {
		t.Errorf("Expected Body 'simple message', got '%s'", entry.Body)
	}
	if entry.Attributes != nil && len(entry.Attributes) > 0 {
		t.Error("Expected no Attributes")
	}
}

func TestLoggerWarnNoFields(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	defer SetOutput(nil)

	Warn("warning without fields")

	output := buf.String()
	if !strings.Contains(output, "warning without fields") {
		t.Error("Expected warning message in output")
	}
}

func TestLoggerErrorNoFields(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	defer SetOutput(nil)

	Error("error without fields")

	output := buf.String()
	if !strings.Contains(output, "error without fields") {
		t.Error("Expected error message in output")
	}
}

func TestLogEntryTimestamp(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	defer SetOutput(nil)

	Info("test")

	output := buf.String()
	var entry LogEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &entry); err != nil {
		t.Fatalf("Failed to parse log output: %v", err)
	}

	// Timestamp should be in RFC3339 format
	if entry.Timestamp == "" {
		t.Error("Expected Timestamp to be present")
	}
}

func TestSetOutputNil(t *testing.T) {
	// This should not panic
	SetOutput(nil)
}

func TestLoggerConcurrent(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	defer SetOutput(nil)

	done := make(chan struct{})

	// Log from multiple goroutines
	for i := 0; i < 10; i++ {
		go func(n int) {
			defer func() { done <- struct{}{} }()
			Info("concurrent log", F("goroutine", n))
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should have 10 log lines
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 10 {
		t.Errorf("Expected 10 log lines, got %d", len(lines))
	}
}
