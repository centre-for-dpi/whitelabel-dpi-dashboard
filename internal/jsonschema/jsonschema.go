// Package jsonschema derives a JSON Schema from a Go struct.
//
// The config structs are already the authoritative description of what a
// deployment may write, so the schema is generated from them rather than
// maintained beside them. Anything else drifts.
//
// The payoff is in the editor: with a `# yaml-language-server: $schema=...`
// header, an integrator editing theme.yaml gets completion, hover
// documentation and inline errors before ever starting the binary. That is the
// reason config uses JSON Schema instead of protobuf — it keeps YAML comments
// and line numbers, and needs no codegen toolchain to clone and run.
//
// Generation is a pure function of a type plus a table of constraints.
package jsonschema

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// Options describe the constraints that cannot be read off a Go type.
//
// Paths are dotted property names with slices transparent, so the direction of
// a metric inside `metrics: []` is addressed as "metrics.direction". Nothing in
// the config is deep enough for that to be ambiguous, and it keeps the tables
// legible.
type Options struct {
	Title       string
	Description string

	// Enums constrains a property to a fixed set, which is what drives editor
	// completion.
	Enums map[string][]string

	// Descriptions documents individual properties on hover.
	Descriptions map[string]string

	// Patterns constrains a string property by regular expression.
	Patterns map[string]string

	// StringTypes names Go types that should be described as strings despite
	// their underlying kind — durations being the case that matters here.
	StringTypes map[string]string // type name -> pattern
}

// Generate renders the schema for the type of v.
func Generate(v any, opts Options) ([]byte, error) {
	t := reflect.TypeOf(v)
	if t == nil {
		return nil, fmt.Errorf("cannot derive a schema from a nil value")
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("cannot derive a schema from %s; want a struct", t.Kind())
	}

	root := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
	}
	if opts.Title != "" {
		root["title"] = opts.Title
	}
	if opts.Description != "" {
		root["description"] = opts.Description
	}

	body, err := describe(t, "", opts)
	if err != nil {
		return nil, err
	}
	for k, val := range body {
		root[k] = val
	}

	// Indented and newline-terminated so the generated files diff cleanly and
	// a drifted `make schema` shows up as a readable change.
	//
	// The error is discarded because it cannot occur: root holds only strings,
	// bools, maps and slices built by this package. There are no channels,
	// functions, cyclic references or NaN floats — the only things encoding/json
	// refuses. Keeping an unreachable branch here would be dead code that no
	// test could ever justify.
	raw, _ := json.MarshalIndent(root, "", "  ")
	return append(raw, '\n'), nil
}

func describe(t reflect.Type, path string, opts Options) (map[string]any, error) {
	// A named type may be declared a string regardless of its kind: config's
	// Duration is an int64 that reads and writes as "30m".
	if pattern, ok := opts.StringTypes[t.Name()]; ok && t.Name() != "" {
		s := map[string]any{"type": "string"}
		if pattern != "" {
			s["pattern"] = pattern
		}
		return s, nil
	}

	switch t.Kind() {
	case reflect.Pointer:
		// A pointer field is optional in the sense that matters here: it can be
		// omitted or written as null, and the two are distinguishable. Metric
		// targets rely on this — a metric with no target is not one with a
		// target of zero.
		inner, err := describe(t.Elem(), path, opts)
		if err != nil {
			return nil, err
		}
		return nullable(inner), nil

	case reflect.Struct:
		return describeStruct(t, path, opts)

	case reflect.Slice, reflect.Array:
		items, err := describe(t.Elem(), path, opts)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "array", "items": items}, nil

	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("%s: only string-keyed maps can be described", path)
		}
		values, err := describe(t.Elem(), path, opts)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "object", "additionalProperties": values}, nil

	case reflect.String:
		return withConstraints(map[string]any{"type": "string"}, path, opts), nil

	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}, nil

	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}, nil

	case reflect.Interface:
		// An empty schema, which in JSON Schema means "any value". Some fields
		// are genuinely heterogeneous — a widget's options differ per widget
		// type — and claiming otherwise would either reject valid config or
		// require a central list of every variant, which is the coupling the
		// composition engine exists to remove. The editor still gets structure
		// everywhere around it.
		if t.NumMethod() == 0 {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("%s: cannot describe the non-empty interface %s", path, t)

	default:
		return nil, fmt.Errorf("%s: cannot describe Go kind %s", path, t.Kind())
	}
}

func describeStruct(t reflect.Type, path string, opts Options) (map[string]any, error) {
	props := map[string]any{}

	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := yamlName(f)
		if name == "" {
			continue
		}

		child := join(path, name)
		schema, err := describe(f.Type, child, opts)
		if err != nil {
			return nil, err
		}
		if d, ok := opts.Descriptions[child]; ok {
			schema["description"] = d
		}
		props[name] = schema
	}

	return map[string]any{
		"type":       "object",
		"properties": props,
		// Mirrors the loader, which decodes with KnownFields(true). A typo must
		// be an error in the editor for the same reason it is at startup:
		// silently ignoring `runtim:` leaves the reader wondering why their
		// setting had no effect.
		"additionalProperties": false,
	}, nil
}

// withConstraints attaches any enum or pattern declared for this path.
func withConstraints(schema map[string]any, path string, opts Options) map[string]any {
	if vals, ok := opts.Enums[path]; ok {
		anyVals := make([]any, len(vals))
		for i, v := range vals {
			anyVals[i] = v
		}
		schema["enum"] = anyVals
	}
	if p, ok := opts.Patterns[path]; ok {
		schema["pattern"] = p
	}
	return schema
}

// nullable widens a schema to admit null.
func nullable(schema map[string]any) map[string]any {
	t, ok := schema["type"].(string)
	if !ok {
		return schema
	}
	schema["type"] = []any{t, "null"}
	return schema
}

// yamlName returns the field's YAML key, or "" when the field is skipped.
func yamlName(f reflect.StructField) string {
	tag, ok := f.Tag.Lookup("yaml")
	if !ok {
		// Untagged exported fields follow yaml.v3's rule of lowercasing.
		return strings.ToLower(f.Name)
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "-" {
		return ""
	}
	if name == "" {
		return strings.ToLower(f.Name)
	}
	return name
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}
