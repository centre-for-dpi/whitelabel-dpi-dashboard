package apispec

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	dpiv1 "github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/gen/dpi/v1"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/openapi"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/transform"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// A field with no description is a field an integrator has to guess at, and the
// descriptors carry no prose of their own. Checked in both directions: an entry
// naming no field is a field that was renamed and left its documentation behind,
// still describing something that no longer exists.
func TestEveryProtoFieldIsDescribed(t *testing.T) {
	g := openapi.New(openapi.Options{})
	for _, name := range []protoreflect.Name{"IngestRequest", "IngestResponse"} {
		g.Ref(dpiv1.File_dpi_v1_ingest_proto.Messages().ByName(name))
	}
	g.Ref(dpiv1.File_dpi_v1_dashboard_proto.Messages().ByName("ListServicesResponse"))
	if err := g.Err(); err != nil {
		t.Fatalf("walking the descriptors: %v", err)
	}

	wanted := g.Described()
	table := descriptions()

	for _, key := range wanted {
		if strings.TrimSpace(table[key]) == "" {
			t.Errorf("%s has no description; add one to descriptions()", key)
		}
	}
	for key := range table {
		if !slices.Contains(wanted, key) {
			t.Errorf("descriptions() documents %q, which no message or field is called; "+
				"it was probably renamed", key)
		}
	}
}

// The committed file is diffed on every change, so a regenerated document that
// had reordered itself would make the drift check unreadable and therefore
// unread.
func TestOutputIsStableAndReadable(t *testing.T) {
	first, err := All(os.DirFS("../.."))
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	for range 5 {
		again, err := All(os.DirFS("../.."))
		if err != nil {
			t.Fatalf("generating: %v", err)
		}
		if string(again[0].JSON) != string(first[0].JSON) {
			t.Fatal("the document varies between runs")
		}
	}
	if !strings.Contains(string(first[0].JSON), "\n  ") {
		t.Error("the document is not indented")
	}
	if !strings.HasSuffix(string(first[0].JSON), "\n") {
		t.Error("the document does not end with a newline")
	}
}

func rendered(t *testing.T) map[string]any {
	t.Helper()
	files, err := All(os.DirFS("../.."))
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(files[0].JSON, &doc); err != nil {
		t.Fatalf("the document is not valid JSON: %v", err)
	}
	return doc
}

// Every reference has to resolve. A dangling one renders as a blank panel in
// whichever tool an integrator points at the file, with no clue why.
func TestEveryReferenceResolves(t *testing.T) {
	doc := rendered(t)
	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	parameters, _ := components["parameters"].(map[string]any)

	var walk func(v any)
	walk = func(v any) {
		switch t2 := v.(type) {
		case map[string]any:
			if ref, ok := t2["$ref"].(string); ok {
				name, found := strings.CutPrefix(ref, "#/components/schemas/")
				if found {
					if _, ok := schemas[name]; !ok {
						t.Errorf("%s does not resolve", ref)
					}
				} else if name, found := strings.CutPrefix(ref, "#/components/parameters/"); found {
					if _, ok := parameters[name]; !ok {
						t.Errorf("%s does not resolve", ref)
					}
				} else {
					t.Errorf("%s is not a reference this document can carry", ref)
				}
			}
			for _, child := range t2 {
				walk(child)
			}
		case []any:
			for _, child := range t2 {
				walk(child)
			}
		}
	}
	walk(doc)
}

func TestEveryOperationIdIsUnique(t *testing.T) {
	paths, _ := rendered(t)["paths"].(map[string]any)
	seen := map[string]string{}
	for path, item := range paths {
		operations, _ := item.(map[string]any)
		for method, op := range operations {
			o, _ := op.(map[string]any)
			id, _ := o["operationId"].(string)
			if id == "" {
				t.Errorf("%s %s has no operationId; a generated client would have no name for it",
					strings.ToUpper(method), path)
				continue
			}
			if where, clash := seen[id]; clash {
				t.Errorf("operationId %q is used by both %s and %s %s", id, where, strings.ToUpper(method), path)
			}
			seen[id] = strings.ToUpper(method) + " " + path
		}
	}
}

// The transform list is read from the package that implements them, so the
// document cannot offer one the mapping layer would reject — the same reason
// the layout schema takes its widget types from the registry.
func TestTheTransformListMatchesTheImplementation(t *testing.T) {
	components, _ := rendered(t)["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	step, _ := schemas["TransformStep"].(map[string]any)
	properties, _ := step["properties"].(map[string]any)
	fn, _ := properties["fn"].(map[string]any)
	listed, _ := fn["enum"].([]any)

	var got []string
	for _, v := range listed {
		got = append(got, v.(string))
	}
	if !slices.Equal(got, transform.Names()) {
		t.Errorf("the document offers %v, the mapping layer implements %v", got, transform.Names())
	}
}

// The examples are read from the committed fixtures rather than restated, so
// that one that cannot go stale is worth more than one that reads a little
// better. This proves the reading actually happened.
func TestTheExamplesComeFromTheFixtures(t *testing.T) {
	paths, _ := rendered(t)["paths"].(map[string]any)

	item, _ := paths["/api/v1/pull/preview"].(map[string]any)
	op, _ := item["post"].(map[string]any)
	body, _ := op["requestBody"].(map[string]any)
	content, _ := body["content"].(map[string]any)
	appJSON, _ := content["application/json"].(map[string]any)
	examples, _ := appJSON["examples"].(map[string]any)
	shipped, _ := examples["shipped"].(map[string]any)
	value, _ := shipped["value"].(map[string]any)
	mapped, _ := value["mapping"].(map[string]any)

	// Whatever config/sources.yaml says today, the example has to be it.
	want, err := shippedMapping(os.DirFS("../.."))
	if err != nil {
		t.Fatalf("reading the shipped mapping: %v", err)
	}
	fields, _ := mapped["map"].(map[string]any)
	if len(fields) != len(want.Map) {
		t.Errorf("the example maps %d fields, sources.yaml maps %d", len(fields), len(want.Map))
	}
	for name, path := range want.Map {
		if fields[name] != path {
			t.Errorf("the example maps %s to %v, sources.yaml maps it to %q", name, fields[name], path)
		}
	}
}

// --- what happens when the fixtures are not there ---------------------------

// The document reads its worked examples from committed files, which means it
// can fail in ways a pure generator cannot. Each failure names the file, because
// "generating the document" on its own tells whoever is holding the build
// nothing about what to do next.
func TestGeneratingWithoutTheFixturesSaysWhichIsMissing(t *testing.T) {
	good := func() fstest.MapFS {
		payload, err := os.ReadFile("../../examples/push/payload.json")
		if err != nil {
			t.Fatal(err)
		}
		upstream, err := os.ReadFile("../../examples/upstream/services.json")
		if err != nil {
			t.Fatal(err)
		}
		sources, err := os.ReadFile("../../config/sources.yaml")
		if err != nil {
			t.Fatal(err)
		}
		return fstest.MapFS{
			"examples/push/payload.json":      {Data: payload},
			"examples/upstream/services.json": {Data: upstream},
			"config/sources.yaml":             {Data: sources},
		}
	}

	// The complete set generates, so each case below fails for the one reason
	// it names rather than because the fixture set was never workable.
	if _, err := All(good()); err != nil {
		t.Fatalf("the reconstructed fixtures do not generate: %v", err)
	}

	for _, tc := range []struct{ name, file, want string }{
		{"the push payload", "examples/push/payload.json", "examples/push/payload.json"},
		{"the upstream fixture", "examples/upstream/services.json", "examples/upstream/services.json"},
		{"the sources file", "config/sources.yaml", "config/sources.yaml"},
	} {
		t.Run("missing "+tc.name, func(t *testing.T) {
			fsys := good()
			delete(fsys, tc.file)
			_, err := All(fsys)
			if err == nil {
				t.Fatal("generated a document with a missing fixture")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error does not name the file: %v", err)
			}
		})

		t.Run("unreadable "+tc.name, func(t *testing.T) {
			fsys := good()
			fsys[tc.file] = &fstest.MapFile{Data: []byte("{ this is not it")}
			if _, err := All(fsys); err == nil {
				t.Error("generated a document from a fixture that does not parse")
			}
		})
	}
}

// The example mapping is the shipped one, so a sources.yaml with no pull
// endpoint to take it from has to say so rather than emitting an empty mapping
// and letting a reader conclude that none is needed.
func TestGeneratingWithoutAPullEndpointIsReported(t *testing.T) {
	payload, err := os.ReadFile("../../examples/push/payload.json")
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := os.ReadFile("../../examples/upstream/services.json")
	if err != nil {
		t.Fatal(err)
	}

	_, err = All(fstest.MapFS{
		"examples/push/payload.json":      {Data: payload},
		"examples/upstream/services.json": {Data: upstream},
		"config/sources.yaml":             {Data: []byte("driver: seed\n")},
	})
	if err == nil {
		t.Fatal("generated a document with no mapping to show")
	}
	if !strings.Contains(err.Error(), "pull endpoint") {
		t.Errorf("the error does not say what is missing: %v", err)
	}
}

// Trimming is what keeps a thirty-kilobyte fixture readable in a renderer. It
// has to survive a document shaped differently from the one it was written for,
// because the alternative is a generator that panics on a fixture change.
func TestTrimmingSurvivesAnUnexpectedShape(t *testing.T) {
	for _, doc := range []map[string]any{
		{},                                    // nothing to trim
		{"services": "not a list"},            // the wrong type entirely
		{"services": []any{"not an object"}},  // a list of the wrong thing
		{"services": []any{map[string]any{}}}, // an object with no series
	} {
		if got := trimIngest(doc, 2, 3); got == nil {
			t.Errorf("trimming %v returned nothing", doc)
		}
	}
	for _, doc := range []map[string]any{
		{},
		{"data": "not an object"},
		{"data": map[string]any{"services": []any{"not an object"}}},
		{"data": map[string]any{"services": []any{map[string]any{}}}},
	} {
		if got := trimUpstream(doc, 2, 3); got == nil {
			t.Errorf("trimming %v returned nothing", doc)
		}
	}
}

func TestFirstNIsAlwaysAList(t *testing.T) {
	// An absent series becomes an empty list rather than a null, so the example
	// stays a valid request.
	if got := firstN(nil, 3); got == nil || len(got) != 0 {
		t.Errorf("firstN(nil) = %v, want an empty list", got)
	}
	if got := firstN([]any{1, 2}, 5); len(got) != 2 {
		t.Errorf("firstN shortened a list that was already short enough: %v", got)
	}
}
