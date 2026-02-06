package tenant

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"gopkg.in/yaml.v3"
)

// QuotaAction defines what happens when a tenant limit is exceeded.
type QuotaAction string

const (
	QuotaActionDrop     QuotaAction = "drop"     // Hard drop when limit exceeded
	QuotaActionLog      QuotaAction = "log"      // Log only (for premium tenants)
	QuotaActionAdaptive QuotaAction = "adaptive" // Adaptive limiting within tenant
)

// QuotasConfig holds the full tenant quotas configuration (loaded from file).
type QuotasConfig struct {
	Global  *GlobalQuota            `yaml:"global"`
	Default *TenantQuota            `yaml:"default"`
	Tenants map[string]*TenantQuota `yaml:"tenants"`
}

// GlobalQuota defines system-wide limits (protects the backend).
type GlobalQuota struct {
	MaxDatapoints  int64       `yaml:"max_datapoints"`  // per window
	MaxCardinality int64       `yaml:"max_cardinality"` // unique series
	Action         QuotaAction `yaml:"action"`
	Window         string      `yaml:"window"` // e.g. "1m"
	parsedWindow   time.Duration
}

// TenantQuota defines per-tenant limits.
type TenantQuota struct {
	MaxDatapoints  int64       `yaml:"max_datapoints"`  // per window
	MaxCardinality int64       `yaml:"max_cardinality"` // unique series
	Action         QuotaAction `yaml:"action"`
	Window         string      `yaml:"window"`   // e.g. "1m"
	Priority       int         `yaml:"priority"` // higher = more important (tiebreak for global enforcement)
	parsedWindow   time.Duration
}

// LoadQuotasConfig loads a tenancy quotas YAML config from file.
func LoadQuotasConfig(path string) (*QuotasConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read tenancy config: %w", err)
	}
	return ParseQuotasConfig(data)
}

// ParseQuotasConfig parses tenancy quotas from YAML bytes.
func ParseQuotasConfig(data []byte) (*QuotasConfig, error) {
	cfg := &QuotasConfig{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse tenancy config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *QuotasConfig) validate() error {
	if c.Global != nil {
		if c.Global.Window == "" {
			c.Global.Window = "1m"
		}
		d, err := time.ParseDuration(c.Global.Window)
		if err != nil {
			return fmt.Errorf("global: invalid window %q: %w", c.Global.Window, err)
		}
		c.Global.parsedWindow = d
		if err := validateAction(c.Global.Action); err != nil {
			return fmt.Errorf("global: %w", err)
		}
	}
	if c.Default != nil {
		if err := c.Default.parseAndValidate("default"); err != nil {
			return err
		}
	}
	for name, tq := range c.Tenants {
		if err := tq.parseAndValidate(name); err != nil {
			return err
		}
	}
	return nil
}

func (tq *TenantQuota) parseAndValidate(name string) error {
	if tq.Window == "" {
		tq.Window = "1m"
	}
	d, err := time.ParseDuration(tq.Window)
	if err != nil {
		return fmt.Errorf("tenant %q: invalid window %q: %w", name, tq.Window, err)
	}
	tq.parsedWindow = d
	if err := validateAction(tq.Action); err != nil {
		return fmt.Errorf("tenant %q: %w", name, err)
	}
	return nil
}

func validateAction(a QuotaAction) error {
	switch a {
	case QuotaActionDrop, QuotaActionLog, QuotaActionAdaptive, "":
		return nil
	default:
		return fmt.Errorf("unknown action %q (valid: drop, log, adaptive)", a)
	}
}

// Prometheus metrics for tenant quotas enforcement.
var (
	tenantDatapointsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_tenant_datapoints_total",
		Help: "Total datapoints per tenant",
	}, []string{"tenant"})

	tenantCardinalityGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "metrics_governor_tenant_cardinality",
		Help: "Current cardinality per tenant",
	}, []string{"tenant"})

	tenantLimitExceededTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_tenant_limit_exceeded_total",
		Help: "Total times a tenant limit was exceeded",
	}, []string{"tenant", "level"}) // level: global, tenant

	globalDatapointsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_global_datapoints_total",
		Help: "Total datapoints across all tenants",
	})

	globalCardinalityGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "metrics_governor_global_cardinality",
		Help: "Current global cardinality (estimated)",
	})

	quotasReloadTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_tenant_quotas_reload_total",
		Help: "Total tenant quotas config reloads",
	})
)

func init() {
	prometheus.MustRegister(tenantDatapointsTotal)
	prometheus.MustRegister(tenantCardinalityGauge)
	prometheus.MustRegister(tenantLimitExceededTotal)
	prometheus.MustRegister(globalDatapointsTotal)
	prometheus.MustRegister(globalCardinalityGauge)
	prometheus.MustRegister(quotasReloadTotal)
}

// tenantWindow tracks datapoint count and cardinality within a time window for a tenant.
type tenantWindow struct {
	datapoints  int64
	cardinality map[string]struct{} // simple set of series keys
	windowEnd   time.Time
}

// QuotaEnforcer enforces hierarchical limits: global → tenant.
// It wraps around the existing limits.Enforcer (which handles per-rule limits).
type QuotaEnforcer struct {
	mu     sync.RWMutex
	config *QuotasConfig

	// Per-tenant tracking
	tenants map[string]*tenantWindow

	// Global tracking
	global *tenantWindow

	reloadCount   atomic.Int64
	lastReloadUTC atomic.Int64
}

// NewQuotaEnforcer creates a QuotaEnforcer from config.
func NewQuotaEnforcer(cfg *QuotasConfig) *QuotaEnforcer {
	qe := &QuotaEnforcer{
		config:  cfg,
		tenants: make(map[string]*tenantWindow),
		global:  &tenantWindow{cardinality: make(map[string]struct{})},
	}
	// Initialize reload timestamp to now so "time since reload" metrics
	// don't report ~56 years (Unix epoch default of 0).
	qe.lastReloadUTC.Store(time.Now().UTC().Unix())
	return qe
}

// ReloadConfig swaps the quotas configuration.
func (qe *QuotaEnforcer) ReloadConfig(cfg *QuotasConfig) {
	qe.mu.Lock()
	qe.config = cfg
	// Reset windows on reload to pick up new limits
	qe.tenants = make(map[string]*tenantWindow)
	qe.global = &tenantWindow{cardinality: make(map[string]struct{})}
	qe.reloadCount.Add(1)
	qe.lastReloadUTC.Store(time.Now().UTC().Unix())
	qe.mu.Unlock()
	quotasReloadTotal.Inc()
}

// Process evaluates metrics against the hierarchical limits.
// tenantID should be pre-detected by the Detector.
// Returns the filtered metrics (may be empty if all dropped).
func (qe *QuotaEnforcer) Process(tenantID string, rms []*metricspb.ResourceMetrics) []*metricspb.ResourceMetrics {
	qe.mu.Lock()
	defer qe.mu.Unlock()

	now := time.Now()

	// Get tenant quota config
	tenantQuota := qe.getTenantQuota(tenantID)

	// Count incoming datapoints and series keys
	dpCount, seriesKeys := countDatapointsAndSeries(rms)

	// --- Level 1: Global limits ---
	if qe.config.Global != nil && (qe.config.Global.MaxDatapoints > 0 || qe.config.Global.MaxCardinality > 0) {
		g := qe.global
		windowDur := qe.config.Global.parsedWindow
		if windowDur == 0 {
			windowDur = time.Minute
		}

		// Reset window if expired
		if now.After(g.windowEnd) {
			g.datapoints = 0
			g.cardinality = make(map[string]struct{})
			g.windowEnd = now.Add(windowDur)
		}

		exceeded := false
		if qe.config.Global.MaxDatapoints > 0 && g.datapoints+int64(dpCount) > qe.config.Global.MaxDatapoints {
			exceeded = true
		}
		if qe.config.Global.MaxCardinality > 0 {
			projectedCard := int64(len(g.cardinality)) + countNewSeries(g.cardinality, seriesKeys)
			if projectedCard > qe.config.Global.MaxCardinality {
				exceeded = true
			}
		}

		if exceeded {
			tenantLimitExceededTotal.WithLabelValues(tenantID, "global").Inc()
			action := qe.config.Global.Action
			if action == "" {
				action = QuotaActionDrop
			}
			switch action {
			case QuotaActionDrop:
				return nil // Drop all
			case QuotaActionLog:
				// Log only — fall through to tenant limits
			case QuotaActionAdaptive:
				// For global adaptive, drop low-priority tenants first
				if tenantQuota != nil && tenantQuota.Priority <= 0 {
					return nil
				}
				// High-priority tenants pass through
			}
		}

		// Track global stats
		g.datapoints += int64(dpCount)
		for _, k := range seriesKeys {
			g.cardinality[k] = struct{}{}
		}
		globalDatapointsTotal.Add(float64(dpCount))
		globalCardinalityGauge.Set(float64(len(g.cardinality)))
	}

	// --- Level 2: Per-tenant limits ---
	if tenantQuota != nil && (tenantQuota.MaxDatapoints > 0 || tenantQuota.MaxCardinality > 0) {
		tw := qe.getOrCreateWindow(tenantID, tenantQuota)
		windowDur := tenantQuota.parsedWindow
		if windowDur == 0 {
			windowDur = time.Minute
		}

		// Reset window if expired
		if now.After(tw.windowEnd) {
			tw.datapoints = 0
			tw.cardinality = make(map[string]struct{})
			tw.windowEnd = now.Add(windowDur)
		}

		exceeded := false
		if tenantQuota.MaxDatapoints > 0 && tw.datapoints+int64(dpCount) > tenantQuota.MaxDatapoints {
			exceeded = true
		}
		if tenantQuota.MaxCardinality > 0 {
			projectedCard := int64(len(tw.cardinality)) + countNewSeries(tw.cardinality, seriesKeys)
			if projectedCard > tenantQuota.MaxCardinality {
				exceeded = true
			}
		}

		if exceeded {
			tenantLimitExceededTotal.WithLabelValues(tenantID, "tenant").Inc()
			action := tenantQuota.Action
			if action == "" {
				action = QuotaActionAdaptive
			}
			switch action {
			case QuotaActionDrop:
				return nil
			case QuotaActionLog:
				// Log only — pass through
			case QuotaActionAdaptive:
				// Adaptive: allow partial data up to limit
				rms = qe.adaptiveFilter(rms, tw, tenantQuota)
				if len(rms) == 0 {
					return nil
				}
			}
		}

		// Track tenant stats (after potential filtering)
		newDPCount, newSeriesKeys := countDatapointsAndSeries(rms)
		tw.datapoints += int64(newDPCount)
		for _, k := range newSeriesKeys {
			tw.cardinality[k] = struct{}{}
		}
		tenantDatapointsTotal.WithLabelValues(tenantID).Add(float64(newDPCount))
		tenantCardinalityGauge.WithLabelValues(tenantID).Set(float64(len(tw.cardinality)))
	}

	return rms
}

// getTenantQuota returns the quota config for a tenant (explicit override or default).
func (qe *QuotaEnforcer) getTenantQuota(tenantID string) *TenantQuota {
	if tq, ok := qe.config.Tenants[tenantID]; ok {
		return tq
	}
	return qe.config.Default
}

// getOrCreateWindow returns the tracking window for a tenant.
func (qe *QuotaEnforcer) getOrCreateWindow(tenantID string, tq *TenantQuota) *tenantWindow {
	tw, ok := qe.tenants[tenantID]
	if !ok {
		tw = &tenantWindow{
			cardinality: make(map[string]struct{}),
			windowEnd:   time.Now().Add(tq.parsedWindow),
		}
		qe.tenants[tenantID] = tw
	}
	return tw
}

// adaptiveFilter keeps ResourceMetrics until the tenant would exceed their limit.
func (qe *QuotaEnforcer) adaptiveFilter(rms []*metricspb.ResourceMetrics, tw *tenantWindow, quota *TenantQuota) []*metricspb.ResourceMetrics {
	remaining := quota.MaxDatapoints - tw.datapoints
	if remaining <= 0 {
		return nil
	}

	var result []*metricspb.ResourceMetrics
	var kept int64
	for _, rm := range rms {
		dpCount := countRMDatapoints(rm)
		if kept+int64(dpCount) <= remaining {
			result = append(result, rm)
			kept += int64(dpCount)
		} else {
			break // Don't partially process a ResourceMetrics
		}
	}
	return result
}

// countDatapointsAndSeries counts total datapoints and builds series keys from ResourceMetrics.
func countDatapointsAndSeries(rms []*metricspb.ResourceMetrics) (int, []string) {
	count := 0
	var keys []string
	for _, rm := range rms {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				dpCount, dpKeys := countMetricDatapointsAndKeys(m)
				count += dpCount
				keys = append(keys, dpKeys...)
			}
		}
	}
	return count, keys
}

// countRMDatapoints counts datapoints in a single ResourceMetrics.
func countRMDatapoints(rm *metricspb.ResourceMetrics) int {
	count := 0
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			count += countMetricDatapoints(m)
		}
	}
	return count
}

// countMetricDatapoints counts datapoints in a single metric.
func countMetricDatapoints(m *metricspb.Metric) int {
	switch data := m.Data.(type) {
	case *metricspb.Metric_Gauge:
		return len(data.Gauge.DataPoints)
	case *metricspb.Metric_Sum:
		return len(data.Sum.DataPoints)
	case *metricspb.Metric_Histogram:
		return len(data.Histogram.DataPoints)
	case *metricspb.Metric_Summary:
		return len(data.Summary.DataPoints)
	case *metricspb.Metric_ExponentialHistogram:
		return len(data.ExponentialHistogram.DataPoints)
	}
	return 0
}

// countMetricDatapointsAndKeys counts datapoints and builds series keys.
func countMetricDatapointsAndKeys(m *metricspb.Metric) (int, []string) {
	name := m.Name
	switch data := m.Data.(type) {
	case *metricspb.Metric_Gauge:
		keys := make([]string, 0, len(data.Gauge.DataPoints))
		for _, dp := range data.Gauge.DataPoints {
			keys = append(keys, buildSeriesKey(name, dp.Attributes))
		}
		return len(data.Gauge.DataPoints), keys
	case *metricspb.Metric_Sum:
		keys := make([]string, 0, len(data.Sum.DataPoints))
		for _, dp := range data.Sum.DataPoints {
			keys = append(keys, buildSeriesKey(name, dp.Attributes))
		}
		return len(data.Sum.DataPoints), keys
	case *metricspb.Metric_Histogram:
		keys := make([]string, 0, len(data.Histogram.DataPoints))
		for _, dp := range data.Histogram.DataPoints {
			keys = append(keys, buildHistSeriesKey(name, dp.Attributes))
		}
		return len(data.Histogram.DataPoints), keys
	case *metricspb.Metric_Summary:
		keys := make([]string, 0, len(data.Summary.DataPoints))
		for _, dp := range data.Summary.DataPoints {
			keys = append(keys, buildSummarySeriesKey(name, dp.Attributes))
		}
		return len(data.Summary.DataPoints), keys
	case *metricspb.Metric_ExponentialHistogram:
		keys := make([]string, 0, len(data.ExponentialHistogram.DataPoints))
		for _, dp := range data.ExponentialHistogram.DataPoints {
			keys = append(keys, buildExpHistSeriesKey(name, dp.Attributes))
		}
		return len(data.ExponentialHistogram.DataPoints), keys
	}
	return 0, nil
}

// buildSeriesKey creates a unique key for a series from metric name + attributes.
func buildSeriesKey(name string, attrs []*commonpb.KeyValue) string {
	if len(attrs) == 0 {
		return name
	}
	key := name + "{"
	for i, kv := range attrs {
		if i > 0 {
			key += ","
		}
		key += kv.Key + "=" + getStringValue(kv.Value)
	}
	key += "}"
	return key
}

func buildHistSeriesKey(name string, attrs []*commonpb.KeyValue) string {
	return buildSeriesKey(name, attrs)
}

func buildSummarySeriesKey(name string, attrs []*commonpb.KeyValue) string {
	return buildSeriesKey(name, attrs)
}

func buildExpHistSeriesKey(name string, attrs []*commonpb.KeyValue) string {
	return buildSeriesKey(name, attrs)
}

// getStringValue extracts the string representation of an AnyValue.
func getStringValue(v *commonpb.AnyValue) string {
	if v == nil {
		return ""
	}
	switch val := v.Value.(type) {
	case *commonpb.AnyValue_StringValue:
		return val.StringValue
	case *commonpb.AnyValue_IntValue:
		return fmt.Sprintf("%d", val.IntValue)
	case *commonpb.AnyValue_DoubleValue:
		return fmt.Sprintf("%g", val.DoubleValue)
	case *commonpb.AnyValue_BoolValue:
		return fmt.Sprintf("%t", val.BoolValue)
	default:
		return ""
	}
}

// countNewSeries counts how many series keys are not already in the existing set.
func countNewSeries(existing map[string]struct{}, keys []string) int64 {
	var count int64
	for _, k := range keys {
		if _, ok := existing[k]; !ok {
			count++
		}
	}
	return count
}
