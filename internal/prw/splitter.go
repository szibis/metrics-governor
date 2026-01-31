package prw

import (
	"github.com/slawomirskowron/metrics-governor/internal/sharding"
)

// Splitter splits WriteRequests by shard key for routing to different endpoints.
type Splitter struct {
	keyBuilder *ShardKeyBuilder
	hashRing   *sharding.HashRing
}

// NewSplitter creates a new PRW metrics splitter.
func NewSplitter(keyBuilder *ShardKeyBuilder, ring *sharding.HashRing) *Splitter {
	return &Splitter{
		keyBuilder: keyBuilder,
		hashRing:   ring,
	}
}

// Split divides a WriteRequest by shard, returning a map of endpoint to WriteRequest.
// Each time series is routed independently based on its shard key.
func (s *Splitter) Split(req *WriteRequest) map[string]*WriteRequest {
	if req == nil || len(req.Timeseries) == 0 || s.hashRing.IsEmpty() {
		return nil
	}

	result := make(map[string]*WriteRequest)

	for _, ts := range req.Timeseries {
		// Build shard key for this time series
		shardKey := s.keyBuilder.BuildKey(&ts)

		// Get endpoint for this shard key
		endpoint := s.hashRing.GetEndpoint(shardKey)
		if endpoint == "" {
			continue
		}

		// Get or create WriteRequest for this endpoint
		endpointReq, ok := result[endpoint]
		if !ok {
			endpointReq = &WriteRequest{
				Timeseries: make([]TimeSeries, 0),
				Metadata:   make([]MetricMetadata, 0),
			}
			result[endpoint] = endpointReq
		}

		// Add time series to the endpoint's request
		endpointReq.Timeseries = append(endpointReq.Timeseries, ts)
	}

	// Distribute metadata to all endpoints (metadata is global)
	if len(req.Metadata) > 0 {
		for _, endpointReq := range result {
			endpointReq.Metadata = req.Metadata
		}
	}

	return result
}

// SplitTimeseries splits a slice of TimeSeries by shard.
// Returns a map of endpoint to time series slice.
func (s *Splitter) SplitTimeseries(timeseries []TimeSeries) map[string][]TimeSeries {
	if len(timeseries) == 0 || s.hashRing.IsEmpty() {
		return nil
	}

	result := make(map[string][]TimeSeries)

	for _, ts := range timeseries {
		shardKey := s.keyBuilder.BuildKey(&ts)
		endpoint := s.hashRing.GetEndpoint(shardKey)
		if endpoint == "" {
			continue
		}

		result[endpoint] = append(result[endpoint], ts)
	}

	return result
}

// GetEndpointForTimeSeries returns the endpoint for a single time series.
func (s *Splitter) GetEndpointForTimeSeries(ts *TimeSeries) string {
	if ts == nil || s.hashRing.IsEmpty() {
		return ""
	}

	shardKey := s.keyBuilder.BuildKey(ts)
	return s.hashRing.GetEndpoint(shardKey)
}

// GetEndpoints returns the current list of endpoints in the hash ring.
func (s *Splitter) GetEndpoints() []string {
	return s.hashRing.GetEndpoints()
}

// UpdateEndpoints updates the endpoints in the hash ring.
func (s *Splitter) UpdateEndpoints(endpoints []string) {
	s.hashRing.UpdateEndpoints(endpoints)
}
