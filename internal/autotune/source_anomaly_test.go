package autotune

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAnomalyClient_FetchScores(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		query := r.URL.Query().Get("query")
		if query == "" {
			t.Error("empty query")
		}

		json.NewEncoder(w).Encode(promQLResponse{
			Status: "success",
			Data: promQLData{
				ResultType: "vector",
				Result: []any{
					map[string]any{
						"metric": map[string]any{"for": "rule_a"},
						"value":  []any{1700000000.0, "0.85"},
					},
					map[string]any{
						"metric": map[string]any{"for": "rule_b"},
						"value":  []any{1700000000.0, "0.15"},
					},
				},
			},
		})
	}))
	defer srv.Close()

	client := NewAnomalyClient(srv.URL, "anomaly_score", 5*time.Second)
	scores, err := client.FetchAnomalyScores(context.Background(), []string{"rule_a", "rule_b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if scores["rule_a"] != 0.85 {
		t.Errorf("expected rule_a=0.85, got %f", scores["rule_a"])
	}
	if scores["rule_b"] != 0.15 {
		t.Errorf("expected rule_b=0.15, got %f", scores["rule_b"])
	}
}

func TestAnomalyClient_EmptyMetrics(t *testing.T) {
	client := NewAnomalyClient("http://localhost:9999", "anomaly_score", 5*time.Second)
	scores, err := client.FetchAnomalyScores(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scores != nil {
		t.Errorf("expected nil scores for empty metrics, got %v", scores)
	}
}

func TestAnomalyClient_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(promQLResponse{Status: "error", Error: "bad query"})
	}))
	defer srv.Close()

	client := NewAnomalyClient(srv.URL, "anomaly_score", 5*time.Second)
	_, err := client.FetchAnomalyScores(context.Background(), []string{"rule_a"})
	if err == nil {
		t.Fatal("expected error on error status")
	}
}

func TestAnomalyClient_ErrorStatusCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := NewAnomalyClient(srv.URL, "anomaly_score", 5*time.Second)
	_, err := client.FetchAnomalyScores(context.Background(), []string{"rule_a"})
	if err == nil {
		t.Fatal("expected error on 503 status")
	}
}

func TestAnomalyClient_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
		json.NewEncoder(w).Encode(promQLResponse{Status: "success"})
	}))
	defer srv.Close()

	client := NewAnomalyClient(srv.URL, "anomaly_score", 50*time.Millisecond)
	_, err := client.FetchAnomalyScores(context.Background(), []string{"rule_a"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestAnomalyClient_ScoreClamp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(promQLResponse{
			Status: "success",
			Data: promQLData{
				ResultType: "vector",
				Result: []any{
					map[string]any{
						"metric": map[string]any{"for": "over"},
						"value":  []any{1700000000.0, "1.5"},
					},
					map[string]any{
						"metric": map[string]any{"for": "under"},
						"value":  []any{1700000000.0, "-0.3"},
					},
				},
			},
		})
	}))
	defer srv.Close()

	client := NewAnomalyClient(srv.URL, "anomaly_score", 5*time.Second)
	scores, err := client.FetchAnomalyScores(context.Background(), []string{"over", "under"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if scores["over"] != 1.0 {
		t.Errorf("expected clamped 1.0 for over, got %f", scores["over"])
	}
	if scores["under"] != 0.0 {
		t.Errorf("expected clamped 0.0 for under, got %f", scores["under"])
	}
}

func TestAnomalyClient_DefaultMetricName(t *testing.T) {
	client := NewAnomalyClient("http://localhost:9999", "", 5*time.Second)
	if client.metric != "anomaly_score" {
		t.Errorf("expected default metric name 'anomaly_score', got %q", client.metric)
	}
}

func TestAnomalyClient_CustomMetricName(t *testing.T) {
	var receivedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.Query().Get("query")
		json.NewEncoder(w).Encode(promQLResponse{
			Status: "success",
			Data:   promQLData{ResultType: "vector", Result: []any{}},
		})
	}))
	defer srv.Close()

	client := NewAnomalyClient(srv.URL, "custom_anomaly_metric", 5*time.Second)
	_, err := client.FetchAnomalyScores(context.Background(), []string{"rule_a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(receivedQuery, "custom_anomaly_metric") {
		t.Errorf("expected query to contain custom metric name, got %q", receivedQuery)
	}
}
