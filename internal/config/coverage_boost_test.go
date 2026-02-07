package config

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// TenancyConfig (0% coverage)
// ---------------------------------------------------------------------------

func TestTenancyConfig_AllFields(t *testing.T) {
	cfg := &Config{
		TenancyEnabled:         true,
		TenancyMode:            "header",
		TenancyHeaderName:      "X-Tenant-ID",
		TenancyLabelName:       "tenant",
		TenancyAttributeKey:    "tenant.id",
		TenancyDefaultTenant:   "default",
		TenancyInjectLabel:     true,
		TenancyInjectLabelName: "__tenant__",
		TenancyStripSource:     true,
		TenancyConfigFile:      "/etc/tenancy.yaml",
	}

	tc := cfg.TenancyConfig()

	if !tc.Enabled {
		t.Error("expected Enabled true")
	}
	if tc.Mode != "header" {
		t.Errorf("expected Mode 'header', got %q", tc.Mode)
	}
	if tc.HeaderName != "X-Tenant-ID" {
		t.Errorf("expected HeaderName 'X-Tenant-ID', got %q", tc.HeaderName)
	}
	if tc.LabelName != "tenant" {
		t.Errorf("expected LabelName 'tenant', got %q", tc.LabelName)
	}
	if tc.AttributeKey != "tenant.id" {
		t.Errorf("expected AttributeKey 'tenant.id', got %q", tc.AttributeKey)
	}
	if tc.DefaultTenant != "default" {
		t.Errorf("expected DefaultTenant 'default', got %q", tc.DefaultTenant)
	}
	if !tc.InjectLabel {
		t.Error("expected InjectLabel true")
	}
	if tc.InjectLabelName != "__tenant__" {
		t.Errorf("expected InjectLabelName '__tenant__', got %q", tc.InjectLabelName)
	}
	if !tc.StripSource {
		t.Error("expected StripSource true")
	}
	if tc.ConfigFile != "/etc/tenancy.yaml" {
		t.Errorf("expected ConfigFile '/etc/tenancy.yaml', got %q", tc.ConfigFile)
	}
}

func TestTenancyConfig_Defaults(t *testing.T) {
	cfg := &Config{}

	tc := cfg.TenancyConfig()

	if tc.Enabled {
		t.Error("expected Enabled false for zero config")
	}
	if tc.Mode != "" {
		t.Errorf("expected empty Mode, got %q", tc.Mode)
	}
	if tc.HeaderName != "" {
		t.Errorf("expected empty HeaderName, got %q", tc.HeaderName)
	}
}

// ---------------------------------------------------------------------------
// GetVersion (0% coverage)
// ---------------------------------------------------------------------------

func TestGetVersion(t *testing.T) {
	v := GetVersion()
	// version is a package-level var; by default "dev" in source
	if v == "" {
		t.Error("expected non-empty version string")
	}
	// In test builds, the default should be "dev"
	if v != "dev" {
		t.Logf("version = %q (may differ with ldflags)", v)
	}
}

// ---------------------------------------------------------------------------
// ByteSize.MarshalYAML (0% coverage in yaml.go:293)
// ---------------------------------------------------------------------------

func TestByteSize_MarshalYAML(t *testing.T) {
	tests := []struct {
		name string
		size ByteSize
		want string
	}{
		{"zero", ByteSize(0), "0"},
		{"ki_aligned", ByteSize(1024), "1Ki"},
		{"mi_aligned", ByteSize(1048576), "1Mi"},
		{"gi_aligned", ByteSize(1073741824), "1Gi"},
		{"ti_aligned", ByteSize(1099511627776), "1Ti"},
		{"non_aligned", ByteSize(1500), "1500"},
		{"256mi", ByteSize(268435456), "256Mi"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			iface, err := tt.size.MarshalYAML()
			if err != nil {
				t.Fatalf("MarshalYAML error: %v", err)
			}
			s, ok := iface.(string)
			if !ok {
				t.Fatalf("expected string, got %T", iface)
			}
			if s != tt.want {
				t.Errorf("MarshalYAML() = %q, want %q", s, tt.want)
			}
		})
	}
}

func TestByteSize_MarshalYAML_RoundTrip(t *testing.T) {
	sizes := []ByteSize{0, 1024, 262144, 1048576, 1073741824}

	for _, bs := range sizes {
		iface, err := bs.MarshalYAML()
		if err != nil {
			t.Fatalf("MarshalYAML error for %d: %v", bs, err)
		}
		str := iface.(string)

		var result ByteSize
		node := &yaml.Node{Kind: yaml.ScalarNode, Value: str}
		err = result.UnmarshalYAML(node)
		if err != nil {
			t.Fatalf("UnmarshalYAML error for %q: %v", str, err)
		}
		if result != bs {
			t.Errorf("round-trip failed: %d -> %q -> %d", bs, str, result)
		}
	}
}

// ---------------------------------------------------------------------------
// byteSizeFlag.String() nil target (66.7% coverage)
// ---------------------------------------------------------------------------

func TestByteSizeFlag_String_NilTarget(t *testing.T) {
	// Test the nil target branch of byteSizeFlag.String()
	b := &byteSizeFlag{target: nil}
	if s := b.String(); s != "0" {
		t.Errorf("expected '0' for nil target, got %q", s)
	}
}

func TestByteSizeFlag_String_NonNil(t *testing.T) {
	v := int64(1048576)
	b := &byteSizeFlag{target: &v}
	s := b.String()
	if s != "1Mi" {
		t.Errorf("expected '1Mi', got %q", s)
	}
}

func TestByteSizeIntFlag_String_NilTarget(t *testing.T) {
	// Test the nil target branch of byteSizeIntFlag.String()
	b := &byteSizeIntFlag{target: nil}
	if s := b.String(); s != "0" {
		t.Errorf("expected '0' for nil target, got %q", s)
	}
}

func TestByteSizeIntFlag_String_NonNil(t *testing.T) {
	v := int(262144)
	b := &byteSizeIntFlag{target: &v}
	s := b.String()
	if s != "256Ki" {
		t.Errorf("expected '256Ki', got %q", s)
	}
}

// ---------------------------------------------------------------------------
// byteSizeFlag.Set() and byteSizeIntFlag.Set() error cases (80% coverage)
// ---------------------------------------------------------------------------

func TestByteSizeFlag_Set_Invalid(t *testing.T) {
	v := int64(0)
	b := &byteSizeFlag{target: &v}
	err := b.Set("invalidXYZ")
	if err == nil {
		t.Error("expected error for invalid byte size string")
	}
}

func TestByteSizeFlag_Set_Valid(t *testing.T) {
	v := int64(0)
	b := &byteSizeFlag{target: &v}
	err := b.Set("256Mi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 268435456 {
		t.Errorf("expected 268435456, got %d", v)
	}
}

func TestByteSizeFlag_Get(t *testing.T) {
	v := int64(1024)
	b := &byteSizeFlag{target: &v}
	got := b.Get()
	if got != int64(1024) {
		t.Errorf("expected 1024, got %v", got)
	}
}

func TestByteSizeIntFlag_Set_Invalid(t *testing.T) {
	v := int(0)
	b := &byteSizeIntFlag{target: &v}
	err := b.Set("notabyte")
	if err == nil {
		t.Error("expected error for invalid byte size string")
	}
}

func TestByteSizeIntFlag_Set_Valid(t *testing.T) {
	v := int(0)
	b := &byteSizeIntFlag{target: &v}
	err := b.Set("1Mi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != 1048576 {
		t.Errorf("expected 1048576, got %d", v)
	}
}

func TestByteSizeIntFlag_Get(t *testing.T) {
	v := int(512)
	b := &byteSizeIntFlag{target: &v}
	got := b.Get()
	if got != int(512) {
		t.Errorf("expected 512, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// addWarnings: exporter TLS file warnings (61.1% coverage)
// The tests in validate_test.go already cover receiver TLS warnings,
// insecure endpoint, large buffer, and batch > buffer. What's missing
// is the exporter TLS file warning path.
// ---------------------------------------------------------------------------

func TestAddWarnings_ExporterTLSFileNotFound(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ExporterTLSEnabled = true
	cfg.ExporterTLSCertFile = "/nonexistent/exporter.crt"
	cfg.ExporterTLSKeyFile = "/nonexistent/exporter.key"
	cfg.ExporterTLSCAFile = "/nonexistent/exporter-ca.crt"

	result := &ValidationResult{Valid: true}
	addWarnings(cfg, result)

	warnCount := 0
	for _, issue := range result.Issues {
		if issue.Severity == SeverityWarning && strings.Contains(issue.Message, "file not found") {
			warnCount++
		}
	}
	if warnCount != 3 {
		t.Errorf("expected 3 TLS file warnings, got %d", warnCount)
		for _, issue := range result.Issues {
			t.Logf("  issue: %s: %s: %s", issue.Severity, issue.Field, issue.Message)
		}
	}
}

func TestAddWarnings_ExporterTLSPartialFiles(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ExporterTLSEnabled = true
	cfg.ExporterTLSCertFile = "/nonexistent/exporter.crt"
	cfg.ExporterTLSKeyFile = "" // empty, should skip
	cfg.ExporterTLSCAFile = ""  // empty, should skip

	result := &ValidationResult{Valid: true}
	addWarnings(cfg, result)

	warnCount := 0
	for _, issue := range result.Issues {
		if issue.Severity == SeverityWarning && strings.Contains(issue.Message, "file not found") {
			warnCount++
		}
	}
	if warnCount != 1 {
		t.Errorf("expected 1 TLS file warning (only cert), got %d", warnCount)
	}
}

func TestAddWarnings_ExporterTLSWithExistingFiles(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")
	os.WriteFile(certFile, []byte("cert"), 0644)
	os.WriteFile(keyFile, []byte("key"), 0644)

	cfg := DefaultConfig()
	cfg.ExporterTLSEnabled = true
	cfg.ExporterTLSCertFile = certFile
	cfg.ExporterTLSKeyFile = keyFile
	cfg.ExporterTLSCAFile = "" // no CA file specified

	result := &ValidationResult{Valid: true}
	addWarnings(cfg, result)

	// Should have no TLS file warnings since files exist
	for _, issue := range result.Issues {
		if strings.Contains(issue.Message, "file not found") {
			t.Errorf("unexpected file not found warning: %s", issue.Message)
		}
	}
}

func TestAddWarnings_ReceiverTLSWithCAFile(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ReceiverTLSEnabled = true
	cfg.ReceiverTLSCertFile = "/nonexistent/receiver.crt"
	cfg.ReceiverTLSKeyFile = "/nonexistent/receiver.key"
	cfg.ReceiverTLSCAFile = "/nonexistent/receiver-ca.crt"

	result := &ValidationResult{Valid: true}
	addWarnings(cfg, result)

	warnCount := 0
	for _, issue := range result.Issues {
		if issue.Severity == SeverityWarning && strings.Contains(issue.Message, "file not found") {
			warnCount++
		}
	}
	if warnCount != 3 {
		t.Errorf("expected 3 TLS file warnings (cert, key, CA), got %d", warnCount)
	}
}

func TestAddWarnings_InsecureNonLocalhost(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ExporterInsecure = true
	cfg.ExporterEndpoint = "remote-backend:4317"

	result := &ValidationResult{Valid: true}
	addWarnings(cfg, result)

	found := false
	for _, issue := range result.Issues {
		if issue.Severity == SeverityWarning && strings.Contains(issue.Message, "insecure") {
			found = true
		}
	}
	if !found {
		t.Error("expected insecure endpoint warning")
	}
}

func TestAddWarnings_InsecureLocalhost_NoWarning(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ExporterInsecure = true
	cfg.ExporterEndpoint = "localhost:4317"

	result := &ValidationResult{Valid: true}
	addWarnings(cfg, result)

	for _, issue := range result.Issues {
		if strings.Contains(issue.Message, "insecure") {
			t.Error("should not warn about insecure for localhost")
		}
	}
}

func TestAddWarnings_LargeBufferSize(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BufferSize = 2000000

	result := &ValidationResult{Valid: true}
	addWarnings(cfg, result)

	found := false
	for _, issue := range result.Issues {
		if issue.Severity == SeverityWarning && strings.Contains(issue.Message, "large buffer") {
			found = true
		}
	}
	if !found {
		t.Error("expected large buffer warning")
	}
}

func TestAddWarnings_BatchLargerThanBuffer(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BufferSize = 100
	cfg.MaxBatchSize = 500

	result := &ValidationResult{Valid: true}
	addWarnings(cfg, result)

	found := false
	for _, issue := range result.Issues {
		if issue.Severity == SeverityWarning && strings.Contains(issue.Message, "batch_size") {
			found = true
		}
	}
	if !found {
		t.Error("expected batch > buffer warning")
	}
}

func TestCheckFileWarning_EmptyPath(t *testing.T) {
	result := &ValidationResult{Valid: true}
	checkFileWarning("", "test.field", result)
	if len(result.Issues) != 0 {
		t.Errorf("expected no issues for empty path, got %d", len(result.Issues))
	}
}

func TestCheckFileWarning_NonexistentFile(t *testing.T) {
	result := &ValidationResult{Valid: true}
	checkFileWarning("/nonexistent/file.pem", "test.field", result)
	if len(result.Issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(result.Issues))
	}
	if result.Issues[0].Severity != SeverityWarning {
		t.Errorf("expected warning severity, got %s", result.Issues[0].Severity)
	}
	if result.Issues[0].Field != "test.field" {
		t.Errorf("expected field 'test.field', got %q", result.Issues[0].Field)
	}
}

func TestCheckFileWarning_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "exists.pem")
	os.WriteFile(f, []byte("data"), 0644)

	result := &ValidationResult{Valid: true}
	checkFileWarning(f, "test.field", result)
	if len(result.Issues) != 0 {
		t.Errorf("expected no issues for existing file, got %d", len(result.Issues))
	}
}

// ---------------------------------------------------------------------------
// applyFlagOverrides: uncovered paths
// Focus on tenancy flags, telemetry flags, queue resilience flags,
// and stats-log flags which are likely not covered yet.
// ---------------------------------------------------------------------------

func TestParseFlagsTenancy(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-tenancy-enabled",
		"-tenancy-mode", "header",
		"-tenancy-header-name", "X-Tenant-ID",
		"-tenancy-label-name", "tenant",
		"-tenancy-attribute-key", "tenant.id",
		"-tenancy-default-tenant", "default",
		"-tenancy-inject-label",
		"-tenancy-inject-label-name", "__tenant__",
		"-tenancy-strip-source",
		"-tenancy-config", "/etc/tenancy.yaml",
	}

	cfg := ParseFlags()

	if !cfg.TenancyEnabled {
		t.Error("expected TenancyEnabled true")
	}
	if cfg.TenancyMode != "header" {
		t.Errorf("expected TenancyMode 'header', got %q", cfg.TenancyMode)
	}
	if cfg.TenancyHeaderName != "X-Tenant-ID" {
		t.Errorf("expected TenancyHeaderName 'X-Tenant-ID', got %q", cfg.TenancyHeaderName)
	}
	if cfg.TenancyLabelName != "tenant" {
		t.Errorf("expected TenancyLabelName 'tenant', got %q", cfg.TenancyLabelName)
	}
	if cfg.TenancyAttributeKey != "tenant.id" {
		t.Errorf("expected TenancyAttributeKey 'tenant.id', got %q", cfg.TenancyAttributeKey)
	}
	if cfg.TenancyDefaultTenant != "default" {
		t.Errorf("expected TenancyDefaultTenant 'default', got %q", cfg.TenancyDefaultTenant)
	}
	if !cfg.TenancyInjectLabel {
		t.Error("expected TenancyInjectLabel true")
	}
	if cfg.TenancyInjectLabelName != "__tenant__" {
		t.Errorf("expected TenancyInjectLabelName '__tenant__', got %q", cfg.TenancyInjectLabelName)
	}
	if !cfg.TenancyStripSource {
		t.Error("expected TenancyStripSource true")
	}
	if cfg.TenancyConfigFile != "/etc/tenancy.yaml" {
		t.Errorf("expected TenancyConfigFile '/etc/tenancy.yaml', got %q", cfg.TenancyConfigFile)
	}
}

func TestParseFlagsQueueResilience(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-queue-batch-drain-size", "20",
		"-queue-burst-drain-size", "200",
		"-queue-retry-timeout", "3s",
		"-queue-close-timeout", "45s",
		"-queue-drain-timeout", "20s",
		"-queue-drain-entry-timeout", "2s",
		"-exporter-dial-timeout", "15s",
	}

	cfg := ParseFlags()

	if cfg.QueueBatchDrainSize != 20 {
		t.Errorf("expected QueueBatchDrainSize 20, got %d", cfg.QueueBatchDrainSize)
	}
	if cfg.QueueBurstDrainSize != 200 {
		t.Errorf("expected QueueBurstDrainSize 200, got %d", cfg.QueueBurstDrainSize)
	}
	if cfg.QueueRetryTimeout != 3*time.Second {
		t.Errorf("expected QueueRetryTimeout 3s, got %v", cfg.QueueRetryTimeout)
	}
	if cfg.QueueCloseTimeout != 45*time.Second {
		t.Errorf("expected QueueCloseTimeout 45s, got %v", cfg.QueueCloseTimeout)
	}
	if cfg.QueueDrainTimeout != 20*time.Second {
		t.Errorf("expected QueueDrainTimeout 20s, got %v", cfg.QueueDrainTimeout)
	}
	if cfg.QueueDrainEntryTimeout != 2*time.Second {
		t.Errorf("expected QueueDrainEntryTimeout 2s, got %v", cfg.QueueDrainEntryTimeout)
	}
	if cfg.ExporterDialTimeout != 15*time.Second {
		t.Errorf("expected ExporterDialTimeout 15s, got %v", cfg.ExporterDialTimeout)
	}
}

func TestParseFlagsTelemetry(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-telemetry-timeout", "5s",
		"-telemetry-push-interval", "15s",
		"-telemetry-compression", "gzip",
		"-telemetry-shutdown-timeout", "3s",
		"-telemetry-retry-enabled",
		"-telemetry-retry-initial", "2s",
		"-telemetry-retry-max-interval", "20s",
		"-telemetry-retry-max-elapsed", "45s",
		"-telemetry-headers", "X-API-Key=abc123",
	}

	cfg := ParseFlags()

	if cfg.TelemetryTimeout != 5*time.Second {
		t.Errorf("expected TelemetryTimeout 5s, got %v", cfg.TelemetryTimeout)
	}
	if cfg.TelemetryPushInterval != 15*time.Second {
		t.Errorf("expected TelemetryPushInterval 15s, got %v", cfg.TelemetryPushInterval)
	}
	if cfg.TelemetryCompression != "gzip" {
		t.Errorf("expected TelemetryCompression 'gzip', got %q", cfg.TelemetryCompression)
	}
	if cfg.TelemetryShutdownTimeout != 3*time.Second {
		t.Errorf("expected TelemetryShutdownTimeout 3s, got %v", cfg.TelemetryShutdownTimeout)
	}
	if !cfg.TelemetryRetryEnabled {
		t.Error("expected TelemetryRetryEnabled true")
	}
	if cfg.TelemetryRetryInitial != 2*time.Second {
		t.Errorf("expected TelemetryRetryInitial 2s, got %v", cfg.TelemetryRetryInitial)
	}
	if cfg.TelemetryRetryMaxInterval != 20*time.Second {
		t.Errorf("expected TelemetryRetryMaxInterval 20s, got %v", cfg.TelemetryRetryMaxInterval)
	}
	if cfg.TelemetryRetryMaxElapsed != 45*time.Second {
		t.Errorf("expected TelemetryRetryMaxElapsed 45s, got %v", cfg.TelemetryRetryMaxElapsed)
	}
	if cfg.TelemetryHeaders != "X-API-Key=abc123" {
		t.Errorf("expected TelemetryHeaders 'X-API-Key=abc123', got %q", cfg.TelemetryHeaders)
	}
}

func TestParseFlagsStatsLogging(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-stats-log-interval", "30s",
		"-limits-log-interval", "60s",
		"-limits-log-individual",
		"-limits-stats-threshold", "100",
	}

	cfg := ParseFlags()

	if cfg.StatsLogInterval != 30*time.Second {
		t.Errorf("expected StatsLogInterval 30s, got %v", cfg.StatsLogInterval)
	}
	if cfg.LimitsLogInterval != 60*time.Second {
		t.Errorf("expected LimitsLogInterval 60s, got %v", cfg.LimitsLogInterval)
	}
	if !cfg.LimitsLogIndividual {
		t.Error("expected LimitsLogIndividual true")
	}
	if cfg.LimitsStatsThreshold != 100 {
		t.Errorf("expected LimitsStatsThreshold 100, got %d", cfg.LimitsStatsThreshold)
	}
}

func TestParseFlagsCardinalityHLL(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-cardinality-mode", "hybrid",
		"-cardinality-hll-threshold", "50000",
		"-cardinality-hll-precision", "14",
	}

	cfg := ParseFlags()

	if cfg.CardinalityMode != "hybrid" {
		t.Errorf("expected CardinalityMode 'hybrid', got %q", cfg.CardinalityMode)
	}
	if cfg.CardinalityHLLThreshold != 50000 {
		t.Errorf("expected CardinalityHLLThreshold 50000, got %d", cfg.CardinalityHLLThreshold)
	}
	if cfg.CardinalityHLLPrecision != 14 {
		t.Errorf("expected CardinalityHLLPrecision 14, got %d", cfg.CardinalityHLLPrecision)
	}
}

func TestParseFlagsPprof(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-pprof-enabled",
	}

	cfg := ParseFlags()

	if !cfg.PprofEnabled {
		t.Error("expected PprofEnabled true")
	}
}

func TestParseFlagsQueueWriteBufferAndCompression(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-queue-write-buffer-size", "524288",
		"-queue-compression", "snappy",
	}

	cfg := ParseFlags()

	if cfg.QueueWriteBufferSize != 524288 {
		t.Errorf("expected QueueWriteBufferSize 524288, got %d", cfg.QueueWriteBufferSize)
	}
	if cfg.QueueCompression != "snappy" {
		t.Errorf("expected QueueCompression 'snappy', got %q", cfg.QueueCompression)
	}
}

func TestParseFlagsRelabelAndSampling(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-relabel-config", "/etc/relabel.yaml",
		"-sampling-config", "/etc/sampling.yaml",
	}

	cfg := ParseFlags()

	if cfg.RelabelConfig != "/etc/relabel.yaml" {
		t.Errorf("expected RelabelConfig '/etc/relabel.yaml', got %q", cfg.RelabelConfig)
	}
	if cfg.SamplingConfig != "/etc/sampling.yaml" {
		t.Errorf("expected SamplingConfig '/etc/sampling.yaml', got %q", cfg.SamplingConfig)
	}
}

func TestParseFlagsMaxBatchBytes(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-max-batch-bytes", "8388608",
	}

	cfg := ParseFlags()

	if cfg.MaxBatchBytes != 8388608 {
		t.Errorf("expected MaxBatchBytes 8388608, got %d", cfg.MaxBatchBytes)
	}
}

func TestParseFlagsQueueType(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-queue-type", "disk",
	}

	cfg := ParseFlags()

	if cfg.QueueType != "disk" {
		t.Errorf("expected QueueType 'disk', got %q", cfg.QueueType)
	}
}

// ---------------------------------------------------------------------------
// Validate: exercise uncovered validation paths (90.6% coverage)
// ---------------------------------------------------------------------------

func TestValidate_BackoffMultiplierTooLow(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueueBackoffEnabled = true
	cfg.QueueBackoffMultiplier = 0.5 // must be > 1

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for low backoff multiplier")
	}
	if !strings.Contains(err.Error(), "queue-backoff-multiplier") {
		t.Errorf("expected error about backoff multiplier, got: %v", err)
	}
}

func TestValidate_BloomCompressionLevelOutOfRange(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BloomPersistenceCompressionLevel = 15

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for out-of-range bloom compression level")
	}
	if !strings.Contains(err.Error(), "bloom-persistence-compression-level") {
		t.Errorf("expected error about bloom compression level, got: %v", err)
	}
}

func TestValidate_InvalidQueueType(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueueType = "invalid"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for invalid queue type")
	}
	if !strings.Contains(err.Error(), "queue-type") {
		t.Errorf("expected error about queue-type, got: %v", err)
	}
}

func TestValidate_InvalidQueueFullBehavior(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueueFullBehavior = "invalid"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for invalid queue-full-behavior")
	}
	if !strings.Contains(err.Error(), "queue-full-behavior") {
		t.Errorf("expected error about queue-full-behavior, got: %v", err)
	}
}

func TestValidate_InvalidQueueTargetUtilization(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueueTargetUtilization = 2.0

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for out-of-range queue-target-utilization")
	}
	if !strings.Contains(err.Error(), "queue-target-utilization") {
		t.Errorf("expected error about queue-target-utilization, got: %v", err)
	}
}

func TestValidate_InvalidQueueCompactThreshold(t *testing.T) {
	cfg := DefaultConfig()
	cfg.QueueCompactThreshold = -0.5

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for negative queue-compact-threshold")
	}
	if !strings.Contains(err.Error(), "queue-compact-threshold") {
		t.Errorf("expected error about queue-compact-threshold, got: %v", err)
	}
}

func TestValidate_MultipleErrorsCombined(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MemoryLimitRatio = 2.0
	cfg.ExporterProtocol = "invalid"
	cfg.QueueType = "invalid"

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation errors")
	}
	msg := err.Error()
	if !strings.Contains(msg, "memory-limit-ratio") {
		t.Error("expected memory-limit-ratio error in message")
	}
	if !strings.Contains(msg, "exporter-protocol") {
		t.Error("expected exporter-protocol error in message")
	}
	if !strings.Contains(msg, "queue-type") {
		t.Error("expected queue-type error in message")
	}
}

// ---------------------------------------------------------------------------
// ByteSize UnmarshalYAML edge case (75% coverage in yaml.go:267)
// ---------------------------------------------------------------------------

func TestByteSize_UnmarshalYAML_EmptyString(t *testing.T) {
	var b ByteSize
	node := &yaml.Node{Kind: yaml.ScalarNode, Value: ""}
	err := b.UnmarshalYAML(node)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b != 0 {
		t.Errorf("expected 0 for empty string, got %d", b)
	}
}

func TestByteSize_UnmarshalYAML_Invalid(t *testing.T) {
	var b ByteSize
	node := &yaml.Node{Kind: yaml.ScalarNode, Value: "invalidXYZ"}
	err := b.UnmarshalYAML(node)
	if err == nil {
		t.Error("expected error for invalid byte size")
	}
}

func TestByteSize_UnmarshalYAML_IntegerNode(t *testing.T) {
	var b ByteSize
	node := &yaml.Node{Kind: yaml.ScalarNode, Value: "1048576", Tag: "!!int"}
	err := b.UnmarshalYAML(node)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b != 1048576 {
		t.Errorf("expected 1048576, got %d", b)
	}
}

func TestByteSize_UnmarshalYAML_StringNotation(t *testing.T) {
	var b ByteSize
	node := &yaml.Node{Kind: yaml.ScalarNode, Value: "256Mi"}
	err := b.UnmarshalYAML(node)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b != 268435456 {
		t.Errorf("expected 268435456, got %d", b)
	}
}

// ---------------------------------------------------------------------------
// ValidateFile integration test with exporter TLS warnings
// ---------------------------------------------------------------------------

func TestValidateFile_Warnings_ExporterTLSFileNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
exporter:
  endpoint: "localhost:4317"
  protocol: "grpc"
  tls:
    enabled: true
    cert_file: "/nonexistent/exporter.crt"
    key_file: "/nonexistent/exporter.key"
    ca_file: "/nonexistent/exporter-ca.crt"
`), 0644)

	result := ValidateFile(path)

	warnCount := 0
	for _, issue := range result.Issues {
		if issue.Severity == SeverityWarning && strings.Contains(issue.Message, "file not found") {
			warnCount++
		}
	}
	if warnCount < 3 {
		t.Errorf("expected at least 3 exporter TLS file warnings, got %d: %s", warnCount, result.JSON())
	}
}

func TestParseFlagsHTTPReceiverPath(t *testing.T) {
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ContinueOnError)

	oldArgs := os.Args
	defer func() { os.Args = oldArgs }()

	os.Args = []string{
		"test",
		"-http-receiver-path", "/custom/metrics",
	}

	cfg := ParseFlags()

	if cfg.HTTPReceiverPath != "/custom/metrics" {
		t.Errorf("expected HTTPReceiverPath '/custom/metrics', got %q", cfg.HTTPReceiverPath)
	}
}
