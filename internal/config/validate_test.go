package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateFile_ValidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
receiver:
  grpc:
    address: ":4317"
  http:
    address: ":4318"
exporter:
  endpoint: "localhost:4317"
  protocol: "grpc"
  insecure: true
buffer:
  size: 10000
  batch_size: 1000
  flush_interval: "5s"
stats:
  address: ":9090"
`), 0644)

	result := ValidateFile(path)
	if !result.Valid {
		t.Fatalf("expected valid config, got issues: %s", result.JSON())
	}
	if result.File != path {
		t.Errorf("expected file %q, got %q", path, result.File)
	}
}

func TestValidateFile_FileNotFound(t *testing.T) {
	result := ValidateFile("/nonexistent/config.yaml")
	if result.Valid {
		t.Fatal("expected invalid for missing file")
	}
	if len(result.Issues) == 0 {
		t.Fatal("expected at least one issue")
	}
	if result.Issues[0].Severity != SeverityError {
		t.Errorf("expected error severity, got %s", result.Issues[0].Severity)
	}
	if result.Issues[0].Field != "file" {
		t.Errorf("expected field 'file', got %q", result.Issues[0].Field)
	}
}

func TestValidateFile_Directory(t *testing.T) {
	dir := t.TempDir()
	result := ValidateFile(dir)
	if result.Valid {
		t.Fatal("expected invalid for directory")
	}
	if !strings.Contains(result.Issues[0].Message, "directory") {
		t.Errorf("expected directory error, got %q", result.Issues[0].Message)
	}
}

func TestValidateFile_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	os.WriteFile(path, []byte(`
receiver:
  grpc:
    address: ":4317"
  invalid_indent
    bad: true
`), 0644)

	result := ValidateFile(path)
	if result.Valid {
		t.Fatal("expected invalid for bad YAML")
	}
	if result.Issues[0].Field != "yaml" {
		t.Errorf("expected field 'yaml', got %q", result.Issues[0].Field)
	}
}

func TestValidateFile_ValidationErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
exporter:
  endpoint: "localhost:4317"
  protocol: "invalid_protocol"
memory:
  limit_ratio: 2.5
`), 0644)

	result := ValidateFile(path)
	if result.Valid {
		t.Fatal("expected invalid for validation errors")
	}

	// Should have at least 2 errors (bad protocol, bad memory ratio)
	errorCount := 0
	for _, issue := range result.Issues {
		if issue.Severity == SeverityError {
			errorCount++
		}
	}
	if errorCount < 2 {
		t.Errorf("expected at least 2 errors, got %d: %s", errorCount, result.JSON())
	}
}

func TestValidateFile_Warnings_InsecureEndpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
exporter:
  endpoint: "remote-host:4317"
  protocol: "grpc"
  insecure: true
`), 0644)

	result := ValidateFile(path)
	if !result.Valid {
		t.Fatalf("expected valid config (warnings only), got: %s", result.JSON())
	}

	foundWarning := false
	for _, issue := range result.Issues {
		if issue.Severity == SeverityWarning && strings.Contains(issue.Message, "insecure") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Error("expected insecure endpoint warning")
	}
}

func TestValidateFile_Warnings_LargeBuffer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
exporter:
  endpoint: "localhost:4317"
  protocol: "grpc"
buffer:
  size: 5000000
  batch_size: 1000
`), 0644)

	result := ValidateFile(path)
	if !result.Valid {
		t.Fatalf("expected valid (warnings only), got: %s", result.JSON())
	}

	foundWarning := false
	for _, issue := range result.Issues {
		if issue.Severity == SeverityWarning && strings.Contains(issue.Message, "large buffer") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Error("expected large buffer warning")
	}
}

func TestValidateFile_Warnings_BatchLargerThanBuffer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
exporter:
  endpoint: "localhost:4317"
  protocol: "grpc"
buffer:
  size: 100
  batch_size: 500
`), 0644)

	result := ValidateFile(path)

	foundWarning := false
	for _, issue := range result.Issues {
		if issue.Severity == SeverityWarning && strings.Contains(issue.Message, "batch_size") {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Error("expected batch > buffer warning")
	}
}

func TestValidateFile_Warnings_TLSFileNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
exporter:
  endpoint: "localhost:4317"
  protocol: "grpc"
receiver:
  tls:
    enabled: true
    cert_file: "/nonexistent/tls.crt"
    key_file: "/nonexistent/tls.key"
`), 0644)

	result := ValidateFile(path)

	warningCount := 0
	for _, issue := range result.Issues {
		if issue.Severity == SeverityWarning && strings.Contains(issue.Message, "file not found") {
			warningCount++
		}
	}
	if warningCount < 2 {
		t.Errorf("expected at least 2 TLS file warnings, got %d: %s", warningCount, result.JSON())
	}
}

func TestValidateFile_JSONOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
exporter:
  endpoint: "localhost:4317"
  protocol: "grpc"
`), 0644)

	result := ValidateFile(path)
	jsonStr := result.JSON()
	if !strings.Contains(jsonStr, `"valid"`) {
		t.Error("JSON output should contain 'valid' field")
	}
	if !strings.Contains(jsonStr, `"file"`) {
		t.Error("JSON output should contain 'file' field")
	}
}

func TestParseValidationError(t *testing.T) {
	tests := []struct {
		input     string
		wantField string
	}{
		{"memory-limit-ratio must be between 0.0 and 1.0", "memory-limit-ratio"},
		{"exporter-protocol must be 'grpc' or 'http'", "exporter-protocol"},
		{"sharding-headless-service is required when sharding-enabled=true", "sharding-headless-service"},
		{"something else entirely", "config"},
	}

	for _, tt := range tests {
		field, _ := parseValidationError(tt.input)
		if field != tt.wantField {
			t.Errorf("parseValidationError(%q): field=%q, want %q", tt.input, field, tt.wantField)
		}
	}
}

func TestIsLocalhost(t *testing.T) {
	tests := []struct {
		endpoint string
		want     bool
	}{
		{"localhost:4317", true},
		{"127.0.0.1:4317", true},
		{"[::1]:4317", true},
		{"remote-host:4317", false},
		{"my-collector.namespace.svc:4317", false},
	}

	for _, tt := range tests {
		got := isLocalhost(tt.endpoint)
		if got != tt.want {
			t.Errorf("isLocalhost(%q) = %v, want %v", tt.endpoint, got, tt.want)
		}
	}
}
