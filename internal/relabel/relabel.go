package relabel

import (
	"crypto/sha256"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"gopkg.in/yaml.v3"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

var (
	relabelOpsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_relabel_operations_total",
		Help: "Total number of relabel operations by action",
	}, []string{"action"})

	relabelConfigReloadsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "metrics_governor_relabel_config_reloads_total",
		Help: "Total number of relabel config reloads by result",
	}, []string{"result"})

	relabelDroppedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "metrics_governor_relabel_dropped_total",
		Help: "Total number of datapoints dropped by relabel rules",
	})
)

func init() {
	prometheus.MustRegister(relabelOpsTotal)
	prometheus.MustRegister(relabelConfigReloadsTotal)
	prometheus.MustRegister(relabelDroppedTotal)
}

// Action defines what a relabel rule does.
type Action string

const (
	ActionReplace   Action = "replace"
	ActionKeep      Action = "keep"
	ActionDrop      Action = "drop"
	ActionLabelMap  Action = "labelmap"
	ActionLabelDrop Action = "labeldrop"
	ActionLabelKeep Action = "labelkeep"
	ActionHashMod   Action = "hashmod"
)

// Config defines a single relabeling rule (Prometheus-compatible).
type Config struct {
	SourceLabels []string `yaml:"source_labels"`
	Separator    string   `yaml:"separator"`
	Regex        string   `yaml:"regex"`
	TargetLabel  string   `yaml:"target_label"`
	Replacement  string   `yaml:"replacement"`
	Action       Action   `yaml:"action"`
	Modulus      uint64   `yaml:"modulus"`

	compiledRegex *regexp.Regexp
}

// FileConfig is the top-level structure of a relabel config file.
type FileConfig struct {
	RelabelConfigs []Config `yaml:"relabel_configs"`
}

// Relabeler applies a set of relabel rules to OTLP metrics.
type Relabeler struct {
	mu      sync.RWMutex
	configs []Config
	ops     atomic.Int64 // counter for total relabel operations
}

// New creates a Relabeler from compiled configs.
func New(configs []Config) (*Relabeler, error) {
	compiled, err := compile(configs)
	if err != nil {
		return nil, err
	}
	return &Relabeler{configs: compiled}, nil
}

// LoadFile loads relabel configs from a YAML file.
func LoadFile(path string) ([]Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("relabel: read config: %w", err)
	}
	return Parse(data)
}

// Parse parses relabel configs from YAML bytes.
func Parse(data []byte) ([]Config, error) {
	var fc FileConfig
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return nil, fmt.Errorf("relabel: parse config: %w", err)
	}
	return compile(fc.RelabelConfigs)
}

func compile(configs []Config) ([]Config, error) {
	out := make([]Config, len(configs))
	for i, c := range configs {
		// Apply defaults
		if c.Separator == "" {
			c.Separator = ";"
		}
		if c.Regex == "" {
			c.Regex = "(.*)"
		}
		if c.Replacement == "" {
			c.Replacement = "$1"
		}
		if c.Action == "" {
			c.Action = ActionReplace
		}

		re, err := regexp.Compile("^(?:" + c.Regex + ")$")
		if err != nil {
			return nil, fmt.Errorf("relabel: rule %d: invalid regex %q: %w", i, c.Regex, err)
		}
		c.compiledRegex = re
		out[i] = c
	}
	return out, nil
}

// ReloadConfig atomically replaces the relabel rules.
func (r *Relabeler) ReloadConfig(configs []Config) error {
	compiled, err := compile(configs)
	if err != nil {
		relabelConfigReloadsTotal.WithLabelValues("error").Inc()
		return err
	}
	r.mu.Lock()
	r.configs = compiled
	r.mu.Unlock()
	relabelConfigReloadsTotal.WithLabelValues("success").Inc()
	return nil
}

// Ops returns the total number of relabel operations performed.
func (r *Relabeler) Ops() int64 {
	return r.ops.Load()
}

// Relabel applies all relabel rules to OTLP ResourceMetrics.
// Returns the modified slice (metrics may be removed by drop/keep rules).
func (r *Relabeler) Relabel(rms []*metricspb.ResourceMetrics) []*metricspb.ResourceMetrics {
	r.mu.RLock()
	configs := r.configs
	r.mu.RUnlock()

	if len(configs) == 0 || len(rms) == 0 {
		return rms
	}

	result := make([]*metricspb.ResourceMetrics, 0, len(rms))
	for _, rm := range rms {
		rm = r.relabelResourceMetrics(rm, configs)
		if rm != nil {
			result = append(result, rm)
		}
	}
	return result
}

func (r *Relabeler) relabelResourceMetrics(rm *metricspb.ResourceMetrics, configs []Config) *metricspb.ResourceMetrics {
	if rm == nil {
		return nil
	}

	filteredScopes := make([]*metricspb.ScopeMetrics, 0, len(rm.ScopeMetrics))
	for _, sm := range rm.ScopeMetrics {
		sm = r.relabelScopeMetrics(sm, configs)
		if sm != nil && len(sm.Metrics) > 0 {
			filteredScopes = append(filteredScopes, sm)
		}
	}

	if len(filteredScopes) == 0 {
		return nil
	}
	rm.ScopeMetrics = filteredScopes
	return rm
}

func (r *Relabeler) relabelScopeMetrics(sm *metricspb.ScopeMetrics, configs []Config) *metricspb.ScopeMetrics {
	if sm == nil {
		return nil
	}

	filteredMetrics := make([]*metricspb.Metric, 0, len(sm.Metrics))
	for _, m := range sm.Metrics {
		m = r.relabelMetric(m, configs)
		if m != nil {
			filteredMetrics = append(filteredMetrics, m)
		}
	}

	if len(filteredMetrics) == 0 {
		return nil
	}
	sm.Metrics = filteredMetrics
	return sm
}

func (r *Relabeler) relabelMetric(m *metricspb.Metric, configs []Config) *metricspb.Metric {
	if m == nil {
		return nil
	}

	// Apply relabeling to each datapoint's attributes.
	// The __name__ pseudo-label maps to the metric name.
	switch data := m.Data.(type) {
	case *metricspb.Metric_Gauge:
		if data.Gauge != nil {
			filtered := r.relabelNumberDataPoints(m.Name, data.Gauge.DataPoints, configs)
			if filtered == nil {
				return nil
			}
			data.Gauge.DataPoints = filtered
			// Check if metric was renamed via __name__
			if len(filtered) > 0 {
				m.Name = r.extractName(m.Name, filtered[0].Attributes)
			}
		}
	case *metricspb.Metric_Sum:
		if data.Sum != nil {
			filtered := r.relabelNumberDataPoints(m.Name, data.Sum.DataPoints, configs)
			if filtered == nil {
				return nil
			}
			data.Sum.DataPoints = filtered
			if len(filtered) > 0 {
				m.Name = r.extractName(m.Name, filtered[0].Attributes)
			}
		}
	case *metricspb.Metric_Histogram:
		if data.Histogram != nil {
			filtered := r.relabelHistogramDataPoints(m.Name, data.Histogram.DataPoints, configs)
			if filtered == nil {
				return nil
			}
			data.Histogram.DataPoints = filtered
			if len(filtered) > 0 {
				m.Name = r.extractName(m.Name, filtered[0].Attributes)
			}
		}
	case *metricspb.Metric_Summary:
		if data.Summary != nil {
			filtered := r.relabelSummaryDataPoints(m.Name, data.Summary.DataPoints, configs)
			if filtered == nil {
				return nil
			}
			data.Summary.DataPoints = filtered
			if len(filtered) > 0 {
				m.Name = r.extractName(m.Name, filtered[0].Attributes)
			}
		}
	case *metricspb.Metric_ExponentialHistogram:
		if data.ExponentialHistogram != nil {
			filtered := r.relabelExponentialHistogramDataPoints(m.Name, data.ExponentialHistogram.DataPoints, configs)
			if filtered == nil {
				return nil
			}
			data.ExponentialHistogram.DataPoints = filtered
			if len(filtered) > 0 {
				m.Name = r.extractName(m.Name, filtered[0].Attributes)
			}
		}
	}

	return m
}

// extractName checks if __name__ was set in attributes and returns the new metric name.
func (r *Relabeler) extractName(originalName string, attrs []*commonpb.KeyValue) string {
	for _, kv := range attrs {
		if kv.Key == "__name__" {
			name := kv.Value.GetStringValue()
			if name != "" {
				return name
			}
			break
		}
	}
	return originalName
}

func (r *Relabeler) relabelNumberDataPoints(metricName string, dps []*metricspb.NumberDataPoint, configs []Config) []*metricspb.NumberDataPoint {
	if len(dps) == 0 {
		return dps
	}
	result := make([]*metricspb.NumberDataPoint, 0, len(dps))
	for _, dp := range dps {
		labels := attrsToLabels(metricName, dp.Attributes)
		labels, keep := applyConfigs(configs, labels)
		r.ops.Add(1)
		if keep {
			dp.Attributes = labelsToAttrs(labels)
			result = append(result, dp)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func (r *Relabeler) relabelHistogramDataPoints(metricName string, dps []*metricspb.HistogramDataPoint, configs []Config) []*metricspb.HistogramDataPoint {
	if len(dps) == 0 {
		return dps
	}
	result := make([]*metricspb.HistogramDataPoint, 0, len(dps))
	for _, dp := range dps {
		labels := attrsToLabels(metricName, dp.Attributes)
		labels, keep := applyConfigs(configs, labels)
		r.ops.Add(1)
		if keep {
			dp.Attributes = labelsToAttrs(labels)
			result = append(result, dp)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func (r *Relabeler) relabelSummaryDataPoints(metricName string, dps []*metricspb.SummaryDataPoint, configs []Config) []*metricspb.SummaryDataPoint {
	if len(dps) == 0 {
		return dps
	}
	result := make([]*metricspb.SummaryDataPoint, 0, len(dps))
	for _, dp := range dps {
		labels := attrsToLabels(metricName, dp.Attributes)
		labels, keep := applyConfigs(configs, labels)
		r.ops.Add(1)
		if keep {
			dp.Attributes = labelsToAttrs(labels)
			result = append(result, dp)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func (r *Relabeler) relabelExponentialHistogramDataPoints(metricName string, dps []*metricspb.ExponentialHistogramDataPoint, configs []Config) []*metricspb.ExponentialHistogramDataPoint {
	if len(dps) == 0 {
		return dps
	}
	result := make([]*metricspb.ExponentialHistogramDataPoint, 0, len(dps))
	for _, dp := range dps {
		labels := attrsToLabels(metricName, dp.Attributes)
		labels, keep := applyConfigs(configs, labels)
		r.ops.Add(1)
		if keep {
			dp.Attributes = labelsToAttrs(labels)
			result = append(result, dp)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// labels is a map of label name → value used during relabeling.
type labels map[string]string

// attrsToLabels converts OTLP KeyValue attributes + metric name to a flat label map.
func attrsToLabels(metricName string, attrs []*commonpb.KeyValue) labels {
	m := make(labels, len(attrs)+1)
	m["__name__"] = metricName
	for _, kv := range attrs {
		m[kv.Key] = kv.Value.GetStringValue()
	}
	return m
}

// labelsToAttrs converts a label map back to OTLP KeyValue attributes.
// The __name__ pseudo-label is preserved for upstream extraction.
func labelsToAttrs(l labels) []*commonpb.KeyValue {
	attrs := make([]*commonpb.KeyValue, 0, len(l))
	for k, v := range l {
		attrs = append(attrs, &commonpb.KeyValue{
			Key:   k,
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}},
		})
	}
	return attrs
}

// applyConfigs applies all relabel configs sequentially to a label set.
// Returns the modified labels and whether to keep the datapoint.
func applyConfigs(configs []Config, l labels) (labels, bool) {
	for _, c := range configs {
		var keep bool
		l, keep = applyConfig(c, l)
		if !keep {
			return nil, false
		}
	}
	return l, true
}

func applyConfig(c Config, l labels) (labels, bool) {
	relabelOpsTotal.WithLabelValues(string(c.Action)).Inc()

	switch c.Action {
	case ActionReplace:
		return actionReplace(c, l), true

	case ActionKeep:
		val := sourceValue(c, l)
		if c.compiledRegex.MatchString(val) {
			return l, true
		}
		relabelDroppedTotal.Inc()
		return l, false

	case ActionDrop:
		val := sourceValue(c, l)
		if c.compiledRegex.MatchString(val) {
			relabelDroppedTotal.Inc()
			return l, false
		}
		return l, true

	case ActionLabelMap:
		return actionLabelMap(c, l), true

	case ActionLabelDrop:
		return actionLabelDrop(c, l), true

	case ActionLabelKeep:
		return actionLabelKeep(c, l), true

	case ActionHashMod:
		return actionHashMod(c, l), true

	default:
		return l, true
	}
}

func sourceValue(c Config, l labels) string {
	vals := make([]string, len(c.SourceLabels))
	for i, name := range c.SourceLabels {
		vals[i] = l[name]
	}
	return strings.Join(vals, c.Separator)
}

func actionReplace(c Config, l labels) labels {
	val := sourceValue(c, l)
	if !c.compiledRegex.MatchString(val) {
		return l
	}
	replacement := c.compiledRegex.ReplaceAllString(val, c.Replacement)
	if c.TargetLabel != "" {
		l[c.TargetLabel] = replacement
	}
	return l
}

func actionLabelMap(c Config, l labels) labels {
	for k, v := range l {
		if c.compiledRegex.MatchString(k) {
			newKey := c.compiledRegex.ReplaceAllString(k, c.Replacement)
			l[newKey] = v
		}
	}
	return l
}

func actionLabelDrop(c Config, l labels) labels {
	for k := range l {
		if c.compiledRegex.MatchString(k) {
			delete(l, k)
		}
	}
	return l
}

func actionLabelKeep(c Config, l labels) labels {
	for k := range l {
		if k == "__name__" {
			continue // always keep __name__
		}
		if !c.compiledRegex.MatchString(k) {
			delete(l, k)
		}
	}
	return l
}

func actionHashMod(c Config, l labels) labels {
	if c.Modulus == 0 {
		return l
	}
	val := sourceValue(c, l)
	hash := sha256.Sum256([]byte(val))
	// Use first 8 bytes as uint64
	var h uint64
	for i := 0; i < 8; i++ {
		h = (h << 8) | uint64(hash[i])
	}
	l[c.TargetLabel] = fmt.Sprintf("%d", h%c.Modulus)
	return l
}
