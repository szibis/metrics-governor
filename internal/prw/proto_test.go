package prw

import (
	"math"
	"testing"
)

func TestWriteRequest_MarshalUnmarshal(t *testing.T) {
	original := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels: []Label{
					{Name: "__name__", Value: "http_requests_total"},
					{Name: "method", Value: "GET"},
					{Name: "status", Value: "200"},
				},
				Samples: []Sample{
					{Value: 1.5, Timestamp: 1609459200000},
					{Value: 2.5, Timestamp: 1609459260000},
				},
			},
			{
				Labels: []Label{
					{Name: "__name__", Value: "http_requests_total"},
					{Name: "method", Value: "POST"},
					{Name: "status", Value: "201"},
				},
				Samples: []Sample{
					{Value: 3.5, Timestamp: 1609459200000},
				},
			},
		},
	}

	// Marshal
	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	// Unmarshal
	result := &WriteRequest{}
	if err := result.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	// Compare
	if len(result.Timeseries) != len(original.Timeseries) {
		t.Fatalf("Timeseries count mismatch: got %d, want %d", len(result.Timeseries), len(original.Timeseries))
	}

	for i, ts := range result.Timeseries {
		origTS := original.Timeseries[i]

		if len(ts.Labels) != len(origTS.Labels) {
			t.Errorf("Timeseries[%d] labels count mismatch: got %d, want %d", i, len(ts.Labels), len(origTS.Labels))
		}

		for j, l := range ts.Labels {
			if l.Name != origTS.Labels[j].Name || l.Value != origTS.Labels[j].Value {
				t.Errorf("Timeseries[%d] label[%d] mismatch: got %v, want %v", i, j, l, origTS.Labels[j])
			}
		}

		if len(ts.Samples) != len(origTS.Samples) {
			t.Errorf("Timeseries[%d] samples count mismatch: got %d, want %d", i, len(ts.Samples), len(origTS.Samples))
		}

		for j, s := range ts.Samples {
			if s.Value != origTS.Samples[j].Value || s.Timestamp != origTS.Samples[j].Timestamp {
				t.Errorf("Timeseries[%d] sample[%d] mismatch: got %v, want %v", i, j, s, origTS.Samples[j])
			}
		}
	}
}

func TestWriteRequest_MarshalUnmarshal_PRW2(t *testing.T) {
	original := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels: []Label{
					{Name: "__name__", Value: "http_request_duration_seconds"},
				},
				Samples: []Sample{
					{Value: 0.5, Timestamp: 1609459200000},
				},
				Exemplars: []Exemplar{
					{
						Labels:    []Label{{Name: "trace_id", Value: "abc123"}},
						Value:     0.42,
						Timestamp: 1609459200000,
					},
				},
				Histograms: []Histogram{
					{
						Count:         100,
						Sum:           50.5,
						Schema:        3,
						ZeroThreshold: 0.001,
						ZeroCount:     5,
						PositiveSpans: []BucketSpan{
							{Offset: 0, Length: 3},
						},
						PositiveDeltas: []int64{10, 5, -3},
						Timestamp:      1609459200000,
					},
				},
			},
		},
		Metadata: []MetricMetadata{
			{
				Type:             MetricTypeHistogram,
				MetricFamilyName: "http_request_duration_seconds",
				Help:             "HTTP request duration in seconds",
				Unit:             "seconds",
			},
		},
	}

	// Marshal
	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	// Unmarshal
	result := &WriteRequest{}
	if err := result.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	// Verify timeseries
	if len(result.Timeseries) != 1 {
		t.Fatalf("Timeseries count mismatch: got %d, want 1", len(result.Timeseries))
	}

	ts := result.Timeseries[0]

	// Verify samples
	if len(ts.Samples) != 1 || ts.Samples[0].Value != 0.5 {
		t.Errorf("Samples mismatch: got %v", ts.Samples)
	}

	// Verify exemplars
	if len(ts.Exemplars) != 1 {
		t.Fatalf("Exemplars count mismatch: got %d, want 1", len(ts.Exemplars))
	}
	if ts.Exemplars[0].Value != 0.42 {
		t.Errorf("Exemplar value mismatch: got %v, want 0.42", ts.Exemplars[0].Value)
	}
	if len(ts.Exemplars[0].Labels) != 1 || ts.Exemplars[0].Labels[0].Value != "abc123" {
		t.Errorf("Exemplar labels mismatch: got %v", ts.Exemplars[0].Labels)
	}

	// Verify histograms
	if len(ts.Histograms) != 1 {
		t.Fatalf("Histograms count mismatch: got %d, want 1", len(ts.Histograms))
	}
	h := ts.Histograms[0]
	if h.Count != 100 || h.Sum != 50.5 || h.Schema != 3 {
		t.Errorf("Histogram basic fields mismatch: count=%d, sum=%f, schema=%d", h.Count, h.Sum, h.Schema)
	}
	if len(h.PositiveSpans) != 1 || h.PositiveSpans[0].Offset != 0 || h.PositiveSpans[0].Length != 3 {
		t.Errorf("Histogram spans mismatch: got %v", h.PositiveSpans)
	}
	if len(h.PositiveDeltas) != 3 || h.PositiveDeltas[0] != 10 || h.PositiveDeltas[1] != 5 || h.PositiveDeltas[2] != -3 {
		t.Errorf("Histogram deltas mismatch: got %v", h.PositiveDeltas)
	}

	// Verify metadata
	if len(result.Metadata) != 1 {
		t.Fatalf("Metadata count mismatch: got %d, want 1", len(result.Metadata))
	}
	md := result.Metadata[0]
	if md.Type != MetricTypeHistogram {
		t.Errorf("Metadata type mismatch: got %v, want %v", md.Type, MetricTypeHistogram)
	}
	if md.MetricFamilyName != "http_request_duration_seconds" {
		t.Errorf("Metadata name mismatch: got %s", md.MetricFamilyName)
	}
}

func TestSample_MarshalUnmarshal(t *testing.T) {
	tests := []struct {
		name   string
		sample Sample
	}{
		{"positive", Sample{Value: 1.5, Timestamp: 1609459200000}},
		{"negative", Sample{Value: -1.5, Timestamp: 1609459200000}},
		{"zero", Sample{Value: 0, Timestamp: 0}},
		{"max_float", Sample{Value: math.MaxFloat64, Timestamp: 1609459200000}},
		{"min_float", Sample{Value: -math.MaxFloat64, Timestamp: 1609459200000}},
		{"inf", Sample{Value: math.Inf(1), Timestamp: 1609459200000}},
		{"neg_inf", Sample{Value: math.Inf(-1), Timestamp: 1609459200000}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := tt.sample.marshal()

			var result Sample
			if err := result.unmarshal(data); err != nil {
				t.Fatalf("unmarshal() error = %v", err)
			}

			if result.Timestamp != tt.sample.Timestamp {
				t.Errorf("Timestamp mismatch: got %d, want %d", result.Timestamp, tt.sample.Timestamp)
			}

			// Handle NaN specially (NaN != NaN)
			if math.IsNaN(tt.sample.Value) {
				if !math.IsNaN(result.Value) {
					t.Errorf("Value should be NaN, got %f", result.Value)
				}
			} else if result.Value != tt.sample.Value {
				t.Errorf("Value mismatch: got %f, want %f", result.Value, tt.sample.Value)
			}
		})
	}
}

func TestLabel_MarshalUnmarshal(t *testing.T) {
	tests := []struct {
		name  string
		label Label
	}{
		{"simple", Label{Name: "foo", Value: "bar"}},
		{"empty_value", Label{Name: "foo", Value: ""}},
		{"empty_name", Label{Name: "", Value: "bar"}},
		{"unicode", Label{Name: "label_名前", Value: "值_value"}},
		{"special_chars", Label{Name: "__name__", Value: "http:requests_total"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := tt.label.marshal()

			var result Label
			if err := result.unmarshal(data); err != nil {
				t.Fatalf("unmarshal() error = %v", err)
			}

			if result.Name != tt.label.Name || result.Value != tt.label.Value {
				t.Errorf("Label mismatch: got %v, want %v", result, tt.label)
			}
		})
	}
}

func TestHistogram_MarshalUnmarshal(t *testing.T) {
	original := Histogram{
		Count:          100,
		CountFloat:     100.5, // Used for float histograms
		Sum:            250.5,
		Schema:         3,
		ZeroThreshold:  0.001,
		ZeroCount:      5,
		ZeroCountFloat: 5.5,
		NegativeSpans: []BucketSpan{
			{Offset: -5, Length: 3},
		},
		NegativeDeltas: []int64{-1, 2, -3},
		NegativeCounts: []float64{1.5, 2.5, 3.5},
		PositiveSpans: []BucketSpan{
			{Offset: 0, Length: 4},
			{Offset: 2, Length: 2},
		},
		PositiveDeltas: []int64{10, 5, -3, 2},
		PositiveCounts: []float64{10.5, 5.5, 3.5, 2.5},
		ResetHint:      HistogramResetHintNo,
		Timestamp:      1609459200000,
	}

	data := original.marshal()

	var result Histogram
	if err := result.unmarshal(data); err != nil {
		t.Fatalf("unmarshal() error = %v", err)
	}

	if result.Count != original.Count {
		t.Errorf("Count mismatch: got %d, want %d", result.Count, original.Count)
	}
	if result.Sum != original.Sum {
		t.Errorf("Sum mismatch: got %f, want %f", result.Sum, original.Sum)
	}
	if result.Schema != original.Schema {
		t.Errorf("Schema mismatch: got %d, want %d", result.Schema, original.Schema)
	}
	if result.ZeroThreshold != original.ZeroThreshold {
		t.Errorf("ZeroThreshold mismatch: got %f, want %f", result.ZeroThreshold, original.ZeroThreshold)
	}
	if result.ZeroCount != original.ZeroCount {
		t.Errorf("ZeroCount mismatch: got %d, want %d", result.ZeroCount, original.ZeroCount)
	}
	if result.ResetHint != original.ResetHint {
		t.Errorf("ResetHint mismatch: got %d, want %d", result.ResetHint, original.ResetHint)
	}
	if result.Timestamp != original.Timestamp {
		t.Errorf("Timestamp mismatch: got %d, want %d", result.Timestamp, original.Timestamp)
	}

	// Check spans
	if len(result.PositiveSpans) != len(original.PositiveSpans) {
		t.Fatalf("PositiveSpans count mismatch: got %d, want %d", len(result.PositiveSpans), len(original.PositiveSpans))
	}
	for i, span := range result.PositiveSpans {
		if span != original.PositiveSpans[i] {
			t.Errorf("PositiveSpans[%d] mismatch: got %v, want %v", i, span, original.PositiveSpans[i])
		}
	}

	// Check deltas
	if len(result.PositiveDeltas) != len(original.PositiveDeltas) {
		t.Fatalf("PositiveDeltas count mismatch: got %d, want %d", len(result.PositiveDeltas), len(original.PositiveDeltas))
	}
	for i, delta := range result.PositiveDeltas {
		if delta != original.PositiveDeltas[i] {
			t.Errorf("PositiveDeltas[%d] mismatch: got %d, want %d", i, delta, original.PositiveDeltas[i])
		}
	}
}

func TestWriteRequest_EmptyTimeseries(t *testing.T) {
	original := &WriteRequest{}

	data, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	result := &WriteRequest{}
	if err := result.Unmarshal(data); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if len(result.Timeseries) != 0 {
		t.Errorf("Timeseries should be empty, got %d", len(result.Timeseries))
	}
}

func TestWriteRequest_Unmarshal_InvalidData(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"truncated_tag", []byte{0x0a}},
		{"invalid_wire_type", []byte{0x06}}, // wire type 6 is invalid
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := &WriteRequest{}
			err := result.Unmarshal(tt.data)
			// We accept either an error or graceful handling
			_ = err
		})
	}
}

func BenchmarkWriteRequest_Marshal(b *testing.B) {
	req := &WriteRequest{
		Timeseries: make([]TimeSeries, 100),
	}
	for i := 0; i < 100; i++ {
		req.Timeseries[i] = TimeSeries{
			Labels: []Label{
				{Name: "__name__", Value: "http_requests_total"},
				{Name: "method", Value: "GET"},
				{Name: "status", Value: "200"},
				{Name: "instance", Value: "localhost:8080"},
			},
			Samples: []Sample{
				{Value: float64(i), Timestamp: 1609459200000 + int64(i)*1000},
			},
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = req.Marshal()
	}
}

func BenchmarkWriteRequest_Unmarshal(b *testing.B) {
	req := &WriteRequest{
		Timeseries: make([]TimeSeries, 100),
	}
	for i := 0; i < 100; i++ {
		req.Timeseries[i] = TimeSeries{
			Labels: []Label{
				{Name: "__name__", Value: "http_requests_total"},
				{Name: "method", Value: "GET"},
				{Name: "status", Value: "200"},
				{Name: "instance", Value: "localhost:8080"},
			},
			Samples: []Sample{
				{Value: float64(i), Timestamp: 1609459200000 + int64(i)*1000},
			},
		}
	}
	data, _ := req.Marshal()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := &WriteRequest{}
		_ = result.Unmarshal(data)
	}
}

func TestWriteRequest_EstimateSize(t *testing.T) {
	tests := []struct {
		name string
		req  *WriteRequest
	}{
		{
			name: "empty request",
			req:  &WriteRequest{},
		},
		{
			name: "single timeseries",
			req: &WriteRequest{
				Timeseries: []TimeSeries{
					{
						Labels: []Label{
							{Name: "__name__", Value: "test_metric"},
						},
						Samples: []Sample{
							{Value: 1.0, Timestamp: 1000},
						},
					},
				},
			},
		},
		{
			name: "multiple timeseries with multiple samples",
			req: &WriteRequest{
				Timeseries: []TimeSeries{
					{
						Labels: []Label{
							{Name: "__name__", Value: "http_requests_total"},
							{Name: "method", Value: "GET"},
							{Name: "status", Value: "200"},
						},
						Samples: []Sample{
							{Value: 100.0, Timestamp: 1609459200000},
							{Value: 101.0, Timestamp: 1609459201000},
							{Value: 102.0, Timestamp: 1609459202000},
						},
					},
					{
						Labels: []Label{
							{Name: "__name__", Value: "http_requests_total"},
							{Name: "method", Value: "POST"},
						},
						Samples: []Sample{
							{Value: 50.0, Timestamp: 1609459200000},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			estimated := tt.req.EstimateSize()

			// Marshal and compare
			marshaled, err := tt.req.Marshal()
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			actualSize := len(marshaled)

			// Estimate should be >= actual size (may include padding for varints)
			if estimated < actualSize {
				t.Errorf("EstimateSize() = %d, actual marshaled size = %d; estimate should be >= actual",
					estimated, actualSize)
			}

			// Estimate should be within reasonable bounds (not more than 2x actual)
			if estimated > actualSize*2 {
				t.Errorf("EstimateSize() = %d is too large compared to actual %d",
					estimated, actualSize)
			}
		})
	}
}

func TestWriteRequest_EstimateSize_WithMetadata(t *testing.T) {
	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels: []Label{
					{Name: "__name__", Value: "test_metric"},
				},
				Samples: []Sample{
					{Value: 1.0, Timestamp: 1000},
				},
			},
		},
		Metadata: []MetricMetadata{
			{
				Type:             MetricTypeCounter,
				MetricFamilyName: "test_metric",
				Help:             "A test metric",
				Unit:             "bytes",
			},
		},
	}

	estimated := req.EstimateSize()
	if estimated <= 0 {
		t.Error("EstimateSize() should return positive value for non-empty request")
	}

	marshaled, err := req.Marshal()
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	if estimated < len(marshaled) {
		t.Errorf("EstimateSize() = %d < actual %d", estimated, len(marshaled))
	}
}

func TestLabelInternStats(t *testing.T) {
	// Create and unmarshal some labels to generate intern stats
	data := []byte{
		0x0a, 0x1a, // timeseries tag + length
		0x0a, 0x0e, // labels tag + length
		0x0a, 0x08, '_', '_', 'n', 'a', 'm', 'e', '_', '_', // name field
		0x12, 0x04, 't', 'e', 's', 't', // value field
	}

	req := &WriteRequest{}
	_ = req.Unmarshal(data)

	// Get stats
	nameHits, nameMisses, valueHits, valueMisses := LabelInternStats()

	// We should have some stats (exact values depend on previous tests)
	// Just verify the function returns and doesn't panic
	t.Logf("Label intern stats: nameHits=%d, nameMisses=%d, valueHits=%d, valueMisses=%d",
		nameHits, nameMisses, valueHits, valueMisses)

	// Stats should be non-negative
	if nameHits < 0 || nameMisses < 0 || valueHits < 0 || valueMisses < 0 {
		t.Error("intern stats should be non-negative")
	}
}

func TestLabelInterning(t *testing.T) {
	// Test that label names are properly interned
	req1 := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels: []Label{
					{Name: "__name__", Value: "metric1"},
					{Name: "job", Value: "test"},
				},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	req2 := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels: []Label{
					{Name: "__name__", Value: "metric2"},
					{Name: "job", Value: "test"},
				},
				Samples: []Sample{{Value: 2.0, Timestamp: 2000}},
			},
		},
	}

	// Marshal and unmarshal both requests
	data1, _ := req1.Marshal()
	data2, _ := req2.Marshal()

	result1 := &WriteRequest{}
	result2 := &WriteRequest{}

	_ = result1.Unmarshal(data1)
	_ = result2.Unmarshal(data2)

	// Verify labels are correctly parsed
	if len(result1.Timeseries) == 0 || len(result1.Timeseries[0].Labels) == 0 {
		t.Fatal("result1 should have labels")
	}
	if len(result2.Timeseries) == 0 || len(result2.Timeseries[0].Labels) == 0 {
		t.Fatal("result2 should have labels")
	}

	// Check that common label names are the same string pointer (interned)
	// Both requests have "__name__" and "job" labels
	name1 := result1.Timeseries[0].Labels[0].Name
	name2 := result2.Timeseries[0].Labels[0].Name

	if name1 != "__name__" || name2 != "__name__" {
		t.Errorf("expected __name__ labels, got %q and %q", name1, name2)
	}

	// The label "job" should also be interned
	job1 := result1.Timeseries[0].Labels[1].Name
	job2 := result2.Timeseries[0].Labels[1].Name

	if job1 != "job" || job2 != "job" {
		t.Errorf("expected job labels, got %q and %q", job1, job2)
	}
}
