package autotune

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestThanosClient_FetchCardinality(t *testing.T) {
	resp := tsdbStatusResponse{
		Status: "success",
		Data: tsdbStatusData{
			HeadStats: tsdbHeadStats{NumSeries: 30000},
			SeriesCountByMetricName: []nameValuePair{
				{Name: "up", Value: 500},
				{Name: "scrape_duration_seconds", Value: 300},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/status/tsdb" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendThanos,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    100,
		Auth:    AuthConfig{Type: AuthNone},
		Thanos:  ThanosSourceConfig{Dedup: true},
	}

	client := NewThanosClient(cfg)
	data, err := client.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if data.TotalSeries != 30000 {
		t.Errorf("expected 30000 total series, got %d", data.TotalSeries)
	}
	if len(data.TopMetrics) != 2 {
		t.Fatalf("expected 2 top metrics, got %d", len(data.TopMetrics))
	}
	if data.Backend != BackendThanos {
		t.Errorf("expected backend Thanos, got %s", data.Backend)
	}
}

func TestThanosClient_TenantHeader(t *testing.T) {
	var receivedTenant string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedTenant = r.Header.Get("THANOS-TENANT")
		json.NewEncoder(w).Encode(tsdbStatusResponse{Status: "success"})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend:  BackendThanos,
		URL:      srv.URL,
		Timeout:  5 * time.Second,
		TopN:     100,
		TenantID: "tenant-42",
		Auth:     AuthConfig{Type: AuthNone},
	}

	client := NewThanosClient(cfg)
	_, err := client.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedTenant != "tenant-42" {
		t.Errorf("expected THANOS-TENANT=tenant-42, got %q", receivedTenant)
	}
}

func TestThanosClient_AllTenantsParam(t *testing.T) {
	var receivedAllTenants string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAllTenants = r.URL.Query().Get("all_tenants")
		json.NewEncoder(w).Encode(tsdbStatusResponse{Status: "success"})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendThanos,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    100,
		Auth:    AuthConfig{Type: AuthNone},
		Thanos:  ThanosSourceConfig{AllTenants: true},
	}

	client := NewThanosClient(cfg)
	_, err := client.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedAllTenants != "true" {
		t.Errorf("expected all_tenants=true, got %q", receivedAllTenants)
	}
}

func TestThanosClient_DedupParam(t *testing.T) {
	var receivedDedup string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedDedup = r.URL.Query().Get("dedup")
		json.NewEncoder(w).Encode(tsdbStatusResponse{Status: "success"})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendThanos,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    100,
		Auth:    AuthConfig{Type: AuthNone},
		Thanos:  ThanosSourceConfig{Dedup: true},
	}

	client := NewThanosClient(cfg)
	_, err := client.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedDedup != "true" {
		t.Errorf("expected dedup=true, got %q", receivedDedup)
	}
}

func TestThanosClient_ErrorStatusCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendThanos,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    100,
		Auth:    AuthConfig{Type: AuthNone},
	}

	client := NewThanosClient(cfg)
	_, err := client.FetchCardinality(context.Background())
	if err == nil {
		t.Fatal("expected error on 502 status")
	}
}

func TestThanosClient_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(tsdbStatusResponse{Status: "error", Error: "thanos error"})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendThanos,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    100,
		Auth:    AuthConfig{Type: AuthNone},
	}

	client := NewThanosClient(cfg)
	_, err := client.FetchCardinality(context.Background())
	if err == nil {
		t.Fatal("expected error on error status")
	}
}

func TestThanosClient_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		json.NewEncoder(w).Encode(tsdbStatusResponse{Status: "success"})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendThanos,
		URL:     srv.URL,
		Timeout: 50 * time.Millisecond,
		TopN:    100,
		Auth:    AuthConfig{Type: AuthNone},
	}

	client := NewThanosClient(cfg)
	_, err := client.FetchCardinality(context.Background())
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
