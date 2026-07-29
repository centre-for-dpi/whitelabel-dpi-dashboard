// Command openapigen writes the dashboard's OpenAPI document.
//
// Run it with `make openapi`. The output is committed, so a clean clone can hand
// api/openapi.json to a code generator without running anything, the binary can
// embed and serve it, and drift between the wire contracts and what is published
// about them shows up as a readable diff rather than as an integrator's bug
// report.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/apispec"
)

// coverage:ignore -- flag parsing and os.Exit; all behaviour lives in run,
// which is covered. A test cannot enter this function without ending the test
// binary.
func main() {
	dir := flag.String("out", "api", "directory to write the document into")
	src := flag.String("src", ".", "repository root, where the worked examples are read from")
	check := flag.Bool("check", false, "verify the committed document is up to date instead of writing it")
	flag.Parse()

	if err := run(*dir, *src, *check); err != nil {
		fmt.Fprintln(os.Stderr, "openapigen:", err)
		os.Exit(1)
	}
}

func run(dir, src string, check bool) error {
	files, err := apispec.All(os.DirFS(src))
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
		return fmt.Errorf("out of date: %v\nrun `make openapi` and commit the result", stale)
	}
	return nil
}
