package sharding

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// MockResolver is a mock DNS resolver for testing.
type MockResolver struct {
	mu        sync.Mutex
	responses map[string][]string
	errors    map[string]error
	callCount int64
}

func NewMockResolver() *MockResolver {
	return &MockResolver{
		responses: make(map[string][]string),
		errors:    make(map[string]error),
	}
}

func (m *MockResolver) SetResponse(host string, ips []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses[host] = ips
}

func (m *MockResolver) SetError(host string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.errors[host] = err
}

func (m *MockResolver) LookupHost(ctx context.Context, host string) ([]string, error) {
	atomic.AddInt64(&m.callCount, 1)

	m.mu.Lock()
	defer m.mu.Unlock()

	if err, ok := m.errors[host]; ok {
		return nil, err
	}

	if ips, ok := m.responses[host]; ok {
		result := make([]string, len(ips))
		copy(result, ips)
		return result, nil
	}

	return nil, errors.New("no such host")
}

func (m *MockResolver) GetCallCount() int64 {
	return atomic.LoadInt64(&m.callCount)
}

// Tests

func TestDiscovery_New(t *testing.T) {
	cfg := DiscoveryConfig{
		HeadlessService: "vminsert.monitoring.svc:8428",
		RefreshInterval: 30 * time.Second,
		Timeout:         5 * time.Second,
	}

	d := NewDiscovery(cfg, nil)
	if d == nil {
		t.Fatal("expected non-nil discovery")
	}
}

func TestDiscovery_New_AppliesDefaults(t *testing.T) {
	cfg := DiscoveryConfig{
		HeadlessService: "vminsert.monitoring.svc:8428",
	}

	d := NewDiscovery(cfg, nil)

	if d.config.RefreshInterval != 30*time.Second {
		t.Errorf("expected default RefreshInterval=30s, got %v", d.config.RefreshInterval)
	}
	if d.config.Timeout != 5*time.Second {
		t.Errorf("expected default Timeout=5s, got %v", d.config.Timeout)
	}
}

func TestDiscovery_Refresh_Success(t *testing.T) {
	resolver := NewMockResolver()
	resolver.SetResponse("vminsert.monitoring.svc", []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"})

	var received []string
	var mu sync.Mutex
	onChange := func(endpoints []string) {
		mu.Lock()
		received = endpoints
		mu.Unlock()
	}

	cfg := DiscoveryConfig{
		HeadlessService: "vminsert.monitoring.svc:8428",
		RefreshInterval: 100 * time.Millisecond,
		Timeout:         1 * time.Second,
	}

	d := NewDiscoveryWithResolver(cfg, onChange, resolver)
	ctx := context.Background()
	d.ForceRefresh(ctx)

	// Wait for async callback
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 3 {
		t.Fatalf("expected 3 endpoints, got %d", len(received))
	}

	// Should be sorted and have port
	expected := []string{"10.0.0.1:8428", "10.0.0.2:8428", "10.0.0.3:8428"}
	for i, ep := range expected {
		if received[i] != ep {
			t.Errorf("endpoint %d: expected %s, got %s", i, ep, received[i])
		}
	}
}

func TestDiscovery_Refresh_NoChange(t *testing.T) {
	resolver := NewMockResolver()
	resolver.SetResponse("vminsert.svc", []string{"10.0.0.1", "10.0.0.2"})

	callCount := int32(0)
	onChange := func(endpoints []string) {
		atomic.AddInt32(&callCount, 1)
	}

	cfg := DiscoveryConfig{
		HeadlessService: "vminsert.svc:8428",
		RefreshInterval: 100 * time.Millisecond,
		Timeout:         1 * time.Second,
	}

	d := NewDiscoveryWithResolver(cfg, onChange, resolver)
	ctx := context.Background()

	// First refresh
	d.ForceRefresh(ctx)
	time.Sleep(50 * time.Millisecond)

	// Second refresh (same results)
	d.ForceRefresh(ctx)
	time.Sleep(50 * time.Millisecond)

	// onChange should only be called once (first time)
	if atomic.LoadInt32(&callCount) != 1 {
		t.Errorf("expected onChange called once, got %d", callCount)
	}
}

func TestDiscovery_Refresh_AddEndpoint(t *testing.T) {
	resolver := NewMockResolver()
	resolver.SetResponse("svc", []string{"10.0.0.1", "10.0.0.2"})

	var calls [][]string
	var mu sync.Mutex
	onChange := func(endpoints []string) {
		mu.Lock()
		cp := make([]string, len(endpoints))
		copy(cp, endpoints)
		calls = append(calls, cp)
		mu.Unlock()
	}

	cfg := DiscoveryConfig{
		HeadlessService: "svc:8428",
		RefreshInterval: 100 * time.Millisecond,
		Timeout:         1 * time.Second,
	}

	d := NewDiscoveryWithResolver(cfg, onChange, resolver)
	ctx := context.Background()

	// Initial refresh
	d.ForceRefresh(ctx)
	time.Sleep(50 * time.Millisecond)

	// Add an endpoint
	resolver.SetResponse("svc", []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"})
	d.ForceRefresh(ctx)
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(calls) != 2 {
		t.Fatalf("expected 2 onChange calls, got %d", len(calls))
	}

	if len(calls[0]) != 2 {
		t.Errorf("first call: expected 2 endpoints, got %d", len(calls[0]))
	}
	if len(calls[1]) != 3 {
		t.Errorf("second call: expected 3 endpoints, got %d", len(calls[1]))
	}
}

func TestDiscovery_Refresh_RemoveEndpoint(t *testing.T) {
	resolver := NewMockResolver()
	resolver.SetResponse("svc", []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"})

	var calls [][]string
	var mu sync.Mutex
	onChange := func(endpoints []string) {
		mu.Lock()
		cp := make([]string, len(endpoints))
		copy(cp, endpoints)
		calls = append(calls, cp)
		mu.Unlock()
	}

	cfg := DiscoveryConfig{
		HeadlessService: "svc:8428",
		RefreshInterval: 100 * time.Millisecond,
		Timeout:         1 * time.Second,
	}

	d := NewDiscoveryWithResolver(cfg, onChange, resolver)
	ctx := context.Background()

	// Initial refresh
	d.ForceRefresh(ctx)
	time.Sleep(50 * time.Millisecond)

	// Remove an endpoint
	resolver.SetResponse("svc", []string{"10.0.0.1", "10.0.0.3"})
	d.ForceRefresh(ctx)
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(calls) != 2 {
		t.Fatalf("expected 2 onChange calls, got %d", len(calls))
	}

	if len(calls[1]) != 2 {
		t.Errorf("second call: expected 2 endpoints, got %d", len(calls[1]))
	}
}

func TestDiscovery_Refresh_DNSError(t *testing.T) {
	resolver := NewMockResolver()
	resolver.SetError("svc", errors.New("DNS resolution failed"))

	onChange := func(endpoints []string) {
		t.Error("onChange should not be called on error")
	}

	cfg := DiscoveryConfig{
		HeadlessService: "svc:8428",
		RefreshInterval: 100 * time.Millisecond,
		Timeout:         1 * time.Second,
	}

	d := NewDiscoveryWithResolver(cfg, onChange, resolver)
	ctx := context.Background()

	// Should not panic
	d.ForceRefresh(ctx)

	// Endpoints should remain empty
	endpoints := d.GetEndpoints()
	if len(endpoints) != 0 {
		t.Errorf("expected 0 endpoints after error, got %d", len(endpoints))
	}
}

func TestDiscovery_Refresh_EmptyResult(t *testing.T) {
	resolver := NewMockResolver()
	resolver.SetResponse("svc", []string{})

	callCount := int32(0)
	onChange := func(endpoints []string) {
		atomic.AddInt32(&callCount, 1)
	}

	cfg := DiscoveryConfig{
		HeadlessService:  "svc:8428",
		RefreshInterval:  100 * time.Millisecond,
		Timeout:          1 * time.Second,
		FallbackOnEmpty:  false,
		FallbackEndpoint: "fallback:8428",
	}

	d := NewDiscoveryWithResolver(cfg, onChange, resolver)
	ctx := context.Background()

	d.ForceRefresh(ctx)
	time.Sleep(50 * time.Millisecond)

	// Empty result, no fallback configured
	endpoints := d.GetEndpoints()
	if len(endpoints) != 0 {
		t.Errorf("expected 0 endpoints, got %d", len(endpoints))
	}
}

func TestDiscovery_Refresh_SortsEndpoints(t *testing.T) {
	resolver := NewMockResolver()
	// Return unsorted
	resolver.SetResponse("svc", []string{"10.0.0.3", "10.0.0.1", "10.0.0.2"})

	var received []string
	var mu sync.Mutex
	onChange := func(endpoints []string) {
		mu.Lock()
		received = endpoints
		mu.Unlock()
	}

	cfg := DiscoveryConfig{
		HeadlessService: "svc:8428",
		Timeout:         1 * time.Second,
	}

	d := NewDiscoveryWithResolver(cfg, onChange, resolver)
	d.ForceRefresh(context.Background())
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// Should be sorted
	expected := []string{"10.0.0.1:8428", "10.0.0.2:8428", "10.0.0.3:8428"}
	for i, ep := range expected {
		if received[i] != ep {
			t.Errorf("endpoint %d: expected %s, got %s", i, ep, received[i])
		}
	}
}

func TestDiscovery_FallbackEndpoint_UsedOnEmpty(t *testing.T) {
	resolver := NewMockResolver()
	resolver.SetResponse("svc", []string{})

	var received []string
	var mu sync.Mutex
	onChange := func(endpoints []string) {
		mu.Lock()
		received = endpoints
		mu.Unlock()
	}

	cfg := DiscoveryConfig{
		HeadlessService:  "svc:8428",
		Timeout:          1 * time.Second,
		FallbackOnEmpty:  true,
		FallbackEndpoint: "fallback:8428",
	}

	d := NewDiscoveryWithResolver(cfg, onChange, resolver)
	d.ForceRefresh(context.Background())
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 1 {
		t.Fatalf("expected 1 endpoint (fallback), got %d", len(received))
	}
	if received[0] != "fallback:8428" {
		t.Errorf("expected fallback:8428, got %s", received[0])
	}
}

func TestDiscovery_FallbackEndpoint_NotUsedWhenResults(t *testing.T) {
	resolver := NewMockResolver()
	resolver.SetResponse("svc", []string{"10.0.0.1"})

	var received []string
	var mu sync.Mutex
	onChange := func(endpoints []string) {
		mu.Lock()
		received = endpoints
		mu.Unlock()
	}

	cfg := DiscoveryConfig{
		HeadlessService:  "svc:8428",
		Timeout:          1 * time.Second,
		FallbackOnEmpty:  true,
		FallbackEndpoint: "fallback:8428",
	}

	d := NewDiscoveryWithResolver(cfg, onChange, resolver)
	d.ForceRefresh(context.Background())
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(received))
	}
	if received[0] != "10.0.0.1:8428" {
		t.Errorf("expected 10.0.0.1:8428, got %s", received[0])
	}
}

func TestDiscovery_GetEndpoints(t *testing.T) {
	resolver := NewMockResolver()
	resolver.SetResponse("svc", []string{"10.0.0.1", "10.0.0.2"})

	cfg := DiscoveryConfig{
		HeadlessService: "svc:8428",
		Timeout:         1 * time.Second,
	}

	d := NewDiscoveryWithResolver(cfg, nil, resolver)
	d.ForceRefresh(context.Background())

	endpoints := d.GetEndpoints()
	if len(endpoints) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(endpoints))
	}
}

func TestDiscovery_GetEndpoints_ThreadSafe(t *testing.T) {
	resolver := NewMockResolver()
	resolver.SetResponse("svc", []string{"10.0.0.1", "10.0.0.2"})

	cfg := DiscoveryConfig{
		HeadlessService: "svc:8428",
		Timeout:         1 * time.Second,
	}

	d := NewDiscoveryWithResolver(cfg, nil, resolver)
	d.ForceRefresh(context.Background())

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			endpoints := d.GetEndpoints()
			if endpoints == nil {
				t.Error("endpoints should not be nil")
			}
		}()
	}
	wg.Wait()
}

func TestDiscovery_Start_PeriodicRefresh(t *testing.T) {
	resolver := NewMockResolver()
	resolver.SetResponse("svc", []string{"10.0.0.1"})

	cfg := DiscoveryConfig{
		HeadlessService: "svc:8428",
		RefreshInterval: 50 * time.Millisecond,
		Timeout:         1 * time.Second,
	}

	d := NewDiscoveryWithResolver(cfg, nil, resolver)

	ctx, cancel := context.WithCancel(context.Background())
	d.Start(ctx)

	// Wait for a few refresh cycles
	time.Sleep(200 * time.Millisecond)
	cancel()
	d.Stop()

	// Should have called resolver multiple times
	callCount := resolver.GetCallCount()
	if callCount < 2 {
		t.Errorf("expected at least 2 refresh calls, got %d", callCount)
	}
}

func TestDiscovery_Start_ImmediateRefresh(t *testing.T) {
	resolver := NewMockResolver()
	resolver.SetResponse("svc", []string{"10.0.0.1"})

	callCount := int32(0)
	onChange := func(endpoints []string) {
		atomic.AddInt32(&callCount, 1)
	}

	cfg := DiscoveryConfig{
		HeadlessService: "svc:8428",
		RefreshInterval: 1 * time.Hour, // Very long interval
		Timeout:         1 * time.Second,
	}

	d := NewDiscoveryWithResolver(cfg, onChange, resolver)

	ctx, cancel := context.WithCancel(context.Background())
	d.Start(ctx)

	// Wait a bit for initial refresh
	time.Sleep(100 * time.Millisecond)
	cancel()
	d.Stop()

	// Should have called onChange immediately
	if atomic.LoadInt32(&callCount) != 1 {
		t.Errorf("expected 1 onChange call from initial refresh, got %d", callCount)
	}
}

func TestDiscovery_Stop(t *testing.T) {
	resolver := NewMockResolver()
	resolver.SetResponse("svc", []string{"10.0.0.1"})

	cfg := DiscoveryConfig{
		HeadlessService: "svc:8428",
		RefreshInterval: 10 * time.Millisecond,
		Timeout:         1 * time.Second,
	}

	d := NewDiscoveryWithResolver(cfg, nil, resolver)

	ctx := context.Background()
	d.Start(ctx)

	// Stop should not block
	done := make(chan struct{})
	go func() {
		d.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(2 * time.Second):
		t.Error("Stop took too long")
	}
}

func TestDiscovery_Stop_Idempotent(t *testing.T) {
	resolver := NewMockResolver()
	resolver.SetResponse("svc", []string{"10.0.0.1"})

	cfg := DiscoveryConfig{
		HeadlessService: "svc:8428",
		RefreshInterval: 10 * time.Millisecond,
		Timeout:         1 * time.Second,
	}

	d := NewDiscoveryWithResolver(cfg, nil, resolver)
	d.Start(context.Background())

	// Multiple stops should not panic
	d.Stop()
	d.Stop()
	d.Stop()
}

func TestDiscovery_IPv6Endpoints(t *testing.T) {
	resolver := NewMockResolver()
	resolver.SetResponse("svc", []string{"2001:db8::1", "2001:db8::2"})

	var received []string
	var mu sync.Mutex
	onChange := func(endpoints []string) {
		mu.Lock()
		received = endpoints
		mu.Unlock()
	}

	cfg := DiscoveryConfig{
		HeadlessService: "svc:8428",
		Timeout:         1 * time.Second,
	}

	d := NewDiscoveryWithResolver(cfg, onChange, resolver)
	d.ForceRefresh(context.Background())
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// IPv6 addresses should be bracketed
	if len(received) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(received))
	}
	if received[0] != "[2001:db8::1]:8428" {
		t.Errorf("expected [2001:db8::1]:8428, got %s", received[0])
	}
}

func TestDiscovery_NoPortInHostname(t *testing.T) {
	resolver := NewMockResolver()
	resolver.SetResponse("svc.namespace.svc.cluster.local", []string{"10.0.0.1"})

	var received []string
	var mu sync.Mutex
	onChange := func(endpoints []string) {
		mu.Lock()
		received = endpoints
		mu.Unlock()
	}

	cfg := DiscoveryConfig{
		HeadlessService: "svc.namespace.svc.cluster.local",
		Timeout:         1 * time.Second,
	}

	d := NewDiscoveryWithResolver(cfg, onChange, resolver)
	d.ForceRefresh(context.Background())
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// No port in config, so no port in result
	if len(received) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(received))
	}
	if received[0] != "10.0.0.1" {
		t.Errorf("expected 10.0.0.1, got %s", received[0])
	}
}

func TestDiscovery_PortInHostname(t *testing.T) {
	resolver := NewMockResolver()
	resolver.SetResponse("svc", []string{"10.0.0.1"})

	var received []string
	var mu sync.Mutex
	onChange := func(endpoints []string) {
		mu.Lock()
		received = endpoints
		mu.Unlock()
	}

	cfg := DiscoveryConfig{
		HeadlessService: "svc:9090",
		Timeout:         1 * time.Second,
	}

	d := NewDiscoveryWithResolver(cfg, onChange, resolver)
	d.ForceRefresh(context.Background())
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	if len(received) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(received))
	}
	if received[0] != "10.0.0.1:9090" {
		t.Errorf("expected 10.0.0.1:9090, got %s", received[0])
	}
}

// Helper tests

func TestEndpointsEqual(t *testing.T) {
	tests := []struct {
		a        []string
		b        []string
		expected bool
	}{
		{nil, nil, true},
		{[]string{}, []string{}, true},
		{[]string{"a"}, []string{"a"}, true},
		{[]string{"a", "b"}, []string{"a", "b"}, true},
		{[]string{"a"}, []string{"b"}, false},
		{[]string{"a", "b"}, []string{"a"}, false},
		{[]string{"a"}, []string{"a", "b"}, false},
	}

	for _, tc := range tests {
		result := endpointsEqual(tc.a, tc.b)
		if result != tc.expected {
			t.Errorf("endpointsEqual(%v, %v) = %v, expected %v", tc.a, tc.b, result, tc.expected)
		}
	}
}

func TestEndpointsDiff(t *testing.T) {
	added, removed := endpointsDiff(
		[]string{"a", "b", "c"},
		[]string{"b", "c", "d"},
	)

	if len(added) != 1 || added[0] != "d" {
		t.Errorf("expected added=[d], got %v", added)
	}
	if len(removed) != 1 || removed[0] != "a" {
		t.Errorf("expected removed=[a], got %v", removed)
	}
}
