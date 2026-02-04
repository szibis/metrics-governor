package exporter

import (
	"fmt"
	"strings"
)

// ExportError is a structured error returned from export operations.
// It carries the error type, HTTP status code, and response message
// to enable smart retry and split-on-error decisions.
type ExportError struct {
	// Err is the underlying error.
	Err error
	// Type is the classified error type.
	Type ErrorType
	// StatusCode is the HTTP status code (0 for gRPC or network errors).
	StatusCode int
	// Message is the response body or error detail from the backend.
	Message string
}

// Error implements the error interface.
func (e *ExportError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("export error: type=%s status=%d", e.Type, e.StatusCode)
}

// Unwrap returns the underlying error for errors.Is/As support.
func (e *ExportError) Unwrap() error {
	return e.Err
}

// IsRetryable returns true if the error is transient and the same request
// may succeed on retry (server errors, network issues, timeouts, rate limits).
func (e *ExportError) IsRetryable() bool {
	switch e.Type {
	case ErrorTypeServerError, ErrorTypeNetwork, ErrorTypeTimeout, ErrorTypeRateLimit:
		return true
	default:
		return false
	}
}

// IsSplittable returns true if the error indicates the payload is too large
// and splitting the batch into smaller pieces may resolve the issue.
// This matches VictoriaMetrics "too big" errors and standard HTTP 413.
func (e *ExportError) IsSplittable() bool {
	if e.StatusCode == 413 {
		return true
	}
	// Check for payload-too-large patterns in the response message
	msg := strings.ToLower(e.Message)
	if (e.StatusCode == 400 || e.Type == ErrorTypeClientError) && containsPayloadTooLarge(msg) {
		return true
	}
	return false
}

// containsPayloadTooLarge checks for common backend error patterns indicating
// the request payload exceeds the server's size limit.
func containsPayloadTooLarge(msg string) bool {
	patterns := []string{
		"too big",
		"too large",
		"exceeds",
		"exceeding",
		"maxrequestsize",
		"max_request_size",
		"payload too large",
		"request entity too large",
		"body too large",
	}
	for _, p := range patterns {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}
