package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestHTTPMiddlewareDisabled(t *testing.T) {
	cfg := ServerConfig{
		Enabled: false,
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	handler := HTTPMiddleware(cfg, next)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("expected handler to be called when auth is disabled")
	}
}

func TestHTTPMiddlewareMissingAuth(t *testing.T) {
	cfg := ServerConfig{
		Enabled:     true,
		BearerToken: "secret-token",
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called without auth")
	})

	handler := HTTPMiddleware(cfg, next)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rec.Code)
	}
}

func TestHTTPMiddlewareBearerTokenValid(t *testing.T) {
	cfg := ServerConfig{
		Enabled:     true,
		BearerToken: "secret-token",
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	handler := HTTPMiddleware(cfg, next)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("expected handler to be called with valid token")
	}
}

func TestHTTPMiddlewareBearerTokenInvalid(t *testing.T) {
	cfg := ServerConfig{
		Enabled:     true,
		BearerToken: "secret-token",
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called with invalid token")
	})

	handler := HTTPMiddleware(cfg, next)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rec.Code)
	}
}

func TestHTTPMiddlewareBasicAuthValid(t *testing.T) {
	cfg := ServerConfig{
		Enabled:           true,
		BasicAuthUsername: "user",
		BasicAuthPassword: "pass",
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	handler := HTTPMiddleware(cfg, next)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.SetBasicAuth("user", "pass")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("expected handler to be called with valid basic auth")
	}
}

func TestHTTPMiddlewareBasicAuthInvalid(t *testing.T) {
	cfg := ServerConfig{
		Enabled:           true,
		BasicAuthUsername: "user",
		BasicAuthPassword: "pass",
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called with invalid basic auth")
	})

	handler := HTTPMiddleware(cfg, next)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.SetBasicAuth("user", "wrong")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rec.Code)
	}
}

func TestHTTPMiddlewareMalformedBearerToken(t *testing.T) {
	cfg := ServerConfig{
		Enabled:     true,
		BearerToken: "secret-token",
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	})

	handler := HTTPMiddleware(cfg, next)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "NotBearer secret-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rec.Code)
	}
}

func TestValidateAuthBearerToken(t *testing.T) {
	cfg := ServerConfig{
		Enabled:     true,
		BearerToken: "secret-token",
	}

	md := metadata.New(map[string]string{
		"authorization": "Bearer secret-token",
	})

	err := validateAuth(md, cfg)
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestValidateAuthBearerTokenInvalid(t *testing.T) {
	cfg := ServerConfig{
		Enabled:     true,
		BearerToken: "secret-token",
	}

	md := metadata.New(map[string]string{
		"authorization": "Bearer wrong-token",
	})

	err := validateAuth(md, cfg)
	if err == nil {
		t.Error("expected error for invalid token")
	}
}

func TestValidateAuthMissingHeader(t *testing.T) {
	cfg := ServerConfig{
		Enabled:     true,
		BearerToken: "secret-token",
	}

	md := metadata.MD{}

	err := validateAuth(md, cfg)
	if err == nil {
		t.Error("expected error for missing header")
	}
}

func TestHTTPTransportBearerToken(t *testing.T) {
	cfg := ClientConfig{
		BearerToken: "secret-token",
	}

	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
	}))
	defer server.Close()

	transport := HTTPTransport(cfg, nil)
	client := &http.Client{Transport: transport}

	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	client.Do(req)

	if receivedAuth != "Bearer secret-token" {
		t.Errorf("expected 'Bearer secret-token', got '%s'", receivedAuth)
	}
}

func TestHTTPTransportBasicAuth(t *testing.T) {
	cfg := ClientConfig{
		BasicAuthUsername: "user",
		BasicAuthPassword: "pass",
	}

	var receivedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
	}))
	defer server.Close()

	transport := HTTPTransport(cfg, nil)
	client := &http.Client{Transport: transport}

	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	client.Do(req)

	if receivedAuth == "" {
		t.Error("expected Authorization header to be set")
	}
	// Should start with "Basic "
	if len(receivedAuth) < 6 || receivedAuth[:6] != "Basic " {
		t.Errorf("expected Basic auth header, got '%s'", receivedAuth)
	}
}

func TestHTTPTransportCustomHeaders(t *testing.T) {
	cfg := ClientConfig{
		Headers: map[string]string{
			"X-Custom-Header": "custom-value",
			"X-Another":       "another-value",
		},
	}

	var headers http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
	}))
	defer server.Close()

	transport := HTTPTransport(cfg, nil)
	client := &http.Client{Transport: transport}

	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)
	client.Do(req)

	if headers.Get("X-Custom-Header") != "custom-value" {
		t.Errorf("expected 'custom-value', got '%s'", headers.Get("X-Custom-Header"))
	}
	if headers.Get("X-Another") != "another-value" {
		t.Errorf("expected 'another-value', got '%s'", headers.Get("X-Another"))
	}
}

func TestGRPCClientInterceptor(t *testing.T) {
	cfg := ClientConfig{
		BearerToken: "secret-token",
	}

	interceptor := GRPCClientInterceptor(cfg)

	var capturedCtx context.Context

	ctx := context.Background()
	err := interceptor(ctx, "/test.Service/Method", nil, nil, nil,
		func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
			capturedCtx = ctx
			return nil
		})

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	md, ok := metadata.FromOutgoingContext(capturedCtx)
	if !ok {
		t.Fatal("expected metadata in context")
	}

	auth := md.Get("authorization")
	if len(auth) == 0 || auth[0] != "Bearer secret-token" {
		t.Errorf("expected 'Bearer secret-token', got %v", auth)
	}
}

func TestBasicAuthEncoded(t *testing.T) {
	encoded := basicAuthEncoded("user", "pass")
	// "user:pass" base64 encoded is "dXNlcjpwYXNz"
	expected := "dXNlcjpwYXNz"
	if encoded != expected {
		t.Errorf("expected '%s', got '%s'", expected, encoded)
	}
}
