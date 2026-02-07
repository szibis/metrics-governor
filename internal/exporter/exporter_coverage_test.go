package exporter

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/compression"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ---------------------------------------------------------------------------
// classifyGRPCError
// ---------------------------------------------------------------------------

func TestClassifyGRPCError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantType ErrorType
	}{
		{
			name:     "nil error",
			err:      nil,
			wantType: ErrorTypeUnknown,
		},
		{
			name:     "DeadlineExceeded",
			err:      status.Error(codes.DeadlineExceeded, "deadline"),
			wantType: ErrorTypeTimeout,
		},
		{
			name:     "Unavailable",
			err:      status.Error(codes.Unavailable, "service unavailable"),
			wantType: ErrorTypeNetwork,
		},
		{
			name:     "Unauthenticated",
			err:      status.Error(codes.Unauthenticated, "unauthenticated"),
			wantType: ErrorTypeAuth,
		},
		{
			name:     "PermissionDenied",
			err:      status.Error(codes.PermissionDenied, "permission denied"),
			wantType: ErrorTypeAuth,
		},
		{
			name:     "ResourceExhausted",
			err:      status.Error(codes.ResourceExhausted, "rate limited"),
			wantType: ErrorTypeRateLimit,
		},
		{
			name:     "InvalidArgument",
			err:      status.Error(codes.InvalidArgument, "bad argument"),
			wantType: ErrorTypeClientError,
		},
		{
			name:     "FailedPrecondition",
			err:      status.Error(codes.FailedPrecondition, "precondition"),
			wantType: ErrorTypeClientError,
		},
		{
			name:     "OutOfRange",
			err:      status.Error(codes.OutOfRange, "out of range"),
			wantType: ErrorTypeClientError,
		},
		{
			name:     "Internal",
			err:      status.Error(codes.Internal, "internal error"),
			wantType: ErrorTypeServerError,
		},
		{
			name:     "Unknown code",
			err:      status.Error(codes.Unknown, "unknown"),
			wantType: ErrorTypeServerError,
		},
		{
			name:     "DataLoss",
			err:      status.Error(codes.DataLoss, "data loss"),
			wantType: ErrorTypeServerError,
		},
		{
			name:     "Aborted",
			err:      status.Error(codes.Aborted, "aborted"),
			wantType: ErrorTypeServerError,
		},
		{
			name:     "non-gRPC timeout error",
			err:      context.DeadlineExceeded,
			wantType: ErrorTypeTimeout,
		},
		{
			name:     "non-gRPC generic error",
			err:      errors.New("something went wrong"),
			wantType: ErrorTypeUnknown,
		},
		{
			name:     "non-gRPC connection refused",
			err:      errors.New("connection refused"),
			wantType: ErrorTypeNetwork,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyGRPCError(tt.err)
			if got != tt.wantType {
				t.Errorf("classifyGRPCError() = %v, want %v", got, tt.wantType)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// classifyHTTPStatusCode
// ---------------------------------------------------------------------------

func TestClassifyHTTPStatusCode(t *testing.T) {
	tests := []struct {
		code     int
		wantType ErrorType
	}{
		{401, ErrorTypeAuth},
		{403, ErrorTypeAuth},
		{429, ErrorTypeRateLimit},
		{400, ErrorTypeClientError},
		{404, ErrorTypeClientError},
		{405, ErrorTypeClientError},
		{413, ErrorTypeClientError},
		{500, ErrorTypeServerError},
		{502, ErrorTypeServerError},
		{503, ErrorTypeServerError},
		{504, ErrorTypeServerError},
		{200, ErrorTypeUnknown},
		{301, ErrorTypeUnknown},
		{100, ErrorTypeUnknown},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("status_%d", tt.code), func(t *testing.T) {
			got := classifyHTTPStatusCode(tt.code)
			if got != tt.wantType {
				t.Errorf("classifyHTTPStatusCode(%d) = %v, want %v", tt.code, got, tt.wantType)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// classifyError
// ---------------------------------------------------------------------------

// timeoutErr implements net.Error with Timeout() returning true.
type timeoutErr struct{}

func (e *timeoutErr) Error() string   { return "i/o timeout" }
func (e *timeoutErr) Timeout() bool   { return true }
func (e *timeoutErr) Temporary() bool { return true }

// networkErr implements net.Error with Timeout() returning false.
type networkErr struct{}

func (e *networkErr) Error() string   { return "network error" }
func (e *networkErr) Timeout() bool   { return false }
func (e *networkErr) Temporary() bool { return true }

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantType ErrorType
	}{
		{
			name:     "nil",
			err:      nil,
			wantType: ErrorTypeUnknown,
		},
		{
			name:     "context deadline exceeded",
			err:      context.DeadlineExceeded,
			wantType: ErrorTypeTimeout,
		},
		{
			name:     "net.Error timeout",
			err:      &timeoutErr{},
			wantType: ErrorTypeTimeout,
		},
		{
			name:     "net.Error non-timeout (network)",
			err:      &networkErr{},
			wantType: ErrorTypeNetwork,
		},
		{
			name:     "connection refused string",
			err:      errors.New("dial tcp: connection refused"),
			wantType: ErrorTypeNetwork,
		},
		{
			name:     "no such host string",
			err:      errors.New("dial tcp: lookup example.com: no such host"),
			wantType: ErrorTypeNetwork,
		},
		{
			name:     "network is unreachable string",
			err:      errors.New("network is unreachable"),
			wantType: ErrorTypeNetwork,
		},
		{
			name:     "connection reset string",
			err:      errors.New("connection reset by peer"),
			wantType: ErrorTypeNetwork,
		},
		{
			name:     "broken pipe string",
			err:      errors.New("write: broken pipe"),
			wantType: ErrorTypeNetwork,
		},
		{
			name:     "timeout string",
			err:      errors.New("operation timeout"),
			wantType: ErrorTypeTimeout,
		},
		{
			name:     "deadline exceeded string",
			err:      errors.New("context deadline exceeded"),
			wantType: ErrorTypeTimeout,
		},
		{
			name:     "generic error",
			err:      errors.New("something unexpected"),
			wantType: ErrorTypeUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyError(tt.err)
			if got != tt.wantType {
				t.Errorf("classifyError(%v) = %v, want %v", tt.err, got, tt.wantType)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isTimeoutError
// ---------------------------------------------------------------------------

func TestIsTimeoutError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"context.DeadlineExceeded", context.DeadlineExceeded, true},
		{"net timeout error", &timeoutErr{}, true},
		{"generic error", errors.New("foo"), false},
		{"non-timeout net.Error", &networkErr{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTimeoutError(tt.err)
			if got != tt.want {
				t.Errorf("isTimeoutError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isNetworkError
// ---------------------------------------------------------------------------

func TestIsNetworkError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"generic error", errors.New("foo"), false},
		{"net.Error non-timeout", &networkErr{}, true},
		{"net.Error timeout (not network)", &timeoutErr{}, false},
		{
			"DNS error",
			&net.DNSError{Err: "no such host", Name: "example.com"},
			true,
		},
		{
			"OpError",
			&net.OpError{Op: "dial", Err: errors.New("connection refused")},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNetworkError(tt.err)
			if got != tt.want {
				t.Errorf("isNetworkError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// containsLower / contains
// ---------------------------------------------------------------------------

func TestContainsLower(t *testing.T) {
	tests := []struct {
		s, substr string
		want      bool
	}{
		{"hello world", "world", true},
		{"hello world", "WORLD", false}, // containsLower is case-sensitive
		{"hello", "hello world", false},
		{"", "", true},
		{"a", "", true},
		{"", "a", false},
		{"abcdef", "cde", true},
		{"abcdef", "xyz", false},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_in_%s", tt.substr, tt.s), func(t *testing.T) {
			got := containsLower(tt.s, tt.substr)
			if got != tt.want {
				t.Errorf("containsLower(%q, %q) = %v, want %v", tt.s, tt.substr, got, tt.want)
			}
		})
	}
}

func TestContains_CaseInsensitive(t *testing.T) {
	tests := []struct {
		s, substr string
		want      bool
	}{
		{"Hello World", "hello", true},
		{"Hello World", "WORLD", true},
		{"Hello World", "missing", false},
		{"", "", true},
		{"abc", "", true},
		{"Connection Refused", "connection refused", true},
		{"TIMEOUT", "timeout", true},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s_in_%s", tt.substr, tt.s), func(t *testing.T) {
			got := contains(tt.s, tt.substr)
			if got != tt.want {
				t.Errorf("contains(%q, %q) = %v, want %v", tt.s, tt.substr, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// countDatapoints
// ---------------------------------------------------------------------------

func TestCountDatapoints(t *testing.T) {
	tests := []struct {
		name  string
		req   *colmetricspb.ExportMetricsServiceRequest
		count int64
	}{
		{
			name:  "nil request",
			req:   &colmetricspb.ExportMetricsServiceRequest{},
			count: 0,
		},
		{
			name: "gauge metric",
			req: &colmetricspb.ExportMetricsServiceRequest{
				ResourceMetrics: []*metricspb.ResourceMetrics{
					{
						ScopeMetrics: []*metricspb.ScopeMetrics{
							{
								Metrics: []*metricspb.Metric{
									{
										Data: &metricspb.Metric_Gauge{
											Gauge: &metricspb.Gauge{
												DataPoints: []*metricspb.NumberDataPoint{
													{}, {}, {},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			count: 3,
		},
		{
			name: "sum metric",
			req: &colmetricspb.ExportMetricsServiceRequest{
				ResourceMetrics: []*metricspb.ResourceMetrics{
					{
						ScopeMetrics: []*metricspb.ScopeMetrics{
							{
								Metrics: []*metricspb.Metric{
									{
										Data: &metricspb.Metric_Sum{
											Sum: &metricspb.Sum{
												DataPoints: []*metricspb.NumberDataPoint{
													{}, {},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			count: 2,
		},
		{
			name: "histogram metric",
			req: &colmetricspb.ExportMetricsServiceRequest{
				ResourceMetrics: []*metricspb.ResourceMetrics{
					{
						ScopeMetrics: []*metricspb.ScopeMetrics{
							{
								Metrics: []*metricspb.Metric{
									{
										Data: &metricspb.Metric_Histogram{
											Histogram: &metricspb.Histogram{
												DataPoints: []*metricspb.HistogramDataPoint{
													{}, {}, {}, {},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			count: 4,
		},
		{
			name: "exponential histogram metric",
			req: &colmetricspb.ExportMetricsServiceRequest{
				ResourceMetrics: []*metricspb.ResourceMetrics{
					{
						ScopeMetrics: []*metricspb.ScopeMetrics{
							{
								Metrics: []*metricspb.Metric{
									{
										Data: &metricspb.Metric_ExponentialHistogram{
											ExponentialHistogram: &metricspb.ExponentialHistogram{
												DataPoints: []*metricspb.ExponentialHistogramDataPoint{
													{}, {},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			count: 2,
		},
		{
			name: "summary metric",
			req: &colmetricspb.ExportMetricsServiceRequest{
				ResourceMetrics: []*metricspb.ResourceMetrics{
					{
						ScopeMetrics: []*metricspb.ScopeMetrics{
							{
								Metrics: []*metricspb.Metric{
									{
										Data: &metricspb.Metric_Summary{
											Summary: &metricspb.Summary{
												DataPoints: []*metricspb.SummaryDataPoint{
													{},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			count: 1,
		},
		{
			name: "mixed metric types",
			req: &colmetricspb.ExportMetricsServiceRequest{
				ResourceMetrics: []*metricspb.ResourceMetrics{
					{
						ScopeMetrics: []*metricspb.ScopeMetrics{
							{
								Metrics: []*metricspb.Metric{
									{
										Data: &metricspb.Metric_Gauge{
											Gauge: &metricspb.Gauge{
												DataPoints: []*metricspb.NumberDataPoint{{}, {}},
											},
										},
									},
									{
										Data: &metricspb.Metric_Sum{
											Sum: &metricspb.Sum{
												DataPoints: []*metricspb.NumberDataPoint{{}, {}, {}},
											},
										},
									},
									{
										Data: &metricspb.Metric_Histogram{
											Histogram: &metricspb.Histogram{
												DataPoints: []*metricspb.HistogramDataPoint{{}},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			count: 6,
		},
		{
			name: "nil gauge data",
			req: &colmetricspb.ExportMetricsServiceRequest{
				ResourceMetrics: []*metricspb.ResourceMetrics{
					{
						ScopeMetrics: []*metricspb.ScopeMetrics{
							{
								Metrics: []*metricspb.Metric{
									{
										Data: &metricspb.Metric_Gauge{
											Gauge: nil,
										},
									},
								},
							},
						},
					},
				},
			},
			count: 0,
		},
		{
			name: "nil sum data",
			req: &colmetricspb.ExportMetricsServiceRequest{
				ResourceMetrics: []*metricspb.ResourceMetrics{
					{
						ScopeMetrics: []*metricspb.ScopeMetrics{
							{
								Metrics: []*metricspb.Metric{
									{
										Data: &metricspb.Metric_Sum{
											Sum: nil,
										},
									},
								},
							},
						},
					},
				},
			},
			count: 0,
		},
		{
			name: "nil histogram data",
			req: &colmetricspb.ExportMetricsServiceRequest{
				ResourceMetrics: []*metricspb.ResourceMetrics{
					{
						ScopeMetrics: []*metricspb.ScopeMetrics{
							{
								Metrics: []*metricspb.Metric{
									{
										Data: &metricspb.Metric_Histogram{
											Histogram: nil,
										},
									},
								},
							},
						},
					},
				},
			},
			count: 0,
		},
		{
			name: "nil exponential histogram data",
			req: &colmetricspb.ExportMetricsServiceRequest{
				ResourceMetrics: []*metricspb.ResourceMetrics{
					{
						ScopeMetrics: []*metricspb.ScopeMetrics{
							{
								Metrics: []*metricspb.Metric{
									{
										Data: &metricspb.Metric_ExponentialHistogram{
											ExponentialHistogram: nil,
										},
									},
								},
							},
						},
					},
				},
			},
			count: 0,
		},
		{
			name: "nil summary data",
			req: &colmetricspb.ExportMetricsServiceRequest{
				ResourceMetrics: []*metricspb.ResourceMetrics{
					{
						ScopeMetrics: []*metricspb.ScopeMetrics{
							{
								Metrics: []*metricspb.Metric{
									{
										Data: &metricspb.Metric_Summary{
											Summary: nil,
										},
									},
								},
							},
						},
					},
				},
			},
			count: 0,
		},
		{
			name: "metric with no data (nil Data interface)",
			req: &colmetricspb.ExportMetricsServiceRequest{
				ResourceMetrics: []*metricspb.ResourceMetrics{
					{
						ScopeMetrics: []*metricspb.ScopeMetrics{
							{
								Metrics: []*metricspb.Metric{
									{Name: "no-data"},
								},
							},
						},
					},
				},
			},
			count: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countDatapoints(tt.req)
			if got != tt.count {
				t.Errorf("countDatapoints() = %d, want %d", got, tt.count)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// exportHTTP error paths
// ---------------------------------------------------------------------------

func TestExportHTTP_ServerErrorStatuses(t *testing.T) {
	statusCodes := []int{429, 500, 502, 503, 401, 403, 400}

	for _, code := range statusCodes {
		t.Run(fmt.Sprintf("status_%d", code), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
			}))
			defer server.Close()

			exp := &OTLPExporter{
				protocol:     ProtocolHTTP,
				timeout:      5 * time.Second,
				httpClient:   server.Client(),
				httpEndpoint: server.URL + "/v1/metrics",
			}

			req := &colmetricspb.ExportMetricsServiceRequest{
				ResourceMetrics: []*metricspb.ResourceMetrics{
					{
						ScopeMetrics: []*metricspb.ScopeMetrics{
							{
								Metrics: []*metricspb.Metric{{Name: "test"}},
							},
						},
					},
				},
			}

			err := exp.Export(context.Background(), req)
			if err == nil {
				t.Errorf("expected error for status %d, got nil", code)
			}
		})
	}
}

func TestExportHTTP_ConnectionError(t *testing.T) {
	// Use a port that is not listening to trigger a connection error.
	exp := &OTLPExporter{
		protocol:     ProtocolHTTP,
		timeout:      1 * time.Second,
		httpClient:   &http.Client{Timeout: 1 * time.Second},
		httpEndpoint: "http://127.0.0.1:1/v1/metrics", // port 1 is not listening
	}

	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{{Name: "test"}},
					},
				},
			},
		},
	}

	err := exp.Export(context.Background(), req)
	if err == nil {
		t.Error("expected error for connection failure, got nil")
	}
}

func TestExportHTTP_SuccessWithCompression(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify Content-Encoding is set
		if r.Header.Get("Content-Encoding") == "" {
			t.Error("expected Content-Encoding header to be set")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	exp := &OTLPExporter{
		protocol:     ProtocolHTTP,
		timeout:      5 * time.Second,
		httpClient:   server.Client(),
		httpEndpoint: server.URL + "/v1/metrics",
		compression: compression.Config{
			Type: compression.TypeGzip,
		},
	}

	req := &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: []*metricspb.ResourceMetrics{
			{
				ScopeMetrics: []*metricspb.ScopeMetrics{
					{
						Metrics: []*metricspb.Metric{{Name: "test"}},
					},
				},
			},
		},
	}

	err := exp.Export(context.Background(), req)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// classifyPRWError
// ---------------------------------------------------------------------------

func TestClassifyPRWError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantType ErrorType
	}{
		{
			name:     "nil",
			err:      nil,
			wantType: ErrorTypeUnknown,
		},
		{
			name:     "PRWClientError 400",
			err:      &PRWClientError{StatusCode: 400, Message: "bad request"},
			wantType: ErrorTypeClientError,
		},
		{
			name:     "PRWClientError 401",
			err:      &PRWClientError{StatusCode: 401, Message: "unauthorized"},
			wantType: ErrorTypeAuth,
		},
		{
			name:     "PRWClientError 403",
			err:      &PRWClientError{StatusCode: 403, Message: "forbidden"},
			wantType: ErrorTypeAuth,
		},
		{
			name:     "PRWClientError 429",
			err:      &PRWClientError{StatusCode: 429, Message: "rate limited"},
			wantType: ErrorTypeRateLimit,
		},
		{
			name:     "PRWServerError 500",
			err:      &PRWServerError{StatusCode: 500, Message: "internal server error"},
			wantType: ErrorTypeServerError,
		},
		{
			name:     "PRWServerError 503",
			err:      &PRWServerError{StatusCode: 503, Message: "service unavailable"},
			wantType: ErrorTypeServerError,
		},
		{
			name:     "timeout in error string",
			err:      errors.New("request timeout"),
			wantType: ErrorTypeTimeout,
		},
		{
			name:     "deadline exceeded in error string",
			err:      errors.New("context deadline exceeded"),
			wantType: ErrorTypeTimeout,
		},
		{
			name:     "context deadline in error string",
			err:      errors.New("context deadline while waiting"),
			wantType: ErrorTypeTimeout,
		},
		{
			name:     "connection refused",
			err:      errors.New("dial tcp: connection refused"),
			wantType: ErrorTypeNetwork,
		},
		{
			name:     "no such host",
			err:      errors.New("lookup example.com: no such host"),
			wantType: ErrorTypeNetwork,
		},
		{
			name:     "network is unreachable",
			err:      errors.New("network is unreachable"),
			wantType: ErrorTypeNetwork,
		},
		{
			name:     "connection reset",
			err:      errors.New("connection reset by peer"),
			wantType: ErrorTypeNetwork,
		},
		{
			name:     "broken pipe",
			err:      errors.New("write: broken pipe"),
			wantType: ErrorTypeNetwork,
		},
		{
			name:     "EOF",
			err:      errors.New("unexpected EOF"),
			wantType: ErrorTypeNetwork,
		},
		{
			name:     "i/o timeout",
			err:      errors.New("i/o timeout"),
			wantType: ErrorTypeTimeout, // "timeout" substring matches first
		},
		{
			name:     "generic error",
			err:      errors.New("something else"),
			wantType: ErrorTypeUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyPRWError(tt.err)
			if got != tt.wantType {
				t.Errorf("classifyPRWError() = %v, want %v", got, tt.wantType)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// classifyExportError (from queued.go)
// ---------------------------------------------------------------------------

func TestClassifyExportError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantType ErrorType
	}{
		{
			name:     "nil",
			err:      nil,
			wantType: ErrorTypeUnknown,
		},
		{
			name:     "timeout pattern",
			err:      errors.New("request timeout reached"),
			wantType: ErrorTypeTimeout,
		},
		{
			name:     "deadline exceeded pattern",
			err:      errors.New("context deadline exceeded"),
			wantType: ErrorTypeTimeout,
		},
		{
			name:     "context deadline pattern",
			err:      errors.New("context deadline while exporting"),
			wantType: ErrorTypeTimeout,
		},
		{
			name:     "connection refused pattern",
			err:      errors.New("dial tcp: connection refused"),
			wantType: ErrorTypeNetwork,
		},
		{
			name:     "no such host pattern",
			err:      errors.New("lookup: no such host"),
			wantType: ErrorTypeNetwork,
		},
		{
			name:     "network is unreachable pattern",
			err:      errors.New("network is unreachable"),
			wantType: ErrorTypeNetwork,
		},
		{
			name:     "connection reset pattern",
			err:      errors.New("connection reset by peer"),
			wantType: ErrorTypeNetwork,
		},
		{
			name:     "broken pipe pattern",
			err:      errors.New("write: broken pipe"),
			wantType: ErrorTypeNetwork,
		},
		{
			name:     "EOF pattern",
			err:      errors.New("unexpected EOF"),
			wantType: ErrorTypeNetwork,
		},
		{
			name:     "i/o timeout pattern",
			err:      errors.New("i/o timeout"),
			wantType: ErrorTypeTimeout, // "timeout" substring matches first
		},
		{
			name:     "status code 401",
			err:      errors.New("failed: status code: 401"),
			wantType: ErrorTypeAuth,
		},
		{
			name:     "status code 403",
			err:      errors.New("failed: status code: 403"),
			wantType: ErrorTypeAuth,
		},
		{
			name:     "Unauthenticated pattern",
			err:      errors.New("rpc error: Unauthenticated"),
			wantType: ErrorTypeAuth,
		},
		{
			name:     "PermissionDenied pattern",
			err:      errors.New("rpc error: PermissionDenied"),
			wantType: ErrorTypeAuth,
		},
		{
			name:     "status code 429",
			err:      errors.New("failed: status code: 429"),
			wantType: ErrorTypeRateLimit,
		},
		{
			name:     "ResourceExhausted pattern",
			err:      errors.New("rpc error: ResourceExhausted"),
			wantType: ErrorTypeRateLimit,
		},
		{
			name:     "status code 500",
			err:      errors.New("status code: 500"),
			wantType: ErrorTypeServerError,
		},
		{
			name:     "status code 503",
			err:      errors.New("status code: 503"),
			wantType: ErrorTypeServerError,
		},
		{
			name:     "Internal pattern",
			err:      errors.New("rpc error: Internal"),
			wantType: ErrorTypeServerError,
		},
		{
			name:     "Unavailable pattern",
			err:      errors.New("rpc error: Unavailable"),
			wantType: ErrorTypeServerError,
		},
		{
			name:     "status code 400",
			err:      errors.New("status code: 400"),
			wantType: ErrorTypeClientError,
		},
		{
			name:     "status code 404",
			err:      errors.New("status code: 404"),
			wantType: ErrorTypeClientError,
		},
		{
			name:     "generic error",
			err:      errors.New("something unknown"),
			wantType: ErrorTypeUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyExportError(tt.err)
			if got != tt.wantType {
				t.Errorf("classifyExportError(%v) = %v, want %v", tt.err, got, tt.wantType)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// getCircuitState (PRWQueuedExporter)
// ---------------------------------------------------------------------------

func TestPRWQueuedExporter_GetCircuitState(t *testing.T) {
	// Test with circuit breaker disabled (nil)
	mock := &mockPRWExporter{}
	cfg := PRWQueueConfig{
		RetryInterval:         time.Hour,
		MaxRetryDelay:         time.Hour,
		CircuitBreakerEnabled: false,
	}
	qe, err := NewPRWQueued(mock, cfg)
	if err != nil {
		t.Fatalf("NewPRWQueued error: %v", err)
	}

	state := qe.getCircuitState()
	if state != "disabled" {
		t.Errorf("getCircuitState() = %q, want %q", state, "disabled")
	}
	qe.Close()

	// Test with circuit breaker enabled -- closed state
	cfg2 := PRWQueueConfig{
		RetryInterval:           time.Hour,
		MaxRetryDelay:           time.Hour,
		CircuitBreakerEnabled:   true,
		CircuitFailureThreshold: 5,
		CircuitResetTimeout:     time.Hour,
	}
	qe2, err := NewPRWQueued(&mockPRWExporter{}, cfg2)
	if err != nil {
		t.Fatalf("NewPRWQueued error: %v", err)
	}

	state = qe2.getCircuitState()
	if state != "closed" {
		t.Errorf("getCircuitState() = %q, want %q", state, "closed")
	}

	// Open the circuit
	for i := 0; i < 5; i++ {
		qe2.circuitBreaker.RecordFailure()
	}
	state = qe2.getCircuitState()
	if state != "open" {
		t.Errorf("getCircuitState() = %q, want %q", state, "open")
	}

	qe2.Close()

	// Test half-open state
	cfg3 := PRWQueueConfig{
		RetryInterval:           time.Hour,
		MaxRetryDelay:           time.Hour,
		CircuitBreakerEnabled:   true,
		CircuitFailureThreshold: 2,
		CircuitResetTimeout:     50 * time.Millisecond,
	}
	qe3, err := NewPRWQueued(&mockPRWExporter{}, cfg3)
	if err != nil {
		t.Fatalf("NewPRWQueued error: %v", err)
	}

	qe3.circuitBreaker.RecordFailure()
	qe3.circuitBreaker.RecordFailure()

	// Wait for reset timeout
	time.Sleep(100 * time.Millisecond)
	qe3.circuitBreaker.AllowRequest() // triggers half-open

	state = qe3.getCircuitState()
	if state != "half_open" {
		t.Errorf("getCircuitState() = %q, want %q", state, "half_open")
	}
	qe3.Close()
}

// ---------------------------------------------------------------------------
// IsPRWRetryableError
// ---------------------------------------------------------------------------

func TestIsPRWRetryableError_Extended(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"server error (retryable)", &PRWServerError{StatusCode: 500, Message: "err"}, true},
		{"client error (not retryable)", &PRWClientError{StatusCode: 400, Message: "err"}, false},
		{"generic error (retryable)", errors.New("connection refused"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsPRWRetryableError(tt.err)
			if got != tt.want {
				t.Errorf("IsPRWRetryableError() = %v, want %v", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// toLower
// ---------------------------------------------------------------------------

func TestToLower(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"Hello World", "hello world"},
		{"ALLCAPS", "allcaps"},
		{"lowercase", "lowercase"},
		{"MiXeD123", "mixed123"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := toLower(tt.input)
			if got != tt.want {
				t.Errorf("toLower(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Case-insensitive string matching (replaces removed containsStr/toLowerStr)
// ---------------------------------------------------------------------------

func TestCaseInsensitiveContains(t *testing.T) {
	tests := []struct {
		s, substr string
		want      bool
	}{
		{"Hello World", "hello", true},
		{"Hello World", "WORLD", true},
		{"Hello World", "missing", false},
		{"", "", true},
		{"abc", "", true},
		{"Connection Refused", "connection refused", true},
		{"TIMEOUT occurred", "timeout", true},
		{"short", "very long string", false},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q_in_%q", tt.substr, tt.s), func(t *testing.T) {
			got := strings.Contains(strings.ToLower(tt.s), strings.ToLower(tt.substr))
			if got != tt.want {
				t.Errorf("Contains(%q, %q) = %v, want %v", tt.s, tt.substr, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Export unsupported protocol
// ---------------------------------------------------------------------------

func TestExport_UnsupportedProtocol(t *testing.T) {
	exp := &OTLPExporter{
		protocol: Protocol("invalid"),
		timeout:  5 * time.Second,
	}

	req := &colmetricspb.ExportMetricsServiceRequest{}
	err := exp.Export(context.Background(), req)
	if err == nil {
		t.Error("expected error for unsupported protocol, got nil")
	}
}

// ---------------------------------------------------------------------------
// Close with nil connections
// ---------------------------------------------------------------------------

func TestClose_NilConnections(t *testing.T) {
	// gRPC with nil connection
	exp := &OTLPExporter{protocol: ProtocolGRPC}
	if err := exp.Close(); err != nil {
		t.Errorf("Close() with nil gRPC conn returned error: %v", err)
	}

	// HTTP with nil client
	exp2 := &OTLPExporter{protocol: ProtocolHTTP}
	if err := exp2.Close(); err != nil {
		t.Errorf("Close() with nil HTTP client returned error: %v", err)
	}

	// Unknown protocol
	exp3 := &OTLPExporter{protocol: Protocol("invalid")}
	if err := exp3.Close(); err != nil {
		t.Errorf("Close() with unknown protocol returned error: %v", err)
	}
}
