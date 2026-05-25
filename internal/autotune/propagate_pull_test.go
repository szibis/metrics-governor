package autotune

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestPullPropagator_HandleGetConfig(t *testing.T) {
	pp := NewPullPropagator(PropagationConfig{})

	changes := []ConfigChange{
		{Action: "increase", RuleName: "rule_a", NewValue: 1250},
	}
	if err := pp.Propagate(context.Background(), changes); err != nil {
		t.Fatalf("propagate: %v", err)
	}

	handler := pp.HandleGetConfig()
	req := httptest.NewRequest(http.MethodGet, "/autotune/config", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("ETag") == "" {
		t.Error("expected ETag header")
	}

	var got []ConfigChange
	json.NewDecoder(w.Body).Decode(&got)
	if len(got) != 1 || got[0].RuleName != "rule_a" {
		t.Errorf("unexpected response: %+v", got)
	}
}

func TestPullPropagator_ETagCaching(t *testing.T) {
	pp := NewPullPropagator(PropagationConfig{})

	changes := []ConfigChange{{Action: "increase"}}
	pp.Propagate(context.Background(), changes)

	handler := pp.HandleGetConfig()

	// First request: get ETag.
	req1 := httptest.NewRequest(http.MethodGet, "/autotune/config", nil)
	w1 := httptest.NewRecorder()
	handler(w1, req1)
	etag := w1.Header().Get("ETag")

	// Second request with If-None-Match: should get 304.
	req2 := httptest.NewRequest(http.MethodGet, "/autotune/config", nil)
	req2.Header.Set("If-None-Match", etag)
	w2 := httptest.NewRecorder()
	handler(w2, req2)

	if w2.Code != http.StatusNotModified {
		t.Errorf("expected 304, got %d", w2.Code)
	}
}

func TestPullPropagator_ETagChangesOnUpdate(t *testing.T) {
	pp := NewPullPropagator(PropagationConfig{})

	pp.Propagate(context.Background(), []ConfigChange{{Action: "increase"}})
	handler := pp.HandleGetConfig()

	// Get first ETag.
	req1 := httptest.NewRequest(http.MethodGet, "/autotune/config", nil)
	w1 := httptest.NewRecorder()
	handler(w1, req1)
	etag1 := w1.Header().Get("ETag")

	// Update with different changes.
	pp.Propagate(context.Background(), []ConfigChange{{Action: "decrease"}})

	// Get second ETag.
	req2 := httptest.NewRequest(http.MethodGet, "/autotune/config", nil)
	w2 := httptest.NewRecorder()
	handler(w2, req2)
	etag2 := w2.Header().Get("ETag")

	if etag1 == etag2 {
		t.Error("ETag should change when config changes")
	}
}

func TestPullPropagator_EmptyConfig(t *testing.T) {
	pp := NewPullPropagator(PropagationConfig{})

	handler := pp.HandleGetConfig()
	req := httptest.NewRequest(http.MethodGet, "/autotune/config", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != "[]" {
		t.Errorf("expected empty array, got %q", w.Body.String())
	}
}

func TestPullPropagator_WrongMethod(t *testing.T) {
	pp := NewPullPropagator(PropagationConfig{})
	handler := pp.HandleGetConfig()

	req := httptest.NewRequest(http.MethodPost, "/autotune/config", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestPullPropagator_FollowerPoll(t *testing.T) {
	// Create a mock leader serving /autotune/config.
	changes := []ConfigChange{{Action: "increase", RuleName: "rule_a"}}

	var mu sync.Mutex
	var received []ConfigChange

	leader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"test-etag"`)
		json.NewEncoder(w).Encode(changes)
	}))
	defer leader.Close()

	pp := NewPullPropagator(PropagationConfig{
		PeerService:  leader.Listener.Addr().String(),
		PullInterval: 50 * time.Millisecond,
	})
	pp.ReceiveUpdates(func(c []ConfigChange) {
		mu.Lock()
		received = c
		mu.Unlock()
	})

	// Manually call fetchFromLeader to test the pull logic.
	etag, err := pp.fetchFromLeader(context.Background(), "")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if etag != `"test-etag"` {
		t.Errorf("expected etag %q, got %q", `"test-etag"`, etag)
	}

	mu.Lock()
	if len(received) != 1 || received[0].RuleName != "rule_a" {
		t.Errorf("unexpected received: %+v", received)
	}
	mu.Unlock()
}

func TestPullPropagator_FollowerPollNotModified(t *testing.T) {
	leader := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"cached"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"cached"`)
		json.NewEncoder(w).Encode([]ConfigChange{})
	}))
	defer leader.Close()

	pp := NewPullPropagator(PropagationConfig{
		PeerService: leader.Listener.Addr().String(),
	})

	// First fetch: gets data.
	etag, err := pp.fetchFromLeader(context.Background(), "")
	if err != nil {
		t.Fatalf("first fetch: %v", err)
	}

	// Second fetch with ETag: gets 304.
	etag2, err := pp.fetchFromLeader(context.Background(), etag)
	if err != nil {
		t.Fatalf("second fetch: %v", err)
	}
	if etag2 != etag {
		t.Errorf("etag should remain same on 304, got %q", etag2)
	}
}

func TestPullPropagator_StartStop(t *testing.T) {
	pp := NewPullPropagator(PropagationConfig{
		PeerService:  "localhost:19999",
		PullInterval: 50 * time.Millisecond,
	})

	ctx := context.Background()
	if err := pp.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Give it a couple of cycles.
	time.Sleep(150 * time.Millisecond)

	pp.Stop()
}
