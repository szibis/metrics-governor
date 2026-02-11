package queue

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ======================================================================
// SpilloverMode.String tests
// ======================================================================

func TestSpilloverMode_String_AllCases(t *testing.T) {
	tests := []struct {
		mode SpilloverMode
		want string
	}{
		{SpilloverMemoryOnly, "memory_only"},
		{SpilloverPartialDisk, "partial_disk"},
		{SpilloverAllDisk, "all_disk"},
		{SpilloverLoadShedding, "load_shedding"},
		{SpilloverMode(99), "unknown"},
	}
	for _, tt := range tests {
		got := tt.mode.String()
		if got != tt.want {
			t.Errorf("SpilloverMode(%d).String() = %q, want %q", tt.mode, got, tt.want)
		}
	}
}

// ======================================================================
// NewSpilloverState / Mode tests
// ======================================================================

func TestNewSpilloverState_InitialMode(t *testing.T) {
	s := NewSpilloverState()
	if s == nil {
		t.Fatal("expected non-nil SpilloverState")
	}
	if s.Mode() != SpilloverMemoryOnly {
		t.Errorf("expected initial mode SpilloverMemoryOnly, got %v", s.Mode())
	}
}

// ======================================================================
// SpilloverState.Evaluate tests
// ======================================================================

func TestSpilloverState_Evaluate_StaysMemoryOnly(t *testing.T) {
	s := NewSpilloverState()
	// Low utilization should stay memory-only
	mode := s.Evaluate(0.50, 80, 70)
	if mode != SpilloverMemoryOnly {
		t.Errorf("expected SpilloverMemoryOnly at 50%%, got %v", mode)
	}
}

func TestSpilloverState_Evaluate_EscalationPath(t *testing.T) {
	s := NewSpilloverState()

	// Escalate to PartialDisk at spillPct (80%)
	mode := s.Evaluate(0.80, 80, 70)
	if mode != SpilloverPartialDisk {
		t.Errorf("expected SpilloverPartialDisk at 80%%, got %v", mode)
	}

	// Escalate to AllDisk at spillPct+10% (90%)
	mode = s.Evaluate(0.90, 80, 70)
	if mode != SpilloverAllDisk {
		t.Errorf("expected SpilloverAllDisk at 90%%, got %v", mode)
	}

	// Escalate to LoadShedding at spillPct+15% (95%) — use 0.96 to avoid floating point edge
	mode = s.Evaluate(0.96, 80, 70)
	if mode != SpilloverLoadShedding {
		t.Errorf("expected SpilloverLoadShedding at 96%%, got %v", mode)
	}
}

func TestSpilloverState_Evaluate_ImmediateEscalationToLoadShedding(t *testing.T) {
	s := NewSpilloverState()
	// Jump directly from MemoryOnly to LoadShedding
	mode := s.Evaluate(0.96, 80, 70)
	if mode != SpilloverLoadShedding {
		t.Errorf("expected direct jump to SpilloverLoadShedding, got %v", mode)
	}
}

func TestSpilloverState_Evaluate_DeescalationStepByStep(t *testing.T) {
	s := NewSpilloverState()

	// Escalate to LoadShedding
	s.Evaluate(0.96, 80, 70)
	if s.Mode() != SpilloverLoadShedding {
		t.Fatalf("expected SpilloverLoadShedding, got %v", s.Mode())
	}

	// Drop to AllDisk threshold: should step down one level (LoadShedding -> AllDisk)
	mode := s.Evaluate(0.91, 80, 70)
	if mode != SpilloverAllDisk {
		t.Errorf("expected SpilloverAllDisk (step-down), got %v", mode)
	}

	// Drop to PartialDisk threshold: should step down one level (AllDisk -> PartialDisk)
	mode = s.Evaluate(0.81, 80, 70)
	if mode != SpilloverPartialDisk {
		t.Errorf("expected SpilloverPartialDisk (step-down), got %v", mode)
	}
}

func TestSpilloverState_Evaluate_FullRecoveryBelowHysteresis(t *testing.T) {
	s := NewSpilloverState()

	// Escalate to AllDisk
	s.Evaluate(0.91, 80, 70)
	if s.Mode() != SpilloverAllDisk {
		t.Fatalf("expected AllDisk, got %v", s.Mode())
	}

	// Drop below hysteresis threshold (70%) -> should recover to MemoryOnly
	mode := s.Evaluate(0.60, 80, 70)
	if mode != SpilloverMemoryOnly {
		t.Errorf("expected full recovery to SpilloverMemoryOnly, got %v", mode)
	}
}

func TestSpilloverState_Evaluate_HysteresisZoneStaysInCurrentMode(t *testing.T) {
	s := NewSpilloverState()

	// Escalate to PartialDisk
	s.Evaluate(0.82, 80, 70)
	if s.Mode() != SpilloverPartialDisk {
		t.Fatalf("expected PartialDisk, got %v", s.Mode())
	}

	// Utilization drops to hysteresis zone (between 70% and 80%)
	// Should stay in current mode (PartialDisk) due to hysteresis
	mode := s.Evaluate(0.75, 80, 70)
	if mode != SpilloverPartialDisk {
		t.Errorf("expected to stay in SpilloverPartialDisk in hysteresis zone, got %v", mode)
	}
}

func TestSpilloverState_Evaluate_RecoverFromPartialDiskToMemory(t *testing.T) {
	s := NewSpilloverState()

	// Escalate to PartialDisk
	s.Evaluate(0.82, 80, 70)
	if s.Mode() != SpilloverPartialDisk {
		t.Fatalf("expected PartialDisk, got %v", s.Mode())
	}

	// Drop below hysteresis -> full recovery to MemoryOnly
	mode := s.Evaluate(0.60, 80, 70)
	if mode != SpilloverMemoryOnly {
		t.Errorf("expected full recovery from PartialDisk, got %v", mode)
	}
}

func TestSpilloverState_Evaluate_MetricsTransitionsUpDown(t *testing.T) {
	s := NewSpilloverState()

	// Transition MemoryOnly -> PartialDisk should set spilloverActive
	s.Evaluate(0.82, 80, 70)
	if s.Mode() != SpilloverPartialDisk {
		t.Fatalf("expected PartialDisk, got %v", s.Mode())
	}

	// Transition back to MemoryOnly should record duration and clear spilloverActive
	s.Evaluate(0.50, 80, 70)
	if s.Mode() != SpilloverMemoryOnly {
		t.Fatalf("expected MemoryOnly after recovery, got %v", s.Mode())
	}
}

func TestSpilloverState_Evaluate_GradualStepDown(t *testing.T) {
	s := NewSpilloverState()

	// Go to LoadShedding
	s.Evaluate(0.96, 80, 70)

	// Step down to AllDisk
	s.Evaluate(0.92, 80, 70)
	if s.Mode() != SpilloverAllDisk {
		t.Fatalf("expected AllDisk, got %v", s.Mode())
	}

	// Step down to PartialDisk
	s.Evaluate(0.85, 80, 70)
	if s.Mode() != SpilloverPartialDisk {
		t.Fatalf("expected PartialDisk, got %v", s.Mode())
	}

	// In hysteresis zone (75%) — should stay PartialDisk
	s.Evaluate(0.75, 80, 70)
	if s.Mode() != SpilloverPartialDisk {
		t.Fatalf("expected PartialDisk in hysteresis zone, got %v", s.Mode())
	}

	// Below hysteresis (65%) — full recovery
	mode := s.Evaluate(0.65, 80, 70)
	if mode != SpilloverMemoryOnly {
		t.Errorf("expected MemoryOnly after full recovery, got %v", mode)
	}
}

// ======================================================================
// SpilloverState.ShouldSpillThisBatch tests
// ======================================================================

func TestSpilloverState_ShouldSpillThisBatch_MemoryOnlyNeverSpills(t *testing.T) {
	s := NewSpilloverState()
	if s.ShouldSpillThisBatch() {
		t.Error("expected no spill in MemoryOnly mode")
	}
}

func TestSpilloverState_ShouldSpillThisBatch_PartialDiskAlternates(t *testing.T) {
	s := NewSpilloverState()
	s.Evaluate(0.82, 80, 70) // PartialDisk

	got1 := s.ShouldSpillThisBatch()
	got2 := s.ShouldSpillThisBatch()
	got3 := s.ShouldSpillThisBatch()
	got4 := s.ShouldSpillThisBatch()

	// Should alternate between true and false
	if got1 == got2 {
		t.Errorf("expected alternating spill in PartialDisk, got %v, %v", got1, got2)
	}
	if got3 == got4 {
		t.Errorf("expected alternating spill in PartialDisk (second pair), got %v, %v", got3, got4)
	}
}

func TestSpilloverState_ShouldSpillThisBatch_AllDiskAlwaysSpills(t *testing.T) {
	s := NewSpilloverState()
	s.Evaluate(0.91, 80, 70) // AllDisk

	for i := 0; i < 5; i++ {
		if !s.ShouldSpillThisBatch() {
			t.Errorf("expected spill in AllDisk mode on iteration %d", i)
		}
	}
}

func TestSpilloverState_ShouldSpillThisBatch_LoadSheddingAlwaysSpills(t *testing.T) {
	s := NewSpilloverState()
	s.Evaluate(0.96, 80, 70) // LoadShedding

	for i := 0; i < 5; i++ {
		if !s.ShouldSpillThisBatch() {
			t.Errorf("expected spill in LoadShedding mode on iteration %d", i)
		}
	}
}

// ======================================================================
// setSpilloverModeGauge / IncrementSpilloverRateLimited / IncrementLoadShedding
// ======================================================================

func TestSetSpilloverModeGauge_AllModes(t *testing.T) {
	// Calling this should not panic for any mode
	setSpilloverModeGauge(SpilloverMemoryOnly)
	setSpilloverModeGauge(SpilloverPartialDisk)
	setSpilloverModeGauge(SpilloverAllDisk)
	setSpilloverModeGauge(SpilloverLoadShedding)
}

func TestIncrementSpilloverRateLimited_NoPanic(t *testing.T) {
	IncrementSpilloverRateLimited()
}

func TestIncrementLoadShedding_NoPanic(t *testing.T) {
	IncrementLoadShedding()
}

// ======================================================================
// MemoryBatchQueue Utilization edge cases
// ======================================================================

func TestMemoryBatchQueue_Utilization_CountOnlyNoBytesLimit(t *testing.T) {
	// No MaxBytes — only count-based utilization
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      10,
		FullBehavior: DropNewest,
	})
	defer q.Close()

	batch := makeTestBatch(1)
	_ = q.PushBatch(batch)

	util := q.Utilization()
	// count utilization = 1/10 = 0.1
	if util < 0.05 || util > 0.15 {
		t.Errorf("expected utilization ~0.1, got %f", util)
	}
}

func TestMemoryBatchQueue_Utilization_ByteHigherThanCount(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      1000,
		MaxBytes:     200,
		FullBehavior: DropNewest,
	})
	defer q.Close()

	b := makeTestBatch(1)
	b.EstimatedBytes = 150
	_ = q.PushBatch(b)

	util := q.Utilization()
	// byte utilization = 150/200 = 0.75, count = 1/1000 = 0.001
	if util < 0.7 || util > 0.8 {
		t.Errorf("expected utilization ~0.75, got %f", util)
	}
}

func TestMemoryBatchQueue_Utilization_CountHigherThanByte(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      2,
		MaxBytes:     100000,
		FullBehavior: DropNewest,
	})
	defer q.Close()

	b := makeTestBatch(1)
	b.EstimatedBytes = 10
	_ = q.PushBatch(b)

	util := q.Utilization()
	// count utilization = 1/2 = 0.5, byte utilization = 10/100000 ≈ 0
	if util < 0.4 || util > 0.6 {
		t.Errorf("expected utilization ~0.5, got %f", util)
	}
}

// ======================================================================
// MemoryBatchQueue — handleFull with bytes_limit for DropOldest
// ======================================================================

func TestMemoryBatchQueue_ByteLimit_DropOldestPath(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      100,
		MaxBytes:     200,
		FullBehavior: DropOldest,
	})
	defer q.Close()

	b1 := makeTestBatch(1)
	b1.EstimatedBytes = 150
	_ = q.PushBatch(b1)

	// This exceeds byte limit (150 + 100 = 250 > 200)
	b2 := makeTestBatch(1)
	b2.EstimatedBytes = 100
	err := q.PushBatch(b2)
	if err != nil {
		t.Errorf("expected successful push with byte-limit drop_oldest, got: %v", err)
	}
}

// ======================================================================
// MemoryBatchQueue — bytes_limit with Block timeout
// ======================================================================

func TestMemoryBatchQueue_ByteLimit_BlockTimeoutPath(t *testing.T) {
	q := NewMemoryBatchQueue(MemoryBatchQueueConfig{
		MaxSize:      1, // also limit channel size to 1
		MaxBytes:     100,
		FullBehavior: Block,
		BlockTimeout: 50 * time.Millisecond,
	})
	defer q.Close()

	// Fill both channel and byte limit
	b1 := makeTestBatch(1)
	b1.EstimatedBytes = 80
	_ = q.PushBatch(b1)

	// Channel is now full (size=1) AND byte limit near capacity
	// The next push triggers handleFullLocked via channel_full default case
	b2 := makeTestBatch(1)
	b2.EstimatedBytes = 80
	err := q.PushBatch(b2)
	if err != ErrBatchQueueFull {
		t.Errorf("expected ErrBatchQueueFull after block timeout, got: %v", err)
	}
}

// ======================================================================
// FastQueue — chunk rotation exercise
// ======================================================================

func TestFastQueue_ChunkRotationSmallChunks(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:              tmpDir,
		MaxInmemoryBlocks: 1,
		ChunkFileSize:     512, // Very small to trigger rotation
		MetaSyncInterval:  time.Hour,
		MaxSize:           1000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq.Close()

	data := make([]byte, 100)
	for i := range data {
		data[i] = byte(i)
	}

	for i := 0; i < 20; i++ {
		if err := fq.Push(data); err != nil {
			t.Fatalf("Failed to push %d: %v", i, err)
		}
	}

	for i := 0; i < 20; i++ {
		got, err := fq.Pop()
		if err != nil {
			t.Fatalf("Failed to pop %d: %v", i, err)
		}
		if len(got) != 100 {
			t.Errorf("Expected 100 bytes, got %d", len(got))
		}
	}
}

// ======================================================================
// FastQueue — cleanupOrphanedChunks exercise
// ======================================================================

func TestFastQueue_CleanupOrphanedChunksHighOffset(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:              tmpDir,
		MaxInmemoryBlocks: 1,
		ChunkFileSize:     1024 * 1024,
		MetaSyncInterval:  10 * time.Millisecond,
		MaxSize:           1000,
	}

	// Create a queue and push some data to generate metadata
	fq1, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	for i := 0; i < 5; i++ {
		if err := fq1.Push([]byte("test")); err != nil {
			t.Fatalf("Failed to push: %v", err)
		}
	}
	fq1.mu.Lock()
	fq1.flushInmemoryBlocksLocked()
	fq1.syncMetadataLocked()
	fq1.mu.Unlock()
	fq1.Close()

	// Now create an orphaned chunk file far beyond the valid range
	orphanedPath := filepath.Join(tmpDir, "0000000100000000")
	if err := os.WriteFile(orphanedPath, []byte("orphaned"), 0644); err != nil {
		t.Fatalf("Failed to create orphaned file: %v", err)
	}

	// Re-open. During recovery, cleanupOrphanedChunks should remove it.
	fq2, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}
	defer fq2.Close()

	if _, err := os.Stat(orphanedPath); !os.IsNotExist(err) {
		t.Error("Expected orphaned chunk to be cleaned up during recovery")
	}
}

// ======================================================================
// FastQueue — cross-chunk read/write
// ======================================================================

func TestFastQueue_CrossChunkReadWriteSmall(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:              tmpDir,
		MaxInmemoryBlocks: 1,
		ChunkFileSize:     256, // Very small
		MetaSyncInterval:  10 * time.Millisecond,
		MaxSize:           1000,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	for i := 0; i < 10; i++ {
		data := make([]byte, 50)
		data[0] = byte(i)
		if err := fq.Push(data); err != nil {
			t.Fatalf("Failed to push %d: %v", i, err)
		}
	}

	fq.mu.Lock()
	fq.flushInmemoryBlocksLocked()
	fq.syncMetadataLocked()
	fq.mu.Unlock()

	for i := 0; i < 10; i++ {
		data, err := fq.Pop()
		if err != nil {
			t.Fatalf("Failed to pop %d: %v", i, err)
		}
		if data[0] != byte(i) {
			t.Errorf("Expected data[0]=%d, got %d", i, data[0])
		}
	}

	fq.Close()
}

// ======================================================================
// FastQueue — recovery with corrupt metadata
// ======================================================================

func TestFastQueue_RecoverCorruptMetadataJSON(t *testing.T) {
	tmpDir := t.TempDir()

	metaPath := filepath.Join(tmpDir, metaFileName)
	if err := os.WriteFile(metaPath, []byte("not-json"), 0600); err != nil {
		t.Fatalf("Failed to write corrupt metadata: %v", err)
	}

	cfg := FastQueueConfig{
		Path:    tmpDir,
		MaxSize: 100,
	}

	_, err := NewFastQueue(cfg)
	if err == nil {
		t.Error("Expected error when recovering with corrupt metadata")
	}
}

// ======================================================================
// FastQueue — recovery with unreadable metadata (directory instead of file)
// ======================================================================

func TestFastQueue_RecoverUnreadableMetadataDir(t *testing.T) {
	tmpDir := t.TempDir()
	metaPath := filepath.Join(tmpDir, metaFileName)

	if err := os.MkdirAll(metaPath, 0755); err != nil {
		t.Fatalf("Failed to create dir: %v", err)
	}

	cfg := FastQueueConfig{
		Path:    tmpDir,
		MaxSize: 100,
	}

	_, err := NewFastQueue(cfg)
	if err == nil {
		t.Error("Expected error when metadata is a directory")
	}
}

// ======================================================================
// FastQueue — write to read-only dir (permission error path)
// ======================================================================

func TestFastQueue_WriteToReadOnlyDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := FastQueueConfig{
		Path:              tmpDir,
		MaxInmemoryBlocks: 1,
		ChunkFileSize:     1024 * 1024,
		MetaSyncInterval:  time.Hour,
		MaxSize:           100,
	}

	fq, err := NewFastQueue(cfg)
	if err != nil {
		t.Fatalf("Failed to create FastQueue: %v", err)
	}

	if err := fq.Push([]byte("test")); err != nil {
		t.Fatalf("Failed to first push: %v", err)
	}

	os.Chmod(tmpDir, 0555)
	defer os.Chmod(tmpDir, 0755)

	// Push another item to trigger disk flush — may fail with permission error
	_ = fq.Push([]byte("test2"))

	os.Chmod(tmpDir, 0755)
	fq.Close()
}

// ======================================================================
// QueueMode.String — the function at 0% coverage (distinct from QueueMode_String test in batchqueue_test.go)
// ======================================================================

func TestQueueMode_StringDescription(t *testing.T) {
	// This tests the String() method which has 0% coverage
	tests := []struct {
		mode QueueMode
		want string
	}{
		{QueueModeMemory, "memory (zero-serialization, no persistence)"},
		{QueueModeDisk, "disk (serialized, full persistence)"},
		{QueueModeHybrid, "hybrid (memory L1 + disk L2 spillover)"},
		{QueueMode("custom"), "custom"},
	}
	for _, tt := range tests {
		got := tt.mode.String()
		if got != tt.want {
			t.Errorf("QueueMode(%q).String() = %q, want %q", tt.mode, got, tt.want)
		}
	}
}
