package exporter

import (
	"errors"
	"fmt"
	"testing"
)

func TestExportError_Error(t *testing.T) {
	e := &ExportError{
		Err:        fmt.Errorf("unexpected status code: 400"),
		Type:       ErrorTypeClientError,
		StatusCode: 400,
		Message:    "too big data size",
	}
	if e.Error() != "unexpected status code: 400" {
		t.Errorf("unexpected Error(): %s", e.Error())
	}
}

func TestExportError_ErrorNilErr(t *testing.T) {
	e := &ExportError{
		Type:       ErrorTypeClientError,
		StatusCode: 400,
	}
	got := e.Error()
	if got != "export error: type=client_error status=400" {
		t.Errorf("unexpected Error() with nil Err: %s", got)
	}
}

func TestExportError_Unwrap(t *testing.T) {
	inner := fmt.Errorf("inner error")
	e := &ExportError{Err: inner}
	if !errors.Is(e, inner) {
		t.Error("expected errors.Is to match inner error")
	}
}

func TestExportError_ErrorsAs(t *testing.T) {
	inner := fmt.Errorf("status 400: too big")
	e := &ExportError{Err: inner, Type: ErrorTypeClientError, StatusCode: 400}
	wrapped := fmt.Errorf("export failed: %w", e)

	var exportErr *ExportError
	if !errors.As(wrapped, &exportErr) {
		t.Fatal("expected errors.As to find ExportError")
	}
	if exportErr.StatusCode != 400 {
		t.Errorf("expected StatusCode 400, got %d", exportErr.StatusCode)
	}
}

func TestExportError_IsRetryable(t *testing.T) {
	tests := []struct {
		name     string
		errType  ErrorType
		expected bool
	}{
		{"server_error", ErrorTypeServerError, true},
		{"network", ErrorTypeNetwork, true},
		{"timeout", ErrorTypeTimeout, true},
		{"rate_limit", ErrorTypeRateLimit, true},
		{"client_error", ErrorTypeClientError, false},
		{"auth", ErrorTypeAuth, false},
		{"unknown", ErrorTypeUnknown, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &ExportError{Type: tt.errType}
			if got := e.IsRetryable(); got != tt.expected {
				t.Errorf("IsRetryable() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestExportError_IsSplittable(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		errType    ErrorType
		message    string
		expected   bool
	}{
		{"413_status", 413, ErrorTypeClientError, "", true},
		{"400_too_big", 400, ErrorTypeClientError, "too big data size exceeding opentelemetry.maxRequestSize", true},
		{"400_too_large", 400, ErrorTypeClientError, "request body too large", true},
		{"400_exceeds", 400, ErrorTypeClientError, "data exceeds max size", true},
		{"400_exceeding", 400, ErrorTypeClientError, "exceeding maxRequestSize=16000000", true},
		{"400_payload_too_large", 400, ErrorTypeClientError, "payload too large", true},
		{"400_max_request_size", 400, ErrorTypeClientError, "maxRequestSize limit reached", true},
		{"400_unrelated", 400, ErrorTypeClientError, "invalid metric name", false},
		{"500_too_big", 500, ErrorTypeServerError, "too big data size", false},
		{"401_auth", 401, ErrorTypeAuth, "unauthorized", false},
		{"200_ok", 200, ErrorTypeUnknown, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &ExportError{
				Type:       tt.errType,
				StatusCode: tt.statusCode,
				Message:    tt.message,
			}
			if got := e.IsSplittable(); got != tt.expected {
				t.Errorf("IsSplittable() = %v, want %v", got, tt.expected)
			}
		})
	}
}
