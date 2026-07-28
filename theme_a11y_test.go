package main

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/a11y"
	"github.com/centre-for-dpi/whitelabel-dpi-dashboard/internal/config"
)

// This tests the shipped palette rather than the contrast machinery, which has
// its own suite against fixtures.
//
// Config validation already refuses to start on a failing palette, so in one
// sense this is redundant. It is here because the failure it guards against is
// not "the checker is broken" but "someone adjusted a colour by eye". A commit
// that darkens a border for looks and quietly drops it under 3:1 should fail in
// review, not in an audit a year later.

func shippedTheme(t *testing.T) config.Theme {
	t.Helper()

	data, err := configFS.ReadFile("config/theme.yaml")
	if err != nil {
		t.Fatalf("reading the shipped theme: %v", err)
	}
	var theme config.Theme
	if err := yaml.Unmarshal(data, &theme); err != nil {
		t.Fatalf("parsing the shipped theme: %v", err)
	}
	return theme
}

func TestShippedThemeMeetsWCAGContrast(t *testing.T) {
	theme := shippedTheme(t)

	for _, mode := range []struct {
		name   string
		tokens map[string]string
	}{
		{"light", theme.Light},
		{"dark", theme.Dark},
	} {
		if len(mode.tokens) == 0 {
			t.Fatalf("the shipped theme declares no %s tokens", mode.name)
		}
		for _, f := range a11y.Check(mode.name, mode.tokens) {
			t.Errorf("shipped palette fails WCAG: %s", f)
		}
	}
}

// Every obligation must be evaluable against the shipped palette. An obligation
// naming a token the theme does not declare is silently skipped by Check, so
// without this a typo in the contract would read as a pass.
func TestEveryObligationIsEvaluatedAgainstTheShippedTheme(t *testing.T) {
	theme := shippedTheme(t)

	for _, mode := range []struct {
		name   string
		tokens map[string]string
	}{
		{"light", theme.Light},
		{"dark", theme.Dark},
	} {
		for _, p := range a11y.Contract() {
			for _, token := range []string{p.Fg, p.Bg} {
				raw, ok := mode.tokens[token]
				if !ok {
					t.Errorf("%s: the contract names %q, which the shipped theme does not declare, so the obligation for %s is never checked",
						mode.name, token, p.Where)
					continue
				}
				if _, err := a11y.ParseColour(raw); err != nil {
					t.Errorf("%s: token %q is %q, which the contrast checker cannot read, so the obligation for %s is never checked",
						mode.name, token, raw, p.Where)
				}
			}
		}
	}
}
