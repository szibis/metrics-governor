package limits

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/szibis/metrics-governor/internal/cardinality"
	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
)

func TestStress_EnforcerMapGrowth(t *testing.T) {
	// Initialize cardinality config for trackers
	cardinality.GlobalConfig = cardinality.Config{
		Mode:          cardinality.ModeExact,
		ExpectedItems: 1000,
	}

	config := &Config{
		Rules: []Rule{
			{
				Name:              "test-rule",
				Match:             RuleMatch{MetricName: ""},
				MaxDatapointsRate: 1000000,
				MaxCardinality:    1000000,
				GroupBy:           []string{"service"},
				Action:            ActionLog,
			},
		},
	}

	enforcer := NewEnforcer(config, true, 1000)
	defer enforcer.Stop()

	var mBefore, mAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&mBefore)

	// Simulate many different groups across multiple windows
	for window := 0; window < 3; window++ {
		for i := 0; i < 5000; i++ {
			rm := []*metricspb.ResourceMetrics{
				{
					Resource: &resourcepb.Resource{
						Attributes: []*commonpb.KeyValue{
							{Key: "service", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{
								StringValue: fmt.Sprintf("svc-%d-%d", window, i),
							}}},
						},
					},
					ScopeMetrics: []*metricspb.ScopeMetrics{
						{
							Metrics: []*metricspb.Metric{
								{
									Name: "test_metric",
									Data: &metricspb.Metric_Gauge{
										Gauge: &metricspb.Gauge{
											DataPoints: []*metricspb.NumberDataPoint{
												{
													Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 1.0},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			}
			enforcer.Process(rm)
		}

		// Simulate window boundary by manipulating ruleStats
		enforcer.mu.Lock()
		for _, rs := range enforcer.ruleStats {
			rs.windowEnd = time.Now().Add(-time.Second) // Force window expiry
		}
		enforcer.mu.Unlock()
	}

	runtime.GC()
	runtime.ReadMemStats(&mAfter)

	// HeapAlloc can decrease after GC (desired). Only fail if heap grew significantly.
	if mAfter.HeapAlloc > mBefore.HeapAlloc {
		growth := mAfter.HeapAlloc - mBefore.HeapAlloc
		if growth > 50*1024*1024 {
			t.Fatalf("memory growth too high: %d bytes", growth)
		}
	}
}
