package auth

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// ServerConfig holds authentication configuration for servers (receivers).
type ServerConfig struct {
	// Enabled enables authentication for the server.
	Enabled bool
	// BearerToken is the expected bearer token for authentication.
	BearerToken string
	// BasicAuthUsername is the username for basic authentication.
	BasicAuthUsername string
	// BasicAuthPassword is the password for basic authentication.
	BasicAuthPassword string
}

// ClientConfig holds authentication configuration for clients (exporters).
type ClientConfig struct {
	// BearerToken is the bearer token to send with requests.
	BearerToken string
	// BasicAuthUsername is the username for basic authentication.
	BasicAuthUsername string
	// BasicAuthPassword is the password for basic authentication.
	BasicAuthPassword string
	// Headers is a map of custom headers to send with requests.
	Headers map[string]string
}

// GRPCServerInterceptor returns a unary interceptor for gRPC server authentication.
func GRPCServerInterceptor(cfg ServerConfig) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if !cfg.Enabled {
			return handler(ctx, req)
		}

		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}

		if err := validateAuth(md, cfg); err != nil {
			return nil, status.Error(codes.Unauthenticated, err.Error())
		}

		return handler(ctx, req)
	}
}

// validateAuth validates the authentication metadata.
func validateAuth(md metadata.MD, cfg ServerConfig) error {
	// Check bearer token
	if cfg.BearerToken != "" {
		auth := md.Get("authorization")
		if len(auth) == 0 {
			return fmt.Errorf("missing authorization header")
		}

		token := strings.TrimPrefix(auth[0], "Bearer ")
		if token == auth[0] {
			return fmt.Errorf("invalid authorization header format")
		}

		if token != cfg.BearerToken {
			return fmt.Errorf("invalid bearer token")
		}
		return nil
	}

	// Check basic auth
	if cfg.BasicAuthUsername != "" && cfg.BasicAuthPassword != "" {
		auth := md.Get("authorization")
		if len(auth) == 0 {
			return fmt.Errorf("missing authorization header")
		}

		// Basic auth is already base64 encoded in the header
		expected := "Basic " + basicAuthEncoded(cfg.BasicAuthUsername, cfg.BasicAuthPassword)
		if auth[0] != expected {
			return fmt.Errorf("invalid basic auth credentials")
		}
		return nil
	}

	return nil
}

// HTTPMiddleware returns an HTTP middleware for authentication.
func HTTPMiddleware(cfg ServerConfig, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !cfg.Enabled {
			next.ServeHTTP(w, r)
			return
		}

		auth := r.Header.Get("Authorization")
		if auth == "" {
			http.Error(w, "missing authorization header", http.StatusUnauthorized)
			return
		}

		// Check bearer token
		if cfg.BearerToken != "" {
			token := strings.TrimPrefix(auth, "Bearer ")
			if token == auth {
				http.Error(w, "invalid authorization header format", http.StatusUnauthorized)
				return
			}
			if token != cfg.BearerToken {
				http.Error(w, "invalid bearer token", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		// Check basic auth
		if cfg.BasicAuthUsername != "" && cfg.BasicAuthPassword != "" {
			expected := "Basic " + basicAuthEncoded(cfg.BasicAuthUsername, cfg.BasicAuthPassword)
			if auth != expected {
				http.Error(w, "invalid basic auth credentials", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// GRPCClientInterceptor returns a unary interceptor for gRPC client authentication.
func GRPCClientInterceptor(cfg ClientConfig) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		md := metadata.MD{}

		// Add bearer token
		if cfg.BearerToken != "" {
			md.Set("authorization", "Bearer "+cfg.BearerToken)
		}

		// Add basic auth
		if cfg.BasicAuthUsername != "" && cfg.BasicAuthPassword != "" {
			md.Set("authorization", "Basic "+basicAuthEncoded(cfg.BasicAuthUsername, cfg.BasicAuthPassword))
		}

		// Add custom headers
		for k, v := range cfg.Headers {
			md.Set(k, v)
		}

		if len(md) > 0 {
			ctx = metadata.NewOutgoingContext(ctx, md)
		}

		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// HTTPTransport returns an http.RoundTripper that adds authentication headers.
func HTTPTransport(cfg ClientConfig, base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &authTransport{
		base: base,
		cfg:  cfg,
	}
}

type authTransport struct {
	base http.RoundTripper
	cfg  ClientConfig
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request to avoid modifying the original
	reqClone := req.Clone(req.Context())

	// Add bearer token
	if t.cfg.BearerToken != "" {
		reqClone.Header.Set("Authorization", "Bearer "+t.cfg.BearerToken)
	}

	// Add basic auth
	if t.cfg.BasicAuthUsername != "" && t.cfg.BasicAuthPassword != "" {
		reqClone.SetBasicAuth(t.cfg.BasicAuthUsername, t.cfg.BasicAuthPassword)
	}

	// Add custom headers
	for k, v := range t.cfg.Headers {
		reqClone.Header.Set(k, v)
	}

	return t.base.RoundTrip(reqClone)
}

// basicAuthEncoded returns the base64 encoded basic auth string.
func basicAuthEncoded(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}
