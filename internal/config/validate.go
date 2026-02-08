package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// ValidationSeverity indicates the severity of a validation issue.
type ValidationSeverity string

const (
	// SeverityError indicates a configuration error that prevents startup.
	SeverityError ValidationSeverity = "error"
	// SeverityWarning indicates a potential issue that won't prevent startup.
	SeverityWarning ValidationSeverity = "warning"
)

// ValidationIssue represents a single validation finding.
type ValidationIssue struct {
	Severity ValidationSeverity `json:"severity"`
	Field    string             `json:"field"`
	Message  string             `json:"message"`
}

// ValidationResult holds the complete validation output.
type ValidationResult struct {
	Valid  bool              `json:"valid"`
	File   string            `json:"file"`
	Issues []ValidationIssue `json:"issues,omitempty"`
}

// JSON returns the validation result as formatted JSON.
func (r *ValidationResult) JSON() string {
	data, _ := json.MarshalIndent(r, "", "  ")
	return string(data)
}

// ValidateFile loads a YAML config file and validates it, returning structured results.
func ValidateFile(path string) *ValidationResult {
	result := &ValidationResult{
		Valid: true,
		File:  path,
	}

	// Check file exists
	info, err := os.Stat(path)
	if err != nil {
		result.Valid = false
		result.Issues = append(result.Issues, ValidationIssue{
			Severity: SeverityError,
			Field:    "file",
			Message:  fmt.Sprintf("cannot access file: %v", err),
		})
		return result
	}
	if info.IsDir() {
		result.Valid = false
		result.Issues = append(result.Issues, ValidationIssue{
			Severity: SeverityError,
			Field:    "file",
			Message:  "path is a directory, expected a file",
		})
		return result
	}

	// Parse YAML
	yamlCfg, err := LoadYAML(path)
	if err != nil {
		result.Valid = false
		result.Issues = append(result.Issues, ValidationIssue{
			Severity: SeverityError,
			Field:    "yaml",
			Message:  fmt.Sprintf("YAML parse error: %v", err),
		})
		return result
	}

	// Convert to Config and validate.
	// Start from defaults so fields not in YAML get valid default values.
	cfg := DefaultConfig()
	yamlOverlay := yamlCfg.ToConfig()
	mergeYAMLIntoConfig(cfg, yamlOverlay)
	cfg.ConfigFile = path

	if err := cfg.Validate(); err != nil {
		result.Valid = false
		// Parse the multi-error format from Validate()
		msg := err.Error()
		prefix := "configuration validation failed:\n  - "
		if strings.HasPrefix(msg, prefix) {
			items := strings.Split(strings.TrimPrefix(msg, prefix), "\n  - ")
			for _, item := range items {
				field, message := parseValidationError(item)
				result.Issues = append(result.Issues, ValidationIssue{
					Severity: SeverityError,
					Field:    field,
					Message:  message,
				})
			}
		} else {
			result.Issues = append(result.Issues, ValidationIssue{
				Severity: SeverityError,
				Field:    "config",
				Message:  msg,
			})
		}
	}

	// Additional warnings (non-fatal)
	addWarnings(cfg, result)

	return result
}

// parseValidationError extracts field and message from a validation error string.
// e.g. "memory-limit-ratio must be between 0.0 and 1.0, got 2.0" → field="memory-limit-ratio", message=...
func parseValidationError(s string) (string, string) {
	s = strings.TrimSpace(s)
	// Try to extract field name from the beginning (field-name must/should/is ...)
	for _, sep := range []string{" must ", " is ", " should "} {
		if idx := strings.Index(s, sep); idx > 0 {
			field := s[:idx]
			if !strings.Contains(field, " ") {
				return field, s
			}
		}
	}
	return "config", s
}

// addWarnings checks for non-fatal issues that are worth flagging.
func addWarnings(cfg *Config, result *ValidationResult) {
	// Warn about insecure exporter in non-localhost scenarios
	if cfg.ExporterInsecure && !isLocalhost(cfg.ExporterEndpoint) {
		result.Issues = append(result.Issues, ValidationIssue{
			Severity: SeverityWarning,
			Field:    "exporter.insecure",
			Message:  fmt.Sprintf("insecure connection to non-localhost endpoint %q", cfg.ExporterEndpoint),
		})
	}

	// Warn about very large buffer sizes
	if cfg.BufferSize > 1000000 {
		result.Issues = append(result.Issues, ValidationIssue{
			Severity: SeverityWarning,
			Field:    "buffer.size",
			Message:  fmt.Sprintf("very large buffer size (%d) may consume significant memory", cfg.BufferSize),
		})
	}

	// Warn about batch size larger than buffer
	if cfg.MaxBatchSize > cfg.BufferSize {
		result.Issues = append(result.Issues, ValidationIssue{
			Severity: SeverityWarning,
			Field:    "buffer.batch_size",
			Message:  fmt.Sprintf("batch_size (%d) is larger than buffer size (%d)", cfg.MaxBatchSize, cfg.BufferSize),
		})
	}

	// Warn about TLS cert files that don't exist
	if cfg.ReceiverTLSEnabled {
		checkFileWarning(cfg.ReceiverTLSCertFile, "receiver.tls.cert_file", result)
		checkFileWarning(cfg.ReceiverTLSKeyFile, "receiver.tls.key_file", result)
		if cfg.ReceiverTLSCAFile != "" {
			checkFileWarning(cfg.ReceiverTLSCAFile, "receiver.tls.ca_file", result)
		}
	}
	if cfg.ExporterTLSEnabled {
		if cfg.ExporterTLSCertFile != "" {
			checkFileWarning(cfg.ExporterTLSCertFile, "exporter.tls.cert_file", result)
		}
		if cfg.ExporterTLSKeyFile != "" {
			checkFileWarning(cfg.ExporterTLSKeyFile, "exporter.tls.key_file", result)
		}
		if cfg.ExporterTLSCAFile != "" {
			checkFileWarning(cfg.ExporterTLSCAFile, "exporter.tls.ca_file", result)
		}
	}
}

func checkFileWarning(path, field string, result *ValidationResult) {
	if path == "" {
		return
	}
	if _, err := os.Stat(path); err != nil {
		result.Issues = append(result.Issues, ValidationIssue{
			Severity: SeverityWarning,
			Field:    field,
			Message:  fmt.Sprintf("file not found: %s", path),
		})
	}
}

func isLocalhost(endpoint string) bool {
	return strings.HasPrefix(endpoint, "localhost") ||
		strings.HasPrefix(endpoint, "127.0.0.1") ||
		strings.HasPrefix(endpoint, "[::1]")
}

// mergeYAMLIntoConfig overlays non-zero YAML-derived values onto a defaults-based config.
// This ensures fields not present in the YAML keep their defaults rather than zero values.
func mergeYAMLIntoConfig(dst, src *Config) {
	// Profile & simplified config
	if src.Profile != "" {
		dst.Profile = src.Profile
	}
	if src.Parallelism != 0 {
		dst.Parallelism = src.Parallelism
	}
	if src.MemoryBudgetPct != 0 {
		dst.MemoryBudgetPct = src.MemoryBudgetPct
	}
	if src.ExportTimeoutBase != 0 {
		dst.ExportTimeoutBase = src.ExportTimeoutBase
	}
	if src.ResilienceLevel != "" {
		dst.ResilienceLevel = src.ResilienceLevel
	}

	// Receiver
	if src.GRPCListenAddr != "" {
		dst.GRPCListenAddr = src.GRPCListenAddr
	}
	if src.HTTPListenAddr != "" {
		dst.HTTPListenAddr = src.HTTPListenAddr
	}

	// Receiver TLS
	dst.ReceiverTLSEnabled = src.ReceiverTLSEnabled
	if src.ReceiverTLSCertFile != "" {
		dst.ReceiverTLSCertFile = src.ReceiverTLSCertFile
	}
	if src.ReceiverTLSKeyFile != "" {
		dst.ReceiverTLSKeyFile = src.ReceiverTLSKeyFile
	}
	if src.ReceiverTLSCAFile != "" {
		dst.ReceiverTLSCAFile = src.ReceiverTLSCAFile
	}
	dst.ReceiverTLSClientAuth = src.ReceiverTLSClientAuth

	// Exporter
	if src.ExporterEndpoint != "" {
		dst.ExporterEndpoint = src.ExporterEndpoint
	}
	if src.ExporterProtocol != "" {
		dst.ExporterProtocol = src.ExporterProtocol
	}
	dst.ExporterInsecure = src.ExporterInsecure
	if src.ExporterTimeout != 0 {
		dst.ExporterTimeout = src.ExporterTimeout
	}

	// Exporter TLS
	dst.ExporterTLSEnabled = src.ExporterTLSEnabled
	if src.ExporterTLSCertFile != "" {
		dst.ExporterTLSCertFile = src.ExporterTLSCertFile
	}
	if src.ExporterTLSKeyFile != "" {
		dst.ExporterTLSKeyFile = src.ExporterTLSKeyFile
	}
	if src.ExporterTLSCAFile != "" {
		dst.ExporterTLSCAFile = src.ExporterTLSCAFile
	}

	// Buffer
	if src.BufferSize != 0 {
		dst.BufferSize = src.BufferSize
	}
	if src.MaxBatchSize != 0 {
		dst.MaxBatchSize = src.MaxBatchSize
	}
	if src.FlushInterval != 0 {
		dst.FlushInterval = src.FlushInterval
	}

	// Stats
	if src.StatsAddr != "" {
		dst.StatsAddr = src.StatsAddr
	}

	// Limits
	dst.LimitsDryRun = src.LimitsDryRun

	// Performance
	if src.ExportConcurrency != 0 {
		dst.ExportConcurrency = src.ExportConcurrency
	}

	// Memory
	if src.MemoryLimitRatio != 0 {
		dst.MemoryLimitRatio = src.MemoryLimitRatio
	}
	if src.BufferMemoryPercent != 0 {
		dst.BufferMemoryPercent = src.BufferMemoryPercent
	}
	if src.QueueMemoryPercent != 0 {
		dst.QueueMemoryPercent = src.QueueMemoryPercent
	}

	// Buffer full policy
	if src.BufferFullPolicy != "" {
		dst.BufferFullPolicy = src.BufferFullPolicy
	}

	// Queue worker pool
	dst.QueueAlwaysQueue = src.QueueAlwaysQueue
	if src.QueueWorkers != 0 {
		dst.QueueWorkers = src.QueueWorkers
	}
}
