package sharding

import (
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// MetricsSplitter splits ResourceMetrics by shard key for routing to different endpoints.
type MetricsSplitter struct {
	keyBuilder *ShardKeyBuilder
	hashRing   *HashRing
}

// NewMetricsSplitter creates a new metrics splitter.
func NewMetricsSplitter(keyBuilder *ShardKeyBuilder, ring *HashRing) *MetricsSplitter {
	return &MetricsSplitter{
		keyBuilder: keyBuilder,
		hashRing:   ring,
	}
}

// Split divides ResourceMetrics by shard, returning a map of endpoint to ResourceMetrics.
// Each datapoint is routed independently based on its shard key.
// The output preserves the Resource and Scope structure but only contains the datapoints
// assigned to that shard.
func (s *MetricsSplitter) Split(rms []*metricspb.ResourceMetrics) map[string][]*metricspb.ResourceMetrics {
	if len(rms) == 0 || s.hashRing.IsEmpty() {
		return nil
	}

	result := make(map[string][]*metricspb.ResourceMetrics)

	for _, rm := range rms {
		if rm == nil {
			continue
		}

		// Extract resource attributes for merging with datapoint attributes
		resourceAttrs := ExtractAttributes(rm.GetResource().GetAttributes())

		// Track which endpoints have data from this resource
		endpointToRM := make(map[string]*metricspb.ResourceMetrics)

		for _, sm := range rm.GetScopeMetrics() {
			if sm == nil {
				continue
			}

			for _, metric := range sm.GetMetrics() {
				if metric == nil {
					continue
				}

				// Split this metric's datapoints by endpoint
				s.splitMetric(metric, resourceAttrs, rm.GetResource(), sm.GetScope(), sm.GetSchemaUrl(), endpointToRM)
			}
		}

		// Add completed ResourceMetrics to result
		for endpoint, splitRM := range endpointToRM {
			result[endpoint] = append(result[endpoint], splitRM)
		}
	}

	return result
}

// splitMetric splits a single metric's datapoints across endpoints.
func (s *MetricsSplitter) splitMetric(
	metric *metricspb.Metric,
	resourceAttrs map[string]string,
	resource *resourcepb.Resource,
	scope *commonpb.InstrumentationScope,
	schemaUrl string,
	endpointToRM map[string]*metricspb.ResourceMetrics,
) {
	metricName := metric.GetName()

	switch data := metric.GetData().(type) {
	case *metricspb.Metric_Gauge:
		if data.Gauge != nil {
			s.splitGauge(metricName, data.Gauge, metric, resourceAttrs, resource, scope, schemaUrl, endpointToRM)
		}
	case *metricspb.Metric_Sum:
		if data.Sum != nil {
			s.splitSum(metricName, data.Sum, metric, resourceAttrs, resource, scope, schemaUrl, endpointToRM)
		}
	case *metricspb.Metric_Histogram:
		if data.Histogram != nil {
			s.splitHistogram(metricName, data.Histogram, metric, resourceAttrs, resource, scope, schemaUrl, endpointToRM)
		}
	case *metricspb.Metric_ExponentialHistogram:
		if data.ExponentialHistogram != nil {
			s.splitExponentialHistogram(metricName, data.ExponentialHistogram, metric, resourceAttrs, resource, scope, schemaUrl, endpointToRM)
		}
	case *metricspb.Metric_Summary:
		if data.Summary != nil {
			s.splitSummary(metricName, data.Summary, metric, resourceAttrs, resource, scope, schemaUrl, endpointToRM)
		}
	}
}

func (s *MetricsSplitter) splitGauge(
	metricName string,
	gauge *metricspb.Gauge,
	metric *metricspb.Metric,
	resourceAttrs map[string]string,
	resource *resourcepb.Resource,
	scope *commonpb.InstrumentationScope,
	schemaUrl string,
	endpointToRM map[string]*metricspb.ResourceMetrics,
) {
	// Group datapoints by endpoint
	endpointToDatapoints := make(map[string][]*metricspb.NumberDataPoint)

	for _, dp := range gauge.GetDataPoints() {
		dpAttrs := ExtractAttributes(dp.GetAttributes())
		merged := MergeAttributes(resourceAttrs, dpAttrs)
		shardKey := s.keyBuilder.BuildKey(metricName, merged)
		endpoint := s.hashRing.GetEndpoint(shardKey)
		endpointToDatapoints[endpoint] = append(endpointToDatapoints[endpoint], dp)
	}

	// Create metrics for each endpoint
	for endpoint, datapoints := range endpointToDatapoints {
		sm := s.getOrCreateScopeMetrics(endpoint, resource, scope, schemaUrl, endpointToRM)
		sm.Metrics = append(sm.Metrics, &metricspb.Metric{
			Name:        metric.Name,
			Description: metric.Description,
			Unit:        metric.Unit,
			Metadata:    metric.Metadata,
			Data: &metricspb.Metric_Gauge{
				Gauge: &metricspb.Gauge{
					DataPoints: datapoints,
				},
			},
		})
	}
}

func (s *MetricsSplitter) splitSum(
	metricName string,
	sum *metricspb.Sum,
	metric *metricspb.Metric,
	resourceAttrs map[string]string,
	resource *resourcepb.Resource,
	scope *commonpb.InstrumentationScope,
	schemaUrl string,
	endpointToRM map[string]*metricspb.ResourceMetrics,
) {
	endpointToDatapoints := make(map[string][]*metricspb.NumberDataPoint)

	for _, dp := range sum.GetDataPoints() {
		dpAttrs := ExtractAttributes(dp.GetAttributes())
		merged := MergeAttributes(resourceAttrs, dpAttrs)
		shardKey := s.keyBuilder.BuildKey(metricName, merged)
		endpoint := s.hashRing.GetEndpoint(shardKey)
		endpointToDatapoints[endpoint] = append(endpointToDatapoints[endpoint], dp)
	}

	for endpoint, datapoints := range endpointToDatapoints {
		sm := s.getOrCreateScopeMetrics(endpoint, resource, scope, schemaUrl, endpointToRM)
		sm.Metrics = append(sm.Metrics, &metricspb.Metric{
			Name:        metric.Name,
			Description: metric.Description,
			Unit:        metric.Unit,
			Metadata:    metric.Metadata,
			Data: &metricspb.Metric_Sum{
				Sum: &metricspb.Sum{
					DataPoints:             datapoints,
					AggregationTemporality: sum.AggregationTemporality,
					IsMonotonic:            sum.IsMonotonic,
				},
			},
		})
	}
}

func (s *MetricsSplitter) splitHistogram(
	metricName string,
	histogram *metricspb.Histogram,
	metric *metricspb.Metric,
	resourceAttrs map[string]string,
	resource *resourcepb.Resource,
	scope *commonpb.InstrumentationScope,
	schemaUrl string,
	endpointToRM map[string]*metricspb.ResourceMetrics,
) {
	endpointToDatapoints := make(map[string][]*metricspb.HistogramDataPoint)

	for _, dp := range histogram.GetDataPoints() {
		dpAttrs := ExtractAttributes(dp.GetAttributes())
		merged := MergeAttributes(resourceAttrs, dpAttrs)
		shardKey := s.keyBuilder.BuildKey(metricName, merged)
		endpoint := s.hashRing.GetEndpoint(shardKey)
		endpointToDatapoints[endpoint] = append(endpointToDatapoints[endpoint], dp)
	}

	for endpoint, datapoints := range endpointToDatapoints {
		sm := s.getOrCreateScopeMetrics(endpoint, resource, scope, schemaUrl, endpointToRM)
		sm.Metrics = append(sm.Metrics, &metricspb.Metric{
			Name:        metric.Name,
			Description: metric.Description,
			Unit:        metric.Unit,
			Metadata:    metric.Metadata,
			Data: &metricspb.Metric_Histogram{
				Histogram: &metricspb.Histogram{
					DataPoints:             datapoints,
					AggregationTemporality: histogram.AggregationTemporality,
				},
			},
		})
	}
}

func (s *MetricsSplitter) splitExponentialHistogram(
	metricName string,
	histogram *metricspb.ExponentialHistogram,
	metric *metricspb.Metric,
	resourceAttrs map[string]string,
	resource *resourcepb.Resource,
	scope *commonpb.InstrumentationScope,
	schemaUrl string,
	endpointToRM map[string]*metricspb.ResourceMetrics,
) {
	endpointToDatapoints := make(map[string][]*metricspb.ExponentialHistogramDataPoint)

	for _, dp := range histogram.GetDataPoints() {
		dpAttrs := ExtractAttributes(dp.GetAttributes())
		merged := MergeAttributes(resourceAttrs, dpAttrs)
		shardKey := s.keyBuilder.BuildKey(metricName, merged)
		endpoint := s.hashRing.GetEndpoint(shardKey)
		endpointToDatapoints[endpoint] = append(endpointToDatapoints[endpoint], dp)
	}

	for endpoint, datapoints := range endpointToDatapoints {
		sm := s.getOrCreateScopeMetrics(endpoint, resource, scope, schemaUrl, endpointToRM)
		sm.Metrics = append(sm.Metrics, &metricspb.Metric{
			Name:        metric.Name,
			Description: metric.Description,
			Unit:        metric.Unit,
			Metadata:    metric.Metadata,
			Data: &metricspb.Metric_ExponentialHistogram{
				ExponentialHistogram: &metricspb.ExponentialHistogram{
					DataPoints:             datapoints,
					AggregationTemporality: histogram.AggregationTemporality,
				},
			},
		})
	}
}

func (s *MetricsSplitter) splitSummary(
	metricName string,
	summary *metricspb.Summary,
	metric *metricspb.Metric,
	resourceAttrs map[string]string,
	resource *resourcepb.Resource,
	scope *commonpb.InstrumentationScope,
	schemaUrl string,
	endpointToRM map[string]*metricspb.ResourceMetrics,
) {
	endpointToDatapoints := make(map[string][]*metricspb.SummaryDataPoint)

	for _, dp := range summary.GetDataPoints() {
		dpAttrs := ExtractAttributes(dp.GetAttributes())
		merged := MergeAttributes(resourceAttrs, dpAttrs)
		shardKey := s.keyBuilder.BuildKey(metricName, merged)
		endpoint := s.hashRing.GetEndpoint(shardKey)
		endpointToDatapoints[endpoint] = append(endpointToDatapoints[endpoint], dp)
	}

	for endpoint, datapoints := range endpointToDatapoints {
		sm := s.getOrCreateScopeMetrics(endpoint, resource, scope, schemaUrl, endpointToRM)
		sm.Metrics = append(sm.Metrics, &metricspb.Metric{
			Name:        metric.Name,
			Description: metric.Description,
			Unit:        metric.Unit,
			Metadata:    metric.Metadata,
			Data: &metricspb.Metric_Summary{
				Summary: &metricspb.Summary{
					DataPoints: datapoints,
				},
			},
		})
	}
}

// getOrCreateScopeMetrics gets or creates the ScopeMetrics for an endpoint.
func (s *MetricsSplitter) getOrCreateScopeMetrics(
	endpoint string,
	resource *resourcepb.Resource,
	scope *commonpb.InstrumentationScope,
	schemaUrl string,
	endpointToRM map[string]*metricspb.ResourceMetrics,
) *metricspb.ScopeMetrics {
	rm, exists := endpointToRM[endpoint]
	if !exists {
		rm = &metricspb.ResourceMetrics{
			Resource:     copyResource(resource),
			ScopeMetrics: []*metricspb.ScopeMetrics{},
		}
		endpointToRM[endpoint] = rm
	}

	// Find or create matching ScopeMetrics
	for _, sm := range rm.ScopeMetrics {
		if scopesMatch(sm.Scope, scope) && sm.SchemaUrl == schemaUrl {
			return sm
		}
	}

	// Create new ScopeMetrics
	newSM := &metricspb.ScopeMetrics{
		Scope:     copyScope(scope),
		SchemaUrl: schemaUrl,
		Metrics:   []*metricspb.Metric{},
	}
	rm.ScopeMetrics = append(rm.ScopeMetrics, newSM)
	return newSM
}

// copyResource creates a shallow copy of the resource.
func copyResource(r *resourcepb.Resource) *resourcepb.Resource {
	if r == nil {
		return nil
	}
	return &resourcepb.Resource{
		Attributes:             r.Attributes, // Keep reference to attributes (immutable)
		DroppedAttributesCount: r.DroppedAttributesCount,
	}
}

// copyScope creates a shallow copy of the scope.
func copyScope(s *commonpb.InstrumentationScope) *commonpb.InstrumentationScope {
	if s == nil {
		return nil
	}
	return &commonpb.InstrumentationScope{
		Name:                   s.Name,
		Version:                s.Version,
		Attributes:             s.Attributes, // Keep reference
		DroppedAttributesCount: s.DroppedAttributesCount,
	}
}

// scopesMatch compares two instrumentation scopes for equality.
func scopesMatch(a, b *commonpb.InstrumentationScope) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Name == b.Name && a.Version == b.Version
}

// CountDatapoints counts total datapoints across all ResourceMetrics.
func CountDatapoints(rms []*metricspb.ResourceMetrics) int {
	count := 0
	for _, rm := range rms {
		if rm == nil {
			continue
		}
		for _, sm := range rm.GetScopeMetrics() {
			if sm == nil {
				continue
			}
			for _, m := range sm.GetMetrics() {
				if m == nil {
					continue
				}
				switch data := m.GetData().(type) {
				case *metricspb.Metric_Gauge:
					if data.Gauge != nil {
						count += len(data.Gauge.GetDataPoints())
					}
				case *metricspb.Metric_Sum:
					if data.Sum != nil {
						count += len(data.Sum.GetDataPoints())
					}
				case *metricspb.Metric_Histogram:
					if data.Histogram != nil {
						count += len(data.Histogram.GetDataPoints())
					}
				case *metricspb.Metric_ExponentialHistogram:
					if data.ExponentialHistogram != nil {
						count += len(data.ExponentialHistogram.GetDataPoints())
					}
				case *metricspb.Metric_Summary:
					if data.Summary != nil {
						count += len(data.Summary.GetDataPoints())
					}
				}
			}
		}
	}
	return count
}
