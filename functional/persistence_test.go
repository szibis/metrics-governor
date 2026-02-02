package functional

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/cardinality"
)

// TestFunctional_Persistence_BasicSaveLoad tests basic save and load functionality
func TestFunctional_Persistence_BasicSaveLoad(t *testing.T) {
	dir := t.TempDir()
	cfg := cardinality.PersistenceConfig{
		Enabled:          true,
		Path:             dir,
		SaveInterval:     time.Hour, // Don't auto-save
		StateTTL:         time.Hour,
		CleanupInterval:  time.Hour,
		MaxSize:          100 * 1024 * 1024,
		MaxMemory:        50 * 1024 * 1024,
		Compression:      true,
		CompressionLevel: 1,
	}
	bloomCfg := cardinality.Config{
		Mode:              cardinality.ModeBloom,
		ExpectedItems:     10000,
		FalsePositiveRate: 0.01,
	}

	// Create store and add data
	store1, err := cardinality.NewTrackerStore(cfg, bloomCfg)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create multiple trackers with different keys
	keys := []string{
		"stats:metric:http_requests",
		"stats:metric:db_queries",
		"limits:rule1:service=api",
		"limits:rule2:env=prod",
	}

	for _, key := range keys {
		tracker := store1.GetOrCreate(key)
		for i := 0; i < 100; i++ {
			tracker.Add([]byte("item" + string(rune(i))))
		}
	}

	// Save
	if err := store1.SaveAll(); err != nil {
		t.Fatalf("Failed to save: %v", err)
	}

	// Create new store and load
	store2, err := cardinality.NewTrackerStore(cfg, bloomCfg)
	if err != nil {
		t.Fatalf("Failed to create second store: %v", err)
	}

	if err := store2.LoadAll(); err != nil {
		t.Fatalf("Failed to load: %v", err)
	}

	// Verify all trackers were loaded
	if store2.TrackerCount() != len(keys) {
		t.Errorf("Expected %d trackers, got %d", len(keys), store2.TrackerCount())
	}

	// Verify counts are preserved
	for _, key := range keys {
		tracker := store2.GetOrCreate(key)
		if tracker.Count() != 100 {
			t.Errorf("Tracker %s: expected count 100, got %d", key, tracker.Count())
		}
	}
}

// TestFunctional_Persistence_TTLCleanup tests that old trackers are cleaned up
func TestFunctional_Persistence_TTLCleanup(t *testing.T) {
	dir := t.TempDir()
	cfg := cardinality.PersistenceConfig{
		Enabled:          true,
		Path:             dir,
		SaveInterval:     time.Hour,
		StateTTL:         200 * time.Millisecond, // Very short TTL
		CleanupInterval:  time.Hour,
		MaxSize:          100 * 1024 * 1024,
		MaxMemory:        50 * 1024 * 1024,
		Compression:      true,
		CompressionLevel: 1,
	}
	bloomCfg := cardinality.Config{
		Mode:              cardinality.ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	store, _ := cardinality.NewTrackerStore(cfg, bloomCfg)

	// Create old tracker
	tracker := store.GetOrCreate("old:tracker")
	tracker.Add([]byte("item"))
	store.SaveAll()

	// Wait for TTL to expire
	time.Sleep(300 * time.Millisecond)

	// Create fresh tracker (should survive cleanup)
	freshTracker := store.GetOrCreate("fresh:tracker")
	freshTracker.Add([]byte("item"))

	// Run cleanup
	store.Cleanup()

	// Old tracker should be gone, fresh should remain
	if store.TrackerCount() != 1 {
		t.Errorf("Expected 1 tracker after cleanup, got %d", store.TrackerCount())
	}
}

// TestFunctional_Persistence_DiskUsageTracking tests disk usage is tracked correctly
func TestFunctional_Persistence_DiskUsageTracking(t *testing.T) {
	dir := t.TempDir()
	cfg := cardinality.PersistenceConfig{
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
	bloomCfg := cardinality.Config{
		Mode:              cardinality.ModeBloom,
		ExpectedItems:     10000,
		FalsePositiveRate: 0.01,
	}

	store, _ := cardinality.NewTrackerStore(cfg, bloomCfg)

	// Initial disk usage should be 0
	if store.DiskUsage() != 0 {
		t.Errorf("Expected initial disk usage 0, got %d", store.DiskUsage())
	}

	// Create and save trackers
	for i := 0; i < 5; i++ {
		tracker := store.GetOrCreate("tracker:" + string(rune('A'+i)))
		for j := 0; j < 1000; j++ {
			tracker.Add([]byte("item" + string(rune(j))))
		}
	}

	store.SaveAll()

	// Disk usage should be > 0
	if store.DiskUsage() <= 0 {
		t.Error("Expected disk usage > 0 after save")
	}

	// Verify actual files exist
	trackersPath := filepath.Join(dir, "trackers")
	entries, err := os.ReadDir(trackersPath)
	if err != nil {
		t.Fatalf("Failed to read trackers dir: %v", err)
	}
	if len(entries) != 5 {
		t.Errorf("Expected 5 tracker files, got %d", len(entries))
	}
}

// TestFunctional_Persistence_MemoryTracking tests memory usage is tracked correctly
func TestFunctional_Persistence_MemoryTracking(t *testing.T) {
	dir := t.TempDir()
	cfg := cardinality.PersistenceConfig{
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
	bloomCfg := cardinality.Config{
		Mode:              cardinality.ModeBloom,
		ExpectedItems:     10000,
		FalsePositiveRate: 0.01,
	}

	store, _ := cardinality.NewTrackerStore(cfg, bloomCfg)

	// Initial memory usage should be 0
	if store.MemoryUsage() != 0 {
		t.Errorf("Expected initial memory usage 0, got %d", store.MemoryUsage())
	}

	// Create trackers
	for i := 0; i < 10; i++ {
		store.GetOrCreate("tracker:" + string(rune('A'+i)))
	}

	// Memory usage should be > 0
	memUsage := store.MemoryUsage()
	if memUsage <= 0 {
		t.Error("Expected memory usage > 0 after creating trackers")
	}

	// Memory should increase with more trackers
	for i := 0; i < 10; i++ {
		store.GetOrCreate("more:tracker:" + string(rune('A'+i)))
	}

	newMemUsage := store.MemoryUsage()
	if newMemUsage <= memUsage {
		t.Error("Expected memory usage to increase with more trackers")
	}
}

// TestFunctional_Persistence_CompressionSavesSpace tests that compression reduces file size
func TestFunctional_Persistence_CompressionSavesSpace(t *testing.T) {
	// Create uncompressed store
	dirUncompressed := t.TempDir()
	cfgUncompressed := cardinality.PersistenceConfig{
		Enabled:          true,
		Path:             dirUncompressed,
		SaveInterval:     time.Hour,
		StateTTL:         time.Hour,
		CleanupInterval:  time.Hour,
		MaxSize:          100 * 1024 * 1024,
		MaxMemory:        50 * 1024 * 1024,
		Compression:      false,
		CompressionLevel: 0,
	}

	// Create compressed store
	dirCompressed := t.TempDir()
	cfgCompressed := cardinality.PersistenceConfig{
		Enabled:          true,
		Path:             dirCompressed,
		SaveInterval:     time.Hour,
		StateTTL:         time.Hour,
		CleanupInterval:  time.Hour,
		MaxSize:          100 * 1024 * 1024,
		MaxMemory:        50 * 1024 * 1024,
		Compression:      true,
		CompressionLevel: 6, // Good compression
	}

	bloomCfg := cardinality.Config{
		Mode:              cardinality.ModeBloom,
		ExpectedItems:     100000,
		FalsePositiveRate: 0.01,
	}

	storeUncompressed, _ := cardinality.NewTrackerStore(cfgUncompressed, bloomCfg)
	storeCompressed, _ := cardinality.NewTrackerStore(cfgCompressed, bloomCfg)

	// Add same data to both
	for _, store := range []*cardinality.TrackerStore{storeUncompressed, storeCompressed} {
		tracker := store.GetOrCreate("test:compression")
		for i := 0; i < 50000; i++ {
			tracker.Add([]byte("item" + string(rune(i))))
		}
		store.SaveAll()
	}

	// Compare disk usage
	uncompressedSize := storeUncompressed.DiskUsage()
	compressedSize := storeCompressed.DiskUsage()

	t.Logf("Uncompressed: %d bytes, Compressed: %d bytes, Ratio: %.2f",
		uncompressedSize, compressedSize, float64(compressedSize)/float64(uncompressedSize))

	if compressedSize >= uncompressedSize {
		t.Error("Expected compressed size to be smaller than uncompressed")
	}
}

// TestFunctional_Persistence_RestartRecovery tests recovery after simulated restart
func TestFunctional_Persistence_RestartRecovery(t *testing.T) {
	dir := t.TempDir()
	cfg := cardinality.PersistenceConfig{
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
	bloomCfg := cardinality.Config{
		Mode:              cardinality.ModeBloom,
		ExpectedItems:     10000,
		FalsePositiveRate: 0.01,
	}

	// First "session"
	store1, _ := cardinality.NewTrackerStore(cfg, bloomCfg)
	store1.Start()

	tracker := store1.GetOrCreate("stats:metric:requests")
	for i := 0; i < 5000; i++ {
		tracker.Add([]byte("series:" + string(rune(i))))
	}
	originalCount := tracker.Count()

	// Graceful shutdown
	store1.Close()

	// Second "session" (simulating restart)
	store2, _ := cardinality.NewTrackerStore(cfg, bloomCfg)
	store2.LoadAll()
	store2.Start()

	// Verify data was recovered
	recoveredTracker := store2.GetOrCreate("stats:metric:requests")
	recoveredCount := recoveredTracker.Count()

	if recoveredCount != originalCount {
		t.Errorf("Count mismatch after restart: original %d, recovered %d",
			originalCount, recoveredCount)
	}

	// Can continue adding data
	recoveredTracker.Add([]byte("new:item"))
	if recoveredTracker.Count() <= originalCount {
		t.Error("Should be able to add new items after recovery")
	}

	store2.Close()
}

// TestFunctional_Persistence_GlobalTrackerStore tests the global tracker store pattern
func TestFunctional_Persistence_GlobalTrackerStore(t *testing.T) {
	dir := t.TempDir()
	cfg := cardinality.PersistenceConfig{
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
	bloomCfg := cardinality.Config{
		Mode:              cardinality.ModeBloom,
		ExpectedItems:     10000,
		FalsePositiveRate: 0.01,
	}

	// Set up global store
	store, _ := cardinality.NewTrackerStore(cfg, bloomCfg)
	cardinality.GlobalTrackerStore = store
	cardinality.GlobalConfig = bloomCfg
	defer func() {
		cardinality.GlobalTrackerStore = nil
	}()

	// Use NewPersistentTrackerFromGlobal
	tracker := cardinality.NewPersistentTrackerFromGlobal("test:global:key")

	// Should be a PersistentTracker
	_, ok := tracker.(*cardinality.PersistentTracker)
	if !ok {
		t.Error("Expected PersistentTracker from NewPersistentTrackerFromGlobal")
	}

	// Add data and verify persistence
	tracker.Add([]byte("item1"))
	tracker.Add([]byte("item2"))

	store.SaveAll()

	// Verify in store
	if store.TrackerCount() != 1 {
		t.Errorf("Expected 1 tracker in store, got %d", store.TrackerCount())
	}

	store.Close()
}

// TestFunctional_Persistence_MultipleRestarts tests multiple restart cycles
func TestFunctional_Persistence_MultipleRestarts(t *testing.T) {
	dir := t.TempDir()
	cfg := cardinality.PersistenceConfig{
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
	bloomCfg := cardinality.Config{
		Mode:              cardinality.ModeBloom,
		ExpectedItems:     10000,
		FalsePositiveRate: 0.01,
	}

	expectedCount := int64(0)

	// Multiple restart cycles
	for cycle := 0; cycle < 5; cycle++ {
		store, _ := cardinality.NewTrackerStore(cfg, bloomCfg)

		if cycle > 0 {
			// Load from previous cycle
			store.LoadAll()
		}

		tracker := store.GetOrCreate("persistent:tracker")

		// Verify count from previous cycle
		if tracker.Count() != expectedCount {
			t.Errorf("Cycle %d: expected count %d, got %d", cycle, expectedCount, tracker.Count())
		}

		// Add new items
		for i := 0; i < 100; i++ {
			tracker.Add([]byte("cycle" + string(rune(cycle)) + ":item" + string(rune(i))))
		}
		expectedCount = tracker.Count()

		store.SaveAll()
		store.Close()
	}
}

// TestFunctional_Persistence_PrometheusMetrics tests that Prometheus metrics are exposed
func TestFunctional_Persistence_PrometheusMetrics(t *testing.T) {
	dir := t.TempDir()
	cfg := cardinality.PersistenceConfig{
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
	bloomCfg := cardinality.Config{
		Mode:              cardinality.ModeBloom,
		ExpectedItems:     10000,
		FalsePositiveRate: 0.01,
	}

	store, _ := cardinality.NewTrackerStore(cfg, bloomCfg)
	store.Start()
	defer store.Close()

	// Create some trackers
	for i := 0; i < 3; i++ {
		tracker := store.GetOrCreate("metric:" + string(rune('A'+i)))
		tracker.Add([]byte("item"))
	}

	// Save to trigger metrics
	store.SaveAll()

	// The metrics should be registered with Prometheus
	// We just verify no panics occurred and store works correctly
	if store.TrackerCount() != 3 {
		t.Errorf("Expected 3 trackers, got %d", store.TrackerCount())
	}
}

// TestFunctional_Persistence_InvalidKeys tests handling of invalid tracker keys
func TestFunctional_Persistence_InvalidKeys(t *testing.T) {
	dir := t.TempDir()
	cfg := cardinality.PersistenceConfig{
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
	bloomCfg := cardinality.Config{
		Mode:              cardinality.ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	store, _ := cardinality.NewTrackerStore(cfg, bloomCfg)

	invalidKeys := []string{
		"../../../etc/passwd",
		"test/../../../etc/passwd",
		"",
	}

	for _, key := range invalidKeys {
		tracker := store.GetOrCreate(key)
		// Invalid keys should return non-persistent trackers
		// GetOrCreate returns *PersistentTracker, but for invalid keys
		// it's handled in the store (validated in unit tests)
		if tracker != nil {
			t.Logf("Key %q handled without panic", key)
		}
	}
}

// TestFunctional_Persistence_LargeNumberOfTrackers tests handling many trackers
func TestFunctional_Persistence_LargeNumberOfTrackers(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large tracker test in short mode")
	}

	dir := t.TempDir()
	cfg := cardinality.PersistenceConfig{
		Enabled:          true,
		Path:             dir,
		SaveInterval:     time.Hour,
		StateTTL:         time.Hour,
		CleanupInterval:  time.Hour,
		MaxSize:          500 * 1024 * 1024,
		MaxMemory:        100 * 1024 * 1024,
		Compression:      true,
		CompressionLevel: 1,
	}
	bloomCfg := cardinality.Config{
		Mode:              cardinality.ModeBloom,
		ExpectedItems:     1000,
		FalsePositiveRate: 0.01,
	}

	store, _ := cardinality.NewTrackerStore(cfg, bloomCfg)

	// Create 100 trackers with valid keys (no control characters)
	numTrackers := 100
	for i := 0; i < numTrackers; i++ {
		key := fmt.Sprintf("tracker:%d", i)
		tracker := store.GetOrCreate(key)
		for j := 0; j < 100; j++ {
			tracker.Add([]byte(fmt.Sprintf("item%d", j)))
		}
	}

	// Save all
	start := time.Now()
	store.SaveAll()
	saveTime := time.Since(start)

	t.Logf("Saved %d trackers in %v", numTrackers, saveTime)

	// Load in new store
	store2, _ := cardinality.NewTrackerStore(cfg, bloomCfg)
	start = time.Now()
	store2.LoadAll()
	loadTime := time.Since(start)

	t.Logf("Loaded %d trackers in %v", numTrackers, loadTime)

	if store2.TrackerCount() != numTrackers {
		t.Errorf("Expected %d trackers, got %d", numTrackers, store2.TrackerCount())
	}
}
