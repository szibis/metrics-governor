package autotune

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// LeaseElector uses the K8s coordination.k8s.io/v1 Lease API for leader election.
// It communicates directly with the K8s API server using the in-cluster service account
// token, avoiding any dependency on client-go.
type LeaseElector struct {
	cfg      HAConfig
	identity string // this pod's identity (hostname or POD_NAME)

	apiHost   string // K8s API server URL
	token     string // service account bearer token
	namespace string // pod namespace

	isLeader atomic.Bool
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	client   *http.Client
}

// leaseMeta represents Lease ObjectMeta fields we need.
type leaseMeta struct {
	Name            string `json:"name"`
	Namespace       string `json:"namespace"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

// leaseSpec represents Lease spec fields.
type leaseSpec struct {
	HolderIdentity       *string `json:"holderIdentity,omitempty"`
	LeaseDurationSeconds *int    `json:"leaseDurationSeconds,omitempty"`
	AcquireTime          *string `json:"acquireTime,omitempty"`
	RenewTime            *string `json:"renewTime,omitempty"`
}

// leaseObject represents a K8s Lease resource.
type leaseObject struct {
	APIVersion string    `json:"apiVersion"`
	Kind       string    `json:"kind"`
	Metadata   leaseMeta `json:"metadata"`
	Spec       leaseSpec `json:"spec"`
}

// NewLeaseElector creates a K8s Lease-based leader elector.
func NewLeaseElector(cfg HAConfig) (*LeaseElector, error) {
	// Detect in-cluster config.
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, fmt.Errorf("not running in Kubernetes (KUBERNETES_SERVICE_HOST/PORT not set)")
	}

	tokenBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		return nil, fmt.Errorf("read service account token: %w", err)
	}

	nsBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return nil, fmt.Errorf("read pod namespace: %w", err)
	}

	ns := string(nsBytes)
	if cfg.LeaseNamespace != "" {
		ns = cfg.LeaseNamespace
	}

	identity, _ := os.Hostname()
	if podName := os.Getenv("POD_NAME"); podName != "" {
		identity = podName
	}

	return &LeaseElector{
		cfg:       cfg,
		identity:  identity,
		apiHost:   fmt.Sprintf("https://%s:%s", host, port),
		token:     string(tokenBytes),
		namespace: ns,
		client:    &http.Client{Timeout: 5 * time.Second},
	}, nil
}

// IsLeader returns whether this instance currently holds the lease.
func (le *LeaseElector) IsLeader() bool {
	return le.isLeader.Load()
}

// Start begins the lease renewal loop.
func (le *LeaseElector) Start(ctx context.Context) error {
	ctx, le.cancel = context.WithCancel(ctx)

	le.wg.Add(1)
	go le.run(ctx)
	return nil
}

// Stop cancels the lease renewal loop.
func (le *LeaseElector) Stop() {
	if le.cancel != nil {
		le.cancel()
	}
	le.wg.Wait()
}

// run is the main lease acquisition/renewal loop.
func (le *LeaseElector) run(ctx context.Context) {
	defer le.wg.Done()

	renewInterval := max(le.cfg.LeaseDuration/3, time.Second)

	ticker := time.NewTicker(renewInterval)
	defer ticker.Stop()

	// Try to acquire immediately on start.
	le.tryAcquireOrRenew(ctx)

	for {
		select {
		case <-ctx.Done():
			le.isLeader.Store(false)
			return
		case <-ticker.C:
			le.tryAcquireOrRenew(ctx)
		}
	}
}

// tryAcquireOrRenew attempts to acquire or renew the lease.
func (le *LeaseElector) tryAcquireOrRenew(ctx context.Context) {
	lease, err := le.getLease(ctx)
	if err != nil {
		// Lease doesn't exist — create it.
		if le.createLease(ctx) == nil {
			le.isLeader.Store(true)
		}
		return
	}

	now := time.Now().UTC()
	holder := ""
	if lease.Spec.HolderIdentity != nil {
		holder = *lease.Spec.HolderIdentity
	}

	// Check if we hold the lease.
	if holder == le.identity {
		// Renew: update renewTime.
		renewTime := now.Format(time.RFC3339Nano)
		lease.Spec.RenewTime = &renewTime
		if le.updateLease(ctx, lease) == nil {
			le.isLeader.Store(true)
		}
		return
	}

	// Check if lease is expired.
	if lease.Spec.RenewTime != nil {
		lastRenew, err := time.Parse(time.RFC3339Nano, *lease.Spec.RenewTime)
		if err == nil && now.Sub(lastRenew) < le.cfg.LeaseDuration {
			// Lease is still held by someone else and not expired.
			le.isLeader.Store(false)
			return
		}
	}

	// Lease is expired or has no renew time — try to take over.
	id := le.identity
	lease.Spec.HolderIdentity = &id
	acquireTime := now.Format(time.RFC3339Nano)
	lease.Spec.AcquireTime = &acquireTime
	lease.Spec.RenewTime = &acquireTime
	if le.updateLease(ctx, lease) == nil {
		le.isLeader.Store(true)
	} else {
		// Lost the race — another pod got it.
		le.isLeader.Store(false)
	}
}

// getLease fetches the Lease object from the K8s API.
func (le *LeaseElector) getLease(ctx context.Context) (*leaseObject, error) {
	url := fmt.Sprintf("%s/apis/coordination.k8s.io/v1/namespaces/%s/leases/%s",
		le.apiHost, le.namespace, le.cfg.LeaseName)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+le.token)

	resp, err := le.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("lease not found")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get lease: status %d: %s", resp.StatusCode, body)
	}

	var lease leaseObject
	if err := json.NewDecoder(resp.Body).Decode(&lease); err != nil {
		return nil, err
	}
	return &lease, nil
}

// createLease creates a new Lease object.
func (le *LeaseElector) createLease(ctx context.Context) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	durationSec := int(le.cfg.LeaseDuration.Seconds())
	id := le.identity

	lease := leaseObject{
		APIVersion: "coordination.k8s.io/v1",
		Kind:       "Lease",
		Metadata: leaseMeta{
			Name:      le.cfg.LeaseName,
			Namespace: le.namespace,
		},
		Spec: leaseSpec{
			HolderIdentity:       &id,
			LeaseDurationSeconds: &durationSec,
			AcquireTime:          &now,
			RenewTime:            &now,
		},
	}

	body, err := json.Marshal(lease)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/apis/coordination.k8s.io/v1/namespaces/%s/leases",
		le.apiHost, le.namespace)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+le.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := le.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create lease: status %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

// updateLease updates an existing Lease object (uses resourceVersion for optimistic concurrency).
func (le *LeaseElector) updateLease(ctx context.Context, lease *leaseObject) error {
	body, err := json.Marshal(lease)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/apis/coordination.k8s.io/v1/namespaces/%s/leases/%s",
		le.apiHost, le.namespace, le.cfg.LeaseName)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+le.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := le.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("update lease: status %d: %s", resp.StatusCode, respBody)
	}
	return nil
}
