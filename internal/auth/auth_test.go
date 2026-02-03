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

func TestGRPCServerInterceptorDisabled(t *testing.T) {
	cfg := ServerConfig{
		Enabled: false,
	}

	interceptor := GRPCServerInterceptor(cfg)

	called := false
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		called = true
		return "response", nil
	}

	ctx := context.Background()
	resp, err := interceptor(ctx, nil, nil, handler)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !called {
		t.Error("handler should be called when auth is disabled")
	}
	if resp != "response" {
		t.Errorf("expected 'response', got %v", resp)
	}
}

func TestGRPCServerInterceptorMissingMetadata(t *testing.T) {
	cfg := ServerConfig{
		Enabled:     true,
		BearerToken: "secret-token",
	}

	interceptor := GRPCServerInterceptor(cfg)

	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		t.Error("handler should not be called without metadata")
		return nil, nil
	}

	ctx := context.Background() // No metadata
	_, err := interceptor(ctx, nil, nil, handler)

	if err == nil {
		t.Error("expected error for missing metadata")
	}
}

func TestGRPCServerInterceptorValidBearerToken(t *testing.T) {
	cfg := ServerConfig{
		Enabled:     true,
		BearerToken: "secret-token",
	}

	interceptor := GRPCServerInterceptor(cfg)

	called := false
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		called = true
		return "response", nil
	}

	md := metadata.New(map[string]string{
		"authorization": "Bearer secret-token",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	resp, err := interceptor(ctx, nil, nil, handler)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !called {
		t.Error("handler should be called with valid token")
	}
	if resp != "response" {
		t.Errorf("expected 'response', got %v", resp)
	}
}

func TestGRPCServerInterceptorInvalidBearerToken(t *testing.T) {
	cfg := ServerConfig{
		Enabled:     true,
		BearerToken: "secret-token",
	}

	interceptor := GRPCServerInterceptor(cfg)

	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		t.Error("handler should not be called with invalid token")
		return nil, nil
	}

	md := metadata.New(map[string]string{
		"authorization": "Bearer wrong-token",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	_, err := interceptor(ctx, nil, nil, handler)

	if err == nil {
		t.Error("expected error for invalid token")
	}
}

func TestGRPCServerInterceptorBasicAuth(t *testing.T) {
	cfg := ServerConfig{
		Enabled:           true,
		BasicAuthUsername: "user",
		BasicAuthPassword: "pass",
	}

	interceptor := GRPCServerInterceptor(cfg)

	called := false
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		called = true
		return "response", nil
	}

	md := metadata.New(map[string]string{
		"authorization": "Basic " + basicAuthEncoded("user", "pass"),
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	resp, err := interceptor(ctx, nil, nil, handler)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !called {
		t.Error("handler should be called with valid basic auth")
	}
	if resp != "response" {
		t.Errorf("expected 'response', got %v", resp)
	}
}

func TestValidateAuthBasicAuthInvalid(t *testing.T) {
	cfg := ServerConfig{
		Enabled:           true,
		BasicAuthUsername: "user",
		BasicAuthPassword: "pass",
	}

	md := metadata.New(map[string]string{
		"authorization": "Basic " + basicAuthEncoded("user", "wrong"),
	})

	err := validateAuth(md, cfg)
	if err == nil {
		t.Error("expected error for invalid basic auth")
	}
}

func TestValidateAuthBasicAuthMissingHeader(t *testing.T) {
	cfg := ServerConfig{
		Enabled:           true,
		BasicAuthUsername: "user",
		BasicAuthPassword: "pass",
	}

	md := metadata.MD{}

	err := validateAuth(md, cfg)
	if err == nil {
		t.Error("expected error for missing authorization header")
	}
}

func TestValidateAuthNoAuthConfigured(t *testing.T) {
	cfg := ServerConfig{
		Enabled: true,
		// No bearer token or basic auth configured
	}

	md := metadata.MD{}

	err := validateAuth(md, cfg)
	if err != nil {
		t.Errorf("expected no error when no auth is configured, got: %v", err)
	}
}

func TestValidateAuthInvalidFormat(t *testing.T) {
	cfg := ServerConfig{
		Enabled:     true,
		BearerToken: "secret-token",
	}

	md := metadata.New(map[string]string{
		"authorization": "InvalidFormat secret-token",
	})

	err := validateAuth(md, cfg)
	if err == nil {
		t.Error("expected error for invalid format")
	}
}

func TestGRPCClientInterceptorBasicAuth(t *testing.T) {
	cfg := ClientConfig{
		BasicAuthUsername: "user",
		BasicAuthPassword: "pass",
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
	if len(auth) == 0 {
		t.Error("expected authorization header")
	}
	expectedAuth := "Basic " + basicAuthEncoded("user", "pass")
	if auth[0] != expectedAuth {
		t.Errorf("expected '%s', got '%s'", expectedAuth, auth[0])
	}
}

func TestGRPCClientInterceptorCustomHeaders(t *testing.T) {
	cfg := ClientConfig{
		Headers: map[string]string{
			"x-custom-header": "custom-value",
		},
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

	header := md.Get("x-custom-header")
	if len(header) == 0 || header[0] != "custom-value" {
		t.Errorf("expected 'custom-value', got %v", header)
	}
}

func TestGRPCClientInterceptorNoAuth(t *testing.T) {
	cfg := ClientConfig{}

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

	// With no auth configured, metadata might be empty or not set
	md, _ := metadata.FromOutgoingContext(capturedCtx)
	auth := md.Get("authorization")
	if len(auth) > 0 {
		t.Errorf("expected no authorization header, got %v", auth)
	}
}

func TestBasicAuthPrecomputed_HTTPMiddleware(t *testing.T) {
	cfg := ServerConfig{
		Enabled:           true,
		BasicAuthUsername: "admin",
		BasicAuthPassword: "s3cret",
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	handler := HTTPMiddleware(cfg, next)

	// Test valid credentials
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.SetBasicAuth("admin", "s3cret")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !called {
		t.Error("expected handler to be called with valid pre-computed basic auth")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	// Test invalid credentials
	called = false
	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req2.SetBasicAuth("admin", "wrong")
	rec2 := httptest.NewRecorder()

	handler.ServeHTTP(rec2, req2)

	if called {
		t.Error("handler should not be called with invalid basic auth")
	}
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rec2.Code)
	}

	// Test missing authorization header
	called = false
	req3 := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec3 := httptest.NewRecorder()

	handler.ServeHTTP(rec3, req3)

	if called {
		t.Error("handler should not be called without authorization header")
	}
	if rec3.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", rec3.Code)
	}

	// Test that multiple requests work (verifying pre-computed value is reused)
	for i := 0; i < 10; i++ {
		called = false
		reqN := httptest.NewRequest(http.MethodGet, "/test", nil)
		reqN.SetBasicAuth("admin", "s3cret")
		recN := httptest.NewRecorder()

		handler.ServeHTTP(recN, reqN)

		if !called {
			t.Errorf("iteration %d: expected handler to be called", i)
		}
	}
}

func TestBasicAuthPrecomputed_GRPCInterceptor(t *testing.T) {
	cfg := ServerConfig{
		Enabled:           true,
		BasicAuthUsername: "admin",
		BasicAuthPassword: "s3cret",
	}

	interceptor := GRPCServerInterceptor(cfg)

	// Test valid credentials
	called := false
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		called = true
		return "response", nil
	}

	md := metadata.New(map[string]string{
		"authorization": "Basic " + basicAuthEncoded("admin", "s3cret"),
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	resp, err := interceptor(ctx, nil, nil, handler)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !called {
		t.Error("handler should be called with valid pre-computed basic auth")
	}
	if resp != "response" {
		t.Errorf("expected 'response', got %v", resp)
	}

	// Test invalid credentials
	called = false
	handlerNoCall := func(ctx context.Context, req interface{}) (interface{}, error) {
		called = true
		return nil, nil
	}

	mdInvalid := metadata.New(map[string]string{
		"authorization": "Basic " + basicAuthEncoded("admin", "wrong"),
	})
	ctxInvalid := metadata.NewIncomingContext(context.Background(), mdInvalid)

	_, err = interceptor(ctxInvalid, nil, nil, handlerNoCall)

	if err == nil {
		t.Error("expected error for invalid basic auth credentials")
	}
	if called {
		t.Error("handler should not be called with invalid basic auth")
	}

	// Test missing authorization header
	called = false
	mdEmpty := metadata.MD{}
	ctxEmpty := metadata.NewIncomingContext(context.Background(), mdEmpty)

	_, err = interceptor(ctxEmpty, nil, nil, handlerNoCall)

	if err == nil {
		t.Error("expected error for missing authorization header")
	}
	if called {
		t.Error("handler should not be called without authorization header")
	}

	// Test that multiple requests work (verifying pre-computed value is reused)
	for i := 0; i < 10; i++ {
		called = false
		handlerLoop := func(ctx context.Context, req interface{}) (interface{}, error) {
			called = true
			return "ok", nil
		}
		ctxLoop := metadata.NewIncomingContext(context.Background(), md)

		_, err = interceptor(ctxLoop, nil, nil, handlerLoop)

		if err != nil {
			t.Errorf("iteration %d: unexpected error: %v", i, err)
		}
		if !called {
			t.Errorf("iteration %d: expected handler to be called", i)
		}
	}
}
