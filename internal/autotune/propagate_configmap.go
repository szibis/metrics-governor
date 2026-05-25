package autotune

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// ConfigMapPropagator patches a K8s ConfigMap with autotune changes.
// Follower pods detect the ConfigMap update via volume mount and
// the sidecar sends SIGHUP to trigger ReloadConfig().
type ConfigMapPropagator struct {
	cfg       PropagationConfig
	apiHost   string
	token     string
	namespace string
	client    *http.Client
	handler   func([]ConfigChange)
}

// NewConfigMapPropagator creates a ConfigMap-based propagator.
func NewConfigMapPropagator(cfg PropagationConfig) (*ConfigMapPropagator, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, fmt.Errorf("not running in Kubernetes (KUBERNETES_SERVICE_HOST/PORT not set)")
	}

	tokenBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		return nil, fmt.Errorf("read service account token: %w", err)
	}

	ns := cfg.ConfigMapNamespace
	if ns == "" {
		nsBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
		if err != nil {
			return nil, fmt.Errorf("read pod namespace: %w", err)
		}
		ns = string(nsBytes)
	}

	return &ConfigMapPropagator{
		cfg:       cfg,
		apiHost:   fmt.Sprintf("https://%s:%s", host, port),
		token:     string(tokenBytes),
		namespace: ns,
		client:    &http.Client{Timeout: 10 * time.Second},
	}, nil
}

// Propagate patches the ConfigMap with the latest changes.
func (cmp *ConfigMapPropagator) Propagate(ctx context.Context, changes []ConfigChange) error {
	data, err := json.Marshal(changes)
	if err != nil {
		return fmt.Errorf("marshal changes: %w", err)
	}

	// Strategic merge patch: update the "autotune-changes" key.
	patch := map[string]any{
		"data": map[string]string{
			"autotune-changes": string(data),
		},
	}
	patchBody, err := json.Marshal(patch)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/api/v1/namespaces/%s/configmaps/%s",
		cmp.apiHost, cmp.namespace, cmp.cfg.ConfigMapName)

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewReader(patchBody))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cmp.token)
	req.Header.Set("Content-Type", "application/strategic-merge-patch+json")

	resp, err := cmp.client.Do(req)
	if err != nil {
		return fmt.Errorf("patch configmap: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("patch configmap: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

// ReceiveUpdates registers a handler for incoming config changes.
// For ConfigMap mode, the sidecar handles detection and sends SIGHUP.
// This handler is a no-op since ConfigMap propagation relies on the
// existing sidecar + SIGHUP mechanism.
func (cmp *ConfigMapPropagator) ReceiveUpdates(handler func([]ConfigChange)) {
	cmp.handler = handler
}

// Start is a no-op for ConfigMap propagation (sidecar handles detection).
func (cmp *ConfigMapPropagator) Start(_ context.Context) error { return nil }

// Stop is a no-op for ConfigMap propagation.
func (cmp *ConfigMapPropagator) Stop() {}
