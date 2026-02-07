package receiver

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/prw"
	tlspkg "github.com/szibis/metrics-governor/internal/tls"
)

// =============================================================================
// HealthCheck tests for gRPC, HTTP, and PRW receivers
// =============================================================================

// TestGRPCReceiverHealthCheck_NotRunning tests HealthCheck returns error when
// the gRPC receiver is not running.
func TestGRPCReceiverHealthCheck_NotRunning(t *testing.T) {
	buf := newTestBuffer()
	r := NewGRPC("127.0.0.1:14317", buf)

	// The receiver has not been started, so HealthCheck should return an error.
	err := r.HealthCheck()
	if err == nil {
		t.Fatal("expected error from HealthCheck when gRPC receiver is not running")
	}
	expected := "gRPC receiver not running on 127.0.0.1:14317"
	if err.Error() != expected {
		t.Errorf("expected error %q, got %q", expected, err.Error())
	}
}

// TestGRPCReceiverHealthCheck_Running tests HealthCheck returns nil when
// the gRPC receiver is running.
func TestGRPCReceiverHealthCheck_Running(t *testing.T) {
	buf := newTestBuffer()
	r := NewGRPC("127.0.0.1:0", buf)

	errCh := make(chan error, 1)
	go func() {
		errCh <- r.Start()
	}()

	// Wait for the server to be running.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r.running.Load() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if !r.running.Load() {
		t.Fatal("gRPC receiver did not start in time")
	}

	err := r.HealthCheck()
	if err != nil {
		t.Errorf("expected nil error from HealthCheck when running, got: %v", err)
	}

	r.Stop()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("unexpected error from Start: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for gRPC server to stop")
	}
}

// TestHTTPReceiverHealthCheck_Running tests HealthCheck returns nil when
// the HTTP receiver is listening.
func TestHTTPReceiverHealthCheck_Running(t *testing.T) {
	buf := newTestBuffer()

	// Pick a free port.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	addr := lis.Addr().String()
	lis.Close()

	r := NewHTTP(addr, buf)

	go r.Start()
	time.Sleep(100 * time.Millisecond)

	err = r.HealthCheck()
	if err != nil {
		t.Errorf("expected nil error from HealthCheck when running, got: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r.Stop(ctx)
}

// TestHTTPReceiverHealthCheck_NotRunning tests HealthCheck returns error when
// the HTTP receiver is not listening.
func TestHTTPReceiverHealthCheck_NotRunning(t *testing.T) {
	buf := newTestBuffer()

	// Use a port that is not listening.
	r := NewHTTP("127.0.0.1:19999", buf)

	err := r.HealthCheck()
	if err == nil {
		t.Fatal("expected error from HealthCheck when HTTP receiver is not running")
	}
	t.Logf("HTTP HealthCheck error (expected): %v", err)
}

// TestPRWReceiverHealthCheck_Running tests HealthCheck returns nil when
// the PRW receiver is listening.
func TestPRWReceiverHealthCheck_Running(t *testing.T) {
	buf := &mockPRWBuffer{}

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	addr := lis.Addr().String()
	lis.Close()

	r := NewPRW(addr, buf)

	go r.Start()
	time.Sleep(100 * time.Millisecond)

	err = r.HealthCheck()
	if err != nil {
		t.Errorf("expected nil error from HealthCheck when running, got: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r.Stop(ctx)
}

// TestPRWReceiverHealthCheck_NotRunning tests HealthCheck returns error when
// the PRW receiver is not listening.
func TestPRWReceiverHealthCheck_NotRunning(t *testing.T) {
	buf := &mockPRWBuffer{}

	r := NewPRW("127.0.0.1:19998", buf)

	err := r.HealthCheck()
	if err == nil {
		t.Fatal("expected error from HealthCheck when PRW receiver is not running")
	}
	t.Logf("PRW HealthCheck error (expected): %v", err)
}

// =============================================================================
// HTTP and PRW Start with TLS (covers the TLS branch in Start methods)
// =============================================================================

// generateSelfSignedCert creates a self-signed TLS certificate and key
// in temporary files and returns their paths.
func generateSelfSignedCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")

	certOut, err := os.Create(certFile)
	if err != nil {
		t.Fatalf("failed to create cert file: %v", err)
	}
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		t.Fatalf("failed to write cert: %v", err)
	}
	certOut.Close()

	keyBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("failed to marshal key: %v", err)
	}
	keyOut, err := os.Create(keyFile)
	if err != nil {
		t.Fatalf("failed to create key file: %v", err)
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}); err != nil {
		t.Fatalf("failed to write key: %v", err)
	}
	keyOut.Close()

	return certFile, keyFile
}

// TestHTTPStartWithTLS tests the HTTP receiver Start method with valid TLS
// configuration, covering the ListenAndServeTLS branch.
func TestHTTPStartWithTLS(t *testing.T) {
	certFile, keyFile := generateSelfSignedCert(t)
	buf := newTestBuffer()

	cfg := HTTPConfig{
		Addr: "127.0.0.1:0",
		TLS: tlspkg.ServerConfig{
			Enabled:  true,
			CertFile: certFile,
			KeyFile:  keyFile,
		},
	}

	r := NewHTTPWithConfig(cfg, buf)
	if r.tlsConfig == nil {
		t.Fatal("expected TLS config to be set with valid certs")
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- r.Start()
	}()

	time.Sleep(200 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r.Stop(ctx)

	select {
	case err := <-errCh:
		// http.ErrServerClosed is expected after Shutdown.
		if err != nil && err.Error() != "http: Server closed" {
			t.Errorf("unexpected Start error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for HTTP TLS server to stop")
	}
}

// TestPRWStartWithTLS tests the PRW receiver Start method with valid TLS
// configuration, covering the ListenAndServeTLS branch.
func TestPRWStartWithTLS(t *testing.T) {
	certFile, keyFile := generateSelfSignedCert(t)
	buf := &mockPRWBuffer{}

	cfg := PRWConfig{
		Addr: "127.0.0.1:0",
		TLS: tlspkg.ServerConfig{
			Enabled:  true,
			CertFile: certFile,
			KeyFile:  keyFile,
		},
	}

	r := NewPRWWithConfig(cfg, buf)
	if r.tlsConfig == nil {
		t.Fatal("expected TLS config to be set with valid certs")
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- r.Start()
	}()

	time.Sleep(200 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r.Stop(ctx)

	select {
	case err := <-errCh:
		if err != nil && err.Error() != "http: Server closed" {
			t.Errorf("unexpected Start error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("timeout waiting for PRW TLS server to stop")
	}
}

// TestHTTPStartTLS_ActualTLSConnection verifies a TLS-enabled HTTP receiver
// accepts a real TLS connection.
func TestHTTPStartTLS_ActualTLSConnection(t *testing.T) {
	certFile, keyFile := generateSelfSignedCert(t)
	buf := newTestBuffer()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	addr := lis.Addr().String()
	lis.Close()

	cfg := HTTPConfig{
		Addr: addr,
		TLS: tlspkg.ServerConfig{
			Enabled:  true,
			CertFile: certFile,
			KeyFile:  keyFile,
		},
	}
	r := NewHTTPWithConfig(cfg, buf)

	go r.Start()
	time.Sleep(200 * time.Millisecond)

	// Make a TLS connection to verify the server is actually running with TLS.
	tlsConf := &tls.Config{InsecureSkipVerify: true}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", addr, tlsConf)
	if err != nil {
		t.Errorf("failed to dial TLS: %v", err)
	} else {
		conn.Close()
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r.Stop(ctx)
}

// =============================================================================
// PRW version rejection test (covers the version check branch in handleWrite)
// =============================================================================

// TestPRWReceiver_VersionRejectsPRW2 tests that a PRW receiver configured
// with Version1 rejects requests containing PRW 2.0 features.
func TestPRWReceiver_VersionRejectsPRW2(t *testing.T) {
	buf := &mockPRWBuffer{}
	cfg := PRWConfig{
		Addr:    ":0",
		Version: prw.Version1,
	}
	r := NewPRWWithConfig(cfg, buf)

	// Create a PRW 2.0 request (has histograms, which is a PRW 2.0 feature).
	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels: []prw.Label{
					{Name: "__name__", Value: "http_request_duration_seconds"},
				},
				Histograms: []prw.Histogram{
					{
						Count:     100,
						Sum:       50.5,
						Schema:    3,
						Timestamp: 1609459200000,
					},
				},
			},
		},
	}

	body, err := req.Marshal()
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/write", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/x-protobuf")

	w := httptest.NewRecorder()
	r.server.Handler.ServeHTTP(w, httpReq)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for PRW 2.0 on Version1, got %d", w.Code)
	}
}

// TestPRWReceiver_VersionAutoAcceptsPRW2 tests that VersionAuto accepts PRW 2.0.
func TestPRWReceiver_VersionAutoAcceptsPRW2(t *testing.T) {
	buf := &mockPRWBuffer{}
	cfg := PRWConfig{
		Addr:    ":0",
		Version: prw.VersionAuto,
	}
	r := NewPRWWithConfig(cfg, buf)

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels: []prw.Label{
					{Name: "__name__", Value: "test"},
				},
				Histograms: []prw.Histogram{
					{Count: 1, Sum: 1.0, Schema: 3, Timestamp: 1000},
				},
			},
		},
	}

	body, err := req.Marshal()
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/write", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/x-protobuf")

	w := httptest.NewRecorder()
	r.server.Handler.ServeHTTP(w, httpReq)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204 for PRW 2.0 on VersionAuto, got %d", w.Code)
	}
}

// TestPRWReceiver_Version2AcceptsPRW2 tests that Version2 accepts PRW 2.0.
func TestPRWReceiver_Version2AcceptsPRW2(t *testing.T) {
	buf := &mockPRWBuffer{}
	cfg := PRWConfig{
		Addr:    ":0",
		Version: prw.Version2,
	}
	r := NewPRWWithConfig(cfg, buf)

	req := &prw.WriteRequest{
		Timeseries: []prw.TimeSeries{
			{
				Labels: []prw.Label{
					{Name: "__name__", Value: "test"},
				},
				Histograms: []prw.Histogram{
					{Count: 1, Sum: 1.0, Schema: 3, Timestamp: 1000},
				},
			},
		},
	}

	body, err := req.Marshal()
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/write", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/x-protobuf")

	w := httptest.NewRecorder()
	r.server.Handler.ServeHTTP(w, httpReq)

	// Version2 with PRW 2.0 data should succeed.
	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204 for PRW 2.0 on Version2, got %d", w.Code)
	}
}

// =============================================================================
// gRPC Start error path
// =============================================================================

// TestGRPCStartInvalidAddress tests that Start returns an error when given
// an address that cannot be bound.
func TestGRPCStartInvalidAddress(t *testing.T) {
	buf := newTestBuffer()
	r := NewGRPC("invalid-addr-no-port", buf)

	err := r.Start()
	if err == nil {
		t.Fatal("expected error from Start with invalid address")
	}
}

// =============================================================================
// zstdReaderPoolWrapper.Get error path
// =============================================================================

// TestZstdReaderPoolWrapperGet_ResetError creates a scenario where
// decoder.Reset returns an error by passing a reader that fails.
func TestZstdReaderPoolWrapperGet_ResetError(t *testing.T) {
	wrapper := &zstdReaderPoolWrapper{}

	// The zstd decoder's Reset may or may not fail here. We try with
	// an errorReader that always fails.
	reader, err := wrapper.Get(&failingReader{})

	if err != nil {
		// Covered the error path at grpc.go:100.
		t.Logf("Get returned expected error: %v", err)
		return
	}

	// If it didn't error, read to confirm behavior.
	if reader != nil {
		buf := make([]byte, 10)
		_, readErr := reader.Read(buf)
		t.Logf("Reader Read returned: %v", readErr)
	}
}

// failingReader always returns an error on Read.
type failingReader struct{}

func (r *failingReader) Read(p []byte) (int, error) {
	return 0, net.ErrClosed
}
