package main

import (
	"strings"
	"testing"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/theme"
)

// The shipped configuration is the first thing every deployment sees and the
// thing they copy when customising. If it does not validate, the binary does
// not start — so it is checked here rather than discovered at `docker run`.
func TestShippedConfigIsValid(t *testing.T) {
	cfg, err := config.Load(configFS, "", func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("the shipped configuration does not validate:\n%v", err)
	}

	// Spot-check that the port of the demo's domain contract actually landed,
	// rather than validating an empty shell.
	if got := len(cfg.Domain.Taxonomy.Categories); got != 10 {
		t.Errorf("got %d categories, want the demo's 10", got)
	}
	if got := len(cfg.Domain.Taxonomy.Regions); got != 13 {
		t.Errorf("got %d regions, want the demo's 13", got)
	}
	if got := len(cfg.Domain.Taxonomy.Providers); got != 20 {
		t.Errorf("got %d providers, want the demo's 20", got)
	}
	if got := len(cfg.Domain.Metrics); got != 4 {
		t.Errorf("got %d metrics, want the demo's 4", got)
	}
	if got := len(cfg.Domain.Signals); got != 4 {
		t.Errorf("got %d signals, want the demo's 4", got)
	}
	if got := len(cfg.Domain.Periods); got != 4 {
		t.Errorf("got %d periods, want the demo's 4", got)
	}
	if cfg.Domain.OnboardedDenominator != 812 {
		t.Errorf("onboardedDenominator = %d, want 812", cfg.Domain.OnboardedDenominator)
	}
}

// Every environment reference in the shipped files must carry a default. A
// deployment running `docker run` with no environment set has to get a working
// dashboard, not a startup error.
func TestShippedConfigNeedsNoEnvironment(t *testing.T) {
	if _, err := config.Load(configFS, "", func(string) (string, bool) { return "", false }); err != nil {
		t.Fatalf("the shipped config requires environment variables that are not set:\n%v", err)
	}
}

func TestShippedFontFilesExist(t *testing.T) {
	// theme.yaml names font files by hand, so a rename would otherwise surface
	// as silently missing typography in the browser.
	cfg, err := config.Load(configFS, "", func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for _, fam := range []config.FontFamily{cfg.Theme.Fonts.Body, cfg.Theme.Fonts.Serif} {
		for _, face := range fam.Faces {
			if face.File == "" {
				continue
			}
			path := "web/static/fonts/" + face.File
			if _, err := webFS.ReadFile(path); err != nil {
				t.Errorf("theme.yaml references %q but %s is not embedded", face.File, path)
			}
		}
	}
}

func TestShippedThemeGeneratesUsableCSS(t *testing.T) {
	cfg, err := config.Load(configFS, "", func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	css := theme.CSS(cfg.Theme)

	for _, want := range []string{
		":root{",
		"--bg:#FAF9F5",
		`@media (prefers-color-scheme:dark)`,
		`:root[data-theme="dark"]{`,
		"--font-body:Outfit",
		"@font-face{",
		"/assets/fonts/outfit-latin.woff2",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("generated stylesheet is missing %q", want)
		}
	}
}
