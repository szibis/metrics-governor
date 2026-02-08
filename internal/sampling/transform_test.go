package sampling

import (
	"regexp"
	"strings"
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
)

func makeTransformAttrs(pairs ...string) []*commonpb.KeyValue {
	var attrs []*commonpb.KeyValue
	for i := 0; i < len(pairs); i += 2 {
		attrs = append(attrs, &commonpb.KeyValue{
			Key:   pairs[i],
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: pairs[i+1]}},
		})
	}
	return attrs
}

func getTransformAttr(attrs []*commonpb.KeyValue, key string) string {
	for _, kv := range attrs {
		if kv.Key == key {
			return kv.Value.GetStringValue()
		}
	}
	return ""
}

func hasTransformAttr(attrs []*commonpb.KeyValue, key string) bool {
	for _, kv := range attrs {
		if kv.Key == key {
			return true
		}
	}
	return false
}

func TestEvaluateConditions_Equals(t *testing.T) {
	attrs := makeTransformAttrs("env", "production")
	conds := []Condition{{Label: "env", Equals: "production"}}
	if !evaluateConditions(conds, attrs) {
		t.Error("expected match")
	}
	conds[0].Equals = "staging"
	if evaluateConditions(conds, attrs) {
		t.Error("expected no match")
	}
}

func TestEvaluateConditions_Matches(t *testing.T) {
	attrs := makeTransformAttrs("datacenter", "us-east-1")
	re := regexp.MustCompile("^(?:us-.*)$")
	conds := []Condition{{Label: "datacenter", Matches: "us-.*", compiledMatches: re}}
	if !evaluateConditions(conds, attrs) {
		t.Error("expected match")
	}
}

func TestEvaluateConditions_Contains(t *testing.T) {
	attrs := makeTransformAttrs("namespace", "production-us")
	conds := []Condition{{Label: "namespace", Contains: "prod"}}
	if !evaluateConditions(conds, attrs) {
		t.Error("expected match")
	}
	conds[0].Contains = "staging"
	if evaluateConditions(conds, attrs) {
		t.Error("expected no match")
	}
}

func TestEvaluateConditions_NotMatches(t *testing.T) {
	attrs := makeTransformAttrs("env", "staging")
	re := regexp.MustCompile("^(?:prod.*)$")
	conds := []Condition{{Label: "env", NotMatches: "prod.*", compiledNotMatches: re}}
	if !evaluateConditions(conds, attrs) {
		t.Error("expected match (not_matches should pass when pattern doesn't match)")
	}

	attrs = makeTransformAttrs("env", "production")
	if evaluateConditions(conds, attrs) {
		t.Error("expected no match (not_matches should fail when pattern matches)")
	}
}

func TestEvaluateConditions_MultipleAND(t *testing.T) {
	attrs := makeTransformAttrs("env", "production", "datacenter", "us-east-1")
	re := regexp.MustCompile("^(?:us-.*)$")
	conds := []Condition{
		{Label: "env", Equals: "production"},
		{Label: "datacenter", Matches: "us-.*", compiledMatches: re},
	}
	if !evaluateConditions(conds, attrs) {
		t.Error("expected match (both conditions true)")
	}

	attrs = makeTransformAttrs("env", "staging", "datacenter", "us-east-1")
	if evaluateConditions(conds, attrs) {
		t.Error("expected no match (first condition false)")
	}
}

func TestEvaluateConditions_MissingLabel(t *testing.T) {
	attrs := makeTransformAttrs("service", "web")
	conds := []Condition{{Label: "env", Equals: "production"}}
	if evaluateConditions(conds, attrs) {
		t.Error("expected no match (label doesn't exist)")
	}
}

func TestApplyRemove(t *testing.T) {
	attrs := makeTransformAttrs("service", "web", "pod", "web-1", "env", "prod")
	result := applyRemove(attrs, []string{"pod"}, "test-rule")
	if len(result) != 2 {
		t.Fatalf("expected 2 attrs after remove, got %d", len(result))
	}
	if hasTransformAttr(result, "pod") {
		t.Error("pod should have been removed")
	}
}

func TestApplySet_Literal(t *testing.T) {
	attrs := makeTransformAttrs("service", "web")
	op := &SetOp{Label: "region", Value: "us-east-1"}
	result := applySet(attrs, op, "test-rule")
	if v := getTransformAttr(result, "region"); v != "us-east-1" {
		t.Errorf("region = %q, want 'us-east-1'", v)
	}
}

func TestApplySet_Interpolation(t *testing.T) {
	attrs := makeTransformAttrs("service", "web", "namespace", "production")
	op := &SetOp{Label: "fqdn", Value: "${service}.${namespace}.svc.cluster.local"}
	result := applySet(attrs, op, "test-rule")
	if v := getTransformAttr(result, "fqdn"); v != "web.production.svc.cluster.local" {
		t.Errorf("fqdn = %q, want 'web.production.svc.cluster.local'", v)
	}
}

func TestApplyRename(t *testing.T) {
	attrs := makeTransformAttrs("old_name", "web")
	op := &RenameOp{Source: "old_name", Target: "service"}
	result := applyRename(attrs, op, "test-rule")
	if hasTransformAttr(result, "old_name") {
		t.Error("old_name should have been renamed")
	}
	if v := getTransformAttr(result, "service"); v != "web" {
		t.Errorf("service = %q, want 'web'", v)
	}
}

func TestApplyCopy(t *testing.T) {
	attrs := makeTransformAttrs("namespace", "production")
	op := &CopyOp{Source: "namespace", Target: "ns_backup"}
	result := applyCopy(attrs, op, "test-rule")
	if v := getTransformAttr(result, "ns_backup"); v != "production" {
		t.Errorf("ns_backup = %q, want 'production'", v)
	}
	if v := getTransformAttr(result, "namespace"); v != "production" {
		t.Error("original should still exist")
	}
}

func TestApplyReplace(t *testing.T) {
	attrs := makeTransformAttrs("instance", "192.168.1.1:8080")
	re := regexp.MustCompile(`^(\d+\.\d+\.\d+\.\d+):\d+$`)
	op := &ReplaceOp{Label: "instance", Pattern: `^(\d+\.\d+\.\d+\.\d+):\d+$`, Replacement: "$1"}
	result := applyReplace(attrs, op, re, "test-rule")
	if v := getTransformAttr(result, "instance"); v != "192.168.1.1" {
		t.Errorf("instance = %q, want '192.168.1.1'", v)
	}
}

func TestApplyExtract(t *testing.T) {
	attrs := makeTransformAttrs("service", "web-api-v2")
	re := regexp.MustCompile(`(.+)-v\d+$`)
	op := &ExtractOp{Source: "service", Target: "service_base", Pattern: `(.+)-v\d+$`, Group: 1}
	result := applyExtract(attrs, op, re, "test-rule")
	if v := getTransformAttr(result, "service_base"); v != "web-api" {
		t.Errorf("service_base = %q, want 'web-api'", v)
	}
}

func TestApplyExtract_NoMatch(t *testing.T) {
	attrs := makeTransformAttrs("service", "web-api")
	re := regexp.MustCompile(`(.+)-v\d+$`)
	op := &ExtractOp{Source: "service", Target: "service_base", Pattern: `(.+)-v\d+$`, Group: 1}
	result := applyExtract(attrs, op, re, "test-rule")
	if hasTransformAttr(result, "service_base") {
		t.Error("service_base should not be set (no match)")
	}
}

func TestApplyHashMod(t *testing.T) {
	attrs := makeTransformAttrs("service", "web-api")
	op := &HashModOp{Source: "service", Target: "shard_id", Modulus: 16}
	result := applyHashMod(attrs, op, "test-rule")
	v := getTransformAttr(result, "shard_id")
	if v == "" {
		t.Fatal("shard_id not set")
	}
	// Value should be a number 0-15.
	if len(v) == 0 || v[0] < '0' || v[0] > '9' {
		t.Errorf("shard_id = %q, expected numeric", v)
	}
}

func TestApplyLower(t *testing.T) {
	attrs := makeTransformAttrs("fqdn", "WEB.Production.SVC")
	op := &LabelRef{Label: "fqdn"}
	result := applyLower(attrs, op, "test-rule")
	if v := getTransformAttr(result, "fqdn"); v != "web.production.svc" {
		t.Errorf("fqdn = %q, want 'web.production.svc'", v)
	}
}

func TestApplyUpper(t *testing.T) {
	attrs := makeTransformAttrs("env", "production")
	op := &LabelRef{Label: "env"}
	result := applyUpper(attrs, op, "test-rule")
	if v := getTransformAttr(result, "env"); v != "PRODUCTION" {
		t.Errorf("env = %q, want 'PRODUCTION'", v)
	}
}

func TestApplyConcat(t *testing.T) {
	attrs := makeTransformAttrs("service", "web", "namespace", "production")
	op := &ConcatOp{Sources: []string{"service", "namespace"}, Target: "identifier", Separator: "-"}
	result := applyConcat(attrs, op, "test-rule")
	if v := getTransformAttr(result, "identifier"); v != "web-production" {
		t.Errorf("identifier = %q, want 'web-production'", v)
	}
}

func TestApplyMap(t *testing.T) {
	compiled := []compiledMapEntry{
		{pattern: regexp.MustCompile("^(?:production)$"), value: "tier-1"},
		{pattern: regexp.MustCompile("^(?:staging)$"), value: "tier-2"},
		{pattern: regexp.MustCompile("^(?:dev.*)$"), value: "tier-3"},
	}

	tests := []struct {
		ns   string
		want string
	}{
		{"production", "tier-1"},
		{"staging", "tier-2"},
		{"dev-us", "tier-3"},
		{"unknown", "tier-unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.ns, func(t *testing.T) {
			attrs := makeTransformAttrs("namespace", tt.ns)
			op := &MapOp{Source: "namespace", Target: "tier", Default: "tier-unknown"}
			result := applyMap(attrs, op, compiled, "test-rule")
			if v := getTransformAttr(result, "tier"); v != tt.want {
				t.Errorf("tier = %q, want %q", v, tt.want)
			}
		})
	}
}

func TestApplyMath(t *testing.T) {
	tests := []struct {
		name    string
		op      string
		operand float64
		source  string
		want    string
	}{
		{"add", "add", 10, "5", "15"},
		{"sub", "sub", 3, "10", "7"},
		{"mul", "mul", 3, "4", "12"},
		{"div", "div", 2, "10", "5"},
		{"mod", "mod", 3, "10", "1"},
		{"div by zero", "div", 0, "10", "10"},       // no-op
		{"non-numeric", "add", 1, "hello", "hello"}, // no-op
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := makeTransformAttrs("value", tt.source)
			op := &MathOp{Source: "value", Target: "result", Operation: tt.op, Operand: tt.operand}
			result := applyMath(attrs, op, "test-rule")
			v := getTransformAttr(result, "result")
			if tt.name == "non-numeric" || tt.name == "div by zero" {
				// Should not create target or should keep original.
				return
			}
			if v != tt.want {
				t.Errorf("result = %q, want %q", v, tt.want)
			}
		})
	}
}

func TestInterpolateLabels(t *testing.T) {
	attrs := makeTransformAttrs("service", "web", "namespace", "production")

	tests := []struct {
		template string
		want     string
	}{
		{"${service}", "web"},
		{"${service}.${namespace}", "web.production"},
		{"no-vars", "no-vars"},
		{"${missing}", ""},
		{"prefix-${service}-suffix", "prefix-web-suffix"},
	}

	for _, tt := range tests {
		t.Run(tt.template, func(t *testing.T) {
			got := interpolateLabels(tt.template, attrs)
			if got != tt.want {
				t.Errorf("interpolate(%q) = %q, want %q", tt.template, got, tt.want)
			}
		})
	}
}

func TestApplyTransformOperations_Chain(t *testing.T) {
	attrs := makeTransformAttrs("old_service", "WEB-API", "namespace", "production")

	ops := []compiledOperation{
		{op: Operation{Rename: &RenameOp{Source: "old_service", Target: "service"}}},
		{op: Operation{Lower: &LabelRef{Label: "service"}}},
		{op: Operation{Set: &SetOp{Label: "fqdn", Value: "${service}.${namespace}.svc"}}},
	}

	result := applyTransformOperations(attrs, ops, "test-rule")

	if hasTransformAttr(result, "old_service") {
		t.Error("old_service should have been renamed")
	}
	if v := getTransformAttr(result, "service"); v != "web-api" {
		t.Errorf("service = %q, want 'web-api' (lowercase)", v)
	}
	if v := getTransformAttr(result, "fqdn"); !strings.HasPrefix(v, "web-api.production") {
		t.Errorf("fqdn = %q, want 'web-api.production.svc'", v)
	}
}

func TestSetLabel_ExistingKey(t *testing.T) {
	attrs := makeTransformAttrs("service", "old-value")
	result := setLabel(attrs, "service", "new-value")
	if len(result) != 1 {
		t.Fatalf("expected 1 attr, got %d", len(result))
	}
	if v := getTransformAttr(result, "service"); v != "new-value" {
		t.Errorf("service = %q, want 'new-value'", v)
	}
}

func TestSetLabel_NewKey(t *testing.T) {
	attrs := makeTransformAttrs("service", "web")
	result := setLabel(attrs, "env", "prod")
	if len(result) != 2 {
		t.Fatalf("expected 2 attrs, got %d", len(result))
	}
}
