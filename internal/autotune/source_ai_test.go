package autotune

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLLMClient_GenerateRecommendations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected bearer auth, got %q", r.Header.Get("Authorization"))
		}

		var req llmRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "gpt-4" {
			t.Errorf("expected model gpt-4, got %s", req.Model)
		}

		decisions := `{"decisions": [{"rule_name": "api_limits", "action": "increase", "new_value": 2000, "reason": "high utilization at 92%"}]}`
		resp := llmResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{
				{Message: struct {
					Content string `json:"content"`
				}{Content: decisions}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewLLMClient(server.URL, "gpt-4", "test-key")
	signals := AggregatedSignals{
		Utilization: map[string]float64{"api_limits": 0.92},
		Timestamp:   time.Now(),
	}

	got, err := client.GenerateRecommendations(context.Background(), signals)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 decision, got %d", len(got))
	}
	if got[0].RuleName != "api_limits" || got[0].Action != ActionIncrease || got[0].NewValue != 2000 {
		t.Errorf("unexpected decision: %+v", got[0])
	}
	if got[0].Reason != "AI: high utilization at 92%" {
		t.Errorf("unexpected reason: %s", got[0].Reason)
	}
}

func TestLLMClient_GenerateRecommendations_HoldFiltered(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decisions := `{"decisions": [{"rule_name": "rule_a", "action": "hold", "new_value": 0, "reason": "stable"}, {"rule_name": "rule_b", "action": "increase", "new_value": 1500, "reason": "growing"}]}`
		resp := llmResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{
				{Message: struct {
					Content string `json:"content"`
				}{Content: decisions}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewLLMClient(server.URL, "test", "")
	got, err := client.GenerateRecommendations(context.Background(), AggregatedSignals{})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	// "hold" should be filtered out.
	if len(got) != 1 || got[0].RuleName != "rule_b" {
		t.Errorf("expected only rule_b, got %+v", got)
	}
}

func TestLLMClient_GenerateRecommendations_EmptyChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := llmResponse{Choices: nil}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewLLMClient(server.URL, "test", "")
	got, err := client.GenerateRecommendations(context.Background(), AggregatedSignals{})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil decisions, got %+v", got)
	}
}

func TestLLMClient_GenerateRecommendations_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewLLMClient(server.URL, "test", "")
	_, err := client.GenerateRecommendations(context.Background(), AggregatedSignals{})
	if err == nil {
		t.Error("expected error on 500")
	}
}

func TestLLMClient_GenerateRecommendations_MalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := llmResponse{
			Choices: []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}{
				{Message: struct {
					Content string `json:"content"`
				}{Content: "not json at all"}},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewLLMClient(server.URL, "test", "")
	_, err := client.GenerateRecommendations(context.Background(), AggregatedSignals{})
	if err == nil {
		t.Error("expected error on malformed LLM output")
	}
}

func TestLLMClient_NoAPIKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("expected no auth header when key is empty")
		}
		resp := llmResponse{Choices: nil}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewLLMClient(server.URL, "test", "")
	_, err := client.GenerateRecommendations(context.Background(), AggregatedSignals{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
