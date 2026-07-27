package configschema

import (
	"encoding/json"
	"regexp"
	"slices"
	"testing"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/jsonschema"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/layout"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/widget"
)

func TestAllCoversEveryConfigFile(t *testing.T) {
	files, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}

	// Every file the loader requires must have a schema, or an integrator gets
	// completion for some of their config and silence for the rest.
	want := []string{config.FileApp, config.FileBrand, config.FileDomain, config.FileTheme, config.FileIcons, layout.FileLayout}
	var got []string
	for _, f := range files {
		got = append(got, f.Describes)
		if !json.Valid(f.JSON) {
			t.Errorf("%s is not valid JSON", f.Name)
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("schemas describe %v, want %v", got, want)
	}
}

func decode(t *testing.T, name string) map[string]any {
	t.Helper()
	files, err := All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	for _, f := range files {
		if f.Describes == name {
			var out map[string]any
			if err := json.Unmarshal(f.JSON, &out); err != nil {
				t.Fatalf("%s: %v", f.Name, err)
			}
			return out
		}
	}
	t.Fatalf("no schema describes %s", name)
	return nil
}

func at(t *testing.T, schema map[string]any, path ...string) map[string]any {
	t.Helper()
	cur := schema
	for _, p := range path {
		props, ok := cur["properties"].(map[string]any)
		if !ok {
			// Step through an array's element schema transparently.
			if items, isArr := cur["items"].(map[string]any); isArr {
				cur = items
				props, ok = cur["properties"].(map[string]any)
			}
			if !ok {
				t.Fatalf("no properties at %q while looking for %v", p, path)
			}
		}
		next, ok := props[p].(map[string]any)
		if !ok {
			t.Fatalf("no property %q in %v", p, path)
		}
		cur = next
	}
	return cur
}

func enumOf(t *testing.T, schema map[string]any) []string {
	t.Helper()
	raw, ok := schema["enum"].([]any)
	if !ok {
		t.Fatalf("schema has no enum: %v", schema)
	}
	out := make([]string, len(raw))
	for i, v := range raw {
		out[i] = v.(string)
	}
	return out
}

func TestStorageDriverEnumMatchesTheLoader(t *testing.T) {
	// If these drift, the editor offers a driver the binary rejects at startup.
	got := enumOf(t, at(t, decode(t, config.FileApp), "storage", "driver"))
	want := []string{config.DriverSQLite, config.DriverPostgres, config.DriverMySQL, config.DriverMariaDB, config.DriverMemory}

	if !slices.Equal(got, want) {
		t.Errorf("driver enum = %v, want %v", got, want)
	}
}

func TestStatusEnumsMatchTheFixedVocabulary(t *testing.T) {
	domain := decode(t, config.FileDomain)

	for _, path := range [][]string{
		{"thresholds", "evaluationOrder"},
		{"statusModel", "order"},
	} {
		// These are arrays of statuses; the enum lives on the element schema.
		arr := at(t, domain, path...)
		items, ok := arr["items"].(map[string]any)
		if !ok {
			t.Fatalf("%v is not an array schema: %v", path, arr)
		}
		if got := enumOf(t, items); !slices.Equal(got, config.Statuses) {
			t.Errorf("%v enum = %v, want %v", path, got, config.Statuses)
		}
	}
}

func TestMetricEnumsMatchTheLoader(t *testing.T) {
	domain := decode(t, config.FileDomain)

	got := enumOf(t, at(t, domain, "metrics", "field"))
	want := []string{config.FieldAvailability, config.FieldErrorRate, config.FieldLatencyP50, config.FieldVolume}
	if !slices.Equal(got, want) {
		t.Errorf("field enum = %v, want %v", got, want)
	}

	got = enumOf(t, at(t, domain, "metrics", "direction"))
	want = []string{config.DirectionHigherIsBetter, config.DirectionLowerIsBetter, config.DirectionNeutral}
	if !slices.Equal(got, want) {
		t.Errorf("direction enum = %v, want %v", got, want)
	}

	got = enumOf(t, at(t, domain, "metrics", "unit"))
	want = []string{config.UnitPercent, config.UnitMillisecond, config.UnitCount}
	if !slices.Equal(got, want) {
		t.Errorf("unit enum = %v, want %v", got, want)
	}
}

func TestDurationPatternAcceptsWhatTheLoaderAccepts(t *testing.T) {
	re := regexp.MustCompile(durationPattern)

	// The editor must not flag a value the binary would happily parse.
	for _, ok := range []string{"30m", "15s", "1h30m", "500ms", "1.5s", "60s", "100ns"} {
		if !re.MatchString(ok) {
			t.Errorf("pattern rejects %q, which time.ParseDuration accepts", ok)
		}
	}
	// And it must flag the bare number the loader deliberately refuses.
	for _, bad := range []string{"30", "", "m", "abc", "30 m"} {
		if re.MatchString(bad) {
			t.Errorf("pattern accepts %q, which the loader rejects", bad)
		}
	}
}

func TestDurationFieldsAreDescribedAsStrings(t *testing.T) {
	got := at(t, decode(t, config.FileApp), "storage", "connMaxLifetime")

	if got["type"] != "string" {
		t.Errorf("connMaxLifetime type = %v, want string", got["type"])
	}
	if got["pattern"] != durationPattern {
		t.Errorf("connMaxLifetime pattern = %v", got["pattern"])
	}
}

func TestUnknownPropertiesAreRejectedEverywhere(t *testing.T) {
	// Mirrors the loader's strict decoding, so a typo is caught in the editor.
	for _, name := range []string{config.FileApp, config.FileBrand, config.FileDomain, config.FileTheme, config.FileIcons, layout.FileLayout} {
		if got := decode(t, name)["additionalProperties"]; got != false {
			t.Errorf("%s: additionalProperties = %v, want false", name, got)
		}
	}
}

func TestRenderReportsUndescribableTypes(t *testing.T) {
	// The error path is for whoever later adds a field config cannot describe.
	type undescribable struct {
		Ch chan int `yaml:"ch"`
	}

	_, err := render([]spec{{"bad.schema.json", "bad.yaml", undescribable{}, jsonschema.Options{}}})
	if err == nil {
		t.Fatal("an undescribable type was accepted")
	}
	if !regexp.MustCompile(`bad\.schema\.json`).MatchString(err.Error()) {
		t.Errorf("error does not name the schema being generated: %v", err)
	}
}

func TestLayoutSchemaOffersEveryWidgetType(t *testing.T) {
	// The enum is what gives an integrator completion on `type:` in
	// layout.yaml. If it drifts from the registry, the editor offers widgets
	// the binary rejects at startup.
	got := enumOf(t, at(t, decode(t, layout.FileLayout),
		"pages", "sections", "widgets", "type"))

	if !slices.Equal(got, widget.Default().Types()) {
		t.Errorf("widget type enum = %v,\nwant %v", got, widget.Default().Types())
	}
}

func TestLayoutSchemaOffersEveryBindSource(t *testing.T) {
	got := enumOf(t, at(t, decode(t, layout.FileLayout),
		"drawer", "tabs", "widgets", "bind", "source"))

	if !slices.Equal(got, widget.Sources()) {
		t.Errorf("bind source enum = %v,\nwant %v", got, widget.Sources())
	}
}

func TestWidgetOptionsAcceptAnyValue(t *testing.T) {
	// Options differ per widget type, so the schema cannot fix their shape
	// without a central list of every variant.
	opts := at(t, decode(t, layout.FileLayout), "pages", "sections", "widgets", "options")

	if opts["type"] != "object" {
		t.Errorf("options type = %v, want object", opts["type"])
	}
	if ap, ok := opts["additionalProperties"].(map[string]any); !ok || len(ap) != 0 {
		t.Errorf("additionalProperties = %v, want an empty schema", opts["additionalProperties"])
	}
}
