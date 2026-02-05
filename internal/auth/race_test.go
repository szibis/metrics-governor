package auth

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// mockRoundTripper is a mock http.RoundTripper that returns a fixed response.
type mockRoundTripper struct{}

func (m *mockRoundTripper) RoundTrip(_ *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader("ok")),
	}, nil
}

// ---------------------------------------------------------------------------
// Race condition tests
// ---------------------------------------------------------------------------

func TestRace_HTTPMiddleware_ConcurrentRequests(t *testing.T) {
	cfg := ServerConfig{
		Enabled:     true,
		BearerToken: "test-token",
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	middleware := HTTPMiddleware(cfg, next)

	server := httptest.NewServer(middleware)
	defer server.Close()

	const goroutines = 8
	const iterations = 300

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			client := server.Client()
			for i := 0; i < iterations; i++ {
				req, err := http.NewRequest(http.MethodGet, server.URL+"/test", nil)
				if err != nil {
					t.Errorf("failed to create request: %v", err)
					return
				}
				req.Header.Set("Authorization", "Bearer test-token")

				resp, err := client.Do(req)
				if err != nil {
					t.Errorf("request failed: %v", err)
					return
				}
				resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					t.Errorf("expected status 200, got %d", resp.StatusCode)
					return
				}
			}
		}()
	}

	wg.Wait()
}

func TestRace_HTTPTransport_ConcurrentRoundTrip(t *testing.T) {
	cfg := ClientConfig{
		BearerToken: "test-token",
	}

	transport := HTTPTransport(cfg, &mockRoundTripper{})

	const goroutines = 8
	const iterations = 300

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				req := httptest.NewRequest(http.MethodGet, "http://example.com/test", nil)

				resp, err := transport.RoundTrip(req)
				if err != nil {
					t.Errorf("round trip failed: %v", err)
					return
				}
				resp.Body.Close()

				if resp.StatusCode != 200 {
					t.Errorf("expected status 200, got %d", resp.StatusCode)
					return
				}
			}
		}()
	}

	wg.Wait()
}

func TestRace_HTTPMiddleware_BearerToken(t *testing.T) {
	cfg := ServerConfig{
		Enabled:     true,
		BearerToken: "race-bearer-token",
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	middleware := HTTPMiddleware(cfg, next)

	server := httptest.NewServer(middleware)
	defer server.Close()

	const goroutines = 8
	const iterations = 300

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			client := server.Client()
			for i := 0; i < iterations; i++ {
				req, err := http.NewRequest(http.MethodGet, server.URL+"/bearer", nil)
				if err != nil {
					t.Errorf("goroutine %d: failed to create request: %v", id, err)
					return
				}
				req.Header.Set("Authorization", "Bearer race-bearer-token")

				resp, err := client.Do(req)
				if err != nil {
					t.Errorf("goroutine %d: request failed: %v", id, err)
					return
				}
				resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					t.Errorf("goroutine %d: expected status 200, got %d", id, resp.StatusCode)
					return
				}
			}
		}(g)
	}

	wg.Wait()
}

func TestRace_HTTPMiddleware_BasicAuth(t *testing.T) {
	cfg := ServerConfig{
		Enabled:           true,
		BasicAuthUsername: "raceuser",
		BasicAuthPassword: "racepass",
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	middleware := HTTPMiddleware(cfg, next)

	server := httptest.NewServer(middleware)
	defer server.Close()

	const goroutines = 8
	const iterations = 300

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			client := server.Client()
			for i := 0; i < iterations; i++ {
				req, err := http.NewRequest(http.MethodGet, server.URL+"/basic", nil)
				if err != nil {
					t.Errorf("goroutine %d: failed to create request: %v", id, err)
					return
				}
				req.SetBasicAuth("raceuser", "racepass")

				resp, err := client.Do(req)
				if err != nil {
					t.Errorf("goroutine %d: request failed: %v", id, err)
					return
				}
				resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					t.Errorf("goroutine %d: expected status 200, got %d", id, resp.StatusCode)
					return
				}
			}
		}(g)
	}

	wg.Wait()
}

func TestRace_HTTPTransport_CustomHeaders(t *testing.T) {
	cfg := ClientConfig{
		BearerToken: "header-token",
		Headers: map[string]string{
			"X-Custom-One":   "value-one",
			"X-Custom-Two":   "value-two",
			"X-Custom-Three": "value-three",
		},
	}

	transport := HTTPTransport(cfg, &mockRoundTripper{})

	const goroutines = 8
	const iterations = 300

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				req := httptest.NewRequest(http.MethodPost, "http://example.com/headers", nil)

				resp, err := transport.RoundTrip(req)
				if err != nil {
					t.Errorf("goroutine %d: round trip failed: %v", id, err)
					return
				}
				resp.Body.Close()

				if resp.StatusCode != 200 {
					t.Errorf("goroutine %d: expected status 200, got %d", id, resp.StatusCode)
					return
				}
			}
		}(g)
	}

	wg.Wait()
}

// ---------------------------------------------------------------------------
// Memory leak tests
// ---------------------------------------------------------------------------

func TestMemLeak_HTTPMiddleware_RequestCycles(t *testing.T) {
	cfg := ServerConfig{
		Enabled:     true,
		BearerToken: "memleak-token",
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	middleware := HTTPMiddleware(cfg, next)

	server := httptest.NewServer(middleware)
	defer server.Close()

	client := server.Client()

	// Warm up to stabilize allocations.
	for i := 0; i < 100; i++ {
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/memleak", nil)
		req.Header.Set("Authorization", "Bearer memleak-token")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("warm-up request failed: %v", err)
		}
		resp.Body.Close()
	}

	// Force GC and record baseline.
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	const iterations = 500

	for i := 0; i < iterations; i++ {
		req, _ := http.NewRequest(http.MethodGet, server.URL+"/memleak", nil)
		req.Header.Set("Authorization", "Bearer memleak-token")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("iteration %d: request failed: %v", i, err)
		}
		resp.Body.Close()
	}

	// Force GC and measure.
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	const maxGrowthBytes = 20 * 1024 * 1024 // 20 MB

	heapGrowth := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	t.Logf("middleware heap before: %d bytes, after: %d bytes, growth: %d bytes",
		before.HeapAlloc, after.HeapAlloc, heapGrowth)

	if heapGrowth > int64(maxGrowthBytes) {
		t.Errorf("potential memory leak: heap grew by %d bytes (limit %d bytes)",
			heapGrowth, maxGrowthBytes)
	}
}

func TestMemLeak_HTTPTransport_RoundTripCycles(t *testing.T) {
	cfg := ClientConfig{
		BearerToken: "memleak-transport-token",
		Headers: map[string]string{
			"X-Leak-Check": "true",
		},
	}

	transport := HTTPTransport(cfg, &mockRoundTripper{})

	// Warm up to stabilize allocations.
	for i := 0; i < 100; i++ {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/memleak", nil)
		resp, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatalf("warm-up round trip failed: %v", err)
		}
		resp.Body.Close()
	}

	// Force GC and record baseline.
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	const iterations = 500

	for i := 0; i < iterations; i++ {
		req := httptest.NewRequest(http.MethodGet,
			fmt.Sprintf("http://example.com/memleak/%d", i), nil)
		resp, err := transport.RoundTrip(req)
		if err != nil {
			t.Fatalf("iteration %d: round trip failed: %v", i, err)
		}
		resp.Body.Close()
	}

	// Force GC and measure.
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	const maxGrowthBytes = 20 * 1024 * 1024 // 20 MB

	heapGrowth := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	t.Logf("transport heap before: %d bytes, after: %d bytes, growth: %d bytes",
		before.HeapAlloc, after.HeapAlloc, heapGrowth)

	if heapGrowth > int64(maxGrowthBytes) {
		t.Errorf("potential memory leak: heap grew by %d bytes (limit %d bytes)",
			heapGrowth, maxGrowthBytes)
	}
}
