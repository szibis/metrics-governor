package sampling

import (
	"regexp"
	"testing"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
)

// makeTestAttrs creates a slice of KeyValue attributes from alternating key-value pairs.
func makeTestAttrs(kvs ...string) []*commonpb.KeyValue {
	var attrs []*commonpb.KeyValue
	for i := 0; i+1 < len(kvs); i += 2 {
		attrs = append(attrs, &commonpb.KeyValue{
			Key:   kvs[i],
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: kvs[i+1]}},
		})
	}
	return attrs
}

// getTestAttr returns the string value for a label key, or "" if not found.
func getTestAttr(attrs []*commonpb.KeyValue, key string) string {
	for _, kv := range attrs {
		if kv.Key == key {
			return kv.Value.GetStringValue()
		}
	}
	return ""
}

// hasTestAttr returns true if the key exists in attrs.
func hasTestAttr(attrs []*commonpb.KeyValue, key string) bool {
	for _, kv := range attrs {
		if kv.Key == key {
			return true
		}
	}
	return false
}

// compileConditions compiles the Matches and NotMatches fields in a slice of Conditions.
func compileConditions(t *testing.T, conds []Condition) []Condition {
	t.Helper()
	out := make([]Condition, len(conds))
	for i, c := range conds {
		out[i] = c
		if c.Matches != "" {
			re, err := regexp.Compile("^(?:" + c.Matches + ")$")
			if err != nil {
				t.Fatalf("bad matches regex %q: %v", c.Matches, err)
			}
			out[i].compiledMatches = re
		}
		if c.NotMatches != "" {
			re, err := regexp.Compile("^(?:" + c.NotMatches + ")$")
			if err != nil {
				t.Fatalf("bad not_matches regex %q: %v", c.NotMatches, err)
			}
			out[i].compiledNotMatches = re
		}
	}
	return out
}

// compileMapEntries builds compiledMapEntry slices from pattern->value pairs.
func compileMapEntries(t *testing.T, values map[string]string) []compiledMapEntry {
	t.Helper()
	var entries []compiledMapEntry
	for pattern, value := range values {
		re, err := regexp.Compile("^(?:" + pattern + ")$")
		if err != nil {
			t.Fatalf("bad map pattern %q: %v", pattern, err)
		}
		entries = append(entries, compiledMapEntry{pattern: re, value: value})
	}
	return entries
}

// --- Test 1: Chain first-match semantics ---

func TestClassifyChainFirstMatchWins(t *testing.T) {
	t.Parallel()

	cc := &compiledClassifyConfig{
		chains: []compiledClassifyChain{
			{
				conditions: compileConditions(t, []Condition{
					{Label: "env", Equals: "production"},
				}),
				set: map[string]string{"tier": "gold"},
			},
			{
				conditions: compileConditions(t, []Condition{
					{Label: "env", Equals: "production"},
				}),
				set: map[string]string{"tier": "silver"},
			},
		},
	}

	attrs := makeTestAttrs("env", "production")
	result := applyClassify(attrs, cc, nil)

	if got := getTestAttr(result, "tier"); got != "gold" {
		t.Errorf("tier = %q, want %q (first chain should win)", got, "gold")
	}
}

// --- Test 2: Chain with all condition types ---

func TestClassifyChainAllConditionTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		attrs []*commonpb.KeyValue
		want  bool
	}{
		{
			name:  "all_conditions_pass",
			attrs: makeTestAttrs("env", "production", "region", "us-east-1", "namespace", "my-production-ns", "cluster", "staging-old"),
			want:  true,
		},
		{
			name:  "equals_fails",
			attrs: makeTestAttrs("env", "staging", "region", "us-east-1", "namespace", "my-production-ns", "cluster", "dev"),
			want:  false,
		},
		{
			name:  "matches_fails",
			attrs: makeTestAttrs("env", "production", "region", "eu-west-1", "namespace", "my-production-ns", "cluster", "dev"),
			want:  false,
		},
		{
			name:  "contains_fails",
			attrs: makeTestAttrs("env", "production", "region", "us-east-1", "namespace", "my-dev-ns", "cluster", "dev"),
			want:  false,
		},
		{
			name:  "not_matches_fails",
			attrs: makeTestAttrs("env", "production", "region", "us-east-1", "namespace", "my-production-ns", "cluster", "production"),
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cc := &compiledClassifyConfig{
				chains: []compiledClassifyChain{
					{
						conditions: compileConditions(t, []Condition{
							{Label: "env", Equals: "production"},
							{Label: "region", Matches: "us-.*"},
							{Label: "namespace", Contains: "production"},
							{Label: "cluster", NotMatches: "production"},
						}),
						set: map[string]string{"classified": "yes"},
					},
				},
			}

			result := applyClassify(tt.attrs, cc, nil)
			got := getTestAttr(result, "classified")
			if tt.want && got != "yes" {
				t.Errorf("classified = %q, want %q", got, "yes")
			}
			if !tt.want && got == "yes" {
				t.Error("classified should not be set when conditions fail")
			}
		})
	}
}

// --- Test 3: Chain set with interpolation ---

func TestClassifyChainInterpolation(t *testing.T) {
	t.Parallel()

	cc := &compiledClassifyConfig{
		chains: []compiledClassifyChain{
			{
				conditions: compileConditions(t, []Condition{
					{Label: "env", Equals: "production"},
				}),
				set: map[string]string{
					"fqdn": "${service}.${namespace}.svc.cluster.local",
					"tag":  "env-${env}",
				},
			},
		},
	}

	attrs := makeTestAttrs("env", "production", "service", "api-gateway", "namespace", "platform")
	result := applyClassify(attrs, cc, nil)

	if got := getTestAttr(result, "fqdn"); got != "api-gateway.platform.svc.cluster.local" {
		t.Errorf("fqdn = %q, want %q", got, "api-gateway.platform.svc.cluster.local")
	}
	if got := getTestAttr(result, "tag"); got != "env-production" {
		t.Errorf("tag = %q, want %q", got, "env-production")
	}
}

// --- Test 4: Mapping single source ---

func TestClassifyMappingSingleSource(t *testing.T) {
	t.Parallel()

	cc := &compiledClassifyConfig{
		mappings: []compiledClassifyMapping{
			{
				sources:   []string{"env"},
				separator: ":",
				target:    "tier",
				entries: compileMapEntries(t, map[string]string{
					"production": "gold",
					"staging":    "silver",
				}),
			},
		},
	}

	tests := []struct {
		env  string
		want string
	}{
		{"production", "gold"},
		{"staging", "silver"},
	}

	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			t.Parallel()
			attrs := makeTestAttrs("env", tt.env)
			result := applyClassify(attrs, cc, nil)
			if got := getTestAttr(result, "tier"); got != tt.want {
				t.Errorf("tier = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Test 5: Mapping multi-source ---

func TestClassifyMappingMultiSource(t *testing.T) {
	t.Parallel()

	cc := &compiledClassifyConfig{
		mappings: []compiledClassifyMapping{
			{
				sources:   []string{"region", "env"},
				separator: "/",
				target:    "deployment_zone",
				entries: compileMapEntries(t, map[string]string{
					"us-east-1/production": "zone-a",
					"eu-west-1/staging":    "zone-b",
				}),
			},
		},
	}

	tests := []struct {
		name   string
		region string
		env    string
		want   string
	}{
		{"match_us_prod", "us-east-1", "production", "zone-a"},
		{"match_eu_staging", "eu-west-1", "staging", "zone-b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			attrs := makeTestAttrs("region", tt.region, "env", tt.env)
			result := applyClassify(attrs, cc, nil)
			if got := getTestAttr(result, "deployment_zone"); got != tt.want {
				t.Errorf("deployment_zone = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Test 6: Mapping default value ---

func TestClassifyMappingDefault(t *testing.T) {
	t.Parallel()

	cc := &compiledClassifyConfig{
		mappings: []compiledClassifyMapping{
			{
				sources:   []string{"env"},
				separator: ":",
				target:    "tier",
				entries: compileMapEntries(t, map[string]string{
					"production": "gold",
				}),
				dflt: "unknown",
			},
		},
	}

	// Non-matching value should fall back to default.
	attrs := makeTestAttrs("env", "dev")
	result := applyClassify(attrs, cc, nil)
	if got := getTestAttr(result, "tier"); got != "unknown" {
		t.Errorf("tier = %q, want %q (default)", got, "unknown")
	}

	// Matching value should use the mapping, not the default.
	attrs2 := makeTestAttrs("env", "production")
	result2 := applyClassify(attrs2, cc, nil)
	if got := getTestAttr(result2, "tier"); got != "gold" {
		t.Errorf("tier = %q, want %q", got, "gold")
	}
}

// --- Test 7: remove_after ---

func TestClassifyRemoveAfter(t *testing.T) {
	t.Parallel()

	cc := &compiledClassifyConfig{
		chains: []compiledClassifyChain{
			{
				conditions: compileConditions(t, []Condition{
					{Label: "env", Equals: "production"},
				}),
				set: map[string]string{"tier": "gold"},
			},
		},
		removeAfter: []string{"env", "scratch"},
	}

	attrs := makeTestAttrs("env", "production", "service", "web", "scratch", "tmp")
	result := applyClassify(attrs, cc, nil)

	if hasTestAttr(result, "env") {
		t.Error("env should have been removed after classification")
	}
	if hasTestAttr(result, "scratch") {
		t.Error("scratch should have been removed after classification")
	}
	if got := getTestAttr(result, "tier"); got != "gold" {
		t.Errorf("tier = %q, want %q", got, "gold")
	}
	if got := getTestAttr(result, "service"); got != "web" {
		t.Errorf("service = %q, want %q (should be preserved)", got, "web")
	}
}

// --- Test 8: Empty chains with mappings only ---

func TestClassifyMappingsOnlyNoChains(t *testing.T) {
	t.Parallel()

	cc := &compiledClassifyConfig{
		mappings: []compiledClassifyMapping{
			{
				sources:   []string{"namespace"},
				separator: ":",
				target:    "team",
				entries: compileMapEntries(t, map[string]string{
					"platform-.*": "platform-eng",
					"data-.*":     "data-eng",
				}),
				dflt: "unassigned",
			},
		},
	}

	tests := []struct {
		name string
		ns   string
		want string
	}{
		{"platform_match", "platform-payments", "platform-eng"},
		{"data_match", "data-pipeline", "data-eng"},
		{"no_match_default", "frontend-web", "unassigned"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			attrs := makeTestAttrs("namespace", tt.ns)
			result := applyClassify(attrs, cc, nil)
			if got := getTestAttr(result, "team"); got != tt.want {
				t.Errorf("team = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- Test 9: Chains only, no mappings ---

func TestClassifyChainsOnlyNoMappings(t *testing.T) {
	t.Parallel()

	cc := &compiledClassifyConfig{
		chains: []compiledClassifyChain{
			{
				conditions: compileConditions(t, []Condition{
					{Label: "env", Equals: "production"},
					{Label: "region", Matches: "us-.*"},
				}),
				set: map[string]string{"priority": "high", "oncall": "us-sre"},
			},
			{
				conditions: compileConditions(t, []Condition{
					{Label: "env", Equals: "production"},
				}),
				set: map[string]string{"priority": "medium", "oncall": "global-sre"},
			},
			{
				conditions: compileConditions(t, []Condition{
					{Label: "env", Equals: "staging"},
				}),
				set: map[string]string{"priority": "low", "oncall": "dev-team"},
			},
		},
	}

	tests := []struct {
		name         string
		attrs        []*commonpb.KeyValue
		wantPriority string
		wantOncall   string
	}{
		{
			name:         "prod_us_first_chain",
			attrs:        makeTestAttrs("env", "production", "region", "us-east-1"),
			wantPriority: "high",
			wantOncall:   "us-sre",
		},
		{
			name:         "prod_eu_second_chain",
			attrs:        makeTestAttrs("env", "production", "region", "eu-west-1"),
			wantPriority: "medium",
			wantOncall:   "global-sre",
		},
		{
			name:         "staging_third_chain",
			attrs:        makeTestAttrs("env", "staging", "region", "us-east-1"),
			wantPriority: "low",
			wantOncall:   "dev-team",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := applyClassify(tt.attrs, cc, nil)
			if got := getTestAttr(result, "priority"); got != tt.wantPriority {
				t.Errorf("priority = %q, want %q", got, tt.wantPriority)
			}
			if got := getTestAttr(result, "oncall"); got != tt.wantOncall {
				t.Errorf("oncall = %q, want %q", got, tt.wantOncall)
			}
		})
	}
}

// --- Test 10: No match in chains ---

func TestClassifyChainNoMatch(t *testing.T) {
	t.Parallel()

	cc := &compiledClassifyConfig{
		chains: []compiledClassifyChain{
			{
				conditions: compileConditions(t, []Condition{
					{Label: "env", Equals: "production"},
				}),
				set: map[string]string{"tier": "gold"},
			},
			{
				conditions: compileConditions(t, []Condition{
					{Label: "env", Equals: "staging"},
				}),
				set: map[string]string{"tier": "silver"},
			},
		},
	}

	// "dev" matches neither chain.
	attrs := makeTestAttrs("env", "dev")
	result := applyClassify(attrs, cc, nil)

	if hasTestAttr(result, "tier") {
		t.Error("tier should not be set when no chain matches")
	}

	// Verify original attributes are preserved.
	if got := getTestAttr(result, "env"); got != "dev" {
		t.Errorf("env = %q, want %q (should be unchanged)", got, "dev")
	}
}

// --- Test 11: Integration via Process() ---

func TestClassifyIntegrationProcess(t *testing.T) {
	t.Parallel()

	cfg := ProcessingConfig{
		Rules: []ProcessingRule{
			{
				Name:   "classify-env",
				Input:  "http_requests_total",
				Action: ActionClassify,
				Classify: &ClassifyConfig{
					Chains: []ClassifyChain{
						{
							When: []Condition{
								{Label: "env", Equals: "production"},
								{Label: "region", Matches: "us-.*"},
							},
							Set: map[string]string{
								"tier":     "gold",
								"sla_team": "us-sre-${region}",
							},
						},
						{
							When: []Condition{
								{Label: "env", Equals: "production"},
							},
							Set: map[string]string{
								"tier":     "silver",
								"sla_team": "global-sre",
							},
						},
					},
					Mappings: []ClassifyMapping{
						{
							Source: "namespace",
							Target: "team_owner",
							Values: map[string]string{
								"platform-.*": "platform-eng",
								"data-.*":     "data-eng",
							},
							Default: "unassigned",
						},
					},
					RemoveAfter: []string{"scratch"},
				},
			},
		},
	}

	sampler, err := NewFromProcessing(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Build a gauge data point with relevant labels.
	dpAttrs := makeTestAttrs(
		"env", "production",
		"region", "us-east-1",
		"namespace", "platform-payments",
		"scratch", "temporary",
	)
	dp := &metricspb.NumberDataPoint{
		Attributes:   dpAttrs,
		TimeUnixNano: 1000,
		Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: 42},
	}
	rms := makeProcGaugeRM("http_requests_total", dp)

	result := sampler.Sample(rms)
	if len(result) == 0 {
		t.Fatal("expected metric to pass through (classify is non-terminal)")
	}

	outDP := result[0].ScopeMetrics[0].Metrics[0].Data.(*metricspb.Metric_Gauge).Gauge.DataPoints[0]
	outAttrs := outDP.Attributes

	// Chain should match first chain (production + us-*), setting tier=gold.
	if got := getTestAttr(outAttrs, "tier"); got != "gold" {
		t.Errorf("tier = %q, want %q", got, "gold")
	}

	// Interpolation: sla_team should be "us-sre-us-east-1".
	if got := getTestAttr(outAttrs, "sla_team"); got != "us-sre-us-east-1" {
		t.Errorf("sla_team = %q, want %q", got, "us-sre-us-east-1")
	}

	// Mapping: namespace=platform-payments should map to team_owner=platform-eng.
	if got := getTestAttr(outAttrs, "team_owner"); got != "platform-eng" {
		t.Errorf("team_owner = %q, want %q", got, "platform-eng")
	}

	// remove_after: scratch should be removed.
	if hasTestAttr(outAttrs, "scratch") {
		t.Error("scratch should have been removed by remove_after")
	}

	// Original labels that were not removed should still be present.
	if got := getTestAttr(outAttrs, "env"); got != "production" {
		t.Errorf("env = %q, want %q (should be preserved)", got, "production")
	}
	if got := getTestAttr(outAttrs, "region"); got != "us-east-1" {
		t.Errorf("region = %q, want %q (should be preserved)", got, "us-east-1")
	}
}

// --- Additional edge cases ---

func TestClassifyNilConfig(t *testing.T) {
	t.Parallel()

	attrs := makeTestAttrs("env", "prod")
	result := applyClassify(attrs, nil, nil)
	if len(result) != 1 {
		t.Fatalf("expected 1 attr, got %d", len(result))
	}
	if got := getTestAttr(result, "env"); got != "prod" {
		t.Errorf("env = %q, want %q", got, "prod")
	}
}

func TestClassifyMappingNoDefaultNoMatch(t *testing.T) {
	t.Parallel()

	cc := &compiledClassifyConfig{
		mappings: []compiledClassifyMapping{
			{
				sources:   []string{"env"},
				separator: ":",
				target:    "tier",
				entries: compileMapEntries(t, map[string]string{
					"production": "gold",
				}),
				dflt: "", // No default.
			},
		},
	}

	attrs := makeTestAttrs("env", "dev")
	result := applyClassify(attrs, cc, nil)

	// Without default and no match, target should not be set.
	if hasTestAttr(result, "tier") {
		t.Error("tier should not be set when no match and no default")
	}
}

func TestClassifyMappingMultiSourceDefaultSeparator(t *testing.T) {
	t.Parallel()

	// Default separator is ":" (set during validation). We simulate that here.
	cc := &compiledClassifyConfig{
		mappings: []compiledClassifyMapping{
			{
				sources:   []string{"host", "port"},
				separator: ":",
				target:    "endpoint_class",
				entries: compileMapEntries(t, map[string]string{
					"web01:8080": "frontend",
					"api01:9090": "backend",
					"db01:.*":    "database",
				}),
				dflt: "other",
			},
		},
	}

	tests := []struct {
		name string
		host string
		port string
		want string
	}{
		{"frontend", "web01", "8080", "frontend"},
		{"backend", "api01", "9090", "backend"},
		{"database_any_port", "db01", "5432", "database"},
		{"fallback_default", "cache01", "6379", "other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			attrs := makeTestAttrs("host", tt.host, "port", tt.port)
			result := applyClassify(attrs, cc, nil)
			if got := getTestAttr(result, "endpoint_class"); got != tt.want {
				t.Errorf("endpoint_class = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClassifyChainsAndMappingsCombined(t *testing.T) {
	t.Parallel()

	cc := &compiledClassifyConfig{
		chains: []compiledClassifyChain{
			{
				conditions: compileConditions(t, []Condition{
					{Label: "env", Equals: "production"},
				}),
				set: map[string]string{"priority": "high"},
			},
		},
		mappings: []compiledClassifyMapping{
			{
				sources:   []string{"service"},
				separator: ":",
				target:    "team",
				entries: compileMapEntries(t, map[string]string{
					"api-.*": "backend",
					"web-.*": "frontend",
				}),
				dflt: "unknown",
			},
		},
	}

	// Both chain and mapping should apply.
	attrs := makeTestAttrs("env", "production", "service", "api-gateway")
	result := applyClassify(attrs, cc, nil)

	if got := getTestAttr(result, "priority"); got != "high" {
		t.Errorf("priority = %q, want %q", got, "high")
	}
	if got := getTestAttr(result, "team"); got != "backend" {
		t.Errorf("team = %q, want %q", got, "backend")
	}
}

func TestClassifyRemoveAfterWithNoChains(t *testing.T) {
	t.Parallel()

	cc := &compiledClassifyConfig{
		mappings: []compiledClassifyMapping{
			{
				sources:   []string{"raw_env"},
				separator: ":",
				target:    "env",
				entries: compileMapEntries(t, map[string]string{
					"prod.*": "production",
					"stg.*":  "staging",
				}),
			},
		},
		removeAfter: []string{"raw_env"},
	}

	attrs := makeTestAttrs("raw_env", "prod-us-east", "service", "web")
	result := applyClassify(attrs, cc, nil)

	if got := getTestAttr(result, "env"); got != "production" {
		t.Errorf("env = %q, want %q", got, "production")
	}
	if hasTestAttr(result, "raw_env") {
		t.Error("raw_env should have been removed by remove_after")
	}
	if got := getTestAttr(result, "service"); got != "web" {
		t.Errorf("service = %q, want %q", got, "web")
	}
}
