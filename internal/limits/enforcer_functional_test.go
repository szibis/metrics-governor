package limits

import (
	"fmt"
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// --- Sample Action Tests ---

// TestEnforcer_SampleAction_ExceedsLimit verifies that once the datapoints rate
// limit is exceeded, the sample action keeps approximately sample_rate fraction
// of the metrics (deterministic hash-based sampling).
func TestEnforcer_SampleAction_ExceedsLimit(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "sample-noisy",
				Match:             RuleMatch{MetricName: "noisy_.*"},
				MaxDatapointsRate: 10,
				Action:            ActionSample,
				SampleRate:        0.5,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Send 100 individual metrics through Process, each with 1 DP and unique attrs
	// so they accumulate towards the rate limit.
	keptAfterLimit := 0
	droppedAfterLimit := 0
	totalAfterLimit := 0

	for i := 0; i < 100; i++ {
		attrs := map[string]string{
			"instance": fmt.Sprintf("inst-%d", i),
		}
		rms := makeLimitsRM("noisy_metric", attrs, 1)
		result := e.Process(rms)

		// First 10 should pass through (within limit).
		if i < 10 {
			if len(result) == 0 {
				t.Errorf("metric %d (within limit) was unexpectedly dropped", i)
			}
			continue
		}

		// After limit exceeded, track sampling results.
		totalAfterLimit++
		if len(result) > 0 {
			keptAfterLimit++
		} else {
			droppedAfterLimit++
		}
	}

	// With sample_rate=0.5, expect roughly 50% of the 90 post-limit metrics
	// to be kept. Allow ±20% tolerance (i.e., between 30% and 70% kept).
	expectedKept := float64(totalAfterLimit) * 0.5
	lowerBound := expectedKept * 0.6 // 30% of total
	upperBound := expectedKept * 1.4 // 70% of total

	if float64(keptAfterLimit) < lowerBound || float64(keptAfterLimit) > upperBound {
		t.Errorf("sampling: kept %d out of %d post-limit metrics (expected roughly %.0f, tolerance %.0f-%.0f)",
			keptAfterLimit, totalAfterLimit, expectedKept, lowerBound, upperBound)
	}

	t.Logf("sampling result: kept=%d dropped=%d total_after_limit=%d (expected ~%.0f kept)",
		keptAfterLimit, droppedAfterLimit, totalAfterLimit, expectedKept)
}

// TestEnforcer_SampleAction_WithinLimits_NoSampling verifies that when metrics
// are within the configured limits, no sampling occurs and all pass through.
func TestEnforcer_SampleAction_WithinLimits_NoSampling(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "sample-low",
				Match:             RuleMatch{MetricName: "low_volume_.*"},
				MaxDatapointsRate: 10000,
				Action:            ActionSample,
				SampleRate:        0.5,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Send 5 DPs (well within limit of 10000).
	for i := 0; i < 5; i++ {
		attrs := map[string]string{
			"instance": fmt.Sprintf("inst-%d", i),
		}
		rms := makeLimitsRM("low_volume_metric", attrs, 1)
		result := e.Process(rms)

		if len(result) == 0 {
			t.Errorf("metric %d within limit should not be dropped (sampling should not fire)", i)
		}
	}
}

// TestEnforcer_SampleAction_Deterministic verifies that the same metric name +
// attributes always produce the same keep/drop decision (deterministic hashing).
func TestEnforcer_SampleAction_Deterministic(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:              "sample-det",
				Match:             RuleMatch{MetricName: "det_.*"},
				MaxDatapointsRate: 5,
				Action:            ActionSample,
				SampleRate:        0.5,
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Exhaust the limit first with unique metrics.
	for i := 0; i < 6; i++ {
		attrs := map[string]string{
			"instance": fmt.Sprintf("exhaust-%d", i),
		}
		rms := makeLimitsRM("det_metric", attrs, 1)
		e.Process(rms)
	}

	// Now send the same metric twice; both calls should yield the same result.
	targetAttrs := map[string]string{
		"instance": "deterministic-target",
	}

	rms1 := makeLimitsRM("det_metric", targetAttrs, 1)
	result1 := e.Process(rms1)

	rms2 := makeLimitsRM("det_metric", targetAttrs, 1)
	result2 := e.Process(rms2)

	kept1 := len(result1) > 0
	kept2 := len(result2) > 0

	if kept1 != kept2 {
		t.Errorf("deterministic sampling failed: first call kept=%v, second call kept=%v (should be same)", kept1, kept2)
	}

	t.Logf("deterministic result: kept=%v (consistent across both calls)", kept1)
}

// --- Strip Labels Tests ---

// TestEnforcer_StripLabels_ExceedsLimit verifies that when cardinality exceeds
// the limit, the strip_labels action removes the specified labels from datapoints
// while preserving all other labels.
func TestEnforcer_StripLabels_ExceedsLimit(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:           "strip-pod",
				Match:          RuleMatch{MetricName: "http_.*"},
				MaxCardinality: 3,
				Action:         ActionStripLabels,
				StripLabels:    []string{"pod_id", "container_id"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Send metrics with many unique pod_id+container_id to exceed cardinality=3.
	// Each unique combination of attributes is a unique series.
	for i := 0; i < 5; i++ {
		attrs := map[string]string{
			"service":      "web",
			"method":       "GET",
			"pod_id":       fmt.Sprintf("pod-%d", i),
			"container_id": fmt.Sprintf("ctr-%d", i),
		}
		rms := makeLimitsRM("http_requests", attrs, 1)
		result := e.Process(rms)

		// Once we exceed cardinality (after 3 unique series), the action fires.
		if i >= 3 && len(result) > 0 {
			// Verify the returned metric has pod_id and container_id REMOVED.
			rm := result[0]
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					dpAttrs := getDPAttributes(m)
					for _, attrMap := range dpAttrs {
						if _, found := attrMap["pod_id"]; found {
							t.Errorf("metric %d: pod_id should have been stripped", i)
						}
						if _, found := attrMap["container_id"]; found {
							t.Errorf("metric %d: container_id should have been stripped", i)
						}
						// Verify other labels are preserved.
						if v, ok := attrMap["service"]; !ok || v != "web" {
							t.Errorf("metric %d: expected service=web to be preserved, got %q", i, v)
						}
						if v, ok := attrMap["method"]; !ok || v != "GET" {
							t.Errorf("metric %d: expected method=GET to be preserved, got %q", i, v)
						}
					}
				}
			}
		}
	}
}

// TestEnforcer_StripLabels_WithinLimits_NoStripping verifies that when metrics
// are within the configured cardinality limit, no labels are stripped.
func TestEnforcer_StripLabels_WithinLimits_NoStripping(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:           "strip-within",
				Match:          RuleMatch{MetricName: "within_http_.*"},
				MaxCardinality: 3,
				Action:         ActionStripLabels,
				StripLabels:    []string{"pod_id", "container_id"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Send only 2 unique series (within limit of 3).
	for i := 0; i < 2; i++ {
		attrs := map[string]string{
			"service":      "web",
			"method":       "GET",
			"pod_id":       fmt.Sprintf("pod-%d", i),
			"container_id": fmt.Sprintf("ctr-%d", i),
		}
		rms := makeLimitsRM("within_http_requests", attrs, 1)
		result := e.Process(rms)

		if len(result) == 0 {
			t.Fatalf("metric %d within limit should not be dropped", i)
		}

		// Verify all labels are preserved (no stripping within limits).
		rm := result[0]
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				dpAttrs := getDPAttributes(m)
				for _, attrMap := range dpAttrs {
					if _, found := attrMap["pod_id"]; !found {
						t.Errorf("metric %d: pod_id should be preserved when within limits", i)
					}
					if _, found := attrMap["container_id"]; !found {
						t.Errorf("metric %d: container_id should be preserved when within limits", i)
					}
					if _, found := attrMap["service"]; !found {
						t.Errorf("metric %d: service should be preserved when within limits", i)
					}
					if _, found := attrMap["method"]; !found {
						t.Errorf("metric %d: method should be preserved when within limits", i)
					}
				}
			}
		}
	}
}

// TestEnforcer_StripLabels_NonexistentLabel verifies that stripping a label
// that does not exist on the datapoint passes the metric through unchanged.
func TestEnforcer_StripLabels_NonexistentLabel(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		Defaults: &DefaultLimits{Action: ActionLog},
		Rules: []Rule{
			{
				Name:           "strip-noexist",
				Match:          RuleMatch{MetricName: "noexist_.*"},
				MaxCardinality: 2,
				Action:         ActionStripLabels,
				StripLabels:    []string{"nonexistent"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	e := NewEnforcer(cfg, false, 0)
	defer e.Stop()

	// Exceed cardinality to trigger the strip_labels action.
	for i := 0; i < 4; i++ {
		attrs := map[string]string{
			"service": fmt.Sprintf("svc-%d", i),
			"method":  "GET",
		}
		rms := makeLimitsRM("noexist_metric", attrs, 1)
		result := e.Process(rms)

		if i >= 2 && len(result) > 0 {
			// After violation, stripping "nonexistent" should be a no-op.
			rm := result[0]
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					dpAttrs := getDPAttributes(m)
					for _, attrMap := range dpAttrs {
						if _, found := attrMap["service"]; !found {
							t.Errorf("metric %d: service should be preserved (strip nonexistent is no-op)", i)
						}
						if _, found := attrMap["method"]; !found {
							t.Errorf("metric %d: method should be preserved (strip nonexistent is no-op)", i)
						}
					}
				}
			}
		}
	}
}

// --- Helpers ---

// getDPAttributes extracts datapoint attributes from a metric as a slice of
// key-value maps. This reads the raw protobuf KeyValue slices rather than
// using extractDatapointAttributes (which skips empty string values).
func getDPAttributes(m *metricspb.Metric) []map[string]string {
	var result []map[string]string

	extract := func(attrs []*commonpb.KeyValue) map[string]string {
		out := make(map[string]string, len(attrs))
		for _, kv := range attrs {
			if kv.Value != nil {
				if sv, ok := kv.Value.Value.(*commonpb.AnyValue_StringValue); ok {
					out[kv.Key] = sv.StringValue
				}
			}
		}
		return out
	}

	switch d := m.Data.(type) {
	case *metricspb.Metric_Sum:
		for _, dp := range d.Sum.DataPoints {
			result = append(result, extract(dp.Attributes))
		}
	case *metricspb.Metric_Gauge:
		for _, dp := range d.Gauge.DataPoints {
			result = append(result, extract(dp.Attributes))
		}
	case *metricspb.Metric_Histogram:
		for _, dp := range d.Histogram.DataPoints {
			result = append(result, extract(dp.Attributes))
		}
	}

	return result
}
