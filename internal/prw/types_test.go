package prw

import (
	"testing"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Version
		wantErr bool
	}{
		{"empty", "", VersionAuto, false},
		{"auto", "auto", VersionAuto, false},
		{"1.0", "1.0", Version1, false},
		{"1", "1", Version1, false},
		{"2.0", "2.0", Version2, false},
		{"2", "2", Version2, false},
		{"invalid", "3.0", VersionAuto, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseVersion(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseVersion() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTimeSeries_MetricName(t *testing.T) {
	ts := &TimeSeries{
		Labels: []Label{
			{Name: "__name__", Value: "http_requests_total"},
			{Name: "method", Value: "GET"},
		},
	}

	if got := ts.MetricName(); got != "http_requests_total" {
		t.Errorf("MetricName() = %v, want http_requests_total", got)
	}

	// Test missing __name__
	ts2 := &TimeSeries{
		Labels: []Label{
			{Name: "method", Value: "GET"},
		},
	}
	if got := ts2.MetricName(); got != "" {
		t.Errorf("MetricName() = %v, want empty", got)
	}
}

func TestTimeSeries_GetLabelValue(t *testing.T) {
	ts := &TimeSeries{
		Labels: []Label{
			{Name: "__name__", Value: "http_requests_total"},
			{Name: "method", Value: "GET"},
			{Name: "status", Value: "200"},
		},
	}

	tests := []struct {
		labelName string
		want      string
	}{
		{"method", "GET"},
		{"status", "200"},
		{"missing", ""},
	}

	for _, tt := range tests {
		t.Run(tt.labelName, func(t *testing.T) {
			if got := ts.GetLabelValue(tt.labelName); got != tt.want {
				t.Errorf("GetLabelValue(%s) = %v, want %v", tt.labelName, got, tt.want)
			}
		})
	}
}

func TestTimeSeries_DatapointCount(t *testing.T) {
	ts := &TimeSeries{
		Labels: []Label{
			{Name: "__name__", Value: "test"},
		},
		Samples: []Sample{
			{Value: 1.0, Timestamp: 1000},
			{Value: 2.0, Timestamp: 2000},
		},
		Exemplars: []Exemplar{
			{Value: 1.0, Timestamp: 1000},
		},
		Histograms: []Histogram{
			{Count: 10, Sum: 50.0, Timestamp: 1000},
		},
	}

	if got := ts.DatapointCount(); got != 4 {
		t.Errorf("DatapointCount() = %v, want 4", got)
	}
}

func TestWriteRequest_TotalDatapoints(t *testing.T) {
	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "metric1"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
			{
				Labels:  []Label{{Name: "__name__", Value: "metric2"}},
				Samples: []Sample{{Value: 2.0, Timestamp: 2000}, {Value: 3.0, Timestamp: 3000}},
			},
		},
	}

	if got := req.TotalDatapoints(); got != 3 {
		t.Errorf("TotalDatapoints() = %v, want 3", got)
	}
}

func TestWriteRequest_TotalTimeseries(t *testing.T) {
	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{Labels: []Label{{Name: "__name__", Value: "metric1"}}},
			{Labels: []Label{{Name: "__name__", Value: "metric2"}}},
			{Labels: []Label{{Name: "__name__", Value: "metric3"}}},
		},
	}

	if got := req.TotalTimeseries(); got != 3 {
		t.Errorf("TotalTimeseries() = %v, want 3", got)
	}
}

func TestWriteRequest_IsPRW2(t *testing.T) {
	// PRW 1.0 request (samples only)
	req1 := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "metric1"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	if req1.IsPRW2() {
		t.Error("IsPRW2() = true for PRW 1.0 request, want false")
	}

	// PRW 2.0 request (with metadata)
	req2 := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "metric1"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
		Metadata: []MetricMetadata{
			{Type: MetricTypeCounter, MetricFamilyName: "metric1"},
		},
	}

	if !req2.IsPRW2() {
		t.Error("IsPRW2() = false for PRW 2.0 request with metadata, want true")
	}

	// PRW 2.0 request (with histograms)
	req3 := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:     []Label{{Name: "__name__", Value: "metric1"}},
				Histograms: []Histogram{{Count: 10, Sum: 50.0, Timestamp: 1000}},
			},
		},
	}

	if !req3.IsPRW2() {
		t.Error("IsPRW2() = false for PRW 2.0 request with histograms, want true")
	}

	// PRW 2.0 request (with exemplars)
	req4 := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:    []Label{{Name: "__name__", Value: "metric1"}},
				Samples:   []Sample{{Value: 1.0, Timestamp: 1000}},
				Exemplars: []Exemplar{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	if !req4.IsPRW2() {
		t.Error("IsPRW2() = false for PRW 2.0 request with exemplars, want true")
	}
}

func TestWriteRequest_Clone(t *testing.T) {
	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "metric1"}, {Name: "env", Value: "prod"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
		Metadata: []MetricMetadata{
			{Type: MetricTypeCounter, MetricFamilyName: "metric1"},
		},
	}

	clone := req.Clone()

	// Modify original
	req.Timeseries[0].Labels[1].Value = "dev"
	req.Timeseries[0].Samples[0].Value = 99.0

	// Clone should be unchanged
	if clone.Timeseries[0].Labels[1].Value != "prod" {
		t.Error("Clone was modified when original changed")
	}
	if clone.Timeseries[0].Samples[0].Value != 1.0 {
		t.Error("Clone sample was modified when original changed")
	}

	// Test nil clone
	var nilReq *WriteRequest
	if nilReq.Clone() != nil {
		t.Error("Clone of nil should be nil")
	}
}

func TestTimeSeries_Clone(t *testing.T) {
	ts := TimeSeries{
		Labels:  []Label{{Name: "__name__", Value: "metric1"}},
		Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
	}

	clone := ts.Clone()

	// Modify original
	ts.Labels[0].Value = "modified"
	ts.Samples[0].Value = 99.0

	// Clone should be unchanged
	if clone.Labels[0].Value != "metric1" {
		t.Error("Clone labels were modified when original changed")
	}
	if clone.Samples[0].Value != 1.0 {
		t.Error("Clone samples were modified when original changed")
	}
}

func TestTimeSeries_SortLabels(t *testing.T) {
	ts := &TimeSeries{
		Labels: []Label{
			{Name: "z_label", Value: "z"},
			{Name: "a_label", Value: "a"},
			{Name: "__name__", Value: "metric"},
			{Name: "m_label", Value: "m"},
		},
	}

	ts.SortLabels()

	expected := []string{"__name__", "a_label", "m_label", "z_label"}
	for i, l := range ts.Labels {
		if l.Name != expected[i] {
			t.Errorf("SortLabels(): label %d = %s, want %s", i, l.Name, expected[i])
		}
	}
}

func TestTimeSeries_LabelsHash(t *testing.T) {
	ts1 := &TimeSeries{
		Labels: []Label{
			{Name: "__name__", Value: "metric1"},
			{Name: "env", Value: "prod"},
		},
	}

	ts2 := &TimeSeries{
		Labels: []Label{
			{Name: "__name__", Value: "metric1"},
			{Name: "env", Value: "prod"},
		},
	}

	ts3 := &TimeSeries{
		Labels: []Label{
			{Name: "__name__", Value: "metric1"},
			{Name: "env", Value: "dev"},
		},
	}

	ts1.SortLabels()
	ts2.SortLabels()
	ts3.SortLabels()

	hash1 := ts1.LabelsHash()
	hash2 := ts2.LabelsHash()
	hash3 := ts3.LabelsHash()

	if hash1 != hash2 {
		t.Error("Same labels should have same hash")
	}

	if hash1 == hash3 {
		t.Error("Different labels should have different hash")
	}
}

func TestMetricType_String(t *testing.T) {
	tests := []struct {
		mt   MetricType
		want string
	}{
		{MetricTypeCounter, "counter"},
		{MetricTypeGauge, "gauge"},
		{MetricTypeHistogram, "histogram"},
		{MetricTypeGaugeHistogram, "gauge_histogram"},
		{MetricTypeSummary, "summary"},
		{MetricTypeInfo, "info"},
		{MetricTypeStateset, "stateset"},
		{MetricTypeUnknown, "unknown"},
		{MetricType(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.mt.String(); got != tt.want {
				t.Errorf("MetricType.String() = %v, want %v", got, tt.want)
			}
		})
	}
}
