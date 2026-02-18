package autotune

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// MimirClient fetches cardinality data from Grafana Mimir via
// /api/v1/cardinality/label_values. Supports multi-tenant via X-Scope-OrgID
// and active vs inmemory count methods.
type MimirClient struct {
	cfg    SourceConfig
	client *http.Client
}

// NewMimirClient creates a Grafana Mimir cardinality source.
func NewMimirClient(cfg SourceConfig) *MimirClient {
	return &MimirClient{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

// FetchCardinality fetches cardinality data from Mimir's cardinality API.
func (m *MimirClient) FetchCardinality(ctx context.Context) (*CardinalityData, error) {
	params := url.Values{}
	params.Set("label_names[]", "__name__")
	params.Set("limit", strconv.Itoa(m.cfg.TopN))
	if m.cfg.Mimir.CountMethod != "" {
		params.Set("count_method", m.cfg.Mimir.CountMethod)
	}

	u := m.cfg.URL + "/api/v1/cardinality/label_values?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	applyAuth(req, m.cfg.Auth)

	// Mimir multi-tenant via X-Scope-OrgID header.
	if m.cfg.TenantID != "" {
		req.Header.Set("X-Scope-OrgID", m.cfg.TenantID)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mimir cardinality request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("mimir cardinality returned %d: %s", resp.StatusCode, string(body))
	}

	var mimirResp mimirCardinalityResponse
	if err := json.NewDecoder(resp.Body).Decode(&mimirResp); err != nil {
		return nil, fmt.Errorf("decode mimir cardinality: %w", err)
	}

	return m.toCardinalityData(&mimirResp), nil
}

func (m *MimirClient) toCardinalityData(resp *mimirCardinalityResponse) *CardinalityData {
	data := &CardinalityData{
		TotalSeries: resp.SeriesCountTotal,
		FetchedAt:   time.Now(),
		Backend:     BackendMimir,
	}

	for _, label := range resp.Labels {
		if label.LabelName != "__name__" {
			continue
		}
		for _, c := range label.Cardinality {
			data.TopMetrics = append(data.TopMetrics, MetricCardinality{
				Name:        c.LabelValue,
				SeriesCount: c.SeriesCount,
			})
		}
	}

	return data
}

// --- Mimir Cardinality API Response Types ---

type mimirCardinalityResponse struct {
	SeriesCountTotal int64                   `json:"series_count_total"`
	Labels           []mimirLabelCardinality `json:"labels"`
}

type mimirLabelCardinality struct {
	LabelName        string                  `json:"label_name"`
	LabelValuesCount int64                   `json:"label_values_count"`
	SeriesCount      int64                   `json:"series_count"`
	Cardinality      []mimirValueCardinality `json:"cardinality"`
}

type mimirValueCardinality struct {
	LabelValue  string `json:"label_value"`
	SeriesCount int64  `json:"series_count"`
}
