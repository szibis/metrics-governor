package autotune

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FilePersister writes autotune changes to a JSON file on disk.
// An external sidecar (e.g., git-sync) or GitOps controller can sync
// the file to version control.
type FilePersister struct {
	path string
	mu   sync.Mutex

	// Retry config (from safeguards).
	maxRetries       int
	retryBackoffBase time.Duration
}

// NewFilePersister creates a file-based change persister.
func NewFilePersister(path string, maxRetries int, retryBackoffBase time.Duration) *FilePersister {
	if maxRetries <= 0 {
		maxRetries = 3
	}
	if retryBackoffBase <= 0 {
		retryBackoffBase = time.Second
	}
	return &FilePersister{
		path:             path,
		maxRetries:       maxRetries,
		retryBackoffBase: retryBackoffBase,
	}
}

// Persist appends changes to the file. Uses write-to-temp + rename for atomicity.
func (fp *FilePersister) Persist(_ context.Context, changes []ConfigChange) error {
	fp.mu.Lock()
	defer fp.mu.Unlock()

	// Load existing history.
	existing, _ := fp.loadFromFile()
	existing = append(existing, changes...)

	// Marshal to JSON.
	data, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal changes: %w", err)
	}

	// Write with retries.
	var lastErr error
	for attempt := 0; attempt <= fp.maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(fp.retryBackoffBase * time.Duration(1<<(attempt-1)))
		}
		if err := fp.atomicWrite(data); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("persist failed after %d retries: %w", fp.maxRetries, lastErr)
}

// LoadHistory reads the change history from the file.
func (fp *FilePersister) LoadHistory(_ context.Context) ([]ConfigChange, error) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	return fp.loadFromFile()
}

// atomicWrite writes data to a temp file then renames to the target path.
func (fp *FilePersister) atomicWrite(data []byte) error {
	dir := filepath.Dir(fp.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".autotune-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpName, fp.path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename to target: %w", err)
	}

	return nil
}

// loadFromFile reads and parses the existing JSON file.
func (fp *FilePersister) loadFromFile() ([]ConfigChange, error) {
	data, err := os.ReadFile(fp.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read file: %w", err)
	}

	var changes []ConfigChange
	if err := json.Unmarshal(data, &changes); err != nil {
		return nil, fmt.Errorf("unmarshal file: %w", err)
	}
	return changes, nil
}
