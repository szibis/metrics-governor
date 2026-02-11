package config

import (
	"strings"
	"testing"
)

func TestParseSemVersion(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  semVersion
	}{
		{name: "standard", input: "0.30.0", want: semVersion{0, 30, 0}},
		{name: "v_prefix", input: "v1.0.0", want: semVersion{1, 0, 0}},
		{name: "prerelease", input: "0.30.0-beta", want: semVersion{0, 30, 0}},
		{name: "build_metadata", input: "0.30.0+build.123", want: semVersion{0, 30, 0}},
		{name: "two_parts", input: "1.2", want: semVersion{1, 2, 0}},
		{name: "single_part", input: "3", want: semVersion{3, 0, 0}},
		{name: "nonzero_patch", input: "2.5.3", want: semVersion{2, 5, 3}},
		{name: "empty_string", input: "", want: semVersion{0, 0, 0}},
		{name: "v_prefix_two_parts", input: "v1.2", want: semVersion{1, 2, 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSemVersion(tt.input)
			if got != tt.want {
				t.Errorf("parseSemVersion(%q) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}

func TestSemVersionLess(t *testing.T) {
	tests := []struct {
		name string
		a, b semVersion
		want bool
	}{
		{name: "less_major", a: semVersion{0, 30, 0}, b: semVersion{1, 0, 0}, want: true},
		{name: "less_minor", a: semVersion{0, 30, 0}, b: semVersion{0, 31, 0}, want: true},
		{name: "less_patch", a: semVersion{0, 30, 0}, b: semVersion{0, 30, 1}, want: true},
		{name: "equal", a: semVersion{0, 30, 0}, b: semVersion{0, 30, 0}, want: false},
		{name: "greater_major", a: semVersion{1, 0, 0}, b: semVersion{0, 30, 0}, want: false},
		{name: "greater_minor", a: semVersion{0, 31, 0}, b: semVersion{0, 30, 0}, want: false},
		{name: "greater_patch", a: semVersion{0, 30, 1}, b: semVersion{0, 30, 0}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.a.Less(tt.b)
			if got != tt.want {
				t.Errorf("%+v.Less(%+v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestSemVersionEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b semVersion
		want bool
	}{
		{name: "equal", a: semVersion{0, 30, 0}, b: semVersion{0, 30, 0}, want: true},
		{name: "diff_major", a: semVersion{1, 30, 0}, b: semVersion{0, 30, 0}, want: false},
		{name: "diff_minor", a: semVersion{0, 31, 0}, b: semVersion{0, 30, 0}, want: false},
		{name: "diff_patch", a: semVersion{0, 30, 1}, b: semVersion{0, 30, 0}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.a.Equal(tt.b)
			if got != tt.want {
				t.Errorf("%+v.Equal(%+v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestSemVersionLessOrEqual(t *testing.T) {
	tests := []struct {
		name string
		a, b semVersion
		want bool
	}{
		{name: "less", a: semVersion{0, 30, 0}, b: semVersion{0, 31, 0}, want: true},
		{name: "equal", a: semVersion{0, 30, 0}, b: semVersion{0, 30, 0}, want: true},
		{name: "greater", a: semVersion{0, 31, 0}, b: semVersion{0, 30, 0}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.a.LessOrEqual(tt.b)
			if got != tt.want {
				t.Errorf("%+v.LessOrEqual(%+v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestSemVersionMinorDistance(t *testing.T) {
	tests := []struct {
		name string
		a, b semVersion
		want int
	}{
		{name: "same", a: semVersion{0, 30, 0}, b: semVersion{0, 30, 0}, want: 0},
		{name: "one_apart", a: semVersion{0, 30, 0}, b: semVersion{0, 31, 0}, want: 1},
		{name: "five_apart", a: semVersion{0, 30, 0}, b: semVersion{0, 35, 0}, want: 5},
		{name: "reverse_order", a: semVersion{0, 35, 0}, b: semVersion{0, 30, 0}, want: 5},
		{name: "cross_major", a: semVersion{0, 30, 0}, b: semVersion{1, 0, 0}, want: 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.a.MinorDistance(tt.b)
			if got != tt.want {
				t.Errorf("%+v.MinorDistance(%+v) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestNewDeprecationRegistry(t *testing.T) {
	reg := NewDeprecationRegistry("0.30.0")
	if reg == nil {
		t.Fatal("NewDeprecationRegistry returned nil")
	}
	expected := semVersion{0, 30, 0}
	if reg.currentVersion != expected {
		t.Errorf("currentVersion = %+v, want %+v", reg.currentVersion, expected)
	}
	if len(reg.entries) != 0 {
		t.Errorf("entries length = %d, want 0", len(reg.entries))
	}
}

func TestNewDeprecationRegistryWithPrefix(t *testing.T) {
	reg := NewDeprecationRegistry("v1.2.3")
	expected := semVersion{1, 2, 3}
	if reg.currentVersion != expected {
		t.Errorf("currentVersion = %+v, want %+v", reg.currentVersion, expected)
	}
}

func TestRegister(t *testing.T) {
	reg := NewDeprecationRegistry("0.30.0")
	entry := DeprecationEntry{
		OldName:      "queue-workers",
		NewName:      "parallelism",
		Message:      "Use --parallelism",
		AnnouncedIn:  "0.30.0",
		DeprecatedIn: "0.31.0",
		RemovedIn:    "1.0.0",
	}
	reg.Register(entry)
	if len(reg.entries) != 1 {
		t.Fatalf("entries length = %d, want 1", len(reg.entries))
	}
	if reg.entries[0].OldName != "queue-workers" {
		t.Errorf("entry OldName = %q, want %q", reg.entries[0].OldName, "queue-workers")
	}
}

func TestComputeStage(t *testing.T) {
	entry := DeprecationEntry{
		OldName:      "queue-workers",
		NewName:      "parallelism",
		Message:      "Use --parallelism",
		AnnouncedIn:  "0.30.0",
		DeprecatedIn: "0.31.0",
		RemovedIn:    "1.0.0",
	}

	tests := []struct {
		name    string
		version string
		want    DeprecationStage
	}{
		{
			name:    "announced_at_announce_version",
			version: "0.30.0",
			want:    StageAnnounced,
		},
		{
			name:    "announced_before_deprecated",
			version: "0.30.5",
			want:    StageAnnounced,
		},
		{
			name:    "deprecated_at_deprecated_version",
			version: "0.31.0",
			want:    StageDeprecated,
		},
		{
			name:    "deprecated_well_before_removal",
			version: "0.35.0",
			want:    StageDeprecated,
		},
		{
			name:    "removed_at_removal_version",
			version: "1.0.0",
			want:    StageRemoved,
		},
		{
			name:    "removed_after_removal_version",
			version: "1.1.0",
			want:    StageRemoved,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := NewDeprecationRegistry(tt.version)
			got := reg.computeStage(entry)
			if got != tt.want {
				t.Errorf("computeStage(version=%s) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}

func TestComputeStageWarningWindow(t *testing.T) {
	// Entry with removal at 0.36.0 to test the warning window logic
	entry := DeprecationEntry{
		OldName:      "queue-workers",
		NewName:      "parallelism",
		Message:      "Use --parallelism",
		AnnouncedIn:  "0.30.0",
		DeprecatedIn: "0.31.0",
		RemovedIn:    "0.36.0",
	}

	tests := []struct {
		name    string
		version string
		want    DeprecationStage
	}{
		{
			name:    "deprecated_far_from_removal",
			version: "0.31.0",
			want:    StageDeprecated,
		},
		{
			name:    "deprecated_3_minor_from_removal",
			version: "0.33.0",
			want:    StageDeprecated,
		},
		{
			name:    "warning_2_minor_from_removal",
			version: "0.34.0",
			want:    StageWarning,
		},
		{
			name:    "warning_1_minor_from_removal",
			version: "0.35.0",
			want:    StageWarning,
		},
		{
			name:    "removed_at_removal",
			version: "0.36.0",
			want:    StageRemoved,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := NewDeprecationRegistry(tt.version)
			got := reg.computeStage(entry)
			if got != tt.want {
				t.Errorf("computeStage(version=%s) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}

func TestComputeStageNoDeprecatedIn(t *testing.T) {
	// Entry with no DeprecatedIn should stay at Announced until Removed
	entry := DeprecationEntry{
		OldName:     "some-flag",
		NewName:     "new-flag",
		Message:     "Use --new-flag",
		AnnouncedIn: "0.30.0",
		RemovedIn:   "1.0.0",
	}

	reg := NewDeprecationRegistry("0.35.0")
	got := reg.computeStage(entry)
	if got != StageAnnounced {
		t.Errorf("computeStage with empty DeprecatedIn = %v, want %v", got, StageAnnounced)
	}
}

func TestCheckAndWarnOnlyInUse(t *testing.T) {
	reg := NewDeprecationRegistry("0.31.0")
	reg.Register(DeprecationEntry{
		OldName:      "queue-workers",
		NewName:      "parallelism",
		Message:      "Use --parallelism",
		AnnouncedIn:  "0.30.0",
		DeprecatedIn: "0.31.0",
		RemovedIn:    "1.0.0",
	})

	t.Run("in_use_produces_warning", func(t *testing.T) {
		explicit := map[string]bool{"queue-workers": true}
		warnings := reg.CheckAndWarn(explicit)
		if len(warnings) != 1 {
			t.Fatalf("got %d warnings, want 1", len(warnings))
		}
		w := warnings[0]
		if !w.InUse {
			t.Error("InUse = false, want true")
		}
		if w.Stage != StageDeprecated {
			t.Errorf("Stage = %v, want %v", w.Stage, StageDeprecated)
		}
		if w.Entry.OldName != "queue-workers" {
			t.Errorf("OldName = %q, want %q", w.Entry.OldName, "queue-workers")
		}
	})

	t.Run("not_in_use_no_warning", func(t *testing.T) {
		explicit := map[string]bool{}
		warnings := reg.CheckAndWarn(explicit)
		if len(warnings) != 0 {
			t.Fatalf("got %d warnings, want 0", len(warnings))
		}
	})

	t.Run("nil_explicit_no_warning", func(t *testing.T) {
		warnings := reg.CheckAndWarn(nil)
		if len(warnings) != 0 {
			t.Fatalf("got %d warnings, want 0", len(warnings))
		}
	})
}

func TestCheckAndWarnMessageFormat(t *testing.T) {
	entry := DeprecationEntry{
		OldName:      "queue-workers",
		NewName:      "parallelism",
		Message:      "Use --parallelism",
		AnnouncedIn:  "0.30.0",
		DeprecatedIn: "0.31.0",
		RemovedIn:    "0.36.0",
	}
	explicit := map[string]bool{"queue-workers": true}

	tests := []struct {
		name    string
		version string
		stage   DeprecationStage
		prefix  string
	}{
		{
			name:    "announced_message",
			version: "0.30.0",
			stage:   StageAnnounced,
			prefix:  "NOTE:",
		},
		{
			name:    "deprecated_message",
			version: "0.31.0",
			stage:   StageDeprecated,
			prefix:  "DEPRECATED:",
		},
		{
			name:    "warning_message",
			version: "0.34.0",
			stage:   StageWarning,
			prefix:  "BREAKING CHANGE SOON:",
		},
		{
			name:    "removed_message",
			version: "0.36.0",
			stage:   StageRemoved,
			prefix:  "REMOVED:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := NewDeprecationRegistry(tt.version)
			reg.Register(entry)
			warnings := reg.CheckAndWarn(explicit)
			if len(warnings) != 1 {
				t.Fatalf("got %d warnings, want 1", len(warnings))
			}
			w := warnings[0]
			if w.Stage != tt.stage {
				t.Errorf("Stage = %v, want %v", w.Stage, tt.stage)
			}
			if !strings.HasPrefix(w.Message, tt.prefix) {
				t.Errorf("Message = %q, want prefix %q", w.Message, tt.prefix)
			}
			if !strings.Contains(w.Message, "--queue-workers") {
				t.Errorf("Message %q should contain --queue-workers", w.Message)
			}
			if !strings.Contains(w.Message, "--parallelism") {
				t.Errorf("Message %q should contain --parallelism", w.Message)
			}
		})
	}
}

func TestDumpDeprecations(t *testing.T) {
	reg := NewDeprecationRegistry("0.30.0")
	reg.Register(DeprecationEntry{
		OldName:      "queue-workers",
		NewName:      "parallelism",
		Message:      "Use --parallelism",
		AnnouncedIn:  "0.30.0",
		DeprecatedIn: "0.31.0",
		RemovedIn:    "1.0.0",
	})

	dump := reg.DumpDeprecations()

	// Check version header
	if !strings.Contains(dump, "v0.30.0") {
		t.Errorf("dump should contain version v0.30.0, got:\n%s", dump)
	}

	// Check column headers
	if !strings.Contains(dump, "Status") {
		t.Errorf("dump should contain 'Status' header")
	}
	if !strings.Contains(dump, "Parameter") {
		t.Errorf("dump should contain 'Parameter' header")
	}
	if !strings.Contains(dump, "Replacement") {
		t.Errorf("dump should contain 'Replacement' header")
	}
	if !strings.Contains(dump, "Removal") {
		t.Errorf("dump should contain 'Removal' header")
	}

	// Check entry content
	if !strings.Contains(dump, "queue-workers") {
		t.Errorf("dump should contain 'queue-workers'")
	}
	if !strings.Contains(dump, "parallelism") {
		t.Errorf("dump should contain 'parallelism'")
	}
	if !strings.Contains(dump, "ANNOUNCED") {
		t.Errorf("dump should contain 'ANNOUNCED' stage")
	}

	// Check footer
	if !strings.Contains(dump, "--strict-deprecations") {
		t.Errorf("dump should contain '--strict-deprecations' in footer")
	}
}

func TestDumpDeprecationsEmpty(t *testing.T) {
	reg := NewDeprecationRegistry("0.30.0")
	dump := reg.DumpDeprecations()
	if !strings.Contains(dump, "v0.30.0") {
		t.Errorf("empty dump should still contain version header")
	}
}

func TestDefaultDeprecations(t *testing.T) {
	entries := DefaultDeprecations()
	// All v0.x deprecations were removed in v1.0.0; no active entries remain.
	if len(entries) != 0 {
		t.Errorf("DefaultDeprecations() returned %d entries, want 0", len(entries))
	}
}

func TestDefaultDeprecationsKnownEntries(t *testing.T) {
	// All v0.x deprecation entries were removed in v1.0.0.
	// Verify the slice is empty (no known entries to check).
	entries := DefaultDeprecations()
	if len(entries) != 0 {
		t.Errorf("DefaultDeprecations() should return empty slice, got %d entries", len(entries))
	}
}

func TestListAll(t *testing.T) {
	reg := NewDeprecationRegistry("1.0.0") // past removal
	entry1 := DeprecationEntry{
		OldName: "old-a", NewName: "new-a", Message: "msg",
		AnnouncedIn: "0.30.0", DeprecatedIn: "0.31.0", RemovedIn: "1.0.0",
	}
	entry2 := DeprecationEntry{
		OldName: "old-b", NewName: "new-b", Message: "msg",
		AnnouncedIn: "0.30.0", DeprecatedIn: "0.31.0", RemovedIn: "1.0.0",
	}
	reg.Register(entry1)
	reg.Register(entry2)

	all := reg.ListAll()
	if len(all) != 2 {
		t.Errorf("ListAll() returned %d entries, want 2", len(all))
	}

	// Verify it returns a copy
	all[0].OldName = "mutated"
	if reg.entries[0].OldName == "mutated" {
		t.Error("ListAll() should return a copy, but mutating the result changed the original")
	}
}

func TestListActive(t *testing.T) {
	reg := NewDeprecationRegistry("1.0.0") // at removal version

	// This entry is REMOVED at v1.0.0
	reg.Register(DeprecationEntry{
		OldName: "removed-param", NewName: "new-param", Message: "msg",
		AnnouncedIn: "0.28.0", DeprecatedIn: "0.29.0", RemovedIn: "1.0.0",
	})
	// This entry is still ANNOUNCED at v1.0.0 (removed in 2.0.0)
	reg.Register(DeprecationEntry{
		OldName: "active-param", NewName: "new-param2", Message: "msg",
		AnnouncedIn: "1.0.0", DeprecatedIn: "1.1.0", RemovedIn: "2.0.0",
	})

	active := reg.ListActive()
	if len(active) != 1 {
		t.Fatalf("ListActive() returned %d entries, want 1", len(active))
	}
	if active[0].OldName != "active-param" {
		t.Errorf("ListActive()[0].OldName = %q, want %q", active[0].OldName, "active-param")
	}
}

func TestListActiveExcludesRemoved(t *testing.T) {
	reg := NewDeprecationRegistry("2.0.0")
	reg.Register(DeprecationEntry{
		OldName: "old-a", NewName: "new-a", Message: "msg",
		AnnouncedIn: "0.30.0", DeprecatedIn: "0.31.0", RemovedIn: "1.0.0",
	})
	reg.Register(DeprecationEntry{
		OldName: "old-b", NewName: "new-b", Message: "msg",
		AnnouncedIn: "0.30.0", DeprecatedIn: "0.31.0", RemovedIn: "1.0.0",
	})

	active := reg.ListActive()
	if len(active) != 0 {
		t.Errorf("ListActive() at v2.0.0 should return 0 entries, got %d", len(active))
	}
}

func TestDeprecationStageString(t *testing.T) {
	tests := []struct {
		stage DeprecationStage
		want  string
	}{
		{StageAnnounced, "ANNOUNCED"},
		{StageDeprecated, "DEPRECATED"},
		{StageWarning, "WARNING"},
		{StageRemoved, "REMOVED"},
		{DeprecationStage(99), "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.stage.String()
			if got != tt.want {
				t.Errorf("DeprecationStage(%d).String() = %q, want %q", tt.stage, got, tt.want)
			}
		})
	}
}

func TestCheckStrictDeprecationsNoError(t *testing.T) {
	// Non-removed stages should not produce an error
	warnings := []DeprecationWarning{
		{
			Entry: DeprecationEntry{OldName: "queue-workers"},
			Stage: StageAnnounced,
			InUse: true,
		},
		{
			Entry: DeprecationEntry{OldName: "export-concurrency"},
			Stage: StageDeprecated,
			InUse: true,
		},
		{
			Entry: DeprecationEntry{OldName: "buffer-memory-percent"},
			Stage: StageWarning,
			InUse: true,
		},
	}

	err := CheckStrictDeprecations(warnings)
	if err != nil {
		t.Errorf("CheckStrictDeprecations() = %v, want nil for non-removed stages", err)
	}
}

func TestCheckStrictDeprecationsReturnsError(t *testing.T) {
	warnings := []DeprecationWarning{
		{
			Entry:   DeprecationEntry{OldName: "queue-workers"},
			Stage:   StageRemoved,
			InUse:   true,
			Message: "REMOVED: --queue-workers was removed in 1.0.0",
		},
	}

	err := CheckStrictDeprecations(warnings)
	if err == nil {
		t.Fatal("CheckStrictDeprecations() = nil, want error for REMOVED stage")
	}
	if !strings.Contains(err.Error(), "queue-workers") {
		t.Errorf("error should mention param name, got: %v", err)
	}
	if !strings.Contains(err.Error(), "strict-deprecations") {
		t.Errorf("error should mention strict-deprecations, got: %v", err)
	}
}

func TestCheckStrictDeprecationsEmptyWarnings(t *testing.T) {
	err := CheckStrictDeprecations(nil)
	if err != nil {
		t.Errorf("CheckStrictDeprecations(nil) = %v, want nil", err)
	}

	err = CheckStrictDeprecations([]DeprecationWarning{})
	if err != nil {
		t.Errorf("CheckStrictDeprecations([]) = %v, want nil", err)
	}
}

func TestCheckStrictDeprecationsFirstRemovedWins(t *testing.T) {
	warnings := []DeprecationWarning{
		{
			Entry:   DeprecationEntry{OldName: "first-removed"},
			Stage:   StageRemoved,
			InUse:   true,
			Message: "REMOVED: --first-removed",
		},
		{
			Entry:   DeprecationEntry{OldName: "second-removed"},
			Stage:   StageRemoved,
			InUse:   true,
			Message: "REMOVED: --second-removed",
		},
	}

	err := CheckStrictDeprecations(warnings)
	if err == nil {
		t.Fatal("expected error")
	}
	// Should report the first removed param encountered
	if !strings.Contains(err.Error(), "first-removed") {
		t.Errorf("error should mention first-removed, got: %v", err)
	}
}

func TestCheckAndWarnMultipleEntries(t *testing.T) {
	reg := NewDeprecationRegistry("0.31.0")
	reg.Register(DeprecationEntry{
		OldName: "queue-workers", NewName: "parallelism", Message: "msg",
		AnnouncedIn: "0.30.0", DeprecatedIn: "0.31.0", RemovedIn: "1.0.0",
	})
	reg.Register(DeprecationEntry{
		OldName: "export-concurrency", NewName: "parallelism", Message: "msg",
		AnnouncedIn: "0.30.0", DeprecatedIn: "0.31.0", RemovedIn: "1.0.0",
	})

	// Only one is in use
	explicit := map[string]bool{"queue-workers": true}
	warnings := reg.CheckAndWarn(explicit)
	if len(warnings) != 1 {
		t.Fatalf("got %d warnings, want 1", len(warnings))
	}

	// Both in use
	explicit["export-concurrency"] = true
	warnings = reg.CheckAndWarn(explicit)
	if len(warnings) != 2 {
		t.Fatalf("got %d warnings, want 2", len(warnings))
	}
}

func TestEndToEndLifecycle(t *testing.T) {
	// Simulate a parameter going through all lifecycle stages as versions advance
	entry := DeprecationEntry{
		OldName:      "queue-workers",
		NewName:      "parallelism",
		Message:      "Use --parallelism to set all worker counts from a single value.",
		AnnouncedIn:  "0.30.0",
		DeprecatedIn: "0.31.0",
		RemovedIn:    "0.36.0",
	}
	explicit := map[string]bool{"queue-workers": true}

	stages := []struct {
		version string
		stage   DeprecationStage
	}{
		{"0.30.0", StageAnnounced},
		{"0.30.5", StageAnnounced},
		{"0.31.0", StageDeprecated},
		{"0.33.0", StageDeprecated},
		{"0.34.0", StageWarning},
		{"0.35.0", StageWarning},
		{"0.36.0", StageRemoved},
		{"0.37.0", StageRemoved},
		{"1.0.0", StageRemoved},
	}

	for _, tt := range stages {
		t.Run(tt.version, func(t *testing.T) {
			reg := NewDeprecationRegistry(tt.version)
			reg.Register(entry)
			warnings := reg.CheckAndWarn(explicit)
			if len(warnings) != 1 {
				t.Fatalf("version %s: got %d warnings, want 1", tt.version, len(warnings))
			}
			if warnings[0].Stage != tt.stage {
				t.Errorf("version %s: stage = %v, want %v", tt.version, warnings[0].Stage, tt.stage)
			}
		})
	}
}
