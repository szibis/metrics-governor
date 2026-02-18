package autotune

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MultiClient fans out cardinality queries to multiple CardinalitySource
// backends and merges results using a configurable strategy.
type MultiClient struct {
	sources []CardinalitySource
	merge   MergeStrategy
}

// NewMultiClient creates a multi-backend fan-out cardinality source.
func NewMultiClient(sources []CardinalitySource, merge MergeStrategy) *MultiClient {
	return &MultiClient{
		sources: sources,
		merge:   merge,
	}
}

// FetchCardinality queries all sources concurrently and merges results.
func (mc *MultiClient) FetchCardinality(ctx context.Context) (*CardinalityData, error) {
	if len(mc.sources) == 0 {
		return nil, fmt.Errorf("multi client: no sources configured")
	}

	type result struct {
		data *CardinalityData
		err  error
	}

	results := make([]result, len(mc.sources))
	var wg sync.WaitGroup

	for i, src := range mc.sources {
		wg.Add(1)
		go func(idx int, s CardinalitySource) {
			defer wg.Done()
			data, err := s.FetchCardinality(ctx)
			results[idx] = result{data: data, err: err}
		}(i, src)
	}
	wg.Wait()

	// Collect successful results. Partial failure is OK.
	var datasets []*CardinalityData
	var firstErr error
	for _, r := range results {
		if r.err != nil {
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		datasets = append(datasets, r.data)
	}

	if len(datasets) == 0 {
		return nil, fmt.Errorf("multi client: all sources failed, first error: %w", firstErr)
	}

	return mc.mergeResults(datasets), nil
}

// mergeResults merges multiple CardinalityData results using the configured strategy.
func (mc *MultiClient) mergeResults(datasets []*CardinalityData) *CardinalityData {
	switch mc.merge {
	case MergeSum:
		return mc.mergeSum(datasets)
	case MergeMax:
		return mc.mergeMax(datasets)
	default: // MergeUnion
		return mc.mergeUnion(datasets)
	}
}

// mergeSum sums series counts across all sources per metric.
func (mc *MultiClient) mergeSum(datasets []*CardinalityData) *CardinalityData {
	merged := &CardinalityData{
		FetchedAt: time.Now(),
		Backend:   BackendMulti,
	}

	metricCounts := make(map[string]int64)
	for _, d := range datasets {
		merged.TotalSeries += d.TotalSeries
		for _, m := range d.TopMetrics {
			metricCounts[m.Name] += m.SeriesCount
		}
	}

	for name, count := range metricCounts {
		merged.TopMetrics = append(merged.TopMetrics, MetricCardinality{
			Name:        name,
			SeriesCount: count,
		})
	}

	return merged
}

// mergeMax takes the max series count per metric across sources.
func (mc *MultiClient) mergeMax(datasets []*CardinalityData) *CardinalityData {
	merged := &CardinalityData{
		FetchedAt: time.Now(),
		Backend:   BackendMulti,
	}

	metricCounts := make(map[string]int64)
	for _, d := range datasets {
		if d.TotalSeries > merged.TotalSeries {
			merged.TotalSeries = d.TotalSeries
		}
		for _, m := range d.TopMetrics {
			if m.SeriesCount > metricCounts[m.Name] {
				metricCounts[m.Name] = m.SeriesCount
			}
		}
	}

	for name, count := range metricCounts {
		merged.TopMetrics = append(merged.TopMetrics, MetricCardinality{
			Name:        name,
			SeriesCount: count,
		})
	}

	return merged
}

// mergeUnion takes the union of all metrics, keeping the highest count for duplicates.
func (mc *MultiClient) mergeUnion(datasets []*CardinalityData) *CardinalityData {
	// Union is functionally the same as max for our use case.
	merged := mc.mergeMax(datasets)

	// But total series is the sum (union of distinct series across backends).
	var totalSum int64
	for _, d := range datasets {
		totalSum += d.TotalSeries
	}
	merged.TotalSeries = totalSum

	return merged
}
