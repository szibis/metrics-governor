package autotune

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestLeaseElector_AcquireAndRenew(t *testing.T) {
	var (
		mu          sync.Mutex
		storedLease *leaseObject
		creates     int
		updates     int
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		switch r.Method {
		case http.MethodGet:
			if storedLease == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			json.NewEncoder(w).Encode(storedLease)

		case http.MethodPost:
			var lease leaseObject
			json.NewDecoder(r.Body).Decode(&lease)
			lease.Metadata.ResourceVersion = "1"
			storedLease = &lease
			creates++
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(storedLease)

		case http.MethodPut:
			var lease leaseObject
			json.NewDecoder(r.Body).Decode(&lease)
			lease.Metadata.ResourceVersion = "2"
			storedLease = &lease
			updates++
			json.NewEncoder(w).Encode(storedLease)
		}
	}))
	defer srv.Close()

	le := &LeaseElector{
		cfg: HAConfig{
			LeaseName:     "test-lease",
			LeaseDuration: 15 * time.Second,
		},
		identity:  "pod-1",
		apiHost:   srv.URL,
		token:     "test-token",
		namespace: "default",
		client:    srv.Client(),
	}

	ctx := context.Background()

	// First call: lease doesn't exist → create.
	le.tryAcquireOrRenew(ctx)
	if !le.IsLeader() {
		t.Error("expected to be leader after creating lease")
	}

	mu.Lock()
	if creates != 1 {
		t.Errorf("expected 1 create, got %d", creates)
	}
	mu.Unlock()

	// Second call: we hold the lease → renew.
	le.tryAcquireOrRenew(ctx)
	if !le.IsLeader() {
		t.Error("expected to still be leader after renewal")
	}

	mu.Lock()
	if updates != 1 {
		t.Errorf("expected 1 update, got %d", updates)
	}
	mu.Unlock()
}

func TestLeaseElector_ExpiredLeaseTakeover(t *testing.T) {
	// Simulate an expired lease held by another pod.
	expiredTime := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339Nano)
	storedLease := &leaseObject{
		APIVersion: "coordination.k8s.io/v1",
		Kind:       "Lease",
		Metadata:   leaseMeta{Name: "test", Namespace: "default", ResourceVersion: "1"},
		Spec: leaseSpec{
			HolderIdentity: strPtr("pod-old"),
			RenewTime:      &expiredTime,
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(storedLease)
		case http.MethodPut:
			json.NewEncoder(w).Encode(storedLease)
		}
	}))
	defer srv.Close()

	le := &LeaseElector{
		cfg: HAConfig{
			LeaseName:     "test",
			LeaseDuration: 15 * time.Second,
		},
		identity:  "pod-new",
		apiHost:   srv.URL,
		token:     "test-token",
		namespace: "default",
		client:    srv.Client(),
	}

	le.tryAcquireOrRenew(context.Background())
	if !le.IsLeader() {
		t.Error("expected to take over expired lease")
	}
}

func TestLeaseElector_ActiveLeaseRejected(t *testing.T) {
	// Simulate an active lease held by another pod (renewed recently).
	recentTime := time.Now().UTC().Format(time.RFC3339Nano)
	storedLease := &leaseObject{
		APIVersion: "coordination.k8s.io/v1",
		Kind:       "Lease",
		Metadata:   leaseMeta{Name: "test", Namespace: "default", ResourceVersion: "1"},
		Spec: leaseSpec{
			HolderIdentity: strPtr("pod-other"),
			RenewTime:      &recentTime,
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(storedLease)
	}))
	defer srv.Close()

	le := &LeaseElector{
		cfg: HAConfig{
			LeaseName:     "test",
			LeaseDuration: 15 * time.Second,
		},
		identity:  "pod-new",
		apiHost:   srv.URL,
		token:     "test-token",
		namespace: "default",
		client:    srv.Client(),
	}

	le.tryAcquireOrRenew(context.Background())
	if le.IsLeader() {
		t.Error("should NOT be leader when another pod holds active lease")
	}
}

func TestLeaseElector_StartStop(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound) // lease not found → try create
	}))
	defer srv.Close()

	le := &LeaseElector{
		cfg: HAConfig{
			LeaseName:     "test",
			LeaseDuration: 5 * time.Second,
		},
		identity:  "pod-1",
		apiHost:   srv.URL,
		token:     "test-token",
		namespace: "default",
		client:    srv.Client(),
	}

	ctx := context.Background()
	if err := le.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Give the goroutine time to run at least once.
	time.Sleep(100 * time.Millisecond)

	le.Stop()

	// After stop, should no longer be leader.
	if le.IsLeader() {
		t.Error("expected not leader after stop")
	}
}

func strPtr(s string) *string { return &s }
