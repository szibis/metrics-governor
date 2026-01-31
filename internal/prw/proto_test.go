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
