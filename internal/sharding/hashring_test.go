package sharding

import (
	"fmt"
	"math"
	"sync"
	"testing"
)

func TestHashRing_NewHashRing(t *testing.T) {
	ring := NewHashRing(100)
	if ring == nil {
		t.Fatal("expected non-nil ring")
	}
	if ring.virtualNodes != 100 {
		t.Errorf("expected virtualNodes=100, got %d", ring.virtualNodes)
	}
	if ring.Size() != 0 {
		t.Errorf("expected empty ring, got size %d", ring.Size())
	}
}

func TestHashRing_NewHashRing_ZeroVirtualNodes(t *testing.T) {
	ring := NewHashRing(0)
	if ring.virtualNodes != DefaultVirtualNodes {
		t.Errorf("expected default virtualNodes=%d, got %d", DefaultVirtualNodes, ring.virtualNodes)
	}
}

func TestHashRing_NewHashRing_NegativeVirtualNodes(t *testing.T) {
	ring := NewHashRing(-5)
	if ring.virtualNodes != DefaultVirtualNodes {
		t.Errorf("expected default virtualNodes=%d, got %d", DefaultVirtualNodes, ring.virtualNodes)
	}
}

func TestHashRing_UpdateEndpoints(t *testing.T) {
	ring := NewHashRing(10)
	endpoints := []string{"10.0.0.1:8428", "10.0.0.2:8428", "10.0.0.3:8428"}

	ring.UpdateEndpoints(endpoints)

	if ring.Size() != 3 {
		t.Errorf("expected size=3, got %d", ring.Size())
	}

	// Verify ring is populated
	if len(ring.ring) == 0 {
		t.Error("expected non-empty ring")
	}

	// Ring should have at most virtualNodes * endpoints entries
	// (may be less due to hash collisions)
	maxExpected := 10 * 3
	if len(ring.ring) > maxExpected {
		t.Errorf("ring too large: %d > %d", len(ring.ring), maxExpected)
	}
}

func TestHashRing_UpdateEndpoints_Empty(t *testing.T) {
	ring := NewHashRing(10)
	ring.UpdateEndpoints([]string{"10.0.0.1:8428"})

	// Now clear
	ring.UpdateEndpoints([]string{})

	if ring.Size() != 0 {
		t.Errorf("expected size=0, got %d", ring.Size())
	}
	if len(ring.ring) != 0 {
		t.Errorf("expected empty ring, got %d entries", len(ring.ring))
	}
}

func TestHashRing_UpdateEndpoints_Replace(t *testing.T) {
	ring := NewHashRing(10)

	ring.UpdateEndpoints([]string{"10.0.0.1:8428", "10.0.0.2:8428"})
	if ring.Size() != 2 {
		t.Errorf("expected size=2, got %d", ring.Size())
	}

	ring.UpdateEndpoints([]string{"10.0.0.3:8428", "10.0.0.4:8428", "10.0.0.5:8428"})
	if ring.Size() != 3 {
		t.Errorf("expected size=3, got %d", ring.Size())
	}

	endpoints := ring.GetEndpoints()
	expected := []string{"10.0.0.3:8428", "10.0.0.4:8428", "10.0.0.5:8428"}
	if len(endpoints) != len(expected) {
		t.Errorf("expected %d endpoints, got %d", len(expected), len(endpoints))
	}
}

func TestHashRing_GetEndpoint(t *testing.T) {
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"10.0.0.1:8428", "10.0.0.2:8428", "10.0.0.3:8428"})

	// Test that we get an endpoint for any key
	endpoint := ring.GetEndpoint("test_metric|service=api")
	if endpoint == "" {
		t.Error("expected non-empty endpoint")
	}

	// Verify the endpoint is one of our endpoints
	validEndpoints := map[string]bool{
		"10.0.0.1:8428": true,
		"10.0.0.2:8428": true,
		"10.0.0.3:8428": true,
	}
	if !validEndpoints[endpoint] {
		t.Errorf("unexpected endpoint: %s", endpoint)
	}
}

func TestHashRing_GetEndpoint_EmptyRing(t *testing.T) {
	ring := NewHashRing(100)

	endpoint := ring.GetEndpoint("test_key")
	if endpoint != "" {
		t.Errorf("expected empty endpoint for empty ring, got %s", endpoint)
	}
}

func TestHashRing_GetEndpoint_SingleEndpoint(t *testing.T) {
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"10.0.0.1:8428"})

	// All keys should go to the single endpoint
	keys := []string{"key1", "key2", "key3", "metric|service=api", "another|env=prod"}
	for _, key := range keys {
		endpoint := ring.GetEndpoint(key)
		if endpoint != "10.0.0.1:8428" {
			t.Errorf("expected 10.0.0.1:8428 for key %s, got %s", key, endpoint)
		}
	}
}

func TestHashRing_Consistency(t *testing.T) {
	ring := NewHashRing(150)
	ring.UpdateEndpoints([]string{"10.0.0.1:8428", "10.0.0.2:8428", "10.0.0.3:8428"})

	// Same key should always return same endpoint
	key := "http_requests_total|service=api|env=prod"
	endpoint1 := ring.GetEndpoint(key)
	endpoint2 := ring.GetEndpoint(key)
	endpoint3 := ring.GetEndpoint(key)

	if endpoint1 != endpoint2 || endpoint2 != endpoint3 {
		t.Errorf("inconsistent endpoints: %s, %s, %s", endpoint1, endpoint2, endpoint3)
	}
}

func TestHashRing_Deterministic(t *testing.T) {
	// Two rings with same endpoints should give same results
	ring1 := NewHashRing(100)
	ring2 := NewHashRing(100)

	endpoints := []string{"10.0.0.1:8428", "10.0.0.2:8428", "10.0.0.3:8428"}
	ring1.UpdateEndpoints(endpoints)
	ring2.UpdateEndpoints(endpoints)

	keys := []string{"key1", "key2", "key3", "metric|label=value"}
	for _, key := range keys {
		ep1 := ring1.GetEndpoint(key)
		ep2 := ring2.GetEndpoint(key)
		if ep1 != ep2 {
			t.Errorf("non-deterministic for key %s: %s vs %s", key, ep1, ep2)
		}
	}
}

func TestHashRing_Distribution(t *testing.T) {
	ring := NewHashRing(150)
	endpoints := []string{"10.0.0.1:8428", "10.0.0.2:8428", "10.0.0.3:8428"}
	ring.UpdateEndpoints(endpoints)

	// Count distribution across 10000 keys
	counts := make(map[string]int)
	numKeys := 10000
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("metric_%d|service=api", i)
		endpoint := ring.GetEndpoint(key)
		counts[endpoint]++
	}

	// Check that distribution is reasonably even
	// With 3 endpoints, each should get ~33% of keys
	// Allow 20% deviation
	expectedPerEndpoint := numKeys / len(endpoints)
	tolerance := float64(expectedPerEndpoint) * 0.20

	for endpoint, count := range counts {
		deviation := math.Abs(float64(count) - float64(expectedPerEndpoint))
		if deviation > tolerance {
			t.Errorf("uneven distribution for %s: got %d, expected ~%d (deviation %.1f%%)",
				endpoint, count, expectedPerEndpoint, deviation/float64(expectedPerEndpoint)*100)
		}
	}
}

func TestHashRing_MinimalRehash(t *testing.T) {
	ring := NewHashRing(150)
	endpoints := []string{"10.0.0.1:8428", "10.0.0.2:8428", "10.0.0.3:8428"}
	ring.UpdateEndpoints(endpoints)

	// Record initial assignments
	numKeys := 10000
	initialAssignments := make(map[string]string)
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("metric_%d|service=api", i)
		initialAssignments[key] = ring.GetEndpoint(key)
	}

	// Add a new endpoint
	newEndpoints := []string{"10.0.0.1:8428", "10.0.0.2:8428", "10.0.0.3:8428", "10.0.0.4:8428"}
	ring.UpdateEndpoints(newEndpoints)

	// Count how many keys changed
	changed := 0
	for key, oldEndpoint := range initialAssignments {
		newEndpoint := ring.GetEndpoint(key)
		if newEndpoint != oldEndpoint {
			changed++
		}
	}

	// With consistent hashing, adding 1 endpoint should move ~1/n keys
	// where n is the new number of endpoints
	// Allow some tolerance for hash distribution variance
	expectedChanges := numKeys / len(newEndpoints)
	maxAllowedChanges := int(float64(expectedChanges) * 2.0) // 2x tolerance

	if changed > maxAllowedChanges {
		t.Errorf("too many keys changed: %d > %d (expected ~%d)",
			changed, maxAllowedChanges, expectedChanges)
	}
}

func TestHashRing_GetEndpoints(t *testing.T) {
	ring := NewHashRing(10)
	endpoints := []string{"10.0.0.3:8428", "10.0.0.1:8428", "10.0.0.2:8428"}
	ring.UpdateEndpoints(endpoints)

	result := ring.GetEndpoints()

	// Should be sorted
	expected := []string{"10.0.0.1:8428", "10.0.0.2:8428", "10.0.0.3:8428"}
	if len(result) != len(expected) {
		t.Fatalf("expected %d endpoints, got %d", len(expected), len(result))
	}
	for i, ep := range expected {
		if result[i] != ep {
			t.Errorf("endpoint %d: expected %s, got %s", i, ep, result[i])
		}
	}
}

func TestHashRing_GetEndpoints_Copy(t *testing.T) {
	ring := NewHashRing(10)
	ring.UpdateEndpoints([]string{"10.0.0.1:8428", "10.0.0.2:8428"})

	result := ring.GetEndpoints()
	result[0] = "modified"

	// Original should not be modified
	original := ring.GetEndpoints()
	if original[0] == "modified" {
		t.Error("GetEndpoints should return a copy")
	}
}

func TestHashRing_Size(t *testing.T) {
	ring := NewHashRing(10)

	if ring.Size() != 0 {
		t.Errorf("expected size=0, got %d", ring.Size())
	}

	ring.UpdateEndpoints([]string{"ep1", "ep2", "ep3"})
	if ring.Size() != 3 {
		t.Errorf("expected size=3, got %d", ring.Size())
	}

	ring.UpdateEndpoints([]string{"ep1"})
	if ring.Size() != 1 {
		t.Errorf("expected size=1, got %d", ring.Size())
	}
}

func TestHashRing_IsEmpty(t *testing.T) {
	ring := NewHashRing(10)

	if !ring.IsEmpty() {
		t.Error("expected empty ring")
	}

	ring.UpdateEndpoints([]string{"ep1"})
	if ring.IsEmpty() {
		t.Error("expected non-empty ring")
	}

	ring.UpdateEndpoints([]string{})
	if !ring.IsEmpty() {
		t.Error("expected empty ring after clearing")
	}
}

func TestHashRing_ConcurrentAccess(t *testing.T) {
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"10.0.0.1:8428", "10.0.0.2:8428", "10.0.0.3:8428"})

	var wg sync.WaitGroup
	numGoroutines := 100
	numIterations := 1000

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numIterations; j++ {
				key := fmt.Sprintf("metric_%d_%d", id, j)
				endpoint := ring.GetEndpoint(key)
				if endpoint == "" {
					t.Errorf("got empty endpoint for key %s", key)
				}
			}
		}(i)
	}

	wg.Wait()
}

func TestHashRing_ConcurrentUpdate(t *testing.T) {
	ring := NewHashRing(100)

	var wg sync.WaitGroup
	numGoroutines := 10

	// Mix of readers and writers
	for i := 0; i < numGoroutines; i++ {
		wg.Add(2)

		// Writer
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				endpoints := []string{
					fmt.Sprintf("10.0.0.%d:8428", id%5+1),
					fmt.Sprintf("10.0.0.%d:8428", (id+1)%5+1),
				}
				ring.UpdateEndpoints(endpoints)
			}
		}(i)

		// Reader
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				ring.GetEndpoint(fmt.Sprintf("key_%d_%d", id, j))
				ring.GetEndpoints()
				ring.Size()
			}
		}(i)
	}

	wg.Wait()
}

func TestHashRing_LongEndpointNames(t *testing.T) {
	ring := NewHashRing(50)

	// Very long endpoint names
	longEndpoint := "this-is-a-very-long-service-name.namespace.svc.cluster.local:8428"
	endpoints := []string{longEndpoint, "short:8428"}
	ring.UpdateEndpoints(endpoints)

	endpoint := ring.GetEndpoint("test_key")
	if endpoint == "" {
		t.Error("expected non-empty endpoint")
	}
}

func TestHashRing_SpecialCharacters(t *testing.T) {
	ring := NewHashRing(50)

	endpoints := []string{
		"svc-with-dashes.ns.svc:8428",
		"svc_with_underscores.ns.svc:8428",
		"192.168.1.100:8428",
	}
	ring.UpdateEndpoints(endpoints)

	// Keys with special characters
	keys := []string{
		"metric|label=value with spaces",
		"metric|label=value&special=chars",
		"metric|label=value\"quotes\"",
		"metric|label=日本語",
	}

	for _, key := range keys {
		endpoint := ring.GetEndpoint(key)
		if endpoint == "" {
			t.Errorf("expected non-empty endpoint for key: %s", key)
		}
	}
}

func TestHashRing_IPv6Endpoints(t *testing.T) {
	ring := NewHashRing(50)

	endpoints := []string{
		"[::1]:8428",
		"[2001:db8::1]:8428",
		"[fe80::1%eth0]:8428",
	}
	ring.UpdateEndpoints(endpoints)

	if ring.Size() != 3 {
		t.Errorf("expected size=3, got %d", ring.Size())
	}

	endpoint := ring.GetEndpoint("test_key")
	validEndpoints := map[string]bool{
		"[::1]:8428":          true,
		"[2001:db8::1]:8428":  true,
		"[fe80::1%eth0]:8428": true,
	}
	if !validEndpoints[endpoint] {
		t.Errorf("unexpected endpoint: %s", endpoint)
	}
}

func TestHashRing_VirtualNodes(t *testing.T) {
	// Test that more virtual nodes leads to better distribution
	testCases := []struct {
		virtualNodes int
		endpoints    int
	}{
		{10, 3},
		{50, 3},
		{150, 3},
		{300, 3},
	}

	numKeys := 10000

	for _, tc := range testCases {
		ring := NewHashRing(tc.virtualNodes)

		endpoints := make([]string, tc.endpoints)
		for i := 0; i < tc.endpoints; i++ {
			endpoints[i] = fmt.Sprintf("10.0.0.%d:8428", i+1)
		}
		ring.UpdateEndpoints(endpoints)

		counts := make(map[string]int)
		for i := 0; i < numKeys; i++ {
			key := fmt.Sprintf("metric_%d", i)
			counts[ring.GetEndpoint(key)]++
		}

		// Calculate standard deviation
		expectedPerEndpoint := float64(numKeys) / float64(tc.endpoints)
		var sumSquaredDiff float64
		for _, count := range counts {
			diff := float64(count) - expectedPerEndpoint
			sumSquaredDiff += diff * diff
		}
		stdDev := math.Sqrt(sumSquaredDiff / float64(tc.endpoints))
		coeffOfVariation := stdDev / expectedPerEndpoint * 100

		t.Logf("virtualNodes=%d: coefficient of variation=%.2f%%", tc.virtualNodes, coeffOfVariation)
	}
}

// Benchmarks

func BenchmarkHashRing_GetEndpoint(b *testing.B) {
	ring := NewHashRing(150)
	ring.UpdateEndpoints([]string{"10.0.0.1:8428", "10.0.0.2:8428", "10.0.0.3:8428"})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ring.GetEndpoint("http_requests_total|service=api|env=prod")
	}
}

func BenchmarkHashRing_GetEndpoint_LargeRing(b *testing.B) {
	ring := NewHashRing(150)

	// 100 endpoints
	endpoints := make([]string, 100)
	for i := 0; i < 100; i++ {
		endpoints[i] = fmt.Sprintf("10.0.%d.%d:8428", i/256, i%256)
	}
	ring.UpdateEndpoints(endpoints)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ring.GetEndpoint("http_requests_total|service=api|env=prod")
	}
}

func BenchmarkHashRing_UpdateEndpoints(b *testing.B) {
	ring := NewHashRing(150)
	endpoints := []string{"10.0.0.1:8428", "10.0.0.2:8428", "10.0.0.3:8428"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ring.UpdateEndpoints(endpoints)
	}
}

func BenchmarkHashRing_UpdateEndpoints_Large(b *testing.B) {
	ring := NewHashRing(150)

	endpoints := make([]string, 100)
	for i := 0; i < 100; i++ {
		endpoints[i] = fmt.Sprintf("10.0.%d.%d:8428", i/256, i%256)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ring.UpdateEndpoints(endpoints)
	}
}
