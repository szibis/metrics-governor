package config

import (
	"strings"
	"testing"
)

// TestDumpEffectiveConfigContainsSections verifies that DumpEffectiveConfig
// produces output with all expected configuration sections.
func TestDumpEffectiveConfigContainsSections(t *testing.T) {
	cfg := DefaultConfig()
	result := &BuildResult{
		Config:         cfg,
		ProfileApplied: ProfileBalanced,
		Derivations: []DerivedValue{
			{Field: "queue-workers", Value: "4", Formula: "CPU cores"},
		},
		Deprecations: []DeprecationWarning{
			{
				Entry: DeprecationEntry{
					OldName:      "old-flag",
					NewName:      "new-flag",
					RemovedIn:    "1.0.0",
					DeprecatedIn: "0.30.0",
				},
				Stage:   StageDeprecated,
				Message: "use --new-flag instead",
			},
		},
	}

	output := DumpEffectiveConfig(result)

	// Check header
	if !strings.Contains(output, "Effective Configuration") {
		t.Error("output should contain 'Effective Configuration' header")
	}

	// Check all key sections
	sections := []string{
		"Receiver:",
		"Exporter:",
		"Buffer:",
		"Queue:",
		"Resilience:",
		"Memory:",
		"Performance:",
	}
	for _, section := range sections {
		if !strings.Contains(output, section) {
			t.Errorf("output should contain section %q", section)
		}
	}

	// Check profile is shown
	if !strings.Contains(output, "Profile: balanced") {
		t.Error("output should contain 'Profile: balanced'")
	}

	// Check derivations appear
	if !strings.Contains(output, "Auto-derived values:") {
		t.Error("output should contain 'Auto-derived values:' section")
	}
	if !strings.Contains(output, "queue-workers = 4 (CPU cores)") {
		t.Error("output should contain the derived queue-workers value")
	}

	// Check deprecation warnings appear
	if !strings.Contains(output, "Deprecation warnings:") {
		t.Error("output should contain 'Deprecation warnings:' section")
	}
	if !strings.Contains(output, "use --new-flag instead") {
		t.Error("output should contain the deprecation warning message")
	}
	if !strings.Contains(output, "[DEPRECATED]") {
		t.Error("output should contain the deprecation stage label")
	}
}

// TestDumpEffectiveConfigNoProfile verifies output when no profile is applied.
func TestDumpEffectiveConfigNoProfile(t *testing.T) {
	cfg := DefaultConfig()
	result := &BuildResult{
		Config: cfg,
	}

	output := DumpEffectiveConfig(result)

	if strings.Contains(output, "Profile:") {
		t.Error("output should NOT contain 'Profile:' when no profile is applied")
	}
}

// TestDumpEffectiveConfigNoDerivations verifies output omits derivations section when empty.
func TestDumpEffectiveConfigNoDerivations(t *testing.T) {
	cfg := DefaultConfig()
	result := &BuildResult{
		Config: cfg,
	}

	output := DumpEffectiveConfig(result)

	if strings.Contains(output, "Auto-derived values:") {
		t.Error("output should NOT contain 'Auto-derived values:' when there are none")
	}
}

// TestDumpEffectiveConfigNoDeprecations verifies output omits deprecations section when empty.
func TestDumpEffectiveConfigNoDeprecations(t *testing.T) {
	cfg := DefaultConfig()
	result := &BuildResult{
		Config: cfg,
	}

	output := DumpEffectiveConfig(result)

	if strings.Contains(output, "Deprecation warnings:") {
		t.Error("output should NOT contain 'Deprecation warnings:' when there are none")
	}
}

// TestLogDerivations verifies that LogDerivations calls logFn for each derivation
// with the correct field/value/formula.
func TestLogDerivations(t *testing.T) {
	derivations := []DerivedValue{
		{Field: "queue-workers", Value: "4", Formula: "CPU cores"},
		{Field: "buffer-size", Value: "20000", Formula: "2 * default"},
	}

	var calls []map[string]interface{}
	mockLog := func(msg string, fields map[string]interface{}) {
		if msg != "auto-derived config value" {
			t.Errorf("unexpected log message: %q", msg)
		}
		calls = append(calls, fields)
	}

	LogDerivations(derivations, mockLog)

	if len(calls) != 2 {
		t.Fatalf("expected 2 log calls, got %d", len(calls))
	}

	// First derivation
	if calls[0]["field"] != "queue-workers" {
		t.Errorf("first call field = %v, want 'queue-workers'", calls[0]["field"])
	}
	if calls[0]["value"] != "4" {
		t.Errorf("first call value = %v, want '4'", calls[0]["value"])
	}
	if calls[0]["formula"] != "CPU cores" {
		t.Errorf("first call formula = %v, want 'CPU cores'", calls[0]["formula"])
	}

	// Second derivation
	if calls[1]["field"] != "buffer-size" {
		t.Errorf("second call field = %v, want 'buffer-size'", calls[1]["field"])
	}
}

// TestLogDerivationsEmpty verifies no calls when derivations is empty.
func TestLogDerivationsEmpty(t *testing.T) {
	called := false
	mockLog := func(msg string, fields map[string]interface{}) {
		called = true
	}

	LogDerivations(nil, mockLog)

	if called {
		t.Error("logFn should not be called with empty derivations")
	}
}

// TestLogDeprecationsAnnounced verifies StageAnnounced calls infoFn.
func TestLogDeprecationsAnnounced(t *testing.T) {
	warnings := []DeprecationWarning{
		{
			Entry: DeprecationEntry{
				OldName:   "old-param",
				NewName:   "new-param",
				RemovedIn: "1.0.0",
			},
			Stage:   StageAnnounced,
			Message: "announced deprecation",
		},
	}

	var infoCalled, warnCalled, errorCalled bool
	infoFn := func(msg string, fields map[string]interface{}) {
		infoCalled = true
		if msg != "announced deprecation" {
			t.Errorf("unexpected info message: %q", msg)
		}
		if fields["old_param"] != "old-param" {
			t.Errorf("expected old_param='old-param', got %v", fields["old_param"])
		}
		if fields["new_param"] != "new-param" {
			t.Errorf("expected new_param='new-param', got %v", fields["new_param"])
		}
	}
	warnFn := func(msg string, fields map[string]interface{}) { warnCalled = true }
	errorFn := func(msg string, fields map[string]interface{}) { errorCalled = true }

	LogDeprecations(warnings, infoFn, warnFn, errorFn)

	if !infoCalled {
		t.Error("infoFn should have been called for StageAnnounced")
	}
	if warnCalled {
		t.Error("warnFn should NOT have been called for StageAnnounced")
	}
	if errorCalled {
		t.Error("errorFn should NOT have been called for StageAnnounced")
	}
}

// TestLogDeprecationsDeprecated verifies StageDeprecated calls warnFn.
func TestLogDeprecationsDeprecated(t *testing.T) {
	warnings := []DeprecationWarning{
		{
			Entry: DeprecationEntry{
				OldName:   "old-param",
				NewName:   "new-param",
				RemovedIn: "1.0.0",
			},
			Stage:   StageDeprecated,
			Message: "deprecated warning",
		},
	}

	var infoCalled, warnCalled, errorCalled bool
	infoFn := func(msg string, fields map[string]interface{}) { infoCalled = true }
	warnFn := func(msg string, fields map[string]interface{}) { warnCalled = true }
	errorFn := func(msg string, fields map[string]interface{}) { errorCalled = true }

	LogDeprecations(warnings, infoFn, warnFn, errorFn)

	if infoCalled {
		t.Error("infoFn should NOT have been called for StageDeprecated")
	}
	if !warnCalled {
		t.Error("warnFn should have been called for StageDeprecated")
	}
	if errorCalled {
		t.Error("errorFn should NOT have been called for StageDeprecated")
	}
}

// TestLogDeprecationsWarning verifies StageWarning calls warnFn.
func TestLogDeprecationsWarning(t *testing.T) {
	warnings := []DeprecationWarning{
		{
			Entry: DeprecationEntry{
				OldName:   "old-param",
				NewName:   "new-param",
				RemovedIn: "1.0.0",
			},
			Stage:   StageWarning,
			Message: "warning stage",
		},
	}

	var infoCalled, warnCalled, errorCalled bool
	infoFn := func(msg string, fields map[string]interface{}) { infoCalled = true }
	warnFn := func(msg string, fields map[string]interface{}) { warnCalled = true }
	errorFn := func(msg string, fields map[string]interface{}) { errorCalled = true }

	LogDeprecations(warnings, infoFn, warnFn, errorFn)

	if infoCalled {
		t.Error("infoFn should NOT have been called for StageWarning")
	}
	if !warnCalled {
		t.Error("warnFn should have been called for StageWarning")
	}
	if errorCalled {
		t.Error("errorFn should NOT have been called for StageWarning")
	}
}

// TestLogDeprecationsRemoved verifies StageRemoved calls errorFn.
func TestLogDeprecationsRemoved(t *testing.T) {
	warnings := []DeprecationWarning{
		{
			Entry: DeprecationEntry{
				OldName:   "old-param",
				NewName:   "new-param",
				RemovedIn: "1.0.0",
			},
			Stage:   StageRemoved,
			Message: "removed error",
		},
	}

	var infoCalled, warnCalled, errorCalled bool
	infoFn := func(msg string, fields map[string]interface{}) { infoCalled = true }
	warnFn := func(msg string, fields map[string]interface{}) { warnCalled = true }
	errorFn := func(msg string, fields map[string]interface{}) { errorCalled = true }

	LogDeprecations(warnings, infoFn, warnFn, errorFn)

	if infoCalled {
		t.Error("infoFn should NOT have been called for StageRemoved")
	}
	if warnCalled {
		t.Error("warnFn should NOT have been called for StageRemoved")
	}
	if !errorCalled {
		t.Error("errorFn should have been called for StageRemoved")
	}
}

// TestLogDeprecationsMixed verifies correct routing with mixed stages.
func TestLogDeprecationsMixed(t *testing.T) {
	warnings := []DeprecationWarning{
		{Entry: DeprecationEntry{OldName: "a", NewName: "b", RemovedIn: "1.0.0"}, Stage: StageAnnounced, Message: "info msg"},
		{Entry: DeprecationEntry{OldName: "c", NewName: "d", RemovedIn: "1.0.0"}, Stage: StageDeprecated, Message: "warn msg"},
		{Entry: DeprecationEntry{OldName: "e", NewName: "f", RemovedIn: "1.0.0"}, Stage: StageRemoved, Message: "error msg"},
	}

	var infoCount, warnCount, errorCount int
	infoFn := func(msg string, fields map[string]interface{}) { infoCount++ }
	warnFn := func(msg string, fields map[string]interface{}) { warnCount++ }
	errorFn := func(msg string, fields map[string]interface{}) { errorCount++ }

	LogDeprecations(warnings, infoFn, warnFn, errorFn)

	if infoCount != 1 {
		t.Errorf("expected 1 info call, got %d", infoCount)
	}
	if warnCount != 1 {
		t.Errorf("expected 1 warn call, got %d", warnCount)
	}
	if errorCount != 1 {
		t.Errorf("expected 1 error call, got %d", errorCount)
	}
}

// TestLogDeprecationsEmpty verifies no calls with empty warnings.
func TestLogDeprecationsEmpty(t *testing.T) {
	called := false
	fn := func(msg string, fields map[string]interface{}) { called = true }

	LogDeprecations(nil, fn, fn, fn)

	if called {
		t.Error("no functions should be called with empty warnings")
	}
}

// TestCheckStrictDeprecationsRemoved verifies error is returned for REMOVED stage.
func TestCheckStrictDeprecationsRemoved(t *testing.T) {
	warnings := []DeprecationWarning{
		{
			Entry: DeprecationEntry{
				OldName: "removed-flag",
				NewName: "new-flag",
			},
			Stage:   StageRemoved,
			Message: "this flag was removed",
		},
	}

	err := CheckStrictDeprecations(warnings)
	if err == nil {
		t.Fatal("expected error for REMOVED stage, got nil")
	}
	if !strings.Contains(err.Error(), "removed-flag") {
		t.Errorf("error should mention the removed flag name, got: %v", err)
	}
	if !strings.Contains(err.Error(), "strict-deprecations") {
		t.Errorf("error should mention strict-deprecations, got: %v", err)
	}
}

// TestCheckStrictDeprecationsNonRemoved verifies nil is returned for non-REMOVED stages.
func TestCheckStrictDeprecationsNonRemoved(t *testing.T) {
	stages := []DeprecationStage{StageAnnounced, StageDeprecated, StageWarning}

	for _, stage := range stages {
		warnings := []DeprecationWarning{
			{
				Entry: DeprecationEntry{OldName: "some-flag", NewName: "new-flag"},
				Stage: stage,
			},
		}

		err := CheckStrictDeprecations(warnings)
		if err != nil {
			t.Errorf("expected nil error for stage %v, got: %v", stage, err)
		}
	}
}

// TestCheckStrictDeprecationsEmpty verifies nil for empty warnings.
func TestCheckStrictDeprecationsEmpty(t *testing.T) {
	err := CheckStrictDeprecations(nil)
	if err != nil {
		t.Errorf("expected nil error for empty warnings, got: %v", err)
	}
}

// TestCheckStrictDeprecationsMixedStages verifies that only REMOVED triggers error,
// even when mixed with other stages.
func TestCheckStrictDeprecationsMixedStages(t *testing.T) {
	warnings := []DeprecationWarning{
		{Entry: DeprecationEntry{OldName: "ok-flag"}, Stage: StageAnnounced},
		{Entry: DeprecationEntry{OldName: "warn-flag"}, Stage: StageWarning},
		{Entry: DeprecationEntry{OldName: "bad-flag"}, Stage: StageRemoved, Message: "removed"},
	}

	err := CheckStrictDeprecations(warnings)
	if err == nil {
		t.Fatal("expected error when REMOVED stage is present")
	}
	if !strings.Contains(err.Error(), "bad-flag") {
		t.Errorf("error should reference the removed flag, got: %v", err)
	}
}

// TestHandleEarlyExitsShowHelp verifies that ShowHelp triggers an early exit.
func TestHandleEarlyExitsShowHelp(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ShowHelp = true

	// HandleEarlyExits prints to stdout/stderr; we just check the return value.
	result := HandleEarlyExits(cfg)
	if !result {
		t.Error("HandleEarlyExits should return true when ShowHelp is set")
	}
}

// TestHandleEarlyExitsShowVersion verifies that ShowVersion triggers an early exit.
func TestHandleEarlyExitsShowVersion(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ShowVersion = true

	result := HandleEarlyExits(cfg)
	if !result {
		t.Error("HandleEarlyExits should return true when ShowVersion is set")
	}
}

// TestHandleEarlyExitsNormal verifies that a normal config does not trigger early exit.
func TestHandleEarlyExitsNormal(t *testing.T) {
	cfg := DefaultConfig()
	// All Show* fields are false/empty by default

	result := HandleEarlyExits(cfg)
	if result {
		t.Error("HandleEarlyExits should return false for a normal config")
	}
}

// TestHandleEarlyExitsShowProfile verifies that ShowProfile triggers an early exit.
func TestHandleEarlyExitsShowProfile(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ShowProfile = "balanced"

	result := HandleEarlyExits(cfg)
	if !result {
		t.Error("HandleEarlyExits should return true when ShowProfile is set to a valid profile")
	}
}

// TestHandleEarlyExitsShowDeprecations verifies that ShowDeprecations triggers an early exit.
func TestHandleEarlyExitsShowDeprecations(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ShowDeprecations = true

	result := HandleEarlyExits(cfg)
	if !result {
		t.Error("HandleEarlyExits should return true when ShowDeprecations is set")
	}
}

// TestLogProfileInfoBalanced verifies that LogProfileInfo calls logFn with
// profile fields for the balanced profile.
func TestLogProfileInfoBalanced(t *testing.T) {
	var logMsgs []string
	var logFields []map[string]interface{}
	mockLog := func(msg string, fields map[string]interface{}) {
		logMsgs = append(logMsgs, msg)
		logFields = append(logFields, fields)
	}

	LogProfileInfo(ProfileBalanced, mockLog)

	if len(logMsgs) == 0 {
		t.Fatal("expected at least one log call for balanced profile")
	}

	// First call should be the main profile info line
	if !strings.Contains(logMsgs[0], "balanced") {
		t.Errorf("first log message should mention 'balanced', got: %q", logMsgs[0])
	}
	if !strings.Contains(logMsgs[0], "governance settings applied") {
		t.Errorf("first log message should mention 'governance settings applied', got: %q", logMsgs[0])
	}

	// Check that profile field is present
	if logFields[0]["profile"] != "balanced" {
		t.Errorf("expected profile field = 'balanced', got %v", logFields[0]["profile"])
	}
}

// TestLogProfileInfoMinimal verifies LogProfileInfo works for the minimal profile.
func TestLogProfileInfoMinimal(t *testing.T) {
	var logMsgs []string
	mockLog := func(msg string, fields map[string]interface{}) {
		logMsgs = append(logMsgs, msg)
	}

	LogProfileInfo(ProfileMinimal, mockLog)

	if len(logMsgs) == 0 {
		t.Fatal("expected at least one log call for minimal profile")
	}
	if !strings.Contains(logMsgs[0], "minimal") {
		t.Errorf("log message should mention 'minimal', got: %q", logMsgs[0])
	}
}

// TestLogProfileInfoPerformance verifies LogProfileInfo works for the performance profile.
func TestLogProfileInfoPerformance(t *testing.T) {
	var logMsgs []string
	mockLog := func(msg string, fields map[string]interface{}) {
		logMsgs = append(logMsgs, msg)
	}

	LogProfileInfo(ProfilePerformance, mockLog)

	if len(logMsgs) == 0 {
		t.Fatal("expected at least one log call for performance profile")
	}
	if !strings.Contains(logMsgs[0], "performance") {
		t.Errorf("log message should mention 'performance', got: %q", logMsgs[0])
	}
}

// TestLogProfileInfoInvalid verifies that LogProfileInfo silently returns
// for an unknown profile name (no log calls, no panic).
func TestLogProfileInfoInvalid(t *testing.T) {
	called := false
	mockLog := func(msg string, fields map[string]interface{}) {
		called = true
	}

	LogProfileInfo(ProfileName("nonexistent"), mockLog)

	if called {
		t.Error("logFn should not be called for an invalid profile name")
	}
}

// TestDumpEffectiveConfigPipelineSplit verifies that pipeline split details
// are shown when enabled.
func TestDumpEffectiveConfigPipelineSplit(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueuePipelineSplitEnabled = true
	cfg.QueuePreparerCount = 2
	cfg.QueueSenderCount = 3

	result := &BuildResult{
		Config: cfg,
	}

	output := DumpEffectiveConfig(result)

	if !strings.Contains(output, "pipeline_split: true") {
		t.Error("output should show pipeline_split as true")
	}
	if !strings.Contains(output, "preparer_count: 2") {
		t.Error("output should show preparer_count when pipeline_split is enabled")
	}
	if !strings.Contains(output, "sender_count: 3") {
		t.Error("output should show sender_count when pipeline_split is enabled")
	}
}

// TestDumpEffectiveConfigPipelineSplitDisabled verifies that preparer/sender
// counts are NOT shown when pipeline split is disabled.
func TestDumpEffectiveConfigPipelineSplitDisabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueuePipelineSplitEnabled = false

	result := &BuildResult{
		Config: cfg,
	}

	output := DumpEffectiveConfig(result)

	if !strings.Contains(output, "pipeline_split: false") {
		t.Error("output should show pipeline_split as false")
	}
	if strings.Contains(output, "preparer_count:") {
		t.Error("output should NOT show preparer_count when pipeline_split is disabled")
	}
	if strings.Contains(output, "sender_count:") {
		t.Error("output should NOT show sender_count when pipeline_split is disabled")
	}
}
