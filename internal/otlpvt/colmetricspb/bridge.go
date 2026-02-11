// bridge.go — Prevents proto registration conflicts with the upstream
// go.opentelemetry.io/proto/otlp module. See commonpb/bridge.go for details.
//
// File name "bridge.go" sorts before "metrics_service.pb.go" ensuring this init runs first.
package colmetricspb

import (
	"reflect"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	upstream "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
)

func init() {
	File_opentelemetry_proto_collector_metrics_v1_metrics_service_proto = upstream.File_opentelemetry_proto_collector_metrics_v1_metrics_service_proto

	// No OneofWrappers for these message types.

	type mi struct {
		idx  int
		name protoreflect.FullName
		typ  reflect.Type
	}
	for _, m := range []mi{
		{0, "opentelemetry.proto.collector.metrics.v1.ExportMetricsServiceRequest", reflect.TypeOf((*ExportMetricsServiceRequest)(nil))},
		{1, "opentelemetry.proto.collector.metrics.v1.ExportMetricsServiceResponse", reflect.TypeOf((*ExportMetricsServiceResponse)(nil))},
		{2, "opentelemetry.proto.collector.metrics.v1.ExportMetricsPartialSuccess", reflect.TypeOf((*ExportMetricsPartialSuccess)(nil))},
	} {
		if d, err := protoregistry.GlobalFiles.FindDescriptorByName(m.name); err == nil {
			file_opentelemetry_proto_collector_metrics_v1_metrics_service_proto_msgTypes[m.idx].Desc = d.(protoreflect.MessageDescriptor)
			file_opentelemetry_proto_collector_metrics_v1_metrics_service_proto_msgTypes[m.idx].GoReflectType = m.typ
		}
	}
}
