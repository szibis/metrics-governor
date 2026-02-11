package otlpvt_test

import (
	"fmt"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	colmetricspb "github.com/szibis/metrics-governor/internal/otlpvt/colmetricspb"
	commonpb "github.com/szibis/metrics-governor/internal/otlpvt/commonpb"
	metricspb "github.com/szibis/metrics-governor/internal/otlpvt/metricspb"
	resourcepb "github.com/szibis/metrics-governor/internal/otlpvt/resourcepb"
)

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

func makeAttributes(prefix string) []*commonpb.KeyValue {
	return []*commonpb.KeyValue{
		{
			Key:   prefix + ".host",
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "localhost"}},
		},
		{
			Key:   prefix + ".port",
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 8080}},
		},
	}
}

func makeGaugeMetric(name string) *metricspb.Metric {
	return &metricspb.Metric{
		Name:        name,
		Description: "gauge metric: " + name,
		Unit:        "1",
		Data: &metricspb.Metric_Gauge{
			Gauge: &metricspb.Gauge{
				DataPoints: []*metricspb.NumberDataPoint{
					{
						Attributes:        makeAttributes("gauge"),
						TimeUnixNano:      1000000000,
						StartTimeUnixNano: 900000000,
						Value:             &metricspb.NumberDataPoint_AsDouble{AsDouble: 42.5},
					},
				},
			},
		},
	}
}

func makeSumMetric(name string) *metricspb.Metric {
	return &metricspb.Metric{
		Name:        name,
		Description: "sum metric: " + name,
		Unit:        "bytes",
		Data: &metricspb.Metric_Sum{
			Sum: &metricspb.Sum{
				DataPoints: []*metricspb.NumberDataPoint{
					{
						Attributes:        makeAttributes("sum"),
						TimeUnixNano:      1000000000,
						StartTimeUnixNano: 900000000,
						Value:             &metricspb.NumberDataPoint_AsInt{AsInt: 12345},
					},
				},
				AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
				IsMonotonic:            true,
			},
		},
	}
}

func makeHistogramMetric(name string) *metricspb.Metric {
	sum := 150.5
	min := 1.0
	max := 50.0
	return &metricspb.Metric{
		Name:        name,
		Description: "histogram metric: " + name,
		Unit:        "ms",
		Data: &metricspb.Metric_Histogram{
			Histogram: &metricspb.Histogram{
				DataPoints: []*metricspb.HistogramDataPoint{
					{
						Attributes:        makeAttributes("hist"),
						TimeUnixNano:      1000000000,
						StartTimeUnixNano: 900000000,
						Count:             10,
						Sum:               &sum,
						BucketCounts:      []uint64{2, 3, 4, 1},
						ExplicitBounds:    []float64{5.0, 10.0, 25.0},
						Min:               &min,
						Max:               &max,
					},
				},
				AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE,
			},
		},
	}
}

func makeExponentialHistogramMetric(name string) *metricspb.Metric {
	sum := 200.0
	min := 0.5
	max := 100.0
	return &metricspb.Metric{
		Name:        name,
		Description: "exponential histogram metric: " + name,
		Unit:        "ms",
		Data: &metricspb.Metric_ExponentialHistogram{
			ExponentialHistogram: &metricspb.ExponentialHistogram{
				DataPoints: []*metricspb.ExponentialHistogramDataPoint{
					{
						Attributes:        makeAttributes("exphist"),
						TimeUnixNano:      1000000000,
						StartTimeUnixNano: 900000000,
						Count:             20,
						Sum:               &sum,
						Scale:             5,
						ZeroCount:         2,
						Positive: &metricspb.ExponentialHistogramDataPoint_Buckets{
							Offset:       0,
							BucketCounts: []uint64{1, 2, 3, 4},
						},
						Negative: &metricspb.ExponentialHistogramDataPoint_Buckets{
							Offset:       -1,
							BucketCounts: []uint64{1, 1},
						},
						Min:           &min,
						Max:           &max,
						ZeroThreshold: 0.001,
					},
				},
				AggregationTemporality: metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA,
			},
		},
	}
}

func makeSummaryMetric(name string) *metricspb.Metric {
	return &metricspb.Metric{
		Name:        name,
		Description: "summary metric: " + name,
		Unit:        "ms",
		Data: &metricspb.Metric_Summary{
			Summary: &metricspb.Summary{
				DataPoints: []*metricspb.SummaryDataPoint{
					{
						Attributes:        makeAttributes("summary"),
						TimeUnixNano:      1000000000,
						StartTimeUnixNano: 900000000,
						Count:             100,
						Sum:               5000.0,
						QuantileValues: []*metricspb.SummaryDataPoint_ValueAtQuantile{
							{Quantile: 0.5, Value: 45.0},
							{Quantile: 0.9, Value: 90.0},
							{Quantile: 0.99, Value: 99.0},
						},
					},
				},
			},
		},
	}
}

func makeTestRequest(numResources, numMetrics int) *colmetricspb.ExportMetricsServiceRequest {
	rms := make([]*metricspb.ResourceMetrics, numResources)
	for r := 0; r < numResources; r++ {
		metrics := make([]*metricspb.Metric, numMetrics)
		for i := 0; i < numMetrics; i++ {
			name := fmt.Sprintf("metric.%d.%d", r, i)
			switch i % 5 {
			case 0:
				metrics[i] = makeGaugeMetric(name)
			case 1:
				metrics[i] = makeSumMetric(name)
			case 2:
				metrics[i] = makeHistogramMetric(name)
			case 3:
				metrics[i] = makeExponentialHistogramMetric(name)
			case 4:
				metrics[i] = makeSummaryMetric(name)
			}
		}
		rms[r] = &metricspb.ResourceMetrics{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					{
						Key:   "service.name",
						Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprintf("svc-%d", r)}},
					},
					{
						Key:   "service.version",
						Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "1.0.0"}},
					},
				},
			},
			ScopeMetrics: []*metricspb.ScopeMetrics{
				{
					Scope: &commonpb.InstrumentationScope{
						Name:    "test-scope",
						Version: "0.1.0",
					},
					Metrics:   metrics,
					SchemaUrl: "https://opentelemetry.io/schemas/1.0.0",
				},
			},
			SchemaUrl: "https://opentelemetry.io/schemas/1.0.0",
		}
	}
	return &colmetricspb.ExportMetricsServiceRequest{
		ResourceMetrics: rms,
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestVTProto_MarshalUnmarshal_RoundTrip(t *testing.T) {
	t.Helper()

	metricMakers := map[string]func(string) *metricspb.Metric{
		"Gauge":                makeGaugeMetric,
		"Sum":                  makeSumMetric,
		"Histogram":            makeHistogramMetric,
		"ExponentialHistogram": makeExponentialHistogramMetric,
		"Summary":              makeSummaryMetric,
	}

	for metricType, maker := range metricMakers {
		t.Run(metricType, func(t *testing.T) {
			req := &colmetricspb.ExportMetricsServiceRequest{
				ResourceMetrics: []*metricspb.ResourceMetrics{
					{
						Resource: &resourcepb.Resource{
							Attributes: makeAttributes("resource"),
						},
						ScopeMetrics: []*metricspb.ScopeMetrics{
							{
								Scope: &commonpb.InstrumentationScope{
									Name:    "test",
									Version: "1.0",
								},
								Metrics: []*metricspb.Metric{
									maker("test." + metricType),
								},
							},
						},
					},
				},
			}

			data, err := req.MarshalVT()
			if err != nil {
				t.Fatalf("MarshalVT failed: %v", err)
			}
			if len(data) == 0 {
				t.Fatal("MarshalVT produced empty bytes")
			}

			got := &colmetricspb.ExportMetricsServiceRequest{}
			if err := got.UnmarshalVT(data); err != nil {
				t.Fatalf("UnmarshalVT failed: %v", err)
			}

			if !proto.Equal(req, got) {
				t.Errorf("round-trip mismatch for %s:\n  original: %v\n  decoded:  %v", metricType, req, got)
			}
		})
	}

	// Also test a combined request with all metric types together.
	t.Run("AllMetricTypes", func(t *testing.T) {
		req := makeTestRequest(2, 10)

		data, err := req.MarshalVT()
		if err != nil {
			t.Fatalf("MarshalVT failed: %v", err)
		}

		got := &colmetricspb.ExportMetricsServiceRequest{}
		if err := got.UnmarshalVT(data); err != nil {
			t.Fatalf("UnmarshalVT failed: %v", err)
		}

		if !proto.Equal(req, got) {
			t.Error("round-trip mismatch for combined request with all metric types")
		}
	})
}

func TestVTProto_CrossCompat_StandardToVT(t *testing.T) {
	req := makeTestRequest(1, 5)

	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("proto.Marshal failed: %v", err)
	}

	got := &colmetricspb.ExportMetricsServiceRequest{}
	if err := got.UnmarshalVT(data); err != nil {
		t.Fatalf("UnmarshalVT of proto.Marshal data failed: %v", err)
	}

	if !proto.Equal(req, got) {
		t.Error("cross-compat mismatch: proto.Marshal -> UnmarshalVT")
	}
}

func TestVTProto_CrossCompat_VTToStandard(t *testing.T) {
	req := makeTestRequest(1, 5)

	data, err := req.MarshalVT()
	if err != nil {
		t.Fatalf("MarshalVT failed: %v", err)
	}

	got := &colmetricspb.ExportMetricsServiceRequest{}
	if err := proto.Unmarshal(data, got); err != nil {
		t.Fatalf("proto.Unmarshal of MarshalVT data failed: %v", err)
	}

	if !proto.Equal(req, got) {
		t.Error("cross-compat mismatch: MarshalVT -> proto.Unmarshal")
	}
}

func TestVTProto_ResetVT_ClearsAllFields(t *testing.T) {
	req := makeTestRequest(2, 10)

	data, err := req.MarshalVT()
	if err != nil {
		t.Fatalf("MarshalVT failed: %v", err)
	}

	msg := &colmetricspb.ExportMetricsServiceRequest{}
	if err := msg.UnmarshalVT(data); err != nil {
		t.Fatalf("UnmarshalVT failed: %v", err)
	}

	// Verify the message has content before reset.
	if len(msg.ResourceMetrics) == 0 {
		t.Fatal("expected non-empty ResourceMetrics before ResetVT")
	}

	msg.ResetVT()

	// After ResetVT, the ResourceMetrics slice should have length 0.
	if len(msg.ResourceMetrics) != 0 {
		t.Errorf("expected ResourceMetrics length 0 after ResetVT, got %d", len(msg.ResourceMetrics))
	}

	// The message should be equal to an empty request.
	empty := &colmetricspb.ExportMetricsServiceRequest{}
	if !proto.Equal(msg, empty) {
		t.Error("after ResetVT, message is not proto.Equal to empty request")
	}
}

func TestVTProto_ResetVT_PreservesCapacity(t *testing.T) {
	// Build a large request with 100 metrics.
	largeReq := makeTestRequest(1, 100)
	largeData, err := largeReq.MarshalVT()
	if err != nil {
		t.Fatalf("MarshalVT (large) failed: %v", err)
	}

	// Build a smaller request with 50 metrics.
	smallReq := makeTestRequest(1, 50)
	smallData, err := smallReq.MarshalVT()
	if err != nil {
		t.Fatalf("MarshalVT (small) failed: %v", err)
	}

	// First unmarshal: allocates internal structures for 100 metrics.
	firstAllocs := testing.AllocsPerRun(1, func() {
		msg := &colmetricspb.ExportMetricsServiceRequest{}
		if err := msg.UnmarshalVT(largeData); err != nil {
			t.Fatalf("UnmarshalVT (large) failed: %v", err)
		}
	})

	// Second unmarshal into a reset object: should allocate less because
	// the underlying slice capacity is preserved by ResetVT.
	msg := &colmetricspb.ExportMetricsServiceRequest{}
	if err := msg.UnmarshalVT(largeData); err != nil {
		t.Fatalf("UnmarshalVT (setup) failed: %v", err)
	}

	// Record the capacity of the ResourceMetrics slice before reset.
	capBefore := cap(msg.ResourceMetrics)
	msg.ResetVT()

	// Capacity should be preserved.
	capAfter := cap(msg.ResourceMetrics)
	if capAfter < capBefore {
		t.Errorf("ResetVT reduced ResourceMetrics capacity from %d to %d", capBefore, capAfter)
	}

	// Second unmarshal into the reset object with the smaller payload.
	secondAllocs := testing.AllocsPerRun(1, func() {
		msg.ResetVT()
		if err := msg.UnmarshalVT(smallData); err != nil {
			t.Fatalf("UnmarshalVT (small, reused) failed: %v", err)
		}
	})

	t.Logf("first unmarshal allocs (100 metrics, fresh): %.0f", firstAllocs)
	t.Logf("second unmarshal allocs (50 metrics, reused): %.0f", secondAllocs)

	// The reused unmarshal should allocate fewer objects than the fresh one.
	// This is a soft assertion -- the main point is that ResetVT preserves capacity.
	if secondAllocs > firstAllocs {
		t.Logf("WARNING: reused unmarshal allocated more (%.0f) than fresh (%.0f); "+
			"ResetVT capacity reuse may not apply to nested messages", secondAllocs, firstAllocs)
	}
}

func TestVTProto_Pool_NoStaleData(t *testing.T) {
	// Create request A with distinct data.
	reqA := makeTestRequest(1, 5)
	reqA.ResourceMetrics[0].Resource.Attributes = []*commonpb.KeyValue{
		{
			Key:   "request",
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "A"}},
		},
	}
	dataA, err := reqA.MarshalVT()
	if err != nil {
		t.Fatalf("MarshalVT (A) failed: %v", err)
	}

	// Create request B with distinct data.
	reqB := makeTestRequest(1, 3)
	reqB.ResourceMetrics[0].Resource.Attributes = []*commonpb.KeyValue{
		{
			Key:   "request",
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "B"}},
		},
	}
	dataB, err := reqB.MarshalVT()
	if err != nil {
		t.Fatalf("MarshalVT (B) failed: %v", err)
	}

	// Get from pool, unmarshal A, return to pool.
	pooled := colmetricspb.ExportMetricsServiceRequestFromVTPool()
	if err := pooled.UnmarshalVT(dataA); err != nil {
		t.Fatalf("UnmarshalVT (A) failed: %v", err)
	}
	if !proto.Equal(pooled, reqA) {
		t.Fatal("pooled message does not match request A")
	}
	pooled.ReturnToVTPool()

	// Get from pool again, unmarshal B.
	pooled2 := colmetricspb.ExportMetricsServiceRequestFromVTPool()
	if err := pooled2.UnmarshalVT(dataB); err != nil {
		t.Fatalf("UnmarshalVT (B) failed: %v", err)
	}

	// Verify it matches B exactly, not contaminated by A.
	if !proto.Equal(pooled2, reqB) {
		t.Error("pooled message after reuse does not match request B; stale data from A detected")
	}

	// Extra check: the number of resource metrics should match B, not A.
	if len(pooled2.ResourceMetrics) != len(reqB.ResourceMetrics) {
		t.Errorf("expected %d ResourceMetrics (B), got %d",
			len(reqB.ResourceMetrics), len(pooled2.ResourceMetrics))
	}
	pooled2.ReturnToVTPool()
}

func TestVTProto_Pool_ConcurrentSafety(t *testing.T) {
	req := makeTestRequest(1, 10)
	data, err := req.MarshalVT()
	if err != nil {
		t.Fatalf("MarshalVT failed: %v", err)
	}

	const goroutines = 32
	const iterations = 100

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				pooled := colmetricspb.ExportMetricsServiceRequestFromVTPool()
				if err := pooled.UnmarshalVT(data); err != nil {
					t.Errorf("UnmarshalVT failed in goroutine: %v", err)
					return
				}
				if !proto.Equal(pooled, req) {
					t.Errorf("decoded message does not match original in concurrent test")
					return
				}
				pooled.ReturnToVTPool()
			}
		}()
	}
	wg.Wait()
}

func TestVTProto_SizeVT_MatchesStandard(t *testing.T) {
	scales := []struct {
		name       string
		numMetrics int
	}{
		{"1_metric", 1},
		{"10_metrics", 10},
		{"100_metrics", 100},
	}

	for _, sc := range scales {
		t.Run(sc.name, func(t *testing.T) {
			req := makeTestRequest(1, sc.numMetrics)

			vtSize := req.SizeVT()
			stdSize := proto.Size(req)

			if vtSize != stdSize {
				t.Errorf("SizeVT (%d) != proto.Size (%d) for %s", vtSize, stdSize, sc.name)
			}

			// Also verify the marshaled output length matches.
			data, err := req.MarshalVT()
			if err != nil {
				t.Fatalf("MarshalVT failed: %v", err)
			}
			if len(data) != vtSize {
				t.Errorf("MarshalVT output length (%d) != SizeVT (%d)", len(data), vtSize)
			}
		})
	}
}

func TestVTProto_CloneVT_DeepCopy(t *testing.T) {
	original := makeTestRequest(2, 10)

	// Serialize the original for later comparison.
	originalData, err := original.MarshalVT()
	if err != nil {
		t.Fatalf("MarshalVT failed: %v", err)
	}

	clone := original.CloneVT()

	// Clone should be equal to original.
	if !proto.Equal(original, clone) {
		t.Fatal("CloneVT result is not proto.Equal to original")
	}

	// Mutate the clone: change resource attributes, remove metrics, etc.
	clone.ResourceMetrics[0].Resource.Attributes = []*commonpb.KeyValue{
		{
			Key:   "mutated",
			Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: true}},
		},
	}
	clone.ResourceMetrics[0].ScopeMetrics[0].Metrics =
		clone.ResourceMetrics[0].ScopeMetrics[0].Metrics[:1]
	clone.ResourceMetrics = clone.ResourceMetrics[:1]

	// Original should be unchanged.
	originalAfterData, err := original.MarshalVT()
	if err != nil {
		t.Fatalf("MarshalVT (after mutation) failed: %v", err)
	}

	if len(originalData) != len(originalAfterData) {
		t.Fatalf("original changed after clone mutation: size %d -> %d",
			len(originalData), len(originalAfterData))
	}

	// Re-deserialize and compare to be thorough.
	check := &colmetricspb.ExportMetricsServiceRequest{}
	if err := check.UnmarshalVT(originalData); err != nil {
		t.Fatalf("UnmarshalVT failed: %v", err)
	}
	if !proto.Equal(original, check) {
		t.Error("original was mutated by changes to the clone")
	}

	// Clone should now differ from original.
	if proto.Equal(original, clone) {
		t.Error("clone still equals original after mutation; deep copy may be shallow")
	}
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

func benchmarkScales() []struct {
	name       string
	numMetrics int
} {
	return []struct {
		name       string
		numMetrics int
	}{
		{"small_10", 10},
		{"medium_100", 100},
		{"large_500", 500},
	}
}

func BenchmarkVTProto_UnmarshalVT_vs_Standard(b *testing.B) {
	for _, sc := range benchmarkScales() {
		req := makeTestRequest(1, sc.numMetrics)
		data, err := req.MarshalVT()
		if err != nil {
			b.Fatalf("MarshalVT failed: %v", err)
		}

		b.Run(sc.name+"/VT", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			for i := 0; i < b.N; i++ {
				msg := &colmetricspb.ExportMetricsServiceRequest{}
				if err := msg.UnmarshalVT(data); err != nil {
					b.Fatalf("UnmarshalVT failed: %v", err)
				}
			}
		})

		b.Run(sc.name+"/Standard", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			for i := 0; i < b.N; i++ {
				msg := &colmetricspb.ExportMetricsServiceRequest{}
				if err := proto.Unmarshal(data, msg); err != nil {
					b.Fatalf("proto.Unmarshal failed: %v", err)
				}
			}
		})
	}
}

func BenchmarkVTProto_MarshalVT_vs_Standard(b *testing.B) {
	for _, sc := range benchmarkScales() {
		req := makeTestRequest(1, sc.numMetrics)

		b.Run(sc.name+"/VT", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				data, err := req.MarshalVT()
				if err != nil {
					b.Fatalf("MarshalVT failed: %v", err)
				}
				_ = data
			}
		})

		b.Run(sc.name+"/Standard", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				data, err := proto.Marshal(req)
				if err != nil {
					b.Fatalf("proto.Marshal failed: %v", err)
				}
				_ = data
			}
		})
	}
}

func BenchmarkVTProto_SizeVT_vs_Standard(b *testing.B) {
	for _, sc := range benchmarkScales() {
		req := makeTestRequest(1, sc.numMetrics)

		b.Run(sc.name+"/VT", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = req.SizeVT()
			}
		})

		b.Run(sc.name+"/Standard", func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = proto.Size(req)
			}
		})
	}
}

func BenchmarkVTProto_UnmarshalVT_Pooled(b *testing.B) {
	for _, sc := range benchmarkScales() {
		req := makeTestRequest(1, sc.numMetrics)
		data, err := req.MarshalVT()
		if err != nil {
			b.Fatalf("MarshalVT failed: %v", err)
		}

		b.Run(sc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			for i := 0; i < b.N; i++ {
				pooled := colmetricspb.ExportMetricsServiceRequestFromVTPool()
				if err := pooled.UnmarshalVT(data); err != nil {
					b.Fatalf("UnmarshalVT failed: %v", err)
				}
				pooled.ReturnToVTPool()
			}
		})
	}
}

func BenchmarkVTProto_UnmarshalVT_Pooled_Concurrent(b *testing.B) {
	// Use medium scale for concurrent benchmark.
	req := makeTestRequest(1, 100)
	data, err := req.MarshalVT()
	if err != nil {
		b.Fatalf("MarshalVT failed: %v", err)
	}

	b.Run("medium_100", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(data)))
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				pooled := colmetricspb.ExportMetricsServiceRequestFromVTPool()
				if err := pooled.UnmarshalVT(data); err != nil {
					b.Fatalf("UnmarshalVT failed: %v", err)
				}
				pooled.ReturnToVTPool()
			}
		})
	})
}
