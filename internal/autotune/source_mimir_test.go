package autotune

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMimirClient_FetchCardinality(t *testing.T) {
	resp := mimirCardinalityResponse{
		SeriesCountTotal: 1307,
		Labels: []mimirLabelCardinality{
			{
				LabelName:        "__name__",
				LabelValuesCount: 162,
				SeriesCount:      1307,
				Cardinality: []mimirValueCardinality{
					{LabelValue: "http_requests_total", SeriesCount: 67},
					{LabelValue: "process_cpu_seconds_total", SeriesCount: 42},
				},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/cardinality/label_values" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		labelNames := r.URL.Query().Get("label_names[]")
		if labelNames != "__name__" {
			t.Errorf("expected label_names[]=__name__, got %s", labelNames)
		}
		countMethod := r.URL.Query().Get("count_method")
		if countMethod != "active" {
			t.Errorf("expected count_method=active, got %s", countMethod)
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendMimir,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    100,
		Auth:    AuthConfig{Type: AuthNone},
		Mimir:   MimirSourceConfig{CountMethod: "active"},
	}

	client := NewMimirClient(cfg)
	data, err := client.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if data.TotalSeries != 1307 {
		t.Errorf("expected 1307 total series, got %d", data.TotalSeries)
	}
	if len(data.TopMetrics) != 2 {
		t.Fatalf("expected 2 top metrics, got %d", len(data.TopMetrics))
	}
	if data.TopMetrics[0].Name != "http_requests_total" {
		t.Errorf("expected first metric http_requests_total, got %s", data.TopMetrics[0].Name)
	}
	if data.TopMetrics[0].SeriesCount != 67 {
		t.Errorf("expected 67 series, got %d", data.TopMetrics[0].SeriesCount)
	}
	if data.Backend != BackendMimir {
		t.Errorf("expected backend Mimir, got %s", data.Backend)
	}
}

func TestMimirClient_TenantHeader(t *testing.T) {
	var receivedOrgID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedOrgID = r.Header.Get("X-Scope-OrgID")
		json.NewEncoder(w).Encode(mimirCardinalityResponse{SeriesCountTotal: 100})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend:  BackendMimir,
		URL:      srv.URL,
		Timeout:  5 * time.Second,
		TopN:     100,
		TenantID: "my-tenant",
		Auth:     AuthConfig{Type: AuthNone},
		Mimir:    MimirSourceConfig{CountMethod: "active"},
	}

	client := NewMimirClient(cfg)
	_, err := client.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedOrgID != "my-tenant" {
		t.Errorf("expected X-Scope-OrgID=my-tenant, got %q", receivedOrgID)
	}
}

func TestMimirClient_InMemoryCountMethod(t *testing.T) {
	var receivedCountMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedCountMethod = r.URL.Query().Get("count_method")
		json.NewEncoder(w).Encode(mimirCardinalityResponse{SeriesCountTotal: 100})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendMimir,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    100,
		Auth:    AuthConfig{Type: AuthNone},
		Mimir:   MimirSourceConfig{CountMethod: "inmemory"},
	}

	client := NewMimirClient(cfg)
	_, err := client.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedCountMethod != "inmemory" {
		t.Errorf("expected count_method=inmemory, got %q", receivedCountMethod)
	}
}

func TestMimirClient_ErrorStatusCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendMimir,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    100,
		Auth:    AuthConfig{Type: AuthNone},
	}

	client := NewMimirClient(cfg)
	_, err := client.FetchCardinality(context.Background())
	if err == nil {
		t.Fatal("expected error on 500 status")
	}
}

func TestMimirClient_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		json.NewEncoder(w).Encode(mimirCardinalityResponse{})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendMimir,
		URL:     srv.URL,
		Timeout: 50 * time.Millisecond,
		TopN:    100,
		Auth:    AuthConfig{Type: AuthNone},
	}

	client := NewMimirClient(cfg)
	_, err := client.FetchCardinality(context.Background())
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestMimirClient_SkipsNonNameLabels(t *testing.T) {
	resp := mimirCardinalityResponse{
		SeriesCountTotal: 500,
		Labels: []mimirLabelCardinality{
			{
				LabelName:   "job",
				SeriesCount: 500,
				Cardinality: []mimirValueCardinality{
					{LabelValue: "api", SeriesCount: 300},
				},
			},
			{
				LabelName:   "__name__",
				SeriesCount: 500,
				Cardinality: []mimirValueCardinality{
					{LabelValue: "metric_a", SeriesCount: 200},
				},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendMimir,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    100,
		Auth:    AuthConfig{Type: AuthNone},
	}

	client := NewMimirClient(cfg)
	data, err := client.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only __name__ cardinality should be extracted.
	if len(data.TopMetrics) != 1 {
		t.Fatalf("expected 1 top metric (from __name__ only), got %d", len(data.TopMetrics))
	}
	if data.TopMetrics[0].Name != "metric_a" {
		t.Errorf("expected metric_a, got %s", data.TopMetrics[0].Name)
	}
}

func TestMimirClient_AuthBearer(t *testing.T) {
	var receivedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(mimirCardinalityResponse{SeriesCountTotal: 100})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendMimir,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    100,
		Auth: AuthConfig{
			Type:  AuthBearer,
			Token: "mimir-token-123",
		},
	}

	client := NewMimirClient(cfg)
	_, err := client.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedAuth != "Bearer mimir-token-123" {
		t.Errorf("expected 'Bearer mimir-token-123', got %q", receivedAuth)
	}
}

func TestMimirClient_LimitParam(t *testing.T) {
	var receivedLimit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedLimit = r.URL.Query().Get("limit")
		json.NewEncoder(w).Encode(mimirCardinalityResponse{SeriesCountTotal: 100})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendMimir,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    50,
		Auth:    AuthConfig{Type: AuthNone},
	}

	client := NewMimirClient(cfg)
	_, err := client.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedLimit != "50" {
		t.Errorf("expected limit=50, got %q", receivedLimit)
	}
}
