package tenant

import (
	"fmt"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// DetectionMode specifies how tenant ID is extracted.
type DetectionMode string

const (
	// ModeHeader extracts tenant from HTTP/gRPC headers (e.g. X-Scope-OrgID for Mimir/Cortex).
	ModeHeader DetectionMode = "header"
	// ModeLabel extracts tenant from metric labels.
	ModeLabel DetectionMode = "label"
	// ModeAttribute extracts tenant from OTLP resource attributes.
	ModeAttribute DetectionMode = "attribute"
)

// Config holds tenancy configuration.
type Config struct {
	Enabled          bool          `yaml:"enabled"`
	Mode             DetectionMode `yaml:"mode"`
	HeaderName       string        `yaml:"header_name"`
	LabelName        string        `yaml:"label_name"`
	AttributeKey     string        `yaml:"attribute_key"`
	DefaultTenant    string        `yaml:"default_tenant"`
	InjectLabel      bool          `yaml:"inject_label"`
	InjectLabelName  string        `yaml:"inject_label_name"`
	StripSourceLabel bool          `yaml:"strip_source_label"`
}

// DefaultConfig returns a Config with production defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:         false,
		Mode:            ModeHeader,
		HeaderName:      "X-Scope-OrgID",
		LabelName:       "tenant",
		AttributeKey:    "service.namespace",
		DefaultTenant:   "default",
		InjectLabel:     true,
		InjectLabelName: "__tenant__",
	}
}

// Validate checks Config for consistency.
func (c *Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	switch c.Mode {
	case ModeHeader:
		if c.HeaderName == "" {
			return fmt.Errorf("tenancy: header_name required when mode=header")
		}
	case ModeLabel:
		if c.LabelName == "" {
			return fmt.Errorf("tenancy: label_name required when mode=label")
		}
	case ModeAttribute:
		if c.AttributeKey == "" {
			return fmt.Errorf("tenancy: attribute_key required when mode=attribute")
		}
	default:
		return fmt.Errorf("tenancy: unknown mode %q (valid: header, label, attribute)", c.Mode)
	}
	if c.DefaultTenant == "" {
		return fmt.Errorf("tenancy: default_tenant must not be empty")
	}
	if c.InjectLabel && c.InjectLabelName == "" {
		return fmt.Errorf("tenancy: inject_label_name required when inject_label=true")
	}
	return nil
}

// Prometheus metrics for tenant detection.
var (
	tenantDetectedTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_tenant_detected_total",
		Help: "Total ResourceMetrics processed per detected tenant",
	}, []string{"tenant", "source"})

	tenantFallbackTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_tenant_fallback_total",
		Help: "Total times default tenant was used as fallback",
	})
)

func init() {
	prometheus.MustRegister(tenantDetectedTotal)
	prometheus.MustRegister(tenantFallbackTotal)
}

// Detector extracts tenant IDs from metrics and optionally injects tenant labels.
type Detector struct {
	mu     sync.RWMutex
	config Config
}

// NewDetector creates a Detector from the given config.
// The config is validated; an error is returned if invalid.
func NewDetector(cfg Config) (*Detector, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Detector{config: cfg}, nil
}

// ReloadConfig swaps the configuration atomically.
func (d *Detector) ReloadConfig(cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	d.mu.Lock()
	d.config = cfg
	d.mu.Unlock()
	return nil
}

// Enabled returns whether tenancy is enabled.
func (d *Detector) Enabled() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.config.Enabled
}

// DetectAndAnnotate processes a batch of ResourceMetrics:
//  1. For each ResourceMetrics, it detects the tenant ID based on mode.
//  2. If inject_label is true, it injects the tenant as a label on every datapoint.
//  3. Returns a map from tenant ID to the ResourceMetrics belonging to that tenant.
//
// For ModeHeader, the tenant is provided externally (by the receiver) via headerTenant.
// Pass "" if no header was extracted (label/attribute modes ignore this parameter).
func (d *Detector) DetectAndAnnotate(rms []*metricspb.ResourceMetrics, headerTenant string) map[string][]*metricspb.ResourceMetrics {
	d.mu.RLock()
	cfg := d.config
	d.mu.RUnlock()

	result := make(map[string][]*metricspb.ResourceMetrics, 4)

	for _, rm := range rms {
		tenant := d.detectTenant(rm, headerTenant, cfg)

		// Inject tenant label on all datapoints if configured
		if cfg.InjectLabel {
			injectTenantLabel(rm, cfg.InjectLabelName, tenant)
		}

		// Optionally strip the source label (e.g. remove "tenant" label after detection)
		if cfg.StripSourceLabel && cfg.Mode == ModeLabel {
			stripLabel(rm, cfg.LabelName)
		}

		result[tenant] = append(result[tenant], rm)
		tenantDetectedTotal.WithLabelValues(tenant, string(cfg.Mode)).Inc()
	}

	return result
}

// Detect returns the tenant ID for a single ResourceMetrics without modifying it.
func (d *Detector) Detect(rm *metricspb.ResourceMetrics, headerTenant string) string {
	d.mu.RLock()
	cfg := d.config
	d.mu.RUnlock()
	return d.detectTenant(rm, headerTenant, cfg)
}

// detectTenant extracts tenant ID based on the configured mode.
func (d *Detector) detectTenant(rm *metricspb.ResourceMetrics, headerTenant string, cfg Config) string {
	switch cfg.Mode {
	case ModeHeader:
		if headerTenant != "" {
			return headerTenant
		}
	case ModeLabel:
		if t := findLabelValue(rm, cfg.LabelName); t != "" {
			return t
		}
	case ModeAttribute:
		if t := findResourceAttribute(rm, cfg.AttributeKey); t != "" {
			return t
		}
	}
	tenantFallbackTotal.Inc()
	return cfg.DefaultTenant
}

// InjectHeaderTenant injects the tenant ID from a request header into each
// ResourceMetrics' resource attributes. This should be called at the receiver
// layer, before data enters the buffer pipeline.
//
// This allows downstream components (limits, sampling, export) to read
// the tenant from the data itself, without needing access to HTTP headers.
func InjectHeaderTenant(rms []*metricspb.ResourceMetrics, attributeKey, tenant string) {
	if tenant == "" {
		return
	}
	for _, rm := range rms {
		if rm.Resource == nil {
			rm.Resource = &resourcepb.Resource{}
		}
		// Check if attribute already exists
		for _, kv := range rm.Resource.Attributes {
			if kv.Key == attributeKey {
				kv.Value = &commonpb.AnyValue{
					Value: &commonpb.AnyValue_StringValue{StringValue: tenant},
				}
				return
			}
		}
		rm.Resource.Attributes = append(rm.Resource.Attributes, &commonpb.KeyValue{
			Key:   attributeKey,
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: tenant}},
		})
	}
}

// --- Helpers ---

// findResourceAttribute looks up a string resource attribute value by key.
func findResourceAttribute(rm *metricspb.ResourceMetrics, key string) string {
	if rm == nil || rm.Resource == nil {
		return ""
	}
	for _, kv := range rm.Resource.Attributes {
		if kv.Key == key {
			if sv, ok := kv.Value.GetValue().(*commonpb.AnyValue_StringValue); ok {
				return sv.StringValue
			}
		}
	}
	return ""
}

// findLabelValue scans all metrics in a ResourceMetrics for a label with
// the given name and returns the first non-empty value found.
func findLabelValue(rm *metricspb.ResourceMetrics, labelName string) string {
	if rm == nil {
		return ""
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if v := findLabelInMetric(m, labelName); v != "" {
				return v
			}
		}
	}
	return ""
}

// findLabelInMetric looks for a label in all datapoints of a metric.
func findLabelInMetric(m *metricspb.Metric, labelName string) string {
	switch data := m.Data.(type) {
	case *metricspb.Metric_Gauge:
		for _, dp := range data.Gauge.DataPoints {
			if v := findAttributeValue(dp.Attributes, labelName); v != "" {
				return v
			}
		}
	case *metricspb.Metric_Sum:
		for _, dp := range data.Sum.DataPoints {
			if v := findAttributeValue(dp.Attributes, labelName); v != "" {
				return v
			}
		}
	case *metricspb.Metric_Histogram:
		for _, dp := range data.Histogram.DataPoints {
			if v := findAttributeValue(dp.Attributes, labelName); v != "" {
				return v
			}
		}
	case *metricspb.Metric_Summary:
		for _, dp := range data.Summary.DataPoints {
			if v := findAttributeValue(dp.Attributes, labelName); v != "" {
				return v
			}
		}
	case *metricspb.Metric_ExponentialHistogram:
		for _, dp := range data.ExponentialHistogram.DataPoints {
			if v := findAttributeValue(dp.Attributes, labelName); v != "" {
				return v
			}
		}
	}
	return ""
}

// findAttributeValue finds a string attribute value in a slice of KeyValues.
func findAttributeValue(attrs []*commonpb.KeyValue, key string) string {
	for _, kv := range attrs {
		if kv.Key == key {
			if sv, ok := kv.Value.GetValue().(*commonpb.AnyValue_StringValue); ok {
				return sv.StringValue
			}
		}
	}
	return ""
}

// injectTenantLabel adds the tenant label to every datapoint in a ResourceMetrics.
func injectTenantLabel(rm *metricspb.ResourceMetrics, labelName, tenant string) {
	kv := &commonpb.KeyValue{
		Key:   labelName,
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: tenant}},
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			injectLabelInMetric(m, kv)
		}
	}
}

// injectLabelInMetric adds a label to all datapoints in a metric.
func injectLabelInMetric(m *metricspb.Metric, kv *commonpb.KeyValue) {
	switch data := m.Data.(type) {
	case *metricspb.Metric_Gauge:
		for _, dp := range data.Gauge.DataPoints {
			dp.Attributes = appendOrReplace(dp.Attributes, kv)
		}
	case *metricspb.Metric_Sum:
		for _, dp := range data.Sum.DataPoints {
			dp.Attributes = appendOrReplace(dp.Attributes, kv)
		}
	case *metricspb.Metric_Histogram:
		for _, dp := range data.Histogram.DataPoints {
			dp.Attributes = appendOrReplace(dp.Attributes, kv)
		}
	case *metricspb.Metric_Summary:
		for _, dp := range data.Summary.DataPoints {
			dp.Attributes = appendOrReplace(dp.Attributes, kv)
		}
	case *metricspb.Metric_ExponentialHistogram:
		for _, dp := range data.ExponentialHistogram.DataPoints {
			dp.Attributes = appendOrReplace(dp.Attributes, kv)
		}
	}
}

// appendOrReplace adds a KeyValue or replaces an existing one with the same key.
func appendOrReplace(attrs []*commonpb.KeyValue, kv *commonpb.KeyValue) []*commonpb.KeyValue {
	for i, existing := range attrs {
		if existing.Key == kv.Key {
			attrs[i] = kv
			return attrs
		}
	}
	return append(attrs, kv)
}

// stripLabel removes a label from all datapoints in a ResourceMetrics.
func stripLabel(rm *metricspb.ResourceMetrics, labelName string) {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			stripLabelInMetric(m, labelName)
		}
	}
}

// stripLabelInMetric removes a label from all datapoints in a metric.
func stripLabelInMetric(m *metricspb.Metric, labelName string) {
	switch data := m.Data.(type) {
	case *metricspb.Metric_Gauge:
		for _, dp := range data.Gauge.DataPoints {
			dp.Attributes = removeAttribute(dp.Attributes, labelName)
		}
	case *metricspb.Metric_Sum:
		for _, dp := range data.Sum.DataPoints {
			dp.Attributes = removeAttribute(dp.Attributes, labelName)
		}
	case *metricspb.Metric_Histogram:
		for _, dp := range data.Histogram.DataPoints {
			dp.Attributes = removeAttribute(dp.Attributes, labelName)
		}
	case *metricspb.Metric_Summary:
		for _, dp := range data.Summary.DataPoints {
			dp.Attributes = removeAttribute(dp.Attributes, labelName)
		}
	case *metricspb.Metric_ExponentialHistogram:
		for _, dp := range data.ExponentialHistogram.DataPoints {
			dp.Attributes = removeAttribute(dp.Attributes, labelName)
		}
	}
}

// removeAttribute removes a KeyValue with the given key from a slice.
func removeAttribute(attrs []*commonpb.KeyValue, key string) []*commonpb.KeyValue {
	for i, kv := range attrs {
		if kv.Key == key {
			return append(attrs[:i], attrs[i+1:]...)
		}
	}
	return attrs
}
