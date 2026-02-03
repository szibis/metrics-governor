package limits

import (
	"container/list"
	"sync"
	"sync/atomic"
)

// ruleCache is a bounded LRU cache for metric-name-to-rule lookups.
type ruleCache struct {
	mu      sync.RWMutex
	entries map[string]*list.Element
	order   *list.List
	maxSize int

	// Observability counters
	hits      atomic.Int64
	misses    atomic.Int64
	evictions atomic.Int64
}

type ruleCacheEntry struct {
	key  string
	rule *Rule // nil = negative cache (no rule matches)
}

func newRuleCache(maxSize int) *ruleCache {
	if maxSize <= 0 {
		return nil
	}
	return &ruleCache{
		entries: make(map[string]*list.Element, maxSize),
		order:   list.New(),
		maxSize: maxSize,
	}
}

// Get retrieves a cached rule for a metric name.
// Returns (rule, true) on hit, (nil, false) on miss.
// A hit with nil rule means "no rule matches" (negative cache).
func (c *ruleCache) Get(metricName string) (*Rule, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.RLock()
	elem, ok := c.entries[metricName]
	c.mu.RUnlock()
	if !ok {
		c.misses.Add(1)
		return nil, false
	}
	// Move to front (promote)
	c.mu.Lock()
	c.order.MoveToFront(elem)
	c.mu.Unlock()
	c.hits.Add(1)
	entry := elem.Value.(*ruleCacheEntry)
	return entry.rule, true
}

// Put adds or updates a cache entry.
func (c *ruleCache) Put(metricName string, rule *Rule) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	// Update existing entry
	if elem, ok := c.entries[metricName]; ok {
		c.order.MoveToFront(elem)
		elem.Value.(*ruleCacheEntry).rule = rule
		return
	}

	// Evict if full
	for c.order.Len() >= c.maxSize {
		back := c.order.Back()
		if back == nil {
			break
		}
		entry := back.Value.(*ruleCacheEntry)
		delete(c.entries, entry.key)
		c.order.Remove(back)
		c.evictions.Add(1)
	}

	// Add new entry
	entry := &ruleCacheEntry{key: metricName, rule: rule}
	elem := c.order.PushFront(entry)
	c.entries[metricName] = elem
}

// Size returns current number of entries.
func (c *ruleCache) Size() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// NegativeEntries returns the count of cached "no match" entries.
func (c *ruleCache) NegativeEntries() int {
	if c == nil {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	count := 0
	for e := c.order.Front(); e != nil; e = e.Next() {
		if e.Value.(*ruleCacheEntry).rule == nil {
			count++
		}
	}
	return count
}

// ClearCache removes all entries.
func (c *ruleCache) ClearCache() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*list.Element, c.maxSize)
	c.order.Init()
}
