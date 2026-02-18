package autotune

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAPIDistributor_Distribute(t *testing.T) {
	d := NewAPIDistributor()

	changes := map[string][]ConfigChange{
		"cluster-a": {{Action: "increase", RuleName: "rule_1", NewValue: 2000}},
		"cluster-b": {{Action: "decrease", RuleName: "rule_2", NewValue: 500}},
	}

	if err := d.Distribute(context.Background(), changes); err != nil {
		t.Fatalf("distribute: %v", err)
	}

	if d.UpdatedAt().IsZero() {
		t.Error("expected updatedAt to be set")
	}
}

func TestAPIDistributor_HandleRecommendations_AllClusters(t *testing.T) {
	d := NewAPIDistributor()

	changes := map[string][]ConfigChange{
		"cluster-a": {{Action: "increase", RuleName: "rule_1"}},
		"cluster-b": {{Action: "decrease", RuleName: "rule_2"}},
	}
	d.Distribute(context.Background(), changes)

	handler := d.HandleRecommendations()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/recommendations", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var got map[string][]ConfigChange
	json.NewDecoder(w.Body).Decode(&got)
	if len(got) != 2 {
		t.Errorf("expected 2 clusters, got %d", len(got))
	}
}

func TestAPIDistributor_HandleRecommendations_SingleCluster(t *testing.T) {
	d := NewAPIDistributor()

	changes := map[string][]ConfigChange{
		"cluster-a": {{Action: "increase", RuleName: "rule_1", NewValue: 1500}},
		"cluster-b": {{Action: "decrease", RuleName: "rule_2"}},
	}
	d.Distribute(context.Background(), changes)

	handler := d.HandleRecommendations()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/recommendations?cluster=cluster-a", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var got []ConfigChange
	json.NewDecoder(w.Body).Decode(&got)
	if len(got) != 1 || got[0].RuleName != "rule_1" {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestAPIDistributor_HandleRecommendations_UnknownCluster(t *testing.T) {
	d := NewAPIDistributor()
	d.Distribute(context.Background(), map[string][]ConfigChange{
		"cluster-a": {{Action: "increase"}},
	})

	handler := d.HandleRecommendations()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/recommendations?cluster=unknown", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var got []ConfigChange
	json.NewDecoder(w.Body).Decode(&got)
	if len(got) != 0 {
		t.Errorf("expected empty, got %+v", got)
	}
}

func TestAPIDistributor_HandleRecommendations_WrongMethod(t *testing.T) {
	d := NewAPIDistributor()
	handler := d.HandleRecommendations()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/recommendations", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestRecommendationClient_FetchRecommendations(t *testing.T) {
	changes := []ConfigChange{
		{Action: "increase", RuleName: "rule_1", NewValue: 2000},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/recommendations" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("cluster") != "my-cluster" {
			t.Errorf("unexpected cluster: %s", r.URL.Query().Get("cluster"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(changes)
	}))
	defer server.Close()

	client := NewRecommendationClient(server.URL, "my-cluster")
	got, err := client.FetchRecommendations(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(got) != 1 || got[0].RuleName != "rule_1" || got[0].NewValue != 2000 {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestRecommendationClient_FetchRecommendations_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewRecommendationClient(server.URL, "test")
	_, err := client.FetchRecommendations(context.Background())
	if err == nil {
		t.Error("expected error on 500")
	}
}

func TestRecommendationClient_FetchRecommendations_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]ConfigChange{})
	}))
	defer server.Close()

	client := NewRecommendationClient(server.URL, "test")
	got, err := client.FetchRecommendations(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
}

func TestAPIDistributor_Integration(t *testing.T) {
	// Full round-trip: distribute → serve → client fetch.
	d := NewAPIDistributor()
	changes := map[string][]ConfigChange{
		"prod-us-east": {{Action: "increase", RuleName: "api_limits", NewValue: 5000, Reason: "high utilization"}},
	}
	d.Distribute(context.Background(), changes)

	server := httptest.NewServer(d.HandleRecommendations())
	defer server.Close()

	client := NewRecommendationClient(server.URL, "prod-us-east")
	got, err := client.FetchRecommendations(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(got) != 1 || got[0].NewValue != 5000 {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestAPIDistributor_UpdateOverwrites(t *testing.T) {
	d := NewAPIDistributor()

	d.Distribute(context.Background(), map[string][]ConfigChange{
		"a": {{Action: "increase", NewValue: 100}},
	})

	before := d.UpdatedAt()
	time.Sleep(time.Millisecond)

	d.Distribute(context.Background(), map[string][]ConfigChange{
		"a": {{Action: "decrease", NewValue: 50}},
	})

	if !d.UpdatedAt().After(before) {
		t.Error("expected updated timestamp to advance")
	}

	handler := d.HandleRecommendations()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/recommendations?cluster=a", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	var got []ConfigChange
	json.NewDecoder(w.Body).Decode(&got)
	if len(got) != 1 || got[0].Action != "decrease" {
		t.Errorf("expected overwritten value, got %+v", got)
	}
}
