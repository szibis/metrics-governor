package health

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLiveHandler_Healthy(t *testing.T) {
	c := New()
	req := httptest.NewRequest(http.MethodGet, "/live", nil)
	rec := httptest.NewRecorder()

	c.LiveHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != StatusUp {
		t.Fatalf("expected status up, got %s", resp.Status)
	}
}

func TestLiveHandler_ShuttingDown(t *testing.T) {
	c := New()
	c.SetShuttingDown()

	req := httptest.NewRequest(http.MethodGet, "/live", nil)
	rec := httptest.NewRecorder()

	c.LiveHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}

	var resp Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != StatusDown {
		t.Fatalf("expected status down, got %s", resp.Status)
	}
}

func TestReadyHandler_AllHealthy(t *testing.T) {
	c := New()
	c.RegisterReadiness("grpc_receiver", func() error { return nil })
	c.RegisterReadiness("http_receiver", func() error { return nil })

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	c.ReadyHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != StatusUp {
		t.Fatalf("expected status up, got %s", resp.Status)
	}
	if len(resp.Components) != 2 {
		t.Fatalf("expected 2 components, got %d", len(resp.Components))
	}
}

func TestReadyHandler_OneDown(t *testing.T) {
	c := New()
	c.RegisterReadiness("grpc_receiver", func() error { return nil })
	c.RegisterReadiness("exporter", func() error {
		return errors.New("connection refused")
	})

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	c.ReadyHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}

	var resp Response
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != StatusDown {
		t.Fatalf("expected status down, got %s", resp.Status)
	}
	expComp := resp.Components["exporter"]
	if expComp.Status != StatusDown {
		t.Fatalf("expected exporter down, got %s", expComp.Status)
	}
	if expComp.Message != "connection refused" {
		t.Fatalf("unexpected message: %s", expComp.Message)
	}
}

func TestReadyHandler_ShuttingDown(t *testing.T) {
	c := New()
	c.RegisterReadiness("grpc_receiver", func() error { return nil })
	c.SetShuttingDown()

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	c.ReadyHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

func TestReadyHandler_NoChecks(t *testing.T) {
	c := New()

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	rec := httptest.NewRecorder()

	c.ReadyHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with no checks, got %d", rec.Code)
	}
}

func TestResponseContentType(t *testing.T) {
	c := New()
	req := httptest.NewRequest(http.MethodGet, "/live", nil)
	rec := httptest.NewRecorder()

	c.LiveHandler().ServeHTTP(rec, req)

	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Fatalf("expected application/json, got %s", ct)
	}
}
