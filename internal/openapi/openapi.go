// Package openapi turns protobuf descriptors into OpenAPI 3.1 schemas.
//
// The wire contracts are protobuf, and what travels over HTTP is protojson —
// which is not the same thing as the JSON a Go struct would produce. int64
// fields are quoted strings, enums are their names rather than their numbers,
// and a well-known Timestamp is an RFC 3339 string rather than an object with
// seconds and nanos in it. Every one of those is a detail an integrator writes
// code against, and every one is something a generator reflecting over the
// generated Go structs gets wrong.
//
// So the schemas are read from the descriptors, which are the same source
// protojson itself reads. Prose the descriptors do not carry — what a field
// means, what units it is in — comes from Options, the way internal/jsonschema
// takes its descriptions for the configuration schemas.
//
// Generation is a pure function of a set of messages plus that table. There is
// no I/O here and no knowledge of any particular endpoint; the document itself
// is assembled by internal/apispec.
package openapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sort"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// Dialect is the JSON Schema dialect OpenAPI 3.1 uses. It is the same one
// internal/jsonschema emits for the configuration files, which is why a schema
// from either package reads the same way.
const Dialect = "https://json-schema.org/draft/2020-12/schema"

// Version is the OpenAPI version the generated document declares.
const Version = "3.1.0"

// Options describe what the descriptors cannot say.
type Options struct {
	// Descriptions documents a message or one of its fields. Keys are the
	// message's full proto name — "dpi.v1.IngestService" — optionally followed
	// by a dot and the field's JSON name: "dpi.v1.IngestService.categoryId".
	//
	// Field names are the JSON ones rather than the proto ones because that is
	// what an integrator sees on the wire.
	Descriptions map[string]string

	// Required lists the fields an operation will reject a body for omitting,
	// keyed by message full name. proto3 has no required fields, so this cannot
	// be read off a descriptor: it is the endpoint's own validation, and saying
	// so is the difference between a schema that describes the wire and one that
	// describes what will be accepted.
	Required map[string][]string
}

// Generator collects the schemas a document refers to.
//
// Messages are registered as they are referenced and named by their short proto
// name, so the same message reached from two operations is described once.
type Generator struct {
	opts    Options
	schemas map[string]map[string]any
	naming  map[protoreflect.FullName]string
	// enums are inlined rather than registered — a reference named "Status"
	// pointing at a list of five strings is a hop for no gain — but they are
	// recorded so the completeness check can see them.
	enums map[protoreflect.FullName]bool
	errs  []error
}

// New returns a generator holding no schemas yet.
func New(opts Options) *Generator {
	return &Generator{
		opts:    opts,
		schemas: map[string]map[string]any{},
		naming:  map[protoreflect.FullName]string{},
		enums:   map[protoreflect.FullName]bool{},
	}
}

// Ref registers a message and returns a reference to it.
func (g *Generator) Ref(md protoreflect.MessageDescriptor) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + g.register(md)}
}

// Schemas is everything registered so far, ready to be placed under
// components.schemas.
func (g *Generator) Schemas() map[string]any {
	out := make(map[string]any, len(g.schemas))
	for name, schema := range g.schemas {
		out[name] = schema
	}
	return out
}

// Err reports every problem found, or nil.
func (g *Generator) Err() error { return errors.Join(g.errs...) }

// Described reports every description key the generator actually consumed.
//
// The test that proves no field went undocumented needs both halves: the keys
// that were wanted, and the keys the table supplies. An orphan in the table is
// as much a defect as a missing entry — it means a field was renamed and its
// documentation was left behind, describing something that no longer exists.
func (g *Generator) Described() []string {
	var out []string
	for full, name := range g.naming {
		schema := g.schemas[name]
		out = append(out, string(full))
		props, _ := schema["properties"].(map[string]any)
		for prop := range props {
			out = append(out, string(full)+"."+prop)
		}
	}
	for full := range g.enums {
		out = append(out, string(full))
	}
	sort.Strings(out)
	return out
}

func (g *Generator) register(md protoreflect.MessageDescriptor) string {
	if name, ok := g.naming[md.FullName()]; ok {
		return name
	}

	name := string(md.Name())
	if existing, clash := g.byName(name); clash && existing != md.FullName() {
		g.errs = append(g.errs, fmt.Errorf(
			"two messages are both called %q (%s and %s); the document would describe one and reference the other",
			name, existing, md.FullName()))
		return name
	}

	// Recorded before the fields are walked, so a message that contains itself
	// resolves to a reference rather than recursing forever.
	g.naming[md.FullName()] = name
	g.schemas[name] = g.message(md)
	return name
}

func (g *Generator) byName(name string) (protoreflect.FullName, bool) {
	for full, n := range g.naming {
		if n == name {
			return full, true
		}
	}
	return "", false
}

func (g *Generator) message(md protoreflect.MessageDescriptor) map[string]any {
	properties := map[string]any{}
	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		properties[fd.JSONName()] = g.field(md, fd)
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
		// Mirrors the decoder: protojson rejects a field it does not recognise,
		// so a typo fails loudly rather than being silently dropped. Saying so
		// in the schema is what lets an editor catch it before the request.
		"additionalProperties": false,
	}
	if desc := g.opts.Descriptions[string(md.FullName())]; desc != "" {
		schema["description"] = desc
	}
	if req := g.opts.Required[string(md.FullName())]; len(req) > 0 {
		sorted := append([]string(nil), req...)
		sort.Strings(sorted)
		// []any rather than []string, so every list this package emits has the
		// same Go type and a caller inspecting one need not know which it is.
		out := make([]any, 0, len(sorted))
		for _, name := range sorted {
			out = append(out, name)
		}
		schema["required"] = out
	}
	return schema
}

func (g *Generator) field(md protoreflect.MessageDescriptor, fd protoreflect.FieldDescriptor) map[string]any {
	var schema map[string]any

	switch {
	case fd.IsMap():
		schema = map[string]any{
			"type":                 "object",
			"additionalProperties": g.value(fd.MapValue()),
		}
	case fd.IsList():
		schema = map[string]any{"type": "array", "items": g.value(fd)}
	default:
		schema = g.value(fd)
	}

	// An explicit `optional` on a scalar is presence tracking, and presence is
	// the whole point of the one field it is used on: a missing availability
	// reading is not a total outage, so absent and zero must stay distinguishable
	// all the way out to the wire.
	if fd.HasOptionalKeyword() {
		schema = nullable(schema)
	}

	if desc := g.opts.Descriptions[string(md.FullName())+"."+fd.JSONName()]; desc != "" {
		schema = withDescription(schema, desc)
	}
	return schema
}

// value is one field's type, ignoring its cardinality.
func (g *Generator) value(fd protoreflect.FieldDescriptor) map[string]any {
	switch fd.Kind() {
	case protoreflect.BoolKind:
		return map[string]any{"type": "boolean"}

	case protoreflect.StringKind:
		return map[string]any{"type": "string"}

	case protoreflect.BytesKind:
		return map[string]any{"type": "string", "format": "byte",
			"description": "Base64, standard alphabet with padding."}

	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return map[string]any{"type": "integer", "format": "int32"}

	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return map[string]any{"type": "integer", "format": "int32", "minimum": 0}

	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		// The one encoding rule most likely to be got wrong. A 64-bit integer
		// does not survive a JavaScript number, so protojson quotes it: responses
		// always carry the string form, and requests may use either.
		return map[string]any{
			"type": []any{"string", "integer"}, "format": "int64",
			"description": "A 64-bit integer. Sent either quoted or bare; always returned quoted.",
		}

	case protoreflect.FloatKind:
		return map[string]any{"type": "number", "format": "float"}

	case protoreflect.DoubleKind:
		return map[string]any{"type": "number", "format": "double"}

	case protoreflect.EnumKind:
		return g.enum(fd.Enum())

	case protoreflect.MessageKind, protoreflect.GroupKind:
		if known := wellKnown(fd.Message()); known != nil {
			return known
		}
		return g.Ref(fd.Message())

	default:
		// coverage:ignore -- every one of proto's eighteen kinds is handled
		// above, so this is reachable only from a descriptor built by a future
		// protobuf release. It exists so that such a release fails loudly here
		// rather than emitting a schema with a field of no type at all.
		g.errs = append(g.errs, fmt.Errorf("%s: no JSON mapping for proto kind %s", fd.FullName(), fd.Kind()))
		return map[string]any{}
	}
}

func (g *Generator) enum(ed protoreflect.EnumDescriptor) map[string]any {
	g.enums[ed.FullName()] = true
	values := ed.Values()
	names := make([]any, 0, values.Len())
	for i := range values.Len() {
		names = append(names, string(values.Get(i).Name()))
	}
	schema := map[string]any{
		"type": "string", "enum": names,
		"description": "Sent as the name or as the number; always returned as the name.",
	}
	if desc := g.opts.Descriptions[string(ed.FullName())]; desc != "" {
		schema["description"] = desc + "\n\n" + schema["description"].(string)
	}
	return schema
}

// wellKnown maps the google.protobuf types protojson gives a scalar form.
//
// Described inline rather than as a component, because a reference named
// "Timestamp" pointing at a bare string tells a reader less than the string
// itself does.
func wellKnown(md protoreflect.MessageDescriptor) map[string]any {
	switch md.FullName() {
	case "google.protobuf.Timestamp":
		return map[string]any{"type": "string", "format": "date-time",
			"description": "RFC 3339, in UTC."}
	case "google.protobuf.Duration":
		return map[string]any{"type": "string",
			"description": `Seconds with up to nine fractional digits, suffixed "s".`}
	case "google.protobuf.Struct":
		return map[string]any{"type": "object"}
	case "google.protobuf.Value":
		return map[string]any{}
	default:
		return nil
	}
}

// nullable widens a schema's type to admit null.
//
// OpenAPI 3.1 dropped the `nullable: true` keyword of 3.0 in favour of JSON
// Schema's type unions, so this is the current spelling.
func nullable(schema map[string]any) map[string]any {
	out := maps.Clone(schema)
	switch t := out["type"].(type) {
	case string:
		out["type"] = []any{t, "null"}
	case []any:
		// No guard against widening twice: a schema is made nullable once, on the
		// way out of value(), and a defensive check here would be a branch no
		// test could ever reach.
		out["type"] = append(append([]any{}, t...), "null")
	case nil:
		// A reference or an untyped schema: null is expressed alongside it.
		if ref, ok := out["$ref"].(string); ok {
			return map[string]any{"oneOf": []any{
				map[string]any{"$ref": ref},
				map[string]any{"type": "null"},
			}}
		}
	}
	return out
}

func withDescription(schema map[string]any, desc string) map[string]any {
	// A sibling of $ref is ignored by some readers, so a described reference is
	// wrapped in allOf, which every reader honours.
	if ref, ok := schema["$ref"]; ok {
		return map[string]any{
			"allOf":       []any{map[string]any{"$ref": ref}},
			"description": desc,
		}
	}
	out := maps.Clone(schema)
	if existing, ok := out["description"].(string); ok && existing != "" {
		// A blank line, not a space. What the field means and how its type is
		// encoded are two different things to say, and run together they read as
		// one sentence that contradicts itself.
		out["description"] = desc + "\n\n" + existing
		return out
	}
	out["description"] = desc
	return out
}

// Marshal renders a document.
//
// Indented and newline-terminated so the committed file diffs cleanly, and
// through encoding/json's map handling so the key order is sorted rather than
// incidental: a regenerated document that had reordered itself would make the
// drift check unreadable and therefore unread.
func Marshal(doc map[string]any) ([]byte, error) {
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}
