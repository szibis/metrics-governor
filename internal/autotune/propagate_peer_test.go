package autotune

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPeerPropagator_PushToMultiplePeers(t *testing.T) {
	var received atomic.Int32

	// Create two mock peer servers.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		var changes []ConfigChange
		if err := json.NewDecoder(r.Body).Decode(&changes); err != nil {
			t.Errorf("decode: %v", err)
		}
		received.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})

	peer1 := httptest.NewServer(handler)
	defer peer1.Close()
	peer2 := httptest.NewServer(handler)
	defer peer2.Close()

	// Extract host:port from server URLs.
	addr1 := peer1.Listener.Addr().String()
	addr2 := peer2.Listener.Addr().String()

	pp := &PeerPropagator{
		cfg:    PropagationConfig{},
		selfIP: "1.2.3.4", // won't match any peer
		client: &http.Client{Timeout: 5 * time.Second},
		peers:  []string{addr1, addr2},
	}

	changes := []ConfigChange{{Action: "increase", RuleName: "rule_a"}}
	if err := pp.Propagate(context.Background(), changes); err != nil {
		t.Fatalf("propagate: %v", err)
	}

	if got := received.Load(); got != 2 {
		t.Errorf("expected 2 peers to receive, got %d", got)
	}
}

func TestPeerPropagator_SkipsSelf(t *testing.T) {
	var received atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer peer.Close()

	selfAddr := peer.Listener.Addr().String()
	selfHost, selfPort, _ := net.SplitHostPort(selfAddr)

	pp := &PeerPropagator{
		cfg:    PropagationConfig{},
		selfIP: selfHost,
		client: &http.Client{Timeout: 5 * time.Second},
		peers:  []string{net.JoinHostPort(selfHost, selfPort)},
	}

	changes := []ConfigChange{{Action: "increase"}}
	if err := pp.Propagate(context.Background(), changes); err != nil {
		t.Fatalf("propagate: %v", err)
	}

	if got := received.Load(); got != 0 {
		t.Errorf("expected 0 pushes (skipped self), got %d", got)
	}
}

func TestPeerPropagator_PartialFailure(t *testing.T) {
	var received atomic.Int32

	goodPeer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer goodPeer.Close()

	badPeer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "error", http.StatusInternalServerError)
	}))
	defer badPeer.Close()

	pp := &PeerPropagator{
		cfg:    PropagationConfig{},
		selfIP: "1.2.3.4",
		client: &http.Client{Timeout: 5 * time.Second},
		peers:  []string{goodPeer.Listener.Addr().String(), badPeer.Listener.Addr().String()},
	}

	changes := []ConfigChange{{Action: "increase"}}
	// Partial success (1/2) should NOT return an error.
	if err := pp.Propagate(context.Background(), changes); err != nil {
		t.Fatalf("partial failure should not error: %v", err)
	}
	if got := received.Load(); got != 1 {
		t.Errorf("expected 1 successful push, got %d", got)
	}
}

func TestPeerPropagator_AllFail(t *testing.T) {
	badPeer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "error", http.StatusInternalServerError)
	}))
	defer badPeer.Close()

	pp := &PeerPropagator{
		cfg:    PropagationConfig{},
		selfIP: "1.2.3.4",
		client: &http.Client{Timeout: 5 * time.Second},
		peers:  []string{badPeer.Listener.Addr().String()},
	}

	err := pp.Propagate(context.Background(), []ConfigChange{{Action: "increase"}})
	if err == nil {
		t.Error("expected error when all peers fail")
	}
}

func TestPeerPropagator_HandlePeerPush(t *testing.T) {
	var mu sync.Mutex
	var received []ConfigChange

	pp := &PeerPropagator{cfg: PropagationConfig{}}
	pp.ReceiveUpdates(func(changes []ConfigChange) {
		mu.Lock()
		received = changes
		mu.Unlock()
	})

	handler := pp.HandlePeerPush()

	changes := []ConfigChange{
		{Action: "increase", RuleName: "rule_a", NewValue: 1250},
	}
	body, _ := json.Marshal(changes)

	req := httptest.NewRequest(http.MethodPost, "/autotune/config",
		bytes.NewReader(body))
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w.Code)
	}

	mu.Lock()
	if len(received) != 1 || received[0].RuleName != "rule_a" {
		t.Errorf("unexpected received changes: %+v", received)
	}
	mu.Unlock()
}

func TestPeerPropagator_HandlePeerPush_WrongMethod(t *testing.T) {
	pp := &PeerPropagator{cfg: PropagationConfig{}}
	handler := pp.HandlePeerPush()

	req := httptest.NewRequest(http.MethodGet, "/autotune/config", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestPeerPropagator_NoPeers(t *testing.T) {
	pp := &PeerPropagator{
		cfg:    PropagationConfig{},
		selfIP: "1.2.3.4",
		client: &http.Client{Timeout: 5 * time.Second},
		peers:  nil,
	}

	// No peers: should succeed (nothing to do).
	err := pp.Propagate(context.Background(), []ConfigChange{{Action: "increase"}})
	if err != nil {
		t.Fatalf("no peers should succeed: %v", err)
	}
}
