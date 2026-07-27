package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/configschema"
)

// The generated schemas are committed so that a clean clone gets editor
// completion for config/*.yaml without installing anything. That only holds if
// they stay in step with the structs, so drift is a test failure rather than
// something discovered when an integrator's editor stops matching reality.
func TestGeneratedSchemasAreUpToDate(t *testing.T) {
	files, err := configschema.All()
	if err != nil {
		t.Fatalf("generating schemas: %v", err)
	}

	for _, f := range files {
		path := filepath.Join("schema", f.Name)
		committed, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s is missing; run `make schema` and commit the result", path)
			continue
		}
		if string(committed) != string(f.JSON) {
			t.Errorf("%s is out of date; run `make schema` and commit the result", path)
		}
	}
}

// Each config file points its editor at the matching schema. A missing or wrong
// header is invisible at runtime and silently costs the integrator completion.
func TestConfigFilesReferenceTheirSchema(t *testing.T) {
	files, err := configschema.All()
	if err != nil {
		t.Fatalf("generating schemas: %v", err)
	}

	for _, f := range files {
		raw, err := configFS.ReadFile("config/" + f.Describes)
		if err != nil {
			t.Errorf("reading config/%s: %v", f.Describes, err)
			continue
		}
		first, _, _ := strings.Cut(string(raw), "\n")
		want := "# yaml-language-server: $schema=../schema/" + f.Name

		if strings.TrimSpace(first) != want {
			t.Errorf("config/%s starts with %q, want %q", f.Describes, strings.TrimSpace(first), want)
		}
	}
}
