package autotune

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPromQLClient_ScalarResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		if query != "prometheus_tsdb_head_series" {
			t.Errorf("unexpected query: %s", query)
		}
		json.NewEncoder(w).Encode(promQLResponse{
			Status: "success",
			Data: promQLData{
				ResultType: "scalar",
				Result: []any{
					[]any{1700000000.0, "42000"},
				},
			},
		})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendPromQL,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    100,
		Auth:    AuthConfig{Type: AuthNone},
		PromQL: PromQLSourceConfig{
			Queries: map[string]string{
				"total_series": "prometheus_tsdb_head_series",
			},
		},
	}

	client := NewPromQLClient(cfg)
	data, err := client.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if data.TotalSeries != 42000 {
		t.Errorf("expected 42000 total series, got %d", data.TotalSeries)
	}
	if data.Backend != BackendPromQL {
		t.Errorf("expected backend PromQL, got %s", data.Backend)
	}
}

func TestPromQLClient_VectorResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(promQLResponse{
			Status: "success",
			Data: promQLData{
				ResultType: "vector",
				Result: []any{
					map[string]any{
						"metric": map[string]any{},
						"value":  []any{1700000000.0, "12345"},
					},
				},
			},
		})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendPromQL,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    100,
		Auth:    AuthConfig{Type: AuthNone},
		PromQL: PromQLSourceConfig{
			Queries: map[string]string{
				"total_series": "prometheus_tsdb_head_series",
			},
		},
	}

	client := NewPromQLClient(cfg)
	data, err := client.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if data.TotalSeries != 12345 {
		t.Errorf("expected 12345 total series, got %d", data.TotalSeries)
	}
}

func TestPromQLClient_MultipleQueries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		var val string
		switch query {
		case "prometheus_tsdb_head_series":
			val = "50000"
		case "rate(scrape_series_added[5m])":
			val = "100"
		default:
			t.Errorf("unexpected query: %s", query)
			val = "0"
		}
		json.NewEncoder(w).Encode(promQLResponse{
			Status: "success",
			Data: promQLData{
				ResultType: "scalar",
				Result:     []any{[]any{1700000000.0, val}},
			},
		})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendPromQL,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    100,
		Auth:    AuthConfig{Type: AuthNone},
		PromQL: PromQLSourceConfig{
			Queries: map[string]string{
				"total_series": "prometheus_tsdb_head_series",
				"growth_rate":  "rate(scrape_series_added[5m])",
			},
		},
	}

	client := NewPromQLClient(cfg)
	data, err := client.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if data.TotalSeries != 50000 {
		t.Errorf("expected 50000 total series, got %d", data.TotalSeries)
	}
	if len(data.TopMetrics) != 1 {
		t.Fatalf("expected 1 non-total metric, got %d", len(data.TopMetrics))
	}
	if data.TopMetrics[0].Name != "growth_rate" {
		t.Errorf("expected growth_rate metric, got %s", data.TopMetrics[0].Name)
	}
	if data.TopMetrics[0].SeriesCount != 100 {
		t.Errorf("expected 100 series count, got %d", data.TopMetrics[0].SeriesCount)
	}
}

func TestPromQLClient_ErrorStatusCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendPromQL,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    100,
		Auth:    AuthConfig{Type: AuthNone},
		PromQL: PromQLSourceConfig{
			Queries: map[string]string{"total_series": "up"},
		},
	}

	client := NewPromQLClient(cfg)
	_, err := client.FetchCardinality(context.Background())
	if err == nil {
		t.Fatal("expected error on 503 status")
	}
}

func TestPromQLClient_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(promQLResponse{Status: "error", Error: "bad query"})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendPromQL,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    100,
		Auth:    AuthConfig{Type: AuthNone},
		PromQL: PromQLSourceConfig{
			Queries: map[string]string{"total_series": "invalid("},
		},
	}

	client := NewPromQLClient(cfg)
	_, err := client.FetchCardinality(context.Background())
	if err == nil {
		t.Fatal("expected error on error status")
	}
}

func TestPromQLClient_EmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(promQLResponse{
			Status: "success",
			Data: promQLData{
				ResultType: "vector",
				Result:     []any{},
			},
		})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendPromQL,
		URL:     srv.URL,
		Timeout: 5 * time.Second,
		TopN:    100,
		Auth:    AuthConfig{Type: AuthNone},
		PromQL: PromQLSourceConfig{
			Queries: map[string]string{"total_series": "nonexistent_metric"},
		},
	}

	client := NewPromQLClient(cfg)
	data, err := client.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if data.TotalSeries != 0 {
		t.Errorf("expected 0 total series for empty result, got %d", data.TotalSeries)
	}
}

func TestPromQLClient_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		json.NewEncoder(w).Encode(promQLResponse{Status: "success"})
	}))
	defer srv.Close()

	cfg := SourceConfig{
		Backend: BackendPromQL,
		URL:     srv.URL,
		Timeout: 50 * time.Millisecond,
		TopN:    100,
		Auth:    AuthConfig{Type: AuthNone},
		PromQL: PromQLSourceConfig{
			Queries: map[string]string{"total_series": "up"},
		},
	}

	client := NewPromQLClient(cfg)
	_, err := client.FetchCardinality(context.Background())
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
