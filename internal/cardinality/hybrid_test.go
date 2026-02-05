package cardinality

import (
	"fmt"
	"math"
	"sync"
	"testing"
)

func newTestHybridTracker(threshold int64) *HybridTracker {
	cfg := Config{
		ExpectedItems:     100000,
		FalsePositiveRate: 0.01,
		HLLThreshold:      threshold,
	}
	return NewHybridTracker(cfg, threshold)
}

func TestHybridTracker_StartsInBloomMode(t *testing.T) {
	tracker := newTestHybridTracker(1000)

	if tracker.Mode() != TrackerModeBloom {
		t.Errorf("Hybrid tracker should start in Bloom mode, got %s", tracker.Mode())
	}
}

func TestHybridTracker_BloomBehavior(t *testing.T) {
	tracker := newTestHybridTracker(10000) // High threshold to stay in Bloom

	// In Bloom mode, Add returns true for new, false for existing
	if !tracker.Add([]byte("key1")) {
		t.Error("Add should return true for new element in Bloom mode")
	}

	if tracker.Add([]byte("key1")) {
		t.Error("Add should return false for existing element in Bloom mode")
	}

	// TestOnly works in Bloom mode
	if !tracker.TestOnly([]byte("key1")) {
		t.Error("TestOnly should return true for existing element in Bloom mode")
	}

	if tracker.TestOnly([]byte("nonexistent")) {
		t.Error("TestOnly should return false for non-existent element in Bloom mode")
	}

	if tracker.Count() != 1 {
		t.Errorf("Count should be 1, got %d", tracker.Count())
	}
}

func TestHybridTracker_AutoSwitchAtThreshold(t *testing.T) {
	threshold := int64(100)
	tracker := newTestHybridTracker(threshold)

	var switchCallbackCalled bool
	var switchCardinality int64
	tracker.OnModeSwitch = func(previous, current TrackerMode, cardinalityAtSwitch int64) {
		switchCallbackCalled = true
		switchCardinality = cardinalityAtSwitch
		if previous != TrackerModeBloom {
			t.Errorf("Previous mode should be Bloom, got %s", previous)
		}
		if current != TrackerModeHLL {
			t.Errorf("Current mode should be HLL, got %s", current)
		}
	}

	// Add items up to threshold
	for i := int64(0); i < threshold; i++ {
		tracker.Add([]byte(fmt.Sprintf("key%d", i)))
	}

	// Should have switched to HLL
	if tracker.Mode() != TrackerModeHLL {
		t.Errorf("Should have switched to HLL after %d items, still in %s", threshold, tracker.Mode())
	}

	if !switchCallbackCalled {
		t.Error("Mode switch callback should have been called")
	}

	if switchCardinality < threshold-5 { // Allow small margin for Bloom FP
		t.Errorf("Cardinality at switch should be ~%d, got %d", threshold, switchCardinality)
	}

	if tracker.SwitchCount() != 1 {
		t.Errorf("Switch count should be 1, got %d", tracker.SwitchCount())
	}
}

func TestHybridTracker_HLLModeTestOnly(t *testing.T) {
	tracker := newTestHybridTracker(50)

	// Switch to HLL by adding items
	for i := 0; i < 100; i++ {
		tracker.Add([]byte(fmt.Sprintf("key%d", i)))
	}

	if tracker.Mode() != TrackerModeHLL {
		t.Fatal("Should be in HLL mode")
	}

	// TestOnly should always return false in HLL mode
	if tracker.TestOnly([]byte("key1")) {
		t.Error("TestOnly should return false in HLL mode")
	}
}

func TestHybridTracker_HLLModeAdd(t *testing.T) {
	tracker := newTestHybridTracker(50)

	// Switch to HLL
	for i := 0; i < 100; i++ {
		tracker.Add([]byte(fmt.Sprintf("key%d", i)))
	}

	if tracker.Mode() != TrackerModeHLL {
		t.Fatal("Should be in HLL mode")
	}

	// Add should always return true in HLL mode
	if !tracker.Add([]byte("new-key")) {
		t.Error("Add should always return true in HLL mode")
	}

	if !tracker.Add([]byte("key1")) {
		t.Error("Add should return true even for duplicates in HLL mode")
	}
}

func TestHybridTracker_Reset(t *testing.T) {
	tracker := newTestHybridTracker(50)

	// Switch to HLL
	for i := 0; i < 100; i++ {
		tracker.Add([]byte(fmt.Sprintf("key%d", i)))
	}

	if tracker.Mode() != TrackerModeHLL {
		t.Fatal("Should be in HLL mode before reset")
	}

	tracker.Reset()

	// Should be back in Bloom mode
	if tracker.Mode() != TrackerModeBloom {
		t.Errorf("Should be in Bloom mode after reset, got %s", tracker.Mode())
	}

	if tracker.Count() != 0 {
		t.Errorf("Count should be 0 after reset, got %d", tracker.Count())
	}

	// Bloom behavior should work again
	if !tracker.Add([]byte("key1")) {
		t.Error("Add should return true for new element after reset")
	}

	if tracker.Add([]byte("key1")) {
		t.Error("Add should return false for existing element after reset")
	}
}

func TestHybridTracker_MemoryUsage(t *testing.T) {
	tracker := newTestHybridTracker(50)

	bloomMem := tracker.MemoryUsage()
	if bloomMem == 0 {
		t.Error("Memory usage should be non-zero in Bloom mode")
	}

	// Switch to HLL
	for i := 0; i < 100; i++ {
		tracker.Add([]byte(fmt.Sprintf("key%d", i)))
	}

	hllMem := tracker.MemoryUsage()
	if hllMem != 12288 {
		t.Errorf("HLL memory should be 12288, got %d", hllMem)
	}
}

func TestHybridTracker_ShouldSample(t *testing.T) {
	tracker := newTestHybridTracker(100)

	// 100% sample rate should keep everything
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("key%d", i))
		if !tracker.ShouldSample(key, 1.0) {
			t.Error("100% sample rate should keep all items")
		}
	}

	// 0% sample rate should drop everything
	for i := 0; i < 100; i++ {
		key := []byte(fmt.Sprintf("key%d", i))
		if tracker.ShouldSample(key, 0.0) {
			t.Error("0% sample rate should drop all items")
		}
	}

	// ~20% sample rate should keep roughly 20% of items
	sampleRate := 0.20
	numKeys := 10000
	kept := 0
	for i := 0; i < numKeys; i++ {
		key := []byte(fmt.Sprintf("test-series-%d", i))
		if tracker.ShouldSample(key, sampleRate) {
			kept++
		}
	}

	actualRate := float64(kept) / float64(numKeys)
	deviation := math.Abs(actualRate - sampleRate)
	if deviation > 0.03 { // Allow 3% deviation
		t.Errorf("Sample rate %.2f produced %.2f%% kept (expected ~%.2f%%)",
			sampleRate, actualRate*100, sampleRate*100)
	}
	t.Logf("Sample rate %.2f: kept %d/%d = %.2f%%", sampleRate, kept, numKeys, actualRate*100)
}

func TestHybridTracker_ShouldSampleDeterministic(t *testing.T) {
	tracker := newTestHybridTracker(100)

	// Same key should always produce the same result
	key := []byte("deterministic-test-key")
	sampleRate := 0.5

	result := tracker.ShouldSample(key, sampleRate)
	for i := 0; i < 100; i++ {
		if tracker.ShouldSample(key, sampleRate) != result {
			t.Error("ShouldSample should be deterministic for the same key")
		}
	}
}

func TestHybridTracker_Concurrent(t *testing.T) {
	tracker := newTestHybridTracker(500) // Low threshold to trigger switch during test

	var wg sync.WaitGroup
	numGoroutines := 10
	keysPerGoroutine := 200 // Total: 2000, well above threshold

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < keysPerGoroutine; j++ {
				key := fmt.Sprintf("g%d-key%d", id, j)
				tracker.Add([]byte(key))
				tracker.TestOnly([]byte(key))
				tracker.Count()
				tracker.Mode()
				tracker.ShouldSample([]byte(key), 0.5)
			}
		}(i)
	}

	wg.Wait()

	// Should have switched to HLL since total > threshold
	if tracker.Mode() != TrackerModeHLL {
		t.Errorf("Should be in HLL mode after concurrent adds exceeding threshold, got %s", tracker.Mode())
	}

	count := tracker.Count()
	if count <= 0 {
		t.Error("Count should be positive after concurrent adds")
	}
}

func TestHybridTracker_CountAccuracyAfterSwitch(t *testing.T) {
	tracker := newTestHybridTracker(100)

	// Add 500 unique items - this will trigger switch at 100
	for i := 0; i < 500; i++ {
		tracker.Add([]byte(fmt.Sprintf("key%d", i)))
	}

	if tracker.Mode() != TrackerModeHLL {
		t.Fatal("Should be in HLL mode")
	}

	// HLL count won't match exactly because it started fresh after switch
	// But it should have counted at least items added after switch
	count := tracker.Count()
	// After switch at ~100, ~400 more items added to HLL
	// HLL should report at least 300 (generous margin since it starts fresh)
	if count < 300 {
		t.Errorf("HLL count %d seems too low after adding 400+ items post-switch", count)
	}
	t.Logf("HLL count after switch: %d (added ~400 items after switch at 100)", count)
}

func TestHybridTracker_NoSwitchWithZeroThreshold(t *testing.T) {
	tracker := newTestHybridTracker(0) // 0 = never switch

	for i := 0; i < 10000; i++ {
		tracker.Add([]byte(fmt.Sprintf("key%d", i)))
	}

	if tracker.Mode() != TrackerModeBloom {
		t.Error("Should remain in Bloom mode when threshold is 0")
	}
}

func TestTrackerMode_String(t *testing.T) {
	tests := []struct {
		mode     TrackerMode
		expected string
	}{
		{TrackerModeBloom, "bloom"},
		{TrackerModeHLL, "hll"},
		{TrackerMode(99), "unknown"},
	}

	for _, tt := range tests {
		if tt.mode.String() != tt.expected {
			t.Errorf("TrackerMode(%d).String() = %q, want %q", tt.mode, tt.mode.String(), tt.expected)
		}
	}
}
