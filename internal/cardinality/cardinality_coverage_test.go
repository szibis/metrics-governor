package cardinality

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// --- 1. ExactTracker.MemoryUsage ---

func TestExactTracker_MemoryUsage(t *testing.T) {
	tracker := NewExactTracker()

	// Empty tracker should have zero memory usage.
	if mem := tracker.MemoryUsage(); mem != 0 {
		t.Errorf("expected 0 memory for empty tracker, got %d", mem)
	}

	// Add some items and verify memory estimate grows.
	tracker.Add([]byte("key1"))
	tracker.Add([]byte("key2"))
	tracker.Add([]byte("key3"))

	expected := uint64(3) * 75
	if mem := tracker.MemoryUsage(); mem != expected {
		t.Errorf("expected memory %d for 3 items, got %d", expected, mem)
	}

	// Duplicates should not increase memory.
	tracker.Add([]byte("key1"))
	if mem := tracker.MemoryUsage(); mem != expected {
		t.Errorf("expected memory %d after duplicate add, got %d", expected, mem)
	}
}

// --- 2-5. PersistentTracker getters: Key, Hash, LastUpdated, CreatedAt ---

func TestPersistentTracker_Getters(t *testing.T) {
	dir := t.TempDir()
	store, err := NewTrackerStore(PersistenceConfig{
		Enabled:          true,
		Path:             dir,
		SaveInterval:     time.Hour,
		StateTTL:         time.Hour,
		CleanupInterval:  time.Hour,
		MaxSize:          100 * 1024 * 1024,
		MaxMemory:        50 * 1024 * 1024,
		Compression:      true,
		CompressionLevel: 1,
	}, DefaultConfig())
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	before := time.Now()
	tracker := store.GetOrCreate("test:getter:key")
	after := time.Now()

	// Key()
	if tracker.Key() != "test:getter:key" {
		t.Errorf("Key() = %q, want %q", tracker.Key(), "test:getter:key")
	}

	// Hash() - should be a non-empty hex string matching hashKey.
	expectedHash := hashKey("test:getter:key")
	if tracker.Hash() != expectedHash {
		t.Errorf("Hash() = %q, want %q", tracker.Hash(), expectedHash)
	}

	// LastUpdated() - should be between before and after.
	lu := tracker.LastUpdated()
	if lu.Before(before) || lu.After(after) {
		t.Errorf("LastUpdated() = %v, want between %v and %v", lu, before, after)
	}

	// CreatedAt() - should be between before and after.
	ca := tracker.CreatedAt()
	if ca.Before(before) || ca.After(after) {
		t.Errorf("CreatedAt() = %v, want between %v and %v", ca, before, after)
	}
}

// --- 6. DefaultPersistenceConfig ---

func TestDefaultPersistenceConfig(t *testing.T) {
	cfg := DefaultPersistenceConfig()

	if cfg.Enabled {
		t.Error("default Enabled should be false")
	}
	if cfg.Path != "./bloom-state" {
		t.Errorf("default Path = %q, want %q", cfg.Path, "./bloom-state")
	}
	if cfg.SaveInterval != 30*time.Second {
		t.Errorf("default SaveInterval = %v, want %v", cfg.SaveInterval, 30*time.Second)
	}
	if cfg.StateTTL != time.Hour {
		t.Errorf("default StateTTL = %v, want %v", cfg.StateTTL, time.Hour)
	}
	if cfg.CleanupInterval != 5*time.Minute {
		t.Errorf("default CleanupInterval = %v, want %v", cfg.CleanupInterval, 5*time.Minute)
	}
	if cfg.MaxSize != 500*1024*1024 {
		t.Errorf("default MaxSize = %d, want %d", cfg.MaxSize, 500*1024*1024)
	}
	if cfg.MaxMemory != 256*1024*1024 {
		t.Errorf("default MaxMemory = %d, want %d", cfg.MaxMemory, 256*1024*1024)
	}
	if !cfg.Compression {
		t.Error("default Compression should be true")
	}
	if cfg.CompressionLevel != 1 {
		t.Errorf("default CompressionLevel = %d, want %d", cfg.CompressionLevel, 1)
	}
}

// --- 7. DiskUsage ---

func TestTrackerStore_DiskUsage(t *testing.T) {
	dir := t.TempDir()
	store := newTestStore(t, dir)

	// Initially zero.
	if du := store.DiskUsage(); du != 0 {
		t.Errorf("initial DiskUsage() = %d, want 0", du)
	}

	// Create a tracker, save, then check disk usage increased.
	tracker := store.GetOrCreate("test:diskusage")
	tracker.Add([]byte("item1"))
	if err := store.SaveAll(); err != nil {
		t.Fatalf("SaveAll failed: %v", err)
	}

	if du := store.DiskUsage(); du <= 0 {
		t.Errorf("DiskUsage() after save = %d, want > 0", du)
	}
}

// --- 8. CheckMemoryPressure ---

func TestCheckMemoryPressure_NoLimit(t *testing.T) {
	dir := t.TempDir()
	cfg := testPersistenceConfig(dir)
	cfg.MaxMemory = 0 // No memory limit.

	store, err := NewTrackerStore(cfg, DefaultConfig())
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Should return immediately without panic.
	store.CheckMemoryPressure()
}

func TestCheckMemoryPressure_EvictionThreshold(t *testing.T) {
	dir := t.TempDir()
	cfg := testPersistenceConfig(dir)
	bloomCfg := Config{
		Mode:              ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	store, err := NewTrackerStore(cfg, bloomCfg)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Create some trackers.
	store.GetOrCreate("test:pressure:1")
	store.GetOrCreate("test:pressure:2")

	// Artificially set memory high enough to trigger eviction threshold (>= 95%).
	store.mu.Lock()
	store.memoryUsage = int64(float64(cfg.MaxMemory) * 0.96)
	store.mu.Unlock()

	store.CheckMemoryPressure()

	// Some trackers should have been evicted or memory reduced.
	// At least verify no panic and the function completed.
	if store.MemoryUsage() > cfg.MaxMemory {
		t.Errorf("memory usage %d should be reduced after eviction, limit is %d", store.MemoryUsage(), cfg.MaxMemory)
	}
}

func TestCheckMemoryPressure_WarningThreshold(t *testing.T) {
	dir := t.TempDir()
	cfg := testPersistenceConfig(dir)
	cfg.StateTTL = 1 * time.Millisecond // Very short TTL so warning-level cleanup removes trackers.
	bloomCfg := Config{
		Mode:              ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	store, err := NewTrackerStore(cfg, bloomCfg)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Create a tracker and let it become half-stale (age > TTL/2).
	store.GetOrCreate("test:pressure:warn")

	// Wait so the tracker becomes stale enough for aggressive cleanup.
	time.Sleep(5 * time.Millisecond)

	// Set memory to warning threshold (>= 80% but < 95%).
	store.mu.Lock()
	store.memoryUsage = int64(float64(cfg.MaxMemory) * 0.85)
	store.mu.Unlock()

	store.CheckMemoryPressure()
	// The warning path triggers aggressive cleanup of half-stale trackers.
}

// --- 9. findOldestTrackerUnlocked ---

func TestFindOldestTrackerUnlocked(t *testing.T) {
	dir := t.TempDir()
	store := newTestStore(t, dir)

	// Empty store should return "".
	store.mu.Lock()
	oldest := store.findOldestTrackerUnlocked()
	store.mu.Unlock()
	if oldest != "" {
		t.Errorf("expected empty string for empty store, got %q", oldest)
	}

	// Create trackers with different timestamps.
	store.GetOrCreate("test:oldest:new")
	time.Sleep(10 * time.Millisecond)
	store.GetOrCreate("test:oldest:newer")

	// Manually set timestamps to control ordering.
	store.mu.Lock()
	oldTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	newTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	store.index.Trackers["test:oldest:new"].LastUpdated = oldTime
	store.index.Trackers["test:oldest:newer"].LastUpdated = newTime
	oldest = store.findOldestTrackerUnlocked()
	store.mu.Unlock()

	if oldest != "test:oldest:new" {
		t.Errorf("findOldestTrackerUnlocked() = %q, want %q", oldest, "test:oldest:new")
	}
}

// --- 10. evictForMemoryUnlocked ---

func TestEvictForMemoryUnlocked(t *testing.T) {
	dir := t.TempDir()
	cfg := testPersistenceConfig(dir)
	bloomCfg := Config{
		Mode:              ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	store, err := NewTrackerStore(cfg, bloomCfg)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Create several trackers with staggered timestamps.
	keys := []string{"test:evict:1", "test:evict:2", "test:evict:3"}
	for i, key := range keys {
		store.GetOrCreate(key)
		store.mu.Lock()
		store.index.Trackers[key].LastUpdated = time.Now().Add(time.Duration(i) * time.Second)
		store.mu.Unlock()
	}

	// Set memory usage well above the limit so eviction triggers.
	store.mu.Lock()
	store.memoryUsage = cfg.MaxMemory * 2
	store.evictForMemoryUnlocked()
	remaining := len(store.trackers)
	store.mu.Unlock()

	// At least some trackers should have been evicted.
	if remaining >= len(keys) {
		t.Errorf("expected some trackers to be evicted, still have %d of %d", remaining, len(keys))
	}
}

func TestEvictForMemoryUnlocked_NoEvictionNeeded(t *testing.T) {
	dir := t.TempDir()
	cfg := testPersistenceConfig(dir)
	bloomCfg := Config{
		Mode:              ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	store, err := NewTrackerStore(cfg, bloomCfg)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	store.GetOrCreate("test:noevict:1")

	// Memory is well under limit, no eviction should happen.
	store.mu.Lock()
	store.memoryUsage = 100 // Tiny amount.
	store.evictForMemoryUnlocked()
	remaining := len(store.trackers)
	store.mu.Unlock()

	if remaining != 1 {
		t.Errorf("expected 1 tracker to remain, got %d", remaining)
	}
}

// --- 11. saveLoop (Start/Stop with short intervals) ---

func TestSaveLoop_StartStop(t *testing.T) {
	dir := t.TempDir()
	cfg := testPersistenceConfig(dir)
	cfg.SaveInterval = 50 * time.Millisecond
	cfg.CleanupInterval = 50 * time.Millisecond

	bloomCfg := Config{
		Mode:              ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	store, err := NewTrackerStore(cfg, bloomCfg)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	store.Start()

	// Create a dirty tracker so the save loop has work to do.
	tracker := store.GetOrCreate("test:saveloop")
	tracker.Add([]byte("data"))

	// Wait for at least one save cycle.
	time.Sleep(200 * time.Millisecond)

	// Close should stop background goroutines gracefully.
	if err := store.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify data was persisted.
	store2, err := NewTrackerStore(cfg, bloomCfg)
	if err != nil {
		t.Fatalf("failed to create second store: %v", err)
	}
	if err := store2.LoadAll(); err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	if store2.TrackerCount() != 1 {
		t.Errorf("expected 1 tracker after reload, got %d", store2.TrackerCount())
	}
}

func TestSaveLoop_NoDirtyTrackers(t *testing.T) {
	dir := t.TempDir()
	cfg := testPersistenceConfig(dir)
	cfg.SaveInterval = 50 * time.Millisecond
	cfg.CleanupInterval = 50 * time.Millisecond

	bloomCfg := Config{
		Mode:              ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	store, err := NewTrackerStore(cfg, bloomCfg)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	store.Start()

	// No dirty trackers; saveLoop should still run without error.
	time.Sleep(150 * time.Millisecond)

	if err := store.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

// --- 12. LoadAll - corrupted/missing files ---

func TestLoadAll_NoIndexFile(t *testing.T) {
	dir := t.TempDir()
	store := newTestStore(t, dir)

	// No index file at all -- should return nil (fresh start).
	if err := store.LoadAll(); err != nil {
		t.Errorf("LoadAll on empty dir should succeed, got: %v", err)
	}
	if store.TrackerCount() != 0 {
		t.Errorf("expected 0 trackers, got %d", store.TrackerCount())
	}
}

func TestLoadAll_CorruptedIndex(t *testing.T) {
	dir := t.TempDir()
	store := newTestStore(t, dir)

	// Write corrupted JSON to meta.json.
	indexPath := filepath.Join(dir, metaFileName)
	if err := os.WriteFile(indexPath, []byte("{not valid json!!!"), 0600); err != nil {
		t.Fatalf("failed to write corrupted index: %v", err)
	}

	err := store.LoadAll()
	if err == nil {
		t.Error("LoadAll should fail with corrupted index")
	}
}

func TestLoadAll_VersionMismatch(t *testing.T) {
	dir := t.TempDir()
	store := newTestStore(t, dir)

	// Write index with wrong version.
	idx := PersistenceIndex{
		Version:  999,
		Trackers: make(map[string]*TrackerMeta),
	}
	data, _ := json.Marshal(idx)
	indexPath := filepath.Join(dir, metaFileName)
	if err := os.WriteFile(indexPath, data, 0600); err != nil {
		t.Fatalf("failed to write index: %v", err)
	}

	// Should not return error but skip loading (start fresh).
	if err := store.LoadAll(); err != nil {
		t.Errorf("LoadAll with version mismatch should succeed (start fresh), got: %v", err)
	}
	if store.TrackerCount() != 0 {
		t.Errorf("expected 0 trackers, got %d", store.TrackerCount())
	}
}

func TestLoadAll_StaleTrackerSkipped(t *testing.T) {
	dir := t.TempDir()
	cfg := testPersistenceConfig(dir)
	cfg.StateTTL = 1 * time.Millisecond // Very short TTL.
	bloomCfg := DefaultConfig()

	// First, create and save a tracker.
	store1, err := NewTrackerStore(cfg, bloomCfg)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	tracker := store1.GetOrCreate("test:stale:load")
	tracker.Add([]byte("data"))
	if err := store1.SaveAll(); err != nil {
		t.Fatalf("SaveAll failed: %v", err)
	}

	// Wait for TTL to expire.
	time.Sleep(10 * time.Millisecond)

	// Load in new store -- stale tracker should be skipped.
	store2, err := NewTrackerStore(cfg, bloomCfg)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	if err := store2.LoadAll(); err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	if store2.TrackerCount() != 0 {
		t.Errorf("expected 0 trackers (stale should be skipped), got %d", store2.TrackerCount())
	}
}

func TestLoadAll_MissingTrackerFile(t *testing.T) {
	dir := t.TempDir()
	cfg := testPersistenceConfig(dir)
	bloomCfg := DefaultConfig()

	// Create the store first so directories are set up.
	store, err := NewTrackerStore(cfg, bloomCfg)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Write a valid index that references a non-existent tracker file.
	hash := hashKey("test:missing:file")
	idx := PersistenceIndex{
		Version: indexVersion,
		Trackers: map[string]*TrackerMeta{
			"test:missing:file": {
				Key:         "test:missing:file",
				Hash:        hash,
				Count:       5,
				LastUpdated: time.Now(),
				CreatedAt:   time.Now(),
			},
		},
	}
	data, _ := json.MarshalIndent(idx, "", "  ")
	indexPath := filepath.Join(dir, metaFileName)
	if err := os.WriteFile(indexPath, data, 0600); err != nil {
		t.Fatalf("failed to write index: %v", err)
	}

	// LoadAll should not return error (it logs and skips the missing file).
	if err := store.LoadAll(); err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	if store.TrackerCount() != 0 {
		t.Errorf("expected 0 trackers (missing file skipped), got %d", store.TrackerCount())
	}
}

func TestLoadAll_CorruptedTrackerFile(t *testing.T) {
	dir := t.TempDir()
	cfg := testPersistenceConfig(dir)
	bloomCfg := DefaultConfig()

	// Create the store first so directories get created.
	store, err := NewTrackerStore(cfg, bloomCfg)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	hash := hashKey("test:corrupted:tracker")

	// Write a valid index.
	idx := PersistenceIndex{
		Version: indexVersion,
		Trackers: map[string]*TrackerMeta{
			"test:corrupted:tracker": {
				Key:         "test:corrupted:tracker",
				Hash:        hash,
				Count:       5,
				LastUpdated: time.Now(),
				CreatedAt:   time.Now(),
			},
		},
	}
	data, _ := json.MarshalIndent(idx, "", "  ")
	indexPath := filepath.Join(dir, metaFileName)
	if err := os.WriteFile(indexPath, data, 0600); err != nil {
		t.Fatalf("failed to write index: %v", err)
	}

	// Write corrupted bloom data file.
	trackersPath := filepath.Join(dir, trackersDir)
	corruptPath := filepath.Join(trackersPath, hash+".bloom")
	if err := os.WriteFile(corruptPath, []byte("not a bloom filter"), 0600); err != nil {
		t.Fatalf("failed to write corrupted tracker file: %v", err)
	}

	// LoadAll should not return error (it logs and skips the corrupt file).
	if err := store.LoadAll(); err != nil {
		t.Fatalf("LoadAll failed: %v", err)
	}
	if store.TrackerCount() != 0 {
		t.Errorf("expected 0 trackers (corrupted file skipped), got %d", store.TrackerCount())
	}
}

// --- 13. saveTracker error paths ---

func TestSaveTracker_MissingTracker(t *testing.T) {
	dir := t.TempDir()
	store := newTestStore(t, dir)

	// Calling saveTracker for a non-existent key should not error (returns nil).
	err := store.saveTracker("nonexistent:key")
	if err != nil {
		t.Errorf("saveTracker for missing key should return nil, got: %v", err)
	}
}

func TestSaveTracker_UncompressedMode(t *testing.T) {
	dir := t.TempDir()
	cfg := testPersistenceConfig(dir)
	cfg.Compression = false
	bloomCfg := DefaultConfig()

	store, err := NewTrackerStore(cfg, bloomCfg)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	tracker := store.GetOrCreate("test:save:uncompressed")
	tracker.Add([]byte("item1"))

	if err := store.saveTracker("test:save:uncompressed"); err != nil {
		t.Fatalf("saveTracker failed: %v", err)
	}

	// Verify .bloom file (not .bloom.gz) exists.
	hash := hashKey("test:save:uncompressed")
	trackersPath := filepath.Join(dir, trackersDir)
	bloomPath := filepath.Join(trackersPath, hash+".bloom")
	if _, err := os.Stat(bloomPath); os.IsNotExist(err) {
		t.Error("expected .bloom file to exist for uncompressed save")
	}
}

// --- 14. cleanOrphanedFiles ---

func TestCleanOrphanedFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := testPersistenceConfig(dir)
	bloomCfg := DefaultConfig()

	store, err := NewTrackerStore(cfg, bloomCfg)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Create a real tracker and save it.
	tracker := store.GetOrCreate("test:orphan:keep")
	tracker.Add([]byte("data"))
	if err := store.SaveAll(); err != nil {
		t.Fatalf("SaveAll failed: %v", err)
	}

	// Create orphaned files in the trackers directory.
	trackersPath := filepath.Join(dir, trackersDir)
	orphan1 := filepath.Join(trackersPath, "deadbeef00000000.bloom.gz")
	orphan2 := filepath.Join(trackersPath, "cafebabe11111111.bloom")
	os.WriteFile(orphan1, []byte("orphan1"), 0600)
	os.WriteFile(orphan2, []byte("orphan2"), 0600)

	// Verify orphaned files exist before cleanup.
	if _, err := os.Stat(orphan1); os.IsNotExist(err) {
		t.Fatal("orphan1 should exist before cleanup")
	}
	if _, err := os.Stat(orphan2); os.IsNotExist(err) {
		t.Fatal("orphan2 should exist before cleanup")
	}

	store.cleanOrphanedFiles()

	// Orphaned files should be removed.
	if _, err := os.Stat(orphan1); !os.IsNotExist(err) {
		t.Error("orphan1 should have been removed")
	}
	if _, err := os.Stat(orphan2); !os.IsNotExist(err) {
		t.Error("orphan2 should have been removed")
	}

	// The real tracker file should still exist.
	hash := hashKey("test:orphan:keep")
	realFile := filepath.Join(trackersPath, hash+".bloom.gz")
	if _, err := os.Stat(realFile); os.IsNotExist(err) {
		t.Error("real tracker file should not have been removed")
	}
}

func TestCleanOrphanedFiles_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	store := newTestStore(t, dir)

	// Should not panic on empty directory.
	store.cleanOrphanedFiles()
}

func TestCleanOrphanedFiles_NonExistentDir(t *testing.T) {
	cfg := PersistenceConfig{
		Enabled:          true,
		Path:             "/tmp/nonexistent_test_dir_coverage",
		SaveInterval:     time.Hour,
		StateTTL:         time.Hour,
		CleanupInterval:  time.Hour,
		MaxSize:          100 * 1024 * 1024,
		MaxMemory:        50 * 1024 * 1024,
		Compression:      true,
		CompressionLevel: 1,
	}

	store, err := NewTrackerStore(cfg, DefaultConfig())
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Remove the trackers directory so cleanOrphanedFiles hits the ReadDir error path.
	trackersPath := filepath.Join(cfg.Path, trackersDir)
	os.RemoveAll(trackersPath)
	defer os.RemoveAll(cfg.Path) // Cleanup.

	// Should not panic.
	store.cleanOrphanedFiles()
}

func TestCleanOrphanedFiles_SkipsDirectories(t *testing.T) {
	dir := t.TempDir()
	store := newTestStore(t, dir)

	// Create a subdirectory inside trackers (should be skipped, not removed).
	trackersPath := filepath.Join(dir, trackersDir)
	subdir := filepath.Join(trackersPath, "somedir")
	if err := os.Mkdir(subdir, 0700); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	store.cleanOrphanedFiles()

	// The directory should still exist (cleanOrphanedFiles skips directories).
	if _, err := os.Stat(subdir); os.IsNotExist(err) {
		t.Error("subdirectory should not have been removed")
	}
}

// --- Additional coverage: LoadAll triggers cleanOrphanedFiles ---

func TestLoadAll_CleansOrphanedFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := testPersistenceConfig(dir)
	bloomCfg := DefaultConfig()

	// Create a store, save a tracker, close.
	store1, err := NewTrackerStore(cfg, bloomCfg)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	tracker := store1.GetOrCreate("test:load:orphan")
	tracker.Add([]byte("d1"))
	store1.SaveAll()

	// Plant an orphaned file.
	trackersPath := filepath.Join(dir, trackersDir)
	orphan := filepath.Join(trackersPath, "orphan123456789abc.bloom.gz")
	os.WriteFile(orphan, []byte("orphan"), 0600)

	// Load in new store -- orphan should be cleaned.
	store2, err := NewTrackerStore(cfg, bloomCfg)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	store2.LoadAll()

	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Error("orphaned file should have been cleaned during LoadAll")
	}
}

// --- Additional: Cleanup evicts by disk size ---

func TestCleanup_EvictsByDiskSize(t *testing.T) {
	dir := t.TempDir()
	cfg := testPersistenceConfig(dir)
	cfg.MaxSize = 1 // Very small disk limit to force size eviction.
	bloomCfg := DefaultConfig()

	store, err := NewTrackerStore(cfg, bloomCfg)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	store.GetOrCreate("test:disk:evict:1")
	store.GetOrCreate("test:disk:evict:2")

	// Artificially increase disk usage.
	store.mu.Lock()
	store.diskUsage = 1000
	store.index.Trackers["test:disk:evict:1"].SizeBytes = 500
	store.index.Trackers["test:disk:evict:2"].SizeBytes = 500
	store.mu.Unlock()

	store.Cleanup()

	// Trackers should be evicted until disk usage is under limit.
	if store.TrackerCount() > 0 && store.DiskUsage() > cfg.MaxSize {
		t.Errorf("cleanup should have evicted trackers to get under disk limit")
	}
}

// --- Additional: PersistentTracker.Add marks dirty ---

func TestPersistentTracker_Add_MarksDirty(t *testing.T) {
	dir := t.TempDir()
	store := newTestStore(t, dir)

	tracker := store.GetOrCreate("test:dirty:add")

	// After creation the tracker is already dirty, clear that.
	store.mu.Lock()
	delete(store.dirty, "test:dirty:add")
	store.mu.Unlock()

	// Add a new item.
	tracker.Add([]byte("newitem"))

	store.mu.RLock()
	isDirty := store.dirty["test:dirty:add"]
	store.mu.RUnlock()

	if !isDirty {
		t.Error("tracker should be marked dirty after Add")
	}
}

// --- Additional: saveTracker with missing metadata ---

func TestSaveTracker_MissingMetadata(t *testing.T) {
	dir := t.TempDir()
	store := newTestStore(t, dir)

	// Create a tracker but remove its metadata.
	store.GetOrCreate("test:save:nometa")
	store.mu.Lock()
	delete(store.index.Trackers, "test:save:nometa")
	store.mu.Unlock()

	// Should return nil (no-op).
	err := store.saveTracker("test:save:nometa")
	if err != nil {
		t.Errorf("saveTracker with missing metadata should return nil, got: %v", err)
	}
}

// --- Additional: MarkDirty ---

func TestMarkDirty(t *testing.T) {
	dir := t.TempDir()
	store := newTestStore(t, dir)

	store.GetOrCreate("test:markdirty")
	store.SaveAll() // Clears dirty.

	store.mu.RLock()
	isDirty := store.dirty["test:markdirty"]
	store.mu.RUnlock()
	if isDirty {
		t.Error("tracker should not be dirty after SaveAll")
	}

	store.MarkDirty("test:markdirty")

	store.mu.RLock()
	isDirty = store.dirty["test:markdirty"]
	store.mu.RUnlock()
	if !isDirty {
		t.Error("tracker should be dirty after MarkDirty")
	}
}

// --- Additional: MarkDirty for non-existent key ---

func TestMarkDirty_NonExistentMeta(t *testing.T) {
	dir := t.TempDir()
	store := newTestStore(t, dir)

	// Should not panic.
	store.MarkDirty("nonexistent:key")

	store.mu.RLock()
	isDirty := store.dirty["nonexistent:key"]
	store.mu.RUnlock()
	if !isDirty {
		t.Error("even non-existent key should be marked dirty")
	}
}

// --- Additional: TrackerCount ---

func TestTrackerCount(t *testing.T) {
	dir := t.TempDir()
	store := newTestStore(t, dir)

	if store.TrackerCount() != 0 {
		t.Errorf("expected 0, got %d", store.TrackerCount())
	}

	store.GetOrCreate("test:count:1")
	store.GetOrCreate("test:count:2")

	if store.TrackerCount() != 2 {
		t.Errorf("expected 2, got %d", store.TrackerCount())
	}
}

// --- Additional: GetOrCreate returns existing tracker ---

func TestGetOrCreate_ReturnsExisting(t *testing.T) {
	dir := t.TempDir()
	store := newTestStore(t, dir)

	t1 := store.GetOrCreate("test:same:key")
	t2 := store.GetOrCreate("test:same:key")

	if t1 != t2 {
		t.Error("GetOrCreate should return the same tracker for the same key")
	}
}

// --- Additional: GetOrCreate with invalid key ---

func TestGetOrCreate_InvalidKey(t *testing.T) {
	dir := t.TempDir()
	store := newTestStore(t, dir)

	tracker := store.GetOrCreate("../invalid")
	if tracker.store != nil {
		t.Error("invalid key should produce a non-persistent tracker")
	}
}

// --- Helpers ---

func testPersistenceConfig(dir string) PersistenceConfig {
	return PersistenceConfig{
		Enabled:          true,
		Path:             dir,
		SaveInterval:     time.Hour,
		StateTTL:         time.Hour,
		CleanupInterval:  time.Hour,
		MaxSize:          100 * 1024 * 1024,
		MaxMemory:        50 * 1024 * 1024,
		Compression:      true,
		CompressionLevel: 1,
	}
}

func newTestStore(t *testing.T, dir string) *TrackerStore {
	t.Helper()
	store, err := NewTrackerStore(testPersistenceConfig(dir), DefaultConfig())
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}
	return store
}
