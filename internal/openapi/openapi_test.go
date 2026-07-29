package openapi_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	dpiv1 "github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/gen/dpi/v1"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/openapi"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func schemas(t *testing.T, opts openapi.Options, names ...protoreflect.Name) map[string]any {
	t.Helper()
	g := openapi.New(opts)
	for _, n := range names {
		if md := dpiv1.File_dpi_v1_ingest_proto.Messages().ByName(n); md != nil {
			g.Ref(md)
			continue
		}
		md := dpiv1.File_dpi_v1_dashboard_proto.Messages().ByName(n)
		if md == nil {
			t.Fatalf("no message named %q", n)
		}
		g.Ref(md)
	}
	if err := g.Err(); err != nil {
		t.Fatalf("generating: %v", err)
	}
	return g.Schemas()
}

func property(t *testing.T, all map[string]any, message, field string) map[string]any {
	t.Helper()
	schema, ok := all[message].(map[string]any)
	if !ok {
		t.Fatalf("no schema for %s; got %v", message, keys(all))
	}
	props, _ := schema["properties"].(map[string]any)
	p, ok := props[field].(map[string]any)
	if !ok {
		t.Fatalf("%s has no property %q; got %v", message, field, keys(props))
	}
	return p
}

func keys(m map[string]any) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// The encoding rule most likely to be got wrong, and the one that breaks a
// client silently rather than loudly: a 64-bit integer does not survive a
// JavaScript number, so protojson quotes it. A generator reflecting over the
// generated Go structs would describe it as an integer and every client built
// from that would reject a valid response.
func TestSixtyFourBitIntegersAreStrings(t *testing.T) {
	all := schemas(t, openapi.Options{}, "Metrics")

	stale := property(t, all, "Metrics", "staleSeconds")
	types, _ := stale["type"].([]any)
	if !slices.Contains(types, any("string")) || !slices.Contains(types, any("integer")) {
		t.Errorf("staleSeconds is typed %v; both spellings are accepted on the way in", stale["type"])
	}
	if stale["format"] != "int64" {
		t.Errorf("staleSeconds has format %v, want int64", stale["format"])
	}

	// A 32-bit integer is not quoted, and describing it as though it were would
	// be the same mistake in the other direction.
	latency := property(t, all, "Metrics", "latencyP50Ms")
	if latency["type"] != "integer" {
		t.Errorf("latencyP50Ms is typed %v, want a plain integer", latency["type"])
	}
}

// Absence is not zero. It is the invariant the whole status model rests on: a
// missing availability reading is unknown, and a zero is a total outage.
func TestPresenceTrackedScalarsAdmitNull(t *testing.T) {
	all := schemas(t, openapi.Options{}, "Metrics")

	availability := property(t, all, "Metrics", "availability")
	types, _ := availability["type"].([]any)
	if !slices.Contains(types, any("null")) {
		t.Errorf("availability is typed %v; absent has to stay distinguishable from zero", availability["type"])
	}

	// A field without the optional keyword is not nullable, or every field would
	// be and the distinction would say nothing.
	errorRate := property(t, all, "Metrics", "errorRate")
	if errorRate["type"] != "number" {
		t.Errorf("errorRate is typed %v, want a plain number", errorRate["type"])
	}
}

func TestEnumsAreTheirNames(t *testing.T) {
	all := schemas(t, openapi.Options{}, "ErrorBucket")

	class := property(t, all, "ErrorBucket", "class")
	if class["type"] != "string" {
		t.Errorf("class is typed %v, want string", class["type"])
	}
	values, _ := class["enum"].([]any)
	if !slices.Contains(values, any("ERROR_CLASS_NETWORK")) {
		t.Errorf("class offers %v, which is not the vocabulary the wire uses", values)
	}
}

func TestTimestampsAreRFC3339Strings(t *testing.T) {
	all := schemas(t, openapi.Options{}, "Maintenance")

	until := property(t, all, "Maintenance", "until")
	if until["type"] != "string" || until["format"] != "date-time" {
		t.Errorf("until is %v/%v; a Timestamp is a string on the wire, not an object",
			until["type"], until["format"])
	}
}

func TestRepeatedAndMapFieldsKeepTheirShape(t *testing.T) {
	all := schemas(t, openapi.Options{}, "Service")

	history := property(t, all, "Service", "history")
	if history["type"] != "array" {
		t.Errorf("history is typed %v, want array", history["type"])
	}
	trends := property(t, all, "Service", "trends")
	if trends["type"] != "object" {
		t.Errorf("trends is typed %v, want object", trends["type"])
	}
	if _, ok := trends["additionalProperties"]; !ok {
		t.Error("trends does not say what its values are")
	}
}

// Mirrors the decoder: protojson rejects a field it does not recognise, so a
// typo fails loudly rather than being dropped.
func TestUnknownPropertiesAreRejectedEverywhere(t *testing.T) {
	all := schemas(t, openapi.Options{}, "IngestRequest")
	for name, schema := range all {
		s, _ := schema.(map[string]any)
		if s["additionalProperties"] != false {
			t.Errorf("%s allows properties the decoder would reject", name)
		}
	}
}

func TestDescriptionsAreAppliedAndDoNotRunTogether(t *testing.T) {
	all := schemas(t, openapi.Options{
		Descriptions: map[string]string{
			"dpi.v1.Metrics":              "One reading.",
			"dpi.v1.Metrics.staleSeconds": "How old the reading is.",
		},
	}, "Metrics")

	schema, _ := all["Metrics"].(map[string]any)
	if schema["description"] != "One reading." {
		t.Errorf("the message description is %v", schema["description"])
	}

	stale := property(t, all, "Metrics", "staleSeconds")
	desc, _ := stale["description"].(string)
	if !strings.HasPrefix(desc, "How old the reading is.") {
		t.Errorf("the field description does not lead: %q", desc)
	}
	// What a field means and how its type is encoded are two things to say, and
	// run together they read as one sentence that contradicts itself.
	if !strings.Contains(desc, "\n\n") {
		t.Errorf("the field and type notes are not separated: %q", desc)
	}
}

// proto3 has no required fields, so what an endpoint will reject a body for
// omitting cannot be read off a descriptor.
func TestRequiredComesFromTheOptions(t *testing.T) {
	all := schemas(t, openapi.Options{
		Required: map[string][]string{"dpi.v1.IngestService": {"id"}},
	}, "IngestService")

	schema, _ := all["IngestService"].(map[string]any)
	required, _ := schema["required"].([]any)
	if len(required) != 1 || required[0] != "id" {
		t.Errorf("required is %v, want [id]", required)
	}
}

func TestMarshalIsStableAndReadable(t *testing.T) {
	doc := map[string]any{"b": 1, "a": map[string]any{"z": true, "y": false}}
	first, err := openapi.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		again, err := openapi.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatal("output varies between runs")
		}
	}
	if !strings.HasSuffix(string(first), "\n") {
		t.Error("output does not end with a newline")
	}
	var back map[string]any
	if err := json.Unmarshal(first, &back); err != nil {
		t.Errorf("output is not valid JSON: %v", err)
	}
}

// A described reference is wrapped rather than annotated in place: a sibling of
// $ref is ignored by some readers, and a field whose description silently
// vanished would be worse than one that never had one.
func TestADescribedReferenceIsWrapped(t *testing.T) {
	all := schemas(t, openapi.Options{
		Descriptions: map[string]string{"dpi.v1.Service.metrics": "The current reading."},
	}, "Service")

	metrics := property(t, all, "Service", "metrics")
	if _, bare := metrics["$ref"]; bare {
		t.Error("the description sits beside a $ref, where a reader may drop it")
	}
	one, _ := metrics["allOf"].([]any)
	if len(one) != 1 {
		t.Fatalf("the reference is not wrapped: %v", metrics)
	}
	if metrics["description"] != "The current reading." {
		t.Errorf("the description is %v", metrics["description"])
	}
}

// An enum's own description leads, and the encoding note follows it, because
// what the values mean matters more than how they travel.
func TestAnEnumCarriesItsOwnDescription(t *testing.T) {
	all := schemas(t, openapi.Options{
		Descriptions: map[string]string{"dpi.v1.ErrorClass": "Whose failure it is."},
	}, "ErrorBucket")

	desc, _ := property(t, all, "ErrorBucket", "class")["description"].(string)
	if !strings.Contains(desc, "Whose failure it is.") {
		t.Errorf("the enum description is missing: %q", desc)
	}
	if !strings.Contains(desc, "always returned as the name") {
		t.Errorf("the encoding note is missing: %q", desc)
	}
}

// A field whose type carries no note of its own gets the description alone,
// rather than a stray separator with nothing after it.
func TestAPlainFieldGetsOnlyItsOwnDescription(t *testing.T) {
	all := schemas(t, openapi.Options{
		Descriptions: map[string]string{"dpi.v1.Service.id": "Stable identifier."},
	}, "Service")

	if got := property(t, all, "Service", "id")["description"]; got != "Stable identifier." {
		t.Errorf("description is %q", got)
	}
}
