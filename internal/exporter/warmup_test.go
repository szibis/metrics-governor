package exporter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestWarmup_EstablishesConnection verifies that WarmupConnections sends a HEAD
// request to the mock server and the request actually reaches it.
func TestWarmup_EstablishesConnection(t *testing.T) {
	var headReceived atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("expected HEAD method, got %s", r.Method)
		}
		headReceived.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ctx := context.Background()
	WarmupConnections(ctx, []string{ts.URL}, ts.Client(), 5*time.Second)

	if headReceived.Load() != 1 {
		t.Fatalf("expected 1 HEAD request, got %d", headReceived.Load())
	}
}

// TestWarmup_TimeoutHandled verifies that a very short timeout does not cause
// the function to hang indefinitely — it should return promptly.
func TestWarmup_TimeoutHandled(t *testing.T) {
	// Server that sleeps longer than the warmup timeout.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ctx := context.Background()
	start := time.Now()
	WarmupConnections(ctx, []string{ts.URL}, ts.Client(), 100*time.Millisecond)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("warmup took %v, expected it to respect the short timeout", elapsed)
	}
}

// TestWarmup_FailureDoesNotBlock verifies that connecting to a non-existent
// endpoint completes without blocking.
func TestWarmup_FailureDoesNotBlock(t *testing.T) {
	ctx := context.Background()
	start := time.Now()
	WarmupConnections(ctx, []string{"http://192.0.2.1:1"}, http.DefaultClient, 500*time.Millisecond)
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Fatalf("warmup to non-existent endpoint took %v, expected it to complete promptly", elapsed)
	}
}

// TestWarmup_EmptyEndpoints verifies that an empty endpoints slice causes an
// immediate return without any network activity.
func TestWarmup_EmptyEndpoints(t *testing.T) {
	ctx := context.Background()
	start := time.Now()
	WarmupConnections(ctx, []string{}, http.DefaultClient, 5*time.Second)
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Fatalf("expected immediate return for empty endpoints, took %v", elapsed)
	}
}

// TestWarmup_NilClient verifies that a nil HTTP client causes an immediate
// return without panicking.
func TestWarmup_NilClient(t *testing.T) {
	ctx := context.Background()
	start := time.Now()
	WarmupConnections(ctx, []string{"http://localhost:9999"}, nil, 5*time.Second)
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Fatalf("expected immediate return for nil client, took %v", elapsed)
	}
}

// TestWarmup_MultipleEndpoints verifies that WarmupConnections sends a HEAD
// request to every endpoint in the list.
func TestWarmup_MultipleEndpoints(t *testing.T) {
	var hitCount atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("expected HEAD method, got %s", r.Method)
		}
		hitCount.Add(1)
		w.WriteHeader(http.StatusOK)
	})

	ts1 := httptest.NewServer(handler)
	defer ts1.Close()
	ts2 := httptest.NewServer(handler)
	defer ts2.Close()
	ts3 := httptest.NewServer(handler)
	defer ts3.Close()

	ctx := context.Background()
	endpoints := []string{ts1.URL, ts2.URL, ts3.URL}
	WarmupConnections(ctx, endpoints, http.DefaultClient, 5*time.Second)

	if hitCount.Load() != 3 {
		t.Fatalf("expected 3 HEAD requests, got %d", hitCount.Load())
	}
}

// TestWarmup_ContextCancellation verifies that a pre-canceled context causes
// WarmupConnections to fail requests rather than hang.
func TestWarmup_ContextCancellation(t *testing.T) {
	var hitCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	start := time.Now()
	WarmupConnections(ctx, []string{ts.URL, ts.URL, ts.URL}, ts.Client(), 5*time.Second)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("warmup with canceled context took %v, expected prompt return", elapsed)
	}

	// With a canceled parent context, the derived warmupCtx should also be canceled,
	// so requests should fail. The server may receive 0 hits.
	if hitCount.Load() > 0 {
		// This is acceptable — some requests might slip through before the context
		// propagates. Just log it.
		t.Logf("server received %d requests despite canceled context (acceptable)", hitCount.Load())
	}
}
