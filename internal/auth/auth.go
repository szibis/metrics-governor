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
	// Pre-compute basic auth for comparison (avoids base64 encoding per-request)
	var expectedBasicAuth string
	if cfg.BasicAuthUsername != "" && cfg.BasicAuthPassword != "" {
		expectedBasicAuth = "Basic " + basicAuthEncoded(cfg.BasicAuthUsername, cfg.BasicAuthPassword)
	}

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if !cfg.Enabled {
			return handler(ctx, req)
		}

		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}

		if err := validateAuthPrecomputed(md, cfg, expectedBasicAuth); err != nil {
			return nil, status.Error(codes.Unauthenticated, err.Error())
		}

		return handler(ctx, req)
	}
}

// validateAuthPrecomputed validates the authentication metadata using a
// pre-computed basic auth string to avoid base64 encoding on every request.
func validateAuthPrecomputed(md metadata.MD, cfg ServerConfig, precomputedBasicAuth string) error {
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

		if auth[0] != precomputedBasicAuth {
			return fmt.Errorf("invalid basic auth credentials")
		}
		return nil
	}

	return nil
}

// validateAuth validates the authentication metadata.
// It computes the basic auth encoding on each call. For hot paths,
// prefer validateAuthPrecomputed with a pre-computed value.
func validateAuth(md metadata.MD, cfg ServerConfig) error {
	var precomputed string
	if cfg.BasicAuthUsername != "" && cfg.BasicAuthPassword != "" {
		precomputed = "Basic " + basicAuthEncoded(cfg.BasicAuthUsername, cfg.BasicAuthPassword)
	}
	return validateAuthPrecomputed(md, cfg, precomputed)
}

// HTTPMiddleware returns an HTTP middleware for authentication.
func HTTPMiddleware(cfg ServerConfig, next http.Handler) http.Handler {
	// Pre-compute basic auth for comparison (avoids base64 encoding per-request)
	var expectedBasicAuth string
	if cfg.BasicAuthUsername != "" && cfg.BasicAuthPassword != "" {
		expectedBasicAuth = "Basic " + basicAuthEncoded(cfg.BasicAuthUsername, cfg.BasicAuthPassword)
	}

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

		// Check basic auth using pre-computed value
		if expectedBasicAuth != "" {
			if auth != expectedBasicAuth {
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
	// Pre-compute auth header values (avoids base64 encoding and string concat per-request)
	var precomputedBearerAuth string
	if cfg.BearerToken != "" {
		precomputedBearerAuth = "Bearer " + cfg.BearerToken
	}

	var precomputedBasicAuth string
	if cfg.BasicAuthUsername != "" && cfg.BasicAuthPassword != "" {
		precomputedBasicAuth = "Basic " + basicAuthEncoded(cfg.BasicAuthUsername, cfg.BasicAuthPassword)
	}

	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		md := metadata.MD{}

		// Add bearer token
		if precomputedBearerAuth != "" {
			md.Set("authorization", precomputedBearerAuth)
		}

		// Add basic auth
		if precomputedBasicAuth != "" {
			md.Set("authorization", precomputedBasicAuth)
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

	// Pre-compute basic auth (avoids base64 encoding per-request)
	var basicAuth string
	if cfg.BasicAuthUsername != "" && cfg.BasicAuthPassword != "" {
		basicAuth = "Basic " + basicAuthEncoded(cfg.BasicAuthUsername, cfg.BasicAuthPassword)
	}

	return &authTransport{
		base:      base,
		cfg:       cfg,
		basicAuth: basicAuth,
	}
}

type authTransport struct {
	base      http.RoundTripper
	cfg       ClientConfig
	basicAuth string // pre-computed "Basic <base64>" header value
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request to avoid modifying the original
	reqClone := req.Clone(req.Context())

	// Add bearer token
	if t.cfg.BearerToken != "" {
		reqClone.Header.Set("Authorization", "Bearer "+t.cfg.BearerToken)
	}

	// Add basic auth using pre-computed value
	if t.basicAuth != "" {
		reqClone.Header.Set("Authorization", t.basicAuth)
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
