// Command seedgen writes the demonstration fixtures.
//
// It produces the same generated data in two different wire formats:
//
//	examples/upstream/services.json   what a PULL endpoint would serve
//	examples/push/payload.json        what a PUSH collector would send
//
// Both describe the identical set of services, which is the point: a team
// deciding how to feed the dashboard can read the two files side by side and
// pick whichever fits how their systems already work. The upstream fixture is
// deliberately shaped like somebody else's API — ratios, enum casing, its own
// field names — so that the mapping in sources.yaml has real work to do.
//
// Run it with `make seed`. The output is committed so a clean clone has a
// working demo and a worked example without running anything first.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"gopkg.in/yaml.v3"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/apimap"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	dpiv1 "github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/gen/dpi/v1"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/model"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/source/seed"
)

// anchor is the moment the demo is generated relative to.
//
// It is a fixed date rather than the current time so that the committed
// fixtures do not change on every run, which would turn `make seed` into a
// noisy diff and make the drift check meaningless.
var anchor = time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)

// The committed fixtures are an excerpt, not the whole dataset.
//
// Their job is to be READ: by someone deciding how to feed the dashboard, and
// by someone checking their own collector's output against a known-good
// example. Six services with a fortnight of history shows every field in both
// formats and fits on a screen; the full 178 services with ninety days each
// runs to several megabytes, which is worse documentation and a needless
// addition to the repository and the binary.
//
// The demo itself does not read these files. Seed mode generates the full set
// in memory from the catalogue, so the running dashboard is never limited to
// the excerpt.
const (
	excerptServices = 6
	excerptDays     = 14
)

// coverage:ignore -- flag parsing and os.Exit; the work is all in run.
func main() {
	catalogue := flag.String("catalogue", "examples/seed-catalogue.yaml", "catalogue to generate from")
	configDir := flag.String("config", "config", "configuration directory, for the thresholds")
	outDir := flag.String("out", "examples", "directory to write fixtures into")
	flag.Parse()

	if err := run(*catalogue, *configDir, *outDir); err != nil {
		fmt.Fprintln(os.Stderr, "seedgen:", err)
		os.Exit(1)
	}
}

func run(cataloguePath, configDir, outDir string) error {
	cat, err := loadCatalogue(cataloguePath)
	if err != nil {
		return err
	}

	domain, err := loadDomain(configDir)
	if err != nil {
		return err
	}

	opts := seed.DefaultOptions(anchor)
	opts.HistoryDays = excerptDays
	snap := excerpt(seed.Generate(cat, domain, opts), excerptServices)

	if err := writeUpstream(filepath.Join(outDir, "upstream", "services.json"), snap); err != nil {
		return err
	}
	if err := writePush(filepath.Join(outDir, "push", "payload.json"), snap); err != nil {
		return err
	}

	fmt.Printf("wrote an excerpt of %d services across %d categories\n",
		len(snap.Services), countCategories(snap))
	return nil
}

// excerpt picks one service of each status, then fills the rest with a spread.
//
// One of each on purpose. The awkward parts of the contract only appear on the
// unusual services: a service in maintenance is the only one carrying the
// maintenance object, and an unreported one is the only one whose uptime is
// null rather than a number. Those two are precisely what an integrator gets
// wrong, and a fixture full of healthy services would document neither.
func excerpt(snap model.Snapshot, n int) model.Snapshot {
	if n <= 0 || len(snap.Services) <= n {
		return snap
	}

	var out []model.Service
	taken := map[string]bool{}

	seenStatus := map[model.Status]bool{}
	for _, sv := range snap.Services {
		if len(out) == n {
			break
		}
		if !seenStatus[sv.Status] {
			seenStatus[sv.Status] = true
			taken[sv.ID] = true
			out = append(out, sv)
		}
	}

	// Fill any remaining room with an even spread, for category variety.
	stride := max(1, len(snap.Services)/n)
	for i := 0; len(out) < n && i < len(snap.Services); i += stride {
		if sv := snap.Services[i]; !taken[sv.ID] {
			taken[sv.ID] = true
			out = append(out, sv)
		}
	}

	// Back into catalogue order, so the fixture reads in the same sequence as
	// the catalogue it came from.
	byIndex := make([]model.Service, 0, len(out))
	for _, sv := range snap.Services {
		if taken[sv.ID] {
			byIndex = append(byIndex, sv)
		}
	}

	snap.Services = byIndex
	return snap
}

func loadCatalogue(path string) (seed.Catalogue, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return seed.Catalogue{}, fmt.Errorf("reading the catalogue: %w", err)
	}
	var cat seed.Catalogue
	if err := yaml.Unmarshal(raw, &cat); err != nil {
		return seed.Catalogue{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if len(cat.Services) == 0 {
		return seed.Catalogue{}, fmt.Errorf("%s declares no services", path)
	}
	return cat, nil
}

// loadDomain reads only what the generator needs: the thresholds, so that the
// generated statuses are decided by the same rule the dashboard publishes.
func loadDomain(dir string) (config.Domain, error) {
	raw, err := os.ReadFile(filepath.Join(dir, config.FileDomain))
	if err != nil {
		return config.Domain{}, fmt.Errorf("reading the domain config: %w", err)
	}
	var d config.Domain
	if err := yaml.Unmarshal(raw, &d); err != nil {
		return config.Domain{}, fmt.Errorf("parsing the domain config: %w", err)
	}
	return d, nil
}

func writeUpstream(path string, snap model.Snapshot) error {
	// The error is discarded because it cannot occur: the upstream types are
	// plain structs of strings, numbers and slices, with no channels, functions,
	// cyclic references or NaN floats — the only things encoding/json refuses.
	raw, _ := json.MarshalIndent(toUpstream(snap), "", "  ")
	return writeFile(path, append(raw, '\n'))
}

func writePush(path string, snap model.Snapshot) error {
	req := &dpiv1.IngestRequest{
		Mode:     dpiv1.IngestMode_INGEST_MODE_REPLACE,
		SourceId: "seedgen",
	}
	for _, sv := range snap.Services {
		req.Services = append(req.Services, apimap.ServiceToIngestProto(sv))
	}

	// Marshalled through protojson so the fixture is exactly what the ingest
	// endpoint accepts, then re-encoded through encoding/json for stable key
	// ordering — protojson varies its whitespace on purpose.
	raw, err := protojson.Marshal(req)
	// coverage:ignore -- protojson rejects invalid UTF-8 in a string field, which
	// generated data never contains. Kept rather than discarded because, unlike
	// the encoding/json calls below, it is reachable for arbitrary input.
	if err != nil {
		return fmt.Errorf("encoding the push fixture: %w", err)
	}

	// Both errors below are discarded because they cannot occur: protojson emits
	// valid JSON, and re-encoding a value that came from encoding/json cannot
	// fail.
	var canonical any
	_ = json.Unmarshal(raw, &canonical)
	pretty, _ := json.MarshalIndent(canonical, "", "  ")

	return writeFile(path, append(pretty, '\n'))
}

func writeFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d KB)\n", path, len(body)/1024)
	return nil
}

func countCategories(snap model.Snapshot) int {
	seen := map[string]bool{}
	for _, sv := range snap.Services {
		seen[sv.CategoryID] = true
	}
	return len(seen)
}
