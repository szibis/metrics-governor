package autotune

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"
)

// PromQLClient fetches cardinality signals by executing configurable PromQL
// queries against any Prometheus-compatible /api/v1/query endpoint.
type PromQLClient struct {
	cfg    SourceConfig
	client *http.Client
}

// NewPromQLClient creates a PromQL-based signal source.
func NewPromQLClient(cfg SourceConfig) *PromQLClient {
	return &PromQLClient{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

// FetchCardinality executes configured PromQL queries and returns results
// as CardinalityData. The "total_series" query result maps to TotalSeries;
// other query results are stored as TopMetrics with query name as metric name.
func (p *PromQLClient) FetchCardinality(ctx context.Context) (*CardinalityData, error) {
	data := &CardinalityData{
		FetchedAt: time.Now(),
		Backend:   BackendPromQL,
	}

	for name, query := range p.cfg.PromQL.Queries {
		val, err := p.executeQuery(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("promql query %q: %w", name, err)
		}
		if name == "total_series" {
			data.TotalSeries = int64(val)
		} else {
			data.TopMetrics = append(data.TopMetrics, MetricCardinality{
				Name:        name,
				SeriesCount: int64(val),
			})
		}
	}

	return data, nil
}

// executeQuery runs a single PromQL instant query and returns the scalar result.
func (p *PromQLClient) executeQuery(ctx context.Context, query string) (float64, error) {
	params := url.Values{}
	params.Set("query", query)

	u := p.cfg.URL + "/api/v1/query?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}

	applyAuth(req, p.cfg.Auth)

	resp, err := p.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("promql query request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return 0, fmt.Errorf("promql query returned %d: %s", resp.StatusCode, string(body))
	}

	var promResp promQLResponse
	if err := json.NewDecoder(resp.Body).Decode(&promResp); err != nil {
		return 0, fmt.Errorf("decode promql response: %w", err)
	}

	if promResp.Status != "success" {
		return 0, fmt.Errorf("promql query error: %s", promResp.Error)
	}

	return extractScalar(promResp.Data), nil
}

// extractScalar extracts a single numeric value from a PromQL response.
// Handles both "scalar" and "vector" result types (takes first vector element).
func extractScalar(data promQLData) float64 {
	switch data.ResultType {
	case "scalar":
		if len(data.Result) == 0 {
			return 0
		}
		// Scalar result: [timestamp, "value"]
		return parsePromValue(data.Result[0])

	case "vector":
		if len(data.Result) == 0 {
			return 0
		}
		// Vector result: [{"metric":{...},"value":[timestamp,"value"]}]
		entry, ok := data.Result[0].(map[string]any)
		if !ok {
			return 0
		}
		valueArr, ok := entry["value"].([]any)
		if !ok || len(valueArr) < 2 {
			return 0
		}
		str, ok := valueArr[1].(string)
		if !ok {
			return 0
		}
		var f float64
		_, _ = fmt.Sscanf(str, "%f", &f)
		return f
	}
	return 0
}

// parsePromValue parses a Prometheus value tuple.
func parsePromValue(v any) float64 {
	arr, ok := v.([]any)
	if !ok || len(arr) < 2 {
		return 0
	}
	str, ok := arr[1].(string)
	if !ok {
		return 0
	}
	var f float64
	_, _ = fmt.Sscanf(str, "%f", &f)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	return f
}

// --- PromQL API Response Types ---

type promQLResponse struct {
	Status string     `json:"status"`
	Error  string     `json:"error,omitempty"`
	Data   promQLData `json:"data"`
}

type promQLData struct {
	ResultType string        `json:"resultType"`
	Result     []any `json:"result"`
}
