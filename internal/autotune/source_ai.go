package autotune

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// LLMClient implements AIAdvisor by calling an LLM endpoint.
// This is an in-process adapter for dev/testing. In production,
// prefer the ExternalSignalSource (decoupled signal provider).
type LLMClient struct {
	endpoint string
	model    string
	apiKey   string
	client   *http.Client
}

// NewLLMClient creates a new in-process LLM advisor.
func NewLLMClient(endpoint, model, apiKey string) *LLMClient {
	return &LLMClient{
		endpoint: endpoint,
		model:    model,
		apiKey:   apiKey,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

// llmRequest is the request payload sent to the LLM endpoint.
type llmRequest struct {
	Model    string       `json:"model"`
	Messages []llmMessage `json:"messages"`
}

type llmMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// llmResponse is the response from the LLM endpoint.
type llmResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// llmDecisions is the structured response we parse from LLM output.
type llmDecisions struct {
	Decisions []struct {
		RuleName string `json:"rule_name"`
		Action   string `json:"action"`
		NewValue int64  `json:"new_value"`
		Reason   string `json:"reason"`
	} `json:"decisions"`
}

// GenerateRecommendations sends aggregated signals to the LLM and parses decisions.
func (lc *LLMClient) GenerateRecommendations(ctx context.Context, signals AggregatedSignals) ([]Decision, error) {
	// Build prompt with signal data.
	signalJSON, err := json.Marshal(signals)
	if err != nil {
		return nil, fmt.Errorf("marshal signals: %w", err)
	}

	prompt := fmt.Sprintf(`You are a metrics governance advisor. Analyze these signals and recommend limit adjustments.

Signals:
%s

Respond with a JSON object containing a "decisions" array. Each decision should have:
- rule_name: the limits rule name
- action: "increase", "decrease", "tighten", or "hold"
- new_value: the recommended new limit value (0 for "hold")
- reason: brief explanation

Only recommend changes when the data strongly supports them. Prefer conservative adjustments.`, string(signalJSON))

	reqBody := llmRequest{
		Model: lc.model,
		Messages: []llmMessage{
			{Role: "user", Content: prompt},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, lc.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if lc.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+lc.apiKey)
	}

	resp, err := lc.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LLM returned status %d", resp.StatusCode)
	}

	var llmResp llmResponse
	if err := json.NewDecoder(resp.Body).Decode(&llmResp); err != nil {
		return nil, fmt.Errorf("decode LLM response: %w", err)
	}

	if len(llmResp.Choices) == 0 {
		return nil, nil
	}

	// Parse the LLM's JSON output.
	var parsed llmDecisions
	if err := json.Unmarshal([]byte(llmResp.Choices[0].Message.Content), &parsed); err != nil {
		return nil, fmt.Errorf("parse LLM decisions: %w", err)
	}

	now := time.Now()
	var decisions []Decision
	for _, d := range parsed.Decisions {
		if d.Action == "hold" || d.Action == "" {
			continue
		}
		decisions = append(decisions, Decision{
			RuleName:  d.RuleName,
			Field:     "max_cardinality",
			NewValue:  d.NewValue,
			Action:    Action(d.Action),
			Reason:    "AI: " + d.Reason,
			Timestamp: now,
		})
	}

	return decisions, nil
}
