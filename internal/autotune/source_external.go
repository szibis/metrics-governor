package autotune

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// ExternalSignals contains data from the External Signal Provider.
type ExternalSignals struct {
	Recommendations []ConfigChange         `json:"recommendations,omitempty"`
	AnomalyScores   map[string]float64     `json:"anomaly_scores,omitempty"`
	CostData        map[string]float64     `json:"cost_data,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
	Timestamp       time.Time              `json:"timestamp"`
	TTL             time.Duration          `json:"ttl"`
}

// ExternalSignalSource fetches signals from a decoupled signal provider.
type ExternalSignalSource interface {
	FetchSignals(ctx context.Context) (*ExternalSignals, error)
}

// ExternalClient polls an External Signal Provider HTTP API.
// The signal provider runs expensive queries (cloud APIs, AI) out-of-band
// and caches results for the governor to poll.
type ExternalClient struct {
	url    string
	client *http.Client
	auth   AuthConfig

	mu     sync.RWMutex
	cached *ExternalSignals
}

// NewExternalClient creates a client that polls the signal provider.
func NewExternalClient(url string, timeout time.Duration, auth AuthConfig) *ExternalClient {
	return &ExternalClient{
		url:    url,
		client: &http.Client{Timeout: timeout},
		auth:   auth,
	}
}

// FetchSignals polls the signal provider for the latest signals.
func (ec *ExternalClient) FetchSignals(ctx context.Context) (*ExternalSignals, error) {
	// Check TTL cache first.
	ec.mu.RLock()
	if ec.cached != nil && time.Since(ec.cached.Timestamp) < ec.cached.TTL {
		cached := ec.cached
		ec.mu.RUnlock()
		return cached, nil
	}
	ec.mu.RUnlock()

	reqURL := ec.url + "/api/v1/signals"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	applyAuth(req, ec.auth)

	resp, err := ec.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("signal provider returned status %d", resp.StatusCode)
	}

	var signals ExternalSignals
	if err := json.NewDecoder(resp.Body).Decode(&signals); err != nil {
		return nil, fmt.Errorf("decode signals: %w", err)
	}

	// Cache the result.
	ec.mu.Lock()
	ec.cached = &signals
	ec.mu.Unlock()

	return &signals, nil
}

// applyAuth is defined in source_vm.go.
