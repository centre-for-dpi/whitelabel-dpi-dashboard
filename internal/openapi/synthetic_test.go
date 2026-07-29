package openapi_test

import (
	"slices"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/openapi"
)

// The wire contracts do not use every proto kind — no bytes, no unsigned, no
// float, no self-reference — so the ones they do not use are exercised against a
// descriptor built here. It is the same reasoning as internal/jsonschema testing
// itself against a local struct: a generator is a pure function of a type, and
// the types it must survive are not only the ones this repository happens to
// declare today.
func syntheticFile(t *testing.T) protoreflect.FileDescriptor {
	t.Helper()

	str := func(s string) *string { return &s }
	i32 := func(n int32) *int32 { return &n }
	field := func(name string, num int32, kind descriptorpb.FieldDescriptorProto_Type,
		label descriptorpb.FieldDescriptorProto_Label, typeName string, optional bool,
	) *descriptorpb.FieldDescriptorProto {
		f := &descriptorpb.FieldDescriptorProto{
			Name: str(name), Number: i32(num), Type: kind.Enum(), Label: label.Enum(),
			JsonName: str(name),
		}
		if typeName != "" {
			f.TypeName = str(typeName)
		}
		if optional {
			f.Proto3Optional = proto.Bool(true)
			f.OneofIndex = i32(0)
		}
		return f
	}

	const (
		optional = descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
		repeated = descriptorpb.FieldDescriptorProto_LABEL_REPEATED
	)

	file := &descriptorpb.FileDescriptorProto{
		Name:       str("synthetic/v1/synthetic.proto"),
		Package:    str("synthetic.v1"),
		Syntax:     str("proto3"),
		Dependency: []string{"google/protobuf/duration.proto", "google/protobuf/struct.proto"},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: str("Everything"),
				Field: []*descriptorpb.FieldDescriptorProto{
					field("raw", 1, descriptorpb.FieldDescriptorProto_TYPE_BYTES, optional, "", false),
					field("count", 2, descriptorpb.FieldDescriptorProto_TYPE_UINT32, optional, "", false),
					field("ratio", 3, descriptorpb.FieldDescriptorProto_TYPE_FLOAT, optional, "", false),
					field("flag", 4, descriptorpb.FieldDescriptorProto_TYPE_BOOL, optional, "", false),
					field("window", 5, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, optional,
						".google.protobuf.Duration", false),
					// A message that contains itself: registered before its fields
					// are walked, or this recurses forever.
					field("child", 6, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, optional,
						".synthetic.v1.Everything", false),
					field("many", 7, descriptorpb.FieldDescriptorProto_TYPE_SINT64, repeated, "", false),
					// An explicitly optional 64-bit integer, so nullable has to
					// widen a type that is already a union.
					field("maybeBig", 8, descriptorpb.FieldDescriptorProto_TYPE_INT64, optional, "", true),
					// An explicitly optional message, so nullable has to widen a
					// bare reference.
					field("maybeChild", 9, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, optional,
						".synthetic.v1.Everything", true),
					// The two well-known types that carry arbitrary JSON. Nothing
					// in the wire contracts uses them; they are handled so that a
					// contract that grows one does not silently emit a reference
					// to a schema describing protobuf's own internals.
					field("blob", 10, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, optional,
						".google.protobuf.Struct", false),
					field("anything", 11, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, optional,
						".google.protobuf.Value", false),
				},
				OneofDecl: []*descriptorpb.OneofDescriptorProto{
					{Name: str("_maybeBig")}, {Name: str("_maybeChild")},
				},
			},
		},
	}
	// Proto3 optional fields each need their own synthetic oneof, in order.
	file.MessageType[0].Field[7].OneofIndex = i32(0)
	file.MessageType[0].Field[8].OneofIndex = i32(1)

	// The one well-known type the synthetic file imports, registered so the
	// descriptor can resolve it.
	files := new(protoregistry.Files)
	for _, wk := range []protoreflect.FileDescriptor{
		durationpb.File_google_protobuf_duration_proto,
		structpb.File_google_protobuf_struct_proto,
	} {
		if err := files.RegisterFile(wk); err != nil {
			t.Fatalf("registering %s: %v", wk.Path(), err)
		}
	}

	fd, err := protodesc.NewFile(file, files)
	if err != nil {
		t.Fatalf("building the synthetic descriptor: %v", err)
	}
	return fd
}

func syntheticSchemas(t *testing.T) map[string]any {
	t.Helper()
	g := openapi.New(openapi.Options{})
	g.Ref(syntheticFile(t).Messages().ByName("Everything"))
	if err := g.Err(); err != nil {
		t.Fatalf("generating: %v", err)
	}
	return g.Schemas()
}

func TestEveryScalarKindHasAJSONMapping(t *testing.T) {
	all := syntheticSchemas(t)

	for _, tc := range []struct{ field, wantType, wantFormat string }{
		{"raw", "string", "byte"},
		{"count", "integer", "int32"},
		{"ratio", "number", "float"},
		{"flag", "boolean", ""},
	} {
		p := property(t, all, "Everything", tc.field)
		if p["type"] != tc.wantType {
			t.Errorf("%s is typed %v, want %s", tc.field, p["type"], tc.wantType)
		}
		if tc.wantFormat != "" && p["format"] != tc.wantFormat {
			t.Errorf("%s has format %v, want %s", tc.field, p["format"], tc.wantFormat)
		}
	}

	// Unsigned, so the schema says so rather than admitting negatives the wire
	// cannot carry.
	if property(t, all, "Everything", "count")["minimum"] != 0 {
		t.Error("an unsigned integer does not declare a minimum")
	}
}

func TestTheWellKnownTypesKeepTheirScalarForm(t *testing.T) {
	all := syntheticSchemas(t)

	if window := property(t, all, "Everything", "window"); window["type"] != "string" {
		t.Errorf("a Duration is typed %v; on the wire it is a string", window["type"])
	}
	if blob := property(t, all, "Everything", "blob"); blob["type"] != "object" {
		t.Errorf("a Struct is typed %v", blob["type"])
	}
	// A Value is any JSON at all, so it declares no type rather than a wrong one.
	anything := property(t, all, "Everything", "anything")
	if _, typed := anything["type"]; typed {
		t.Errorf("a Value declares a type: %v", anything)
	}
	// None of the three is a reference: naming a component "Duration" that
	// resolves to a bare string tells a reader less than the string does.
	for _, field := range []string{"window", "blob", "anything"} {
		if _, isRef := property(t, all, "Everything", field)["$ref"]; isRef {
			t.Errorf("%s is a reference rather than described in place", field)
		}
	}
}

// Registered before its fields are walked, or a message containing itself
// recurses until the stack ends.
func TestASelfReferencingMessageResolves(t *testing.T) {
	all := syntheticSchemas(t)
	child := property(t, all, "Everything", "child")
	if child["$ref"] != "#/components/schemas/Everything" {
		t.Errorf("the self-reference is %v", child["$ref"])
	}
}

func TestNullableWidensBothShapesOfType(t *testing.T) {
	all := syntheticSchemas(t)

	// A union type gains null alongside what was already there.
	big := property(t, all, "Everything", "maybeBig")
	types, _ := big["type"].([]any)
	for _, want := range []string{"string", "integer", "null"} {
		if !slices.Contains(types, any(want)) {
			t.Errorf("an optional int64 is typed %v, missing %q", big["type"], want)
		}
	}

	// A reference has no type to widen, so null is expressed beside it.
	child := property(t, all, "Everything", "maybeChild")
	one, _ := child["oneOf"].([]any)
	if len(one) != 2 {
		t.Fatalf("an optional message is %v; a $ref cannot carry a type union", child)
	}
}

func TestRepeatedScalarsBecomeArrays(t *testing.T) {
	many := property(t, syntheticSchemas(t), "Everything", "many")
	if many["type"] != "array" {
		t.Errorf("a repeated field is typed %v", many["type"])
	}
	items, _ := many["items"].(map[string]any)
	if items["format"] != "int64" {
		t.Errorf("its items are %v", items)
	}
}

// Two messages with the same short name would have the document describe one and
// reference the other, which is worse than failing.
func TestAShortNameCollisionIsReported(t *testing.T) {
	g := openapi.New(openapi.Options{})
	g.Ref(syntheticFile(t).Messages().ByName("Everything"))
	g.Ref(collidingFile(t).Messages().ByName("Everything"))

	err := g.Err()
	if err == nil {
		t.Fatal("two messages called Everything were accepted")
	}
	if !strings.Contains(err.Error(), "Everything") {
		t.Errorf("the error does not name the collision: %v", err)
	}
}

func collidingFile(t *testing.T) protoreflect.FileDescriptor {
	t.Helper()
	str := func(s string) *string { return &s }
	fd, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:    str("other/v1/other.proto"),
		Package: str("other.v1"),
		Syntax:  str("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: str("Everything"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name: str("id"), Number: proto.Int32(1), JsonName: str("id"),
				Type:  descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
				Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			}},
		}},
	}, nil)
	if err != nil {
		t.Fatalf("building the colliding descriptor: %v", err)
	}
	return fd
}

func TestMarshalReportsWhatItCannotEncode(t *testing.T) {
	if _, err := openapi.Marshal(map[string]any{"x": make(chan int)}); err == nil {
		t.Error("a value encoding/json cannot render was marshalled anyway")
	}
}
