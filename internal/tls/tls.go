package tls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
)

// ServerConfig holds TLS configuration for servers (receivers).
type ServerConfig struct {
	// Enabled enables TLS for the server.
	Enabled bool
	// CertFile is the path to the server certificate file.
	CertFile string
	// KeyFile is the path to the server private key file.
	KeyFile string
	// CAFile is the path to the CA certificate file for client verification (mTLS).
	CAFile string
	// ClientAuth specifies whether client certificates are required.
	ClientAuth bool
}

// ClientConfig holds TLS configuration for clients (exporters).
type ClientConfig struct {
	// Enabled enables TLS for the client.
	Enabled bool
	// CertFile is the path to the client certificate file (for mTLS).
	CertFile string
	// KeyFile is the path to the client private key file (for mTLS).
	KeyFile string
	// CAFile is the path to the CA certificate file for server verification.
	CAFile string
	// InsecureSkipVerify skips server certificate verification.
	InsecureSkipVerify bool
	// ServerName overrides the server name for certificate verification.
	ServerName string
}

// NewServerTLSConfig creates a TLS configuration for servers.
func NewServerTLSConfig(cfg ServerConfig) (*tls.Config, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load server certificate: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	if cfg.ClientAuth && cfg.CAFile != "" {
		caCert, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate: %w", err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}

		tlsConfig.ClientCAs = caCertPool
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	}

	return tlsConfig, nil
}

// NewClientTLSConfig creates a TLS configuration for clients.
func NewClientTLSConfig(cfg ClientConfig) (*tls.Config, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
	}

	if cfg.ServerName != "" {
		tlsConfig.ServerName = cfg.ServerName
	}

	// Load client certificate for mTLS
	if cfg.CertFile != "" && cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	// Load CA certificate for server verification
	if cfg.CAFile != "" {
		caCert, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate: %w", err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}

		tlsConfig.RootCAs = caCertPool
	}

	return tlsConfig, nil
}
