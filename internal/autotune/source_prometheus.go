package autotune

import "context"

// PrometheusClient fetches cardinality data from Prometheus via
// /api/v1/status/tsdb. Prometheus uses the same API format as VictoriaMetrics,
// so this is a thin wrapper that delegates to VMClient.
type PrometheusClient struct {
	vm *VMClient
}

// NewPrometheusClient creates a Prometheus cardinality source.
// Under the hood it reuses VMClient since the API format is identical.
func NewPrometheusClient(cfg SourceConfig) *PrometheusClient {
	cfg.Backend = BackendPrometheus
	return &PrometheusClient{
		vm: NewVMClient(cfg),
	}
}

// FetchCardinality fetches cardinality data from Prometheus /api/v1/status/tsdb.
func (p *PrometheusClient) FetchCardinality(ctx context.Context) (*CardinalityData, error) {
	data, err := p.vm.FetchCardinality(ctx)
	if err != nil {
		return nil, err
	}
	data.Backend = BackendPrometheus
	return data, nil
}
