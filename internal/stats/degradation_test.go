package stats

import (
	"sync"
	"testing"

	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
)

func TestStatsDegradation_FullToBasic(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelFull)
	if c.Level() != StatsLevelFull {
		t.Fatalf("expected full level, got %s", c.Level())
	}

	ok := c.Degrade()
	if !ok {
		t.Fatal("expected degradation to succeed")
	}
	if c.Level() != StatsLevelBasic {
		t.Errorf("expected basic level after degrade, got %s", c.Level())
	}

	// Process a batch — should use basic processing (no cardinality tracking)
	metrics := makeDegradationTestMetrics(10)
	c.Process(metrics)

	// Verify basic counting still works
	datapoints, _, _ := c.GetGlobalStats()
	if datapoints == 0 {
		t.Error("expected datapoints to be counted at basic level")
	}
}

func TestStatsDegradation_BasicToNone(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelBasic)
	if c.Level() != StatsLevelBasic {
		t.Fatalf("expected basic level, got %s", c.Level())
	}

	ok := c.Degrade()
	if !ok {
		t.Fatal("expected degradation to succeed")
	}
	if c.Level() != StatsLevelNone {
		t.Errorf("expected none level after degrade, got %s", c.Level())
	}

	// Process a batch — should be a no-op
	metrics := makeDegradationTestMetrics(10)
	c.Process(metrics)
}

func TestStatsDegradation_NoneCannotDegrade(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelBasic)

	// First degrade: basic → none
	ok := c.Degrade()
	if !ok {
		t.Fatal("expected first degradation to succeed")
	}

	// Second degrade: none → cannot
	ok = c.Degrade()
	if ok {
		t.Error("expected degradation to fail at none level")
	}
	if c.Level() != StatsLevelNone {
		t.Errorf("expected level to stay at none, got %s", c.Level())
	}
}

func TestStatsDegradation_FullToNone(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelFull)

	// full → basic
	ok := c.Degrade()
	if !ok || c.Level() != StatsLevelBasic {
		t.Fatalf("first degrade: ok=%v, level=%s", ok, c.Level())
	}

	// basic → none
	ok = c.Degrade()
	if !ok || c.Level() != StatsLevelNone {
		t.Fatalf("second degrade: ok=%v, level=%s", ok, c.Level())
	}

	// none → cannot
	ok = c.Degrade()
	if ok {
		t.Error("third degrade should fail")
	}
}

func TestStatsDegradation_PreservesExistingData(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelFull)

	// Collect stats at full level
	for i := 0; i < 100; i++ {
		metrics := makeDegradationTestMetrics(5)
		c.Process(metrics)
	}

	// Record pre-degradation stats
	dpBefore, _, _ := c.GetGlobalStats()
	if dpBefore == 0 {
		t.Fatal("expected datapoints before degradation")
	}

	// Degrade to basic
	c.Degrade()
	if c.Level() != StatsLevelBasic {
		t.Fatalf("expected basic, got %s", c.Level())
	}

	// Existing data should be preserved
	dpAfter, _, _ := c.GetGlobalStats()
	if dpAfter != dpBefore {
		t.Errorf("existing data lost: before=%d after=%d", dpBefore, dpAfter)
	}

	// New batches still counted
	c.Process(makeDegradationTestMetrics(5))
	dpFinal, _, _ := c.GetGlobalStats()
	if dpFinal <= dpBefore {
		t.Error("new batches not counted after degradation")
	}
}

func TestStatsDegradation_ConcurrentProcessDuringDegrade(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelFull)
	var wg sync.WaitGroup

	// 4 goroutines processing batches
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				c.Process(makeDegradationTestMetrics(5))
			}
		}()
	}

	// 1 goroutine calls Degrade() mid-stream
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 100; j++ {
			c.Degrade()
		}
	}()

	wg.Wait()

	// Should not panic or corrupt data
	// Level should be at most none (possibly none after multiple degrades)
	level := c.Level()
	if level != StatsLevelNone && level != StatsLevelBasic {
		t.Errorf("unexpected level after concurrent degrade: %s", level)
	}
}

func TestStatsDegradation_ConfiguredLevelPreserved(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelFull)

	c.Degrade() // full → basic
	if c.ConfiguredLevel() != StatsLevelFull {
		t.Errorf("configured level should remain full, got %s", c.ConfiguredLevel())
	}

	c.Degrade() // basic → none
	if c.ConfiguredLevel() != StatsLevelFull {
		t.Errorf("configured level should remain full, got %s", c.ConfiguredLevel())
	}
}

func TestStatsDegradation_ProcessNoneLevel(t *testing.T) {
	c := NewCollector([]string{"service"}, StatsLevelBasic)
	c.Degrade() // basic → none

	// At none level, Process should be a fast no-op
	for i := 0; i < 1000; i++ {
		c.Process(makeDegradationTestMetrics(10))
	}

	// Should not have counted anything (level=none skips all processing)
	datapoints, _, _ := c.GetGlobalStats()
	if datapoints != 0 {
		t.Errorf("expected 0 datapoints at none level, got %d", datapoints)
	}
}

// makeDegradationTestMetrics creates test metrics for degradation tests.
func makeDegradationTestMetrics(dpPerMetric int) []*metricspb.ResourceMetrics {
	dps := make([]*metricspb.NumberDataPoint, dpPerMetric)
	for i := range dps {
		dps[i] = &metricspb.NumberDataPoint{
			Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: float64(i)},
		}
	}
	return []*metricspb.ResourceMetrics{
		{
			Resource: &resourcepb.Resource{},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Metrics: []*metricspb.Metric{
						{
							Name: "test.degradation",
							Data: &metricspb.Metric_Gauge{
								Gauge: &metricspb.Gauge{
									DataPoints: dps,
								},
							},
						},
					},
				},
			},
		},
	}
}
