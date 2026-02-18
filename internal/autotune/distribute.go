package autotune

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// ConfigDistributor distributes config changes to remote clusters (central mode).
type ConfigDistributor interface {
	Distribute(ctx context.Context, clusterConfigs map[string][]ConfigChange) error
}

// APIDistributor exposes a signal/recommendation API for island clusters to poll.
// Central hub publishes recommendations; islands GET /api/v1/recommendations.
type APIDistributor struct {
	mu              sync.RWMutex
	recommendations map[string][]ConfigChange // clusterName → changes
	updatedAt       time.Time
}

// NewAPIDistributor creates a new API-based distributor.
func NewAPIDistributor() *APIDistributor {
	return &APIDistributor{
		recommendations: make(map[string][]ConfigChange),
	}
}

// Distribute stores recommendations for island clusters to poll.
func (d *APIDistributor) Distribute(_ context.Context, clusterConfigs map[string][]ConfigChange) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.recommendations = clusterConfigs
	d.updatedAt = time.Now()
	return nil
}

// HandleRecommendations returns an HTTP handler that serves recommendations.
// Islands poll GET /api/v1/recommendations?cluster=<name>.
func (d *APIDistributor) HandleRecommendations() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		cluster := r.URL.Query().Get("cluster")

		d.mu.RLock()
		defer d.mu.RUnlock()

		w.Header().Set("Content-Type", "application/json")

		if cluster != "" {
			changes, ok := d.recommendations[cluster]
			if !ok {
				changes = []ConfigChange{}
			}
			_ = json.NewEncoder(w).Encode(changes)
			return
		}

		// No cluster filter: return all.
		_ = json.NewEncoder(w).Encode(d.recommendations)
	}
}

// UpdatedAt returns when recommendations were last updated.
func (d *APIDistributor) UpdatedAt() time.Time {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.updatedAt
}

// RecommendationClient polls a central hub's recommendation API.
// Used by island governors to incorporate central signals.
type RecommendationClient struct {
	url     string
	cluster string
	client  *http.Client
}

// NewRecommendationClient creates a client that polls central recommendations.
func NewRecommendationClient(url, cluster string) *RecommendationClient {
	return &RecommendationClient{
		url:     url,
		cluster: cluster,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

// FetchRecommendations polls the central hub for this cluster's recommendations.
func (rc *RecommendationClient) FetchRecommendations(ctx context.Context) ([]ConfigChange, error) {
	reqURL := fmt.Sprintf("%s/api/v1/recommendations?cluster=%s", rc.url, rc.cluster)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := rc.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var changes []ConfigChange
	if err := json.NewDecoder(resp.Body).Decode(&changes); err != nil {
		return nil, fmt.Errorf("decode recommendations: %w", err)
	}
	return changes, nil
}
