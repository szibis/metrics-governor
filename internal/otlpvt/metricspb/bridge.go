// bridge.go — Prevents proto registration conflicts with the upstream
// go.opentelemetry.io/proto/otlp module. See commonpb/bridge.go for details.
//
// File name "bridge.go" sorts before "metrics.pb.go" ensuring this init runs first.
package metricspb

import (
	"reflect"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	upstream "go.opentelemetry.io/proto/otlp/metrics/v1"
)

func init() {
	File_opentelemetry_proto_metrics_v1_metrics_proto = upstream.File_opentelemetry_proto_metrics_v1_metrics_proto

	// OneofWrappers (normally set by generated init, which we bypass).
	file_opentelemetry_proto_metrics_v1_metrics_proto_msgTypes[3].OneofWrappers = []any{
		(*Metric_Gauge)(nil),
		(*Metric_Sum)(nil),
		(*Metric_Histogram)(nil),
		(*Metric_ExponentialHistogram)(nil),
		(*Metric_Summary)(nil),
	}
	file_opentelemetry_proto_metrics_v1_metrics_proto_msgTypes[9].OneofWrappers = []any{
		(*NumberDataPoint_AsDouble)(nil),
		(*NumberDataPoint_AsInt)(nil),
	}
	file_opentelemetry_proto_metrics_v1_metrics_proto_msgTypes[10].OneofWrappers = []any{}
	file_opentelemetry_proto_metrics_v1_metrics_proto_msgTypes[11].OneofWrappers = []any{}
	file_opentelemetry_proto_metrics_v1_metrics_proto_msgTypes[13].OneofWrappers = []any{
		(*Exemplar_AsDouble)(nil),
		(*Exemplar_AsInt)(nil),
	}

	// Populate MessageInfo entries with our Go types and upstream descriptors.
	type mi struct {
		idx  int
		name protoreflect.FullName
		typ  reflect.Type
	}
	for _, m := range []mi{
		{0, "opentelemetry.proto.metrics.v1.MetricsData", reflect.TypeOf((*MetricsData)(nil))},
		{1, "opentelemetry.proto.metrics.v1.ResourceMetrics", reflect.TypeOf((*ResourceMetrics)(nil))},
		{2, "opentelemetry.proto.metrics.v1.ScopeMetrics", reflect.TypeOf((*ScopeMetrics)(nil))},
		{3, "opentelemetry.proto.metrics.v1.Metric", reflect.TypeOf((*Metric)(nil))},
		{4, "opentelemetry.proto.metrics.v1.Gauge", reflect.TypeOf((*Gauge)(nil))},
		{5, "opentelemetry.proto.metrics.v1.Sum", reflect.TypeOf((*Sum)(nil))},
		{6, "opentelemetry.proto.metrics.v1.Histogram", reflect.TypeOf((*Histogram)(nil))},
		{7, "opentelemetry.proto.metrics.v1.ExponentialHistogram", reflect.TypeOf((*ExponentialHistogram)(nil))},
		{8, "opentelemetry.proto.metrics.v1.Summary", reflect.TypeOf((*Summary)(nil))},
		{9, "opentelemetry.proto.metrics.v1.NumberDataPoint", reflect.TypeOf((*NumberDataPoint)(nil))},
		{10, "opentelemetry.proto.metrics.v1.HistogramDataPoint", reflect.TypeOf((*HistogramDataPoint)(nil))},
		{11, "opentelemetry.proto.metrics.v1.ExponentialHistogramDataPoint", reflect.TypeOf((*ExponentialHistogramDataPoint)(nil))},
		{12, "opentelemetry.proto.metrics.v1.SummaryDataPoint", reflect.TypeOf((*SummaryDataPoint)(nil))},
		{13, "opentelemetry.proto.metrics.v1.Exemplar", reflect.TypeOf((*Exemplar)(nil))},
		{14, "opentelemetry.proto.metrics.v1.ExponentialHistogramDataPoint.Buckets", reflect.TypeOf((*ExponentialHistogramDataPoint_Buckets)(nil))},
		{15, "opentelemetry.proto.metrics.v1.SummaryDataPoint.ValueAtQuantile", reflect.TypeOf((*SummaryDataPoint_ValueAtQuantile)(nil))},
	} {
		if d, err := protoregistry.GlobalFiles.FindDescriptorByName(m.name); err == nil {
			file_opentelemetry_proto_metrics_v1_metrics_proto_msgTypes[m.idx].Desc = d.(protoreflect.MessageDescriptor)
			file_opentelemetry_proto_metrics_v1_metrics_proto_msgTypes[m.idx].GoReflectType = m.typ
		}
	}

	// Populate enum types.
	type ei struct {
		idx  int
		name protoreflect.FullName
		typ  reflect.Type
	}
	for _, e := range []ei{
		{0, "opentelemetry.proto.metrics.v1.AggregationTemporality", reflect.TypeOf(AggregationTemporality(0))},
		{1, "opentelemetry.proto.metrics.v1.DataPointFlags", reflect.TypeOf(DataPointFlags(0))},
	} {
		if d, err := protoregistry.GlobalFiles.FindDescriptorByName(e.name); err == nil {
			file_opentelemetry_proto_metrics_v1_metrics_proto_enumTypes[e.idx].Desc = d.(protoreflect.EnumDescriptor)
			file_opentelemetry_proto_metrics_v1_metrics_proto_enumTypes[e.idx].GoReflectType = e.typ
		}
	}
}
