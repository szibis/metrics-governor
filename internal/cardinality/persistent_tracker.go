package cardinality

import (
	"time"
)

// PersistentTracker wraps BloomTracker with persistence support.
type PersistentTracker struct {
	*BloomTracker
	key         string
	hash        string
	store       *TrackerStore
	lastUpdated time.Time
	createdAt   time.Time
}

// Add tests membership and adds element if new.
// Returns true if the element was new (not seen before).
// Marks the tracker as dirty for persistence.
func (t *PersistentTracker) Add(key []byte) bool {
	isNew := t.BloomTracker.Add(key)
	if isNew {
		t.lastUpdated = time.Now()
		if t.store != nil {
			t.store.MarkDirty(t.key)
		}
	}
	return isNew
}

// Key returns the tracker's unique key.
func (t *PersistentTracker) Key() string {
	return t.key
}

// Hash returns the SHA256 hash used for the filename.
func (t *PersistentTracker) Hash() string {
	return t.hash
}

// LastUpdated returns when the tracker was last modified.
func (t *PersistentTracker) LastUpdated() time.Time {
	return t.lastUpdated
}

// CreatedAt returns when the tracker was created.
func (t *PersistentTracker) CreatedAt() time.Time {
	return t.createdAt
}
