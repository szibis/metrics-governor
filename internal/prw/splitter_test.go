package prw

import (
	"testing"

	"github.com/szibis/metrics-governor/internal/sharding"
)

func TestSplitter_Split(t *testing.T) {
	// Create hash ring with 2 endpoints
	hashRing := sharding.NewHashRing(10)
	hashRing.UpdateEndpoints([]string{"endpoint1:9090", "endpoint2:9090"})

	// Create splitter with service label
	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service"}})
	splitter := NewSplitter(keyBuilder, hashRing)

	// Create request with multiple time series
	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels: []Label{
					{Name: "__name__", Value: "http_requests"},
					{Name: "service", Value: "api"},
				},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
			{
				Labels: []Label{
					{Name: "__name__", Value: "http_requests"},
					{Name: "service", Value: "web"},
				},
				Samples: []Sample{{Value: 2.0, Timestamp: 1000}},
			},
			{
				Labels: []Label{
					{Name: "__name__", Value: "http_requests"},
					{Name: "service", Value: "api"},
				},
				Samples: []Sample{{Value: 3.0, Timestamp: 2000}},
			},
		},
	}

	result := splitter.Split(req)

	// Should have split across endpoints
	if len(result) == 0 {
		t.Fatal("Split() returned empty result")
	}

	// Count total time series
	totalTS := 0
	for _, r := range result {
		totalTS += len(r.Timeseries)
	}

	if totalTS != 3 {
		t.Errorf("Split() total timeseries = %d, want 3", totalTS)
	}
}

func TestSplitter_Split_EmptyRequest(t *testing.T) {
	hashRing := sharding.NewHashRing(10)
	hashRing.UpdateEndpoints([]string{"endpoint1:9090"})

	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{}})
	splitter := NewSplitter(keyBuilder, hashRing)

	// Nil request
	result := splitter.Split(nil)
	if result != nil {
		t.Error("Split(nil) should return nil")
	}

	// Empty timeseries
	result = splitter.Split(&WriteRequest{})
	if result != nil {
		t.Error("Split(empty) should return nil")
	}
}

func TestSplitter_Split_EmptyHashRing(t *testing.T) {
	hashRing := sharding.NewHashRing(10)
	// Don't add any endpoints

	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{}})
	splitter := NewSplitter(keyBuilder, hashRing)

	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "test"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
		},
	}

	result := splitter.Split(req)
	if result != nil {
		t.Error("Split() with empty hash ring should return nil")
	}
}

func TestSplitter_Split_WithMetadata(t *testing.T) {
	hashRing := sharding.NewHashRing(10)
	hashRing.UpdateEndpoints([]string{"endpoint1:9090", "endpoint2:9090"})

	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service"}})
	splitter := NewSplitter(keyBuilder, hashRing)

	req := &WriteRequest{
		Timeseries: []TimeSeries{
			{
				Labels:  []Label{{Name: "__name__", Value: "metric1"}, {Name: "service", Value: "a"}},
				Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
			},
			{
				Labels:  []Label{{Name: "__name__", Value: "metric2"}, {Name: "service", Value: "b"}},
				Samples: []Sample{{Value: 2.0, Timestamp: 1000}},
			},
		},
		Metadata: []MetricMetadata{
			{Type: MetricTypeCounter, MetricFamilyName: "metric1"},
			{Type: MetricTypeGauge, MetricFamilyName: "metric2"},
		},
	}

	result := splitter.Split(req)

	// Each endpoint should have the full metadata
	for _, r := range result {
		if len(r.Metadata) != 2 {
			t.Errorf("Split endpoint metadata count = %d, want 2", len(r.Metadata))
		}
	}
}

func TestSplitter_SplitTimeseries(t *testing.T) {
	hashRing := sharding.NewHashRing(10)
	hashRing.UpdateEndpoints([]string{"ep1", "ep2", "ep3"})

	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{}})
	splitter := NewSplitter(keyBuilder, hashRing)

	timeseries := []TimeSeries{
		{Labels: []Label{{Name: "__name__", Value: "metric_a"}}, Samples: []Sample{{Value: 1.0}}},
		{Labels: []Label{{Name: "__name__", Value: "metric_b"}}, Samples: []Sample{{Value: 2.0}}},
		{Labels: []Label{{Name: "__name__", Value: "metric_c"}}, Samples: []Sample{{Value: 3.0}}},
	}

	result := splitter.SplitTimeseries(timeseries)

	// Count total
	total := 0
	for _, ts := range result {
		total += len(ts)
	}

	if total != 3 {
		t.Errorf("SplitTimeseries() total = %d, want 3", total)
	}
}

func TestSplitter_GetEndpointForTimeSeries(t *testing.T) {
	hashRing := sharding.NewHashRing(10)
	hashRing.UpdateEndpoints([]string{"endpoint1:9090"})

	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{}})
	splitter := NewSplitter(keyBuilder, hashRing)

	ts := &TimeSeries{
		Labels:  []Label{{Name: "__name__", Value: "test_metric"}},
		Samples: []Sample{{Value: 1.0, Timestamp: 1000}},
	}

	endpoint := splitter.GetEndpointForTimeSeries(ts)
	if endpoint != "endpoint1:9090" {
		t.Errorf("GetEndpointForTimeSeries() = %q, want 'endpoint1:9090'", endpoint)
	}

	// Nil time series
	endpoint = splitter.GetEndpointForTimeSeries(nil)
	if endpoint != "" {
		t.Errorf("GetEndpointForTimeSeries(nil) = %q, want empty", endpoint)
	}
}

func TestSplitter_GetEndpoints(t *testing.T) {
	hashRing := sharding.NewHashRing(10)
	hashRing.UpdateEndpoints([]string{"ep2", "ep1", "ep3"})

	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{}})
	splitter := NewSplitter(keyBuilder, hashRing)

	endpoints := splitter.GetEndpoints()

	if len(endpoints) != 3 {
		t.Errorf("GetEndpoints() returned %d endpoints, want 3", len(endpoints))
	}
}

func TestSplitter_UpdateEndpoints(t *testing.T) {
	hashRing := sharding.NewHashRing(10)
	hashRing.UpdateEndpoints([]string{"ep1"})

	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{}})
	splitter := NewSplitter(keyBuilder, hashRing)

	if len(splitter.GetEndpoints()) != 1 {
		t.Error("Initial endpoints count wrong")
	}

	splitter.UpdateEndpoints([]string{"ep1", "ep2", "ep3"})

	if len(splitter.GetEndpoints()) != 3 {
		t.Errorf("After update, endpoints count = %d, want 3", len(splitter.GetEndpoints()))
	}
}

func TestSplitter_ConsistentHashing(t *testing.T) {
	hashRing := sharding.NewHashRing(100)
	hashRing.UpdateEndpoints([]string{"ep1", "ep2", "ep3"})

	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service"}})
	splitter := NewSplitter(keyBuilder, hashRing)

	ts := &TimeSeries{
		Labels: []Label{
			{Name: "__name__", Value: "http_requests"},
			{Name: "service", Value: "api"},
		},
	}

	// Same time series should always go to same endpoint
	endpoint1 := splitter.GetEndpointForTimeSeries(ts)
	endpoint2 := splitter.GetEndpointForTimeSeries(ts)
	endpoint3 := splitter.GetEndpointForTimeSeries(ts)

	if endpoint1 != endpoint2 || endpoint2 != endpoint3 {
		t.Error("Consistent hashing failed - same time series got different endpoints")
	}
}

func BenchmarkSplitter_Split(b *testing.B) {
	hashRing := sharding.NewHashRing(150)
	hashRing.UpdateEndpoints([]string{"ep1", "ep2", "ep3", "ep4", "ep5"})

	keyBuilder := NewShardKeyBuilder(ShardKeyConfig{Labels: []string{"service", "env"}})
	splitter := NewSplitter(keyBuilder, hashRing)

	req := &WriteRequest{
		Timeseries: make([]TimeSeries, 100),
	}
	for i := 0; i < 100; i++ {
		req.Timeseries[i] = TimeSeries{
			Labels: []Label{
				{Name: "__name__", Value: "http_requests"},
				{Name: "service", Value: "api"},
				{Name: "env", Value: "prod"},
			},
			Samples: []Sample{{Value: float64(i), Timestamp: int64(i)}},
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = splitter.Split(req)
	}
}
