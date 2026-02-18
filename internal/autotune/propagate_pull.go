package autotune

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// PullPropagator provides pull-based config propagation.
// The leader exposes GET /autotune/config. Followers periodically poll it.
type PullPropagator struct {
	cfg     PropagationConfig
	handler func([]ConfigChange)
	client  *http.Client

	mu          sync.RWMutex
	lastChanges []ConfigChange
	lastETag    string

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewPullPropagator creates a pull-based propagator.
func NewPullPropagator(cfg PropagationConfig) *PullPropagator {
	return &PullPropagator{
		cfg:    cfg,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// Propagate stores changes locally for the GET handler to serve.
// In pull mode, the leader doesn't push — followers pull.
func (pp *PullPropagator) Propagate(_ context.Context, changes []ConfigChange) error {
	data, err := json.Marshal(changes)
	if err != nil {
		return err
	}
	etag := fmt.Sprintf(`"%x"`, sha256.Sum256(data))

	pp.mu.Lock()
	pp.lastChanges = changes
	pp.lastETag = etag
	pp.mu.Unlock()
	return nil
}

// ReceiveUpdates registers a handler for incoming config changes.
func (pp *PullPropagator) ReceiveUpdates(handler func([]ConfigChange)) {
	pp.handler = handler
}

// HandleGetConfig returns an HTTP handler for GET /autotune/config.
// Supports ETag-based caching: returns 304 Not Modified if unchanged.
func (pp *PullPropagator) HandleGetConfig() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		pp.mu.RLock()
		changes := pp.lastChanges
		etag := pp.lastETag
		pp.mu.RUnlock()

		// Check If-None-Match for caching.
		if r.Header.Get("If-None-Match") == etag && etag != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if etag != "" {
			w.Header().Set("ETag", etag)
		}

		if changes == nil {
			_, _ = w.Write([]byte("[]"))
			return
		}

		_ = json.NewEncoder(w).Encode(changes)
	}
}

// Start begins the follower polling loop. Only runs on followers.
func (pp *PullPropagator) Start(ctx context.Context) error {
	if pp.cfg.PeerService == "" {
		return nil // leader-only mode, no polling needed
	}

	ctx, pp.cancel = context.WithCancel(ctx)
	pp.wg.Add(1)
	go pp.pollLoop(ctx)
	return nil
}

// Stop stops the polling loop.
func (pp *PullPropagator) Stop() {
	if pp.cancel != nil {
		pp.cancel()
	}
	pp.wg.Wait()
}

// pollLoop periodically fetches config from the leader.
func (pp *PullPropagator) pollLoop(ctx context.Context) {
	defer pp.wg.Done()

	interval := pp.cfg.PullInterval
	if interval == 0 {
		interval = 10 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastETag string

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			newETag, err := pp.fetchFromLeader(ctx, lastETag)
			if err == nil && newETag != "" {
				lastETag = newETag
			}
		}
	}
}

// fetchFromLeader fetches config from the leader's /autotune/config endpoint.
func (pp *PullPropagator) fetchFromLeader(ctx context.Context, etag string) (string, error) {
	url := fmt.Sprintf("http://%s/autotune/config", pp.cfg.PeerService)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := pp.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return etag, nil // no changes
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("leader returned status %d", resp.StatusCode)
	}

	var changes []ConfigChange
	if err := json.NewDecoder(resp.Body).Decode(&changes); err != nil {
		return "", fmt.Errorf("decode changes: %w", err)
	}

	if pp.handler != nil {
		pp.handler(changes)
	}

	return resp.Header.Get("ETag"), nil
}
