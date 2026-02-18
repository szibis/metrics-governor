package autotune

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// mockSource is a test helper that implements CardinalitySource.
type mockSource struct {
	data *CardinalityData
	err  error
}

func (m *mockSource) FetchCardinality(ctx context.Context) (*CardinalityData, error) {
	return m.data, m.err
}

func TestMultiClient_NoSources(t *testing.T) {
	mc := NewMultiClient(nil, MergeUnion)
	_, err := mc.FetchCardinality(context.Background())
	if err == nil {
		t.Fatal("expected error with no sources")
	}
}

func TestMultiClient_SingleSource(t *testing.T) {
	src := &mockSource{data: &CardinalityData{
		TotalSeries: 5000,
		TopMetrics:  []MetricCardinality{{Name: "up", SeriesCount: 200}},
		FetchedAt:   time.Now(),
		Backend:     BackendVM,
	}}

	mc := NewMultiClient([]CardinalitySource{src}, MergeUnion)
	data, err := mc.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if data.TotalSeries != 5000 {
		t.Errorf("expected 5000 total series, got %d", data.TotalSeries)
	}
	if data.Backend != BackendMulti {
		t.Errorf("expected backend multi, got %s", data.Backend)
	}
}

func TestMultiClient_MergeSum(t *testing.T) {
	s1 := &mockSource{data: &CardinalityData{
		TotalSeries: 1000,
		TopMetrics: []MetricCardinality{
			{Name: "http_requests_total", SeriesCount: 100},
			{Name: "up", SeriesCount: 50},
		},
	}}
	s2 := &mockSource{data: &CardinalityData{
		TotalSeries: 2000,
		TopMetrics: []MetricCardinality{
			{Name: "http_requests_total", SeriesCount: 200},
			{Name: "node_cpu_seconds", SeriesCount: 300},
		},
	}}

	mc := NewMultiClient([]CardinalitySource{s1, s2}, MergeSum)
	data, err := mc.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if data.TotalSeries != 3000 {
		t.Errorf("expected sum 3000 total, got %d", data.TotalSeries)
	}

	metrics := make(map[string]int64)
	for _, m := range data.TopMetrics {
		metrics[m.Name] = m.SeriesCount
	}
	if metrics["http_requests_total"] != 300 {
		t.Errorf("expected http_requests_total sum 300, got %d", metrics["http_requests_total"])
	}
	if metrics["up"] != 50 {
		t.Errorf("expected up 50, got %d", metrics["up"])
	}
	if metrics["node_cpu_seconds"] != 300 {
		t.Errorf("expected node_cpu_seconds 300, got %d", metrics["node_cpu_seconds"])
	}
}

func TestMultiClient_MergeMax(t *testing.T) {
	s1 := &mockSource{data: &CardinalityData{
		TotalSeries: 1000,
		TopMetrics: []MetricCardinality{
			{Name: "up", SeriesCount: 100},
		},
	}}
	s2 := &mockSource{data: &CardinalityData{
		TotalSeries: 3000,
		TopMetrics: []MetricCardinality{
			{Name: "up", SeriesCount: 50},
		},
	}}

	mc := NewMultiClient([]CardinalitySource{s1, s2}, MergeMax)
	data, err := mc.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if data.TotalSeries != 3000 {
		t.Errorf("expected max 3000 total, got %d", data.TotalSeries)
	}

	if len(data.TopMetrics) != 1 || data.TopMetrics[0].SeriesCount != 100 {
		t.Errorf("expected max(100,50)=100 for up, got %v", data.TopMetrics)
	}
}

func TestMultiClient_MergeUnion(t *testing.T) {
	s1 := &mockSource{data: &CardinalityData{
		TotalSeries: 1000,
		TopMetrics: []MetricCardinality{
			{Name: "up", SeriesCount: 100},
		},
	}}
	s2 := &mockSource{data: &CardinalityData{
		TotalSeries: 2000,
		TopMetrics: []MetricCardinality{
			{Name: "up", SeriesCount: 50},
			{Name: "node_cpu_seconds", SeriesCount: 300},
		},
	}}

	mc := NewMultiClient([]CardinalitySource{s1, s2}, MergeUnion)
	data, err := mc.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Union: total is sum of totals.
	if data.TotalSeries != 3000 {
		t.Errorf("expected union total 3000, got %d", data.TotalSeries)
	}

	// Union: per-metric is max.
	metrics := make(map[string]int64)
	for _, m := range data.TopMetrics {
		metrics[m.Name] = m.SeriesCount
	}
	if metrics["up"] != 100 {
		t.Errorf("expected max(100,50)=100 for up, got %d", metrics["up"])
	}
	if metrics["node_cpu_seconds"] != 300 {
		t.Errorf("expected 300 for node_cpu_seconds, got %d", metrics["node_cpu_seconds"])
	}
}

func TestMultiClient_PartialFailure(t *testing.T) {
	s1 := &mockSource{data: &CardinalityData{
		TotalSeries: 5000,
		TopMetrics:  []MetricCardinality{{Name: "up", SeriesCount: 200}},
	}}
	s2 := &mockSource{err: fmt.Errorf("connection refused")}

	mc := NewMultiClient([]CardinalitySource{s1, s2}, MergeUnion)
	data, err := mc.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("expected partial success, got error: %v", err)
	}

	if data.TotalSeries != 5000 {
		t.Errorf("expected 5000 from successful source, got %d", data.TotalSeries)
	}
}

func TestMultiClient_AllSourcesFail(t *testing.T) {
	s1 := &mockSource{err: fmt.Errorf("timeout")}
	s2 := &mockSource{err: fmt.Errorf("connection refused")}

	mc := NewMultiClient([]CardinalitySource{s1, s2}, MergeUnion)
	_, err := mc.FetchCardinality(context.Background())
	if err == nil {
		t.Fatal("expected error when all sources fail")
	}
}

func TestMultiClient_ThreeSources(t *testing.T) {
	s1 := &mockSource{data: &CardinalityData{
		TotalSeries: 100,
		TopMetrics:  []MetricCardinality{{Name: "a", SeriesCount: 10}},
	}}
	s2 := &mockSource{data: &CardinalityData{
		TotalSeries: 200,
		TopMetrics:  []MetricCardinality{{Name: "b", SeriesCount: 20}},
	}}
	s3 := &mockSource{data: &CardinalityData{
		TotalSeries: 300,
		TopMetrics:  []MetricCardinality{{Name: "a", SeriesCount: 30}},
	}}

	mc := NewMultiClient([]CardinalitySource{s1, s2, s3}, MergeSum)
	data, err := mc.FetchCardinality(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if data.TotalSeries != 600 {
		t.Errorf("expected sum 600 total, got %d", data.TotalSeries)
	}

	metrics := make(map[string]int64)
	for _, m := range data.TopMetrics {
		metrics[m.Name] = m.SeriesCount
	}
	// a: 10+30=40, b: 20
	if metrics["a"] != 40 {
		t.Errorf("expected a=40, got %d", metrics["a"])
	}
	if metrics["b"] != 20 {
		t.Errorf("expected b=20, got %d", metrics["b"])
	}
}
