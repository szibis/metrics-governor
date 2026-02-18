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

// ThanosClient fetches cardinality data from Thanos (Receive or Query)
// via /api/v1/status/tsdb. Supports per-tenant via THANOS-TENANT header
// and all_tenants query parameter.
type ThanosClient struct {
	cfg    SourceConfig
	client *http.Client
}

// NewThanosClient creates a Thanos cardinality source.
func NewThanosClient(cfg SourceConfig) *ThanosClient {
	return &ThanosClient{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

// FetchCardinality fetches cardinality data from Thanos /api/v1/status/tsdb.
func (tc *ThanosClient) FetchCardinality(ctx context.Context) (*CardinalityData, error) {
	params := url.Values{}
	params.Set("limit", strconv.Itoa(tc.cfg.TopN))

	if tc.cfg.Thanos.AllTenants {
		params.Set("all_tenants", "true")
	}
	if tc.cfg.Thanos.Dedup {
		params.Set("dedup", "true")
	}

	u := tc.cfg.URL + "/api/v1/status/tsdb?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	applyAuth(req, tc.cfg.Auth)

	// Thanos per-tenant via THANOS-TENANT header.
	if tc.cfg.TenantID != "" {
		req.Header.Set("THANOS-TENANT", tc.cfg.TenantID)
	}

	resp, err := tc.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("thanos tsdb status request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("thanos tsdb status returned %d: %s", resp.StatusCode, string(body))
	}

	// Thanos uses the same response format as VM/Prometheus.
	var tsdbResp tsdbStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&tsdbResp); err != nil {
		return nil, fmt.Errorf("decode thanos tsdb status: %w", err)
	}

	if tsdbResp.Status != "success" {
		return nil, fmt.Errorf("thanos tsdb status error: %s", tsdbResp.Error)
	}

	return tc.toCardinalityData(&tsdbResp), nil
}

func (tc *ThanosClient) toCardinalityData(resp *tsdbStatusResponse) *CardinalityData {
	data := &CardinalityData{
		TotalSeries: resp.Data.HeadStats.NumSeries,
		FetchedAt:   time.Now(),
		Backend:     BackendThanos,
	}

	for _, m := range resp.Data.SeriesCountByMetricName {
		data.TopMetrics = append(data.TopMetrics, MetricCardinality{
			Name:        m.Name,
			SeriesCount: m.Value,
		})
	}

	return data
}
