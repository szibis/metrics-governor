// bridge.go — Prevents proto registration conflicts with the upstream
// go.opentelemetry.io/proto/otlp module. See commonpb/bridge.go for details.
//
// File name "bridge.go" sorts before "resource.pb.go" ensuring this init runs first.
package resourcepb

import (
	"reflect"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	upstream "go.opentelemetry.io/proto/otlp/resource/v1"
)

func init() {
	File_opentelemetry_proto_resource_v1_resource_proto = upstream.File_opentelemetry_proto_resource_v1_resource_proto

	// No OneofWrappers for Resource.

	type mi struct {
		idx  int
		name protoreflect.FullName
		typ  reflect.Type
	}
	for _, m := range []mi{
		{0, "opentelemetry.proto.resource.v1.Resource", reflect.TypeOf((*Resource)(nil))},
	} {
		if d, err := protoregistry.GlobalFiles.FindDescriptorByName(m.name); err == nil {
			file_opentelemetry_proto_resource_v1_resource_proto_msgTypes[m.idx].Desc = d.(protoreflect.MessageDescriptor)
			file_opentelemetry_proto_resource_v1_resource_proto_msgTypes[m.idx].GoReflectType = m.typ
		}
	}
}
