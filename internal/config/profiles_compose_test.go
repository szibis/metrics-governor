package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// composeService represents a minimal subset of docker-compose service config.
type composeService struct {
	Environment []string `yaml:"environment"`
}

type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

// composeOverridesDir returns the path to compose_overrides/ relative to the repo root.
func composeOverridesDir(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	// Navigate from internal/config/profiles_compose_test.go → repo root
	repoRoot := filepath.Join(filepath.Dir(filename), "..", "..")
	dir := filepath.Join(repoRoot, "compose_overrides")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Skipf("compose_overrides directory not found at %s", dir)
	}
	return dir
}

// parseEnvValue extracts the value for a given key from environment list.
func parseEnvValue(envList []string, key string) (string, bool) {
	prefix := key + "="
	for _, e := range envList {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimPrefix(e, prefix), true
		}
	}
	return "", false
}

// parseMaxThroughputDPS extracts the numeric dps value from a MaxThroughput string like "~80k dps".
// Handles formats: "~80k dps", "~500k+ dps", "~10k dps"
func parseMaxThroughputDPS(s string) (int, error) {
	s = strings.TrimPrefix(s, "~")
	s = strings.TrimSuffix(s, " dps")
	s = strings.TrimSuffix(s, "+")

	multiplier := 1
	if strings.HasSuffix(s, "k") {
		s = strings.TrimSuffix(s, "k")
		multiplier = 1000
	}

	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w", s, err)
	}
	return v * multiplier, nil
}

// profileForCompose maps compose override filenames to profile names.
var profileForCompose = map[string]ProfileName{
	"profile-observable.yaml":  ProfileObservable,
	"profile-safety.yaml":      ProfileSafety,
	"profile-balanced.yaml":    ProfileBalanced,
	"profile-resilient.yaml":   ProfileResilient,
	"profile-performance.yaml": ProfilePerformance,
	"profile-minimal.yaml":     ProfileMinimal,
}

func TestComposeConfigs_WithinProfileRating(t *testing.T) {
	dir := composeOverridesDir(t)

	for filename, profileName := range profileForCompose {
		t.Run(string(profileName), func(t *testing.T) {
			path := filepath.Join(dir, filename)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("file not found: %s", path)
				return
			}

			var cf composeFile
			if err := yaml.Unmarshal(data, &cf); err != nil {
				t.Fatalf("failed to parse %s: %v", filename, err)
			}

			gen, ok := cf.Services["metrics-generator"]
			if !ok {
				t.Skip("no metrics-generator service in compose file")
				return
			}

			targetDPSStr, found := parseEnvValue(gen.Environment, "TARGET_DATAPOINTS_PER_SEC")
			if !found {
				t.Skip("TARGET_DATAPOINTS_PER_SEC not set in compose override")
				return
			}

			targetDPS, err := strconv.Atoi(targetDPSStr)
			if err != nil {
				t.Fatalf("invalid TARGET_DATAPOINTS_PER_SEC: %q", targetDPSStr)
			}

			profile, err := GetProfile(profileName)
			if err != nil {
				t.Fatalf("failed to get profile %s: %v", profileName, err)
			}

			maxDPS, err := parseMaxThroughputDPS(profile.MaxThroughput)
			if err != nil {
				t.Fatalf("failed to parse MaxThroughput %q: %v", profile.MaxThroughput, err)
			}

			// Compose target should be <= 75% of profile max throughput.
			// Exception: minimal (no spikes, simple test) and performance (rated 500k+ with
			// pipeline split headroom) are allowed to test at 100%.
			maxRatio := 0.75
			if profileName == ProfileMinimal || profileName == ProfilePerformance {
				maxRatio = 1.0
			}
			limit := int(float64(maxDPS) * maxRatio)
			if targetDPS > limit {
				t.Errorf("compose TARGET_DATAPOINTS_PER_SEC=%d exceeds %.0f%% of profile max (%d dps, limit=%d)",
					targetDPS, maxRatio*100, maxDPS, limit)
			}
		})
	}
}

func TestComposeConfigs_VerifierThresholdAppropriate(t *testing.T) {
	dir := composeOverridesDir(t)

	for filename, profileName := range profileForCompose {
		t.Run(string(profileName), func(t *testing.T) {
			path := filepath.Join(dir, filename)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Skipf("file not found: %s", path)
				return
			}

			var cf composeFile
			if err := yaml.Unmarshal(data, &cf); err != nil {
				t.Fatalf("failed to parse %s: %v", filename, err)
			}

			verifier, ok := cf.Services["verifier"]
			if !ok {
				t.Skip("no verifier service in compose file")
				return
			}

			thresholdStr, found := parseEnvValue(verifier.Environment, "PASS_THRESHOLD")
			if !found {
				t.Skip("PASS_THRESHOLD not set")
				return
			}

			threshold, err := strconv.ParseFloat(thresholdStr, 64)
			if err != nil {
				t.Fatalf("invalid PASS_THRESHOLD: %q", thresholdStr)
			}

			// Non-minimal profiles should have >= 95% threshold
			// Minimal is a dev/testing profile with intentionally relaxed threshold
			if profileName != ProfileMinimal && threshold < 95.0 {
				t.Errorf("PASS_THRESHOLD=%.1f for %s is below minimum 95%%", threshold, profileName)
			}

			// Disk-based safety profiles should have >= 98%
			if profileName == ProfileSafety && threshold < 98.0 {
				t.Errorf("safety profile PASS_THRESHOLD=%.1f should be >= 98%%", threshold)
			}
		})
	}
}

func TestParseMaxThroughputDPS(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"~10k dps", 10000},
		{"~80k dps", 80000},
		{"~100k dps", 100000},
		{"~150k dps", 150000},
		{"~500k+ dps", 500000},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := parseMaxThroughputDPS(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("parseMaxThroughputDPS(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
