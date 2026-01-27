package tls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServerConfigDisabled(t *testing.T) {
	cfg := ServerConfig{
		Enabled: false,
	}

	tlsConfig, err := NewServerTLSConfig(cfg)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if tlsConfig != nil {
		t.Error("expected nil TLS config when disabled")
	}
}

func TestClientConfigDisabled(t *testing.T) {
	cfg := ClientConfig{
		Enabled: false,
	}

	tlsConfig, err := NewClientTLSConfig(cfg)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if tlsConfig != nil {
		t.Error("expected nil TLS config when disabled")
	}
}

func TestServerConfigMissingCert(t *testing.T) {
	cfg := ServerConfig{
		Enabled:  true,
		CertFile: "/nonexistent/cert.pem",
		KeyFile:  "/nonexistent/key.pem",
	}

	_, err := NewServerTLSConfig(cfg)
	if err == nil {
		t.Error("expected error for missing certificate files")
	}
}

func TestClientConfigMissingCert(t *testing.T) {
	cfg := ClientConfig{
		Enabled:  true,
		CertFile: "/nonexistent/cert.pem",
		KeyFile:  "/nonexistent/key.pem",
	}

	_, err := NewClientTLSConfig(cfg)
	if err == nil {
		t.Error("expected error for missing certificate files")
	}
}

func TestClientConfigMissingCA(t *testing.T) {
	cfg := ClientConfig{
		Enabled: true,
		CAFile:  "/nonexistent/ca.pem",
	}

	_, err := NewClientTLSConfig(cfg)
	if err == nil {
		t.Error("expected error for missing CA file")
	}
}

func TestServerConfigMissingCA(t *testing.T) {
	// Create temp cert files
	tmpDir, err := os.MkdirTemp("", "tls-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	certFile := filepath.Join(tmpDir, "server.crt")
	keyFile := filepath.Join(tmpDir, "server.key")

	// Generate self-signed cert for testing
	if err := generateSelfSignedCert(certFile, keyFile); err != nil {
		t.Fatalf("failed to generate cert: %v", err)
	}

	cfg := ServerConfig{
		Enabled:    true,
		CertFile:   certFile,
		KeyFile:    keyFile,
		CAFile:     "/nonexistent/ca.pem",
		ClientAuth: true,
	}

	_, err = NewServerTLSConfig(cfg)
	if err == nil {
		t.Error("expected error for missing CA file")
	}
}

func TestServerConfigValidCert(t *testing.T) {
	// Create temp cert files
	tmpDir, err := os.MkdirTemp("", "tls-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	certFile := filepath.Join(tmpDir, "server.crt")
	keyFile := filepath.Join(tmpDir, "server.key")

	if err := generateSelfSignedCert(certFile, keyFile); err != nil {
		t.Fatalf("failed to generate cert: %v", err)
	}

	cfg := ServerConfig{
		Enabled:  true,
		CertFile: certFile,
		KeyFile:  keyFile,
	}

	tlsConfig, err := NewServerTLSConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tlsConfig == nil {
		t.Fatal("expected non-nil TLS config")
	}
	if len(tlsConfig.Certificates) != 1 {
		t.Errorf("expected 1 certificate, got %d", len(tlsConfig.Certificates))
	}
	if tlsConfig.MinVersion != tls.VersionTLS12 {
		t.Errorf("expected min version TLS 1.2, got %d", tlsConfig.MinVersion)
	}
}

func TestClientConfigValidCert(t *testing.T) {
	// Create temp cert files
	tmpDir, err := os.MkdirTemp("", "tls-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	certFile := filepath.Join(tmpDir, "client.crt")
	keyFile := filepath.Join(tmpDir, "client.key")

	if err := generateSelfSignedCert(certFile, keyFile); err != nil {
		t.Fatalf("failed to generate cert: %v", err)
	}

	cfg := ClientConfig{
		Enabled:  true,
		CertFile: certFile,
		KeyFile:  keyFile,
	}

	tlsConfig, err := NewClientTLSConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tlsConfig == nil {
		t.Fatal("expected non-nil TLS config")
	}
	if len(tlsConfig.Certificates) != 1 {
		t.Errorf("expected 1 certificate, got %d", len(tlsConfig.Certificates))
	}
}

func TestClientConfigInsecureSkipVerify(t *testing.T) {
	cfg := ClientConfig{
		Enabled:            true,
		InsecureSkipVerify: true,
	}

	tlsConfig, err := NewClientTLSConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tlsConfig == nil {
		t.Fatal("expected non-nil TLS config")
	}
	if !tlsConfig.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify to be true")
	}
}

func TestClientConfigServerName(t *testing.T) {
	cfg := ClientConfig{
		Enabled:    true,
		ServerName: "example.com",
	}

	tlsConfig, err := NewClientTLSConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tlsConfig == nil {
		t.Fatal("expected non-nil TLS config")
	}
	if tlsConfig.ServerName != "example.com" {
		t.Errorf("expected ServerName 'example.com', got '%s'", tlsConfig.ServerName)
	}
}

func TestServerConfigClientAuth(t *testing.T) {
	// Create temp cert files
	tmpDir, err := os.MkdirTemp("", "tls-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	certFile := filepath.Join(tmpDir, "server.crt")
	keyFile := filepath.Join(tmpDir, "server.key")
	caFile := filepath.Join(tmpDir, "ca.crt")

	if err := generateSelfSignedCert(certFile, keyFile); err != nil {
		t.Fatalf("failed to generate server cert: %v", err)
	}

	// Use server cert as CA for testing
	if err := copyFile(certFile, caFile); err != nil {
		t.Fatalf("failed to copy CA file: %v", err)
	}

	cfg := ServerConfig{
		Enabled:    true,
		CertFile:   certFile,
		KeyFile:    keyFile,
		CAFile:     caFile,
		ClientAuth: true,
	}

	tlsConfig, err := NewServerTLSConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tlsConfig == nil {
		t.Fatal("expected non-nil TLS config")
	}
	if tlsConfig.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Error("expected ClientAuth to be RequireAndVerifyClientCert")
	}
	if tlsConfig.ClientCAs == nil {
		t.Error("expected ClientCAs to be set")
	}
}

func TestClientConfigWithCA(t *testing.T) {
	// Create temp cert files
	tmpDir, err := os.MkdirTemp("", "tls-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	certFile := filepath.Join(tmpDir, "cert.crt")
	keyFile := filepath.Join(tmpDir, "cert.key")
	caFile := filepath.Join(tmpDir, "ca.crt")

	if err := generateSelfSignedCert(certFile, keyFile); err != nil {
		t.Fatalf("failed to generate cert: %v", err)
	}

	// Use cert as CA for testing
	if err := copyFile(certFile, caFile); err != nil {
		t.Fatalf("failed to copy CA file: %v", err)
	}

	cfg := ClientConfig{
		Enabled: true,
		CAFile:  caFile,
	}

	tlsConfig, err := NewClientTLSConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tlsConfig == nil {
		t.Fatal("expected non-nil TLS config")
	}
	if tlsConfig.RootCAs == nil {
		t.Error("expected RootCAs to be set")
	}
}

// generateSelfSignedCert generates a self-signed certificate for testing.
func generateSelfSignedCert(certFile, keyFile string) error {
	// Generate key
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}

	// Create certificate template
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "test-cert",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	// Create certificate
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return err
	}

	// Write cert
	certOut, err := os.Create(certFile)
	if err != nil {
		return err
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		return err
	}

	// Write key
	keyOut, err := os.Create(keyFile)
	if err != nil {
		return err
	}
	defer keyOut.Close()
	keyDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return err
	}
	return pem.Encode(keyOut, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
