package sampling

import (
	"strings"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
)

// applyClassify applies the classify action to a datapoint's attributes.
// Chains are evaluated first (first-match wins), then all mappings apply in order.
// Returns the modified attributes slice.
func applyClassify(attrs []*commonpb.KeyValue, cc *compiledClassifyConfig, metrics *ruleMetrics) []*commonpb.KeyValue {
	if cc == nil {
		return attrs
	}

	// Phase 1: Chains — first match wins.
	chainMatched := false
	for _, chain := range cc.chains {
		if evaluateConditions(chain.conditions, attrs) {
			// Apply all set labels with interpolation support.
			for k, v := range chain.set {
				value := interpolateLabels(v, attrs)
				attrs = setLabel(attrs, k, value)
			}
			if metrics != nil {
				metrics.classifyChainMatch.Inc()
				metrics.classifyLabelsSet.Add(float64(len(chain.set)))
			}
			chainMatched = true
			break // First match wins.
		}
	}

	if !chainMatched && len(cc.chains) > 0 && metrics != nil {
		metrics.classifyChainNoMatch.Inc()
	}

	// Phase 2: Mappings — all apply in order.
	for _, m := range cc.mappings {
		// Build the lookup value from source(s).
		var lookupValue string
		if len(m.sources) == 1 {
			lookupValue = getLabelValue(attrs, m.sources[0])
		} else {
			parts := make([]string, len(m.sources))
			for i, src := range m.sources {
				parts[i] = getLabelValue(attrs, src)
			}
			lookupValue = strings.Join(parts, m.separator)
		}

		// Try each compiled pattern (first match wins within this mapping).
		matched := false
		for _, entry := range m.entries {
			if entry.pattern.MatchString(lookupValue) {
				attrs = setLabel(attrs, m.target, entry.value)
				if metrics != nil {
					metrics.classifyLabelsSet.Inc()
				}
				matched = true
				break
			}
		}

		// No match — use default if set.
		if !matched && m.dflt != "" {
			attrs = setLabel(attrs, m.target, m.dflt)
			if metrics != nil {
				metrics.classifyLabelsSet.Inc()
			}
		}
	}

	// Phase 3: Remove labels after classification.
	if len(cc.removeAfter) > 0 {
		attrs = applyRemove(attrs, cc.removeAfter, nil)
	}

	return attrs
}
