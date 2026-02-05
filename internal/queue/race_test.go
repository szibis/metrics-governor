package queue

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// --- Race condition tests ---
// These test concurrent access patterns on SendQueue and FastQueue.
// Queue sizes are kept large enough to avoid eviction-induced lock contention,
// since these tests target data race detection, not eviction behavior.

func TestRace_SendQueue_ConcurrentPushPop(t *testing.T) {
	dir := t.TempDir()
	q, err := New(Config{
		Path:         dir,
		MaxSize:      5000,
		MaxBytes:     100 * 1024 * 1024,
		FullBehavior: DropOldest,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	var wg sync.WaitGroup

	// Pushers
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				data := []byte(fmt.Sprintf("push-%d-%d-payload", id, j))
				q.PushData(data)
			}
		}(i)
	}

	// Poppers
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				q.Pop()
				runtime.Gosched()
			}
		}()
	}

	wg.Wait()
}

func TestRace_SendQueue_ConcurrentPushWithLen(t *testing.T) {
	dir := t.TempDir()
	q, err := New(Config{
		Path:         dir,
		MaxSize:      5000,
		MaxBytes:     100 * 1024 * 1024,
		FullBehavior: DropOldest,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				q.PushData([]byte(fmt.Sprintf("data-%d-%d", id, j)))
				q.Len()
				q.Size()
			}
		}(i)
	}

	wg.Wait()
}

func TestRace_SendQueue_PushWithRetries(t *testing.T) {
	dir := t.TempDir()
	q, err := New(Config{
		Path:         dir,
		MaxSize:      5000,
		MaxBytes:     100 * 1024 * 1024,
		FullBehavior: DropOldest,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	// Push initial data
	for j := 0; j < 50; j++ {
		q.PushData([]byte(fmt.Sprintf("retry-data-%d", j)))
	}

	var wg sync.WaitGroup

	// Concurrent retry updates and pops
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				entry, err := q.Pop()
				if err == nil && entry != nil {
					q.UpdateRetries(entry.ID, entry.Retries+1)
				}
				q.PushData([]byte(fmt.Sprintf("new-data-%d-%d", id, j)))
			}
		}(i)
	}

	wg.Wait()
}

func TestRace_SendQueue_PeekConcurrent(t *testing.T) {
	dir := t.TempDir()
	q, err := New(Config{
		Path:         dir,
		MaxSize:      5000,
		MaxBytes:     100 * 1024 * 1024,
		FullBehavior: DropOldest,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	// Pre-fill
	for j := 0; j < 50; j++ {
		q.PushData([]byte(fmt.Sprintf("peek-data-%d", j)))
	}

	var wg sync.WaitGroup

	// Concurrent peek, pop, push
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				q.Peek()
				q.Pop()
				q.PushData([]byte(fmt.Sprintf("new-%d-%d", id, j)))
				q.Len()
			}
		}(i)
	}

	wg.Wait()
}

func TestRace_FastQueue_ConcurrentPushPop(t *testing.T) {
	dir := t.TempDir()
	fq, err := NewFastQueue(FastQueueConfig{
		Path:               dir,
		MaxSize:            5000,
		MaxBytes:           100 * 1024 * 1024,
		MaxInmemoryBlocks:  64,
		ChunkFileSize:      16 * 1024 * 1024,
		MetaSyncInterval:   500 * time.Millisecond,
		StaleFlushInterval: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer fq.Close()

	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				data := []byte(fmt.Sprintf("fq-push-%d-%d-data", id, j))
				fq.Push(data)
			}
		}(i)
	}

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				fq.Pop()
				runtime.Gosched()
			}
		}()
	}

	wg.Wait()
}

func TestRace_FastQueue_ConcurrentPushWithStats(t *testing.T) {
	dir := t.TempDir()
	fq, err := NewFastQueue(FastQueueConfig{
		Path:               dir,
		MaxSize:            5000,
		MaxBytes:           100 * 1024 * 1024,
		MaxInmemoryBlocks:  64,
		ChunkFileSize:      8 * 1024 * 1024,
		MetaSyncInterval:   500 * time.Millisecond,
		StaleFlushInterval: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer fq.Close()

	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				fq.Push([]byte(fmt.Sprintf("stat-data-%d-%d", id, j)))
				fq.Len()
				fq.Size()
			}
		}(i)
	}

	wg.Wait()
}

// --- Memory leak tests ---

func TestMemLeak_SendQueue_PushPopCycles(t *testing.T) {
	dir := t.TempDir()
	q, err := New(Config{
		Path:         dir,
		MaxSize:      200,
		MaxBytes:     10 * 1024 * 1024,
		FullBehavior: DropOldest,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	const cycles = 100
	const itemsPerCycle = 50

	for c := 0; c < cycles; c++ {
		for i := 0; i < itemsPerCycle; i++ {
			q.PushData([]byte(fmt.Sprintf("cycle-%d-item-%d-payload", c, i)))
		}
		for i := 0; i < itemsPerCycle; i++ {
			q.Pop()
		}
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("SendQueue push/pop cycles: heap_before=%dKB, heap_after=%dKB, len=%d",
		heapBefore/1024, heapAfter/1024, q.Len())

	if heapAfter > heapBefore+30*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after %d cycles",
			heapBefore/1024, heapAfter/1024, cycles)
	}
}

func TestMemLeak_FastQueue_PushPopCycles(t *testing.T) {
	dir := t.TempDir()
	fq, err := NewFastQueue(FastQueueConfig{
		Path:               dir,
		MaxSize:            200,
		MaxBytes:           10 * 1024 * 1024,
		MaxInmemoryBlocks:  32,
		ChunkFileSize:      1 * 1024 * 1024,
		MetaSyncInterval:   500 * time.Millisecond,
		StaleFlushInterval: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer fq.Close()

	runtime.GC()
	runtime.GC()

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	heapBefore := m.HeapInuse

	const cycles = 100
	const itemsPerCycle = 50

	for c := 0; c < cycles; c++ {
		for i := 0; i < itemsPerCycle; i++ {
			fq.Push([]byte(fmt.Sprintf("fq-cycle-%d-item-%d-payload", c, i)))
		}
		for i := 0; i < itemsPerCycle; i++ {
			fq.Pop()
		}
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(10 * time.Millisecond)

	runtime.ReadMemStats(&m)
	heapAfter := m.HeapInuse

	t.Logf("FastQueue push/pop cycles: heap_before=%dKB, heap_after=%dKB, len=%d",
		heapBefore/1024, heapAfter/1024, fq.Len())

	if heapAfter > heapBefore+30*1024*1024 {
		t.Errorf("Possible memory leak: heap grew from %dKB to %dKB after %d cycles",
			heapBefore/1024, heapAfter/1024, cycles)
	}
}
