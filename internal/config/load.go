package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Load assembles a bundle from embedded defaults, overlays any same-named files
// found in dir, expands environment references, and parses the result.
//
// Overlay is whole-file, not deep-merge. A deployment that supplies domain.yaml
// replaces the shipped one outright. Deep-merging config is a well-known source
// of "why is this value still set" confusion, and whole-file replacement means
// the file on disk is exactly what is in effect.
//
// alsoKnown names config files this package does not parse but which
// legitimately live in the same directory — layout.yaml and sources.yaml are
// read by their own packages. Without it the overlay would reject them, and a
// deployment overriding its layout could not also override its brand.
//
// dir may be empty, in which case only the defaults are used.
func Load(defaults fs.FS, dir string, lookupEnv func(string) (string, bool), alsoKnown ...string) (Config, error) {
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}

	b := make(Bundle, len(requiredFiles))
	for _, name := range requiredFiles {
		raw, err := fs.ReadFile(defaults, defaultPath(name))
		if err != nil {
			return Config{}, fmt.Errorf("reading embedded default %s: %w", name, err)
		}
		b[name] = raw
	}

	if dir != "" {
		if err := overlay(b, dir, alsoKnown); err != nil {
			return Config{}, err
		}
	}

	for name, raw := range b {
		expanded, err := ExpandEnv(raw, lookupEnv)
		if err != nil {
			return Config{}, Errors{{File: name, Msg: err.Error()}}
		}
		b[name] = expanded
	}

	return Parse(b)
}

// defaultPath maps a bundle name to its location inside the embedded tree.
func defaultPath(name string) string { return "config/" + name }

// overlay replaces bundle entries with same-named files from dir.
//
// Unrecognised .yaml files are an error rather than a shrug: a deployment that
// writes `domains.yaml` has almost certainly meant `domain.yaml`, and silently
// ignoring it would leave them editing a file with no effect.
func overlay(b Bundle, dir string, alsoKnown []string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading config directory %s: %w", dir, err)
	}

	var errs Errors
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") && !strings.HasSuffix(name, ".json") {
			continue
		}

		key := canonicalName(name)
		if slices.Contains(alsoKnown, key) {
			// Parsed by another package, so it is left alone rather than
			// rejected.
			continue
		}
		if _, known := b[key]; !known {
			errs = append(errs, Error{
				File: name,
				Msg: fmt.Sprintf("unrecognised config file in %s; expected one of %v",
					dir, append(slices.Clone(requiredFiles), alsoKnown...)),
			})
			continue
		}

		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			errs = append(errs, Error{File: name, Msg: err.Error()})
			continue
		}
		b[key] = raw
	}
	return errs.Err()
}

// canonicalName maps app.yml or app.json onto the bundle's app.yaml key, so a
// deployment is not forced to adopt one extension. yaml.v3 parses JSON too.
func canonicalName(name string) string {
	base := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(name, ".yaml"), ".yml"), ".json")
	return base + ".yaml"
}
