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

// VMClient fetches cardinality data from VictoriaMetrics (or Prometheus)
// via the /api/v1/status/tsdb endpoint. Supports dual-mode operation:
// TSDB insights (fast, lightweight) and cardinality explorer (deep drill-down).
type VMClient struct {
	cfg    SourceConfig
	client *http.Client
}

// NewVMClient creates a VictoriaMetrics cardinality source.
func NewVMClient(cfg SourceConfig) *VMClient {
	return &VMClient{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

// FetchCardinality fetches cardinality data using TSDB insights mode.
func (v *VMClient) FetchCardinality(ctx context.Context) (*CardinalityData, error) {
	return v.fetchTSDBInsights(ctx)
}

// FetchExplorer fetches deep cardinality data using the cardinality explorer mode.
// This is called separately at a longer interval.
func (v *VMClient) FetchExplorer(ctx context.Context) (*CardinalityData, error) {
	return v.fetchExplorer(ctx)
}

// fetchTSDBInsights queries /api/v1/status/tsdb for top-level cardinality overview.
func (v *VMClient) fetchTSDBInsights(ctx context.Context) (*CardinalityData, error) {
	params := url.Values{}
	params.Set("topN", strconv.Itoa(v.cfg.TopN))

	// Add tenant ID for VM cluster mode.
	if v.cfg.TenantID != "" {
		params.Set("extra_label", "tenant_id:"+v.cfg.TenantID)
	}

	u := v.cfg.URL + "/api/v1/status/tsdb?" + params.Encode()
	return v.doRequest(ctx, u)
}

// fetchExplorer queries /api/v1/status/tsdb with match[]/focusLabel/date for deep drill-down.
func (v *VMClient) fetchExplorer(ctx context.Context) (*CardinalityData, error) {
	params := url.Values{}
	params.Set("topN", strconv.Itoa(v.cfg.TopN))

	if v.cfg.TenantID != "" {
		params.Set("extra_label", "tenant_id:"+v.cfg.TenantID)
	}

	for _, m := range v.cfg.VM.ExplorerMatch {
		params.Add("match[]", m)
	}
	if v.cfg.VM.ExplorerFocusLabel != "" {
		params.Set("focusLabel", v.cfg.VM.ExplorerFocusLabel)
	}
	if v.cfg.VM.ExplorerDate != "" {
		params.Set("date", v.cfg.VM.ExplorerDate)
	}

	u := v.cfg.URL + "/api/v1/status/tsdb?" + params.Encode()
	return v.doRequest(ctx, u)
}

// doRequest executes an HTTP GET and parses the TSDB status response.
func (v *VMClient) doRequest(ctx context.Context, rawURL string) (*CardinalityData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	applyAuth(req, v.cfg.Auth)

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tsdb status request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("tsdb status returned %d: %s", resp.StatusCode, string(body))
	}

	var tsdbResp tsdbStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&tsdbResp); err != nil {
		return nil, fmt.Errorf("decode tsdb status: %w", err)
	}

	if tsdbResp.Status != "success" {
		return nil, fmt.Errorf("tsdb status error: %s", tsdbResp.Error)
	}

	return v.toCardinalityData(&tsdbResp), nil
}

// toCardinalityData converts a TSDB status response to the common format.
func (v *VMClient) toCardinalityData(resp *tsdbStatusResponse) *CardinalityData {
	data := &CardinalityData{
		TotalSeries: resp.Data.HeadStats.NumSeries,
		FetchedAt:   time.Now(),
		Backend:     v.cfg.Backend,
	}

	for _, m := range resp.Data.SeriesCountByMetricName {
		data.TopMetrics = append(data.TopMetrics, MetricCardinality{
			Name:        m.Name,
			SeriesCount: m.Value,
		})
	}

	// Parse focusLabel results (VM cardinality explorer mode).
	if len(resp.Data.SeriesCountByFocusLabelValue) > 0 {
		if data.TopLabelValues == nil {
			data.TopLabelValues = make(map[string][]LabelValueCardinality)
		}
		label := v.cfg.VM.ExplorerFocusLabel
		if label == "" {
			label = "_focus"
		}
		for _, lv := range resp.Data.SeriesCountByFocusLabelValue {
			data.TopLabelValues[label] = append(data.TopLabelValues[label], LabelValueCardinality{
				Value:       lv.Name,
				SeriesCount: lv.Value,
			})
		}
	}

	return data
}

// --- TSDB Status API Response Types ---

type tsdbStatusResponse struct {
	Status string         `json:"status"`
	Error  string         `json:"error,omitempty"`
	Data   tsdbStatusData `json:"data"`
}

type tsdbStatusData struct {
	HeadStats                      tsdbHeadStats    `json:"headStats"`
	SeriesCountByMetricName        []nameValuePair  `json:"seriesCountByMetricName"`
	LabelValueCountByLabelName     []nameValuePair  `json:"labelValueCountByLabelName"`
	MemoryInBytesByLabelName       []nameValuePair  `json:"memoryInBytesByLabelName"`
	SeriesCountByLabelValuePair    []nameValuePair  `json:"seriesCountByLabelValuePair"`
	SeriesCountByFocusLabelValue   []nameValuePair  `json:"seriesCountByFocusLabelValue"`
}

type tsdbHeadStats struct {
	NumSeries    int64 `json:"numSeries"`
	NumLabelPairs int64 `json:"numLabelPairs"`
	ChunkCount   int64 `json:"chunkCount"`
	MinTime      int64 `json:"minTime"`
	MaxTime      int64 `json:"maxTime"`
}

type nameValuePair struct {
	Name  string `json:"name"`
	Value int64  `json:"value"`
}

// --- Auth Helper ---

// applyAuth adds authentication headers to an HTTP request.
func applyAuth(req *http.Request, auth AuthConfig) {
	switch auth.Type {
	case AuthBasic:
		req.SetBasicAuth(auth.Username, auth.Password)
	case AuthBearer:
		req.Header.Set("Authorization", "Bearer "+auth.Token)
	case AuthHeader:
		if auth.HeaderName != "" {
			req.Header.Set(auth.HeaderName, auth.HeaderValue)
		}
	}
}
