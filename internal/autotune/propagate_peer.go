package autotune

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// PeerPropagator pushes config changes to sibling pods via HTTP.
// It discovers peers by resolving the headless K8s service DNS name.
type PeerPropagator struct {
	cfg     PropagationConfig
	selfIP  string
	client  *http.Client
	handler func([]ConfigChange)

	mu    sync.RWMutex
	peers []string // resolved peer IPs

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewPeerPropagator creates a peer-push propagator.
func NewPeerPropagator(cfg PropagationConfig) *PeerPropagator {
	selfIP := os.Getenv("POD_IP")
	if selfIP == "" {
		// Fallback: detect via interfaces.
		if addrs, err := net.InterfaceAddrs(); err == nil {
			for _, a := range addrs {
				if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
					selfIP = ipnet.IP.String()
					break
				}
			}
		}
	}

	return &PeerPropagator{
		cfg:    cfg,
		selfIP: selfIP,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// Propagate pushes changes to all discovered peers.
func (pp *PeerPropagator) Propagate(ctx context.Context, changes []ConfigChange) error {
	pp.mu.RLock()
	peers := make([]string, len(pp.peers))
	copy(peers, pp.peers)
	pp.mu.RUnlock()

	data, err := json.Marshal(changes)
	if err != nil {
		return fmt.Errorf("marshal changes: %w", err)
	}

	var (
		mu      sync.Mutex
		errs    []error
		wg      sync.WaitGroup
		success int
	)

	for _, peer := range peers {
		peerHost, _, _ := net.SplitHostPort(peer)
		if peerHost == pp.selfIP {
			continue // skip self
		}
		wg.Add(1)
		go func(addr string) {
			defer wg.Done()
			if err := pp.pushToPeer(ctx, addr, data); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("peer %s: %w", addr, err))
				mu.Unlock()
			} else {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}(peer)
	}
	wg.Wait()

	// Partial success is acceptable — some peers may be down.
	if len(errs) > 0 && success == 0 {
		return fmt.Errorf("all peer pushes failed: %v", errs)
	}
	return nil
}

// pushToPeer sends changes to a single peer.
func (pp *PeerPropagator) pushToPeer(ctx context.Context, addr string, data []byte) error {
	url := fmt.Sprintf("http://%s/autotune/config", addr)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := pp.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// ReceiveUpdates registers a handler for incoming peer pushes.
func (pp *PeerPropagator) ReceiveUpdates(handler func([]ConfigChange)) {
	pp.handler = handler
}

// HandlePeerPush returns an HTTP handler for receiving peer pushes.
// Register this at POST /autotune/config on the stats/metrics server.
func (pp *PeerPropagator) HandlePeerPush() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var changes []ConfigChange
		if err := json.NewDecoder(r.Body).Decode(&changes); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}

		if pp.handler != nil {
			pp.handler(changes)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// Start begins periodic DNS discovery of peers.
func (pp *PeerPropagator) Start(ctx context.Context) error {
	if pp.cfg.PeerService == "" {
		return fmt.Errorf("peer_service not configured")
	}

	ctx, pp.cancel = context.WithCancel(ctx)
	pp.wg.Add(1)
	go pp.discoverLoop(ctx)
	return nil
}

// Stop stops the DNS discovery loop.
func (pp *PeerPropagator) Stop() {
	if pp.cancel != nil {
		pp.cancel()
	}
	pp.wg.Wait()
}

// discoverLoop periodically resolves the headless service DNS.
func (pp *PeerPropagator) discoverLoop(ctx context.Context) {
	defer pp.wg.Done()

	// Resolve immediately on start.
	pp.refreshPeers(ctx)

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pp.refreshPeers(ctx)
		}
	}
}

// refreshPeers resolves the headless service DNS A records.
func (pp *PeerPropagator) refreshPeers(ctx context.Context) {
	host, port, err := net.SplitHostPort(pp.cfg.PeerService)
	if err != nil {
		host = pp.cfg.PeerService
		port = "9091" // default autotune port
	}

	resolver := &net.Resolver{}
	resolveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	addrs, err := resolver.LookupHost(resolveCtx, host)
	if err != nil {
		return // keep existing peers on DNS failure
	}

	// Build addr:port list.
	peers := make([]string, len(addrs))
	for i, addr := range addrs {
		peers[i] = net.JoinHostPort(addr, port)
	}

	pp.mu.Lock()
	pp.peers = peers
	pp.mu.Unlock()
}
