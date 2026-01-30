package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// BenchmarkHTTPMiddleware_BearerToken benchmarks HTTP middleware with bearer token
func BenchmarkHTTPMiddleware_BearerToken(b *testing.B) {
	cfg := ServerConfig{
		Enabled:     true,
		BearerToken: "test-token-12345",
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	middleware := HTTPMiddleware(cfg, handler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer test-token-12345")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		middleware.ServeHTTP(rec, req)
	}
}

// BenchmarkHTTPMiddleware_BasicAuth benchmarks HTTP middleware with basic auth
func BenchmarkHTTPMiddleware_BasicAuth(b *testing.B) {
	cfg := ServerConfig{
		Enabled:           true,
		BasicAuthUsername: "admin",
		BasicAuthPassword: "password123",
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	middleware := HTTPMiddleware(cfg, handler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// "admin:password123" base64 encoded
	req.Header.Set("Authorization", "Basic YWRtaW46cGFzc3dvcmQxMjM=")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		middleware.ServeHTTP(rec, req)
	}
}

// BenchmarkHTTPMiddleware_Disabled benchmarks middleware when auth is disabled
func BenchmarkHTTPMiddleware_Disabled(b *testing.B) {
	cfg := ServerConfig{
		Enabled: false,
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	middleware := HTTPMiddleware(cfg, handler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		middleware.ServeHTTP(rec, req)
	}
}

// BenchmarkHTTPTransport_BearerToken benchmarks the HTTP client transport with auth
func BenchmarkHTTPTransport_BearerToken(b *testing.B) {
	cfg := ClientConfig{
		BearerToken: "test-token-12345",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	transport := HTTPTransport(cfg, http.DefaultTransport)
	client := &http.Client{Transport: transport}

	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, _ := client.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
	}
}

// BenchmarkHTTPTransport_CustomHeaders benchmarks with custom headers
func BenchmarkHTTPTransport_CustomHeaders(b *testing.B) {
	cfg := ClientConfig{
		Headers: map[string]string{
			"X-Custom-Header-1": "value1",
			"X-Custom-Header-2": "value2",
			"X-Custom-Header-3": "value3",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	transport := HTTPTransport(cfg, http.DefaultTransport)
	client := &http.Client{Transport: transport}

	req, _ := http.NewRequest(http.MethodGet, server.URL, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, _ := client.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
	}
}

// BenchmarkGRPCClientInterceptor_BearerToken benchmarks the gRPC client interceptor
func BenchmarkGRPCClientInterceptor_BearerToken(b *testing.B) {
	cfg := ClientConfig{
		BearerToken: "test-token-12345",
	}

	interceptor := GRPCClientInterceptor(cfg)
	ctx := context.Background()

	invoker := func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		return nil
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = interceptor(ctx, "/test.Service/Method", nil, nil, nil, invoker)
	}
}

// BenchmarkGRPCServerInterceptor_Enabled benchmarks the gRPC server interceptor
func BenchmarkGRPCServerInterceptor_Enabled(b *testing.B) {
	cfg := ServerConfig{
		Enabled:     true,
		BearerToken: "test-token",
	}

	interceptor := GRPCServerInterceptor(cfg)

	md := metadata.New(map[string]string{
		"authorization": "Bearer test-token",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return nil, nil
	}

	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/Method",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = interceptor(ctx, nil, info, handler)
	}
}

// BenchmarkGRPCServerInterceptor_Disabled benchmarks interceptor when disabled
func BenchmarkGRPCServerInterceptor_Disabled(b *testing.B) {
	cfg := ServerConfig{
		Enabled: false,
	}

	interceptor := GRPCServerInterceptor(cfg)
	ctx := context.Background()

	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return nil, nil
	}

	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/Method",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = interceptor(ctx, nil, info, handler)
	}
}

// BenchmarkGRPCServerInterceptor_Concurrent benchmarks concurrent auth validation
func BenchmarkGRPCServerInterceptor_Concurrent(b *testing.B) {
	cfg := ServerConfig{
		Enabled:     true,
		BearerToken: "test-token",
	}

	interceptor := GRPCServerInterceptor(cfg)

	md := metadata.New(map[string]string{
		"authorization": "Bearer test-token",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return nil, nil
	}

	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/Method",
	}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = interceptor(ctx, nil, info, handler)
		}
	})
}

// BenchmarkHTTPMiddleware_Concurrent benchmarks concurrent HTTP middleware
func BenchmarkHTTPMiddleware_Concurrent(b *testing.B) {
	cfg := ServerConfig{
		Enabled:     true,
		BearerToken: "test-token-12345",
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	middleware := HTTPMiddleware(cfg, handler)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Authorization", "Bearer test-token-12345")
			rec := httptest.NewRecorder()
			middleware.ServeHTTP(rec, req)
		}
	})
}
