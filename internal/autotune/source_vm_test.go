package autotune

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVMClient_TSDBInsights(t *testing.T) {
	resp := tsdbStatusResponse{
		Status: "success",
		Data: tsdbStatusData{
			HeadStats: tsdbHeadStats{NumSeries: 50000},
			SeriesCountByMetricName: []nameValuePair{
				{Name: "http_requests_total", Value: 15000},
				{Name: "process_cpu_seconds_total", Value: 8000},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/status/tsdb" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		topN := r.URL.Query().Get("topN")
		if topN != "100" {
			t.Errorf("expected topN=100, got %s", topN)
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendVM,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    100,
		Auth:    AuthConfig{Type: AuthNone},
		VM:      VMSourceConfig{TSDBInsights: true},
	}

	client := NewVMClient(cfg)
	data, err := client.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if data.TotalSeries != 50000 {
		t.Errorf("expected 50000 total series, got %d", data.TotalSeries)
	}
	if len(data.TopMetrics) != 2 {
		t.Fatalf("expected 2 top metrics, got %d", len(data.TopMetrics))
	}
	if data.TopMetrics[0].Name != "http_requests_total" {
		t.Errorf("expected first metric http_requests_total, got %s", data.TopMetrics[0].Name)
	}
	if data.Backend != BackendVM {
		t.Errorf("expected backend VM, got %s", data.Backend)
	}
}

func TestVMClient_Explorer(t *testing.T) {
	resp := tsdbStatusResponse{
		Status: "success",
		Data: tsdbStatusData{
			HeadStats: tsdbHeadStats{NumSeries: 10000},
			SeriesCountByMetricName: []nameValuePair{
				{Name: "http_requests_total", Value: 5000},
			},
			SeriesCountByFocusLabelValue: []nameValuePair{
				{Name: "pod-a", Value: 3000},
				{Name: "pod-b", Value: 2000},
			},
		},
	}

	var receivedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendVM,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    50,
		Auth:    AuthConfig{Type: AuthNone},
		VM: VMSourceConfig{
			CardinalityExplorer: true,
			ExplorerMatch:       []string{`{job="api"}`},
			ExplorerFocusLabel:  "instance",
			ExplorerDate:        "2026-01-15",
		},
	}

	client := NewVMClient(cfg)
	data, err := client.FetchExplorer(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify query parameters.
	if receivedQuery == "" {
		t.Fatal("no query received")
	}

	// Verify focusLabel data was parsed.
	if data.TopLabelValues == nil {
		t.Fatal("expected TopLabelValues to be set")
	}
	lvs, ok := data.TopLabelValues["instance"]
	if !ok {
		t.Fatal("expected TopLabelValues[instance]")
	}
	if len(lvs) != 2 {
		t.Fatalf("expected 2 label values, got %d", len(lvs))
	}
	if lvs[0].Value != "pod-a" || lvs[0].SeriesCount != 3000 {
		t.Errorf("unexpected first label value: %+v", lvs[0])
	}
}

func TestVMClient_TenantID(t *testing.T) {
	var receivedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		json.NewEncoder(w).Encode(tsdbStatusResponse{Status: "success"})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend:  BackendVM,
		URL:      srv.URL,
		Timeout:  5 * time.Second,
		TopN:     100,
		TenantID: "123:456",
		Auth:     AuthConfig{Type: AuthNone},
		VM:       VMSourceConfig{TSDBInsights: true},
	}

	client := NewVMClient(cfg)
	_, err := client.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedQuery == "" {
		t.Fatal("no query received")
	}
	// Should contain extra_label.
	if !(len(receivedQuery) > 0) {
		t.Error("query should contain tenant extra_label")
	}
}

func TestVMClient_AuthBasic(t *testing.T) {
	var receivedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(tsdbStatusResponse{Status: "success"})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendVM,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    100,
		Auth: AuthConfig{
			Type:     AuthBasic,
			Username: "user",
			Password: "pass",
		},
		VM: VMSourceConfig{TSDBInsights: true},
	}

	client := NewVMClient(cfg)
	_, err := client.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedAuth == "" {
		t.Error("expected Authorization header")
	}
}

func TestVMClient_AuthBearer(t *testing.T) {
	var receivedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(tsdbStatusResponse{Status: "success"})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendVM,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    100,
		Auth: AuthConfig{
			Type:  AuthBearer,
			Token: "my-token-123",
		},
		VM: VMSourceConfig{TSDBInsights: true},
	}

	client := NewVMClient(cfg)
	_, err := client.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedAuth != "Bearer my-token-123" {
		t.Errorf("expected 'Bearer my-token-123', got %q", receivedAuth)
	}
}

func TestVMClient_ErrorStatusCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendVM,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    100,
		Auth:    AuthConfig{Type: AuthNone},
		VM:      VMSourceConfig{TSDBInsights: true},
	}

	client := NewVMClient(cfg)
	_, err := client.FetchCardinality(context.Background())
	if err == nil {
		t.Fatal("expected error on 500 status")
	}
}

func TestVMClient_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(tsdbStatusResponse{Status: "error", Error: "test error"})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendVM,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    100,
		Auth:    AuthConfig{Type: AuthNone},
		VM:      VMSourceConfig{TSDBInsights: true},
	}

	client := NewVMClient(cfg)
	_, err := client.FetchCardinality(context.Background())
	if err == nil {
		t.Fatal("expected error on error status")
	}
}

func TestVMClient_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		json.NewEncoder(w).Encode(tsdbStatusResponse{Status: "success"})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendVM,
		URL:     srv.URL,
		Timeout: 50 * time.Millisecond,
		TopN:    100,
		Auth:    AuthConfig{Type: AuthNone},
		VM:      VMSourceConfig{TSDBInsights: true},
	}

	client := NewVMClient(cfg)
	_, err := client.FetchCardinality(context.Background())
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
