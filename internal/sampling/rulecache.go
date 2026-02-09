package sampling

import (
	"container/list"
	"sync"
	"sync/atomic"
)

// processingRuleCache is a bounded LRU cache mapping metric names to the indices
// of processing rules whose name pattern matches. This avoids re-evaluating every
// rule's regex on every datapoint for the same metric name.
//
// For rules with input_labels, we still cache the name-match; the label check
// happens post-lookup. This means cached indices may include rules that ultimately
// don't match (label mismatch), but we skip the O(R) regex scan.
type processingRuleCache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	order   *list.List
	maxSize int

	// Observability counters.
	hits      atomic.Int64
	misses    atomic.Int64
	evictions atomic.Int64
}

type procCacheEntry struct {
	key     string
	indices []int // indices of rules whose name pattern matches this metric name
}

func newProcessingRuleCache(maxSize int) *processingRuleCache {
	if maxSize <= 0 {
		return nil
	}
	return &processingRuleCache{
		entries: make(map[string]*list.Element, maxSize),
		order:   list.New(),
		maxSize: maxSize,
	}
}

// Get retrieves cached rule indices for a metric name.
// Returns (indices, true) on hit, (nil, false) on miss.
func (c *processingRuleCache) Get(metricName string) ([]int, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	elem, ok := c.entries[metricName]
	if !ok {
		c.mu.Unlock()
		c.misses.Add(1)
		return nil, false
	}
	c.order.MoveToFront(elem)
	indices := elem.Value.(*procCacheEntry).indices
	c.mu.Unlock()
	c.hits.Add(1)
	return indices, true
}

// Put adds or updates a cache entry.
func (c *processingRuleCache) Put(metricName string, indices []int) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.entries[metricName]; ok {
		c.order.MoveToFront(elem)
		elem.Value.(*procCacheEntry).indices = indices
		return
	}

	// Evict LRU if full.
	for c.order.Len() >= c.maxSize {
		back := c.order.Back()
		if back == nil {
			break
		}
		entry := back.Value.(*procCacheEntry)
		delete(c.entries, entry.key)
		c.order.Remove(back)
		c.evictions.Add(1)
	}

	entry := &procCacheEntry{key: metricName, indices: indices}
	elem := c.order.PushFront(entry)
	c.entries[metricName] = elem
}

// Size returns current number of entries.
func (c *processingRuleCache) Size() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// ClearCache removes all entries (called on config reload).
func (c *processingRuleCache) ClearCache() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*list.Element, c.maxSize)
	c.order.Init()
}

// EstimatedMemoryBytes returns an estimate of cache memory usage.
func (c *processingRuleCache) EstimatedMemoryBytes() int64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	var total int64
	for e := c.order.Front(); e != nil; e = e.Next() {
		entry := e.Value.(*procCacheEntry)
		// ~200 bytes overhead (list.Element + map entry + struct + slice header) + key + indices
		total += 200 + int64(len(entry.key)) + int64(len(entry.indices)*8)
	}
	return total
}
