package tenant

import (
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// --- Test Helpers ---

func makeStringKV(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key:   key,
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}},
	}
}

func makeGaugeRM(resourceAttrs map[string]string, metricName string, dpAttrs map[string]string) *metricspb.ResourceMetrics {
	kvs := make([]*commonpb.KeyValue, 0, len(resourceAttrs))
	for k, v := range resourceAttrs {
		kvs = append(kvs, makeStringKV(k, v))
	}
	dpKVs := make([]*commonpb.KeyValue, 0, len(dpAttrs))
	for k, v := range dpAttrs {
		dpKVs = append(dpKVs, makeStringKV(k, v))
	}
	return &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{Attributes: kvs},
		ScopeMetrics: []*metricspb.ScopeMetrics{{
			Metrics: []*metricspb.Metric{{
				Name: metricName,
				Data: &metricspb.Metric_Gauge{
					Gauge: &metricspb.Gauge{
						DataPoints: []*metricspb.NumberDataPoint{{
							Attributes: dpKVs,
						}},
					},
				},
			}},
		}},
	}
}

func makeSumRM(resourceAttrs map[string]string, metricName string, dpAttrs map[string]string) *metricspb.ResourceMetrics {
	kvs := make([]*commonpb.KeyValue, 0, len(resourceAttrs))
	for k, v := range resourceAttrs {
		kvs = append(kvs, makeStringKV(k, v))
	}
	dpKVs := make([]*commonpb.KeyValue, 0, len(dpAttrs))
	for k, v := range dpAttrs {
		dpKVs = append(dpKVs, makeStringKV(k, v))
	}
	return &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{Attributes: kvs},
		ScopeMetrics: []*metricspb.ScopeMetrics{{
			Metrics: []*metricspb.Metric{{
				Name: metricName,
				Data: &metricspb.Metric_Sum{
					Sum: &metricspb.Sum{
						DataPoints: []*metricspb.NumberDataPoint{{
							Attributes: dpKVs,
						}},
					},
				},
			}},
		}},
	}
}

// --- Config Validation Tests ---

func TestConfig_Validate_Disabled(t *testing.T) {
	cfg := Config{Enabled: false}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("disabled config should validate: %v", err)
	}
}

func TestConfig_Validate_HeaderMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default header config should validate: %v", err)
	}
	cfg.HeaderName = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("empty header_name should fail validation")
	}
}

func TestConfig_Validate_LabelMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeLabel
	if err := cfg.Validate(); err != nil {
		t.Fatalf("label config should validate: %v", err)
	}
	cfg.LabelName = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("empty label_name should fail validation")
	}
}

func TestConfig_Validate_AttributeMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeAttribute
	if err := cfg.Validate(); err != nil {
		t.Fatalf("attribute config should validate: %v", err)
	}
	cfg.AttributeKey = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("empty attribute_key should fail validation")
	}
}

func TestConfig_Validate_UnknownMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = "magic"
	if err := cfg.Validate(); err == nil {
		t.Fatal("unknown mode should fail validation")
	}
}

func TestConfig_Validate_EmptyDefaultTenant(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.DefaultTenant = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("empty default_tenant should fail validation")
	}
}

func TestConfig_Validate_InjectLabelNoName(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.InjectLabel = true
	cfg.InjectLabelName = ""
	if err := cfg.Validate(); err == nil {
		t.Fatal("inject_label without inject_label_name should fail")
	}
}

// --- Detector Creation Tests ---

func TestNewDetector_Valid(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	d, err := NewDetector(cfg)
	if err != nil {
		t.Fatalf("valid config should create detector: %v", err)
	}
	if !d.Enabled() {
		t.Fatal("detector should be enabled")
	}
}

func TestNewDetector_InvalidConfig(t *testing.T) {
	cfg := Config{Enabled: true, Mode: "invalid"}
	_, err := NewDetector(cfg)
	if err == nil {
		t.Fatal("invalid config should fail detector creation")
	}
}

func TestNewDetector_Disabled(t *testing.T) {
	cfg := Config{Enabled: false}
	d, err := NewDetector(cfg)
	if err != nil {
		t.Fatalf("disabled config should create detector: %v", err)
	}
	if d.Enabled() {
		t.Fatal("detector should not be enabled")
	}
}

// --- Header Mode Detection Tests ---

func TestDetect_HeaderMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeHeader
	cfg.InjectLabel = false
	d, _ := NewDetector(cfg)

	rm := makeGaugeRM(nil, "cpu", nil)
	tenant := d.Detect(rm, "team-alpha")
	if tenant != "team-alpha" {
		t.Fatalf("expected team-alpha, got %s", tenant)
	}
}

func TestDetect_HeaderMode_Fallback(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeHeader
	cfg.DefaultTenant = "fallback"
	d, _ := NewDetector(cfg)

	rm := makeGaugeRM(nil, "cpu", nil)
	tenant := d.Detect(rm, "")
	if tenant != "fallback" {
		t.Fatalf("expected fallback, got %s", tenant)
	}
}

// --- Label Mode Detection Tests ---

func TestDetect_LabelMode_Gauge(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeLabel
	cfg.LabelName = "tenant"
	cfg.InjectLabel = false
	d, _ := NewDetector(cfg)

	rm := makeGaugeRM(nil, "cpu", map[string]string{"tenant": "team-beta"})
	tenant := d.Detect(rm, "")
	if tenant != "team-beta" {
		t.Fatalf("expected team-beta, got %s", tenant)
	}
}

func TestDetect_LabelMode_Sum(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeLabel
	cfg.LabelName = "org"
	cfg.InjectLabel = false
	d, _ := NewDetector(cfg)

	rm := makeSumRM(nil, "requests_total", map[string]string{"org": "acme"})
	tenant := d.Detect(rm, "")
	if tenant != "acme" {
		t.Fatalf("expected acme, got %s", tenant)
	}
}

func TestDetect_LabelMode_Fallback(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeLabel
	cfg.LabelName = "tenant"
	cfg.DefaultTenant = "unknown"
	d, _ := NewDetector(cfg)

	rm := makeGaugeRM(nil, "cpu", map[string]string{"host": "a"})
	tenant := d.Detect(rm, "")
	if tenant != "unknown" {
		t.Fatalf("expected unknown, got %s", tenant)
	}
}

// --- Attribute Mode Detection Tests ---

func TestDetect_AttributeMode(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeAttribute
	cfg.AttributeKey = "service.namespace"
	cfg.InjectLabel = false
	d, _ := NewDetector(cfg)

	rm := makeGaugeRM(map[string]string{"service.namespace": "payments"}, "latency", nil)
	tenant := d.Detect(rm, "")
	if tenant != "payments" {
		t.Fatalf("expected payments, got %s", tenant)
	}
}

func TestDetect_AttributeMode_Fallback(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeAttribute
	cfg.AttributeKey = "service.namespace"
	cfg.DefaultTenant = "none"
	d, _ := NewDetector(cfg)

	rm := makeGaugeRM(map[string]string{"host.name": "node1"}, "mem", nil)
	tenant := d.Detect(rm, "")
	if tenant != "none" {
		t.Fatalf("expected none, got %s", tenant)
	}
}

func TestDetect_AttributeMode_NilResource(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeAttribute
	cfg.DefaultTenant = "fallback"
	d, _ := NewDetector(cfg)

	rm := &metricspb.ResourceMetrics{
		ScopeMetrics: []*metricspb.ScopeMetrics{{}},
	}
	tenant := d.Detect(rm, "")
	if tenant != "fallback" {
		t.Fatalf("expected fallback, got %s", tenant)
	}
}

// --- DetectAndAnnotate Tests ---

func TestDetectAndAnnotate_InjectsLabel(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeHeader
	cfg.InjectLabel = true
	cfg.InjectLabelName = "__tenant__"
	d, _ := NewDetector(cfg)

	rm := makeGaugeRM(nil, "cpu", map[string]string{"host": "a"})
	result := d.DetectAndAnnotate([]*metricspb.ResourceMetrics{rm}, "team-x")

	if len(result) != 1 {
		t.Fatalf("expected 1 tenant group, got %d", len(result))
	}
	rms, ok := result["team-x"]
	if !ok || len(rms) != 1 {
		t.Fatal("expected team-x group with 1 RM")
	}
	// Check injected label
	dp := rms[0].ScopeMetrics[0].Metrics[0].GetGauge().DataPoints[0]
	found := false
	for _, kv := range dp.Attributes {
		if kv.Key == "__tenant__" {
			if sv, ok := kv.Value.GetValue().(*commonpb.AnyValue_StringValue); ok && sv.StringValue == "team-x" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("__tenant__ label not injected")
	}
}

func TestDetectAndAnnotate_MultiTenant(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeAttribute
	cfg.AttributeKey = "service.namespace"
	cfg.InjectLabel = false
	d, _ := NewDetector(cfg)

	rms := []*metricspb.ResourceMetrics{
		makeGaugeRM(map[string]string{"service.namespace": "alpha"}, "cpu", nil),
		makeGaugeRM(map[string]string{"service.namespace": "beta"}, "mem", nil),
		makeGaugeRM(map[string]string{"service.namespace": "alpha"}, "disk", nil),
	}

	result := d.DetectAndAnnotate(rms, "")
	if len(result) != 2 {
		t.Fatalf("expected 2 tenant groups, got %d", len(result))
	}
	if len(result["alpha"]) != 2 {
		t.Fatalf("expected 2 RMs for alpha, got %d", len(result["alpha"]))
	}
	if len(result["beta"]) != 1 {
		t.Fatalf("expected 1 RM for beta, got %d", len(result["beta"]))
	}
}

func TestDetectAndAnnotate_StripSourceLabel(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeLabel
	cfg.LabelName = "tenant"
	cfg.StripSourceLabel = true
	cfg.InjectLabel = true
	cfg.InjectLabelName = "__tenant__"
	d, _ := NewDetector(cfg)

	rm := makeGaugeRM(nil, "cpu", map[string]string{"tenant": "team-y", "host": "a"})
	result := d.DetectAndAnnotate([]*metricspb.ResourceMetrics{rm}, "")

	rms := result["team-y"]
	dp := rms[0].ScopeMetrics[0].Metrics[0].GetGauge().DataPoints[0]
	for _, kv := range dp.Attributes {
		if kv.Key == "tenant" {
			t.Fatal("source label 'tenant' should have been stripped")
		}
	}
	// __tenant__ should be injected
	found := false
	for _, kv := range dp.Attributes {
		if kv.Key == "__tenant__" {
			found = true
		}
	}
	if !found {
		t.Fatal("__tenant__ label should be injected")
	}
}

func TestDetectAndAnnotate_LabelReplaceExisting(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeHeader
	cfg.InjectLabel = true
	cfg.InjectLabelName = "__tenant__"
	d, _ := NewDetector(cfg)

	rm := makeGaugeRM(nil, "cpu", map[string]string{"__tenant__": "old"})
	result := d.DetectAndAnnotate([]*metricspb.ResourceMetrics{rm}, "new")

	dp := result["new"][0].ScopeMetrics[0].Metrics[0].GetGauge().DataPoints[0]
	for _, kv := range dp.Attributes {
		if kv.Key == "__tenant__" {
			sv := kv.Value.GetValue().(*commonpb.AnyValue_StringValue)
			if sv.StringValue != "new" {
				t.Fatalf("expected replaced __tenant__=new, got %s", sv.StringValue)
			}
			return
		}
	}
	t.Fatal("__tenant__ label not found")
}

// --- InjectHeaderTenant Tests ---

func TestInjectHeaderTenant(t *testing.T) {
	rm := makeGaugeRM(nil, "cpu", nil)
	rms := []*metricspb.ResourceMetrics{rm}

	InjectHeaderTenant(rms, "__org_id__", "team-z")

	found := findResourceAttribute(rms[0], "__org_id__")
	if found != "team-z" {
		t.Fatalf("expected team-z resource attribute, got %q", found)
	}
}

func TestInjectHeaderTenant_NilResource(t *testing.T) {
	rm := &metricspb.ResourceMetrics{}
	rms := []*metricspb.ResourceMetrics{rm}

	InjectHeaderTenant(rms, "tenant_id", "abc")

	if rm.Resource == nil {
		t.Fatal("resource should have been created")
	}
	found := findResourceAttribute(rm, "tenant_id")
	if found != "abc" {
		t.Fatalf("expected abc, got %q", found)
	}
}

func TestInjectHeaderTenant_EmptyTenant(t *testing.T) {
	rm := makeGaugeRM(nil, "cpu", nil)
	origLen := len(rm.Resource.Attributes)
	InjectHeaderTenant([]*metricspb.ResourceMetrics{rm}, "tenant_id", "")
	if len(rm.Resource.Attributes) != origLen {
		t.Fatal("empty tenant should not inject attribute")
	}
}

func TestInjectHeaderTenant_OverwriteExisting(t *testing.T) {
	rm := makeGaugeRM(map[string]string{"org_id": "old"}, "cpu", nil)
	InjectHeaderTenant([]*metricspb.ResourceMetrics{rm}, "org_id", "new")

	found := findResourceAttribute(rm, "org_id")
	if found != "new" {
		t.Fatalf("expected new, got %q", found)
	}
}

// --- ReloadConfig Tests ---

func TestDetector_ReloadConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeHeader
	d, _ := NewDetector(cfg)

	newCfg := DefaultConfig()
	newCfg.Enabled = true
	newCfg.Mode = ModeAttribute
	newCfg.AttributeKey = "env"
	if err := d.ReloadConfig(newCfg); err != nil {
		t.Fatalf("reload should succeed: %v", err)
	}

	rm := makeGaugeRM(map[string]string{"env": "prod"}, "cpu", nil)
	tenant := d.Detect(rm, "ignored-header")
	if tenant != "prod" {
		t.Fatalf("expected prod after reload, got %s", tenant)
	}
}

func TestDetector_ReloadConfig_Invalid(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	d, _ := NewDetector(cfg)

	bad := Config{Enabled: true, Mode: "bad"}
	if err := d.ReloadConfig(bad); err == nil {
		t.Fatal("reload with invalid config should fail")
	}
	// Detector should still work with original config
	rm := makeGaugeRM(nil, "cpu", nil)
	tenant := d.Detect(rm, "team-a")
	if tenant != "team-a" {
		t.Fatalf("original config should still work, got %s", tenant)
	}
}

// --- Helper Tests ---

func TestFindAttributeValue_NotFound(t *testing.T) {
	attrs := []*commonpb.KeyValue{makeStringKV("a", "1")}
	if v := findAttributeValue(attrs, "b"); v != "" {
		t.Fatalf("expected empty, got %q", v)
	}
}

func TestFindAttributeValue_NonString(t *testing.T) {
	attrs := []*commonpb.KeyValue{{
		Key:   "count",
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 42}},
	}}
	if v := findAttributeValue(attrs, "count"); v != "" {
		t.Fatalf("expected empty for non-string, got %q", v)
	}
}

func TestRemoveAttribute(t *testing.T) {
	attrs := []*commonpb.KeyValue{
		makeStringKV("a", "1"),
		makeStringKV("b", "2"),
		makeStringKV("c", "3"),
	}
	result := removeAttribute(attrs, "b")
	if len(result) != 2 {
		t.Fatalf("expected 2 attrs, got %d", len(result))
	}
	for _, kv := range result {
		if kv.Key == "b" {
			t.Fatal("b should have been removed")
		}
	}
}

func TestRemoveAttribute_NotFound(t *testing.T) {
	attrs := []*commonpb.KeyValue{makeStringKV("a", "1")}
	result := removeAttribute(attrs, "z")
	if len(result) != 1 {
		t.Fatalf("expected 1 attr, got %d", len(result))
	}
}

// --- Histogram Metric Detection ---

func TestDetect_LabelMode_Histogram(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeLabel
	cfg.LabelName = "team"
	cfg.InjectLabel = false
	d, _ := NewDetector(cfg)

	rm := &metricspb.ResourceMetrics{
		Resource: &resourcepb.Resource{},
		ScopeMetrics: []*metricspb.ScopeMetrics{{
			Metrics: []*metricspb.Metric{{
				Name: "latency",
				Data: &metricspb.Metric_Histogram{
					Histogram: &metricspb.Histogram{
						DataPoints: []*metricspb.HistogramDataPoint{{
							Attributes: []*commonpb.KeyValue{makeStringKV("team", "infra")},
						}},
					},
				},
			}},
		}},
	}
	tenant := d.Detect(rm, "")
	if tenant != "infra" {
		t.Fatalf("expected infra, got %s", tenant)
	}
}

// --- Race Condition Tests ---

func TestRace_DetectorConcurrent(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeAttribute
	cfg.AttributeKey = "ns"
	cfg.InjectLabel = true
	cfg.InjectLabelName = "__tenant__"
	d, _ := NewDetector(cfg)

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 100; j++ {
				rm := makeGaugeRM(map[string]string{"ns": "t1"}, "cpu", nil)
				d.DetectAndAnnotate([]*metricspb.ResourceMetrics{rm}, "")
			}
		}()
	}
	// Concurrent reload
	go func() {
		defer func() { done <- struct{}{} }()
		for j := 0; j < 50; j++ {
			newCfg := DefaultConfig()
			newCfg.Enabled = true
			newCfg.Mode = ModeAttribute
			newCfg.AttributeKey = "ns"
			_ = d.ReloadConfig(newCfg)
		}
	}()
	for i := 0; i < 11; i++ {
		<-done
	}
}

// --- Benchmarks ---

func BenchmarkDetect_HeaderMode(b *testing.B) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeHeader
	cfg.InjectLabel = false
	d, _ := NewDetector(cfg)
	rm := makeGaugeRM(nil, "cpu", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d.Detect(rm, "team-1")
	}
}

func BenchmarkDetect_AttributeMode(b *testing.B) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeAttribute
	cfg.AttributeKey = "service.namespace"
	cfg.InjectLabel = false
	d, _ := NewDetector(cfg)
	rm := makeGaugeRM(map[string]string{"service.namespace": "payments"}, "cpu", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d.Detect(rm, "")
	}
}

func BenchmarkDetectAndAnnotate_WithInjection(b *testing.B) {
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Mode = ModeAttribute
	cfg.AttributeKey = "service.namespace"
	cfg.InjectLabel = true
	cfg.InjectLabelName = "__tenant__"
	d, _ := NewDetector(cfg)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rm := makeGaugeRM(map[string]string{"service.namespace": "p"}, "cpu", map[string]string{"host": "a"})
		d.DetectAndAnnotate([]*metricspb.ResourceMetrics{rm}, "")
	}
}
