package sharding

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// --- Race condition tests ---

func TestRace_HashRing_ConcurrentGetEndpoint(t *testing.T) {
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"host1:9090", "host2:9090", "host3:9090"})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				ring.GetEndpoint(fmt.Sprintf("metric_%d_%d", id, j))
			}
		}(i)
	}

	wg.Wait()
}

func TestRace_HashRing_UpdateWhileReading(t *testing.T) {
	ring := NewHashRing(100)
	ring.UpdateEndpoints([]string{"host1:9090"})

	var wg sync.WaitGroup

	// Readers
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				ring.GetEndpoint(fmt.Sprintf("key-%d-%d", id, j))
				ring.GetEndpoints()
				ring.Size()
				ring.IsEmpty()
			}
		}(i)
	}

	// Writers
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				endpoints := make([]string, 0, j%5+1)
				for k := 0; k <= j%5; k++ {
					endpoints = append(endpoints, fmt.Sprintf("host%d-%d:9090", id, k))
				}
				ring.UpdateEndpoints(endpoints)
				runtime.Gosched()
			}
		}(i)
	}

	wg.Wait()
}

func TestRace_HashRing_ConcurrentUpdates(t *testing.T) {
	ring := NewHashRing(50)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				endpoints := []string{
					fmt.Sprintf("host-%d-a:9090", id),
					fmt.Sprintf("host-%d-b:9090", id),
				}
				ring.UpdateEndpoints(endpoints)
				ring.GetEndpoint("test-key")
				ring.Size()
			}
		}(i)
	}

	wg.Wait()
}

func TestRace_HashRing_EmptyRingAccess(t *testing.T) {
	ring := NewHashRing(100)

	var wg sync.WaitGroup

	// Access empty ring
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				ring.GetEndpoint(fmt.Sprintf("key-%d", j))
				ring.IsEmpty()
				ring.Size()
			}
		}(i)
	}

	// Add endpoints mid-flight
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(time.Millisecond)
		ring.UpdateEndpoints([]string{"host1:9090", "host2:9090"})
	}()

	wg.Wait()
}

func TestRace_ShardKeyBuilder_Concurrent(t *testing.T) {
	// ShardKeyBuilder uses a non-thread-safe strings.Builder internally,
	// so each goroutine gets its own builder (matching production usage
	// where builders are per-goroutine or per-request).
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			builder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service", "env"}})
			for j := 0; j < 500; j++ {
				attrs := map[string]string{
					"service": fmt.Sprintf("svc-%d", id%4),
					"env":     "prod",
				}
				builder.BuildKey("metric_name", attrs)
			}
		}(i)
	}

	wg.Wait()
}

// --- Memory leak tests ---

func TestMemLeak_HashRing_UpdateCycles(t *testing.T) {
	ring := NewHashRing(100)

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	for i := 0; i < 1000; i++ {
		endpoints := make([]string, 0, 10)
		for j := 0; j < 10; j++ {
			endpoints = append(endpoints, fmt.Sprintf("host-%d-%d:9090", i, j))
		}
		ring.UpdateEndpoints(endpoints)
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("HashRing update cycles: heap_before=%dKB, heap_after=%dKB, size=%d",
		heapBefore/1024, heapAfter/1024, ring.Size())

	// Only the last update's endpoints should be live
	if heapAfter > heapBefore+20*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after 1000 update cycles",
			heapBefore/1024, heapAfter/1024)
	}
}

func TestMemLeak_HashRing_CreateDestroyCycles(t *testing.T) {
	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	for i := 0; i < 500; i++ {
		ring := NewHashRing(100)
		endpoints := make([]string, 20)
		for j := 0; j < 20; j++ {
			endpoints[j] = fmt.Sprintf("host-%d-%d:9090", i, j)
		}
		ring.UpdateEndpoints(endpoints)
		for j := 0; j < 100; j++ {
			ring.GetEndpoint(fmt.Sprintf("key-%d", j))
		}
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("HashRing create/destroy: heap_before=%dKB, heap_after=%dKB",
		heapBefore/1024, heapAfter/1024)

	if heapAfter > heapBefore+20*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after 500 create/destroy cycles",
			heapBefore/1024, heapAfter/1024)
	}
}
