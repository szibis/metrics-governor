package cardinality

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTrackerStore_CreateAndSave(t *testing.T) {
	dir := t.TempDir()
	cfg := PersistenceConfig{
		Enabled:          true,
		Path:             dir,
		SaveInterval:     time.Hour, // Don't auto-save during tests
		StateTTL:         time.Hour,
		CleanupInterval:  time.Hour,
		MaxSize:          100 * 1024 * 1024,
		MaxMemory:        50 * 1024 * 1024,
		Compression:      true,
		CompressionLevel: 1,
	}
	bloomCfg := Config{
		Mode:              ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	store, err := NewTrackerStore(cfg, bloomCfg)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	// Create and use a tracker
	tracker := store.GetOrCreate("test:metric:1")
	tracker.Add([]byte("key1"))
	tracker.Add([]byte("key2"))
	tracker.Add([]byte("key3"))

	// Save
	if err := store.SaveAll(); err != nil {
		t.Fatalf("failed to save: %v", err)
	}

	// Verify files exist
	metaPath := filepath.Join(dir, metaFileName)
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		t.Error("meta.json not created")
	}

	// Verify tracker file exists
	trackersPath := filepath.Join(dir, trackersDir)
	entries, err := os.ReadDir(trackersPath)
	if err != nil {
		t.Fatalf("failed to read trackers dir: %v", err)
	}
	if len(entries) == 0 {
		t.Error("no tracker files created")
	}

	// Verify the file has .bloom.gz extension
	found := false
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".gz" {
			found = true
			break
		}
	}
	if !found {
		t.Error("compressed tracker file not created")
	}
}

func TestTrackerStore_LoadExisting(t *testing.T) {
	dir := t.TempDir()
	cfg := PersistenceConfig{
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
	bloomCfg := Config{
		Mode:              ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	// Create and save
	store1, err := NewTrackerStore(cfg, bloomCfg)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	tracker := store1.GetOrCreate("test:metric:load")
	tracker.Add([]byte("item1"))
	tracker.Add([]byte("item2"))

	if err := store1.SaveAll(); err != nil {
		t.Fatalf("failed to save: %v", err)
	}

	// Create new store and load
	store2, err := NewTrackerStore(cfg, bloomCfg)
	if err != nil {
		t.Fatalf("failed to create second store: %v", err)
	}

	if err := store2.LoadAll(); err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	// Verify tracker was loaded
	if store2.TrackerCount() != 1 {
		t.Errorf("expected 1 tracker, got %d", store2.TrackerCount())
	}

	// Get the loaded tracker
	loadedTracker := store2.GetOrCreate("test:metric:load")
	if loadedTracker.Count() != 2 {
		t.Errorf("expected count 2, got %d", loadedTracker.Count())
	}
}

func TestTrackerStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := PersistenceConfig{
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
	bloomCfg := Config{
		Mode:              ModeBloom,
		ExpectedItems:     10000,
		FalsePositiveRate: 0.01,
	}

	// Create and add many items
	store1, _ := NewTrackerStore(cfg, bloomCfg)
	tracker := store1.GetOrCreate("test:roundtrip")

	itemCount := 1000
	for i := 0; i < itemCount; i++ {
		tracker.Add([]byte("item" + string(rune(i))))
	}
	originalCount := tracker.Count()

	// Save
	store1.SaveAll()

	// Load in new store
	store2, _ := NewTrackerStore(cfg, bloomCfg)
	store2.LoadAll()

	loadedTracker := store2.GetOrCreate("test:roundtrip")
	loadedCount := loadedTracker.Count()

	// Count should match (stored in metadata)
	if loadedCount != originalCount {
		t.Errorf("count mismatch: original %d, loaded %d", originalCount, loadedCount)
	}

	// Test membership for existing items
	if !loadedTracker.TestOnly([]byte("item0")) {
		t.Error("expected item0 to exist")
	}
}

func TestCompression_Enabled(t *testing.T) {
	dir := t.TempDir()
	cfg := PersistenceConfig{
		Enabled:          true,
		Path:             dir,
		SaveInterval:     time.Hour,
		StateTTL:         time.Hour,
		CleanupInterval:  time.Hour,
		MaxSize:          100 * 1024 * 1024,
		MaxMemory:        50 * 1024 * 1024,
		Compression:      true,
		CompressionLevel: 6,
	}
	bloomCfg := Config{
		Mode:              ModeBloom,
		ExpectedItems:     10000,
		FalsePositiveRate: 0.01,
	}

	store, _ := NewTrackerStore(cfg, bloomCfg)
	tracker := store.GetOrCreate("test:compression")

	// Add many items to make compression effective
	for i := 0; i < 5000; i++ {
		tracker.Add([]byte("item" + string(rune(i))))
	}

	store.SaveAll()

	// Check that .gz file exists
	trackersPath := filepath.Join(dir, trackersDir)
	entries, _ := os.ReadDir(trackersPath)

	var gzFound bool
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".gz" {
			gzFound = true
		}
	}

	if !gzFound {
		t.Error("compressed file not created")
	}
}

func TestCompression_Disabled(t *testing.T) {
	dir := t.TempDir()
	cfg := PersistenceConfig{
		Enabled:          true,
		Path:             dir,
		SaveInterval:     time.Hour,
		StateTTL:         time.Hour,
		CleanupInterval:  time.Hour,
		MaxSize:          100 * 1024 * 1024,
		MaxMemory:        50 * 1024 * 1024,
		Compression:      false,
		CompressionLevel: 0,
	}
	bloomCfg := Config{
		Mode:              ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	store, _ := NewTrackerStore(cfg, bloomCfg)
	tracker := store.GetOrCreate("test:nocompression")
	tracker.Add([]byte("item1"))
	store.SaveAll()

	// Check that non-gz file exists
	trackersPath := filepath.Join(dir, trackersDir)
	entries, _ := os.ReadDir(trackersPath)

	var rawFound bool
	for _, entry := range entries {
		name := entry.Name()
		if filepath.Ext(name) == ".bloom" {
			rawFound = true
		}
	}

	if !rawFound {
		t.Error("raw (non-compressed) file not created")
	}
}

func TestCompression_AutoDetect(t *testing.T) {
	dir := t.TempDir()

	// Create with compression enabled
	cfg := PersistenceConfig{
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
	bloomCfg := Config{
		Mode:              ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	store1, _ := NewTrackerStore(cfg, bloomCfg)
	tracker := store1.GetOrCreate("test:autodetect")
	tracker.Add([]byte("item1"))
	store1.SaveAll()

	// Load with compression disabled (should still work due to auto-detect)
	cfg.Compression = false
	store2, _ := NewTrackerStore(cfg, bloomCfg)
	if err := store2.LoadAll(); err != nil {
		t.Fatalf("failed to load compressed file with compression disabled: %v", err)
	}

	if store2.TrackerCount() != 1 {
		t.Errorf("expected 1 tracker, got %d", store2.TrackerCount())
	}
}

func TestCleanup_RemovesStaleTrackers(t *testing.T) {
	dir := t.TempDir()
	cfg := PersistenceConfig{
		Enabled:          true,
		Path:             dir,
		SaveInterval:     time.Hour,
		StateTTL:         100 * time.Millisecond, // Very short TTL
		CleanupInterval:  time.Hour,
		MaxSize:          100 * 1024 * 1024,
		MaxMemory:        50 * 1024 * 1024,
		Compression:      true,
		CompressionLevel: 1,
	}
	bloomCfg := Config{
		Mode:              ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	store, _ := NewTrackerStore(cfg, bloomCfg)
	store.GetOrCreate("test:stale")
	store.SaveAll()

	// Wait for TTL to expire
	time.Sleep(200 * time.Millisecond)

	// Run cleanup
	store.Cleanup()

	// Tracker should be removed
	if store.TrackerCount() != 0 {
		t.Errorf("expected 0 trackers after cleanup, got %d", store.TrackerCount())
	}
}

func TestCleanup_KeepsFreshTrackers(t *testing.T) {
	dir := t.TempDir()
	cfg := PersistenceConfig{
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
	bloomCfg := Config{
		Mode:              ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	store, _ := NewTrackerStore(cfg, bloomCfg)
	store.GetOrCreate("test:fresh")

	// Run cleanup immediately
	store.Cleanup()

	// Tracker should still exist
	if store.TrackerCount() != 1 {
		t.Errorf("expected 1 tracker after cleanup, got %d", store.TrackerCount())
	}
}

func TestMemory_TracksUsage(t *testing.T) {
	dir := t.TempDir()
	cfg := PersistenceConfig{
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
	bloomCfg := Config{
		Mode:              ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	store, _ := NewTrackerStore(cfg, bloomCfg)

	initialMemory := store.MemoryUsage()
	if initialMemory != 0 {
		t.Errorf("expected initial memory 0, got %d", initialMemory)
	}

	// Create tracker
	store.GetOrCreate("test:memory")

	// Memory should increase
	if store.MemoryUsage() <= initialMemory {
		t.Error("memory usage should increase after creating tracker")
	}
}

func TestMissingDirectory_Created(t *testing.T) {
	dir := t.TempDir()
	nestedPath := filepath.Join(dir, "nested", "deeply", "path")

	cfg := PersistenceConfig{
		Enabled:          true,
		Path:             nestedPath,
		SaveInterval:     time.Hour,
		StateTTL:         time.Hour,
		CleanupInterval:  time.Hour,
		MaxSize:          100 * 1024 * 1024,
		MaxMemory:        50 * 1024 * 1024,
		Compression:      true,
		CompressionLevel: 1,
	}
	bloomCfg := Config{
		Mode:              ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	store, err := NewTrackerStore(cfg, bloomCfg)
	if err != nil {
		t.Fatalf("failed to create store with nested path: %v", err)
	}

	// Verify directories were created
	trackersPath := filepath.Join(nestedPath, trackersDir)
	if _, err := os.Stat(trackersPath); os.IsNotExist(err) {
		t.Error("trackers directory not created")
	}

	// Clean up
	store.Close()
}

func TestConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	cfg := PersistenceConfig{
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
	bloomCfg := Config{
		Mode:              ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	store, _ := NewTrackerStore(cfg, bloomCfg)

	// Concurrent tracker creation and usage
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < 100; j++ {
				tracker := store.GetOrCreate("test:concurrent")
				tracker.Add([]byte("item"))
				tracker.Count()
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify no panics and store is consistent
	if store.TrackerCount() != 1 {
		t.Errorf("expected 1 tracker, got %d", store.TrackerCount())
	}
}

func TestGracefulShutdown(t *testing.T) {
	dir := t.TempDir()
	cfg := PersistenceConfig{
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
	bloomCfg := Config{
		Mode:              ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	store, _ := NewTrackerStore(cfg, bloomCfg)
	store.Start()

	// Create and use tracker
	tracker := store.GetOrCreate("test:shutdown")
	tracker.Add([]byte("item1"))

	// Close should save dirty trackers
	if err := store.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	// Verify data was saved by loading in new store
	store2, _ := NewTrackerStore(cfg, bloomCfg)
	store2.LoadAll()

	if store2.TrackerCount() != 1 {
		t.Errorf("expected 1 tracker after reload, got %d", store2.TrackerCount())
	}
}

func TestPathTraversal_Rejected(t *testing.T) {
	dir := t.TempDir()
	cfg := PersistenceConfig{
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
	bloomCfg := Config{
		Mode:              ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	store, _ := NewTrackerStore(cfg, bloomCfg)

	// Try to create tracker with path traversal
	tracker := store.GetOrCreate("../../../etc/passwd")

	// Should return a non-persistent tracker (store is nil)
	if tracker.store != nil {
		t.Error("tracker with path traversal key should not be persistent")
	}
}

func TestInvalidPathRejected(t *testing.T) {
	cfg := PersistenceConfig{
		Enabled:          true,
		Path:             "../../../tmp",
		SaveInterval:     time.Hour,
		StateTTL:         time.Hour,
		CleanupInterval:  time.Hour,
		MaxSize:          100 * 1024 * 1024,
		MaxMemory:        50 * 1024 * 1024,
		Compression:      true,
		CompressionLevel: 1,
	}
	bloomCfg := Config{
		Mode:              ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	_, err := NewTrackerStore(cfg, bloomCfg)
	if err == nil {
		t.Error("expected error for path with '..'")
	}
}

func TestHashKey(t *testing.T) {
	// Same key should produce same hash
	hash1 := hashKey("test:metric:1")
	hash2 := hashKey("test:metric:1")
	if hash1 != hash2 {
		t.Error("same key should produce same hash")
	}

	// Different keys should produce different hashes
	hash3 := hashKey("test:metric:2")
	if hash1 == hash3 {
		t.Error("different keys should produce different hashes")
	}

	// Hash should be hex string
	for _, c := range hash1 {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("hash should be hex string, got char: %c", c)
		}
	}
}

func TestIsValidKey(t *testing.T) {
	tests := []struct {
		key   string
		valid bool
	}{
		{"test:metric:1", true},
		{"stats:label:service=foo", true},
		{"limits:rule:group=bar", true},
		{"../etc/passwd", false},
		{"test/../passwd", false},
		{"", false},
		{"test\x00key", false},
		{"test\nkey", false},
	}

	for _, tt := range tests {
		if isValidKey(tt.key) != tt.valid {
			t.Errorf("isValidKey(%q) = %v, want %v", tt.key, !tt.valid, tt.valid)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{1073741824, "1.0 GB"},
	}

	for _, tt := range tests {
		result := formatBytes(tt.bytes)
		if result != tt.expected {
			t.Errorf("formatBytes(%d) = %s, want %s", tt.bytes, result, tt.expected)
		}
	}
}

func TestNewPersistentTrackerFromGlobal_WithStore(t *testing.T) {
	dir := t.TempDir()
	cfg := PersistenceConfig{
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
	bloomCfg := Config{
		Mode:              ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	store, _ := NewTrackerStore(cfg, bloomCfg)
	GlobalTrackerStore = store
	GlobalConfig = bloomCfg
	defer func() {
		GlobalTrackerStore = nil
	}()

	// Should return a PersistentTracker
	tracker := NewPersistentTrackerFromGlobal("test:global")
	_, ok := tracker.(*PersistentTracker)
	if !ok {
		t.Error("expected PersistentTracker when GlobalTrackerStore is set")
	}
}

func TestNewPersistentTrackerFromGlobal_WithoutStore(t *testing.T) {
	GlobalTrackerStore = nil
	GlobalConfig = Config{
		Mode:              ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	// Should return a regular BloomTracker
	tracker := NewPersistentTrackerFromGlobal("test:global")
	_, ok := tracker.(*BloomTracker)
	if !ok {
		t.Error("expected BloomTracker when GlobalTrackerStore is nil")
	}
}
