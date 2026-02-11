// bridge.go — Prevents proto registration conflicts with the upstream
// go.opentelemetry.io/proto/otlp module (pulled transitively by the OTel SDK).
//
// Both our generated types and the upstream register identical proto file paths
// and message names. By importing the upstream first and copying its FileDescriptor,
// the generated init() in common.pb.go sees File_* != nil and skips Build(),
// avoiding the duplicate registration entirely.
//
// File name "bridge.go" sorts before "common.pb.go" ensuring this init runs first.
package commonpb

import (
	"reflect"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	// Force upstream init — registers descriptors in the global registry.
	upstream "go.opentelemetry.io/proto/otlp/common/v1"
)

func init() {
	// Prevent generated init from calling TypeBuilder.Build() (which re-registers).
	File_opentelemetry_proto_common_v1_common_proto = upstream.File_opentelemetry_proto_common_v1_common_proto

	// OneofWrappers (normally set by generated init, which we bypass).
	file_opentelemetry_proto_common_v1_common_proto_msgTypes[0].OneofWrappers = []any{
		(*AnyValue_StringValue)(nil),
		(*AnyValue_BoolValue)(nil),
		(*AnyValue_IntValue)(nil),
		(*AnyValue_DoubleValue)(nil),
		(*AnyValue_ArrayValue)(nil),
		(*AnyValue_KvlistValue)(nil),
		(*AnyValue_BytesValue)(nil),
	}

	// Populate MessageInfo entries with our Go types and upstream descriptors.
	type mi struct {
		idx  int
		name protoreflect.FullName
		typ  reflect.Type
	}
	for _, m := range []mi{
		{0, "opentelemetry.proto.common.v1.AnyValue", reflect.TypeOf((*AnyValue)(nil))},
		{1, "opentelemetry.proto.common.v1.ArrayValue", reflect.TypeOf((*ArrayValue)(nil))},
		{2, "opentelemetry.proto.common.v1.KeyValueList", reflect.TypeOf((*KeyValueList)(nil))},
		{3, "opentelemetry.proto.common.v1.KeyValue", reflect.TypeOf((*KeyValue)(nil))},
		{4, "opentelemetry.proto.common.v1.InstrumentationScope", reflect.TypeOf((*InstrumentationScope)(nil))},
		{5, "opentelemetry.proto.common.v1.EntityRef", reflect.TypeOf((*EntityRef)(nil))},
	} {
		if d, err := protoregistry.GlobalFiles.FindDescriptorByName(m.name); err == nil {
			file_opentelemetry_proto_common_v1_common_proto_msgTypes[m.idx].Desc = d.(protoreflect.MessageDescriptor)
			file_opentelemetry_proto_common_v1_common_proto_msgTypes[m.idx].GoReflectType = m.typ
		}
	}
}
