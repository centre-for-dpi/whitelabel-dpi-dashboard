// Command schemagen writes the JSON Schema for each configuration file.
//
// Run it with `make schema`. The output is committed, so a clean clone gets
// editor completion and inline validation for config/*.yaml without installing
// anything, and so that drift shows up as a readable diff.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/configschema"
)

// coverage:ignore -- flag parsing and os.Exit; all behaviour lives in run,
// which is covered. A test cannot enter this function without ending the test
// binary.
func main() {
	dir := flag.String("out", "schema", "directory to write schema files into")
	check := flag.Bool("check", false, "verify the committed files are up to date instead of writing them")
	flag.Parse()

	if err := run(*dir, *check); err != nil {
		fmt.Fprintln(os.Stderr, "schemagen:", err)
		os.Exit(1)
	}
}

func run(dir string, check bool) error {
	files, err := configschema.All()
	// coverage:ignore -- unreachable pass-through: All only fails for a config
	// type jsonschema cannot describe, and every current type can be. The error
	// path itself is exercised in configschema's own tests via render.
	if err != nil {
		return err
	}

	if !check {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	var stale []string
	for _, f := range files {
		path := filepath.Join(dir, f.Name)

		if check {
			existing, err := os.ReadFile(path)
			if err != nil || string(existing) != string(f.JSON) {
				stale = append(stale, path)
			}
			continue
		}

		if err := os.WriteFile(path, f.JSON, 0o644); err != nil {
			return err
		}
		fmt.Printf("wrote %s (describes %s)\n", path, f.Describes)
	}

	if len(stale) > 0 {
		return fmt.Errorf("out of date: %v\nrun `make schema` and commit the result", stale)
	}
	return nil
}
