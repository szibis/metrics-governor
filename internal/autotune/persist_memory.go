package autotune

import (
	"context"
	"sync"
)

// MemoryPersister stores autotune changes in memory only.
// Changes are lost on restart. This is the default persister.
type MemoryPersister struct {
	mu      sync.Mutex
	history []ConfigChange
	maxSize int // max history entries to retain
}

// NewMemoryPersister creates an in-memory change persister.
func NewMemoryPersister(maxSize int) *MemoryPersister {
	if maxSize <= 0 {
		maxSize = 1000
	}
	return &MemoryPersister{
		maxSize: maxSize,
	}
}

// Persist stores changes in memory.
func (m *MemoryPersister) Persist(_ context.Context, changes []ConfigChange) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.history = append(m.history, changes...)
	// Trim if over max size.
	if len(m.history) > m.maxSize {
		m.history = m.history[len(m.history)-m.maxSize:]
	}
	return nil
}

// LoadHistory returns all stored changes.
func (m *MemoryPersister) LoadHistory(_ context.Context) ([]ConfigChange, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]ConfigChange, len(m.history))
	copy(result, m.history)
	return result, nil
}
