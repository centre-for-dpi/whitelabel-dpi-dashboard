package jsonschema_test

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/jsonschema"
)

type inner struct {
	Name string `yaml:"name"`
}

type sample struct {
	Text      string            `yaml:"text"`
	Count     int               `yaml:"count"`
	Ratio     float64           `yaml:"ratio"`
	Enabled   bool              `yaml:"enabled"`
	Target    *float64          `yaml:"target"`
	Items     []inner           `yaml:"items"`
	Tokens    map[string]string `yaml:"tokens"`
	Nested    inner             `yaml:"nested"`
	Untagged  string
	OnlyOpts  string `yaml:",omitempty"`
	Skipped   string `yaml:"-"`
	unhandled string //nolint:unused // present to prove unexported fields are skipped
}

func generate(t *testing.T, v any, opts jsonschema.Options) map[string]any {
	t.Helper()
	raw, err := jsonschema.Generate(v, opts)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("generated schema is not valid JSON: %v\n%s", err, raw)
	}
	return out
}

func props(t *testing.T, schema map[string]any) map[string]any {
	t.Helper()
	p, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema has no properties object: %v", schema)
	}
	return p
}

func TestScalarKindsMapToJSONTypes(t *testing.T) {
	p := props(t, generate(t, sample{}, jsonschema.Options{}))

	for _, tc := range []struct{ field, want string }{
		{"text", "string"},
		{"count", "integer"},
		{"ratio", "number"},
		{"enabled", "boolean"},
	} {
		got := p[tc.field].(map[string]any)["type"]
		if got != tc.want {
			t.Errorf("%s: type = %v, want %q", tc.field, got, tc.want)
		}
	}
}

func TestPointerFieldsAdmitNull(t *testing.T) {
	// A metric with no target is not a metric with a target of zero, so the
	// schema has to allow the distinction the Go type makes.
	p := props(t, generate(t, sample{}, jsonschema.Options{}))

	got, ok := p["target"].(map[string]any)["type"].([]any)
	if !ok {
		t.Fatalf("target type is not a list: %v", p["target"])
	}
	if len(got) != 2 || got[0] != "number" || got[1] != "null" {
		t.Errorf("target type = %v, want [number null]", got)
	}
}

func TestCollectionsAreDescribed(t *testing.T) {
	p := props(t, generate(t, sample{}, jsonschema.Options{}))

	items := p["items"].(map[string]any)
	if items["type"] != "array" {
		t.Errorf("items type = %v, want array", items["type"])
	}
	elem := items["items"].(map[string]any)
	if elem["type"] != "object" {
		t.Errorf("array element type = %v, want object", elem["type"])
	}
	if _, ok := elem["properties"].(map[string]any)["name"]; !ok {
		t.Error("array element schema lost the struct's own properties")
	}

	tokens := p["tokens"].(map[string]any)
	if tokens["type"] != "object" {
		t.Errorf("tokens type = %v, want object", tokens["type"])
	}
	if ap, ok := tokens["additionalProperties"].(map[string]any); !ok || ap["type"] != "string" {
		t.Errorf("map value schema = %v, want a string schema", tokens["additionalProperties"])
	}
}

func TestUnknownPropertiesAreRejected(t *testing.T) {
	// This mirrors the loader's KnownFields(true). A typo must be an error in
	// the editor for the same reason it is at startup.
	schema := generate(t, sample{}, jsonschema.Options{})

	if schema["additionalProperties"] != false {
		t.Errorf("additionalProperties = %v, want false", schema["additionalProperties"])
	}
	nested := props(t, schema)["nested"].(map[string]any)
	if nested["additionalProperties"] != false {
		t.Error("nested objects allow unknown properties")
	}
}

func TestFieldNamingFollowsYAMLTags(t *testing.T) {
	p := props(t, generate(t, sample{}, jsonschema.Options{}))

	if _, ok := p["untagged"]; !ok {
		t.Error("an untagged exported field should appear lowercased, as yaml.v3 treats it")
	}
	// A tag carrying only options still leaves yaml.v3 deriving the name.
	if _, ok := p["onlyopts"]; !ok {
		t.Errorf(`a field tagged yaml:",omitempty" should keep its derived name; got %v`, keysOf(p))
	}
	if _, ok := p["Skipped"]; ok {
		t.Error(`a field tagged yaml:"-" leaked into the schema`)
	}
	if _, ok := p["skipped"]; ok {
		t.Error(`a field tagged yaml:"-" leaked into the schema`)
	}
	if _, ok := p["unhandled"]; ok {
		t.Error("an unexported field leaked into the schema")
	}
}

func TestEnumsAndDescriptionsAreAttached(t *testing.T) {
	// Enums are what make editor completion work, which is the whole reason
	// for generating these files.
	schema := generate(t, sample{}, jsonschema.Options{
		Enums:        map[string][]string{"text": {"alpha", "beta"}},
		Descriptions: map[string]string{"count": "how many"},
		Patterns:     map[string]string{"text": "^[a-z]+$"},
	})
	p := props(t, schema)

	text := p["text"].(map[string]any)
	enum, ok := text["enum"].([]any)
	if !ok || len(enum) != 2 || enum[0] != "alpha" {
		t.Errorf("enum = %v, want [alpha beta]", text["enum"])
	}
	if text["pattern"] != "^[a-z]+$" {
		t.Errorf("pattern = %v", text["pattern"])
	}
	if got := p["count"].(map[string]any)["description"]; got != "how many" {
		t.Errorf("description = %v", got)
	}
}

func TestNestedPathsAddressProperties(t *testing.T) {
	// Slices are transparent in the path, so a field inside `items: []` is
	// addressed as "items.name".
	schema := generate(t, sample{}, jsonschema.Options{
		Enums: map[string][]string{"items.name": {"one", "two"}},
	})

	elem := props(t, schema)["items"].(map[string]any)["items"].(map[string]any)
	name := props(t, elem)["name"].(map[string]any)
	if _, ok := name["enum"]; !ok {
		t.Errorf("enum did not reach items.name: %v", name)
	}
}

type duration int64

type withDuration struct {
	Timeout duration `yaml:"timeout"`
}

func TestNamedTypesCanBeDeclaredStrings(t *testing.T) {
	// config.Duration is an int64 that reads and writes as "30m"; the schema
	// has to describe what the file contains, not what Go stores.
	schema := generate(t, withDuration{}, jsonschema.Options{
		StringTypes: map[string]string{"duration": `^[0-9]+(ns|us|ms|s|m|h)$`},
	})

	timeout := props(t, schema)["timeout"].(map[string]any)
	if timeout["type"] != "string" {
		t.Errorf("timeout type = %v, want string", timeout["type"])
	}
	if timeout["pattern"] == nil {
		t.Error("declared string type lost its pattern")
	}
}

func TestNamedStringTypeWithoutPattern(t *testing.T) {
	schema := generate(t, withDuration{}, jsonschema.Options{
		StringTypes: map[string]string{"duration": ""},
	})

	timeout := props(t, schema)["timeout"].(map[string]any)
	if timeout["type"] != "string" {
		t.Errorf("timeout type = %v, want string", timeout["type"])
	}
	if _, ok := timeout["pattern"]; ok {
		t.Error("an empty pattern was emitted")
	}
}

func TestTitleAndDescription(t *testing.T) {
	schema := generate(t, sample{}, jsonschema.Options{
		Title:       "Sample",
		Description: "a sample",
	})

	if schema["title"] != "Sample" {
		t.Errorf("title = %v", schema["title"])
	}
	if schema["description"] != "a sample" {
		t.Errorf("description = %v", schema["description"])
	}
	if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %v", schema["$schema"])
	}
}

func TestOutputIsStableAndReadable(t *testing.T) {
	// The generated files are committed, so `make schema` drifting must show up
	// as a readable diff rather than a reordered blob.
	first, err := jsonschema.Generate(sample{}, jsonschema.Options{Title: "Sample"})
	if err != nil {
		t.Fatal(err)
	}
	for range 10 {
		again, err := jsonschema.Generate(sample{}, jsonschema.Options{Title: "Sample"})
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatal("schema output varies between runs")
		}
	}

	if !strings.Contains(string(first), "\n  ") {
		t.Error("output is not indented")
	}
	if !strings.HasSuffix(string(first), "\n") {
		t.Error("output does not end with a newline")
	}
}

func TestGenerateRejectsUnusableInputs(t *testing.T) {
	if _, err := jsonschema.Generate(nil, jsonschema.Options{}); err == nil {
		t.Error("nil was accepted")
	}
	if _, err := jsonschema.Generate("a string", jsonschema.Options{}); err == nil {
		t.Error("a non-struct was accepted")
	}

	type badMap struct {
		M map[int]string `yaml:"m"`
	}
	if _, err := jsonschema.Generate(badMap{}, jsonschema.Options{}); err == nil {
		t.Error("an int-keyed map was accepted")
	}

	type badKind struct {
		C chan int `yaml:"c"`
	}
	if _, err := jsonschema.Generate(badKind{}, jsonschema.Options{}); err == nil {
		t.Error("a channel field was accepted")
	}

	type badSlice struct {
		S []chan int `yaml:"s"`
	}
	if _, err := jsonschema.Generate(badSlice{}, jsonschema.Options{}); err == nil {
		t.Error("a slice of channels was accepted")
	}

	type badMapValue struct {
		M map[string]chan int `yaml:"m"`
	}
	if _, err := jsonschema.Generate(badMapValue{}, jsonschema.Options{}); err == nil {
		t.Error("a map of channels was accepted")
	}

	type badPointer struct {
		P *chan int `yaml:"p"`
	}
	if _, err := jsonschema.Generate(badPointer{}, jsonschema.Options{}); err == nil {
		t.Error("a pointer to a channel was accepted")
	}
}

func TestGenerateAcceptsAPointerToAStruct(t *testing.T) {
	if _, err := jsonschema.Generate(&sample{}, jsonschema.Options{}); err != nil {
		t.Errorf("a pointer to a struct was rejected: %v", err)
	}
}

func TestNullableOnlyWidensSimpleTypes(t *testing.T) {
	// A pointer to a struct has an object schema whose "type" is already a
	// plain string; a pointer to a slice-of-slices would not be. The widening
	// must leave anything it does not understand alone rather than corrupt it.
	type withStructPointer struct {
		P *inner `yaml:"p"`
	}
	p := props(t, generate(t, withStructPointer{}, jsonschema.Options{}))

	got := p["p"].(map[string]any)
	typ, ok := got["type"].([]any)
	if !ok || len(typ) != 2 || typ[1] != "null" {
		t.Errorf("pointer-to-struct type = %v, want [object null]", got["type"])
	}
	if _, ok := got["properties"]; !ok {
		t.Error("widening a struct pointer dropped its properties")
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestPointerToPointerIsLeftAlone(t *testing.T) {
	// nullable() widens a plain "type": "string" into a list. Applied twice it
	// would corrupt an already-widened schema, so it must leave anything it
	// does not recognise untouched.
	type doubled struct {
		P **float64 `yaml:"p"`
	}
	p := props(t, generate(t, doubled{}, jsonschema.Options{}))

	typ, ok := p["p"].(map[string]any)["type"].([]any)
	if !ok {
		t.Fatalf("type = %v, want a list", p["p"])
	}
	if len(typ) != 2 || typ[0] != "number" || typ[1] != "null" {
		t.Errorf("type = %v, want [number null] rather than a doubly-widened list", typ)
	}
}

func TestEmptyInterfaceDescribesAnyValue(t *testing.T) {
	// A widget's options are genuinely heterogeneous across widget types.
	// Claiming a fixed shape would either reject valid config or need a central
	// list of every variant, which is the coupling the layout engine removes.
	type withAny struct {
		Options map[string]any `yaml:"options"`
		Loose   any            `yaml:"loose"`
	}
	p := props(t, generate(t, withAny{}, jsonschema.Options{}))

	opts := p["options"].(map[string]any)
	if opts["type"] != "object" {
		t.Errorf("options type = %v, want object", opts["type"])
	}
	ap, ok := opts["additionalProperties"].(map[string]any)
	if !ok {
		t.Fatalf("additionalProperties = %v, want an empty schema", opts["additionalProperties"])
	}
	if len(ap) != 0 {
		t.Errorf("additionalProperties = %v, want an empty schema meaning any value", ap)
	}

	if loose, ok := p["loose"].(map[string]any); !ok || len(loose) != 0 {
		t.Errorf("a bare any field = %v, want an empty schema", p["loose"])
	}
}

func TestNonEmptyInterfaceIsRejected(t *testing.T) {
	// "Any value" is honest for an empty interface. For one with methods it
	// would be a lie: the config cannot hold arbitrary JSON there.
	type withMethods struct {
		S fmt.Stringer `yaml:"s"`
	}
	if _, err := jsonschema.Generate(withMethods{}, jsonschema.Options{}); err == nil {
		t.Error("a non-empty interface was described as any value")
	}
}
