package autotune

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExternalClient_FetchSignals(t *testing.T) {
	signals := ExternalSignals{
		Recommendations: []ConfigChange{
			{Action: "increase", RuleName: "rule_1", NewValue: 2000},
		},
		AnomalyScores: map[string]float64{"metric_a": 0.8},
		Timestamp:     time.Now(),
		TTL:           5 * time.Minute,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/signals" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(signals)
	}))
	defer server.Close()

	client := NewExternalClient(server.URL, 10*time.Second, AuthConfig{})
	got, err := client.FetchSignals(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(got.Recommendations) != 1 {
		t.Errorf("expected 1 recommendation, got %d", len(got.Recommendations))
	}
	if got.AnomalyScores["metric_a"] != 0.8 {
		t.Errorf("expected anomaly score 0.8, got %v", got.AnomalyScores["metric_a"])
	}
}

func TestExternalClient_FetchSignals_TTLCache(t *testing.T) {
	calls := 0
	signals := ExternalSignals{
		Timestamp: time.Now(),
		TTL:       1 * time.Hour, // long TTL
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(signals)
	}))
	defer server.Close()

	client := NewExternalClient(server.URL, 10*time.Second, AuthConfig{})

	// First call: hits server.
	_, err := client.FetchSignals(context.Background())
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 call, got %d", calls)
	}

	// Second call: should use cache.
	_, err = client.FetchSignals(context.Background())
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected still 1 call (cached), got %d", calls)
	}
}

func TestExternalClient_FetchSignals_ExpiredCache(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		signals := ExternalSignals{
			Timestamp: time.Now(),
			TTL:       1 * time.Millisecond, // very short TTL
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(signals)
	}))
	defer server.Close()

	client := NewExternalClient(server.URL, 10*time.Second, AuthConfig{})

	_, err := client.FetchSignals(context.Background())
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}

	// Wait for TTL to expire.
	time.Sleep(5 * time.Millisecond)

	_, err = client.FetchSignals(context.Background())
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls after TTL expired, got %d", calls)
	}
}

func TestExternalClient_FetchSignals_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := NewExternalClient(server.URL, 10*time.Second, AuthConfig{})
	_, err := client.FetchSignals(context.Background())
	if err == nil {
		t.Error("expected error on 503")
	}
}

func TestExternalClient_FetchSignals_BearerAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			t.Errorf("expected Bearer auth, got %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ExternalSignals{Timestamp: time.Now(), TTL: time.Millisecond})
	}))
	defer server.Close()

	client := NewExternalClient(server.URL, 10*time.Second, AuthConfig{
		Type:  AuthBearer,
		Token: "test-token",
	})
	_, err := client.FetchSignals(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
}

func TestExternalClient_FetchSignals_BasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "admin" || pass != "secret" {
			t.Errorf("expected basic auth admin:secret, got %s:%s ok=%v", user, pass, ok)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ExternalSignals{Timestamp: time.Now(), TTL: time.Millisecond})
	}))
	defer server.Close()

	client := NewExternalClient(server.URL, 10*time.Second, AuthConfig{
		Type:     AuthBasic,
		Username: "admin",
		Password: "secret",
	})
	_, err := client.FetchSignals(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
}

func TestExternalClient_FetchSignals_CustomHeaderAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		val := r.Header.Get("X-Custom-Key")
		if val != "my-key" {
			t.Errorf("expected custom header, got %q", val)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(ExternalSignals{Timestamp: time.Now(), TTL: time.Millisecond})
	}))
	defer server.Close()

	client := NewExternalClient(server.URL, 10*time.Second, AuthConfig{
		Type:        AuthHeader,
		HeaderName:  "X-Custom-Key",
		HeaderValue: "my-key",
	})
	_, err := client.FetchSignals(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
}

func TestExternalClient_FetchSignals_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{invalid json"))
	}))
	defer server.Close()

	client := NewExternalClient(server.URL, 10*time.Second, AuthConfig{})
	_, err := client.FetchSignals(context.Background())
	if err == nil {
		t.Error("expected error on malformed JSON")
	}
}

func TestApplyAuth_None(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	applyAuth(req, AuthConfig{Type: AuthNone})
	if req.Header.Get("Authorization") != "" {
		t.Error("expected no auth header")
	}
}
