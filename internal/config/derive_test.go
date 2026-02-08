package config

import (
	"fmt"
	"runtime"
	"testing"
)

func TestDetectResources(t *testing.T) {
	r := DetectResources()

	if r.CPUCount < 1 {
		t.Errorf("expected CPUCount >= 1, got %d", r.CPUCount)
	}
	if r.CPUCount != runtime.NumCPU() {
		t.Errorf("expected CPUCount = runtime.NumCPU() (%d), got %d", runtime.NumCPU(), r.CPUCount)
	}
	if r.Source != "system" {
		t.Errorf("expected Source \"system\", got %q", r.Source)
	}
}

func TestAutoDerive_AllDefaults(t *testing.T) {
	// With default config (all zero sentinels) and 4 CPUs / 1GB memory,
	// AutoDerive should fill in all derivable fields.
	cfg := DefaultConfig()
	resources := ResourceInfo{
		CPUCount:    4,
		MemoryBytes: 1073741824, // 1 GB
		Source:      "system",
	}
	explicitFields := map[string]bool{}

	derived := AutoDerive(cfg, resources, explicitFields)

	// Verify config was mutated correctly.
	if cfg.QueueWorkers != 4 {
		t.Errorf("QueueWorkers: got %d, want 4", cfg.QueueWorkers)
	}
	if cfg.ExportConcurrency != 16 {
		t.Errorf("ExportConcurrency: got %d, want 16 (4*4)", cfg.ExportConcurrency)
	}
	if cfg.QueueGlobalSendLimit != 32 {
		t.Errorf("QueueGlobalSendLimit: got %d, want 32 (4*8)", cfg.QueueGlobalSendLimit)
	}

	// PipelineSplit is disabled by default, so preparer/sender should not be derived.
	if cfg.QueuePreparerCount != 0 {
		t.Errorf("QueuePreparerCount should remain 0 when pipeline split disabled, got %d", cfg.QueuePreparerCount)
	}
	if cfg.QueueSenderCount != 0 {
		t.Errorf("QueueSenderCount should remain 0 when pipeline split disabled, got %d", cfg.QueueSenderCount)
	}

	// Memory-based: QueueMemoryPercent is 0.10 by default, so QueueMaxBytes = 1GB * 0.10 = ~107MB
	mem := float64(resources.MemoryBytes)
	expectedQueueMaxBytes := int64(mem * 0.10)
	if cfg.QueueMaxBytes != expectedQueueMaxBytes {
		t.Errorf("QueueMaxBytes: got %d, want %d", cfg.QueueMaxBytes, expectedQueueMaxBytes)
	}

	// MaxBatchBytes: min(QueueMaxBytes/4, 8MB)
	// QueueMaxBytes/4 = 107374182/4 = 26843545, which is > 8MB (8388608).
	// So per the code: quarter > 0 && quarter < limit must be true.
	// 26843545 > 8388608, so the condition quarter < limit is FALSE.
	// MaxBatchBytes should NOT be changed from default (8388608).
	if cfg.MaxBatchBytes != 8388608 {
		t.Errorf("MaxBatchBytes: got %d, want 8388608 (default unchanged)", cfg.MaxBatchBytes)
	}

	// Verify derived values list covers the fields that were set.
	derivedFields := map[string]bool{}
	for _, d := range derived {
		derivedFields[d.Field] = true
	}
	expectedDerived := []string{
		"queue-workers",
		"export-concurrency",
		"queue.global_send_limit",
		"queue-max-bytes",
	}
	for _, f := range expectedDerived {
		if !derivedFields[f] {
			t.Errorf("expected field %q in derived list, not found", f)
		}
	}
	// Pipeline split fields should NOT appear.
	if derivedFields["queue.pipeline_split.preparer_count"] {
		t.Error("preparer_count should not be in derived list when pipeline split disabled")
	}
	if derivedFields["queue.pipeline_split.sender_count"] {
		t.Error("sender_count should not be in derived list when pipeline split disabled")
	}
}

func TestAutoDerive_PipelineSplitEnabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueuePipelineSplitEnabled = true
	resources := ResourceInfo{
		CPUCount:    4,
		MemoryBytes: 1073741824,
		Source:      "system",
	}
	explicitFields := map[string]bool{}

	derived := AutoDerive(cfg, resources, explicitFields)

	if cfg.QueuePreparerCount != 4 {
		t.Errorf("QueuePreparerCount: got %d, want 4 (NumCPU)", cfg.QueuePreparerCount)
	}
	if cfg.QueueSenderCount != 8 {
		t.Errorf("QueueSenderCount: got %d, want 8 (NumCPU*2)", cfg.QueueSenderCount)
	}

	derivedFields := map[string]bool{}
	for _, d := range derived {
		derivedFields[d.Field] = true
	}
	if !derivedFields["queue.pipeline_split.preparer_count"] {
		t.Error("expected preparer_count in derived list")
	}
	if !derivedFields["queue.pipeline_split.sender_count"] {
		t.Error("expected sender_count in derived list")
	}
}

func TestAutoDerive_ExplicitFieldsSkipped(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueuePipelineSplitEnabled = true
	resources := ResourceInfo{
		CPUCount:    4,
		MemoryBytes: 1073741824,
		Source:      "system",
	}

	// Mark every derivable field as explicit: none should be changed.
	explicitFields := map[string]bool{
		"queue-workers":                       true,
		"export-concurrency":                  true,
		"queue.pipeline_split.preparer_count": true,
		"queue.pipeline_split.sender_count":   true,
		"queue.global_send_limit":             true,
		"queue-max-bytes":                     true,
		"max-batch-bytes":                     true,
	}

	// Save original values.
	origQueueWorkers := cfg.QueueWorkers
	origExportConcurrency := cfg.ExportConcurrency
	origPreparerCount := cfg.QueuePreparerCount
	origSenderCount := cfg.QueueSenderCount
	origGlobalSendLimit := cfg.QueueGlobalSendLimit
	origQueueMaxBytes := cfg.QueueMaxBytes
	origMaxBatchBytes := cfg.MaxBatchBytes

	derived := AutoDerive(cfg, resources, explicitFields)

	if len(derived) != 0 {
		t.Errorf("expected no derivations when all fields explicit, got %d: %v", len(derived), derived)
	}
	if cfg.QueueWorkers != origQueueWorkers {
		t.Errorf("QueueWorkers changed from %d to %d", origQueueWorkers, cfg.QueueWorkers)
	}
	if cfg.ExportConcurrency != origExportConcurrency {
		t.Errorf("ExportConcurrency changed from %d to %d", origExportConcurrency, cfg.ExportConcurrency)
	}
	if cfg.QueuePreparerCount != origPreparerCount {
		t.Errorf("QueuePreparerCount changed from %d to %d", origPreparerCount, cfg.QueuePreparerCount)
	}
	if cfg.QueueSenderCount != origSenderCount {
		t.Errorf("QueueSenderCount changed from %d to %d", origSenderCount, cfg.QueueSenderCount)
	}
	if cfg.QueueGlobalSendLimit != origGlobalSendLimit {
		t.Errorf("QueueGlobalSendLimit changed from %d to %d", origGlobalSendLimit, cfg.QueueGlobalSendLimit)
	}
	if cfg.QueueMaxBytes != origQueueMaxBytes {
		t.Errorf("QueueMaxBytes changed from %d to %d", origQueueMaxBytes, cfg.QueueMaxBytes)
	}
	if cfg.MaxBatchBytes != origMaxBatchBytes {
		t.Errorf("MaxBatchBytes changed from %d to %d", origMaxBatchBytes, cfg.MaxBatchBytes)
	}
}

func TestAutoDerive_SingleExplicitField(t *testing.T) {
	// Only queue-workers is explicit; other fields should still be derived.
	cfg := DefaultConfig()
	cfg.QueueWorkers = 8 // user explicitly set this
	resources := ResourceInfo{
		CPUCount:    4,
		MemoryBytes: 1073741824,
		Source:      "system",
	}
	explicitFields := map[string]bool{
		"queue-workers": true,
	}

	derived := AutoDerive(cfg, resources, explicitFields)

	// QueueWorkers must remain at user-set value.
	if cfg.QueueWorkers != 8 {
		t.Errorf("QueueWorkers: got %d, want 8 (user-set)", cfg.QueueWorkers)
	}

	// Other fields should be derived normally.
	if cfg.ExportConcurrency != 16 {
		t.Errorf("ExportConcurrency: got %d, want 16", cfg.ExportConcurrency)
	}
	if cfg.QueueGlobalSendLimit != 32 {
		t.Errorf("QueueGlobalSendLimit: got %d, want 32", cfg.QueueGlobalSendLimit)
	}

	// queue-workers should NOT appear in derived list.
	for _, d := range derived {
		if d.Field == "queue-workers" {
			t.Error("queue-workers should not appear in derived list when explicit")
		}
	}
	// export-concurrency should appear.
	found := false
	for _, d := range derived {
		if d.Field == "export-concurrency" {
			found = true
			break
		}
	}
	if !found {
		t.Error("export-concurrency should appear in derived list")
	}
}

func TestAutoDerive_CPUZeroFloor(t *testing.T) {
	// CPUCount=0 should be treated as 1.
	cfg := DefaultConfig()
	resources := ResourceInfo{
		CPUCount:    0,
		MemoryBytes: 0,
		Source:      "none",
	}
	explicitFields := map[string]bool{}

	AutoDerive(cfg, resources, explicitFields)

	if cfg.QueueWorkers != 1 {
		t.Errorf("QueueWorkers: got %d, want 1 (floor for 0 CPUs)", cfg.QueueWorkers)
	}
	if cfg.ExportConcurrency != 4 {
		t.Errorf("ExportConcurrency: got %d, want 4 (1*4)", cfg.ExportConcurrency)
	}
	if cfg.QueueGlobalSendLimit != 8 {
		t.Errorf("QueueGlobalSendLimit: got %d, want 8 (1*8)", cfg.QueueGlobalSendLimit)
	}
}

func TestAutoDerive_NegativeCPUFloor(t *testing.T) {
	// Negative CPUCount should also floor to 1.
	cfg := DefaultConfig()
	resources := ResourceInfo{
		CPUCount:    -2,
		MemoryBytes: 0,
		Source:      "none",
	}
	explicitFields := map[string]bool{}

	AutoDerive(cfg, resources, explicitFields)

	if cfg.QueueWorkers != 1 {
		t.Errorf("QueueWorkers: got %d, want 1 (floor for negative CPUs)", cfg.QueueWorkers)
	}
	if cfg.ExportConcurrency != 4 {
		t.Errorf("ExportConcurrency: got %d, want 4 (1*4)", cfg.ExportConcurrency)
	}
}

func TestAutoDerive_NoMemoryDerivation(t *testing.T) {
	// When MemoryBytes=0, memory-based fields should not be derived.
	cfg := DefaultConfig()
	origQueueMaxBytes := cfg.QueueMaxBytes
	origMaxBatchBytes := cfg.MaxBatchBytes
	resources := ResourceInfo{
		CPUCount:    4,
		MemoryBytes: 0,
		Source:      "none",
	}
	explicitFields := map[string]bool{}

	derived := AutoDerive(cfg, resources, explicitFields)

	if cfg.QueueMaxBytes != origQueueMaxBytes {
		t.Errorf("QueueMaxBytes should not change with 0 memory, got %d", cfg.QueueMaxBytes)
	}
	if cfg.MaxBatchBytes != origMaxBatchBytes {
		t.Errorf("MaxBatchBytes should not change with 0 memory, got %d", cfg.MaxBatchBytes)
	}

	for _, d := range derived {
		if d.Field == "queue-max-bytes" || d.Field == "max-batch-bytes" {
			t.Errorf("memory-derived field %q should not appear when MemoryBytes=0", d.Field)
		}
	}
}

func TestAutoDerive_NonZeroFieldsNotOverwritten(t *testing.T) {
	// When config fields are already non-zero, they should not be overwritten.
	cfg := DefaultConfig()
	cfg.QueueWorkers = 10
	cfg.ExportConcurrency = 20
	cfg.QueueGlobalSendLimit = 50
	resources := ResourceInfo{
		CPUCount:    4,
		MemoryBytes: 0,
		Source:      "system",
	}
	explicitFields := map[string]bool{}

	derived := AutoDerive(cfg, resources, explicitFields)

	if cfg.QueueWorkers != 10 {
		t.Errorf("QueueWorkers: got %d, want 10 (pre-set)", cfg.QueueWorkers)
	}
	if cfg.ExportConcurrency != 20 {
		t.Errorf("ExportConcurrency: got %d, want 20 (pre-set)", cfg.ExportConcurrency)
	}
	if cfg.QueueGlobalSendLimit != 50 {
		t.Errorf("QueueGlobalSendLimit: got %d, want 50 (pre-set)", cfg.QueueGlobalSendLimit)
	}

	for _, d := range derived {
		if d.Field == "queue-workers" || d.Field == "export-concurrency" || d.Field == "queue.global_send_limit" {
			t.Errorf("field %q should not be derived when already non-zero", d.Field)
		}
	}
}

func TestAutoDerive_MaxBatchBytesSmallQueue(t *testing.T) {
	// When QueueMaxBytes is small enough that QueueMaxBytes/4 < 8MB,
	// MaxBatchBytes should be set to QueueMaxBytes/4.
	cfg := DefaultConfig()
	cfg.QueueMemoryPercent = 0.01 // 1% of memory
	resources := ResourceInfo{
		CPUCount:    4,
		MemoryBytes: 1073741824, // 1 GB
		Source:      "system",
	}
	explicitFields := map[string]bool{}

	AutoDerive(cfg, resources, explicitFields)

	// QueueMaxBytes = 1GB * 0.01 = 10737418 bytes (~10.2 MB)
	mem := float64(resources.MemoryBytes)
	expectedQueueMaxBytes := int64(mem * 0.01)
	if cfg.QueueMaxBytes != expectedQueueMaxBytes {
		t.Errorf("QueueMaxBytes: got %d, want %d", cfg.QueueMaxBytes, expectedQueueMaxBytes)
	}

	// MaxBatchBytes = QueueMaxBytes/4 = 10737418/4 = 2684354 (~2.6 MB), which is < 8MB.
	expectedMaxBatchBytes := int(expectedQueueMaxBytes / 4)
	if cfg.MaxBatchBytes != expectedMaxBatchBytes {
		t.Errorf("MaxBatchBytes: got %d, want %d (QueueMaxBytes/4)", cfg.MaxBatchBytes, expectedMaxBatchBytes)
	}
}

func TestAutoDerive_DerivedValueFormulas(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueuePipelineSplitEnabled = true
	cfg.QueueMemoryPercent = 0.005 // very small so MaxBatchBytes gets derived
	resources := ResourceInfo{
		CPUCount:    2,
		MemoryBytes: 1073741824,
		Source:      "system",
	}
	explicitFields := map[string]bool{}

	derived := AutoDerive(cfg, resources, explicitFields)

	expectedFormulas := map[string]string{
		"queue-workers":                       "NumCPU",
		"export-concurrency":                  "NumCPU * 4",
		"queue.pipeline_split.preparer_count": "NumCPU",
		"queue.pipeline_split.sender_count":   "NumCPU * 2",
		"queue.global_send_limit":             "NumCPU * 8",
	}

	derivedMap := map[string]DerivedValue{}
	for _, d := range derived {
		derivedMap[d.Field] = d
	}

	for field, expectedFormula := range expectedFormulas {
		d, ok := derivedMap[field]
		if !ok {
			t.Errorf("expected field %q in derived list", field)
			continue
		}
		if d.Formula != expectedFormula {
			t.Errorf("field %q: formula got %q, want %q", field, d.Formula, expectedFormula)
		}
	}
}

func TestAutoDerive_DerivedValueValues(t *testing.T) {
	cfg := DefaultConfig()
	resources := ResourceInfo{
		CPUCount:    8,
		MemoryBytes: 0,
		Source:      "system",
	}
	explicitFields := map[string]bool{}

	derived := AutoDerive(cfg, resources, explicitFields)

	expectedValues := map[string]string{
		"queue-workers":           "8",
		"export-concurrency":      "32",
		"queue.global_send_limit": "64",
	}

	derivedMap := map[string]DerivedValue{}
	for _, d := range derived {
		derivedMap[d.Field] = d
	}

	for field, expectedValue := range expectedValues {
		d, ok := derivedMap[field]
		if !ok {
			t.Errorf("expected field %q in derived list", field)
			continue
		}
		if d.Value != expectedValue {
			t.Errorf("field %q: value got %q, want %q", field, d.Value, expectedValue)
		}
	}
}

func TestAutoDerive_QueueMaxBytesFormula(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueueMemoryPercent = 0.25
	resources := ResourceInfo{
		CPUCount:    4,
		MemoryBytes: 4294967296, // 4 GB
		Source:      "system",
	}
	explicitFields := map[string]bool{}

	derived := AutoDerive(cfg, resources, explicitFields)

	derivedMap := map[string]DerivedValue{}
	for _, d := range derived {
		derivedMap[d.Field] = d
	}

	d, ok := derivedMap["queue-max-bytes"]
	if !ok {
		t.Fatal("expected queue-max-bytes in derived list")
	}
	if d.Formula != "MemoryLimit * 25%" {
		t.Errorf("queue-max-bytes formula: got %q, want %q", d.Formula, "MemoryLimit * 25%")
	}

	// QueueMaxBytes = 4GB * 0.25 = 1GB
	if cfg.QueueMaxBytes != 1073741824 {
		t.Errorf("QueueMaxBytes: got %d, want 1073741824", cfg.QueueMaxBytes)
	}
	if d.Value != "1Gi" {
		t.Errorf("queue-max-bytes value: got %q, want \"1Gi\"", d.Value)
	}
}

func TestAutoDerive_ZeroMemoryPercent(t *testing.T) {
	// When QueueMemoryPercent is 0, memory-based queue sizing should not apply.
	cfg := DefaultConfig()
	cfg.QueueMemoryPercent = 0
	origQueueMaxBytes := cfg.QueueMaxBytes
	resources := ResourceInfo{
		CPUCount:    4,
		MemoryBytes: 1073741824,
		Source:      "system",
	}
	explicitFields := map[string]bool{}

	derived := AutoDerive(cfg, resources, explicitFields)

	if cfg.QueueMaxBytes != origQueueMaxBytes {
		t.Errorf("QueueMaxBytes should not change with 0 memory percent, got %d", cfg.QueueMaxBytes)
	}

	for _, d := range derived {
		if d.Field == "queue-max-bytes" {
			t.Error("queue-max-bytes should not be derived when QueueMemoryPercent is 0")
		}
	}
}

func TestAutoDerive_TableDriven(t *testing.T) {
	tests := []struct {
		name              string
		cpuCount          int
		memoryBytes       int64
		pipelineSplit     bool
		memoryPercent     float64
		explicitFields    map[string]bool
		wantWorkers       int
		wantConcurrency   int
		wantGlobalLimit   int
		wantPreparerCount int
		wantSenderCount   int
		wantDerivedCount  int // minimum number of derived fields
	}{
		{
			name:             "1 CPU, no memory",
			cpuCount:         1,
			memoryBytes:      0,
			pipelineSplit:    false,
			memoryPercent:    0.10,
			explicitFields:   map[string]bool{},
			wantWorkers:      1,
			wantConcurrency:  4,
			wantGlobalLimit:  8,
			wantDerivedCount: 3,
		},
		{
			name:              "16 CPUs, pipeline split, 2GB",
			cpuCount:          16,
			memoryBytes:       2147483648,
			pipelineSplit:     true,
			memoryPercent:     0.10,
			explicitFields:    map[string]bool{},
			wantWorkers:       16,
			wantConcurrency:   64,
			wantGlobalLimit:   128,
			wantPreparerCount: 16,
			wantSenderCount:   32,
			wantDerivedCount:  6,
		},
		{
			name:          "all explicit, nothing derived",
			cpuCount:      4,
			memoryBytes:   1073741824,
			pipelineSplit: true,
			memoryPercent: 0.10,
			explicitFields: map[string]bool{
				"queue-workers":                       true,
				"export-concurrency":                  true,
				"queue.pipeline_split.preparer_count": true,
				"queue.pipeline_split.sender_count":   true,
				"queue.global_send_limit":             true,
				"queue-max-bytes":                     true,
				"max-batch-bytes":                     true,
			},
			wantWorkers:       0,
			wantConcurrency:   0,
			wantGlobalLimit:   0,
			wantPreparerCount: 0,
			wantSenderCount:   0,
			wantDerivedCount:  0,
		},
		{
			name:             "CPU floor from 0 to 1",
			cpuCount:         0,
			memoryBytes:      0,
			pipelineSplit:    false,
			memoryPercent:    0.10,
			explicitFields:   map[string]bool{},
			wantWorkers:      1,
			wantConcurrency:  4,
			wantGlobalLimit:  8,
			wantDerivedCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.QueuePipelineSplitEnabled = tt.pipelineSplit
			cfg.QueueMemoryPercent = tt.memoryPercent
			resources := ResourceInfo{
				CPUCount:    tt.cpuCount,
				MemoryBytes: tt.memoryBytes,
				Source:      "system",
			}

			derived := AutoDerive(cfg, resources, tt.explicitFields)

			if tt.explicitFields["queue-workers"] {
				// Should remain at default 0.
				if cfg.QueueWorkers != 0 {
					t.Errorf("QueueWorkers: got %d, want 0 (explicit)", cfg.QueueWorkers)
				}
			} else {
				if cfg.QueueWorkers != tt.wantWorkers {
					t.Errorf("QueueWorkers: got %d, want %d", cfg.QueueWorkers, tt.wantWorkers)
				}
			}

			if tt.explicitFields["export-concurrency"] {
				if cfg.ExportConcurrency != 0 {
					t.Errorf("ExportConcurrency: got %d, want 0 (explicit)", cfg.ExportConcurrency)
				}
			} else {
				if cfg.ExportConcurrency != tt.wantConcurrency {
					t.Errorf("ExportConcurrency: got %d, want %d", cfg.ExportConcurrency, tt.wantConcurrency)
				}
			}

			if tt.explicitFields["queue.global_send_limit"] {
				if cfg.QueueGlobalSendLimit != 0 {
					t.Errorf("QueueGlobalSendLimit: got %d, want 0 (explicit)", cfg.QueueGlobalSendLimit)
				}
			} else {
				if cfg.QueueGlobalSendLimit != tt.wantGlobalLimit {
					t.Errorf("QueueGlobalSendLimit: got %d, want %d", cfg.QueueGlobalSendLimit, tt.wantGlobalLimit)
				}
			}

			if tt.pipelineSplit {
				if tt.explicitFields["queue.pipeline_split.preparer_count"] {
					if cfg.QueuePreparerCount != 0 {
						t.Errorf("QueuePreparerCount: got %d, want 0 (explicit)", cfg.QueuePreparerCount)
					}
				} else {
					if cfg.QueuePreparerCount != tt.wantPreparerCount {
						t.Errorf("QueuePreparerCount: got %d, want %d", cfg.QueuePreparerCount, tt.wantPreparerCount)
					}
				}

				if tt.explicitFields["queue.pipeline_split.sender_count"] {
					if cfg.QueueSenderCount != 0 {
						t.Errorf("QueueSenderCount: got %d, want 0 (explicit)", cfg.QueueSenderCount)
					}
				} else {
					if cfg.QueueSenderCount != tt.wantSenderCount {
						t.Errorf("QueueSenderCount: got %d, want %d", cfg.QueueSenderCount, tt.wantSenderCount)
					}
				}
			}

			if len(derived) < tt.wantDerivedCount {
				t.Errorf("derived count: got %d, want >= %d", len(derived), tt.wantDerivedCount)
			}
		})
	}
}

func TestAutoDerive_MaxBatchBytesDerived(t *testing.T) {
	// Set up a scenario where QueueMaxBytes/4 < 8MB so MaxBatchBytes IS derived.
	cfg := DefaultConfig()
	cfg.QueueMemoryPercent = 0.002 // very small: 0.2% of 1GB = ~2MB
	resources := ResourceInfo{
		CPUCount:    2,
		MemoryBytes: 1073741824, // 1GB
		Source:      "system",
	}
	explicitFields := map[string]bool{}

	derived := AutoDerive(cfg, resources, explicitFields)

	// QueueMaxBytes = 1GB * 0.002 = 2147483 bytes
	mem := float64(resources.MemoryBytes)
	expectedQueueMaxBytes := int64(mem * 0.002)
	if cfg.QueueMaxBytes != expectedQueueMaxBytes {
		t.Errorf("QueueMaxBytes: got %d, want %d", cfg.QueueMaxBytes, expectedQueueMaxBytes)
	}

	// MaxBatchBytes = min(2147483/4, 8388608) = 536870
	quarter := expectedQueueMaxBytes / 4
	if quarter >= 8388608 {
		t.Fatal("test setup error: quarter should be < 8MB")
	}
	if cfg.MaxBatchBytes != int(quarter) {
		t.Errorf("MaxBatchBytes: got %d, want %d", cfg.MaxBatchBytes, quarter)
	}

	// Verify it appears in the derived list with correct formula.
	found := false
	for _, d := range derived {
		if d.Field == "max-batch-bytes" {
			found = true
			if d.Formula != "min(QueueMaxBytes/4, 8MB)" {
				t.Errorf("max-batch-bytes formula: got %q, want %q", d.Formula, "min(QueueMaxBytes/4, 8MB)")
			}
			break
		}
	}
	if !found {
		t.Error("max-batch-bytes should appear in derived list")
	}
}

func TestAutoDerive_MaxBatchBytesExplicit(t *testing.T) {
	// When max-batch-bytes is explicit, it should not be derived even if conditions are met.
	cfg := DefaultConfig()
	cfg.QueueMemoryPercent = 0.002
	cfg.MaxBatchBytes = 999999
	resources := ResourceInfo{
		CPUCount:    2,
		MemoryBytes: 1073741824,
		Source:      "system",
	}
	explicitFields := map[string]bool{
		"max-batch-bytes": true,
	}

	derived := AutoDerive(cfg, resources, explicitFields)

	if cfg.MaxBatchBytes != 999999 {
		t.Errorf("MaxBatchBytes should remain at explicit value 999999, got %d", cfg.MaxBatchBytes)
	}

	for _, d := range derived {
		if d.Field == "max-batch-bytes" {
			t.Error("max-batch-bytes should not appear in derived list when explicit")
		}
	}
}

func TestAutoDerive_HighCPUCount(t *testing.T) {
	// Verify derivation with high CPU counts.
	cfg := DefaultConfig()
	resources := ResourceInfo{
		CPUCount:    128,
		MemoryBytes: 0,
		Source:      "system",
	}
	explicitFields := map[string]bool{}

	AutoDerive(cfg, resources, explicitFields)

	if cfg.QueueWorkers != 128 {
		t.Errorf("QueueWorkers: got %d, want 128", cfg.QueueWorkers)
	}
	if cfg.ExportConcurrency != 512 {
		t.Errorf("ExportConcurrency: got %d, want 512 (128*4)", cfg.ExportConcurrency)
	}
	if cfg.QueueGlobalSendLimit != 1024 {
		t.Errorf("QueueGlobalSendLimit: got %d, want 1024 (128*8)", cfg.QueueGlobalSendLimit)
	}
}

func TestAutoDerive_MemoryFormattedValues(t *testing.T) {
	// Verify that DerivedValue.Value uses FormatByteSize formatting.
	tests := []struct {
		name          string
		memoryBytes   int64
		memoryPercent float64
		wantValue     string
	}{
		{
			name:          "1GB * 10% = 100Mi-ish",
			memoryBytes:   1073741824,
			memoryPercent: 0.10,
			wantValue:     func() string { m := int64(1073741824); return FormatByteSize(int64(float64(m) * 0.10)) }(),
		},
		{
			name:          "4GB * 25% = 1Gi",
			memoryBytes:   4294967296,
			memoryPercent: 0.25,
			wantValue:     "1Gi",
		},
		{
			name:          "2GB * 50% = 1Gi",
			memoryBytes:   2147483648,
			memoryPercent: 0.50,
			wantValue:     "1Gi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.QueueMemoryPercent = tt.memoryPercent
			resources := ResourceInfo{
				CPUCount:    1,
				MemoryBytes: tt.memoryBytes,
				Source:      "system",
			}

			derived := AutoDerive(cfg, resources, map[string]bool{})

			for _, d := range derived {
				if d.Field == "queue-max-bytes" {
					if d.Value != tt.wantValue {
						t.Errorf("queue-max-bytes value: got %q, want %q", d.Value, tt.wantValue)
					}
					return
				}
			}
			t.Error("queue-max-bytes not found in derived list")
		})
	}
}

func TestAutoDerive_NilExplicitFields(t *testing.T) {
	// Nil explicitFields should not cause a panic (map lookup on nil returns zero value).
	cfg := DefaultConfig()
	resources := ResourceInfo{
		CPUCount:    4,
		MemoryBytes: 0,
		Source:      "system",
	}

	// In Go, looking up a key in a nil map returns the zero value (false).
	// This should work without panicking.
	derived := AutoDerive(cfg, resources, nil)

	if cfg.QueueWorkers != 4 {
		t.Errorf("QueueWorkers: got %d, want 4", cfg.QueueWorkers)
	}
	if len(derived) < 3 {
		t.Errorf("expected at least 3 derivations with nil explicitFields, got %d", len(derived))
	}
}

func TestAutoDerive_MaxInt64Memory(t *testing.T) {
	// math.MaxInt64 memory should be skipped (treated as unknown/unlimited).
	cfg := DefaultConfig()
	origQueueMaxBytes := cfg.QueueMaxBytes
	resources := ResourceInfo{
		CPUCount:    4,
		MemoryBytes: 9223372036854775807, // math.MaxInt64
		Source:      "system",
	}
	explicitFields := map[string]bool{}

	derived := AutoDerive(cfg, resources, explicitFields)

	if cfg.QueueMaxBytes != origQueueMaxBytes {
		t.Errorf("QueueMaxBytes should not change with MaxInt64 memory, got %d", cfg.QueueMaxBytes)
	}

	for _, d := range derived {
		if d.Field == "queue-max-bytes" || d.Field == "max-batch-bytes" {
			t.Errorf("memory-derived field %q should not appear when MemoryBytes=MaxInt64", d.Field)
		}
	}
}

func TestDerivedValueString(t *testing.T) {
	// Basic sanity: DerivedValue fields are properly populated.
	d := DerivedValue{
		Field:   "queue-workers",
		Value:   "4",
		Formula: "NumCPU",
	}
	if d.Field != "queue-workers" {
		t.Errorf("unexpected Field: %s", d.Field)
	}
	if d.Value != "4" {
		t.Errorf("unexpected Value: %s", d.Value)
	}
	if d.Formula != "NumCPU" {
		t.Errorf("unexpected Formula: %s", d.Formula)
	}
}

func TestResourceInfoStruct(t *testing.T) {
	r := ResourceInfo{
		CPUCount:    4,
		MemoryBytes: 1073741824,
		Source:      "cgroup",
	}
	if r.CPUCount != 4 {
		t.Errorf("CPUCount: got %d, want 4", r.CPUCount)
	}
	if r.MemoryBytes != 1073741824 {
		t.Errorf("MemoryBytes: got %d, want 1073741824", r.MemoryBytes)
	}
	if r.Source != "cgroup" {
		t.Errorf("Source: got %q, want \"cgroup\"", r.Source)
	}
}

func TestAutoDerive_QueueMemoryPercentFormula(t *testing.T) {
	// Verify the formula string includes the correct percentage.
	tests := []struct {
		percent     float64
		wantFormula string
	}{
		{0.10, "MemoryLimit * 10%"},
		{0.25, "MemoryLimit * 25%"},
		{0.50, "MemoryLimit * 50%"},
		{0.05, "MemoryLimit * 5%"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%.0f%%", tt.percent*100), func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.QueueMemoryPercent = tt.percent
			resources := ResourceInfo{
				CPUCount:    1,
				MemoryBytes: 1073741824,
				Source:      "system",
			}

			derived := AutoDerive(cfg, resources, map[string]bool{})

			for _, d := range derived {
				if d.Field == "queue-max-bytes" {
					if d.Formula != tt.wantFormula {
						t.Errorf("formula: got %q, want %q", d.Formula, tt.wantFormula)
					}
					return
				}
			}
			t.Error("queue-max-bytes not found in derived list")
		})
	}
}
