package sharding

import (
	"hash/maphash"
	"sort"
	"strconv"
	"sync"
)

// hashSeed is a fixed seed for the lifetime of this process.
// The ring is rebuilt from the endpoint list on restart, so
// intra-process determinism is sufficient.
var hashSeed = maphash.MakeSeed()

// DefaultVirtualNodes is the default number of virtual nodes per endpoint.
const DefaultVirtualNodes = 150

// HashRing implements a consistent hash ring with virtual nodes for even distribution.
type HashRing struct {
	mu           sync.RWMutex
	ring         []uint64          // Sorted hash values
	nodes        map[uint64]string // Hash -> endpoint mapping
	endpoints    []string          // Current endpoints (sorted)
	virtualNodes int               // Virtual nodes per endpoint
}

// NewHashRing creates a new consistent hash ring.
// If virtualNodes is 0 or negative, DefaultVirtualNodes (150) is used.
func NewHashRing(virtualNodes int) *HashRing {
	if virtualNodes <= 0 {
		virtualNodes = DefaultVirtualNodes
	}
	return &HashRing{
		ring:         make([]uint64, 0),
		nodes:        make(map[uint64]string),
		endpoints:    make([]string, 0),
		virtualNodes: virtualNodes,
	}
}

// UpdateEndpoints atomically updates the endpoint list and rebuilds the ring.
func (h *HashRing) UpdateEndpoints(endpoints []string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Sort endpoints for deterministic ordering
	sorted := make([]string, len(endpoints))
	copy(sorted, endpoints)
	sort.Strings(sorted)

	// Clear existing ring
	h.ring = make([]uint64, 0, len(sorted)*h.virtualNodes)
	h.nodes = make(map[uint64]string, len(sorted)*h.virtualNodes)
	h.endpoints = sorted

	// Add virtual nodes for each endpoint
	for _, endpoint := range sorted {
		for i := 0; i < h.virtualNodes; i++ {
			// Hash format: "endpoint#i"
			key := endpoint + "#" + strconv.Itoa(i)
			hash := maphash.String(hashSeed, key)

			// Handle hash collisions by skipping duplicates
			if _, exists := h.nodes[hash]; !exists {
				h.ring = append(h.ring, hash)
				h.nodes[hash] = endpoint
			}
		}
	}

	// Sort the ring
	sort.Slice(h.ring, func(i, j int) bool {
		return h.ring[i] < h.ring[j]
	})
}

// GetEndpoint returns the endpoint responsible for the given key.
// Returns empty string if the ring is empty.
func (h *HashRing) GetEndpoint(key string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if len(h.ring) == 0 {
		return ""
	}

	hash := maphash.String(hashSeed, key)

	// Binary search for the first ring position >= hash
	idx := sort.Search(len(h.ring), func(i int) bool {
		return h.ring[i] >= hash
	})

	// Wrap around if necessary
	if idx >= len(h.ring) {
		idx = 0
	}

	return h.nodes[h.ring[idx]]
}

// GetEndpoints returns a copy of the current endpoint list.
func (h *HashRing) GetEndpoints() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make([]string, len(h.endpoints))
	copy(result, h.endpoints)
	return result
}

// Size returns the number of real endpoints in the ring.
func (h *HashRing) Size() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.endpoints)
}

// IsEmpty returns true if the ring has no endpoints.
func (h *HashRing) IsEmpty() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.endpoints) == 0
}
