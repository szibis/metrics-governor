package sampling

import (
	"hash/fnv"
	"math"
	"regexp"
	"strconv"
	"strings"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
)

// labelInterpolationRe matches ${label_name} patterns for interpolation.
var labelInterpolationRe = regexp.MustCompile(`\$\{([^}]+)\}`)

// evaluateConditions checks if all conditions match the given attributes.
// Returns true if all conditions match (AND logic) or if there are no conditions.
func evaluateConditions(conditions []Condition, attrs []*commonpb.KeyValue) bool {
	for _, c := range conditions {
		labelValue := getLabelValue(attrs, c.Label)

		if c.Equals != "" && labelValue != c.Equals {
			return false
		}
		if c.Contains != "" && !strings.Contains(labelValue, c.Contains) {
			return false
		}
		if c.compiledMatches != nil && !c.compiledMatches.MatchString(labelValue) {
			return false
		}
		if c.compiledNotMatches != nil && c.compiledNotMatches.MatchString(labelValue) {
			return false
		}
	}
	return true
}

// applyTransformOperations applies all compiled operations to the attributes in order.
// Returns the modified attributes slice.
func applyTransformOperations(attrs []*commonpb.KeyValue, ops []compiledOperation, ruleName string) []*commonpb.KeyValue {
	for _, cop := range ops {
		attrs = applyOperation(attrs, cop, ruleName)
	}
	return attrs
}

// applyOperation applies a single compiled operation.
func applyOperation(attrs []*commonpb.KeyValue, cop compiledOperation, ruleName string) []*commonpb.KeyValue {
	op := cop.op

	if len(op.Remove) > 0 {
		return applyRemove(attrs, op.Remove, ruleName)
	}
	if op.Set != nil {
		return applySet(attrs, op.Set, ruleName)
	}
	if op.Rename != nil {
		return applyRename(attrs, op.Rename, ruleName)
	}
	if op.Copy != nil {
		return applyCopy(attrs, op.Copy, ruleName)
	}
	if op.Replace != nil {
		return applyReplace(attrs, op.Replace, cop.compiledReplace, ruleName)
	}
	if op.Extract != nil {
		return applyExtract(attrs, op.Extract, cop.compiledExtract, ruleName)
	}
	if op.HashMod != nil {
		return applyHashMod(attrs, op.HashMod, ruleName)
	}
	if op.Lower != nil {
		return applyLower(attrs, op.Lower, ruleName)
	}
	if op.Upper != nil {
		return applyUpper(attrs, op.Upper, ruleName)
	}
	if op.Concat != nil {
		return applyConcat(attrs, op.Concat, ruleName)
	}
	if op.Map != nil {
		return applyMap(attrs, op.Map, cop.compiledMap, ruleName)
	}
	if op.Math != nil {
		return applyMath(attrs, op.Math, ruleName)
	}

	return attrs
}

// applyRemove removes the specified labels.
func applyRemove(attrs []*commonpb.KeyValue, labels []string, ruleName string) []*commonpb.KeyValue {
	remove := make(map[string]bool, len(labels))
	for _, l := range labels {
		remove[l] = true
	}

	result := make([]*commonpb.KeyValue, 0, len(attrs))
	removed := 0
	for _, kv := range attrs {
		if remove[kv.Key] {
			removed++
		} else {
			result = append(result, kv)
		}
	}
	if removed > 0 {
		processingTransformLabelsRemovedTotal.WithLabelValues(ruleName).Add(float64(removed))
		processingTransformOperationsTotal.WithLabelValues(ruleName, "remove").Inc()
	}
	return result
}

// applySet sets a label to a value with interpolation support.
func applySet(attrs []*commonpb.KeyValue, op *SetOp, ruleName string) []*commonpb.KeyValue {
	value := interpolateLabels(op.Value, attrs)
	attrs = setLabel(attrs, op.Label, value)
	processingTransformLabelsAddedTotal.WithLabelValues(ruleName).Inc()
	processingTransformOperationsTotal.WithLabelValues(ruleName, "set").Inc()
	return attrs
}

// applyRename renames a label.
func applyRename(attrs []*commonpb.KeyValue, op *RenameOp, ruleName string) []*commonpb.KeyValue {
	for _, kv := range attrs {
		if kv.Key == op.Source {
			kv.Key = op.Target
			processingTransformLabelsModifiedTotal.WithLabelValues(ruleName).Inc()
			processingTransformOperationsTotal.WithLabelValues(ruleName, "rename").Inc()
			return attrs
		}
	}
	return attrs
}

// applyCopy copies a label value to a new name.
func applyCopy(attrs []*commonpb.KeyValue, op *CopyOp, ruleName string) []*commonpb.KeyValue {
	value := getLabelValue(attrs, op.Source)
	if value != "" {
		attrs = setLabel(attrs, op.Target, value)
		processingTransformLabelsAddedTotal.WithLabelValues(ruleName).Inc()
		processingTransformOperationsTotal.WithLabelValues(ruleName, "copy").Inc()
	}
	return attrs
}

// applyReplace does regex replacement on a label value.
func applyReplace(attrs []*commonpb.KeyValue, op *ReplaceOp, re *regexp.Regexp, ruleName string) []*commonpb.KeyValue {
	for _, kv := range attrs {
		if kv.Key == op.Label {
			oldVal := kv.Value.GetStringValue()
			newVal := re.ReplaceAllString(oldVal, op.Replacement)
			if newVal != oldVal {
				kv.Value = &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: newVal}}
				processingTransformLabelsModifiedTotal.WithLabelValues(ruleName).Inc()
			}
			processingTransformOperationsTotal.WithLabelValues(ruleName, "replace").Inc()
			return attrs
		}
	}
	return attrs
}

// applyExtract extracts a regex capture group to a new label.
func applyExtract(attrs []*commonpb.KeyValue, op *ExtractOp, re *regexp.Regexp, ruleName string) []*commonpb.KeyValue {
	sourceVal := getLabelValue(attrs, op.Source)
	if sourceVal == "" {
		return attrs
	}

	matches := re.FindStringSubmatch(sourceVal)
	if matches != nil && op.Group < len(matches) {
		attrs = setLabel(attrs, op.Target, matches[op.Group])
		processingTransformLabelsAddedTotal.WithLabelValues(ruleName).Inc()
	}
	processingTransformOperationsTotal.WithLabelValues(ruleName, "extract").Inc()
	return attrs
}

// applyHashMod hashes a label value mod N to a new label.
func applyHashMod(attrs []*commonpb.KeyValue, op *HashModOp, ruleName string) []*commonpb.KeyValue {
	sourceVal := getLabelValue(attrs, op.Source)
	if sourceVal == "" {
		return attrs
	}

	h := fnv.New64a()
	h.Write([]byte(sourceVal))
	result := h.Sum64() % op.Modulus

	attrs = setLabel(attrs, op.Target, strconv.FormatUint(result, 10))
	processingTransformLabelsAddedTotal.WithLabelValues(ruleName).Inc()
	processingTransformOperationsTotal.WithLabelValues(ruleName, "hash_mod").Inc()
	return attrs
}

// applyLower lowercases a label value.
func applyLower(attrs []*commonpb.KeyValue, op *LabelRef, ruleName string) []*commonpb.KeyValue {
	for _, kv := range attrs {
		if kv.Key == op.Label {
			oldVal := kv.Value.GetStringValue()
			newVal := strings.ToLower(oldVal)
			if newVal != oldVal {
				kv.Value = &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: newVal}}
				processingTransformLabelsModifiedTotal.WithLabelValues(ruleName).Inc()
			}
			processingTransformOperationsTotal.WithLabelValues(ruleName, "lower").Inc()
			return attrs
		}
	}
	return attrs
}

// applyUpper uppercases a label value.
func applyUpper(attrs []*commonpb.KeyValue, op *LabelRef, ruleName string) []*commonpb.KeyValue {
	for _, kv := range attrs {
		if kv.Key == op.Label {
			oldVal := kv.Value.GetStringValue()
			newVal := strings.ToUpper(oldVal)
			if newVal != oldVal {
				kv.Value = &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: newVal}}
				processingTransformLabelsModifiedTotal.WithLabelValues(ruleName).Inc()
			}
			processingTransformOperationsTotal.WithLabelValues(ruleName, "upper").Inc()
			return attrs
		}
	}
	return attrs
}

// applyConcat concatenates multiple label values with a separator.
func applyConcat(attrs []*commonpb.KeyValue, op *ConcatOp, ruleName string) []*commonpb.KeyValue {
	var parts []string
	for _, src := range op.Sources {
		parts = append(parts, getLabelValue(attrs, src))
	}
	attrs = setLabel(attrs, op.Target, strings.Join(parts, op.Separator))
	processingTransformLabelsAddedTotal.WithLabelValues(ruleName).Inc()
	processingTransformOperationsTotal.WithLabelValues(ruleName, "concat").Inc()
	return attrs
}

// applyMap maps a label value via lookup table (supports regex keys).
func applyMap(attrs []*commonpb.KeyValue, op *MapOp, compiled []compiledMapEntry, ruleName string) []*commonpb.KeyValue {
	sourceVal := getLabelValue(attrs, op.Source)

	// Try each compiled pattern.
	for _, entry := range compiled {
		if entry.pattern.MatchString(sourceVal) {
			attrs = setLabel(attrs, op.Target, entry.value)
			processingTransformLabelsAddedTotal.WithLabelValues(ruleName).Inc()
			processingTransformOperationsTotal.WithLabelValues(ruleName, "map").Inc()
			return attrs
		}
	}

	// No match — use default.
	if op.Default != "" {
		attrs = setLabel(attrs, op.Target, op.Default)
		processingTransformLabelsAddedTotal.WithLabelValues(ruleName).Inc()
	}
	processingTransformOperationsTotal.WithLabelValues(ruleName, "map").Inc()
	return attrs
}

// applyMath performs math on a numeric label value.
func applyMath(attrs []*commonpb.KeyValue, op *MathOp, ruleName string) []*commonpb.KeyValue {
	sourceVal := getLabelValue(attrs, op.Source)
	if sourceVal == "" {
		return attrs
	}

	num, err := strconv.ParseFloat(sourceVal, 64)
	if err != nil {
		// Non-numeric label — skip silently.
		return attrs
	}

	var result float64
	switch op.Operation {
	case "add":
		result = num + op.Operand
	case "sub":
		result = num - op.Operand
	case "mul":
		result = num * op.Operand
	case "div":
		if op.Operand == 0 {
			return attrs
		}
		result = num / op.Operand
	case "mod":
		if op.Operand == 0 {
			return attrs
		}
		result = math.Mod(num, op.Operand)
	default:
		return attrs
	}

	// Format as integer if result is a whole number.
	var formatted string
	if result == math.Trunc(result) && !math.IsInf(result, 0) {
		formatted = strconv.FormatInt(int64(result), 10)
	} else {
		formatted = strconv.FormatFloat(result, 'f', -1, 64)
	}

	attrs = setLabel(attrs, op.Target, formatted)
	processingTransformLabelsModifiedTotal.WithLabelValues(ruleName).Inc()
	processingTransformOperationsTotal.WithLabelValues(ruleName, "math").Inc()
	return attrs
}

// --- Helper functions ---

// getLabelValue returns the string value of a label, or "" if not found.
func getLabelValue(attrs []*commonpb.KeyValue, key string) string {
	for _, kv := range attrs {
		if kv.Key == key {
			return kv.Value.GetStringValue()
		}
	}
	return ""
}

// setLabel sets or adds a label. If the label already exists, its value is updated.
func setLabel(attrs []*commonpb.KeyValue, key, value string) []*commonpb.KeyValue {
	for _, kv := range attrs {
		if kv.Key == key {
			kv.Value = &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}
			return attrs
		}
	}
	return append(attrs, &commonpb.KeyValue{
		Key:   key,
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}},
	})
}

// interpolateLabels replaces ${label_name} references with actual label values.
func interpolateLabels(template string, attrs []*commonpb.KeyValue) string {
	if !strings.Contains(template, "${") {
		return template
	}

	return labelInterpolationRe.ReplaceAllStringFunc(template, func(match string) string {
		// Extract label name from ${label_name}
		labelName := match[2 : len(match)-1]
		return getLabelValue(attrs, labelName)
	})
}
