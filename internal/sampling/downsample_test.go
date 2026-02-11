package sampling

import (
	"math"
	"testing"
	"time"

	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
)

// ---------------------------------------------------------------------------
// Aggregation Processor Tests
// ---------------------------------------------------------------------------

func TestAggProcessor_Avg(t *testing.T) {
	p := newAggProcessor(DSAvg, time.Minute)
	window := uint64(time.Minute)

	// All in window 0.
	p.ingest(10, 2.0)
	p.ingest(20, 4.0)
	p.ingest(30, 6.0)

	// Cross into window 1 to trigger emission.
	emitted := p.ingest(window+1, 10.0)
	if len(emitted) != 1 {
		t.Fatalf("expected 1 emitted point, got %d", len(emitted))
	}
	if emitted[0].value != 4.0 { // avg(2,4,6)
		t.Errorf("expected avg 4.0, got %f", emitted[0].value)
	}
}

func TestAggProcessor_Min(t *testing.T) {
	p := newAggProcessor(DSMin, time.Minute)
	window := uint64(time.Minute)

	p.ingest(10, 5.0)
	p.ingest(20, 2.0)
	p.ingest(30, 8.0)

	emitted := p.ingest(window+1, 1.0)
	if len(emitted) != 1 {
		t.Fatalf("expected 1 emitted, got %d", len(emitted))
	}
	if emitted[0].value != 2.0 {
		t.Errorf("expected min 2.0, got %f", emitted[0].value)
	}
}

func TestAggProcessor_Max(t *testing.T) {
	p := newAggProcessor(DSMax, time.Minute)
	window := uint64(time.Minute)

	p.ingest(10, 5.0)
	p.ingest(20, 9.0)
	p.ingest(30, 3.0)

	emitted := p.ingest(window+1, 1.0)
	if len(emitted) != 1 {
		t.Fatalf("expected 1 emitted, got %d", len(emitted))
	}
	if emitted[0].value != 9.0 {
		t.Errorf("expected max 9.0, got %f", emitted[0].value)
	}
}

func TestAggProcessor_Sum(t *testing.T) {
	p := newAggProcessor(DSSum, time.Minute)
	window := uint64(time.Minute)

	p.ingest(10, 1.0)
	p.ingest(20, 2.0)
	p.ingest(30, 3.0)

	emitted := p.ingest(window+1, 0.0)
	if len(emitted) != 1 {
		t.Fatalf("expected 1 emitted, got %d", len(emitted))
	}
	if emitted[0].value != 6.0 {
		t.Errorf("expected sum 6.0, got %f", emitted[0].value)
	}
}

func TestAggProcessor_Count(t *testing.T) {
	p := newAggProcessor(DSCount, time.Minute)
	window := uint64(time.Minute)

	p.ingest(10, 100.0)
	p.ingest(20, 200.0)
	p.ingest(30, 300.0)

	emitted := p.ingest(window+1, 0.0)
	if len(emitted) != 1 {
		t.Fatalf("expected 1 emitted, got %d", len(emitted))
	}
	if emitted[0].value != 3.0 {
		t.Errorf("expected count 3, got %f", emitted[0].value)
	}
}

func TestAggProcessor_Last(t *testing.T) {
	p := newAggProcessor(DSLast, time.Minute)
	window := uint64(time.Minute)

	p.ingest(10, 1.0)
	p.ingest(20, 2.0)
	p.ingest(30, 7.0)

	emitted := p.ingest(window+1, 0.0)
	if len(emitted) != 1 {
		t.Fatalf("expected 1 emitted, got %d", len(emitted))
	}
	if emitted[0].value != 7.0 {
		t.Errorf("expected last 7.0, got %f", emitted[0].value)
	}
}

func TestAggProcessor_Delta(t *testing.T) {
	p := newAggProcessor(DSDelta, time.Minute)
	window := uint64(time.Minute)

	// Simulate counter: 100, 150, 200 → delta = 200 - 100 = 100.
	p.ingest(10, 100.0)
	p.ingest(20, 150.0)
	p.ingest(30, 200.0)

	emitted := p.ingest(window+1, 250.0)
	if len(emitted) != 1 {
		t.Fatalf("expected 1 emitted, got %d", len(emitted))
	}
	if emitted[0].value != 100.0 {
		t.Errorf("expected delta 100.0, got %f", emitted[0].value)
	}
}

func TestAggProcessor_MultipleWindows(t *testing.T) {
	p := newAggProcessor(DSAvg, time.Minute)
	window := uint64(time.Minute)

	p.ingest(10, 10.0)
	p.ingest(20, 20.0)

	// Window 1.
	e1 := p.ingest(window+10, 30.0)
	if len(e1) != 1 || e1[0].value != 15.0 {
		t.Fatalf("window 0: expected avg 15.0, got %v", e1)
	}

	p.ingest(window+20, 40.0)

	// Window 2.
	e2 := p.ingest(2*window+10, 50.0)
	if len(e2) != 1 || e2[0].value != 35.0 {
		t.Fatalf("window 1: expected avg 35.0, got %v", e2)
	}
}

func TestAggProcessor_Flush(t *testing.T) {
	p := newAggProcessor(DSSum, time.Minute)
	p.ingest(10, 5.0)
	p.ingest(20, 3.0)

	flushed := p.flush()
	if len(flushed) != 1 {
		t.Fatalf("expected 1 flushed, got %d", len(flushed))
	}
	if flushed[0].value != 8.0 {
		t.Errorf("expected flushed sum 8.0, got %f", flushed[0].value)
	}

	// Double flush → empty.
	if f2 := p.flush(); len(f2) != 0 {
		t.Errorf("expected empty on double flush, got %d", len(f2))
	}
}

func TestAggProcessor_FlushEmpty(t *testing.T) {
	p := newAggProcessor(DSAvg, time.Minute)
	if f := p.flush(); len(f) != 0 {
		t.Errorf("expected empty flush, got %d", len(f))
	}
}

func TestAggProcessor_Timestamp(t *testing.T) {
	p := newAggProcessor(DSLast, time.Minute)
	window := uint64(time.Minute)

	p.ingest(10, 1.0)
	emitted := p.ingest(window+1, 2.0)

	if len(emitted) != 1 {
		t.Fatalf("expected 1 emitted, got %d", len(emitted))
	}
	// Emitted timestamp should be windowEnd - 1.
	if emitted[0].timestamp != window-1 {
		t.Errorf("expected timestamp %d, got %d", window-1, emitted[0].timestamp)
	}
}

// ---------------------------------------------------------------------------
// LTTB Processor Tests
// ---------------------------------------------------------------------------

func TestLTTBProcessor_BasicEmission(t *testing.T) {
	// 1-second buckets, 10 resolution (100ms per bucket).
	p := newLTTBProcessor(time.Second, 10)
	bucketDur := uint64(time.Second) / 10

	// Ingest points in bucket 0.
	p.ingest(0, 1.0)
	p.ingest(10, 2.0)

	// Move to bucket 1 → no emission yet (prevSelected just set).
	e1 := p.ingest(bucketDur, 3.0)
	if len(e1) != 0 {
		t.Fatalf("expected 0 emissions on first bucket transition, got %d", len(e1))
	}

	// Move to bucket 2 → emits prevSelected from bucket 0.
	e2 := p.ingest(2*bucketDur, 4.0)
	if len(e2) != 1 {
		t.Fatalf("expected 1 emission, got %d", len(e2))
	}
}

func TestLTTBProcessor_PreservesExtremes(t *testing.T) {
	p := newLTTBProcessor(time.Second, 10)
	bucketDur := uint64(time.Second) / 10

	// Bucket 0: flat values around 5.0 + one spike at 100.0.
	p.ingest(0, 5.0)
	p.ingest(10, 5.1)
	p.ingest(20, 100.0) // spike
	p.ingest(30, 5.0)

	// Transition to bucket 1.
	p.ingest(bucketDur, 5.0)

	// Transition to bucket 2 → should emit the spike from bucket 0.
	emitted := p.ingest(2*bucketDur, 5.0)
	if len(emitted) != 1 {
		t.Fatalf("expected 1 emitted, got %d", len(emitted))
	}
	if emitted[0].value != 100.0 {
		t.Errorf("LTTB should preserve the spike: expected 100.0, got %f", emitted[0].value)
	}
}

func TestLTTBProcessor_Flush(t *testing.T) {
	p := newLTTBProcessor(time.Second, 5)
	bucketDur := uint64(time.Second) / 5

	p.ingest(0, 1.0)
	p.ingest(bucketDur, 2.0)   // bucket transition sets prevSelected
	p.ingest(2*bucketDur, 3.0) // bucket transition emits + sets prevSelected

	flushed := p.flush()
	// Should emit prevSelected + current bucket's best.
	if len(flushed) < 1 {
		t.Fatalf("expected ≥1 flushed, got %d", len(flushed))
	}
}

func TestLTTBProcessor_SinglePointBuckets(t *testing.T) {
	p := newLTTBProcessor(time.Second, 10)
	bucketDur := uint64(time.Second) / 10

	// One point per bucket.
	p.ingest(0, 1.0)
	p.ingest(bucketDur, 2.0)
	emitted := p.ingest(2*bucketDur, 3.0)
	if len(emitted) != 1 {
		t.Fatalf("expected 1 emitted, got %d", len(emitted))
	}
	if emitted[0].value != 1.0 {
		t.Errorf("expected 1.0, got %f", emitted[0].value)
	}
}

// ---------------------------------------------------------------------------
// SDT Processor Tests
// ---------------------------------------------------------------------------

func TestSDTProcessor_ConstantValue(t *testing.T) {
	p := newSDTProcessor(0.5)

	// First point always emitted.
	e0 := p.ingest(0, 10.0)
	if len(e0) != 1 {
		t.Fatalf("first point should emit, got %d", len(e0))
	}

	// Constant value within deadband → compressed away.
	for i := uint64(1); i <= 20; i++ {
		e := p.ingest(i*1000, 10.0)
		if len(e) != 0 {
			t.Fatalf("constant value should be compressed, but emitted at i=%d", i)
		}
	}
}

func TestSDTProcessor_StepChange(t *testing.T) {
	p := newSDTProcessor(0.5)

	p.ingest(0, 10.0) // first point emitted

	// Stay constant.
	p.ingest(1000, 10.0)
	p.ingest(2000, 10.0)

	// Big step change (well outside deadband of 0.5).
	emitted := p.ingest(3000, 20.0)
	if len(emitted) != 1 {
		t.Fatalf("step change should emit, got %d", len(emitted))
	}
}

func TestSDTProcessor_GradualDrift(t *testing.T) {
	p := newSDTProcessor(1.0) // deadband ±1.0

	p.ingest(0, 10.0) // first point

	// Gradual drift: 10.0, 10.1, 10.2, ... — within deadband.
	emitted := 0
	for i := 1; i <= 5; i++ {
		e := p.ingest(uint64(i)*1000, 10.0+float64(i)*0.1)
		emitted += len(e)
	}
	// Values 10.1 to 10.5 are within ±1.0 deadband of 10.0.
	if emitted != 0 {
		t.Errorf("gradual drift within deadband should not emit, got %d emissions", emitted)
	}

	// Now jump outside deadband.
	e := p.ingest(6000, 15.0)
	if len(e) == 0 {
		t.Error("jump outside deadband should emit")
	}
}

func TestSDTProcessor_Flush(t *testing.T) {
	p := newSDTProcessor(0.5)

	p.ingest(0, 10.0)
	p.ingest(1000, 10.0) // compressed

	flushed := p.flush()
	// Should emit the last compressed point.
	if len(flushed) != 1 {
		t.Fatalf("expected 1 flushed, got %d", len(flushed))
	}
	if flushed[0].value != 10.0 {
		t.Errorf("expected 10.0, got %f", flushed[0].value)
	}
}

func TestSDTProcessor_FlushAfterEmit(t *testing.T) {
	p := newSDTProcessor(0.5)
	p.ingest(0, 10.0) // emitted

	// No subsequent points → flush should be empty.
	if f := p.flush(); len(f) != 0 {
		t.Errorf("expected empty flush, got %d", len(f))
	}
}

// ---------------------------------------------------------------------------
// Adaptive Processor Tests
// ---------------------------------------------------------------------------

func TestAdaptiveProcessor_StableSignal(t *testing.T) {
	p := newAdaptiveProcessor(0.1, 1.0, 20)

	emitted := 0
	// Feed 100 identical values (CV ≈ 0 → keepRate → minRate 0.1).
	for i := 0; i < 100; i++ {
		e := p.ingest(uint64(i)*1000, 50.0)
		emitted += len(e)
	}

	// With minRate=0.1 and 100 points, expect ≈10 emitted.
	if emitted > 20 {
		t.Errorf("stable signal: expected ~10 emitted, got %d (too many)", emitted)
	}
	if emitted < 3 {
		t.Errorf("stable signal: expected ~10 emitted, got %d (too few)", emitted)
	}
}

func TestAdaptiveProcessor_VolatileSignal(t *testing.T) {
	p := newAdaptiveProcessor(0.1, 1.0, 10)

	emitted := 0
	// Feed 100 highly variable values (CV >> 1 → keepRate → maxRate 1.0).
	for i := 0; i < 100; i++ {
		v := float64(i%2) * 1000.0 // alternates 0 and 1000
		e := p.ingest(uint64(i)*1000, v)
		emitted += len(e)
	}

	// With maxRate=1.0, all or most points should be emitted.
	if emitted < 50 {
		t.Errorf("volatile signal: expected ≥50 emitted, got %d", emitted)
	}
}

func TestAdaptiveProcessor_RateAdjusts(t *testing.T) {
	p := newAdaptiveProcessor(0.1, 1.0, 5)

	// Feed 10 stable values to drive keepRate down.
	for i := 0; i < 10; i++ {
		p.ingest(uint64(i)*1000, 50.0)
	}
	lowRate := p.keepRateValue()

	// Feed 10 volatile values.
	for i := 10; i < 20; i++ {
		p.ingest(uint64(i)*1000, float64(i%2)*500.0)
	}
	highRate := p.keepRateValue()

	if highRate <= lowRate {
		t.Errorf("rate should increase with variance: stable=%f, volatile=%f", lowRate, highRate)
	}
}

func TestAdaptiveProcessor_FlushEmpty(t *testing.T) {
	p := newAdaptiveProcessor(0.1, 1.0, 10)
	p.ingest(0, 1.0)
	if f := p.flush(); len(f) != 0 {
		t.Errorf("expected empty flush, got %d", len(f))
	}
}

// ---------------------------------------------------------------------------
// Downsample Engine Tests
// ---------------------------------------------------------------------------

func TestDownsampleEngine_CreateAndReuse(t *testing.T) {
	de := newDownsampleEngine()
	cfg := &DownsampleConfig{
		Method:       DSAvg,
		parsedWindow: time.Minute,
	}

	// First ingest creates a new processor.
	de.ingestAndEmit("series1", cfg, 10, 5.0)
	if de.seriesCount() != 1 {
		t.Errorf("expected 1 series, got %d", de.seriesCount())
	}

	// Same key reuses processor.
	de.ingestAndEmit("series1", cfg, 20, 6.0)
	if de.seriesCount() != 1 {
		t.Errorf("expected 1 series (reused), got %d", de.seriesCount())
	}

	// Different key creates new.
	de.ingestAndEmit("series2", cfg, 30, 7.0)
	if de.seriesCount() != 2 {
		t.Errorf("expected 2 series, got %d", de.seriesCount())
	}
}

func TestDownsampleEngine_FlushAll(t *testing.T) {
	de := newDownsampleEngine()
	cfg := &DownsampleConfig{
		Method:       DSSum,
		parsedWindow: time.Minute,
	}

	de.ingestAndEmit("s1", cfg, 10, 1.0)
	de.ingestAndEmit("s1", cfg, 20, 2.0)
	de.ingestAndEmit("s2", cfg, 10, 10.0)

	flushed := de.flushAll()
	if len(flushed) != 2 {
		t.Fatalf("expected 2 series flushed, got %d", len(flushed))
	}

	for key, pts := range flushed {
		if len(pts) != 1 {
			t.Errorf("series %s: expected 1 flushed point, got %d", key, len(pts))
		}
		switch key {
		case "s1":
			if pts[0].value != 3.0 {
				t.Errorf("s1: expected sum 3.0, got %f", pts[0].value)
			}
		case "s2":
			if pts[0].value != 10.0 {
				t.Errorf("s2: expected sum 10.0, got %f", pts[0].value)
			}
		}
	}

	if de.seriesCount() != 0 {
		t.Errorf("expected 0 series after flushAll, got %d", de.seriesCount())
	}
}

func TestDownsampleEngine_Cleanup(t *testing.T) {
	de := newDownsampleEngine()
	cfg := &DownsampleConfig{
		Method:       DSLast,
		parsedWindow: time.Minute,
	}

	de.ingestAndEmit("active", cfg, 10, 1.0)
	de.ingestAndEmit("stale", cfg, 10, 2.0)

	// Make "stale" old by finding its shard and updating lastSeen.
	shard := &de.shards[fnvShard("stale")]
	shard.mu.Lock()
	shard.lastSeen["stale"] = time.Now().Add(-20 * time.Minute)
	shard.mu.Unlock()

	de.cleanupAll(10 * time.Minute)

	if de.seriesCount() != 1 {
		t.Errorf("expected 1 series after cleanup, got %d", de.seriesCount())
	}
}

// ---------------------------------------------------------------------------
// Helper Function Tests
// ---------------------------------------------------------------------------

func TestBuildDSSeriesKey_Empty(t *testing.T) {
	key := buildDSSeriesKey("cpu_usage", nil)
	if key != "cpu_usage" {
		t.Errorf("expected 'cpu_usage', got %q", key)
	}
}

func TestBuildDSSeriesKey_Sorted(t *testing.T) {
	attrs := []*commonpb.KeyValue{
		{Key: "z", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "1"}}},
		{Key: "a", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "2"}}},
	}
	key := buildDSSeriesKey("metric", attrs)
	expected := "metric|a=2|z=1"
	if key != expected {
		t.Errorf("expected %q, got %q", expected, key)
	}
}

func TestGetNumberValue_Double(t *testing.T) {
	dp := &metricspb.NumberDataPoint{
		Value: &metricspb.NumberDataPoint_AsDouble{AsDouble: 3.14},
	}
	if v := getNumberValue(dp); v != 3.14 {
		t.Errorf("expected 3.14, got %f", v)
	}
}

func TestGetNumberValue_Int(t *testing.T) {
	dp := &metricspb.NumberDataPoint{
		Value: &metricspb.NumberDataPoint_AsInt{AsInt: 42},
	}
	if v := getNumberValue(dp); v != 42.0 {
		t.Errorf("expected 42.0, got %f", v)
	}
}

func TestCloneDatapointWithValue(t *testing.T) {
	template := &metricspb.NumberDataPoint{
		Attributes: []*commonpb.KeyValue{
			{Key: "host", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "web1"}}},
		},
		StartTimeUnixNano: 100,
		TimeUnixNano:      200,
		Value:             &metricspb.NumberDataPoint_AsDouble{AsDouble: 1.0},
		Flags:             1,
	}

	cloned := cloneDatapointWithValue(template, 999, 42.5)
	if cloned.TimeUnixNano != 999 {
		t.Errorf("expected ts 999, got %d", cloned.TimeUnixNano)
	}
	if v := cloned.Value.(*metricspb.NumberDataPoint_AsDouble).AsDouble; v != 42.5 {
		t.Errorf("expected value 42.5, got %f", v)
	}
	if len(cloned.Attributes) != 1 || cloned.Attributes[0].Key != "host" {
		t.Error("attributes not preserved")
	}
	if cloned.StartTimeUnixNano != 100 {
		t.Error("start time not preserved")
	}
}

// ---------------------------------------------------------------------------
// Config Validation Tests
// ---------------------------------------------------------------------------

func TestDownsampleConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     DownsampleConfig
		wantErr bool
	}{
		{"valid avg", DownsampleConfig{Method: DSAvg, Window: "1m"}, false},
		{"valid delta", DownsampleConfig{Method: DSDelta, Window: "5m"}, false},
		{"valid lttb", DownsampleConfig{Method: DSLTTB, Window: "1m", Resolution: 10}, false},
		{"valid sdt", DownsampleConfig{Method: DSSDT, Deviation: 0.5}, false},
		{"valid adaptive", DownsampleConfig{Method: DSAdaptive, Window: "1m", MinRate: 0.1, MaxRate: 1.0}, false},
		{"unknown method", DownsampleConfig{Method: "bogus", Window: "1m"}, true},
		{"avg missing window", DownsampleConfig{Method: DSAvg}, true},
		{"invalid window", DownsampleConfig{Method: DSAvg, Window: "not-a-duration"}, true},
		{"sdt missing deviation", DownsampleConfig{Method: DSSDT, Deviation: 0}, true},
		{"lttb defaults resolution", DownsampleConfig{Method: DSLTTB, Window: "1m"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Integration Test: Downsample in Sampler.Sample()
// ---------------------------------------------------------------------------

func TestSamplerWithDownsampleAvg(t *testing.T) {
	cfg := FileConfig{
		DefaultRate: 1.0,
		Rules: []Rule{
			{
				Name:     "ds-test",
				Match:    map[string]string{"__name__": "test_metric"},
				Strategy: StrategyDownsample,
				Downsample: &DownsampleConfig{
					Method: DSAvg,
					Window: "1m",
				},
			},
		},
	}
	s, err := newFromLegacy(cfg)
	if err != nil {
		t.Fatal(err)
	}

	window := uint64(time.Minute)

	// Build 3 datapoints in window 0 and 1 in window 1.
	rms := []*metricspb.ResourceMetrics{
		makeGaugeRM("test_metric", []dpVal{
			{ts: 10, val: 2.0},
			{ts: 20, val: 4.0},
			{ts: 30, val: 6.0},
			{ts: window + 10, val: 100.0}, // triggers window 0 emission
		}),
	}

	result := s.Sample(rms)
	// Window 0 (avg=4.0) should be emitted; window 1 point is accumulated.
	total := countGaugeDatapoints(result)
	if total != 1 {
		t.Fatalf("expected 1 emitted datapoint, got %d", total)
	}

	val := getFirstGaugeValue(result)
	if val != 4.0 {
		t.Errorf("expected avg 4.0, got %f", val)
	}
}

func TestSamplerWithDownsampleSDT(t *testing.T) {
	cfg := FileConfig{
		DefaultRate: 1.0,
		Rules: []Rule{
			{
				Name:     "sdt-test",
				Match:    map[string]string{"__name__": "stable_metric"},
				Strategy: StrategyDownsample,
				Downsample: &DownsampleConfig{
					Method:    DSSDT,
					Deviation: 1.0,
				},
			},
		},
	}
	s, err := newFromLegacy(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Feed constant values.
	rms := []*metricspb.ResourceMetrics{
		makeGaugeRM("stable_metric", []dpVal{
			{ts: 1000, val: 50.0},
			{ts: 2000, val: 50.0},
			{ts: 3000, val: 50.0},
			{ts: 4000, val: 50.0},
		}),
	}

	result := s.Sample(rms)
	total := countGaugeDatapoints(result)
	// SDT emits first point, then compresses the rest.
	if total != 1 {
		t.Errorf("SDT: expected 1 emitted (first point), got %d", total)
	}
}

func TestSamplerWithDownsamplePassthrough(t *testing.T) {
	// A non-matching metric should pass through normally.
	cfg := FileConfig{
		DefaultRate: 1.0,
		Rules: []Rule{
			{
				Name:     "ds",
				Match:    map[string]string{"__name__": "matched_.*"},
				Strategy: StrategyDownsample,
				Downsample: &DownsampleConfig{
					Method: DSAvg,
					Window: "1m",
				},
			},
		},
	}
	s, err := newFromLegacy(cfg)
	if err != nil {
		t.Fatal(err)
	}

	rms := []*metricspb.ResourceMetrics{
		makeGaugeRM("unmatched_metric", []dpVal{
			{ts: 10, val: 1.0},
			{ts: 20, val: 2.0},
		}),
	}
	result := s.Sample(rms)
	total := countGaugeDatapoints(result)
	if total != 2 {
		t.Errorf("non-matching metric should pass through: expected 2, got %d", total)
	}
}

func TestSamplerDownsampleReloadClearsState(t *testing.T) {
	cfg := FileConfig{
		DefaultRate: 1.0,
		Rules: []Rule{
			{
				Name:     "ds",
				Match:    map[string]string{"__name__": "m"},
				Strategy: StrategyDownsample,
				Downsample: &DownsampleConfig{
					Method: DSSum,
					Window: "1m",
				},
			},
		},
	}
	s, err := newFromLegacy(cfg)
	if err != nil {
		t.Fatal(err)
	}

	// Ingest some data.
	s.Sample([]*metricspb.ResourceMetrics{
		makeGaugeRM("m", []dpVal{{ts: 10, val: 100.0}}),
	})
	if s.dsEngine.seriesCount() != 1 {
		t.Fatal("expected 1 series before reload")
	}

	// Reload clears downsample state.
	if err := reloadFromLegacy(s, cfg); err != nil {
		t.Fatal(err)
	}
	if s.dsEngine.seriesCount() != 0 {
		t.Errorf("expected 0 series after reload, got %d", s.dsEngine.seriesCount())
	}
}

// ---------------------------------------------------------------------------
// Benchmark
// ---------------------------------------------------------------------------

func BenchmarkAggDownsample(b *testing.B) {
	p := newAggProcessor(DSAvg, time.Minute)
	window := uint64(time.Minute)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ts := uint64(i%60) * (window / 60) // cycle through one window
		p.ingest(ts, float64(i))
	}
}

func BenchmarkLTTBDownsample(b *testing.B) {
	p := newLTTBProcessor(time.Minute, 12)
	bucketDur := uint64(time.Minute) / 12

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ts := uint64(i) * (bucketDur / 10)
		p.ingest(ts, float64(i)*0.1+math.Sin(float64(i)))
	}
}

func BenchmarkSDTDownsample(b *testing.B) {
	p := newSDTProcessor(0.5)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.ingest(uint64(i)*1000, float64(i%10))
	}
}

func BenchmarkAdaptiveDownsample(b *testing.B) {
	p := newAdaptiveProcessor(0.1, 1.0, 20)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.ingest(uint64(i)*1000, float64(i%100))
	}
}

// ---------------------------------------------------------------------------
// Test Helpers
// ---------------------------------------------------------------------------

type dpVal struct {
	ts  uint64
	val float64
}

func makeGaugeRM(name string, dps []dpVal) *metricspb.ResourceMetrics {
	points := make([]*metricspb.NumberDataPoint, len(dps))
	for i, d := range dps {
		points[i] = &metricspb.NumberDataPoint{
			TimeUnixNano: d.ts,
			Value:        &metricspb.NumberDataPoint_AsDouble{AsDouble: d.val},
		}
	}
	return &metricspb.ResourceMetrics{
		ScopeMetrics: []*metricspb.ScopeMetrics{
			{
				Metrics: []*metricspb.Metric{
					{
						Name: name,
						Data: &metricspb.Metric_Gauge{
							Gauge: &metricspb.Gauge{DataPoints: points},
						},
					},
				},
			},
		},
	}
}

func countGaugeDatapoints(rms []*metricspb.ResourceMetrics) int {
	total := 0
	for _, rm := range rms {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if g, ok := m.Data.(*metricspb.Metric_Gauge); ok && g.Gauge != nil {
					total += len(g.Gauge.DataPoints)
				}
			}
		}
	}
	return total
}

func getFirstGaugeValue(rms []*metricspb.ResourceMetrics) float64 {
	for _, rm := range rms {
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				if g, ok := m.Data.(*metricspb.Metric_Gauge); ok && g.Gauge != nil && len(g.Gauge.DataPoints) > 0 {
					return getNumberValue(g.Gauge.DataPoints[0])
				}
			}
		}
	}
	return 0
}
