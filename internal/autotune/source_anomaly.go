package autotune

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AnomalyClient fetches anomaly scores from a vmanomaly deployment
// by querying the anomaly_score metric via PromQL.
type AnomalyClient struct {
	url    string
	metric string
	client *http.Client
}

// NewAnomalyClient creates a vmanomaly anomaly score client.
func NewAnomalyClient(vmanomalyURL, metricName string, timeout time.Duration) *AnomalyClient {
	if metricName == "" {
		metricName = "anomaly_score"
	}
	return &AnomalyClient{
		url:    vmanomalyURL,
		metric: metricName,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// FetchAnomalyScores queries vmanomaly for anomaly scores of the given metrics.
// Returns a map of metric name -> anomaly score (0.0-1.0).
func (ac *AnomalyClient) FetchAnomalyScores(ctx context.Context, metrics []string) (map[string]float64, error) {
	if len(metrics) == 0 {
		return nil, nil
	}

	// Build a PromQL query that fetches anomaly scores for specific metrics.
	// vmanomaly writes anomaly_score{for="metric_name"} = <score>.
	nameFilter := strings.Join(metrics, "|")
	query := fmt.Sprintf(`%s{for=~"%s"}`, ac.metric, nameFilter)

	params := url.Values{}
	params.Set("query", query)

	u := ac.url + "/api/v1/query?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create anomaly request: %w", err)
	}

	resp, err := ac.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anomaly query request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("anomaly query returned %d: %s", resp.StatusCode, string(body))
	}

	var promResp promQLResponse
	if err := json.NewDecoder(resp.Body).Decode(&promResp); err != nil {
		return nil, fmt.Errorf("decode anomaly response: %w", err)
	}

	if promResp.Status != "success" {
		return nil, fmt.Errorf("anomaly query error: %s", promResp.Error)
	}

	return parseAnomalyScores(promResp.Data), nil
}

// parseAnomalyScores extracts metric name -> score from a PromQL vector response.
func parseAnomalyScores(data promQLData) map[string]float64 {
	scores := make(map[string]float64)

	for _, item := range data.Result {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}

		// Extract the "for" label value as the metric name.
		metricLabels, ok := entry["metric"].(map[string]any)
		if !ok {
			continue
		}
		metricName, ok := metricLabels["for"].(string)
		if !ok {
			continue
		}

		// Extract the value.
		valueArr, ok := entry["value"].([]any)
		if !ok || len(valueArr) < 2 {
			continue
		}
		str, ok := valueArr[1].(string)
		if !ok {
			continue
		}
		var score float64
		_, _ = fmt.Sscanf(str, "%f", &score)

		// Clamp to [0, 1].
		if score < 0 {
			score = 0
		}
		if score > 1 {
			score = 1
		}

		scores[metricName] = score
	}

	return scores
}
