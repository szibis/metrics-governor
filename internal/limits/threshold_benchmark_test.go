package limits

import (
	"net/http"
	"net/http/httptest"
	"testing"

	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
)

// populateBenchEnforcer creates an enforcer with n groups having random datapoint
// counts between 1 and maxDPs per group. Returns the enforcer ready for benchmarking.
func populateBenchEnforcer(b *testing.B, n int, threshold int64) *Enforcer {
	b.Helper()

	cfg := &Config{
		Rules: []Rule{
			{
				Name:              "bench-rule",
				Match:             RuleMatch{MetricName: "bench_.*"},
				MaxDatapointsRate: 100000, // High so we don't trigger enforcement
				MaxCardinality:    100000,
				Action:            ActionLog,
				GroupBy:           []string{"group"},
			},
		},
	}
	LoadConfigFromStruct(cfg)

	enforcer := NewEnforcer(cfg, false, 0)
	if threshold > 0 {
		enforcer.SetStatsThreshold(threshold)
	}

	// Populate groups with varying datapoint counts
	for i := 0; i < n; i++ {
		groupName := string(rune('A'+(i/26)%26)) + string(rune('a'+i%26))
		// Half the groups get low dps, half get high dps
		dpCount := 1
		if i%2 == 0 {
			dpCount = 10
		}
		for j := 0; j < dpCount; j++ {
			rm := createTestResourceMetrics(
				map[string]string{"group": groupName},
				[]*metricspb.Metric{createTestMetric("bench_metric", map[string]string{}, 1)},
			)
			enforcer.Process([]*metricspb.ResourceMetrics{rm})
		}
	}

	return enforcer
}

func BenchmarkServeHTTP_NoThreshold(b *testing.B) {
	enforcer := populateBenchEnforcer(b, 1000, 0)
	defer enforcer.Stop()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		enforcer.ServeHTTP(rec, req)
	}
}

func BenchmarkServeHTTP_WithThreshold(b *testing.B) {
	// threshold=5 filters out groups with <5 dps (the half with 1 dp each)
	enforcer := populateBenchEnforcer(b, 1000, 5)
	defer enforcer.Stop()

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		enforcer.ServeHTTP(rec, req)
	}
}
